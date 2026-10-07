package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
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
	Register(Resource{Prefix: "/goals", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listGoalsRoute)
		rt.Write(http.MethodPost, "/", createGoal)
		rt.Write(http.MethodPatch, "/{goal_id}", updateGoal)
		rt.Write(http.MethodDelete, "/{goal_id}", deleteGoal)
		rt.Write(http.MethodPost, "/{goal_id}/close", closeGoal)
		rt.Write(http.MethodPost, "/{goal_id}/reopen", reopenGoal)
		rt.Read(http.MethodGet, "/{goal_id}/suggestions", goalSuggestions)
		rt.Write(http.MethodPost, "/{goal_id}/transactions", linkGoalTransaction)
		rt.Write(http.MethodPost, "/{goal_id}/transactions/bulk", linkGoalTransactions)
		rt.Write(http.MethodDelete, "/{goal_id}/transactions/{transaction_id}", unlinkGoalTransaction)
	}})
}

// --- Wire types --------------------------------------------------------------

// GoalFunding is one account's part in a goal, positive for money set aside.
// The rows sum to SavedSoFar, so an account that contributed is listed even
// after it leaves the funding list.
type GoalFunding struct {
	AccountID   uuid.UUID    `json:"account_id"`
	AccountName string       `json:"account_name"`
	Saved       domain.Money `json:"saved"`
}

type GoalResponse struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// Emoji is the card's glyph, null when none was chosen. Imported goals
	// have none.
	Emoji     *string   `json:"emoji"`
	AccountID uuid.UUID `json:"account_id"`
	// AccountName is the account the reserve lands in. Resolved here because a
	// card that named an id would send the client after a row to print a word.
	AccountName string `json:"account_name"`
	// FundingAccountIDs is every account the goal may draw on: the write
	// shape. Funding is the same join with what each account has put in.
	FundingAccountIDs []uuid.UUID   `json:"funding_account_ids"`
	Funding           []GoalFunding `json:"funding"`

	TargetAmount domain.Money `json:"target_amount"`
	TargetOn     *Date        `json:"target_on"`
	// CompletedOn comes only from the Simplifi import; nothing here stamps it,
	// rather than inventing a date.
	CompletedOn *Date `json:"completed_on"`
	// ClosedOn is the day the user closed the goal, null while it is open. A
	// closed goal reserves nothing; its figures are its history.
	ClosedOn *Date `json:"closed_on"`
	// Stage is "saving", "funded", "spending" or "closed" (domain
	// GoalProgress.Stage); the card is drawn by it.
	Stage string `json:"stage"`
	// IsTakenFromPlan decides whether the contribution shows in the spending
	// plan's Goals bucket; money the plan already counted must not count twice.
	IsTakenFromPlan bool `json:"is_taken_from_plan"`

	SavedSoFar domain.Money `json:"saved_so_far"`
	// Withdrawn is money taken back out, positive ("Spent" in the reference
	// product). SavedSoFar is already net of it.
	Withdrawn domain.Money `json:"withdrawn"`
	// SpentOnGoal is what the goal's money went on, net of refunds, across
	// every account. Not subtracted from SavedSoFar and not comparable to
	// Withdrawn: the same purchase is usually in both.
	SpentOnGoal domain.Money `json:"spent_on_goal"`
	// SpendingByCategory is SpentOnGoal by each row's own category, largest
	// first; a goal is a second axis across the category tree, not a category.
	// The rows sum to SpentOnGoal.
	SpendingByCategory []GoalCategorySpendResponse `json:"spending_by_category"`
	// UnassignedWithdrawn is Withdrawn less SpentOnGoal, never negative:
	// money taken out that no spending row accounts for yet.
	UnassignedWithdrawn domain.Money `json:"unassigned_withdrawn"`
	// Funded is SavedSoFar plus Withdrawn: the bar's length, so a goal saved
	// and then spent still reads as having met its target.
	Funded               domain.Money `json:"funded"`
	LeftToSave           domain.Money `json:"left_to_save"`
	ContributedThisMonth domain.Money `json:"contributed_this_month"`
	// PctComplete is clamped at 100 for the bar; SavedSoFar is the unclamped
	// truth. Null when the target is zero.
	PctComplete *domain.Rate `json:"pct_complete"`
	// PctFunded is the same over Funded, and is the figure the bar and its
	// label read. Null alongside PctComplete.
	PctFunded *domain.Rate `json:"pct_funded"`
	// MonthlyNeeded is null for an open-ended goal: no target date means no
	// required rate, and rendering one would invent a deadline. Null too once
	// the goal has been funded or closed.
	MonthlyNeeded *domain.Money `json:"monthly_needed"`
	// MonthsToTarget is what MonthlyNeeded was divided by, this month included,
	// and null alongside it. Sent because the client's local clock can land in
	// a different month than the server's.
	MonthsToTarget *int `json:"months_to_target"`
	// TargetHasPassed is what MonthsToTarget cannot say: it clamps a past date
	// to one month.
	TargetHasPassed bool `json:"target_has_passed"`
	IsComplete      bool `json:"is_complete"`
	// IsFunded is whether the target was ever reached, money spent since
	// included. IsComplete is whether the money is still there.
	IsFunded bool `json:"is_funded"`

	TxnIDs []uuid.UUID `json:"txn_ids"`
	// WithdrawalTxnIDs and SpendingTxnIDs let a picker open on the kind a row
	// was filed under; the card reads Contributions.
	WithdrawalTxnIDs []uuid.UUID `json:"withdrawal_txn_ids"`
	SpendingTxnIDs   []uuid.UUID `json:"spending_txn_ids"`
	// Contributions is every row in TxnIDs the ledger still holds, newest
	// first. Amount is the register's sign and Saved the card's. A row deleted
	// since keeps its id in TxnIDs and has no entry here.
	Contributions []GoalContributionResponse `json:"contributions"`
}

