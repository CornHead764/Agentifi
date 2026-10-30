package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrateRefusesMissingSQLiteAfterCompletedUpgrade(t *testing.T) {
	source, dsn := legacyFixture(t, true)
	cfg := upgradeConfig(t, dsn)
	require.NoError(t, migrate(t.Context(), cfg, nil))
	var completed bool
	require.NoError(t, source.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM agentifi_sqlite_migrated)").Scan(&completed))
	require.True(t, completed)
	require.NoError(t, os.RemoveAll(cfg.DataDir))
	for _, dir := range []string{cfg.DataDir, filepath.Join(t.TempDir(), "repointed")} {
		cfg.DataDir = dir
		err := migrate(t.Context(), cfg, nil)
		require.ErrorContains(t, err, "already migrated")
		require.ErrorContains(t, err, "restore")
		require.NoFileExists(t, filepath.Join(dir, "server.db"))
	}
	require.ErrorContains(t, serve(t.Context(), cfg), "restore")
}

func TestMigrateRefusesLegacyWriter(t *testing.T) {
	source, dsn := legacyFixture(t, true)
	tx, err := source.Begin(t.Context())
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(t.Context(), "UPDATE users SET theme='dark'")
	require.NoError(t, err)
	cfg := upgradeConfig(t, dsn)
	require.ErrorContains(t, migrate(t.Context(), cfg, nil), "stop the old agentifi")
	require.NoFileExists(t, filepath.Join(cfg.DataDir, "server.db"))
	require.NoError(t, tx.Rollback(t.Context()))
	require.NoError(t, migrate(t.Context(), cfg, nil))
}
