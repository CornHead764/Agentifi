package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The assistant as an agent: it proposes, somebody accepts, and it hears back.
//
// These are the lifecycle a card goes through from the model's tool call to
// the ledger and back to the model — names resolved on the server, an Accept
// that runs once however it is pressed, a refusal that reaches the card and
// the model, and a card that can depend on another card proposed beside it.

// addAccount seeds one more account in the household, by an invented name.
func addAccount(l *ledger, name string) string {
	l.t.Helper()
	account := &store.Account{
		Name: name, Kind: domain.KindCreditCard, Type: "credit_card", Currency: "USD",
		IncludeInNetWorth: true,
	}
	require.NoError(l.t, l.env.DB.CreateAccount(l.t.Context(), store.SpaceIDOf(l.id("space")), account))
	return account.ID.String()
}

// ask puts one question and returns the conversation as it stands afterwards.
func ask(l *ledger, conversation, question string) map[string]any {
	l.t.Helper()
	return l.alex.post("/assistant/conversations/"+conversation+"/ask",
		map[string]any{"question": question}).requireStatus(http.StatusOK).
		json()["conversation"].(map[string]any)
}

func actionsOf(conversation map[string]any) []map[string]any {
	raw, _ := conversation["actions"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, one := range raw {
		out = append(out, one.(map[string]any))
	}
	return out
}

// toolResults is what each tool call answered, in order.
func toolResults(conversation map[string]any) []string {
	var out []string
	for _, raw := range conversation["messages"].([]any) {
		message := raw.(map[string]any)
		if message["role"] == "tool" {
			out = append(out, message["content"].(string))
		}
	}
	return out
}

func TestARuleIsProposedFromNamesAndCreatedWhenAccepted(t *testing.T) {
	// An example rule: anything on one card under $20 from Apple, filed as
	// groceries — said in words, not ids.
	l := buildLedger(t)
	card := addAccount(l, "Harbor Cash Back")
	model := newFakeModel(t,
		toolReply("create_rule", `{"name":"Apple under $20 is Groceries",
			"summary":"File small Apple charges on the Harbor card as groceries",
			"keywords":["APPLE"],"account":"my harbor card","amount_max":"20",
			"direction":"expense","set_category":"groceries"}`),
		answerReply("Here's the rule I'd make to accomplish that."))
	id := allowWrites(l, model)

	actions := actionsOf(ask(l, id, "a rule for Apple under $20 on my harbor card as groceries"))
	require.Len(t, actions, 1)
	action := actions[0]
	require.Equal(t, "pending", action["status"])
	require.Equal(t, "POST", action["method"])
	require.Equal(t, "/rules", action["path"])

	body := action["body"].(map[string]any)
	require.Equal(t, l.str("groceries"), body["actions"].(map[string]any)["set_category_id"])
	var sawAccount, sawAmount, sawKeyword bool
	for _, raw := range body["conditions"].([]any) {
		item := raw.(map[string]any)
		switch item["field"] {
		case "account":
			require.Equal(t, []any{card}, item["value_ids"])
			sawAccount = true
		case "amount":
			require.Equal(t, "less_than", item["operator"])
			require.Equal(t, "20.00", item["amount_max"])
			require.Equal(t, false, item["state"], "expense is state false")
			sawAmount = true
		case "statement_name":
			require.Equal(t, []any{"APPLE"}, item["value_texts"])
			sawKeyword = true
		}
	}
	require.True(t, sawAccount && sawAmount && sawKeyword, "conditions: %v", body["conditions"])

	// The card names what the ids are, so nobody approves a uuid.
	names := action["preview"].(map[string]any)["names"].(map[string]any)
	require.Equal(t, "Harbor Cash Back", names[card])
	require.Equal(t, "Groceries", names[l.str("groceries")])

	// Nothing exists until it is accepted.
	require.Empty(t, l.alex.get("/rules").requireStatus(http.StatusOK).list())

	applied := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"], "%v", applied["result"])
	resource, _ := applied["resource_id"].(string)
	require.NotEmpty(t, resource, "the card has to be able to link to the rule it made")

	rule := l.alex.get("/rules/" + resource).requireStatus(http.StatusOK).json()
	require.Equal(t, "Apple under $20 is Groceries", rule["name"])
	require.Len(t, rule["filter"].(map[string]any)["items"], 3)
}

func TestANameThatFitsTwoAccountsIsPutToThePersonNotGuessed(t *testing.T) {
	l := buildLedger(t)
	addAccount(l, "Harbor Cash Back")
	addAccount(l, "Harbor Travel")
	model := newFakeModel(t,
		toolReply("create_rule", `{"name":"x","summary":"x","keywords":["APPLE"],
			"account":"harbor card","set_category":"Groceries"}`),
		answerReply("Which Harbor card do you mean?"))
	id := allowWrites(l, model)

	conversation := ask(l, id, "rule for apple on my harbor card")
	require.Empty(t, actionsOf(conversation), "an ambiguous name must not become a card")
	results := toolResults(conversation)
	require.Len(t, results, 1)
	require.Contains(t, results[0], "Harbor Cash Back")
	require.Contains(t, results[0], "Harbor Travel")
	require.Contains(t, results[0], "Ask the person")
}

func TestANameThatFitsNothingSaysWhatThereIs(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transactions", `{"transaction_ids":["`+someTransaction(l)+`"],
			"summary":"x","category":"Pet Supplies"}`),
		answerReply("There is no such category."))
	id := allowWrites(l, model)

	conversation := ask(l, id, "file it under pet supplies")
	require.Empty(t, actionsOf(conversation))
	results := toolResults(conversation)
	require.Contains(t, results[0], `no category called \"Pet Supplies\"`)
	require.Contains(t, results[0], "Groceries", "the refusal lists what does exist")
}

