package pgimport

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/seed"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/CornHead764/agentifi/backend/internal/testdb"
	"github.com/CornHead764/agentifi/backend/migrations"
	household "github.com/CornHead764/agentifi/backend/migrations/household"
	server "github.com/CornHead764/agentifi/backend/migrations/server"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { storetest.Main(m, "pgimport") }
func fixture(t *testing.T) (*store.Store, Options) {
	t.Helper()
	db := storetest.Empty(t)
	cipher, e := store.NewCipher("pgimport-test-only")
	require.NoError(t, e)
	db = db.WithCipher(cipher)
	o := seed.DefaultOptions()
	o.Transactions = 48
	storage := t.TempDir()
	o.Storage = &provider.LocalStorage{BasePath: storage}
	require.NoError(t, seed.Run(t.Context(), db, o))
	var schema string
	require.NoError(t, db.Conn().QueryRow(t.Context(), "SELECT current_schema()").Scan(&schema))
	u, e := url.Parse(testdb.URL())
	require.NoError(t, e)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return db, Options{From: u.String(), Data: t.TempDir(), StoragePath: storage}
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
	require.NoError(t, db.Conn().QueryRow(t.Context(), "SELECT id::text,space_id::text FROM accounts WHERE currency='JPY' LIMIT 1").Scan(&id, &house))
	d, e := sqlitedb.Open(t.Context(), filepath.Join(o.Data, "households", house+".db"), time.Now)
	require.NoError(t, e)
	defer d.Close()
	var got int64
	require.NoError(t, d.Read().QueryRowContext(t.Context(), "SELECT opening_balance FROM accounts WHERE id=?", id).Scan(&got))
	var want int64
	require.NoError(t, db.Conn().QueryRow(t.Context(), "SELECT opening_balance::bigint FROM accounts WHERE id=$1", id).Scan(&want))
	require.Equal(t, want, got)
	var kwdid, kwdhouse string
	require.NoError(t, db.Conn().QueryRow(t.Context(), "SELECT id::text,space_id::text FROM accounts WHERE currency='KWD' LIMIT 1").Scan(&kwdid, &kwdhouse))
	kd, e := sqlitedb.Open(t.Context(), filepath.Join(o.Data, "households", kwdhouse+".db"), time.Now)
	require.NoError(t, e)
	defer kd.Close()
	require.NoError(t, kd.Read().QueryRowContext(t.Context(), "SELECT opening_balance FROM accounts WHERE id=?", kwdid).Scan(&got))
	require.NoError(t, db.Conn().QueryRow(t.Context(), "SELECT (opening_balance*1000)::bigint FROM accounts WHERE id=$1", kwdid).Scan(&want))
	require.Equal(t, want, got)
}
func TestRefusals(t *testing.T) {
	for _, name := range []string{"fractional-yen", "old-migration", "existing-data", "fx-conflict", "security-price-conflict", "security-currency-conflict", "security-created-at-conflict"} {
		t.Run(name, func(t *testing.T) {
			db, o := fixture(t)
			expected := ""
			switch name {
			case "fractional-yen":
				var id string
				require.NoError(t, db.Conn().QueryRow(t.Context(), "UPDATE accounts SET opening_balance=1500.25 WHERE currency='JPY' RETURNING id::text").Scan(&id))
				expected = "accounts.opening_balance id " + id
			case "old-migration":
				_, e := db.Conn().Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=10")
				require.NoError(t, e)
				expected = "source migration is 9"
			case "existing-data":
				require.NoError(t, os.Mkdir(filepath.Join(o.Data, "households"), 0700))
				expected = "non-empty --data"
			case "fx-conflict":
				_, e := db.Conn().Exec(t.Context(), "UPDATE fx_rates SET rate=rate+1 WHERE id=(SELECT id FROM fx_rates WHERE (base_currency,quote_currency,date) IN (SELECT base_currency,quote_currency,date FROM fx_rates GROUP BY 1,2,3 HAVING count(*)>1) LIMIT 1)")
				require.NoError(t, e)
				expected = "fx_rates conflict"
			case "security-price-conflict", "security-currency-conflict", "security-created-at-conflict":
				var id, securityID string
				require.NoError(t, db.Conn().QueryRow(t.Context(), `SELECT p.id::text,p.security_id::text FROM security_prices p JOIN securities s ON s.id=p.security_id WHERE (s.symbol,p.on_date) IN (SELECT s.symbol,p.on_date FROM security_prices p JOIN securities s ON s.id=p.security_id GROUP BY 1,2 HAVING count(*)>1) LIMIT 1`).Scan(&id, &securityID))
				var e error
				switch name {
				case "security-price-conflict":
					_, e = db.Conn().Exec(t.Context(), "UPDATE security_prices SET close=close+1 WHERE id=$1", id)
				case "security-currency-conflict":
					_, e = db.Conn().Exec(t.Context(), "UPDATE securities SET currency='KWD' WHERE id=$1", securityID)
				case "security-created-at-conflict":
					_, e = db.Conn().Exec(t.Context(), "UPDATE security_prices SET created_at=created_at+interval '1 second' WHERE id=$1", id)
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
	_, e := db.Conn().Exec(t.Context(), "UPDATE spaces SET is_deleted=true WHERE id=(SELECT id FROM spaces LIMIT 1)")
	require.NoError(t, e)
	var memberships int
	require.NoError(t, db.Conn().QueryRow(t.Context(), "SELECT count(*) FROM memberships WHERE space_id IN (SELECT id FROM spaces WHERE is_deleted)").Scan(&memberships))
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
	require.Equal(t, "cafe rouge", folded(" Café Rouge "))
}
func TestEveryMoneyColumnMapped(t *testing.T) {
	// Discover monetary types in the embedded source, then check both baselines
	// retain each as INTEGER. The percentage column deliberately is not money.
	sourceSQL, e := migrations.FS.ReadFile("00001_initial.sql")
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
