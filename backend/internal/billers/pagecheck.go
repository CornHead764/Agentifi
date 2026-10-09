package billers

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The checks a page runs before it will take a form: a challenge widget
// (Turnstile, reCAPTCHA, hCaptcha) inside the form, and the JavaScript
// challenge an Azure Front Door WAF serves in front of the whole site. Nothing
// here answers or clicks one: the page is read and given time, and a check
// that has not cleared within the wait stops the sign-in for a person.

// pageCheckJS reads which check the page shows: "waf" for the edge's
// challenge page, "pending" for a widget without its token, "cleared" for a
// widget holding one, and "" for none. The token inputs are the only sign a
// widget has passed; an invisible reCAPTCHA is left out because it runs only
// once its form is sent.
const pageCheckJS = deepAllJS + `
const agentifiPageCheck = () => {
  if (/^\/\.azwaf\//.test(location.pathname) || document.querySelector('script[src*="/.azwaf/"]')) return 'waf';
  const tokens = agentifiDeepAll(document,
    '[name="cf-turnstile-response"], [name="g-recaptcha-response"], [name="h-captcha-response"]');
  if (tokens.some((el) => !!el.value)) return 'cleared';
  const widgets = agentifiDeepAll(document,
    '[name="cf-turnstile-response"], .cf-turnstile, [data-sitekey]:not([data-size="invisible"]), ' +
    'iframe[src*="challenges.cloudflare.com"]');
  return widgets.length ? 'pending' : '';
};
`

const (
	pageCheckScript        = `() => {` + pageCheckJS + `return agentifiPageCheck(); }`
	pageCheckShownScript   = `() => {` + pageCheckJS + `return agentifiPageCheck() !== ''; }`
	pageCheckClearedScript = `() => {` + pageCheckJS + `
  const at = agentifiPageCheck();
  return at !== 'waf' && at !== 'pending';
}`
)

// pageCheckAppears is how long a page that shows no check yet is given to
// mount one: a widget drawn after the page settled, or one the form draws
// once it is filled in.
const pageCheckAppears = 2 * time.Second

// PageCheckWait is how long a pending check is given to clear.
const PageCheckWait = 45 * time.Second

// turnstileAutoWait is how long a Turnstile widget is given to clear on its
// own before the checkbox is clicked. Turnstile auto-solves within a second
// or two when it will; a widget still pending after this is one that wants a
// click.
const turnstileAutoWait = 3 * time.Second

// turnstileClickWait is how long the clicked checkbox is given to verify
// and set its token. Turnstile's verification finishes within a couple of
// seconds; a widget still pending after this is one for a person.
const turnstileClickWait = 5 * time.Second

// ErrPageCheckPending is a press held back because the page's check had not
// cleared: the sign-in can go on once a person ticks it.
var ErrPageCheckPending = errors.New("the page check had not cleared")

// pageCheckHeld is a press held back by a pending check, said in the module's
// own words and recognisable as ErrPageCheckPending.
type pageCheckHeld string

func (e pageCheckHeld) Error() string { return string(e) }
func (e pageCheckHeld) Unwrap() error { return ErrPageCheckPending }

// PageCheckPending says whether the page is showing a check it has not yet
// passed.
func PageCheckPending(page browser.Page) bool {
	at, err := page.Evaluate(pageCheckScript, nil)
	state, _ := at.(string)
	return err == nil && (state == "waf" || state == "pending")
}

// AwaitPageCheck lets a check clear before the page is read or its form sent,
// since a form sent before its token is in is refused, and says whether one is
// still pending once the wait is over. Only in Camoufox, where the providers
// that show such a check run; in Chrome there is no wait.
//
// A Turnstile widget that does not auto-solve within a short window is clicked
// once: Camoufox's humanize option moves the cursor to the checkbox naturally,
// and the remaining wait covers the verification that follows. A widget that
// still has not cleared after the click is left for a person.
func AwaitPageCheck(page browser.Page) bool {
	if !browser.InFirefox(page) {
		return false
	}
	if page.WaitForFunction(pageCheckShownScript, pageCheckAppears) != nil {
		return false
	}
	if page.WaitForFunction(pageCheckClearedScript, turnstileAutoWait) != nil {
		browser.ClickTurnstile(page)
		_ = page.WaitForFunction(pageCheckClearedScript, turnstileClickWait)
	}
	return PageCheckPending(page)
}

// coverScript reads what is on top at the centre of the marked control,
// descending into open shadow roots, or null when the control itself is.
// The element's tag, id and classes and an iframe's host are read, never its
// text.
const coverScript = `() => {` + pageCheckJS + `
  const target = agentifiDeepAll(document, '[data-agentifi-submit]')[0];
  if (!target) return null;
  const box = target.getBoundingClientRect();
  const x = box.left + box.width / 2, y = box.top + box.height / 2;
  let hit = document.elementFromPoint(x, y);
  while (hit && hit.shadowRoot) {
    const inner = hit.shadowRoot.elementFromPoint(x, y);
    if (!inner || inner === hit) break;
    hit = inner;
  }
  if (!hit || hit === target || target.contains(hit)) return null;
  const check = '.cf-turnstile, [data-sitekey], iframe[src*="challenges.cloudflare.com"]';
  return {
    tag: hit.tagName.toLowerCase(),
    id: hit.id || '',
    classes: String(hit.getAttribute('class') || ''),
    src: hit.tagName === 'IFRAME' ? String(hit.src || '') : '',
    check: !!hit.closest(check) || agentifiPageCheck() === 'waf',
  };
}`

// Cover is the element on top of a control a press could not land on, as the
// page reported it.
type Cover struct {
	Tag     string `json:"tag"`
	ID      string `json:"id"`
	Classes string `json:"classes"`
	// Src is an iframe's address; only its host is ever repeated.
	Src string `json:"src"`
	// Check is the cover being part of a page check.
	Check bool `json:"check"`
}

// readCover is what covers the marked control, or nil when nothing does.
func readCover(page browser.Page) (*Cover, error) {
	var cover *Cover
	if err := browser.EvaluateInto(page, coverScript, nil, &cover); err != nil {
		return nil, err
	}
	return cover, nil
}

// IsPageCheck says whether the cover is a page check: one the page marks as
// such, or a frame served from the challenge widget's host.
func (c Cover) IsPageCheck() bool {
	return c.Check || c.frameHost() == "challenges.cloudflare.com"
}

// Describe names the cover briefly by its markup, never its text: a tag with
// its id or first class, or a frame with its host.
func (c Cover) Describe() string {
	tag := strings.ToLower(c.Tag)
	if tag == "" {
		tag = "element"
	}
	if host := c.frameHost(); tag == "iframe" && host != "" {
		return "a frame from " + host
	}
	switch {
	case c.ID != "":
		return "a <" + tag + "> with id " + textutil.ClipMarked(c.ID, 40)
	case strings.TrimSpace(c.Classes) != "":
		return "a <" + tag + "> of class " + textutil.ClipMarked(strings.Fields(c.Classes)[0], 40)
	}
	return "a <" + tag + ">"
}

func (c Cover) frameHost() string {
	if c.Src == "" {
		return ""
	}
	address, err := url.Parse(c.Src)
	if err != nil {
		return ""
	}
	return strings.ToLower(address.Hostname())
}
