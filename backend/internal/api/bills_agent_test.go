package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// The bills resource against a scripted engine.
//
// Three things are worth the whole stack: that a person can sign in to a
// provider through the app and the session is kept without any credential
// coming back; that a pull answers with what it did, including the challenge
// it parked; and that the assistant cannot reach any of it.
//
// Every figure, code and session here is invented.

// fakeBillsAgent is a service.BillsAgent with the connect and the pull
// scripted. It stands where the engine that drives Chromium stands, so these
// tests exercise the resource rather than a browser.
type fakeBillsAgent struct {
	mu sync.Mutex
	// challenge makes the next pull park a sign-in instead of answering bills.
	challenge bool
	// document makes the next pull offer a statement.
	document bool
	// refuse makes the next pull answer that the provider wants a sign-in,
	// and refusePassword that it tried the kept password and was turned away.
	refuse         bool
	refusePassword bool
	// codes records what was answered into a sign-in.
	codes []string
	seen  []string
	// credentials records the login each pull carried, nil for none.
	credentials []map[string]string
	// connects records each sign-in the engine was asked to start, and lives
	// each live browser it was asked to open.
	connects []map[string]string
	lives    []map[string]string
	// cancelled records the sign-ins the engine was told to give up.
	cancelled []string
	// releases records the profiles the engine was told to let go of, gaveUp
	// is what it answers for one, and driven makes it refuse: something is
	// still refreshing that profile's lock.
	releases []string
	gaveUp   provider.BillProfileRelease
	driven   bool
}

func newFakeBillsAgent(t *testing.T) *fakeBillsAgent {
	t.Helper()
	return &fakeBillsAgent{}
}

// billsClient is a signed-in client whose environment reaches the fake engine.
//
// Its own environment rather than the ledger's, because the frozen clock and
// the engine are both built into it: a client made before the engine was set
// would open a real browser.
func billsClient(l *ledger, agent *fakeBillsAgent) *client {
	l.t.Helper()
	env := NewEnv(testConfig(), db(l.t), WithClock(func() time.Time { return seriesClock }))
	env.BillsAgent = agent
	account, known := l.users["alex"]
	require.True(l.t, known)
	return (&client{t: l.t, env: env, handler: RouterFor(env)}).
		as(account).inSpace(store.SpaceIDOf(l.id("space")))
}

func (a *fakeBillsAgent) Available() bool { return true }

func (a *fakeBillsAgent) Health(context.Context) error { return nil }

func (a *fakeBillsAgent) Providers(context.Context) ([]provider.BillProvider, error) {
	return []provider.BillProvider{{
		ID: "spectrum", Name: "Spectrum", Access: "browser",
		Home: "https://www.spectrum.net",
		SignIn: provider.BillProviderSign{
			Kinds: []string{"typed", "live"}, Prompt: "Sign in with your username",
		},
		Challenges: []string{"sms", "email"}, SessionPersists: true,
		KeepaliveDays: 7, HasDocuments: true,
	}}, nil
}

func (a *fakeBillsAgent) StartConnect(
	ctx context.Context, request provider.BillConnectStart,
) (provider.BillConnectState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.connects = append(a.connects, map[string]string{
		"provider": request.Provider, "profile": request.Profile, "site": request.Site,
		"username": request.Username, "password": request.Password,
		"totp": request.Code, "totp_secret": request.Secret, "second_factor": request.SecondFactor,
	})
	return provider.BillConnectState{
		SessionID: "c1", Provider: "spectrum", State: provider.BillConnectOTP,
		Method: "sms", Prompt: "Enter the code we texted you",
	}, nil
}

func (a *fakeBillsAgent) StartLiveConnect(
	ctx context.Context, providerID, profile, site string, width, height int,
) (provider.BillConnectState, error) {
	a.mu.Lock()
	a.lives = append(a.lives, map[string]string{
		"provider": providerID, "profile": profile, "site": site,
	})
	a.mu.Unlock()
	return provider.BillConnectState{
		SessionID: "c1", Provider: "spectrum", State: provider.BillConnectInteractive,
		Prompt: "Sign in below", Width: width, Height: height,
	}, nil
}

func (a *fakeBillsAgent) ConnectStatus(ctx context.Context, sessionID string) (provider.BillConnectState, error) {
	return provider.BillConnectState{
		SessionID: sessionID, Provider: "spectrum", State: provider.BillConnectOTP, Method: "sms",
	}, nil
}

func (a *fakeBillsAgent) ConnectTrail(ctx context.Context, sessionID string) ([]provider.BillTrailEntry, error) {
	return []provider.BillTrailEntry{{
		At:    time.Date(2026, time.September, 19, 20, 33, 52, 0, time.UTC),
		Step:  "sign-in",
		State: "password",
		URL:   "https://www.spectrum.net/login",
		Title: "Sign in",
		Form: provider.BillSignInFormFlags{
			Password: true, Username: true,
		},
		Inputs: map[string]int{"email": 1, "password": 1},
		Error:  "That password was not recognized. Try again.",
		Did: provider.BillSignInAction{
			Acted: true, Pressed: "button", Words: "Sign In", Changed: true,
		},
	}, {
		At:      time.Date(2026, time.September, 19, 20, 33, 58, 0, time.UTC),
		Step:    "sign-in",
		State:   "factor",
		URL:     "https://www.spectrum.net/mfa",
		Inputs:  map[string]int{"radio": 2},
		Choices: []provider.BillSignInChoice{{Kind: "radio", Words: "Text message"}},
		Chose:   "Text message",
	}}, nil
}

func (a *fakeBillsAgent) SteerTo(ctx context.Context, sessionID, address string) (provider.BillSignInSteer, error) {
	return provider.BillSignInSteer{Provider: "spectrum", URL: address}, nil
}

