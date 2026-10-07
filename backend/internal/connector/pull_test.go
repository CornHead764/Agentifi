package connector

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The kept password at a session the provider has let lapse: tried once, and
// what came of it said plainly.

var keptLogin = map[string]string{"username": "someone@example.test", "password": "invented"}

// refusedUntilSignedIn is a walk that answers ErrNeedsSignIn on the kept
// token and bills on the one a sign-in minted, which carries `answered`.
func refusedUntilSignedIn(call billers.Call) (billers.Pull, error) {
	var shape struct {
		Answered bool `json:"answered"`
	}
	_ = json.Unmarshal(call.Session, &shape)
	if !shape.Answered {
		return billers.Pull{}, billers.ErrNeedsSignIn
	}
	return billers.Pull{Bills: []billers.Bill{fakeBill()}}, nil
}

func keptToken() string {
	return string(fakeSession("fake-token", time.Now().Add(time.Hour), false))
}

func TestAWalkThatSaysNeedsSignInIsSignedInPastOnceWithTheKeptPassword(t *testing.T) {
	module := newFakeAPI()
	module.fetchBills = refusedUntilSignedIn
	module.authenticate = func(billers.Credentials) billers.SignIn {
		return billers.SignIn{Session: fakeSession("fake-token", time.Now().Add(time.Hour), true)}
	}
	engine := testEngine(t, module, nil)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "alliant", SessionState: keptToken(), Subaccounts: []string{"one"}, Credential: keptLogin,
	})
	require.NoError(t, err)
	require.False(t, out.NeedsSignIn)
	require.False(t, out.PasswordRefused)
	require.Equal(t, 1, module.authenticated)
	require.Len(t, out.Bills, 1)
	require.Contains(t, string(out.SessionState), `"answered":true`, "the session the password opened is the one to keep")
}

func TestAPasswordTheProviderRefusesIsTriedOnceAndSaidSo(t *testing.T) {
	module := newFakeAPI()
	module.fetchBills = refusedUntilSignedIn
	module.authenticate = func(billers.Credentials) billers.SignIn {
		return billers.SignIn{Failed: "Invalid username or password"}
	}
	engine := testEngine(t, module, nil)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "alliant", SessionState: keptToken(), Subaccounts: []string{"one"}, Credential: keptLogin,
	})
	require.NoError(t, err)
	require.True(t, out.NeedsSignIn)
	require.True(t, out.PasswordRefused)
	require.False(t, out.CodeNeeded)
	require.Equal(t, 1, module.authenticated)
	require.Equal(t, 1, module.fetched, "a refused password is not followed by another walk")
	require.Equal(t, "Alliant Energy did not accept the kept password (Invalid username or password)", out.Reason)
}

func TestWithNoKeptPasswordARefusedSessionIsASignInNotARefusal(t *testing.T) {
	module := newFakeAPI()
	module.fetchBills = refusedUntilSignedIn
	engine := testEngine(t, module, nil)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "alliant", SessionState: keptToken(), Subaccounts: []string{"one"},
	})
	require.NoError(t, err)
	require.True(t, out.NeedsSignIn)
	require.False(t, out.PasswordRefused)
	require.Equal(t, 0, module.authenticated)
	require.Equal(t, "Alliant Energy refused the kept session", out.Reason)
}

func TestAChallengeAtTheKeptPasswordParksRatherThanRefusing(t *testing.T) {
	module := newFakeAPI()
	module.fetchBills = refusedUntilSignedIn
	module.authenticate = func(billers.Credentials) billers.SignIn {
		return billers.SignIn{Challenge: &billers.Challenge{
			State: billers.StateOTP, Method: "sms", Prompt: "the code we texted you",
			Answer: func(context.Context, string, *billers.Notes) billers.SignIn { return billers.SignIn{} },
		}}
	}
	engine := testEngine(t, module, nil)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "alliant", SessionState: keptToken(), Subaccounts: []string{"one"}, Credential: keptLogin,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Challenge)
	require.False(t, out.NeedsSignIn)
	require.False(t, out.PasswordRefused)
	require.False(t, out.CodeNeeded, "a parked challenge is answered through its own row")
	require.Equal(t, 1, module.authenticated)
}

