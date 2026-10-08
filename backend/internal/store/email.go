package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The watched mailbox and every message the reader has looked at. The one
// secret (a Graph refresh token or an IMAP app password) is sealed under
// emailConnectionContext(space, id); EmailConnection has no field for the
// ciphertext, so nothing serialized from here can carry it.

type EmailConnection struct {
	ID      uuid.UUID
	SpaceID SpaceID
	// Label is which mailbox this is, in the household's words; unique per space.
	Label string
	// Kind is graph or imap.
	Kind string
	// Address is the mailbox, which is also the path Graph reads.
	Address string
	// ClientID and Tenant are the graph app registration's; not secrets.
	ClientID string
	Tenant   string
	// Host, Port and Username are for imap.
	Host     string
	Port     int
	Username string
	// HasSecret is all a listing says about the secret.
	HasSecret bool
	Folder    string
	// Cursor is where the reader stands, in the provider's own terms; nil
	// starts from the lookback.
	Cursor        json.RawMessage
	Enabled       bool
	LastPolledAt  *time.Time
	LastPollError string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const (
	EmailKindGraph = "graph"
	EmailKindIMAP  = "imap"
)

type BillEmail struct {
	ID           uuid.UUID
	SpaceID      SpaceID
	ConnectionID uuid.UUID
	// MessageID is the internet message-id, also the bill's external id.
	MessageID  string
	ReceivedAt time.Time
	Sender     string
	Subject    string
	// Biller is which parser claimed the message, empty for none.
	Biller  domain.BillerID
	Outcome string
	// Note is one sentence for the log.
	Note       string
	BillID     uuid.UUID
	DocumentID uuid.UUID
	// RuleID is the household rule that claimed the message; TransactionID
	// is the row it posted (the expense, where it posted two).
	RuleID        uuid.UUID
	TransactionID uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const (
	EmailOutcomeBill = "bill"
	EmailOutcomeOTP  = "otp"
	// EmailOutcomeUnrecognised: no parser claimed it, which is most mail.
	EmailOutcomeUnrecognised = "unrecognised"
	// EmailOutcomeProposed: a known bill whose figure could not be read, filed
	// for a person.
	EmailOutcomeProposed = "proposed"
	EmailOutcomeRule     = "rule"
	// EmailOutcomeFailed: the reader could not finish. Not retried; the note
	// says why.
	EmailOutcomeFailed = "failed"
)

// --- Connections -------------------------------------------------------------

const emailConnectionColumns = `id, space_id, label, kind, address, client_id, tenant,
	host, port, username, (secret IS NOT NULL), folder, cursor, enabled,
	last_polled_at, last_poll_error, created_at, updated_at`

func scanEmailConnection(row scanner) (EmailConnection, error) {
	var (
		one     EmailConnection
		spaceID uuid.UUID
		port    *int
	)
	err := row.Scan(&one.ID, &spaceID, &one.Label, &one.Kind, &one.Address, &one.ClientID,
		&one.Tenant, &one.Host, &port, &one.Username, &one.HasSecret, &one.Folder,
		&one.Cursor, &one.Enabled, &one.LastPolledAt, &one.LastPollError,
		&one.CreatedAt, &one.UpdatedAt)
	if err != nil {
		return one, err
	}
	one.SpaceID = SpaceIDOf(spaceID)
	one.Port = Deref(port)
	return one, nil
}

func (s *Store) CreateEmailConnection(ctx context.Context, spaceID SpaceID, one *EmailConnection) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	err := s.db.QueryRow(ctx,
		`INSERT INTO email_connections
		     (id, space_id, label, kind, address, client_id, tenant, host, port,
		      username, folder, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING created_at, updated_at`,
		one.ID, spaceID.UUID(), one.Label, one.Kind, one.Address, one.ClientID, one.Tenant,
		one.Host, PtrIf(one.Port, one.Port != 0), one.Username, one.Folder, one.Enabled).
		Scan(&one.CreatedAt, &one.UpdatedAt)
	return wrap("store: create email connection", err)
}

// UpdateEmailConnection writes the fields a person owns. Secret, cursor and
// poll stamp have their own queries, so a stale settings save cannot clear a
// sealed token.
func (s *Store) UpdateEmailConnection(ctx context.Context, spaceID SpaceID, one *EmailConnection) error {
	return s.execOne(ctx, "store: update email connection",
		`UPDATE email_connections
		    SET label = $3, address = $4, client_id = $5, tenant = $6, host = $7,
		        port = $8, username = $9, folder = $10, enabled = $11, updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), one.ID, one.Label, one.Address, one.ClientID, one.Tenant,
		one.Host, PtrIf(one.Port, one.Port != 0), one.Username, one.Folder, one.Enabled)
}

func (s *Store) GetEmailConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) (EmailConnection, error) {
	one, err := scanEmailConnection(s.db.QueryRow(ctx,
		`SELECT `+emailConnectionColumns+` FROM email_connections WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id))
	return one, wrap("store: get email connection", err)
}

