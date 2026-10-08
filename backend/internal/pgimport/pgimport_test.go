package pgimport

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
)

const (
	spaceID      = "11111111-1111-4111-8111-111111111111"
	userID       = "22222222-2222-4222-8222-222222222222"
	accountID    = "33333333-3333-4333-8333-333333333333"
	categoryID   = "44444444-4444-4444-8444-444444444444"
	firstTxnID   = "55555555-5555-4555-8555-555555555551"
	secondTxnID  = "55555555-5555-4555-8555-555555555552"
	securityID   = "66666666-6666-4666-8666-666666666666"
	tagID        = "77777777-7777-4777-8777-777777777777"
	seriesID     = "88888888-8888-4888-8888-888888888888"
	automationID = "99999999-9999-4999-8999-999999999999"
)

// The rows are invented. Each table holds a column of a kind the copy
// converts: money, a rate, a percentage stored as text, arrays of uuids,
// strings and integers, jsonb, bytea, a bigint, a double, dates, timestamps
// and booleans. accounts sorts before spaces, and the second transaction
// points at the first, so the copy meets children before their parents.
var seed = []string{
	`INSERT INTO spaces (id, name, primary_currency, timezone, setup_guide_skipped, sidebar_account_types)
	 VALUES ('` + spaceID + `', 'Example Household', 'USD', 'UTC', '{budget,goals}', '["checking", "savings"]')`,
	`INSERT INTO users (id, email, locale, theme, created_at)
	 VALUES ('` + userID + `', 'person@example.com', 'en-US', 'system', '2026-01-01 00:00:00+00')`,
	`INSERT INTO accounts (id, name, kind, type, currency, sort_order, space_id, provider_balance, credit_limit,
	                      interest_rate, opening_balance_on, provider_extra, excluded_from_reports)
	 VALUES ('` + accountID + `', 'Example Checking', 'bank', 'checking', 'USD', 0, '` + spaceID + `', 2000.00, NULL,
	         0.0425000000, '2026-01-01', '{"note": "invented"}', true)`,
	`INSERT INTO categories (id, name, kind, sort_order, space_id, txf_ids)
	 VALUES ('` + categoryID + `', 'Groceries', 'expense', 0, '` + spaceID + `', '{N100,N200}')`,
	`INSERT INTO transactions (id, account_id, date, effective_date, amount, currency, source, space_id,
	                          category_id, category_from_pair, provider_extra, created_at)
	 VALUES ('` + firstTxnID + `', '` + accountID + `', '2026-01-15', '2026-01-16', -1250.50, 'USD', 'simplefin',
	         '` + spaceID + `', '` + categoryID + `', true, '{"memo": "invented"}', '2026-01-15 03:30:00.123456-05')`,
	`INSERT INTO transactions (id, account_id, date, amount, currency, amount_primary, fx_rate_used, source, space_id,
	                          padded_txn_id)
	 VALUES ('` + secondTxnID + `', '` + accountID + `', '2026-02-01', 3000.00, 'EUR', 2750.25, 0.9167500000, 'manual',
	         '` + spaceID + `', '` + firstTxnID + `')`,
	`INSERT INTO alert_rules (id, user_id, alert_type, space_id, threshold_amount, threshold_pct)
	 VALUES ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', '` + userID + `', 'large_transaction', '` + spaceID + `', 500.00, 25.50)`,
	`INSERT INTO securities (id, symbol, name, kind, currency, space_id)
	 VALUES ('` + securityID + `', 'EXMPL', 'Example Fund', 'fund', 'USD', '` + spaceID + `')`,
	`INSERT INTO holdings (id, account_id, security_id, shares, cost_basis, average_cost, as_of, space_id)
	 VALUES ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', '` + accountID + `', '` + securityID + `', 12.3456789012, 1000.00,
	         81.0000000001, '2026-03-01', '` + spaceID + `')`,
	`INSERT INTO passkeys (id, user_id, credential_id, public_key, sign_count, name)
	 VALUES ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', '` + userID + `', '\x0102ff', '\x00', 9000000000, 'Example key')`,
	`INSERT INTO series (id, account_id, kind, description, currency, alias, "interval", start_on, reminder_days,
	                    match_criteria, space_id, by_month_day, by_day, template_tag_ids, match_amount_min)
	 VALUES ('` + seriesID + `', '` + accountID + `', 'bill', 'EXAMPLE POWER CO', 'USD', 'monthly', 1, '2026-01-01', 3,
	         'amount', '` + spaceID + `', '{1,15}', '{MO}', '{` + strings.ToUpper(tagID) + `}', 99.99)`,
	`INSERT INTO assistant_automations (id, space_id, created_by, name, trigger, prompt, trigger_config, confidence_threshold)
	 VALUES ('` + automationID + `', '` + spaceID + `', '` + userID + `', 'Example automation', 'manual',
	         'An invented prompt', '{"day": 1}', 0.75)`,
}

