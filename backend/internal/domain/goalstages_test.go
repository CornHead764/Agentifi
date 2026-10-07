package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The goal's life, in invented figures over a 1,000.00 target saved in
// acct-savings: two contributions of 600.00 and 400.00 fund it, two
// withdrawals of 500.00 and 300.00 move 800.00 to checking, and 650.00 of
// purchases on a card are filed as spending on it.

func lifeGoal(mutate ...func(*Goal)) Goal {
	return testGoal(append([]func(*Goal){func(g *Goal) {
		g.TargetAmount = MustFromString("1000.00")
		g.TargetOn = NewDate(2026, time.December, 1)
	}}, mutate...)...)
}

func lifeContribution(txnID, amount string, kind GoalKind, on Date) GoalContribution {
	return goalContribution(txnID, amount, on, func(c *GoalContribution) {
		c.Kind = kind
		c.AccountID = "acct-savings"
	})
}

var (
	lifeSaved = []GoalContribution{
		lifeContribution("in-1", "600.00", GoalIn, NewDate(2026, time.May, 1)),
		lifeContribution("in-2", "400.00", GoalIn, NewDate(2026, time.June, 1)),
	}
	lifeWithdrawn = []GoalContribution{
		lifeContribution("out-1", "-500.00", GoalOut, NewDate(2026, time.July, 10)),
		lifeContribution("out-2", "-300.00", GoalOut, NewDate(2026, time.July, 20)),
	}
	lifeSpent = []GoalContribution{
		lifeContribution("buy-1", "-450.00", GoalSpent, NewDate(2026, time.July, 12)),
		lifeContribution("buy-2", "-200.00", GoalSpent, NewDate(2026, time.July, 22)),
	}
)

func lifeRows(groups ...[]GoalContribution) []GoalContribution {
	var out []GoalContribution
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func TestAGoalShortOfItsTargetIsSaving(t *testing.T) {
	card := GoalProgressFor(lifeGoal(), lifeSaved[:1], goalToday)

	require.Equal(t, GoalStageSaving, card.Stage())
	require.True(t, card.HasMonthlyNeeded)
	require.Equal(t, "400.00", card.LeftToSave().String())
}

func TestAGoalThatReachedItsTargetWithNothingTakenOutIsFunded(t *testing.T) {
	card := GoalProgressFor(lifeGoal(), lifeSaved, goalToday)

	require.Equal(t, GoalStageFunded, card.Stage())
	require.False(t, card.HasMonthlyNeeded)
}

func TestAGoalDrawnOnIsSpendingAndAsksForNothingMore(t *testing.T) {
	card := GoalProgressFor(lifeGoal(), lifeRows(lifeSaved, lifeWithdrawn, lifeSpent), goalToday)

	require.Equal(t, GoalStageSpending, card.Stage())
	// Saved 1,000.00 and took 800.00 out: 200.00 still in the account, and
	// the target was met, so nothing is asked for although saved < target.
	require.Equal(t, "200.00", card.SavedSoFar.String())
	require.True(t, card.IsFunded())
	require.False(t, card.HasMonthlyNeeded)
}

func TestAGoalDrawnOnBeforeItWasFundedIsSpendingToo(t *testing.T) {
	card := GoalProgressFor(lifeGoal(), lifeRows(lifeSaved[:1], lifeWithdrawn[:1]), goalToday)

	require.Equal(t, GoalStageSpending, card.Stage())
	require.False(t, card.IsFunded())
}

func TestSpendingFiledWithNothingWithdrawnIsSpending(t *testing.T) {
	card := GoalProgressFor(lifeGoal(), lifeRows(lifeSaved[:1], lifeSpent[:1]), goalToday)

	require.Equal(t, GoalStageSpending, card.Stage())
}

func TestAZeroTargetIsNeverFunded(t *testing.T) {
	open := lifeGoal(func(g *Goal) { g.TargetAmount = Zero })

	require.Equal(t, GoalStageSaving, GoalProgressFor(open, lifeSaved, goalToday).Stage())
}

func TestAClosedGoalIsClosedWhateverItsFigures(t *testing.T) {
	closed := lifeGoal(func(g *Goal) { g.ClosedOn = NewDate(2026, time.August, 1) })
	card := GoalProgressFor(closed, lifeRows(lifeSaved[:1]), goalToday)

	require.Equal(t, GoalStageClosed, card.Stage())
	require.False(t, card.HasMonthlyNeeded)
	// Its history stays readable.
	require.Equal(t, "600.00", card.SavedSoFar.String())
}

func TestUnassignedWithdrawnIsWithdrawnLessSpent(t *testing.T) {
	card := GoalProgressFor(lifeGoal(), lifeRows(lifeSaved, lifeWithdrawn, lifeSpent), goalToday)

	require.Equal(t, "800.00", card.Withdrawn.String())
	require.Equal(t, "650.00", card.SpentOnGoal.String())
	require.Equal(t, "150.00", card.UnassignedWithdrawn().String())
}

func TestSpendingBeyondTheWithdrawalsLeavesNothingUnassigned(t *testing.T) {
	require.Equal(t, "0.00",
		UnassignedWithdrawn(MustFromString("300.00"), MustFromString("650.00")).String())
}

func TestNothingWithdrawnLeavesNothingUnassigned(t *testing.T) {
	require.Equal(t, "0.00", UnassignedWithdrawn(Zero, Zero).String())
}

func TestAClosedGoalReservesNothingInItsAccount(t *testing.T) {
	open := lifeGoal()
	closed := lifeGoal(func(g *Goal) {
		g.ID = "goal-2"
		g.ClosedOn = NewDate(2026, time.August, 1)
	})
	rows := lifeRows(lifeSaved, []GoalContribution{
		goalContribution("in-3", "-250.00", NewDate(2026, time.June, 2), func(c *GoalContribution) {
			c.GoalID = "goal-2"
		}),
	})

	// The open goal's 1,000.00 stays reserved; the closed one's 250.00 is
	// available again.
	require.Equal(t, "1000.00",
		ReservedInAccount("acct-savings", []Goal{open, closed}, rows).String())
}

func TestAClosedGoalStopsCountingInTheGoalsBucketFromTheMonthItClosed(t *testing.T) {
	row := GoalContribution{
		GoalID: "g-1", TxnID: "t-goal", Amount: MustFromString("-200"), IsTakenFromPlan: true,
		On: NewDate(2026, time.August, 15), GoalClosedOn: NewDate(2026, time.August, 20),
	}
	require.False(t, row.CountsInGoalsBucket(NewMonth(2026, time.August)))

	closedLater := row
	closedLater.GoalClosedOn = NewDate(2026, time.September, 2)
	require.True(t, closedLater.CountsInGoalsBucket(NewMonth(2026, time.August)))
}

func TestAClosedGoalContributesNothingToThisMonthsGoalsBucket(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-goal", "-200", planUncategorized)}
		in.GoalContributions = []GoalContribution{{
			GoalID: "g-1", TxnID: "t-goal", Amount: MustFromString("-200"), IsTakenFromPlan: true,
			AccountID: "acct-checking", On: NewDate(2026, time.August, 15),
			GoalClosedOn: NewDate(2026, time.August, 20),
		}}
	}), Zero, nil)

	require.Equal(t, "0.00", month.Bucket(BucketGoals).CalculatedAmount.String())
	// Still the goal's row, so it does not fall into Other Spend either.
	require.Equal(t, "0.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
}
