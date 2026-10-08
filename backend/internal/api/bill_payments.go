package api

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Bills against the bank rows that paid them: matching a provider's whole
// history on request, and which bill a payment settled.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewBillPaymentServiceHandler(billPaymentService{env}, opts...)
	})
}

type billPaymentService struct{ env *Env }

func (s billPaymentService) MatchConnectionBillHistory(
	ctx context.Context, req *agentifiv1.MatchConnectionBillHistoryRequest,
) (*agentifiv1.MatchConnectionBillHistoryResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	rows, err := s.env.DB.ListBillSubaccounts(ctx, sp.ID(), connection.ID)
	if err != nil {
		return nil, err
	}
	shown := make([]store.BillSubaccount, 0, len(rows))
	for _, row := range rows {
		if row.IsSelected {
			shown = append(shown, row)
		}
	}
	offered, accounts, err := matchBillHistory(ctx, s.env, sp, connection, shown)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.MatchConnectionBillHistoryResponse{StatementsOffered: offered, Accounts: accounts}, nil
}

func (s billPaymentService) MatchSubaccountBillHistory(
	ctx context.Context, req *agentifiv1.MatchSubaccountBillHistoryRequest,
) (*agentifiv1.MatchSubaccountBillHistoryResponse, error) {
	sp := spaceFrom(ctx)
	subaccount, err := billSubaccountOf(ctx, s.env, sp, req.GetSubaccountId())
	if err != nil {
		return nil, err
	}
	connection, err := s.env.DB.GetBillConnection(ctx, sp.ID(), subaccount.ConnectionID)
	if err != nil {
		return nil, err
	}
	offered, accounts, err := matchBillHistory(ctx, s.env, sp, connection, []store.BillSubaccount{subaccount})
	if err != nil {
		return nil, err
	}
	return &agentifiv1.MatchSubaccountBillHistoryResponse{StatementsOffered: offered, Accounts: accounts}, nil
}

// matchBillHistory matches each subaccount's history, and says whether the
// provider's bills can carry a statement at all, so a tally with none filed
// can say why.
func matchBillHistory(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	connection store.BillConnection, subaccounts []store.BillSubaccount,
) (bool, []*agentifiv1.BillAccountHistory, error) {
	found, err := billsService(env).MatchHistory(ctx, sp.ID(), subaccounts)
	if err != nil {
		return false, nil, err
	}
	biller, _ := domain.BillerByID(connection.Biller)
	accounts := make([]*agentifiv1.BillAccountHistory, 0, len(found))
	for _, one := range found {
		accounts = append(accounts, &agentifiv1.BillAccountHistory{
			SubaccountId: one.Subaccount.ID.String(), Label: one.Subaccount.Label,
			SeriesId: billNullableID(one.SeriesID),
			Matched:  int32(one.Matched), Settled: int32(one.Settled),
			WithStatement: int32(one.WithStatement), Unsettled: int32(one.Unsettled),
			CadenceGapDays: int32(one.CadenceGapDays),
		})
	}
	return biller.HasDocuments || biller.Access == domain.AccessEmail, accounts, nil
}

// ListBillsSettledByTransaction answers which bill a payment settled, with or
// without a statement document behind it.
func (s billPaymentService) ListBillsSettledByTransaction(
	ctx context.Context, req *agentifiv1.ListBillsSettledByTransactionRequest,
) (*agentifiv1.ListBillsSettledByTransactionResponse, error) {
	sp := spaceFrom(ctx)
	txnID, err := idFrom(req.GetTransactionId(), "Transaction")
	if err != nil {
		return nil, err
	}
	// A deleted row settles nothing a person can see.
	txn, err := s.env.DB.GetTransaction(ctx, sp.ID(), txnID)
	if err != nil {
		return nil, notFoundAs(err, "Transaction")
	}
	if txn.IsDeleted {
		return nil, errNotFound("Transaction")
	}
	settled, err := s.env.DB.BillsSettledByTransaction(ctx, sp.ID(), txnID)
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListBillsSettledByTransactionResponse{Bills: make([]*agentifiv1.SettledBill, 0, len(settled))}
	for _, one := range settled {
		out.Bills = append(out.Bills, &agentifiv1.SettledBill{
			BillId: one.Bill.ID.String(), ConnectionId: one.Connection.ID.String(),
			Provider: one.Connection.Title(), DueOn: one.Bill.DueOn.String(),
			AmountDue: moneyProto(one.Bill.AmountDue), Status: string(one.Bill.Status),
			DocumentId: billNullableID(one.Bill.DocumentID),
		})
	}
	return out, nil
}
