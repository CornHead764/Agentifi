package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Bills, income and the cash-flow projection, end to end.
// calculations.md §8 and §9.
//
// Two properties carry most of this file. A series has two names and only one
// of them is ever matched against, so renaming for the eye must not stop the
// rule firing. And the recurrence is an RRULE, not a menu: "twice a month" and
// "multiple fixed dates" are the same rule with a different number of month
// days, and a day-31 rule in February clamps rather than disappearing.

// seriesClock is the day these tests pretend it is. Occurrence status, the
// default horizon and the annualized year all read the clock, so it is frozen
// rather than left to drift into a year where the fixtures are history.
var seriesClock = time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)

// frozenClient is a caller whose "today" does not move.
func frozenClient(l *ledger, user string) *client {
	l.t.Helper()
	env := NewEnv(testConfig(), db(l.t), WithClock(func() time.Time { return seriesClock }))
	account, known := l.users[user]
	require.True(l.t, known, "no seeded user named %q", user)
	return (&client{t: l.t, env: env, handler: RouterFor(env)}).
		as(account).inSpace(store.SpaceIDOf(l.id("space")))
}

// newSeries posts a series and returns it, defaulting everything the caller
// did not name.
func newSeries(c *client, l *ledger, body map[string]any) map[string]any {
	c.t.Helper()
	payload := map[string]any{
		"account_id": l.str("checking"),
		"kind":       string(domain.SeriesBill),
		"amount":     "-100.00",
		"start_on":   "2026-01-01",
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
	}
	for key, value := range body {
		payload[key] = value
	}
	if _, named := payload["description"]; !named {
		payload["description"] = "ACME BILL AUTOPAY"
	}
	return c.post("/series", payload).requireStatus(http.StatusCreated).json()
}

func occurrencesIn(c *client, query string) map[string]any {
	c.t.Helper()
	return c.get("/occurrences?" + query).requireStatus(http.StatusOK).json()
}

func dueDates(t *testing.T, list map[string]any, seriesID string) []string {
	t.Helper()
	out := []string{}
	for _, raw := range list["items"].([]any) {
		item := raw.(map[string]any)
		if seriesID == "" || item["series_id"] == seriesID {
			out = append(out, item["due_on"].(string))
		}
	}
	return out
}

// --- Two names ---------------------------------------------------------------

func TestRenamingASeriesLeavesTheTextItMatchesOnAlone(t *testing.T) {
	// §9: description is what gets compared, display_name is what a person
	// reads, and every display path reads label. "Paycheck" shares nothing
	// with "ACME CORP DES:PAYROLL"; renaming the rule to read nicely must not
	// stop the bank's wording matching it.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	created := newSeries(alex, l, map[string]any{
		"description": "ACME CORP DES:PAYROLL",
		"kind":        string(domain.SeriesIncome),
		"amount":      "3000.00",
	})
	require.Equal(t, "ACME CORP DES:PAYROLL", created["description"])
	require.Nil(t, created["display_name"])
	require.Equal(t, "ACME CORP DES:PAYROLL", created["label"],
		"with no display name the label falls back to the matching text")

	renamed := alex.patch("/series/"+created["id"].(string),
		map[string]any{"display_name": "Paycheck"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "ACME CORP DES:PAYROLL", renamed["description"],
		"renaming the series rewrote the string matching compares")
	require.Equal(t, "Paycheck", renamed["display_name"])
	require.Equal(t, "Paycheck", renamed["label"])

	// Both names find it, which is what proves the matching text survived.
	require.Len(t, alex.get("/series?search=ACME").requireStatus(http.StatusOK).list(), 1)
	require.Len(t, alex.get("/series?search=Paycheck").requireStatus(http.StatusOK).list(), 1)
}

func TestEditingTheScheduleMovesThePointerOntoTheNewRule(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	space := store.SpaceIDOf(l.id("space"))

	// Never fired: the pointer starts over from the new start date.
	fresh := newSeries(alex, l, map[string]any{"description": "WATER UTILITY"})
	id := fresh["id"].(string)
	require.Equal(t, "2026-01-01", fresh["next_due_on"])
	moved := alex.patch("/series/"+id, map[string]any{
		"start_on":   "2026-01-15",
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{15}},
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-01-15", moved["next_due_on"],
		"the pointer stayed on the old rule's date, a slot nobody can see")
	january := occurrencesIn(alex, "from=2026-01-01&to=2026-01-31")
	require.Equal(t, []string{"2026-01-15"}, dueDates(t, january, id))

	// Fired: the pointer keeps its place in time and lands on the new rule's
	// next slot, rather than winding back to the start.
	rent := newSeries(alex, l, map[string]any{"description": "RENT"})
	rentID := rent["id"].(string)
	_, err := alex.env.DB.Pool().Exec(t.Context(),
		`UPDATE series SET next_due_on = '2026-10-01' WHERE space_id = $1 AND id = $2`,
		space.UUID(), uuid.MustParse(rentID))
	require.NoError(t, err)
	repointed := alex.patch("/series/"+rentID, map[string]any{
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{5}},
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-10-05", repointed["next_due_on"])

	// An edit that leaves the schedule alone leaves the pointer alone too.
	renamed := alex.patch("/series/"+rentID, map[string]any{"display_name": "Home"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-10-05", renamed["next_due_on"])
}

func TestASeriesWithoutADescriptionIsRefused(t *testing.T) {
	l := buildLedger(t)
	response := frozenClient(l, "alex").post("/series", map[string]any{
		"account_id":  l.str("checking"),
		"kind":        string(domain.SeriesBill),
		"description": "   ",
		"amount":      "-10.00",
		"start_on":    "2026-01-01",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
	})
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "description")
}

func TestAnOccurrenceIsLabelledNeverDescribed(t *testing.T) {
	// Nothing on the calendar matches on wording, so an occurrence carries the
	// label and not the matching text.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description":  "CITY POWER AUTOPAY",
		"display_name": "Power bill",
	})

	list := occurrencesIn(alex, "from=2026-09-01&to=2026-09-30")
	found := false
	for _, raw := range list["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] == created["id"] {
			found = true
			require.Equal(t, "Power bill", item["label"])
			require.NotContains(t, item, "description")
		}
	}
	require.True(t, found)
}

// --- The rule is an array of month days --------------------------------------

func TestTwiceAMonthIsTwoEntriesInByMonthDayNotAFrequencyOfItsOwn(t *testing.T) {
	// §9: TWICE_A_MONTH is FREQ=MONTHLY;BYMONTHDAY=[n,m]. The alias labels the
	// rule; it never replaces it.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "RENT HALF",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1, 15}},
	})

	recurrence := created["recurrence"].(map[string]any)
	require.Equal(t, string(domain.AliasTwiceAMonth), recurrence["alias"])
	require.Equal(t, "MONTHLY", recurrence["frequency"])
	require.Equal(t, float64(1), recurrence["interval"])
	require.Equal(t, []any{float64(1), float64(15)}, recurrence["by_month_day"])

	list := occurrencesIn(alex, "from=2026-09-01&to=2026-09-30")
	require.Equal(t, []string{"2026-09-01", "2026-09-15"}, dueDates(t, list, created["id"].(string)))
}

func TestMultipleFixedDatesIsTheSameRuleWithMoreMonthDays(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "CARD PAYMENT",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1, 10, 20}},
	})

	recurrence := created["recurrence"].(map[string]any)
	require.Equal(t, string(domain.AliasMultipleFixed), recurrence["alias"])
	require.Equal(t, "MONTHLY", recurrence["frequency"],
		"multiple fixed dates became a frequency of its own")

	list := occurrencesIn(alex, "from=2026-09-01&to=2026-09-30")
	require.Equal(t, []string{"2026-09-01", "2026-09-10", "2026-09-20"},
		dueDates(t, list, created["id"].(string)))
}

