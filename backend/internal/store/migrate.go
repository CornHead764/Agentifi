package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/CornHead764/agentifi/backend/migrations"
)

// AppliedMigration is one migration goose ran, reported so a caller can print
// what happened without importing goose.
type AppliedMigration struct {
	Version int64
	Source  string
}

// Migrate applies every pending migration from the embedded schema. Closing
// the stdlib.OpenDBFromPool adapter goose needs does not close the pool.
func (s *Store) Migrate(ctx context.Context) ([]AppliedMigration, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("store: migrate needs a pool, not a transaction")
	}
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return nil, fmt.Errorf("store: preparing migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: applying migrations: %w", err)
	}

	applied := make([]AppliedMigration, 0, len(results))
	for _, result := range results {
		applied = append(applied, AppliedMigration{Version: result.Source.Version, Source: result.Source.Path})
	}
	return applied, nil
}

// SchemaVersion is the highest migration version recorded in the database, and
// zero on an empty one.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("store: schema version needs a pool, not a transaction")
	}
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return 0, fmt.Errorf("store: preparing migrations: %w", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: reading schema version: %w", err)
	}
	return version, nil
}

// LatestMigrationVersion is the highest migration version embedded in this
// binary — the version a database must be at for this build's queries. Read
// through goose so it counts what goose would apply.
func (s *Store) LatestMigrationVersion() (int64, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("store: migration version needs a pool, not a transaction")
	}
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return 0, fmt.Errorf("store: preparing migrations: %w", err)
	}
	var latest int64
	for _, source := range provider.ListSources() {
		if source.Version > latest {
			latest = source.Version
		}
	}
	return latest, nil
}
