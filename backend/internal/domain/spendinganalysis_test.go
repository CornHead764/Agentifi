package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The Spending report's rules (calculations.md §11). Expected values are
// worked by hand from the invented rows each test builds.

func spendingDay(month time.Month, day int) Date { return NewDate(2026, month, day) }

// spendingPosting is aggPosting on a chosen reporting date.
func spendingPosting(id ID, amount string, category Category, on Date) Posting {
	posting := aggPosting(id, amount, category)
	posting.Txn.Date = on
	return posting
}

func windowKeys(windows []SpendingWindow) []string {
	out := []string{}
	for _, window := range windows {
		out = append(out, window.From.String()+".."+window.Through.String())
	}
	return out
}

func periodKeys(periods []Period) []string {
	out := []string{}
	for _, period := range periods {
		out = append(out, period.Key())
	}
	return out
}

func TestPeriodsAreCalendarMonthsQuartersAndYears(t *testing.T) {
	on := spendingDay(time.November, 4)

	quarter := PeriodOf(on, GrainQuarter)
	require.Equal(t, "2026-10-01", quarter.Start.String())
	require.Equal(t, "2026-12-31", quarter.End().String())
	require.Equal(t, "2026-Q4", quarter.Key())
	require.Equal(t, "2026-07-01", quarter.Shift(-1).Start.String())
	require.Equal(t, "2025-Q4", quarter.Shift(-4).Key())

	require.Equal(t, "2026", PeriodOf(on, GrainYear).Key())
	require.Equal(t, "2026-12-31", PeriodOf(on, GrainYear).End().String())
	require.Equal(t, "2026-11", PeriodOf(on, GrainMonth).Key())
	require.Equal(t, "2026-02-28", PeriodOf(spendingDay(time.February, 9), GrainMonth).End().String())
}

func TestThePeriodInProgressRunsThroughToday(t *testing.T) {
	today := spendingDay(time.October, 3)

	current := PeriodWindow(PeriodOf(today, GrainMonth), today)
	require.Equal(t, "2026-10-01", current.From.String())
	require.Equal(t, "2026-10-03", current.Through.String())
	require.True(t, current.Partial)

	past := PeriodWindow(PeriodOf(spendingDay(time.September, 12), GrainMonth), today)
	require.Equal(t, "2026-09-30", past.Through.String())
	require.False(t, past.Partial)

	// The last day of a period is the whole period, not a cut one.
	lastDay := spendingDay(time.September, 30)
	require.False(t, PeriodWindow(PeriodOf(lastDay, GrainMonth), lastDay).Partial)
}

func TestAComparisonIsCutAtTheSamePointIntoItsPeriod(t *testing.T) {
	month := PeriodWindow(PeriodOf(spendingDay(time.October, 3), GrainMonth), spendingDay(time.October, 3))
	prior := month.SameDaysInto(month.Period.Shift(-1))
	require.Equal(t, "2026-09-01..2026-09-03", windowKeys([]SpendingWindow{prior})[0])
	require.True(t, prior.Partial)

	// A year to date against last year: the same calendar day.
	year := PeriodWindow(PeriodOf(spendingDay(time.October, 3), GrainYear), spendingDay(time.October, 3))
	require.Equal(t, "2025-01-01..2025-10-03",
		windowKeys([]SpendingWindow{year.SameDaysInto(year.Period.Shift(-1))})[0])

	// A quarter cut on May 31 is one whole month and 31 days in; the quarter
	// before has February there, so it is cut on February's last day.
	quarterToday := spendingDay(time.May, 31)
	quarter := PeriodWindow(PeriodOf(quarterToday, GrainQuarter), quarterToday)
	require.Equal(t, "2026-01-01..2026-02-28",
		windowKeys([]SpendingWindow{quarter.SameDaysInto(quarter.Period.Shift(-1))})[0])

	// A whole period is compared with the whole of the other.
	whole := PeriodWindow(PeriodOf(spendingDay(time.August, 1), GrainMonth), spendingDay(time.October, 3))
	require.Equal(t, "2026-07-01..2026-07-31",
		windowKeys([]SpendingWindow{whole.SameDaysInto(whole.Period.Shift(-1))})[0])
}

