package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Accounts at the bank that a connection must not create, keyed on the
// provider's account id — the only stable thing about a remote account. The
// labels beside it are a display snapshot; nothing matches on them.

type IgnoredRemoteAccount struct {
	ID           uuid.UUID
	ConnectionID uuid.UUID
	ExternalID   string
	Name         string
	Institution  string
	MaskedNumber string
	IgnoredAt    time.Time
}

// ListIgnoredRemoteAccounts returns one connection's refusals, newest first.
func (s *Store) ListIgnoredRemoteAccounts(
	ctx context.Context, spaceID SpaceID, connectionID uuid.UUID,
) ([]IgnoredRemoteAccount, error) {
	return queryAll(ctx, s.db, "store: list ignored remote accounts", func(row scanner) (IgnoredRemoteAccount, error) {
		var one IgnoredRemoteAccount
		err := row.Scan(&one.ID, &one.ConnectionID, &one.ExternalID,
			&one.Name, &one.Institution, &one.MaskedNumber, &one.IgnoredAt)
		return one, err
	},
		`SELECT id, connection_id, external_id, name, institution, masked_number, ignored_at
		   FROM ignored_remote_accounts
		  WHERE space_id = $1 AND connection_id = $2
		  ORDER BY ignored_at DESC`,
		spaceID.UUID(), connectionID)
}

func (s *Store) IgnoredExternalIDs(
	ctx context.Context, spaceID SpaceID, connectionID uuid.UUID,
) (map[string]bool, error) {
	ignored, err := s.ListIgnoredRemoteAccounts(ctx, spaceID, connectionID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ignored))
	for _, one := range ignored {
		out[one.ExternalID] = true
	}
	return out, nil
}

// IgnoreRemoteAccount records a refusal idempotently, refreshing the display
// labels on a repeat call.
func (s *Store) IgnoreRemoteAccount(
	ctx context.Context, spaceID SpaceID, one *IgnoredRemoteAccount,
) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	err := s.db.QueryRow(ctx,
		`INSERT INTO ignored_remote_accounts
		     (id, space_id, connection_id, external_id, name, institution, masked_number)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (connection_id, external_id) DO UPDATE
		     SET name = EXCLUDED.name,
		         institution = EXCLUDED.institution,
		         masked_number = EXCLUDED.masked_number
		 RETURNING id, ignored_at`,
		one.ID, spaceID.UUID(), one.ConnectionID, one.ExternalID,
		one.Name, one.Institution, one.MaskedNumber).Scan(&one.ID, &one.IgnoredAt)
	return wrap("store: ignore remote account", err)
}

// RestoreRemoteAccount lifts a refusal, reporting whether there was one; false
// is a 404 at the API, since the client's list is stale.
func (s *Store) RestoreRemoteAccount(
	ctx context.Context, spaceID SpaceID, connectionID, id uuid.UUID,
) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM ignored_remote_accounts
		  WHERE space_id = $1 AND connection_id = $2 AND id = $3`,
		spaceID.UUID(), connectionID, id)
	if err != nil {
		return false, wrap("store: restore remote account", err)
	}
	return tag.RowsAffected() > 0, nil
}
