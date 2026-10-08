package api

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
)

// The ledger's services over their own protocol: transactions, categories,
// filters, transfers, refunds, attachments and the unused purge. The REST URLs
// they used to answer are the bridge's, and the resource tests still reach
// them.

func mask(paths ...string) *fieldmaskpb.FieldMask { return &fieldmaskpb.FieldMask{Paths: paths} }

func TestTheRegisterEchoesItsWindowAndCarriesAmountsAsMoney(t *testing.T) {
	l := buildLedger(t)
	res, err := call[agentifiv1.ListTransactionsRequest, agentifiv1.ListTransactionsResponse](
		l.alex, agentifiv1connect.TransactionServiceListTransactionsProcedure,
		&agentifiv1.ListTransactionsRequest{From: "2026-08-01", To: "2026-08-31"})
	require.Nil(t, err)
	require.Equal(t, "2026-08-01", res.GetWindow().GetFrom())
	require.Equal(t, "2026-08-31", res.GetWindow().GetTo())
	require.Equal(t, "posted", res.GetWindow().GetDateField())
	require.Equal(t, int32(5), res.GetCount())
	// Groceries, the corner store, the card charge and both transfer legs.
	require.Equal(t, "-150.00", res.GetTotal().GetAmount())
	for _, item := range res.GetItems() {
		require.NotEqual(t, l.str("stranger_txn"), item.GetId())
		// Unset rather than empty: no filter kept part of this row.
		require.Nil(t, item.GetMatchedSplitIds())
	}
}

func TestAnEmptyAccountSelectionListsNothing(t *testing.T) {
	l := buildLedger(t)
	res, err := call[agentifiv1.ListTransactionsRequest, agentifiv1.ListTransactionsResponse](
		l.alex, agentifiv1connect.TransactionServiceListTransactionsProcedure,
		&agentifiv1.ListTransactionsRequest{AccountId: &agentifiv1.IdSet{}})
	require.Nil(t, err)
	require.Zero(t, res.GetCount())
}

func TestAnotherSpacesTransactionIsNotFound(t *testing.T) {
	l := buildLedger(t)
	for _, id := range []string{l.str("stranger_txn"), "not-an-id"} {
		_, err := call[agentifiv1.GetTransactionRequest, agentifiv1.GetTransactionResponse](
			l.alex, agentifiv1connect.TransactionServiceGetTransactionProcedure,
			&agentifiv1.GetTransactionRequest{TransactionId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "Transaction not found", err.Message())
	}
}

func TestATransactionPatchSetsLeavesAndClears(t *testing.T) {
	l := buildLedger(t)
	update := func(req *agentifiv1.UpdateTransactionRequest) *agentifiv1.Transaction {
		t.Helper()
		req.TransactionId = l.str("august_corner")
		res, err := call[agentifiv1.UpdateTransactionRequest, agentifiv1.UpdateTransactionResponse](
			l.alex, agentifiv1connect.TransactionServiceUpdateTransactionProcedure, req)
		require.Nil(t, err)
		return res.GetTransaction()
	}

	// Set.
	txn := update(&agentifiv1.UpdateTransactionRequest{
		Notes: proto.String("for the office"), TagIds: []string{l.str("tag")},
		UpdateMask: mask("notes", "tag_ids"),
	})
	require.Equal(t, "for the office", txn.GetNotes())
	require.Equal(t, []string{l.str("tag")}, txn.GetTagIds())

	// Absent: a rename leaves the note and the tags.
	txn = update(&agentifiv1.UpdateTransactionRequest{Payee: proto.String("Corner Market"), UpdateMask: mask("payee")})
	require.Equal(t, "Corner Market", txn.GetPayee())
	require.Equal(t, "for the office", txn.GetNotes())
	require.Equal(t, []string{l.str("tag")}, txn.GetTagIds())

	// Cleared: named in the mask and unset; a list named and empty.
	txn = update(&agentifiv1.UpdateTransactionRequest{UpdateMask: mask("notes", "tag_ids")})
	require.Nil(t, txn.Notes)
	require.Empty(t, txn.GetTagIds())
	require.Equal(t, "Corner Market", txn.GetPayee())
}

func TestClearingARequiredTransactionFieldIsAConflict(t *testing.T) {
	l := buildLedger(t)
	l.alex.rpc(agentifiv1connect.TransactionServiceUpdateTransactionProcedure, map[string]any{
		"transaction_id": l.str("august_corner"), "update_mask": "payee",
	}).requireCode(connect.CodeFailedPrecondition).requireStatus(http.StatusConflict)
}

func TestAViewerIsRefusedATransactionWrite(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.CreateTransactionRequest, agentifiv1.CreateTransactionResponse](
		l.as("vera"), agentifiv1connect.TransactionServiceCreateTransactionProcedure,
		&agentifiv1.CreateTransactionRequest{
			AccountId: l.str("checking"), Date: "2026-08-01",
			Amount: &agentifiv1.Money{Amount: "-10.00"},
		})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
	require.Equal(t, int32(http.StatusForbidden), problemIn(t, err).GetStatus())
}

