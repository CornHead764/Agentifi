package store

import (
	"context"
	"time"

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
	err = s.InTx(ctx, func(tx *Store) error {
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
			tag, err := tx.db.Exec(ctx, `
				INSERT INTO document_links (document_id, space_id, kind, target_id, role)
				SELECT d.id, d.space_id, $3, $4, $5 FROM documents d
				WHERE d.space_id = $1 AND d.id = $2
				ON CONFLICT (document_id, kind, target_id) DO NOTHING`,
				spaceID.UUID(), key.documentID, string(DocumentLinkReceipt), key.transactionID, role)
			if err != nil {
				return wrap("store: link receipt", err)
			}
			added += int(tag.RowsAffected())
		}
		for key := range have {
			if _, ok := want[key]; ok {
				continue
			}
			tag, err := tx.db.Exec(ctx, `
				DELETE FROM document_links
				WHERE space_id = $1 AND document_id = $2 AND kind = $3 AND target_id = $4`,
				spaceID.UUID(), key.documentID, string(DocumentLinkReceipt), key.transactionID)
			if err != nil {
				return wrap("store: unlink receipt", err)
			}
			if tag.RowsAffected() > 0 {
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
	ids, err := queryAll(ctx, s.db, "store: list receipt candidates", scanValue[uuid.UUID], `
		SELECT t.id FROM transactions t
		JOIN series_bill_links l ON l.series_id = t.series_id AND l.space_id = t.space_id
		WHERE t.space_id = $1 AND t.series_due_on IS NOT NULL
		UNION
		SELECT transaction_id FROM merchant_matches WHERE space_id = $1
		UNION
		SELECT transaction_id FROM bill_payments WHERE space_id = $1 AND transaction_id IS NOT NULL
		UNION
		SELECT target_id FROM document_links WHERE space_id = $1 AND kind = $2`,
		spaceID.UUID(), string(DocumentLinkReceipt))
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
	_, err := s.db.Exec(ctx, `
		DELETE FROM document_links
		WHERE space_id = $1 AND kind = $2 AND role = $3 AND target_id = $4
		  AND NOT EXISTS (SELECT 1 FROM bill_payments p WHERE p.space_id = $1 AND p.transaction_id = $4)`,
		spaceID.UUID(), string(DocumentLinkReceipt), DocumentRoleStatement, t.ID)
	return wrap("store: release row statements", err)
}

// ReconcileSeriesReceipts reconciles every row that holds a slot of the given
// series, for when a series gains or loses its bill.
func (s *Store) ReconcileSeriesReceipts(ctx context.Context, spaceID SpaceID, seriesIDs []uuid.UUID) error {
	ids, err := queryAll(ctx, s.db, "store: list series receipt rows", scanValue[uuid.UUID], `
		SELECT id FROM transactions
		WHERE space_id = $1 AND series_id = ANY($2) AND series_due_on IS NOT NULL
		UNION
		SELECT l.target_id FROM document_links l
		JOIN transactions t ON t.id = l.target_id AND t.space_id = l.space_id
		WHERE l.space_id = $1 AND l.kind = $3 AND l.role = $4 AND t.series_id = ANY($2)`,
		spaceID.UUID(), seriesIDs, string(DocumentLinkReceipt), DocumentRoleStatement)
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
	ids, err := queryAll(ctx, s.db, "store: list bill receipt rows", scanValue[uuid.UUID], `
		SELECT t.id FROM bills b
		JOIN series_bill_links l ON l.subaccount_id = b.subaccount_id AND l.space_id = b.space_id
		JOIN transactions t ON t.series_id = l.series_id AND t.space_id = l.space_id
		WHERE b.space_id = $1 AND b.id = $2
		  AND t.series_due_on BETWEEN b.due_on - 14 AND b.due_on + 14
		UNION
		SELECT p.transaction_id FROM bills b
		JOIN bill_payments p ON p.subaccount_id = b.subaccount_id AND p.space_id = b.space_id
		WHERE b.space_id = $1 AND b.id = $2 AND p.transaction_id IS NOT NULL
		UNION
		SELECT l.target_id FROM document_links l
		JOIN document_links bl ON bl.document_id = l.document_id AND bl.space_id = l.space_id
		WHERE l.space_id = $1 AND l.kind = $3 AND bl.kind = $4 AND bl.target_id = $2`,
		spaceID.UUID(), billID, string(DocumentLinkReceipt), string(DocumentLinkBill))
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
	ids, err := queryAll(ctx, s.db, "store: list order receipt rows", scanValue[uuid.UUID], `
		SELECT transaction_id FROM merchant_matches WHERE space_id = $1 AND order_id = $2
		UNION
		SELECT target_id FROM document_links
		WHERE space_id = $1 AND document_id = $3 AND kind = $4`,
		spaceID.UUID(), orderID, documentID, string(DocumentLinkReceipt))
	if err != nil {
		return err
	}
	_, _, err = s.ReconcileReceipts(ctx, spaceID, ids)
	return err
}

// wantedReceipts is what the rows' receipts should be, with each link's role.
// A row that is deleted or only forecast settles nothing and holds none.
func (s *Store) wantedReceipts(
	ctx context.Context, spaceID SpaceID, txnIDs []uuid.UUID,
) (map[receiptKey]string, error) {
	want := map[receiptKey]string{}

	slotted, err := queryAll(ctx, s.db, "store: wanted receipts", scanValue[uuid.UUID], `
		SELECT t.id FROM transactions t
		JOIN series_bill_links l ON l.series_id = t.series_id AND l.space_id = t.space_id
		WHERE t.space_id = $1 AND t.id = ANY($2) AND t.series_due_on IS NOT NULL
		  AND `+MoneyMovedOn("t"), spaceID.UUID(), txnIDs)
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

	invoices, err := queryAll(ctx, s.db, "store: wanted receipts", func(rows scanner) (receiptKey, error) {
		var key receiptKey
		return key, rows.Scan(&key.transactionID, &key.documentID)
	}, `
		SELECT DISTINCT m.transaction_id, l.document_id
		FROM merchant_matches m
		JOIN transactions t ON t.id = m.transaction_id AND t.space_id = m.space_id
		JOIN document_links l ON l.space_id = m.space_id
			AND l.kind = $3 AND l.target_id = m.order_id
		WHERE m.space_id = $1 AND m.transaction_id = ANY($2) AND m.refund_id IS NULL
		  AND `+MoneyMovedOn("t"),
		spaceID.UUID(), txnIDs, string(DocumentLinkMerchantOrder))
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
	keys, err := queryAll(ctx, s.db, "store: held receipts", func(rows scanner) (receiptKey, error) {
		var key receiptKey
		return key, rows.Scan(&key.transactionID, &key.documentID)
	}, `
		SELECT target_id, document_id FROM document_links
		WHERE space_id = $1 AND kind = $2 AND target_id = ANY($3)`,
		spaceID.UUID(), string(DocumentLinkReceipt), txnIDs)
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
	return queryAll(ctx, s.db, "store: receipts of transaction", func(rows scanner) (BehindDocument, error) {
		var (
			one      BehindDocument
			source   ReceiptSource
			billName string
			dueOn    *time.Time
			merchant string
		)
		document, err := scanDocument(scanExtra{rows: rows, extra: []any{
			&one.Role, &billName, &dueOn, &merchant, &source.OrderNumber,
		}})
		if err != nil {
			return BehindDocument{}, err
		}
		switch one.Role {
		case DocumentRoleStatement:
			source.Of, source.Name = DocumentLinkBill, billName
			if dueOn != nil {
				source.DueOn = dateOf(*dueOn)
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
		       coalesce(bill.name, ''), bill.due_on,
		       coalesce(ord.merchant, ''), coalesce(ord.order_number, '')
		FROM document_links l
		JOIN documents d ON d.id = l.document_id AND d.space_id = l.space_id
		LEFT JOIN LATERAL (
			SELECT c.label AS name, b.due_on
			FROM document_links bl
			JOIN bills b ON b.id = bl.target_id AND b.space_id = bl.space_id
			JOIN bill_subaccounts sa ON sa.id = b.subaccount_id AND sa.space_id = b.space_id
			JOIN bill_connections c ON c.id = sa.connection_id AND c.space_id = sa.space_id
			WHERE bl.document_id = l.document_id AND bl.space_id = l.space_id AND bl.kind = $4
			ORDER BY b.due_on DESC LIMIT 1
		) bill ON l.role = $6
		LEFT JOIN LATERAL (
			SELECT o.merchant, o.order_number
			FROM document_links ol
			JOIN merchant_orders o ON o.id = ol.target_id AND o.space_id = ol.space_id
			WHERE ol.document_id = l.document_id AND ol.space_id = l.space_id AND ol.kind = $5
			ORDER BY o.ordered_on DESC LIMIT 1
		) ord ON l.role = $7
		WHERE l.space_id = $1 AND l.kind = $2 AND l.target_id = $3
		ORDER BY l.created_at, d.id`,
		spaceID.UUID(), string(DocumentLinkReceipt), txnID,
		string(DocumentLinkBill), string(DocumentLinkMerchantOrder),
		DocumentRoleStatement, DocumentRoleInvoice)
}

// scanExtra feeds scanDocument a row with extra trailing columns.
type scanExtra struct {
	rows  scanner
	extra []any
}

func (s scanExtra) Scan(dest ...any) error {
	return s.rows.Scan(append(dest, s.extra...)...)
}
