package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Tokens issues and verifies the bearer token that identifies a caller.
//
// The token says who, and nothing else — no role, no space — so deactivating an
// account or revoking a membership takes effect on the next request.
type Tokens struct {
	// SecretKey signs and verifies. Rotating it logs everyone out.
	SecretKey string
	// Algorithm is empty for HS256. Only the HMAC family is accepted: the
	// signing key here is a shared secret, and handing a shared secret to an
	// asymmetric verifier is the classic algorithm-confusion forgery.
	Algorithm string
	// Expiry is how long a new token lives. Zero for DefaultTokenExpiry.
	Expiry time.Duration
	// Revoked holds the jti of every signed-out token.
	Revoked *SecretStore
	// Durable persists revocations across a restart. Nil keeps them in memory
	// only, for tests; production always sets it.
	Durable RevocationStore
	// Now is nil for the real clock.
	Now func() time.Time
}

// RevocationStore is where signed-out jtis outlive the process that saw them.
type RevocationStore interface {
	RevokeToken(ctx context.Context, jti string, until time.Time) error
	TokenRevoked(ctx context.Context, jti string) (bool, error)
}

// DefaultTokenExpiry is a one-day session.
const DefaultTokenExpiry = 24 * time.Hour

// Claims is everything a valid token carries. The set is pinned by a test so a
// role or space id cannot creep in.
type Claims struct {
	Subject  uuid.UUID
	IssuedAt time.Time
	Expires  time.Time
	// TokenID is the `jti`: the handle logout revokes by.
	TokenID string
}

// Issue mints a token for the configured lifetime.
func (t *Tokens) Issue(userID uuid.UUID) (string, error) {
	expiry := t.Expiry
	if expiry <= 0 {
		expiry = DefaultTokenExpiry
	}
	return t.IssueFor(userID, expiry)
}

// IssueFor mints a token with an explicit lifetime.
func (t *Tokens) IssueFor(userID uuid.UUID, expiry time.Duration) (string, error) {
	return t.IssueAt(userID, now(t.Now), expiry)
}

// IssueAt mints a token that claims to have been issued at a given moment.
//
// `iat` is a Unix second, so a password change sets the session cutoff to the
// next second and stamps the replacement token with it; otherwise the cutoff
// cannot tell the new token from the ones it ends. Nothing validates `iat`, and
// the lifetime is measured from it.
func (t *Tokens) IssueAt(userID uuid.UUID, issued time.Time, expiry time.Duration) (string, error) {
	method, err := t.method()
	if err != nil {
		return "", err
	}
	jti, err := RandomToken(16)
	if err != nil {
		return "", err
	}

	claims := jwt.MapClaims{
		"sub": userID.String(),
		"iat": issued.Unix(),
		"exp": issued.Add(expiry).Unix(),
		"jti": jti,
	}
	signed, err := jwt.NewWithClaims(method, claims).SignedString([]byte(t.SecretKey))
	if err != nil {
		return "", fmt.Errorf("auth: signing a token: %w", err)
	}
	return signed, nil
}

// Verify checks the signature, the expiry and the shape of a token. Every
// failure returns ErrInvalidCredentials, so an attacker is not told which half
// of a forged token to fix. Revocation is a separate call.
func (t *Tokens) Verify(raw string) (Claims, error) {
	method, err := t.method()
	if err != nil {
		return Claims{}, err
	}

	parsed, err := jwt.Parse(raw,
		func(*jwt.Token) (any, error) { return []byte(t.SecretKey), nil },
		jwt.WithValidMethods([]string{method.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now(t.Now) }),
	)
	if err != nil {
		return Claims{}, ErrInvalidCredentials
	}

	mapped, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return Claims{}, ErrInvalidCredentials
	}
	subject, _ := mapped["sub"].(string)
	userID, err := uuid.Parse(subject)
	if err != nil {
		return Claims{}, ErrInvalidCredentials
	}
	jti, _ := mapped["jti"].(string)

	return Claims{
		Subject:  userID,
		IssuedAt: unixClaim(mapped["iat"]),
		Expires:  unixClaim(mapped["exp"]),
		TokenID:  jti,
	}, nil
}

// Revoke retires one token, keyed on the jti so signing out on one device does
// not sign out everywhere. The mark is kept until the token would have expired,
// and written durably first when a store is attached.
func (t *Tokens) Revoke(ctx context.Context, raw string) error {
	claims, err := t.Verify(raw)
	if err != nil {
		return err
	}
	if claims.TokenID == "" {
		return nil
	}
	remaining := claims.Expires.Sub(now(t.Now))
	if remaining <= 0 {
		return nil
	}
	if t.Durable != nil {
		if err := t.Durable.RevokeToken(ctx, claims.TokenID, claims.Expires); err != nil {
			return fmt.Errorf("auth: persisting the revocation: %w", err)
		}
	}
	t.Revoked.Set(revocationKey(claims.TokenID), true, remaining)
	return nil
}

// IsRevoked reports whether this token has been signed out. A token with no jti
// reads as live. A miss in memory falls through to the durable store, which
// another process or a pre-restart one may have written.
func (t *Tokens) IsRevoked(ctx context.Context, claims Claims) bool {
	if claims.TokenID == "" {
		return false
	}
	if _, revoked := t.Revoked.Get(revocationKey(claims.TokenID)); revoked {
		return true
	}
	if t.Durable == nil {
		return false
	}
	revoked, err := t.Durable.TokenRevoked(ctx, claims.TokenID)
	if err != nil {
		// Fail closed: an unreadable revocation table must not bring signed-out
		// tokens back to life.
		return true
	}
	return revoked
}

// Authenticate is Verify plus the revocation check, so no handler can do one
// and forget the other.
func (t *Tokens) Authenticate(ctx context.Context, raw string) (Claims, error) {
	claims, err := t.Verify(raw)
	if err != nil {
		return Claims{}, err
	}
	if t.IsRevoked(ctx, claims) {
		return Claims{}, ErrInvalidCredentials
	}
	return claims, nil
}

func (t *Tokens) method() (jwt.SigningMethod, error) {
	switch t.Algorithm {
	case "", "HS256":
		return jwt.SigningMethodHS256, nil
	case "HS384":
		return jwt.SigningMethodHS384, nil
	case "HS512":
		return jwt.SigningMethodHS512, nil
	default:
		return nil, fmt.Errorf("auth: %q is not an HMAC signing algorithm; the token key is a shared secret", t.Algorithm)
	}
}

func revocationKey(jti string) string { return "revoked_token:" + jti }

func unixClaim(raw any) time.Time {
	seconds, ok := raw.(float64)
	if !ok {
		return time.Time{}
	}
	return time.Unix(int64(seconds), 0).UTC()
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
