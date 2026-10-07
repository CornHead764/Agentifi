package domain

import (
	"fmt"
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

// Recurrence rules: an RFC 5545 RRULE subset plus the dropdown alias. The rule
// fields are the truth; the alias is stored too so "Twice a month" reads back
// as itself. Occurrences are expanded, never looked up in a frequency table:
// every-14-days falls 26 times in most years and 27 in some.

// Frequency is the RRULE FREQ values Simplifi's reminders actually use.
type Frequency string

const (
	// FreqNone is the one-time case, which never repeats.
	FreqNone    Frequency = ""
	FreqDaily   Frequency = "DAILY"
	FreqWeekly  Frequency = "WEEKLY"
	FreqMonthly Frequency = "MONTHLY"
	FreqYearly  Frequency = "YEARLY"
)

// Weekday is an RRULE BYDAY code, stored so the JSON never depends on whether
// 0 is Monday or Sunday.
type Weekday string

const (
	WeekdayMO Weekday = "MO"
	WeekdayTU Weekday = "TU"
	WeekdayWE Weekday = "WE"
	WeekdayTH Weekday = "TH"
	WeekdayFR Weekday = "FR"
	WeekdaySA Weekday = "SA"
	WeekdaySU Weekday = "SU"
)

// Index is the weekday number with Monday at zero, which is the numbering the
// expansion below anchors its weeks on.
func (w Weekday) Index() int {
	switch w {
	case WeekdayTU:
		return 1
	case WeekdayWE:
		return 2
	case WeekdayTH:
		return 3
	case WeekdayFR:
		return 4
	case WeekdaySA:
		return 5
	case WeekdaySU:
		return 6
	default:
		return 0
	}
}

// RecurrenceAlias is one of the eight labels the frequency dropdown offers.
type RecurrenceAlias string

const (
	AliasEveryWeek     RecurrenceAlias = "EVERY_WEEK"
	AliasEveryMonth    RecurrenceAlias = "EVERY_MONTH"
	AliasTwiceAMonth   RecurrenceAlias = "TWICE_A_MONTH"
	AliasEveryQuarter  RecurrenceAlias = "EVERY_QUARTER"
	AliasEveryYear     RecurrenceAlias = "EVERY_YEAR"
	AliasEveryXDays    RecurrenceAlias = "EVERY_X_DAYS"
	AliasMultipleFixed RecurrenceAlias = "MULTIPLE_FIXED"
	AliasOneTime       RecurrenceAlias = "ONE_TIME"
)

// oneTimeHorizonDays is how far ahead to look for the "next" occurrence of a
// rule that has none.
const oneTimeHorizonDays = 3650

// Recurrence is one RRULE. FreqNone is the one-time case, which never repeats.
type Recurrence struct {
	Alias     RecurrenceAlias
	Frequency Frequency
	Interval  int
	// ByMonthDay is days of the month; negative counts back from the end, so
	// -1 is the last day.
	ByMonthDay []int
	ByDay      []Weekday
	// ByMonth is the months of the year the rule is active in, RRULE BYMONTH
	// as it limits a DAILY, WEEKLY or MONTHLY rule: lawn care from April to
	// October. Empty is every month. A yearly or one-time rule carries none.
	ByMonth []time.Month
}

// NewRecurrence refuses a rule whose fields contradict each other. Stores must
// go through here. The active months are kept sorted and distinct, and all
// twelve are stored as none.
func NewRecurrence(
	alias RecurrenceAlias, frequency Frequency, interval int,
	byMonthDay []int, byDay []Weekday, byMonth []time.Month,
) (Recurrence, error) {
	r := Recurrence{
		Alias:      alias,
		Frequency:  frequency,
		Interval:   interval,
		ByMonthDay: byMonthDay,
		ByDay:      byDay,
		ByMonth:    ActiveMonths(byMonth),
	}
	if err := r.Validate(); err != nil {
		return Recurrence{}, err
	}
	return r, nil
}

// ActiveMonths is a month list sorted and without repeats, or nil when it
// names all twelve: a rule active every month is not seasonal.
func ActiveMonths(months []time.Month) []time.Month {
	out := make([]time.Month, 0, len(months))
	for _, month := range months {
		if !slices.Contains(out, month) {
			out = append(out, month)
		}
	}
	slices.Sort(out)
	if len(out) == 0 || len(out) == 12 {
		return nil
	}
	return out
}

func (r Recurrence) Validate() error {
	if r.Interval < 1 {
		return fmt.Errorf("recurrence: interval must be at least 1, got %d", r.Interval)
	}
	if (r.Frequency == FreqNone) != (r.Alias == AliasOneTime) {
		return fmt.Errorf("recurrence: %s and frequency=%q disagree", r.Alias, r.Frequency)
	}
	for _, day := range r.ByMonthDay {
		if day == 0 || day > 31 || day < -31 {
			return fmt.Errorf("recurrence: by month day out of range: %d", day)
		}
	}
	for _, month := range r.ByMonth {
		if month < time.January || month > time.December {
			return fmt.Errorf("recurrence: by month out of range: %d", month)
		}
	}
	if len(r.ByMonth) > 0 && !r.TakesActiveMonths() {
		return fmt.Errorf("recurrence: %s has no active months; its anchor names its month", r.Alias)
	}
	return nil
}

// TakesActiveMonths reports whether the rule can be limited to some months of
// the year. A yearly rule falls in its anchor's month and a one-time rule on
// its date, so neither has a season to name.
func (r Recurrence) TakesActiveMonths() bool {
	switch r.Frequency {
	case FreqDaily, FreqWeekly, FreqMonthly:
		return true
	}
	return false
}

// ActiveIn reports whether the rule may land in this month of the year.
func (r Recurrence) ActiveIn(month time.Month) bool {
	return len(r.ByMonth) == 0 || slices.Contains(r.ByMonth, month)
}

func EveryWeek(weekdays ...Weekday) Recurrence {
	return Recurrence{Alias: AliasEveryWeek, Frequency: FreqWeekly, Interval: 1, ByDay: weekdays}
}

func EveryMonth(day int) Recurrence {
	return Recurrence{Alias: AliasEveryMonth, Frequency: FreqMonthly, Interval: 1, ByMonthDay: []int{day}}
}

func TwiceAMonth(first, second int) Recurrence {
	return Recurrence{Alias: AliasTwiceAMonth, Frequency: FreqMonthly, Interval: 1, ByMonthDay: []int{first, second}}
}

// EveryQuarter takes the day of the month as an optional argument; with none,
// the series' anchor date supplies it.
func EveryQuarter(day ...int) Recurrence {
	return Recurrence{Alias: AliasEveryQuarter, Frequency: FreqMonthly, Interval: 3, ByMonthDay: day}
}

func EveryYear() Recurrence {
	return Recurrence{Alias: AliasEveryYear, Frequency: FreqYearly, Interval: 1}
}

func EveryXDays(days int) Recurrence {
	return Recurrence{Alias: AliasEveryXDays, Frequency: FreqDaily, Interval: days}
}

func MultipleFixed(days ...int) Recurrence {
	return Recurrence{Alias: AliasMultipleFixed, Frequency: FreqMonthly, Interval: 1, ByMonthDay: days}
}

func OneTime() Recurrence {
	return Recurrence{Alias: AliasOneTime, Interval: 1}
}

// AliasForRule names the dropdown entry a bare rule reads back as. Simplifi's
// export carries rule fields and no alias; defaulting to one-time would label
// monthly bills "One-time payment" while the projection pays them monthly.
func AliasForRule(frequency Frequency, interval int, byMonthDay []int) RecurrenceAlias {
	if interval < 1 {
		interval = 1
	}
	switch {
	case frequency == FreqNone:
		return AliasOneTime
	case frequency == FreqWeekly && interval == 1:
		return AliasEveryWeek
	case frequency == FreqMonthly && interval == 1 && len(byMonthDay) == 2:
		return AliasTwiceAMonth
	case frequency == FreqMonthly && interval == 1 && len(byMonthDay) > 2:
		return AliasMultipleFixed
	case frequency == FreqMonthly && interval == 1:
		return AliasEveryMonth
	case frequency == FreqMonthly && interval == 3:
		return AliasEveryQuarter
	case frequency == FreqYearly && interval == 1:
		return AliasEveryYear
	default:
		return AliasEveryXDays
	}
}

func (r Recurrence) Repeats() bool { return r.Frequency != FreqNone }

// step is the interval floored at one: an unvalidated zero interval would
// otherwise loop for ever.
func (r Recurrence) step() int {
	if r.Interval < 1 {
		return 1
	}
	return r.Interval
}

func dateOrder(a, b Date) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	}
	return 0
}