func (a *fakeBillsAgent) SteerClick(ctx context.Context, sessionID, text, selector string) (provider.BillSignInSteer, error) {
	return provider.BillSignInSteer{Provider: "spectrum"}, nil
}

func (a *fakeBillsAgent) SteerDOM(ctx context.Context, sessionID, selector string, limit int) (provider.BillSignInDOM, error) {
	return provider.BillSignInDOM{Provider: "spectrum"}, nil
}

func (a *fakeBillsAgent) SteerFetch(
	ctx context.Context, sessionID string, request provider.BillSignInFetchRequest,
) (provider.BillSignInFetch, error) {
	return provider.BillSignInFetch{Provider: "spectrum", URL: request.URL}, nil
}

// AnswerConnect takes the code. One code is right; every other asks again,
// which is what a provider does and what the dialog has to draw.
func (a *fakeBillsAgent) AnswerConnect(
	ctx context.Context, sessionID, code string,
) (provider.BillConnectState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.codes = append(a.codes, code)
	if code != "314159" {
		return provider.BillConnectState{
			SessionID: "c1", Provider: "spectrum", State: provider.BillConnectOTP,
			Method: "sms", Prompt: "That code was not right",
		}, nil
	}
	return provider.BillConnectState{
		SessionID: "c1", Provider: "spectrum", State: provider.BillConnectSignedIn,
	}, nil
}

func (a *fakeBillsAgent) CancelSignIn(ctx context.Context, sessionID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelled = append(a.cancelled, sessionID)
	return nil
}

func (a *fakeBillsAgent) CompleteConnect(ctx context.Context, sessionID string) (provider.BillConnectComplete, error) {
	return provider.BillConnectComplete{
		Provider:     "spectrum",
		SessionState: json.RawMessage(`{"token":"kept-1"}`),
		Subaccounts: []provider.BillSubaccountRef{{
			ExternalID: "premise-7", Label: "Main account", MaskedNumber: "••0000",
		}},
		AccountHint: "Alex",
	}, nil
}

func (a *fakeBillsAgent) Pull(
	ctx context.Context, request provider.BillPullRequest,
) (provider.BillPull, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen = append(a.seen, request.SessionState)
	a.credentials = append(a.credentials, request.Credential)
	switch {
	case a.refusePassword:
		return provider.BillPull{
			NeedsSignIn: true, PasswordRefused: true,
			Reason: "Spectrum did not accept the kept password (Invalid username or password)",
		}, nil
	case a.refuse:
		return provider.BillPull{
			NeedsSignIn: true, Reason: "Spectrum did not accept the sign-in",
		}, nil
	case a.challenge:
		return provider.BillPull{Challenge: &provider.BillChallengeState{
			SessionID: "park-1", State: "otp", Method: "sms",
			Prompt: "Enter the code we texted you",
		}}, nil
	}
	return a.bills(), nil
}

func (a *fakeBillsAgent) ResumePull(ctx context.Context, sessionID string) (provider.BillPull, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bills(), nil
}

func (a *fakeBillsAgent) FetchDocument(ctx context.Context, ref string) ([]byte, string, string, error) {
	return storetest.PDF(), "application/pdf", "october.pdf", nil
}

func (a *fakeBillsAgent) Keepalive(
	ctx context.Context, providerID, profile, site, sessionState string,
) (provider.BillKeepalive, error) {
	return provider.BillKeepalive{OK: true, SignedIn: true}, nil
}

func (a *fakeBillsAgent) ForgetProfile(ctx context.Context, profile string) error { return nil }

func (a *fakeBillsAgent) ReleaseProfile(
	ctx context.Context, profile string,
) (provider.BillProfileRelease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.releases = append(a.releases, profile)
	if a.driven {
		return provider.BillProfileRelease{}, &provider.AgentError{
			Kind:    provider.ErrAgentConflict,
			Message: "something is still driving this connection's browser",
		}
	}
	return a.gaveUp, nil
}

// bills is the pull that went well. Callers hold a.mu.
func (a *fakeBillsAgent) bills() provider.BillPull {
	bill := provider.BillStatement{
		Subaccount: "premise-7", ExternalID: "stmt-10",
		IssuedOn: domain.NewDate(2026, 9, 20), DueOn: domain.NewDate(2026, 10, 26),
		AmountDue: domain.MustFromString("120.00"), Currency: "USD", Status: "Open",
		StatementURL: "https://www.spectrum.net/statements/stmt-10",
	}
	if a.document {
		bill.Document = &provider.BillDocumentRef{
			Ref: "doc-1", ContentType: "application/pdf",
			Size: len(storetest.PDF()), Filename: "october.pdf",
		}
	}
	return provider.BillPull{
		SessionState: json.RawMessage(`{"token":"rolled-2"}`),
		Bills:        []provider.BillStatement{bill},
	}
}

// noBillsAgent is a build with no engine at all: nothing registered, nothing
// to open a browser with. Not the deployed case — serve always builds one —
// but the refusal it answers with is what a household would see if it ever
// were, so it is pinned rather than assumed.
type noBillsAgent struct{ *fakeBillsAgent }

func (noBillsAgent) Available() bool { return false }

// signInThroughTheAgent runs a connect to the end and answers with the
// connection's id.
func signInThroughTheAgent(t *testing.T, alex *client, agent *fakeBillsAgent) string {
	t.Helper()
	return signInThroughTheAgentAs(t, alex, agent, "Main account")
}

// signInThroughTheAgentAs is the same connect on a connection of the given
// label.
func signInThroughTheAgentAs(t *testing.T, alex *client, agent *fakeBillsAgent, label string) string {
	t.Helper()
	return signInThroughTheAgentSending(t, alex, agent, label, typedSignIn())
}

func typedSignIn() map[string]any {
	return map[string]any{"mode": "typed", "username": "alex", "password": "hunter2"}
}

