package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The expected values come from calculations.md §8: an unpaid bill turns past
// due the day after its due date, and income never turns past due.

func TestOccurrenceStatusOf(t *testing.T) {
	due := dupDay("2026-03-10")
	cases := []struct {
		name      string
		kind      SeriesKind
		fulfilled bool
		asOf      string
		want      OccurrenceStatus
	}{
		{"bill on its due date", SeriesBill, false, "2026-03-10", OccurrenceUpcoming},
		{"bill the day after", SeriesBill, false, "2026-03-11", OccurrencePastDue},
		{"subscription the day after", SeriesSubscription, false, "2026-03-11", OccurrencePastDue},
		{"income never past due", SeriesIncome, false, "2026-04-30", OccurrenceUpcoming},
		{"paid bill", SeriesBill, true, "2026-03-11", OccurrencePaid},
		{"received income", SeriesIncome, true, "2026-03-11", OccurrenceReceived},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, OccurrenceStatusOf(c.kind, c.fulfilled, due, dupDay(c.asOf)))
		})
	}
}
