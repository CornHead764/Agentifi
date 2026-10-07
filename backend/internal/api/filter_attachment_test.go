package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The has_attachment facet: a row with a file on it, attached by hand or filed
// as a receipt, which is what the register's paperclip counts.

func attachmentFilter(l *ledger, attached bool) string {
	l.t.Helper()
	return l.alex.post("/filters", map[string]any{
		"name":  "Attachments",
		"scope": "saved_view",
		"items": []map[string]any{
			{"field": "has_attachment", "operator": "is_true", "state": attached},
		},
	}).requireStatus(http.StatusCreated).json()["id"].(string)
}

// fileReceipt files a document on a row as a receipt, the link the bill and
// merchant paths write, rather than as a hand attachment.
func fileReceipt(l *ledger, txn string) {
	l.t.Helper()
	space := store.SpaceIDOf(l.id("space"))
	doc := &store.Document{
		ContentSHA256: "ab" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd",
		ContentType:   "application/pdf", SizeBytes: 64, Filename: "invoice.pdf",
		StorageKey: space.String() + "/invoice.pdf", Source: store.DocumentSourceUpload,
	}
	require.NoError(l.t, l.env.DB.CreateDocument(l.t.Context(), space, doc))
	require.NoError(l.t, l.env.DB.LinkDocument(l.t.Context(), space, store.DocumentLink{
		DocumentID: doc.ID, Kind: store.DocumentLinkReceipt, TargetID: l.id(txn),
	}))
}

func TestTheRegisterFiltersOnWhetherARowHasAFile(t *testing.T) {
	l := buildLedger(t)
	attachTo(l, l.str("july"), "receipt.png", pngBytes())
	fileReceipt(l, "august_groceries")
	// A split row is kept or dropped whole, by its own file.
	l.alex.put("/transactions/"+l.str("august_corner")+"/splits", map[string]any{
		"splits": []map[string]any{
			{"amount": "-10.00", "category_id": l.str("groceries")},
			{"amount": "-15.00"},
		},
	}).requireStatus(http.StatusOK)
	attachTo(l, l.str("august_corner"), "corner.png", pngBytes())

	with, without := attachmentFilter(l, true), attachmentFilter(l, false)

	page := registerPage(l, "filter_id="+with+"&limit=500")
	require.Equal(t, map[string]bool{
		l.str("july"): true, l.str("august_groceries"): true, l.str("august_corner"): true,
	}, idsIn(page))

	rest := idsIn(registerPage(l, "filter_id="+without+"&limit=500"))
	for _, name := range []string{"july", "august_groceries", "august_corner", "stranger_txn"} {
		require.NotContains(t, rest, l.str(name))
	}
	require.Contains(t, rest, l.str("card_charge"))

	august := registerPage(l, reportsAugust+"&filter_id="+with+"&limit=500")
	require.Equal(t, "-75.00", august["total"], "the corner row counts whole, not by one split")
	aggregate := aggregateOf(l, reportsAugust+"&filter_id="+with+"&direction=spending")
	require.Equal(t, "-75.00", aggregate["total"], "the chart sums what the list shows")
	require.EqualValues(t, 3, aggregate["count"], "both of the corner row's allocations")
}

func TestAReportRunsOverTheRowsWithAFile(t *testing.T) {
	l := buildLedger(t)
	fileReceipt(l, "august_groceries")
	result := l.alex.get("/reports/run?filter_id=" + attachmentFilter(l, true) +
		"&mode=transaction&rows=category&sign=expenses&" + reportsAugust).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "-50.00", result["transaction"].(map[string]any)["total"])
}

func TestAnAttachmentFilterRoundTrips(t *testing.T) {
	l := buildLedger(t)
	for _, state := range []bool{true, false} {
		filter := l.alex.get("/filters/" + attachmentFilter(l, state)).
			requireStatus(http.StatusOK).json()
		items := filter["items"].([]any)
		require.Len(t, items, 1)
		item := items[0].(map[string]any)
		require.Equal(t, "has_attachment", item["field"])
		require.Equal(t, "is_true", item["operator"])
		require.Equal(t, state, item["state"])
	}
}

func TestAWatchlistCountsTheRowsWithAFile(t *testing.T) {
	l := planLedger(t)
	fileReceipt(l, "august_groceries")
	card := newWatchlist(l, map[string]any{
		"name": "Receipted", "filter_id": attachmentFilter(l, true), "target_amount": "60.00",
	})
	require.Equal(t, "50.00", card["this_month_spent"])
}

func TestNothingThatRunsAsARowArrivesMatchesOnAnAttachment(t *testing.T) {
	// A rule, a note and a trigger all run before a file can be on the row.
	l := buildLedger(t)
	attached := []map[string]any{
		{"field": "has_attachment", "operator": "is_true", "state": true},
	}
	l.alex.post("/rules", map[string]any{
		"name": "Anything receipted", "conditions": attached,
		"actions": map[string]any{"set_is_reviewed": true},
	}).requireStatus(http.StatusUnprocessableEntity)
	l.alex.post("/guidance", fuelStopNote(map[string]any{"conditions": attached})).
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{"conditions": attached})).
		requireStatus(http.StatusUnprocessableEntity)
}
