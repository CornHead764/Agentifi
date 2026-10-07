package domain

import (
	"fmt"
	"sort"
	"strconv"
	"time"
)

// The Spending report (calculations.md §11, "Spending report"): one calendar
// period's spending beside a comparison, broken down by category, payee or
// tag. Every figure is Aggregate over the postings whose reporting date falls
// in a window, so the report and the register's Spending tab give one number
// for one window and one scope. Rules that are each a wrong number if missed:
//
//   - The period in progress runs through today, and a comparison against it
//     is cut at the same point into its own period (SameDaysInto), or a
//     third of a month is compared with a whole one.
//   - An average divides by the periods it names, a period with nothing in it
//     counting as zero, and cuts each of them the same way.
//   - A difference is spend against spend: a net credit is negative spend, so
//     a refund-heavy period reads as less spent, never as more.

// PeriodGrain is how long one period of the report is.
type PeriodGrain string

const (
	GrainMonth   PeriodGrain = "month"
	GrainQuarter PeriodGrain = "quarter"
	GrainYear    PeriodGrain = "year"
)

// ParsePeriodGrain reads a grain, false for anything else.
func ParsePeriodGrain(value string) (PeriodGrain, bool) {
	switch grain := PeriodGrain(value); grain {
	case GrainMonth, GrainQuarter, GrainYear:
		return grain, true
	}
	return "", false
}

// months is how many calendar months one period spans.
func (g PeriodGrain) months() int {
	switch g {
	case GrainQuarter:
		return 3
	case GrainYear:
		return 12
	}
	return 1
}

// Period is one calendar month, quarter or year.
type Period struct {
	Grain PeriodGrain
	Start Date
}

// PeriodOf is the period of the grain a day falls in.
func PeriodOf(on Date, grain PeriodGrain) Period {
	month := on.Month
	switch grain {
	case GrainQuarter:
		month = time.Month((int(on.Month)-1)/3*3 + 1)
	case GrainYear:
		month = time.January
	}
	return Period{Grain: grain, Start: NewDate(on.Year, month, 1)}
}

// Shift is the period n periods later, or earlier for a negative n.
func (p Period) Shift(n int) Period {
	return Period{Grain: p.Grain, Start: MonthOf(p.Start).Shift(n * p.Grain.months()).FirstDay()}
}

// End is the period's last day.
func (p Period) End() Date { return MonthOf(p.Start).Shift(p.Grain.months() - 1).LastDay() }

// Contains reports whether a day falls in the period.
func (p Period) Contains(on Date) bool { return !on.Before(p.Start) && !on.After(p.End()) }

// Key names the period the way the report engine's time columns do:
// 2026-10, 2026-Q4, 2026. Keys sort into chronological order.
func (p Period) Key() string {
	switch p.Grain {
	case GrainQuarter:
		return fmt.Sprintf("%04d-Q%d", p.Start.Year, (int(p.Start.Month)-1)/3+1)
	case GrainYear:
		return strconv.Itoa(p.Start.Year)
	}
	return MonthOf(p.Start).String()
}

// SpendingWindow is the reporting dates one period contributes: the whole
// period, or its first part when it is cut short.
type SpendingWindow struct {
	Period  Period
	From    Date
	Through Date
	// Partial is a window that stops before its period's end: the period
	// today is in, or a comparison cut to match it.
	Partial bool
}

// PeriodWindow is the window of a period as of today: through today for the
// period today falls in, whole otherwise.
func PeriodWindow(p Period, today Date) SpendingWindow {
	window := SpendingWindow{Period: p, From: p.Start, Through: p.End()}
	if p.Contains(today) && today.Before(window.Through) {
		window.Through, window.Partial = today, true
	}
	return window
}

// Contains reports whether a reporting date falls in the window.
func (w SpendingWindow) Contains(on Date) bool { return !on.Before(w.From) && !on.After(w.Through) }

// SameDaysInto is another period cut at the point this window reached in its
// own: as many whole months in and the same day of the month, clamped to that
// month's last day. October 1–3 against September is September 1–3; January
// 1 – October 3 against the year before is January 1 – October 3 of it; a
// quarter cut on November 4 cuts the quarter before on August 4. A whole
// window is compared with the whole of the other period.
func (w SpendingWindow) SameDaysInto(other Period) SpendingWindow {
	whole := SpendingWindow{Period: other, From: other.Start, Through: other.End()}
	if !w.Partial {
		return whole
	}
	monthsIn := MonthCount(MonthOf(w.Period.Start), MonthOf(w.Through))
	month := MonthOf(other.Start).Shift(monthsIn)
	cut := ClampToMonth(month.Year, month.Month, w.Through.Day)
	if cut.Before(whole.Through) {
		whole.Through, whole.Partial = cut, true
	}
	return whole
}

