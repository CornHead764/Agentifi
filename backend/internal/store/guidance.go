package store

import (
	"context"
	"github.com/google/uuid"
	"time"
)

// Guidance is a standing instruction scoped by a household filter.
type Guidance struct {
	ID          uuid.UUID
	SpaceID     SpaceID
	Name        string
	Instruction string
	FilterID    uuid.UUID
	IsActive    bool
	Position    int
	IsDeleted   bool
	CreatedBy   uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type GuidanceQuery struct {
	IncludeDeleted bool
	ActiveOnly     bool
}

const guidanceColumns = `id, name, instruction, filter_id, is_active, "position", is_deleted, created_by, created_at, updated_at`

func (s *Store) CreateGuidance(ctx context.Context, spaceID SpaceID, note *Guidance) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	if note.ID == uuid.Nil {
		note.ID = uuid.New()
	}
	note.SpaceID = spaceID
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `INSERT INTO assistant_guidance
 (id,name,instruction,filter_id,is_active,"position",is_deleted,created_by,created_at,updated_at)
 VALUES (?,?,?,?,?,?,?,?,?,?)`, note.ID, note.Name, note.Instruction, NullUUID(note.FilterID), note.IsActive, note.Position, note.IsDeleted, NullUUID(note.CreatedBy), now, now)
	if err == nil {
		note.CreatedAt = fromUnixMillis(now)
		note.UpdatedAt = note.CreatedAt
	}
	return wrap("store: create guidance", err)
}
func (s *Store) GetGuidance(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Guidance, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return Guidance{}, err
	}
	note, err := scanGuidance(db.Read().QueryRowContext(ctx, `SELECT `+guidanceColumns+` FROM assistant_guidance WHERE id = ?`, id))
	note.SpaceID = spaceID
	return note, wrap("store: get guidance", err)
}
func (s *Store) ListGuidance(ctx context.Context, spaceID SpaceID, q GuidanceQuery) ([]Guidance, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + guidanceColumns + ` FROM assistant_guidance WHERE 1=1`
	if !q.IncludeDeleted {
		query += ` AND NOT is_deleted`
	}
	if q.ActiveOnly {
		query += ` AND is_active`
	}
	notes, err := sqliteQueryAll(ctx, db.Read(), "store: list guidance", scanGuidance, query+` ORDER BY "position",id`)
	for i := range notes {
		notes[i].SpaceID = spaceID
	}
	return notes, err
}
func (s *Store) UpdateGuidance(ctx context.Context, spaceID SpaceID, note *Guidance) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	err = sqliteExecOne(ctx, db.Write(), "store: update guidance", `UPDATE assistant_guidance SET name=?,instruction=?,filter_id=?,is_active=?,"position"=?,is_deleted=?,updated_at=? WHERE id=?`, note.Name, note.Instruction, NullUUID(note.FilterID), note.IsActive, note.Position, note.IsDeleted, now, note.ID)
	if err == nil {
		note.UpdatedAt = fromUnixMillis(now)
	}
	return err
}
func (s *Store) DeleteGuidance(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete guidance", `UPDATE assistant_guidance SET is_deleted=1,updated_at=? WHERE id=?`, db.Now(), id)
}
func (s *Store) ReorderGuidance(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) error {
	return s.InHouseholdTx(ctx, spaceID, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		for position, id := range ids {
			if err := sqliteExecOne(ctx, db.Write(), "store: reorder guidance", `UPDATE assistant_guidance SET "position"=?,updated_at=? WHERE id=? AND NOT is_deleted`, position, db.Now(), id); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) NextGuidancePosition(ctx context.Context, spaceID SpaceID) (int, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return 0, err
	}
	var next int
	err = db.Read().QueryRowContext(ctx, `SELECT coalesce(max("position")+1,0) FROM assistant_guidance WHERE NOT is_deleted`).Scan(&next)
	return next, wrap("store: next guidance position", err)
}
func scanGuidance(row scanner) (Guidance, error) {
	var note Guidance
	var filterID, createdBy *uuid.UUID
	var created, updated int64
	err := row.Scan(&note.ID, &note.Name, &note.Instruction, &filterID, &note.IsActive, &note.Position, &note.IsDeleted, &createdBy, &created, &updated)
	note.FilterID = Deref(filterID)
	note.CreatedBy = Deref(createdBy)
	note.CreatedAt = fromUnixMillis(created)
	note.UpdatedAt = fromUnixMillis(updated)
	return note, err
}
