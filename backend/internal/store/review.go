package store

import (
	"context"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// RecentReviewedPerName bounds how many reviewed rows RecentReviewed reads per
// statement name. It is wider than domain.UsualAmountHistory because the
// domain rule also drops rows of the other sign or another currency.
const RecentReviewedPerName = 24

// RecentReviewed is the newest reviewed rows whose statement name is one of
// names, compared trimmed and lower-cased, at most RecentReviewedPerName per
// name. It is the history domain.AmountUsualness reads; the rule folds the
// names again, so SQLite's ASCII-only lower() only has to over-select.
func (s *Store) RecentReviewed(ctx context.Context, spaceID SpaceID, names []string) ([]domain.Transaction, error) {
	if len(names) == 0 {
		return nil, nil
	}
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	marks, args := InArgs(keys)
	args = append(args, RecentReviewedPerName)
	rows, err := db.Read().QueryContext(ctx, `
		SELECT id, date, amount, currency, statement_name FROM (
			SELECT id, date, amount, currency, statement_name,
				row_number() OVER (PARTITION BY lower(trim(statement_name)) ORDER BY date DESC, id DESC) AS n
			FROM transactions
			WHERE is_reviewed AND `+MoneyMoved+` AND lower(trim(statement_name)) IN `+marks+`
		) WHERE n <= ?`, args...)
	if err != nil {
		return nil, wrap("store: recent reviewed", err)
	}
	defer rows.Close()
	var out []domain.Transaction
	for rows.Next() {
		var id, date, currency, statement string
		var amount int64
		if err := rows.Scan(&id, &date, &amount, &currency, &statement); err != nil {
			return nil, wrap("store: recent reviewed", err)
		}
		on, err := domain.ParseDate(date)
		if err != nil {
			return nil, err
		}
		value, err := fromMinor(amount, currency)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.Transaction{
			ID: domain.ID(id), Date: on, Amount: domain.FromDecimal(value),
			Currency: currency, StatementName: statement, IsReviewed: true,
		})
	}
	return out, wrap("store: recent reviewed", rows.Err())
}
