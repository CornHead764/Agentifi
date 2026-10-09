package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// pullClaims is which connections have a pull running in this process.
// Process-wide because a service is built per request. Two pulls of one
// connection would be two browsers on one profile, which the engine refuses.
type pullClaims struct{ running sync.Map }

var (
	billPulls     pullClaims
	merchantPulls pullClaims
)

func (c *pullClaims) claim(id uuid.UUID) bool {
	_, taken := c.running.LoadOrStore(id, struct{}{})
	return !taken
}

func (c *pullClaims) release(id uuid.UUID) { c.running.Delete(id) }

func (c *pullClaims) held(id uuid.UUID) bool {
	_, held := c.running.Load(id)
	return held
}

// ErrPullRunning is a pull asked for while one of the same connection is
// already running here.
var ErrPullRunning = errors.New("a pull of this is already running; it will say how it went when it finishes")

func BillPullRunning(id uuid.UUID) bool { return billPulls.held(id) }

// MerchantPullRunning is a pull of the account running here; an invoice
// backfill holds the same claim but is not a pull.
func MerchantPullRunning(id uuid.UUID) bool {
	if _, backfilling := MerchantBackfillRunning(id); backfilling {
		return false
	}
	return merchantPulls.held(id)
}

// PullProgress is what a running merchant pull is doing: the line its module
// last reported, when the pull began and when the line last changed.
type PullProgress struct {
	Line      string
	StartedAt time.Time
	UpdatedAt time.Time
}

// merchantPullProgress is the progress of each account's running pull in this
// process, beside the claim it holds.
var merchantPullProgress sync.Map

// MerchantPullProgress is the progress of the account's running pull, if one
// has begun reporting.
func MerchantPullProgress(id uuid.UUID) (PullProgress, bool) {
	found, ok := merchantPullProgress.Load(id)
	if !ok {
		return PullProgress{}, false
	}
	return found.(PullProgress), true
}

// trackMerchantPull has the pull's engine and module report into
// MerchantPullProgress; the returned func ends the tracking.
func trackMerchantPull(ctx context.Context, id uuid.UUID) (context.Context, func()) {
	started := time.Now()
	hear := func(line string) {
		merchantPullProgress.Store(id, PullProgress{Line: line, StartedAt: started, UpdatedAt: time.Now()})
	}
	hear("Starting the update")
	return provider.WithPullProgress(ctx, hear), func() { merchantPullProgress.Delete(id) }
}

// pullTimeout bounds a pull nobody is waiting on: the background pull a
// sign-in starts, and an Update now whose page went away. Without it a
// provider that never answers would hold the claim for the process's life.
const pullTimeout = 10 * time.Minute

// inBackground runs a pull whose claim the caller holds, on its own context,
// since the starting request will have answered long before.
func (c *pullClaims) inBackground(id uuid.UUID, run func(ctx context.Context)) {
	c.inBackgroundFor(id, pullTimeout, run)
}

// inBackgroundFor is inBackground with its own bound.
func (c *pullClaims) inBackgroundFor(id uuid.UUID, timeout time.Duration, run func(ctx context.Context)) {
	go func() {
		defer c.release(id)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		run(ctx)
	}()
}
