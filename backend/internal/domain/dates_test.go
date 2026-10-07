package domain

import "testing"

func TestParseMonthReadsOnlyYearDashMonth(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Month
		ok   bool
	}{
		{"2026-08", NewMonth(2026, 8), true},
		{"2026-01", NewMonth(2026, 1), true},
		{"2026-08-15", Month{}, false},
		{"2026-08junk", Month{}, false},
		{"26-08", Month{}, false},
		{"", Month{}, false},
	} {
		got, ok := ParseMonth(tc.in)
		if ok != tc.ok {
			t.Errorf("ParseMonth(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("ParseMonth(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