func TestADayThirtyOneRuleClampsToTheEndOfFebruary(t *testing.T) {
	// §9: never skipped, never rolled into March. A rent reminder that
	// vanishes in February is a missed payment; one that reappears on 3 March
	// is a duplicate of the March occurrence.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "RENT",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{31}},
	})
	id := created["id"].(string)

	require.Equal(t, []string{"2027-02-28"},
		dueDates(t, occurrencesIn(alex, "from=2027-02-01&to=2027-02-28"), id))
	// The clamped February occurrence does not reappear at the start of March.
	require.Equal(t, []string{"2027-03-31"},
		dueDates(t, occurrencesIn(alex, "from=2027-03-01&to=2027-03-31"), id))
	// And a leap February clamps to the 29th.
	require.Equal(t, []string{"2028-02-29"},
		dueDates(t, occurrencesIn(alex, "from=2028-02-01&to=2028-02-29"), id))
}

func TestByMonthDayIsAlwaysAnArrayOnTheWire(t *testing.T) {
	// A client that has to check for null before iterating the month days
	// will forget once.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "ONE OFF",
		"recurrence":  map[string]any{},
	})
	recurrence := created["recurrence"].(map[string]any)
	require.Equal(t, string(domain.AliasOneTime), recurrence["alias"])
	require.Equal(t, []any{}, recurrence["by_month_day"])
	require.Equal(t, []any{}, recurrence["by_day"])
}

func TestAnImpossibleRuleIsRefused(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	for _, recurrence := range []map[string]any{
		{"frequency": "MONTHLY", "by_month_day": []int{32}},
		{"frequency": "MONTHLY", "by_month_day": []int{0}},
		{"frequency": "WEEKLY", "by_day": []string{"FUNDAY"}},
		{"frequency": "DAILY", "interval": 0},
	} {
		alex.post("/series", map[string]any{
			"account_id":  l.str("checking"),
			"kind":        string(domain.SeriesBill),
			"description": "BAD RULE",
			"amount":      "-10.00",
			"start_on":    "2026-01-01",
			"recurrence":  recurrence,
		}).requireStatus(http.StatusUnprocessableEntity)
	}
}

func TestTheAnnualizedAmountComesFromExpandingTheRuleNotFromATable(t *testing.T) {
	// §9: biweekly is 26 or 27 depending on the year, and "extra paycheck
	// month" is a shipped notification that depends on getting this right.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	biweekly := newSeries(alex, l, map[string]any{
		"description": "ACME CORP DES:PAYROLL",
		"kind":        string(domain.SeriesIncome),
		"amount":      "1000.00",
		"start_on":    "2026-01-02",
		"recurrence":  map[string]any{"frequency": "DAILY", "interval": 14},
	})

	perYear := int(biweekly["occurrences_per_year"].(float64))
	require.Contains(t, []int{26, 27}, perYear,
		"a fortnightly rule was counted from a lookup table")
	require.Equal(t,
		domain.MustFromString("1000.00").Scale(decimalOf(perYear)).String(),
		biweekly["annualized_amount"])

	// The count is the expansion, so the calendar has to agree with it.
	year := occurrencesIn(alex, "from=2026-01-01&to=2026-12-31")
	require.Len(t, dueDates(t, year, biweekly["id"].(string)), perYear)

	monthly := newSeries(alex, l, map[string]any{"description": "RENT"})
	require.Equal(t, float64(12), monthly["occurrences_per_year"])
	require.Equal(t, "-1200.00", monthly["annualized_amount"])
}

func decimalOf(count int) domain.Rate {
	return domain.MustFromString(fmt.Sprintf("%d", count)).Decimal()
}

// --- Occurrences -------------------------------------------------------------

func TestAnOccurrenceBeforeTodayIsPastDue(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "RENT"})
	id := created["id"].(string)

	list := occurrencesIn(alex, "from=2026-08-01&to=2026-09-30")
	statuses := map[string]string{}
	for _, raw := range list["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] == id {
			statuses[item["due_on"].(string)] = item["status"].(string)
		}
	}
	require.Equal(t, "past_due", statuses["2026-08-01"])
	require.Equal(t, "upcoming", statuses["2026-09-01"])
	require.Equal(t, float64(1), list["summary"].(map[string]any)["past_due"])
}

func TestAcceptingAnOccurrenceRecordsTheChargeAndAdvancesThePointer(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description":  "CITY POWER AUTOPAY",
		"display_name": "Power bill",
	})
	id := created["id"].(string)
	require.Equal(t, "2026-01-01", created["due_on"])

	charge := alex.post("/occurrences/accept", map[string]any{
		"series_id": id, "due_on": "2026-01-01",
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "-100.00", charge["amount"])
	// The charge carries the bank-facing text and the human-facing one in the
	// fields each belongs in.
	require.Equal(t, "CITY POWER AUTOPAY", charge["statement_name"])
	require.Equal(t, "Power bill", charge["payee"])

	after := alex.get("/series/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-02-01", after["next_due_on"])

	// The paid slot is not projected again, and reads back as paid when asked
	// for explicitly.
	require.NotContains(t,
		dueDates(t, occurrencesIn(alex, "from=2026-01-01&to=2026-01-31"), id), "2026-01-01")
	fulfilled := occurrencesIn(alex, "from=2026-01-01&to=2026-01-31&include_fulfilled=true")
	for _, raw := range fulfilled["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] == id && item["due_on"] == "2026-01-01" {
			require.Equal(t, "paid", item["status"])
			require.Equal(t, charge["id"], item["transaction_id"])
		}
	}
}

func TestBackFillingAnOlderOccurrenceLeavesThePointerAlone(t *testing.T) {
	// §9: advancing on a back-fill silently skips a payment still to come.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "RENT"})
	id := created["id"].(string)

	// February is on or after the pointer (January), so it is not a
	// back-fill: the pointer moves past it.
	alex.post("/occurrences/accept", map[string]any{
		"series_id": id, "due_on": "2026-02-01", "amount": "-100.00",
	}).requireStatus(http.StatusCreated)
	after := alex.get("/series/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-03-01", after["next_due_on"])

	// January is before the pointer: a back-fill.
	alex.post("/occurrences/accept", map[string]any{
		"series_id": id, "due_on": "2026-01-01", "amount": "-100.00",
	}).requireStatus(http.StatusCreated)
	after = alex.get("/series/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-03-01", after["next_due_on"], "a back-fill moved the schedule pointer")
}

func TestAProvidersForecastRowLeavesTheBillStillDue(t *testing.T) {
	// Simplifi materialises what it expects: the import brings across rows
	// dated in the future, one per upcoming bill, each claiming its occurrence's
	// slot. Read as payments they would mark every bill due next month "paid",
	// leaving Bills & Income with nothing upcoming.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "RENT",
		"amount":      "-100.00",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
	})
	id := created["id"].(string)

	// Dated after the frozen clock: the money has not moved yet.
	forecast := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.September, 1),
		Amount: domain.MustFromString("-100.00"), StatementName: "RENT", Payee: "Rent",
		SeriesID: uuid.MustParse(id), SeriesDueOn: domain.NewDate(2026, time.September, 1),
	}
	seedTxn(l, "forecast", forecast)

	listed := occurrencesIn(alex, "from=2026-09-01&to=2026-09-30")
	require.Contains(t, dueDates(t, listed, id), "2026-09-01",
		"a bill the provider has written a row for is still a bill that is due")

	for _, raw := range listed["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] != id {
			continue
		}
		require.Equal(t, "upcoming", item["status"], "nothing has been paid yet")
		require.Equal(t, l.str("forecast"), item["transaction_id"],
			"the scheduled row is still worth linking to")
	}
}

