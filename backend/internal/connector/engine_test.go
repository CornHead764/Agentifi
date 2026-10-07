package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The engine as bills drive it, against fake modules: no browser and no network.

func TestATypedConnectSignsInListsTheAccountsAndCompletes(t *testing.T) {
	module := newFakeAPI()
	module.subaccounts = []billers.Subaccount{
		{ExternalID: "one", Label: "One", MaskedNumber: "••••1234"},
		{ExternalID: "two", Label: "Two", MaskedNumber: "••••5678"},
	}
	engine := testEngine(t, module, nil)

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "alliant", Profile: "connection-1", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	require.Equal(t, "alliant", state.Provider)
	// Two billed accounts, so somebody picks.
	require.Equal(t, "accounts", state.State)
	require.Len(t, state.Accounts, 2)
	require.Equal(t, "••••1234", state.Accounts[0].MaskedNumber)
	require.Empty(t, state.Method)

	again, err := engine.ConnectStatus(context.Background(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, "accounts", again.State)

	done, err := engine.CompleteConnect(context.Background(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, "alliant", done.Provider)
	require.Equal(t, "Alex", done.AccountHint)
	require.Len(t, done.Subaccounts, 2, "the walk is not repeated at complete")
	require.Equal(t, "fake-token", billers.Session(done.SessionState).Kind())

	// The session is gone once it has been completed.
	_, err = engine.ConnectStatus(context.Background(), state.SessionID)
	require.ErrorIs(t, err, provider.ErrAgentNotFound)
	require.Equal(t, 0, engine.Sessions())
}

func TestASignInCanBeGivenUpWhichFreesTheProfileAtOnce(t *testing.T) {
	engine := testEngine(t, newFakeAPI(), nil)
	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "alliant", Profile: "connection-1", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)

	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
	err = engine.CancelSignIn(context.Background(), state.SessionID)
	require.ErrorIs(t, err, provider.ErrAgentNotFound)
	require.Equal(t, 0, engine.Sessions())
}

func TestARefusedSignInIsFailedAndKeepsNothing(t *testing.T) {
	module := newFakeAPI()
	module.authenticate = func(billers.Credentials) billers.SignIn {
		return billers.SignIn{Failed: "Invalid username or password"}
	}
	engine := testEngine(t, module, nil)

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "alliant", Profile: "connection-1", Username: "someone@example.test", Password: "wrong",
	})
	require.NoError(t, err)
	require.Equal(t, billers.StateFailed, state.State)
	require.Equal(t, "Invalid username or password", state.Error)
	require.Equal(t, 0, engine.Sessions(), "a refused sign-in leaves nothing open")
}

func TestAnUnknownProviderIsRefusedBeforeAnythingIsAskedOfIt(t *testing.T) {
	engine := testEngine(t, newFakeAPI(), nil)
	_, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "nowhere", Profile: "", Username: "a", Password: "b",
	})
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
	require.ErrorContains(t, err, `unknown provider "nowhere"`)

	_, err = engine.Pull(context.Background(), provider.BillPullRequest{Provider: ""})
	require.ErrorContains(t, err, "a provider is required")
}

func TestAPullAnswersBillsAndADocumentRefAndTheRefReadsOnce(t *testing.T) {
	module := newFakeAPI()
	module.document = &billers.Document{
		Bytes: []byte("%PDF-1.4 invented statement"), ContentType: "application/pdf",
		Filename: "statement.pdf",
	}
	engine := testEngine(t, module, nil)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "alliant", Profile: "connection-1",
		SessionState: string(fakeSession("fake-token", time.Now().Add(time.Hour), false)),
		Subaccounts:  []string{"one"},
	})
	require.NoError(t, err)
	require.False(t, out.NeedsSignIn)
	require.Len(t, out.Bills, 1)
	require.Equal(t, "10.00", out.Bills[0].AmountDue.String())
	require.Equal(t, "2026-09-20", out.Bills[0].DueOn.String())
	require.NotNil(t, out.Bills[0].Document)
	require.Equal(t, "application/pdf", out.Bills[0].Document.ContentType)
	require.Positive(t, out.Bills[0].Document.Size)
	require.Equal(t, "fake-token", billers.Session(out.SessionState).Kind())

	ref := out.Bills[0].Document.Ref
	bytes, contentType, filename, err := engine.FetchDocument(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, "application/pdf", contentType)
	require.Equal(t, "statement.pdf", filename)
	require.NotEmpty(t, bytes)

	_, _, _, err = engine.FetchDocument(context.Background(), ref)
	require.ErrorIs(t, err, provider.ErrAgentNotFound,
		"a ref is good for one read")
}

