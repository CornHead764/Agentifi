package domain

import (
	"cmp"
	"slices"
)

// A bill payment is a provider's own record of money it received: a day and
// an amount, as a medical portal lists them. Matching one to the bank row that
// carried it is by money first and time second, like a merchant order, but
// stricter: a copay-sized figure is common, and a receipt filed on the wrong
// row is worse than none. So a payment and a row are each other's only when
// neither has another candidate.

type BillPaymentFacts struct {
	Ref    string
	PaidOn Date
	// Amount is the magnitude the provider received.
	Amount Money
}

type BillPaymentRowFacts struct {
	Ref string
	On  Date
	// Amount is signed as the bank sees it: money leaving is negative.
	Amount Money
}

// A card charged in a portal posts that day or within the week; a portal that
// dates a payment by the day it processed it can be a day or two behind the
// card.
const (
	billPaymentDaysBefore = 2
	billPaymentDaysAfter  = 5
)

// BillPaymentSpan is the bank dates a payment made on paidOn can post on.
func BillPaymentSpan(paidOn Date) (from, to Date) {
	return paidOn.AddDays(-billPaymentDaysBefore), paidOn.AddDays(billPaymentDaysAfter)
}

// RowCouldBeBillPayment says a row could be the payment: money out of exactly
// the amount paid, posted within its span.
func RowCouldBeBillPayment(payment BillPaymentFacts, row BillPaymentRowFacts) bool {
	if !payment.Amount.IsPositive() || !row.Amount.Neg().Equal(payment.Amount) {
		return false
	}
	from, to := BillPaymentSpan(payment.PaidOn)
	return row.On.NotBefore(from) && row.On.NotAfter(to)
}

// MatchBillPayments is each payment's bank row, by Ref. A pair is made only
// when the payment has that one candidate row and the row that one candidate
// payment; whatever is ambiguous matches nothing, because the payment whose
// row looks forced may have been paid from an account the bank feed never
// sees. The rows offered must already exclude those another payment holds.
func MatchBillPayments(payments []BillPaymentFacts, rows []BillPaymentRowFacts) map[string]string {
	out := map[string]string{}
	for _, payment := range payments {
		row, only := onlyCandidateRow(payment, rows)
		if only && onlyCandidatePayment(row, payments) {
			out[payment.Ref] = row.Ref
		}
	}
	return out
}

func onlyCandidateRow(payment BillPaymentFacts, rows []BillPaymentRowFacts) (BillPaymentRowFacts, bool) {
	var found BillPaymentRowFacts
	count := 0
	for _, row := range rows {
		if RowCouldBeBillPayment(payment, row) {
			found = row
			count++
		}
	}
	return found, count == 1
}

func onlyCandidatePayment(row BillPaymentRowFacts, payments []BillPaymentFacts) bool {
	count := 0
	for _, payment := range payments {
		if RowCouldBeBillPayment(payment, row) {
			count++
		}
	}
	return count == 1
}

// BillIssueFacts is one statement as StatementsPaidBy reads it.
type BillIssueFacts struct {
	ID       ID
	IssuedOn Date
}

// StatementsPaidBy is what a payment settled: the statements issued on the
// latest day on or before the day it was paid, several when the provider
// issued several that day. A payment made before any statement, such as a
// copay at the front desk, settled none.
func StatementsPaidBy(paidOn Date, statements []BillIssueFacts) []ID {
	var latest Date
	for _, one := range statements {
		if one.IssuedOn.IsZero() || one.IssuedOn.After(paidOn) {
			continue
		}
		if latest.IsZero() || one.IssuedOn.After(latest) {
			latest = one.IssuedOn
		}
	}
	if latest.IsZero() {
		return nil
	}
	var out []ID
	for _, one := range statements {
		if one.IssuedOn == latest {
			out = append(out, one.ID)
		}
	}
	slices.SortFunc(out, func(a, b ID) int { return cmp.Compare(a, b) })
	return out
}
