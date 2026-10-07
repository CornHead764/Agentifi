package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Savings goals, end to end.
//
// Two readings of one figure meet in this resource and they are not the same.
// The spending plan's Goals bucket wants money spent on goals, so it is
// negative there; progress is about money accumulated, so it is positive. Every
// test here that names an amount is also asserting which of the two it is.
//
// Which way a row counts is recorded when it is linked, not read off its sign —
// see TestANegativeRowCountsAsAWithdrawalWhenTheLinkSaysSo for why the sign
// cannot answer it. A link that names no direction still gets the sign, which
// is what the tests below that omit it are relying on.
//
// The other rule is the one about a goal being a reserve inside a real account
// rather than a ledger of its own: one pot of money shown twice, never two.

// seedContribution is money leaving the checking account into a goal.
//
// The funding leg, which is the shape a contribution entered here takes: the
// money leaves the account funding the goal, so the amount is negative and the
// goal reserves somewhere else. The other shape — the leg posted on the goal's
// own account, positive, which is what the Simplifi import brings across — is
// covered by its own test below and by domain.GoalContribution.Saved.
func seedContribution(l *ledger, key string, on domain.Date, amount string) uuid.UUID {
	return seedTxn(l, key, &store.Transaction{
		AccountID: l.id("checking"), Date: on,
		Amount: domain.MustFromString(amount), StatementName: "TRANSFER TO SAVINGS",
		Payee: "Savings",
	})
}

// seedCardCharge is a purchase on an account the goal never reserves in — the
// shape of the spending a goal records rather than moves. A nil categoryID
// leaves the charge uncategorized.
func seedCardCharge(l *ledger, accountID, key string, on domain.Date, amount string, categoryID uuid.UUID) uuid.UUID {
	return seedTxn(l, key, &store.Transaction{
		AccountID: uuid.MustParse(accountID), Date: on,
		Amount: domain.MustFromString(amount), StatementName: "ISLAND RESORT",
		Payee: "Island Resort", CategoryID: categoryID,
	})
}

// savingsAccount is where a goal in these tests reserves.
//
// It has to be an account other than the one the contribution leaves, or the
// fixture says the money both left checking and is still reserved inside it --
// which subtracts it twice and describes nothing that can happen.
func savingsAccount(l *ledger) string {
	l.t.Helper()
	return newAccount(l, "Rainy Day Savings", string(domain.KindCash), "savings", "0.00")
}

func goalList(l *ledger) []map[string]any {
	l.t.Helper()
	return l.alex.get("/goals").requireStatus(http.StatusOK).list()
}

func newGoal(l *ledger, body map[string]any) map[string]any {
	l.t.Helper()
	return l.alex.post("/goals", body).requireStatus(http.StatusCreated).json()
}

func goalFunding(t *testing.T, goal map[string]any) []map[string]any {
	t.Helper()
	rows, ok := goal["funding"].([]any)
	require.True(t, ok, "the goal carries no funding rows: %v", goal)
	out := []map[string]any{}
	for _, raw := range rows {
		out = append(out, raw.(map[string]any))
	}
	return out
}

// emergencyFund is a goal part-funded by one 40.00 contribution in August.
func emergencyFund(l *ledger, overrides map[string]any) map[string]any {
	l.t.Helper()
	seedContribution(l, "contribution", domain.NewDate(2026, time.August, 8), "-40.00")
	body := map[string]any{
		"name":          "Emergency Fund",
		"account_id":    savingsAccount(l),
		"target_amount": "1000.00",
		"target_on":     "2026-12-31",
		"txn_ids":       []string{l.str("contribution")},
	}
	for key, value := range overrides {
		body[key] = value
	}
	return newGoal(l, body)
}

// --- Progress ----------------------------------------------------------------

func TestAGoalsProgressReadsPositiveWhileItsContributionPostsNegative(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, nil)

	require.Equal(t, "40.00", goal["saved_so_far"])
	require.Equal(t, "960.00", goal["left_to_save"])
	require.Equal(t, "40.00", goal["contributed_this_month"])
	require.Equal(t, false, goal["is_complete"])
	requireRate(t, "4", goal["pct_complete"])
	// August through December inclusive is five months to save 960.00 in.
	require.Equal(t, "192.00", goal["monthly_needed"])
	require.Equal(t, []any{l.str("contribution")}, goal["txn_ids"])
}

func TestAWithdrawalReducesWhatAGoalHasSaved(t *testing.T) {
	// A goal that has been raided is not a goal that has been met.
	l := planLedger(t)
	seedContribution(l, "contribution", domain.NewDate(2026, time.August, 8), "-40.00")
	seedContribution(l, "withdrawal", domain.NewDate(2026, time.August, 15), "15.00")

	goal := newGoal(l, map[string]any{
		"name": "Emergency Fund", "account_id": savingsAccount(l),
		"target_amount": "1000.00",
		"txn_ids":       []string{l.str("contribution"), l.str("withdrawal")},
	})
	require.Equal(t, "25.00", goal["saved_so_far"])
	require.Equal(t, "975.00", goal["left_to_save"])
}

func TestAnOverfundedGoalNeedsNothingMoreRatherThanLessThanNothing(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, map[string]any{"target_amount": "30.00"})

	require.Equal(t, "40.00", goal["saved_so_far"])
	require.Equal(t, "0.00", goal["left_to_save"])
	require.Equal(t, true, goal["is_complete"])
	// The bar cannot draw past its end; saved_so_far stays the unclamped truth.
	requireRate(t, "100", goal["pct_complete"])
}

