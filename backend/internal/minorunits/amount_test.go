package minorunits

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
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
			if !errors.Is(err, tc.err) || got != (Amount{}) {
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
		_, minorErr := ToMinor(decimal.New(tc.coefficient, tc.exponent), "KWD")
		if !errors.Is(minorErr, tc.err) {
			t.Errorf("ToMinor coefficient %d exponent %d: %v, want %v", tc.coefficient, tc.exponent, minorErr, tc.err)
		}
		if tc.err == nil && got != (Amount{Currency: "KWD"}) {
			t.Errorf("zero conversion = %+v", got)
		}
	}
}

func TestMinorRejectsExtremeScientificNotation(t *testing.T) {
	for _, tc := range []struct {
		text string
		err  error
	}{
		{"1e2147483647", ErrOverflow},
		{"-1e2147483647", ErrOverflow},
		{"1e-2147483647", ErrRounding},
		{"-1e-2147483647", ErrRounding},
	} {
		if _, err := Minor(tc.text, 2); !errors.Is(err, tc.err) {
			t.Errorf("Minor(%q): %v, want %v", tc.text, err, tc.err)
		}
	}
}

func TestCurrencyValidation(t *testing.T) {
	got, err := New(-123, "usd")
	if err != nil || got != (Amount{Amount: -123, Currency: "USD"}) {
		t.Fatalf("New = %+v, %v", got, err)
	}
	for _, code := range []string{"", "US", "USDD", "ZZZ", "123", " USD"} {
		if _, err := New(0, code); err == nil {
			t.Errorf("New accepted currency %q", code)
		}
		if _, err := FromDecimal(decimal.Zero, code); err == nil {
			t.Errorf("FromDecimal accepted currency %q", code)
		}
		if _, err := (Amount{Currency: code}).Decimal(); err == nil {
			t.Errorf("Decimal accepted currency %q", code)
		}
	}
}

func TestPinnedISO4217Scales(t *testing.T) {
	for code, want := range map[string]int{
		"JPY": 0, "KWD": 3, "BHD": 3, "IQD": 3, "IRR": 2, "RSD": 2,
		"CLF": 4, "USD": 2, "UYW": 4, "OMR": 3, "TND": 3, "LYD": 3,
		"ISK": 0, "MGA": 2, "UGX": 0, "XAF": 0, "XOF": 0, "XPF": 0,
		"XAD": 2, "XCG": 2, "ZWG": 2,
	} {
		t.Run(code, func(t *testing.T) {
			got, err := Decimals(code)
			if err != nil || got != want {
				t.Fatalf("Decimals(%q) = %d, %v; want %d", code, got, err, want)
			}
			d := decimal.New(1234, -int32(want))
			amount, err := FromDecimal(d, code)
			if err != nil || amount.Amount != 1234 {
				t.Fatalf("FromDecimal(%s, %q) = %+v, %v", d, code, amount, err)
			}
			raw, err := ToMinor(d, code)
			if err != nil || raw != 1234 {
				t.Fatalf("ToMinor(%s, %q) = %d, %v", d, code, raw, err)
			}
			back, err := FromMinor(raw, code)
			if err != nil || !back.Equal(d) {
				t.Fatalf("FromMinor(%d, %q) = %s, %v", raw, code, back, err)
			}
		})
	}
}

func TestISO4217TableChecksum(t *testing.T) {
	codes := make([]string, 0, len(iso4217MinorUnits))
	for code := range iso4217MinorUnits {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	var table strings.Builder
	for _, code := range codes {
		fmt.Fprintf(&table, "%s=%d\n", code, iso4217MinorUnits[code])
	}
	const want = "15a3273d0b6e9f5d8e1352d7f6a4fce048c0fbd5bab39dab5bbe2b66d240d97a"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(table.String()))); got != want {
		t.Fatalf("ISO 4217 table checksum = %s, want %s; scale changes require a storage migration", got, want)
	}
}

func TestCurrenciesWithoutMinorUnitsAreRefused(t *testing.T) {
	for code, scale := range iso4217MinorUnits {
		if scale >= 0 {
			continue
		}
		if _, err := Decimals(code); err == nil {
			t.Errorf("Decimals accepted %s with ISO minor unit N.A.", code)
		}
		if _, err := FromDecimal(decimal.Zero, code); err == nil {
			t.Errorf("FromDecimal accepted %s with ISO minor unit N.A.", code)
		}
	}
}
