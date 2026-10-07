package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Where an account's history starts, end to end. calculations.md §4.
//
// The seeded ledger gains a wallet linked today: 28,750.00 by the provider,
// nothing in its ledger. Every past window must read it as zero.

func linkWalletToday(t *testing.T, l *ledger) uuid.UUID {
	t.Helper()
	wallet := &store.Account{
		Name: "Wallet 1", Kind: domain.KindInvestment, Type: "crypto", Currency: "USD",
		IncludeInNetWorth: true,
		ProviderBalance:   domain.MustFromString("28750.00"), HasProviderBalance: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), store.SpaceIDOf(l.id("space")), wallet))
	return wallet.ID
}

func listedAccountByID(t *testing.T, l *ledger, id uuid.UUID) map[string]any {
	t.Helper()
	for _, row := range l.alex.get("/accounts").requireStatus(http.StatusOK).list() {
		if row["id"] == id.String() {
			return row
		}
	}
	t.Fatalf("account %s is not listed", id)
	return nil
}

func walletSnapshots(t *testing.T, l *ledger, wallet uuid.UUID) map[string]string {
	t.Helper()
	history, err := db(t).ListBalanceHistory(t.Context(), store.SpaceIDOf(l.id("space")),
		domain.DateOf(time.Now()))
	require.NoError(t, err)
	out := map[string]string{}
	for _, point := range history {
		if point.AccountID == domain.ID(wallet.String()) {
			out[point.On.String()] = point.Balance.String()
		}
	}
	return out
}

func TestAnAccountLinkedTodayIsNotInAPastNetWorth(t *testing.T) {
	l := buildLedger(t)
	linkWalletToday(t, l)

	// August is before the wallet existed: the investment group is there
	// with nothing in it, at both ends, and the headline is the ledger's own.
	august := netWorth(l, "from=2026-08-01&to=2026-08-31")
	investments := groupOf(t, august, string(domain.KindInvestment))
	require.Equal(t, "0.00", investments["start"])
	require.Equal(t, "0.00", investments["end"])
	for _, raw := range august["points"].([]any) {
		for _, kind := range raw.(map[string]any)["by_kind"].([]any) {
			if kind.(map[string]any)["kind"] == string(domain.KindInvestment) {
				require.Equal(t, "0.00", kind.(map[string]any)["amount"], "no point carries the wallet")
			}
		}
	}

	// A window ending today opens at zero and closes at the balance.
	sinceAugust := netWorth(l, "from=2026-08-01")
	investments = groupOf(t, sinceAugust, string(domain.KindInvestment))
	require.Equal(t, "0.00", investments["start"])
	require.Equal(t, "28750.00", investments["end"])
	require.Nil(t, investments["change_pct"], "a group that opened at zero has no percentage")
}

func TestTheListingSaysWhereTheHistoryStarts(t *testing.T) {
	l := buildLedger(t)
	wallet := linkWalletToday(t, l)
	today := domain.DateOf(time.Now()).String()

	row := listedAccountByID(t, l, wallet)
	require.Nil(t, row["history_starts_on"])
	history := row["history"].(map[string]any)
	require.Equal(t, today, history["starts_on"])
	require.Equal(t, today, history["automatic_starts_on"])

	// The checking account's stated opening balance is the earliest evidence
	// it has.
	checking := listedAccountByID(t, l, l.id("checking"))
	require.Equal(t, "2026-01-01", checking["history"].(map[string]any)["starts_on"])
}

func TestTheHistoryStartCanBeSetByHandAndCleared(t *testing.T) {
	l := buildLedger(t)
	wallet := linkWalletToday(t, l)
	today := domain.DateOf(time.Now())

	body := l.alex.patch("/accounts/"+wallet.String(), map[string]any{
		"history_starts_on": "2026-08-15",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-08-15", body["history_starts_on"])

	row := listedAccountByID(t, l, wallet)
	require.Equal(t, "2026-08-15", row["history"].(map[string]any)["starts_on"])
	require.Equal(t, today.String(), row["history"].(map[string]any)["automatic_starts_on"])

	// The snapshots were rebuilt in the same request: August 15 onward, flat
	// at the one balance the wallet has ever reported.
	snapshots := walletSnapshots(t, l, wallet)
	require.Equal(t, "28750.00", snapshots["2026-08-15"])
	require.NotContains(t, snapshots, "2026-08-14")
	require.Equal(t, "28750.00", snapshots[today.String()])

	investments := groupOf(t, netWorth(l, "from=2026-08-01&to=2026-08-31"),
		string(domain.KindInvestment))
	require.Equal(t, "0.00", investments["start"])
	require.Equal(t, "28750.00", investments["end"])

	// Cleared: automatic again, and the rows before today are gone.
	body = l.alex.patch("/accounts/"+wallet.String(), map[string]any{
		"history_starts_on": nil,
	}).requireStatus(http.StatusOK).json()
	require.Nil(t, body["history_starts_on"])
	require.Equal(t, map[string]string{today.String(): "28750.00"}, walletSnapshots(t, l, wallet))
	investments = groupOf(t, netWorth(l, "from=2026-08-01&to=2026-08-31"),
		string(domain.KindInvestment))
	require.Equal(t, "0.00", investments["end"])
}

func TestTheBalanceHistoryLineBeginsAtTheHistoryStart(t *testing.T) {
	l := buildLedger(t)
	wallet := linkWalletToday(t, l)
	today := domain.DateOf(time.Now())

	body := l.alex.get("/account-balance-history?account_id=" + wallet.String() +
		"&from=" + today.AddDays(-30).String() + "&to=" + today.String()).
		requireStatus(http.StatusOK).json()
	points := body["points"].([]any)
	require.Len(t, points, 1)
	require.Equal(t, today.String(), points[0].(map[string]any)["on"])
	require.Equal(t, "28750.00", points[0].(map[string]any)["balance"])
}

func TestAnAssetsImportedValueHistoryStartsItsHistory(t *testing.T) {
	l := buildLedger(t)
	house := l.alex.post("/accounts", map[string]any{
		"name": "Home", "kind": "asset", "type": "real_estate",
	}).requireStatus(http.StatusCreated).json()
	id := house["id"].(string)

	l.alex.post("/accounts/"+id+"/value-history", map[string]any{
		"points": []map[string]any{
			{"on": "2024-03-01", "value": "300000.00"},
			{"on": "2025-03-01", "value": "315000.00"},
		},
	}).requireStatus(http.StatusOK)

	row := listedAccountByID(t, l, uuid.MustParse(id))
	require.Equal(t, "2024-03-01", row["history"].(map[string]any)["starts_on"])

	// Rebuilt with the import, not at the next nightly pass.
	snapshots := walletSnapshots(t, l, uuid.MustParse(id))
	require.NotContains(t, snapshots, "2024-02-29")
	require.Equal(t, "300000.00", snapshots["2024-03-01"])
	require.Equal(t, "315000.00", snapshots["2025-03-01"])
}