func TestFindAnswersWithTheChoicesWhenANameIsAmbiguous(t *testing.T) {
	l := buildLedger(t)
	addAccount(l, "Harbor Cash Back")
	addAccount(l, "Harbor Travel")
	model := newFakeModel(t,
		toolReply("find", `{"kind":"account","name":"harbor"}`),
		answerReply("Which one?"))
	id := configure(l, model)

	results := toolResults(ask(l, id, "which harbor card"))
	var found map[string]any
	require.NoError(t, json.Unmarshal([]byte(results[0]), &found))
	require.Nil(t, found["match"])
	require.Len(t, found["choices"], 2)
}

func TestTwoAcceptsPressedTogetherRunTheRequestOnce(t *testing.T) {
	// A double-click arrives as two requests at once. The card is claimed
	// before its request is issued, so exactly one of them issues it.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Once only","summary":"Add a tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	action := actionsOf(ask(l, id, "add a tag"))[0]

	const presses = 8
	codes := make([]int, presses)
	var wg sync.WaitGroup
	for i := range presses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).Code
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, code := range codes {
		if code == http.StatusOK {
			ok++
		} else {
			require.Equal(t, http.StatusConflict, code)
		}
	}
	require.Equal(t, 1, ok, "codes: %v", codes)

	made := 0
	for _, tag := range l.alex.get("/tags").requireStatus(http.StatusOK).list() {
		if tag["name"] == "Once only" {
			made++
		}
	}
	require.Equal(t, 1, made)
}

func TestACardInterruptedMidApplyRecoversAsRetryable(t *testing.T) {
	// The process died between claiming the card and hearing back. A fresh
	// claim is left alone — the request may still be running — but one older
	// than the timeout is settled as failed, saying it may or may not have
	// landed, and can be tried again.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Interrupted","summary":"Add a tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	action := actionsOf(ask(l, id, "add a tag"))[0]
	actionID := uuid.MustParse(action["id"].(string))

	_, err := l.env.DB.ClaimAssistantAction(t.Context(), store.SpaceIDOf(l.id("space")), actionID)
	require.NoError(t, err)

	fresh := l.alex.get("/assistant/conversations/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "applying", actionsOf(fresh)[0]["status"])
	l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusConflict)

	later := time.Now().Add(10 * time.Minute)
	l.env.Now = func() time.Time { return later }

	stale := l.alex.get("/assistant/conversations/" + id).requireStatus(http.StatusOK).json()
	card := actionsOf(stale)[0]
	require.Equal(t, "failed", card["status"])
	require.Contains(t, card["result"], "interrupted")

	retried := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", retried["status"], "%v", retried["result"])
}

