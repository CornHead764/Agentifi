package api

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Reports, end to end. calculations.md §11: one engine, two renderings, and
// presets over two knobs and a sign.
//
// The seeded ledger carries only expenses in one account, so these tests add
// the rows §11 is about: income, a bill, a subscription, a category with a TXF
// code, and a month with nothing before it.

const reportsAugust = "from=2026-08-01&to=2026-08-31"

// buildReportsLedger is the household plus the rows the report engine needs.
//
// August: income 3,000.00, groceries −50.00, corner shop −25.00 (both
// discretionary), a −120.00 bill and a −15.00 subscription, and a −200.00
// donation under a category with a TXF code. The card charge seeded by
// buildLedger is posted in August and effective in September, which is what
// makes the two dates visibly different here.
func buildReportsLedger(t *testing.T) *ledger {
	t.Helper()
	l := buildLedger(t)
	ctx := t.Context()
	space := store.SpaceIDOf(l.id("space"))

	salary := &store.Category{
		Name: "Salary", Kind: domain.CategoryIncome, TxfID: "USA_460",
		IsUserAssignable: true, IsEditable: true,
	}
	utilities := &store.Category{
		Name: "Utilities", Kind: domain.CategoryExpense,
		IsUserAssignable: true, IsEditable: true,
	}
	streaming := &store.Category{
		Name: "Streaming", Kind: domain.CategoryExpense,
		IsUserAssignable: true, IsEditable: true,
	}
	donations := &store.Category{
		Name: "Charitable Donations", Kind: domain.CategoryExpense, TxfID: "USA_280",
		IsUserAssignable: true, IsEditable: true,
	}
	for _, category := range []*store.Category{salary, utilities, streaming, donations} {
		require.NoError(t, db(t).CreateCategory(ctx, space, category))
	}
	l.ids["salary_category"] = salary.ID
	l.ids["utilities"] = utilities.ID
	l.ids["streaming"] = streaming.ID
	l.ids["donations"] = donations.ID

	rows := []struct {
		key string
		txn *store.Transaction
	}{
		{"august_salary", &store.Transaction{
			AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 1),
			Amount: domain.MustFromString("3000.00"), Currency: "USD",
			StatementName: "ACME CORP DES:PAYROLL", Payee: "Acme Corp",
			CategoryID: salary.ID, Source: domain.SourceSync,
		}},
		{"august_power_bill", &store.Transaction{
			AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 8),
			Amount: domain.MustFromString("-120.00"), Currency: "USD",
			StatementName: "CITY POWER AUTOPAY", Payee: "City Power",
			CategoryID: utilities.ID, IsBill: true, Source: domain.SourceSync,
		}},
		{"august_streaming", &store.Transaction{
			AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 12),
			Amount: domain.MustFromString("-15.00"), Currency: "USD",
			StatementName: "STREAMFLIX MONTHLY", Payee: "Streamflix",
			CategoryID: streaming.ID, IsSubscription: true, Source: domain.SourceSync,
		}},
		{"august_donation", &store.Transaction{
			AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 18),
			Amount: domain.MustFromString("-200.00"), Currency: "USD",
			StatementName: "HABITAT FOR HUMANITY", Payee: "Habitat for Humanity",
			CategoryID: donations.ID, Source: domain.SourceSync,
		}},
		// June stands alone: May before it holds nothing, which is the zero
		// prior period every percentage delta has to survive.
		{"june_salary", &store.Transaction{
			AccountID: l.id("checking"), Date: domain.NewDate(2026, time.June, 1),
			Amount: domain.MustFromString("1000.00"), Currency: "USD",
			StatementName: "ACME CORP DES:PAYROLL", Payee: "Acme Corp",
			CategoryID: salary.ID, Source: domain.SourceSync,
		}},
		{"june_groceries", &store.Transaction{
			AccountID: l.id("checking"), Date: domain.NewDate(2026, time.June, 4),
			Amount: domain.MustFromString("-40.00"), Currency: "USD",
			StatementName: "SAFEWAY #1234 SPRINGFIELD ZZ", Payee: "Safeway",
			CategoryID: l.id("groceries"), Source: domain.SourceSync,
		}},
	}
	for _, row := range rows {
		require.NoError(t, db(t).CreateTransaction(ctx, space, row.txn))
		l.ids[row.key] = row.txn.ID
	}
	return l
}

func runReportAt(l *ledger, query string) map[string]any {
	l.t.Helper()
	return l.alex.get("/reports/run?" + query).requireStatus(http.StatusOK).json()
}

// reportNode finds a group by label at one level of the drill-down.
func reportNode(t *testing.T, nodes []any, label string) map[string]any {
	t.Helper()
	for _, raw := range nodes {
		node := raw.(map[string]any)
		if node["label"] == label {
			return node
		}
	}
	t.Fatalf("no report group labelled %q in %v", label, reportLabels(nodes))
	return nil
}

