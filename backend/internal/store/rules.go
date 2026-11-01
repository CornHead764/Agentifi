package store

import (
	"context"
	"time"

	. "github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/gen/jet/household/model"
	tb "github.com/CornHead764/agentifi/backend/internal/gen/jet/household/table"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store/nullid"
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

func (s *Store) CreateRule(ctx context.Context, spaceID SpaceID, rule *Rule) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error { return tx.createRule(ctx, spaceID, rule) })
}

func (s *Store) createRule(ctx context.Context, spaceID SpaceID, rule *Rule) error {
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
	r := tb.Rules
	row := ruleRow(*rule, now)
	row.ID, row.CreatedAt = rule.ID, now
	_, err = r.INSERT(r.ID, r.CreatedAt, ruleWriteColumns).MODEL(row).ExecContext(ctx, db.Write())
	if err != nil {
		return wrap("store: create rule", err)
	}
	if err := ReplaceRuleTags(ctx, db.Write(), rule.ID, rule.AddTagIDs); err != nil {
		return err
	}
	rule.CreatedAt, rule.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	return nil
}

func (s *Store) GetRule(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Rule, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return Rule{}, err
	}
	rules, err := selectRules(ctx, db.Read(), spaceID, tb.Rules.ID.EQ(UUID(id)))
	if err == nil && len(rules) == 0 {
		err = ErrNotFound
	}
	if err != nil {
		return Rule{}, wrap("store: get rule", err)
	}
	return rules[0], nil
}

// ListRules returns rules in run order: priority, then id. The id tiebreak
// keeps equal priorities resolving the same way on every sync.
func (s *Store) ListRules(ctx context.Context, spaceID SpaceID, q RuleQuery) ([]Rule, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	r := tb.Rules
	var where []BoolExpression
	if !q.IncludeDeleted {
		where = append(where, NOT(r.IsDeleted))
	}
	if q.ActiveOnly {
		where = append(where, r.IsActive)
	}
	rules, err := selectRules(ctx, db.Read(), spaceID, where...)
	return rules, wrap("store: list rules", err)
}

// selectRules reads the rules that meet every condition in where, in run order,
// each with its tags in order, in one statement.
func selectRules(ctx context.Context, db sqlitedb.Conn, spaceID SpaceID, where ...BoolExpression) ([]Rule, error) {
	r, l := tb.Rules, tb.RuleTags
	var rows []struct {
		model.Rules
		Tags []model.RuleTags
	}
	stmt := SELECT(r.ID, r.CreatedAt, ruleWriteColumns, l.RuleID, l.TagID).
		FROM(r.LEFT_JOIN(l, l.RuleID.EQ(r.ID))).ORDER_BY(r.Priority, r.ID, l.Position)
	if len(where) > 0 {
		stmt = stmt.WHERE(AND(where...))
	}
	if err := stmt.QueryContext(ctx, db, &rows); err != nil {
		return nil, err
	}
	rules := make([]Rule, len(rows))
	for i, row := range rows {
		tags := make([]uuid.UUID, len(row.Tags))
		for j, tag := range row.Tags {
			tags[j] = tag.TagID
		}
		rules[i] = Rule{ID: row.ID, SpaceID: spaceID, Name: row.Name, FilterID: row.FilterID,
			Priority: int(row.Priority), IsActive: row.IsActive, IsDeleted: row.IsDeleted,
			SetPayee: deref(row.SetPayee), SetCategoryID: row.SetCategoryID.UUID(), AddTagIDs: tags,
			SetNotes: deref(row.SetNotes), SetExcludedFromReports: row.SetExcludedFromReports,
			SetExcludedFromSpendingPlan: row.SetExcludedFromSpendingPlan, SetIsReviewed: row.SetIsReviewed,
			CreatedAt: fromUnixMillis(row.CreatedAt), UpdatedAt: fromUnixMillis(row.UpdatedAt)}
	}
	return rules, nil
}

func (s *Store) UpdateRule(ctx context.Context, spaceID SpaceID, rule *Rule) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error { return tx.updateRule(ctx, spaceID, rule) })
}