// GoalCategorySpendResponse is one line of the goal's breakdown.
type GoalCategorySpendResponse struct {
	// CategoryID is null for uncategorized rows, which are listed so the parts
	// sum to the total.
	CategoryID   *uuid.UUID   `json:"category_id"`
	CategoryName string       `json:"category_name"`
	Spent        domain.Money `json:"spent"`
	// TransactionCount counts a split row once per category it touches.
	TransactionCount int `json:"transaction_count"`
}

// GoalContributionResponse is one transaction's part in a goal, as the card
// lists it. Saved is positive for money set aside and negative for money
// taken back out; Amount is the ledger's own sign.
type GoalContributionResponse struct {
	TransactionID uuid.UUID    `json:"transaction_id"`
	Date          Date         `json:"date"`
	AccountID     uuid.UUID    `json:"account_id"`
	AccountName   string       `json:"account_name"`
	Payee         string       `json:"payee"`
	Amount        domain.Money `json:"amount"`
	Saved         domain.Money `json:"saved"`
	// Kind is "contribution", "withdrawal" or "spending". Saved is zero for the
	// last of those, and Spent carries it instead.
	Kind  string       `json:"kind"`
	Spent domain.Money `json:"spent"`
}

// GoalTransactionLink names the row a contribution or a withdrawal is, and
// which of the two it is. Direction is "contribution", "withdrawal" or
// "spending"; omitted, it falls back to the ledger sign.
type GoalTransactionLink struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	Direction     string    `json:"direction"`
}

// GoalTransactionBulkLink files a whole selection under one goal at one
// direction: the register's "Count toward a goal".
type GoalTransactionBulkLink struct {
	TransactionIDs []uuid.UUID `json:"transaction_ids"`
	// Direction is required here: a mixed selection classified by sign is a
	// guess per row.
	Direction string `json:"direction"`
}

// GoalBulkLinkResponse reports what the selection did, and the goal as it now
// stands. Partial rather than all-or-nothing: one row in another goal must not
// refuse the rest, so Skipped says which and why.
type GoalBulkLinkResponse struct {
	Goal    GoalResponse       `json:"goal"`
	Linked  int                `json:"linked"`
	Skipped []GoalBulkLinkSkip `json:"skipped"`
}

type GoalBulkLinkSkip struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	Reason        string    `json:"reason"`
}

type GoalCreate struct {
	Name              string       `json:"name"`
	Emoji             *string      `json:"emoji"`
	AccountID         uuid.UUID    `json:"account_id"`
	TargetAmount      domain.Money `json:"target_amount"`
	TargetOn          *Date        `json:"target_on"`
	IsTakenFromPlan   *bool        `json:"is_taken_from_plan"`
	FundingAccountIDs []uuid.UUID  `json:"funding_account_ids"`
	TxnIDs            []uuid.UUID  `json:"txn_ids"`
	// A pointer: omitted means "classify by sign", which differs from none.
	WithdrawalTxnIDs *[]uuid.UUID `json:"withdrawal_txn_ids"`
	SpendingTxnIDs   []uuid.UUID  `json:"spending_txn_ids"`
}