func TestAGoalWithNoTargetAmountHasNoPercentageRatherThanAnInfiniteOne(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, map[string]any{"target_amount": "0.00"})
	require.Nil(t, goal["pct_complete"])
}

func TestAnOpenEndedGoalHasNoRequiredMonthlyRate(t *testing.T) {
	// No target date means no deadline, and rendering a rate would invent one.
	l := planLedger(t)
	goal := emergencyFund(l, map[string]any{"target_on": nil})
	require.Nil(t, goal["target_on"])
	require.Nil(t, goal["monthly_needed"])
}

func TestATargetDateInThisMonthLeavesOneMonthAndNotZero(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, map[string]any{"target_on": "2026-08-31"})
	require.Equal(t, "960.00", goal["monthly_needed"])
}

func TestAWithdrawalIsReportedAsSpentAndNotOnlyNettedAway(t *testing.T) {
	// The card prints "Spent" beside "Available", and saved_so_far is already
	// net of the withdrawal — so without this figure the two amounts differ by
	// money the screen never accounts for.
	l := planLedger(t)
	seedContribution(l, "contribution", domain.NewDate(2026, time.August, 8), "-40.00")
	seedContribution(l, "withdrawal", domain.NewDate(2026, time.August, 15), "15.00")

	goal := newGoal(l, map[string]any{
		"name": "Emergency Fund", "account_id": savingsAccount(l),
		"target_amount": "1000.00",
		"txn_ids":       []string{l.str("contribution"), l.str("withdrawal")},
	})
	require.Equal(t, "25.00", goal["saved_so_far"])
	// Positive: a negative here would read as a second contribution.
	require.Equal(t, "15.00", goal["withdrawn"])
}

// The bar's length, and the reason it is not saved_so_far.
//
// A household that saved the whole thousand, moved it to the account it would
// spend from and spent it has met this goal. Drawn off saved_so_far the bar
// empties as the money goes and ends at nothing, which says the saving never
// happened.
func TestAGoalSavedAndThenSpentStillReadsAsFullyFunded(t *testing.T) {
	l := planLedger(t)
	seedContribution(l, "contribution", domain.NewDate(2026, time.August, 8), "-1000.00")
	seedContribution(l, "withdrawal", domain.NewDate(2026, time.August, 15), "1000.00")

	goal := newGoal(l, map[string]any{
		"name": "Trip", "account_id": savingsAccount(l),
		"target_amount": "1000.00",
		"txn_ids":       []string{l.str("contribution"), l.str("withdrawal")},
	})

	require.Equal(t, "0.00", goal["saved_so_far"], "nothing is still set aside")
	require.Equal(t, "1000.00", goal["withdrawn"])
	require.Equal(t, "1000.00", goal["funded"], "but the target was reached")
	requireRate(t, "0", goal["pct_complete"])
	requireRate(t, "100", goal["pct_funded"])
	require.Equal(t, false, goal["is_complete"])
	require.Equal(t, true, goal["is_funded"])
}

func TestAPartlySpentGoalSplitsItsBarBetweenSetAsideAndWithdrawn(t *testing.T) {
	l := planLedger(t)
	seedContribution(l, "contribution", domain.NewDate(2026, time.August, 8), "-400.00")
	seedContribution(l, "withdrawal", domain.NewDate(2026, time.August, 15), "150.00")

	goal := newGoal(l, map[string]any{
		"name": "Trip", "account_id": savingsAccount(l),
		"target_amount": "1000.00",
		"txn_ids":       []string{l.str("contribution"), l.str("withdrawal")},
	})

	require.Equal(t, "250.00", goal["saved_so_far"])
	require.Equal(t, "400.00", goal["funded"], "the two segments together")
	requireRate(t, "25", goal["pct_complete"])
	requireRate(t, "40", goal["pct_funded"])
	require.Equal(t, false, goal["is_funded"])
}

func TestAGoalWithNoTargetHasNoFundedPercentageEither(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, map[string]any{"target_amount": "0.00"})
	require.Nil(t, goal["pct_funded"])
}

func TestTheDissolvedContainersLegReadsAsTheGoalBeingFunded(t *testing.T) {
	// What the Simplifi import leaves behind. Simplifi records a contribution
	// as a transfer into a GOAL account holding the money; the importer drops
	// that container and keeps the leg that left the real account, which is
	// negative and posts on the very account the goal reserves in.
	//
	// Reading that as a withdrawal is how a completed $15,000 goal renders as
	// -$15,000 saved with $30,000 still to go.
	l := planLedger(t)
	savings := savingsAccount(l)
	left := seedTxn(l, "set_aside", &store.Transaction{
		AccountID: uuid.MustParse(savings), Date: domain.NewDate(2026, time.August, 8),
		Amount:        domain.MustFromString("-15000.00"),
		StatementName: "Japan Trip Goal", Payee: "Japan Trip Goal",
	})

	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savings,
		"target_amount": "15000.00",
		"txn_ids":       []string{left.String()},
	})
	require.Equal(t, "15000.00", goal["saved_so_far"])
	require.Equal(t, "0.00", goal["left_to_save"])
	require.Equal(t, "0.00", goal["withdrawn"])
	require.Equal(t, true, goal["is_complete"])
}

func TestAGoalThatHasNotBeenRaidedHasSpentNothing(t *testing.T) {
	l := planLedger(t)
	require.Equal(t, "0.00", emergencyFund(l, nil)["withdrawn"])
}

// --- What the card names ------------------------------------------------------

func TestAGoalNamesTheAccountItReservesInRatherThanOnlyItsID(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, nil)
	require.Equal(t, "Rainy Day Savings", goal["account_name"])
	require.NotEqual(t, l.str("checking"), goal["account_id"],
		"the goal reserves where the money went, not where it came from")
}

