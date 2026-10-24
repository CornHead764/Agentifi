package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// A receipt is a document_links row of kind DocumentLinkReceipt from a
// document to the bank row it is the paperwork for: the statement of the bill
// whose slot the row settled or that the bill payment paired with the row
// paid (role statement), or the invoice of the merchant order the row was
// matched to (role invoice). Receipts are derived, and only
// ReconcileReceipts writes or removes them; nothing a person attaches is ever
// one, since a hand-added file is linked with DocumentLinkTransaction.

// receiptKey is one receipt link.
type receiptKey struct {
	transactionID uuid.UUID
	documentID    uuid.UUID
}

// ReconcileReceipts makes the receipt links on the given rows exactly what
// their bill slots and merchant matches say they should be, adding what is
// missing and removing what no longer holds. Running it twice changes nothing
// the second time.
func (s *Store) ReconcileReceipts(
	ctx context.Context, spaceID SpaceID, txnIDs []uuid.UUID,
) (added, removed int, err error) {
	if len(txnIDs) == 0 {
		return 0, 0, nil
	}
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return 0, 0, err
	}
	err = h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		want, err := tx.wantedReceipts(ctx, spaceID, txnIDs)
		if err != nil {
			return err
		}
		have, err := tx.heldReceipts(ctx, spaceID, txnIDs)
		if err != nil {
			return err
		}
		for key, role := range want {
			if _, ok := have[key]; ok {
				continue
			}
			tag, err := db.Write().ExecContext(ctx, `
				INSERT INTO document_links (document_id, kind, target_id, role, created_at)
				SELECT d.id, ?, ?, ?, ? FROM documents d WHERE d.id = ?
				ON CONFLICT (document_id, kind, target_id) DO NOTHING`,
				string(DocumentLinkReceipt), key.transactionID, role, db.Now(), key.documentID)
			if err != nil {
				return wrap("store: link receipt", err)
			}
			n, err := tag.RowsAffected()
			if err != nil {
				return wrap("store: link receipt", err)
			}
			added += int(n)
		}
		for key := range have {
			if _, ok := want[key]; ok {
				continue
			}
			tag, err := db.Write().ExecContext(ctx, `
				DELETE FROM document_links WHERE document_id = ? AND kind = ? AND target_id = ?`,
				key.documentID, string(DocumentLinkReceipt), key.transactionID)
			if err != nil {
				return wrap("store: unlink receipt", err)
			}
			n, err := tag.RowsAffected()
			if err != nil {
				return wrap("store: unlink receipt", err)
			}
			if n > 0 {
				removed++
				if err := tx.TouchDocument(ctx, spaceID, key.documentID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return added, removed, nil
}

// ReconcileSpaceReceipts is ReconcileReceipts over every row in the space that
// could hold a receipt or already does: the backfill, and the daily pass that
// catches a write no hook saw.
func (s *Store) ReconcileSpaceReceipts(ctx context.Context, spaceID SpaceID) (added, removed int, err error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return 0, 0, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return 0, 0, err
	}
	ids, err := sqliteQueryAll(ctx, db.Read(), "store: list receipt candidates", scanValue[uuid.UUID], `
		SELECT t.id FROM transactions t JOIN series_bill_links l ON l.series_id = t.series_id
		WHERE t.series_due_on IS NOT NULL
		UNION SELECT transaction_id FROM merchant_matches
		UNION SELECT transaction_id FROM bill_payments WHERE transaction_id IS NOT NULL
		UNION SELECT target_id FROM document_links WHERE kind = ?`, string(DocumentLinkReceipt))
	if err != nil {
		return 0, 0, err
	}
	return s.ReconcileReceipts(ctx, spaceID, ids)
}

// refreshRowReceipts follows one row write. A row that holds no slot can hold
// no statement but the one its bill payment settled, which a row write does
// not change, so only a row that holds a slot, or that stopped counting as
// money moved, needs the whole reconcile.
func (s *Store) refreshRowReceipts(ctx context.Context, spaceID SpaceID, t *Transaction) error {
	if t.SeriesID != uuid.Nil || t.IsDeleted || t.EstimateStatus != "" {
		_, _, err := s.ReconcileReceipts(ctx, spaceID, []uuid.UUID{t.ID})
		return err
	}
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `
		DELETE FROM document_links WHERE kind = ? AND role = ? AND target_id = ?
		  AND NOT EXISTS (SELECT 1 FROM bill_payments p WHERE p.transaction_id = ?)`,
		string(DocumentLinkReceipt), DocumentRoleStatement, t.ID, t.ID)
	return wrap("store: release row statements", err)
}

