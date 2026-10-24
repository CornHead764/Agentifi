package store

import (
	"context"
	"github.com/google/uuid"
)

// Dismissals are household-local signatures, not individual ledger rows.
func (s *Store) ListDismissedSuggestions(ctx context.Context, spaceID SpaceID) ([]string, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list dismissed suggestions", scanValue[string], `SELECT signature FROM suggestion_dismissals ORDER BY dismissed_at DESC`)
}

func (s *Store) DismissSuggestion(ctx context.Context, spaceID SpaceID, signature string) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `INSERT INTO suggestion_dismissals (id, signature, dismissed_at) VALUES (?, ?, ?) ON CONFLICT (signature) DO NOTHING`, uuid.New(), signature, db.Now())
	return wrap("store: dismiss suggestion", err)
}

func (s *Store) RestoreSuggestion(ctx context.Context, spaceID SpaceID, signature string) (bool, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return false, err
	}
	tag, err := db.Write().ExecContext(ctx, `DELETE FROM suggestion_dismissals WHERE signature = ?`, signature)
	if err != nil {
		return false, wrap("store: restore suggestion", err)
	}
	n, err := tag.RowsAffected()
	return n > 0, wrap("store: restore suggestion", err)
}
