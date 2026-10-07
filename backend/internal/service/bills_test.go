package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The bills bridge against a database: a pull run twice writes nothing the
// second time, and occurrence readers get the stored statement with its payment
// date resolved once. Every figure here is invented.

type billFixture struct {
	bills      *Bills
	space      store.SpaceID
	connection *store.BillConnection
	subaccount *store.BillSubaccount
}

func newBillFixture(t *testing.T, rule domain.AutopayRule) billFixture {
	t.Helper()
	space := newSpace(t)
	connection := &store.BillConnection{
		Biller: domain.BillerSpectrum, Label: "Main account",
		CredentialSource: store.BillCredentialSession,
		AutopayRule:      rule.Kind, AutopayDays: rule.Days, AutopayDayOfMonth: rule.DayOfMonth,
		PullEnabled: true,
	}
	require.NoError(t, db(t).CreateBillConnection(t.Context(), space, connection))
	subaccount := &store.BillSubaccount{
		ConnectionID: connection.ID, ExternalID: "line-1", Label: "Internet", IsSelected: true,
	}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), space, subaccount))

	bills := NewBills(db(t))
	bills.Now = func() time.Time { return time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC) }
	return billFixture{bills: bills, space: space, connection: connection, subaccount: subaccount}
}

func pulledBill(dueOn domain.Date, amount string) store.Bill {
	return store.Bill{
		DueOn: dueOn, AmountDue: domain.MustFromString(amount), Currency: "USD",
		Status: domain.BillOpen, Source: store.BillSourceProvider,
	}
}

func TestBillsIngestIsIdempotent(t *testing.T) {
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	pull := []store.Bill{pulledBill(on(2026, time.October, 26), "90.00")}

	first, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, pull)
	require.NoError(t, err)
	require.Equal(t, 1, first.New)
	require.Zero(t, first.Amended)

	second, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, pull)
	require.NoError(t, err)
	require.Zero(t, second.New, "the same cycle is the same bill")
	require.Zero(t, second.Amended, "and nothing about it moved")
	require.Equal(t, 1, second.Unchanged)
	require.Len(t, second.Bills, 1)
}

func TestBillsIngestKeepsTwoInvoicesOfOneDayApart(t *testing.T) {
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	invoice := func(number, amount string) store.Bill {
		bill := pulledBill(on(2026, time.May, 4), amount)
		bill.Status, bill.Invoice, bill.ExternalID = domain.BillPaid, number, "customer-1:"+number
		return bill
	}
	pull := []store.Bill{invoice("INV-7", "60.00"), invoice("INV-8", "35.00")}

	first, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, pull)
	require.NoError(t, err)
	require.Equal(t, 2, first.New, "each invoice is its own bill")
	require.Zero(t, first.Amended)

	second, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, pull)
	require.NoError(t, err)
	require.Zero(t, second.New, "a second pull files nothing beside them")
	require.Zero(t, second.Amended, "and folds neither into the other")
	require.Equal(t, 2, second.Unchanged)
	require.Len(t, second.Bills, 2)
	amounts := map[string]string{}
	for _, bill := range second.Bills {
		amounts[bill.Invoice] = bill.AmountDue.String()
	}
	require.Equal(t, map[string]string{"INV-7": "60.00", "INV-8": "35.00"}, amounts)
}

func TestBillsIngestKeepsAClosedCycleOnTheDueDateItWasFirstStatedWith(t *testing.T) {
	// A provider that states the open cycle's due date, and later lists the
	// same cycle in its history under its bill date, is one statement.
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	open := pulledBill(on(2026, time.October, 20), "61.00")
	open.ExternalID, open.IssuedOn = "cust-1:2026-09-30", on(2026, time.September, 30)
	open.AutopayOn = on(2026, time.October, 20)
	_, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, []store.Bill{open})
	require.NoError(t, err)

	closed := pulledBill(on(2026, time.September, 30), "61.00")
	closed.ExternalID, closed.IssuedOn = "cust-1:2026-09-30", on(2026, time.September, 30)
	closed.Status = domain.BillPaid
	result, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, []store.Bill{closed})
	require.NoError(t, err)
	require.Zero(t, result.New)
	require.Len(t, result.Bills, 1)
	require.Equal(t, on(2026, time.October, 20), result.Bills[0].DueOn)
	require.Equal(t, on(2026, time.October, 20), result.Bills[0].AutopayOn)
	require.Equal(t, domain.BillPaid, result.Bills[0].Status)
}

