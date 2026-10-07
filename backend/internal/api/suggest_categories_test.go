package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Suggesting categories: what a run is told about the row's own category, and
// that agreeing with it is never put in front of anybody.

// suggestRow writes one row on the checking account for a payee nobody has
// seen, so the history settles nothing and the model is asked.
func suggestRow(l *ledger, payee string, category string, reviewed bool) string {
	l.t.Helper()
	body := map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-02", "amount": "-31.00",
		"payee": payee, "statement_name": "POS " + payee, "is_reviewed": reviewed,
	}
	if category != "" {
		body["category_id"] = category
	}
	return l.alex.post("/transactions", body).requireStatus(http.StatusCreated).json()["id"].(string)
}

func openingSeen(t *testing.T, model *fakeModel) string {
	t.Helper()
	require.NotEmpty(t, model.seen, "the model was not asked")
	return model.seen[0]["messages"].([]any)[1].(map[string]any)["content"].(string)
}

func TestTheRowsOwnCategoryIsWeighedByWhetherItWasReviewed(t *testing.T) {
	cases := map[string]struct {
		category string
		reviewed bool
		want     string
	}{
		"reviewed":   {category: "groceries", reviewed: true, want: domain.CategoryStandingReviewed},
		"unreviewed": {category: "groceries", reviewed: false, want: domain.CategoryStandingUnreviewed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			l := buildLedger(t)
			model := newFakeModel(t, answerReply("The category stands."))
			allowWrites(l, model)
			row := suggestRow(l, "Quillfeather Paper Co", l.str(tc.category), tc.reviewed)
			id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
				requireStatus(http.StatusCreated).json()["id"].(string)

			l.alex.post("/assistant-automations/"+id+"/run", map[string]any{"transaction_id": row}).
				requireStatus(http.StatusOK)
			opening := openingSeen(t, model)
			require.Contains(t, opening, `"category_standing"`)
			require.Contains(t, opening, tc.want)
		})
	}

	t.Run("uncategorized", func(t *testing.T) {
		l := buildLedger(t)
		model := newFakeModel(t, answerReply("Could not guess: nothing to go on."))
		allowWrites(l, model)
		row := suggestRow(l, "Quillfeather Paper Co", "", true)
		id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
			requireStatus(http.StatusCreated).json()["id"].(string)

		l.alex.post("/assistant-automations/"+id+"/run", map[string]any{"transaction_id": row}).
			requireStatus(http.StatusOK)
		require.NotContains(t, openingSeen(t, model), "category_standing",
			"a row with no category has nothing to weigh")
	})
}

func TestAnImportedReviewedRowsCategoryStandsToo(t *testing.T) {
	// A Simplifi export carries the reviewed flag a person set there: for the
	// row being decided that is somebody's decision, whatever the history
	// makes of such rows.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("The category stands."))
	allowWrites(l, model)
	row := suggestRow(l, "Quillfeather Paper Co", l.str("groceries"), true)
	_, err := l.env.DB.Pool().Exec(t.Context(),
		`UPDATE transactions SET source = $1 WHERE id = $2`, domain.SourceSimplifiImport, row)
	require.NoError(t, err)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	l.alex.post("/assistant-automations/"+id+"/run", map[string]any{"transaction_id": row}).
		requireStatus(http.StatusOK)
	opening := openingSeen(t, model)
	require.Contains(t, opening, domain.CategoryStandingReviewed)
	require.NotContains(t, opening, "reviewed_on_arrival")
}

