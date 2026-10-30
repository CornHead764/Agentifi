package store

import (
	"context"
	"fmt"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

type rateSnapshot map[string]map[string]domain.Rate

// SnapshotFxRates materializes the catalogue before a household transaction.
func (s *Store) SnapshotFxRates(ctx context.Context) (provider.FxRateStore, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	rows, err := db.Read().QueryContext(ctx, `SELECT quote_currency, date, rate FROM fx_rates WHERE base_currency = 'USD'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := rateSnapshot{}
	for rows.Next() {
		var quote, date, text string
		if err := rows.Scan(&quote, &date, &text); err != nil {
			return nil, err
		}
		rate, _, err := parseRate(text)
		if err != nil {
			return nil, err
		}
		if out[quote] == nil {
			out[quote] = map[string]domain.Rate{}
		}
		out[quote][date] = rate
	}
	return out, rows.Err()
}
func (s rateSnapshot) ExactRate(_ context.Context, _ domain.ID, quote string, on domain.Date) (domain.Rate, bool, error) {
	rate, ok := s[quote][on.String()]
	return rate, ok, nil
}
func (s rateSnapshot) ClosestRate(_ context.Context, _ domain.ID, quote string, on domain.Date) (domain.Rate, bool, error) {
	target := on.String()
	before, after := "", ""
	for date := range s[quote] {
		if date <= target && (before == "" || date > before) {
			before = date
		}
		if date > target && (after == "" || date < after) {
			after = date
		}
	}
	if before != "" {
		return s[quote][before], true, nil
	}
	if after != "" {
		return s[quote][after], true, nil
	}
	return domain.Rate{}, false, nil
}
func (s rateSnapshot) UpsertRates(context.Context, []provider.FxRate) error {
	return fmt.Errorf("store: rate snapshot is read-only")
}
