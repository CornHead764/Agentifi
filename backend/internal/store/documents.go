package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Document is a file the application holds. The row is metadata only: the
// bytes live in the deployment's provider.StorageProvider under StorageKey,
// so deleting a row and deleting its blob are separate acts. A document is
// reached through document_links (one file can belong to a bill and to the
// bank row that paid it) and deduplicated per space by ContentSHA256.
//
// A link's target is polymorphic and has no foreign key, so the kind is a
// closed set validated here; the role is a free label.
type Document struct {
	ID      uuid.UUID
	SpaceID SpaceID
	// ContentSHA256 is the hex digest of the bytes and the dedupe key within a
	// space, or a placeholder — see UnhashedDocumentHash.
	ContentSHA256 string
	ContentType   string
	SizeBytes     int
	Filename      string
	StorageKey    string
	Source        string
	SourceRef     string
	// UploadedByUserID is uuid.Nil for a pulled statement, and after the
	// uploader is deleted (ON DELETE SET NULL: the file outlives the person).
	UploadedByUserID uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// DocumentLinkKind is what kind of thing a link points at. Closed set.
type DocumentLinkKind string

const (
	DocumentLinkBill          DocumentLinkKind = "bill"
	DocumentLinkTransaction   DocumentLinkKind = "transaction"
	DocumentLinkMerchantOrder DocumentLinkKind = "merchant_order"
	// DocumentLinkReceipt points at a bank row, like DocumentLinkTransaction,
	// but is derived: only ReconcileReceipts writes it.
	DocumentLinkReceipt DocumentLinkKind = "receipt"
)

func (k DocumentLinkKind) Valid() bool {
	switch k {
	case DocumentLinkBill, DocumentLinkTransaction, DocumentLinkMerchantOrder, DocumentLinkReceipt:
		return true
	}
	return false
}

// The roles a link can carry. Not a closed set.
const (
	DocumentRoleAttachment = "attachment"
	DocumentRoleStatement  = "statement"
	DocumentRoleInvoice    = "invoice"
	DocumentRoleReceipt    = "receipt"
)

const (
	DocumentSourceUpload       = "upload"
	DocumentSourceBillPull     = "bill_pull"
	DocumentSourceMerchantPull = "merchant_pull"
	DocumentSourceEmail        = "email"
	DocumentSourceReceiptScan  = "receipt_scan"
)

type DocumentLink struct {
	DocumentID uuid.UUID
	SpaceID    SpaceID
	Kind       DocumentLinkKind
	TargetID   uuid.UUID
	Role       string
	CreatedAt  time.Time
}

// unhashedPrefix marks a document whose bytes could not be hashed. The
// placeholder contains a hyphen, so it never equals a hex digest and never
// matches a dedupe lookup.
const unhashedPrefix = "legacy-unhashed-"

// UnhashedDocumentHash is the placeholder hash for a document whose bytes
// cannot be hashed, derived from its id so it is unique and stable. The
// importer uses it for Simplifi attachments the export named but did not
// carry.
func UnhashedDocumentHash(id uuid.UUID) string {
	hash := unhashedPrefix + strings.ReplaceAll(id.String(), "-", "")
	return hash + strings.Repeat("-", 64-len(hash))
}

const documentColumns = `id, space_id, content_sha256, content_type, size_bytes, filename,
	storage_key, source, source_ref, uploaded_by_user_id, created_at, updated_at`

func (s *Store) CreateDocument(ctx context.Context, spaceID SpaceID, d *Document) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	d.SpaceID = spaceID
	err := s.db.QueryRow(ctx, `
		INSERT INTO documents (id, space_id, content_sha256, content_type, size_bytes,
			filename, storage_key, source, source_ref, uploaded_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING created_at, updated_at`,
		d.ID, spaceID.UUID(), d.ContentSHA256, d.ContentType, d.SizeBytes,
		d.Filename, d.StorageKey, d.Source, d.SourceRef, dbconv.NullUUID(d.UploadedByUserID),
	).Scan(&d.CreatedAt, &d.UpdatedAt)
	return wrap("store: create document", err)
}

