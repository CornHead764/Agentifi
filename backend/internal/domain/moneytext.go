package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// moneyTextShape is an amount once spaces are gone: at most one sign, before
// or after the currency sign, then digits grouped by threes or not grouped at
// all, then any number of decimals.
var moneyTextShape = regexp.MustCompile(
	`^([-+])?\$?([-+])?((?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?|\.\d+)$`)

// ParseMoneyText reads an amount as a page, a statement, a mail or a file
// writes one: "$1,234.56", "-$12.34", "$-12.34", "+5.00", "(12.34)", "−12.34"
// (a Unicode minus), "12.34 USD". False is anything else, including "" — which
// is absent, never zero.
//
// Only a leading sign or enclosing parentheses make a figure negative. A
// hyphen anywhere else ("12-34", "$10 - $20") is not a sign, and a figure
// carrying one is refused rather than read with the hyphen dropped. Grouping
// commas must group by threes, so "12,34" is refused rather than read as 1234.
// Decimals are kept as written: rounding is Money's, at the end.
func ParseMoneyText(s string) (Money, bool) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		if r == '−' {
			return '-'
		}
		return r
	}, s)
	s = strings.TrimPrefix(strings.TrimSuffix(s, "USD"), "USD")
	negative := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		negative = true
		s = s[1 : len(s)-1]
	}
	m := moneyTextShape.FindStringSubmatch(s)
	if m == nil {
		return Zero, false
	}
	sign := m[1] + m[2]
	if len(sign) > 1 || (negative && sign != "") {
		return Zero, false
	}
	amount, err := FromString(strings.ReplaceAll(m[3], ",", ""))
	if err != nil {
		return Zero, false
	}
	if negative || sign == "-" {
		amount = amount.Neg()
	}
	return amount, true
}

// ErrNoAmount is an absent amount: JSON null or an empty string, never zero.
var ErrNoAmount = errors.New("no amount")

// MoneyFromJSONValue reads an amount from a value decoded with
// json.Decoder.UseNumber. A JSON number is read from its literal digits; a
// string is read as ParseMoneyText reads it. A float64 is refused, because its
// digits are already gone, and so is anything else that is not an amount.
func MoneyFromJSONValue(value any) (Money, error) {
	switch typed := value.(type) {
	case nil:
		return Zero, ErrNoAmount
	case json.Number:
		amount, err := FromString(typed.String())
		if err != nil {
			return Zero, fmt.Errorf("not an amount: %s", typed)
		}
		return amount, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return Zero, ErrNoAmount
		}
		amount, ok := ParseMoneyText(typed)
		if !ok {
			return Zero, fmt.Errorf("not an amount: %q", typed)
		}
		return amount, nil
	case bool:
		return Zero, fmt.Errorf("expected an amount, found a boolean: %v", typed)
	case float64:
		return Zero, errors.New("arrived as a float64, which has already lost digits; " +
			"decode with json.Decoder.UseNumber")
	default:
		return Zero, fmt.Errorf("not an amount: a %T", value)
	}
}
