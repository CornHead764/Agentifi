package pgimport

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/testenv"
	household "github.com/CornHead764/agentifi/backend/migrations/household"
	server "github.com/CornHead764/agentifi/backend/migrations/server"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

// The importer still needs a genuine legacy PostgreSQL source, never a
// PostgreSQL destination or a Store adapter. Other unit tests run without it.
func fixture(t *testing.T) (*pgx.Conn, Options) {
	t.Helper()
	source := os.Getenv("TEST_DATABASE_URL")
	if source == "" {
		testenv.Missing(t, "TEST_DATABASE_URL is unset; legacy PostgreSQL source unavailable")
	}
	admin, err := pgx.Connect(t.Context(), source)
	if err != nil {
		testenv.Missing(t, "legacy PostgreSQL source unavailable: %v", err)
	}
	schema := fmt.Sprintf("agentifi_pgimport_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = admin.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = admin.Close(context.Background())
	})
	u, err := url.Parse(source)
	require.NoError(t, err)
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	sourceURL := u.String()
	sqlDB, err := sql.Open("pgx", sourceURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("testdata"))
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	db, err := pgx.Connect(t.Context(), sourceURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	// Identical catalogue rows in separate households exercise deduplication
	// and the conflict refusal cases. Nonzero JPY/KWD amounts exercise scaling.
	_, err = db.Exec(t.Context(), `
INSERT INTO spaces (id,name,primary_currency,timezone)
SELECT ('00000000-0000-0000-0000-'||lpad(i::text,12,'0'))::uuid,
       'Household '||i,'USD','UTC' FROM generate_series(1,3) i;
INSERT INTO users (id,email,locale,theme)
VALUES ('10000000-0000-0000-0000-000000000001','import@example.test','en','system');
INSERT INTO memberships (id,user_id,role,space_id)
SELECT gen_random_uuid(),'10000000-0000-0000-0000-000000000001','owner',id FROM spaces;
INSERT INTO accounts (id,name,kind,type,currency,sort_order,opening_balance,space_id)
SELECT gen_random_uuid(),'Yen account','asset','checking','JPY',0,1500,id FROM spaces;
INSERT INTO accounts (id,name,kind,type,currency,sort_order,opening_balance,space_id)
SELECT gen_random_uuid(),'Dinar account','asset','checking','KWD',1,1.23,id FROM spaces;
INSERT INTO fx_rates (id,base_currency,quote_currency,date,rate,source,space_id,created_at,updated_at)
SELECT gen_random_uuid(),'USD','JPY','2026-01-01',150,'fixture',id,'2026-01-01','2026-01-01' FROM spaces;
INSERT INTO securities (id,symbol,name,kind,currency,space_id,created_at,updated_at)
SELECT gen_random_uuid(),'TEST','Test security','stock','USD',id,'2026-01-01','2026-01-01' FROM spaces;
INSERT INTO security_prices (id,space_id,security_id,on_date,close,created_at)
SELECT gen_random_uuid(),space_id,id,'2026-01-01',12.34,'2026-01-01' FROM securities;
`)
	require.NoError(t, err)
	return db, Options{From: sourceURL, Data: t.TempDir(), StoragePath: t.TempDir()}
}
func TestSeededImport(t *testing.T) {
	db, o := fixture(t)
	var out bytes.Buffer
	o.Output = &out
	report, e := Import(t.Context(), o)
	require.NoError(t, e)
	require.Len(t, report.Households, 3)
	require.Contains(t, out.String(), "verified counts")
	var id, house string
	require.NoError(t, db.QueryRow(t.Context(), "SELECT id::text,space_id::text FROM accounts WHERE currency='JPY' LIMIT 1").Scan(&id, &house))
	d, e := sqlitedb.Open(t.Context(), filepath.Join(o.Data, "households", house+".db"), time.Now)
	require.NoError(t, e)
	defer d.Close()
	var got int64
	require.NoError(t, d.Read().QueryRowContext(t.Context(), "SELECT opening_balance FROM accounts WHERE id=?", id).Scan(&got))
	var want int64
	require.NoError(t, db.QueryRow(t.Context(), "SELECT opening_balance::bigint FROM accounts WHERE id=$1", id).Scan(&want))
	require.Equal(t, want, got)
	var kwdid, kwdhouse string
	require.NoError(t, db.QueryRow(t.Context(), "SELECT id::text,space_id::text FROM accounts WHERE currency='KWD' LIMIT 1").Scan(&kwdid, &kwdhouse))
	kd, e := sqlitedb.Open(t.Context(), filepath.Join(o.Data, "households", kwdhouse+".db"), time.Now)
	require.NoError(t, e)
	defer kd.Close()
	require.NoError(t, kd.Read().QueryRowContext(t.Context(), "SELECT opening_balance FROM accounts WHERE id=?", kwdid).Scan(&got))
	require.NoError(t, db.QueryRow(t.Context(), "SELECT (opening_balance*1000)::bigint FROM accounts WHERE id=$1", kwdid).Scan(&want))
	require.Equal(t, want, got)
}

