package browser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKeystrokesAreOnePerCharacterWithTheirDrawnTiming(t *testing.T) {
	pace := TypingPace
	pace.Draw = func(n int64) int64 { return 0 }
	keys := pace.Keystrokes("aé1")
	require.Equal(t, []Keystroke{
		{Text: "a", Gap: pace.Gap[0], Hold: pace.Hold[0]},
		{Text: "é", Gap: pace.Gap[0], Hold: pace.Hold[0]},
		{Text: "1", Gap: pace.Gap[0], Hold: pace.Hold[0]},
	}, keys)
	pace.Draw = func(n int64) int64 { return n - 1 }
	require.Equal(t, pace.Wait[1], pace.Pause())
}

// The slowest draw at every key still types a twenty-character password in a
// few seconds.
func TestAPasswordIsTypedInAFewSeconds(t *testing.T) {
	pace := TypingPace
	pace.Draw = func(n int64) int64 { return n - 1 }
	var took time.Duration
	for _, key := range pace.Keystrokes("twenty-characters-xy") {
		took += key.Gap + key.Hold
	}
	require.LessOrEqual(t, took, 5*time.Second)
}

func TestTheRandomPaceStaysInItsBounds(t *testing.T) {
	for _, key := range TypingPace.Keystrokes("an-invented-password") {
		require.GreaterOrEqual(t, key.Gap, TypingPace.Gap[0])
		require.LessOrEqual(t, key.Gap, TypingPace.Gap[1])
		require.GreaterOrEqual(t, key.Hold, TypingPace.Hold[0])
		require.LessOrEqual(t, key.Hold, TypingPace.Hold[1])
	}
	x, y := TypingPace.Aim(200, 40)
	require.True(t, x >= 50 && x <= 150, x)
	require.True(t, y >= 10 && y <= 30, y)
}
