package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The assistant when it can change things.
//
// What is being checked here is the gap between proposing and applying, in
// both directions: that a write tool reaches nothing on its own, and that the
// same request does reach the real handler — with the real validation — when
// somebody presses the button.
//
// The stand-in model is scripted, so a test says exactly which tool the model
// called with which arguments. The interesting failures are all on this side.

// someTransaction is a real row in the seeded ledger, by id.
//
// Read through the API rather than reached for in the fixture, so a test that
// proposes a change to it is proposing one against a row the register actually
// lists.
func someTransaction(l *ledger) string {
	l.t.Helper()
	rows := l.alex.get("/transactions?from=2000-01-01&to=2100-01-01").
		requireStatus(http.StatusOK).json()["items"].([]any)
	require.NotEmpty(l.t, rows, "the seeded ledger has no transactions")
	return rows[0].(map[string]any)["id"].(string)
}

// allowWrites switches changes on for the space and returns a conversation id.
func allowWrites(l *ledger, model *fakeModel) string {
	l.t.Helper()
	l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model", "api_key": "sk-test-key",
		"allow_writes": true,
	}).requireStatus(http.StatusOK)
	return l.alex.post("/assistant/conversations", nil).
		requireStatus(http.StatusCreated).json()["id"].(string)
}

func TestANewConnectionProposesButNeverAppliesUnasked(t *testing.T) {
	// A household that sets up a model can be shown suggestions at once, each
	// a card it accepts; nothing is applied without asking until it says so.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("fine"))
	l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model",
	}).requireStatus(http.StatusOK)

	body := l.alex.get("/assistant").requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["allow_writes"])
	require.Equal(t, false, body["apply_without_asking"])

	// A later save that leaves the field out keeps what is stored.
	l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model", "allow_writes": false,
	}).requireStatus(http.StatusOK)
	again := l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "another-model",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, false, again["allow_writes"])
}

func TestTheCatalogueOfferedShrinksWhenChangesAreOff(t *testing.T) {
	// The write half is absent rather than refused: a model that cannot see a
	// tool does not offer to use it.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("fine"))
	id := configure(l, model)
	l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "hello"}).requireStatus(http.StatusOK)

	encoded, err := json.Marshal(model.seen[0]["tools"])
	require.NoError(t, err)
	require.Contains(t, string(encoded), "search_transactions")
	require.NotContains(t, string(encoded), "update_transaction")
}

func TestAWriteToolIsRefusedAtTheToolCallWhenChangesAreOff(t *testing.T) {
	// Not only absent from the list. A smaller model that has seen the name
	// somewhere will call it anyway, and the refusal has to be at the point of
	// use rather than only at the point of offering.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.id("stranger_txn").String()+
			`","summary":"x","payee":"Nope"}`),
		answerReply("I cannot change that."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusOK).json()
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "has not switched changes on")
	require.Empty(t, body["conversation"].(map[string]any)["actions"])
}

func TestAProposedChangeChangesNothingUntilItIsApplied(t *testing.T) {
	// The property the whole design rests on.
	l := buildLedger(t)
	txn := someTransaction(l)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Rename it to Corner Store","payee":"Corner Store"}`),
		answerReply("I have put one change in front of you."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename that payee"}).requireStatus(http.StatusOK).json()

	actions := body["conversation"].(map[string]any)["actions"].([]any)
	require.Len(t, actions, 1)
	action := actions[0].(map[string]any)
	require.Equal(t, "pending", action["status"])
	require.Equal(t, "PATCH", action["method"])
	require.Equal(t, "/transactions/"+txn, action["path"])
	require.Equal(t, "Rename it to Corner Store", action["summary"])

	// And the ledger is untouched.
	before := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.NotEqual(t, "Corner Store", before["payee"])
}

func TestApplyingAChangeIssuesTheStoredRequestForReal(t *testing.T) {
	l := buildLedger(t)
	txn := someTransaction(l)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Rename it","payee":"Corner Store"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	applied := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"], "%v", applied["result"])
	require.EqualValues(t, http.StatusOK, applied["status_code"])

	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.Equal(t, "Corner Store", after["payee"])
}

func TestApplyingAChangeMarksTheRowReviewed(t *testing.T) {
	// The queue of cards is a review queue: pressing Apply is somebody reading
	// the row, reading what was proposed for it, and saying yes. Leaving it
	// unreviewed afterwards would mean finding it again in the register to
	// tick it.
	l := buildLedger(t)
	txn := someTransaction(l)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Rename it","payee":"Corner Store"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	before := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.Equal(t, false, before["is_reviewed"])

	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK)

	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.Equal(t, "Corner Store", after["payee"])
	require.Equal(t, true, after["is_reviewed"])
}

