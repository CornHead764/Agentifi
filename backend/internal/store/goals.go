package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
)

// Savings goals. A goal reserves money inside a real account; what it has
// saved is the transaction ids in `goals.txn_ids`, so a transfer counts toward
// a goal only because somebody said it did. `goal_funding_accounts` is
// replaced wholesale on every write, as the create sheet sends the whole list.

type Goal struct {
	ID           uuid.UUID
	Name         string
	Emoji        string
	AccountID    uuid.UUID
	TargetAmount domain.Money
	TargetOn     domain.Date
	CompletedOn  domain.Date
	// ClosedOn is zero while the goal is open.
	ClosedOn        domain.Date
	IsTakenFromPlan bool
	TxnIDs          []uuid.UUID
	// WithdrawalTxnIDs take money back out of the reserve; SpendingTxnIDs
	// record what it was spent on without touching it. Both are disjoint
	// subsets of TxnIDs; a row in neither is a contribution.
	WithdrawalTxnIDs  []uuid.UUID
	SpendingTxnIDs    []uuid.UUID
	FundingAccountIDs []uuid.UUID
}

// DomainGoal is the stored goal as the calculations read it. The contributions
// are not part of it: they are transactions, and the caller loads them.
func DomainGoal(g Goal) domain.Goal {
	funding := make([]domain.ID, 0, len(g.FundingAccountIDs))
	for _, id := range g.FundingAccountIDs {
		funding = append(funding, domainID(id))
	}
	return domain.Goal{
		ID:                domainID(g.ID),
		Name:              g.Name,
		AccountID:         domainID(g.AccountID),
		TargetAmount:      g.TargetAmount,
		TargetOn:          g.TargetOn,
		IsTakenFromPlan:   g.IsTakenFromPlan,
		FundingAccountIDs: funding,
		ClosedOn:          g.ClosedOn,
	}
}

const goalColumns = `id, account_id, name, emoji, target_amount, target_on, completed_on,
	closed_on, is_taken_from_plan, txn_ids, withdrawal_txn_ids, spending_txn_ids`

func (s *Store) ListGoals(ctx context.Context, spaceID SpaceID, includeDeleted bool) ([]Goal, error) {
	goals, err := queryAll(ctx, s.db, "store: list goals", scanGoal,
		`SELECT `+goalColumns+` FROM goals
		 WHERE space_id = $1 AND (is_deleted = false OR $2)
		 ORDER BY created_at, id`,
		spaceID.UUID(), includeDeleted)
	if err != nil {
		return nil, err
	}
	return goals, s.attachFundingAccounts(ctx, goals)
}

func (s *Store) GetGoal(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Goal, error) {
	goal, err := scanGoal(s.db.QueryRow(ctx,
		`SELECT `+goalColumns+` FROM goals WHERE space_id = $1 AND id = $2 AND is_deleted = false`,
		spaceID.UUID(), id))
	if err != nil {
		return Goal{}, wrap("store: get goal", err)
	}
	goals := []Goal{goal}
	if err := s.attachFundingAccounts(ctx, goals); err != nil {
		return Goal{}, err
	}
	return goals[0], nil
}

func (s *Store) CreateGoal(ctx context.Context, spaceID SpaceID, g *Goal) error {
	if g.ID == uuid.Nil {
		g.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO goals (id, space_id, account_id, name, emoji, target_amount, target_on,
			is_taken_from_plan, txn_ids, withdrawal_txn_ids, spending_txn_ids, closed_on)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		g.ID, spaceID.UUID(), g.AccountID, g.Name, pgconv.NullText(g.Emoji),
		pgconv.Money(g.TargetAmount), pgconv.NullDate(g.TargetOn), g.IsTakenFromPlan, g.TxnIDs,
		withdrawalsOf(*g), spendingOf(*g), pgconv.NullDate(g.ClosedOn))
	if err != nil {
		return wrap("store: create goal", err)
	}
	return s.replaceFundingAccounts(ctx, *g)
}