// daysOfMonth resolves the requested days of a month, clamped to its length:
// day 31 in February lands on the 28th/29th, never skipped and never rolled
// into March.
func daysOfMonth(days []int, month Month) []Date {
	seen := make(map[Date]bool, len(days))
	out := make([]Date, 0, len(days))
	for _, day := range days {
		wanted := day
		if day < 0 {
			wanted = month.Days() + 1 + day
		}
		if wanted < 1 {
			wanted = 1
		}
		on := month.Day(wanted)
		if !seen[on] {
			seen[on] = true
			out = append(out, on)
		}
	}
	slices.SortFunc(out, dateOrder)
	return out
}

// ExpandOccurrences lists every occurrence inside the window, ascending.
// startOn supplies the interval phase and any day, weekday or month the rule
// leaves empty; nothing occurs before it. A zero endOn means no end.
func ExpandOccurrences(recurrence Recurrence, startOn, windowStart, windowEnd Date, endOn Date) []Date {
	last := windowEnd
	if !endOn.IsZero() && endOn.Before(last) {
		last = endOn
	}
	lower := windowStart
	if startOn.After(lower) {
		lower = startOn
	}
	if last.Before(lower) {
		return nil
	}

	var out []Date
	switch recurrence.Frequency {
	case FreqNone:
		if lower.NotAfter(startOn) && startOn.NotAfter(last) {
			return []Date{startOn}
		}
		return nil
	case FreqDaily:
		out = expandDaily(recurrence, startOn, lower, last)
	case FreqWeekly:
		out = expandWeekly(recurrence, startOn, lower, last)
	case FreqMonthly:
		out = expandMonthly(recurrence, startOn, lower, last)
	default:
		return expandYearly(recurrence, startOn, lower, last)
	}
	// Active months filter and never shift: an off month's occurrence is not
	// shifted into the season, and the interval keeps its phase across the gap.
	if len(recurrence.ByMonth) > 0 {
		out = slices.DeleteFunc(out, func(on Date) bool { return !recurrence.ActiveIn(on.Month) })
	}
	return out
}

