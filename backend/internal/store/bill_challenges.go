package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

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

const billChallengeColumns = `id, connection_id, agent_session, method, prompt,
	image, state, answered_by, raised_by, created_at, expires_at, answered_at`

func scanBillChallenge(spaceID SpaceID) func(scanner) (BillChallenge, error) {
	return func(row scanner) (BillChallenge, error) {
		one := BillChallenge{SpaceID: spaceID}
		var image, answered sql.Null[string]
		var created, expires int64
		var answeredAt sql.Null[int64]
		err := row.Scan(&one.ID, &one.ConnectionID, &one.AgentSession, &one.Method,
			&one.Prompt, &image, &one.State, &answered, &one.RaisedBy, &created,
			&expires, &answeredAt)
		one.Image, one.AnsweredBy = image.V, answered.V
		one.CreatedAt, one.ExpiresAt = fromUnixMillis(created), fromUnixMillis(expires)
		one.AnsweredAt = nullableTime(answeredAt)
		return one, err
	}
}

func (s *Store) CreateBillChallenge(ctx context.Context, spaceID SpaceID, one *BillChallenge) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	if one.State == "" {
		one.State = BillChallengeWaiting
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	stored, err := scanBillChallenge(spaceID)(db.Write().QueryRowContext(ctx,
		`INSERT INTO bill_challenges
		     (id, connection_id, agent_session, method, prompt, image, state,
		      raised_by, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING `+billChallengeColumns,
		one.ID, one.ConnectionID, one.AgentSession, one.Method, one.Prompt,
		pgconv.NullText(one.Image), one.State, one.RaisedBy, unixMillis(one.ExpiresAt), db.Now()))
	if err != nil {
		return wrap("store: create bill challenge", err)
	}
	*one = stored
	return nil
}

func (s *Store) GetBillChallenge(ctx context.Context, spaceID SpaceID, id uuid.UUID) (BillChallenge, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return BillChallenge{}, err
	}
	one, err := scanBillChallenge(spaceID)(db.Read().QueryRowContext(ctx,
		`SELECT `+billChallengeColumns+` FROM bill_challenges WHERE id = ?`, id))
	return one, wrap("store: get bill challenge", err)
}

// ListBillChallenges is the space's challenges, newest first. An empty state
// is every one not expired.
func (s *Store) ListBillChallenges(ctx context.Context, spaceID SpaceID, state string) ([]BillChallenge, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list bill challenges", scanBillChallenge(spaceID),
		`SELECT `+billChallengeColumns+` FROM bill_challenges
		  WHERE CASE WHEN ? = '' THEN state <> ? ELSE state = ? END
		  ORDER BY created_at DESC`,
		state, BillChallengeExpired, state)
}

// MarkBillChallengeAnswered records who answered, and when. Only a waiting row
// moves, so an expired challenge is not revived and the mailbox and a person
// cannot both claim one.
func (s *Store) MarkBillChallengeAnswered(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, answeredBy string,
) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: answer bill challenge",
		`UPDATE bill_challenges
		    SET state = ?, answered_by = ?, answered_at = ?
		  WHERE id = ? AND state = ?`,
		BillChallengeAnswered, answeredBy, db.Now(), id, BillChallengeWaiting)
}

// MarkBillChallengeState ends a challenge another way: failed or expired.
func (s *Store) MarkBillChallengeState(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, state string,
) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: set bill challenge state",
		`UPDATE bill_challenges SET state = ? WHERE id = ?`, state, id)
}

// ExpireBillChallenges retires every waiting row whose session the engine has
// reaped, across every space (the scheduler runs for the install), and reports
// how many.
// Files are visited in household creation order (then ID). Each file's update
// is atomic, but the sweep is not: an error returns the completed row count.
func (s *Store) ExpireBillChallenges(ctx context.Context, now time.Time) (int, error) {
	ids, err := s.householdIDs(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		db, err := s.household(ctx, id.UUID())
		if err != nil {
			return total, err
		}
		tag, err := db.Write().ExecContext(ctx,
			`UPDATE bill_challenges SET state = ? WHERE state = ? AND expires_at <= ?`,
			BillChallengeExpired, BillChallengeWaiting, unixMillis(now))
		if err != nil {
			return total, wrap("store: expire bill challenges", err)
		}
		n, err := tag.RowsAffected()
		if err != nil {
			return total, wrap("store: expire bill challenges", err)
		}
		total += int(n)
	}
	return total, nil
}
