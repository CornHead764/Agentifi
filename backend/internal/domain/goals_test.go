package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	goalToday     = NewDate(2026, time.August, 21)
	goalThisMonth = NewMonth(2026, time.August)
)

func testGoal(mutate ...func(*Goal)) Goal {
	goal := Goal{
		ID:              "goal-1",
		Name:            "Emergency fund",
		AccountID:       "acct-savings",
		TargetAmount:    MustFromString("10000.00"),
		TargetOn:        NewDate(2027, time.August, 1),
		IsTakenFromPlan: true,
	}
	for _, apply := range mutate {
		apply(&goal)
	}
	return goal
}

func goalContribution(txnID, amount string, on Date, mutate ...func(*GoalContribution)) GoalContribution {
	// Sign-classified, the Simplifi-import shape: a contribution is the leg
	// that left the funding account, a withdrawal the leg that arrived.
	row := GoalContribution{
		GoalID:          "goal-1",
		TxnID:           ID(txnID),
		AccountID:       "acct-checking",
		On:              on,
		Amount:          MustFromString(amount),
		Kind:            kindBySign(amount),
		IsTakenFromPlan: true,
	}
	for _, apply := range mutate {
		apply(&row)
	}
	return row
}

func kindBySign(amount string) GoalKind {
	if MustFromString(amount).IsPositive() {
		return GoalOut
	}
	return GoalIn
}

func goalContributions() []GoalContribution {
	return []GoalContribution{
		goalContribution("t-1", "-1000.00", NewDate(2026, time.June, 1)),
		goalContribution("t-2", "-1000.00", NewDate(2026, time.July, 1)),
		goalContribution("t-3", "-500.00", NewDate(2026, time.August, 5)),
		goalContribution("t-4", "200.00", NewDate(2026, time.August, 9)),
	}
}

func TestGoalWithdrawalsComeOffTheTotal(t *testing.T) {
	require.Equal(t, "2300.00", SavedSoFar(testGoal(), goalContributions()).String())
}

func TestOnlyThisGoalsTransactionsCount(t *testing.T) {
	other := goalContribution("t-other", "-5000.00", goalToday, func(c *GoalContribution) { c.GoalID = "goal-2" })
	rows := append(goalContributions(), other)

	require.Equal(t, "2300.00", SavedSoFar(testGoal(), rows).String())
}

func TestAContributionReadsAsSavedAndAWithdrawalAsTakenBack(t *testing.T) {
	funded := goalContribution("t-1", "-1000.00", goalToday)
	raided := goalContribution("t-2", "250.00", goalToday)

	require.Equal(t, "1000.00", funded.Saved().String())
	require.Equal(t, "-250.00", raided.Saved().String())
	require.Equal(t, "750.00",
		SavedSoFar(testGoal(), []GoalContribution{funded, raided}).String())
}

// Both rows are 5,000 leaving the funding account; only the recorded Kind
// separates a contribution from a withdrawal.
func TestTheRecordedDirectionDecidesRatherThanTheSign(t *testing.T) {
	intoSavings := goalContribution("t-1", "-5000.00", goalToday)
	spent := goalContribution("t-2", "-5000.00", goalToday, func(c *GoalContribution) {
		c.Kind = GoalOut
	})

	require.Equal(t, "5000.00", intoSavings.Saved().String())
	require.Equal(t, "-5000.00", spent.Saved().String())
	require.Equal(t, "0.00",
		SavedSoFar(testGoal(), []GoalContribution{intoSavings, spent}).String())
}