func expandDaily(recurrence Recurrence, startOn, lower, last Date) []Date {
	step := recurrence.step()
	elapsed := DaysBetween(startOn, lower)
	skipped := 0
	if elapsed > 0 {
		skipped = (elapsed + step - 1) / step
	}
	var out []Date
	for current := startOn.AddDays(skipped * step); current.NotAfter(last); current = current.AddDays(step) {
		out = append(out, current)
	}
	return out
}

func expandWeekly(recurrence Recurrence, startOn, lower, last Date) []Date {
	indexes := make([]int, 0, len(recurrence.ByDay))
	for _, day := range recurrence.ByDay {
		if !slices.Contains(indexes, day.Index()) {
			indexes = append(indexes, day.Index())
		}
	}
	if len(indexes) == 0 {
		indexes = append(indexes, mondayIndex(startOn))
	}
	slices.Sort(indexes)

	// Phase is taken from the anchor's own week, so an every-two-weeks series
	// keeps landing on the weeks the user picked rather than on ISO even weeks.
	weekStart := startOn.AddDays(-mondayIndex(startOn))
	step := 7 * recurrence.step()
	skipped := 0
	if elapsed := DaysBetween(weekStart, lower); elapsed > 0 {
		skipped = elapsed / step
	}
	var out []Date
	for current := weekStart.AddDays(skipped * step); current.NotAfter(last); current = current.AddDays(step) {
		for _, index := range indexes {
			on := current.AddDays(index)
			if on.NotBefore(lower) && on.NotAfter(last) && on.NotBefore(startOn) {
				out = append(out, on)
			}
		}
	}
	return out
}

// mondayIndex is the weekday of a date with Monday at zero.
func mondayIndex(d Date) int { return (int(d.Time().Weekday()) + 6) % 7 }

func expandMonthly(recurrence Recurrence, startOn, lower, last Date) []Date {
	days := recurrence.ByMonthDay
	if len(days) == 0 {
		days = []int{startOn.Day}
	}
	anchor := MonthOf(startOn)
	step := recurrence.step()
	skipped := 0
	if elapsed := MonthCount(anchor, MonthOf(lower)); elapsed > 0 {
		skipped = elapsed / step
	}
	var out []Date
	for month := anchor.Shift(skipped * step); month.FirstDay().NotAfter(last); month = month.Shift(step) {
		for _, on := range daysOfMonth(days, month) {
			if on.NotBefore(lower) && on.NotAfter(last) {
				out = append(out, on)
			}
		}
	}
	return out
}

