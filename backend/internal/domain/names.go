package domain

import (
	"strings"
	"unicode"
)

// Which account, category or rule a person meant by the words they used.
// Resolved on the server because a model left to find ids picks the first
// plausible row silently. The answer is one row, several to choose from, or
// none; only one row becomes part of a proposal.
//
// Matching is deliberately modest: an exact name wins, otherwise every
// meaningful word must begin a word of the candidate, after kind words
// ("card", "my") are set aside. No edit distance: a near miss that silently
// resolves is the guess this exists to prevent.

// NameCandidate is one row a name might mean.
type NameCandidate struct {
	ID   string
	Name string
	// Aliases are other words the row answers to: a card's last four digits, a
	// category's parent path. Matched like the name, never shown instead of it.
	Aliases []string
}

// NameResolution is what a name resolved to. Exactly one of Match and Choices
// is meaningful: a match, or the rows it might have been, or neither.
type NameResolution struct {
	Match   *NameCandidate
	Choices []NameCandidate
}

// Ambiguous reports a name that matched more than one row.
func (r NameResolution) Ambiguous() bool { return r.Match == nil && len(r.Choices) > 1 }

// nameNoise are words that say what kind of thing is meant rather than which.
var nameNoise = map[string]bool{
	"my": true, "the": true, "a": true, "an": true, "our": true,
	"card": true, "account": true, "acct": true, "category": true, "tag": true,
	"rule": true, "bill": true, "watchlist": true, "goal": true,
}

// ResolveName finds the row a name means. Tiers in order (id, whole name or
// alias, every meaningful word); the first that finds anything decides,
// because the next tier down would only find more, and several finds are
// returned as choices.
func ResolveName(query string, candidates []NameCandidate) NameResolution {
	query = strings.TrimSpace(query)
	if query == "" {
		return NameResolution{}
	}
	for i := range candidates {
		if strings.EqualFold(candidates[i].ID, query) {
			return NameResolution{Match: &candidates[i]}
		}
	}

	wanted := nameKey(query)
	var exact []int
	for i, one := range candidates {
		for _, text := range append([]string{one.Name}, one.Aliases...) {
			if nameKey(text) == wanted {
				exact = append(exact, i)
				break
			}
		}
	}
	if resolved, ok := decide(exact, candidates); ok {
		return resolved
	}

	words := meaningfulWords(query)
	if len(words) == 0 {
		return NameResolution{}
	}
	var partial []int
	for i, one := range candidates {
		have := Words(strings.Join(append([]string{one.Name}, one.Aliases...), " "))
		if everyWordBegins(words, have) {
			partial = append(partial, i)
		}
	}
	if resolved, ok := decide(partial, candidates); ok {
		return resolved
	}
	return NameResolution{}
}

func decide(found []int, candidates []NameCandidate) (NameResolution, bool) {
	switch len(found) {
	case 0:
		return NameResolution{}, false
	case 1:
		return NameResolution{Match: &candidates[found[0]]}, true
	}
	out := make([]NameCandidate, 0, len(found))
	for _, i := range found {
		out = append(out, candidates[i])
	}
	return NameResolution{Choices: out}, true
}

// nameKey is a name reduced to its words, for the whole-name comparison:
// "Food & Dining › Groceries" and "food dining groceries" are the same key.
func nameKey(text string) string {
	return strings.Join(Words(text), " ")
}

// Words is text as every name comparison reads it: folded by FoldName, then
// split on everything that is not a letter or a digit, so "CAFÉ-Bar" and
// "cafe bar" are the same two words.
func Words(text string) []string {
	return strings.FieldsFunc(FoldName(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// meaningfulWords is the request without the words that only say what kind of
// thing it is. A request made of nothing else keeps its words: "Card" may be
// the name of something.
func meaningfulWords(text string) []string {
	all := Words(text)
	out := make([]string, 0, len(all))
	for _, word := range all {
		if !nameNoise[word] {
			out = append(out, word)
		}
	}
	if len(out) == 0 {
		return all
	}
	return out
}

func everyWordBegins(words, have []string) bool {
	for _, word := range words {
		found := false
		for _, candidate := range have {
			if strings.HasPrefix(candidate, word) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
