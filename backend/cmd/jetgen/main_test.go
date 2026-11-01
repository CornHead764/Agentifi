package main

import (
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-jet/jet/v2/generator/metadata"
	"github.com/go-jet/jet/v2/generator/template"
	"github.com/stretchr/testify/require"
)

func generated(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "jet")
	require.NoError(t, generate(t.Context(), out))
	return out
}

// fieldTypes reads every generated model struct as schema/Type.Field → type.
func fieldTypes(t *testing.T, out string) map[string]string {
	t.Helper()
	fields := map[string]string{}
	for _, s := range schemas {
		files, err := filepath.Glob(filepath.Join(out, s.name, "model", "*.go"))
		require.NoError(t, err)
		require.NotEmpty(t, files)
		for _, path := range files {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			require.NoError(t, err)
			ast.Inspect(file, func(n ast.Node) bool {
				spec, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				for _, field := range spec.Type.(*ast.StructType).Fields.List {
					for _, name := range field.Names {
						fields[s.name+"/"+spec.Name.Name+"."+name.Name] = types.ExprString(field.Type)
					}
				}
				return false
			})
		}
	}
	return fields
}

func TestColumnsGetTheirGoTypes(t *testing.T) {
	out := generated(t)
	fields := fieldTypes(t, out)
	for field, want := range map[string]string{
		"household/Transactions.ID":                   "uuid.UUID",
		"household/Transactions.CategoryID":           "nullid.ID",
		"household/Transactions.ExternalID":           "*string",
		"household/Transactions.Amount":               "minor.Amount",
		"household/Transactions.AmountPrimary":        "*minor.Amount",
		"household/Transactions.IsPending":            "bool",
		"household/Transactions.CreatedAt":            "int64",
		"household/Accounts.PendingHolds":             "minor.Amount",
		"household/Accounts.RequiresReceipts":         "*bool",
		"household/Tombstones.RowID":                  "string",
		"household/AssistantAutomationRuns.DecidedBy": "string",
		"household/DuplicateCandidates.DecidedBy":     "nullid.ID",
		"household/HouseholdMetadata.HouseholdUUID":   "uuid.UUID",
		"server/Users.ID":                             "uuid.UUID",
		"server/Users.IsActive":                       "bool",
		"server/Passkeys.RpID":                        "*string",
	} {
		require.Equal(t, want, fields[field], field)
	}
	table, err := os.ReadFile(filepath.Join(out, "household", "table", "accounts.go"))
	require.NoError(t, err)
	require.Regexp(t, `ExcludedFromReports\s+sqlite\.ColumnBool`, string(table))
	_, err = os.Stat(filepath.Join(out, "household", "table", "goose_db_version.go"))
	require.ErrorIs(t, err, os.ErrNotExist, "goose's own table is generated")
}

// A nullable uuid read into a *uuid.UUID is filled from the store's uuid.Nil
// as easily as from a real id, and writes the zero uuid where NULL belongs.
func TestNullableUUIDColumns(t *testing.T) {
	out := generated(t)
	fields := fieldTypes(t, out)
	dir := t.TempDir()
	nullable := 0
	for _, s := range schemas {
		path := filepath.Join(dir, s.name+".db")
		require.NoError(t, s.build(t.Context(), path))
		db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
		require.NoError(t, err)
		kinds, err := columnKinds(db, map[string]bool{})
		require.NoError(t, err)
		rows, err := db.Query(`SELECT m.name, p.name, p."notnull" FROM sqlite_schema m, pragma_table_info(m.name) p
			WHERE m.type = 'table' AND m.name <> 'goose_db_version'`)
		require.NoError(t, err)
		for rows.Next() {
			var table, column string
			var notNull bool
			require.NoError(t, rows.Scan(&table, &column, &notNull))
			if kinds[table+"."+column] != uuidColumn {
				continue
			}
			field := s.name + "/" + template.DefaultTableModel(metadata.Table{Name: table}).TypeName + "." +
				template.DefaultTableModelField(metadata.Column{Name: column}).Name
			want := "uuid.UUID"
			if !notNull {
				want = "nullid.ID"
				nullable++
			}
			require.Equal(t, want, fields[field], "%s.%s", table, column)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, db.Close())
	}
	require.Greater(t, nullable, 50)
	t.Logf("%d nullable uuid columns", nullable)
}

