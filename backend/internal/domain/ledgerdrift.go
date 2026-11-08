package domain

import "time"

// Ledger drift: a connected account whose own rows have stopped adding up to
// the bank's balance (calculations.md §3, "Ledger drift"). The balance itself
// is always the bank's, so a missing or doubled row shows nowhere else: it
// silently moves every earlier balance in the walk back.

const (
	// LedgerDriftSettleDays is how old an observation must be before it is
	// measured: banks post a row and reflect it in the balance days apart.
	LedgerDriftSettleDays = 7
	// LedgerDriftMinDays and LedgerDriftMinSyncs are how long, and over how
	// many syncs, the same drift must stand among the settled observations.
	LedgerDriftMinDays  = 3
	LedgerDriftMinSyncs = 2
)

// DriftSuspect is what a drift's amount says about one row.
type DriftSuspect string

const (
	// SuspectDuplicate: the drift is the row's amount, so the row is
	// probably counted twice.
	SuspectDuplicate DriftSuspect = "duplicate"
	// SuspectMissing: the drift is the row's amount negated, so a row like
	// it is probably missing.
	SuspectMissing DriftSuspect = "missing"
)

// Drift is a standing difference between an account's rows and its bank.
type Drift struct {
	// Amount is how far the rows have moved past the bank's balance: negative
	// when they come to less than it.
	Amount Money
	// Since is the sync that first saw it.
	Since time.Time
	// Suspect and SuspectID name the one row the amount points at; Suspect is
	// empty when no single row does.
	Suspect   DriftSuspect
	SuspectID ID
}

// LedgerDrift is the account's standing drift, false when there is none.
// observations are oldest first; postings are the account's rows as they
// stand now. Every amount is read under the account's pending reading.
func LedgerDrift(
	account Account, observations []BalanceObservation, postings []Posting, now time.Time,
) (Drift, bool) {
	if !account.HasProviderBalance || len(observations) < 2 {
		return Drift{}, false
	}
	counting := account.BankIncludesPending
	drift := ledgerDriftSeries(observations, counting)

	last := len(observations) - 1
	current := drift[last]
	if !current.IsZero() {
		current = current.Add(observations[last].ledgerTotal(counting).Neg()).Add(
			balanceSumIf(postings, account.InBalance)).Round()
	}
	if current.IsZero() {
		return Drift{}, false
	}

	settledBy := now.AddDate(0, 0, -LedgerDriftSettleDays)
	end := last
	for end >= 0 && observations[end].At.After(settledBy) {
		end--
	}
	if end < 1 || drift[end].IsZero() {
		return Drift{}, false
	}
	amount := drift[end]
	first := end
	for first > 0 && drift[first-1].Equal(amount) {
		first--
	}
	span := observations[end].At.Sub(observations[first].At)
	if end-first+1 < LedgerDriftMinSyncs || span < LedgerDriftMinDays*24*time.Hour {
		return Drift{}, false
	}

	out := Drift{Amount: amount, Since: observations[first].At}
	from := observations[first-1].At.AddDate(0, 0, -LedgerDriftSettleDays)
	out.Suspect, out.SuspectID = driftSuspect(account, postings, amount, from, out.Since)
	return out, true
}

// ledgerDriftSeries is the drift standing after each observation. It starts
// at zero and moves by what each sync's rows leave unexplained. Edits between
// syncs move it only while it is non-zero, so fixing a drift clears it while
// tidying a ledger that already agreed raises none.
func ledgerDriftSeries(observations []BalanceObservation, counting bool) []Money {
	out := make([]Money, len(observations))
	out[0] = Zero
	for k := 1; k < len(observations); k++ {
		this, before := observations[k], observations[k-1]
		next := out[k-1].Add(this.unexplained(before, counting))
		if !out[k-1].IsZero() {
			edited := this.ledgerTotal(counting).Sub(this.movement(counting)).Sub(before.ledgerTotal(counting))
			next = next.Add(edited)
		}
		out[k] = next.Round()
	}
	return out
}

func (o BalanceObservation) ledgerTotal(counting bool) Money {
	if counting {
		return o.Ledger
	}
	return o.Settled
}

func (o BalanceObservation) movement(counting bool) Money {
	if counting {
		return o.Movement
	}
	return o.SettledMovement
}

// driftSuspect is the one row inside the balance, added between from and
// through, whose amount is the drift (a duplicate) or its negation (a row like
// it is missing). Nothing when no single row fits.
func driftSuspect(
	account Account, postings []Posting, amount Money, from, through time.Time,
) (DriftSuspect, ID) {
	var doubled, twins []ID
	for _, posting := range postings {
		added := posting.Txn.AddedAt
		if !account.InBalance(posting) || !added.After(from) || added.After(through) {
			continue
		}
		switch moved := posting.NativeAmount().Round(); {
		case moved.Equal(amount):
			doubled = append(doubled, posting.Txn.ID)
		case moved.Equal(amount.Neg()):
			twins = append(twins, posting.Txn.ID)
		}
	}
	switch {
	case len(doubled) == 1 && len(twins) == 0:
		return SuspectDuplicate, doubled[0]
	case len(twins) == 1 && len(doubled) == 0:
		return SuspectMissing, twins[0]
	}
	return "", ""
}
