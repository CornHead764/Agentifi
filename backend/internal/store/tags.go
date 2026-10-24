package store

import (
	"context"
	"database/sql"
	"time"

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

const tagColumns = `id, name, color, is_deleted, created_at, updated_at`

func (s *Store) CreateTag(ctx context.Context, spaceID SpaceID, t *Tag) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	t.SpaceID = spaceID
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO tags (id, name, color, is_deleted, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, t.ID, t.Name, nullableTagColor(t.Color), t.IsDeleted, now, now)
	if err == nil {
		t.CreatedAt, t.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return wrap("store: create tag", err)
}

func (s *Store) GetTag(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Tag, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return Tag{}, err
	}
	t, err := scanTag(db.Read().QueryRowContext(ctx, `SELECT `+tagColumns+` FROM tags WHERE id = ?`, id))
	t.SpaceID = spaceID
	return t, wrap("store: get tag", err)
}

func (s *Store) ListTags(ctx context.Context, spaceID SpaceID, includeDeleted bool) ([]Tag, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + tagColumns + ` FROM tags`
	if !includeDeleted {
		query += ` WHERE NOT is_deleted`
	}
	query += ` ORDER BY name`
	return sqliteQueryAll(ctx, db.Read(), "store: list tags", func(row scanner) (Tag, error) {
		t, err := scanTag(row)
		t.SpaceID = spaceID
		return t, err
	}, query)
}

func (s *Store) UpdateTag(ctx context.Context, spaceID SpaceID, t *Tag) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	err = sqliteExecOne(ctx, db.Write(), "store: update tag",
		`UPDATE tags SET name = ?, color = ?, is_deleted = ?, updated_at = ? WHERE id = ?`,
		t.Name, nullableTagColor(t.Color), t.IsDeleted, now, t.ID)
	if err == nil {
		t.UpdatedAt = fromUnixMillis(now)
	}
	return err
}

func (s *Store) DeleteTag(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete tag",
		`UPDATE tags SET is_deleted = true, updated_at = ? WHERE id = ?`, db.Now(), id)
}

func nullableTagColor(color string) any {
	if color == "" {
		return nil
	}
	return color
}

func scanTag(row scanner) (Tag, error) {
	var t Tag
	var color sql.Null[string]
	var created, updated int64
	if err := row.Scan(&t.ID, &t.Name, &color, &t.IsDeleted, &created, &updated); err != nil {
		return Tag{}, err
	}
	t.Color = color.V
	t.CreatedAt, t.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	return t, nil
}
