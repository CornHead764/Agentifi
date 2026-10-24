package store

import (
	"context"
	"database/sql"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/google/uuid"
	"time"
)

// Notifications are household-local. Clearing preserves their dedupe records;
// resolving a standing condition permits its next occurrence to become news.
type Notification struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	AlertType    domain.AlertType
	Title        string
	Body         string
	URL          string
	DedupeKey    string
	ConditionKey string
	ReadAt       *time.Time
	CreatedAt    time.Time
}
type NotificationQuery struct {
	UnreadOnly bool
	Limit      int
}

const notificationColumns = `id, user_id, alert_type, title, body, url, dedupe_key, read_at, created_at`

func scanNotification(row scanner) (Notification, error) {
	var one Notification
	var url sql.Null[string]
	var read sql.Null[int64]
	var created int64
	if err := row.Scan(&one.ID, &one.UserID, &one.AlertType, &one.Title, &one.Body, &url, &one.DedupeKey, &read, &created); err != nil {
		return one, err
	}
	one.URL, one.ReadAt, one.CreatedAt = url.V, nullableTime(read), fromUnixMillis(created)
	return one, nil
}
func (s *Store) ListNotifications(ctx context.Context, spaceID SpaceID, userID uuid.UUID, q NotificationQuery) ([]Notification, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list notifications", scanNotification,
		`SELECT `+notificationColumns+` FROM notifications WHERE user_id = ? AND resolved_at IS NULL AND cleared_at IS NULL AND (? = false OR read_at IS NULL) ORDER BY created_at DESC, id DESC LIMIT ?`, userID, q.UnreadOnly, limit)
}
func (s *Store) UnreadNotificationCount(ctx context.Context, spaceID SpaceID, userID uuid.UUID) (int, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return 0, err
	}
	var count int
	err = db.Read().QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE user_id = ? AND read_at IS NULL AND resolved_at IS NULL AND cleared_at IS NULL`, userID).Scan(&count)
	return count, wrap("store: unread notification count", err)
}
func (s *Store) RecordNotification(ctx context.Context, spaceID SpaceID, userID uuid.UUID, one *Notification) (bool, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return false, err
	}
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	now := db.Now()
	tag, err := db.Write().ExecContext(ctx, `INSERT INTO notifications (id,user_id,alert_type,title,body,url,dedupe_key,condition_key,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, one.ID, userID, string(one.AlertType), one.Title, one.Body, PtrIf(one.URL, one.URL != ""), one.DedupeKey, PtrIf(one.ConditionKey, one.ConditionKey != ""), now, now)
	if err != nil {
		return false, wrap("store: record notification", err)
	}
	n, err := tag.RowsAffected()
	return n > 0, wrap("store: record notification", err)
}
func (s *Store) MarkNotificationRead(ctx context.Context, spaceID SpaceID, userID, id uuid.UUID) (bool, error) {
	n, err := s.notificationUpdate(ctx, spaceID, "store: mark notification read", `read_at = ?, updated_at = ?`, `user_id = ? AND id = ? AND read_at IS NULL`, userID, id)
	return n > 0, err
}
func (s *Store) MarkAllNotificationsRead(ctx context.Context, spaceID SpaceID, userID uuid.UUID) (int, error) {
	return s.notificationUpdate(ctx, spaceID, "store: mark all notifications read", `read_at = ?, updated_at = ?`, `user_id = ? AND read_at IS NULL`, userID)
}
func (s *Store) ClearNotification(ctx context.Context, spaceID SpaceID, userID, id uuid.UUID) (bool, error) {
	n, err := s.notificationUpdate(ctx, spaceID, "store: clear notification", `cleared_at = ?, read_at = coalesce(read_at, ?), updated_at = ?`, `user_id = ? AND id = ? AND cleared_at IS NULL`, userID, id)
	return n > 0, err
}
func (s *Store) ClearAllNotifications(ctx context.Context, spaceID SpaceID, userID uuid.UUID) (int, error) {
	return s.notificationUpdate(ctx, spaceID, "store: clear all notifications", `cleared_at = ?, read_at = coalesce(read_at, ?), updated_at = ?`, `user_id = ? AND cleared_at IS NULL`, userID)
}
func (s *Store) ResolveConditionsExcept(ctx context.Context, spaceID SpaceID, userID uuid.UUID, types []domain.AlertType, holding []string) (int, error) {
	placeholders, args := inArgs(types)
	where := `user_id = ? AND alert_type IN ` + placeholders + ` AND condition_key IS NOT NULL AND resolved_at IS NULL`
	values := append([]any{userID}, args...)
	if len(holding) > 0 {
		placeholders, args = inArgs(holding)
		where += ` AND condition_key NOT IN ` + placeholders
		values = append(values, args...)
	}
	return s.notificationUpdate(ctx, spaceID, "store: resolve conditions", `resolved_at = ?, updated_at = ?`, where, values...)
}
func (s *Store) ResolveConditions(ctx context.Context, spaceID SpaceID, prefix string) (int, error) {
	return s.notificationUpdate(ctx, spaceID, "store: resolve conditions", `resolved_at = ?, updated_at = ?`, `resolved_at IS NULL AND substr(condition_key, 1, length(?)) = ?`, prefix, prefix)
}

// All assignments are private fixed SQL fragments; each timestamp receives the
// same injected clock value, including the optional clear/read coalesce stamp.
func (s *Store) notificationUpdate(ctx context.Context, spaceID SpaceID, what, assignments, predicate string, values ...any) (int, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return 0, err
	}
	now := db.Now()
	args := []any{now, now}
	if assignments == `cleared_at = ?, read_at = coalesce(read_at, ?), updated_at = ?` {
		args = append(args, now)
	}
	args = append(args, values...)
	tag, err := db.Write().ExecContext(ctx, `UPDATE notifications SET `+assignments+` WHERE `+predicate, args...)
	if err != nil {
		return 0, wrap(what, err)
	}
	n, err := tag.RowsAffected()
	return int(n), wrap(what, err)
}
