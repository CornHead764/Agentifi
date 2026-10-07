package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProfileDirIsAPlainNameOnTheVolumeOrNothing(t *testing.T) {
	root := t.TempDir()
	dir, err := ProfileDir(root, "connection-1")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "connection-1"), dir)

	for _, refused := range []string{"", ".", "..", "../escape", "a/b", "a b", "a\\b", "conn:1"} {
		_, err := ProfileDir(root, refused)
		require.ErrorIs(t, err, ErrNotAProfile, "%q", refused)
	}
}

func TestRecordedDeviceIsWrittenOnceAndReadBackAfterTheAgentMovesOn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	first, err := RecordedDevice(dir, DefaultDevice(DefaultViewport, testUserAgent))
	require.NoError(t, err)
	require.Equal(t, testUserAgent, first.UserAgent)

	// The next release ships a different Chrome. The profile is still the same
	// device, which is the whole point of the file.
	moved := DefaultDevice(Size{Width: 800, Height: 600}, testUserAgent)
	moved.UserAgent = "Mozilla/5.0 (X11; Linux x86_64) Chrome/999.0.0.0"
	kept, err := RecordedDevice(dir, moved)
	require.NoError(t, err)
	require.Equal(t, first, kept)
}

func TestRecordedDeviceReadsAKeptFile(t *testing.T) {
	dir := t.TempDir()
	// Written by hand in the shape already on the profiles volume.
	require.NoError(t, os.WriteFile(filepath.Join(dir, DeviceFile), []byte(`{
  "user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/128.0.0.0",
  "viewport": {
    "width": 1024,
    "height": 768
  },
  "locale": "en-GB",
  "timezone": "Europe/London"
}
`), 0o600))

	kept, err := RecordedDevice(dir, DefaultDevice(DefaultViewport, testUserAgent))
	require.NoError(t, err)
	require.Equal(t, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/128.0.0.0", kept.UserAgent)
	require.Equal(t, Size{Width: 1024, Height: 768}, kept.Viewport)
	require.Equal(t, "en-GB", kept.Locale)
	require.Equal(t, "Europe/London", kept.Timezone)
}

func TestRecordedDeviceWritesTheKeptShape(t *testing.T) {
	dir := t.TempDir()
	_, err := RecordedDevice(dir, DefaultDevice(Size{Width: 1280, Height: 900}, testUserAgent))
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(dir, DeviceFile))
	require.NoError(t, err)
	var written map[string]any
	require.NoError(t, json.Unmarshal(raw, &written))
	require.Contains(t, written, "user_agent")
	require.Contains(t, written, "viewport")
	require.Contains(t, written, "locale")
	require.Contains(t, written, "timezone")
	require.Equal(t, float64(1280), written["viewport"].(map[string]any)["width"])
	require.Equal(t, byte('\n'), raw[len(raw)-1])
}

func TestARecordedDeviceNothingCanReadIsWrittenAfresh(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, DeviceFile), []byte("{not json"), 0o600))

	device, err := RecordedDevice(dir, DefaultDevice(DefaultViewport, testUserAgent))
	require.NoError(t, err)
	require.Equal(t, testUserAgent, device.UserAgent)

	again, err := RecordedDevice(dir, DefaultDevice(Size{Width: 640, Height: 480}, testUserAgent))
	require.NoError(t, err)
	require.Equal(t, device, again)
}

func TestOneProfileIsOpenAtATime(t *testing.T) {
	engine := &Engine{}
	dir := filepath.Join(t.TempDir(), "connection-1")

	require.NoError(t, engine.Claim(dir))
	require.True(t, engine.Held(dir))
	require.ErrorIs(t, engine.Claim(dir), ErrProfileBusy)
	// A different connection is not the same directory.
	require.NoError(t, engine.Claim(dir+"-other"))

	engine.Release(dir)
	require.False(t, engine.Held(dir))
	require.NoError(t, engine.Claim(dir))
}