// The reserve was already reduced by the funding withdrawal; charging the
// purchases again would read 5,500 overdrawn.
func TestSpendingIsTrackedWithoutMovingTheReserve(t *testing.T) {
	spentOnIt := func(c *GoalContribution) { c.Kind = GoalSpent }
	rows := []GoalContribution{
		goalContribution("t-1", "-15000.00", NewDate(2026, time.June, 1)),
		goalContribution("t-2", "15000.00", NewDate(2026, time.August, 1)),
		goalContribution("t-3", "-4000.00", goalToday, spentOnIt),
		goalContribution("t-4", "-2000.00", goalToday, spentOnIt),
		// A cancelled booking, refunded to the same card.
		goalContribution("t-5", "500.00", goalToday, spentOnIt),
	}

	require.Equal(t, "0.00", SavedSoFar(testGoal(), rows).String())
	require.Equal(t, "5500.00", SpentOn(testGoal(), rows).String(), "net of the refund")
	// Spending moves nothing; the withdrawal is August's whole movement.
	require.Equal(t, "-15000.00",
		ContributedThisMonth(testGoal(), rows, goalThisMonth).String())
}

func TestAGoalWithNoSpendingRecordedHasSpentNothing(t *testing.T) {
	require.Equal(t, "0.00", SpentOn(testGoal(), goalContributions()).String())
}

func TestACompletedGoalIsNotReportedAsOwingTwiceItsTarget(t *testing.T) {
	// A 15,000 goal met by one transfer must not read as -15,000 saved.
	goal := testGoal(func(g *Goal) { g.TargetAmount = MustFromString("15000.00") })
	rows := []GoalContribution{goalContribution("t-1", "-15000.00", goalToday)}

	saved := SavedSoFar(goal, rows)
	require.Equal(t, "15000.00", saved.String())
	require.Equal(t, "0.00", LeftToSave(goal, saved).String())
}

func TestAGoalWithNoContributionsHasSavedNothing(t *testing.T) {
	require.Equal(t, "0.00", SavedSoFar(testGoal(), nil).String())
}

// Saved in full, moved out and spent: Funded holds at the target.
func TestAGoalSavedAndThenSpentIsStillFullyFunded(t *testing.T) {
	spentOnIt := func(c *GoalContribution) { c.Kind = GoalSpent }
	rows := []GoalContribution{
		goalContribution("t-1", "-10000.00", NewDate(2026, time.June, 1)),
		goalContribution("t-2", "10000.00", NewDate(2026, time.August, 1)),
		goalContribution("t-3", "-9800.00", goalToday, spentOnIt),
	}
	goal := testGoal()

	require.Equal(t, "0.00", SavedSoFar(goal, rows).String(), "nothing is still set aside")
	require.Equal(t, "10000.00", Withdrawn(goal, rows).String())
	require.Equal(t, "10000.00", Funded(goal, rows).String())

	pct, ok := PctFunded(goal, Funded(goal, rows))
	require.True(t, ok)
	require.Equal(t, "100.00", pct.StringFixed(2))

	progress := GoalProgressFor(goal, rows, goalToday)
	require.False(t, progress.IsComplete(), "the money is gone")
	require.True(t, progress.IsFunded(), "the target was reached")
}

func TestAPartlySpentGoalSplitsTheBarBetweenSetAsideAndWithdrawn(t *testing.T) {
	rows := []GoalContribution{
		goalContribution("t-1", "-4000.00", NewDate(2026, time.June, 1)),
		goalContribution("t-2", "1500.00", NewDate(2026, time.August, 1)),
	}
	goal := testGoal()

	require.Equal(t, "2500.00", SavedSoFar(goal, rows).String())
	require.Equal(t, "1500.00", Withdrawn(goal, rows).String())
	require.Equal(t, "4000.00", Funded(goal, rows).String(), "the two segments together")

	pct, ok := PctFunded(goal, Funded(goal, rows))
	require.True(t, ok)
	require.Equal(t, "40.00", pct.StringFixed(2))
}

