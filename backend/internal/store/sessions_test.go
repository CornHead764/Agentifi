package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newSessionFor(t *testing.T, st *Store, userID uuid.UUID, expires time.Time) Session {
	t.Helper()
	session := Session{
		ID: uuid.New(), UserID: userID, TokenHash: []byte(uuid.NewString()),
		Kind: SessionWeb, Name: "Firefox on Linux",
		CreatedAt: expires.Add(-time.Hour), LastUsedAt: expires.Add(-time.Hour), ExpiresAt: expires,
	}
	require.NoError(t, st.CreateSession(t.Context(), session))
	return session
}

func sessionIDs(t *testing.T, st *Store, userID uuid.UUID, now time.Time) []uuid.UUID {
	t.Helper()
	sessions, err := st.ListSessions(t.Context(), userID, now)
	require.NoError(t, err)
	ids := []uuid.UUID{}
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	return ids
}

func TestTheSessionListLeavesOutRevokedAndExpiredSessions(t *testing.T) {
	st := newSQLiteTest(t)
	user := newUserIn(t, st, false, true)
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	live := newSessionFor(t, st, user.ID, now.Add(time.Hour))
	revoked := newSessionFor(t, st, user.ID, now.Add(time.Hour))
	newSessionFor(t, st, user.ID, now.Add(-time.Minute))
	require.NoError(t, st.RevokeSession(t.Context(), user.ID, revoked.ID))

	require.Equal(t, []uuid.UUID{live.ID}, sessionIDs(t, st, user.ID, now))
}

func TestOnlyTheOwnerCanRevokeASession(t *testing.T) {
	st := newSQLiteTest(t)
	owner := newUserIn(t, st, false, true)
	other := newUserIn(t, st, false, true)
	now := time.Now()
	session := newSessionFor(t, st, owner.ID, now.Add(time.Hour))

	require.ErrorIs(t, st.RevokeSession(t.Context(), other.ID, session.ID), ErrNotFound)
	require.Equal(t, []uuid.UUID{session.ID}, sessionIDs(t, st, owner.ID, now))
}

func TestAPasswordChangeEndsEverySessionButTheOneKept(t *testing.T) {
	st := newSQLiteTest(t)
	user := newUserIn(t, st, false, true)
	now := time.Now()
	kept := newSessionFor(t, st, user.ID, now.Add(time.Hour))
	newSessionFor(t, st, user.ID, now.Add(time.Hour))

	require.NoError(t, st.SetUserPassword(t.Context(), user.ID, "the-hash", false, kept.ID))
	require.Equal(t, []uuid.UUID{kept.ID}, sessionIDs(t, st, user.ID, now))

	require.NoError(t, st.SetUserPassword(t.Context(), user.ID, "another-hash", true, uuid.Nil))
	require.Empty(t, sessionIDs(t, st, user.ID, now))
}

func TestDeactivatingAnAccountEndsItsSessions(t *testing.T) {
	st := newSQLiteTest(t)
	user := newUserIn(t, st, false, true)
	now := time.Now()
	newSessionFor(t, st, user.ID, now.Add(time.Hour))

	require.NoError(t, st.SetUserActive(t.Context(), user.ID, false))
	require.NoError(t, st.SetUserActive(t.Context(), user.ID, true))
	require.Empty(t, sessionIDs(t, st, user.ID, now))
}

func TestPruningKeepsSessionsThatAreStillLive(t *testing.T) {
	st := newSQLiteTest(t)
	user := newUserIn(t, st, false, true)
	now := time.Now()
	live := newSessionFor(t, st, user.ID, now.Add(time.Hour))
	expired := newSessionFor(t, st, user.ID, now.Add(-time.Hour))
	revoked := newSessionFor(t, st, user.ID, now.Add(time.Hour))
	require.NoError(t, st.RevokeSession(t.Context(), user.ID, revoked.ID))

	pruned, err := st.PruneSessions(t.Context(), now.Add(time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 2, pruned)

	_, err = st.SessionByTokenHash(t.Context(), live.TokenHash)
	require.NoError(t, err)
	_, err = st.SessionByTokenHash(t.Context(), expired.TokenHash)
	require.ErrorIs(t, err, ErrNotFound)
}
