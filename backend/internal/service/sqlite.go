package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func (b base) household(ctx context.Context, id store.SpaceID) (*sqlitedb.DB, string, error) {
	currency, err := b.store.HouseholdCurrency(ctx, id)
	if err != nil {
		return nil, "", err
	}
	db, err := b.store.HouseholdDB(ctx, id)
	return db, currency, err
}

type minorMoney struct {
	amount   domain.Money
	valid    bool
	currency string
}

func (m *minorMoney) Scan(v any) error {
	if v == nil {
		m.valid = false
		return nil
	}
	n, ok := v.(int64)
	if !ok {
		return fmt.Errorf("service: expected integer money, got %T", v)
	}
	amount, err := moneyFromMinor(n, m.currency)
	if err != nil {
		return err
	}
	m.amount = amount
	m.valid = true
	return nil
}

// Value refuses an amount carrying another currency's scale, as the store's
// writes do.
func (m minorMoney) Value() (driver.Value, error) {
	if !m.valid {
		return nil, nil
	}
	scale, err := minorunits.Decimals(m.currency)
	if err != nil {
		return nil, err
	}
	if places, ok := m.amount.Places(); ok && places != scale {
		return nil, &domain.MixedScaleError{Left: places, Right: scale}
	}
	return minorunits.ToMinor(m.amount.Decimal(), m.currency)
}

// moneyFromMinor gives the amount its currency's scale.
func moneyFromMinor(n int64, currency string) (domain.Money, error) {
	scale, err := minorunits.Decimals(currency)
	if err != nil {
		return domain.Zero, err
	}
	return domain.FromMinor(n, scale), nil
}
func sqlMoney(m domain.Money, currency string) minorMoney {
	return minorMoney{amount: m, valid: true, currency: currency}
}
func sqlNullMoney(m domain.Money, valid bool, currency string) minorMoney {
	return minorMoney{amount: m, valid: valid, currency: currency}
}

func nullDate(v sql.Null[string]) domain.Date {
	if !v.Valid {
		return domain.Date{}
	}
	d, _ := domain.ParseDate(v.V)
	return d
}
func nullMillis(v sql.Null[int64]) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.V).UTC()
	return &t
}

func optionalDecimal(value string, valid bool) any {
	if !valid {
		return nil
	}
	return value
}
