package store_test

import (
	"encoding/json"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSQLiteEmail(t *testing.T) {
	st := storetest.New(t)
	cipher, err := store.NewCipher("email-test-key")
	require.NoError(t, err)
	st = st.WithCipher(cipher)
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	ctx := t.Context()
	mailbox := store.EmailConnection{Label: "Bills", Kind: store.EmailKindIMAP, Address: "bills@example.invalid", Host: "imap.example.invalid", Port: 993, Username: "bills", Folder: "Inbox", Enabled: true}
	require.NoError(t, st.CreateEmailConnection(ctx, a, &mailbox))
	other := mailbox
	other.ID = uuid.Nil
	require.NoError(t, st.CreateEmailConnection(ctx, b, &other))
	due, err := st.ListEmailConnectionsDue(ctx, time.Now())
	require.NoError(t, err)
	require.Empty(t, due)
	require.NoError(t, st.SaveEmailSecret(ctx, a, mailbox.ID, "mailbox-secret"))
	opened, err := st.EmailSecret(ctx, a, mailbox.ID)
	require.NoError(t, err)
	require.Equal(t, "mailbox-secret", opened)
	hh, err := st.HouseholdDB(ctx, a)
	require.NoError(t, err)
	var sealed string
	require.NoError(t, hh.Read().QueryRowContext(ctx, `SELECT secret FROM email_connections WHERE id = ?`, mailbox.ID).Scan(&sealed))
	require.NotContains(t, sealed, "mailbox-secret")
	due, err = st.ListEmailConnectionsDue(ctx, time.Now())
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, a, due[0].SpaceID)
	encoded, err := json.Marshal(due)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "mailbox-secret")
	require.NoError(t, st.SaveEmailCursor(ctx, a, mailbox.ID, json.RawMessage(`{"position":3}`)))
	got, err := st.GetEmailConnection(ctx, a, mailbox.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"position":3}`, string(got.Cursor))
	require.Equal(t, 993, got.Port)
	first := time.Now().UTC().Truncate(time.Millisecond)
	streak, err := st.MarkEmailPoll(ctx, a, mailbox.ID, "failed", first)
	require.NoError(t, err)
	require.Equal(t, 1, streak.Failures)
	require.Equal(t, first, *streak.FailingSince)
	streak, err = st.MarkEmailPoll(ctx, a, mailbox.ID, "failed again", first.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 2, streak.Failures)
	require.Equal(t, first, *streak.FailingSince)
	streak, err = st.MarkEmailPoll(ctx, a, mailbox.ID, "", first)
	require.NoError(t, err)
	require.Zero(t, streak.Failures)
	require.Nil(t, streak.FailingSince)
	msg := store.BillEmail{ConnectionID: mailbox.ID, MessageID: "<mail-1>", ReceivedAt: first, Sender: "sender", Subject: "bill", Outcome: store.EmailOutcomeUnrecognised}
	fresh, err := st.RecordBillEmail(ctx, a, &msg)
	require.NoError(t, err)
	require.True(t, fresh)
	originalID, created := msg.ID, msg.CreatedAt
	msg.ID = uuid.Nil
	msg.Outcome = store.EmailOutcomeFailed
	fresh, err = st.RecordBillEmail(ctx, a, &msg)
	require.NoError(t, err)
	require.False(t, fresh)
	require.Equal(t, originalID, msg.ID)
	require.Equal(t, created, msg.CreatedAt)
	exists, err := st.HasBillEmail(ctx, a, mailbox.ID, msg.MessageID)
	require.NoError(t, err)
	require.True(t, exists)
	exists, err = st.HasBillEmail(ctx, b, mailbox.ID, msg.MessageID)
	require.NoError(t, err)
	require.False(t, exists)
	rows, err := st.ListBillEmails(ctx, a, uuid.Nil, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, a, rows[0].SpaceID)
	msg.Outcome = store.EmailOutcomeUnrecognised
	require.NoError(t, st.ReplaceBillEmail(ctx, a, &msg))
	saved, err := st.GetBillEmail(ctx, a, msg.ID)
	require.NoError(t, err)
	require.Equal(t, msg.Outcome, saved.Outcome)
	require.NoError(t, st.SaveEmailCursor(ctx, a, mailbox.ID, nil))
	require.NoError(t, st.ClearEmailSecret(ctx, a, mailbox.ID))
	_, err = st.EmailSecret(ctx, a, mailbox.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.NoError(t, st.DeleteEmailConnection(ctx, a, mailbox.ID))
	_, err = st.GetEmailConnection(ctx, a, mailbox.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}
