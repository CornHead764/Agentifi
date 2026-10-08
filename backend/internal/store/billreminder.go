package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// BillPaymentCandidates is every bank row dated in the window that took one
// of the amounts out: the rows that may have paid a billed account's
// statements. Amounts are magnitudes; forecasts and deleted rows paid
// nothing and are left out.
func (s *Store) BillPaymentCandidates(
	ctx context.Context, spaceID SpaceID, from, to domain.Date, amounts []domain.Money,
) ([]domain.BillPaymentCandidate, error) {
	if len(amounts) == 0 {
		return nil, nil
	}
	paid := make([]dbconv.Number, 0, len(amounts))
	for _, amount := range amounts {
		paid = append(paid, dbconv.Money(amount.Abs().Neg()))
	}
	return queryAll(ctx, s.db, "store: bill payment candidates", scanBillPaymentCandidate,
		`SELECT id, account_id, category_id, series_id, date, amount, statement_name
		   FROM transactions
		  WHERE space_id = $1 AND `+MoneyMoved+`
		    AND date BETWEEN $2 AND $3 AND amount = ANY($4::numeric[])
		  ORDER BY date, id`,
		spaceID.UUID(), from.Time(), to.Time(), paid)
}

func scanBillPaymentCandidate(row scanner) (domain.BillPaymentCandidate, error) {
	var (
		id, accountID        uuid.UUID
		categoryID, seriesID *uuid.UUID
		on                   time.Time
		amount               dbconv.Number
		out                  domain.BillPaymentCandidate
	)
	if err := row.Scan(&id, &accountID, &categoryID, &seriesID, &on, &amount, &out.StatementName); err != nil {
		return out, err
	}
	money, err := dbconv.ReadMoney(amount, "transactions.amount")
	if err != nil {
		return out, err
	}
	out.ID = domain.ID(id.String())
	out.AccountID = domain.ID(accountID.String())
	if categoryID != nil {
		out.CategoryID = domain.ID(categoryID.String())
	}
	if seriesID != nil {
		out.SeriesID = domain.ID(seriesID.String())
	}
	out.On = domain.DateOf(on)
	out.Amount = money
	return out, nil
}
