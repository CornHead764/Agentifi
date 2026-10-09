package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// A category named Uncategorized (an import can create one) is the absence of
// a category, so the assistant is never offered it and never proposes it.

func uncategorizedCategory(l *ledger) string {
	l.t.Helper()
	return l.alex.post("/categories", map[string]any{
		"name": "Uncategorized", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
}

func askAndRead(l *ledger, conversationID string) (string, []any) {
	l.t.Helper()
	body := l.alex.post("/assistant/conversations/"+conversationID+"/ask",
		map[string]any{"question": "file it"}).requireStatus(http.StatusOK).json()
	conversation := body["conversation"].(map[string]any)
	messages := conversation["messages"].([]any)
	return messages[1].(map[string]any)["content"].(string), conversation["actions"].([]any)
}

func TestTheModelIsNotOfferedUncategorizedAsACategory(t *testing.T) {
	l := buildLedger(t)
	uncategorized := uncategorizedCategory(l)
	model := newFakeModel(t, toolReply("list_categories", "{}"), answerReply("done"))
	id := configure(l, model)

	content, _ := askAndRead(l, id)
	require.NotContains(t, content, uncategorized)
	require.NotContains(t, content, `"Uncategorized"`)
	require.Contains(t, content, `"name"`)
}

func TestAProposalNamingUncategorizedByIDIsDropped(t *testing.T) {
	l := buildLedger(t)
	uncategorized := uncategorizedCategory(l)
	txn := someTransaction(l)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+txn+
			`","summary":"File it","category_id":"`+uncategorized+`"}`),
		answerReply("done"))
	id := allowWrites(l, model)

	content, actions := askAndRead(l, id)
	require.Contains(t, content, "not a category to file under")
	require.Empty(t, actions)
}

func TestASplitPartNamingUncategorizedIsDropped(t *testing.T) {
	l := buildLedger(t)
	uncategorized := uncategorizedCategory(l)
	amount := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()["amount"].(string)
	total, err := domain.FromString(amount)
	require.NoError(t, err)
	half, ok := total.DivInt(2)
	require.True(t, ok)
	model := newFakeModel(t,
		toolReply("split_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","summary":"Two items","splits":[`+
			`{"amount":"`+half.String()+`","category_id":"`+l.str("groceries")+`"},`+
			`{"amount":"`+total.Sub(half).String()+`","category_id":"`+uncategorized+`"}]}`),
		answerReply("done"))
	id := allowWrites(l, model)

	content, actions := askAndRead(l, id)
	require.Contains(t, content, "not a category to file under")
	require.Empty(t, actions)
}