func TestKnownDocumentsKeepsADailyPullFromFetchingTheSameStatementTwice(t *testing.T) {
	module := newFakeAPI()
	module.document = &billers.Document{Bytes: []byte("%PDF-"), ContentType: "application/pdf"}
	engine := testEngine(t, module, nil)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider:     "alliant",
		SessionState: string(fakeSession("fake-token", time.Now().Add(time.Hour), false)),
		Subaccounts:  []string{"one"}, KnownDocuments: []string{"fake-1"},
	})
	require.NoError(t, err)
	require.Len(t, out.Bills, 1)
	require.Nil(t, out.Bills[0].Document)
	require.Equal(t, 0, module.documents, "the statement was not asked for")
}

func TestAnExpiredSessionRefreshesAndARefusedRefreshIsNeedsSignIn(t *testing.T) {
	module := newFakeAPI()
	engine := testEngine(t, module, nil)
	expired := string(fakeSession("fake-token", time.Now().Add(-time.Hour), false))

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "alliant", SessionState: expired, Subaccounts: []string{"one"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, module.refreshed)
	require.Len(t, out.Bills, 1)

	// The refusal with no kept password to sign in past it.
	module = newFakeAPI()
	module.refresh = func(billers.Call) (billers.Session, bool, string) {
		return nil, true, "Unauthorized Access"
	}
	engine = testEngine(t, module, nil)
	out, err = engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "alliant", SessionState: expired, Subaccounts: []string{"one"},
	})
	require.NoError(t, err)
	require.True(t, out.NeedsSignIn)
	require.Equal(t, "Unauthorized Access", out.Reason)
	require.Equal(t, 0, module.fetched)
}

func TestAKeptPasswordSignsInPastAnExpiredSessionWithNobodyInvolved(t *testing.T) {
	module := newFakeAPI()
	module.refresh = func(billers.Call) (billers.Session, bool, string) {
		return nil, true, "Unauthorized Access"
	}
	engine := testEngine(t, module, nil)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider:     "alliant",
		SessionState: string(fakeSession("fake-token", time.Now().Add(-time.Hour), false)),
		Subaccounts:  []string{"one"},
		Credential:   map[string]string{"username": "someone@example.test", "password": "invented"},
	})
	require.NoError(t, err)
	require.False(t, out.NeedsSignIn)
	require.Equal(t, 1, module.authenticated)
	require.Len(t, out.Bills, 1)
}

func TestATokenRefusedBeforeItsExpiryIsSignedInPastOnceAndNotTwice(t *testing.T) {
	module := newFakeAPI()
	refusals := 0
	module.fetchBills = func(call billers.Call) (billers.Pull, error) {
		// The session the sign-in minted carries `answered`; the kept one does
		// not, which is how this tells the two apart.
		var shape struct {
			Answered bool `json:"answered"`
		}
		_ = json.Unmarshal(call.Session, &shape)
		if !shape.Answered {
			refusals++
			return billers.Pull{NeedsSignIn: true, Reason: "the token was refused"}, nil
		}
		return billers.Pull{Bills: []billers.Bill{fakeBill()}}, nil
	}
	module.authenticate = func(billers.Credentials) billers.SignIn {
		return billers.SignIn{Session: fakeSession("fake-token", time.Now().Add(time.Hour), true)}
	}
	engine := testEngine(t, module, nil)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider:     "alliant",
		SessionState: string(fakeSession("fake-token", time.Now().Add(time.Hour), false)),
		Subaccounts:  []string{"one"},
		Credential:   map[string]string{"username": "someone@example.test", "password": "invented"},
	})
	require.NoError(t, err)
	require.Len(t, out.Bills, 1)
	require.Equal(t, 1, refusals)
	require.Equal(t, 1, module.authenticated, "a token refused right after minting is the provider saying no")
	require.Contains(t, out.Notes[0], "refused the kept session")
}

func TestASessionOfAKindTheProviderDoesNotTakeIsRefused(t *testing.T) {
	engine := testEngine(t, newFakeAPI(), nil)
	_, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider:     "alliant",
		SessionState: string(fakeSession("costco-b2c", time.Now().Add(time.Hour), false)),
	})
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
	require.ErrorContains(t, err, `does not take a "costco-b2c" session`)
}