// signInKeepingOnlyTheSession is a connection whose password is not kept: a
// connect, and then the settings move to the session alone.
func signInKeepingOnlyTheSession(t *testing.T, alex *client, agent *fakeBillsAgent) string {
	t.Helper()
	id := signInThroughTheAgent(t, alex, agent)
	moved := alex.patch("/bills/connections/"+id, map[string]any{"credential_source": "session"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "session", moved["credential_source"])
	return id
}

// signInThroughTheAgentSending is the same connect with the sign-in body the
// caller wants, for the fields only one test sends.
func signInThroughTheAgentSending(
	t *testing.T, alex *client, agent *fakeBillsAgent, label string, body map[string]any,
) string {
	t.Helper()
	id := newBillConnection(alex, map[string]any{"label": label})["id"].(string)
	signInConnection(t, alex, id, body)
	return id
}

// signInConnection runs a connect to the end on a connection that exists.
func signInConnection(t *testing.T, alex *client, id string, body map[string]any) {
	t.Helper()
	step := alex.post("/bills/connections/"+id+"/sign-in", body).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "otp", step["state"])
	require.Equal(t, "sms", step["method"])
	require.Equal(t, "c1", step["session_id"])

	wrong := alex.post("/bills/connections/"+id+"/sign-in/c1/answer",
		map[string]any{"code": "000000"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "otp", wrong["state"], "a wrong code asks again")

	done := alex.post("/bills/connections/"+id+"/sign-in/c1/answer",
		map[string]any{"code": "314159"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "signed_in", done["state"])

	saved := alex.post("/bills/connections/"+id+"/sign-in/c1/complete", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, saved["pulling"], "a finished sign-in starts a pull")
	awaitBillPull(t, id)
	require.Equal(t, true, saved["connected"])
	require.NotNil(t, saved["signed_in_at"])
	subaccounts := saved["subaccounts"].([]any)
	require.Len(t, subaccounts, 1)
	require.Equal(t, "premise-7", subaccounts[0].(map[string]any)["external_id"])
}

// awaitBillPull waits out the pull a finished sign-in starts in the
// background, so what a test does next is not refused as a second pull of the
// same connection, and nothing is still writing when the test's database goes.
func awaitBillPull(t *testing.T, id string) {
	t.Helper()
	connection := uuid.MustParse(id)
	require.Eventually(t, func() bool { return !service.BillPullRunning(connection) },
		5*time.Second, 2*time.Millisecond, "the pull a sign-in started never finished")
}

// A step an engine refuses is not a fault of this server.
//
// When the household presses Connect on a connection running the built-in
// engine, the dialog opens on the live browser the way it does at a browser
// provider, and the engine may refuse the step. A refusal returned as a 502
// shows as "The server had a problem", because the client shows no body at
// 500 and above, and leaves nothing to act on. What this holds is the
// mapping: a 400 from the engine reaches the household as a 400 carrying the
// engine's own words.
func TestARefusedLiveSignInIsNotABadGateway(t *testing.T) {
	l := buildLedger(t)
	env := NewEnv(testConfig(), db(t), WithClock(func() time.Time { return seriesClock }))
	alex := (&client{t: t, env: env, handler: RouterFor(env)}).
		as(l.users["alex"]).inSpace(store.SpaceIDOf(l.id("space")))

	// A provider reached over HTTP with a kept token has no sign-in page at
	// all, so there is nothing for a live browser to open.
	connection := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerAlliant), "label": "Main account",
	})
	id := connection["id"].(string)

	refused := alex.post("/bills/connections/"+id+"/sign-in",
		map[string]any{"mode": "live", "width": 1280, "height": 900}).
		requireStatus(http.StatusBadRequest).json()
	detail := fmt.Sprint(refused["detail"])
	require.Contains(t, detail, "no sign-in page",
		"the engine's own words, not a gateway failure")
	require.Contains(t, detail, "username and password", "and what to do instead")
	require.NotContains(t, detail, "the merchant agent answered",
		"the wrapper sentence is for the log, not for the household")
}

func TestWithoutAnEngineTheBillsResourceTakesTypedBillsOnly(t *testing.T) {
	l := buildLedger(t)
	env := NewEnv(testConfig(), db(t), WithClock(func() time.Time { return seriesClock }))
	env.BillsAgent = noBillsAgent{&fakeBillsAgent{}}
	alex := (&client{t: t, env: env, handler: RouterFor(env)}).
		as(l.users["alex"]).inSpace(store.SpaceIDOf(l.id("space")))
	connection := newBillConnection(alex, nil)

	status := alex.get("/bills/agent").requireStatus(http.StatusOK).json()
	require.Equal(t, false, status["configured"])
	require.Equal(t, false, status["healthy"])
	require.Contains(t, status["error"], "no browser engine")

	alex.post("/bills/connections/"+connection["id"].(string)+"/sign-in",
		map[string]any{"mode": "typed", "username": "alex", "password": "pw"}).
		requireStatus(http.StatusConflict)
	alex.post("/bills/connections/"+connection["id"].(string)+"/pull", nil).
		requireStatus(http.StatusConflict)
}

func TestSigningInToABillProviderAnswersItsCodeAndKeepsTheSession(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)

	status := alex.get("/bills/agent").requireStatus(http.StatusOK).json()
	require.Equal(t, true, status["configured"])
	require.Equal(t, true, status["healthy"])
	providers := status["providers"].([]any)
	require.Len(t, providers, 1)
	require.Equal(t, "Spectrum", providers[0].(map[string]any)["name"])

	id := signInThroughTheAgent(t, alex, agent)

	// A session belongs to the connection that started it.
	other := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerErie), "label": "Main vehicle",
	})
	alex.get("/bills/connections/" + other["id"].(string) + "/sign-in/c1").
		requireStatus(http.StatusNotFound)

	// Nothing about the sign-in comes back as a credential.
	listed := alex.get("/bills/connections").requireStatus(http.StatusOK).list()
	for _, row := range listed {
		for field := range row {
			require.NotContains(t, []string{"session_state", "password", "token"}, field)
		}
	}
	require.NotContains(t, alex.get("/bills/connections").requireStatus(http.StatusOK).
		Body.String(), "kept-1")

	// Disconnecting gives the session back and asks for a sign-in.
	forgotten := alex.del("/bills/connections/" + id + "/session").
		requireStatus(http.StatusOK).json()
	require.Equal(t, false, forgotten["connected"])
	require.Equal(t, true, forgotten["needs_sign_in"])
}

