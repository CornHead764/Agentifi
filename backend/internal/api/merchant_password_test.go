package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A merchant login's kept password, over the wire: asked for with the
// sign-in, said to be kept and never shown, handed to a pull, paused when the
// merchant turns it down, and forgotten on request. Every login and key here
// is invented.

const (
	merchantPassword = "a-kept-password"
	merchantAuthKey  = "JBSWY3DPEHPK3PXP"
)

// requireNoSecret fails when a response carries the password or the key.
func requireNoSecret(t *testing.T, r *response) *response {
	t.Helper()
	body := r.Body.String()
	require.NotContains(t, body, merchantPassword, "a response carried the password")
	require.NotContains(t, body, merchantAuthKey, "a response carried the authenticator key")
	return r
}

func TestAKeptMerchantPasswordIsSaidToBeKeptAndNeverShown(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeAgent(t)
	agent.export = agentExport()
	withAgent(l, agent)
	account := merchantAccount(l, "Alex")
	base := "/merchants/amazon/accounts/" + account

	// Only a key that is one.
	l.alex.post(base+"/sign-in", map[string]any{
		"email": "alex@example.com", "password": merchantPassword,
		"totp_secret": "123456",
	}).requireStatus(http.StatusUnprocessableEntity)

	requireNoSecret(t, l.alex.post(base+"/sign-in", map[string]any{
		"email": "alex@example.com", "password": merchantPassword,
		"totp_secret": strings.ToLower(merchantAuthKey[:8]) + " " + merchantAuthKey[8:],
	}).requireStatus(http.StatusOK))
	requireNoSecret(t, l.alex.post(base+"/sign-in/s1/answer", map[string]any{"code": "123456"}).
		requireStatus(http.StatusOK))
	saved := requireNoSecret(t, l.alex.post(base+"/sign-in/s1/complete", nil).requireStatus(http.StatusOK)).json()
	awaitMerchantPull(t, account)
	require.Equal(t, true, saved["connected"])
	require.Equal(t, true, saved["has_password"])
	require.Equal(t, true, saved["has_totp"])
	require.Equal(t, "", saved["sign_in_paused"])
	listed := requireNoSecret(t, l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK)).list()[0]
	require.Equal(t, true, listed["has_password"])

	// A lapsed session: the pull is handed the kept login, key and all.
	agent.mu.Lock()
	agent.wall = true
	agent.mu.Unlock()
	requireNoSecret(t, l.alex.post(base+"/pull", nil).requireStatus(http.StatusOK))
	handed := agent.credentials[len(agent.credentials)-1]
	require.NotNil(t, handed)
	require.NotNil(t, handed.MailedCode, "the pull can wait for a mailed code")
	require.Equal(t, provider.MerchantCredential{
		Email: "alex@example.com", Password: merchantPassword, TOTPSecret: merchantAuthKey,
	}, provider.MerchantCredential{
		Email: handed.Email, Password: handed.Password, TOTPSecret: handed.TOTPSecret,
	})

	// The merchant turns it down: kept, paused, and the account says why.
	agent.mu.Lock()
	agent.paused = provider.SignInPausedPasswordRefused
	agent.mu.Unlock()
	requireNoSecret(t, l.alex.post(base+"/pull", nil).requireStatus(http.StatusConflict))
	paused := requireNoSecret(t, l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK)).list()[0]
	require.Equal(t, true, paused["needs_sign_in"])
	require.Equal(t, true, paused["has_password"])
	require.Equal(t, "password_refused", paused["sign_in_paused"])
	require.Contains(t, paused["last_sync_error"], "did not accept the kept password")

	// The scheduler leaves a paused password alone. The due list spans every
	// space, so the check is on this account alone.
	due, err := l.env.DB.ListMerchantAccountsDue(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	for _, one := range due {
		require.NotEqual(t, account, one.ID.String(), "a paused password is not tried on a timer")
	}

	// Forgetting it keeps the session and lifts the pause.
	forgot := requireNoSecret(t, l.alex.del(base+"/credential").requireStatus(http.StatusOK)).json()
	require.Equal(t, false, forgot["has_password"])
	require.Equal(t, false, forgot["has_totp"])
	require.Equal(t, "", forgot["sign_in_paused"])
	require.Equal(t, true, forgot["connected"])
}

func TestTheAssistantCannotSignInToAMerchantOrTouchItsPasswordOrSession(t *testing.T) {
	for _, route := range dispatchableRoutes() {
		path := route.Path()
		if !strings.HasPrefix(path, "/merchants/{merchant}/accounts/{id}") {
			continue
		}
		for _, denied := range []string{"/sign-in", "/credential", "/session"} {
			require.NotContains(t, path, denied, "%s %s is reachable from an in-process call",
				route.Method, path)
		}
	}
	const account = "/merchants/amazon/accounts/8c6b1f33-0000-4000-8000-000000000001"
	for _, step := range []string{"/sign-in", "/sign-in/s1/answer", "/sign-in/s1/complete", "/credential", "/session"} {
		require.Error(t, refuseDeniedPath(account+step), step)
	}
	require.NoError(t, refuseDeniedPath(account+"/pull"), "an update is a person's to approve, and allowed")
}

// A merchant login's choice of second factor: kept on the account with the
// sign-in, said back as a word, handed to the pull, and refused when it is
// not one of the three.
func TestAMerchantSecondFactorChoiceIsKeptAndHandedToThePull(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeAgent(t)
	agent.export = agentExport()
	withAgent(l, agent)
	account := merchantAccount(l, "Alex")
	base := "/merchants/amazon/accounts/" + account

	l.alex.post(base+"/sign-in", map[string]any{
		"email": "alex@example.com", "password": merchantPassword,
		"second_factor": "carrier-pigeon",
	}).requireStatus(http.StatusUnprocessableEntity)
	l.alex.post(base+"/sign-in", map[string]any{
		"email": "alex@example.com", "password": merchantPassword,
		"second_factor": "sms",
	}).requireStatus(http.StatusUnprocessableEntity)

	requireNoSecret(t, l.alex.post(base+"/sign-in", map[string]any{
		"email": "alex@example.com", "password": merchantPassword,
		"totp_secret": merchantAuthKey, "second_factor": "totp",
	}).requireStatus(http.StatusOK))
	requireNoSecret(t, l.alex.post(base+"/sign-in/s1/answer", map[string]any{"code": "123456"}).
		requireStatus(http.StatusOK))
	saved := requireNoSecret(t, l.alex.post(base+"/sign-in/s1/complete", nil).requireStatus(http.StatusOK)).json()
	awaitMerchantPull(t, account)
	require.Equal(t, "totp", saved["second_factor"])

	agent.mu.Lock()
	agent.wall = true
	agent.mu.Unlock()
	requireNoSecret(t, l.alex.post(base+"/pull", nil).requireStatus(http.StatusOK))
	handed := agent.credentials[len(agent.credentials)-1]
	require.NotNil(t, handed)
	require.Equal(t, "totp", string(handed.SecondFactor))
}
