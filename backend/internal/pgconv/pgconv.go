// Package pgconv contains nullable domain-value helpers shared by API and store.
package pgconv

import (
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// NullDate writes NULL for the zero date. An unset effective date means "same
// as the posted date"; 0001-01-01 would file the row two thousand years ago.
func NullDate(d domain.Date) *time.Time {
	if d.IsZero() {
		return nil
	}
	t := d.Time()
	return &t
}

// NullText writes NULL for the empty string, so a cleared note does not sort
// and filter as a zero-length one.
func NullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func NullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// ReadNullDate reads a nullable date column; NULL is the zero date, which
// NullDate writes back as NULL.
func ReadNullDate(t *time.Time) domain.Date {
	if t == nil {
		return domain.Date{}
	}
	return domain.DateOf(*t)
}

// ReadNullUUID reads a nullable uuid column; NULL is uuid.Nil, which NullUUID
// writes back as NULL.
func ReadNullUUID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}