func TestASuggestionOfTheCategoryARowHasIsNeverShown(t *testing.T) {
	l := buildLedger(t)
	row := suggestRow(l, "Quillfeather Paper Co", l.str("groceries"), false)
	model := newFakeModel(t,
		toolReply("update_transaction", fmt.Sprintf(
			`{"transaction_id":%q,"category_id":%q,"summary":"Groceries (confidence 0.70)"}`,
			row, l.str("groceries"))),
		answerReply("The category stands."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run", map[string]any{"transaction_id": row}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, float64(0), run["actions"])
	require.Empty(t, run["conversation"].(map[string]any)["actions"])
	require.Contains(t, model.seen[1]["messages"].([]any)[3].(map[string]any)["content"],
		"already this row's category")

	require.Empty(t, l.alex.get("/assistant-automations/pending").requireStatus(http.StatusOK).list())
	shown := l.alex.get("/transactions/" + row).requireStatus(http.StatusOK).json()
	require.Nil(t, shown["suggestion"])
	require.Equal(t, l.str("groceries"), shown["category_id"])
}

func TestADifferentCategoryIsStillSuggested(t *testing.T) {
	l := buildLedger(t)
	row := suggestRow(l, "Quillfeather Paper Co", l.str("groceries"), false)
	model := newFakeModel(t,
		toolReply("update_transaction", fmt.Sprintf(
			`{"transaction_id":%q,"category_id":%q,"summary":"Food (confidence 0.70)"}`,
			row, l.str("food"))),
		answerReply("Proposed Food."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run", map[string]any{"transaction_id": row}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), run["actions"])
	shown := l.alex.get("/transactions/" + row).requireStatus(http.StatusOK).json()
	require.Equal(t, l.str("food"), shown["suggestion"].(map[string]any)["category_id"])
}

func TestHistoryAloneDoesNotOverruleAReviewedCategory(t *testing.T) {
	// Corner Store has been Groceries eight times, a vote that files an
	// unreviewed row on its own. A row somebody reviewed under Food is the
	// model's to decide, told the category stands.
	for _, reviewed := range []bool{true, false} {
		t.Run(fmt.Sprintf("reviewed=%v", reviewed), func(t *testing.T) {
			l := buildLedger(t)
			model := newFakeModel(t, answerReply("The category stands."))
			allowWrites(l, model)
			seedHistory(l, "Corner Store", "CORNER STORE", l.str("groceries"), 8)
			id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
				"confidence_threshold": 0.85,
			})).requireStatus(http.StatusCreated).json()["id"].(string)
			row := l.alex.post("/transactions", map[string]any{
				"account_id": l.str("checking"), "date": "2026-09-04", "amount": "-12.50",
				"payee": "Corner Store", "statement_name": "CORNER STORE",
				"category_id": l.str("food"), "is_reviewed": reviewed,
			}).requireStatus(http.StatusCreated).json()["id"].(string)

			run := l.alex.post("/assistant-automations/"+id+"/run", map[string]any{"transaction_id": row}).
				requireStatus(http.StatusOK).json()
			if !reviewed {
				require.Equal(t, "agentifi", run["decided_by"], "an unreviewed category is outvoted")
				require.Equal(t, float64(1), run["actions"])
				require.Empty(t, model.seen)
				return
			}
			require.Equal(t, "model", run["decided_by"])
			require.Contains(t, openingSeen(t, model), domain.CategoryStandingReviewed)
			require.Equal(t, float64(0), run["actions"])
		})
	}
}

func TestFiringSaysWhatStandsInTheWayOfSuggestions(t *testing.T) {
	l := buildLedger(t)
	fire := func() map[string]any {
		return l.alex.post("/assistant-automations/fire", map[string]any{
			"transaction_ids": []string{l.str("august_groceries")}, "force": true,
		}).requireStatus(http.StatusConflict).json()
	}
	require.Equal(t, "assistant_unavailable", fire()["code"])

	model := newFakeModel(t, answerReply("ok"))
	configure(l, model)
	require.Equal(t, "changes_off", fire()["code"])

	allowWrites(l, model)
	queued := l.alex.post("/assistant-automations/fire", map[string]any{
		"transaction_ids": []string{l.str("august_groceries")}, "force": true,
	}).requireStatus(http.StatusAccepted).json()
	require.Equal(t, float64(1), queued["queued"], "the built-in is made on first use")

	builtIn := l.alex.get("/assistant-automations").requireStatus(http.StatusOK).list()
	require.Len(t, builtIn, 1)
	require.Equal(t, domain.AutomationTemplateCheckCategory, builtIn[0]["template_key"])
	require.Equal(t, domain.AutomationModePropose, builtIn[0]["mode"])
	l.alex.patch("/assistant-automations/"+builtIn[0]["id"].(string), map[string]any{"is_enabled": false}).
		requireStatus(http.StatusOK)
	require.Equal(t, "automation_off", fire()["code"])
}

func TestOnlyARestatedCategoryIsDropped(t *testing.T) {
	groceries, food := uuid.New(), uuid.New()
	row := store.Transaction{CategoryID: groceries}
	require.True(t, onlySetsCategory(row, map[string]any{"category_id": groceries.String()}))
	require.False(t, onlySetsCategory(row, map[string]any{"category_id": food.String()}))
	require.False(t, onlySetsCategory(row, map[string]any{
		"category_id": groceries.String(), "payee": "Quillfeather Paper Co",
	}), "a change to anything else is still a change")
	require.False(t, onlySetsCategory(store.Transaction{},
		map[string]any{"category_id": groceries.String()}), "an uncategorized row gains one")
	split := store.Transaction{CategoryID: groceries, Splits: []store.Split{{CategoryID: food}}}
	require.False(t, onlySetsCategory(split, map[string]any{"category_id": groceries.String()}))

	same, other := groceries, food
	shown := TransactionResponse{CategoryID: &same}
	require.True(t, restatesRowCategory(shown, &TransactionSuggestion{CategoryID: &same}))
	require.False(t, restatesRowCategory(shown, &TransactionSuggestion{CategoryID: &other}))
	require.False(t, restatesRowCategory(TransactionResponse{},
		&TransactionSuggestion{CategoryID: &same}))
	require.False(t, restatesRowCategory(shown, &TransactionSuggestion{
		CategoryID: &same, Splits: []TransactionSuggestionSplit{{CategoryID: &other}},
	}), "a split is a change even where one part keeps the category")
}
