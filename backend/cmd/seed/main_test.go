package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestRefusesNonemptyDatabase(t *testing.T) {
	data := t.TempDir()
	db, err := store.OpenSQLite(t.Context(), data, time.Now)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	s := store.Space{Name: "Existing household", PrimaryCurrency: "USD", Timezone: "UTC"}
	require.NoError(t, db.CreateSpace(t.Context(), &s))
	db.Close()
	var out bytes.Buffer
	err = run(t.Context(), []string{"--data", data, "--storage-path", t.TempDir()}, &out)
	require.ErrorContains(t, err, "refusing database")
	db, err = store.OpenSQLite(t.Context(), data, time.Now)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	spaces, err := db.ListSpaces(t.Context())
	require.NoError(t, err)
	require.Len(t, spaces, 1)
	require.Empty(t, out.String())
}