// The register and the Bills screen want opposite things from the same row,
// which is why a forecast is an estimate rather than a pending charge: the
// occurrence keeps its link to the row it was written for, and the ledger a
// person reads is the money that moved.
func TestAForecastIsAnUpcomingBillAndNotARegisterRow(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "RENT",
		"amount":      "-100.00",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
	})
	id := created["id"].(string)

	seedTxn(l, "forecast", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.September, 1),
		Amount: domain.MustFromString("-100.00"), StatementName: "RENT", Payee: "Rent",
		Source:      domain.SourceSimplifiImport,
		SeriesID:    uuid.MustParse(id),
		SeriesDueOn: domain.NewDate(2026, time.September, 1),
		// What the import writes for a scheduled-transaction forecast.
		EstimateStatus: store.ProjectedEstimate,
	})

	rows := alex.get("/transactions?from=2000-01-01&to=2100-01-01").
		requireStatus(http.StatusOK).json()["items"].([]any)
	for _, raw := range rows {
		require.NotEqual(t, l.str("forecast"), raw.(map[string]any)["id"],
			"a bill nobody has paid is not a transaction")
	}

	found := 0
	for _, raw := range occurrencesIn(alex, "from=2026-09-01&to=2026-09-30")["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] != id {
			continue
		}
		found++
		require.Equal(t, "upcoming", item["status"])
		require.Equal(t, l.str("forecast"), item["transaction_id"],
			"the occurrence still names the row it was written for")
	}
	require.Equal(t, 1, found, "the bill has to reach the screen to be checked at all")
}

// The due date passing does not pay the bill. An import's forecasts can be
// dated before the day it ran: read as payments they close an occurrence
// that nothing ever matched.
func TestAForecastWhoseDueDateHasPassedIsNotPaid(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "WATER",
		"amount":      "-100.00",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{15}},
	})
	id := created["id"].(string)

	// A week before the frozen clock, and still nothing has posted against it.
	seedTxn(l, "stale", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 15),
		Amount: domain.MustFromString("-100.00"), StatementName: "WATER", Payee: "Water",
		Source:         domain.SourceSimplifiImport,
		SeriesID:       uuid.MustParse(id),
		SeriesDueOn:    domain.NewDate(2026, time.August, 15),
		EstimateStatus: store.ProjectedEstimate,
	})

	found := 0
	for _, raw := range occurrencesIn(alex, "from=2026-08-01&to=2026-08-31")["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] != id {
			continue
		}
		found++
		require.Equal(t, "past_due", item["status"], "a forecast pays nothing")
	}
	require.Equal(t, 1, found, "the bill has to reach the screen to be checked at all")
}

func TestAProvidersForecastRowIsNotProjectedOnTopOfItself(t *testing.T) {
	// The other half: the amount is in the ledger, so the projection must not
	// add the occurrence again on top of the balance that already carries it.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "RENT",
		"amount":      "-40.00",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{5}},
	})

	seedTxn(l, "forecast", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.September, 5),
		Amount: domain.MustFromString("-40.00"), StatementName: "RENT", Payee: "Rent",
		SeriesID:    uuid.MustParse(created["id"].(string)),
		SeriesDueOn: domain.NewDate(2026, time.September, 5),
	})

	checking := lineFor(t, cashFlow(alex, "from=2026-09-01&to=2026-09-30"), l.str("checking"))
	points := checking["points"].([]any)

	// The window opens before the money moves, so the line starts whole and
	// falls once, on the day the bill is due. Twice would be the double count.
	require.Equal(t, "125.00", checking["starting_balance"])
	require.Equal(t, "125.00", points[3].(map[string]any)["balance"])
	require.Equal(t, "85.00", points[4].(map[string]any)["balance"], "the 5 September bill")
	require.Equal(t, "85.00", points[29].(map[string]any)["balance"],
		"the scheduled charge was laid out twice")
}

func TestSkippingAnOccurrenceStopsItBeingProjectedAgain(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "GYM MEMBERSHIP"})
	id := created["id"].(string)

	alex.post("/occurrences/skip", map[string]any{
		"series_id": id, "due_on": "2026-09-01",
	}).requireStatus(http.StatusNoContent)

	require.NotContains(t,
		dueDates(t, occurrencesIn(alex, "from=2026-09-01&to=2026-09-30"), id), "2026-09-01")

	fulfilled := occurrencesIn(alex, "from=2026-09-01&to=2026-09-30&include_fulfilled=true")
	for _, raw := range fulfilled["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] == id && item["due_on"] == "2026-09-01" {
			require.Equal(t, "skipped", item["status"])
		}
	}
	// A skip is a tombstone, not a payment: it never reaches the register.
	page := l.alex.get("/transactions?limit=500").requireStatus(http.StatusOK).json()
	for _, raw := range page["items"].([]any) {
		require.NotEqual(t, "GYM MEMBERSHIP", raw.(map[string]any)["statement_name"])
	}
}

func TestASlotTheRuleNeverFiresOnIsRefused(t *testing.T) {
	// Accepting a day the series does not fall on would create a slot no
	// expansion can ever match, and the occurrence would be projected forever.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "RENT"})

	alex.post("/occurrences/accept", map[string]any{
		"series_id": created["id"], "due_on": "2026-09-17",
	}).requireStatus(http.StatusConflict)
}

// --- Cash flow ---------------------------------------------------------------

func cashFlow(c *client, query string) map[string]any {
	c.t.Helper()
	return c.get("/cash-flow?" + query).requireStatus(http.StatusOK).json()
}

func lineFor(t *testing.T, body map[string]any, accountID string) map[string]any {
	t.Helper()
	for _, raw := range body["accounts"].([]any) {
		line := raw.(map[string]any)
		if line["account_id"] == accountID {
			return line
		}
	}
	t.Fatalf("no cash flow line for account %s", accountID)
	return nil
}

func TestCashFlowProjectsOneLinePerAccountFromItsOwnBalance(t *testing.T) {
	// §8: balance(d) = current balance + every occurrence due by d, per
	// account, with the account selector a filter over the same expansion.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	newSeries(alex, l, map[string]any{
		"description": "RENT",
		"amount":      "-40.00",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{5}},
	})

	body := cashFlow(alex, "from=2026-09-01&to=2026-09-30")
	require.Len(t, body["accounts"], 2, "one line per account, whether or not it has bills")

	checking := lineFor(t, body, l.str("checking"))
	require.Equal(t, "125.00", checking["starting_balance"])

	points := checking["points"].([]any)
	require.Len(t, points, 30)
	require.Equal(t, "125.00", points[0].(map[string]any)["balance"])
	require.Equal(t, "125.00", points[3].(map[string]any)["balance"])
	require.Equal(t, "85.00", points[4].(map[string]any)["balance"], "the 5 September bill")
	require.Equal(t, "85.00", points[29].(map[string]any)["balance"])

	// The card has no series, so its line is flat at its own balance rather
	// than borrowing the other account's occurrences.
	card := lineFor(t, body, l.str("card"))
	require.Equal(t, "-300.00", card["starting_balance"])
	require.Equal(t, "-300.00", card["points"].([]any)[29].(map[string]any)["balance"])
}

