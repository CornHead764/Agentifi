// Package browser is the Chromium internal/connector drives, in this
// process, with nothing in it that knows what a website looks like.
//
// playwright-go drives browsers through Playwright's Node driver, which the
// image carries. This process never fetches a browser: Google Chrome is
// installed beside it (FindChrome). A persistent context is its own browser:
// closing it closes that Chromium.
package browser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
	"github.com/playwright-community/playwright-go"
)

// LaunchArgs is shared by the browser and every persistent profile: a profile
// that differs is a second device to a provider. --no-sandbox lets Chromium
// start as a non-root user in a container without CAP_SYS_ADMIN.
var LaunchArgs = []string{"--disable-blink-features=AutomationControlled", "--no-sandbox"}

// UserAgentFor is the user agent the installed Chrome of that version sends,
// in Chrome's reduced form. It follows the running browser rather than a pin,
// so the string never contradicts the features and client hints a site
// compares it with; the OS matches the container for the same reason.
func UserAgentFor(version string) string {
	major, _, _ := strings.Cut(version, ".")
	return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/" + major + ".0.0.0 Safari/537.36"
}

func (e *Engine) UserAgent() (string, error) {
	b, err := e.Browser()
	if err != nil {
		return "", err
	}
	return UserAgentFor(b.Version()), nil
}

const (
	defaultLocale   = "en-US"
	defaultTimezone = "UTC"
	// A provider's portal is slow and a sign-in page waits on a person.
	pageTimeoutMS = 45_000
)

var DefaultViewport = Size{Width: 1280, Height: 900}

type Size struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Engine owns the Playwright driver and the shared browser, one per process.
// The driver starts on first use, so an install with no connectors never pays
// for it.
type Engine struct {
	Headless bool
	// DriverDir is where the Node driver was vendored at build time. Empty
	// falls back to the user cache directory, where a run-time download would
	// land.
	DriverDir string
	// ChromePath is the Google Chrome executable; see FindChrome.
	ChromePath string
	// FirefoxEndpoint is the Camoufox server's websocket; empty is none.
	FirefoxEndpoint string
	ProfilesRoot    string

	// Now is nil for the real clock. Only the profile lock's stamps read it.
	Now func() time.Time
	// Heartbeat is zero for LockHeartbeat.
	Heartbeat time.Duration

	mu      sync.Mutex
	pw      *playwright.Playwright
	browser playwright.Browser
	firefox playwright.Browser

	// claims maps a held profile directory to its lock token. A mutex of its
	// own, so restamping never waits behind a Chromium launch or vice versa.
	claimsMu sync.Mutex
	claims   map[string]string
	lost     map[string]func()
	// beating is closed to stop the heartbeat; nil when there is none.
	beating chan struct{}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func NewEngine(settings config.Browser) *Engine {
	return &Engine{
		Headless:        !settings.Headful,
		DriverDir:       settings.DriverDir,
		ChromePath:      settings.ChromePath,
		FirefoxEndpoint: settings.CamoufoxURL,
		ProfilesRoot:    settings.ProfilesDir,
	}
}

// start brings the driver up once. Callers hold e.mu.
func (e *Engine) start() (*playwright.Playwright, error) {
	if e.pw != nil {
		return e.pw, nil
	}
	options := &playwright.RunOptions{
		// Chrome is installed outside this process; a host may have no route
		// out.
		SkipInstallBrowsers: true,
		DriverDirectory:     e.DriverDir,
		Verbose:             false,
	}
	pw, err := playwright.Run(options)
	if err != nil {
		return nil, fmt.Errorf("browser: the Playwright driver would not start: %w", err)
	}
	e.pw = pw
	return pw, nil
}

// ErrNoChrome is a Chrome missing from where the engine launches it.
var ErrNoChrome = errors.New("browser: Google Chrome is not installed")

// Chrome is the Google Chrome the engine launches.
type Chrome struct {
	// Path is the configured executable, Resolved the file it names, which
	// is the one launched, so a browser runs from one version throughout.
	Path     string
	Resolved string
	// Origin is the record scripts/install-chrome.sh leaves beside the
	// executable: the package, the URL and the checksum it was installed
	// from. Empty for a Chrome installed some other way.
	Origin string
}

// FindChrome is the Chrome at path. Google Chrome rather than Playwright's
// bundled Chromium: some sites refuse the bundled build's TLS negotiation at
// the network level, so a missing Chrome is an error and never a fallback.
// The image does not carry Chrome; the compose file's chrome service
// downloads it from Google into a volume.
func FindChrome(path string) (Chrome, error) {
	missing := func(detail string) (Chrome, error) {
		return Chrome{}, fmt.Errorf("%w at %s (%s). The compose file's chrome service downloads it "+
			"from Google when the stack starts: run `docker compose up -d`, and `docker compose logs chrome` "+
			"says why it has not. Outside the stack, set AGENTIFI_CHROME_PATH to a Chrome executable",
			ErrNoChrome, textutil.FirstNonBlank(path, "(no AGENTIFI_CHROME_PATH)"), detail)
	}
	if path == "" {
		return missing("no path is set")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return missing("nothing is there")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return missing(err.Error())
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return missing("it is not an executable file")
	}
	chrome := Chrome{Path: path, Resolved: resolved}
	if origin, err := os.ReadFile(filepath.Join(filepath.Dir(resolved), "ORIGIN")); err == nil {
		chrome.Origin = strings.TrimSpace(string(origin))
	}
	return chrome, nil
}

// Browser is the shared browser, launched on first use and relaunched if it
// has gone.
func (e *Engine) Browser() (playwright.Browser, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.browser != nil && e.browser.IsConnected() {
		return e.browser, nil
	}
	chrome, err := FindChrome(e.ChromePath)
	if err != nil {
		return nil, err
	}
	pw, err := e.start()
	if err != nil {
		return nil, err
	}
	// The channel keeps Playwright's launch flags for Google Chrome; the
	// executable path says which Chrome.
	launched, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Channel:        playwright.String("chrome"),
		ExecutablePath: playwright.String(chrome.Resolved),
		Headless:       playwright.Bool(e.Headless),
		Args:           LaunchArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("browser: Chrome would not launch: %w", err)
	}
	e.browser = launched
	return launched, nil
}

