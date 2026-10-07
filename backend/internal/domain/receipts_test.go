package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The rule, as docs/data-model.md states it: on an account that requires
// receipts, a row that spends money needs one; money arriving, a transfer, a
// forecast, an opening balance and a revaluation do not; a person may settle a
// row as needing none; any document behind the row is its receipt.

var (
	hsaPharmacy = Category{ID: "cat-pharmacy", Name: "Pharmacy", Kind: CategoryExpense}
	hsaTransfer = Category{ID: "cat-transfer", Name: "Transfer", Kind: CategoryTransfer}
	hsaInterest = Category{ID: "cat-interest", Name: "Interest", Kind: CategoryIncome}

	hsaCategories = map[ID]Category{
		hsaPharmacy.ID: hsaPharmacy, hsaTransfer.ID: hsaTransfer, hsaInterest.ID: hsaInterest,
	}
)

func hsaPosting(amount string, category Category) Posting {
	return Posting{
		Txn: Transaction{
			ID: "txn-hsa", AccountID: "acct-hsa", Date: NewDate(2026, time.March, 4),
			Amount: MustFromString(amount), StatementName: "DEBIT CARD PURCHASE",
			CategoryID: category.ID, Source: SourceSync, Currency: "USD",
		},
		Account:     Account{ID: "acct-hsa", Name: "Health savings", Kind: KindCash, RequiresReceipts: true},
		Category:    category,
		HasCategory: true,
	}
}

func TestABankingHSARequiresReceiptsUntilTheHouseholdSaysOtherwise(t *testing.T) {
	require.True(t, AccountRequiresReceipts("hsa", KindCash, "Health savings", false, false))
	require.False(t, AccountRequiresReceipts("hsa", KindCash, "Health savings", false, true))
	require.False(t, AccountRequiresReceipts("checking", KindCash, "Everyday checking", false, false))
	require.True(t, AccountRequiresReceipts("credit_card", KindCreditCard, "Rewards card", true, true))
}

func TestAnInvestedHSARequiresReceiptsOnlyWhenTheHouseholdSaysSo(t *testing.T) {
	require.False(t, AccountRequiresReceipts("hsa_investment", KindInvestment, "HSA investments", false, false),
		"its outflows buy funds or go back to the cash side")
	require.True(t, AccountRequiresReceipts("hsa_investment", KindInvestment, "HSA investments", true, true))
	require.False(t, AccountRequiresReceipts("hsa", KindInvestment, "Health savings", false, false),
		"the kind decides: an invested account is not read as the cash side")
}

func TestACashAccountNamedAsAnHSAOrFSARequiresReceipts(t *testing.T) {
	require.True(t, AccountRequiresReceipts("checking", KindCash, "Family HSA", false, false))
	require.True(t, AccountRequiresReceipts("checking", KindCash, "Dependent-care FSA (work)", false, false))
	require.False(t, AccountRequiresReceipts("checking", KindCash, "Family HSA", false, true),
		"the household's own setting wins")
	require.False(t, AccountRequiresReceipts("other_investment", KindInvestment, "HSA Brokerage", false, false),
		"the invested side buys funds")
	require.False(t, AccountRequiresReceipts("checking", KindCash, "Hsavings plus", false, false),
		"a whole word only")
}

func TestASpendingRowWithNoDocumentIsMissingItsReceipt(t *testing.T) {
	posting := hsaPosting("-42.00", hsaPharmacy)
	require.Equal(t, ReceiptMissing, ReceiptStatusOf(posting, hsaCategories, false))
	require.Equal(t, ReceiptOnFile, ReceiptStatusOf(posting, hsaCategories, true))
}

func TestAnUncategorizedSpendingRowStillNeedsAReceipt(t *testing.T) {
	posting := hsaPosting("-18.00", Category{})
	posting.Txn.CategoryID, posting.HasCategory = "", false
	require.Equal(t, ReceiptMissing, ReceiptStatusOf(posting, hsaCategories, false))
}