// chartPeriods is how many bars the period chart draws per grain.
var chartPeriods = map[PeriodGrain]int{GrainMonth: 12, GrainQuarter: 5, GrainYear: 5}

// ChartPeriods is the period chart's bars, oldest first, ending with the
// period today is in: twelve months, five quarters or five years.
func ChartPeriods(grain PeriodGrain, today Date) []Period {
	current := PeriodOf(today, grain)
	count := chartPeriods[grain]
	out := make([]Period, 0, count)
	for offset := count - 1; offset >= 0; offset-- {
		out = append(out, current.Shift(-offset))
	}
	return out
}

// TablePeriods is the table view's columns: the chart's periods, reaching
// back for years to the first year anything was reported in.
func TablePeriods(grain PeriodGrain, today, earliest Date, hasEarliest bool) []Period {
	periods := ChartPeriods(grain, today)
	if grain != GrainYear || !hasEarliest || !earliest.Before(periods[0].Start) {
		return periods
	}
	first := PeriodOf(earliest, grain)
	out := []Period{}
	for period := first; period.Start.Before(periods[0].Start); period = period.Shift(1) {
		out = append(out, period)
	}
	return append(out, periods...)
}

// FirstReportingDate is the earliest reporting date of a posting that counts
// as income or expense, false when none does.
func FirstReportingDate(postings []Posting, mode DateMode) (Date, bool) {
	var first Date
	found := false
	for _, posting := range postings {
		if !CountsAsIncomeOrExpense(posting, false) {
			continue
		}
		on := ReportingDate(posting.Txn, mode)
		if !found || on.Before(first) {
			first, found = on, true
		}
	}
	return first, found
}

// SpendingComparison is what the selected period is set beside.
type SpendingComparison string

const (
	CompareSameLastYear      SpendingComparison = "same_last_year"
	ComparePrior             SpendingComparison = "prior"
	CompareYearToDateAverage SpendingComparison = "ytd_average"
	CompareAverage2          SpendingComparison = "average_2"
	CompareAverage3          SpendingComparison = "average_3"
	CompareAverage4          SpendingComparison = "average_4"
	CompareAverage6          SpendingComparison = "average_6"
	CompareAverage12         SpendingComparison = "average_12"
	CompareNothing           SpendingComparison = "none"
)

// SpendingComparisonsFor is the Compare menu of a grain, in its order.
func SpendingComparisonsFor(grain PeriodGrain) []SpendingComparison {
	switch grain {
	case GrainQuarter:
		return []SpendingComparison{CompareSameLastYear, ComparePrior, CompareYearToDateAverage,
			CompareAverage2, CompareAverage4, CompareNothing}
	case GrainYear:
		return []SpendingComparison{ComparePrior, CompareAverage3, CompareNothing}
	}
	return []SpendingComparison{CompareSameLastYear, ComparePrior, CompareYearToDateAverage,
		CompareAverage3, CompareAverage6, CompareAverage12, CompareNothing}
}

// IsAverage reports whether the comparison divides over several periods.
func (c SpendingComparison) IsAverage() bool {
	switch c {
	case CompareYearToDateAverage, CompareAverage2, CompareAverage3, CompareAverage4,
		CompareAverage6, CompareAverage12:
		return true
	}
	return false
}

var averageLengths = map[SpendingComparison]int{
	CompareAverage2: 2, CompareAverage3: 3, CompareAverage4: 4, CompareAverage6: 6, CompareAverage12: 12,
}