func TestTheChartEndsWithThePeriodTodayIsIn(t *testing.T) {
	today := spendingDay(time.October, 3)

	months := ChartPeriods(GrainMonth, today)
	require.Len(t, months, 12)
	require.Equal(t, "2025-11", months[0].Key())
	require.Equal(t, "2026-10", months[11].Key())

	require.Equal(t, []string{"2025-Q4", "2026-Q1", "2026-Q2", "2026-Q3", "2026-Q4"},
		periodKeys(ChartPeriods(GrainQuarter, today)))
	require.Equal(t, []string{"2022", "2023", "2024", "2025", "2026"},
		periodKeys(ChartPeriods(GrainYear, today)))
}

func TestTheTableReachesBackToTheFirstYearWithAnything(t *testing.T) {
	today := spendingDay(time.October, 3)

	require.Equal(t, []string{"2019", "2020", "2021", "2022", "2023", "2024", "2025", "2026"},
		periodKeys(TablePeriods(GrainYear, today, NewDate(2019, time.June, 14), true)))
	// Nothing older than the chart: the chart's five years.
	require.Len(t, TablePeriods(GrainYear, today, NewDate(2024, time.March, 2), true), 5)
	require.Len(t, TablePeriods(GrainYear, today, Date{}, false), 5)
	// Months and quarters keep the chart's columns.
	require.Len(t, TablePeriods(GrainMonth, today, NewDate(2019, time.June, 14), true), 12)
}

func TestFirstReportingDateSkipsWhatDoesNotCount(t *testing.T) {
	transfer := spendingPosting("t0", "-500.00", aggFood, NewDate(2024, time.January, 5))
	transfer.Txn.TransferPairID = "pair-1"
	first := spendingPosting("t1", "-10.00", aggFood, NewDate(2024, time.March, 9))
	later := spendingPosting("t2", "-10.00", aggFood, NewDate(2025, time.May, 1))

	on, found := FirstReportingDate([]Posting{later, transfer, first}, DateEffective)
	require.True(t, found)
	require.Equal(t, "2024-03-09", on.String())

	_, found = FirstReportingDate([]Posting{transfer}, DateEffective)
	require.False(t, found)
}

func TestEachGrainOffersItsOwnCompareMenu(t *testing.T) {
	require.Equal(t, []SpendingComparison{
		CompareSameLastYear, ComparePrior, CompareYearToDateAverage,
		CompareAverage3, CompareAverage6, CompareAverage12, CompareNothing,
	}, SpendingComparisonsFor(GrainMonth))
	require.Equal(t, []SpendingComparison{
		CompareSameLastYear, ComparePrior, CompareYearToDateAverage,
		CompareAverage2, CompareAverage4, CompareNothing,
	}, SpendingComparisonsFor(GrainQuarter))
	require.Equal(t, []SpendingComparison{ComparePrior, CompareAverage3, CompareNothing},
		SpendingComparisonsFor(GrainYear))
}

func TestComparisonPeriodsAreThePeriodsBeforeTheSelectedOne(t *testing.T) {
	october := PeriodOf(spendingDay(time.October, 3), GrainMonth)
	require.Equal(t, []string{"2025-10"}, periodKeys(ComparisonPeriods(october, CompareSameLastYear)))
	require.Equal(t, []string{"2026-09"}, periodKeys(ComparisonPeriods(october, ComparePrior)))
	require.Equal(t, []string{"2026-07", "2026-08", "2026-09"},
		periodKeys(ComparisonPeriods(october, CompareAverage3)))
	require.Len(t, ComparisonPeriods(october, CompareAverage12), 12)
	require.Equal(t, "2025-10", ComparisonPeriods(october, CompareAverage12)[0].Key())
	require.Equal(t,
		[]string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06", "2026-07", "2026-08", "2026-09"},
		periodKeys(ComparisonPeriods(october, CompareYearToDateAverage)))
	require.Empty(t, ComparisonPeriods(october, CompareNothing))

	// A January has no earlier month this year to average.
	require.Empty(t, ComparisonPeriods(PeriodOf(spendingDay(time.January, 20), GrainMonth), CompareYearToDateAverage))

	q4 := PeriodOf(spendingDay(time.October, 3), GrainQuarter)
	require.Equal(t, []string{"2025-Q4"}, periodKeys(ComparisonPeriods(q4, CompareSameLastYear)))
	require.Equal(t, []string{"2026-Q1", "2026-Q2", "2026-Q3"},
		periodKeys(ComparisonPeriods(q4, CompareYearToDateAverage)))
	require.Equal(t, []string{"2026-Q2", "2026-Q3"}, periodKeys(ComparisonPeriods(q4, CompareAverage2)))

	year := PeriodOf(spendingDay(time.October, 3), GrainYear)
	require.Equal(t, []string{"2023", "2024", "2025"}, periodKeys(ComparisonPeriods(year, CompareAverage3)))
}

