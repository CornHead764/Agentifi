package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// newTokens is Tokens over a real server.db in dir, with web sessions that
// live ten unused minutes, and one account to issue them to.
func newTokens(t *testing.T, clock *fakeClock) (*Tokens, string, uuid.UUID) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.OpenSQLite(t.Context(), dir, time.Now)
	require.NoError(t, err)
	t.Cleanup(st.Close)
	user := &store.User{Email: uuid.NewString() + "@example.test", IsActive: true}
	require.NoError(t, st.CreateUser(t.Context(), user))
	tokens := &Tokens{
		Store:     st,
		Lifetimes: map[store.SessionKind]time.Duration{store.SessionWeb: 10 * time.Minute},
		Now:       clock.now,
	}
	return tokens, dir, user.ID
}

func TestTheRawTokenIsNeverStored(t *testing.T) {
	// Read from the files on disk, so no column or index can hold a copy.
	tokens, dir, user := newTokens(t, &fakeClock{at: time.Now()})
	raw, _, err := tokens.Issue(t.Context(), user, store.SessionWeb, "Firefox on Linux")
	require.NoError(t, err)

	var written []byte
	for _, name := range []string{"server.db", "server.db-wal"} {
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		written = append(written, contents...)
	}
	require.True(t, bytes.Contains(written, HashToken(raw)), "the digest is what is kept")
	require.False(t, bytes.Contains(written, []byte(raw)), "the token itself was written")
}

func TestUseSlidesTheExpiry(t *testing.T) {
	clock := &fakeClock{at: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	tokens, _, user := newTokens(t, clock)
	raw, _, err := tokens.Issue(t.Context(), user, store.SessionWeb, "")
	require.NoError(t, err)

	// Each use inside the ten minutes buys ten more from that use.
	for range 3 {
		clock.advance(9 * time.Minute)
		_, _, err := tokens.Authenticate(t.Context(), raw)
		require.NoError(t, err)
	}
	clock.advance(10 * time.Minute)
	_, _, err = tokens.Authenticate(t.Context(), raw)
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAUseIsWrittenAtMostOnceAMinute(t *testing.T) {
	started := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{at: started}
	tokens, _, user := newTokens(t, clock)
	raw, _, err := tokens.Issue(t.Context(), user, store.SessionWeb, "")
	require.NoError(t, err)
	stored := func() store.Session {
		session, err := tokens.Store.SessionByTokenHash(t.Context(), HashToken(raw))
		require.NoError(t, err)
		return session
	}

	clock.advance(59 * time.Second)
	_, extended, err := tokens.Authenticate(t.Context(), raw)
	require.NoError(t, err)
	require.False(t, extended)
	require.Equal(t, started, stored().LastUsedAt)

	clock.advance(time.Second)
	_, extended, err = tokens.Authenticate(t.Context(), raw)
	require.NoError(t, err)
	require.True(t, extended)
	require.Equal(t, started.Add(time.Minute), stored().LastUsedAt)
	require.Equal(t, started.Add(11*time.Minute), stored().ExpiresAt)
}

func TestRevocationRetiresOneSessionOnItsNextRequest(t *testing.T) {
	// Signing out on one device must not sign the user out everywhere.
	tokens, _, user := newTokens(t, &fakeClock{at: time.Now()})
	phone, phoneSession, err := tokens.Issue(t.Context(), user, store.SessionWeb, "")
	require.NoError(t, err)
	laptop, _, err := tokens.Issue(t.Context(), user, store.SessionWeb, "")
	require.NoError(t, err)

	require.NoError(t, tokens.Revoke(t.Context(), phoneSession))

	_, _, err = tokens.Authenticate(t.Context(), phone)
	require.ErrorIs(t, err, ErrInvalidCredentials)
	_, _, err = tokens.Authenticate(t.Context(), laptop)
	require.NoError(t, err)
}

func TestEveryWayATokenCanBeWrongIsTheSameRefusal(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	tokens, _, user := newTokens(t, clock)
	issue := func() (string, store.Session) {
		raw, session, err := tokens.Issue(t.Context(), user, store.SessionWeb, "")
		require.NoError(t, err)
		return raw, session
	}
	live, _ := issue()
	revoked, session := issue()
	require.NoError(t, tokens.Revoke(t.Context(), session))
	clock.advance(-time.Hour)
	expired, _ := issue()
	clock.advance(time.Hour)

	for name, candidate := range map[string]string{
		"garbage":   "not a token",
		"truncated": live[:len(live)-1],
		"revoked":   revoked,
		"expired":   expired,
		"empty":     "",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := tokens.Authenticate(t.Context(), candidate)
			require.ErrorIs(t, err, ErrInvalidCredentials)
		})
	}
}

func TestAPendingLoginIsNotASession(t *testing.T) {
	pending := &PendingLogin{Store: NewSecretStore(), TTL: time.Minute}
	userID := uuid.New()

	handle, err := pending.Issue(userID)
	require.NoError(t, err)

	// A wrong code must leave the challenge alive: a typo should not cost the
	// user their password entry.
	got, err := pending.User(handle)
	require.NoError(t, err)
	require.Equal(t, userID, got)
	got, err = pending.User(handle)
	require.NoError(t, err)
	require.Equal(t, userID, got)

	pending.Spend(handle)
	_, err = pending.User(handle)
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAPendingLoginHandleIsNotAToken(t *testing.T) {
	// It must not verify as a bearer token: it grants nothing but the
	// second-factor exchange.
	tokens, _, user := newTokens(t, &fakeClock{at: time.Now()})
	pending := &PendingLogin{Store: NewSecretStore()}
	handle, err := pending.Issue(user)
	require.NoError(t, err)

	_, _, err = tokens.Authenticate(t.Context(), handle)
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time          { return c.at }
func (c *fakeClock) advance(d time.Duration) { c.at = c.at.Add(d) }
