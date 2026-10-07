package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Rule conditions are the shared Filter, evaluated by domain.Matches. A rule
// differs from a register filter in two ways: an empty condition set matches
// nothing, and a field a rule cannot evaluate is refused when the rule is
// prepared rather than silently matching nothing. Existing rows are changed
// only through PreviewRule then ApplyChanges.

type Rules struct{ base }

func NewRules(st *store.Store) *Rules { return &Rules{newBase(st)} }

// RuleCandidate is a transaction as the rules read it: the Posting is what
// domain.Matches evaluates, the rest is what the diff needs.
type RuleCandidate struct {
	ID      uuid.UUID
	Posting domain.Posting
	Facets  domain.Facets
	Notes   string
}

// RuleActions is what a matching rule changes. Each flag has a Has* partner
// because setting a flag to false is a real action, distinct from leaving it
// alone; the string and id actions use their zero value for "leave alone".
type RuleActions struct {
	SetPayee      string
	SetCategoryID uuid.UUID
	AddTagIDs     []uuid.UUID
	SetNotes      string

	ExcludedFromReports         bool
	HasExcludedFromReports      bool
	ExcludedFromSpendingPlan    bool
	HasExcludedFromSpendingPlan bool
	IsReviewed                  bool
	HasIsReviewed               bool
}

// PreparedRule is a rule with its conditions resolved, ready to run with no
// database.
type PreparedRule struct {
	ID       uuid.UUID
	Name     string
	Filter   domain.Filter
	Actions  RuleActions
	Priority int
}

// RuleChange is what one rule run would do to one row. Only fields whose
// value actually moves are set.
type RuleChange struct {
	TransactionID uuid.UUID
	// RuleID is the first rule that matched.
	RuleID uuid.UUID

	Payee      string
	HasPayee   bool
	CategoryID uuid.UUID
	// A rule can set a category but never clear one.
	HasCategoryID bool
	Notes         string
	HasNotes      bool

	ExcludedFromReports         bool
	HasExcludedFromReports      bool
	ExcludedFromSpendingPlan    bool
	HasExcludedFromSpendingPlan bool
	IsReviewed                  bool
	HasIsReviewed               bool

	AddTagIDs []uuid.UUID
}

func (c RuleChange) IsEmpty() bool {
	return !c.HasPayee && !c.HasCategoryID && !c.HasNotes && !c.HasExcludedFromReports &&
		!c.HasExcludedFromSpendingPlan && !c.HasIsReviewed && len(c.AddTagIDs) == 0
}

type RulePreview struct {
	RuleID  uuid.UUID
	Matched int
	Changes []RuleChange
	// Rows is the matched candidates.
	Rows []RuleCandidate
}

func (p RulePreview) Unchanged() int { return p.Matched - len(p.Changes) }

// MatchesRule reports whether a rule fires on one row. No conditions means no
// match, unlike a register filter: an empty rule would otherwise rewrite every
// transaction in the space. A date condition reads the effective date (trap
// 4), as automations and guidance notes do, so a card charge posted late in
// one month and paid in the next falls to the next month's rule.
func MatchesRule(rule PreparedRule, row RuleCandidate) bool {
	if len(rule.Filter.Items) == 0 {
		return false
	}
	return domain.Matches(rule.Filter, row.Posting, row.Facets, domain.DateEffective)
}

// MergeNotes appends a rule's note unless the notes already contain it, so a
// rule running twice leaves the notes as the first run did.
func MergeNotes(existing, incoming string) string {
	adding := strings.TrimSpace(incoming)
	if adding == "" {
		return existing
	}
	kept := strings.TrimSpace(existing)
	switch {
	case kept == "":
		return adding
	case strings.Contains(kept, adding):
		return kept
	default:
		return kept + " " + adding
	}
}

// PlanChanges is what running these rules over these rows would change.
// Rules run in priority order, lowest first, and the first rule to set a field
// wins, so conflicting rules resolve the same way on every sync. Tags and
// notes accumulate instead.
func PlanChanges(rules []PreparedRule, rows []RuleCandidate) []RuleChange {
	ordered := make([]PreparedRule, len(rules))
	copy(ordered, rules)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Priority != ordered[j].Priority {
			return ordered[i].Priority < ordered[j].Priority
		}
		return lessUUID(ordered[i].ID, ordered[j].ID)
	})

	var changes []RuleChange
	for _, row := range rows {
		if change, ok := planRow(ordered, row); ok {
			changes = append(changes, change)
		}
	}
	return changes
}

