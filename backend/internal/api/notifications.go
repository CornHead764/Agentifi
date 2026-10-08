package api

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Notification settings. Every response is the whole alert catalog, each alert
// carrying this person's choice or the catalog default, so the defaults live
// in one place and not also in the client.
//
// Settings are per person, not per space: every query is keyed on the user as
// well as the space. The read and clear methods are READ, not WRITE: they are
// keyed on the caller's own user id, so a viewer touches nothing the household
// shares.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewNotificationServiceHandler(notificationService{env}, opts...)
	})
}

type notificationService struct{ env *Env }

func (s notificationService) ListNotifications(
	ctx context.Context, req *agentifiv1.ListNotificationsRequest,
) (*agentifiv1.ListNotificationsResponse, error) {
	sp := spaceFrom(ctx)
	rows, err := s.env.DB.ListNotifications(ctx, sp.ID(), sp.UserID(), store.NotificationQuery{
		UnreadOnly: req.GetUnread(),
	})
	if err != nil {
		return nil, err
	}
	unread, err := s.env.DB.UnreadNotificationCount(ctx, sp.ID(), sp.UserID())
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListNotificationsResponse{
		Notifications: make([]*agentifiv1.Notification, 0, len(rows)),
		Unread:        int32(unread),
	}
	for _, one := range rows {
		out.Notifications = append(out.Notifications, notificationProto(one))
	}
	return out, nil
}

func (s notificationService) MarkNotificationRead(
	ctx context.Context, req *agentifiv1.MarkNotificationReadRequest,
) (*agentifiv1.MarkNotificationReadResponse, error) {
	sp := spaceFrom(ctx)
	id, err := idFrom(req.GetNotificationId(), "Notification")
	if err != nil {
		return nil, err
	}
	marked, err := s.env.DB.MarkNotificationRead(ctx, sp.ID(), sp.UserID(), id)
	if err != nil {
		return nil, err
	}
	if !marked {
		// Already read, or somebody else's. Both are a 404 here: there is
		// nothing this person can do about either.
		return nil, errNotFound("Notification")
	}
	return &agentifiv1.MarkNotificationReadResponse{}, nil
}

func (s notificationService) MarkAllNotificationsRead(
	ctx context.Context, _ *agentifiv1.MarkAllNotificationsReadRequest,
) (*agentifiv1.MarkAllNotificationsReadResponse, error) {
	sp := spaceFrom(ctx)
	if _, err := s.env.DB.MarkAllNotificationsRead(ctx, sp.ID(), sp.UserID()); err != nil {
		return nil, err
	}
	return &agentifiv1.MarkAllNotificationsReadResponse{}, nil
}

func (s notificationService) ClearNotification(
	ctx context.Context, req *agentifiv1.ClearNotificationRequest,
) (*agentifiv1.ClearNotificationResponse, error) {
	sp := spaceFrom(ctx)
	id, err := idFrom(req.GetNotificationId(), "Notification")
	if err != nil {
		return nil, err
	}
	cleared, err := s.env.DB.ClearNotification(ctx, sp.ID(), sp.UserID(), id)
	if err != nil {
		return nil, err
	}
	if !cleared {
		return nil, errNotFound("Notification")
	}
	return &agentifiv1.ClearNotificationResponse{}, nil
}

func (s notificationService) ClearAllNotifications(
	ctx context.Context, _ *agentifiv1.ClearAllNotificationsRequest,
) (*agentifiv1.ClearAllNotificationsResponse, error) {
	sp := spaceFrom(ctx)
	if _, err := s.env.DB.ClearAllNotifications(ctx, sp.ID(), sp.UserID()); err != nil {
		return nil, err
	}
	return &agentifiv1.ClearAllNotificationsResponse{}, nil
}