func reportLabels(nodes []any) []string {
	out := make([]string, 0, len(nodes))
	for _, raw := range nodes {
		out = append(out, raw.(map[string]any)["label"].(string))
	}
	return out
}

func childrenOf(node map[string]any) []any { return node["children"].([]any) }

func groupsOf(result map[string]any) []any {
	return result["transaction"].(map[string]any)["groups"].([]any)
}

// --- One engine, two renderings ----------------------------------------------

func TestBothRenderingsAreOneEngineOverTwoKnobs(t *testing.T) {
	// §11: the engine's report types are presets over two knobs plus a sign,
	// not one implementation each. The two renderings therefore have to agree on
	// every figure they both carry, over the same window and the same filter.
	l := buildReportsLedger(t)

	transaction := runReportAt(l, reportsAugust+"&mode=transaction&rows=category&sign=expenses")
	summary := runReportAt(l,
		reportsAugust+"&mode=summary&rows=category&columns=time&time_grain=month&sign=expenses")

	require.Equal(t, transaction["totals"], summary["totals"],
		"the two renderings disagree about what was selected")
	require.Equal(t,
		transaction["transaction"].(map[string]any)["total"],
		summary["summary"].(map[string]any)["total"],
		"the two renderings disagree about the grand total")
	require.Nil(t, transaction["summary"])
	require.Nil(t, summary["transaction"])
}

func TestBothRenderingsSelectTheSameRows(t *testing.T) {
	// The knobs choose a rendering; everything before them — the window, what
	// counts as income or expense, the saved Filter and the sign — is one
	// selection path. Two engines would drift here first.
	l := buildReportsLedger(t)

	for _, narrowing := range []string{
		"",
		"&filter_id=" + l.str("filter"),
		"&sign=income",
		"&from=2026-06-01&to=2026-06-30",
	} {
		transaction := runReportAt(l, reportsAugust+"&mode=transaction&rows=category"+narrowing)
		summary := runReportAt(l, reportsAugust+"&mode=summary&rows=category&columns=time"+narrowing)
		require.Equal(t, transaction["totals"], summary["totals"],
			"the renderings selected different rows for %q", narrowing)
		require.Equal(t,
			transaction["transaction"].(map[string]any)["total"],
			summary["summary"].(map[string]any)["total"],
			"the renderings totalled differently for %q", narrowing)
	}

	// §2: both legs of a matched transfer are out of profit and loss, in
	// either rendering. Moving money between two accounts is not spending.
	both := runReportAt(l, reportsAugust+"&mode=transaction&rows=payee")
	require.NotContains(t, reportLabels(groupsOf(both)), "Transfers")
	require.Equal(t, float64(6), both["totals"].(map[string]any)["count"],
		"a transfer leg reached the report")
}

func TestASplitReceiptIsCountedUnderEachOfItsParts(t *testing.T) {
	// The allocation is the unit, not the transaction: a receipt split
	// between two categories is two rows, and the parent contributes nothing
	// of its own or the row is counted twice.
	l := buildReportsLedger(t)
	l.alex.put("/transactions/"+l.str("august_groceries")+"/splits", map[string]any{
		"splits": []map[string]any{
			{"amount": "-30.00", "category_id": l.str("groceries")},
			{"amount": "-20.00", "category_id": l.str("utilities")},
		},
	}).requireStatus(http.StatusOK)

	result := runReportAt(l, reportsAugust+"&mode=transaction&rows=category&sign=expenses")
	expenses := reportNode(t, groupsOf(result), "Expenses")
	food := reportNode(t, childrenOf(expenses), "Food & Dining")
	require.Equal(t, "-30.00", reportNode(t, childrenOf(food), "Groceries")["total"])
	require.Equal(t, "-140.00", reportNode(t, childrenOf(expenses), "Utilities")["total"])
	// The receipt itself is still 50.00 of the grand total, not 100.00.
	require.Equal(t, "-410.00", result["transaction"].(map[string]any)["total"])
}

