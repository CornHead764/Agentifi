package service

import (
	"context"
	"fmt"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A card charge's effective date is the due date of the bill it lands on: the
// bill feed's date where there is one, otherwise computed from the statement
// cycle. A non-card row stores the zero date, because
// domain.Transaction.ReportingDate already falls back to the transaction date.
type CreditCards struct{ base }

func NewCreditCards(st *store.Store) *CreditCards { return &CreditCards{newBase(st)} }

// PaymentDueDay is read off the bill feed's due date rather than a column of
// its own, so the two can never disagree. Zero means unknown.
func PaymentDueDay(account store.Account) int {
	if account.DueDate.IsZero() {
		return 0
	}
	return account.DueDate.Day
}

func StatementCloseDay(account store.Account) int {
	if account.StatementCloseDay == nil {
		return 0
	}
	return int(*account.StatementCloseDay)
}

// ApplyEffectiveDate stamps a row and reports whether its effective date
// changed. Without overwrite, a date already on the row is left alone, so a
// hand correction survives a re-sync.
func ApplyEffectiveDate(
	txn *store.Transaction, account store.Account, billDueDate domain.Date, overwrite bool,
) bool {
	if !overwrite && !txn.EffectiveDate.IsZero() {
		return false
	}
	derived := domain.EffectiveDateFor(txn.Date, account.Kind == domain.KindCreditCard,
		StatementCloseDay(account), PaymentDueDay(account), billDueDate)
	if derived == txn.EffectiveDate {
		return false
	}
	txn.EffectiveDate = derived
	return true
}

// Restamp re-derives and writes the effective date for every live row in one
// account, overwriting hand corrections.
func (c *CreditCards) Restamp(
	ctx context.Context,
	spaceID store.SpaceID,
	accountID uuid.UUID,
	billDueDates map[uuid.UUID]domain.Date,
) (int, error) {
	account, err := c.store.GetAccount(ctx, spaceID, accountID)
	if err != nil {
		return 0, err
	}
	rows, err := c.store.ListTransactions(ctx, spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{accountID}})
	if err != nil {
		return 0, err
	}

	var moved []*store.Transaction
	for i := range rows {
		row := &rows[i]
		if row.IsDeleted {
			continue
		}
		if ApplyEffectiveDate(row, account, billDueDates[row.ID], true) {
			moved = append(moved, row)
		}
	}
	if len(moved) == 0 {
		return 0, nil
	}

	err = c.inTx(ctx, func(tx *store.Store) error {
		for _, txn := range moved {
			// Only the one column, so an edit made since the read survives.
			_, err := tx.Conn().Exec(ctx, `
				UPDATE transactions SET effective_date = $3, updated_at = now()
				WHERE space_id = $1 AND id = $2`,
				spaceID.UUID(), txn.ID, pgconv.NullDate(txn.EffectiveDate))
			if err != nil {
				return fmt.Errorf("service: restamp effective date: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(moved), nil
}
