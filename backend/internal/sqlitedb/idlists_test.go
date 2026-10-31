package sqlitedb

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/migrations/household"
)

func TestIDListsMoveIntoJoinTables(t *testing.T) {
	d := openTestDB(t)
	if err := d.MigrateFreshHouseholdTo(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	categoryFixture(t, d, "groceries")
	execTest(t, d.Write(), `INSERT INTO tags (id, name, created_at, updated_at) VALUES ('trip', 'Trip', 1, 1), ('home', 'Home', 1, 1), ('spare', 'Spare', 1, 1)`)
	execTest(t, d.Write(), `INSERT INTO accounts (id, name, kind, type, currency, sort_order, created_at, updated_at) VALUES ('checking', 'Checking', 'cash', 'checking', 'USD', 0, 1, 1)`)
	execTest(t, d.Write(), `INSERT INTO filters (id, scope, created_at, updated_at) VALUES ('f', 'report', 1, 1)`)
	execTest(t, d.Write(), `INSERT INTO filter_items (id, filter_id, field, operator, group_index, "position", value_ids, created_at, updated_at) VALUES
		('c', 'f', 'category', 'in', 0, 0, '["groceries","gone"]', 1, 1),
		('t', 'f', 'tag', 'in', 0, 1, '["home","trip"]', 1, 1),
		('p', 'f', 'payee', 'in', 0, 2, '[]', 1, 1)`)
	execTest(t, d.Write(), `INSERT INTO rules (id, name, filter_id, priority, add_tag_ids, created_at, updated_at) VALUES ('r', 'Rule', 'f', 0, '["trip","trip","home","spare"]', 1, 1)`)
	execTest(t, d.Write(), `INSERT INTO series (id, account_id, kind, description, currency, alias, "interval", start_on, reminder_days, match_criteria, template_tag_ids, template_splits, created_at, updated_at)
		VALUES ('s', 'checking', 'bill', 'Water', 'USD', 'monthly', 1, '2026-01-01', 3, 'amount', '["home"]',
		'[{"amount":"-10.00","category_id":"groceries","memo":"a","tag_ids":["trip"]},{"amount":"-2.50","memo":"b"}]', 1, 1)`)

	if _, err := d.MigrateHousehold(t.Context(), filepath.Join(t.TempDir(), "before.db")); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]string{
		`SELECT json_group_array(coalesce(category_id, tag_id, account_id) ORDER BY filter_item_id, "position") FROM filter_item_values`: `["groceries","home","trip"]`,
		`SELECT json_group_array(tag_id ORDER BY "position") FROM rule_tags`:                                                             `["trip","home","spare"]`,
		`SELECT json_group_array(tag_id) FROM series_tags`:                                                                               `["home"]`,
		`SELECT json_group_array(json_array(split_index, tag_id)) FROM series_split_tags`:                                                `[[0,"trip"]]`,
		`SELECT template_splits FROM series`:                                                                                             `[{"amount":"-10.00","category_id":"groceries","memo":"a"},{"amount":"-2.50","memo":"b"}]`,
		`SELECT ` + household.SplitTemplateSQL + ` FROM series`:                                                                          `[{"amount":"-10.00","category_id":"groceries","memo":"a","tag_ids":["trip"]},{"amount":"-2.50","memo":"b","tag_ids":[]}]`,
	} {
		var got string
		if err := d.Read().QueryRowContext(t.Context(), query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if got != want {
			t.Errorf("%s = %s, want %s", query, got, want)
		}
	}
	execTest(t, d.Write(), `DELETE FROM tags WHERE id = 'spare'`)
	var left int
	if err := d.Read().QueryRowContext(t.Context(), `SELECT count(*) FROM rule_tags`).Scan(&left); err != nil || left != 2 {
		t.Fatalf("rule_tags after deleting a tag = %d, %v", left, err)
	}
	if _, err := d.Write().ExecContext(t.Context(), `DELETE FROM categories WHERE id = 'groceries'`); err == nil {
		t.Fatal("a category a filter names was deleted")
	}
	assertHealthySQLite(t, d)
}

// A has_tag item with ids means "has one of these tags", and with none "has
// any tag", so dropping its ids would change what it matches. Each item is
// evaluated before and after the migration against a row tagged a, one
// tagged b and one untagged.
func TestAHasTagItemWithIDsKeepsWhatItMatches(t *testing.T) {
	d := openTestDB(t)
	if err := d.MigrateFreshHouseholdTo(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	execTest(t, d.Write(), `INSERT INTO tags (id, name, created_at, updated_at) VALUES ('a', 'A', 1, 1), ('b', 'B', 1, 1)`)
	execTest(t, d.Write(), `INSERT INTO filters (id, scope, created_at, updated_at) VALUES ('f', 'report', 1, 1)`)
	execTest(t, d.Write(), `INSERT INTO filter_items (id, filter_id, field, operator, group_index, "position", negated, value_ids, created_at, updated_at) VALUES
		('existing', 'f', 'has_tag', 'in', 0, 0, false, '["a"]', 1, 1),
		('existing-negated', 'f', 'has_tag', 'in', 1, 0, true, '["a"]', 1, 1),
		('dangling', 'f', 'has_tag', 'in', 2, 0, false, '["gone"]', 1, 1),
		('dangling-negated', 'f', 'has_tag', 'in', 3, 0, true, '["gone"]', 1, 1),
		('any', 'f', 'has_tag', 'in', 4, 0, false, '[]', 1, 1)`)
	items := func(query string) map[string]domain.FilterItem {
		t.Helper()
		rows, err := d.Read().QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]domain.FilterItem{}
		for rows.Next() {
			var id, field, values string
			var negated bool
			if err := rows.Scan(&id, &field, &negated, &values); err != nil {
				t.Fatal(err)
			}
			item := domain.FilterItem{Field: domain.FilterField(field), Operator: domain.OpIn, Negated: negated}
			if err := json.Unmarshal([]byte(values), &item.Values); err != nil {
				t.Fatal(err)
			}
			out[id] = item
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := items(`SELECT id, field, negated, value_ids FROM filter_items`)
	if _, err := d.MigrateHousehold(t.Context(), filepath.Join(t.TempDir(), "before.db")); err != nil {
		t.Fatal(err)
	}
	after := items(`SELECT i.id, i.field, i.negated, coalesce((SELECT json_group_array(v.tag_id ORDER BY v."position")
		FROM filter_item_values v WHERE v.filter_item_id = i.id), '[]') FROM filter_items i`)

	rows := map[string]domain.Posting{
		"tagged a": {Txn: domain.Transaction{TagIDs: []domain.ID{"a"}}},
		"tagged b": {Txn: domain.Transaction{TagIDs: []domain.ID{"b"}}},
		"untagged": {Txn: domain.Transaction{}},
	}
	for id, was := range before {
		now, ok := after[id]
		if !ok {
			t.Fatalf("item %s is gone", id)
		}
		for name, row := range rows {
			matched := domain.Matches(domain.Filter{Items: []domain.FilterItem{was}}, row, domain.Facets{}, domain.DateEffective)
			matches := domain.Matches(domain.Filter{Items: []domain.FilterItem{now}}, row, domain.Facets{}, domain.DateEffective)
			if matched != matches {
				t.Errorf("item %s on the row %s: matched %v before, %v after (%+v)", id, name, matched, matches, now)
			}
		}
	}
	if got := after["existing"]; got.Field != domain.FieldTag || len(got.Values) != 1 {
		t.Errorf("a has_tag item naming a tag became %+v, want a tag item naming it", got)
	}
	if got := after["any"]; got.Field != domain.FieldHasTag {
		t.Errorf("a has_tag item with no ids became %+v, want it unchanged", got)
	}
	assertHealthySQLite(t, d)
}
