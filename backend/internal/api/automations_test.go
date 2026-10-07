package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Automations, end to end against the stand-in model.
//
// What matters is the chain a person cannot watch: a row arrives, the right
// automation fires for it and no other, the model is handed the row and its
// history, its proposal lands as a card the space can decide, and the whole
// exchange — prompt, context, tool calls, report — is readable afterwards.

func TestTheHistorySearchLooksForTheSubjectsOwnSpelling(t *testing.T) {
	require.Equal(t, "LUMIÈRE", spelledAs("CAFÉ LUMIÈRE 0417 ", "lumiere"))
	require.Equal(t, "Lumiere", spelledAs("CAFE Lumiere", "lumiere"))
}

func suggestCategoriesBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"name":    "Suggest categories",
		"trigger": domain.AutomationTriggerTransaction,
		"trigger_config": map[string]any{
			"skip_transfers": true,
		},
		"prompt":  "Decide whether the category is right and propose a fix if not.",
		"context": map[string]any{"transaction": true, "similar_transactions": 5, "categories": true},
		"mode":    domain.AutomationModePropose,
		"tools":   []string{"list_categories", "update_transaction"},
	}
	for key, value := range overrides {
		body[key] = value
	}
	return body
}

func TestAnAutomationIsRefusedWhenItDoesNotHoldTogether(t *testing.T) {
	// Refused on save, because a run has nobody to tell.
	l := buildLedger(t)
	for name, body := range map[string]map[string]any{
		"no name":        suggestCategoriesBody(map[string]any{"name": " "}),
		"no prompt":      suggestCategoriesBody(map[string]any{"prompt": ""}),
		"unknown tool":   suggestCategoriesBody(map[string]any{"tools": []string{"pay_bill"}}),
		"bad mode":       suggestCategoriesBody(map[string]any{"mode": "yolo"}),
		"bad trigger":    suggestCategoriesBody(map[string]any{"trigger": "whenever"}),
		"daily, no time": suggestCategoriesBody(map[string]any{"trigger": "daily", "trigger_config": map[string]any{}}),
		"observe with a change tool": suggestCategoriesBody(map[string]any{
			"mode": domain.AutomationModeObserve, "tools": []string{"update_transaction"},
		}),
		"too many rounds": suggestCategoriesBody(map[string]any{"max_tool_rounds": 99}),
	} {
		response := l.alex.post("/assistant-automations", body)
		require.Equal(t, http.StatusBadRequest, response.Code, name)
	}
	l.as("vera").post("/assistant-automations", suggestCategoriesBody(nil)).requireStatus(http.StatusForbidden)
}

func TestAnAutomationRoundTripsAndListsWithItsFigures(t *testing.T) {
	l := buildLedger(t)
	made := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()
	require.Equal(t, true, made["is_enabled"])
	require.Equal(t, float64(6), made["max_tool_rounds"], "the default number of rounds")

	id := made["id"].(string)
	patched := l.alex.patch("/assistant-automations/"+id, map[string]any{
		"is_enabled": false, "model": "other-model",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, false, patched["is_enabled"])
	require.Equal(t, "other-model", patched["model"])
	require.Equal(t, "Suggest categories", patched["name"], "a patch left the rest alone")

	rows := l.alex.get("/assistant-automations").requireStatus(http.StatusOK).list()
	require.Len(t, rows, 1)
	require.Equal(t, float64(0), rows[0]["runs"])
	require.Equal(t, float64(0), rows[0]["pending_actions"])

	// A viewer can read the list but not change it.
	l.as("vera").get("/assistant-automations").requireStatus(http.StatusOK)
	l.as("vera").patch("/assistant-automations/"+id, map[string]any{"name": "x"}).
		requireStatus(http.StatusForbidden)
	// Somebody else's space sees nothing.
	l.as("bob").get("/assistant-automations/" + id).requireStatus(http.StatusNotFound)

	l.alex.del("/assistant-automations/" + id).requireStatus(http.StatusNoContent)
	l.alex.get("/assistant-automations/" + id).requireStatus(http.StatusNotFound)
}

func TestTemplatesAreServedForTheEditor(t *testing.T) {
	l := buildLedger(t)
	rows := l.alex.get("/assistant-automations/templates").requireStatus(http.StatusOK).list()
	require.NotEmpty(t, rows)
	require.Equal(t, "check_category", rows[0]["key"])
}

func TestACopyOfATemplateRunsTheTemplateUntilTheHouseholdEditsIt(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Nothing to change."), answerReply("Nothing to change."))
	allowWrites(l, model)
	template, ok := domain.AutomationTemplateByKey(domain.AutomationTemplateCheckCategory)
	require.True(t, ok)
	space := store.SpaceIDOf(l.id("space"))

	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"template_key": template.Key, "prompt": template.Prompt,
		"description": template.Description, "tools": template.Tools,
		"confidence_threshold": 0,
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	automationID := uuid.MustParse(id)
	stored, err := l.env.DB.GetAutomation(t.Context(), space, automationID)
	require.NoError(t, err)
	require.True(t, stored.PromptFromTemplate)
	require.True(t, stored.DescriptionFromTemplate)

	// The row's own copy is older wording; the run is sent the template's.
	_, err = l.env.DB.Pool().Exec(t.Context(),
		`UPDATE assistant_automations SET prompt = 'An older wording of the check.',
		        description = 'An older description.' WHERE id = $1`, automationID)
	require.NoError(t, err)
	read := l.alex.get("/assistant-automations/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, template.Prompt, read["prompt"])
	require.Equal(t, template.Description, read["description"])
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Contains(t, run["prompt"], "Make the best guess the evidence allows")
	require.NotContains(t, run["prompt"], "An older wording")

	// Saving the editor untouched, rewrapped, keeps it on the template.
	l.alex.patch("/assistant-automations/"+id, map[string]any{
		"name": "Our category check", "prompt": strings.ReplaceAll(template.Prompt, "\n", " \n  "),
	}).requireStatus(http.StatusOK)
	stored, err = l.env.DB.GetAutomation(t.Context(), space, automationID)
	require.NoError(t, err)
	require.True(t, stored.PromptFromTemplate)
	require.Equal(t, template.Prompt, stored.Prompt)

	// The household's own words are kept, and run, from then on.
	ours := template.Prompt + "\nThe gym membership is Fitness."
	l.alex.patch("/assistant-automations/"+id, map[string]any{"prompt": ours}).
		requireStatus(http.StatusOK)
	stored, err = l.env.DB.GetAutomation(t.Context(), space, automationID)
	require.NoError(t, err)
	require.False(t, stored.PromptFromTemplate)
	require.True(t, stored.DescriptionFromTemplate, "the description was not edited")
	l.alex.patch("/assistant-automations/"+id, map[string]any{"is_enabled": true}).
		requireStatus(http.StatusOK)
	run = l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Contains(t, run["prompt"], "The gym membership is Fitness.")
}

func TestARunAboutATransactionHandsTheModelTheRowAndItsHistoryAndLeavesACard(t *testing.T) {
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
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "manual", run["fired_by"])
	require.Equal(t, "Proposed Groceries for Corner Store.", run["output"])
	require.Equal(t, float64(1), run["tool_calls"])
	require.Equal(t, float64(1), run["actions"])
	require.Contains(t, run["subject"], "Corner Store")

	// What the model was told: the system prompt names the mode and the
	// task, and the opening message carries the row with its id, the
	// category tree, and the payee's history.
	require.Contains(t, run["prompt"], "Decide whether the category is right")
	require.Contains(t, run["prompt"], "never say you have changed something")
	request := model.seen[0]
	first := request["messages"].([]any)
	system := first[0].(map[string]any)["content"].(string)
	require.Equal(t, run["prompt"], system)
	opening := first[1].(map[string]any)["content"].(string)
	require.Contains(t, opening, "### The transaction")
	require.Contains(t, opening, l.str("august_corner"))
	require.Contains(t, opening, `"statement_name": "CORNER STORE"`)
	require.Contains(t, opening, "### Categories")
	require.Contains(t, opening, l.str("groceries"))
	require.Contains(t, opening, "### Similar past transactions")
	// Only the tools the automation chose were offered.
	offered := request["tools"].([]any)
	require.Len(t, offered, 2)

	// The thread is on the run, and the card is pending in it.
	conversation := run["conversation"].(map[string]any)
	actions := conversation["actions"].([]any)
	require.Len(t, actions, 1)
	action := actions[0].(map[string]any)
	require.Equal(t, "pending", action["status"])
	require.Equal(t, "PATCH", action["method"])
	require.Equal(t, "/transactions/"+l.str("august_corner"), action["path"])

	// The review queue lists it with the run it came from.
	pending := l.alex.get("/assistant-automations/pending").requireStatus(http.StatusOK).list()
	require.Len(t, pending, 1)
	require.Equal(t, action["id"], pending[0]["id"])
	require.Equal(t, run["id"], pending[0]["run_id"])
	require.Contains(t, pending[0]["subject"], "Corner Store")
	// The same card as the thread's, preview and all.
	require.NotNil(t, action["preview"])
	require.Equal(t, action["preview"], pending[0]["preview"])

	// A run's thread does not clutter the chat rail.
	for _, listed := range l.alex.get("/assistant/conversations").requireStatus(http.StatusOK).list() {
		require.NotEqual(t, conversation["id"], listed["id"], "the run's thread is in the chat rail")
	}

	// Applying from the queue recategorizes the row, and the figures follow.
	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK)
	row := l.alex.get("/transactions/" + l.str("august_corner")).requireStatus(http.StatusOK).json()
	require.Equal(t, l.str("groceries"), row["category_id"])
	listed := l.alex.get("/assistant-automations").requireStatus(http.StatusOK).list()
	require.Equal(t, float64(1), listed[0]["runs"])
	require.Equal(t, float64(0), listed[0]["pending_actions"])
	require.Equal(t, domain.AutomationRunSucceeded, listed[0]["last_status"])
}

