package api

import (
	"net/http"
	"sort"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// What happened in the investment accounts, named. There is no
// investment-transaction table: purchases, dividends and contributions are
// ordinary rows, and each one's action is derived by domain.ClassifyActivity
// rather than stored.
//
//   - A row nothing recognizes is "unknown" and gets no chip; guessing one of
//     withdrawal, purchase, fee or wire would invent a fact.
//   - The category is never trusted by name. The bank's wording is read
//     first and the category's kind last.
//
// The summary is computed over the same rows, so it always describes the list.

func init() {
	Register(Resource{Prefix: "/investment-activity", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listInvestmentActivity)
	}})
}

// ActivityRow is one classified row of an investment account.
type ActivityRow struct {
	TransactionID uuid.UUID    `json:"transaction_id"`
	AccountID     uuid.UUID    `json:"account_id"`
	On            Date         `json:"on"`
	Payee         string       `json:"payee"`
	StatementName string       `json:"statement_name"`
	CategoryID    *uuid.UUID   `json:"category_id"`
	Amount        domain.Money `json:"amount"`
	// Kind is domain.ActivityKind. "unknown" is a real answer and the client
	// renders it as no chip rather than as a guess.
	Kind string `json:"kind"`
	// IsPending is kept on the row: an authorized purchase has moved the money
	// even though the broker has not settled it, and the list says which.
	IsPending bool `json:"is_pending"`
}

// ActivitySummaryResponse is one kind's contribution over the window, at the
// ledger's own sign — fees negative, dividends positive.
type ActivitySummaryResponse struct {
	Kind  string       `json:"kind"`
	Count int          `json:"count"`
	Total domain.Money `json:"total"`
}

// ActivityResponse is the Transactions tab. Income and Fees are computed here,
// since summing them off a windowed, paginated list would be wrong.
type ActivityResponse struct {
	Window     WindowResponse            `json:"window"`
	AccountIDs []uuid.UUID               `json:"account_ids"`
	Items      []ActivityRow             `json:"items"`
	Summary    []ActivitySummaryResponse `json:"summary"`
	// Income is dividends, interest and the distributions that were reinvested
	// rather than paid out. Fees is reported positive, as a magnitude.
	Income domain.Money `json:"income"`
	Fees   domain.Money `json:"fees"`
}

func listInvestmentActivity(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	window, err := WindowFromRequest(r)
	if err != nil {
		return err
	}
	wanted, err := queryAccountFilter(r)
	if err != nil {
		return err
	}

	response := ActivityResponse{
		Window:     windowResponse(window),
		AccountIDs: []uuid.UUID{},
		Items:      []ActivityRow{},
		Summary:    []ActivitySummaryResponse{},
	}
	if wanted.selectsNothing() {
		return writeJSON(w, http.StatusOK, response)
	}

	accounts, err := env.DB.ListAccounts(r.Context(), sp.ID(),
		wanted.narrow(store.AccountQuery{IncludeClosed: true}))
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(accounts))
	for _, account := range accounts {
		if account.Kind == domain.KindInvestment {
			ids = append(ids, account.ID)
		}
	}
	response.AccountIDs = store.NonNil(ids)
	// No investment accounts is an empty answer: omitted AccountIDs below
	// would read as every account.
	if len(ids) == 0 {
		return writeJSON(w, http.StatusOK, response)
	}

	postings, _, err := service.LoadPostings(r.Context(), env.DB, sp.ID(), store.TransactionQuery{
		AccountIDs: ids,
		From:       window.From,
		To:         window.To,
		DateMode:   window.Mode,
	})
	if err != nil {
		return err
	}

	rows := domain.ClassifyActivities(postings)
	for _, row := range rows {
		txn := row.Posting.Txn
		item := ActivityRow{
			TransactionID: mustParseID(txn.ID),
			AccountID:     mustParseID(txn.AccountID),
			On:            Date(txn.Date),
			Payee:         txn.Payee,
			StatementName: txn.StatementName,
			Amount:        row.Posting.Amount(),
			Kind:          string(row.Kind),
			IsPending:     txn.IsPending,
		}
		if row.Posting.HasCategory {
			id := mustParseID(row.Posting.Category.ID)
			item.CategoryID = &id
		}
		response.Items = append(response.Items, item)
	}
	// Newest first, the way the register reads, with a stable tie-break so two
	// rows on one day do not swap places between requests.
	sort.SliceStable(response.Items, func(i, j int) bool {
		left, right := response.Items[i], response.Items[j]
		if left.On != right.On {
			return domain.Date(left.On).After(domain.Date(right.On))
		}
		return left.TransactionID.String() < right.TransactionID.String()
	})

	for _, summary := range domain.SummarizeActivity(rows) {
		response.Summary = append(response.Summary, ActivitySummaryResponse{
			Kind: string(summary.Kind), Count: summary.Count, Total: summary.Total,
		})
	}
	response.Income = domain.InvestmentIncome(rows)
	response.Fees = domain.InvestmentFees(rows)
	return writeJSON(w, http.StatusOK, response)
}

// mustParseID turns a domain id back into its uuid. Every posting id came from
// the store, so a failure is a programming error, visible as the zero uuid.
func mustParseID(id domain.ID) uuid.UUID {
	parsed, err := store.ParseID(id)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}
