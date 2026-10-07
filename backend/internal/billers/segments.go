package billers

import (
	"fmt"
	"strconv"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// A code typed one character per box, under a Confirm that stays disabled
// until every box is full. Widgets either move the cursor to the next box as
// each fills, so typing into the first fills them all (what a person does,
// tried first), or keep each box its own field, which a fill of each box in
// turn handles.

// codeGroupsJS is a script prelude: `agentifiGroupOf(input)` is which run of
// one-character boxes a box belongs to, counted from 1, or 0 for a box that is
// not one character wide.
//
// Which boxes belong together is the nearest box around them holding more than
// one. A box is one character wide by its maxlength, or, for a widget that
// limits it in script, by being a narrow numeric or one-time-code box with no
// maxlength.
const codeGroupsJS = `
const agentifiGroupOf = (() => {
` + agent.VisibleJS + `
  const single = (i) => i.maxLength === 1 || (i.maxLength < 0 &&
    (String(i.inputMode || '').toLowerCase() === 'numeric' || i.autocomplete === 'one-time-code') &&
    i.offsetWidth > 0 && i.offsetWidth <= 80);
  const groups = new Map();
  return (input) => {
    if (!single(input)) return 0;
    let at = input.parentElement;
    for (let up = 0; up < 4 && at; up += 1) {
      if ([...at.querySelectorAll('input')].filter(visible).filter(single).length >= 2) {
        if (!groups.has(at)) groups.set(at, groups.size + 1);
        return groups.get(at);
      }
      at = at.parentElement;
    }
    return 0;
  };
})();
`

// digitAttribute is what markSegmentsScript puts on each box of the code, with
// its place in the code as the value.
const digitAttribute = `data-agentifi-digit`

// markSegmentsScript marks the boxes of the one run that is a code — by the
// rule codeGroup applies to the page reading, run over the same boxes — and
// answers how many there are, or 0.
const markSegmentsScript = `(limits) => {` + agent.DialogScopeJS + codeGroupsJS + `
` + agent.VisibleJS + `
  const typed = (i) => ['text', 'tel', 'number', 'search', 'email', 'password', 'url', ''].includes(String(i.type || '').toLowerCase());
  for (const stale of document.querySelectorAll('[data-agentifi-digit]')) stale.removeAttribute('data-agentifi-digit');
  const runs = new Map();
  for (const input of [...agentifiScope.querySelectorAll('input')].filter(visible).filter(typed)) {
    const group = agentifiGroupOf(input);
    if (group === 0) continue;
    if (!runs.has(group)) runs.set(group, []);
    runs.get(group).push(input);
  }
  const codes = [...runs.values()].filter((run) => run.length >= limits.min && run.length <= limits.max);
  if (codes.length !== 1) return 0;
  codes[0].forEach((input, at) => input.setAttribute('data-agentifi-digit', String(at)));
  return codes[0].length;
}`

// segmentsFilledScript is how many of the marked boxes hold a character. A
// count and never what they hold.
const segmentsFilledScript = `() => [...document.querySelectorAll('[data-agentifi-digit]')]
  .filter((input) => String(input.value || '') !== '').length`

func digitMark(at int) string {
	return `[` + digitAttribute + `="` + strconv.Itoa(at) + `"]`
}

// answerSegments types a code into a page whose code is a box per character,
// and says whether the page was one.
func (d Draft) answerSegments(page browser.Page, code string) (bool, error) {
	boxes, ok := evaluateCount(page, markSegmentsScript, map[string]int{"min": minSegments, "max": maxSegments})
	if !ok || boxes == 0 {
		return false, nil
	}
	want := min(boxes, len([]rune(code)))
	if err := page.Click(digitMark(0)); err != nil {
		return true, err
	}
	if err := page.Type(code); err != nil {
		return true, err
	}
	if filled, _ := evaluateCount(page, segmentsFilledScript, nil); filled >= want {
		return true, nil
	}
	for at, r := range []rune(code) {
		if at >= boxes {
			break
		}
		if err := page.Fill(digitMark(at), string(r)); err != nil {
			return true, err
		}
	}
	if filled, _ := evaluateCount(page, segmentsFilledScript, nil); filled < want {
		return true, fmt.Errorf("%s's code boxes would not take the code", d.Name())
	}
	return true, nil
}

func evaluateCount(page browser.Page, script string, arg any) (int, bool) {
	var count int
	if err := browser.EvaluateInto(page, script, arg, &count); err != nil {
		return 0, false
	}
	return count, true
}
