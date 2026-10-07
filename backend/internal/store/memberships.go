package store

import (
	"context"
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

const membershipColumns = `id, space_id, user_id, role, invited_at, accepted_at,
	dashboard_layout, created_at, updated_at`

func (s *Store) CreateMembership(ctx context.Context, spaceID SpaceID, m *Membership) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	m.SpaceID = spaceID
	err := s.db.QueryRow(ctx, `
		INSERT INTO memberships (id, space_id, user_id, role, invited_at, accepted_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`,
		m.ID, spaceID.UUID(), m.UserID, string(m.Role), m.InvitedAt, m.AcceptedAt,
	).Scan(&m.CreatedAt, &m.UpdatedAt)
	return wrap("store: create membership", err)
}

func (s *Store) ListMemberships(ctx context.Context, spaceID SpaceID) ([]Membership, error) {
	return queryAll(ctx, s.db, "store: list memberships", scanMembership,
		`SELECT `+membershipColumns+` FROM memberships WHERE space_id = $1 ORDER BY created_at`,
		spaceID.UUID())
}

// ListAllMemberships is every membership on the install, invitations
// included, for server administration only. Anything serving a household uses
// ListMemberships.
func (s *Store) ListAllMemberships(ctx context.Context) ([]Membership, error) {
	return queryAll(ctx, s.db, "store: list all memberships", scanMembership,
		`SELECT `+membershipColumns+` FROM memberships ORDER BY created_at`)
}

// GetMembership returns one user's membership in one space, accepted or not.
func (s *Store) GetMembership(ctx context.Context, spaceID SpaceID, userID uuid.UUID) (Membership, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+membershipColumns+` FROM memberships WHERE space_id = $1 AND user_id = $2`,
		spaceID.UUID(), userID)
	m, err := scanMembership(row)
	return m, wrap("store: get membership", err)
}

func (s *Store) UpdateMembership(ctx context.Context, spaceID SpaceID, m *Membership) error {
	err := s.db.QueryRow(ctx, `
		UPDATE memberships
		SET role = $3, invited_at = $4, accepted_at = $5, updated_at = now()
		WHERE space_id = $1 AND id = $2
		RETURNING updated_at`,
		spaceID.UUID(), m.ID, string(m.Role), m.InvitedAt, m.AcceptedAt,
	).Scan(&m.UpdatedAt)
	return wrap("store: update membership", err)
}

func (s *Store) DeleteMembership(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: delete membership", `DELETE FROM memberships WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
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
	return queryAll(ctx, s.db, "store: list pending invitations", func(rows scanner) (Invitation, error) {
		var inv Invitation
		var spaceID uuid.UUID
		var role string
		err := rows.Scan(&inv.Membership.ID, &spaceID, &inv.Membership.UserID, &role,
			&inv.Membership.InvitedAt, &inv.Membership.AcceptedAt,
			&inv.Membership.DashboardLayout, &inv.Membership.CreatedAt,
			&inv.Membership.UpdatedAt, &inv.SpaceName)
		if err != nil {
			return Invitation{}, err
		}
		inv.Membership.SpaceID = SpaceID(spaceID)
		inv.Membership.Role = Role(role)
		return inv, nil
	}, `
		SELECT m.id, m.space_id, m.user_id, m.role, m.invited_at, m.accepted_at,
		       m.dashboard_layout, m.created_at, m.updated_at, s.name
		FROM memberships m
		JOIN spaces s ON s.id = m.space_id
		WHERE m.user_id = $1 AND m.accepted_at IS NULL AND NOT s.is_deleted
		ORDER BY m.created_at`, userID)
}

// AcceptInvitation stamps one pending membership as accepted and returns it.
// The user id is in the WHERE clause, since there is no space to scope by. An
// accepted, foreign or deleted-space membership is ErrNotFound, identical to
// an unknown id, so the refusal does not confirm the invitation exists.
func (s *Store) AcceptInvitation(
	ctx context.Context, id, userID uuid.UUID, at time.Time,
) (Membership, error) {
	row := s.db.QueryRow(ctx, `
		UPDATE memberships m
		   SET accepted_at = $3, updated_at = now()
		 WHERE m.id = $1 AND m.user_id = $2 AND m.accepted_at IS NULL
		   AND EXISTS (SELECT 1 FROM spaces s WHERE s.id = m.space_id AND NOT s.is_deleted)
		RETURNING `+membershipColumns, id, userID, at)
	m, err := scanMembership(row)
	return m, wrap("store: accept invitation", err)
}

// DeclineInvitation drops one pending membership, scoped the same way. Leaving
// a space is DeleteMembership.
func (s *Store) DeclineInvitation(ctx context.Context, id, userID uuid.UUID) error {
	return s.execOne(ctx, "store: decline invitation",
		`DELETE FROM memberships WHERE id = $1 AND user_id = $2 AND accepted_at IS NULL`,
		id, userID)
}

// ResolveSpace decides which space a request is about, and is the only way a
// SpaceID is produced from a user. Without `requested` it takes the user's
// oldest accepted membership. accepted_at is checked here so no route can
// forget it. A missing space and a non-member are both ErrNotFound, on purpose.
func (s *Store) ResolveSpace(ctx context.Context, userID uuid.UUID, requested *SpaceID) (Space, Membership, error) {
	query := `
		SELECT m.id, m.space_id, m.user_id, m.role, m.invited_at, m.accepted_at, m.created_at, m.updated_at,
		       s.id, s.name, s.primary_currency, s.timezone, s.is_deleted, s.created_at, s.updated_at
		FROM memberships m
		JOIN spaces s ON s.id = m.space_id
		WHERE m.user_id = $1 AND m.accepted_at IS NOT NULL AND NOT s.is_deleted
		  AND ($2::uuid IS NULL OR m.space_id = $2::uuid)
		ORDER BY m.created_at
		LIMIT 1`

	var wanted *uuid.UUID
	if requested != nil {
		id := requested.UUID()
		wanted = &id
	}

	var (
		m        Membership
		space    Space
		mSpaceID uuid.UUID
		spaceKey uuid.UUID
		role     string
	)
	err := s.db.QueryRow(ctx, query, userID, wanted).Scan(
		&m.ID, &mSpaceID, &m.UserID, &role, &m.InvitedAt, &m.AcceptedAt, &m.CreatedAt, &m.UpdatedAt,
		&spaceKey, &space.Name, &space.PrimaryCurrency, &space.Timezone, &space.IsDeleted,
		&space.CreatedAt, &space.UpdatedAt)
	if err != nil {
		return Space{}, Membership{}, wrap("store: resolve space", err)
	}
	m.SpaceID = SpaceID(mSpaceID)
	m.Role = Role(role)
	space.ID = SpaceID(spaceKey)
	return space, m, nil
}

func scanMembership(row scanner) (Membership, error) {
	var m Membership
	var spaceID uuid.UUID
	var role string
	err := row.Scan(&m.ID, &spaceID, &m.UserID, &role, &m.InvitedAt, &m.AcceptedAt,
		&m.DashboardLayout, &m.CreatedAt, &m.UpdatedAt)
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
	return s.execOne(ctx, "store: set dashboard layout", `
		UPDATE memberships SET dashboard_layout = $3, updated_at = now()
		 WHERE space_id = $1 AND user_id = $2`,
		spaceID.UUID(), userID, layout)
}
