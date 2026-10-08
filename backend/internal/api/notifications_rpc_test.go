package api

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// NotificationService over its own protocol. The REST URLs it used to answer
// are the bridge's, and notifications_test.go still reaches them.

func TestAViewerMayClearTheirBellButNotChangeAnAlert(t *testing.T) {
	l := buildLedger(t)
	vera := l.as("vera")
	_, err := call[agentifiv1.MarkAllNotificationsReadRequest, agentifiv1.MarkAllNotificationsReadResponse](
		vera, agentifiv1connect.NotificationServiceMarkAllNotificationsReadProcedure,
		&agentifiv1.MarkAllNotificationsReadRequest{})
	require.Nil(t, err)
	_, err = call[agentifiv1.ClearAllNotificationsRequest, agentifiv1.ClearAllNotificationsResponse](
		vera, agentifiv1connect.NotificationServiceClearAllNotificationsProcedure,
		&agentifiv1.ClearAllNotificationsRequest{})
	require.Nil(t, err)

	_, err = call[agentifiv1.SaveAlertSettingRequest, agentifiv1.SaveAlertSettingResponse](
		vera, agentifiv1connect.NotificationServiceSaveAlertSettingProcedure,
		&agentifiv1.SaveAlertSettingRequest{
			AlertType: string(domain.AlertLargeTransaction), IsEnabled: true, ChannelInApp: true,
			ThresholdAmount: &agentifiv1.NullableMoney{Amount: "250.00"},
		})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}

func TestSomebodyElsesNotificationIsNotFound(t *testing.T) {
	l := buildLedger(t)
	theirs := store.Notification{
		AlertType: domain.AlertLargeTransaction,
		Title:     "A large transaction", Body: "$1,200.00 at Somewhere",
		DedupeKey: "not-alexs",
	}
	_, err := l.env.DB.RecordNotification(
		t.Context(), store.SpaceIDOf(l.id("space")), l.users["vera"].ID, &theirs)
	require.NoError(t, err)

	for _, id := range []string{theirs.ID.String(), "not-an-id"} {
		_, refused := call[agentifiv1.MarkNotificationReadRequest, agentifiv1.MarkNotificationReadResponse](
			l.alex, agentifiv1connect.NotificationServiceMarkNotificationReadProcedure,
			&agentifiv1.MarkNotificationReadRequest{NotificationId: id})
		require.Equal(t, connect.CodeNotFound, refused.Code())
		_, refused = call[agentifiv1.ClearNotificationRequest, agentifiv1.ClearNotificationResponse](
			l.alex, agentifiv1connect.NotificationServiceClearNotificationProcedure,
			&agentifiv1.ClearNotificationRequest{NotificationId: id})
		require.Equal(t, connect.CodeNotFound, refused.Code())
	}

	res, refused := call[agentifiv1.ListNotificationsRequest, agentifiv1.ListNotificationsResponse](
		l.as("vera"), agentifiv1connect.NotificationServiceListNotificationsProcedure,
		&agentifiv1.ListNotificationsRequest{Unread: true})
	require.Nil(t, refused)
	require.Len(t, res.GetNotifications(), 1, "still there and still unread")
	require.EqualValues(t, 1, res.GetUnread())
	require.Nil(t, res.GetNotifications()[0].GetReadAt())
}

func TestAlertThresholdsTravelAsTheirOwnTypes(t *testing.T) {
	l := buildLedger(t)
	res, err := call[agentifiv1.GetAlertSettingsRequest, agentifiv1.GetAlertSettingsResponse](
		l.alex, agentifiv1connect.NotificationServiceGetAlertSettingsProcedure,
		&agentifiv1.GetAlertSettingsRequest{})
	require.Nil(t, err)
	alerts := map[string]*agentifiv1.AlertSetting{}
	for _, alert := range res.GetSettings().GetAlerts() {
		alerts[alert.GetType()] = alert
	}
	require.Len(t, alerts, len(domain.AlertCatalog))

	large := alerts[string(domain.AlertLargeTransaction)]
	require.Equal(t, "500.00", large.GetThresholdAmount().GetAmount())
	require.Nil(t, large.ThresholdPercent)
	share := alerts[string(domain.AlertBillsToIncomePct)]
	require.Equal(t, "50", share.GetThresholdPercent())
	require.Nil(t, share.GetThresholdAmount(), "an alert with no amount has it absent, not zero")

	l.alex.rpc(agentifiv1connect.NotificationServiceSaveAlertSettingProcedure, map[string]any{
		"alert_type": string(domain.AlertBillsToIncomePct), "is_enabled": true,
		"channel_in_app": true, "threshold_percent": "twelve",
	}).requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)

	saved, err := call[agentifiv1.SaveAlertSettingRequest, agentifiv1.SaveAlertSettingResponse](
		l.alex, agentifiv1connect.NotificationServiceSaveAlertSettingProcedure,
		&agentifiv1.SaveAlertSettingRequest{
			AlertType: string(domain.AlertBillsToIncomePct), IsEnabled: true, ChannelInApp: true,
			ThresholdPercent: proto.String("12.5"),
		})
	require.Nil(t, err)
	for _, alert := range saved.GetSettings().GetAlerts() {
		if alert.GetType() == string(domain.AlertBillsToIncomePct) {
			require.Equal(t, "12.5", alert.GetThresholdPercent())
			require.True(t, alert.GetConfigured())
		}
	}
}

func TestAPushSubscriptionIsRemovedOnceAndOnlyByAWriter(t *testing.T) {
	l := buildLedger(t)
	one := seedPush(l, "https://push.example.test/abc")

	listed, err := call[agentifiv1.ListPushSubscriptionsRequest, agentifiv1.ListPushSubscriptionsResponse](
		l.alex, agentifiv1connect.NotificationServiceListPushSubscriptionsProcedure,
		&agentifiv1.ListPushSubscriptionsRequest{})
	require.Nil(t, err)
	require.False(t, listed.GetPush().GetEnabled())
	require.Len(t, listed.GetPush().GetSubscriptions(), 1)
	require.Nil(t, listed.GetPush().GetSubscriptions()[0].GetLastUsedAt(), "never used is absent")

	remove := func(c *client, id string) *connect.Error {
		_, err := call[agentifiv1.DeletePushSubscriptionRequest, agentifiv1.DeletePushSubscriptionResponse](
			c, agentifiv1connect.NotificationServiceDeletePushSubscriptionProcedure,
			&agentifiv1.DeletePushSubscriptionRequest{SubscriptionId: id})
		return err
	}
	require.Equal(t, connect.CodePermissionDenied, remove(l.as("vera"), one.ID.String()).Code())
	require.Nil(t, remove(l.alex, one.ID.String()))
	require.Equal(t, connect.CodeNotFound, remove(l.alex, one.ID.String()).Code())
}
