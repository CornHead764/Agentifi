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
	"github.com/CornHead764/agentifi/backend/internal/testdb"
)

// These tests run against a real SQLite file: exact numeric round trips and
// space scoping are properties of the database. internal/testdb owns the file
// and its lifecycle.

var (
	testDB   *Store
	testPath string
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	schema, reason := testdb.Start(ctx, "store", func(ctx context.Context, path string) error {
		var err error
		if testDB, err = Open(ctx, path); err != nil {
			return err
		}
		_, err = testDB.Migrate(ctx)
		return err
	})
	cancel()
	if schema == nil {
		// Fatal, not a skip: a broken migration must not read as a passing suite.
		fmt.Fprintln(os.Stderr, "store: "+reason)
		os.Exit(1)
	}
	testPath = schema.Path

	code := m.Run()

	schema.Drop()
	testDB.Close()
	os.Exit(code)
}

func db(t *testing.T) *Store {
	t.Helper()
	return testDB
}

// newSpace creates an isolated tenant for one test; tests share a database.
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
