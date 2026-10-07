package importer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// num builds the JSON number a decoder would have produced; a real export
// never hands the mapper a float64.
func num(text string) json.Number { return json.Number(text) }

func problems(list []Problem, storeName, field string) []Problem {
	var out []Problem
	for _, problem := range list {
		if problem.Store == storeName && (field == "" || problem.Field == field) {
			out = append(out, problem)
		}
	}
	return out
}

func requireProblem(t *testing.T, list []Problem, storeName, field, substring string) Problem {
	t.Helper()
	for _, problem := range problems(list, storeName, field) {
		if strings.Contains(problem.Message, substring) {
			return problem
		}
	}
	t.Fatalf("no problem for %s %s containing %q; have %v", storeName, field, substring, list)
	return Problem{}
}

func accountNamed(t *testing.T, m *Mapped, name string) *store.Account {
	t.Helper()
	for _, account := range m.Accounts {
		if account.Name == name {
			return account
		}
	}
	t.Fatalf("no account named %q", name)
	return nil
}

func categoryNamed(t *testing.T, m *Mapped, name string) *store.Category {
	t.Helper()
	for _, category := range m.Categories {
		if category.Name == name {
			return category
		}
	}
	t.Fatalf("no category named %q", name)
	return nil
}

func txnByStatement(t *testing.T, m *Mapped, statement string) *store.Transaction {
	t.Helper()
	for _, txn := range m.Transactions {
		if txn.StatementName == statement {
			return txn
		}
	}
	t.Fatalf("no transaction with statement name %q", statement)
	return nil
}

func TestACompleteExportMapsWithoutASingleError(t *testing.T) {
	out := mapped(t, complete(t))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.True(t, out.Report.OK())
}

func TestEveryStoreIsEitherReadOrDeclaredIgnored(t *testing.T) {
	export := complete(t)
	out := mapped(t, export)
	for _, name := range export.StoreNames(fixtureDataset) {
		_, read := out.Report.Read[name]
		_, ignored := out.Report.IgnoredStores[name]
		require.True(t, read || ignored, "store %s is neither read nor declared ignored", name)
	}
}

func TestAStoreNobodyHasWrittenAReaderForIsNotedAndLeftOut(t *testing.T) {
	export := addStore(t, complete(t), "investmentsQuotesDetailed", map[string]any{
		"q1": map[string]any{"id": "q1", "symbol": "ZZZX", "lastPrice": num("12.34")},
	})
	out := mapped(t, export)
	require.Empty(t, out.Report.Errors, out.Report.Render())
	requireProblem(t, out.Report.Notes, "investmentsQuotesDetailed", "", "not read")
	require.Empty(t, problems(out.Report.Warnings, "investmentsQuotesDetailed", ""))
}

// -- 1. the space -----------------------------------------------------

func TestTheDatasetBecomesTheSpace(t *testing.T) {
	out := mapped(t, complete(t))
	require.Equal(t, "Household", out.Space.Name)
	require.Equal(t, "USD", out.Space.PrimaryCurrency)
	require.Equal(t, "America/Denver", out.Space.Timezone)
	require.Equal(t, out.Space.ID, out.SpaceID)
}

func TestAnOwnerIsWrittenBecauseAlertRulesAreScopedToAUser(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Users, 1)
	require.Equal(t, DefaultOwnerEmail, out.Users[0].Email)
	require.Len(t, out.Memberships, 1)
	require.Equal(t, store.RoleOwner, out.Memberships[0].Role)
	require.Equal(t, out.Users[0].ID, out.AlertRules[0].UserID)
}

func TestTheSpaceNameCanBeOverriddenWithoutTouchingTheExport(t *testing.T) {
	out, err := MapExport(complete(t), "", Options{OwnerEmail: "me@example.test", SpaceName: "Ours"})
	require.NoError(t, err)
	require.Equal(t, "Ours", out.Space.Name)
	require.Equal(t, "me@example.test", out.Users[0].Email)
}

// -- 2. institutions --------------------------------------------------

func TestInstitutionsArriveAsReferenceData(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Institutions, 1)
	require.Equal(t, "Institution A", out.Institutions[0].Name)
	require.Equal(t, "i1", out.Institutions[0].ExternalID)
}

func TestNoConnectionCarriesOverAndTheReportSaysSo(t *testing.T) {
	out := mapped(t, complete(t))
	requireProblem(t, out.Report.Warnings, "institutionLoginsStore", "", "arrives unlinked")
	requireProblem(t, out.Report.Warnings, "accountsStore", "isConnected", "imported unlinked")
}

// -- 3. accounts ------------------------------------------------------

func TestTypeAndSubtypeBecomeKindForArithmeticAndTypeForTheLabel(t *testing.T) {
	out := mapped(t, complete(t))
	checking := accountNamed(t, out, "Checking 1")
	require.Equal(t, domain.KindCash, checking.Kind)
	require.Equal(t, "checking", checking.Type)

	card := accountNamed(t, out, "Card 1")
	require.Equal(t, domain.KindCreditCard, card.Kind)
	require.Equal(t, "credit_card", card.Type)
	require.True(t, card.Kind.IsDebt())
}

func TestEveryAccountArrivesUnlinkedWithNoSyncFloor(t *testing.T) {
	out := mapped(t, complete(t))
	for _, account := range out.Accounts {
		require.Empty(t, account.SimpleFINAccountID)
		require.True(t, account.SyncFloorOn.IsZero())
		require.Equal(t, uuid.Nil, account.ConnectionID)
	}
}

func TestTheBalanceIsTheSignNormalizedOneAndTheGoalReserveSurvives(t *testing.T) {
	out := mapped(t, complete(t))
	checking := accountNamed(t, out, "Checking 1")
	require.True(t, checking.HasProviderBalance)
	require.Equal(t, "1899.50", checking.ProviderBalance.String())
	require.Equal(t, "500.00", checking.GoalBalance.String())
	require.Equal(t, "-1200.00", accountNamed(t, out, "Card 1").ProviderBalance.String())
	require.Equal(t, "****0000", checking.MaskedNumber)
}

// SimpleFIN has no field for either, so the import is their only source.
func TestACardsLimitAndRateAreImported(t *testing.T) {
	card := accountNamed(t, mapped(t, complete(t)), "Card 1")
	require.True(t, card.HasCreditLimit)
	require.Equal(t, "5000.00", card.CreditLimit.String())
	// The export writes 24.99 for a 24.99% APR; the column holds a rate.
	require.True(t, card.HasInterestRate)
	require.Equal(t, "0.2499", card.InterestRate.String())
}

func TestAnAccountWithNoTermsGetsNoLimitRatherThanAZeroOne(t *testing.T) {
	checking := accountNamed(t, mapped(t, complete(t)), "Checking 1")
	require.False(t, checking.HasCreditLimit)
	require.False(t, checking.HasInterestRate)
}

func TestExcludedFromBudgetsBecomesExcludedFromSpendingPlan(t *testing.T) {
	out := mapped(t, complete(t))
	card := accountNamed(t, out, "Card 1")
	require.True(t, card.ExcludedFromSpendingPlan)
	require.False(t, card.ExcludedFromReports)
}

func TestAnAccountSubtypeWeHaveNeverSeenIsNamedNotGuessed(t *testing.T) {
	out := mapped(t, edited(t, "accountsStore", "a1", map[string]any{"subType": "CRYPTO_WALLET"}))
	problem := requireProblem(t, out.Report.Errors, "accountsStore", "subType", "unknown account subtype")
	require.Contains(t, problem.Message, "CRYPTO_WALLET")
	require.Equal(t, "a1", problem.RecordID)
}

// -- 4. categories ----------------------------------------------------

func TestTheTreeTheTxfCodeAndTheSystemMarkerAllSurvive(t *testing.T) {
	out := mapped(t, complete(t))
	groceries := categoryNamed(t, out, "Groceries")
	require.Equal(t, categoryNamed(t, out, "Food & Dining").ID, groceries.ParentID)
	require.Equal(t, "N001", groceries.TxfID)
	require.Equal(t, []string{"N001"}, groceries.TxfIDs)
	require.Equal(t, "GROCERIES", groceries.KnownCategoryID)
	require.Equal(t, domain.CategoryTransfer, categoryNamed(t, out, "Transfer").Kind)
}

