package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// balanceAddedOn is when the fixture accounts were added: before every row the
// tests below date, so their history spans every day they ask about.
var balanceAddedOn = NewDate(2026, time.July, 1)

func balanceAccount() Account {
	return Account{
		ID: "acct-1", Name: "Checking 1", Kind: KindCash, Currency: "USD", AddedOn: balanceAddedOn,
	}
}

func balanceCard() Account {
	return Account{
		ID:             "acct-card",
		Name:           "Card 1",
		Kind:           KindCreditCard,
		Currency:       "USD",
		CreditLimit:    MustFromString("5000"),
		HasCreditLimit: true,
		AddedOn:        balanceAddedOn,
	}
}

func balanceTxn(id ID, day int, amount string) Transaction {
	return Transaction{
		ID:        id,
		AccountID: "acct-1",
		Date:      NewDate(2026, time.August, day),
		Amount:    MustFromString(amount),
		Source:    SourceSync,
		Currency:  "USD",
	}
}

func balancePostings(account Account, txns ...Transaction) []Posting {
	out := make([]Posting, 0, len(txns))
	for _, txn := range txns {
		out = append(out, Posting{Txn: txn, Account: account})
	}
	return out
}

func balanceStrings(figures map[ID]Money) map[ID]string {
	out := make(map[ID]string, len(figures))
	for id, figure := range figures {
		out[id] = figure.String()
	}
	return out
}

func TestTheTransactionsAreTheBalanceOfAManualAccount(t *testing.T) {
	acct := balanceAccount()
	acct.OpeningBalance = MustFromString("1000.00")
	rows := balancePostings(acct, balanceTxn("t1", 15, "-25.00"), balanceTxn("t2", 16, "-75.50"))
	require.Equal(t, "899.50", AccountBalance(acct, rows).String())
}

func TestPendingRowsAreLeftOutOfTheSettledBalance(t *testing.T) {
	acct := balanceAccount()
	acct.OpeningBalance = MustFromString("1000.00")
	pending := balanceTxn("t1", 15, "-25.00")
	pending.IsPending = true
	rows := balancePostings(acct, pending)

	require.Equal(t, "1000.00", AccountBalance(acct, rows).String())
	require.Equal(t, "975.00", BalanceWithPending(acct, rows).String())
}

func TestDeletedRowsDoNotMoveTheAccountBalance(t *testing.T) {
	acct := balanceAccount()
	acct.OpeningBalance = MustFromString("1000.00")
	deleted := balanceTxn("t1", 15, "-25.00")
	deleted.IsDeleted = true
	require.Equal(t, "1000.00", AccountBalance(acct, balancePostings(acct, deleted)).String())
}

func TestARowExcludedFromReportsStillMovesTheBalance(t *testing.T) {
	acct := balanceAccount()
	acct.OpeningBalance = MustFromString("1000.00")
	excluded := balanceTxn("t1", 15, "-25.00")
	excluded.ExcludedFromReports = true
	require.Equal(t, "975.00", AccountBalance(acct, balancePostings(acct, excluded)).String())
}

func TestTheProviderFigureWinsOverTheLedger(t *testing.T) {
	// The ledger can lag the bank; the bank is the account of record.
	acct := balanceAccount()
	acct.ProviderBalance, acct.HasProviderBalance = MustFromString("1234.56"), true
	rows := balancePostings(acct, balanceTxn("t1", 15, "-25.00"))
	require.Equal(t, "1234.56", AccountBalance(acct, rows).String())
}

func TestTheOpeningRowAbsorbsTheDifference(t *testing.T) {
	// For a loan this figure is the original principal — the one number
	// checkable against the paperwork.
	acct := balanceAccount()
	acct.ProviderBalance, acct.HasProviderBalance = MustFromString("-18000.00"), true
	rows := balancePostings(acct, balanceTxn("t1", 15, "-2000.00"), balanceTxn("t2", 16, "500.00"))

	opening, err := OpeningBalanceForConnected(acct, rows)
	require.NoError(t, err)
	require.Equal(t, "-16500.00", opening.String())
}

func TestDerivingAnOpeningBalanceForAManualAccountIsAnError(t *testing.T) {
	_, err := OpeningBalanceForConnected(balanceAccount(), nil)
	require.ErrorContains(t, err, "manual")
}