func TestSpendingRecordedAgainstAGoalDoesNotLengthenTheBar(t *testing.T) {
	// Spending is not a withdrawal; counting both draws the money twice.
	rows := []GoalContribution{
		goalContribution("t-1", "-4000.00", NewDate(2026, time.June, 1)),
		goalContribution("t-2", "-900.00", goalToday, func(c *GoalContribution) { c.Kind = GoalSpent }),
	}
	goal := testGoal()

	require.Equal(t, "0.00", Withdrawn(goal, rows).String())
	require.Equal(t, "4000.00", Funded(goal, rows).String())
}

func TestAGoalWithNothingWithdrawnIsFundedByWhatIsSaved(t *testing.T) {
	goal := testGoal()
	rows := []GoalContribution{goalContribution("t-1", "-2500.00", goalToday)}

	require.Equal(t, SavedSoFar(goal, rows).String(), Funded(goal, rows).String())
}

func TestLeftToSaveIsTheTargetLessWhatIsSaved(t *testing.T) {
	require.Equal(t, "7700.00", LeftToSave(testGoal(), MustFromString("2300.00")).String())
}

func TestAnOverfundedGoalNeedsNothingMoreRatherThanNegative(t *testing.T) {
	require.Equal(t, "0.00", LeftToSave(testGoal(), MustFromString("12000.00")).String())
}

func TestPctCompleteReadsAsAPercentageOfTheTarget(t *testing.T) {
	pct, ok := PctComplete(testGoal(), MustFromString("2500.00"))

	require.True(t, ok)
	require.Equal(t, "25.00", pct.StringFixed(2))
}

func TestTheDisplayFigureIsClampedAtOneHundred(t *testing.T) {
	pct, ok := PctComplete(testGoal(), MustFromString("12000.00"))

	require.True(t, ok)
	require.Equal(t, "100.00", pct.StringFixed(2))
}

func TestATargetOfZeroHasNoPercentageRatherThanAnInfiniteOne(t *testing.T) {
	goal := testGoal(func(g *Goal) { g.TargetAmount = Zero })

	_, ok := PctComplete(goal, MustFromString("50.00"))

	require.False(t, ok)
}

func TestContributedThisMonthSumsOnlyTheCurrentMonthsMovements(t *testing.T) {
	require.Equal(t, "300.00", ContributedThisMonth(testGoal(), goalContributions(), goalThisMonth).String())
}

func TestAContributionReversedTheNextDayFundedNothing(t *testing.T) {
	rows := []GoalContribution{
		goalContribution("t-a", "-200.00", NewDate(2026, time.August, 3)),
		goalContribution("t-b", "200.00", NewDate(2026, time.August, 4)),
	}

	require.Equal(t, "0.00", ContributedThisMonth(testGoal(), rows, goalThisMonth).String())
}

func TestMonthlyNeededSpreadsTheRemainderOverTheMonthsLeft(t *testing.T) {
	// August 2026 through August 2027 inclusive is 13 months.
	months, ok := MonthsUntil(goalToday, NewDate(2027, time.August, 1))
	require.True(t, ok)
	require.Equal(t, 13, months)

	needed, ok := MonthlyNeeded(testGoal(), MustFromString("2300.00"), goalToday)
	require.True(t, ok)
	require.Equal(t, "592.31", needed.String())
}

func TestTheCurrentMonthCountsBecauseItCanStillBeContributedTo(t *testing.T) {
	months, ok := MonthsUntil(goalToday, NewDate(2026, time.August, 31))

	require.True(t, ok)
	require.Equal(t, 1, months)
}

func TestATargetDateInThePastNeedsTheWholeRemainderNow(t *testing.T) {
	overdue := testGoal(func(g *Goal) { g.TargetOn = NewDate(2026, time.January, 1) })

	months, ok := MonthsUntil(goalToday, overdue.TargetOn)
	require.True(t, ok)
	require.Equal(t, 1, months)

	needed, ok := MonthlyNeeded(overdue, MustFromString("2300.00"), goalToday)
	require.True(t, ok)
	require.Equal(t, "7700.00", needed.String())
}