func TestAGoalKeepsTheGlyphTheCardLeadsWith(t *testing.T) {
	// Two screens read it. A goal with no column for one would render the
	// same fallback everywhere, and the picker would save nothing.
	l := planLedger(t)
	goal := emergencyFund(l, map[string]any{"emoji": "🏝"})
	require.Equal(t, "🏝", goal["emoji"])
	require.Equal(t, "🏝", goalList(l)[0]["emoji"])

	cleared := l.alex.raw(http.MethodPatch, "/goals/"+goal["id"].(string), `{"emoji": null}`).
		requireStatus(http.StatusOK).json()
	require.Nil(t, cleared["emoji"])
}

func TestAGoalWithoutAGlyphSaysSoRatherThanSendingAnEmptyString(t *testing.T) {
	l := planLedger(t)
	require.Nil(t, emergencyFund(l, nil)["emoji"])
}

func TestTheFundingRowsSayWhatEachAccountPutIn(t *testing.T) {
	// The join answers which accounts may fund the goal; the card also has to
	// print what each one holds, and a list of ids leaves it fetching a row per
	// account to render a name.
	l := planLedger(t)
	goal := emergencyFund(l, map[string]any{
		"funding_account_ids": []string{l.str("checking"), l.str("card")},
	})

	funding := goalFunding(t, goal)
	require.Len(t, funding, 2)
	require.Equal(t, l.str("checking"), funding[0]["account_id"])
	require.Equal(t, "Everyday Checking", funding[0]["account_name"])
	require.Equal(t, "40.00", funding[0]["saved"])
	// Named but not yet drawn on: zero rather than absent, because the user
	// named it and a card that dropped the row would look like it forgot.
	require.Equal(t, l.str("card"), funding[1]["account_id"])
	require.Equal(t, "Rewards Card", funding[1]["account_name"])
	require.Equal(t, "0.00", funding[1]["saved"])
}

func TestTheFundingRowsAddUpToWhatTheGoalHasSaved(t *testing.T) {
	// The contribution came from checking, which the join no longer names. It
	// is still where the money came from, and parts that summed to less than
	// the total above them would be two savings figures on one card.
	l := planLedger(t)
	goal := emergencyFund(l, map[string]any{"funding_account_ids": []string{l.str("card")}})

	total := domain.Zero
	for _, row := range goalFunding(t, goal) {
		total = total.Add(domain.MustFromString(row["saved"].(string)))
	}
	require.Equal(t, goal["saved_so_far"], total.String())
	require.Equal(t, []any{l.str("card")}, goal["funding_account_ids"])
}

func TestACompletionDateComesFromTheImportAndIsNotInventedHere(t *testing.T) {
	// Simplifi records the day a goal was met and the importer brings it
	// across. Nothing in this application stamps one, so a goal completed here
	// reads as complete with no date rather than with the date of whichever
	// contribution happened to cross the line.
	l := planLedger(t)
	goal := emergencyFund(l, map[string]any{"target_amount": "30.00"})
	require.Equal(t, true, goal["is_complete"])
	require.Nil(t, goal["completed_on"])

	_, err := l.env.DB.Pool().Exec(l.t.Context(),
		`UPDATE goals SET completed_on = '2026-08-08' WHERE id = $1`,
		uuid.MustParse(goal["id"].(string)))
	require.NoError(t, err)

	require.Equal(t, "2026-08-08", goalList(l)[0]["completed_on"])
}

// --- One pot of money, shown twice -------------------------------------------

func TestAGoalsSavedSoFarReducesItsAccountsAvailableBalance(t *testing.T) {
	// calculations.md §6: the goal reserves money inside the account it names,
	// so the household's spendable total falls by what was set aside. Two
	// figures that disagree here mean the user is told they can spend savings
	// they have already committed.
	l := planLedger(t)
	goal := emergencyFund(l, nil)

	accounts := l.alex.get("/accounts").requireStatus(http.StatusOK).list()

	// The reserve lands on the account the goal names.
	savings := findByID(t, accounts, goal["account_id"].(string))
	require.Equal(t, "40.00", savings["goal_balance"])

	// And not on the account the money came from, which already fell by the
	// same 40.00 when the transfer left it. Reserving there too would subtract
	// one contribution from the household's spendable total twice.
	checking := findByID(t, accounts, l.str("checking"))
	balances := checking["balances"].(map[string]any)
	// 500.00 opening, less 100 + 50 + 25 + 200 + 40 of movement.
	require.Equal(t, "85.00", balances["balance"])
	require.Equal(t, "0.00", checking["goal_balance"])
	require.Equal(t, "85.00", balances["available_balance"])
}

func TestTheReserveLandsOnlyOnTheAccountTheGoalNames(t *testing.T) {
	// Reserving in every funding account would subtract the same savings from
	// each of them, and the spendable total would drop by a multiple.
	l := planLedger(t)
	emergencyFund(l, map[string]any{
		"funding_account_ids": []string{l.str("checking"), l.str("card")},
	})

	accounts := l.alex.get("/accounts").requireStatus(http.StatusOK).list()
	require.Equal(t, "0.00", findByID(t, accounts, l.str("card"))["goal_balance"])
}

// --- The Goals bucket --------------------------------------------------------

func TestAContributionTakenFromThePlanReachesTheGoalsBucket(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, nil)
	require.Equal(t, true, goal["is_taken_from_plan"])

	month := planFor(l, augustMonth)
	require.Equal(t, "-40.00", effective(t, month, "goals"))
	require.Equal(t, []any{l.str("contribution")},
		planBucket(t, month, "goals")["contributing_txn_ids"])
	// It is a goal contribution and nothing else: the two seeded August rows
	// are all Other Spend has.
	require.Equal(t, "-75.00", effective(t, month, "other_spend"))
	require.Equal(t, "-115.00", month["left_this_month"])
}