func TestAMalformedAmountNamesItsField(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.CreateTransactionRequest, agentifiv1.CreateTransactionResponse](
		l.alex, agentifiv1connect.TransactionServiceCreateTransactionProcedure,
		&agentifiv1.CreateTransactionRequest{
			AccountId: l.str("checking"), Date: "2026-08-01",
			Amount: &agentifiv1.Money{Amount: "ten dollars"},
		})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"body", "amount"}, problemIn(t, err).GetFields()[0].GetLoc())
}

func TestACategoryPatchSetsLeavesAndClears(t *testing.T) {
	l := buildLedger(t)
	update := func(req *agentifiv1.UpdateCategoryRequest) *agentifiv1.Category {
		t.Helper()
		req.CategoryId = l.str("groceries")
		res, err := call[agentifiv1.UpdateCategoryRequest, agentifiv1.UpdateCategoryResponse](
			l.alex, agentifiv1connect.CategoryServiceUpdateCategoryProcedure, req)
		require.Nil(t, err)
		return res.GetCategory()
	}

	category := update(&agentifiv1.UpdateCategoryRequest{
		TxfId: proto.String("N100"), TxfIds: []string{"N100", "N101"}, UpdateMask: mask("txf_id", "txf_ids"),
	})
	require.Equal(t, "N100", category.GetTxfId())
	require.Equal(t, []string{"N100", "N101"}, category.GetTxfIds())

	category = update(&agentifiv1.UpdateCategoryRequest{Name: proto.String("Supermarket"), UpdateMask: mask("name")})
	require.Equal(t, "Supermarket", category.GetName())
	require.Equal(t, "N100", category.GetTxfId())
	require.Equal(t, l.str("food"), category.GetParentId())

	category = update(&agentifiv1.UpdateCategoryRequest{UpdateMask: mask("txf_id", "txf_ids", "parent_id")})
	require.Nil(t, category.TxfId)
	require.Empty(t, category.GetTxfIds())
	require.Nil(t, category.ParentId)
	require.Equal(t, "Supermarket", category.GetName())
}

func TestAnotherSpacesCategoryIsNotFound(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.GetCategoryRequest, agentifiv1.GetCategoryResponse](
		l.alex, agentifiv1connect.CategoryServiceGetCategoryProcedure,
		&agentifiv1.GetCategoryRequest{CategoryId: l.str("stranger_category")})
	require.Equal(t, connect.CodeNotFound, err.Code())
}

func TestSeedingTheDefaultsTwiceAddsNothingTheSecondTime(t *testing.T) {
	l := buildLedger(t)
	seed := func() *agentifiv1.SeedDefaultCategoriesResponse {
		t.Helper()
		res, err := call[agentifiv1.SeedDefaultCategoriesRequest, agentifiv1.SeedDefaultCategoriesResponse](
			l.alex, agentifiv1connect.CategoryServiceSeedDefaultCategoriesProcedure,
			&agentifiv1.SeedDefaultCategoriesRequest{})
		require.Nil(t, err)
		return res
	}
	first := seed()
	require.Positive(t, first.GetCreated())
	second := seed()
	require.Zero(t, second.GetCreated())
	require.Equal(t, len(first.GetCategories()), len(second.GetCategories()))
}

