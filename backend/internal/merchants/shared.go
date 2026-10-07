package merchants

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
)

// The readers both modules share. A page is read once per look, with one
// script answering every selector a classifier asks about: a locator call per
// selector gives the page a chance to move underneath the reading each time.

type reading struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Text is the body's own words, collapsed and capped.
	Text string `json:"text"`
	// Visible says, per name, whether a visible match of that group exists;
	// Texts is the first visible match's own text.
	Visible map[string]bool   `json:"visible"`
	Texts   map[string]string `json:"texts"`
}

func (r reading) shows(group string) bool { return r.Visible[group] }

func (r reading) textOf(group string) string { return r.Texts[group] }

// readPageScript counts visible matches, not present ones: a sign-in page
// carries fields nobody can type into, and counting those reads the wrong
// screen.
const readPageScript = `(groups) => {` + agent.DrawnJS + agent.CleanJS + `
  const visible = {};
  const texts = {};
  for (const [name, selector] of Object.entries(groups || {})) {
    let found = null;
    try {
      for (const el of document.querySelectorAll(selector)) {
        if (agentifiDrawn(el)) { found = el; break; }
      }
    } catch (err) {
      // A selector this browser will not parse is a group with no match,
      // never a reading that throws.
    }
    visible[name] = Boolean(found);
    texts[name] = found ? clean(found.innerText) : '';
  }
  return {
    url: location.href,
    title: document.title,
    text: clean(document.body ? document.body.innerText : '').slice(0, 4000),
    visible,
    texts,
  };
}`

func readPage(page browser.Page, groups map[string]string) (reading, error) {
	var out reading
	if err := browser.EvaluateInto(page, readPageScript, groups, &out); err != nil {
		return reading{}, err
	}
	out.complete(page)
	return out, nil
}

// complete fills what a reading left out, so a caller never asks a nil map.
func (r *reading) complete(page browser.Page) {
	if r.Visible == nil {
		r.Visible = map[string]bool{}
	}
	if r.Texts == nil {
		r.Texts = map[string]string{}
	}
	if r.URL == "" {
		r.URL = page.URL()
	}
}

func clean(value string) string { return strings.Join(strings.Fields(value), " ") }

func isoDay(at time.Time) string { return at.Format("2006-01-02") }

// parseDateText answers "" for text that is not a date, and every caller skips
// such a line: a purchase filed under the wrong day is worse than none.
// Amazon writes "September 5, 2026" on an order card and "Sep 5, 2026" on a
// returns card, which billers.DayIn reads; Costco answers ISO stamps, and its
// own pages print M/D/YYYY or M/D/YY.
func parseDateText(text string) string {
	text = clean(text)
	if text == "" {
		return ""
	}
	if iso := isoPrefix.FindString(text); iso != "" {
		return iso
	}
	if day := billers.DayIn(text); day != "" {
		return day
	}
	if at, err := time.Parse("1/2/06", text); err == nil {
		return isoDay(at)
	}
	return ""
}

var isoPrefix = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

// loose is a figure the site may write as a number, a string, or a string
// with a currency symbol, field by field.
type loose string

// UnmarshalJSON keeps a JSON number exactly as written rather than passing it
// through a float.
func (l *loose) UnmarshalJSON(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" {
		*l = ""
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		*l = loose(text)
		return nil
	}
	*l = loose(trimmed)
	return nil
}

// Money is false when the field carried none. Absent is not zero: a receipt
// with no tax figure has an unknown tax, and "0.00" is a claim.
func (l loose) Money() (domain.Money, bool) {
	amount, err := domain.MoneyFromJSONValue(string(l))
	return amount, err == nil
}

func (l loose) Int() (int, bool) {
	value, ok := domain.ParseMoneyText(string(l))
	if !ok {
		return 0, false
	}
	whole := value.Round()
	return int(whole.Decimal().IntPart()), true
}

func (l loose) String() string { return clean(string(l)) }

func moneyString(value string) string {
	amount, ok := domain.ParseMoneyText(value)
	if !ok {
		return ""
	}
	return amount.String()
}

// share is the unit price behind a line's amount. A missing quantity is one,
// which is what these pages mean by leaving it out.
func share(total domain.Money, quantity int) domain.Money {
	if quantity < 1 {
		quantity = 1
	}
	each, ok := total.DivInt(quantity)
	if !ok {
		return total
	}
	return each
}

// decodeInto re-shapes a page's answer into a struct, keeping numbers as
// written so no receipt total passes through a float.
func decodeInto(value any, out any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := httpx.DecodeJSON(encoded, out); err != nil {
		return fmt.Errorf("merchants: the site answered something unreadable: %w", err)
	}
	return nil
}

// --- The steps every sign-in form takes ------------------------------------------

func imageOf(page browser.Page, selector string) (string, error) {
	shot, err := page.ScreenshotOf(selector)
	if err != nil || len(shot) == 0 {
		return "", nil
	}
	return base64.StdEncoding.EncodeToString(shot), nil
}

// readFile is nil when the file holds nothing, which is how FromCostco and
// FromAmazon refuse one.
func readFile(parsed merchantimport.Parsed, err error) *merchantimport.Parsed {
	if err != nil {
		return nil
	}
	return &parsed
}
