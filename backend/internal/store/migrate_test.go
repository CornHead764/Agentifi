package store

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/migrations"
	householdmigrations "github.com/CornHead764/agentifi/backend/migrations/household"
	servermigrations "github.com/CornHead764/agentifi/backend/migrations/server"
)

// TestMigrateIsIdempotent: TestMain already migrated this schema, so a second
// run must report nothing rather than fail.
func TestMigrateIsIdempotent(t *testing.T) {
	ctx := t.Context()

	applied, err := db(t).Migrate(ctx)
	require.NoError(t, err)
	require.Empty(t, applied)

	// The newest embedded migration, not a literal, so new migrations do not
	// break this test.
	version, err := db(t).SchemaVersion(ctx)
	require.NoError(t, err)
	require.EqualValues(t, newestMigrationVersion(t), version)

	again, err := db(t).SchemaVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, version, again)
}

// TestLatestMigrationVersionTracksTheEmbeddedFiles: `serve` refuses to start
// against a database behind this number, so it must reflect the migrations the
// binary carries.
func TestLatestMigrationVersionTracksTheEmbeddedFiles(t *testing.T) {
	latest, err := db(t).LatestMigrationVersion()
	require.NoError(t, err)
	require.EqualValues(t, newestMigrationVersion(t), latest)

	version, err := db(t).SchemaVersion(t.Context())
	require.NoError(t, err)
	require.Equal(t, latest, version)
}

// newestMigrationVersion reads the highest numeric prefix in the embedded
// migrations.
func newestMigrationVersion(t *testing.T) int64 {
	t.Helper()
	entries, err := servermigrations.FS.ReadDir(".")
	require.NoError(t, err)

	var newest int64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		digits, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		value, err := strconv.ParseInt(digits, 10, 64)
		require.NoError(t, err, "migration %s has no numeric prefix", name)
		if value > newest {
			newest = value
		}
	}
	require.NotZero(t, newest, "no migrations found")
	return newest
}

// TestSchemaCarriesEveryTable checks the embedded migrations are the whole
// schema, not a truncated copy that applies cleanly.
func TestSchemaCarriesEveryTable(t *testing.T) {
	var count int
	err := serverDB(t).Read().QueryRowContext(t.Context(),
		`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&count)
	require.NoError(t, err)
	serverSchema, err := servermigrations.FS.ReadFile("00001_initial.sql")
	require.NoError(t, err)
	require.Equal(t, strings.Count(string(serverSchema), "CREATE TABLE ")+1, count,
		"all server tables plus the migration version table")
	household := householdDB(t, newSpace(t))
	require.NoError(t, household.Read().QueryRowContext(t.Context(),
		`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&count))
	householdSchema, err := householdmigrations.FS.ReadFile("00001_initial.sql")
	require.NoError(t, err)
	require.Equal(t, strings.Count(string(householdSchema), "CREATE TABLE ")+1, count,
		"all household tables plus the migration version table")
}

// The historical SMS backfill still normalizes legacy values before the current
// SQLite constraints take effect. Temporarily suspend checks to seed old rows.
func TestMigrationFourMovesTextLoginsToTypeItYourself(t *testing.T) {
	ctx := t.Context()
	file, err := migrations.FS.ReadFile("00004_second_factor_no_sms.sql")
	require.NoError(t, err)
	up, _, ok := strings.Cut(string(file), "-- +goose Down")
	require.True(t, ok, "the migration has a Down")
	_, up, ok = strings.Cut(up, "-- +goose Up")
	require.True(t, ok, "the migration has an Up")

	space := newSpace(t)
	connection := newBillConnection(t, space)
	emailed := &BillConnection{
		Biller: domain.BillerSpectrum, Label: "Second account",
		CredentialSource: BillCredentialSession, AutopayRule: domain.AutopayNone,
	}
	require.NoError(t, sealedDB(t).CreateBillConnection(ctx, space, emailed))
	account := newSignedInMerchantAccount(t, space, "Alex")

	handle := householdDB(t, space)
	conn, err := handle.Write().(*sql.DB).Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `PRAGMA ignore_check_constraints = ON`)
	require.NoError(t, err)
	defer conn.ExecContext(ctx, `PRAGMA ignore_check_constraints = OFF`) //nolint:errcheck
	_, err = conn.ExecContext(ctx, `UPDATE bill_connections SET second_factor = 'sms' WHERE id = ?`, connection.ID)
	require.NoError(t, err, "legacy data can carry SMS")
	_, err = conn.ExecContext(ctx, `UPDATE bill_connections SET second_factor = 'email' WHERE id = ?`, emailed.ID)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `UPDATE merchant_accounts SET second_factor = 'sms' WHERE id = ?`, account.ID)
	require.NoError(t, err)

	// Only the data backfill is shared; SQLite's initial schema supplies checks.
	backfill, _, ok := strings.Cut(up, "ALTER TABLE")
	require.True(t, ok)
	_, err = conn.ExecContext(ctx, backfill)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `PRAGMA ignore_check_constraints = OFF`)
	require.NoError(t, err)

	var billFactor, emailFactor, merchantFactor string
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT second_factor FROM bill_connections WHERE id = ?`,
		connection.ID).Scan(&billFactor))
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT second_factor FROM bill_connections WHERE id = ?`,
		emailed.ID).Scan(&emailFactor))
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT second_factor FROM merchant_accounts WHERE id = ?`,
		account.ID).Scan(&merchantFactor))
	require.Equal(t, "", billFactor)
	require.Equal(t, "email", emailFactor, "a login set to anything else is left alone")
	require.Equal(t, "", merchantFactor)

	for _, table := range []string{"bill_connections", "merchant_accounts"} {
		_, err = conn.ExecContext(ctx, `UPDATE `+table+` SET second_factor = 'sms'`)
		require.Error(t, err, table)
	}
}
