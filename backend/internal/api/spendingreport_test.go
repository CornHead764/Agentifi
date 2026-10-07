package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The Spending report over the reports ledger, on September 12. What the
// ledger holds, by reporting date:
//
//	Jun  1  salary            +1000.00
//	Jun  4  groceries           −40.00
//	Jul 15  uncategorized      −100.00
//	Aug  1  salary            +3000.00
//	Aug  5  groceries           −50.00
//	Aug  8  utilities          −120.00
//	Aug 10  a paired transfer   (counts nowhere)
//	Aug 12  streaming           −15.00
//	Aug 18  donations          −200.00
//	Aug 20  uncategorized       −25.00
//	Sep 10  card charge, uncategorized, posted Aug 25   −75.00
//
// and the other space's −10.00 on August 12.

func spendingReportLedger(t *testing.T) *ledger {
	t.Helper()
	l := buildReportsLedger(t)
	today := domain.NewDate(2026, time.September, 12)
	l.env.Now = func() time.Time { return today.Time().Add(12 * time.Hour) }
	return l
}

func spendingReport(l *ledger, query string) map[string]any {
	l.t.Helper()
	return l.alex.get("/reports/spending?" + query).requireStatus(http.StatusOK).json()
}

func spendingRows(report map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, raw := range report["rows"].([]any) {
		row := raw.(map[string]any)
		out[row["label"].(string)] = row
	}
	return out
}

func rowLabels(report map[string]any) []string {
	out := []string{}
	for _, raw := range report["rows"].([]any) {
		out = append(out, raw.(map[string]any)["label"].(string))
	}
	return out
}

func difference(row map[string]any) map[string]any {
	return row["difference"].(map[string]any)
}

func TestSpendingReportSetsAWholeMonthBesideTheOneBefore(t *testing.T) {
	l := spendingReportLedger(t)
	report := spendingReport(l, "grain=month&period=2026-08-01&compare=prior")

	period := report["period"].(map[string]any)
	require.Equal(t, "2026-08", period["key"])
	require.Equal(t, "2026-08-31", period["through"])
	require.Equal(t, false, period["partial"])

	summary := report["summary"].(map[string]any)
	require.Equal(t, "3000.00", summary["income"])
	// The card charge posted August 25 is due September 10: September's (trap 4).
	require.Equal(t, "-410.00", summary["spent"])
	require.Equal(t, "2590.00", summary["remaining"])
	require.Equal(t, "great", summary["rating"])

	require.Equal(t,
		[]string{"Charitable Donations", "Utilities", "Food & Dining", "Uncategorized", "Streaming"},
		rowLabels(report))
	rows := spendingRows(report)
	require.Equal(t, "-25.00", rows["Uncategorized"]["amount"])
	require.Equal(t, "-100.00", rows["Uncategorized"]["comparison"])
	require.Equal(t, "-75.00", difference(rows["Uncategorized"])["amount"])
	require.Equal(t, "-75", difference(rows["Uncategorized"])["pct"])
	require.Equal(t, "new_spend", difference(rows["Utilities"])["state"])
	require.Nil(t, difference(rows["Utilities"])["pct"])

	comparison := report["comparison"].(map[string]any)
	require.Equal(t, "-100.00", comparison["spent"])
	require.Equal(t, "2026-07-31", comparison["periods"].([]any)[0].(map[string]any)["through"])

	require.EqualValues(t, 1, report["uncategorized_count"])
}

func TestSpendingReportCutsTheComparisonToTheSameDays(t *testing.T) {
	l := spendingReportLedger(t)
	report := spendingReport(l, "grain=month&compare=prior")

	period := report["period"].(map[string]any)
	require.Equal(t, "2026-09", period["key"])
	require.Equal(t, "2026-09-12", period["through"])
	require.Equal(t, true, period["partial"])
	require.Equal(t, "2026-09-12", report["window"].(map[string]any)["to"])

	// August 1–12 only: the donation on the 18th and the store on the 20th
	// are past the point September has reached.
	comparison := report["comparison"].(map[string]any)
	cut := comparison["periods"].([]any)[0].(map[string]any)
	require.Equal(t, "2026-08-01", cut["from"])
	require.Equal(t, "2026-08-12", cut["through"])
	require.Equal(t, "-185.00", comparison["spent"])
	// $75 against $185: $110 less, −59.46%.
	require.Equal(t, "-110.00", comparison["difference"].(map[string]any)["amount"])
	require.Equal(t, "-59.46", comparison["difference"].(map[string]any)["pct"])

	rows := spendingRows(report)
	require.Equal(t, "new_spend", difference(rows["Uncategorized"])["state"])
	require.Equal(t, "no_spend", difference(rows["Utilities"])["state"])
	require.Equal(t, "no_spend", difference(rows["Food & Dining"])["state"])
	require.NotContains(t, rows, "Charitable Donations")
	require.EqualValues(t, 1, report["uncategorized_count"])
}

