package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
)

// A person's choices about the alert catalog (the catalog itself is domain).
// Every query is keyed on the user as well as the space: people sharing a
// space do not share alert settings.

type AlertRule struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	AlertType domain.AlertType
	// AccountID narrows a rule to one account; Nil for the space-wide rule the
	// settings page edits.
	AccountID uuid.UUID

	IsEnabled bool
	// IsPaused is "pause all", kept apart from IsEnabled so lifting the pause
	// restores what somebody chose.
	IsPaused     bool
	ChannelEmail bool
	ChannelPush  bool
	ChannelInApp bool

	ThresholdAmount    domain.Money
	HasThresholdAmount bool
	ThresholdCount     int
	HasThresholdCount  bool
	// ThresholdPercent is percent units, not a fraction: 10 means 10%.
	ThresholdPercent    domain.Rate
	HasThresholdPercent bool
}

const alertRuleColumns = `id, user_id, alert_type, account_id, is_enabled, is_paused,
	channel_email, channel_push, channel_in_app,
	threshold_amount, threshold_count, threshold_pct`

// ListAlertRules returns this person's rules unordered; the catalog decides
// display order.
func (s *Store) ListAlertRules(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID,
) ([]AlertRule, error) {
	return queryAll(ctx, s.db, "store: list alert rules", scanAlertRule,
		`SELECT `+alertRuleColumns+` FROM alert_rules WHERE space_id = $1 AND user_id = $2`,
		spaceID.UUID(), userID)
}

// SaveAlertRule upserts one person's settings for one alert.
func (s *Store) SaveAlertRule(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID, rule *AlertRule,
) error {
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	err := s.db.QueryRow(ctx,
		`INSERT INTO alert_rules
		     (id, space_id, user_id, alert_type, account_id, is_enabled, is_paused,
		      channel_email, channel_push, channel_in_app,
		      threshold_amount, threshold_count, threshold_pct)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 ON CONFLICT (space_id, user_id, alert_type, account_id) DO UPDATE
		     SET is_enabled = EXCLUDED.is_enabled,
		         is_paused = EXCLUDED.is_paused,
		         channel_email = EXCLUDED.channel_email,
		         channel_push = EXCLUDED.channel_push,
		         channel_in_app = EXCLUDED.channel_in_app,
		         threshold_amount = EXCLUDED.threshold_amount,
		         threshold_count = EXCLUDED.threshold_count,
		         threshold_pct = EXCLUDED.threshold_pct,
		         updated_at = now()
		 RETURNING id`,
		rule.ID, spaceID.UUID(), userID, string(rule.AlertType), pgconv.NullUUID(rule.AccountID),
		rule.IsEnabled, rule.IsPaused,
		rule.ChannelEmail, rule.ChannelPush, rule.ChannelInApp,
		pgconv.NullMoney(rule.ThresholdAmount, rule.HasThresholdAmount),
		PtrIf(rule.ThresholdCount, rule.HasThresholdCount),
		pgconv.NullNumeric(rule.ThresholdPercent, rule.HasThresholdPercent),
	).Scan(&rule.ID)
	return wrap("store: save alert rule", err)
}

// PauseAllAlerts writes only rows that already exist: an untouched alert has
// no row, and defaults are read through the pause flag anyway, so rows are not
// materialised for decisions nobody made.
func (s *Store) PauseAllAlerts(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID, paused bool,
) error {
	_, err := s.db.Exec(ctx,
		`UPDATE alert_rules SET is_paused = $3, updated_at = now()
		  WHERE space_id = $1 AND user_id = $2`,
		spaceID.UUID(), userID, paused)
	return wrap("store: pause all alerts", err)
}

func scanAlertRule(row scanner) (AlertRule, error) {
	var (
		rule      AlertRule
		alertType string
		accountID *uuid.UUID
		amount    pgtype.Numeric
		count     *int
		percent   pgtype.Numeric
	)
	err := row.Scan(&rule.ID, &rule.UserID, &alertType, &accountID,
		&rule.IsEnabled, &rule.IsPaused,
		&rule.ChannelEmail, &rule.ChannelPush, &rule.ChannelInApp,
		&amount, &count, &percent)
	if err != nil {
		return AlertRule{}, err
	}
	rule.AlertType = domain.AlertType(alertType)
	rule.AccountID = Deref(accountID)
	if rule.ThresholdAmount, rule.HasThresholdAmount, err =
		pgconv.ReadNullMoney(amount, "alert_rules.threshold_amount"); err != nil {
		return AlertRule{}, err
	}
	if rule.ThresholdPercent, rule.HasThresholdPercent, err =
		pgconv.ReadNullDecimal(percent, "alert_rules.threshold_pct"); err != nil {
		return AlertRule{}, err
	}
	if count != nil {
		rule.ThresholdCount, rule.HasThresholdCount = *count, true
	}
	return rule, nil
}
