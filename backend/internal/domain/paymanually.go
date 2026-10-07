package domain

import "cmp"

// A billed account no autopay takes is paid by hand, so its newest unpaid
// statement is a one-off reminder until it is paid (calculations.md §9,
// "Pay-manually reminders"). Only the newest: a provider that carries an
// unpaid balance forward restates it on every statement, so the older ones
// are not separate debts.

// BilledAccountFacts is one billed account as PayManually reads it.
type BilledAccountFacts struct {
	ID ID
	// Autopay is its connection's rule.
	Autopay AutopayRule
	// Linked says a recurring reminder carries its bills. That reminder is
	// the account's, so no one-off one is made beside it.
	Linked     bool
	Statements []StatementFacts
	// PaidOn is the day of each payment the provider lists that a bank row
	// was paired with (MatchBillPayments).
	PaidOn []Date
}

// StatementFacts is one statement as PayManually reads it.
type StatementFacts struct {
	ID ID
	// DueOn is the printed due date, or the issue date where none is printed.
	DueOn    Date
	IssuedOn Date
	// AutopayOn is the provider's own stated payment day; zero when it states
	// none.
	AutopayOn Date
	// AmountDue is the magnitude owed.
	AmountDue Money
	Status    BillStatus
	// MarkedPaid is a person's word that it was paid where the ledger cannot
	// see.
	MarkedPaid bool
}

// ManualBill is one pay-manually reminder.
type ManualBill struct {
	BillID       ID
	SubaccountID ID
	DueOn        Date
	// Amount is signed like a posting: negative, money out.
	Amount Money
}

// PayManually is every billed account's pay-manually reminder: its newest
// standing statements (several when the provider issued several that day),
// each one open, owing more than zero, stating no autopay day, not marked
// paid, and settled by no paired payment (StatementsPaidBy). An account
// whose connection autopays, or that a recurring reminder carries, has none.
func PayManually(accounts []BilledAccountFacts) []ManualBill {
	var out []ManualBill
	for _, account := range accounts {
		if account.Linked || account.Autopay.Kind != AutopayNone {
			continue
		}
		issues := make([]BillIssueFacts, 0, len(account.Statements))
		for _, one := range account.Statements {
			issues = append(issues, BillIssueFacts{ID: one.ID, IssuedOn: one.IssuedOn})
		}
		settled := map[ID]bool{}
		for _, paid := range account.PaidOn {
			for _, id := range StatementsPaidBy(paid, issues) {
				settled[id] = true
			}
		}
		for _, one := range newestStatements(account.Statements) {
			if one.Status != BillOpen || !one.AmountDue.IsPositive() || !one.AutopayOn.IsZero() ||
				one.MarkedPaid || settled[one.ID] {
				continue
			}
			out = append(out, ManualBill{
				BillID: one.ID, SubaccountID: account.ID, DueOn: one.DueOn, Amount: one.AmountDue.Neg(),
			})
		}
	}
	return out
}

// newestStatements is the statements of the latest day, by issue date where
// the provider states one and due date where it does not. A superseded
// statement is never the newest.
func newestStatements(statements []StatementFacts) []StatementFacts {
	var latest Date
	for _, one := range statements {
		if one.Status == BillSuperseded {
			continue
		}
		if on := statementDay(one); latest.IsZero() || on.After(latest) {
			latest = on
		}
	}
	var out []StatementFacts
	for _, one := range statements {
		if one.Status != BillSuperseded && statementDay(one) == latest {
			out = append(out, one)
		}
	}
	return out
}

func statementDay(one StatementFacts) Date {
	return cmp.Or(one.IssuedOn, one.DueOn)
}
