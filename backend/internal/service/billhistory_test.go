package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A bill on file makes a past bank row fit the slot it missed against the
// reminder's estimate, so a pull and a new link offer the history again and
// the row receives the bill's statement. Every figure here is invented.

func attachBillStatement(
	t *testing.T, fixture billFixture, dueOn domain.Date, name string,
) store.Document {
	t.Helper()
	bills, err := db(t).ListBills(t.Context(), fixture.space, fixture.subaccount.ID)
	require.NoError(t, err)
	stored, held := BillOfCycle(bills, dueOn, "")
	require.True(t, held)
	docs, _ := newDocuments(t)
	document, err := docs.AttachStatement(t.Context(), fixture.space, stored.ID, DocumentUpload{
		Bytes: pngBytes(name), Filename: name + ".png", Source: store.DocumentSourceUpload,
	})
	require.NoError(t, err)
	return document
}

// paidBill is a past cycle as a provider's history reports it. An open one
// would be superseded by any newer bill and speak for no slot.
func paidBill(dueOn domain.Date, amount string) store.Bill {
	bill := pulledBill(dueOn, amount)
	bill.Status = domain.BillPaid
	return bill
}

func receiptsOn(t *testing.T, spaceID store.SpaceID, id uuid.UUID) []uuid.UUID {
	t.Helper()
	behind, err := db(t).DocumentsBehindTransaction(t.Context(), spaceID, id)
	require.NoError(t, err)
	var out []uuid.UUID
	for _, one := range behind {
		if one.Via == store.DocumentLinkReceipt {
			out = append(out, one.Document.ID)
		}
	}
	return out
}

func TestABillPullSlotsAPastChargeAndFilesItsStatement(t *testing.T) {
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	account := newAccount(t, fixture.space, "Everyday Checking")
	series := newSeries(t, fixture.space, account,
		seriesDescription("NORTHWIND POWER"),
		seriesAmount(domain.MustFromString("-250.00")),
		seriesNextDueOn(on(2026, time.July, 15)))
	water := newSeries(t, fixture.space, account,
		seriesDescription("NORTHWIND WATER"),
		seriesAmount(domain.MustFromString("-12.00")),
		seriesNextDueOn(on(2026, time.July, 15)))
	_, err := fixture.bills.Link(t.Context(), fixture.space, series.ID, fixture.subaccount.ID)
	require.NoError(t, err)

	march := newTransaction(t, fixture.space, account, on(2026, time.March, 18), "-63.00",
		withStatementName("NORTHWIND POWER"))
	twice := newTransaction(t, fixture.space, account, on(2026, time.March, 20), "-63.00",
		withStatementName("NORTHWIND POWER"))
	april := newTransaction(t, fixture.space, account, on(2026, time.April, 17), "-99.00",
		withStatementName("NORTHWIND POWER"))
	unlinked := newTransaction(t, fixture.space, account, on(2026, time.March, 16), "-12.00",
		withStatementName("NORTHWIND WATER"))

	_, err = fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, []store.Bill{
		paidBill(on(2026, time.March, 17), "63.00"),
		pulledBill(on(2026, time.April, 16), "70.00"),
	})
	require.NoError(t, err)

	require.Equal(t, series.ID, reload(t, fixture.space, march.ID).SeriesID)
	require.Equal(t, on(2026, time.March, 15), reload(t, fixture.space, march.ID).SeriesDueOn)
	require.Equal(t, uuid.Nil, reload(t, fixture.space, twice.ID).SeriesID,
		"the March slot is settled once")
	require.Equal(t, uuid.Nil, reload(t, fixture.space, april.ID).SeriesID,
		"99.00 is outside the exact band of April's 70.00")
	require.Equal(t, uuid.Nil, reload(t, fixture.space, unlinked.ID).SeriesID,
		"a series not linked to the bill is not offered the history")
	require.Equal(t, on(2026, time.July, 15), loadSeries(t, fixture.space, series.ID).NextDueOn,
		"a back-fill leaves the pointer where it stood")
	require.Equal(t, on(2026, time.July, 15), loadSeries(t, fixture.space, water.ID).NextDueOn)

	statement := attachBillStatement(t, fixture, on(2026, time.March, 17), "march")
	require.Equal(t, []uuid.UUID{statement.ID}, receiptsOn(t, fixture.space, march.ID))
	require.Empty(t, receiptsOn(t, fixture.space, twice.ID))
}

func TestLinkingABillCatchesUpOnHistoryAndMovesAPastDuePointer(t *testing.T) {
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	account := newAccount(t, fixture.space, "Everyday Checking")
	series := newSeries(t, fixture.space, account,
		seriesDescription("NORTHWIND POWER"),
		seriesAmount(domain.MustFromString("-250.00")),
		seriesNextDueOn(on(2026, time.March, 15)))

	held := newTransaction(t, fixture.space, account, on(2026, time.February, 16), "-58.00",
		withStatementName("NORTHWIND POWER"), withSeries(series.ID, on(2026, time.February, 15)))
	february := newTransaction(t, fixture.space, account, on(2026, time.February, 17), "-58.00",
		withStatementName("NORTHWIND POWER"))
	march := newTransaction(t, fixture.space, account, on(2026, time.March, 18), "-63.00",
		withStatementName("NORTHWIND POWER"))

	_, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, []store.Bill{
		paidBill(on(2026, time.February, 16), "58.00"),
		paidBill(on(2026, time.March, 17), "63.00"),
	})
	require.NoError(t, err)
	statement := attachBillStatement(t, fixture, on(2026, time.March, 17), "march")
	require.Equal(t, uuid.Nil, reload(t, fixture.space, march.ID).SeriesID,
		"nothing is linked yet, so the estimate still decides")

	_, err = fixture.bills.Link(t.Context(), fixture.space, series.ID, fixture.subaccount.ID)
	require.NoError(t, err)

	require.Equal(t, on(2026, time.March, 15), reload(t, fixture.space, march.ID).SeriesDueOn)
	require.Equal(t, []uuid.UUID{statement.ID}, receiptsOn(t, fixture.space, march.ID))
	require.Equal(t, on(2026, time.April, 15), loadSeries(t, fixture.space, series.ID).NextDueOn,
		"the past-due slot was the pointer, so paying it moves the pointer on")

	require.Equal(t, on(2026, time.February, 15), reload(t, fixture.space, held.ID).SeriesDueOn)
	require.Equal(t, uuid.Nil, reload(t, fixture.space, february.ID).SeriesID,
		"a slot a row already holds is not claimed twice")
}
