package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The review queue over the seeded ledger. Expected values come from
// calculations.md §15, not from running the gather.

func gatherFor(t *testing.T, l *ledger) reviewSnapshot {
	t.Helper()
	l.env.Now = func() time.Time { return time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC) }
	sp, err := auth.ResolveSpace(t.Context(), l.env.DB, l.user("alex"), l.str("space"))
	require.NoError(t, err)
	snapshot, err := gatherReview(t.Context(), l.env, sp)
	require.NoError(t, err)
	return snapshot
}

func rowsByID(snapshot reviewSnapshot) map[string]reviewRow {
	out := map[string]reviewRow{}
	for _, day := range snapshot.Days {
		for _, row := range day.Rows {
			out[row.Txn.ID.String()] = row
		}
	}
	return out
}

func dayOn(t *testing.T, snapshot reviewSnapshot, on domain.Date) reviewDay {
	t.Helper()
	for _, day := range snapshot.Days {
		if day.Date == on {
			return day
		}
	}
	t.Fatalf("no review day on %s", on)
	return reviewDay{}
}

func TestReviewRowsLane(t *testing.T) {
	l := buildLedger(t)
	snapshot := gatherFor(t, l)
	rows := rowsByID(snapshot)

	require.NotContains(t, rows, l.str("august_corner"), "a reviewed row is not in the lane")
	require.Equal(t, domain.ReviewReason(""), rows[l.str("august_groceries")].Reason)
	require.Equal(t, domain.ReviewNoCategory, rows[l.str("july")].Reason)

	// Trap 4: the card charge is filed under its effective date.
	september := dayOn(t, snapshot, domain.NewDate(2026, time.September, 10))
	require.Len(t, september.Rows, 1)
	require.Equal(t, l.str("card_charge"), september.Rows[0].Txn.ID.String())
	require.Equal(t, 1, september.NeedsALook)

	for i := 1; i < len(snapshot.Days); i++ {
		require.True(t, snapshot.Days[i-1].Date.After(snapshot.Days[i].Date), "days run newest first")
	}

	august5 := dayOn(t, snapshot, domain.NewDate(2026, time.August, 5))
	require.Equal(t, "50.00", august5.Out.String())
	require.Equal(t, len(rows), snapshot.Summary.New)
}

func TestReviewUnusualAmount(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	// Three reviewed visits at 20.00 make 20.00 the usual amount, so the
	// 50.00 visit is at least twice it.
	for _, day := range []int{1, 2, 3} {
		require.NoError(t, l.env.DB.CreateTransaction(t.Context(), space, &store.Transaction{
			AccountID: l.id("checking"), Date: domain.NewDate(2026, time.July, day),
			Amount: domain.MustFromString("-20.00"), Currency: "USD",
			StatementName: "Safeway #1234 Springfield ZZ ", Payee: "Safeway",
			CategoryID: l.id("groceries"), IsReviewed: true, Source: domain.SourceSync,
		}))
	}
	rows := rowsByID(gatherFor(t, l))
	require.Equal(t, domain.ReviewUnusualAmount, rows[l.str("august_groceries")].Reason)
}

func TestReviewHeldBalanceItem(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	account, err := l.env.DB.GetAccount(t.Context(), space, l.id("checking"))
	require.NoError(t, err)
	seen := time.Date(2026, time.September, 14, 9, 0, 0, 0, time.UTC)
	account.HasWithheldBalance, account.WithheldBalance, account.WithheldBalanceAt = true, domain.MustFromString("0.00"), &seen
	require.NoError(t, l.env.DB.UpdateAccount(t.Context(), space, &account))

	snapshot := gatherFor(t, l)
	require.Len(t, snapshot.Items, 1)
	item := snapshot.Items[0]
	require.Equal(t, "held:"+l.str("checking"), item.Key)
	require.Equal(t, domain.ReviewMoneyAtStake, item.Kind.Tier())
	require.Equal(t, 1, snapshot.Summary.MoneyAtStake)
	require.Equal(t, snapshot.Summary.MoneyAtStake+snapshot.Summary.NeedsALook, snapshot.Summary.Badge())
}
