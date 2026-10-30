package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

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

const emailConnectionColumns = `id, label, kind, address, client_id, tenant,
	host, port, username, (secret IS NOT NULL), folder, cursor, enabled,
	last_polled_at, last_poll_error, created_at, updated_at`

func scanEmailConnection(row scanner, spaceID SpaceID) (EmailConnection, error) {
	var one EmailConnection
	var port sql.Null[int]
	var cursor sql.Null[string]
	var polled sql.Null[int64]
	var created, updated int64
	err := row.Scan(&one.ID, &one.Label, &one.Kind, &one.Address, &one.ClientID,
		&one.Tenant, &one.Host, &port, &one.Username, &one.HasSecret, &one.Folder,
		&cursor, &one.Enabled, &polled, &one.LastPollError, &created, &updated)
	if err != nil {
		return one, err
	}
	one.SpaceID, one.Port = spaceID, port.V
	if cursor.Valid {
		one.Cursor = json.RawMessage(cursor.V)
	}
	one.LastPolledAt = nullableTime(polled)
	one.CreatedAt, one.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	return one, nil
}

func (s *Store) CreateEmailConnection(ctx context.Context, spaceID SpaceID, one *EmailConnection) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO email_connections
		     (id, label, kind, address, client_id, tenant, host, port,
		      username, folder, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		one.ID, one.Label, one.Kind, one.Address, one.ClientID, one.Tenant,
		one.Host, PtrIf(one.Port, one.Port != 0), one.Username, one.Folder, one.Enabled,
		now, now)
	if err == nil {
		one.CreatedAt, one.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return wrap("store: create email connection", err)
}

// UpdateEmailConnection writes the fields a person owns. Secret, cursor and
// poll stamp have their own queries, so a stale settings save cannot clear a
// sealed token.
func (s *Store) UpdateEmailConnection(ctx context.Context, spaceID SpaceID, one *EmailConnection) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: update email connection",
		`UPDATE email_connections SET label = ?, address = ?, client_id = ?, tenant = ?, host = ?,
		        port = ?, username = ?, folder = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		one.Label, one.Address, one.ClientID, one.Tenant, one.Host,
		PtrIf(one.Port, one.Port != 0), one.Username, one.Folder, one.Enabled, db.Now(), one.ID)
}

func (s *Store) GetEmailConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) (EmailConnection, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return EmailConnection{}, err
	}
	one, err := scanEmailConnection(db.Read().QueryRowContext(ctx,
		`SELECT `+emailConnectionColumns+` FROM email_connections WHERE id = ?`, id), spaceID)
	return one, wrap("store: get email connection", err)
}

func (s *Store) ListEmailConnections(ctx context.Context, spaceID SpaceID) ([]EmailConnection, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list email connections",
		func(row scanner) (EmailConnection, error) { return scanEmailConnection(row, spaceID) },
		`SELECT `+emailConnectionColumns+` FROM email_connections ORDER BY label`)
}

func (s *Store) DeleteEmailConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete email connection",
		`DELETE FROM email_connections WHERE id = ?`, id)
}

// ListEmailConnectionsDue is every enabled mailbox, in every space, with a
// secret and not polled since the given moment.
func (s *Store) ListEmailConnectionsDue(ctx context.Context, since time.Time) ([]EmailConnection, error) {
	ids, err := s.householdIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := []EmailConnection{}
	for _, spaceID := range ids {
		db, err := s.household(ctx, spaceID.UUID())
		if err != nil {
			return nil, err
		}
		rows, err := sqliteQueryAll(ctx, db.Read(), "store: email connections due",
			func(row scanner) (EmailConnection, error) { return scanEmailConnection(row, spaceID) },
			`SELECT `+emailConnectionColumns+` FROM email_connections
			  WHERE enabled AND secret IS NOT NULL AND (last_polled_at IS NULL OR last_polled_at < ?)`,
			unixMillis(since))
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) SaveEmailSecret(ctx context.Context, spaceID SpaceID, id uuid.UUID, secret string) error {
	sealed, err := s.sealString(emailConnectionContext(spaceID, id), secret)
	if err != nil {
		return err
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: save email secret",
		`UPDATE email_connections SET secret = ?, last_poll_error = '', updated_at = ? WHERE id = ?`,
		sealed, db.Now(), id)
}

// EmailSecret opens the sealed secret; ErrNotFound when there is none.
func (s *Store) EmailSecret(ctx context.Context, spaceID SpaceID, id uuid.UUID) (string, error) {
	cipher, err := s.requireCipher()
	if err != nil {
		return "", err
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return "", err
	}
	var secret sql.Null[string]
	if err := db.Read().QueryRowContext(ctx, `SELECT secret FROM email_connections WHERE id = ?`, id).Scan(&secret); err != nil {
		return "", wrap("store: email secret", err)
	}
	if !secret.Valid {
		return "", wrap("store: email secret", ErrNotFound)
	}
	return cipher.Open(emailConnectionContext(spaceID, id), secret.V)
}

// ClearEmailSecret disconnects the mailbox; the connection and its log stay.
func (s *Store) ClearEmailSecret(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: clear email secret",
		`UPDATE email_connections SET secret = NULL, updated_at = ? WHERE id = ?`, db.Now(), id)
}

// SaveEmailCursor writes where the reader has got to. A nil cursor restarts
// the folder, as a changed host, address or folder requires.
func (s *Store) SaveEmailCursor(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, cursor json.RawMessage,
) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`UPDATE email_connections SET cursor = ?, updated_at = ? WHERE id = ?`,
		nullableJSON(cursor), db.Now(), id)
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
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return EmailPollStreak{}, err
	}
	var streak EmailPollStreak
	var failing sql.Null[int64]
	now := db.Now()
	err = db.Write().QueryRowContext(ctx,
		`UPDATE email_connections SET last_polled_at = ?, last_poll_error = ?,
		        poll_failures = CASE WHEN ? = '' THEN 0 ELSE poll_failures + 1 END,
		        poll_failing_since = CASE WHEN ? = '' THEN NULL ELSE coalesce(poll_failing_since, ?) END,
		        updated_at = ? WHERE id = ? RETURNING poll_failures, poll_failing_since`,
		now, errText, errText, errText, unixMillis(at), now, id).Scan(&streak.Failures, &failing)
	streak.FailingSince = nullableTime(failing)
	return streak, wrap("store: mark email poll", err)
}

// --- Messages ----------------------------------------------------------------

const billEmailColumns = `id, connection_id, message_id, received_at, sender,
	subject, biller, outcome, note, bill_id, document_id, rule_id, transaction_id,
	created_at, updated_at`

func scanBillEmail(row scanner, spaceID SpaceID) (BillEmail, error) {
	var one BillEmail
	var bill, document, rule, transaction uuid.NullUUID
	var received, created, updated int64
	err := row.Scan(&one.ID, &one.ConnectionID, &one.MessageID, &received,
		&one.Sender, &one.Subject, &one.Biller, &one.Outcome, &one.Note, &bill, &document,
		&rule, &transaction, &created, &updated)
	if err != nil {
		return one, err
	}
	one.SpaceID = spaceID
	one.BillID, one.DocumentID, one.RuleID, one.TransactionID = bill.UUID, document.UUID, rule.UUID, transaction.UUID
	one.ReceivedAt, one.CreatedAt, one.UpdatedAt = fromUnixMillis(received), fromUnixMillis(created), fromUnixMillis(updated)
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
		        updated_at = EXCLUDED.updated_at`)
}

