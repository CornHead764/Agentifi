package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Role is what a membership grants. A user with no membership authenticates
// and sees nothing, as after a revoked invitation.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

func (r Role) CanWrite() bool { return r != RoleViewer }

// IsOwner reports whether this role may change membership, delete the space,
// or edit its settings — admins included.
func (r Role) IsOwner() bool { return r == RoleOwner || r == RoleAdmin }

// Owns is the narrower question: the space's owner, not an admin. An admin
// cannot change an owner's membership or make another owner, so only an owner
// counts when checking that a space still has somebody who can.
func (r Role) Owns() bool { return r == RoleOwner }

type Membership struct {
	ID      uuid.UUID
	SpaceID SpaceID
	UserID  uuid.UUID
	Role    Role
	// An invitation grants nothing until accepted; the queries here check
	// AcceptedAt so callers cannot forget.
	InvitedAt  *time.Time
	AcceptedAt *time.Time
	// DashboardLayout is this person's arrangement of this space's dashboard
	// (per membership, not per user or space). Nil is the built-in order, not
	// an empty layout.
	DashboardLayout []byte
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (m Membership) IsAccepted() bool { return m.AcceptedAt != nil }

const membershipColumns = `id, household_id, user_id, role, invited_at, accepted_at,
	dashboard_layout, created_at, updated_at`

func (s *Store) CreateMembership(ctx context.Context, spaceID SpaceID, m *Membership) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	m.SpaceID = spaceID
	db, err := s.server()
	if err != nil {
		return err
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO memberships (id, household_id, user_id, role, invited_at, accepted_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, spaceID.UUID(), m.UserID, string(m.Role), nullableMillis(m.InvitedAt), nullableMillis(m.AcceptedAt), now, now)
	if err == nil {
		m.CreatedAt, m.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return wrap("store: create membership", err)
}

func (s *Store) ListMemberships(ctx context.Context, spaceID SpaceID) ([]Membership, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list memberships", scanMembership,
		`SELECT `+membershipColumns+` FROM memberships WHERE household_id = ? ORDER BY created_at, rowid`, spaceID.UUID())
}

// ListAllMemberships is every membership on the install, invitations
// included, for server administration only. Anything serving a household uses
// ListMemberships.
func (s *Store) ListAllMemberships(ctx context.Context) ([]Membership, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list all memberships", scanMembership,
		`SELECT `+membershipColumns+` FROM memberships ORDER BY created_at, rowid`)
}

// GetMembership returns one user's membership in one space, accepted or not.
func (s *Store) GetMembership(ctx context.Context, spaceID SpaceID, userID uuid.UUID) (Membership, error) {
	db, err := s.server()
	if err != nil {
		return Membership{}, err
	}
	row := db.Read().QueryRowContext(ctx,
		`SELECT `+membershipColumns+` FROM memberships WHERE household_id = ? AND user_id = ?`,
		spaceID.UUID(), userID)
	m, err := scanMembership(row)
	return m, wrap("store: get membership", err)
}

func (s *Store) UpdateMembership(ctx context.Context, spaceID SpaceID, m *Membership) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	now := db.Now()
	err = sqliteExecOne(ctx, db.Write(), "store: update membership", `
		UPDATE memberships SET role = ?, invited_at = ?, accepted_at = ?, updated_at = ?
		WHERE household_id = ? AND id = ?`,
		string(m.Role), nullableMillis(m.InvitedAt), nullableMillis(m.AcceptedAt), now, spaceID.UUID(), m.ID)
	if err == nil {
		m.UpdatedAt = fromUnixMillis(now)
	}
	return wrap("store: update membership", err)
}

func (s *Store) DeleteMembership(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete membership", `DELETE FROM memberships WHERE household_id = ? AND id = ?`, spaceID.UUID(), id)
}

// Invitation is an outstanding membership with its space's name, which travels
// with it because every other space read refuses a non-member.
type Invitation struct {
	Membership Membership
	SpaceName  string
}