// ReconcileSeriesReceipts reconciles every row that holds a slot of the given
// series, for when a series gains or loses its bill.
func (s *Store) ReconcileSeriesReceipts(ctx context.Context, spaceID SpaceID, seriesIDs []uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	idsSQL, args := inArgs(seriesIDs)
	args = append(args, string(DocumentLinkReceipt), DocumentRoleStatement)
	_, more := inArgs(seriesIDs)
	args = append(args, more...)
	ids, err := sqliteQueryAll(ctx, db.Read(), "store: list series receipt rows", scanValue[uuid.UUID], `
		SELECT id FROM transactions WHERE series_id IN `+idsSQL+` AND series_due_on IS NOT NULL
		UNION SELECT l.target_id FROM document_links l JOIN transactions t ON t.id = l.target_id
		WHERE l.kind = ? AND l.role = ? AND t.series_id IN `+idsSQL, args...)
	if err != nil {
		return err
	}
	_, _, err = s.ReconcileReceipts(ctx, spaceID, ids)
	return err
}

// reconcileBillReceipts reconciles the rows that could have settled one bill:
// slots of the series linked to its subaccount near its due date (a margin
// wider than any domain.MatchWindow), and the rows its subaccount's payments
// are paired with.
func (s *Store) reconcileBillReceipts(ctx context.Context, spaceID SpaceID, billID uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	ids, err := sqliteQueryAll(ctx, db.Read(), "store: list bill receipt rows", scanValue[uuid.UUID], `
		SELECT t.id FROM bills b
		JOIN series_bill_links l ON l.subaccount_id = b.subaccount_id
		JOIN transactions t ON t.series_id = l.series_id
		WHERE b.id = ? AND t.series_due_on BETWEEN date(b.due_on, '-14 days') AND date(b.due_on, '+14 days')
		UNION SELECT p.transaction_id FROM bills b JOIN bill_payments p ON p.subaccount_id = b.subaccount_id
		WHERE b.id = ? AND p.transaction_id IS NOT NULL
		UNION SELECT l.target_id FROM document_links l JOIN document_links bl ON bl.document_id = l.document_id
		WHERE l.kind = ? AND bl.kind = ? AND bl.target_id = ?`,
		billID, billID, string(DocumentLinkReceipt), string(DocumentLinkBill), billID)
	if err != nil {
		return err
	}
	_, _, err = s.ReconcileReceipts(ctx, spaceID, ids)
	return err
}

// reconcileOrderReceipts reconciles the rows matched to one order and the rows
// one of its documents is already a receipt on, for when the order gains or
// loses that document.
func (s *Store) reconcileOrderReceipts(
	ctx context.Context, spaceID SpaceID, orderID, documentID uuid.UUID,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	ids, err := sqliteQueryAll(ctx, db.Read(), "store: list order receipt rows", scanValue[uuid.UUID], `
		SELECT transaction_id FROM merchant_matches WHERE order_id = ?
		UNION SELECT target_id FROM document_links WHERE document_id = ? AND kind = ?`,
		orderID, documentID, string(DocumentLinkReceipt))
	if err != nil {
		return err
	}
	_, _, err = s.ReconcileReceipts(ctx, spaceID, ids)
	return err
}

// wantedReceipts is what the rows' receipts should be, with each link's role.
// A row that is deleted or only forecast settles nothing and holds none. An
// order's invoice goes on the rows that paid for it, never on a credit: a
// refund points at its purchase instead.
func (s *Store) wantedReceipts(
	ctx context.Context, spaceID SpaceID, txnIDs []uuid.UUID,
) (map[receiptKey]string, error) {
	want := map[receiptKey]string{}

	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	idsSQL, args := inArgs(txnIDs)
	slotted, err := sqliteQueryAll(ctx, db.Read(), "store: wanted receipts", scanValue[uuid.UUID], `
		SELECT t.id FROM transactions t JOIN series_bill_links l ON l.series_id = t.series_id
		WHERE t.id IN `+idsSQL+` AND t.series_due_on IS NOT NULL AND `+MoneyMovedOn("t"), args...)
	if err != nil {
		return nil, err
	}
	for _, txnID := range slotted {
		statements, err := s.documentsOfTransactionsBill(ctx, spaceID, txnID)
		if err != nil {
			return nil, err
		}
		for _, one := range statements {
			want[receiptKey{txnID, one.ID}] = DocumentRoleStatement
		}
	}

	invoices, err := sqliteQueryAll(ctx, db.Read(), "store: wanted receipts", func(rows scanner) (receiptKey, error) {
		var key receiptKey
		err := rows.Scan(&key.transactionID, &key.documentID)
		return key, err
	}, `SELECT DISTINCT m.transaction_id, l.document_id FROM merchant_matches m
		JOIN transactions t ON t.id = m.transaction_id
		JOIN document_links l ON l.kind = 'merchant_order' AND l.target_id = m.order_id
		WHERE m.transaction_id IN `+idsSQL+` AND m.refund_id IS NULL AND t.amount < 0
		  AND `+MoneyMovedOn("t"), args...)
	if err != nil {
		return nil, err
	}
	for _, key := range invoices {
		if _, ok := want[key]; !ok {
			want[key] = DocumentRoleInvoice
		}
	}

	paid, err := s.statementsOfPaidRows(ctx, spaceID, txnIDs)
	if err != nil {
		return nil, err
	}
	for key, role := range paid {
		if _, ok := want[key]; !ok {
			want[key] = role
		}
	}
	return want, nil
}

