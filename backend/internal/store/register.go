package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// RegisterEntry is the part of a row needed for the unfiltered register's net.
// Splits, exclusions and refund refiling do not change a whole row's amount.
type RegisterEntry struct {
	ID        uuid.UUID
	Amount    domain.Money
	IsPadding bool
}

type RegisterQuery struct {
	TransactionQuery
	HasReviewed bool
	IsReviewed  bool
	Descending  bool
	Search      string
}

// RegisterEntries reads no response bodies or allocations. Search stays in Go:
// SQLite's case folding is not strings.ToLower's Unicode case folding.
func (s *Store) RegisterEntries(ctx context.Context, spaceID SpaceID, q RegisterQuery) ([]RegisterEntry, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return nil, err
	}
	query := &strings.Builder{}
	args := &sqliteArgList{}
	query.WriteString(`SELECT id, amount, currency, amount_primary, padded_txn_id IS NOT NULL`)
	if q.Search != "" {
		query.WriteString(`, payee, statement_name, COALESCE(notes, '')`)
	}
	query.WriteString(` FROM transactions WHERE ` + MoneyMoved)
	if len(q.AccountIDs) > 0 {
		marks, values := InArgs(q.AccountIDs)
		query.WriteString(` AND account_id IN ` + marks)
		args.values = append(args.values, values...)
	} else {
		query.WriteString(` AND account_id IN (SELECT id FROM accounts WHERE ignored_at IS NULL)`)
	}
	if q.HasReviewed {
		fmt.Fprintf(query, ` AND is_reviewed = %s`, args.add(q.IsReviewed))
	}
	dateExpr := `date`
	if q.DateMode == domain.DateEffective {
		dateExpr = `COALESCE(effective_date, date)`
	}
	if !q.From.IsZero() {
		fmt.Fprintf(query, ` AND %s >= %s`, dateExpr, args.add(q.From.String()))
	}
	if !q.To.IsZero() {
		fmt.Fprintf(query, ` AND %s <= %s`, dateExpr, args.add(q.To.String()))
	}
	order := ` ASC`
	if q.Descending {
		order = ` DESC`
	}
	query.WriteString(` ORDER BY ` + dateExpr + order + `, id` + order)
	rows, err := db.Read().QueryContext(ctx, query.String(), args.values...)
	if err != nil {
		return nil, wrap("store: register entries", err)
	}
	defer rows.Close()
	var entries []RegisterEntry
	needle := strings.ToLower(q.Search)
	for rows.Next() {
		var entry RegisterEntry
		var amount int64
		var converted sql.Null[int64]
		var currency, payee, statement, notes string
		dest := []any{&entry.ID, &amount, &currency, &converted, &entry.IsPadding}
		if needle != "" {
			dest = append(dest, &payee, &statement, &notes)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, wrap("store: register entry", err)
		}
		if needle != "" && !strings.Contains(strings.ToLower(payee), needle) &&
			!strings.Contains(strings.ToLower(statement), needle) && !strings.Contains(strings.ToLower(notes), needle) {
			continue
		}
		value, err := fromMinor(amount, currency)
		if err != nil {
			return nil, err
		}
		txn := domain.Transaction{Amount: domain.FromDecimal(value)}
		if converted.Valid {
			value, err = fromMinor(converted.V, primary)
			if err != nil {
				return nil, err
			}
			txn.AmountPrimary, txn.HasAmountPrimary = domain.FromDecimal(value), true
		}
		entry.Amount = txn.PrimaryAmount()
		entries = append(entries, entry)
	}
	return entries, wrap("store: register entries", rows.Err())
}
