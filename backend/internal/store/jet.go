package store

import (
	"math"

	. "github.com/go-jet/jet/v2/sqlite"
)

// paged keeps limit rows of stmt after skipping offset, ignoring either when it
// is not positive. SQLite has no OFFSET without a LIMIT, and go-jet writes no
// LIMIT for a negative count, so an offset alone takes the largest limit. It
// sets the clauses on stmt itself, as go-jet's builders do, and returns it.
func paged(stmt SelectStatement, limit, offset int64) SelectStatement {
	if limit <= 0 && offset > 0 {
		limit = math.MaxInt64
	}
	if limit > 0 {
		stmt = stmt.LIMIT(limit)
	}
	if offset > 0 {
		stmt = stmt.OFFSET(offset)
	}
	return stmt
}
