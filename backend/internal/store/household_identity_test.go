package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
