package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDaysBetweenCountsCalendarDaysSigned(t *testing.T) {
	require.Equal(t, 1, DaysBetween(NewDate(2026, time.February, 28), NewDate(2026, time.March, 1)))
	require.Equal(t, 2, DaysBetween(NewDate(2028, time.February, 28), NewDate(2028, time.March, 1)))
	require.Equal(t, -31, DaysBetween(NewDate(2027, time.January, 1), NewDate(2026, time.December, 1)))
	require.Zero(t, DaysBetween(NewDate(2026, time.March, 8), NewDate(2026, time.March, 8)))
}

func recurrenceTestSeries() Series {
	return Series{
		ID:          "ser-rent",
		AccountID:   "acct-1",
		Description: "ACME PROPERTY MGMT RENT",
		Amount:      MustFromString("-1500.00"),
		Recurrence:  EveryMonth(1),
		StartOn:     NewDate(2026, time.January, 1),
		NextDueOn:   NewDate(2026, time.September, 1),
		Currency:    "USD",
		IsActive:    true,
	}
}

func TestEachAliasStoresTheRuleFieldsTheSpecGivesIt(t *testing.T) {
	cases := []struct {
		name       string
		recurrence Recurrence
		alias      RecurrenceAlias
		frequency  Frequency
		interval   int
	}{
		{"every week", EveryWeek(), AliasEveryWeek, FreqWeekly, 1},
		{"every month", EveryMonth(3), AliasEveryMonth, FreqMonthly, 1},
		{"twice a month", TwiceAMonth(1, 15), AliasTwiceAMonth, FreqMonthly, 1},
		{"every quarter", EveryQuarter(1), AliasEveryQuarter, FreqMonthly, 3},
		{"every year", EveryYear(), AliasEveryYear, FreqYearly, 1},
		{"every x days", EveryXDays(14), AliasEveryXDays, FreqDaily, 14},
		{"multiple fixed", MultipleFixed(5, 12, 20), AliasMultipleFixed, FreqMonthly, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.alias, tc.recurrence.Alias)
			require.Equal(t, tc.frequency, tc.recurrence.Frequency)
			require.Equal(t, tc.interval, tc.recurrence.Interval)
			require.NoError(t, tc.recurrence.Validate())
		})
	}
}

func TestARuleWithoutAnAliasReadsBackAsTheEntryThatWouldHaveBuiltIt(t *testing.T) {
	for _, made := range []Recurrence{
		EveryWeek(WeekdayMO), EveryMonth(3), TwiceAMonth(1, 15), EveryQuarter(1),
		EveryYear(), EveryXDays(10), MultipleFixed(1, 10, 20), OneTime(),
	} {
		require.Equal(t, made.Alias, AliasForRule(made.Frequency, made.Interval, made.ByMonthDay), "%+v", made)
	}
	// What no named entry spells is the "every X" one.
	require.Equal(t, AliasEveryXDays, AliasForRule(FreqMonthly, 12, []int{14}))
	require.Equal(t, AliasEveryXDays, AliasForRule(FreqWeekly, 2, nil))
	// A rule that never went through Validate may carry a zero interval.
	require.Equal(t, AliasEveryMonth, AliasForRule(FreqMonthly, 0, []int{14}))
}

func TestAOneTimeSeriesHasNoFrequencyAndNeverRepeats(t *testing.T) {
	rule := OneTime()
	require.Equal(t, AliasOneTime, rule.Alias)
	require.Equal(t, FreqNone, rule.Frequency)
	require.False(t, rule.Repeats())
	require.Equal(t,
		[]Date{NewDate(2026, time.March, 4)},
		ExpandOccurrences(rule, NewDate(2026, time.March, 4), NewDate(2026, time.January, 1), NewDate(2027, time.January, 1), Date{}),
	)
}

func TestTheAliasAndTheRuleFieldsMayNotDisagree(t *testing.T) {
	_, err := NewRecurrence(AliasEveryMonth, FreqNone, 1, nil, nil, nil)
	require.Error(t, err)
	_, err = NewRecurrence(AliasOneTime, FreqMonthly, 1, nil, nil, nil)
	require.Error(t, err)
}

func TestAnIntervalBelowOneIsRejected(t *testing.T) {
	_, err := NewRecurrence(AliasEveryXDays, FreqDaily, 0, nil, nil, nil)
	require.Error(t, err)
}

