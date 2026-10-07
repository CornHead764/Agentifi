package store

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
	require.Equal(t, 67, count)
}
