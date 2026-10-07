package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Bills against the payments that settled them, over HTTP. Every figure is
// invented; the clock is seriesClock, 2026-08-22.

func TestARowThatPaidABillWithNoStatementStillNamesTheBill(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	series := newSeries(alex, l, map[string]any{
		"description": "SPECTRUM INTERNET",
		"start_on":    "2026-09-01",
	})
	connection := newBillConnection(alex, nil)
	subaccount := alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "line-1", "label": "Internet"}).
		requireStatus(http.StatusCreated).json()
	alex.post("/bills/links", map[string]any{
		"series_id": series["id"], "subaccount_id": subaccount["id"],
	}).requireStatus(http.StatusCreated)
	alex.post("/bills/connections/"+connection["id"].(string)+"/bills", map[string]any{
		"due_on": "2026-09-03", "amount_due": "80.00",
	}).requireStatus(http.StatusCreated)

	charge := alex.post("/occurrences/accept", map[string]any{
		"series_id": series["id"], "due_on": "2026-09-01",
	}).requireStatus(http.StatusCreated).json()

	require.Empty(t, alex.get("/documents?transaction_id="+charge["id"].(string)).
		requireStatus(http.StatusOK).list(), "no statement was filed, so no receipt")
	settled := alex.get("/bill-payments/transactions/" + charge["id"].(string)).
		requireStatus(http.StatusOK).list()
	require.Len(t, settled, 1)
	require.Equal(t, "2026-09-03", settled[0]["due_on"])
	require.Equal(t, "80.00", settled[0]["amount_due"])
	require.Equal(t, "open", settled[0]["status"])
	require.Nil(t, settled[0]["document_id"])
	require.Equal(t, connection["id"], settled[0]["connection_id"])
	require.Equal(t, "Spectrum (Main account)", settled[0]["provider"])

	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	bob.get("/bill-payments/transactions/" + charge["id"].(string)).requireStatus(http.StatusNotFound)
}

func TestMatchingAProvidersHistoryReportsEachBilledAccount(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	series := newSeries(alex, l, map[string]any{
		"description": "NORTHWIND LIFE",
		"start_on":    "2026-09-01",
	})
	connection := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerNorthwesternMutual), "label": "policy",
	})
	linked := alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "acct-1", "label": "Billing account"}).
		requireStatus(http.StatusCreated).json()
	alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "acct-2", "label": "Second account"}).
		requireStatus(http.StatusCreated)
	alex.post("/bills/links", map[string]any{
		"series_id": series["id"], "subaccount_id": linked["id"],
	}).requireStatus(http.StatusCreated)

	whole := alex.post("/bill-payments/connections/"+connection["id"].(string)+"/match", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, false, whole["statements_offered"], "this provider files no statement document")
	accounts := whole["accounts"].([]any)
	require.Len(t, accounts, 2)
	byLabel := map[string]map[string]any{}
	for _, raw := range accounts {
		one := raw.(map[string]any)
		byLabel[one["label"].(string)] = one
	}
	require.Equal(t, series["id"], byLabel["Billing account"]["series_id"])
	require.Nil(t, byLabel["Second account"]["series_id"])
	require.EqualValues(t, 0, byLabel["Billing account"]["matched"])

	one := alex.post("/bill-payments/subaccounts/"+linked["id"].(string)+"/match", nil).
		requireStatus(http.StatusOK).json()
	require.Len(t, one["accounts"].([]any), 1)

	vera := l.as("vera")
	vera.post("/bill-payments/connections/"+connection["id"].(string)+"/match", nil).
		requireStatus(http.StatusForbidden)
	vera.post("/bill-payments/subaccounts/"+linked["id"].(string)+"/match", nil).
		requireStatus(http.StatusForbidden)
}
