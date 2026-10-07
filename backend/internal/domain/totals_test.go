package domain

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSummarizeAllocationsSortsByKindNotSign(t *testing.T) {
	summary := SummarizeAllocations([]ReportedAmount{
		{Amount: MustFromString("1000.00"), Kind: CategoryIncome},
		{Amount: MustFromString("-600.00"), Kind: CategoryExpense, IsBillOrSubscription: true},
		{Amount: MustFromString("-200.00"), Kind: CategoryExpense},
		// A store return under Groceries lowers spending, not raises income.
		{Amount: MustFromString("50.00"), Kind: CategoryExpense},
		{Amount: MustFromString("-300.00"), Kind: CategoryTransfer},
	})

	require.Equal(t, "1000.00", summary.Income.String())
	require.Equal(t, "-750.00", summary.Expenses.String())
	require.Equal(t, "-600.00", summary.Bills.String())
	require.Equal(t, "-150.00", summary.Discretionary.String())
	require.Equal(t, "250.00", summary.Net.String())
	require.True(t, summary.HasSavingsRate)
	require.True(t, summary.SavingsRate.Equal(decimal.RequireFromString("0.25")), summary.SavingsRate.String())
	require.Equal(t, 5, summary.Count)
}

func TestSummarizeAllocationsHasNoSavingsRateWithoutIncome(t *testing.T) {
	summary := SummarizeAllocations([]ReportedAmount{
		{Amount: MustFromString("-40.00"), Kind: CategoryExpense},
	})

	require.Equal(t, "0.00", summary.Income.String())
	require.Equal(t, "-40.00", summary.Net.String())
	require.False(t, summary.HasSavingsRate)
}

func TestSummarizeOccurrencesReadsTheSeriesKind(t *testing.T) {
	summary := SummarizeOccurrences([]Occurrence{
		{Kind: SeriesIncome, Amount: MustFromString("2000.00")},
		{Kind: SeriesBill, Amount: MustFromString("-120.00")},
		{Kind: SeriesSubscription, Amount: MustFromString("-15.00")},
		// An expected refund is money coming in, but not earnings.
		{Kind: SeriesRefund, Amount: MustFromString("35.00")},
		// The card payment's purchases were counted when they posted.
		{Kind: SeriesCreditCardPayment, Amount: MustFromString("-900.00")},
		{Kind: SeriesTransfer, Amount: MustFromString("-500.00")},
	})

	require.Equal(t, "2000.00", summary.Income.String())
	require.Equal(t, "-100.00", summary.Expenses.String())
	require.Equal(t, "1900.00", summary.Net.String())
}
