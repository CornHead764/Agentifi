package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// One account's cash flow on the projection card: its own estimate, and where
// its balance has been. The seeded checking account opens at 500.00 and has
// -100 on 15 July and -50, -200 (a transfer) and -25 in August; the card sits
// at -300.00 by the provider, with +200 on 10 August and a -75 charge posted
// on 25 August whose effective date is 10 September.

func frozenOn(l *ledger, day domain.Date) {
	l.env.Now = func() time.Time { return day.Time().Add(12 * time.Hour) }
}

func TestAnAccountsFirstLookAtTheCardMakesItsEstimate(t *testing.T) {
	l := buildLedger(t)
	frozenOn(l, domain.NewDate(2026, time.September, 26))
	path := "/cash-flow-forecast?account_id=" + l.str("checking")

	body := l.alex.get(path).requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["available"], body["unavailable"])
	require.Equal(t, "average", body["method"])
	require.Equal(t, l.str("checking"), body["account_id"])
	months := body["months"].([]any)
	require.Len(t, months, 6)
	first := months[0].(map[string]any)
	require.Equal(t, "2026-09", first["month"])
	// The history begins on 15 July, so July is partial and left out: August
	// alone is read, 275.00 out with the transfer included.
	require.Equal(t, "0.00", first["money_in"])
	require.Equal(t, "275.00", first["money_out"])
	require.Contains(t, body["narrative"], "its one complete month (2026-08)")
	require.NotNil(t, body["windows"].([]any)[0].(map[string]any)["scheduled_balance"])

	// Stored, not re-made on every look.
	again := l.alex.get(path).requireStatus(http.StatusOK).json()
	require.Equal(t, body["generated_at"], again["generated_at"])

	// The household forecast is still the automation's, and there is none.
	household := l.alex.get("/cash-flow-forecast").requireStatus(http.StatusOK).json()
	require.Equal(t, false, household["available"])
	require.Contains(t, household["unavailable"], "No forecast has been made yet")
}

func TestReRunningAnAccountsEstimateMakesANewOne(t *testing.T) {
	l := buildLedger(t)
	frozenOn(l, domain.NewDate(2026, time.September, 26))
	query := "?account_id=" + l.str("card")

	before := l.alex.get("/cash-flow-forecast" + query).requireStatus(http.StatusOK).json()
	after := l.alex.post("/cash-flow-forecast/run"+query, map[string]any{}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, after["available"])
	require.NotEqual(t, before["generated_at"], after["generated_at"])
	first := after["months"].([]any)[0].(map[string]any)
	require.Equal(t, "200.00", first["money_in"])
	require.Equal(t, "75.00", first["money_out"])

	// A viewer reads the estimate and cannot re-make it; another household
	// reaches neither; the household forecast is not re-run from here.
	l.as("vera").get("/cash-flow-forecast" + query).requireStatus(http.StatusOK)
	l.as("vera").post("/cash-flow-forecast/run"+query, map[string]any{}).
		requireStatus(http.StatusForbidden)
	l.as("bob").get("/cash-flow-forecast" + query).requireStatus(http.StatusNotFound)
	l.alex.post("/cash-flow-forecast/run", map[string]any{}).requireStatus(http.StatusBadRequest)
}

func TestAnAccountWithNoCompleteMonthSaysWhyItHasNoEstimate(t *testing.T) {
	l := buildLedger(t)
	frozenOn(l, domain.NewDate(2026, time.September, 26))
	fresh := &store.Account{
		Name: "Brand New Savings", Kind: domain.KindCash, Type: "savings", Currency: "USD",
		IncludeInNetWorth: true,
	}
	require.NoError(t, l.env.DB.CreateAccount(t.Context(), store.SpaceID(l.id("space")), fresh))
	require.NoError(t, l.env.DB.CreateTransaction(t.Context(), store.SpaceID(l.id("space")),
		&store.Transaction{
			AccountID: fresh.ID, Date: domain.NewDate(2026, time.September, 3),
			Amount: domain.MustFromString("40.00"), Currency: "USD",
			StatementName: "DEPOSIT", Payee: "Deposit", Source: domain.SourceManual,
		}))

	body := l.alex.get("/cash-flow-forecast?account_id=" + fresh.ID.String()).
		requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["available"])
	require.Contains(t, body["unavailable"], "no complete month of history yet")
}

func historyPoints(body map[string]any) map[string]string {
	out := map[string]string{}
	for _, raw := range body["points"].([]any) {
		point := raw.(map[string]any)
		out[point["on"].(string)] = point["balance"].(string)
	}
	return out
}

func TestTheBalanceHistoryIsTheBalanceAtTheEndOfEachDay(t *testing.T) {
	l := buildLedger(t)
	frozenOn(l, domain.NewDate(2026, time.September, 26))

	checking := l.alex.get("/account-balance-history?account_id=" + l.str("checking") +
		"&from=2026-07-14&to=2026-07-16").requireStatus(http.StatusOK).json()
	require.Equal(t, map[string]string{
		"2026-07-14": "500.00", "2026-07-15": "400.00", "2026-07-16": "400.00",
	}, historyPoints(checking))

	// The card walks back from -300.00, and its charge steps the line on the
	// day it posted, not on its September effective date.
	card := l.alex.get("/account-balance-history?account_id=" + l.str("card") +
		"&from=2026-08-24&to=2026-08-26").requireStatus(http.StatusOK).json()
	require.Equal(t, map[string]string{
		"2026-08-24": "-225.00", "2026-08-25": "-300.00", "2026-08-26": "-300.00",
	}, historyPoints(card))
}

func TestTheBalanceHistoryEndsTodayOnTheBalanceTheHeaderShows(t *testing.T) {
	l := buildLedger(t)
	frozenOn(l, domain.NewDate(2026, time.September, 26))
	body := l.alex.get("/account-balance-history?account_id=" + l.str("checking") +
		"&from=2026-09-25&to=2026-10-30").requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-09-26", body["to"], "a history stops at today")
	points := body["points"].([]any)
	require.Len(t, points, 2)
	require.Equal(t, "125.00", points[1].(map[string]any)["balance"])
	require.Equal(t, "125.00", body["balance"])
}

func TestTheBalanceHistoryNeedsBothEndsAndItsOwnAccount(t *testing.T) {
	l := buildLedger(t)
	frozenOn(l, domain.NewDate(2026, time.September, 26))
	base := "/account-balance-history?account_id=" + l.str("checking")
	l.alex.get(base + "&from=2026-09-01").requireStatus(http.StatusBadRequest)
	l.alex.get(base + "&from=2026-09-10&to=2026-09-01").requireStatus(http.StatusUnprocessableEntity)
	l.alex.get(base + "&from=2020-01-01&to=2026-09-01").requireStatus(http.StatusBadRequest)
	l.alex.get("/account-balance-history?from=2026-09-01&to=2026-09-02").
		requireStatus(http.StatusUnprocessableEntity)
	l.as("bob").get(base + "&from=2026-09-01&to=2026-09-02").requireStatus(http.StatusNotFound)
}
