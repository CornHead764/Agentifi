package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Savings goals.
//
// A goal reserves money inside the one account it names, however many accounts
// fund it: what it has saved raises that account's goal_balance and lowers its
// available balance. Reserving in every funding account would subtract the
// same savings several times.
//
// Contributions are transaction ids (`goals.txn_ids`), never inferred from
// movements, and so is their direction: a contribution and a withdrawal look
// the same in the ledger, so `goals.withdrawal_txn_ids` records which rows come
// back out. A link with no direction falls back to the sign (money arriving is
// a withdrawal), which is what the Simplifi import leaves.
//
// `goals.spending_txn_ids` reports what the goal's money was spent on, from any
// account. It leaves `saved_so_far` alone (the withdrawal already reduced it)
// and leaves the spending plan.
//
// A closed goal (`goals.closed_on`) reserves nothing and keeps its rows; the
// card shows its history and reopening it reserves again.
//
// Progress is positive (money accumulated); the plan's Goals bucket is
// negative (money spent on goals). Saved() answers the first and the plan
// negates it, once.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewGoalServiceHandler(goalService{env}, opts...)
	})
}

type goalService struct{ env *Env }

// --- Handlers ----------------------------------------------------------------

func (s goalService) ListGoals(ctx context.Context, _ *agentifiv1.ListGoalsRequest) (*agentifiv1.ListGoalsResponse, error) {
	sp := spaceFrom(ctx)
	rows, err := s.env.DB.ListGoals(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	out, err := goalProtos(ctx, s.env, sp, rows)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ListGoalsResponse{Goals: out}, nil
}

func (s goalService) CreateGoal(ctx context.Context, req *agentifiv1.CreateGoalRequest) (*agentifiv1.CreateGoalResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if strings.TrimSpace(req.GetName()) == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	accountID, err := uuidFrom(req.GetAccountId(), "body", "account_id")
	if err != nil {
		return nil, err
	}
	target := domain.Zero
	if req.TargetAmount != nil {
		if target, err = moneyFrom(req.TargetAmount, "body", "target_amount"); err != nil {
			return nil, err
		}
	}
	var targetOn domain.Date
	if req.TargetOn != nil {
		if targetOn, err = dateFrom(req.GetTargetOn(), "body", "target_on"); err != nil {
			return nil, err
		}
	}
	fundingIDs, err := uuidsFrom(req.GetFundingAccountIds(), "body", "funding_account_ids")
	if err != nil {
		return nil, err
	}
	txnIDs, err := uuidsFrom(req.GetTxnIds(), "body", "txn_ids")
	if err != nil {
		return nil, err
	}
	spendingIDs, err := uuidsFrom(req.GetSpendingTxnIds(), "body", "spending_txn_ids")
	if err != nil {
		return nil, err
	}
	givenWithdrawals, err := idSetFrom(req.WithdrawalTxnIds, "withdrawal_txn_ids")
	if err != nil {
		return nil, err
	}

	if _, err := requireAccount(ctx, env, sp, accountID); err != nil {
		return nil, err
	}
	funding, err := resolveFundingAccounts(ctx, env, sp, fundingIDs)
	if err != nil {
		return nil, err
	}
	txnIDs, err = resolveGoalTransactions(ctx, env, sp, txnIDs)
	if err != nil {
		return nil, err
	}
	withdrawals, err := goalWithdrawalIDs(ctx, env, sp, givenWithdrawals, txnIDs)
	if err != nil {
		return nil, err
	}
	spending, err := resolveGoalTransactions(ctx, env, sp, spendingIDs)
	if err != nil {
		return nil, err
	}

	row := store.Goal{
		Name:              req.GetName(),
		Emoji:             req.GetEmoji(),
		AccountID:         accountID,
		TargetAmount:      target.Round(),
		TargetOn:          targetOn,
		IsTakenFromPlan:   req.IsTakenFromPlan == nil || req.GetIsTakenFromPlan(),
		FundingAccountIDs: funding,
		TxnIDs:            txnIDs,
		WithdrawalTxnIDs:  withdrawals,
		SpendingTxnIDs:    spending,
	}
	if err := env.DB.CreateGoal(ctx, sp.ID(), &row); err != nil {
		return nil, err
	}
	goal, err := goalProtoByID(ctx, env, sp, row.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateGoalResponse{Goal: goal}, nil
}

func (s goalService) UpdateGoal(ctx context.Context, req *agentifiv1.UpdateGoalRequest) (*agentifiv1.UpdateGoalResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	row, err := goalByID(ctx, env, sp, req.GetGoalId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	accountID := optOf(mask, "account_id", req.AccountId)
	targetAmount, err := optMoneyOf(mask, "target_amount", req.TargetAmount)
	if err != nil {
		return nil, err
	}
	targetOn := optOf(mask, "target_on", req.TargetOn)
	var targetDay Opt[Date]
	if targetOn.Set {
		targetDay = Opt[Date]{Set: true, Null: targetOn.Null}
		if targetOn.Present() {
			parsed, err := dateFrom(targetOn.Value, "body", "target_on")
			if err != nil {
				return nil, err
			}
			targetDay.Value = Date(parsed)
		}
	}
	funding, err := idSetFrom(req.FundingAccountIds, "funding_account_ids")
	if err != nil {
		return nil, err
	}
	txnIDs, err := idSetFrom(req.TxnIds, "txn_ids")
	if err != nil {
		return nil, err
	}
	withdrawalIDs, err := idSetFrom(req.WithdrawalTxnIds, "withdrawal_txn_ids")
	if err != nil {
		return nil, err
	}
	spendingIDs, err := idSetFrom(req.SpendingTxnIds, "spending_txn_ids")
	if err != nil {
		return nil, err
	}

	var account uuid.UUID
	if accountID.Present() {
		if account, err = uuidFrom(accountID.Value, "body", "account_id"); err != nil {
			return nil, err
		}
		if _, err := requireAccount(ctx, env, sp, account); err != nil {
			return nil, err
		}
	}
	if accountID.Cleared() {
		// The named account is where the reserve lands; without one the
		// savings would stop reducing any available balance.
		return nil, errConflict("account_id cannot be cleared")
	}
	if err := applyRequired("name", optOf(mask, "name", req.Name), &row.Name); err != nil {
		return nil, err
	}
	applyNullable(optOf(mask, "emoji", req.Emoji), &row.Emoji)
	if accountID.Present() {
		row.AccountID = account
	}
	if err := applyRequired("target_amount", targetAmount, &row.TargetAmount); err != nil {
		return nil, err
	}
	applyNullable(targetDay, (*Date)(&row.TargetOn))
	if err := applyRequired("is_taken_from_plan",
		optOf(mask, "is_taken_from_plan", req.IsTakenFromPlan), &row.IsTakenFromPlan); err != nil {
		return nil, err
	}
	if funding != nil {
		resolved, err := resolveFundingAccounts(ctx, env, sp, *funding)
		if err != nil {
			return nil, err
		}
		row.FundingAccountIDs = resolved
	}
	if txnIDs != nil {
		resolved, err := resolveGoalTransactions(ctx, env, sp, *txnIDs)
		if err != nil {
			return nil, err
		}
		row.TxnIDs = resolved
	}
	// A replaced txn_ids with no direction is reclassified, not merged: the old
	// directions were chosen for a different set. A patch that touches neither
	// leaves both.
	if withdrawalIDs != nil || txnIDs != nil {
		withdrawals, err := goalWithdrawalIDs(ctx, env, sp, withdrawalIDs, row.TxnIDs)
		if err != nil {
			return nil, err
		}
		row.WithdrawalTxnIDs = withdrawals
	}
	if spendingIDs != nil {
		spending, err := resolveGoalTransactions(ctx, env, sp, *spendingIDs)
		if err != nil {
			return nil, err
		}
		row.SpendingTxnIDs = spending
	}

	if err := env.DB.UpdateGoal(ctx, sp.ID(), &row); err != nil {
		return nil, err
	}
	goal, err := goalProtoByID(ctx, env, sp, row.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateGoalResponse{Goal: goal}, nil
}

// DeleteGoal soft-deletes, because a closed spending-plan month names the
// goal's contributions and a hard delete would take that evidence with it.
func (s goalService) DeleteGoal(ctx context.Context, req *agentifiv1.DeleteGoalRequest) (*agentifiv1.DeleteGoalResponse, error) {
	sp := spaceFrom(ctx)
	row, err := goalByID(ctx, s.env, sp, req.GetGoalId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteGoal(ctx, sp.ID(), row.ID); err != nil {
		return nil, err
	}
	return &agentifiv1.DeleteGoalResponse{}, nil
}

// CloseGoal stops the goal reserving money, from today, and keeps its rows.
// Closing a closed goal keeps the day it was first closed.
func (s goalService) CloseGoal(ctx context.Context, req *agentifiv1.CloseGoalRequest) (*agentifiv1.CloseGoalResponse, error) {
	goal, err := s.setClosed(ctx, req.GetGoalId(), true)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CloseGoalResponse{Goal: goal}, nil
}

// ReopenGoal reserves the goal's money again.
func (s goalService) ReopenGoal(ctx context.Context, req *agentifiv1.ReopenGoalRequest) (*agentifiv1.ReopenGoalResponse, error) {
	goal, err := s.setClosed(ctx, req.GetGoalId(), false)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ReopenGoalResponse{Goal: goal}, nil
}

func (s goalService) setClosed(ctx context.Context, rawID string, closed bool) (*agentifiv1.Goal, error) {
	sp := spaceFrom(ctx)
	row, err := goalByID(ctx, s.env, sp, rawID)
	if err != nil {
		return nil, err
	}
	switch {
	case closed && row.ClosedOn.IsZero():
		row.ClosedOn = domain.DateOf(s.env.now())
	case !closed:
		row.ClosedOn = domain.Date{}
	}
	if err := s.env.DB.UpdateGoal(ctx, sp.ID(), &row); err != nil {
		return nil, err
	}
	return goalProtoByID(ctx, s.env, sp, row.ID)
}

// LinkGoalTransaction adds one row to the goal's contributions, or re-files a
// row already there under the other direction. A row in another goal is
// refused: it would reserve the same money twice.
func (s goalService) LinkGoalTransaction(
	ctx context.Context, req *agentifiv1.LinkGoalTransactionRequest,
) (*agentifiv1.LinkGoalTransactionResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	row, err := goalByID(ctx, env, sp, req.GetGoalId())
	if err != nil {
		return nil, err
	}
	txnID, err := uuidFrom(req.GetTransactionId(), "body", "transaction_id")
	if err != nil {
		return nil, err
	}
	if txnID == uuid.Nil {
		return nil, errInvalid("missing", []string{"body", "transaction_id"}, "transaction_id is required")
	}
	if _, err := resolveGoalTransactions(ctx, env, sp, []uuid.UUID{txnID}); err != nil {
		return nil, err
	}
	kind, err := linkDirection(ctx, env, sp, txnID, req.GetDirection())
	if err != nil {
		return nil, err
	}

	counted := false
	for _, existing := range row.TxnIDs {
		if existing == txnID {
			counted = true
			break
		}
	}
	if !counted {
		others, err := env.DB.ListGoals(ctx, sp.ID(), false)
		if err != nil {
			return nil, err
		}
		for _, other := range others {
			if other.ID == row.ID {
				continue
			}
			for _, existing := range other.TxnIDs {
				if existing == txnID {
					return nil, errConflict("that transaction already counts toward %s", other.Name)
				}
			}
		}
		row.TxnIDs = append(row.TxnIDs, txnID)
	}
	row.WithdrawalTxnIDs = withID(row.WithdrawalTxnIDs, txnID, kind == domain.GoalOut)
	row.SpendingTxnIDs = withID(row.SpendingTxnIDs, txnID, kind == domain.GoalSpent)

	if err := env.DB.UpdateGoal(ctx, sp.ID(), &row); err != nil {
		return nil, err
	}
	goal, err := goalProtoByID(ctx, env, sp, row.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.LinkGoalTransactionResponse{Goal: goal}, nil
}

// maxGoalLinkRows bounds one bulk link, at the register's page size.
const maxGoalLinkRows = 500

// LinkGoalTransactions files a whole selection under one goal in one write,
// with the single-row rules; the offending rows are reported and the rest are
// linked.
func (s goalService) LinkGoalTransactions(
	ctx context.Context, req *agentifiv1.LinkGoalTransactionsRequest,
) (*agentifiv1.LinkGoalTransactionsResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	row, err := goalByID(ctx, env, sp, req.GetGoalId())
	if err != nil {
		return nil, err
	}
	if len(req.GetTransactionIds()) == 0 {
		return nil, errInvalid("missing", []string{"body", "transaction_ids"},
			"transaction_ids is required")
	}
	if len(req.GetTransactionIds()) > maxGoalLinkRows {
		return nil, errInvalid("too_many", []string{"body", "transaction_ids"},
			"at most %d transactions at a time", maxGoalLinkRows)
	}
	kind := domain.GoalKind(req.GetDirection())
	switch kind {
	case domain.GoalIn, domain.GoalOut, domain.GoalSpent:
	default:
		return nil, errInvalid("invalid", []string{"body", "direction"},
			"direction must be %q, %q or %q", domain.GoalIn, domain.GoalOut, domain.GoalSpent)
	}
	ids, err := uuidsFrom(req.GetTransactionIds(), "body", "transaction_ids")
	if err != nil {
		return nil, err
	}
	wanted, err := resolveGoalTransactions(ctx, env, sp, ids)
	if err != nil {
		return nil, err
	}

	// Read once and index, rather than per row: a selection of five hundred
	// would otherwise be five hundred list-and-scan passes over every goal.
	others, err := env.DB.ListGoals(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	claimedBy := map[uuid.UUID]string{}
	for _, other := range others {
		if other.ID == row.ID {
			continue
		}
		for _, txnID := range other.TxnIDs {
			claimedBy[txnID] = other.Name
		}
	}
	counted := make(map[uuid.UUID]bool, len(row.TxnIDs))
	for _, txnID := range row.TxnIDs {
		counted[txnID] = true
	}

	out := &agentifiv1.LinkGoalTransactionsResponse{Skipped: []*agentifiv1.GoalBulkLinkSkip{}}
	for _, txnID := range wanted {
		if name, taken := claimedBy[txnID]; taken {
			out.Skipped = append(out.Skipped, &agentifiv1.GoalBulkLinkSkip{
				TransactionId: txnID.String(),
				Reason:        fmt.Sprintf("already counts toward %s", name),
			})
			continue
		}
		if !counted[txnID] {
			counted[txnID] = true
			row.TxnIDs = append(row.TxnIDs, txnID)
		}
		row.WithdrawalTxnIDs = withID(row.WithdrawalTxnIDs, txnID, kind == domain.GoalOut)
		row.SpendingTxnIDs = withID(row.SpendingTxnIDs, txnID, kind == domain.GoalSpent)
		out.Linked++
	}

	if err := env.DB.UpdateGoal(ctx, sp.ID(), &row); err != nil {
		return nil, err
	}
	if out.Goal, err = goalProtoByID(ctx, env, sp, row.ID); err != nil {
		return nil, err
	}
	return out, nil
}

// linkDirection resolves which way the link counts; absent falls back to the
// ledger sign (money arriving is a withdrawal).
func linkDirection(
	ctx context.Context, env *Env, sp auth.SpaceContext, txnID uuid.UUID, direction string,
) (domain.GoalKind, error) {
	switch domain.GoalKind(direction) {
	case domain.GoalIn, domain.GoalOut, domain.GoalSpent:
		return domain.GoalKind(direction), nil
	case "":
		txn, err := env.DB.GetTransaction(ctx, sp.ID(), txnID)
		if err != nil {
			return "", err
		}
		if store.DomainTransaction(txn).PrimaryAmount().IsPositive() {
			return domain.GoalOut, nil
		}
		return domain.GoalIn, nil
	default:
		return "", errInvalid("invalid", []string{"body", "direction"},
			"direction must be %q, %q or %q", domain.GoalIn, domain.GoalOut, domain.GoalSpent)
	}
}

// goalWithdrawalIDs resolves which counted rows come back out. An explicit
// list is authoritative; an absent one falls back to the ledger sign, as
// linkDirection does, which is what create_goal and the Simplifi import rely on.
func goalWithdrawalIDs(
	ctx context.Context, env *Env, sp auth.SpaceContext, given *[]uuid.UUID, counted []uuid.UUID,
) ([]uuid.UUID, error) {
	if given != nil {
		return resolveGoalTransactions(ctx, env, sp, *given)
	}
	var out []uuid.UUID
	for _, id := range counted {
		txn, err := env.DB.GetTransaction(ctx, sp.ID(), id)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return nil, err
		}
		if store.DomainTransaction(txn).PrimaryAmount().IsPositive() {
			out = append(out, id)
		}
	}
	return out, nil
}

// idSetFrom reads an id set whose absence means "leave it alone" or "decide
// for me", which an empty set does not.
func idSetFrom(set *agentifiv1.IdSet, name string) (*[]uuid.UUID, error) {
	if set == nil {
		return nil, nil
	}
	ids, err := uuidsFrom(set.GetIds(), "body", name)
	if err != nil {
		return nil, err
	}
	return &ids, nil
}

// withID adds or removes one id, keeping the slice a set.
func withID(ids []uuid.UUID, id uuid.UUID, present bool) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids)+1)
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	if present {
		out = append(out, id)
	}
	return out
}

// UnlinkGoalTransaction takes one row back out of the goal's contributions.
// The row itself is untouched: it stops counting, nothing else.
func (s goalService) UnlinkGoalTransaction(
	ctx context.Context, req *agentifiv1.UnlinkGoalTransactionRequest,
) (*agentifiv1.UnlinkGoalTransactionResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if _, err := idFrom(req.GetGoalId(), "Goal"); err != nil {
		return nil, err
	}
	txnID, err := idFrom(req.GetTransactionId(), "Transaction")
	if err != nil {
		return nil, err
	}
	row, err := goalByID(ctx, env, sp, req.GetGoalId())
	if err != nil {
		return nil, err
	}
	kept := make([]uuid.UUID, 0, len(row.TxnIDs))
	for _, existing := range row.TxnIDs {
		if existing != txnID {
			kept = append(kept, existing)
		}
	}
	if len(kept) == len(row.TxnIDs) {
		return nil, errNotFound("Goal transaction")
	}
	row.TxnIDs = kept
	row.WithdrawalTxnIDs = withID(row.WithdrawalTxnIDs, txnID, false)
	row.SpendingTxnIDs = withID(row.SpendingTxnIDs, txnID, false)
	if err := env.DB.UpdateGoal(ctx, sp.ID(), &row); err != nil {
		return nil, err
	}
	goal, err := goalProtoByID(ctx, env, sp, row.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UnlinkGoalTransactionResponse{Goal: goal}, nil
}

func goalProtoByID(ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID) (*agentifiv1.Goal, error) {
	row, err := loadGoal(ctx, env, sp, id)
	if err != nil {
		return nil, err
	}
	out, err := goalProtos(ctx, env, sp, []store.Goal{row})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// --- Progress ----------------------------------------------------------------

func goalProtos(ctx context.Context, env *Env, sp auth.SpaceContext, rows []store.Goal) ([]*agentifiv1.Goal, error) {
	if len(rows) == 0 {
		return []*agentifiv1.Goal{}, nil
	}
	today := domain.DateOf(env.now())
	contributions, transactions, err := goalContributions(ctx, env, sp, rows)
	if err != nil {
		return nil, err
	}
	names, err := goalAccountNames(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	categoryNames, err := goalCategoryNames(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	// Deleted categories are named too, so a breakdown line does not go
	// nameless because the category behind it was retired after the purchase.
	mapped := make([]domain.Transaction, 0, len(transactions))
	for _, txn := range transactions {
		mapped = append(mapped, store.DomainTransaction(txn))
	}

	out := make([]*agentifiv1.Goal, 0, len(rows))
	for _, row := range rows {
		goal := store.DomainGoal(row)
		progress := domain.GoalProgressFor(goal, contributions, today)
		mine := domain.ContributionsFor(goal, contributions)

		pctComplete, hasPctComplete := progress.PctComplete()
		pctFunded, hasPctFunded := progress.PctFunded()
		response := &agentifiv1.Goal{
			Id:                   row.ID.String(),
			Name:                 row.Name,
			Emoji:                dbconv.NullText(row.Emoji),
			AccountId:            row.AccountID.String(),
			AccountName:          names[row.AccountID],
			FundingAccountIds:    uuidStrings(row.FundingAccountIDs),
			Funding:              fundingRows(row, mine, names),
			TargetAmount:         moneyProto(row.TargetAmount),
			TargetOn:             dateProto(row.TargetOn),
			CompletedOn:          dateProto(row.CompletedOn),
			ClosedOn:             dateProto(row.ClosedOn),
			Stage:                string(progress.Stage()),
			IsTakenFromPlan:      row.IsTakenFromPlan,
			SavedSoFar:           moneyProto(progress.SavedSoFar),
			Withdrawn:            moneyProto(progress.Withdrawn),
			SpentOnGoal:          moneyProto(progress.SpentOnGoal),
			SpendingByCategory:   categorySpendRows(goal, mine, mapped, categoryNames),
			UnassignedWithdrawn:  moneyProto(progress.UnassignedWithdrawn()),
			Funded:               moneyProto(progress.Funded),
			LeftToSave:           moneyProto(progress.LeftToSave()),
			ContributedThisMonth: moneyProto(progress.ContributedThisMonth),
			PctComplete:          rateProto(pctComplete, hasPctComplete),
			PctFunded:            rateProto(pctFunded, hasPctFunded),
			MonthlyNeeded:        nullableMoneyProto(progress.MonthlyNeeded, progress.HasMonthlyNeeded),
			TargetHasPassed:      domain.TargetHasPassed(today, goal.TargetOn),
			IsComplete:           progress.IsComplete(),
			IsFunded:             progress.IsFunded(),
			TxnIds:               uuidStrings(row.TxnIDs),
			WithdrawalTxnIds:     uuidStrings(row.WithdrawalTxnIDs),
			SpendingTxnIds:       uuidStrings(row.SpendingTxnIDs),
			Contributions:        contributionRows(mine, transactions, names),
		}
		if months, ok := domain.MonthsUntil(today, goal.TargetOn); ok {
			wire := int32(months)
			response.MonthsToTarget = &wire
		}
		out = append(out, response)
	}
	return out, nil
}

// goalContributions builds the join rows the spending plan reads, signed the
// way a transaction is: negative for money moved into the goal, which is what
// ComputeMonth sums into the Goals bucket.
func goalContributions(
	ctx context.Context, env *Env, sp auth.SpaceContext, goals []store.Goal,
) ([]domain.GoalContribution, map[uuid.UUID]store.Transaction, error) {
	var wanted []uuid.UUID
	for _, goal := range goals {
		wanted = append(wanted, goal.TxnIDs...)
	}
	if len(wanted) == 0 {
		return nil, nil, nil
	}

	// Read by id rather than listing the ledger and filtering: a goal names a
	// handful of rows, and internal/store's query takes no id list.
	byID := make(map[uuid.UUID]store.Transaction, len(wanted))
	for _, id := range wanted {
		if _, seen := byID[id]; seen {
			continue
		}
		txn, err := env.DB.GetTransaction(ctx, sp.ID(), id)
		if err != nil {
			if isNotFound(err) {
				// A contribution whose row was deleted still belongs to the
				// goal's history; it simply has no amount to add.
				continue
			}
			return nil, nil, err
		}
		byID[id] = txn
	}

	var out []domain.GoalContribution
	for _, goal := range goals {
		out = append(out, store.GoalContributions(store.LinksOf(goal), store.LedgerLookup(byID))...)
	}
	return out, byID, nil
}

// contributionRows is one goal's contributions as the card lists them,
// newest first, with the row's name and account attached.
func contributionRows(
	mine []domain.GoalContribution, transactions map[uuid.UUID]store.Transaction,
	names map[uuid.UUID]string,
) []*agentifiv1.GoalContribution {
	type dated struct {
		on  domain.Date
		row *agentifiv1.GoalContribution
	}
	rows := make([]dated, 0, len(mine))
	for _, contribution := range mine {
		txnID, err := store.ParseID(contribution.TxnID)
		if err != nil {
			continue
		}
		txn, ok := transactions[txnID]
		if !ok {
			continue
		}
		payee := txn.Payee
		if strings.TrimSpace(payee) == "" {
			payee = txn.StatementName
		}
		rows = append(rows, dated{on: contribution.On, row: &agentifiv1.GoalContribution{
			TransactionId: txnID.String(),
			Date:          contribution.On.String(),
			AccountId:     txn.AccountID.String(),
			AccountName:   names[txn.AccountID],
			Payee:         payee,
			Amount:        moneyProto(contribution.Amount),
			Saved:         moneyProto(contribution.Saved()),
			Kind:          string(contribution.Kind),
			Spent:         moneyProto(contribution.Spent()),
		}})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[j].on.Before(rows[i].on) })
	out := make([]*agentifiv1.GoalContribution, 0, len(rows))
	for _, one := range rows {
		out = append(out, one.row)
	}
	return out
}

// fundingRows is what each account has put in, declared funding accounts first
// (at zero if they have funded nothing), then any other contributor, so the
// rows sum to SavedSoFar.
func fundingRows(row store.Goal, contributions []domain.GoalContribution, names map[uuid.UUID]string) []*agentifiv1.GoalFunding {
	saved := map[uuid.UUID]domain.Money{}
	order := make([]uuid.UUID, 0, len(row.FundingAccountIDs)+len(contributions))
	add := func(id uuid.UUID) {
		if _, seen := saved[id]; !seen {
			saved[id] = domain.Zero
			order = append(order, id)
		}
	}
	for _, id := range row.FundingAccountIDs {
		add(id)
	}
	for _, contribution := range contributions {
		// Spending rows name the card the money went onto, which funded
		// nothing.
		if contribution.Kind == domain.GoalSpent {
			continue
		}
		id, err := store.ParseID(contribution.AccountID)
		if err != nil {
			continue
		}
		add(id)
		saved[id] = saved[id].Add(contribution.Saved())
	}

	out := make([]*agentifiv1.GoalFunding, 0, len(order))
	for _, id := range order {
		out = append(out, &agentifiv1.GoalFunding{
			AccountId:   id.String(),
			AccountName: names[id],
			Saved:       moneyProto(saved[id].Round()),
		})
	}
	return out
}

// categorySpendRows names the domain's breakdown for the wire.
func categorySpendRows(
	goal domain.Goal, mine []domain.GoalContribution, transactions []domain.Transaction,
	names map[uuid.UUID]string,
) []*agentifiv1.GoalCategorySpend {
	lines := domain.SpendingByCategory(goal, mine, transactions)
	out := make([]*agentifiv1.GoalCategorySpend, 0, len(lines))
	for _, line := range lines {
		row := &agentifiv1.GoalCategorySpend{
			CategoryName:     "Uncategorized",
			Spent:            moneyProto(line.Spent),
			TransactionCount: int32(line.TxnCount),
		}
		if id, err := store.ParseID(line.CategoryID); err == nil && id != uuid.Nil {
			text := id.String()
			row.CategoryId = &text
			if name, ok := names[id]; ok {
				row.CategoryName = name
			}
		}
		out = append(out, row)
	}
	return out
}

func goalCategoryNames(ctx context.Context, env *Env, sp auth.SpaceContext) (map[uuid.UUID]string, error) {
	categories, err := env.DB.ListCategories(ctx, sp.ID(), true)
	if err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]string, len(categories))
	for _, category := range categories {
		names[category.ID] = category.Name
	}
	return names, nil
}

// goalAccountNames names every account a goal can point at, deleted and closed
// ones included: a goal outlives the account it was funded from.
func goalAccountNames(ctx context.Context, env *Env, sp auth.SpaceContext) (map[uuid.UUID]string, error) {
	accounts, err := env.DB.ListAccounts(ctx, sp.ID(),
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]string, len(accounts))
	for _, account := range accounts {
		names[account.ID] = account.Name
	}
	return names, nil
}

// --- Loading -----------------------------------------------------------------

func goalByID(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (store.Goal, error) {
	id, err := idFrom(rawID, "Goal")
	if err != nil {
		return store.Goal{}, err
	}
	return loadGoal(ctx, env, sp, id)
}

func loadGoal(ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID) (store.Goal, error) {
	goal, err := env.DB.GetGoal(ctx, sp.ID(), id)
	if err != nil {
		return store.Goal{}, notFoundAs(err, "Goal")
	}
	return goal, nil
}

// resolveFundingAccounts refuses an account from another space and drops
// duplicates, which the *Add Account* row produces easily.
func resolveFundingAccounts(ctx context.Context, env *Env, sp auth.SpaceContext, ids []uuid.UUID) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		if _, err := requireAccount(ctx, env, sp, id); err != nil {
			return nil, err
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// resolveGoalTransactions refuses a contribution from another space rather than
// dropping it, which would leave the goal short with nothing to explain why.
func resolveGoalTransactions(ctx context.Context, env *Env, sp auth.SpaceContext, ids []uuid.UUID) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		row, err := env.DB.GetTransaction(ctx, sp.ID(), id)
		if err != nil {
			if isNotFound(err) {
				return nil, errConflict("transaction %s is not in this space", id)
			}
			return nil, err
		}
		if row.IsDeleted {
			return nil, errConflict("transaction %s is not in this space", id)
		}
		// A deleted row reads as absent; a forecast is in this space, so it gets
		// a refusal saying what is wrong with it.
		if !store.CountsTowardBalance(row) {
			return nil, errConflict(
				"transaction %s is a forecast, and a goal counts money that has moved", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}