func TestAnAutomationThatWouldChangeThingsFailsPlainlyWhileChangesAreOff(t *testing.T) {
	// The connection's switch outranks the automation's mode: "changes off"
	// has to mean nothing is written, whatever else is stored.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Nothing to do."))
	configure(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunFailed, run["status"])
	require.Contains(t, run["error"], "changes are switched off")
	require.Equal(t, "changes_off", run["error_code"],
		"the page cannot offer the switch without knowing this is what failed")
	require.Empty(t, model.seen, "the model was called for a run that could not act")

	// The same run with the assistant switched off names that instead: the
	// page offers the setup, not the changes switch.
	l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model", "is_enabled": false,
	}).requireStatus(http.StatusOK)
	run = l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunFailed, run["status"])
	require.Equal(t, "assistant_unavailable", run["error_code"])
}

func TestAnObservingAutomationOnlyReports(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","category_id":"`+l.str("groceries")+`","summary":"x"}`),
		answerReply("Nothing unusual."))
	configure(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"mode": domain.AutomationModeObserve, "tools": []string{"search_transactions"},
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "Nothing unusual.", run["output"])
	// The model tried a change tool anyway; it was refused, not proposed.
	require.Equal(t, float64(0), run["actions"])
	tools := model.seen[0]["tools"].([]any)
	require.Len(t, tools, 1)
	require.Empty(t, l.alex.get("/assistant-automations/pending").requireStatus(http.StatusOK).list())
}

func TestArrivingRowsFireOnlyTheAutomationsWhoseConditionsTheyMeet(t *testing.T) {
	// The service half, driven directly: the sync and the importers call this
	// after the settle, and what it queues is what the worker runs.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("ok"))
	allowWrites(l, model)

	everything := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"name": "everything", "trigger_config": map[string]any{},
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	noTransfers := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"name": "no transfers", "trigger_config": map[string]any{"skip_transfers": true},
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	uncategorized := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"name": "gaps", "trigger_config": map[string]any{},
		"conditions": []map[string]any{
			{"field": "is_uncategorized", "operator": "is_true", "state": true},
		},
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	cardOnly := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"name": "card", "trigger_config": map[string]any{},
		"conditions": []map[string]any{
			{"field": "account", "operator": "in", "value_ids": []string{l.str("card")}},
		},
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"name": "off", "is_enabled": false,
	})).requireStatus(http.StatusCreated)
	l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"name": "manual", "trigger": domain.AutomationTriggerManual,
	})).requireStatus(http.StatusCreated)

	automations, err := NewAutomations(l.env)
	require.NoError(t, err)
	queued, err := automations.EnqueueForTransactions(t.Context(), store.SpaceIDOf(l.id("space")), []uuid.UUID{
		l.id("august_groceries"), // synced, categorized, checking
		l.id("transfer_out"),     // synced transfer leg, checking
		l.id("july"),             // a Simplifi import: never fires
	})
	require.NoError(t, err)

	count := func(automation string) int {
		return len(l.alex.get("/assistant-automations/" + automation + "/runs").
			requireStatus(http.StatusOK).list())
	}
	require.Equal(t, 2, count(everything), "both synced rows")
	require.Equal(t, 1, count(noTransfers), "the transfer leg was skipped")
	require.Equal(t, 0, count(uncategorized),
		"the grocery row has a category, and a paired transfer leg never needs one")
	require.Equal(t, 0, count(cardOnly), "neither row is on the card")
	require.Equal(t, 3, queued)

	// Firing the same rows again queues nothing: a re-sync costs nothing here.
	again, err := automations.EnqueueForTransactions(t.Context(), store.SpaceIDOf(l.id("space")),
		[]uuid.UUID{l.id("august_groceries"), l.id("transfer_out")})
	require.NoError(t, err)
	require.Equal(t, 0, again)

	all := l.alex.get("/assistant-automations/runs?status=queued").requireStatus(http.StatusOK).list()
	require.Len(t, all, 3)
	for _, run := range all {
		require.Equal(t, "transaction", run["fired_by"])
	}

	// The worker drains them. The one about the transfer leg is skipped
	// before any model is asked; the two about the grocery row reach the
	// stand-in model and succeed. Afterwards the queue is empty.
	automations.Drain(t.Context())
	require.Empty(t, l.alex.get("/assistant-automations/runs?status=queued").
		requireStatus(http.StatusOK).list())
	require.Len(t, l.alex.get("/assistant-automations/runs?status=succeeded").
		requireStatus(http.StatusOK).list(), 2)
	skipped := l.alex.get("/assistant-automations/runs?status=skipped").
		requireStatus(http.StatusOK).list()
	require.Len(t, skipped, 1)
	require.Contains(t, skipped[0]["output"], "leg of a paired transfer")
}

