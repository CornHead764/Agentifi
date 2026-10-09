package api

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Possible duplicates: a charge two sources both wrote, such as a Simplifi
// import and the SimpleFIN sync that followed it. domain.FindDuplicateCandidates
// proposes pairs after every ingest and after a Simplifi import; this resource
// lists the ones waiting and takes the household's verdict. Nothing is retired
// until a person says so.
//
// A pair is dated by the posted day (trap 4 concerns reports; here the two
// rows are compared as the sources posted them).

func init() {
	Register(Resource{Prefix: "/transaction-duplicates", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listDuplicates)
		rt.Write(http.MethodPost, "/scan", scanDuplicates)
		rt.Write(http.MethodPost, "/{pair_id}/decide", decideDuplicate)
	}})
}

// DuplicateRowResponse is one side of a pair, with what a person compares.
type DuplicateRowResponse struct {
	ID            uuid.UUID    `json:"id"`
	Date          Date         `json:"date"`
	Amount        domain.Money `json:"amount"`
	Currency      string       `json:"currency"`
	Payee         string       `json:"payee"`
	StatementName string       `json:"statement_name"`
	CategoryName  *string      `json:"category_name"`
	Notes         *string      `json:"notes"`
	Source        string       `json:"source"`
	IsPending     bool         `json:"is_pending"`
	// IsTransferLeg is set when retiring this row would release a transfer.
	IsTransferLeg bool `json:"is_transfer_leg"`
}

// DuplicatePairResponse is two rows that may be one charge.
type DuplicatePairResponse struct {
	ID          uuid.UUID `json:"id"`
	AccountID   uuid.UUID `json:"account_id"`
	AccountName string    `json:"account_name"`
	DaysApart   int       `json:"days_apart"`
	// SuggestedKeepID is the row to keep if the pair is a duplicate.
	SuggestedKeepID uuid.UUID            `json:"suggested_keep_id"`
	First           DuplicateRowResponse `json:"first"`
	Second          DuplicateRowResponse `json:"second"`
}

// DuplicateListResponse is the pairs waiting on a verdict.
type DuplicateListResponse struct {
	Count int                     `json:"count"`
	Pairs []DuplicatePairResponse `json:"pairs"`
}

// DuplicateScanResponse is what a scan of the whole history found.
type DuplicateScanResponse struct {
	// Found is how many pairs were new; Open is how many now wait.
	Found int `json:"found"`
	Open  int `json:"open"`
}

// DuplicateDecisionRequest is the verdict on one pair. KeepID is for a
// duplicate verdict and defaults to the suggested row.
type DuplicateDecisionRequest struct {
	Verdict string     `json:"verdict"`
	KeepID  *uuid.UUID `json:"keep_id"`
}

// DuplicateDecisionResponse is what the verdict did.
type DuplicateDecisionResponse struct {
	ID      uuid.UUID  `json:"id"`
	Verdict string     `json:"verdict"`
	KeptID  *uuid.UUID `json:"kept_id"`
	// RetiredID is the copy removed, for a duplicate verdict.
	RetiredID *uuid.UUID `json:"retired_id"`
}