func TestAGoalFundedFromMoneyThePlanAlreadyCountedDoesNotCountTwice(t *testing.T) {
	// is_taken_from_plan off means the contribution still leaves Other Spend —
	// it simply does not reappear in the Goals bucket, or the user pays for it
	// twice.
	l := planLedger(t)
	emergencyFund(l, map[string]any{"is_taken_from_plan": false})

	month := planFor(l, augustMonth)
	require.Equal(t, "0.00", effective(t, month, "goals"))
	require.Empty(t, planBucket(t, month, "goals")["contributing_txn_ids"])
	require.Equal(t, "-75.00", effective(t, month, "other_spend"))
	require.Equal(t, "-75.00", month["left_this_month"])
}

func TestTheGoalsBucketFollowsTheFlagWhenItIsTurnedOff(t *testing.T) {
	// The flag lives on the goal, not the transaction: it is a statement about
	// how the goal is funded, and changing it has to move the bucket.
	l := planLedger(t)
	goal := emergencyFund(l, nil)
	require.Equal(t, "-40.00", effective(t, planFor(l, augustMonth), "goals"))

	l.alex.patch("/goals/"+goal["id"].(string),
		map[string]any{"is_taken_from_plan": false}).requireStatus(http.StatusOK)
	require.Equal(t, "0.00", effective(t, planFor(l, augustMonth), "goals"))
}

func TestAPerMonthExclusionKeepsAContributionOutOfTheGoalsBucket(t *testing.T) {
	l := planLedger(t)
	emergencyFund(l, nil)

	month := l.alex.post("/spending-plan/"+augustMonth+"/buckets/goals/exclusions",
		map[string]any{"entry_id": l.str("contribution")}).requireStatus(http.StatusOK).json()
	require.Equal(t, "0.00", effective(t, month, "goals"))

	// And the transaction is untouched, as every per-month exclusion is.
	txn := l.alex.get("/transactions/" + l.str("contribution")).
		requireStatus(http.StatusOK).json()
	require.Equal(t, false, txn["excluded_from_spending_plan"])
}

// --- Editing -----------------------------------------------------------------

func TestAGoalRoundTripsItsFundingAccountsWithoutDuplicates(t *testing.T) {
	// The create flow's *Add Account* row repeats easily.
	l := planLedger(t)
	goal := emergencyFund(l, map[string]any{
		"funding_account_ids": []string{l.str("checking"), l.str("checking"), l.str("card")},
	})
	require.ElementsMatch(t, []any{l.str("checking"), l.str("card")},
		goal["funding_account_ids"])

	updated := l.alex.patch("/goals/"+goal["id"].(string),
		map[string]any{"funding_account_ids": []string{l.str("card")}}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, []any{l.str("card")}, updated["funding_account_ids"])
}

func TestAGoalsNamedAccountCannotBeCleared(t *testing.T) {
	// A goal with none has nothing to inflate: its savings would stop reducing
	// any account's available balance while still reading as saved.
	l := planLedger(t)
	goal := emergencyFund(l, nil)
	response := l.alex.raw(http.MethodPatch, "/goals/"+goal["id"].(string), `{"account_id": null}`)
	response.requireStatus(http.StatusConflict)
	require.Contains(t, response.Body.String(), "account_id")
}

func TestDeletingAGoalLeavesItsContributionsInTheLedger(t *testing.T) {
	// Soft delete: a closed spending-plan month names the contributions, and a
	// hard delete would take that evidence with it.
	l := planLedger(t)
	goal := emergencyFund(l, nil)

	l.alex.del("/goals/" + goal["id"].(string)).requireStatus(http.StatusNoContent)
	require.Empty(t, goalList(l))
	l.alex.patch("/goals/"+goal["id"].(string),
		map[string]any{"name": "Back"}).requireStatus(http.StatusNotFound)

	l.alex.get("/transactions/" + l.str("contribution")).requireStatus(http.StatusOK)
}

func TestAGoalNeedsAName(t *testing.T) {
	l := planLedger(t)
	response := l.alex.post("/goals", map[string]any{
		"name": "  ", "account_id": l.str("checking"), "target_amount": "10.00",
	})
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "name")
}

// --- Money on the wire -------------------------------------------------------

func TestMoneyCrossesTheGoalsWireAsAString(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, nil)
	for _, field := range []string{"target_amount", "saved_so_far", "withdrawn",
		"left_to_save", "contributed_this_month", "monthly_needed"} {
		require.IsType(t, "", goal[field], field)
	}
	require.IsType(t, "", goalFunding(t, goal)[0]["saved"])
}

func TestAJsonNumberInAGoalsMoneyFieldIsRefused(t *testing.T) {
	l := planLedger(t)
	response := l.alex.raw(http.MethodPost, "/goals", fmt.Sprintf(
		`{"name": "Emergency Fund", "account_id": %q, "target_amount": 1000}`, l.str("checking")))
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "string")
}

// --- Tenancy -----------------------------------------------------------------

func seedStrangerGoal(l *ledger) uuid.UUID {
	l.t.Helper()
	id := uuid.New()
	_, err := l.env.DB.Pool().Exec(l.t.Context(), `
		INSERT INTO goals (id, space_id, account_id, name, target_amount)
		VALUES ($1, $2, $3, 'Theirs', 100)`,
		id, l.id("other_space"), l.id("stranger_account"))
	require.NoError(l.t, err)
	return id
}

