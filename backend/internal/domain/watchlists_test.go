package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

var (
	wlToday     = NewDate(2026, time.August, 21)
	wlHousehold = Category{ID: "cat-household", Name: "Household", Kind: CategoryExpense}
)

func wlWatchlist(mutate ...func(*Watchlist)) Watchlist {
	watchlist := Watchlist{ID: "wl-1", Name: "Groceries", FilterID: "flt-1", TrendMonths: 12}
	for _, apply := range mutate {
		apply(&watchlist)
	}
	return watchlist
}

// wlMatcher stands in for the filter engine: a split-aware category facet.
func wlMatcher(categoryIDs ...ID) WatchlistMatcher {
	wanted := map[ID]bool{}
	for _, id := range categoryIDs {
		wanted[id] = true
	}
	if len(wanted) == 0 {
		wanted[envGroceriesCategory.ID] = true
	}
	return func(_ Watchlist, posting Posting, _ DateMode) []WatchlistPart {
		if len(posting.Txn.Splits) == 0 {
			if wanted[posting.Txn.CategoryID] {
				return []WatchlistPart{{Posting: posting, Amount: posting.Amount()}}
			}
			return nil
		}
		var parts []WatchlistPart
		for _, split := range posting.Txn.Splits {
			if wanted[split.CategoryID] {
				parts = append(parts, WatchlistPart{Posting: posting, Split: split, HasSplit: true, Amount: split.Amount})
			}
		}
		return parts
	}
}

func wlSpend(txnID, amount string, on Date, mutate ...func(*Posting)) Posting {
	return planPosting(txnID, amount, on, mutate...)
}

// wlRows are two spends and a refund this month, a spend in March, one last
// December.
func wlRows() []Posting {
	return []Posting{
		wlSpend("t-1", "-100.00", NewDate(2026, time.August, 3)),
		wlSpend("t-2", "-50.00", NewDate(2026, time.August, 10)),
		wlSpend("t-3", "20.00", NewDate(2026, time.August, 12)),
		wlSpend("t-4", "-300.00", NewDate(2026, time.March, 5)),
		wlSpend("t-5", "-75.00", NewDate(2025, time.December, 20)),
	}
}

func TestTheWatchlistSumsTheMatchingRowsInTheCurrentMonth(t *testing.T) {
	spent := WatchlistThisMonthSpent(wlWatchlist(), wlRows(), wlMatcher(), wlToday, DateEffective)

	require.Equal(t, "130.00", spent.String())
}

func TestARefundReducesThisMonthsSpendRatherThanAddingToIt(t *testing.T) {
	// calculations.md §13 records this departure from §7.
	var withoutRefund []Posting
	for _, row := range wlRows() {
		if row.Txn.ID != "t-3" {
			withoutRefund = append(withoutRefund, row)
		}
	}

	spent := WatchlistThisMonthSpent(wlWatchlist(), withoutRefund, wlMatcher(), wlToday, DateEffective)

	require.Equal(t, "150.00", spent.String())
}

func TestRowsOutsideTheFilterAreIgnored(t *testing.T) {
	spent := WatchlistThisMonthSpent(wlWatchlist(), wlRows(), wlMatcher(wlHousehold.ID), wlToday, DateEffective)

	require.Equal(t, "0.00", spent.String())
}

func TestARowExcludedFromReportsIsExcludedFromTheWatchlist(t *testing.T) {
	rows := append(wlRows(), wlSpend("t-x", "-40.00", NewDate(2026, time.August, 18), func(p *Posting) {
		p.Txn.ExcludedFromReports = true
	}))

	spent := WatchlistThisMonthSpent(wlWatchlist(), rows, wlMatcher(), wlToday, DateEffective)

	require.Equal(t, "130.00", spent.String())
}

func TestATransferLegIsNotSpending(t *testing.T) {
	rows := append(wlRows(), wlSpend("t-t", "-500.00", NewDate(2026, time.August, 19), func(p *Posting) {
		p.Category = planTransferCategory
		p.Txn.TransferPairID = "p"
	}))

	spent := WatchlistThisMonthSpent(wlWatchlist(), rows, wlMatcher(), wlToday, DateEffective)

	require.Equal(t, "130.00", spent.String())
}

