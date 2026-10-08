package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The estimated cash-flow forecast, end to end against the stand-in model.
//
// What matters here is the chain nobody can watch: the daily automation is
// handed a numeric series with no payees in it, its JSON answer is parsed and
// stored, and the projection card reads it back with the model's name on it. And
// the other half, which matters more: an answer that does not parse fails the
// run and changes nothing, so the card goes on drawing the arithmetic.

func forecastAutomationBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"name":           "Estimate the next six months of cash flow",
		"trigger":        domain.AutomationTriggerDaily,
		"trigger_config": map[string]any{"at": "05:45"},
		"prompt":         "Estimate the next six months and answer with the JSON object.",
		"context":        map[string]any{"cash_flow_history": true},
		"mode":           domain.AutomationModeObserve,
		"tools":          []string{"cash_flow"},
		"template_key":   domain.AutomationTemplateCashFlowForecast,
	}
	for key, value := range overrides {
		body[key] = value
	}
	return body
}

// forecastAnswer is a well-formed answer for the six months from today.
//
// The figures are deliberately smaller than the seeded ledger's busiest month:
// the plausibility check refuses a forecast several times the household's own
// rhythm, and a fixture that tripped it would be testing the wrong thing.
func forecastAnswer(months int) string {
	first := domain.MonthOf(domain.DateOf(time.Now()))
	rows := make([]string, 0, months)
	for index := 0; index < months; index++ {
		rows = append(rows, fmt.Sprintf(
			`{"month":%q,"money_in":"100.00","money_out":"120.00"}`, first.Shift(index)))
	}
	return `{"months":[` + strings.Join(rows, ",") +
		`],"narrative":"Two paychecks a month against about 120 of everyday spending."}`
}

func TestAProjectionWithNoForecastYetSaysWhyRatherThanFailing(t *testing.T) {
	// Never an error and never an empty chart: the card draws the arithmetic
	// and this line says where an estimate would come from.
	l := buildLedger(t)
	body := l.alex.get("/cash-flow-forecast").requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["available"])
	require.Contains(t, body["unavailable"], "No forecast has been made yet")
	require.Empty(t, body["windows"])
}

func TestTheForecastRunIsHandedAmountsWithoutPayeesAndItsAnswerIsStored(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply(forecastAnswer(6)))
	configure(l, model)
	id := l.alex.post("/assistant-automations", forecastAutomationBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run", map[string]any{}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])

	// What it was shown: the series, and nothing that names anybody.
	opening := model.seen[0]["messages"].([]any)[1].(map[string]any)["content"].(string)
	require.Contains(t, opening, "Money in and out, by month")
	require.Contains(t, opening, "Money in and out, by day")
	require.NotContains(t, opening, "CORNER STORE")
	require.NotContains(t, opening, "Safeway")
	require.NotContains(t, opening, "Everyday Checking")
	require.NotContains(t, opening, "Groceries")

	body := l.alex.get("/cash-flow-forecast").requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["available"])
	require.Equal(t, "test-model", body["model"], "the estimate says which model produced it")
	require.Equal(t, float64(0), body["age_days"])
	require.Contains(t, body["narrative"], "everyday spending")
	require.Len(t, body["months"], 6)

	windows := body["windows"].([]any)
	require.Len(t, windows, 4, "one per range the projection card offers")
	first := windows[0].(map[string]any)
	require.Equal(t, float64(30), first["days"])
	require.Equal(t, true, first["is_complete"])
	// Out runs above in in every forecast month, so every window's movement is
	// negative. The proration itself is arithmetic and is tested in the domain;
	// what this asserts is that the window carries figures at all.
	require.NotEqual(t, "0.00", first["money_in"])
	require.True(t, strings.HasPrefix(first["net"].(string), "-"), first["net"])
	require.NotNil(t, first["scheduled_balance"],
		"the estimate is reconciled against the projection the chart draws")
	require.NotNil(t, first["estimated_balance"])
	require.NotNil(t, first["difference"])

	longest := windows[3].(map[string]any)
	require.Equal(t, float64(180), longest["days"])
	// Six months from this month's first day run 181 days or more, so whether
	// 180 days from today fits depends on the day of the month the test runs.
	today := domain.DateOf(time.Now())
	lastForecastDay := domain.MonthOf(today).Shift(5).LastDay()
	require.Equal(t, !today.AddDays(180).After(lastForecastDay), longest["is_complete"],
		"a window is complete only when the six-month estimate reaches its last day")
}