// ListPendingInvitations is every invitation this user has not taken, across
// every space. The user id is the tenancy here, carried into the WHERE clause.
func (s *Store) ListPendingInvitations(ctx context.Context, userID uuid.UUID) ([]Invitation, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list pending invitations", func(row scanner) (Invitation, error) {
		var inv Invitation
		var spaceID uuid.UUID
		var invited, accepted sql.Null[int64]
		var created, updated int64
		err := row.Scan(&inv.Membership.ID, &spaceID, &inv.Membership.UserID, &inv.Membership.Role,
			&invited, &accepted, &inv.Membership.DashboardLayout, &created, &updated, &inv.SpaceName)
		inv.Membership.SpaceID = SpaceID(spaceID)
		inv.Membership.InvitedAt, inv.Membership.AcceptedAt = nullableTime(invited), nullableTime(accepted)
		inv.Membership.CreatedAt, inv.Membership.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
		return inv, err
	}, `SELECT m.id, m.household_id, m.user_id, m.role, m.invited_at, m.accepted_at,
		       m.dashboard_layout, m.created_at, m.updated_at, s.name
		FROM memberships m JOIN households s ON s.id = m.household_id
		WHERE m.user_id = ? AND m.accepted_at IS NULL AND NOT s.is_deleted
		ORDER BY m.created_at, m.rowid`, userID)
}

// AcceptInvitation stamps one pending membership as accepted and returns it.
// The user id is in the WHERE clause, since there is no space to scope by. An
// accepted, foreign or deleted-space membership is ErrNotFound, identical to
// an unknown id, so the refusal does not confirm the invitation exists.
func (s *Store) AcceptInvitation(
	ctx context.Context, id, userID uuid.UUID, at time.Time,
) (Membership, error) {
	db, err := s.server()
	if err != nil {
		return Membership{}, err
	}
	row := db.Write().QueryRowContext(ctx, `
		UPDATE memberships
		   SET accepted_at = ?, updated_at = ?
		 WHERE id = ? AND user_id = ? AND accepted_at IS NULL
		   AND EXISTS (SELECT 1 FROM households s WHERE s.id = memberships.household_id AND NOT s.is_deleted)
		RETURNING `+membershipColumns, unixMillis(at), db.Now(), id, userID)
	m, err := scanMembership(row)
	return m, wrap("store: accept invitation", err)
}

// DeclineInvitation drops one pending membership, scoped the same way. Leaving
// a space is DeleteMembership.
func (s *Store) DeclineInvitation(ctx context.Context, id, userID uuid.UUID) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: decline invitation",
		`DELETE FROM memberships WHERE id = ? AND user_id = ? AND accepted_at IS NULL`, id, userID)
}

// ResolveSpace decides which space a request is about, and is the only way a
// SpaceID is produced from a user. Without `requested` it takes the user's
// oldest accepted membership. accepted_at is checked here so no route can
// forget it. A missing space and a non-member are both ErrNotFound, on purpose.
func (s *Store) ResolveSpace(ctx context.Context, userID uuid.UUID, requested *SpaceID) (Space, Membership, error) {
	db, err := s.server()
	if err != nil {
		return Space{}, Membership{}, err
	}
	query := `SELECT ` + membershipColumns + ` FROM memberships
		WHERE user_id = ? AND accepted_at IS NOT NULL
		AND household_id IN (SELECT id FROM households WHERE NOT is_deleted)`
	args := []any{userID}
	if requested != nil {
		query += ` AND household_id = ?`
		args = append(args, requested.UUID())
	}
	query += ` ORDER BY created_at, id LIMIT 1`
	m, err := scanMembership(db.Read().QueryRowContext(ctx, query, args...))
	if err != nil {
		return Space{}, Membership{}, wrap("store: resolve space", err)
	}
	space, err := s.GetSpace(ctx, m.SpaceID)
	return space, m, err
}

func scanMembership(row scanner) (Membership, error) {
	var m Membership
	var spaceID uuid.UUID
	var invited, accepted sql.Null[int64]
	var created, updated int64
	var role string
	err := row.Scan(&m.ID, &spaceID, &m.UserID, &role, &invited, &accepted,
		&m.DashboardLayout, &created, &updated)
	m.InvitedAt, m.AcceptedAt = nullableTime(invited), nullableTime(accepted)
	m.CreatedAt, m.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	if err != nil {
		return Membership{}, err
	}
	m.SpaceID = SpaceID(spaceID)
	m.Role = Role(role)
	return m, nil
}

// SetDashboardLayout stores one person's arrangement of one space's dashboard,
// written whole: a partial update of an order has no meaning.
func (s *Store) SetDashboardLayout(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID, layout []byte,
) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	var value any
	if layout != nil {
		value = string(layout)
	}
	return sqliteExecOne(ctx, db.Write(), "store: set dashboard layout", `
		UPDATE memberships SET dashboard_layout = ?, updated_at = ?
		 WHERE household_id = ? AND user_id = ?`, value, db.Now(), spaceID.UUID(), userID)
}
