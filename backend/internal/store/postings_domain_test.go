package store

import (
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The row-to-domain mapping is the only place these flags cross, and an
// omitted field fails silently: the calculation reads the zero value and every
// report counts a category the user excluded.

func TestDomainCategoryCarriesTheExclusionFlagsTheCalculationsRead(t *testing.T) {
	row := Category{
		ID:                       uuid.New(),
		Name:                     "Reimbursable",
		Kind:                     "expense",
		ExcludedFromReports:      true,
		ExcludedFromSpendingPlan: true,
	}

	got := DomainCategory(row)
	require.True(t, got.ExcludedFromReports)
	require.True(t, got.ExcludedFromSpendingPlan)
	require.False(t, got.CountsAsIncomeOrExpense())
	require.False(t, got.CountsTowardSpendingPlan())
}

func TestDomainCategoryLeavesAnUnflaggedCategoryCounting(t *testing.T) {
	got := DomainCategory(Category{ID: uuid.New(), Name: "Groceries", Kind: "expense"})
	require.True(t, got.CountsAsIncomeOrExpense())
	require.True(t, got.CountsTowardSpendingPlan())
}

func TestDomainCategoryKeepsTheTwoFlagsIndependent(t *testing.T) {
	reports := DomainCategory(Category{ID: uuid.New(), Kind: "expense", ExcludedFromReports: true})
	require.False(t, reports.CountsAsIncomeOrExpense())
	require.True(t, reports.CountsTowardSpendingPlan())

	plan := DomainCategory(Category{ID: uuid.New(), Kind: "expense", ExcludedFromSpendingPlan: true})
	require.True(t, plan.CountsAsIncomeOrExpense())
	require.False(t, plan.CountsTowardSpendingPlan())
}

func TestDomainAccountStillCarriesItsOwnPair(t *testing.T) {
	got := DomainAccount(Account{
		ID:                       uuid.New(),
		ExcludedFromReports:      true,
		ExcludedFromSpendingPlan: true,
	})
	require.True(t, got.ExcludedFromReports)
	require.True(t, got.ExcludedFromSpendingPlan)
}

// store.IsTransfer serves callers holding a bare row and delegates to the
// domain, so it cannot drift from domain.Posting.IsTransfer.

func TestIsTransferAnswersBothShapesFromABareRow(t *testing.T) {
	transfer := Category{ID: uuid.New(), Name: "Transfer", Kind: "transfer"}
	groceries := Category{ID: uuid.New(), Name: "Groceries", Kind: "expense"}
	categories := map[uuid.UUID]Category{transfer.ID: transfer, groceries.ID: groceries}

	cases := []struct {
		name string
		txn  Transaction
		want bool
	}{
		{"an ordinary categorized row", Transaction{CategoryID: groceries.ID}, false},
		{"an uncategorized row", Transaction{}, false},
		{"a matched pair leg", Transaction{TransferPairID: uuid.New()}, true},
		{"filed under a transfer category, never matched",
			Transaction{CategoryID: transfer.ID}, true},
		{"both at once",
			Transaction{CategoryID: transfer.ID, TransferPairID: uuid.New()}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, IsTransfer(tc.txn, categories))
		})
	}
}

func TestIsTransferTreatsACategoryItWasNotGivenAsUncategorized(t *testing.T) {
	// A partial map must not start calling rows transfers, and must not stop
	// the pair half from answering.
	transfer := Category{ID: uuid.New(), Kind: "transfer"}
	require.False(t, IsTransfer(Transaction{CategoryID: transfer.ID}, map[uuid.UUID]Category{}))
	require.True(t, IsTransfer(
		Transaction{CategoryID: transfer.ID, TransferPairID: uuid.New()},
		map[uuid.UUID]Category{}))
}

// MoneyMoved and MoneyMovedOn are pasted into hand-written SQL, so what they
// render is part of the queries' meaning.

func TestMoneyMovedRendersBothClausesInBothForms(t *testing.T) {
	require.Equal(t, "NOT is_deleted AND estimate_status IS NULL", MoneyMoved)
	require.Equal(t, "NOT t.is_deleted AND t.estimate_status IS NULL", MoneyMovedOn("t"))
	require.Equal(t, MoneyMoved, MoneyMovedOn(""),
		"the unaliased form is the aliased one with no qualifier, not a second spelling")
}

func TestMoneyMovedNamesBothColumnsWhicheverFormIsUsed(t *testing.T) {
	// A Simplifi forecast is not deleted, so `NOT is_deleted` alone admits
	// every one.
	for _, alias := range []string{"", "t", "txn"} {
		rendered := MoneyMovedOn(alias)
		require.Contains(t, rendered, "is_deleted")
		require.Contains(t, rendered, "estimate_status IS NULL")
	}
}

func TestTheMerchantMatchableClauseStillCarriesTheLivenessHalf(t *testing.T) {
	require.Contains(t, merchantMatchable(domain.MerchantAmazon), MoneyMoved)
}
