package api

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Rows a goal is likely missing, for the card's "Find rows": the ledger
// around the goal's own rows, narrowed and ranked by domain.SuggestGoalRows.
// Nothing is linked here; the user picks and the bulk link files them.

// maxGoalSuggestions bounds one answer at the bulk link's own limit.
const maxGoalSuggestions = maxGoalLinkRows

func (s goalService) ListGoalSuggestions(
	ctx context.Context, req *agentifiv1.ListGoalSuggestionsRequest,
) (*agentifiv1.ListGoalSuggestionsResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	row, err := goalByID(ctx, env, sp, req.GetGoalId())
	if err != nil {
		return nil, err
	}
	kind := domain.GoalKind(strings.TrimSpace(req.GetKind()))
	switch kind {
	case domain.GoalIn, domain.GoalOut, domain.GoalSpent:
	default:
		return nil, errInvalid("invalid", []string{"query", "kind"},
			"kind must be %q, %q or %q", domain.GoalIn, domain.GoalOut, domain.GoalSpent)
	}

	all, err := env.DB.ListGoals(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	contributions, counted, err := goalContributions(ctx, env, sp, all)
	if err != nil {
		return nil, err
	}
	goal := store.DomainGoal(row)
	mine := domain.ContributionsFor(goal, contributions)
	today := domain.DateOf(env.now())

	out := &agentifiv1.ListGoalSuggestionsResponse{Kind: string(kind), Rows: []*agentifiv1.GoalSuggestion{}}
	from, to, ok := domain.GoalSuggestionWindow(kind, goal, mine, today)
	if !ok {
		return out, nil
	}
	out.From, out.To = dateProto(from), dateProto(to)

	query := store.TransactionQuery{From: from, To: to, DateMode: domain.DatePosted}
	// Contributions and withdrawals live on the goal's own accounts; both legs
	// of a transfer between them are loaded so the domain can tell which way
	// the money went.
	if kind != domain.GoalSpent {
		query.AccountIDs = append([]uuid.UUID{row.AccountID}, row.FundingAccountIDs...)
	}
	candidates, err := env.DB.LoadPostings(ctx, sp.ID(), query)
	if err != nil {
		return nil, err
	}
	countedRows := make([]domain.Transaction, 0, len(counted))
	for _, txn := range counted {
		countedRows = append(countedRows, store.DomainTransaction(txn))
	}
	names, err := goalAccountNames(ctx, env, sp)
	if err != nil {
		return nil, err
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
		suggestion := &agentifiv1.GoalSuggestion{
			TransactionId:   txnID.String(),
			Date:            txn.Date.String(),
			AccountId:       accountID.String(),
			AccountName:     names[accountID],
			Payee:           txn.DisplayPayee(),
			Amount:          moneyProto(txn.Amount),
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
			days := int32(one.DaysFromWithdrawal)
			suggestion.DaysFromWithdrawal = &days
		}
		out.Rows = append(out.Rows, suggestion)
	}
	return out, nil
}
