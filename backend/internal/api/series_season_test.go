package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// lawnCare is monthly on the 12th, active April to October.
var lawnCare = map[string]any{
	"frequency": "MONTHLY", "by_month_day": []int{12}, "by_month": []int{4, 5, 6, 7, 8, 9, 10},
}

func TestASeasonalSeriesRoundTripsItsMonthsAndCountsOnlyThem(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "GREEN LAWN CO",
		"amount":      "-55.00",
		"start_on":    "2026-04-12",
		"recurrence":  lawnCare,
	})
	recurrence := created["recurrence"].(map[string]any)
	require.Equal(t, []any{4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0}, recurrence["by_month"])
	require.Equal(t, float64(7), created["occurrences_per_year"])
	require.Equal(t, "-385.00", created["annualized_amount"])

	list := occurrencesIn(alex, "from=2026-09-01&to=2027-05-31")
	require.Equal(t, []string{"2026-09-12", "2026-10-12", "2027-04-12", "2027-05-12"},
		dueDates(t, list, created["id"].(string)))
}

func TestAnEveryMonthSeriesSendsAnEmptyMonthList(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{"description": "RENT"})
	require.Equal(t, []any{}, created["recurrence"].(map[string]any)["by_month"])
}

func TestASeasonalSeriesSetUpOffSeasonStartsWithTheSeason(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "GREEN LAWN CO",
		"start_on":    "2026-11-12",
		"recurrence":  lawnCare,
	})
	require.Equal(t, "2027-04-12", created["start_on"])
	require.Equal(t, "2027-04-12", created["due_on"])
}

func TestMakingASeriesSeasonalMovesItsPointerPastTheOffSeason(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newSeries(alex, l, map[string]any{
		"description": "GREEN LAWN CO",
		"start_on":    "2026-01-12",
		"recurrence":  map[string]any{"frequency": "MONTHLY", "by_month_day": []int{12}},
	})
	updated := alex.patch("/series/"+created["id"].(string), map[string]any{
		"recurrence": map[string]any{
			"frequency": "MONTHLY", "by_month_day": []int{12}, "by_month": []int{4, 5, 6, 7, 8},
		},
	}).requireStatus(http.StatusOK).json()
	// Nothing has paid it, so the pointer restarts on the season's first slot.
	require.Equal(t, "2026-04-12", updated["next_due_on"])
	list := occurrencesIn(alex, "from=2026-09-01&to=2026-12-31")
	require.Empty(t, dueDates(t, list, created["id"].(string)))
}

func TestActiveMonthsAYearlyRuleCannotTakeOrNoSlotCanReachAreRefused(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	for _, recurrence := range []map[string]any{
		{"frequency": "YEARLY", "by_month": []int{5}},
		{"frequency": "MONTHLY", "by_month_day": []int{1}, "by_month": []int{13}},
		// Quarterly from January never reaches February.
		{"frequency": "MONTHLY", "interval": 3, "by_month_day": []int{1}, "by_month": []int{2}},
	} {
		alex.post("/series", map[string]any{
			"account_id":  l.str("checking"),
			"kind":        string(domain.SeriesBill),
			"description": "BAD SEASON",
			"amount":      "-10.00",
			"start_on":    "2026-01-01",
			"recurrence":  recurrence,
		}).requireStatus(http.StatusUnprocessableEntity)
	}
}

func TestTheCashFlowProjectionSkipsAnOffSeasonMonth(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	newSeries(alex, l, map[string]any{
		"description": "GREEN LAWN CO",
		"amount":      "-40.00",
		"start_on":    "2026-04-05",
		"recurrence": map[string]any{
			"frequency": "MONTHLY", "by_month_day": []int{5}, "by_month": []int{4, 5, 6, 7, 8, 9, 10},
		},
	})

	body := cashFlow(alex, "from=2026-10-01&to=2026-11-30")
	points := lineFor(t, body, l.str("checking"))["points"].([]any)
	require.Equal(t, "125.00", points[0].(map[string]any)["balance"])
	require.Equal(t, "85.00", points[4].(map[string]any)["balance"], "the 5 October visit")
	require.Equal(t, "85.00", points[len(points)-1].(map[string]any)["balance"],
		"no visit on 5 November")
}

func TestTheSpendingPlanPlansNoBillInAnOffSeasonMonth(t *testing.T) {
	l := planLedger(t)
	id := seedPowerBill(l)
	_, err := l.env.DB.Pool().Exec(l.t.Context(),
		`UPDATE series SET by_month = '[9,10]' WHERE id = $1`, id)
	require.NoError(t, err)

	require.Empty(t, billRows(planFor(l, augustMonth)))
	september := billRows(planFor(l, "2026-09"))
	require.Len(t, september, 1)
	require.Equal(t, "2026-09-12", september[0]["due_on"])
}