func TestAChangeAppliedWithoutAskingLeavesTheRowToBeReviewed(t *testing.T) {
	// The other half of the rule. Nobody looked at this one, so the register
	// must still ask them to — a tick here would report a review that did not
	// happen, on exactly the rows most worth a second pair of eyes.
	l := buildLedger(t)
	txn := someTransaction(l)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Rename it","payee":"Corner Store"}`),
		answerReply("I renamed it."))
	id := applyWithoutAsking(l, model)

	l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusOK)

	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.Equal(t, "Corner Store", after["payee"])
	require.Equal(t, false, after["is_reviewed"])
}

func TestApplyingAChangeThatSaysUnreviewedDoesNotOverruleIt(t *testing.T) {
	// The tick is a default, not an override. A card that states the flag has
	// already said what it wants, and turning "mark this unreviewed" into its
	// opposite would be the card doing the reverse of what it read.
	l := buildLedger(t)
	txn := someTransaction(l)
	l.alex.patch("/transactions/"+txn, map[string]any{"is_reviewed": true}).
		requireStatus(http.StatusOK)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"It needs another look","is_reviewed":false}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "flag it for me"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK)

	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.Equal(t, false, after["is_reviewed"])
}

func TestAChangeIsAppliedOnceHoweverManyTimesTheButtonIsPressed(t *testing.T) {
	// A card open in two tabs, or double-clicked. "Create this transaction"
	// must not be a thing an impatient person can do twice.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Vacation","summary":"Add a Vacation tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "add a vacation tag"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK)
	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusConflict)

	made := 0
	for _, tag := range l.alex.get("/tags").requireStatus(http.StatusOK).list() {
		if tag["name"] == "Vacation" {
			made++
		}
	}
	require.Equal(t, 1, made)
}

func TestADiscardedChangeNeverRuns(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Never","summary":"Add a tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "add a tag"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	discarded := l.alex.post("/assistant-actions/"+action["id"].(string)+"/discard", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "discarded", discarded["status"])
	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusConflict)

	for _, tag := range l.alex.get("/tags").requireStatus(http.StatusOK).list() {
		require.NotEqual(t, "Never", tag["name"])
	}
}