func planRow(rules []PreparedRule, row RuleCandidate) (RuleChange, bool) {
	txn := row.Posting.Txn
	change := RuleChange{TransactionID: row.ID}
	notes := row.Notes
	matched := false

	for _, rule := range rules {
		if !MatchesRule(rule, row) {
			continue
		}
		if !matched {
			change.RuleID, matched = rule.ID, true
		}
		actions := rule.Actions

		if actions.SetPayee != "" && !change.HasPayee && actions.SetPayee != txn.Payee {
			change.Payee, change.HasPayee = actions.SetPayee, true
		}
		// A split row's categories are its splits'; a rule's one category
		// would sit on the parent beside them.
		if actions.SetCategoryID != uuid.Nil && !change.HasCategoryID && len(txn.Splits) == 0 &&
			domain.ID(actions.SetCategoryID.String()) != txn.CategoryID {
			change.CategoryID, change.HasCategoryID = actions.SetCategoryID, true
		}
		for _, tagID := range actions.AddTagIDs {
			if !hasDomainID(txn.TagIDs, tagID) && !hasUUID(change.AddTagIDs, tagID) {
				change.AddTagIDs = append(change.AddTagIDs, tagID)
			}
		}
		if actions.SetNotes != "" {
			notes = MergeNotes(notes, actions.SetNotes)
		}
		setFlag(&change.ExcludedFromReports, &change.HasExcludedFromReports,
			actions.ExcludedFromReports, actions.HasExcludedFromReports, txn.ExcludedFromReports)
		setFlag(&change.ExcludedFromSpendingPlan, &change.HasExcludedFromSpendingPlan,
			actions.ExcludedFromSpendingPlan, actions.HasExcludedFromSpendingPlan,
			txn.ExcludedFromSpendingPlan)
		setFlag(&change.IsReviewed, &change.HasIsReviewed,
			actions.IsReviewed, actions.HasIsReviewed, txn.IsReviewed)
	}

	if !matched {
		return RuleChange{}, false
	}
	if notes != row.Notes {
		change.Notes, change.HasNotes = notes, true
	}
	if change.IsEmpty() {
		return RuleChange{}, false
	}
	return change, true
}

func setFlag(field *bool, has *bool, wanted, wantedSet, current bool) {
	if !wantedSet || *has || wanted == current {
		return
	}
	*field, *has = wanted, true
}

