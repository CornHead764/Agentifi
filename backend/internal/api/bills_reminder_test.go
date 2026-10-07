package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A reminder suggested from a billed account's statements. Every figure is
// invented; the clock is seriesClock, 2026-08-22.

func lawnSubaccount(t *testing.T, alex *client) (connection, subaccount map[string]any) {
	t.Helper()
	connection = newBillConnection(alex, map[string]any{"label": "Lawn service"})
	subaccount = alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "yard-1", "label": "Front yard"}).
		requireStatus(http.StatusCreated).json()
	return connection, subaccount
}

func fileBill(l *ledger, subaccountID string, on domain.Date, amount string, status domain.BillStatus) {
	l.t.Helper()
	bill := &store.Bill{
		SubaccountID: uuid.MustParse(subaccountID), DueOn: on,
		AmountDue: domain.MustFromString(amount), Currency: "USD",
		Status: status, Source: "provider", FetchedAt: seriesClock,
	}
	require.NoError(l.t, l.env.DB.UpsertBill(l.t.Context(), store.SpaceIDOf(l.id("space")), bill, false))
}

func TestASeasonalBilledAccountSuggestsASeasonalReminder(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	_, subaccount := lawnSubaccount(t, alex)
	id := subaccount["id"].(string)
	for month := time.April; month <= time.October; month++ {
		fileBill(l, id, domain.NewDate(2025, month, 8), "45.00", domain.BillPaid)
	}
	for month := time.April; month <= time.July; month++ {
		fileBill(l, id, domain.NewDate(2026, month, 8), "45.00", domain.BillPaid)
	}
	fileBill(l, id, domain.NewDate(2026, time.August, 7), "45.00", domain.BillOpen)
	june := seedTxn(l, "lawn-june", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.June, 9),
		Amount: domain.MustFromString("-45.00"), StatementName: "GREEN LAWN CO 5602",
	})
	july := seedTxn(l, "lawn-july", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.July, 8),
		Amount: domain.MustFromString("-45.00"), StatementName: "GREEN LAWN CO 5651",
	})

	got := alex.get("/bills/subaccounts/" + id + "/suggested-reminder").
		requireStatus(http.StatusOK).json()
	recurrence := got["recurrence"].(map[string]any)
	require.Equal(t, []any{4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0}, recurrence["by_month"])
	require.Equal(t, []any{8.0}, recurrence["by_month_day"])
	require.Equal(t, "2026-08-08", got["start_on"], "the slot the open August bill claims")
	require.Equal(t, "-45.00", got["amount"])
	require.Equal(t, false, got["amount_varies"])
	require.Equal(t, "exact", got["match_criteria"])
	require.Equal(t, true, got["confident"])
	require.EqualValues(t, 12, got["due_dates"])
	require.Equal(t, l.str("checking"), got["account_id"])
	require.Equal(t, "GREEN LAWN CO 5651", got["description"])
	require.Equal(t, "Lawn service", got["display_name"])
	require.Equal(t, "bill", got["kind"])
	require.Equal(t, []any{june.String(), july.String()}, got["payment_ids"])
	require.Nil(t, got["paid_by_series_id"])

	// Confirming it is the ordinary create and link; the account then keeps
	// a reminder current and has nothing more to suggest.
	series := newSeries(alex, l, map[string]any{
		"description":  got["description"],
		"display_name": got["display_name"],
		"amount":       got["amount"],
		"start_on":     got["start_on"],
		"recurrence":   recurrence,
	})
	require.Equal(t, "2026-08-08", series["due_on"])
	alex.post("/bills/links", map[string]any{
		"series_id": series["id"], "subaccount_id": id,
	}).requireStatus(http.StatusCreated)
	alex.get("/bills/subaccounts/" + id + "/suggested-reminder").requireStatus(http.StatusConflict)
}

func TestTooFewBillsSuggestNothing(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	_, subaccount := lawnSubaccount(t, alex)
	id := subaccount["id"].(string)
	fileBill(l, id, domain.NewDate(2026, time.June, 8), "45.00", domain.BillPaid)
	fileBill(l, id, domain.NewDate(2026, time.July, 8), "45.00", domain.BillPaid)
	alex.get("/bills/subaccounts/" + id + "/suggested-reminder").requireStatus(http.StatusNotFound)
}

func TestASuggestionNamesTheSeriesThePaymentsAlreadyBelongTo(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	_, subaccount := lawnSubaccount(t, alex)
	id := subaccount["id"].(string)
	water := newSeries(alex, l, map[string]any{
		"description": "CITY WATER", "amount": "-61.00", "start_on": "2026-05-20",
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{20}},
	})
	for month := time.May; month <= time.July; month++ {
		fileBill(l, id, domain.NewDate(2026, month, 20), "61.00", domain.BillPaid)
		alex.post("/occurrences/accept", map[string]any{
			"series_id": water["id"], "due_on": domain.NewDate(2026, month, 20).String(),
		}).requireStatus(http.StatusCreated)
	}

	got := alex.get("/bills/subaccounts/" + id + "/suggested-reminder").
		requireStatus(http.StatusOK).json()
	require.Equal(t, water["id"], got["paid_by_series_id"])
}

func TestAnotherSpacesBilledAccountHasNoSuggestion(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	_, subaccount := lawnSubaccount(t, alex)
	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	bob.get("/bills/subaccounts/" + subaccount["id"].(string) + "/suggested-reminder").
		requireStatus(http.StatusNotFound)
}
