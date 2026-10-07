package domain

// Rows a goal is likely missing, offered for the user to file in bulk. A
// suggestion is never linked on its own: a contribution, a withdrawal and a
// purchase on the goal look like any other row, and only the user knows.
//
// The goal's life orders the search. Contributions arrive in the reserve
// account before the money is drawn on; withdrawals leave it once something
// was saved; the purchases follow the withdrawals onto whichever account paid,
// so they are looked for around them and ranked by how much they resemble
// the spending already filed.

import "sort"

const (
	// goalSpendingLead is how long before the first withdrawal a purchase may
	// fall: a deposit is often paid on a card just before the money is transferred.
	goalSpendingLead = 14
	// goalSpendingTail is how long after the later of the target date and the
	// last withdrawal purchases keep arriving.
	goalSpendingTail = 60
	// goalLookback bounds a search that has no row of the goal's to start
	// from.
	goalLookback = 365
	// goalNearWithdrawal and goalCloseToWithdrawal are the day distances that
	// rank a purchase as close to a withdrawal.
	goalNearWithdrawal    = 7
	goalCloseToWithdrawal = 30
)

// GoalSuggestionInputs is everything SuggestGoalRows reads.
type GoalSuggestionInputs struct {
	Goal Goal
	// Mine is this goal's rows (ContributionsFor).
	Mine []GoalContribution
	// Counted is every transaction any goal counts, this one's included: none
	// is suggested again, and nor is the other leg of a counted transfer,
	// which would count the same money twice.
	Counted []Transaction
	// Candidates is the ledger to search, at least GoalSuggestionWindow's
	// span, both legs of a transfer included.
	Candidates []Posting
	Today      Date
}

// GoalSuggestion is one row offered, with why it ranks where it does.
type GoalSuggestion struct {
	Posting Posting
	// MatchesCategory and MatchesPayee say the row resembles spending already
	// filed under the goal.
	MatchesCategory bool
	MatchesPayee    bool
	// DaysFromWithdrawal is the distance to the nearest withdrawal, set only
	// when HasWithdrawal.
	DaysFromWithdrawal int
	HasWithdrawal      bool
}

func (s GoalSuggestion) score() int {
	score := 0
	if s.MatchesCategory {
		score += 4
	}
	if s.MatchesPayee {
		score += 2
	}
	if s.HasWithdrawal {
		switch {
		case s.DaysFromWithdrawal <= goalNearWithdrawal:
			score += 2
		case s.DaysFromWithdrawal <= goalCloseToWithdrawal:
			score++
		}
	}
	return score
}

// goalDates is the first and last date among a goal's rows of one kind.
func goalDates(mine []GoalContribution, kind GoalKind) (first, last Date, ok bool) {
	for _, row := range mine {
		if row.Kind != kind {
			continue
		}
		if !ok || row.On.Before(first) {
			first = row.On
		}
		if !ok || row.On.After(last) {
			last = row.On
		}
		ok = true
	}
	return first, last, ok
}

func earliestGoalRow(mine []GoalContribution) (Date, bool) {
	var first Date
	for i, row := range mine {
		if i == 0 || row.On.Before(first) {
			first = row.On
		}
	}
	return first, len(mine) > 0
}

// GoalSuggestionWindow is the span of transaction dates searched for one kind,
// both ends inclusive; false when there is nothing to search from.
//
//   - contribution: from a year before the goal's first row (or today) to the
//     first withdrawal, or today while nothing was withdrawn.
//   - withdrawal: from the first contribution (or a year back) to today.
//   - spending: from goalSpendingLead days before the first withdrawal to
//     goalSpendingTail days after the later of the target date and the last
//     withdrawal. Without a withdrawal it starts from the goal's first row and
//     ends goalSpendingTail days after the later of that row and the target
//     date; a goal with no rows has nothing to search around.
func GoalSuggestionWindow(kind GoalKind, goal Goal, mine []GoalContribution, today Date) (from, to Date, ok bool) {
	firstIn, _, hasIn := goalDates(mine, GoalIn)
	firstOut, lastOut, hasOut := goalDates(mine, GoalOut)
	switch kind {
	case GoalIn:
		from = today.AddDays(-goalLookback)
		if first, ok := earliestGoalRow(mine); ok {
			from = first.AddDays(-goalLookback)
		}
		to = today
		if hasOut {
			to = firstOut
		}
		return from, to, true
	case GoalOut:
		from = today.AddDays(-goalLookback)
		if hasIn {
			from = firstIn
		}
		return from, today, true
	case GoalSpent:
		start, end := firstOut, lastOut
		if !hasOut {
			first, ok := earliestGoalRow(mine)
			if !ok {
				return Date{}, Date{}, false
			}
			start, end = first, first
		}
		if goal.TargetOn.After(end) {
			end = goal.TargetOn
		}
		return start.AddDays(-goalSpendingLead), end.AddDays(goalSpendingTail), true
	}
	return Date{}, Date{}, false
}

