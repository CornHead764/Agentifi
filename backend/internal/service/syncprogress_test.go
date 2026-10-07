package service

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The match screen's Finish and the sync's progress. Finish writes every
// account the screen decided on before any transaction is read, and a sync
// says what it is doing while it runs.

func TestFinishingAppliesEveryChoiceBeforeAnySync(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("",
		bridgeAccount("acc-1", "CHECKING", "1200.00",
			bridgeTxn("t-1", syncDay, "-12.50", "FUEL STOP #1140")),
		bridgeAccount("acc-2", "Joint Savings", "300.00",
			bridgeTxn("t-2", syncDay, "5.00", "INTEREST")),
		bridgeAccount("acc-3", "Coin Wallet", "0.12")))
	f.claimPending()

	mine := newAccount(t, f.space, "Everyday Checking")
	imported := syncDay.AddDays(-10)
	newTransaction(t, f.space, mine, imported, "-40.00",
		withStatementName("GROCER #12"), withSource(domain.SourceSimplifiImport))

	connection := f.finish(link("acc-1", mine.ID), LinkChoice{ExternalID: "acc-2", Action: LinkAsNew}, ignore("acc-3"))
	require.Equal(t, store.ConnectionActive, connection.Status)

	byName := map[string]store.Account{}
	for _, account := range f.accounts() {
		byName[account.Name] = account
	}
	require.Len(t, byName, 2, "the ignored account was created, or the paired one duplicated")

	adopted := byName["Everyday Checking"]
	require.Equal(t, mine.ID, adopted.ID)
	require.Equal(t, f.connect, adopted.ConnectionID, "the pairing waits for a sync")
	require.Equal(t, "acc-1", adopted.ExternalID)
	require.Equal(t, imported, adopted.SyncFloorOn, "the floor is the imported history's newest day")

	created, ok := byName["Joint Savings"]
	require.True(t, ok, "the new account waits for a sync")
	require.Equal(t, f.connect, created.ConnectionID)
	require.Equal(t, "300.00", created.ProviderBalance.String())

	ignored, err := db(t).ListIgnoredRemoteAccounts(t.Context(), f.space, f.connect)
	require.NoError(t, err)
	require.Len(t, ignored, 1)
	require.Equal(t, "acc-3", ignored[0].ExternalID)
	require.Equal(t, "Coin Wallet", ignored[0].Name)

	require.Len(t, f.transactions(), 1, "finishing imported transactions")

	report := f.run()
	require.Equal(t, 2, report.TransactionsImported)
	require.Zero(t, report.AccountsCreated, "the sync created what Finish already had")
	require.Len(t, f.accounts(), 2)
}

func TestFinishingWritesNothingWhenOneChoiceIsRefused(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("",
		bridgeAccount("acc-1", "CHECKING", "1200.00"),
		bridgeAccount("acc-2", "Joint Savings", "300.00"),
		bridgeAccount("acc-3", "Coin Wallet", "0.12")))
	f.claimPending()
	mine := newAccount(t, f.space, "Everyday Checking")

	_, err := f.sync.FinishLinking(t.Context(), f.space, f.connect,
		[]LinkChoice{ignore("acc-3"), link("acc-1", mine.ID), link("acc-2", mine.ID)})
	var refused LinkRefused
	require.ErrorAs(t, err, &refused)

	_, err = f.sync.FinishLinking(t.Context(), f.space, f.connect,
		[]LinkChoice{ignore("acc-3"), link("acc-9", mine.ID)})
	require.ErrorAs(t, err, &refused, "an account the Bridge does not reach was accepted")

	gone := newAccount(t, f.space, "Old Checking")
	require.NoError(t, db(t).DeleteAccount(t.Context(), f.space, gone.ID))
	_, err = f.sync.FinishLinking(t.Context(), f.space, f.connect,
		[]LinkChoice{ignore("acc-3"), link("acc-1", gone.ID)})
	require.ErrorAs(t, err, &refused, "a deleted account was fed")

	require.Equal(t, store.ConnectionPendingLink, f.connection().Status)
	require.Len(t, f.accounts(), 2)
	ignored, err := db(t).ListIgnoredRemoteAccounts(t.Context(), f.space, f.connect)
	require.NoError(t, err)
	require.Empty(t, ignored, "half the choices were written")
}

func TestRematchingPointsTheFeedAtTheChosenAccount(t *testing.T) {
	// A connection that made its own account for something the household
	// already kept is re-matched; its copy is kept, detached, with its rows.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "CHECKING", "1200.00",
		bridgeTxn("t-1", syncDay, "-12.50", "FUEL STOP #1140"))))
	f.claim()
	f.run()
	made := f.accounts()[0]

	mine := newAccount(t, f.space, "Everyday Checking")
	f.finish(link("acc-1", mine.ID))

	stored, err := db(t).GetAccount(t.Context(), f.space, mine.ID)
	require.NoError(t, err)
	require.Equal(t, f.connect, stored.ConnectionID)
	require.Equal(t, "acc-1", stored.ExternalID)
	copyOf, err := db(t).GetAccount(t.Context(), f.space, made.ID)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, copyOf.ConnectionID, "two accounts are fed by one at the bank")
}

