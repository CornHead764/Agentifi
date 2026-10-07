// Package domain is the pure calculation core: plain structs in, Money or
// Decimal out, no I/O. It may import only the standard library,
// shopspring/decimal and golang.org/x/text (purity_test.go enforces this), so
// a wrong number is findable by a unit test.
package domain

import (
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"
)

// Money is an amount in some currency. A distinct type, not an alias for
// decimal.Decimal, so a rate or share count cannot be added to a balance. The
// zero value is a usable $0.00.
type Money struct {
	d decimal.Decimal
}

// Rate is a conversion factor, a price, a share count or a percentage,
// usually numeric(20,10) in the schema. Not Money: never rounded to the cent,
// since quantizing on ingest destroys the only copy of the figure.
type Rate = decimal.Decimal

var Zero = Money{}

// FromString parses a stored or serialized amount. There is deliberately no
// FromFloat: an escape hatch would be used.
func FromString(s string) (Money, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return Zero, fmt.Errorf("money: cannot parse %q: %w", s, err)
	}
	return Money{d}, nil
}

// MustFromString is FromString for hand-written literals; it panics.
func MustFromString(s string) Money {
	m, err := FromString(s)
	if err != nil {
		panic(err)
	}
	return m
}

// FromCents builds an amount from an integer count of minor units.
func FromCents(cents int64) Money {
	return Money{decimal.New(cents, -2)}
}

// FromDecimal wraps a decimal already known to be an amount.
func FromDecimal(d decimal.Decimal) Money { return Money{d} }

// Decimal exposes the underlying value for arithmetic this type does not wrap.
func (m Money) Decimal() decimal.Decimal { return m.d }

func (m Money) Add(other Money) Money { return Money{m.d.Add(other.d)} }
func (m Money) Sub(other Money) Money { return Money{m.d.Sub(other.d)} }
func (m Money) Neg() Money            { return Money{m.d.Neg()} }
func (m Money) Abs() Money            { return Money{m.d.Abs()} }

// Scale multiplies by a rate or plain factor. Not rounded: rounding per row
// drifts across an aggregate, so it belongs at the end of the chain.
func (m Money) Scale(factor Rate) Money { return Money{m.d.Mul(factor)} }

// MulInt is the amount n times over: a unit price by a quantity.
func (m Money) MulInt(n int) Money { return Money{m.d.Mul(decimal.NewFromInt(int64(n)))} }

// DivInt divides by a whole number (days elapsed, months remaining). Not
// rounded, like Scale.
func (m Money) DivInt(n int) (Money, bool) {
	if n == 0 {
		return Zero, false
	}
	return Money{m.d.Div(decimal.NewFromInt(int64(n)))}, true
}

// DivRate divides by a rate or factor. False when the divisor is zero; the
// caller renders an em dash rather than a substitute number.
func (m Money) DivRate(r Rate) (Money, bool) {
	if r.IsZero() {
		return Zero, false
	}
	return Money{m.d.Div(r)}, true
}

// Round quantizes to two places, half away from zero (-0.005 becomes -0.01).
// Call it once at the end of a calculation, never on an intermediate.
func (m Money) Round() Money { return Money{m.d.Round(2)} }

func (m Money) IsZero() bool     { return m.d.IsZero() }
func (m Money) IsNegative() bool { return m.d.IsNegative() }
func (m Money) IsPositive() bool { return m.d.IsPositive() }

// IsExpense reports the sign convention: expenses are stored negative, income
// positive, zero is neither. Display flips signs; storage never does.
func (m Money) IsExpense() bool { return m.d.IsNegative() }
func (m Money) IsIncome() bool  { return m.d.IsPositive() }

func (m Money) Cmp(other Money) int      { return m.d.Cmp(other.d) }
func (m Money) Equal(other Money) bool   { return m.d.Equal(other.d) }
func (m Money) LessThan(o Money) bool    { return m.d.LessThan(o.d) }
func (m Money) GreaterThan(o Money) bool { return m.d.GreaterThan(o.d) }

// String is the wire and storage format: a quantized decimal string.
func (m Money) String() string { return m.Round().d.StringFixed(2) }

// MarshalJSON emits a JSON string, never a number, so no consumer ever sees a
// float64. The client coerces once, at its API boundary.
func (m Money) MarshalJSON() ([]byte, error) { return json.Marshal(m.String()) }

// UnmarshalJSON accepts a string and refuses a number: a JSON number has
// already been through a float. A refusal is an *AmountJSONError.
func (m *Money) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return &AmountJSONError{Raw: append([]byte(nil), data...), NotString: true}
	}
	parsed, err := FromString(s)
	if err != nil {
		return &AmountJSONError{Raw: append([]byte(nil), data...), Text: s}
	}
	*m = parsed
	return nil
}

// AmountJSONError is a JSON value Money refused. encoding/json reports it
// without the field it was under, so Raw, the value as sent, is what a caller
// has to find that field by.
type AmountJSONError struct {
	Raw []byte
	// NotString is a value that was not a JSON string; Text is the string
	// otherwise.
	NotString bool
	Text      string
}

func (e *AmountJSONError) Error() string {
	return "money: " + e.Reason()
}

// Reason is the message without the package prefix.
func (e *AmountJSONError) Reason() string {
	if e.NotString {
		return fmt.Sprintf("expected a JSON string such as \"12.34\", got %s", e.Raw)
	}
	return NotAnAmount(e.Text)
}

// NotAnAmount is the one wording for text that does not parse as money.
func NotAnAmount(text string) string {
	return fmt.Sprintf("%q is not an amount such as \"1250.00\"", text)
}

// Total sums amounts and rounds exactly once, at the end: three thirds of a
// cent round to nothing individually and to a cent together.
func Total(amounts ...Money) Money {
	sum := decimal.Zero
	for _, a := range amounts {
		sum = sum.Add(a.d)
	}
	return Money{sum}.Round()
}

// Sum is Total over a slice, for the many callers that have one already.
func Sum[T any](items []T, amount func(T) Money) Money {
	sum := decimal.Zero
	for _, item := range items {
		sum = sum.Add(amount(item).d)
	}
	return Money{sum}.Round()
}

// Ratio divides two amounts, false when the denominator is zero. The caller
// renders an em dash: "no prior period" is not "no change".
func Ratio(numerator, denominator Money) (Rate, bool) {
	if denominator.IsZero() {
		return decimal.Zero, false
	}
	return numerator.d.Div(denominator.d), true
}

// Percent is Ratio as a percentage, rounded to two places: 4.21 for 4.21%.
// Both serialize as a quoted decimal, so the unit lives in the field name: a
// Percent goes behind `*_pct`, a Ratio behind a proportion name like `share`
// or `savings_rate`. A client that read a `_pct` as a fraction shows 421.00%.
func Percent(numerator, denominator Money) (Rate, bool) {
	r, ok := Ratio(numerator, denominator)
	if !ok {
		return decimal.Zero, false
	}
	return r.Mul(decimal.NewFromInt(100)).Round(2), true
}
