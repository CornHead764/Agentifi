package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

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

// forecastMonthRow is one month in the JSON column: money as strings, the
// month as "YYYY-MM" so a database reader can tell which it is.
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO cash_flow_forecasts
		     (id, automation_id, automation_run_id, account_id, method, model,
		      generated_on, periods, narrative, raw_answer, generated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		one.ID, pgconvDocumentUUID(one.AutomationID), pgconvDocumentUUID(one.RunID),
		pgconvDocumentUUID(one.AccountID), one.Method, one.Model, one.GeneratedOn.String(), string(periods),
		one.Forecast.Narrative, one.RawAnswer, now)
	if err == nil {
		one.GeneratedAt = fromUnixMillis(now)
	}
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if _, err := db.Write().ExecContext(ctx,
			`DELETE FROM cash_flow_forecasts WHERE account_id = ? AND method = ?`,
			one.AccountID, ForecastMethodAverage); err != nil {
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
	return s.latestForecast(ctx, spaceID, "store: latest cash flow forecast", `account_id IS NULL`)
}

// LatestAccountForecast is the newest estimate for one account, or ErrNotFound
// when none has been made.
func (s *Store) LatestAccountForecast(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID,
) (CashFlowForecast, error) {
	return s.latestForecast(ctx, spaceID, "store: latest account forecast", `account_id = ?`, accountID)
}

func (s *Store) latestForecast(
	ctx context.Context, spaceID SpaceID, op, where string, args ...any,
) (CashFlowForecast, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return CashFlowForecast{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return CashFlowForecast{}, err
	}
	row := db.Read().QueryRowContext(ctx,
		`SELECT id, automation_id, automation_run_id, account_id, method, model, generated_at,
		        generated_on, periods, narrative, raw_answer
		   FROM cash_flow_forecasts
		  WHERE `+where+` ORDER BY generated_at DESC, id DESC LIMIT 1`, args...)

	var (
		one                            CashFlowForecast
		automationID, runID, accountID sql.Null[string]
		generatedOn                    string
		generatedAt                    int64
		periods                        []byte
	)
	err = row.Scan(&one.ID, &automationID, &runID, &accountID, &one.Method, &one.Model,
		&generatedAt, &generatedOn, &periods, &one.Forecast.Narrative, &one.RawAnswer)
	if err != nil {
		return CashFlowForecast{}, wrap(op, err)
	}
	for _, pair := range []struct {
		raw  sql.Null[string]
		dest *uuid.UUID
	}{
		{automationID, &one.AutomationID}, {runID, &one.RunID}, {accountID, &one.AccountID},
	} {
		if pair.raw.Valid {
			*pair.dest, err = uuid.Parse(pair.raw.V)
			if err != nil {
				return CashFlowForecast{}, wrap(op, err)
			}
		}
	}
	one.GeneratedAt = fromUnixMillis(generatedAt)
	one.GeneratedOn, err = domain.ParseDate(generatedOn)
	if err != nil {
		return CashFlowForecast{}, wrap(op, err)
	}
	months, err := decodeForecastMonths(periods)
	if err != nil {
		return CashFlowForecast{}, wrap(op, err)
	}
	one.Forecast.Months = months
	return one, nil
}
