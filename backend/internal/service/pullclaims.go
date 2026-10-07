package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
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
