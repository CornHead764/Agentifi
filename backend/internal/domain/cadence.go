package domain

import (
	"math"
	"sort"
)

// Cadence detection names the rhythm of a run of charges nobody has made a
// series of yet, and turns it into the series a suggestion proposes.

// MaxIntervalJitter is how far gaps may vary, as a fraction of the cadence;
// weekends and holidays move a charge by a couple of days.
const MaxIntervalJitter = 0.35

// cadence is one supported rhythm. The day ranges are disjoint and leave holes
// on purpose: a rhythm that fits none has no faithful recurrence and would
// drift.
type cadence struct {
	name string
	low  int
	high int
}

var cadences = []cadence{
	{"weekly", 6, 8},
	{"biweekly", 13, 16},
	{"monthly", 27, 34},
	{"quarterly", 85, 96},
	{"yearly", 350, 380},
}

// looksSemiMonthly reports twice-a-month rather than fortnightly gaps. 24 a
// year average 15.2 days apart, 26 average 14.05, and biweekly is pinned near
// 14, so a mean above the midpoint is semi-monthly, which the model cannot
// express. Short history under-fires, never over-fires.
func looksSemiMonthly(intervals []int) bool {
	if len(intervals) < 3 {
		return false
	}
	total := 0
	for _, gap := range intervals {
		total += gap
	}
	return float64(total)/float64(len(intervals)) >= 14.9
}

// ClassifyCadence names the rhythm these gaps describe, with a 0..1
// regularity, or false when they fit no cadence or wander too much.
func ClassifyCadence(intervals []int) (string, float64, bool) {
	if len(intervals) == 0 {
		return "", 0, false
	}
	middle := medianInts(intervals)
	name := ""
	for _, c := range cadences {
		if float64(c.low) <= middle && middle <= float64(c.high) {
			name = c.name
			break
		}
	}
	if name == "" {
		return "", 0, false
	}
	if name == "biweekly" && looksSemiMonthly(intervals) {
		return "", 0, false
	}
	// Every gap must be near the median, not just their average.
	worst := 0.0
	for _, gap := range intervals {
		if off := math.Abs(float64(gap) - middle); off > worst {
			worst = off
		}
	}
	if worst > middle*MaxIntervalJitter {
		return "", 0, false
	}
	return name, 1.0 - worst/(middle*MaxIntervalJitter), true
}

// medianInts averages the two middles for an even count.
func medianInts(values []int) float64 {
	sorted := append([]int{}, values...)
	sort.Ints(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return float64(sorted[middle])
	}
	return float64(sorted[middle-1]+sorted[middle]) / 2
}

// RecurrenceFor is the cadence as an RRULE anchored on the last sighting.
// Fortnightly is every-14-days, the alias Simplifi's dropdown offers.
func RecurrenceFor(name string, anchor Date) Recurrence {
	switch name {
	case "weekly":
		return EveryWeek(weekdayOf(anchor))
	case "biweekly":
		return EveryXDays(14)
	case "monthly":
		return EveryMonth(anchor.Day)
	case "quarterly":
		return EveryQuarter(anchor.Day)
	}
	return EveryYear()
}

// weekdayOf is the RRULE code for a date.
func weekdayOf(on Date) Weekday {
	codes := []Weekday{
		WeekdayMO, WeekdayTU, WeekdayWE, WeekdayTH, WeekdayFR, WeekdaySA, WeekdaySU,
	}
	return codes[mondayIndex(on)]
}

// SuggestTolerance is exact for a fixed amount, otherwise the auto band widened
// from what the series has charged.
func SuggestTolerance(amounts []Money) AmountTolerance {
	seen := map[string]bool{}
	for _, amount := range amounts {
		seen[amount.String()] = true
	}
	if len(seen) <= 1 {
		return ExactAmount()
	}
	return AutoAmount()
}

// MedianAmount is the middle amount, to the cent: one bonus does not drag it.
func MedianAmount(amounts []Money) Money {
	sorted := make([]Money, len(amounts))
	copy(sorted, amounts)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].LessThan(sorted[j]) })
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle].Round()
	}
	pair := sorted[middle-1].Add(sorted[middle])
	halved, _ := pair.DivInt(2)
	return halved.Round()
}

// NextStart is the first predicted occurrence not yet past. The anchor stays
// on the last sighting so the rule keeps the pattern's phase.
func NextStart(recurrence Recurrence, lastSeen, today Date) Date {
	after := lastSeen
	if yesterday := today.AddDays(-1); yesterday.After(after) {
		after = yesterday
	}
	if found, ok := NextOccurrenceAfter(recurrence, lastSeen, after, Date{}); ok {
		return found
	}
	return lastSeen.AddDays(int(PeriodDays(recurrence)))
}