func TestSpendingReportAveragesCountEmptyMonths(t *testing.T) {
	l := spendingReportLedger(t)

	// June 1–12, July 1–12 and August 1–12: $40, nothing and $185 of
	// spending, $75 a month.
	report := spendingReport(l, "grain=month&compare=average_3")
	comparison := report["comparison"].(map[string]any)
	require.Equal(t, true, comparison["average"])
	require.Len(t, comparison["periods"], 3)
	require.Equal(t, "-75.00", comparison["spent"])
	require.Equal(t, "change", comparison["difference"].(map[string]any)["state"])
	require.Equal(t, "0", comparison["difference"].(map[string]any)["pct"])

	rows := spendingRows(report)
	require.Equal(t, "-30.00", rows["Food & Dining"]["comparison"])
	require.Equal(t, "-40.00", rows["Utilities"]["comparison"])
	require.Equal(t, "-5.00", rows["Streaming"]["comparison"])

	// January to August, each cut at the 12th: $225 over eight months.
	ytd := spendingReport(l, "grain=month&compare=ytd_average")
	require.Len(t, ytd["comparison"].(map[string]any)["periods"], 8)
	require.Equal(t, "-28.13", ytd["comparison"].(map[string]any)["spent"])

	none := spendingReport(l, "grain=month&compare=none")
	require.Nil(t, none["comparison"])
	require.Equal(t, "none", difference(spendingRows(none)["Uncategorized"])["state"])
}

func TestSpendingReportSummarizesJune(t *testing.T) {
	l := spendingReportLedger(t)
	summary := spendingReport(l, "grain=month&period=2026-06-15")["summary"].(map[string]any)
	require.Equal(t, "1000.00", summary["income"])
	require.Equal(t, "-40.00", summary["spent"])
	require.Equal(t, "0.96", summary["savings_rate"])
	require.Equal(t, "0.04", summary["spending_rate"])
	require.Equal(t, "great", summary["rating"])

	// May took nothing in.
	may := spendingReport(l, "grain=month&period=2026-05-01")["summary"].(map[string]any)
	require.Nil(t, may["savings_rate"])
	require.Equal(t, "none", may["rating"])
}

func TestSpendingReportChartsTwelveMonths(t *testing.T) {
	l := spendingReportLedger(t)
	periods := spendingReport(l, "grain=month")["periods"].([]any)
	require.Len(t, periods, 12)
	require.Equal(t, "2025-10", periods[0].(map[string]any)["key"])

	august := periods[10].(map[string]any)
	require.Equal(t, "2026-08", august["key"])
	require.Equal(t, "3000.00", august["income"])
	require.Equal(t, "-410.00", august["spent"])
	require.Equal(t, "2590.00", august["remaining"])

	september := periods[11].(map[string]any)
	require.Equal(t, true, september["partial"])
	require.Equal(t, "-75.00", september["spent"])
	require.Equal(t, "0.00", september["income"])
}

func TestSpendingReportQuarterComparesTheSamePointIntoTheQuarter(t *testing.T) {
	l := spendingReportLedger(t)
	report := spendingReport(l, "grain=quarter&compare=prior")
	require.Equal(t, "2026-Q3", report["period"].(map[string]any)["key"])
	require.Equal(t, "-585.00", report["summary"].(map[string]any)["spent"])

	// Two months and twelve days in: April 1 – June 12.
	comparison := report["comparison"].(map[string]any)
	cut := comparison["periods"].([]any)[0].(map[string]any)
	require.Equal(t, "2026-04-01", cut["from"])
	require.Equal(t, "2026-06-12", cut["through"])
	require.Equal(t, "-40.00", comparison["spent"])
	require.Equal(t, "1362.5", comparison["difference"].(map[string]any)["pct"])
	require.Len(t, report["periods"], 5)
}

