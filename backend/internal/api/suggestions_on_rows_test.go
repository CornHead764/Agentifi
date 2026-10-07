package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The suggestion, on the row it is about.
//
// Two halves of one change. A space that has an assistant it may write with
// gets the category check without setting it up, so rows arriving anywhere
// reach a person with something proposed; and what was proposed rides on the
// transaction, so the register can show it and decide it without anybody
// finding a second screen.

func TestArrivingRowsCreateTheCategoryCheckForASpaceThatNeverSetOneUp(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("ok"))
	allowWrites(l, model)
	require.Empty(t, l.alex.get("/assistant-automations").requireStatus(http.StatusOK).list(),
		"nothing has been created yet")

	automations, err := NewAutomations(l.env)
	require.NoError(t, err)
	space := store.SpaceIDOf(l.id("space"))
	queued, err := automations.EnqueueForTransactions(t.Context(), space,
		[]uuid.UUID{l.id("august_groceries")})
	require.NoError(t, err)
	require.Equal(t, 1, queued, "the row fired the automation that was just created")

	made := l.alex.get("/assistant-automations").requireStatus(http.StatusOK).list()
	require.Len(t, made, 1)
	require.Equal(t, domain.AutomationTemplateCheckCategory, made[0]["template_key"])
	require.Equal(t, domain.AutomationTriggerTransaction, made[0]["trigger"])
	require.Equal(t, true, made[0]["is_enabled"])
	// The household said the assistant may propose; nobody said it may act.
	require.Equal(t, domain.AutomationModePropose, made[0]["mode"],
		"a suggestion waits for a person unless the household chose otherwise")
	require.Equal(t, l.users["alex"].ID.String(), made[0]["created_by"], "runs act as an owner")

	// A second batch of rows does not create a second one.
	_, err = automations.EnqueueForTransactions(t.Context(), space,
		[]uuid.UUID{l.id("august_corner")})
	require.NoError(t, err)
	require.Len(t, l.alex.get("/assistant-automations").requireStatus(http.StatusOK).list(), 1)
}

func TestNoAssistantMeansNoAutomationIsCreated(t *testing.T) {
	// Nothing about this household has been pointed at a model, and firing
	// rows at them must leave the space exactly as it was.
	l := buildLedger(t)
	automations, err := NewAutomations(l.env)
	require.NoError(t, err)
	queued, err := automations.EnqueueForTransactions(t.Context(),
		store.SpaceIDOf(l.id("space")), []uuid.UUID{l.id("august_groceries")})
	require.NoError(t, err)
	require.Equal(t, 0, queued)
	require.Empty(t, l.alex.get("/assistant-automations").requireStatus(http.StatusOK).list())

	// Configured but reads-only is the same answer: proposing is switched off,
	// so an automation that proposes would only fail at four in the morning.
	model := newFakeModel(t, answerReply("ok"))
	configure(l, model)
	_, err = automations.EnqueueForTransactions(t.Context(),
		store.SpaceIDOf(l.id("space")), []uuid.UUID{l.id("august_corner")})
	require.NoError(t, err)
	require.Empty(t, l.alex.get("/assistant-automations").requireStatus(http.StatusOK).list())
}

func TestASpaceThatAlreadyWatchesTransactionsIsLeftAlone(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("ok"))
	allowWrites(l, model)
	l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated)

	automations, err := NewAutomations(l.env)
	require.NoError(t, err)
	_, err = automations.EnqueueForTransactions(t.Context(),
		store.SpaceIDOf(l.id("space")), []uuid.UUID{l.id("august_groceries")})
	require.NoError(t, err)

	rows := l.alex.get("/assistant-automations").requireStatus(http.StatusOK).list()
	require.Len(t, rows, 1, "the household's own automation, not a duplicate beside it")
	require.Equal(t, "Suggest categories", rows[0]["name"])
}