// ComparisonPeriods is the periods a comparison sets the selected one beside,
// oldest first. A year-to-date average is the periods of the selected one's
// year before it, so it is empty for a January or a first quarter; an
// N-period average is the N periods before the selected one.
func ComparisonPeriods(selected Period, comparison SpendingComparison) []Period {
	switch comparison {
	case CompareSameLastYear:
		return []Period{selected.Shift(-12 / selected.Grain.months())}
	case ComparePrior:
		return []Period{selected.Shift(-1)}
	case CompareYearToDateAverage:
		out := []Period{}
		for period := PeriodOf(PeriodOf(selected.Start, GrainYear).Start, selected.Grain); period.Start.Before(selected.Start); period = period.Shift(1) {
			out = append(out, period)
		}
		return out
	}
	count := averageLengths[comparison]
	out := make([]Period, 0, count)
	for offset := count; offset >= 1; offset-- {
		out = append(out, selected.Shift(-offset))
	}
	return out
}

// ComparisonWindows is ComparisonPeriods, each cut to the point the selected
// window reached (SameDaysInto).
func ComparisonWindows(selected SpendingWindow, comparison SpendingComparison) []SpendingWindow {
	periods := ComparisonPeriods(selected.Period, comparison)
	out := make([]SpendingWindow, 0, len(periods))
	for _, period := range periods {
		out = append(out, selected.SameDaysInto(period))
	}
	return out
}

// SpendingIn is Aggregate over the postings whose reporting date falls in the
// window: the register's Spending or Income tab over the same dates.
func SpendingIn(postings []Posting, window SpendingWindow, opts AggregateOptions) AggregateResult {
	within := make([]Posting, 0, len(postings))
	for _, posting := range postings {
		if window.Contains(ReportingDate(posting.Txn, opts.Mode)) {
			within = append(within, posting)
		}
	}
	return Aggregate(within, opts)
}

// AverageSpending is the comparison of several periods: each line's totals
// summed and divided by how many periods there are, a period with nothing on
// that line counting as zero, rounded once. One period averages to itself;
// none averages to nothing (false).
func AverageSpending(periods []AggregateResult) (AggregateResult, bool) {
	if len(periods) == 0 {
		return AggregateResult{}, false
	}
	tally := newAggregateTally()
	totals := make([]Money, 0, len(periods))
	count := 0
	for _, period := range periods {
		totals = append(totals, period.Total)
		count += period.Count
		for _, bucket := range period.Buckets {
			tally.add(bucket, bucket.Total)
		}
	}
	for key, total := range tally.totals {
		average, _ := total.DivInt(len(periods))
		tally.totals[key] = average.Round()
	}
	total, _ := Sum(totals, func(m Money) Money { return m }).DivInt(len(periods))
	return AggregateResult{Total: total.Round(), Count: count, Buckets: tally.ranked()}, true
}

// DifferenceState is how a line's difference reads.
type DifferenceState string

const (
	// DifferenceChange is spend on both sides, or nothing on either.
	DifferenceChange DifferenceState = "change"
	// DifferenceNewSpend is spend this period with nothing to compare it with.
	DifferenceNewSpend DifferenceState = "new_spend"
	// DifferenceNoSpend is nothing this period against spend in the comparison.
	DifferenceNoSpend DifferenceState = "no_spend"
	// DifferenceNone is no comparison chosen, or none to be had.
	DifferenceNone DifferenceState = "none"
)

// SpendingDifference is one line's change against its comparison.
type SpendingDifference struct {
	// Amount is this period's spend less the comparison's: positive is more
	// spent, negative is less.
	Amount Money
	// Pct is Amount over the comparison's spend, in percent, absent when the
	// comparison is zero.
	Pct    Rate
	HasPct bool
	State  DifferenceState
}

// CompareSpending sets a ledger amount beside its comparison. Both are ledger
// amounts, spending negative, so a net credit is negative spend: $50 of
// refunds against $20 spent is $70 less spent, −350%.
func CompareSpending(amount, comparison Money, hasComparison bool) SpendingDifference {
	if !hasComparison {
		return SpendingDifference{State: DifferenceNone}
	}
	spent, was := amount.Neg().Round(), comparison.Neg().Round()
	out := SpendingDifference{Amount: spent.Sub(was), State: DifferenceChange}
	switch {
	case was.IsZero() && spent.IsZero():
		out.HasPct = true
		return out
	case was.IsZero():
		out.State = DifferenceNewSpend
		return out
	case spent.IsZero():
		out.State = DifferenceNoSpend
	}
	out.Pct, out.HasPct = Percent(out.Amount, was.Abs())
	return out
}