func TestBillsIngestDoesNotJoinBillsThatShareOnlyAFallbackID(t *testing.T) {
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	first := pulledBill(on(2026, time.September, 20), "40.00")
	first.ExternalID = "policy-1:owed"
	second := pulledBill(on(2026, time.October, 20), "40.00")
	second.ExternalID = "policy-1:owed"
	_, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, []store.Bill{first})
	require.NoError(t, err)
	result, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, []store.Bill{second})
	require.NoError(t, err)
	require.Equal(t, 1, result.New)
	require.Len(t, result.Bills, 2)
}

func TestBillsIngestAmendsSameDueDate(t *testing.T) {
	// A corrected statement is the same bill (identity is subaccount and due
	// date), so the row is updated rather than doubled.
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	dueOn := on(2026, time.October, 26)

	_, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID,
		[]store.Bill{pulledBill(dueOn, "90.00")})
	require.NoError(t, err)

	result, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID,
		[]store.Bill{pulledBill(dueOn, "105.00")})
	require.NoError(t, err)
	require.Zero(t, result.New)
	require.Equal(t, 1, result.Amended)
	require.Len(t, result.Bills, 1)
	require.Equal(t, "105.00", result.Bills[0].AmountDue.String())
	require.NotNil(t, result.Bills[0].AmendedAt)
}

func TestBillsIngestSupersedesOlderOpenBill(t *testing.T) {
	// A provider that issues next month's statement just stops mentioning last
	// month's; the newest open cycle is the only one still owed.
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})

	_, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID,
		[]store.Bill{pulledBill(on(2026, time.September, 26), "90.00")})
	require.NoError(t, err)

	result, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID,
		[]store.Bill{pulledBill(on(2026, time.October, 26), "92.00")})
	require.NoError(t, err)
	require.Equal(t, 1, result.New)
	require.Equal(t, 1, result.Superseded)

	byDue := map[domain.Date]domain.BillStatus{}
	for _, one := range result.Bills {
		byDue[one.DueOn] = one.Status
	}
	require.Equal(t, domain.BillSuperseded, byDue[on(2026, time.September, 26)])
	require.Equal(t, domain.BillOpen, byDue[on(2026, time.October, 26)])
}

func TestBillConnectForResolvesAutopayFromRule(t *testing.T) {
	// The connection's rule is applied here and nowhere else. Four days before
	// the 26th is the 22nd.
	fixture := newBillFixture(t,
		domain.AutopayRule{Kind: domain.AutopayDaysBeforeDue, Days: 4})
	series := newSeries(t, fixture.space, newAccount(t, fixture.space, "Everyday Checking"),
		seriesNextDueOn(on(2026, time.October, 15)))
	_, err := fixture.bills.Link(t.Context(), fixture.space, series.ID, fixture.subaccount.ID)
	require.NoError(t, err)
	_, err = fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID,
		[]store.Bill{pulledBill(on(2026, time.October, 26), "90.00")})
	require.NoError(t, err)

	connects, err := fixture.bills.BillConnectFor(t.Context(), fixture.space, nil)
	require.NoError(t, err)
	linked := connects[domain.ID(series.ID.String())]
	require.Len(t, linked, 1)
	connect := linked[0]
	require.Equal(t, on(2026, time.October, 26), connect.DueOn)
	// Signed as the ledger signs it: money going out is negative.
	require.Equal(t, "-90.00", connect.Amount.String())
	require.Equal(t, on(2026, time.October, 22), connect.AutopayOn)

	// A provider that states its own date wins over the rule, which would
	// have said the 22nd.
	stated := pulledBill(on(2026, time.October, 26), "90.00")
	stated.AutopayOn = on(2026, time.October, 24)
	_, err = fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID,
		[]store.Bill{stated})
	require.NoError(t, err)
	connects, err = fixture.bills.BillConnectFor(t.Context(), fixture.space, nil)
	require.NoError(t, err)
	linked = connects[domain.ID(series.ID.String())]
	require.Len(t, linked, 1)
	require.Equal(t, on(2026, time.October, 24), linked[0].AutopayOn)
}

