package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"

	"github.com/stretchr/testify/require"
)

func TestCreateSeededSpaceProvisionsSeparateFiles(t *testing.T) {
	st := newSQLiteTest(t)
	space := &Space{Name: "seeded"}
	require.NoError(t, st.CreateSeededSpace(t.Context(), space))
	_, err := st.GetSpace(t.Context(), space.ID)
	require.NoError(t, err)
	categories, err := st.ListCategories(t.Context(), space.ID, false)
	require.NoError(t, err)
	require.NotEmpty(t, categories)
}

func TestCreateSeededSpaceCompensatesHouseholdOpenFailure(t *testing.T) {
	st := newSQLiteTest(t)
	space := &Space{ID: NewSpaceID(), Name: "failed"}
	path := filepath.Join(st.sqlite.dataDir, "households", space.ID.UUID().String()+".db")
	require.NoError(t, os.Mkdir(path, 0700))
	require.Error(t, st.CreateSeededSpace(t.Context(), space))
	_, err := st.GetSpace(t.Context(), space.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.DirExists(t, path, "failed provisioning must not remove a pre-existing path")
}

func TestRetireHouseholdEvictsScopeAndRejectsRetainedClones(t *testing.T) {
	st := newSQLiteTest(t)
	id := newSQLiteSpace(t, st)
	scoped, err := st.ForHousehold(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, st.sqlite.scopes[id.UUID()])
	require.NoError(t, st.retireHousehold(id, false))
	require.NotContains(t, st.sqlite.scopes, id.UUID())
	_, err = scoped.HouseholdDB(t.Context(), id)
	require.ErrorContains(t, err, "retired")
	_, err = st.HouseholdDB(t.Context(), id)
	require.ErrorContains(t, err, "retired")
	require.NoFileExists(t, filepath.Join(st.sqlite.dataDir, "households", id.UUID().String()+".db"))
}

func TestDeleteSpaceCheckpointsUncachedHouseholdBeforeArchiving(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	st, err := OpenSQLite(ctx, dir, time.Now)
	require.NoError(t, err)
	space := &Space{Name: "WAL recovery"}
	require.NoError(t, st.CreateSpace(ctx, space))
	db, err := st.HouseholdDB(ctx, space.ID)
	require.NoError(t, err)
	_, err = db.Write().ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	require.NoError(t, err)
	_, err = db.Write().ExecContext(ctx, `CREATE TABLE retirement_marker (value TEXT) STRICT`)
	require.NoError(t, err)
	_, err = db.Write().ExecContext(ctx, `INSERT INTO retirement_marker VALUES ('kept in WAL')`)
	require.NoError(t, err)
	path := filepath.Join(dir, "households", space.ID.String()+".db")
	mainBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	walBytes, err := os.ReadFile(path + "-wal")
	require.NoError(t, err)
	require.NotEmpty(t, walBytes)
	st.Close()
	// Reconstruct the committed on-disk state of a process that exited
	// without closing its pools: the new store has no cached household.
	require.NoError(t, os.WriteFile(path, mainBytes, 0600))
	require.NoError(t, os.WriteFile(path+"-wal", walBytes, 0600))
	st, err = OpenSQLite(ctx, dir, time.Now)
	require.NoError(t, err)
	defer st.Close()
	require.NoError(t, st.DeleteSpace(ctx, space.ID))
	archives, err := filepath.Glob(filepath.Join(dir, "households", "deleted", space.ID.String()+".*.db"))
	require.NoError(t, err)
	require.Len(t, archives, 1)
	archived, err := sqlitedb.Open(ctx, archives[0], time.Now)
	require.NoError(t, err)
	defer archived.Close()
	var value string
	require.NoError(t, archived.Read().QueryRowContext(ctx, `SELECT value FROM retirement_marker`).Scan(&value))
	require.Equal(t, "kept in WAL", value)
	require.NoFileExists(t, path+"-wal")
	require.NoFileExists(t, path+"-shm")
}
