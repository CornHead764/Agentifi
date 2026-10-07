package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The expected figures below are the bill's own, copied: nothing is computed,
// so every expectation is either the statement's figure or what the account
// already held, as StatementAfterBill's rules say.

func septemberStatement() StatementBill {
	return StatementBill{
		AmountDue: MustFromString("420.00"), MinimumDue: MustFromString("35.00"), HasMinimumDue: true,
		DueOn: NewDate(2026, time.October, 22),
	}
}

func TestAStatementFillsAnAccountThatHoldsNone(t *testing.T) {
	next, changed := StatementAfterBill(CardStatement{}, septemberStatement())
	require.True(t, changed)
	require.Equal(t, CardStatement{
		Balance: MustFromString("420.00"), HasBalance: true,
		MinimumDue: MustFromString("35.00"), HasMinimumDue: true,
		DueOn: NewDate(2026, time.October, 22), FromBill: true,
	}, next)
}

func TestANewerStatementReplacesEverythingEvenAHandEnteredOlderCycle(t *testing.T) {
	typed := CardStatement{
		Balance: MustFromString("380.00"), HasBalance: true,
		MinimumDue: MustFromString("30.00"), HasMinimumDue: true,
		DueOn: NewDate(2026, time.September, 22),
	}
	next, changed := StatementAfterBill(typed, septemberStatement())
	require.True(t, changed)
	require.Equal(t, "420.00", next.Balance.String())
	require.Equal(t, "35.00", next.MinimumDue.String())
	require.Equal(t, NewDate(2026, time.October, 22), next.DueOn)
	require.True(t, next.FromBill)

	// A newer statement that states no minimum clears the old cycle's.
	noMinimum := septemberStatement()
	noMinimum.HasMinimumDue, noMinimum.MinimumDue = false, Money{}
	next, _ = StatementAfterBill(typed, noMinimum)
	require.False(t, next.HasMinimumDue, "last cycle's minimum beside this cycle's due date is a wrong number")
}

func TestAHandEnteredFigureForTheSameCycleStands(t *testing.T) {
	typed := CardStatement{
		Balance: MustFromString("410.00"), HasBalance: true,
		DueOn: NewDate(2026, time.October, 22),
	}
	next, changed := StatementAfterBill(typed, septemberStatement())
	require.False(t, changed)
	require.Equal(t, typed, next)
}

func TestTheSameCycleReadAgainRefreshesWhatABillWrote(t *testing.T) {
	filed := CardStatement{
		Balance: MustFromString("412.00"), HasBalance: true,
		MinimumDue: MustFromString("35.00"), HasMinimumDue: true,
		DueOn: NewDate(2026, time.October, 22), FromBill: true,
	}
	next, changed := StatementAfterBill(filed, septemberStatement())
	require.True(t, changed, "a corrected statement for the cycle a bill wrote")
	require.Equal(t, "420.00", next.Balance.String())

	_, changed = StatementAfterBill(next, septemberStatement())
	require.False(t, changed, "the same statement twice is nothing to write")

	noMinimum := septemberStatement()
	noMinimum.HasMinimumDue, noMinimum.MinimumDue = false, Money{}
	kept, _ := StatementAfterBill(filed, noMinimum)
	require.Equal(t, "35.00", kept.MinimumDue.String(), "the same cycle keeps a minimum the re-read did not state")
}

func TestAnOlderStatementNeverWrites(t *testing.T) {
	for _, fromBill := range []bool{true, false} {
		held := CardStatement{
			Balance: MustFromString("500.00"), HasBalance: true,
			DueOn: NewDate(2026, time.November, 22), FromBill: fromBill,
		}
		next, changed := StatementAfterBill(held, septemberStatement())
		require.False(t, changed)
		require.Equal(t, held, next)
	}
	_, changed := StatementAfterBill(CardStatement{}, StatementBill{AmountDue: MustFromString("1.00")})
	require.False(t, changed, "a bill with no due date writes nothing")
}

func TestAStatementIsStoredAsAMagnitude(t *testing.T) {
	signed := septemberStatement()
	signed.AmountDue, signed.MinimumDue = MustFromString("-420.00"), MustFromString("-35.00")
	next, _ := StatementAfterBill(CardStatement{}, signed)
	require.Equal(t, "420.00", next.Balance.String())
	require.Equal(t, "35.00", next.MinimumDue.String())
}
