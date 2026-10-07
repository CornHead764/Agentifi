package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"

	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Page is the handful of things a provider module asks of a page, narrow
// enough that a test can hand a module a fake page instead of launching
// Chromium.
type Page interface {
	URL() string
	Title() (string, error)
	// Goto waits for the document, not for the network: a portal that polls
	// never goes idle.
	Goto(url string) error
	// Evaluate's argument is shaped as JSON on the way in, so any value that
	// marshals may be handed to a page; see serializable.go.
	Evaluate(script string, arg any) (any, error)
	WaitForFunction(script string, timeout time.Duration) error
	// AddInitScript runs a script before every document of this page's
	// context, so a hook is in place before the app's own code.
	AddInitScript(script string) error
	// Fill types into the first visible match: a sign-in page often carries a
	// hidden field that matches a generic union first, such as a
	// `display:none input[type=email]` placed above the username box.
	Fill(selector, value string) error
	// FillVisible fills only a field that is actually on the page, and says
	// whether there was one.
	FillVisible(selector, value string) (bool, error)
	// TypeInto types into a field rather than setting its value at once, for
	// a form that reacts to key and mouse events rather than to a value set
	// directly: a mouse click into the first visible match, the old text
	// selected and deleted, then the value key by key at TypingPace. A field
	// that already holds the value is left alone.
	TypeInto(selector, value string) error
	// TypeIntoVisible types only into a field that is actually on the page,
	// and says whether there was one.
	TypeIntoVisible(selector, value string) (bool, error)
	Click(selector string) error
	ClickVisible(selector string) (bool, error)
	// ForceClickVisible is ClickVisible past Playwright's actionability
	// checks, for a control the caller has read as pressable with nothing on
	// top of it: at a covered control a forced click presses the cover.
	ForceClickVisible(selector string) (bool, error)
	// ClickText takes a pattern because a control's text is often split over
	// child elements.
	ClickText(selectors string, pattern *regexp.Regexp) (bool, error)
	// Choose presses `target` (what a person would press, e.g. a custom
	// radio's label) and says how it landed, or "" when nothing matches. The
	// order runs from most faithful to least: a click, which alone goes through
	// the actionability checks; Playwright's Check on `radio`, which verifies
	// the radio ended up selected; and a forced Check, last because those
	// checks catch a genuinely broken page. Never a forced click: at a covered
	// control it presses the cover and reports success.
	Choose(target, radio string) (string, error)
	CheckIfUnchecked(selector string) (bool, error)
	Count(selector string) (int, error)
	Press(key string) error
	Type(text string) error
	// Settle waits for the document, then the network with a ceiling, because
	// a page may never go idle.
	Settle()
	// Sleep is on the interface so a test is not slowed by it.
	Sleep(d time.Duration)
	Screenshot() ([]byte, error)
	// ScreenshotOf is one element's picture (a CAPTCHA is legible cut out of
	// the page), or empty bytes for a selector that matches nothing.
	ScreenshotOf(selector string) ([]byte, error)
	// SelectMatching picks the first matching option of the first visible
	// `select` and says which, or "" when none offers one.
	SelectMatching(selector string, pattern *regexp.Regexp) (string, error)
	// OnResponse's handler runs on the driver's own goroutine, so it records
	// what it wants and reads the body later, from the caller's.
	OnResponse(handler func(r Response))
	// OnDownload is OnResponse for a navigation the browser answered as a
	// file: it is no response, and no OnResponse handler sees it.
	OnDownload(handler func(d Download))
	// OnPopup is a window this page opened, such as a file shown in a new
	// tab. Its handler runs on the driver's goroutine too.
	OnPopup(handler func(popup Page))
	Close() error
	// CookieNames is the names of the cookies this page's context sends to
	// the addresses, never their values.
	CookieNames(urls ...string) ([]string, error)
	// Bytes goes to the address in a page of its own in this page's context,
	// so the session's cookies go with it.
	Navigator
}

// PDFPrinter is a page that can print what it shows. Only headless Chromium
// prints; Camoufox and a headful Chrome answer an error.
type PDFPrinter interface {
	PDF() ([]byte, error)
}

type Response interface {
	URL() string
	Status() int
	// Header is "" for a header the response does not carry. A redirect's
	// Location is readable here though a page's own fetch is never shown it.
	Header(name string) string
	Body() ([]byte, error)
}

type Download interface {
	URL() string
	// Bytes waits for the file to finish.
	Bytes() ([]byte, error)
}

