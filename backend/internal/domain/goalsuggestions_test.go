package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Invented ledger around lifeGoal: savings is the reserve, checking funds it
// and receives the withdrawals, the card pays for the trip.

var (
	suggestTransfer = Category{ID: "cat-transfer", Name: "Transfer", Kind: CategoryTransfer}
	suggestAirfare  = Category{ID: "cat-air", Name: "Airfare", Kind: CategoryExpense}
	suggestHotel    = Category{ID: "cat-hotel", Name: "Hotel", Kind: CategoryExpense}
	suggestGrocery  = Category{ID: "cat-grocery", Name: "Groceries", Kind: CategoryExpense}
)

func suggestRow(id, account, amount string, on Date, mutate ...func(*Posting)) Posting {
	p := Posting{
		Txn: Transaction{
			ID: ID(id), AccountID: ID(account), Date: on, Amount: MustFromString(amount),
			Payee: "Payee " + id,
		},
		Account: Account{ID: ID(account)},
	}
	for _, apply := range mutate {
		apply(&p)
	}
	return p
}

func filed(category Category) func(*Posting) {
	return func(p *Posting) {
		p.Txn.CategoryID = category.ID
		p.Category, p.HasCategory = category, true
	}
}

func paired(token string) func(*Posting) {
	return func(p *Posting) { p.Txn.TransferPairID = ID(token) }
}

func payee(name string) func(*Posting) {
	return func(p *Posting) { p.Txn.Payee = name }
}

func suggestedIDs(rows []GoalSuggestion) []ID {
	out := make([]ID, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Posting.Txn.ID)
	}
	return out
}

func suggestInputs(mine []GoalContribution, counted []Transaction, candidates ...Posting) GoalSuggestionInputs {
	goal := lifeGoal(func(g *Goal) { g.FundingAccountIDs = []ID{"acct-savings", "acct-checking"} })
	return GoalSuggestionInputs{
		Goal: goal, Mine: mine, Counted: counted, Candidates: candidates, Today: goalToday,
	}
}

func TestContributionsAreDepositsIntoTheReserveBeforeTheFirstWithdrawal(t *testing.T) {
	in := suggestInputs(lifeRows(lifeSaved[:1], lifeWithdrawn),
		[]Transaction{{ID: "in-1"}, {ID: "out-1"}, {ID: "out-2"}},
		suggestRow("dep-1", "acct-savings", "400.00", NewDate(2026, time.June, 1), filed(suggestTransfer)),
		suggestRow("interest", "acct-savings", "1.25", NewDate(2026, time.June, 30)),
		// After the first withdrawal (July 10): not a contribution to suggest.
		suggestRow("dep-late", "acct-savings", "50.00", NewDate(2026, time.August, 1)),
		// Money leaving the reserve and money arriving elsewhere are not.
		suggestRow("out-x", "acct-savings", "-20.00", NewDate(2026, time.June, 5)),
		suggestRow("pay", "acct-checking", "900.00", NewDate(2026, time.June, 1)),
		// Already counted.
		suggestRow("in-1", "acct-savings", "600.00", NewDate(2026, time.May, 1)),
	)

	// The transfer first, then the newest.
	require.Equal(t, []ID{"dep-1", "interest"}, suggestedIDs(SuggestGoalRows(GoalIn, in)))
}

func TestWithdrawalsAreMoneyLeavingTheReserveAfterTheFirstContribution(t *testing.T) {
	in := suggestInputs(lifeSaved, []Transaction{{ID: "in-1"}, {ID: "in-2"}},
		suggestRow("to-checking", "acct-savings", "-500.00", NewDate(2026, time.July, 10), paired("p-1")),
		suggestRow("from-savings", "acct-checking", "500.00", NewDate(2026, time.July, 10), paired("p-1")),
		// Before the first contribution (May 1).
		suggestRow("old-out", "acct-savings", "-75.00", NewDate(2026, time.April, 2)),
		// A funding account's transfer into the reserve is a contribution.
		suggestRow("to-savings", "acct-checking", "-300.00", NewDate(2026, time.June, 3), paired("p-2")),
		suggestRow("into-savings", "acct-savings", "300.00", NewDate(2026, time.June, 3), paired("p-2")),
		// A funding account's transfer elsewhere may be the goal's money moving on.
		suggestRow("to-brokerage", "acct-checking", "-200.00", NewDate(2026, time.July, 1), filed(suggestTransfer)),
		// A funding account's purchase is spending, not a withdrawal.
		suggestRow("groceries", "acct-checking", "-60.00", NewDate(2026, time.July, 2), filed(suggestGrocery)),
	)

	require.Equal(t, []ID{"to-checking", "to-brokerage"}, suggestedIDs(SuggestGoalRows(GoalOut, in)))
}

