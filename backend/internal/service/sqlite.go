package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/money"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/google/uuid"
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
	d, err := money.FromMinor(n, m.currency)
	if err != nil {
		return err
	}
	m.amount = domain.FromDecimal(d)
	m.valid = true
	return nil
}
func (m minorMoney) Value() (driver.Value, error) {
	if !m.valid {
		return nil, nil
	}
	return money.ToMinor(m.amount.Decimal(), m.currency)
}
func sqlMoney(m domain.Money, currency string) minorMoney {
	return minorMoney{amount: m, valid: true, currency: currency}
}
func sqlNullMoney(m domain.Money, valid bool, currency string) minorMoney {
	return minorMoney{amount: m, valid: valid, currency: currency}
}

type uuidJSON []uuid.UUID

func (a *uuidJSON) Scan(v any) error {
	var raw []byte
	switch v := v.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		return fmt.Errorf("service: expected JSON array, got %T", v)
	}
	if len(raw) == 0 || raw[0] != '[' {
		return fmt.Errorf("service: expected JSON array")
	}
	return json.Unmarshal(raw, a)
}
func (a uuidJSON) Value() (driver.Value, error) {
	if a == nil {
		return "[]", nil
	}
	raw, err := json.Marshal(a)
	return string(raw), err
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

func optionalDate(d domain.Date) any {
	if d.IsZero() {
		return nil
	}
	return d.String()
}

func sqlIn[T any](values []T) (string, []any) {
	if len(values) == 0 {
		return "(NULL)", nil
	}
	args := make([]any, len(values))
	marks := make([]byte, 0, len(values)*2+1)
	marks = append(marks, '(')
	for i, value := range values {
		if i > 0 {
			marks = append(marks, ',')
		}
		marks = append(marks, '?')
		args[i] = value
	}
	marks = append(marks, ')')
	return string(marks), args
}

func optionalDecimal(value string, valid bool) any {
	if !valid {
		return nil
	}
	return value
}
