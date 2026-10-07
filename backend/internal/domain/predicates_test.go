package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	predGroceries = Category{ID: "cat-groceries", Name: "Groceries", Kind: CategoryExpense}
	predSalary    = Category{ID: "cat-salary", Name: "Salary", Kind: CategoryIncome}
	predTransfer  = Category{ID: "cat-transfer", Name: "Transfer", Kind: CategoryTransfer}
)

func predAccount() Account {
	return Account{
		ID:                "acct-1",
		Name:              "Checking 1",
		Kind:              KindCash,
		Currency:          "USD",
		IncludeInNetWorth: true,
	}
}

func predTxn() Transaction {
	return Transaction{
		ID:            "txn-1",
		AccountID:     "acct-1",
		Date:          NewDate(2026, time.August, 15),
		Amount:        MustFromString("-25.00"),
		StatementName: "SQ *COFFEE 1234",
		Payee:         "Coffee",
		CategoryID:    predGroceries.ID,
		Source:        SourceSync,
		Currency:      "USD",
	}
}

func predPosting() Posting {
	return Posting{
		Txn:         predTxn(),
		Account:     predAccount(),
		Category:    predGroceries,
		HasCategory: true,
	}
}

func TestAnOrdinarySyncedExpenseCounts(t *testing.T) {
	require.True(t, CountsAsIncomeOrExpense(predPosting(), true))
}

func TestTransactionFlagsExcludeARowFromReports(t *testing.T) {
	cases := []struct {
		flag string
		set  func(*Transaction)
	}{
		{"is deleted", func(txn *Transaction) { txn.IsDeleted = true }},
		{"excluded from reports", func(txn *Transaction) { txn.ExcludedFromReports = true }},
	}
	for _, tc := range cases {
		t.Run(tc.flag, func(t *testing.T) {
			p := predPosting()
			tc.set(&p.Txn)
			require.False(t, CountsAsIncomeOrExpense(p, true))
		})
	}
}

func TestAccountLevelExclusionAppliesToEveryRowInIt(t *testing.T) {
	p := predPosting()
	p.Account.ExcludedFromReports = true
	require.False(t, CountsAsIncomeOrExpense(p, true))
}

func TestAnIgnoredAccountCountsNowhereWhateverItsFlagsSay(t *testing.T) {
	p := predPosting()
	p.Account.IsIgnored = true
	require.False(t, CountsAsIncomeOrExpense(p, true))
	require.False(t, CountsAsIncomeOrExpense(p, false), "not even for a screen that counts closed accounts")
	require.False(t, CountsTowardSpendingPlan(p, nil))
	// Money still moved: a balance walked through the account is unchanged.
	require.True(t, CountsTowardBalance(p))
}

func TestAClosedAccountCountsOnlyWhenTheCallerAsksForIt(t *testing.T) {
	closed := predPosting()
	closed.Account.IsClosed = true
	require.False(t, CountsAsIncomeOrExpense(closed, true))
	require.True(t, CountsAsIncomeOrExpense(closed, false))
}

func TestTransferCategoriesNeverCount(t *testing.T) {
	p := predPosting()
	p.Category = predTransfer
	require.False(t, CountsAsIncomeOrExpense(p, true))
}

func TestBothLegsOfAMatchedTransferAreExcluded(t *testing.T) {
	out, back := predPosting(), predPosting()
	out.Txn.Amount, back.Txn.Amount = MustFromString("-500.00"), MustFromString("500.00")
	out.HasCategory, back.HasCategory = false, false
	out.Txn.TransferPairID, back.Txn.TransferPairID = "pair-1", "pair-1"

	require.False(t, CountsAsIncomeOrExpense(out, true))
	require.False(t, CountsAsIncomeOrExpense(back, true))
}

func TestBookkeepingRowsAreNotSpending(t *testing.T) {
	for _, source := range []Source{SourceOpeningBalance, SourceBalanceAdjustment} {
		t.Run(string(source), func(t *testing.T) {
			p := predPosting()
			p.Txn.Source = source
			require.False(t, CountsAsIncomeOrExpense(p, true))
		})
	}
}

func TestAnImportedSimplifiRowIsOrdinaryHistory(t *testing.T) {
	p := predPosting()
	p.Txn.Source = SourceSimplifiImport
	require.True(t, CountsAsIncomeOrExpense(p, true))
}

func TestIncomeCountsTheSameWayAsExpense(t *testing.T) {
	p := predPosting()
	p.Txn.Amount = MustFromString("2500.00")
	p.Category, p.Txn.CategoryID = predSalary, predSalary.ID
	require.True(t, CountsAsIncomeOrExpense(p, true))
}