func TestADay31SeriesLandsOnTheLastDayOfFebruary(t *testing.T) {
	// Never skipped (a missed rent reminder) and never rolled into March (a
	// duplicate of the March occurrence).
	found := ExpandOccurrences(EveryMonth(31), NewDate(2026, time.January, 31),
		NewDate(2026, time.January, 1), NewDate(2026, time.May, 31), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.January, 31),
		NewDate(2026, time.February, 28),
		NewDate(2026, time.March, 31),
		NewDate(2026, time.April, 30),
		NewDate(2026, time.May, 31),
	}, found)
}

func TestADay31SeriesFindsThe29thInALeapFebruary(t *testing.T) {
	found := ExpandOccurrences(EveryMonth(31), NewDate(2028, time.January, 31),
		NewDate(2028, time.February, 1), NewDate(2028, time.February, 29), Date{})
	require.Equal(t, []Date{NewDate(2028, time.February, 29)}, found)
}

func TestClampingDoesNotMergeTwoDaysOfAMultipleFixedSeries(t *testing.T) {
	found := ExpandOccurrences(MultipleFixed(15, 30, 31), NewDate(2026, time.January, 15),
		NewDate(2026, time.February, 1), NewDate(2026, time.February, 28), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.February, 15),
		NewDate(2026, time.February, 28),
	}, found)
}

func TestANegativeDayCountsBackFromTheEndOfTheMonth(t *testing.T) {
	found := ExpandOccurrences(EveryMonth(-1), NewDate(2026, time.January, 1),
		NewDate(2026, time.January, 1), NewDate(2026, time.March, 31), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.January, 31),
		NewDate(2026, time.February, 28),
		NewDate(2026, time.March, 31),
	}, found)
}

func TestAFebruary29YearlySeriesClampsInTheYearsThatHaveNo29th(t *testing.T) {
	found := ExpandOccurrences(EveryYear(), NewDate(2028, time.February, 29),
		NewDate(2028, time.January, 1), NewDate(2030, time.December, 31), Date{})
	require.Equal(t, []Date{
		NewDate(2028, time.February, 29),
		NewDate(2029, time.February, 28),
		NewDate(2030, time.February, 28),
	}, found)
}

func TestOccurrencesNeverPredateTheAnchor(t *testing.T) {
	found := ExpandOccurrences(EveryMonth(1), NewDate(2026, time.June, 1),
		NewDate(2026, time.January, 1), NewDate(2026, time.July, 31), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.June, 1),
		NewDate(2026, time.July, 1),
	}, found)
}

func TestAQuarterlySeriesStepsThreeMonthsFromItsAnchor(t *testing.T) {
	found := ExpandOccurrences(EveryQuarter(15), NewDate(2026, time.February, 15),
		NewDate(2026, time.January, 1), NewDate(2026, time.December, 31), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.February, 15),
		NewDate(2026, time.May, 15),
		NewDate(2026, time.August, 15),
		NewDate(2026, time.November, 15),
	}, found)
}

func TestTwiceAMonthGivesBothDaysInOrder(t *testing.T) {
	found := ExpandOccurrences(TwiceAMonth(15, 1), NewDate(2026, time.January, 1),
		NewDate(2026, time.January, 1), NewDate(2026, time.February, 28), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.January, 1),
		NewDate(2026, time.January, 15),
		NewDate(2026, time.February, 1),
		NewDate(2026, time.February, 15),
	}, found)
}

func TestAWeeklySeriesUsesItsByDayRatherThanTheAnchorsWeekday(t *testing.T) {
	found := ExpandOccurrences(EveryWeek(WeekdayFR), NewDate(2026, time.August, 3),
		NewDate(2026, time.August, 1), NewDate(2026, time.August, 31), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.August, 7),
		NewDate(2026, time.August, 14),
		NewDate(2026, time.August, 21),
		NewDate(2026, time.August, 28),
	}, found)
}

func TestAWeeklySeriesWithoutByDayFallsBackToTheAnchorsWeekday(t *testing.T) {
	found := ExpandOccurrences(EveryWeek(), NewDate(2026, time.August, 3),
		NewDate(2026, time.August, 1), NewDate(2026, time.August, 24), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.August, 3),
		NewDate(2026, time.August, 10),
		NewDate(2026, time.August, 17),
		NewDate(2026, time.August, 24),
	}, found)
}