func (s *Store) ListEmailConnections(ctx context.Context, spaceID SpaceID) ([]EmailConnection, error) {
	return queryAll(ctx, s.db, "store: list email connections", scanEmailConnection,
		`SELECT `+emailConnectionColumns+` FROM email_connections
		  WHERE space_id = $1 ORDER BY label COLLATE NOCASE`, spaceID.UUID())
}

func (s *Store) DeleteEmailConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: delete email connection",
		`DELETE FROM email_connections WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
}

// ListEmailConnectionsDue is every enabled mailbox, in every space, with a
// secret and not polled since the given moment.
func (s *Store) ListEmailConnectionsDue(ctx context.Context, since time.Time) ([]EmailConnection, error) {
	return queryAll(ctx, s.db, "store: email connections due", scanEmailConnection,
		`SELECT `+emailConnectionColumns+` FROM email_connections
		  WHERE enabled AND secret IS NOT NULL
		    AND (last_polled_at IS NULL OR last_polled_at < $1)
		  ORDER BY created_at`, since)
}

func (s *Store) SaveEmailSecret(ctx context.Context, spaceID SpaceID, id uuid.UUID, secret string) error {
	sealed, err := s.sealString(emailConnectionContext(spaceID, id), secret)
	if err != nil {
		return err
	}
	return s.execOne(ctx, "store: save email secret",
		`UPDATE email_connections
		    SET secret = $3, last_poll_error = '', updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, sealed)
}

// EmailSecret opens the sealed secret; ErrNotFound when there is none.
func (s *Store) EmailSecret(ctx context.Context, spaceID SpaceID, id uuid.UUID) (string, error) {
	return s.readSealed(ctx, "store: email secret", "email_connections", "secret",
		emailConnectionContext(spaceID, id), spaceID, id)
}

// ClearEmailSecret disconnects the mailbox; the connection and its log stay.
func (s *Store) ClearEmailSecret(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: clear email secret",
		`UPDATE email_connections SET secret = NULL, updated_at = now()
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
}

// SaveEmailCursor writes where the reader has got to. A nil cursor restarts
// the folder, as a changed host, address or folder requires.
func (s *Store) SaveEmailCursor(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, cursor json.RawMessage,
) error {
	var arg any
	if len(cursor) > 0 {
		arg = string(cursor)
	}
	_, err := s.db.Exec(ctx,
		`UPDATE email_connections SET cursor = $3, updated_at = now()
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id, arg)
	return wrap("store: save email cursor", err)
}

// EmailPollStreak is a mailbox's run of failed polls: how many, and when the
// first happened. Zero and nil once a poll finishes.
type EmailPollStreak struct {
	Failures     int
	FailingSince *time.Time
}

// MarkEmailPoll records that a poll ran at `at` and its error ("" for
// success), and returns the failing streak it leaves.
func (s *Store) MarkEmailPoll(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, errText string, at time.Time,
) (EmailPollStreak, error) {
	var streak EmailPollStreak
	err := s.db.QueryRow(ctx,
		`UPDATE email_connections
		    SET last_polled_at = now(), last_poll_error = $3,
		        poll_failures = CASE WHEN $3 = '' THEN 0 ELSE poll_failures + 1 END,
		        poll_failing_since = CASE WHEN $3 = '' THEN NULL
		                                  ELSE coalesce(poll_failing_since, $4) END,
		        updated_at = now()
		  WHERE space_id = $1 AND id = $2
		  RETURNING poll_failures, poll_failing_since`,
		spaceID.UUID(), id, errText, at).Scan(&streak.Failures, &streak.FailingSince)
	return streak, wrap("store: mark email poll", err)
}

// --- Messages ----------------------------------------------------------------

const billEmailColumns = `id, space_id, connection_id, message_id, received_at, sender,
	subject, biller, outcome, note, bill_id, document_id, rule_id, transaction_id,
	created_at, updated_at`

func scanBillEmail(row scanner) (BillEmail, error) {
	var (
		one         BillEmail
		spaceID     uuid.UUID
		bill        *uuid.UUID
		document    *uuid.UUID
		rule        *uuid.UUID
		transaction *uuid.UUID
	)
	err := row.Scan(&one.ID, &spaceID, &one.ConnectionID, &one.MessageID, &one.ReceivedAt,
		&one.Sender, &one.Subject, &one.Biller, &one.Outcome, &one.Note, &bill, &document,
		&rule, &transaction, &one.CreatedAt, &one.UpdatedAt)
	if err != nil {
		return one, err
	}
	one.SpaceID = SpaceIDOf(spaceID)
	for _, pair := range []struct {
		from *uuid.UUID
		to   *uuid.UUID
	}{
		{bill, &one.BillID}, {document, &one.DocumentID},
		{rule, &one.RuleID}, {transaction, &one.TransactionID},
	} {
		if pair.from != nil {
			*pair.to = *pair.from
		}
	}
	return one, nil
}

