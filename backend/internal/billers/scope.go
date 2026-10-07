package billers

import (
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// deepAllJS is a script prelude: `agentifiDeepAll(root, selector)` is every
// match under root and inside every open shadow root beneath it, the light
// DOM's matches first, so a control inside a component library's shadow root
// only wins a ranking nothing in the light DOM already won.
const deepAllJS = `
const agentifiDeepAll = (root, selector) => {
  const out = [...root.querySelectorAll(selector)];
  const walk = (at, depth) => {
    if (depth > 8) return;
    for (const host of at.querySelectorAll('*')) {
      if (!host.shadowRoot) continue;
      out.push(...host.shadowRoot.querySelectorAll(selector));
      walk(host.shadowRoot, depth + 1);
    }
  };
  walk(root, 0);
  return out;
};
`

// scopeAttribute marks the dialog in front of the page, so Playwright's
// selectors in the fills can be aimed inside it.
const (
	scopeAttribute = `data-agentifi-scope`
	scopeMark      = `[` + scopeAttribute + `]`
)

// markScopeScript marks the dialog in front of the page, takes the mark off
// anything an earlier round marked, and says whether there was one.
const markScopeScript = `() => {` + agent.DialogScopeJS + `
  for (const stale of document.querySelectorAll('[data-agentifi-scope]')) stale.removeAttribute('data-agentifi-scope');
  if (agentifiScope === document) return false;
  agentifiScope.setAttribute('data-agentifi-scope', '');
  return true;
}`

// pageScope is where a step's selectors are aimed: the whole page, or inside
// the dialog in front of it.
type pageScope struct{ dialog bool }

// scopeOf marks the dialog in front of the page, if there is one, and answers
// the scope a step's selectors go through. A page that cannot be asked is the
// whole page.
func scopeOf(page browser.Page) pageScope {
	marked, err := page.Evaluate(markScopeScript, nil)
	return pageScope{dialog: err == nil && marked == true}
}

// sel is a selector union aimed inside the dialog, or unchanged on a page with
// none. Each alternative is prefixed, because `[mark] a, b` is `b` anywhere.
func (s pageScope) sel(selector string) string {
	if !s.dialog {
		return selector
	}
	parts := splitUnion(selector)
	for at, part := range parts {
		parts[at] = scopeMark + " " + part
	}
	return strings.Join(parts, ", ")
}

// splitUnion is a selector list cut at its top-level commas: not inside an
// attribute's brackets, a pseudo-class's parentheses or a quoted string.
func splitUnion(selector string) []string {
	var parts []string
	depth, quote, start := 0, rune(0), 0
	for at, r := range selector {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '[' || r == '(':
			depth++
		case r == ']' || r == ')':
			depth--
		case r == ',' && depth == 0:
			parts = append(parts, strings.TrimSpace(selector[start:at]))
			start = at + 1
		}
	}
	return append(parts, strings.TrimSpace(selector[start:]))
}
