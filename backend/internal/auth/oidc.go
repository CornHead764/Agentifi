package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/httpx"
	oidclib "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDC authorization-code login against one configured provider, with PKCE and
// a nonce.
//
//   - State, nonce and code verifier are held server-side, keyed by an opaque
//     state value. A verifier round-tripped through the browser is not one.
//   - The id_token is the source of truth. Userinfo is merged underneath the
//     signed claims, so it cannot overwrite the audience-validated ones.
//   - An identity is the issuer and the subject together; `users` is unique on
//     the pair.
//
// Whether an identity may become a session is the service layer's decision.

// DiscoveryTTL is how long a provider's metadata is trusted.
const DiscoveryTTL = time.Hour

// OIDCIdentity is who the provider says this is, and which provider said it.
type OIDCIdentity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	FullName      string
}

// OIDCSettings is the provider as an administrator configures it. These values
// change while the server runs, so they are read through Settings and written
// through Configure, never touched directly.
type OIDCSettings struct {
	Enabled      bool
	ProviderName string
	// DiscoveryURL is the full .well-known URL, not the issuer.
	DiscoveryURL string
	ClientID     string
	ClientSecret string
	Scopes       []string
	// AutoRegister decides whether an unknown subject may create an account.
	AutoRegister bool
	// RequireVerifiedEmail refuses an identity whose address the provider will
	// not vouch for.
	RequireVerifiedEmail bool
	// LinkExistingEmail decides whether a matching, verified address may adopt
	// an account that already exists here.
	LinkExistingEmail bool
}

// IsConfigured reports whether a login can be attempted at all.
func (s OIDCSettings) IsConfigured() bool {
	return s.Enabled && s.ClientID != "" && s.DiscoveryURL != ""
}

// OIDC is the configured provider for one instance. The zero value is a
// disabled provider.
//
// The fields below are the deployment's, not the administrator's: letting a
// settings form change them would let somebody point the callback somewhere
// the provider has never heard of.
type OIDC struct {
	// RedirectURI is where the provider sends the browser back. Empty derives
	// it from FrontendURL, which is right only when the API is proxied under
	// it.
	RedirectURI string
	FrontendURL string
	// StateTTL bounds how long a started login may sit in a browser tab.
	StateTTL time.Duration
	// State holds the nonce and code verifier between the redirect out and the
	// callback back.
	State *SecretStore
	// HTTPClient is nil for a client with a sane timeout.
	HTTPClient *http.Client
	Now        func() time.Time

	mu        sync.Mutex
	settings  OIDCSettings
	cached    *oidclib.Provider
	cachedFor string
	cachedAt  time.Time
}

// oidcState is what one in-flight login needs to finish. It never leaves the
// process.
type oidcState struct {
	nonce    string
	verifier string
	// browserBinding is the SHA-256 of the cookie value handed to the browser
	// that began this login. The callback must present the preimage, so a
	// state URL an attacker completes against their own provider account
	// cannot be replayed into somebody else's browser — without it, that
	// callback logs the victim into the attacker's space (login CSRF).
	browserBinding string
}

// BindingDigest is what BeginLogin stores instead of the cookie value, so the
// server-side state is not itself a bearer credential for the flow.
func BindingDigest(binding string) string {
	sum := sha256.Sum256([]byte("agentifi:oidc-binding:" + binding))
	return hex.EncodeToString(sum[:])
}

