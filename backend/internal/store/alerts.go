package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/decimaltext"
	"github.com/CornHead764/agentifi/backend/internal/domain"
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
	db, currency, err := s.householdMoneyDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list alert rules",
		func(row scanner) (AlertRule, error) { return scanAlertRule(row, currency) },
		`SELECT `+alertRuleColumns+` FROM alert_rules WHERE user_id = ?`, userID)
}

// SaveAlertRule upserts one person's settings for one alert.
func (s *Store) SaveAlertRule(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID, rule *AlertRule,
) error {
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	db, currency, err := s.householdMoneyDB(ctx, spaceID)
	if err != nil {
		return err
	}
	var amount, percent any
	if rule.HasThresholdAmount {
		amount, err = toMinor(rule.ThresholdAmount.Decimal(), currency)
		if err != nil {
			return err
		}
	}
	if rule.HasThresholdPercent {
		percent = decimaltext.Format(rule.ThresholdPercent)
	}
	conflict := `(user_id, alert_type, account_id) WHERE account_id IS NOT NULL`
	if rule.AccountID == uuid.Nil {
		conflict = `(user_id, alert_type) WHERE account_id IS NULL`
	}
	now := db.Now()
	err = db.Write().QueryRowContext(ctx,
		`INSERT INTO alert_rules
		     (id, user_id, alert_type, account_id, is_enabled, is_paused,
		      channel_email, channel_push, channel_in_app,
		      threshold_amount, threshold_count, threshold_pct, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT `+conflict+` DO UPDATE
		     SET is_enabled = EXCLUDED.is_enabled, is_paused = EXCLUDED.is_paused,
		         channel_email = EXCLUDED.channel_email, channel_push = EXCLUDED.channel_push,
		         channel_in_app = EXCLUDED.channel_in_app, threshold_amount = EXCLUDED.threshold_amount,
		         threshold_count = EXCLUDED.threshold_count, threshold_pct = EXCLUDED.threshold_pct,
		         updated_at = EXCLUDED.updated_at RETURNING id`,
		rule.ID, userID, string(rule.AlertType), NullUUID(rule.AccountID),
		rule.IsEnabled, rule.IsPaused, rule.ChannelEmail, rule.ChannelPush, rule.ChannelInApp,
		amount, PtrIf(rule.ThresholdCount, rule.HasThresholdCount), percent, now, now).Scan(&rule.ID)
	return wrap("store: save alert rule", err)
}

// PauseAllAlerts writes only rows that already exist: an untouched alert has
// no row, and defaults are read through the pause flag anyway, so rows are not
// materialised for decisions nobody made.
func (s *Store) PauseAllAlerts(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID, paused bool,
) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`UPDATE alert_rules SET is_paused = ?, updated_at = ? WHERE user_id = ?`,
		paused, db.Now(), userID)
	return wrap("store: pause all alerts", err)
}

func scanAlertRule(row scanner, currency string) (AlertRule, error) {
	var rule AlertRule
	var accountID uuid.NullUUID
	var amount sql.Null[int64]
	var percent sql.Null[string]
	var count sql.Null[int]
	err := row.Scan(&rule.ID, &rule.UserID, &rule.AlertType, &accountID,
		&rule.IsEnabled, &rule.IsPaused, &rule.ChannelEmail, &rule.ChannelPush,
		&rule.ChannelInApp, &amount, &count, &percent)
	if err != nil {
		return AlertRule{}, err
	}
	rule.AccountID = accountID.UUID
	if amount.Valid {
		converted, err := fromMinor(amount.V, currency)
		if err != nil {
			return AlertRule{}, err
		}
		rule.ThresholdAmount, rule.HasThresholdAmount = domain.FromDecimal(converted), true
	}
	if percent.Valid {
		var err error
		rule.ThresholdPercent, err = decimaltext.Parse(percent.V)
		if err != nil {
			return AlertRule{}, err
		}
		rule.HasThresholdPercent = true
	}
	rule.ThresholdCount, rule.HasThresholdCount = count.V, count.Valid
	return rule, nil
}
