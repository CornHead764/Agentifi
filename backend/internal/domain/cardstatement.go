package domain

// A card's statement figures. SimpleFIN reports only a balance, so these are
// copied facts (typed, from a connector, or from a filed bill), never derived.
// What is decided here is only whether a bill's copy replaces what is held.

// HasStatement says an account of this kind is billed by statement (a card or
// a loan), so a billed account may be linked to it.
func HasStatement(kind AccountKind) bool {
	return kind == KindCreditCard || kind == KindLoan
}

// CardStatement is the statement figures an account holds, and whether a bill
// wrote them.
type CardStatement struct {
	// Balance is the statement balance, owed as a magnitude.
	Balance    Money
	HasBalance bool
	// MinimumDue is the minimum payment, owed as a magnitude.
	MinimumDue    Money
	HasMinimumDue bool
	DueOn         Date
	// FromBill is false for figures typed in, reported by a connector, or
	// absent.
	FromBill bool
}

// StatementBill is what one filed bill says about a card's statement.
type StatementBill struct {
	// AmountDue is the statement balance.
	AmountDue     Money
	MinimumDue    Money
	HasMinimumDue bool
	DueOn         Date
}

// StatementAfterBill is what an account's statement becomes when a bill for it
// is filed, and whether that changed anything. The cycle is the due date:
//
//   - A later due date (or none held) replaces all three figures. A minimum
//     the bill omits is cleared: last cycle's minimum beside this cycle's due
//     date is a wrong number.
//   - The same due date refreshes only figures a bill wrote; a hand edit for
//     that cycle stands, as the user's correction of the mail. Here an omitted
//     minimum keeps what was held.
//   - An earlier due date never writes: it is an old statement arriving late.
//
// A bill with no due date writes nothing.
func StatementAfterBill(held CardStatement, bill StatementBill) (CardStatement, bool) {
	if bill.DueOn.IsZero() {
		return held, false
	}
	newer := held.DueOn.IsZero() || bill.DueOn.After(held.DueOn)
	sameCycle := bill.DueOn == held.DueOn
	if !newer && !(sameCycle && held.FromBill) {
		return held, false
	}
	next := CardStatement{
		Balance: bill.AmountDue.Abs(), HasBalance: true,
		DueOn: bill.DueOn, FromBill: true,
	}
	switch {
	case bill.HasMinimumDue:
		next.MinimumDue, next.HasMinimumDue = bill.MinimumDue.Abs(), true
	case !newer:
		next.MinimumDue, next.HasMinimumDue = held.MinimumDue, held.HasMinimumDue
	}
	changed := !held.FromBill || held.DueOn != next.DueOn ||
		held.HasBalance != next.HasBalance || !held.Balance.Equal(next.Balance) ||
		held.HasMinimumDue != next.HasMinimumDue || !held.MinimumDue.Equal(next.MinimumDue)
	return next, changed
}
