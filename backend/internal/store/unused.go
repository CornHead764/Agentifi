package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Finding the categories and tags nothing uses, and deleting them.
//
// The difficulty is the inventory below. Several references carry no foreign
// key at all — a series' jsonb split template, a rule's add_tag_ids, every
// saved Filter's value_ids, the assistant's pending proposals — and the keys
// that exist are ON DELETE SET NULL, which empties a rule rather than failing.
// A new reference to categories(id) or tags(id) must be added here.
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
	out := make([]useProbe, 0, len(filterOwners))
	for _, owner := range filterOwners {
		out = append(out, useProbe{owner.label, fmt.Sprintf(`SELECT u AS id
			FROM filters f
			JOIN filter_items i ON i.filter_id = f.id
			CROSS JOIN LATERAL unnest(i.value_ids) AS u
			WHERE f.space_id = $1 AND NOT f.is_deleted AND i.field = '%s' AND (%s)
			  AND (cardinality(i.value_ids) = 1 OR cardinality(i.value_ids) * 2 <=
			       (SELECT count(*) FROM %s WHERE space_id = $1 AND NOT is_deleted))`,
			field, owner.where, table)})
	}
	return out
}

// pendingProposalProbe is every id a waiting proposal carries anywhere — the
// request, the edited body, the preview or its path. The shape varies by tool,
// so it reads every string in the documents rather than a known key.
var pendingProposalProbe = useProbe{"pending assistant proposals", `
	SELECT (v #>> '{}')::uuid AS id
	FROM assistant_actions a,
	     jsonb_path_query(jsonb_build_array(a.body, a.proposed_body, a.preview), 'strict $.**') v
	WHERE a.space_id = $1 AND a.status = '` + domain.AssistantActionPending + `'
	  AND jsonb_typeof(v) = 'string' AND v #>> '{}' ~ '^` + uuidPattern + `$'
	UNION
	SELECT m[1]::uuid FROM assistant_actions a, regexp_matches(a.path, '(` + uuidPattern + `)', 'g') m
	WHERE a.space_id = $1 AND a.status = '` + domain.AssistantActionPending + `'`}

// categoryUseProbes is every place a category id is written down, with what a
// screen calls it. Each selects one uuid column named id.
var categoryUseProbes = slices.Concat([]useProbe{
	{"reviewed transactions", `SELECT category_id AS id FROM transactions
		WHERE space_id = $1 AND category_id IS NOT NULL AND is_reviewed`},
	{"splits", `SELECT category_id AS id FROM transaction_splits
		WHERE space_id = $1 AND category_id IS NOT NULL`},
	{"rule actions", `SELECT set_category_id AS id FROM rules
		WHERE space_id = $1 AND NOT is_deleted AND set_category_id IS NOT NULL`},
	{"recurring series and reminders", `SELECT category_id AS id FROM series
		WHERE space_id = $1 AND NOT is_deleted AND category_id IS NOT NULL`},
	{"split templates", `SELECT (e->>'category_id')::uuid AS id
		FROM series s, jsonb_array_elements(s.template_splits) e
		WHERE s.space_id = $1 AND NOT s.is_deleted AND jsonb_typeof(s.template_splits) = 'array'
		  AND e->>'category_id' ~ '^` + uuidPattern + `$'`},
	{"mail rules", `SELECT category_id AS id FROM mail_rules
		WHERE space_id = $1 AND category_id IS NOT NULL
		UNION SELECT income_category_id FROM mail_rules
		WHERE space_id = $1 AND income_category_id IS NOT NULL`},
}, filterUseProbes(domain.FieldCategory, "categories"), []useProbe{
	pendingProposalProbe,
	{"the assistant's runs", `SELECT expected_category_id AS id FROM assistant_automation_runs
		WHERE space_id = $1 AND expected_category_id IS NOT NULL`},
	{"the assistant's corrections", `SELECT proposed_category_id AS id FROM assistant_corrections
		WHERE space_id = $1 AND proposed_category_id IS NOT NULL
		UNION SELECT chosen_category_id FROM assistant_corrections
		WHERE space_id = $1 AND chosen_category_id IS NOT NULL`},
})

var tagUseProbes = slices.Concat([]useProbe{
	{"reviewed transactions", `SELECT l.tag_id AS id FROM transaction_tags l
		JOIN transactions x ON x.id = l.transaction_id WHERE x.space_id = $1 AND x.is_reviewed`},
	{"splits", `SELECT l.tag_id AS id FROM split_tags l
		JOIN tags t ON t.id = l.tag_id WHERE t.space_id = $1`},
	{"rule actions", `SELECT unnest(add_tag_ids) AS id FROM rules
		WHERE space_id = $1 AND NOT is_deleted`},
	{"recurring series and reminders", `SELECT unnest(template_tag_ids) AS id FROM series
		WHERE space_id = $1 AND NOT is_deleted`},
	{"split templates", `SELECT (t #>> '{}')::uuid AS id
		FROM series s, jsonb_array_elements(s.template_splits) e,
		     jsonb_array_elements(CASE WHEN jsonb_typeof(e->'tag_ids') = 'array'
		                               THEN e->'tag_ids' ELSE '[]'::jsonb END) t
		WHERE s.space_id = $1 AND NOT s.is_deleted AND jsonb_typeof(s.template_splits) = 'array'
		  AND t #>> '{}' ~ '^` + uuidPattern + `$'`},
	{"goals", `SELECT tag_id AS id FROM goals
		WHERE space_id = $1 AND NOT is_deleted AND tag_id IS NOT NULL`},
}, filterUseProbes(domain.FieldTag, "tags"), []useProbe{
	pendingProposalProbe,
})

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
	if only == nil {
		only = []uuid.UUID{}
	}
	uses := map[uuid.UUID][]string{}
	for _, probe := range probes {
		what := "store: " + noun + " uses (" + probe.label + ")"
		ids, err := queryAll(ctx, s.db, what, scanValue[uuid.UUID], `SELECT DISTINCT q.id FROM (`+probe.sql+`) q
			WHERE q.id IS NOT NULL AND (cardinality($2::uuid[]) = 0 OR q.id = ANY($2))`,
			spaceID.UUID(), only)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if !slices.Contains(uses[id], probe.label) {
				uses[id] = append(uses[id], probe.label)
			}
		}
	}
	return uses, nil
}