func TestATriggerFilterIsStoredAsTheOneFilterAndCanBeCleared(t *testing.T) {
	l := buildLedger(t)
	created := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"conditions": []map[string]any{
			{"field": "account", "operator": "in", "value_ids": []string{l.str("checking")}},
		},
	})).requireStatus(http.StatusCreated).json()
	id := created["id"].(string)
	filter := created["filter"].(map[string]any)
	require.Equal(t, created["filter_id"], filter["id"])
	require.Equal(t, AutomationFilterScope, filter["scope"])
	items := filter["items"].([]any)
	require.Len(t, items, 1)
	require.Equal(t, "account", items[0].(map[string]any)["field"])

	// Editing the conditions edits the same owned filter in place.
	edited := l.alex.patch("/assistant-automations/"+id, map[string]any{
		"conditions": []map[string]any{
			{"field": "is_pending", "operator": "is_true", "state": false},
		},
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, created["filter_id"], edited["filter_id"])
	require.Equal(t, "is_pending",
		edited["filter"].(map[string]any)["items"].([]any)[0].(map[string]any)["field"])

	// An edit that leaves the conditions out leaves them alone.
	renamed := l.alex.patch("/assistant-automations/"+id, map[string]any{"name": "Renamed"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, created["filter_id"], renamed["filter_id"])

	// An empty list is every row, not an empty filter that would match none.
	cleared := l.alex.patch("/assistant-automations/"+id, map[string]any{
		"conditions": []map[string]any{},
	}).requireStatus(http.StatusOK).json()
	require.Nil(t, cleared["filter_id"])
	require.Nil(t, cleared["filter"])
}

func TestATriggerFilterRefusesTheFacetARuleCannotRead(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"conditions": []map[string]any{
			{"field": "is_bill_or_subscription", "operator": "is_true", "state": true},
		},
	})).requireStatus(http.StatusUnprocessableEntity)
}

func TestATriggerWhoseFilterVanishedFiresOnNothing(t *testing.T) {
	// A scope must never widen on its own: an automation limited to the card
	// whose filter row is gone does not start firing on every account.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("ok"))
	allowWrites(l, model)
	created := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"name": "checking only", "trigger_config": map[string]any{},
		"conditions": []map[string]any{
			{"field": "account", "operator": "in", "value_ids": []string{l.str("checking")}},
		},
	})).requireStatus(http.StatusCreated).json()
	filterID := uuid.MustParse(created["filter_id"].(string))
	require.NoError(t, l.env.DB.DeleteFilter(t.Context(), store.SpaceIDOf(l.id("space")), filterID))

	automations, err := NewAutomations(l.env)
	require.NoError(t, err)
	_, err = automations.EnqueueForTransactions(t.Context(), store.SpaceIDOf(l.id("space")),
		[]uuid.UUID{l.id("august_groceries")})
	require.NoError(t, err)
	require.Empty(t, l.alex.get("/assistant-automations/"+created["id"].(string)+"/runs").
		requireStatus(http.StatusOK).list())
}

func TestAPreviewRendersThePromptForTheUnsavedEdits(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("ok"))
	configure(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	preview := l.alex.post("/assistant-automations/"+id+"/preview", map[string]any{
		"prompt": "Something I have not saved yet.", "mode": domain.AutomationModeObserve,
		"tools": []string{"list_categories"},
	}).requireStatus(http.StatusOK).json()
	require.Contains(t, preview["prompt"], "Something I have not saved yet.")
	require.Contains(t, preview["prompt"], "You can only read")
	require.Contains(t, preview["opening"], "### The transaction")
	require.NotNil(t, preview["transaction_id"], "the server chose a recent row to render against")
	require.Empty(t, model.seen, "a preview does not call the model")

	// Nothing was saved by previewing.
	saved := l.alex.get("/assistant-automations/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationModePropose, saved["mode"])
}

func TestTheAssistantCannotReachItsOwnAutomations(t *testing.T) {
	// A conversation that creates an automation that applies changes unattended
	// is the assistant reaching past what a person approved.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("change_endpoint", `{"method":"POST","path":"/assistant-automations",`+
			`"body":{"name":"x"},"summary":"Set up an automation"}`),
		answerReply("done"))
	id := allowWrites(l, model)
	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "automate it"}).requireStatus(http.StatusOK).json()
	require.Empty(t, body["conversation"].(map[string]any)["actions"])
	tool := body["conversation"].(map[string]any)["messages"].([]any)[1].(map[string]any)
	require.Contains(t, tool["content"], "not reachable")
}

func TestTheConnectionProbeNamesADroppedToolCall(t *testing.T) {
	// The server answers 200 with reasoning and no call — what a server with
	// no tool-call parser sends. The probe has to say so, and say that the
	// prompted style works, because that is the fix.
	l := buildLedger(t)
	dropped := `{"choices":[{"message":{"content":null,"reasoning":"We need to call ping."},` +
		`"finish_reason":"stop"}]}`
	prompted := answerReply(`{"tool_call":{"name":"ping","arguments":{}}}`)
	model := newFakeModel(t, dropped, prompted)
	configure(l, model)

	result := l.alex.post("/assistant/connection/test", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, true, result["reachable"])
	require.Equal(t, false, result["native_tool_calls"])
	require.Equal(t, true, result["prompted_tool_calls"])
	require.Equal(t, "prompted", result["recommended"])
	require.Contains(t, result["detail"], "not turning the model's tool calls")

	// And the style can be saved, after which a question goes through with
	// the catalogue in the prompt.
	status := l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model", "tool_call_style": "prompted",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "prompted", status["tool_call_style"])
	l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model", "tool_call_style": "sideways",
	}).requireStatus(http.StatusBadRequest)
}

