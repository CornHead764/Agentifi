package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

func TestADeviceCodeSignInAnswersOnlyTheConnectionThatStartedIt(t *testing.T) {
	space, connection := store.NewSpaceID(), uuid.New()
	session := uuid.NewString()
	mailboxSignIns.Put(session, &mailboxSignInState{
		spaceID: space, connectionID: connection,
		state: MailboxSignInFailed, err: "consent declined",
		expiresAt: time.Now().Add(time.Minute),
	}, time.Now().Add(time.Minute))
	mailbox := &Mailbox{}

	state, _ := mailbox.GraphSignInState(store.NewSpaceID(), connection, session)
	require.Equal(t, MailboxSignInExpired, state, "another space read the outcome")
	state, _ = mailbox.GraphSignInState(space, uuid.New(), session)
	require.Equal(t, MailboxSignInExpired, state, "another connection read the outcome")

	state, detail := mailbox.GraphSignInState(space, connection, session)
	require.Equal(t, MailboxSignInFailed, state)
	require.Equal(t, "consent declined", detail)
}
