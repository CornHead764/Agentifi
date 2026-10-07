package agent

import (
	"strconv"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// The part of a page a sign-in reads and acts on: the whole document, or the
// dialog standing in front of it. A provider may answer the password with a
// dialog (a code prompt, an enrolment step) while the sign-in form is still in
// the document behind it; reading the form would call the page `password` and
// press its button again, sending another code. A person looks only at the
// dialog, and so does every reading and press here.

// DialogScopeJS is a script prelude: `agentifiScope` is the dialog in front of
// the page, or the document when there is none. A dialog is `dialog[open]`,
// `[role="dialog"]`, `[role="alertdialog"]`, `[aria-modal="true"]` (taken on
// its word) or Bootstrap 4's `.modal.show`, which may carry no role.
//
// A dialog not declared modal must cover the page to count: most visible
// controls behind it must not be topmost at their own centre. A live-chat
// panel is a `role="dialog"` that leaves the sign-in form pressable. A cookie
// banner is never the dialog.
// Nested dialogs: the innermost open one, last in document order, is in front.
// VisibleJS declares visible(el) for a page script: a box with a size whose
// computed visibility is visible, the rule Playwright waits on before it types
// or clicks. A box hidden by visibility keeps its size, so a size alone would
// read a sign-in's password box that waits behind a Continue as one to fill.
const VisibleJS = `
const visible = (el) => !!(el && (el.offsetWidth || el.offsetHeight || el.getClientRects().length) &&
  getComputedStyle(el).visibility === 'visible');
`

const DialogScopeJS = `
const agentifiScope = (() => {
` + VisibleJS + `
  const consent = /cookie|consent|onetrust|truste|cybot|gdpr/i;
  const isConsent = (el) => {
    for (let at = el; at && at !== document.body; at = at.parentElement) {
      const said = [at.id || '', typeof at.className === 'string' ? at.className : '',
        at.getAttribute('aria-label') || ''].join(' ');
      if (consent.test(said)) return true;
    }
    return false;
  };
  const declared = (el) => {
    if (el.getAttribute('aria-modal') === 'true') return true;
    try { return el.tagName === 'DIALOG' && el.matches(':modal'); } catch (e) { return false; }
  };
  const covers = (dialog) => {
    const behind = [...document.querySelectorAll('input, button, a[href], select, textarea')]
      .filter((el) => visible(el) && !dialog.contains(el) && String(el.type || '').toLowerCase() !== 'hidden')
      .filter((el) => {
        const r = el.getBoundingClientRect();
        const x = r.left + r.width / 2, y = r.top + r.height / 2;
        return x >= 0 && y >= 0 && x < window.innerWidth && y < window.innerHeight;
      })
      .slice(0, 30);
    if (behind.length === 0) return true;
    const hidden = behind.filter((el) => {
      const r = el.getBoundingClientRect();
      const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
      return !top || !(top === el || el.contains(top) || (top.closest && top.closest('label') && top.closest('label').contains(el)));
    });
    return hidden.length * 2 >= behind.length;
  };
  const found = [...document.querySelectorAll(
    'dialog[open], [role="dialog"], [role="alertdialog"], [aria-modal="true"], .modal.show')]
    .filter((el) => visible(el) && el.getAttribute('aria-hidden') !== 'true' && !isConsent(el))
    .filter((el) => String(el.innerText || '').trim() !== '' || el.querySelector('input, button'))
    .filter((el) => declared(el) || covers(el));
  return found.length > 0 ? found[found.length - 1] : document;
})();
`

// signatureExpr is the page as the change wait compares it, as an
// expression so it drops into a waitForFunction. The address is not enough:
// some identity providers swap the whole form in place through XHR under one
// URL and title, so the signature is the DOM (heading, boxes, buttons, whether
// it is complaining) and whether a dialog is open over it.
const signatureExpr = `((() => {` + DialogScopeJS + `
` + VisibleJS + CleanJS + HeadingsJS + PageTextJS + `
  const root = agentifiScope;
  const inputs = [...root.querySelectorAll('input')].filter(visible);
  const text = pageTextOf(root);
  const heading = headingsOf(root, 'h1, h2, h3').slice(0, 2).join(' · ');
  return [location.href.replace(/[?#].*$/, ''), document.title, root === document ? '' : 'dialog', heading,
    inputs.map((i) => [(i.type || 'text'), i.name || i.id || ''].join(':')).join(','),
    [...root.querySelectorAll(` + SubmitSelectorJS + `)].filter(visible).length,
    ` + TroublePatternJS + `.test(text) ? 1 : 0,
  ].join('|');
})())`

// A sign-in widget posts by XHR and re-renders itself, so a flat wait reads it
// mid-render as an empty page. The wait is for the page to differ
// (ChangeWait), then a short settle for the paint (Settle).
const (
	ChangeWait = 6 * time.Second
	Settle     = 500 * time.Millisecond
)

// Signature is the page as AwaitChange compares it, or "" for a page that
// could not be read (mid-navigation), which falls back to the flat settle.
func Signature(page browser.Page) string {
	read, err := page.Evaluate("() => "+signatureExpr, nil)
	if err != nil {
		return ""
	}
	signature, _ := read.(string)
	return signature
}

// AwaitChange waits for the page to differ from before: a new address, a new
// title, a form with different boxes on it, or the site's complaint about what
// was just sent. A page that never differs costs ChangeWait once and is then
// read as it stands.
func AwaitChange(page browser.Page, before string) {
	if before != "" {
		_ = page.WaitForFunction(
			"() => "+signatureExpr+" !== "+strconv.Quote(before), ChangeWait)
	}
	page.Sleep(Settle)
}

// Submit presses the form's button, or Enter where the page shows none, waits
// for the page to answer, and says whether it differed.
func Submit(page browser.Page, button string) (Step, error) {
	before := Signature(page)
	pressed, err := page.ClickVisible(button)
	if err != nil {
		return Step{}, err
	}
	step := Step{Acted: true, Pressed: PressedButton}
	if !pressed {
		if err := page.Press("Enter"); err != nil {
			return Step{}, err
		}
		step.Pressed = PressedEnter
	}
	return Changed(page, before, step), nil
}

// Changed waits for the page to differ from before (AwaitChange) and says on
// the step whether it did. A page unreadable before the press counts as
// changed: "unreadable" is a different finding from "went nowhere", which the
// loop guard gives up on.
func Changed(page browser.Page, before string, step Step) Step {
	AwaitChange(page, before)
	step.Changed = before == "" || Signature(page) != before
	return step
}
