package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFirstDayAfterClampsToTheMonth(t *testing.T) {
	require.Equal(t, NewDate(2026, time.February, 28), FirstDayAfter(31, NewDate(2026, time.February, 10)))
	require.Equal(t, NewDate(2028, time.February, 29), FirstDayAfter(31, NewDate(2028, time.February, 10)))
	require.Equal(t, NewDate(2026, time.March, 31), FirstDayAfter(31, NewDate(2026, time.February, 28)))
}

func TestAChargeOnClosingDayBelongsToTheNextStatement(t *testing.T) {
	require.Equal(t, NewDate(2026, time.March, 15), CycleCloseFor(NewDate(2026, time.March, 14), 15))
	require.Equal(t, NewDate(2026, time.April, 15), CycleCloseFor(NewDate(2026, time.March, 15), 15))
}

func TestEffectiveDateIsTheDueDateOfTheStatementTheChargeLandsIn(t *testing.T) {
	// Closes the 15th, due the 5th: a March 10 charge closes March 15 and is
	// paid April 5.
	require.Equal(t, NewDate(2026, time.April, 5),
		ComputeEffectiveDate(NewDate(2026, time.March, 10), 15, 5))
	// A charge on the closing day rolls into the next statement, a month later.
	require.Equal(t, NewDate(2026, time.May, 5),
		ComputeEffectiveDate(NewDate(2026, time.March, 15), 15, 5))
}

func TestADueDayAfterTheCloseStaysInTheSameMonth(t *testing.T) {
	require.Equal(t, NewDate(2026, time.March, 25),
		ComputeEffectiveDate(NewDate(2026, time.March, 2), 5, 25))
}

func TestYearEndRollsOver(t *testing.T) {
	require.Equal(t, NewDate(2027, time.February, 5),
		ComputeEffectiveDate(NewDate(2026, time.December, 20), 15, 5))
}

func TestAnUnconfiguredCycleLeavesTheChargeOnItsOwnDate(t *testing.T) {
	require.Equal(t, NewDate(2026, time.March, 10), ComputeEffectiveDate(NewDate(2026, time.March, 10), 0, 5))
	require.Equal(t, NewDate(2026, time.March, 10), ComputeEffectiveDate(NewDate(2026, time.March, 10), 15, 0))
}

func TestNonCardsStoreNothingRatherThanACopyOfTheDate(t *testing.T) {
	require.True(t, EffectiveDateFor(NewDate(2026, time.March, 10), false, 15, 5, Date{}).IsZero())
}

func TestACardWithoutACycleStoresNothing(t *testing.T) {
	require.True(t, EffectiveDateFor(NewDate(2026, time.March, 10), true, 0, 5, Date{}).IsZero())
}

func TestTheBillFeedWinsOverTheCycle(t *testing.T) {
	resolved := EffectiveDateFor(NewDate(2026, time.March, 10), true, 15, 5, NewDate(2026, time.April, 2))
	require.Equal(t, NewDate(2026, time.April, 2), resolved)
}