// The clamp gives "1" for both an overdue and a near deadline.
func TestAPassedTargetDateSaysSoSeparatelyFromTheMonthsLeft(t *testing.T) {
	require.True(t, TargetHasPassed(goalToday, NewDate(2026, time.January, 1)))
	require.False(t, TargetHasPassed(goalToday, goalToday))
	require.False(t, TargetHasPassed(goalToday, NewDate(2027, time.August, 1)))
	require.False(t, TargetHasPassed(goalToday, Date{}))
}

func TestAGoalWithNoTargetDateHasNoRequiredRate(t *testing.T) {
	_, ok := MonthsUntil(goalToday, Date{})
	require.False(t, ok)

	openEnded := testGoal(func(g *Goal) { g.TargetOn = Date{} })
	_, ok = MonthlyNeeded(openEnded, MustFromString("2300.00"), goalToday)
	require.False(t, ok)
}

func TestAMetGoalNeedsNothingAMonth(t *testing.T) {
	needed, ok := MonthlyNeeded(testGoal(), MustFromString("10000.00"), goalToday)

	require.True(t, ok)
	require.Equal(t, "0.00", needed.String())
}

func TestTheGoalCardReportsEveryFigureFromOnePass(t *testing.T) {
	card := GoalProgressFor(testGoal(), goalContributions(), goalToday)

	require.Equal(t, "2300.00", card.SavedSoFar.String())
	require.Equal(t, "300.00", card.ContributedThisMonth.String())
	require.Equal(t, "7700.00", card.LeftToSave().String())
	require.True(t, card.HasMonthlyNeeded)
	require.Equal(t, "592.31", card.MonthlyNeeded.String())
	require.False(t, card.IsComplete())

	pct, ok := card.PctComplete()
	require.True(t, ok)
	require.Equal(t, "23.00", pct.StringFixed(2))
}

func TestAGoalThatReachedItsTargetIsComplete(t *testing.T) {
	met := testGoal(func(g *Goal) { g.TargetAmount = MustFromString("2300.00") })

	require.True(t, GoalProgressFor(met, goalContributions(), goalToday).IsComplete())
}

func TestTheReserveLandsOnlyOnTheAccountTheGoalNames(t *testing.T) {
	// Reserving in every funding account would subtract the same savings from
	// each one's available balance.
	multi := testGoal(func(g *Goal) {
		g.FundingAccountIDs = []ID{"acct-savings", "acct-checking"}
	})
	goals := []Goal{multi}

	require.Equal(t, "2300.00", ReservedInAccount("acct-savings", goals, goalContributions()).String())
	require.Equal(t, "0.00", ReservedInAccount("acct-checking", goals, goalContributions()).String())
}

func TestSeveralGoalsInOneAccountAddUp(t *testing.T) {
	second := testGoal(func(g *Goal) { g.ID = "goal-2" })
	rows := append(goalContributions(), goalContribution("t-5", "-400.00", goalToday, func(c *GoalContribution) {
		c.GoalID = "goal-2"
	}))

	require.Equal(t, "2700.00", ReservedInAccount("acct-savings", []Goal{testGoal(), second}, rows).String())
}

func goalSpendRow(txnID, amount string) GoalContribution {
	return goalContribution(txnID, amount, goalToday, func(c *GoalContribution) {
		c.Kind = GoalSpent
	})
}

func goalSpendTxn(txnID, amount, categoryID string) Transaction {
	return Transaction{
		ID:         ID(txnID),
		AccountID:  "acct-checking",
		Date:       goalToday,
		Amount:     MustFromString(amount),
		CategoryID: ID(categoryID),
	}
}

