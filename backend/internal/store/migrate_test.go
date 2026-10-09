package store

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/migrations"
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
	entries, err := migrations.FS.ReadDir(".")
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
	err := db(t).db.QueryRow(t.Context(),
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_type = 'BASE TABLE'`,
		testSchema).Scan(&count)
	require.NoError(t, err)
	// Every table the migrations leave, plus goose's version table. Update
	// when a migration adds or drops a table.
	require.Equal(t, 68, count)
}

// A login set to a text before migration 00004 is set to "" after it, and the
// constraint then refuses the word. Run inside a transaction that is rolled
// back, from the wider constraint 00001 left, using the file's own statements.
func TestMigrationFourMovesTextLoginsToTypeItYourself(t *testing.T) {
	ctx := t.Context()
	file, err := migrations.FS.ReadFile("00004_second_factor_no_sms.sql")
	require.NoError(t, err)
	up, down, ok := strings.Cut(string(file), "-- +goose Down")
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

	tx, err := db(t).pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, down)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE bill_connections SET second_factor = 'sms' WHERE id = $1`, connection.ID)
	require.NoError(t, err, "the wider constraint takes a text")
	_, err = tx.Exec(ctx, `UPDATE bill_connections SET second_factor = 'email' WHERE id = $1`, emailed.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE merchant_accounts SET second_factor = 'sms' WHERE id = $1`, account.ID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, up)
	require.NoError(t, err)

	var billFactor, emailFactor, merchantFactor string
	require.NoError(t, tx.QueryRow(ctx, `SELECT second_factor FROM bill_connections WHERE id = $1`,
		connection.ID).Scan(&billFactor))
	require.NoError(t, tx.QueryRow(ctx, `SELECT second_factor FROM bill_connections WHERE id = $1`,
		emailed.ID).Scan(&emailFactor))
	require.NoError(t, tx.QueryRow(ctx, `SELECT second_factor FROM merchant_accounts WHERE id = $1`,
		account.ID).Scan(&merchantFactor))
	require.Equal(t, "", billFactor)
	require.Equal(t, "email", emailFactor, "a login set to anything else is left alone")
	require.Equal(t, "", merchantFactor)

	for _, table := range []string{"bill_connections", "merchant_accounts"} {
		_, err = tx.Exec(ctx, `SAVEPOINT refused`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `UPDATE `+table+` SET second_factor = 'sms'`)
		require.Error(t, err, table)
		_, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT refused`)
		require.NoError(t, err)
	}
}
