package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
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
	Register(Resource{Prefix: "/refunds", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/transactions/{id}", refundLinksForTransaction)
		rt.Read(http.MethodGet, "/transactions/{id}/candidates", refundCandidates)
		rt.Write(http.MethodPost, "/transactions/{id}/charges", linkRefund)
		rt.Write(http.MethodDelete, "/transactions/{id}/charges/{charge_id}", unlinkRefund)
	}})
}

// RefundChargeResponse is one transaction on either end of a link, carrying what
// a person needs to recognise it and nothing more.
type RefundChargeResponse struct {
	ID            uuid.UUID  `json:"id"`
	AccountID     uuid.UUID  `json:"account_id"`
	AccountName   string     `json:"account_name"`
	Date          Date       `json:"date"`
	Amount        string     `json:"amount"`
	Payee         string     `json:"payee"`
	StatementName string     `json:"statement_name"`
	CategoryID    *uuid.UUID `json:"category_id"`
	CategoryName  *string    `json:"category_name"`
}

// RefundLinksResponse answers both halves at once: Refunds is what this row
// gives back, RefundedBy is what gives this row back.
type RefundLinksResponse struct {
	// CanBeARefund says whether to offer the affordance. The server decides so
	// the domain rule has no second copy in the client.
	CanBeARefund bool                   `json:"can_be_a_refund"`
	Refunds      []RefundChargeResponse `json:"refunds"`
	RefundedBy   []RefundChargeResponse `json:"refunded_by"`
	// RefundState is how much of this row RefundedBy gives back: "full",
	// "partial", or "" (domain.RefundState).
	RefundState string `json:"refund_state"`
}

// RefundCandidateListResponse is the charges offered for one credit, likeliest
// first.
type RefundCandidateListResponse struct {
	Candidates []RefundChargeResponse `json:"candidates"`
}

// RefundLinkRequest names the charge the credit gives back.
type RefundLinkRequest struct {
	ChargeTransactionID uuid.UUID `json:"charge_transaction_id"`
}

// refundView is the lookup tables a response needs, loaded once per request.
type refundView struct {
	accounts   map[uuid.UUID]string
	categories map[uuid.UUID]store.Category
}

func loadRefundView(
	env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext,
) (refundView, error) {
	view := refundView{accounts: map[uuid.UUID]string{}, categories: map[uuid.UUID]store.Category{}}
	// Deleted and closed included: last year's charge may sit in an account
	// since closed.
	accounts, err := env.DB.ListAccounts(r.Context(), sp.ID(),
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return view, err
	}
	for _, one := range accounts {
		view.accounts[one.ID] = one.Name
	}
	categories, err := env.DB.ListCategories(r.Context(), sp.ID(), true)
	if err != nil {
		return view, err
	}
	for _, one := range categories {
		view.categories[one.ID] = one
	}
	return view, nil
}

func refundChargeResponse(txn store.Transaction, view refundView) RefundChargeResponse {
	out := RefundChargeResponse{
		ID:            txn.ID,
		AccountID:     txn.AccountID,
		AccountName:   view.accounts[txn.AccountID],
		Date:          Date(txn.Date),
		Amount:        txn.Amount.String(),
		Payee:         store.DomainTransaction(txn).DisplayPayee(),
		StatementName: txn.StatementName,
	}
	if category, known := view.categories[txn.CategoryID]; known {
		id, name := category.ID, category.Name
		out.CategoryID, out.CategoryName = &id, &name
	}
	return out
}

// refundLinksForTransaction reports the links this row is either side of.
func refundLinksForTransaction(
	env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext,
) error {
	id, err := pathUUID(r, "id", "Transaction")
	if err != nil {
		return err
	}
	posting, _, err := refundPosting(env, r, sp, id)
	if err != nil {
		return err
	}
	links, err := env.DB.ListRefundLinksFor(r.Context(), sp.ID(), []uuid.UUID{id})
	if err != nil {
		return err
	}

	out := RefundLinksResponse{
		CanBeARefund: domain.CanBeARefund(posting),
		Refunds:      []RefundChargeResponse{},
		RefundedBy:   []RefundChargeResponse{},
	}
	if len(links) == 0 {
		return writeJSON(w, http.StatusOK, out)
	}

	view, err := loadRefundView(env, w, r, sp)
	if err != nil {
		return err
	}
	// A row that has gone is skipped: DeleteTransaction releases its links in
	// the same database transaction, so a dangling link is a delete in flight.
	var credits []domain.Money
	for _, link := range links {
		other, side := link.ChargeTxnID, &out.Refunds
		if link.RefundTxnID != id {
			other, side = link.RefundTxnID, &out.RefundedBy
		}
		row, err := env.DB.GetTransaction(r.Context(), sp.ID(), other)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return err
		}
		*side = append(*side, refundChargeResponse(row, view))
		if side == &out.RefundedBy {
			credits = append(credits, row.Amount)
		}
	}
	out.RefundState = domain.RefundState(posting.Txn.Amount, credits)
	return writeJSON(w, http.StatusOK, out)
}

