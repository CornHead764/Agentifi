package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The Spending report: a calendar month, quarter or year beside a comparison
// (calculations.md §11). It reads the register's own query — accounts, the
// report's filter and the chart's category drill — through matchRegister and
// totals through domain.Aggregate, so its figures are the register Spending
// tab's for the same window and scope. One request answers every view of one
// state: the chart, the cards, the breakdown, the table and the flow.

// SpendingPeriodResponse is one period's window.
type SpendingPeriodResponse struct {
	Key     string `json:"key"`
	From    Date   `json:"from"`
	Through Date   `json:"through"`
	// End is the period's last day, past Through when it is cut short.
	End     Date `json:"end"`
	Partial bool `json:"partial"`
}

// SpendingChartPeriod is one bar of the period chart.
type SpendingChartPeriod struct {
	SpendingPeriodResponse
	Income domain.Money `json:"income"`
	// Spent is a ledger amount: negative is spending.
	Spent domain.Money `json:"spent"`
	// Remaining is Income plus Spent; negative is overspent.
	Remaining domain.Money `json:"remaining"`
}

type SpendingDifferenceResponse struct {
	// Amount is this period's spend less the comparison's: positive is more
	// spent.
	Amount domain.Money `json:"amount"`
	// Pct is in percent, null against a comparison of zero.
	Pct *domain.Rate `json:"pct"`
	// State is change, new_spend, no_spend or none.
	State string `json:"state"`
}

type SpendingComparisonResponse struct {
	Compare string `json:"compare"`
	// Periods is the windows compared with, each cut as the selected one is.
	Periods []SpendingPeriodResponse `json:"periods"`
	Average bool                     `json:"average"`
	// Spent is the comparison's spending, averaged when Average.
	Spent      domain.Money               `json:"spent"`
	Difference SpendingDifferenceResponse `json:"difference"`
}

type SpendingSummaryResponse struct {
	Income    domain.Money `json:"income"`
	Spent     domain.Money `json:"spent"`
	Remaining domain.Money `json:"remaining"`
	// SavingsRate and SpendingRate are fractions, null with nothing coming in.
	SavingsRate  *domain.Rate `json:"savings_rate"`
	SpendingRate *domain.Rate `json:"spending_rate"`
	// Rating is none, low, good or great. For the period in progress it
	// rates the projection's savings rate.
	Rating string `json:"rating"`
	// Projection is the period in progress carried to its end by what is
	// still scheduled; null for a whole period, a stored filter or a drill.
	Projection *SpendingProjectionResponse `json:"projection"`
}

// SpendingProjectionResponse is the cards as the period in progress is
// expected to close.
type SpendingProjectionResponse struct {
	// End is the period's last day.
	End Date `json:"end"`
	// ExpectedIncome and ExpectedSpent are the reminders still to come;
	// ExpectedSpent is a ledger amount.
	ExpectedIncome domain.Money `json:"expected_income"`
	ExpectedSpent  domain.Money `json:"expected_spent"`
	// Count is how many reminders the expected figures hold.
	Count     int          `json:"count"`
	Income    domain.Money `json:"income"`
	Spent     domain.Money `json:"spent"`
	Remaining domain.Money `json:"remaining"`
	// SavingsRate and SpendingRate are fractions, null with nothing coming in.
	SavingsRate  *domain.Rate `json:"savings_rate"`
	SpendingRate *domain.Rate `json:"spending_rate"`
}

type SpendingRowResponse struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Amount and Comparison are ledger amounts: a line netting to a credit
	// is positive.
	Amount     domain.Money               `json:"amount"`
	Comparison domain.Money               `json:"comparison"`
	Difference SpendingDifferenceResponse `json:"difference"`
	// Share is the line's part of the period's spending, null for a credit.
	Share *domain.Rate `json:"share"`
}

type SpendingTableRowResponse struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Cells runs parallel to the table's periods.
	Cells      []domain.Money             `json:"cells"`
	Total      domain.Money               `json:"total"`
	Difference SpendingDifferenceResponse `json:"difference"`
}

type SpendingTableResponse struct {
	Periods []SpendingPeriodResponse `json:"periods"`
	// Prior is the period the last column is compared with, cut to match.
	Prior SpendingPeriodResponse     `json:"prior"`
	Rows  []SpendingTableRowResponse `json:"rows"`
}

type SpendingFlowNode struct {
	Key    string       `json:"key"`
	Label  string       `json:"label"`
	Amount domain.Money `json:"amount"`
	// Share is of the period's income, null with none.
	Share *domain.Rate `json:"share"`
}

