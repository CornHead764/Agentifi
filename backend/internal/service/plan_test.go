package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// monthFigures is one month's answer as printed money, so two chains compare on
// figures rather than decimal internals.
func monthFigures(view PlanView, month domain.Month) map[string]string {
	computed := view.Results[month]
	out := map[string]string{"left_this_month": computed.LeftThisMonth().String()}
	for _, key := range domain.BucketOrder {
		bucket := computed.Bucket(key)
		out[string(key)] = bucket.CalculatedAmount.String()
		out[string(key)+" posted"] = bucket.PostedAmount.String()
	}
	return out
}

// A month reads the same whichever month is open: a January slot paid on
// February 2 must be paid in January's stored figure either way, or February's
// carry-in moves with the navigation.
func TestAMonthReadsTheSameWhicheverMonthIsOpen(t *testing.T) {
	spaceID := newSpace(t)
	checking := newAccount(t, spaceID, "Checking")
	series := newSeries(t, spaceID, checking,
		seriesNextDueOn(on(2026, time.January, 15)),
		seriesOverrideDueOn(on(2026, time.January, 30)))
	newTransaction(t, spaceID, checking, on(2026, time.February, 2), "-15.00",
		withSeries(series.ID, on(2026, time.January, 30)))

	plan := NewPlan(db(t))
	plan.Now = func() time.Time { return time.Date(2026, time.March, 15, 12, 0, 0, 0, time.UTC) }

	january, february := domain.NewMonth(2026, time.January), domain.NewMonth(2026, time.February)
	// February's chain reaches back to January only once January has a row.
	_, err := plan.OpenMonth(t.Context(), spaceID, january)
	require.NoError(t, err)

	fromJanuary, err := plan.Compute(t.Context(), spaceID, january)
	require.NoError(t, err)
	fromFebruary, err := plan.Compute(t.Context(), spaceID, february)
	require.NoError(t, err)

	require.Equal(t, monthFigures(fromJanuary, january), monthFigures(fromFebruary, january))
	require.Equal(t, fromJanuary.Results[january].Buckets, fromFebruary.Results[january].Buckets)
	require.Equal(t, fromJanuary.Bills[january], fromFebruary.Bills[january])

	// The occurrence is filled, so January does not also charge for it. The
	// money moved in February, which is where it counts, alongside the
	// occurrence February still owes.
	require.Equal(t, "0.00",
		fromJanuary.Results[january].Bucket(domain.BucketBills).CalculatedAmount.String())
	require.Equal(t, "-30.00",
		fromFebruary.Results[february].Bucket(domain.BucketBills).CalculatedAmount.String())

	// January still lists the slot, saying what it cost rather than a zero
	// that reads as nothing due.
	row := fromJanuary.Bills[january][0]
	require.True(t, row.IsFulfilled)
	require.Equal(t, "-15.00", row.Amount.String())
}