// EvaluateInto runs a script in the page and decodes its answer into out.
func EvaluateInto(page Page, script string, arg any, out any) error {
	answered, err := page.Evaluate(script, arg)
	if err != nil {
		return err
	}
	return decodeJSON(answered, out)
}

// EvaluateIntoContext answers when ctx ends; see EvaluateContext.
func EvaluateIntoContext(ctx context.Context, page Page, script string, arg any, out any) error {
	answered, err := EvaluateContext(ctx, page, script, arg)
	if err != nil {
		return err
	}
	return decodeJSON(answered, out)
}

func decodeJSON(answered any, out any) error {
	encoded, err := json.Marshal(answered)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		return fmt.Errorf("browser: the page answered something unreadable: %w", err)
	}
	return nil
}

const settleNetworkIdle = 8 * time.Second

// clickWait bounds a press well below the context's navigation timeout: a
// control something is sitting on top of does not become pressable by waiting.
const clickWait = 5 * time.Second

// Pace is how TypeInto types: each key waits Gap after the one before it and
// is held down for Hold, both drawn evenly between their bounds.
type Pace struct {
	Hold, Gap, Wait [2]time.Duration
	// Draw answers in [0, n). Nil is math/rand/v2.
	Draw func(n int64) int64
}

// TypingPace types a twenty-character password in about three seconds.
var TypingPace = Pace{
	Hold: [2]time.Duration{25 * time.Millisecond, 75 * time.Millisecond},
	Gap:  [2]time.Duration{50 * time.Millisecond, 150 * time.Millisecond},
	Wait: [2]time.Duration{400 * time.Millisecond, 1200 * time.Millisecond},
}

type Keystroke struct {
	Text      string
	Gap, Hold time.Duration
}

// Keystrokes is value one character at a time, each with its own timing.
func (p Pace) Keystrokes(value string) []Keystroke {
	out := make([]Keystroke, 0, len(value))
	for _, char := range value {
		out = append(out, Keystroke{Text: string(char), Gap: p.between(p.Gap), Hold: p.between(p.Hold)})
	}
	return out
}

// Pause is how long to wait between the last key and the press that sends
// it, drawn from Wait.
func (p Pace) Pause() time.Duration { return p.between(p.Wait) }

// Aim is where in a field of the given size to click: a point drawn from its
// middle half.
func (p Pace) Aim(width, height float64) (x, y float64) {
	spot := func(size float64) float64 {
		quarter := size / 4
		return quarter + float64(p.draw(1001))/1000*2*quarter
	}
	return spot(width), spot(height)
}

func (p Pace) between(bounds [2]time.Duration) time.Duration {
	spread := int64(bounds[1] - bounds[0])
	if spread <= 0 {
		return bounds[0]
	}
	return bounds[0] + time.Duration(p.draw(spread+1))
}

func (p Pace) draw(n int64) int64 {
	if p.Draw != nil {
		return p.Draw(n)
	}
	return rand.Int64N(n)
}

// How Choose landed. TookForce is the one worth reporting: it got past the
// checks that catch a broken page.
const (
	TookClick = "click"
	TookTick  = "tick"
	TookForce = "force"
)

type livePage struct {
	page    playwright.Page
	pace    Pace
	firefox bool
}

func Wrap(page playwright.Page) Page { return &livePage{page: page, pace: TypingPace} }

// Unwrap is for the engine's own work (screencasts, CDP, downloads).
func Unwrap(p Page) (playwright.Page, bool) {
	live, ok := p.(*livePage)
	if !ok {
		return nil, false
	}
	return live.page, true
}

func (p *livePage) URL() string { return p.page.URL() }

func (p *livePage) Title() (string, error) { return p.page.Title() }

func (p *livePage) Goto(url string) error {
	_, err := p.page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	return err
}

func (p *livePage) Evaluate(script string, arg any) (any, error) {
	shaped, err := jsonArg(arg)
	if err != nil {
		return nil, err
	}
	if shaped == nil {
		return p.page.Evaluate(script)
	}
	return p.page.Evaluate(script, shaped)
}

func (p *livePage) WaitForFunction(script string, timeout time.Duration) error {
	// The nil is the expression's argument, which playwright-go takes before
	// the options; without it the options are handed to the page as the
	// argument and the timeout is ignored.
	_, err := p.page.WaitForFunction(script, nil, playwright.PageWaitForFunctionOptions{
		Timeout: playwright.Float(float64(timeout.Milliseconds())),
	})
	return err
}

func (p *livePage) AddInitScript(script string) error {
	return p.page.Context().AddInitScript(playwright.Script{Content: playwright.String(script)})
}

