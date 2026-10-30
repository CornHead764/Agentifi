package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

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

const billPaymentColumns = `id, subaccount_id, external_id, paid_on, amount, method,
	transaction_id, fetched_at`

func scanBillPayment(row scanner, spaceID SpaceID) (BillPayment, error) {
	var one BillPayment
	var paidOn string
	var amount, fetched int64
	var txn sql.Null[string]
	if err := row.Scan(&one.ID, &one.SubaccountID, &one.ExternalID, &paidOn, &amount,
		&one.Method, &txn, &fetched); err != nil {
		return one, err
	}
	one.SpaceID, one.FetchedAt = spaceID, fromUnixMillis(fetched)
	var err error
	one.PaidOn, err = domain.ParseDate(paidOn)
	if err != nil {
		return one, err
	}
	if txn.Valid {
		one.TransactionID, err = uuid.Parse(txn.V)
		if err != nil {
			return one, err
		}
	}
	value, err := fromMinor(amount, "USD")
	one.Amount = value
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	err = h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		var released []uuid.UUID
		for _, one := range payments {
			before, err := sqliteQueryAll(ctx, db.Read(), "store: read bill payment",
				func(row scanner) (BillPayment, error) { return scanBillPayment(row, spaceID) },
				`SELECT `+billPaymentColumns+` FROM bill_payments WHERE subaccount_id = ? AND external_id = ?`,
				subaccountID, one.ExternalID)
			if err != nil {
				return err
			}
			if len(before) == 0 {
				added++
			} else if was := before[0]; was.TransactionID != uuid.Nil &&
				(was.PaidOn != one.PaidOn || !was.Amount.Equal(one.Amount)) {
				released = append(released, was.TransactionID)
			}
			amount, err := toMinor(one.Amount, "USD")
			if err != nil {
				return wrap("store: bill payment amount", err)
			}
			now := db.Now()
			if _, err := db.Write().ExecContext(ctx, `
				INSERT INTO bill_payments
				    (id, subaccount_id, external_id, paid_on, amount, method, fetched_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (subaccount_id, external_id) DO UPDATE
				   SET paid_on = EXCLUDED.paid_on, amount = EXCLUDED.amount, method = EXCLUDED.method,
				       fetched_at = EXCLUDED.fetched_at,
				       transaction_id = CASE
				           WHEN bill_payments.paid_on = EXCLUDED.paid_on AND bill_payments.amount = EXCLUDED.amount
				           THEN bill_payments.transaction_id END,
				       updated_at = excluded.updated_at`,
				uuid.New(), subaccountID, one.ExternalID, one.PaidOn.String(),
				amount, one.Method, unixMillis(one.FetchedAt), now, now); err != nil {
				return wrap("store: upsert bill payment", err)
			}
		}
		_, _, err = tx.ReconcileReceipts(ctx, spaceID, released)
		return err
	})
	return added, err
}

// ListBillPayments is a billed account's payments, newest first.
func (s *Store) ListBillPayments(ctx context.Context, spaceID SpaceID, subaccountID uuid.UUID) ([]BillPayment, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list bill payments",
		func(row scanner) (BillPayment, error) { return scanBillPayment(row, spaceID) },
		`SELECT `+billPaymentColumns+` FROM bill_payments WHERE subaccount_id = ?
		ORDER BY paid_on DESC, external_id`, subaccountID)
}

// ListPairedBillPayments is the payments on the billed accounts that a bank
// row still carrying money was paired with, oldest first.
func (s *Store) ListPairedBillPayments(
	ctx context.Context, spaceID SpaceID, subaccountIDs []uuid.UUID,
) ([]BillPayment, error) {
	if len(subaccountIDs) == 0 {
		return nil, nil
	}
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	idsSQL, args := InArgs(subaccountIDs)
	return sqliteQueryAll(ctx, db.Read(), "store: list paired bill payments",
		func(row scanner) (BillPayment, error) { return scanBillPayment(row, spaceID) },
		`SELECT `+prefixed("p", billPaymentColumns)+` FROM bill_payments p
		JOIN transactions t ON t.id = p.transaction_id
		WHERE p.subaccount_id IN `+idsSQL+` AND `+MoneyMovedOn("t")+`
		ORDER BY p.paid_on, p.external_id`, args...)
}

