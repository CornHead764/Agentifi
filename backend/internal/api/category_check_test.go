package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Where a category check got to, and what it concluded when it concluded
// nothing.
//
// The register has one word — "Uncategorized" — for two rows that need
// different things from a person: one nobody has looked at, and one the
// assistant looked at and could not place. Re-running the check over the
// second kind is work the household does because the screen cannot tell
// them apart.

func TestARunThatFilesNothingLeavesTheRowUndetermined(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Nothing about this row says what it was for."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])

	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.Nil(t, row["category_id"], "still uncategorized, which is half of the state")
	require.NotNil(t, row["category_checked_at"], "and checked, which is the other half")
	require.Equal(t, "Nothing about this row says what it was for.", row["category_check_note"],
		"the model's own closing line is the reason a person reads")
	require.Equal(t, false, row["checking_category"], "the run is over")
}

func TestAModelThatCouldNotGuessLeavesTheRowUndetermined(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Could not guess: a bare reference number."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)

	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.NotNil(t, row["category_checked_at"])
	require.Equal(t, "Could not guess: a bare reference number.", row["category_check_note"])
}

func TestASkipThatIsAnAnswerLeavesNoUndeterminedMark(t *testing.T) {
	// A card's autopay is one leg of a pair, which the pairing files; a payee
	// whose history is transfer legs is a transfer, proposed under Transfer.
	// Each is the check's answer, not a shrug, so neither row reads "could not
	// place it".
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("should not be asked"))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	leg := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("transfer_out")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSkipped, leg["status"], leg["error"])
	require.Contains(t, leg["output"], "leg of a paired transfer")

	unpaired := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-01", "amount": "-200.00",
		"payee": "Transfer to Rewards Card", "statement_name": "ONLINE TRANSFER TO CARD",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	verdict := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": unpaired}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, verdict["status"], verdict["error"])
	require.Contains(t, verdict["output"], "transfer legs")
	require.Equal(t, 1.0, verdict["confidence"], "the one past row for this payee is a transfer leg")
	require.Empty(t, model.seen)

	for _, txn := range []string{l.str("transfer_out"), unpaired} {
		row := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
		require.Nil(t, row["category_checked_at"], txn)
		require.Equal(t, "", row["category_check_note"], txn)
	}
	suggestion := l.alex.get("/transactions/" + unpaired).requireStatus(http.StatusOK).
		json()["suggestion"].(map[string]any)
	require.Equal(t, categoryWithMarker(l, domain.KnownCategoryTransfer), suggestion["category_id"],
		"money leaving checking for a transfer is a Transfer, not a card payment")
}

func TestAnUnmatchedCardPaymentIsProposedAsOne(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("should not be asked"))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	payment := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("card"), "date": "2026-09-03", "amount": "150.00",
		"payee": "Mobile Pymt", "statement_name": "MOBILE PYMT RECEIVED",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": payment}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, domain.CardPaymentConfidence, run["confidence"])
	require.Empty(t, model.seen)

	suggestion := l.alex.get("/transactions/" + payment).requireStatus(http.StatusOK).
		json()["suggestion"].(map[string]any)
	require.Equal(t, categoryWithMarker(l, domain.KnownCategoryCreditCardPayment),
		suggestion["category_id"])
}

func categoryWithMarker(l *ledger, marker string) string {
	for _, category := range l.alex.get("/categories").requireStatus(http.StatusOK).list() {
		if category["known_category_id"] == marker {
			return category["id"].(string)
		}
	}
	l.t.Fatalf("no category carries %s", marker)
	return ""
}

func TestAHistoryThatConfirmsTheCategoryClearsAStaleMark(t *testing.T) {
	l := buildLedger(t)
	quiet := newFakeModel(t, answerReply("No idea."))
	allowWrites(l, quiet)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)
	require.NotNil(t, l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()["category_checked_at"])

	l.alex.patch("/transactions/"+l.str("august_corner"),
		map[string]any{"category_id": l.str("groceries")}).requireStatus(http.StatusOK)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 8)
	again := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSkipped, again["status"], again["error"])
	require.Contains(t, again["output"], "history agrees")

	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.Nil(t, row["category_checked_at"])
	require.Equal(t, "", row["category_check_note"])
	require.Equal(t, again["id"], row["category_check_run_id"], "the run that settled it")
}

