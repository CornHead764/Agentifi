// Package storetest is the TestMain and the row fixtures of every test binary
// that reaches the database through internal/store. Not a _test.go file
// because Go does not share test code across packages.
//
// internal/store's own tests cannot import this package, since it imports
// store; they call internal/testdb directly and keep their fixtures local.
package storetest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/testdb"
)

// startTimeout bounds connecting and migrating a fresh schema.
const startTimeout = 60 * time.Second

var shared *store.Store

// Main is a test binary's TestMain: it migrates a private database, runs the
// tests, then runs cleanup and drops the schema. It exits the process. A
// database that cannot be created or migrated fails the binary rather than
// skipping its tests: a broken migration must not read as a passing suite.
func Main(m *testing.M, prefix string, cleanup ...func()) {
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	schema, reason := testdb.Start(ctx, prefix, func(ctx context.Context, path string) error {
		var err error
		if shared, err = store.Open(ctx, path); err != nil {
			return err
		}
		_, err = shared.Migrate(ctx)
		return err
	})
	cancel()
	if schema == nil {
		fmt.Fprintln(os.Stderr, "storetest: "+reason)
		os.Exit(1)
	}

	code := m.Run()

	schema.Drop()
	// Not deferred: os.Exit runs no defers.
	for _, one := range cleanup {
		one()
	}
	shared.Close()
	os.Exit(code)
}

// DB is the migrated store.
func DB(t testing.TB) *store.Store {
	t.Helper()
	return shared
}

// Empty is a migrated database of the test's own, with no rows in it, for a
// test about the server as a whole, such as one with no accounts yet. It is
// dropped when the test ends.
func Empty(t testing.TB) *store.Store {
	t.Helper()
	DB(t)
	var fresh *store.Store
	ctx, cancel := context.WithTimeout(t.Context(), startTimeout)
	defer cancel()
	prefix := "empty_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	schema, reason := testdb.Start(ctx, prefix, func(ctx context.Context, path string) error {
		var err error
		if fresh, err = store.Open(ctx, path); err != nil {
			return err
		}
		_, err = fresh.Migrate(ctx)
		return err
	})
	if schema == nil {
		t.Fatal(reason)
	}
	t.Cleanup(func() {
		fresh.Close()
		schema.Drop()
	})
	return fresh
}

// NewSpace creates an isolated tenant for one test. Tests share a database, so
// they must not share a space.
func NewSpace(t testing.TB) store.SpaceID {
	t.Helper()
	space := &store.Space{Name: t.Name(), PrimaryCurrency: "USD"}
	require.NoError(t, DB(t).CreateSpace(t.Context(), space))
	return space.ID
}

// NewOwnedSpace creates a space whose owner has accepted the membership;
// ListSpacesForUser sees only accepted ones.
func NewOwnedSpace(t testing.TB, name string, owner uuid.UUID) store.SpaceID {
	t.Helper()
	space := &store.Space{Name: name, PrimaryCurrency: "USD"}
	require.NoError(t, DB(t).CreateSpace(t.Context(), space))
	now := time.Now().UTC()
	require.NoError(t, DB(t).CreateMembership(t.Context(), space.ID, &store.Membership{
		SpaceID: space.ID, UserID: owner, Role: store.RoleOwner,
		InvitedAt: &now, AcceptedAt: &now,
	}))
	return space.ID
}

type AccountOption func(*store.Account)

func WithKind(kind domain.AccountKind) AccountOption {
	return func(a *store.Account) { a.Kind, a.Type = kind, string(kind) }
}

func WithConnection(id uuid.UUID) AccountOption {
	return func(a *store.Account) { a.ConnectionID = id }
}

// NewAccount creates a USD checking account counted in net worth.
func NewAccount(t testing.TB, spaceID store.SpaceID, name string, opts ...AccountOption) *store.Account {
	t.Helper()
	account := &store.Account{
		Name:              name,
		Kind:              domain.KindCash,
		Type:              "checking",
		Currency:          "USD",
		IncludeInNetWorth: true,
	}
	for _, opt := range opts {
		opt(account)
	}
	require.NoError(t, DB(t).CreateAccount(t.Context(), spaceID, account))
	return account
}

// NewConnection creates an active SimpleFIN connection with a placeholder
// access URL.
func NewConnection(t testing.TB, spaceID store.SpaceID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := DB(t).Pool().Exec(t.Context(), `
		INSERT INTO connections (id, space_id, access_url_encrypted, status)
		VALUES ($1, $2, 'x', 'active')`, id, spaceID.UUID())
	require.NoError(t, err)
	return id
}

// NewCategory creates an assignable expense category; parentID may be
// uuid.Nil.
func NewCategory(t testing.TB, spaceID store.SpaceID, name string, parentID uuid.UUID) *store.Category {
	t.Helper()
	category := &store.Category{
		Name: name, Kind: domain.CategoryExpense, ParentID: parentID,
		IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(t, DB(t).CreateCategory(t.Context(), spaceID, category))
	return category
}

// NoDatabase is a store opener for a run that must not reach the database.
func NoDatabase(context.Context) (*store.Store, error) {
	panic("the dry run must not open a database")
}

// PDF is the smallest thing the document store sniffs as a PDF.
func PDF() []byte { return []byte("%PDF-1.7\n% an invented statement\n") }

func NewTag(t testing.TB, spaceID store.SpaceID, name string) *store.Tag {
	t.Helper()
	tag := &store.Tag{Name: name}
	require.NoError(t, DB(t).CreateTag(t.Context(), spaceID, tag))
	return tag
}
