package billers

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// The cookie banner in front of a sign-in form. A banner's filter can cover
// the submit button, so the fill declines it first, once, always the way that
// consents to nothing. Before typing rather than before pressing, because a
// consent manager may reload the page when consent changes, and a reload
// after the fill is a form sent empty.

// consentManager is one consent-management platform's banner, by the element
// ids the platform ships, which are the same at every site that bought it.
type consentManager struct {
	Name   string `json:"name"`
	Banner string `json:"banner"`
	// Reject is the controls that decline everything the banner asks for, in
	// the order they are tried; Close the ones that dismiss it without an
	// answer. There is deliberately no list of the controls that accept.
	Reject []string `json:"reject"`
	Close  []string `json:"close"`
	// Untick is the banner's consent switches and Save the controls that keep
	// them as set. A save is a reject only once every switch is off, so the
	// fill turns them off first and a save is offered only when none is on.
	Untick string   `json:"untick,omitempty"`
	Save   []string `json:"save,omitempty"`
}

// consentManagers is every banner the fill knows how to decline. OneTrust's
// preference centre is a second banner of its own: a site whose "Opt Out"
// opens it gets its refuse-all pressed on the second pass, or, in a centre
// with none, its switches turned off and its choices confirmed. Its close
// accepts everything at some sites, so it is the last resort.
var consentManagers = []consentManager{
	{
		Name: "OneTrust", Banner: "#onetrust-banner-sdk",
		Reject: []string{"#onetrust-reject-all-handler"},
		Close:  []string{"#onetrust-close-btn-container button", ".onetrust-close-btn-handler"},
	},
	{
		Name: "OneTrust", Banner: "#onetrust-pc-sdk",
		Reject: []string{".ot-pc-refuse-all-handler"},
		Close:  []string{"#close-pc-btn-handler"},
		Untick: ".category-switch-handler",
		Save:   []string{".save-preference-btn-handler"},
	},
	{
		Name: "TrustArc", Banner: "#truste-consent-track",
		Reject: []string{"#truste-consent-required"},
		Close:  []string{"#truste-consent-close"},
	},
	{
		Name: "Cookiebot", Banner: "#CybotCookiebotDialog",
		Reject: []string{"#CybotCookiebotDialogBodyButtonDecline"},
	},
}

// consentScript is every way out of a visible banner: the platform, whether
// the control rejects or closes, its selector and its words. It reads and
// never presses. Every other control in the banner is offered too, as kind
// "other" with its own mark, because a site may put its reject on a control
// of its own choosing ("Opt Out") rather than the platform's id.
const consentScript = `(managers) => {
` + agent.VisibleJS + agent.CleanJS + `
  const said = (el) => clean(el.innerText || el.textContent || el.value || el.getAttribute('aria-label') || '').slice(0, 80);
  for (const stale of document.querySelectorAll('[data-agentifi-consent]')) stale.removeAttribute('data-agentifi-consent');
  let marks = 0;
  const out = [];
  for (const manager of managers) {
    const banner = document.querySelector(manager.banner);
    if (!visible(banner)) continue;
    const seen = new Set();
    const on = manager.untick ? [...banner.querySelectorAll(manager.untick)].some((el) => el.checked && !el.disabled) : true;
    for (const [kind, selectors] of [['reject', manager.reject || []], ['save', on ? [] : manager.save || []], ['close', manager.close || []]]) {
      for (const selector of selectors) {
        const el = document.querySelector(selector);
        if (!visible(el) || seen.has(el)) continue;
        seen.add(el);
        out.push({ manager: manager.name, banner: manager.banner, kind, selector, words: said(el) });
      }
    }
    for (const el of banner.querySelectorAll('button, a, [role="button"], input[type="button"], input[type="submit"]')) {
      if (!visible(el) || seen.has(el)) continue;
      seen.add(el);
      el.setAttribute('data-agentifi-consent', String(marks));
      out.push({ manager: manager.name, banner: manager.banner, kind: 'other', selector: '[data-agentifi-consent="' + marks + '"]', words: said(el) });
      marks += 1;
    }
  }
  return out;
}`

// untickScript turns off every consent switch in a visible banner that has
// them. A switch is often a hidden input under
// a drawn toggle, so it is clicked through the DOM rather than by pointer.
const untickScript = `(managers) => {
` + agent.VisibleJS + `
  for (const manager of managers) {
    if (!manager.untick) continue;
    const banner = document.querySelector(manager.banner);
    if (!visible(banner)) continue;
    for (const el of banner.querySelectorAll(manager.untick)) {
      if (el.checked && !el.disabled) el.click();
    }
  }
}`

