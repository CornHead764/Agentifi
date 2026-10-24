package store

import (
	"context"
	"database/sql"
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
	server, err := s.server()
	if err != nil {
		return SetupGuide{}, err
	}
	var guide SetupGuide
	var dismissed sql.Null[int64]
	var skipped jsonArray[string]
	err = server.Read().QueryRowContext(ctx, `SELECT setup_guide_dismissed_at, setup_guide_skipped FROM households WHERE id = ?`, spaceID.UUID()).Scan(&dismissed, &skipped)
	if err != nil {
		return guide, wrap("store: get setup guide", err)
	}
	guide.DismissedAt, guide.Skipped = nullableTime(dismissed), []string(skipped)
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return guide, err
	}
	err = db.Read().QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM transactions WHERE source = ?),
		       EXISTS (SELECT 1 FROM accounts),
		       EXISTS (SELECT 1 FROM connections WHERE NOT is_deleted),
		       EXISTS (SELECT 1 FROM connections c WHERE NOT c.is_deleted AND c.status <> ?
		                  AND c.last_successful_sync_at IS NOT NULL
		                  AND EXISTS (SELECT 1 FROM accounts a WHERE a.connection_id = c.id AND NOT a.is_deleted)),
		       EXISTS (SELECT 1 FROM assistant_connections WHERE is_enabled),
		       EXISTS (SELECT 1 FROM bill_connections)`,
		string(domain.SourceSimplifiImport), string(ConnectionPendingLink),
	).Scan(&guide.Imported, &guide.HasAccounts, &guide.Connected, &guide.Synced, &guide.Assistant, &guide.BillProviders)
	return guide, wrap("store: get setup guide", err)
}

// SaveSetupGuide writes what somebody chose; nil dismissedAt shows the guide.
func (s *Store) SaveSetupGuide(
	ctx context.Context, spaceID SpaceID, dismissedAt *time.Time, skipped []string,
) error {
	if skipped == nil {
		skipped = []string{}
	}
	db, err := s.server()
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: save setup guide", `
		UPDATE households SET setup_guide_dismissed_at = ?, setup_guide_skipped = ?, updated_at = ?
		WHERE id = ?`, nullableMillis(dismissedAt), jsonArray[string](skipped), db.Now(), spaceID.UUID())
}
