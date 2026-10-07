package api

import "testing"

func TestOptionalMagnitudeReadsADollarSizeFromEitherWireShape(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{nil, ""},
		{"", ""},
		{"20", "20.00"},
		{"$20.00", "20.00"},
		{"-$20.00", "20.00"},
		{"(1,234.50)", "1234.50"},
		{float64(20), "20.00"},
		{float64(20.5), "20.50"},
	} {
		got, err := optionalMagnitude(map[string]any{"amount_max": tc.value}, "amount_max")
		if err != nil {
			t.Fatalf("optionalMagnitude(%#v) = %v; want no error", tc.value, err)
		}
		if got != tc.want {
			t.Errorf("optionalMagnitude(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestOptionalMagnitudeRefusesWhatIsNotOneAmount(t *testing.T) {
	for _, value := range []any{"1,2", "12-34", "abc", true, []any{}} {
		if _, err := optionalMagnitude(map[string]any{"amount_max": value}, "amount_max"); err == nil {
			t.Errorf("optionalMagnitude(%#v) = nil error; want refused", value)
		}
	}
}
