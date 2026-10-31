package main

import (
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/pgimport"
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
		"household/Transactions.CategoryID":           "*uuid.UUID",
		"household/Transactions.ExternalID":           "*string",
		"household/Transactions.Amount":               "minor.Amount",
		"household/Transactions.AmountPrimary":        "*minor.Amount",
		"household/Transactions.IsPending":            "bool",
		"household/Transactions.CreatedAt":            "int64",
		"household/Accounts.PendingHolds":             "minor.Amount",
		"household/Accounts.RequiresReceipts":         "*bool",
		"household/Tombstones.RowID":                  "string",
		"household/AssistantAutomationRuns.DecidedBy": "string",
		"household/DuplicateCandidates.DecidedBy":     "*uuid.UUID",
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

func TestEveryImportedMoneyColumnIsMoney(t *testing.T) {
	for column := range pgimport.MoneyCurrencies {
		require.True(t, money[column], "%s is money in pgimport.MoneyCurrencies", column)
	}
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
	require.NoError(t, os.WriteFile(stale, []byte("package model\n"), 0o644))
	require.NoError(t, generate(t.Context(), out))
	_, err := os.Stat(stale)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestItRefusesADirectoryItDidNotWrite(t *testing.T) {
	out := t.TempDir()
	notes := filepath.Join(out, "notes.txt")
	require.NoError(t, os.WriteFile(notes, []byte("mine"), 0o644))
	require.ErrorContains(t, generate(t.Context(), out), "refusing to remove it")
	_, err := os.Stat(notes)
	require.NoError(t, err)
}

// store imports the generated packages, so the generator must build without
// store or anything else of this module but the schema.
func TestItImportsOnlyTheMigrationsAndSqlitedb(t *testing.T) {
	const module = "github.com/CornHead764/agentifi/backend/"
	allowed := []string{module + "internal/sqlitedb", module + "migrations/household", module + "migrations/server"}
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, module) && !slices.Contains(allowed, path) {
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
}
