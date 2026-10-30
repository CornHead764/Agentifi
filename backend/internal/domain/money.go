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
//
// An amount read from storage carries its currency's ISO 4217 scale (3 for
// KWD, 0 for JPY), and Round and String quantize at it. An amount parsed from
// text or JSON has no scale until it meets one: Add, Sub and the comparisons
// give the result the scale of whichever side has one, and refuse two
// different scales with a *MixedScaleError panic. An amount with no scale
// rounds and prints at two places.
type Money struct {
	d decimal.Decimal
	// places is the scale plus one, so the zero value has none.
	places int8
}

// defaultPlaces is where an amount with no scale rounds and prints.
const defaultPlaces = 2

// maxPlaces bounds a scale: int64 minor units hold nothing finer.
const maxPlaces = 18

// MixedScaleError is arithmetic or a comparison between amounts at two
// different scales, which are two different currencies. The caller converts
// one first (Money.Convert); the digits are never coerced.
type MixedScaleError struct {
	Left, Right int
}

func (e *MixedScaleError) Error() string {
	return fmt.Sprintf("money: cannot combine an amount at %d places with one at %d; convert it first",
		e.Left, e.Right)
}

// joinPlaces is the scale of a result combining a and b.
func joinPlaces(a, b Money) int8 {
	switch {
	case a.places == 0 || a.places == b.places:
		return b.places
	case b.places == 0:
		return a.places
	}
	panic(&MixedScaleError{Left: int(a.places) - 1, Right: int(b.places) - 1})
}

func placesTag(places int) int8 {
	if places < 0 || places > maxPlaces {
		panic(fmt.Sprintf("money: scale %d is outside 0..%d", places, maxPlaces))
	}
	return int8(places + 1)
}

// FromMinor builds an amount from an integer count of minor units at a
// currency's scale: 1234 at 3 places is 1.234.
func FromMinor(units int64, places int) Money {
	return Money{decimal.New(units, -int32(places)), placesTag(places)}
}

// SameScale is whether m and other can be combined: either has no scale yet,
// or both have the same one.
func (m Money) SameScale(other Money) bool {
	return m.places == 0 || other.places == 0 || m.places == other.places
}

// Places is the amount's scale, false when it has none yet.
func (m Money) Places() (int, bool) {
	return int(m.places) - 1, m.places != 0
}

// roundingPlaces is where Round and String quantize.
func (m Money) roundingPlaces() int32 {
	if m.places == 0 {
		return defaultPlaces
	}
	return int32(m.places) - 1
}

// AtPlaces gives an amount a currency's scale. It refuses, rather than
// rounds, an amount with more digits than the scale holds, and an amount that
// already has a different scale.
func (m Money) AtPlaces(places int) (Money, error) {
	tag := placesTag(places)
	if m.places != 0 && m.places != tag {
		return Zero, &MixedScaleError{Left: int(m.places) - 1, Right: places}
	}
	if !m.d.Equal(m.d.Truncate(int32(places))) {
		return Zero, fmt.Errorf("money: %s has more than %d decimal places", m.d.String(), places)
	}
	return Money{m.d, tag}, nil
}

// Convert restates the amount in another currency: times rate, carrying that
// currency's scale. It is how an amount crosses from one currency's figures
// into another's. Not rounded, like Scale.
func (m Money) Convert(rate Rate, places int) Money {
	return Money{m.d.Mul(rate), placesTag(places)}
}

// FaceValue is Convert at a rate of one: an amount with no exchange rate
// counted as if it were already in the other currency. Only where the total
// is reported as approximate.
func (m Money) FaceValue(places int) Money {
	return Money{m.d, placesTag(places)}
}

// Rate is a conversion factor, a price, a share count or a percentage,
// usually numeric(20,10) in the schema. Not Money: never rounded to the cent,
// since quantizing on ingest destroys the only copy of the figure.
type Rate = decimal.Decimal

var Zero = Money{}

// FromString parses a serialized amount, which has no scale until it meets one. There is deliberately no
// FromFloat: an escape hatch would be used.
func FromString(s string) (Money, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return Zero, fmt.Errorf("money: cannot parse %q: %w", s, err)
	}
	return Money{d: d}, nil
}

