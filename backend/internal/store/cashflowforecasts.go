package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Cash-flow forecasts: a model's for the household, and each account's
// average. Model answers are append-only (the read is the newest row), so a
// run whose answer did not parse leaves yesterday's forecast standing. An
// account's average is recomputed from the ledger and replaces the last.

// Forecast methods: who made the estimate.
const (
	ForecastMethodModel   = "model"
	ForecastMethodAverage = "average"
)

type CashFlowForecast struct {
	ID           uuid.UUID
	AutomationID uuid.UUID
	RunID        uuid.UUID
	// AccountID is the account an averaged estimate is for; Nil for the
	// household forecast.
	AccountID   uuid.UUID
	Method      string
	Model       string
	GeneratedAt time.Time
	// GeneratedOn is the day the API's windows are measured from.
	GeneratedOn domain.Date
	Forecast    domain.CashFlowForecast
	// RawAnswer is kept so "the forecast looks wrong" can be answered.
	RawAnswer string
}

// forecastMonthRow is one month in the jsonb column: money as strings, the
// month as "YYYY-MM" so a psql reader can tell which it is.
type forecastMonthRow struct {
	Month    string       `json:"month"`
	MoneyIn  domain.Money `json:"money_in"`
	MoneyOut domain.Money `json:"money_out"`
}

func encodeForecastMonths(months []domain.CashFlowMonth) ([]byte, error) {
	rows := make([]forecastMonthRow, 0, len(months))
	for _, one := range months {
		rows = append(rows, forecastMonthRow{
			Month: one.Month.String(), MoneyIn: one.In, MoneyOut: one.Out,
		})
	}
	return json.Marshal(rows)
}

func decodeForecastMonths(raw []byte) ([]domain.CashFlowMonth, error) {
	var rows []forecastMonthRow
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := make([]domain.CashFlowMonth, 0, len(rows))
	for _, one := range rows {
		month, err := domain.ParseForecastMonth(one.Month)
		if err != nil {
			return nil, fmt.Errorf("a stored forecast month is unreadable: %w", err)
		}
		out = append(out, domain.CashFlowMonth{Month: month, In: one.MoneyIn, Out: one.MoneyOut})
	}
	return out, nil
}

func (s *Store) SaveCashFlowForecast(
	ctx context.Context, spaceID SpaceID, one *CashFlowForecast,
) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	if one.Method == "" {
		one.Method = ForecastMethodModel
	}
	periods, err := encodeForecastMonths(one.Forecast.Months)
	if err != nil {
		return wrap("store: save cash flow forecast", err)
	}
	err = s.db.QueryRow(ctx,
		`INSERT INTO cash_flow_forecasts
		     (id, space_id, automation_id, automation_run_id, account_id, method, model,
		      generated_on, periods, narrative, raw_answer)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING generated_at`,
		one.ID, spaceID.UUID(), dbconv.NullUUID(one.AutomationID), dbconv.NullUUID(one.RunID),
		dbconv.NullUUID(one.AccountID), one.Method, one.Model, one.GeneratedOn.Time(), periods,
		one.Forecast.Narrative, one.RawAnswer).
		Scan(&one.GeneratedAt)
	return wrap("store: save cash flow forecast", err)
}

// ReplaceAccountForecast stores an account's averaged estimate in place of the
// last.
func (s *Store) ReplaceAccountForecast(
	ctx context.Context, spaceID SpaceID, one *CashFlowForecast,
) error {
	if one.AccountID == uuid.Nil {
		return fmt.Errorf("store: an account forecast needs its account")
	}
	one.Method = ForecastMethodAverage
	return s.InTx(ctx, func(tx *Store) error {
		if _, err := tx.db.Exec(ctx,
			`DELETE FROM cash_flow_forecasts
			  WHERE space_id = $1 AND account_id = $2 AND method = $3`,
			spaceID.UUID(), one.AccountID, ForecastMethodAverage); err != nil {
			return wrap("store: replace account forecast", err)
		}
		return tx.SaveCashFlowForecast(ctx, spaceID, one)
	})
}

// LatestCashFlowForecast is the newest household answer for a space, or
// ErrNotFound when no run has produced one yet.
func (s *Store) LatestCashFlowForecast(
	ctx context.Context, spaceID SpaceID,
) (CashFlowForecast, error) {
	return s.latestForecast(ctx, "store: latest cash flow forecast",
		`space_id = $1 AND account_id IS NULL`, spaceID.UUID())
}

// LatestAccountForecast is the newest estimate for one account, or ErrNotFound
// when none has been made.
func (s *Store) LatestAccountForecast(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID,
) (CashFlowForecast, error) {
	return s.latestForecast(ctx, "store: latest account forecast",
		`space_id = $1 AND account_id = $2`, spaceID.UUID(), accountID)
}

func (s *Store) latestForecast(
	ctx context.Context, op, where string, args ...any,
) (CashFlowForecast, error) {
	row := s.db.QueryRow(ctx,
		`SELECT id, automation_id, automation_run_id, account_id, method, model, generated_at,
		        generated_on, periods, narrative, raw_answer
		   FROM cash_flow_forecasts
		  WHERE `+where+` ORDER BY generated_at DESC, id DESC LIMIT 1`, args...)

	var (
		one                            CashFlowForecast
		automationID, runID, accountID *uuid.UUID
		generatedOn                    time.Time
		periods                        []byte
	)
	err := row.Scan(&one.ID, &automationID, &runID, &accountID, &one.Method, &one.Model,
		&one.GeneratedAt, &generatedOn, &periods, &one.Forecast.Narrative, &one.RawAnswer)
	if err != nil {
		return CashFlowForecast{}, wrap(op, err)
	}
	one.AutomationID = Deref(automationID)
	one.RunID = Deref(runID)
	one.AccountID = Deref(accountID)
	one.GeneratedOn = domain.DateOf(generatedOn)
	months, err := decodeForecastMonths(periods)
	if err != nil {
		return CashFlowForecast{}, wrap(op, err)
	}
	one.Forecast.Months = months
	return one, nil
}