func TestOnlyTheMatchingAllocationOfASplitRowIsCounted(t *testing.T) {
	receipt := wlSpend("t-split", "-200.00", NewDate(2026, time.August, 6), func(p *Posting) {
		p.Txn.Splits = []Split{
			{ID: "s-1", Amount: MustFromString("-50.00"), CategoryID: envGroceriesCategory.ID},
			{ID: "s-2", Amount: MustFromString("-150.00"), CategoryID: wlHousehold.ID},
		}
	})

	spent := WatchlistThisMonthSpent(wlWatchlist(), []Posting{receipt}, wlMatcher(), wlToday, DateEffective)

	require.Equal(t, "50.00", spent.String())
}

func TestTheWatchlistProjectsTheRunRateToTheEndOfTheMonth(t *testing.T) {
	// $130 over 21 elapsed days, carried across all 31 days of August.
	projection := SummarizeWatchlist(wlWatchlist(), wlRows(), wlMatcher(), wlToday, DateEffective).MonthProjection

	require.Equal(t, "191.90", projection.String())
}

func TestTheFirstOfTheMonthProjectsFromOneDayRatherThanDividingByZero(t *testing.T) {
	first := NewDate(2026, time.August, 1)
	rows := []Posting{wlSpend("t-1", "-10.00", first)}

	projection := SummarizeWatchlist(wlWatchlist(), rows, wlMatcher(), first, DateEffective).MonthProjection

	require.Equal(t, "310.00", projection.String())
}

func TestYearToDateRunsFromJanuaryFirstAndLeavesLastYearOut(t *testing.T) {
	ytd := WatchlistYearToDate(wlWatchlist(), wlRows(), wlMatcher(), wlToday, DateEffective)

	require.Equal(t, "430.00", ytd.String())
}

func TestTheTrendReturnsOneBarPerMonthEndingWithTheCurrentOne(t *testing.T) {
	trend := WatchlistMonthlyTrend(wlWatchlist(), wlRows(), wlMatcher(), wlToday, 3, DateEffective)

	require.Equal(t, []string{"2026-06", "2026-07", "2026-08"}, []string{
		trend[0].Month.String(), trend[1].Month.String(), trend[2].Month.String(),
	})
	require.Equal(t, []string{"0.00", "0.00", "130.00"}, []string{
		trend[0].Spent.String(), trend[1].Spent.String(), trend[2].Spent.String(),
	})
}

func TestTheCurrentMonthIsFlaggedPartial(t *testing.T) {
	trend := WatchlistMonthlyTrend(wlWatchlist(), wlRows(), wlMatcher(), wlToday, 3, DateEffective)

	require.Equal(t, []bool{false, false, true}, []bool{
		trend[0].IsPartial, trend[1].IsPartial, trend[2].IsPartial,
	})
}

func TestTheBarCountDefaultsToTheWatchlistsOwnSetting(t *testing.T) {
	watchlist := wlWatchlist(func(w *Watchlist) { w.TrendMonths = 4 })

	trend := WatchlistMonthlyTrend(watchlist, wlRows(), wlMatcher(), wlToday, 0, DateEffective)

	require.Len(t, trend, 4)
}

func TestTheTrailingAverageUsesFullMonthsOnly(t *testing.T) {
	// August 2025 through July 2026: ($300 + $75) / 12. August's partial $130
	// is left out.
	average := WatchlistTrailingAverage(wlWatchlist(), wlRows(), wlMatcher(), wlToday, 12, DateEffective)

	require.Equal(t, "31.25", average.String())
}

func TestAMonthWithNoSpendingStillDividesTheAverage(t *testing.T) {
	rows := []Posting{wlSpend("t-1", "-120.00", NewDate(2026, time.July, 4))}

	average := WatchlistTrailingAverage(wlWatchlist(), rows, wlMatcher(), wlToday, 12, DateEffective)

	require.Equal(t, "10.00", average.String())
}

func TestTheAverageWindowIsTheMonthsBeforeTheCurrentOne(t *testing.T) {
	justBefore := []Posting{wlSpend("t-old", "-60.00", NewDate(2025, time.July, 31))}

	average := WatchlistTrailingAverage(wlWatchlist(), justBefore, wlMatcher(), wlToday, 12, DateEffective)

	require.Equal(t, "0.00", average.String())
}

