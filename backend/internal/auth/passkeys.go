package auth

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// Passkeys: the WebAuthn ceremonies. Credentials are rows, through
// PasskeyStore. Challenges stay in a SecretStore and are spent by Take the
// first time they are looked at, so a captured assertion cannot be replayed.

// Passkey is one registered credential. CredentialID and PublicKey are raw
// bytes; base64url appears only at the edges.
type Passkey struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	CredentialID []byte
	PublicKey    []byte
	// SignCount is the authenticator's own counter. See counterAdvanced.
	SignCount uint32
	Name      string
	// Transports is what the browser said the authenticator speaks: "internal",
	// "hybrid", "usb". Stored so a later ceremony can hint at the right one.
	Transports []string
	// RPID is the relying party this credential was registered against. A
	// credential is only valid for the domain that minted it.
	RPID           string
	IsDiscoverable bool
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}

// PasskeyStore is persistence for registered credentials.
//
// Delete is scoped to the owner rather than checked after the row is fetched:
// a passkey id travels in URLs, and "load it, then compare the user" is one
// early return away from letting anyone revoke anyone's credential.
type PasskeyStore interface {
	ListPasskeys(ctx context.Context, userID uuid.UUID) ([]Passkey, error)
	// GetPasskeyByCredential returns ErrNotFound when no such credential
	// exists. It takes no user id: at login time there is no user yet.
	GetPasskeyByCredential(ctx context.Context, credentialID []byte) (Passkey, error)
	AddPasskey(ctx context.Context, key Passkey) error
	UpdatePasskeyUse(ctx context.Context, id uuid.UUID, signCount uint32, usedAt time.Time) error
	DeletePasskey(ctx context.Context, userID, id uuid.UUID) (bool, error)
}

// Passkeys runs the ceremonies for one instance.
type Passkeys struct {
	// RPName is the name the browser shows in its prompt.
	RPName string
	// RPID and Origin empty mean "follow the browser's origin", which is what
	// most self-hosted deployments want. See origin.go.
	RPID   string
	Origin string
	// ChallengeTTL is zero for DefaultChallengeTTL.
	ChallengeTTL time.Duration
	// Challenges holds the in-flight ceremonies. Never given to the client.
	Challenges *SecretStore
	Now        func() time.Time
}

const DefaultChallengeTTL = 5 * time.Minute

const (
	registerChallengePrefix = "passkey_register:"
	loginChallengePrefix    = "passkey_authenticate:"
)

// passkeyChallenge is what one in-flight ceremony needs to finish. The relying
// party is stored with it so a request from a different origin cannot finish
// a ceremony another origin started.
type passkeyChallenge struct {
	session webauthn.SessionData
	context WebAuthnContext
	userID  uuid.UUID
	name    string
}

// BeginRegistration returns the options for enrolling a new credential, and
// the handle the caller passes back to FinishRegistration. Existing credentials
// go out in excludeCredentials, or a second enrolment silently orphans the
// first.
func (p *Passkeys) BeginRegistration(ctx context.Context, wctx WebAuthnContext, userID uuid.UUID, email, name string, keys PasskeyStore) (string, *protocol.CredentialCreation, error) {
	engine, err := p.engine(wctx)
	if err != nil {
		return "", nil, err
	}
	existing, err := keys.ListPasskeys(ctx, userID)
	if err != nil {
		return "", nil, err
	}

	exclusions := make([]protocol.CredentialDescriptor, 0, len(existing))
	for _, key := range existing {
		exclusions = append(exclusions, protocol.CredentialDescriptor{
			Type:         protocol.PublicKeyCredentialType,
			CredentialID: key.CredentialID,
			Transport:    transportsOf(key.Transports),
		})
	}

	creation, session, err := engine.BeginRegistration(
		newCeremonyUser(userID, email, nil),
		webauthn.WithExclusions(exclusions),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			// Preferred, not required: a security key that cannot hold a
			// resident credential should still be enrollable as a second
			// factor, and the login ceremony asks for discoverable ones.
			ResidentKey: protocol.ResidentKeyRequirementPreferred,
			// Required: a passkey that proves possession but not the person is
			// not a first factor, and this application logs people in with it.
			UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return "", nil, fmt.Errorf("auth: beginning passkey registration: %w", err)
	}

	handle, err := p.issueChallenge(registerChallengePrefix, passkeyChallenge{
		session: *session,
		context: wctx,
		userID:  userID,
		name:    name,
	})
	if err != nil {
		return "", nil, err
	}
	return handle, creation, nil
}

