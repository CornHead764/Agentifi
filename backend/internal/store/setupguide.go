package store

import (
	"context"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// SetupGuide is what the setup guide knows of one space: what somebody chose
// (hiding it, skipping steps) and what the ledger already shows done.
type SetupGuide struct {
	DismissedAt *time.Time
	Skipped     []string

	// Imported is a Simplifi import written into the space.
	Imported bool
	// HasAccounts closes the import to this space, which takes only an
	// empty one.
	HasAccounts bool
	// Connected is a SimpleFIN connection, linked or not.
	Connected bool
	// Synced is a connection whose accounts are linked and that has synced
	// at least once.
	Synced bool
	// Assistant is a model the space's assistant can reach.
	Assistant bool
	// BillProviders is a bill provider connection.
	BillProviders bool
}

func (s *Store) GetSetupGuide(ctx context.Context, spaceID SpaceID) (SetupGuide, error) {
	var guide SetupGuide
	err := s.db.QueryRow(ctx, `
		SELECT s.setup_guide_dismissed_at, s.setup_guide_skipped,
		       EXISTS (SELECT 1 FROM transactions t WHERE t.space_id = s.id AND t.source = $2),
		       EXISTS (SELECT 1 FROM accounts a WHERE a.space_id = s.id),
		       EXISTS (SELECT 1 FROM connections c WHERE c.space_id = s.id AND NOT c.is_deleted),
		       EXISTS (SELECT 1 FROM connections c
		                WHERE c.space_id = s.id AND NOT c.is_deleted AND c.status <> $3
		                  AND c.last_successful_sync_at IS NOT NULL
		                  AND EXISTS (SELECT 1 FROM accounts a
		                               WHERE a.space_id = s.id AND a.connection_id = c.id
		                                 AND NOT a.is_deleted)),
		       EXISTS (SELECT 1 FROM assistant_connections x WHERE x.space_id = s.id AND x.is_enabled),
		       EXISTS (SELECT 1 FROM bill_connections b WHERE b.space_id = s.id)
		FROM spaces s WHERE s.id = $1`,
		spaceID.UUID(), string(domain.SourceSimplifiImport), string(ConnectionPendingLink),
	).Scan(&guide.DismissedAt, &guide.Skipped, &guide.Imported, &guide.HasAccounts,
		&guide.Connected, &guide.Synced, &guide.Assistant, &guide.BillProviders)
	return guide, wrap("store: get setup guide", err)
}

// SaveSetupGuide writes what somebody chose; nil dismissedAt shows the guide.
func (s *Store) SaveSetupGuide(
	ctx context.Context, spaceID SpaceID, dismissedAt *time.Time, skipped []string,
) error {
	if skipped == nil {
		skipped = []string{}
	}
	return s.execOne(ctx, "store: save setup guide", `
		UPDATE spaces SET setup_guide_dismissed_at = $2, setup_guide_skipped = $3, updated_at = now()
		WHERE id = $1`, spaceID.UUID(), dismissedAt, skipped)
}