func TestAPromptedConnectionAnswersAQuestionThroughTextToolCalls(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		answerReply("Let me look. {\"tool_call\":{\"name\":\"list_accounts\",\"arguments\":{}}}"),
		answerReply("You have two accounts."))
	l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model", "tool_call_style": "prompted",
	}).requireStatus(http.StatusOK)
	id := l.alex.post("/assistant/conversations", nil).requireStatus(http.StatusCreated).json()["id"].(string)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "what accounts?"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "You have two accounts.", body["answer"])
	require.Equal(t, []any{"list_accounts"}, body["tool_calls"])
	require.NotContains(t, model.seen[0], "tools")
	second := model.seen[1]["messages"].([]any)
	last := second[len(second)-1].(map[string]any)
	require.Equal(t, "user", last["role"])
	require.True(t, strings.HasPrefix(last["content"].(string), "Result of list_accounts"))
}

func TestADryRunRecordsWhatItWouldHaveDoneAndChangesNothing(t *testing.T) {
	// The question before switching an automation on is "what would it do to
	// my ledger". A dry run answers it with changes switched off, leaves a card
	// nobody can apply, and touches no row.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","category_id":"`+l.str("groceries")+`","summary":"Corner Store is groceries"}`),
		answerReply("I would have recategorized Corner Store as Groceries."))
	configure(l, model) // changes stay off
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner"), "dry_run": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, true, run["dry_run"])
	require.Equal(t, float64(1), run["actions"])
	require.Contains(t, run["prompt"], "This is a dry run")

	// The model was told nothing happened, and the card says so too.
	result := run["conversation"].(map[string]any)["messages"].([]any)[1].(map[string]any)
	require.Contains(t, result["content"], "recorded as a dry run")
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, domain.AssistantActionSimulated, action["status"])
	require.Equal(t, "/transactions/"+l.str("august_corner"), action["path"])

	// Not in the queue, not applicable, and the row is as it was.
	require.Empty(t, l.alex.get("/assistant-automations/pending").requireStatus(http.StatusOK).list())
	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusConflict)
	row := l.alex.get("/transactions/" + l.str("august_corner")).requireStatus(http.StatusOK).json()
	require.Nil(t, row["category_id"])
}

// seedHistory writes n past rows for a payee with one category, so the vote
// has something to count.
func seedHistory(l *ledger, payee, statement string, category string, n int) {
	l.t.Helper()
	for i := 0; i < n; i++ {
		body := map[string]any{
			"account_id": l.str("checking"), "date": fmt.Sprintf("2026-0%d-1%d", 1+i%6, i%9),
			"amount": "-12.50", "payee": payee, "statement_name": statement,
		}
		if category != "" {
			body["category_id"] = category
		}
		l.alex.post("/transactions", body).requireStatus(http.StatusCreated)
	}
}

func TestAUnanimousHistoryDecidesWithoutTheModel(t *testing.T) {
	// Corner Store has been Groceries eight times. The eighth-plus-one does
	// not need a language model: the card is made from the history, the run
	// says Agentifi decided, and the model was never called.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("should not be asked"))
	allowWrites(l, model)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 8)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "agentifi", run["decided_by"])
	require.GreaterOrEqual(t, run["confidence"].(float64), 0.85)
	require.Equal(t, float64(0), run["tool_calls"])
	require.Equal(t, float64(1), run["actions"])
	require.Contains(t, run["output"], "Groceries")
	require.Empty(t, model.seen, "the model was called for a row the history settled")

	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "pending", action["status"])
	require.Equal(t, l.str("groceries"), action["body"].(map[string]any)["category_id"])
	require.Contains(t, action["summary"], "8 of 8")

	// The same row, already Groceries: nothing to do, and still no model.
	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).requireStatus(http.StatusOK)
	again := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSkipped, again["status"])
	require.Contains(t, again["output"], "history agrees")
	require.Empty(t, model.seen)
}

// splitInTwo splits a row in two parts; an empty category leaves that part
// unfiled.
func splitInTwo(l *ledger, txnID, first, firstCategory, second, secondCategory string) {
	l.t.Helper()
	part := func(amount, category string) map[string]any {
		out := map[string]any{"amount": amount}
		if category != "" {
			out["category_id"] = category
		}
		return out
	}
	l.alex.put("/transactions/"+txnID+"/splits", map[string]any{"splits": []any{
		part(first, firstCategory), part(second, secondCategory),
	}}).requireStatus(http.StatusOK)
}

func TestACheckOnAFullyFiledSplitRowProposesNoCategoryForItsParent(t *testing.T) {
	// The history would file Corner Store under Groceries without a model.
	// A split row whose every part is filed has its categories already, and
	// the parent of a split row carries none, so the check leaves it alone.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("should not be asked"))
	allowWrites(l, model)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 8)
	splitInTwo(l, l.str("august_corner"), "-15.00", l.str("groceries"), "-10.00", l.str("food"))
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSkipped, run["status"], run["error"])
	require.Equal(t, float64(0), run["actions"])
	require.Contains(t, run["output"], "every part filed")
	require.Empty(t, model.seen)

	row := l.alex.get("/transactions/" + l.str("august_corner")).requireStatus(http.StatusOK).json()
	require.Nil(t, row["category_id"])
	require.Nil(t, row["category_checked_at"], "a skip that settled the row marks nothing")
	require.Nil(t, row["suggestion"])
}

func TestACheckOnASplitRowWithAnUnfiledPartAsksTheModel(t *testing.T) {
	// The history names one category for a whole row, which is no answer for
	// a split; the model can propose a split.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Left as is."))
	allowWrites(l, model)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 8)
	splitInTwo(l, l.str("august_corner"), "-15.00", l.str("groceries"), "-10.00", "")
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "model", run["decided_by"])
	require.Equal(t, float64(0), run["actions"])
	require.Len(t, model.seen, 1)
	opening := model.seen[0]["messages"].([]any)[1].(map[string]any)["content"].(string)
	require.Contains(t, opening, `"splits"`, "the row is shown with its splits")
}

func TestSplitHistoryIsNotReadAsReviewedAndKeptUncategorized(t *testing.T) {
	// Two earlier payments to the same person, each split and filed part by
	// part and then reviewed. Their parents carry no category, which is not
	// the household leaving the payee uncategorized.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Proposed."))
	allowWrites(l, model)
	for _, date := range []string{"2026-07-03", "2026-08-03"} {
		past := l.alex.post("/transactions", map[string]any{
			"account_id": l.str("checking"), "date": date, "amount": "-40.00",
			"payee": "Cousin Pat", "statement_name": "ZELLE TO COUSIN PAT", "is_reviewed": true,
		}).requireStatus(http.StatusCreated).json()["id"].(string)
		splitInTwo(l, past, "-25.00", l.str("groceries"), "-15.00", l.str("food"))
	}
	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-01", "amount": "-40.00",
		"payee": "Cousin Pat", "statement_name": "ZELLE TO COUSIN PAT",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Len(t, model.seen, 1)
	opening := model.seen[0]["messages"].([]any)[1].(map[string]any)["content"].(string)
	require.Contains(t, opening, "2 were split and every part filed")
	require.NotContains(t, opening, "reviewed and kept uncategorized")
	require.Contains(t, opening, `"splits"`)
	require.Contains(t, opening, `"uncategorized_rows": 2`,
		"the account's seeded July row and corner store; the split payments are filed")
}

