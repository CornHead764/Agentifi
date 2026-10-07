package domain

// Savings goals. A goal reserves money inside the one account it names:
// SavedSoFar feeds that account's GoalBalance, which the available balance
// subtracts. Contributions are tracked by transaction id, never inferred from
// account movements, or every unrelated deposit would fund the goal.

import (
	"sort"

	"github.com/shopspring/decimal"
)

// GoalKind is how one transaction relates to a goal. Contributions and
// withdrawals move the reserve; spending only records where the money went.
type GoalKind string

const (
	GoalIn  GoalKind = "contribution"
	GoalOut GoalKind = "withdrawal"
	// GoalSpent is money spent on the goal's purpose, from any account. It
	// leaves SavedSoFar alone: the withdrawal that funded it already reduced
	// the reserve, and counting it twice would read as overdrawn.
	GoalSpent GoalKind = "spending"
)

// GoalContribution is one transaction's participation in one goal.
type GoalContribution struct {
	GoalID    ID
	TxnID     ID
	AccountID ID
	On        Date
	Amount    Money

	// Kind is recorded, not derived (see Saved). Unset reads as a contribution.
	Kind GoalKind

	// IsTakenFromPlan is false when the goal is funded from money the plan
	// already counted, so it does not reappear in the Goals bucket. The column
	// defaults to true and Go's zero value is false: every construction site
	// must set it or the Goals bucket silently reads zero.
	IsTakenFromPlan bool
	// GoalClosedOn is the goal's Goal.ClosedOn, carried on the row because
	// the spending plan reads rows, not goals.
	GoalClosedOn Date
}

// CountsInGoalsBucket is whether this row is money set aside in month: a
// contribution or withdrawal that posted in it, from a goal taken from the
// plan and not closed by then. A closed goal leaves the months before its
// close as they were, when the money really was being set aside.
func (c GoalContribution) CountsInGoalsBucket(month Month) bool {
	if c.Kind == GoalSpent || !c.IsTakenFromPlan || !month.Contains(c.On) {
		return false
	}
	return c.GoalClosedOn.IsZero() || month.Before(MonthOf(c.GoalClosedOn))
}

// GoalProgress is the goal card, computed in one pass so its figures cannot
// disagree.
type GoalProgress struct {
	Goal       Goal
	SavedSoFar Money
	// Withdrawn is what came back out; Funded is saved plus withdrawn.
	Withdrawn Money
	Funded    Money
	// SpentOnGoal is what the withdrawn money went on (SpentOn).
	SpentOnGoal          Money
	ContributedThisMonth Money
	// MonthlyNeeded is unset (HasMonthlyNeeded false) without a target date,
	// and once the goal has been funded or closed: it asks for nothing more.
	MonthlyNeeded    Money
	HasMonthlyNeeded bool
}

// GoalStage is where a goal is in its life: saved toward, funded, drawn on
// and spent, or closed by the user.
type GoalStage string

const (
	GoalStageSaving   GoalStage = "saving"
	GoalStageFunded   GoalStage = "funded"
	GoalStageSpending GoalStage = "spending"
	GoalStageClosed   GoalStage = "closed"
)

// Stage is closed once the user closed the goal; spending once any money was
// taken out or spent on it, funded or not; funded once a positive target was
// reached with nothing taken out; saving otherwise. A zero target is never
// funded, because it names no amount to reach.
func (p GoalProgress) Stage() GoalStage {
	switch {
	case p.Goal.IsClosed():
		return GoalStageClosed
	case p.Withdrawn.IsPositive() || !p.SpentOnGoal.IsZero():
		return GoalStageSpending
	case p.Goal.TargetAmount.IsPositive() && p.IsFunded():
		return GoalStageFunded
	default:
		return GoalStageSaving
	}
}

func (p GoalProgress) UnassignedWithdrawn() Money {
	return UnassignedWithdrawn(p.Withdrawn, p.SpentOnGoal)
}

func (p GoalProgress) LeftToSave() Money { return LeftToSave(p.Goal, p.SavedSoFar) }

func (p GoalProgress) PctComplete() (Rate, bool) { return PctComplete(p.Goal, p.SavedSoFar) }

func (p GoalProgress) PctFunded() (Rate, bool) { return PctFunded(p.Goal, p.Funded) }

func (p GoalProgress) IsComplete() bool {
	return !p.SavedSoFar.LessThan(p.Goal.TargetAmount)
}

