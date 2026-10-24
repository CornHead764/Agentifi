package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/pgimport"
	"github.com/stretchr/testify/require"
)

func TestMigrateRecoversInterruptedPublication(t *testing.T) {
	_, dsn := legacyFixture(t, true)
	cfg := upgradeConfig(t, dsn)
	require.NoError(t, os.MkdirAll(cfg.DataDir, 0700))
	stage, err := os.MkdirTemp(cfg.DataDir, ".import-postgres-")
	require.NoError(t, err)
	_, err = pgimport.Import(t.Context(), pgimport.Options{From: dsn, Data: stage, StoragePath: cfg.StoragePath})
	require.NoError(t, err)
	// Simulate a process dying after the first rename but before server.db.
	require.NoError(t, os.Rename(filepath.Join(stage, "households"), filepath.Join(cfg.DataDir, "households")))
	require.NoError(t, migrate(t.Context(), cfg, nil))
	require.FileExists(t, filepath.Join(cfg.DataDir, "server.db"))
	require.FileExists(t, filepath.Join(cfg.DataDir, "households", "00000000-0000-0000-0000-000000000001.db"))
	stages, err := filepath.Glob(filepath.Join(cfg.DataDir, ".import-postgres-*"))
	require.NoError(t, err)
	require.Empty(t, stages)
}