func TestAChangeThatTheApiRefusesIsRecordedAsFailedRatherThanApplied(t *testing.T) {
	// The handler's own validation is the one that decides. A proposal that
	// passes the tool's argument check can still be wrong, and the card has to
	// say so rather than reporting a change nobody made.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_category", `{"name":"","kind":"expense","summary":"Add one"}`),
		toolReply("change_endpoint",
			`{"method":"POST","path":"/tags","body":{"name":""},"summary":"Add a nameless tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "add a category"}).requireStatus(http.StatusOK).json()

	// The first call never became a proposal: an empty name is refused where
	// the model can still correct it.
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "`name` is required")

	// The second did, and fails when it is applied.
	actions := body["conversation"].(map[string]any)["actions"].([]any)
	require.Len(t, actions, 1)
	action := actions[0].(map[string]any)
	settled := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "failed", settled["status"])
	require.NotEmpty(t, settled["result"])
}

func TestAProposalTellsTheModelNothingHasChangedYet(t *testing.T) {
	// The outcome reaches it with the next question, never inside the answer
	// that proposed it: there is no round in which a refusal can send it
	// looking for another way through, and no answer that reports a change as
	// done. TestTheModelHearsWhatHappenedToItsCards is the other half.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Later","summary":"Add a tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "add a tag"}).requireStatus(http.StatusOK)

	encoded, err := json.Marshal(model.seen[1]["messages"])
	require.NoError(t, err)
	require.Contains(t, string(encoded), "Nothing has changed yet")
	require.Contains(t, string(encoded), "waiting for the person")
}

func TestAViewerCannotApplyAChange(t *testing.T) {
	// The route is registered with Write, so the refusal is before the handler.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Nope","summary":"Add a tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "add a tag"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	l.as("vera").post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusForbidden)
}

func TestAnotherHouseholdCannotSeeOrApplyAChange(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Private","summary":"Add a tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "add a tag"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	l.as("bob").get("/assistant-actions/" + action["id"].(string)).
		requireStatus(http.StatusNotFound)
	l.as("bob").post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusNotFound)
}

func TestAProposalCannotReachTheAssistantsOwnConfiguration(t *testing.T) {
	// A model editing the provider it is talking through, or asking itself a
	// question, is a loop with a bill attached.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("change_endpoint", `{"method":"DELETE","path":"/assistant/connection",`+
			`"summary":"Remove the provider"}`),
		answerReply("I cannot do that."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "delete your own connection"}).
		requireStatus(http.StatusOK).json()
	require.Empty(t, body["conversation"].(map[string]any)["actions"])
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "not reachable from here")

	// Still configured.
	require.Equal(t, true, l.alex.get("/assistant").
		requireStatus(http.StatusOK).json()["configured"])
}

func TestAProposalCannotReachTheCardsThemselves(t *testing.T) {
	// A card that applies another card is the model approving its own work.
	// The card it would reach need not be one from this conversation either:
	// a card is scoped to the space and the person, so an older one somebody
	// deliberately left pending is in range of the same path.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Pending","summary":"Add a tag"}`),
		answerReply("Proposed."),
		toolReply("change_endpoint", `{"method":"POST","path":"/assistant-actions/`+
			`00000000-0000-0000-0000-000000000000/apply","summary":"Approve it"}`),
		answerReply("I cannot do that."))
	id := allowWrites(l, model)

	first := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "add a tag"}).requireStatus(http.StatusOK).json()
	card := first["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	second := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "now approve that"}).requireStatus(http.StatusOK).json()
	refused := ""
	for _, one := range second["conversation"].(map[string]any)["messages"].([]any) {
		if content, ok := one.(map[string]any)["content"].(string); ok &&
			strings.Contains(content, "not reachable from here") {
			refused = content
		}
	}
	require.NotEmpty(t, refused, "the refusal never reached the model")

	// Nothing new was proposed, and the card that was there is still waiting.
	require.Len(t, second["conversation"].(map[string]any)["actions"].([]any), 1)
	still := l.alex.get("/assistant-actions/" + card["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "pending", still["status"])
}

func TestTheCardEndpointsAreNotOfferedToTheModel(t *testing.T) {
	// list_endpoints reads this list, so a path the dispatcher refuses is not
	// one the model is told about in the first place.
	for _, route := range dispatchableRoutes() {
		require.NotContains(t, route.Path(), "/assistant")
	}
}

func TestAnIdTheModelInventedIsRefusedWhereItCanStillFixIt(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transaction",
			`{"transaction_id":"the coffee one","summary":"x","payee":"Cafe"}`),
		answerReply("I need the id."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename the coffee one"}).
		requireStatus(http.StatusOK).json()
	require.Empty(t, body["conversation"].(map[string]any)["actions"])
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "has to be an id")
}

func TestAChangeThatSetsNothingIsRefused(t *testing.T) {
	// A card that reads as a change and changes nothing is worse than no card.
	l := buildLedger(t)
	txn := someTransaction(l)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+`","summary":"tidy it"}`),
		answerReply("Nothing to change."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "tidy it up"}).requireStatus(http.StatusOK).json()
	require.Empty(t, body["conversation"].(map[string]any)["actions"])
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "would change nothing")
}

func TestSavingTheConnectionDoesNotSwitchChangesOnByItself(t *testing.T) {
	// Editing the model name must not be how somebody grants write access.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("fine"))
	allowWrites(l, model)

	body := l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "another-model",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["allow_writes"], "an omitted field leaves the setting alone")

	off := l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "another-model", "allow_writes": false,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, false, off["allow_writes"])
}

// Applying without being asked.
//
// The second switch, and the tests below are mostly about what does *not*
// change when it is on: the same request, through the same dispatcher, with
// the same bounds. What changes is when it goes out, and whether a card is
// waiting for somebody.

// applyWithoutAsking switches both changes and automatic application on, and
// returns a conversation id.
func applyWithoutAsking(l *ledger, model *fakeModel) string {
	l.t.Helper()
	l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model", "api_key": "sk-test-key",
		"allow_writes": true, "apply_without_asking": true,
	}).requireStatus(http.StatusOK)
	return l.alex.post("/assistant/conversations", nil).
		requireStatus(http.StatusCreated).json()["id"].(string)
}

