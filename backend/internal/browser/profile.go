package browser

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

// A browser provider's session of record is a Chromium profile on the profiles
// volume, one directory per connection: device trust often lives in IndexedDB
// or a service worker's cache, which a storage state does not carry.
//
// The directory names, the device file's name and its shape are the volume's
// format: changing any of them orphans every profile on it.

// DeviceFile holds a profile's recorded device properties.
const DeviceFile = "fingerprint.json"

// profileName is what a profile id may be. A name that is not a plain one is
// refused rather than cleaned up: a profile id that escapes the volume is not a
// profile id.
var profileName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

var ErrNotAProfile = errors.New("browser: that is not a profile name")

// ErrProfileBusy is a second open of a profile something else is holding. Two
// Chromiums on one user data directory corrupt it, so a second open is refused
// rather than queued.
var ErrProfileBusy = errors.New("browser: that connection is already open")

// ErrLockTakenOver is a refresh of a lock another process took over after this
// one went quiet for longer than LockStale.
var ErrLockTakenOver = errors.New("browser: the profile lock was taken over")

// Device is what a profile records about its browser on its first open. The
// JSON shape is part of the volume's format.
type Device struct {
	UserAgent string `json:"user_agent"`
	Viewport  Size   `json:"viewport"`
	Locale    string `json:"locale"`
	Timezone  string `json:"timezone"`
}

func DefaultDevice(viewport Size, userAgent string) Device {
	if viewport.Width == 0 || viewport.Height == 0 {
		viewport = DefaultViewport
	}
	zone := os.Getenv("TZ")
	if zone == "" {
		zone = defaultTimezone
	}
	return Device{
		UserAgent: userAgent,
		Viewport:  viewport,
		Locale:    defaultLocale,
		Timezone:  zone,
	}
}

const DefaultProfilesRoot = "/profiles"

// ProfileDir is where a connection's profile lives. ErrNotAProfile means "no
// profile, open a context from the sealed storage state".
func ProfileDir(root, profile string) (string, error) {
	if !profileName.MatchString(profile) || profile == "." || profile == ".." {
		return "", fmt.Errorf("%w: %q", ErrNotAProfile, profile)
	}
	if root == "" {
		root = DefaultProfilesRoot
	}
	return filepath.Join(root, profile), nil
}