type GoalUpdate struct {
	Name              Opt[string]       `json:"name"`
	Emoji             Opt[string]       `json:"emoji"`
	AccountID         Opt[uuid.UUID]    `json:"account_id"`
	TargetAmount      Opt[domain.Money] `json:"target_amount"`
	TargetOn          Opt[Date]         `json:"target_on"`
	IsTakenFromPlan   Opt[bool]         `json:"is_taken_from_plan"`
	FundingAccountIDs *[]uuid.UUID      `json:"funding_account_ids"`
	TxnIDs            *[]uuid.UUID      `json:"txn_ids"`
	WithdrawalTxnIDs  *[]uuid.UUID      `json:"withdrawal_txn_ids"`
	SpendingTxnIDs    *[]uuid.UUID      `json:"spending_txn_ids"`
}

// --- Handlers ----------------------------------------------------------------

func listGoalsRoute(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListGoals(r.Context(), sp.ID(), false)
	if err != nil {
		return err
	}
	out, err := goalResponses(r.Context(), env, sp, rows)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func createGoal(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body GoalCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Name) == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if _, err := requireAccount(r.Context(), env, sp, body.AccountID); err != nil {
		return err
	}
	funding, err := resolveFundingAccounts(r.Context(), env, sp, body.FundingAccountIDs)
	if err != nil {
		return err
	}
	txnIDs, err := resolveGoalTransactions(r.Context(), env, sp, body.TxnIDs)
	if err != nil {
		return err
	}
	withdrawals, err := goalWithdrawalIDs(r.Context(), env, sp, body.WithdrawalTxnIDs, txnIDs)
	if err != nil {
		return err
	}
	spending, err := resolveGoalTransactions(r.Context(), env, sp, body.SpendingTxnIDs)
	if err != nil {
		return err
	}

	row := store.Goal{
		Name:              body.Name,
		Emoji:             store.Deref(body.Emoji, ""),
		AccountID:         body.AccountID,
		TargetAmount:      body.TargetAmount.Round(),
		TargetOn:          dateOrZero(body.TargetOn),
		IsTakenFromPlan:   body.IsTakenFromPlan == nil || *body.IsTakenFromPlan,
		FundingAccountIDs: funding,
		TxnIDs:            txnIDs,
		WithdrawalTxnIDs:  withdrawals,
		SpendingTxnIDs:    spending,
	}
	if err := env.DB.CreateGoal(r.Context(), sp.ID(), &row); err != nil {
		return err
	}
	return respondWithGoal(env, w, r, sp, row.ID, http.StatusCreated)
}

func updateGoal(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "goal_id", "Goal")
	if err != nil {
		return err
	}
	row, err := loadGoal(r.Context(), env, sp, id)
	if err != nil {
		return err
	}
	var body GoalUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	if body.AccountID.Present() {
		if _, err := requireAccount(r.Context(), env, sp, body.AccountID.Value); err != nil {
			return err
		}
	}
	if body.AccountID.Cleared() {
		// The named account is where the reserve lands; without one the
		// savings would stop reducing any available balance.
		return errConflict("account_id cannot be cleared")
	}
	if err := applyRequired("name", body.Name, &row.Name); err != nil {
		return err
	}
	applyNullable(body.Emoji, &row.Emoji)
	if body.AccountID.Present() {
		row.AccountID = body.AccountID.Value
	}
	if err := applyRequired("target_amount", body.TargetAmount, &row.TargetAmount); err != nil {
		return err
	}
	applyNullable(body.TargetOn, (*Date)(&row.TargetOn))
	if err := applyRequired("is_taken_from_plan", body.IsTakenFromPlan, &row.IsTakenFromPlan); err != nil {
		return err
	}
	if body.FundingAccountIDs != nil {
		funding, err := resolveFundingAccounts(r.Context(), env, sp, *body.FundingAccountIDs)
		if err != nil {
			return err
		}
		row.FundingAccountIDs = funding
	}
	if body.TxnIDs != nil {
		txnIDs, err := resolveGoalTransactions(r.Context(), env, sp, *body.TxnIDs)
		if err != nil {
			return err
		}
		row.TxnIDs = txnIDs
	}
	// A replaced txn_ids with no direction is reclassified, not merged: the old
	// directions were chosen for a different set. A patch that touches neither
	// leaves both.
	if body.WithdrawalTxnIDs != nil || body.TxnIDs != nil {
		withdrawals, err := goalWithdrawalIDs(r.Context(), env, sp, body.WithdrawalTxnIDs, row.TxnIDs)
		if err != nil {
			return err
		}
		row.WithdrawalTxnIDs = withdrawals
	}
	if body.SpendingTxnIDs != nil {
		spending, err := resolveGoalTransactions(r.Context(), env, sp, *body.SpendingTxnIDs)
		if err != nil {
			return err
		}
		row.SpendingTxnIDs = spending
	}

	if err := env.DB.UpdateGoal(r.Context(), sp.ID(), &row); err != nil {
		return err
	}
	return respondWithGoal(env, w, r, sp, row.ID, http.StatusOK)
}