func TestTheLowBalanceWarningIsTheProjectionCrossingAThreshold(t *testing.T) {
	// §8: the notification is this function crossing a line, not a second
	// calculation. The day it names has to be a day on the chart.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	newSeries(alex, l, map[string]any{
		"description": "RENT",
		"amount":      "-40.00",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{5}},
	})

	body := cashFlow(alex, "from=2026-09-01&to=2026-09-30&threshold=100.00")
	require.Equal(t, "100.00", body["threshold"])
	checking := lineFor(t, body, l.str("checking"))

	below := checking["first_below"].(map[string]any)
	require.Equal(t, "2026-09-05", below["on"])
	require.Equal(t, "85.00", below["balance"])

	// It is the first point on this line that is under the line, and the
	// lowest is the worst point on the same series.
	points := checking["points"].([]any)
	threshold := domain.MustFromString("100.00")
	expected := ""
	lowest := domain.MustFromString("999999.00")
	for _, raw := range points {
		point := raw.(map[string]any)
		balance := domain.MustFromString(point["balance"].(string))
		if expected == "" && balance.LessThan(threshold) {
			expected = point["on"].(string)
		}
		if balance.LessThan(lowest) {
			lowest = balance
		}
	}
	require.Equal(t, expected, below["on"])
	require.Equal(t, lowest.String(), checking["lowest"].(map[string]any)["balance"])

	// A line that never crosses has no warning at all, rather than a warning
	// dated at the end of the window.
	require.Nil(t, lineFor(t, cashFlow(alex, "from=2026-09-01&to=2026-09-30"), l.str("checking"))["first_below"])
}

func TestTheAccountSelectorIsAFilterOverOneExpansion(t *testing.T) {
	// Not a second query: a line drawn alone must be the same line drawn
	// beside the others.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	newSeries(alex, l, map[string]any{
		"description": "RENT",
		"amount":      "-40.00",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{5}},
	})

	everything := cashFlow(alex, "from=2026-09-01&to=2026-09-30")
	selected := cashFlow(alex, "from=2026-09-01&to=2026-09-30&account_id="+l.str("checking"))

	require.Len(t, selected["accounts"], 1)
	require.Equal(t, lineFor(t, everything, l.str("checking")),
		lineFor(t, selected, l.str("checking")))

	// The markers come from the same expansion the lines were projected from.
	for _, raw := range selected["occurrences"].([]any) {
		require.Equal(t, l.str("checking"), raw.(map[string]any)["account_id"])
	}
	require.Equal(t, selected["combined"], lineFor(t, selected, l.str("checking"))["points"])
}

func TestAnAcceptedBillIsLaidOutOnceRatherThanTwice(t *testing.T) {
	// Accepting an occurrence writes a row dated the day it falls due. The
	// projection opens the day before the window, so that row is not yet in the
	// balance and the occurrence is what puts it there. Counting both would
	// charge the user twice for their own rent.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "RENT",
		"amount":      "-40.00",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{5}},
	})

	alex.post("/occurrences/accept", map[string]any{
		"series_id": created["id"], "due_on": "2026-09-05",
	}).requireStatus(http.StatusCreated)

	checking := lineFor(t, cashFlow(alex, "from=2026-09-01&to=2026-09-30"), l.str("checking"))
	require.Equal(t, "125.00", checking["starting_balance"],
		"the charge falls due inside the window, so it is not in the opening figure")
	points := checking["points"].([]any)
	require.Equal(t, "85.00", points[29].(map[string]any)["balance"],
		"the accepted bill was projected on top of the row accepting it wrote")
}

// --- The wire ----------------------------------------------------------------

func TestASeriesAmountMustBeAJSONString(t *testing.T) {
	// Ground rule 1: refused at the door, not rounded later.
	l := buildLedger(t)
	response := frozenClient(l, "alex").raw(http.MethodPost, "/series", fmt.Sprintf(
		`{"account_id": %q, "kind": "bill", "description": "RENT", "amount": -100.5,
		  "start_on": "2026-01-01", "recurrence": {"frequency": "MONTHLY", "by_month_day": [1]}}`,
		l.str("checking")))
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "string")
}

func TestEverySeriesFigureCrossesTheWireAsAString(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "RENT"})
	require.Equal(t, "-100.00", created["amount"])
	require.IsType(t, "", created["annualized_amount"])

	list := occurrencesIn(alex, "from=2026-09-01&to=2026-09-30")
	require.IsType(t, "", list["items"].([]any)[0].(map[string]any)["amount"])
	require.IsType(t, "", list["summary"].(map[string]any)["net"])
}

func TestAnUnknownSeriesKindIsRefused(t *testing.T) {
	l := buildLedger(t)
	frozenClient(l, "alex").post("/series", map[string]any{
		"account_id":  l.str("checking"),
		"kind":        "groceries",
		"description": "RENT",
		"amount":      "-10.00",
		"start_on":    "2026-01-01",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestALimitedRangeNeedsBothItsBounds(t *testing.T) {
	// §9: the amount tolerance is a band, and a half-specified band would
	// match everything or nothing depending on which end was missing.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	alex.post("/series", map[string]any{
		"account_id":       l.str("checking"),
		"kind":             string(domain.SeriesBill),
		"description":      "RENT",
		"amount":           "-100.00",
		"start_on":         "2026-01-01",
		"recurrence":       map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
		"match_criteria":   string(domain.CriteriaRange),
		"match_amount_min": "-120.00",
	}).requireStatus(http.StatusConflict)

	created := newSeries(alex, l, map[string]any{
		"description":      "RENT",
		"match_criteria":   string(domain.CriteriaRange),
		"match_amount_min": "-120.00",
		"match_amount_max": "-80.00",
	})
	require.Equal(t, "-120.00", created["match_amount_min"])
	require.Equal(t, "-80.00", created["match_amount_max"])
}

// --- Tenancy -----------------------------------------------------------------

func TestASeriesInAnotherSpaceIsInvisible(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	stranger := (&client{t: t, env: alex.env, handler: RouterFor(alex.env)}).
		as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	theirs := stranger.post("/series", map[string]any{
		"account_id":  l.str("stranger_account"),
		"kind":        string(domain.SeriesBill),
		"description": "NOT YOURS",
		"amount":      "-10.00",
		"start_on":    "2026-01-01",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
	}).requireStatus(http.StatusCreated).json()
	id := theirs["id"].(string)

	alex.get("/series/" + id).requireStatus(http.StatusNotFound)
	alex.patch("/series/"+id, map[string]any{"display_name": "Mine now"}).
		requireStatus(http.StatusNotFound)
	alex.del("/series/" + id).requireStatus(http.StatusNotFound)
	alex.post("/occurrences/accept", map[string]any{"series_id": id, "due_on": "2026-09-01"}).
		requireStatus(http.StatusNotFound)
	alex.post("/occurrences/skip", map[string]any{"series_id": id, "due_on": "2026-09-01"}).
		requireStatus(http.StatusNotFound)

	require.Empty(t, alex.get("/series").requireStatus(http.StatusOK).list())
	require.Empty(t, occurrencesIn(alex, "from=2026-09-01&to=2026-09-30")["items"])
}

func TestASeriesCannotBorrowAnAccountFromAnotherSpace(t *testing.T) {
	l := buildLedger(t)
	frozenClient(l, "alex").post("/series", map[string]any{
		"account_id":  l.str("stranger_account"),
		"kind":        string(domain.SeriesBill),
		"description": "RENT",
		"amount":      "-10.00",
		"start_on":    "2026-01-01",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
	}).requireStatus(http.StatusConflict)
}

func TestAViewerReadsTheBillsButChangesNothing(t *testing.T) {
	l := buildLedger(t)
	newSeries(frozenClient(l, "alex"), l, map[string]any{"description": "RENT"})
	vera := frozenClient(l, "vera")

	vera.get("/series").requireStatus(http.StatusOK)
	vera.get("/occurrences?from=2026-09-01&to=2026-09-30").requireStatus(http.StatusOK)
	vera.get("/cash-flow?from=2026-09-01&to=2026-09-30").requireStatus(http.StatusOK)
	vera.get("/series/suggested").requireStatus(http.StatusOK)
	vera.get("/series/refunds").requireStatus(http.StatusOK)

	id := vera.get("/series").requireStatus(http.StatusOK).list()[0]["id"].(string)
	vera.post("/series", map[string]any{"description": "Mine"}).requireStatus(http.StatusForbidden)
	vera.patch("/series/"+id, map[string]any{"display_name": "Mine"}).requireStatus(http.StatusForbidden)
	vera.del("/series/" + id).requireStatus(http.StatusForbidden)
	vera.post("/occurrences/accept", map[string]any{"series_id": id, "due_on": "2026-09-01"}).
		requireStatus(http.StatusForbidden)
	vera.post("/occurrences/skip", map[string]any{"series_id": id, "due_on": "2026-09-01"}).
		requireStatus(http.StatusForbidden)
}

func TestADeletedSeriesStopsProjectingButKeepsItsHistory(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "OLD GYM"})
	id := created["id"].(string)

	charge := alex.post("/occurrences/accept", map[string]any{
		"series_id": id, "due_on": "2026-01-01",
	}).requireStatus(http.StatusCreated).json()

	alex.del("/series/" + id).requireStatus(http.StatusNoContent)
	alex.get("/series/" + id).requireStatus(http.StatusNotFound)
	require.Empty(t, dueDates(t, occurrencesIn(alex, "from=2026-09-01&to=2026-09-30"), id))

	// The charge it already matched is still in the register.
	l.alex.get("/transactions/" + charge["id"].(string)).requireStatus(http.StatusOK)
}