func TestAGoalFromAnotherSpaceIsInvisible(t *testing.T) {
	l := planLedger(t)
	strangerGoal := seedStrangerGoal(l)

	require.Empty(t, goalList(l))
	l.alex.patch("/goals/"+strangerGoal.String(),
		map[string]any{"name": "Mine now"}).requireStatus(http.StatusNotFound)
	l.alex.del("/goals/" + strangerGoal.String()).requireStatus(http.StatusNotFound)
}

func TestAGoalCannotBorrowAnotherSpacesRows(t *testing.T) {
	// Refused rather than quietly dropped: a caller told the contribution
	// landed would see the goal read short with nothing to explain why.
	l := planLedger(t)
	l.alex.post("/goals", map[string]any{
		"name": "Theirs", "account_id": l.str("stranger_account"), "target_amount": "100.00",
	}).requireStatus(http.StatusConflict)

	l.alex.post("/goals", map[string]any{
		"name": "Theirs", "account_id": l.str("checking"), "target_amount": "100.00",
		"funding_account_ids": []string{l.str("stranger_account")},
	}).requireStatus(http.StatusConflict)

	l.alex.post("/goals", map[string]any{
		"name": "Theirs", "account_id": l.str("checking"), "target_amount": "100.00",
		"txn_ids": []string{l.str("stranger_txn")},
	}).requireStatus(http.StatusConflict)
}

func TestAViewerReadsTheGoalsButCannotChangeThem(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, nil)
	vera := l.as("vera")

	vera.get("/goals").requireStatus(http.StatusOK)
	vera.post("/goals", map[string]any{
		"name": "Theirs", "account_id": l.str("checking"), "target_amount": "100.00",
	}).requireStatus(http.StatusForbidden)
	vera.patch("/goals/"+goal["id"].(string),
		map[string]any{"name": "Nope"}).requireStatus(http.StatusForbidden)
	vera.del("/goals/" + goal["id"].(string)).requireStatus(http.StatusForbidden)
}