func TestARunThatProposesACategoryLeavesNoUndeterminedMark(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","category_id":"`+l.str("groceries")+`","summary":"Corner Store is groceries"}`),
		answerReply("Proposed Groceries for Corner Store."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)

	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.NotNil(t, row["suggestion"], "something is waiting on this row")
	require.Nil(t, row["category_checked_at"],
		`"could not place it" beside a suggestion would be the cell contradicting itself`)
}

func TestASecondRunClearsTheUndeterminedMarkItSet(t *testing.T) {
	l := buildLedger(t)
	quiet := newFakeModel(t, answerReply("No idea."))
	allowWrites(l, quiet)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)
	require.NotNil(t, l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()["category_checked_at"])

	// The household writes a guidance note, adds an order, renames the payee —
	// whatever it was, the next run places the row, and the mark has to go.
	decided := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","category_id":"`+l.str("groceries")+`","summary":"Groceries after all"}`),
		answerReply("Proposed Groceries."))
	allowWrites(l, decided)
	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)

	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.Nil(t, row["category_checked_at"])
	require.Equal(t, "", row["category_check_note"])
}

func TestTheUndeterminedRowsAreAFilterAndNotASideChannel(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Could not place it."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)

	created := l.alex.post("/filters", map[string]any{
		"name":  "Nothing could place these",
		"scope": "saved_view",
		"items": []map[string]any{
			{"field": "is_category_undetermined", "operator": "is_true", "state": true},
		},
	}).requireStatus(http.StatusCreated).json()

	page := registerPage(l, "filter_id="+created["id"].(string)+"&limit=500")
	require.Equal(t, map[string]bool{l.str("august_corner"): true}, idsIn(page),
		"the checked row, and not every uncategorized one")
}

func TestAWaitingSuggestionIsAFilterTheListAndItsSummaryAgreeOn(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","category_id":"`+l.str("groceries")+`","summary":"Corner Store is groceries"}`),
		answerReply("Proposed Groceries for Corner Store."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)
	l.alex.patch("/transactions/"+l.str("august_corner"),
		map[string]any{"is_reviewed": false}).requireStatus(http.StatusOK)

	unreviewed := func(suggested bool) string {
		return l.alex.post("/filters", map[string]any{
			"name":  "Unreviewed",
			"scope": "saved_view",
			"items": []map[string]any{
				{"field": "is_reviewed", "operator": "is_true", "state": false, "position": 0},
				{"field": "has_category_suggestion", "operator": "is_true", "state": suggested, "position": 1},
			},
		}).requireStatus(http.StatusCreated).json()["id"].(string)
	}
	with, without := unreviewed(true), unreviewed(false)

	page := registerPage(l, august+"&filter_id="+with+"&limit=500")
	require.Equal(t, map[string]bool{l.str("august_corner"): true}, idsIn(page),
		"the one row something is waiting on")
	require.Equal(t, "-25.00", page["total"])

	rest := idsIn(registerPage(l, august+"&filter_id="+without+"&limit=500"))
	require.NotContains(t, rest, l.str("august_corner"))
	require.Contains(t, rest, l.str("august_groceries"), "unreviewed, and nothing proposed")

	aggregate := aggregateOf(l, august+"&filter_id="+with+"&direction=spending")
	require.EqualValues(t, 1, aggregate["count"], "the chart counts the rows the list shows")
	require.Equal(t, "-25.00", aggregate["total"])
}

func TestARuleCannotMatchOnAWaitingSuggestion(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/rules", map[string]any{
		"name": "Anything proposed",
		"conditions": []map[string]any{
			{"field": "has_category_suggestion", "operator": "is_true", "state": true},
		},
		"actions": map[string]any{"set_is_reviewed": true},
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestCategoryChecksReportProgressForTheRowsTheyWereGiven(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Could not place it."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)

	progress := l.alex.get("/transactions/category-checks?id=" + l.str("august_corner") +
		"&id=" + l.str("august_groceries")).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(2), progress["total"])
	require.Equal(t, float64(2), progress["done"], "neither row has a run outstanding")

	rows := map[string]map[string]any{}
	for _, item := range progress["rows"].([]any) {
		one := item.(map[string]any)
		rows[one["transaction_id"].(string)] = one
	}
	require.Len(t, rows, 2)
	require.Equal(t, false, rows[l.str("august_corner")]["checking"])
	require.Equal(t, "Could not place it.", rows[l.str("august_corner")]["category_check_note"])
	require.Nil(t, rows[l.str("august_groceries")]["category_checked_at"],
		"a row nobody checked carries no verdict")
}

