package domain

// A card charge's effective date is the due date of the bill it lands on: the
// bill's own due date where there is one, otherwise computed from the
// statement cycle (calculations.md §3, "Credit card fields").

// FirstDayAfter is the earliest date strictly after `after` that falls on
// dayOfMonth, clamped in each month.
func FirstDayAfter(dayOfMonth int, after Date) Date {
	if same := MonthOf(after).Day(dayOfMonth); same.After(after) {
		return same
	}
	return MonthOf(after).Next().Day(dayOfMonth)
}

// CycleCloseFor is the closing date of the statement a charge lands on. The
// close is strictly after the charge, so a charge on the closing day belongs
// to the next statement.
func CycleCloseFor(txnDate Date, statementCloseDay int) Date {
	return FirstDayAfter(statementCloseDay, txnDate)
}

// ComputeEffectiveDate is the first payment due day after the charge's
// statement closes, or the charge's own date when the cycle is not configured.
func ComputeEffectiveDate(txnDate Date, statementCloseDay, paymentDueDay int) Date {
	if statementCloseDay == 0 || paymentDueDay == 0 {
		return txnDate
	}
	return FirstDayAfter(paymentDueDay, CycleCloseFor(txnDate, statementCloseDay))
}

// EffectiveDateFor is the effective date to store for a row. Callers compare
// a stored value with a fresh result to tell a derived date from a hand
// correction, so this must stay a pure function of its arguments.
//
// A non-card row, and a card without both cycle days, stores the zero date:
// Transaction.ReportingDate already falls back to the posted date.
func EffectiveDateFor(
	txnDate Date,
	isCard bool,
	statementCloseDay, paymentDueDay int,
	billDueDate Date,
) Date {
	switch {
	case !isCard:
		return Date{}
	case !billDueDate.IsZero():
		return billDueDate
	case statementCloseDay == 0 || paymentDueDay == 0:
		return Date{}
	default:
		return ComputeEffectiveDate(txnDate, statementCloseDay, paymentDueDay)
	}
}
