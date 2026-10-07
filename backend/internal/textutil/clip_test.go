package textutil

import "testing"

func TestFirstLineSplitsOnEitherLineEndingAndTrims(t *testing.T) {
	cases := map[string]string{
		"one sentence, then ids and stamps\nid=42\nstamp=2026-01-01": "one sentence, then ids and stamps",
		"a line ended by CRLF\r\nmore below":                         "a line ended by CRLF",
		"  padded line  \ntrailer":                                   "padded line",
		"no break at all":                                            "no break at all",
		"":                                                           "",
	}
	for in, want := range cases {
		if got := FirstLine(in); got != want {
			t.Errorf("FirstLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClipCountsCharactersNotBytes(t *testing.T) {
	cases := []struct {
		value  string
		runes  int
		clip   string
		marked string
	}{
		{"", 3, "", ""},
		{"abc", 3, "abc", "abc"},
		{"abcd", 3, "abc", "abc…"},
		{"café au lait", 4, "café", "café…"},
		{"ééé", 2, "éé", "éé…"},
		{"日本語テキスト", 3, "日本語", "日本語…"},
		{"abc", 0, "", "…"},
	}
	for _, c := range cases {
		if got := Clip(c.value, c.runes); got != c.clip {
			t.Errorf("Clip(%q, %d) = %q, want %q", c.value, c.runes, got, c.clip)
		}
		if got := ClipMarked(c.value, c.runes); got != c.marked {
			t.Errorf("ClipMarked(%q, %d) = %q, want %q", c.value, c.runes, got, c.marked)
		}
	}
}

func TestFirstNonBlankSkipsWhitespaceAndTrimsWhatItKeeps(t *testing.T) {
	cases := []struct {
		values []string
		want   string
	}{
		{nil, ""},
		{[]string{"", "  ", "\t"}, ""},
		{[]string{" \n", "  Acme Co  ", "later"}, "Acme Co"},
		{[]string{"first", "second"}, "first"},
	}
	for _, c := range cases {
		if got := FirstNonBlank(c.values...); got != c.want {
			t.Errorf("FirstNonBlank(%q) = %q, want %q", c.values, got, c.want)
		}
	}
}

func TestCapitalizeRaisesOnlyTheFirstLetter(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"purchases":     "Purchases",
		"token expired": "Token expired",
		"éclair":        "Éclair",
		"Already":       "Already",
	}
	for value, want := range cases {
		if got := Capitalize(value); got != want {
			t.Errorf("Capitalize(%q) = %q, want %q", value, got, want)
		}
	}
}
