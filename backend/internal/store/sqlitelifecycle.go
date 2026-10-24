package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

func (s *Store) householdIDs(ctx context.Context) ([]SpaceID, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list household IDs", func(row scanner) (SpaceID, error) {
		var id uuid.UUID
		err := row.Scan(&id)
		return SpaceID(id), err
	}, `SELECT id FROM households WHERE NOT is_deleted ORDER BY created_at, id`)
}

// retireHousehold closes the cached handle before moving its file. Failed
// provisioning removes the incomplete file; explicit deletion archives it.
func (s *Store) retireHousehold(id SpaceID, remove bool) error {
	if s.sqliteInTx {
		return errors.New("store: cannot retire a household in a transaction")
	}
	state := s.sqlite
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return errors.New("store: closed")
	}
	// Invalidate both cached scopes and future routing through retained clones.
	// Failed archival remains retired so a retry cannot recreate the active file.
	delete(state.scopes, id.UUID())
	if state.retired == nil {
		state.retired = make(map[uuid.UUID]bool)
	}
	state.retired[id.UUID()] = true
	if db := state.households[id.UUID()]; db != nil {
		if err := db.Close(); err != nil {
			return wrap("store: close household", err)
		}
		delete(state.households, id.UUID())
	}
	path := filepath.Join(state.dataDir, "households", id.UUID().String()+".db")
	if remove {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	dir := filepath.Join(state.dataDir, "households", "deleted")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	destination := filepath.Join(dir, fmt.Sprintf("%s.%d.db", id.UUID(), state.clock().UnixMilli()))
	return wrap("store: archive household", os.Rename(path, destination))
}
