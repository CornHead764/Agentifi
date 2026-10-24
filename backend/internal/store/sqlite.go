package store

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/google/uuid"
)

// sqliteStore owns handles shared by WithCipher and transaction-scoped clones.
// Hold mu through opening/migrating so no caller observes a partial schema.
type sqliteStore struct {
	mu         sync.Mutex
	dataDir    string
	clock      func() time.Time
	server     *sqlitedb.DB
	households map[uuid.UUID]*sqlitedb.DB
	closed     bool
	namedLocks map[string]chan struct{}
	scopes     map[uuid.UUID]*Store
	retired    map[uuid.UUID]bool
}

// OpenSQLite opens and migrates server.db. Household files are opened and
// migrated lazily, once per ID. The caller must hold the data directory's
// sqlitedb instance lock for the lifetime of the Store.
func OpenSQLite(ctx context.Context, dataDir string, clock func() time.Time) (*Store, error) {
	if clock == nil {
		return nil, errors.New("store: clock is required")
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "households"), 0700); err != nil {
		return nil, err
	}
	db, err := sqlitedb.Open(ctx, filepath.Join(dataDir, "server.db"), clock)
	if err != nil {
		return nil, err
	}
	if _, err := db.MigrateServer(ctx, migrationSnapshot(dataDir, "server")); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{sqlite: &sqliteStore{dataDir: dataDir, clock: clock, server: db, households: make(map[uuid.UUID]*sqlitedb.DB)}}, nil
}

func migrationSnapshot(dir, name string) string {
	return filepath.Join(dir, name+"-before-migration-"+uuid.NewString()+".db")
}

// server rejects access from a household transaction: no transaction spans files.
func (s *Store) server() (*sqlitedb.DB, error) {
	if s.sqlite == nil {
		return nil, errors.New("store: not a SQLite store")
	}
	if s.sqliteInTx {
		if s.sqliteScope != uuid.Nil {
			return nil, errors.New("store: server access in household transaction")
		}
		return s.sqliteTx, nil
	}
	s.sqlite.mu.Lock()
	defer s.sqlite.mu.Unlock()
	if s.sqlite.closed {
		return nil, errors.New("store: closed")
	}
	return s.sqlite.server, nil
}

func (s *Store) household(ctx context.Context, id uuid.UUID) (*sqlitedb.DB, error) {
	if s.sqlite == nil {
		return nil, errors.New("store: not a SQLite store")
	}
	if id == uuid.Nil {
		return nil, errors.New("store: household ID is required")
	}
	if s.sqliteInTx {
		if s.sqliteScope != id {
			return nil, errors.New("store: cross-file access in transaction")
		}
		return s.sqliteTx, nil
	}
	if s.sqliteScope != uuid.Nil && s.sqliteScope != id {
		return nil, errors.New("store: household scope mismatch")
	}
	state := s.sqlite
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return nil, errors.New("store: closed")
	}
	if state.retired[id] {
		return nil, errors.New("store: household retired")
	}
	if db := state.households[id]; db != nil {
		return db, nil
	}
	db, err := sqlitedb.Open(ctx, filepath.Join(state.dataDir, "households", id.String()+".db"), state.clock)
	if err != nil {
		return nil, err
	}
	if _, err := db.MigrateHousehold(ctx, migrationSnapshot(filepath.Join(state.dataDir, "households"), id.String())); err != nil {
		_ = db.Close()
		return nil, err
	}
	state.households[id] = db
	return db, nil
}

// ServerDB and HouseholdDB are the only SQLite escape hatches for direct SQL
// in service/seed code. They preserve transaction routing; use Read for reads
// and Write for writes. Do not retain their handles beyond the Store's lifetime.
func (s *Store) ServerDB() (*sqlitedb.DB, error) { return s.server() }
func (s *Store) HouseholdDB(ctx context.Context, id SpaceID) (*sqlitedb.DB, error) {
	return s.household(ctx, id.UUID())
}

// ForHousehold selects the file for the existing InTx callback API. Root stores
// use server.db in InTx; tenant callers must scope before beginning. Nested InTx
// uses savepoints on the selected file. Callback stores are not goroutine-safe.
// On the transitional Postgres backend this is a no-op.
// Metadata is read before the transaction; currency changes require a new scope.
func (s *Store) ForHousehold(ctx context.Context, id SpaceID) (*Store, error) {
	if s.sqlite == nil {
		return s, nil
	}
	if s.sqliteInTx {
		return nil, errors.New("store: cannot scope inside a transaction")
	}
	space, err := s.GetSpace(ctx, id)
	if err != nil {
		return nil, err
	}
	db, err := s.household(ctx, id.UUID())
	if err != nil {
		return nil, err
	}
	var primary string
	if err := db.Read().QueryRowContext(ctx, `SELECT primary_currency FROM household_metadata WHERE id = 1`).Scan(&primary); err != nil {
		return nil, wrap("store: household currency", err)
	}
	if primary != space.PrimaryCurrency {
		server, err := s.server()
		if err != nil {
			return nil, err
		}
		if _, err := server.Write().ExecContext(ctx, `UPDATE households SET primary_currency = ?, updated_at = ? WHERE id = ?`, primary, server.Now(), id.UUID()); err != nil {
			return nil, wrap("store: repair currency mirror", err)
		}
		slog.WarnContext(ctx, "repaired household primary currency mirror", "household", id.UUID(), "old_currency", space.PrimaryCurrency, "primary_currency", primary)
	}
	child := *s
	child.sqliteScope = id.UUID()
	child.sqlitePrimaryCurrency = primary
	s.sqlite.mu.Lock()
	if s.sqlite.scopes == nil {
		s.sqlite.scopes = make(map[uuid.UUID]*Store)
	}
	s.sqlite.scopes[id.UUID()] = &child
	s.sqlite.mu.Unlock()
	return &child, nil
}

// InHouseholdTx selects a household and preserves same-file nested savepoints.
func (s *Store) InHouseholdTx(ctx context.Context, id SpaceID, fn func(*Store) error) error {
	scoped, err := s.scoped(ctx, id)
	if err != nil {
		return err
	}
	return scoped.InTx(ctx, fn)
}

func (s *Store) inSQLiteTx(ctx context.Context, fn func(*Store) error) error {
	var db *sqlitedb.DB
	var err error
	if s.sqliteScope == uuid.Nil {
		db, err = s.server()
	} else {
		db, err = s.household(ctx, s.sqliteScope)
	}
	if err != nil {
		return err
	}
	return db.InTx(ctx, func(tx *sqlitedb.DB) error {
		child := *s
		child.sqliteTx, child.sqliteInTx = tx, true
		return fn(&child)
	})
}

func (s *Store) closeSQLite() {
	// A callback does not own the shared pools; closing it must not close its parent.
	if s.sqliteInTx {
		return
	}
	state := s.sqlite
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	state.closed = true
	_ = state.server.Close()
	for _, db := range state.households {
		_ = db.Close()
	}
}