func TestTheLockFileIsWhatTheTwoProcessesAgreeOn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	now := time.Now()

	// Another process is holding this one.
	other, err := TakeProfileLock(dir, "agent", now)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, LockFile))

	_, err = TakeProfileLock(dir, LockHolder, now)
	require.ErrorIs(t, err, ErrProfileBusy)
	require.ErrorContains(t, err, "agent is holding")

	// An engine's claim is refused by the same lock, and the in-process slot
	// is given back rather than left held.
	engine := &Engine{}
	require.ErrorIs(t, engine.Claim(dir), ErrProfileBusy)
	require.False(t, engine.Held(dir))

	ReleaseProfileLock(dir, other)
	require.NoError(t, engine.Claim(dir))
	require.True(t, engine.Held(dir))
	engine.Release(dir)
	require.NoFileExists(t, filepath.Join(dir, LockFile))
}

// The deploy overlap, which is the case the file lock exists for: the old
// container is still driving a real Chromium on that directory, and it says so
// by refreshing. A holder that is still refreshing is honoured however long it
// has been holding.
func TestALockThatIsBeingRefreshedIsNeverTakenOver(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	start := time.Now()
	token, err := TakeProfileLock(dir, "agent", start)
	require.NoError(t, err)

	// An hour of it, a heartbeat at a time: a person who walked away mid
	// sign-in is the same shape as a container that is simply still working.
	last := start
	for at := start; at.Sub(start) < time.Hour; at = at.Add(LockHeartbeat) {
		require.NoError(t, RefreshProfileLock(dir, "agent", token, at))
		last = at
		_, err := TakeProfileLock(dir, LockHolder, at)
		require.ErrorIs(t, err, ErrProfileBusy, "at %s", at.Sub(start))
	}

	// Still refused a moment short of the window after the last heartbeat.
	_, err = TakeProfileLock(dir, LockHolder, last.Add(LockStale-time.Second))
	require.ErrorIs(t, err, ErrProfileBusy)
}

func TestALockWhoseHolderStoppedRefreshingIsTakenOverOnceItIsStale(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	start := time.Now()
	token, err := TakeProfileLock(dir, "agent", start)
	require.NoError(t, err)

	// It was refreshed for a while and then the container went away.
	last := start.Add(10 * LockHeartbeat)
	require.NoError(t, RefreshProfileLock(dir, "agent", token, last))

	// A heartbeat late is not gone; the window is a few of them, so a host
	// under load does not lose a lock it is still holding.
	_, err = TakeProfileLock(dir, LockHolder, last.Add(LockHeartbeat))
	require.ErrorIs(t, err, ErrProfileBusy)

	taken, err := TakeProfileLock(dir, LockHolder, last.Add(LockStale+time.Second))
	require.NoError(t, err)
	require.NotEqual(t, token, taken)

	// And the holder it replaced does not restamp its way back on top of it.
	require.ErrorIs(t, RefreshProfileLock(dir, "agent", token, last.Add(time.Hour)), ErrLockTakenOver)
	held, err := readProfileLock(filepath.Join(dir, LockFile))
	require.NoError(t, err)
	require.Equal(t, taken, held.lock.Token)

	// A lock nothing can read is not a claim anybody can act on.
	require.NoError(t, os.WriteFile(filepath.Join(dir, LockFile), []byte("{not json"), 0o600))
	_, err = TakeProfileLock(dir, LockHolder, time.Now())
	require.NoError(t, err)
}

// A lock whose container was replaced must not refuse every later sign-in.
// The PID in the file is no help: the application is the container's main
// process, so the new one has it too.
func TestTheLockADeployOrphanedDoesNotOutliveTheContainer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	written := time.Now()
	_, err := TakeProfileLock(dir, LockHolder, written)
	require.NoError(t, err)

	held, err := readProfileLock(filepath.Join(dir, LockFile))
	require.NoError(t, err)
	require.Equal(t, os.Getpid(), held.lock.PID)

	restarted := written.Add(11 * time.Minute)
	engine := &Engine{Now: func() time.Time { return restarted }}
	require.NoError(t, engine.Claim(dir))
	engine.Release(dir)
}

// A refresh does not write a file back that a Release has removed: the lock
// would then sit there with no Chromium behind it until it went stale.
func TestARefreshDoesNotResurrectAReleasedLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	token, err := TakeProfileLock(dir, LockHolder, time.Now())
	require.NoError(t, err)
	ReleaseProfileLock(dir, token)

	require.Error(t, RefreshProfileLock(dir, LockHolder, token, time.Now()))
	require.NoFileExists(t, filepath.Join(dir, LockFile))
}

