package store

import (
	"sync"
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

func storedSession(t *testing.T, st *Store, session Session) Session {
	t.Helper()
	stored, err := st.SessionByTokenHash(t.Context(), session.TokenHash)
	require.NoError(t, err)
	return stored
}

func TestAStaleTouchDoesNotMoveASessionBackwards(t *testing.T) {
	st := newSQLiteTest(t)
	user := newUserIn(t, st, false, true)
	started := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	session := newSessionFor(t, st, user.ID, started.Add(time.Hour))
	// A use stale by a minute or more is written; the stored use is started-1h.
	touch := func(usedAt time.Time) bool {
		touched, err := st.TouchSession(t.Context(), session.ID,
			usedAt, usedAt.Add(24*time.Hour), usedAt.Add(-time.Minute))
		require.NoError(t, err)
		return touched
	}

	require.True(t, touch(started.Add(5*time.Minute)))
	// A request that read the session before that write, and finished after it.
	require.False(t, touch(started.Add(2*time.Minute)))

	stored := storedSession(t, st, session)
	require.Equal(t, started.Add(5*time.Minute), stored.LastUsedAt)
	require.Equal(t, started.Add(5*time.Minute+24*time.Hour), stored.ExpiresAt)
}

func TestConcurrentTouchesLeaveTheLatestWrittenUse(t *testing.T) {
	st := newSQLiteTest(t)
	user := newUserIn(t, st, false, true)
	started := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	session := newSessionFor(t, st, user.ID, started.Add(time.Hour))

	// Uses 90 seconds apart, each far enough past the stored one to be written
	// on its own, released together so they land in no particular order.
	const callers = 32
	var (
		ready   sync.WaitGroup
		done    sync.WaitGroup
		release = make(chan struct{})
		touched [callers]bool
		errs    [callers]error
	)
	for i := range callers {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			usedAt := started.Add(time.Duration(i) * 90 * time.Second)
			ready.Done()
			<-release
			touched[i], errs[i] = st.TouchSession(t.Context(), session.ID,
				usedAt, usedAt.Add(24*time.Hour), usedAt.Add(-time.Minute))
		}()
	}
	ready.Wait()
	close(release)
	done.Wait()

	// Every write moved the use forward by at least a minute, so the last
	// one written is the latest of those written.
	var latest time.Time
	for i := range callers {
		require.NoError(t, errs[i])
		if usedAt := started.Add(time.Duration(i) * 90 * time.Second); touched[i] && usedAt.After(latest) {
			latest = usedAt
		}
	}
	require.False(t, latest.IsZero(), "no touch was written")
	stored := storedSession(t, st, session)
	require.Equal(t, latest, stored.LastUsedAt)
	require.Equal(t, latest.Add(24*time.Hour), stored.ExpiresAt)
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
