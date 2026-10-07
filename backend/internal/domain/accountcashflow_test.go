package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func historyStrings(points BalanceProjection) []string {
	out := make([]string, 0, len(points))
	for _, point := range points {
		out = append(out, point.On.String()+" "+point.Balance.String())
	}
	return out
}

func TestTheHistoryOfAManualAccountCountsOnlySettledRows(t *testing.T) {
	// §3 Historical balance: opening balance plus settled rows by posted date.
	// The pending row, the deleted one and the provider's estimate never moved
	// the balance, so none of them appears on the line.
	acct := balanceAccount()
	acct.OpeningBalance = MustFromString("1000.00")
	pending := balanceTxn("p", 3, "-30.00")
	pending.IsPending = true
	deleted := balanceTxn("d", 3, "-999.00")
	deleted.IsDeleted = true
	estimate := balanceTxn("e", 4, "-500.00")
	estimate.IsEstimate = true
	rows := balancePostings(acct,
		balanceTxn("a", 2, "-200.00"), pending, deleted, estimate, balanceTxn("b", 4, "50.00"))

	history := BalanceHistory(acct, rows,
		NewDate(2026, time.August, 1), NewDate(2026, time.August, 5))
	require.Equal(t, []string{
		"2026-08-01 1000.00",
		"2026-08-02 800.00",
		"2026-08-03 800.00",
		"2026-08-04 850.00",
		"2026-08-05 850.00",
	}, historyStrings(history))
}

func TestTheHistoryOfAConnectedAccountWalksBackFromTheProviderFigure(t *testing.T) {
	// §3: a past balance walks back from the provider's figure, taking off
	// every row dated after the day — pending ones included, as BalanceAsOf
	// does. -300 today; +20 on the 5th, -50 pending on the 4th, -100 on the 2nd.
	card := balanceCard()
	card.ProviderBalance = MustFromString("-300.00")
	card.HasProviderBalance = true
	pending := balanceTxn("p", 4, "-50.00")
	pending.IsPending = true
	rows := balancePostings(card,
		balanceTxn("a", 2, "-100.00"), pending, balanceTxn("b", 5, "20.00"))

	history := BalanceHistory(card, rows,
		NewDate(2026, time.August, 1), NewDate(2026, time.August, 5))
	require.Equal(t, []string{
		"2026-08-01 -170.00",
		"2026-08-02 -270.00",
		"2026-08-03 -270.00",
		"2026-08-04 -320.00",
		"2026-08-05 -300.00",
	}, historyStrings(history))
}

func TestTheHistoryMovesACardChargeOnItsPostedDateNotItsDueDate(t *testing.T) {
	// A card's effective date is the statement's due date. The balance moved
	// the day the charge posted, and that is the day the line steps.
	card := balanceCard()
	card.ProviderBalance = MustFromString("-80.00")
	card.HasProviderBalance = true
	charge := balanceTxn("a", 2, "-80.00")
	charge.EffectiveDate = NewDate(2026, time.September, 20)
	history := BalanceHistory(card, balancePostings(card, charge),
		NewDate(2026, time.August, 1), NewDate(2026, time.August, 3))
	require.Equal(t, []string{
		"2026-08-01 0.00",
		"2026-08-02 -80.00",
		"2026-08-03 -80.00",
	}, historyStrings(history))
}

func TestEveryPointOfTheHistoryIsTheBalanceAsOfThatDay(t *testing.T) {
	// One definition of "the balance on a day", whichever function draws it.
	for _, connected := range []bool{false, true} {
		acct := balanceAccount()
		acct.OpeningBalance = MustFromString("250.00")
		acct.ProviderBalance = MustFromString("412.00")
		acct.HasProviderBalance = connected
		pending := balanceTxn("p", 9, "-12.00")
		pending.IsPending = true
		rows := balancePostings(acct, balanceTxn("a", 3, "-40.00"), balanceTxn("b", 3, "15.50"),
			pending, balanceTxn("c", 12, "300.00"), balanceTxn("d", 20, "-7.50"))
		from, through := NewDate(2026, time.August, 1), NewDate(2026, time.August, 25)
		for _, point := range BalanceHistory(acct, rows, from, through) {
			require.Equal(t, BalanceAsOf(acct, rows, point.On, DatePosted).String(),
				point.Balance.String(), "connected=%v on %s", connected, point.On)
		}
	}
}

