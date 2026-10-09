package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
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
// well as the space.

func init() {
	Register(Resource{Prefix: "/notifications", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listNotifications)
		// Read routes, not Write: these are keyed on the caller's own user id,
		// so a viewer touches nothing the household shares.
		rt.Read(http.MethodPost, "/read", markAllNotificationsRead)
		rt.Read(http.MethodPost, "/{notification_id}/read", markNotificationRead)
		rt.Read(http.MethodDelete, "/", clearAllNotifications)
		rt.Read(http.MethodDelete, "/{notification_id}", clearNotification)
		rt.Read(http.MethodGet, "/settings", listAlertSettings)
		rt.Write(http.MethodPut, "/settings/{alert_type}", saveAlertSetting)
		rt.Write(http.MethodPost, "/settings/pause", pauseAllAlerts)
		rt.Read(http.MethodGet, "/push", listPushSubscriptions)
		rt.Write(http.MethodPost, "/push", subscribeToPush)
		rt.Write(http.MethodDelete, "/push/{subscription_id}", unsubscribeFromPush)
		rt.Write(http.MethodPost, "/push/test", sendTestPush)
	}})
}

// NotificationResponse is one card in the feed.
type NotificationResponse struct {
	ID        uuid.UUID `json:"id"`
	AlertType string    `json:"alert_type"`
	// Label is the catalog's name for the alert, so a card can say which kind
	// of thing it is without the client keeping a second copy of the catalog.
	Label     string     `json:"label"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	URL       string     `json:"url"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// NotificationFeedResponse is the panel behind the bell.
type NotificationFeedResponse struct {
	Notifications []NotificationResponse `json:"notifications"`
	// Unread is the count for the badge, which is the whole unread count and
	// not the length of this page.
	Unread int `json:"unread"`
}

// PushSubscriptionsResponse is the browsers this person has allowed, and what
// the browser needs to add another.
type PushSubscriptionsResponse struct {
	// PublicKey is the VAPID application key the browser subscribes with. It
	// is public by design — the private half never leaves the server.
	PublicKey     string                     `json:"public_key"`
	Subscriptions []PushSubscriptionResponse `json:"subscriptions"`
}

type PushSubscriptionResponse struct {
	ID        uuid.UUID  `json:"id"`
	UserAgent string     `json:"user_agent"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsed  *time.Time `json:"last_used_at"`
}

// PushSubscribeRequest is what the browser's PushManager hands back.
type PushSubscribeRequest struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// AlertSettingResponse is one catalog entry with this person's answer folded in.
type AlertSettingResponse struct {
	Type    string `json:"type"`
	Label   string `json:"label"`
	Trigger string `json:"trigger"`
	Group   string `json:"group"`

	// Threshold names which of the three threshold fields means anything for
	// this alert: "none", "amount", "count" or "percent".
	Threshold string `json:"threshold"`
	// Channels are the ones this alert can use at all. A client must not draw
	// a toggle outside this list: the credit-score alerts arrive by email only.
	Channels []string `json:"channels"`
	// Evaluated is false while nothing yet decides that this alert has fired.
	// Sent so the page can say so rather than implying a feed that is silent.
	Evaluated bool `json:"evaluated"`

	IsEnabled    bool `json:"is_enabled"`
	IsPaused     bool `json:"is_paused"`
	ChannelEmail bool `json:"channel_email"`
	ChannelPush  bool `json:"channel_push"`
	ChannelInApp bool `json:"channel_in_app"`

	ThresholdAmount  *domain.Money `json:"threshold_amount"`
	ThresholdCount   *int          `json:"threshold_count"`
	ThresholdPercent *domain.Rate  `json:"threshold_percent"`

	// Configured is false while this row is still the catalog's default, so
	// the page can show that nothing has been changed yet.
	Configured bool `json:"configured"`
}

// AlertSettingsResponse is the whole catalog plus the one switch above it.
type AlertSettingsResponse struct {
	Alerts []AlertSettingResponse `json:"alerts"`
	// EmailEnabled says whether this deployment has a relay, so the page does
	// not offer a switch that delivers nothing.
	EmailEnabled bool `json:"email_enabled"`
	// AllPaused is true only when every rule this person has is paused and
	// there is at least one. Derived, not stored.
	AllPaused bool `json:"all_paused"`
}

// AlertSettingUpdate is a whole row, not a patch: a partial update of three
// independent channel booleans races two toggles clicked in either order.
type AlertSettingUpdate struct {
	IsEnabled        bool          `json:"is_enabled"`
	ChannelEmail     bool          `json:"channel_email"`
	ChannelPush      bool          `json:"channel_push"`
	ChannelInApp     bool          `json:"channel_in_app"`
	ThresholdAmount  *domain.Money `json:"threshold_amount"`
	ThresholdCount   *int          `json:"threshold_count"`
	ThresholdPercent *domain.Rate  `json:"threshold_percent"`
}

// PauseAllRequest is the switch at the top of the page.
type PauseAllRequest struct {
	Paused bool `json:"paused"`
}

func listNotifications(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	unreadOnly, _, err := queryBool(r, "unread")
	if err != nil {
		return err
	}
	rows, err := env.DB.ListNotifications(r.Context(), sp.ID(), sp.UserID(), store.NotificationQuery{
		UnreadOnly: unreadOnly,
	})
	if err != nil {
		return err
	}
	unread, err := env.DB.UnreadNotificationCount(r.Context(), sp.ID(), sp.UserID())
	if err != nil {
		return err
	}
	out := make([]NotificationResponse, 0, len(rows))
	for _, one := range rows {
		out = append(out, notificationResponse(one))
	}
	return writeJSON(w, http.StatusOK, NotificationFeedResponse{Notifications: out, Unread: unread})
}

func markNotificationRead(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "notification_id", "Notification")
	if err != nil {
		return err
	}
	marked, err := env.DB.MarkNotificationRead(r.Context(), sp.ID(), sp.UserID(), id)
	if err != nil {
		return err
	}
	if !marked {
		// Already read, or somebody else's. Both are a 404 here: there is
		// nothing this person can do about either.
		return errNotFound("Notification")
	}
	return writeNoContent(w)
}

func markAllNotificationsRead(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if _, err := env.DB.MarkAllNotificationsRead(r.Context(), sp.ID(), sp.UserID()); err != nil {
		return err
	}
	return writeNoContent(w)
}

func clearNotification(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "notification_id", "Notification")
	if err != nil {
		return err
	}
	cleared, err := env.DB.ClearNotification(r.Context(), sp.ID(), sp.UserID(), id)
	if err != nil {
		return err
	}
	if !cleared {
		return errNotFound("Notification")
	}
	return writeNoContent(w)
}

func clearAllNotifications(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if _, err := env.DB.ClearAllNotifications(r.Context(), sp.ID(), sp.UserID()); err != nil {
		return err
	}
	return writeNoContent(w)
}

// newAlerts builds the sweep with whichever channels this deployment has. The
// mailer is nil when no relay is configured, so email is off rather than an
// error on every alert. The clock is the Env's, so an injected clock moves the
// window the sweep judges "due".
func newAlerts(env *Env) *service.Alerts {
	alerts := service.NewAlerts(env.DB)
	alerts.Now = env.now
	alerts.Push = envPusher{env}
	if env.Cfg.EmailEnabled() {
		alerts.Mail = &provider.Mailer{
			Host: env.Cfg.SMTPHost, Port: env.Cfg.SMTPPort,
			Username: env.Cfg.SMTPUsername, Password: env.Cfg.SMTPPassword,
			From: env.Cfg.SMTPFrom, StartTLS: env.Cfg.SMTPStartTLS,
		}
	}
	return alerts
}

// envPusher is the web push channel. Its keypair is resolved on the first
// send, so building an Alerts never touches the database.
type envPusher struct{ env *Env }

func (p envPusher) Send(ctx context.Context, sub provider.PushSubscription, payload any) error {
	push, err := p.env.pusher(ctx)
	if err != nil {
		slog.Warn("web push is unavailable", "error", err)
		return err
	}
	return push.Send(ctx, sub, payload)
}

// pusher is the sender for this deployment, or the one a test installed.
func (env *Env) pusher(ctx context.Context) (service.Pusher, error) {
	if env.Pusher != nil {
		return env.Pusher, nil
	}
	keys, err := env.vapidKeys(ctx)
	if err != nil {
		return nil, err
	}
	return &provider.Push{Keys: keys, Subject: env.vapidSubject(ctx)}, nil
}

// vapidKeys is the keypair the configuration names, else the one this server
// generated for itself on first need and keeps in the database, so it is the
// same after a restart and a subscription made today still opens tomorrow.
func (env *Env) vapidKeys(ctx context.Context) (provider.VapidKeys, error) {
	if env.Cfg.VAPIDPublicKey != "" && env.Cfg.VAPIDPrivateKey != "" {
		return provider.VapidKeys{PublicKey: env.Cfg.VAPIDPublicKey, PrivateKey: env.Cfg.VAPIDPrivateKey}, nil
	}
	env.vapidMu.Lock()
	defer env.vapidMu.Unlock()
	if env.vapid.PublicKey != "" {
		return env.vapid, nil
	}
	sealed, err := sealedStore(env)
	if err != nil {
		return provider.VapidKeys{}, err
	}
	private, public, err := sealed.EnsureVAPIDKeys(ctx, provider.GenerateVapidKeys)
	if err != nil {
		return provider.VapidKeys{}, err
	}
	env.vapid = provider.VapidKeys{PrivateKey: private, PublicKey: public}
	return env.vapid, nil
}

// vapidSubject is the `sub` claim push services may use to reach the operator:
// the configured one, else this server's own https address (which sends nobody's
// email to a third party), else the first administrator's address.
func (env *Env) vapidSubject(ctx context.Context) string {
	if env.Cfg.VAPIDSubject != "" {
		return env.Cfg.VAPIDSubject
	}
	if strings.HasPrefix(env.Cfg.FrontendURL, "https://") {
		return strings.TrimRight(env.Cfg.FrontendURL, "/")
	}
	if users, err := env.DB.ListUsers(ctx); err == nil {
		for _, one := range users {
			if one.IsSuperuser && one.IsActive && one.Email != "" {
				return "mailto:" + one.Email
			}
		}
	}
	return "mailto:admin@example.com"
}

func listPushSubscriptions(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListPushSubscriptions(r.Context(), sp.ID(), sp.UserID())
	if err != nil {
		return err
	}
	keys, err := env.vapidKeys(r.Context())
	if err != nil {
		return err
	}
	out := PushSubscriptionsResponse{
		PublicKey:     keys.PublicKey,
		Subscriptions: make([]PushSubscriptionResponse, 0, len(rows)),
	}
	for _, one := range rows {
		out.Subscriptions = append(out.Subscriptions, PushSubscriptionResponse{
			ID: one.ID, UserAgent: one.UserAgent,
			CreatedAt: one.CreatedAt, LastUsed: one.LastUsedAt,
		})
	}
	return writeJSON(w, http.StatusOK, out)
}

func subscribeToPush(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body PushSubscribeRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Endpoint) == "" ||
		strings.TrimSpace(body.P256dh) == "" || strings.TrimSpace(body.Auth) == "" {
		return errBadRequest("A push subscription needs an endpoint and both keys")
	}
	// The sender's own check, made here so an undeliverable subscription is
	// refused where somebody can see it.
	if err := provider.AssertEndpointAllowed(r.Context(), body.Endpoint, nil); err != nil {
		return errBadRequest("%s is not somewhere this server will send to", body.Endpoint)
	}

	one := store.PushSubscription{
		Endpoint: body.Endpoint, P256dh: body.P256dh, Auth: body.Auth,
		UserAgent: textutil.Clip(textutil.FirstNonBlank(r.UserAgent(), "a browser"), 255),
	}
	if err := env.DB.SavePushSubscription(r.Context(), sp.ID(), sp.UserID(), &one); err != nil {
		return err
	}
	return listPushSubscriptions(env, w, r, sp)
}

// PushTestResponse says how many of this person's browsers the test reached.
type PushTestResponse struct {
	Sent int `json:"sent"`
}

// sendTestPush delivers a notification to every browser this person has
// allowed, so turning push on shows itself working. A subscription the push
// service reports gone is forgotten, as in a real alert.
func sendTestPush(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	push, err := env.pusher(r.Context())
	if err != nil {
		return err
	}
	rows, err := env.DB.ListPushSubscriptions(r.Context(), sp.ID(), sp.UserID())
	if err != nil {
		return err
	}
	payload := map[string]any{
		"title": "Notifications are on",
		"body":  "Alerts from Agentifi will appear here.",
		"url":   "/settings/notifications",
	}
	out := PushTestResponse{}
	for _, one := range rows {
		err := push.Send(r.Context(), one.Credentials(), payload)
		switch {
		case err == nil:
			out.Sent++
			_ = env.DB.TouchPushSubscription(r.Context(), one.ID)
		case errors.Is(err, provider.ErrPushSubscriptionGone):
			_ = env.DB.DeletePushSubscriptionByEndpoint(r.Context(), sp.ID(), one.Endpoint)
		default:
			slog.Warn("a test push was not delivered", "error", err)
		}
	}
	return writeJSON(w, http.StatusOK, out)
}

func unsubscribeFromPush(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "subscription_id", "Subscription")
	if err != nil {
		return err
	}
	removed, err := env.DB.DeletePushSubscription(r.Context(), sp.ID(), sp.UserID(), id)
	if err != nil {
		return err
	}
	if !removed {
		return errNotFound("Subscription")
	}
	return writeNoContent(w)
}

func notificationResponse(one store.Notification) NotificationResponse {
	label := string(one.AlertType)
	if definition, ok := domain.AlertByType(one.AlertType); ok {
		label = definition.Label
	}
	return NotificationResponse{
		ID:        one.ID,
		AlertType: string(one.AlertType),
		Label:     label,
		Title:     one.Title,
		Body:      one.Body,
		URL:       one.URL,
		ReadAt:    one.ReadAt,
		CreatedAt: one.CreatedAt,
	}
}

func listAlertSettings(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rules, err := env.DB.ListAlertRules(r.Context(), sp.ID(), sp.UserID())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, env.alertSettings(rules))
}

func saveAlertSetting(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	definition, err := pathAlertType(r)
	if err != nil {
		return err
	}
	var body AlertSettingUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	rule, err := ruleFrom(definition, body)
	if err != nil {
		return err
	}
	// The pause flag is not in the body: letting a row edit carry it would let
	// one alert quietly un-pause the rest.
	existing, err := env.DB.ListAlertRules(r.Context(), sp.ID(), sp.UserID())
	if err != nil {
		return err
	}
	for _, one := range existing {
		if one.AlertType == definition.Type && one.AccountID == rule.AccountID {
			rule.ID, rule.IsPaused = one.ID, one.IsPaused
			break
		}
	}
	if err := env.DB.SaveAlertRule(r.Context(), sp.ID(), sp.UserID(), &rule); err != nil {
		return err
	}

	rules, err := env.DB.ListAlertRules(r.Context(), sp.ID(), sp.UserID())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, env.alertSettings(rules))
}

func pauseAllAlerts(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body PauseAllRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	// Pausing writes only rows that exist, so the defaults are materialised
	// first for an alert nobody has touched.
	if body.Paused {
		if err := materialiseAlertDefaults(env, r, sp); err != nil {
			return err
		}
	}
	if err := env.DB.PauseAllAlerts(r.Context(), sp.ID(), sp.UserID(), body.Paused); err != nil {
		return err
	}
	rules, err := env.DB.ListAlertRules(r.Context(), sp.ID(), sp.UserID())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, env.alertSettings(rules))
}

// materialiseAlertDefaults writes the catalog default for every alert this
// person has never touched, so a switch acting on stored rows acts on all.
func materialiseAlertDefaults(env *Env, r *http.Request, sp auth.SpaceContext) error {
	existing, err := env.DB.ListAlertRules(r.Context(), sp.ID(), sp.UserID())
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
		if err := env.DB.SaveAlertRule(r.Context(), sp.ID(), sp.UserID(), &rule); err != nil {
			return err
		}
	}
	return nil
}

// alertSettings folds stored answers into the catalog, in the catalog's order.
func (env *Env) alertSettings(rules []store.AlertRule) AlertSettingsResponse {
	stored := make(map[domain.AlertType]store.AlertRule, len(rules))
	for _, one := range rules {
		if one.AccountID == uuid.Nil {
			stored[one.AlertType] = one
		}
	}

	out := AlertSettingsResponse{Alerts: make([]AlertSettingResponse, 0, len(domain.AlertCatalog))}
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
	return out
}

func alertSetting(
	definition domain.AlertDefinition, rule store.AlertRule, configured bool,
) AlertSettingResponse {
	channels := make([]string, 0, len(definition.Channels))
	for _, one := range definition.Channels {
		channels = append(channels, string(one))
	}
	response := AlertSettingResponse{
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
		Configured:   configured,
	}
	if rule.HasThresholdAmount {
		amount := rule.ThresholdAmount
		response.ThresholdAmount = &amount
	}
	if rule.HasThresholdCount {
		count := rule.ThresholdCount
		response.ThresholdCount = &count
	}
	if rule.HasThresholdPercent {
		percent := rule.ThresholdPercent
		response.ThresholdPercent = &percent
	}
	return response
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
func ruleFrom(definition domain.AlertDefinition, body AlertSettingUpdate) (store.AlertRule, error) {
	rule := store.AlertRule{
		AlertType:    definition.Type,
		IsEnabled:    body.IsEnabled,
		ChannelEmail: body.ChannelEmail,
		ChannelPush:  body.ChannelPush,
		ChannelInApp: body.ChannelInApp,
	}
	for channel, on := range map[domain.AlertChannel]bool{
		domain.ChannelEmail: body.ChannelEmail,
		domain.ChannelPush:  body.ChannelPush,
		domain.ChannelInApp: body.ChannelInApp,
	} {
		if on && !definition.Allows(channel) {
			return store.AlertRule{}, errBadRequest(
				"%s cannot be sent by %s", definition.Label, channel)
		}
	}

	switch definition.Threshold {
	case domain.ThresholdAmount:
		if body.ThresholdAmount == nil {
			return store.AlertRule{}, errBadRequest("%s needs an amount", definition.Label)
		}
		if body.ThresholdAmount.IsNegative() {
			return store.AlertRule{}, errBadRequest("An amount cannot be negative")
		}
		rule.ThresholdAmount, rule.HasThresholdAmount = *body.ThresholdAmount, true
	case domain.ThresholdCount:
		if body.ThresholdCount == nil {
			return store.AlertRule{}, errBadRequest("%s needs a count", definition.Label)
		}
		if *body.ThresholdCount < 1 {
			return store.AlertRule{}, errBadRequest("A count has to be at least one")
		}
		rule.ThresholdCount, rule.HasThresholdCount = *body.ThresholdCount, true
	case domain.ThresholdPercent:
		if body.ThresholdPercent == nil {
			return store.AlertRule{}, errBadRequest("%s needs a percentage", definition.Label)
		}
		if body.ThresholdPercent.IsNegative() {
			return store.AlertRule{}, errBadRequest("A percentage cannot be negative")
		}
		rule.ThresholdPercent, rule.HasThresholdPercent = *body.ThresholdPercent, true
	case domain.ThresholdNone:
		if body.ThresholdAmount != nil || body.ThresholdCount != nil ||
			body.ThresholdPercent != nil {
			return store.AlertRule{}, errBadRequest(
				"%s has nothing to threshold on", definition.Label)
		}
	}
	return rule, nil
}

func pathAlertType(r *http.Request) (domain.AlertDefinition, error) {
	raw := strings.TrimSpace(chi.URLParam(r, "alert_type"))
	definition, ok := domain.AlertByType(domain.AlertType(raw))
	if !ok {
		return domain.AlertDefinition{}, errNotFound("Alert")
	}
	return definition, nil
}
