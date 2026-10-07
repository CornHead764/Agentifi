package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func aug(day int) Date { return NewDate(2026, time.August, day) }

func observed(account ID, day int, balance string) BalancePoint {
	return BalancePoint{AccountID: account, On: aug(day), Balance: MustFromString(balance), Observed: true}
}

func derived(account ID, day int, balance string) BalancePoint {
	return BalancePoint{AccountID: account, On: aug(day), Balance: MustFromString(balance)}
}

func anchored(point BalancePoint, day int, balance string) BalancePoint {
	point.Anchor = BalanceAnchor{On: aug(day), Balance: MustFromString(balance)}
	point.HasAnchor = true
	return point
}

func pointStrings(points []BalancePoint) []string {
	out := make([]string, 0, len(points))
	for _, point := range points {
		out = append(out, string(point.AccountID)+" "+point.On.String()+" "+point.Balance.String())
	}
	return out
}

func droppedDays(dropped map[Date]bool) []string {
	var out []string
	for day := range dropped {
		out = append(out, day.String())
	}
	return out
}

// snapshotLoan is a connected mortgage owing 250,000.00 and paying 400.00 on
// the 4th.
func snapshotLoan() (Account, []Posting) {
	loan := Account{
		ID: "loan", Name: "Mortgage 1", Kind: KindLoan, Currency: "USD", AddedOn: balanceAddedOn,
		ProviderBalance: MustFromString("-249600.00"), HasProviderBalance: true,
	}
	payment := balanceTxn("pay", 4, "400.00")
	payment.AccountID = "loan"
	return loan, balancePostings(loan, payment)
}

// snapshotCard is a connected card at -300.00 today, with -100.00 on the 2nd,
// a -50.00 row on the 4th and a 20.00 refund on the 5th.
func snapshotCard() (Account, []Posting) {
	card := balanceCard()
	card.ProviderBalance = MustFromString("-300.00")
	card.HasProviderBalance = true
	return card, balancePostings(card,
		balanceTxn("a", 2, "-100.00"), balanceTxn("dup", 4, "-50.00"), balanceTxn("b", 5, "20.00"))
}

func TestAZeroBetweenTwoAgreeingReadingsIsADroppedReading(t *testing.T) {
	// A mortgage reads 250,000.00 owed, 0.00 the next day, then 249,600.00:
	// the two readings agree within a tenth, so the zero is the feed, not a
	// payoff.
	loan, _ := snapshotLoan()
	dropped := DroppedReadings(loan, []BalancePoint{
		observed("loan", 1, "-250000.00"),
		observed("loan", 2, "0.00"),
		observed("loan", 3, "-249600.00"),
	})
	require.Equal(t, []string{"2026-08-02"}, droppedDays(dropped))
}

func TestARunOfZerosBetweenAgreeingReadingsIsDroppedWhole(t *testing.T) {
	loan, _ := snapshotLoan()
	dropped := DroppedReadings(loan, []BalancePoint{
		observed("loan", 1, "-250000.00"),
		observed("loan", 2, "0.00"),
		observed("loan", 3, "0.00"),
		observed("loan", 4, "-249600.00"),
	})
	require.ElementsMatch(t, []string{"2026-08-02", "2026-08-03"}, droppedDays(dropped))
}

func TestAPayoffThatComesBackAtADifferentFigureIsKept(t *testing.T) {
	// A card owing 2,000.00 paid to zero and used again for 300.00: the
	// readings either side are 1,700.00 apart, far outside a tenth of 2,000.
	card, _ := snapshotCard()
	dropped := DroppedReadings(card, []BalancePoint{
		observed("acct-card", 1, "-2000.00"),
		observed("acct-card", 2, "0.00"),
		observed("acct-card", 3, "-300.00"),
	})
	require.Empty(t, dropped)
}

func TestAZeroWithNoReadingAfterItIsKept(t *testing.T) {
	loan, _ := snapshotLoan()
	dropped := DroppedReadings(loan, []BalancePoint{
		observed("loan", 1, "-250000.00"),
		observed("loan", 2, "0.00"),
	})
	require.Empty(t, dropped)
}

func TestAZeroOnASmallBalanceIsKept(t *testing.T) {
	// Below SuspectResetThreshold (100) a zero is ordinary, as it is to the
	// sync's guard.
	card, _ := snapshotCard()
	dropped := DroppedReadings(card, []BalancePoint{
		observed("acct-card", 1, "-80.00"),
		observed("acct-card", 2, "0.00"),
		observed("acct-card", 3, "-80.00"),
	})
	require.Empty(t, dropped)
}

func TestAHouseholdThatAcceptsZerosKeepsEveryZero(t *testing.T) {
	loan, _ := snapshotLoan()
	loan.AcceptZeroBalance = true
	dropped := DroppedReadings(loan, []BalancePoint{
		observed("loan", 1, "-250000.00"),
		observed("loan", 2, "0.00"),
		observed("loan", 3, "-249600.00"),
	})
	require.Empty(t, dropped)
}

func TestAnAnchorIsTheProviderFigureAtTheNewestRow(t *testing.T) {
	card, rows := snapshotCard()
	anchor, ok := AnchorFor(card, rows, aug(3))
	require.True(t, ok)
	require.Equal(t, "2026-08-05", anchor.On.String())
	require.Equal(t, "-300.00", anchor.Balance.String())

	anchor, ok = AnchorFor(card, rows, aug(9))
	require.True(t, ok)
	require.Equal(t, "2026-08-09", anchor.On.String())
	require.Equal(t, "-300.00", anchor.Balance.String())

	_, ok = AnchorFor(balanceAccount(), nil, aug(3))
	require.False(t, ok, "a manual account has no outside figure")
}

