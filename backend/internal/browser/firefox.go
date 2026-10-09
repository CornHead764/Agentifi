package browser

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/playwright-community/playwright-go"
)

// The second browser: Camoufox, a Firefox build running as a Playwright server
// in its own container (tools/camoufox), for providers whose sign-in works
// only in Firefox. It is chosen per provider, never as a fallback, so nothing
// is ever handed between browsers. Unlike Chrome:
//
//   - Scripts run in an isolated world: they see the DOM and the origin's
//     storage but not the page's JavaScript, so a hook on `fetch` or XHR
//     patches a window the app never calls. Read what the app stores instead.
//   - No profile on disk: a session is a storage state seeded into a fresh
//     context each time.
//   - No screencast, which is Chrome's DevTools protocol: its live view is
//     a throttled screenshot of the page with the typed fields covered
//     (StartFirefoxLiveView), which is enough for a person to tick a box.
//
// The client and the server must be the same Playwright version: the image
// pins the Python package to go.mod's playwright-go driver version.

var ErrNoFirefox = errors.New("browser: this provider runs in Camoufox and CAMOUFOX_URL is not set")

// FirefoxProvider is a provider every browser for which is Camoufox. With no
// Camoufox server configured it has none (ErrNoFirefox). A person drives such
// a page only to tick a page check; a developer's live sign-in is Chrome's.
type FirefoxProvider interface {
	RunsInFirefox() bool
}

// FirefoxOriginProvider is a FirefoxProvider whose calls outside a pull are
// made from a Camoufox page on a cheap document at its own origin.
type FirefoxOriginProvider interface {
	FirefoxProvider
	FirefoxOrigin() (origin, document string)
}

func RunsInFirefox(module any) bool {
	firefox, ok := module.(FirefoxProvider)
	return ok && firefox.RunsInFirefox()
}

// InFirefox says whether page is a Camoufox page: what a page can be left to
// do by itself differs between the two browsers.
func InFirefox(page Page) bool {
	switch p := page.(type) {
	case *livePage:
		return p.firefox
	case *StubPage:
		return p.Firefox
	}
	return false
}

func (e *Engine) HasFirefox() bool { return e.firefoxEndpoint() != "" }

func (e *Engine) firefoxEndpoint() string { return e.FirefoxEndpoint }

// Firefox is the connection to the Camoufox server, remade if it has dropped:
// the server's container restarts on its own.
func (e *Engine) Firefox() (playwright.Browser, error) {
	endpoint := e.firefoxEndpoint()
	if endpoint == "" {
		return nil, ErrNoFirefox
	}
	e.mu.Lock()
	if e.firefox != nil && e.firefox.IsConnected() {
		defer e.mu.Unlock()
		return e.firefox, nil
	}
	pw, err := e.start()
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	// Outside e.mu: a Camoufox that accepts the socket and never finishes the
	// handshake must not stall every Chrome launch in the process behind it.
	connected, err := pw.Firefox.Connect(endpoint, playwright.BrowserTypeConnectOptions{
		Timeout: playwright.Float(firefoxConnectTimeoutMS),
	})
	if err != nil {
		return nil, fmt.Errorf("browser: the Camoufox server would not connect: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.firefox != nil && e.firefox.IsConnected() {
		_ = connected.Close()
		return e.firefox, nil
	}
	e.firefox = connected
	return connected, nil
}

const firefoxConnectTimeoutMS = 15_000

// ClickTurnstile finds the Cloudflare Turnstile challenge frame by its URL
// and clicks the checkbox area inside it. The click is forced past
// Playwright's actionability checks because Turnstile renders its checkbox
// asynchronously inside the frame, so the body element never passes the
// visibility check.
func ClickTurnstile(page Page) {
	live, ok := page.(*livePage)
	if !ok {
		return
	}
	for _, frame := range live.page.Frames() {
		if !strings.Contains(frame.URL(), "challenges.cloudflare.com") {
			continue
		}
		x := 12 + rand.Float64()*20
		y := 22 + rand.Float64()*20
		_ = frame.Click("body", playwright.FrameClickOptions{
			Position: &playwright.Position{X: x, Y: y},
			Timeout:  playwright.Float(3000),
			Force:    playwright.Bool(true),
		})
		return
	}
}

// NewFirefoxContext sets no user agent, viewport, locale or consistency
// script: Camoufox presents one consistent desktop browser itself, and any
// override would contradict it.
func (e *Engine) NewFirefoxContext(kept StorageState) (playwright.BrowserContext, error) {
	b, err := e.Firefox()
	if err != nil {
		return nil, err
	}
	context, err := b.NewContext()
	if err != nil {
		return nil, fmt.Errorf("browser: no Camoufox context: %w", err)
	}
	context.SetDefaultTimeout(pageTimeoutMS)
	context.SetDefaultNavigationTimeout(pageTimeoutMS)
	if kept.Seedable() {
		SeedProfile(context, kept, func(string) {})
	}
	return context, nil
}

func (e *Engine) OpenFirefoxPage(kept StorageState) (playwright.BrowserContext, Page, error) {
	context, err := e.NewFirefoxContext(kept)
	if err != nil {
		return nil, nil, err
	}
	page, err := OpenPage(context)
	if err != nil {
		_ = context.Close()
		return nil, nil, err
	}
	return context, &livePage{page: page, pace: TypingPace, firefox: true}, nil
}

func (e *Engine) OpenFirefoxFetchSurface(origin, document string) (FetchSurface, error) {
	context, err := e.NewFirefoxContext(StorageState{})
	if err != nil {
		return FetchSurface{}, err
	}
	return fetchSurfaceIn(context, origin, document)
}
