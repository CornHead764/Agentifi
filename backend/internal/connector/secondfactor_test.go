package connector

import (
	"context"
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/browser/agent"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The login's own choice of second factor, carried to the page that asks.
// Every value here is invented.

// An unattended pull of a login whose codes come by e-mail: the factor page is
// given the preference, and the code box after it (which names no channel) is
// parked for the mailbox rather than filled with a minted code that cannot be
// it.
func TestAPullHonoursALoginsEmailedCodeAndMintsNothingIntoIt(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/login"}
	typed, chosen := 0, false
	module.classify = func(browser.Page) (billers.State, error) {
		switch {
		case chosen:
			return billers.State{State: billers.StateOTP, Prompt: "Enter the code"}, nil
		case typed > 0:
			return billers.State{State: billers.StateFactor}, nil
		}
		return billers.State{State: billers.StatePassword}, nil
	}
	module.chooseFactor = func(browser.Page) (agent.Factor, error) {
		chosen = true
		return agent.Factor{Kind: "email", Chose: "Email me a code",
			Choices: []billers.FactorChoice{{Kind: "radio", Words: "Text me"}, {Kind: "radio", Words: "Email me a code"}}}, nil
	}
	module.fetchBills = func(billers.Call) (billers.Pull, error) { return billers.Pull{}, billers.ErrNeedsSignIn }
	var filled []string
	page.OnFill = func(_, value string) error {
		if value == keptLogin["password"] {
			typed++
		}
		filled = append(filled, value)
		return nil
	}
	engine := testEngine(t, module, page)
	credential := map[string]string{}
	for key, value := range keptLogin {
		credential[key] = value
	}
	credential["totp_secret"] = "JBSWY3DPEHPK3PXP"

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`, Credential: credential, SecondFactor: "email",
	})

	require.NoError(t, err)
	defer engine.CloseAll()
	require.Equal(t, []string{"email"}, module.preferred, "the factor page was given the login's choice")
	require.NotNil(t, out.Challenge, "the code is parked for the mailbox")
	require.Equal(t, billers.StateOTP, out.Challenge.State)
	for _, value := range filled {
		require.NotRegexp(t, `^\d{6}$`, value, "no code was minted into the box")
	}
}

// A login whose chosen factor was not on the menu takes nothing else: the
// sign-in stops, naming the choice and what the page offered, and the trail
// says so on the line that showed the menu.
func TestAChosenFactorTheMenuDidNotOfferStopsTheSignInNamingBoth(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateFactor}, nil
	}
	module.chooseFactor = func(browser.Page) (agent.Factor, error) {
		return agent.Factor{
			Choices: []billers.FactorChoice{{Kind: "radio", Words: "Text message"}, {Kind: "radio", Words: "Passkey"}},
		}, nil
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/mfa"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
		SecondFactor: "email",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)
	require.Equal(t, billers.StateFailed, state.State)
	require.Contains(t, state.Prompt, "a code sent by e-mail")
	require.Contains(t, state.Prompt, `"Text message", "Passkey"`)
	require.Equal(t, `a code sent by e-mail was chosen and not offered; offered "Text message", "Passkey"`, state.Error)
	require.Equal(t, []string{"email"}, module.preferred, "the menu was asked once, with the choice")

	require.Equal(t, "a code sent by e-mail was chosen for this login and not offered, so nothing was taken",
		state.Trail[0].Note)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

func TestAPreferenceTheMenuHonouredSaysNothing(t *testing.T) {
	require.Empty(t, unmetPreference("", agent.Factor{Kind: "sms", Choices: []billers.FactorChoice{{}}}))
	require.Empty(t, unmetPreference("totp", agent.Factor{Kind: "totp", Choices: []billers.FactorChoice{{}}}))
	require.Equal(t, "an authenticator app was chosen for this login and not offered, so nothing was taken",
		unmetPreference("totp", agent.Factor{Choices: []billers.FactorChoice{{Words: "Passkey"}}}))
	require.Equal(t, "an authenticator app was chosen for this login; the provider's own order took a code sent by e-mail",
		unmetPreference("totp", agent.Factor{Kind: "email", Choices: []billers.FactorChoice{{Words: "Email"}}}))
}

// A login whose chosen way is offered: the menu is given the choice, and the
// code box after it is the person's to answer in the dialog. An authenticator
// with no setup key kept is answered the same way as a text.
func TestAChosenWayWithNoKeyAsksThePersonForTheCode(t *testing.T) {
	for _, tc := range []struct {
		prefer, method, words string
	}{
		{prefer: "totp", method: "totp", words: "Authenticator app"},
		{prefer: "", method: "sms", words: "Text message"},
	} {
		module := newFakeBrowser()
		chosen := false
		module.classify = func(browser.Page) (billers.State, error) {
			if chosen {
				return billers.State{State: billers.StateOTP, Method: tc.method, Prompt: "Enter the code"}, nil
			}
			return billers.State{State: billers.StateFactor}, nil
		}
		module.chooseFactor = func(browser.Page) (agent.Factor, error) {
			chosen = true
			return agent.Factor{Kind: tc.method, Chose: tc.words, Choices: []billers.FactorChoice{
				{Kind: "radio", Words: "Email"}, {Kind: "radio", Words: tc.words},
			}}, nil
		}
		engine := testEngine(t, module, readingPage("https://account.example.test/mfa"))

		state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
			Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
			SecondFactor: tc.prefer,
		})
		require.NoError(t, err)
		state = awaitSignIn(t, engine, state)
		require.Equal(t, billers.StateOTP, state.State, "%s: the dialog asks for the code", tc.prefer)
		require.Equal(t, tc.method, state.Method)
		require.Equal(t, []string{tc.prefer}, module.preferred)
		require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
	}
}