func expandYearly(recurrence Recurrence, startOn, lower, last Date) []Date {
	// No BYMONTH: a yearly reminder is anchored on its due date, so a rule
	// imported with several months expands to its anchor's month only
	// (calculations.md §13).
	days := recurrence.ByMonthDay
	if len(days) == 0 {
		days = []int{startOn.Day}
	}
	step := recurrence.step()
	skipped := 0
	if elapsed := lower.Year - startOn.Year; elapsed > 0 {
		skipped = elapsed / step
	}
	var out []Date
	for year := startOn.Year + skipped*step; year <= last.Year; year += step {
		for _, on := range daysOfMonth(days, NewMonth(year, startOn.Month)) {
			if on.NotBefore(lower) && on.NotAfter(last) {
				out = append(out, on)
			}
		}
	}
	return out
}

// PeriodDays is the average days between occurrences — an estimate for sizing
// horizons, never for what is shown. It is the period inside the active
// months: a seasonal monthly bill is a month apart when it falls, and its
// match window is a monthly one.
func PeriodDays(recurrence Recurrence) float64 {
	interval := float64(recurrence.step())
	perMonth := float64(max(1, len(recurrence.ByMonthDay)))
	switch recurrence.Frequency {
	case FreqNone:
		return float64(oneTimeHorizonDays)
	case FreqDaily:
		return interval
	case FreqWeekly:
		return 7.0 * interval / float64(max(1, len(recurrence.ByDay)))
	case FreqMonthly:
		return 30.44 * interval / perMonth
	default:
		return 365.25 * interval / perMonth
	}
}

// NextOccurrenceAfter is the first occurrence strictly later than the given
// date. It reports false past the end of the series.
func NextOccurrenceAfter(recurrence Recurrence, startOn, after Date, endOn Date) (Date, bool) {
	horizon := after.AddDays(int(PeriodDays(recurrence)*2) + 8)
	if len(recurrence.ByMonth) > 0 {
		// The off season lies between, up to eleven months of it.
		horizon = horizon.AddDays(366)
	}
	found := ExpandOccurrences(recurrence, startOn, after.AddDays(1), horizon, endOn)
	if len(found) == 0 {
		return Date{}, false
	}
	return found[0], true
}

// ActiveStart is where a series on this rule begins: its start date when that
// falls in an active month, else the first occurrence after it, so a lawn
// service set up in November is first due in April rather than waiting on a
// slot no expansion lists. False when the rule never lands in an active month,
// as a quarterly rule whose phase misses every month named.
func ActiveStart(recurrence Recurrence, startOn, endOn Date) (Date, bool) {
	if recurrence.ActiveIn(startOn.Month) {
		return startOn, true
	}
	reach := startOn.AddDays(366 + int(PeriodDays(recurrence)*13))
	found := ExpandOccurrences(recurrence, startOn, startOn, reach, endOn)
	if len(found) == 0 {
		return Date{}, false
	}
	return found[0], true
}

// OccurrencesPerYear counts the rule's occurrences in a real calendar year,
// expanded so a 27-paycheck year counts 27.
func OccurrencesPerYear(recurrence Recurrence, startOn Date, year int, endOn Date) int {
	return len(ExpandOccurrences(recurrence, startOn, NewDate(year, time.January, 1), NewDate(year, time.December, 31), endOn))
}

// AnnualizedAmount is the amount times the occurrences the rule actually has in
// the year.
func AnnualizedAmount(amount Money, recurrence Recurrence, startOn Date, year int, endOn Date) Money {
	count := OccurrencesPerYear(recurrence, startOn, year, endOn)
	return amount.Scale(decimal.NewFromInt(int64(count)))
}

// OccurrencesByMonth counts occurrences per month of the year, months with none
// included.
func OccurrencesByMonth(recurrence Recurrence, startOn Date, year int, endOn Date) map[Month]int {
	counts := make(map[Month]int, 12)
	for month := time.January; month <= time.December; month++ {
		counts[NewMonth(year, month)] = 0
	}
	for _, on := range ExpandOccurrences(recurrence, startOn, NewDate(year, time.January, 1), NewDate(year, time.December, 31), endOn) {
		counts[MonthOf(on)]++
	}
	return counts
}