func TestGoalReservesAndHoldsComeOutOfTheAvailableBalance(t *testing.T) {
	acct := balanceAccount()
	acct.ProviderBalance, acct.HasProviderBalance = MustFromString("2000.00"), true
	acct.GoalBalance = MustFromString("500.00")
	acct.PendingHolds = MustFromString("120.00")
	require.Equal(t, "1380.00", AvailableBalance(acct, nil).String())
}

func TestAConnectedAccountWalksBackwardsFromTheProviderFigure(t *testing.T) {
	// Never forward from the ledger: forward accumulation drifts whenever a
	// payment has left the payer but not been applied by the lender.
	acct := balanceAccount()
	acct.ProviderBalance, acct.HasProviderBalance = MustFromString("1000.00"), true
	rows := balancePostings(acct, balanceTxn("t1", 10, "-50.00"), balanceTxn("t2", 20, "-30.00"))

	got := BalanceAsOf(acct, rows, NewDate(2026, time.August, 15), DatePosted)
	require.Equal(t, "1030.00", got.String())
}

func TestAsOfTodayEqualsTheCurrentBalance(t *testing.T) {
	acct := balanceAccount()
	acct.ProviderBalance, acct.HasProviderBalance = MustFromString("1000.00"), true
	rows := balancePostings(acct, balanceTxn("t1", 10, "-50.00"))

	got := BalanceAsOf(acct, rows, NewDate(2026, time.August, 31), DatePosted)
	require.Equal(t, "1000.00", got.String())
}

func TestAManualAccountWalksForwardBecauseThereIsNothingToWalkBackFrom(t *testing.T) {
	acct := balanceAccount()
	acct.OpeningBalance = MustFromString("100.00")
	rows := balancePostings(acct, balanceTxn("t1", 10, "-40.00"), balanceTxn("t2", 20, "-30.00"))

	got := BalanceAsOf(acct, rows, NewDate(2026, time.August, 15), DatePosted)
	require.Equal(t, "60.00", got.String())
}

func TestCreditUsedReadsTheMagnitudeOfANegativeBalance(t *testing.T) {
	used, ok := CreditUsedPct(balanceCard(), MustFromString("-1250.00"))
	require.True(t, ok)
	require.Equal(t, "0.25", used.String())
}

func TestNoCreditLimitIsUnknownRatherThanZero(t *testing.T) {
	card := balanceCard()
	card.CreditLimit, card.HasCreditLimit = Zero, false
	_, ok := CreditUsedPct(card, MustFromString("-100"))
	require.False(t, ok)
}

func TestAZeroCreditLimitDoesNotDivideByZero(t *testing.T) {
	card := balanceCard()
	card.CreditLimit = Zero
	_, ok := CreditUsedPct(card, MustFromString("-100"))
	require.False(t, ok)
}

// Both scales of the same APR have to land on the same rate: 24.99 from a
// source that reports percentages, 0.2499 from one that reports rates.
func TestAPRReadsBothScalesOfTheSameRate(t *testing.T) {
	percentage, ok := APRAsRate(decimal.RequireFromString("24.99"))
	require.True(t, ok)
	require.Equal(t, "0.2499", percentage.String())

	rate, ok := APRAsRate(decimal.RequireFromString("0.2499"))
	require.True(t, ok)
	require.Equal(t, "0.2499", rate.String())
}

func TestAPRKeepsAPromotionalZero(t *testing.T) {
	rate, ok := APRAsRate(decimal.Zero)
	require.True(t, ok)
	require.True(t, rate.IsZero())
}

func TestAPRRefusesFiguresThatAreNotRates(t *testing.T) {
	_, negative := APRAsRate(decimal.RequireFromString("-5"))
	require.False(t, negative)

	// 150 read as a percentage is a 150% APR: not a believable card rate.
	_, absurd := APRAsRate(decimal.RequireFromString("150"))
	require.False(t, absurd)
}

func TestAManualLedgerRunsFromTheOpeningBalance(t *testing.T) {
	acct := balanceAccount()
	acct.OpeningBalance = MustFromString("100.00")
	rows := balancePostings(acct,
		balanceTxn("t1", 1, "-10.00"),
		balanceTxn("t2", 2, "-20.00"),
		balanceTxn("t3", 3, "5.00"),
	)
	require.Equal(t,
		map[ID]string{"t1": "90.00", "t2": "70.00", "t3": "75.00"},
		balanceStrings(RunningBalances(acct, rows, DatePosted)))
}

