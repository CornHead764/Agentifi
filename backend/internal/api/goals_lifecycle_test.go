package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A goal's life on the wire: saved toward, funded, drawn on and spent,
// closed. The figures are invented: a 1,000.00 target saved in full, 600.00
// taken out to checking and 450.00 of it spent on a card.

func linkGoalRow(l *ledger, goalID string, txnID uuid.UUID, direction string) map[string]any {
	l.t.Helper()
	return l.alex.post("/goals/"+goalID+"/transactions", map[string]any{
		"transaction_id": txnID.String(), "direction": direction,
	}).requireStatus(http.StatusOK).json()
}

func fundedTrip(l *ledger) map[string]any {
	l.t.Helper()
	seedContribution(l, "saved", domain.NewDate(2026, time.June, 1), "-1000.00")
	return newGoal(l, map[string]any{
		"name": "Lake Trip", "account_id": savingsAccount(l), "target_amount": "1000.00",
		"target_on": "2026-12-31", "txn_ids": []string{l.str("saved")},
	})
}

func TestAGoalSavedInFullIsFundedAndAsksForNothingMore(t *testing.T) {
	l := planLedger(t)
	goal := fundedTrip(l)

	require.Equal(t, "funded", goal["stage"])
	require.Nil(t, goal["monthly_needed"])
	require.Equal(t, "0.00", goal["unassigned_withdrawn"])
	require.Nil(t, goal["closed_on"])
}

func TestAGoalShortOfItsTargetIsSaving(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, nil)

	require.Equal(t, "saving", goal["stage"])
	require.Equal(t, "192.00", goal["monthly_needed"])
}

func TestMoneyTakenOutAndNotYetLinkedToSpendingIsReported(t *testing.T) {
	l := planLedger(t)
	card := newAccount(l, "Trip Card", string(domain.KindCreditCard), "credit_card", "0.00")
	goal := fundedTrip(l)
	id := goal["id"].(string)
	out := seedContribution(l, "out", domain.NewDate(2026, time.August, 1), "600.00")
	charge := seedCardCharge(l, card, "lodge", domain.NewDate(2026, time.August, 3), "-450.00", uuid.Nil)

	linkGoalRow(l, id, out, "withdrawal")
	final := linkGoalRow(l, id, charge, "spending")

	require.Equal(t, "spending", final["stage"])
	require.Equal(t, "400.00", final["saved_so_far"])
	require.Equal(t, "600.00", final["withdrawn"])
	require.Equal(t, "450.00", final["spent_on_goal"])
	require.Equal(t, "150.00", final["unassigned_withdrawn"])
	// Funded once, so nothing more is asked although 400.00 < 1,000.00.
	require.Nil(t, final["monthly_needed"])
	require.Equal(t, true, final["is_funded"])
}

func TestClosingAGoalReleasesItsReserveAndReopeningTakesItBack(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, nil)
	id := goal["id"].(string)
	savings := goal["account_id"].(string)
	reserved := func() any {
		accounts := l.alex.get("/accounts").requireStatus(http.StatusOK).list()
		return findByID(t, accounts, savings)["goal_balance"]
	}
	require.Equal(t, "40.00", reserved())

	closed := l.alex.post("/goals/"+id+"/close", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, "closed", closed["stage"])
	require.Equal(t, "2026-08-20", closed["closed_on"])
	// Its history stays readable.
	require.Equal(t, "40.00", closed["saved_so_far"])
	require.Len(t, closed["contributions"], 1)
	require.Equal(t, "0.00", reserved())

	// Closed in August: the month's Goals bucket holds nothing of it, and the
	// row stays the goal's rather than turning into Other Spend.
	month := planFor(l, augustMonth)
	require.Equal(t, "0.00", effective(t, month, "goals"))
	require.Equal(t, "-75.00", effective(t, month, "other_spend"))

	again := l.alex.post("/goals/"+id+"/close", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-08-20", again["closed_on"])

	open := l.alex.post("/goals/"+id+"/reopen", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, "saving", open["stage"])
	require.Nil(t, open["closed_on"])
	require.Equal(t, "40.00", reserved())
	require.Equal(t, "-40.00", effective(t, planFor(l, augustMonth), "goals"))
}

