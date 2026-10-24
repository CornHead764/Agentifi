package minorunits

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
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
	shifted := d.Shift(int32(scale))
	if !shifted.Equal(shifted.Truncate(0)) {
		return 0, fmt.Errorf("value %s would round at scale %d", d, scale)
	}
	if !shifted.BigInt().IsInt64() {
		return 0, fmt.Errorf("value %s overflows int64", d)
	}
	return shifted.IntPart(), nil
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