// refundCandidates offers the charges this credit might be giving back, ranked
// by domain.RankRefundCandidates. Without a search it loads the window before
// the credit; with one, every matching row over all history, since a typed
// payee says which row is meant.
func refundCandidates(
	env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext,
) error {
	id, err := pathUUID(r, "id", "Transaction")
	if err != nil {
		return err
	}
	refund, _, err := refundPosting(env, r, sp, id)
	if err != nil {
		return err
	}
	if !domain.CanBeARefund(refund) {
		return errConflict("That transaction is not a credit a refund link applies to")
	}
	limit, err := queryInt(r, "limit", 25, 1, 200)
	if err != nil {
		return err
	}

	search := r.URL.Query().Get("q")
	query := store.TransactionQuery{SearchText: search}
	within := 0
	if search == "" {
		on := refund.Txn.ReportingDate(domain.DateEffective)
		within = domain.RefundCandidateWindowDays
		query.From = on.AddDays(-within)
		query.To = on
		query.DateMode = domain.DateEffective
	}
	postings, rows, err := service.LoadPostings(r.Context(), env.DB, sp.ID(), query)
	if err != nil {
		return err
	}

	ranked := domain.RankRefundCandidates(refund, postings, within)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	view, err := loadRefundView(env, w, r, sp)
	if err != nil {
		return err
	}
	out := RefundCandidateListResponse{Candidates: make([]RefundChargeResponse, 0, len(ranked))}
	for _, one := range ranked {
		key, err := store.ParseID(one.Txn.ID)
		if err != nil {
			return err
		}
		row, known := rows[key]
		if !known {
			continue
		}
		out.Candidates = append(out.Candidates, refundChargeResponse(row, view))
	}
	return writeJSON(w, http.StatusOK, out)
}

func linkRefund(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", "Transaction")
	if err != nil {
		return err
	}
	var body RefundLinkRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.ChargeTransactionID == uuid.Nil {
		return errInvalid("required", []string{"charge_transaction_id"},
			"Name the charge this credit gives back")
	}

	// Both rows are read through the space-scoped getter first, so an id from
	// another household is a 404 and never reaches the insert.
	refund, _, err := refundPosting(env, r, sp, id)
	if err != nil {
		return err
	}
	charge, _, err := refundPosting(env, r, sp, body.ChargeTransactionID)
	if err != nil {
		return err
	}
	if !domain.CanBeARefund(refund) {
		return errConflict("That transaction is not a credit a refund link applies to")
	}
	if !domain.CanBeRefunded(charge) {
		return errConflict("That transaction is not a charge a credit can give back")
	}
	if charge.Txn.Amount.Abs().LessThan(refund.Txn.Amount.Abs()) {
		return errConflict("That charge is smaller than the credit, so it cannot be what was refunded")
	}

	if err := env.DB.LinkRefund(r.Context(), sp.ID(), id, body.ChargeTransactionID); err != nil {
		if isNotFound(err) {
			return errConflict("One of those transactions changed; reload and try again")
		}
		return err
	}
	return refundLinksForTransaction(env, w, r, sp)
}

func unlinkRefund(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", "Transaction")
	if err != nil {
		return err
	}
	chargeID, err := pathUUID(r, "charge_id", "Transaction")
	if err != nil {
		return err
	}
	released, err := env.DB.UnlinkRefund(r.Context(), sp.ID(), id, chargeID)
	if err != nil {
		return err
	}
	if released == 0 {
		return errNotFound("Refund link")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// refundPosting resolves one transaction into the posting the predicates read,
// so account, category and amount come from the same row.
func refundPosting(
	env *Env, r *http.Request, sp auth.SpaceContext, id uuid.UUID,
) (domain.Posting, store.Transaction, error) {
	row, err := env.DB.GetTransaction(r.Context(), sp.ID(), id)
	if err != nil {
		return domain.Posting{}, row, notFoundAs(err, "Transaction")
	}
	account, err := env.DB.GetAccount(r.Context(), sp.ID(), row.AccountID)
	if err != nil {
		return domain.Posting{}, row, err
	}
	posting := domain.Posting{
		Txn:     store.DomainTransaction(row),
		Account: store.DomainAccount(account),
	}
	if row.CategoryID != uuid.Nil {
		category, err := env.DB.GetCategory(r.Context(), sp.ID(), row.CategoryID)
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
