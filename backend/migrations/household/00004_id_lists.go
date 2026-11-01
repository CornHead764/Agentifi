package household

import (
	"context"
	"database/sql"
	"strings"
)

// IDListTables are the join tables that replace the queried JSON id lists:
// filter_items.value_ids, rules.add_tag_ids, series.template_tag_ids and the
// tag_ids inside each element of series.template_splits. Unlike CaptureTables
// at version 2 they are created here, with their capture columns already in
// place.
var IDListTables = []SyncTable{
	{Name: "filter_item_values", Keys: []string{"filter_item_id", `"position"`}, HasUpdatedAt: false},
	{Name: "rule_tags", Keys: []string{"rule_id", "tag_id"}, HasUpdatedAt: false},
	{Name: "series_tags", Keys: []string{"series_id", "tag_id"}, HasUpdatedAt: false},
	{Name: "series_split_tags", Keys: []string{"series_id", "split_index", "tag_id"}, HasUpdatedAt: false},
}

// Target foreign keys are deferred: the importer writes filters and rules in
// an order that does not follow them, and a transaction is checked whole.
const idListSchema = `
CREATE TABLE filter_item_values (
    filter_item_id TEXT NOT NULL,
    "position" INTEGER NOT NULL,
    category_id TEXT,
    tag_id TEXT,
    account_id TEXT,
    updated_at INTEGER,
    change_seq INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT filter_item_values_pkey PRIMARY KEY (filter_item_id, "position"),
    CONSTRAINT filter_item_values_one_target CHECK ((category_id IS NOT NULL) + (tag_id IS NOT NULL) + (account_id IS NOT NULL) = 1),
    CONSTRAINT filter_item_values_filter_item_id_fkey FOREIGN KEY (filter_item_id) REFERENCES filter_items(id) ON DELETE CASCADE,
    CONSTRAINT filter_item_values_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT filter_item_values_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT filter_item_values_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) DEFERRABLE INITIALLY DEFERRED
) STRICT;
CREATE INDEX filter_item_values_category_idx ON filter_item_values (category_id) WHERE category_id IS NOT NULL;
CREATE INDEX filter_item_values_tag_idx ON filter_item_values (tag_id) WHERE tag_id IS NOT NULL;
CREATE INDEX filter_item_values_account_idx ON filter_item_values (account_id) WHERE account_id IS NOT NULL;

CREATE TABLE rule_tags (
    rule_id TEXT NOT NULL,
    tag_id TEXT NOT NULL,
    "position" INTEGER NOT NULL,
    updated_at INTEGER,
    change_seq INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT rule_tags_pkey PRIMARY KEY (rule_id, tag_id),
    CONSTRAINT rule_tags_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES rules(id) ON DELETE CASCADE,
    CONSTRAINT rule_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
) STRICT;
CREATE INDEX rule_tags_tag_idx ON rule_tags (tag_id);

CREATE TABLE series_tags (
    series_id TEXT NOT NULL,
    tag_id TEXT NOT NULL,
    "position" INTEGER NOT NULL,
    updated_at INTEGER,
    change_seq INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT series_tags_pkey PRIMARY KEY (series_id, tag_id),
    CONSTRAINT series_tags_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE,
    CONSTRAINT series_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
) STRICT;
CREATE INDEX series_tags_tag_idx ON series_tags (tag_id);

CREATE TABLE series_split_tags (
    series_id TEXT NOT NULL,
    split_index INTEGER NOT NULL,
    tag_id TEXT NOT NULL,
    "position" INTEGER NOT NULL,
    updated_at INTEGER,
    change_seq INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT series_split_tags_pkey PRIMARY KEY (series_id, split_index, tag_id),
    CONSTRAINT series_split_tags_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE,
    CONSTRAINT series_split_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
) STRICT;
CREATE INDEX series_split_tags_tag_idx ON series_split_tags (tag_id);
`

// FilterValueColumn is the filter_item_values column holding a field's ids,
// or "" for a field that names no ids.
func FilterValueColumn(field string) string {
	switch field {
	case "category", "tag", "account":
		return field + "_id"
	}
	return ""
}

// MoveSplitTagsSQL takes the tag_ids out of each object in the split
// templates of the series matching where (over series s) and files them in
// series_split_tags, replacing what was there. Run it after every write of
// template_splits. lenient drops tag ids naming no tag rather than
// failing the foreign key; only the migration wants that.
func MoveSplitTagsSQL(where string, lenient bool) []string {
	exists := ""
	if lenient {
		exists = ` AND EXISTS (SELECT 1 FROM tags g WHERE g.id = t.value)`
	}
	return []string{`
DELETE FROM series_split_tags WHERE series_id IN (SELECT s.id FROM series s WHERE ` + where + `)`, `
INSERT OR IGNORE INTO series_split_tags (series_id, split_index, tag_id, "position")
SELECT s.id, e.key, t.value, t.key
FROM series s, json_each(s.template_splits) e, json_each(e.value, '$.tag_ids') t
WHERE (` + where + `) AND json_type(s.template_splits) = 'array' AND e.type = 'object'
  AND json_type(e.value, '$.tag_ids') = 'array'` + exists, `
UPDATE series AS s SET template_splits = (
    SELECT json_group_array(CASE WHEN e.type = 'object' THEN json_remove(json(e.value), '$.tag_ids')
        ELSE json(s.template_splits -> e.fullkey) END ORDER BY e.key)
    FROM json_each(s.template_splits) e)
WHERE (` + where + `) AND json_type(s.template_splits) = 'array'
  AND EXISTS (SELECT 1 FROM json_each(s.template_splits) e WHERE e.type = 'object' AND json_type(e.value, '$.tag_ids') IS NOT NULL)`}
}