func TestAStuckCardIsReleasedWhenItIsPressedDirectly(t *testing.T) {
	// The same recovery reached through the card itself, without the thread
	// being read first — the Accept all path.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Stuck","summary":"Add a tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	action := actionsOf(ask(l, id, "add a tag"))[0]
	_, err := l.env.DB.ClaimAssistantAction(t.Context(), store.SpaceIDOf(l.id("space")),
		uuid.MustParse(action["id"].(string)))
	require.NoError(t, err)
	later := time.Now().Add(10 * time.Minute)
	l.env.Now = func() time.Time { return later }

	applied := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"], "%v", applied["result"])
}

func TestARefusedChangeSaysWhyAndCanBeRetried(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("change_endpoint",
			`{"method":"POST","path":"/tags","body":{"name":""},"summary":"Add a nameless tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	action := actionsOf(ask(l, id, "add a tag"))[0]

	failed := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "failed", failed["status"])
	require.NotEmpty(t, failed["result"])

	// A refused request changed nothing, so pressing again is a retry rather
	// than a conflict — and it is refused again, for the same reason.
	again := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "failed", again["status"])

	// And a failed card can be given up on.
	declined := l.alex.post("/assistant-actions/"+action["id"].(string)+"/decline",
		map[string]any{"reason": "never mind"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "discarded", declined["status"])
	require.Equal(t, "never mind", declined["decline_reason"])
}

func TestTheModelHearsWhatHappenedToItsCards(t *testing.T) {
	// Accepted, refused, declined with a reason: all three reach the model with
	// the person's next question, as the app speaking rather than the person.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Trips","summary":"Add a Trips tag"}`),
		toolReply("create_tag", `{"name":"Travel","summary":"Add a Travel tag"}`),
		answerReply("Proposed two."),
		answerReply("Understood."))
	id := allowWrites(l, model)
	actions := actionsOf(ask(l, id, "add a travel tag"))
	require.Len(t, actions, 2)

	l.alex.post("/assistant-actions/"+actions[0]["id"].(string)+"/decline",
		map[string]any{"reason": "I only want one tag, called Travel"}).
		requireStatus(http.StatusOK)
	applied := l.alex.post("/assistant-actions/"+actions[1]["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"])

	seenBefore := len(model.seen)
	ask(l, id, "thanks")
	messages := model.seen[seenBefore]["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	require.Equal(t, "user", last["role"])
	content := last["content"].(string)
	require.Contains(t, content, "not typed by the person")
	require.Contains(t, content, `declined "Add a Trips tag"`)
	require.Contains(t, content, "I only want one tag, called Travel")
	require.Contains(t, content, `accepted "Add a Travel tag"`)
	require.Contains(t, content, "Its id is "+applied["resource_id"].(string))
	require.True(t, strings.HasSuffix(content, "thanks"))

	// Once: the question after that carries no outcomes.
	seenBefore = len(model.seen)
	ask(l, id, "and again")
	messages = model.seen[seenBefore]["messages"].([]any)
	require.Equal(t, "and again", messages[len(messages)-1].(map[string]any)["content"])
}