func TestTheTwoExclusionFlagsAreIndependent(t *testing.T) {
	// Ground rule 4: excluding from reports must not exclude from the plan.
	reportOnly := predPosting()
	reportOnly.Txn.ExcludedFromReports = true
	require.False(t, CountsAsIncomeOrExpense(reportOnly, true))
	require.True(t, CountsTowardSpendingPlan(reportOnly, nil))

	planOnly := predPosting()
	planOnly.Txn.ExcludedFromSpendingPlan = true
	require.True(t, CountsAsIncomeOrExpense(planOnly, true))
	require.False(t, CountsTowardSpendingPlan(planOnly, nil))
}

func TestAccountLevelPlanExclusionIsAlsoIndependent(t *testing.T) {
	p := predPosting()
	p.Account.ExcludedFromSpendingPlan = true
	require.False(t, CountsTowardSpendingPlan(p, nil))
	require.True(t, CountsAsIncomeOrExpense(p, true))
}

func TestThePerMonthExclusionListDropsARowWithoutMutatingIt(t *testing.T) {
	p := predPosting()
	require.True(t, CountsTowardSpendingPlan(p, nil))
	require.False(t, CountsTowardSpendingPlan(p, map[ID]bool{p.Txn.ID: true}))
	// The transaction itself is untouched, so it still counts elsewhere.
	require.False(t, p.Txn.ExcludedFromSpendingPlan)
}

func TestCreditCardPaymentsDoNotChargeTheUserTwice(t *testing.T) {
	payment := predPosting()
	payment.Txn.Amount = MustFromString("-400.00")
	payment.Txn.TransferPairID = "pair-cc"
	payment.HasCategory = false
	require.False(t, CountsTowardSpendingPlan(payment, nil))
}

func TestExcludingFromReportsDoesNotUndoMoneyLeavingTheBank(t *testing.T) {
	p := predPosting()
	p.Txn.ExcludedFromReports = true
	p.Txn.ExcludedFromSpendingPlan = true
	require.True(t, CountsTowardBalance(p))
}

func TestATransferLegStillMovesTheBalance(t *testing.T) {
	p := predPosting()
	p.Txn.TransferPairID = "pair-1"
	require.True(t, CountsTowardBalance(p))
}

func TestDeletedRowsDoNotMoveTheBalance(t *testing.T) {
	p := predPosting()
	p.Txn.IsDeleted = true
	require.False(t, CountsTowardBalance(p))
}

func TestPendingCountsTowardBalanceButIsNotSettled(t *testing.T) {
	p := predPosting()
	p.Txn.IsPending = true
	require.True(t, CountsTowardBalance(p))
	require.False(t, IsSettled(p))
}

// An estimate is the one row all three questions answer no to.
func TestAnEstimateCountsTowardNothing(t *testing.T) {
	p := predPosting()
	p.Txn.IsEstimate = true
	p.Txn.Date = NewDate(2027, time.April, 1)

	require.False(t, CountsAsIncomeOrExpense(p, true))
	require.False(t, CountsTowardSpendingPlan(p, nil))
	require.False(t, CountsTowardBalance(p))
	require.False(t, IsSettled(p))
}

func TestAnEstimateIsNotAPendingCharge(t *testing.T) {
	estimate := predPosting()
	estimate.Txn.IsEstimate = true
	pending := predPosting()
	pending.Txn.IsPending = true

	// The two look alike on a screen and mean opposite things: the bank has
	// taken the pending one and has never heard of the estimate.
	require.False(t, CountsTowardBalance(estimate))
	require.True(t, CountsTowardBalance(pending))
}

func TestReportsReadTheEffectiveDate(t *testing.T) {
	charge := predTxn()
	charge.EffectiveDate = NewDate(2026, time.September, 5)
	require.Equal(t, NewDate(2026, time.September, 5), ReportingDate(charge, DateEffective))
}

func TestTheRegisterReadsThePostedDate(t *testing.T) {
	charge := predTxn()
	charge.EffectiveDate = NewDate(2026, time.September, 5)
	require.Equal(t, NewDate(2026, time.August, 15), ReportingDate(charge, DatePosted))
}

func TestEffectiveFallsBackToPostedWhenUnset(t *testing.T) {
	require.Equal(t, NewDate(2026, time.August, 15), ReportingDate(predTxn(), DateEffective))
}

func TestAnUnnamedDateModeIsThePostedDate(t *testing.T) {
	charge := predTxn()
	charge.EffectiveDate = NewDate(2026, time.September, 5)
	require.Equal(t, NewDate(2026, time.August, 15), ReportingDate(charge, ""))
}