func TestAnInvertedHistoryWindowIsEmpty(t *testing.T) {
	require.Empty(t, BalanceHistory(balanceAccount(), nil,
		NewDate(2026, time.August, 5), NewDate(2026, time.August, 1)))
}

func TestAnAccountsCashFlowsAreItsSettledRowsTransfersIncluded(t *testing.T) {
	acct := balanceAccount()
	pending := balanceTxn("p", 3, "-30.00")
	pending.IsPending = true
	estimate := balanceTxn("e", 4, "-500.00")
	estimate.IsEstimate = true
	deleted := balanceTxn("d", 5, "-9.00")
	deleted.IsDeleted = true
	hidden := balanceTxn("h", 6, "-12.00")
	hidden.ExcludedFromReports = true
	card := balanceTxn("c", 7, "-60.00")
	card.EffectiveDate = NewDate(2026, time.September, 25)
	rows := balancePostings(acct, pending, estimate, deleted, hidden, card)
	rows = append(rows, Posting{
		Txn: balanceTxn("t", 8, "-400.00"), Account: acct,
		Category: Category{Kind: CategoryTransfer}, HasCategory: true,
	})

	flows := AccountCashFlows(rows)
	require.Equal(t, []CashFlow{
		{On: NewDate(2026, time.August, 6), Amount: MustFromString("-12.00")},
		{On: NewDate(2026, time.August, 7), Amount: MustFromString("-60.00")},
		{On: NewDate(2026, time.August, 8), Amount: MustFromString("-400.00")},
	}, flows)
}

func TestTheAccountEstimateIsTheAverageCompleteMonthSinceItsHistoryBegan(t *testing.T) {
	// Made on 2026-09-26: the complete months are 2025-09 through 2026-08. The
	// account's history begins on 1 June, so June, July and August are read —
	// August empty — and September's partial month is not.
	today := NewDate(2026, time.September, 26)
	flows := []CashFlow{
		{On: NewDate(2026, time.June, 1), Amount: MustFromString("3000.00")},
		{On: NewDate(2026, time.June, 3), Amount: MustFromString("-1000.00")},
		{On: NewDate(2026, time.July, 1), Amount: MustFromString("3000.00")},
		{On: NewDate(2026, time.July, 9), Amount: MustFromString("-1500.00")},
		{On: NewDate(2026, time.September, 2), Amount: MustFromString("-7777.00")},
	}
	forecast, ok := EstimateAccountCashFlow(flows, today)
	require.True(t, ok)
	require.Len(t, forecast.Months, AccountForecastMonths)
	require.Equal(t, "2026-09", forecast.Months[0].Month.String())
	require.Equal(t, "2027-02", forecast.Months[5].Month.String())
	for _, month := range forecast.Months {
		// 6000 in over three months, and 2500 out: 833.333… rounds to 833.33.
		require.Equal(t, "2000.00", month.In.String())
		require.Equal(t, "833.33", month.Out.String())
	}
	require.Contains(t, forecast.Narrative, "the last 3 complete months (2026-06 through 2026-08)")
	require.NoError(t, CashFlowForecastCoversFrom(forecast, today))
}

func TestAHistoryThatBeganMidMonthLeavesThatMonthOut(t *testing.T) {
	// The first row is on 12 June, so June holds only part of a month and is
	// not read: July and August are, 3000 in and 1500 out over two months.
	today := NewDate(2026, time.September, 26)
	forecast, ok := EstimateAccountCashFlow([]CashFlow{
		{On: NewDate(2026, time.June, 12), Amount: MustFromString("-400.00")},
		{On: NewDate(2026, time.July, 1), Amount: MustFromString("3000.00")},
		{On: NewDate(2026, time.August, 9), Amount: MustFromString("-1500.00")},
	}, today)
	require.True(t, ok)
	require.Equal(t, "1500.00", forecast.Months[0].In.String())
	require.Equal(t, "750.00", forecast.Months[0].Out.String())
	require.Contains(t, forecast.Narrative, "the last 2 complete months (2026-07 through 2026-08)")
}

