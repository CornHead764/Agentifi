package api

import (
	"fmt"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// DocumentService over its own protocol. Uploading and the bytes stay plain
// HTTP; documents_test.go covers them and the bridged listing.

func TestListingDocumentsNeedsATransaction(t *testing.T) {
	l := buildLedger(t)
	refused := l.alex.rpc(agentifiv1connect.DocumentServiceListDocumentsProcedure, `{}`).
		requireCode(connect.CodeInvalidArgument).
		requireStatus(http.StatusUnprocessableEntity)
	detail := refused.json()["detail"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"query", "transaction_id"}, detail["loc"])

	l.alex.rpc(agentifiv1connect.DocumentServiceListDocumentsProcedure,
		map[string]any{"transaction_id": "not-an-id"}).
		requireCode(connect.CodeInvalidArgument)
}

func TestADocumentReadsBackAndAnotherHouseholdsIsNotFound(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("august_groceries"), "receipt.png", pngBytes())
	id := fmt.Sprint(created["id"])

	read, err := call[agentifiv1.GetDocumentRequest, agentifiv1.GetDocumentResponse](
		l.alex, agentifiv1connect.DocumentServiceGetDocumentProcedure,
		&agentifiv1.GetDocumentRequest{DocumentId: id})
	require.Nil(t, err)
	require.Equal(t, "receipt.png", read.GetDocument().GetFilename())
	require.Equal(t, "/documents/"+id+"/content", read.GetDocument().GetUrl())
	require.Equal(t, l.users["alex"].ID.String(), read.GetDocument().GetUploadedByUserId())

	behind, err := call[agentifiv1.ListDocumentsRequest, agentifiv1.ListDocumentsResponse](
		l.alex, agentifiv1connect.DocumentServiceListDocumentsProcedure,
		&agentifiv1.ListDocumentsRequest{TransactionId: l.str("august_groceries")})
	require.Nil(t, err)
	require.Len(t, behind.GetDocuments(), 1)
	require.Equal(t, "transaction", behind.GetDocuments()[0].GetVia())
	require.Nil(t, behind.GetDocuments()[0].GetReceiptOf(), "an attachment is nobody's receipt")

	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	_, err = call[agentifiv1.GetDocumentRequest, agentifiv1.GetDocumentResponse](
		bob, agentifiv1connect.DocumentServiceGetDocumentProcedure,
		&agentifiv1.GetDocumentRequest{DocumentId: id})
	require.Equal(t, connect.CodeNotFound, err.Code())
	_, err = call[agentifiv1.ListDocumentsRequest, agentifiv1.ListDocumentsResponse](
		bob, agentifiv1connect.DocumentServiceListDocumentsProcedure,
		&agentifiv1.ListDocumentsRequest{TransactionId: l.str("august_groceries")})
	require.Equal(t, connect.CodeNotFound, err.Code())
}
