package store

import (
	"os"
	"path/filepath"
	"testing"

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
	require.NoDirExists(t, path)
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
