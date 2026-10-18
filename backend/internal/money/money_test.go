package money

import (
	"errors"
	"math"
	"testing"

	"github.com/shopspring/decimal"
)

func TestCurrencyMinorUnits(t *testing.T) {
	for _, tc := range []struct {
		code   string
		value  string
		amount int64
	}{
		{"JPY", "123", 123},
		{"JPY", "-123.000", -123},
		{"KWD", "123.456", 123456},
		{"KWD", "-0.001", -1},
		{"USD", "123.45", 12345},
		{"USD", "-0.01", -1},
		{"USD", "1.23000", 123},
		{"USD", "1e2", 10000},
		{"CHF", "1.01", 101},
		{"jpy", "0", 0},
	} {
		t.Run(tc.code+"/"+tc.value, func(t *testing.T) {
			d := decimal.RequireFromString(tc.value)
			got, err := FromDecimal(d, tc.code)
			if err != nil {
				t.Fatal(err)
			}
			if got.Amount != tc.amount {
				t.Fatalf("amount = %d, want %d", got.Amount, tc.amount)
			}
			back, err := got.Decimal()
			if err != nil || !back.Equal(d) {
				t.Fatalf("round trip = %v, %v; want %s", back, err, tc.value)
			}
		})
	}
}

func TestInt64Boundaries(t *testing.T) {
	for _, tc := range []struct {
		code string
		max  string
		min  string
	}{
		{"JPY", "9223372036854775807", "-9223372036854775808"},
		{"USD", "92233720368547758.07", "-92233720368547758.08"},
		{"KWD", "9223372036854775.807", "-9223372036854775.808"},
	} {
		for _, bound := range []struct {
			value  string
			amount int64
		}{
			{tc.max, math.MaxInt64},
			{tc.min, math.MinInt64},
		} {
			t.Run(tc.code+"/"+bound.value, func(t *testing.T) {
				got, err := FromDecimal(decimal.RequireFromString(bound.value), tc.code)
				if err != nil || got.Amount != bound.amount {
					t.Fatalf("conversion = %+v, %v; want %d", got, err, bound.amount)
				}
				back, err := got.Decimal()
				if err != nil || back.String() != bound.value {
					t.Fatalf("decimal = %v, %v; want %s", back, err, bound.value)
				}
			})
		}
	}
}

func TestRefusesRoundingAndOverflow(t *testing.T) {
	for _, tc := range []struct {
		code  string
		value string
		err   error
	}{
		{"JPY", "0.1", ErrRounding},
		{"JPY", "-0.1", ErrRounding},
		{"USD", "1.001", ErrRounding},
		{"USD", "-1.005", ErrRounding},
		{"KWD", "1.0001", ErrRounding},
		{"KWD", "-1.0005", ErrRounding},
		{"JPY", "9223372036854775808", ErrOverflow},
		{"JPY", "-9223372036854775809", ErrOverflow},
		{"USD", "92233720368547758.08", ErrOverflow},
		{"USD", "-92233720368547758.09", ErrOverflow},
		{"KWD", "9223372036854775.808", ErrOverflow},
		{"KWD", "-9223372036854775.809", ErrOverflow},
		{"USD", "1e100", ErrOverflow},
		{"USD", "-1e100", ErrOverflow},
	} {
		t.Run(tc.code+"/"+tc.value, func(t *testing.T) {
			got, err := FromDecimal(decimal.RequireFromString(tc.value), tc.code)
			if !errors.Is(err, tc.err) || got != (Money{}) {
				t.Fatalf("conversion = %+v, %v; want zero and %v", got, err, tc.err)
			}
		})
	}
}

func TestExtremeExponents(t *testing.T) {
	for _, tc := range []struct {
		coefficient int64
		exponent    int32
		err         error
	}{
		{1, math.MaxInt32, ErrOverflow},
		{-1, math.MaxInt32, ErrOverflow},
		{1, math.MinInt32, ErrRounding},
		{-1, math.MinInt32, ErrRounding},
		{0, math.MaxInt32, nil},
		{0, math.MinInt32, nil},
	} {
		got, err := FromDecimal(decimal.New(tc.coefficient, tc.exponent), "KWD")
		if !errors.Is(err, tc.err) {
			t.Errorf("coefficient %d exponent %d: %v, want %v", tc.coefficient, tc.exponent, err, tc.err)
		}
		if tc.err == nil && got != (Money{Currency: "KWD"}) {
			t.Errorf("zero conversion = %+v", got)
		}
	}
}

func TestCurrencyValidation(t *testing.T) {
	got, err := New(-123, "usd")
	if err != nil || got != (Money{Amount: -123, Currency: "USD"}) {
		t.Fatalf("New = %+v, %v", got, err)
	}
	for _, code := range []string{"", "US", "USDD", "ZZZ", "123", " USD"} {
		if _, err := New(0, code); err == nil {
			t.Errorf("New accepted currency %q", code)
		}
		if _, err := FromDecimal(decimal.Zero, code); err == nil {
			t.Errorf("FromDecimal accepted currency %q", code)
		}
		if _, err := (Money{Currency: code}).Decimal(); err == nil {
			t.Errorf("Decimal accepted currency %q", code)
		}
	}
}