func TestAChallengeParksThePullTheAnswerResumesItAndNothingIsAskedTwice(t *testing.T) {
	module := newFakeAPI()
	module.fetchBills = func(call billers.Call) (billers.Pull, error) {
		var shape struct {
			Answered bool `json:"answered"`
		}
		_ = json.Unmarshal(call.Session, &shape)
		if shape.Answered {
			return billers.Pull{
				Bills:   []billers.Bill{fakeBill()},
				Session: fakeSession("fake-token", time.Now().Add(time.Hour), true),
			}, nil
		}
		return billers.Pull{Challenge: &billers.Challenge{
			State: billers.StateOTP, Method: "sms", Prompt: "the code we texted you",
			Answer: func(ctx context.Context, code string, notes *billers.Notes) billers.SignIn {
				if code != "424242" {
					return billers.SignIn{Failed: "that code was refused"}
				}
				return billers.SignIn{Session: fakeSession("fake-token", time.Now().Add(time.Hour), true)}
			},
		}}, nil
	}
	engine := testEngine(t, module, nil)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider:     "alliant",
		SessionState: string(fakeSession("fake-token", time.Now().Add(time.Hour), false)),
		Subaccounts:  []string{"one"},
	})
	require.NoError(t, err)
	require.Empty(t, out.Bills)
	require.NotNil(t, out.Challenge)
	require.Equal(t, billers.StateOTP, out.Challenge.State)
	require.Equal(t, "sms", out.Challenge.Method)
	require.Equal(t, "the code we texted you", out.Challenge.Prompt)
	parked := out.Challenge.SessionID

	// A pull's challenge is answered, not given up: closing it would throw away a
	// half-done pull.
	err = engine.CancelSignIn(context.Background(), parked)
	require.ErrorIs(t, err, provider.ErrAgentConflict)
	require.ErrorContains(t, err, "belongs to a pull")

	wrong, err := engine.AnswerConnect(context.Background(), parked, "000000")
	require.NoError(t, err)
	require.Equal(t, billers.StateFailed, wrong.State)
	require.Equal(t, "that code was refused", wrong.Error)

	// A resume before the answer lands is refused rather than silently redone.
	_, err = engine.ResumePull(context.Background(), parked)
	require.ErrorIs(t, err, provider.ErrAgentConflict)

	answered, err := engine.AnswerConnect(context.Background(), parked, "424242")
	require.NoError(t, err)
	require.Equal(t, billers.StateSignedIn, answered.State)

	resumed, err := engine.ResumePull(context.Background(), parked)
	require.NoError(t, err)
	require.False(t, resumed.NeedsSignIn)
	require.Len(t, resumed.Bills, 1)
	require.Equal(t, "10.00", resumed.Bills[0].AmountDue.String())

	_, err = engine.ResumePull(context.Background(), parked)
	require.ErrorIs(t, err, provider.ErrAgentNotFound)
	require.Equal(t, 0, engine.Sessions())
}

func TestABrowserProviderWithNoKeptSessionAndNoProfileAsksForASignIn(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/account"}
	engine := testEngine(t, module, page)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
	})
	require.NoError(t, err)
	require.True(t, out.NeedsSignIn)
	require.Contains(t, out.Reason, "no kept session")
	require.Equal(t, 0, module.fetched, "no browser is opened to be told there is nothing to open")
}

// A profile on the volume is a kept session even when the backend sent none:
// the directory is the session of record, the sealed state the fallback.
func TestABrowserProfileOnDiskIsAKeptSessionEvenWithNoSealedState(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/account"}
	engine := testEngine(t, module, page)

	dir, err := browser.ProfileDir(engine.ProfilesRoot, "connection-9")
	require.NoError(t, err)
	_, err = browser.RecordedDevice(dir, browser.DefaultDevice(browser.DefaultViewport, testUserAgent))
	require.NoError(t, err)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
	})
	require.NoError(t, err)
	require.False(t, out.NeedsSignIn)
	require.Len(t, out.Bills, 1)
	require.Equal(t, 1, module.fetched)
}

// A pull that ends "needs sign-in" lets the browser go on the way out: a
// profile claim left behind refuses the Connect that follows. testEngine fails
// the test if a browser it opened was not closed.
func TestAPullThatEndsNeedsSignInLetsItsBrowserGo(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/login"}
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StatePassword}, nil
	}
	module.fetchBills = func(billers.Call) (billers.Pull, error) {
		return billers.Pull{NeedsSignIn: true, Reason: "the profile has been signed out"}, nil
	}
	engine := testEngine(t, module, page)
	dir, err := browser.ProfileDir(engine.ProfilesRoot, "connection-9")
	require.NoError(t, err)
	_, err = browser.RecordedDevice(dir, browser.DefaultDevice(browser.DefaultViewport, testUserAgent))
	require.NoError(t, err)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
	})
	require.NoError(t, err)
	require.True(t, out.NeedsSignIn)
	require.Equal(t, 0, engine.Sessions(),
		"the session is gone, so nothing is holding the profile")
}

