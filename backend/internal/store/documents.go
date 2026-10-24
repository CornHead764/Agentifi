package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

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

const documentColumns = `id, content_sha256, content_type, size_bytes, filename,
	storage_key, source, source_ref, uploaded_by_user_id, created_at, updated_at`

func (s *Store) CreateDocument(ctx context.Context, spaceID SpaceID, d *Document) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	d.SpaceID = spaceID
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO documents (id, content_sha256, content_type, size_bytes,
			filename, storage_key, source, source_ref, uploaded_by_user_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.ContentSHA256, d.ContentType, d.SizeBytes,
		d.Filename, d.StorageKey, d.Source, d.SourceRef, pgconvDocumentUUID(d.UploadedByUserID),
		now, now)
	if err == nil {
		d.CreatedAt, d.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return wrap("store: create document", err)
}

func (s *Store) GetDocument(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Document, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return Document{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return Document{}, err
	}
	row := db.Read().QueryRowContext(ctx,
		`SELECT `+documentColumns+` FROM documents WHERE id = ?`, id)
	d, err := scanDocument(row)
	d.SpaceID = spaceID
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return Document{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return Document{}, err
	}
	row := db.Read().QueryRowContext(ctx,
		`SELECT `+documentColumns+` FROM documents WHERE content_sha256 = ?`, sha256Hex)
	d, err := scanDocument(row)
	d.SpaceID = spaceID
	return d, wrap("store: get document by hash", err)
}

