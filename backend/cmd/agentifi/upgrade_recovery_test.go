package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/pgimport"
	"github.com/stretchr/testify/require"
)

func TestMigrateRecoversInterruptedPublication(t *testing.T) {
	source, dsn := legacyFixture(t, true)
	cfg := upgradeConfig(t, dsn)
	require.NoError(t, os.MkdirAll(cfg.DataDir, 0700))
	cfg.StoragePath = filepath.Join(cfg.DataDir, "attachments")
	require.NoError(t, os.MkdirAll(cfg.StoragePath, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(cfg.StoragePath, "receipt.pdf"), []byte("receipt"), 0600))
	_, err := source.Exec(t.Context(), `INSERT INTO documents (id,space_id,content_sha256,filename,content_type,size_bytes,storage_key,source)
VALUES (gen_random_uuid(),'00000000-0000-0000-0000-000000000001',encode(sha256('receipt'::bytea),'hex'),'receipt.pdf','application/pdf',7,'receipt.pdf','upload')`)
	require.NoError(t, err)
	stage, err := os.MkdirTemp(cfg.DataDir, ".import-postgres-")
	require.NoError(t, err)
	_, err = pgimport.Import(t.Context(), pgimport.Options{From: dsn, Data: stage, StoragePath: cfg.StoragePath})
	require.NoError(t, err)
	// Simulate dying after retaining legacy uploads and publishing both directories.
	require.NoError(t, os.Rename(filepath.Join(stage, "households"), filepath.Join(cfg.DataDir, "households")))
	retained := filepath.Join(cfg.DataDir, "attachments.legacy-interrupted")
	require.NoError(t, os.Rename(cfg.StoragePath, retained))
	require.NoError(t, os.Rename(filepath.Join(stage, "attachments"), cfg.StoragePath))
	require.NoError(t, migrate(t.Context(), cfg, nil))
	require.FileExists(t, filepath.Join(cfg.DataDir, "server.db"))
	require.FileExists(t, filepath.Join(cfg.DataDir, "households", "00000000-0000-0000-0000-000000000001.db"))
	require.FileExists(t, filepath.Join(retained, "receipt.pdf"))
	stages, err := filepath.Glob(filepath.Join(cfg.DataDir, ".import-postgres-*"))
	require.NoError(t, err)
	require.Empty(t, stages)
}
