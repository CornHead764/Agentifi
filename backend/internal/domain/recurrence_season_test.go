package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// aprilToOctober is lawn care: monthly, active April to October.
func aprilToOctober(day int) Recurrence {
	r := EveryMonth(day)
	r.ByMonth = []time.Month{
		time.April, time.May, time.June, time.July, time.August, time.September, time.October,
	}
	return r
}

func TestASeasonalMonthlySeriesFallsOnlyInItsActiveMonths(t *testing.T) {
	found := ExpandOccurrences(aprilToOctober(12), NewDate(2026, time.April, 12),
		NewDate(2026, time.January, 1), NewDate(2027, time.June, 30), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.April, 12), NewDate(2026, time.May, 12), NewDate(2026, time.June, 12),
		NewDate(2026, time.July, 12), NewDate(2026, time.August, 12), NewDate(2026, time.September, 12),
		NewDate(2026, time.October, 12),
		NewDate(2027, time.April, 12), NewDate(2027, time.May, 12), NewDate(2027, time.June, 12),
	}, found)
}

func TestASeasonalSeriesCountsSevenTimesAYearAndNothingInWinter(t *testing.T) {
	rule := aprilToOctober(12)
	start := NewDate(2026, time.April, 12)
	require.Equal(t, 7, OccurrencesPerYear(rule, start, 2027, Date{}))
	require.True(t, MustFromString("-385.00").Equal(
		AnnualizedAmount(MustFromString("-55.00"), rule, start, 2027, Date{})))
	counts := OccurrencesByMonth(rule, start, 2027, Date{})
	require.Zero(t, counts[NewMonth(2027, time.December)])
	require.Zero(t, counts[NewMonth(2027, time.March)])
	require.Equal(t, 1, counts[NewMonth(2027, time.April)])
}

func TestTheOccurrenceAfterTheLastOfTheSeasonIsTheFirstOfTheNext(t *testing.T) {
	found, ok := NextOccurrenceAfter(aprilToOctober(12), NewDate(2026, time.April, 12),
		NewDate(2026, time.October, 12), Date{})
	require.True(t, ok)
	require.Equal(t, NewDate(2027, time.April, 12), found)
}

func TestActiveMonthsKeepAnEveryXDaysSeriesOnItsPhase(t *testing.T) {
	// Every 14 days from 2 January falls on 5 and 19 June; the months before
	// do not restart the count.
	rule := EveryXDays(14)
	rule.ByMonth = []time.Month{time.June}
	found := ExpandOccurrences(rule, NewDate(2026, time.January, 2),
		NewDate(2026, time.January, 1), NewDate(2026, time.December, 31), Date{})
	require.Equal(t, []Date{NewDate(2026, time.June, 5), NewDate(2026, time.June, 19)}, found)
}

func TestActiveMonthsAreSortedDistinctAndAllTwelveMeansNone(t *testing.T) {
	built, err := NewRecurrence(AliasEveryMonth, FreqMonthly, 1, []int{12}, nil,
		[]time.Month{time.October, time.April, time.April})
	require.NoError(t, err)
	require.Equal(t, []time.Month{time.April, time.October}, built.ByMonth)

	all := make([]time.Month, 0, 12)
	for month := time.January; month <= time.December; month++ {
		all = append(all, month)
	}
	built, err = NewRecurrence(AliasEveryMonth, FreqMonthly, 1, []int{12}, nil, all)
	require.NoError(t, err)
	require.Nil(t, built.ByMonth)
}

func TestAYearlyOrOneTimeRuleTakesNoActiveMonths(t *testing.T) {
	_, err := NewRecurrence(AliasEveryYear, FreqYearly, 1, nil, nil, []time.Month{time.May})
	require.Error(t, err)
	_, err = NewRecurrence(AliasOneTime, FreqNone, 1, nil, nil, []time.Month{time.May})
	require.Error(t, err)
	_, err = NewRecurrence(AliasEveryWeek, FreqWeekly, 1, nil, nil, []time.Month{time.May})
	require.NoError(t, err)
}

func TestAMonthOutsideTheYearIsRefused(t *testing.T) {
	_, err := NewRecurrence(AliasEveryMonth, FreqMonthly, 1, []int{1}, nil, []time.Month{13})
	require.Error(t, err)
}

func TestASeriesStartedOffSeasonBeginsAtTheSeasonsFirstOccurrence(t *testing.T) {
	start, ok := ActiveStart(aprilToOctober(10), NewDate(2026, time.November, 10), Date{})
	require.True(t, ok)
	require.Equal(t, NewDate(2027, time.April, 10), start)

	start, ok = ActiveStart(aprilToOctober(10), NewDate(2026, time.May, 3), Date{})
	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.May, 3), start)
}

func TestARuleWhosePhaseMissesEveryActiveMonthHasNoStart(t *testing.T) {
	// Quarterly from January falls in January, April, July and October.
	rule := EveryQuarter(15)
	rule.ByMonth = []time.Month{time.February}
	_, ok := ActiveStart(rule, NewDate(2026, time.January, 15), Date{})
	require.False(t, ok)
}

func TestASeasonalSeriesShowsNoSlotOffSeason(t *testing.T) {
	series := recurrenceTestSeries()
	series.Recurrence = aprilToOctober(12)
	series.StartOn = NewDate(2026, time.April, 12)
	series.NextDueOn = NewDate(2026, time.October, 12)
	slots := OccurrenceSlots(series, NewDate(2026, time.October, 1), NewDate(2027, time.March, 31), nil)
	require.Equal(t, []OccurrenceSlot{{
		ScheduledOn: NewDate(2026, time.October, 12), DueOn: NewDate(2026, time.October, 12),
	}}, slots)
}

func TestAnOffSeasonChargeFillsNoSlotOfASeasonalSeries(t *testing.T) {
	series := recurrenceTestSeries()
	series.Recurrence = aprilToOctober(12)
	series.StartOn = NewDate(2026, time.April, 12)
	context := MatchContext{Series: series, Tolerance: AnyAmount()}
	txn := Transaction{
		AccountID: series.AccountID, Amount: series.Amount, Currency: "USD",
		StatementName: series.Description, Date: NewDate(2026, time.December, 12),
	}
	_, found := MatchingOccurrence(context, txn)
	require.False(t, found)

	txn.Date = NewDate(2026, time.September, 13)
	on, found := MatchingOccurrence(context, txn)
	require.True(t, found)
	require.Equal(t, NewDate(2026, time.September, 12), on)
}

func TestTheSeasonEndsTheSchedulePointerAtTheNextSpring(t *testing.T) {
	series := recurrenceTestSeries()
	series.Recurrence = aprilToOctober(12)
	series.StartOn = NewDate(2026, time.April, 12)
	series.NextDueOn = NewDate(2026, time.October, 12)
	next, ok := AdvancePointerPast(MatchContext{Series: series}, NewDate(2026, time.October, 12))
	require.True(t, ok)
	require.Equal(t, NewDate(2027, time.April, 12), next)
}