// NewContext is a throwaway context from a storage state, for a connection
// that keeps no profile.
func (e *Engine) NewContext(storageStatePath string, viewport Size) (playwright.BrowserContext, error) {
	b, err := e.Browser()
	if err != nil {
		return nil, err
	}
	device := DefaultDevice(viewport, UserAgentFor(b.Version()))
	options := playwright.BrowserNewContextOptions{
		UserAgent:  playwright.String(device.UserAgent),
		Viewport:   &playwright.Size{Width: device.Viewport.Width, Height: device.Viewport.Height},
		Locale:     playwright.String(device.Locale),
		TimezoneId: playwright.String(device.Timezone),
	}
	if storageStatePath != "" {
		options.StorageStatePath = playwright.String(storageStatePath)
	}
	context, err := b.NewContext(options)
	if err != nil {
		return nil, fmt.Errorf("browser: no context: %w", err)
	}
	settle(context)
	return context, nil
}

// OpenProfile launches a Chromium on a connection's profile directory with its
// recorded device (see RecordedDevice). The caller Claims the directory first.
func (e *Engine) OpenProfile(dir string, device Device) (playwright.BrowserContext, error) {
	chrome, err := FindChrome(e.ChromePath)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	pw, err := e.start()
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	context, err := pw.Chromium.LaunchPersistentContext(dir,
		playwright.BrowserTypeLaunchPersistentContextOptions{
			Channel:        playwright.String("chrome"),
			ExecutablePath: playwright.String(chrome.Resolved),
			Headless:       playwright.Bool(e.Headless),
			UserAgent:      playwright.String(device.UserAgent),
			Viewport:       &playwright.Size{Width: device.Viewport.Width, Height: device.Viewport.Height},
			Locale:         playwright.String(device.Locale),
			TimezoneId:     playwright.String(device.Timezone),
			Args:           LaunchArgs,
		})
	if err != nil {
		return nil, fmt.Errorf("browser: the profile at %s would not open: %w", dir, err)
	}
	settle(context)
	return context, nil
}

func OpenPage(context playwright.BrowserContext) (playwright.Page, error) {
	if pages := context.Pages(); len(pages) > 0 {
		return pages[0], nil
	}
	return context.NewPage()
}

// typedFields is everything on a page a person types into. Buttons styled as
// inputs stay visible: a screenshot of a failed sign-in is about them.
const typedFields = `input:not([type=submit]):not([type=button]):not([type=reset]):not([type=image])` +
	`:not([type=checkbox]):not([type=radio]):not([type=hidden]), textarea, [contenteditable]:not([contenteditable=false])`

// screenshotTimeoutMS is short beside the page's own timeout: a screenshot is
// taken of a page that has already failed, and must not hold the error back.
const screenshotTimeoutMS = 10_000

// Screenshot is a PNG of the viewport with every typed field, in every frame,
// covered. Playwright draws the cover itself, so it holds in Camoufox, whose
// page scripts cannot reach the page's own world.
func Screenshot(page playwright.Page) ([]byte, error) {
	return page.Screenshot(playwright.PageScreenshotOptions{
		Type:     playwright.ScreenshotTypePng,
		FullPage: playwright.Bool(false),
		Mask:     typedFieldMask(page),
		Scale:    playwright.ScreenshotScaleCss,
		Timeout:  playwright.Float(screenshotTimeoutMS),
	})
}

// typedFieldMask is every typed field, in every frame, for a screenshot's Mask.
func typedFieldMask(page playwright.Page) []playwright.Locator {
	var mask []playwright.Locator
	for _, frame := range page.Frames() {
		mask = append(mask, frame.Locator(typedFields))
	}
	return mask
}

// Close gives back the driver and the shared browser. A persistent context
// belongs to whoever opened it and should be closed first, so its profile is
// written out rather than killed. Stopping the driver ends every browser it
// started, so every claim's lock is released too.
func (e *Engine) Close() error {
	e.stopBeat()
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.releaseAll()
	var failures []error
	if e.browser != nil {
		if err := e.browser.Close(); err != nil {
			failures = append(failures, err)
		}
		e.browser = nil
	}
	if e.firefox != nil {
		if err := e.firefox.Close(); err != nil {
			failures = append(failures, err)
		}
		e.firefox = nil
	}
	if e.pw != nil {
		if err := e.pw.Stop(); err != nil {
			failures = append(failures, err)
		}
		e.pw = nil
	}
	return errors.Join(failures...)
}

func settle(context playwright.BrowserContext) {
	context.SetDefaultTimeout(pageTimeoutMS)
	context.SetDefaultNavigationTimeout(pageTimeoutMS)
	applyDesktopConsistency(context)
}