func TestBillConnectForRespectsTheDueDateSwitch(t *testing.T) {
	// The switch is the series' own and the domain applies it, so
	// BillConnectFor hands over the whole statement, including the payment date.
	//
	// The reminder says the 15th at 74.00; the bill says the 13th at 90.00 and
	// autopays on the due date. The bill's amount is the slot's either way;
	// with the switch off the occurrence stays on the 15th, with it on it
	// moves to the 13th.
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayOnDueDate})
	account := newAccount(t, fixture.space, "Everyday Checking")
	held := newSeries(t, fixture.space, account,
		seriesNextDueOn(on(2026, time.October, 15)),
		seriesAmount(domain.MustFromString("-74.00")),
		seriesAutoAdjustDueOn(false))
	_, err := fixture.bills.Link(t.Context(), fixture.space, held.ID, fixture.subaccount.ID)
	require.NoError(t, err)
	_, err = fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID,
		[]store.Bill{pulledBill(on(2026, time.October, 13), "90.00")})
	require.NoError(t, err)

	connects, err := fixture.bills.BillConnectFor(t.Context(), fixture.space, nil)
	require.NoError(t, err)

	window := on(2026, time.October, 1)
	occurrences := domain.ExpectedOccurrences(
		[]domain.Series{ToDomainSeries(seriesRowFor(t, fixture.space, held.ID))},
		window, on(2026, time.October, 31), nil, connects, nil, nil)
	require.Len(t, occurrences, 1)
	require.Equal(t, on(2026, time.October, 15), occurrences[0].DueOn, "the switch is off")
	require.Equal(t, "-90.00", occurrences[0].Amount.String(), "the bill's figure needs no switch")
	require.Equal(t, on(2026, time.October, 13), occurrences[0].PaysOn,
		"autopay is its own fact and is not the switch")

	// The same bill, the same loader, the switch on.
	require.NoError(t, fixture.bills.Unlink(t.Context(), fixture.space, held.ID))
	adjusting := newSeries(t, fixture.space, account,
		seriesNextDueOn(on(2026, time.October, 15)),
		seriesAmount(domain.MustFromString("-74.00")),
		seriesAutoAdjustDueOn(true))
	_, err = fixture.bills.Link(t.Context(), fixture.space, adjusting.ID, fixture.subaccount.ID)
	require.NoError(t, err)

	connects, err = fixture.bills.BillConnectFor(t.Context(), fixture.space, nil)
	require.NoError(t, err)
	occurrences = domain.ExpectedOccurrences(
		[]domain.Series{ToDomainSeries(seriesRowFor(t, fixture.space, adjusting.ID))},
		window, on(2026, time.October, 31), nil, connects, nil, nil)
	require.Len(t, occurrences, 1, "the bill moves the slot rather than adding one")
	require.Equal(t, on(2026, time.October, 13), occurrences[0].DueOn)
	require.Equal(t, "-90.00", occurrences[0].Amount.String())
	require.Equal(t, on(2026, time.October, 13), occurrences[0].PaysOn)
}

func TestBillConnectForCarriesPaidBillsButNotSupersededOnes(t *testing.T) {
	// A reminder whose pointer was never advanced still has past slots to
	// show, and each one's cost is the bill the provider reported for it,
	// paid or not. A superseded statement is a cycle's stale word and never
	// reaches a slot.
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	series := newSeries(t, fixture.space, newAccount(t, fixture.space, "Everyday Checking"),
		seriesNextDueOn(on(2026, time.August, 15)))
	_, err := fixture.bills.Link(t.Context(), fixture.space, series.ID, fixture.subaccount.ID)
	require.NoError(t, err)
	_, err = fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, []store.Bill{
		pulledBill(on(2026, time.August, 17), "60.00"),
		pulledBill(on(2026, time.September, 16), "65.00"),
		pulledBill(on(2026, time.October, 16), "70.00"),
	})
	require.NoError(t, err)
	bills, err := db(t).ListBills(t.Context(), fixture.space, fixture.subaccount.ID)
	require.NoError(t, err)
	statuses := map[domain.Date]domain.BillStatus{
		on(2026, time.August, 17):    domain.BillPaid,
		on(2026, time.September, 16): domain.BillSuperseded,
		on(2026, time.October, 16):   domain.BillOpen,
	}
	for _, bill := range bills {
		require.NoError(t, db(t).SetBillStatus(t.Context(), fixture.space, bill.ID, statuses[bill.DueOn]))
	}

	connects, err := fixture.bills.BillConnectFor(t.Context(), fixture.space, nil)
	require.NoError(t, err)
	linked := connects[domain.ID(series.ID.String())]
	require.Len(t, linked, 2)
	require.Equal(t, on(2026, time.August, 17), linked[0].DueOn)
	require.Equal(t, "-60.00", linked[0].Amount.String())
	require.True(t, linked[0].Paid)
	require.Equal(t, on(2026, time.October, 16), linked[1].DueOn)
	require.Equal(t, "-70.00", linked[1].Amount.String())
	require.False(t, linked[1].Paid)
}

