package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The authenticator below signs with a real P-256 key and assembles real
// authenticatorData, so go-webauthn's verification does its actual work: a
// mismatched origin, a stale challenge or a swapped credential fails here for
// the same reason it would fail in a browser. Stubbing the verifier would test
// that the ceremony calls a function.

const (
	testRPID   = "localhost"
	testOrigin = "http://localhost"

	flagUserPresent    = 0x01
	flagUserVerified   = 0x04
	flagBackupEligible = 0x08
	flagBackupState    = 0x10
	flagAttestedData   = 0x40
)

type softAuthenticator struct {
	credentialID []byte
	userHandle   []byte
	key          *ecdsa.PrivateKey
	signCount    uint32
	// noCounter models the authenticators — every synced passkey — that do not
	// implement the signature counter and report zero forever.
	noCounter bool
	// syncs models a passkey provider such as Bitwarden: the credential is
	// backup eligible and backed up, so every assertion carries BE and BS.
	syncs bool
}

func (a *softAuthenticator) flags(base byte) byte {
	if a.syncs {
		base |= flagBackupEligible | flagBackupState
	}
	return base
}

func newSoftAuthenticator(t *testing.T, userID uuid.UUID) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	id := uuid.New()
	return &softAuthenticator{
		credentialID: id[:],
		userHandle:   userHandleFor(userID),
		key:          key,
	}
}

func (a *softAuthenticator) coseKey(t *testing.T) []byte {
	t.Helper()
	// Uncompressed SEC 1: 0x04, then X and Y at 32 bytes each.
	point, err := a.key.PublicKey.Bytes()
	require.NoError(t, err)
	encoded, err := webauthncbor.Marshal(map[int]any{
		1:  2,  // kty: EC2
		3:  -7, // alg: ES256
		-1: 1,  // crv: P-256
		-2: point[1:33],
		-3: point[33:65],
	})
	require.NoError(t, err)
	return encoded
}

func (a *softAuthenticator) authData(t *testing.T, rpID string, flags byte, attested bool) []byte {
	t.Helper()
	hash := sha256.Sum256([]byte(rpID))
	data := append([]byte{}, hash[:]...)
	data = append(data, flags)
	data = binary.BigEndian.AppendUint32(data, a.signCount)
	if attested {
		data = append(data, make([]byte, 16)...) // AAGUID
		data = binary.BigEndian.AppendUint16(data, uint16(len(a.credentialID)))
		data = append(data, a.credentialID...)
		data = append(data, a.coseKey(t)...)
	}
	return data
}

func clientData(t *testing.T, ceremony, challenge, origin string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":        ceremony,
		"challenge":   challenge,
		"origin":      origin,
		"crossOrigin": false,
	})
	require.NoError(t, err)
	return raw
}

func (a *softAuthenticator) register(t *testing.T, challenge, origin, rpID string) []byte {
	t.Helper()
	client := clientData(t, "webauthn.create", challenge, origin)
	attestation, err := webauthncbor.Marshal(map[string]any{
		"fmt":      "none",
		"attStmt":  map[string]any{},
		"authData": a.authData(t, rpID, a.flags(flagUserPresent|flagUserVerified|flagAttestedData), true),
	})
	require.NoError(t, err)

	return mustJSON(t, map[string]any{
		"id":    b64(a.credentialID),
		"rawId": b64(a.credentialID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64(client),
			"attestationObject": b64(attestation),
			"transports":        []string{"internal"},
		},
		"clientExtensionResults": map[string]any{"credProps": map[string]any{"rk": true}},
	})
}

func (a *softAuthenticator) authenticate(t *testing.T, challenge, origin, rpID string) []byte {
	t.Helper()
	if !a.noCounter {
		a.signCount++
	}
	client := clientData(t, "webauthn.get", challenge, origin)
	authData := a.authData(t, rpID, a.flags(flagUserPresent|flagUserVerified), false)

	clientHash := sha256.Sum256(client)
	signed := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, signed[:])
	require.NoError(t, err)

	return mustJSON(t, map[string]any{
		"id":    b64(a.credentialID),
		"rawId": b64(a.credentialID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64(client),
			"authenticatorData": b64(authData),
			"signature":         b64(signature),
			"userHandle":        b64(a.userHandle),
		},
		"clientExtensionResults": map[string]any{},
	})
}

func b64(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
}

func newPasskeys() *Passkeys {
	return &Passkeys{RPName: "Agentifi", Challenges: NewSecretStore(), ChallengeTTL: time.Minute}
}

func localContext() WebAuthnContext {
	return WebAuthnContext{RPID: testRPID, Origin: testOrigin}
}

