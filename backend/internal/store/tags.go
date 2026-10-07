package store

import (
	"context"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

	"github.com/google/uuid"
)

type Tag struct {
	ID        uuid.UUID
	SpaceID   SpaceID
	Name      string
	Color     string
	IsDeleted bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

const tagColumns = `id, space_id, name, color, is_deleted, created_at, updated_at`

func (s *Store) CreateTag(ctx context.Context, spaceID SpaceID, t *Tag) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	t.SpaceID = spaceID
	err := s.db.QueryRow(ctx, `
		INSERT INTO tags (id, space_id, name, color, is_deleted)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at`,
		t.ID, spaceID.UUID(), t.Name, pgconv.NullText(t.Color), t.IsDeleted,
	).Scan(&t.CreatedAt, &t.UpdatedAt)
	return wrap("store: create tag", err)
}

func (s *Store) GetTag(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Tag, error) {
	row := s.db.QueryRow(ctx, `SELECT `+tagColumns+` FROM tags WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	t, err := scanTag(row)
	return t, wrap("store: get tag", err)
}

func (s *Store) ListTags(ctx context.Context, spaceID SpaceID, includeDeleted bool) ([]Tag, error) {
	sql := `SELECT ` + tagColumns + ` FROM tags WHERE space_id = $1`
	if !includeDeleted {
		sql += ` AND NOT is_deleted`
	}
	sql += ` ORDER BY name`

	return queryAll(ctx, s.db, "store: list tags", scanTag, sql, spaceID.UUID())
}

func (s *Store) UpdateTag(ctx context.Context, spaceID SpaceID, t *Tag) error {
	err := s.db.QueryRow(ctx, `
		UPDATE tags SET name = $3, color = $4, is_deleted = $5, updated_at = now()
		WHERE space_id = $1 AND id = $2
		RETURNING updated_at`,
		spaceID.UUID(), t.ID, t.Name, pgconv.NullText(t.Color), t.IsDeleted,
	).Scan(&t.UpdatedAt)
	return wrap("store: update tag", err)
}

func (s *Store) DeleteTag(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: delete tag",
		`UPDATE tags SET is_deleted = true, updated_at = now() WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
}

func scanTag(row scanner) (Tag, error) {
	var t Tag
	var spaceID uuid.UUID
	var color *string
	if err := row.Scan(&t.ID, &spaceID, &t.Name, &color, &t.IsDeleted, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return Tag{}, err
	}
	t.SpaceID = SpaceID(spaceID)
	t.Color = Deref(color)
	return t, nil
}