func TestATransferCounterpartyIsFiledAsATransfer(t *testing.T) {
	// The mistake this guards against: a credit card payment "categorized"
	// as a loan payment. Its history is transfer legs, and the vote files it
	// under Transfer before any model can be asked what a payment "is".
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("should not be asked"))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-01", "amount": "-200.00",
		"payee": "Transfer to Rewards Card", "statement_name": "ONLINE TRANSFER TO CARD",
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "agentifi", run["decided_by"])
	require.Contains(t, run["output"], "transfer legs")
	require.Equal(t, float64(1), run["actions"])
	require.Empty(t, model.seen)
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, categoryWithMarker(l, domain.KnownCategoryTransfer),
		action["body"].(map[string]any)["category_id"])
}

func TestAnUnsettledHistoryGoesToTheModelWithTheVote(t *testing.T) {
	// Split history: the model is asked, and its opening message carries
	// Agentifi's assessment with the leading category named.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Left as is."))
	allowWrites(l, model)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 3)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("food"), 3)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "model", run["decided_by"])
	require.Less(t, run["confidence"].(float64), 0.85)
	require.Len(t, model.seen, 1)
	opening := model.seen[0]["messages"].([]any)[1].(map[string]any)["content"].(string)
	require.Contains(t, opening, "### Agentifi's assessment")
	require.Contains(t, opening, "Confidence 0.")
	require.Contains(t, opening, "does not settle this")
}

func TestUnreviewedUncategorizedHistoryIsNotReadAsLeavingThePayeeAlone(t *testing.T) {
	// A chain's third visit, the first two still waiting in the register with
	// no category. Nobody has decided anything about this payee, so the model
	// is told there is no categorized history and to decide from the row.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Proposed."))
	allowWrites(l, model)
	seedHistory(l, "Burger Barn", "BURGER BARN 0412", "", 2)
	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-01", "amount": "-9.00",
		"payee": "Burger Barn", "statement_name": "BURGER BARN 0412",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "model", run["decided_by"])
	require.Nil(t, run["confidence"], "no history and no stated figure, so no score")
	require.Len(t, model.seen, 1)
	opening := model.seen[0]["messages"].([]any)[1].(map[string]any)["content"].(string)
	require.Contains(t, opening, "None of the 2 past rows from this payee has a category: "+
		"2 nobody has reviewed yet, which is not a decision.")
	require.Contains(t, opening, "does not stop a guess")
	require.Contains(t, opening, "well-known chain or brand")
	require.NotContains(t, opening, "leaves this payee alone")
}

func TestReviewedUncategorizedHistoryLowersConfidenceButAllowsAGuess(t *testing.T) {
	// The same payee, but somebody reviewed those rows in the app and kept
	// them without a category. That weighs against a category; it does not
	// forbid one.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Proposed."))
	allowWrites(l, model)
	for _, date := range []string{"2026-07-03", "2026-08-03"} {
		l.alex.post("/transactions", map[string]any{
			"account_id": l.str("checking"), "date": date, "amount": "-9.00",
			"payee": "Burger Barn", "statement_name": "BURGER BARN 0412", "is_reviewed": true,
		}).requireStatus(http.StatusCreated)
	}
	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-01", "amount": "-9.00",
		"payee": "Burger Barn", "statement_name": "BURGER BARN 0412",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "model", run["decided_by"])
	require.Len(t, model.seen, 1)
	opening := model.seen[0]["messages"].([]any)[1].(map[string]any)["content"].(string)
	require.Contains(t, opening, "2 the household reviewed and kept uncategorized, which "+
		"lowers confidence in a category but does not forbid one")
	require.Contains(t, opening, "does not stop a guess")
	require.NotContains(t, opening, "leaves this payee alone")
	require.NotContains(t, opening, "nobody has reviewed")
}

func TestAFundOnAnInvestmentAccountIsGuessedWithTheModelsConfidence(t *testing.T) {
	// Monthly purchases of a retirement fund on a brokerage account, none
	// categorized. Rows there are born reviewed, so the flag is nobody's
	// decision: the model is told so, guesses from the fund's name, and the
	// run records the confidence it stated.
	l := buildLedger(t)
	brokerage := l.alex.post("/accounts", map[string]any{
		"name": "Brokerage B", "kind": string(domain.KindInvestment), "type": "brokerage",
		"currency": "USD",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	retirement := l.alex.post("/categories", map[string]any{
		"name": "Retirement Contribution", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	for _, date := range []string{"2026-06-15", "2026-07-15", "2026-08-15"} {
		l.alex.post("/transactions", map[string]any{
			"account_id": brokerage, "date": date, "amount": "-150.00",
			"payee": "Target Date Fund", "statement_name": "BUY TARGET DATE FUND",
		}).requireStatus(http.StatusCreated)
	}
	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": brokerage, "date": "2026-09-15", "amount": "-150.00",
		"payee": "Target Date Fund", "statement_name": "BUY TARGET DATE FUND",
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+arrived+`","category_id":"`+
			retirement+`","summary":"A target-date fund bought on a brokerage account `+
			`(confidence 0.55)"}`),
		answerReply("Proposed Retirement Contribution.\nConfidence: 0.55"))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "model", run["decided_by"])
	require.InDelta(t, 0.55, run["confidence"].(float64), 0.0001)
	require.Equal(t, float64(1), run["actions"])
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "pending", action["status"])
	require.Equal(t, retirement, action["body"].(map[string]any)["category_id"])

	opening := model.seen[0]["messages"].([]any)[1].(map[string]any)["content"].(string)
	require.Contains(t, opening, "None of the 3 past rows from this payee has a category: "+
		"3 arrived marked reviewed")
	require.Contains(t, opening, "a fund or security on an investment account")
	require.NotContains(t, opening, "leaves this payee alone")
	require.NotContains(t, opening, "the household reviewed")
	require.Contains(t, opening, `"reviewed_on_arrival": true`)
}

func TestAZeroThresholdAlwaysAsksTheModel(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Asked."))
	allowWrites(l, model)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 8)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0,
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "model", run["decided_by"])
	require.Len(t, model.seen, 1)
	l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 1.5,
	})).requireStatus(http.StatusBadRequest)
}

