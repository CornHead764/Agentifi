package store

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	servermigrations "github.com/CornHead764/agentifi/backend/migrations/server"
)

// AppliedMigration identifies a schema migration.
type AppliedMigration struct {
	Version int64
	Source  string
}

// Migrate is idempotent: opening the store already migrates server.db and
// household files migrate before their first use.
func (s *Store) Migrate(ctx context.Context) ([]AppliedMigration, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	_, err = db.MigrateServer(ctx, migrationSnapshot(s.sqlite.dataDir, "server"))
	return nil, err
}

// SchemaVersion reports the server schema version.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	db, err := s.server()
	if err != nil {
		return 0, err
	}
	var version int64
	err = db.Read().QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1").Scan(&version)
	return version, wrap("store: schema version", err)
}

// LatestMigrationVersion reports the highest embedded server migration.
func (s *Store) LatestMigrationVersion() (int64, error) {
	var latest int64
	err := fs.WalkDir(servermigrations.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		version, err := strconv.ParseInt(strings.SplitN(entry.Name(), "_", 2)[0], 10, 64)
		if err != nil {
			return fmt.Errorf("store: migration filename %q: %w", path, err)
		}
		if version > latest {
			latest = version
		}
		return nil
	})
	return latest, err
}