func TestALiveBillSignInOpensABrowserOfTheAskedSize(t *testing.T) {
	// The developer steers in docs/connectors/bills.md start here.
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	connection := newBillConnection(alex, nil)
	id := connection["id"].(string)

	live := alex.post("/bills/connections/"+id+"/sign-in", map[string]any{
		"mode": "live", "width": 800, "height": 600,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "interactive", live["state"])
	require.EqualValues(t, 800, live["width"])
}

// What the provider showed, round by round, for the household to paste to
// somebody. The dialog reads it on a failure; this route is how it is read
// while the sign-in is still open.
func TestTheSignInTrailSaysWhatTheProviderShowedAndNothingThatWasTyped(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	connection := newBillConnection(alex, map[string]any{"label": "Main account"})
	id := connection["id"].(string)

	alex.post("/bills/connections/"+id+"/sign-in", map[string]any{
		"mode": "typed", "username": "alex", "password": "hunter2",
	}).requireStatus(http.StatusOK)

	trail := alex.get("/bills/connections/" + id + "/sign-in/c1/trail").
		requireStatus(http.StatusOK).list()
	require.Len(t, trail, 2)
	round := trail[0]
	require.Equal(t, "sign-in", round["step"])
	require.Equal(t, "password", round["state"])
	require.Equal(t, "https://www.spectrum.net/login", round["url"])
	require.Equal(t, true, round["form"].(map[string]any)["password"])
	require.Contains(t, round["error"], "not recognized")
	require.NotContains(t, fmt.Sprint(round), "hunter2")

	// What the round did, which is half the line: the dialog draws it, and a
	// trail without it would account for a sign-in that pressed a key the form
	// ignored by saying nothing at all.
	did := round["did"].(map[string]any)
	require.Equal(t, true, did["acted"])
	require.Equal(t, "button", did["pressed"])
	require.Equal(t, "Sign In", did["words"])
	require.Equal(t, true, did["changed"])

	// And what a factor page was offering, which is the same answer for the
	// round that asks which way to verify.
	factor := trail[1]
	require.Equal(t, "factor", factor["state"])
	require.Equal(t, "Text message", factor["chose"])
	offered := factor["choices"].([]any)
	require.Len(t, offered, 1)
	require.Equal(t, "radio", offered[0].(map[string]any)["kind"])
	require.Equal(t, "Text message", offered[0].(map[string]any)["words"])

	// A sign-in is readable only from the connection it was started on.
	other := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerErie), "label": "Main vehicle",
	})
	alex.get("/bills/connections/" + other["id"].(string) + "/sign-in/c1/trail").
		requireStatus(http.StatusNotFound)
}

func TestTheAssistantCannotReadOrSteerALiveSignIn(t *testing.T) {
	// Everything under the sign-in is the one thing the assistant may not
	// start: mid-credential, at the household's own portal, with whatever the
	// provider has put on the screen. The page reading is one; the four
	// developer steers are four more, and each of them drives a browser that
	// is signed in to a real account.
	for _, route := range dispatchableRoutes() {
		require.NotContains(t, route.Path(), "/sign-in/{session}",
			"%s %s is reachable from an in-process call", route.Method, route.Path())
	}
	const connection = "/bills/connections/8c6b1f33-0000-4000-8000-000000000001/sign-in/c1/"
	for _, step := range []string{"page", "goto", "click", "dom", "fetch", "input", "trail"} {
		require.Error(t, refuseDeniedPath(connection+step), step)
	}
}

// And the steers are the owner's, not a writer's. They are Write-registered,
// so a viewer never reaches the handler at all; the owner check inside is what
// separates them from every other write on the resource.
func TestOnlyAnOwnerMaySteerALiveSignIn(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	id := signInThroughTheAgent(t, alex, agent)

	vera := (&client{t: t, env: alex.env, handler: alex.handler}).
		as(l.users["vera"]).inSpace(store.SpaceIDOf(l.id("space")))
	for _, step := range []string{"goto", "click", "dom", "fetch"} {
		vera.post("/bills/connections/"+id+"/sign-in/c1/"+step,
			map[string]any{"url": "https://www.alliantenergy.test/", "selector": "a", "text": "x"}).
			requireStatus(http.StatusForbidden)
	}
}

