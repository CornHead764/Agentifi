package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func netAccount(id ID, kind AccountKind) Account {
	return Account{ID: id, Name: string(id), Kind: kind, Currency: "USD", IncludeInNetWorth: true}
}

func netFigures(pairs map[ID]string) map[ID]Money {
	out := make(map[ID]Money, len(pairs))
	for id, amount := range pairs {
		out[id] = MustFromString(amount)
	}
	return out
}

var (
	netChecking = netAccount("a-cash", KindCash)
	netSavings  = netAccount("a-save", KindCash)
	netCard     = netAccount("a-card", KindCreditCard)
	netHouse    = netAccount("a-house", KindAsset)
	netLoan     = netAccount("a-loan", KindLoan)

	netOn = NewDate(2026, time.August, 21)
)

func TestAssetsAndDebtSplitBySignAndDebtReadsPositive(t *testing.T) {
	got := NetWorthAt(
		[]Account{netChecking, netCard, netHouse},
		netFigures(map[ID]string{"a-cash": "5000", "a-card": "-1200", "a-house": "300000"}),
		netOn,
	)
	require.Equal(t, "305000.00", got.Assets.String())
	require.Equal(t, "1200.00", got.Debt.String())
	require.Equal(t, "303800.00", got.Net.String())
}

func TestAccountsFlaggedOutOfNetWorthAreLeftOut(t *testing.T) {
	excluded := netChecking
	excluded.IncludeInNetWorth = false
	got := NetWorthAt(
		[]Account{excluded, netHouse},
		netFigures(map[ID]string{"a-cash": "5000", "a-house": "300000"}),
		netOn,
	)
	require.Equal(t, "300000.00", got.Net.String())
}

func TestAnIgnoredAccountIsOutOfNetWorthThoughFlaggedIn(t *testing.T) {
	ignored := netChecking
	ignored.IsIgnored = true
	accounts := []Account{ignored, netHouse}
	balances := netFigures(map[ID]string{"a-cash": "5000", "a-house": "300000"})
	require.Equal(t, "300000.00", NetWorthAt(accounts, balances, netOn).Net.String())

	groups := GroupChanges(accounts, balances, balances, "")
	require.Len(t, groups, 1)
	require.Equal(t, string(KindAsset), groups[0].Key)
}

func TestAnAccountWithNoBalanceContributesNothing(t *testing.T) {
	got := NetWorthAt(
		[]Account{netChecking, netSavings},
		netFigures(map[ID]string{"a-cash": "5000"}),
		netOn,
	)
	require.Equal(t, "5000.00", got.Net.String())
}

func TestDebtToAssetIsARatioOfPositives(t *testing.T) {
	got := NetWorthAt(
		[]Account{netChecking, netLoan},
		netFigures(map[ID]string{"a-cash": "10000", "a-loan": "-2500"}),
		netOn,
	)
	ratio, ok := got.DebtToAsset()
	require.True(t, ok)
	require.Equal(t, "0.25", ratio.String())
}

func TestNoAssetsGivesNoRatioRatherThanAnError(t *testing.T) {
	got := NetWorthAt([]Account{netLoan}, netFigures(map[ID]string{"a-loan": "-2500"}), netOn)
	_, ok := got.DebtToAsset()
	require.False(t, ok)
}

func netHistory() []BalancePoint {
	return []BalancePoint{
		{AccountID: "a-cash", On: NewDate(2026, time.August, 1), Balance: MustFromString("1000")},
		{AccountID: "a-cash", On: NewDate(2026, time.August, 15), Balance: MustFromString("1500")},
		{AccountID: "a-cash", On: NewDate(2026, time.August, 25), Balance: MustFromString("900")},
		{AccountID: "a-card", On: NewDate(2026, time.August, 10), Balance: MustFromString("-200")},
	}
}