func TestAViewerCannotCloseAGoal(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, nil)

	l.as("vera").post("/goals/"+goal["id"].(string)+"/close", nil).requireStatus(http.StatusForbidden)
}

func suggestionIDs(t *testing.T, body map[string]any) []string {
	t.Helper()
	rows, ok := body["rows"].([]any)
	require.True(t, ok, "no rows in %v", body)
	out := []string{}
	for _, raw := range rows {
		out = append(out, raw.(map[string]any)["transaction_id"].(string))
	}
	return out
}

func TestFindRowsSuggestsEachKindAroundTheGoalsOwnRows(t *testing.T) {
	l := planLedger(t)
	card := newAccount(l, "Trip Card", string(domain.KindCreditCard), "credit_card", "0.00")
	savings := savingsAccount(l)
	onSavings := func(key string, on domain.Date, amount string) uuid.UUID {
		return seedTxn(l, key, &store.Transaction{
			AccountID: uuid.MustParse(savings), Date: on,
			Amount: domain.MustFromString(amount), StatementName: "SAVINGS " + key, Payee: key,
		})
	}
	first := onSavings("first", domain.NewDate(2026, time.May, 2), "500.00")
	second := onSavings("second", domain.NewDate(2026, time.June, 2), "500.00")
	out := onSavings("out", domain.NewDate(2026, time.August, 1), "-600.00")
	near := seedCardCharge(l, card, "near", domain.NewDate(2026, time.August, 4), "-120.00", uuid.Nil)
	before := seedCardCharge(l, card, "before", domain.NewDate(2026, time.April, 1), "-80.00", uuid.Nil)

	goal := newGoal(l, map[string]any{
		"name": "Lake Trip", "account_id": savings, "target_amount": "1000.00",
		"txn_ids": []string{first.String()}, "withdrawal_txn_ids": []string{},
	})
	id := goal["id"].(string)
	suggest := func(kind string) map[string]any {
		return l.alex.get("/goals/" + id + "/suggestions?kind=" + kind).requireStatus(http.StatusOK).json()
	}

	contributions := suggestionIDs(t, suggest("contribution"))
	require.Contains(t, contributions, second.String())
	require.NotContains(t, contributions, first.String(), "already counted")
	require.NotContains(t, contributions, out.String(), "money leaving is not a contribution")

	require.Equal(t, []string{out.String()}, suggestionIDs(t, suggest("withdrawal")))

	linkGoalRow(l, id, out, "withdrawal")
	spending := suggest("spending")
	ids := suggestionIDs(t, spending)
	require.Equal(t, near.String(), ids[0], "three days from the withdrawal ranks first")
	require.NotContains(t, ids, before.String(), "before the window")
	require.NotContains(t, ids, out.String())
	require.Equal(t, "2026-07-18", spending["from"])
	top := spending["rows"].([]any)[0].(map[string]any)
	require.Equal(t, "Trip Card", top["account_name"])
	require.Equal(t, float64(3), top["days_from_withdrawal"])
}

func TestFindRowsRefusesAKindThatIsNeitherWay(t *testing.T) {
	l := planLedger(t)
	goal := emergencyFund(l, nil)

	l.alex.get("/goals/" + goal["id"].(string) + "/suggestions?kind=sideways").
		requireStatus(http.StatusUnprocessableEntity)
}

func TestNeitherAFundedGoalDrawnOnNorAClosedOneIsNudged(t *testing.T) {
	l := planLedger(t)
	trip := fundedTrip(l)
	out := seedContribution(l, "out", domain.NewDate(2026, time.August, 1), "600.00")
	linkGoalRow(l, trip["id"].(string), out, "withdrawal")
	fund := emergencyFund(l, nil)
	l.alex.post("/goals/"+fund["id"].(string)+"/close", nil).requireStatus(http.StatusOK)

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "goal_contribution"))
}
