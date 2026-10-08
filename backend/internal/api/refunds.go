package api

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Refund links: which charge a credit gives back, read in both directions from
// one transaction.
//
// domain.LedgerKind needs no link for the ordinary case, since a credit filed
// under a spending category already nets against it. A link is for the credit
// nothing else can place.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewRefundServiceHandler(refundService{env}, opts...)
	})
}

type refundService struct{ env *Env }

// refundView is the lookup tables a response needs, loaded once per request.
type refundView struct {
	accounts   map[uuid.UUID]string
	categories map[uuid.UUID]store.Category
}

func loadRefundView(ctx context.Context, env *Env, sp auth.SpaceContext) (refundView, error) {
	view := refundView{accounts: map[uuid.UUID]string{}, categories: map[uuid.UUID]store.Category{}}
	// Deleted and closed included: last year's charge may sit in an account
	// since closed.
	accounts, err := env.DB.ListAccounts(ctx, sp.ID(),
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return view, err
	}
	for _, one := range accounts {
		view.accounts[one.ID] = one.Name
	}
	categories, err := env.DB.ListCategories(ctx, sp.ID(), true)
	if err != nil {
		return view, err
	}
	for _, one := range categories {
		view.categories[one.ID] = one
	}
	return view, nil
}

func refundChargeProto(txn store.Transaction, view refundView) *agentifiv1.RefundCharge {
	out := &agentifiv1.RefundCharge{
		Id:            txn.ID.String(),
		AccountId:     txn.AccountID.String(),
		AccountName:   view.accounts[txn.AccountID],
		Date:          txn.Date.String(),
		Amount:        moneyProto(txn.Amount),
		Payee:         store.DomainTransaction(txn).DisplayPayee(),
		StatementName: txn.StatementName,
	}
	if category, known := view.categories[txn.CategoryID]; known {
		out.CategoryId, out.CategoryName = proto.String(category.ID.String()), proto.String(category.Name)
	}
	return out
}

// GetRefundLinks reports the links this row is either side of.
func (s refundService) GetRefundLinks(
	ctx context.Context, req *agentifiv1.GetRefundLinksRequest,
) (*agentifiv1.GetRefundLinksResponse, error) {
	id, err := idFrom(req.GetId(), "Transaction")
	if err != nil {
		return nil, err
	}
	links, err := refundLinks(ctx, s.env, spaceFrom(ctx), id)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetRefundLinksResponse{Links: links}, nil
}

func refundLinks(ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID) (*agentifiv1.RefundLinks, error) {
	posting, _, err := refundPosting(ctx, env, sp, id)
	if err != nil {
		return nil, err
	}
	links, err := env.DB.ListRefundLinksFor(ctx, sp.ID(), []uuid.UUID{id})
	if err != nil {
		return nil, err
	}

	out := &agentifiv1.RefundLinks{
		CanBeARefund: domain.CanBeARefund(posting),
		Refunds:      []*agentifiv1.RefundCharge{},
		RefundedBy:   []*agentifiv1.RefundCharge{},
	}
	if len(links) == 0 {
		return out, nil
	}

	view, err := loadRefundView(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	// A row that has gone is skipped: DeleteTransaction releases its links in
	// the same database transaction, so a dangling link is a delete in flight.
	for _, link := range links {
		other, side := link.ChargeTxnID, &out.Refunds
		if link.RefundTxnID != id {
			other, side = link.RefundTxnID, &out.RefundedBy
		}
		row, err := env.DB.GetTransaction(ctx, sp.ID(), other)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return nil, err
		}
		*side = append(*side, refundChargeProto(row, view))
	}
	return out, nil
}