func TestResolvePostingsJoinsTransactionsToAccountsAndCategories(t *testing.T) {
	acct := predAccount()
	rows, err := ResolvePostings(
		[]Transaction{predTxn()},
		map[ID]Account{acct.ID: acct},
		map[ID]Category{predGroceries.ID: predGroceries},
	)
	require.NoError(t, err)
	require.Equal(t, acct, rows[0].Account)
	require.Equal(t, predGroceries, rows[0].Category)
	require.True(t, rows[0].HasCategory)
}

func TestAnUnknownAccountErrorsRatherThanDroppingTheRow(t *testing.T) {
	// Silently skipping turns a broken import into a wrong total.
	txn := predTxn()
	txn.AccountID = "acct-missing"
	_, err := ResolvePostings([]Transaction{txn}, nil, nil)
	require.ErrorContains(t, err, "unknown account")
}

func TestAnUncategorizedTransactionResolvesWithNoCategory(t *testing.T) {
	acct := predAccount()
	txn := predTxn()
	txn.CategoryID = ""
	rows, err := ResolvePostings([]Transaction{txn}, map[ID]Account{acct.ID: acct}, nil)
	require.NoError(t, err)
	require.False(t, rows[0].HasCategory)
}

func TestAnUnknownCategoryErrorsRatherThanDroppingTheRow(t *testing.T) {
	// A missing transfer category would start counting as income or expense.
	acct := predAccount()
	txn := predTxn()
	txn.CategoryID = "cat-missing"
	_, err := ResolvePostings(
		[]Transaction{txn},
		map[ID]Account{acct.ID: acct},
		map[ID]Category{},
	)
	require.ErrorContains(t, err, "unknown category")
}

func TestALegWhosePartnerWasDeletedIsFound(t *testing.T) {
	survivor, partner := predTxn(), predTxn()
	survivor.ID, partner.ID = "txn-a", "txn-b"
	survivor.Amount, partner.Amount = MustFromString("-500"), MustFromString("500")
	survivor.TransferPairID, partner.TransferPairID = "pair-1", "pair-1"
	partner.IsDeleted = true

	require.Equal(t,
		[]Transaction{survivor},
		FindOrphanTransferLegs([]Transaction{survivor, partner}))
}

func TestAnIntactPairIsNotAnOrphan(t *testing.T) {
	a, b := predTxn(), predTxn()
	a.ID, b.ID = "txn-a", "txn-b"
	a.TransferPairID, b.TransferPairID = "pair-1", "pair-1"
	require.Empty(t, FindOrphanTransferLegs([]Transaction{a, b}))
}

func TestUnpairedTransactionsAreIgnoredByTheOrphanScan(t *testing.T) {
	require.Empty(t, FindOrphanTransferLegs([]Transaction{predTxn()}))
}

func TestAThreeLegGroupIsReportedWholeSoItCanBeReleased(t *testing.T) {
	// Three rows sharing one token cannot all be paired with each other; the
	// only honest recovery is releasing every leg for a re-match.
	a, b, c := predTxn(), predTxn(), predTxn()
	a.ID, b.ID, c.ID = "txn-a", "txn-b", "txn-c"
	a.TransferPairID, b.TransferPairID, c.TransferPairID = "pair-x", "pair-x", "pair-x"

	got := FindOrphanTransferLegs([]Transaction{a, b, c})
	require.Len(t, got, 3)

	// Two rows on one token are still a healthy pair.
	pair1, pair2 := predTxn(), predTxn()
	pair1.ID, pair2.ID = "txn-d", "txn-e"
	pair1.TransferPairID, pair2.TransferPairID = "pair-ok", "pair-ok"
	require.Empty(t, FindOrphanTransferLegs([]Transaction{pair1, pair2}))
}

// The category's own pair (ground rule 4).

func TestACategoryExcludedFromReportsStopsCountingAsIncomeOrExpense(t *testing.T) {
	p := predPosting()
	p.Category.ExcludedFromReports = true
	require.False(t, CountsAsIncomeOrExpense(p, true))
}

func TestACategoryExcludedFromTheSpendingPlanStopsConsumingFreeToSpend(t *testing.T) {
	p := predPosting()
	p.Category.ExcludedFromSpendingPlan = true
	require.False(t, CountsTowardSpendingPlan(p, nil))
}