func enrol(t *testing.T, keys *Passkeys, store PasskeyStore, userID uuid.UUID, device *softAuthenticator) Passkey {
	t.Helper()
	handle, options, err := keys.BeginRegistration(context.Background(), localContext(), userID, "alex@example.test", "YubiKey", store)
	require.NoError(t, err)

	registered, err := keys.FinishRegistration(context.Background(), userID, handle,
		device.register(t, b64(options.Response.Challenge), testOrigin, testRPID), "", store)
	require.NoError(t, err)
	return registered
}

func TestARegisteredPasskeyLogsIn(t *testing.T) {
	keys, store := newPasskeys(), &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)

	registered := enrol(t, keys, store, userID, device)
	require.Equal(t, "YubiKey", registered.Name)
	require.Equal(t, testRPID, registered.RPID)
	require.Equal(t, []string{"internal"}, registered.Transports)
	require.True(t, registered.IsDiscoverable)
	require.Nil(t, registered.LastUsedAt, "not used yet")

	handle, options, err := keys.BeginAuthentication(localContext())
	require.NoError(t, err)
	matched, err := keys.FinishAuthentication(context.Background(), handle,
		device.authenticate(t, b64(options.Response.Challenge), testOrigin, testRPID), store)
	require.NoError(t, err)

	require.Equal(t, userID, matched.UserID)
	require.Equal(t, registered.ID, matched.ID)
	require.NotNil(t, matched.LastUsedAt)
}

func TestASyncedPasskeyFromAPasswordManagerLogsIn(t *testing.T) {
	// A Bitwarden-style provider sets backup-eligible and backed-up on every
	// assertion and never counts. go-webauthn compares the stored
	// backup-eligible flag with the assertion's, so a credential stored
	// without it is refused.
	keys, store := newPasskeys(), &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)
	device.syncs, device.noCounter = true, true

	registered := enrol(t, keys, store, userID, device)
	require.NotNil(t, registered.BackupEligible)
	require.True(t, *registered.BackupEligible)

	for range 2 {
		handle, options, err := keys.BeginAuthentication(localContext())
		require.NoError(t, err)
		matched, err := keys.FinishAuthentication(context.Background(), handle,
			device.authenticate(t, b64(options.Response.Challenge), testOrigin, testRPID), store)
		require.NoError(t, err)
		require.Equal(t, registered.ID, matched.ID)
	}
}

func TestAPasskeyStoredBeforeFlagsWereRecordedLogsInAndLearnsThem(t *testing.T) {
	keys, store := newPasskeys(), &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)
	device.syncs, device.noCounter = true, true
	registered := enrol(t, keys, store, userID, device)
	store.keys[0].BackupEligible = nil

	handle, options, err := keys.BeginAuthentication(localContext())
	require.NoError(t, err)
	_, err = keys.FinishAuthentication(context.Background(), handle,
		device.authenticate(t, b64(options.Response.Challenge), testOrigin, testRPID), store)
	require.NoError(t, err)

	stored, err := store.GetPasskeyByCredential(context.Background(), registered.CredentialID)
	require.NoError(t, err)
	require.NotNil(t, stored.BackupEligible)
	require.True(t, *stored.BackupEligible)

	handle, options, err = keys.BeginAuthentication(localContext())
	require.NoError(t, err)
	device.syncs = false
	_, err = keys.FinishAuthentication(context.Background(), handle,
		device.authenticate(t, b64(options.Response.Challenge), testOrigin, testRPID), store)
	require.ErrorIs(t, err, ErrInvalidPasskey, "backup eligibility cannot change once learned")
}

func TestLoginOptionsNameNoAccount(t *testing.T) {
	// No email goes in, so no credential list can come out. Tailoring the list
	// to an address would make this endpoint answer "does that account exist".
	keys := newPasskeys()
	_, options, err := keys.BeginAuthentication(localContext())
	require.NoError(t, err)
	require.Empty(t, options.Response.AllowedCredentials)
}

func TestAChallengeIsSpentTheFirstTimeItIsUsed(t *testing.T) {
	// Otherwise a captured assertion is replayable until the challenge expires.
	keys, store := newPasskeys(), &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)
	enrol(t, keys, store, userID, device)

	handle, options, err := keys.BeginAuthentication(localContext())
	require.NoError(t, err)
	assertion := device.authenticate(t, b64(options.Response.Challenge), testOrigin, testRPID)

	_, err = keys.FinishAuthentication(context.Background(), handle, assertion, store)
	require.NoError(t, err)

	_, err = keys.FinishAuthentication(context.Background(), handle, assertion, store)
	require.ErrorIs(t, err, ErrInvalidChallenge)
}

