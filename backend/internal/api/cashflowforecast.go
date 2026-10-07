package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The estimated cash-flow forecast. `/cash-flow` is the arithmetic; this is a
// model's reading of the last twelve months, stored by the daily automation
// and broken into the projection card's day windows. It never passes an
// estimate off as a sum:
//
//   - It never answers instead of the projection. A space with no forecast
//     gets `available: false` and a reason, not an error.
//   - Every figure carries its provenance: which model, when, and how many of
//     the window's days it reaches.
//   - The comparison balance comes from `/cash-flow` itself through the
//     in-process call surface, so it is the figure the chart draws.
//
// Asked for exactly one account, it answers with that account's own estimate
// instead: the average of its recent months (service.AccountForecasts), made
// on first open and re-made nightly and by POST /run.

func init() {
	Register(Resource{Prefix: "/cash-flow-forecast", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", readCashFlowForecast)
		rt.Write(http.MethodPost, "/run", rerunCashFlowForecast)
	}})
}

// forecastHorizons are the windows the projection card offers. Kept in step
// with HORIZONS in frontend/src/components/transactions/ProjectedCashFlow.tsx.
var forecastHorizons = []int{30, 60, 90, 180}

// maxForecastWindows bounds a request that spells its own horizons. A card with
// more than a handful of ranges is not a card.
const maxForecastWindows = 8

type CashFlowForecastMonthResponse struct {
	Month    string       `json:"month"`
	MoneyIn  domain.Money `json:"money_in"`
	MoneyOut domain.Money `json:"money_out"`
	Net      domain.Money `json:"net"`
}

// CashFlowForecastWindowResponse is the estimate over one of the card's ranges.
type CashFlowForecastWindowResponse struct {
	Days     int          `json:"days"`
	Through  Date         `json:"through"`
	MoneyIn  domain.Money `json:"money_in"`
	MoneyOut domain.Money `json:"money_out"`
	Net      domain.Money `json:"net"`
	// CoveredDays is how many of the window's days the forecast reaches, and
	// IsComplete whether that is all of them, so an incomplete estimate is not
	// read as a wrong one.
	CoveredDays int  `json:"covered_days"`
	IsComplete  bool `json:"is_complete"`
	// EstimatedBalance and ScheduledBalance are the estimate's and the
	// arithmetic's endings for the same day. Null when the deterministic
	// projection could not be read.
	EstimatedBalance *domain.Money `json:"estimated_balance"`
	ScheduledBalance *domain.Money `json:"scheduled_balance"`
	Difference       *domain.Money `json:"difference"`
}

// CashFlowForecastResponse is the whole answer.
type CashFlowForecastResponse struct {
	// Available is false when no run has produced a forecast that parsed. The
	// card falls back to the projection and says why.
	Available bool `json:"available"`
	// Unavailable says which of the reasons it is, in a sentence a person can
	// act on. Empty when a forecast is available.
	Unavailable string `json:"unavailable,omitempty"`
	// Method is who made the estimate: "model" for the household forecast the
	// automation writes, "average" for an account's own. AccountID is the
	// account an average is for.
	Method    string     `json:"method,omitempty"`
	AccountID *uuid.UUID `json:"account_id,omitempty"`
	// Model, GeneratedAt and AgeDays are the provenance shown on the card. An
	// age of zero is sent, not omitted: "made today" is the answer a card most
	// wants.
	Model       string     `json:"model,omitempty"`
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
	AgeDays     int        `json:"age_days"`
	Narrative   string     `json:"narrative,omitempty"`
	// From is the day the windows are measured from — today, not the day the
	// forecast was made, because the card asks "the next 30 days" now.
	From    Date                             `json:"from,omitempty"`
	Through Date                             `json:"through,omitempty"`
	Months  []CashFlowForecastMonthResponse  `json:"months,omitempty"`
	Windows []CashFlowForecastWindowResponse `json:"windows,omitempty"`
}

