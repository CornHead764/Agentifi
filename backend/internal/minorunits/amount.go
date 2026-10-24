// Package minorunits is the SQLite storage counterpart of internal/pgconv.
// It converts decimal currency amounts to exact integer minor units using a
// committed ISO 4217 table, independent of locale or cash-rounding metadata.
package minorunits

import (
	"errors"
	"strings"

	"github.com/shopspring/decimal"
)

var (
	ErrRounding = errors.New("minorunits: amount requires rounding")
	ErrOverflow = errors.New("minorunits: amount overflows int64 minor units")
)

// Amount stores a SQLite amount in minor units and its ISO 4217 currency code.
// The zero value has no currency; use New or FromDecimal before conversion.
type Amount struct {
	Amount   int64
	Currency string
}

// New validates the currency and canonicalizes its code to uppercase.
func New(amount int64, code string) (Amount, error) {
	if _, err := Decimals(code); err != nil {
		return Amount{}, err
	}
	return Amount{Amount: amount, Currency: strings.ToUpper(code)}, nil
}

// FromDecimal accepts only exact, representable amounts. Trailing decimal zeros
// do not require rounding. No float conversion or implicit rounding is used.
func FromDecimal(amount decimal.Decimal, code string) (Amount, error) {
	scale, err := Decimals(code)
	if err != nil {
		return Amount{}, err
	}
	m := Amount{Currency: strings.ToUpper(code)}
	value, err := scaledMinor(amount, scale)
	if err != nil {
		return Amount{}, err
	}
	m.Amount = value
	return m, err
}

// Decimal returns the exact major-unit amount, rejecting an invalid currency.
func (m Amount) Decimal() (decimal.Decimal, error) {
	scale, err := Decimals(m.Currency)
	if err != nil {
		return decimal.Decimal{}, err
	}
	return decimal.New(m.Amount, -int32(scale)), nil
}
