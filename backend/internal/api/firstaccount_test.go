package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// emptyServer is a client against a server with no accounts at all.
func emptyServer(t *testing.T) *client {
	t.Helper()
	env := NewEnv(testConfig(), storetest.Empty(t))
	return &client{t: t, env: env, handler: RouterFor(env)}
}

func TestAServerWithNoAccountsOffersTheFirstOne(t *testing.T) {
	c := emptyServer(t)

	status := c.do(http.MethodGet, "/auth/first-account", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, status["open"])

	created := c.do(http.MethodPost, "/auth/first-account", map[string]any{
		"email": "First@Example.test", "full_name": "Avery", "password": "a long enough passphrase",
	}).requireStatus(http.StatusCreated).json()
	token, _ := created["access_token"].(string)
	require.NotEmpty(t, token)

	c.token = token
	me := c.do(http.MethodGet, "/auth/me", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, "first@example.test", me["email"])
	require.Equal(t, true, me["is_superuser"])
	require.Equal(t, false, me["must_change_password"])

	spaces := c.do(http.MethodGet, "/spaces", nil).requireStatus(http.StatusOK).list()
	require.Len(t, spaces, 1)
	require.Equal(t, "Household", spaces[0]["name"])
	require.Equal(t, "owner", spaces[0]["role"])
}

func TestTheFirstAccountSignUpIsClosedOnceAnAccountExists(t *testing.T) {
	c := emptyServer(t)
	c.do(http.MethodPost, "/auth/first-account", map[string]any{
		"email": "first@example.test", "password": "a long enough passphrase",
	}).requireStatus(http.StatusCreated)

	anonymous := &client{t: t, env: c.env, handler: c.handler}
	status := anonymous.do(http.MethodGet, "/auth/first-account", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, false, status["open"])

	refused := anonymous.do(http.MethodPost, "/auth/first-account", map[string]any{
		"email": "second@example.test", "password": "a long enough passphrase",
	}).requireStatus(http.StatusConflict).json()
	require.Equal(t, "first_account_taken", refused["code"])

	users, err := c.env.DB.ListUsers(t.Context())
	require.NoError(t, err)
	require.Len(t, users, 1)
}

func TestTheFirstAccountNeedsAPasswordLongEnough(t *testing.T) {
	c := emptyServer(t)
	c.do(http.MethodPost, "/auth/first-account", map[string]any{
		"email": "first@example.test", "password": "short",
	}).requireStatus(http.StatusUnprocessableEntity)

	open := c.do(http.MethodGet, "/auth/first-account", nil).json()
	require.Equal(t, true, open["open"])
}

func TestTheFirstAccountAnOIDCSignInRegistersAdministersTheServer(t *testing.T) {
	c := emptyServer(t)
	c.env.OIDC.Configure(auth.OIDCSettings{Enabled: true, AutoRegister: true})

	first, created, err := loginWithOIDC(t.Context(), c.env, auth.OIDCIdentity{
		Issuer: "https://idp.example.test", Subject: "one", Email: "one@example.test", EmailVerified: true,
	})
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, first.IsSuperuser)

	second, created, err := loginWithOIDC(t.Context(), c.env, auth.OIDCIdentity{
		Issuer: "https://idp.example.test", Subject: "two", Email: "two@example.test", EmailVerified: true,
	})
	require.NoError(t, err)
	require.True(t, created)
	require.False(t, second.IsSuperuser)
}
