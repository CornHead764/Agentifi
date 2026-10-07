package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Pay-manually reminders over HTTP. Every figure is invented; the clock is
// seriesClock.

// clinicStatements is a clinic's billed account whose unpaid balance is
// restated on three monthly statements, the newest due later this month, at
// a connection with no autopay. It answers the connection, the billed account
// and the newest statement's id.
func clinicStatements(t *testing.T, l *ledger, alex *client) (connection, subaccount map[string]any, newest uuid.UUID) {
	t.Helper()
	connection = newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerMyChart), "label": "Example Health",
	})
	subaccount = alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "70005678", "label": "Guarantor account ****5678"}).
		requireStatus(http.StatusCreated).json()
	for _, month := range []time.Month{time.June, time.July, time.August} {
		bill := &store.Bill{
			SubaccountID: uuid.MustParse(subaccount["id"].(string)),
			IssuedOn:     domain.NewDate(2026, month, 3), DueOn: domain.NewDate(2026, month, 28),
			AmountDue: domain.MustFromString("90.00"), Currency: "USD",
			Status: domain.BillOpen, Source: store.BillSourceProvider, FetchedAt: seriesClock,
		}
		require.NoError(t, l.env.DB.UpsertBill(t.Context(), store.SpaceIDOf(l.id("space")), bill, false))
		newest = bill.ID
	}
	return connection, subaccount, newest
}

// payManuallyItems is the listing's occurrences that belong to no series.
func payManuallyItems(t *testing.T, list map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range list["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] == nil {
			out = append(out, item)
		}
	}
	return out
}

const payManuallyWindow = "from=2026-08-01&to=2026-09-30"

func TestTheNewestUnpaidStatementIsOneReminderInUpcoming(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	before := occurrencesIn(alex, payManuallyWindow)
	_, _, newest := clinicStatements(t, l, alex)

	list := occurrencesIn(alex, payManuallyWindow)
	items := payManuallyItems(t, list)
	require.Len(t, items, 1, "the older statements carry the same balance, so they are not separate debts")
	item := items[0]
	require.Equal(t, "2026-08-28", item["due_on"])
	require.Equal(t, "-90.00", item["amount"])
	require.Equal(t, "bill", item["kind"])
	require.Equal(t, "upcoming", item["status"])
	require.Equal(t, "Example Health", item["label"])
	require.Nil(t, item["account_id"])
	require.Equal(t, newest.String(), item["bill"].(map[string]any)["id"])
	link := item["bill_link"].(map[string]any)
	require.Equal(t, "Guarantor account ****5678", link["subaccount_label"])
	require.Equal(t, false, link["autopay"])

	was := domain.MustFromString(before["summary"].(map[string]any)["expenses"].(string))
	after := domain.MustFromString(list["summary"].(map[string]any)["expenses"].(string))
	require.True(t, after.Sub(was).Equal(domain.MustFromString("-90.00")), "counted once in the expenses")

	require.Empty(t, payManuallyItems(t, occurrencesIn(alex, payManuallyWindow+"&account_id="+l.str("checking"))),
		"it is paid from no account anyone named")
}

func TestMarkingTheStatementPaidEndsItsReminder(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	_, _, newest := clinicStatements(t, l, alex)

	l.as("vera").post("/bills/statements/"+newest.String()+"/paid", nil).requireStatus(http.StatusForbidden)
	alex.post("/bills/statements/"+uuid.NewString()+"/paid", nil).requireStatus(http.StatusNotFound)
	alex.post("/bills/statements/"+newest.String()+"/paid", nil).requireStatus(http.StatusNoContent)

	require.Empty(t, payManuallyItems(t, occurrencesIn(alex, payManuallyWindow)))
}

func TestAPaymentPairedWithABankRowEndsTheReminder(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	_, subaccount, _ := clinicStatements(t, l, alex)
	space := store.SpaceIDOf(l.id("space"))

	row := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 12),
		Amount: domain.MustFromString("-90.00"), Currency: "USD",
		StatementName: "EXAMPLE HEALTH PATIENT PMT", Payee: "Example Health",
	}
	require.NoError(t, l.env.DB.CreateTransaction(t.Context(), space, row))
	_, err := l.env.DB.UpsertBillPayments(t.Context(), space, uuid.MustParse(subaccount["id"].(string)),
		[]store.BillPayment{{
			ExternalID: "70005678:2026-08-11", PaidOn: domain.NewDate(2026, time.August, 11),
			Amount: domain.MustFromString("90.00"), FetchedAt: seriesClock,
		}})
	require.NoError(t, err)
	require.Len(t, payManuallyItems(t, occurrencesIn(alex, payManuallyWindow)), 1,
		"a payment no bank row carries yet settles nothing")

	made, err := l.env.DB.MatchBillPayments(t.Context(), space)
	require.NoError(t, err)
	require.Equal(t, 1, made)
	require.Empty(t, payManuallyItems(t, occurrencesIn(alex, payManuallyWindow)))
}

func TestAConnectionThatAutopaysHasNoPayManuallyReminder(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection, _, _ := clinicStatements(t, l, alex)

	alex.patch("/bills/connections/"+connection["id"].(string), map[string]any{
		"autopay_rule": string(domain.AutopayOnDueDate),
	}).requireStatus(http.StatusOK)

	require.Empty(t, payManuallyItems(t, occurrencesIn(alex, payManuallyWindow)))
}

func TestALinkedReminderIsNotDuplicatedAndSaysItIsPaidByHand(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	_, subaccount, _ := clinicStatements(t, l, alex)
	series := newSeries(alex, l, map[string]any{
		"description": "EXAMPLE HEALTH", "start_on": "2026-06-28",
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{28}},
	})
	alex.post("/bills/links", map[string]any{
		"series_id": series["id"], "subaccount_id": subaccount["id"],
	}).requireStatus(http.StatusCreated)

	list := occurrencesIn(alex, payManuallyWindow)
	require.Empty(t, payManuallyItems(t, list))
	linked := 0
	for _, raw := range list["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] == series["id"] {
			linked++
			require.Equal(t, false, item["bill_link"].(map[string]any)["autopay"])
		}
	}
	require.Positive(t, linked)
}

func TestTheAssistantHearsOfAPayManuallyReminder(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	clinicStatements(t, l, alex)

	tools := assistantTools{env: alex.env, sp: auth.SpaceContext{Space: store.Space{ID: store.SpaceIDOf(l.id("space"))}}}
	answer, err := tools.upcomingBills(t.Context(), map[string]any{"from": "2026-08-01", "to": "2026-09-30"})
	require.NoError(t, err)
	encoded, err := json.Marshal(answer)
	require.NoError(t, err)
	var read struct {
		Expected []map[string]any `json:"expected"`
	}
	require.NoError(t, json.Unmarshal(encoded, &read))
	var manual []map[string]any
	for _, one := range read.Expected {
		if one["pay_manually"] == true {
			manual = append(manual, one)
		}
	}
	require.Len(t, manual, 1)
	require.Equal(t, "Example Health", manual[0]["name"])
	require.Equal(t, "2026-08-28", manual[0]["due_on"])
	require.Equal(t, "-90.00", manual[0]["amount"])
	require.Equal(t, "upcoming", manual[0]["status"])
}
