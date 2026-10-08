package api

import (
	"context"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The Spending report: a calendar month, quarter or year beside a comparison
// (calculations.md §11). It reads the register's own query — accounts, the
// report's filter and the chart's category drill — through matchRegister and
// totals through domain.Aggregate, so its figures are the register Spending
// tab's for the same window and scope. One request answers every view of one
// state: the chart, the cards, the breakdown, the table and the flow.

func (s reportService) GetSpendingReport(
	ctx context.Context, req *agentifiv1.GetSpendingReportRequest,
) (*agentifiv1.GetSpendingReportResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	query, err := registerQueryOf(spendingRegister{req}, req.Reviewed, nil, nil)
	if err != nil {
		return nil, err
	}
	options, err := aggregateOptionsOf(req.GetDirection(), req.GetGroupBy(), req.GetUnder())
	if err != nil {
		return nil, err
	}
	options.Direction = domain.AggregateSpending
	options.Mode = domain.DateEffective

	grain := domain.GrainMonth
	if raw := strings.TrimSpace(req.GetGrain()); raw != "" {
		parsed, ok := domain.ParsePeriodGrain(raw)
		if !ok {
			return nil, errInvalid("enum", []string{"query", "grain"},
				"grain must be month, quarter or year, got %q", raw)
		}
		grain = parsed
	}
	today := domain.DateOf(env.now())
	current := domain.PeriodOf(today, grain)
	selected := current
	if on, given, err := parseQueryDate("period", req.GetPeriod()); err != nil {
		return nil, err
	} else if given {
		selected = domain.PeriodOf(on, grain)
		if selected.Start.After(current.Start) {
			return nil, errInvalid("future_period", []string{"query", "period"},
				"%s has not started yet", selected.Key())
		}
	}
	comparison, err := spendingComparisonOf(req.GetCompare(), grain)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	matched, err := matchRegister(ctx, env, sp, query)
	if err != nil {
		return nil, err
	}
	postings := matched.Postings
	options.Partial = matched.Partial
	if err := nameAggregate(ctx, env, sp, &options); err != nil {
		return nil, err
	}

	total := options
	total.GroupBy, total.Under = domain.AggregateByNone, ""
	income := total
	income.Direction = domain.AggregateIncome

	periods := make([]*agentifiv1.SpendingReportChartPeriod, 0, len(chart))
	for _, period := range chart {
		window := domain.PeriodWindow(period, today)
		bar := domain.SummarizeSpending(
			domain.SpendingIn(postings, window, income).Total,
			domain.SpendingIn(postings, window, total).Total)
		periods = append(periods, &agentifiv1.SpendingReportChartPeriod{
			Key:       window.Period.Key(),
			From:      window.From.String(),
			Through:   window.Through.String(),
			End:       window.Period.End().String(),
			Partial:   window.Partial,
			Income:    moneyProto(bar.Income),
			Spent:     moneyProto(bar.Spent),
			Remaining: moneyProto(bar.Remaining),
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
	projection, err := projectSpending(ctx, env, sp, query, options, summary, selectedWindow)
	if err != nil {
		return nil, err
	}

	out := &agentifiv1.GetSpendingReportResponse{
		Grain:  string(grain),
		Today:  today.String(),
		Period: spendingPeriodProto(selectedWindow),
		Window: windowProto(Window{
			From: selectedWindow.From, HasFrom: true, To: selectedWindow.Through, HasTo: true,
			Mode: domain.DateEffective,
		}),
		Periods: periods,
		Compare: string(comparison),
		Summary: &agentifiv1.SpendingReportSummary{
			Income:       moneyProto(summary.Income),
			Spent:        moneyProto(summary.Spent),
			Remaining:    moneyProto(summary.Remaining),
			SavingsRate:  rateProto(summary.SavingsRate, summary.HasRates),
			SpendingRate: rateProto(summary.SpendingRate, summary.HasRates),
			Rating:       string(summary.Rating),
		},
		GroupBy:            string(options.GroupBy),
		UncategorizedCount: int32(domain.CountUncategorized(postings, selectedWindow, domain.DateEffective)),
	}
	if projection != nil {
		expected := projection.Summary
		out.Summary.Rating = string(expected.Rating)
		out.Summary.Projection = &agentifiv1.SpendingReportProjection{
			End:            projection.End.String(),
			ExpectedIncome: moneyProto(projection.ExpectedIncome),
			ExpectedSpent:  moneyProto(projection.ExpectedSpent),
			Count:          int32(projection.Count),
			Income:         moneyProto(expected.Income),
			Spent:          moneyProto(expected.Spent),
			Remaining:      moneyProto(expected.Remaining),
			SavingsRate:    rateProto(expected.SavingsRate, expected.HasRates),
			SpendingRate:   rateProto(expected.SpendingRate, expected.HasRates),
		}
	}
	for _, option := range domain.SpendingComparisonsFor(grain) {
		out.CompareOptions = append(out.CompareOptions, string(option))
	}
	if hasComparison {
		windows := make([]*agentifiv1.SpendingReportPeriod, 0, len(comparisonWindows))
		for _, window := range comparisonWindows {
			windows = append(windows, spendingPeriodProto(window))
		}
		out.Comparison = &agentifiv1.SpendingReportComparison{
			Compare:    string(comparison),
			Periods:    windows,
			Average:    comparison.IsAverage(),
			Spent:      moneyProto(average.Total),
			Difference: spendingDifferenceProto(domain.CompareSpending(spent.Total, average.Total, true)),
		}
	}
	for _, row := range domain.SpendingRows(spent, average, hasComparison) {
		out.Rows = append(out.Rows, &agentifiv1.SpendingReportRow{
			Key:        row.Key,
			Label:      row.Label,
			Amount:     moneyProto(row.Amount),
			Comparison: moneyProto(row.Comparison),
			Difference: spendingDifferenceProto(row.Difference),
			Share:      rateProto(row.Share, row.HasShare),
		})
	}

	earliest, hasEarliest := domain.FirstReportingDate(postings, domain.DateEffective)
	tablePeriods := domain.TablePeriods(grain, today, earliest, hasEarliest)
	columns := make([]domain.AggregateResult, 0, len(tablePeriods))
	out.Table = &agentifiv1.SpendingReportTable{
		Periods: make([]*agentifiv1.SpendingReportPeriod, 0, len(tablePeriods)),
		Prior:   spendingPeriodProto(priorWindow),
	}
	for _, period := range tablePeriods {
		window := domain.PeriodWindow(period, today)
		out.Table.Periods = append(out.Table.Periods, spendingPeriodProto(window))
		columns = append(columns, domain.SpendingIn(postings, window, options))
	}
	for _, row := range domain.SpendingTable(columns, domain.SpendingIn(postings, priorWindow, options)) {
		out.Table.Rows = append(out.Table.Rows, &agentifiv1.SpendingReportTableRow{
			Key:        row.Key,
			Label:      row.Label,
			Cells:      moneyProtos(row.Cells),
			Total:      moneyProto(row.Total),
			Difference: spendingDifferenceProto(row.Difference),
		})
	}

	sources := income
	sources.GroupBy = domain.AggregateByCategory
	topLevel := domain.SpendingIn(postings, selectedWindow, sources)
	if under := domain.IncomeFlowUnder(topLevel, options.Categories); under != "" {
		sources.Under = under
		topLevel = domain.SpendingIn(postings, selectedWindow, sources)
	}
	out.Flow = spendingFlowProto(domain.FlowOfSpending(topLevel, spent))
	return out, nil
}

// spendingRegister is the report's request as the register query reads it.
// The report resolves its own window and neither pages nor orders, so those
// knobs are empty.
type spendingRegister struct {
	*agentifiv1.GetSpendingReportRequest
}

func (spendingRegister) GetFrom() string      { return "" }
func (spendingRegister) GetTo() string        { return "" }
func (spendingRegister) GetDateField() string { return "" }
func (spendingRegister) GetPadding() string   { return "" }
func (spendingRegister) GetOrder() string     { return "" }

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

// spendingComparisonOf reads `compare`, which must be on the grain's menu.
// Empty, it is the prior period.
func spendingComparisonOf(raw string, grain domain.PeriodGrain) (domain.SpendingComparison, error) {
	raw = strings.TrimSpace(raw)
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

func spendingPeriodProto(window domain.SpendingWindow) *agentifiv1.SpendingReportPeriod {
	return &agentifiv1.SpendingReportPeriod{
		Key:     window.Period.Key(),
		From:    window.From.String(),
		Through: window.Through.String(),
		End:     window.Period.End().String(),
		Partial: window.Partial,
	}
}

func spendingDifferenceProto(difference domain.SpendingDifference) *agentifiv1.SpendingReportDifference {
	return &agentifiv1.SpendingReportDifference{
		Amount: moneyProto(difference.Amount),
		Pct:    rateProto(difference.Pct, difference.HasPct),
		State:  string(difference.State),
	}
}

func spendingFlowProto(flow domain.SpendingFlow) *agentifiv1.SpendingReportFlow {
	nodes := func(from []domain.FlowNode) []*agentifiv1.SpendingReportFlowNode {
		out := make([]*agentifiv1.SpendingReportFlowNode, 0, len(from))
		for _, node := range from {
			out = append(out, &agentifiv1.SpendingReportFlowNode{
				Key: node.Key, Label: node.Label, Amount: moneyProto(node.Amount),
				Share: rateProto(node.Share, node.HasShare),
			})
		}
		return out
	}
	return &agentifiv1.SpendingReportFlow{
		Income:      nodes(flow.Income),
		Credits:     nodes(flow.Credits),
		Spending:    nodes(flow.Spending),
		IncomeTotal: moneyProto(flow.IncomeTotal),
		Spent:       moneyProto(flow.Spent),
		SpentShare:  rateProto(flow.SpentShare, flow.HasSpentShare),
	}
}
