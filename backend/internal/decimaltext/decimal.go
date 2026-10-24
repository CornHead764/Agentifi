// Package decimaltext defines the SQLite representation of domain.Rate values.
// Canonical text is decimal.Decimal.String(): no exponent or plus sign, no
// leading integer zeros, no trailing fractional zeros, and zero is always "0".
// Arithmetic and ordering must happen in Go, never through SQLite TEXT coercion.
package decimaltext

import (
	"fmt"
	"github.com/shopspring/decimal"
)

func Format(d decimal.Decimal) string { return d.String() }

func Parse(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if Format(d) != s {
		return decimal.Decimal{}, fmt.Errorf("noncanonical decimal %q", s)
	}
	return d, nil
}

// Check returns a pure SQLite CHECK predicate, without numeric coercion or UDFs.
// column must be a trusted SQL identifier, not user input.
func Check(column string) string {
	u := "ltrim(" + column + ", '-')"
	return "(" + column + " = '0' OR (" + column + " <> '-0' AND " + column + " NOT GLOB '*[^0-9.-]*' AND instr(substr(" + column + ", 2), '-') = 0 AND (" + u + " GLOB '[1-9]*' OR " + u + " GLOB '0.[0-9]*') AND " + u + " NOT GLOB '*.*.*' AND " + u + " NOT GLOB '*.' AND (instr(" + u + ", '.') = 0 OR " + u + " NOT GLOB '*0')))"
}