func TestIsNotUserAssignableIsStoredPositively(t *testing.T) {
	out := mapped(t, complete(t))
	transfer := categoryNamed(t, out, "Transfer")
	require.False(t, transfer.IsUserAssignable)
	require.False(t, transfer.IsEditable)
	require.True(t, categoryNamed(t, out, "Groceries").IsUserAssignable)
}

func TestACategoryTypeWeHaveNeverSeenIsNamed(t *testing.T) {
	out := mapped(t, edited(t, "categoryStore", "c1", map[string]any{"type": "REBATE"}))
	requireProblem(t, out.Report.Errors, "categoryStore", "type", "unknown category type")
}

// -- 5. tags ----------------------------------------------------------

func TestTagsCarryTheirColour(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Tags, 1)
	require.Equal(t, "Vacation", out.Tags[0].Name)
	require.Equal(t, "#ff8800", out.Tags[0].Color)
}

func TestTwoTagsWithOneNameAreRefusedBecauseTheColumnIsUnique(t *testing.T) {
	export := complete(t)
	storeOf(t, export, "tagStore")["t2"] = map[string]any{"id": "t2", "name": "vacation"}
	out := mapped(t, export)
	requireProblem(t, out.Report.Errors, "tagStore", "name", "unique per space")
}

// -- 6. filters -------------------------------------------------------

func TestACategoryFilterKeepsItsIdsJoinable(t *testing.T) {
	out := mapped(t, complete(t))
	var envelopeFilter *store.Filter
	for _, filter := range out.Filters {
		if filter.Scope == scopeEnvelope {
			envelopeFilter = filter
		}
	}
	require.NotNil(t, envelopeFilter)
	require.Len(t, envelopeFilter.Items, 1)
	item := envelopeFilter.Items[0]
	require.Equal(t, string(domain.FieldCategory), item.Field)
	require.Equal(t, []uuid.UUID{categoryNamed(t, out, "Groceries").ID}, item.ValueIDs)
	require.Empty(t, item.ValueTexts)
}

func TestAPayeeFilterLandsInTheTextColumnInstead(t *testing.T) {
	out := mapped(t, complete(t))
	for _, filter := range out.Filters {
		if filter.Scope != scopeWatchlist {
			continue
		}
		item := filter.Items[0]
		require.Equal(t, string(domain.FieldPayee), item.Field)
		require.Equal(t, []string{"Coffee"}, item.ValueTexts)
		require.Empty(t, item.ValueIDs)
		require.Equal(t, string(domain.OpContains), item.Operator)
		return
	}
	t.Fatal("no watchlist filter")
}

func TestTheOrGroupsOfTheRulesBuilderSurvive(t *testing.T) {
	out := mapped(t, complete(t))
	for _, filter := range out.Filters {
		if filter.Scope != scopeRule {
			continue
		}
		require.Len(t, filter.Items, 2)
		require.Equal(t, 0, filter.Items[0].GroupIndex)
		require.Equal(t, 1, filter.Items[1].GroupIndex)
		require.Equal(t, "COFFEE", filter.Items[0].Text)
		require.True(t, filter.Items[1].HasAmountMin)
		require.Equal(t, "-50.00", filter.Items[1].AmountMin.String())
		require.Equal(t, "-1.00", filter.Items[1].AmountMax.String())
		return
	}
	t.Fatal("no rule filter")
}

func TestAFilterFacetWeHaveNeverSeenIsNamed(t *testing.T) {
	export := complete(t)
	recordOf(t, export, "filterStore", "f1")["filterItems"] = []any{
		map[string]any{"type": "MERCHANT_CATEGORY_CODE"},
	}
	out := mapped(t, export)
	requireProblem(t, out.Report.Errors, "filterStore", "filterItems.type", "unknown facet")
}

// -- 7. transactions --------------------------------------------------

func TestSimplifisTwoNamesAreStoredTheWayOurUILabelsThem(t *testing.T) {
	out := mapped(t, complete(t))
	txn := txnByStatement(t, out, "SQ *COFFEE 1234")
	require.Equal(t, "Coffee", txn.Payee)
	require.Equal(t, "SQ *COFFEE 1234", txn.StatementName)
}

func TestARowWithNoRenameDisplaysTheBankString(t *testing.T) {
	out := mapped(t, complete(t))
	require.Equal(t, "PLUMBER", txnByStatement(t, out, "PLUMBER").Payee)
}

func TestTheTwoExclusionFlagsStayIndependent(t *testing.T) {
	out := mapped(t, complete(t))
	txn := txnByStatement(t, out, "SQ *COFFEE 1234")
	require.False(t, txn.ExcludedFromReports)
	require.True(t, txn.ExcludedFromSpendingPlan)
}

func TestEveryImportedRowIsStampedWithTheImportAsItsSource(t *testing.T) {
	out := mapped(t, complete(t))
	require.NotEmpty(t, out.Transactions)
	for _, txn := range out.Transactions {
		require.Equal(t, domain.SourceSimplifiImport, txn.Source)
		require.Empty(t, txn.ExternalID)
	}
}

func TestTheStoredRunningBalanceCheckNumberAndFlagSurvive(t *testing.T) {
	out := mapped(t, complete(t))
	txn := txnByStatement(t, out, "SQ *COFFEE 1234")
	require.True(t, txn.HasBalance)
	require.Equal(t, "1899.50", txn.Balance.String())
	require.Equal(t, "1042", txn.CheckNumber)
	require.Equal(t, "red", txn.UserFlag)
	require.Equal(t, "check this", txn.UserFlagNote)
	require.True(t, txn.IsReviewed)
}

func TestStateDecidesPendingAndAnUnknownStateIsNamed(t *testing.T) {
	out := mapped(t, complete(t))
	require.False(t, txnByStatement(t, out, "SQ *COFFEE 1234").IsPending)
	pending := false
	for _, txn := range out.Transactions {
		if txn.IsPending {
			pending = true
		}
	}
	require.True(t, pending, "the PENDING fixture row should be pending")

	broken := mapped(t, edited(t, "transactionStore", "x1", map[string]any{"state": "IN_FLIGHT"}))
	requireProblem(t, broken.Report.Errors, "transactionStore", "state", "unknown transaction state")
}

// Simplifi writes these as much as a year ahead.
func TestAScheduledForecastIsAnEstimateAndNotAPendingCharge(t *testing.T) {
	out := mapped(t, edited(t, "transactionStore", "x5", map[string]any{
		"state": "PENDING", "source": "SCHEDULED_TRANSACTION_PENDING",
	}))
	txn := txnByStatement(t, out, "STREAMSVC.COM 800-555-0100")
	require.False(t, txn.IsPending, "a forecast is not an authorization the bank has taken")
	require.Equal(t, store.ProjectedEstimate, txn.EstimateStatus)
}

func TestAPendingChargeFromTheBankIsStillPending(t *testing.T) {
	out := mapped(t, edited(t, "transactionStore", "x5", map[string]any{
		"state": "PENDING", "source": "QCS_REFRESH_BANK_PENDING",
	}))
	txn := txnByStatement(t, out, "STREAMSVC.COM 800-555-0100")
	require.True(t, txn.IsPending)
	require.Empty(t, txn.EstimateStatus)
}

func TestAnUnknownSourceIsNamedRatherThanReadAsReal(t *testing.T) {
	broken := mapped(t, edited(t, "transactionStore", "x5", map[string]any{"source": "TELEPATHY"}))
	requireProblem(t, broken.Report.Errors, "transactionStore", "source", "unknown transaction source")
}

func TestASplitCarriesTheCategoryAndTheParentDoesNot(t *testing.T) {
	out := mapped(t, complete(t))
	txn := txnByStatement(t, out, "SUPERMARKET")
	require.Equal(t, uuid.Nil, txn.CategoryID)
	require.Len(t, txn.Splits, 2)
	require.Equal(t, "-60.00", txn.Splits[0].Amount.String())
	require.Equal(t, categoryNamed(t, out, "Groceries").ID, txn.Splits[0].CategoryID)
	require.Equal(t, "food", txn.Splits[0].Memo)
	require.Equal(t, 0, txn.Splits[0].Position)
	require.Equal(t, 1, txn.Splits[1].Position)
}

func TestTagsReachBothTheTransactionAndTheSplit(t *testing.T) {
	out := mapped(t, complete(t))
	tagID := out.Tags[0].ID
	require.Equal(t, []uuid.UUID{tagID}, txnByStatement(t, out, "SQ *COFFEE 1234").TagIDs)
	require.Equal(t, []uuid.UUID{tagID}, txnByStatement(t, out, "SUPERMARKET").Splits[1].TagIDs)
}

