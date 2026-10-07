package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The assistant, end to end against a stand-in model.
//
// The provider is an httptest server speaking chat-completions, so the tool
// loop is exercised for real — the model asks for a tool, the app runs it
// against the seeded ledger, and the answer comes back. What is being checked
// is the app's half: that a tool nobody wrote is refused, that the key never
// leaves, and that what was looked up is recorded.

// fakeModel is a chat-completions endpoint that replies with a script.
type fakeModel struct {
	server *httptest.Server
	// replies are returned in order; the last one repeats.
	replies []string
	seen    []map[string]any
	at      int
}

func newFakeModel(t *testing.T, replies ...string) *fakeModel {
	t.Helper()
	model := &fakeModel{replies: replies}
	model.server = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			body["_authorization"] = r.Header.Get("Authorization")
			model.seen = append(model.seen, body)

			reply := model.replies[len(model.replies)-1]
			if model.at < len(model.replies) {
				reply = model.replies[model.at]
			}
			model.at++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(reply))
		}))
	t.Cleanup(model.server.Close)
	return model
}

func answerReply(text string) string {
	body, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"content": text}}},
	})
	return string(body)
}

func toolReply(name, arguments string) string {
	body, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{
			"content": "",
			"tool_calls": []map[string]any{{
				"id": "call-1", "type": "function",
				"function": map[string]any{"name": name, "arguments": arguments},
			}},
		}}},
	})
	return string(body)
}

// configure points the space at a stand-in model, answering questions only,
// and returns a conversation id.
func configure(l *ledger, model *fakeModel) string {
	l.t.Helper()
	l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "test-model", "api_key": "sk-test-key",
		"allow_writes": false,
	}).requireStatus(http.StatusOK)
	return l.alex.post("/assistant/conversations", nil).
		requireStatus(http.StatusCreated).json()["id"].(string)
}

func TestTheAssistantIsDormantUntilAModelIsConfigured(t *testing.T) {
	// The same stance as SMTP and SimpleFIN: nothing is sent anywhere until
	// somebody sets up a provider.
	l := buildLedger(t)
	body := l.alex.get("/assistant").requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["configured"])
	require.NotEmpty(t, body["tools"], "what it may read is listed before it is switched on")
}

func TestAskingWithNoModelIsRefusedRatherThanSilent(t *testing.T) {
	l := buildLedger(t)
	id := l.alex.post("/assistant/conversations", nil).
		requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "what did we spend?"}).requireStatus(http.StatusConflict)
}

func TestTheApiKeyNeverComesBack(t *testing.T) {
	// It is a credential. `has_key` is what a settings screen needs and all it
	// should ever get.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("fine"))
	configure(l, model)

	body := l.alex.get("/assistant").requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["has_key"])
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "sk-test-key")
}

func TestAQuestionIsAnsweredAndBothTurnsAreKept(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("You spent $412.00 on groceries."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "How much on groceries?"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "You spent $412.00 on groceries.", body["answer"])

	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Len(t, messages, 2)
	require.Equal(t, "user", messages[0].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[1].(map[string]any)["role"])
}

func TestTheModelIsToldTheRulesAndTheDate(t *testing.T) {
	// Without the date it answers "this month" about whichever month it
	// imagines; without the rules it invents figures.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("fine"))
	id := configure(l, model)
	l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "hello"}).requireStatus(http.StatusOK)

	require.NotEmpty(t, model.seen)
	first := model.seen[0]
	system := first["messages"].([]any)[0].(map[string]any)["content"].(string)
	require.Contains(t, system, "Never state a figure you have not read from a tool")
	require.Contains(t, system, "Today is ")
	require.Equal(t, "Bearer sk-test-key", first["_authorization"])
	require.NotEmpty(t, first["tools"], "the catalogue is offered")
}

func TestTheModelIsToldTheSpacesCurrency(t *testing.T) {
	// Tool results carry bare amounts, so a model never told the currency
	// answers in whichever one it guesses.
	l := buildLedger(t)
	space, err := l.env.DB.GetSpace(t.Context(), store.SpaceIDOf(l.id("space")))
	require.NoError(t, err)
	space.PrimaryCurrency = "CAD"
	require.NoError(t, l.env.DB.UpdateSpace(t.Context(), &space))

	model := newFakeModel(t, answerReply("fine"))
	id := configure(l, model)
	l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "what is left this month?"}).requireStatus(http.StatusOK)

	require.NotEmpty(t, model.seen)
	system := model.seen[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	require.Contains(t, system, "This household's currency is CAD.")
	require.Contains(t, system, "State every amount in CAD with its $ sign")
}