func wlBreakdownRows() []Posting {
	return []Posting{
		wlSpend("b-1", "-60.00", NewDate(2026, time.August, 2), func(p *Posting) { p.Txn.Payee = "Market" }),
		wlSpend("b-2", "-40.00", NewDate(2026, time.August, 4), func(p *Posting) {
			p.Txn.Payee = "Market"
			p.Txn.TagIDs = []ID{"tag-a"}
		}),
		wlSpend("b-3", "-100.00", NewDate(2026, time.August, 6), func(p *Posting) {
			p.Txn.Payee = "Hardware"
			p.Txn.CategoryID = wlHousehold.ID
			p.Category = wlHousehold
			p.Txn.TagIDs = []ID{"tag-a", "tag-b"}
		}),
	}
}

func wlBreakdown(dimension BreakdownDimension) []BreakdownRow {
	return WatchlistBreakdown(
		wlWatchlist(),
		wlBreakdownRows(),
		wlMatcher(envGroceriesCategory.ID, wlHousehold.ID),
		NewDate(2026, time.August, 1),
		NewDate(2026, time.August, 31),
		dimension,
		DateEffective,
	)
}

func TestTheBreakdownSplitsTheMonthByCategoryLargestFirst(t *testing.T) {
	rows := wlBreakdown(BreakdownByCategory)

	require.Len(t, rows, 2)
	require.Equal(t, string(envGroceriesCategory.ID), rows[0].Key)
	require.Equal(t, "100.00", rows[0].Spent.String())
	require.Equal(t, string(wlHousehold.ID), rows[1].Key)
	require.Equal(t, "100.00", rows[1].Spent.String())
}

func TestBreakdownSharesAreOfTheWindowsTotalSpend(t *testing.T) {
	byPayee := map[string]BreakdownRow{}
	for _, row := range wlBreakdown(BreakdownByPayee) {
		byPayee[row.Key] = row
	}

	require.Equal(t, "100.00", byPayee["Hardware"].Spent.String())
	require.True(t, byPayee["Hardware"].HasShare)
	require.Equal(t, "0.5", byPayee["Hardware"].Share.String())
}

func TestATwoTagRowCountsInFullUnderEachTagSoSharesCanPassOne(t *testing.T) {
	rows := wlBreakdown(BreakdownByTag)

	spentByTag := map[string]string{}
	totalShare := decimal.Zero
	for _, row := range rows {
		spentByTag[row.Key] = row.Spent.String()
		totalShare = totalShare.Add(row.Share)
	}

	require.Equal(t, map[string]string{"tag-a": "140.00", "tag-b": "100.00", "": "60.00"}, spentByTag)
	require.True(t, totalShare.GreaterThan(decimal.NewFromInt(1)))
}

func TestTheWatchlistCardReportsTheFourFiguresFromOnePass(t *testing.T) {
	summary := SummarizeWatchlist(wlWatchlist(), wlRows(), wlMatcher(), wlToday, DateEffective)

	require.Equal(t, "130.00", summary.ThisMonthSpent.String())
	require.Equal(t, "191.90", summary.MonthProjection.String())
	require.Equal(t, "430.00", summary.YearToDate.String())
	require.Equal(t, "31.25", summary.TwelveMonthAverage.String())
	require.Len(t, summary.MonthlyTrend, 12)
}

func TestATargetGivesTheRemainderAndTheShareUsed(t *testing.T) {
	watchlist := wlWatchlist(func(w *Watchlist) {
		w.TargetAmount = MustFromString("150")
		w.HasTarget = true
	})

	summary := SummarizeWatchlist(watchlist, wlRows(), wlMatcher(), wlToday, DateEffective)

	left, ok := summary.LeftToTarget()
	require.True(t, ok)
	require.Equal(t, "20.00", left.String())

	pct, ok := summary.PctOfTarget()
	require.True(t, ok)
	require.Equal(t, "86.67", pct.StringFixed(2))

	require.False(t, summary.IsOverTarget())
	require.True(t, summary.IsProjectedOverTarget())
}

func TestAnUnsetTargetReadsAsNoTargetRatherThanATargetOfZero(t *testing.T) {
	summary := SummarizeWatchlist(wlWatchlist(), wlRows(), wlMatcher(), wlToday, DateEffective)

	_, ok := summary.LeftToTarget()
	require.False(t, ok)

	_, ok = summary.PctOfTarget()
	require.False(t, ok)

	require.False(t, summary.IsOverTarget())
	require.False(t, summary.IsProjectedOverTarget())
}

