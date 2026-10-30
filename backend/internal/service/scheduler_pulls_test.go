package service

import (
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestSchedulerPullsOffSkipsBillPulls(t *testing.T) {
	pulls := func(fixture bridgeFixture) int {
		fixture.agent.mu.Lock()
		defer fixture.agent.mu.Unlock()
		return len(fixture.agent.factors)
	}
	for _, off := range []bool{true, false} {
		fixture := newBridgeFixture(t, domain.BillerAlliant)
		scheduler := &Scheduler{
			Store: fixture.bills.store, Bills: fixture.bills, PullsOff: off,
			Now: fixture.bills.now, Log: quietLogger(),
		}
		scheduler.RunDue(t.Context())
		if off {
			require.Zero(t, pulls(fixture), "PullsOff ran a bill pull")
		} else {
			require.Positive(t, pulls(fixture), "the due connection was not pulled")
		}
	}
}