func TestAnAttachmentRecordsTheDocumentAndWarnsThatNoBlobExists(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Attachments, 1)
	require.Equal(t, "receipt.pdf", out.Attachments[0].Filename)
	require.Equal(t, "application/pdf", out.Attachments[0].ContentType)
	require.Equal(t, 20480, out.Attachments[0].SizeBytes)
	require.Equal(t, "simplifi-import:d1", out.Attachments[0].StorageKey)
	requireProblem(t, out.Report.Warnings, "transactionStore", "attachments", "no blob exists behind it")
}

func TestAKnownCategoryIdTheCategoryStoreLacksIsNamed(t *testing.T) {
	// A warning, not a refusal, which would lose the amount as well.
	out := mapped(t, edited(t, "transactionStore", "x1", map[string]any{
		"coa": nil, "knownCategoryId": "PET_INSURANCE",
	}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	requireProblem(t, out.Report.Warnings, "transactionStore", "knownCategoryId", "PET_INSURANCE")
	require.Equal(t, uuid.Nil, txnByStatement(t, out, "SQ *COFFEE 1234").CategoryID)
}

func TestACoaThatNamesAnAccountIsATransferNotACategory(t *testing.T) {
	// coa is "category or account"; a transfer row tags it ACCOUNT.
	out := mapped(t, edited(t, "transactionStore", "x1", map[string]any{
		"coa": map[string]any{"type": "ACCOUNT", "id": "a2"},
	}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.Equal(t, uuid.Nil, txnByStatement(t, out, "SQ *COFFEE 1234").CategoryID)
}

func TestAnUncategorizedCoaDoesNotFallBackToKnownCategoryId(t *testing.T) {
	// coa has answered. A stale knownCategoryId beside it must not override
	// the user's choice to leave the row uncategorised.
	out := mapped(t, edited(t, "transactionStore", "x1", map[string]any{
		"coa":             map[string]any{"type": "UNCATEGORIZED", "id": "0"},
		"knownCategoryId": "GROCERIES",
	}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.Equal(t, uuid.Nil, txnByStatement(t, out, "SQ *COFFEE 1234").CategoryID)
}

func TestATransactionWhoseAccountIsMissingIsNamedNotDropped(t *testing.T) {
	out := mapped(t, edited(t, "transactionStore", "x1", map[string]any{"accountId": "a99"}))
	requireProblem(t, out.Report.Errors, "transactionStore", "accountId", "no earlier step imported")
}

// -- transfer pairs ---------------------------------------------------

func TestTheTokenIsWrittenOnBothLegs(t *testing.T) {
	out := mapped(t, complete(t))
	left := txnByStatement(t, out, "TRANSFER TO SAVINGS")
	right := txnByStatement(t, out, "TRANSFER FROM CHECKING")
	require.NotEqual(t, uuid.Nil, left.TransferPairID)
	require.Equal(t, left.TransferPairID, right.TransferPairID)
}

func TestEachPairGetsOneTokenNotOnePerLeg(t *testing.T) {
	out := mapped(t, complete(t))
	require.Equal(t, 1, out.Report.Written["transfer_pairs"])
}

func TestAPartnerThatIsNotInTheExportLeavesTheLegUnpaired(t *testing.T) {
	// The surviving leg is kept unpaired rather than refused, which would
	// lose the amount too.
	export := complete(t)
	recordOf(t, export, "transactionStore", "x2")["transfer"] = "x99"
	delete(recordOf(t, export, "transactionStore", "x3"), "transfer")
	out := mapped(t, export)
	require.Empty(t, out.Report.Errors, out.Report.Render())
	requireProblem(t, out.Report.Warnings, "transactionStore", "transfer", "imports unpaired")
	require.Equal(t, uuid.Nil, txnByStatement(t, out, "TRANSFER TO SAVINGS").TransferPairID)
}

func TestTwoForecastsLinkedToEachOtherImportUnpaired(t *testing.T) {
	// Simplifi links its projected occurrences the way it links real rows.
	// A forecast is not a leg (store.CountsTowardBalance, as checkHandPair).
	export := complete(t)
	for _, id := range []string{"x2", "x3"} {
		recordOf(t, export, "transactionStore", id)["state"] = "PENDING"
		recordOf(t, export, "transactionStore", id)["source"] = "SCHEDULED_TRANSACTION_PENDING"
	}

	out := mapped(t, export)

	require.Empty(t, out.Report.Errors, out.Report.Render())
	requireProblem(t, out.Report.Warnings, "transactionStore", "transfer", "imports unpaired")
	require.Equal(t, uuid.Nil, txnByStatement(t, out, "TRANSFER TO SAVINGS").TransferPairID)
	require.Equal(t, uuid.Nil, txnByStatement(t, out, "TRANSFER FROM CHECKING").TransferPairID)
	require.Equal(t, 0, out.Report.Written["transfer_pairs"],
		"a refused pair is counted as one written")
}

func TestAForecastDoesNotDragItsRealPartnerIntoAPair(t *testing.T) {
	// One real row and one forecast is still not a transfer.
	export := complete(t)
	recordOf(t, export, "transactionStore", "x3")["state"] = "PENDING"
	recordOf(t, export, "transactionStore", "x3")["source"] = "SCHEDULED_TRANSACTION_PENDING"

	out := mapped(t, export)

	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.Equal(t, uuid.Nil, txnByStatement(t, out, "TRANSFER TO SAVINGS").TransferPairID)
	require.Equal(t, uuid.Nil, txnByStatement(t, out, "TRANSFER FROM CHECKING").TransferPairID)
}

// -- 8. series --------------------------------------------------------

func TestTheRruleIsStoredAsColumnsWithTheDropdownLabelBesideIt(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Series, 1)
	series := out.Series[0]
	require.Equal(t, domain.AliasEveryMonth, series.Alias)
	require.Equal(t, domain.FreqMonthly, series.Frequency)
	require.Equal(t, 1, series.Interval)
	require.Equal(t, []int{3}, series.ByMonthDay)
	require.Equal(t, domain.SeriesSubscription, series.Kind)
	require.Equal(t, "auto", series.MatchCriteria)
	require.Equal(t, []string{"STREAMSVC.COM 800-555-0100"}, series.LearnedDescriptions)
}

func TestTheMatchingNameAndTheDisplayNameAreSeparate(t *testing.T) {
	out := mapped(t, complete(t))
	require.Equal(t, "STREAMSVC.COM 800-555-0100", out.Series[0].Description)
	require.Equal(t, "Example Streaming", out.Series[0].DisplayName)
	require.Equal(t, "-15.00", out.Series[0].Amount.String())
}

func TestAPostedRowClaimsTheSeriesAndTheSlotNotJustTheSeries(t *testing.T) {
	out := mapped(t, complete(t))
	txn := txnByStatement(t, out, "STREAMSVC.COM 800-555-0100")
	require.Equal(t, out.Series[0].ID, txn.SeriesID)
	require.Equal(t, domain.NewDate(2026, 8, 3), txn.SeriesDueOn)
}

func TestATransactionNamingASeriesTheExportLacksIsAnError(t *testing.T) {
	out := mapped(t, edited(t, "transactionStore", "x5", map[string]any{"stModelId": "s99"}))
	requireProblem(t, out.Report.Errors, "transactionStore", "stModelId", "s99")
}

func TestAnUnknownRecurrenceFrequencyIsNamed(t *testing.T) {
	export := complete(t)
	recordOf(t, export, "scheduledTransactionsStore", "s1")["recurrence"] = map[string]any{
		"frequency": "FORTNIGHTLY", "interval": num("1"), "alias": "EVERY_WEEK",
	}
	out := mapped(t, export)
	requireProblem(t, out.Report.Errors, "scheduledTransactionsStore", "recurrence.frequency", "unknown frequency")
}

func TestARecurrenceWithoutAnAliasIsLabelledFromItsRule(t *testing.T) {
	// Simplifi's export carries frequency, interval and byMonthDay; an alias
	// is not part of its shape.
	export := complete(t)
	recordOf(t, export, "scheduledTransactionsStore", "s1")["recurrence"] = map[string]any{
		"frequency": "MONTHLY", "interval": num("1"), "byMonthDay": []any{num("14")},
	}
	out := mapped(t, export)
	require.Empty(t, out.Report.Errors)
	require.Equal(t, domain.AliasEveryMonth, out.Series[0].Alias)
	require.Equal(t, domain.FreqMonthly, out.Series[0].Frequency)
}

// -- 9. rules ---------------------------------------------------------

func TestARulesConditionsAreTheSharedFilterAndItsActionsAreColumns(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Rules, 1)
	rule := out.Rules[0]
	require.Equal(t, "Coffee", rule.Name)
	require.Equal(t, "Coffee", rule.SetPayee)
	require.Equal(t, categoryNamed(t, out, "Groceries").ID, rule.SetCategoryID)
	require.Equal(t, []uuid.UUID{out.Tags[0].ID}, rule.AddTagIDs)
	require.Equal(t, 10, rule.Priority)
	require.NotNil(t, rule.SetExcludedFromSpendingPlan)
	require.True(t, *rule.SetExcludedFromSpendingPlan)
	// Null means "leave alone", not false.
	require.Nil(t, rule.SetExcludedFromReports)

	var ruleFilter *store.Filter
	for _, filter := range out.Filters {
		if filter.ID == rule.FilterID {
			ruleFilter = filter
		}
	}
	require.NotNil(t, ruleFilter)
	require.Len(t, ruleFilter.Items, 2)
}

func TestATransactionRecordsTheRuleThatTouchedIt(t *testing.T) {
	out := mapped(t, complete(t))
	for _, txn := range out.Transactions {
		if txn.RuleID != uuid.Nil {
			require.Equal(t, out.Rules[0].ID, txn.RuleID)
			return
		}
	}
	t.Fatal("no transaction recorded a rule")
}

func TestARuleWithNothingToMatchOnIsRefused(t *testing.T) {
	out := mapped(t, edited(t, "renameRuleStore", "r1", map[string]any{
		"filterId": nil, "renamePayeeFrom": nil,
	}))
	requireProblem(t, out.Report.Errors, "renameRuleStore", "filterId", "nor wording to match")
}

func TestASimplifiRenameRuleBecomesAFilterOnTheStatementName(t *testing.T) {
	// Simplifi's rename rule carries no filter; the wording it matches is the
	// statement name, not the display payee.
	out := mapped(t, edited(t, "renameRuleStore", "r1", map[string]any{
		"filterId":        nil,
		"renamedPayee":    nil,
		"setPayee":        nil,
		"renamePayeeFrom": "STATE TOLL ROAD",
		"renamePayeeTo":   "Toll Road",
		"matchCriteria":   "If Payee Contains",
	}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	rule := out.Rules[0]
	for _, filter := range out.Filters {
		if filter.ID == rule.FilterID {
			require.Equal(t, scopeRule, filter.Scope)
			require.Len(t, filter.Items, 1)
			require.Equal(t, string(domain.FieldStatementName), filter.Items[0].Field)
			require.Equal(t, string(domain.OpContains), filter.Items[0].Operator)
			require.Equal(t, []string{"STATE TOLL ROAD"}, filter.Items[0].ValueTexts)
			require.Equal(t, "Toll Road", rule.SetPayee)
			return
		}
	}
	t.Fatal("the rename rule's synthesised filter was not written")
}

func TestARuleWithInlineConditionsGetsAFilterOfItsOwn(t *testing.T) {
	out := mapped(t, edited(t, "renameRuleStore", "r1", map[string]any{
		"filterId": nil,
		"conditions": []any{
			map[string]any{"type": "PAYEE", "values": []any{"COFFEE"}, "operator": "CONTAINS"},
		},
	}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	rule := out.Rules[0]
	for _, filter := range out.Filters {
		if filter.ID == rule.FilterID {
			require.Equal(t, scopeRule, filter.Scope)
			require.Len(t, filter.Items, 1)
			return
		}
	}
	t.Fatal("the rule's own filter was not written")
}

func TestARenameRuleRemembersWhereItCameFrom(t *testing.T) {
	out := mapped(t, complete(t))
	require.Equal(t, "simplifi:renameRuleStore:r1", out.Rules[0].SourceRef)
}

// withRuleEvidence adds rows touched by two transaction rules the export does
// not hold: tr9, whose rows agree, and tr8, whose rows agree on nothing.
func withRuleEvidence(t *testing.T) *Export {
	t.Helper()
	export := complete(t)
	rows := storeOf(t, export, "transactionStore")
	row := func(id, statement, payee, category string, extra map[string]any) {
		record := map[string]any{
			"id": id, "accountId": "a1", "postedOn": "2026-08-10", "amount": num("-30"),
			"payee": statement, "renamedPayee": payee, "coa": category,
		}
		for field, value := range extra {
			record[field] = value
		}
		rows[id] = record
	}
	row("x20", "CITY PARKING 001", "Parking", "c1", map[string]any{
		"transactionRuleId": "tr9", "isExcludedFromReports": true, "tags": []any{"t1"},
	})
	row("x21", "city parking 001", "Parking", "c1", map[string]any{
		"transactionRuleId": "tr9", "isExcludedFromReports": true,
	})
	row("x22", "CITY PARKING 002", "Parking", "c1", map[string]any{
		"transactionRuleId": "tr9", "isExcludedFromReports": true, "isExcludedFromF2S": true,
	})
	row("x23", "LOOSE WORDING", "Payee A", "c1", map[string]any{"transactionRuleId": "tr8"})
	row("x24", "LOOSE WORDING 2", "Payee B", "c3", map[string]any{"transactionRuleId": "tr8"})
	return export
}

func ruleBySource(t *testing.T, out *Mapped, sourceRef string) *Rule {
	t.Helper()
	for _, rule := range out.Rules {
		if rule.SourceRef == sourceRef {
			return rule
		}
	}
	t.Fatalf("no rule from %s", sourceRef)
	return nil
}

func filterByID(t *testing.T, out *Mapped, id uuid.UUID) *store.Filter {
	t.Helper()
	for _, filter := range out.Filters {
		if filter.ID == id {
			return filter
		}
	}
	t.Fatalf("no filter %s", id)
	return nil
}

func TestATransactionRuleMissingFromTheExportIsRebuiltFromItsRows(t *testing.T) {
	// Simplifi's transaction rules live on its server; the export holds only
	// the transactionRuleId each touched row carries.
	out := mapped(t, withRuleEvidence(t))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.Len(t, out.Rules, 3)

	rule := ruleBySource(t, out, "simplifi:transactionRuleId:tr9")
	require.True(t, rule.IsActive)
	require.Equal(t, "Parking (rebuilt from Simplifi)", rule.Name)
	require.Equal(t, "Parking", rule.SetPayee)
	require.Equal(t, categoryNamed(t, out, "Groceries").ID, rule.SetCategoryID)
	// Only one of the rows carries the tag, so the rule does not add it.
	require.Empty(t, rule.AddTagIDs)
	// The two exclusion flags are decided apart: every row is out of reports,
	// only one is out of the spending plan.
	require.NotNil(t, rule.SetExcludedFromReports)
	require.True(t, *rule.SetExcludedFromReports)
	require.Nil(t, rule.SetExcludedFromSpendingPlan)
	require.Nil(t, rule.SetIsReviewed)

	filter := filterByID(t, out, rule.FilterID)
	require.Equal(t, scopeRule, filter.Scope)
	require.Len(t, filter.Items, 1)
	require.Equal(t, string(domain.FieldStatementName), filter.Items[0].Field)
	require.Equal(t, string(domain.OpIsExactly), filter.Items[0].Operator)
	require.Equal(t, []string{"CITY PARKING 001", "CITY PARKING 002"}, filter.Items[0].ValueTexts)

	for _, statement := range []string{"CITY PARKING 001", "city parking 001", "CITY PARKING 002"} {
		require.Equal(t, rule.ID, txnByStatement(t, out, statement).RuleID, statement)
	}
	requireProblem(t, out.Report.Warnings, "transactionStore", "transactionRuleId", "not in the export")
}

func TestARebuiltRuleWhoseRowsAgreeOnNothingArrivesSwitchedOff(t *testing.T) {
	out := mapped(t, withRuleEvidence(t))
	rule := ruleBySource(t, out, "simplifi:transactionRuleId:tr8")
	require.False(t, rule.IsActive)
	require.Contains(t, rule.Name, "choose its actions")
	require.Empty(t, rule.SetPayee)
	require.Equal(t, uuid.Nil, rule.SetCategoryID)
	require.Equal(t, rule.ID, txnByStatement(t, out, "LOOSE WORDING").RuleID)
}

// -- 10. goals --------------------------------------------------------

func TestAGoalNamesTheAccountWhoseReserveItInflates(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Goals, 1)
	goal := out.Goals[0]
	require.Equal(t, accountNamed(t, out, "Checking 1").ID, goal.AccountID)
	require.Equal(t, "15000.00", goal.TargetAmount.String())
	require.Equal(t, "250.00", goal.ContributionAmount.String())
	require.Equal(t, "500.00", goal.SavedSoFar.String())
	require.True(t, goal.IsTakenFromPlan)
	require.Equal(t, out.Tags[0].ID, goal.TagID)
	require.Len(t, goal.TxnIDs, 1)
}

// -- 11. the spending plan --------------------------------------------

func TestTheMonthLandsOnTheFirstOfTheMonth(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Months, 1)
	require.Equal(t, domain.NewDate(2026, 8, 1), out.Months[0].Month)
}

func TestTheFiveFieldPatternSurvivesVerbatimForEveryBucket(t *testing.T) {
	out := mapped(t, complete(t))
	month := out.Months[0]

	require.Equal(t, "6200.00", month.Income.Calculated.String())
	require.Len(t, month.Income.TxnIDs, 1)
	require.Empty(t, month.Income.ExcludedTxnIDs)
	require.False(t, month.Income.HasOverwritten)
	require.False(t, month.Income.ResetOverwritten)

	require.Equal(t, "-1200.50", month.Bills.Calculated.String())
	require.Len(t, month.Bills.TxnIDs, 1)
	require.Len(t, month.Bills.ExcludedTxnIDs, 1)
	require.True(t, month.Bills.HasOverwritten)
	require.Equal(t, "-1300.50", month.Bills.Overwritten.String())

	require.Equal(t, "-15.00", month.Subscriptions.Calculated.String())
	require.False(t, month.Subscriptions.HasOverwritten)
	require.True(t, month.Subscriptions.ResetOverwritten)

	require.Equal(t, "0.00", month.Transfer.Calculated.String())
	require.Len(t, month.Transfer.TxnIDs, 2)

	require.Equal(t, "-220.00", month.Spent.Calculated.String())
	require.Len(t, month.Spent.TxnIDs, 1)
	require.Len(t, month.Spent.ExcludedTxnIDs, 1)
}

func TestTheHeadlineFiguresAndTheProjectionBlockAreCopiedNotRecomputed(t *testing.T) {
	out := mapped(t, complete(t))
	month := out.Months[0]
	require.Equal(t, "400.01", month.CalculatedRollover.String())
	require.Equal(t, "100.00", month.SetAside.String())
	require.Equal(t, "4000.00", month.TotalToSpend.String())
	require.Equal(t, "1234.56", month.LeftToSpend.String())
	require.Equal(t, "-900.00", month.ProjectedOtherSpending.String())
	require.Equal(t, domain.ProjectionRunRate, month.ProjectionType)
	require.Equal(t, domain.NewDate(2026, 8, 1), month.ProjectionStartDate)
	require.Equal(t, domain.NewDate(2026, 8, 31), month.ProjectionEndDate)
	require.Equal(t, "50.00", month.ProjectionBuffer.String())
	require.True(t, month.NewMonthViewed)
}

func TestTheGoalsBucketKeepsSimplifisFigureAndSaysWhyItsTrailIsEmpty(t *testing.T) {
	out := mapped(t, complete(t))
	month := out.Months[0]
	require.Equal(t, "-250.00", month.Goals.Calculated.String())
	require.Empty(t, month.Goals.TxnIDs)
	requireProblem(t, out.Report.Warnings, "freeToSpendStore", "goalsIds", "names goals, not transactions")
}

func TestPlannedSpendingHasNoAuditTrailAndSaysSo(t *testing.T) {
	out := mapped(t, complete(t))
	month := out.Months[0]
	require.Equal(t, "-600.00", month.PlannedSpending.Calculated.String())
	require.Empty(t, month.PlannedSpending.TxnIDs)
	require.False(t, month.PlannedSpending.HasOverwritten)
	requireProblem(t, out.Report.Warnings, "freeToSpendStore", "calculatedPlannedSpendingAmount",
		"left at their defaults")
}

func TestEnvelopesAreRowsOfTheirOwnKeyedToTheMonth(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Envelopes, 1)
	envelope := out.Envelopes[0]
	require.Equal(t, out.Months[0].ID, envelope.SpendingPlanMonthID)
	require.Equal(t, "Groceries", envelope.Name)
	require.Equal(t, "600.00", envelope.TargetAmount.String())
	require.True(t, envelope.HasOverwrittenTarget)
	require.Equal(t, "650.00", envelope.OverwrittenTargetAmount.String())
	// Simplifi stores spend in the ledger's sign; our envelope figures are
	// magnitudes, so the import flips it.
	require.Equal(t, "125.50", envelope.CalculatedSpentAmount.String())
	require.Equal(t, "57.50", envelope.RolloverAmount.String())
	require.True(t, envelope.Recurring)
	require.NotEqual(t, uuid.Nil, envelope.RecurringGroupID)
	require.Len(t, envelope.TxnIDs, 2)
	require.Len(t, envelope.ExcludedTxnIDs, 1)
	require.NotEqual(t, uuid.Nil, envelope.FilterID)
}

func TestADanglingTransactionIdIsDroppedFromTheTrailAndCounted(t *testing.T) {
	export := complete(t)
	recordOf(t, export, "freeToSpendStore", "m1")["spentTxnIds"] = []any{"x7", "gone"}
	out := mapped(t, export)
	require.Len(t, out.Months[0].Spent.TxnIDs, 1)
	requireProblem(t, out.Report.Warnings, "freeToSpendStore", "spentTxnIds", "dropped from the audit trail")
}

func TestAnUnparseableMonthIsNamed(t *testing.T) {
	out := mapped(t, edited(t, "freeToSpendStore", "m1", map[string]any{"date": "August"}))
	requireProblem(t, out.Report.Errors, "freeToSpendStore", "date", "not a month")
}

// -- 12. watchlists ---------------------------------------------------

func TestAWatchlistIsAFilterANameAndATarget(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Watchlists, 1)
	watchlist := out.Watchlists[0]
	require.Equal(t, "Coffee", watchlist.Name)
	require.Equal(t, "☕", watchlist.Emoji)
	require.True(t, watchlist.HasTarget)
	require.Equal(t, "50.00", watchlist.TargetAmount.String())
	require.Equal(t, "month", watchlist.Period)
	require.NotEqual(t, uuid.Nil, watchlist.FilterID)
}

// -- 13. investments --------------------------------------------------

func TestTheSecurityMasterTakesItsPriceFromTheQuoteStore(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Securities, 1)
	security := out.Securities[0]
	require.Equal(t, "VTI", security.Symbol)
	require.Equal(t, "etf", security.Kind)
	require.True(t, security.HasLastPrice)
	// Ten places, not two: a share price is not money.
	require.Equal(t, "250.1234", security.LastPrice.String())
	require.Equal(t, "248.5", security.PriorClose.String())
	require.NotNil(t, security.LastPriceAt)
}

func TestAHoldingKeepsFractionalSharesAndTheBasisCompletenessFlag(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.Holdings, 1)
	holding := out.Holdings[0]
	require.Equal(t, "10.5", holding.Shares.String())
	require.Equal(t, "190.4761904762", holding.AverageCost.String())
	require.True(t, holding.HasCostBasis)
	require.Equal(t, "2000.00", holding.CostBasis.String())
	require.True(t, holding.IsCostBasisComplete)
	require.Equal(t, accountNamed(t, out, "Investment 1").ID, holding.AccountID)
}

func TestAnUnknownBasisIsAStateAndNotAZero(t *testing.T) {
	out := mapped(t, edited(t, "investmentHoldingsV2Store", "h1", map[string]any{
		"costBasis": nil, "isCostBasisComplete": nil,
	}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.False(t, out.Holdings[0].HasCostBasis)
	require.False(t, out.Holdings[0].IsCostBasisComplete)
}

// -- 14. balance history ----------------------------------------------

func TestEachPointOfTheSeriesBecomesOneSnapshot(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.BalanceSnapshots, 2)
	require.Equal(t, domain.NewDate(2026, 8, 14), out.BalanceSnapshots[0].AsOf)
	require.Equal(t, "1925.00", out.BalanceSnapshots[0].Balance.String())
	require.Equal(t, "1899.50", out.BalanceSnapshots[1].Balance.String())
}

func TestTwoBalancesForOneDayCollapseToOne(t *testing.T) {
	// Most days of an export carry both an ONLINE and a CURRENT balance. Writing both would double the history, so the day keeps one.
	export := complete(t)
	recordOf(t, export, "accountsBalancesStore", "b1")["balances"] = []any{
		map[string]any{"date": "2026-08-14", "balance": num("1925.0")},
		map[string]any{"date": "2026-08-14", "balance": num("1900.0")},
	}
	out := mapped(t, export)
	require.Empty(t, out.Report.Errors, out.Report.Render())
	onDay := 0
	for _, snapshot := range out.BalanceSnapshots {
		if snapshot.AsOf.String() == "2026-08-14" {
			onDay++
		}
	}
	require.Equal(t, 1, onDay)
}

func TestTheCurrentBalanceWinsTheDayOverTheOnlineOne(t *testing.T) {
	// The flat shape: one record per balance, tagged ONLINE (settled) or
	// CURRENT (pending included). CURRENT is what Simplifi shows.
	export := complete(t)
	records := storeOf(t, export, "accountsBalancesStore")
	delete(records, "b1")
	records["b_online"] = map[string]any{
		"id": "b_online", "accountId": "a1",
		"balanceType": "ONLINE", "balanceAmount": num("1925.0"), "balanceOn": "2026-08-14",
	}
	records["b_current"] = map[string]any{
		"id": "b_current", "accountId": "a1",
		"balanceType": "CURRENT", "balanceAmount": num("1900.0"), "balanceOn": "2026-08-14",
	}
	out := mapped(t, export)
	require.Empty(t, out.Report.Errors, out.Report.Render())
	for _, snapshot := range out.BalanceSnapshots {
		if snapshot.AsOf.String() == "2026-08-14" {
			require.Equal(t, "1900.00", snapshot.Balance.String())
			return
		}
	}
	t.Fatal("no balance was written for the day")
}

func TestTheAccountTakesItsBalanceFromTheNewestRecordNotTheCache(t *testing.T) {
	// normalizedBalance is a cache and can be stale.
	export := complete(t)
	records := storeOf(t, export, "accountsBalancesStore")
	delete(records, "b1")
	records["b_new"] = map[string]any{
		"id": "b_new", "accountId": "a1",
		"balanceType": "CURRENT", "balanceAmount": num("1234.56"), "balanceOn": "2026-08-20",
	}
	records["b_old"] = map[string]any{
		"id": "b_old", "accountId": "a1",
		"balanceType": "CURRENT", "balanceAmount": num("999.00"), "balanceOn": "2026-08-14",
	}
	out := mapped(t, export)
	require.Empty(t, out.Report.Errors, out.Report.Render())
	account := accountNamed(t, out, "Checking 1")
	require.True(t, account.HasProviderBalance)
	require.Equal(t, "1234.56", account.ProviderBalance.String())
}

// -- 15. alert rules --------------------------------------------------

func TestAnAlertRuleCarriesItsChannelsAndItsThreshold(t *testing.T) {
	out := mapped(t, complete(t))
	require.Len(t, out.AlertRules, 1)
	rule := out.AlertRules[0]
	require.Equal(t, string(domain.AlertLowBankBalance), rule.AlertType)
	require.True(t, rule.ChannelEmail)
	require.False(t, rule.ChannelPush)
	require.True(t, rule.ChannelInApp)
	require.True(t, rule.HasThresholdAmount)
	require.Equal(t, "200.00", rule.ThresholdAmount.String())
	require.Equal(t, accountNamed(t, out, "Checking 1").ID, rule.AccountID)
}

func TestAnAlertWeDeliberatelyDoNotShipIsAWarningWithTheReason(t *testing.T) {
	out := mapped(t, edited(t, "alertRulesStore", "al1", map[string]any{"type": "CREDIT_SCORE_NEW_ALERT"}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.Empty(t, out.AlertRules)
	requireProblem(t, out.Report.Warnings, "alertRulesStore", "type", "Equifax")
}

func TestEveryImportedAlertTypeIsOneTheCatalogHolds(t *testing.T) {
	// A row whose type the catalog does not hold is a rule nothing reads.
	for code, alertType := range alertTypes {
		_, held := domain.AlertByType(alertType)
		require.True(t, held, "%s maps to %q", code, alertType)
	}
}

// simplifiAlert is al1 reshaped the way alertRulesStore really stores a rule:
// the choices in preferences, the offered default in inputVariables, and none
// of the flat fields.
func simplifiAlert(t *testing.T, changes map[string]any) *Export {
	t.Helper()
	shape := map[string]any{
		"type": "HIGH_BALANCE", "accountId": nil,
		"isEnabled": nil, "isPaused": nil, "email": nil, "push": nil, "inApp": nil,
		"thresholdAmount": nil,
		"preferences": map[string]any{
			"isEnabled": true, "isEmailOk": false, "isPushOk": true, "isInProductOk": true,
			"isSmsOk": false, "isPushSupported": true,
		},
		"inputVariables": map[string]any{
			"threshold": map[string]any{"default": "4321", "format": "currency"},
		},
	}
	for field, value := range changes {
		shape[field] = value
	}
	return edited(t, "alertRulesStore", "al1", shape)
}

func TestAnAlertReadsItsChannelsFromSimplifisPreferences(t *testing.T) {
	out := mapped(t, simplifiAlert(t, nil))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	rule := out.AlertRules[0]
	require.Equal(t, string(domain.AlertHighCardBalance), rule.AlertType)
	require.True(t, rule.IsEnabled)
	require.False(t, rule.ChannelEmail)
	require.True(t, rule.ChannelPush)
	require.True(t, rule.ChannelInApp)
}

func TestAnAlertSwitchedOffInSimplifiArrivesSwitchedOff(t *testing.T) {
	out := mapped(t, simplifiAlert(t, map[string]any{
		"preferences": map[string]any{"isEnabled": false, "isInProductOk": true},
	}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.False(t, out.AlertRules[0].IsEnabled)
}

func TestAnAlertTakesTheThresholdSimplifiOffered(t *testing.T) {
	out := mapped(t, simplifiAlert(t, nil))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	rule := out.AlertRules[0]
	require.True(t, rule.HasThresholdAmount)
	require.Equal(t, "4321.00", rule.ThresholdAmount.String())
}

func TestAThresholdTheHouseholdSetBeatsTheOneOffered(t *testing.T) {
	for name, values := range map[string]any{
		"as JSON text": `{"threshold": "750"}`,
		"as an object": map[string]any{"threshold": num("750")},
	} {
		t.Run(name, func(t *testing.T) {
			out := mapped(t, simplifiAlert(t, map[string]any{"inputValues": values}))
			require.Empty(t, out.Report.Errors, out.Report.Render())
			require.Equal(t, "750.00", out.AlertRules[0].ThresholdAmount.String())
		})
	}
}

func TestAPercentageThresholdIsInPercentUnits(t *testing.T) {
	out := mapped(t, simplifiAlert(t, map[string]any{
		"type": "WATCHLIST_TARGET",
		"inputVariables": map[string]any{
			"threshold": map[string]any{"default": "15", "format": "percentage"},
		},
	}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	rule := out.AlertRules[0]
	require.True(t, rule.HasThresholdPct)
	require.Equal(t, "15", rule.ThresholdPct.String())
	require.False(t, rule.HasThresholdAmount)
}

func TestAThresholdAlertWithNoNumberTakesTheCatalogDefault(t *testing.T) {
	// A stored rule never falls back to the catalog when it is read, so one
	// written without its number would never fire.
	for code, check := range map[string]func(t *testing.T, rule *AlertRule){
		"UNCATEGORIZED_TXNS": func(t *testing.T, rule *AlertRule) {
			definition, _ := domain.AlertByType(domain.AlertUncategorized)
			require.NotNil(t, rule.ThresholdCount)
			require.Equal(t, definition.DefaultCount, *rule.ThresholdCount)
		},
		"BILLS_TO_INCOME_PERCENTAGE": func(t *testing.T, rule *AlertRule) {
			definition, _ := domain.AlertByType(domain.AlertBillsToIncomePct)
			require.True(t, rule.HasThresholdPct)
			require.Equal(t, int64(definition.DefaultPercent), rule.ThresholdPct.IntPart())
		},
		"LOW_BALANCE": func(t *testing.T, rule *AlertRule) {
			definition, _ := domain.AlertByType(domain.AlertLowBankBalance)
			require.True(t, rule.HasThresholdAmount)
			require.True(t, definition.DefaultAmount.Equal(rule.ThresholdAmount))
		},
	} {
		t.Run(code, func(t *testing.T) {
			out := mapped(t, simplifiAlert(t, map[string]any{"type": code, "inputVariables": nil}))
			require.Empty(t, out.Report.Errors, out.Report.Render())
			check(t, out.AlertRules[0])
		})
	}
}

func TestAnAlertWithoutAThresholdStoresNone(t *testing.T) {
	out := mapped(t, simplifiAlert(t, map[string]any{"type": "EXTRA_PAYCHECK", "inputVariables": nil}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	rule := out.AlertRules[0]
	require.False(t, rule.HasThresholdAmount)
	require.False(t, rule.HasThresholdPct)
	require.Nil(t, rule.ThresholdCount)
}

func TestAThresholdOfTheWrongKindIsAnError(t *testing.T) {
	out := mapped(t, simplifiAlert(t, map[string]any{
		"inputVariables": map[string]any{
			"threshold": map[string]any{"default": "10", "format": "percentage"},
		},
	}))
	requireProblem(t, out.Report.Errors, "alertRulesStore", "inputVariables.threshold.format", "amount")
}

func TestAnAlertNobodyHasMappedIsAnError(t *testing.T) {
	out := mapped(t, edited(t, "alertRulesStore", "al1", map[string]any{"type": "PET_EXPENSE_SPIKE"}))
	requireProblem(t, out.Report.Errors, "alertRulesStore", "type", "unknown alert type")
}

// -- the order is load-bearing ----------------------------------------

func TestAWatchlistMappedBeforeItsFilterExistsFailsLoudly(t *testing.T) {
	out := mapped(t, without(t, "filterStore"))
	requireProblem(t, out.Report.Errors, "spendingWatchListStore", "filterId", "no earlier step imported")
}

func TestAnEnvelopeMappedBeforeItsFilterExistsFailsLoudly(t *testing.T) {
	out := mapped(t, without(t, "filterStore"))
	requireProblem(t, out.Report.Errors, "freeToSpendStore", "plannedSpendingItems.filterId",
		"no earlier step imported")
}

func TestTheDocumentedOrderResolvesEveryReference(t *testing.T) {
	out := mapped(t, complete(t))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.NotEmpty(t, out.Envelopes)
	require.NotEmpty(t, out.Watchlists)
	require.NotEmpty(t, out.Holdings)
	require.NotEmpty(t, out.AlertRules)
}

// -- it fails loudly on what it reads, and notes what it does not -------

func TestAFieldTheImporterHasNeverSeenIsNotedByNameAndChangesNothing(t *testing.T) {
	plain := mapped(t, complete(t))
	out := mapped(t, edited(t, "transactionStore", "x1", map[string]any{"vibeScore": num("7")}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	problem := requireProblem(t, out.Report.Notes, "transactionStore", "vibeScore", "not read")
	require.Equal(t, "x1", problem.RecordID)
	require.Equal(t, plain.Report.Written, out.Report.Written)
}

func TestNestedFieldsTheImporterHasNeverSeenAreNotedToo(t *testing.T) {
	export := complete(t)
	for _, record := range storeOf(t, export, "accountsStore") {
		record.(map[string]any)["syncMetadata"] = map[string]any{"cursor": "abc"}
	}
	out := mapped(t, export)
	require.Empty(t, out.Report.Errors, out.Report.Render())
	requireProblem(t, out.Report.Notes, "accountsStore", "syncMetadata", "not read")
}

func TestOneUnrepresentableRecordDoesNotStopTheOthersBeingReported(t *testing.T) {
	export := complete(t)
	recordOf(t, export, "accountsStore", "a1")["subType"] = "CRYPTO_WALLET"
	recordOf(t, export, "categoryStore", "c1")["type"] = "REBATE"
	out := mapped(t, export)
	requireProblem(t, out.Report.Errors, "accountsStore", "subType", "unknown")
	requireProblem(t, out.Report.Errors, "categoryStore", "type", "unknown")
}

func TestTheSameProblemOnManyRecordsIsOneLineWithACount(t *testing.T) {
	export := complete(t)
	for _, record := range storeOf(t, export, "transactionStore") {
		record.(map[string]any)["vibeScore"] = num("7")
	}
	out := mapped(t, export)
	found := problems(out.Report.Notes, "transactionStore", "vibeScore")
	require.Len(t, found, 1)
	require.Equal(t, 7, found[0].Occurrences)
}

func TestWarningsAreGroupedByKindWithEveryLineKept(t *testing.T) {
	report := NewReport()
	report.Warnf(KindUnlinked, "institutionLoginsStore", "", "", "2 Simplifi connections are not carried over")
	report.Warnf(KindUnlinked, "accountsStore", "a1", "isConnected", "%q was connected", "Checking")
	report.Warnf(KindUnlinked, "accountsStore", "a2", "isConnected", "%q was connected", "Savings")
	report.Warnf(KindRuleOff, "transaction-rules", "r1", "", "rule %q arrives switched off", "Coffee")
	report.Warnf(KindUnlinked, "accountsStore", "a3", "isConnected", "%q was connected", "Savings")
	report.NotRead("transaction-rules", "r1", "userModifiedAt")
	report.NotRead("transaction-rules", "r2", "userModifiedAt")

	groups := report.Groups()
	require.Len(t, groups, 3)

	require.Equal(t, KindUnlinked, groups[0].Kind)
	require.Equal(t, ActionLinkAccounts, groups[0].Action)
	require.False(t, groups[0].Note)
	require.Equal(t, 4, groups[0].Count)
	require.Equal(t, []string{
		"institutionLoginsStore: 2 Simplifi connections are not carried over",
		`accountsStore a1 isConnected: "Checking" was connected`,
		`accountsStore a2 isConnected: "Savings" was connected (x2)`,
	}, groups[0].Items)

	require.Equal(t, KindRuleOff, groups[1].Kind)
	require.Equal(t, ActionReviewRules, groups[1].Action)
	require.Equal(t, 1, groups[1].Count)

	require.Equal(t, KindNotRead, groups[2].Kind)
	require.True(t, groups[2].Note)
	require.Equal(t, 2, groups[2].Count)
	require.Equal(t, []string{"transaction-rules r1 userModifiedAt: not read (x2)"}, groups[2].Items)
	for _, group := range groups {
		require.NotEmpty(t, group.Summary, group.Kind)
	}
}

func TestEveryKindHasASummary(t *testing.T) {
	for kind, info := range kinds {
		require.NotEmpty(t, info.summary, kind)
	}
}

func TestTheReportNamesTheStoreTheRecordAndTheField(t *testing.T) {
	out := mapped(t, edited(t, "accountsStore", "a1", map[string]any{"subType": "CRYPTO_WALLET"}))
	problem := out.Report.Errors[0]
	require.Equal(t, "accountsStore", problem.Store)
	require.Equal(t, "a1", problem.RecordID)
	require.Equal(t, "subType", problem.Field)
	require.Contains(t, problem.Render(), "accountsStore a1 subType")
}

// -- ids ---------------------------------------------------------------

func TestTheSameSimplifiIdAlwaysMapsToTheSameUUID(t *testing.T) {
	export := complete(t)
	mapper := NewMapper(export, fixtureDataset, Options{OwnerEmail: DefaultOwnerEmail})
	first := mapper.allocate(kindAccount, "a1")
	require.Equal(t, first, mapper.allocate(kindAccount, "a1"))
	require.NotEqual(t, first, mapper.allocate(kindCategory, "a1"))
}

func TestEveryRowHasItsPrimaryKeyBeforeAnythingIsWritten(t *testing.T) {
	out := mapped(t, complete(t))
	require.False(t, out.SpaceID.IsZero())
	for _, account := range out.Accounts {
		require.NotEqual(t, uuid.Nil, account.ID)
	}
	for _, txn := range out.Transactions {
		require.NotEqual(t, uuid.Nil, txn.ID)
		for _, split := range txn.Splits {
			require.NotEqual(t, uuid.Nil, split.ID)
		}
	}
	for _, envelope := range out.Envelopes {
		require.NotEqual(t, uuid.Nil, envelope.ID)
	}
}

// -- shapes the docs left open -----------------------------------------

func TestAllocationsCarriedDirectlyOnSplitAreRead(t *testing.T) {
	out := mapped(t, edited(t, "transactionStore", "x4", map[string]any{
		"allocations": nil,
		"split": []any{
			map[string]any{"id": "al1", "amount": num("-60"), "coa": "c1"},
			map[string]any{"id": "al2", "amount": num("-40"), "coa": "c0"},
		},
	}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.Len(t, txnByStatement(t, out, "SUPERMARKET").Splits, 2)
}

func TestASplitFlagWithNoAllocationsIsRefusedRatherThanFlattened(t *testing.T) {
	out := mapped(t, edited(t, "transactionStore", "x4", map[string]any{"allocations": nil}))
	requireProblem(t, out.Report.Errors, "transactionStore", "split", "no allocations")
}

func TestMoneyArrivingAtACardIsAPaymentNotIncome(t *testing.T) {
	// A card's payment series can carry a BALANCE_ADJUSTMENT coa and a
	// positive amount, which the sign alone reads as a paycheck.
	export := complete(t)
	series := recordOf(t, export, "scheduledTransactionsStore", "s1")
	delete(series, "type")
	template := series["transaction"].(map[string]any)
	template["accountId"] = "a2"
	template["amount"] = num("186.00")
	template["coa"] = map[string]any{"type": "BALANCE_ADJUSTMENT", "id": "1"}
	delete(template, "isSubscription")
	delete(template, "isBill")

	out := mapped(t, export)
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.Len(t, out.Series, 1)
	require.Equal(t, domain.SeriesCreditCardPayment, out.Series[0].Kind)
}

func TestAnExpenseSeriesOnACardStaysABill(t *testing.T) {
	// Only money arriving is a payment. A negative amount on the same card is
	// an ordinary charge and must not be swept into the card-payment rule.
	export := complete(t)
	series := recordOf(t, export, "scheduledTransactionsStore", "s1")
	delete(series, "type")
	template := series["transaction"].(map[string]any)
	template["accountId"] = "a2"
	template["amount"] = num("-15.00")
	delete(template, "isSubscription")
	delete(template, "isBill")

	out := mapped(t, export)
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.Equal(t, domain.SeriesBill, out.Series[0].Kind)
}

func TestAMatchedTransactionIsNotATransferPair(t *testing.T) {
	// matchedTxn is the downloaded row an entry was merged with, which the
	// merge consumed; pairing on it would build false pairs.
	export := complete(t)
	delete(recordOf(t, export, "transactionStore", "x2"), "transfer")
	delete(recordOf(t, export, "transactionStore", "x3"), "transfer")
	recordOf(t, export, "transactionStore", "x2")["matchedTxn"] = "x3"
	out := mapped(t, export)
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.Equal(t, uuid.Nil, txnByStatement(t, out, "TRANSFER TO SAVINGS").TransferPairID)
	require.Equal(t, uuid.Nil, txnByStatement(t, out, "TRANSFER FROM CHECKING").TransferPairID)
}

func TestAReferenceWrittenAsAnObjectReadsTheSameAsABareId(t *testing.T) {
	out := mapped(t, edited(t, "transactionStore", "x2", map[string]any{
		"coa": map[string]any{"id": "c2", "type": "CATEGORY"},
	}))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.Equal(t, categoryNamed(t, out, "Transfer").ID,
		txnByStatement(t, out, "TRANSFER TO SAVINGS").CategoryID)
}

func TestABalanceSeriesUnderPointsOrHistoryIsRead(t *testing.T) {
	for _, field := range []string{"points", "history"} {
		export := complete(t)
		record := recordOf(t, export, "accountsBalancesStore", "b1")
		record[field] = record["balances"]
		delete(record, "balances")
		out := mapped(t, export)
		require.Empty(t, out.Report.Errors, out.Report.Render())
		require.Len(t, out.BalanceSnapshots, 2)
	}
}

// --- Simplifi's savings-goal container ---------------------------------------

// withGoalContainer moves the fixture's goal into a GOAL account of its own,
// which is how Simplifi actually holds one: the container has the money and
// Checking 1 carries the same figure again in goalBalance.
func withGoalContainer(t *testing.T) *Export {
	t.Helper()
	export := complete(t)

	accounts := storeOf(t, export, "accountsStore")
	accounts["gc"] = map[string]any{
		"id": "gc", "name": "Emergency Fund", "type": "GOAL", "subType": "UNKNOWN",
		"normalizedBalance": num("500"), "isExcludedFromReports": true,
		"isExcludedFromBudgets": true,
	}

	// The contribution as Simplifi writes it: the leg arriving in the container
	// is the one the goal names, and it is paired with the leg that left.
	txns := storeOf(t, export, "transactionStore")
	txns["gin"] = map[string]any{
		"id": "gin", "accountId": "gc", "postedOn": "2026-08-05", "amount": num("500"),
		"payee": "Emergency Fund", "transfer": "gout",
	}
	txns["gout"] = map[string]any{
		"id": "gout", "accountId": "a1", "postedOn": "2026-08-05", "amount": num("-500"),
		"payee": "Emergency Fund", "transfer": "gin",
	}

	goal := recordOf(t, export, "goalStore", "g1")
	goal["accountId"] = "gc"
	goal["txnIds"] = []any{"gin"}
	return export
}

func TestAGoalContainerIsNotImportedAsAnAccount(t *testing.T) {
	// Simplifi keeps a savings goal's money in an account of its own; ours
	// reserves inside a real account.
	out := mapped(t, withGoalContainer(t))
	require.Empty(t, out.Report.Errors, out.Report.Render())

	for _, account := range out.Accounts {
		require.NotEqual(t, "Emergency Fund", account.Name,
			"the container was imported as an account")
	}

	goal := out.Goals[0]
	checking := accountNamed(t, out, "Checking 1")
	require.Equal(t, checking.ID, goal.AccountID,
		"the goal reserves in the account whose goalBalance it matches")
}

func TestTheContributionSurvivesAsTheLegThatLeftTheRealAccount(t *testing.T) {
	// Dropping the container drops its half of the transfer, so the goal
	// comes to rest on the other half.
	out := mapped(t, withGoalContainer(t))
	require.Empty(t, out.Report.Errors, out.Report.Render())

	byID := map[uuid.UUID]*store.Transaction{}
	for _, txn := range out.Transactions {
		byID[txn.ID] = txn
	}

	goal := out.Goals[0]
	require.Len(t, goal.TxnIDs, 1)
	contribution, present := byID[goal.TxnIDs[0]]
	require.True(t, present, "the goal names a transaction that was not written")

	checking := accountNamed(t, out, "Checking 1")
	require.Equal(t, checking.ID, contribution.AccountID)
	require.Equal(t, "-500.00", contribution.Amount.String(),
		"the surviving leg is the money that left, which is the funding-leg shape")

	// And it is no longer a transfer: there is no second account it went to.
	require.Equal(t, uuid.Nil, contribution.TransferPairID,
		"a half-paired leg outliving its partner is the -150% savings-rate trap")
}

func TestAContainerWhoseReserveIsAmbiguousIsLeftAlone(t *testing.T) {
	// With no account carrying the figure, the container stays and the
	// report says why.
	export := withGoalContainer(t)
	account := recordOf(t, export, "accountsStore", "a1")
	account["goalBalance"] = num("0")

	out := mapped(t, export)
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.NotNil(t, accountNamed(t, out, "Emergency Fund"),
		"the container was dropped without anywhere to move the reserve to")
	require.Contains(t, out.Report.Render(), "goalBalance")
}

// A net-refunded envelope, which Simplifi sends as a positive figure, comes
// out negative: the refund gives the envelope headroom back.
func TestANetRefundedEnvelopeKeepsItsSignAfterTheFlip(t *testing.T) {
	export := complete(t)
	for _, record := range storeOf(t, export, "freeToSpendStore") {
		row, ok := record.(map[string]any)
		if !ok {
			continue
		}
		planned, ok := row["plannedSpendingItems"].([]any)
		if !ok || len(planned) == 0 {
			continue
		}
		planned[0].(map[string]any)["calculatedSpentAmount"] = json.Number("40")
	}

	out := mapped(t, export)
	require.Len(t, out.Envelopes, 1)
	require.Equal(t, "-40.00", out.Envelopes[0].CalculatedSpentAmount.String())
}
