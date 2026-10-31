package portcheck

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/api"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/seed"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/CornHead764/agentifi/backend/migrations/household"
)

// The id list migration keeps what every list says and what every filter
// matches. A household holding every list shape is taken back to the JSON
// columns and given ids naming no row, a repeated tag, ids on a field that
// takes none and has_tag items with ids. Read there, at version 3, each list
// must come back after migrating with only those drops, a has_tag item with
// ids must come back as a tag item, and every filter must match the same
// rows. Every GET route must also answer byte for byte what it answered
// before the round trip.
func TestTheIDListMigrationKeepsEveryRead(t *testing.T) {
	ctx := t.Context()
	cipher, err := store.NewCipher("portcheck-test-secret")
	require.NoError(t, err)
	db := storetest.Empty(t).WithCipher(cipher)
	opts := seed.DefaultOptions()
	opts.Transactions = 48
	opts.Storage = &provider.LocalStorage{BasePath: t.TempDir()}
	require.NoError(t, seed.Run(ctx, db, opts))
	spaces, err := db.ListSpaces(ctx)
	require.NoError(t, err)
	owned := everyListShape(t, db, spaces[0].ID)

	cfg := &config.Config{SecretKey: "portcheck-test-secret"}
	today, err := time.Parse(time.DateOnly, seed.DefaultToday)
	require.NoError(t, err)
	before := rawReads(t, db, cfg, today)
	for _, id := range owned {
		require.Contains(t, before[spaces[0].ID.String()], id, "the shape's read was not discovered")
	}

	hasTagItems := 0
	for _, space := range spaces {
		postings, err := db.LoadPostings(ctx, space.ID, store.TransactionQuery{})
		require.NoError(t, err)
		h, err := db.HouseholdDB(ctx, space.ID)
		require.NoError(t, err)
		var file string
		require.NoError(t, h.Read().QueryRowContext(ctx,
			`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file))
		v3 := backToJSONWithStrayIDs(t, file)
		_, err = h.MigrateHousehold(ctx, filepath.Join(t.TempDir(), "before-"+space.ID.String()+".db"))
		require.NoError(t, err)

		filters, err := db.ListFilters(ctx, space.ID, true)
		require.NoError(t, err)
		gotItems := map[string]listedItem{}
		for _, filter := range filters {
			for _, item := range filter.Items {
				gotItems[item.ID.String()] = listedItem{item.Field, ids(item.ValueIDs)}
			}
		}
		wantItems := map[string]listedItem{}
		for id, item := range v3.items {
			if item.field == string(domain.FieldHasTag) && len(item.ids) > 0 {
				item.field = string(domain.FieldTag)
				hasTagItems++
			}
			item.ids = v3.surviving(household.FilterValueColumn(item.field), item.ids)
			wantItems[id] = item
		}
		require.Equal(t, wantItems, gotItems, "filter items in %s", space.ID)

		rules, err := db.ListRules(ctx, space.ID, store.RuleQuery{IncludeDeleted: true})
		require.NoError(t, err)
		gotRules := map[string][]string{}
		for _, rule := range rules {
			gotRules[rule.ID.String()] = ids(rule.AddTagIDs)
		}
		wantRules := map[string][]string{}
		for id, tags := range v3.ruleTags {
			wantRules[id] = v3.surviving("tag_id", tags)
		}
		require.Equal(t, wantRules, gotRules, "rule tags in %s", space.ID)

		gotSeries := seriesTagLists(t, h.Read())
		wantSeries := map[string]seriesLists{}
		for id, lists := range v3.series {
			want := seriesLists{tags: v3.surviving("tag_id", lists.tags)}
			for _, line := range lists.lines {
				want.lines = append(want.lines, v3.surviving("tag_id", line))
			}
			wantSeries[id] = want
		}
		require.Equal(t, wantSeries, gotSeries, "series tags in %s", space.ID)

		day := domain.DateOf(today)
		for _, filter := range filters {
			was := filter
			was.Items = slices.Clone(filter.Items)
			for i, item := range was.Items {
				stored := v3.items[item.ID.String()]
				was.Items[i].Field = stored.field
				was.Items[i].ValueIDs = make([]uuid.UUID, 0, len(stored.ids))
				for _, id := range stored.ids {
					was.Items[i].ValueIDs = append(was.Items[i].ValueIDs, uuid.MustParse(id))
				}
			}
			require.Equal(t,
				matchedRows(store.DomainFilterOn(was, day), postings),
				matchedRows(store.DomainFilterOn(filter, day), postings),
				"what filter %s matches in %s", filter.ID, space.ID)
		}
	}
	require.Equal(t, 4, hasTagItems, "the has_tag shapes were not injected")

	after := rawReads(t, db, cfg, today)
	require.Equal(t, len(before), len(after))
	for space, reads := range before {
		require.Equal(t, len(reads), len(after[space]), space)
		for path, body := range reads {
			require.Equal(t, string(body), string(after[space][path]), "%s %s", space, path)
		}
	}
}

type listedItem struct {
	field string
	ids   []string
}

type seriesLists struct {
	tags  []string
	lines [][]string
}

// v3Lists is every id list as the version-3 JSON columns hold it, and the ids
// of the rows they may name.
type v3Lists struct {
	items    map[string]listedItem
	ruleTags map[string][]string
	series   map[string]seriesLists
	rows     map[string]map[string]bool
}

// surviving is ids as the migration keeps them in column: those naming a row,
// each once, in order. A field with no column keeps none.
func (v v3Lists) surviving(column string, ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if v.rows[column][id] && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func ids(list []uuid.UUID) []string {
	out := make([]string, 0, len(list))
	for _, id := range list {
		out = append(out, id.String())
	}
	return out
}

func matchedRows(filter domain.Filter, postings []domain.Posting) []domain.ID {
	out := []domain.ID{}
	for _, posting := range domain.Select(filter, postings, nil, domain.DateEffective) {
		out = append(out, posting.Txn.ID)
	}
	return out
}

// seriesTagLists reads each series' template tags and its split lines' tags
// through the statements the store reads them with.
func seriesTagLists(t *testing.T, read sqlitedb.Conn) map[string]seriesLists {
	rows, err := read.QueryContext(t.Context(), `SELECT id, `+store.SeriesTags.IDsSQL("series")+`, `+
		household.SplitTemplateSQL+` FROM series`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]seriesLists{}
	for rows.Next() {
		var id, tags string
		var splits sql.NullString
		require.NoError(t, rows.Scan(&id, &tags, &splits))
		out[id] = parseSeriesLists(t, tags, splits)
	}
	require.NoError(t, rows.Err())
	return out
}

func parseSeriesLists(t *testing.T, tags string, splits sql.NullString) seriesLists {
	out := seriesLists{tags: []string{}}
	require.NoError(t, json.Unmarshal([]byte(tags), &out.tags))
	if !splits.Valid {
		return out
	}
	var lines []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(splits.String), &lines))
	for _, line := range lines {
		tagIDs := []string{}
		if raw, ok := line["tag_ids"]; ok {
			require.NoError(t, json.Unmarshal(raw, &tagIDs))
		}
		out.lines = append(out.lines, tagIDs)
	}
	return out
}

// everyListShape adds, beside the seed's own, lists that are empty, hold one
// id or several, filter items over each id field and over none, and split
// templates with and without tags. It returns paths that must be read.
func everyListShape(t *testing.T, db *store.Store, space store.SpaceID) []string {
	ctx := t.Context()
	var tags []uuid.UUID
	for _, name := range []string{"Alpha", "Bravo", "Charlie"} {
		tag := store.Tag{Name: name}
		require.NoError(t, db.CreateTag(ctx, space, &tag))
		tags = append(tags, tag.ID)
	}
	categories, err := db.ListCategories(ctx, space, false)
	require.NoError(t, err)
	accounts, err := db.ListAccounts(ctx, space, store.AccountQuery{})
	require.NoError(t, err)
	first, second := categories[0].ID, categories[1].ID

	filter := store.Filter{Name: "Every shape", Scope: "report", Items: []store.FilterItem{
		{Field: "category", Operator: "in", ValueIDs: []uuid.UUID{first}},
		{Field: "tag", Operator: "in", Position: 1, ValueIDs: tags},
		{Field: "account", Operator: "in", Position: 2, ValueIDs: []uuid.UUID{accounts[0].ID}},
		{Field: "category", Operator: "in", Position: 3, Negated: true, ValueIDs: []uuid.UUID{second, first}},
		{Field: "payee", Operator: "contains", GroupIndex: 1, Text: "market"},
	}}
	require.NoError(t, db.CreateFilter(ctx, space, &filter))
	paths := []string{"GET /filters/" + filter.ID.String()}
	// backToJSONWithStrayIDs turns these into has_tag items, which only
	// version 3 could hold: naming Alpha, or naming only a tag that is gone.
	for _, shape := range []struct {
		name    string
		negated bool
		ids     []uuid.UUID
	}{
		{"Has tag Alpha", false, tags[:1]},
		{"Has no tag Alpha", true, tags[:1]},
		{"Has tag gone", false, nil},
		{"Has no tag gone", true, nil},
	} {
		hasTag := store.Filter{Name: shape.name, Scope: "report", Items: []store.FilterItem{
			{Field: "tag", Operator: "in", Negated: shape.negated, ValueIDs: shape.ids},
		}}
		require.NoError(t, db.CreateFilter(ctx, space, &hasTag))
		paths = append(paths, "GET /filters/"+hasTag.ID.String())
	}
	for _, rule := range []store.Rule{
		{Name: "No tags", FilterID: filter.ID, IsActive: true, SetPayee: "Market"},
		{Name: "Three tags", FilterID: filter.ID, IsActive: true, AddTagIDs: []uuid.UUID{tags[2], tags[0], tags[1]}},
	} {
		require.NoError(t, db.CreateRule(ctx, space, &rule))
		paths = append(paths, "GET /rules/"+rule.ID.String())
	}

	recur, err := domain.NewRecurrence(domain.AliasEveryMonth, domain.FreqMonthly, 1, []int{20}, nil, nil)
	require.NoError(t, err)
	line := func(amount string, category uuid.UUID, tagIDs string) string {
		out := `{"amount":"` + amount + `","category_id":"` + category.String() + `","memo":"Line"`
		if tagIDs != "" {
			out += `,"tag_ids":` + tagIDs
		}
		return out + `}`
	}
	quoted := func(ids ...uuid.UUID) string {
		raw, _ := json.Marshal(ids)
		return string(raw)
	}
	for i, shape := range []struct {
		tags   []uuid.UUID
		splits string
	}{
		{nil, ""},
		{[]uuid.UUID{tags[1]}, `[` + line("-60.00", first, `[]`) + `,` + line("-40.00", second, ``) + `]`},
		{[]uuid.UUID{tags[0], tags[2]}, `[` + line("-70.00", first, quoted(tags[2], tags[0])) + `,` +
			line("-20.00", second, ``) + `,` + line("-10.00", first, quoted(tags[1])) + `]`},
	} {
		w := store.SeriesWrite{AccountID: accounts[0].ID, CategoryID: first, Kind: "bill",
			Description: "SHAPE " + string(rune('A'+i)), Amount: domain.MustFromString("-100.00"),
			Currency: accounts[0].Currency, Recurrence: recur, StartOn: domain.DateOf(time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)),
			IsActive: true, TemplateTagIDs: shape.tags}
		if shape.splits != "" {
			w.TemplateSplits = []byte(shape.splits)
		}
		require.NoError(t, db.CreateSeries(ctx, space, &w))
		paths = append(paths, "GET /series/"+w.ID.String())
	}
	return paths
}

// backToJSONWithStrayIDs runs migration 4's Down on the household file, then
// adds to the JSON lists only ids the Up drops (ids naming no row, a repeated
// rule tag, and ids on a payee item) and turns everyListShape's tag items into
// has_tag items. It answers the lists as version 3 then holds them.
func backToJSONWithStrayIDs(t *testing.T, file string) v3Lists {
	ctx := t.Context()
	conn, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: file,
		RawQuery: "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"}).String())
	require.NoError(t, err)
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, household.UnmigrateIDLists(ctx, tx))
	_, err = tx.ExecContext(ctx, `DELETE FROM goose_db_version WHERE version_id = 4`)
	require.NoError(t, err)

	stray := func() string { return uuid.NewString() }
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE filter_items SET value_ids = json_insert(value_ids, '$[#]', ?)
			WHERE field IN ('category', 'account') AND json_array_length(value_ids) > 0`, []any{stray()}},
		{`UPDATE filter_items SET value_ids = '["' || ? || '",' || substr(value_ids, 2)
			WHERE field = 'tag' AND json_array_length(value_ids) > 0`, []any{stray()}},
		{`UPDATE filter_items SET value_ids = json_array((SELECT id FROM categories LIMIT 1)) WHERE field = 'payee'`, nil},
		{`UPDATE rules SET add_tag_ids = json_insert('["' || ? || '",' || substr(add_tag_ids, 2), '$[#]', add_tag_ids ->> 0)
			WHERE json_array_length(add_tag_ids) > 0`, []any{stray()}},
		{`UPDATE series SET template_tag_ids = json_insert(template_tag_ids, '$[#]', ?)
			WHERE json_array_length(template_tag_ids) > 0`, []any{stray()}},
		{`UPDATE filter_items SET field = 'has_tag' WHERE filter_id IN
			(SELECT id FROM filters WHERE name IN ('Has tag Alpha', 'Has no tag Alpha', 'Has tag gone', 'Has no tag gone'))`, nil},
		{`UPDATE filter_items SET value_ids = json_array(?) WHERE filter_id IN
			(SELECT id FROM filters WHERE name IN ('Has tag gone', 'Has no tag gone'))`, []any{stray()}},
	} {
		_, err := tx.ExecContext(ctx, statement.sql, statement.args...)
		require.NoError(t, err, statement.sql)
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, template_splits FROM series WHERE template_splits IS NOT NULL`)
	require.NoError(t, err)
	templates := map[string]string{}
	for rows.Next() {
		var id, splits string
		require.NoError(t, rows.Scan(&id, &splits))
		templates[id] = splits
	}
	require.NoError(t, rows.Err())
	rows.Close()
	for id, splits := range templates {
		var lines []map[string]any
		require.NoError(t, json.Unmarshal([]byte(splits), &lines))
		for _, line := range lines {
			if tagIDs, ok := line["tag_ids"].([]any); ok && len(tagIDs) > 0 {
				line["tag_ids"] = append(tagIDs, stray())
			}
		}
		raw, err := json.Marshal(lines)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `UPDATE series SET template_splits = ? WHERE id = ?`, string(raw), id)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	return readV3Lists(t, conn)
}

func readV3Lists(t *testing.T, conn *sql.DB) v3Lists {
	ctx := t.Context()
	out := v3Lists{items: map[string]listedItem{}, ruleTags: map[string][]string{},
		series: map[string]seriesLists{}, rows: map[string]map[string]bool{}}
	each := func(query string, scan func(*sql.Rows)) {
		rows, err := conn.QueryContext(ctx, query)
		require.NoError(t, err, query)
		defer rows.Close()
		for rows.Next() {
			scan(rows)
		}
		require.NoError(t, rows.Err())
	}
	list := func(raw string) []string {
		out := []string{}
		require.NoError(t, json.Unmarshal([]byte(raw), &out))
		return out
	}
	for column, table := range map[string]string{"category_id": "categories", "tag_id": "tags", "account_id": "accounts"} {
		out.rows[column] = map[string]bool{}
		each(`SELECT id FROM `+table, func(rows *sql.Rows) {
			var id string
			require.NoError(t, rows.Scan(&id))
			out.rows[column][id] = true
		})
	}
	each(`SELECT id, field, value_ids FROM filter_items`, func(rows *sql.Rows) {
		var id, field, values string
		require.NoError(t, rows.Scan(&id, &field, &values))
		out.items[id] = listedItem{field, list(values)}
	})
	each(`SELECT id, add_tag_ids FROM rules`, func(rows *sql.Rows) {
		var id, tags string
		require.NoError(t, rows.Scan(&id, &tags))
		out.ruleTags[id] = list(tags)
	})
	each(`SELECT id, template_tag_ids, template_splits FROM series`, func(rows *sql.Rows) {
		var id, tags string
		var splits sql.NullString
		require.NoError(t, rows.Scan(&id, &tags, &splits))
		out.series[id] = parseSeriesLists(t, tags, splits)
	})
	return out
}

// rawReads answers every GET route Capture discovers, per household, as the
// exact bytes the router wrote.
func rawReads(t *testing.T, db *store.Store, cfg *config.Config, today time.Time) map[string]map[string][]byte {
	ctx := t.Context()
	snapshot, err := Capture(ctx, db, cfg, today)
	require.NoError(t, err)
	noon := time.Date(today.Year(), today.Month(), today.Day(), 12, 0, 0, 0, time.UTC)
	env := api.NewEnv(OfflineConfig(cfg), db)
	env.Now = func() time.Time { return noon }
	env.Releases = nil
	handler := api.RouterFor(env)
	out := map[string]map[string][]byte{}
	for space, responses := range snapshot.Households {
		token := ownerToken(t, ctx, db, env, space)
		keys := make([]string, 0, len(responses))
		for key := range responses {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out[space] = map[string][]byte{}
		for _, key := range keys {
			req := httptest.NewRequest(http.MethodGet, strings.TrimPrefix(key, "GET "), nil).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Space-Id", space)
			req.Header.Set("X-Request-Id", "portcheck")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			out[space][key] = bytes.Clone(rec.Body.Bytes())
		}
	}
	return out
}

func ownerToken(t *testing.T, ctx context.Context, db *store.Store, env *api.Env, space string) string {
	memberships, err := db.ListMemberships(ctx, store.SpaceIDOf(uuid.MustParse(space)))
	require.NoError(t, err)
	for _, m := range memberships {
		if m.IsAccepted() && m.Role == store.RoleOwner {
			token, _, err := env.Tokens.Issue(ctx, m.UserID, store.SessionWeb, "test")
			require.NoError(t, err)
			return token
		}
	}
	t.Fatalf("household %s has no accepted owner", space)
	return ""
}
