package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The expected values are the four separate calls' output on an identical
// ledger: the pass must change how often the ledger is read, not what it writes.
func TestDailyBalancePassMatchesSteps(t *testing.T) {
	today := domain.NewDate(2026, time.August, 20)
	build := func() SpaceID {
		space := newSpace(t)
		checking := newAccount(t, space, "Pass Checking")
		checking.OpeningBalance = domain.MustFromString("500")
		checking.OpeningBalanceOn = domain.NewDate(2026, time.July, 1)
		require.NoError(t, db(t).UpdateAccount(t.Context(), space, checking))
		savings := newAccount(t, space, "Pass Savings")
		savings.OpeningBalanceOn = domain.NewDate(2026, time.August, 1)
		require.NoError(t, db(t).UpdateAccount(t.Context(), space, savings))
		for _, row := range []struct {
			account *Account
			on      domain.Date
			amount  string
		}{
			{checking, domain.NewDate(2026, time.July, 3), "-100"},
			{checking, domain.NewDate(2026, time.August, 2), "-40"},
			{checking, domain.NewDate(2026, time.August, 17), "25"},
			{savings, domain.NewDate(2026, time.August, 5), "300"},
			{savings, domain.NewDate(2026, time.August, 19), "-50"},
		} {
			require.NoError(t, db(t).CreateTransaction(t.Context(), space, &Transaction{
				AccountID: row.account.ID, Date: row.on, Amount: domain.MustFromString(row.amount),
				Currency: "USD", StatementName: "X", Payee: "X", Source: domain.SourceManual,
			}))
		}
		// A drifted derived row outside the rebuild window, for the re-derive.
		require.NoError(t, db(t).UpsertBalanceSnapshots(t.Context(), space, []BalanceSnapshot{
			{AccountID: checking.ID, AsOf: domain.NewDate(2026, time.July, 10), Balance: domain.MustFromString("12345")},
		}))
		return space
	}
	type row struct {
		account string
		on      domain.Date
		balance string
	}
	state := func(space SpaceID) ([]row, map[string]domain.Date) {
		accounts, err := db(t).ListAccounts(t.Context(), space, AccountQuery{IncludeClosed: true, IncludeIgnored: true})
		require.NoError(t, err)
		names, starts := map[string]string{}, map[string]domain.Date{}
		for _, account := range accounts {
			names[account.ID.String()] = account.Name
			starts[account.Name] = account.HistoryRebuiltFrom
		}
		history, err := db(t).ListAllBalanceHistory(t.Context(), space)
		require.NoError(t, err)
		rows := make([]row, 0, len(history))
		for _, point := range history {
			rows = append(rows, row{names[string(point.AccountID)], point.On, point.Balance.String()})
		}
		return rows, starts
	}

	steps := build()
	from := today.AddDays(-7)
	_, err := db(t).SnapshotAccounts(t.Context(), steps, today)
	require.NoError(t, err)
	_, err = db(t).RebuildBalanceHistory(t.Context(), steps, from, today.AddDays(-1))
	require.NoError(t, err)
	_, err = db(t).ReconcileHistoryStarts(t.Context(), steps, today)
	require.NoError(t, err)
	_, err = db(t).RederiveBalanceHistory(t.Context(), steps)
	require.NoError(t, err)

	pass := build()
	require.Equal(t, DailyBalanceErrors{}, db(t).RunDailyBalancePass(t.Context(), pass, today, from))

	wantRows, wantStarts := state(steps)
	gotRows, gotStarts := state(pass)
	require.NotEmpty(t, wantRows)
	require.NotContains(t, wantRows, row{"Pass Checking", domain.NewDate(2026, time.July, 10), "12345.00"},
		"the re-derive corrects the drifted row")
	for name, start := range wantStarts {
		require.False(t, start.IsZero(), "the reconcile records %s's history start", name)
	}
	require.ElementsMatch(t, wantRows, gotRows)
	require.Equal(t, wantStarts, gotStarts)
}
