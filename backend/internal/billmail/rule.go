package billmail

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// A household's mail rule as data: each field is read by a label (what follows
// it, via the parsers' helpers) or a pattern (its first capture group), label
// first. The action half of the rule is the store's.

type Rule struct {
	Name string
	// Sender is a full address, "@domain", or "" for any.
	Sender          string
	SubjectContains string
	BodyContains    string
	AmountLabel     string
	AmountPattern   string
	// DateLabel and DatePattern unset or unreadable mean the received date.
	DateLabel   string
	DatePattern string
	// ReferenceLabel and ReferencePattern read the receipt id, or on a bill
	// rule the account number that picks the billed account.
	ReferenceLabel   string
	ReferencePattern string
	// Issued* and Minimum* are read only by a bill rule.
	IssuedLabel    string
	IssuedPattern  string
	MinimumLabel   string
	MinimumPattern string
	// PayeeLabel reads the rest of the line after a label, overriding Payee.
	Payee      string
	PayeeLabel string
	// NotesLabel and NotesEndLabel bound a stretch of the mail, such as the
	// table of what was bought, kept in the transaction's notes. Both are read
	// as labels only.
	NotesLabel    string
	NotesEndLabel string
}

type Extraction struct {
	Amount domain.Money
	// HasDate false means the caller uses the received date.
	Date      domain.Date
	HasDate   bool
	Reference string
	Payee     string
	// Section is what NotesLabel bounds, "" when the rule names none or the
	// mail does not carry it; it never fails the extraction.
	Section string
}

// Match: a rule that sets no conditions matches everything.
func (r Rule) Match(m Message) bool {
	if r.Sender != "" && !senderIs(m.Sender, r.Sender) {
		return false
	}
	if r.SubjectContains != "" && !textutil.ContainsFold(m.Subject, r.SubjectContains) {
		return false
	}
	if r.BodyContains != "" && !textutil.ContainsFold(bodyText(m), r.BodyContains) {
		return false
	}
	return true
}

// Check refuses a rule with no amount source or a bad pattern, so none is
// stored that would fail on every message.
func (r Rule) Check() error {
	if r.AmountLabel == "" && r.AmountPattern == "" {
		return errors.New("the rule names neither an amount label nor an amount pattern")
	}
	for _, named := range []struct {
		field, pattern string
	}{
		{"amount_pattern", r.AmountPattern},
		{"date_pattern", r.DatePattern},
		{"reference_pattern", r.ReferencePattern},
		{"issued_pattern", r.IssuedPattern},
		{"minimum_pattern", r.MinimumPattern},
	} {
		if named.pattern == "" {
			continue
		}
		if err := CheckPattern(named.pattern); err != nil {
			return fmt.Errorf("%s: %w", named.field, err)
		}
	}
	return nil
}

func CheckPattern(pattern string) error {
	_, err := compilePattern(pattern)
	return err
}

// Extract fails only on the amount, naming the field it looked at.
func (r Rule) Extract(m Message) (Extraction, error) {
	text := bodyText(m)
	var out Extraction

	if err := r.Check(); err != nil {
		return out, err
	}
	raw, err := r.read(text, r.AmountLabel, r.AmountPattern)
	if err != nil {
		return out, err
	}
	amount, ok := ParseAmount(raw)
	if !ok {
		return out, fmt.Errorf("no amount follows %q in this mail",
			firstSet(r.AmountLabel, r.AmountPattern))
	}
	out.Amount = amount

	if r.DateLabel != "" || r.DatePattern != "" {
		raw, err := r.read(text, r.DateLabel, r.DatePattern)
		if err != nil {
			return out, err
		}
		out.Date, out.HasDate = parseRuleDate(raw)
	}

	if r.ReferenceLabel != "" || r.ReferencePattern != "" {
		raw, err := r.read(text, r.ReferenceLabel, r.ReferencePattern)
		if err != nil {
			return out, err
		}
		out.Reference = firstToken(raw)
	}

	out.Payee = r.Payee
	if r.PayeeLabel != "" {
		if named := findAfterLabel(text, r.PayeeLabel); named != "" {
			out.Payee = named
		}
	}
	if r.NotesLabel != "" {
		out.Section = sectionAfterLabel(text, r.NotesLabel, r.NotesEndLabel)
	}
	return out, nil
}

