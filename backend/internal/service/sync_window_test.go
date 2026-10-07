package service

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The Bridge warns on every transaction request wider than 45 days.

// requireWindowsCover asserts the bridge was asked about exactly from through
// through, in contiguous windows none of them wider than 45 days.
func requireWindowsCover(t *testing.T, windows []bridgeWindow, from, through domain.Date) {
	t.Helper()
	require.NotEmpty(t, windows, "the bridge was never asked for transactions")
	require.Equal(t, from, windows[0].start, "the read did not start where it should")
	require.Equal(t, through.AddDays(1), windows[len(windows)-1].end,
		"the read did not end today (the end date is exclusive)")
	for i, window := range windows {
		require.LessOrEqual(t, window.days(), 45, "window %d asks for %d days", i, window.days())
		require.Positive(t, window.days(), "window %d is empty", i)
		if i > 0 {
			require.Equal(t, windows[i-1].end, window.start, "windows %d and %d leave a gap or overlap", i-1, i)
		}
	}
}

func TestAFirstSyncBackfillsInWindowsTheBridgeAccepts(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-1", syncDay.AddDays(-200), "-25.00", "SAFEWAY #1234"),
	)))
	f.claim()
	require.Equal(t, 1, f.run().TransactionsImported)

	requireWindowsCover(t, f.bridge.windows, syncDay.AddDays(-365), syncDay)
}

func TestARoutineSyncOfAQuietAccountAsksAboutOneMonth(t *testing.T) {
	// A loan that posted six months ago and nothing since.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Car Loan", "-9000.00",
		bridgeTxn("t-1", syncDay.AddDays(-180), "450.00", "LOAN PAYMENT"),
	)))
	f.claim()
	f.run()

	f.bridge.windows = nil
	report := f.run()
	require.Equal(t, 0, report.TransactionsImported)
	require.Len(t, f.bridge.windows, 1, "a routine sync took more than one request")
	requireWindowsCover(t, f.bridge.windows, syncDay.AddDays(-syncResumeOverlapDays), syncDay)
}

func TestARoutineSyncOfAnAccountWithNoRowsIsNotAYearlyBackfill(t *testing.T) {
	// An account with nothing in it has no newest row, which must not read
	// as "never synced".
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Brokerage", "5000.00")))
	f.claim()
	f.run()

	f.bridge.windows = nil
	f.run()
	requireWindowsCover(t, f.bridge.windows, syncDay.AddDays(-syncResumeOverlapDays), syncDay)
	require.Len(t, f.bridge.windows, 1)
}

func TestASyncAfterALongGapCatchesUpInWindows(t *testing.T) {
	// The server was down for a hundred days. Nothing may be skipped, so the
	// read reaches back to the last one, a window at a time.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-1", syncDay.AddDays(-2), "-25.00", "SAFEWAY #1234"),
	)))
	f.claim()
	f.run()

	f.today = syncDay.AddDays(100)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-1", syncDay.AddDays(-2), "-25.00", "SAFEWAY #1234"),
		bridgeTxn("t-2", syncDay.AddDays(50), "-61.00", "HARDWARE STORE"),
	)))
	f.bridge.windows = nil
	require.Equal(t, 1, f.run().TransactionsImported)
	requireWindowsCover(t, f.bridge.windows, syncDay.AddDays(-syncResumeOverlapDays), f.today)
	require.Len(t, f.bridge.windows, 3)
}

func TestLinkingAnAccountForgetsHowFarAnEarlierLinkRead(t *testing.T) {
	// A relink is a new feed. Resuming from the old one's read would step
	// over the month behind the account's newest row.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claimPending()

	mine := newAccount(t, f.space, "Everyday Checking")
	newTransaction(t, f.space, mine, syncDay.AddDays(-90), "-40.00",
		withStatementName("FUEL STOP #1140"), withSource(domain.SourceSimplifiImport))
	require.NoError(t, db(t).SetAccountSyncedThrough(t.Context(), f.space, mine.ID, syncDay))

	f.finish(link("acc-1", mine.ID))
	stored, err := db(t).GetAccount(t.Context(), f.space, mine.ID)
	require.NoError(t, err)
	require.True(t, stored.SyncedThroughOn.IsZero(), "the old link's read survived the new link")

	f.run()
	requireWindowsCover(t, f.bridge.windows, syncDay.AddDays(-90-syncResumeOverlapDays), syncDay)

	stored, err = db(t).GetAccount(t.Context(), f.space, mine.ID)
	require.NoError(t, err)
	require.Equal(t, syncDay, stored.SyncedThroughOn)
}
