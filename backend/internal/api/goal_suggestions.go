package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Rows a goal is likely missing, for the card's "Find rows": the ledger
// around the goal's own rows, narrowed and ranked by domain.SuggestGoalRows.
// Nothing is linked here; the user picks and the bulk link files them.

// maxGoalSuggestions bounds one answer at the bulk link's own limit.
const maxGoalSuggestions = maxGoalLinkRows

type GoalSuggestionsResponse struct {
	// Kind is the direction the rows would be filed under.
	Kind string `json:"kind"`
	// From and To are the transaction dates searched, inclusive; null when
	// the goal has nothing to search around.
	From *Date `json:"from"`
	To   *Date `json:"to"`
	// Rows is best first. Truncated says more matched than were sent.
	Rows      []GoalSuggestionRow `json:"rows"`
	Truncated bool                `json:"truncated"`
}

type GoalSuggestionRow struct {
	TransactionID uuid.UUID    `json:"transaction_id"`
	Date          Date         `json:"date"`
	AccountID     uuid.UUID    `json:"account_id"`
	AccountName   string       `json:"account_name"`
	Payee         string       `json:"payee"`
	Amount        domain.Money `json:"amount"`
	// CategoryName is "Split" for a split row and empty for an uncategorized
	// one.
	CategoryName    string `json:"category_name"`
	MatchesCategory bool   `json:"matches_category"`
	MatchesPayee    bool   `json:"matches_payee"`
	// DaysFromWithdrawal is the distance to the goal's nearest withdrawal,
	// null when it has none.
	DaysFromWithdrawal *int `json:"days_from_withdrawal"`
}

func goalSuggestions(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "goal_id", "Goal")
	if err != nil {
		return err
	}
	row, err := loadGoal(r.Context(), env, sp, id)
	if err != nil {
		return err
	}
	kind := domain.GoalKind(strings.TrimSpace(r.URL.Query().Get("kind")))
	switch kind {
	case domain.GoalIn, domain.GoalOut, domain.GoalSpent:
	default:
		return errInvalid("invalid", []string{"query", "kind"},
			"kind must be %q, %q or %q", domain.GoalIn, domain.GoalOut, domain.GoalSpent)
	}

	all, err := env.DB.ListGoals(r.Context(), sp.ID(), false)
	if err != nil {
		return err
	}
	contributions, counted, err := goalContributions(r.Context(), env, sp, all)
	if err != nil {
		return err
	}
	goal := store.DomainGoal(row)
	mine := domain.ContributionsFor(goal, contributions)
	today := domain.DateOf(env.now())

	out := GoalSuggestionsResponse{Kind: string(kind), Rows: []GoalSuggestionRow{}}
	from, to, ok := domain.GoalSuggestionWindow(kind, goal, mine, today)
	if !ok {
		return writeJSON(w, http.StatusOK, out)
	}
	out.From, out.To = nullableDate(from), nullableDate(to)

	query := store.TransactionQuery{From: from, To: to, DateMode: domain.DatePosted}
	// Contributions and withdrawals live on the goal's own accounts; both legs
	// of a transfer between them are loaded so the domain can tell which way
	// the money went.
	if kind != domain.GoalSpent {
		query.AccountIDs = append([]uuid.UUID{row.AccountID}, row.FundingAccountIDs...)
	}
	candidates, err := env.DB.LoadPostings(r.Context(), sp.ID(), query)
	if err != nil {
		return err
	}
	countedRows := make([]domain.Transaction, 0, len(counted))
	for _, txn := range counted {
		countedRows = append(countedRows, store.DomainTransaction(txn))
	}
	names, err := goalAccountNames(r.Context(), env, sp)
	if err != nil {
		return err
	}

	suggestions := domain.SuggestGoalRows(kind, domain.GoalSuggestionInputs{
		Goal: goal, Mine: mine, Counted: countedRows, Candidates: candidates, Today: today,
	})
	if len(suggestions) > maxGoalSuggestions {
		suggestions, out.Truncated = suggestions[:maxGoalSuggestions], true
	}
	for _, one := range suggestions {
		txn := one.Posting.Txn
		txnID, err := store.ParseID(txn.ID)
		if err != nil {
			continue
		}
		accountID, err := store.ParseID(txn.AccountID)
		if err != nil {
			continue
		}
		suggestion := GoalSuggestionRow{
			TransactionID:   txnID,
			Date:            Date(txn.Date),
			AccountID:       accountID,
			AccountName:     names[accountID],
			Payee:           txn.DisplayPayee(),
			Amount:          txn.Amount,
			MatchesCategory: one.MatchesCategory,
			MatchesPayee:    one.MatchesPayee,
		}
		switch {
		case len(txn.Splits) > 0:
			suggestion.CategoryName = "Split"
		case one.Posting.HasCategory:
			suggestion.CategoryName = one.Posting.Category.Name
		}
		if one.HasWithdrawal {
			days := one.DaysFromWithdrawal
			suggestion.DaysFromWithdrawal = &days
		}
		out.Rows = append(out.Rows, suggestion)
	}
	return writeJSON(w, http.StatusOK, out)
}
