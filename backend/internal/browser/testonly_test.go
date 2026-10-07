package browser

import (
	"os"
	"path/filepath"
	"time"
)

// ReadProfileHold decides on the same rule as TakeProfileLock.
func ReadProfileHold(dir string, now time.Time) ProfileHold {
	raw, err := os.ReadFile(filepath.Join(dir, LockFile))
	return holdOf(raw, err, now)
}

func (e *Engine) Held(dir string) bool {
	e.claimsMu.Lock()
	defer e.claimsMu.Unlock()
	_, held := e.claims[dir]
	return held
}

func (v *LiveView) Painted() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.since
}
