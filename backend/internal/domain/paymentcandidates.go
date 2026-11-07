package domain

import (
	"cmp"
	"slices"
)

// PaymentRow is a row that may be offered as the payment for an occurrence a
// person marked paid.
type PaymentRow struct {
	Posting
	// InSeries is a row already filed under an occurrence of some series.
	InSeries bool
}

// PaymentCandidates is the rows that could be the payment for one occurrence
// marked paid: unlinked rows in the series' account and direction, inside the
// slot's window and amount band, wording ignored. Nearest the slot first.
// calculations.md §9, "Is this it?".
func PaymentCandidates(context MatchContext, occurrenceOn Date, rows []PaymentRow) []Transaction {
	series := context.Series
	expected := context.Expected(occurrenceOn)
	before, after := MatchWindow(series.Recurrence)
	earliest, latest := occurrenceOn.AddDays(-before), occurrenceOn.AddDays(after)

	var out []Transaction
	for _, row := range rows {
		txn := row.Txn
		switch {
		case row.InSeries, txn.IsDeleted, txn.IsEstimate, !txn.Source.IsCashFlow():
			continue
		case row.IsTransfer(), txn.ExcludedFromReports, txn.ExcludedFromSpendingPlan:
			continue
		case txn.AccountID != series.AccountID, txn.Currency != series.Currency:
			continue
		case !sameSignedDirection(txn.Amount, expected):
			continue
		case txn.Date.Before(earliest), txn.Date.After(latest):
			continue
		case !context.Tolerance.Accepts(expected, txn.Amount, context.ObservedAmounts):
			continue
		}
		out = append(out, txn)
	}
	slices.SortStableFunc(out, func(a, b Transaction) int {
		return cmp.Or(
			cmp.Compare(abs(DaysBetween(occurrenceOn, a.Date)), abs(DaysBetween(occurrenceOn, b.Date))),
			a.Amount.Sub(expected).Abs().Cmp(b.Amount.Sub(expected).Abs()),
			cmp.Compare(a.Date.String(), b.Date.String()),
			cmp.Compare(a.ID, b.ID),
		)
	})
	return out
}