func TestEveryPresetIsTheSameEngineWithDifferentKnobs(t *testing.T) {
	l := buildReportsLedger(t)
	presets := l.alex.get("/reports/presets").requireStatus(http.StatusOK).list()
	require.NotEmpty(t, presets)

	served := 0
	for _, preset := range presets {
		config := preset["config"].(map[string]any)
		if preset["served_by"] != nil {
			// Net worth is balances and the Monthly Summary is a narrative
			// snapshot; both name their own endpoint rather than being a ninth
			// branch in the engine.
			l.alex.get("/reports/run?preset=" + preset["preset"].(string)).
				requireStatus(http.StatusUnprocessableEntity)
			continue
		}
		served++

		byName := runReportAt(l, reportsAugust+"&preset="+preset["preset"].(string))
		knobs := reportsAugust +
			"&mode=" + config["mode"].(string) +
			"&rows=" + config["rows"].(string) +
			"&sign=" + config["sign"].(string)
		if config["columns"].(string) != "" {
			knobs += "&columns=" + config["columns"].(string)
		}
		if config["time_grain"].(string) != "" {
			knobs += "&time_grain=" + config["time_grain"].(string)
		}
		byKnobs := runReportAt(l, knobs)

		delete(byName["config"].(map[string]any), "preset")
		delete(byKnobs["config"].(map[string]any), "preset")
		require.Equal(t, byName, byKnobs,
			"preset %s is not the same query as its own knobs", preset["preset"])
	}
	// Four engine-served presets; Spending, Net Worth, Savings and the
	// Monthly Summary name their own endpoints.
	require.Equal(t, 4, served, "the gallery lost a preset")
}

func TestTheSavingsReportIsBalancesInSavingsAccountsOnly(t *testing.T) {
	// Simplifi's Savings report is the balance held in savings
	// accounts over the window with a per-account month pivot — balances,
	// not transactions, which is why it is its own endpoint.
	l := buildReportsLedger(t)
	result := l.alex.get("/reports/savings?from=2026-08-01&to=2026-08-31").
		requireStatus(http.StatusOK).json()

	months := result["months"].([]any)
	require.Equal(t, []any{"2026-08"}, months)

	accounts := result["accounts"].([]any)
	totals := result["totals"].([]any)
	require.Len(t, totals, len(months))

	// Every row is a savings account; the checking and card accounts the
	// ledger also holds must not appear.
	for _, raw := range accounts {
		row := raw.(map[string]any)
		require.NotContains(t,
			[]string{"Everyday Checking", "Cashback Mastercard"}, row["name"])
	}

	// The Total row is its rows' cells added up.
	var sum float64
	for _, raw := range accounts {
		row := raw.(map[string]any)
		cell, err := strconv.ParseFloat(row["cells"].([]any)[0].(string), 64)
		require.NoError(t, err)
		sum += cell
	}
	total, err := strconv.ParseFloat(totals[0].(string), 64)
	require.NoError(t, err)
	require.InDelta(t, sum, total, 0.001)
}

func TestTransactionModeSubtotalsAtEveryLevelOfTheHierarchy(t *testing.T) {
	// Category → subcategory → transaction, a subtotal at each level and a
	// grand total. A level whose subtotal is not its children's sum is a
	// drill-down that contradicts the row above it.
	l := buildReportsLedger(t)
	result := runReportAt(l, reportsAugust+"&mode=transaction&rows=category&sign=expenses")

	expenses := reportNode(t, groupsOf(result), "Expenses")
	require.Equal(t, float64(0), expenses["depth"])

	food := reportNode(t, childrenOf(expenses), "Food & Dining")
	require.Equal(t, float64(1), food["depth"])
	groceries := reportNode(t, childrenOf(food), "Groceries")
	require.Equal(t, float64(2), groceries["depth"])
	require.Equal(t, "-50.00", groceries["total"])
	require.Len(t, groceries["transactions"], 1)

	// Every level's subtotal is its own rows plus its children's subtotals.
	var check func(node map[string]any) domain.Money
	check = func(node map[string]any) domain.Money {
		sum := domain.Zero
		for _, raw := range node["transactions"].([]any) {
			sum = sum.Add(domain.MustFromString(raw.(map[string]any)["amount"].(string)))
		}
		for _, child := range childrenOf(node) {
			sum = sum.Add(check(child.(map[string]any)))
		}
		require.Equal(t, node["total"], sum.String(),
			"the subtotal on %q is not its own rows plus its children", node["label"])
		return sum
	}
	grand := domain.Zero
	for _, group := range groupsOf(result) {
		grand = grand.Add(check(group.(map[string]any)))
	}
	require.Equal(t, result["transaction"].(map[string]any)["total"], grand.String())
	require.Equal(t, "-410.00", grand.String())
}

