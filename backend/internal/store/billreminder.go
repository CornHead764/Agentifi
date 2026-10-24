package store

import (
	"context"
	"database/sql"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/google/uuid"
)

// BillPaymentCandidates returns bank rows in the window whose signed amount
// matches a provider payment. Decode in the account's currency: equal integer
// minor units in two currencies need not represent equal payment amounts.
func (s *Store) BillPaymentCandidates(ctx context.Context, spaceID SpaceID, from, to domain.Date, amounts []domain.Money) ([]domain.BillPaymentCandidate, error) {
	if len(amounts) == 0 {
		return nil, nil
	}
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	candidates, err := sqliteQueryAll(ctx, db.Read(), "store: bill payment candidates", scanBillPaymentCandidate,
		`SELECT t.id, t.account_id, t.category_id, t.series_id, t.date, t.amount, t.statement_name, a.currency
 FROM transactions t JOIN accounts a ON a.id = t.account_id
 WHERE `+MoneyMovedOn("t")+` AND t.date BETWEEN ? AND ? ORDER BY t.date, t.id`, from.String(), to.String())
	if err != nil {
		return nil, err
	}
	var out []domain.BillPaymentCandidate
	for _, candidate := range candidates {
		for _, amount := range amounts {
			if candidate.Amount.Equal(amount.Abs().Neg()) {
				out = append(out, candidate)
				break
			}
		}
	}
	return out, nil
}

func scanBillPaymentCandidate(row scanner) (domain.BillPaymentCandidate, error) {
	var id, accountID uuid.UUID
	var categoryID, seriesID sql.Null[string]
	var on, currency string
	var amount int64
	var out domain.BillPaymentCandidate
	if err := row.Scan(&id, &accountID, &categoryID, &seriesID, &on, &amount, &out.StatementName, &currency); err != nil {
		return out, err
	}
	value, err := fromMinor(amount, currency)
	if err != nil {
		return out, err
	}
	out.On, err = domain.ParseDate(on)
	if err != nil {
		return out, err
	}
	out.ID, out.AccountID = domain.ID(id.String()), domain.ID(accountID.String())
	if categoryID.Valid {
		out.CategoryID = domain.ID(categoryID.V)
	}
	if seriesID.Valid {
		out.SeriesID = domain.ID(seriesID.V)
	}
	out.Amount = domain.FromDecimal(value)
	return out, nil
}
