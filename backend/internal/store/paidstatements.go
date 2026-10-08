package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// BillPayment is a payment a provider lists for one billed account, and the
// bank row domain.MatchBillPayments paired it with. The statement it settled
// (domain.StatementsPaidBy) is that row's receipt.
type BillPayment struct {
	ID           uuid.UUID
	SpaceID      SpaceID
	SubaccountID uuid.UUID
	// ExternalID is the provider's own key, which a re-pull matches on.
	ExternalID string
	PaidOn     domain.Date
	// Amount is the magnitude the provider received.
	Amount domain.Money
	// Method is how it was paid, in the provider's words ("Visa ending 0000").
	Method        string
	TransactionID uuid.UUID
	FetchedAt     time.Time
}

const billPaymentColumns = `id, space_id, subaccount_id, external_id, paid_on, amount, method,
	transaction_id, fetched_at`

func scanBillPayment(row scanner) (BillPayment, error) {
	var (
		one     BillPayment
		spaceID uuid.UUID
		paidOn  time.Time
		amount  dbconv.Number
		txn     *uuid.UUID
	)
	if err := row.Scan(&one.ID, &spaceID, &one.SubaccountID, &one.ExternalID, &paidOn, &amount,
		&one.Method, &txn, &one.FetchedAt); err != nil {
		return one, err
	}
	one.SpaceID, one.PaidOn, one.TransactionID = SpaceIDOf(spaceID), dateOf(paidOn), Deref(txn)
	var err error
	one.Amount, err = dbconv.ReadMoney(amount, "bill_payments.amount")
	return one, err
}

// UpsertBillPayments writes what one pull read of a billed account's
// payments, keyed on the provider's id. A payment whose day or amount changed
// loses its bank row, since the pairing was made on those, and the receipts
// of the row it held are reconciled. It answers how many were new.
func (s *Store) UpsertBillPayments(
	ctx context.Context, spaceID SpaceID, subaccountID uuid.UUID, payments []BillPayment,
) (int, error) {
	added := 0
	err := s.InTx(ctx, func(tx *Store) error {
		var released []uuid.UUID
		for _, one := range payments {
			before, err := queryAll(ctx, tx.db, "store: read bill payment", scanBillPayment, `
				SELECT `+billPaymentColumns+` FROM bill_payments
				WHERE space_id = $1 AND subaccount_id = $2 AND external_id = $3`, spaceID.UUID(), subaccountID, one.ExternalID)
			if err != nil {
				return err
			}
			if len(before) == 0 {
				added++
			} else if was := before[0]; was.TransactionID != uuid.Nil &&
				(was.PaidOn != one.PaidOn || !was.Amount.Equal(one.Amount)) {
				released = append(released, was.TransactionID)
			}
			if _, err := tx.db.Exec(ctx, `
				INSERT INTO bill_payments
				    (id, space_id, subaccount_id, external_id, paid_on, amount, method, fetched_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				ON CONFLICT (subaccount_id, external_id) DO UPDATE
				   SET paid_on = EXCLUDED.paid_on, amount = EXCLUDED.amount, method = EXCLUDED.method,
				       fetched_at = EXCLUDED.fetched_at,
				       transaction_id = CASE
				           WHEN bill_payments.paid_on = EXCLUDED.paid_on AND bill_payments.amount = EXCLUDED.amount
				           THEN bill_payments.transaction_id END,
				       updated_at = now()`,
				uuid.New(), spaceID.UUID(), subaccountID, one.ExternalID, one.PaidOn,
				dbconv.Money(one.Amount), one.Method, one.FetchedAt); err != nil {
				return wrap("store: upsert bill payment", err)
			}
		}
		_, _, err := tx.ReconcileReceipts(ctx, spaceID, released)
		return err
	})
	return added, err
}