func TestSummaryModeIsAPivotWithATotalRowAndATotalColumn(t *testing.T) {
	l := buildReportsLedger(t)
	result := runReportAt(l,
		"from=2026-06-01&to=2026-08-31&mode=summary&rows=category&columns=time&time_grain=month&sign=expenses")
	pivot := result["summary"].(map[string]any)

	columns := pivot["columns"].([]any)
	require.Equal(t, []string{"2026-06", "2026-07", "2026-08"}, reportLabels(columns))

	columnTotals := pivot["column_totals"].([]any)
	require.Len(t, columnTotals, len(columns))

	running := make([]domain.Money, len(columns))
	grand := domain.Zero
	for _, raw := range pivot["rows"].([]any) {
		row := raw.(map[string]any)
		cells := row["cells"].([]any)
		require.Len(t, cells, len(columns))
		rowTotal := domain.Zero
		for index, cell := range cells {
			amount := domain.MustFromString(cell.(string))
			rowTotal = rowTotal.Add(amount)
			running[index] = running[index].Add(amount)
		}
		require.Equal(t, row["total"], rowTotal.String(),
			"the Total column on %q is not the sum of its cells", row["label"])
		grand = grand.Add(rowTotal)
	}
	for index := range columns {
		require.Equal(t, columnTotals[index], running[index].String(),
			"the Total row disagrees with column %v", columns[index])
	}
	require.Equal(t, pivot["total"], grand.String())
}

func TestTheNetLineIsIncomePlusExpensesPerPeriodNotASecondQuery(t *testing.T) {
	// §11: Income & Expense renders both sides and the net line is the column
	// total of the same pivot.
	l := buildReportsLedger(t)
	result := runReportAt(l, "from=2026-06-01&to=2026-08-31&preset=income_expense")
	pivot := result["summary"].(map[string]any)

	columns := reportLabels(pivot["columns"].([]any))
	august := -1
	for index, label := range columns {
		if label == "2026-08" {
			august = index
		}
	}
	require.NotEqual(t, -1, august)

	net := domain.Zero
	for _, raw := range pivot["rows"].([]any) {
		net = net.Add(domain.MustFromString(raw.(map[string]any)["cells"].([]any)[august].(string)))
	}
	require.Equal(t, pivot["column_totals"].([]any)[august], net.String())

	// Income positive, expenses negative, stored signs untouched, and the net
	// is the two added rather than a third figure from somewhere else.
	totals := result["totals"].(map[string]any)
	require.Equal(t, "4000.00", totals["income"])
	income := domain.MustFromString(totals["income"].(string))
	expenses := domain.MustFromString(totals["expenses"].(string))
	require.True(t, expenses.IsExpense())
	require.Equal(t, totals["net"], income.Add(expenses).String())
}

// --- The two dates -----------------------------------------------------------

func TestAReportFilesACardChargeUnderItsEffectiveDate(t *testing.T) {
	// §2: reports read effective_date, the register reads date. The seeded
	// card charge posts on 25 August and hits cash flow on 10 September;
	// reading the posted date here would move a month of card spending.
	l := buildReportsLedger(t)

	august := runReportAt(l, reportsAugust+"&mode=transaction&rows=payee&sign=expenses")
	require.NotContains(t, reportLabels(groupsOf(august)), "Harbor Coffee",
		"the report filed a September charge under August")

	september := runReportAt(l,
		"from=2026-09-01&to=2026-09-30&mode=transaction&rows=payee&sign=expenses")
	require.Contains(t, reportLabels(groupsOf(september)), "Harbor Coffee")
	require.Equal(t, "effective", september["window"].(map[string]any)["date_field"])
}

func TestTheRegisterAndTheReportDisagreeAboutTheCardChargeOnPurpose(t *testing.T) {
	// The same row, the same window, two endpoints that must answer
	// differently: the register shows what happened in August, the report
	// shows what August cost.
	l := buildReportsLedger(t)

	register := l.alex.get("/transactions?account_id=" + l.str("card") + "&" + reportsAugust).
		requireStatus(http.StatusOK).json()
	require.Contains(t, idsIn(register), l.str("card_charge"))
	require.Equal(t, "posted", register["window"].(map[string]any)["date_field"])

	report := runReportAt(l, reportsAugust+"&mode=transaction&rows=account&sign=expenses")
	require.NotContains(t, reportLabels(groupsOf(report)), "Rewards Card")
}

// --- Taxes -------------------------------------------------------------------

func TestAnIncomeAndExpensePivotSplitsIntoItsTwoSections(t *testing.T) {
	// The transaction rendering keeps Income above Expenses, and so does the
	// pivot rather than flattening both signs into one alphabetical list. Each
	// row names its family, the families arrive in order with their own
	// subtotal rows, and the section totals still add up to the grand total.
	l := buildReportsLedger(t)
	result := runReportAt(l,
		reportsAugust+"&mode=summary&rows=category&columns=time&time_grain=month&sign=both")
	pivot := result["summary"].(map[string]any)

	sections := pivot["sections"].([]any)
	require.Equal(t, []string{"Income", "Expenses"}, reportLabels(sections))

	rows := pivot["rows"].([]any)
	seenExpense := false
	sectionTotals := map[string]float64{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		section := row["section"].(string)
		if section == string(domain.CategoryExpense) {
			seenExpense = true
		} else {
			require.False(t, seenExpense, "an income row arrived after the expenses began")
		}
		total, err := strconv.ParseFloat(row["total"].(string), 64)
		require.NoError(t, err)
		sectionTotals[section] += total
	}

	var grand float64
	for _, raw := range sections {
		section := raw.(map[string]any)
		total, err := strconv.ParseFloat(section["total"].(string), 64)
		require.NoError(t, err)
		require.InDelta(t, sectionTotals[section["key"].(string)], total, 0.001,
			"a section subtotal disagrees with its own rows")
		grand += total
	}
	pivotTotal, err := strconv.ParseFloat(pivot["total"].(string), 64)
	require.NoError(t, err)
	require.InDelta(t, pivotTotal, grand, 0.001)
}

