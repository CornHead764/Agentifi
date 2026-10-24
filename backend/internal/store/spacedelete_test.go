package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeleteSpaceArchivesHouseholdAndRemovesMetadata(t *testing.T) {
	st := newSQLiteTest(t)
	space := &Space{Name: "retire me"}
	require.NoError(t, st.CreateSpace(t.Context(), space))
	_, err := st.HouseholdDB(t.Context(), space.ID)
	require.NoError(t, err)
	source := filepath.Join(st.sqlite.dataDir, "households", space.ID.UUID().String()+".db")
	require.FileExists(t, source)
	require.NoError(t, st.DeleteSpace(t.Context(), space.ID))
	_, err = st.GetSpace(t.Context(), space.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoFileExists(t, source)
	files, err := filepath.Glob(filepath.Join(st.sqlite.dataDir, "households", "deleted", space.ID.UUID().String()+".*.db"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	info, err := os.Stat(files[0])
	require.NoError(t, err)
	require.Positive(t, info.Size())
}

func TestDeleteSpaceRejectsTransactionWithoutDeletingMetadata(t *testing.T) {
	st := newSQLiteTest(t)
	space := &Space{Name: "still live"}
	require.NoError(t, st.CreateSpace(t.Context(), space))
	require.Error(t, st.InTx(t.Context(), func(tx *Store) error {
		return tx.DeleteSpace(t.Context(), space.ID)
	}))
	found, err := st.GetSpace(t.Context(), space.ID)
	require.NoError(t, err)
	require.False(t, found.IsDeleted)
}