// MustFromString is FromString for hand-written literals; it panics.
func MustFromString(s string) Money {
	m, err := FromString(s)
	if err != nil {
		panic(err)
	}
	return m
}

// FromDecimal wraps a decimal already known to be an amount, with no scale.
func FromDecimal(d decimal.Decimal) Money { return Money{d: d} }

// Decimal exposes the underlying value for arithmetic this type does not wrap.
func (m Money) Decimal() decimal.Decimal { return m.d }

func (m Money) Add(other Money) Money { return Money{m.d.Add(other.d), joinPlaces(m, other)} }
func (m Money) Sub(other Money) Money { return Money{m.d.Sub(other.d), joinPlaces(m, other)} }
func (m Money) Neg() Money            { return Money{m.d.Neg(), m.places} }
func (m Money) Abs() Money            { return Money{m.d.Abs(), m.places} }

// Scale multiplies by a rate or plain factor, staying in the same currency.
// Not rounded: rounding per row drifts across an aggregate, so it belongs at
// the end of the chain. A conversion into another currency is Convert.
func (m Money) Scale(factor Rate) Money { return Money{m.d.Mul(factor), m.places} }

// MulInt is the amount n times over: a unit price by a quantity.
func (m Money) MulInt(n int) Money {
	return Money{m.d.Mul(decimal.NewFromInt(int64(n))), m.places}
}

// DivInt divides by a whole number (days elapsed, months remaining). Not
// rounded, like Scale.
func (m Money) DivInt(n int) (Money, bool) {
	if n == 0 {
		return Zero, false
	}
	return Money{m.d.Div(decimal.NewFromInt(int64(n))), m.places}, true
}

// DivRate divides by a rate or factor. False when the divisor is zero; the
// caller renders an em dash rather than a substitute number.
func (m Money) DivRate(r Rate) (Money, bool) {
	if r.IsZero() {
		return Zero, false
	}
	return Money{m.d.Div(r), m.places}, true
}

// Round quantizes to the amount's scale, or two places when it has none, half
// away from zero: -0.005 becomes -0.01, and -0.0005 at 3 places -0.001. Call
// it once at the end of a calculation, never on an intermediate.
func (m Money) Round() Money { return Money{m.d.Round(m.roundingPlaces()), m.places} }

// MinorUnit is one unit in the amount's last place, at its scale: 0.01 for
// USD, 0.001 for KWD, 1 for JPY.
func (m Money) MinorUnit() Money {
	return Money{decimal.New(1, -m.roundingPlaces()), m.places}
}

func (m Money) IsZero() bool     { return m.d.IsZero() }
func (m Money) IsNegative() bool { return m.d.IsNegative() }
func (m Money) IsPositive() bool { return m.d.IsPositive() }

// IsExpense reports the sign convention: expenses are stored negative, income
// positive, zero is neither. Display flips signs; storage never does.
func (m Money) IsExpense() bool { return m.d.IsNegative() }
func (m Money) IsIncome() bool  { return m.d.IsPositive() }

func (m Money) Cmp(other Money) int {
	joinPlaces(m, other)
	return m.d.Cmp(other.d)
}
func (m Money) Equal(other Money) bool   { return m.Cmp(other) == 0 }
func (m Money) LessThan(o Money) bool    { return m.Cmp(o) < 0 }
func (m Money) GreaterThan(o Money) bool { return m.Cmp(o) > 0 }

// String is the wire format: a decimal string quantized at the amount's
// scale, "1.234" for KWD and "1234" for JPY.
func (m Money) String() string {
	places := m.roundingPlaces()
	return m.d.Round(places).StringFixed(places)
}

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
	sum := Zero
	for _, a := range amounts {
		sum = sum.Add(a)
	}
	return sum.Round()
}

// Sum is Total over a slice, for the many callers that have one already.
func Sum[T any](items []T, amount func(T) Money) Money {
	sum := Zero
	for _, item := range items {
		sum = sum.Add(amount(item))
	}
	return sum.Round()
}

// Ratio divides two amounts, false when the denominator is zero. The caller
// renders an em dash: "no prior period" is not "no change". The two may be at
// different scales: a converted amount over its native one is the rate.
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
