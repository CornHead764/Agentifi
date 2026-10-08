package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The estimated cash-flow forecast. GetCashFlow is the arithmetic; this is a
// model's reading of the last twelve months, stored by the daily automation
// and broken into the projection card's day windows. It never passes an
// estimate off as a sum:
//
//   - It never answers instead of the projection. A space with no forecast
//     gets `available: false` and a reason, not an error.
//   - Every figure carries its provenance: which model, when, and how many of
//     the window's days it reaches.
//   - The comparison balance comes from GetCashFlow itself, so it is the
//     figure the chart draws.
//
// Asked for exactly one account, it answers with that account's own estimate
// instead: the average of its recent months (service.AccountForecasts), made
// on first open and re-made nightly and by RunCashFlowForecast.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewCashFlowForecastServiceHandler(cashFlowForecastService{env}, opts...)
	})
}

type cashFlowForecastService struct{ env *Env }

// forecastHorizons are the windows the projection card offers. Kept in step
// with HORIZONS in frontend/src/components/transactions/ProjectedCashFlow.tsx.
var forecastHorizons = []int{30, 60, 90, 180}

// maxForecastWindows bounds a request that spells its own horizons. A card with
// more than a handful of ranges is not a card.
const maxForecastWindows = 8

func (s cashFlowForecastService) GetCashFlowForecast(
	ctx context.Context, req *agentifiv1.GetCashFlowForecastRequest,
) (*agentifiv1.GetCashFlowForecastResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	horizons, err := parseForecastHorizons(req.GetHorizons())
	if err != nil {
		return nil, err
	}
	today := domain.DateOf(env.now())
	accountID, forAccount, err := forecastAccount(ctx, env, sp, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	answer := func(forecast *agentifiv1.CashFlowForecast) (*agentifiv1.GetCashFlowForecastResponse, error) {
		return &agentifiv1.GetCashFlowForecastResponse{Forecast: forecast}, nil
	}
	if forAccount {
		row, found, err := accountForecast(ctx, env, sp, accountID, today, false)
		if err != nil {
			return nil, err
		}
		if !found {
			return answer(noAccountForecast(accountID))
		}
		return answer(forecastProto(ctx, env, row, today, horizons, req.GetAccountId()))
	}

	row, err := env.DB.LatestCashFlowForecast(ctx, sp.ID())
	if err != nil {
		if isNotFound(err) {
			return answer(&agentifiv1.CashFlowForecast{
				Unavailable: proto.String("No forecast has been made yet. The estimate comes from the " +
					"\"Estimate the next six months of cash flow\" automation on the " +
					"Assistant page."),
			})
		}
		return nil, err
	}
	return answer(forecastProto(ctx, env, row, today, horizons, req.GetAccountId()))
}

// RunCashFlowForecast re-makes one account's estimate now and answers with it,
// in the same shape the read does.
func (s cashFlowForecastService) RunCashFlowForecast(
	ctx context.Context, req *agentifiv1.RunCashFlowForecastRequest,
) (*agentifiv1.RunCashFlowForecastResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	horizons, err := parseForecastHorizons(req.GetHorizons())
	if err != nil {
		return nil, err
	}
	accountID, forAccount, err := forecastAccount(ctx, env, sp, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	if !forAccount {
		return nil, errBadRequest("re-running an estimate takes one account_id; the household " +
			"forecast is made by its automation on the Assistant page")
	}
	today := domain.DateOf(env.now())
	row, found, err := accountForecast(ctx, env, sp, accountID, today, true)
	if err != nil {
		return nil, err
	}
	if !found {
		return &agentifiv1.RunCashFlowForecastResponse{Forecast: noAccountForecast(accountID)}, nil
	}
	return &agentifiv1.RunCashFlowForecastResponse{
		Forecast: forecastProto(ctx, env, row, today, horizons, req.GetAccountId()),
	}, nil
}

// forecastAccount is the one account a request names, when it names exactly
// one. The account has to be a live one in this space.
func forecastAccount(
	ctx context.Context, env *Env, sp auth.SpaceContext, set *agentifiv1.IdSet,
) (uuid.UUID, bool, error) {
	wanted, err := cashFlowAccounts(set)
	if err != nil || len(wanted.IDs) != 1 {
		return uuid.Nil, false, err
	}
	account, err := env.DB.GetAccount(ctx, sp.ID(), wanted.IDs[0])
	if err != nil {
		return uuid.Nil, false, notFoundAs(err, "Account")
	}
	if account.IsDeleted {
		return uuid.Nil, false, errNotFound("Account")
	}
	return account.ID, true, nil
}

// accountForecast is the account's stored estimate, made now when asked to,
// when there is none, or when the stored one began in an earlier month.
func accountForecast(
	ctx context.Context, env *Env, sp auth.SpaceContext, accountID uuid.UUID, today domain.Date,
	remake bool,
) (store.CashFlowForecast, bool, error) {
	if !remake {
		row, err := env.DB.LatestAccountForecast(ctx, sp.ID(), accountID)
		switch {
		case err == nil && domain.MonthOf(row.GeneratedOn) == domain.MonthOf(today):
			return row, true, nil
		case err != nil && !isNotFound(err):
			return store.CashFlowForecast{}, false, err
		}
	}
	return service.NewAccountForecasts(env.DB).RefreshAccount(ctx, sp.ID(), accountID, today)
}

func noAccountForecast(accountID uuid.UUID) *agentifiv1.CashFlowForecast {
	return &agentifiv1.CashFlowForecast{
		Method: proto.String(store.ForecastMethodAverage), AccountId: proto.String(accountID.String()),
		Unavailable: proto.String("This account has no complete month of history yet, so there is " +
			"nothing to average. Its estimate appears after its first full month."),
	}
}

// forecastProto is a stored forecast broken into the card's windows and set
// beside the projection over the same accounts.
func forecastProto(
	ctx context.Context, env *Env, row store.CashFlowForecast,
	today domain.Date, horizons []int, accounts *agentifiv1.IdSet,
) *agentifiv1.CashFlowForecast {
	method := row.Method
	if method == "" {
		method = store.ForecastMethodModel
	}
	out := &agentifiv1.CashFlowForecast{
		Method: proto.String(method), AccountId: protoOptID(row.AccountID),
		Model: protoNonEmpty(row.Model), GeneratedAt: timestamppb.New(row.GeneratedAt),
		AgeDays: int32(forecastAgeDays(row.GeneratedOn, today)),
	}
	windows := domain.BucketCashFlowForecast(row.Forecast, today, horizons)
	// A forecast whose last month is past buckets to nothing: stale, not
	// broken, and saying so beats four windows of zeroes.
	if row.Forecast.Horizon().Before(today) {
		out.Unavailable = proto.String("The last forecast ran out on " + row.Forecast.Horizon().String() +
			". Run the cash-flow forecast automation again for a current one.")
		return out
	}

	scheduled, opening, reconcilable := scheduledBalances(ctx, env, today, windows, accounts)
	out.Available = true
	out.Narrative = protoNonEmpty(row.Forecast.Narrative)
	out.From = proto.String(today.String())
	out.Through = proto.String(row.Forecast.Horizon().String())
	for _, month := range row.Forecast.Months {
		out.Months = append(out.Months, &agentifiv1.CashFlowForecastMonth{
			Month: month.Month.String(), MoneyIn: moneyProto(month.In), MoneyOut: moneyProto(month.Out),
			Net: moneyProto(month.Net()),
		})
	}
	for _, window := range windows {
		one := &agentifiv1.CashFlowForecastWindow{
			Days: int32(window.Days), Through: window.Through.String(), MoneyIn: moneyProto(window.In),
			MoneyOut: moneyProto(window.Out), Net: moneyProto(window.Net()),
			CoveredDays: int32(window.Covered), IsComplete: window.IsComplete(),
		}
		if ending, known := scheduled[window.Through]; reconcilable && known {
			check := domain.ReconcileCashFlowForecast(opening, ending, window)
			one.EstimatedBalance = nullableMoneyProto(check.Estimated, true)
			one.ScheduledBalance = nullableMoneyProto(check.Scheduled, true)
			one.Difference = nullableMoneyProto(check.Difference, true)
		}
		out.Windows = append(out.Windows, one)
	}
	return out
}

// protoNonEmpty is an optional text field: unset for empty text.
func protoNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return proto.String(s)
}

// forecastAgeDays is how old the forecast is, in whole days.
func forecastAgeDays(generatedOn, today domain.Date) int {
	days := domain.DaysBetween(generatedOn, today)
	if days < 0 {
		return 0
	}
	return days
}

// parseForecastHorizons reads `horizons=30,90`, defaulting to the windows the
// card offers.
func parseForecastHorizons(raw string) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return forecastHorizons, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxForecastWindows {
		return nil, errBadRequest("at most %d horizons at once", maxForecastWindows)
	}
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		days, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || days < 1 || days > maxHorizonDays {
			return nil, errBadRequest("horizons is a list of day counts between 1 and %d, "+
				"such as \"30,90\"", maxHorizonDays)
		}
		out = append(out, days)
	}
	return out, nil
}

