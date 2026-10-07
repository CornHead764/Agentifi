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

// A "Suggest categories" request of any size is one batch: queued at once,
// worked off a few runs at a time, followed by its id, and summarized as a
// comparison with the categories the rows already had.

// septemberWindow is the register query that matches only the rows
// suggestRow writes; the ledger's own rows are all earlier.
const septemberWindow = "?date_field=posted&from=2026-09-01&to=2026-09-30"

func TestARequestOverTheBulkSizeIsQueuedWholeBehindEveryOtherRun(t *testing.T) {
	l := buildLedger(t)
	allowWrites(l, newFakeModel(t, answerReply("ok")))

	ids := make([]string, 0, domain.BulkSuggestionRows+1)
	for i := range domain.BulkSuggestionRows + 1 {
		ids = append(ids, suggestRow(l, fmt.Sprintf("Payee %d", i), "", false))
	}
	fired := l.alex.post("/assistant-automations/fire", map[string]any{
		"transaction_ids": ids, "force": true,
	}).requireStatus(http.StatusAccepted).json()
	require.Equal(t, float64(domain.BulkSuggestionRows+1), fired["rows"])
	require.Equal(t, float64(domain.BulkSuggestionRows+1), fired["queued"])
	batch := fired["batch_id"].(string)

	// One row asked for afterwards is not held back by the whole batch.
	single := l.alex.post("/assistant-automations/fire", map[string]any{
		"transaction_ids": []string{l.str("august_groceries")}, "force": true,
	}).requireStatus(http.StatusAccepted).json()
	require.Equal(t, float64(1), single["queued"])
	// The queue is every space's; earlier tests' leftovers are claimed past.
	var claimed store.AutomationRun
	for claimed.SpaceID != store.SpaceIDOf(l.id("space")) {
		var err error
		claimed, err = l.env.DB.ClaimQueuedAutomationRun(t.Context())
		require.NoError(t, err)
	}
	require.Equal(t, l.id("august_groceries"), claimed.TransactionID)
	require.False(t, claimed.Bulk)

	progress := l.alex.get("/category-suggestion-batches/" + batch).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(domain.BulkSuggestionRows+1), progress["rows"])
	require.Equal(t, float64(domain.BulkSuggestionRows+1), progress["pending"])
	require.Equal(t, float64(0), progress["done"])
	require.Equal(t, false, progress["finished"])

	// The single row's batch is too small to follow; the bulk one is shown.
	latest := l.alex.get("/category-suggestion-batches/latest").requireStatus(http.StatusOK).json()
	require.Equal(t, batch, latest["batch"].(map[string]any)["id"])

	// Cancelling drops what has not started; nothing is left waiting.
	cancelled := l.alex.post("/category-suggestion-batches/"+batch+"/cancel", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, cancelled["cancelled"])
	require.Equal(t, true, cancelled["finished"])
	require.Equal(t, float64(domain.BulkSuggestionRows+1), cancelled["not_run"])
	require.Equal(t, float64(domain.BulkSuggestionRows+1), cancelled["done"])
	waiting := l.alex.get("/assistant-automations/runs?status=queued").requireStatus(http.StatusOK).list()
	require.Empty(t, waiting)

	l.alex.post("/category-suggestion-batches/"+batch+"/dismiss", nil).
		requireStatus(http.StatusNoContent)
	latest = l.alex.get("/category-suggestion-batches/latest").requireStatus(http.StatusOK).json()
	require.Nil(t, latest["batch"])

	// A viewer may follow a batch but not stop one.
	l.as("vera").get("/category-suggestion-batches/" + batch).requireStatus(http.StatusOK)
	l.as("vera").post("/category-suggestion-batches/"+batch+"/cancel", nil).
		requireStatus(http.StatusForbidden)
}

func TestABatchComparesTheCheckWithTheCategoriesTheRowsHad(t *testing.T) {
	// The stand-in model names Groceries for every row. A row already filed
	// under Groceries agrees, one filed under Food differs and gets a
	// suggestion, and one with no category is suggested one. Reviewed and
	// unreviewed rows are counted apart.
	l := buildLedger(t)
	groceries := l.str("groceries")
	model := newFakeModel(t, answerReply(fmt.Sprintf("Groceries (%s)\nConfidence: 0.9", groceries)))
	allowWrites(l, model)

	suggestRow(l, "Quillfeather Paper Co", groceries, true)
	suggestRow(l, "Marlowe Hardware", groceries, false)
	differs := suggestRow(l, "Tansy Florist", l.str("food"), false)
	suggestRow(l, "Brindle Bakery", "", false)

	fired := l.alex.post("/assistant-automations/fire"+septemberWindow, map[string]any{
		"all_matching": true, "force": true,
	}).requireStatus(http.StatusAccepted).json()
	require.Equal(t, float64(4), fired["rows"], "only the rows the query matches")
	batch := fired["batch_id"].(string)

	automations, err := NewAutomations(l.env)
	require.NoError(t, err)
	automations.Drain(t.Context())

	summary := l.alex.get("/category-suggestion-batches/" + batch).requireStatus(http.StatusOK).json()
	require.Equal(t, true, summary["finished"])
	require.Equal(t, float64(4), summary["done"])
	require.Equal(t, map[string]any{
		"agreed": float64(1), "differs": float64(0), "unsure": float64(0), "suggested": float64(0),
		"undetermined": float64(0), "skipped": float64(0), "failed": float64(0),
	}, summary["reviewed"])
	require.Equal(t, map[string]any{
		"agreed": float64(1), "differs": float64(1), "unsure": float64(0), "suggested": float64(1),
		"undetermined": float64(0), "skipped": float64(0), "failed": float64(0),
	}, summary["unreviewed"])

	// The disagreement surfaces as usual, as a suggestion on its row.
	row := l.alex.get("/transactions/" + differs).requireStatus(http.StatusOK).json()
	require.NotNil(t, row["suggestion"])
}

func TestAFireNamesRowsOrAsksForAllMatchingNotBoth(t *testing.T) {
	l := buildLedger(t)
	allowWrites(l, newFakeModel(t, answerReply("ok")))
	l.alex.post("/assistant-automations/fire"+septemberWindow, map[string]any{
		"all_matching": true, "transaction_ids": []string{l.str("august_groceries")},
	}).requireStatus(http.StatusBadRequest)

	// Nothing queued keeps no batch to follow.
	fired := l.alex.post("/assistant-automations/fire"+septemberWindow, map[string]any{
		"all_matching": true, "force": true,
	}).requireStatus(http.StatusAccepted).json()
	require.Equal(t, float64(0), fired["queued"])
	require.Nil(t, fired["batch_id"])
}

func TestAnotherSpacesBatchIsNotFound(t *testing.T) {
	l := buildLedger(t)
	l.alex.get("/category-suggestion-batches/" + uuid.NewString()).requireStatus(http.StatusNotFound)
}