// ListRefundCandidates offers the charges this credit might be giving back,
// ranked by domain.RankRefundCandidates. Without a search it loads the window
// before the credit; with one, every matching row over all history, since a
// typed payee says which row is meant.
func (s refundService) ListRefundCandidates(
	ctx context.Context, req *agentifiv1.ListRefundCandidatesRequest,
) (*agentifiv1.ListRefundCandidatesResponse, error) {
	sp := spaceFrom(ctx)
	id, err := idFrom(req.GetId(), "Transaction")
	if err != nil {
		return nil, err
	}
	refund, _, err := refundPosting(ctx, s.env, sp, id)
	if err != nil {
		return nil, err
	}
	if !domain.CanBeARefund(refund) {
		return nil, errConflict("That transaction is not a credit a refund link applies to")
	}
	limit, err := limitField("limit", req.Limit, 25, 1, 200)
	if err != nil {
		return nil, err
	}

	query := store.TransactionQuery{SearchText: req.GetQ()}
	within := 0
	if req.GetQ() == "" {
		on := refund.Txn.ReportingDate(domain.DateEffective)
		within = domain.RefundCandidateWindowDays
		query.From = on.AddDays(-within)
		query.To = on
		query.DateMode = domain.DateEffective
	}
	postings, rows, err := service.LoadPostings(ctx, s.env.DB, sp.ID(), query)
	if err != nil {
		return nil, err
	}

	ranked := domain.RankRefundCandidates(refund, postings, within)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	view, err := loadRefundView(ctx, s.env, sp)
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListRefundCandidatesResponse{Candidates: make([]*agentifiv1.RefundCharge, 0, len(ranked))}
	for _, one := range ranked {
		key, err := store.ParseID(one.Txn.ID)
		if err != nil {
			return nil, err
		}
		row, known := rows[key]
		if !known {
			continue
		}
		out.Candidates = append(out.Candidates, refundChargeProto(row, view))
	}
	return out, nil
}

func (s refundService) LinkRefund(
	ctx context.Context, req *agentifiv1.LinkRefundRequest,
) (*agentifiv1.LinkRefundResponse, error) {
	sp := spaceFrom(ctx)
	id, err := idFrom(req.GetId(), "Transaction")
	if err != nil {
		return nil, err
	}
	chargeID, err := uuidField(req.GetChargeTransactionId(), "body", "charge_transaction_id")
	if err != nil {
		return nil, err
	}
	if chargeID == uuid.Nil {
		return nil, errInvalid("required", []string{"charge_transaction_id"},
			"Name the charge this credit gives back")
	}

	// Both rows are read through the space-scoped getter first, so an id from
	// another household is a 404 and never reaches the insert.
	refund, _, err := refundPosting(ctx, s.env, sp, id)
	if err != nil {
		return nil, err
	}
	charge, _, err := refundPosting(ctx, s.env, sp, chargeID)
	if err != nil {
		return nil, err
	}
	if !domain.CanBeARefund(refund) {
		return nil, errConflict("That transaction is not a credit a refund link applies to")
	}
	if !domain.CanBeRefunded(charge) {
		return nil, errConflict("That transaction is not a charge a credit can give back")
	}
	if charge.Txn.Amount.Abs().LessThan(refund.Txn.Amount.Abs()) {
		return nil, errConflict("That charge is smaller than the credit, so it cannot be what was refunded")
	}

	if err := s.env.DB.LinkRefund(ctx, sp.ID(), id, chargeID); err != nil {
		if isNotFound(err) {
			return nil, errConflict("One of those transactions changed; reload and try again")
		}
		return nil, err
	}
	links, err := refundLinks(ctx, s.env, sp, id)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.LinkRefundResponse{Links: links}, nil
}

func (s refundService) UnlinkRefund(
	ctx context.Context, req *agentifiv1.UnlinkRefundRequest,
) (*agentifiv1.UnlinkRefundResponse, error) {
	sp := spaceFrom(ctx)
	id, err := idFrom(req.GetId(), "Transaction")
	if err != nil {
		return nil, err
	}
	chargeID, err := idFrom(req.GetChargeId(), "Transaction")
	if err != nil {
		return nil, err
	}
	released, err := s.env.DB.UnlinkRefund(ctx, sp.ID(), id, chargeID)
	if err != nil {
		return nil, err
	}
	if released == 0 {
		return nil, errNotFound("Refund link")
	}
	return &agentifiv1.UnlinkRefundResponse{}, nil
}

// refundPosting resolves one transaction into the posting the predicates read,
// so account, category and amount come from the same row.
func refundPosting(
	ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID,
) (domain.Posting, store.Transaction, error) {
	row, err := env.DB.GetTransaction(ctx, sp.ID(), id)
	if err != nil {
		return domain.Posting{}, row, notFoundAs(err, "Transaction")
	}
	account, err := env.DB.GetAccount(ctx, sp.ID(), row.AccountID)
	if err != nil {
		return domain.Posting{}, row, err
	}
	posting := domain.Posting{
		Txn:     store.DomainTransaction(row),
		Account: store.DomainAccount(account),
	}
	if row.CategoryID != uuid.Nil {
		category, err := env.DB.GetCategory(ctx, sp.ID(), row.CategoryID)
		if err != nil {
			if !isNotFound(err) {
				return domain.Posting{}, row, err
			}
		} else {
			posting.Category, posting.HasCategory = store.DomainCategory(category), true
		}
	}
	return posting, row, nil
}