// Dismissing a suggestion.
//
// The sweep re-derives its proposals on every read, so without a record of
// what was waved away the same one comes back every time — and another charge
// joining the group makes it look newly interesting rather than newly ignored.

const dismissedSignature = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// seedRepeatingCharge writes the same charge on the same day of four
// consecutive months, which is what the sweep reads as a pattern —
// service.MinOccurrences is three.
func seedRepeatingCharge(l *ledger, name string) {
	l.t.Helper()
	for i := 4; i >= 1; i-- {
		month := seriesClock.AddDate(0, -i, 0)
		seedTxn(l, fmt.Sprintf("suggestion-%s-%d", name, i), &store.Transaction{
			AccountID:     l.id("checking"),
			Date:          domain.DateOf(month),
			EffectiveDate: domain.DateOf(month),
			Amount:        domain.MustFromString("-42.00"),
			StatementName: name,
			Payee:         name,
		})
	}
}

func TestADismissedSuggestionStaysDismissed(t *testing.T) {
	l := buildLedger(t)
	seedRepeatingCharge(l, "GLACIER FITNESS CLUB")
	c := frozenClient(l, "alex")

	before := c.get("/series/suggested").requireStatus(http.StatusOK).list()
	require.NotEmpty(t, before, "four identical monthly charges implied no series")
	signature := before[0]["signature"].(string)

	c.post("/series/suggested/"+signature+"/dismiss", nil).requireStatus(http.StatusNoContent)

	after := c.get("/series/suggested").requireStatus(http.StatusOK).list()
	for _, one := range after {
		require.NotEqual(t, signature, one["signature"], "a dismissed suggestion came back")
	}
	// The waved-away pile is its own list, so a person can find it to wave
	// it back.
	dismissed := c.get("/series/suggested?dismissed=true").requireStatus(http.StatusOK).list()
	require.Len(t, dismissed, 1)
	require.Equal(t, signature, dismissed[0]["signature"])

	// And it can be put back in play.
	c.del("/series/suggested/" + signature + "/dismiss").requireStatus(http.StatusNoContent)
	restored := c.get("/series/suggested").requireStatus(http.StatusOK).list()
	require.Len(t, restored, len(before))
}

func TestDismissingIsIdempotentAndRestoringWhatWasNotDismissedIs404(t *testing.T) {
	l := buildLedger(t)
	c := frozenClient(l, "alex")

	c.post("/series/suggested/"+dismissedSignature+"/dismiss", nil).requireStatus(http.StatusNoContent)
	c.post("/series/suggested/"+dismissedSignature+"/dismiss", nil).requireStatus(http.StatusNoContent)

	c.del("/series/suggested/" + dismissedSignature + "/dismiss").requireStatus(http.StatusNoContent)
	c.del("/series/suggested/" + dismissedSignature + "/dismiss").requireStatus(http.StatusNotFound)
}