func TestAForecastTheParserRefusesFailsTheRunAndLeavesTheProjectionAlone(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("I would rather not guess at your finances."))
	configure(l, model)
	id := l.alex.post("/assistant-automations", forecastAutomationBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run", map[string]any{}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunFailed, run["status"])
	require.Contains(t, run["error"], "no object was found")

	body := l.alex.get("/cash-flow-forecast").requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["available"], "nothing was stored, so nothing is shown")
}

func TestAForecastForTheWrongMonthsIsRefused(t *testing.T) {
	// A model answering for last year has not read the request. Stored, it
	// would draw a chart of a year nobody is in.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply(
		`{"months":[{"month":"2019-01","money_in":"100.00","money_out":"120.00"}],`+
			`"narrative":"Long ago."}`))
	configure(l, model)
	id := l.alex.post("/assistant-automations", forecastAutomationBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run", map[string]any{}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunFailed, run["status"])
	require.Contains(t, run["error"], "starts at 2019-01")
	require.Equal(t, false,
		l.alex.get("/cash-flow-forecast").requireStatus(http.StatusOK).json()["available"])
}

func TestADryRunOfTheForecastStoresNothing(t *testing.T) {
	// A dry run is for reading what would happen. It must not leave a forecast
	// the card would then draw as though a real run had made it.
	l := buildLedger(t)
	model := newFakeModel(t, answerReply(forecastAnswer(6)))
	configure(l, model)
	id := l.alex.post("/assistant-automations", forecastAutomationBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run", map[string]any{"dry_run": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])

	body := l.alex.get("/cash-flow-forecast").requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["available"])
}

func TestAForecastIsReadableByTheHouseholdAloneAndBadHorizonsAreRefused(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply(forecastAnswer(6)))
	configure(l, model)
	id := l.alex.post("/assistant-automations", forecastAutomationBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.post("/assistant-automations/"+id+"/run", map[string]any{}).
		requireStatus(http.StatusOK)

	// Somebody from another household reaches nothing here at all.
	l.as("bob").get("/cash-flow-forecast").requireStatus(http.StatusNotFound)
	// A viewer reads it: it is a read, like the projection itself.
	require.Equal(t, true,
		l.as("vera").get("/cash-flow-forecast").requireStatus(http.StatusOK).json()["available"])

	l.alex.get("/cash-flow-forecast?horizons=30,tuesday").requireStatus(http.StatusBadRequest)
	single := l.alex.get("/cash-flow-forecast?horizons=45").requireStatus(http.StatusOK).json()
	require.Len(t, single["windows"], 1)
}

func TestTheForecastRunIsHandedTheRemindersAsDatesAndAmounts(t *testing.T) {
	// On seriesClock's day the run estimates August through January, so it is shown
	// the reminders through the end of January and the rent from July nobody paid,
	// without the names the household gave them.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	newSeries(alex, l, map[string]any{
		"description": "HOMESTEAD RENTALS", "display_name": "Rent", "amount": "-640.00",
		"start_on": "2026-07-01",
	})
	newSeries(alex, l, map[string]any{
		"description": "WIDGETWORKS PAYROLL", "display_name": "Paycheck",
		"kind": string(domain.SeriesIncome), "amount": "910.00", "start_on": "2026-12-15",
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{15}},
	})

	model := newFakeModel(t, answerReply(forecastAnswer(6)))
	configure(l, model)
	id := alex.post("/assistant-automations", forecastAutomationBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	alex.post("/assistant-automations/"+id+"/run", map[string]any{}).requireStatus(http.StatusOK)

	opening := model.seen[0]["messages"].([]any)[1].(map[string]any)["content"].(string)
	require.NotContains(t, opening, "Rent")
	require.NotContains(t, opening, "Paycheck")
	require.NotContains(t, opening, "HOMESTEAD")

	_, after, found := strings.Cut(opening, "### Reminders")
	require.True(t, found, "the run is shown the reminders")
	_, block, _ := strings.Cut(after, "```json\n")
	block, _, _ = strings.Cut(block, "\n```")
	var rows [][]string
	require.NoError(t, json.Unmarshal([]byte(block), &rows))
	require.Equal(t, [][]string{
		{"2026-07-01", "0.00", "640.00", "past_due"},
		{"2026-08-01", "0.00", "640.00", "past_due"},
		{"2026-09-01", "0.00", "640.00", "upcoming"},
		{"2026-10-01", "0.00", "640.00", "upcoming"},
		{"2026-11-01", "0.00", "640.00", "upcoming"},
		{"2026-12-01", "0.00", "640.00", "upcoming"},
		{"2026-12-15", "910.00", "0.00", "upcoming"},
		{"2027-01-01", "0.00", "640.00", "upcoming"},
		{"2027-01-15", "910.00", "0.00", "upcoming"},
	}, rows)
}