// SpendingRow is one line of the breakdown: a category, a payee or a tag.
type SpendingRow struct {
	Key   string
	Label string
	// Amount and Comparison are ledger amounts: spending negative, a line
	// that nets to a credit positive.
	Amount     Money
	Comparison Money
	Difference SpendingDifference
	// Share is the line's part of the period's spending, over the lines that
	// net to spend; absent for a line that nets to a credit or to nothing.
	Share    Rate
	HasShare bool
}

// SpendingRows joins the period's lines with the comparison's: every line
// either side has, the most spent first. The shares are over the lines, so a
// two-tag row counted under both tags still leaves them summing to 100%.
func SpendingRows(current, comparison AggregateResult, hasComparison bool) []SpendingRow {
	byKey := map[string]*SpendingRow{}
	order := []string{}
	row := func(bucket AggregateBucket) *SpendingRow {
		found, seen := byKey[bucket.Key]
		if !seen {
			found = &SpendingRow{Key: bucket.Key, Label: bucket.Label}
			byKey[bucket.Key] = found
			order = append(order, bucket.Key)
		}
		return found
	}
	gross := Zero
	for _, bucket := range current.Buckets {
		row(bucket).Amount = bucket.Total.Round()
		if bucket.Total.IsNegative() {
			gross = gross.Add(bucket.Total.Neg())
		}
	}
	if hasComparison {
		for _, bucket := range comparison.Buckets {
			row(bucket).Comparison = bucket.Total.Round()
		}
	}

	out := make([]SpendingRow, 0, len(order))
	for _, key := range order {
		one := *byKey[key]
		one.Difference = CompareSpending(one.Amount, one.Comparison, hasComparison)
		if one.Amount.IsNegative() {
			one.Share, one.HasShare = Ratio(one.Amount.Neg(), gross)
		}
		out = append(out, one)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Amount.Equal(out[j].Amount) {
			return out[i].Amount.LessThan(out[j].Amount)
		}
		if !out[i].Comparison.Equal(out[j].Comparison) {
			return out[i].Comparison.LessThan(out[j].Comparison)
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// SavingsRating grades a savings rate against the 15–30% people are told to
// aim for.
type SavingsRating string

const (
	RatingNone  SavingsRating = "none"
	RatingLow   SavingsRating = "low"
	RatingGood  SavingsRating = "good"
	RatingGreat SavingsRating = "great"
)

var (
	ratingGood  = MustFromString("0.15").Decimal()
	ratingGreat = MustFromString("0.30").Decimal()
)

// RateSavings is none at or below zero (or with no rate), low under 15%, good
// from 15% and great from 30%.
func RateSavings(rate Rate, hasRate bool) SavingsRating {
	switch {
	case !hasRate || !rate.IsPositive():
		return RatingNone
	case rate.LessThan(ratingGood):
		return RatingLow
	case rate.LessThan(ratingGreat):
		return RatingGood
	}
	return RatingGreat
}

// SpendingSummary is the four cards over one period.
type SpendingSummary struct {
	Income Money
	// Spent is a ledger amount: negative is spending.
	Spent Money
	// Remaining is Income plus Spent; negative is overspent.
	Remaining Money
	// SavingsRate is Remaining over Income and SpendingRate is spend over
	// Income, both fractions, absent unless something came in.
	SavingsRate  Rate
	SpendingRate Rate
	HasRates     bool
	Rating       SavingsRating
}

// SummarizeSpending is the cards from the period's income and spending
// totals. The rates need income above zero: a period whose only income is a
// clawback has nothing to save from.
func SummarizeSpending(income, spent Money) SpendingSummary {
	income, spent = income.Round(), spent.Round()
	out := SpendingSummary{Income: income, Spent: spent, Remaining: Total(income, spent)}
	if income.IsPositive() {
		out.SavingsRate, _ = Ratio(out.Remaining, income)
		out.SpendingRate, _ = Ratio(spent.Neg(), income)
		out.HasRates = true
	}
	out.Rating = RateSavings(out.SavingsRate, out.HasRates)
	return out
}

// SpendingTableRow is one line of the table view across its periods.
type SpendingTableRow struct {
	Key   string
	Label string
	// Cells runs parallel to the table's periods, ledger amounts.
	Cells []Money
	Total Money
	// Difference is the last period against the one before it, cut to the
	// same point when the last is in progress.
	Difference SpendingDifference
}

// SpendingTable lays the lines out across periods, the most spent in total
// first. `prior` is the period before the last, cut as SameDaysInto cuts it.
func SpendingTable(columns []AggregateResult, prior AggregateResult) []SpendingTableRow {
	byKey := map[string]*SpendingTableRow{}
	order := []string{}
	for index, column := range columns {
		for _, bucket := range column.Buckets {
			row, seen := byKey[bucket.Key]
			if !seen {
				row = &SpendingTableRow{Key: bucket.Key, Label: bucket.Label, Cells: make([]Money, len(columns))}
				byKey[bucket.Key] = row
				order = append(order, bucket.Key)
			}
			row.Cells[index] = bucket.Total.Round()
		}
	}
	priorByKey := map[string]Money{}
	for _, bucket := range prior.Buckets {
		priorByKey[bucket.Key] = bucket.Total
	}

	out := make([]SpendingTableRow, 0, len(order))
	for _, key := range order {
		row := *byKey[key]
		row.Total = Sum(row.Cells, func(m Money) Money { return m })
		if len(row.Cells) > 0 {
			row.Difference = CompareSpending(row.Cells[len(row.Cells)-1], priorByKey[key], true)
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Total.Equal(out[j].Total) {
			return out[i].Total.LessThan(out[j].Total)
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// FlowNode is one band of the flow view, as a magnitude.
type FlowNode struct {
	Key    string
	Label  string
	Amount Money
	// Share is Amount over the period's income, absent with no income.
	Share    Rate
	HasShare bool
}

// SpendingFlow is the flow view: income lines into total income, and total
// income with the spending lines that net to a credit into total spent,
// which fans out to the lines that net to spend.
type SpendingFlow struct {
	Income  []FlowNode
	Credits []FlowNode
	// Spending is the lines that net to spend, each as the amount spent.
	Spending    []FlowNode
	IncomeTotal Money
	// Spent is the period's spending as a magnitude: the spend lines less the
	// credits.
	Spent         Money
	SpentShare    Rate
	HasSpentShare bool
}

// FlowOfSpending draws the flow from the period's income and spending by
// line. Every share is of income, as the flow is income being spent; an
// income line that nets negative draws no band.
func FlowOfSpending(income, spending AggregateResult) SpendingFlow {
	total := income.Total.Round()
	share := func(amount Money) (Rate, bool) {
		if !total.IsPositive() {
			return Rate{}, false
		}
		return Ratio(amount, total)
	}
	node := func(bucket AggregateBucket, amount Money) FlowNode {
		one := FlowNode{Key: bucket.Key, Label: bucket.Label, Amount: amount.Round()}
		one.Share, one.HasShare = share(one.Amount)
		return one
	}

	out := SpendingFlow{
		Income: []FlowNode{}, Credits: []FlowNode{}, Spending: []FlowNode{},
		IncomeTotal: total, Spent: spending.Total.Neg().Round(),
	}
	for _, bucket := range income.Buckets {
		if bucket.Total.IsPositive() {
			out.Income = append(out.Income, node(bucket, bucket.Total))
		}
	}
	for _, bucket := range spending.Buckets {
		switch {
		case bucket.Total.IsPositive():
			out.Credits = append(out.Credits, node(bucket, bucket.Total))
		case bucket.Total.IsNegative():
			out.Spending = append(out.Spending, node(bucket, bucket.Total.Neg()))
		}
	}
	out.SpentShare, out.HasSpentShare = share(out.Spent)
	return out
}

// IncomeFlowUnder is the category the flow's income side is drilled into: the
// one income category every income line rolls up to, so a household whose
// income all files under one parent sees its sources rather than a single
// band. Empty when the income spreads over several, or over none.
func IncomeFlowUnder(topLevel AggregateResult, categories map[ID]Category) ID {
	if len(topLevel.Buckets) != 1 {
		return ""
	}
	if _, known := categories[ID(topLevel.Buckets[0].Key)]; !known {
		return ""
	}
	return ID(topLevel.Buckets[0].Key)
}

// CountUncategorized is how many rows in the window still need a category
// and would count once they had one: the report's "to categorize" note.
func CountUncategorized(postings []Posting, window SpendingWindow, mode DateMode) int {
	count := 0
	for _, posting := range postings {
		if !window.Contains(ReportingDate(posting.Txn, mode)) {
			continue
		}
		if CountsAsIncomeOrExpense(posting, false) && posting.Txn.IsUncategorized() {
			count++
		}
	}
	return count
}
