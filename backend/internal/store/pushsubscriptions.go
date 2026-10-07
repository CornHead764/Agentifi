package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Browsers a person has allowed notifications in; a send goes to all of them.
// Keyed on the push service's per-browser endpoint, so re-subscribing updates
// the row.

type PushSubscription struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Endpoint   string
	P256dh     string
	Auth       string
	UserAgent  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Credentials is the shape the push provider takes.
func (p PushSubscription) Credentials() provider.PushSubscription {
	return provider.PushSubscription{Endpoint: p.Endpoint, P256dh: p.P256dh, Auth: p.Auth}
}

func (s *Store) ListPushSubscriptions(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID,
) ([]PushSubscription, error) {
	return queryAll(ctx, s.db, "store: list push subscriptions", func(row scanner) (PushSubscription, error) {
		var one PushSubscription
		err := row.Scan(&one.ID, &one.UserID, &one.Endpoint, &one.P256dh, &one.Auth,
			&one.UserAgent, &one.CreatedAt, &one.LastUsedAt)
		return one, err
	},
		`SELECT id, user_id, endpoint, p256dh, auth, user_agent, created_at, last_used_at
		   FROM push_subscriptions WHERE space_id = $1 AND user_id = $2
		  ORDER BY created_at`, spaceID.UUID(), userID)
}

// SavePushSubscription records a browser or refreshes its keys. Idempotent,
// since every page reload re-subscribes.
func (s *Store) SavePushSubscription(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID, one *PushSubscription,
) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	err := s.db.QueryRow(ctx,
		`INSERT INTO push_subscriptions
		     (id, space_id, user_id, endpoint, p256dh, auth, user_agent)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (user_id, endpoint) DO UPDATE
		     SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
		         user_agent = EXCLUDED.user_agent
		 RETURNING id, created_at`,
		one.ID, spaceID.UUID(), userID, one.Endpoint, one.P256dh, one.Auth, one.UserAgent).
		Scan(&one.ID, &one.CreatedAt)
	return wrap("store: save push subscription", err)
}

func (s *Store) DeletePushSubscription(
	ctx context.Context, spaceID SpaceID, userID, id uuid.UUID,
) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM push_subscriptions WHERE space_id = $1 AND user_id = $2 AND id = $3`,
		spaceID.UUID(), userID, id)
	if err != nil {
		return false, wrap("store: delete push subscription", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeletePushSubscriptionByEndpoint drops a subscription the push service says
// is gone (uninstalled browser, revoked permission).
func (s *Store) DeletePushSubscriptionByEndpoint(
	ctx context.Context, spaceID SpaceID, endpoint string,
) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM push_subscriptions WHERE space_id = $1 AND endpoint = $2`,
		spaceID.UUID(), endpoint)
	return wrap("store: delete push subscription", err)
}

func (s *Store) TouchPushSubscription(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE push_subscriptions SET last_used_at = now() WHERE id = $1`, id)
	return wrap("store: touch push subscription", err)
}