// FinishRegistration verifies the browser's raw JSON response and stores the
// credential.
func (p *Passkeys) FinishRegistration(ctx context.Context, userID uuid.UUID, handle string, response []byte, name string, keys PasskeyStore) (Passkey, error) {
	challenge, err := p.spendChallenge(registerChallengePrefix, handle)
	if err != nil {
		return Passkey{}, err
	}
	if challenge.userID != userID {
		return Passkey{}, ErrInvalidChallenge
	}

	engine, err := p.engine(challenge.context)
	if err != nil {
		return Passkey{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return Passkey{}, ErrInvalidPasskey
	}
	credential, err := engine.CreateCredential(newCeremonyUser(userID, "", nil), challenge.session, parsed)
	if err != nil {
		return Passkey{}, ErrInvalidPasskey
	}

	switch _, err := keys.GetPasskeyByCredential(ctx, credential.ID); {
	case err == nil:
		return Passkey{}, ErrPasskeyRegistered
	case !isNotFound(err):
		return Passkey{}, err
	}

	if name == "" {
		name = challenge.name
	}
	if name == "" {
		name = "Passkey"
	}
	stored := Passkey{
		ID:             uuid.New(),
		UserID:         userID,
		CredentialID:   credential.ID,
		PublicKey:      credential.PublicKey,
		SignCount:      credential.Authenticator.SignCount,
		Name:           name,
		Transports:     transportNames(credential.Transport),
		RPID:           challenge.context.RPID,
		IsDiscoverable: isDiscoverable(parsed.ClientExtensionResults),
		CreatedAt:      now(p.Now),
	}
	if err := keys.AddPasskey(ctx, stored); err != nil {
		return Passkey{}, err
	}
	return stored, nil
}

// BeginAuthentication returns the options for a login. It takes no email and
// returns no allowCredentials: tailoring the list to an address would make it
// an account-enumeration oracle.
func (p *Passkeys) BeginAuthentication(wctx WebAuthnContext) (string, *protocol.CredentialAssertion, error) {
	engine, err := p.engine(wctx)
	if err != nil {
		return "", nil, err
	}
	assertion, session, err := engine.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return "", nil, fmt.Errorf("auth: beginning passkey login: %w", err)
	}

	handle, err := p.issueChallenge(loginChallengePrefix, passkeyChallenge{
		session: *session,
		context: wctx,
	})
	if err != nil {
		return "", nil, err
	}
	return handle, assertion, nil
}

// FinishAuthentication verifies an assertion and returns the credential that
// made it. The caller decides whether that user may log in.
//
// Every refusal is ErrInvalidPasskey; a database failure is returned as itself.
func (p *Passkeys) FinishAuthentication(ctx context.Context, handle string, response []byte, keys PasskeyStore) (Passkey, error) {
	challenge, err := p.spendChallenge(loginChallengePrefix, handle)
	if err != nil {
		return Passkey{}, err
	}
	engine, err := p.engine(challenge.context)
	if err != nil {
		return Passkey{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return Passkey{}, ErrInvalidPasskey
	}

	// storeErr carries a database failure out of the handler, which can only
	// report "no user". Without it an unreachable Postgres would be
	// indistinguishable from a credential nobody has registered.
	var (
		matched  Passkey
		storeErr error
	)
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		found, err := keys.GetPasskeyByCredential(ctx, rawID)
		if err != nil {
			if !isNotFound(err) {
				storeErr = err
			}
			return nil, ErrInvalidPasskey
		}
		if !bytes.Equal(userHandle, userHandleFor(found.UserID)) {
			return nil, ErrInvalidPasskey
		}
		matched = found
		return newCeremonyUser(found.UserID, "", []webauthn.Credential{credentialOf(found)}), nil
	}

	credential, err := engine.ValidateDiscoverableLogin(handler, challenge.session, parsed)
	if storeErr != nil {
		return Passkey{}, storeErr
	}
	if err != nil {
		return Passkey{}, ErrInvalidPasskey
	}
	if !bytes.Equal(credential.ID, matched.CredentialID) {
		return Passkey{}, ErrInvalidPasskey
	}
	if !counterAdvanced(matched.SignCount, credential.Authenticator.SignCount) {
		return Passkey{}, ErrInvalidPasskey
	}

	used := now(p.Now)
	if err := keys.UpdatePasskeyUse(ctx, matched.ID, credential.Authenticator.SignCount, used); err != nil {
		return Passkey{}, err
	}
	matched.SignCount = credential.Authenticator.SignCount
	matched.LastUsedAt = &used
	return matched, nil
}