func TestTheOtherLegOfACountedTransferIsNotSuggested(t *testing.T) {
	in := suggestInputs(lifeSaved, []Transaction{
		{ID: "in-1"}, {ID: "in-2"}, {ID: "from-savings", TransferPairID: "p-1"},
	},
		suggestRow("to-checking", "acct-savings", "-500.00", NewDate(2026, time.July, 10), paired("p-1")),
	)

	require.Empty(t, SuggestGoalRows(GoalOut, in))
}

func TestSpendingIsLookedForAroundTheWithdrawals(t *testing.T) {
	from, to, ok := GoalSuggestionWindow(GoalSpent, lifeGoal(), lifeRows(lifeSaved, lifeWithdrawn), goalToday)

	require.True(t, ok)
	// Fourteen days before July 10; sixty after the December 1 target, which
	// is later than the last withdrawal.
	require.Equal(t, NewDate(2026, time.June, 26), from)
	require.Equal(t, NewDate(2027, time.January, 30), to)
}

func TestWithoutATargetDateSpendingRunsPastTheLastWithdrawal(t *testing.T) {
	open := lifeGoal(func(g *Goal) { g.TargetOn = Date{} })
	_, to, ok := GoalSuggestionWindow(GoalSpent, open, lifeRows(lifeSaved, lifeWithdrawn), goalToday)

	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.September, 18), to)
}

func TestAGoalWithNoRowsHasNoSpendingToSuggest(t *testing.T) {
	_, _, ok := GoalSuggestionWindow(GoalSpent, lifeGoal(), nil, goalToday)

	require.False(t, ok)
}

func TestSpendingIsRankedByResemblanceAndNearnessToAWithdrawal(t *testing.T) {
	counted := []Transaction{
		{ID: "in-1"}, {ID: "in-2"}, {ID: "out-1"}, {ID: "out-2"},
		{ID: "buy-1", CategoryID: suggestAirfare.ID, Payee: "Skyline Air"},
	}
	in := suggestInputs(lifeRows(lifeSaved, lifeWithdrawn, lifeSpent[:1]), counted,
		// Same category, within a month of a withdrawal: category outranks payee.
		suggestRow("air-2", "acct-card", "-310.00", NewDate(2026, time.August, 15), filed(suggestAirfare)),
		// Same payee, two days from a withdrawal.
		suggestRow("air-3", "acct-card", "-90.00", NewDate(2026, time.July, 22), payee("SKYLINE AIR")),
		// Nothing alike, a day from a withdrawal.
		suggestRow("hotel", "acct-card", "-240.00", NewDate(2026, time.July, 11), filed(suggestHotel)),
		// Nothing alike, far from both.
		suggestRow("market", "acct-checking", "-55.00", NewDate(2026, time.October, 30), filed(suggestGrocery)),
		// Never suggested: a card payment filed as a transfer, its paired leg,
		// money arriving, the reserve's own outflow, a row before the window.
		suggestRow("card-pay", "acct-checking", "-800.00", NewDate(2026, time.August, 1), filed(suggestTransfer)),
		suggestRow("card-in", "acct-card", "800.00", NewDate(2026, time.August, 1), paired("p-9")),
		suggestRow("refund", "acct-card", "30.00", NewDate(2026, time.August, 3)),
		suggestRow("from-reserve", "acct-savings", "-40.00", NewDate(2026, time.August, 4)),
		suggestRow("too-early", "acct-card", "-70.00", NewDate(2026, time.June, 1)),
	)

	rows := SuggestGoalRows(GoalSpent, in)
	require.Equal(t, []ID{"air-2", "air-3", "hotel", "market"}, suggestedIDs(rows))
	require.True(t, rows[0].MatchesCategory)
	require.True(t, rows[1].MatchesPayee)
	require.Equal(t, 2, rows[1].DaysFromWithdrawal)
	require.Equal(t, 1, rows[2].DaysFromWithdrawal)
}

func TestARowAnotherGoalCountsIsNotSuggested(t *testing.T) {
	in := suggestInputs(lifeRows(lifeSaved, lifeWithdrawn), []Transaction{{ID: "elsewhere"}},
		suggestRow("elsewhere", "acct-card", "-90.00", NewDate(2026, time.July, 15)),
	)

	require.Empty(t, SuggestGoalRows(GoalSpent, in))
}
