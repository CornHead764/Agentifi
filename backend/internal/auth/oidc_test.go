package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// A real provider, in-process: a discovery document, a JWKS, a token endpoint
// that checks PKCE, and a userinfo endpoint. The id_token is genuinely signed
// and genuinely verified against the published key, so the parts that matter —
// audience, issuer, nonce, PKCE — are exercised rather than stubbed.
type fakeIdP struct {
	server *httptest.Server
	key    *rsa.PrivateKey

	clientID string
	subject  string
	email    string
	// nonce is what the id_token will carry. Empty means "echo whatever the
	// authorize URL asked for", which is what an honest provider does.
	nonce string
	// userinfo is what the userinfo endpoint returns. Nil means it 404s.
	userinfo map[string]any

	// codeChallenge is what the test captured off the authorize URL, so the
	// token endpoint can hold the exchange to it.
	codeChallenge  string
	requestedNonce string
	// binding is the cookie value BeginLogin returned, presented back at
	// CompleteLogin like the browser would.
	binding string

	discoveryHits int
	seenVerifier  string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	idp := &fakeIdP{
		key:      key,
		clientID: "agentifi",
		subject:  "provider-sub-1",
		email:    "New@Example.TEST",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		idp.discoveryHits++
		writeJSON(w, map[string]any{
			"issuer":                                idp.server.URL,
			"authorization_endpoint":                idp.server.URL + "/authorize",
			"token_endpoint":                        idp.server.URL + "/token",
			"userinfo_endpoint":                     idp.server.URL + "/userinfo",
			"jwks_uri":                              idp.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []any{map[string]any{
			"kty": "RSA",
			"kid": "test-key",
			"alg": "RS256",
			"use": "sig",
			"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		idp.seenVerifier = r.Form.Get("code_verifier")
		if !idp.pkceHolds() {
			http.Error(w, "invalid_grant", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"access_token": "provider-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idp.idToken(t),
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		if idp.userinfo == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, idp.userinfo)
	})

	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

// pkceHolds is the check a real provider makes: the verifier presented at the
// token endpoint must hash to the challenge presented at the authorize
// endpoint.
func (f *fakeIdP) pkceHolds() bool {
	digest := sha256.Sum256([]byte(f.seenVerifier))
	return f.codeChallenge != "" && base64.RawURLEncoding.EncodeToString(digest[:]) == f.codeChallenge
}

func (f *fakeIdP) idToken(t *testing.T) string {
	t.Helper()
	nonce := f.nonce
	if nonce == "" {
		nonce = f.requestedNonce
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":            f.server.URL,
		"sub":            f.subject,
		"aud":            f.clientID,
		"iat":            time.Now().Unix(),
		"exp":            time.Now().Add(time.Hour).Unix(),
		"nonce":          nonce,
		"email":          f.email,
		"email_verified": true,
	})
	token.Header["kid"] = "test-key"
	signed, err := token.SignedString(f.key)
	require.NoError(t, err)
	return signed
}

func (f *fakeIdP) client() *OIDC {
	client := &OIDC{
		RedirectURI: "https://money.example.com/auth/oidc/callback",
		State:       NewSecretStore(),
		HTTPClient:  f.server.Client(),
	}
	client.Configure(f.settings())
	return client
}

func (f *fakeIdP) settings() OIDCSettings {
	return OIDCSettings{
		Enabled:      true,
		ProviderName: "Example",
		DiscoveryURL: f.server.URL + "/.well-known/openid-configuration",
		ClientID:     f.clientID,
		ClientSecret: "shh",
		Scopes:       []string{"openid", "email", "profile"},
	}
}

// begin walks the redirect the browser would follow, recording what the
// provider would have seen.
func (f *fakeIdP) begin(t *testing.T, client *OIDC) string {
	t.Helper()
	raw, binding, err := client.BeginLogin(context.Background())
	require.NoError(t, err)
	f.binding = binding

	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	query := parsed.Query()

	require.Equal(t, "S256", query.Get("code_challenge_method"))
	require.NotEmpty(t, query.Get("code_challenge"))
	require.Equal(t, "code", query.Get("response_type"))
	require.Equal(t, client.CallbackURI(), query.Get("redirect_uri"))

	f.codeChallenge = query.Get("code_challenge")
	f.requestedNonce = query.Get("nonce")
	return query.Get("state")
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestACallbackBecomesAnIdentity(t *testing.T) {
	idp := newFakeIdP(t)
	client := idp.client()
	state := idp.begin(t, client)

	identity, err := client.CompleteLogin(context.Background(), "the-code", state, idp.binding)
	require.NoError(t, err)

	require.Equal(t, idp.server.URL, identity.Issuer)
	require.Equal(t, "provider-sub-1", identity.Subject)
	require.Equal(t, "new@example.test", identity.Email, "normalized once, here")
	require.True(t, identity.EmailVerified)
}

func TestTheCodeVerifierNeverLeavesTheServerUntilTheExchange(t *testing.T) {
	// A verifier round-tripped through the browser is not a verifier: what
	// goes out is the challenge, and the exchange is what proves we hold the
	// matching secret.
	idp := newFakeIdP(t)
	client := idp.client()
	state := idp.begin(t, client)

	_, err := client.CompleteLogin(context.Background(), "the-code", state, idp.binding)
	require.NoError(t, err)

	require.NotEmpty(t, idp.seenVerifier)
	require.NotEqual(t, idp.codeChallenge, idp.seenVerifier)
	digest := sha256.Sum256([]byte(idp.seenVerifier))
	require.Equal(t, idp.codeChallenge, base64.RawURLEncoding.EncodeToString(digest[:]))
}

func TestTheStateIsSpentWhetherOrNotTheRestSucceeds(t *testing.T) {
	// A callback URL lands in browser history and in proxy logs. It must not
	// be walkable twice.
	idp := newFakeIdP(t)
	client := idp.client()
	state := idp.begin(t, client)

	_, err := client.CompleteLogin(context.Background(), "the-code", state, idp.binding)
	require.NoError(t, err)

	_, err = client.CompleteLogin(context.Background(), "the-code", state, idp.binding)
	require.ErrorIs(t, err, ErrOIDCLogin)
}

func TestAnUnknownStateIsRefused(t *testing.T) {
	idp := newFakeIdP(t)
	client := idp.client()
	idp.begin(t, client)

	_, err := client.CompleteLogin(context.Background(), "the-code", "a state nobody issued", "")
	require.ErrorIs(t, err, ErrOIDCLogin)
}

func TestACallbackWithoutTheBrowserBindingIsRefused(t *testing.T) {
	// The state URL is shareable by design — that is what makes it an attack:
	// finish the flow as yourself, then hand the callback to somebody else and
	// their browser wakes up logged into your space. The cookie binding is
	// what breaks the handoff.
	idp := newFakeIdP(t)
	client := idp.client()
	state := idp.begin(t, client)

	_, err := client.CompleteLogin(context.Background(), "the-code", state, "a value from some other browser")
	require.ErrorIs(t, err, ErrOIDCLogin)

	// The right value, in the browser that began the flow, completes.
	fresh := newFakeIdP(t)
	freshClient := fresh.client()
	freshState := fresh.begin(t, freshClient)
	_, err = freshClient.CompleteLogin(context.Background(), "the-code", freshState, fresh.binding)
	require.NoError(t, err)
}

func TestAnIdTokenWithTheWrongNonceIsRefused(t *testing.T) {
	// Without this the same id_token can be fed back through a second login.
	idp := newFakeIdP(t)
	client := idp.client()
	state := idp.begin(t, client)
	idp.nonce = "a nonce from some other login"

	_, err := client.CompleteLogin(context.Background(), "the-code", state, idp.binding)
	require.ErrorIs(t, err, ErrOIDCLogin)
}

func TestAnIdTokenForAnotherAudienceIsRefused(t *testing.T) {
	idp := newFakeIdP(t)
	client := idp.client()
	state := idp.begin(t, client)
	idp.clientID = "some-other-app" // the client id in the token, not in the request

	_, err := client.CompleteLogin(context.Background(), "the-code", state, idp.binding)
	require.ErrorIs(t, err, ErrOIDCLogin)
}

func TestUserinfoCannotOverwriteTheSignedClaims(t *testing.T) {
	// Userinfo is fetched with a bearer token and merged underneath the signed
	// claims, so a provider that returns a different email there cannot
	// replace the audience-validated one.
	idp := newFakeIdP(t)
	idp.userinfo = map[string]any{
		"sub":   "provider-sub-1",
		"email": "attacker@example.test",
		"name":  "New Person",
	}
	client := idp.client()
	state := idp.begin(t, client)

	identity, err := client.CompleteLogin(context.Background(), "the-code", state, idp.binding)
	require.NoError(t, err)
	require.Equal(t, "new@example.test", identity.Email)
	require.Equal(t, "New Person", identity.FullName, "userinfo still fills what the token omitted")
}

func TestAUserinfoSubjectThatDisagreesWithTheSignedOneIsRefused(t *testing.T) {
	// The two responses are about different people; nothing good follows from
	// picking one.
	idp := newFakeIdP(t)
	idp.userinfo = map[string]any{"sub": "someone-else", "email": "new@example.test"}
	client := idp.client()
	state := idp.begin(t, client)

	_, err := client.CompleteLogin(context.Background(), "the-code", state, idp.binding)
	require.ErrorIs(t, err, ErrOIDCLogin)
}

func TestAnIdentityIsTheIssuerAndTheSubjectTogether(t *testing.T) {
	// Two providers can both mint subject "1234". The pair is what `users` is
	// unique on, so the same subject from two issuers must produce two
	// identities.
	first, second := newFakeIdP(t), newFakeIdP(t)
	first.subject, second.subject = "1234", "1234"
	second.email = "other@example.test"

	clientOne := first.client()
	firstState := first.begin(t, clientOne)
	one, err := clientOne.CompleteLogin(context.Background(), "c", firstState, first.binding)
	require.NoError(t, err)

	clientTwo := second.client()
	secondState := second.begin(t, clientTwo)
	two, err := clientTwo.CompleteLogin(context.Background(), "c", secondState, second.binding)
	require.NoError(t, err)

	require.Equal(t, one.Subject, two.Subject)
	require.NotEqual(t, one.Issuer, two.Issuer)
}

func TestDiscoveryIsFetchedOnceAndCached(t *testing.T) {
	// Refetching per login turns every sign-in into an extra round trip and
	// makes a slow IdP look like a broken app.
	idp := newFakeIdP(t)
	client := idp.client()

	for range 3 {
		state := idp.begin(t, client)
		_, err := client.CompleteLogin(context.Background(), "the-code", state, idp.binding)
		require.NoError(t, err)
	}
	require.Equal(t, 1, idp.discoveryHits)

	client.ResetDiscoveryCache()
	state := idp.begin(t, client)
	_, err := client.CompleteLogin(context.Background(), "the-code", state, idp.binding)
	require.NoError(t, err)
	require.Equal(t, 2, idp.discoveryHits)
}

func TestAnUnconfiguredProviderRefusesRatherThanTrying(t *testing.T) {
	disabled := &OIDC{State: NewSecretStore()}
	require.False(t, disabled.IsConfigured())

	_, _, err := disabled.BeginLogin(context.Background())
	require.ErrorIs(t, err, ErrOIDCDisabled)
}

func TestAnUnreachableProviderIsAnOperatorProblemNotACallerProblem(t *testing.T) {
	client := &OIDC{
		State:      NewSecretStore(),
		HTTPClient: &http.Client{Timeout: time.Second},
	}
	client.Configure(OIDCSettings{
		Enabled:      true,
		DiscoveryURL: "http://127.0.0.1:1/.well-known/openid-configuration",
		ClientID:     "agentifi",
	})
	_, _, err := client.BeginLogin(context.Background())
	require.ErrorIs(t, err, ErrOIDCUnreachable)
}

func TestTheCallbackURIIsDerivedFromTheFrontendWhenUnset(t *testing.T) {
	client := &OIDC{FrontendURL: "https://money.example.com/"}
	require.Equal(t, "https://money.example.com/auth/oidc/callback", client.CallbackURI())

	client.RedirectURI = "https://api.example.com/auth/oidc/callback"
	require.Equal(t, "https://api.example.com/auth/oidc/callback", client.CallbackURI())
}

// Reconfiguring while the server runs is what makes the settings screen more
// than a form that writes a row nothing reads until the next deploy.

func TestConfigureTakesEffectWithoutARestart(t *testing.T) {
	idp := newFakeIdP(t)
	client := &OIDC{State: NewSecretStore(), HTTPClient: idp.server.Client()}
	require.False(t, client.IsConfigured())

	_, _, err := client.BeginLogin(context.Background())
	require.ErrorIs(t, err, ErrOIDCDisabled)

	client.Configure(idp.settings())
	require.True(t, client.IsConfigured())
	require.Equal(t, "Example", client.Settings().ProviderName)

	_, _, err = client.BeginLogin(context.Background())
	require.NoError(t, err)
}

func TestConfigureDropsTheCachedDiscoveryDocument(t *testing.T) {
	// A cached provider outliving a client-id change holds a verifier built
	// for the old audience, and every sign-in then fails naming none of that.
	idp := newFakeIdP(t)
	client := idp.client()

	_, _, err := client.BeginLogin(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, idp.discoveryHits)

	settings := idp.settings()
	settings.ClientID = idp.clientID
	client.Configure(settings)

	_, _, err = client.BeginLogin(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, idp.discoveryHits)
}

func TestSettingsIsACopyRatherThanTheLiveStruct(t *testing.T) {
	// A caller that could write through the returned slice would be changing
	// the provider a login in flight is reading.
	idp := newFakeIdP(t)
	client := idp.client()

	scopes := client.Settings().Scopes
	scopes[0] = "tampered"
	require.Equal(t, "openid", client.Settings().Scopes[0])
}

func TestProbeDiscoveryReadsAProviderWithoutConfiguringOne(t *testing.T) {
	idp := newFakeIdP(t)
	client := &OIDC{State: NewSecretStore(), HTTPClient: idp.server.Client()}

	document, err := client.ProbeDiscovery(context.Background(),
		idp.server.URL+"/.well-known/openid-configuration")
	require.NoError(t, err)
	require.Equal(t, idp.server.URL, document.Issuer)
	require.NotEmpty(t, document.TokenEndpoint)
	// Probing says nothing about what this install uses.
	require.False(t, client.IsConfigured())
}

func TestProbeDiscoveryReportsAProviderThatIsNotOne(t *testing.T) {
	client := &OIDC{HTTPClient: &http.Client{Timeout: time.Second}}
	_, err := client.ProbeDiscovery(context.Background(),
		"http://127.0.0.1:1/.well-known/openid-configuration")
	require.ErrorIs(t, err, ErrOIDCUnreachable)
}