func TestApplyingWithoutAskingIsOffEvenWhenChangesAreOn(t *testing.T) {
	// Two switches, and the second is not implied by the first. Somebody who
	// switches changes on gets cards, which is the whole point of that step.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("fine"))
	configure(l, model)

	body := l.alex.get("/assistant").requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["apply_without_asking"])

	allowWrites(l, model)
	body = l.alex.get("/assistant").requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["allow_writes"])
	require.Equal(t, false, body["apply_without_asking"])
}

func TestWithTheSettingOffAChangeStillWaitsForSomebody(t *testing.T) {
	// With the setting off, a proposed change still waits for somebody: a
	// card, a pending row, and a ledger nobody has touched.
	l := buildLedger(t)
	txn := someTransaction(l)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Rename it","payee":"Corner Store"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusOK).json()
	actions := body["conversation"].(map[string]any)["actions"].([]any)
	require.Len(t, actions, 1)
	require.Equal(t, "pending", actions[0].(map[string]any)["status"])

	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.NotEqual(t, "Corner Store", after["payee"])
}

func TestWithTheSettingOnAChangeIsMadeAtOnceAndNothingIsWaiting(t *testing.T) {
	l := buildLedger(t)
	txn := someTransaction(l)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Rename it","payee":"Corner Store"}`),
		answerReply("I renamed it to Corner Store."))
	id := applyWithoutAsking(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusOK).json()

	// The ledger, during the same request that asked.
	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.Equal(t, "Corner Store", after["payee"])

	// And the record of it, settled rather than pending, so nothing is waiting
	// for anybody and the card cannot be applied a second time.
	actions := body["conversation"].(map[string]any)["actions"].([]any)
	require.Len(t, actions, 1)
	action := actions[0].(map[string]any)
	require.Equal(t, "applied", action["status"])
	require.EqualValues(t, http.StatusOK, action["status_code"])
	require.Equal(t, "PATCH", action["method"])
	require.Equal(t, "/transactions/"+txn, action["path"])

	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusConflict)
}

func TestWithTheSettingOnTheModelIsToldItWorked(t *testing.T) {
	// The mirror of the proposing mode, and the inversion has to be complete:
	// there the model must not claim a change it did not make, here it must
	// not describe one it did make as a suggestion.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Vacation","summary":"Add a Vacation tag"}`),
		answerReply("Added it."))
	id := applyWithoutAsking(l, model)
	l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "add a vacation tag"}).requireStatus(http.StatusOK)

	encoded, err := json.Marshal(model.seen[1]["messages"])
	require.NoError(t, err)
	require.Contains(t, string(encoded), "This change has been made")
	require.NotContains(t, string(encoded), "Nothing has changed yet")

	// And the prompt it was given says to report it in the past tense.
	system := model.seen[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	require.Contains(t, system, "takes effect immediately")
	require.Contains(t, system, "in the past tense")
}

func TestARefusedChangeIsReportedAsRefusedRatherThanRetried(t *testing.T) {
	// Nothing absorbs a bad change here, so the model has to be told plainly
	// that it was refused and told to stop, or it loops on variations of the
	// rejected edit.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("change_endpoint",
			`{"method":"POST","path":"/tags","body":{"name":""},"summary":"Add a nameless tag"}`),
		answerReply("That was refused."))
	id := applyWithoutAsking(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "add a tag"}).requireStatus(http.StatusOK).json()

	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "failed", action["status"])

	encoded, err := json.Marshal(model.seen[1]["messages"])
	require.NoError(t, err)
	require.Contains(t, string(encoded), "do not try another way round it")
}

func TestAViewerCannotChangeAnythingHoweverTheSettingIsSet(t *testing.T) {
	// The ask endpoint is registered with Write, so a viewer is refused before
	// a tool runs — and the dispatcher would refuse them again behind it. The
	// setting moves when a change is issued and never who may issue one.
	l := buildLedger(t)
	txn := someTransaction(l)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Rename it","payee":"Corner Store"}`),
		answerReply("Renamed."))
	id := applyWithoutAsking(l, model)

	l.as("vera").post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusForbidden)

	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.NotEqual(t, "Corner Store", after["payee"])

	// And a card an owner proposed is not a viewer's to apply either. An
	// omitted field leaves the setting alone, so turning automatic application
	// off has to be said.
	proposed := l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model",
		"allow_writes": true, "apply_without_asking": false,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, false, proposed["apply_without_asking"])

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusOK).json()
	card := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	l.as("vera").post("/assistant-actions/"+card["id"].(string)+"/apply", nil).
		requireStatus(http.StatusForbidden)
}