func TestABrowserPullPinsItsCookiesAndAnswersTheRolledState(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/account"}
	engine := testEngine(t, module, page)
	var logged bytes.Buffer
	engine.Log = slog.New(slog.NewTextHandler(&logged, nil))

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[{"name":"session","value":"invented","domain":".example.test","path":"/","expires":-1}],"origins":[]}`,
	})
	require.NoError(t, err)
	require.Len(t, out.Bills, 1)
	require.JSONEq(t, `{"cookies":[],"origins":[]}`, string(out.SessionState))
	// Which cookies were pinned is for the log, not the household.
	require.NotContains(t, strings.Join(out.Notes, "\n"), "cookie")
	require.Contains(t, logged.String(), "1 session cookie pinned")
	require.Contains(t, logged.String(), "connector=")
}

// What a module saw and did is handed back with the pull, page lines at the
// address the page stood at, so the connection can keep it, and so are the
// labels it read.
func TestAPullHandsBackTheTrailItsModuleKept(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/account?token=invented"}
	module.fetchBills = func(call billers.Call) (billers.Pull, error) {
		call.Saw("the account page")
		call.Mark("%d statements read", 2)
		return billers.Pull{
			Bills:       []billers.Bill{fakeBill()},
			Subaccounts: []billers.Subaccount{{ExternalID: "one", Label: "One, as its page names it"}},
		}, nil
	}
	engine := testEngine(t, module, page)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`,
	})
	require.NoError(t, err)
	require.Len(t, out.Trail, 2)
	require.Equal(t, "read", out.Trail[0].Step)
	require.Equal(t, "page", out.Trail[0].State)
	require.Equal(t, "https://example.test/account", out.Trail[0].URL, "no query: an address carries tokens there")
	require.Equal(t, "the account page", out.Trail[0].Note)
	require.Equal(t, "note", out.Trail[1].State)
	require.Empty(t, out.Trail[1].URL)
	require.Equal(t, "2 statements read", out.Trail[1].Note)
	require.Equal(t, []provider.BillSubaccountRef{{ExternalID: "one", Label: "One, as its page names it"}}, out.Subaccounts)
}

func TestABrowserPullSignedOutSignsInWithTheKeptPasswordRatherThanWaiting(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/login"}
	signedIn := false
	module.classify = func(browser.Page) (billers.State, error) {
		if signedIn {
			return billers.State{State: billers.StateSignedIn}, nil
		}
		return billers.State{State: billers.StatePassword}, nil
	}
	module.fetchBills = func(billers.Call) (billers.Pull, error) {
		if !signedIn {
			return billers.Pull{NeedsSignIn: true, Reason: "the profile has been signed out"}, nil
		}
		return billers.Pull{Bills: []billers.Bill{fakeBill()}}, nil
	}
	// Filling the password is what "signs in" this fake.
	page.OnFill = func(selector, value string) error {
		if value == "invented" {
			signedIn = true
		}
		return nil
	}
	engine := testEngine(t, module, page)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`,
		Credential:   map[string]string{"username": "someone@example.test", "password": "invented"},
	})
	require.NoError(t, err)
	require.False(t, out.NeedsSignIn)
	require.Len(t, out.Bills, 1)
	require.Contains(t, out.Notes[len(out.Notes)-2], "trying the kept password")
	require.Equal(t, "Erie Insurance's sign-in ran in Chrome", out.Notes[len(out.Notes)-1],
		"the trail says which browser the sign-in ran in")
}

func TestAPullThatSucceedsAfterSigningInLeadsWithWhatTheSecondAttemptSaid(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/login"}
	signedIn := false
	module.classify = func(browser.Page) (billers.State, error) {
		if signedIn {
			return billers.State{State: billers.StateSignedIn}, nil
		}
		return billers.State{State: billers.StatePassword}, nil
	}
	module.fetchBills = func(call billers.Call) (billers.Pull, error) {
		if !signedIn {
			call.Notes.Addf("the portal holds no signed-in user")
			return billers.Pull{NeedsSignIn: true}, nil
		}
		call.Notes.Addf("the portal answered 1 statement")
		return billers.Pull{Bills: []billers.Bill{fakeBill()}}, nil
	}
	page.OnFill = func(selector, value string) error {
		if value == "invented" {
			signedIn = true
		}
		return nil
	}
	engine := testEngine(t, module, page)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`,
		Credential:   map[string]string{"username": "someone@example.test", "password": "invented"},
	})
	require.NoError(t, err)
	require.Len(t, out.Bills, 1)
	require.Equal(t, "the portal answered 1 statement", out.Notes[0],
		"the connection keeps the first note, and the failed attempt is not the news")
	require.Contains(t, out.Notes, "the portal holds no signed-in user")
}