func TestAnEveryXDaysSeriesIgnoresMonthBoundaries(t *testing.T) {
	found := ExpandOccurrences(EveryXDays(14), NewDate(2026, time.January, 2),
		NewDate(2026, time.January, 1), NewDate(2026, time.March, 1), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.January, 2),
		NewDate(2026, time.January, 16),
		NewDate(2026, time.January, 30),
		NewDate(2026, time.February, 13),
		NewDate(2026, time.February, 27),
	}, found)
}

func TestAWindowStartingMidStreamKeepsTheIntervalPhase(t *testing.T) {
	found := ExpandOccurrences(EveryXDays(14), NewDate(2026, time.January, 2),
		NewDate(2026, time.June, 1), NewDate(2026, time.June, 30), Date{})
	require.Equal(t, []Date{
		NewDate(2026, time.June, 5),
		NewDate(2026, time.June, 19),
	}, found)
}

func TestAnEndDateStopsTheSeries(t *testing.T) {
	found := ExpandOccurrences(EveryMonth(1), NewDate(2026, time.January, 1),
		NewDate(2026, time.January, 1), NewDate(2026, time.December, 31), NewDate(2026, time.March, 15))
	require.Equal(t, []Date{
		NewDate(2026, time.January, 1),
		NewDate(2026, time.February, 1),
		NewDate(2026, time.March, 1),
	}, found)
}

func TestAnInvertedWindowIsEmptyRatherThanAnError(t *testing.T) {
	found := ExpandOccurrences(EveryMonth(1), NewDate(2026, time.January, 1),
		NewDate(2026, time.May, 1), NewDate(2026, time.April, 1), Date{})
	require.Empty(t, found)
}

func TestTheNextOccurrenceIsTheFirstStrictlyLaterThanTheDateGiven(t *testing.T) {
	found, ok := NextOccurrenceAfter(EveryMonth(1), NewDate(2026, time.January, 1), NewDate(2026, time.September, 1), Date{})
	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.October, 1), found)
}

func TestThereIsNoNextOccurrenceOnceTheSeriesHasEnded(t *testing.T) {
	_, ok := NextOccurrenceAfter(EveryMonth(1), NewDate(2026, time.January, 1),
		NewDate(2026, time.September, 1), NewDate(2026, time.September, 30))
	require.False(t, ok)
}

func TestAOneTimeSeriesHasNothingAfterIt(t *testing.T) {
	_, ok := NextOccurrenceAfter(OneTime(), NewDate(2026, time.March, 4), NewDate(2026, time.March, 4), Date{})
	require.False(t, ok)
}

func TestAnEvery14DaysSeriesFalls26TimesInMostYears(t *testing.T) {
	require.Equal(t, 26, OccurrencesPerYear(EveryXDays(14), NewDate(2026, time.January, 2), 2026, Date{}))
}

func TestTheSameSeriesFalls27TimesInTheYearThePhaseLinesUp(t *testing.T) {
	// A frequency table would say 26.
	require.Equal(t, 27, OccurrencesPerYear(EveryXDays(14), NewDate(2026, time.January, 2), 2027, Date{}))
}

func TestAMonthlySeriesIsAlwaysTwelve(t *testing.T) {
	require.Equal(t, 12, OccurrencesPerYear(EveryMonth(31), NewDate(2026, time.January, 31), 2026, Date{}))
}

func TestASeriesThatStartsMidYearOnlyCountsFromItsAnchor(t *testing.T) {
	require.Equal(t, 3, OccurrencesPerYear(EveryMonth(1), NewDate(2026, time.October, 1), 2026, Date{}))
}

func TestAnnualizedAmountMultipliesByTheRealCountAndStaysExact(t *testing.T) {
	amount := MustFromString("-120.50")
	rule := EveryXDays(14)
	require.Equal(t, "-3133.00", AnnualizedAmount(amount, rule, NewDate(2026, time.January, 2), 2026, Date{}).String())
	require.Equal(t, "-3253.50", AnnualizedAmount(amount, rule, NewDate(2026, time.January, 2), 2027, Date{}).String())
}

func TestTheExtraPaycheckMonthsAreTheOnesHoldingAThirdOccurrence(t *testing.T) {
	found := ExtraOccurrenceMonths(EveryXDays(14), NewDate(2026, time.January, 2), 2027, Date{})
	require.Equal(t, []Month{
		NewMonth(2027, time.January),
		NewMonth(2027, time.July),
		NewMonth(2027, time.December),
	}, found)
}

