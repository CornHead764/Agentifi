package api

import (
	"context"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/buildinfo"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Whether a newer release is out. The answer is kept for updateCheckEvery,
// so an administrator opening the app does not ask GitHub each time, and
// anonymous callers share an hourly limit there.

const updateCheckEvery = 6 * time.Hour

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

func (s adminService) GetUpdate(ctx context.Context, req *agentifiv1.GetUpdateRequest) (*agentifiv1.GetUpdateResponse, error) {
	env := s.env
	out := &agentifiv1.GetUpdateResponse{
		Enabled:        env.Live().UpdateCheckEnabled,
		CurrentVersion: env.runningVersion(),
	}
	if !out.Enabled || env.Releases == nil {
		out.Enabled = false
		return out, nil
	}
	cache := &env.updates
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := env.now()
	if req.GetRefresh() || cache.checkedAt.IsZero() || now.Sub(cache.checkedAt) >= updateCheckEvery {
		release, err := env.Releases.Latest(context.WithoutCancel(ctx))
		cache.checkedAt = now
		if err != nil {
			cache.err = err.Error()
		} else {
			cache.release, cache.err = release, ""
		}
	}
	out.CheckedAt = timestamppb.New(cache.checkedAt)
	out.Error = cache.err
	out.LatestVersion = cache.release.Version
	out.Url = cache.release.URL
	out.UpdateAvailable = domain.UpdateAvailable(out.CurrentVersion, out.LatestVersion)
	return out, nil
}