func TestAToolCallIsRunAgainstTheLedgerAndRecorded(t *testing.T) {
	// The whole loop: the model asks, the app reads its own ledger, the answer
	// goes back, and what was looked up is kept as the audit trail.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("list_accounts", "{}"),
		answerReply("You have a few accounts."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "what accounts do I have?"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, []any{"list_accounts"}, body["tool_calls"])

	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Len(t, messages, 3)
	tool := messages[1].(map[string]any)
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "list_accounts", tool["tool_name"])
	require.Contains(t, tool["content"], "Everyday Checking",
		"the tool read the real ledger, not a description of one")

	// And the result was sent back for the model to answer from.
	second := model.seen[1]
	encoded, err := json.Marshal(second["messages"])
	require.NoError(t, err)
	require.Contains(t, string(encoded), "Everyday Checking")
}

func TestAToolNobodyWroteIsRefusedWithoutReachingAnything(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("delete_everything", "{}"),
		answerReply("I could not do that."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "delete it all"}).requireStatus(http.StatusOK).json()
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	tool := messages[1].(map[string]any)
	require.Contains(t, tool["content"], "there is no tool called")
}

func TestAToolCalledWithABadDateIsCorrectableRatherThanFatal(t *testing.T) {
	// A bad date is something the model can fix on the next round. Losing the
	// whole conversation to it would be worse.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("spending_by_category", `{"from":"last tuesday","to":"2026-08-31"}`),
		answerReply("I need a proper date range."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "spending?"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "I need a proper date range.", body["answer"])
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "must be a date")
}

func TestAModelThatRefusesIsReportedInItsOwnWords(t *testing.T) {
	// "the model refused (401)" tells somebody their key is wrong. "Internal
	// server error" tells them nothing and implies the fault is here.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("unused"))
	id := configure(l, model)
	model.server.Config.Handler = http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key"}}`))
		})

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "hello"}).requireStatus(http.StatusBadGateway).json()
	require.Contains(t, body["detail"], "401")
}

func TestAConversationIsOnePersonsNotTheSpaces(t *testing.T) {
	// Sharing a ledger is not the same as sharing your questions about it.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("fine"))
	id := configure(l, model)

	l.as("vera").get("/assistant/conversations/" + id).requireStatus(http.StatusNotFound)
	require.Empty(t, l.as("vera").get("/assistant/conversations").
		requireStatus(http.StatusOK).list())
}