// visible chains Playwright's `visible=true` filter rather than spelling
// `:visible` into every alternative of a provider's selector union.
func (p *livePage) visible(selector string) playwright.Locator {
	return p.page.Locator(selector).Locator("visible=true")
}

func (p *livePage) Fill(selector, value string) error {
	return p.visible(selector).First().Fill(value)
}

func (p *livePage) FillVisible(selector, value string) (bool, error) {
	field := p.visible(selector).First()
	count, err := field.Count()
	if err != nil || count == 0 {
		return false, err
	}
	return true, field.Fill(value)
}

func (p *livePage) TypeInto(selector, value string) error {
	return p.typeInto(p.visible(selector).First(), value)
}

func (p *livePage) TypeIntoVisible(selector, value string) (bool, error) {
	field := p.visible(selector).First()
	count, err := field.Count()
	if err != nil || count == 0 {
		return false, err
	}
	return true, p.typeInto(field, value)
}

func (p *livePage) typeInto(field playwright.Locator, value string) error {
	current, err := field.InputValue(playwright.LocatorInputValueOptions{
		Timeout: playwright.Float(float64(clickWait.Milliseconds())),
	})
	if err != nil {
		return err
	}
	if current == value {
		return nil
	}
	click := clickOptions(false)
	if box, err := field.BoundingBox(); err == nil && box != nil {
		x, y := p.pace.Aim(box.Width, box.Height)
		click.Position = &playwright.Position{X: x, Y: y}
	}
	if err := field.Click(click); err != nil {
		return err
	}
	keyboard := p.page.Keyboard()
	if current != "" {
		if err := keyboard.Press("ControlOrMeta+A"); err != nil {
			return err
		}
		if err := keyboard.Press("Backspace"); err != nil {
			return err
		}
	}
	for _, key := range p.pace.Keystrokes(value) {
		time.Sleep(key.Gap)
		if err := keyboard.Type(key.Text, playwright.KeyboardTypeOptions{
			Delay: playwright.Float(float64(key.Hold.Milliseconds())),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p *livePage) Click(selector string) error {
	return p.visible(selector).First().Click(clickOptions(false))
}

func (p *livePage) ClickVisible(selector string) (bool, error) {
	return p.clickVisible(selector, false)
}

func (p *livePage) ForceClickVisible(selector string) (bool, error) {
	return p.clickVisible(selector, true)
}

func (p *livePage) clickVisible(selector string, force bool) (bool, error) {
	control := p.visible(selector).First()
	count, err := control.Count()
	if err != nil || count == 0 {
		return false, err
	}
	if err := control.Click(clickOptions(force)); err != nil {
		return false, err
	}
	_ = p.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateDomcontentloaded,
	})
	return true, nil
}

func (p *livePage) ClickText(selectors string, pattern *regexp.Regexp) (bool, error) {
	control := p.visible(selectors).Filter(playwright.LocatorFilterOptions{
		HasText: pattern,
	}).First()
	count, err := control.Count()
	if err != nil || count == 0 {
		return false, err
	}
	if err := control.Click(clickOptions(false)); err != nil {
		return false, err
	}
	// Not synchronised with a navigation the click may start: the caller's own
	// grace period covers the gap.
	_ = p.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateDomcontentloaded,
	})
	return true, nil
}

func clickOptions(force bool) playwright.LocatorClickOptions {
	return playwright.LocatorClickOptions{
		Force:   playwright.Bool(force),
		Timeout: playwright.Float(float64(clickWait.Milliseconds())),
	}
}

func checkOptions(force bool) playwright.LocatorCheckOptions {
	return playwright.LocatorCheckOptions{
		Force:   playwright.Bool(force),
		Timeout: playwright.Float(float64(clickWait.Milliseconds())),
	}
}

func (p *livePage) Choose(target, radio string) (string, error) {
	control := p.visible(target).First()
	count, err := control.Count()
	if err != nil || count == 0 {
		return "", err
	}
	refused := control.Click(clickOptions(false))
	if refused == nil {
		return TookClick, nil
	}
	// Unfiltered: a custom radio is often styled down to nothing, and
	// Playwright can still tick it.
	box := p.page.Locator(radio).First()
	count, err = box.Count()
	if err != nil || count == 0 {
		return "", refused
	}
	if err := box.Check(checkOptions(false)); err == nil {
		return TookTick, nil
	}
	if err := box.Check(checkOptions(true)); err != nil {
		return "", err
	}
	return TookForce, nil
}