func TestAnAverageAgainstAPartialPeriodCutsEveryPeriod(t *testing.T) {
	today := spendingDay(time.October, 3)
	selected := PeriodWindow(PeriodOf(today, GrainMonth), today)
	require.Equal(t,
		[]string{"2026-07-01..2026-07-03", "2026-08-01..2026-08-03", "2026-09-01..2026-09-03"},
		windowKeys(ComparisonWindows(selected, CompareAverage3)))
}

func TestSpendingInReadsTheEffectiveDate(t *testing.T) {
	// A card charge made September 28 whose statement is due October 25 is
	// October's spending (trap 4).
	card := spendingPosting("t1", "-80.00", aggFood, spendingDay(time.September, 28))
	card.Txn.EffectiveDate = spendingDay(time.October, 25)
	cash := spendingPosting("t2", "-20.00", aggFood, spendingDay(time.September, 29))

	postings := []Posting{card, cash}
	opts := aggSpending(AggregateByCategory)
	september := PeriodWindow(PeriodOf(spendingDay(time.September, 1), GrainMonth), spendingDay(time.December, 1))
	october := PeriodWindow(PeriodOf(spendingDay(time.October, 1), GrainMonth), spendingDay(time.December, 1))
	require.Equal(t, "-20.00", SpendingIn(postings, september, opts).Total.String())
	require.Equal(t, "-80.00", SpendingIn(postings, october, opts).Total.String())
}

func TestAnAverageCountsAnEmptyPeriodAsZero(t *testing.T) {
	// Food: $30 in July, $60 in August, nothing in September: $30 a month.
	// Household: $10 in August alone: $3.33 a month, rounded once.
	postings := []Posting{
		spendingPosting("t1", "-30.00", aggGroceries, spendingDay(time.July, 4)),
		spendingPosting("t2", "-60.00", aggFood, spendingDay(time.August, 9)),
		spendingPosting("t3", "-10.00", aggHousehold, spendingDay(time.August, 21)),
	}
	opts := aggSpending(AggregateByCategory)
	selected := PeriodWindow(PeriodOf(spendingDay(time.October, 3), GrainMonth), spendingDay(time.October, 31))
	periods := []AggregateResult{}
	for _, window := range ComparisonWindows(selected, CompareAverage3) {
		periods = append(periods, SpendingIn(postings, window, opts))
	}

	average, ok := AverageSpending(periods)
	require.True(t, ok)
	require.Equal(t, "-33.33", average.Total.String())
	require.Equal(t, map[string]string{"Food & Dining": "-30.00", "Household": "-3.33"},
		bucketTotals(average.Buckets))

	_, ok = AverageSpending(nil)
	require.False(t, ok)
}

func TestCompareSpendingReadsSpendAgainstSpend(t *testing.T) {
	money := MustFromString

	more := CompareSpending(money("-120.00"), money("-100.00"), true)
	require.Equal(t, DifferenceChange, more.State)
	require.Equal(t, "20.00", more.Amount.String())
	require.True(t, more.HasPct)
	require.Equal(t, "20", more.Pct.String())

	less := CompareSpending(money("-25.00"), money("-100.00"), true)
	require.Equal(t, "-75.00", less.Amount.String())
	require.Equal(t, "-75", less.Pct.String())

	// $50 back against $20 spent is $70 less spent: −350%.
	credit := CompareSpending(money("50.00"), money("-20.00"), true)
	require.Equal(t, "-70.00", credit.Amount.String())
	require.Equal(t, "-350", credit.Pct.String())

	fresh := CompareSpending(money("-40.00"), Zero, true)
	require.Equal(t, DifferenceNewSpend, fresh.State)
	require.Equal(t, "40.00", fresh.Amount.String())
	require.False(t, fresh.HasPct)

	none := CompareSpending(Zero, money("-50.00"), true)
	require.Equal(t, DifferenceNoSpend, none.State)
	require.Equal(t, "-50.00", none.Amount.String())
	require.Equal(t, "-100", none.Pct.String())

	same := CompareSpending(money("-150.00"), money("-150.00"), true)
	require.Equal(t, DifferenceChange, same.State)
	require.Equal(t, "0", same.Pct.String())

	require.Equal(t, DifferenceNone, CompareSpending(money("-10.00"), Zero, false).State)
}

