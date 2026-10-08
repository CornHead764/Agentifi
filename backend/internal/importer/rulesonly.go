package importer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The rules-only import brings an export's rules into a space the full import
// already wrote, without touching anything else in it.
//
//	agentifi import export.json --rules-only --owner-email you@example.com --dry-run
//
// The space's ids are not the ones this run allocated, so a rule's category
// and tags are found again by name (a category by its full path). A rule that
// names something the space no longer has, or has twice, is refused, and a
// refusal writes nothing at all.
//
// It is safe to run again: a rule whose rules.source_ref is already in the
// space is left alone, deleted or not, and an unmarked rule from an older
// import is adopted by what it matches and renames to. A transaction rule from
// the rules file upgrades in place the rule an earlier run rebuilt from its
// rows, keeping its id so the transactions that name it still do.

// RulesOutcome is what a rules-only run did with each rule, by name.
type RulesOutcome struct {
	// Present were already in the space under their source_ref.
	Present []string
	// Adopted were already in the space without a source_ref, in the shape an
	// import writes, and now carry it.
	Adopted []string
	// Upgraded were rebuilt from their rows by an earlier run and now carry
	// Simplifi's own definition.
	Upgraded []string
	// Inserted are new.
	Inserted []string
	// Refused could not be written faithfully; each says why.
	Refused []string
}

func (o *RulesOutcome) Render() string {
	var b strings.Builder
	section := func(title string, names []string) {
		fmt.Fprintf(&b, "%s: %d\n", title, len(names))
		for _, name := range names {
			fmt.Fprintf(&b, "  %s\n", name)
		}
	}
	section("Already imported", o.Present)
	section("Adopted (imported before, now marked)", o.Adopted)
	section("Upgraded (rebuilt before, now Simplifi's own definition)", o.Upgraded)
	section("New", o.Inserted)
	section("Refused", o.Refused)
	return b.String()
}

// FindImportedSpace finds the space a full import of this export wrote: the
// owner's space of the same name, which is the fingerprint the full import
// refuses to write twice. Exactly one must match.
func FindImportedSpace(ctx context.Context, db *store.Store, mapped *Mapped) (uuid.UUID, error) {
	owner := ""
	for _, user := range mapped.Users {
		if user.Email != "" {
			owner = user.Email
			break
		}
	}
	rows, err := db.Pool().Query(ctx, `
		SELECT s.id FROM spaces s
		JOIN memberships mem ON mem.space_id = s.id
		JOIN users u ON u.id = mem.user_id
		WHERE lower(u.email) = lower($1) AND s.name = $2 AND NOT s.is_deleted`,
		owner, mapped.Space.Name)
	if err != nil {
		return uuid.Nil, fmt.Errorf("importer: finding the imported space: %w", err)
	}
	var found []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return uuid.Nil, fmt.Errorf("importer: finding the imported space: %w", err)
		}
		found = append(found, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return uuid.Nil, fmt.Errorf("importer: finding the imported space: %w", err)
	}
	switch len(found) {
	case 0:
		return uuid.Nil, fmt.Errorf("importer: %s has no space named %q; pass --owner-email and "+
			"--space-name as the full import was run, or --space-id", owner, mapped.Space.Name)
	case 1:
		return found[0], nil
	default:
		return uuid.Nil, fmt.Errorf("importer: %s has %d spaces named %q; pass --space-id",
			owner, len(found), mapped.Space.Name)
	}
}

