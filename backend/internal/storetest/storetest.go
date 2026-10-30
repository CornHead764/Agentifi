// Package storetest provides SQLite test stores and domain row fixtures.
package storetest

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

var (
	shared *store.Store
	bound  sync.Map // testing.TB -> *store.Store
)

// Private gives this test its own store (fresh server catalogue and settings)
// and makes DB(t), and so every fixture helper, return it for this test.
func Private(t testing.TB) *store.Store {
	t.Helper()
	if st, ok := bound.Load(t); ok {
		return st.(*store.Store)
	}
	st := New(t)
	bound.Store(t, st)
	t.Cleanup(func() { bound.Delete(t) })
	return st
}

// Main opens a private SQLite data directory for a test binary.
func Main(m *testing.M, prefix string, cleanup ...func()) {
	sqlitedb.SetReadersForTests(1)
	dir, err := os.MkdirTemp("", "agentifi-"+prefix+"-")
	if err != nil {
		panic(err)
	}
	shared, err = store.OpenSQLite(context.Background(), dir, time.Now)
	if err != nil {
		_ = os.RemoveAll(dir)
		panic(err)
	}
	code := m.Run()
	for _, one := range cleanup {
		one()
	}
	shared.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func DB(t testing.TB) *store.Store {
	t.Helper()
	if st, ok := bound.Load(t); ok {
		return st.(*store.Store)
	}
	if shared == nil {
		t.Fatal("storetest.Main was not called")
	}
	return shared
}

func Empty(t testing.TB) *store.Store { return New(t) }

// NewSpace creates an isolated tenant for one test. Tests share a schema, so
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
	connection := &store.Connection{ID: uuid.New(), Name: "Test", Status: store.ConnectionActive}
	st := DB(t)
	cipher, err := store.NewCipher("test-only")
	require.NoError(t, err)
	require.NoError(t, st.WithCipher(cipher).CreateConnection(t.Context(), spaceID, connection, "x"))
	return connection.ID
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