func TestImportPreservesDefaultLayoutLegacyAttachments(t *testing.T) {
	source, o := fixture(t)
	o.StoragePath = filepath.Join(o.Data, "attachments")
	require.NoError(t, os.MkdirAll(filepath.Join(o.StoragePath, "legacy-space"), 0700))
	content := []byte("legacy receipt")
	require.NoError(t, os.WriteFile(filepath.Join(o.StoragePath, "legacy-space", "receipt.pdf"), content, 0600))
	_, err := source.Exec(t.Context(), `INSERT INTO documents (id,space_id,content_sha256,filename,content_type,size_bytes,storage_key,source)
VALUES (gen_random_uuid(),'00000000-0000-0000-0000-000000000001',encode(sha256($1::bytea),'hex'),'receipt.pdf','application/pdf',$2,'legacy-space/receipt.pdf','upload')`, content, len(content))
	require.NoError(t, err)
	var output bytes.Buffer
	o.Output = &output
	_, err = Import(t.Context(), o)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(o.Data, "server.db"))
	aside, err := filepath.Glob(filepath.Join(o.Data, "attachments.legacy-*"))
	require.NoError(t, err)
	require.Len(t, aside, 1)
	preserved, err := os.ReadFile(filepath.Join(aside[0], "legacy-space", "receipt.pdf"))
	require.NoError(t, err)
	require.Equal(t, content, preserved)
	require.Contains(t, output.String(), aside[0])
}
func TestRefusals(t *testing.T) {
	for _, name := range []string{"fractional-yen", "old-migration", "existing-data", "fx-conflict", "security-price-conflict", "security-currency-conflict", "security-created-at-conflict"} {
		t.Run(name, func(t *testing.T) {
			db, o := fixture(t)
			expected := ""
			switch name {
			case "fractional-yen":
				var id string
				require.NoError(t, db.QueryRow(t.Context(), "UPDATE accounts SET opening_balance=1500.25 WHERE id=(SELECT id FROM accounts WHERE currency='JPY' LIMIT 1) RETURNING id::text").Scan(&id))
				expected = "accounts.opening_balance id " + id
			case "old-migration":
				_, e := db.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=10")
				require.NoError(t, e)
				expected = "source migration is 9"
			case "existing-data":
				require.NoError(t, os.Mkdir(filepath.Join(o.Data, "households"), 0700))
				expected = "non-empty data directory"
			case "fx-conflict":
				_, e := db.Exec(t.Context(), "UPDATE fx_rates SET rate=rate+1 WHERE id=(SELECT id FROM fx_rates WHERE (base_currency,quote_currency,date) IN (SELECT base_currency,quote_currency,date FROM fx_rates GROUP BY 1,2,3 HAVING count(*)>1) LIMIT 1)")
				require.NoError(t, e)
				expected = "fx_rates conflict"
			case "security-price-conflict", "security-currency-conflict", "security-created-at-conflict":
				var id, securityID string
				require.NoError(t, db.QueryRow(t.Context(), `SELECT p.id::text,p.security_id::text FROM security_prices p JOIN securities s ON s.id=p.security_id WHERE (s.symbol,p.on_date) IN (SELECT s.symbol,p.on_date FROM security_prices p JOIN securities s ON s.id=p.security_id GROUP BY 1,2 HAVING count(*)>1) LIMIT 1`).Scan(&id, &securityID))
				var e error
				switch name {
				case "security-price-conflict":
					_, e = db.Exec(t.Context(), "UPDATE security_prices SET close=close+1 WHERE id=$1", id)
				case "security-currency-conflict":
					_, e = db.Exec(t.Context(), "UPDATE securities SET currency='KWD' WHERE id=$1", securityID)
				case "security-created-at-conflict":
					_, e = db.Exec(t.Context(), "UPDATE security_prices SET created_at=created_at+interval '1 second' WHERE id=$1", id)
				}
				require.NoError(t, e)
				expected = "security_prices conflict"
			}
			_, e := Import(t.Context(), o)
			require.ErrorContains(t, e, expected)
			_, e = os.Stat(filepath.Join(o.Data, "server.db"))
			require.True(t, os.IsNotExist(e))
		})
	}
}
func TestDeletedSpace(t *testing.T) {
	db, o := fixture(t)
	_, e := db.Exec(t.Context(), "UPDATE spaces SET is_deleted=true WHERE id=(SELECT id FROM spaces LIMIT 1)")
	require.NoError(t, e)
	var memberships int
	require.NoError(t, db.QueryRow(t.Context(), "SELECT count(*) FROM memberships WHERE space_id IN (SELECT id FROM spaces WHERE is_deleted)").Scan(&memberships))
	report, e := Import(t.Context(), o)
	require.NoError(t, e)
	require.Equal(t, 1, report.DeletedSpaces)
	require.Equal(t, memberships, report.DeletedMemberships)
	require.Len(t, report.Households, 2)
}
func TestMinorUnits(t *testing.T) {
	for _, tt := range []struct {
		text, code string
		want       int64
	}{{"1500", "JPY", 1500}, {"1.234", "KWD", 1234}, {"12.34", "USD", 1234}} {
		scale, e := decimals(tt.code)
		require.NoError(t, e)
		got, e := minor(tt.text, scale)
		require.NoError(t, e)
		require.Equal(t, tt.want, got)
	}
	_, e := minor("1.01", 0)
	require.ErrorContains(t, e, "round")
	_, e = minor("9223372036854775808", 0)
	require.ErrorContains(t, e, "overflow")
}