func TestSigningInPullsTheBillAndFilesTheStatement(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	agent.document = true
	alex := billsClient(l, agent)
	// The sign-in starts the first pull itself, and it has finished by the
	// time this answers.
	id := signInThroughTheAgent(t, alex, agent)

	connection := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "ok", connection["last_pull_status"])
	require.NotNil(t, connection["last_pulled_at"])
	require.Equal(t, false, connection["pulling"])

	subaccounts := alex.get("/bills/subaccounts?connection_id=" + id).
		requireStatus(http.StatusOK).list()
	require.Len(t, subaccounts, 1)
	bills := alex.get("/bills/subaccounts/" + subaccounts[0]["id"].(string) + "/bills").
		requireStatus(http.StatusOK).list()
	require.Len(t, bills, 1)
	require.Equal(t, "120.00", bills[0]["amount_due"])
	require.Equal(t, "2026-10-26", bills[0]["due_on"])
	require.Equal(t, "provider", bills[0]["source"])
	require.NotNil(t, bills[0]["document_id"])

	// The statement is in the document store, fetched by reference and served
	// back from here.
	content := alex.get("/documents/" + bills[0]["document_id"].(string) + "/content").
		requireStatus(http.StatusOK)
	require.Equal(t, storetest.PDF(), content.Body.Bytes())

	// Update now after it pulls the same cycle, which changes nothing and
	// asks the agent for no document it already holds.
	again := alex.post("/bills/connections/"+id+"/pull", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "ok", again["status"])
	require.EqualValues(t, 0, again["new"])
	require.EqualValues(t, 1, again["unchanged"])
	require.Nil(t, again["challenge"])

	agent.mu.Lock()
	defer agent.mu.Unlock()
	require.Len(t, agent.seen, 2)
	require.Contains(t, agent.seen[0], "kept-1", "the sign-in's pull used the sign-in's session")
	require.Contains(t, agent.seen[1], "rolled-2", "Update now used the one that pull was handed")
}