func TestAnAutomationCannotFileMoneyOutUnderAnIncomeCategory(t *testing.T) {
	// A $25.00 Corner Store purchase filed under an income category, because
	// a mis-filed history said so. The tool refuses on the round the model can
	// still act on, and the refusal names the direction of the money.
	l := buildLedger(t)
	pay := l.alex.post("/categories", map[string]any{
		"name": "Paycheck", "kind": string(domain.CategoryIncome),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","category_id":"`+pay+`","summary":"Paycheck, as the history says"}`),
		answerReply("I proposed nothing."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, float64(0), run["actions"])
	tool := run["conversation"].(map[string]any)["messages"].([]any)[1].(map[string]any)
	require.Contains(t, tool["content"], "money out")
	require.Contains(t, tool["content"], "income category")

	// The same proposal in chat is a person's call and goes through.
	chat := l.alex.post("/assistant/conversations", nil).requireStatus(http.StatusCreated).json()["id"].(string)
	model.at = 0
	body := l.alex.post("/assistant/conversations/"+chat+"/ask",
		map[string]any{"question": "file the Corner Store row under Paycheck"}).
		requireStatus(http.StatusOK).json()
	require.Len(t, body["conversation"].(map[string]any)["actions"], 1)
}

func TestAModelsRefundOnACardWaitsForAPersonEvenWhereChangesApply(t *testing.T) {
	// A $14.50 credit from an outfitter on the credit card, with no history
	// behind it. The model files it under the category of what was bought,
	// which is where a return belongs; the automation is set to apply, but a
	// refund is held as a card for a person.
	l := buildLedger(t)
	credit := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("card"), "date": "2026-09-03", "amount": "14.50",
		"payee": "Example Outfitters", "statement_name": "EXAMPLE OUTFITTERS RETURN",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+credit+`","category_id":"`+
			l.str("groceries")+`","summary":"Groceries: a return to the outfitter"}`),
		answerReply("Proposed Groceries for the return.\nConfidence: 0.9"))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85, "mode": domain.AutomationModeApply,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": credit}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "model", run["decided_by"])
	require.Equal(t, float64(1), run["actions"])
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "pending", action["status"])
	require.Equal(t, l.str("groceries"), action["body"].(map[string]any)["category_id"])

	row := l.alex.get("/transactions/" + credit).requireStatus(http.StatusOK).json()
	require.Nil(t, row["category_id"], "a refund is proposed, never applied unasked")
}

func TestABlindRunHidesTheCategoryAndRemembersIt(t *testing.T) {
	// "How close does it get?" is answered by showing the automation a row
	// somebody already categorized, with the category hidden. The history
	// still settles it, the card is a dry-run card, and the run keeps what
	// the row said so the two can be compared.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("should not be asked"))
	configure(l, model) // changes stay off: a blind run changes nothing
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 8)
	l.alex.patch("/transactions/"+l.str("august_corner"),
		map[string]any{"category_id": l.str("groceries")}).requireStatus(http.StatusOK)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	// Not blind: the row already says Groceries and the history agrees.
	plain := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner"), "dry_run": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSkipped, plain["status"])

	// Blind: the same row is decided as if it had no category.
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner"), "blind": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, true, run["blind"])
	require.Equal(t, true, run["dry_run"], "a blind run is a dry run whatever the body said")
	require.Equal(t, l.str("groceries"), run["expected_category_id"])
	require.Equal(t, "agentifi", run["decided_by"])
	require.Equal(t, float64(1), run["actions"])
	require.Contains(t, run["output"], "hidden from this run")
	require.Empty(t, model.seen)

	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, domain.AssistantActionSimulated, action["status"])
	require.Equal(t, l.str("groceries"), action["body"].(map[string]any)["category_id"])
	row := l.alex.get("/transactions/" + l.str("august_corner")).requireStatus(http.StatusOK).json()
	require.Equal(t, l.str("groceries"), row["category_id"], "the row is as it was")
}

func TestABlindRunShowsTheModelAnUncategorizedRow(t *testing.T) {
	// A split history sends the row to the model, and the model must see the
	// row as the household would have before categorizing it: no category on
	// the row, the payee's other rows as they are.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","category_id":"`+l.str("groceries")+`","summary":"Corner Store is groceries"}`),
		answerReply("Groceries."))
	configure(l, model)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 3)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("food"), 3)
	l.alex.patch("/transactions/"+l.str("august_corner"),
		map[string]any{"category_id": l.str("food")}).requireStatus(http.StatusOK)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner"), "blind": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "model", run["decided_by"])
	require.Equal(t, l.str("food"), run["expected_category_id"])
	require.Equal(t, float64(1), run["actions"])

	opening := run["conversation"].(map[string]any)["messages"].([]any)[0].(map[string]any)
	shown := opening["content"].(string)
	require.Contains(t, shown, `"category": null`)
	require.NotContains(t, shown[:strings.Index(shown, "### The account")], l.str("food"),
		"the row's own category leaked into what the model was shown")
	require.Contains(t, shown, "6 categorized rows", "the payee's other rows are still evidence")
}

