package store

import (
	"context"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

	"github.com/google/uuid"
)

// Guidance is a standing instruction to the assistant: words passed to the
// model verbatim, scoped by a shared Filter (ground rule 3). Nothing here
// parses or decides; see internal/domain/guidance.go.
//
// The API refuses a note without conditions, since the evaluator reads an
// empty clause list as matching nothing. FilterID is nullable only because the
// filter row is written first.
type Guidance struct {
	ID          uuid.UUID
	SpaceID     SpaceID
	Name        string
	Instruction string
	FilterID    uuid.UUID
	IsActive    bool
	// Position orders the notes as the household arranged them; notes about
	// one payee are read as one paragraph.
	Position  int
	IsDeleted bool
	CreatedBy uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GuidanceQuery narrows a listing. The zero value is every live note, on or
// off, in display order.
type GuidanceQuery struct {
	IncludeDeleted bool
	// ActiveOnly is the run path, so a switched-off note cannot reach a prompt
	// through a forgetful caller.
	ActiveOnly bool
}

const guidanceColumns = `id, space_id, name, instruction, filter_id, is_active, "position",
	is_deleted, created_by, created_at, updated_at`

func (s *Store) CreateGuidance(ctx context.Context, spaceID SpaceID, note *Guidance) error {
	if note.ID == uuid.Nil {
		note.ID = uuid.New()
	}
	note.SpaceID = spaceID
	err := s.db.QueryRow(ctx, `
		INSERT INTO assistant_guidance (id, space_id, name, instruction, filter_id, is_active,
			"position", is_deleted, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at, updated_at`,
		note.ID, spaceID.UUID(), note.Name, note.Instruction, dbconv.NullUUID(note.FilterID),
		note.IsActive, note.Position, note.IsDeleted, dbconv.NullUUID(note.CreatedBy),
	).Scan(&note.CreatedAt, &note.UpdatedAt)
	return wrap("store: create guidance", err)
}

func (s *Store) GetGuidance(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Guidance, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+guidanceColumns+` FROM assistant_guidance WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	note, err := scanGuidance(row)
	return note, wrap("store: get guidance", err)
}

// ListGuidance returns notes in read order: position, then id, so the
// paragraph the model reads is stable between runs.
func (s *Store) ListGuidance(
	ctx context.Context, spaceID SpaceID, q GuidanceQuery,
) ([]Guidance, error) {
	sql := `SELECT ` + guidanceColumns + ` FROM assistant_guidance WHERE space_id = $1`
	if !q.IncludeDeleted {
		sql += ` AND NOT is_deleted`
	}
	if q.ActiveOnly {
		sql += ` AND is_active`
	}
	sql += ` ORDER BY "position", id`

	return queryAll(ctx, s.db, "store: list guidance", scanGuidance, sql, spaceID.UUID())
}

func (s *Store) UpdateGuidance(ctx context.Context, spaceID SpaceID, note *Guidance) error {
	err := s.db.QueryRow(ctx, `
		UPDATE assistant_guidance SET name = $3, instruction = $4, filter_id = $5,
			is_active = $6, "position" = $7, is_deleted = $8, updated_at = now()
		WHERE space_id = $1 AND id = $2
		RETURNING updated_at`,
		spaceID.UUID(), note.ID, note.Name, note.Instruction, dbconv.NullUUID(note.FilterID),
		note.IsActive, note.Position, note.IsDeleted,
	).Scan(&note.UpdatedAt)
	return wrap("store: update guidance", err)
}

// DeleteGuidance soft-deletes and keeps its filter, so the row stays
// recoverable with its conditions.
func (s *Store) DeleteGuidance(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: delete guidance", `UPDATE assistant_guidance SET is_deleted = true,
		updated_at = now() WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
}

// ReorderGuidance renumbers notes to the given order in one transaction.
func (s *Store) ReorderGuidance(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) error {
	return s.InTx(ctx, func(tx *Store) error {
		for position, id := range ids {
			if err := tx.execOne(ctx, "store: reorder guidance", `
				UPDATE assistant_guidance SET "position" = $3, updated_at = now()
				WHERE space_id = $1 AND id = $2 AND NOT is_deleted`,
				spaceID.UUID(), id, position); err != nil {
				return err
			}
		}
		return nil
	})
}

// NextGuidancePosition puts a new note last.
func (s *Store) NextGuidancePosition(ctx context.Context, spaceID SpaceID) (int, error) {
	var next int
	err := s.db.QueryRow(ctx, `SELECT coalesce(max("position") + 1, 0)
		FROM assistant_guidance WHERE space_id = $1 AND NOT is_deleted`,
		spaceID.UUID()).Scan(&next)
	return next, wrap("store: next guidance position", err)
}

func scanGuidance(row scanner) (Guidance, error) {
	var (
		note                Guidance
		spaceID             uuid.UUID
		filterID, createdBy *uuid.UUID
	)
	err := row.Scan(&note.ID, &spaceID, &note.Name, &note.Instruction, &filterID,
		&note.IsActive, &note.Position, &note.IsDeleted, &createdBy,
		&note.CreatedAt, &note.UpdatedAt)
	if err != nil {
		return Guidance{}, err
	}
	note.SpaceID = SpaceID(spaceID)
	note.FilterID = Deref(filterID)
	note.CreatedBy = Deref(createdBy)
	return note, nil
}