func TestTheBreakdownGroupsAGoalsSpendingByCategoryLargestFirst(t *testing.T) {
	rows := []GoalContribution{
		goalSpendRow("t-flights", "-1200.00"),
		goalSpendRow("t-hotel", "-800.00"),
		goalSpendRow("t-flights-2", "-300.00"),
	}
	txns := []Transaction{
		goalSpendTxn("t-flights", "-1200.00", "cat-airfare"),
		goalSpendTxn("t-hotel", "-800.00", "cat-lodging"),
		goalSpendTxn("t-flights-2", "-300.00", "cat-airfare"),
	}

	lines := SpendingByCategory(testGoal(), rows, txns)

	require.Equal(t, []GoalCategorySpend{
		{CategoryID: "cat-airfare", Spent: MustFromString("1500.00"), TxnCount: 2},
		{CategoryID: "cat-lodging", Spent: MustFromString("800.00"), TxnCount: 1},
	}, lines)
}

func TestTheBreakdownSumsToSpentOnAndIgnoresTheReserveMoving(t *testing.T) {
	rows := append(goalContributions(), goalSpendRow("t-flights", "-1200.00"))
	txns := []Transaction{goalSpendTxn("t-flights", "-1200.00", "cat-airfare")}

	lines := SpendingByCategory(testGoal(), rows, txns)

	require.Len(t, lines, 1)
	require.Equal(t, SpentOn(testGoal(), rows).String(), lines[0].Spent.String())
}

// A refund lowers its category: the ledger sign is negated, not folded.
func TestARefundReducesItsCategoryInTheBreakdown(t *testing.T) {
	rows := []GoalContribution{
		goalSpendRow("t-flights", "-1200.00"),
		goalSpendRow("t-refund", "200.00"),
	}
	txns := []Transaction{
		goalSpendTxn("t-flights", "-1200.00", "cat-airfare"),
		goalSpendTxn("t-refund", "200.00", "cat-airfare"),
	}

	lines := SpendingByCategory(testGoal(), rows, txns)

	require.Equal(t, "1000.00", lines[0].Spent.String())
}

func TestASplitChargeAppearsUnderEveryCategoryItTouches(t *testing.T) {
	rows := []GoalContribution{goalSpendRow("t-package", "-1000.00")}
	txn := goalSpendTxn("t-package", "-1000.00", "cat-airfare")
	txn.Splits = []Split{
		{ID: "s-1", Amount: MustFromString("-600.00"), CategoryID: "cat-airfare"},
		{ID: "s-2", Amount: MustFromString("-400.00"), CategoryID: "cat-lodging"},
	}

	lines := SpendingByCategory(testGoal(), rows, []Transaction{txn})

	require.Equal(t, []GoalCategorySpend{
		{CategoryID: "cat-airfare", Spent: MustFromString("600.00"), TxnCount: 1},
		{CategoryID: "cat-lodging", Spent: MustFromString("400.00"), TxnCount: 1},
	}, lines)
}

// Kept so the parts sum to the total.
func TestAnUncategorizedChargeKeepsItsLineInTheBreakdown(t *testing.T) {
	rows := []GoalContribution{goalSpendRow("t-misc", "-50.00")}

	lines := SpendingByCategory(testGoal(), rows, []Transaction{goalSpendTxn("t-misc", "-50.00", "")})

	require.Equal(t, []GoalCategorySpend{{Spent: MustFromString("50.00"), TxnCount: 1}}, lines)
}

func TestTheBreakdownReadsOnlyItsOwnGoal(t *testing.T) {
	other := goalSpendRow("t-other", "-900.00")
	other.GoalID = "goal-2"
	rows := []GoalContribution{goalSpendRow("t-flights", "-100.00"), other}
	txns := []Transaction{
		goalSpendTxn("t-flights", "-100.00", "cat-airfare"),
		goalSpendTxn("t-other", "-900.00", "cat-airfare"),
	}

	lines := SpendingByCategory(testGoal(), rows, txns)

	require.Equal(t, []GoalCategorySpend{
		{CategoryID: "cat-airfare", Spent: MustFromString("100.00"), TxnCount: 1},
	}, lines)
}
