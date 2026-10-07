package connector

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The engine's tests drive fake providers: what is tested is the flow, not a
// provider's website. The fakes carry catalogued ids so the facts the engine
// reads (documents, keepalive) come from domain.Billers.

// fakeAPI is an api provider under Alliant's id.
type fakeAPI struct {
	kind string

	// The answers, in the order a pull asks for them.
	authenticate func(credentials billers.Credentials) billers.SignIn
	refresh      func(call billers.Call) (billers.Session, bool, string)
	fetchBills   func(call billers.Call) (billers.Pull, error)
	subaccounts  []billers.Subaccount
	document     *billers.Document

	// What happened.
	authenticated int
	refreshed     int
	fetched       int
	documents     int
	lastCall      billers.Call
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		kind: "fake-token",
		subaccounts: []billers.Subaccount{
			{ExternalID: "one", Label: "One", MaskedNumber: "••••1234"},
		},
	}
}

func (f *fakeAPI) ID() domain.BillerID { return domain.BillerAlliant }

func (f *fakeAPI) SessionKinds() []string { return []string{f.kind} }

func (f *fakeAPI) BrowserOrigin() (string, string) { return "", "" }

func (f *fakeAPI) Authenticate(ctx context.Context, credentials billers.Credentials, call billers.Call) billers.SignIn {
	f.authenticated++
	f.lastCall = call
	if f.authenticate != nil {
		return f.authenticate(credentials)
	}
	return billers.SignIn{Session: fakeSession(f.kind, time.Now().Add(time.Hour), false)}
}

func (f *fakeAPI) Refresh(call billers.Call) (billers.Session, bool, string) {
	f.refreshed++
	f.lastCall = call
	if f.refresh != nil {
		return f.refresh(call)
	}
	return fakeSession(f.kind, time.Now().Add(time.Hour), false), false, ""
}

func (f *fakeAPI) Subaccounts(call billers.Call) ([]billers.Subaccount, error) {
	f.lastCall = call
	return f.subaccounts, nil
}

func (f *fakeAPI) FetchBills(call billers.Call) (billers.Pull, error) {
	f.fetched++
	f.lastCall = call
	if f.fetchBills != nil {
		return f.fetchBills(call)
	}
	return billers.Pull{Bills: []billers.Bill{fakeBill()}}, nil
}

func (f *fakeAPI) FetchDocument(call billers.Call, bill billers.Bill) (*billers.Document, error) {
	f.documents++
	if f.document == nil {
		return nil, nil
	}
	return f.document, nil
}

// fakeBrowser is a browser provider under Erie's id.
type fakeBrowser struct {
	billers.Draft

	classify     func(page browser.Page) (billers.State, error)
	fillEmail    func(page browser.Page, email string) (agent.Step, error)
	chooseFactor func(page browser.Page) (agent.Factor, error)
	// preferred is the login's preference each choice of factor was given.
	preferred  []string
	fetchBills func(call billers.Call) (billers.Pull, error)

	fetched int
}

func newFakeBrowser() *fakeBrowser {
	return &fakeBrowser{Draft: billers.Draft{
		BillerID: domain.BillerErie,
		Home:     "https://example.test",
		SignIn:   "https://example.test/login",
		Landing:  "https://example.test/account",
		AccountArea: func(url string) bool {
			return url == "https://example.test/account"
		},
	}}
}

func (f *fakeBrowser) Classify(page browser.Page) (billers.State, error) {
	if f.classify != nil {
		return f.classify(page)
	}
	return billers.State{State: billers.StateSignedIn}, nil
}

// FillEmail is the Draft's unless a test sets what the round did.
func (f *fakeBrowser) FillEmail(page browser.Page, email string) (agent.Step, error) {
	if f.fillEmail != nil {
		return f.fillEmail(page, email)
	}
	return f.Draft.FillEmail(page, email)
}

// ChooseFactor is the Draft's unless a test sets what the menu offered.
func (f *fakeBrowser) ChooseFactor(page browser.Page, prefer string) (agent.Factor, error) {
	f.preferred = append(f.preferred, prefer)
	if f.chooseFactor != nil {
		return f.chooseFactor(page)
	}
	return f.Draft.ChooseFactor(page, prefer)
}