func (s *Store) updateRule(ctx context.Context, spaceID SpaceID, rule *Rule) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	if rule.AddTagIDs == nil {
		rule.AddTagIDs = []uuid.UUID{}
	}
	now := db.Now()
	r := tb.Rules
	if err := execOne(ctx, db.Write(), "store: update rule",
		r.UPDATE(ruleWriteColumns).MODEL(ruleRow(*rule, now)).WHERE(r.ID.EQ(UUID(rule.ID)))); err != nil {
		return err
	}
	if err := ReplaceRuleTags(ctx, db.Write(), rule.ID, rule.AddTagIDs); err != nil {
		return err
	}
	rule.UpdatedAt = fromUnixMillis(now)
	return nil
}

// ruleWriteColumns are what an update writes; a create adds id and created_at.
var ruleWriteColumns = ColumnList{tb.Rules.Name, tb.Rules.FilterID, tb.Rules.Priority, tb.Rules.IsActive,
	tb.Rules.IsDeleted, tb.Rules.SetPayee, tb.Rules.SetCategoryID, tb.Rules.SetNotes,
	tb.Rules.SetExcludedFromReports, tb.Rules.SetExcludedFromSpendingPlan, tb.Rules.SetIsReviewed, tb.Rules.UpdatedAt}

// ruleRow is the rule as ruleWriteColumns write it: an unset payee, category or
// notes is NULL.
func ruleRow(rule Rule, now int64) model.Rules {
	return model.Rules{Name: rule.Name, FilterID: rule.FilterID, Priority: int64(rule.Priority),
		IsActive: rule.IsActive, IsDeleted: rule.IsDeleted, SetPayee: NullText(rule.SetPayee),
		SetCategoryID: nullid.From(rule.SetCategoryID), SetNotes: NullText(rule.SetNotes),
		SetExcludedFromReports: rule.SetExcludedFromReports, SetExcludedFromSpendingPlan: rule.SetExcludedFromSpendingPlan,
		SetIsReviewed: rule.SetIsReviewed, UpdatedAt: now}
}

// ReplaceRuleTags swaps the tags a rule adds, through a store handle or the
// importer's transaction. A repeated id keeps its first place.
func ReplaceRuleTags(ctx context.Context, db Execer, rule uuid.UUID, tags []uuid.UUID) error {
	l := tb.RuleTags
	if _, err := l.DELETE().WHERE(l.RuleID.EQ(UUID(rule))).ExecContext(ctx, db); err != nil {
		return wrap("store: replace rule_tags", err)
	}
	if len(tags) == 0 {
		return nil
	}
	insert := l.INSERT(l.RuleID, l.TagID, l.Position)
	for position, tag := range tags {
		insert = insert.MODEL(model.RuleTags{RuleID: rule, TagID: tag, Position: int64(position)})
	}
	_, err := insert.ON_CONFLICT().DO_NOTHING().ExecContext(ctx, db)
	return wrap("store: replace rule_tags", err)
}

// DeleteRule soft-deletes. The filter is left alone: filters are shared, and
// hard-deleting one would leave another surface matching everything.
func (s *Store) DeleteRule(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	r := tb.Rules
	return execOne(ctx, db.Write(), "store: delete rule",
		r.UPDATE().SET(r.IsDeleted.SET(Bool(true)), r.UpdatedAt.SET(Int(db.Now()))).WHERE(r.ID.EQ(UUID(id))))
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
		r := tb.Rules
		for priority, id := range ids {
			if err := execOne(ctx, db.Write(), "store: reorder rules",
				r.UPDATE().SET(r.Priority.SET(Int(int64(priority))), r.UpdatedAt.SET(Int(db.Now()))).
					WHERE(r.ID.EQ(UUID(id)).AND(NOT(r.IsDeleted)))); err != nil {
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
	r := tb.Rules
	var next struct{ Next int64 }
	err = SELECT(COALESCE(MAXi(r.Priority).ADD(Int(1)), Int(0)).AS("next")).FROM(r).WHERE(NOT(r.IsDeleted)).
		QueryContext(ctx, db.Read(), &next)
	return int(next.Next), wrap("store: next rule priority", err)
}