func TestAProviderThatCouldNotBeReachedAtTheSignInIsAFailureNotARefusal(t *testing.T) {
	module := newFakeAPI()
	module.fetchBills = refusedUntilSignedIn
	unreachable := errors.New("dial tcp: i/o timeout")
	module.authenticate = func(billers.Credentials) billers.SignIn {
		return billers.SignIn{Failed: unreachable.Error(), Err: unreachable}
	}
	engine := testEngine(t, module, nil)

	_, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "alliant", SessionState: keptToken(), Subaccounts: []string{"one"}, Credential: keptLogin,
	})
	require.ErrorIs(t, err, unreachable)
	require.Equal(t, 1, module.authenticated)
}

func TestARefusedPasswordAfterAnExpiredOrMissingSessionIsARefusal(t *testing.T) {
	for name, session := range map[string]string{
		"expired": string(fakeSession("fake-token", time.Now().Add(-time.Hour), false)),
		"none":    "",
	} {
		t.Run(name, func(t *testing.T) {
			module := newFakeAPI()
			module.refresh = func(billers.Call) (billers.Session, bool, string) {
				return nil, true, "Unauthorized Access"
			}
			module.authenticate = func(billers.Credentials) billers.SignIn {
				return billers.SignIn{Failed: "Invalid username or password"}
			}
			engine := testEngine(t, module, nil)

			out, err := engine.Pull(context.Background(), provider.BillPullRequest{
				Provider: "alliant", SessionState: session, Subaccounts: []string{"one"}, Credential: keptLogin,
			})
			require.NoError(t, err)
			require.True(t, out.NeedsSignIn)
			require.True(t, out.PasswordRefused)
			require.Equal(t, 1, module.authenticated)
			require.Equal(t, 0, module.fetched)
			require.Contains(t, out.Reason, "did not accept the kept password")
		})
	}
}

// signInPage is a browser provider's sign-in as a stub: the password form at
// the sign-in address, the account once `accepts` says the password was
// right, and a blank tab anywhere else.
func signInPage(module *fakeBrowser, page *browser.StubPage, accepts func(string) bool) *int {
	typed := 0
	signedIn := false
	module.classify = func(page browser.Page) (billers.State, error) {
		switch {
		case signedIn:
			return billers.State{State: billers.StateSignedIn}, nil
		case page.URL() == module.SignInURL():
			return billers.State{State: billers.StatePassword}, nil
		}
		return billers.State{State: billers.StateInteractive}, nil
	}
	module.fetchBills = func(billers.Call) (billers.Pull, error) {
		if !signedIn {
			return billers.Pull{}, billers.ErrNeedsSignIn
		}
		return billers.Pull{Bills: []billers.Bill{fakeBill()}}, nil
	}
	page.OnFill = func(selector, value string) error {
		if value == keptLogin["password"] {
			typed++
			signedIn = accepts(value)
		}
		return nil
	}
	return &typed
}

func TestABrowserWalkThatSaysNeedsSignInGoesToTheFormAndSignsInOnce(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/account"}
	typed := signInPage(module, page, func(string) bool { return true })
	engine := testEngine(t, module, page)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`, Credential: keptLogin,
	})
	require.NoError(t, err)
	require.False(t, out.NeedsSignIn)
	require.Len(t, out.Bills, 1)
	require.Equal(t, 1, *typed)
	require.Contains(t, page.Visited, "https://example.test/login")
}

func TestABrowserProviderWithOnlyAKeptPasswordSignsInOnAFreshBrowser(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "about:blank"}
	typed := signInPage(module, page, func(string) bool { return true })
	engine := testEngine(t, module, page)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"}, Credential: keptLogin,
	})
	require.NoError(t, err)
	require.False(t, out.NeedsSignIn)
	require.Len(t, out.Bills, 1)
	require.Equal(t, 1, *typed)
	require.NotEmpty(t, out.SessionState, "the session the password opened is handed back to be kept")
}

func TestABrowserPullThatFailsCarriesThePageItFailedOn(t *testing.T) {
	covered := errors.New("something on the page covered the button")
	module := newFakeBrowser()
	module.fetchBills = func(billers.Call) (billers.Pull, error) { return billers.Pull{}, covered }
	page := &browser.StubPage{
		Location: "https://example.test/account",
		OnScreen: func() ([]byte, error) { return []byte("the account page"), nil },
	}
	engine := testEngine(t, module, page)

	_, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`,
	})
	require.ErrorIs(t, err, covered)
	require.Equal(t, []byte("the account page"), provider.ScreenshotOf(err))
}

