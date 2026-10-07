package domain

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseMoneyTextReadsEveryWayAnAmountIsWritten(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"123.45", "123.45"},
		{"$1,234.50", "1234.50"},
		{"  $1,234.50 ", "1234.50"},
		{"-12.34", "-12.34"},
		{"−12.34", "-12.34"},
		{"-$12.34", "-12.34"},
		{"$-12.34", "-12.34"},
		{"+$5.00", "5.00"},
		{"(12.34)", "-12.34"},
		{"($1,234.56)", "-1234.56"},
		{"12.34 USD", "12.34"},
		{"USD 12.34", "12.34"},
		{"87", "87.00"},
		{".50", "0.50"},
		{"1 234.00", "1234.00"},
		{"1,000,000.00", "1000000.00"},
	} {
		got, ok := ParseMoneyText(tc.in)
		if !ok {
			t.Errorf("ParseMoneyText(%q) refused; want %s", tc.in, tc.want)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("ParseMoneyText(%q) = %s; want %s", tc.in, got, tc.want)
		}
	}
}

func TestParseMoneyTextKeepsMillsForMoneyToRound(t *testing.T) {
	got, ok := ParseMoneyText("123.455")
	if !ok || got.Decimal().String() != "123.455" {
		t.Fatalf("got %v %v; the figure must arrive unrounded", got.Decimal(), ok)
	}
	if got.String() != "123.46" {
		t.Fatalf("rounds to %s; want half away from zero, 123.46", got)
	}
}

func TestParseMoneyTextRefusesWhatIsNotOneAmount(t *testing.T) {
	for _, in := range []string{
		"", "  ", "$", "abc", "12-34", "$10 - $20", "12,34", "1,2345.00",
		"--12", "+-12", "(-12.34)", "12.34-", "1.2.3", "12.34 EUR",
	} {
		if got, ok := ParseMoneyText(in); ok {
			t.Errorf("ParseMoneyText(%q) = %s; want refused", in, got)
		}
	}
}

func TestMoneyFromJSONValueReadsANumberFromItsDigits(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{json.Number("0.30000000000000004"), "0.30000000000000004"},
		{json.Number("15000"), "15000"},
		{json.Number("-25.5"), "-25.5"},
		{"$1,234.56", "1234.56"},
		{"(12.34)", "-12.34"},
	} {
		got, err := MoneyFromJSONValue(tc.in)
		if err != nil || got.Decimal().String() != tc.want {
			t.Errorf("MoneyFromJSONValue(%#v) = %s, %v; want %s", tc.in, got.Decimal(), err, tc.want)
		}
	}
}

func TestMoneyFromJSONValueRefusesWhatIsNotAnAmount(t *testing.T) {
	for _, in := range []any{"12,34", "about twenty dollars", true, 25.5, []any{}} {
		if got, err := MoneyFromJSONValue(in); err == nil || errors.Is(err, ErrNoAmount) {
			t.Errorf("MoneyFromJSONValue(%#v) = %s, %v; want refused", in, got, err)
		}
	}
	for _, in := range []any{nil, "", "  "} {
		if _, err := MoneyFromJSONValue(in); !errors.Is(err, ErrNoAmount) {
			t.Errorf("MoneyFromJSONValue(%#v) = %v; want ErrNoAmount", in, err)
		}
	}
}