// seriesRowFor reloads a series the way every reader does, so the test runs on
// the stored row rather than on the struct the helper built.
func seriesRowFor(t *testing.T, spaceID store.SpaceID, id uuid.UUID) SeriesRow {
	t.Helper()
	row, err := NewSeriesMatcher(db(t)).GetSeries(t.Context(), spaceID, id)
	require.NoError(t, err)
	return row
}

func TestAChargeForALinkedBillsFigureSettlesItsPastDueSlot(t *testing.T) {
	// The estimate is 250.00 to the cent; the provider billed March at 63.00
	// and reports it paid. The bank charge for 63.00 is March's payment.
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	account := newAccount(t, fixture.space, "Everyday Checking")
	series := newSeries(t, fixture.space, account,
		seriesDescription("NORTHWIND POWER"),
		seriesAmount(domain.MustFromString("-250.00")),
		seriesNextDueOn(on(2026, time.March, 15)))
	_, err := fixture.bills.Link(t.Context(), fixture.space, series.ID, fixture.subaccount.ID)
	require.NoError(t, err)
	_, err = fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID,
		[]store.Bill{pulledBill(on(2026, time.March, 17), "63.00")})
	require.NoError(t, err)
	bills, err := db(t).ListBills(t.Context(), fixture.space, fixture.subaccount.ID)
	require.NoError(t, err)
	require.Len(t, bills, 1)
	require.NoError(t, db(t).SetBillStatus(t.Context(), fixture.space, bills[0].ID, domain.BillPaid))

	charge := newTransaction(t, fixture.space, account, on(2026, time.March, 18), "-63.00",
		withStatementName("NORTHWIND POWER"))
	outcome, ok, err := matchOne(t, fixture.space, *charge)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.OutcomeStampSeries, outcome.Outcome)
	require.Equal(t, on(2026, time.March, 15), reload(t, fixture.space, charge.ID).SeriesDueOn)
	require.Equal(t, on(2026, time.April, 15), loadSeries(t, fixture.space, series.ID).NextDueOn)
}

// A provider that carries an unpaid balance forward restates it on every
// statement; once ingested, the newest is the one reminder to pay by hand.
func TestCarriedForwardStatementsLeaveOneReminderToPayByHand(t *testing.T) {
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	var pull []store.Bill
	for _, month := range []time.Month{time.July, time.August, time.September} {
		one := pulledBill(on(2026, month, 24), "64.00")
		one.IssuedOn = on(2026, month, 2)
		pull = append(pull, one)
	}
	_, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, pull)
	require.NoError(t, err)

	manual, err := fixture.bills.PayManually(t.Context(), fixture.space)
	require.NoError(t, err)
	require.Len(t, manual, 1)
	require.Equal(t, on(2026, time.September, 24), manual[0].DueOn)
	require.True(t, manual[0].Amount.Equal(domain.MustFromString("-64.00")))
	require.Equal(t, fixture.subaccount.ID, manual[0].Subaccount.ID)
	require.Equal(t, fixture.connection.ID, manual[0].Connection.ID)

	require.NoError(t, db(t).MarkBillPaid(t.Context(), fixture.space, manual[0].Bill.ID))
	manual, err = fixture.bills.PayManually(t.Context(), fixture.space)
	require.NoError(t, err)
	require.Empty(t, manual, "marked paid, and the older statements are no separate debts")

	require.NoError(t, db(t).SetBillSubaccountSelected(t.Context(), fixture.space, fixture.subaccount.ID, false))
	_, err = fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID,
		[]store.Bill{pulledBill(on(2026, time.October, 24), "64.00")})
	require.NoError(t, err)
	manual, err = fixture.bills.PayManually(t.Context(), fixture.space)
	require.NoError(t, err)
	require.Empty(t, manual, "a hidden account is reminded of nothing")
}
