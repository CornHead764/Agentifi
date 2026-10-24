package sqlitedb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/decimaltext"
	"github.com/CornHead764/agentifi/backend/migrations/household"
	"github.com/CornHead764/agentifi/backend/migrations/server"
	"github.com/pressly/goose/v3"
)

func TestOuterWriterRequiresCallbackHandle(t *testing.T) {
	d := openTestDB(t)
	execTest(t, d.Write(), "CREATE TABLE example (id INTEGER) STRICT")
	err := d.InTx(t.Context(), func(tx *DB) error {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		_, err := d.Write().ExecContext(ctx, "INSERT INTO example VALUES (1)")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("outer writer = %v; want bounded context error", err)
		}
		_, err = tx.Write().ExecContext(t.Context(), "INSERT INTO example VALUES (2)")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalDecimalChecks(t *testing.T) {
	d := openTestDB(t)
	execTest(t, d.Write(), "CREATE TABLE decimals (value TEXT CHECK ("+decimaltext.Check("value")+")) STRICT")
	for _, value := range []string{"0", "1", "-1", "0.1", "-0.00001", "12345678901234567890.1234567890123456789"} {
		if _, err := decimaltext.Parse(value); err != nil {
			t.Fatal(err)
		}
		execTest(t, d.Write(), "INSERT INTO decimals VALUES (?)", value)
	}
	for _, value := range []string{"", "-0", "+1", "01", "00.1", "1.", ".1", "1.0", "-0.0", "1e2", "--1", "1-2", "1..2", " 1", "NaN"} {
		if _, err := decimaltext.Parse(value); err == nil {
			t.Errorf("Parse accepted %q", value)
		}
		if _, err := d.Write().ExecContext(t.Context(), "INSERT INTO decimals VALUES (?)", value); err == nil {
			t.Errorf("CHECK accepted %q", value)
		}
	}
	for _, schema := range []struct {
		text    []byte
		columns []string
	}{
		{mustSchema(t, household.FS.ReadFile), []string{"interest_rate", "threshold_pct", "shares", "average_cost", "last_price", "prior_close", "fx_rate_used"}},
		{mustSchema(t, server.FS.ReadFile), []string{"rate", "close"}},
	} {
		for _, column := range schema.columns {
			if !strings.Contains(string(schema.text), "CHECK ("+decimaltext.Check(column)+")") {
				t.Errorf("missing canonical CHECK for %s", column)
			}
		}
	}
}

func mustSchema(t *testing.T, read func(string) ([]byte, error)) []byte {
	t.Helper()
	b, err := read("00001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSQLiteBaselinesDownAndUp(t *testing.T) {
	for _, name := range []string{"server", "household"} {
		t.Run(name, func(t *testing.T) {
			d := openTestDB(t)
			fs := server.FS
			if name == "household" {
				fs = household.FS
			}
			provider, err := goose.NewProvider(goose.DialectSQLite3, d.write, fs)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = provider.Up(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err = provider.Down(t.Context()); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = d.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("remaining schema objects = %d, %v", count, err)
			}
			if _, err = provider.Up(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertHealthySQLite(t, d)
		})
	}
}
