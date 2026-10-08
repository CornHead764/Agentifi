// Package pgimport copies a database written by the Postgres-backed release
// into a fresh SQLite file, so an existing install keeps its data across the
// move.
//
// The copy is driven by the SQLite schema: every table and column it declares
// is read from Postgres and converted by the pair of column types, so a table
// added to both schemas needs nothing here. Anything Postgres holds that the
// SQLite schema has no place for is refused rather than dropped.
package pgimport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// PostgresSchemaVersion is the goose version of migrations/00001_initial.sql,
// the last schema a Postgres-backed release wrote. A database at any other
// version has columns this copy does not know.
const PostgresSchemaVersion = 1

// Report is what a copy wrote, verified against the source.
type Report struct {
	Tables []TableReport
}

// Rows is the number of rows copied across every table.
func (r Report) Rows() int64 {
	var n int64
	for _, t := range r.Tables {
		n += t.Rows
	}
	return n
}

// TableReport is one table's row count and the total of each money column,
// equal on both sides.
type TableReport struct {
	Table string
	Rows  int64
	Money []ColumnSum
}

type ColumnSum struct {
	Column string
	Sum    decimal.Decimal
}

type table struct {
	name    string
	columns []column
}

type column struct {
	name       string
	sqliteType string
	kind       kind
	pg         pgType
}

