package billers

import (
	"regexp"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// What a page looked like: where a sign-in ended, because the session's page
// closes with a failure, and each page a pull reads, so a pull that read less
// than the household expected can be accounted for afterwards. Its headings,
// the start of its words and every control on it, shadow roots and
// same-origin frames included.
//
// Nothing typed and nothing secret: no field's value, no attribute whose name
// says token, secret or password, and no query or fragment of an address (an
// href carries tokens there as often as not). A run of four or more digits
// comes off wherever it is, because on a sign-in page that is a phone number's
// tail, an account's or a one-time code, and this is carried on a trail the
// household may paste; only a year a date prints stays.

// snapshotScript reads the page's structure. Bounded in the page as well as
// here, so a page of ten thousand links costs a hundred and twenty lines. A
// reading of a signed-in page (paths) names each link's path and classes, and
// walks the dialog in front, else the page's main region, else the document:
// the menus around a billing page are not what it is read for.
const snapshotScript = `(paths) => {` + agent.DialogScopeJS + `
` + agent.VisibleJS + agent.CleanJS + `
  const secret = /token|secret|password/i;
  const typed = (el) => ['INPUT', 'SELECT', 'TEXTAREA'].includes(el.tagName);
  const interactive = 'input, button, a, select, textarea, dialog, iframe, embed, object, label, [role], [tabindex], [aria-modal]';
  const kept = ['role', 'type', 'name', 'aria-label', 'autocomplete', 'inputmode', 'maxlength', 'aria-modal', 'aria-checked', 'aria-disabled'];
  const tree = [];
  let budget = 120;
  const pathOf = (address) => {
    try {
      const link = new URL(address, location.href);
      if (link.protocol === 'javascript:') return 'script';
      if (link.protocol === 'blob:') return 'blob';
      if (link.origin === location.origin && link.pathname === location.pathname && link.hash) return '#';
      return link.origin === location.origin ? link.pathname : link.host + link.pathname;
    } catch (e) {
      return '';
    }
  };
  const describe = (el) => {
    const parts = [el.tagName.toLowerCase() + (el.id ? '#' + clean(el.id).slice(0, 60) : '')];
    for (const name of kept) {
      if (secret.test(name) || !el.hasAttribute(name)) continue;
      parts.push(name + '=' + clean(el.getAttribute(name)).slice(0, 60));
    }
    if (paths) {
      const address = el.getAttribute('href') || el.getAttribute('src') || el.getAttribute('data') || '';
      const at = address === '' ? '' : pathOf(address);
      if (at !== '') parts.push('href=' + clean(at).slice(0, 100));
      if (typeof el.className === 'string' && el.className.trim() !== '') {
        parts.push('.' + clean(el.className).split(' ').slice(0, 3).join('.').slice(0, 60));
      }
    }
    if (el.disabled === true) parts.push('disabled');
    if (el.tagName === 'INPUT' && ['checkbox', 'radio'].includes(String(el.type).toLowerCase())) {
      parts.push(el.checked ? 'ticked' : 'unticked');
    }
    const words = typed(el) || el.tagName === 'IFRAME' ? '' : clean(el.innerText || el.textContent).slice(0, 80);
    if (words !== '') parts.push('"' + words + '"');
    return parts.join(' ');
  };
  const walk = (root, depth) => {
    if (depth > 12) return;
    for (const el of root.querySelectorAll('*')) {
      if (budget <= 0) return;
      const tag = el.tagName.toLowerCase();
      const hidden = tag === 'input' && String(el.type).toLowerCase() === 'hidden';
      if (!hidden && (el.matches(interactive) || (el.shadowRoot && tag.includes('-'))) && visible(el)) {
        tree.push('  '.repeat(depth) + describe(el));
        budget -= 1;
      }
      if (el.shadowRoot) {
        tree.push('  '.repeat(depth + 1) + '#shadow-root');
        walk(el.shadowRoot, depth + 2);
      }
      if (tag === 'iframe' && visible(el)) {
        let inner = null;
        try { inner = el.contentDocument; } catch (e) { inner = null; }
        if (inner && inner.body) {
          tree.push('  '.repeat(depth + 1) + '#frame');
          walk(inner, depth + 2);
        } else {
          tree.push('  '.repeat(depth + 1) + '#frame from another origin, not read');
        }
      }
    }
  };
  const start = !paths ? document :
    agentifiScope !== document ? agentifiScope : (document.querySelector('main, [role="main"]') || document);
  walk(start, 0);
  const said = start === document ? document.body : start;
  const headings = [...document.querySelectorAll('h1, h2, h3')].filter(visible)
    .map((el) => clean(el.innerText)).filter((line) => line !== '').slice(0, 6);
  return {
    dialog: agentifiScope === document ? '' : clean(agentifiScope.getAttribute('aria-label') || agentifiScope.tagName.toLowerCase()),
    headings,
    text: clean(said ? said.innerText : '').slice(0, 2400),
    tree,
    paths: !!paths,
  };
}`

// snapshotLimit is how long a snapshot may be, in characters: enough for the
// headings, a paragraph of the page and a screenful of controls.
const snapshotLimit = 4000

// longDigits is what comes off a snapshot wherever it appears, and datedYear
// a year a date prints, which stays.
var (
	longDigits = regexp.MustCompile(`\d{4,}`)
	datedYear  = regexp.MustCompile(
		`(?i)(\b\d{1,2}/\d{1,2}/|\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s*\d{1,2},?\s*)((?:19|20)\d\d)\b`)
)

type PageSnapshot struct {
	Dialog   string   `json:"dialog"`
	Headings []string `json:"headings"`
	Text     string   `json:"text"`
	Tree     []string `json:"tree"`
	// Paths says the page was read as a signed-in page a pull reads, whose
	// words are worth more of the snapshot than a sign-in page's.
	Paths bool `json:"paths"`
}

// Snapshot is the page as a failed sign-in leaves it, as text, or "" for a
// page that could not be read.
func Snapshot(page browser.Page) string { return snapshotOf(page, false) }

// ReadingSnapshot is a signed-in page as a pull reads it: Snapshot with each
// link's path, from the dialog in front or the page's main region.
func ReadingSnapshot(page browser.Page) string { return snapshotOf(page, true) }

func snapshotOf(page browser.Page, paths bool) string {
	var read PageSnapshot
	if err := browser.EvaluateInto(page, snapshotScript, paths, &read); err != nil {
		return ""
	}
	return SnapshotText(read)
}

// SnapshotText is a snapshot as the trail carries it: capped, and with every
// long run of digits taken off. Pure, so the redaction is a test's to hold.
func SnapshotText(read PageSnapshot) string {
	var out strings.Builder
	if read.Dialog != "" {
		out.WriteString("dialog in front: " + read.Dialog + "\n")
	}
	if len(read.Headings) > 0 {
		out.WriteString("headings: " + strings.Join(read.Headings, " · ") + "\n")
	}
	if read.Text != "" {
		words := 600
		if read.Paths {
			words = 1500
		}
		out.WriteString("text: " + textutil.ClipMarked(read.Text, words) + "\n")
	}
	if len(read.Tree) > 0 {
		out.WriteString("controls:\n")
		for _, line := range read.Tree {
			out.WriteString("  " + textutil.ClipMarked(line, 200) + "\n")
		}
	}
	return textutil.ClipMarked(withoutLongDigits(strings.TrimRight(out.String(), "\n")), snapshotLimit)
}

// withoutLongDigits takes every run of four or more digits off but a year a
// date prints.
func withoutLongDigits(text string) string {
	const kept = "\x00"
	marked := datedYear.ReplaceAllStringFunc(text, func(date string) string {
		found := datedYear.FindStringSubmatch(date)
		return found[1] + strings.Join(strings.Split(found[2], ""), kept)
	})
	return strings.ReplaceAll(longDigits.ReplaceAllString(marked, "…"), kept, "")
}