// SuggestGoalRows is the rows of one kind the goal is likely missing, best
// first.
//
//   - contribution: money arriving in the reserve account inside the window.
//   - withdrawal: money leaving the reserve account inside the window, and
//     transfers out of the goal's other funding accounts that did not go to
//     the reserve account (those are contributions).
//   - spending: money leaving any account but the reserve inside the window,
//     leaving out transfers and card payments, ranked by category and payee
//     resemblance to the spending already filed and by distance to the
//     nearest withdrawal.
//
// Rows any goal counts, the other leg of a counted transfer, forecasts and
// deleted rows are never suggested.
func SuggestGoalRows(kind GoalKind, in GoalSuggestionInputs) []GoalSuggestion {
	from, to, ok := GoalSuggestionWindow(kind, in.Goal, in.Mine, in.Today)
	if !ok {
		return nil
	}

	counted := make(map[ID]bool, len(in.Counted))
	countedPairs := map[ID]bool{}
	for _, txn := range in.Counted {
		counted[txn.ID] = true
		if txn.TransferPairID != "" {
			countedPairs[txn.TransferPairID] = true
		}
	}
	// Which accounts each transfer touches, to tell a funding account's
	// transfer into the reserve from one out of the household's savings.
	pairAccounts := map[ID][]ID{}
	for _, p := range in.Candidates {
		if p.Txn.TransferPairID != "" {
			pairAccounts[p.Txn.TransferPairID] = append(pairAccounts[p.Txn.TransferPairID], p.Txn.AccountID)
		}
	}
	funding := map[ID]bool{}
	for _, id := range in.Goal.FundingAccountIDs {
		if id != in.Goal.AccountID {
			funding[id] = true
		}
	}

	var withdrawals []Date
	categories := map[ID]bool{}
	payees := map[string]bool{}
	mineByID := make(map[ID]GoalKind, len(in.Mine))
	for _, row := range in.Mine {
		mineByID[row.TxnID] = row.Kind
		if row.Kind == GoalOut {
			withdrawals = append(withdrawals, row.On)
		}
	}
	for _, txn := range in.Counted {
		if mineByID[txn.ID] != GoalSpent {
			continue
		}
		for _, id := range txn.CategoryIDs() {
			categories[id] = true
		}
		if name := nameKey(txn.DisplayPayee()); name != "" {
			payees[name] = true
		}
	}

	reserve := in.Goal.AccountID
	var out []GoalSuggestion
	for _, p := range in.Candidates {
		txn := p.Txn
		if txn.IsDeleted || txn.IsEstimate || counted[txn.ID] ||
			(txn.TransferPairID != "" && countedPairs[txn.TransferPairID]) {
			continue
		}
		if txn.Date.Before(from) || txn.Date.After(to) {
			continue
		}
		amount := txn.PrimaryAmount()
		switch kind {
		case GoalIn:
			if txn.AccountID != reserve || !amount.IsPositive() {
				continue
			}
		case GoalOut:
			if !amount.IsNegative() {
				continue
			}
			if txn.AccountID != reserve {
				if !funding[txn.AccountID] || !p.IsTransfer() ||
					containsID(pairAccounts[txn.TransferPairID], reserve) {
					continue
				}
			}
		case GoalSpent:
			if txn.AccountID == reserve || !amount.IsNegative() || p.IsTransfer() {
				continue
			}
		default:
			continue
		}

		suggestion := GoalSuggestion{Posting: p}
		if kind == GoalSpent {
			for _, id := range txn.CategoryIDs() {
				if categories[id] {
					suggestion.MatchesCategory = true
				}
			}
			suggestion.MatchesPayee = payees[nameKey(txn.DisplayPayee())]
		}
		for _, on := range withdrawals {
			days := DaysBetween(on, txn.Date)
			if days < 0 {
				days = -days
			}
			if !suggestion.HasWithdrawal || days < suggestion.DaysFromWithdrawal {
				suggestion.DaysFromWithdrawal, suggestion.HasWithdrawal = days, true
			}
		}
		out = append(out, suggestion)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if kind == GoalSpent {
			if a.score() != b.score() {
				return a.score() > b.score()
			}
			if a.HasWithdrawal && b.HasWithdrawal && a.DaysFromWithdrawal != b.DaysFromWithdrawal {
				return a.DaysFromWithdrawal < b.DaysFromWithdrawal
			}
		} else {
			// The reserve's own rows first, then transfers: a deposit that
			// is a transfer from the household is far likelier to be saving.
			if (a.Posting.Txn.AccountID == reserve) != (b.Posting.Txn.AccountID == reserve) {
				return a.Posting.Txn.AccountID == reserve
			}
			if a.Posting.IsTransfer() != b.Posting.IsTransfer() {
				return a.Posting.IsTransfer()
			}
		}
		if a.Posting.Txn.Date != b.Posting.Txn.Date {
			return a.Posting.Txn.Date.After(b.Posting.Txn.Date)
		}
		return a.Posting.Txn.ID < b.Posting.Txn.ID
	})
	return out
}

func containsID(ids []ID, want ID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