type SpendingFlowResponse struct {
	Income      []SpendingFlowNode `json:"income"`
	Credits     []SpendingFlowNode `json:"credits"`
	Spending    []SpendingFlowNode `json:"spending"`
	IncomeTotal domain.Money       `json:"income_total"`
	// Spent is a magnitude here, as every band is.
	Spent      domain.Money `json:"spent"`
	SpentShare *domain.Rate `json:"spent_share"`
}

type SpendingReportResponse struct {
	Grain string `json:"grain"`
	Today Date   `json:"today"`
	// Period is the selected one; Window is its dates as a register window,
	// for the transaction list beneath (trap 5).
	Period  SpendingPeriodResponse `json:"period"`
	Window  WindowResponse         `json:"window"`
	Periods []SpendingChartPeriod  `json:"periods"`

	Compare        string   `json:"compare"`
	CompareOptions []string `json:"compare_options"`
	// Comparison is null when there is nothing to compare with.
	Comparison *SpendingComparisonResponse `json:"comparison"`

	Summary SpendingSummaryResponse `json:"summary"`
	GroupBy string                  `json:"group_by"`
	Rows    []SpendingRowResponse   `json:"rows"`
	// UncategorizedCount is the rows in the period still needing a category.
	UncategorizedCount int                   `json:"uncategorized_count"`
	Table              SpendingTableResponse `json:"table"`
	Flow               SpendingFlowResponse  `json:"flow"`
}

