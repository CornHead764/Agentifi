// Package nullid is a nullable uuid column. The store's own types say "none"
// with uuid.Nil, and the column says it with NULL, so From is the only way in
// and maps uuid.Nil to NULL: the zero uuid never reaches a column where NULL
// belongs, where a column with no foreign key would keep it silently.
package nullid

import (
	"database/sql/driver"

	"github.com/google/uuid"
)

// ID is NULL when zero.
type ID struct{ id uuid.UUID }

// From is the write side; uuid.Nil is NULL.
func From(id uuid.UUID) ID { return ID{id} }

// UUID is the read side; NULL is uuid.Nil.
func (n ID) UUID() uuid.UUID { return n.id }

func (n *ID) Scan(src any) error {
	n.id = uuid.Nil
	if src == nil {
		return nil
	}
	return n.id.Scan(src)
}

func (n ID) Value() (driver.Value, error) {
	if n.id == uuid.Nil {
		return nil, nil
	}
	return n.id.String(), nil
}