func TestARowIsLinkedToAGoalFromTheCardAndTakenBackOut(t *testing.T) {
	// The card's own way in: "Contribute" and "Withdraw" name one row each.
	// Neither call below names a direction, so both fall back to the sign —
	// money leaving checking is a contribution, money arriving is a withdrawal
	// — and the card lists both with their names, newest first.
	l := planLedger(t)
	contribution := seedContribution(l, "contribution", domain.NewDate(2026, time.August, 8), "-40.00")
	withdrawal := seedContribution(l, "withdrawal", domain.NewDate(2026, time.August, 15), "15.00")
	goal := newGoal(l, map[string]any{
		"name": "Vacation", "account_id": savingsAccount(l), "target_amount": "1000.00",
	})
	id := goal["id"].(string)
	require.Equal(t, "0.00", goal["saved_so_far"])
	require.Empty(t, goal["contributions"])

	linked := l.alex.post("/goals/"+id+"/transactions", map[string]any{
		"transaction_id": contribution.String(),
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "40.00", linked["saved_so_far"])
	rows := linked["contributions"].([]any)
	require.Len(t, rows, 1)
	first := rows[0].(map[string]any)
	require.Equal(t, contribution.String(), first["transaction_id"])
	require.Equal(t, "Savings", first["payee"])
	require.Equal(t, l.str("checking"), first["account_id"])
	require.Equal(t, "-40.00", first["amount"], "the ledger's sign")
	require.Equal(t, "40.00", first["saved"], "the card's sign")

	// Naming the same row again changes nothing.
	again := l.alex.post("/goals/"+id+"/transactions", map[string]any{
		"transaction_id": contribution.String(),
	}).requireStatus(http.StatusOK).json()
	require.Len(t, again["contributions"], 1)

	raided := l.alex.post("/goals/"+id+"/transactions", map[string]any{
		"transaction_id": withdrawal.String(),
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "25.00", raided["saved_so_far"])
	require.Equal(t, "15.00", raided["withdrawn"])
	rows = raided["contributions"].([]any)
	require.Len(t, rows, 2)
	require.Equal(t, withdrawal.String(), rows[0].(map[string]any)["transaction_id"], "newest first")
	require.Equal(t, "-15.00", rows[0].(map[string]any)["saved"])

	// A row counts toward one goal. A second goal asking for it is told which.
	other := newGoal(l, map[string]any{
		"name": "Car", "account_id": savingsAccount(l), "target_amount": "500.00",
	})
	refused := l.alex.post("/goals/"+other["id"].(string)+"/transactions", map[string]any{
		"transaction_id": contribution.String(),
	}).requireStatus(http.StatusConflict).json()
	require.Contains(t, refused["detail"], "Vacation")

	// A row nobody in the space owns, and a viewer, are both refused.
	l.alex.post("/goals/"+id+"/transactions", map[string]any{
		"transaction_id": uuid.New().String(),
	}).requireStatus(http.StatusConflict)
	l.as("vera").post("/goals/"+id+"/transactions", map[string]any{
		"transaction_id": contribution.String(),
	}).requireStatus(http.StatusForbidden)

	// Taking the withdrawal back out restores the saved figure; the row
	// itself is untouched.
	released := l.alex.del("/goals/" + id + "/transactions/" + withdrawal.String()).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "40.00", released["saved_so_far"])
	require.Len(t, released["contributions"], 1)
	l.alex.del("/goals/" + id + "/transactions/" + withdrawal.String()).requireStatus(http.StatusNotFound)
	l.alex.get("/transactions/" + withdrawal.String()).requireStatus(http.StatusOK)
}

// The defect the recorded direction exists for.
//
// Both rows leave the account that funds the goal and both are negative. One is
// the transfer that put the money into savings; the other paid for the thing
// the goal was saving for. Nothing in the ledger separates them, so reading by
// sign alone would count the second as a contribution and leave the goal
// reading fully funded after the household had spent a third of it — and the
// card's Withdraw picker, which keeps one sign per move, would never offer the
// row at all.
func TestANegativeRowCountsAsAWithdrawalWhenTheLinkSaysSo(t *testing.T) {
	l := planLedger(t)
	intoSavings := seedContribution(l, "contribution", domain.NewDate(2026, time.August, 8), "-15000.00")
	spent := seedContribution(l, "spent", domain.NewDate(2026, time.August, 20), "-5000.00")
	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	id := goal["id"].(string)

	l.alex.post("/goals/"+id+"/transactions", map[string]any{
		"transaction_id": intoSavings.String(), "direction": "contribution",
	}).requireStatus(http.StatusOK)
	raided := l.alex.post("/goals/"+id+"/transactions", map[string]any{
		"transaction_id": spent.String(), "direction": "withdrawal",
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, "10000.00", raided["saved_so_far"])
	require.Equal(t, "5000.00", raided["withdrawn"])
	require.Equal(t, []any{spent.String()}, raided["withdrawal_txn_ids"])

	rows := raided["contributions"].([]any)
	require.Len(t, rows, 2)
	newest := rows[0].(map[string]any)
	require.Equal(t, spent.String(), newest["transaction_id"], "newest first")
	require.Equal(t, "-5000.00", newest["amount"], "the ledger's sign")
	require.Equal(t, "-5000.00", newest["saved"], "and the card's, which agrees only here")
	require.Equal(t, "withdrawal", newest["kind"])
}

// Pressing the other button re-files the row. Refusing it would leave a person
// who picked the wrong direction with nothing to do but unlink and start again.
func TestLinkingACountedRowAgainChangesWhichWayItCounts(t *testing.T) {
	l := planLedger(t)
	spent := seedContribution(l, "spent", domain.NewDate(2026, time.August, 20), "-5000.00")
	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	id := goal["id"].(string)

	filed := l.alex.post("/goals/"+id+"/transactions", map[string]any{
		"transaction_id": spent.String(), "direction": "contribution",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "5000.00", filed["saved_so_far"])

	refiled := l.alex.post("/goals/"+id+"/transactions", map[string]any{
		"transaction_id": spent.String(), "direction": "withdrawal",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "-5000.00", refiled["saved_so_far"])
	require.Len(t, refiled["contributions"], 1, "re-filed, not counted twice")

	// And taking it back out forgets the direction with the row.
	released := l.alex.del("/goals/" + id + "/transactions/" + spent.String()).
		requireStatus(http.StatusOK).json()
	require.Empty(t, released["withdrawal_txn_ids"])
}

// Where the goal's money went, from an account the goal never reserved in.
//
// The reserve is drawn down once, by the withdrawal. The card charges that
// followed it report where the money went and must not draw it down again — a
// household that saved 15,000, moved it to checking and booked a trip
// would otherwise read as 3,500 overdrawn on a goal it had funded exactly.
func TestSpendingIsAssociatedFromAnyAccountWithoutMovingTheReserve(t *testing.T) {
	l := planLedger(t)
	card := newAccount(l, "Sample Rewards Card", string(domain.KindCreditCard), "credit_card", "0.00")
	intoSavings := seedContribution(l, "contribution", domain.NewDate(2026, time.August, 1), "-15000.00")
	back := seedContribution(l, "back", domain.NewDate(2026, time.August, 10), "15000.00")
	flights := seedCardCharge(l, card, "flights", domain.NewDate(2026, time.August, 12), "-4000.00", uuid.Nil)
	refund := seedCardCharge(l, card, "refund", domain.NewDate(2026, time.August, 20), "500.00", uuid.Nil)

	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	id := goal["id"].(string)
	link := func(txnID uuid.UUID, direction string) map[string]any {
		return l.alex.post("/goals/"+id+"/transactions", map[string]any{
			"transaction_id": txnID.String(), "direction": direction,
		}).requireStatus(http.StatusOK).json()
	}
	link(intoSavings, "contribution")
	link(back, "withdrawal")
	link(flights, "spending")
	final := link(refund, "spending")

	require.Equal(t, "0.00", final["saved_so_far"], "saved, then taken back out")
	require.Equal(t, "15000.00", final["withdrawn"])
	require.Equal(t, "3500.00", final["spent_on_goal"], "net of the refund")
	require.Equal(t, []any{flights.String(), refund.String()}, final["spending_txn_ids"])
	require.Len(t, final["contributions"], 4)

	// The card funded nothing, so it is not one of the goal's funding accounts.
	for _, row := range goalFunding(t, final) {
		require.NotEqual(t, card, row["account_id"])
	}

	for _, row := range final["contributions"].([]any) {
		one := row.(map[string]any)
		if one["transaction_id"] != flights.String() {
			continue
		}
		require.Equal(t, "spending", one["kind"])
		require.Equal(t, "0.00", one["saved"], "it reports; it does not move the reserve")
		require.Equal(t, "4000.00", one["spent"])
	}
}

// Re-filing works across all three kinds, and unlinking forgets the kind.
func TestSpendingCanBeRefiledAsAWithdrawalAndBack(t *testing.T) {
	l := planLedger(t)
	card := newAccount(l, "Sample Rewards Card", string(domain.KindCreditCard), "credit_card", "0.00")
	charge := seedCardCharge(l, card, "flights", domain.NewDate(2026, time.August, 12), "-4000.00", uuid.Nil)
	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	id := goal["id"].(string)
	link := func(direction string) map[string]any {
		return l.alex.post("/goals/"+id+"/transactions", map[string]any{
			"transaction_id": charge.String(), "direction": direction,
		}).requireStatus(http.StatusOK).json()
	}

	spending := link("spending")
	require.Equal(t, "0.00", spending["saved_so_far"])
	require.Equal(t, "4000.00", spending["spent_on_goal"])

	// The same row as a withdrawal: it leaves the spending list rather than
	// appearing in both, or the goal would report the money twice.
	withdrawn := link("withdrawal")
	require.Equal(t, "-4000.00", withdrawn["saved_so_far"])
	require.Equal(t, "0.00", withdrawn["spent_on_goal"])
	require.Empty(t, withdrawn["spending_txn_ids"])
	require.Len(t, withdrawn["contributions"], 1)

	released := l.alex.del("/goals/" + id + "/transactions/" + charge.String()).
		requireStatus(http.StatusOK).json()
	require.Empty(t, released["withdrawal_txn_ids"])
	require.Empty(t, released["spending_txn_ids"])
}

func TestADirectionThatIsNeitherWayIsRefused(t *testing.T) {
	l := planLedger(t)
	spent := seedContribution(l, "spent", domain.NewDate(2026, time.August, 20), "-5000.00")
	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	l.alex.post("/goals/"+goal["id"].(string)+"/transactions", map[string]any{
		"transaction_id": spent.String(), "direction": "sideways",
	}).requireStatus(http.StatusUnprocessableEntity)
}

// A create that says nothing about direction is classified by sign, which is
// what the Simplifi import's own numbers were built on.
func TestACreateThatNamesNoDirectionIsClassifiedByItsSigns(t *testing.T) {
	l := planLedger(t)
	seedContribution(l, "contribution", domain.NewDate(2026, time.August, 8), "-40.00")
	withdrawal := seedContribution(l, "withdrawal", domain.NewDate(2026, time.August, 15), "15.00")
	goal := newGoal(l, map[string]any{
		"name": "Emergency Fund", "account_id": savingsAccount(l), "target_amount": "1000.00",
		"txn_ids": []string{l.str("contribution"), l.str("withdrawal")},
	})

	require.Equal(t, "25.00", goal["saved_so_far"])
	require.Equal(t, []any{withdrawal.String()}, goal["withdrawal_txn_ids"])
}

// And a create that does name one is taken at its word, however the rows read.
func TestACreateThatNamesItsWithdrawalsIsTakenAtItsWord(t *testing.T) {
	l := planLedger(t)
	seedContribution(l, "contribution", domain.NewDate(2026, time.August, 8), "-15000.00")
	spent := seedContribution(l, "spent", domain.NewDate(2026, time.August, 20), "-5000.00")
	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
		"txn_ids":            []string{l.str("contribution"), l.str("spent")},
		"withdrawal_txn_ids": []string{spent.String()},
	})

	require.Equal(t, "10000.00", goal["saved_so_far"])
	require.Equal(t, "5000.00", goal["withdrawn"])
}

// --- Filing a whole selection, and reading it back by category ---------------

// A trip is thirty charges, and nobody is opening thirty rows.
func TestASelectionIsFiledUnderOneGoalInOneCall(t *testing.T) {
	l := planLedger(t)
	card := newAccount(l, "Sample Rewards Card", string(domain.KindCreditCard), "credit_card", "0.00")
	flights := seedCardCharge(l, card, "flights", domain.NewDate(2026, time.August, 12), "-4000.00", uuid.Nil)
	hotel := seedCardCharge(l, card, "hotel", domain.NewDate(2026, time.August, 13), "-2000.00", uuid.Nil)
	meals := seedCardCharge(l, card, "meals", domain.NewDate(2026, time.August, 14), "-500.00", uuid.Nil)

	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	id := goal["id"].(string)

	out := l.alex.post("/goals/"+id+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{flights.String(), hotel.String(), meals.String()},
		"direction":       "spending",
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, float64(3), out["linked"])
	require.Empty(t, out["skipped"])

	final := out["goal"].(map[string]any)
	require.Equal(t, "6500.00", final["spent_on_goal"])
	require.Equal(t, "0.00", final["saved_so_far"], "spending reports; it does not move the reserve")
	require.Len(t, final["spending_txn_ids"], 3)
}

// One row belonging elsewhere must not refuse the other two. A user who
// selected a range has no way to find the offender by bisecting it.
func TestABulkLinkReportsTheRowsItCouldNotTakeAndFilesTheRest(t *testing.T) {
	l := planLedger(t)
	card := newAccount(l, "Sample Rewards Card", string(domain.KindCreditCard), "credit_card", "0.00")
	flights := seedCardCharge(l, card, "flights", domain.NewDate(2026, time.August, 12), "-4000.00", uuid.Nil)
	hotel := seedCardCharge(l, card, "hotel", domain.NewDate(2026, time.August, 13), "-2000.00", uuid.Nil)
	elsewhere := seedCardCharge(l, card, "roof", domain.NewDate(2026, time.August, 14), "-900.00", uuid.Nil)

	roof := newGoal(l, map[string]any{
		"name": "New roof", "account_id": savingsAccount(l), "target_amount": "9000.00",
	})
	l.alex.post("/goals/"+roof["id"].(string)+"/transactions", map[string]any{
		"transaction_id": elsewhere.String(), "direction": "spending",
	}).requireStatus(http.StatusOK)

	trip := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	out := l.alex.post("/goals/"+trip["id"].(string)+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{flights.String(), elsewhere.String(), hotel.String()},
		"direction":       "spending",
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, float64(2), out["linked"])
	skipped := out["skipped"].([]any)
	require.Len(t, skipped, 1)
	require.Equal(t, elsewhere.String(), skipped[0].(map[string]any)["transaction_id"])
	require.Contains(t, skipped[0].(map[string]any)["reason"], "New roof")
	require.Equal(t, "6000.00", out["goal"].(map[string]any)["spent_on_goal"])
}

// The direction is required in bulk. The sign fallback the single link keeps
// for a caller that omits it would be thirty guesses rather than one answer.
func TestABulkLinkRefusesToGuessTheDirection(t *testing.T) {
	l := planLedger(t)
	card := newAccount(l, "Sample Rewards Card", string(domain.KindCreditCard), "credit_card", "0.00")
	flights := seedCardCharge(l, card, "flights", domain.NewDate(2026, time.August, 12), "-4000.00", uuid.Nil)
	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})

	l.alex.post("/goals/"+goal["id"].(string)+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{flights.String()},
	}).requireStatus(http.StatusUnprocessableEntity)
}