func ProfileExists(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// RecordedDevice is the device this profile is, read back on every open after the
// first so an upgrade that moves UserAgent does not make a site ask again. An
// unreadable file is written afresh rather than reported, so the connection
// stays pullable.
func RecordedDevice(dir string, proposed Device) (Device, error) {
	raw, err := os.ReadFile(filepath.Join(dir, DeviceFile))
	if err == nil {
		var kept Device
		if json.Unmarshal(raw, &kept) == nil && kept.UserAgent != "" {
			if kept.Viewport.Width == 0 {
				kept.Viewport.Width = proposed.Viewport.Width
			}
			if kept.Viewport.Height == 0 {
				kept.Viewport.Height = proposed.Viewport.Height
			}
			if kept.Locale == "" {
				kept.Locale = proposed.Locale
			}
			if kept.Timezone == "" {
				kept.Timezone = proposed.Timezone
			}
			return kept, nil
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return proposed, fmt.Errorf("browser: no profile directory at %s: %w", dir, err)
	}
	encoded, err := json.MarshalIndent(proposed, "", "  ")
	if err != nil {
		return proposed, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(dir, DeviceFile), encoded, 0o600); err != nil {
		return proposed, fmt.Errorf("browser: the recorded device could not be written: %w", err)
	}
	return proposed, nil
}

// Claim takes the one holder's place for a profile directory, or answers
// ErrProfileBusy. Two Chromiums on one user data directory corrupt it, and the
// old and new containers overlap on every deploy: the map is this process's
// lock, the lock file is what any two processes agree on. A successful claim
// starts the heartbeat and makes it safe to clear a dead Chromium's singleton.
func (e *Engine) Claim(dir string) error {
	e.claimsMu.Lock()
	defer e.claimsMu.Unlock()
	if _, held := e.claims[dir]; held {
		return fmt.Errorf("%w: %s", ErrProfileBusy, dir)
	}
	token, err := TakeProfileLock(dir, LockHolder, e.now())
	if err != nil {
		return err
	}
	if e.claims == nil {
		e.claims = map[string]string{}
	}
	e.claims[dir] = token
	ClearSingleton(dir)
	e.startBeat()
	return nil
}

// Release gives the directory back. Call it only once the context is closed.
func (e *Engine) Release(dir string) {
	if dir == "" {
		return
	}
	e.claimsMu.Lock()
	defer e.claimsMu.Unlock()
	token, held := e.claims[dir]
	if !held {
		return
	}
	delete(e.claims, dir)
	delete(e.lost, dir)
	ReleaseProfileLock(dir, token)
	if len(e.claims) == 0 {
		e.stopBeatLocked()
	}
}

func (e *Engine) releaseAll() {
	e.claimsMu.Lock()
	defer e.claimsMu.Unlock()
	for dir, token := range e.claims {
		ReleaseProfileLock(dir, token)
	}
	e.claims = nil
	e.lost = nil
}

// OnLost is what to do when the heartbeat finds another process has taken a
// claimed directory over: close the browser on it. The claim is dropped before
// lost runs.
func (e *Engine) OnLost(dir string, lost func()) {
	e.claimsMu.Lock()
	defer e.claimsMu.Unlock()
	if _, held := e.claims[dir]; !held {
		return
	}
	if e.lost == nil {
		e.lost = map[string]func(){}
	}
	e.lost[dir] = lost
}

// startBeat keeps every lock this process holds restamped: one goroutine for
// the whole claim set, ended by the last Release or by Close. Callers hold
// e.claimsMu.
func (e *Engine) startBeat() {
	if e.beating != nil {
		return
	}
	every := e.Heartbeat
	if every <= 0 {
		every = LockHeartbeat
	}
	stop := make(chan struct{})
	e.beating = stop
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				e.restamp()
			}
		}
	}()
}

func (e *Engine) stopBeat() {
	e.claimsMu.Lock()
	defer e.claimsMu.Unlock()
	e.stopBeatLocked()
}

func (e *Engine) stopBeatLocked() {
	if e.beating == nil {
		return
	}
	close(e.beating)
	e.beating = nil
}

// restamp holds the claim mutex for the whole pass, so it cannot land between
// a Release's two steps and write back a lock file that was just removed.
func (e *Engine) restamp() {
	now := e.now()
	var lost []func()
	e.claimsMu.Lock()
	for dir, token := range e.claims {
		// A takeover cannot be undone, so the browser here is closed instead.
		if errors.Is(RefreshProfileLock(dir, LockHolder, token, now), ErrLockTakenOver) {
			delete(e.claims, dir)
			if fn := e.lost[dir]; fn != nil {
				lost = append(lost, fn)
			}
			delete(e.lost, dir)
		}
	}
	e.claimsMu.Unlock()
	for _, fn := range lost {
		go fn()
	}
}

// The lock any two processes sharing the profiles volume agree on: a lock file,
// with a flock on the guard beside it ordering changes to it. A crash leaves
// the file behind, so a holder rewrites it every LockHeartbeat and a lock
// nobody has rewritten for LockStale is taken over. Staleness is a stopped
// heartbeat, not a fixed span (a person's sign-in can legitimately run long),
// and not the PID either: the app is the container's main process, so a
// restarted container reuses the same low PID.
const (
	LockFile = "profile.lock"
	// LockHolder is what this process writes into it. A name is read for the
	// message and for nothing else, so a lock written under a different name
	// is honoured too.
	LockHolder    = "agentifi"
	LockHeartbeat = 30 * time.Second
	// LockStale is a few heartbeats: long enough that a loaded host does not
	// lose a lock it still holds, short enough that a profile orphaned by a
	// deploy is soon usable again.
	LockStale = 3 * time.Minute
)

