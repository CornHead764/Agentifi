package minorunits

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/shopspring/decimal"
)

var (
	ErrRounding = errors.New("minorunits: amount requires rounding")
	ErrOverflow = errors.New("minorunits: amount overflows int64 minor units")
)

func Decimals(code string) (int, error) {
	n, ok := iso4217MinorUnits[strings.ToUpper(code)]
	if !ok {
		return 0, fmt.Errorf("minorunits: unknown ISO 4217 currency %q", code)
	}
	if n < 0 {
		return 0, fmt.Errorf("minorunits: ISO 4217 currency %q has no minor unit", code)
	}
	return n, nil
}

func Minor(text string, scale int) (int64, error) {
	d, err := decimal.NewFromString(text)
	if err != nil {
		return 0, err
	}
	return scaledMinor(d, scale)
}

func scaledMinor(d decimal.Decimal, scale int) (int64, error) {
	coefficient := d.Coefficient()
	if coefficient.Sign() == 0 {
		return 0, nil
	}
	exponent := int64(d.Exponent()) + int64(scale)
	if exponent > 18 {
		return 0, ErrOverflow
	}
	if exponent < 0 {
		// Reject impossible divisors before materializing 10^exponent.
		if exponent < -int64(coefficient.BitLen()) {
			return 0, ErrRounding
		}
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(-exponent), nil)
		var remainder big.Int
		coefficient.QuoRem(coefficient, divisor, &remainder)
		if remainder.Sign() != 0 {
			return 0, ErrRounding
		}
	} else if exponent > 0 {
		factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(exponent), nil)
		coefficient.Mul(coefficient, factor)
	}
	if !coefficient.IsInt64() {
		return 0, ErrOverflow
	}
	return coefficient.Int64(), nil
}

func ToMinor(amount decimal.Decimal, code string) (int64, error) {
	scale, err := Decimals(code)
	if err != nil {
		return 0, err
	}
	return scaledMinor(amount, scale)
}

func FromMinor(amount int64, code string) (decimal.Decimal, error) {
	scale, err := Decimals(code)
	if err != nil {
		return decimal.Zero, err
	}
	return decimal.New(amount, -int32(scale)), nil
}