func TestTheCategorysTwoExclusionFlagsAreIndependentOfEachOther(t *testing.T) {
	reportOnly := predPosting()
	reportOnly.Category.ExcludedFromReports = true
	require.False(t, CountsAsIncomeOrExpense(reportOnly, true))
	require.True(t, CountsTowardSpendingPlan(reportOnly, nil))

	planOnly := predPosting()
	planOnly.Category.ExcludedFromSpendingPlan = true
	require.True(t, CountsAsIncomeOrExpense(planOnly, true))
	require.False(t, CountsTowardSpendingPlan(planOnly, nil))
}

func TestTheCategorysExclusionIsIndependentOfTheAccountsAndTheRows(t *testing.T) {
	// Three levels, six flags, and no level may stand in for another: a row
	// whose category is excluded must not come back because the account and
	// the transaction are both clean.
	p := predPosting()
	require.False(t, p.Txn.ExcludedFromReports)
	require.False(t, p.Account.ExcludedFromReports)
	p.Category.ExcludedFromReports = true
	require.False(t, CountsAsIncomeOrExpense(p, true))
}

func TestAnUncategorizedRowIsUnaffectedByAnyCategorysFlags(t *testing.T) {
	// Uncategorized spending is real spending. The clause is only asked of a
	// row that has a category to ask it of.
	p := predPosting()
	p.HasCategory, p.Txn.CategoryID = false, ""
	p.Category = Category{ExcludedFromReports: true, ExcludedFromSpendingPlan: true}
	require.True(t, CountsAsIncomeOrExpense(p, true))
	require.True(t, CountsTowardSpendingPlan(p, nil))
}

// The category-level rule on its own, which is what the split paths ask.

func TestTheCategoryRuleIsTheOneTheRowPredicatesAsk(t *testing.T) {
	cases := []struct {
		name     string
		category Category
		reports  bool
		plan     bool
	}{
		{"ordinary expense", predGroceries, true, true},
		{"transfer", predTransfer, false, false},
		{"excluded from reports", Category{Kind: CategoryExpense, ExcludedFromReports: true}, false, true},
		{"excluded from the plan", Category{Kind: CategoryExpense, ExcludedFromSpendingPlan: true}, true, false},
		{"excluded from both", Category{
			Kind:                     CategoryExpense,
			ExcludedFromReports:      true,
			ExcludedFromSpendingPlan: true,
		}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.reports, tc.category.CountsAsIncomeOrExpense())
			require.Equal(t, tc.plan, tc.category.CountsTowardSpendingPlan())

			// And the row predicates answer the same, for a row that is
			// otherwise ordinary — one rule, asked at two altitudes.
			p := predPosting()
			p.Category = tc.category
			require.Equal(t, tc.reports, CountsAsIncomeOrExpense(p, true))
			require.Equal(t, tc.plan, CountsTowardSpendingPlan(p, nil))
		})
	}
}

func TestATransferIsEitherAMatchedLegOrATransferCategory(t *testing.T) {
	cases := []struct {
		name string
		set  func(*Posting)
		want bool
	}{
		{"an ordinary categorized expense", func(*Posting) {}, false},
		{"a leg of a matched pair", func(p *Posting) { p.Txn.TransferPairID = "pair-1" }, true},
		{"filed under a transfer category", func(p *Posting) { p.Category = predTransfer }, true},
		{"both at once", func(p *Posting) {
			p.Txn.TransferPairID, p.Category = "pair-1", predTransfer
		}, true},
		{"an uncategorized row that never matched", func(p *Posting) {
			p.HasCategory, p.Txn.CategoryID = false, ""
		}, false},
		{"an uncategorized leg of a matched pair", func(p *Posting) {
			p.HasCategory, p.Txn.CategoryID = false, ""
			p.Txn.TransferPairID = "pair-1"
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := predPosting()
			tc.set(&p)
			require.Equal(t, tc.want, p.IsTransfer())
		})
	}
}

func TestAZeroCategoryOnAnUncategorizedRowIsNotATransfer(t *testing.T) {
	// Reading Category without HasCategory is right only while the zero Kind
	// is not CategoryTransfer.
	p := predPosting()
	p.HasCategory, p.Txn.CategoryID, p.Category = false, "", Category{}
	require.False(t, p.IsTransfer())
}

func TestTheRowPredicatesAgreeWithIsTransfer(t *testing.T) {
	for _, set := range []func(*Posting){
		func(p *Posting) { p.Txn.TransferPairID = "pair-1" },
		func(p *Posting) { p.Category = predTransfer },
	} {
		p := predPosting()
		set(&p)
		require.True(t, p.IsTransfer())
		require.False(t, CountsAsIncomeOrExpense(p, true))
		require.False(t, CountsTowardSpendingPlan(p, nil))
	}
}
