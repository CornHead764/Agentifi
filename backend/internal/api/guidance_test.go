package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// fuelStopNote is a guidance note about fuel stops, in the shape the builder
// sends: the bank's wording, contains, case as the user typed it.
func fuelStopNote(overrides map[string]any) map[string]any {
	body := map[string]any{
		"name": "Fuel Stop",
		"instruction": "One charge on the day under $20 is convenience-store food — file it " +
			"as Fast Food. One over $20 is a tank of fuel — Gas & Fuel. Two on the same day: " +
			"the larger is fuel and the smaller is food.",
		"conditions": []map[string]any{{
			"field": "statement_name", "operator": "contains", "value_texts": []string{"fuel stop"},
		}},
	}
	for key, value := range overrides {
		body[key] = value
	}
	return body
}

func TestAGuidanceNoteKeepsItsConditionsAndSaysWhatTheyMean(t *testing.T) {
	l := buildLedger(t)
	note := l.alex.post("/guidance", fuelStopNote(nil)).requireStatus(http.StatusCreated).json()

	require.Equal(t, "Fuel Stop", note["name"])
	require.Equal(t, true, note["is_active"])
	require.Equal(t, true, note["owns_filter"])
	require.Equal(t, `the statement name contains "fuel stop"`, note["applies_to"])
	require.NotNil(t, note["filter_id"])

	listed := l.alex.get("/guidance").requireStatus(http.StatusOK).list()
	require.Len(t, listed, 1)
	require.Equal(t, note["id"], listed[0]["id"])
}

func TestANoteWithNoConditionsIsRefusedAsARuleIs(t *testing.T) {
	// The same door a rule is refused at, for the same reason: the evaluator
	// reads an empty clause list as matching nothing, so the note would be
	// stored, listed, and never said.
	l := buildLedger(t)
	l.alex.post("/guidance", map[string]any{
		"name": "House style", "instruction": "Prefer the narrowest category that fits.",
	}).requireStatus(http.StatusUnprocessableEntity)

	l.alex.post("/guidance", fuelStopNote(map[string]any{"conditions": []map[string]any{}})).
		requireStatus(http.StatusUnprocessableEntity)

	// And the facet a rule cannot read is refused here for the same reason.
	l.alex.post("/guidance", fuelStopNote(map[string]any{
		"conditions": []map[string]any{
			{"field": "is_bill_or_subscription", "operator": "is_true", "state": true},
		},
	})).requireStatus(http.StatusUnprocessableEntity)
}

