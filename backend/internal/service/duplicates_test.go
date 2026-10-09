package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A Simplifi import followed by a SimpleFIN link. The sync floor stops the
// bank re-creating history before the newest imported row, but a charge the
// bank posts a day after Simplifi dated it, under its own wording, is on the
// right side of the floor and matches nothing by content. It lands twice.

// linkedAfterImport is an account holding one imported charge, linked to the
// bank with a bridge that reports the same charge posted a day later.
func linkedAfterImport(t *testing.T) (*syncFixture, store.Account, store.Transaction) {
	t.Helper()
	f := newSyncFixture(t)
	f.sync.Ingest = Ingest{Duplicates: NewDuplicates(db(t))}

	account := &store.Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), f.space, account))
	imported := newTransaction(t, f.space, account, on(2026, time.March, 15), "-25.00",
		withStatementName("Safeway"), withSource(domain.SourceSimplifiImport))

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 16), "-25.00", "POS DEBIT SAFEWAY #1234"),
	)))
	f.claimPending()
	f.finish(link("acc-1", account.ID))
	return f, *account, *imported
}

func openPairs(t *testing.T, f *syncFixture) []store.DuplicateCandidate {
	t.Helper()
	pairs, err := db(t).ListOpenDuplicates(t.Context(), f.space)
	require.NoError(t, err)
	return pairs
}

func TestTheFloorLetsABankCopyThroughWhenTheBankPostsADayLateAndTheSyncProposesIt(t *testing.T) {
	f, account, imported := linkedAfterImport(t)
	require.Equal(t, imported.Date, mustAccount(t, f, account.ID).SyncFloorOn,
		"the floor is the newest imported day")
	f.run()

	rows := f.transactions()
	require.Len(t, rows, 2, "the bank's copy is dated after the floor and reads differently")

	pairs := openPairs(t, f)
	require.Len(t, pairs, 1)
	require.Equal(t, account.ID, pairs[0].AccountID)
	require.Equal(t, 1, pairs[0].DaysApart)
}

func TestKeepingTheImportedCopyLetsTheNextSyncSettleItByTheBanksId(t *testing.T) {
	f, _, imported := linkedAfterImport(t)
	f.run()
	pair := openPairs(t, f)[0]

	retired, err := NewDuplicates(db(t)).MarkDuplicate(t.Context(), f.space, uuid.Nil, pair.ID, uuid.Nil)
	require.NoError(t, err)
	require.Equal(t, imported.ID, retired.KeptID)

	require.Equal(t, 0, f.run().TransactionsImported, "the charge is not written a third time")
	live := 0
	for _, row := range f.transactions() {
		live++
		require.Equal(t, imported.ID, row.ID)
		require.Equal(t, "t1", row.ExternalID)
	}
	require.Equal(t, 1, live)
	require.Empty(t, openPairs(t, f))
}

func TestARulingOfDistinctSurvivesTheNextSync(t *testing.T) {
	f, _, _ := linkedAfterImport(t)
	f.run()
	pair := openPairs(t, f)[0]

	require.NoError(t, NewDuplicates(db(t)).MarkDistinct(t.Context(), f.space, uuid.Nil, pair.ID))
	f.run()
	_, err := NewDuplicates(db(t)).DetectSpace(t.Context(), f.space)
	require.NoError(t, err)

	require.Empty(t, openPairs(t, f))
	require.Len(t, f.transactions(), 2)
}

func TestAPairCannotBeDecidedTwice(t *testing.T) {
	f, _, _ := linkedAfterImport(t)
	f.run()
	pair := openPairs(t, f)[0]
	duplicates := NewDuplicates(db(t))

	require.NoError(t, duplicates.MarkDistinct(t.Context(), f.space, uuid.Nil, pair.ID))
	require.ErrorIs(t, duplicates.MarkDistinct(t.Context(), f.space, uuid.Nil, pair.ID), ErrDuplicateDecided)
	_, err := duplicates.MarkDuplicate(t.Context(), f.space, uuid.Nil, pair.ID, uuid.Nil)
	require.ErrorIs(t, err, ErrDuplicateDecided)
}

func TestAPairWhoseRowWasDeletedMeanwhileCannotBeRetired(t *testing.T) {
	f, _, imported := linkedAfterImport(t)
	f.run()
	pair := openPairs(t, f)[0]
	require.NoError(t, db(t).DeleteTransaction(t.Context(), f.space, imported.ID))

	require.Empty(t, openPairs(t, f), "a pair with a deleted row is no longer asked about")
	_, err := NewDuplicates(db(t)).MarkDuplicate(t.Context(), f.space, uuid.Nil, pair.ID, uuid.Nil)
	require.ErrorIs(t, err, ErrDuplicateGone)
}

func TestTheKeptRowMustBeOneOfThePair(t *testing.T) {
	f, _, _ := linkedAfterImport(t)
	f.run()
	pair := openPairs(t, f)[0]

	_, err := NewDuplicates(db(t)).MarkDuplicate(t.Context(), f.space, uuid.Nil, pair.ID, uuid.New())
	require.ErrorIs(t, err, ErrNotInPair)
	require.Len(t, f.transactions(), 2)
}

func mustAccount(t *testing.T, f *syncFixture, id uuid.UUID) store.Account {
	t.Helper()
	account, err := db(t).GetAccount(t.Context(), f.space, id)
	require.NoError(t, err)
	return account
}