// MatchBillPayments pairs the space's unpaired bill payments with bank rows
// (domain.MatchBillPayments) and files the statements they settled on those
// rows. A pairing whose row was deleted or turned into a forecast is released
// first. It answers how many pairs were made.
func (s *Store) MatchBillPayments(ctx context.Context, spaceID SpaceID) (int, error) {
	made := 0
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	err = h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		released, err := sqliteQueryAll(ctx, db.Read(), "store: release bill payments", scanValue[uuid.UUID],
			`SELECT p.transaction_id FROM bill_payments p JOIN transactions t ON t.id = p.transaction_id
			 WHERE NOT (`+MoneyMovedOn("t")+`)`)
		if err != nil {
			return err
		}
		if _, err = db.Write().ExecContext(ctx, `UPDATE bill_payments SET transaction_id = NULL, updated_at = ?
			WHERE transaction_id IN (SELECT t.id FROM transactions t WHERE NOT (`+MoneyMovedOn("t")+`))`,
			db.Now()); err != nil {
			return wrap("store: release bill payments", err)
		}
		if err != nil {
			return err
		}

		unpaired, err := sqliteQueryAll(ctx, db.Read(), "store: list unpaired bill payments",
			func(row scanner) (BillPayment, error) { return scanBillPayment(row, spaceID) },
			`SELECT `+billPaymentColumns+` FROM bill_payments WHERE transaction_id IS NULL`)
		if err != nil || len(unpaired) == 0 {
			if err == nil {
				_, _, err = tx.ReconcileReceipts(ctx, spaceID, released)
			}
			return err
		}
		from, to := unpairedSpan(unpaired)
		amounts := make([]int64, 0, len(unpaired))
		facts := make([]domain.BillPaymentFacts, 0, len(unpaired))
		for _, one := range unpaired {
			amount, err := toMinor(one.Amount.Neg(), "USD")
			if err != nil {
				return err
			}
			amounts = append(amounts, amount)
			facts = append(facts, domain.BillPaymentFacts{Ref: one.ID.String(), PaidOn: one.PaidOn, Amount: one.Amount})
		}

		amountSQL, amountArgs := InArgs(amounts)
		args := append([]any{from.String(), to.String()}, amountArgs...)
		rows, err := sqliteQueryAll(ctx, db.Read(), "store: list bill payment rows", func(row scanner) (domain.BillPaymentRowFacts, error) {
			var id uuid.UUID
			var on, currency string
			var amount int64
			if err := row.Scan(&id, &on, &amount, &currency); err != nil {
				return domain.BillPaymentRowFacts{}, err
			}
			date, err := domain.ParseDate(on)
			if err != nil {
				return domain.BillPaymentRowFacts{}, err
			}
			value, err := fromMinor(amount, currency)
			return domain.BillPaymentRowFacts{Ref: id.String(), On: date, Amount: value}, err
		}, `SELECT t.id, t.date, t.amount, t.currency FROM transactions t
			WHERE `+MoneyMovedOn("t")+` AND NOT t.is_pending AND t.transfer_pair_id IS NULL
			  AND t.currency = 'USD' AND t.date BETWEEN ? AND ? AND t.amount IN `+amountSQL+`
			  AND NOT EXISTS (SELECT 1 FROM bill_payments p WHERE p.transaction_id = t.id)`, args...)
		if err != nil {
			return err
		}

		receipted := released
		for paymentRef, rowRef := range domain.MatchBillPayments(facts, rows) {
			paymentID, rowID := uuid.MustParse(paymentRef), uuid.MustParse(rowRef)
			if _, err := db.Write().ExecContext(ctx, `
				UPDATE bill_payments SET transaction_id = ?, updated_at = ? WHERE id = ?`,
				rowID, db.Now(), paymentID); err != nil {
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	held, err := sqliteQueryAll(ctx, db.Read(), "store: payment of row",
		func(row scanner) (BillPayment, error) { return scanBillPayment(row, spaceID) },
		`SELECT `+prefixed("p", billPaymentColumns)+` FROM bill_payments p
		JOIN transactions t ON t.id = p.transaction_id
		WHERE p.transaction_id = ? AND `+MoneyMovedOn("t"), txnID)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	idsSQL, args := InArgs(txnIDs)
	held, err := sqliteQueryAll(ctx, db.Read(), "store: list paid rows", scanValue[uuid.UUID],
		`SELECT transaction_id FROM bill_payments WHERE transaction_id IN `+idsSQL, args...)
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
