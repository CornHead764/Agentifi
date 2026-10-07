package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The sign-in bookkeeping bill connections and merchant accounts share.

// signIns records which space and connection started each sign-in, by the
// engine's session id: an id alone should not open somebody's sign-in.
var signIns expiring[string, signInOwner]

type signInOwner struct {
	space      store.SpaceID
	connection uuid.UUID
}

const signInMemory = time.Hour

func rememberSignIn(sessionID string, spaceID store.SpaceID, connectionID uuid.UUID) {
	if sessionID != "" {
		signIns.Put(sessionID, signInOwner{space: spaceID, connection: connectionID}, time.Now().Add(signInMemory))
	}
}

func ownSignIn(sessionID string, spaceID store.SpaceID, connectionID uuid.UUID) error {
	if owner, ok := signIns.Get(sessionID); !ok || owner != (signInOwner{space: spaceID, connection: connectionID}) {
		return fmt.Errorf("sign-in: %w", store.ErrNotFound)
	}
	return nil
}

var ErrNoMailboxForCode = errors.New("no mailbox is connected to read the code from")

// MailAnswerer is a connector whose sign-ins AnswerFromMail can answer: Bills
// or Merchants, with S the state its sign-in reports.
type MailAnswerer[S any] interface {
	// mailedCode is how to wait for the target's code, or nil with no mailbox
	// to read it from.
	mailedCode(ctx context.Context, spaceID store.SpaceID, target uuid.UUID) (func(since time.Time) (string, bool), error)
	signInStatus(ctx context.Context, sessionID string) (S, error)
	answerSignIn(ctx context.Context, sessionID, code string) (S, error)
}

// AnswerFromMail waits for the code a typed sign-in was sent to reach the
// household's mailbox and types it in; found is false when none arrived in
// time, and the state is then where the sign-in stands. With no mailbox it is
// ErrNoMailboxForCode, which the dialog treats the same. The dialog runs it
// beside the code field, so a person who types the code first wins and the
// mailed code types nothing.
func AnswerFromMail[S any](
	ctx context.Context, connector MailAnswerer[S], spaceID store.SpaceID, target uuid.UUID, sessionID string,
) (state S, found bool, err error) {
	if err := ownSignIn(sessionID, spaceID, target); err != nil {
		return state, false, err
	}
	wait, err := connector.mailedCode(ctx, spaceID, target)
	if err != nil {
		return state, false, err
	}
	if wait == nil {
		return state, false, ErrNoMailboxForCode
	}
	code, found := wait(time.Now())
	if !found {
		state, err = connector.signInStatus(ctx, sessionID)
		return state, false, err
	}
	state, err = connector.answerSignIn(ctx, sessionID, code)
	return state, true, err
}