func TestImportedNamesUseDomainFolding(t *testing.T) {
	s := &source{}
	for _, tt := range []struct{ name, want string }{
		{" Café Rouge ", " cafe rouge "},
		{"Straße", "straße"},
		{"Ｆｕｅｌ", "fuel"},
	} {
		got, err := s.convert(nil, "categories", column{name: "name_folded"}, row{"name": ptr(tt.name)})
		require.NoError(t, err)
		require.Equal(t, tt.want, got)
	}
}
func TestEveryMoneyColumnMapped(t *testing.T) {
	// Discover monetary types in the legacy fixture, then check both baselines
	// retain each as INTEGER. The percentage column deliberately is not money.
	sourceSQL, e := os.ReadFile("testdata/00001_initial.sql")
	require.NoError(t, e)
	sourceSQL = append(sourceSQL, []byte("\nCREATE TABLE merchant_orders (\n refund_total numeric(15,2)\n);\n")...)
	tableRE := regexp.MustCompile(`CREATE TABLE (\w+) \(([\s\S]*?)\n\);`)
	moneyRE := regexp.MustCompile(`(?m)^\s+(\w+) numeric\((?:15|14),2\)`)
	h, e := household.FS.ReadFile("00001_initial.sql")
	require.NoError(t, e)
	sv, e := server.FS.ReadFile("00001_initial.sql")
	require.NoError(t, e)
	baseline := string(h) + string(sv)
	seen := map[string]bool{}
	for _, table := range tableRE.FindAllStringSubmatch(string(sourceSQL), -1) {
		for _, col := range moneyRE.FindAllStringSubmatch(table[2], -1) {
			key := table[1] + "." + col[1]
			if key == "alert_rules.threshold_pct" {
				continue
			}
			_, ok := MoneyCurrencies[key]
			require.True(t, ok, "unmapped %s", key)
			seen[key] = true
			require.Contains(t, baseline, col[1]+" INTEGER")
		}
	}
	for key := range MoneyCurrencies {
		require.True(t, seen[key], "obsolete money mapping %s", key)
	}
	require.Equal(t, 72, len(seen))
	require.True(t, strings.Contains(baseline, "refund_checked_on"))
}
