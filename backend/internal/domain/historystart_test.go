package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A wallet linked this morning: 28,750.00 by the provider, nothing in its
// ledger. Every figure below is worked by hand from §4's history-start rule.
func walletLinkedToday() Account {
	return Account{
		ID: "wallet", Name: "Wallet 1", Kind: KindInvestment, Currency: "USD",
		ProviderBalance: MustFromString("28750.00"), HasProviderBalance: true,
		IncludeInNetWorth: true,
		AddedOn:           NewDate(2026, time.September, 26),
	}
}

func walletRow(id ID, on Date, amount string) Transaction {
	return Transaction{
		ID: id, AccountID: "wallet", Date: on, Amount: MustFromString(amount),
		Source: SourceSync, Currency: "USD",
	}
}

func TestAnAccountAddedTodayWithNoRowsHoldsNothingBeforeToday(t *testing.T) {
	wallet := walletLinkedToday()
	today := NewDate(2026, time.September, 26)

	require.Equal(t, today, HistoryStart(wallet, nil))
	require.Equal(t, "0.00", BalanceAsOf(wallet, nil, today.AddDays(-1), DatePosted).String())
	require.Equal(t, "0.00",
		BalanceAsOf(wallet, nil, NewDate(2019, time.January, 31), DatePosted).String())
	require.Equal(t, "28750.00", BalanceAsOf(wallet, nil, today, DatePosted).String())

	// The ledger's own walk still answers the balance it implies, which is
	// what a projection opening yesterday starts from.
	require.Equal(t, "28750.00",
		LedgerBalanceAsOf(wallet, nil, today.AddDays(-1), DatePosted).String())

	// One point, today's; no flat line back through the week.
	require.Equal(t, []string{"2026-09-26 28750.00"},
		historyStrings(BalanceHistory(wallet, nil, NewDate(2026, time.September, 20), today)))
	require.Empty(t, BalanceHistory(wallet, nil,
		NewDate(2026, time.September, 1), NewDate(2026, time.September, 25)))
}

func TestAnAccountAddedTodayAddsNothingToYesterdaysNetWorth(t *testing.T) {
	checking := Account{
		ID: "checking", Kind: KindCash, Currency: "USD", IncludeInNetWorth: true,
		ProviderBalance: MustFromString("4000.00"), HasProviderBalance: true,
		AddedOn: NewDate(2024, time.May, 1),
	}
	wallet := walletLinkedToday()
	yesterday := NewDate(2026, time.September, 25)

	balances := map[ID]Money{
		checking.ID: BalanceAsOf(checking, nil, yesterday, DatePosted),
		wallet.ID:   BalanceAsOf(wallet, nil, yesterday, DatePosted),
	}
	worth := NetWorthAt([]Account{checking, wallet}, balances, yesterday)
	require.Equal(t, "4000.00", worth.Net.String())

	groups := GroupChanges([]Account{checking, wallet}, balances, map[ID]Money{
		checking.ID: MustFromString("4000.00"), wallet.ID: MustFromString("28750.00"),
	}, "")
	require.Equal(t, "investment", groups[1].Key)
	require.Equal(t, "0.00", groups[1].Start.String())
	_, hasPct := groups[1].ChangePct()
	require.False(t, hasPct, "a group that started at zero has no percentage")
}

func TestImportingOlderRowsMovesTheStartBack(t *testing.T) {
	// The same wallet once three months of its history arrive: +500.00 on
	// June 10 and -250.00 on July 1. The start follows the earliest row, and
	// June 10's close walks back from 28,750.00 over the July row:
	// 28,750.00 − (−250.00) = 29,000.00.
	wallet := walletLinkedToday()
	rows := balancePostings(wallet,
		walletRow("jun", NewDate(2026, time.June, 10), "500.00"),
		walletRow("jul", NewDate(2026, time.July, 1), "-250.00"))

	require.Equal(t, NewDate(2026, time.June, 10), HistoryStart(wallet, rows))
	require.Equal(t, "0.00",
		BalanceAsOf(wallet, rows, NewDate(2026, time.June, 9), DatePosted).String())
	require.Equal(t, "29000.00",
		BalanceAsOf(wallet, rows, NewDate(2026, time.June, 10), DatePosted).String())
	require.Equal(t, "28750.00",
		BalanceAsOf(wallet, rows, NewDate(2026, time.July, 1), DatePosted).String())
}

