package textutil

import "testing"

func TestHasWordHonorsUnderscoreAndFoldCasePerCall(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		word string
		opts WordOptions
		want bool
	}{
		{"underscore counted as a word char, so no break", "my_123456_text", "123456",
			WordOptions{Underscore: true}, false},
		{"underscore not a word char, so it is a break", "my_ira_account", "ira",
			WordOptions{}, true},
		{"hyphen is never a word char", "pre-ira-post", "ira", WordOptions{Underscore: true}, true},
		{"fold case matches either case", "Plainly texted you", "plainly",
			WordOptions{FoldCase: true}, true},
		{"no fold case is exact", "Plainly texted you", "plainly", WordOptions{}, false},
		{"a letter neighbour blocks the match", "Overdue", "Due", WordOptions{}, false},
		{"a non-ASCII letter neighbour blocks the match too", "café123", "123", WordOptions{}, false},
		{"an empty word matches nothing", "anything", "", WordOptions{}, false},
		{"a short code inside a longer run is not standalone", "call 3141592 now", "314159",
			WordOptions{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasWord(tc.text, tc.word, tc.opts); got != tc.want {
				t.Errorf("HasWord(%q, %q, %+v) = %v, want %v", tc.text, tc.word, tc.opts, got, tc.want)
			}
		})
	}
}

func TestContainsAnyFoldIsAPlainSubstringCheckCaseAside(t *testing.T) {
	needles := []string{"crypto", "bitcoin"}
	if !ContainsAnyFold("Our BITCOIN wallet", needles) {
		t.Error("want a fold-case substring match")
	}
	if ContainsAnyFold("nothing here", needles) {
		t.Error("want no match")
	}
}
