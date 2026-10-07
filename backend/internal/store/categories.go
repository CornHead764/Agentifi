package store

import (
	"context"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

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

const categoryColumns = `id, space_id, parent_id, name, kind, known_category_id,
	txf_id, txf_ids, is_user_assignable, is_editable, excluded_from_reports,
	excluded_from_spending_plan, excluded_from_category_list, sort_order, is_deleted,
	created_at, updated_at`

func (s *Store) CreateCategory(ctx context.Context, spaceID SpaceID, c *Category) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	c.SpaceID = spaceID
	if c.TxfIDs == nil {
		c.TxfIDs = []string{}
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO categories (id, space_id, parent_id, name, kind, known_category_id,
			txf_id, txf_ids, is_user_assignable, is_editable, excluded_from_reports,
			excluded_from_spending_plan, excluded_from_category_list, sort_order, is_deleted)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING created_at, updated_at`,
		c.ID, spaceID.UUID(), pgconv.NullUUID(c.ParentID), c.Name, string(c.Kind),
		pgconv.NullText(c.KnownCategoryID), pgconv.NullText(c.TxfID), c.TxfIDs, c.IsUserAssignable, c.IsEditable,
		c.ExcludedFromReports, c.ExcludedFromSpendingPlan, c.ExcludedFromCategoryList,
		c.SortOrder, c.IsDeleted,
	).Scan(&c.CreatedAt, &c.UpdatedAt)
	return wrap("store: create category", err)
}

func (s *Store) GetCategory(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Category, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+categoryColumns+` FROM categories WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	c, err := scanCategory(row)
	return c, wrap("store: get category", err)
}

// ListCategories returns the space's categories, deleted ones included only on
// request — a deleted category still names the transactions that referenced
// it, so a report that hides it silently mislabels rows as uncategorized.
func (s *Store) ListCategories(ctx context.Context, spaceID SpaceID, includeDeleted bool) ([]Category, error) {
	sql := `SELECT ` + categoryColumns + ` FROM categories WHERE space_id = $1`
	if !includeDeleted {
		sql += ` AND NOT is_deleted`
	}
	sql += ` ORDER BY sort_order, name`

	return queryAll(ctx, s.db, "store: list categories", scanCategory, sql, spaceID.UUID())
}

func (s *Store) UpdateCategory(ctx context.Context, spaceID SpaceID, c *Category) error {
	if c.TxfIDs == nil {
		c.TxfIDs = []string{}
	}
	err := s.db.QueryRow(ctx, `
		UPDATE categories SET
			parent_id = $3, name = $4, kind = $5, known_category_id = $6,
			txf_id = $7, txf_ids = $8, is_user_assignable = $9, is_editable = $10,
			excluded_from_reports = $11, excluded_from_spending_plan = $12,
			excluded_from_category_list = $13, sort_order = $14, is_deleted = $15,
			updated_at = now()
		WHERE space_id = $1 AND id = $2
		RETURNING updated_at`,
		spaceID.UUID(), c.ID, pgconv.NullUUID(c.ParentID), c.Name, string(c.Kind),
		pgconv.NullText(c.KnownCategoryID), pgconv.NullText(c.TxfID), c.TxfIDs, c.IsUserAssignable, c.IsEditable,
		c.ExcludedFromReports, c.ExcludedFromSpendingPlan, c.ExcludedFromCategoryList,
		c.SortOrder, c.IsDeleted,
	).Scan(&c.UpdatedAt)
	return wrap("store: update category", err)
}

func (s *Store) DeleteCategory(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	if err := s.refuseProtectedCategories(ctx, spaceID, []uuid.UUID{id}); err != nil {
		return err
	}
	return s.execOne(ctx, "store: delete category",
		`UPDATE categories SET is_deleted = true, updated_at = now() WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
}

// refuseProtectedCategories returns ErrCategoryProtected when any of ids is a
// category domain.CategoryProtection keeps.
func (s *Store) refuseProtectedCategories(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) error {
	var protected bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM categories
		WHERE space_id = $1 AND id = ANY($2)
		  AND (NOT is_editable OR known_category_id = ANY($3)))`,
		spaceID.UUID(), ids, domain.ProtectedKnownCategoryIDs()).Scan(&protected)
	if err != nil {
		return wrap("store: check protected categories", err)
	}
	if protected {
		return ErrCategoryProtected
	}
	return nil
}

func scanCategory(row scanner) (Category, error) {
	var (
		c              Category
		spaceID        uuid.UUID
		parentID       *uuid.UUID
		knownID, txfID *string
		kind           string
	)
	err := row.Scan(&c.ID, &spaceID, &parentID, &c.Name, &kind, &knownID, &txfID,
		&c.TxfIDs, &c.IsUserAssignable, &c.IsEditable, &c.ExcludedFromReports,
		&c.ExcludedFromSpendingPlan, &c.ExcludedFromCategoryList, &c.SortOrder,
		&c.IsDeleted, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Category{}, err
	}
	c.SpaceID = SpaceID(spaceID)
	c.Kind = domain.CategoryKind(kind)
	c.ParentID = Deref(parentID)
	c.KnownCategoryID = Deref(knownID)
	c.TxfID = Deref(txfID)
	return c, nil
}
