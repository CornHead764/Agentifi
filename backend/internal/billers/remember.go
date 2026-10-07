package billers

import (
	"regexp"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// "Remember this device", by the words beside the box, for a box named nothing
// in particular that rememberSelector cannot find. Ticking it is the
// difference between a code at this sign-in and a code at every pull.

// rememberWords is a box that keeps this browser from being asked again. Not
// here, deliberately: "remember my email", terms, offers, and a bank's "use
// token"; ticking the wrong box is worse than ticking none.
var rememberWords = regexp.MustCompile(
	`(?i)\bremember (?:me|this (?:device|computer|browser)|my (?:device|computer|browser))\b|` +
		`\btrust(?:ed)? (?:this )?(?:device|computer|browser)\b|` +
		`\bthis (?:is a )?trusted (?:device|computer|browser)\b|` +
		`\bdo(?:n[’']?t| not) (?:ask|require)\b|` +
		`\bskip\b.{0,40}\b(?:verification|this step)\b|` +
		`\bkeep me (?:signed|logged) in\b`)

// rememberBoxesScript is every tick box in the step's scope with the words a
// person reads beside it and whether it is ticked. It reads and never ticks.
// A box styled away behind its label is still offered, for the label to be
// pressed.
const rememberBoxesScript = `() => {` + agent.DialogScopeJS + `
` + agent.VisibleJS + agent.CleanJS + agent.EscapedJS + agent.NameOfJS + `
  // The last resort for a checkbox nothing names: the words beside it, where
  // it is the only checkbox in the box holding them.
  const words = (el) => {
    const said = nameOf(el);
    if (said !== '') return said.slice(0, 200);
    if (el.parentElement &&
        el.parentElement.querySelectorAll('input[type="checkbox"], [role="checkbox"]').length === 1) {
      return clean(el.parentElement.innerText || el.parentElement.textContent).slice(0, 200);
    }
    return '';
  };
  const out = [];
  [...agentifiScope.querySelectorAll('input[type="checkbox"], [role="checkbox"]')].forEach((el, at) => {
    const native = el.tagName === 'INPUT';
    if (!visible(el) && !(native && visible(labelOf(el)))) return;
    out.push({
      at,
      words: words(el),
      checked: native ? el.checked === true : el.getAttribute('aria-checked') === 'true',
    });
  });
  return out;
}`

// markRememberScript puts the mark on the box the words picked — or on its
// label, for a box nobody can see — and says which press that wants: "check"
// for a box Playwright can tick and read back, "click" for anything else.
const markRememberScript = `(pick) => {` + agent.DialogScopeJS + `
` + agent.VisibleJS + agent.CleanJS + agent.EscapedJS + agent.NameOfJS + `
  for (const stale of document.querySelectorAll('[data-agentifi-remember]')) stale.removeAttribute('data-agentifi-remember');
  const el = [...agentifiScope.querySelectorAll('input[type="checkbox"], [role="checkbox"]')][pick.at];
  if (!el) return '';
  if (visible(el)) {
    el.setAttribute('data-agentifi-remember', '');
    return el.tagName === 'INPUT' ? 'check' : 'click';
  }
  const label = labelOf(el);
  if (!visible(label)) return '';
  label.setAttribute('data-agentifi-remember', '');
  return 'click';
}`

const rememberMark = `[data-agentifi-remember]`

type RememberBox struct {
	At      int    `json:"at"`
	Words   string `json:"words"`
	Checked bool   `json:"checked"`
}

// BestRemember is the box a step ticks: the first unticked one whose words say
// it keeps this device from being asked again.
func BestRemember(boxes []RememberBox) (RememberBox, bool) {
	for _, box := range boxes {
		if !box.Checked && rememberWords.MatchString(box.Words) {
			return box, true
		}
	}
	return RememberBox{}, false
}

// tickRememberedByWords ticks that box, where there is one. Nothing here is an
// error: a box that will not tick costs the next sign-in a code, not this one.
func tickRememberedByWords(page browser.Page) {
	var boxes []RememberBox
	if err := browser.EvaluateInto(page, rememberBoxesScript, nil, &boxes); err != nil {
		return
	}
	best, found := BestRemember(boxes)
	if !found {
		return
	}
	press, err := page.Evaluate(markRememberScript, map[string]int{"at": best.At})
	if err != nil {
		return
	}
	switch press {
	case "check":
		_, _ = page.CheckIfUnchecked(rememberMark)
	case "click":
		_, _ = page.ClickVisible(rememberMark)
	}
}
