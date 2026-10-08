package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The history-start rule (calculations.md §4) as the materialized history
// sees it: rows before an account's start are removed, rows the start newly
// exposes are filled, and an imported row is never deleted.

// linkedToday is a connected wallet created now, holding 28,750.00 by the
// provider and nothing in its ledger.
func linkedToday(t *testing.T, space SpaceID) *Account {
	t.Helper()
	account := &Account{
		Name: "Wallet 1", Kind: domain.KindInvestment, Type: "crypto", Currency: "USD",
		IncludeInNetWorth: true,
		ProviderBalance:   domain.MustFromString("28750.00"), HasProviderBalance: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, account))
	return account
}

func accountHistory(t *testing.T, space SpaceID, accountID uuid.UUID, through domain.Date) map[string]string {
	t.Helper()
	history, err := db(t).ListBalanceHistory(t.Context(), space, through)
	require.NoError(t, err)
	out := map[string]string{}
	for _, point := range history {
		if point.AccountID == domain.ID(accountID.String()) {
			out[point.On.String()] = point.Balance.String()
		}
	}
	return out
}

func walletRow(t *testing.T, space SpaceID, accountID uuid.UUID, on domain.Date, amount string) {
	t.Helper()
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, &Transaction{
		AccountID: accountID, Date: on, Amount: domain.MustFromString(amount), Currency: "USD",
		StatementName: "X", Payee: "X", Source: domain.SourceSync,
	}))
}

func TestTheRebuildRemovesRowsFromBeforeAnAccountExisted(t *testing.T) {
	space := newSpace(t)
	wallet := linkedToday(t, space)
	today := domain.DateOf(time.Now())

	// Stale rows: the trailing week, flat at today's balance, for an account
	// linked today.
	stale := make([]BalanceSnapshot, 0, 3)
	for back := 3; back >= 1; back-- {
		stale = append(stale, BalanceSnapshot{
			AccountID: wallet.ID, AsOf: today.AddDays(-back), Balance: domain.MustFromString("28750.00"),
		})
	}
	require.NoError(t, db(t).UpsertBalanceSnapshots(t.Context(), space, stale))

	rebuilt, err := db(t).ReconcileHistoryStarts(t.Context(), space, today)
	require.NoError(t, err)
	require.Equal(t, 1, rebuilt)
	require.Equal(t, map[string]string{today.String(): "28750.00"},
		accountHistory(t, space, wallet.ID, today))

	stored, err := db(t).GetAccount(t.Context(), space, wallet.ID)
	require.NoError(t, err)
	require.Equal(t, today, stored.HistoryRebuiltFrom)

	// Nothing moved, so the next pass leaves it alone.
	rebuilt, err = db(t).ReconcileHistoryStarts(t.Context(), space, today)
	require.NoError(t, err)
	require.Equal(t, 0, rebuilt)
}

func TestImportingOlderRowsExtendsTheMaterializedHistory(t *testing.T) {
	space := newSpace(t)
	wallet := linkedToday(t, space)
	today := domain.DateOf(time.Now())
	_, err := db(t).ReconcileHistoryStarts(t.Context(), space, today)
	require.NoError(t, err)

	// Ten days of history arrive: +400.00 ten days back, -100.00 five back.
	// Walking back from 28,750.00: days -10 to -6 hold 28,850.00, days -5 to
	// -1 hold 28,750.00.
	walletRow(t, space, wallet.ID, today.AddDays(-10), "400.00")
	walletRow(t, space, wallet.ID, today.AddDays(-5), "-100.00")

	rebuilt, err := db(t).ReconcileHistoryStarts(t.Context(), space, today)
	require.NoError(t, err)
	require.Equal(t, 1, rebuilt)

	history := accountHistory(t, space, wallet.ID, today)
	require.Len(t, history, 11)
	require.NotContains(t, history, today.AddDays(-11).String())
	require.Equal(t, "28850.00", history[today.AddDays(-10).String()])
	require.Equal(t, "28850.00", history[today.AddDays(-6).String()])
	require.Equal(t, "28750.00", history[today.AddDays(-5).String()])
	require.Equal(t, "28750.00", history[today.String()])
}

func TestAnOverrideTrimsDerivedRowsAndKeepsImportedOnes(t *testing.T) {
	space := newSpace(t)
	wallet := linkedToday(t, space)
	today := domain.DateOf(time.Now())
	walletRow(t, space, wallet.ID, today.AddDays(-20), "400.00")

	imported := today.AddDays(-30)
	_, err := db(t).db.Exec(t.Context(), `
		INSERT INTO balance_snapshots (id, account_id, as_of, balance, space_id, is_imported)
		VALUES ($1, $2, $3, 100000, $4, true)`,
		uuid.New(), wallet.ID, imported, space.UUID())
	require.NoError(t, err)

	// The imported balance is evidence: the history starts at it.
	stored, err := db(t).GetAccount(t.Context(), space, wallet.ID)
	require.NoError(t, err)
	require.Equal(t, imported, stored.ObservedSince)
	require.NoError(t, db(t).RebuildAccountHistory(t.Context(), space, wallet.ID, today))
	require.Len(t, accountHistory(t, space, wallet.ID, today), 31)

	// Moved later by hand: derived rows before the new start go; the imported
	// one stays for an override moved back.
	stored.HistoryStartsOn = today.AddDays(-5)
	require.NoError(t, db(t).UpdateAccount(t.Context(), space, &stored))
	require.NoError(t, db(t).RebuildAccountHistory(t.Context(), space, wallet.ID, today))

	history := accountHistory(t, space, wallet.ID, today)
	require.Len(t, history, 7)
	require.Equal(t, "1000.00", history[imported.String()])
	require.NotContains(t, history, today.AddDays(-6).String())
	require.Contains(t, history, today.AddDays(-5).String())

	// Cleared: automatic again, and the days between are filled back in.
	stored.HistoryStartsOn = domain.Date{}
	require.NoError(t, db(t).UpdateAccount(t.Context(), space, &stored))
	require.NoError(t, db(t).RebuildAccountHistory(t.Context(), space, wallet.ID, today))
	require.Len(t, accountHistory(t, space, wallet.ID, today), 31)
}
