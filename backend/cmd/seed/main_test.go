package main

import (
	"bytes"
	"net/url"
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/CornHead764/agentifi/backend/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { storetest.Main(m, "seedcmd") }

func TestRefusesNonemptyDatabase(t *testing.T) {
	db := storetest.Empty(t)
	s := store.Space{Name: "Existing household", PrimaryCurrency: "USD", Timezone: "UTC"}
	require.NoError(t, db.CreateSpace(t.Context(), &s))
	var schema string
	require.NoError(t, db.Conn().QueryRow(t.Context(), "SELECT current_schema()").Scan(&schema))
	u, err := url.Parse(testdb.URL())
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	var out bytes.Buffer
	err = run(t.Context(), []string{"--database-url", u.String(), "--storage-path", t.TempDir()}, &out)
	require.ErrorContains(t, err, "refusing database")
	spaces, err := db.ListSpaces(t.Context())
	require.NoError(t, err)
	require.Len(t, spaces, 1)
	require.Empty(t, out.String())
}
