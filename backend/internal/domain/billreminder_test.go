package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func paidBill(year int, month time.Month, day int, amount string) BillHistoryEntry {
	return BillHistoryEntry{DueOn: NewDate(year, month, day), Amount: MustFromString(amount), Status: BillPaid}
}

func TestARegularMonthlyBillSuggestsItsDayAndAmount(t *testing.T) {
	var bills []BillHistoryEntry
	for month := time.January; month <= time.June; month++ {
		bills = append(bills, paidBill(2026, month, 15, "80.00"))
	}
	got, ok := SuggestBillReminder(bills, nil, NewDate(2026, time.July, 2))
	require.True(t, ok)
	require.Equal(t, EveryMonth(15), got.Recurrence)
	require.Equal(t, NewDate(2026, time.July, 15), got.StartOn)
	require.Equal(t, "-80.00", got.Amount.String())
	require.Equal(t, CriteriaExact, got.Tolerance.Criteria)
	require.False(t, got.AmountVaries)
	require.True(t, got.Confident)
	require.Equal(t, 6, got.DueDates)
	require.Empty(t, got.AccountID, "no bank rows, so no account to name")
}

func TestAnAnnualBillSuggestsAYearlyReminder(t *testing.T) {
	bills := []BillHistoryEntry{
		paidBill(2023, time.March, 10, "240.00"),
		paidBill(2024, time.March, 12, "255.00"),
		paidBill(2025, time.March, 10, "240.00"),
	}
	got, ok := SuggestBillReminder(bills, nil, NewDate(2025, time.June, 1))
	require.True(t, ok)
	require.Equal(t, EveryYear(), got.Recurrence)
	require.Equal(t, NewDate(2026, time.March, 10), got.StartOn)
	require.Equal(t, "-240.00", got.Amount.String())
	require.True(t, got.AmountVaries)
	require.True(t, got.Confident)
}

// lawnSeason is two seasons of visits on about the 8th, April to October.
// One day in May carries two invoices, and October's bill is still open.
func lawnSeason() []BillHistoryEntry {
	var bills []BillHistoryEntry
	for _, on := range []Date{
		NewDate(2025, time.April, 8), NewDate(2025, time.May, 8), NewDate(2025, time.June, 9),
		NewDate(2025, time.July, 8), NewDate(2025, time.August, 8), NewDate(2025, time.September, 8),
		NewDate(2025, time.October, 8),
		NewDate(2026, time.April, 8), NewDate(2026, time.May, 8), NewDate(2026, time.June, 8),
		NewDate(2026, time.July, 8), NewDate(2026, time.August, 10), NewDate(2026, time.September, 8),
	} {
		bills = append(bills, paidBill(on.Year, on.Month, on.Day, "45.00"))
	}
	bills = append(bills,
		paidBill(2026, time.May, 8, "30.00"),
		BillHistoryEntry{DueOn: NewDate(2026, time.October, 7), Amount: MustFromString("45.00"), Status: BillOpen},
		BillHistoryEntry{DueOn: NewDate(2026, time.September, 30), Amount: MustFromString("45.00"), Status: BillSuperseded},
	)
	return bills
}

func TestSeasonalBillsSuggestAMonthlyReminderActiveAprilToOctober(t *testing.T) {
	got, ok := SuggestBillReminder(lawnSeason(), nil, NewDate(2026, time.October, 2))
	require.True(t, ok)

	want := EveryMonth(8)
	want.ByMonth = []time.Month{
		time.April, time.May, time.June, time.July, time.August, time.September, time.October,
	}
	require.Equal(t, want, got.Recurrence)
	require.True(t, got.Confident)
	// Two invoices on one day are one visit; the superseded bill is no visit.
	require.Equal(t, 14, got.DueDates)
	require.Equal(t, NewDate(2025, time.April, 8), got.FirstDue)
	require.Equal(t, NewDate(2026, time.October, 7), got.LastDue)
	require.Equal(t, "-45.00", got.Amount.String())
	require.True(t, got.AmountVaries, "the two-invoice day asked for 75.00")
	require.Equal(t, NewDate(2026, time.October, 8), got.StartOn,
		"the slot the open bill due 7 October claims")
}

