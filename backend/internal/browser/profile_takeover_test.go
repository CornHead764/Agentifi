package browser

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Two processes that both find a stale lock — a deploy's two containers — must
// not both take it: two Chromiums on one user data directory corrupt it.
func TestOnlyOneOfTwoProcessesTakesAStaleLockOver(t *testing.T) {
	for round := 0; round < 50; round++ {
		dir := filepath.Join(t.TempDir(), "connection-1")
		_, err := TakeProfileLock(dir, "the container that died", time.Now().Add(-LockStale-time.Minute))
		require.NoError(t, err)

		var wg sync.WaitGroup
		var mu sync.Mutex
		won := 0
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := TakeProfileLock(dir, LockHolder, time.Now()); err == nil {
					mu.Lock()
					won++
					mu.Unlock()
				} else {
					require.ErrorIs(t, err, ErrProfileBusy)
				}
			}()
		}
		wg.Wait()
		require.Equal(t, 1, won, "round %d", round)
	}
}

// A holder that lost its lock to a takeover must not delete the new holder's
// lock when it lets go.
func TestAReleaseAfterATakeoverLeavesTheNewHoldersLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	quiet, err := TakeProfileLock(dir, LockHolder, time.Now().Add(-LockStale-time.Minute))
	require.NoError(t, err)
	_, err = TakeProfileLock(dir, "the new container", time.Now())
	require.NoError(t, err)

	ReleaseProfileLock(dir, quiet)

	require.FileExists(t, filepath.Join(dir, LockFile))
	require.Equal(t, HoldLive, ReadProfileHold(dir, time.Now()))
}

// The heartbeat that finds its lock taken over closes the browser on it.
func TestAHeartbeatThatFindsItsLockTakenOverClosesTheBrowser(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	var clock sync.Mutex
	now := time.Now().Add(-LockStale - time.Minute)
	engine := &Engine{
		Heartbeat: time.Millisecond,
		Now: func() time.Time {
			clock.Lock()
			defer clock.Unlock()
			return now
		},
	}
	require.NoError(t, engine.Claim(dir))
	closed := make(chan struct{})
	engine.OnLost(dir, func() { close(closed) })

	// The process went quiet, and another took the directory.
	engine.stopBeat()
	_, err := TakeProfileLock(dir, "the new container", time.Now())
	require.NoError(t, err)
	clock.Lock()
	now = time.Now()
	clock.Unlock()
	engine.claimsMu.Lock()
	engine.startBeat()
	engine.claimsMu.Unlock()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the browser on a directory another process took was left running")
	}
	require.False(t, engine.Held(dir))
	require.Equal(t, HoldLive, ReadProfileHold(dir, time.Now()), "the new holder keeps its lock")
	require.NoError(t, engine.Close())
}