func TestSavingTheConnectionAgainKeepsTheKey(t *testing.T) {
	// So a settings form can be saved without the key ever being sent to the
	// browser and returned.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("fine"))
	configure(l, model)

	body := l.alex.put("/assistant/connection", map[string]any{
		"base_url": model.server.URL, "model": "another-model",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["has_key"])
	require.Equal(t, "another-model", body["model"])
}

func TestABaseUrlThatIsNotOneIsRefused(t *testing.T) {
	l := buildLedger(t)
	l.alex.put("/assistant/connection", map[string]any{
		"base_url": "not-a-url", "model": "m",
	}).requireStatus(http.StatusBadRequest)
}

func TestOnlyTheOwnerOrAnAdminMaySetTheAssistantConnection(t *testing.T) {
	// The connection carries the key the server sends elsewhere, so repointing
	// it is the household's settings, not a member's.
	h := newEmptyHousehold(t)
	for _, role := range []store.Role{store.RoleMember, store.RoleViewer} {
		h.as(t, role).put("/assistant/connection", map[string]any{
			"base_url": "http://model.example.test", "model": "m",
		}).requireStatus(http.StatusForbidden)
	}

	h.as(t, store.RoleAdmin).put("/assistant/connection", map[string]any{
		"base_url": "http://model.example.test", "model": "m",
	}).requireStatus(http.StatusOK)

	for _, role := range []store.Role{store.RoleMember, store.RoleViewer} {
		h.as(t, role).del("/assistant/connection").requireStatus(http.StatusForbidden)
	}
	h.as(t, store.RoleOwner).del("/assistant/connection").requireStatus(http.StatusNoContent)
}

// The portfolio tool.
//
// Without this tool, the assistant can see an investment account's balance
// and not a single position in it, so a question about a holding has nothing
// to read and the system prompt forbids answering from memory. The
// Investments page's *Why* and *Summarize* are built on this tool; without it
// both would be a model guessing.

func TestTheAssistantCanReadThePortfolio(t *testing.T) {
	l := buildPortfolioLedger(t)
	model := newFakeModel(t,
		toolReply("portfolio_holdings", "{}"),
		answerReply("AAA is up on the day."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "how is the portfolio doing?"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, []any{"portfolio_holdings"}, body["tool_calls"])

	tool := body["conversation"].(map[string]any)["messages"].([]any)[1].(map[string]any)
	content := tool["content"].(string)
	require.Contains(t, content, "AAA")
	require.Contains(t, content, "1000.00", "the market value the table also shows")
	require.Contains(t, content, "400.00", "and the gain")
	require.Contains(t, content, "Brokerage", "named by account, not by an id")
}

func TestTheAssistantIsToldWhichFiguresAreUnknownRatherThanZero(t *testing.T) {
	// BBB has no lots and no prior close. Sending zeros would have the model
	// report a break-even position that had a flat day, and neither is true.
	l := buildPortfolioLedger(t)
	model := newFakeModel(t,
		toolReply("portfolio_holdings", `{"symbol":"bbb"}`),
		answerReply("BBB's basis was never recorded."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "what is BBB's gain?"}).
		requireStatus(http.StatusOK).json()

	tool := body["conversation"].(map[string]any)["messages"].([]any)[1].(map[string]any)
	var payload struct {
		Holdings []map[string]any `json:"holdings"`
	}
	require.NoError(t, json.Unmarshal([]byte(tool["content"].(string)), &payload))
	require.Len(t, payload.Holdings, 1, "the symbol argument narrowed it")
	require.Equal(t, "BBB", payload.Holdings[0]["symbol"])
	require.Nil(t, payload.Holdings[0]["cost_basis"])
	require.Nil(t, payload.Holdings[0]["total_gain"])
	require.Nil(t, payload.Holdings[0]["day_change"])
}

func TestAskingTheAssistantAboutASymbolNobodyHoldsSaysSo(t *testing.T) {
	// Correctable on the next round rather than fatal, like a bad date.
	l := buildPortfolioLedger(t)
	model := newFakeModel(t,
		toolReply("portfolio_holdings", `{"symbol":"NVDA"}`),
		answerReply("You do not hold NVDA."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "how is NVDA doing?"}).
		requireStatus(http.StatusOK).json()
	tool := body["conversation"].(map[string]any)["messages"].([]any)[1].(map[string]any)
	require.Contains(t, tool["content"], "no holding with the symbol")
}

// The rest of what it can read.
//
// The point of these is coverage of the *reach*, not of the figures: each of
// the named tools below is a thin trim over an endpoint whose own numbers are
// tested where that endpoint is. What is worth checking here is that the tool
// reaches the endpoint at all, that the escape hatches reach the ones nobody
// named, and that the reach stops where it is supposed to.

func TestTheAssistantCanReadEveryScreenItHasAToolFor(t *testing.T) {
	l := buildLedger(t)
	for _, call := range []struct{ tool, arguments string }{
		{"list_categories", "{}"},
		{"list_tags", "{}"},
		{"list_rules", "{}"},
		{"savings_goals", "{}"},
		{"watchlists", "{}"},
		{"recurring_series", "{}"},
		{"recent_alerts", "{}"},
		{"cash_flow", `{"from":"2026-08-01","to":"2026-09-30"}`},
		{"income_and_expense", `{"from":"2026-07-01","to":"2026-08-31"}`},
	} {
		model := newFakeModel(t, toolReply(call.tool, call.arguments), answerReply("done"))
		id := configure(l, model)
		body := l.alex.post("/assistant/conversations/"+id+"/ask",
			map[string]any{"question": "tell me"}).requireStatus(http.StatusOK).json()

		messages := body["conversation"].(map[string]any)["messages"].([]any)
		content := messages[1].(map[string]any)["content"].(string)
		require.NotContains(t, content, `"error"`, "%s answered with an error: %s",
			call.tool, content)
	}
}

func TestTheAssistantIsToldTheRemindersStillUnpaidAsWellAsTheOnesToCome(t *testing.T) {
	// Asked about September on seriesClock's day, it hears about the rent due on the
	// first of July and August that nobody paid, as the Reminders strip shows
	// them, and nothing the strip would not.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	newSeries(alex, l, map[string]any{
		"description": "HOMESTEAD RENTALS", "display_name": "Rent", "amount": "-640.00",
		"start_on": "2026-07-01",
	})
	newSeries(alex, l, map[string]any{
		"description": "WIDGETWORKS PAYROLL", "display_name": "Paycheck",
		"kind": string(domain.SeriesIncome), "amount": "910.00", "start_on": "2026-08-15",
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{15}},
	})

	model := newFakeModel(t,
		toolReply("upcoming_bills", `{"from":"2026-09-01","to":"2026-09-30"}`),
		answerReply("Rent is late twice."))
	id := configure(l, model)
	body := alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "what do I still owe?"}).requireStatus(http.StatusOK).json()
	tool := body["conversation"].(map[string]any)["messages"].([]any)[1].(map[string]any)

	var payload struct {
		Expected []struct {
			Name   string `json:"name"`
			DueOn  string `json:"due_on"`
			Amount string `json:"amount"`
			Status string `json:"status"`
		} `json:"expected"`
	}
	require.NoError(t, json.Unmarshal([]byte(tool["content"].(string)), &payload))
	got := []string{}
	for _, one := range payload.Expected {
		got = append(got, one.DueOn+" "+one.Name+" "+one.Amount+" "+one.Status)
	}
	require.Equal(t, []string{
		"2026-07-01 Rent -640.00 past_due",
		"2026-08-01 Rent -640.00 past_due",
		"2026-09-01 Rent -640.00 upcoming",
		"2026-09-15 Paycheck 910.00 upcoming",
	}, got, "a paycheck not yet arrived is never overdue, so the one on 08-15 is left out")
}

