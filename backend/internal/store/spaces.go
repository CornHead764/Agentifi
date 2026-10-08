package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

	"github.com/google/uuid"
)

// Space is one complete set of finances — Simplifi's "dataset" — and the
// tenancy root, so the one table with no space_id.
type Space struct {
	ID   SpaceID
	Name string
	// PrimaryCurrency is per space; transactions.amount_primary is converted
	// against it.
	PrimaryCurrency string
	// Timezone interprets date-only columns and decides when "this month"
	// rolls over for the spending plan.
	Timezone string

	// DefaultDateRange is the preset every page opens on ("1M", "YTD", "ALL").
	// Empty means the built-in default and is not written into the column, so
	// changing the default needs no migration.
	DefaultDateRange string
	// SidebarAccountTypes: nil is "all" and empty non-nil is "none".
	SidebarAccountTypes []string

	IsDeleted bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

const spaceColumns = `id, name, primary_currency, timezone, default_date_range,
	sidebar_account_types, is_deleted, created_at, updated_at`

func (s *Store) CreateSpace(ctx context.Context, space *Space) error {
	if space.ID.IsZero() {
		space.ID = NewSpaceID()
	}
	if space.PrimaryCurrency == "" {
		space.PrimaryCurrency = "USD"
	}
	if space.Timezone == "" {
		space.Timezone = "UTC"
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO spaces (id, name, primary_currency, timezone, is_deleted)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at`,
		space.ID.UUID(), space.Name, space.PrimaryCurrency, space.Timezone, space.IsDeleted,
	).Scan(&space.CreatedAt, &space.UpdatedAt)
	return wrap("store: create space", err)
}

// GetSpace reads a space by id, including a deleted one; the caller decides
// whether that is an error (the admin screens need to see it).
func (s *Store) GetSpace(ctx context.Context, id SpaceID) (Space, error) {
	row := s.db.QueryRow(ctx, `SELECT `+spaceColumns+` FROM spaces WHERE id = $1`, id.UUID())
	space, err := scanSpace(row)
	return space, wrap("store: get space", err)
}

// ListSpacesForUser returns the spaces a user has joined; accepted_at is
// checked here so no screen shows an unaccepted invitation's space.
func (s *Store) ListSpacesForUser(ctx context.Context, userID uuid.UUID) ([]Space, error) {
	return queryAll(ctx, s.db, "store: list spaces", scanSpace, `
		SELECT s.id, s.name, s.primary_currency, s.timezone, s.default_date_range,
			s.sidebar_account_types, s.is_deleted, s.created_at, s.updated_at
		FROM spaces s
		JOIN memberships m ON m.space_id = s.id
		WHERE m.user_id = $1 AND m.accepted_at IS NOT NULL AND NOT s.is_deleted
		ORDER BY m.created_at`, userID)
}

// ListSpaces returns every live space, for background jobs with no user.
func (s *Store) ListSpaces(ctx context.Context) ([]Space, error) {
	return queryAll(ctx, s.db, "store: list spaces", scanSpace,
		`SELECT `+spaceColumns+` FROM spaces WHERE NOT is_deleted ORDER BY created_at`)
}

func (s *Store) UpdateSpace(ctx context.Context, space *Space) error {
	err := s.db.QueryRow(ctx, `
		UPDATE spaces
		SET name = $2, primary_currency = $3, timezone = $4, default_date_range = $5,
			sidebar_account_types = $6, is_deleted = $7, updated_at = now()
		WHERE id = $1
		RETURNING updated_at`,
		space.ID.UUID(), space.Name, space.PrimaryCurrency, space.Timezone,
		dbconv.NullText(space.DefaultDateRange), sidebarTypesArg(space.SidebarAccountTypes),
		space.IsDeleted,
	).Scan(&space.UpdatedAt)
	return wrap("store: update space", err)
}

// restrictedBySpace is every space-scoped table that refers to another with
// ON DELETE RESTRICT. Their rows go before the space, so deleting it does not
// rest on the order the cascade from spaces happens to reach them in.
var restrictedBySpace = []string{
	"assistant_automations", "assistant_guidance", "envelopes", "rules", "watchlists",
	"holdings", "transactions",
}

// DeleteSpace removes a space and every row in it, for good. The transaction
// holds the database's write lock from its start, so a write into the space
// that starts meanwhile waits and then finds no space rather than landing in
// one half deleted.
func (s *Store) DeleteSpace(ctx context.Context, id SpaceID) error {
	return s.InTx(ctx, func(tx *Store) error {
		var found uuid.UUID
		err := tx.db.QueryRow(ctx, `SELECT id FROM spaces WHERE id = $1`, id.UUID()).Scan(&found)
		if err != nil {
			return wrap("store: find space", err)
		}
		for _, table := range restrictedBySpace {
			if _, err := tx.db.Exec(ctx, `DELETE FROM `+table+` WHERE space_id = $1`, id.UUID()); err != nil {
				return wrap("store: delete space "+table, err)
			}
		}
		return tx.execOne(ctx, "store: delete space", `DELETE FROM spaces WHERE id = $1`, id.UUID())
	})
}

func scanSpace(row scanner) (Space, error) {
	var space Space
	var id uuid.UUID
	var defaultRange *string
	var sidebarTypes []byte
	err := row.Scan(&id, &space.Name, &space.PrimaryCurrency, &space.Timezone,
		&defaultRange, &sidebarTypes, &space.IsDeleted, &space.CreatedAt, &space.UpdatedAt)
	if err != nil {
		return Space{}, err
	}
	space.ID = SpaceID(id)
	space.DefaultDateRange = Deref(defaultRange)
	if len(sidebarTypes) > 0 {
		if err := json.Unmarshal(sidebarTypes, &space.SidebarAccountTypes); err != nil {
			return Space{}, fmt.Errorf("spaces.sidebar_account_types: %w", err)
		}
	}
	return space, nil
}

// sidebarTypesArg keeps NULL ("never chosen"), [] ("none") and a list apart;
// encoding nil as [] would empty every untouched sidebar.
func sidebarTypesArg(types []string) any {
	if types == nil {
		return nil
	}
	encoded, err := json.Marshal(types)
	if err != nil {
		return nil
	}
	return string(encoded)
}