func TestAHistoryOlderThanTheWindowReadsAllTwelveMonths(t *testing.T) {
	// A row before the window says the account existed through all twelve
	// months, so the empty ones count as zero: 1200 out over 12 is 100. The
	// old row itself is not read.
	today := NewDate(2026, time.September, 26)
	forecast, ok := EstimateAccountCashFlow([]CashFlow{
		{On: NewDate(2025, time.August, 31), Amount: MustFromString("-9999.00")},
		{On: NewDate(2026, time.June, 12), Amount: MustFromString("-1200.00")},
	}, today)
	require.True(t, ok)
	require.Equal(t, "100.00", forecast.Months[0].Out.String())
	require.Contains(t, forecast.Narrative, "the last 12 complete months (2025-09 through 2026-08)")
}

func TestTheAccountEstimateRoundsOnceHalfAwayFromZero(t *testing.T) {
	// 0.05 out over two months is 0.025 a month, which rounds to 0.03.
	today := NewDate(2026, time.September, 1)
	forecast, ok := EstimateAccountCashFlow([]CashFlow{
		{On: NewDate(2026, time.July, 1), Amount: MustFromString("-0.05")},
		{On: NewDate(2026, time.August, 1), Amount: MustFromString("0.00")},
	}, today)
	require.True(t, ok)
	require.Equal(t, "0.03", forecast.Months[0].Out.String())
	require.Equal(t, "0.00", forecast.Months[0].In.String())
}

func TestTheAccountEstimateReadsAtMostTwelveMonths(t *testing.T) {
	// Two years of 100 out a month, and one far older month of 5000: only the
	// twelve complete months before today's are read.
	today := NewDate(2026, time.September, 26)
	flows := []CashFlow{{On: NewDate(2024, time.January, 5), Amount: MustFromString("-5000.00")}}
	for offset := 1; offset <= 24; offset++ {
		flows = append(flows, CashFlow{
			On: MonthOf(today).Shift(-offset).Day(10), Amount: MustFromString("-100.00"),
		})
	}
	forecast, ok := EstimateAccountCashFlow(flows, today)
	require.True(t, ok)
	require.Equal(t, "100.00", forecast.Months[0].Out.String())
	require.Contains(t, forecast.Narrative, "the last 12 complete months")
}

func TestAnAccountWithNoCompleteMonthHasNoEstimate(t *testing.T) {
	today := NewDate(2026, time.September, 26)
	_, ok := EstimateAccountCashFlow([]CashFlow{
		{On: NewDate(2026, time.September, 3), Amount: MustFromString("-20.00")},
	}, today)
	require.False(t, ok)
	_, ok = EstimateAccountCashFlow(nil, today)
	require.False(t, ok)
}

func TestAnAccountWithOneMonthSaysWhetherItWasWhole(t *testing.T) {
	today := NewDate(2026, time.September, 26)
	whole, ok := EstimateAccountCashFlow([]CashFlow{
		{On: NewDate(2026, time.August, 1), Amount: MustFromString("-20.00")},
	}, today)
	require.True(t, ok)
	require.Contains(t, whole.Narrative, "its one complete month (2026-08)")

	// Begun mid-August, with nothing after it: the only month there is, read
	// with the caveat rather than not at all.
	partial, ok := EstimateAccountCashFlow([]CashFlow{
		{On: NewDate(2026, time.August, 3), Amount: MustFromString("-20.00")},
	}, today)
	require.True(t, ok)
	require.Equal(t, "20.00", partial.Months[0].Out.String())
	require.Contains(t, partial.Narrative, "its first month (2026-08), which may not be a full one")
}