func hasUUID(ids []uuid.UUID, want uuid.UUID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func hasDomainID(ids []domain.ID, want uuid.UUID) bool {
	wanted := domain.ID(want.String())
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

// LoadRules returns every active, undeleted rule in the space with its
// conditions resolved.
func (r *Rules) LoadRules(ctx context.Context, spaceID store.SpaceID) ([]PreparedRule, error) {
	rows, err := r.conn().Query(ctx, `
		SELECT id, name, filter_id, priority, set_payee, set_category_id, add_tag_ids, set_notes,
			set_excluded_from_reports, set_excluded_from_spending_plan, set_is_reviewed
		FROM rules
		WHERE space_id = $1 AND is_active AND NOT is_deleted
		ORDER BY priority, id`, spaceID.UUID())
	if err != nil {
		return nil, fmt.Errorf("service: load rules: %w", err)
	}
	defer rows.Close()

	type ruleRow struct {
		prepared PreparedRule
		filterID uuid.UUID
	}
	var loaded []ruleRow
	for rows.Next() {
		var (
			row                                     ruleRow
			payee, notes                            *string
			categoryID                              *uuid.UUID
			excludeReports, excludePlan, isReviewed *bool
		)
		err := rows.Scan(&row.prepared.ID, &row.prepared.Name, &row.filterID,
			&row.prepared.Priority, &payee, &categoryID, &row.prepared.Actions.AddTagIDs,
			&notes, &excludeReports, &excludePlan, &isReviewed)
		if err != nil {
			return nil, fmt.Errorf("service: load rules: %w", err)
		}
		row.prepared.Actions.SetPayee = stringOrEmpty(payee)
		row.prepared.Actions.SetNotes = stringOrEmpty(notes)
		row.prepared.Actions.SetCategoryID = pgconv.ReadNullUUID(categoryID)
		row.prepared.Actions.ExcludedFromReports, row.prepared.Actions.HasExcludedFromReports =
			readTriState(excludeReports)
		row.prepared.Actions.ExcludedFromSpendingPlan,
			row.prepared.Actions.HasExcludedFromSpendingPlan = readTriState(excludePlan)
		row.prepared.Actions.IsReviewed, row.prepared.Actions.HasIsReviewed =
			readTriState(isReviewed)
		loaded = append(loaded, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("service: load rules: %w", err)
	}
	if len(loaded) == 0 {
		return nil, nil
	}

	filters, err := r.filtersByID(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	out := make([]PreparedRule, 0, len(loaded))
	for _, row := range loaded {
		rule, err := prepareRule(row.prepared, filters[row.filterID])
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

func (r *Rules) LoadRule(
	ctx context.Context, spaceID store.SpaceID, ruleID uuid.UUID,
) (PreparedRule, error) {
	var (
		prepared                                PreparedRule
		filterID                                uuid.UUID
		payee, notes                            *string
		categoryID                              *uuid.UUID
		excludeReports, excludePlan, isReviewed *bool
	)
	err := r.conn().QueryRow(ctx, `
		SELECT id, name, filter_id, priority, set_payee, set_category_id, add_tag_ids, set_notes,
			set_excluded_from_reports, set_excluded_from_spending_plan, set_is_reviewed
		FROM rules WHERE space_id = $1 AND id = $2`, spaceID.UUID(), ruleID).
		Scan(&prepared.ID, &prepared.Name, &filterID, &prepared.Priority, &payee, &categoryID,
			&prepared.Actions.AddTagIDs, &notes, &excludeReports, &excludePlan, &isReviewed)
	if err != nil {
		return PreparedRule{}, fmt.Errorf("service: load rule %s: %w", ruleID, err)
	}
	prepared.Actions.SetPayee = stringOrEmpty(payee)
	prepared.Actions.SetNotes = stringOrEmpty(notes)
	prepared.Actions.SetCategoryID = pgconv.ReadNullUUID(categoryID)
	prepared.Actions.ExcludedFromReports, prepared.Actions.HasExcludedFromReports =
		readTriState(excludeReports)
	prepared.Actions.ExcludedFromSpendingPlan, prepared.Actions.HasExcludedFromSpendingPlan =
		readTriState(excludePlan)
	prepared.Actions.IsReviewed, prepared.Actions.HasIsReviewed = readTriState(isReviewed)

	filters, err := r.filtersByID(ctx, spaceID)
	if err != nil {
		return PreparedRule{}, err
	}
	return prepareRule(prepared, filters[filterID])
}

// filtersByID loads every filter in the space, deleted ones included: a rule
// pointing at a soft-deleted filter still has conditions.
func (r *Rules) filtersByID(
	ctx context.Context, spaceID store.SpaceID,
) (map[uuid.UUID]store.Filter, error) {
	filters, err := r.store.ListFilters(ctx, spaceID, true)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]store.Filter, len(filters))
	for _, filter := range filters {
		byID[filter.ID] = filter
	}
	return byID, nil
}

func prepareRule(rule PreparedRule, stored store.Filter) (PreparedRule, error) {
	filter := store.DomainFilter(stored)
	if err := filter.Validate(); err != nil {
		return PreparedRule{}, fmt.Errorf("service: rule %s: %w", rule.ID, err)
	}
	for i := range filter.Items {
		item := &filter.Items[i]
		if domain.RuleCannotMatch(item.Field) {
			return PreparedRule{}, fmt.Errorf(
				"service: rule %s cannot match on %s", rule.ID, item.Field)
		}
		normalizeRuleTextOperator(item)
	}
	rule.Filter = filter
	return rule, nil
}

// normalizeRuleTextOperator reads an unset operator on a name field as
// Contains. The evaluator's default is an exact any-of match, but the rules
// builder only produces keyword chips, so a rule stored without an operator
// would otherwise stop firing.
func normalizeRuleTextOperator(item *domain.FilterItem) {
	if item.Operator != "" {
		return
	}
	if item.Field == domain.FieldPayee || item.Field == domain.FieldStatementName {
		item.Operator = domain.OpContains
	}
}

func readTriState(value *bool) (bool, bool) {
	if value == nil {
		return false, false
	}
	return *value, true
}

type CandidateQuery struct {
	// TransactionIDs nil means the whole space.
	TransactionIDs []uuid.UUID
	// Since bounds the preview to recent history.
	Since domain.Date
}

// LoadCandidates returns the rows a rule run may touch: every undeleted row,
// manual ones included, unlike transfer pairing.
func (r *Rules) LoadCandidates(
	ctx context.Context, spaceID store.SpaceID, q CandidateQuery,
) ([]RuleCandidate, error) {
	var txns []store.Transaction
	if q.TransactionIDs != nil {
		if len(q.TransactionIDs) == 0 {
			return nil, nil
		}
		// One read per row. store.TransactionQuery.IDs could fetch these in
		// one query, but ListTransactions also drops estimates by default.
		for _, id := range q.TransactionIDs {
			txn, err := r.store.GetTransaction(ctx, spaceID, id)
			if errors.Is(err, store.ErrNotFound) {
				// Deleted between the sync writing it and the rules running.
				continue
			}
			if err != nil {
				return nil, err
			}
			if txn.IsDeleted {
				continue
			}
			txns = append(txns, txn)
		}
	} else {
		var err error
		txns, err = r.store.ListTransactions(ctx, spaceID, store.TransactionQuery{From: q.Since})
		if err != nil {
			return nil, err
		}
	}
	if len(txns) == 0 {
		return nil, nil
	}
	return r.candidates(ctx, spaceID, txns)
}

func (r *Rules) candidates(
	ctx context.Context, spaceID store.SpaceID, txns []store.Transaction,
) ([]RuleCandidate, error) {
	accounts, err := r.store.ListAccounts(ctx, spaceID,
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	categories, err := r.store.ListCategories(ctx, spaceID, true)
	if err != nil {
		return nil, err
	}
	postings, err := store.BuildPostings(txns, accounts, categories)
	if err != nil {
		return nil, err
	}

	out := make([]RuleCandidate, 0, len(txns))
	for i, txn := range txns {
		facets := store.Facets(txn)
		out = append(out, RuleCandidate{
			ID:      txn.ID,
			Posting: postings[i],
			Facets:  domain.Facets(facets),
			Notes:   txn.Notes,
		})
	}
	return out, nil
}

// PreviewRule reports what this rule would do to existing transactions. It
// writes nothing.
func (r *Rules) PreviewRule(
	ctx context.Context, spaceID store.SpaceID, ruleID uuid.UUID, query CandidateQuery,
) (RulePreview, error) {
	rule, err := r.LoadRule(ctx, spaceID, ruleID)
	if err != nil {
		return RulePreview{}, err
	}
	rows, err := r.LoadCandidates(ctx, spaceID, query)
	if err != nil {
		return RulePreview{}, err
	}
	var matched []RuleCandidate
	for _, row := range rows {
		if MatchesRule(rule, row) {
			matched = append(matched, row)
		}
	}
	return RulePreview{
		RuleID:  ruleID,
		Matched: len(matched),
		Changes: PlanChanges([]PreparedRule{rule}, matched),
		Rows:    matched,
	}, nil
}

// ApplyChanges commits a planned diff and returns the number of rows touched.
// It takes the approved changes rather than re-deriving them, so rows that
// arrived after the preview are not swept in.
func (r *Rules) ApplyChanges(
	ctx context.Context, spaceID store.SpaceID, changes []RuleChange,
) (int, error) {
	touched := 0
	err := r.inTx(ctx, func(tx *store.Store) error {
		for _, change := range changes {
			if change.IsEmpty() {
				continue
			}
			if err := applyChange(ctx, tx.Conn(), spaceID, change); err != nil {
				return err
			}
			touched++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return touched, nil
}

func applyChange(ctx context.Context, tx pgConn, spaceID store.SpaceID, change RuleChange) error {
	sets := []string{"rule_id = $3", "updated_at = now()"}
	args := []any{spaceID.UUID(), change.TransactionID, change.RuleID}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if change.HasPayee {
		add("payee", change.Payee)
	}
	if change.HasCategoryID {
		add("category_id", change.CategoryID)
	}
	if change.HasNotes {
		add("notes", pgconv.NullText(change.Notes))
	}
	if change.HasExcludedFromReports {
		add("excluded_from_reports", change.ExcludedFromReports)
	}
	if change.HasExcludedFromSpendingPlan {
		add("excluded_from_spending_plan", change.ExcludedFromSpendingPlan)
	}
	if change.HasIsReviewed {
		add("is_reviewed", change.IsReviewed)
	}

	_, err := tx.Exec(ctx,
		`UPDATE transactions SET `+strings.Join(sets, ", ")+
			` WHERE space_id = $1 AND id = $2`, args...)
	if err != nil {
		return fmt.Errorf("service: apply rule change: %w", err)
	}

	for _, tagID := range change.AddTagIDs {
		// The join table has no space id; selecting through tags keeps a
		// tag from another space from being attached.
		_, err := tx.Exec(ctx, `
			INSERT INTO transaction_tags (transaction_id, tag_id)
			SELECT $2, id FROM tags WHERE space_id = $1 AND id = $3
			ON CONFLICT DO NOTHING`, spaceID.UUID(), change.TransactionID, tagID)
		if err != nil {
			return fmt.Errorf("service: tag transaction: %w", err)
		}
	}
	return nil
}

// RunRules applies every live rule to rows that just arrived from a sync or
// import; new rows have no user edits to overwrite, so no preview is needed.
func (r *Rules) RunRules(
	ctx context.Context, spaceID store.SpaceID, transactionIDs []uuid.UUID,
) (int, error) {
	if len(transactionIDs) == 0 {
		return 0, nil
	}
	rules, err := r.LoadRules(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	if len(rules) == 0 {
		return 0, nil
	}
	rows, err := r.LoadCandidates(ctx, spaceID, CandidateQuery{TransactionIDs: transactionIDs})
	if err != nil {
		return 0, err
	}
	return r.ApplyChanges(ctx, spaceID, PlanChanges(rules, rows))
}