func (p *livePage) CheckIfUnchecked(selector string) (bool, error) {
	box := p.visible(selector).First()
	count, err := box.Count()
	if err != nil || count == 0 {
		return false, err
	}
	// A box whose state cannot be read is left alone: ticking a checkbox that
	// was already ticked unticks it.
	checked, err := box.IsChecked()
	if err != nil || checked {
		return false, err
	}
	return true, box.Check(checkOptions(false))
}

func (p *livePage) Count(selector string) (int, error) {
	return p.page.Locator(selector).Count()
}

func (p *livePage) Press(key string) error { return p.page.Keyboard().Press(key) }

func (p *livePage) Type(text string) error { return p.page.Keyboard().Type(text) }

func (p *livePage) Settle() {
	_ = p.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateDomcontentloaded,
	})
	_ = p.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State:   playwright.LoadStateNetworkidle,
		Timeout: playwright.Float(float64(settleNetworkIdle.Milliseconds())),
	})
}

func (p *livePage) Sleep(d time.Duration) { time.Sleep(d) }

func (p *livePage) Screenshot() ([]byte, error) { return Screenshot(p.page) }

func (p *livePage) PDF() ([]byte, error) {
	printed, err := p.page.PDF(letterPDF())
	if err != nil {
		return nil, fmt.Errorf("browser: the page would not print: %w", err)
	}
	if len(printed) == 0 {
		return nil, errors.New("browser: the page printed to nothing")
	}
	return printed, nil
}

func (p *livePage) ScreenshotOf(selector string) ([]byte, error) {
	element := p.visible(selector).First()
	count, err := element.Count()
	if err != nil || count == 0 {
		return nil, err
	}
	return element.Screenshot(playwright.LocatorScreenshotOptions{Type: playwright.ScreenshotTypePng})
}

func (p *livePage) SelectMatching(selector string, pattern *regexp.Regexp) (string, error) {
	boxes, err := p.visible(selector).All()
	if err != nil {
		return "", err
	}
	for _, box := range boxes {
		labels, err := box.Locator("option").AllInnerTexts()
		if err != nil {
			continue
		}
		for _, label := range labels {
			if !pattern.MatchString(label) {
				continue
			}
			if _, err := box.SelectOption(playwright.SelectOptionValues{
				Labels: &[]string{label},
			}); err != nil {
				return "", err
			}
			return strings.TrimSpace(label), nil
		}
	}
	return "", nil
}

func (p *livePage) OnResponse(handler func(r Response)) {
	p.page.On("response", func(response playwright.Response) {
		handler(liveResponse{response: response})
	})
}

type liveResponse struct{ response playwright.Response }

func (r liveResponse) URL() string           { return r.response.URL() }
func (r liveResponse) Status() int           { return r.response.Status() }
func (r liveResponse) Body() ([]byte, error) { return r.response.Body() }

func (r liveResponse) Header(name string) string {
	value, _ := r.response.HeaderValue(name)
	return value
}

func (p *livePage) OnDownload(handler func(d Download)) {
	p.page.On("download", func(download playwright.Download) {
		handler(liveDownload{download: download})
	})
}

func (p *livePage) OnPopup(handler func(popup Page)) {
	p.page.On("popup", func(popup playwright.Page) {
		handler(&livePage{page: popup, pace: p.pace, firefox: p.firefox})
	})
}

func (p *livePage) Close() error { return p.page.Close() }

func (p *livePage) CookieNames(urls ...string) ([]string, error) {
	cookies, err := p.page.Context().Cookies(urls...)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(cookies))
	for index, cookie := range cookies {
		names[index] = cookie.Name
	}
	return names, nil
}

type liveDownload struct{ download playwright.Download }

func (d liveDownload) URL() string            { return d.download.URL() }
func (d liveDownload) Bytes() ([]byte, error) { return downloadBytes(d.download) }

func (p *livePage) Bytes(address string) (int, string, []byte, error) {
	return (&contextNavigator{context: p.page.Context()}).Bytes(address)
}

// Glimpse is what a page showed, for a reader that found nothing to report.
func Glimpse(page Page, chars int) string {
	title, _ := page.Title()
	var body string
	if text, err := page.Evaluate(`() => {`+CleanJS+`
  return document.body ? clean(document.body.innerText) : "";
}`, nil); err == nil {
		body, _ = text.(string)
	}
	return fmt.Sprintf("page %q at %s; seen: %q",
		textutil.Clip(title, 80), textutil.Clip(page.URL(), 140), textutil.Clip(body, chars))
}

// IsTimeout says an error is the driver giving up on a wait, such as a click
// whose target never became clickable.
func IsTimeout(err error) bool { return errors.Is(err, playwright.ErrTimeout) }