// Settings is the provider as it stands right now, copied so the caller cannot
// change it from underneath a login in flight.
func (o *OIDC) Settings() OIDCSettings {
	if o == nil {
		return OIDCSettings{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	settings := o.settings
	settings.Scopes = slices.Clone(o.settings.Scopes)
	return settings
}

// Configure replaces the provider while the server runs.
//
// The discovery cache is dropped unconditionally: a client id or secret changed
// against the same IdP would otherwise leave a verifier built for the old
// audience.
func (o *OIDC) Configure(settings OIDCSettings) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.settings = settings
	o.settings.Scopes = slices.Clone(settings.Scopes)
	o.cached = nil
	o.cachedFor = ""
}

// IsConfigured reports whether a login can be attempted at all.
func (o *OIDC) IsConfigured() bool {
	return o != nil && o.Settings().IsConfigured()
}

// CallbackURI is where the provider sends the browser back.
func (o *OIDC) CallbackURI() string {
	if o.RedirectURI != "" {
		return o.RedirectURI
	}
	return strings.TrimRight(o.FrontendURL, "/") + "/auth/oidc/callback"
}

// BeginLogin returns the URL to send the browser to and the binding value to
// plant in it as an HttpOnly cookie. CompleteLogin refuses any callback that
// does not present it; these routes run before there is a session, so the
// cookie is the only anchor tying a callback to the browser that started it.
func (o *OIDC) BeginLogin(ctx context.Context) (string, string, error) {
	settings := o.Settings()
	provider, err := o.provider(ctx, settings)
	if err != nil {
		return "", "", err
	}

	state, err := RandomToken(32)
	if err != nil {
		return "", "", err
	}
	nonce, err := RandomToken(32)
	if err != nil {
		return "", "", err
	}
	binding, err := RandomToken(32)
	if err != nil {
		return "", "", err
	}
	verifier := oauth2.GenerateVerifier()

	ttl := o.StateTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	o.State.Set(oidcStateKey(state), oidcState{
		nonce:          nonce,
		verifier:       verifier,
		browserBinding: BindingDigest(binding),
	}, ttl)

	config := o.oauthConfig(settings, provider)
	return config.AuthCodeURL(state,
		oidclib.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	), binding, nil
}

// CompleteLogin turns a callback into an identity, or refuses.
//
// The state is spent whether or not the rest succeeds, so a callback URL that
// lands in history or a proxy log cannot be walked twice. A missing or wrong
// browser binding refuses before the exchange (login CSRF).
func (o *OIDC) CompleteLogin(ctx context.Context, code, state, presentedBinding string) (OIDCIdentity, error) {
	pending, ok := TakeAs[oidcState](o.State, oidcStateKey(state))
	if !ok || pending.nonce == "" {
		return OIDCIdentity{}, ErrOIDCLogin
	}
	if !ConstantTimeEquals(pending.browserBinding, BindingDigest(presentedBinding)) {
		return OIDCIdentity{}, ErrOIDCLogin
	}

	settings := o.Settings()
	provider, err := o.provider(ctx, settings)
	if err != nil {
		return OIDCIdentity{}, err
	}
	ctx = oidclib.ClientContext(ctx, o.httpClient())

	token, err := o.oauthConfig(settings, provider).
		Exchange(ctx, code, oauth2.VerifierOption(pending.verifier))
	if err != nil {
		return OIDCIdentity{}, ErrOIDCLogin
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return OIDCIdentity{}, ErrOIDCLogin
	}

	// Verify checks the signature against the provider's JWKS, the audience
	// against our client id, the issuer against the discovery document, and
	// the expiry.
	idToken, err := provider.Verifier(&oidclib.Config{ClientID: settings.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return OIDCIdentity{}, ErrOIDCLogin
	}
	// Without this the same id_token can be fed back through a second login.
	if !ConstantTimeEquals(idToken.Nonce, pending.nonce) {
		return OIDCIdentity{}, ErrOIDCLogin
	}
	// at_hash is optional in the code flow: checked when present, not
	// demanded.
	if idToken.AccessTokenHash != "" {
		if err := idToken.VerifyAccessToken(token.AccessToken); err != nil {
			return OIDCIdentity{}, ErrOIDCLogin
		}
	}

	var signed oidcProfile
	if err := idToken.Claims(&signed); err != nil {
		return OIDCIdentity{}, ErrOIDCLogin
	}

	profile := signed
	if info, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token)); err == nil {
		var extra oidcProfile
		if err := info.Claims(&extra); err == nil {
			// A userinfo subject that disagrees with the signed one means the
			// two responses are about different people.
			if extra.Subject != "" && extra.Subject != signed.Subject {
				return OIDCIdentity{}, ErrOIDCLogin
			}
			profile = extra.mergedUnder(signed)
		}
	}

	// The issuer comes from the signed token, which is what the subject is
	// scoped to.
	if idToken.Issuer == "" || profile.Subject == "" || profile.Email == "" {
		return OIDCIdentity{}, ErrOIDCLogin
	}
	return OIDCIdentity{
		Issuer:        idToken.Issuer,
		Subject:       profile.Subject,
		Email:         strings.ToLower(strings.TrimSpace(profile.Email)),
		EmailVerified: profile.emailVerified(),
		FullName:      profile.fullName(),
	}, nil
}

// ResetDiscoveryCache forgets the provider metadata. For tests, and for a
// deployment that has just repointed its IdP.
func (o *OIDC) ResetDiscoveryCache() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cached = nil
	o.cachedFor = ""
}

