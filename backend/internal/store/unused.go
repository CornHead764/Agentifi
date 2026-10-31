package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/migrations/household"
)

// Finding the categories and tags nothing uses, and deleting them.
//
// The difficulty is the inventory below. Some references carry no foreign key
// at all — the category in a series' split template, the assistant's pending
// proposals — and the keys that exist are ON DELETE SET NULL or CASCADE, which
// empties a rule rather than failing. A new reference to categories(id) or
// tags(id) must be added here.
//
// A saved Filter (one something live points at — rule, watchlist, envelope,
// report, guidance note, automation) is a use only when an item names the id
// narrowly: one id, or at most half of the space's live categories (or tags).
// Broad items — "Uncategorized" stored as `not any of [every category]`, or
// Simplifi's "everything except X" — would otherwise make nothing unused; they
// get the purged ids dropped instead, which changes nothing they match. Ad hoc
// register searches are not uses.
//
// Subcategories are the caller's question (whether a child goes in the same
// batch); see prunableCategories in the api package.
//
// A transaction is a use only once it is reviewed, deleted or not: an
// unreviewed row is the backlog, and its category or tag is still a guess.
// Purging takes a tag off the unreviewed rows carrying it, and sends an
// unreviewed row an automation filed back to uncategorized so the automation
// can be asked again (see PruneCategories). Splits are uses whatever their
// row's state.
//
// Soft-deleted rules, series, watchlists, goals and guidance notes are not
// uses.

// ErrPurgeEmptiesFilter refuses a purge that would leave an item in a saved
// filter naming nothing at all. An empty membership item selects nothing and a
// negated one selects everything, so emptying one rewrites whatever owns it.
var ErrPurgeEmptiesFilter = errors.New("store: a saved filter would be left naming nothing")

// uuidPattern matches a uuid written as text, so a malformed one is skipped
// rather than failing the cast for the whole query.
const uuidPattern = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

type useProbe struct {
	label string
	sql   string
}

// filterOwners are the predicates, over the filter row aliased f, that make a
// filter saved rather than a throwaway search.
var filterOwners = []struct {
	label string
	where string
}{
	{"rule conditions", `EXISTS (SELECT 1 FROM rules o WHERE o.filter_id = f.id AND NOT o.is_deleted)`},
	{"watchlists", `EXISTS (SELECT 1 FROM watchlists o WHERE o.filter_id = f.id AND NOT o.is_deleted)`},
	{"planned-spend envelopes", `EXISTS (SELECT 1 FROM envelopes o WHERE o.filter_id = f.id)`},
	{"saved reports", `f.scope IN ('report', 'saved_view')`},
	{"assistant guidance", `EXISTS (SELECT 1 FROM assistant_guidance o
		WHERE o.filter_id = f.id AND NOT o.is_deleted)`},
	{"automation triggers", `EXISTS (SELECT 1 FROM assistant_automations o WHERE o.filter_id = f.id)`},
}

func savedFilter() string {
	out := "(NOT f.is_deleted AND ("
	for i, owner := range filterOwners {
		if i > 0 {
			out += " OR "
		}
		out += owner.where
	}
	return out + "))"
}

// filterUseProbes asks, per owner, which ids a narrow item of a saved filter
// names. table is the universe the "at most half" is measured against.
func filterUseProbes(field domain.FilterField, table string) []useProbe {
	column := household.FilterValueColumn(string(field))
	out := make([]useProbe, 0, len(filterOwners))
	for _, owner := range filterOwners {
		out = append(out, useProbe{owner.label, fmt.Sprintf(`SELECT u.%s AS id
			FROM filters f
			JOIN filter_items i ON i.filter_id = f.id
			JOIN filter_item_values u ON u.filter_item_id = i.id
			WHERE NOT f.is_deleted AND i.field = '%s' AND (%s)
			  AND ((SELECT count(*) FROM filter_item_values n WHERE n.filter_item_id = i.id) * 2 <=
			       (SELECT count(*) FROM %s WHERE NOT is_deleted)
			    OR (SELECT count(*) FROM filter_item_values n WHERE n.filter_item_id = i.id) = 1)`,
			column, field, owner.where, table)})
	}
	return out
}

