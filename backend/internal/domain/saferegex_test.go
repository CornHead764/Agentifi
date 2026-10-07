package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPatternMatchesCaseInsensitively(t *testing.T) {
	require.True(t, SafeSearch("amazon", "AMAZON MKTPLACE PMTS"))
	require.False(t, SafeSearch("amazon", "WHOLE FOODS MKT"))
}

func TestAnInvalidPatternUnderMatchesRatherThanBreakingTheRuleEngine(t *testing.T) {
	require.False(t, SafeSearch("(unclosed", "anything"))
	// Cached as invalid, and still no match on the second look.
	require.False(t, SafeSearch("(unclosed", "anything"))
}

func TestABacktrackingPatternRunsInLinearTime(t *testing.T) {
	// (a+)+b against a long run of 'a' is the classic exponential
	// backtracking case; RE2 bounds it without a timeout.
	text := ""
	for range 64 {
		text += "a"
	}
	start := time.Now()
	require.False(t, SafeSearch("(a+)+b", text))
	require.Less(t, time.Since(start), time.Second)
}

func TestAPatternIsCompiledOnceAndReused(t *testing.T) {
	safeRegexMu.Lock()
	clear(safeRegexCache)
	safeRegexMu.Unlock()

	require.True(t, SafeSearch("^STARBUCKS", "STARBUCKS #1234"))
	first := compilePattern("^STARBUCKS")
	require.Same(t, first, compilePattern("^STARBUCKS"))
}