// SplitTemplateSQL reads series.template_splits (table aliased or named
// series) with each split's tag_ids put back from series_split_tags.
const SplitTemplateSQL = `CASE WHEN json_type(series.template_splits) = 'array'
	AND EXISTS (SELECT 1 FROM series_split_tags st WHERE st.series_id = series.id)
THEN (SELECT json_group_array(CASE WHEN e.type = 'object' THEN json_set(json(e.value), '$.tag_ids', json(coalesce(
		(SELECT json_group_array(st.tag_id ORDER BY st."position") FROM series_split_tags st
		 WHERE st.series_id = series.id AND st.split_index = e.key), '[]')))
	ELSE json(series.template_splits -> e.fullkey) END ORDER BY e.key)
	FROM json_each(series.template_splits) e)
ELSE series.template_splits END`

// MigrateIDLists moves the id lists into their join tables. An id that names
// no row is dropped: the foreign key would refuse it, and the evaluator
// already matched nothing by it. A has_tag item with ids meant "has one of
// these tags", which is a tag item; left as has_tag with no ids it would mean
// "has any tag", so it becomes one.
func MigrateIDLists(ctx context.Context, tx *sql.Tx) error {
	var script strings.Builder
	script.WriteString(idListSchema)
	script.WriteString(`
INSERT INTO filter_item_values (filter_item_id, "position", category_id, tag_id, account_id)
SELECT i.id, u.key,
    CASE WHEN i.field = 'category' THEN u.value END,
    CASE WHEN i.field IN ('tag', 'has_tag') THEN u.value END,
    CASE WHEN i.field = 'account' THEN u.value END
FROM filter_items i, json_each(i.value_ids) u
WHERE (i.field = 'category' AND EXISTS (SELECT 1 FROM categories c WHERE c.id = u.value))
   OR (i.field IN ('tag', 'has_tag') AND EXISTS (SELECT 1 FROM tags c WHERE c.id = u.value))
   OR (i.field = 'account' AND EXISTS (SELECT 1 FROM accounts c WHERE c.id = u.value));
UPDATE filter_items SET field = 'tag' WHERE field = 'has_tag' AND json_array_length(value_ids) > 0;
INSERT OR IGNORE INTO rule_tags (rule_id, tag_id, "position")
SELECT r.id, t.value, t.key FROM rules r, json_each(r.add_tag_ids) t
WHERE EXISTS (SELECT 1 FROM tags g WHERE g.id = t.value);
INSERT OR IGNORE INTO series_tags (series_id, tag_id, "position")
SELECT s.id, t.value, t.key FROM series s, json_each(s.template_tag_ids) t
WHERE EXISTS (SELECT 1 FROM tags g WHERE g.id = t.value);
`)
	script.WriteString(strings.Join(MoveSplitTagsSQL("1", true), ";\n") + ";\n")
	script.WriteString(`
ALTER TABLE filter_items DROP COLUMN value_ids;
ALTER TABLE rules DROP COLUMN add_tag_ids;
ALTER TABLE series DROP COLUMN template_tag_ids;
`)
	script.WriteString(ChangeCaptureSQL(IDListTables))
	names := make([]string, len(IDListTables))
	for i, table := range IDListTables {
		names[i] = table.Name
	}
	script.WriteString(WriteCounterSQL(names))
	_, err := tx.ExecContext(ctx, script.String())
	return err
}

// UnmigrateIDLists puts the lists back as JSON columns.
func UnmigrateIDLists(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE filter_items ADD COLUMN value_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(value_ids) AND json_type(value_ids) = 'array');
ALTER TABLE rules ADD COLUMN add_tag_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(add_tag_ids) AND json_type(add_tag_ids) = 'array');
ALTER TABLE series ADD COLUMN template_tag_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(template_tag_ids) AND json_type(template_tag_ids) = 'array');
UPDATE filter_items SET value_ids = (SELECT json_group_array(coalesce(v.category_id, v.tag_id, v.account_id) ORDER BY v."position")
    FROM filter_item_values v WHERE v.filter_item_id = filter_items.id)
WHERE EXISTS (SELECT 1 FROM filter_item_values v WHERE v.filter_item_id = filter_items.id);
UPDATE rules SET add_tag_ids = (SELECT json_group_array(t.tag_id ORDER BY t."position") FROM rule_tags t WHERE t.rule_id = rules.id)
WHERE EXISTS (SELECT 1 FROM rule_tags t WHERE t.rule_id = rules.id);
UPDATE series SET template_tag_ids = (SELECT json_group_array(t.tag_id ORDER BY t."position") FROM series_tags t WHERE t.series_id = series.id)
WHERE EXISTS (SELECT 1 FROM series_tags t WHERE t.series_id = series.id);
UPDATE series SET template_splits = `+SplitTemplateSQL+`;
DROP TABLE series_split_tags;
DROP TABLE series_tags;
DROP TABLE rule_tags;
DROP TABLE filter_item_values;
`)
	return err
}