// ExtraOccurrenceMonths lists the months of the year holding more occurrences
// than the usual month.
//
// This is the "extra paycheck month" notification. "Usual" is the year's most
// common non-empty count, so a series that started mid-year does not report its
// first full month as extra.
func ExtraOccurrenceMonths(recurrence Recurrence, startOn Date, year int, endOn Date) []Month {
	counts := OccurrencesByMonth(recurrence, startOn, year, endOn)
	frequency := map[int]int{}
	for _, count := range counts {
		if count > 0 {
			frequency[count]++
		}
	}
	if len(frequency) == 0 {
		return nil
	}
	// Ties go to the smaller count, so a series whose occurrences are split
	// evenly between two-a-month and three-a-month still reports the threes.
	usual := 0
	for count, seen := range frequency {
		if usual == 0 || seen > frequency[usual] || (seen == frequency[usual] && count < usual) {
			usual = count
		}
	}
	var out []Month
	for month, count := range counts {
		if count > usual {
			out = append(out, month)
		}
	}
	slices.SortFunc(out, func(a, b Month) int { return a.Compare(b) })
	return out
}

// Series is a scheduled transaction. Description is the matching input;
// DisplayName is what is shown, so renaming never breaks matching.
type Series struct {
	ID        ID
	AccountID ID
	Kind      SeriesKind
	// Description is the matching input. Never rendered.
	Description string
	Amount      Money
	Recurrence  Recurrence
	// StartOn is the rule's anchor, and the first occurrence.
	StartOn Date
	// NextDueOn is the schedule pointer: the next occurrence nothing has
	// fulfilled yet. Zero means it has not moved off StartOn.
	NextDueOn Date
	// DisplayName is the display name. Never matched against.
	DisplayName string
	Currency    string
	CategoryID  ID
	EndOn       Date

	// OverrideNextDueOn and OverrideNextAmount let one occurrence differ
	// without forking the series.
	OverrideNextDueOn     Date
	OverrideNextAmount    Money
	HasOverrideNextAmount bool

	// AutoAdjustDueOn shows an occurrence on its open linked bill's due date.
	// A linked bill's amount needs no switch: it is the provider's own figure.
	AutoAdjustDueOn bool

	IsActive  bool
	IsDeleted bool
}

// Label is what every display path reads. No matching path may read this.
func (s Series) Label() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Description
}

// ScheduledDueOn is where the pointer stands, before any one-off date override.
func (s Series) ScheduledDueOn() Date {
	if !s.NextDueOn.IsZero() {
		return s.NextDueOn
	}
	return s.StartOn
}

// DueOn is the next occurrence's real date, override included.
func (s Series) DueOn() Date {
	if !s.OverrideNextDueOn.IsZero() {
		return s.OverrideNextDueOn
	}
	return s.ScheduledDueOn()
}

func (s Series) IsLive() bool { return s.IsActive && !s.IsDeleted }

// ScheduledDueDates lists the slots the rule lands on in the window, before any
// override or biller date. These are the occurrence's identity: charges are
// filed under them, or a bill whose due date moved reads as unpaid for ever.
func ScheduledDueDates(series Series, windowStart, windowEnd Date) []Date {
	if !series.IsLive() {
		return nil
	}
	return ExpandOccurrences(series.Recurrence, series.StartOn, windowStart, windowEnd, series.EndOn)
}

// OccurrenceSlot is one scheduled slot and the day it is shown on.
type OccurrenceSlot struct {
	ScheduledOn Date
	DueOn       Date
}

// OccurrenceSlots lists the occurrences shown in the window, each scheduled
// slot at its display date (OccurrenceDueOn), ascending by that date. A slot
// is listed once, on its display date and never also on its scheduled date,
// or the bill is projected twice.
func OccurrenceSlots(series Series, windowStart, windowEnd Date, bills []BillConnect) []OccurrenceSlot {
	if !series.IsLive() {
		return nil
	}
	// A bill moves a slot by at most its match window; only the next slot's
	// override moves one further.
	before, after := MatchWindow(series.Recurrence)
	candidates := ScheduledDueDates(series, windowStart.AddDays(-after), windowEnd.AddDays(before))
	if next := series.ScheduledDueOn(); !series.OverrideNextDueOn.IsZero() && !slices.Contains(candidates, next) {
		candidates = append(candidates, next)
	}
	out := make([]OccurrenceSlot, 0, len(candidates))
	for _, slot := range candidates {
		shown := OccurrenceDueOn(series, slot, bills)
		if windowStart.NotAfter(shown) && shown.NotAfter(windowEnd) {
			out = append(out, OccurrenceSlot{ScheduledOn: slot, DueOn: shown})
		}
	}
	slices.SortFunc(out, func(a, b OccurrenceSlot) int {
		if order := dateOrder(a.DueOn, b.DueOn); order != 0 {
			return order
		}
		return dateOrder(a.ScheduledOn, b.ScheduledOn)
	})
	return out
}

