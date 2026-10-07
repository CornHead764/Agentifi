package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A yearly premium kept on a monthly reminder, at a provider whose bills are
// its payment history and carry no statement document. Every figure here is
// invented.
func TestMatchHistoryLinksPastPaymentsAndTalliesBillsWithoutStatements(t *testing.T) {
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	account := newAccount(t, fixture.space, "Everyday Checking")
	series := newSeries(t, fixture.space, account,
		seriesDescription("FABRIKAM LIFE PREMIUM"),
		seriesAmount(domain.MustFromString("-480.00")),
		seriesNextDueOn(on(2026, time.November, 15)))

	held := newTransaction(t, fixture.space, account, on(2025, time.January, 16), "-480.00",
		withStatementName("FABRIKAM LIFE PREMIUM"), withSeries(series.ID, on(2025, time.January, 15)))
	missed := newTransaction(t, fixture.space, account, on(2026, time.January, 16), "-480.00",
		withStatementName("FABRIKAM LIFE PREMIUM"))

	_, err := fixture.bills.Ingest(t.Context(), fixture.space, fixture.subaccount.ID, []store.Bill{
		paidBill(on(2025, time.January, 16), "480.00"),
		paidBill(on(2026, time.January, 16), "480.00"),
		pulledBill(on(2027, time.January, 15), "480.00"),
	})
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, reload(t, fixture.space, missed.ID).SeriesID,
		"nothing is linked yet, so no bill offered the history")

	_, err = db(t).LinkSeriesBill(t.Context(), fixture.space, series.ID, fixture.subaccount.ID)
	require.NoError(t, err)

	unlinked := &store.BillSubaccount{
		ConnectionID: fixture.connection.ID, ExternalID: "line-2", Label: "Rider", IsSelected: true,
	}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), fixture.space, unlinked))

	got, err := fixture.bills.MatchHistory(t.Context(), fixture.space,
		[]store.BillSubaccount{*fixture.subaccount, *unlinked})
	require.NoError(t, err)
	require.Len(t, got, 2)

	require.Equal(t, series.ID, got[0].SeriesID)
	require.Equal(t, 1, got[0].Matched)
	require.Equal(t, domain.BillHistory{Settled: 2, WithStatement: 0, Unsettled: 0}, got[0].BillHistory,
		"the bill due in January 2027 is still to come")
	// Gaps of 365 and 364 days: the median of two is the upper.
	require.Equal(t, 365, got[0].CadenceGapDays)

	require.Equal(t, uuid.Nil, got[1].SeriesID)
	require.Equal(t, 0, got[1].Matched)
	require.Equal(t, domain.BillHistory{}, got[1].BillHistory)

	require.Equal(t, on(2026, time.January, 15), reload(t, fixture.space, missed.ID).SeriesDueOn)
	require.Equal(t, on(2026, time.November, 15), loadSeries(t, fixture.space, series.ID).NextDueOn,
		"a back-fill leaves the pointer where it stood")

	require.Empty(t, receiptsOn(t, fixture.space, missed.ID), "the provider gave no statement to file")
	settled, err := db(t).BillsSettledByTransaction(t.Context(), fixture.space, missed.ID)
	require.NoError(t, err)
	require.Len(t, settled, 1)
	require.Equal(t, on(2026, time.January, 16), settled[0].Bill.DueOn)
	require.Equal(t, domain.BillPaid, settled[0].Bill.Status)
	require.Equal(t, uuid.Nil, settled[0].Bill.DocumentID)
	require.Equal(t, fixture.connection.ID, settled[0].Connection.ID)

	statement := attachBillStatement(t, fixture, on(2025, time.January, 16), "premium")
	require.Equal(t, []uuid.UUID{statement.ID}, receiptsOn(t, fixture.space, held.ID))

	again, err := fixture.bills.MatchHistory(t.Context(), fixture.space, []store.BillSubaccount{*fixture.subaccount})
	require.NoError(t, err)
	require.Equal(t, 0, again[0].Matched, "a second run finds nothing new")
	require.Equal(t, domain.BillHistory{Settled: 2, WithStatement: 1}, again[0].BillHistory)
}

func TestBillsSettledByTransactionIgnoresARowHoldingNoSlot(t *testing.T) {
	fixture := newBillFixture(t, domain.AutopayRule{Kind: domain.AutopayNone})
	account := newAccount(t, fixture.space, "Everyday Checking")
	row := newTransaction(t, fixture.space, account, on(2026, time.March, 16), "-40.00",
		withStatementName("NORTHWIND POWER"))
	settled, err := db(t).BillsSettledByTransaction(t.Context(), fixture.space, row.ID)
	require.NoError(t, err)
	require.Empty(t, settled)
}
