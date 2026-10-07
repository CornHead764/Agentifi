package store

import "github.com/google/uuid"

// containsID reports whether a row with the given id is in rows.
func containsID[T any](rows []T, id uuid.UUID, idOf func(T) uuid.UUID) bool {
	for _, row := range rows {
		if idOf(row) == id {
			return true
		}
	}
	return false
}
