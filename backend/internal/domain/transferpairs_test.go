package domain

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Transfer pairing: which two rows are the halves of one movement.

var (
	checkingID = ID("checking")
	savingsID  = ID("savings")
	cardID     = ID("card")
)

type legOption func(*TransferLeg)

func inCurrency(code string) legOption {
	return func(l *TransferLeg) { l.Currency = code }
}

func inCard() legOption {
	return func(l *TransferLeg) { l.AccountIsCard = true }
}

func leg(number byte, accountID ID, amount string, day time.Time, opts ...legOption) TransferLeg {
	out := TransferLeg{
		ID:        ID(fmt.Sprintf("leg-%02d", number)),
		AccountID: accountID,
		Amount:    MustFromString(amount),
		Currency:  "USD",
		On:        DateOf(day),
	}
	for _, opt := range opts {
		opt(&out)
	}
	return out
}

func march(day int) time.Time { return time.Date(2026, time.March, day, 0, 0, 0, 0, time.UTC) }

func TestADebitAndTheMatchingCreditPair(t *testing.T) {
	legs := []TransferLeg{
		leg(10, checkingID, "-500.00", march(2)),
		leg(11, savingsID, "500.00", march(3)),
	}
	pairs := PlanPairs(legs, PairOptions{})
	require.Len(t, pairs, 1)
	require.Equal(t, legs[0].ID, pairs[0].PayingID)
	require.Equal(t, legs[1].ID, pairs[0].ReceivingID)
	require.Equal(t, 1, pairs[0].DaysApart)
}

func TestADebitInACardAccountIsNeverAPayingLeg(t *testing.T) {
	// A card charge is a purchase. Pairing it with a same-sized deposit a day
	// later deletes both from profit and loss — the spend stops being spend and
	// the repayment stops being income.
	legs := []TransferLeg{
		leg(10, cardID, "-42.00", march(2), inCard()),
		leg(11, checkingID, "42.00", march(3)),
	}
	require.Empty(t, PlanPairs(legs, PairOptions{}))
}

func TestACardPaymentStillPairsTheOtherWayRound(t *testing.T) {
	// The asymmetry is one-directional: money *into* a card is a real transfer
	// and both legs have to stop counting.
	legs := []TransferLeg{
		leg(10, checkingID, "-300.00", march(2)),
		leg(11, cardID, "300.00", march(4), inCard()),
	}
	pairs := PlanPairs(legs, PairOptions{})
	require.Len(t, pairs, 1)
	require.Equal(t, legs[0].ID, pairs[0].PayingID)
	require.Equal(t, legs[1].ID, pairs[0].ReceivingID)
}

func TestTheSameFigureInTwoCurrenciesIsNotTheSameMoney(t *testing.T) {
	legs := []TransferLeg{
		leg(10, checkingID, "-100.00", march(2)),
		leg(11, savingsID, "100.00", march(2), inCurrency("EUR")),
	}
	require.Empty(t, PlanPairs(legs, PairOptions{}))
}

func TestTwoRowsInOneAccountAreNotATransfer(t *testing.T) {
	legs := []TransferLeg{
		leg(10, checkingID, "-100.00", march(2)),
		leg(11, checkingID, "100.00", march(2)),
	}
	require.Empty(t, PlanPairs(legs, PairOptions{}))
}

func TestAGapWiderThanTheToleranceIsNotAPair(t *testing.T) {
	legs := []TransferLeg{
		leg(10, checkingID, "-100.00", march(1)),
		leg(11, savingsID, "100.00", march(8)),
	}
	require.Empty(t, PlanPairs(legs, PairOptions{}))
}

func TestTheClosestCreditWinsAndEachLegPairsOnce(t *testing.T) {
	legs := []TransferLeg{
		leg(10, checkingID, "-100.00", march(3)),
		leg(11, savingsID, "100.00", march(1)),
		leg(12, savingsID, "100.00", march(4)),
	}
	pairs := PlanPairs(legs, PairOptions{})
	require.Len(t, pairs, 1)
	require.Equal(t, ID("leg-12"), pairs[0].ReceivingID)
}

func TestAmountsBucketByValueRatherThanByFloat(t *testing.T) {
	// -0.30 and 0.3 are one amount; a float or a raw string key would split them.
	legs := []TransferLeg{
		leg(10, checkingID, "-0.30", march(2)),
		leg(11, savingsID, "0.3", march(2)),
	}
	require.Len(t, PlanPairs(legs, PairOptions{}), 1)
}

func TestCandidatesMayMatchHistoryButHistoryMayNotMatchItself(t *testing.T) {
	fresh := leg(12, savingsID, "100.00", march(3))
	legs := []TransferLeg{
		leg(10, checkingID, "-100.00", march(2)),
		// Two rows that have sat unpaired: pairing them now would rewrite
		// history nobody touched.
		leg(11, savingsID, "250.00", march(2)),
		leg(13, checkingID, "-250.00", march(2)),
		fresh,
	}
	pairs := PlanPairs(legs, PairOptions{CandidateIDs: []ID{fresh.ID}})
	require.Len(t, pairs, 1)
	require.Equal(t, ID("leg-10"), pairs[0].PayingID)
	require.Equal(t, fresh.ID, pairs[0].ReceivingID)
}

func TestAnEmptyCandidateListProposesNothing(t *testing.T) {
	legs := []TransferLeg{
		leg(10, checkingID, "-500.00", march(2)),
		leg(11, savingsID, "500.00", march(3)),
	}
	require.Empty(t, PlanPairs(legs, PairOptions{CandidateIDs: []ID{}}))
}
