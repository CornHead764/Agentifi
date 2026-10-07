package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCadencesAreNamedFromTheMedianGap(t *testing.T) {
	for _, c := range []struct {
		expected  string
		intervals []int
	}{
		{"monthly", []int{31, 28, 31}},
		{"weekly", []int{7, 7, 7}},
		{"biweekly", []int{14, 14, 14}},
		{"quarterly", []int{91, 92}},
		{"yearly", []int{365}},
	} {
		name, _, ok := ClassifyCadence(c.intervals)
		require.True(t, ok, c.expected)
		require.Equal(t, c.expected, name)
	}
}

func TestARhythmTheModelCannotExpressIsLeftAlone(t *testing.T) {
	_, _, ok := ClassifyCadence([]int{45, 47})
	require.False(t, ok)
}

func TestOneWanderingGapDisqualifiesTheWholeGroup(t *testing.T) {
	// Two charges a fortnight apart and one six months later average out to
	// something monthly-looking, and must not.
	_, _, ok := ClassifyCadence([]int{14, 180})
	require.False(t, ok)
}

func TestTwiceAMonthIsNotProposedAsEveryFortnight(t *testing.T) {
	// 24 a year average 15.2 days apart and 26 average 14.05, so the mean
	// separates them where no single gap can.
	_, _, ok := ClassifyCadence([]int{15, 16, 15, 16})
	require.False(t, ok)

	name, _, ok := ClassifyCadence([]int{14, 15, 14, 13})
	require.True(t, ok)
	require.Equal(t, "biweekly", name)
}

func TestEachCadenceBecomesARealRRule(t *testing.T) {
	anchor := NewDate(2026, time.March, 5)
	require.Equal(t, EveryMonth(5), RecurrenceFor("monthly", anchor))
	require.Equal(t, AliasEveryXDays, RecurrenceFor("biweekly", anchor).Alias)
	require.Equal(t, 3, RecurrenceFor("weekly", anchor).ByDay[0].Index())
}
