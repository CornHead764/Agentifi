package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// calculations.md §9, "Is this it?". A monthly rent of 1,500.00 on the 1st,
// exact band, so the window is 3 days early to 5 late: 29 August to
// 6 September for the 1 September slot.

func paymentRow(id, amount string, on Date, mods ...func(*PaymentRow)) PaymentRow {
	row := PaymentRow{Posting: Posting{Txn: Transaction{
		ID: ID(id), AccountID: "acct-1", Date: on, Amount: MustFromString(amount),
		Currency: "USD", Source: SourceSync, StatementName: "SOMETHING ELSE ENTIRELY",
	}}}
	for _, mod := range mods {
		mod(&row)
	}
	return row
}

func candidateIDs(found []Transaction) []ID {
	out := []ID{}
	for _, one := range found {
		out = append(out, one.ID)
	}
	return out
}

func TestACandidateIsAnyUnlinkedRowInTheWindowAndBandWhateverItsWording(t *testing.T) {
	slot := NewDate(2026, time.September, 1)
	rows := []PaymentRow{
		paymentRow("in-window", "-1500.00", NewDate(2026, time.September, 2)),
		paymentRow("three-early", "-1500.00", NewDate(2026, time.August, 29)),
		paymentRow("five-late", "-1500.00", NewDate(2026, time.September, 6)),
		paymentRow("four-early", "-1500.00", NewDate(2026, time.August, 28)),
		paymentRow("six-late", "-1500.00", NewDate(2026, time.September, 7)),
	}
	got := PaymentCandidates(matchTestContext(), slot, rows)
	require.Equal(t, []ID{"in-window", "three-early", "five-late"}, candidateIDs(got),
		"nearest the slot first; the wording shares nothing with the series")
}

func TestACandidateMustBeFreeToBeThePayment(t *testing.T) {
	slot := NewDate(2026, time.September, 1)
	on := NewDate(2026, time.September, 2)
	rows := []PaymentRow{
		paymentRow("linked", "-1500.00", on, func(r *PaymentRow) { r.InSeries = true }),
		paymentRow("transfer-leg", "-1500.00", on, func(r *PaymentRow) { r.Txn.TransferPairID = "pair-1" }),
		paymentRow("transfer-category", "-1500.00", on, func(r *PaymentRow) {
			r.Category, r.HasCategory = Category{Kind: CategoryTransfer}, true
		}),
		paymentRow("out-of-reports", "-1500.00", on, func(r *PaymentRow) { r.Txn.ExcludedFromReports = true }),
		paymentRow("out-of-plan", "-1500.00", on, func(r *PaymentRow) { r.Txn.ExcludedFromSpendingPlan = true }),
		paymentRow("deleted", "-1500.00", on, func(r *PaymentRow) { r.Txn.IsDeleted = true }),
		paymentRow("forecast", "-1500.00", on, func(r *PaymentRow) { r.Txn.IsEstimate = true }),
		paymentRow("bookkeeping", "-1500.00", on, func(r *PaymentRow) { r.Txn.Source = SourceBalanceAdjustment }),
		paymentRow("other-account", "-1500.00", on, func(r *PaymentRow) { r.Txn.AccountID = "acct-2" }),
		paymentRow("other-currency", "-1500.00", on, func(r *PaymentRow) { r.Txn.Currency = "EUR" }),
		paymentRow("refund", "1500.00", on),
		paymentRow("outside-band", "-1499.99", on),
		paymentRow("free", "-1500.00", on),
	}
	got := PaymentCandidates(matchTestContext(), slot, rows)
	require.Equal(t, []ID{"free"}, candidateIDs(got))
}

func TestCandidatesTheSameDayApartAreOrderedByAmount(t *testing.T) {
	// An any-amount band admits both; 1,490.00 is nearer 1,500.00 than 1,400.00.
	context := matchTestContext()
	context.Tolerance = AnyAmount()
	slot := NewDate(2026, time.September, 1)
	rows := []PaymentRow{
		paymentRow("far", "-1400.00", NewDate(2026, time.September, 2)),
		paymentRow("near", "-1490.00", NewDate(2026, time.August, 31)),
	}
	require.Equal(t, []ID{"near", "far"}, candidateIDs(PaymentCandidates(context, slot, rows)))
}