// LockForPurge row-locks each category and tag about to go, for the rest of
// the transaction. Every foreign key into them takes a key-share lock, which
// this blocks, so a row filed under one during the purge either committed
// before the lock (and the uses check sees it) or waits. Without it the check
// and the delete have a gap between them.
func (s *Store) LockForPurge(
	ctx context.Context, spaceID SpaceID, categoryIDs, tagIDs []uuid.UUID,
) error {
	for _, table := range []struct {
		name string
		ids  []uuid.UUID
	}{{"categories", categoryIDs}, {"tags", tagIDs}} {
		if len(table.ids) == 0 {
			continue
		}
		rows, err := s.db.Query(ctx, `SELECT id FROM `+table.name+`
			WHERE space_id = $1 AND id = ANY($2) ORDER BY id FOR UPDATE`,
			spaceID.UUID(), table.ids)
		if err != nil {
			return wrap("store: lock "+table.name+" for purge", err)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return wrap("store: lock "+table.name+" for purge", err)
		}
	}
	return nil
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
	var out PurgeResult
	err := s.InTx(ctx, func(tx *Store) error {
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
		out.Resuggest, err = queryAll(ctx, tx.db, "store: purge categories", scanValue[uuid.UUID], `
			UPDATE transactions x
			   SET category_id = NULL, category_check_run_id = NULL, updated_at = now()
			 WHERE x.space_id = $1 AND x.category_id = ANY($2)
			   AND NOT x.is_reviewed AND NOT x.is_deleted
			   AND EXISTS (
			       SELECT 1 FROM assistant_actions a
			         JOIN assistant_conversations c ON c.id = a.conversation_id
			        WHERE a.space_id = $1 AND c.automation_run_id IS NOT NULL
			          AND a.status = $3 AND a.tool_name = 'update_transaction'
			          AND a.path = '/transactions/' || x.id::text
			          AND a.body->>'category_id' = x.category_id::text
			          AND COALESCE(a.proposed_body->>'category_id', a.body->>'category_id')
			              = x.category_id::text)
			RETURNING x.id`,
			spaceID.UUID(), ids, domain.AssistantActionApplied)
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
	var out PurgeResult
	err := s.InTx(ctx, func(tx *Store) error {
		var err error
		if out, err = tx.purge(ctx, spaceID, "tags", domain.FieldTag, ids); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		_, err = tx.db.Exec(ctx, `
			DELETE FROM transaction_tags l USING transactions x
			 WHERE x.id = l.transaction_id AND x.space_id = $1 AND NOT x.is_reviewed
			   AND l.tag_id = ANY($2)`,
			spaceID.UUID(), ids)
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
	// An item that names only purged ids, over the filter aliased f.
	const emptied = `EXISTS (SELECT 1 FROM filter_items i
		WHERE i.filter_id = f.id AND i.field = $3 AND i.value_ids && $2
		  AND NOT EXISTS (SELECT 1 FROM unnest(i.value_ids) u WHERE NOT (u = ANY($2))))`

	err := s.InTx(ctx, func(tx *Store) error {
		var refused int
		if err := tx.db.QueryRow(ctx, `SELECT count(*) FROM filters f
			WHERE f.space_id = $1 AND `+savedFilter()+` AND `+emptied,
			spaceID.UUID(), ids, string(field)).Scan(&refused); err != nil {
			return wrap(what, err)
		}
		if refused > 0 {
			return ErrPurgeEmptiesFilter
		}

		tag, err := tx.db.Exec(ctx, `UPDATE filters f SET is_deleted = true, updated_at = now()
			WHERE f.space_id = $1 AND NOT f.is_deleted AND `+emptied,
			spaceID.UUID(), ids, string(field))
		if err != nil {
			return wrap(what, err)
		}
		out.FiltersRetired = int(tag.RowsAffected())

		tag, err = tx.db.Exec(ctx, `
			UPDATE filter_items i SET value_ids = COALESCE((
				SELECT array_agg(u ORDER BY ord)
				FROM unnest(i.value_ids) WITH ORDINALITY AS t(u, ord)
				WHERE NOT (u = ANY($2))), '{}'::uuid[]), updated_at = now()
			FROM filters f
			WHERE f.id = i.filter_id AND NOT f.is_deleted
			  AND i.space_id = $1 AND i.field = $3 AND i.value_ids && $2`,
			spaceID.UUID(), ids, string(field))
		if err != nil {
			return wrap(what, err)
		}
		out.FiltersRepaired = int(tag.RowsAffected())

		editable := ""
		if table == "categories" {
			editable = " AND is_editable"
		}
		tag, err = tx.db.Exec(ctx, `UPDATE `+table+` SET is_deleted = true, updated_at = now()
			WHERE space_id = $1 AND id = ANY($2) AND NOT is_deleted`+editable,
			spaceID.UUID(), ids)
		if err != nil {
			return wrap(what, err)
		}
		out.Deleted = int(tag.RowsAffected())
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
