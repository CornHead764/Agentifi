package store_test

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteBillChallenges(t *testing.T) {
	st := storetest.New(t)
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Millisecond)
	create := func(space store.SpaceID, expires time.Time) store.BillChallenge {
		db, err := st.HouseholdDB(ctx, space)
		require.NoError(t, err)
		connection := uuid.New()
		_, err = db.Write().ExecContext(ctx, `INSERT INTO bill_connections (id,biller,label,created_at,updated_at) VALUES (?, 'test', ?, ?, ?)`, connection, connection.String(), db.Now(), db.Now())
		require.NoError(t, err)
		one := store.BillChallenge{ConnectionID: connection, AgentSession: "session", Method: "code", Prompt: "Code", Image: "image", RaisedBy: store.BillChallengeFromPull, ExpiresAt: expires}
		require.NoError(t, st.CreateBillChallenge(ctx, space, &one))
		require.Equal(t, space, one.SpaceID)
		require.Equal(t, expires, one.ExpiresAt)
		return one
	}
	answered := create(a, now.Add(-time.Hour))
	expiredA, expiredB := create(a, now), create(b, now)
	future := create(b, now.Add(time.Hour))
	require.NoError(t, st.MarkBillChallengeAnswered(ctx, a, answered.ID, store.BillChallengeByPerson))
	require.ErrorIs(t, st.MarkBillChallengeAnswered(ctx, a, answered.ID, store.BillChallengeByMailbox), store.ErrNotFound)
	_, err := st.GetBillChallenge(ctx, b, answered.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	n, err := st.ExpireBillChallenges(ctx, now)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	for _, one := range []store.BillChallenge{expiredA, expiredB} {
		got, err := st.GetBillChallenge(ctx, one.SpaceID, one.ID)
		require.NoError(t, err)
		require.Equal(t, store.BillChallengeExpired, got.State)
		require.Nil(t, got.AnsweredAt)
	}
	got, err := st.GetBillChallenge(ctx, a, answered.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillChallengeByPerson, got.AnsweredBy)
	require.NotNil(t, got.AnsweredAt)
	rows, err := st.ListBillChallenges(ctx, b, "")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, future.ID, rows[0].ID)
	require.NoError(t, st.MarkBillChallengeState(ctx, b, future.ID, store.BillChallengeFailed))
	rows, err = st.ListBillChallenges(ctx, b, store.BillChallengeFailed)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}
