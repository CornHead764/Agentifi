package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	firefoxOnLinux = "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"
	safariOnIOS    = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 " +
		"(KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
)

// browserFor signs in from a browser that says what it is.
func browserFor(t *testing.T, email, userAgent string) *client {
	t.Helper()
	c := newClient(t)
	r := httptest.NewRequest(http.MethodPost, "/auth/token",
		strings.NewReader(url.Values{"username": {email}, "password": {testPassword}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("User-Agent", userAgent)
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, r)
	return c.signedIn((&response{t: t, ResponseRecorder: recorder}).requireStatus(http.StatusOK))
}

func sessionNamed(t *testing.T, sessions []map[string]any, name string) map[string]any {
	t.Helper()
	for _, session := range sessions {
		if session["name"] == name {
			return session
		}
	}
	require.Failf(t, "no such session", "%q in %v", name, sessions)
	return nil
}

func TestTheSessionListNamesEachBrowserAndMarksThisOne(t *testing.T) {
	t.Parallel()
	user := makeUser(t, testPassword)
	laptop := browserFor(t, user.Email, firefoxOnLinux)
	browserFor(t, user.Email, safariOnIOS)

	sessions := laptop.get("/auth/sessions").requireStatus(http.StatusOK).list()
	require.Len(t, sessions, 2)
	require.Equal(t, true, sessionNamed(t, sessions, "Firefox on Linux")["current"])
	phone := sessionNamed(t, sessions, "Safari on iOS")
	require.Equal(t, false, phone["current"])
	require.Equal(t, "web", phone["kind"])
}

func TestARevokedSessionIsRefusedOnItsNextRequest(t *testing.T) {
	t.Parallel()
	user := makeUser(t, testPassword)
	laptop := browserFor(t, user.Email, firefoxOnLinux)
	phone := browserFor(t, user.Email, safariOnIOS)

	sessions := laptop.get("/auth/sessions").requireStatus(http.StatusOK).list()
	id := sessionNamed(t, sessions, "Safari on iOS")["id"].(string)
	laptop.del("/auth/sessions/" + id).requireStatus(http.StatusNoContent)

	phone.get("/auth/me").requireStatus(http.StatusUnauthorized)
	laptop.get("/auth/me").requireStatus(http.StatusOK)
}

func TestSomebodyElsesSessionCannotBeRevoked(t *testing.T) {
	t.Parallel()
	owner := makeUser(t, testPassword)
	theirs := browserFor(t, owner.Email, firefoxOnLinux)
	id := theirs.get("/auth/sessions").requireStatus(http.StatusOK).list()[0]["id"].(string)

	stranger := browserFor(t, makeUser(t, testPassword).Email, safariOnIOS)
	stranger.del("/auth/sessions/" + id).requireStatus(http.StatusNotFound)
	theirs.get("/auth/me").requireStatus(http.StatusOK)
}

func TestSigningOutEverywhereEndsEverySessionAndToken(t *testing.T) {
	t.Parallel()
	user := makeUser(t, testPassword)
	laptop := browserFor(t, user.Email, firefoxOnLinux)
	phone := browserFor(t, user.Email, safariOnIOS)
	token := laptop.post("/auth/personal-tokens", map[string]any{"name": "Backup script"}).
		requireStatus(http.StatusCreated).json()["token"].(string)
	script := newClient(t)
	script.token = token

	laptop.post("/auth/sessions/revoke-all", nil).requireStatus(http.StatusNoContent)

	for _, c := range []*client{laptop, phone, script} {
		c.get("/auth/me").requireStatus(http.StatusUnauthorized)
	}
}

func TestAPersonalTokenWorksAsABearerTokenWithoutAnOrigin(t *testing.T) {
	t.Parallel()
	// What the MCP server and scripts send: no cookie, no Origin.
	user := makeUser(t, testPassword)
	browser := browserFor(t, user.Email, firefoxOnLinux)
	created := browser.post("/auth/personal-tokens", map[string]any{"name": "  MCP server  "}).
		requireStatus(http.StatusCreated).json()
	token := created["token"].(string)
	require.Equal(t, "MCP server", created["session"].(map[string]any)["name"])

	script := newClient(t)
	script.token = token
	require.Equal(t, user.Email, script.get("/auth/me").requireStatus(http.StatusOK).json()["email"])
	script.patch("/auth/me", map[string]any{"theme": "dark"}).requireStatus(http.StatusOK)

	listed := browser.get("/auth/sessions").requireStatus(http.StatusOK)
	require.NotContains(t, listed.Body.String(), token, "a token is shown once, when it is made")
	require.Equal(t, "personal", sessionNamed(t, listed.list(), "MCP server")["kind"])
}

func TestAPersonalTokenNeedsAName(t *testing.T) {
	t.Parallel()
	user := makeUser(t, testPassword)
	browser := browserFor(t, user.Email, firefoxOnLinux)
	for _, name := range []string{"", "   ", strings.Repeat("n", maxSessionNameLength+1)} {
		browser.post("/auth/personal-tokens", map[string]any{"name": name}).
			requireStatus(http.StatusUnprocessableEntity)
	}
}
