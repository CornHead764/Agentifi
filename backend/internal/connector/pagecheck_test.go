package connector

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A page check in Camoufox is never answered by the engine: a sign-in a person
// is watching parks on it with the page's picture and goes on once the person
// has ticked it; one nobody watches stops and says why.

// firefoxBill is the fake browser provider run in Camoufox.
type firefoxBill struct{ *fakeBrowser }

func (firefoxBill) RunsInFirefox() bool { return true }

// checkedSignIn is a Camoufox sign-in whose page shows a pending check until
// done says it has cleared, and whose password form signs in when filled.
type checkedSignIn struct {
	engine  Bills
	module  firefoxBill
	page    *browser.StubPage
	surface *browser.StubSurface
	cleared atomic.Bool
	signed  atomic.Bool
}

func newCheckedSignIn(t *testing.T) *checkedSignIn {
	t.Helper()
	c := &checkedSignIn{
		module:  firefoxBill{newFakeBrowser()},
		page:    &browser.StubPage{Firefox: true, Location: "https://example.test/login"},
		surface: browser.NewStubSurface("https://example.test/login"),
	}
	c.page.OnEvaluate = func(script string, _ any) (any, error) {
		if strings.Contains(script, "return agentifiPageCheck(); }") && !c.cleared.Load() {
			return "pending", nil
		}
		return "", nil
	}
	c.page.OnFill = func(selector, value string) error {
		if value == "invented" {
			c.signed.Store(true)
		}
		return nil
	}
	c.module.classify = func(browser.Page) (billers.State, error) {
		if c.signed.Load() {
			return billers.State{State: billers.StateSignedIn}, nil
		}
		return billers.State{State: billers.StatePassword}, nil
	}
	c.engine = testEngine(t, c.module, c.page)
	t.Cleanup(c.engine.CloseAll)
	c.engine.Sleep = func(time.Duration) { time.Sleep(time.Millisecond) }
	opened := c.engine.Open
	c.engine.Open = func(open agent.Open) (*OpenBrowser, error) {
		browserOpened, err := opened(open)
		if err != nil {
			return nil, err
		}
		browserOpened.Live = func() (*browser.LiveView, error) {
			return browser.NewLiveView(c.surface, browser.Size{Width: 1280, Height: 720}), nil
		}
		return browserOpened, nil
	}
	c.engine.OpenFirefox = c.engine.Open
	return c
}

func (c *checkedSignIn) start(t *testing.T) provider.BillConnectState {
	t.Helper()
	state, err := c.engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-1", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	return state
}

