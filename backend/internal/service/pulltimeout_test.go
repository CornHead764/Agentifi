package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A pull whose engine answers just as the pull's bound runs out: what follows
// the answer is all store writes, under a deadline that has already passed.
// The connection must still say how the pull ended.

// lateBillsAgent answers each pull only once the pull's context is done.
type lateBillsAgent struct {
	fakeBillsAgent
}

func (a *lateBillsAgent) Pull(
	ctx context.Context, request provider.BillPullRequest,
) (provider.BillPull, error) {
	<-ctx.Done()
	return a.fakeBillsAgent.Pull(context.WithoutCancel(ctx), request)
}

func TestABillPullThatRunsOutOfTimeAfterTheEngineAnswersIsStillRecorded(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerAlliant)
	fixture.bills.Agent = &lateBillsAgent{fakeBillsAgent: fakeBillsAgent{answers: []map[string]any{
		okPull(`{"token":"rolled-two"}`, pulledStatement("stmt-1", "2026-10-26", "120.00")),
	}}}
	fixture.bills.PullTimeout = 20 * time.Millisecond

	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullFailed, result.Status)

	ran, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullFailed, ran.LastPullStatus)
	require.Contains(t, ran.LastPullError, "did not finish within")
}

func TestABillSignInWallMetAtTheDeadlineIsRecordedAndPaused(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerAlliant)
	fixture.bills.Agent = &lateBillsAgent{fakeBillsAgent: fakeBillsAgent{answers: []map[string]any{{
		"needs_sign_in": true, "password_refused": true,
		"reason": "Alliant Energy did not accept the kept password",
	}}}}
	fixture.bills.PullTimeout = 20 * time.Millisecond

	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullNeedsSignIn, result.Status)

	ran, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullNeedsSignIn, ran.LastPullStatus)
	require.Equal(t, provider.SignInPausedPasswordRefused, ran.SignInPausedFor,
		"the scheduler must not try a refused password again")
}

// lateMerchantAgent fetches only once the pull's context is done, and then
// either answers or, when hang is set, gives up with the context's error.
type lateMerchantAgent struct {
	*fakeMerchantAgent
	hang bool
}

func (a *lateMerchantAgent) Fetch(
	ctx context.Context, merchant domain.MerchantID, storageState json.RawMessage,
	sinceDays int, skipDetails, invoiced, refundChecks []string, credential *provider.MerchantCredential,
) (provider.MerchantFetchResult, error) {
	<-ctx.Done()
	if a.hang {
		return provider.MerchantFetchResult{}, ctx.Err()
	}
	return a.fakeMerchantAgent.Fetch(context.WithoutCancel(ctx), merchant, storageState,
		sinceDays, skipDetails, invoiced, refundChecks, credential)
}

func TestAMerchantPullThatRunsOutOfTimeIsStillRecorded(t *testing.T) {
	for name, tc := range map[string]struct {
		hang   bool
		detail string
	}{
		"while the engine is fetching": {hang: true, detail: "did not answer in time"},
		"after the engine answers":     {hang: false, detail: "did not finish in time"},
	} {
		t.Run(name, func(t *testing.T) {
			agent := &fakeMerchantAgent{signIn: provider.MerchantSignInSignedIn}
			merchants, st, space, account := merchantsWithAgent(t, agent)
			state, err := merchants.StartSignIn(t.Context(), space, account.ID, "alex@example.com", "kept", "", "")
			require.NoError(t, err)
			require.NoError(t, merchants.CompleteSignIn(t.Context(), space, account.ID, state.SessionID, ""))
			merchants.Agent = &lateMerchantAgent{fakeMerchantAgent: agent, hang: tc.hang}

			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			_, err = merchants.pull(ctx, space, account.ID, 30, true)
			require.Error(t, err)

			ran := accountNow(t, st, space, account)
			require.Equal(t, store.MerchantSyncFailed, ran.LastSyncStatus)
			require.Contains(t, ran.LastSyncError, tc.detail)
		})
	}
}