func TestASingleSignPivotStaysOneFlatList(t *testing.T) {
	// A Spending Summary has one family, and one family is no split at all.
	l := buildReportsLedger(t)
	result := runReportAt(l,
		reportsAugust+"&mode=summary&rows=category&columns=time&time_grain=month&sign=expenses")
	pivot := result["summary"].(map[string]any)
	require.Empty(t, pivot["sections"])
}

func TestTheTaxesReportGroupsByTheCategorysTxfCode(t *testing.T) {
	l := buildReportsLedger(t)
	result := runReportAt(l, reportsAugust+"&preset=taxes")

	// Kind, then form, then line item, then payee — the reference walk
	// `Expenses › Schedule A › Cash charity contributions › <payee>`.
	expenses := reportNode(t, groupsOf(result), "Expenses")
	schedA := reportNode(t, childrenOf(expenses), "Schedule A")
	donations := reportNode(t, childrenOf(schedA), "Cash charity contributions")
	require.Equal(t, "-200.00", donations["total"])
	require.Equal(t, []string{"Habitat for Humanity"}, reportLabels(childrenOf(donations)))

	income := reportNode(t, groupsOf(result), "Income")
	w2 := reportNode(t, childrenOf(income), "W-2")
	salary := reportNode(t, childrenOf(w2), "Salary or wages, self")
	require.Equal(t, "3000.00", salary["total"])

	// A category with no code is not silently filed under somebody else's.
	require.Contains(t, reportLabels(childrenOf(expenses)), "Unmapped")
}

func TestAnUnknownTxfCodeStaysVisibleUnderOtherForms(t *testing.T) {
	// The label table cannot know a code the spec added after it; the report
	// files the raw code under "Other forms" rather than dropping the money.
	require.Equal(t, txfLine{Form: "Other forms", Item: "USA_9999"}, txfLineFor("USA_9999"))
	require.Equal(t, txfLine{Form: "Schedule A", Item: "Home mortgage interest"}, txfLineFor("USA_283"))
}

// --- Monthly Summary ---------------------------------------------------------

func monthlySummary(l *ledger, month string) map[string]any {
	l.t.Helper()
	return l.alex.get("/reports/monthly-summary?month=" + month).
		requireStatus(http.StatusOK).json()
}

func entryLabels(entries any) []string {
	rows := entries.([]any)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.(map[string]any)["label"].(string))
	}
	return out
}

func TestTheMonthlySummaryLeavesBillsAndSubscriptionsOutOfItsTopLists(t *testing.T) {
	// §11: that exclusion is the point of the panel — it answers where the
	// discretionary money went. A Top Categories list that included the power
	// bill would just be the Spending report with fewer rows.
	l := buildReportsLedger(t)
	body := monthlySummary(l, "2026-08")

	require.Equal(t, "2026-08", body["month"])
	require.Equal(t, "2026-07", body["prior_month"])

	categories := entryLabels(body["top_categories"])
	require.Contains(t, categories, "Groceries")
	require.NotContains(t, categories, "Utilities", "the power bill is in Top Categories")
	require.NotContains(t, categories, "Streaming", "the subscription is in Top Categories")

	payees := entryLabels(body["top_payees"])
	require.Contains(t, payees, "Habitat for Humanity")
	require.NotContains(t, payees, "City Power")
	require.NotContains(t, payees, "Streamflix")

	// The bar beside the lists still counts them, and the two sides sum to
	// the month's expenses.
	require.Equal(t, "-135.00", body["bills"])
	require.Equal(t, "-275.00", body["discretionary"])
	require.Equal(t, "-410.00", body["expenses"])
}

func TestEachTopRowCarriesItsOccurrenceCount(t *testing.T) {
	l := buildReportsLedger(t)
	body := monthlySummary(l, "2026-08")
	for _, raw := range body["top_categories"].([]any) {
		row := raw.(map[string]any)
		require.GreaterOrEqual(t, row["count"], float64(1), "row %q has no occurrences", row["label"])
	}
}

