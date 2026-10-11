package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/buildinfo"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Whether a newer release is out. The answer is kept for updateCheckEvery,
// so an administrator opening the app does not ask GitHub each time, and
// anonymous callers share an hourly limit there.

const updateCheckEvery = 6 * time.Hour

type AdminUpdateResponse struct {
	// Enabled is false when UPDATE_CHECK_ENABLED is off; nothing was asked.
	Enabled bool `json:"enabled"`
	// CurrentVersion is empty for a build that was not told its version.
	CurrentVersion string `json:"current_version"`
	// LatestVersion is the newest release tag, "v0.3.0"; empty until a check
	// succeeds.
	LatestVersion   string     `json:"latest_version"`
	UpdateAvailable bool       `json:"update_available"`
	URL             string     `json:"url"`
	CheckedAt       *time.Time `json:"checked_at"`
	// Error is why the last check failed. The last good answer, if any, is
	// still the one shown.
	Error string `json:"error"`
}

type updateCache struct {
	mu        sync.Mutex
	release   provider.Release
	checkedAt time.Time
	err       string
}

// runningVersion is what this binary reports as its release.
func (e *Env) runningVersion() string {
	if e.Version != "" {
		return e.Version
	}
	return buildinfo.Read().Version
}

func readUpdate(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	out := AdminUpdateResponse{
		Enabled:        env.Live().UpdateCheckEnabled,
		CurrentVersion: env.runningVersion(),
	}
	if !out.Enabled || env.Releases == nil {
		out.Enabled = false
		return writeJSON(w, http.StatusOK, out)
	}
	cache := &env.updates
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := env.now()
	if r.URL.Query().Get("refresh") == "1" || cache.checkedAt.IsZero() ||
		now.Sub(cache.checkedAt) >= updateCheckEvery {
		release, err := env.Releases.Latest(context.WithoutCancel(r.Context()))
		cache.checkedAt = now
		if err != nil {
			cache.err = err.Error()
		} else {
			cache.release, cache.err = release, ""
		}
	}
	checked := cache.checkedAt
	out.CheckedAt = &checked
	out.Error = cache.err
	out.LatestVersion = cache.release.Version
	out.URL = cache.release.URL
	out.UpdateAvailable = domain.UpdateAvailable(out.CurrentVersion, out.LatestVersion)
	return writeJSON(w, http.StatusOK, out)
}
