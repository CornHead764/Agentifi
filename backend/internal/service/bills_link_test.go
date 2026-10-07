package service

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func TestLinkHealthReadsTheConnectionNotTheBill(t *testing.T) {
	bridged := store.BillConnection{Biller: domain.BillerAlliant, HasSession: true}
	require.Equal(t, LinkHealthy, LinkHealth(bridged))

	bridged.LastPullStatus = store.BillPullChallenge
	require.Equal(t, LinkChallenge, LinkHealth(bridged))
	bridged.LastPullStatus = store.BillPullFailed
	require.Equal(t, LinkFailed, LinkHealth(bridged))

	// A refused or missing session outranks whatever the last pull said.
	bridged.LastPullStatus = store.BillPullOK
	bridged.NeedsSignIn = true
	require.Equal(t, LinkNeedsSignIn, LinkHealth(bridged))
	bridged.NeedsSignIn = false
	bridged.HasSession = false
	require.Equal(t, LinkNotConnected, LinkHealth(bridged))

	// A provider nobody signs in to is never "not connected".
	mailed := store.BillConnection{Biller: domain.BillerApple}
	require.Equal(t, LinkHealthy, LinkHealth(mailed))
	mailed.LastPullStatus = store.BillPullFailed
	require.Equal(t, LinkFailed, LinkHealth(mailed))
}