func (s *Store) GetDocument(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Document, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+documentColumns+` FROM documents WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	d, err := scanDocument(row)
	return d, wrap("store: get document", err)
}

// GetDocumentByContentHash is the dedupe lookup. A placeholder hash is refused
// rather than looked up: it says nothing about the bytes.
func (s *Store) GetDocumentByContentHash(
	ctx context.Context, spaceID SpaceID, sha256Hex string,
) (Document, error) {
	if strings.HasPrefix(sha256Hex, unhashedPrefix) {
		return Document{}, fmt.Errorf("store: get document by hash: %w", ErrNotFound)
	}
	row := s.db.QueryRow(ctx,
		`SELECT `+documentColumns+` FROM documents WHERE space_id = $1 AND content_sha256 = $2`,
		spaceID.UUID(), sha256Hex)
	d, err := scanDocument(row)
	return d, wrap("store: get document by hash", err)
}

// ListDocumentStorageKeys is where every document of a space keeps its bytes,
// linked or not.
func (s *Store) ListDocumentStorageKeys(ctx context.Context, spaceID SpaceID) ([]string, error) {
	return queryAll(ctx, s.db, "store: list document storage keys", scanValue[string],
		`SELECT storage_key FROM documents WHERE space_id = $1 ORDER BY created_at, id`, spaceID.UUID())
}

// ListDocumentsByLink returns everything linked to one target, oldest link
// first. Ordered by the link, not the document: a deduplicated file keeps the
// creation time of its first upload.
func (s *Store) ListDocumentsByLink(
	ctx context.Context, spaceID SpaceID, kind DocumentLinkKind, targetID uuid.UUID,
) ([]Document, error) {
	if err := validDocumentKind(kind); err != nil {
		return nil, err
	}
	return queryAll(ctx, s.db, "store: list documents by link", scanDocument, `
		SELECT `+prefixed("d", documentColumns)+`
		FROM documents d
		JOIN document_links l ON l.document_id = d.id AND l.space_id = d.space_id
		WHERE d.space_id = $1 AND l.kind = $2 AND l.target_id = $3
		ORDER BY l.created_at, d.id`,
		spaceID.UUID(), string(kind), targetID)
}

// transactionDocumentKinds are the links that put a document on a bank row:
// the register's paperclip and the has_attachment filter field both read
// these, so the two cannot disagree.
var transactionDocumentKinds = []string{string(DocumentLinkTransaction), string(DocumentLinkReceipt)}

// CountDocumentsOnTransactions counts the documents each of a page of bank
// rows shows, attached or filed as a receipt, in one query; the register asks
// for every row it renders. A file that is both counts once.
func (s *Store) CountDocumentsOnTransactions(
	ctx context.Context, spaceID SpaceID, txnIDs []uuid.UUID,
) (map[uuid.UUID]int, error) {
	counts := map[uuid.UUID]int{}
	if len(txnIDs) == 0 {
		return counts, nil
	}
	found, err := queryAll(ctx, s.db, "store: count documents on transactions", scanPair[uuid.UUID, int], `
		SELECT target_id, count(DISTINCT document_id) FROM document_links
		WHERE space_id = $1 AND kind = ANY($2) AND target_id = ANY($3)
		GROUP BY target_id`, spaceID.UUID(), transactionDocumentKinds, txnIDs)
	if err != nil {
		return nil, err
	}
	for _, one := range found {
		counts[one.first] = one.second
	}
	return counts, nil
}

// TransactionsWithDocuments is every row in the space with at least one
// document on it, by CountDocumentsOnTransactions' definition, for the filter
// field that asks.
func (s *Store) TransactionsWithDocuments(
	ctx context.Context, spaceID SpaceID,
) (map[uuid.UUID]bool, error) {
	ids, err := queryAll(ctx, s.db, "store: transactions with documents", scanValue[uuid.UUID], `
		SELECT DISTINCT target_id FROM document_links
		WHERE space_id = $1 AND kind = ANY($2)`, spaceID.UUID(), transactionDocumentKinds)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// LinkDocument says what a document is a document of, idempotently. The
// document is re-read in the same statement, so a link cannot be written into
// a space that does not hold it — the tenancy check a polymorphic target
// cannot get from a foreign key.
func (s *Store) LinkDocument(ctx context.Context, spaceID SpaceID, link DocumentLink) error {
	if err := validDocumentKind(link.Kind); err != nil {
		return err
	}
	role := link.Role
	if role == "" {
		role = DocumentRoleAttachment
	}
	return s.InTx(ctx, func(tx *Store) error {
		if link.Kind == DocumentLinkBill && role == DocumentRoleStatement {
			if err := tx.unlinkOtherStatements(ctx, spaceID, link.TargetID, link.DocumentID); err != nil {
				return err
			}
		}
		tag, err := tx.db.Exec(ctx, `
			INSERT INTO document_links (document_id, space_id, kind, target_id, role)
			SELECT d.id, d.space_id, $3, $4, $5 FROM documents d
			WHERE d.space_id = $1 AND d.id = $2
			ON CONFLICT (document_id, kind, target_id) DO NOTHING`,
			spaceID.UUID(), link.DocumentID, string(link.Kind), link.TargetID, role)
		if err != nil {
			return wrap("store: link document", err)
		}
		if tag.RowsAffected() == 0 {
			// Either the document is not this space's (a bug) or the link already
			// exists (the idempotent path); tell them apart.
			if _, err := tx.GetDocument(ctx, spaceID, link.DocumentID); err != nil {
				return err
			}
		}
		switch link.Kind {
		case DocumentLinkMerchantOrder:
			return tx.reconcileOrderReceipts(ctx, spaceID, link.TargetID, link.DocumentID)
		case DocumentLinkBill:
			return tx.reconcileBillReceipts(ctx, spaceID, link.TargetID)
		}
		return nil
	})
}

// unlinkOtherStatements leaves a bill one statement: every statement link but
// keep's is removed, and each removed document starts its purge grace period.
func (s *Store) unlinkOtherStatements(
	ctx context.Context, spaceID SpaceID, billID, keep uuid.UUID,
) error {
	removed, err := queryAll(ctx, s.db, "store: unlink statements", scanValue[uuid.UUID], `
		DELETE FROM document_links
		WHERE space_id = $1 AND kind = $2 AND target_id = $3 AND role = $4
		  AND document_id <> $5
		RETURNING document_id`,
		spaceID.UUID(), string(DocumentLinkBill), billID, DocumentRoleStatement, keep)
	if err != nil {
		return err
	}
	for _, id := range removed {
		if err := s.TouchDocument(ctx, spaceID, id); err != nil {
			return err
		}
	}
	return nil
}

// UnlinkDocument removes one link. An orphaned document is left for the purge
// job's grace period, so a mis-click is recoverable.
func (s *Store) UnlinkDocument(
	ctx context.Context, spaceID SpaceID, documentID uuid.UUID,
	kind DocumentLinkKind, targetID uuid.UUID,
) error {
	if err := validDocumentKind(kind); err != nil {
		return err
	}
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.execOne(ctx, "store: unlink document", `
			DELETE FROM document_links
			WHERE space_id = $1 AND document_id = $2 AND kind = $3 AND target_id = $4`,
			spaceID.UUID(), documentID, string(kind), targetID); err != nil {
			return err
		}
		switch kind {
		case DocumentLinkMerchantOrder:
			return tx.reconcileOrderReceipts(ctx, spaceID, targetID, documentID)
		case DocumentLinkBill:
			return tx.reconcileBillReceipts(ctx, spaceID, targetID)
		}
		return nil
	})
}

// ListDocumentLinks returns everything one document is a document of.
func (s *Store) ListDocumentLinks(
	ctx context.Context, spaceID SpaceID, documentID uuid.UUID,
) ([]DocumentLink, error) {
	return queryAll(ctx, s.db, "store: list document links", func(rows scanner) (DocumentLink, error) {
		var l DocumentLink
		var spaceID uuid.UUID
		var kind string
		if err := rows.Scan(&l.DocumentID, &spaceID, &kind, &l.TargetID, &l.Role,
			&l.CreatedAt); err != nil {
			return DocumentLink{}, err
		}
		l.SpaceID, l.Kind = SpaceID(spaceID), DocumentLinkKind(kind)
		return l, nil
	}, `
		SELECT document_id, space_id, kind, target_id, role, created_at
		FROM document_links
		WHERE space_id = $1 AND document_id = $2
		ORDER BY created_at, kind, target_id`,
		spaceID.UUID(), documentID)
}

// DeleteDocument removes the row and, by cascade, its links. The bytes are the
// caller's to remove.
func (s *Store) DeleteDocument(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: delete document", `DELETE FROM documents WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
}

