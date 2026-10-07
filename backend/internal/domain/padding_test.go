package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A $6.50 lunch deducted from pay, and the $6.50 of pay the mail
// rule records beside it (invented). The pad is folded out of a list and
// counted by every figure.

func paddingPair() (purchase, pad Posting) {
	lunch := Category{ID: "cat-lunch", Name: "Lunch", Kind: CategoryExpense}
	paycheck := Category{ID: "cat-paycheck", Name: "Paycheck", Kind: CategoryIncome}
	purchase = Posting{
		Txn:     Transaction{ID: "txn-lunch", Amount: MustFromString("-6.50"), CategoryID: lunch.ID},
		Account: predAccount(), Category: lunch, HasCategory: true,
	}
	pad = Posting{
		Txn: Transaction{
			ID: "txn-pad", Amount: MustFromString("6.50"), CategoryID: paycheck.ID,
			PaddedTxnID: purchase.Txn.ID,
		},
		Account: predAccount(), Category: paycheck, HasCategory: true,
	}
	return purchase, pad
}

func TestPaddingIsFoldedOutOfAListInOrder(t *testing.T) {
	purchase, pad := paddingPair()
	other := Posting{Txn: Transaction{ID: "txn-other", Amount: MustFromString("-12.00")}, Account: predAccount()}

	shown, padding := FoldPadding([]Posting{pad, purchase, other})
	require.Equal(t, []Posting{purchase, other}, shown)
	require.Equal(t, []Posting{pad}, padding)
	require.Equal(t, MustFromString("6.50"), Sum(padding, Posting.Amount))

	shown, padding = FoldPadding([]Posting{purchase})
	require.Equal(t, []Posting{purchase}, shown)
	require.Empty(t, padding)
}

func TestPaddingStillCountsAsIncomeEverywhere(t *testing.T) {
	purchase, pad := paddingPair()
	require.True(t, pad.Txn.IsPadding())
	require.False(t, purchase.Txn.IsPadding(), "the purchase is not the pad")

	require.True(t, CountsTowardBalance(pad))
	require.True(t, CountsAsIncomeOrExpense(pad, false))
	require.True(t, CountsTowardSpendingPlan(pad, nil))
	require.Equal(t, CategoryIncome, LedgerKind(pad.Category, pad.HasCategory))
	require.True(t, Sum([]Posting{purchase, pad}, Posting.Amount).IsZero(),
		"the pair nets to nothing against the balance")
}
