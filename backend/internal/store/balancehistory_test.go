package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// §12: a materialized value needs a periodic full rebuild that produces
// identical output. These tests check that for the daily balance history.

func snapshotAccount(t *testing.T, spaceID SpaceID) *Account {
	t.Helper()
	account := newAccount(t, spaceID, "Snapshot Checking")
	return account
}

func TestRebuiltHistoryMatchesWhatTheLedgerDerives(t *testing.T) {
	space := newSpace(t)
	account := snapshotAccount(t, space)
	account.OpeningBalance = domain.MustFromString("500")
	require.NoError(t, db(t).UpdateAccount(t.Context(), space, account))

	mustTxn := func(on domain.Date, amount string) {
		row := &Transaction{
			AccountID: account.ID, Date: on,
			Amount: domain.MustFromString(amount), Currency: "USD",
			StatementName: "X", Payee: "X", Source: domain.SourceManual,
		}
		require.NoError(t, db(t).CreateTransaction(t.Context(), space, row))
	}
	first := domain.NewDate(2026, time.August, 1)
	mustTxn(first.AddDays(0), "-100")
	mustTxn(first.AddDays(2), "-40")
	mustTxn(first.AddDays(5), "25")

	from := first
	through := first.AddDays(6)

	written, err := db(t).RebuildBalanceHistory(t.Context(), space, from, through)
	require.NoError(t, err)
	require.Equal(t, 7, written)

	history, err := db(t).ListBalanceHistory(t.Context(), space, through)
	require.NoError(t, err)
	require.NotEmpty(t, history)

	postings, err := db(t).LoadPostings(t.Context(), space, TransactionQuery{})
	require.NoError(t, err)

	domainAccount := DomainAccount(*account)
	for _, point := range history {
		if point.AccountID != domain.ID(account.ID.String()) {
			continue
		}
		require.Equal(t,
			domain.BalanceAsOf(domainAccount, postings, point.On, domain.DatePosted),
			point.Balance,
			"the materialized figure for %s must be what the ledger derives", point.On)
	}
}

func TestARebuildOverwritesDriftedRowsExactly(t *testing.T) {
	space := newSpace(t)
	account := snapshotAccount(t, space)
	// Held since July, so August 10 is inside its history.
	account.OpeningBalanceOn = domain.NewDate(2026, time.July, 1)
	require.NoError(t, db(t).UpdateAccount(t.Context(), space, account))

	on := domain.NewDate(2026, time.August, 10)
	// A wrong row, which the rebuild must repair.
	require.NoError(t, db(t).UpsertBalanceSnapshots(t.Context(), space, []BalanceSnapshot{
		{AccountID: account.ID, AsOf: on, Balance: domain.MustFromString("99999")},
	}))

	_, err := db(t).RebuildBalanceHistory(t.Context(), space, on, on)
	require.NoError(t, err)

	history, err := db(t).ListBalanceHistory(t.Context(), space, on)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, domain.Zero.Round().String(), history[0].Balance.String())
}

func TestTheIncrementalRowAgreesWithTheRebuildOnTheSameDay(t *testing.T) {
	space := newSpace(t)
	account := snapshotAccount(t, space)

	txnDate := domain.NewDate(2026, time.August, 3)
	row := &Transaction{
		AccountID: account.ID, Date: txnDate,
		Amount: domain.MustFromString("-60"), Currency: "USD",
		StatementName: "X", Payee: "X", Source: domain.SourceManual,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, row))
	// Dated after the snapshot day: a current-balance snapshot would count it
	// and the rebuild would not.
	future := &Transaction{
		AccountID: account.ID, Date: txnDate.AddDays(10),
		Amount: domain.MustFromString("-500"), Currency: "USD",
		StatementName: "Y", Payee: "Y", Source: domain.SourceManual,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, future))

	today := txnDate.AddDays(1)
	n, err := db(t).SnapshotAccounts(t.Context(), space, today)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 1)

	appended, err := db(t).ListBalanceHistory(t.Context(), space, today)
	require.NoError(t, err)

	// The rebuild and the nightly pass must agree over the same ledger.
	_, err = db(t).RebuildBalanceHistory(t.Context(), space, today, today)
	require.NoError(t, err)

	rebuilt, err := db(t).ListBalanceHistory(t.Context(), space, today)
	require.NoError(t, err)
	require.Equal(t,
		balanceOfAccount(t, appended, account.ID),
		balanceOfAccount(t, rebuilt, account.ID))
	require.Equal(t, "-60.00", balanceOfAccount(t, rebuilt, account.ID))
}

func TestAnOldRowFollowsATransactionDeletedAfterItWasWritten(t *testing.T) {
	// A card at -300.00 by the provider: -100.00 on the 2nd, a duplicate
	// -50.00 on the 4th, a 20.00 refund on the 5th. Written with the duplicate
	// on file, the 2nd and 3rd are -300 less (-50 + 20) = -270. With it
	// deleted they are -300 less 20 = -320; from the 4th on nothing moved,
	// and the history starts at the first row, on the 2nd.
	space := newSpace(t)
	card := &Account{
		Name: "Card 1", Kind: domain.KindCreditCard, Type: "credit_card", Currency: "USD",
		IncludeInNetWorth: true,
		ProviderBalance:   domain.MustFromString("-300.00"), HasProviderBalance: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, card))
	first := domain.NewDate(2025, time.August, 1)
	walletRow(t, space, card.ID, first.AddDays(1), "-100.00")
	duplicate := &Transaction{
		AccountID: card.ID, Date: first.AddDays(3), Amount: domain.MustFromString("-50.00"),
		Currency: "USD", StatementName: "X", Payee: "X", Source: domain.SourceSync,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, duplicate))
	walletRow(t, space, card.ID, first.AddDays(4), "20.00")

	through := first.AddDays(5)
	_, err := db(t).RebuildBalanceHistory(t.Context(), space, first, through)
	require.NoError(t, err)
	imported := first.AddDays(7)
	_, err = db(t).db.Exec(t.Context(), `
		INSERT INTO balance_snapshots (id, account_id, as_of, balance, space_id, is_imported)
		VALUES ($1, $2, $3, 0, $4, true)`,
		uuid.New(), card.ID, imported.Time(), space.UUID())
	require.NoError(t, err)
	require.Equal(t, "-270.00", accountHistory(t, space, card.ID, through)["2025-08-03"])

	require.NoError(t, db(t).DeleteTransaction(t.Context(), space, duplicate.ID))
	moved, err := db(t).RederiveBalanceHistory(t.Context(), space)
	require.NoError(t, err)
	require.Equal(t, 2, moved)

	require.Equal(t, map[string]string{
		"2025-08-02": "-320.00",
		"2025-08-03": "-320.00",
		"2025-08-04": "-320.00",
		"2025-08-05": "-300.00",
		"2025-08-06": "-300.00",
		"2025-08-08": "0.00",
	}, accountHistory(t, space, card.ID, imported))

	again, err := db(t).RederiveBalanceHistory(t.Context(), space)
	require.NoError(t, err)
	require.Zero(t, again, "a second pass over an unchanged ledger moves nothing")
}

func balanceOfAccount(
	t *testing.T, history []domain.BalancePoint, accountID uuid.UUID,
) string {
	t.Helper()
	for _, point := range history {
		if point.AccountID == domain.ID(accountID.String()) {
			return point.Balance.String()
		}
	}
	t.Fatalf("no history row for account %s", accountID)
	return ""
}