// pendingProposalProbe is every id a waiting proposal carries anywhere — the
// request, the edited body, the preview or its path. The shape varies by tool,
// so it reads every string in the documents rather than a known key.
var pendingProposalProbe = useProbe{"pending assistant proposals", `
	SELECT v.value AS id FROM assistant_actions a,
	     json_tree(json_array(json(a.body), json(a.proposed_body), json(a.preview))) v
	WHERE a.status = '` + domain.AssistantActionPending + `' AND v.type = 'text'
	UNION
	SELECT 'path:' || a.path FROM assistant_actions a WHERE a.status = '` + domain.AssistantActionPending + `'`}

// categoryUseProbes is every place a category id is written down, with what a
// screen calls it. Each selects one uuid column named id.
var categoryUseProbes = slices.Concat([]useProbe{
	{"reviewed transactions", `SELECT category_id AS id FROM transactions WHERE category_id IS NOT NULL AND is_reviewed`},
	{"splits", `SELECT category_id AS id FROM transaction_splits WHERE category_id IS NOT NULL`},
	{"rule actions", `SELECT set_category_id AS id FROM rules WHERE NOT is_deleted AND set_category_id IS NOT NULL`},
	{"recurring series and reminders", `SELECT category_id AS id FROM series WHERE NOT is_deleted AND category_id IS NOT NULL`},
	{"split templates", `SELECT json_extract(e.value, '$.category_id') AS id
		FROM series s, json_each(CASE WHEN json_type(s.template_splits) = 'array' THEN s.template_splits ELSE '[]' END) e
		WHERE NOT s.is_deleted`},
	{"mail rules", `SELECT category_id AS id FROM mail_rules WHERE category_id IS NOT NULL
		UNION SELECT income_category_id FROM mail_rules WHERE income_category_id IS NOT NULL`},
}, filterUseProbes(domain.FieldCategory, "categories"), []useProbe{
	pendingProposalProbe,
	{"the assistant's runs", `SELECT expected_category_id AS id FROM assistant_automation_runs WHERE expected_category_id IS NOT NULL`},
	{"the assistant's corrections", `SELECT proposed_category_id AS id FROM assistant_corrections WHERE proposed_category_id IS NOT NULL
		UNION SELECT chosen_category_id FROM assistant_corrections WHERE chosen_category_id IS NOT NULL`},
})

var tagUseProbes = slices.Concat([]useProbe{
	{"reviewed transactions", `SELECT l.tag_id AS id FROM transaction_tags l
		JOIN transactions x ON x.id = l.transaction_id WHERE x.is_reviewed`},
	{"splits", `SELECT tag_id AS id FROM split_tags`},
	{"rule actions", `SELECT t.tag_id AS id FROM rule_tags t JOIN rules r ON r.id = t.rule_id WHERE NOT r.is_deleted`},
	{"recurring series and reminders", `SELECT t.tag_id AS id FROM series_tags t JOIN series s ON s.id = t.series_id WHERE NOT s.is_deleted`},
	{"split templates", `SELECT t.tag_id AS id FROM series_split_tags t JOIN series s ON s.id = t.series_id WHERE NOT s.is_deleted`},
	{"goals", `SELECT tag_id AS id FROM goals WHERE NOT is_deleted AND tag_id IS NOT NULL`},
}, filterUseProbes(domain.FieldTag, "tags"), []useProbe{pendingProposalProbe})

func labels(probes []useProbe) []string {
	out := make([]string, 0, len(probes))
	for _, probe := range probes {
		out = append(out, probe.label)
	}
	return out
}

// CategoryUseLabels names what CategoryUses looks at, so a screen can say what
// "unused" covered rather than repeating the list and drifting from it.
func CategoryUseLabels() []string { return labels(categoryUseProbes) }

func TagUseLabels() []string { return labels(tagUseProbes) }

// CategoryUses maps each category id to the labels of the places naming it; an
// absent category is referred to by nothing. A non-empty `only` narrows to
// those ids.
func (s *Store) CategoryUses(
	ctx context.Context, spaceID SpaceID, only []uuid.UUID,
) (map[uuid.UUID][]string, error) {
	return s.uses(ctx, spaceID, "category", categoryUseProbes, only)
}

func (s *Store) TagUses(
	ctx context.Context, spaceID SpaceID, only []uuid.UUID,
) (map[uuid.UUID][]string, error) {
	return s.uses(ctx, spaceID, "tag", tagUseProbes, only)
}

