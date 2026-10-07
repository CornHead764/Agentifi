package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Conversions between Postgres and the domain's value types. Numeric columns
// are scanned as pgtype.Numeric and converted by internal/pgconv; there is no
// path here from a numeric column to a float.

// dateArg encodes a calendar day. domain.Date carries no zone so "which month
// is this in" cannot depend on the reader's.
func dateOf(t time.Time) domain.Date { return domain.DateOf(t) }

// domainID converts a key to the domain's string ids (the golden tests use
// Simplifi's opaque string ids).
func domainID(id uuid.UUID) domain.ID { return domain.ID(id.String()) }

func nullDomainID(id *uuid.UUID) domain.ID {
	if id == nil {
		return ""
	}
	return domainID(*id)
}

func ParseID(id domain.ID) (uuid.UUID, error) {
	parsed, err := uuid.Parse(string(id))
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: invalid id %q: %w", id, err)
	}
	return parsed, nil
}

// Deref reads a nullable column as fallback's value when NULL, or the zero
// value when fallback is omitted; for text, NULL and empty are the same fact.
func Deref[T any](p *T, fallback ...T) T {
	if p != nil {
		return *p
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	var zero T
	return zero
}

// NonNil is a nil-free copy of a slice column: pgx encodes a nil slice as
// NULL, and an empty JSON array must not read back as "null".
func NonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// PtrIf renders an absent value as a nil pointer, so a flag column with no
// value written becomes JSON null or a NULL argument rather than a zero one.
func PtrIf[T any](value T, present bool) *T {
	if !present {
		return nil
	}
	return &value
}

// jsonArg writes NULL for an absent document rather than JSON "null", which
// would make `provider_extra IS NOT NULL` true for every row.
func jsonArg(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func domainIDs(ids []uuid.UUID) []domain.ID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]domain.ID, len(ids))
	for i, id := range ids {
		out[i] = domainID(id)
	}
	return out
}
