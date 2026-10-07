package domain

import "sort"

// Revaluing a physical asset: the arithmetic between an outside estimate
// arriving and the ledger moving. A revaluation is not spending; it is written
// as SourceBalanceAdjustment (see Source.IsCashFlow).

// ProjectedMileage ages an odometer reading forward to a date, linearly, since
// the estimate is most sensitive to mileage. Never backwards: a reading dated
// after the target is returned unchanged. A milesPerYear of zero is real (a
// car in storage), hence the flag rather than a sentinel.
func ProjectedMileage(reading int, readOn Date, milesPerYear int, hasRate bool, on Date) int {
	if !hasRate || readOn.IsZero() || on.Before(readOn) {
		return reading
	}
	days := DaysBetween(readOn, on)
	if days <= 0 {
		return reading
	}
	return reading + milesPerYear*days/365
}

// RevaluationAmount is the adjustment from what the ledger says an asset is
// worth to what the estimate says. False when they agree, so no zero rows are
// written.
func RevaluationAmount(current, estimate Money) (Money, bool) {
	delta := estimate.Sub(current)
	if delta.IsZero() {
		return Money{}, false
	}
	return delta, true
}

// Revaluation is what moving an asset to an estimate writes.
type Revaluation struct {
	// Adjustment is the SourceBalanceAdjustment row, dated the estimate's day.
	Adjustment Money
	// Anchor is the provider balance the account moves to when MovesAnchor is
	// set. An account with a provider balance reads its current balance from
	// that figure and walks its past backwards from it, so the row alone
	// would leave today's value where it was and push every earlier day down
	// by the adjustment. Moving the anchor by the same amount the row adds
	// keeps every day before the row where it was.
	Anchor      Money
	MovesAnchor bool
}

// RevaluationOf is how an asset moves from its current balance to an
// estimate: a row for the difference, and for an account with a provider
// balance the anchor set to the estimate. False when they already agree.
func RevaluationOf(account Account, postings []Posting, estimate Money) (Revaluation, bool) {
	adjustment, moved := RevaluationAmount(AccountBalance(account, postings), estimate)
	if !moved {
		return Revaluation{}, false
	}
	out := Revaluation{Adjustment: adjustment}
	if account.HasProviderBalance {
		out.Anchor, out.MovesAnchor = estimate, true
	}
	return out, true
}

// StaleValuation reports whether an asset is due to be re-priced: always when
// never valued, otherwise once maxAgeDays have passed since the last
// valuation (not the last sync).
func StaleValuation(valuedOn Date, today Date, maxAgeDays int) bool {
	if valuedOn.IsZero() {
		return true
	}
	return valuedOn.AddDays(maxAgeDays).NotAfter(today)
}

// ----- value history ---------------------------------------------------------

// DatedAmount is one row's contribution to a balance.
type DatedAmount struct {
	On     Date
	Amount Money
}

// ValuationAdjustments turns a value history into the rows that reproduce it.
// The balance is a sum of rows, so values become differences: each point's
// adjustment is what the running balance (existing rows on or before it plus
// adjustments already emitted) is short of that day's value. Computing each
// against the untouched opening balance would restate the total at every
// point. Points needing no correction produce no row, so re-importing the same
// file adds nothing.
func ValuationAdjustments(opening Money, existing []DatedAmount, points []ValuePoint) []DatedAmount {
	sorted := make([]ValuePoint, len(points))
	copy(sorted, points)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].On.Before(sorted[j].On) })

	ledger := make([]DatedAmount, len(existing))
	copy(ledger, existing)
	sort.SliceStable(ledger, func(i, j int) bool { return ledger[i].On.Before(ledger[j].On) })

	var out []DatedAmount
	running := opening
	next := 0
	for _, point := range sorted {
		for next < len(ledger) && ledger[next].On.NotAfter(point.On) {
			running = running.Add(ledger[next].Amount)
			next++
		}
		delta, moved := RevaluationAmount(running, point.Value)
		if !moved {
			continue
		}
		out = append(out, DatedAmount{On: point.On, Amount: delta})
		running = running.Add(delta)
	}
	return out
}
