package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Tokens issues the opaque tokens that identify a caller and checks them
// against the sessions table. A token is 32 random bytes and the table holds
// only its SHA-256, so the lookup is the whole check: revoking a row signs the
// token out on its next request.
//
// A session says who, and nothing else — no role, no space — so deactivating an
// account or revoking a membership takes effect on the next request.
type Tokens struct {
	Store SessionStore
	// Lifetimes is how long each kind of session lives unused. A kind missing
	// from it lives DefaultSessionLifetime.
	Lifetimes map[store.SessionKind]time.Duration
	// Now is nil for the real clock.
	Now func() time.Time
}

// SessionStore is where sessions are kept: the sessions table in server.db.
type SessionStore interface {
	CreateSession(ctx context.Context, session store.Session) error
	SessionByTokenHash(ctx context.Context, hash []byte) (store.Session, error)
	TouchSession(ctx context.Context, id uuid.UUID, usedAt, expiresAt time.Time) error
	RevokeSession(ctx context.Context, userID, id uuid.UUID) error
}

// DefaultSessionLifetime is one unused day.
const DefaultSessionLifetime = 24 * time.Hour

// TouchInterval is how often a session in use is written back. Each write
// slides the expiry; between writes a request only reads.
const TouchInterval = time.Minute

// HashToken is the digest a token is stored and looked up by.
func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func (t *Tokens) lifetime(kind store.SessionKind) time.Duration {
	if lifetime := t.Lifetimes[kind]; lifetime > 0 {
		return lifetime
	}
	return DefaultSessionLifetime
}

// Issue starts a session and returns its token, which exists nowhere else
// once the caller has handed it on.
func (t *Tokens) Issue(
	ctx context.Context, userID uuid.UUID, kind store.SessionKind, name string,
) (string, store.Session, error) {
	raw, err := RandomToken(32)
	if err != nil {
		return "", store.Session{}, err
	}
	issued := now(t.Now).UTC()
	session := store.Session{
		ID:         uuid.New(),
		UserID:     userID,
		TokenHash:  HashToken(raw),
		Kind:       kind,
		Name:       name,
		CreatedAt:  issued,
		LastUsedAt: issued,
		ExpiresAt:  issued.Add(t.lifetime(kind)),
	}
	if err := t.Store.CreateSession(ctx, session); err != nil {
		return "", store.Session{}, fmt.Errorf("auth: starting a session: %w", err)
	}
	return raw, session, nil
}

// Authenticate finds the live session a token names and records the use. A
// missing, revoked or expired session is ErrInvalidCredentials, all alike.
// extended reports that this use slid the expiry.
func (t *Tokens) Authenticate(ctx context.Context, raw string) (session store.Session, extended bool, err error) {
	if raw == "" {
		return store.Session{}, false, ErrInvalidCredentials
	}
	session, err = t.Store.SessionByTokenHash(ctx, HashToken(raw))
	if err != nil {
		if isNotFound(err) {
			return store.Session{}, false, ErrInvalidCredentials
		}
		return store.Session{}, false, err
	}
	at := now(t.Now).UTC()
	if session.RevokedAt != nil || !at.Before(session.ExpiresAt) {
		return store.Session{}, false, ErrInvalidCredentials
	}
	if at.Sub(session.LastUsedAt) < TouchInterval {
		return session, false, nil
	}
	expires := at.Add(t.lifetime(session.Kind))
	if err := t.Store.TouchSession(ctx, session.ID, at, expires); err != nil {
		return store.Session{}, false, err
	}
	session.LastUsedAt, session.ExpiresAt = at, expires
	return session, true, nil
}

// Revoke ends one session, so signing out on one device does not sign out
// everywhere.
func (t *Tokens) Revoke(ctx context.Context, session store.Session) error {
	return t.Store.RevokeSession(ctx, session.UserID, session.ID)
}

// PendingLogin is the half-finished login between a correct password and a
// correct second factor. It carries no space and grants nothing.
type PendingLogin struct {
	Store *SecretStore
	// TTL is zero for DefaultPendingLoginTTL.
	TTL time.Duration
}

const DefaultPendingLoginTTL = 5 * time.Minute

// Issue parks a user id behind an opaque handle and returns the handle.
func (p *PendingLogin) Issue(userID uuid.UUID) (string, error) {
	handle, err := RandomToken(32)
	if err != nil {
		return "", err
	}
	ttl := p.TTL
	if ttl <= 0 {
		ttl = DefaultPendingLoginTTL
	}
	p.Store.Set(pendingLoginKey(handle), userID, ttl)
	return handle, nil
}

// User reads the handle without spending it: a typo must not cost the password
// entry. Guessing is bounded by TOTP's per-user attempt counter.
func (p *PendingLogin) User(handle string) (uuid.UUID, error) {
	userID, ok := GetAs[uuid.UUID](p.Store, pendingLoginKey(handle))
	if !ok {
		return uuid.Nil, ErrInvalidCredentials
	}
	return userID, nil
}

// Spend retires the handle. Called once the second factor has verified.
func (p *PendingLogin) Spend(handle string) {
	p.Store.Delete(pendingLoginKey(handle))
}

func pendingLoginKey(handle string) string { return "mfa_pending:" + handle }

// RandomToken returns n bytes of cryptographic randomness, base64url-encoded
// without padding — safe in a URL, a header and a JSON string.
func RandomToken(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: reading randomness: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