func TestAKeepaliveRefreshesAnAPISessionAndSaysSoWhenThereIsNoneToTouch(t *testing.T) {
	module := newFakeAPI()
	engine := testEngine(t, module, nil)

	nothing, err := engine.Keepalive(context.Background(), "alliant", "connection-1", "", "")
	require.NoError(t, err)
	require.True(t, nothing.OK)
	require.False(t, nothing.SignedIn)
	require.Equal(t, 0, module.refreshed)

	touched, err := engine.Keepalive(context.Background(), "alliant", "connection-1", "",
		string(fakeSession("fake-token", time.Now().Add(time.Hour), false)))
	require.NoError(t, err)
	require.True(t, touched.SignedIn)
	require.Equal(t, "fake-token", billers.Session(touched.SessionState).Kind())
	require.Equal(t, 1, module.refreshed)
}

func TestAKeepaliveOnABrowserProfileReadsTheLandingPageAndPinsWhatItFinds(t *testing.T) {
	module := newFakeBrowser()
	page := &browser.StubPage{Location: "https://example.test/account"}
	engine := testEngine(t, module, page)
	var logged bytes.Buffer
	engine.Log = slog.New(slog.NewTextHandler(&logged, nil))

	out, err := engine.Keepalive(context.Background(), "erie", "connection-9", "",
		`{"cookies":[],"origins":[]}`)
	require.NoError(t, err)
	require.True(t, out.SignedIn)
	require.Equal(t, []string{"https://example.test/account"}, page.Visited)
	require.Contains(t, logged.String(), "1 session cookie pinned")

	// A landing page that shows a sign-in form is a session the provider has
	// forgotten, and the connection is told so.
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StatePassword, Prompt: "Sign in"}, nil
	}
	out, err = engine.Keepalive(context.Background(), "erie", "connection-9", "",
		`{"cookies":[],"origins":[]}`)
	require.NoError(t, err)
	require.False(t, out.SignedIn)
	require.Equal(t, "Sign in", out.Reason)
}

func TestTheReaperClosesWhatNobodyCameBackFor(t *testing.T) {
	module := newFakeAPI()
	engine := testEngine(t, module, nil)
	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "alliant", Profile: "connection-1", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	require.Equal(t, 1, engine.Sessions())

	// Past SessionTTL, nobody has come back.
	engine.Now = func() time.Time { return time.Now().Add(SessionTTL + time.Minute) }
	engine.Reap()
	require.Equal(t, 0, engine.Sessions())
	_, err = engine.ConnectStatus(context.Background(), state.SessionID)
	require.ErrorIs(t, err, provider.ErrAgentNotFound)
}

// What the modules remember about a page goes with the page, however the page
// goes, the reaper included.
func TestTheReaperForgetsThePageItCloses(t *testing.T) {
	var forgotten []browser.Page
	was := forget
	forget = func(page browser.Page) { forgotten = append(forgotten, page) }
	t.Cleanup(func() { forget = was })

	module := newFakeBrowser()
	page := readingPage("https://login.example.test/sso")
	engine := testEngine(t, module, page)
	closed := false
	s := engine.open(module)
	s.Attach(&OpenBrowser{Page: page, Close: func() { closed = true }})

	engine.Now = func() time.Time { return time.Now().Add(SessionTTL + time.Minute) }
	engine.Reap()
	require.Equal(t, 0, engine.Sessions())
	require.True(t, closed)
	require.Equal(t, []browser.Page{page}, forgotten)
}

func TestAMintedDocumentIsForgottenAfterTenMinutes(t *testing.T) {
	module := newFakeAPI()
	module.document = &billers.Document{Bytes: []byte("%PDF-"), ContentType: "application/pdf"}
	engine := testEngine(t, module, nil)

	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider:     "alliant",
		SessionState: string(fakeSession("fake-token", time.Now().Add(time.Hour), false)),
		Subaccounts:  []string{"one"},
	})
	require.NoError(t, err)
	ref := out.Bills[0].Document.Ref

	engine.Now = func() time.Time { return time.Now().Add(DocumentTTL + time.Minute) }
	_, _, _, err = engine.FetchDocument(context.Background(), ref)
	require.ErrorIs(t, err, provider.ErrAgentNotFound)
}

