// Package store persists server metadata and household ledgers in SQLite.
// Household transactions are scoped to one file; monetary values use exact
// currency-denominated integer minor units.
package store

import (
	"context"
	"errors"
	"fmt"

	"database/sql"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/google/uuid"
)

// ErrNotFound distinguishes a missing row from a broken query.
var ErrNotFound = errors.New("store: not found")

// SpaceID is the tenant key, a distinct type so an account id passed where the
// space belongs does not compile.
type SpaceID uuid.UUID

func (s SpaceID) UUID() uuid.UUID    { return uuid.UUID(s) }
func (s SpaceID) String() string     { return uuid.UUID(s).String() }
func (s SpaceID) IsZero() bool       { return uuid.UUID(s) == uuid.Nil }
func NewSpaceID() SpaceID            { return SpaceID(uuid.New()) }
func SpaceIDOf(id uuid.UUID) SpaceID { return SpaceID(id) }

func ParseSpaceID(s string) (SpaceID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return SpaceID{}, fmt.Errorf("store: invalid space id %q: %w", s, err)
	}
	return SpaceID(id), nil
}

type Store struct {
	// cipher seals the credential columns; nil until WithCipher.
	cipher *Cipher
	// sqlite is shared by scoped clones; transaction handles live only on clones.
	sqlite                *sqliteStore
	sqliteScope           uuid.UUID
	sqliteTx              *sqlitedb.DB
	sqliteInTx            bool
	sqlitePrimaryCurrency string
}

// primaryCurrency is materialized when the household is scoped, before its
// transaction begins. A currency-change workflow must obtain a fresh scope.
func (s *Store) primaryCurrency() (string, error) {
	if s.sqliteScope == uuid.Nil || s.sqlitePrimaryCurrency == "" {
		return "", errors.New("store: primary currency requires household scope")
	}
	return s.sqlitePrimaryCurrency, nil
}

// scoped reads authoritative metadata before transactions and retains it within them.
func (s *Store) scoped(ctx context.Context, id SpaceID) (*Store, error) {
	if s.sqliteScope != uuid.Nil {
		if s.sqliteScope != id.UUID() {
			return nil, errors.New("store: household scope mismatch")
		}
		if s.sqliteInTx {
			return s, nil
		}
		return s.ForHousehold(ctx, id)
	}
	if s.sqliteInTx {
		return nil, errors.New("store: household access in server transaction")
	}
	return s.ForHousehold(ctx, id)
}

// HouseholdCurrency returns the materialized currency, preserving transaction
// routing rather than reading server metadata from inside a household transaction.
func (s *Store) HouseholdCurrency(ctx context.Context, id SpaceID) (string, error) {
	h, err := s.scoped(ctx, id)
	if err != nil {
		return "", err
	}
	return h.primaryCurrency()
}

func (s *Store) Close() { s.closeSQLite() }

func (s *Store) Ping(ctx context.Context) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	return db.Read().QueryRowContext(ctx, "SELECT 1").Scan(new(int))
}

// InTx runs fn inside a transaction, committing on nil and rolling back on any
// error or panic. Nesting opens a savepoint, so a service wrapping another's
// write does not commit half of it.
// All writes within fn must use its callback Store, never the outer Store:
// SQLite has one writer per file, so an outer write waits for this transaction
// to finish and deadlocks without context cancellation. Nested work should use
// the callback Store's InTx (a savepoint), not the outer Store's InTx.
func (s *Store) InTx(ctx context.Context, fn func(*Store) error) error {
	return s.inSQLiteTx(ctx, fn)
}

// wrap classifies missing rows and SQLite constraints while preserving context.
func wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", what, sqliteError(err))
}

// scanner is shared by database/sql single-row and list queries.
type scanner interface {
	Scan(dest ...any) error
}

// scanValue reads a one-column row.
func scanValue[T any](row scanner) (T, error) {
	var value T
	err := row.Scan(&value)
	return value, err
}

type pair[A, B any] struct {
	first  A
	second B
}

// scanPair reads a two-column row.
func scanPair[A, B any](row scanner) (pair[A, B], error) {
	var one pair[A, B]
	err := row.Scan(&one.first, &one.second)
	return one, err
}
