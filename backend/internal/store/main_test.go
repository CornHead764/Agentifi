package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/testdb"
)

// These tests run against a real Postgres: exact numeric round trips and space
// scoping are properties of the database. internal/testdb owns the schema and
// its lifecycle.

var (
	testDB     *Store
	testSchema string
	// skipReason is set when the database cannot be reached, so every test
	// skips with the same explanation.
	skipReason string
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	schema, reason := testdb.Start(ctx, "store", func(ctx context.Context, cfg *pgxpool.Config) error {
		var err error
		if testDB, err = OpenPool(ctx, cfg); err != nil {
			return err
		}
		_, err = testDB.Migrate(ctx)
		return err
	})
	cancel()
	if schema == nil {
		skipReason = reason
		os.Exit(m.Run())
	}
	testSchema = schema.Name

	code := m.Run()

	schema.Drop()
	testDB.Close()
	os.Exit(code)
}

func testDatabaseURL() string { return testdb.URL() }

func db(t *testing.T) *Store {
	t.Helper()
	if testDB == nil {
		t.Skip("skipping: " + skipReason)
	}
	return testDB
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