func TestAnAssertionForAnotherOriginIsRefused(t *testing.T) {
	keys, store := newPasskeys(), &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)
	enrol(t, keys, store, userID, device)

	handle, options, err := keys.BeginAuthentication(localContext())
	require.NoError(t, err)
	_, err = keys.FinishAuthentication(context.Background(), handle,
		device.authenticate(t, b64(options.Response.Challenge), "https://evil.example", testRPID), store)
	require.ErrorIs(t, err, ErrInvalidPasskey)
}

func TestAnUnknownCredentialIsRefusedTheSameWay(t *testing.T) {
	// And says nothing about whether any account holds a passkey.
	keys, store := newPasskeys(), &MemoryPasskeys{}
	stranger := newSoftAuthenticator(t, uuid.New())

	handle, options, err := keys.BeginAuthentication(localContext())
	require.NoError(t, err)
	_, err = keys.FinishAuthentication(context.Background(), handle,
		stranger.authenticate(t, b64(options.Response.Challenge), testOrigin, testRPID), store)
	require.ErrorIs(t, err, ErrInvalidPasskey)
}

func TestACloneIsRefusedWithTheSameAnswerAsAnythingElse(t *testing.T) {
	// A counter that repeats means two authenticators hold one private key.
	// go-webauthn only sets CloneWarning; the refusal is ours, and it must be
	// indistinguishable from any other passkey failure.
	keys, store := newPasskeys(), &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)
	enrol(t, keys, store, userID, device)

	handle, options, err := keys.BeginAuthentication(localContext())
	require.NoError(t, err)
	_, err = keys.FinishAuthentication(context.Background(), handle,
		device.authenticate(t, b64(options.Response.Challenge), testOrigin, testRPID), store)
	require.NoError(t, err)

	// The clone answers with a counter it has already used.
	device.signCount--
	handle, options, err = keys.BeginAuthentication(localContext())
	require.NoError(t, err)
	_, err = keys.FinishAuthentication(context.Background(), handle,
		device.authenticate(t, b64(options.Response.Challenge), testOrigin, testRPID), store)
	require.ErrorIs(t, err, ErrInvalidPasskey)
}

func TestASyncedPasskeyThatNeverCountsStillLogsIn(t *testing.T) {
	// Synced passkeys report zero forever. Refusing zero-to-zero as a clone
	// would lock out every iCloud Keychain user on their second device.
	keys, store := newPasskeys(), &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)
	enrol(t, keys, store, userID, device)

	device.noCounter = true
	for range 3 {
		handle, options, err := keys.BeginAuthentication(localContext())
		require.NoError(t, err)
		_, err = keys.FinishAuthentication(context.Background(), handle,
			device.authenticate(t, b64(options.Response.Challenge), testOrigin, testRPID), store)
		require.NoError(t, err)
	}
}

func TestTheSignCounterMustMoveForward(t *testing.T) {
	require.True(t, counterAdvanced(5, 6))
	require.False(t, counterAdvanced(5, 5))
	require.False(t, counterAdvanced(5, 4))
}

func TestAnAuthenticatorThatDoesNotCountIsNotAClone(t *testing.T) {
	require.True(t, counterAdvanced(0, 0))
	require.True(t, counterAdvanced(0, 1))
}

func TestASecondRegistrationExcludesTheFirst(t *testing.T) {
	// Without excludeCredentials the second registration succeeds and silently
	// orphans the first.
	keys, store := newPasskeys(), &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)
	enrol(t, keys, store, userID, device)

	_, options, err := keys.BeginRegistration(context.Background(), localContext(), userID, "alex@example.test", "", store)
	require.NoError(t, err)
	require.Len(t, options.Response.CredentialExcludeList, 1)
	require.Equal(t, device.credentialID, []byte(options.Response.CredentialExcludeList[0].CredentialID))
}

func TestTheSameCredentialCannotBeRegisteredTwice(t *testing.T) {
	keys, store := newPasskeys(), &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)
	enrol(t, keys, store, userID, device)

	handle, options, err := keys.BeginRegistration(context.Background(), localContext(), userID, "alex@example.test", "", store)
	require.NoError(t, err)
	_, err = keys.FinishRegistration(context.Background(), userID, handle,
		device.register(t, b64(options.Response.Challenge), testOrigin, testRPID), "", store)
	require.ErrorIs(t, err, ErrPasskeyRegistered)
}

func TestARegistrationChallengeBelongsToOneUser(t *testing.T) {
	keys, store := newPasskeys(), &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)

	handle, options, err := keys.BeginRegistration(context.Background(), localContext(), userID, "alex@example.test", "", store)
	require.NoError(t, err)

	_, err = keys.FinishRegistration(context.Background(), uuid.New(), handle,
		device.register(t, b64(options.Response.Challenge), testOrigin, testRPID), "", store)
	require.ErrorIs(t, err, ErrInvalidChallenge)
}