func TestPendingRowsCountAsHistoryAndDeletedAndEstimatedOnesDoNot(t *testing.T) {
	wallet := walletLinkedToday()
	deleted := walletRow("deleted", NewDate(2026, time.January, 5), "-10.00")
	deleted.IsDeleted = true
	estimate := walletRow("estimate", NewDate(2026, time.February, 1), "-20.00")
	estimate.IsEstimate = true
	pending := walletRow("pending", NewDate(2026, time.September, 1), "-30.00")
	pending.IsPending = true

	rows := balancePostings(wallet, deleted, estimate, pending)
	require.Equal(t, NewDate(2026, time.September, 1), AutomaticHistoryStart(wallet, rows))
}

func TestTheStartIsThePostedDateNotTheEffectiveDate(t *testing.T) {
	// A card charge posted August 20 is filed under its bill's due date,
	// September 15, for reporting. A balance moves when the bank posts it.
	card := Account{ID: "card", Kind: KindCreditCard, AddedOn: NewDate(2026, time.September, 26)}
	charge := Transaction{
		ID: "c", AccountID: "card", Date: NewDate(2026, time.August, 20),
		EffectiveDate: NewDate(2026, time.September, 15), Amount: MustFromString("-40.00"),
	}
	require.Equal(t, NewDate(2026, time.August, 20),
		AutomaticHistoryStart(card, balancePostings(card, charge)))
}

func TestAnOverrideMovesTheStartEarlierOrLater(t *testing.T) {
	wallet := walletLinkedToday()
	rows := balancePostings(wallet,
		walletRow("jun", NewDate(2026, time.June, 10), "500.00"),
		walletRow("jul", NewDate(2026, time.July, 1), "-250.00"))

	// Earlier than any evidence: the household says it held the wallet since
	// New Year, so March walks back over both rows: 28,750 − 500 + 250.
	earlier := wallet
	earlier.HistoryStartsOn = NewDate(2026, time.January, 1)
	require.Equal(t, NewDate(2026, time.January, 1), HistoryStart(earlier, rows))
	require.Equal(t, NewDate(2026, time.June, 10), AutomaticHistoryStart(earlier, rows))
	require.Equal(t, "28500.00",
		BalanceAsOf(earlier, rows, NewDate(2026, time.March, 1), DatePosted).String())
	require.Equal(t, "0.00",
		BalanceAsOf(earlier, rows, NewDate(2025, time.December, 31), DatePosted).String())

	// Later: the June row stays in the ledger but the history begins August 1.
	later := wallet
	later.HistoryStartsOn = NewDate(2026, time.August, 1)
	require.Equal(t, "0.00",
		BalanceAsOf(later, rows, NewDate(2026, time.July, 31), DatePosted).String())
	require.Equal(t, "28750.00",
		BalanceAsOf(later, rows, NewDate(2026, time.August, 1), DatePosted).String())
	require.Equal(t, []string{"2026-08-01 28750.00", "2026-08-02 28750.00"},
		historyStrings(BalanceHistory(later, rows,
			NewDate(2026, time.July, 30), NewDate(2026, time.August, 2))))
}

