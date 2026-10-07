package billers

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// factorChoicesScript is the menu a factor page is offering, as a person reads
// it: every control that could be a way to verify, what it says, and whether
// choosing it selects or sends. It reads and does not decide, so the ranking
// is one pure function. Names are worked out the way a screen reader would
// (aria-label, aria-labelledby, a wrapping label, a `for` label, a label
// beside it), inside the dialog in front of the page when there is one.
const factorChoicesScript = `(selector) => {` + agent.DialogScopeJS + `
` + agent.VisibleJS + agent.CleanJS + agent.EscapedJS + agent.NameOfJS + `
  // The last resort for a radio nothing is bound to: the words printed beside
  // it, which is the nearest box around it holding no other choice.
  const nearby = (el) => {
    let at = el.parentElement;
    for (let up = 0; up < 4 && at; up += 1) {
      if (at.querySelectorAll('input[type="radio"], [role="radio"]').length <= 1) {
        const said = textOf(at);
        if (said !== '') return said;
      }
      at = at.parentElement;
    }
    return '';
  };

  const selects = (el) =>
    (el.tagName === 'INPUT' && String(el.type || '').toLowerCase() === 'radio') ||
    String(el.getAttribute('role') || '').toLowerCase() === 'radio';

  // A radio styled by its own label is invisible itself and pressed through
  // the label; a choice with neither on the screen is no choice at all.
  const reachable = (el) => {
    if (visible(el)) return true;
    if (!selects(el)) return false;
    return visible(el.closest ? el.closest('label') : null) || visible(boundTo(el));
  };

  // Whether a radio is already the chosen one: the input's own state, or
  // what an ARIA radio says of itself.
  const checked = (el) => el.tagName === 'INPUT' ? el.checked === true :
    String(el.getAttribute('aria-checked') || '').toLowerCase() === 'true';

  const out = [];
  [...agentifiScope.querySelectorAll(selector)].forEach((el, at) => {
    if (!reachable(el)) return;
    const picks = selects(el);
    let words = picks ? nameOf(el) : clean(textOf(el) || el.getAttribute('aria-label') || '');
    // An ARIA radio is named by its own content, the way a button is: the
    // words are inside it, and nothing labels it from outside.
    if (words === '' && picks && el.tagName !== 'INPUT') words = textOf(el);
    if (words === '' && picks) words = nearby(el);
    if (words === '') return;
    out.push({
      at,
      kind: picks ? 'radio' : (el.tagName === 'A' ? 'link' : 'button'),
      words: words.slice(0, 120),
      selects: picks,
      checked: picks && checked(el),
    });
  });
  // On a page with radios on it, the radios are the menu and the buttons are
  // how it is sent: a Continue listed as a way to verify is a choice nobody
  // was offered. A page with no radios is the other shape, where the choices
  // are themselves the buttons.
  const chosen = out.filter((one) => one.selects);
  return (chosen.length > 0 ? chosen : out).slice(0, 20);
}`

// markFactorScript marks the choice the ranking picked, so the click lands on
// it and not on whatever a union matches first. A radio is marked through its
// label whenever it has one: that is how a person selects it, and the only way
// that works where an empty `<label for>` is stretched over a visible input and
// swallows every click. The radio itself is marked too, for the tick the click
// falls back to.
const markFactorScript = `(pick) => {` + agent.DialogScopeJS + `
` + agent.VisibleJS + agent.CleanJS + agent.EscapedJS + agent.NameOfJS + `
  for (const stale of document.querySelectorAll(pick.mark)) stale.removeAttribute(pick.attribute);
  for (const stale of document.querySelectorAll(pick.radioMark)) stale.removeAttribute(pick.radioAttribute);
  const el = [...agentifiScope.querySelectorAll(pick.selector)][pick.at];
  if (!el) return false;
  const selects = (el.tagName === 'INPUT' && String(el.type || '').toLowerCase() === 'radio') ||
    String(el.getAttribute('role') || '').toLowerCase() === 'radio';
  let target = el;
  if (selects) {
    el.setAttribute(pick.radioAttribute, '');
    const wrapping = el.closest ? el.closest('label') : null;
    const bound = boundTo(el);
    target = [wrapping, bound].find(visible) || el;
  }
  if (!visible(target)) return false;
  target.setAttribute(pick.attribute, '');
  return true;
}`

type FactorChoice = agent.FactorChoice

// maskedDetail is the tail of a phone number or account a portal prints in a
// choice. A trail is pasted further than a screen, so it comes off. Nothing
// the ranking reads carries a digit.
var maskedDetail = regexp.MustCompile(`\d{2,}`)