func TestAProfileIsForgottenAndANameThatIsNoProfileIsNotAFailure(t *testing.T) {
	engine := testEngine(t, newFakeAPI(), nil)
	require.NoError(t, engine.ForgetProfile(context.Background(), "connection-1"))
	require.NoError(t, engine.ForgetProfile(context.Background(), ".."))
}

// A typed sign-in that ends on a page this engine does not recognise is a
// failure with its trail, not a Continue button: `interactive` means somebody
// is driving, and in a typed sign-in nobody is.
func TestATypedSignInThatEndsOnAPageNobodyRecognisesFailsWithItsTrail(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateInteractive}, nil
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/sso"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	require.Equal(t, "unrecognised page", state.Error)
	require.Contains(t, state.Prompt, "did not recognise")
	require.NotEmpty(t, state.Trail, "the page it stopped on is the whole of what there is to go on")
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// The sign-in form at the end of a sign-in that has already filled it in: the
// poll re-classifies it as `password` for as long as anybody asks, so it has
// to settle into a failure.
func TestAPollThatFindsTheSignInFormAgainIsAFailureWithItsTrail(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateSignedIn}, nil
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/sso"))
	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	require.Equal(t, billers.StateSignedIn, awaitSignIn(t, engine, state).State)

	// The page goes back to the form under the dialog's feet.
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StatePassword}, nil
	}
	polled, err := engine.ConnectStatus(context.Background(), state.SessionID)

	require.NoError(t, err)
	require.Equal(t, billers.StateFailed, polled.State)
	require.Equal(t, "the sign-in form came back", polled.Error)
	require.Contains(t, polled.Prompt, "sign-in form again")
	require.NotEmpty(t, polled.Trail, "the page it stopped on is the whole of what there is to go on")
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A sign-in that broke on a page shows that page, taken before the browser
// goes.
func TestASignInThatBrokeOnAPageShowsThePage(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{}, errors.New("something on the page covered the button")
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/sso"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	require.Contains(t, state.Error, "covered the button")
	require.NotEmpty(t, state.Image)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A state nobody here has heard of is named and failed rather than handed
// over for somebody to press Continue at.
func TestAStateTheDialogHasNoScreenForIsNamedRatherThanShown(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: "half_signed_in"}, nil
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/sso"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	require.Equal(t, "unanswerable state: half_signed_in", state.Error)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// The same rule at the other end: a code typed at a page that is asking for
// the form's own fields drives the form, rather than answering the dialog with
// the state it was already showing.
func TestAnAnswerAtAFormStepDrivesTheFormRatherThanComingBackTheSame(t *testing.T) {
	module := newFakeBrowser()
	rounds := []billers.State{
		{State: billers.StatePassword}, // what AnswerConnect reads
		{State: billers.StatePassword}, // what the loop fills
		{State: billers.StateSignedIn},
	}
	module.classify = func(browser.Page) (billers.State, error) {
		where := rounds[0]
		if len(rounds) > 1 {
			rounds = rounds[1:]
		}
		return where, nil
	}
	page := readingPage("https://login.example.test/sso")
	engine := testEngine(t, module, page)
	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	require.Equal(t, billers.StateSignedIn, awaitSignIn(t, engine, state).State)

	// The dialog presses Continue at it anyway, with whatever is in the box.
	rounds = []billers.State{{State: billers.StatePassword}, {State: billers.StateSignedIn}}
	answered, err := engine.AnswerConnect(context.Background(), state.SessionID, "123456")

	require.NoError(t, err)
	require.Equal(t, billers.StateSignedIn, answered.State)
	for _, filled := range page.Filled {
		require.NotEqual(t, "123456", filled.Value,
			"a code is never typed into a form that is not asking for one")
	}
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A password form still up after the password went in is given its time to
// clear, and is then a refusal: the password is typed once, never once a round.
func TestABillPasswordFormThatStaysUpIsTypedIntoOnce(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StatePassword}, nil
	}
	page := readingPage("https://login.example.test/sso")
	engine := testEngine(t, module, page)

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	require.Equal(t, "the password was not accepted", state.Error)
	typed := 0
	for _, filled := range append(page.Filled, page.TypedInto...) {
		if filled.Value == "invented" {
			typed++
		}
	}
	require.Equal(t, 1, typed)
	require.GreaterOrEqual(t, page.Slept, passwordAnswerWait)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// The rounds outlive the request that started them, and say what they are at:
// the session is answered as soon as it exists and the poll carries the rest.
func TestATypedSignInAnswersAtOnceAndSaysWhatItIsDoing(t *testing.T) {
	module := newFakeBrowser()
	var mu sync.Mutex
	reads := 0
	filling := make(chan struct{})
	carryOn := make(chan struct{})
	module.classify = func(browser.Page) (billers.State, error) {
		mu.Lock()
		reads++
		round := reads
		mu.Unlock()
		switch round {
		case 1:
			return billers.State{State: billers.StatePassword}, nil
		case 2:
			// The loop is held here, one round in, with the dialog polling.
			close(filling)
			<-carryOn
		}
		return billers.State{State: billers.StateSignedIn}, nil
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/sso"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	require.Equal(t, provider.BillConnectSigningIn, state.State)
	require.Contains(t, state.Prompt, "Opening the sign-in page")

	<-filling
	polled, err := engine.ConnectStatus(context.Background(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, provider.BillConnectSigningIn, polled.State)
	require.Equal(t, "Filling in the password", polled.Prompt)

	// Said without reading the page: a classify taken mid-round reads a page
	// between two forms.
	mu.Lock()
	before := reads
	mu.Unlock()
	_, err = engine.ConnectStatus(context.Background(), state.SessionID)
	require.NoError(t, err)
	mu.Lock()
	require.Equal(t, before, reads, "the poll read the page the loop was driving")
	mu.Unlock()

	// Nothing is answered or completed underneath the rounds either.
	_, err = engine.AnswerConnect(context.Background(), state.SessionID, "123456")
	require.ErrorIs(t, err, provider.ErrAgentConflict)
	_, err = engine.CompleteConnect(context.Background(), state.SessionID)
	require.ErrorIs(t, err, provider.ErrAgentConflict)

	close(carryOn)
	require.Equal(t, billers.StateSignedIn, awaitSignIn(t, engine, polled).State)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A sign-in cancelled in the middle of a round stops at that round: a loop that
// went on driving a closed browser would take the next sign-in's profile with
// it.
func TestASignInCancelledMidRoundStopsTheRoundsAndFreesTheProfile(t *testing.T) {
	module := newFakeBrowser()
	var mu sync.Mutex
	reads := 0
	driving := make(chan struct{})
	carryOn := make(chan struct{})
	module.classify = func(browser.Page) (billers.State, error) {
		mu.Lock()
		reads++
		first := reads == 1
		mu.Unlock()
		if first {
			close(driving)
			<-carryOn
		}
		return billers.State{State: billers.StatePassword}, nil
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/sso"))
	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	<-driving

	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
	require.Equal(t, 0, engine.Sessions())
	close(carryOn)

	// The round it was in finishes, and the next one finds no browser: the
	// loop lets go rather than filling a form on a page that has been closed.
	require.Never(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return reads > 1
	}, 100*time.Millisecond, 10*time.Millisecond)
}

// A round that failed outright lets the browser go at once and keeps the
// session: the profile claim is released and the trail can still be read.
func TestARoundThatFailedOutrightFreesTheBrowserAndKeepsTheFailure(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{}, errors.New("the page would not answer")
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/sso"))
	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)

	landed := awaitSignIn(t, engine, state)
	require.Equal(t, billers.StateFailed, landed.State)
	require.Contains(t, landed.Error, "the page would not answer")
	require.NotEmpty(t, landed.Trail)
	require.Equal(t, 1, engine.Sessions(), "the failure is still there to be read")

	s, err := engine.find(state.SessionID)
	require.NoError(t, err)
	require.Nil(t, s.Opened(), "the browser is already gone, which is what the next sign-in needs")
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// The reaper skips a sign-in whose rounds are still running, however long
// since the last poll, but takes a loop wedged past RoundsBound: the profile it
// holds would refuse the next sign-in.
func TestTheReaperLeavesASignInWhoseRoundsAreStillRunning(t *testing.T) {
	module := newFakeBrowser()
	atRound := make(chan int)
	carryOn := make(chan struct{})
	round := 0
	module.classify = func(browser.Page) (billers.State, error) {
		round++
		atRound <- round
		<-carryOn
		return billers.State{State: billers.StatePassword}, nil
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/sso"))
	// The rounds read the clock too, so the test shares it with them.
	started := time.Now()
	var clock atomic.Int64
	clock.Store(started.UnixNano())
	engine.Now = func() time.Time { return time.Unix(0, clock.Load()) }

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)

	// A round that lands past SessionTTL: nothing has polled this session, and it
	// is still not idle.
	require.Equal(t, 1, <-atRound)
	clock.Store(started.Add(SessionTTL + time.Minute).UnixNano())
	close(carryOn)
	require.Equal(t, 2, <-atRound)
	engine.Reap()
	require.Equal(t, 1, engine.Sessions(), "the rounds were still running")

	// A round that never lands: past RoundsBound the loop is wedged, and the
	// profile goes.
	clock.Store(started.Add(SessionTTL + RoundsBound + 2*time.Minute).UnixNano())
	engine.Reap()
	require.Equal(t, 0, engine.Sessions())
	_, err = engine.ConnectStatus(context.Background(), state.SessionID)
	require.ErrorIs(t, err, provider.ErrAgentNotFound)
}

// The rounds in their own goroutine with the poll on top of them. `go test
// -race` is the assertion.
func TestThePollAndTheRoundsAreSafeOnTheSameSession(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		// Long enough that the poll is reading while the loop is driving.
		time.Sleep(5 * time.Millisecond)
		return billers.State{State: billers.StatePassword}, nil
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/sso"))
	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)

	const rounds = 200
	var running sync.WaitGroup
	running.Add(2)
	go func() {
		defer running.Done()
		for i := 0; i < rounds; i++ {
			if _, err := engine.ConnectStatus(context.Background(), state.SessionID); err != nil {
				t.Error(err)
				return
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()
	go func() {
		defer running.Done()
		for i := 0; i < rounds; i++ {
			if _, err := engine.ConnectTrail(context.Background(), state.SessionID); err != nil {
				t.Error(err)
				return
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()
	running.Wait()

	// The page never moved, so the loop gave up: one trail round for each time it
	// was asked, and one failure however many times the poll answered it.
	landed := awaitSignIn(t, engine, state)
	require.Equal(t, billers.StateFailed, landed.State)
	require.Equal(t, "failed", landed.Trail[len(landed.Trail)-1].Step)
	for _, round := range landed.Trail[:len(landed.Trail)-1] {
		require.Equal(t, "sign-in", round.Step)
	}
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// Letting go of a connection's browser: the session this process holds, and
// the lock file and singleton a dead container left, in one call.
func TestReleasingABrowserClosesTheSignInAndGivesUpWhatADeadProcessLeft(t *testing.T) {
	engine := testEngine(t, newFakeAPI(), nil)
	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "alliant", Profile: "connection-1", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)

	// What the container that died left behind on the volume.
	dir, err := browser.ProfileDir(engine.ProfilesRoot, "connection-1")
	require.NoError(t, err)
	_, err = browser.TakeProfileLock(dir, "the container that died",
		time.Now().Add(-browser.LockStale-time.Minute))
	require.NoError(t, err)
	require.NoError(t, os.Symlink("dead-host-4242", filepath.Join(dir, "SingletonLock")))

	released, err := engine.ReleaseProfile(context.Background(), "connection-1")
	require.NoError(t, err)
	require.Equal(t, []string{state.SessionID}, released.Sessions)
	require.True(t, released.Lock)
	require.True(t, released.Singleton)
	require.True(t, released.Released())

	require.Equal(t, 0, engine.Sessions(), "the sign-in is nobody's now")
	require.NoFileExists(t, filepath.Join(dir, browser.LockFile))
	_, err = os.Lstat(filepath.Join(dir, "SingletonLock"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// A lock somebody is still refreshing belongs to a running Chromium (on a
// deploy, the container being replaced) and is refused.
func TestReleasingABrowserSomethingIsStillDrivingIsRefused(t *testing.T) {
	engine := testEngine(t, newFakeAPI(), nil)
	dir, err := browser.ProfileDir(engine.ProfilesRoot, "connection-1")
	require.NoError(t, err)
	_, err = browser.TakeProfileLock(dir, "the other container", time.Now())
	require.NoError(t, err)
	require.NoError(t, os.Symlink("live", filepath.Join(dir, "SingletonLock")))

	_, err = engine.ReleaseProfile(context.Background(), "connection-1")
	require.ErrorIs(t, err, provider.ErrAgentConflict)
	require.ErrorContains(t, err, "lets go by itself")
	require.FileExists(t, filepath.Join(dir, browser.LockFile))
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	require.NoError(t, err)
	require.Equal(t, "live", target)
}

// Nothing to give up is an ordinary answer, and so is a connection with no
// profile at all (an api provider).
func TestReleasingABrowserNothingIsHoldingGivesUpNothing(t *testing.T) {
	engine := testEngine(t, newFakeAPI(), nil)

	released, err := engine.ReleaseProfile(context.Background(), "connection-1")
	require.NoError(t, err)
	require.False(t, released.Released())
	require.Empty(t, released.Sessions)

	released, err = engine.ReleaseProfile(context.Background(), "")
	require.NoError(t, err)
	require.False(t, released.Released())
}
