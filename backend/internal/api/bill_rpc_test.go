package api

import (
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// BillService, BillSignInService and BillPaymentService over their own
// protocol. The REST URLs they used to answer are the bridge's, and
// bills_test.go and bills_agent_test.go still reach them.

func newBillConnectionRPC(c *client, req *agentifiv1.CreateBillConnectionRequest) *agentifiv1.BillConnection {
	c.t.Helper()
	if req.Biller == "" {
		req.Biller = string(domain.BillerSpectrum)
	}
	res, err := call[agentifiv1.CreateBillConnectionRequest, agentifiv1.CreateBillConnectionResponse](
		c, agentifiv1connect.BillServiceCreateBillConnectionProcedure, req)
	require.Nil(c.t, err)
	return res.GetConnection()
}

func TestAnotherSpacesBillConnectionIsNotFound(t *testing.T) {
	l := buildLedger(t)
	connection := newBillConnectionRPC(frozenClient(l, "alex"), &agentifiv1.CreateBillConnectionRequest{Label: "Main account"})

	bob := l.as("bob")
	bob.spaceID = l.str("other_space")
	for _, id := range []string{connection.GetId(), "not-an-id"} {
		_, err := call[agentifiv1.GetBillConnectionRequest, agentifiv1.GetBillConnectionResponse](
			bob, agentifiv1connect.BillServiceGetBillConnectionProcedure,
			&agentifiv1.GetBillConnectionRequest{ConnectionId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "Bill connection not found", err.Message())
	}
}

func TestAViewerIsRefusedABillWrite(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.CreateBillConnectionRequest, agentifiv1.CreateBillConnectionResponse](
		l.as("vera"), agentifiv1connect.BillServiceCreateBillConnectionProcedure,
		&agentifiv1.CreateBillConnectionRequest{Biller: string(domain.BillerErie), Label: "Main vehicle"})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
	require.Equal(t, int32(http.StatusForbidden), problemIn(t, err).GetStatus())

	_, err = call[agentifiv1.ListBillConnectionsRequest, agentifiv1.ListBillConnectionsResponse](
		l.as("vera"), agentifiv1connect.BillServiceListBillConnectionsProcedure,
		&agentifiv1.ListBillConnectionsRequest{})
	require.Nil(t, err)
}

func TestABillConnectionUpdateLeavesAloneWhatTheMaskDoesNotName(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection := newBillConnectionRPC(alex, &agentifiv1.CreateBillConnectionRequest{
		Label: "Main account", Username: "household", PullAt: proto.String("06:00"),
	})
	update := func(req *agentifiv1.UpdateBillConnectionRequest) *agentifiv1.BillConnection {
		t.Helper()
		req.ConnectionId = connection.GetId()
		res, err := call[agentifiv1.UpdateBillConnectionRequest, agentifiv1.UpdateBillConnectionResponse](
			alex, agentifiv1connect.BillServiceUpdateBillConnectionProcedure, req)
		require.Nil(t, err)
		return res.GetConnection()
	}

	// Set, and absent: the pull hour the mask does not name stays.
	changed := update(&agentifiv1.UpdateBillConnectionRequest{
		Username: proto.String("someone"), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"username"}},
	})
	require.Equal(t, "someone", changed.GetUsername())
	require.Equal(t, "06:00", changed.GetPullAt())

	// Cleared: named and unset.
	changed = update(&agentifiv1.UpdateBillConnectionRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"pull_at"}},
	})
	require.Nil(t, changed.PullAt)
	require.Equal(t, "someone", changed.GetUsername())

	// The autopay figure follows its rule.
	changed = update(&agentifiv1.UpdateBillConnectionRequest{
		AutopayRule: proto.String(string(domain.AutopayDaysBeforeDue)), AutopayDays: proto.Int32(3),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"autopay_rule", "autopay_days"}},
	})
	require.Equal(t, int32(3), changed.GetAutopayDays())
	require.Nil(t, changed.AutopayDay)

	// A label cannot be cleared, the 409 every patch answers.
	alex.rpc(agentifiv1connect.BillServiceUpdateBillConnectionProcedure, map[string]any{
		"connection_id": connection.GetId(), "update_mask": "label",
	}).requireCode(connect.CodeFailedPrecondition).requireStatus(http.StatusConflict)

	// An account id that is not an id is the request's fault, at its field.
	refused := alex.rpc(agentifiv1connect.BillServiceUpdateBillConnectionProcedure, map[string]any{
		"connection_id": connection.GetId(), "autopay_account_id": "checking", "update_mask": "autopayAccountId",
	}).requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
	detail := refused.json()["detail"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"body", "autopay_account_id"}, detail["loc"])
}

func TestABillSubaccountUpdateSetsLeavesAndClearsItsNumber(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection := newBillConnectionRPC(alex, &agentifiv1.CreateBillConnectionRequest{Label: "Main account"})
	created, err := call[agentifiv1.CreateBillSubaccountRequest, agentifiv1.CreateBillSubaccountResponse](
		alex, agentifiv1connect.BillServiceCreateBillSubaccountProcedure, &agentifiv1.CreateBillSubaccountRequest{
			ConnectionId: connection.GetId(), Label: "Internet", MaskedNumber: proto.String("••01"),
		})
	require.Nil(t, err)
	require.Equal(t, "Internet", created.GetSubaccount().GetExternalId(), "the label stands in for a key")
	update := func(req *agentifiv1.UpdateBillSubaccountRequest) *agentifiv1.BillSubaccount {
		t.Helper()
		req.SubaccountId = created.GetSubaccount().GetId()
		res, err := call[agentifiv1.UpdateBillSubaccountRequest, agentifiv1.UpdateBillSubaccountResponse](
			alex, agentifiv1connect.BillServiceUpdateBillSubaccountProcedure, req)
		require.Nil(t, err)
		return res.GetSubaccount()
	}

	renamed := update(&agentifiv1.UpdateBillSubaccountRequest{
		Label: proto.String("Fiber"), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"label"}},
	})
	require.Equal(t, "Fiber", renamed.GetLabel())
	require.Equal(t, "••01", renamed.GetMaskedNumber())

	cleared := update(&agentifiv1.UpdateBillSubaccountRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"masked_number"}},
	})
	require.Nil(t, cleared.MaskedNumber)
	require.Equal(t, "Fiber", cleared.GetLabel())
}