type profileLock struct {
	Holder string `json:"holder"`
	PID    int    `json:"pid"`
	At     string `json:"at"`
	// Token stops a holder that went stale and was taken over from restamping
	// its way back on top of the new holder. A lock without one is never
	// refreshed by us.
	Token string `json:"token,omitempty"`
}

// TakeProfileLock claims a profile directory across processes, and answers the
// token the hold must refresh itself with.
func TakeProfileLock(dir, holder string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, LockFile)
	token := lockToken()
	written, err := marshalProfileLock(holder, token, now)
	if err != nil {
		return "", err
	}
	staged, err := stageProfileLock(dir, written)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(staged) }()

	// A link rather than a create: exclusive, and never visible half written.
	// A stale lock is removed and the link retried, never renamed over: two
	// processes that both judged it stale would both rename and both succeed.
	for attempt := 0; attempt < 3; attempt++ {
		err = os.Link(staged, path)
		if err == nil {
			return token, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		raw, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		held, parseErr := parseProfileLock(raw)
		if readErr == nil && parseErr == nil && now.Sub(held.at) < LockStale {
			return "", fmt.Errorf("%w: %s is holding %s", ErrProfileBusy, held.lock.Holder, dir)
		}
		// Stale or unreadable: take it over rather than leave the connection
		// unpullable forever.
		if _, err := removeLockIf(dir, raw); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: another process took %s first", ErrProfileBusy, dir)
}

// removeLockIf removes the lock file only while it is still the one that was
// read, so a process that judged a lock stale never removes the fresh one
// another process put there since.
func removeLockIf(dir string, judged []byte) (bool, error) {
	removed := false
	err := underLockGuard(dir, func() error {
		path := filepath.Join(dir, LockFile)
		current, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(current, judged) {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		removed = true
		return nil
	})
	return removed, err
}

// lockGuardFile sits beside the lock, and is never removed.
const lockGuardFile = ".profile.lock.guard"

// underLockGuard runs fn under an exclusive flock on the directory's guard
// file. Everything that changes or removes an existing lock file runs under it,
// so a read and the change that depends on it cannot interleave with another
// process's. The kernel drops the flock when its holder dies.
func underLockGuard(dir string, fn func() error) error {
	guard, err := os.OpenFile(filepath.Join(dir, lockGuardFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer guard.Close()
	if err := syscall.Flock(int(guard.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("browser: the profile lock guard: %w", err)
	}
	defer func() { _ = syscall.Flock(int(guard.Fd()), syscall.LOCK_UN) }()
	return fn()
}

// RefreshProfileLock rewrites only a lock this hold still owns: writing over a
// taken-over or released lock would resurrect a claim no Chromium is behind.
func RefreshProfileLock(dir, holder, token string, now time.Time) error {
	written, err := marshalProfileLock(holder, token, now)
	if err != nil {
		return err
	}
	staged, err := stageProfileLock(dir, written)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(staged) }()
	return underLockGuard(dir, func() error {
		path := filepath.Join(dir, LockFile)
		held, err := readProfileLock(path)
		if err != nil {
			return err
		}
		if held.lock.Token != token {
			return fmt.Errorf("%w: %s", ErrLockTakenOver, dir)
		}
		return os.Rename(staged, path)
	})
}

// ReleaseProfileLock removes the lock only while it is still this hold's:
// after a takeover, removing it would let a third process in beside a live
// Chromium.
func ReleaseProfileLock(dir, token string) {
	raw, err := os.ReadFile(filepath.Join(dir, LockFile))
	if err != nil {
		return
	}
	held, err := parseProfileLock(raw)
	if err != nil || held.lock.Token != token {
		return
	}
	_, _ = removeLockIf(dir, raw)
}

type ProfileHold int

const (
	HoldFree ProfileHold = iota
	// HoldAbandoned is a lock nobody has refreshed for LockStale, or one that
	// cannot be read.
	HoldAbandoned
	HoldLive
)

func holdOf(raw []byte, readErr error, now time.Time) ProfileHold {
	if errors.Is(readErr, os.ErrNotExist) {
		return HoldFree
	}
	if readErr != nil {
		return HoldAbandoned
	}
	held, err := parseProfileLock(raw)
	if err != nil {
		return HoldAbandoned
	}
	if now.Sub(held.at) < LockStale {
		return HoldLive
	}
	return HoldAbandoned
}

// ProfileGiveUp is what releasing a profile directory removed.
type ProfileGiveUp struct {
	Lock      bool
	Singleton bool
}

// ReleaseAbandonedProfile removes the lock file and Chromium singleton a
// killed holder left. It answers ErrProfileBusy while anybody is still
// refreshing the lock, and that refusal is the whole safety of this call.
func ReleaseAbandonedProfile(dir string, now time.Time) (ProfileGiveUp, error) {
	raw, err := os.ReadFile(filepath.Join(dir, LockFile))
	switch holdOf(raw, err, now) {
	case HoldLive:
		return ProfileGiveUp{}, fmt.Errorf("%w: something is still driving %s", ErrProfileBusy, dir)
	case HoldFree:
		// No lock, but a killed Chromium can still have left a singleton.
		return ProfileGiveUp{Singleton: ClearSingleton(dir)}, nil
	}
	removed, err := removeLockIf(dir, raw)
	if err != nil {
		return ProfileGiveUp{}, err
	}
	if !removed {
		return ProfileGiveUp{}, fmt.Errorf("%w: %s was claimed while it was being released", ErrProfileBusy, dir)
	}
	return ProfileGiveUp{Lock: true, Singleton: ClearSingleton(dir)}, nil
}

func marshalProfileLock(holder, token string, now time.Time) ([]byte, error) {
	return json.Marshal(profileLock{
		Holder: holder,
		// For a human reading the file; nothing decides on it.
		PID:   os.Getpid(),
		At:    now.UTC().Format(time.RFC3339),
		Token: token,
	})
}

// stageProfileLock writes the bytes beside the lock, to be linked or renamed
// into place: another process catching a write halfway would read a truncated
// lock as abandoned and take a live holder's profile over.
func stageProfileLock(dir string, written []byte) (string, error) {
	file, err := os.CreateTemp(dir, ".profile.lock-*")
	if err != nil {
		return "", err
	}
	if _, err := file.Write(written); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func lockToken() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// The singleton symlinks a Chromium keeps in a user data directory. A killed
// container leaves them naming a host that will never exist again, and the
// next container's Chromium then refuses to launch at all.
var singletonFiles = []string{"SingletonLock", "SingletonCookie", "SingletonSocket"}

// ClearSingleton removes a dead Chromium's singleton and says whether any was
// there. Call it only after a successful claim or an abandoned-profile release,
// which prove no live holder is refreshing the lock: clearing the singleton
// under a live Chromium corrupts the profile. os.Remove, not RemoveAll: these
// are symlinks, and the socket one points into /tmp.
func ClearSingleton(dir string) bool {
	cleared := false
	for _, name := range singletonFiles {
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			cleared = true
		}
	}
	return cleared
}

type heldLock struct {
	lock profileLock
	at   time.Time
}

func readProfileLock(path string) (heldLock, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return heldLock{}, err
	}
	return parseProfileLock(raw)
}

func parseProfileLock(raw []byte) (heldLock, error) {
	var lock profileLock
	if err := json.Unmarshal(raw, &lock); err != nil {
		return heldLock{}, err
	}
	at, err := time.Parse(time.RFC3339, lock.At)
	if err != nil {
		return heldLock{}, err
	}
	return heldLock{lock: lock, at: at}, nil
}

// ForgetProfile removes a connection's profile.
func ForgetProfile(root, profile string) error {
	dir, err := ProfileDir(root, profile)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