// IsFunded is whether the goal ever reached its target, spending since then
// included. IsComplete is whether the money is still there.
func (p GoalProgress) IsFunded() bool {
	return !p.Funded.LessThan(p.Goal.TargetAmount)
}

// ContributionsFor narrows a mixed set of join rows to one goal's.
func ContributionsFor(goal Goal, contributions []GoalContribution) []GoalContribution {
	rows := make([]GoalContribution, 0, len(contributions))
	for _, row := range contributions {
		if row.GoalID == goal.ID {
			rows = append(rows, row)
		}
	}
	return rows
}

// Saved is what this row put into the goal: positive in, negative out, zero
// for spending. Direction comes from Kind, not the ledger sign — a debit from
// a funding account can be either a contribution or a withdrawal, and only
// the user knows which. Rows imported from Simplifi's dissolved GOAL accounts
// are classified by sign at import.
func (c GoalContribution) Saved() Money {
	switch c.Kind {
	case GoalOut:
		return c.Amount.Abs().Neg()
	case GoalSpent:
		return Zero
	default:
		return c.Amount.Abs()
	}
}

// Spent is what this row spent on the goal's purpose, positive out. It keeps
// the ledger sign (negated) so a refund brings the total back down.
func (c GoalContribution) Spent() Money {
	if c.Kind != GoalSpent {
		return Zero
	}
	return c.Amount.Neg()
}

// SavedSoFar is contributions less withdrawals, not clamped at zero, or the
// account's reserve and the goal's figure would disagree.
func SavedSoFar(goal Goal, contributions []GoalContribution) Money {
	return Sum(ContributionsFor(goal, contributions), GoalContribution.Saved)
}

// SpentOn is spending on the goal's purpose net of refunds. Deliberately not
// subtracted from SavedSoFar: the same purchase is often both a withdrawal and
// a card charge.
func SpentOn(goal Goal, contributions []GoalContribution) Money {
	return Sum(ContributionsFor(goal, contributions), GoalContribution.Spent)
}

// Withdrawn is money that reached the goal and came back out, positive.
func Withdrawn(goal Goal, contributions []GoalContribution) Money {
	var out []Money
	for _, row := range ContributionsFor(goal, contributions) {
		if saved := row.Saved(); saved.IsNegative() {
			out = append(out, saved.Neg())
		}
	}
	return Total(out...)
}

// Funded is everything that ever reached the goal (saved plus withdrawn), what
// the bar fills to: a goal saved in full and then spent has still been met.
// Gross rather than a high-water mark, because backdated rows make a peak
// meaningless.
func Funded(goal Goal, contributions []GoalContribution) Money {
	return SavedSoFar(goal, contributions).Add(Withdrawn(goal, contributions))
}

// UnassignedWithdrawn is money taken out of the reserve that no spending row
// accounts for yet, never negative: spending beyond the withdrawals was paid
// from elsewhere and leaves nothing unaccounted.
func UnassignedWithdrawn(withdrawn, spent Money) Money {
	remaining := withdrawn.Sub(spent)
	if remaining.IsNegative() {
		return Zero
	}
	return remaining.Round()
}

// LeftToSave is never negative — an overfunded goal needs nothing more, not
// less.
func LeftToSave(goal Goal, saved Money) Money {
	remaining := goal.TargetAmount.Sub(saved)
	if remaining.IsNegative() {
		return Zero
	}
	return remaining.Round()
}

// PctComplete is progress for the bar, clamped at 100%; false for a zero
// target. SavedSoFar stays the unclamped truth.
func PctComplete(goal Goal, saved Money) (Rate, bool) {
	raw, ok := Percent(saved, goal.TargetAmount)
	if !ok {
		return decimal.Zero, false
	}
	hundred := decimal.NewFromInt(100)
	if raw.GreaterThan(hundred) {
		return hundred, true
	}
	return raw, true
}

// PctFunded is how much of the target the goal has ever held, clamped at 100%
// and false for a target of zero — PctComplete's rule, over Funded.
func PctFunded(goal Goal, funded Money) (Rate, bool) {
	return PctComplete(goal, funded)
}

// ContributedThisMonth is net movement into the goal this month: a contribution
// reversed the next day funded nothing.
func ContributedThisMonth(goal Goal, contributions []GoalContribution, month Month) Money {
	var inMonth []GoalContribution
	for _, row := range ContributionsFor(goal, contributions) {
		if month.Contains(row.On) {
			inMonth = append(inMonth, row)
		}
	}
	return Sum(inMonth, GoalContribution.Saved)
}