// The breakdown is why a goal charge keeps its ordinary category instead of
// being filed under the goal: the household still wants to know what the trip
// was made of.
func TestAGoalReportsWhatItsMoneyWentOnByCategory(t *testing.T) {
	l := planLedger(t)
	card := newAccount(l, "Sample Rewards Card", string(domain.KindCreditCard), "credit_card", "0.00")
	flights := seedCardCharge(l, card, "flights",
		domain.NewDate(2026, time.August, 12), "-4000.00", l.id("groceries"))
	meals := seedCardCharge(l, card, "meals",
		domain.NewDate(2026, time.August, 14), "-500.00", l.id("food"))
	uncategorized := seedCardCharge(l, card, "misc", domain.NewDate(2026, time.August, 15), "-100.00", uuid.Nil)

	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	out := l.alex.post("/goals/"+goal["id"].(string)+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{flights.String(), meals.String(), uncategorized.String()},
		"direction":       "spending",
	}).requireStatus(http.StatusOK).json()

	lines := out["goal"].(map[string]any)["spending_by_category"].([]any)
	require.Len(t, lines, 3)

	first := lines[0].(map[string]any)
	require.Equal(t, l.id("groceries").String(), first["category_id"])
	require.Equal(t, "Groceries", first["category_name"])
	require.Equal(t, "4000.00", first["spent"])
	require.Equal(t, float64(1), first["transaction_count"])

	last := lines[2].(map[string]any)
	require.Nil(t, last["category_id"])
	require.Equal(t, "Uncategorized", last["category_name"], "a gap to fill, not spending that did not happen")
	require.Equal(t, "100.00", last["spent"])
}

