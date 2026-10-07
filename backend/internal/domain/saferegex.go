package domain

import (
	"log/slog"
	"regexp"
	"sync"
	"unicode/utf8"
)

// Matching for user-typed patterns (watchlist vendor patterns, payee and
// transaction rules). Go's regexp is RE2: linear in the input and never
// backtracking, so a pattern like (a+)+b cannot stall the process and there is
// deliberately no match timeout, which could only drop a real match. Compiled
// patterns are cached because rules run per transaction.

// safeRegexCacheLimit bounds the cache; rules and watchlists number in the
// dozens.
const safeRegexCacheLimit = 1024

var (
	safeRegexMu    sync.Mutex
	safeRegexCache = map[string]*regexp.Regexp{}
)

// compilePattern compiles and caches a user pattern, returning nil for one
// that will not compile.
func compilePattern(pattern string) *regexp.Regexp {
	safeRegexMu.Lock()
	defer safeRegexMu.Unlock()

	if compiled, ok := safeRegexCache[pattern]; ok {
		return compiled
	}
	compiled, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		slog.Warn("user regex pattern does not compile and was skipped",
			"pattern", clipPattern(pattern), "error", err)
		compiled = nil
	}
	// A plain reset: the working set is tiny.
	if len(safeRegexCache) >= safeRegexCacheLimit {
		clear(safeRegexCache)
	}
	safeRegexCache[pattern] = compiled
	return compiled
}

// SafeSearch reports whether pattern matches anywhere in text,
// case-insensitively. An invalid pattern reports no match, so one malformed
// rule under-matches instead of stopping the rule engine.
func SafeSearch(pattern, text string) bool {
	compiled := compilePattern(pattern)
	if compiled == nil {
		return false
	}
	return compiled.MatchString(text)
}

// clipPattern keeps a malformed user pattern from reaching the log at full
// length, cutting on a rune boundary.
func clipPattern(pattern string) string {
	const limit = 100
	if len(pattern) <= limit {
		return pattern
	}
	clipped := pattern[:limit]
	for !utf8.ValidString(clipped) {
		clipped = clipped[:len(clipped)-1]
	}
	return clipped + "..."
}

// ValidPattern reports why a pattern will not compile, so a form can refuse
// it at save time. SafeSearch stays forgiving for patterns already stored.
func ValidPattern(pattern string) error {
	_, err := regexp.Compile("(?i)" + pattern)
	return err
}