func TestAPulledChallengeIsListedAndAnsweredFromTheSettingsPage(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	id := signInThroughTheAgent(t, alex, agent)

	agent.mu.Lock()
	agent.challenge = true
	agent.mu.Unlock()

	parked := alex.post("/bills/connections/"+id+"/pull", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "challenge", parked["status"])
	challenge := parked["challenge"].(map[string]any)
	require.Equal(t, "sms", challenge["method"])
	require.Equal(t, "waiting", challenge["state"])
	require.Equal(t, id, challenge["connection_id"])
	require.Nil(t, challenge["answered_by"])
	require.Nil(t, challenge["answered_at"])
	require.NotEmpty(t, challenge["expires_at"])

	waiting := alex.get("/bills/challenges?state=waiting").requireStatus(http.StatusOK).list()
	require.Len(t, waiting, 1)
	require.Equal(t, challenge["id"], waiting[0]["id"])
	one := alex.get("/bills/challenges/" + challenge["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, challenge["prompt"], one["prompt"])

	// The notification's URL is the page that answers it.
	feed := alex.get("/notifications").requireStatus(http.StatusOK).json()
	first := feed["notifications"].([]any)[0].(map[string]any)
	require.Equal(t, "bill_challenge", first["alert_type"])
	require.Equal(t, "/settings/bills?challenge="+challenge["id"].(string), first["url"])

	// Answering resumes the pull the challenge stopped, so the answer's reply
	// is the pull's own result.
	agent.mu.Lock()
	agent.challenge = false
	agent.mu.Unlock()
	answered := alex.post("/bills/challenges/"+challenge["id"].(string)+"/answer",
		map[string]any{"code": "314159"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "ok", answered["status"])
	require.EqualValues(t, 1, answered["unchanged"], "the cycle the sign-in's own pull filed")

	after := alex.get("/bills/challenges/" + challenge["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "answered", after["state"])
	require.Equal(t, "person", after["answered_by"])
	require.NotNil(t, after["answered_at"])

	// An empty code is refused for a texted code, whatever the dialog sends.
	alex.post("/bills/challenges/"+challenge["id"].(string)+"/answer",
		map[string]any{"code": ""}).requireStatus(http.StatusUnprocessableEntity)
}

func TestTheBillSignInRoutesAreNotReachableInProcess(t *testing.T) {
	// The assistant proposes what a person could approve. These are the routes
	// where the thing proposed is a credential being used, so they are refused
	// to an in-process call and are not in the list the model is given.
	for _, route := range dispatchableRoutes() {
		if !strings.HasPrefix(route.Path(), "/bills") {
			continue
		}
		for _, refused := range []string{"sign-in", "/session", "/answer"} {
			require.NotContains(t, route.Path(), refused,
				"%s %s is reachable from an in-process call", route.Method, route.Path())
		}
	}
	for _, path := range []string{
		"/bills/connections/8c6b1f33-0000-4000-8000-000000000001/sign-in",
		"/bills/connections/8c6b1f33-0000-4000-8000-000000000001/sign-in/retry",
		"/bills/connections/8c6b1f33-0000-4000-8000-000000000001/sign-in/c1/goto",
		"/bills/connections/8c6b1f33-0000-4000-8000-000000000001/sign-in/c1/trail",
		"/bills/connections/8c6b1f33-0000-4000-8000-000000000001/sign-in/c1",
		"/bills/connections/8c6b1f33-0000-4000-8000-000000000001/session",
		"/bills/challenges/8c6b1f33-0000-4000-8000-000000000002/answer",
	} {
		require.Error(t, refuseDeniedPath(path), "%s is reachable", path)
	}
	// The rest of the resource still is: a pull is a sync somebody could press
	// themselves, and the bills a household keeps are ordinary reads.
	require.NoError(t, refuseDeniedPath("/bills/connections"))
	require.NoError(t, refuseDeniedPath(
		"/bills/connections/8c6b1f33-0000-4000-8000-000000000001/pull"))
}

func TestAKeptPasswordSignsInUnattendedAndNeverComesBack(t *testing.T) {
	// The whole point of keeping one: the nightly pull carries it to the
	// agent, and nothing else ever sees it — not a listing, not the row in
	// clear, not the pull that ran without asking.
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)

	withoutOne := signInKeepingOnlyTheSession(t, alex, agent)
	id := signInThroughTheAgentAs(t, alex, agent, "Second account")

	kept := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "stored", kept["credential_source"])
	require.Equal(t, "session",
		alex.get("/bills/connections/" + withoutOne).requireStatus(http.StatusOK).json()["credential_source"])

	listing := alex.get("/bills/connections").requireStatus(http.StatusOK).Body.String()
	require.NotContains(t, listing, "hunter2")
	var sealed string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT credential_sealed FROM bill_connections WHERE id = $1`, id).Scan(&sealed))
	require.NotContains(t, sealed, "hunter2")
	require.NotContains(t, sealed, "alex")

	// Each sign-in pulled once already, carrying what it had.
	require.Len(t, agent.credentials, 2)
	alex.post("/bills/connections/"+withoutOne+"/pull", nil).requireStatus(http.StatusOK)
	require.Nil(t, agent.credentials[2], "a connection that kept nothing sends nothing")

	result := alex.post("/bills/connections/"+id+"/pull", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, "ok", result["status"])
	require.Equal(t, map[string]string{"username": "alex", "password": "hunter2"}, agent.credentials[3])

	// Forgetting the session alone is not disconnecting: the password signs
	// in again on the next pull, which is what it is kept for.
	alex.del("/bills/connections/" + id + "/session").requireStatus(http.StatusOK)
	again := alex.post("/bills/connections/"+id+"/pull", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, "ok", again["status"])
	require.Equal(t, "hunter2", agent.credentials[4]["password"])

	// Forgetting the password is.
	gone := alex.del("/bills/connections/" + id + "/credential").requireStatus(http.StatusOK).json()
	require.Equal(t, "session", gone["credential_source"])
	require.Equal(t, false, gone["connected"])
	stopped := alex.post("/bills/connections/"+id+"/pull", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, "needs_sign_in", stopped["status"])
	require.Contains(t, stopped["error"], "sign in first")
	require.Len(t, agent.credentials, 5, "nothing went to the agent")
}

func TestASignInNobodyFinishedIsGivenUpRatherThanHeldOpen(t *testing.T) {
	// The agent holds one browser per connection, so an abandoned sign-in
	// refuses the next one until its reaper gets to it. Dismissing the dialog
	// says so, and the password held for the keep goes with it.
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	id := newBillConnection(alex, nil)["id"].(string)

	step := alex.post("/bills/connections/"+id+"/sign-in", map[string]any{
		"mode": "typed", "username": "alex", "password": "hunter2",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "c1", step["session_id"])

	given := alex.del("/bills/connections/" + id + "/sign-in/c1").
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, given["cancelled"])
	require.Equal(t, []string{"c1"}, agent.cancelled)

	// The session is nobody's now: finishing it, or giving it up twice, is a
	// 404 rather than a second word to the agent.
	alex.post("/bills/connections/"+id+"/sign-in/c1/complete", nil).
		requireStatus(http.StatusNotFound)
	alex.del("/bills/connections/" + id + "/sign-in/c1").requireStatus(http.StatusNotFound)
	require.Len(t, agent.cancelled, 1)

	// Nothing was kept: the connection is where it was before the dialog.
	connection := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "session", connection["credential_source"])
	require.Equal(t, false, connection["connected"])
}

// A sign-in that stopped on something passing is run again with what was
// typed into it, after its dialog was closed, until one lands.
func TestASignInThatDidNotLandIsRetriedWithWhatWasTyped(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	id := newBillConnection(alex, nil)["id"].(string)

	alex.post("/bills/connections/"+id+"/sign-in/retry", nil).requireStatus(http.StatusConflict)
	fresh := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, false, fresh["can_retry_sign_in"], "nothing was ever typed")

	alex.post("/bills/connections/"+id+"/sign-in", map[string]any{
		"mode": "typed", "username": "alex", "password": "hunter2",
	}).requireStatus(http.StatusOK)
	alex.del("/bills/connections/" + id + "/sign-in/c1").requireStatus(http.StatusOK)

	closed := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, true, closed["can_retry_sign_in"])
	require.Equal(t, "session", closed["credential_source"], "nothing was sealed")

	step := alex.post("/bills/connections/"+id+"/sign-in/retry", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, "c1", step["session_id"])
	require.Equal(t, "otp", step["state"])
	require.Len(t, agent.connects, 2)
	require.Equal(t, "alex", agent.connects[1]["username"])
	require.Equal(t, "hunter2", agent.connects[1]["password"])

	alex.post("/bills/connections/"+id+"/sign-in/c1/answer", map[string]any{"code": "314159"}).
		requireStatus(http.StatusOK)
	alex.post("/bills/connections/"+id+"/sign-in/c1/complete", nil).requireStatus(http.StatusOK)
	awaitBillPull(t, id)

	landed := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, false, landed["can_retry_sign_in"], "a landed sign-in is not retried")
	require.Equal(t, "stored", landed["credential_source"])
	alex.post("/bills/connections/"+id+"/sign-in/retry", nil).requireStatus(http.StatusConflict)
}

// What was typed is the typist's, for the site it was typed at: another member
// cannot send it, and a site edited since would receive a password nobody
// typed into it.
func TestARetryIsOnlyTheTypistsAndOnlyAtTheSiteItWasTypedFor(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	space := store.SpaceIDOf(l.id("space"))
	membership, err := alex.env.DB.GetMembership(context.Background(), space, l.users["vera"].ID)
	require.NoError(t, err)
	membership.Role = store.RoleMember
	require.NoError(t, alex.env.DB.UpdateMembership(context.Background(), space, &membership))
	vera := (&client{t: t, env: alex.env, handler: alex.handler}).as(l.users["vera"]).inSpace(space)
	id := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerCommunityConnect), "site": "springfield",
	})["id"].(string)

	alex.post("/bills/connections/"+id+"/sign-in", typedSignIn()).requireStatus(http.StatusOK)
	alex.del("/bills/connections/" + id + "/sign-in/c1").requireStatus(http.StatusOK)

	seen := vera.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, false, seen["can_retry_sign_in"], "vera typed nothing")
	vera.post("/bills/connections/"+id+"/sign-in/retry", nil).requireStatus(http.StatusConflict)
	require.Len(t, agent.connects, 1)

	alex.patch("/bills/connections/"+id, map[string]any{"site": "shelbyville"}).
		requireStatus(http.StatusOK)
	moved := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, false, moved["can_retry_sign_in"])
	alex.post("/bills/connections/"+id+"/sign-in/retry", nil).requireStatus(http.StatusConflict)
	require.Len(t, agent.connects, 1, "nothing was sent to the new site")
}

func TestAKeptAuthenticatorKeyMintsTheCodeAndIsSealedWithThePassword(t *testing.T) {
	// A provider that asks for a second factor at every sign-in can only be
	// pulled while everyone is asleep if Agentifi holds the setup key — so it
	// is sealed with the password, sent to the agent as a key rather than a
	// code, and never comes back.
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)

	// An invented key: base32, and the only one in this repository.
	const key = "JBSWY3DPEHPK3PXP"
	id := signInThroughTheAgentSending(t, alex, agent, "Main account", map[string]any{
		"mode": "typed", "username": "alex", "password": "hunter2",
		"totp_secret": key,
	})

	// With a key and no code typed, the first code is minted here: a provider
	// that asks in the sign-in call itself would otherwise need a phone. The
	// key goes with it, because the page the agent opened can ask again.
	require.Len(t, agent.connects, 1)
	require.Regexp(t, `^\d{6}$`, agent.connects[0]["totp"], "the connect carried a minted code")
	require.Equal(t, key, agent.connects[0]["totp_secret"])

	kept := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, true, kept["has_totp"])
	require.Equal(t, "stored", kept["credential_source"])
	listing := alex.get("/bills/connections").requireStatus(http.StatusOK).Body.String()
	require.NotContains(t, listing, key)
	var sealed string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT credential_sealed FROM bill_connections WHERE id = $1`, id).Scan(&sealed))
	require.NotContains(t, sealed, key)

	// The pull sends the key, not a code: the engine mints one at the moment
	// it signs in, which can be minutes after this.
	alex.post("/bills/connections/"+id+"/pull", nil).requireStatus(http.StatusOK)
	require.Equal(t, map[string]string{
		"username": "alex", "password": "hunter2", "totp_secret": key,
	}, agent.credentials[0])

	// Forgetting the password forgets the key with it.
	gone := alex.del("/bills/connections/" + id + "/credential").requireStatus(http.StatusOK).json()
	require.Equal(t, false, gone["has_totp"])
}

func TestAnAuthenticatorKeyIsRefusedUnlessItIsASetupKey(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	id := newBillConnection(alex, nil)["id"].(string)

	// A six-digit code pasted into the key field is the mistake worth catching
	// here rather than at four in the morning.
	alex.post("/bills/connections/"+id+"/sign-in", map[string]any{
		"mode": "typed", "username": "alex", "password": "hunter2",
		"totp_secret": "182931",
	}).requireStatus(http.StatusUnprocessableEntity)
	require.Empty(t, agent.connects, "it never reached the agent")
}

func TestAKeptPasswordSurvivesANeedsSignInAndARefusalIsMarkedNotForgotten(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	id := signInThroughTheAgentAs(t, alex, agent, "Main account")

	// A lapsed session is no fault of the password.
	agent.refuse = true
	result := alex.post("/bills/connections/"+id+"/pull", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, "needs_sign_in", result["status"])
	require.Equal(t, "Spectrum did not accept the sign-in", result["error"])
	connection := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "stored", connection["credential_source"])
	require.Equal(t, true, connection["needs_sign_in"])
	require.Equal(t, "", connection["sign_in_paused"])

	// The engine tried the password and was turned away: kept, and marked.
	agent.refuse = false
	agent.refusePassword = true
	result = alex.post("/bills/connections/"+id+"/pull", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, "needs_sign_in", result["status"])
	require.Equal(t,
		"Spectrum did not accept the kept password (Invalid username or password). The password is "+
			"still kept, but it will not be tried on its own again until you sign in, change it, "+
			"or press Update now.",
		result["error"])
	connection = alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "stored", connection["credential_source"])
	require.Equal(t, "password_refused", connection["sign_in_paused"])

	// A person pressing Update now is a person asking for one more try.
	agent.refusePassword = false
	alex.post("/bills/connections/"+id+"/pull", nil).requireStatus(http.StatusOK)
	require.Equal(t, "hunter2", agent.credentials[2]["password"])
	connection = alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "", connection["sign_in_paused"])
	require.Equal(t, false, connection["needs_sign_in"])
}