// ExtractBill fails when a named due date is unreadable: a bill's identity is
// its account and due date, and the received date standing in would file a
// second bill for one cycle. A rule naming no due date files on the received
// date.
func (r Rule) ExtractBill(m Message) (Bill, error) {
	read, err := r.Extract(m)
	if err != nil {
		return Bill{}, err
	}
	out := Bill{AmountDue: read.Amount.Abs(), DueOn: domain.DateOf(m.ReceivedAt)}
	if r.DateLabel != "" || r.DatePattern != "" {
		if !read.HasDate {
			return Bill{}, fmt.Errorf("no due date follows %q in this mail",
				firstSet(r.DateLabel, r.DatePattern))
		}
		out.DueOn = read.Date
	}

	text := bodyText(m)
	if r.IssuedLabel != "" || r.IssuedPattern != "" {
		raw, err := r.read(text, r.IssuedLabel, r.IssuedPattern)
		if err != nil {
			return Bill{}, err
		}
		if day, ok := parseRuleDate(raw); ok {
			out.IssuedOn = day
		}
	}
	if r.MinimumLabel != "" || r.MinimumPattern != "" {
		raw, err := r.read(text, r.MinimumLabel, r.MinimumPattern)
		if err != nil {
			return Bill{}, err
		}
		if minimum, ok := ParseAmount(raw); ok {
			out.MinimumDue, out.HasMinimumDue = minimum.Abs(), true
		}
	}
	if r.ReferenceLabel != "" || r.ReferencePattern != "" {
		raw, err := r.read(text, r.ReferenceLabel, r.ReferencePattern)
		if err != nil {
			return Bill{}, err
		}
		out.ExternalID, out.MaskedNumber = accountReference(raw)
	}
	return out, nil
}

// accountReference answers the provider key and the masked last four, from the
// first run of digit-bearing fields. A number the mail masks ("••••1234",
// "XXXX-1234") yields no key: it would never meet the pull's unmasked one.
func accountReference(raw string) (string, string) {
	var parts []string
	for _, field := range strings.Fields(raw) {
		field = strings.Trim(field, ",;:()[]#")
		if !strings.ContainsAny(field, "0123456789•*") {
			if len(parts) > 0 {
				break
			}
			continue
		}
		if !accountish.MatchString(field) || (len(parts) > 0 && spelled(field)) {
			break
		}
		parts = append(parts, field)
	}
	joined := strings.Join(parts, "")
	last := domain.MaskAccount(joined)
	if last == "" {
		return "", ""
	}
	if strings.ContainsAny(joined, "•*") || strings.Contains(joined, "..") || maskedRun.MatchString(joined) {
		return "", last
	}
	return strings.ToUpper(strings.NewReplacer("-", "", ".", "").Replace(joined)), last
}

var (
	accountish = regexp.MustCompile(`^[0-9A-Za-z•*.\-]+$`)
	maskedRun  = regexp.MustCompile(`(?i)xx`)
)

// spelled ignores X, which masks digits.
func spelled(field string) bool {
	for _, c := range field {
		if (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') && c != 'x' && c != 'X' {
			return true
		}
	}
	return false
}

func (r Rule) read(text, label, pattern string) (string, error) {
	if label != "" {
		if found := findAfterLabel(text, label); found != "" {
			return found, nil
		}
	}
	if pattern == "" {
		return "", nil
	}
	shape, err := compilePattern(pattern)
	if err != nil {
		return "", err
	}
	found := shape.FindStringSubmatch(text)
	if found == nil {
		return "", nil
	}
	return found[1], nil
}

// compilePattern requires a capture group, or the rule silently reads "".
func compilePattern(pattern string) (*regexp.Regexp, error) {
	shape, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("%q is not a pattern this build can read: %w", pattern, err)
	}
	if shape.NumSubexp() < 1 {
		return nil, fmt.Errorf("%q has no capture group, so there is nothing to read", pattern)
	}
	return shape, nil
}

var twoDigitYear = regexp.MustCompile(`\b(\d{1,2}[/-]\d{1,2}[/-]\d{2})\b`)

// parseRuleDate adds till receipts' two-digit year, kept out of ParseDate so
// the shared reader's due dates are not put at risk.
func parseRuleDate(s string) (domain.Date, bool) {
	if day, ok := ParseDate(s); ok {
		return day, true
	}
	found := twoDigitYear.FindStringSubmatch(s)
	if found == nil {
		return domain.Date{}, false
	}
	for _, layout := range []string{"1/2/06", "1-2-06"} {
		if t, err := time.Parse(layout, found[1]); err == nil {
			return domain.DateOf(t), true
		}
	}
	return domain.Date{}, false
}

func firstToken(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}

func firstSet(values ...string) string {
	for _, one := range values {
		if one != "" {
			return one
		}
	}
	return ""
}