func TestAConnectedLedgerEndsOnTheProviderFigure(t *testing.T) {
	acct := balanceAccount()
	acct.ProviderBalance, acct.HasProviderBalance = MustFromString("75.00"), true
	rows := balancePostings(acct, balanceTxn("t1", 1, "-10.00"), balanceTxn("t2", 2, "-20.00"))
	require.Equal(t, "75.00", RunningBalances(acct, rows, DatePosted)["t2"].String())
}

func TestSameDayRowsOrderByIdSoTheSequenceIsStable(t *testing.T) {
	acct := balanceAccount()
	acct.OpeningBalance = MustFromString("100.00")
	rows := balancePostings(acct, balanceTxn("t2", 1, "-20.00"), balanceTxn("t1", 1, "-10.00"))
	require.Equal(t,
		map[ID]string{"t1": "90.00", "t2": "70.00"},
		balanceStrings(RunningBalances(acct, rows, DatePosted)))
}

func TestDeletedRowsGetNoRunningBalance(t *testing.T) {
	acct := balanceAccount()
	acct.OpeningBalance = MustFromString("100.00")
	deleted := balanceTxn("t1", 1, "-10.00")
	deleted.IsDeleted = true
	rows := balancePostings(acct, deleted, balanceTxn("t2", 2, "-20.00"))

	got := RunningBalances(acct, rows, DatePosted)
	require.NotContains(t, got, ID("t1"))
	require.Equal(t, "80.00", got["t2"].String())
}

func TestTheOpeningRowIsPartOfTheLedgerForAManualAccount(t *testing.T) {
	acct := balanceAccount()
	opening := balanceTxn("t0", 1, "500.00")
	opening.Date = NewDate(2026, time.January, 1)
	opening.Source = SourceOpeningBalance
	rows := balancePostings(acct, opening, balanceTxn("t1", 1, "-10.00"))

	require.Equal(t,
		map[ID]string{"t0": "500.00", "t1": "490.00"},
		balanceStrings(RunningBalances(acct, rows, DatePosted)))
}

func TestAForeignAccountBalancesInItsOwnCurrency(t *testing.T) {
	// A euro account inside a dollar space: every row carries a converted
	// primary amount for the aggregates, and none of it may leak into the
	// account's own figures — those combine with balances stored in euros.
	acct := balanceAccount()
	acct.Currency = "EUR"
	acct.OpeningBalance = MustFromString("1000.00")
	euro := func(id ID, day int, amount string) Transaction {
		txn := balanceTxn(id, day, amount)
		txn.Currency = "EUR"
		return txn
	}
	converted := func(id ID, day int, amount, primary string) Transaction {
		txn := euro(id, day, amount)
		txn.AmountPrimary, txn.HasAmountPrimary = MustFromString(primary), true
		return txn
	}
	rows := balancePostings(acct,
		converted("t1", 10, "-50.00", "-58.00"),
		euro("t2", 20, "-30.00"),
	)

	require.Equal(t, "920.00", AccountBalance(acct, rows).String())
	require.Equal(t,
		map[ID]string{"t1": "950.00", "t2": "920.00"},
		balanceStrings(RunningBalances(acct, rows, DatePosted)))

	asOf := BalanceAsOf(acct, rows, NewDate(2026, time.August, 15), DatePosted)
	require.Equal(t, "950.00", asOf.String())
}

func TestAConnectedForeignAccountAnchorsAndWalksInNativeCurrency(t *testing.T) {
	acct := balanceAccount()
	acct.Currency = "EUR"
	acct.ProviderBalance, acct.HasProviderBalance = MustFromString("870.00"), true
	txn := balanceTxn("t1", 20, "-50.00")
	txn.Currency = "EUR"
	txn.AmountPrimary, txn.HasAmountPrimary = MustFromString("-58.00"), true

	got := BalanceAsOf(acct, balancePostings(acct, txn), NewDate(2026, time.August, 15), DatePosted)
	require.Equal(t, "920.00", got.String())

	opening, err := OpeningBalanceForConnected(acct, balancePostings(acct, txn))
	require.NoError(t, err)
	require.Equal(t, "920.00", opening.String())

	require.Equal(t, "870.00",
		RunningBalances(acct, balancePostings(acct, txn), DatePosted)["t1"].String())
}
