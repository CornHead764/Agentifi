package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The pull a finished sign-in starts by itself: one pull, through the path
// Update now takes, never two at once, and a failure that is the pull's and
// not the sign-in's. Every session and figure here is invented.

// signedInBridge is a bridge fixture whose connection was just signed in with
// a session no other test's connection holds, so the pulls of this connection
// can be told from any other the scheduler's pass reaches in the shared schema.
type signedInBridge struct {
	bridgeFixture
	token string
}

func awaitPullClaim(t *testing.T, claims *pullClaims, id uuid.UUID) {
	t.Helper()
	require.Eventually(t, func() bool { return !claims.held(id) },
		5*time.Second, 2*time.Millisecond, "the pull a sign-in started never finished")
}

func pullsOfThisSignIn(fixture signedInBridge) int {
	agent := fixture.agent
	agent.mu.Lock()
	defer agent.mu.Unlock()
	count := 0
	for _, session := range agent.seen {
		if strings.Contains(session, fixture.token) {
			count++
		}
	}
	return count
}

func signedInFixture(t *testing.T) signedInBridge {
	t.Helper()
	fixture := newBridgeFixture(t, domain.BillerAlliant)
	token := "signed-in-" + uuid.NewString()
	require.NoError(t, fixture.bills.store.SaveBillConnectionSession(
		t.Context(), fixture.space, fixture.connection.ID, `{"token":"`+token+`"}`, "profile-1", true))
	fixture.agent.answers = []map[string]any{
		okPull(`{"token":"rolled-two"}`, pulledStatement("stmt-1", "2026-10-26", "120.00")),
	}
	return signedInBridge{bridgeFixture: fixture, token: token}
}

func TestASignInStartsOnePullThroughThePathUpdateNowTakes(t *testing.T) {
	fixture := signedInFixture(t)

	require.True(t, fixture.bills.PullAfterSignIn(fixture.space, fixture.connection.ID))
	awaitPullClaim(t, &billPulls, fixture.connection.ID)

	require.Equal(t, 1, pullsOfThisSignIn(fixture))
	row, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, row.LastPullStatus)
	require.NotNil(t, row.LastPulledAt, "stamped, so the scheduler's next pass leaves it alone")
	bills, err := fixture.bills.store.ListBills(t.Context(), fixture.space, fixture.subaccount.ID)
	require.NoError(t, err)
	require.Len(t, bills, 1, "the bill it found is filed")
	kept, err := fixture.bills.store.BillConnectionSession(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"token":"rolled-two"}`, kept, "the rolled session is kept, as Update now keeps it")
}

func TestAPullAlreadyRunningIsNotStartedTwice(t *testing.T) {
	fixture := signedInFixture(t)
	gate := make(chan struct{})
	fixture.agent.gate = gate

	require.True(t, fixture.bills.PullAfterSignIn(fixture.space, fixture.connection.ID))
	require.True(t, BillPullRunning(fixture.connection.ID))
	require.Eventually(t, func() bool {
		fixture.agent.mu.Lock()
		defer fixture.agent.mu.Unlock()
		return fixture.agent.gate == nil
	}, 5*time.Second, 2*time.Millisecond, "the pull reached the engine")

	// A second sign-in's trigger, Update now and the scheduler's pass — which
	// sees a stamp the sign-in cleared — all find the pull under way.
	require.False(t, fixture.bills.PullAfterSignIn(fixture.space, fixture.connection.ID))
	_, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.ErrorIs(t, err, ErrPullRunning)
	fixture.bills.PullDue(t.Context(), fixture.bills.now().Add(-time.Hour))

	row, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, "", row.LastPullStatus, "a refused second pull records nothing")

	close(gate)
	awaitPullClaim(t, &billPulls, fixture.connection.ID)
	require.Equal(t, 1, pullsOfThisSignIn(fixture))
	require.False(t, BillPullRunning(fixture.connection.ID))

	// Once it has finished, Update now runs.
	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, result.Status)
}

func TestAPullThatFailsAfterASignInIsRecordedAndTheSignInStands(t *testing.T) {
	fixture := signedInFixture(t)
	fixture.agent.fail = errors.New("the provider's statements page did not load")

	require.True(t, fixture.bills.PullAfterSignIn(fixture.space, fixture.connection.ID))
	awaitPullClaim(t, &billPulls, fixture.connection.ID)

	row, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullFailed, row.LastPullStatus)
	require.Contains(t, row.LastPullError, "did not load")
	require.True(t, row.HasSession, "the session the sign-in kept is still kept")
	require.NotNil(t, row.SignedInAt)
	require.False(t, row.NeedsSignIn)
	require.NotEmpty(t, fixture.push.sent, "and the household is told, as for any failed pull")
}

func TestAPullWithTheSessionForgottenAsksForASignIn(t *testing.T) {
	fixture := signedInFixture(t)
	require.NoError(t, fixture.bills.store.ClearBillConnectionSession(t.Context(), fixture.space, fixture.connection.ID))

	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullNeedsSignIn, result.Status)
	row, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.True(t, row.NeedsSignIn, "the pull keeps the flag the forgetting raised")
}

func TestAMerchantPullWithTheSessionForgottenAsksForASignIn(t *testing.T) {
	merchants, st, space, account := merchantsWithAgent(t, &fakeMerchantAgent{})
	require.NoError(t, st.ClearMerchantSession(t.Context(), space, account.ID))

	_, err := merchants.Pull(t.Context(), space, account.ID, 0)
	require.ErrorIs(t, err, ErrMerchantNeedsSignIn)
}

func TestAMerchantSignInStartsOnePullThatReadsAYearBack(t *testing.T) {
	gate := make(chan struct{})
	agent := &fakeMerchantAgent{signIn: provider.MerchantSignInOTP, gate: gate}
	merchants, st, space, account := merchantsWithAgent(t, agent)

	state, err := merchants.StartSignIn(t.Context(), space, account.ID, "alex@example.com", "a-password", "", "")
	require.NoError(t, err)
	_, err = merchants.AnswerSignIn(t.Context(), space, account.ID, state.SessionID, "123456")
	require.NoError(t, err)
	require.NoError(t, merchants.CompleteSignIn(t.Context(), space, account.ID, state.SessionID, ""))

	require.True(t, merchants.PullAfterSignIn(space, account.ID))
	require.True(t, MerchantPullRunning(account.ID))
	require.False(t, merchants.PullAfterSignIn(space, account.ID))
	_, err = merchants.Pull(t.Context(), space, account.ID, 0)
	require.ErrorIs(t, err, ErrPullRunning)

	close(gate)
	awaitPullClaim(t, &merchantPulls, account.ID)
	agent.mu.Lock()
	require.Equal(t, []int{365}, agent.days, "one pull, and the first after a sign-in reads a year")
	agent.mu.Unlock()
	after := accountNow(t, st, space, account)
	require.Equal(t, store.MerchantSyncOK, after.LastSyncStatus)
	require.NotNil(t, after.LastSyncedAt)
}