// scheduledBalances reads the deterministic projection the card is drawing
// from GetCashFlow, the procedure the chart calls, so the comparison uses the
// chart's own figures. It answers each window's last-day balance, the opening
// balance, and false rather than an error on any trouble, since a forecast is
// still worth showing without the comparison.
func scheduledBalances(
	ctx context.Context, env *Env, from domain.Date,
	windows []domain.CashFlowForecastWindow, accounts *agentifiv1.IdSet,
) (map[domain.Date]domain.Money, domain.Money, bool) {
	last := from
	for _, window := range windows {
		if window.Through.After(last) {
			last = window.Through
		}
	}
	answer, err := cashFlowService{env}.GetCashFlow(ctx, &agentifiv1.GetCashFlowRequest{
		From: from.String(), To: last.String(), AccountId: accounts,
	})
	if err != nil || len(answer.GetCombined()) == 0 {
		return nil, domain.Zero, false
	}
	byDay := make(map[domain.Date]domain.Money, len(answer.GetCombined()))
	var opening domain.Money
	for i, point := range answer.GetCombined() {
		on, err := parseDate(point.GetOn())
		if err != nil {
			return nil, domain.Zero, false
		}
		balance, err := moneyFrom(point.GetBalance())
		if err != nil {
			return nil, domain.Zero, false
		}
		if i == 0 {
			opening = balance
		}
		byDay[on] = balance
	}
	return byDay, opening, true
}