func TestSwitchingTheSettingOnDoesNotApplyWhatIsAlreadyWaiting(t *testing.T) {
	// A pending card was proposed under the rule that somebody would decide
	// it. Changing the rule afterwards must not decide it for them: the
	// setting governs changes asked for from now on, and reaches backwards to
	// nothing.
	l := buildLedger(t)
	txn := someTransaction(l)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Rename it","payee":"Corner Store"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusOK).json()
	card := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "pending", card["status"])

	l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model",
		"allow_writes": true, "apply_without_asking": true,
	}).requireStatus(http.StatusOK)

	// Still pending, and the ledger still untouched.
	still := l.alex.get("/assistant-actions/" + card["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "pending", still["status"])
	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.NotEqual(t, "Corner Store", after["payee"])

	// It is still somebody's to decide, and applying it still works.
	applied := l.alex.post("/assistant-actions/"+card["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"], "%v", applied["result"])
}

func TestSwitchingChangesOffClearsAutomaticApplication(t *testing.T) {
	// Otherwise a household that turned changes off months ago, and has
	// forgotten this was ever set, gets an assistant editing their ledger
	// unattended the moment they turn changes back on to try something.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("fine"))
	applyWithoutAsking(l, model)

	off := l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model", "allow_writes": false,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, false, off["allow_writes"])
	require.Equal(t, false, off["apply_without_asking"])

	// Turning changes back on gives cards again, not automatic application.
	back := l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model", "allow_writes": true,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, true, back["allow_writes"])
	require.Equal(t, false, back["apply_without_asking"])
}

func TestAutomaticApplicationStillCannotReachTheAssistantsOwnConfiguration(t *testing.T) {
	// Every bound the Apply button enforces is enforced here, because there is
	// one implementation of applying and both go through it.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("change_endpoint", `{"method":"DELETE","path":"/assistant/connection",`+
			`"summary":"Remove the provider"}`),
		answerReply("I cannot do that."))
	id := applyWithoutAsking(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "delete your own connection"}).
		requireStatus(http.StatusOK).json()
	require.Empty(t, body["conversation"].(map[string]any)["actions"])
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "not reachable from here")

	require.Equal(t, true, l.alex.get("/assistant").
		requireStatus(http.StatusOK).json()["configured"])
}

func TestAnotherHouseholdsRowIsUnreachableWithTheSettingOn(t *testing.T) {
	// The dispatcher hands the handler the caller's own resolved space, so a
	// path naming somebody else's transaction fails as it would in a browser —
	// and the failure is recorded rather than silently doing nothing.
	l := buildLedger(t)
	stranger := l.id("stranger_txn").String()
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+stranger+
			`","summary":"Rename it","payee":"Mine now"}`),
		answerReply("That was refused."))
	id := applyWithoutAsking(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "failed", action["status"])
	require.EqualValues(t, http.StatusNotFound, action["status_code"])
}

func TestAnUpdateWithEmptyFieldsChangesOnlyWhatItNames(t *testing.T) {
	// The model sent every field of update_transaction with nothing in it
	// beside the category it meant. Applied as sent, that blanks the payee
	// and strips the tags.
	action, err := buildAction("update_transaction", map[string]any{
		"transaction_id": "8f8b461a-3b56-41b7-9d50-6eaf9f22c514", "summary": "Electronics",
		"category_id": "25cf4ed0-3295-4833-8d95-a24bad9ee07d",
		"payee":       "", "notes": "", "tag_ids": []any{}, "is_reviewed": false,
	})
	require.NoError(t, err)
	body := action.Body
	require.Equal(t, "25cf4ed0-3295-4833-8d95-a24bad9ee07d", body["category_id"].(string))
	require.NotContains(t, body, "payee")
	require.NotContains(t, body, "notes")
	require.NotContains(t, body, "tag_ids")
	require.Equal(t, false, body["is_reviewed"], "a stated flag is a change")

	_, err = buildAction("update_transaction", map[string]any{
		"transaction_id": "8f8b461a-3b56-41b7-9d50-6eaf9f22c514", "summary": "nothing", "payee": "",
	})
	require.Error(t, err, "nothing named is nothing to propose")
}

// --- The person's own answer -------------------------------------------------
//
// A card asks one question — is this the right category — and takes three
// answers: yes, no, and "no, this one". These check the third,
// which applies the request with the household's category in it, keeps what
// the model asked for beside it, and records the disagreement.