// FactorChoices is the menu a factor page is offering, read and never pressed.
func FactorChoices(page browser.Page) ([]FactorChoice, error) {
	var choices []FactorChoice
	if err := browser.EvaluateInto(page, factorChoicesScript, factorChoiceSelector, &choices); err != nil {
		return nil, err
	}
	for at := range choices {
		choices[at].Words = strings.TrimSpace(maskedDetail.ReplaceAllString(choices[at].Words, "…"))
	}
	return choices, nil
}

// FactorKind is which second factor a choice's words name: "totp", "sms",
// "email", or "". The order is the whole rule: a spoken factor first, because
// "Call me with a code from the app" carries the app's words; a text before
// the app for the same reason; e-mail after text, because "Text or e-mail me"
// is a text first; then the app; a tap on a phone is nothing; and only then a
// phone by name or masked number, because "the app on your phone" is the app.
func FactorKind(words string) string {
	switch {
	case spokenWords.MatchString(words):
		return ""
	case textMessageWords.MatchString(words):
		return "sms"
	case emailWords.MatchString(words):
		return "email"
	case authenticatorWords.MatchString(words):
		return "totp"
	case pushWords.MatchString(words):
		return ""
	case phoneWords.MatchString(words):
		return "sms"
	}
	return ""
}

var emailWords = regexp.MustCompile(`(?i)\be-?mail\b|\S@\S`)

// FactorRank is how well a choice answers an unattended sign-in: 1 for an
// authenticator app (the engine mints the code), 2 for a text (parks a
// challenge for a person), 0 for anything else. E-mail is 0 and taken only
// when the login prefers it, because not every household's mailbox answers.
func FactorRank(choice FactorChoice) int {
	switch FactorKind(choice.Words) {
	case "totp":
		return 1
	case "sms":
		return 2
	}
	return 0
}

// BestFactor is the choice a sign-in takes. A login that named its way takes
// that way or nothing: any other sends a code where nobody is waiting for it.
// A login that named none takes the ranking, with the page's order within a
// rank.
func BestFactor(choices []FactorChoice, prefer string) (FactorChoice, bool) {
	if prefer != "" {
		for _, choice := range choices {
			if FactorKind(choice.Words) == prefer {
				return choice, true
			}
		}
		return FactorChoice{}, false
	}
	best, found, bestRank := FactorChoice{}, false, 0
	for _, choice := range choices {
		rank := FactorRank(choice)
		if rank == 0 {
			continue
		}
		if !found || rank < bestRank {
			best, bestRank, found = choice, rank, true
		}
	}
	return best, found
}

// ChooseFactor takes the way BestFactor picks and says what the page was
// offering either way. The menu is read whole before
// anything is pressed.
//
// A radio selects and sends nothing, so the page's own button is pressed after
// it; a choice that is itself a submit button has already sent the page on.
// The page says which (`Selects`) rather than Go guessing from the tag. A radio
// the page already selected is not clicked again. A choice that could not be
// taken ends the sign-in at once, naming it.
func (d Draft) ChooseFactor(page browser.Page, prefer string) (agent.Factor, error) {
	choices, err := FactorChoices(page)
	if err != nil {
		return agent.Factor{}, err
	}
	factor := agent.Factor{Choices: choices}
	best, found := BestFactor(choices, prefer)
	if !found {
		return factor, nil
	}
	// The choice is recorded before it is taken, so every way out carries what was
	// on offer and what was picked.
	factor.Kind, factor.Chose = FactorKind(best.Words), best.Words
	marked, err := page.Evaluate(markFactorScript, map[string]any{
		"selector": factorChoiceSelector, "at": best.At,
		"mark": factorMark, "attribute": factorAttribute,
		"radioMark": factorRadioMark, "radioAttribute": factorRadioAttribute,
	})
	if err != nil || marked != true {
		return factor, fmt.Errorf("%s offered %q and drew nothing that stands for it", d.Name(), best.Words)
	}
	if best.Selects && best.Checked {
		step, err := d.submit(page)
		if err != nil {
			return factor, err
		}
		factor.Confirmed, factor.Step = true, step
		return factor, nil
	}
	before := agent.Signature(page)
	took, err := page.Choose(factorMark, factorRadioMark)
	if err != nil || took == "" {
		// Playwright's account of a click that never landed is a thirty-second retry
		// log; the household is told which choice the page would not take.
		return factor, fmt.Errorf("%s would not let %q be chosen", d.Name(), best.Words)
	}
	factor.Forced = took == browser.TookForce
	if !best.Selects {
		agent.AwaitChange(page, before)
		return factor, nil
	}
	// A portal may reveal the rest of the step once a way is chosen.
	page.Sleep(agent.Settle)
	step, err := d.submit(page)
	if err != nil {
		return factor, err
	}
	factor.Confirmed, factor.Step = true, step
	return factor, nil
}