// deleteGoal soft-deletes, because a closed spending-plan month names the
// goal's contributions and a hard delete would take that evidence with it.
func deleteGoal(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "goal_id", "Goal")
	if err != nil {
		return err
	}
	if _, err := loadGoal(r.Context(), env, sp, id); err != nil {
		return err
	}
	if err := env.DB.DeleteGoal(r.Context(), sp.ID(), id); err != nil {
		return err
	}
	return writeNoContent(w)
}

// closeGoal stops the goal reserving money, from today, and keeps its rows.
// Closing a closed goal keeps the day it was first closed.
func closeGoal(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	return setGoalClosed(env, w, r, sp, true)
}

// reopenGoal reserves the goal's money again.
func reopenGoal(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	return setGoalClosed(env, w, r, sp, false)
}

func setGoalClosed(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext, closed bool) error {
	id, err := pathUUID(r, "goal_id", "Goal")
	if err != nil {
		return err
	}
	row, err := loadGoal(r.Context(), env, sp, id)
	if err != nil {
		return err
	}
	switch {
	case closed && row.ClosedOn.IsZero():
		row.ClosedOn = domain.DateOf(env.now())
	case !closed:
		row.ClosedOn = domain.Date{}
	}
	if err := env.DB.UpdateGoal(r.Context(), sp.ID(), &row); err != nil {
		return err
	}
	return respondWithGoal(env, w, r, sp, row.ID, http.StatusOK)
}

// linkGoalTransaction adds one row to the goal's contributions, or re-files a
// row already there under the other direction. A row in another goal is
// refused: it would reserve the same money twice.
func linkGoalTransaction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "goal_id", "Goal")
	if err != nil {
		return err
	}
	row, err := loadGoal(r.Context(), env, sp, id)
	if err != nil {
		return err
	}
	var body GoalTransactionLink
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.TransactionID == uuid.Nil {
		return errInvalid("missing", []string{"body", "transaction_id"}, "transaction_id is required")
	}
	if _, err := resolveGoalTransactions(r.Context(), env, sp, []uuid.UUID{body.TransactionID}); err != nil {
		return err
	}
	kind, err := linkDirection(r.Context(), env, sp, body)
	if err != nil {
		return err
	}

	counted := false
	for _, existing := range row.TxnIDs {
		if existing == body.TransactionID {
			counted = true
			break
		}
	}
	if !counted {
		others, err := env.DB.ListGoals(r.Context(), sp.ID(), false)
		if err != nil {
			return err
		}
		for _, other := range others {
			if other.ID == row.ID {
				continue
			}
			for _, existing := range other.TxnIDs {
				if existing == body.TransactionID {
					return errConflict("that transaction already counts toward %s", other.Name)
				}
			}
		}
		row.TxnIDs = append(row.TxnIDs, body.TransactionID)
	}
	row.WithdrawalTxnIDs = withID(row.WithdrawalTxnIDs, body.TransactionID, kind == domain.GoalOut)
	row.SpendingTxnIDs = withID(row.SpendingTxnIDs, body.TransactionID, kind == domain.GoalSpent)

	if err := env.DB.UpdateGoal(r.Context(), sp.ID(), &row); err != nil {
		return err
	}
	return respondWithGoal(env, w, r, sp, row.ID, http.StatusOK)
}

// maxGoalLinkRows bounds one bulk link, at the register's page size.
const maxGoalLinkRows = 500