func readCashFlowForecast(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	horizons, err := forecastHorizonsFromRequest(r)
	if err != nil {
		return err
	}
	today := domain.DateOf(env.now())
	accountID, forAccount, err := forecastAccount(r, env, sp)
	if err != nil {
		return err
	}
	if forAccount {
		row, found, err := accountForecast(r, env, sp, accountID, today, false)
		if err != nil {
			return err
		}
		if !found {
			return writeJSON(w, http.StatusOK, noAccountForecast(accountID))
		}
		return writeJSON(w, http.StatusOK, forecastResponse(env, r, sp, row, today, horizons))
	}

	row, err := env.DB.LatestCashFlowForecast(r.Context(), sp.ID())
	if err != nil {
		if isNotFound(err) {
			return writeJSON(w, http.StatusOK, CashFlowForecastResponse{
				Unavailable: "No forecast has been made yet. The estimate comes from the " +
					"\"Estimate the next six months of cash flow\" automation on the " +
					"Assistant page.",
			})
		}
		return err
	}
	return writeJSON(w, http.StatusOK, forecastResponse(env, r, sp, row, today, horizons))
}

// rerunCashFlowForecast re-makes one account's estimate now and answers with
// it, in the same shape the read does.
func rerunCashFlowForecast(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	horizons, err := forecastHorizonsFromRequest(r)
	if err != nil {
		return err
	}
	accountID, forAccount, err := forecastAccount(r, env, sp)
	if err != nil {
		return err
	}
	if !forAccount {
		return errBadRequest("re-running an estimate takes one account_id; the household " +
			"forecast is made by its automation on the Assistant page")
	}
	today := domain.DateOf(env.now())
	row, found, err := accountForecast(r, env, sp, accountID, today, true)
	if err != nil {
		return err
	}
	if !found {
		return writeJSON(w, http.StatusOK, noAccountForecast(accountID))
	}
	return writeJSON(w, http.StatusOK, forecastResponse(env, r, sp, row, today, horizons))
}

// forecastAccount is the one account a request names, when it names exactly
// one. The account has to be a live one in this space.
func forecastAccount(r *http.Request, env *Env, sp auth.SpaceContext) (uuid.UUID, bool, error) {
	ids, given, err := queryUUIDs(r, "account_id")
	if err != nil || !given || len(ids) != 1 {
		return uuid.Nil, false, err
	}
	account, err := env.DB.GetAccount(r.Context(), sp.ID(), ids[0])
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
	r *http.Request, env *Env, sp auth.SpaceContext, accountID uuid.UUID, today domain.Date,
	remake bool,
) (store.CashFlowForecast, bool, error) {
	if !remake {
		row, err := env.DB.LatestAccountForecast(r.Context(), sp.ID(), accountID)
		switch {
		case err == nil && domain.MonthOf(row.GeneratedOn) == domain.MonthOf(today):
			return row, true, nil
		case err != nil && !isNotFound(err):
			return store.CashFlowForecast{}, false, err
		}
	}
	return service.NewAccountForecasts(env.DB).RefreshAccount(r.Context(), sp.ID(), accountID, today)
}

func noAccountForecast(accountID uuid.UUID) CashFlowForecastResponse {
	return CashFlowForecastResponse{
		Method: store.ForecastMethodAverage, AccountID: &accountID,
		Unavailable: "This account has no complete month of history yet, so there is " +
			"nothing to average. Its estimate appears after its first full month.",
	}
}

