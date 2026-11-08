package sqlitedb

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	householdmigrations "github.com/CornHead764/agentifi/backend/migrations/household"
	servermigrations "github.com/CornHead764/agentifi/backend/migrations/server"
	"github.com/pressly/goose/v3"
)

// MigrateServer applies the embedded server schema.
func (d *DB) MigrateServer(ctx context.Context, snapshotPath string) (string, error) {
	return d.Migrate(ctx, servermigrations.FS, snapshotPath)
}

// MigrateHousehold applies the embedded single-household schema.
func (d *DB) MigrateHousehold(ctx context.Context, snapshotPath string) (string, error) {
	return d.migrate(ctx, householdmigrations.FS, snapshotPath, householdGoMigrations())
}

// MigrateFreshHouseholdTo applies the household schema to a new file only as
// far as version, for an importer whose source has the shape of that version.
// MigrateHousehold takes it the rest of the way.
func (d *DB) MigrateFreshHouseholdTo(ctx context.Context, version int64) error {
	provider, err := goose.NewProvider(goose.DialectSQLite3, d.write, householdmigrations.FS, householdGoMigrations())
	if err != nil {
		return fmt.Errorf("sqlitedb: prepare migrations: %w", err)
	}
	if _, err := provider.UpTo(ctx, version); err != nil {
		return fmt.Errorf("sqlitedb: migrate fresh database to %d: %w", version, err)
	}
	return nil
}

func householdGoMigrations() goose.ProviderOption {
	return goose.WithGoMigrations(
		goose.NewGoMigration(2, &goose.GoFunc{RunTx: householdmigrations.MigrateChangeCapture}, nil),
		goose.NewGoMigration(3, &goose.GoFunc{RunTx: householdmigrations.MigrateWriteCounter},
			&goose.GoFunc{RunTx: householdmigrations.UnmigrateWriteCounter}),
		goose.NewGoMigration(4, &goose.GoFunc{RunTx: householdmigrations.MigrateIDLists},
			&goose.GoFunc{RunTx: householdmigrations.UnmigrateIDLists}))
}

// Only the schema is cached: no household identity, user data, or clock is
// retained in the template. Build it with the same migrations used on upgrades.
var householdTemplate struct {
	sync.Mutex
	data []byte
}

// OpenNewHousehold exclusively creates a household with the current schema.
// The caller must insert its household_metadata identity before publishing it.
// Existing files are never overwritten; upgrades still use MigrateHousehold.
func OpenNewHousehold(ctx context.Context, path string, clock func() time.Time) (_ *DB, err error) {
	data, err := householdSchema(ctx)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if removeErr := os.Remove(path + suffix); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					err = errors.Join(err, removeErr)
				}
			}
		}
	}()
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err = errors.Join(writeErr, file.Close()); err != nil {
		return nil, err
	}
	return Open(ctx, path, clock)
}

func householdSchema(ctx context.Context) ([]byte, error) {
	householdTemplate.Lock()
	defer householdTemplate.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if householdTemplate.data != nil {
		return householdTemplate.data, nil
	}
	dir, err := os.MkdirTemp("", "agentifi-household-schema-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	db, err := Open(ctx, filepath.Join(dir, "source.db"), time.Now)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.MigrateHousehold(ctx, ""); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "template.db")
	if err := db.Snapshot(ctx, path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	householdTemplate.data = data
	return data, nil
}

// Migrate saves an existing database before applying pending migrations.
// Call it before serving queries, while holding the instance lock.
// It returns the snapshot path, or an empty string if no snapshot was needed.
func (d *DB) Migrate(ctx context.Context, migrations fs.FS, snapshotPath string) (string, error) {
	return d.migrate(ctx, migrations, snapshotPath)
}

func (d *DB) migrate(ctx context.Context, migrations fs.FS, snapshotPath string, options ...goose.ProviderOption) (string, error) {
	if d.tx != nil {
		return "", errors.New("sqlitedb: migrations cannot run inside a transaction")
	}
	var tables int
	if err := d.write.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'`).Scan(&tables); err != nil {
		return "", fmt.Errorf("sqlitedb: inspect schema: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, d.write, migrations, options...)
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
		if err := d.Snapshot(ctx, snapshotPath); err != nil {
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

// Snapshot writes a consistent standalone database, including committed WAL
// contents. The destination must not already exist.
func (d *DB) Snapshot(ctx context.Context, path string) error {
	if d.tx != nil {
		return errors.New("sqlitedb: snapshots cannot run inside a transaction")
	}
	_, err := d.write.ExecContext(ctx, "VACUUM INTO ?1", path)
	return err
}