// consentGoneScript is the banner no longer standing in front of anything.
const consentGoneScript = `(banner) => {` + agent.VisibleJS + `
  const el = document.querySelector(banner);
  return !visible(el);
}`

const consentWait = 2 * time.Second

// ConsentControl is one way out of a cookie banner, as the page offers it.
type ConsentControl struct {
	Manager string `json:"manager"`
	// Banner is the banner the control stands in, by the platform's selector
	// for it: a second pass declines a different banner or none.
	Banner   string `json:"banner"`
	Kind     string `json:"kind"`
	Selector string `json:"selector"`
	Words    string `json:"words"`
}

// consentAccepts is a control that consents, whatever list it was found in: a
// site that relabels its reject button "Accept" has made it the other button.
var consentAccepts = regexp.MustCompile(`(?i)\b(accept|agree|allow|consent|got it|ok(ay)?)\b`)

// consentRejects is a control whose whole text declines. Anchored, so "Opt out
// of the newsletter" and a "Cookie settings" menu are not.
var consentRejects = regexp.MustCompile(
	`(?i)^\s*(?:(?:reject|decline|deny|refuse)(?: all)?(?: cookies)?|opt[- ]?out|` +
		`(?:strictly )?necessary(?: cookies)? only|only (?:strictly )?necessary(?: cookies)?|` +
		`do not sell(?: or share)?(?: my (?:personal )?(?:information|data))?)\s*[.!]?\s*$`)

// BestConsent is the control the fill presses, and whether the banner offers
// one it may: the platform's own reject, then its save with every switch off,
// then any other control in the banner whose words decline, then a close, the
// page's order within each, and never a control whose words say it accepts.
func BestConsent(controls []ConsentControl) (ConsentControl, bool) {
	for _, pass := range []func(ConsentControl) bool{
		func(c ConsentControl) bool { return c.Kind == "reject" },
		func(c ConsentControl) bool { return c.Kind == "save" },
		func(c ConsentControl) bool { return c.Kind == "other" && consentRejects.MatchString(c.Words) },
		func(c ConsentControl) bool { return c.Kind == "close" },
	} {
		for _, control := range controls {
			if pass(control) && !consentAccepts.MatchString(control.Words) {
				return control, true
			}
		}
	}
	return ConsentControl{}, false
}

// dismissConsent declines a visible cookie banner and says what it pressed, or
// "". One press per banner, no retry, and never an error: a banner that stays
// is the next round's. Two passes, for the banner whose "Opt Out" opens a
// preference centre.
func (d Draft) dismissConsent(page browser.Page) string {
	var said []string
	declined := map[string]bool{}
	for pass := 0; pass < consentPasses; pass++ {
		pressed, banner := d.declineConsent(page, declined)
		if pressed == "" {
			break
		}
		declined[banner] = true
		said = append(said, pressed)
	}
	return strings.Join(said, ", then ")
}

// consentPasses is how many banners one fill declines: the banner, and the
// preference centre its reject may open.
const consentPasses = 2

// declineConsent presses the control BestConsent picks in a banner not yet
// declined, and says what it pressed and in which banner.
func (d Draft) declineConsent(page browser.Page, declined map[string]bool) (string, string) {
	_, _ = page.Evaluate(untickScript, consentManagers)
	var controls []ConsentControl
	if err := browser.EvaluateInto(page, consentScript, consentManagers, &controls); err != nil {
		return "", ""
	}
	controls = slices.DeleteFunc(controls, func(c ConsentControl) bool { return declined[c.Banner] })
	best, found := BestConsent(controls)
	if !found {
		return "", ""
	}
	pressed, err := page.ClickVisible(best.Selector)
	if err != nil || !pressed {
		return "", ""
	}
	for _, manager := range consentManagers {
		if manager.Banner == best.Banner {
			_ = page.WaitForFunction("() => ("+consentGoneScript+")("+strconv.Quote(manager.Banner)+")", consentWait)
		}
	}
	page.Sleep(agent.Settle)
	if best.Words != "" {
		return best.Manager + " “" + best.Words + "”", best.Banner
	}
	return best.Manager + " " + best.Kind, best.Banner
}