func TestSpendingReportTableRunsAcrossThePeriods(t *testing.T) {
	l := spendingReportLedger(t)
	table := spendingReport(l, "grain=month")["table"].(map[string]any)
	require.Len(t, table["periods"], 12)
	require.Equal(t, "2026-08-12", table["prior"].(map[string]any)["through"])

	var uncategorized map[string]any
	for _, raw := range table["rows"].([]any) {
		if row := raw.(map[string]any); row["label"] == "Uncategorized" {
			uncategorized = row
		}
	}
	require.NotNil(t, uncategorized)
	cells := uncategorized["cells"].([]any)
	require.Equal(t, []any{"-100.00", "-25.00", "-75.00"}, cells[9:])
	require.Equal(t, "-200.00", uncategorized["total"])
	require.Equal(t, "new_spend", difference(uncategorized)["state"])

	// Years start no earlier than the chart's five when nothing is older.
	require.Len(t, spendingReport(l, "grain=year")["table"].(map[string]any)["periods"], 5)
}

func TestSpendingReportFlowsIncomeIntoSpending(t *testing.T) {
	l := spendingReportLedger(t)
	flow := spendingReport(l, "grain=month&period=2026-08-01")["flow"].(map[string]any)
	require.Equal(t, "3000.00", flow["income_total"])
	require.Equal(t, "410.00", flow["spent"])
	income := flow["income"].([]any)
	require.Len(t, income, 1)
	require.Equal(t, "Salary", income[0].(map[string]any)["label"])
	require.Equal(t, "1", income[0].(map[string]any)["share"])
	require.Empty(t, flow["credits"])
	require.Len(t, flow["spending"], 5)
}