// newAlerts builds the sweep with whichever channels this deployment has. The
// mailer is nil when no relay is configured, so email is off rather than an
// error on every alert. The clock is the Env's, so an injected clock moves the
// window the sweep judges "due".
func newAlerts(env *Env) *service.Alerts {
	alerts := service.NewAlerts(env.DB)
	alerts.Now = env.now
	if push := newPusher(env.Cfg); push != nil {
		alerts.Push = push
	}
	if env.Cfg.EmailEnabled() {
		alerts.Mail = &provider.Mailer{
			Host: env.Cfg.SMTPHost, Port: env.Cfg.SMTPPort,
			Username: env.Cfg.SMTPUsername, Password: env.Cfg.SMTPPassword,
			From: env.Cfg.SMTPFrom, StartTLS: env.Cfg.SMTPStartTLS,
		}
	}
	return alerts
}

// newPusher builds the web push channel, or nil when this deployment has no
// VAPID keypair.
func newPusher(cfg *config.Config) *provider.Push {
	if cfg.VAPIDPublicKey == "" || cfg.VAPIDPrivateKey == "" {
		return nil
	}
	return &provider.Push{
		Keys:    provider.VapidKeys{PublicKey: cfg.VAPIDPublicKey, PrivateKey: cfg.VAPIDPrivateKey},
		Subject: cfg.VAPIDSubject,
	}
}

func (s notificationService) ListPushSubscriptions(
	ctx context.Context, _ *agentifiv1.ListPushSubscriptionsRequest,
) (*agentifiv1.ListPushSubscriptionsResponse, error) {
	push, err := pushSubscriptions(ctx, s.env, spaceFrom(ctx))
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ListPushSubscriptionsResponse{Push: push}, nil
}

func (s notificationService) CreatePushSubscription(
	ctx context.Context, req *agentifiv1.CreatePushSubscriptionRequest,
) (*agentifiv1.CreatePushSubscriptionResponse, error) {
	sp := spaceFrom(ctx)
	if strings.TrimSpace(req.GetEndpoint()) == "" ||
		strings.TrimSpace(req.GetP256Dh()) == "" || strings.TrimSpace(req.GetAuth()) == "" {
		return nil, errBadRequest("A push subscription needs an endpoint and both keys")
	}
	// The sender's own check, made here so an undeliverable subscription is
	// refused where somebody can see it.
	if err := provider.AssertEndpointAllowed(ctx, req.GetEndpoint(), nil); err != nil {
		return nil, errBadRequest("%s is not somewhere this server will send to", req.GetEndpoint())
	}

	var userAgent string
	if info, ok := connect.CallInfoForHandlerContext(ctx); ok {
		userAgent = info.RequestHeader().Get("User-Agent")
	}
	one := store.PushSubscription{
		Endpoint: req.GetEndpoint(), P256dh: req.GetP256Dh(), Auth: req.GetAuth(),
		UserAgent: textutil.Clip(textutil.FirstNonBlank(userAgent, "a browser"), 255),
	}
	if err := s.env.DB.SavePushSubscription(ctx, sp.ID(), sp.UserID(), &one); err != nil {
		return nil, err
	}
	push, err := pushSubscriptions(ctx, s.env, sp)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreatePushSubscriptionResponse{Push: push}, nil
}

func (s notificationService) DeletePushSubscription(
	ctx context.Context, req *agentifiv1.DeletePushSubscriptionRequest,
) (*agentifiv1.DeletePushSubscriptionResponse, error) {
	sp := spaceFrom(ctx)
	id, err := idFrom(req.GetSubscriptionId(), "Subscription")
	if err != nil {
		return nil, err
	}
	removed, err := s.env.DB.DeletePushSubscription(ctx, sp.ID(), sp.UserID(), id)
	if err != nil {
		return nil, err
	}
	if !removed {
		return nil, errNotFound("Subscription")
	}
	return &agentifiv1.DeletePushSubscriptionResponse{}, nil
}