// MonthsUntil counts months to the target including the current one; false
// without a target date. A past or current-month target gives one, never zero
// or negative.
func MonthsUntil(today Date, targetOn Date) (int, bool) {
	if targetOn.IsZero() {
		return 0, false
	}
	months := MonthCount(MonthOf(today), MonthOf(targetOn)) + 1
	if months < 1 {
		months = 1
	}
	return months, true
}

// TargetHasPassed tells "one month left" from "overdue", which MonthsUntil's
// clamp cannot.
func TargetHasPassed(today Date, targetOn Date) bool {
	return !targetOn.IsZero() && targetOn.Before(today)
}

// MonthlyNeeded is what must go in each month to hit the target date; false
// for an open-ended goal, which has no deadline to invent.
func MonthlyNeeded(goal Goal, saved Money, today Date) (Money, bool) {
	months, ok := MonthsUntil(today, goal.TargetOn)
	if !ok {
		return Zero, false
	}
	perMonth, ok := LeftToSave(goal, saved).DivInt(months)
	if !ok {
		return Zero, false
	}
	return perMonth.Round(), true
}

// GoalProgressFor builds the whole card in one pass.
func GoalProgressFor(goal Goal, contributions []GoalContribution, today Date) GoalProgress {
	rows := ContributionsFor(goal, contributions)
	saved := SavedSoFar(goal, rows)
	progress := GoalProgress{
		Goal:                 goal,
		SavedSoFar:           saved,
		Withdrawn:            Withdrawn(goal, rows),
		Funded:               Funded(goal, rows),
		SpentOnGoal:          SpentOn(goal, rows),
		ContributedThisMonth: ContributedThisMonth(goal, rows, MonthOf(today)),
	}
	if !goal.IsClosed() && !progress.IsFunded() {
		progress.MonthlyNeeded, progress.HasMonthlyNeeded = MonthlyNeeded(goal, saved, today)
	}
	return progress
}

// ReservedInAccount is the GoalBalance for one account: only goals that name
// it, however many accounts fund them, or the same savings would be subtracted
// from several balances. A closed goal reserves nothing, so what it still
// holds is available again.
func ReservedInAccount(accountID ID, goals []Goal, contributions []GoalContribution) Money {
	var reserved []Money
	for _, goal := range goals {
		if goal.AccountID == accountID && !goal.IsClosed() {
			reserved = append(reserved, SavedSoFar(goal, contributions))
		}
	}
	return Total(reserved...)
}

// GoalCategorySpend is one category's share of what a goal's money went on.
type GoalCategorySpend struct {
	// CategoryID is unset for uncategorized rows, which are kept so the parts
	// sum to the total.
	CategoryID ID
	Spent      Money
	// TxnCount counts a split row once per category it touches.
	TxnCount int
}

// SpendingByCategory is a goal's spending by category, largest first. Split
// aware, using the filter engine's share arithmetic; signs match SpentOn so
// the rows sum to it. A contribution whose transaction is missing (deleted) is
// skipped.
func SpendingByCategory(
	goal Goal, contributions []GoalContribution, transactions []Transaction,
) []GoalCategorySpend {
	byID := make(map[ID]Transaction, len(transactions))
	for _, txn := range transactions {
		byID[txn.ID] = txn
	}

	totals := map[ID]Money{}
	counts := map[ID]int{}
	var order []ID
	add := func(categoryID ID, amount Money) {
		if _, seen := totals[categoryID]; !seen {
			totals[categoryID] = Zero
			order = append(order, categoryID)
		}
		totals[categoryID] = totals[categoryID].Add(amount)
		counts[categoryID]++
	}

	for _, row := range ContributionsFor(goal, contributions) {
		if row.Kind != GoalSpent {
			continue
		}
		txn, ok := byID[row.TxnID]
		if !ok {
			continue
		}
		if len(txn.Splits) == 0 {
			add(txn.CategoryID, row.Spent())
			continue
		}
		for _, split := range txn.Splits {
			add(split.CategoryID, SplitAmountPrimary(txn, split).Neg())
		}
	}

	out := make([]GoalCategorySpend, 0, len(order))
	for _, categoryID := range order {
		out = append(out, GoalCategorySpend{
			CategoryID: categoryID,
			Spent:      totals[categoryID].Round(),
			TxnCount:   counts[categoryID],
		})
	}
	// Ties by id so the order is stable.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Spent.Equal(out[j].Spent) {
			return out[i].Spent.GreaterThan(out[j].Spent)
		}
		return out[i].CategoryID < out[j].CategoryID
	})
	return out
}
