package store

import (
	"math"

	. "github.com/go-jet/jet/v2/sqlite"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store/minor"
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

// rowMoney is how a row mapper reads a generated row's money columns, each in
// the currency that the row, or a row joined to it, names. It keeps the first
// error, so a mapper reads every column and checks err once.
type rowMoney struct{ err error }

func (m *rowMoney) read(amount minor.Amount, currency string) domain.Money {
	value, err := amount.Money(currency)
	if err != nil && m.err == nil {
		m.err = err
	}
	return value
}

// readNull reads a nullable column as the store's value and Has flag.
func (m *rowMoney) readNull(amount *minor.Amount, currency string) (domain.Money, bool) {
	if amount == nil {
		return domain.Money{}, false
	}
	return m.read(*amount, currency), true
}
