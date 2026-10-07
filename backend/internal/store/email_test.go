package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// mailboxSecret is an invented app password in the shape Google issues.
const mailboxSecret = "abcd efgh ijkl mnop"

func newEmailConnection(t *testing.T, spaceID SpaceID) *EmailConnection {
	t.Helper()
	connection := &EmailConnection{
		Label: "the bills mailbox", Kind: EmailKindIMAP,
		Address: "bills@example.invalid", Host: "imap.example.invalid", Port: 993,
		Username: "bills@example.invalid", Folder: "Inbox", Enabled: true,
	}
	require.NoError(t, sealedDB(t).CreateEmailConnection(t.Context(), spaceID, connection))
	return connection
}

func TestAMailboxSecretReachesPostgresAsCiphertext(t *testing.T) {
	space := newSpace(t)
	connection := newEmailConnection(t, space)

	require.NoError(t, sealedDB(t).SaveEmailSecret(
		t.Context(), space, connection.ID, mailboxSecret))

	var stored string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT secret FROM email_connections WHERE id = $1`, connection.ID).Scan(&stored))
	require.NotContains(t, stored, mailboxSecret)

	opened, err := sealedDB(t).EmailSecret(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, mailboxSecret, opened)

	listed, err := sealedDB(t).ListEmailConnections(t.Context(), space)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.True(t, listed[0].HasSecret)
	encoded, err := json.Marshal(listed)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), mailboxSecret)
}

func TestASecretSealedForOneMailboxWillNotOpenOnAnother(t *testing.T) {
	// The row binding stops a payload copied onto another row from opening.
	space := newSpace(t)
	mine := newEmailConnection(t, space)
	theirs := &EmailConnection{
		Label: "the other mailbox", Kind: EmailKindIMAP,
		Address: "other@example.invalid", Host: "imap.example.invalid", Port: 993,
		Username: "other@example.invalid", Folder: "Inbox", Enabled: true,
	}
	require.NoError(t, sealedDB(t).CreateEmailConnection(t.Context(), space, theirs))
	require.NoError(t, sealedDB(t).SaveEmailSecret(t.Context(), space, mine.ID, mailboxSecret))

	var sealed string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT secret FROM email_connections WHERE id = $1`, mine.ID).Scan(&sealed))
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE email_connections SET secret = $2 WHERE id = $1`, theirs.ID, sealed)
	require.NoError(t, err)

	_, err = sealedDB(t).EmailSecret(t.Context(), space, theirs.ID)
	require.ErrorIs(t, err, ErrCredentialUnreadable)
}

func TestAMailboxIsDueOnlyWithSomethingToSignInWith(t *testing.T) {
	space := newSpace(t)
	connection := newEmailConnection(t, space)
	since := time.Now().UTC().Add(-time.Hour)
	emailID := func(c EmailConnection) uuid.UUID { return c.ID }

	due, err := db(t).ListEmailConnectionsDue(t.Context(), since)
	require.NoError(t, err)
	require.False(t, containsID(due, connection.ID, emailID), "no secret, so nothing to poll with")

	require.NoError(t, sealedDB(t).SaveEmailSecret(
		t.Context(), space, connection.ID, mailboxSecret))
	due, err = db(t).ListEmailConnectionsDue(t.Context(), since)
	require.NoError(t, err)
	require.True(t, containsID(due, connection.ID, emailID), "never polled, so it is due")

	_, err = db(t).MarkEmailPoll(t.Context(), space, connection.ID, "", time.Now())
	require.NoError(t, err)
	due, err = db(t).ListEmailConnectionsDue(t.Context(), since)
	require.NoError(t, err)
	require.False(t, containsID(due, connection.ID, emailID), "polled inside the window")
}

func TestAMessageTheReaderHasSeenIsOneRow(t *testing.T) {
	space := newSpace(t)
	connection := newEmailConnection(t, space)
	const messageID = "<b71f0c2a-4ee1@mail.example.invalid>"
	received := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	one := &BillEmail{
		ConnectionID: connection.ID, MessageID: messageID, ReceivedAt: received,
		Sender: "billing@example.invalid", Subject: "Your statement is ready",
		Biller: domain.BillerRsyncNet, Outcome: EmailOutcomeBill,
	}
	isNew, err := db(t).RecordBillEmail(t.Context(), space, one)
	require.NoError(t, err)
	require.True(t, isNew)

	seen, err := db(t).HasBillEmail(t.Context(), space, connection.ID, messageID)
	require.NoError(t, err)
	require.True(t, seen)

	again := &BillEmail{
		ConnectionID: connection.ID, MessageID: messageID, ReceivedAt: received,
		Sender: "billing@example.invalid", Subject: "Your statement is ready",
		Biller: domain.BillerRsyncNet, Outcome: EmailOutcomeBill,
	}
	isNew, err = db(t).RecordBillEmail(t.Context(), space, again)
	require.NoError(t, err)
	require.False(t, isNew, "the same message is the same row")
	require.Equal(t, one.ID, again.ID)

	log, err := db(t).ListBillEmails(t.Context(), space, connection.ID, 50)
	require.NoError(t, err)
	require.Len(t, log, 1)
	require.Equal(t, domain.BillerRsyncNet, log[0].Biller)
}

func TestACursorAndAMessageLogBelongToOneSpace(t *testing.T) {
	space, stranger := newSpace(t), newSpace(t)
	connection := newEmailConnection(t, space)

	_, err := sealedDB(t).GetEmailConnection(t.Context(), stranger, connection.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = sealedDB(t).EmailSecret(t.Context(), stranger, connection.ID)
	require.ErrorIs(t, err, ErrNotFound)

	cursor := json.RawMessage(`{"uidvalidity":412,"last_uid":1809}`)
	require.NoError(t, db(t).SaveEmailCursor(t.Context(), space, connection.ID, cursor))
	stored, err := db(t).GetEmailConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(cursor), string(stored.Cursor))

	// A folder change resets the cursor: UIDs are numbered per folder.
	require.NoError(t, db(t).SaveEmailCursor(t.Context(), space, connection.ID, nil))
	stored, err = db(t).GetEmailConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Empty(t, stored.Cursor)
}