func TestANoteWithNothingToSayIsRefused(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/guidance", fuelStopNote(map[string]any{"instruction": "   "})).
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.post("/guidance", fuelStopNote(map[string]any{"name": " "})).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestANoteIsSwitchedOffWithoutBeingRetyped(t *testing.T) {
	l := buildLedger(t)
	id := l.alex.post("/guidance", fuelStopNote(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	off := l.alex.patch("/guidance/"+id, map[string]any{"is_active": false}).requireStatus(http.StatusOK).json()
	require.Equal(t, false, off["is_active"])
	require.Equal(t, "Fuel Stop", off["name"], "the words survive the switch")

	on := l.alex.patch("/guidance/"+id, map[string]any{"is_active": true}).requireStatus(http.StatusOK).json()
	require.Equal(t, true, on["is_active"])
}

func TestEditingANotesConditionsRewritesTheFilterItOwns(t *testing.T) {
	l := buildLedger(t)
	created := l.alex.post("/guidance", fuelStopNote(nil)).requireStatus(http.StatusCreated).json()
	id := created["id"].(string)

	updated := l.alex.patch("/guidance/"+id, map[string]any{
		"conditions": []map[string]any{{
			"field": "payee", "operator": "contains", "value_texts": []string{"Fuel"},
		}},
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, `the payee contains "Fuel"`, updated["applies_to"])
	require.Equal(t, created["filter_id"], updated["filter_id"], "the same filter, rewritten")

	// Emptying the conditions is refused rather than quietly widening or
	// silencing the note, and so is clearing the filter it points at.
	l.alex.patch("/guidance/"+id, map[string]any{"conditions": []map[string]any{}}).
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.patch("/guidance/"+id, map[string]any{"filter_id": nil}).
		requireStatus(http.StatusConflict)
}

func TestANotePointingAtASharedFilterIsNotEditedFromHere(t *testing.T) {
	// Rewriting a saved filter's items from this screen would rewrite whatever
	// watchlist or envelope reads the same row, with nothing here to say so.
	l := buildLedger(t)
	id := l.alex.post("/guidance", map[string]any{
		"name": "Groceries", "instruction": "Split these by receipt when one is attached.",
		"filter_id": l.str("filter"),
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	note := l.alex.get("/guidance/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, false, note["owns_filter"])

	l.alex.patch("/guidance/"+id, map[string]any{
		"conditions": []map[string]any{{
			"field": "payee", "operator": "contains", "value_texts": []string{"Fuel"},
		}},
	}).requireStatus(http.StatusConflict)
}

func TestANoteFromAnotherSpaceIsNotReachable(t *testing.T) {
	l := buildLedger(t)
	id := l.alex.post("/guidance", fuelStopNote(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	l.alex.post("/guidance", map[string]any{
		"name": "Someone else's filter", "instruction": "Not this space's to read.",
		"filter_id": l.str("stranger_filter"),
	}).requireStatus(http.StatusConflict)

	// A viewer reads the notes and writes none.
	l.as("vera").get("/guidance").requireStatus(http.StatusOK)
	l.as("vera").post("/guidance", fuelStopNote(nil)).requireStatus(http.StatusForbidden)
	l.as("vera").del("/guidance/" + id).requireStatus(http.StatusForbidden)
}

func TestNotesAreReorderedAsAWholeSet(t *testing.T) {
	// Two notes about one payee are read as one paragraph, so which sentence
	// comes first is the household's to decide — and a half-applied reorder is
	// an order nobody chose.
	l := buildLedger(t)
	first := l.alex.post("/guidance", fuelStopNote(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	second := l.alex.post("/guidance", fuelStopNote(map[string]any{"name": "Fuel Stop, fuel"})).
		requireStatus(http.StatusCreated).json()["id"].(string)

	l.alex.post("/guidance/reorder", map[string]any{"guidance_ids": []string{second}}).
		requireStatus(http.StatusUnprocessableEntity)

	reordered := l.alex.post("/guidance/reorder",
		map[string]any{"guidance_ids": []string{second, first}}).
		requireStatus(http.StatusOK).list()
	require.Equal(t, second, reordered[0]["id"])
	require.Equal(t, first, reordered[1]["id"])
}

func TestADeletedNoteIsGone(t *testing.T) {
	l := buildLedger(t)
	id := l.alex.post("/guidance", fuelStopNote(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.del("/guidance/" + id).requireStatus(http.StatusNoContent)
	l.alex.get("/guidance/" + id).requireStatus(http.StatusNotFound)
	require.Empty(t, l.alex.get("/guidance").requireStatus(http.StatusOK).list())
}

// --- What a run is shown ------------------------------------------------------

// guidedCheckBody is the category check with the guidance section asked for.
func guidedCheckBody(overrides map[string]any) map[string]any {
	body := suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
		"context": map[string]any{
			"transaction": true, "similar_transactions": 5, "categories": true, "guidance": true,
		},
	})
	for key, value := range overrides {
		body[key] = value
	}
	return body
}

func TestANoteAboutThePayeeIsPutInFrontOfTheModel(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Left as is."))
	allowWrites(l, model)
	l.alex.post("/guidance", fuelStopNote(nil)).requireStatus(http.StatusCreated)
	l.alex.post("/guidance", fuelStopNote(map[string]any{
		"name": "Costco", "instruction": "Warehouse runs are Groceries unless the receipt says otherwise.",
		"conditions": []map[string]any{{
			"field": "statement_name", "operator": "contains", "value_texts": []string{"costco"},
		}},
	})).requireStatus(http.StatusCreated)

	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-12", "amount": "-12.00",
		"payee": "Fuel Stop", "statement_name": "FUEL STOP #0631 SPRINGFIELD",
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	id := l.alex.post("/assistant-automations", guidedCheckBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	preview := l.alex.post("/assistant-automations/"+id+"/preview",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()

	opening := preview["opening"].(string)
	require.Contains(t, opening, "### The household's guidance")
	require.Contains(t, opening, "**Fuel Stop** — applies to "+`the statement name contains "fuel stop"`)
	require.Contains(t, opening, "the larger is fuel and the smaller is food")
	require.NotContains(t, opening, "Warehouse runs are Groceries",
		"a note about another merchant is not this row's business")
}

func TestAnAutomationThatWasNotAskedForGuidanceIsNotShownIt(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/guidance", fuelStopNote(nil)).requireStatus(http.StatusCreated)
	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-12", "amount": "-12.00",
		"payee": "Fuel Stop", "statement_name": "FUEL STOP #0631 SPRINGFIELD",
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	preview := l.alex.post("/assistant-automations/"+id+"/preview",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.NotContains(t, preview["opening"], "The household's guidance")
}

func TestANoteThatIsSwitchedOffReachesNothing(t *testing.T) {
	l := buildLedger(t)
	id := l.alex.post("/guidance", fuelStopNote(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.patch("/guidance/"+id, map[string]any{"is_active": false}).requireStatus(http.StatusOK)

	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-12", "amount": "-12.00",
		"payee": "Fuel Stop", "statement_name": "FUEL STOP #0631 SPRINGFIELD",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	automation := l.alex.post("/assistant-automations", guidedCheckBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	preview := l.alex.post("/assistant-automations/"+automation+"/preview",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.NotContains(t, preview["opening"], "The household's guidance")
}

func TestAGuidedRowIsNotSettledByTheHistoryAlone(t *testing.T) {
	// A payee the household has filed the same way eight times is exactly the
	// payee somebody writes a note about — and a confident vote is what stops
	// the model being asked at all, so the note would never be read on the
	// rows it was written for.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Under $20, so Fast Food by the household's note."))
	allowWrites(l, model)
	seedHistory(l, "Fuel Stop", "FUEL STOP #0631 SPRINGFIELD", l.str("groceries"), 8)
	l.alex.post("/guidance", fuelStopNote(nil)).requireStatus(http.StatusCreated)

	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-12", "amount": "-12.00",
		"payee": "Fuel Stop", "statement_name": "FUEL STOP #0631 SPRINGFIELD",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	id := l.alex.post("/assistant-automations", guidedCheckBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "model", run["decided_by"], "a guided row goes to the model")
	require.Len(t, model.seen, 1)
	opening := model.seen[0]["messages"].([]any)[1].(map[string]any)["content"].(string)
	require.Contains(t, opening, "### The household's guidance")
	require.Contains(t, opening, "### Agentifi's assessment", "the vote is still shown")
}

func TestAnUnguidedRowStillSkipsTheModel(t *testing.T) {
	// The other half: notes exist, none is about this row, and the shortcut
	// that keeps a local model out of eight-of-eight rows is intact.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("should not be asked"))
	allowWrites(l, model)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 8)
	l.alex.post("/guidance", fuelStopNote(nil)).requireStatus(http.StatusCreated)

	id := l.alex.post("/assistant-automations", guidedCheckBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "agentifi", run["decided_by"])
	require.Empty(t, model.seen)
}

func TestARunAboutNoOneRowIsShownEveryNoteWithItsScope(t *testing.T) {
	// The daily sweep decides many rows, so selecting against a subject it
	// does not have would show it nothing. Every note, each carrying the rows
	// it applies to, so the model can tell which is which.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Nothing to do."))
	allowWrites(l, model)
	l.alex.post("/guidance", fuelStopNote(nil)).requireStatus(http.StatusCreated)

	id := l.alex.post("/assistant-automations", map[string]any{
		"name": "Daily sweep", "trigger": "daily",
		"trigger_config": map[string]any{"at": "06:30"},
		"prompt":         "Categorize what the rules missed.",
		"context":        map[string]any{"categories": true, "guidance": true},
		"mode":           domain.AutomationModePropose,
		"tools":          []string{"search_transactions", "list_categories", "update_transaction"},
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	preview := l.alex.post("/assistant-automations/"+id+"/preview", map[string]any{}).
		requireStatus(http.StatusOK).json()
	opening := preview["opening"].(string)
	require.Contains(t, opening, "### The household's guidance")
	require.Contains(t, opening, `applies to the statement name contains "fuel stop"`)
	require.Contains(t, opening, `follow a note only on a row that matches what`)
}

func TestASameDaySiblingIsInFrontOfTheModelToo(t *testing.T) {
	// The Fuel Stop note asks about two charges on one day, so the run has to
	// be able to see the other one. The similar-rows section reads a window
	// that runs to today rather than stopping at the row's own date, so the
	// sibling is there — with its amount, which is what the note sorts on.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("The larger is fuel."))
	allowWrites(l, model)
	l.alex.post("/guidance", fuelStopNote(nil)).requireStatus(http.StatusCreated)

	fuel := map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-12", "amount": "-48.00",
		"payee": "Fuel Stop", "statement_name": "FUEL STOP #0631 SPRINGFIELD",
	}
	food := map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-12", "amount": "-8.50",
		"payee": "Fuel Stop", "statement_name": "FUEL STOP #0631 SPRINGFIELD",
	}
	l.alex.post("/transactions", fuel).requireStatus(http.StatusCreated)
	smaller := l.alex.post("/transactions", food).requireStatus(http.StatusCreated).
		json()["id"].(string)

	id := l.alex.post("/assistant-automations", guidedCheckBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	preview := l.alex.post("/assistant-automations/"+id+"/preview",
		map[string]any{"transaction_id": smaller}).requireStatus(http.StatusOK).json()

	opening := preview["opening"].(string)
	require.Contains(t, opening, "-48.00", "the day's other charge, so the note can compare them")
	require.Contains(t, opening, "-8.50")
	require.Contains(t, opening, "2026-09-12")
}