func TestASignatureThatIsNotADigestIsRefused(t *testing.T) {
	// The column only ever holds digests; arbitrary text would be harmless but
	// permanent litter.
	c := frozenClient(buildLedger(t), "alex")
	c.post("/series/suggested/not-a-digest/dismiss", nil).
		requireStatus(http.StatusUnprocessableEntity)
	c.post("/series/suggested/"+strings.Repeat("z", 64)+"/dismiss", nil).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestAViewerCannotDismissASuggestion(t *testing.T) {
	l := buildLedger(t)
	frozenClient(l, "vera").
		post("/series/suggested/"+dismissedSignature+"/dismiss", nil).
		requireStatus(http.StatusForbidden)
}

// Making one transaction recurring.
//
// The sweep only volunteers a series once a pattern is thick enough to be
// worth interrupting for. The register's *Create a series* action is the other
// direction: the user has already said this repeats, so the answer is never
// "no pattern found" — a lone charge comes back as a monthly series on its own
// amount and date, and the history only decides how much was filled in for
// them.

func TestASingleChargeStillYieldsASeriesToCreate(t *testing.T) {
	l := buildLedger(t)
	id := seedTxn(l, "lonely", &store.Transaction{
		AccountID:     l.id("checking"),
		Date:          domain.DateOf(seriesClock.AddDate(0, 0, -3)),
		EffectiveDate: domain.DateOf(seriesClock.AddDate(0, 0, -3)),
		Amount:        domain.MustFromString("-58.40"),
		StatementName: "HARBOR POINT DENTAL",
		Payee:         "Harbor Point Dental",
	})
	c := frozenClient(l, "alex")

	// Too thin for the sweep to have proposed anything.
	for _, one := range c.get("/series/suggested").requireStatus(http.StatusOK).list() {
		require.NotEqual(t, "HARBOR POINT DENTAL", one["description"])
	}

	one := c.get("/series/suggested/for/" + id.String()).requireStatus(http.StatusOK).json()
	require.Equal(t, "HARBOR POINT DENTAL", one["description"],
		"the series must match on the bank's wording, not the edited payee")
	require.Equal(t, "Harbor Point Dental", one["display_name"])
	require.Equal(t, "-58.40", one["amount"], "money crosses the wire as a string")
	require.Equal(t, string(domain.SeriesBill), one["kind"], "money leaving is a bill")
	require.Equal(t, l.str("checking"), one["account_id"])
	require.EqualValues(t, 1, one["occurrences"], "one charge is one occurrence")

	recurrence := one["recurrence"].(map[string]any)
	require.Equal(t, "MONTHLY", recurrence["frequency"])

	// And it starts in the future: a series starting in the past
	// re-materializes occurrences the ledger already holds.
	require.GreaterOrEqual(t, one["start_on"].(string), domain.DateOf(seriesClock).String())
}

func TestASuggestionForARepeatingChargeReadsTheCadenceOffTheHistory(t *testing.T) {
	l := buildLedger(t)
	seedRepeatingCharge(l, "GLACIER FITNESS CLUB")
	c := frozenClient(l, "alex")

	// Any one of the four rows answers with the whole group.
	id := l.id("suggestion-GLACIER FITNESS CLUB-1")
	one := c.get("/series/suggested/for/" + id.String()).requireStatus(http.StatusOK).json()

	require.Equal(t, "GLACIER FITNESS CLUB", one["description"])
	require.Equal(t, "-42.00", one["amount"])
	require.EqualValues(t, 4, one["occurrences"],
		"the cadence came off one row rather than off the group it belongs to")
	require.Equal(t, "MONTHLY", one["recurrence"].(map[string]any)["frequency"])
	require.Len(t, one["transaction_ids"].([]any), 4)

	// It is the same proposal the sweep makes, so accepting either one twice
	// cannot produce two series for one pattern.
	swept := c.get("/series/suggested").requireStatus(http.StatusOK).list()
	require.NotEmpty(t, swept)
	require.Equal(t, swept[0]["signature"], one["signature"])
}

func TestARowAlreadyInASeriesHasNothingToSuggest(t *testing.T) {
	l := buildLedger(t)
	c := frozenClient(l, "alex")
	series := newSeries(c, l, map[string]any{"description": "ORCHARD LANE HOA"})

	id := seedTxn(l, "already-linked", &store.Transaction{
		AccountID:     l.id("checking"),
		Date:          domain.DateOf(seriesClock),
		EffectiveDate: domain.DateOf(seriesClock),
		Amount:        domain.MustFromString("-100.00"),
		StatementName: "ORCHARD LANE HOA",
		Payee:         "Orchard Lane HOA",
		SeriesID:      uuid.MustParse(series["id"].(string)),
	})

	c.get("/series/suggested/for/" + id.String()).requireStatus(http.StatusNotFound)
}

func TestASuggestionCannotBeAskedForARowInAnotherSpace(t *testing.T) {
	l := buildLedger(t)
	id := seedTxn(l, "private", &store.Transaction{
		AccountID:     l.id("checking"),
		Date:          domain.DateOf(seriesClock),
		EffectiveDate: domain.DateOf(seriesClock),
		Amount:        domain.MustFromString("-12.00"),
		StatementName: "SOMEBODY ELSES CHARGE",
		Payee:         "Somebody Else",
	})
	// An unrelated id is the same answer as one in another space: a 404 that
	// says nothing about whether the row exists.
	c := frozenClient(l, "alex")
	c.get("/series/suggested/for/" + uuid.NewString()).requireStatus(http.StatusNotFound)
	c.get("/series/suggested/for/not-a-uuid").requireStatus(http.StatusNotFound)
	c.get("/series/suggested/for/" + id.String()).requireStatus(http.StatusOK)
}

func TestAViewerMayAskWhatASeriesWouldLookLike(t *testing.T) {
	// Reading a proposal changes nothing, so it is a Read route.
	l := buildLedger(t)
	id := seedTxn(l, "viewable", &store.Transaction{
		AccountID:     l.id("checking"),
		Date:          domain.DateOf(seriesClock),
		EffectiveDate: domain.DateOf(seriesClock),
		Amount:        domain.MustFromString("-31.00"),
		StatementName: "RIVERSIDE PARKING",
		Payee:         "Riverside Parking",
	})
	frozenClient(l, "vera").
		get("/series/suggested/for/" + id.String()).
		requireStatus(http.StatusOK)
}

// The transaction template.
//
// A series is the parts of a transaction a person would otherwise retype every
// month. The importer fills `template_tag_ids` and `template_splits`, and the
// app reads and writes them too, so a series created in the app produces a
// tagged, split and categorized charge.

func TestASeriesCarriesItsTagsOntoTheChargeThatPaysIt(t *testing.T) {
	l := buildLedger(t)
	c := frozenClient(l, "alex")
	series := newSeries(c, l, map[string]any{
		"description": "ORCHARD LANE HOA",
		"category_id": l.str("groceries"),
		"tag_ids":     []string{l.str("tag")},
	})
	require.Equal(t, []any{l.str("tag")}, series["tag_ids"])

	c.post("/occurrences/accept", map[string]any{
		"series_id": series["id"], "due_on": "2026-09-01",
	}).requireStatus(http.StatusCreated)

	charge := chargeForSeries(c, series["id"].(string))
	require.Equal(t, l.str("groceries"), charge["category_id"])
	require.Equal(t, []any{l.str("tag")}, charge["tag_ids"])
}

func TestASeriesSplitIsCarvedOntoTheChargeVerbatimWhenTheAmountMatches(t *testing.T) {
	l := buildLedger(t)
	c := frozenClient(l, "alex")
	series := newSeries(c, l, map[string]any{
		"description": "UTILITIES AUTOPAY",
		"amount":      "-100.00",
		"splits": []any{
			map[string]any{"amount": "-70.00", "category_id": l.str("groceries")},
			map[string]any{"amount": "-30.00", "category_id": l.str("food")},
		},
	})
	require.Len(t, series["splits"].([]any), 2)

	c.post("/occurrences/accept", map[string]any{
		"series_id": series["id"], "due_on": "2026-09-01",
	}).requireStatus(http.StatusCreated)

	splits := chargeForSeries(c, series["id"].(string))["splits"].([]any)
	require.Len(t, splits, 2)
	require.Equal(t, "-70.00", splits[0].(map[string]any)["amount"])
	require.Equal(t, "-30.00", splits[1].(map[string]any)["amount"])
}

func TestASplitFollowsAnAmountTheBillActuallyArrivedFor(t *testing.T) {
	// The template is written against the series' amount. A transaction whose
	// splits do not sum to it is the one thing the ledger refuses, so an
	// occurrence accepted for something else has to be carved in proportion.
	l := buildLedger(t)
	c := frozenClient(l, "alex")
	series := newSeries(c, l, map[string]any{
		"description": "POWER AUTOPAY",
		"amount":      "-100.00",
		"splits": []any{
			map[string]any{"amount": "-70.00", "category_id": l.str("groceries")},
			map[string]any{"amount": "-30.00", "category_id": l.str("food")},
		},
	})

	c.post("/occurrences/accept", map[string]any{
		"series_id": series["id"], "due_on": "2026-09-01", "amount": "-150.00",
	}).requireStatus(http.StatusCreated)

	charge := chargeForSeries(c, series["id"].(string))
	require.Equal(t, "-150.00", charge["amount"])
	splits := charge["splits"].([]any)
	require.Equal(t, "-105.00", splits[0].(map[string]any)["amount"])
	require.Equal(t, "-45.00", splits[1].(map[string]any)["amount"])
}

func TestARoundingRemainderLandsOnTheLargestSplitRatherThanBeingLost(t *testing.T) {
	l := buildLedger(t)
	c := frozenClient(l, "alex")
	series := newSeries(c, l, map[string]any{
		"description": "THIRDS AUTOPAY",
		"amount":      "-30.00",
		"splits": []any{
			map[string]any{"amount": "-10.00", "category_id": l.str("groceries")},
			map[string]any{"amount": "-10.00", "category_id": l.str("food")},
			map[string]any{"amount": "-10.00", "category_id": l.str("groceries")},
		},
	})

	c.post("/occurrences/accept", map[string]any{
		"series_id": series["id"], "due_on": "2026-09-01", "amount": "-100.00",
	}).requireStatus(http.StatusCreated)

	splits := chargeForSeries(c, series["id"].(string))["splits"].([]any)
	total := domain.Zero
	for _, raw := range splits {
		total = total.Add(domain.MustFromString(raw.(map[string]any)["amount"].(string)))
	}
	require.Equal(t, "-100.00", total.String(), "the split stopped summing to its transaction")
}

func TestASeriesSplitMustSumToTheSeriesAndNeedsTwoLines(t *testing.T) {
	l := buildLedger(t)
	c := frozenClient(l, "alex")

	c.post("/series", map[string]any{
		"account_id": l.str("checking"), "kind": "bill", "amount": "-100.00",
		"start_on": "2026-01-01", "description": "LOPSIDED",
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
		"splits": []any{
			map[string]any{"amount": "-70.00", "category_id": l.str("groceries")},
			map[string]any{"amount": "-40.00", "category_id": l.str("food")},
		},
	}).requireStatus(http.StatusConflict)

	// One line is not a split; it is the category the series already has.
	c.post("/series", map[string]any{
		"account_id": l.str("checking"), "kind": "bill", "amount": "-100.00",
		"start_on": "2026-01-01", "description": "ONE LINE",
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
		"splits": []any{
			map[string]any{"amount": "-100.00", "category_id": l.str("groceries")},
		},
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestASeriesTemplateCannotBorrowAnotherSpacesTagOrCategory(t *testing.T) {
	l := buildLedger(t)
	c := frozenClient(l, "alex")

	c.post("/series", map[string]any{
		"account_id": l.str("checking"), "kind": "bill", "amount": "-100.00",
		"start_on": "2026-01-01", "description": "BORROWED TAG",
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
		"tag_ids":    []string{l.str("stranger_tag")},
	}).requireStatus(http.StatusConflict)

	c.post("/series", map[string]any{
		"account_id": l.str("checking"), "kind": "bill", "amount": "-100.00",
		"start_on": "2026-01-01", "description": "BORROWED CATEGORY",
		"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{1}},
		"splits": []any{
			map[string]any{"amount": "-50.00", "category_id": l.str("stranger_category")},
			map[string]any{"amount": "-50.00", "category_id": l.str("groceries")},
		},
	}).requireStatus(http.StatusConflict)
}

func TestATemplateIsEditedAndClearedThroughTheSamePatch(t *testing.T) {
	l := buildLedger(t)
	c := frozenClient(l, "alex")
	series := newSeries(c, l, map[string]any{
		"description": "EDITABLE",
		"tag_ids":     []string{l.str("tag")},
		"splits": []any{
			map[string]any{"amount": "-60.00", "category_id": l.str("groceries")},
			map[string]any{"amount": "-40.00", "category_id": l.str("food")},
		},
	})
	id := series["id"].(string)

	// Absent leaves it alone.
	unchanged := c.patch("/series/"+id, map[string]any{"display_name": "Renamed"}).
		requireStatus(http.StatusOK).json()
	require.Len(t, unchanged["splits"].([]any), 2)
	require.Len(t, unchanged["tag_ids"].([]any), 1)

	// Empty clears it.
	cleared := c.patch("/series/"+id, map[string]any{"tag_ids": []any{}, "splits": []any{}}).
		requireStatus(http.StatusOK).json()
	require.Empty(t, cleared["splits"].([]any))
	require.Empty(t, cleared["tag_ids"].([]any))
}

func TestMovingTheAmountAndTheSplitTogetherIsCheckedAgainstTheNewAmount(t *testing.T) {
	// The old amount would refuse a split that is correct for the new one, and
	// there is no order of two requests that avoids it.
	l := buildLedger(t)
	c := frozenClient(l, "alex")
	series := newSeries(c, l, map[string]any{"description": "TOGETHER", "amount": "-100.00"})

	updated := c.patch("/series/"+series["id"].(string), map[string]any{
		"amount": "-200.00",
		"splits": []any{
			map[string]any{"amount": "-150.00", "category_id": l.str("groceries")},
			map[string]any{"amount": "-50.00", "category_id": l.str("food")},
		},
	}).requireStatus(http.StatusOK).json()
	require.Len(t, updated["splits"].([]any), 2)
}

// chargeForSeries is the one ledger row an accepted occurrence produced.
func chargeForSeries(c *client, seriesID string) map[string]any {
	c.t.Helper()
	body := c.get("/transactions?limit=200").requireStatus(http.StatusOK).json()
	for _, raw := range body["items"].([]any) {
		row := raw.(map[string]any)
		if row["series_id"] == seriesID {
			return row
		}
	}
	c.t.Fatalf("no charge linked to series %s", seriesID)
	return nil
}

// --- The status vocabulary ----------------------------------------------------

// One word per state, wherever the occurrence is drawn.
//
// The history endpoint does not spell the states itself: a fulfilled income
// slot is "Received" in its own history as on the spending plan, not "Paid",
// or one thing has two names on two screens a person moves between.
func TestAnIncomeOccurrenceIsReceivedInItsHistoryToo(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"kind": string(domain.SeriesIncome), "amount": "2500.00",
		"description": "NORTHWIND PAYROLL",
	})
	id := created["id"].(string)

	alex.post("/occurrences/accept", map[string]any{"series_id": id, "due_on": "2026-01-01"}).
		requireStatus(http.StatusCreated)

	history := alex.get("/series/" + id + "/history?to=2026-02&months=2").
		requireStatus(http.StatusOK).json()
	rows := history["rows"].([]any)
	january := rows[len(rows)-1].(map[string]any)
	require.Equal(t, "2026-01-01", january["due_on"])
	require.Equal(t, "received", january["status"], "not paid — the money came in")
	require.Equal(t, false, january["off_schedule"])
	require.Equal(t, float64(1), history["paid_count"],
		"a received slot is a settled slot, and the average is over those")
}

// Income has no overdue state: a paycheck that has not arrived is upcoming,
// not a missed bill, in the occurrence list as on the spending plan.
func TestAnUnreceivedPaycheckIsUpcomingRatherThanPastDue(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	newSeries(alex, l, map[string]any{
		"kind": string(domain.SeriesIncome), "amount": "2500.00",
		"description": "NORTHWIND PAYROLL",
	})

	list := alex.get("/occurrences?from=2026-01-01&to=2026-01-31").
		requireStatus(http.StatusOK).json()
	items := list["items"].([]any)
	require.NotEmpty(t, items)
	require.Equal(t, "upcoming", items[0].(map[string]any)["status"])
	require.Equal(t, float64(0), list["summary"].(map[string]any)["past_due"])
}

// A charge filed under a series away from its schedule is still paid, not a
// status of its own — "posted" — which would make "the money moved" look
// like a different state from "the money moved on time".
func TestAnOffScheduleChargeIsPaidWithANoteRatherThanAStatusOfItsOwn(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "GYM MEMBERSHIP"})
	id := created["id"].(string)

	charge := alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-01-17",
		"amount": "-100.00", "payee": "Gym",
	}).requireStatus(http.StatusCreated).json()
	alex.post("/transactions/"+charge["id"].(string)+"/link-series",
		map[string]any{"series_id": id, "due_on": "2026-01-01"}).requireStatus(http.StatusOK)

	history := alex.get("/series/" + id + "/history?to=2026-01&months=1").
		requireStatus(http.StatusOK).json()
	for _, row := range history["rows"].([]any) {
		one := row.(map[string]any)
		if one["transaction"] == nil {
			continue
		}
		require.Equal(t, "paid", one["status"])
	}
}