func TestGenericWordingFallsBackToTheAccountsOwnHistory(t *testing.T) {
	// "Payment" on a student loan names nobody, so the payee lookup found
	// nothing and the model declined to classify it. The account is the
	// payee there: its other rows with money the same way are the history,
	// the vote settles it without a model, and a payment arriving at a loan is
	// not refused for being money in an expense category.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("should not be asked"))
	allowWrites(l, model)
	loan := l.alex.post("/accounts", map[string]any{
		"name": "Student Loan A", "kind": string(domain.KindLoan), "type": "student_loan",
		"currency": "USD", "opening_balance": "-5000",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	payment := l.alex.post("/categories", map[string]any{
		"name": "Loan Payment", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	for i := 0; i < 8; i++ {
		l.alex.post("/transactions", map[string]any{
			"account_id": loan, "date": fmt.Sprintf("2026-0%d-1%d", 1+i%6, i%9),
			"amount": "55.00", "payee": "Payment", "statement_name": "Payment",
			"category_id": payment,
		}).requireStatus(http.StatusCreated)
	}
	// Interest charged runs the other way and must not vote.
	l.alex.post("/transactions", map[string]any{
		"account_id": loan, "date": "2026-07-01", "amount": "-12.00",
		"payee": "Payment", "statement_name": "Payment", "category_id": l.str("groceries"),
	}).requireStatus(http.StatusCreated)
	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": loan, "date": "2026-08-01", "amount": "55.00",
		"payee": "Payment", "statement_name": "Payment",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "agentifi", run["decided_by"])
	require.Equal(t, float64(1), run["actions"])
	require.Contains(t, run["output"], "Loan Payment")
	require.Empty(t, model.seen)
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, payment, action["body"].(map[string]any)["category_id"])
	require.Contains(t, action["summary"], "8 of 8")
	opening := run["conversation"].(map[string]any)["messages"].([]any)[0].(map[string]any)
	require.Contains(t, opening["content"], "own rows with money running the same way")

	// What a model would have been told: the account, and what it is for.
	preview := l.alex.post("/assistant-automations/"+id+"/preview",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	shown := preview["opening"].(string)
	require.Contains(t, shown, "### The account")
	require.Contains(t, shown, `"name": "Student Loan A"`)
	require.Contains(t, shown, "A loan: money in is a payment on the loan")
	require.Contains(t, shown, `"category": "Loan Payment"`)
	require.Contains(t, shown, "the account stands in for the payee")
}

func TestTheAssessmentDoesNotCallACreditUnderSpendingAWrongHistory(t *testing.T) {
	// Six returns to an outfitter on the card, filed under Groceries, and a
	// seventh arriving. Money in under an expense category is what a refund
	// looks like, and the assessment says so rather than call the history wrong.
	// Money out under an income category is still a history gone wrong.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("unused"))
	allowWrites(l, model)
	for i := 0; i < 6; i++ {
		l.alex.post("/transactions", map[string]any{
			"account_id": l.str("card"), "date": fmt.Sprintf("2026-0%d-11", 2+i),
			"amount": "10.00", "payee": "Example Outfitters", "statement_name": "EXAMPLE OUTFITTERS",
			"category_id": l.str("groceries"),
		}).requireStatus(http.StatusCreated)
	}
	credit := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("card"), "date": "2026-09-02", "amount": "10.00",
		"payee": "Example Outfitters", "statement_name": "EXAMPLE OUTFITTERS",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	pay := l.alex.post("/categories", map[string]any{
		"name": "Paycheck", "kind": string(domain.CategoryIncome),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	seedHistory(l, "Corner Store", "CORNER STORE", pay, 6)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	shown := l.alex.post("/assistant-automations/"+id+"/preview",
		map[string]any{"transaction_id": credit}).requireStatus(http.StatusOK).json()["opening"].(string)
	require.Contains(t, shown, "That is an expense category and this row is money in")
	require.Contains(t, shown, "right for a refund, return or credit from a merchant")
	require.NotContains(t, shown, "Do not follow it")
	require.Contains(t, shown, "A credit card: money out is a charge or interest")

	shown = l.alex.post("/assistant-automations/"+id+"/preview",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()["opening"].(string)
	require.Contains(t, shown, "that is an income category and this row is money out")
	require.Contains(t, shown, "Do not follow it")
}

func TestRowsCanBeFiredAtTheAutomationsByHand(t *testing.T) {
	// A person ticks rows in the register and asks for the automations to
	// look at them now. They are queued as if they had just arrived — the
	// trigger's own filters still apply — and not twice while still waiting.
	l := buildLedger(t)
	allowWrites(l, newFakeModel(t, answerReply("nothing to say")))
	l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"trigger_config": map[string]any{"skip_transfers": true},
	})).requireStatus(http.StatusCreated)

	l.alex.post("/assistant-automations/fire", map[string]any{"transaction_ids": []string{}}).
		requireStatus(http.StatusBadRequest)
	out := l.alex.post("/assistant-automations/fire", map[string]any{
		"transaction_ids": []string{l.str("august_groceries"), l.str("transfer_out")},
	}).requireStatus(http.StatusAccepted).json()
	require.Equal(t, float64(2), out["rows"])
	require.Equal(t, float64(1), out["queued"], "the transfer leg is skipped by the trigger")

	waiting := l.alex.get("/assistant-automations/runs?status=queued").requireStatus(http.StatusOK).list()
	require.Len(t, waiting, 1)
	require.Equal(t, l.str("august_groceries"), waiting[0]["transaction_id"])
	require.Equal(t, domain.AutomationFiredByManual, waiting[0]["fired_by"])

	again := l.alex.post("/assistant-automations/fire", map[string]any{
		"transaction_ids": []string{l.str("august_groceries")},
	}).requireStatus(http.StatusAccepted).json()
	require.Equal(t, float64(0), again["queued"])

	// A viewer may not fire anything.
	l.as("vera").post("/assistant-automations/fire", map[string]any{
		"transaction_ids": []string{l.str("august_groceries")},
	}).requireStatus(http.StatusForbidden)
}

func TestACorrectedCategoryIsShownToTheNextRunAboutThatPayee(t *testing.T) {
	// The loop the whole thing exists for. Somebody changes a category on a
	// card; the next run about the same payee is shown what was proposed and
	// what they chose, in words, so the model has something to be right about
	// beyond the payee's history — which is the very thing it just got wrong.
	l := buildLedger(t)
	txn := l.str("august_corner")
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Corner Store is Food","category_id":"`+l.str("food")+`"}`),
		answerReply("Proposed."))
	conversation := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+conversation+"/ask",
		map[string]any{"question": "categorize it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply",
		map[string]any{"category_id": l.str("groceries")}).requireStatus(http.StatusOK)

	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"context": map[string]any{
			"transaction": true, "similar_transactions": 5, "categories": true,
			"corrections": true,
		},
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	preview := l.alex.post("/assistant-automations/"+id+"/preview",
		map[string]any{"transaction_id": txn}).requireStatus(http.StatusOK).json()
	opening := preview["opening"].(string)
	require.Contains(t, opening, "### Corrections the household has made")
	require.Contains(t, opening, `"you_proposed": "Food \u0026 Dining"`)
	require.Contains(t, opening, `"they_chose": "Food \u0026 Dining › Groceries"`)
	require.Contains(t, opening, `"about": "this payee"`)
}

func TestAnAutomationThatWasNotAskedForCorrectionsIsNotShownThem(t *testing.T) {
	l := buildLedger(t)
	txn := l.str("august_corner")
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Corner Store is Food","category_id":"`+l.str("food")+`"}`),
		answerReply("Proposed."))
	conversation := allowWrites(l, model)
	body := l.alex.post("/assistant/conversations/"+conversation+"/ask",
		map[string]any{"question": "categorize it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply",
		map[string]any{"category_id": l.str("groceries")}).requireStatus(http.StatusOK)

	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	preview := l.alex.post("/assistant-automations/"+id+"/preview",
		map[string]any{"transaction_id": txn}).requireStatus(http.StatusOK).json()
	require.NotContains(t, preview["opening"], "Corrections the household has made")
}

// --- A transfer the household never matched ----------------------------------

// "Is this a transfer" is two clauses — a matched pair leg, or a row filed
// under a transfer-type category — and everything the assistant sees is built
// from both. Built from the paired leg alone, a household that files
// transfers by category without pairing the legs would have rows that are
// transfers in every sense except the one the model is told about.

// transferCategory is a real transfer-type category in the space.
func transferCategory(t *testing.T, l *ledger) *store.Category {
	t.Helper()
	category := &store.Category{
		Name: "Transfer", Kind: domain.CategoryTransfer,
		IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(t, db(t).CreateCategory(t.Context(),
		store.SpaceIDOf(l.id("space")), category))
	return category
}

func TestARowFiledAsATransferIsShownToTheModelAsOne(t *testing.T) {
	// The subject's own row, as the model reads it. The payee is new, so the
	// history decides nothing and the run reaches the model — which is the
	// only way to see what it was handed.
	l := buildLedger(t)
	category := transferCategory(t, l)
	model := newFakeModel(t, answerReply("Left as is."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	subject := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.September, 1),
		Amount: domain.MustFromString("-425.00"), Currency: "USD",
		StatementName: "MOVE TO EXAMPLE BROKERAGE", Payee: "To Example Brokerage",
		CategoryID: category.ID, Source: domain.SourceSync,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), subject))

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": subject.ID.String()}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])

	require.NotEmpty(t, model.seen, "the model was never asked, so it was told nothing")
	messages := model.seen[0]["messages"].([]any)
	opening := messages[1].(map[string]any)["content"].(string)
	require.Contains(t, opening, "### The transaction")
	require.Contains(t, opening, `"is_transfer": true`,
		"a row filed under a transfer category was described to the model as not a transfer")
}

func TestAHistoryOfUnmatchedTransfersVotesTheSameAsMatchedLegs(t *testing.T) {
	// The vote reads the same field. Three past rows under the transfer
	// category, none of them ever paired: the verdict is "this payee is a
	// transfer", as for a payee whose history is matched legs, and no model is
	// asked. The household's own Transfer is the one adopted and proposed.
	l := buildLedger(t)
	category := transferCategory(t, l)
	space := store.SpaceIDOf(l.id("space"))
	for day := 1; day <= 3; day++ {
		require.NoError(t, db(t).CreateTransaction(t.Context(), space, &store.Transaction{
			AccountID: l.id("checking"),
			Date:      domain.NewDate(2026, time.June, day),
			Amount:    domain.MustFromString("-425.00"), Currency: "USD",
			StatementName: "MOVE TO EXAMPLE BROKERAGE", Payee: "To Example Brokerage",
			CategoryID: category.ID, IsReviewed: true, Source: domain.SourceSync,
		}))
	}

	model := newFakeModel(t, answerReply("should not be asked"))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	arrived := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-01", "amount": "-425.00",
		"payee": "To Example Brokerage", "statement_name": "MOVE TO EXAMPLE BROKERAGE",
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()

	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "agentifi", run["decided_by"])
	require.Contains(t, run["output"], "transfer")
	require.Empty(t, model.seen)
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, category.ID.String(), action["body"].(map[string]any)["category_id"])
}