// BehindDocument is one document and how it reached the transaction: attached
// to the row by hand, or filed on it as a receipt of the bill its slot settled
// or the order it was matched to. Via lets a panel offer Remove only for what
// a person attached; Receipt says what a receipt came from, and is set on an
// attached copy of the same file too.
type BehindDocument struct {
	Document Document
	Via      DocumentLinkKind
	Role     string
	Receipt  *ReceiptSource
}

// DocumentsBehindTransaction is every document a bank row can show: what is
// attached to it, then its receipts. A file that is both is listed once, as
// the attachment.
func (s *Store) DocumentsBehindTransaction(
	ctx context.Context, spaceID SpaceID, txnID uuid.UUID,
) ([]BehindDocument, error) {
	attached, err := s.ListDocumentsByLink(ctx, spaceID, DocumentLinkTransaction, txnID)
	if err != nil {
		return nil, err
	}
	receipts, err := s.receiptsOfTransaction(ctx, spaceID, txnID)
	if err != nil {
		return nil, err
	}
	out := make([]BehindDocument, 0, len(attached)+len(receipts))
	at := map[uuid.UUID]int{}
	for _, one := range attached {
		at[one.ID] = len(out)
		out = append(out, BehindDocument{Document: one, Via: DocumentLinkTransaction, Role: DocumentRoleAttachment})
	}
	for _, one := range receipts {
		if i, ok := at[one.Document.ID]; ok {
			out[i].Receipt = one.Receipt
			continue
		}
		out = append(out, one)
	}
	return out, nil
}