// forecastResponse is a stored forecast broken into the card's windows and
// set beside the projection.
func forecastResponse(
	env *Env, r *http.Request, sp auth.SpaceContext, row store.CashFlowForecast,
	today domain.Date, horizons []int,
) CashFlowForecastResponse {
	method := row.Method
	if method == "" {
		method = store.ForecastMethodModel
	}
	var accountID *uuid.UUID
	if row.AccountID != uuid.Nil {
		accountID = &row.AccountID
	}
	windows := domain.BucketCashFlowForecast(row.Forecast, today, horizons)
	// A forecast whose last month is past buckets to nothing: stale, not
	// broken, and saying so beats four windows of zeroes.
	if row.Forecast.Horizon().Before(today) {
		return CashFlowForecastResponse{
			Unavailable: "The last forecast ran out on " + row.Forecast.Horizon().String() +
				". Run the cash-flow forecast automation again for a current one.",
			Method: method, AccountID: accountID,
			Model: row.Model, GeneratedAt: &row.GeneratedAt,
			AgeDays: forecastAgeDays(row.GeneratedOn, today),
		}
	}

	scheduled, opening, reconcilable := scheduledBalances(env, r, sp, today, windows)
	out := CashFlowForecastResponse{
		Available: true, Method: method, AccountID: accountID,
		Model: row.Model, GeneratedAt: &row.GeneratedAt,
		AgeDays: forecastAgeDays(row.GeneratedOn, today), Narrative: row.Forecast.Narrative,
		From: Date(today), Through: Date(row.Forecast.Horizon()),
	}
	for _, month := range row.Forecast.Months {
		out.Months = append(out.Months, CashFlowForecastMonthResponse{
			Month: month.Month.String(), MoneyIn: month.In, MoneyOut: month.Out,
			Net: month.Net(),
		})
	}
	for _, window := range windows {
		one := CashFlowForecastWindowResponse{
			Days: window.Days, Through: Date(window.Through), MoneyIn: window.In,
			MoneyOut: window.Out, Net: window.Net(), CoveredDays: window.Covered,
			IsComplete: window.IsComplete(),
		}
		if ending, known := scheduled[window.Through]; reconcilable && known {
			check := domain.ReconcileCashFlowForecast(opening, ending, window)
			one.EstimatedBalance = &check.Estimated
			one.ScheduledBalance = &check.Scheduled
			one.Difference = &check.Difference
		}
		out.Windows = append(out.Windows, one)
	}
	return out
}

// forecastAgeDays is how old the forecast is, in whole days.
func forecastAgeDays(generatedOn, today domain.Date) int {
	days := domain.DaysBetween(generatedOn, today)
	if days < 0 {
		return 0
	}
	return days
}

// forecastHorizonsFromRequest reads `horizons=30,90`, defaulting to the windows
// the card offers.
func forecastHorizonsFromRequest(r *http.Request) ([]int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("horizons"))
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

// scheduledBalances reads the deterministic projection the card is drawing,
// through the in-process call surface so the comparison uses the chart's own
// figures. It answers each window's last-day balance, the opening balance, and
// false rather than an error on any trouble, since a forecast is still worth
// showing without the comparison.
func scheduledBalances(
	env *Env, r *http.Request, sp auth.SpaceContext, from domain.Date,
	windows []domain.CashFlowForecastWindow,
) (map[domain.Date]domain.Money, domain.Money, bool) {
	last := from
	for _, window := range windows {
		if window.Through.After(last) {
			last = window.Through
		}
	}
	query := url.Values{"from": {from.String()}, "to": {last.String()}}
	for _, id := range r.URL.Query()["account_id"] {
		query.Add("account_id", id)
	}

	response, err := env.dispatch(r.Context(), sp, http.MethodGet, "/cash-flow", query, nil)
	// Truncated counts as unreadable: a partial projection would answer with
	// the balance on whichever day the cap fell.
	if err != nil || !response.OK() || !response.IsJSON() || response.Truncated {
		return nil, domain.Zero, false
	}
	var answer struct {
		Combined []CashFlowPointResponse `json:"combined"`
	}
	if err := json.Unmarshal([]byte(response.Body), &answer); err != nil ||
		len(answer.Combined) == 0 {
		return nil, domain.Zero, false
	}
	byDay := make(map[domain.Date]domain.Money, len(answer.Combined))
	for _, point := range answer.Combined {
		byDay[domain.Date(point.On)] = point.Balance
	}
	return byDay, answer.Combined[0].Balance, true
}
