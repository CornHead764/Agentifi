package domain

import "time"

// What a sync saw of a connected account, kept so the ledger can be compared
// with the bank over time rather than one sync at a time (calculations.md §3).

// BalanceObservation is one sync's view of an account: the figure the bank
// reported, the ledger's totals once the sync's rows were in, and how far the
// sync's own rows moved those totals. Ledger and Movement count pending rows;
// Settled and SettledMovement count posted rows only.
type BalanceObservation struct {
	At              time.Time
	Reported        Money
	Ledger          Money
	Settled         Money
	Movement        Money
	SettledMovement Money
}

// ObserveBalance is the observation a sync records once its rows are in.
// before and after are the rows its import could touch, as
// ExplainReportedBalance compares them; postings are all the account's rows.
func ObserveBalance(
	at time.Time, reported Money, before, after []Transaction, postings []Posting,
) BalanceObservation {
	movement, _ := ledgerMovement(before, after, CountsTowardBalance)
	settledMovement, _ := ledgerMovement(before, after, IsSettled)
	return BalanceObservation{
		At:              at,
		Reported:        reported,
		Ledger:          LedgerTotal(postings),
		Settled:         SettledTotal(postings),
		Movement:        movement.Round(),
		SettledMovement: settledMovement.Round(),
	}
}

// unexplained is how far the sync's rows moved the ledger past the bank since
// the observation before: the rows' movement less the change in the reported
// figure, under one reading of the figure.
func (o BalanceObservation) unexplained(before BalanceObservation, includesPending bool) Money {
	movement := o.SettledMovement
	if includesPending {
		movement = o.Movement
	}
	return movement.Sub(o.Reported.Sub(before.Reported)).Round()
}

// PendingReadingSyncs is how many deciding syncs in a row must agree before
// an account's reading changes.
const PendingReadingSyncs = 3

// PendingReading is whether the bank's figure counts pending rows, from the
// account's observations oldest first. A sync decides when exactly one reading
// explains the change since the observation before it. The newest
// PendingReadingSyncs deciding syncs must agree; otherwise current stands.
func PendingReading(current bool, observations []BalanceObservation) bool {
	agreed, seen := false, 0
	for i := len(observations) - 1; i > 0 && seen < PendingReadingSyncs; i-- {
		counting := observations[i].unexplained(observations[i-1], true).IsZero()
		leaving := observations[i].unexplained(observations[i-1], false).IsZero()
		if counting == leaving {
			continue
		}
		if seen > 0 && counting != agreed {
			return current
		}
		agreed = counting
		seen++
	}
	if seen < PendingReadingSyncs {
		return current
	}
	return agreed
}
