package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTakeSpendsTheKey(t *testing.T) {
	// Every challenge and one-time token goes out through Take. Reading and
	// deleting separately leaves a window in which two concurrent replays of
	// one challenge both see it as unused.
	store := NewSecretStore()
	store.Set("challenge", "payload", time.Minute)

	value, ok := TakeAs[string](store, "challenge")
	require.True(t, ok)
	require.Equal(t, "payload", value)

	_, ok = store.Take("challenge")
	require.False(t, ok)
}

func TestAnExpiredEntryIsGone(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	store := NewSecretStore()
	store.Now = clock.now

	store.Set("state", "value", time.Minute)
	clock.advance(59 * time.Second)
	_, ok := store.Get("state")
	require.True(t, ok)

	clock.advance(2 * time.Second)
	_, ok = store.Get("state")
	require.False(t, ok)
	require.Zero(t, store.Len(), "expired entries are reclaimed, not merely hidden")
}

func TestClaimIsWonOnce(t *testing.T) {
	store := NewSecretStore()
	require.True(t, store.Claim("step", time.Minute))
	require.False(t, store.Claim("step", time.Minute))
}

func TestHitCountsInAWindowThatIsNotExtendedByGuessing(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	store := NewSecretStore()
	store.Now = clock.now

	require.Equal(t, 1, store.Hit("attempts", time.Minute))
	clock.advance(30 * time.Second)
	require.Equal(t, 2, store.Hit("attempts", time.Minute))
	clock.advance(31 * time.Second)
	require.Equal(t, 1, store.Hit("attempts", time.Minute), "the window did not roll over")
}

func TestAValueOfTheWrongTypeReadsAsAbsent(t *testing.T) {
	// The only way this happens is two callers sharing a key prefix, and the
	// right answer for a challenge whose payload we cannot understand is to
	// refuse the ceremony rather than to guess at it.
	store := NewSecretStore()
	store.Set("key", 42, time.Minute)

	_, ok := TakeAs[string](store, "key")
	require.False(t, ok)
}