func (s *Store) heldReceipts(
	ctx context.Context, spaceID SpaceID, txnIDs []uuid.UUID,
) (map[receiptKey]struct{}, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	idsSQL, args := inArgs(txnIDs)
	keys, err := sqliteQueryAll(ctx, db.Read(), "store: held receipts", func(rows scanner) (receiptKey, error) {
		var key receiptKey
		err := rows.Scan(&key.transactionID, &key.documentID)
		return key, err
	}, `SELECT target_id, document_id FROM document_links
		WHERE kind = 'receipt' AND target_id IN `+idsSQL, args...)
	if err != nil {
		return nil, err
	}
	have := make(map[receiptKey]struct{}, len(keys))
	for _, key := range keys {
		have[key] = struct{}{}
	}
	return have, nil
}

// ReceiptSource says what a receipt is the paperwork of, for the panel to
// name: the bill (its connection's label and due date) or the merchant order
// (the merchant and its order number).
type ReceiptSource struct {
	Of          DocumentLinkKind
	Name        string
	DueOn       domain.Date
	OrderNumber string
}

// receiptsOfTransaction is the receipts on one row and what each came from.
func (s *Store) receiptsOfTransaction(
	ctx context.Context, spaceID SpaceID, txnID uuid.UUID,
) ([]BehindDocument, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: receipts of transaction", func(rows scanner) (BehindDocument, error) {
		var (
			one      BehindDocument
			source   ReceiptSource
			billName string
			dueOn    sql.Null[string]
			merchant string
		)
		document, err := scanDocument(scanExtra{rows: rows, extra: []any{
			&one.Role, &billName, &dueOn, &merchant, &source.OrderNumber,
		}})
		if err != nil {
			return BehindDocument{}, err
		}
		document.SpaceID = spaceID
		switch one.Role {
		case DocumentRoleStatement:
			source.Of, source.Name = DocumentLinkBill, billName
			if dueOn.Valid {
				source.DueOn, err = domain.ParseDate(dueOn.V)
				if err != nil {
					return BehindDocument{}, err
				}
			}
		case DocumentRoleInvoice:
			source.Of = DocumentLinkMerchantOrder
			source.Name = merchant
			if known, ok := domain.MerchantByID(domain.MerchantID(merchant)); ok {
				source.Name = known.Name
			}
		}
		one.Document, one.Via, one.Receipt = document, DocumentLinkReceipt, &source
		return one, nil
	}, `
		SELECT `+prefixed("d", documentColumns)+`, l.role,
		coalesce((SELECT c.label FROM document_links bl JOIN bills b ON b.id = bl.target_id
			JOIN bill_subaccounts sa ON sa.id = b.subaccount_id
			JOIN bill_connections c ON c.id = sa.connection_id
			WHERE bl.document_id = l.document_id AND bl.kind = 'bill' AND l.role = 'statement'
			ORDER BY b.due_on DESC LIMIT 1), ''),
		(SELECT b.due_on FROM document_links bl JOIN bills b ON b.id = bl.target_id
			WHERE bl.document_id = l.document_id AND bl.kind = 'bill' AND l.role = 'statement'
			ORDER BY b.due_on DESC LIMIT 1),
		coalesce((SELECT o.merchant FROM document_links ol JOIN merchant_orders o ON o.id = ol.target_id
			WHERE ol.document_id = l.document_id AND ol.kind = 'merchant_order' AND l.role = 'invoice'
			ORDER BY o.ordered_on DESC LIMIT 1), ''),
		coalesce((SELECT o.order_number FROM document_links ol JOIN merchant_orders o ON o.id = ol.target_id
			WHERE ol.document_id = l.document_id AND ol.kind = 'merchant_order' AND l.role = 'invoice'
			ORDER BY o.ordered_on DESC LIMIT 1), '')
		FROM document_links l JOIN documents d ON d.id = l.document_id
		WHERE l.kind = ? AND l.target_id = ? ORDER BY l.created_at, d.id`,
		string(DocumentLinkReceipt), txnID)
}

// scanExtra feeds scanDocument a row with extra trailing columns.
type scanExtra struct {
	rows  scanner
	extra []any
}

func (s scanExtra) Scan(dest ...any) error {
	return s.rows.Scan(append(dest, s.extra...)...)
}