func TestAPendingChargeNeedsAReceiptAlready(t *testing.T) {
	posting := hsaPosting("-42.00", hsaPharmacy)
	posting.Txn.IsPending = true
	require.Equal(t, ReceiptMissing, ReceiptStatusOf(posting, hsaCategories, false))
}

func TestRowsThatSpendNothingNeedNoReceipt(t *testing.T) {
	cases := []struct {
		name    string
		posting func() Posting
	}{
		{"an account that does not require them", func() Posting {
			p := hsaPosting("-42.00", hsaPharmacy)
			p.Account.RequiresReceipts = false
			return p
		}},
		{"an ignored account", func() Posting {
			p := hsaPosting("-42.00", hsaPharmacy)
			p.Account.IsIgnored = true
			return p
		}},
		{"interest paid in", func() Posting { return hsaPosting("0.37", hsaInterest) }},
		{"a contribution paired with its checking leg", func() Posting {
			p := hsaPosting("150.00", hsaTransfer)
			p.Txn.TransferPairID = "pair-1"
			return p
		}},
		{"money sent to an investment account", func() Posting {
			return hsaPosting("-500.00", hsaTransfer)
		}},
		{"an unfiled outflow paired as a transfer", func() Posting {
			p := hsaPosting("-500.00", hsaPharmacy)
			p.Txn.TransferPairID = "pair-2"
			return p
		}},
		{"a forecast", func() Posting {
			p := hsaPosting("-42.00", hsaPharmacy)
			p.Txn.IsEstimate = true
			return p
		}},
		{"a deleted row", func() Posting {
			p := hsaPosting("-42.00", hsaPharmacy)
			p.Txn.IsDeleted = true
			return p
		}},
		{"an opening balance", func() Posting {
			p := hsaPosting("-42.00", Category{})
			p.HasCategory, p.Txn.CategoryID, p.Txn.Source = false, "", SourceOpeningBalance
			return p
		}},
		{"a zero amount", func() Posting { return hsaPosting("0.00", hsaPharmacy) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, ReceiptNotRequired, ReceiptStatusOf(c.posting(), hsaCategories, false))
		})
	}
}

func TestAReceiptDismissedByHandSettlesTheRow(t *testing.T) {
	posting := hsaPosting("-3.00", hsaPharmacy)
	posting.Txn.ReceiptNotNeeded = true
	require.Equal(t, ReceiptNotNeeded, ReceiptStatusOf(posting, hsaCategories, false))
	require.Equal(t, ReceiptOnFile, ReceiptStatusOf(posting, hsaCategories, true),
		"a document behind the row is its receipt whatever else was said")
}

func TestASplitRowIsATransferOnlyWhenEverySplitIs(t *testing.T) {
	posting := hsaPosting("-200.00", Category{})
	posting.HasCategory, posting.Txn.CategoryID = false, ""
	posting.Txn.Splits = []Split{
		{ID: "s1", Amount: MustFromString("-150.00"), CategoryID: hsaTransfer.ID},
		{ID: "s2", Amount: MustFromString("-50.00"), CategoryID: hsaPharmacy.ID},
	}
	require.Equal(t, ReceiptMissing, ReceiptStatusOf(posting, hsaCategories, false))

	posting.Txn.Splits[1].CategoryID = hsaTransfer.ID
	require.Equal(t, ReceiptNotRequired, ReceiptStatusOf(posting, hsaCategories, false))
}

func TestOnlyFilterCallersWithFacetsMayAskAboutReceipts(t *testing.T) {
	require.True(t, RuleCannotMatch(FieldIsMissingReceipt))
	filter := Filter{Items: []FilterItem{
		{Field: FieldIsMissingReceipt, Operator: OpIsTrue, State: true, HasState: true},
	}}
	require.NoError(t, filter.Validate())
	posting := hsaPosting("-42.00", hsaPharmacy)
	require.True(t, Matches(filter, posting, Facets{MissingReceipt: true}, DatePosted))
	require.False(t, Matches(filter, posting, Facets{}, DatePosted))
}