// ListBillPayments is a billed account's payments, newest first.
func (s *Store) ListBillPayments(ctx context.Context, spaceID SpaceID, subaccountID uuid.UUID) ([]BillPayment, error) {
	return queryAll(ctx, s.db, "store: list bill payments", scanBillPayment, `
		SELECT `+billPaymentColumns+` FROM bill_payments
		WHERE space_id = $1 AND subaccount_id = $2
		ORDER BY paid_on DESC, external_id`,
		spaceID.UUID(), subaccountID)
}

// ListPairedBillPayments is the payments on the billed accounts that a bank
// row still carrying money was paired with, oldest first.
func (s *Store) ListPairedBillPayments(
	ctx context.Context, spaceID SpaceID, subaccountIDs []uuid.UUID,
) ([]BillPayment, error) {
	if len(subaccountIDs) == 0 {
		return nil, nil
	}
	return queryAll(ctx, s.db, "store: list paired bill payments", scanBillPayment, `
		SELECT `+prefixed("p", billPaymentColumns)+` FROM bill_payments p
		JOIN transactions t ON t.id = p.transaction_id AND t.space_id = p.space_id
		WHERE p.space_id = $1 AND p.subaccount_id IN (SELECT value FROM json_each($2)) AND `+MoneyMovedOn("t")+`
		ORDER BY p.paid_on, p.external_id`,
		spaceID.UUID(), subaccountIDs)
}

// MatchBillPayments pairs the space's unpaired bill payments with bank rows
// (domain.MatchBillPayments) and files the statements they settled on those
// rows. A pairing whose row was deleted or turned into a forecast is released
// first. It answers how many pairs were made.
func (s *Store) MatchBillPayments(ctx context.Context, spaceID SpaceID) (int, error) {
	made := 0
	err := s.InTx(ctx, func(tx *Store) error {
		released, err := queryAll(ctx, tx.db, "store: release bill payments", scanValue[uuid.UUID], `
			SELECT t.id FROM bill_payments p
			  JOIN transactions t ON t.id = p.transaction_id AND t.space_id = p.space_id
			 WHERE p.space_id = $1 AND NOT (`+MoneyMovedOn("t")+`)`, spaceID.UUID())
		if err != nil {
			return err
		}
		if _, err := tx.db.Exec(ctx, `
			UPDATE bill_payments SET transaction_id = NULL, updated_at = now()
			 WHERE space_id = $1 AND transaction_id IN (SELECT value FROM json_each($2))`,
			spaceID.UUID(), released); err != nil {
			return wrap("store: release bill payments", err)
		}

		unpaired, err := queryAll(ctx, tx.db, "store: list unpaired bill payments", scanBillPayment, `
			SELECT `+billPaymentColumns+` FROM bill_payments
			WHERE space_id = $1 AND transaction_id IS NULL`, spaceID.UUID())
		if err != nil || len(unpaired) == 0 {
			if err == nil {
				_, _, err = tx.ReconcileReceipts(ctx, spaceID, released)
			}
			return err
		}
		from, to := unpairedSpan(unpaired)
		paid := make([]domain.Money, 0, len(unpaired))
		facts := make([]domain.BillPaymentFacts, 0, len(unpaired))
		for _, one := range unpaired {
			paid = append(paid, one.Amount.Neg())
			facts = append(facts, domain.BillPaymentFacts{Ref: one.ID.String(), PaidOn: one.PaidOn, Amount: one.Amount})
		}

		amounts, err := moneyArray(paid)
		if err != nil {
			return err
		}
		rows, err := queryAll(ctx, tx.db, "store: list bill payment rows", func(row scanner) (domain.BillPaymentRowFacts, error) {
			var (
				id     uuid.UUID
				on     time.Time
				amount dbconv.Number
			)
			if err := row.Scan(&id, &on, &amount); err != nil {
				return domain.BillPaymentRowFacts{}, err
			}
			money, err := dbconv.ReadMoney(amount, "transactions.amount")
			return domain.BillPaymentRowFacts{Ref: id.String(), On: dateOf(on), Amount: money}, err
		}, `
			SELECT t.id, t.date, t.amount FROM transactions t
			WHERE t.space_id = $1 AND `+MoneyMovedOn("t")+` AND NOT t.is_pending
			  AND t.transfer_pair_id IS NULL
			  AND t.date BETWEEN $2 AND $3 AND t.amount IN (SELECT value FROM json_each($4))
			  AND NOT EXISTS (SELECT 1 FROM bill_payments p WHERE p.transaction_id = t.id)`,
			spaceID.UUID(), from, to, amounts)
		if err != nil {
			return err
		}

		receipted := released
		for paymentRef, rowRef := range domain.MatchBillPayments(facts, rows) {
			paymentID, rowID := uuid.MustParse(paymentRef), uuid.MustParse(rowRef)
			if _, err := tx.db.Exec(ctx, `
				UPDATE bill_payments SET transaction_id = $3, updated_at = now()
				WHERE space_id = $1 AND id = $2`, spaceID.UUID(), paymentID, rowID); err != nil {
				return wrap("store: pair bill payment", err)
			}
			made++
			receipted = append(receipted, rowID)
		}
		_, _, err = tx.ReconcileReceipts(ctx, spaceID, receipted)
		return err
	})
	return made, err
}