// Contributions and withdrawals move the reserve; only spending says where the
// money went, so only spending appears in the breakdown.
func TestTheBreakdownIgnoresTheReserveMoving(t *testing.T) {
	l := planLedger(t)
	intoSavings := seedContribution(l, "contribution", domain.NewDate(2026, time.August, 1), "-15000.00")
	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	out := l.alex.post("/goals/"+goal["id"].(string)+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{intoSavings.String()}, "direction": "contribution",
	}).requireStatus(http.StatusOK).json()

	final := out["goal"].(map[string]any)
	require.Equal(t, "15000.00", final["saved_so_far"])
	require.Empty(t, final["spending_by_category"])
}

// A goal counts money that has moved.
//
// Reading only half of that rule would let a projected occurrence be filed
// under a goal and then added to saved_so_far — reporting a household as
// having put away what it has only planned to. Both the link and the sum ask
// store.CountsTowardBalance in full, the same rule the transfer pairer, the
// balance sites and the Amazon matcher already ask.

func TestAForecastCannotBeFiledUnderAGoal(t *testing.T) {
	l := planLedger(t)
	forecast := seedTxn(l, "forecast", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.October, 1),
		Amount: domain.MustFromString("-40.00"), StatementName: "TRANSFER TO SAVINGS",
		Payee: "Savings", EstimateStatus: store.ProjectedEstimate,
		Source: domain.SourceSimplifiImport,
	})
	goal := newGoal(l, map[string]any{
		"name": "Vacation", "account_id": savingsAccount(l), "target_amount": "1000.00",
	})
	id := goal["id"].(string)

	refused := l.alex.post("/goals/"+id+"/transactions", map[string]any{
		"transaction_id": forecast.String(),
	}).requireStatus(http.StatusConflict)
	require.Contains(t, refused.Body.String(), "forecast")

	// And the bulk route is the same rule, not a second way in.
	l.alex.post("/goals/"+id+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{forecast.String()},
		"direction":       "contribution",
	}).requireStatus(http.StatusConflict)

	// Nothing was filed either way: the goal is still empty.
	listed := l.alex.get("/goals").requireStatus(http.StatusOK).list()
	require.Len(t, listed, 1)
	require.Equal(t, "0.00", listed[0]["saved_so_far"])
	require.Empty(t, listed[0]["contributions"])
}

func TestARealContributionIsStillAccepted(t *testing.T) {
	// So the refusal above is about the forecast and not about the fixture.
	l := planLedger(t)
	real := seedContribution(l, "real", domain.NewDate(2026, time.August, 8), "-40.00")
	goal := newGoal(l, map[string]any{
		"name": "Vacation", "account_id": savingsAccount(l), "target_amount": "1000.00",
	})
	linked := l.alex.post("/goals/"+goal["id"].(string)+"/transactions", map[string]any{
		"transaction_id": real.String(),
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "40.00", linked["saved_so_far"])
}
