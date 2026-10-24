package store

import (
	"context"
	"fmt"
	"math"

	"github.com/CornHead764/agentifi/backend/internal/minorunits"
)

// CurrencyRescaleError identifies values that cannot be represented exactly.
type CurrencyRescaleError struct {
	Table, Column string
	Count         int64
}

func (e *CurrencyRescaleError) Error() string {
	return fmt.Sprintf("cannot change primary currency: %s.%s has %d values that would lose digits or overflow", e.Table, e.Column, e.Count)
}

// PrimaryMoneyColumns is the complete primary-denominated INTEGER inventory.
// Account, transaction, series, security and merchant amounts have their own currencies.
var PrimaryMoneyColumns = map[string][]string{
	"alert_rules":          {"threshold_amount"},
	"balance_snapshots":    {"balance_primary"},
	"envelopes":            {"target_amount", "overwritten_target_amount", "calculated_spent_amount", "rollover_amount"},
	"filter_items":         {"amount_min", "amount_max"},
	"goals":                {"target_amount", "contribution_amount", "contributed_this_month", "saved_so_far", "spent"},
	"institutions":         {"hide_below_balance"},
	"spending_plan_months": {"calculated_rollover_amount", "calculated_income_amount", "overwritten_income_amount", "calculated_bills_amount", "overwritten_bills_amount", "calculated_subscriptions_amount", "overwritten_subscriptions_amount", "calculated_transfer_amount", "overwritten_transfer_amount", "calculated_goals_amount", "overwritten_goals_amount", "calculated_planned_spending_amount", "overwritten_planned_spending_amount", "calculated_spent_amount", "overwritten_spent_amount", "set_aside", "total_to_spend_amount", "left_to_spend_amount", "projected_other_spending", "projection_buffer"},
	"transactions":         {"amount_primary"},
	"watchlists":           {"target_amount"},
}

// RescalePrimaryCurrency must run inside the household write transaction. It
// preflights every column before any mutation and never lets SQLite promote an
// overflowing integer multiplication to REAL.
func (s *Store) RescalePrimaryCurrency(ctx context.Context, id SpaceID, next string) error {
	if !s.sqliteInTx || s.sqliteScope != id.UUID() {
		return fmt.Errorf("store: currency rescale requires household transaction")
	}
	db, err := s.household(ctx, id.UUID())
	if err != nil {
		return err
	}
	var old string
	if err := db.Read().QueryRowContext(ctx, `SELECT primary_currency FROM household_metadata WHERE id = 1`).Scan(&old); err != nil {
		return err
	}
	oldExp, err := minorunits.Decimals(old)
	if err != nil {
		return err
	}
	newExp, err := minorunits.Decimals(next)
	if err != nil {
		return err
	}
	delta := newExp - oldExp
	factor := int64(1)
	for n := delta; n > 0; n-- {
		factor *= 10
	}
	for n := delta; n < 0; n++ {
		factor *= 10
	}
	if factor != 1 {
		for table, columns := range PrimaryMoneyColumns {
			for _, col := range columns {
				predicate := col + ` % ? <> 0`
				args := []any{factor}
				if delta > 0 {
					predicate = col + ` > ? OR ` + col + ` < ?`
					args = []any{int64(math.MaxInt64) / factor, int64(math.MinInt64) / factor}
				}
				var count int64
				if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE `+predicate, args...).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					return &CurrencyRescaleError{Table: table, Column: col, Count: count}
				}
			}
		}
		op := ` / `
		if delta > 0 {
			op = ` * `
		}
		for table, columns := range PrimaryMoneyColumns {
			for _, col := range columns {
				if _, err := db.Write().ExecContext(ctx, `UPDATE `+table+` SET `+col+` = `+col+op+`? WHERE `+col+` IS NOT NULL`, factor); err != nil {
					return err
				}
			}
		}
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE household_metadata SET primary_currency = ?, updated_at = ? WHERE id = 1`, next, db.Now()); err != nil {
		return err
	}
	s.sqlitePrimaryCurrency = next
	return nil
}