func readSpendingReport(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	query, err := registerQuery(r)
	if err != nil {
		return err
	}
	options, err := aggregateOptions(r)
	if err != nil {
		return err
	}
	options.Direction = domain.AggregateSpending
	options.Mode = domain.DateEffective

	grain := domain.GrainMonth
	if raw := strings.TrimSpace(r.URL.Query().Get("grain")); raw != "" {
		parsed, ok := domain.ParsePeriodGrain(raw)
		if !ok {
			return errInvalid("enum", []string{"query", "grain"},
				"grain must be month, quarter or year, got %q", raw)
		}
		grain = parsed
	}
	today := domain.DateOf(env.now())
	current := domain.PeriodOf(today, grain)
	selected := current
	if on, given, err := queryDate(r, "period"); err != nil {
		return err
	} else if given {
		selected = domain.PeriodOf(on, grain)
		if selected.Start.After(current.Start) {
			return errInvalid("future_period", []string{"query", "period"},
				"%s has not started yet", selected.Key())
		}
	}
	comparison, err := spendingComparisonFromRequest(r, grain)
	if err != nil {
		return err
	}

	selectedWindow := domain.PeriodWindow(selected, today)
	comparisonWindows := domain.ComparisonWindows(selectedWindow, comparison)
	chart := domain.ChartPeriods(grain, today)
	lastWindow := domain.PeriodWindow(current, today)
	priorWindow := lastWindow.SameDaysInto(current.Shift(-1))

	// Every window the answer reads, loaded once. Years reach back to the
	// first row for the table, so they load everything.
	from := chart[0].Start
	for _, window := range append(comparisonWindows, selectedWindow, priorWindow) {
		if window.From.Before(from) {
			from = window.From
		}
	}
	query.Window, err = ResolveWindow(from, grain != domain.GrainYear, today, true, domain.DateEffective)
	if err != nil {
		return err
	}
	matched, err := matchRegister(r.Context(), env, sp, query)
	if err != nil {
		return err
	}
	postings := matched.Postings
	options.Partial = matched.Partial
	if err := nameAggregate(r.Context(), env, sp, &options); err != nil {
		return err
	}

	total := options
	total.GroupBy, total.Under = domain.AggregateByNone, ""
	income := total
	income.Direction = domain.AggregateIncome

	periods := make([]SpendingChartPeriod, 0, len(chart))
	for _, period := range chart {
		window := domain.PeriodWindow(period, today)
		bar := domain.SummarizeSpending(
			domain.SpendingIn(postings, window, income).Total,
			domain.SpendingIn(postings, window, total).Total)
		periods = append(periods, SpendingChartPeriod{
			SpendingPeriodResponse: spendingPeriodResponse(window),
			Income:                 bar.Income,
			Spent:                  bar.Spent,
			Remaining:              bar.Remaining,
		})
	}

	spent := domain.SpendingIn(postings, selectedWindow, options)
	compared := make([]domain.AggregateResult, 0, len(comparisonWindows))
	for _, window := range comparisonWindows {
		compared = append(compared, domain.SpendingIn(postings, window, options))
	}
	average, hasComparison := domain.AverageSpending(compared)

	summary := domain.SummarizeSpending(
		domain.SpendingIn(postings, selectedWindow, income).Total, spent.Total)
	projection, err := projectSpending(r.Context(), env, sp, query, options, summary, selectedWindow)
	if err != nil {
		return err
	}

	response := SpendingReportResponse{
		Grain:          string(grain),
		Today:          Date(today),
		Period:         spendingPeriodResponse(selectedWindow),
		Window:         windowResponse(Window{From: selectedWindow.From, HasFrom: true, To: selectedWindow.Through, HasTo: true, Mode: domain.DateEffective}),
		Periods:        periods,
		Compare:        string(comparison),
		CompareOptions: []string{},
		Summary: SpendingSummaryResponse{
			Income:       summary.Income,
			Spent:        summary.Spent,
			Remaining:    summary.Remaining,
			SavingsRate:  store.PtrIf(summary.SavingsRate, summary.HasRates),
			SpendingRate: store.PtrIf(summary.SpendingRate, summary.HasRates),
			Rating:       string(summary.Rating),
		},
		GroupBy:            string(options.GroupBy),
		Rows:               []SpendingRowResponse{},
		UncategorizedCount: domain.CountUncategorized(postings, selectedWindow, domain.DateEffective),
	}
	if projection != nil {
		expected := projection.Summary
		response.Summary.Rating = string(expected.Rating)
		response.Summary.Projection = &SpendingProjectionResponse{
			End:            Date(projection.End),
			ExpectedIncome: projection.ExpectedIncome,
			ExpectedSpent:  projection.ExpectedSpent,
			Count:          projection.Count,
			Income:         expected.Income,
			Spent:          expected.Spent,
			Remaining:      expected.Remaining,
			SavingsRate:    store.PtrIf(expected.SavingsRate, expected.HasRates),
			SpendingRate:   store.PtrIf(expected.SpendingRate, expected.HasRates),
		}
	}
	for _, option := range domain.SpendingComparisonsFor(grain) {
		response.CompareOptions = append(response.CompareOptions, string(option))
	}
	if hasComparison {
		windows := make([]SpendingPeriodResponse, 0, len(comparisonWindows))
		for _, window := range comparisonWindows {
			windows = append(windows, spendingPeriodResponse(window))
		}
		response.Comparison = &SpendingComparisonResponse{
			Compare:    string(comparison),
			Periods:    windows,
			Average:    comparison.IsAverage(),
			Spent:      average.Total,
			Difference: spendingDifferenceResponse(domain.CompareSpending(spent.Total, average.Total, true)),
		}
	}
	for _, row := range domain.SpendingRows(spent, average, hasComparison) {
		response.Rows = append(response.Rows, SpendingRowResponse{
			Key:        row.Key,
			Label:      row.Label,
			Amount:     row.Amount,
			Comparison: row.Comparison,
			Difference: spendingDifferenceResponse(row.Difference),
			Share:      store.PtrIf(row.Share, row.HasShare),
		})
	}

	earliest, hasEarliest := domain.FirstReportingDate(postings, domain.DateEffective)
	tablePeriods := domain.TablePeriods(grain, today, earliest, hasEarliest)
	columns := make([]domain.AggregateResult, 0, len(tablePeriods))
	response.Table = SpendingTableResponse{
		Periods: make([]SpendingPeriodResponse, 0, len(tablePeriods)),
		Prior:   spendingPeriodResponse(priorWindow),
		Rows:    []SpendingTableRowResponse{},
	}
	for _, period := range tablePeriods {
		window := domain.PeriodWindow(period, today)
		response.Table.Periods = append(response.Table.Periods, spendingPeriodResponse(window))
		columns = append(columns, domain.SpendingIn(postings, window, options))
	}
	for _, row := range domain.SpendingTable(columns, domain.SpendingIn(postings, priorWindow, options)) {
		response.Table.Rows = append(response.Table.Rows, SpendingTableRowResponse{
			Key:        row.Key,
			Label:      row.Label,
			Cells:      row.Cells,
			Total:      row.Total,
			Difference: spendingDifferenceResponse(row.Difference),
		})
	}

	sources := income
	sources.GroupBy = domain.AggregateByCategory
	topLevel := domain.SpendingIn(postings, selectedWindow, sources)
	if under := domain.IncomeFlowUnder(topLevel, options.Categories); under != "" {
		sources.Under = under
		topLevel = domain.SpendingIn(postings, selectedWindow, sources)
	}
	response.Flow = spendingFlowResponse(domain.FlowOfSpending(topLevel, spent))

	return writeJSON(w, http.StatusOK, response)
}

