package api

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
)

// The browser's session lives in an HttpOnly cookie, so a write that carries
// it must prove it came from the app: SameSite=Strict keeps other sites'
// requests from carrying it, and the Origin check covers the rest.

func clockedClient(t *testing.T, now *time.Time) *client {
	t.Helper()
	env := NewEnv(testConfig(), db(t), WithClock(func() time.Time { return *now }))
	return &client{t: t, env: env, handler: RouterFor(env)}
}

func TestTheSessionCookieIsOutOfReachOfScriptsAndOtherSites(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	c := clockedClient(t, &now)
	user := makeUser(t, testPassword)

	cookie := login(c, user.Email, testPassword).requireStatus(http.StatusOK).sessionCookie()
	require.NotNil(t, cookie)
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Equal(t, "/api", cookie.Path)
	// testConfig's browser sessions last a day unused.
	require.Equal(t, now.Add(24*time.Hour), cookie.Expires)
}

func TestTheCookieFollowsTheSlidingExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	c := clockedClient(t, &now)
	user := makeUser(t, testPassword)
	c.signedIn(login(c, user.Email, testPassword).requireStatus(http.StatusOK))

	now = now.Add(20 * time.Hour)
	cookie := c.get("/auth/me").requireStatus(http.StatusOK).sessionCookie()
	require.NotNil(t, cookie, "the browser would drop the cookie before the session ends")
	require.Equal(t, now.Add(24*time.Hour), cookie.Expires)

	now = now.Add(20 * time.Hour)
	c.get("/auth/me").requireStatus(http.StatusOK)
}

func TestACookieWriteFromAnotherOriginIsRefused(t *testing.T) {
	t.Parallel()
	for name, origin := range map[string]string{
		"another site":     "https://elsewhere.example",
		"a sibling port":   "http://localhost:5174",
		"no origin at all": "",
		"an opaque origin": "null",
		"another scheme":   "https://localhost:5173",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t)
			user := makeUser(t, testPassword)
			c.signedIn(login(c, user.Email, testPassword).requireStatus(http.StatusOK))

			c.origin = origin
			c.patch("/auth/me", map[string]any{"theme": "dark"}).requireStatus(http.StatusForbidden)
			c.post("/auth/logout", nil).requireStatus(http.StatusForbidden)

			stored, err := db(t).GetUser(t.Context(), user.ID)
			require.NoError(t, err)
			require.Equal(t, "system", stored.Theme)
			c.get("/auth/me").requireStatus(http.StatusOK)
		})
	}
}

// signIns each prepare a sign-in and return its client and the request that
// completes it, which a refusal leaves unspent.
var signIns = map[string]func(t *testing.T) (*client, func() *response){
	"password": func(t *testing.T) (*client, func() *response) {
		c := newClient(t)
		user := makeUser(t, testPassword)
		return c, func() *response { return login(c, user.Email, testPassword) }
	},
	"second factor": func(t *testing.T) (*client, func() *response) {
		c := newClient(t)
		user := makeUser(t, testPassword)
		secret, _ := enrolTOTPFor(t, c, user)
		c.token = ""
		challenge := login(c, user.Email, testPassword).requireStatus(http.StatusOK).json()
		code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
		require.NoError(t, err)
		return c, func() *response {
			return c.post("/auth/token/mfa", map[string]any{
				"mfa_token": challenge["mfa_token"], "code": code,
			})
		}
	},
	"first account": func(t *testing.T) (*client, func() *response) {
		c := emptyServer(t)
		return c, func() *response {
			return c.post("/auth/first-account", map[string]any{
				"email": "first@example.test", "password": "a long enough passphrase",
			})
		}
	},
}

func TestASignInPostedFromAnotherSiteIsRefused(t *testing.T) {
	t.Parallel()
	// Login CSRF: a page elsewhere posts the attacker's credentials, and the
	// browser is left signed in to the attacker's account.
	for name, prepare := range signIns {
		for hostileName, hostile := range map[string]string{
			"another site":     "https://elsewhere.example",
			"an opaque origin": "null",
		} {
			t.Run(name+"/"+hostileName, func(t *testing.T) {
				t.Parallel()
				c, send := prepare(t)
				c.origin = hostile
				refused := send().requireStatus(http.StatusBadRequest)
				require.Equal(t, auth.OriginMismatch, refused.json()["code"])
				require.Nil(t, refused.sessionCookie())
			})
		}
	}
}

