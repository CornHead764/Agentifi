package store

import (
	"context"
	"database/sql"
	"time"

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

const ruleColumns = `id, name, filter_id, priority, is_active, is_deleted,
	set_payee, set_category_id, add_tag_ids, set_notes, set_excluded_from_reports,
	set_excluded_from_spending_plan, set_is_reviewed, created_at, updated_at`

func (s *Store) CreateRule(ctx context.Context, spaceID SpaceID, rule *Rule) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	rule.SpaceID = spaceID
	if rule.AddTagIDs == nil {
		rule.AddTagIDs = []uuid.UUID{}
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO rules (id, name, filter_id, priority, is_active, is_deleted,
			set_payee, set_category_id, add_tag_ids, set_notes, set_excluded_from_reports,
			set_excluded_from_spending_plan, set_is_reviewed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.Name, rule.FilterID, rule.Priority, rule.IsActive,
		rule.IsDeleted, NullText(rule.SetPayee), NullUUID(rule.SetCategoryID), JSONArray[uuid.UUID](rule.AddTagIDs),
		NullText(rule.SetNotes), rule.SetExcludedFromReports, rule.SetExcludedFromSpendingPlan,
		rule.SetIsReviewed, now, now)
	if err == nil {
		rule.CreatedAt, rule.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return wrap("store: create rule", err)
}

func (s *Store) GetRule(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Rule, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return Rule{}, err
	}
	rule, err := scanRule(db.Read().QueryRowContext(ctx, `SELECT `+ruleColumns+` FROM rules WHERE id = ?`, id), spaceID)
	return rule, wrap("store: get rule", err)
}

// ListRules returns rules in run order: priority, then id. The id tiebreak
// keeps equal priorities resolving the same way on every sync.
func (s *Store) ListRules(ctx context.Context, spaceID SpaceID, q RuleQuery) ([]Rule, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	sql := `SELECT ` + ruleColumns + ` FROM rules WHERE 1=1`
	if !q.IncludeDeleted {
		sql += ` AND NOT is_deleted`
	}
	if q.ActiveOnly {
		sql += ` AND is_active`
	}
	sql += ` ORDER BY priority, id`

	return sqliteQueryAll(ctx, db.Read(), "store: list rules", func(row scanner) (Rule, error) { return scanRule(row, spaceID) }, sql)
}

func (s *Store) UpdateRule(ctx context.Context, spaceID SpaceID, rule *Rule) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	if rule.AddTagIDs == nil {
		rule.AddTagIDs = []uuid.UUID{}
	}
	now := db.Now()
	err = sqliteExecOne(ctx, db.Write(), "store: update rule", `
		UPDATE rules SET name = ?, filter_id = ?, priority = ?, is_active = ?, is_deleted = ?,
			set_payee = ?, set_category_id = ?, add_tag_ids = ?, set_notes = ?,
			set_excluded_from_reports = ?, set_excluded_from_spending_plan = ?,
			set_is_reviewed = ?, updated_at = ? WHERE id = ?`,
		rule.Name, rule.FilterID, rule.Priority, rule.IsActive,
		rule.IsDeleted, NullText(rule.SetPayee), NullUUID(rule.SetCategoryID), JSONArray[uuid.UUID](rule.AddTagIDs),
		NullText(rule.SetNotes), rule.SetExcludedFromReports, rule.SetExcludedFromSpendingPlan,
		rule.SetIsReviewed, now, rule.ID)
	if err == nil {
		rule.UpdatedAt = fromUnixMillis(now)
	}
	return wrap("store: update rule", err)
}

// DeleteRule soft-deletes. The filter is left alone: filters are shared, and
// hard-deleting one would leave another surface matching everything.
func (s *Store) DeleteRule(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete rule",
		`UPDATE rules SET is_deleted = true, updated_at = ? WHERE id = ?`, db.Now(), id)
}

// ReorderRules renumbers rules to the given order in one transaction, since a
// half-applied order is one nobody chose. An id naming no live rule in the
// space is ErrNotFound rather than shifting everything after it.
func (s *Store) ReorderRules(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		for priority, id := range ids {
			if err := sqliteExecOne(ctx, db.Write(), "store: reorder rules", `
				UPDATE rules SET priority = ?, updated_at = ? WHERE id = ? AND NOT is_deleted`,
				priority, db.Now(), id); err != nil {
				return err
			}
		}
		return nil
	})
}

// NextRulePriority puts a new rule last, so it cannot outrank rules the user
// already tuned.
func (s *Store) NextRulePriority(ctx context.Context, spaceID SpaceID) (int, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	var next int
	err = db.Read().QueryRowContext(ctx,
		`SELECT coalesce(max(priority) + 1, 0) FROM rules WHERE NOT is_deleted`).Scan(&next)
	return next, wrap("store: next rule priority", err)
}

func scanRule(row scanner, spaceID SpaceID) (Rule, error) {
	rule := Rule{SpaceID: spaceID}
	var payee, notes sql.Null[string]
	var category sql.Null[uuid.UUID]
	var tags JSONArray[uuid.UUID]
	var reports, plan, reviewed sql.Null[bool]
	var created, updated int64
	err := row.Scan(&rule.ID, &rule.Name, &rule.FilterID, &rule.Priority,
		&rule.IsActive, &rule.IsDeleted, &payee, &category, &tags, &notes,
		&reports, &plan, &reviewed, &created, &updated)
	if err != nil {
		return Rule{}, err
	}
	rule.SetPayee, rule.SetNotes, rule.SetCategoryID = payee.V, notes.V, category.V
	rule.AddTagIDs = tags
	rule.SetExcludedFromReports, rule.SetExcludedFromSpendingPlan, rule.SetIsReviewed =
		nullValue(reports), nullValue(plan), nullValue(reviewed)
	rule.CreatedAt, rule.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	return rule, nil
}