func TestADerivedPointFollowsARowDeletedAfterItWasWritten(t *testing.T) {
	// Written on the 3rd with the duplicate -50.00 on the 4th still on file:
	// -300 less (-50 + 20) = -270. With the duplicate deleted, the same point
	// is -300 less 20 = -320.
	card, rows := snapshotCard()
	history := []BalancePoint{anchored(derived("acct-card", 3, "-270.00"), 5, "-300.00")}
	require.Equal(t, []string{"acct-card 2026-08-03 -270.00"},
		pointStrings(RederiveHistory([]Account{card}, map[ID][]Posting{"acct-card": rows}, history)))

	rows[1].Txn.IsDeleted = true
	require.Equal(t, []string{"acct-card 2026-08-03 -320.00"},
		pointStrings(RederiveHistory([]Account{card}, map[ID][]Posting{"acct-card": rows}, history)))
}

func TestADerivedPointFollowsAnEditedAmount(t *testing.T) {
	// The -50.00 on the 4th was really -75.00: the 3rd is -300 less (-75 + 20).
	card, rows := snapshotCard()
	rows[1].Txn.Amount = MustFromString("-75.00")
	history := []BalancePoint{anchored(derived("acct-card", 3, "-270.00"), 5, "-300.00")}
	require.Equal(t, []string{"acct-card 2026-08-03 -245.00"},
		pointStrings(RederiveHistory([]Account{card}, map[ID][]Posting{"acct-card": rows}, history)))
}

func TestADerivedPointWalksFromTheNextObservationBeforeItsOwnAnchor(t *testing.T) {
	// The import observed -500.00 on the 4th. The 3rd walks back from that,
	// not from today's -300.00: -500 less the -50 on the 4th = -450. The
	// observation itself stands.
	card, rows := snapshotCard()
	history := []BalancePoint{
		anchored(derived("acct-card", 3, "-270.00"), 5, "-300.00"),
		observed("acct-card", 4, "-500.00"),
	}
	require.Equal(t, []string{
		"acct-card 2026-08-03 -450.00",
		"acct-card 2026-08-04 -500.00",
	}, pointStrings(RederiveHistory([]Account{card}, map[ID][]Posting{"acct-card": rows}, history)))
}

func TestADroppedReadingIsNotReadAndNotWalkedFrom(t *testing.T) {
	// The zero on the 2nd goes. The 3rd, a gap-fill row with no anchor of its
	// own, walks back from the reading on the 5th: -249,600 less the 400.00
	// payment on the 4th = -250,000.
	loan, rows := snapshotLoan()
	history := []BalancePoint{
		observed("loan", 1, "-250000.00"),
		observed("loan", 2, "0.00"),
		derived("loan", 3, "0.00"),
		observed("loan", 5, "-249600.00"),
	}
	require.Equal(t, []string{
		"loan 2026-08-01 -250000.00",
		"loan 2026-08-03 -250000.00",
		"loan 2026-08-05 -249600.00",
	}, pointStrings(RederiveHistory([]Account{loan}, map[ID][]Posting{"loan": rows}, history)))
}

func TestRederivingAnUnchangedLedgerMovesNothing(t *testing.T) {
	// Points written by the daily pass — BalanceAsOf with AnchorFor, on each
	// day — are what re-deriving them gives back.
	card, rows := snapshotCard()
	var history []BalancePoint
	for day := 1; day <= 6; day++ {
		point := BalancePoint{
			AccountID: "acct-card", On: aug(day),
			Balance: BalanceAsOf(card, rows, aug(day), DatePosted),
		}
		point.Anchor, point.HasAnchor = AnchorFor(card, rows, aug(day))
		history = append(history, point)
	}
	require.Equal(t, pointStrings(history),
		pointStrings(RederiveHistory([]Account{card}, map[ID][]Posting{"acct-card": rows}, history)))
}

func TestAManualAccountsDerivedPointIsItsLedger(t *testing.T) {
	// 1,000.00 opening, -200.00 on the 2nd: the stale 1,000.00 written for the
	// 3rd is 800.00.
	acct := balanceAccount()
	acct.OpeningBalance = MustFromString("1000.00")
	rows := balancePostings(acct, balanceTxn("a", 2, "-200.00"))
	history := []BalancePoint{derived("acct-1", 1, "1000.00"), derived("acct-1", 3, "1000.00")}
	require.Equal(t, []string{
		"acct-1 2026-08-01 1000.00",
		"acct-1 2026-08-03 800.00",
	}, pointStrings(RederiveHistory([]Account{acct}, map[ID][]Posting{"acct-1": rows}, history)))
}

func TestAPointWithNothingToWalkFromStands(t *testing.T) {
	// A point with no anchor before it and no observation after it.
	card, rows := snapshotCard()
	history := []BalancePoint{derived("acct-card", 3, "-123.00")}
	require.Equal(t, []string{"acct-card 2026-08-03 -123.00"},
		pointStrings(RederiveHistory([]Account{card}, map[ID][]Posting{"acct-card": rows}, history)))
}

func TestADerivedPointBeforeTheHistoryStartIsZero(t *testing.T) {
	card, rows := snapshotCard()
	card.HistoryStartsOn = aug(3)
	history := []BalancePoint{anchored(derived("acct-card", 2, "-170.00"), 5, "-300.00")}
	require.Equal(t, []string{"acct-card 2026-08-02 0.00"},
		pointStrings(RederiveHistory([]Account{card}, map[ID][]Posting{"acct-card": rows}, history)))
}

func TestAPointOfAnAccountNotAskedAboutIsKept(t *testing.T) {
	history := []BalancePoint{derived("other", 3, "42.00"), observed("other", 4, "0.00")}
	require.Equal(t, pointStrings(history), pointStrings(RederiveHistory(nil, nil, history)))
}