func TestAnAPIPullThatFailsHasNoPageToShow(t *testing.T) {
	unreachable := errors.New("dial tcp: i/o timeout")
	module := newFakeAPI()
	module.fetchBills = func(billers.Call) (billers.Pull, error) { return billers.Pull{}, unreachable }
	engine := testEngine(t, module, nil)

	_, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "alliant", SessionState: keptToken(), Subaccounts: []string{"one"},
	})
	require.ErrorIs(t, err, unreachable)
	require.Nil(t, provider.ScreenshotOf(err))
}

func TestABrowserPasswordTheProviderRefusesIsTriedOnceAndNamesThePage(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/account"}
	typed := signInPage(module, page, func(string) bool { return false })
	engine := testEngine(t, module, page)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`, Credential: keptLogin,
	})
	require.NoError(t, err)
	require.True(t, out.NeedsSignIn)
	require.True(t, out.PasswordRefused)
	require.Equal(t, 1, module.fetched)
	require.GreaterOrEqual(t, *typed, 1)
	require.Contains(t, out.Reason, "Erie Insurance did not accept the kept password (stopped at the password page")
	require.NotContains(t, out.Reason, "kept session")
}

func TestABrowserSignInThatReachesACodeParksThePullForTheAnswerers(t *testing.T) {
	// A code the kept password reached is parked on the page that asked, so the
	// mailbox gets its chance; the answer then carries the same pull on.
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/login"}
	typed, answered := 0, false
	module.classify = func(browser.Page) (billers.State, error) {
		switch {
		case answered:
			return billers.State{State: billers.StateSignedIn}, nil
		case typed > 0:
			return billers.State{State: billers.StateOTP, Method: "sms", Prompt: "Enter the code we texted"}, nil
		}
		return billers.State{State: billers.StatePassword}, nil
	}
	module.fetchBills = func(billers.Call) (billers.Pull, error) {
		if !answered {
			return billers.Pull{}, billers.ErrNeedsSignIn
		}
		return billers.Pull{Bills: []billers.Bill{fakeBill()}}, nil
	}
	page.OnFill = func(selector, value string) error {
		switch value {
		case keptLogin["password"]:
			typed++
		case "246810":
			answered = true
		}
		return nil
	}
	engine := testEngine(t, module, page)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`, Credential: keptLogin,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Challenge, "a code is parked rather than reported")
	require.False(t, out.NeedsSignIn)
	require.False(t, out.CodeNeeded, "a parked code is answered through its own row")
	require.Equal(t, billers.StateOTP, out.Challenge.State)
	require.Equal(t, "sms", out.Challenge.Method)
	require.Equal(t, 1, typed)

	_, err = engine.AnswerConnect(context.Background(), out.Challenge.SessionID, "246810")
	require.NoError(t, err)
	resumed, err := engine.ResumePull(context.Background(), out.Challenge.SessionID)
	require.NoError(t, err)
	require.Nil(t, resumed.Challenge)
	require.False(t, resumed.NeedsSignIn)
	require.Len(t, resumed.Bills, 1)
	require.Equal(t, 1, typed, "the resumed pull does not type the password a second time")
}

func TestABrowserSignInThatReachesAPhoneTapIsACodeNeededNotARefusal(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/login"}
	typed := 0
	module.classify = func(browser.Page) (billers.State, error) {
		if typed > 0 {
			return billers.State{State: billers.StateApproval, Prompt: "Approve the sign-in on your phone"}, nil
		}
		return billers.State{State: billers.StatePassword}, nil
	}
	module.fetchBills = func(billers.Call) (billers.Pull, error) {
		return billers.Pull{}, billers.ErrNeedsSignIn
	}
	page.OnFill = func(selector, value string) error {
		if value == keptLogin["password"] {
			typed++
		}
		return nil
	}
	engine := testEngine(t, module, page)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`, Credential: keptLogin,
	})
	require.NoError(t, err)
	require.True(t, out.NeedsSignIn)
	require.False(t, out.PasswordRefused)
	require.True(t, out.CodeNeeded, "a tap nobody is there to make is not tried again unattended")
	require.Equal(t, 1, typed)
	require.Equal(t, "Erie Insurance took the kept password and then asked for a tap on a phone: "+
		"Approve the sign-in on your phone", out.Reason)
}
