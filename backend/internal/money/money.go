// Package money converts currency amounts to and from exact integer minor units.
// Currency scales follow ISO 4217 standard rounding metadata, not cash rounding.
package money

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"
	"golang.org/x/text/currency"
)

var (
	ErrRounding = errors.New("money: amount requires rounding")
	ErrOverflow = errors.New("money: amount overflows int64 minor units")
)

// Money stores an amount in minor units and its ISO 4217 currency code.
// The zero value has no currency; use New or FromDecimal before conversion.
type Money struct {
	Amount   int64
	Currency string
}

// New validates the currency and canonicalizes its code to uppercase.
func New(amount int64, code string) (Money, error) {
	unit, err := currency.ParseISO(code)
	if err != nil {
		return Money{}, fmt.Errorf("money: invalid currency %q: %w", code, err)
	}
	return Money{Amount: amount, Currency: unit.String()}, nil
}

// FromDecimal accepts only exact, representable amounts. Trailing decimal zeros
// do not require rounding. No float conversion or implicit rounding is used.
func FromDecimal(amount decimal.Decimal, code string) (Money, error) {
	unit, err := currency.ParseISO(code)
	if err != nil {
		return Money{}, fmt.Errorf("money: invalid currency %q: %w", code, err)
	}
	m := Money{Currency: unit.String()}
	scale, _ := currency.Standard.Rounding(unit)
	coefficient := amount.Coefficient()
	if coefficient.Sign() == 0 {
		return m, nil
	}
	exponent := int64(amount.Exponent()) + int64(scale)
	if exponent > 18 {
		return Money{}, ErrOverflow
	}
	if exponent < 0 {
		// A larger divisor cannot divide a nonzero coefficient exactly. This
		// also bounds allocation for inputs with extreme decimal exponents.
		if -exponent > int64(coefficient.BitLen()) {
			return Money{}, ErrRounding
		}
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(-exponent), nil)
		var remainder big.Int
		coefficient.QuoRem(coefficient, divisor, &remainder)
		if remainder.Sign() != 0 {
			return Money{}, ErrRounding
		}
	} else if exponent > 0 {
		factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(exponent), nil)
		coefficient.Mul(coefficient, factor)
	}
	if !coefficient.IsInt64() {
		return Money{}, ErrOverflow
	}
	m.Amount = coefficient.Int64()
	return m, nil
}

// Decimal returns the exact major-unit amount, rejecting an invalid currency.
func (m Money) Decimal() (decimal.Decimal, error) {
	unit, err := currency.ParseISO(m.Currency)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("money: invalid currency %q: %w", m.Currency, err)
	}
	scale, _ := currency.Standard.Rounding(unit)
	return decimal.New(m.Amount, -int32(scale)), nil
}
