// Command jetgen writes go-jet's table and model packages for the household
// and server schemas into <out>/household and <out>/server. Each schema is
// built from the embedded migrations through sqlitedb, Go migrations
// included, and read back through the same pure-Go driver, so generating
// needs neither cgo nor an installed jet CLI.
//
// It removes <out> before writing, and refuses an <out> holding anything it
// did not write.
//
//	go run ./cmd/jetgen internal/gen/jet
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-jet/jet/v2/generator/metadata"
	jetsqlite "github.com/go-jet/jet/v2/generator/sqlite"
	"github.com/go-jet/jet/v2/generator/template"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/CornHead764/agentifi/backend/internal/moneycolumns"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: jetgen <out>")
		os.Exit(2)
	}
	if err := generate(context.Background(), os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// The migrations stamp nothing that reaches the schema with the clock, but a
// fixed one keeps the build independent of when it runs.
func clock() time.Time { return time.Unix(0, 0).UTC() }

type schema struct {
	name  string
	build func(ctx context.Context, path string) error
}

var schemas = []schema{
	{"household", func(ctx context.Context, path string) error {
		db, err := sqlitedb.OpenNewHousehold(ctx, path, clock)
		if err != nil {
			return err
		}
		return db.Close()
	}},
	{"server", func(ctx context.Context, path string) error {
		db, err := sqlitedb.Open(ctx, path, clock)
		if err != nil {
			return err
		}
		_, err = db.MigrateServer(ctx, "")
		return errors.Join(err, db.Close())
	}},
}

func generate(ctx context.Context, out string) error {
	if err := owned(out); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "jetgen-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	unclaimed := map[string]bool{}
	for column := range moneycolumns.Columns {
		unclaimed[column] = true
	}
	for _, s := range schemas {
		path := filepath.Join(dir, s.name+".db")
		if err := s.build(ctx, path); err != nil {
			return fmt.Errorf("jetgen: building the %s schema: %w", s.name, err)
		}
		if err := generateSchema(path, filepath.Join(out, s.name), unclaimed); err != nil {
			return fmt.Errorf("jetgen: %s: %w", s.name, err)
		}
	}
	if len(unclaimed) > 0 {
		return fmt.Errorf("jetgen: money columns in neither schema: %s", strings.Join(slices.Sorted(maps.Keys(unclaimed)), ", "))
	}
	return nil
}

// owned refuses to remove a directory that holds anything but a previous run's
// output, so a mistyped <out> cannot take a source tree with it.
func owned(out string) error {
	entries, err := os.ReadDir(out)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !slices.ContainsFunc(schemas, func(s schema) bool { return s.name == entry.Name() }) {
			return fmt.Errorf("jetgen: %s holds %s, which jetgen did not write; refusing to remove it", out, entry.Name())
		}
	}
	return nil
}

func generateSchema(path, out string, unclaimed map[string]bool) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	kinds, err := columnKinds(db, unclaimed)
	if err != nil {
		return err
	}
	return jetsqlite.GenerateDB(db, out, generatorTemplate(kinds))
}

type kind int

const (
	plain kind = iota
	uuidColumn
	flag
	amount
)

// A TEXT column holds a uuid when it is named id or *_id, or is one of
// alsoUUID, unless notUUID names it bare or as table.column.
var (
	notUUID = map[string]bool{
		"external_id": true, "simplefin_account_id": true, "known_category_id": true,
		"txf_id": true, "profile_id": true, "message_id": true, "client_id": true,
		"passkeys.rp_id":                       true, // the WebAuthn relying party, a host name
		"tombstones.row_id":                    true, // a composite key is a JSON array
		"assistant_automation_runs.decided_by": true, // who decided: "model", "rule", …
	}
	alsoUUID = map[string]bool{
		"created_by": true, "corrected_by": true, "decided_by": true, "household_uuid": true,
	}
)

func isUUID(table, column, typ string) bool {
	return typ == "TEXT" && !notUUID[column] && !notUUID[table+"."+column] &&
		(column == "id" || strings.HasSuffix(column, "_id") || alsoUUID[column])
}

// pragma_table_info does not report CHECK constraints, so flags are read
// from the DDL; every 0/1 constraint in the schema is written this way.
var flagCheck = regexp.MustCompile(`(?m)^\s*(\w+) INTEGER\b.*\bCHECK \((\w+) IN \(0, 1\)\)`)

// columnKinds maps table.column to its kind for every column whose Go type
// is not jet's default. A money column found here is removed from unclaimed.
func columnKinds(db *sql.DB, unclaimed map[string]bool) (map[string]kind, error) {
	kinds := map[string]kind{}
	rows, err := db.Query(`
		SELECT m.name, m.sql, p.name, p.type
		FROM sqlite_schema m, pragma_table_info(m.name) p
		WHERE m.type = 'table'
		ORDER BY m.name, p.cid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parsed := map[string]bool{}
	for rows.Next() {
		var table, ddl, column, typ string
		if err := rows.Scan(&table, &ddl, &column, &typ); err != nil {
			return nil, err
		}
		if !parsed[table] {
			parsed[table] = true
			for _, m := range flagCheck.FindAllStringSubmatch(ddl, -1) {
				if m[1] == m[2] {
					kinds[table+"."+m[1]] = flag
				}
			}
		}
		key := table + "." + column
		_, money := moneycolumns.Columns[key]
		switch {
		case money:
			if typ != "INTEGER" {
				return nil, fmt.Errorf("money column %s is %s, not INTEGER", key, typ)
			}
			kinds[key] = amount
			delete(unclaimed, key)
		case isUUID(table, column, typ):
			kinds[key] = uuidColumn
		}
	}
	return kinds, rows.Err()
}

var (
	uuidType   = template.NewType(uuid.UUID{})
	boolType   = template.NewType(true)
	amountType = template.Type{
		ImportPath: "github.com/CornHead764/agentifi/backend/internal/store/minor",
		Name:       "minor.Amount",
	}
)

func generatorTemplate(kinds map[string]kind) template.Template {
	return template.Default(sqlite.Dialect).UseSchema(func(s metadata.Schema) template.Schema {
		return template.DefaultSchema(s).
			UseModel(template.DefaultModel().UseTable(func(t metadata.Table) template.TableModel {
				model := template.DefaultTableModel(t).UseField(func(c metadata.Column) template.TableModelField {
					field := template.DefaultTableModelField(c)
					var typ template.Type
					switch kinds[t.Name+"."+c.Name] {
					case uuidColumn:
						typ = uuidType
					case flag:
						typ = boolType
					case amount:
						typ = amountType
					default:
						return field
					}
					if c.IsNullable {
						typ.Name = "*" + typ.Name
					}
					return field.UseType(typ)
				})
				model.Skip = t.Name == "goose_db_version"
				return model
			})).
			UseSQLBuilder(template.DefaultSQLBuilder().UseTable(func(t metadata.Table) template.TableSQLBuilder {
				builder := template.DefaultTableSQLBuilder(t).UseColumn(func(c metadata.Column) template.TableSQLBuilderColumn {
					column := template.DefaultTableSQLBuilderColumn(c)
					if kinds[t.Name+"."+c.Name] == flag {
						column.Type = "Bool"
					}
					return column
				})
				builder.Skip = t.Name == "goose_db_version"
				return builder
			}))
	})
}