// The heartbeat itself, on the real clock because a ticker is what is being
// tested. The interval is the engine's, so the wait is milliseconds.
func TestAHeldLockIsRestampedWhileItIsHeld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	engine := &Engine{Heartbeat: time.Millisecond}
	require.NoError(t, engine.Claim(dir))
	t.Cleanup(func() { engine.Release(dir) })

	first, err := readProfileLock(filepath.Join(dir, LockFile))
	require.NoError(t, err)
	require.NotEmpty(t, first.lock.Token)

	// The stamp is whole seconds, so what is asserted is that the file is
	// being rewritten at all, not that its second moved.
	written := func() int64 {
		info, err := os.Stat(filepath.Join(dir, LockFile))
		require.NoError(t, err)
		return info.ModTime().UnixNano()
	}
	was := written()
	require.Eventually(t, func() bool { return written() != was }, 5*time.Second, time.Millisecond)

	again, err := readProfileLock(filepath.Join(dir, LockFile))
	require.NoError(t, err)
	require.Equal(t, first.lock.Token, again.lock.Token, "the same hold, restamped")
}

// The property the deploy overlap rests on: a reader never catches a restamp
// halfway. A lock written where it stands is truncated for an instant on every
// heartbeat, and a reader that landed there would read a live holder's lock as
// one nobody can act on and take the profile over.
func TestARestampIsNeverSeenHalfWritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	start := time.Now()
	token, err := TakeProfileLock(dir, LockHolder, start)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for beat := range 400 {
			_ = RefreshProfileLock(dir, LockHolder, token, start.Add(time.Duration(beat)*time.Second))
		}
	}()

	for range 400 {
		// The other process's read: whatever it catches, it is a lock some
		// holder is refreshing, never an empty file.
		held, err := readProfileLock(filepath.Join(dir, LockFile))
		require.NoError(t, err)
		require.Equal(t, token, held.lock.Token)
	}
	<-done
}

// A claim that is given back leaves nothing running. The beat belongs to the
// claim set, not to any one claim, so the last Release is what ends it.
func TestClaimAndReleaseLeaveNoGoroutineBehind(t *testing.T) {
	root := t.TempDir()
	engine := &Engine{Heartbeat: time.Millisecond}
	before := runtime.NumGoroutine()

	for round := range 20 {
		first := filepath.Join(root, fmt.Sprintf("connection-%d", round))
		second := first + "-other"
		require.NoError(t, engine.Claim(first))
		require.NoError(t, engine.Claim(second))
		engine.Release(first)
		engine.Release(second)
	}

	// Counted from this goroutine rather than through require.Eventually,
	// which runs its condition in a goroutine of its own and would be counting
	// itself.
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.LessOrEqual(t, runtime.NumGoroutine(), before)
}

// Close is the other end: a process shutting down has stopped every browser
// it started, so it stops the heartbeat and gives its locks back rather than
// leaving them for the next process to wait out.
func TestCloseStopsTheHeartbeatAndReleasesTheLocks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	engine := &Engine{Heartbeat: time.Millisecond}
	require.NoError(t, engine.Claim(dir))
	require.NoError(t, engine.Close())

	require.NoFileExists(t, filepath.Join(dir, LockFile))
	require.False(t, engine.Held(dir))
	_, err := TakeProfileLock(dir, LockHolder, time.Now())
	require.NoError(t, err)
}

// The other half of the same deploy. Chromium keeps its own singleton in the
// user data directory and refuses to launch on one a different host is
// holding; a container killed mid-sign-in leaves one naming a host that will
// never come back, and Playwright surfaces the refusal as a failed launch
// rather than as anything our lock could see.
func TestAClaimClearsTheSingletonADeadChromiumLeft(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	// The shape Chromium writes: symlinks, and the socket points at a /tmp on
	// a host this process has never been.
	require.NoError(t, os.Symlink("abcdef012345-4242", filepath.Join(dir, "SingletonLock")))
	require.NoError(t, os.Symlink("1234567890123456789", filepath.Join(dir, "SingletonCookie")))
	require.NoError(t, os.Symlink("/tmp/org.chromium.Chromium.invented/SingletonSocket",
		filepath.Join(dir, "SingletonSocket")))

	engine := &Engine{}
	require.NoError(t, engine.Claim(dir))
	t.Cleanup(func() { engine.Release(dir) })

	for _, name := range singletonFiles {
		_, err := os.Lstat(filepath.Join(dir, name))
		require.ErrorIs(t, err, os.ErrNotExist, "%s", name)
	}
}

