package api

import (
	"testing"
	"time"

	"github.com/google/uuid"
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

func memberOf(t *testing.T, l *ledger, user string) auth.SpaceContext {
	t.Helper()
	sp, err := auth.ResolveSpace(t.Context(), l.env.DB, l.user(user), l.str("space"))
	require.NoError(t, err)
	return sp
}

func txnOf(t *testing.T, l *ledger, key string) store.Transaction {
	t.Helper()
	row, err := l.env.DB.GetTransaction(t.Context(), store.SpaceIDOf(l.id("space")), l.id(key))
	require.NoError(t, err)
	return row
}

func TestReviewApproveAndUndo(t *testing.T) {
	l := buildLedger(t)
	gatherFor(t, l)
	sp := memberOf(t, l, "alex")

	done, err := approveReviewRows(t.Context(), l.env, sp, []uuid.UUID{l.id("august_groceries"), l.id("july")}, false)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{l.id("august_groceries")}, done.Approved)
	// A row with no category is filed, not ticked.
	require.Len(t, done.Skipped, 1)
	require.Equal(t, l.id("july"), done.Skipped[0].Txn.ID)
	require.True(t, txnOf(t, l, "august_groceries").IsReviewed)
	require.False(t, txnOf(t, l, "july").IsReviewed)

	restored, changed, err := undoReview(t.Context(), l.env, sp, done.Undo)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{l.id("august_groceries")}, restored)
	require.Empty(t, changed)
	require.False(t, txnOf(t, l, "august_groceries").IsReviewed)
}

func TestReviewFileAndUndo(t *testing.T) {
	l := buildLedger(t)
	gatherFor(t, l)
	sp := memberOf(t, l, "alex")

	token, err := fileReviewRow(t.Context(), l.env, sp, l.id("july"), l.id("groceries"))
	require.NoError(t, err)
	filed := txnOf(t, l, "july")
	require.Equal(t, l.id("groceries"), filed.CategoryID)
	require.True(t, filed.IsReviewed)

	restored, _, err := undoReview(t.Context(), l.env, sp, token)
	require.NoError(t, err)
	require.Len(t, restored, 1)
	back := txnOf(t, l, "july")
	require.Equal(t, uuid.Nil, back.CategoryID)
	require.False(t, back.IsReviewed)
}

func TestReviewUndoLeavesAChangedRow(t *testing.T) {
	l := buildLedger(t)
	gatherFor(t, l)
	sp := memberOf(t, l, "alex")

	token, err := fileReviewRow(t.Context(), l.env, sp, l.id("july"), l.id("groceries"))
	require.NoError(t, err)
	l.alex.patch("/transactions/"+l.str("july"), map[string]any{"category_id": l.str("food")}).requireStatus(200)

	restored, changed, err := undoReview(t.Context(), l.env, sp, token)
	require.NoError(t, err)
	require.Empty(t, restored)
	require.Equal(t, []uuid.UUID{l.id("july")}, changed)
	require.Equal(t, l.id("food"), txnOf(t, l, "july").CategoryID)
}

func TestReviewUndoRefusesATamperedToken(t *testing.T) {
	l := buildLedger(t)
	gatherFor(t, l)
	sp := memberOf(t, l, "alex")
	token, err := fileReviewRow(t.Context(), l.env, sp, l.id("july"), l.id("groceries"))
	require.NoError(t, err)

	_, _, err = undoReview(t.Context(), l.env, sp, "x"+token)
	require.Error(t, err)
}

func TestReviewWritesRefuseAViewer(t *testing.T) {
	l := buildLedger(t)
	gatherFor(t, l)
	_, err := approveReviewRows(t.Context(), l.env, memberOf(t, l, "vera"), []uuid.UUID{l.id("august_groceries")}, false)
	require.ErrorIs(t, err, auth.ErrReadOnly)
	require.False(t, txnOf(t, l, "august_groceries").IsReviewed)
}

func TestReviewActAcceptsAHeldBalance(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	account, err := l.env.DB.GetAccount(t.Context(), space, l.id("checking"))
	require.NoError(t, err)
	seen := time.Date(2026, time.September, 14, 9, 0, 0, 0, time.UTC)
	account.HasWithheldBalance, account.WithheldBalance, account.WithheldBalanceAt = true, domain.MustFromString("0.00"), &seen
	require.NoError(t, l.env.DB.UpdateAccount(t.Context(), space, &account))
	gatherFor(t, l)
	sp := memberOf(t, l, "alex")
	key := "held:" + l.str("checking")

	require.NoError(t, actOnReviewItem(t.Context(), l.env, sp, key, "accept_balance", ""))
	after, err := l.env.DB.GetAccount(t.Context(), space, l.id("checking"))
	require.NoError(t, err)
	require.False(t, after.HasWithheldBalance)

	// Decided, the item is gone, so deciding it again finds nothing.
	require.True(t, isNotFound(actOnReviewItem(t.Context(), l.env, sp, key, "accept_balance", "")))
}