// linkGoalTransactions files a whole selection under one goal in one write,
// with the single-row rules; the offending rows are reported and the rest are
// linked.
func linkGoalTransactions(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "goal_id", "Goal")
	if err != nil {
		return err
	}
	row, err := loadGoal(r.Context(), env, sp, id)
	if err != nil {
		return err
	}
	var body GoalTransactionBulkLink
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if len(body.TransactionIDs) == 0 {
		return errInvalid("missing", []string{"body", "transaction_ids"},
			"transaction_ids is required")
	}
	if len(body.TransactionIDs) > maxGoalLinkRows {
		return errInvalid("too_many", []string{"body", "transaction_ids"},
			"at most %d transactions at a time", maxGoalLinkRows)
	}
	kind := domain.GoalKind(body.Direction)
	switch kind {
	case domain.GoalIn, domain.GoalOut, domain.GoalSpent:
	default:
		return errInvalid("invalid", []string{"body", "direction"},
			"direction must be %q, %q or %q", domain.GoalIn, domain.GoalOut, domain.GoalSpent)
	}
	wanted, err := resolveGoalTransactions(r.Context(), env, sp, body.TransactionIDs)
	if err != nil {
		return err
	}

	// Read once and index, rather than per row: a selection of five hundred
	// would otherwise be five hundred list-and-scan passes over every goal.
	others, err := env.DB.ListGoals(r.Context(), sp.ID(), false)
	if err != nil {
		return err
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

	out := GoalBulkLinkResponse{Skipped: []GoalBulkLinkSkip{}}
	for _, txnID := range wanted {
		if name, taken := claimedBy[txnID]; taken {
			out.Skipped = append(out.Skipped, GoalBulkLinkSkip{
				TransactionID: txnID,
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

	if err := env.DB.UpdateGoal(r.Context(), sp.ID(), &row); err != nil {
		return err
	}
	fresh, err := loadGoal(r.Context(), env, sp, row.ID)
	if err != nil {
		return err
	}
	goals, err := goalResponses(r.Context(), env, sp, []store.Goal{fresh})
	if err != nil {
		return err
	}
	out.Goal = goals[0]
	return writeJSON(w, http.StatusOK, out)
}

// linkDirection resolves which way the link counts; absent falls back to the
// ledger sign (money arriving is a withdrawal).
func linkDirection(
	ctx context.Context, env *Env, sp auth.SpaceContext, body GoalTransactionLink,
) (domain.GoalKind, error) {
	switch domain.GoalKind(body.Direction) {
	case domain.GoalIn, domain.GoalOut, domain.GoalSpent:
		return domain.GoalKind(body.Direction), nil
	case "":
		txn, err := env.DB.GetTransaction(ctx, sp.ID(), body.TransactionID)
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

// unlinkGoalTransaction takes one row back out of the goal's contributions.
// The row itself is untouched: it stops counting, nothing else.
func unlinkGoalTransaction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "goal_id", "Goal")
	if err != nil {
		return err
	}
	txnID, err := pathUUID(r, "transaction_id", "Transaction")
	if err != nil {
		return err
	}
	row, err := loadGoal(r.Context(), env, sp, id)
	if err != nil {
		return err
	}
	kept := make([]uuid.UUID, 0, len(row.TxnIDs))
	for _, existing := range row.TxnIDs {
		if existing != txnID {
			kept = append(kept, existing)
		}
	}
	if len(kept) == len(row.TxnIDs) {
		return errNotFound("Goal transaction")
	}
	row.TxnIDs = kept
	row.WithdrawalTxnIDs = withID(row.WithdrawalTxnIDs, txnID, false)
	row.SpendingTxnIDs = withID(row.SpendingTxnIDs, txnID, false)
	if err := env.DB.UpdateGoal(r.Context(), sp.ID(), &row); err != nil {
		return err
	}
	return respondWithGoal(env, w, r, sp, row.ID, http.StatusOK)
}

func respondWithGoal(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext, id uuid.UUID, status int) error {
	row, err := loadGoal(r.Context(), env, sp, id)
	if err != nil {
		return err
	}
	out, err := goalResponses(r.Context(), env, sp, []store.Goal{row})
	if err != nil {
		return err
	}
	return writeJSON(w, status, out[0])
}

// --- Progress ----------------------------------------------------------------

func goalResponses(ctx context.Context, env *Env, sp auth.SpaceContext, rows []store.Goal) ([]GoalResponse, error) {
	if len(rows) == 0 {
		return []GoalResponse{}, nil
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

	out := make([]GoalResponse, 0, len(rows))
	for _, row := range rows {
		goal := store.DomainGoal(row)
		progress := domain.GoalProgressFor(goal, contributions, today)
		mine := domain.ContributionsFor(goal, contributions)

		response := GoalResponse{
			ID:                   row.ID,
			Name:                 row.Name,
			Emoji:                pgconv.NullText(row.Emoji),
			AccountID:            row.AccountID,
			AccountName:          names[row.AccountID],
			FundingAccountIDs:    store.NonNil(row.FundingAccountIDs),
			Funding:              fundingRows(row, mine, names),
			TargetAmount:         row.TargetAmount,
			TargetOn:             nullableDate(row.TargetOn),
			CompletedOn:          nullableDate(row.CompletedOn),
			ClosedOn:             nullableDate(row.ClosedOn),
			Stage:                string(progress.Stage()),
			IsTakenFromPlan:      row.IsTakenFromPlan,
			SavedSoFar:           progress.SavedSoFar,
			Withdrawn:            progress.Withdrawn,
			SpentOnGoal:          progress.SpentOnGoal,
			SpendingByCategory:   categorySpendRows(goal, mine, mapped, categoryNames),
			UnassignedWithdrawn:  progress.UnassignedWithdrawn(),
			Funded:               progress.Funded,
			LeftToSave:           progress.LeftToSave(),
			ContributedThisMonth: progress.ContributedThisMonth,
			IsComplete:           progress.IsComplete(),
			IsFunded:             progress.IsFunded(),
			TxnIDs:               store.NonNil(row.TxnIDs),
			WithdrawalTxnIDs:     store.NonNil(row.WithdrawalTxnIDs),
			SpendingTxnIDs:       store.NonNil(row.SpendingTxnIDs),
			Contributions:        contributionRows(mine, transactions, names),
		}
		if pct, ok := progress.PctComplete(); ok {
			response.PctComplete = &pct
		}
		if pct, ok := progress.PctFunded(); ok {
			response.PctFunded = &pct
		}
		if progress.HasMonthlyNeeded {
			needed := progress.MonthlyNeeded
			response.MonthlyNeeded = &needed
		}
		if months, ok := domain.MonthsUntil(today, goal.TargetOn); ok {
			response.MonthsToTarget = &months
		}
		response.TargetHasPassed = domain.TargetHasPassed(today, goal.TargetOn)
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
) []GoalContributionResponse {
	out := make([]GoalContributionResponse, 0, len(mine))
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
		out = append(out, GoalContributionResponse{
			TransactionID: txnID,
			Date:          Date(contribution.On),
			AccountID:     txn.AccountID,
			AccountName:   names[txn.AccountID],
			Payee:         payee,
			Amount:        contribution.Amount,
			Saved:         contribution.Saved(),
			Kind:          string(contribution.Kind),
			Spent:         contribution.Spent(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return domain.Date(out[j].Date).Before(domain.Date(out[i].Date))
	})
	return out
}

// fundingRows is what each account has put in, declared funding accounts first
// (at zero if they have funded nothing), then any other contributor, so the
// rows sum to SavedSoFar.
func fundingRows(row store.Goal, contributions []domain.GoalContribution, names map[uuid.UUID]string) []GoalFunding {
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

	out := make([]GoalFunding, 0, len(order))
	for _, id := range order {
		out = append(out, GoalFunding{
			AccountID:   id,
			AccountName: names[id],
			Saved:       saved[id].Round(),
		})
	}
	return out
}

// categorySpendRows names the domain's breakdown for the wire.
func categorySpendRows(
	goal domain.Goal, mine []domain.GoalContribution, transactions []domain.Transaction,
	names map[uuid.UUID]string,
) []GoalCategorySpendResponse {
	lines := domain.SpendingByCategory(goal, mine, transactions)
	out := make([]GoalCategorySpendResponse, 0, len(lines))
	for _, line := range lines {
		row := GoalCategorySpendResponse{
			CategoryName:     "Uncategorized",
			Spent:            line.Spent,
			TransactionCount: line.TxnCount,
		}
		if id, err := store.ParseID(line.CategoryID); err == nil && id != uuid.Nil {
			row.CategoryID = &id
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