// The legacy Postgres schema typed its uuid columns, so every column it shares
// with SQLite says whether the name rule is right.
func TestUUIDColumnsAreTheLegacySchemasUUIDs(t *testing.T) {
	legacy, err := os.ReadFile("../../internal/pgimport/testdata/00001_initial.sql")
	require.NoError(t, err)
	pgTypes := map[string]string{}
	columnRE := regexp.MustCompile(`(?m)^\s+(\w+) ([\w\[\]]+)`)
	for _, m := range regexp.MustCompile(`CREATE TABLE (\w+) \(([\s\S]*?)\n\);`).FindAllStringSubmatch(string(legacy), -1) {
		for _, c := range columnRE.FindAllStringSubmatch(m[2], -1) {
			if c[1] != "CONSTRAINT" {
				pgTypes[m[1]+"."+c[1]] = c[2]
			}
		}
	}
	dir := t.TempDir()
	compared := 0
	for _, s := range schemas {
		path := filepath.Join(dir, s.name+".db")
		require.NoError(t, s.build(t.Context(), path))
		db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
		require.NoError(t, err)
		kinds, err := columnKinds(db, map[string]bool{})
		require.NoError(t, err)
		rows, err := db.Query(`SELECT m.name || '.' || p.name FROM sqlite_schema m, pragma_table_info(m.name) p WHERE m.type = 'table'`)
		require.NoError(t, err)
		for rows.Next() {
			var key string
			require.NoError(t, rows.Scan(&key))
			if pgType, ok := pgTypes[key]; ok {
				compared++
				require.Equal(t, pgType == "uuid", kinds[key] == uuidColumn, "%s was %s in Postgres", key, pgType)
			}
		}
		require.NoError(t, rows.Err())
		require.NoError(t, db.Close())
	}
	require.Greater(t, compared, 500)
}

func TestAMoneyColumnMustBeInteger(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE transactions (id TEXT, amount TEXT)`)
	require.NoError(t, err)
	_, err = columnKinds(db, map[string]bool{})
	require.ErrorContains(t, err, "money column transactions.amount is TEXT")
}

func TestItReplacesItsOwnOutput(t *testing.T) {
	out := generated(t)
	stale := filepath.Join(out, "household", "model", "dropped_table.go")
	require.NoError(t, os.WriteFile(stale, []byte(generatedHeader+"\npackage model\n"), 0o644))
	require.NoError(t, generate(t.Context(), out))
	_, err := os.Stat(stale)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// refused checks that generate refuses out and leaves every file in it as
// it was.
func refused(t *testing.T, out string) {
	t.Helper()
	before := tree(t, out)
	require.NotEmpty(t, before)
	require.ErrorContains(t, generate(t.Context(), out), "refusing to remove it")
	require.Equal(t, before, tree(t, out))
}

func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		var body []byte
		if entry.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			body = []byte("-> " + target)
			require.NoError(t, err)
		} else {
			body, err = os.ReadFile(path)
			require.NoError(t, err)
		}
		files[path] = string(body)
		return nil
	}))
	return files
}

func TestItRefusesADirectoryItDidNotWrite(t *testing.T) {
	out := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(out, "notes.txt"), []byte("mine"), 0o644))
	refused(t, out)
}

// backend/migrations has the same two top-level directories as the output.
func TestItRefusesTheMigrations(t *testing.T) {
	out := filepath.Join(t.TempDir(), "migrations")
	require.NoError(t, os.MkdirAll(filepath.Join(out, "household"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(out, "server"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(out, "household", "00001_initial.sql"), []byte("-- +goose Up\n"), 0o644))
	refused(t, out)

	real := filepath.Join(t.TempDir(), "migrations")
	require.NoError(t, os.CopyFS(real, os.DirFS("../../migrations")))
	refused(t, real)
}

func TestItRefusesAFileWithoutGoJetsHeader(t *testing.T) {
	out := generated(t)
	require.NoError(t, os.WriteFile(filepath.Join(out, "household", "model", "notes.go"), []byte("package model\n"), 0o644))
	refused(t, out)
}

func TestItRefusesASymlink(t *testing.T) {
	out := generated(t)
	require.NoError(t, os.Symlink("../../../migrations", filepath.Join(out, "household", "model", "link.go")))
	refused(t, out)
}

func TestItRefusesTheWorkingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "work")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644))
	t.Chdir(dir)
	for _, out := range []string{".", "..", "", dir} {
		before := tree(t, dir)
		require.ErrorContains(t, generate(t.Context(), out), "refusing to remove it", "%q", out)
		require.Equal(t, before, tree(t, dir))
	}
}

// store imports the generated packages, so the generator must build without
// store or anything else of this module but the schema, directly or through
// any package it imports.
func TestItDependsOnlyOnTheSchema(t *testing.T) {
	const module = "github.com/CornHead764/agentifi/backend/"
	allowed := []string{
		module + "cmd/jetgen",
		module + "internal/moneycolumns",
		module + "internal/sqlitedb",
		module + "migrations/household",
		module + "migrations/server",
	}
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	require.NoError(t, err)
	deps := strings.Fields(string(out))
	require.Contains(t, deps, module+"internal/sqlitedb")
	for _, path := range deps {
		if strings.HasPrefix(path, module) {
			require.Contains(t, allowed, path, "jetgen depends on %s", path)
		}
	}
}