// documentsOfTransactionsBill is the statement of the bill whose series slot
// this row settled, via series_bill_links. Which statement is this slot's is
// domain.BillClaimsSlot, asked with the row's slot rather than the series'
// current pointer; the query narrows to the cycles either side first.
func (s *Store) documentsOfTransactionsBill(
	ctx context.Context, spaceID SpaceID, txnID uuid.UUID,
) ([]Document, error) {
	held, ok, err := s.billSlotOf(ctx, spaceID, txnID)
	if err != nil || !ok {
		return nil, err
	}
	slot, recurrence, subaccountID := held.slot, held.recurrence, held.subaccountID
	before, after := domain.MatchWindow(recurrence)

	found, err := queryAll(ctx, s.db, "store: documents of transaction bill", func(rows scanner) (billDocument, error) {
		var one billDocument
		var dueOn time.Time
		document, err := scanDocument(scanExtra{rows: rows, extra: []any{&dueOn}})
		one.document, one.dueOn = document, dateOf(dueOn)
		return one, err
	}, `
		SELECT `+prefixed("d", documentColumns)+`, b.due_on
		FROM bills b
		JOIN document_links l ON l.space_id = b.space_id
			AND l.kind = 'bill' AND l.target_id = b.id
		JOIN documents d ON d.id = l.document_id AND d.space_id = l.space_id
		WHERE b.space_id = $1 AND b.subaccount_id = $2 AND b.due_on BETWEEN $3 AND $4
		ORDER BY b.due_on, l.created_at, d.id`,
		spaceID.UUID(), subaccountID,
		slot.AddDays(-before).Time(), slot.AddDays(after).Time())
	if err != nil {
		return nil, err
	}

	out := make([]Document, 0, len(found))
	for _, one := range found {
		if domain.BillClaimsSlot(recurrence, slot, one.dueOn) {
			out = append(out, one.document)
		}
	}
	return out, nil
}

// billDocument is a document and the due date of the bill it is the statement
// of.
type billDocument struct {
	document Document
	dueOn    domain.Date
}

// UnlinkedDocumentsOlderThan is the purge job's worklist: documents with no
// links whose last link went before the cutoff. Links are hard-deleted, so
// the unlink path touches the document's updated_at and that stands in for
// the date; a document never linked falls back to its creation time.
func (s *Store) UnlinkedDocumentsOlderThan(
	ctx context.Context, spaceID SpaceID, cutoff time.Time,
) ([]Document, error) {
	return queryAll(ctx, s.db, "store: list unlinked documents", scanDocument, `
		SELECT `+prefixed("d", documentColumns)+`
		FROM documents d
		WHERE d.space_id = $1
		  AND greatest(d.updated_at, d.created_at) < $2
		  AND NOT EXISTS (SELECT 1 FROM document_links l WHERE l.document_id = d.id)
		  -- A mailed document nothing could read a figure from has no link to
		  -- make: document_links points at a bill, a row or an order, and this
		  -- is a document *of a message*. bill_emails.document_id is its only
		  -- reference, so the purge honours that one too.
		  AND NOT EXISTS (SELECT 1 FROM bill_emails e WHERE e.document_id = d.id)
		ORDER BY d.created_at, d.id`,
		spaceID.UUID(), cutoff)
}

// TouchDocument stamps updated_at, which starts the purge clock.
func (s *Store) TouchDocument(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE documents SET updated_at = now() WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	return wrap("store: touch document", err)
}

func validDocumentKind(kind DocumentLinkKind) error {
	if !kind.Valid() {
		return fmt.Errorf("store: %q is not a document link kind", string(kind))
	}
	return nil
}

// prefixed qualifies a column list for a joined query.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = alias + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}

func scanDocument(row scanner) (Document, error) {
	var d Document
	var spaceID uuid.UUID
	var uploadedBy *uuid.UUID
	if err := row.Scan(&d.ID, &spaceID, &d.ContentSHA256, &d.ContentType, &d.SizeBytes,
		&d.Filename, &d.StorageKey, &d.Source, &d.SourceRef, &uploadedBy,
		&d.CreatedAt, &d.UpdatedAt); err != nil {
		return Document{}, err
	}
	d.SpaceID = SpaceID(spaceID)
	d.UploadedByUserID = Deref(uploadedBy)
	// char(64) pads, so a hash reads back with trailing spaces.
	d.ContentSHA256 = strings.TrimRight(d.ContentSHA256, " ")
	return d, nil
}
