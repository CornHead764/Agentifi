package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

var noAutopay = domain.AutopayRule{Kind: domain.AutopayNone}

// statement is an invented open statement issued and due on the days given;
// an empty issue day is one the provider does not state.
func statement(id, issued, due, owed string) domain.StatementFacts {
	one := domain.StatementFacts{
		ID: domain.ID(id), DueOn: day(due), AmountDue: amt(owed), Status: domain.BillOpen,
	}
	if issued != "" {
		one.IssuedOn = day(issued)
	}
	return one
}

// carriedForward is a clinic's guarantor account whose unpaid balance is
// restated on three monthly statements.
func carriedForward() domain.BilledAccountFacts {
	return domain.BilledAccountFacts{
		ID: "acct-clinic", Autopay: noAutopay,
		Statements: []domain.StatementFacts{
			statement("jan", "2026-01-05", "2026-01-30", "90.00"),
			statement("feb", "2026-02-04", "2026-02-27", "90.00"),
			statement("mar", "2026-03-03", "2026-03-28", "90.00"),
		},
	}
}

func TestThreeCarriedForwardStatementsAreOneReminderForTheNewest(t *testing.T) {
	require.Equal(t, []domain.ManualBill{{
		BillID: "mar", SubaccountID: "acct-clinic", DueOn: day("2026-03-28"), Amount: amt("-90.00"),
	}}, domain.PayManually([]domain.BilledAccountFacts{carriedForward()}))
}

func TestAStatementSettledByAPairedPaymentIsNoReminder(t *testing.T) {
	account := carriedForward()
	account.PaidOn = []domain.Date{day("2026-03-10")}
	require.Empty(t, domain.PayManually([]domain.BilledAccountFacts{account}))
}

// A payment made before the newest statement was issued settled the one
// before it, so the newest is still owed.
func TestAPaymentBeforeTheNewestStatementLeavesItOwed(t *testing.T) {
	account := carriedForward()
	account.PaidOn = []domain.Date{day("2026-02-20")}
	got := domain.PayManually([]domain.BilledAccountFacts{account})
	require.Len(t, got, 1)
	require.Equal(t, domain.ID("mar"), got[0].BillID)
}

func TestAnAccountWhoseConnectionAutopaysHasNoReminder(t *testing.T) {
	for _, kind := range []domain.AutopayRuleKind{
		domain.AutopayOnDueDate, domain.AutopayDaysBeforeDue, domain.AutopayDayOfMonth,
	} {
		account := carriedForward()
		account.Autopay = domain.AutopayRule{Kind: kind, Days: 3, DayOfMonth: 20}
		require.Emptyf(t, domain.PayManually([]domain.BilledAccountFacts{account}), "rule %s", kind)
	}
}

func TestAStatementThatStatesItsOwnAutopayDayIsNoReminder(t *testing.T) {
	account := carriedForward()
	account.Statements[2].AutopayOn = day("2026-03-25")
	require.Empty(t, domain.PayManually([]domain.BilledAccountFacts{account}))
}

func TestAStatementOwingNothingIsNoReminder(t *testing.T) {
	account := carriedForward()
	account.Statements[2].AmountDue = amt("0.00")
	require.Empty(t, domain.PayManually([]domain.BilledAccountFacts{account}),
		"the older statements are not separate debts")
}

func TestAStatementMarkedPaidIsNoReminder(t *testing.T) {
	account := carriedForward()
	account.Statements[2].MarkedPaid = true
	require.Empty(t, domain.PayManually([]domain.BilledAccountFacts{account}))
}

func TestAStatementTheProviderReportsPaidIsNoReminder(t *testing.T) {
	account := carriedForward()
	account.Statements[2].Status = domain.BillPaid
	require.Empty(t, domain.PayManually([]domain.BilledAccountFacts{account}))
}

func TestAnAccountARecurringReminderCarriesHasNoSecondReminder(t *testing.T) {
	account := carriedForward()
	account.Linked = true
	require.Empty(t, domain.PayManually([]domain.BilledAccountFacts{account}))
}

func TestASupersededStatementIsNeverTheNewest(t *testing.T) {
	account := domain.BilledAccountFacts{
		ID: "acct-water", Autopay: noAutopay,
		Statements: []domain.StatementFacts{
			statement("reissued", "2026-04-02", "2026-04-24", "51.00"),
			statement("cycle", "2026-04-01", "2026-04-22", "48.00"),
		},
	}
	account.Statements[0].Status = domain.BillSuperseded
	got := domain.PayManually([]domain.BilledAccountFacts{account})
	require.Len(t, got, 1)
	require.Equal(t, domain.ID("cycle"), got[0].BillID)
}

// A provider that states no issue date is ordered by due date, and two
// invoices due the same day are two debts.
func TestTwoInvoicesOfTheNewestDayAreTwoReminders(t *testing.T) {
	account := domain.BilledAccountFacts{
		ID: "acct-lawn", Autopay: noAutopay,
		Statements: []domain.StatementFacts{
			statement("may", "", "2026-05-15", "62.00"),
			statement("jun-a", "", "2026-06-15", "62.00"),
			statement("jun-b", "", "2026-06-15", "19.50"),
		},
	}
	got := domain.PayManually([]domain.BilledAccountFacts{account})
	require.Len(t, got, 2)
	require.ElementsMatch(t, []domain.ID{"jun-a", "jun-b"}, []domain.ID{got[0].BillID, got[1].BillID})
}

func TestAPayManuallyReminderIsExpectedOnItsDayAndCountedOnceAsABill(t *testing.T) {
	manual := []domain.ManualBill{{
		BillID: "mar", SubaccountID: "acct-clinic", DueOn: day("2026-03-28"), Amount: amt("-90.00"),
	}}
	got := domain.ExpectedOccurrences(nil, day("2026-03-01"), day("2026-03-31"), nil, nil, manual, nil)
	require.Equal(t, []domain.Occurrence{{
		Kind: domain.SeriesBill, DueOn: day("2026-03-28"), ScheduledOn: day("2026-03-28"),
		Amount: amt("-90.00"), BillID: "mar",
	}}, got)
	require.True(t, domain.SummarizeOccurrences(got).Expenses.Equal(amt("-90.00")))

	require.Empty(t, domain.ExpectedOccurrences(nil, day("2026-04-01"), day("2026-04-30"), nil, nil, manual, nil),
		"outside the window")
	require.Empty(t, domain.ExpectedOccurrences(nil, day("2026-03-01"), day("2026-03-31"), nil, nil, manual,
		map[domain.ID]bool{"checking": true}), "it is paid from no account anyone named")
}