func TestTheEscapeHatchesReachWhatNobodyWroteAToolFor(t *testing.T) {
	// This covers the application rather than a fixed list of questions: the
	// endpoint list comes from the registry, and anything on it can be read.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("list_endpoints", `{"search":"transfers"}`),
		toolReply("read_endpoint", `{"path":"/transfers"}`),
		answerReply("You have no paired transfers."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "which of my transfers are paired?"}).
		requireStatus(http.StatusOK).json()
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "/transfers")
	require.Contains(t, messages[2].(map[string]any)["content"], `"path":"/transfers"`)
}

func TestReadingAnEndpointDoesNotReachAnotherHousehold(t *testing.T) {
	// The dispatcher hands the handler the caller's own resolved space, so a
	// path naming somebody else's row answers as it would in the browser.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("read_endpoint",
			`{"path":"/transactions/`+l.id("stranger_txn").String()+`"}`),
		answerReply("I could not find that."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "what is that?"}).requireStatus(http.StatusOK).json()
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "404")
}

func TestAPlaceholderPathIsRefusedRatherThanRequested(t *testing.T) {
	// "/goals/{goal_id}" copied out of the endpoint list unchanged. Refusing
	// it by name tells the model what to do; a 404 would not.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("read_endpoint", `{"path":"/goals/{goal_id}"}`),
		answerReply("I need a real id."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "read a goal"}).requireStatus(http.StatusOK).json()
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "placeholder")
}

func TestReadingIsGetOnlyHoweverThePathIsSpelled(t *testing.T) {
	// read_endpoint has no method argument, so the only way to change anything
	// is a proposal. This is the check that it stays that way: a DELETE path
	// handed to the read tool is still a GET, and GET /tags/{id} is a read.
	l := buildLedger(t)
	before := len(l.alex.get("/tags").requireStatus(http.StatusOK).list())

	model := newFakeModel(t,
		toolReply("read_endpoint", `{"path":"/tags/`+l.id("tag").String()+`"}`),
		answerReply("read"))
	id := configure(l, model)
	l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "read the tag"}).requireStatus(http.StatusOK)

	require.Len(t, l.alex.get("/tags").requireStatus(http.StatusOK).list(), before)
}