// unpairedSpan is the bank dates any of the payments could post on.
func unpairedSpan(payments []BillPayment) (from, to domain.Date) {
	for i, one := range payments {
		early, late := domain.BillPaymentSpan(one.PaidOn)
		if i == 0 || early.Before(from) {
			from = early
		}
		if i == 0 || late.After(to) {
			to = late
		}
	}
	return from, to
}

// billsPaidBy is what the bill payment paired with the row settled
// (domain.StatementsPaidBy over its billed account's bills), and the billed
// account, or false when no payment holds the row or it is a deleted row or a
// forecast.
func (s *Store) billsPaidBy(ctx context.Context, spaceID SpaceID, txnID uuid.UUID) ([]Bill, uuid.UUID, bool, error) {
	held, err := queryAll(ctx, s.db, "store: payment of row", scanBillPayment, `
		SELECT `+prefixed("p", billPaymentColumns)+` FROM bill_payments p
		JOIN transactions t ON t.id = p.transaction_id AND t.space_id = p.space_id
		WHERE p.space_id = $1 AND p.transaction_id = $2 AND `+MoneyMovedOn("t"),
		spaceID.UUID(), txnID)
	if err != nil || len(held) == 0 {
		return nil, uuid.Nil, false, err
	}
	payment := held[0]
	bills, err := s.ListBills(ctx, spaceID, payment.SubaccountID)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	issues := make([]domain.BillIssueFacts, 0, len(bills))
	for _, bill := range bills {
		issues = append(issues, domain.BillIssueFacts{ID: domain.ID(bill.ID.String()), IssuedOn: bill.IssuedOn})
	}
	var out []Bill
	for _, id := range domain.StatementsPaidBy(payment.PaidOn, issues) {
		for _, bill := range bills {
			if domain.ID(bill.ID.String()) == id {
				out = append(out, bill)
			}
		}
	}
	return out, payment.SubaccountID, true, nil
}

// statementsOfPaidRows is the receipt each row a bill payment holds should
// carry: the statement of every bill that payment settled.
func (s *Store) statementsOfPaidRows(
	ctx context.Context, spaceID SpaceID, txnIDs []uuid.UUID,
) (map[receiptKey]string, error) {
	held, err := queryAll(ctx, s.db, "store: list paid rows", scanValue[uuid.UUID], `
		SELECT transaction_id FROM bill_payments WHERE space_id = $1 AND transaction_id IN (SELECT value FROM json_each($2))`,
		spaceID.UUID(), txnIDs)
	if err != nil {
		return nil, err
	}
	want := map[receiptKey]string{}
	for _, txnID := range held {
		bills, _, _, err := s.billsPaidBy(ctx, spaceID, txnID)
		if err != nil {
			return nil, err
		}
		for _, bill := range bills {
			if bill.DocumentID != uuid.Nil {
				want[receiptKey{txnID, bill.DocumentID}] = DocumentRoleStatement
			}
		}
	}
	return want, nil
}