func TestSpendingRowsJoinThePeriodWithItsComparison(t *testing.T) {
	money := MustFromString
	current := AggregateResult{Buckets: []AggregateBucket{
		{Key: "food", Label: "Food & Dining", Total: money("-75.00")},
		{Key: "shopping", Label: "Shopping", Total: money("10.00")},
		{Key: "household", Label: "Household", Total: money("-25.00")},
	}}
	comparison := AggregateResult{Buckets: []AggregateBucket{
		{Key: "food", Label: "Food & Dining", Total: money("-50.00")},
		{Key: "travel", Label: "Travel", Total: money("-40.00")},
	}}

	rows := SpendingRows(current, comparison, true)
	labels := []string{}
	for _, row := range rows {
		labels = append(labels, row.Label)
	}
	require.Equal(t, []string{"Food & Dining", "Household", "Travel", "Shopping"}, labels)

	food := rows[0]
	require.Equal(t, "-50.00", food.Comparison.String())
	require.Equal(t, "25.00", food.Difference.Amount.String())
	require.Equal(t, "50", food.Difference.Pct.String())
	// Shares are of the $100 the spending lines add up to; the credit has none.
	require.Equal(t, "0.75", food.Share.String())
	require.Equal(t, "0.25", rows[1].Share.String())
	require.Equal(t, DifferenceNewSpend, rows[1].Difference.State)
	require.Equal(t, DifferenceNoSpend, rows[2].Difference.State)
	require.False(t, rows[2].HasShare)
	require.False(t, rows[3].HasShare)

	// No comparison: the comparison side stays empty.
	alone := SpendingRows(current, comparison, false)
	require.Len(t, alone, 3)
	require.Equal(t, DifferenceNone, alone[0].Difference.State)
}

func TestSummarizeSpendingRatesWhatWasLeft(t *testing.T) {
	money := MustFromString

	saved := SummarizeSpending(money("4000.00"), money("-3400.00"))
	require.Equal(t, "600.00", saved.Remaining.String())
	require.True(t, saved.HasRates)
	require.Equal(t, "0.15", saved.SavingsRate.String())
	require.Equal(t, "0.85", saved.SpendingRate.String())
	require.Equal(t, RatingGood, saved.Rating)

	over := SummarizeSpending(money("1000.00"), money("-1250.00"))
	require.Equal(t, "-250.00", over.Remaining.String())
	require.Equal(t, "-0.25", over.SavingsRate.String())
	require.Equal(t, "1.25", over.SpendingRate.String())
	require.Equal(t, RatingNone, over.Rating)

	nothingIn := SummarizeSpending(Zero, money("-80.00"))
	require.False(t, nothingIn.HasRates)
	require.Equal(t, "-80.00", nothingIn.Remaining.String())
	require.Equal(t, RatingNone, nothingIn.Rating)

	require.Equal(t, RatingLow, SummarizeSpending(money("1000.00"), money("-900.00")).Rating)
	require.Equal(t, RatingGreat, SummarizeSpending(money("1000.00"), money("-700.00")).Rating)
}

func TestSpendingTableSetsTheLastPeriodAgainstTheOneBefore(t *testing.T) {
	money := MustFromString
	columns := []AggregateResult{
		{Buckets: []AggregateBucket{
			{Key: "food", Label: "Food & Dining", Total: money("-40.00")},
			{Key: "travel", Label: "Travel", Total: money("-300.00")},
		}},
		{Buckets: []AggregateBucket{
			{Key: "food", Label: "Food & Dining", Total: money("-60.00")},
		}},
	}
	// The period before the last, cut to the same days: $20 of food.
	prior := AggregateResult{Buckets: []AggregateBucket{
		{Key: "food", Label: "Food & Dining", Total: money("-20.00")},
	}}

	rows := SpendingTable(columns, prior)
	require.Len(t, rows, 2)
	require.Equal(t, "Travel", rows[0].Label)
	require.Equal(t, []Money{money("-300.00"), Zero}, rows[0].Cells)
	require.Equal(t, "-300.00", rows[0].Total.String())
	require.Equal(t, DifferenceChange, rows[0].Difference.State)
	require.Equal(t, "0.00", rows[0].Difference.Amount.String())

	food := rows[1]
	require.Equal(t, "-100.00", food.Total.String())
	require.Equal(t, "40.00", food.Difference.Amount.String())
	require.Equal(t, "200", food.Difference.Pct.String())
}