// provider is the discovery document, cached together with the verifier that
// holds the JWKS cache, so signing keys are not refetched per login.
func (o *OIDC) provider(ctx context.Context, settings OIDCSettings) (*oidclib.Provider, error) {
	if !settings.IsConfigured() {
		return nil, ErrOIDCDisabled
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cached != nil && o.cachedFor == settings.DiscoveryURL &&
		now(o.Now).Sub(o.cachedAt) < DiscoveryTTL {
		return o.cached, nil
	}

	document, err := o.fetchDiscovery(ctx, settings.DiscoveryURL)
	if err != nil {
		return nil, err
	}
	if err := document.usable(); err != nil {
		return nil, err
	}

	// Built from the fetched document rather than through oidc.NewProvider,
	// which would refetch it from an issuer URL we would have to guess.
	provider := (&oidclib.ProviderConfig{
		IssuerURL:   document.Issuer,
		AuthURL:     document.AuthorizationEndpoint,
		TokenURL:    document.TokenEndpoint,
		UserInfoURL: document.UserInfoEndpoint,
		JWKSURL:     document.JWKSURI,
		Algorithms:  document.SigningAlgorithms,
	}).NewProvider(oidclib.ClientContext(ctx, o.httpClient()))

	o.cached = provider
	o.cachedFor = settings.DiscoveryURL
	o.cachedAt = now(o.Now)
	return provider, nil
}

type DiscoveryDocument struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	UserInfoEndpoint      string   `json:"userinfo_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	SigningAlgorithms     []string `json:"id_token_signing_alg_values_supported"`
}

// usable reports whether a login could be built from this document.
func (d DiscoveryDocument) usable() error {
	if d.Issuer == "" || d.AuthorizationEndpoint == "" ||
		d.TokenEndpoint == "" || d.JWKSURI == "" {
		return fmt.Errorf("%w: the discovery document is missing an endpoint", ErrOIDCUnreachable)
	}
	return nil
}

// ProbeDiscovery fetches one provider's metadata and reports what it says,
// without configuring anything or disturbing the cache.
func (o *OIDC) ProbeDiscovery(ctx context.Context, discoveryURL string) (DiscoveryDocument, error) {
	document, err := o.fetchDiscovery(ctx, discoveryURL)
	if err != nil {
		return DiscoveryDocument{}, err
	}
	return document, document.usable()
}

func (o *OIDC) fetchDiscovery(ctx context.Context, discoveryURL string) (DiscoveryDocument, error) {
	var document DiscoveryDocument
	if _, err := httpx.GetJSON(ctx, o.httpClient(), discoveryURL, 1<<20, &document); err != nil {
		return DiscoveryDocument{}, fmt.Errorf("%w: %w", ErrOIDCUnreachable, err)
	}
	return document, nil
}

func (o *OIDC) oauthConfig(settings OIDCSettings, provider *oidclib.Provider) *oauth2.Config {
	scopes := settings.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidclib.ScopeOpenID, "email", "profile"}
	}
	return &oauth2.Config{
		ClientID:     settings.ClientID,
		ClientSecret: settings.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  o.CallbackURI(),
		Scopes:       scopes,
	}
}

func (o *OIDC) httpClient() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func oidcStateKey(state string) string { return "oidc_state:" + state }

type oidcProfile struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	// EmailVerified is raw because providers disagree about its type: the spec
	// says boolean and several send the string "true".
	EmailVerified     json.RawMessage `json:"email_verified"`
	Name              string          `json:"name"`
	PreferredUsername string          `json:"preferred_username"`
}

// mergedUnder puts the signed claims on top of these. Anything the id_token
// asserted wins; userinfo only fills gaps.
func (p oidcProfile) mergedUnder(signed oidcProfile) oidcProfile {
	merged := p
	if signed.Subject != "" {
		merged.Subject = signed.Subject
	}
	if signed.Email != "" {
		merged.Email = signed.Email
	}
	if len(signed.EmailVerified) > 0 {
		merged.EmailVerified = signed.EmailVerified
	}
	if signed.Name != "" {
		merged.Name = signed.Name
	}
	if signed.PreferredUsername != "" {
		merged.PreferredUsername = signed.PreferredUsername
	}
	return merged
}

func (p oidcProfile) emailVerified() bool {
	raw := strings.Trim(strings.TrimSpace(string(p.EmailVerified)), `"`)
	verified, err := strconv.ParseBool(raw)
	return err == nil && verified
}

func (p oidcProfile) fullName() string {
	if p.Name != "" {
		return p.Name
	}
	return p.PreferredUsername
}