func TestAPercentageDeltaAgainstAZeroPriorPeriodIsNullNotZero(t *testing.T) {
	// §11: undefined, rendered as an em dash. The client cannot render a dash
	// from 0, and "∞" is not a number it can render at all.
	l := buildReportsLedger(t)
	body := monthlySummary(l, "2026-06")

	require.Equal(t, "1000.00", body["income"])
	require.Equal(t, "-40.00", body["expenses"])

	require.Contains(t, body, "income_change_pct")
	require.Nil(t, body["income_change_pct"], "a delta against an empty May came back as a number")
	require.Nil(t, body["expenses_change_pct"])
	require.Nil(t, body["net_change_pct"])

	// And the same rule one level down: a row with no counterpart last month.
	for _, raw := range body["top_categories"].([]any) {
		row := raw.(map[string]any)
		if row["label"] == "Groceries" {
			require.Nil(t, row["change_pct"])
		}
	}
}

func TestAPercentageDeltaAgainstARealPriorPeriodIsANumber(t *testing.T) {
	// The other half of the rule: null has to mean "no prior figure", not
	// "this endpoint never computes one".
	l := buildReportsLedger(t)
	body := monthlySummary(l, "2026-08")
	// July held one −100.00 row and no income at all. Expenses went from
	// −100.00 to −410.00, which is −310% against the magnitude of the prior
	// month — the delta keeps the storage sign convention rather than being
	// flipped for display here.
	require.Nil(t, body["income_change_pct"])
	require.Equal(t, "-310", body["expenses_change_pct"])
}

// --- The wire ----------------------------------------------------------------

func TestEveryReportFigureCrossesTheWireAsAString(t *testing.T) {
	l := buildReportsLedger(t)
	result := runReportAt(l, reportsAugust+"&mode=summary&rows=category&columns=time")

	totals := result["totals"].(map[string]any)
	for _, key := range []string{"income", "expenses", "net"} {
		require.IsType(t, "", totals[key], "totals.%s is not a string", key)
	}
	pivot := result["summary"].(map[string]any)
	require.IsType(t, "", pivot["total"])
	for _, raw := range pivot["rows"].([]any) {
		for _, cell := range raw.(map[string]any)["cells"].([]any) {
			require.IsType(t, "", cell)
		}
	}
}

func TestAnUnknownKnobIsRefusedRatherThanDefaulted(t *testing.T) {
	// A misspelled dimension that fell back to a default would render a report
	// the caller did not ask for, with no way to tell.
	l := buildReportsLedger(t)
	for _, query := range []string{
		"mode=pivot", "rows=colour", "sign=positive",
		"mode=summary&columns=weather", "mode=summary&columns=time&time_grain=fortnight",
		"preset=not_a_report",
	} {
		l.alex.get("/reports/run?" + query).requireStatus(http.StatusUnprocessableEntity)
	}
}

// --- Saved reports and tenancy -----------------------------------------------

func TestASavedReportIsAFilterAndRunsWithIt(t *testing.T) {
	// Ground rule 3: one Filter entity, mounted everywhere. A saved report is
	// a filter row with the report scope, not a second filter model.
	l := buildReportsLedger(t)
	created := l.alex.post("/reports", map[string]any{
		"name":   "Grocery spending",
		"config": map[string]any{"mode": "transaction", "rows": "category", "sign": "expenses"},
		"items": []map[string]any{
			{"field": "category", "operator": "in", "value_ids": []string{l.str("groceries")}},
		},
	}).requireStatus(http.StatusCreated).json()

	id := created["id"].(string)
	require.Equal(t, "transaction", created["config"].(map[string]any)["mode"])

	result := l.alex.get("/reports/run?filter_id=" + id + "&mode=transaction&rows=category&sign=expenses&" + reportsAugust).
		requireStatus(http.StatusOK).json()
	require.Equal(t, id, result["filter_id"])
	require.Equal(t, "-50.00", result["transaction"].(map[string]any)["total"])

	listed := l.alex.get("/reports").requireStatus(http.StatusOK).list()
	require.Equal(t, id, findByID(t, listed, id)["id"])
}

func TestAViewerRunsAReportButCannotSaveOne(t *testing.T) {
	// Running a report is a read. A viewer who could not look at one would be
	// locked out of the half of the application that is reports.
	l := buildReportsLedger(t)
	vera := l.as("vera")

	vera.get("/reports/run?" + reportsAugust).requireStatus(http.StatusOK)
	vera.get("/reports/monthly-summary?month=2026-08").requireStatus(http.StatusOK)
	vera.get("/reports").requireStatus(http.StatusOK)

	vera.post("/reports", map[string]any{"name": "Mine"}).requireStatus(http.StatusForbidden)
}

func TestAReportRefusesAFilterFromAnotherSpace(t *testing.T) {
	l := buildReportsLedger(t)
	l.alex.get("/reports/run?filter_id=" + l.str("stranger_filter")).
		requireStatus(http.StatusConflict)
}