func TestAMonthlySeriesHasNoExtraMonth(t *testing.T) {
	require.Empty(t, ExtraOccurrenceMonths(EveryMonth(1), NewDate(2026, time.January, 1), 2026, Date{}))
}

func TestThePeriodEstimateIsInTheRightBallpark(t *testing.T) {
	cases := []struct {
		name      string
		rule      Recurrence
		low, high float64
	}{
		{"every week", EveryWeek(), 6, 8},
		{"every 14 days", EveryXDays(14), 13, 15},
		{"twice a month", TwiceAMonth(1, 15), 14, 16},
		{"every month", EveryMonth(1), 29, 32},
		{"every quarter", EveryQuarter(1), 88, 95},
		{"every year", EveryYear(), 360, 370},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := PeriodDays(tc.rule)
			require.GreaterOrEqual(t, found, tc.low)
			require.LessOrEqual(t, found, tc.high)
		})
	}
}

func TestTheLabelIsTheDisplayNameWhenThereIsOne(t *testing.T) {
	series := recurrenceTestSeries()
	series.DisplayName = "Rent"
	require.Equal(t, "Rent", series.Label())
}

func TestTheLabelFallsBackToTheDescription(t *testing.T) {
	require.Equal(t, "ACME PROPERTY MGMT RENT", recurrenceTestSeries().Label())
}

func TestTheDueDatePrefersTheOneOffOverride(t *testing.T) {
	series := recurrenceTestSeries()
	require.Equal(t, NewDate(2026, time.September, 1), series.DueOn())

	series.OverrideNextDueOn = NewDate(2026, time.September, 4)
	require.Equal(t, NewDate(2026, time.September, 4), series.DueOn())
	require.Equal(t, NewDate(2026, time.September, 1), series.ScheduledDueOn())
}

func TestThePointerStartsOnTheAnchorWhenItHasNotMoved(t *testing.T) {
	series := recurrenceTestSeries()
	series.NextDueOn = Date{}
	require.Equal(t, NewDate(2026, time.January, 1), series.DueOn())
}

func TestAnOverrideMovesAnOccurrenceRatherThanAddingOne(t *testing.T) {
	// Adding one would project a bill pushed back three days twice.
	series := recurrenceTestSeries()
	series.OverrideNextDueOn = NewDate(2026, time.September, 4)
	require.Equal(t, []OccurrenceSlot{
		{ScheduledOn: NewDate(2026, time.August, 1), DueOn: NewDate(2026, time.August, 1)},
		{ScheduledOn: NewDate(2026, time.September, 1), DueOn: NewDate(2026, time.September, 4)},
		{ScheduledOn: NewDate(2026, time.October, 1), DueOn: NewDate(2026, time.October, 1)},
	}, OccurrenceSlots(series, NewDate(2026, time.August, 1), NewDate(2026, time.October, 31), nil))
}

func TestADeletedOrPausedSeriesHasNoDueDates(t *testing.T) {
	start, end := NewDate(2026, time.August, 1), NewDate(2026, time.October, 31)

	deleted := recurrenceTestSeries()
	deleted.IsDeleted = true
	require.Empty(t, OccurrenceSlots(deleted, start, end, nil))

	paused := recurrenceTestSeries()
	paused.IsActive = false
	require.Empty(t, OccurrenceSlots(paused, start, end, nil))
}

func TestAForecastClaimsASlotWithoutSettlingIt(t *testing.T) {
	today := NewDate(2026, time.September, 20)
	holders := []SlotHolder{{
		SeriesID: "s-mortgage", DueOn: NewDate(2026, time.September, 1),
		On: NewDate(2026, time.September, 1), IsForecast: true,
	}}

	claimed, settled := SettledSlots(holders, today)

	require.True(t, claimed["s-mortgage"][NewDate(2026, time.September, 1)],
		"the row is there, which is what stops the bill being projected twice")
	require.False(t, settled["s-mortgage"][NewDate(2026, time.September, 1)],
		"but nothing has paid it, so the bill is still due")
}

func TestAPostedChargeSettlesTheSlotItFills(t *testing.T) {
	today := NewDate(2026, time.September, 20)
	// Paid the 12th for an occurrence due the 15th: the row's own date is what
	// says the money has moved, not the due date.
	holders := []SlotHolder{{
		SeriesID: "s-rent", DueOn: NewDate(2026, time.September, 15),
		On: NewDate(2026, time.September, 12),
	}}

	_, settled := SettledSlots(holders, today)

	require.True(t, settled["s-rent"][NewDate(2026, time.September, 15)])
}