// pushSubscriptions is the browsers this person has allowed, and what the
// browser needs to add another. Enabled is false when this deployment has no
// VAPID keypair: the page says so rather than offering a button that cannot
// work.
func pushSubscriptions(
	ctx context.Context, env *Env, sp auth.SpaceContext,
) (*agentifiv1.PushSubscriptions, error) {
	rows, err := env.DB.ListPushSubscriptions(ctx, sp.ID(), sp.UserID())
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.PushSubscriptions{
		Enabled:       env.Cfg.VAPIDPublicKey != "" && env.Cfg.VAPIDPrivateKey != "",
		PublicKey:     env.Cfg.VAPIDPublicKey,
		Subscriptions: make([]*agentifiv1.PushSubscription, 0, len(rows)),
	}
	for _, one := range rows {
		subscription := &agentifiv1.PushSubscription{
			Id: one.ID.String(), UserAgent: one.UserAgent, CreatedAt: timestamppb.New(one.CreatedAt),
		}
		if one.LastUsedAt != nil {
			subscription.LastUsedAt = timestamppb.New(*one.LastUsedAt)
		}
		out.Subscriptions = append(out.Subscriptions, subscription)
	}
	return out, nil
}

func notificationProto(one store.Notification) *agentifiv1.Notification {
	label := string(one.AlertType)
	if definition, ok := domain.AlertByType(one.AlertType); ok {
		label = definition.Label
	}
	out := &agentifiv1.Notification{
		Id:        one.ID.String(),
		AlertType: string(one.AlertType),
		Label:     label,
		Title:     one.Title,
		Body:      one.Body,
		Url:       one.URL,
		CreatedAt: timestamppb.New(one.CreatedAt),
	}
	if one.ReadAt != nil {
		out.ReadAt = timestamppb.New(*one.ReadAt)
	}
	return out
}

func (s notificationService) GetAlertSettings(
	ctx context.Context, _ *agentifiv1.GetAlertSettingsRequest,
) (*agentifiv1.GetAlertSettingsResponse, error) {
	sp := spaceFrom(ctx)
	rules, err := s.env.DB.ListAlertRules(ctx, sp.ID(), sp.UserID())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetAlertSettingsResponse{Settings: s.env.alertSettings(rules)}, nil
}

func (s notificationService) SaveAlertSetting(
	ctx context.Context, req *agentifiv1.SaveAlertSettingRequest,
) (*agentifiv1.SaveAlertSettingResponse, error) {
	sp := spaceFrom(ctx)
	definition, ok := domain.AlertByType(domain.AlertType(strings.TrimSpace(req.GetAlertType())))
	if !ok {
		return nil, errNotFound("Alert")
	}
	rule, err := ruleFrom(definition, req)
	if err != nil {
		return nil, err
	}
	// The pause flag is not in the request: letting a row edit carry it would
	// let one alert quietly un-pause the rest.
	existing, err := s.env.DB.ListAlertRules(ctx, sp.ID(), sp.UserID())
	if err != nil {
		return nil, err
	}
	for _, one := range existing {
		if one.AlertType == definition.Type && one.AccountID == rule.AccountID {
			rule.ID, rule.IsPaused = one.ID, one.IsPaused
			break
		}
	}
	if err := s.env.DB.SaveAlertRule(ctx, sp.ID(), sp.UserID(), &rule); err != nil {
		return nil, err
	}

	rules, err := s.env.DB.ListAlertRules(ctx, sp.ID(), sp.UserID())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.SaveAlertSettingResponse{Settings: s.env.alertSettings(rules)}, nil
}