func TestBalancesOnTakesTheMostRecentPointAtOrBeforeTheDate(t *testing.T) {
	got := BalancesOn(netHistory(), NewDate(2026, time.August, 20))
	require.Equal(t, "1500.00", got["a-cash"].String())
	require.Equal(t, "-200.00", got["a-card"].String())
	require.Len(t, got, 2)
}

func TestAPointExactlyOnTheDateCounts(t *testing.T) {
	got := BalancesOn(netHistory(), NewDate(2026, time.August, 15))
	require.Equal(t, "1500.00", got["a-cash"].String())
}

func TestAnAccountWithNoHistoryYetIsAbsentNotZero(t *testing.T) {
	// Absent and zero are different facts: one account did not exist, the other
	// was empty.
	got := BalancesOn(netHistory(), NewDate(2026, time.August, 5))
	require.Len(t, got, 1)
	require.NotContains(t, got, ID("a-card"))
}

func TestNetWorthChangeAndPercentageOverAWindow(t *testing.T) {
	start := NetWorthAt([]Account{netChecking},
		netFigures(map[ID]string{"a-cash": "1000"}), NewDate(2026, time.January, 1))
	end := NetWorthAt([]Account{netChecking}, netFigures(map[ID]string{"a-cash": "1250"}), netOn)

	require.Equal(t, "250.00", NetWorthChange(start, end).String())
	pct, ok := NetWorthChangePct(start, end)
	require.True(t, ok)
	require.Equal(t, "25", pct.String())
}

func TestAWindowThatOpenedAtZeroHasNoPercentage(t *testing.T) {
	start := NetWorthAt([]Account{netChecking},
		netFigures(map[ID]string{"a-cash": "0"}), NewDate(2026, time.January, 1))
	end := NetWorthAt([]Account{netChecking}, netFigures(map[ID]string{"a-cash": "500"}), netOn)

	require.Equal(t, "500.00", NetWorthChange(start, end).String())
	_, ok := NetWorthChangePct(start, end)
	require.False(t, ok)
}

func TestClimbingOutOfDebtReadsAsAPositiveMove(t *testing.T) {
	start := NetWorthAt([]Account{netLoan},
		netFigures(map[ID]string{"a-loan": "-1000"}), NewDate(2026, time.January, 1))
	end := NetWorthAt([]Account{netLoan}, netFigures(map[ID]string{"a-loan": "-500"}), netOn)

	require.Equal(t, "500.00", NetWorthChange(start, end).String())
	pct, ok := NetWorthChangePct(start, end)
	require.True(t, ok)
	require.Equal(t, "50", pct.String())
}

func TestGroupChangesRollUpByKindOverTheSelectedWindow(t *testing.T) {
	rows := GroupChanges(
		[]Account{netChecking, netSavings, netCard},
		netFigures(map[ID]string{"a-cash": "1000", "a-save": "5000", "a-card": "-400"}),
		netFigures(map[ID]string{"a-cash": "1200", "a-save": "5500", "a-card": "-300"}),
		"",
	)
	byKey := map[string]GroupChange{}
	for _, row := range rows {
		byKey[row.Key] = row
	}
	require.Equal(t, "700.00", byKey["cash"].Change().String())
	require.Equal(t, "100.00", byKey["credit_card"].Change().String())
}

func TestGroupPercentagesAreRelativeToTheWindowStartNotMonthOverMonth(t *testing.T) {
	rows := GroupChanges([]Account{netChecking},
		netFigures(map[ID]string{"a-cash": "1000"}),
		netFigures(map[ID]string{"a-cash": "1100"}), "")

	pct, ok := rows[0].ChangePct()
	require.True(t, ok)
	require.Equal(t, "10", pct.String())
}

func TestAGroupThatStartedEmptyHasNoPercentage(t *testing.T) {
	rows := GroupChanges([]Account{netChecking},
		netFigures(map[ID]string{"a-cash": "0"}),
		netFigures(map[ID]string{"a-cash": "1100"}), "")

	_, ok := rows[0].ChangePct()
	require.False(t, ok)
}