// projectionReach is how far past the period's end the reminders are read, so
// a bill due early next month that autopays before this one closes is found.
const projectionReach = 31

// projectSpending carries the period in progress to its end by the reminders
// still due in it (domain.ProjectSpending), over the report's own accounts. A
// stored filter, a search or a drill narrows the report to rows a schedule
// cannot be matched against, so none of them is projected.
func projectSpending(
	ctx context.Context, env *Env, sp auth.SpaceContext, query RegisterQuery,
	options domain.AggregateOptions, actual domain.SpendingSummary, window domain.SpendingWindow,
) (*domain.SpendingProjection, error) {
	if !window.Partial || query.HasFilter || query.Search != "" || options.Under != "" ||
		query.Accounts.selectsNothing() {
		return nil, nil
	}
	reminders, err := loadReminders(ctx, env, sp, window.Through, window.Period.End().AddDays(projectionReach))
	if err != nil {
		return nil, err
	}
	accounts, err := env.DB.ListAccounts(ctx, sp.ID(), query.Accounts.narrow(store.AccountQuery{IncludeClosed: true}))
	if err != nil {
		return nil, err
	}
	scope := domain.ProjectionScope{
		Accounts:   make(map[domain.ID]domain.Account, len(accounts)),
		Series:     make(map[domain.ID]domain.Series, len(reminders)),
		Categories: options.Categories,
		Currency:   sp.Space.PrimaryCurrency,
	}
	for _, account := range accounts {
		scope.Accounts[domain.ID(account.ID.String())] = store.DomainAccount(account)
	}
	expected := make([]domain.Occurrence, 0, len(reminders))
	for _, one := range reminders {
		expected = append(expected, one.Occurrence)
		scope.Series[one.SeriesID] = one.Series
	}
	projection, ok := domain.ProjectSpending(actual, window, expected, scope)
	if !ok {
		return nil, nil
	}
	return &projection, nil
}

// spendingComparisonFromRequest reads `compare`, which must be on the grain's
// menu. Omitted, it is the prior period.
func spendingComparisonFromRequest(r *http.Request, grain domain.PeriodGrain) (domain.SpendingComparison, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("compare"))
	if raw == "" {
		return domain.ComparePrior, nil
	}
	offered := domain.SpendingComparisonsFor(grain)
	for _, option := range offered {
		if string(option) == raw {
			return option, nil
		}
	}
	names := make([]string, 0, len(offered))
	for _, option := range offered {
		names = append(names, string(option))
	}
	return "", errInvalid("enum", []string{"query", "compare"},
		"a %s compares with %s, not %q", grain, strings.Join(names, ", "), raw)
}

func spendingPeriodResponse(window domain.SpendingWindow) SpendingPeriodResponse {
	return SpendingPeriodResponse{
		Key:     window.Period.Key(),
		From:    Date(window.From),
		Through: Date(window.Through),
		End:     Date(window.Period.End()),
		Partial: window.Partial,
	}
}

func spendingDifferenceResponse(difference domain.SpendingDifference) SpendingDifferenceResponse {
	return SpendingDifferenceResponse{
		Amount: difference.Amount,
		Pct:    store.PtrIf(difference.Pct, difference.HasPct),
		State:  string(difference.State),
	}
}

func spendingFlowResponse(flow domain.SpendingFlow) SpendingFlowResponse {
	nodes := func(from []domain.FlowNode) []SpendingFlowNode {
		out := make([]SpendingFlowNode, 0, len(from))
		for _, node := range from {
			out = append(out, SpendingFlowNode{
				Key: node.Key, Label: node.Label, Amount: node.Amount,
				Share: store.PtrIf(node.Share, node.HasShare),
			})
		}
		return out
	}
	return SpendingFlowResponse{
		Income:      nodes(flow.Income),
		Credits:     nodes(flow.Credits),
		Spending:    nodes(flow.Spending),
		IncomeTotal: flow.IncomeTotal,
		Spent:       flow.Spent,
		SpentShare:  store.PtrIf(flow.SpentShare, flow.HasSpentShare),
	}
}
