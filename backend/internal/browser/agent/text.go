package agent

import "github.com/CornHead764/agentifi/backend/internal/browser"

// The small readers a sign-in page script builds on: a string cleaned for
// comparison, an id made safe for a selector, a control's name from outside
// itself, and the words a page is complaining with.

// CleanJS declares clean(value): a string's runs of whitespace collapsed to
// one space each and trimmed, the form a reading or an error line is
// compared and capped in. Defined in browser, which this package imports
// and reads page text for in its own right.
const CleanJS = browser.CleanJS

// EscapedJS declares escaped(value): value through CSS.escape, the form an
// id or attribute value must take to drop safely into a selector, or value
// itself where this page has no CSS.escape to call. Defined in browser, for
// the same reason as CleanJS.
const EscapedJS = browser.EscapedJS

// NameOfJS declares textOf(el) (el's own cleaned text), boundTo(el) (the
// label a for= points at, or null), labelOf(el) (the label el sits inside,
// or the one bound to it by for=), and nameOf(el): el's name from outside
// itself — aria-label, aria-labelledby, a wrapping label, a label bound by
// for=, or a label beside it. Never el's own text, which a caller reads on
// top where an unlabelled control still names itself, such as a button or a
// link. Requires EscapedJS and CleanJS already spliced into the same scope.
const NameOfJS = `
const textOf = (el) => clean(el.innerText || el.textContent || '');
const boundTo = (el) => (el.id ? document.querySelector('label[for="' + escaped(el.id) + '"]') : null);
const labelOf = (el) => (el.closest ? el.closest('label') : null) || boundTo(el);
const nameOf = (el) => {
  const said = [];
  const aria = el.getAttribute('aria-label');
  if (aria) said.push(clean(aria));
  const by = el.getAttribute('aria-labelledby');
  if (by) {
    for (const id of by.split(/\s+/)) {
      const at = document.getElementById(id);
      if (at) said.push(textOf(at));
    }
  }
  const label = labelOf(el);
  if (label) said.push(textOf(label));
  const beside = el.nextElementSibling;
  if (beside && beside.tagName === 'LABEL' && beside !== label) said.push(textOf(beside));
  return clean(said.join(' '));
};
`

// DrawnJS declares agentifiDrawn(el): a box with none of display:none,
// visibility:hidden or opacity:0 and a nonzero rect — a page's own word that
// something is drawn, read where a control (an error box, a working
// overlay, a radio's label) must be checked apart from VisibleJS's stricter,
// Playwright-matching rule.
const DrawnJS = `
const agentifiDrawn = (el) => {
  if (!el) return false;
  const style = window.getComputedStyle(el);
  if (style.visibility === 'hidden' || style.display === 'none' || style.opacity === '0') return false;
  const box = el.getBoundingClientRect();
  return box.width > 0 && box.height > 0;
};
`

// HeadingsJS declares headingsOf(scope, selector): the visible elements
// under scope matching selector, as cleaned text with the empty ones
// dropped. Requires VisibleJS and CleanJS already spliced into the same
// scope.
const HeadingsJS = `
const headingsOf = (scope, selector) => [...scope.querySelectorAll(selector)].filter(visible)
  .map((el) => clean(el.innerText)).filter((line) => line !== '');
`

// PageTextJS declares pageTextOf(scope): scope's own words when it is a
// dialog, or the body's when it is the document, capped to the length a
// note or a signature can carry.
const PageTextJS = `
const pageTextOf = (scope) => String(scope === document ?
  (document.body && document.body.innerText ? document.body.innerText : '') :
  (scope.innerText || '')).slice(0, 20000);
`

// CollapseSpacesJS declares collapseSpaces(el): el's own text with its runs
// of spaces and tabs collapsed to one each and trimmed, its line breaks kept
// — a table a note quotes a row of, where a blank line between cells is
// itself a fact about the row.
const CollapseSpacesJS = `
const collapseSpaces = (el) => String((el && el.innerText) || '').replace(/[ \t]+/g, ' ').trim();
`

// TroublePatternJS is the regex literal a page's own words are tested
// against for a sign-in's refusal: wrong credentials, a locked or expired
// account, a code that did not match, or a refusal the page names only as
// an error.
const TroublePatternJS = `/invalid|incorrect|try again|locked|expired|not recognized|not recognised|does not match|error/i`

// SubmitSelector is a sign-in page's own submit control: a submit button, a
// submit input, or a form's only untyped button.
const SubmitSelector = `button[type="submit"], input[type="submit"], button:not([type])`

// SubmitSelectorJS is SubmitSelector quoted for splicing into a
// querySelectorAll call in a page script.
const SubmitSelectorJS = "'" + SubmitSelector + "'"