func TestSpendingPastTheTargetReadsAsOver(t *testing.T) {
	watchlist := wlWatchlist(func(w *Watchlist) {
		w.TargetAmount = MustFromString("100")
		w.HasTarget = true
	})

	summary := SummarizeWatchlist(watchlist, wlRows(), wlMatcher(), wlToday, DateEffective)

	left, ok := summary.LeftToTarget()
	require.True(t, ok)
	require.Equal(t, "-30.00", left.String())
	require.True(t, summary.IsOverTarget())
}

func TestANamedMonthIsTheSameFigureTheTrendBarShows(t *testing.T) {
	spent := WatchlistSpentInMonth(wlWatchlist(), wlRows(), wlMatcher(), NewMonth(2026, time.March), DateEffective)

	require.Equal(t, "300.00", spent.String())
}

// The breakdown groups by CategoryID and the exclusion rule reads Category, so
// the two must name the same category.

func TestAPartsResolvedCategoryIsTheOneItsCategoryIDNames(t *testing.T) {
	other := Category{ID: "cat-other", Name: "Other", Kind: CategoryExpense}
	categories := map[ID]Category{
		envGroceriesCategory.ID: envGroceriesCategory,
		other.ID:                other,
	}

	whole := wlSpend("t-1", "-100.00", NewDate(2026, time.August, 3))
	part := NewPart(whole, Match{Posting: whole, Amount: whole.Amount()}, categories)
	require.True(t, part.HasCategory)
	require.Equal(t, part.CategoryID(), part.Category.ID)

	split := Split{ID: "s-1", Amount: MustFromString("-40.00"), CategoryID: other.ID}
	whole.Txn.Splits = []Split{split}
	fromSplit := NewPart(whole,
		Match{Posting: whole, Split: &split, Amount: split.Amount}, categories)
	require.True(t, fromSplit.HasCategory)
	require.Equal(t, other.ID, fromSplit.CategoryID(), "the split's own category, never its parent's")
	require.Equal(t, fromSplit.CategoryID(), fromSplit.Category.ID)
}

func TestAWholeRowPartTakesTheCategoryThePostingAlreadyCarries(t *testing.T) {
	whole := wlSpend("t-1", "-100.00", NewDate(2026, time.August, 3))
	part := NewPart(whole, Match{Posting: whole, Amount: whole.Amount()}, nil)
	require.True(t, part.HasCategory)
	require.Equal(t, envGroceriesCategory.ID, part.Category.ID)
	require.Equal(t, part.CategoryID(), part.Category.ID)
}

func TestASplitPartWhoseCategoryIsNotLoadedIsTreatedAsUncategorized(t *testing.T) {
	// A category the caller failed to load must not silently exclude spending.
	whole := wlSpend("t-1", "-100.00", NewDate(2026, time.August, 3))
	split := Split{ID: "s-1", Amount: MustFromString("-40.00"), CategoryID: "cat-missing"}
	whole.Txn.Splits = []Split{split}
	part := NewPart(whole,
		Match{Posting: whole, Split: &split, Amount: split.Amount}, map[ID]Category{})
	require.False(t, part.HasCategory)
	require.Equal(t, ID("cat-missing"), part.CategoryID())
}

func TestASplitUnderACategoryExcludedFromReportsIsNotWatched(t *testing.T) {
	excluded := Category{ID: "cat-excluded", Name: "Reimbursed", Kind: CategoryExpense,
		ExcludedFromReports: true}
	categories := map[ID]Category{
		envGroceriesCategory.ID: envGroceriesCategory,
		excluded.ID:             excluded,
	}

	row := wlSpend("t-1", "-100.00", NewDate(2026, time.August, 3), func(p *Posting) {
		p.Txn.CategoryID, p.HasCategory = "", false
		p.Txn.Splits = []Split{
			{ID: "s-1", Amount: MustFromString("-60.00"), CategoryID: envGroceriesCategory.ID},
			{ID: "s-2", Amount: MustFromString("-40.00"), CategoryID: excluded.ID},
		}
	})

	matcher := WatchlistMatcherFor(map[ID]Filter{wlWatchlist().FilterID: {}}, nil, categories)

	spent := WatchlistSpentInMonth(wlWatchlist(), []Posting{row}, matcher,
		NewMonth(2026, time.August), DatePosted)
	require.Equal(t, "60.00", spent.String(),
		"the groceries half only — the excluded half is not watched")
}
