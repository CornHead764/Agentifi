package domain

import "time"

// Month is a year and a month — the unit the spending plan is materialized in.
type Month struct {
	Year  int
	Month time.Month
}

func NewMonth(year int, month time.Month) Month { return Month{year, month} }

func MonthOf(d Date) Month { return Month{d.Year, d.Month} }

func (m Month) String() string { return m.FirstDay().Time().Format("2006-01") }

// ParseMonth reads a calendar month written as 2006-01, or false for anything
// else — a full date, a trailing fragment, or no month at all.
func ParseMonth(s string) (Month, bool) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return Month{}, false
	}
	return Month{t.Year(), t.Month()}, true
}

func (m Month) FirstDay() Date { return Date{m.Year, m.Month, 1} }

func (m Month) LastDay() Date { return Date{m.Year, m.Month, m.Days()} }

// Days is the length of this month, leap years included.
func (m Month) Days() int {
	// Day zero of the next month is the last day of this one.
	return time.Date(m.Year, m.Month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func (m Month) Contains(d Date) bool { return d.Year == m.Year && d.Month == m.Month }

func (m Month) Next() Month { return m.Shift(1) }
func (m Month) Prev() Month { return m.Shift(-1) }

func (m Month) Shift(months int) Month {
	index := m.Year*12 + int(m.Month) - 1 + months
	return Month{index / 12, time.Month(index%12 + 1)}
}

// Day returns the given day of this month, clamped to its last day: a
// BYMONTHDAY=[31] series lands on Feb 28/29, never skipped or rolled into
// March.
func (m Month) Day(dayOfMonth int) Date {
	if dayOfMonth > m.Days() {
		dayOfMonth = m.Days()
	}
	if dayOfMonth < 1 {
		dayOfMonth = 1
	}
	return Date{m.Year, m.Month, dayOfMonth}
}

func (m Month) Before(o Month) bool { return m.Compare(o) < 0 }
func (m Month) After(o Month) bool  { return m.Compare(o) > 0 }

func (m Month) Compare(o Month) int {
	switch {
	case m.Year != o.Year:
		if m.Year < o.Year {
			return -1
		}
		return 1
	case m.Month != o.Month:
		if m.Month < o.Month {
			return -1
		}
		return 1
	}
	return 0
}

// MonthsBetween lists every month from start to end inclusive, empty if end
// precedes start.
func MonthsBetween(start, end Month) []Month {
	var out []Month
	for cur := start; !cur.After(end); cur = cur.Next() {
		out = append(out, cur)
	}
	return out
}

// MonthCount is the signed number of month steps from start to end.
func MonthCount(start, end Month) int {
	return (end.Year-start.Year)*12 + int(end.Month) - int(start.Month)
}

// DaysElapsedInMonth counts the days already lived including today, and is
// never zero: run-rate projections divide by it.
func DaysElapsedInMonth(today Date) int { return today.Day }

// DaysRemainingInMonth counts the days left including today, since today's
// share is not yet spent. The divisor for "left this month, per day".
func DaysRemainingInMonth(today Date) int {
	return MonthOf(today).Days() - today.Day + 1
}

// ClampToMonth builds a date that survives day 31 in a 30-day month.
func ClampToMonth(year int, month time.Month, dayOfMonth int) Date {
	return Month{year, month}.Day(dayOfMonth)
}

// FullMonthsBefore lists the count complete months ending before the one today
// falls in, oldest first. Trailing averages exclude the current partial month,
// which would drag them down.
func FullMonthsBefore(today Date, count int) []Month {
	latest := MonthOf(today).Prev()
	out := make([]Month, 0, count)
	for offset := count - 1; offset >= 0; offset-- {
		out = append(out, latest.Shift(-offset))
	}
	return out
}

// DateRange lists every day from start to end inclusive.
func DateRange(start, end Date) []Date {
	var out []Date
	for cur := start; !cur.After(end); cur = cur.AddDays(1) {
		out = append(out, cur)
	}
	return out
}

// DaysBetween is the signed number of whole days from one calendar day to
// another, negative going back.
func DaysBetween(from, to Date) int {
	return int(to.Time().Sub(from.Time()) / (24 * time.Hour))
}
