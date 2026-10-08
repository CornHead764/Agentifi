package api

import (
	"context"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The Savings report: balances, not transactions. It reads the Net Worth
// page's balance series, narrowed to savings accounts, so the two screens
// cannot disagree about what an account held on a day.

func (s reportService) GetSavingsReport(
	ctx context.Context, req *agentifiv1.GetSavingsReportRequest,
) (*agentifiv1.GetSavingsReportResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	window, err := windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
	if err != nil {
		return nil, err
	}
	requested, err := parseGranularity(req.GetGranularity())
	if err != nil {
		return nil, err
	}

	accounts, err := env.DB.ListAccounts(ctx, sp.ID(), store.AccountQuery{IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	savings := make([]store.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Type == "savings" {
			savings = append(savings, account)
		}
	}

	series, err := newBalanceSeries(ctx, env, sp, savings, window, requested)
	if err != nil {
		return nil, err
	}
	converter, err := newBalanceConverter(ctx, env, sp, savings, series.end)
	if err != nil {
		return nil, err
	}

	total := func(balances map[domain.ID]domain.Money) domain.Money {
		var sum domain.Money
		for _, account := range savings {
			sum = sum.Add(balances[domain.ID(account.ID.String())])
		}
		return sum.Round()
	}

	points := make([]*agentifiv1.SavingsPoint, 0, len(series.samples))
	for _, on := range series.samples {
		points = append(points, &agentifiv1.SavingsPoint{
			On: on.String(), Balance: moneyProto(total(converter.apply(series.on(on)))),
		})
	}

	start := total(converter.apply(series.on(series.start)))
	end := total(converter.apply(series.on(series.end)))
	change := end.Sub(start).Round()
	changePct, hasChangePct := domain.Percent(change, start.Abs())

	// One column per month the window touches; a month still in progress is
	// read at the window's end, which is the balance the drawer shows today.
	monthList := domain.MonthsBetween(domain.MonthOf(series.start), domain.MonthOf(series.end))
	months := make([]string, 0, len(monthList))
	rows := make([]*agentifiv1.SavingsAccountRow, len(savings))
	for index, account := range savings {
		rows[index] = &agentifiv1.SavingsAccountRow{
			AccountId: account.ID.String(), Name: account.Name,
			Cells: make([]*agentifiv1.Money, 0, len(monthList)),
		}
	}
	totals := make([]*agentifiv1.Money, 0, len(monthList))
	for _, month := range monthList {
		months = append(months, month.String())
		day := month.LastDay()
		if day.After(series.end) {
			day = series.end
		}
		balances := converter.apply(series.on(day))
		var columnTotal domain.Money
		for index, account := range savings {
			balance := balances[domain.ID(account.ID.String())].Round()
			rows[index].Cells = append(rows[index].Cells, moneyProto(balance))
			columnTotal = columnTotal.Add(balance)
		}
		totals = append(totals, moneyProto(columnTotal.Round()))
	}

	return &agentifiv1.GetSavingsReportResponse{
		Window:      windowProto(window),
		Granularity: series.granularity,
		Points:      points,
		End:         moneyProto(end),
		Change:      moneyProto(change),
		ChangePct:   rateProto(changePct, hasChangePct),
		Months:      months,
		Accounts:    rows,
		Totals:      totals,
	}, nil
}