func TestASavedReportInAnotherSpaceIsNotFound(t *testing.T) {
	l := buildReportsLedger(t)
	ctx := t.Context()
	stranger := &store.Filter{Name: "Theirs", Scope: reportScope}
	require.NoError(t, db(t).CreateFilter(ctx, store.SpaceIDOf(l.id("other_space")), stranger))

	for _, path := range []string{
		"/reports/" + stranger.ID.String(),
		// A watchlist filter is not a report, even in this space.
		"/reports/" + l.str("filter"),
	} {
		l.alex.get(path).requireStatus(http.StatusNotFound)
	}
	l.alex.patch("/reports/"+stranger.ID.String(), map[string]any{"name": "Mine now"}).
		requireStatus(http.StatusNotFound)
	l.alex.del("/reports/" + stranger.ID.String()).requireStatus(http.StatusNotFound)
}

func TestAReportNeverReachesAnotherSpacesRows(t *testing.T) {
	l := buildReportsLedger(t)
	result := runReportAt(l, "mode=transaction&rows=payee")
	require.NotContains(t, reportLabels(groupsOf(result)), "Not Yours")
}

// A filtered report shows the parts the filter matched, not every part of a
// row one part matched.
//
// The watchlist and the report are the same Filter over the same ledger — the
// screen hands a watchlist's own filter_id straight to this endpoint — so a
// $50 row split $30 Groceries / $20 Dining has to read $30 in both. A Select
// that keeps whole postings while the expansion takes all of a row's splits
// would read $30 on the watchlist and $50 here, with a Dining line inside a
// Groceries report.
func TestAFilteredReportCountsOnlyTheSplitsTheFilterMatched(t *testing.T) {
	l := buildReportsLedger(t)
	split := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-14",
		"amount": "-50.00", "payee": "Corner Market",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	l.alex.put("/transactions/"+split+"/splits", map[string]any{
		"splits": []map[string]any{
			{"amount": "-30.00", "category_id": l.str("groceries")},
			{"amount": "-20.00", "category_id": l.str("streaming")},
		},
	}).requireStatus(http.StatusOK)

	groceries := l.alex.post("/filters", map[string]any{
		"name": "Groceries only", "scope": "report",
		"items": []map[string]any{{
			"field": "category", "operator": "in", "value_ids": []string{l.str("groceries")},
		}},
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	report := runReportAt(l, reportsAugust+"&mode=transaction&rows=category&filter_id="+groceries)

	labels, lines := walkReport(report)
	require.NotContains(t, labels, "Streaming",
		"a part the filter rejected is not a group in a filtered report")

	total := domain.Zero
	for _, row := range lines {
		if row["transaction_id"] == split {
			total = total.Add(domain.MustFromString(row["amount"].(string)))
		}
	}
	require.Equal(t, "-30.00", total.String(), "the matched split, not the whole row")
}

// walkReport flattens the drill-down: group labels at every depth, and every
// transaction line under any of them.
func walkReport(report map[string]any) ([]string, []map[string]any) {
	var labels []string
	var lines []map[string]any
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, raw := range nodes {
			node := raw.(map[string]any)
			labels = append(labels, node["label"].(string))
			if rows, ok := node["transactions"].([]any); ok {
				for _, line := range rows {
					lines = append(lines, line.(map[string]any))
				}
			}
			if kids, ok := node["children"].([]any); ok {
				walk(kids)
			}
		}
	}
	walk(report["transaction"].(map[string]any)["groups"].([]any))
	return labels, lines
}

// One function, one currency. A whole-row branch that reads the primary
// amount while the split branch reads the split raw would report a
// foreign-currency row's parts in the wrong currency, and they would not sum
// to the row above them.
func TestASplitsShareOfAConvertedRowIsReportedInPrimary(t *testing.T) {
	l := buildReportsLedger(t)
	euro := newAccount(l, "Euro Account", string(domain.KindCash), "checking", "0.00")
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE accounts SET currency = 'EUR' WHERE id = $1`, uuid.MustParse(euro))
	require.NoError(t, err)

	row := &store.Transaction{
		AccountID: uuid.MustParse(euro), Date: domain.NewDate(2026, time.August, 14),
		Currency: "EUR", Amount: domain.MustFromString("-100.00"),
		AmountPrimary: domain.MustFromString("-110.00"), HasAmountPrimary: true,
		StatementName: "EURO SHOP", Payee: "Euro Shop",
		Splits: []store.Split{
			{Position: 0, Amount: domain.MustFromString("-60.00"), CategoryID: l.id("groceries")},
			{Position: 1, Amount: domain.MustFromString("-40.00"), CategoryID: l.id("streaming")},
		},
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), row))

	report := runReportAt(l, reportsAugust+"&mode=transaction&rows=category")

	parts := domain.Zero
	_, lines := walkReport(report)
	for _, one := range lines {
		if one["transaction_id"] == row.ID.String() {
			parts = parts.Add(domain.MustFromString(one["amount"].(string)))
		}
	}
	require.Equal(t, "-110.00", parts.String(),
		"the parts sum to what the row converted to, not to its native figure")
}

// --- The category's own exclusion -------------------------------------------

// Ground rule 4 has a third level. `excluded_from_reports` and
// `excluded_from_spending_plan` are set on transactions, on accounts *and on
// categories*. The category pair can be stored, imported and offered by the
// settings screen while no predicate reads it: store.DomainCategory has to
// carry both on the way into the domain, or every report counts a category
// the user switched off.
//
// These go through the PATCH the settings screen sends, not a direct UPDATE:
// the claim under test is that the switch in the UI does what it says.

func excludeCategory(l *ledger, categoryID uuid.UUID, patch map[string]any) {
	l.t.Helper()
	l.alex.patch("/categories/"+categoryID.String(), patch).requireStatus(http.StatusOK)
}

// categoryTotal finds a group by label at any depth: the drill-down nests
// categories under the sign group, and how deep is not what these tests are
// about.
func categoryTotal(t *testing.T, report map[string]any, label string) string {
	t.Helper()
	var find func(nodes []any) map[string]any
	find = func(nodes []any) map[string]any {
		for _, raw := range nodes {
			node := raw.(map[string]any)
			if node["label"] == label {
				return node
			}
			if kids, ok := node["children"].([]any); ok {
				if hit := find(kids); hit != nil {
					return hit
				}
			}
		}
		return nil
	}
	node := find(groupsOf(report))
	require.NotNil(t, node, "no report group labelled %q", label)
	return node["total"].(string)
}

func TestACategoryExcludedFromReportsLeavesTheReport(t *testing.T) {
	l := buildReportsLedger(t)
	query := reportsAugust + "&mode=transaction&rows=category&sign=expenses"

	before := runReportAt(l, query)
	require.Equal(t, "-200.00", categoryTotal(t, before, "Charitable Donations"))
	beforeTotal := before["transaction"].(map[string]any)["total"].(string)

	excludeCategory(l, l.id("donations"), map[string]any{"excluded_from_reports": true})

	after := runReportAt(l, query)
	labels, _ := walkReport(after)
	require.NotContains(t, labels, "Charitable Donations",
		"the excluded category is still a row in the report")

	// And the money it held left the grand total, rather than being hidden
	// from the drill-down while still counted above it.
	require.Equal(t,
		domain.MustFromString(beforeTotal).Sub(domain.MustFromString("-200.00")).String(),
		after["transaction"].(map[string]any)["total"].(string))
}

func TestExcludingACategoryFromThePlanLeavesReportsAlone(t *testing.T) {
	// The two are independent at the category level exactly as they are at the
	// other two. Ticking the plan box must not empty a report.
	l := buildReportsLedger(t)
	query := reportsAugust + "&mode=transaction&rows=category&sign=expenses"

	excludeCategory(l, l.id("donations"), map[string]any{"excluded_from_spending_plan": true})

	after := runReportAt(l, query)
	require.Equal(t, "-200.00", categoryTotal(t, after, "Charitable Donations"))
}

func TestOnlyTheSplitFiledUnderAnExcludedCategoryLeavesTheReport(t *testing.T) {
	// Splits are the report's unit, and the selection above them asks the
	// question of the *parent* posting — which a split row has no category on.
	// One receipt, two categories, one of them excluded: the excluded share
	// goes and its sibling stays.
	l := buildReportsLedger(t)
	row := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 14),
		Currency: "USD", Amount: domain.MustFromString("-100.00"),
		StatementName: "BIG BOX #77", Payee: "Big Box",
		Splits: []store.Split{
			{Position: 0, Amount: domain.MustFromString("-60.00"), CategoryID: l.id("groceries")},
			{Position: 1, Amount: domain.MustFromString("-40.00"), CategoryID: l.id("donations")},
		},
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), row))

	query := reportsAugust + "&mode=transaction&rows=category&sign=expenses"
	shareOf := func(report map[string]any) domain.Money {
		sum := domain.Zero
		_, lines := walkReport(report)
		for _, one := range lines {
			if one["transaction_id"] == row.ID.String() {
				sum = sum.Add(domain.MustFromString(one["amount"].(string)))
			}
		}
		return sum
	}

	require.Equal(t, "-100.00", shareOf(runReportAt(l, query)).String())

	excludeCategory(l, l.id("donations"), map[string]any{"excluded_from_reports": true})

	require.Equal(t, "-60.00", shareOf(runReportAt(l, query)).String(),
		"the split under the excluded category is still being counted")
}