func (f *fakeBrowser) FetchBills(call billers.Call) (billers.Pull, error) {
	f.fetched++
	if f.fetchBills != nil {
		return f.fetchBills(call)
	}
	return billers.Pull{Bills: []billers.Bill{fakeBill()}}, nil
}

// fakeSession is a kept token in the shape the engine reads two keys out of.
func fakeSession(kind string, expires time.Time, answered bool) billers.Session {
	encoded, _ := json.Marshal(map[string]any{
		"kind":         kind,
		"expires_at":   expires.UTC().Format(time.RFC3339),
		"account_hint": "Alex",
		"answered":     answered,
	})
	return billers.Session(encoded)
}

func fakeBill() billers.Bill {
	return billers.Bill{
		Subaccount: "one", ExternalID: "fake-1",
		IssuedOn: "2026-09-01", DueOn: "2026-09-20",
		AmountDue: domain.MustFromString("10.00"), Currency: "USD", Status: "open",
	}
}

// testEngine is an engine with no Chromium behind it: the browser is a stub
// page and the module's HTTP caller is whatever the test hands it.
func testEngine(t *testing.T, module billers.Module, page *browser.StubPage) Bills {
	t.Helper()
	engine, _ := testLiveEngine(t, module, page)
	return engine
}

// testLiveEngine also hands back the live browser's fakes: the input surface
// and the CDP session that paints frames.
func testLiveEngine(
	t *testing.T, module billers.Module, page *browser.StubPage,
) (Bills, *liveFakes) {
	t.Helper()
	engine := Bills{&Engine{
		Billers:      billers.NewWith(module),
		ProfilesRoot: t.TempDir(),
		Now:          time.Now,
	}}
	fakes := &liveFakes{}
	opened := 0
	closed := 0
	engine.Open = func(open agent.Open) (*OpenBrowser, error) {
		profile, viewport := open.Profile, open.Viewport
		if page == nil {
			t.Fatalf("a browser was opened for a provider that has none")
		}
		opened++
		return &OpenBrowser{
			Page: page, Dir: profile, Viewport: viewport,
			Live: func() (*browser.LiveView, error) {
				fakes.Surface = browser.NewStubSurface(page.Location)
				fakes.Cast = &browser.StubCaster{}
				view := browser.NewLiveView(fakes.Surface, viewport)
				if err := view.Attach(fakes.Cast); err != nil {
					return nil, err
				}
				return view, nil
			},
			StorageState: func() ([]byte, error) { return []byte(`{"cookies":[],"origins":[]}`), nil },
			PinCookies:   func(note func(string)) { note("1 session cookie pinned") },
			Close:        func() { closed++ },
		}, nil
	}
	engine.OpenFirefox = engine.Open
	engine.Fetcher = func(bool, string, string) (browser.Fetcher, func(), error) {
		return nil, func() {}, nil
	}
	t.Cleanup(func() {
		if opened != closed {
			t.Errorf("%d browsers opened and %d closed", opened, closed)
		}
	})
	return engine, fakes
}

// awaitSignIn is the dialog's poll: a typed sign-in drives its rounds on its
// own, so where it landed is what the poll finds once it settles.
func awaitSignIn(
	t *testing.T, engine Bills, state provider.BillConnectState,
) provider.BillConnectState {
	t.Helper()
	for round := 0; round < 400; round++ {
		if state.State != provider.BillConnectSigningIn {
			return state
		}
		time.Sleep(5 * time.Millisecond)
		polled, err := engine.ConnectStatus(context.Background(), state.SessionID)
		if err != nil {
			t.Fatalf("the sign-in could not be polled: %v", err)
		}
		state = polled
	}
	t.Fatalf("the sign-in was still working after two seconds")
	return state
}

// liveFakes are the live browser's two halves, set when a live sign-in opens
// its browser.
type liveFakes struct {
	Surface *browser.StubSurface
	Cast    *browser.StubCaster
}