func TestAnAutomationThatOnlyObservesDoesNotCountAsTheCategoryCheck(t *testing.T) {
	// "Flag an unusual transaction" watches arriving rows and proposes
	// nothing. A household with only that one still has no suggestions.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("ok"))
	allowWrites(l, model)
	l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"name": "flag unusual", "mode": domain.AutomationModeObserve,
		"tools": []string{"search_transactions"},
	})).requireStatus(http.StatusCreated)

	automations, err := NewAutomations(l.env)
	require.NoError(t, err)
	_, err = automations.EnqueueForTransactions(t.Context(),
		store.SpaceIDOf(l.id("space")), []uuid.UUID{l.id("august_groceries")})
	require.NoError(t, err)

	rows := l.alex.get("/assistant-automations").requireStatus(http.StatusOK).list()
	require.Len(t, rows, 2)
	keys := []string{rows[0]["template_key"].(string), rows[1]["template_key"].(string)}
	require.Contains(t, keys, domain.AutomationTemplateCheckCategory)
}

func TestAPendingSuggestionRidesOnTheRowItIsAbout(t *testing.T) {
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

	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	suggestion := row["suggestion"].(map[string]any)
	require.Equal(t, "update_transaction", suggestion["tool"])
	require.Equal(t, l.str("groceries"), suggestion["category_id"])
	require.Equal(t, "Corner Store is groceries", suggestion["summary"])
	require.Equal(t, run["id"], suggestion["run_id"], "the run, so the register can link to it")
	require.NotEmpty(t, suggestion["conversation_id"])
	require.NotEmpty(t, suggestion["action_id"])

	// And on the list, where the register reads it.
	listed := l.alex.get("/transactions?limit=200").requireStatus(http.StatusOK).json()
	found := 0
	for _, item := range listed["items"].([]any) {
		one := item.(map[string]any)
		if one["suggestion"] == nil {
			continue
		}
		found++
		require.Equal(t, l.str("august_corner"), one["id"])
	}
	require.Equal(t, 1, found, "only the row a change is waiting on carries one")

	// Applying it settles the card, so the row stops offering it — and the
	// same apply path still ticks the row reviewed.
	action := suggestion["action_id"].(string)
	l.alex.post("/assistant-actions/"+action+"/apply", nil).requireStatus(http.StatusOK)
	after := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.Nil(t, after["suggestion"])
	require.Equal(t, l.str("groceries"), after["category_id"])
	require.Equal(t, true, after["is_reviewed"])
}

func TestAChangeThatNamesNoCategoryIsNotASuggestion(t *testing.T) {
	// A rename is a card in its thread, but the register has one cell for a
	// suggestion and it shows a category: drawn there, this would have read
	// "Uncategorized" with a tick to approve it.
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","payee":"The Corner Store","summary":"Tidy the payee"}`),
		answerReply("Proposed a cleaner payee."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])

	pending := l.alex.get("/assistant-automations/pending").requireStatus(http.StatusOK).list()
	require.Len(t, pending, 1, "the card itself still waits in the queue")
	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.Nil(t, row["suggestion"], "but it is not a suggestion on the row")
}