// counterAdvanced answers: has the authenticator's signature counter moved
// forward?
//
// A counter that repeats or goes backwards means the private key has been
// copied, so the login is refused. Both sides at zero is allowed: synced
// passkeys never implement the counter.
//
// go-webauthn only warns (CloneWarning) and still returns the credential, so
// the enforcement lives here.
func counterAdvanced(stored, presented uint32) bool {
	if stored == 0 && presented == 0 {
		return true
	}
	return presented > stored
}

func (p *Passkeys) engine(wctx WebAuthnContext) (*webauthn.WebAuthn, error) {
	engine, err := webauthn.New(&webauthn.Config{
		RPID:          wctx.RPID,
		RPDisplayName: p.RPName,
		RPOrigins:     []string{wctx.Origin},
	})
	if err != nil {
		return nil, fmt.Errorf("auth: configuring the relying party: %w", err)
	}
	return engine, nil
}

func (p *Passkeys) issueChallenge(prefix string, challenge passkeyChallenge) (string, error) {
	handle, err := RandomToken(32)
	if err != nil {
		return "", err
	}
	ttl := p.ChallengeTTL
	if ttl <= 0 {
		ttl = DefaultChallengeTTL
	}
	p.Challenges.Set(prefix+handle, challenge, ttl)
	return handle, nil
}

// spendChallenge reads and deletes in one step, so two concurrent replays of
// one assertion cannot both find the challenge unused.
func (p *Passkeys) spendChallenge(prefix, handle string) (passkeyChallenge, error) {
	challenge, ok := TakeAs[passkeyChallenge](p.Challenges, prefix+handle)
	if !ok || challenge.context.RPID == "" || challenge.context.Origin == "" {
		return passkeyChallenge{}, ErrInvalidChallenge
	}
	return challenge, nil
}

// userHandleFor is the opaque user handle the authenticator stores alongside a
// discoverable credential. Changing its format would orphan every enrolled
// credential.
func userHandleFor(userID uuid.UUID) []byte { return []byte(userID.String()) }

// ceremonyUser adapts our records to what go-webauthn wants to be handed.
type ceremonyUser struct {
	id          []byte
	name        string
	credentials []webauthn.Credential
}

func newCeremonyUser(userID uuid.UUID, email string, credentials []webauthn.Credential) *ceremonyUser {
	if email == "" {
		email = userID.String()
	}
	return &ceremonyUser{id: userHandleFor(userID), name: email, credentials: credentials}
}

func (u *ceremonyUser) WebAuthnID() []byte                         { return u.id }
func (u *ceremonyUser) WebAuthnName() string                       { return u.name }
func (u *ceremonyUser) WebAuthnDisplayName() string                { return u.name }
func (u *ceremonyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func credentialOf(key Passkey) webauthn.Credential {
	return webauthn.Credential{
		ID:            key.CredentialID,
		PublicKey:     key.PublicKey,
		Transport:     transportsOf(key.Transports),
		Authenticator: webauthn.Authenticator{SignCount: key.SignCount},
	}
}

func transportsOf(names []string) []protocol.AuthenticatorTransport {
	if len(names) == 0 {
		return nil
	}
	out := make([]protocol.AuthenticatorTransport, len(names))
	for i, name := range names {
		out[i] = protocol.AuthenticatorTransport(name)
	}
	return out
}

func transportNames(transports []protocol.AuthenticatorTransport) []string {
	if len(transports) == 0 {
		return nil
	}
	out := make([]string, len(transports))
	for i, transport := range transports {
		out[i] = string(transport)
	}
	return out
}

// isDiscoverable reads the credProps extension. An authenticator that does not
// report it is assumed non-discoverable.
func isDiscoverable(extensions protocol.AuthenticationExtensionsClientOutputs) bool {
	properties, ok := extensions["credProps"].(map[string]any)
	if !ok {
		return false
	}
	residentKey, _ := properties["rk"].(bool)
	return residentKey
}