func (s *Store) uses(
	ctx context.Context, spaceID SpaceID, noun string, probes []useProbe, only []uuid.UUID,
) (map[uuid.UUID][]string, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	uses := map[uuid.UUID][]string{}
	pattern := regexp.MustCompile(uuidPattern)
	for _, probe := range probes {
		what := "store: " + noun + " uses (" + probe.label + ")"
		values, err := sqliteQueryAll(ctx, db.Read(), what, scanValue[string],
			`SELECT DISTINCT q.id FROM (`+probe.sql+`) q WHERE q.id IS NOT NULL`)
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			candidates := []string{value}
			if probe.label == pendingProposalProbe.label && strings.HasPrefix(value, "path:") {
				candidates = pattern.FindAllString(value, -1)
			}
			for _, candidate := range candidates {
				id, err := uuid.Parse(candidate)
				if err != nil || (len(only) > 0 && !slices.Contains(only, id)) {
					continue
				}
				if !slices.Contains(uses[id], probe.label) {
					uses[id] = append(uses[id], probe.label)
				}
			}
		}
	}
	return uses, nil
}

// LockForPurge reserves the household writer for the caller's transaction,
// closing the gap between its uses check and deletion.
func (s *Store) LockForPurge(ctx context.Context, spaceID SpaceID, categoryIDs, tagIDs []uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	if !h.sqliteInTx {
		return fmt.Errorf("store: purge lock requires household transaction")
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	// A write statement acquires SQLite's writer reservation even with no matches.
	_, err = db.Write().ExecContext(ctx, `UPDATE categories SET id=id WHERE 0`)
	return wrap("store: lock household for purge", err)
}

type PurgeResult struct {
	Deleted         int
	FiltersRepaired int
	// FiltersRetired is register searches retired because an item named only
	// purged ids. Nothing points at those, so no rule or report changes.
	FiltersRetired int
	// Resuggest is the unreviewed rows an automation had filed under a purged
	// category, left uncategorized for the caller to fire again.
	Resuggest []uuid.UUID
}

// PruneCategories soft deletes the categories and drops them from every filter
// naming them. The caller must already have checked, in the same transaction
// after LockForPurge, that nothing else refers to them. Refuses with
// ErrPurgeEmptiesFilter.
//
// An unreviewed row still filed under one was filed either by an automation —
// an applied action from an automation's thread that set this very category,
// not changed by the person who applied it — or by somebody. The automation's
// rows go back to uncategorized and come back in Resuggest; the rest keep the
// retired category, as deleting a category by hand leaves them.
func (s *Store) PruneCategories(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) (PurgeResult, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return PurgeResult{}, err
	}
	var out PurgeResult
	err = h.InTx(ctx, func(tx *Store) error {
		if err := tx.refuseProtectedCategories(ctx, spaceID, ids); err != nil {
			return err
		}
		var err error
		if out, err = tx.purge(ctx, spaceID, "categories", domain.FieldCategory, ids); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		marks, args := InArgs(ids)
		values := append([]any{db.Now()}, args...)
		values = append(values, string(domain.AssistantActionApplied))
		out.Resuggest, err = sqliteQueryAll(ctx, db.Write(), "store: purge categories", scanValue[uuid.UUID], `
			UPDATE transactions AS x SET category_id=NULL,category_check_run_id=NULL,updated_at=?
			WHERE x.category_id IN `+marks+` AND NOT x.is_reviewed AND NOT x.is_deleted
			AND EXISTS (SELECT 1 FROM assistant_actions a JOIN assistant_conversations c ON c.id=a.conversation_id
				WHERE c.automation_run_id IS NOT NULL AND a.status=? AND a.tool_name='update_transaction'
				AND a.path='/transactions/' || x.id
				AND json_extract(a.body,'$.category_id')=x.category_id
				AND COALESCE(json_extract(a.proposed_body,'$.category_id'),json_extract(a.body,'$.category_id'))=x.category_id)
			RETURNING id`, values...)
		return err
	})
	if err != nil {
		return PurgeResult{}, err
	}
	return out, nil
}

