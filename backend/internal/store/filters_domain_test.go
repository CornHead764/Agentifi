package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestDomainFilterResolvesADatePresetAtEvaluationTime(t *testing.T) {
	today := domainDate(2026, time.August, 23)
	stored := Filter{Items: []FilterItem{{Field: "date", Operator: "between", DatePreset: "this-year"}}}

	got := DomainFilterOn(stored, today)
	require.Equal(t, domain.NewDate(2026, time.January, 1), got.Items[0].Start)
	require.Equal(t, today, got.Items[0].End)
}

func TestDomainFilterRollsARelativePresetForward(t *testing.T) {
	// The saved dates are a snapshot of the day the filter was created; the
	// preset is what has to keep meaning "the last three months".
	stored := Filter{Items: []FilterItem{{
		Field: "date", Operator: "between",
		DateFrom: domainDate(2026, time.March, 23), DateTo: domainDate(2026, time.August, 23),
		DatePreset: "-3m",
	}}}

	got := DomainFilterOn(stored, domainDate(2026, time.November, 30))
	require.Equal(t, domain.NewDate(2026, time.August, 30), got.Items[0].Start, "AddDate normalizes month ends")
	require.Equal(t, domain.NewDate(2026, time.November, 30), got.Items[0].End)
}

func TestDomainFilterFallsBackToSavedDatesOnAnUnknownPreset(t *testing.T) {
	stored := Filter{Items: []FilterItem{{
		Field: "date", Operator: "between",
		DateFrom: domainDate(2026, time.January, 1), DateTo: domainDate(2026, time.June, 30),
		DatePreset: "whenever",
	}}}

	got := DomainFilterOn(stored, domainDate(2026, time.August, 23))
	require.Equal(t, domain.NewDate(2026, time.January, 1), got.Items[0].Start,
		"an unknown token degrades to the window the user saw, not to an open range")
	require.Equal(t, domain.NewDate(2026, time.June, 30), got.Items[0].End)
}

func TestResolveDatePresetNamedWindows(t *testing.T) {
	day := domainDate(2026, time.August, 23)

	from, to, ok := resolveDatePreset("last-month", day)
	require.True(t, ok)
	require.Equal(t, domain.NewDate(2026, time.July, 1), from)
	require.Equal(t, domain.NewDate(2026, time.July, 31), to)

	from, to, ok = resolveDatePreset("last-quarter", day)
	require.True(t, ok)
	require.Equal(t, domain.NewDate(2026, time.April, 1), from)
	require.Equal(t, domain.NewDate(2026, time.June, 30), to)

	january := domainDate(2027, time.January, 9)
	from, to, _ = resolveDatePreset("last-month", january)
	require.Equal(t, domain.NewDate(2026, time.December, 1), from)
	require.Equal(t, domain.NewDate(2026, time.December, 31), to)
	from, to, _ = resolveDatePreset("last-quarter", january)
	require.Equal(t, domain.NewDate(2026, time.October, 1), from)
	require.Equal(t, domain.NewDate(2026, time.December, 31), to)

	from, to, ok = resolveDatePreset("all-time", day)
	require.True(t, ok)
	require.Zero(t, from)
	require.Zero(t, to)
}

func domainDate(year int, month time.Month, day int) domain.Date {
	return domain.NewDate(year, month, day)
}
