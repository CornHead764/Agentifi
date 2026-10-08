// Package service is the orchestration layer: it loads the inputs of the pure
// calculations in internal/domain and writes their outputs.
//
// Each service splits into a pure decision half (rows in, a decision or diff
// out) and an apply half that writes exactly what the decision returned and
// re-derives nothing, so a preview cannot drift from what is applied. No
// calculation is reimplemented here. Numeric columns this package scans itself
// are read as dbconv.Number, never float64.
package service

import (
	"bytes"
	"context"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// dbConn is what the database and a transaction both satisfy, so the SQL in
// this package runs unchanged inside or outside a transaction.
type dbConn = store.DB

// base is the store every service in this package holds. Its SQL runs on the
// store's handle, so a service built from a store inside store.InTx writes inside
// that transaction.
type base struct {
	store *store.Store
}

func newBase(st *store.Store) base { return base{store: st} }

func (b base) conn() dbConn { return b.store.Conn() }

// inTx runs fn inside a transaction, or a savepoint of the caller's when the
// service's store is already in one.
func (b base) inTx(ctx context.Context, fn func(tx *store.Store) error) error {
	return b.store.InTx(ctx, fn)
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// lessUUID orders keys by their bytes so listings are stable across runs.
func lessUUID(a, b uuid.UUID) bool { return bytes.Compare(a[:], b[:]) < 0 }

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