func TestARuleCanNameACategoryProposedBesideIt(t *testing.T) {
	// "Make a Coffee category and a rule that files Blue Door under it": the
	// rule is proposed before the category exists, and reads its id when it
	// runs. Accept all applies them in the order they were proposed.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_category", `{"name":"Coffee","kind":"expense","summary":"Add Coffee"}`),
		toolReply("create_rule", `{"name":"Blue Door is Coffee","summary":"File Blue Door as Coffee",
			"keywords":["BLUE DOOR"],"set_category":"Coffee"}`),
		answerReply("Two cards."))
	id := allowWrites(l, model)
	actions := actionsOf(ask(l, id, "coffee category and a rule"))
	require.Len(t, actions, 2)
	category, rule := actions[0], actions[1]
	placeholder := rule["body"].(map[string]any)["actions"].(map[string]any)["set_category_id"]
	require.Equal(t, "@action:"+category["id"].(string), placeholder)

	// The rule alone cannot run yet, and says which card to accept first.
	early := l.alex.post("/assistant-actions/"+rule["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "failed", early["status"])
	require.Contains(t, early["result"], "Add Coffee")

	// Named out of order; applied in the order they were proposed.
	settled := l.alex.post("/assistant-actions/apply-many", map[string]any{
		"action_ids": []string{rule["id"].(string), category["id"].(string)},
	}).requireStatus(http.StatusOK).json()["actions"].([]any)
	require.Len(t, settled, 2)
	for _, raw := range settled {
		require.Equal(t, "applied", raw.(map[string]any)["status"], "%v", raw)
	}
	created := settled[0].(map[string]any)["resource_id"].(string)
	made := l.alex.get("/rules/" + settled[1].(map[string]any)["resource_id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, created, made["actions"].(map[string]any)["set_category_id"])
}

func TestABulkChangeIsOneGroupAcceptedInOneGo(t *testing.T) {
	l := buildLedger(t)
	rows := l.alex.get("/transactions?from=2000-01-01&to=2100-01-01").
		requireStatus(http.StatusOK).json()["items"].([]any)
	var ids []string
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["transfer_pair_id"] == nil && len(ids) < 2 {
			ids = append(ids, row["id"].(string))
		}
	}
	require.Len(t, ids, 2)
	encoded, _ := json.Marshal(ids)
	model := newFakeModel(t,
		toolReply("update_transactions", `{"transaction_ids":`+string(encoded)+`,
			"summary":"File both as groceries","category":"Groceries","add_tags":["reimbursable"]}`),
		answerReply("One card for both."))
	id := allowWrites(l, model)

	actions := actionsOf(ask(l, id, "file those as groceries"))
	require.Len(t, actions, 2)
	group := actions[0]["group_id"]
	require.NotNil(t, group)
	require.Equal(t, group, actions[1]["group_id"])
	require.Equal(t, "File both as groceries",
		actions[0]["preview"].(map[string]any)["group_summary"])

	l.alex.post("/assistant-actions/apply-many", map[string]any{
		"action_ids": []any{actions[0]["id"], actions[1]["id"]},
	}).requireStatus(http.StatusOK)
	// Pressing Accept all a second time applies nothing twice.
	again := l.alex.post("/assistant-actions/apply-many", map[string]any{
		"action_ids": []any{actions[0]["id"], actions[1]["id"]},
	}).requireStatus(http.StatusOK).json()["actions"].([]any)
	for _, raw := range again {
		require.Equal(t, "applied", raw.(map[string]any)["status"])
	}
	for _, txn := range ids {
		row := l.alex.get("/transactions/" + txn).requireStatus(http.StatusOK).json()
		require.Equal(t, l.str("groceries"), row["category_id"])
		require.Contains(t, row["tag_ids"], l.str("tag"))
	}
}

func TestAViewerCannotAcceptOrDeclineAnything(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Nope","summary":"Add a tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	action := actionsOf(ask(l, id, "add a tag"))[0]

	vera := l.as("vera")
	vera.post("/assistant-actions/apply-many",
		map[string]any{"action_ids": []any{action["id"]}}).requireStatus(http.StatusForbidden)
	vera.post("/assistant-actions/"+action["id"].(string)+"/decline", nil).
		requireStatus(http.StatusForbidden)

	still := l.alex.get("/assistant-actions/" + action["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "pending", still["status"])
}

func TestAnotherHouseholdCannotAcceptACardInABatch(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_tag", `{"name":"Private","summary":"Add a tag"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	action := actionsOf(ask(l, id, "add a tag"))[0]

	l.as("bob").post("/assistant-actions/apply-many",
		map[string]any{"action_ids": []any{action["id"]}}).requireStatus(http.StatusNotFound)
}

func TestUpdatingARulesAmountKeepsItsKeywords(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_rule", `{"name":"Cafe","summary":"x","keywords":["CAFE"],
			"set_category":"Groceries"}`),
		answerReply("Proposed."),
		toolReply("update_rule", `{"rule":"cafe","summary":"Only under $10","amount_max":"10"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	created := actionsOf(ask(l, id, "rule for cafe"))[0]
	l.alex.post("/assistant-actions/"+created["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK)

	actions := actionsOf(ask(l, id, "only under ten dollars"))
	update := actions[len(actions)-1]
	require.Equal(t, "PATCH", update["method"])
	fields := map[string]bool{}
	for _, raw := range update["body"].(map[string]any)["conditions"].([]any) {
		fields[raw.(map[string]any)["field"].(string)] = true
	}
	require.True(t, fields["statement_name"], "the keywords were dropped")
	require.True(t, fields["amount"])

	applied := l.alex.post("/assistant-actions/"+update["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"], "%v", applied["result"])
}

func TestAWatchlistsTargetCanBeChanged(t *testing.T) {
	l := buildLedger(t)
	made := l.alex.post("/watchlists", map[string]any{
		"name": "Groceries", "category_ids": []string{l.str("groceries")}, "target_amount": "300.00",
	}).requireStatus(http.StatusCreated).json()
	model := newFakeModel(t,
		toolReply("update_watchlist", `{"watchlist":"groceries","summary":"Lower it",
			"target_amount":"250"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	action := actionsOf(ask(l, id, "lower my grocery watchlist to 250"))[0]
	require.Equal(t, "/watchlists/"+made["id"].(string), action["path"])

	applied := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"], "%v", applied["result"])
	after := l.alex.get("/watchlists/" + made["id"].(string)).requireStatus(http.StatusOK).json()
	require.Equal(t, "250.00", after["target_amount"])
}

func TestAnAccountsExclusionsAreProposedByName(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_account", `{"account":"rewards card","summary":"Keep it out of the plan",
			"excluded_from_spending_plan":true}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	action := actionsOf(ask(l, id, "leave the rewards card out of the plan"))[0]
	require.Equal(t, "/accounts/"+l.str("card"), action["path"])
	require.Equal(t, map[string]any{"excluded_from_spending_plan": true}, action["body"])

	applied := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"], "%v", applied["result"])
	account := l.alex.get("/accounts/" + l.str("card")).requireStatus(http.StatusOK).json()
	require.Equal(t, true, account["excluded_from_spending_plan"])
	require.Equal(t, false, account["excluded_from_reports"], "the two exclusions are separate")
}

func TestAnAccountsHistoryStartIsProposedAndReturnedToAutomatic(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_account", `{"account":"rewards card","summary":"Start its history in March",
			"history_starts_on":"2026-03-01"}`),
		toolReply("update_account", `{"account":"rewards card","summary":"Back to automatic",
			"history_starts_on":"automatic"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	actions := actionsOf(ask(l, id, "start the card's history in March, then undo that"))
	require.Len(t, actions, 2)
	require.Equal(t, map[string]any{"history_starts_on": "2026-03-01"}, actions[0]["body"])
	require.Equal(t, map[string]any{"history_starts_on": nil}, actions[1]["body"])

	for i, want := range []any{"2026-03-01", nil} {
		applied := l.alex.post("/assistant-actions/"+actions[i]["id"].(string)+"/apply", nil).
			requireStatus(http.StatusOK).json()
		require.Equal(t, "applied", applied["status"], "%v", applied["result"])
		account := l.alex.get("/accounts/" + l.str("card")).requireStatus(http.StatusOK).json()
		require.Equal(t, want, account["history_starts_on"])
	}
}

func TestARecurringBillIsProposedAsMoneyOutWhateverTheSign(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("create_recurring", `{"kind":"bill","name":"Electric","matches":"CITY POWER",
			"account":"everyday checking","amount":"120","repeats":"monthly",
			"next_due_on":"2026-10-05","summary":"Expect the power bill monthly"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)
	action := actionsOf(ask(l, id, "add my power bill"))[0]
	require.Equal(t, "-120.00", action["body"].(map[string]any)["amount"])

	applied := l.alex.post("/assistant-actions/"+action["id"].(string)+"/apply", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "applied", applied["status"], "%v", applied["result"])
	_, err := uuid.Parse(applied["resource_id"].(string))
	require.NoError(t, err)
}