// postgres is a fresh Postgres database holding the Postgres schema at
// goose version 1 and the seed rows. It skips without TEST_DATABASE_URL or a
// reachable server, unless CI is set.
func postgres(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	unavailable := func(format string, args ...any) {
		t.Helper()
		if os.Getenv("CI") != "" {
			t.Fatalf(format, args...)
		}
		t.Skipf(format, args...)
	}
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		unavailable("TEST_DATABASE_URL is not set")
	}
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		unavailable("no Postgres at TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(func() { admin.Close(context.Background()) })

	name := fmt.Sprintf("agentifi_pgimport_test_%d", os.Getpid())
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})

	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = name
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })

	schema, err := os.ReadFile(filepath.Join("..", "..", "migrations", "00001_initial.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up := string(schema)
	up = up[strings.Index(up, "-- +goose Up"):strings.Index(up, "-- +goose Down")]
	if _, err := conn.Exec(ctx, up); err != nil {
		t.Fatalf("applying the Postgres schema: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE goose_db_version (
			id serial PRIMARY KEY,
			version_id bigint NOT NULL,
			is_applied boolean NOT NULL,
			tstamp timestamp DEFAULT now()
		);
		INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, true), (1, true);`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range seed {
		if _, err := conn.Exec(ctx, statement); err != nil {
			t.Fatalf("seeding: %v\n%s", err, statement)
		}
	}
	return conn
}

func TestImportCopiesEveryRow(t *testing.T) {
	ctx := context.Background()
	src := postgres(t)
	path := filepath.Join(t.TempDir(), "agentifi.db")

	dst, err := OpenTarget(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	report, err := Import(ctx, src, dst)
	if err != nil {
		t.Fatal(err)
	}

	if got := report.Rows(); got != int64(len(seed)) {
		t.Errorf("copied %d rows, want %d", got, len(seed))
	}
	for _, table := range report.Tables {
		if table.Table != "transactions" {
			continue
		}
		if table.Rows != 2 {
			t.Errorf("transactions: %d rows, want 2", table.Rows)
		}
		sums := map[string]string{}
		for _, m := range table.Money {
			sums[m.Column] = m.Sum.StringFixed(2)
		}
		if sums["amount"] != "1749.50" || sums["amount_primary"] != "2750.25" {
			t.Errorf("transactions money totals %v, want amount 1749.50 and amount_primary 2750.25", sums)
		}
	}

	db := dst.Pool()
	for _, tc := range []struct {
		query string
		want  any
	}{
		{`SELECT amount FROM transactions WHERE id = '` + firstTxnID + `'`, int64(-125050)},
		{`SELECT date FROM transactions WHERE id = '` + firstTxnID + `'`, "2026-01-15"},
		{`SELECT effective_date FROM transactions WHERE id = '` + firstTxnID + `'`, "2026-01-16"},
		{`SELECT created_at FROM transactions WHERE id = '` + firstTxnID + `'`, "2026-01-15T08:30:00.123456Z"},
		{`SELECT provider_extra FROM transactions WHERE id = '` + firstTxnID + `'`, `{"memo":"invented"}`},
		{`SELECT category_from_pair FROM transactions WHERE id = '` + firstTxnID + `'`, int64(1)},
		{`SELECT category_id FROM transactions WHERE id = '` + firstTxnID + `'`, categoryID},
		{`SELECT effective_date FROM transactions WHERE id = '` + secondTxnID + `'`, nil},
		{`SELECT fx_rate_used FROM transactions WHERE id = '` + secondTxnID + `'`, "0.91675"},
		{`SELECT amount_primary FROM transactions WHERE id = '` + secondTxnID + `'`, int64(275025)},
		{`SELECT padded_txn_id FROM transactions WHERE id = '` + secondTxnID + `'`, firstTxnID},
		{`SELECT excluded_from_reports FROM accounts`, int64(1)},
		{`SELECT excluded_from_spending_plan FROM accounts`, int64(0)},
		{`SELECT provider_balance FROM accounts`, int64(200000)},
		{`SELECT credit_limit FROM accounts`, nil},
		{`SELECT interest_rate FROM accounts`, "0.0425"},
		{`SELECT opening_balance_on FROM accounts`, "2026-01-01"},
		{`SELECT threshold_amount FROM alert_rules`, int64(50000)},
		{`SELECT threshold_pct FROM alert_rules`, "25.5"},
		{`SELECT shares FROM holdings`, "12.3456789012"},
		{`SELECT average_cost FROM holdings`, "81.0000000001"},
		{`SELECT cost_basis FROM holdings`, int64(100000)},
		{`SELECT sign_count FROM passkeys`, int64(9000000000)},
		{`SELECT "interval" FROM series`, int64(1)},
		{`SELECT by_month_day FROM series`, "[1,15]"},
		{`SELECT by_day FROM series`, `["MO"]`},
		{`SELECT by_month FROM series`, "[]"},
		{`SELECT template_tag_ids FROM series`, `["` + tagID + `"]`},
		{`SELECT match_amount_min FROM series`, int64(9999)},
		{`SELECT setup_guide_skipped FROM spaces`, `["budget","goals"]`},
		{`SELECT sidebar_account_types FROM spaces`, `["checking","savings"]`},
		{`SELECT txf_ids FROM categories`, `["N100","N200"]`},
		{`SELECT confidence_threshold FROM assistant_automations`, 0.75},
		{`SELECT trigger_config FROM assistant_automations`, `{"day":1}`},
		{`SELECT created_at FROM users`, "2026-01-01T00:00:00.000000Z"},
	} {
		var got any
		if err := db.QueryRow(ctx, tc.query).Scan(&got); err != nil {
			t.Errorf("%s: %v", tc.query, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %#v, want %#v", tc.query, got, tc.want)
		}
	}

	var credential []byte
	if err := db.QueryRow(ctx, `SELECT credential_id FROM passkeys`).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(credential, []byte{0x01, 0x02, 0xff}) {
		t.Errorf("credential_id = %x, want 0102ff", credential)
	}

	var amount dbconv.Number
	if err := db.QueryRow(ctx, `SELECT amount FROM transactions WHERE id = $1`, firstTxnID).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if !amount.Decimal.Equal(decimal.RequireFromString("-1250.50")) {
		t.Errorf("the server reads the amount as %s, want -1250.50", amount.Decimal)
	}

	if _, err := OpenTarget(ctx, path); err == nil || !strings.Contains(err.Error(), "already holds data") {
		t.Errorf("a second import into the same file: %v, want a refusal", err)
	}
}

func TestImportRollsBackOnAFailedRow(t *testing.T) {
	ctx := context.Background()
	src := postgres(t)
	if _, err := src.Exec(ctx, `UPDATE transactions SET accepted_on = 'infinity' WHERE id = '`+secondTxnID+`'`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agentifi.db")

	dst, err := OpenTarget(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Import(ctx, src, dst)
	dst.Close()
	if err == nil || !strings.Contains(err.Error(), "accepted_on") {
		t.Fatalf("import of an infinite date: %v, want an error naming accepted_on", err)
	}

	again, err := OpenTarget(ctx, path)
	if err != nil {
		t.Fatalf("the failed import left rows behind: %v", err)
	}
	again.Close()
}

func TestImportRefusesAnotherPostgresVersion(t *testing.T) {
	ctx := context.Background()
	src := postgres(t)
	if _, err := src.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (2, true)`); err != nil {
		t.Fatal(err)
	}
	dst, err := OpenTarget(ctx, filepath.Join(t.TempDir(), "agentifi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err := Import(ctx, src, dst); err == nil || !strings.Contains(err.Error(), "schema version 2") {
		t.Errorf("import at version 2: %v, want a refusal naming the version", err)
	}
}

func TestImportRefusesAColumnSQLiteLacks(t *testing.T) {
	ctx := context.Background()
	src := postgres(t)
	if _, err := src.Exec(ctx, `ALTER TABLE spaces ADD COLUMN motto text`); err != nil {
		t.Fatal(err)
	}
	dst, err := OpenTarget(ctx, filepath.Join(t.TempDir(), "agentifi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err := Import(ctx, src, dst); err == nil || !strings.Contains(err.Error(), "spaces.motto") {
		t.Errorf("import with an extra column: %v, want a refusal naming spaces.motto", err)
	}
}

func TestImportRefusesARowWithoutItsParent(t *testing.T) {
	ctx := context.Background()
	src := postgres(t)
	if _, err := src.Exec(ctx, `
		ALTER TABLE categories DISABLE TRIGGER ALL;
		UPDATE categories SET parent_id = 'dddddddd-dddd-4ddd-8ddd-dddddddddddd';
		ALTER TABLE categories ENABLE TRIGGER ALL;`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agentifi.db")
	dst, err := OpenTarget(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Import(ctx, src, dst)
	dst.Close()
	if err == nil || !strings.Contains(err.Error(), "categories row") {
		t.Fatalf("import of a dangling parent_id: %v, want a foreign key refusal naming categories", err)
	}
	again, err := OpenTarget(ctx, path)
	if err != nil {
		t.Fatalf("the refused import left rows behind: %v", err)
	}
	again.Close()
}