func (s notificationService) PauseAllAlerts(
	ctx context.Context, req *agentifiv1.PauseAllAlertsRequest,
) (*agentifiv1.PauseAllAlertsResponse, error) {
	sp := spaceFrom(ctx)
	// Pausing writes only rows that exist, so the defaults are materialised
	// first for an alert nobody has touched.
	if req.GetPaused() {
		if err := materialiseAlertDefaults(ctx, s.env, sp); err != nil {
			return nil, err
		}
	}
	if err := s.env.DB.PauseAllAlerts(ctx, sp.ID(), sp.UserID(), req.GetPaused()); err != nil {
		return nil, err
	}
	rules, err := s.env.DB.ListAlertRules(ctx, sp.ID(), sp.UserID())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.PauseAllAlertsResponse{Settings: s.env.alertSettings(rules)}, nil
}

// materialiseAlertDefaults writes the catalog default for every alert this
// person has never touched, so a switch acting on stored rows acts on all.
func materialiseAlertDefaults(ctx context.Context, env *Env, sp auth.SpaceContext) error {
	existing, err := env.DB.ListAlertRules(ctx, sp.ID(), sp.UserID())
	if err != nil {
		return err
	}
	held := make(map[domain.AlertType]bool, len(existing))
	for _, one := range existing {
		if one.AccountID == uuid.Nil {
			held[one.AlertType] = true
		}
	}
	for _, definition := range domain.AlertCatalog {
		if held[definition.Type] {
			continue
		}
		rule := defaultRule(definition)
		if err := env.DB.SaveAlertRule(ctx, sp.ID(), sp.UserID(), &rule); err != nil {
			return err
		}
	}
	return nil
}

// alertSettings folds stored answers into the catalog, in the catalog's order.
func (env *Env) alertSettings(rules []store.AlertRule) *agentifiv1.AlertSettings {
	stored := make(map[domain.AlertType]store.AlertRule, len(rules))
	for _, one := range rules {
		if one.AccountID == uuid.Nil {
			stored[one.AlertType] = one
		}
	}

	out := &agentifiv1.AlertSettings{Alerts: make([]*agentifiv1.AlertSetting, 0, len(domain.AlertCatalog))}
	paused, configured := 0, 0
	for _, definition := range domain.AlertCatalog {
		rule, held := stored[definition.Type]
		if !held {
			rule = defaultRule(definition)
		} else {
			configured++
			if rule.IsPaused {
				paused++
			}
		}
		out.Alerts = append(out.Alerts, alertSetting(definition, rule, held))
	}
	out.AllPaused = configured > 0 && paused == configured
	out.EmailEnabled = env.Cfg.EmailEnabled()
	// Web push also needs a secure context in the browser; this says only that
	// the server could sign a message.
	out.PushEnabled = env.Cfg.VAPIDPublicKey != "" && env.Cfg.VAPIDPrivateKey != ""
	return out
}

func alertSetting(
	definition domain.AlertDefinition, rule store.AlertRule, configured bool,
) *agentifiv1.AlertSetting {
	channels := make([]string, 0, len(definition.Channels))
	for _, one := range definition.Channels {
		channels = append(channels, string(one))
	}
	out := &agentifiv1.AlertSetting{
		Type:      string(definition.Type),
		Label:     definition.Label,
		Trigger:   definition.Trigger,
		Group:     string(definition.Group),
		Threshold: string(definition.Threshold),
		Channels:  channels,
		Evaluated: definition.Evaluated,

		IsEnabled:    rule.IsEnabled,
		IsPaused:     rule.IsPaused,
		ChannelEmail: rule.ChannelEmail,
		ChannelPush:  rule.ChannelPush,
		ChannelInApp: rule.ChannelInApp,

		ThresholdAmount:  nullableMoneyProto(rule.ThresholdAmount, rule.HasThresholdAmount),
		ThresholdPercent: rateProto(rule.ThresholdPercent, rule.HasThresholdPercent),
		Configured:       configured,
	}
	if rule.HasThresholdCount {
		count := int32(rule.ThresholdCount)
		out.ThresholdCount = &count
	}
	return out
}