// --- Linking an existing charge ----------------------------------------------

func TestLinkingAChargeClaimsTheNearestOccurrenceAndAdvancesThePointer(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "GYM MEMBERSHIP"})
	id := created["id"].(string)
	require.Equal(t, "2026-01-01", created["due_on"])

	charge := alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"),
		"date":       "2026-01-03",
		"amount":     "-100.00",
		"payee":      "Gym",
	}).requireStatus(http.StatusCreated).json()

	linked := alex.post("/transactions/"+charge["id"].(string)+"/link-series", map[string]any{
		"series_id": id,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, id, linked["series_id"], "the row now belongs to the series")
	require.Equal(t, "2026-01-01", linked["series_due_on"],
		"a charge posted two days late files under the occurrence it pays")
	require.Equal(t, "Gym", linked["payee"],
		"filing a row under a series keeps its payee, as the automatic matcher does")

	after := alex.get("/series/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-02-01", after["next_due_on"], "the fulfilled slot moved the pointer")
}

func TestLinkingIntoAClaimedSlotIsRefused(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "RENT DIRECT DEBIT"})
	id := created["id"].(string)

	alex.post("/occurrences/accept", map[string]any{
		"series_id": id, "due_on": "2026-01-01",
	}).requireStatus(http.StatusCreated)

	charge := alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"),
		"date":       "2026-01-02",
		"amount":     "-100.00",
	}).requireStatus(http.StatusCreated).json()

	// The occurrence is paid; a second charge cannot also pay it.
	alex.post("/transactions/"+charge["id"].(string)+"/link-series", map[string]any{
		"series_id": id, "due_on": "2026-01-01",
	}).requireStatus(http.StatusConflict)

	// A day the rule never lands on is not a slot at all.
	alex.post("/transactions/"+charge["id"].(string)+"/link-series", map[string]any{
		"series_id": id, "due_on": "2026-01-15",
	}).requireStatus(http.StatusConflict)
}