func listDuplicates(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	open, err := env.DB.ListOpenDuplicates(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	out := DuplicateListResponse{Count: len(open), Pairs: []DuplicatePairResponse{}}
	if len(open) == 0 {
		return writeJSON(w, http.StatusOK, out)
	}

	ids := make([]uuid.UUID, 0, 2*len(open))
	for _, pair := range open {
		ids = append(ids, pair.FirstID, pair.SecondID)
	}
	rows, err := env.DB.ListTransactions(r.Context(), sp.ID(), store.TransactionQuery{IDs: ids})
	if err != nil {
		return err
	}
	byID := make(map[uuid.UUID]store.Transaction, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	accounts, err := env.DB.ListAccounts(r.Context(), sp.ID(), store.AccountQuery{
		IncludeClosed: true, IncludeIgnored: true, IncludeDeleted: true,
	})
	if err != nil {
		return err
	}
	accountNames := make(map[uuid.UUID]string, len(accounts))
	for _, account := range accounts {
		accountNames[account.ID] = account.Name
	}
	categories, err := env.DB.ListCategories(r.Context(), sp.ID(), true)
	if err != nil {
		return err
	}
	categoryNames := make(map[uuid.UUID]string, len(categories))
	for _, category := range categories {
		categoryNames[category.ID] = category.Name
	}

	out.Pairs = make([]DuplicatePairResponse, 0, len(open))
	for _, pair := range open {
		first, second := byID[pair.FirstID], byID[pair.SecondID]
		keep, err := uuid.Parse(string(domain.SuggestedDuplicateKeep(
			store.DomainTransaction(first), store.DomainTransaction(second))))
		if err != nil {
			return err
		}
		out.Pairs = append(out.Pairs, DuplicatePairResponse{
			ID:              pair.ID,
			AccountID:       pair.AccountID,
			AccountName:     accountNames[pair.AccountID],
			DaysApart:       pair.DaysApart,
			SuggestedKeepID: keep,
			First:           duplicateRow(first, categoryNames),
			Second:          duplicateRow(second, categoryNames),
		})
	}
	return writeJSON(w, http.StatusOK, out)
}

func duplicateRow(row store.Transaction, categoryNames map[uuid.UUID]string) DuplicateRowResponse {
	out := DuplicateRowResponse{
		ID:            row.ID,
		Date:          Date(row.Date),
		Amount:        row.Amount,
		Currency:      row.Currency,
		Payee:         row.Payee,
		StatementName: row.StatementName,
		Source:        string(row.Source),
		IsPending:     row.IsPending,
		IsTransferLeg: row.TransferPairID != uuid.Nil,
	}
	if name, ok := categoryNames[row.CategoryID]; ok {
		out.CategoryName = &name
	}
	if row.Notes != "" {
		out.Notes = &row.Notes
	}
	return out
}

// scanDuplicates looks over the whole history, for rows that were there before
// the check was, and for a household that wants to ask again.
func scanDuplicates(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	found, err := service.NewDuplicates(env.DB).DetectSpace(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	open, err := env.DB.CountOpenDuplicates(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, DuplicateScanResponse{Found: found, Open: open})
}

func decideDuplicate(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "pair_id", "Duplicate pair")
	if err != nil {
		return err
	}
	var body DuplicateDecisionRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	duplicates := service.NewDuplicates(env.DB)

	switch body.Verdict {
	case store.VerdictDistinct:
		if err := duplicates.MarkDistinct(r.Context(), sp.ID(), sp.UserID(), id); err != nil {
			return duplicateFailure(err)
		}
		return writeJSON(w, http.StatusOK, DuplicateDecisionResponse{ID: id, Verdict: body.Verdict})
	case store.VerdictDuplicate:
		keepID := uuid.Nil
		if body.KeepID != nil {
			keepID = *body.KeepID
		}
		retired, err := duplicates.MarkDuplicate(r.Context(), sp.ID(), sp.UserID(), id, keepID)
		if err != nil {
			return duplicateFailure(err)
		}
		if err := purgeAttachments(env, r, sp, retired.RetiredID); err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, DuplicateDecisionResponse{
			ID: id, Verdict: body.Verdict, KeptID: &retired.KeptID, RetiredID: &retired.RetiredID,
		})
	}
	return errInvalid("invalid", []string{"body", "verdict"}, "verdict must be %q or %q",
		store.VerdictDuplicate, store.VerdictDistinct)
}

func duplicateFailure(err error) error {
	switch {
	case errors.Is(err, service.ErrDuplicateDecided):
		return errConflict("This pair has already been decided")
	case errors.Is(err, service.ErrDuplicateGone):
		return errConflict("One of these transactions has been deleted since the pair was found")
	case errors.Is(err, service.ErrNotInPair):
		return errInvalid("invalid", []string{"body", "keep_id"}, "keep_id is neither transaction of the pair")
	}
	return notFoundAs(err, "Duplicate pair")
}