func TestTheBankRowsThatPaidTheBillsNameTheAccountAndTheWording(t *testing.T) {
	payments := []BillPaymentCandidate{
		{ID: "p-may", AccountID: "checking", CategoryID: "lawn", On: NewDate(2026, time.May, 9),
			Amount: MustFromString("-75.00"), StatementName: "GREEN LAWN CO 5521"},
		{ID: "p-jun", AccountID: "checking", CategoryID: "lawn", On: NewDate(2026, time.June, 9),
			Amount: MustFromString("-45.00"), StatementName: "GREEN LAWN CO 5602"},
		{ID: "p-jul", AccountID: "card", On: NewDate(2026, time.July, 8),
			Amount: MustFromString("-45.00"), StatementName: "GREEN LAWN WEB"},
		{ID: "p-dec", AccountID: "checking", On: NewDate(2026, time.December, 15),
			Amount: MustFromString("-45.00"), StatementName: "GREEN LAWN CO 6001"},
		{ID: "p-refund", AccountID: "checking", On: NewDate(2026, time.August, 11),
			Amount: MustFromString("45.00"), StatementName: "GREEN LAWN CO REFUND"},
		{ID: "p-sep", AccountID: "checking", CategoryID: "lawn", On: NewDate(2026, time.September, 7),
			Amount: MustFromString("-45.00"), StatementName: "GREEN LAWN CO 5788"},
	}
	got, ok := SuggestBillReminder(lawnSeason(), payments, NewDate(2026, time.October, 2))
	require.True(t, ok)
	require.Equal(t, []ID{"p-may", "p-jun", "p-jul", "p-sep"}, got.PaymentIDs)
	require.Equal(t, 4, got.PaymentsFound)
	require.Equal(t, ID("checking"), got.AccountID)
	require.Equal(t, "GREEN LAWN CO 5788", got.Description)
	require.Equal(t, ID("lawn"), got.CategoryID)
	require.Empty(t, got.PaidBySeries)
}

func TestPaymentsAlreadyInASeriesNameIt(t *testing.T) {
	var bills []BillHistoryEntry
	var payments []BillPaymentCandidate
	for month := time.January; month <= time.April; month++ {
		bills = append(bills, paidBill(2026, month, 20, "61.00"))
		payments = append(payments, BillPaymentCandidate{
			ID: ID("p-" + month.String()), AccountID: "checking", SeriesID: "ser-water",
			On: NewDate(2026, month, 21), Amount: MustFromString("-61.00"), StatementName: "CITY WATER",
		})
	}
	got, ok := SuggestBillReminder(bills, payments, NewDate(2026, time.May, 1))
	require.True(t, ok)
	require.Equal(t, ID("ser-water"), got.PaidBySeries)
}

func TestVisitsSixWeeksApartInASeasonAreALowConfidenceMonthlyGuess(t *testing.T) {
	var bills []BillHistoryEntry
	for _, on := range []Date{
		NewDate(2025, time.April, 10), NewDate(2025, time.May, 22), NewDate(2025, time.July, 3),
		NewDate(2025, time.August, 14), NewDate(2025, time.September, 25),
		NewDate(2026, time.April, 9), NewDate(2026, time.May, 21), NewDate(2026, time.July, 2),
		NewDate(2026, time.August, 13), NewDate(2026, time.September, 24),
	} {
		bills = append(bills, paidBill(on.Year, on.Month, on.Day, "52.00"))
	}
	got, ok := SuggestBillReminder(bills, nil, NewDate(2026, time.October, 2))
	require.True(t, ok)
	require.False(t, got.Confident)

	want := EveryMonth(14)
	want.ByMonth = []time.Month{
		time.April, time.May, time.June, time.July, time.August, time.September,
	}
	require.Equal(t, want, got.Recurrence, "June has no bill some years and is still in season")
	require.Equal(t, NewDate(2027, time.April, 14), got.StartOn)
}

func TestIrregularBillsSuggestNothing(t *testing.T) {
	bills := []BillHistoryEntry{
		paidBill(2026, time.January, 3, "19.00"),
		paidBill(2026, time.January, 13, "240.00"),
		paidBill(2026, time.April, 20, "75.00"),
		paidBill(2026, time.May, 2, "12.50"),
		paidBill(2026, time.September, 30, "310.00"),
	}
	_, ok := SuggestBillReminder(bills, nil, NewDate(2026, time.October, 2))
	require.False(t, ok)
}

func TestTwoDueDatesAreTooFewToSuggestFrom(t *testing.T) {
	bills := []BillHistoryEntry{
		paidBill(2026, time.August, 1, "30.00"),
		paidBill(2026, time.September, 1, "30.00"),
		{DueOn: NewDate(2026, time.August, 20), Amount: MustFromString("30.00"), Status: BillSuperseded},
	}
	_, ok := SuggestBillReminder(bills, nil, NewDate(2026, time.October, 2))
	require.False(t, ok)
}

func TestAOneMonthGapIsALateBillNotASeason(t *testing.T) {
	// Monthly on the 3rd with March missing: one whole month between bills
	// is too short to be an off season.
	var bills []BillHistoryEntry
	for _, month := range []time.Month{
		time.January, time.February, time.April, time.May, time.June, time.July,
	} {
		bills = append(bills, paidBill(2026, month, 3, "40.00"))
	}
	got, ok := SuggestBillReminder(bills, nil, NewDate(2026, time.July, 10))
	require.True(t, ok)
	require.Empty(t, got.Recurrence.ByMonth)
	require.Equal(t, 3, got.Recurrence.ByMonthDay[0])
	require.False(t, got.Confident)
}