func TestAnExpiredChallengeIsRefused(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	challenges := NewSecretStore()
	challenges.Now = clock.now
	keys := &Passkeys{RPName: "Agentifi", Challenges: challenges, ChallengeTTL: time.Minute, Now: clock.now}
	store := &MemoryPasskeys{}
	userID := uuid.New()
	device := newSoftAuthenticator(t, userID)

	handle, options, err := keys.BeginRegistration(context.Background(), localContext(), userID, "alex@example.test", "", store)
	require.NoError(t, err)

	clock.advance(2 * time.Minute)
	_, err = keys.FinishRegistration(context.Background(), userID, handle,
		device.register(t, b64(options.Response.Challenge), testOrigin, testRPID), "", store)
	require.ErrorIs(t, err, ErrInvalidChallenge)
}

func TestSomeoneElsesPasskeyIdIsNotAHandleToIt(t *testing.T) {
	store := &MemoryPasskeys{}
	keys := newPasskeys()
	mine := uuid.New()
	registered := enrol(t, keys, store, mine, newSoftAuthenticator(t, mine))

	removed, err := store.DeletePasskey(context.Background(), uuid.New(), registered.ID)
	require.NoError(t, err)
	require.False(t, removed)

	removed, err = store.DeletePasskey(context.Background(), mine, registered.ID)
	require.NoError(t, err)
	require.True(t, removed)
}

// --- Relying-party resolution -------------------------------------------------
//
// Pure string work, and where a self-hosted install most often ends up with
// passkeys that "just don't work".

func TestTheRelyingPartyFollowsTheBrowsersOrigin(t *testing.T) {
	keys := newPasskeys()
	resolved, err := keys.ResolveContext("https://money.example.com")
	require.NoError(t, err)
	require.Equal(t, WebAuthnContext{RPID: "money.example.com", Origin: "https://money.example.com"}, resolved)
}

func TestLocalhostOverPlainHTTPIsAllowedAndNothingElseIs(t *testing.T) {
	keys := newPasskeys()
	resolved, err := keys.ResolveContext("http://localhost:5173")
	require.NoError(t, err)
	require.Equal(t, "localhost", resolved.RPID)

	_, err = keys.ResolveContext("http://money.example.com")
	requireOriginCode(t, err, OriginInsecure)
}

func TestAnIPAddressIsRefusedWithAReasonTheUICanShow(t *testing.T) {
	// Browsers reject an RP ID that is an IP literal; saying so beats a
	// SecurityError the user cannot act on.
	keys := newPasskeys()
	_, err := keys.ResolveContext("https://192.168.1.10")
	requireOriginCode(t, err, OriginIP)

	_, err = keys.ResolveContext("https://[::1]:8000")
	requireOriginCode(t, err, OriginIP)
}

func TestAPinnedRPIDAcceptsItsSubdomainsAndRefusesTheRest(t *testing.T) {
	keys := newPasskeys()
	keys.RPID = "example.com"

	resolved, err := keys.ResolveContext("https://app.example.com")
	require.NoError(t, err)
	require.Equal(t, "example.com", resolved.RPID)

	_, err = keys.ResolveContext("https://example.com.evil.test")
	requireOriginCode(t, err, OriginMismatch)
}

func TestAMalformedOriginIsAnErrorNotAGuess(t *testing.T) {
	keys := newPasskeys()
	for _, origin := range []string{"money.example.com", "https://", "https://[::1", "https://host:port"} {
		_, err := keys.ResolveContext(origin)
		requireOriginCode(t, err, OriginInvalid)
	}
}

func TestAProxyThatDropsTheOriginFallsBackToTheForwardedHost(t *testing.T) {
	request := httptest.NewRequest("POST", "/auth/passkeys/authenticate/options", nil)
	request.Header.Set("X-Forwarded-Host", "money.example.com")
	request.Header.Set("X-Forwarded-Proto", "https")

	resolved, err := newPasskeys().ContextForRequest(request)
	require.NoError(t, err)
	require.Equal(t, "money.example.com", resolved.RPID)
}

func TestNoOriginAtAllIsAnErrorNotAGuess(t *testing.T) {
	request := httptest.NewRequest("POST", "/auth/passkeys/authenticate/options", nil)
	request.Host = ""

	_, err := newPasskeys().ContextForRequest(request)
	requireOriginCode(t, err, OriginMissing)
}

func requireOriginCode(t *testing.T, err error, code string) {
	t.Helper()
	var originErr *OriginError
	require.ErrorAs(t, err, &originErr)
	require.Equal(t, code, originErr.Code)
}