func TestAnImportedRowIsCheckedWhenSomebodyAsks(t *testing.T) {
	// The rule that only a bank's or a file's row fires an automation is about
	// *arrival* — a hand-entered row was categorized by whoever typed it — and
	// must not be asked of a person's own "run these", or ticking rows in the
	// register queues nothing at all. Nearly every row of an imported ledger
	// is a source that rule refuses. The same button on one row forces, and
	// the batch must agree with it.
	l := buildLedger(t)
	allowWrites(l, newFakeModel(t, answerReply("nothing to say")))
	l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated)

	imported := l.alex.get("/transactions/" + l.str("july")).
		requireStatus(http.StatusOK).json()
	require.Equal(t, string(domain.SourceSimplifiImport), imported["source"],
		"the fixture row this test is about must be an imported one")

	out := l.alex.post("/assistant-automations/fire", map[string]any{
		"transaction_ids": []string{l.str("july")},
	}).requireStatus(http.StatusAccepted).json()
	require.Equal(t, float64(1), out["queued"], "an imported row was refused a check by hand")

	waiting := l.alex.get("/assistant-automations/runs?status=queued").
		requireStatus(http.StatusOK).list()
	require.Len(t, waiting, 1)
	require.Equal(t, l.str("july"), waiting[0]["transaction_id"])
}

func TestADryRunReachesTheImportedRowsToo(t *testing.T) {
	// The same gate, one screen over: "dry run for the 5 most recent matching
	// transactions" picks the newest rows the automation is about. Asked as an
	// arrival, it would skip every imported one — which in a carried-over
	// ledger is all of them, so the button would answer "no recent transaction
	// would fire this automation".
	l := buildLedger(t)
	model := newFakeModel(t)
	for i := 0; i < 8; i++ {
		model.replies = append(model.replies, answerReply("Nothing to change."))
	}
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	runs := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"dry_run": true, "recent": 5}).requireStatus(http.StatusAccepted).list()
	subjects := make([]string, 0, len(runs))
	for _, run := range runs {
		subjects = append(subjects, run["transaction_id"].(string))
	}
	require.Contains(t, subjects, l.str("july"), "the imported row was left out of the dry run")
}

func TestARefundFromAShopIsProposedUnderTheCategoryOfWhatWasBought(t *testing.T) {
	// Six Corner Store purchases under Groceries, and an $8.00 credit from it
	// matching none of them to the cent. The category check reads the shop's
	// purchases without asking the model and proposes Groceries — for a person
	// to accept, even though the automation is set to apply.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("should not be asked"))
	allowWrites(l, model)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 6)
	credit := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-06-20", "amount": "8.00",
		"statement_name": "CORNER STORE REFUND",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85, "mode": domain.AutomationModeApply,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": credit}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "agentifi", run["decided_by"])
	require.Empty(t, model.seen, "the model was called for a refund the purchases settled")
	require.Contains(t, run["output"], "Proposed: Groceries")

	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "pending", action["status"])
	require.Equal(t, l.str("groceries"), action["body"].(map[string]any)["category_id"])
	require.Contains(t, action["summary"], "6 of 6 categorized purchases")

	row := l.alex.get("/transactions/" + credit).requireStatus(http.StatusOK).json()
	require.Nil(t, row["category_id"], "a refund is proposed, never applied unasked")
}

func TestARefundMatchingAPurchaseToTheCentTakesThatPurchasesCategory(t *testing.T) {
	// Corner Store is Groceries six times, but the $31.00 credit gives back
	// the one $31.00 purchase, which was filed under Food.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("should not be asked"))
	allowWrites(l, model)
	seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 6)
	l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-06-02", "amount": "-31.00",
		"statement_name": "CORNER STORE", "category_id": l.str("food"),
	}).requireStatus(http.StatusCreated)
	credit := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-06-20", "amount": "31.00",
		"statement_name": "CORNER STORE",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": credit}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "agentifi", run["decided_by"])
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "pending", action["status"])
	require.Equal(t, l.str("food"), action["body"].(map[string]any)["category_id"])
	require.Contains(t, action["summary"], "purchase of the same amount")
}