func (s *Store) UpdateGoal(ctx context.Context, spaceID SpaceID, g *Goal) error {
	_, err := s.db.Exec(ctx, `
		UPDATE goals SET account_id = $3, name = $4, emoji = $5, target_amount = $6,
			target_on = $7, is_taken_from_plan = $8, txn_ids = $9,
			withdrawal_txn_ids = $10, spending_txn_ids = $11, closed_on = $12, updated_at = now()
		WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), g.ID, g.AccountID, g.Name, pgconv.NullText(g.Emoji),
		pgconv.Money(g.TargetAmount), pgconv.NullDate(g.TargetOn), g.IsTakenFromPlan, g.TxnIDs,
		withdrawalsOf(*g), spendingOf(*g), pgconv.NullDate(g.ClosedOn))
	if err != nil {
		return wrap("store: update goal", err)
	}
	return s.replaceFundingAccounts(ctx, *g)
}

// DeleteGoal soft-deletes, because a closed spending-plan month names the
// goal's contributions.
func (s *Store) DeleteGoal(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE goals SET is_deleted = true, updated_at = now() WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	return wrap("store: delete goal", err)
}

// withdrawalsOf and spendingOf keep each subset within TxnIDs and disjoint,
// enforced here rather than trusted: a withdrawal id outside TxnIDs would
// subtract from saved_so_far on no evidence, and a row in both would read by
// order. Withdrawal wins the tie because it moves money.
func withdrawalsOf(g Goal) []uuid.UUID { return subsetOf(g.TxnIDs, g.WithdrawalTxnIDs, nil) }

func spendingOf(g Goal) []uuid.UUID {
	return subsetOf(g.TxnIDs, g.SpendingTxnIDs, g.WithdrawalTxnIDs)
}

func subsetOf(counted, wanted, losesTo []uuid.UUID) []uuid.UUID {
	inGoal := make(map[uuid.UUID]bool, len(counted))
	for _, id := range counted {
		inGoal[id] = true
	}
	for _, id := range losesTo {
		inGoal[id] = false
	}
	out := make([]uuid.UUID, 0, len(wanted))
	seen := map[uuid.UUID]bool{}
	for _, id := range wanted {
		if inGoal[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// attachFundingAccounts fills the join for every goal in one query.
func (s *Store) attachFundingAccounts(ctx context.Context, goals []Goal) error {
	if len(goals) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(goals))
	for _, goal := range goals {
		ids = append(ids, goal.ID)
	}
	pairs, err := queryAll(ctx, s.db, "store: list goal funding accounts", scanPair[uuid.UUID, uuid.UUID],
		`SELECT goal_id, account_id FROM goal_funding_accounts WHERE goal_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	funding := map[uuid.UUID][]uuid.UUID{}
	for _, one := range pairs {
		funding[one.first] = append(funding[one.first], one.second)
	}
	for i := range goals {
		goals[i].FundingAccountIDs = funding[goals[i].ID]
	}
	return nil
}

func (s *Store) replaceFundingAccounts(ctx context.Context, goal Goal) error {
	if _, err := s.db.Exec(ctx,
		`DELETE FROM goal_funding_accounts WHERE goal_id = $1`, goal.ID); err != nil {
		return wrap("store: replace goal funding accounts", err)
	}
	for _, accountID := range goal.FundingAccountIDs {
		_, err := s.db.Exec(ctx,
			`INSERT INTO goal_funding_accounts (goal_id, account_id) VALUES ($1, $2)`,
			goal.ID, accountID)
		if err != nil {
			return wrap("store: replace goal funding accounts", err)
		}
	}
	return nil
}

func scanGoal(row scanner) (Goal, error) {
	var (
		out                             Goal
		emoji                           *string
		target                          pgtype.Numeric
		targetOn, completedOn, closedOn *time.Time
		err                             error
	)
	if err = row.Scan(&out.ID, &out.AccountID, &out.Name, &emoji, &target, &targetOn,
		&completedOn, &closedOn, &out.IsTakenFromPlan, &out.TxnIDs, &out.WithdrawalTxnIDs,
		&out.SpendingTxnIDs); err != nil {
		return Goal{}, err
	}
	out.Emoji = Deref(emoji)
	if out.TargetAmount, err = pgconv.ReadMoney(target, "goals.target_amount"); err != nil {
		return Goal{}, err
	}
	out.TargetOn = pgconv.ReadNullDate(targetOn)
	out.CompletedOn = pgconv.ReadNullDate(completedOn)
	out.ClosedOn = pgconv.ReadNullDate(closedOn)
	return out, nil
}

// --- The one classification every reader of goal progress shares -------------

// GoalLinks is one goal's three id columns plus the spending-plan flag — the
// whole input to GoalContributions. A struct because three same-typed slices
// are easily swapped, and the result is a plausible wrong number.
type GoalLinks struct {
	GoalID           uuid.UUID
	TxnIDs           []uuid.UUID
	WithdrawalTxnIDs []uuid.UUID
	SpendingTxnIDs   []uuid.UUID
	IsTakenFromPlan  bool
	// ClosedOn is the goal's; a closed goal adds nothing to the Goals bucket
	// from the month it closed.
	ClosedOn domain.Date
}

func LinksOf(g Goal) GoalLinks {
	return GoalLinks{
		GoalID:           g.ID,
		TxnIDs:           g.TxnIDs,
		WithdrawalTxnIDs: g.WithdrawalTxnIDs,
		SpendingTxnIDs:   g.SpendingTxnIDs,
		IsTakenFromPlan:  g.IsTakenFromPlan,
		ClosedOn:         g.ClosedOn,
	}
}

// GoalKinds resolves which way each counted row moves the goal. Spending is
// applied first, withdrawals second, so withdrawal wins a row named by both.
func (l GoalLinks) GoalKinds() map[uuid.UUID]domain.GoalKind {
	kinds := make(map[uuid.UUID]domain.GoalKind, len(l.TxnIDs))
	for _, id := range l.SpendingTxnIDs {
		kinds[id] = domain.GoalSpent
	}
	for _, id := range l.WithdrawalTxnIDs {
		kinds[id] = domain.GoalOut
	}
	return kinds
}

// GoalContributions turns one goal's links into the join rows the calculations
// read, signed as the register signs them. It is the only place a goal link
// becomes a domain row: the direction cannot be re-derived from the amount,
// and a reader that takes `txn_ids` alone reserves withdrawals and spending.
//
// ledger answers what a transaction id is (a map for sweeps, a per-id read for
// the card). A contribution whose row is gone has no amount to add.
func GoalContributions(
	links GoalLinks, ledger func(uuid.UUID) (Transaction, bool),
) []domain.GoalContribution {
	kinds := links.GoalKinds()
	out := make([]domain.GoalContribution, 0, len(links.TxnIDs))
	for _, txnID := range links.TxnIDs {
		txn, held := ledger(txnID)
		// CountsTowardBalance, not just not-deleted: a forecast linked to a goal
		// is not money put away.
		if !held || !CountsTowardBalance(txn) {
			continue
		}
		kind := kinds[txnID]
		if kind == "" {
			// A row named by neither subset is a contribution.
			kind = domain.GoalIn
		}
		mapped := DomainTransaction(txn)
		out = append(out, domain.GoalContribution{
			GoalID:    domainID(links.GoalID),
			TxnID:     domainID(txnID),
			AccountID: domainID(txn.AccountID),
			On:        mapped.ReportingDate(domain.DateEffective),
			Amount:    mapped.PrimaryAmount(),
			Kind:      kind,
			// A statement about how the goal is funded, so it lives on the goal.
			IsTakenFromPlan: links.IsTakenFromPlan,
			GoalClosedOn:    links.ClosedOn,
		})
	}
	return out
}

// LedgerLookup adapts a loaded ledger to GoalContributions' reader.
func LedgerLookup(byID map[uuid.UUID]Transaction) func(uuid.UUID) (Transaction, bool) {
	return func(id uuid.UUID) (Transaction, bool) {
		txn, held := byID[id]
		return txn, held
	}
}
