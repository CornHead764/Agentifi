package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"

	"github.com/stretchr/testify/require"
)

// A household file copied under another household's name must be refused,
// not served as that household's data.
func TestAHouseholdFileUnderAnotherHouseholdsNameIsRefused(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	st, err := OpenSQLite(ctx, dir, time.Now)
	require.NoError(t, err)
	a, b := &Space{Name: "a"}, &Space{Name: "b"}
	require.NoError(t, st.CreateSpace(ctx, a))
	require.NoError(t, st.CreateSpace(ctx, b))
	st.Close()

	data, err := os.ReadFile(filepath.Join(dir, "households", a.ID.UUID().String()+".db"))
	require.NoError(t, err)
	bPath := filepath.Join(dir, "households", b.ID.UUID().String()+".db")
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(bPath + suffix)
	}
	require.NoError(t, os.WriteFile(bPath, data, 0o600))

	st, err = OpenSQLite(ctx, dir, time.Now)
	require.NoError(t, err)
	defer st.Close()
	_, err = st.HouseholdDB(ctx, b.ID)
	require.ErrorContains(t, err, "belongs to household "+a.ID.UUID().String())
	_, err = st.HouseholdDB(ctx, a.ID)
	require.NoError(t, err)
}

func TestMissingHouseholdLookupDoesNotCreateFileAfterRestart(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	st, err := OpenSQLite(ctx, dir, time.Now)
	require.NoError(t, err)
	space := &Space{Name: "deleted"}
	require.NoError(t, st.CreateSpace(ctx, space))
	require.NoError(t, st.DeleteSpace(ctx, space.ID))
	st.Close()
	st, err = OpenSQLite(ctx, dir, time.Now)
	require.NoError(t, err)
	defer st.Close()
	for _, id := range []SpaceID{space.ID, NewSpaceID()} {
		_, err := st.ListCategories(ctx, id, false)
		require.ErrorIs(t, err, ErrNotFound)
		require.NoFileExists(t, filepath.Join(dir, "households", id.UUID().String()+".db"))
	}
}

func TestHouseholdWithoutMetadataIsNotFound(t *testing.T) {
	st := newSQLiteTest(t)
	id := NewSpaceID()
	path := filepath.Join(st.sqlite.dataDir, "households", id.UUID().String()+".db")
	db, err := sqlitedb.Open(t.Context(), path, time.Now)
	require.NoError(t, err)
	_, err = db.MigrateHousehold(t.Context(), "")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	_, err = st.HouseholdDB(t.Context(), id)
	require.ErrorIs(t, err, ErrNotFound)
}
