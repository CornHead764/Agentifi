package store

import (
	"context"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The notification feed. `uq_notification_dedupe` on (user_id, dedupe_key)
// makes "fires once" a guarantee. It is per person, not per space, so the key
// must name the space, or one person in two households is told once about a
// thing that happened in both.
//
// A standing state (a balance below its line, an unreadable mailbox) also
// carries `condition_key`; uq_notification_open_condition allows one
// unresolved row per state per person. Resolving the row when the state ends
// lets its next occurrence be news again.
//
// Clearing a notification stamps `cleared_at` and never deletes the row: the
// row is the dedupe record, and a deleted one would be sent again.

type Notification struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	AlertType domain.AlertType
	Title     string
	Body      string
	URL       string
	DedupeKey string
	// ConditionKey is the state this reports, empty for an event. It must name
	// the space too.
	ConditionKey string
	ReadAt       *time.Time
	CreatedAt    time.Time
}

type NotificationQuery struct {
	UnreadOnly bool
	// Limit caps the page; zero means the default.
	Limit int
}

const notificationColumns = `id, user_id, alert_type, title, body, url, dedupe_key,
	read_at, created_at`

// ListNotifications returns one person's feed, newest first.
func (s *Store) ListNotifications(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID, q NotificationQuery,
) ([]Notification, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return queryAll(ctx, s.db, "store: list notifications", scanNotification,
		`SELECT `+notificationColumns+` FROM notifications
		  WHERE space_id = $1 AND user_id = $2 AND resolved_at IS NULL AND cleared_at IS NULL
		    AND ($3 = false OR read_at IS NULL)
		  ORDER BY created_at DESC, id DESC
		  LIMIT $4`,
		spaceID.UUID(), userID, q.UnreadOnly, limit)
}

func scanNotification(row scanner) (Notification, error) {
	var (
		one       Notification
		alertType string
		url       *string
	)
	if err := row.Scan(&one.ID, &one.UserID, &alertType, &one.Title, &one.Body, &url,
		&one.DedupeKey, &one.ReadAt, &one.CreatedAt); err != nil {
		return Notification{}, err
	}
	one.AlertType = domain.AlertType(alertType)
	one.URL = Deref(url)
	return one, nil
}

// UnreadNotificationCount is what the bell shows.
func (s *Store) UnreadNotificationCount(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID,
) (int, error) {
	var count int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM notifications
		  WHERE space_id = $1 AND user_id = $2 AND read_at IS NULL AND resolved_at IS NULL
		    AND cleared_at IS NULL`,
		spaceID.UUID(), userID).Scan(&count)
	return count, wrap("store: unread notification count", err)
}

// RecordNotification writes one and reports whether it is new. False is the
// dedupe working, not an error; for a condition, an unresolved row for the
// same state already exists.
func (s *Store) RecordNotification(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID, one *Notification,
) (bool, error) {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	tag, err := s.db.Exec(ctx,
		`INSERT INTO notifications
		     (id, space_id, user_id, alert_type, title, body, url, dedupe_key, condition_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT DO NOTHING`,
		one.ID, spaceID.UUID(), userID, string(one.AlertType),
		one.Title, one.Body, pgconv.NullText(one.URL), one.DedupeKey,
		pgconv.NullText(one.ConditionKey))
	if err != nil {
		return false, wrap("store: record notification", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkNotificationRead marks one, reporting whether there was one to mark.
func (s *Store) MarkNotificationRead(
	ctx context.Context, spaceID SpaceID, userID, id uuid.UUID,
) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE notifications SET read_at = now(), updated_at = now()
		  WHERE space_id = $1 AND user_id = $2 AND id = $3 AND read_at IS NULL`,
		spaceID.UUID(), userID, id)
	if err != nil {
		return false, wrap("store: mark notification read", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkAllNotificationsRead clears the bell.
func (s *Store) MarkAllNotificationsRead(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID,
) (int, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE notifications SET read_at = now(), updated_at = now()
		  WHERE space_id = $1 AND user_id = $2 AND read_at IS NULL`,
		spaceID.UUID(), userID)
	if err != nil {
		return 0, wrap("store: mark all notifications read", err)
	}
	return int(tag.RowsAffected()), nil
}

// ClearNotification takes one off the feed, reporting whether there was one
// to clear. A cleared notification counts as read.
func (s *Store) ClearNotification(
	ctx context.Context, spaceID SpaceID, userID, id uuid.UUID,
) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE notifications
		    SET cleared_at = now(), read_at = coalesce(read_at, now()), updated_at = now()
		  WHERE space_id = $1 AND user_id = $2 AND id = $3 AND cleared_at IS NULL`,
		spaceID.UUID(), userID, id)
	if err != nil {
		return false, wrap("store: clear notification", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ClearAllNotifications empties one person's feed in one space.
func (s *Store) ClearAllNotifications(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID,
) (int, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE notifications
		    SET cleared_at = now(), read_at = coalesce(read_at, now()), updated_at = now()
		  WHERE space_id = $1 AND user_id = $2 AND cleared_at IS NULL`,
		spaceID.UUID(), userID)
	if err != nil {
		return 0, wrap("store: clear all notifications", err)
	}
	return int(tag.RowsAffected()), nil
}

// ResolveConditionsExcept ends every open condition of these alert types for
// one person except those still holding, and reports how many. The sweep
// decides afresh which states hold, so one it no longer finds has ended.
func (s *Store) ResolveConditionsExcept(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID,
	types []domain.AlertType, holding []string,
) (int, error) {
	names := make([]string, len(types))
	for i, one := range types {
		names[i] = string(one)
	}
	if holding == nil {
		holding = []string{}
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE notifications SET resolved_at = now(), updated_at = now()
		  WHERE space_id = $1 AND user_id = $2 AND alert_type = ANY($3)
		    AND condition_key IS NOT NULL AND resolved_at IS NULL
		    AND NOT (condition_key = ANY($4))`,
		spaceID.UUID(), userID, names, holding)
	if err != nil {
		return 0, wrap("store: resolve conditions", err)
	}
	return int(tag.RowsAffected()), nil
}

// ResolveConditions ends every open condition in a space, for everybody, whose
// key starts with prefix. A connector's trouble can be several states, and one
// successful read ends them all.
func (s *Store) ResolveConditions(ctx context.Context, spaceID SpaceID, prefix string) (int, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE notifications SET resolved_at = now(), updated_at = now()
		  WHERE space_id = $1 AND resolved_at IS NULL
		    AND left(condition_key, length($2)) = $2`,
		spaceID.UUID(), prefix)
	if err != nil {
		return 0, wrap("store: resolve conditions", err)
	}
	return int(tag.RowsAffected()), nil
}
