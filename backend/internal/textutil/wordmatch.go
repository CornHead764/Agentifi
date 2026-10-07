package textutil

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// WordOptions decides what a match's edges may run into without breaking it:
// Underscore says whether '_' is itself a word character, and FoldCase says
// letters match case aside.
type WordOptions struct {
	Underscore bool
	FoldCase   bool
}

func wordRune(r rune, underscore bool) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || (underscore && r == '_')
}

// FindPhrase is the first place phrase matches in text as a whole word under
// opts, and where the match starts and ends in text, or -1, -1 for no match.
// Nothing in phrase is a pattern: a punctuation edge may sit against
// anything, so "Receipt #" matches "Receipt #4417", but a letter or digit
// edge may not run on into one in text, so "Due" does not match inside
// "Overdue". An empty phrase matches nothing.
func FindPhrase(text, phrase string, opts WordOptions) (start, end int) {
	if phrase == "" {
		return -1, -1
	}
	first, _ := utf8.DecodeRuneInString(phrase)
	last, _ := utf8.DecodeLastRuneInString(phrase)
	for at := range text {
		n, ok := matchPrefix(text[at:], phrase, opts.FoldCase)
		if !ok {
			continue
		}
		if wordRune(first, opts.Underscore) && at > 0 {
			if before, _ := utf8.DecodeLastRuneInString(text[:at]); wordRune(before, opts.Underscore) {
				continue
			}
		}
		if wordRune(last, opts.Underscore) && at+n < len(text) {
			if after, _ := utf8.DecodeRuneInString(text[at+n:]); wordRune(after, opts.Underscore) {
				continue
			}
		}
		return at, at + n
	}
	return -1, -1
}

// HasWord reports whether word appears anywhere in text as a whole word
// under opts.
func HasWord(text, word string, opts WordOptions) bool {
	start, _ := FindPhrase(text, word, opts)
	return start >= 0
}

func matchPrefix(s, phrase string, foldCase bool) (int, bool) {
	if !foldCase {
		if strings.HasPrefix(s, phrase) {
			return len(phrase), true
		}
		return 0, false
	}
	n := 0
	for _, want := range phrase {
		got, size := utf8.DecodeRuneInString(s[n:])
		if size == 0 || !equalFoldRune(got, want) {
			return 0, false
		}
		n += size
	}
	return n, true
}

func equalFoldRune(a, b rune) bool {
	if a == b {
		return true
	}
	for folded := unicode.SimpleFold(a); folded != a; folded = unicode.SimpleFold(folded) {
		if folded == b {
			return true
		}
	}
	return false
}

// ContainsFold reports whether s contains phrase as a plain substring, case
// aside; neither edge needs to be a word boundary.
func ContainsFold(s, phrase string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(phrase))
}

// ContainsAnyFold reports whether s contains any of phrases, case aside.
func ContainsAnyFold(s string, phrases []string) bool {
	for _, phrase := range phrases {
		if ContainsFold(s, phrase) {
			return true
		}
	}
	return false
}