func TestABillCreatedAsAProcedureTakesItsStatementThroughDocuments(t *testing.T) {
	// The procedure carries no file. The statement follows through
	// POST /documents, linked to the bill as its statement, and the bill
	// names it from then on.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection := newBillConnectionRPC(alex, &agentifiv1.CreateBillConnectionRequest{Label: "Main account"})
	subaccount, err := call[agentifiv1.CreateBillSubaccountRequest, agentifiv1.CreateBillSubaccountResponse](
		alex, agentifiv1connect.BillServiceCreateBillSubaccountProcedure, &agentifiv1.CreateBillSubaccountRequest{
			ConnectionId: connection.GetId(), ExternalId: "line-1", Label: "Internet",
		})
	require.Nil(t, err)

	refused := alex.rpc(agentifiv1connect.BillServiceCreateBillProcedure, map[string]any{
		"connection_id": connection.GetId(), "amount_due": map[string]any{"amount": "80.00"},
	}).requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
	require.Equal(t, []any{"body", "due_on"}, refused.json()["detail"].([]any)[0].(map[string]any)["loc"])

	created, err := call[agentifiv1.CreateBillRequest, agentifiv1.CreateBillResponse](
		alex, agentifiv1connect.BillServiceCreateBillProcedure, &agentifiv1.CreateBillRequest{
			ConnectionId: connection.GetId(), DueOn: "2026-09-03", AmountDue: &agentifiv1.Money{Amount: "80.00"},
		})
	require.Nil(t, err)
	require.Equal(t, int32(1), created.GetNew())
	bill := created.GetBills()[0]
	require.Equal(t, "80.00", bill.GetAmountDue().GetAmount())
	require.Nil(t, bill.DocumentId)
	require.Nil(t, bill.MinimumDue)

	document := alex.upload("/documents", "file", "september.pdf", storetest.PDF(), map[string]string{
		"kind": "bill", "target_id": bill.GetId(), "role": "statement",
	}).requireStatus(http.StatusCreated).json()

	listed, err := call[agentifiv1.ListSubaccountBillsRequest, agentifiv1.ListSubaccountBillsResponse](
		alex, agentifiv1connect.BillServiceListSubaccountBillsProcedure,
		&agentifiv1.ListSubaccountBillsRequest{SubaccountId: subaccount.GetSubaccount().GetId()})
	require.Nil(t, err)
	require.Len(t, listed.GetBills(), 1)
	require.Equal(t, document["id"], listed.GetBills()[0].GetDocumentId())
}

func TestTheBillSignInMethodsAreThePersonsOwn(t *testing.T) {
	// dispatch.go's list and the proto agree (rpc_contract_test.go); this
	// holds the link the assistant hands on, and that the challenge listing
	// stays readable to it.
	service := (&agentifiv1.StartBillSignInRequest{}).ProtoReflect().Descriptor().ParentFile().
		Services().ByName("BillSignInService")
	require.NotNil(t, service)
	open := map[string]bool{"ListBillChallenges": true, "GetBillChallenge": true}
	for _, method := range methodsOf(service) {
		proc := procedures[procedureName(method)]
		if open[string(method.Name())] {
			require.Equal(t, agentifiv1.Dispatch_DISPATCH_ALLOWED, proc.dispatch, proc.name)
			continue
		}
		require.Equal(t, agentifiv1.Dispatch_DISPATCH_HUMAN_ONLY, proc.dispatch, proc.name)
		link := proto.GetExtension(method.Options(), agentifiv1.E_HumanLink).(string)
		require.True(t, strings.HasPrefix(link, "/settings/bills"), "%s links to %q", proc.name, link)
	}
}

func TestBillHistoryIsMatchedPerSelectedAccount(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection := newBillConnectionRPC(alex, &agentifiv1.CreateBillConnectionRequest{Label: "Main account"})
	for _, label := range []string{"Internet", "Phone"} {
		_, err := call[agentifiv1.CreateBillSubaccountRequest, agentifiv1.CreateBillSubaccountResponse](
			alex, agentifiv1connect.BillServiceCreateBillSubaccountProcedure,
			&agentifiv1.CreateBillSubaccountRequest{ConnectionId: connection.GetId(), Label: label})
		require.Nil(t, err)
	}
	matched, err := call[agentifiv1.MatchConnectionBillHistoryRequest, agentifiv1.MatchConnectionBillHistoryResponse](
		alex, agentifiv1connect.BillPaymentServiceMatchConnectionBillHistoryProcedure,
		&agentifiv1.MatchConnectionBillHistoryRequest{ConnectionId: connection.GetId()})
	require.Nil(t, err)
	require.Len(t, matched.GetAccounts(), 2)
	for _, account := range matched.GetAccounts() {
		require.Nil(t, account.SeriesId, "linked to no reminder, so nothing to match")
	}

	_, err = call[agentifiv1.MatchConnectionBillHistoryRequest, agentifiv1.MatchConnectionBillHistoryResponse](
		l.as("vera"), agentifiv1connect.BillPaymentServiceMatchConnectionBillHistoryProcedure,
		&agentifiv1.MatchConnectionBillHistoryRequest{ConnectionId: connection.GetId()})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}