// RecordBillEmail writes what the reader made of one message and reports
// whether it was new. Keyed on connection and message-id, so an overlapping
// poll updates its earlier row; fields this reading left empty keep what an
// earlier one produced.
func (s *Store) RecordBillEmail(ctx context.Context, spaceID SpaceID, one *BillEmail) (bool, error) {
	return s.upsertBillEmail(ctx, spaceID, one, `
		    SET outcome = EXCLUDED.outcome, biller = EXCLUDED.biller, note = EXCLUDED.note,
		        bill_id = COALESCE(EXCLUDED.bill_id, bill_emails.bill_id),
		        document_id = COALESCE(EXCLUDED.document_id, bill_emails.document_id),
		        rule_id = COALESCE(EXCLUDED.rule_id, bill_emails.rule_id),
		        transaction_id = COALESCE(EXCLUDED.transaction_id, bill_emails.transaction_id),
		        updated_at = now()`)
}

// ReplaceBillEmail writes a re-read of a logged message, overwriting rather
// than keeping earlier fields: the row must describe one reading.
func (s *Store) ReplaceBillEmail(ctx context.Context, spaceID SpaceID, one *BillEmail) error {
	_, err := s.upsertBillEmail(ctx, spaceID, one, `
		    SET outcome = EXCLUDED.outcome, biller = EXCLUDED.biller, note = EXCLUDED.note,
		        bill_id = EXCLUDED.bill_id, document_id = EXCLUDED.document_id,
		        rule_id = EXCLUDED.rule_id, transaction_id = EXCLUDED.transaction_id,
		        updated_at = now()`)
	return err
}

func (s *Store) upsertBillEmail(
	ctx context.Context, spaceID SpaceID, one *BillEmail, onConflict string,
) (bool, error) {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	var isNew bool
	// An update keeps the stored row's id, so only an insert returns $1.
	stored, err := scanBillEmail(withTail{s.db.QueryRow(ctx,
		`INSERT INTO bill_emails
		     (id, space_id, connection_id, message_id, received_at, sender, subject,
		      biller, outcome, note, bill_id, document_id, rule_id, transaction_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 ON CONFLICT (connection_id, message_id) DO UPDATE`+onConflict+`
		 RETURNING `+billEmailColumns+`, (id = $1)`,
		one.ID, spaceID.UUID(), one.ConnectionID, one.MessageID, one.ReceivedAt, one.Sender,
		one.Subject, string(one.Biller), one.Outcome, one.Note, dbconv.NullUUID(one.BillID),
		dbconv.NullUUID(one.DocumentID), dbconv.NullUUID(one.RuleID),
		dbconv.NullUUID(one.TransactionID)), []any{&isNew}})
	if err != nil {
		return false, wrap("store: record bill email", err)
	}
	*one = stored
	return isNew, nil
}

// withTail scans a row's own columns and then the extra ones the query added,
// so the upsert's "was new" flag needs no second column list.
type withTail struct {
	row   scanner
	extra []any
}

func (w withTail) Scan(dest ...any) error { return w.row.Scan(append(dest, w.extra...)...) }

func (s *Store) GetBillEmail(ctx context.Context, spaceID SpaceID, id uuid.UUID) (BillEmail, error) {
	one, err := scanBillEmail(s.db.QueryRow(ctx,
		`SELECT `+billEmailColumns+` FROM bill_emails WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id))
	return one, wrap("store: get bill email", err)
}

func (s *Store) HasBillEmail(
	ctx context.Context, spaceID SpaceID, connectionID uuid.UUID, messageID string,
) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM bill_emails
		  WHERE space_id = $1 AND connection_id = $2 AND message_id = $3)`,
		spaceID.UUID(), connectionID, messageID).Scan(&exists)
	return exists, wrap("store: has bill email", err)
}

// ListBillEmails is the message log, newest first: one mailbox's when
// connectionID is set, the space's otherwise.
func (s *Store) ListBillEmails(
	ctx context.Context, spaceID SpaceID, connectionID uuid.UUID, limit int,
) ([]BillEmail, error) {
	if limit <= 0 {
		limit = 50
	}
	return queryAll(ctx, s.db, "store: list bill emails", scanBillEmail,
		`SELECT `+billEmailColumns+` FROM bill_emails
		  WHERE space_id = $1 AND ($2 IS NULL OR connection_id = $2)
		  ORDER BY received_at DESC, created_at DESC
		  LIMIT $3`, spaceID.UUID(), dbconv.NullUUID(connectionID), limit)
}