func TestFlowSharesAreOfIncome(t *testing.T) {
	money := MustFromString
	income := AggregateResult{Total: money("2000.00"), Buckets: []AggregateBucket{
		{Key: "salary", Label: "Salary", Total: money("1500.00")},
		{Key: "bonus", Label: "Bonus", Total: money("500.00")},
	}}
	spending := AggregateResult{Total: money("-1700.00"), Buckets: []AggregateBucket{
		{Key: "home", Label: "Home", Total: money("-1000.00")},
		{Key: "food", Label: "Food & Dining", Total: money("-800.00")},
		{Key: "shopping", Label: "Shopping", Total: money("100.00")},
	}}

	flow := FlowOfSpending(income, spending)
	require.Equal(t, "2000.00", flow.IncomeTotal.String())
	require.Equal(t, "1700.00", flow.Spent.String())
	require.Equal(t, "0.85", flow.SpentShare.String())
	require.Len(t, flow.Income, 2)
	require.Equal(t, "0.75", flow.Income[0].Share.String())
	require.Equal(t, "0.25", flow.Income[1].Share.String())
	require.Len(t, flow.Credits, 1)
	require.Equal(t, "Shopping", flow.Credits[0].Label)
	require.Equal(t, "100.00", flow.Credits[0].Amount.String())
	require.Equal(t, "0.05", flow.Credits[0].Share.String())
	require.Len(t, flow.Spending, 2)
	require.Equal(t, "1000.00", flow.Spending[0].Amount.String())
	require.Equal(t, "0.5", flow.Spending[0].Share.String())
	require.Equal(t, "0.4", flow.Spending[1].Share.String())

	nothingIn := FlowOfSpending(AggregateResult{}, spending)
	require.False(t, nothingIn.HasSpentShare)
	require.False(t, nothingIn.Spending[0].HasShare)
}

func TestIncomeFlowDrillsIntoASingleIncomeParent(t *testing.T) {
	categories := aggCategories()
	one := AggregateResult{Buckets: []AggregateBucket{{Key: string(aggSalary.ID), Label: "Salary"}}}
	require.Equal(t, aggSalary.ID, IncomeFlowUnder(one, categories))

	two := AggregateResult{Buckets: []AggregateBucket{
		{Key: string(aggSalary.ID), Label: "Salary"},
		{Key: string(aggFood.ID), Label: "Food & Dining"},
	}}
	require.Equal(t, ID(""), IncomeFlowUnder(two, categories))
	require.Equal(t, ID(""), IncomeFlowUnder(AggregateResult{}, categories))
}

func TestCountUncategorizedCountsRowsThatWouldCount(t *testing.T) {
	today := spendingDay(time.October, 3)
	window := PeriodWindow(PeriodOf(today, GrainMonth), today)

	waiting := spendingPosting("t1", "-12.00", Category{}, spendingDay(time.October, 2))
	paired := spendingPosting("t2", "-500.00", Category{}, spendingDay(time.October, 2))
	paired.Txn.TransferPairID = "pair-1"
	hidden := spendingPosting("t3", "-9.00", Category{}, spendingDay(time.October, 1))
	hidden.Txn.ExcludedFromReports = true
	earlier := spendingPosting("t4", "-7.00", Category{}, spendingDay(time.September, 30))
	filed := spendingPosting("t5", "-7.00", aggFood, spendingDay(time.October, 1))
	split := spendingPosting("t6", "-30.00", Category{}, spendingDay(time.October, 3))
	split.Txn.Splits = []Split{
		{ID: "s1", Amount: MustFromString("-10.00"), CategoryID: aggFood.ID},
		{ID: "s2", Amount: MustFromString("-20.00")},
	}

	require.Equal(t, 2, CountUncategorized(
		[]Posting{waiting, paired, hidden, earlier, filed, split}, window, DateEffective))
}