// ReplaceBillEmail writes a re-read of a logged message, overwriting rather
// than keeping earlier fields: the row must describe one reading.
func (s *Store) ReplaceBillEmail(ctx context.Context, spaceID SpaceID, one *BillEmail) error {
	_, err := s.upsertBillEmail(ctx, spaceID, one, `
		    SET outcome = EXCLUDED.outcome, biller = EXCLUDED.biller, note = EXCLUDED.note,
		        bill_id = EXCLUDED.bill_id, document_id = EXCLUDED.document_id,
		        rule_id = EXCLUDED.rule_id, transaction_id = EXCLUDED.transaction_id,
		        updated_at = EXCLUDED.updated_at`)
	return err
}

func (s *Store) upsertBillEmail(
	ctx context.Context, spaceID SpaceID, one *BillEmail, onConflict string,
) (bool, error) {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	scoped, err := s.scoped(ctx, spaceID)
	if err != nil {
		return false, err
	}
	var isNew bool
	var stored BillEmail
	err = scoped.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		var exists bool
		if err := db.Read().QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM bill_emails WHERE connection_id = ? AND message_id = ?)`,
			one.ConnectionID, one.MessageID).Scan(&exists); err != nil {
			return err
		}
		isNew = !exists
		now := db.Now()
		stored, err = scanBillEmail(db.Write().QueryRowContext(ctx,
			`INSERT INTO bill_emails
			     (id, connection_id, message_id, received_at, sender, subject,
			      biller, outcome, note, bill_id, document_id, rule_id, transaction_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (connection_id, message_id) DO UPDATE`+onConflict+`
			 RETURNING `+billEmailColumns,
			one.ID, one.ConnectionID, one.MessageID, unixMillis(one.ReceivedAt), one.Sender,
			one.Subject, string(one.Biller), one.Outcome, one.Note, NullUUID(one.BillID),
			NullUUID(one.DocumentID), NullUUID(one.RuleID),
			NullUUID(one.TransactionID), now, now), spaceID)
		return err
	})
	if err != nil {
		return false, wrap("store: record bill email", err)
	}
	*one = stored
	return isNew, nil
}

func (s *Store) GetBillEmail(ctx context.Context, spaceID SpaceID, id uuid.UUID) (BillEmail, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return BillEmail{}, err
	}
	one, err := scanBillEmail(db.Read().QueryRowContext(ctx,
		`SELECT `+billEmailColumns+` FROM bill_emails WHERE id = ?`, id), spaceID)
	return one, wrap("store: get bill email", err)
}

func (s *Store) HasBillEmail(
	ctx context.Context, spaceID SpaceID, connectionID uuid.UUID, messageID string,
) (bool, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return false, err
	}
	var exists bool
	err = db.Read().QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM bill_emails WHERE connection_id = ? AND message_id = ?)`,
		connectionID, messageID).Scan(&exists)
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
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list bill emails",
		func(row scanner) (BillEmail, error) { return scanBillEmail(row, spaceID) },
		`SELECT `+billEmailColumns+` FROM bill_emails
		  WHERE (? IS NULL OR connection_id = ?) ORDER BY received_at DESC, created_at DESC LIMIT ?`,
		NullUUID(connectionID), NullUUID(connectionID), limit)
}