func TestApplyingWithADifferentCategoryFilesItWhereThePersonSaid(t *testing.T) {
	l := buildLedger(t)
	txn := l.str("august_corner")
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Corner Store is Food","category_id":"`+l.str("food")+`"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "categorize it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	applied := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply",
		map[string]any{"category_id": l.str("groceries")}).requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"], "%v", applied["result"])

	// The row carries the person's category, and the card carries both: what
	// ran, and what was asked for.
	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.Equal(t, l.str("groceries"), after["category_id"])
	require.Equal(t, l.str("groceries"), applied["body"].(map[string]any)["category_id"])
	require.Equal(t, l.str("food"), applied["proposed_body"].(map[string]any)["category_id"])
}

func TestClearingTheCategoryOnACardLeavesTheRowUncategorized(t *testing.T) {
	// An empty choice is a choice: "no, leave this alone" is the answer to a
	// proposal for a row the household files under nothing on purpose. It must
	// not read as "no override given" and quietly apply the model's category.
	l := buildLedger(t)
	txn := l.str("august_corner")
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Corner Store is Groceries","category_id":"`+l.str("groceries")+`"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "categorize it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply",
		map[string]any{"category_id": ""}).requireStatus(http.StatusOK)

	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	require.Nil(t, after["category_id"])
}

func TestASplitIsAppliedWithTheCategoryChosenForEachPart(t *testing.T) {
	l := buildLedger(t)
	txn := l.str("august_corner")
	model := newFakeModel(t,
		toolReply("split_transaction", `{"transaction_id":"`+txn+
			`","summary":"Two items","splits":[`+
			`{"amount":"-10.00","category_id":"`+l.str("food")+`","memo":"Dog food"},`+
			`{"amount":"-15.00","category_id":"`+l.str("food")+`","memo":"Batteries"}]}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "split it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	applied := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply",
		map[string]any{"split_categories": []any{
			map[string]any{"index": 1, "category_id": l.str("groceries")},
		}}).requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"], "%v", applied["result"])

	after := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
	splits := after["splits"].([]any)
	require.Len(t, splits, 2)
	require.Equal(t, l.str("food"), splits[0].(map[string]any)["category_id"])
	require.Equal(t, l.str("groceries"), splits[1].(map[string]any)["category_id"])

	// The part nobody touched is not recorded as a disagreement.
	proposed := applied["proposed_body"].(map[string]any)["splits"].([]any)
	require.Equal(t, l.str("food"), proposed[1].(map[string]any)["category_id"])
}

func TestASplitThatDoesNotAddUpToTheTransactionIsRefused(t *testing.T) {
	// august_corner is -25.00; these parts are -24.00. Catching the mismatch
	// here, rather than at the real endpoint's generic conflict, names it.
	l := buildLedger(t)
	txn := l.str("august_corner")
	model := newFakeModel(t,
		toolReply("split_transaction", `{"transaction_id":"`+txn+
			`","summary":"Two items","splits":[`+
			`{"amount":"-10.00","category_id":"`+l.str("food")+`","memo":"Dog food"},`+
			`{"amount":"-14.00","category_id":"`+l.str("food")+`","memo":"Batteries"}]}`),
		answerReply("Fixed the amounts."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "split it"}).requireStatus(http.StatusOK).json()
	require.Empty(t, body["conversation"].(map[string]any)["actions"])
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "add up to")
}

func TestACategoryChosenForACardThatFilesNothingIsRefused(t *testing.T) {
	// The page and the stored proposal have drifted apart. Applying the
	// model's own choice and reporting success would apply the one thing the
	// person said no to.
	l := buildLedger(t)
	txn := l.str("august_corner")
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Rename it","payee":"Corner Shop"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "rename it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply",
		map[string]any{"split_categories": []any{
			map[string]any{"index": 0, "category_id": l.str("groceries")},
		}}).requireStatus(http.StatusBadRequest)

	// And the card is still there to be decided properly.
	still := l.alex.get("/assistant-actions/" + action["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "pending", still["status"])
}

func TestACategoryFromAnotherHouseholdIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	l := buildLedger(t)
	txn := l.str("august_corner")
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"Corner Store is Groceries","category_id":"`+l.str("groceries")+`"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "categorize it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)

	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply",
		map[string]any{"category_id": l.str("stranger_category")}).
		requireStatus(http.StatusConflict)

	still := l.alex.get("/assistant-actions/" + action["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "pending", still["status"])
	require.Nil(t, still["proposed_body"], "nothing was rewritten by a refused override")
}