// until polls the sign-in as the dialog does, until it is in one of states.
func (c *checkedSignIn) until(t *testing.T, id string, states ...string) provider.BillConnectState {
	t.Helper()
	for range 1000 {
		state, err := c.engine.ConnectStatus(context.Background(), id)
		require.NoError(t, err)
		for _, want := range states {
			if state.State == want {
				return state
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the sign-in never reached %v", states)
	return provider.BillConnectState{}
}

func TestASignInParksOnAPageCheckInCamoufoxAndGoesOnOnceThePersonTicksIt(t *testing.T) {
	c := newCheckedSignIn(t)
	state := c.start(t)

	parked := c.until(t, state.SessionID, provider.BillConnectInteractive)
	require.Equal(t, "Tick the box that says you're human, then the sign-in continues.", parked.Prompt)
	require.NotEmpty(t, parked.Image, "the person is shown the page")
	require.Equal(t, 1280, parked.Width)
	require.Equal(t, 720, parked.Height)
	require.False(t, c.signed.Load(), "nothing is typed while the check is up")

	// The person's click arrives at the page's pixels, and is what clears it.
	require.NoError(t, c.engine.SignInInput(context.Background(), state.SessionID, []provider.BillLiveInput{
		{Type: "click", X: 412, Y: 305},
	}))
	require.Equal(t, []string{"click 412,305 left x1"}, c.surface.Did())
	c.cleared.Store(true)

	landed := c.until(t, state.SessionID, provider.BillConnectSignedIn, provider.BillConnectFailed)
	require.Equal(t, provider.BillConnectSignedIn, landed.State)
	require.True(t, c.signed.Load(), "the form is filled and sent once the check has cleared")
}

func TestAnInputIsRefusedUnlessTheSignInIsWaitingForOne(t *testing.T) {
	c := newCheckedSignIn(t)
	c.cleared.Store(true)
	state := c.start(t)
	c.until(t, state.SessionID, provider.BillConnectSignedIn)

	err := c.engine.SignInInput(context.Background(), state.SessionID, []provider.BillLiveInput{{Type: "click", X: 1, Y: 1}})
	require.ErrorIs(t, err, provider.ErrAgentConflict)
	require.Empty(t, c.surface.Did())
}

func TestAParkedSignInThatIsNeverTickedFailsAfterTheWait(t *testing.T) {
	c := newCheckedSignIn(t)
	var ticks atomic.Int64
	c.engine.Now = func() time.Time { return time.Unix(0, 0).Add(time.Duration(ticks.Add(1)) * time.Minute) }
	state := c.start(t)

	failed := c.until(t, state.SessionID, provider.BillConnectFailed)
	require.Equal(t, pageCheckUntickedErr, failed.Error)
	require.False(t, c.signed.Load())
}

func TestClosingTheDialogEndsAParkedSignInWithoutTypingAnything(t *testing.T) {
	c := newCheckedSignIn(t)
	state := c.start(t)
	c.until(t, state.SessionID, provider.BillConnectInteractive)

	require.NoError(t, c.engine.CancelSignIn(context.Background(), state.SessionID))

	require.Equal(t, 0, c.engine.Sessions())
	time.Sleep(20 * time.Millisecond)
	require.False(t, c.signed.Load())
}

func TestAnUnattendedPullStopsAtAPageCheckAndSaysAPersonMustTickIt(t *testing.T) {
	c := newCheckedSignIn(t)
	c.module.fetchBills = func(billers.Call) (billers.Pull, error) {
		return billers.Pull{NeedsSignIn: true, Reason: "the profile has been signed out"}, nil
	}

	out, err := c.engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`,
		Credential:   map[string]string{"username": "someone@example.test", "password": "invented"},
	})
	require.NoError(t, err)
	require.True(t, out.NeedsSignIn)
	require.True(t, out.PageCheck, "the connection is paused for a person")
	require.Contains(t, out.Reason, "only a person can tick")
	require.Contains(t, strings.Join(out.Notes, "\n"), "nobody was at this sign-in")
	require.False(t, c.signed.Load(), "the kept password is not typed behind a check")
	require.Empty(t, c.surface.Did(), "the engine never clicks a check itself")
}

func TestAParkedPageCheckTakesClicksButNoKeys(t *testing.T) {
	c := newCheckedSignIn(t)
	state := c.start(t)
	c.until(t, state.SessionID, provider.BillConnectInteractive)

	for _, refused := range []provider.BillLiveInput{
		{Type: "key", Key: "Enter"},
		{Type: "text", Text: "typed"},
		{Type: "keydown", Key: "Tab"},
		{Type: "wheel", DY: 100},
	} {
		err := c.engine.SignInInput(context.Background(), state.SessionID, []provider.BillLiveInput{
			{Type: "click", X: 1, Y: 1}, refused,
		})
		require.ErrorIs(t, err, provider.ErrAgentConflict, refused.Type)
	}
	require.Empty(t, c.surface.Did(), "a batch with a refused event plays none of it")
}

func TestACheckDrawnOnceTheFormIsFilledAlsoWaitsForThePerson(t *testing.T) {
	c := newCheckedSignIn(t)
	var filled atomic.Bool
	c.page.OnEvaluate = func(script string, _ any) (any, error) {
		if strings.Contains(script, "return agentifiPageCheck(); }") && filled.Load() && !c.cleared.Load() {
			return "pending", nil
		}
		return "", nil
	}
	c.page.OnFill = func(selector, value string) error {
		if value == "invented" {
			filled.Store(true)
			if c.cleared.Load() {
				c.signed.Store(true)
			}
		}
		return nil
	}
	state := c.start(t)

	parked := c.until(t, state.SessionID, provider.BillConnectInteractive, provider.BillConnectFailed)
	require.Equal(t, provider.BillConnectInteractive, parked.State, parked.Error)
	require.Equal(t, pageCheckPrompt, parked.Prompt)

	require.NoError(t, c.engine.SignInInput(context.Background(), state.SessionID, []provider.BillLiveInput{
		{Type: "click", X: 40, Y: 40},
	}))
	c.cleared.Store(true)

	landed := c.until(t, state.SessionID, provider.BillConnectSignedIn, provider.BillConnectFailed)
	require.Equal(t, provider.BillConnectSignedIn, landed.State, landed.Error)
}