// A payment must be fileable under its own series even when Simplifi's
// forecast for that occurrence already sits in the slot.
//
// The import brings those forecasts across — as much as a year ahead —
// dated on the due date and holding the slot they were written for. They are
// estimates: the Upcoming screen still offers such an occurrence as due, so
// the link must not refuse the slot the screen offers. For a household that
// imported from Simplifi that is every bill the export forecast.
func TestAForecastInTheSlotDoesNotRefuseTheChargeThatPaysIt(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "MORTGAGE PAYMENT"})
	id := created["id"].(string)

	forecast := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.January, 1),
		Amount: domain.MustFromString("-2400.00"), Currency: "USD",
		StatementName: "MORTGAGE PAYMENT", Payee: "Mortgage",
		Source: domain.SourceSimplifiImport, EstimateStatus: store.ProjectedEstimate,
		SeriesID: uuid.MustParse(id), SeriesDueOn: domain.NewDate(2026, time.January, 1),
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), forecast))

	charge := alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-01-01",
		"amount": "-2400.00", "payee": "Mortgage",
	}).requireStatus(http.StatusCreated).json()

	linked := alex.post("/transactions/"+charge["id"].(string)+"/link-series", map[string]any{
		"series_id": id,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-01-01", linked["series_due_on"])
	require.Equal(t, forecast.ID.String(), linked["id"],
		"the charge upgrades the forecast in place, as the automatic matcher does")
	require.Nil(t, linked["estimate_status"])

	// And the slot now reads as paid, not as still forecast: one row holds it.
	history := alex.get("/series/" + id + "/history?to=2026-01&months=1").
		requireStatus(http.StatusOK).json()
	rows := history["rows"].([]any)
	require.NotEmpty(t, rows)
	first := rows[0].(map[string]any)
	require.Equal(t, "paid", first["status"])
	require.Equal(t, forecast.ID.String(), first["transaction"].(map[string]any)["id"])
	alex.get("/transactions/" + charge["id"].(string)).requireStatus(http.StatusNotFound)
}

// A refusal that names nothing leaves the user with nowhere to go. The one
// case that is a genuine double — a slot a real charge already pays — says
// which charge pays it.
func TestARefusedSlotNamesTheChargeThatHoldsIt(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "RENT DIRECT DEBIT"})
	id := created["id"].(string)

	alex.post("/occurrences/accept", map[string]any{
		"series_id": id, "due_on": "2026-01-01", "date": "2026-01-01",
	}).requireStatus(http.StatusCreated)

	second := alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-01-02", "amount": "-100.00",
	}).requireStatus(http.StatusCreated).json()

	refused := alex.post("/transactions/"+second["id"].(string)+"/link-series", map[string]any{
		"series_id": id, "due_on": "2026-01-01",
	}).requireStatus(http.StatusConflict).json()
	require.Contains(t, refused["detail"], "2026-01-01")
	require.Contains(t, refused["detail"], "already records this occurrence")
}

func TestUnlinkingReleasesTheSlotForTheRightCharge(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "STREAMING SVC"})
	id := created["id"].(string)

	charge := alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"),
		"date":       "2026-01-01",
		"amount":     "-100.00",
	}).requireStatus(http.StatusCreated).json()
	chargeID := charge["id"].(string)

	alex.post("/transactions/"+chargeID+"/link-series", map[string]any{
		"series_id": id,
	}).requireStatus(http.StatusOK)

	released := alex.post("/transactions/"+chargeID+"/unlink-series", nil).
		requireStatus(http.StatusOK).json()
	require.Nil(t, released["series_id"], "the row no longer names a series")

	// The slot is open again: another charge can claim it.
	second := alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"),
		"date":       "2025-12-31",
		"amount":     "-100.00",
	}).requireStatus(http.StatusCreated).json()
	alex.post("/transactions/"+second["id"].(string)+"/link-series", map[string]any{
		"series_id": id, "due_on": "2026-01-01",
	}).requireStatus(http.StatusOK)

	// Unlinking a row that is not linked is a named refusal, not a shrug.
	alex.post("/transactions/"+chargeID+"/unlink-series", nil).
		requireStatus(http.StatusConflict)
}

func TestASeriesHistoryReadsEachMonthsSlotBesideWhatPaidIt(t *testing.T) {
	// Opening a bill from the spending plan asks what it cost each month, which
	// months were paid, and what paid them. The history is the rule's slots
	// over a run of months, newest first, each with its charge.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "GYM MEMBERSHIP"})
	id := created["id"].(string)

	charge := alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-01-03", "amount": "-104.50", "payee": "Gym",
	}).requireStatus(http.StatusCreated).json()
	alex.post("/transactions/"+charge["id"].(string)+"/link-series", map[string]any{"series_id": id}).
		requireStatus(http.StatusOK)
	alex.post("/occurrences/accept", map[string]any{"series_id": id, "due_on": "2026-02-01"}).
		requireStatus(http.StatusCreated)
	alex.post("/occurrences/skip", map[string]any{"series_id": id, "due_on": "2026-03-01"}).
		requireStatus(http.StatusNoContent)

	history := alex.get("/series/" + id + "/history?to=2026-04&months=4").
		requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-01", history["from"])
	require.Equal(t, "2026-04", history["to"])
	require.Equal(t, "GYM MEMBERSHIP", history["series"].(map[string]any)["label"])
	rows := history["rows"].([]any)
	require.Len(t, rows, 4)

	april := rows[0].(map[string]any)
	require.Equal(t, "2026-04-01", april["due_on"])
	require.Equal(t, "-100.00", april["expected"])
	require.Nil(t, april["transaction"])
	require.Contains(t, []any{"upcoming", "past_due"}, april["status"])

	require.Equal(t, "skipped", rows[1].(map[string]any)["status"])

	february := rows[2].(map[string]any)
	require.Equal(t, "paid", february["status"])
	require.Equal(t, "-100.00", february["transaction"].(map[string]any)["amount"])

	january := rows[3].(map[string]any)
	require.Equal(t, "paid", january["status"])
	txn := january["transaction"].(map[string]any)
	require.Equal(t, charge["id"], txn["id"])
	require.Equal(t, "-104.50", txn["amount"])
	require.Equal(t, "Gym", txn["payee"], "linking keeps the row's own payee")
	require.Equal(t, "Everyday Checking", txn["account_name"])

	require.Equal(t, float64(2), history["paid_count"])
	require.Equal(t, "-102.25", history["average_paid"])

	alex.get("/series/" + id + "/history?to=nonsense").requireStatus(http.StatusUnprocessableEntity)
	alex.get("/series/" + uuid.NewString() + "/history").requireStatus(http.StatusNotFound)
}