func TestAKeptPasswordIsMadeBySigningInNotBySettings(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)

	// Moving off a kept password forgets it: asking for it back has nothing
	// to seal.
	id := signInKeepingOnlyTheSession(t, alex, agent)
	alex.patch("/bills/connections/"+id, map[string]any{"credential_source": "stored"}).
		requireStatus(http.StatusUnprocessableEntity)
	alex.post("/bills/connections/"+id+"/pull", nil).requireStatus(http.StatusOK)
	require.Nil(t, agent.credentials[len(agent.credentials)-1])

	// Signing in again keeps it.
	signInConnection(t, alex, id, typedSignIn())
	again := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "stored", again["credential_source"])
	alex.post("/bills/connections/"+id+"/pull", nil).requireStatus(http.StatusOK)
	require.Equal(t, "hunter2", agent.credentials[len(agent.credentials)-1]["password"])
}

// Releasing a connection's browser, which is the way out of the one failure a
// household could not clear for itself.
//
// Two holds and one button: the sign-in this process is holding, and the lock
// and singleton a container that died mid-sign-in left on the profiles volume.
// The answer says which of them was there, because "there was nothing to
// release" is a normal outcome and has to read as reassurance.
func TestReleasingAConnectionsBrowserSaysWhatItGaveUp(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	agent.gaveUp = provider.BillProfileRelease{
		Sessions: []string{"c1"}, Lock: true, Singleton: true,
	}
	alex := billsClient(l, agent)
	id := newBillConnection(alex, nil)["id"].(string)

	released := alex.del("/bills/connections/" + id + "/browser").
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, released["released"])
	require.EqualValues(t, 1, released["sessions"])
	require.Equal(t, true, released["lock"])
	require.Equal(t, true, released["singleton"])
	require.Contains(t, released["message"], "closed the sign-in")
	require.Contains(t, released["message"], "gave up the lock")
	require.Contains(t, released["message"], "kept session and password are untouched")
	// The connection's own profile, and nothing else on the volume.
	require.Equal(t, []string{id}, agent.releases)

	// Nothing kept was given up with it: this is not either of the forgets.
	connection := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "session", connection["credential_source"])

	// And the same press when the hold had already timed out.
	agent.gaveUp = provider.BillProfileRelease{}
	nothing := alex.del("/bills/connections/" + id + "/browser").
		requireStatus(http.StatusOK).json()
	require.Equal(t, false, nothing["released"])
	require.Contains(t, nothing["message"], "there was nothing to release")
	require.Contains(t, nothing["message"], "Sign in again")
}

