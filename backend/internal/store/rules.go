package store

import (
	"context"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

	"github.com/google/uuid"
)

// Rule has no condition columns: FilterID points at the shared Filter entity
// (ground rule 3), which internal/domain evaluates.
//
// The action flags are *bool because NULL ("leave the row alone") differs from
// false (*Include everywhere*, a real action). The two exclusions are
// independent, as in the rest of the app.
type Rule struct {
	ID       uuid.UUID
	SpaceID  SpaceID
	Name     string
	FilterID uuid.UUID
	// Priority orders the run, lowest first; the first rule to set a field wins.
	Priority  int
	IsActive  bool
	IsDeleted bool

	SetPayee      string
	SetCategoryID uuid.UUID
	AddTagIDs     []uuid.UUID
	SetNotes      string

	SetExcludedFromReports      *bool
	SetExcludedFromSpendingPlan *bool
	SetIsReviewed               *bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// RuleQuery narrows a listing. The zero value is every live rule, active and
// inactive, in run order.
type RuleQuery struct {
	IncludeDeleted bool
	// ActiveOnly is the run path, so a switched-off rule cannot fire through a
	// forgetful caller.
	ActiveOnly bool
}

const ruleColumns = `id, space_id, name, filter_id, priority, is_active, is_deleted,
	set_payee, set_category_id, add_tag_ids, set_notes, set_excluded_from_reports,
	set_excluded_from_spending_plan, set_is_reviewed, created_at, updated_at`

func (s *Store) CreateRule(ctx context.Context, spaceID SpaceID, rule *Rule) error {
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	rule.SpaceID = spaceID
	if rule.AddTagIDs == nil {
		rule.AddTagIDs = []uuid.UUID{}
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO rules (id, space_id, name, filter_id, priority, is_active, is_deleted,
			set_payee, set_category_id, add_tag_ids, set_notes, set_excluded_from_reports,
			set_excluded_from_spending_plan, set_is_reviewed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING created_at, updated_at`,
		rule.ID, spaceID.UUID(), rule.Name, rule.FilterID, rule.Priority, rule.IsActive,
		rule.IsDeleted, dbconv.NullText(rule.SetPayee), dbconv.NullUUID(rule.SetCategoryID), rule.AddTagIDs,
		dbconv.NullText(rule.SetNotes), rule.SetExcludedFromReports, rule.SetExcludedFromSpendingPlan,
		rule.SetIsReviewed,
	).Scan(&rule.CreatedAt, &rule.UpdatedAt)
	return wrap("store: create rule", err)
}

func (s *Store) GetRule(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Rule, error) {
	row := s.db.QueryRow(ctx, `SELECT `+ruleColumns+` FROM rules WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	rule, err := scanRule(row)
	return rule, wrap("store: get rule", err)
}

// ListRules returns rules in run order: priority, then id. The id tiebreak
// keeps equal priorities resolving the same way on every sync.
func (s *Store) ListRules(ctx context.Context, spaceID SpaceID, q RuleQuery) ([]Rule, error) {
	sql := `SELECT ` + ruleColumns + ` FROM rules WHERE space_id = $1`
	if !q.IncludeDeleted {
		sql += ` AND NOT is_deleted`
	}
	if q.ActiveOnly {
		sql += ` AND is_active`
	}
	sql += ` ORDER BY priority, id`

	return queryAll(ctx, s.db, "store: list rules", scanRule, sql, spaceID.UUID())
}

func (s *Store) UpdateRule(ctx context.Context, spaceID SpaceID, rule *Rule) error {
	if rule.AddTagIDs == nil {
		rule.AddTagIDs = []uuid.UUID{}
	}
	err := s.db.QueryRow(ctx, `
		UPDATE rules SET name = $3, filter_id = $4, priority = $5, is_active = $6, is_deleted = $7,
			set_payee = $8, set_category_id = $9, add_tag_ids = $10, set_notes = $11,
			set_excluded_from_reports = $12, set_excluded_from_spending_plan = $13,
			set_is_reviewed = $14, updated_at = now()
		WHERE space_id = $1 AND id = $2
		RETURNING updated_at`,
		spaceID.UUID(), rule.ID, rule.Name, rule.FilterID, rule.Priority, rule.IsActive,
		rule.IsDeleted, dbconv.NullText(rule.SetPayee), dbconv.NullUUID(rule.SetCategoryID), rule.AddTagIDs,
		dbconv.NullText(rule.SetNotes), rule.SetExcludedFromReports, rule.SetExcludedFromSpendingPlan,
		rule.SetIsReviewed,
	).Scan(&rule.UpdatedAt)
	return wrap("store: update rule", err)
}

// DeleteRule soft-deletes. The filter is left alone: filters are shared, and
// hard-deleting one would leave another surface matching everything.
func (s *Store) DeleteRule(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: delete rule",
		`UPDATE rules SET is_deleted = true, updated_at = now() WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
}

// ReorderRules renumbers rules to the given order in one transaction, since a
// half-applied order is one nobody chose. An id naming no live rule in the
// space is ErrNotFound rather than shifting everything after it.
func (s *Store) ReorderRules(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) error {
	return s.InTx(ctx, func(tx *Store) error {
		for priority, id := range ids {
			if err := tx.execOne(ctx, "store: reorder rules", `
				UPDATE rules SET priority = $3, updated_at = now()
				WHERE space_id = $1 AND id = $2 AND NOT is_deleted`,
				spaceID.UUID(), id, priority); err != nil {
				return err
			}
		}
		return nil
	})
}

// NextRulePriority puts a new rule last, so it cannot outrank rules the user
// already tuned.
func (s *Store) NextRulePriority(ctx context.Context, spaceID SpaceID) (int, error) {
	var next int
	err := s.db.QueryRow(ctx,
		`SELECT coalesce(max(priority) + 1, 0) FROM rules WHERE space_id = $1 AND NOT is_deleted`,
		spaceID.UUID()).Scan(&next)
	return next, wrap("store: next rule priority", err)
}

func scanRule(row scanner) (Rule, error) {
	var (
		rule         Rule
		spaceID      uuid.UUID
		payee, notes *string
		categoryID   *uuid.UUID
	)
	err := row.Scan(&rule.ID, &spaceID, &rule.Name, &rule.FilterID, &rule.Priority,
		&rule.IsActive, &rule.IsDeleted, &payee, &categoryID, &rule.AddTagIDs, &notes,
		&rule.SetExcludedFromReports, &rule.SetExcludedFromSpendingPlan, &rule.SetIsReviewed,
		&rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return Rule{}, err
	}
	rule.SpaceID = SpaceID(spaceID)
	rule.SetPayee = Deref(payee)
	rule.SetNotes = Deref(notes)
	rule.SetCategoryID = Deref(categoryID)
	return rule, nil
}