func TestCategoryChecksCountARowThatIsNoLongerThere(t *testing.T) {
	// A progress meter that never reaches its total is worse than one that
	// rounds: the row is gone and no result is coming for it.
	l := buildLedger(t)
	missing := "00000000-0000-4000-8000-0000000000ff"
	progress := l.alex.get("/transactions/category-checks?id=" + l.str("august_corner") +
		"&id=" + missing).requireStatus(http.StatusOK).json()

	require.Equal(t, float64(2), progress["total"])
	require.Equal(t, float64(2), progress["done"])
	require.Len(t, progress["rows"].([]any), 1)
}

func TestCategoryChecksRefuseAnEmptyRequest(t *testing.T) {
	l := buildLedger(t)
	l.alex.get("/transactions/category-checks").requireStatus(http.StatusBadRequest)
}

// The run that decided a row's category is stamped onto the row, so a person
// can open that run's detail from the cell and read why. Every genuine
// outcome records it: the row it gave up on, and the row it placed.

func TestAnUndeterminedRowRemembersTheRunThatGaveUp(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Nothing about this row says what it was for."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()

	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.Nil(t, row["category_id"], "still undetermined, and now inspectable")
	require.Equal(t, run["id"], row["category_check_run_id"],
		"the row points at the run that could not place it")

	// The category-checks endpoint carries it too, so a freshly-checked row in
	// a batch gets the affordance without a page refetch.
	progress := l.alex.get("/transactions/category-checks?id=" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	first := progress["rows"].([]any)[0].(map[string]any)
	require.Equal(t, run["id"], first["category_check_run_id"])
}

func TestAnAnswerThatNamesTheCategoryInProseBecomesACard(t *testing.T) {
	// The model states its answer and never calls the tool. The answer is the
	// proposal: Agentifi files it through the same tool, at the stated
	// confidence, and the row has a card, not "could not place it".
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Category: Groceries (id "+l.str("groceries")+
		")\nCorner Store sells food, and row "+l.str("august_corner")+" is a small purchase."+
		"\nConfidence: 0.92"))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, float64(1), run["actions"])
	require.Contains(t, run["output"], "Proposed: Groceries")
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "pending", action["status"])
	require.Equal(t, l.str("groceries"), action["body"].(map[string]any)["category_id"])
	require.Contains(t, action["summary"], "(confidence 0.92)")

	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.NotNil(t, row["suggestion"])
	require.Nil(t, row["category_checked_at"], "not undetermined")
	require.Nil(t, row["category_id"], "a proposal, not a change")
}

func TestAProseAnswerFilesNothingWithoutACategoryOfThisSpace(t *testing.T) {
	// An id that is not one of the space's categories, or an answer that says
	// it could not guess, is no proposal: the row stays undetermined.
	for name, answer := range map[string]func(l *ledger) string{
		"a stranger's id": func(*ledger) string {
			return "Category: Water (id 0b3c8a52-7d4e-4f61-9a2b-5c6d7e8f9a01)\nConfidence: 0.9"
		},
		"the row's own id": func(l *ledger) string {
			return "Row " + l.str("august_corner") + " is groceries.\nConfidence: 0.9"
		},
		"could not guess": func(l *ledger) string {
			return "Could not guess: Groceries (id " + l.str("groceries") + ") or Food.\nConfidence: 0.3"
		},
		"no confidence": func(l *ledger) string {
			return "Groceries (id " + l.str("groceries") + ")."
		},
	} {
		t.Run(name, func(t *testing.T) {
			l := buildLedger(t)
			model := newFakeModel(t, answerReply(answer(l)))
			allowWrites(l, model)
			id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
				requireStatus(http.StatusCreated).json()["id"].(string)

			run := l.alex.post("/assistant-automations/"+id+"/run",
				map[string]any{"transaction_id": l.str("august_corner")}).
				requireStatus(http.StatusOK).json()
			require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
			require.Equal(t, float64(0), run["actions"])
			row := l.alex.get("/transactions/" + l.str("august_corner")).
				requireStatus(http.StatusOK).json()
			require.NotNil(t, row["category_checked_at"])
		})
	}
}

func TestAPlacedRowRemembersTheRunThatPlacedIt(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","category_id":"`+l.str("groceries")+`","summary":"Corner Store is groceries"}`),
		answerReply("Proposed Groceries for Corner Store."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()

	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.NotNil(t, row["suggestion"], "a proposal is waiting, so this is a decision")
	require.Nil(t, row["category_checked_at"], "not undetermined")
	require.Equal(t, run["id"], row["category_check_run_id"],
		"and still inspectable: the run that proposed it")
}
