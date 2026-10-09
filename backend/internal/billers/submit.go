package billers

import (
	"fmt"
	"strconv"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// commitFieldScript is what leaving a field does: `fill` dispatches `input`,
// and a widget that validates on `change` or blur (which decides whether its
// submit button is pressable) hears neither.
const commitFieldScript = `() => {
  const el = document.activeElement;
  if (!el || el === document.body) return false;
  el.dispatchEvent(new Event('change', { bubbles: true }));
  if (typeof el.blur === 'function') el.blur();
  return true;
}`

// pressableJS says whether a control can take a press: not disabled by the
// property, by the aria attribute a component library sets, or by the computed
// pointer-events a component library's disabled class sets, which Playwright
// waits on like a disabled attribute.
const pressableJS = `
const agentifiPressable = (el) => el.disabled !== true && el.getAttribute('aria-disabled') !== 'true' &&
  getComputedStyle(el).pointerEvents !== 'none';
`

// submitControlsScript is every control a step could press, as the page sees
// it: its words, whether an attribute says it submits, and whether it can be
// pressed yet. Read, not decided, so SubmitRank is pure. The query is the
// dialog in front of the page when there is one, then open shadow roots
// (scope.go): a Confirm behind a dialog cannot be pressed, and a component
// library's own button can.
const submitControlsScript = `(selector) => {` + agent.DialogScopeJS + deepAllJS + pressableJS + `
` + agent.VisibleJS + agent.CleanJS + `
  const said = (el) => clean(el.innerText || el.textContent || el.value || '');
  const out = [];
  agentifiDeepAll(agentifiScope, selector).forEach((el, at) => {
    if (!visible(el)) return;
    out.push({
      at,
      kind: el.tagName === 'INPUT' ? 'input' : 'button',
      words: said(el).slice(0, 80),
      typed: (el.getAttribute('type') || '').toLowerCase() === 'submit',
      disabled: !agentifiPressable(el),
    });
  });
  return out;
}`

// markSubmitScript marks the control the ranking picked and clears an earlier
// round's marks.
const markSubmitScript = `(pick) => {` + agent.DialogScopeJS + deepAllJS + `
  for (const stale of agentifiDeepAll(document, pick.mark)) stale.removeAttribute(pick.attribute);
  const el = agentifiDeepAll(agentifiScope, pick.selector)[pick.at];
  if (!el) return false;
  el.setAttribute(pick.attribute, '');
  return true;
}`

// submitEnabledScript is the marked control being pressable.
const submitEnabledScript = `() => {` + deepAllJS + pressableJS + `
  const el = agentifiDeepAll(document, '[data-agentifi-submit]')[0];
  return !!el && agentifiPressable(el);
}`

// submitWait is how long the control is given to be pressable just before the
// press: a widget enables it a paint after the fill, and a form that validates
// on the server or refreshes a page check's token disables it again for a
// moment. Bounded, because a page whose button never enables is one to be
// honest about.
const submitWait = 10 * time.Second

// submit presses the form's own button and waits for the page to answer: look
// away from the field (blur validation), find the control that sends the step
// on by its words (a two-step sign-in's first button says Next), give it a
// moment to become pressable, and wait for the page to differ. Enter is the
// last resort: it works on a plain form and a React form ignores it.
func (d Draft) submit(page browser.Page) (agent.Step, error) {
	d.commitField(page)
	before := agent.Signature(page)
	step, err := d.press(page)
	if err != nil {
		return agent.Step{}, err
	}
	agent.AwaitChange(page, before)
	step.Acted = true
	// A page unreadable before the press counts as changed: "unreadable" is a
	// different finding from "went nowhere", which the loop guard gives up on.
	step.Changed = before == "" || agent.Signature(page) != before
	return step, nil
}

// submitAfter is submit, with the cookie banner declined before typing carried
// on the step for the trail.
func (d Draft) submitAfter(page browser.Page, dismissed string) (agent.Step, error) {
	step, err := d.submit(page)
	step.Dismissed = dismissed
	return step, err
}

// press presses the control the page is offering, or Enter because nothing on
// it matched, and says which it was. The control is read after the page
// check has cleared, since a page holds its button disabled until then, and
// is waited on right before the click whatever that reading said, since a
// form can disable it again while it validates.
func (d Draft) press(page browser.Page) (agent.Step, error) {
	if AwaitPageCheck(page) {
		control, _ := d.chooseSubmit(page)
		return agent.Step{}, pageCheckHeld(fmt.Sprintf("%s's page check had not cleared after %s, so %s was not pressed",
			d.Name(), PageCheckWait, buttonWords(control)))
	}
	enter := agent.Step{Pressed: agent.PressedEnter}
	if control, ok := d.chooseSubmit(page); ok {
		// Pressing a control that is not pressable is a click Playwright waits
		// on until it times out.
		if page.WaitForFunction(submitEnabledScript, submitWait) == nil {
			if d.PressDirectly {
				if step, pressed := d.pressDirectly(page, control); pressed {
					return step, nil
				}
			}
			pressed, err := page.ClickVisible(submitMark)
			if browser.IsTimeout(err) {
				return d.timedOut(page, control, err)
			}
			if err != nil {
				return agent.Step{}, err
			}
			if pressed {
				return pressedStep(control), nil
			}
		} else {
			enter.Words, enter.Waited = control.Words, true
		}
	}
	if err := page.Press("Enter"); err != nil {
		return agent.Step{}, err
	}
	return enter, nil
}

// pressDirectly is the forced click a PressDirectly provider's button takes,
// only where the page answers that nothing is on top of it and it is
// pressable. Pressed false leaves the ordinary press to run.
func (d Draft) pressDirectly(page browser.Page, control SubmitControl) (agent.Step, bool) {
	if _, clear := d.unpressed(page, control); !clear {
		return agent.Step{}, false
	}
	pressed, err := page.ForceClickVisible(submitMark)
	if err != nil && browser.IsTimeout(err) && browser.ClickPerformed(err) {
		pressed, err = true, nil
	}
	if err != nil || !pressed {
		return agent.Step{}, false
	}
	step := pressedStep(control)
	step.Forced = true
	step.Note = buttonWords(control) + " was pressed directly, past Playwright's checks, with nothing on top of it and pressable"
	return step, true
}

func pressedStep(control SubmitControl) agent.Step {
	return agent.Step{Pressed: control.Kind, Words: control.Words, Waited: control.Disabled}
}

// timedOut is the click Playwright gave up on. A call log showing the click
// done is a press whose navigation outlasted the click's wait, which the
// round's own wait for the page covers. A control the page shows pressable
// with nothing on top is pressed once more past Playwright's checks: the page
// check has cleared and the target is the one read, so what Playwright was
// waiting on is nothing the press needs. Anything else is unpressed.
func (d Draft) timedOut(page browser.Page, control SubmitControl, clicked error) (agent.Step, error) {
	button := buttonWords(control)
	log := browser.ClickLog(clicked)
	if browser.ClickPerformed(clicked) {
		step := pressedStep(control)
		step.Note = playwrightSaid(button+" was pressed and the page it led to outlasted the click's wait", log)
		return step, nil
	}
	said, clear := d.unpressed(page, control)
	if !clear {
		return agent.Step{}, &PressFailure{Said: said, Note: playwrightSaid(button+" could not be pressed", log)}
	}
	pressed, err := page.ForceClickVisible(submitMark)
	if err != nil && browser.IsTimeout(err) && browser.ClickPerformed(err) {
		pressed, err = true, nil
	}
	if err != nil || !pressed {
		note := playwrightSaid(button+" could not be pressed, with nothing on top of it and pressable, nor by a forced click", log)
		if forced := browser.ClickLog(err); forced != "" {
			note += "; the forced click: " + forced
		}
		return agent.Step{}, &PressFailure{Said: said, Note: note}
	}
	step := pressedStep(control)
	step.Forced = true
	step.Note = playwrightSaid(button+" was pressed past Playwright's checks after its click timed out with nothing on top of it and pressable", log)
	return step, nil
}

func playwrightSaid(sentence, log string) string {
	if log == "" {
		return sentence
	}
	return sentence + "; Playwright: " + log
}

// PressFailure is a press that did not land: Said is the sentence a person
// reads, Note how the press went with Playwright's account of the wait, for
// the sign-in's notes and trail.
type PressFailure struct {
	Said string
	Note string
}

func (f *PressFailure) Error() string { return f.Said }

// unpressed is the press that timed out, said as what the page shows: what is
// on top at the control's centre when something is, the control itself still
// disabled when it is, and neither claimed when the page could say neither.
// Clear says the page answered that nothing is on top and the control is
// pressable.
func (d Draft) unpressed(page browser.Page, control SubmitControl) (string, bool) {
	button := buttonWords(control)
	cover, unread := readCover(page)
	if cover != nil {
		if cover.IsPageCheck() {
			return fmt.Sprintf("%s's page check had not cleared and covered %s, so it could not be pressed",
				d.Name(), button), false
		}
		return fmt.Sprintf("%s on %s's page covered %s, so it could not be pressed",
			cover.Describe(), d.Name(), button), false
	}
	pressable, err := page.Evaluate(submitEnabledScript, nil)
	if err == nil && pressable == false {
		return fmt.Sprintf("%s on %s's page stayed disabled, so it could not be pressed", button, d.Name()), false
	}
	return fmt.Sprintf("%s on %s's page could not be pressed", button, d.Name()),
		unread == nil && err == nil && pressable == true
}

func buttonWords(control SubmitControl) string {
	if control.Words == "" {
		return "the sign-in button"
	}
	return "the " + strconv.Quote(control.Words) + " button"
}

func (d Draft) chooseSubmit(page browser.Page) (SubmitControl, bool) {
	controls, err := SubmitControls(page)
	if err != nil {
		return SubmitControl{}, false
	}
	best, found := BestSubmit(controls)
	if !found {
		return SubmitControl{}, false
	}
	marked, err := page.Evaluate(markSubmitScript, map[string]any{
		"selector": submitControlSelector, "at": best.At,
		"mark": submitMark, "attribute": submitAttribute,
	})
	if err != nil || marked != true {
		return SubmitControl{}, false
	}
	return best, true
}

// SubmitControl is one control a step could press, as the page sees it.
type SubmitControl struct {
	// At is the control's place in the page's own query, which is how the
	// press finds the one the ranking picked.
	At   int    `json:"at"`
	Kind string `json:"kind"`
	// Words is the control's own text, capped: the provider's, never the
	// household's.
	Words string `json:"words"`
	Typed bool   `json:"typed"`
	// Disabled is the control not taking a press, by any of the spellings
	// pressableJS reads.
	Disabled bool `json:"disabled"`
}

// SubmitControls is every control on the page a step could press, read and
// never pressed.
func SubmitControls(page browser.Page) ([]SubmitControl, error) {
	var controls []SubmitControl
	if err := browser.EvaluateInto(page, submitControlsScript, submitControlSelector, &controls); err != nil {
		return nil, err
	}
	return controls, nil
}

// BestSubmit is the control a step presses. Vocabulary ranks first and
// pressability second, so a disabled "Sign in" beats an enabled "Search"
// submit: a control not yet pressable is waited for, a wrong one never is.
func BestSubmit(controls []SubmitControl) (SubmitControl, bool) {
	best, found, bestRank := SubmitControl{}, false, 0
	for _, control := range controls {
		rank := SubmitRank(control)
		if rank == 0 {
			continue
		}
		if !found || rank < bestRank || (rank == bestRank && best.Disabled && !control.Disabled) {
			best, bestRank, found = control, rank, true
		}
	}
	return best, found
}

// SubmitRank is how well one control says it sends this step on: 1 for a
// control whose whole text is the act, 2 for one that says in an attribute
// that it submits the form, 3 for the act said at more length, and 0 for a
// control this step does not press.
// Pure over what the page reported, so the probe and the press never disagree.
func SubmitRank(control SubmitControl) int {
	switch {
	case submitWords.MatchString(control.Words):
		return 1
	case control.Typed:
		return 2
	case submitLeadWords.MatchString(control.Words):
		return 3
	}
	return 0
}

// commitField's errors are nothing to report: the submit is about to find out.
func (d Draft) commitField(page browser.Page) {
	_, _ = page.Evaluate(commitFieldScript, nil)
}
