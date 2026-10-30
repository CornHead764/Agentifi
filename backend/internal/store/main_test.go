package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
)

var testDB *Store

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agentifi-store-")
	if err != nil {
		panic(err)
	}
	testDB, err = OpenSQLite(context.Background(), dir, time.Now)
	if err != nil {
		panic(err)
	}
	code := m.Run()
	testDB.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func db(t *testing.T) *Store {
	t.Helper()
	return testDB
}

func householdDB(t *testing.T, space SpaceID) *sqlitedb.DB {
	t.Helper()
	handle, err := db(t).household(t.Context(), space.UUID())
	require.NoError(t, err)
	return handle
}

func serverDB(t *testing.T) *sqlitedb.DB {
	t.Helper()
	handle, err := db(t).server()
	require.NoError(t, err)
	return handle
}

// newSpace creates an isolated tenant for one test; tests share a schema.
func newSpace(t *testing.T) SpaceID {
	t.Helper()
	space := &Space{Name: t.Name()}
	require.NoError(t, db(t).CreateSpace(t.Context(), space))
	return space.ID
}

func newAccount(t *testing.T, spaceID SpaceID, name string) *Account {
	t.Helper()
	account := &Account{
		Name:              name,
		Kind:              domain.KindCash,
		Type:              "checking",
		Currency:          "USD",
		IncludeInNetWorth: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), spaceID, account))
	return account
}

func newTag(t *testing.T, spaceID SpaceID, name string) *Tag {
	t.Helper()
	tag := &Tag{Name: name}
	require.NoError(t, db(t).CreateTag(t.Context(), spaceID, tag))
	return tag
}

func newCategory(t *testing.T, spaceID SpaceID, name string, kind domain.CategoryKind) *Category {
	t.Helper()
	category := &Category{Name: name, Kind: kind, IsUserAssignable: true, IsEditable: true}
	require.NoError(t, db(t).CreateCategory(t.Context(), spaceID, category))
	return category
}

func newUser(t *testing.T) *User {
	t.Helper()
	user := &User{Email: fmt.Sprintf("%s-%s@example.test", t.Name(), uuid.NewString()), IsActive: true}
	require.NoError(t, db(t).CreateUser(t.Context(), user))
	return user
}
