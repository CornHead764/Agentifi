package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"reflect"
	"strings"
	"sync"
	"testing"
)

var testStores sync.Map

func db(t *testing.T) *store.Store {
	t.Helper()
	if value, ok := testStores.Load(t); ok {
		return value.(*store.Store)
	}
	s := storetest.New(t)
	testStores.Store(t, s)
	t.Cleanup(func() { testStores.Delete(t) })
	return s
}
func householdSQL(t *testing.T, space uuid.UUID) sqlitedb.Conn {
	t.Helper()
	h, err := db(t).HouseholdDB(t.Context(), store.SpaceID(space))
	require.NoError(t, err)
	return h.Write()
}
func queryRow(t *testing.T, ctx context.Context, query string, space uuid.UUID, args ...any) *sql.Row {
	t.Helper()
	if strings.Contains(query, "FROM households") || strings.Contains(query, "FROM memberships") {
		server, err := db(t).ServerDB()
		require.NoError(t, err)
		return server.Read().QueryRowContext(ctx, query, append([]any{space}, args...)...)
	}
	return householdSQL(t, space).QueryRowContext(ctx, query, args...)
}
func scanValue[T any](t *testing.T, row interface{ Scan(...any) error }) T {
	t.Helper()
	var value T
	if reflect.TypeOf(&value).Elem().Kind() == reflect.Slice {
		var raw string
		require.NoError(t, row.Scan(&raw))
		require.NoError(t, json.Unmarshal([]byte(raw), &value))
	} else {
		require.NoError(t, row.Scan(&value))
	}
	return value
}

func newOwnedSpace(t *testing.T, user uuid.UUID) store.SpaceID {
	t.Helper()
	space := store.Space{Name: "Personal", PrimaryCurrency: "USD", Timezone: "UTC"}
	require.NoError(t, db(t).CreateSpace(t.Context(), &space))
	require.NoError(t, db(t).CreateMembership(t.Context(), space.ID, &store.Membership{UserID: user, Role: store.RoleOwner}))
	return space.ID
}