// OpenTarget opens the SQLite database at path for an import. A file that
// does not exist yet, or has no tables, is created and migrated; one that is
// behind this build's schema or holds any row is refused.
func OpenTarget(ctx context.Context, path string) (*store.Store, error) {
	db, err := store.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := prepareTarget(ctx, db, path); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func prepareTarget(ctx context.Context, db *store.Store, path string) error {
	var tables int64
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table'`).Scan(&tables); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if tables == 0 {
		if _, err := db.Migrate(ctx); err != nil {
			return err
		}
	}

	version, err := db.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	latest, err := db.LatestMigrationVersion()
	if err != nil {
		return err
	}
	if version != latest {
		return fmt.Errorf("%s is at schema version %d and this build's is %d; import into a new file", path, version, latest)
	}

	schema, err := sqliteTables(ctx, db.Pool())
	if err != nil {
		return err
	}
	for _, t := range schema {
		var n int64
		if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM `+quote(t.name)).Scan(&n); err != nil {
			return fmt.Errorf("counting %s: %w", t.name, err)
		}
		if n > 0 {
			return fmt.Errorf("%s already holds data (%d rows in %s); import only into an empty database", path, n, t.name)
		}
	}
	return nil
}

// Import copies every row of src into dst in one SQLite transaction, then
// checks foreign keys, row counts and money totals before committing. On any
// error nothing is written. Postgres is read in one repeatable-read snapshot,
// so the totals compared are the ones copied even if a server is still
// writing.
func Import(ctx context.Context, src *pgx.Conn, dst *store.Store) (Report, error) {
	pg, err := src.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Report{}, fmt.Errorf("reading Postgres: %w", err)
	}
	defer pg.Rollback(ctx)

	version, err := postgresVersion(ctx, pg)
	if err != nil {
		return Report{}, err
	}
	if version != PostgresSchemaVersion {
		return Report{}, fmt.Errorf("the Postgres database is at schema version %d, but this import reads version %d; "+
			"migrate it with the last Postgres-backed release (or restore a backup taken at version %d) first",
			version, PostgresSchemaVersion, PostgresSchemaVersion)
	}

	schema, err := sqliteTables(ctx, dst.Pool())
	if err != nil {
		return Report{}, err
	}
	tables, err := matchPostgres(ctx, pg, schema)
	if err != nil {
		return Report{}, err
	}

	tx, err := dst.Pool().Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	// Rows arrive table by table in name order, so a row may precede its
	// parent; deferring the checks to commit lets the copy ignore the order,
	// and foreign_key_check below names any row left without one.
	if _, err := tx.Exec(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return Report{}, err
	}

	for _, t := range tables {
		if err := copyTable(ctx, pg, tx, t); err != nil {
			return Report{}, fmt.Errorf("copying %s: %w", t.name, err)
		}
	}
	if err := foreignKeyCheck(ctx, tx); err != nil {
		return Report{}, err
	}
	report, err := verify(ctx, pg, tx, tables)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("committing the copy: %w", err)
	}
	return report, nil
}

// postgresVersion reads goose's version table the way goose does: a version
// counts when its latest row says it is applied.
func postgresVersion(ctx context.Context, pg pgx.Tx) (int64, error) {
	var exists bool
	if err := pg.QueryRow(ctx, `SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("reading Postgres: %w", err)
	}
	if !exists {
		return 0, errors.New("the Postgres database has no goose_db_version table; it is not an Agentifi database, or it was never migrated")
	}
	rows, err := pg.Query(ctx, `SELECT version_id, is_applied FROM goose_db_version ORDER BY id DESC`)
	if err != nil {
		return 0, fmt.Errorf("reading goose_db_version: %w", err)
	}
	defer rows.Close()
	seen := map[int64]bool{}
	var version int64
	for rows.Next() {
		var id int64
		var applied bool
		if err := rows.Scan(&id, &applied); err != nil {
			return 0, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if applied && id > version {
			version = id
		}
	}
	return version, rows.Err()
}

func sqliteTables(ctx context.Context, db sqlitedb.Querier) ([]table, error) {
	rows, err := db.Query(ctx, `SELECT name FROM sqlite_schema
		WHERE type = 'table' AND name <> 'goose_db_version' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing SQLite tables: %w", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tables := make([]table, 0, len(names))
	for _, name := range names {
		cols, err := db.Query(ctx, `SELECT name, type FROM pragma_table_info($1) ORDER BY cid`, name)
		if err != nil {
			return nil, fmt.Errorf("reading the columns of %s: %w", name, err)
		}
		t := table{name: name}
		for cols.Next() {
			var c column
			if err := cols.Scan(&c.name, &c.sqliteType); err != nil {
				cols.Close()
				return nil, err
			}
			c.sqliteType = strings.ToUpper(c.sqliteType)
			t.columns = append(t.columns, c)
		}
		cols.Close()
		if err := cols.Err(); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, nil
}

// matchPostgres pairs each SQLite column with its Postgres column and decides
// its conversion. It refuses a table or column on either side with no
// partner, since the copy would otherwise drop it or leave it unfilled.
func matchPostgres(ctx context.Context, pg pgx.Tx, schema []table) ([]table, error) {
	rows, err := pg.Query(ctx, `
		SELECT c.table_name, c.column_name, c.data_type, c.udt_name, c.numeric_scale
		FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = current_schema() AND t.table_type = 'BASE TABLE' AND c.table_name <> 'goose_db_version'`)
	if err != nil {
		return nil, fmt.Errorf("reading the Postgres columns: %w", err)
	}
	pgColumns := map[string]map[string]pgType{}
	for rows.Next() {
		var tableName, columnName string
		var t pgType
		if err := rows.Scan(&tableName, &columnName, &t.dataType, &t.udtName, &t.scale); err != nil {
			rows.Close()
			return nil, err
		}
		if pgColumns[tableName] == nil {
			pgColumns[tableName] = map[string]pgType{}
		}
		pgColumns[tableName][columnName] = t
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var problems []string
	out := make([]table, 0, len(schema))
	for _, t := range schema {
		source, ok := pgColumns[t.name]
		if !ok {
			problems = append(problems, fmt.Sprintf("table %s is missing from Postgres", t.name))
			continue
		}
		matched := table{name: t.name}
		for _, c := range t.columns {
			pgCol, ok := source[c.name]
			if !ok {
				problems = append(problems, fmt.Sprintf("column %s.%s is missing from Postgres", t.name, c.name))
				continue
			}
			k, err := classify(pgCol, c.sqliteType)
			if err != nil {
				problems = append(problems, fmt.Sprintf("column %s.%s: %v", t.name, c.name, err))
				continue
			}
			matched.columns = append(matched.columns, column{name: c.name, sqliteType: c.sqliteType, kind: k, pg: pgCol})
			delete(source, c.name)
		}
		for name := range source {
			problems = append(problems, fmt.Sprintf("Postgres column %s.%s has no place in the SQLite schema", t.name, name))
		}
		delete(pgColumns, t.name)
		out = append(out, matched)
	}
	for name := range pgColumns {
		problems = append(problems, fmt.Sprintf("Postgres table %s has no place in the SQLite schema", name))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("the Postgres and SQLite schemas do not match:\n  %s", strings.Join(problems, "\n  "))
	}
	return out, nil
}

func copyTable(ctx context.Context, pg pgx.Tx, tx *sqlitedb.Tx, t table) error {
	reads := make([]string, len(t.columns))
	names := make([]string, len(t.columns))
	params := make([]string, len(t.columns))
	for i, c := range t.columns {
		reads[i] = selectExpr(c.kind, c.pg, quote(c.name))
		names[i] = quote(c.name)
		params[i] = fmt.Sprintf("$%d", i+1)
	}
	insert := `INSERT INTO ` + quote(t.name) + ` (` + strings.Join(names, ", ") + `) VALUES (` + strings.Join(params, ", ") + `)`

	rows, err := pg.Query(ctx, `SELECT `+strings.Join(reads, ", ")+` FROM `+quote(t.name))
	if err != nil {
		return err
	}
	defer rows.Close()
	targets := make([]any, len(t.columns))
	values := make([]any, len(t.columns))
	for rows.Next() {
		for i, c := range t.columns {
			targets[i] = c.kind.scanTarget()
		}
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		for i, c := range t.columns {
			v, err := c.kind.value(targets[i])
			if err != nil {
				return fmt.Errorf("column %s: %w", c.name, err)
			}
			values[i] = v
		}
		if _, err := tx.Exec(ctx, insert, values...); err != nil {
			return err
		}
	}
	return rows.Err()
}

func foreignKeyCheck(ctx context.Context, tx *sqlitedb.Tx) error {
	rows, err := tx.Query(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("checking foreign keys: %w", err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var child, rowid, parent, fk any
		if err := rows.Scan(&child, &rowid, &parent, &fk); err != nil {
			return err
		}
		problems = append(problems, fmt.Sprintf("%v row %v points at a missing %v", child, rowid, parent))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d rows break a foreign key:\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	return nil
}

// verify compares each table's row count and each money column's total on
// the two sides: Postgres's exact decimal sum against SQLite's sum of
// hundredths.
func verify(ctx context.Context, pg pgx.Tx, tx *sqlitedb.Tx, tables []table) (Report, error) {
	var report Report
	for _, t := range tables {
		pgSums := []string{"count(*)"}
		sqliteSums := []string{"count(*)"}
		var money []string
		for _, c := range t.columns {
			if c.kind == kindMoney {
				money = append(money, c.name)
				pgSums = append(pgSums, "sum("+quote(c.name)+")::text")
				sqliteSums = append(sqliteSums, "sum("+quote(c.name)+")")
			}
		}

		var pgCount int64
		pgTotals := make([]*string, len(money))
		pgTargets := []any{&pgCount}
		for i := range pgTotals {
			pgTargets = append(pgTargets, &pgTotals[i])
		}
		if err := pg.QueryRow(ctx, `SELECT `+strings.Join(pgSums, ", ")+` FROM `+quote(t.name)).Scan(pgTargets...); err != nil {
			return Report{}, fmt.Errorf("totalling %s in Postgres: %w", t.name, err)
		}

		var sqliteCount int64
		sqliteTotals := make([]any, len(money))
		sqliteTargets := []any{&sqliteCount}
		for i := range sqliteTotals {
			sqliteTargets = append(sqliteTargets, &sqliteTotals[i])
		}
		if err := tx.QueryRow(ctx, `SELECT `+strings.Join(sqliteSums, ", ")+` FROM `+quote(t.name)).Scan(sqliteTargets...); err != nil {
			return Report{}, fmt.Errorf("totalling %s in SQLite: %w", t.name, err)
		}

		if pgCount != sqliteCount {
			return Report{}, fmt.Errorf("%s: Postgres has %d rows but SQLite has %d", t.name, pgCount, sqliteCount)
		}
		tr := TableReport{Table: t.name, Rows: sqliteCount}
		for i, name := range money {
			want := decimal.Zero
			if pgTotals[i] != nil {
				d, err := decimal.NewFromString(*pgTotals[i])
				if err != nil {
					return Report{}, fmt.Errorf("%s.%s: Postgres total %q: %w", t.name, name, *pgTotals[i], err)
				}
				want = d
			}
			got := decimal.Zero
			switch v := sqliteTotals[i].(type) {
			case nil:
			case int64:
				got = decimal.New(v, -2)
			default:
				return Report{}, fmt.Errorf("%s.%s: SQLite total is a %T, not an integer", t.name, name, v)
			}
			if !got.Equal(want) {
				return Report{}, fmt.Errorf("%s.%s: Postgres totals %s but SQLite totals %s", t.name, name, want.StringFixed(2), got.StringFixed(2))
			}
			tr.Money = append(tr.Money, ColumnSum{Column: name, Sum: got})
		}
		report.Tables = append(report.Tables, tr)
	}
	return report, nil
}

// quote is an identifier quoted for either database.
func quote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
