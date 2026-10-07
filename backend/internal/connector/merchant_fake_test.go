package connector

import (
	"encoding/json"
	"io"
	"log/slog"
	"sync"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/merchants"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
)

// A merchant that is not a merchant, for the engine's own flows: it answers
// the states a test scripted, records what was typed into it, and pulls
// whatever the test said it would.

type fakeModule struct {
	id domain.MerchantID
	// states is what Classify answers, in order; the last one repeats.
	states []merchants.State
	kinds  []string
	// fetch is the pull; nil answers an empty file.
	fetch func(call merchants.Call) (merchants.Result, error)

	mu       sync.Mutex
	read     int
	email    string
	password string
	// fills is how many times a password was typed, which is what holds a
	// pull to one try.
	fills    int
	answered string
	// acts says whether Answer acted on the code it was given.
	acts     bool
	attached int
	forgot   int
	captcha  string
}

func (f *fakeModule) ID() domain.MerchantID {
	if f.id == "" {
		return domain.MerchantAmazon
	}
	return f.id
}

func (f *fakeModule) SignInURL() string { return "https://merchant.test/sign-in" }

func (f *fakeModule) LandingURL() string { return "https://merchant.test/purchases" }

func (f *fakeModule) SignInPrompt() string { return "Sign in to the merchant below" }

func (f *fakeModule) SessionKinds() []string { return f.kinds }

func (f *fakeModule) Attach(page browser.Page) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attached++
}

func (f *fakeModule) Forget(page browser.Page) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgot++
}

func (f *fakeModule) Classify(page browser.Page) (merchants.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.states) == 0 {
		return merchants.State{State: merchants.StateInteractive}, nil
	}
	at := f.read
	if at >= len(f.states) {
		at = len(f.states) - 1
	}
	f.read++
	return f.states[at], nil
}

func (f *fakeModule) AccountHint(page browser.Page) (string, error) { return "Alex", nil }

func (f *fakeModule) FillEmail(page browser.Page, email string) (agent.Step, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.email = email
	return agent.Step{Acted: true, Changed: true}, nil
}

func (f *fakeModule) FillPassword(page browser.Page, password, email string) (agent.Step, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.password = password
	f.fills++
	return agent.Step{Acted: true, Changed: true}, nil
}

func (f *fakeModule) CaptchaImage(page browser.Page) (string, error) { return f.captcha, nil }

func (f *fakeModule) Answer(page browser.Page, where merchants.State, code, _ string) (agent.Step, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered = code
	return agent.Step{Acted: f.acts, Changed: f.acts}, nil
}

// ChooseFactor takes the emailed code where the module acts, and nothing
// where it does not.
func (f *fakeModule) ChooseFactor(page browser.Page, prefer string) (agent.Factor, error) {
	if !f.acts {
		return agent.Factor{}, nil
	}
	return agent.Factor{Kind: "email", Step: agent.Step{Acted: true, Changed: true}}, nil
}

func (f *fakeModule) Fetch(call merchants.Call) (merchants.Result, error) {
	if f.fetch != nil {
		return f.fetch(call)
	}
	return merchants.Result{Parsed: &merchantimport.Parsed{}}, nil
}

// openedBrowser is what a test's engine opens instead of Chromium.
type openedBrowser struct {
	page   *browser.StubPage
	jar    string
	closed int
	// opens is how many times the opener was called.
	opens  int
	seeded json.RawMessage
	// seeds is every state the opener was seeded with, in order.
	seeds []json.RawMessage
}

func (o *openedBrowser) opener() agent.Opener {
	return func(open agent.Open) (*OpenBrowser, error) {
		o.opens++
		o.seeded = open.State
		o.seeds = append(o.seeds, open.State)
		return &OpenBrowser{
			Page:         o.page,
			StorageState: func() ([]byte, error) { return []byte(o.jar), nil },
			Close:        func() { o.closed++ },
		}, nil
	}
}

// quietLog keeps a deliberate panic's stack out of the test output.
func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// signInOn runs the sign-in loop on a page nobody else holds, settled the way
// a merchant sign-in hands it to the dialog.
func signInOn(module merchants.Module, page browser.Page, email, password string) (merchants.State, error) {
	s := shopSession(module, nil)
	s.setLogin(billers.Credentials{Username: email, Password: password})
	s.Attach(&OpenBrowser{Page: page})
	where, err := (&Engine{}).advance(s)
	if err != nil {
		return where, err
	}
	return settled(s.name, where), nil
}
