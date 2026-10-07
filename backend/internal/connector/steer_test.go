package connector

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The developer steers, over a sign-in with a stub page behind it. The fake
// module carries Erie's id, so the steer is held inside the catalogue's home.

// erieHome is the catalogue's own value, read rather than repeated.
const erieHome = "https://www.erieinsurance.com"

// steerSession is a live sign-in standing on the provider's own site.
func steerSession(t *testing.T, page *browser.StubPage) (Bills, string) {
	t.Helper()
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateInteractive}, nil
	}
	engine, _ := testLiveEngine(t, module, page)
	where := page.Location
	state, err := engine.StartLiveConnect(context.Background(), "erie", "connection-1", "", 900, 700)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.CancelSignIn(context.Background(), state.SessionID) })
	// Opening the sign-in took the browser to the module's own login address;
	// the person has since signed in and is standing where the test put them.
	page.Location = where
	return engine, state.SessionID
}

func TestASteerGoesToTheProvidersOwnPagesAndNowhereElse(t *testing.T) {
	page := &browser.StubPage{Location: erieHome + "/account/summary"}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		return map[string]any{"text": "Your policies", "elements": []any{}}, nil
	}
	engine, session := steerSession(t, page)

	steered, err := engine.SteerTo(context.Background(), session, "/account/billing")
	require.NoError(t, err)
	require.Equal(t, "erie", steered.Provider)
	require.Equal(t, "Your policies", steered.Text)
	require.Equal(t, erieHome+"/account/billing", page.Visited[len(page.Visited)-1])
	require.NotNil(t, steered.Requests, "always a list, never null")

	// An address off the provider's site is a session handed over, and the
	// refusal names where the browser stays.
	_, err = engine.SteerTo(context.Background(), session, "https://example.test/steal")
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
	require.Contains(t, err.Error(), "www.erieinsurance.com")
	require.Len(t, page.Visited, 2, "nothing was navigated to")
}

func TestASteerReadsTheMarkupWithNoFieldValuesInIt(t *testing.T) {
	page := &browser.StubPage{Location: erieHome + "/login"}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		// What the in-page reader answers, with the redaction not yet applied:
		// the engine's own copy of the rule is what this holds.
		return []any{map[string]any{
			"tag": "input",
			"attributes": map[string]any{
				"type": "password", "name": "password",
				"value": "invented-secret", "data-csrf-token": "abc123",
			},
			"text": "Password",
			"html": `<input type="password" VALUE="invented-secret">`,
		}}, nil
	}
	engine, session := steerSession(t, page)

	found, err := engine.SteerDOM(context.Background(), session, `input[type="password"]`, 5)
	require.NoError(t, err)
	require.Equal(t, 1, found.Count)
	require.Equal(t, map[string]string{"type": "password", "name": "password"},
		found.Elements[0].Attributes)
	require.Equal(t, `<input type="password" value="">`, found.Elements[0].HTML)

	_, err = engine.SteerDOM(context.Background(), session, "", 5)
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
}

func TestASteerPressesTheControlItsWordsNameAndSaysSoWhenThereIsNone(t *testing.T) {
	page := &browser.StubPage{Location: erieHome + "/account/billing"}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		return map[string]any{"text": "Statements", "elements": []any{}}, nil
	}
	var pressed *regexp.Regexp
	page.OnClickText = func(selectors string, pattern *regexp.Regexp) (bool, error) {
		pressed = pattern
		return pattern.MatchString("View bill details"), nil
	}
	engine, session := steerSession(t, page)

	steered, err := engine.SteerClick(context.Background(), session, "View bill details", "")
	require.NoError(t, err)
	require.Equal(t, "Statements", steered.Text)
	require.True(t, pressed.MatchString("VIEW\nBILL   DETAILS"))

	// A control that never appears is a 404 naming what was looked for, after
	// the grace an SPA's own painting needs.
	_, err = engine.SteerClick(context.Background(), session, "Pay in full", "")
	require.ErrorIs(t, err, provider.ErrAgentNotFound)
	require.Contains(t, err.Error(), `"Pay in full"`)
	require.Equal(t, steerWait, page.Slept, "it waited the grace out once and then gave up")

	_, err = engine.SteerClick(context.Background(), session, "", "")
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
}

func TestASteeredFetchIsMadeByThePageAndStaysOnTheSite(t *testing.T) {
	page := &browser.StubPage{Location: erieHome + "/account/summary"}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		return map[string]any{
			"status": float64(200), "content_type": "application/json",
			"text": `{"bills":[]}`,
		}, nil
	}
	engine, session := steerSession(t, page)

	answered, err := engine.SteerFetch(context.Background(), session,
		provider.BillSignInFetchRequest{URL: "/api/bills", Method: "get"})
	require.NoError(t, err)
	require.Equal(t, erieHome+"/api/bills", answered.URL)
	require.Equal(t, 200, answered.Status)
	require.Equal(t, `{"bills":[]}`, answered.Text)

	_, err = engine.SteerFetch(context.Background(), session,
		provider.BillSignInFetchRequest{URL: "https://example.test/api"})
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
}

func TestASteerNeedsASessionWithAPage(t *testing.T) {
	engine := testEngine(t, newFakeAPI(), nil)
	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "alliant", Profile: "connection-1", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.CancelSignIn(context.Background(), state.SessionID) })

	_, err = engine.SteerTo(context.Background(), state.SessionID, "/x")
	require.ErrorIs(t, err, provider.ErrAgentConflict)

	_, err = engine.SteerTo(context.Background(), "no-such-session", "/x")
	require.ErrorIs(t, err, provider.ErrAgentNotFound)
}
