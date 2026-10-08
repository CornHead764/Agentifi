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
	paid := make([]domain.Money, 0, len(amounts))
	for _, amount := range amounts {
		paid = append(paid, amount.Abs().Neg())
	}
	hundredths, err := moneyArray(paid)
	if err != nil {
		return nil, err
	}
	return queryAll(ctx, s.db, "store: bill payment candidates", scanBillPaymentCandidate,
		`SELECT id, account_id, category_id, series_id, date, amount, statement_name
		   FROM transactions
		  WHERE space_id = $1 AND `+MoneyMoved+`
		    AND date BETWEEN $2 AND $3 AND amount IN (SELECT value FROM json_each($4))
		  ORDER BY date, id`,
		spaceID.UUID(), from, to, hundredths)
}

// moneyArray is amounts as a money column stores them, for a json_each
// argument compared against one.
func moneyArray(amounts []domain.Money) ([]int64, error) {
	out := make([]int64, 0, len(amounts))
	for _, amount := range amounts {
		value, err := dbconv.Money(amount).Value()
		if err != nil {
			return nil, err
		}
		out = append(out, value.(int64))
	}
	return out, nil
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
