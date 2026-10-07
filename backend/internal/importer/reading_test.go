package importer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnEmojiArrivesAsCodepointNotationAndIsDecoded(t *testing.T) {
	// Simplifi writes the watchlist glyph as "U+1f413" rather than as the
	// character.
	cases := []struct{ raw, want string }{
		{"U+1f413", "🐓"},
		{"u+1F413", "🐓"},
		// A sequence, which is how a joined emoji or a flag is written.
		{"U+1F468 U+200D U+1F4BB", "👨‍💻"},
		// Already a character: this application writes real glyphs, and they
		// must survive a round trip untouched.
		{"🐓", "🐓"},
		{"", ""},
		// Not the notation, however much it looks like it.
		{"U+NOTHEX", "U+NOTHEX"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, decodeCodepoints(c.raw), "input %q", c.raw)
	}
}

func TestParseDateAcceptsAnInstantTruncatedToItsDayAndRefusesTrailingJunk(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"2026-08-15", "2026-08-15", true},
		{"2026-08-21T18:00:00.000Z", "2026-08-21", true},
		{"2026-08-21 18:00:00", "2026-08-21", true},
		{"2026-01-01junk", "", false},
		{"2026-08-1", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := parseDate(c.raw)
		require.Equal(t, c.ok, ok, "input %q", c.raw)
		if c.ok {
			require.Equal(t, c.want, got.String(), "input %q", c.raw)
		}
	}
}

func TestMonthStartReadsOnlyAMonthOrADateNeverATrailingFragment(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"2026-08", "2026-08-01", true},
		{"2026-08-15", "2026-08-01", true},
		{"2026-08junk", "", false},
		{"not a month", "", false},
	}
	for _, c := range cases {
		r := newRecord(map[string]any{"date": c.raw})
		got := r.monthStart("date")
		if c.ok {
			require.False(t, r.failed(), "input %q", c.raw)
			require.Equal(t, c.want, got.String(), "input %q", c.raw)
		} else {
			require.True(t, r.failed(), "input %q should have failed", c.raw)
		}
	}
}