func TestASkippedOccurrenceIsSettled(t *testing.T) {
	today := NewDate(2026, time.September, 20)
	holders := []SlotHolder{{
		SeriesID: "s-gym", DueOn: NewDate(2026, time.September, 1),
		On: NewDate(2026, time.September, 1), IsSkipped: true,
	}}

	_, settled := SettledSlots(holders, today)

	require.True(t, settled["s-gym"][NewDate(2026, time.September, 1)])
}

// A payment dated next week has not happened yet, so the bill it will pay is
// still owed today.
func TestAFuturePaymentDoesNotSettleItsSlotYet(t *testing.T) {
	today := NewDate(2026, time.September, 20)
	holders := []SlotHolder{{
		SeriesID: "s-rent", DueOn: NewDate(2026, time.October, 1),
		On: NewDate(2026, time.September, 30),
	}}

	claimed, settled := SettledSlots(holders, today)

	require.True(t, claimed["s-rent"][NewDate(2026, time.October, 1)])
	require.False(t, settled["s-rent"][NewDate(2026, time.October, 1)])
}

// Forecast and payment on one slot: settled, because one is money that moved.
func TestAPaidSlotStaysSettledWhenAForecastAlsoHoldsIt(t *testing.T) {
	today := NewDate(2026, time.September, 20)
	due := NewDate(2026, time.September, 1)
	holders := []SlotHolder{
		{SeriesID: "s-mortgage", DueOn: due, On: due, IsForecast: true},
		{SeriesID: "s-mortgage", DueOn: due, On: due},
	}

	_, settled := SettledSlots(holders, today)

	require.True(t, settled["s-mortgage"][due])
}

// A payment-settled slot is accounted for by its payment; only a skipped one
// needs SkippedSlots.
func TestOnlySkippedSlotsAreSkipped(t *testing.T) {
	today := NewDate(2026, time.September, 20)
	due := NewDate(2026, time.September, 1)
	holders := []SlotHolder{
		{SeriesID: "s-gym", DueOn: due, On: due, IsSkipped: true},
		{SeriesID: "s-rent", DueOn: due, On: due},
		{SeriesID: "s-mortgage", DueOn: due, On: due, IsForecast: true},
	}

	skipped := SkippedSlots(holders)
	_, settled := SettledSlots(holders, today)

	require.True(t, skipped["s-gym"][due])
	require.False(t, skipped["s-rent"][due], "paid is not skipped")
	require.True(t, settled["s-rent"][due], "but it is settled")
	require.False(t, skipped["s-mortgage"][due])
}

// A deleted duplicate is a skipped holder; the surviving payment in the same
// slot must win, or the plan shows it excluded with nothing to include.
func TestAPaymentStillInTheSlotTakesBackADeletedRowsSkip(t *testing.T) {
	due := NewDate(2026, time.August, 14)
	holders := []SlotHolder{
		{SeriesID: "s-payroll", DueOn: due, On: due, IsSkipped: true},
		{SeriesID: "s-payroll", DueOn: due, On: due},
	}

	require.False(t, SkippedSlots(holders)["s-payroll"][due])
}

// A skipped bill whose provider still projects it stays skipped.
func TestAForecastDoesNotTakeBackASkip(t *testing.T) {
	due := NewDate(2026, time.August, 1)
	holders := []SlotHolder{
		{SeriesID: "s-gym", DueOn: due, On: due, IsForecast: true},
		{SeriesID: "s-gym", DueOn: due, On: due, IsSkipped: true},
	}

	require.True(t, SkippedSlots(holders)["s-gym"][due])
}

func TestOneSlotsPaymentDoesNotUnskipTheNextMonths(t *testing.T) {
	august, september := NewDate(2026, time.August, 1), NewDate(2026, time.September, 1)
	holders := []SlotHolder{
		{SeriesID: "s-gym", DueOn: august, On: august},
		{SeriesID: "s-gym", DueOn: september, On: september, IsSkipped: true},
	}

	skipped := SkippedSlots(holders)
	require.False(t, skipped["s-gym"][august])
	require.True(t, skipped["s-gym"][september])
}