// WriteRules writes the mapped rules into an existing space. A dry run does
// everything but commit.
func WriteRules(ctx context.Context, db *store.Store, mapped *Mapped, spaceID uuid.UUID, dryRun bool) (*RulesOutcome, error) {
	if !mapped.Report.OK() {
		return nil, fmt.Errorf("%w: %d errors; nothing was written. See the report for the store, "+
			"record and field of each", ErrRefused, len(mapped.Report.Errors))
	}
	pool := db.Pool()
	if pool == nil {
		return nil, fmt.Errorf("importer: writing needs a pool, not a transaction-scoped store")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("importer: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var live bool
	err = tx.QueryRow(ctx, `SELECT NOT is_deleted FROM spaces WHERE id = $1`, spaceID).Scan(&live)
	if errors.Is(err, sqlitedb.ErrNoRows) || (err == nil && !live) {
		return nil, fmt.Errorf("importer: no space %s", spaceID)
	}
	if err != nil {
		return nil, fmt.Errorf("importer: reading the space: %w", err)
	}

	target, err := readRuleTarget(ctx, tx, spaceID)
	if err != nil {
		return nil, err
	}
	filters := map[uuid.UUID]*store.Filter{}
	for _, filter := range mapped.Filters {
		filters[filter.ID] = filter
	}
	source := newSourceNames(mapped)

	outcome := &RulesOutcome{}
	w := &writer{ctx: ctx, tx: tx, space: spaceID}
	adopted := map[uuid.UUID]bool{}
	for _, rule := range mapped.Rules {
		if _, present := target.bySourceRef[rule.SourceRef]; present {
			outcome.Present = append(outcome.Present, rule.Name)
			continue
		}
		filter := filters[rule.FilterID]
		if filter == nil {
			outcome.Refused = append(outcome.Refused, rule.Name+": its filter was not mapped")
			continue
		}
		if rebuilt, found := target.rebuiltBy(rule); found {
			placed, why := target.place(rule, filter, source)
			if why != "" {
				outcome.Refused = append(outcome.Refused, rule.Name+": "+why)
				continue
			}
			if err := upgradeRule(ctx, tx, w, rebuilt, placed); err != nil {
				return nil, err
			}
			name := rule.Name
			if rebuilt.edited {
				name += " (it had been edited here; Simplifi's definition replaced the edit)"
			}
			outcome.Upgraded = append(outcome.Upgraded, name)
			continue
		}
		if matched, found := target.unmarkedMatch(rule, filter, adopted); found {
			adopted[matched] = true
			if _, err := tx.Exec(ctx, `UPDATE rules SET source_ref = $1, updated_at = now() WHERE id = $2`,
				rule.SourceRef, matched); err != nil {
				return nil, fmt.Errorf("importer: marking rule %q: %w", rule.Name, err)
			}
			outcome.Adopted = append(outcome.Adopted, rule.Name)
			continue
		}
		placed, why := target.place(rule, filter, source)
		if why != "" {
			outcome.Refused = append(outcome.Refused, rule.Name+": "+why)
			continue
		}
		w.filter(spaceID, placed.filter)
		w.rule(spaceID, placed.rule)
		outcome.Inserted = append(outcome.Inserted, rule.Name)
	}
	if w.err != nil {
		return nil, w.err
	}
	if len(outcome.Refused) > 0 {
		return outcome, fmt.Errorf("%w: %d rules cannot be written faithfully; nothing was written",
			ErrRefused, len(outcome.Refused))
	}
	if dryRun {
		return outcome, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("importer: commit: %w", err)
	}
	return outcome, nil
}

// ruleTarget is what the space already holds that a rule may name or repeat.
type ruleTarget struct {
	bySourceRef map[string]uuid.UUID
	existing    map[uuid.UUID]existingRule
	unmarked    []unmarkedRule
	// categories, tags and accounts map a folded name (a category's whole
	// path) to every id carrying it, so a name held twice is seen as
	// ambiguous.
	categories map[string][]uuid.UUID
	tags       map[string][]uuid.UUID
	accounts   map[string][]uuid.UUID
}

// existingRule is what an upgrade needs of a rule already in the space.
type existingRule struct {
	id, filterID uuid.UUID
	// edited is a rule changed here after the import wrote it.
	edited bool
}

// unmarkedRule is a rule with no source_ref in the one shape an import writes:
// a single statement-name clause, which a re-import adopts rather than
// writing a second copy.
type unmarkedRule struct {
	id       uuid.UUID
	operator string
	values   []string
	payee    string
}

func readRuleTarget(ctx context.Context, tx *sqlitedb.Tx, spaceID uuid.UUID) (*ruleTarget, error) {
	target := &ruleTarget{
		bySourceRef: map[string]uuid.UUID{},
		existing:    map[uuid.UUID]existingRule{},
		categories:  map[string][]uuid.UUID{},
		tags:        map[string][]uuid.UUID{},
		accounts:    map[string][]uuid.UUID{},
	}

	err := eachRow(ctx, tx, "rules", `
		SELECT r.id, r.filter_id, r.updated_at > r.created_at, r.source_ref, coalesce(r.set_payee, ''),
		       (SELECT count(*) FROM filter_items i WHERE i.filter_id = r.filter_id),
		       coalesce(i.field, ''), coalesce(i.operator, ''), coalesce(i.value_texts, '{}'),
		       coalesce(i.negated, false)
		  FROM rules r
		  LEFT JOIN filter_items i ON i.filter_id = r.filter_id
		 WHERE r.space_id = $1`, []any{spaceID}, func(rows *sqlitedb.Rows) error {
		var (
			id, filterID     uuid.UUID
			edited           bool
			sourceRef, payee string
			items            int
			field, operator  string
			values           []string
			negated          bool
		)
		if err := rows.Scan(&id, &filterID, &edited, &sourceRef, &payee, &items, &field, &operator, &values, &negated); err != nil {
			return err
		}
		target.existing[id] = existingRule{id: id, filterID: filterID, edited: edited}
		if sourceRef != "" {
			target.bySourceRef[sourceRef] = id
			return nil
		}
		if items == 1 && !negated && field == string(domain.FieldStatementName) {
			target.unmarked = append(target.unmarked, unmarkedRule{id: id, operator: operator, values: values, payee: payee})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	type node struct {
		name   string
		parent uuid.UUID
	}
	nodes := map[uuid.UUID]node{}
	err = eachRow(ctx, tx, "categories", `
		SELECT id, name, coalesce(parent_id, '00000000-0000-0000-0000-000000000000')
		  FROM categories WHERE space_id = $1 AND NOT is_deleted`, []any{spaceID}, func(rows *sqlitedb.Rows) error {
		var id, parent uuid.UUID
		var name string
		if err := rows.Scan(&id, &name, &parent); err != nil {
			return err
		}
		nodes[id] = node{name, parent}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for id := range nodes {
		path := categoryPath(id, func(at uuid.UUID) (string, uuid.UUID, bool) {
			found, ok := nodes[at]
			return found.name, found.parent, ok
		})
		target.categories[path] = append(target.categories[path], id)
	}

	byName := func(into map[string][]uuid.UUID) func(*sqlitedb.Rows) error {
		return func(rows *sqlitedb.Rows) error {
			var id uuid.UUID
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
			folded := domain.FoldName(name)
			into[folded] = append(into[folded], id)
			return nil
		}
	}
	if err := eachRow(ctx, tx, "tags", `SELECT id, name FROM tags WHERE space_id = $1 AND NOT is_deleted`,
		[]any{spaceID}, byName(target.tags)); err != nil {
		return nil, err
	}
	if err := eachRow(ctx, tx, "accounts", `SELECT id, name FROM accounts WHERE space_id = $1 AND NOT is_deleted`,
		[]any{spaceID}, byName(target.accounts)); err != nil {
		return nil, err
	}
	return target, nil
}

// eachRow hands each row of a query to each, and names the table it was
// reading in any error.
func eachRow(ctx context.Context, tx *sqlitedb.Tx, what, sql string, args []any, each func(*sqlitedb.Rows) error) error {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("importer: reading the space's %s: %w", what, err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows); err != nil {
			return fmt.Errorf("importer: reading the space's %s: %w", what, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("importer: reading the space's %s: %w", what, err)
	}
	return nil
}

// categoryPath is a category's folded name and its ancestors', which is what
// identifies it across two imports: "Food / Groceries" and "Gifts / Groceries"
// are different categories with the same name.
func categoryPath(id uuid.UUID, lookup func(uuid.UUID) (string, uuid.UUID, bool)) string {
	parts := domain.CategoryPath(id, lookup)
	for i, part := range parts {
		parts[i] = domain.FoldName(part)
	}
	return strings.Join(parts, "\x00")
}

// rebuiltBy finds the rule an earlier run rebuilt for the Simplifi rule this
// definition is.
func (t *ruleTarget) rebuiltBy(rule *Rule) (existingRule, bool) {
	simplifiID, isDefinition := strings.CutPrefix(rule.SourceRef, sourceRefTransactionRuleDefinition)
	if !isDefinition {
		return existingRule{}, false
	}
	id, found := t.bySourceRef[sourceRefTransactionRule+simplifiID]
	if !found {
		return existingRule{}, false
	}
	return t.existing[id], true
}

// upgradeRule rewrites a rebuilt rule in place as the definition placed for
// it. A rule deleted here stays deleted.
func upgradeRule(ctx context.Context, tx *sqlitedb.Tx, w *writer, rebuilt existingRule, placed placedRule) error {
	rule := placed.rule
	if _, err := tx.Exec(ctx, `DELETE FROM filter_items WHERE filter_id = $1`, rebuilt.filterID); err != nil {
		return fmt.Errorf("importer: upgrading rule %q: %w", rule.Name, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE filters SET name = $2, updated_at = now() WHERE id = $1`,
		rebuilt.filterID, dbconv.NullText(placed.filter.Name)); err != nil {
		return fmt.Errorf("importer: upgrading rule %q: %w", rule.Name, err)
	}
	items := slices.Clone(placed.filter.Items)
	for i := range items {
		items[i].FilterID = rebuilt.filterID
	}
	w.filterItems(w.space, items)
	if w.err != nil {
		return w.err
	}
	_, err := tx.Exec(ctx, `
		UPDATE rules SET name = $2, priority = $3, is_active = $4, is_deleted = is_deleted OR $5,
		       source_ref = $6, set_payee = $7, set_category_id = $8, add_tag_ids = $9, set_notes = $10,
		       set_excluded_from_reports = $11, set_excluded_from_spending_plan = $12,
		       set_is_reviewed = $13, updated_at = now()
		 WHERE id = $1`,
		rebuilt.id, rule.Name, rule.Priority, rule.IsActive, rule.IsDeleted,
		rule.SourceRef, dbconv.NullText(rule.SetPayee), dbconv.NullUUID(rule.SetCategoryID), store.NonNil(rule.AddTagIDs),
		dbconv.NullText(rule.SetNotes), rule.SetExcludedFromReports, rule.SetExcludedFromSpendingPlan,
		rule.SetIsReviewed)
	if err != nil {
		return fmt.Errorf("importer: upgrading rule %q: %w", rule.Name, err)
	}
	return nil
}

// unmarkedMatch finds an unmarked rule that is this one: the same single
// statement-name clause and the same rename.
func (t *ruleTarget) unmarkedMatch(rule *Rule, filter *store.Filter, taken map[uuid.UUID]bool) (uuid.UUID, bool) {
	if len(filter.Items) != 1 {
		return uuid.Nil, false
	}
	item := filter.Items[0]
	if item.Field != string(domain.FieldStatementName) || item.Negated {
		return uuid.Nil, false
	}
	want := foldedSet(item.ValueTexts)
	for _, candidate := range t.unmarked {
		if taken[candidate.id] || candidate.operator != item.Operator || candidate.payee != rule.SetPayee {
			continue
		}
		if slices.Equal(foldedSet(candidate.values), want) {
			return candidate.id, true
		}
	}
	return uuid.Nil, false
}

func foldedSet(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, domain.FoldName(value))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// sourceNames reads the mapped categories and tags back to the names the space
// knows them by.
type sourceNames struct {
	categoryPaths map[uuid.UUID]string
	tagNames      map[uuid.UUID]string
	accountNames  map[uuid.UUID]string
}

func newSourceNames(mapped *Mapped) *sourceNames {
	byID := map[uuid.UUID]*store.Category{}
	for _, category := range mapped.Categories {
		byID[category.ID] = category
	}
	names := &sourceNames{
		categoryPaths: map[uuid.UUID]string{},
		tagNames:      map[uuid.UUID]string{},
		accountNames:  map[uuid.UUID]string{},
	}
	for id := range byID {
		names.categoryPaths[id] = categoryPath(id, func(at uuid.UUID) (string, uuid.UUID, bool) {
			found, ok := byID[at]
			if !ok {
				return "", uuid.Nil, false
			}
			return found.Name, found.ParentID, true
		})
	}
	for _, tag := range mapped.Tags {
		names.tagNames[tag.ID] = tag.Name
	}
	for _, account := range mapped.Accounts {
		names.accountNames[account.ID] = account.Name
	}
	return names
}

type placedRule struct {
	rule   *Rule
	filter *store.Filter
}

// place rewrites a mapped rule onto the space's ids, fresh ids for the rule
// and its filter, or says why it cannot be written faithfully.
func (t *ruleTarget) place(rule *Rule, filter *store.Filter, source *sourceNames) (placedRule, string) {
	for _, item := range filter.Items {
		if len(item.ValueIDs) > 0 && item.Field != string(domain.FieldAccount) && item.Field != string(domain.FieldCategory) {
			return placedRule{}, fmt.Sprintf("its %s condition names records by id, "+
				"which a rules-only import cannot find again", item.Field)
		}
	}
	copied := *rule
	copied.ID = uuid.New()
	copied.FilterID = uuid.New()

	if rule.SetCategoryID != uuid.Nil {
		path, known := source.categoryPaths[rule.SetCategoryID]
		if !known {
			return placedRule{}, "its category was not mapped"
		}
		found := t.categories[path]
		if len(found) != 1 {
			return placedRule{}, fmt.Sprintf("the space has %d categories named %q",
				len(found), strings.ReplaceAll(path, "\x00", " / "))
		}
		copied.SetCategoryID = found[0]
	}
	copied.AddTagIDs = nil
	for _, tag := range rule.AddTagIDs {
		name := source.tagNames[tag]
		found := t.tags[domain.FoldName(name)]
		if len(found) != 1 {
			return placedRule{}, fmt.Sprintf("the space has %d tags named %q", len(found), name)
		}
		copied.AddTagIDs = append(copied.AddTagIDs, found[0])
	}

	placed := *filter
	placed.ID = copied.FilterID
	placed.Items = make([]store.FilterItem, len(filter.Items))
	for i, item := range filter.Items {
		item.ID = uuid.New()
		item.FilterID = placed.ID
		ids, why := t.placeIDs(item, source)
		if why != "" {
			return placedRule{}, why
		}
		item.ValueIDs = ids
		placed.Items[i] = item
	}
	return placedRule{rule: &copied, filter: &placed}, ""
}

// placeIDs finds an account or category condition's records in the space, an
// account by its name and a category by its path.
func (t *ruleTarget) placeIDs(item store.FilterItem, source *sourceNames) ([]uuid.UUID, string) {
	if len(item.ValueIDs) == 0 {
		return item.ValueIDs, ""
	}
	out := make([]uuid.UUID, 0, len(item.ValueIDs))
	for _, id := range item.ValueIDs {
		var name string
		var found []uuid.UUID
		if item.Field == string(domain.FieldAccount) {
			name = source.accountNames[id]
			found = t.accounts[domain.FoldName(name)]
		} else {
			path, known := source.categoryPaths[id]
			if !known {
				return nil, "its category condition was not mapped"
			}
			name = strings.ReplaceAll(path, "\x00", " / ")
			found = t.categories[path]
		}
		if name == "" || len(found) != 1 {
			return nil, fmt.Sprintf("the space has %d %ss named %q", len(found), item.Field, name)
		}
		out = append(out, found[0])
	}
	return out, ""
}