func TestAFiltersItemsAreReplacedOnlyWhenNamed(t *testing.T) {
	l := buildLedger(t)
	update := func(req *agentifiv1.UpdateFilterRequest) (*agentifiv1.Filter, *connect.Error) {
		t.Helper()
		req.FilterId = l.str("filter")
		res, err := call[agentifiv1.UpdateFilterRequest, agentifiv1.UpdateFilterResponse](
			l.alex, agentifiv1connect.FilterServiceUpdateFilterProcedure, req)
		return res.GetFilter(), err
	}

	filter, err := update(&agentifiv1.UpdateFilterRequest{Name: proto.String("Food"), UpdateMask: mask("name")})
	require.Nil(t, err)
	require.Equal(t, "Food", filter.GetName())
	require.Len(t, filter.GetItems(), 1)

	filter, err = update(&agentifiv1.UpdateFilterRequest{
		Items: []*agentifiv1.FilterItemWrite{{
			Field: "amount", Operator: "between",
			AmountMin: &agentifiv1.NullableMoney{Amount: "10.00"}, AmountMax: &agentifiv1.NullableMoney{Amount: "20.00"},
		}},
		UpdateMask: mask("items"),
	})
	require.Nil(t, err)
	require.Len(t, filter.GetItems(), 1)
	require.Equal(t, "10.00", filter.GetItems()[0].GetAmountMin().GetAmount())
	require.Nil(t, filter.GetItems()[0].DateFrom)

	// A watchlist that matched everything would claim the whole ledger.
	_, err = update(&agentifiv1.UpdateFilterRequest{UpdateMask: mask("items")})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"body", "items"}, problemIn(t, err).GetFields()[0].GetLoc())
}

func TestAnotherSpacesFilterIsNotFound(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.DeleteFilterRequest, agentifiv1.DeleteFilterResponse](
		l.alex, agentifiv1connect.FilterServiceDeleteFilterProcedure,
		&agentifiv1.DeleteFilterRequest{FilterId: l.str("stranger_filter")})
	require.Equal(t, connect.CodeNotFound, err.Code())
}

func TestTransfersAreListedOnThePostedDate(t *testing.T) {
	l := buildLedger(t)
	res, err := call[agentifiv1.ListTransfersRequest, agentifiv1.ListTransfersResponse](
		l.alex, agentifiv1connect.TransferServiceListTransfersProcedure,
		&agentifiv1.ListTransfersRequest{From: "2026-08-01", To: "2026-08-31"})
	require.Nil(t, err)
	require.Equal(t, "posted", res.GetWindow().GetDateField())
	require.Len(t, res.GetTransfers(), 1)
	transfer := res.GetTransfers()[0]
	require.Equal(t, "200.00", transfer.GetAmount().GetAmount())
	require.Equal(t, l.str("transfer_out"), transfer.GetFrom().GetTransactionId())
	require.Equal(t, l.str("transfer_in"), transfer.GetTo().GetTransactionId())
}

func TestAViewerIsRefusedUnpairingOverTheProcedure(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.UnpairTransferRequest, agentifiv1.UnpairTransferResponse](
		l.as("vera"), agentifiv1connect.TransferServiceUnpairTransferProcedure,
		&agentifiv1.UnpairTransferRequest{PairId: l.str("transfer_pair")})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}

func TestAnotherSpacesRowHasNoRefundLinks(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.GetRefundLinksRequest, agentifiv1.GetRefundLinksResponse](
		l.alex, agentifiv1connect.RefundServiceGetRefundLinksProcedure,
		&agentifiv1.GetRefundLinksRequest{Id: l.str("stranger_txn")})
	require.Equal(t, connect.CodeNotFound, err.Code())

	res, err := call[agentifiv1.GetRefundLinksRequest, agentifiv1.GetRefundLinksResponse](
		l.alex, agentifiv1connect.RefundServiceGetRefundLinksProcedure,
		&agentifiv1.GetRefundLinksRequest{Id: l.str("august_corner")})
	require.Nil(t, err)
	require.False(t, res.GetLinks().GetCanBeARefund())
}

func TestAnAttachmentNobodyHasIsNotFound(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.DeleteAttachmentRequest, agentifiv1.DeleteAttachmentResponse](
		l.alex, agentifiv1connect.AttachmentServiceDeleteAttachmentProcedure,
		&agentifiv1.DeleteAttachmentRequest{AttachmentId: l.str("august_corner")})
	require.Equal(t, connect.CodeNotFound, err.Code())
	require.Equal(t, "Attachment not found", err.Message())
}

func TestTheUnusedListAndAViewersPurge(t *testing.T) {
	l := buildLedger(t)
	res, err := call[agentifiv1.ListUnusedRequest, agentifiv1.ListUnusedResponse](
		l.alex, agentifiv1connect.UnusedServiceListUnusedProcedure, &agentifiv1.ListUnusedRequest{})
	require.Nil(t, err)
	require.NotEmpty(t, res.GetCategoriesChecked())
	require.Contains(t, res.GetCategoriesChecked(), "subcategories")

	_, err = call[agentifiv1.PurgeUnusedRequest, agentifiv1.PurgeUnusedResponse](
		l.as("vera"), agentifiv1connect.UnusedServicePurgeUnusedProcedure,
		&agentifiv1.PurgeUnusedRequest{TagIds: []string{l.str("tag")}})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}
