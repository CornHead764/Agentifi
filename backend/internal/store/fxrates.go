package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/decimaltext"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Stored exchange rates are a server-wide catalogue keyed by pair and day.
// Read the rates needed by a household transaction before beginning it.

// FxRates satisfies provider.FxRateStore.
type FxRates struct{ store *Store }

func NewFxRates(s *Store) *FxRates { return &FxRates{store: s} }

// ExactRate returns the rate stored for that exact day, if any.
func (f *FxRates) ExactRate(
	ctx context.Context, spaceID domain.ID, quote string, on domain.Date,
) (domain.Rate, bool, error) {
	if _, err := spaceUUID(spaceID); err != nil {
		return domain.Rate{}, false, err
	}
	db, err := f.store.server()
	if err != nil {
		return domain.Rate{}, false, err
	}
	var rate string
	err = db.Read().QueryRowContext(ctx,
		`SELECT rate FROM fx_rates WHERE base_currency = 'USD' AND quote_currency = ? AND date = ?`,
		quote, on.String()).Scan(&rate)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Rate{}, false, nil
	}
	if err != nil {
		return domain.Rate{}, false, wrap("store: exact fx rate", err)
	}
	return parseRate(rate)
}

// ClosestRate returns the nearest stored rate, preferring the most recent day
// at or before the target — what the money was actually worth, not a later
// day's guess.
func (f *FxRates) ClosestRate(
	ctx context.Context, spaceID domain.ID, quote string, on domain.Date,
) (domain.Rate, bool, error) {
	if _, err := spaceUUID(spaceID); err != nil {
		return domain.Rate{}, false, err
	}
	db, err := f.store.server()
	if err != nil {
		return domain.Rate{}, false, err
	}
	var rate string
	err = db.Read().QueryRowContext(ctx,
		`SELECT rate FROM fx_rates WHERE base_currency = 'USD' AND quote_currency = ?1
		  ORDER BY (date <= ?2) DESC, abs(julianday(date) - julianday(?2))
		  LIMIT 1`, quote, on.String()).Scan(&rate)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Rate{}, false, nil
	}
	if err != nil {
		return domain.Rate{}, false, wrap("store: closest fx rate", err)
	}
	return parseRate(rate)
}

// UpsertRates replaces catalogue entries for the same pair and day.
func (f *FxRates) UpsertRates(ctx context.Context, rates []provider.FxRate) error {
	if len(rates) == 0 {
		return nil
	}
	db, err := f.store.server()
	if err != nil {
		return err
	}
	for _, one := range rates {
		if _, err := spaceUUID(one.SpaceID); err != nil {
			return err
		}
		_, err := db.Write().ExecContext(ctx,
			`INSERT INTO fx_rates (id, base_currency, quote_currency, date, rate, source, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (base_currency, quote_currency, date) DO UPDATE
			     SET rate = excluded.rate, source = excluded.source, updated_at = excluded.updated_at`,
			uuid.New(), one.BaseCurrency, one.QuoteCurrency, one.On.String(), decimaltext.Format(one.Rate), one.Source, db.Now(), db.Now())
		if err != nil {
			return wrap("store: upsert fx rates", err)
		}
	}
	return nil
}

// parseRate reads a numeric(20,10) cast to text, so no float loses the tenth
// decimal place.
func parseRate(text string) (domain.Rate, bool, error) {
	rate, err := decimal.NewFromString(text)
	if err != nil {
		return domain.Rate{}, false, wrap("store: fx rate", err)
	}
	return rate, true, nil
}

func spaceUUID(id domain.ID) (uuid.UUID, error) {
	parsed, err := uuid.Parse(string(id))
	if err != nil {
		return uuid.Nil, wrap("store: fx rate space", err)
	}
	return parsed, nil
}