func TestAnAssetStartsAtItsEarliestImportedValue(t *testing.T) {
	// A house added today, its value history imported from a spreadsheet:
	// 300,000 in March 2024, 315,000 in March 2025, 325,000 in March 2026.
	// The import writes differences (+300,000, +15,000, +10,000), and the
	// house's history begins at the first of them.
	house := Account{
		ID: "house", Kind: KindAsset, Currency: "USD", IncludeInNetWorth: true,
		AddedOn: NewDate(2026, time.September, 26),
	}
	adjustments := ValuationAdjustments(Zero, nil, []ValuePoint{
		{On: NewDate(2024, time.March, 1), Value: MustFromString("300000.00")},
		{On: NewDate(2025, time.March, 1), Value: MustFromString("315000.00")},
		{On: NewDate(2026, time.March, 1), Value: MustFromString("325000.00")},
	})
	txns := make([]Transaction, 0, len(adjustments))
	for i, row := range adjustments {
		txns = append(txns, Transaction{
			ID: ID(rune('a' + i)), AccountID: "house", Date: row.On, Amount: row.Amount,
			Source: SourceBalanceAdjustment, Currency: "USD",
		})
	}
	rows := balancePostings(house, txns...)

	require.Equal(t, NewDate(2024, time.March, 1), HistoryStart(house, rows))
	require.Equal(t, "0.00",
		BalanceAsOf(house, rows, NewDate(2024, time.February, 29), DatePosted).String())
	require.Equal(t, "300000.00",
		BalanceAsOf(house, rows, NewDate(2024, time.March, 1), DatePosted).String())
	require.Equal(t, "315000.00",
		BalanceAsOf(house, rows, NewDate(2025, time.June, 1), DatePosted).String())
	require.Equal(t, "325000.00",
		BalanceAsOf(house, rows, NewDate(2026, time.September, 26), DatePosted).String())
}

func TestAStatedOpeningBalanceStartsTheHistoryOnItsDay(t *testing.T) {
	// A manual savings account: 1,000.00 as of May 1, a first row June 1.
	savings := Account{
		ID: "savings", Kind: KindCash, OpeningBalance: MustFromString("1000.00"),
		OpeningBalanceOn: NewDate(2026, time.May, 1), AddedOn: NewDate(2026, time.September, 26),
	}
	rows := balancePostings(savings, Transaction{
		ID: "dep", AccountID: "savings", Date: NewDate(2026, time.June, 1),
		Amount: MustFromString("200.00"),
	})
	require.Equal(t, NewDate(2026, time.May, 1), HistoryStart(savings, rows))
	require.Equal(t, "0.00",
		BalanceAsOf(savings, rows, NewDate(2026, time.April, 30), DatePosted).String())
	require.Equal(t, "1000.00",
		BalanceAsOf(savings, rows, NewDate(2026, time.May, 15), DatePosted).String())
	require.Equal(t, "1200.00",
		BalanceAsOf(savings, rows, NewDate(2026, time.June, 1), DatePosted).String())
}

func TestAnImportedBalanceHistoryCountsAsHistory(t *testing.T) {
	// A brokerage account the Simplifi import carried over with balances
	// from January 31, 2025 and no rows at all.
	brokerage := walletLinkedToday()
	brokerage.ObservedSince = NewDate(2025, time.January, 31)
	require.Equal(t, NewDate(2025, time.January, 31), HistoryStart(brokerage, nil))
}

func TestAnAccountWithNoEvidenceHasNoFloor(t *testing.T) {
	bare := Account{ID: "bare", ProviderBalance: MustFromString("10.00"), HasProviderBalance: true}
	require.True(t, HistoryStart(bare, nil).IsZero())
	require.Equal(t, "10.00",
		BalanceAsOf(bare, nil, NewDate(2019, time.January, 1), DatePosted).String())
}

func TestStoredPointsBeforeTheStartAreDropped(t *testing.T) {
	history := []BalancePoint{
		{AccountID: "wallet", On: NewDate(2026, time.September, 19), Balance: MustFromString("28750.00")},
		{AccountID: "wallet", On: NewDate(2026, time.September, 26), Balance: MustFromString("28750.00")},
		{AccountID: "other", On: NewDate(2026, time.September, 19), Balance: MustFromString("5.00")},
	}
	kept := HistoryWithin(history, map[ID]Date{"wallet": NewDate(2026, time.September, 26)})
	require.Len(t, kept, 2)
	require.Equal(t, NewDate(2026, time.September, 26), kept[0].On)
	require.Equal(t, ID("other"), kept[1].AccountID)

	// And the day before the start reads nothing from them.
	on := BalancesOn(kept, NewDate(2026, time.September, 25))
	_, held := on["wallet"]
	require.False(t, held)
}
