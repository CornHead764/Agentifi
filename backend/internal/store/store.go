// Package store is the persistence layer: a pgx pool, hand-written SQL, and
// the single translation from database rows to the domain structs.
//
// Every tenant-owned query takes a SpaceID and puts it in the WHERE clause, so
// a row id from one household never reaches another's rows. No amount is ever
// a float64: numerics are read as pgtype.Numeric and converted exactly through
// types.go — pgx hands out a float64 if offered one, so never offer.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned instead of pgx.ErrNoRows so callers above this
// package do not have to import pgx to tell a missing row from a broken query.
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

// DB is the subset of pgx both a pool and a transaction satisfy, so every
// query in this package runs unchanged inside or outside a transaction.
type DB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store holds the pool and the handle queries run against. Inside InTx the
// handle is the transaction, so calls on the callback's store cannot escape it.
type Store struct {
	pool *pgxpool.Pool
	db   DB
	tx   pgx.Tx
	// cipher seals the credential columns; nil until WithCipher.
	cipher *Cipher
}

// ParseConfig turns a libpq URL into a pool config. A unix socket goes in the
// `host` query parameter (postgres://u@/db?host=/run), not the authority, or
// pgx treats the path as a hostname.
func ParseConfig(databaseURL string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parsing database url: %w", err)
	}
	return cfg, nil
}

// OpenPool connects with an adjusted config and pings, so a configuration
// mistake fails at startup rather than on the first request.
func OpenPool(ctx context.Context, cfg *pgxpool.Config) (*Store, error) {
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: connecting: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: connecting: %w", err)
	}
	return &Store{pool: pool, db: pool}, nil
}

// Pool exposes the underlying pool for the migration runner, which needs a
// database/sql handle, and for health checks.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Conn is the handle this store's queries run against: the transaction inside
// InTx, the pool outside. SQL written outside this package goes through it so
// it commits or rolls back with the store's writes.
func (s *Store) Conn() DB { return s.db }

func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Ping(ctx context.Context) error {
	if s.pool == nil {
		return errors.New("store: no pool on a transaction-scoped store")
	}
	return s.pool.Ping(ctx)
}

// InTx runs fn inside a transaction, committing on nil and rolling back on any
// error or panic. Nesting opens a savepoint, so a service wrapping another's
// write does not commit half of it.
func (s *Store) InTx(ctx context.Context, fn func(*Store) error) (err error) {
	var tx pgx.Tx
	if s.tx != nil {
		tx, err = s.tx.Begin(ctx)
	} else {
		tx, err = s.pool.Begin(ctx)
	}
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if err = fn(&Store{pool: s.pool, db: tx, tx: tx, cipher: s.cipher}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// wrap turns pgx's no-rows sentinel into this package's, leaving every other
// error alone with its context attached.
func wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// execOne runs a write that must hit a row, reporting ErrNotFound when it
// changed none.
func (s *Store) execOne(ctx context.Context, what, sql string, args ...any) error {
	tag, err := s.db.Exec(ctx, sql, args...)
	if err != nil {
		return wrap(what, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap(what, ErrNotFound)
	}
	return nil
}

// scanner is what pgx.Row and pgx.Rows share, so one scan function per table
// serves both single-row and list queries.
type scanner interface {
	Scan(dest ...any) error
}

// collect drains rows through a per-row scan function. Not RowToStructByName:
// the hand-written mapping makes a renamed column break the build rather than
// return a zero value.
func collect[T any](rows pgx.Rows, scan func(scanner) (T, error)) ([]T, error) {
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// queryAll runs a list query and drains it through scan, naming the query in
// any error it returns.
func queryAll[T any](ctx context.Context, db DB, what string, scan func(scanner) (T, error), sql string, args ...any) ([]T, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrap(what, err)
	}
	out, err := collect(rows, scan)
	if err != nil {
		return nil, wrap(what, err)
	}
	return out, nil
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
