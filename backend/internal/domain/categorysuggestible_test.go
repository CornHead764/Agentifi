package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestUncategorizedIsNeverASuggestibleCategory(t *testing.T) {
	for _, name := range []string{"Uncategorized", "uncategorized", "  UNCATEGORIZED "} {
		require.False(t, domain.Category{Name: name}.CanBeSuggested(), name)
	}
	for _, name := range []string{"Groceries", "Uncategorized Gifts", "", "Categorized"} {
		require.True(t, domain.Category{Name: name}.CanBeSuggested(), name)
	}
}

func TestMoneyOutNeverTakesAnIncomeCategory(t *testing.T) {
	out, in := domain.MustFromString("-12.00"), domain.MustFromString("12.00")
	income := domain.Category{Name: "Paycheck", Kind: domain.CategoryIncome}
	expense := domain.Category{Name: "Groceries", Kind: domain.CategoryExpense}
	transfer := domain.Category{Name: "Transfer", Kind: domain.CategoryTransfer}

	require.False(t, income.FitsDirection(out, false))
	require.True(t, expense.FitsDirection(out, false))
	require.True(t, transfer.FitsDirection(out, false))
	require.True(t, income.FitsDirection(in, false))
	require.True(t, expense.FitsDirection(in, false), "a refund goes back under what was bought")
	require.True(t, income.FitsDirection(out, true), "a loan's rows run the other way")
	require.True(t, income.FitsDirection(domain.Zero, false))
}
