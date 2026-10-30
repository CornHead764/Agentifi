package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpendingWindowBoundaries(t *testing.T) {
	// Expected total is worked by hand from the window rule in docs/calculations.md.
	start := spendingDay(time.June, 1)
	end := spendingDay(time.June, 30)
	window := PeriodWindow(PeriodOf(start, GrainMonth), end)
	first := spendingPosting("first", "-10.00", aggFood, start)
	last := spendingPosting("last", "5.00", aggFood, end)
	split := spendingPosting("split", "-90.00", Category{}, start)
	split.Txn.Splits = []Split{
		{ID: "food", Amount: MustFromString("-30.00"), CategoryID: aggFood.ID},
		{ID: "home", Amount: MustFromString("-60.00"), CategoryID: aggHousehold.ID},
	}
	excluded := spendingPosting("excluded", "-100.00", aggPrivate, start)
	before := spendingPosting("before", "-200.00", aggFood, spendingDay(time.May, 31))
	after := spendingPosting("after", "-300.00", aggFood, spendingDay(time.July, 1))
	postings := []Posting{before, first, split, excluded, last, after}
	opts := aggSpending(AggregateByCategory)
	opts.Partial = map[ID][]Match{split.Txn.ID: {Allocations(split)[0]}}

	got := SpendingIn(postings, window, opts)
	// $10 spent plus the $30 selected split, less a $5 refund.
	require.Equal(t, "-35.00", got.Total.String())
	require.Equal(t, 3, got.Count)
	require.Equal(t, map[string]string{"Food & Dining": "-35.00"}, bucketTotals(got.Buckets))
	require.Len(t, got.Months, 1)
	require.Equal(t, "2026-06", got.Months[0].Month)
	require.Equal(t, Aggregate([]Posting{first, split, excluded, last}, opts), got)
}

func TestSpendingWindowExcludesOutside(t *testing.T) {
	window := PeriodWindow(PeriodOf(spendingDay(time.June, 1), GrainMonth), spendingDay(time.June, 30))
	opts := aggSpending(AggregateByCategory)
	postings := make([]Posting, 2000)
	for i := range postings {
		postings[i] = spendingPosting("outside", "-10.00", aggFood, spendingDay(time.May, 1))
	}
	empty := testing.AllocsPerRun(10, func() { SpendingIn(nil, window, opts) })
	outside := testing.AllocsPerRun(10, func() { SpendingIn(postings, window, opts) })
	require.Equal(t, empty, outside)
}