// ListDocumentStorageKeys is where every document of a space keeps its bytes,
// linked or not.
func (s *Store) ListDocumentStorageKeys(ctx context.Context, spaceID SpaceID) ([]string, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list document storage keys", scanValue[string],
		`SELECT storage_key FROM documents ORDER BY created_at, id`)
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list documents by link", documentScanner(spaceID), `
		SELECT `+prefixed("d", documentColumns)+`
		FROM documents d JOIN document_links l ON l.document_id = d.id
		WHERE l.kind = ? AND l.target_id = ?
		ORDER BY l.created_at, d.id`, string(kind), targetID)
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	kinds, kindArgs := inArgs(transactionDocumentKinds)
	for batch := range slices.Chunk(txnIDs, sqliteBatchSize) {
		ids, idArgs := inArgs(batch)
		args := append(append([]any{}, kindArgs...), idArgs...)
		found, err := sqliteQueryAll(ctx, db.Read(), "store: count documents on transactions", scanPair[uuid.UUID, int], `
			SELECT target_id, count(DISTINCT document_id) FROM document_links
			WHERE kind IN `+kinds+` AND target_id IN `+ids+` GROUP BY target_id`, args...)
		if err != nil {
			return nil, err
		}
		for _, one := range found {
			counts[one.first] = one.second
		}
	}
	return counts, nil
}

// TransactionsWithDocuments is every row in the space with at least one
// document on it, by CountDocumentsOnTransactions' definition, for the filter
// field that asks.
func (s *Store) TransactionsWithDocuments(
	ctx context.Context, spaceID SpaceID,
) (map[uuid.UUID]bool, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	kinds, args := inArgs(transactionDocumentKinds)
	ids, err := sqliteQueryAll(ctx, db.Read(), "store: transactions with documents", scanValue[uuid.UUID], `
		SELECT DISTINCT target_id FROM document_links WHERE kind IN `+kinds, args...)
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if link.Kind == DocumentLinkBill && role == DocumentRoleStatement {
			if err := tx.unlinkOtherStatements(ctx, spaceID, link.TargetID, link.DocumentID); err != nil {
				return err
			}
		}
		tag, err := db.Write().ExecContext(ctx, `
			INSERT INTO document_links (document_id, kind, target_id, role, created_at)
			SELECT d.id, ?, ?, ?, ? FROM documents d WHERE d.id = ?
			ON CONFLICT (document_id, kind, target_id) DO NOTHING`,
			string(link.Kind), link.TargetID, role, db.Now(), link.DocumentID)
		if err != nil {
			return wrap("store: link document", err)
		}
		n, err := tag.RowsAffected()
		if err != nil {
			return wrap("store: link document", err)
		}
		if n == 0 {
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	removed, err := sqliteQueryAll(ctx, db.Write(), "store: unlink statements", scanValue[uuid.UUID], `
		DELETE FROM document_links WHERE kind = ? AND target_id = ? AND role = ?
		  AND document_id <> ? RETURNING document_id`,
		string(DocumentLinkBill), billID, DocumentRoleStatement, keep)
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if err := sqliteExecOne(ctx, db.Write(), "store: unlink document", `
			DELETE FROM document_links WHERE document_id = ? AND kind = ? AND target_id = ?`,
			documentID, string(kind), targetID); err != nil {
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list document links", func(rows scanner) (DocumentLink, error) {
		var l DocumentLink
		var kind string
		var created int64
		if err := rows.Scan(&l.DocumentID, &kind, &l.TargetID, &l.Role, &created); err != nil {
			return DocumentLink{}, err
		}
		l.SpaceID, l.Kind, l.CreatedAt = spaceID, DocumentLinkKind(kind), fromUnixMillis(created)
		return l, nil
	}, `SELECT document_id, kind, target_id, role, created_at FROM document_links
		WHERE document_id = ? ORDER BY created_at, kind, target_id`, documentID)
}

// DeleteDocument removes the row and, by cascade, its links. The bytes are the
// caller's to remove.
func (s *Store) DeleteDocument(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete document", `DELETE FROM documents WHERE id = ?`, id)
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

	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	found, err := sqliteQueryAll(ctx, db.Read(), "store: documents of transaction bill", func(rows scanner) (billDocument, error) {
		var one billDocument
		var dueOn string
		document, err := scanDocument(scanExtra{rows: rows, extra: []any{&dueOn}})
		if err != nil {
			return one, err
		}
		document.SpaceID = spaceID
		one.document = document
		one.dueOn, err = domain.ParseDate(dueOn)
		return one, err
	}, `SELECT `+prefixed("d", documentColumns)+`, b.due_on FROM bills b
		JOIN document_links l ON l.kind = 'bill' AND l.target_id = b.id
		JOIN documents d ON d.id = l.document_id
		WHERE b.subaccount_id = ? AND b.due_on BETWEEN ? AND ?
		ORDER BY b.due_on, l.created_at, d.id`,
		subaccountID, slot.AddDays(-before).String(), slot.AddDays(after).String())
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list unlinked documents", documentScanner(spaceID), `
		SELECT `+prefixed("d", documentColumns)+`
		FROM documents d
		WHERE max(d.updated_at, d.created_at) < ?
		  AND NOT EXISTS (SELECT 1 FROM document_links l WHERE l.document_id = d.id)
		  -- A mailed document nothing could read a figure from has no link to
		  -- make: document_links points at a bill, a row or an order, and this
		  -- is a document *of a message*. bill_emails.document_id is its only
		  -- reference, so the purge honours that one too.
		  AND NOT EXISTS (SELECT 1 FROM bill_emails e WHERE e.document_id = d.id)
		  -- Connector failure evidence is referenced only from its connection.
		  AND NOT EXISTS (SELECT 1 FROM bill_connections b
		                  WHERE b.last_pull_screenshot_document_id = d.id OR b.last_pull_trail_document_id = d.id)
		  AND NOT EXISTS (SELECT 1 FROM merchant_accounts m WHERE m.last_sync_screenshot_document_id = d.id)
		ORDER BY d.created_at, d.id`,
		unixMillis(cutoff))
}

// TouchDocument stamps updated_at, which starts the purge clock.
func (s *Store) TouchDocument(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `UPDATE documents SET updated_at = ? WHERE id = ?`,
		db.Now(), id)
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
	var uploadedBy sql.Null[string]
	var created, updated int64
	if err := row.Scan(&d.ID, &d.ContentSHA256, &d.ContentType, &d.SizeBytes,
		&d.Filename, &d.StorageKey, &d.Source, &d.SourceRef, &uploadedBy, &created, &updated); err != nil {
		return Document{}, err
	}
	if uploadedBy.Valid {
		var err error
		d.UploadedByUserID, err = uuid.Parse(uploadedBy.V)
		if err != nil {
			return Document{}, err
		}
	}
	d.CreatedAt, d.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	d.ContentSHA256 = strings.TrimRight(d.ContentSHA256, " ")
	return d, nil
}

func documentScanner(spaceID SpaceID) func(scanner) (Document, error) {
	return func(row scanner) (Document, error) {
		d, err := scanDocument(row)
		d.SpaceID = spaceID
		return d, err
	}
}

func pgconvDocumentUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
