package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

// Match pgimport's source fixture: apply the frozen legacy migrations in an
// isolated schema, never modify the database selected by TEST_DATABASE_URL.
func legacyFixture(t *testing.T, seeded bool) (*pgx.Conn, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is unset; legacy PostgreSQL source unavailable")
	}
	admin, err := pgx.Connect(t.Context(), dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("agentifi_upgrade_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = admin.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = admin.Close(context.Background())
	})
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	dsn = u.String()
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../internal/pgimport/testdata"))
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	conn, err := pgx.Connect(t.Context(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	if seeded {
		_, err = conn.Exec(t.Context(), `INSERT INTO users (id,email,locale,theme) VALUES ('10000000-0000-0000-0000-000000000001','upgrade@example.test','en','system');
INSERT INTO spaces (id,name,primary_currency,timezone) VALUES ('00000000-0000-0000-0000-000000000001','Upgrade household','USD','UTC');
INSERT INTO memberships (id,user_id,role,space_id) VALUES (gen_random_uuid(),'10000000-0000-0000-0000-000000000001','owner','00000000-0000-0000-0000-000000000001');`)
		require.NoError(t, err)
	}
	return conn, dsn
}

func upgradeConfig(t *testing.T, dsn string) *config.Config {
	t.Helper()
	t.Setenv("DEBUG", "true")
	t.Setenv("DATA_DIR", filepath.Join(t.TempDir(), "db"))
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("BACKUP_DIR", "")
	t.Setenv("SYNC_ENABLED", "false")
	t.Setenv("STORAGE_PATH", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Setenv("HTTP_ADDR", listener.Addr().String())
	require.NoError(t, listener.Close())
	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.DatabaseURL = dsn // Deliberately empty means no legacy source, not config's default DSN.
	return cfg
}

func TestMigrateImportsLegacyPostgresOnce(t *testing.T) {
	source, dsn := legacyFixture(t, true)
	cfg := upgradeConfig(t, dsn)
	require.NoError(t, migrate(t.Context(), cfg, nil))
	db, err := sqlitedb.Open(t.Context(), filepath.Join(cfg.DataDir, "server.db"), time.Now)
	require.NoError(t, err)
	var email string
	require.NoError(t, db.Read().QueryRowContext(t.Context(), "SELECT email FROM users").Scan(&email))
	require.Equal(t, "upgrade@example.test", email)
	require.NoError(t, db.Close())
	require.FileExists(t, filepath.Join(cfg.DataDir, "households", "00000000-0000-0000-0000-000000000001.db"))
	var count int
	require.NoError(t, source.QueryRow(t.Context(), "SELECT count(*) FROM users").Scan(&count))
	require.Equal(t, 1, count, "source remains intact")
	_, err = source.Exec(t.Context(), "INSERT INTO users (id,email,locale,theme) VALUES (gen_random_uuid(),'later@example.test','en','system')")
	require.NoError(t, err)
	require.NoError(t, migrate(t.Context(), cfg, nil))
	db, err = sqlitedb.Open(t.Context(), filepath.Join(cfg.DataDir, "server.db"), time.Now)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM users").Scan(&count))
	require.Equal(t, 1, count, "existing SQLite is never re-imported")
}

func TestMigratePreservesDefaultLayoutLegacyAttachments(t *testing.T) {
	_, dsn := legacyFixture(t, true)
	cfg := upgradeConfig(t, dsn)
	cfg.StoragePath = filepath.Join(cfg.DataDir, "attachments")
	require.NoError(t, os.MkdirAll(cfg.StoragePath, 0700))
	legacyFile := filepath.Join(cfg.StoragePath, "legacy-upload")
	require.NoError(t, os.WriteFile(legacyFile, []byte("legacy bytes"), 0600))
	require.NoError(t, migrate(t.Context(), cfg, nil))
	require.FileExists(t, filepath.Join(cfg.DataDir, "server.db"))
	aside, err := filepath.Glob(filepath.Join(cfg.DataDir, "attachments.legacy-*"))
	require.NoError(t, err)
	require.Len(t, aside, 1)
	content, err := os.ReadFile(filepath.Join(aside[0], "legacy-upload"))
	require.NoError(t, err)
	require.Equal(t, "legacy bytes", string(content))
}

func TestServeRefusesUnimportedPostgres(t *testing.T) {
	_, dsn := legacyFixture(t, true)
	cfg := upgradeConfig(t, dsn)
	require.ErrorContains(t, serve(t.Context(), cfg), "agentifi migrate")
	_, err := os.Stat(filepath.Join(cfg.DataDir, "server.db"))
	require.True(t, os.IsNotExist(err), "refusal must not create SQLite")
}

func assertUpgradeServeStarts(t *testing.T, cfg *config.Config) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(20 * time.Second):
			t.Error("serve did not shut down")
		}
	})
	client := &http.Client{Timeout: 100 * time.Millisecond}
	require.Eventually(t, func() bool {
		response, err := client.Get("http://" + cfg.HTTPAddr + "/health")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, 10*time.Second, 25*time.Millisecond)
}

func TestServeStartsWithoutLegacyData(t *testing.T) {
	t.Run("empty Postgres", func(t *testing.T) {
		_, dsn := legacyFixture(t, false)
		assertUpgradeServeStarts(t, upgradeConfig(t, dsn))
	})
	t.Run("no DATABASE_URL", func(t *testing.T) { assertUpgradeServeStarts(t, upgradeConfig(t, "")) })
}

func TestConfiguredUnreachablePostgresRefusesFreshInstallation(t *testing.T) {
	for _, command := range []string{"serve", "migrate"} {
		t.Run(command, func(t *testing.T) {
			cfg := upgradeConfig(t, "postgres://127.0.0.1:1/unreachable?connect_timeout=1")
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			var err error
			if command == "serve" {
				err = serve(ctx, cfg)
			} else {
				err = migrate(ctx, cfg, nil)
			}
			require.ErrorContains(t, err, "127.0.0.1")
			require.ErrorContains(t, err, "start Postgres or unset DATABASE_URL")
			_, err = os.Stat(filepath.Join(cfg.DataDir, "server.db"))
			require.True(t, os.IsNotExist(err), "unreachable configured Postgres must never create server.db")
		})
	}
}

func TestServeRefusesHeldDataDirectoryLock(t *testing.T) {
	cfg := upgradeConfig(t, "")
	lock, err := lockDataDir(cfg)
	require.NoError(t, err)
	defer lock.Close()
	require.ErrorContains(t, serve(t.Context(), cfg), "lock")
}

func TestFailedLegacyImportLeavesNoSQLite(t *testing.T) {
	source, dsn := legacyFixture(t, true)
	_, err := source.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=(SELECT max(version_id) FROM goose_db_version)")
	require.NoError(t, err)
	cfg := upgradeConfig(t, dsn)
	require.ErrorContains(t, migrate(t.Context(), cfg, nil), "source migration")
	_, err = os.Stat(filepath.Join(cfg.DataDir, "server.db"))
	require.True(t, os.IsNotExist(err))
	stages, err := filepath.Glob(filepath.Join(cfg.DataDir, ".import-postgres-*"))
	require.NoError(t, err)
	require.Empty(t, stages)
}
