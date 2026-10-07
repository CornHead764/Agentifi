package connector

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A sign-in a person started that never landed is reported once, with its
// page and its trail, however it ended. Every value here is invented.

type endings struct {
	mu   sync.Mutex
	seen []provider.BillSignInEnded
}

func (r *endings) keep(end provider.BillSignInEnded) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, end)
}

func (r *endings) all() []provider.BillSignInEnded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]provider.BillSignInEnded(nil), r.seen...)
}

// codePage is a provider that asks for a code once the password is in.
func codePage() *fakeBrowser {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateOTP, Method: "sms", Prompt: "Enter the code we texted"}, nil
	}
	return module
}

func startedSignIn(t *testing.T, engine Bills, prefer string) provider.BillConnectState {
	t.Helper()
	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
		SecondFactor: prefer,
	})
	require.NoError(t, err)
	return awaitSignIn(t, engine, state)
}

func TestASignInThatFailsIsReportedOnceWithItsPageAndTrail(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateFactor}, nil
	}
	module.chooseFactor = func(browser.Page) (agent.Factor, error) {
		return agent.Factor{Choices: []billers.FactorChoice{{Kind: "radio", Words: "Email"}}}, nil
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/mfa"))
	ended := &endings{}
	engine.SignInEnded = ended.keep

	state := startedSignIn(t, engine, "sms")
	require.Equal(t, billers.StateFailed, state.State)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))

	seen := ended.all()
	require.Len(t, seen, 1, "the failure is reported, and closing the dialog after it reports nothing more")
	require.Equal(t, state.SessionID, seen[0].SessionID)
	require.Equal(t, `a text message was chosen and not offered; offered "Email"`, seen[0].Detail)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("\x89PNG stub")), seen[0].Image)
	require.NotEmpty(t, seen[0].Trail)
	require.Equal(t, "failed", seen[0].Trail[len(seen[0].Trail)-1].Step)
}

func TestASignInThePersonClosesIsReportedWithWhereItStood(t *testing.T) {
	engine := testEngine(t, codePage(), readingPage("https://account.example.test/verify"))
	ended := &endings{}
	engine.SignInEnded = ended.keep

	state := startedSignIn(t, engine, "sms")
	require.Equal(t, billers.StateOTP, state.State)
	require.Empty(t, ended.all(), "a sign-in waiting for a code has not ended")
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))

	seen := ended.all()
	require.Len(t, seen, 1)
	require.Equal(t, "The sign-in was closed before it finished, while Erie Insurance was asking for a code.", seen[0].Detail)
	require.NotEmpty(t, seen[0].Image, "the page is taken before the browser closes")
	require.NotEmpty(t, seen[0].Trail)
}

func TestASignInNobodyCameBackToIsReportedWhenItIsReaped(t *testing.T) {
	engine := testEngine(t, codePage(), readingPage("https://account.example.test/verify"))
	ended := &endings{}
	engine.SignInEnded = ended.keep
	now := time.Now()
	engine.Now = func() time.Time { return now }

	state := startedSignIn(t, engine, "")
	require.Equal(t, billers.StateOTP, state.State)
	now = now.Add(browser.SessionTTL + time.Minute)
	engine.Reap()

	seen := ended.all()
	require.Len(t, seen, 1)
	require.Equal(t, "The sign-in was left unfinished, while Erie Insurance was asking for a code.", seen[0].Detail)
}

func TestASignInThatLandsReportsNothing(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateSignedIn}, nil
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/account"))
	ended := &endings{}
	engine.SignInEnded = ended.keep

	state := startedSignIn(t, engine, "")
	require.Equal(t, billers.StateSignedIn, state.State)
	_, err := engine.CompleteConnect(context.Background(), state.SessionID)
	require.NoError(t, err)
	require.Empty(t, ended.all())
}