// Occurrence is one expected instance of a series: what is due, when, and for
// how much.
type Occurrence struct {
	// SeriesID and AccountID are empty on a pay-manually reminder, which is
	// one statement (BillID) and belongs to no series.
	SeriesID  ID
	AccountID ID
	Kind      SeriesKind
	// DueOn is the display date; ScheduledOn is the slot, the only key the
	// ledger files a charge under. Checking settled slots with DueOn makes a
	// moved bill read unpaid after it is paid.
	DueOn       Date
	ScheduledOn Date
	Amount      Money
	// PaysOn is the day the money actually leaves, when that is known — an
	// autopaying bill's own scheduled date. Zero otherwise.
	PaysOn Date
	// BillID is the linked bill this occurrence's figures came from, when one
	// speaks about the slot. A slot two same-day bills speak about is listed
	// once per bill.
	BillID ID
}

// MovesOn is the payment date when known, else the due date. Only the
// cash-flow projection asks this; every other reader asks when it is due.
func (o Occurrence) MovesOn() Date {
	if !o.PaysOn.IsZero() {
		return o.PaysOn
	}
	return o.DueOn
}

// SlotHolder is one transaction's claim on one occurrence slot, reduced to the
// facts that decide what the claim means.
type SlotHolder struct {
	SeriesID ID
	DueOn    Date
	// On is the row's own date, which is not the due date: a bill due the 15th
	// and paid the 12th is a settled slot, and one a provider wrote for next
	// April is not.
	On Date
	// IsForecast marks a row written ahead of the money — the provider's
	// projection of the occurrence, not a payment of it.
	IsForecast bool
	// IsSkipped covers both shapes of "this is not happening": the tombstone a
	// skip writes, and a charge since deleted.
	IsSkipped bool
}

// SkippedSlots is the occurrences the household said are not happening. The
// spending plan needs it: a skipped slot has no transaction and would otherwise
// sit in Bills as money owed. A live payment on the slot takes it back
// (IsSkipped also covers a deleted duplicate charge); a forecast does not.
func SkippedSlots(holders []SlotHolder) map[ID]map[Date]bool {
	skipped := map[ID]map[Date]bool{}
	answered := map[ID]map[Date]bool{}
	for _, holder := range holders {
		if holder.SeriesID == "" || holder.DueOn.IsZero() {
			continue
		}
		into := answered
		if holder.IsSkipped {
			into = skipped
		} else if holder.IsForecast {
			continue
		}
		if into[holder.SeriesID] == nil {
			into[holder.SeriesID] = map[Date]bool{}
		}
		into[holder.SeriesID][holder.DueOn] = true
	}
	for series, dues := range answered {
		for due := range dues {
			delete(skipped[series], due)
		}
		if len(skipped[series]) == 0 {
			delete(skipped, series)
		}
	}
	return skipped
}

// SettledSlots reports which occurrences some row names (claimed) and which
// have been dealt with (settled). A forecast, or a payment dated in the future,
// claims a slot without settling it. Callers must load skips and forecasts, or
// a skipped bill alerts as overdue for ever.
func SettledSlots(holders []SlotHolder, today Date) (claimed, settled map[ID]map[Date]bool) {
	claimed, settled = map[ID]map[Date]bool{}, map[ID]map[Date]bool{}
	mark := func(into map[ID]map[Date]bool, holder SlotHolder) {
		if into[holder.SeriesID] == nil {
			into[holder.SeriesID] = map[Date]bool{}
		}
		into[holder.SeriesID][holder.DueOn] = true
	}
	for _, holder := range holders {
		if holder.SeriesID == "" || holder.DueOn.IsZero() {
			continue
		}
		mark(claimed, holder)
		// A skip settles; otherwise only money that has moved does.
		if holder.IsSkipped || (!holder.IsForecast && !holder.On.After(today)) {
			mark(settled, holder)
		}
	}
	return claimed, settled
}
