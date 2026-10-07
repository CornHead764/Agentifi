package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newTokens() *Tokens {
	return &Tokens{SecretKey: "test-signing-key", Expiry: time.Hour, Revoked: NewSecretStore()}
}

func TestTheTokenCarriesIdentityAndNothingElse(t *testing.T) {
	// No role, no space, no membership. Authorization is resolved per request,
	// so a claim added here would keep granting access after the membership
	// behind it was revoked. This test exists to fail when someone adds one.
	tokens := newTokens()
	raw, err := tokens.Issue(uuid.New())
	require.NoError(t, err)

	require.Equal(t, []string{"exp", "iat", "jti", "sub"}, claimNames(t, raw))
}

func TestAValidTokenRoundTrips(t *testing.T) {
	tokens := newTokens()
	userID := uuid.New()

	raw, err := tokens.Issue(userID)
	require.NoError(t, err)
	claims, err := tokens.Verify(raw)
	require.NoError(t, err)

	require.Equal(t, userID, claims.Subject)
	require.NotEmpty(t, claims.TokenID)
	require.WithinDuration(t, time.Now().Add(time.Hour), claims.Expires, time.Minute)
}

func TestEveryWayATokenCanBeWrongIsTheSameRefusal(t *testing.T) {
	tokens := newTokens()
	raw, err := tokens.Issue(uuid.New())
	require.NoError(t, err)

	other := &Tokens{SecretKey: "a different key", Expiry: time.Hour, Revoked: NewSecretStore()}
	otherKey, err := other.Issue(uuid.New())
	require.NoError(t, err)

	expired := &Tokens{
		SecretKey: tokens.SecretKey,
		Revoked:   NewSecretStore(),
		Now:       func() time.Time { return time.Now().Add(-2 * time.Hour) },
	}
	stale, err := expired.IssueFor(uuid.New(), time.Minute)
	require.NoError(t, err)

	for name, candidate := range map[string]string{
		"garbage":          "not a token",
		"wrong signature":  raw[:len(raw)-4] + "AAAA",
		"another key":      otherKey,
		"expired":          stale,
		"alg none forgery": unsignedToken(t, uuid.New()),
		"empty":            "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tokens.Verify(candidate)
			require.ErrorIs(t, err, ErrInvalidCredentials)
		})
	}
}

func TestRevocationRetiresOneTokenAndNotTheUser(t *testing.T) {
	// Signing out on one device must not sign the user out everywhere.
	tokens := newTokens()
	userID := uuid.New()

	phone, err := tokens.Issue(userID)
	require.NoError(t, err)
	laptop, err := tokens.Issue(userID)
	require.NoError(t, err)

	require.NoError(t, tokens.Revoke(context.Background(), phone))

	_, err = tokens.Authenticate(context.Background(), phone)
	require.ErrorIs(t, err, ErrInvalidCredentials)
	_, err = tokens.Authenticate(context.Background(), laptop)
	require.NoError(t, err)
}

func TestARevocationIsKeptOnlyAsLongAsTheTokenWouldHaveLived(t *testing.T) {
	// Holding it longer buys nothing; holding it for less would let a
	// signed-out token come back to life.
	clock := &fakeClock{at: time.Now()}
	revoked := NewSecretStore()
	revoked.Now = clock.now
	tokens := &Tokens{SecretKey: "test-signing-key", Revoked: revoked, Now: clock.now}

	raw, err := tokens.IssueFor(uuid.New(), 10*time.Minute)
	require.NoError(t, err)
	require.NoError(t, tokens.Revoke(context.Background(), raw))

	claims, err := tokens.Verify(raw)
	require.NoError(t, err)
	require.True(t, tokens.IsRevoked(context.Background(), claims))

	clock.advance(11 * time.Minute)
	require.False(t, tokens.IsRevoked(context.Background(), claims), "the mark outlived the token it retired")
	_, err = tokens.Verify(raw)
	require.ErrorIs(t, err, ErrInvalidCredentials, "and the token itself has expired anyway")
}

func TestAnAsymmetricAlgorithmIsRefusedOutright(t *testing.T) {
	// The signing key here is a shared secret. Handing a shared secret to an
	// asymmetric verifier is the classic algorithm-confusion forgery, so the
	// configuration is refused rather than honoured.
	tokens := &Tokens{SecretKey: "k", Algorithm: "RS256", Revoked: NewSecretStore()}
	_, err := tokens.Issue(uuid.New())
	require.Error(t, err)
	require.Contains(t, err.Error(), "HMAC")
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
	tokens := newTokens()
	pending := &PendingLogin{Store: NewSecretStore()}
	handle, err := pending.Issue(uuid.New())
	require.NoError(t, err)

	_, err = tokens.Verify(handle)
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

// claimNames reads the claim names out of the payload segment directly, rather
// than through the struct, so a claim nobody parses still shows up here.
func claimNames(t *testing.T, raw string) []string {
	t.Helper()
	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3)

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	var claims map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &claims))

	names := make([]string, 0, len(claims))
	for name := range claims {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// unsignedToken forges a token whose header says the signature is not needed.
func unsignedToken(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	header := encode(map[string]string{"alg": "none", "typ": "JWT"})
	payload := encode(map[string]any{
		"sub": userID.String(),
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
		"jti": "forged",
	})
	return header + "." + payload + "."
}

type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time          { return c.at }
func (c *fakeClock) advance(d time.Duration) { c.at = c.at.Add(d) }
