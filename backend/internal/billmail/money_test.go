package billmail

import (
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestParseAmount(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"$1,234.56", "1234.56", true},
		{"123.45", "123.45", true},
		{"87", "87", true},
		{"$75.00  Auto Pay Date", "75", true},
		{"$12,345,678.90", "12345678.9", true},
		{"($41.00)", "-41", true},
		{"-$41.00", "-41", true},
		{"$41.00 CR", "-41", true},
		{"$41.00CR", "-41", true},
		{"no figure here", "", false},
	} {
		got, ok := ParseAmount(tc.in)
		if ok != tc.ok {
			t.Fatalf("ParseAmount(%q) ok = %v, want %v", tc.in, ok, tc.ok)
		}
		if ok && !got.Equal(domain.MustFromString(tc.want)) {
			t.Fatalf("ParseAmount(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}