// The refusal the cross-process lock exists for: on a deploy the container
// being replaced is still driving a real Chromium on that directory, and two
// on one user data directory corrupt it. The engine's own sentence says so and
// says when to try again.
func TestReleasingABrowserSomethingIsStillDrivingIsRefused(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	agent.driven = true
	alex := billsClient(l, agent)
	id := newBillConnection(alex, nil)["id"].(string)

	refused := alex.del("/bills/connections/" + id + "/browser").
		requireStatus(http.StatusConflict).json()
	require.Contains(t, refused["detail"], "still driving")
}

// Owner's work, and not the assistant's. It reaches across to the profiles
// volume and takes a lock off it, which is a thing done to a server with the
// connection in front of you.
func TestOnlyAnOwnerMayReleaseAConnectionsBrowser(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	id := newBillConnection(alex, nil)["id"].(string)

	vera := (&client{t: t, env: alex.env, handler: alex.handler}).
		as(l.users["vera"]).inSpace(store.SpaceIDOf(l.id("space")))
	vera.del("/bills/connections/" + id + "/browser").requireStatus(http.StatusForbidden)
	require.Empty(t, agent.releases)

	require.Error(t, refuseDeniedPath(
		"/bills/connections/8c6b1f33-0000-4000-8000-000000000001/browser"))

	// A connection is reachable only from its own space, so the browser behind
	// one is too: another household's owner names no connection here.
	bob := (&client{t: t, env: alex.env, handler: alex.handler}).
		as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	bob.del("/bills/connections/" + id + "/browser").requireStatus(http.StatusNotFound)
	require.Empty(t, agent.releases)
}

// A message worded to be read correctly on the first pass.
//
// Dismissing the dialog would give up nothing, since the hold is a lock file
// a dead process left; twenty minutes is the session reaper's own timeout,
// not this lock's. The refusal names one thing to do, and it is a button.
func TestTheAlreadyOpenRefusalPointsAtTheRelease(t *testing.T) {
	refused := billAgentError(&provider.AgentError{
		Kind:    provider.ErrAgentConflict,
		Message: "that connection is already open in the agent; finish or close that sign-in first",
	})
	require.Contains(t, refused.Error(), "Release the browser")
	require.Contains(t, refused.Error(), "restart left its browser locked")
	require.NotContains(t, refused.Error(), "Dismissing")
	require.NotContains(t, refused.Error(), "twenty minutes")
}

// How a login's second factor is answered is a choice offered at every
// provider: kept on the connection when the sign-in starts, said back on the
// connection as a word and never as a key, handed to the engine, and nothing
// but the words it can be.
func TestASecondFactorChoiceIsKeptOnTheConnectionAndSaidBack(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	id := newBillConnection(alex, nil)["id"].(string)

	alex.post("/bills/connections/"+id+"/sign-in", map[string]any{
		"mode": "typed", "username": "alex", "password": "hunter2",
		"second_factor": "carrier-pigeon",
	}).requireStatus(http.StatusUnprocessableEntity)
	alex.post("/bills/connections/"+id+"/sign-in", map[string]any{
		"mode": "typed", "username": "alex", "password": "hunter2",
		"second_factor": "sms",
	}).requireStatus(http.StatusUnprocessableEntity)
	require.Empty(t, agent.connects, "a text is typed in by hand, never chosen as the way")

	alex.post("/bills/connections/"+id+"/sign-in", map[string]any{
		"mode": "typed", "username": "alex", "password": "hunter2",
		"second_factor": "email",
	}).requireStatus(http.StatusOK)
	require.Len(t, agent.connects, 1)
	require.Equal(t, "email", agent.connects[0]["second_factor"])

	kept := alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "email", kept["second_factor"])
}

// A typed sign-in's code can be watched for in the mailbox, as a shop's can; a
// space with no mailbox answers that there is none, which the dialog reads as
// nothing arriving and leaves the code to the person.
func TestABillSignInsMailedCodeWithNoMailboxIsAConflictNotAFailure(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	id := newBillConnection(alex, nil)["id"].(string)
	state := alex.post("/bills/connections/"+id+"/sign-in", map[string]any{
		"mode": "typed", "username": "alex", "password": "hunter2",
		"second_factor": "email",
	}).requireStatus(http.StatusOK).json()

	alex.post("/bills/connections/"+id+"/sign-in/"+state["session_id"].(string)+"/mailed-code", nil).
		requireStatus(http.StatusConflict)
	require.Empty(t, agent.codes, "nothing was typed into the sign-in")
}

func (a *fakeBillsAgent) SignInInput(ctx context.Context, sessionID string, events []provider.BillLiveInput) error {
	return nil
}
