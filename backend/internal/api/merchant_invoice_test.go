package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// An order's invoice, shown on the bank row the order was matched to for as
// long as the match stands, and to nobody outside the household.

func TestTheRowAnOrderMatchedShowsItsInvoiceUntilItIsUnmatched(t *testing.T) {
	l := buildLedger(t)
	account := l.alex.post("/merchants/costco/accounts", map[string]any{"label": "Alex"}).
		requireStatus(http.StatusCreated).json()["id"].(string)
	warehouse := costcoRow(l, "2026-09-05", "-50.00", "COSTCO WHSE #0123 SPRINGFIELD ZZ")
	uploadTo(l.alex, "/merchants/costco/imports", "costco.json", costcoJSON,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	order := l.alex.get("/merchants/transactions/" + warehouse).requireStatus(http.StatusOK).
		json()["order"].(map[string]any)
	orderID := order["id"].(string)

	// Only a pull stores an order's invoice; this is what it stores.
	stored, _, err := l.env.documents().Store(t.Context(), store.SpaceIDOf(l.id("space")), service.DocumentUpload{
		Bytes: storetest.PDF(), Filename: "costco-receipt-21100123456789012345.pdf",
		Source: store.DocumentSourceMerchantPull, SourceRef: "costco:21100123456789012345",
		Link: store.DocumentLink{
			Kind: store.DocumentLinkMerchantOrder, TargetID: uuid.MustParse(orderID), Role: store.DocumentRoleInvoice,
		},
	})
	require.NoError(t, err)
	invoiceID := stored.ID.String()
	l.alex.upload("/documents", "file", "mine.pdf", storetest.PDF(),
		map[string]string{"kind": "merchant_order", "target_id": orderID}).
		requireStatus(http.StatusConflict)

	behind := l.alex.get("/documents?transaction_id=" + warehouse).requireStatus(http.StatusOK).list()
	require.Len(t, behind, 1)
	require.Equal(t, invoiceID, behind[0]["id"])
	require.Equal(t, "receipt", behind[0]["via"])
	receiptOf := behind[0]["receipt_of"].(map[string]any)
	require.Equal(t, "merchant_order", receiptOf["kind"])
	require.Equal(t, "Costco", receiptOf["name"])
	require.Equal(t, "21100123456789012345", receiptOf["order_number"])
	l.alex.get("/documents/" + invoiceID + "/content").requireStatus(http.StatusOK)

	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	bob.get("/documents?transaction_id=" + warehouse).requireStatus(http.StatusNotFound)
	bob.get("/documents/" + invoiceID + "/content").requireStatus(http.StatusNotFound)
	bob.del("/merchants/transactions/" + warehouse + "/orders/" + orderID).requireStatus(http.StatusNotFound)
	require.Len(t, l.alex.get("/documents?transaction_id="+warehouse).requireStatus(http.StatusOK).list(), 1)

	l.alex.del("/merchants/transactions/" + warehouse + "/orders/" + orderID).requireStatus(http.StatusNoContent)
	require.Empty(t, l.alex.get("/documents?transaction_id="+warehouse).requireStatus(http.StatusOK).list(),
		"the invoice leaves the row with the match")
	links, err := l.env.DB.ListDocumentLinks(t.Context(), store.SpaceIDOf(l.id("space")), stored.ID)
	require.NoError(t, err)
	require.Len(t, links, 1, "and stays on its order")
	require.Equal(t, store.DocumentLinkMerchantOrder, links[0].Kind)
}
