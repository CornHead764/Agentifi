package agent

import (
	"sync"

	"github.com/playwright-community/playwright-go"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Browser is an opened browser and what can be done with it.
type Browser struct {
	Page browser.Page
	// Dir is the profile directory this claimed, or "" for a throwaway context.
	Dir string
	// Viewport is the size this browser actually opened at.
	Viewport browser.Size
	// Live starts the view a person acts through: Chrome's screencast, or
	// screenshots of a Camoufox page. Nil in flow tests.
	Live         func() (*browser.LiveView, error)
	StorageState func() ([]byte, error)
	// PinCookies dates the session cookies so closing the browser does not end
	// the sign-in, noting each one it dated.
	PinCookies func(note func(string))
	// Close closes the browser and releases the profile claim.
	Close func()
}

// Open is what a browser is opened with.
type Open struct {
	// Profile is a connection's kept browser. A name that is not a profile
	// (the merchants pass "") opens a throwaway context instead.
	Profile string
	// State is the sealed session: it seeds a throwaway context, and a profile
	// that is missing.
	State []byte
	// Viewport is what a live sign-in asked for; a profile that has recorded
	// its own device keeps that one. Zero is browser.DefaultViewport.
	Viewport browser.Size
	// Note hears what the opening had to say. Nil drops it.
	Note func(string)
}

func (o Open) note(line string) {
	if o.Note != nil {
		o.Note(line)
	}
}

type Opener func(Open) (*Browser, error)

// For is the opener a connector runs in: Chrome, or Camoufox for a
// browser.FirefoxProvider, which with no Camoufox server is refused rather
// than opened in Chrome.
func For(connector any, chrome, firefox Opener) Opener {
	if !browser.RunsInFirefox(connector) {
		return chrome
	}
	if firefox == nil {
		return func(Open) (*Browser, error) { return nil, browser.ErrNoFirefox }
	}
	return firefox
}

// Chrome opens a connection's own profile, or a throwaway context from the
// sealed state. A profile on disk is the session of record; the sealed state
// only seeds one that is missing.
func Chrome(engine *browser.Engine) Opener {
	return func(open Open) (*Browser, error) {
		viewport := open.Viewport
		if viewport.Width == 0 || viewport.Height == 0 {
			viewport = browser.DefaultViewport
		}
		kept, err := browser.ReadStorageState(open.State)
		if err != nil {
			open.note(err.Error())
		}
		dir, dirErr := browser.ProfileDir(engine.ProfilesRoot, open.Profile)
		if dirErr != nil {
			context, err := engine.NewContext("", viewport)
			if err != nil {
				return nil, err
			}
			if kept.Seedable() {
				browser.SeedProfile(context, kept, open.note)
			}
			return over(context, nil, "", viewport, func() {})
		}

		plan := browser.PlanProfile(dir, kept)
		if plan.Note != "" {
			open.note(plan.Note)
		}
		userAgent, err := engine.UserAgent()
		if err != nil {
			return nil, err
		}
		device, err := browser.RecordedDevice(dir, browser.DefaultDevice(viewport, userAgent))
		if err != nil {
			return nil, err
		}
		if err := engine.Claim(dir); err != nil {
			return nil, &provider.AgentError{Kind: provider.ErrAgentConflict,
				Message: "that connection is already open in the agent; finish or close that sign-in first"}
		}
		context, err := engine.OpenProfile(dir, device)
		if err != nil {
			engine.Release(dir)
			return nil, err
		}
		engine.OnLost(dir, func() { _ = context.Close() })
		if plan.Seed {
			browser.SeedProfile(context, kept, open.note)
		}
		// The profile's recorded viewport, not the one that was asked for: the
		// clicks a live view sends are in these pixels.
		return over(context, nil, dir, device.Viewport, func() { engine.Release(dir) })
	}
}

// Firefox opens a fresh Camoufox context seeded with the sealed state. It has
// no profile directory, and its live view is screenshots, not a screencast
// (see internal/browser/firefox.go).
func Firefox(engine *browser.Engine) Opener {
	return func(open Open) (*Browser, error) {
		kept, err := browser.ReadStorageState(open.State)
		if err != nil {
			open.note(err.Error())
		}
		context, page, err := engine.OpenFirefoxPage(kept)
		if err != nil {
			return nil, err
		}
		opened, err := over(context, page, "", browser.DefaultViewport, func() {})
		if err != nil {
			return nil, err
		}
		opened.Live = func() (*browser.LiveView, error) {
			return browser.StartFirefoxLiveView(page)
		}
		return opened, nil
	}
}

// over is the Browser a context is, on page, or on a page it opens when page
// is nil. A context that cannot open a page is closed and released.
func over(
	context playwright.BrowserContext, page browser.Page, dir string, viewport browser.Size,
	release func(),
) (*Browser, error) {
	closeAll := func() {
		// A persistent context *is* the browser: the directory is free for the
		// next opener only once this has closed.
		_ = context.Close()
		release()
	}
	if page == nil {
		opened, err := browser.OpenPage(context)
		if err != nil {
			closeAll()
			return nil, &provider.AgentError{Kind: provider.ErrAgentFailed,
				Message: "the browser opened with no page: " + err.Error()}
		}
		page = browser.Wrap(opened)
	}
	return &Browser{
		Page:     page,
		Dir:      dir,
		Viewport: viewport,
		Live: func() (*browser.LiveView, error) {
			return browser.StartLiveView(page, viewport)
		},
		StorageState: func() ([]byte, error) {
			return browser.TakeStorageState(context)
		},
		PinCookies: func(note func(string)) {
			var lines []string
			if _, err := browser.PinSessionCookies(context, &lines); err != nil {
				return
			}
			for _, line := range lines {
				note(line)
			}
		},
		Close: closeAll,
	}, nil
}

// Hold is the browser a session drives. A cancel can arrive while a request
// is still reading it, so it is behind its own lock, and nil once closed.
type Hold struct {
	mu      sync.Mutex
	browser *Browser
}

func (h *Hold) Opened() *Browser {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.browser
}

func (h *Hold) Attach(opened *Browser) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.browser = opened
}

// Pictured is err with the held page it happened on; see Pictured.
func (h *Hold) Pictured(err error) error {
	opened := h.Opened()
	if opened == nil {
		return err
	}
	return Pictured(opened.Page, err)
}

// Take hands the browser over and forgets it, so closing twice closes once.
func (h *Hold) Take() *Browser {
	h.mu.Lock()
	defer h.mu.Unlock()
	opened := h.browser
	h.browser = nil
	return opened
}

// Release closes the browser once, after forget has dropped what the
// connector remembers about its page.
func (h *Hold) Release(forget func(browser.Page)) {
	opened := h.Take()
	if opened == nil {
		return
	}
	if opened.Page != nil && forget != nil {
		forget(opened.Page)
	}
	if opened.Close != nil {
		opened.Close()
	}
}
