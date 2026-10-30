package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

type Category struct {
	ID       uuid.UUID
	SpaceID  SpaceID
	ParentID uuid.UUID
	Name     string
	Kind     domain.CategoryKind

	// KnownCategoryID is Simplifi's stable system marker, kept so imported
	// rows keep their special meanings after a user renames the category.
	KnownCategoryID string
	// TxfID is the Tax Exchange Format code the Taxes report groups by.
	// TxfIDs is the fuller list a category may map to; the report reads TxfID.
	TxfID  string
	TxfIDs []string

	IsUserAssignable bool
	IsEditable       bool

	ExcludedFromReports      bool
	ExcludedFromSpendingPlan bool
	ExcludedFromCategoryList bool

	SortOrder int
	IsDeleted bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

const categoryColumns = `id, parent_id, name, kind, known_category_id,
	txf_id, txf_ids, is_user_assignable, is_editable, excluded_from_reports,
	excluded_from_spending_plan, excluded_from_category_list, sort_order, is_deleted,
	created_at, updated_at`

func (s *Store) CreateCategory(ctx context.Context, spaceID SpaceID, c *Category) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	c.SpaceID = spaceID
	if c.TxfIDs == nil {
		c.TxfIDs = []string{}
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO categories (id, parent_id, name, kind, known_category_id,
			txf_id, txf_ids, is_user_assignable, is_editable, excluded_from_reports,
			excluded_from_spending_plan, excluded_from_category_list, sort_order, is_deleted,
			created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, categoryParent(c.ParentID), c.Name, string(c.Kind),
		nullableTagColor(c.KnownCategoryID), nullableTagColor(c.TxfID), JSONArray[string](c.TxfIDs), c.IsUserAssignable, c.IsEditable,
		c.ExcludedFromReports, c.ExcludedFromSpendingPlan, c.ExcludedFromCategoryList,
		c.SortOrder, c.IsDeleted, now, now)
	if err == nil {
		c.CreatedAt, c.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return wrap("store: create category", err)
}

func (s *Store) GetCategory(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Category, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return Category{}, err
	}
	c, err := scanCategory(db.Read().QueryRowContext(ctx,
		`SELECT `+categoryColumns+` FROM categories WHERE id = ?`, id))
	c.SpaceID = spaceID
	return c, wrap("store: get category", err)
}

// ListCategories returns the space's categories, deleted ones included only on
// request — a deleted category still names the transactions that referenced
// it, so a report that hides it silently mislabels rows as uncategorized.
func (s *Store) ListCategories(ctx context.Context, spaceID SpaceID, includeDeleted bool) ([]Category, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + categoryColumns + ` FROM categories`
	if !includeDeleted {
		query += ` WHERE NOT is_deleted`
	}
	query += ` ORDER BY sort_order, name`
	return sqliteQueryAll(ctx, db.Read(), "store: list categories", func(row scanner) (Category, error) {
		c, err := scanCategory(row)
		c.SpaceID = spaceID
		return c, err
	}, query)
}

func (s *Store) UpdateCategory(ctx context.Context, spaceID SpaceID, c *Category) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	if c.TxfIDs == nil {
		c.TxfIDs = []string{}
	}
	now := db.Now()
	err = sqliteExecOne(ctx, db.Write(), "store: update category", `
		UPDATE categories SET
			parent_id = ?, name = ?, kind = ?, known_category_id = ?,
			txf_id = ?, txf_ids = ?, is_user_assignable = ?, is_editable = ?,
			excluded_from_reports = ?, excluded_from_spending_plan = ?,
			excluded_from_category_list = ?, sort_order = ?, is_deleted = ?,
			updated_at = ?
		WHERE id = ?`,
		categoryParent(c.ParentID), c.Name, string(c.Kind),
		nullableTagColor(c.KnownCategoryID), nullableTagColor(c.TxfID), JSONArray[string](c.TxfIDs), c.IsUserAssignable, c.IsEditable,
		c.ExcludedFromReports, c.ExcludedFromSpendingPlan, c.ExcludedFromCategoryList,
		c.SortOrder, c.IsDeleted, now, c.ID)
	if err == nil {
		c.UpdatedAt = fromUnixMillis(now)
	}
	return err
}

func (s *Store) DeleteCategory(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	if err := s.refuseProtectedCategories(ctx, spaceID, []uuid.UUID{id}); err != nil {
		return err
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete category",
		`UPDATE categories SET is_deleted = true, updated_at = ? WHERE id = ?`, db.Now(), id)
}

// refuseProtectedCategories returns ErrCategoryProtected when any of ids is a
// category domain.CategoryProtection keeps.
func (s *Store) refuseProtectedCategories(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	in, args := InArgs(ids)
	markers, markerArgs := InArgs(domain.ProtectedKnownCategoryIDs())
	args = append(args, markerArgs...)
	var protected bool
	err = db.Read().QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM categories
		WHERE id IN `+in+`
		  AND (NOT is_editable OR known_category_id IN `+markers+`))`, args...).Scan(&protected)
	if err != nil {
		return wrap("store: check protected categories", err)
	}
	if protected {
		return ErrCategoryProtected
	}
	return nil
}

func categoryParent(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func scanCategory(row scanner) (Category, error) {
	var c Category
	var parent sql.Null[uuid.UUID]
	var known, txf sql.Null[string]
	var kind string
	var ids JSONArray[string]
	var created, updated int64
	err := row.Scan(&c.ID, &parent, &c.Name, &kind, &known, &txf,
		&ids, &c.IsUserAssignable, &c.IsEditable, &c.ExcludedFromReports,
		&c.ExcludedFromSpendingPlan, &c.ExcludedFromCategoryList, &c.SortOrder,
		&c.IsDeleted, &created, &updated)
	if err != nil {
		return Category{}, err
	}
	c.Kind, c.ParentID = domain.CategoryKind(kind), parent.V
	c.KnownCategoryID, c.TxfID, c.TxfIDs = known.V, txf.V, ids
	c.CreatedAt, c.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	return c, nil
}
