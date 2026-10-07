package connector

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A panic under a module call is a failed pull, not a 500 with a stack in it.
// The panic used is playwright-go's argument serializer asserting a map.

func TestAPanicInAModuleIsAFailedPullAndNotACrash(t *testing.T) {
	module := newFakeAPI()
	module.fetchBills = func(billers.Call) (billers.Pull, error) {
		panic("interface conversion: interface {} is map[string]string, not map[string]interface {}")
	}
	engine := testEngine(t, module, nil)
	logged := &bytes.Buffer{}
	engine.Log = slog.New(slog.NewTextHandler(logged, &slog.HandlerOptions{Level: slog.LevelError}))

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "alliant", Profile: "connection-1",
		SessionState: string(fakeSession("fake-token", time.Now().Add(time.Hour), false)),
		Subaccounts:  []string{"one"},
	})

	require.Error(t, err)
	require.ErrorIs(t, err, provider.ErrAgentFailed)
	require.Contains(t, err.Error(), "Alliant Energy")
	require.Contains(t, err.Error(), "map[string]string")
	require.NotContains(t, err.Error(), "goroutine",
		"the stack is logged, never shown to the household")
	require.Empty(t, out.Bills)
	require.Equal(t, 0, engine.Sessions(), "the pull let its session go on the way out")

	require.Contains(t, logged.String(), "a connector panicked")
	require.Contains(t, logged.String(), "connector.TestAPanicInAModuleIsAFailedPullAndNotACrash",
		"the stack is logged once, at error level")
}

func TestAPanicInABrowserModulesPullStillClosesTheBrowser(t *testing.T) {
	module := newFakeBrowser()
	module.fetchBills = func(billers.Call) (billers.Pull, error) { panic("the page went away") }
	page := &browser.StubPage{Location: "https://example.test/account"}
	// testEngine fails the test if a browser it opened is not closed: a panic
	// must not leave Chromium on the profile.
	engine := testEngine(t, module, page)
	engine.Log = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	_, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9",
		SessionState: `{"cookies":[],"origins":[]}`, Subaccounts: []string{"one"},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "Erie Insurance")
	require.Contains(t, err.Error(), "the page went away")
	require.Equal(t, 0, engine.Sessions())
}