// And the case that must not be broken by the one above: a live holder is
// still refreshing, so the claim is refused and the singleton is left exactly
// where it is — it belongs to a Chromium that is running.
func TestARefusedClaimLeavesTheSingletonAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	_, err := TakeProfileLock(dir, "agent", time.Now())
	require.NoError(t, err)
	for _, name := range singletonFiles {
		require.NoError(t, os.Symlink("live", filepath.Join(dir, name)))
	}

	engine := &Engine{}
	require.ErrorIs(t, engine.Claim(dir), ErrProfileBusy)

	for _, name := range singletonFiles {
		target, err := os.Readlink(filepath.Join(dir, name))
		require.NoError(t, err, "%s", name)
		require.Equal(t, "live", target)
	}
}

// Removing a symlink removes the link, never what it points at: the socket one
// names a path in somebody's /tmp.
func TestClearingTheSingletonDoesNotFollowItsSymlinks(t *testing.T) {
	dir := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "not-ours")
	require.NoError(t, os.WriteFile(elsewhere, []byte("someone else's"), 0o600))
	require.NoError(t, os.Symlink(elsewhere, filepath.Join(dir, "SingletonSocket")))

	ClearSingleton(dir)
	require.NoFileExists(t, filepath.Join(dir, "SingletonSocket"))
	require.FileExists(t, elsewhere)

	// A directory with no singleton in it — the clean-exit case — is not a
	// failure either.
	ClearSingleton(dir)
}

func TestProfileExistsAndForget(t *testing.T) {
	root := t.TempDir()
	dir, err := ProfileDir(root, "connection-1")
	require.NoError(t, err)
	require.False(t, ProfileExists(dir))

	_, err = RecordedDevice(dir, DefaultDevice(DefaultViewport, testUserAgent))
	require.NoError(t, err)
	require.True(t, ProfileExists(dir))

	require.NoError(t, ForgetProfile(root, "connection-1"))
	require.False(t, ProfileExists(dir))
	require.ErrorIs(t, ForgetProfile(root, ".."), ErrNotAProfile)
}

// Which session a pull opens with, when a profile on disk and a sealed session
// from the backend could both supply one.
//
// The rule that matters is the first one: a profile on disk is the session of
// record, and the sealed state the backend sent along is not used. Getting that
// backwards would overwrite a live sign-in with whatever was last sealed.
func TestAProfileOnDiskIsOpenedAsItStandsAndASentSessionIsNotUsed(t *testing.T) {
	root := t.TempDir()
	dir, err := ProfileDir(root, "connection-1")
	require.NoError(t, err)
	kept := StorageState{Cookies: []StoredCookie{{Name: "session", Value: "invented"}}}

	// Nothing on disk, and nothing sent: a fresh profile, said plainly.
	plan := PlanProfile(dir, StorageState{})
	require.False(t, plan.Seed)
	require.Contains(t, plan.Note, "a fresh one was started")

	// Nothing on disk and a sealed snapshot: the snapshot seeds it.
	plan = PlanProfile(dir, kept)
	require.True(t, plan.Seed)
	require.Contains(t, plan.Note, "seeded from the kept session")

	// A profile on disk: opened as it stands, whatever was sent.
	_, err = RecordedDevice(dir, DefaultDevice(DefaultViewport, testUserAgent))
	require.NoError(t, err)
	plan = PlanProfile(dir, kept)
	require.False(t, plan.Seed)
	require.Empty(t, plan.Note, "the ordinary case is no news, and must not be a pull's first note")
}