func TestASplitPartWithoutACategoryIsRefused(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("split_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","splits":[{"amount":"-10.00","category_id":"`+l.str("groceries")+
			`"},{"amount":"-15.00"}],"summary":"Half groceries"}`),
		answerReply("Could not propose the split."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)

	require.Empty(t, l.alex.get("/assistant-automations/pending").requireStatus(http.StatusOK).list())
	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.Nil(t, row["suggestion"])
}

func TestASuggestedSplitRidesOnTheRowWithItsPartsNamed(t *testing.T) {
	l := buildLedger(t)
	amount := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()["amount"].(string)
	total, err := domain.FromString(amount)
	require.NoError(t, err)
	half, ok := total.DivInt(2)
	require.True(t, ok)
	rest := total.Sub(half)

	model := newFakeModel(t,
		toolReply("split_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","summary":"Two items","splits":[`+
			`{"amount":"`+half.String()+`","category_id":"`+l.str("groceries")+`","memo":"Milk"},`+
			`{"amount":"`+rest.String()+`","category_id":"`+l.str("groceries")+`","memo":"Bread"}]}`),
		answerReply("Proposed a split."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"tools": []string{"list_categories", "split_transaction"},
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)

	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	suggestion := row["suggestion"].(map[string]any)
	require.Equal(t, "split_transaction", suggestion["tool"])
	require.Nil(t, suggestion["category_id"], "a split files nothing on the row itself")
	splits := suggestion["splits"].([]any)
	require.Len(t, splits, 2)
	first := splits[0].(map[string]any)
	// Amounts are strings on the wire, like every other amount here.
	require.Equal(t, half.String(), first["amount"])
	require.Equal(t, "Milk", first["memo"])
	require.Equal(t, l.str("groceries"), first["category_id"])
}

func TestAnotherHouseholdsPendingCardNeverAppearsOnARow(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","category_id":"`+l.str("groceries")+`","summary":"Corner Store is groceries"}`),
		answerReply("Proposed."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).requireStatus(http.StatusOK)

	// Bob's household cannot even read the row; what matters is that the card
	// is scoped by space in the query that hangs it on one.
	l.as("bob").get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusNotFound)
}

// proposeGroceriesForCorner runs the category check on the corner-store row
// with the model proposing Groceries and whatever else extra adds to the
// arguments, and returns the card's id.
func proposeGroceriesForCorner(t *testing.T, l *ledger, extra string) string {
	t.Helper()
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+l.str("august_corner")+
			`","category_id":"`+l.str("groceries")+`"`+extra+`,"summary":"Corner Store is groceries"}`),
		answerReply("Proposed Groceries for Corner Store."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	return row["suggestion"].(map[string]any)["action_id"].(string)
}

func TestAcceptingAnAutomationsSuggestionMarksTheRowReviewedWhateverTheCardSaid(t *testing.T) {
	// The category check is told the tick is the person's act; a model that
	// sets the flag anyway must not leave an accepted row looking unread.
	l := buildLedger(t)
	action := proposeGroceriesForCorner(t, l, `,"is_reviewed":false`)

	l.alex.post("/assistant-actions/"+action+"/apply", nil).requireStatus(http.StatusOK)
	row := l.alex.get("/transactions/" + l.str("august_corner")).
		requireStatus(http.StatusOK).json()
	require.Equal(t, l.str("groceries"), row["category_id"])
	require.Equal(t, true, row["is_reviewed"])
	require.Nil(t, row["suggestion"])
}

func TestFilingTheRowByHandSettlesItsSuggestion(t *testing.T) {
	l := buildLedger(t)
	action := proposeGroceriesForCorner(t, l, "")

	row := l.alex.patch("/transactions/"+l.str("august_corner"),
		map[string]any{"category_id": l.str("food")}).requireStatus(http.StatusOK).json()
	require.Nil(t, row["suggestion"], "the register stops offering what the person overruled")
	require.Equal(t, l.str("food"), row["category_id"])

	card := l.alex.get("/assistant-actions/" + action).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AssistantActionDiscarded, card["status"])
	require.Empty(t, l.alex.get("/assistant-automations/pending").
		requireStatus(http.StatusOK).list(), "nothing is left waiting on the row")

	// The next check learns from it as it would from a change made at Accept.
	corrections, err := l.env.DB.ListAssistantCorrections(t.Context(),
		store.SpaceIDOf(l.id("space")), 10)
	require.NoError(t, err)
	require.Len(t, corrections, 1)
	require.Equal(t, l.id("groceries"), corrections[0].ProposedCategoryID)
	require.Equal(t, l.id("food"), corrections[0].ChosenCategoryID)
	require.Equal(t, "CORNER STORE", corrections[0].StatementName)
}

func TestAnEditThatLeavesTheCategoryAloneKeepsTheSuggestion(t *testing.T) {
	// The edit dialog sends the category with every save; a note typed on the
	// row is not an answer to what was proposed.
	l := buildLedger(t)
	action := proposeGroceriesForCorner(t, l, "")

	row := l.alex.patch("/transactions/"+l.str("august_corner"),
		map[string]any{"category_id": nil, "notes": "paid in coins"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, action, row["suggestion"].(map[string]any)["action_id"])
}
