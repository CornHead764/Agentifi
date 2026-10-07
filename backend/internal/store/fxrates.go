package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Stored exchange rates, one row per space, pair and day. Per space because
// re-stating what a transaction was worth must give the same answer next year,
// which a shared table another space refreshed would not. Everything is quoted
// against USD; the provider crosses other pairs.

// FxRates satisfies provider.FxRateStore.
type FxRates struct{ store *Store }

func NewFxRates(s *Store) *FxRates { return &FxRates{store: s} }

// ExactRate returns the rate stored for that exact day, if any.
func (f *FxRates) ExactRate(
	ctx context.Context, spaceID domain.ID, quote string, on domain.Date,
) (domain.Rate, bool, error) {
	space, err := spaceUUID(spaceID)
	if err != nil {
		return domain.Rate{}, false, err
	}
	var rate string
	err = f.store.db.QueryRow(ctx,
		`SELECT rate::text FROM fx_rates
		  WHERE space_id = $1 AND quote_currency = $2 AND date = $3`,
		space, quote, on.Time()).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
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
	space, err := spaceUUID(spaceID)
	if err != nil {
		return domain.Rate{}, false, err
	}
	var rate string
	err = f.store.db.QueryRow(ctx,
		`SELECT rate::text FROM fx_rates
		  WHERE space_id = $1 AND quote_currency = $2
		  ORDER BY (date <= $3) DESC,
		           CASE WHEN date <= $3 THEN $3::date - date ELSE date - $3::date END
		  LIMIT 1`,
		space, quote, on.Time()).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Rate{}, false, nil
	}
	if err != nil {
		return domain.Rate{}, false, wrap("store: closest fx rate", err)
	}
	return parseRate(rate)
}

// UpsertRates writes rates, replacing any already stored for the same space,
// pair and day.
func (f *FxRates) UpsertRates(ctx context.Context, rates []provider.FxRate) error {
	if len(rates) == 0 {
		return nil
	}
	for _, one := range rates {
		space, err := spaceUUID(one.SpaceID)
		if err != nil {
			return err
		}
		_, err = f.store.db.Exec(ctx,
			`INSERT INTO fx_rates
			     (id, space_id, base_currency, quote_currency, date, rate, source)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (space_id, base_currency, quote_currency, date) DO UPDATE
			     SET rate = EXCLUDED.rate, source = EXCLUDED.source, updated_at = now()`,
			uuid.New(), space, one.BaseCurrency, one.QuoteCurrency, one.On.Time(),
			one.Rate.String(), one.Source)
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
