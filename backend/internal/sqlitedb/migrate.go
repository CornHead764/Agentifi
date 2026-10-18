package sqlitedb

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	servermigrations "github.com/CornHead764/agentifi/backend/migrations/server"
	"github.com/pressly/goose/v3"
)

// MigrateServer applies the embedded server schema.
func (d *DB) MigrateServer(ctx context.Context, snapshotPath string) (string, error) {
	return d.Migrate(ctx, servermigrations.FS, snapshotPath)
}

// Migrate saves an existing database before applying pending migrations.
// Call it before serving queries, while holding the instance lock.
// It returns the snapshot path, or an empty string if no snapshot was needed.
func (d *DB) Migrate(ctx context.Context, migrations fs.FS, snapshotPath string) (string, error) {
	if d.tx != nil {
		return "", errors.New("sqlitedb: migrations cannot run inside a transaction")
	}
	var tables int
	if err := d.write.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'`).Scan(&tables); err != nil {
		return "", fmt.Errorf("sqlitedb: inspect schema: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, d.write, migrations)
	if err != nil {
		return "", fmt.Errorf("sqlitedb: prepare migrations: %w", err)
	}
	pending, err := provider.HasPending(ctx)
	if err != nil {
		return "", fmt.Errorf("sqlitedb: check migrations: %w", err)
	}
	if !pending {
		return "", nil
	}
	snapshot := ""
	if tables > 0 {
		if snapshotPath == "" {
			return "", errors.New("sqlitedb: snapshot path is required for an existing database")
		}
		if _, err := d.write.ExecContext(ctx, "VACUUM INTO ?1", snapshotPath); err != nil {
			return "", fmt.Errorf("sqlitedb: snapshot before migration: %w", err)
		}
		snapshot = snapshotPath
	}
	if _, err := provider.Up(ctx); err != nil {
		if snapshot != "" {
			return snapshot, fmt.Errorf("sqlitedb: migration failed; restore snapshot %q before retrying with the previous binary: %w", snapshot, err)
		}
		return "", fmt.Errorf("sqlitedb: migrate fresh database: %w", err)
	}
	return snapshot, nil
}
