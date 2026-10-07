package billmail

import (
	"regexp"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Month-first throughout: every provider here is American, and a European
// reading of 03/04 would silently move a due date a month.
var dateShapes = []struct {
	shape   *regexp.Regexp
	layouts []string
}{
	{regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`), []string{"2006-01-02"}},
	{regexp.MustCompile(`\b\d{1,2}/\d{1,2}/\d{4}\b`), []string{"1/2/2006"}},
	{regexp.MustCompile(`\b\d{1,2}-\d{1,2}-\d{4}\b`), []string{"1-2-2006"}},
	{
		regexp.MustCompile(`(?i)\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+\d{1,2}(?:st|nd|rd|th)?,?\s+\d{4}\b`),
		[]string{"January 2, 2006", "Jan 2, 2006"},
	},
}

var ordinal = regexp.MustCompile(`(?i)(\d{1,2})(?:st|nd|rd|th)\b`)

func ParseDate(s string) (domain.Date, bool) {
	at, best := -1, ""
	var layouts []string
	for _, shape := range dateShapes {
		where := shape.shape.FindStringIndex(s)
		if where == nil || (at >= 0 && where[0] >= at) {
			continue
		}
		at, best, layouts = where[0], s[where[0]:where[1]], shape.layouts
	}
	if at < 0 {
		return domain.Date{}, false
	}
	return ParseDay(best, layouts)
}

// ParseDay reads s whole, against each layout in turn, stripping a period
// after a month name and an ordinal suffix on a day number first ("Sept." and
// "3rd" both read, since mail and billing pages write both). A month a
// layout does not spell, such as "Sept", falls back to its three-letter
// abbreviation.
func ParseDay(s string, layouts []string) (domain.Date, bool) {
	s = ordinal.ReplaceAllString(strings.ReplaceAll(s, ".", ""), "$1")
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return domain.DateOf(t), true
		}
	}
	// A month name no layout knows — "Sept" — and a day the mail wrote without
	// its comma, both reduced to the abbreviated form.
	if fields := strings.Fields(strings.ReplaceAll(s, ",", " ")); len(fields) == 3 {
		month := fields[0]
		if len(month) > 3 {
			month = month[:3]
		}
		if t, err := time.Parse("Jan 2 2006", month+" "+fields[1]+" "+fields[2]); err == nil {
			return domain.DateOf(t), true
		}
	}
	return domain.Date{}, false
}
