// Package minorunits is the SQLite storage counterpart of internal/pgconv.
// It converts decimal currency amounts to exact integer minor units using a
// committed ISO 4217 table, independent of locale or cash-rounding metadata.
package minorunits

import (
	"errors"
	"math/big"
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
	coefficient := amount.Coefficient()
	if coefficient.Sign() == 0 {
		return m, nil
	}
	exponent := int64(amount.Exponent()) + int64(scale)
	if exponent > 18 {
		return Amount{}, ErrOverflow
	}
	if exponent < 0 {
		// A larger divisor cannot divide a nonzero coefficient exactly. This
		// also bounds allocation for inputs with extreme decimal exponents.
		if -exponent > int64(coefficient.BitLen()) {
			return Amount{}, ErrRounding
		}
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(-exponent), nil)
		var remainder big.Int
		coefficient.QuoRem(coefficient, divisor, &remainder)
		if remainder.Sign() != 0 {
			return Amount{}, ErrRounding
		}
	} else if exponent > 0 {
		factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(exponent), nil)
		coefficient.Mul(coefficient, factor)
	}
	if !coefficient.IsInt64() {
		return Amount{}, ErrOverflow
	}
	m.Amount = coefficient.Int64()
	return m, nil
}

// Decimal returns the exact major-unit amount, rejecting an invalid currency.
func (m Amount) Decimal() (decimal.Decimal, error) {
	scale, err := Decimals(m.Currency)
	if err != nil {
		return decimal.Decimal{}, err
	}
	return decimal.New(m.Amount, -int32(scale)), nil
}