func TestSpendingReportIsTheRegisterSpendingTabsFigure(t *testing.T) {
	l := spendingReportLedger(t)
	report := spendingReport(l, "grain=month&period=2026-08-01")
	window := report["window"].(map[string]any)

	tab := l.alex.get("/transactions/aggregate?direction=spending&group_by=category&date_field=effective" +
		"&from=" + window["from"].(string) + "&to=" + window["to"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, tab["total"], report["summary"].(map[string]any)["spent"])
	buckets := map[string]any{}
	for _, raw := range tab["buckets"].([]any) {
		bucket := raw.(map[string]any)
		buckets[bucket["label"].(string)] = bucket["total"]
	}
	for label, row := range spendingRows(report) {
		require.Equal(t, buckets[label], row["amount"], label)
	}

	engine := runReportAt(l, "from=2026-08-01&to=2026-08-31&mode=summary&rows=category&sign=both")
	require.Equal(t, engine["totals"].(map[string]any)["expenses"], report["summary"].(map[string]any)["spent"])
	require.Equal(t, engine["totals"].(map[string]any)["income"], report["summary"].(map[string]any)["income"])
}

func TestSpendingReportTakesTheRegistersScope(t *testing.T) {
	l := spendingReportLedger(t)

	// The card holds the charge and a transfer leg, which counts nowhere.
	card := spendingReport(l, "grain=month&period=2026-08-01&account_id="+l.str("card"))
	require.Equal(t, "0.00", card["summary"].(map[string]any)["spent"])
	require.Equal(t, "-75.00",
		spendingReport(l, "grain=month&account_id="+l.str("card"))["summary"].(map[string]any)["spent"])

	// The seeded groceries filter keeps August's $50 alone.
	filtered := spendingReport(l, "grain=month&period=2026-08-01&filter_id="+l.str("filter"))
	require.Equal(t, "-50.00", filtered["summary"].(map[string]any)["spent"])
	require.Equal(t, []string{"Food & Dining"}, rowLabels(filtered))

	// Drilled into Food & Dining, by subcategory.
	drilled := spendingReport(l, "grain=month&period=2026-08-01&filter_id="+l.str("filter")+"&under="+l.str("food"))
	require.Equal(t, []string{"Groceries"}, rowLabels(drilled))

	byPayee := spendingReport(l, "grain=month&period=2026-08-01&group_by=payee")
	require.Contains(t, spendingRows(byPayee), "Habitat for Humanity")

	// Another space's row is not here, and theirs is all they see.
	require.NotContains(t, spendingRows(spendingReport(l, "grain=month&period=2026-08-01&group_by=payee")), "Not Yours")
	theirs := l.as("bob").inSpace(store.SpaceIDOf(l.id("other_space"))).
		get("/reports/spending?grain=month&period=2026-08-01").
		requireStatus(http.StatusOK).json()
	require.Equal(t, "-10.00", theirs["summary"].(map[string]any)["spent"])

	// A viewer reads it.
	l.as("vera").get("/reports/spending?grain=year").requireStatus(http.StatusOK)
}

func TestSpendingReportRefusesWhatItCannotAnswer(t *testing.T) {
	l := spendingReportLedger(t)
	for _, query := range []string{
		"grain=week",
		"grain=year&compare=average_12",
		"grain=quarter&compare=average_3",
		"grain=month&period=2026-10-01",
	} {
		l.alex.get("/reports/spending?" + query).requireStatus(http.StatusUnprocessableEntity)
	}
	options := spendingReport(l, "grain=year")["compare_options"]
	require.Equal(t, []any{"prior", "average_3", "none"}, options)
}

// September 12, with September so far holding the −75.00 card charge. A
// paycheck of 3,000.00 is due on the 28th and a 15.00 subscription on the
// 25th; the 120.00 power bill due on the 14th was paid on the 11th, so it is
// counted once, by its charge; a card payment counts nowhere.
func TestSpendingReportProjectsTheMonthInProgress(t *testing.T) {
	l := spendingReportLedger(t)
	series := func(day int, body map[string]any) string {
		body["account_id"] = l.str("checking")
		body["recurrence"] = map[string]any{"frequency": "MONTHLY", "by_month_day": []int{day}}
		return l.alex.post("/series", body).requireStatus(http.StatusCreated).json()["id"].(string)
	}
	series(28, map[string]any{
		"kind": "income", "description": "ACME CORP DES:PAYROLL", "amount": "3000.00",
		"start_on": "2026-09-28", "category_id": l.str("salary_category"),
	})
	series(25, map[string]any{
		"kind": "subscription", "description": "STREAMFLIX MONTHLY", "amount": "-15.00",
		"start_on": "2026-09-25", "category_id": l.str("streaming"),
	})
	power := series(14, map[string]any{
		"kind": "bill", "description": "CITY POWER AUTOPAY", "amount": "-120.00",
		"start_on": "2026-09-14", "category_id": l.str("utilities"),
	})
	series(20, map[string]any{
		"kind": "credit_card_payment", "description": "CARD PAYMENT", "amount": "-500.00",
		"start_on": "2026-09-20",
	})
	l.alex.post("/occurrences/accept", map[string]any{
		"series_id": power, "due_on": "2026-09-14", "date": "2026-09-11",
	}).requireStatus(http.StatusCreated)

	summary := spendingReport(l, "grain=month")["summary"].(map[string]any)
	require.Equal(t, "0.00", summary["income"])
	// −75.00 − 120.00
	require.Equal(t, "-195.00", summary["spent"])
	require.Nil(t, summary["savings_rate"])

	projection := summary["projection"].(map[string]any)
	require.Equal(t, "2026-09-30", projection["end"])
	require.Equal(t, float64(2), projection["count"])
	require.Equal(t, "3000.00", projection["expected_income"])
	require.Equal(t, "-15.00", projection["expected_spent"])
	require.Equal(t, "3000.00", projection["income"])
	require.Equal(t, "-210.00", projection["spent"])
	require.Equal(t, "2790.00", projection["remaining"])
	// 2,790 / 3,000 and 210 / 3,000
	require.Equal(t, "0.93", projection["savings_rate"])
	require.Equal(t, "0.07", projection["spending_rate"])
	// The rating is the projection's.
	require.Equal(t, "great", summary["rating"])

	// A whole month is what happened, with nothing to anticipate.
	august := spendingReport(l, "grain=month&period=2026-08-01")["summary"].(map[string]any)
	require.Nil(t, august["projection"])
}