// defaultRule is the catalog's own answer, as a rule.
func defaultRule(definition domain.AlertDefinition) store.AlertRule {
	// An alert off by default has every channel off too, or the settings page
	// would show it as on.
	on := definition.DefaultEnabled()
	rule := store.AlertRule{
		AlertType:    definition.Type,
		IsEnabled:    on,
		ChannelEmail: on && definition.DefaultsOn(domain.ChannelEmail),
		ChannelPush:  on && definition.DefaultsOn(domain.ChannelPush),
		ChannelInApp: on && definition.DefaultsOn(domain.ChannelInApp),
	}
	switch definition.Threshold {
	case domain.ThresholdAmount:
		rule.ThresholdAmount, rule.HasThresholdAmount = definition.DefaultAmount, true
	case domain.ThresholdCount:
		rule.ThresholdCount, rule.HasThresholdCount = definition.DefaultCount, true
	case domain.ThresholdPercent:
		rule.ThresholdPercent = decimal.NewFromInt(int64(definition.DefaultPercent))
		rule.HasThresholdPercent = true
	}
	return rule
}

// ruleFrom validates an edit against the catalog. A channel the alert cannot
// use is refused rather than stored, and so is a threshold on an alert that
// has none or a missing one where it is needed: either rule could never fire.
func ruleFrom(
	definition domain.AlertDefinition, req *agentifiv1.SaveAlertSettingRequest,
) (store.AlertRule, error) {
	rule := store.AlertRule{
		AlertType:    definition.Type,
		IsEnabled:    req.GetIsEnabled(),
		ChannelEmail: req.GetChannelEmail(),
		ChannelPush:  req.GetChannelPush(),
		ChannelInApp: req.GetChannelInApp(),
	}
	for channel, on := range map[domain.AlertChannel]bool{
		domain.ChannelEmail: req.GetChannelEmail(),
		domain.ChannelPush:  req.GetChannelPush(),
		domain.ChannelInApp: req.GetChannelInApp(),
	} {
		if on && !definition.Allows(channel) {
			return store.AlertRule{}, errBadRequest(
				"%s cannot be sent by %s", definition.Label, channel)
		}
	}

	switch definition.Threshold {
	case domain.ThresholdAmount:
		if req.ThresholdAmount == nil {
			return store.AlertRule{}, errBadRequest("%s needs an amount", definition.Label)
		}
		amount, err := moneyFrom(req.ThresholdAmount, "body", "threshold_amount")
		if err != nil {
			return store.AlertRule{}, err
		}
		if amount.IsNegative() {
			return store.AlertRule{}, errBadRequest("An amount cannot be negative")
		}
		rule.ThresholdAmount, rule.HasThresholdAmount = amount, true
	case domain.ThresholdCount:
		if req.ThresholdCount == nil {
			return store.AlertRule{}, errBadRequest("%s needs a count", definition.Label)
		}
		if req.GetThresholdCount() < 1 {
			return store.AlertRule{}, errBadRequest("A count has to be at least one")
		}
		rule.ThresholdCount, rule.HasThresholdCount = int(req.GetThresholdCount()), true
	case domain.ThresholdPercent:
		if req.ThresholdPercent == nil {
			return store.AlertRule{}, errBadRequest("%s needs a percentage", definition.Label)
		}
		percent, err := decimal.NewFromString(req.GetThresholdPercent())
		if err != nil {
			return store.AlertRule{}, errInvalid("decimal_parsing", []string{"body", "threshold_percent"},
				"%q is not a number", req.GetThresholdPercent())
		}
		if percent.IsNegative() {
			return store.AlertRule{}, errBadRequest("A percentage cannot be negative")
		}
		rule.ThresholdPercent, rule.HasThresholdPercent = percent, true
	case domain.ThresholdNone:
		if req.ThresholdAmount != nil || req.ThresholdCount != nil || req.ThresholdPercent != nil {
			return store.AlertRule{}, errBadRequest(
				"%s has nothing to threshold on", definition.Label)
		}
	}
	return rule, nil
}
