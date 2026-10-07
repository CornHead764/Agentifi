package domain

// ReportedAmount is one allocation as report totals read it: a split, or a
// whole row when it has none. Kind is LedgerKind's answer for its category.
type ReportedAmount struct {
	Amount               Money
	Kind                 CategoryKind
	IsBillOrSubscription bool
}

// AllocationSummary is the totals row of a report and the Monthly Summary's
// figures (calculations.md §11).
type AllocationSummary struct {
	Income   Money
	Expenses Money
	Net      Money
	// Bills and Discretionary split Expenses by whether the allocation belongs
	// to a bill or subscription; the two sum to Expenses.
	Bills         Money
	Discretionary Money
	// SavingsRate is Net over Income, absent when nothing came in.
	SavingsRate    Rate
	HasSavingsRate bool
	Count          int
}

// SummarizeAllocations totals allocations by ledger kind, never by sign: a
// refund filed under a spending category lowers Expenses, and a clawed-back
// paycheck lowers Income. A transfer-kind allocation is neither.
func SummarizeAllocations(rows []ReportedAmount) AllocationSummary {
	income, bills, discretionary := Zero, Zero, Zero
	for _, one := range rows {
		switch {
		case one.Kind == CategoryIncome:
			income = income.Add(one.Amount)
		case one.Kind != CategoryExpense:
		case one.IsBillOrSubscription:
			bills = bills.Add(one.Amount)
		default:
			discretionary = discretionary.Add(one.Amount)
		}
	}
	expenses := bills.Add(discretionary)
	net := Total(income, expenses)
	rate, hasRate := Ratio(net, income.Round())
	return AllocationSummary{
		Income:         income.Round(),
		Expenses:       expenses.Round(),
		Net:            net,
		Bills:          bills.Round(),
		Discretionary:  discretionary.Round(),
		SavingsRate:    rate,
		HasSavingsRate: hasRate,
		Count:          len(rows),
	}
}

// OccurrenceSummary is the Bills & Income overview's figures over expected
// occurrences.
type OccurrenceSummary struct {
	Income   Money
	Expenses Money
	Net      Money
}

// SummarizeOccurrences totals occurrences by their series' kind, as the
// spending plan's Income and Bills buckets do (calculations.md §5): income
// series are Income, a transfer or card payment nets to zero, and every other
// kind, an expected refund included, counts against Expenses with its sign.
func SummarizeOccurrences(occurrences []Occurrence) OccurrenceSummary {
	income, expenses := Zero, Zero
	for _, one := range occurrences {
		switch {
		case one.Kind == SeriesIncome:
			income = income.Add(one.Amount)
		case one.Kind.NetsToZero():
		default:
			expenses = expenses.Add(one.Amount)
		}
	}
	return OccurrenceSummary{
		Income:   income.Round(),
		Expenses: expenses.Round(),
		Net:      Total(income, expenses),
	}
}
