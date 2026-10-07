package store

import (
	"context"

	"github.com/google/uuid"
)

// Dismissed suggestions. The Suggested tab re-derives its proposals on every
// read, so a dismissal is keyed on the group's signature (account, direction,
// currency, kind, matched wording) rather than a row: a new charge joining the
// group must not resurrect it.

// ListDismissedSuggestions returns the signatures this space has waved away.
func (s *Store) ListDismissedSuggestions(ctx context.Context, spaceID SpaceID) ([]string, error) {
	return queryAll(ctx, s.db, "store: list dismissed suggestions", scanValue[string],
		`SELECT signature FROM suggestion_dismissals WHERE space_id = $1 ORDER BY dismissed_at DESC`,
		spaceID.UUID())
}

// DismissSuggestion records one, idempotently.
func (s *Store) DismissSuggestion(ctx context.Context, spaceID SpaceID, signature string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO suggestion_dismissals (id, space_id, signature)
		VALUES ($1, $2, $3)
		ON CONFLICT (space_id, signature) DO NOTHING`,
		uuid.New(), spaceID.UUID(), signature)
	return wrap("store: dismiss suggestion", err)
}

// RestoreSuggestion undoes a dismissal, reporting whether one was there so
// the API can 404.
func (s *Store) RestoreSuggestion(ctx context.Context, spaceID SpaceID, signature string) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM suggestion_dismissals WHERE space_id = $1 AND signature = $2`,
		spaceID.UUID(), signature)
	if err != nil {
		return false, wrap("store: restore suggestion", err)
	}
	return tag.RowsAffected() > 0, nil
}
