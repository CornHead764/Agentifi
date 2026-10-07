package connector

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A live sign-in: the person signs in through a screencast of the provider's
// own page, driven here through a stub page and a fake screencast.

func TestALiveSignInOpensAtTheProvidersForm(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateInteractive}, nil
	}
	page := &browser.StubPage{Location: "https://example.test/login"}
	engine, fakes := testLiveEngine(t, module, page)

	state, err := engine.StartLiveConnect(context.Background(), "erie", "connection-1", "", 4000, 200)
	require.NoError(t, err)
	require.Equal(t, "erie", state.Provider)
	require.Equal(t, billers.StateInteractive, state.State)
	require.NotEmpty(t, state.Prompt, "the dialog says what the person is being asked to do")
	// The size the dialog draws at, clamped to what it can draw.
	require.Equal(t, 1280, state.Width)
	require.Equal(t, 480, state.Height)
	require.Equal(t, []string{"https://example.test/login"}, page.Visited)
	require.Equal(t, []string{"Page.startScreencast"}, fakes.Cast.Sent)

	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
	require.Equal(t, 0, engine.Sessions())
	// The screencast stops with the browser rather than painting into a
	// session nobody is watching.
	require.Contains(t, fakes.Cast.Sent, "Page.stopScreencast")
	require.Contains(t, fakes.Cast.Sent, "detach")
}

func TestAProviderWithNoSignInPageHasNoLiveBrowser(t *testing.T) {
	engine := testEngine(t, newFakeAPI(), nil)
	_, err := engine.StartLiveConnect(context.Background(), "alliant", "connection-1", "", 900, 700)
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
	require.Contains(t, err.Error(), "send a username and password")
	require.Equal(t, 0, engine.Sessions())
}

func TestALiveSignInIsNeverSettledIntoAFailure(t *testing.T) {
	module := newFakeBrowser()
	// A factor page: in a live browser the person presses one, so there is
	// nothing for the dialog to say.
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateFactor}, nil
	}
	page := &browser.StubPage{Location: "https://example.test/mfa"}
	engine, _ := testLiveEngine(t, module, page)
	state, err := engine.StartLiveConnect(context.Background(), "erie", "connection-1", "", 900, 700)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.CancelSignIn(context.Background(), state.SessionID) })

	where, err := engine.ConnectStatus(context.Background(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, billers.StateInteractive, where.State)
	require.Empty(t, where.Error)
}

func TestALiveSignInIsCompletedOnlyOnceTheProviderSaysItIsIn(t *testing.T) {
	module := newFakeBrowser()
	in := false
	module.classify = func(browser.Page) (billers.State, error) {
		if in {
			return billers.State{State: billers.StateSignedIn}, nil
		}
		return billers.State{State: billers.StateInteractive, Prompt: "still at the form"}, nil
	}
	page := &browser.StubPage{Location: "https://example.test/login"}
	engine, _ := testLiveEngine(t, module, page)
	state, err := engine.StartLiveConnect(context.Background(), "erie", "connection-1", "", 900, 700)
	require.NoError(t, err)

	_, err = engine.CompleteConnect(context.Background(), state.SessionID)
	require.ErrorIs(t, err, provider.ErrAgentConflict)
	require.Contains(t, err.Error(), "the sign-in is not finished")
	require.Equal(t, 1, engine.Sessions(), "a sign-in refused is a sign-in still open")

	in = true
	done, err := engine.CompleteConnect(context.Background(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, "erie", done.Provider)
	require.JSONEq(t, `{"cookies":[],"origins":[]}`, string(done.SessionState))
	// It stood on the landing page before the jar was read, so the cookies the
	// site sets after a sign-in are in it.
	require.Equal(t, "https://example.test/account", page.Visited[len(page.Visited)-1])
	require.Equal(t, 0, engine.Sessions())
}

func TestALiveSignInNobodyCameBackToIsReaped(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateInteractive}, nil
	}
	page := &browser.StubPage{Location: "https://example.test/login"}
	engine, fakes := testLiveEngine(t, module, page)
	at := engine.now()
	engine.Now = func() time.Time { return at }

	state, err := engine.StartLiveConnect(context.Background(), "erie", "connection-1", "", 900, 700)
	require.NoError(t, err)
	require.Equal(t, 1, engine.Sessions())

	at = at.Add(SessionTTL + time.Minute)
	engine.Reap()
	require.Equal(t, 0, engine.Sessions())
	require.Contains(t, fakes.Cast.Sent, "Page.stopScreencast")

	_, err = engine.ConnectStatus(context.Background(), state.SessionID)
	require.ErrorIs(t, err, provider.ErrAgentNotFound)
}
