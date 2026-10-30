package store

import (
	"context"
	"database/sql"
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
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list push subscriptions", func(row scanner) (PushSubscription, error) {
		var one PushSubscription
		var created int64
		var used sql.Null[int64]
		err := row.Scan(&one.ID, &one.UserID, &one.Endpoint, &one.P256dh, &one.Auth,
			&one.UserAgent, &created, &used)
		one.CreatedAt, one.LastUsedAt = fromUnixMillis(created), nullableTime(used)
		return one, err
	},
		`SELECT id, user_id, endpoint, p256dh, auth, user_agent, created_at, last_used_at
		   FROM push_subscriptions WHERE user_id = ?
		  ORDER BY created_at`, userID)
}

// SavePushSubscription records a browser or refreshes its keys. Idempotent,
// since every page reload re-subscribes.
func (s *Store) SavePushSubscription(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID, one *PushSubscription,
) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	var created int64
	err = db.Write().QueryRowContext(ctx,
		`INSERT INTO push_subscriptions
		     (id, user_id, endpoint, p256dh, auth, user_agent, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, endpoint) DO UPDATE
		     SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
		         user_agent = EXCLUDED.user_agent
		 RETURNING id, created_at`,
		one.ID, userID, one.Endpoint, one.P256dh, one.Auth, one.UserAgent, db.Now()).
		Scan(&one.ID, &created)
	one.CreatedAt = fromUnixMillis(created)
	one.UserID = userID
	return wrap("store: save push subscription", err)
}

func (s *Store) DeletePushSubscription(
	ctx context.Context, spaceID SpaceID, userID, id uuid.UUID,
) (bool, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return false, err
	}
	tag, err := db.Write().ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return false, wrap("store: delete push subscription", err)
	}
	n, err := tag.RowsAffected()
	return n > 0, wrap("store: delete push subscription", err)
}

// DeletePushSubscriptionByEndpoint drops a subscription the push service says
// is gone (uninstalled browser, revoked permission).
func (s *Store) DeletePushSubscriptionByEndpoint(
	ctx context.Context, spaceID SpaceID, endpoint string,
) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return wrap("store: delete push subscription", err)
}

func (s *Store) TouchPushSubscription(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`UPDATE push_subscriptions SET last_used_at = ? WHERE id = ?`, db.Now(), id)
	return wrap("store: touch push subscription", err)
}
