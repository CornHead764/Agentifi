package store

import (
	"context"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

	"github.com/google/uuid"
)

// A sign-in a pull left waiting on somebody. A scheduled bill pull that meets
// "we texted you a code" has nobody at a dialog, so the wait must outlive the
// request — long enough for the mailbox reader to answer it or a push to bring
// somebody to the modal.
//
// Nothing on this row is a credential: the engine keeps the browser context
// and this keeps only its id.

type BillChallenge struct {
	ID           uuid.UUID
	SpaceID      SpaceID
	ConnectionID uuid.UUID
	// AgentSession is the engine's sign-in id an answer is posted against.
	AgentSession string
	Method       string
	// Prompt is the provider's own wording.
	Prompt string
	// Image is a CAPTCHA, base64.
	Image string
	State string
	// AnsweredBy is which path answered: mailbox or person.
	AnsweredBy string
	// RaisedBy is what met the challenge: pull, connect or keepalive.
	RaisedBy   string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	AnsweredAt *time.Time
}

// The bill_challenges.state values.
const (
	BillChallengeWaiting  = "waiting"
	BillChallengeAnswered = "answered"
	BillChallengeExpired  = "expired"
	BillChallengeFailed   = "failed"
)

// The answered_by values.
const (
	BillChallengeByMailbox = "mailbox"
	BillChallengeByPerson  = "person"
)

// The raised_by values.
const (
	BillChallengeFromPull      = "pull"
	BillChallengeFromConnect   = "connect"
	BillChallengeFromKeepalive = "keepalive"
)

// IsWaiting says the challenge is still one somebody can answer.
func (c BillChallenge) IsWaiting() bool { return c.State == BillChallengeWaiting }

const billChallengeColumns = `id, space_id, connection_id, agent_session, method, prompt,
	image, state, answered_by, raised_by, created_at, expires_at, answered_at`

func scanBillChallenge(row scanner) (BillChallenge, error) {
	var (
		one      BillChallenge
		spaceID  uuid.UUID
		image    *string
		answered *string
	)
	err := row.Scan(&one.ID, &spaceID, &one.ConnectionID, &one.AgentSession, &one.Method,
		&one.Prompt, &image, &one.State, &answered, &one.RaisedBy, &one.CreatedAt,
		&one.ExpiresAt, &one.AnsweredAt)
	if err != nil {
		return one, err
	}
	one.SpaceID = SpaceIDOf(spaceID)
	one.Image = Deref(image)
	one.AnsweredBy = Deref(answered)
	return one, nil
}

func (s *Store) CreateBillChallenge(ctx context.Context, spaceID SpaceID, one *BillChallenge) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	if one.State == "" {
		one.State = BillChallengeWaiting
	}
	stored, err := scanBillChallenge(s.db.QueryRow(ctx,
		`INSERT INTO bill_challenges
		     (id, space_id, connection_id, agent_session, method, prompt, image, state,
		      raised_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING `+billChallengeColumns,
		one.ID, spaceID.UUID(), one.ConnectionID, one.AgentSession, one.Method, one.Prompt,
		dbconv.NullText(one.Image), one.State, one.RaisedBy, one.ExpiresAt))
	if err != nil {
		return wrap("store: create bill challenge", err)
	}
	*one = stored
	return nil
}

func (s *Store) GetBillChallenge(ctx context.Context, spaceID SpaceID, id uuid.UUID) (BillChallenge, error) {
	one, err := scanBillChallenge(s.db.QueryRow(ctx,
		`SELECT `+billChallengeColumns+` FROM bill_challenges WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id))
	return one, wrap("store: get bill challenge", err)
}

// ListBillChallenges is the space's challenges, newest first. An empty state
// is every one not expired.
func (s *Store) ListBillChallenges(ctx context.Context, spaceID SpaceID, state string) ([]BillChallenge, error) {
	return queryAll(ctx, s.db, "store: list bill challenges", scanBillChallenge,
		`SELECT `+billChallengeColumns+` FROM bill_challenges
		  WHERE space_id = $1
		    AND CASE WHEN $2 = '' THEN state <> $3 ELSE state = $2 END
		  ORDER BY created_at DESC`,
		spaceID.UUID(), state, BillChallengeExpired)
}

// MarkBillChallengeAnswered records who answered, and when. Only a waiting row
// moves, so an expired challenge is not revived and the mailbox and a person
// cannot both claim one.
func (s *Store) MarkBillChallengeAnswered(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, answeredBy string,
) error {
	return s.execOne(ctx, "store: answer bill challenge",
		`UPDATE bill_challenges
		    SET state = $4, answered_by = $3, answered_at = now()
		  WHERE space_id = $1 AND id = $2 AND state = $5`,
		spaceID.UUID(), id, answeredBy, BillChallengeAnswered, BillChallengeWaiting)
}

// MarkBillChallengeState ends a challenge another way: failed or expired.
func (s *Store) MarkBillChallengeState(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, state string,
) error {
	return s.execOne(ctx, "store: set bill challenge state",
		`UPDATE bill_challenges SET state = $3 WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, state)
}

// ExpireBillChallenges retires every waiting row whose session the engine has
// reaped, across every space (the scheduler runs for the install), and reports
// how many.
func (s *Store) ExpireBillChallenges(ctx context.Context, now time.Time) (int, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE bill_challenges SET state = $1 WHERE state = $2 AND expires_at <= $3`,
		BillChallengeExpired, BillChallengeWaiting, now)
	if err != nil {
		return 0, wrap("store: expire bill challenges", err)
	}
	return int(tag.RowsAffected()), nil
}
