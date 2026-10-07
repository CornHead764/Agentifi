package domain

// The Spending report's period in progress carried to its end
// (calculations.md §11, "Spending report"): what it has counted through
// today plus the recurring occurrences still expected before it closes. A
// paycheck due on the 30th belongs in the month's savings rate on the 3rd.

// ProjectionScope is what the report reads, so an occurrence counts in the
// projection exactly when its posting would count in the report.
type ProjectionScope struct {
	// Accounts are the accounts the report reads; an occurrence on any other
	// is left out.
	Accounts map[ID]Account
	// Series and Categories resolve an occurrence's category.
	Series     map[ID]Series
	Categories map[ID]Category
	// Currency is the household's: a series in another has no figure here.
	Currency string
}

func (s ProjectionScope) counts(one Occurrence) bool {
	account, ok := s.Accounts[one.AccountID]
	if !ok || account.ExcludedFromReports {
		return false
	}
	series := s.Series[one.SeriesID]
	if series.Currency != "" && s.Currency != "" && series.Currency != s.Currency {
		return false
	}
	if category, filed := s.Categories[series.CategoryID]; filed && !category.CountsAsIncomeOrExpense() {
		return false
	}
	return true
}

// SpendingProjection is the period in progress as it is expected to close.
type SpendingProjection struct {
	// End is the period's last day.
	End Date
	// ExpectedIncome and ExpectedSpent are the occurrences still to come,
	// ExpectedSpent with the ledger's sign (negative is spending).
	ExpectedIncome Money
	ExpectedSpent  Money
	// Count is how many occurrences the expected figures hold.
	Count int
	// Summary is the cards with the expected figures added to the actual
	// ones.
	Summary SpendingSummary
}

// ProjectSpending carries a partial window to its period's end. `expected`
// is the occurrences no transaction has settled (ExpectedOccurrences), so one
// already paid is counted once, by its posting. An occurrence counts when the
// day its money moves (Occurrence.MovesOn) falls from the window's last day,
// today, through the period's end and the scope reads it; its series' kind
// files it, as SummarizeOccurrences does, and a transfer or card payment is
// not counted at all. A whole window has nothing left to expect, and answers
// false.
func ProjectSpending(
	actual SpendingSummary, window SpendingWindow, expected []Occurrence, scope ProjectionScope,
) (SpendingProjection, bool) {
	if !window.Partial {
		return SpendingProjection{}, false
	}
	end := window.Period.End()
	due := make([]Occurrence, 0, len(expected))
	for _, one := range expected {
		on := one.MovesOn()
		if one.Kind.NetsToZero() || on.Before(window.Through) || on.After(end) || !scope.counts(one) {
			continue
		}
		due = append(due, one)
	}
	totals := SummarizeOccurrences(due)
	return SpendingProjection{
		End:            end,
		ExpectedIncome: totals.Income,
		ExpectedSpent:  totals.Expenses,
		Count:          len(due),
		Summary:        SummarizeSpending(actual.Income.Add(totals.Income), actual.Spent.Add(totals.Expenses)),
	}, true
}
