package store

import (
	"context"
	"time"

	. "github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/gen/jet/household/model"
	tb "github.com/CornHead764/agentifi/backend/internal/gen/jet/household/table"
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

var tagColumns = ColumnList{tb.Tags.ID, tb.Tags.Name, tb.Tags.Color, tb.Tags.IsDeleted, tb.Tags.CreatedAt, tb.Tags.UpdatedAt}

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
	_, err = tb.Tags.INSERT(tagColumns).
		MODEL(model.Tags{ID: t.ID, Name: t.Name, Color: NullText(t.Color), IsDeleted: t.IsDeleted, CreatedAt: now, UpdatedAt: now}).
		ExecContext(ctx, db.Write())
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
	var row model.Tags
	if err := SELECT(tagColumns).FROM(tb.Tags).WHERE(tb.Tags.ID.EQ(UUID(id))).QueryContext(ctx, db.Read(), &row); err != nil {
		return Tag{}, wrap("store: get tag", err)
	}
	return tagFromRow(row, spaceID), nil
}

func (s *Store) ListTags(ctx context.Context, spaceID SpaceID, includeDeleted bool) ([]Tag, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	query := SELECT(tagColumns).FROM(tb.Tags).ORDER_BY(tb.Tags.Name)
	if !includeDeleted {
		query = query.WHERE(NOT(tb.Tags.IsDeleted))
	}
	var rows []model.Tags
	if err := query.QueryContext(ctx, db.Read(), &rows); err != nil {
		return nil, wrap("store: list tags", err)
	}
	tags := make([]Tag, len(rows))
	for i, row := range rows {
		tags[i] = tagFromRow(row, spaceID)
	}
	return tags, nil
}

func (s *Store) UpdateTag(ctx context.Context, spaceID SpaceID, t *Tag) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	tags := tb.Tags
	err = execOne(ctx, db.Write(), "store: update tag",
		tags.UPDATE(tags.Name, tags.Color, tags.IsDeleted, tags.UpdatedAt).
			MODEL(model.Tags{Name: t.Name, Color: NullText(t.Color), IsDeleted: t.IsDeleted, UpdatedAt: now}).
			WHERE(tags.ID.EQ(UUID(t.ID))))
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
	tags := tb.Tags
	return execOne(ctx, db.Write(), "store: delete tag",
		tags.UPDATE().SET(tags.IsDeleted.SET(Bool(true)), tags.UpdatedAt.SET(Int(db.Now()))).
			WHERE(tags.ID.EQ(UUID(id))))
}

func tagFromRow(row model.Tags, spaceID SpaceID) Tag {
	return Tag{ID: row.ID, SpaceID: spaceID, Name: row.Name, Color: deref(row.Color), IsDeleted: row.IsDeleted,
		CreatedAt: fromUnixMillis(row.CreatedAt), UpdatedAt: fromUnixMillis(row.UpdatedAt)}
}