func TestFinishingAgainLeavesAnAccountItAlreadyFeedsAlone(t *testing.T) {
	// Linking again would move the floor up to the newest row and refuse a
	// charge the bank posts late.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "CHECKING", "1200.00",
		bridgeTxn("t-1", syncDay, "-12.50", "FUEL STOP #1140"))))
	f.claim()
	f.run()
	fed := f.accounts()[0]

	f.finish(link("acc-1", fed.ID))

	stored, err := db(t).GetAccount(t.Context(), f.space, fed.ID)
	require.NoError(t, err)
	require.Equal(t, fed.SyncFloorOn, stored.SyncFloorOn)
	require.Equal(t, fed.SyncedThroughOn, stored.SyncedThroughOn)
}

func TestASyncSaysWhatItIsDoingAndHowItEnded(t *testing.T) {
	f := newSyncFixture(t)
	body := payload("",
		bridgeAccount("acc-1", "Everyday Checking", "1200.00",
			bridgeTxn("t-1", syncDay, "-12.50", "FUEL STOP #1140"),
			bridgeTxn("t-2", syncDay, "-3.00", "PARKING")),
		bridgeAccount("acc-2", "Joint Savings", "300.00",
			bridgeTxn("t-3", syncDay, "5.00", "INTEREST")))
	f.bridge.serve(body)
	f.claim()

	var mu sync.Mutex
	seen := map[string]SyncProgress{}
	f.bridge.answer = func() (int, string) {
		if progress, ok := SyncProgressOf(f.connect); ok && progress.Phase == SyncImporting {
			mu.Lock()
			seen[progress.AccountName] = progress
			mu.Unlock()
		}
		return http.StatusOK, body
	}
	f.run()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 2)
	first, second := seen["Everyday Checking"], seen["Joint Savings"]
	require.Equal(t, SyncRunning, first.State)
	require.Equal(t, 2, first.Accounts)
	require.Equal(t, 1, first.Account)
	require.Equal(t, 2, second.Account)
	require.Equal(t, 2, second.TransactionsImported, "the first account's rows were not counted as they landed")

	done, ok := SyncProgressOf(f.connect)
	require.True(t, ok)
	require.Equal(t, SyncSucceeded, done.State)
	require.Empty(t, done.Phase)
	require.Equal(t, 2, done.Accounts)
	require.Equal(t, 3, done.TransactionsImported)
	require.NotNil(t, done.FinishedAt)
}

func TestASyncStartsInTheBackgroundAndASecondJoinsIt(t *testing.T) {
	f := newSyncFixture(t)
	body := payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-1", syncDay, "-12.50", "FUEL STOP #1140")))
	f.bridge.serve(body)
	f.claim()

	gate := make(chan struct{})
	reading := make(chan struct{}, 1)
	f.bridge.answer = func() (int, string) {
		select {
		case reading <- struct{}{}:
		default:
		}
		<-gate
		return http.StatusOK, body
	}

	started, err := f.sync.StartSync(t.Context(), f.space, f.connect)
	require.NoError(t, err)
	require.Equal(t, SyncRunning, started.State)
	<-reading

	joined, err := f.sync.StartSync(t.Context(), f.space, f.connect)
	require.NoError(t, err)
	require.Equal(t, SyncRunning, joined.State)
	require.Equal(t, started.StartedAt, joined.StartedAt, "a second run was started beside the first")

	blocking, err := f.sync.SyncConnection(t.Context(), f.space, f.connect)
	require.NoError(t, err)
	require.True(t, blocking.Skipped, "the scheduler ran beside a sync already going")

	close(gate)
	require.Eventually(t, func() bool {
		progress, _ := SyncProgressOf(f.connect)
		return progress.State != SyncRunning
	}, 10*time.Second, 10*time.Millisecond)
	progress, _ := SyncProgressOf(f.connect)
	require.Equal(t, SyncSucceeded, progress.State)
	require.Equal(t, 1, progress.TransactionsImported)
	require.Len(t, f.transactions(), 1)
}

func TestAFailedSyncSaysWhyAndTheNextOneRuns(t *testing.T) {
	f := newSyncFixture(t)
	f.claim()
	f.bridge.serveStatus(http.StatusInternalServerError)

	_, err := f.sync.SyncConnection(t.Context(), f.space, f.connect)
	require.Error(t, err)
	failed, ok := SyncProgressOf(f.connect)
	require.True(t, ok)
	require.Equal(t, SyncFailed, failed.State)
	require.NotEmpty(t, failed.Message)

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.run()
	retried, _ := SyncProgressOf(f.connect)
	require.Equal(t, SyncSucceeded, retried.State)
	require.Empty(t, retried.Message)
}

func TestARefusedCredentialEndsTheRunAsAFailure(t *testing.T) {
	f := newSyncFixture(t)
	f.claim()
	f.bridge.serveStatus(http.StatusForbidden)

	report := f.run()
	require.Equal(t, store.ConnectionCredentialsExpired, report.Status)
	progress, _ := SyncProgressOf(f.connect)
	require.Equal(t, SyncFailed, progress.State)
	require.Equal(t, report.StatusDetail, progress.Message)
}

func TestAnUnmatchedConnectionIsSkippedWithoutStarting(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claimPending()
	before := f.bridge.accountCalls

	progress, err := f.sync.StartSync(t.Context(), f.space, f.connect)
	require.NoError(t, err)
	require.Equal(t, SyncSkipped, progress.State)
	require.Contains(t, progress.Message, "Match")
	require.Equal(t, before, f.bridge.accountCalls)
}
