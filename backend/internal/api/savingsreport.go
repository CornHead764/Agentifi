package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The Savings report: balances, not transactions. It reads the Net Worth
// page's balance series, narrowed to savings accounts, so the two screens
// cannot disagree about what an account held on a day.

// SavingsPoint is one sampled day of the chart.
type SavingsPoint struct {
	On      Date         `json:"on"`
	Balance domain.Money `json:"balance"`
}

// SavingsAccountRow is one savings account across the month columns.
type SavingsAccountRow struct {
	AccountID uuid.UUID `json:"account_id"`
	Name      string    `json:"name"`
	// Cells runs parallel to Months: the balance at each month's end, with
	// the window's own end standing in for a month still in progress.
	Cells []domain.Money `json:"cells"`
}

type SavingsReportResponse struct {
	Window      WindowResponse `json:"window"`
	Granularity string         `json:"granularity"`
	Points      []SavingsPoint `json:"points"`

	End       domain.Money `json:"end"`
	Change    domain.Money `json:"change"`
	ChangePct *domain.Rate `json:"change_pct"`

	// Months is the pivot's columns as `YYYY-MM`; Totals is its Total row.
	Months   []string            `json:"months"`
	Accounts []SavingsAccountRow `json:"accounts"`
	Totals   []domain.Money      `json:"totals"`
}

func readSavingsReport(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	window, err := WindowFromRequest(r)
	if err != nil {
		return err
	}
	requested, err := granularityFromRequest(r)
	if err != nil {
		return err
	}

	accounts, err := env.DB.ListAccounts(r.Context(), sp.ID(),
		store.AccountQuery{IncludeClosed: true})
	if err != nil {
		return err
	}
	savings := make([]store.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Type == "savings" {
			savings = append(savings, account)
		}
	}

	series, err := newBalanceSeries(r.Context(), env, sp, savings, window, requested)
	if err != nil {
		return err
	}
	converter, err := newBalanceConverter(r.Context(), env, sp, savings, series.end)
	if err != nil {
		return err
	}

	total := func(balances map[domain.ID]domain.Money) domain.Money {
		var sum domain.Money
		for _, account := range savings {
			sum = sum.Add(balances[domain.ID(account.ID.String())])
		}
		return sum.Round()
	}

	points := make([]SavingsPoint, 0, len(series.samples))
	for _, on := range series.samples {
		points = append(points, SavingsPoint{
			On: Date(on), Balance: total(converter.apply(series.on(on))),
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
	rows := make([]SavingsAccountRow, len(savings))
	for index, account := range savings {
		rows[index] = SavingsAccountRow{
			AccountID: account.ID, Name: account.Name,
			Cells: make([]domain.Money, 0, len(monthList)),
		}
	}
	totals := make([]domain.Money, 0, len(monthList))
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
			rows[index].Cells = append(rows[index].Cells, balance)
			columnTotal = columnTotal.Add(balance)
		}
		totals = append(totals, columnTotal.Round())
	}

	return writeJSON(w, http.StatusOK, SavingsReportResponse{
		Window:      windowResponse(window),
		Granularity: series.granularity,
		Points:      points,
		End:         end,
		Change:      change,
		ChangePct:   store.PtrIf(changePct, hasChangePct),
		Months:      months,
		Accounts:    rows,
		Totals:      totals,
	})
}