// PruneTags is PruneCategories for tags. The tag comes off every unreviewed
// row carrying it; no automation suggests tags.
func (s *Store) PruneTags(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) (PurgeResult, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return PurgeResult{}, err
	}
	var out PurgeResult
	err = h.InTx(ctx, func(tx *Store) error {
		var err error
		if out, err = tx.purge(ctx, spaceID, "tags", domain.FieldTag, ids); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		marks, args := InArgs(ids)
		_, err = db.Write().ExecContext(ctx, `DELETE FROM transaction_tags WHERE transaction_id IN
			(SELECT id FROM transactions WHERE NOT is_reviewed) AND tag_id IN `+marks, args...)
		return wrap("store: purge tags", err)
	})
	if err != nil {
		return PurgeResult{}, err
	}
	return out, nil
}

func (s *Store) purge(
	ctx context.Context, spaceID SpaceID, table string, field domain.FilterField, ids []uuid.UUID,
) (PurgeResult, error) {
	var out PurgeResult
	if len(ids) == 0 {
		return out, nil
	}
	what := "store: purge " + table
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return PurgeResult{}, err
	}
	marks, idsArgs := InArgs(ids)
	column := household.FilterValueColumn(string(field))
	// Reuse a CTE so the membership predicates bind each id only once.
	cte := `WITH purged AS (SELECT value FROM json_each(?)) `
	encoded, err := json.Marshal(ids)
	if err != nil {
		return PurgeResult{}, err
	}
	emptied := `EXISTS (SELECT 1 FROM filter_items i WHERE i.filter_id=f.id AND i.field=?
		AND EXISTS (SELECT 1 FROM filter_item_values u WHERE u.filter_item_id=i.id AND u.` + column + ` IN (SELECT value FROM purged))
		AND NOT EXISTS (SELECT 1 FROM filter_item_values u WHERE u.filter_item_id=i.id AND u.` + column + ` NOT IN (SELECT value FROM purged)))`
	named := `EXISTS (SELECT 1 FROM filter_item_values u WHERE u.filter_item_id=i.id AND u.` + column + ` IN (SELECT value FROM purged))`
	err = h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		var refused int
		if err := db.Read().QueryRowContext(ctx, cte+`SELECT count(*) FROM filters f WHERE `+savedFilter()+` AND `+emptied,
			string(encoded), string(field)).Scan(&refused); err != nil {
			return wrap(what, err)
		}
		if refused > 0 {
			return ErrPurgeEmptiesFilter
		}
		tag, err := db.Write().ExecContext(ctx, cte+`UPDATE filters AS f SET is_deleted=true,updated_at=?
			WHERE NOT f.is_deleted AND `+emptied, string(encoded), db.Now(), string(field))
		if err != nil {
			return wrap(what, err)
		}
		n, err := tag.RowsAffected()
		if err != nil {
			return wrap(what, err)
		}
		out.FiltersRetired = int(n)
		tag, err = db.Write().ExecContext(ctx, cte+`UPDATE filter_items AS i SET updated_at=?
			WHERE i.field=? AND EXISTS (SELECT 1 FROM filters f WHERE f.id=i.filter_id AND NOT f.is_deleted)
			AND `+named, string(encoded), db.Now(), string(field))
		if err != nil {
			return wrap(what, err)
		}
		n, err = tag.RowsAffected()
		if err != nil {
			return wrap(what, err)
		}
		out.FiltersRepaired = int(n)
		if _, err := db.Write().ExecContext(ctx, `DELETE FROM filter_item_values WHERE `+column+` IN `+marks+`
			AND filter_item_id IN (SELECT i.id FROM filter_items i JOIN filters f ON f.id = i.filter_id
				WHERE i.field = ? AND NOT f.is_deleted)`, append(slices.Clone(idsArgs), string(field))...); err != nil {
			return wrap(what, err)
		}
		editable := ""
		if table == "categories" {
			editable = ` AND is_editable`
		}
		tag, err = db.Write().ExecContext(ctx, `UPDATE `+table+` SET is_deleted=true,updated_at=? WHERE id IN `+marks+` AND NOT is_deleted`+editable,
			append([]any{db.Now()}, idsArgs...)...)
		if err != nil {
			return wrap(what, err)
		}
		n, err = tag.RowsAffected()
		if err != nil {
			return wrap(what, err)
		}
		out.Deleted = int(n)
		if out.Deleted != len(ids) {
			return wrap(what, ErrNotFound)
		}
		return nil
	})
	if err != nil {
		return PurgeResult{}, err
	}
	return out, nil
}