// A container that died holds both the lock and its Chromium's singleton.
// Taking the lock over while leaving the singleton makes the next launch
// refuse the user data directory, with nothing in our lock to explain it.
// TestAProfileOrphanedByADeployStillOpens is the same path with a real
// Chromium behind it; this one runs everywhere.
func TestALockTakenOverFromADeadHolderClearsTheSingletonToo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	_, err := TakeProfileLock(dir, LockHolder, time.Now().Add(-LockStale-time.Minute))
	require.NoError(t, err)
	for _, name := range singletonFiles {
		require.NoError(t, os.Symlink("dead-host-4242", filepath.Join(dir, name)))
	}

	engine := &Engine{}
	require.NoError(t, engine.Claim(dir), "a lock nobody refreshes is taken over")
	t.Cleanup(func() { engine.Release(dir) })

	for _, name := range singletonFiles {
		_, err := os.Lstat(filepath.Join(dir, name))
		require.ErrorIs(t, err, os.ErrNotExist, "%s outlived the takeover", name)
	}
}

// Releasing by hand is the same reading, asked for on purpose: a household is
// not made to wait out LockStale on a profile a restart left locked.
func TestReleasingAProfileNobodyRefreshesGivesUpBothHolds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	now := time.Now()
	_, err := TakeProfileLock(dir, LockHolder, now.Add(-LockStale-time.Minute))
	require.NoError(t, err)
	for _, name := range singletonFiles {
		require.NoError(t, os.Symlink("dead-host-4242", filepath.Join(dir, name)))
	}

	gave, err := ReleaseAbandonedProfile(dir, now)
	require.NoError(t, err)
	require.True(t, gave.Lock)
	require.True(t, gave.Singleton)
	require.NoFileExists(t, filepath.Join(dir, LockFile))
	for _, name := range singletonFiles {
		_, err := os.Lstat(filepath.Join(dir, name))
		require.ErrorIs(t, err, os.ErrNotExist, "%s", name)
	}
}

// The case the cross-process lock exists for, and the one a release must never
// win: the old container is still driving a real Chromium on this directory,
// and taking its lock or its singleton away is the corruption.
func TestReleasingAProfileSomebodyIsStillRefreshingIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	now := time.Now()
	_, err := TakeProfileLock(dir, "the other container", now)
	require.NoError(t, err)
	for _, name := range singletonFiles {
		require.NoError(t, os.Symlink("live", filepath.Join(dir, name)))
	}

	_, err = ReleaseAbandonedProfile(dir, now.Add(LockStale-time.Second))
	require.ErrorIs(t, err, ErrProfileBusy)
	require.FileExists(t, filepath.Join(dir, LockFile))
	for _, name := range singletonFiles {
		target, err := os.Readlink(filepath.Join(dir, name))
		require.NoError(t, err, "%s", name)
		require.Equal(t, "live", target)
	}

	// And the same directory once the heartbeat has stopped mattering.
	gave, err := ReleaseAbandonedProfile(dir, now.Add(LockStale+time.Second))
	require.NoError(t, err)
	require.True(t, gave.Lock)
}

// Releasing a profile nothing is holding is what somebody gets when the hold
// had already timed out. It gives up nothing, and says so rather than failing.
func TestReleasingAProfileNothingHoldsGivesUpNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	gave, err := ReleaseAbandonedProfile(dir, time.Now())
	require.NoError(t, err)
	require.False(t, gave.Lock)
	require.False(t, gave.Singleton)

	// A singleton with no lock beside it is still a hold: a Chromium killed
	// with the container that released neither.
	require.NoError(t, os.Symlink("dead-host-4242", filepath.Join(dir, "SingletonLock")))
	gave, err = ReleaseAbandonedProfile(dir, time.Now())
	require.NoError(t, err)
	require.False(t, gave.Lock)
	require.True(t, gave.Singleton)
}

// A lock nothing can read is not a claim anybody can act on — the rule
// TakeProfileLock takes one over by, asked here as well so the two cannot
// drift into a release that refuses what a claim would take.
func TestALockNothingCanReadIsNotALiveHold(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connection-1")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, LockFile), []byte("{half-writ"), 0o600))

	require.Equal(t, HoldAbandoned, ReadProfileHold(dir, time.Now()))
	gave, err := ReleaseAbandonedProfile(dir, time.Now())
	require.NoError(t, err)
	require.True(t, gave.Lock)
}
