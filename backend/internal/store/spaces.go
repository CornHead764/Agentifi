package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	if s.sqliteInTx {
		return fmt.Errorf("store: provisioning cannot run in a transaction")
	}
	db, err := s.server()
	if err != nil {
		return err
	}
	if space.ID.IsZero() {
		space.ID = NewSpaceID()
	}
	if space.PrimaryCurrency == "" {
		space.PrimaryCurrency = "USD"
	}
	if space.Timezone == "" {
		space.Timezone = "UTC"
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO households (id, name, primary_currency, timezone, is_deleted,
			created_at, updated_at, setup_guide_skipped, file_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, '[]', ?)`,
		space.ID.UUID(), space.Name, space.PrimaryCurrency, space.Timezone, space.IsDeleted,
		now, now, space.ID.UUID().String()+".db")
	if err != nil {
		return wrap("store: create space", err)
	}
	if err := s.createHousehold(ctx, space); err != nil {
		_, cleanupErr := db.Write().ExecContext(context.WithoutCancel(ctx), `DELETE FROM households WHERE id = ?`, space.ID.UUID())
		return errors.Join(wrap("store: provision household currency", err), cleanupErr)
	}
	space.CreatedAt, space.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	return nil
}

// GetSpace reads a space by id, including a deleted one; the caller decides
// whether that is an error (the admin screens need to see it).
func (s *Store) GetSpace(ctx context.Context, id SpaceID) (Space, error) {
	db, err := s.server()
	if err != nil {
		return Space{}, err
	}
	row := db.Read().QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM households WHERE id = ?`, id.UUID())
	space, err := scanSpace(row)
	return space, wrap("store: get space", err)
}

// ListSpacesForUser returns the spaces a user has joined; accepted_at is
// checked here so no screen shows an unaccepted invitation's space.
func (s *Store) ListSpacesForUser(ctx context.Context, userID uuid.UUID) ([]Space, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list spaces", scanSpace, `
		SELECT s.id, s.name, s.primary_currency, s.timezone, s.default_date_range,
			s.sidebar_account_types, s.is_deleted, s.created_at, s.updated_at
		FROM households s
		JOIN memberships m ON m.household_id = s.id
		WHERE m.user_id = ? AND m.accepted_at IS NOT NULL AND NOT s.is_deleted
		ORDER BY m.created_at, m.rowid`, userID)
}

// CountOwnedSpaces is how many live spaces the user owns; an admin role and an
// unaccepted invitation do not count.
func (s *Store) CountOwnedSpaces(ctx context.Context, userID uuid.UUID) (int, error) {
	db, err := s.server()
	if err != nil {
		return 0, err
	}
	var count int
	err = db.Read().QueryRowContext(ctx, `
		SELECT count(*) FROM memberships m JOIN households s ON s.id = m.household_id
		WHERE m.user_id = ? AND m.role = ? AND m.accepted_at IS NOT NULL AND NOT s.is_deleted`,
		userID, RoleOwner).Scan(&count)
	return count, wrap("store: count owned spaces", err)
}

// unseededTables are the household tables a new space's file already has rows
// in, or that only move alongside another table.
var unseededTables = map[string]bool{
	"goose_db_version": true, "change_counter": true, "write_counter": true,
	"tombstones": true, "household_metadata": true, "categories": true,
}

// SpaceHoldsRecords reports whether a space's file has a row in any table
// besides its categories and bookkeeping, which every new space starts with.
func (s *Store) SpaceHoldsRecords(ctx context.Context, id SpaceID) (bool, error) {
	db, err := s.household(ctx, id.UUID())
	if err != nil {
		return false, err
	}
	rows, err := db.Read().QueryContext(ctx,
		`SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return false, wrap("store: list household tables", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return false, err
		}
		if !unseededTables[name] {
			tables = append(tables, name)
		}
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return false, wrap("store: list household tables", err)
	}
	for _, table := range tables {
		var held bool
		query := `SELECT EXISTS (SELECT 1 FROM "` + strings.ReplaceAll(table, `"`, `""`) + `")`
		if err := db.Read().QueryRowContext(ctx, query).Scan(&held); err != nil {
			return false, wrap("store: read "+table, err)
		}
		if held {
			return true, nil
		}
	}
	return false, nil
}

// ListSpaces returns every live space, for background jobs with no user.
func (s *Store) ListSpaces(ctx context.Context) ([]Space, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list spaces", scanSpace,
		`SELECT `+spaceColumns+` FROM households WHERE NOT is_deleted ORDER BY created_at, rowid`)
}

func (s *Store) UpdateSpace(ctx context.Context, space *Space) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	now := db.Now()
	err = db.Write().QueryRowContext(ctx, `
		UPDATE households
		SET name = ?, primary_currency = ?, timezone = ?, default_date_range = ?,
			sidebar_account_types = ?, is_deleted = ?, updated_at = ?
		WHERE id = ? RETURNING updated_at`,
		space.Name, space.PrimaryCurrency, space.Timezone,
		NullText(space.DefaultDateRange), sidebarTypesArg(space.SidebarAccountTypes),
		space.IsDeleted, now, space.ID.UUID(),
	).Scan(&now)
	space.UpdatedAt = fromUnixMillis(now)
	return wrap("store: update space", err)
}

// DeleteSpace hard-deletes metadata after retiring the household file. Marking
// it deleted first makes interrupted retirement resumable without listing it
// as live. Retired files are retained, not unlinked, for recovery.
func (s *Store) DeleteSpace(ctx context.Context, id SpaceID) error {
	if s.sqliteInTx {
		return fmt.Errorf("store: delete space cannot run in a transaction")
	}
	db, err := s.server()
	if err != nil {
		return err
	}
	if err := sqliteExecOne(ctx, db.Write(), "store: mark deleted space",
		`UPDATE households SET is_deleted = 1, updated_at = ? WHERE id = ?`, db.Now(), id.UUID()); err != nil {
		return err
	}
	if err := s.retireHousehold(id, false); err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete space", `DELETE FROM households WHERE id = ?`, id.UUID())
}

func scanSpace(row scanner) (Space, error) {
	var space Space
	var id uuid.UUID
	var defaultRange *string
	var sidebarTypes []byte
	var created, updated int64
	err := row.Scan(&id, &space.Name, &space.PrimaryCurrency, &space.Timezone,
		&defaultRange, &sidebarTypes, &space.IsDeleted, &created, &updated)
	if err != nil {
		return Space{}, err
	}
	space.ID = SpaceID(id)
	space.CreatedAt, space.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	space.DefaultDateRange = Deref(defaultRange)
	if len(sidebarTypes) > 0 {
		if err := json.Unmarshal(sidebarTypes, &space.SidebarAccountTypes); err != nil {
			return Space{}, fmt.Errorf("spaces.sidebar_account_types: %w", err)
		}
	}
	return space, nil
}

// sidebarTypesArg keeps NULL ("never chosen"), [] ("none") and a list apart;
// encoding nil as [] would empty every untouched sidebar. The JSON is bound as
// a string: households is STRICT, and a []byte binds as a BLOB.
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