func TestASignInWithoutAnOriginOrFromTheAppIsAccepted(t *testing.T) {
	t.Parallel()
	for name, prepare := range signIns {
		for originName, origin := range map[string]string{
			// The MCP server and scripts.
			"no origin": "",
			"the app":   "http://localhost:5173",
		} {
			t.Run(name+"/"+originName, func(t *testing.T) {
				t.Parallel()
				c, send := prepare(t)
				c.origin = "https://elsewhere.example"
				send().requireStatus(http.StatusBadRequest)

				c.origin = origin
				accepted := send()
				require.Less(t, accepted.Code, 300, "body: %s", accepted.Body.String())
				c.signedIn(accepted).get("/auth/me").requireStatus(http.StatusOK)
			})
		}
	}
}

func TestAPasskeySignInPostedFromAnotherSiteIsRefused(t *testing.T) {
	t.Parallel()
	// The assertion is checked against the challenge only once the Origin
	// guard has passed, so an unknown challenge tells the two apart.
	verify := func(origin string) *response {
		c := newClient(t)
		c.origin = origin
		return c.post("/auth/passkeys/authenticate/verify", map[string]any{
			"challenge_id": "unknown", "credential": map[string]any{},
		})
	}

	refused := verify("https://elsewhere.example").requireStatus(http.StatusBadRequest)
	require.Equal(t, auth.OriginMismatch, refused.json()["code"])
	require.Nil(t, refused.sessionCookie())

	for _, origin := range []string{"", "http://localhost:5173"} {
		passed := verify(origin).requireStatus(http.StatusBadRequest).json()
		require.Nil(t, passed["code"], "origin %q", origin)
		require.Equal(t, "Invalid or expired challenge", passed["detail"])
	}
}

func TestACookieWriteFromTheAddressTheAppWasReachedOnIsAccepted(t *testing.T) {
	t.Parallel()
	// FRONTEND_URL is one address; the binary serves the app on any other
	// that reaches it.
	c := newClient(t)
	user := makeUser(t, testPassword)
	c.signedIn(login(c, user.Email, testPassword).requireStatus(http.StatusOK))

	c.origin = "http://example.com"
	c.patch("/auth/me", map[string]any{"theme": "dark"}).requireStatus(http.StatusOK)
}

func TestOIDCSignInSetsTheSessionCookie(t *testing.T) {
	t.Parallel()
	c := emptyServer(t)
	idp := newTestIdP(t)
	c.env.OIDC.HTTPClient = idp.server.Client()
	c.env.OIDC.Configure(auth.OIDCSettings{
		Enabled: true, ProviderName: "Example", AutoRegister: true,
		DiscoveryURL: idp.server.URL + "/.well-known/openid-configuration",
		ClientID:     "agentifi", ClientSecret: "shh", Scopes: []string{"openid", "email"},
	})

	started := c.get("/auth/oidc/login").requireStatus(http.StatusTemporaryRedirect)
	authorize, err := url.Parse(started.Header().Get("Location"))
	require.NoError(t, err)
	idp.nonce = authorize.Query().Get("nonce")
	var binding *http.Cookie
	for _, cookie := range started.Result().Cookies() {
		if cookie.Name == oidcBindingCookie {
			binding = cookie
		}
	}
	require.NotNil(t, binding)

	callback := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?"+url.Values{
		"code": {"the-code"}, "state": {authorize.Query().Get("state")},
	}.Encode(), nil)
	callback.AddCookie(binding)
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, callback)
	landed := (&response{t: t, ResponseRecorder: recorder}).requireStatus(http.StatusTemporaryRedirect)

	require.Equal(t, "http://localhost:5173/auth/oidc/callback", landed.Header().Get("Location"),
		"nothing that names the session is in the URL")
	me := c.signedIn(landed).get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, "oidc.person@example.test", me["email"])
}

// testIdP is the least of a provider the callback needs: discovery, a key, and
// a token endpoint whose id_token is signed with it and carries the nonce.
type testIdP struct {
	server *httptest.Server
	nonce  string
}

func newTestIdP(t *testing.T) *testIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	idp := &testIdP{}
	answer := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(value))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{
			"issuer":                                idp.server.URL,
			"authorization_endpoint":                idp.server.URL + "/authorize",
			"token_endpoint":                        idp.server.URL + "/token",
			"jwks_uri":                              idp.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{"keys": []any{map[string]any{
			"kty": "RSA", "kid": "test-key", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		claims, err := json.Marshal(map[string]any{
			"iss": idp.server.URL, "sub": "provider-sub-1", "aud": "agentifi",
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
			"nonce": idp.nonce, "email": "oidc.person@example.test", "email_verified": true,
		})
		require.NoError(t, err)
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
		require.NoError(t, err)
		signed, err := signer.Sign(claims)
		require.NoError(t, err)
		idToken, err := signed.CompactSerialize()
		require.NoError(t, err)
		answer(w, map[string]any{
			"access_token": "provider-access-token", "token_type": "Bearer",
			"expires_in": 3600, "id_token": idToken,
		})
	})
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}