func TestSearchingTransactionsSaysWhenThePageWasCut(t *testing.T) {
	// A model that reads the length of a page as the answer to "how many" is
	// wrong by exactly the page size, and confidently. The whole window is
	// matched before the page is cut, so the count is the true one.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("search_transactions",
			`{"from":"2000-01-01","to":"2100-01-01","limit":1}`),
		toolReply("search_transactions",
			`{"from":"2000-01-01","to":"2100-01-01","unreviewed":true}`),
		answerReply("done"))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "how many?"}).requireStatus(http.StatusOK).json()
	messages := body["conversation"].(map[string]any)["messages"].([]any)

	var page struct {
		Count    *int `json:"count"`
		Returned int  `json:"returned"`
		HasMore  bool `json:"has_more"`
	}
	require.NoError(t, json.Unmarshal(
		[]byte(messages[1].(map[string]any)["content"].(string)), &page))
	require.Equal(t, 1, page.Returned)
	require.True(t, page.HasMore)
	require.NotNil(t, page.Count)
	require.Greater(t, *page.Count, page.Returned)

	var filtered struct {
		Count    *int `json:"count"`
		Returned int  `json:"returned"`
	}
	require.NoError(t, json.Unmarshal(
		[]byte(messages[2].(map[string]any)["content"].(string)), &filtered))
	require.NotNil(t, filtered.Count)
	require.Equal(t, filtered.Returned, *filtered.Count)
}

func TestAFileIsNotSomethingTheModelCanBeHanded(t *testing.T) {
	// Not every endpoint answers JSON. An attachment answers the bytes of a
	// scanned receipt; several hundred kilobytes of that in a context window
	// is the whole conversation gone and nothing gained.
	l := buildLedger(t)
	created := attachTo(l, l.str("august_groceries"), "receipt.png", pngBytes())

	model := newFakeModel(t,
		toolReply("read_endpoint",
			`{"path":"/documents/`+created["id"].(string)+`/content"}`),
		answerReply("I cannot read a file."))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "export it"}).requireStatus(http.StatusOK).json()
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	require.Contains(t, messages[1].(map[string]any)["content"], "a file rather than a record")
}

// toolAnswer asks one tool through the stand-in model and decodes what it
// returned.
func toolAnswer(l *ledger, name, arguments string) map[string]any {
	l.t.Helper()
	model := newFakeModel(l.t, toolReply(name, arguments), answerReply("done"))
	id := configure(l, model)
	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "?"}).requireStatus(http.StatusOK).json()
	messages := body["conversation"].(map[string]any)["messages"].([]any)
	var out map[string]any
	require.NoError(l.t, json.Unmarshal([]byte(messages[1].(map[string]any)["content"].(string)), &out))
	return out
}

func TestTheAssistantsSpendingByCategoryNetsARefund(t *testing.T) {
	// August: Safeway -50.00 and a 20.00 return under Groceries, the corner
	// store's -25.00 uncategorized. The transfer is not spending, and the card
	// charge's effective date is in September.
	l := buildLedger(t)
	l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-12", "amount": "20.00",
		"payee": "Safeway", "category_id": l.str("groceries"),
	}).requireStatus(http.StatusCreated)

	out := toolAnswer(l, "spending_by_category", `{"from":"2026-08-01","to":"2026-08-31"}`)

	require.Equal(t, []any{
		map[string]any{"category": "Groceries", "spent": "30.00"},
		map[string]any{"category": "Uncategorized", "spent": "25.00"},
	}, out["categories"])
}

func TestTheAssistantsNetWorthIsTheNetWorthScreens(t *testing.T) {
	l := buildLedger(t)
	frozenOn(l, domain.NewDate(2026, time.September, 26))

	out := toolAnswer(l, "net_worth", `{}`)

	screen := l.alex.get("/net-worth?from=2026-09-26&to=2026-09-26").
		requireStatus(http.StatusOK).json()["end"].(map[string]any)
	require.Equal(t, screen["net"], out["net_worth"])
	require.Equal(t, screen["assets"], out["assets"])
	require.Equal(t, screen["debt"], out["debts"])
	require.Equal(t, "2026-09-26", out["as_of"])
}
