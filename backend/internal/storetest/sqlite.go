package storetest

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// New opens a migrated SQLite store in a private temporary data directory.
func New(t testing.TB) *store.Store {
	t.Helper()
	st, err := store.OpenSQLite(t.Context(), t.TempDir(), time.Now)
	require.NoError(t, err)
	t.Cleanup(st.Close)
	return st
}

// NewHousehold provisions server metadata and a migrated household file.
func NewHousehold(t testing.TB, st *store.Store) store.SpaceID {
	t.Helper()
	space := store.Space{Name: t.Name(), PrimaryCurrency: "USD", Timezone: "UTC"}
	require.NoError(t, st.CreateSpace(t.Context(), &space))
	return space.ID
}
