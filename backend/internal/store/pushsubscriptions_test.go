package store_test

import (
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLitePushSubscriptions(t *testing.T) {
	st := storetest.New(t)
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	ctx, user := t.Context(), uuid.New()
	one := store.PushSubscription{Endpoint: "https://push.example/browser", P256dh: "key", Auth: "secret", UserAgent: "browser"}
	require.NoError(t, st.SavePushSubscription(ctx, a, user, &one))
	originalID, created := one.ID, one.CreatedAt
	require.Equal(t, user, one.UserID)
	require.NoError(t, st.SavePushSubscription(ctx, b, user, &store.PushSubscription{Endpoint: one.Endpoint, P256dh: "other", Auth: "other"}))
	one.ID, one.Auth = uuid.Nil, "refreshed"
	require.NoError(t, st.SavePushSubscription(ctx, a, user, &one))
	require.Equal(t, originalID, one.ID)
	require.Equal(t, created, one.CreatedAt)
	require.NoError(t, st.TouchPushSubscription(ctx, b, one.ID))
	rows, err := st.ListPushSubscriptions(ctx, a, user)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].LastUsedAt)
	require.Equal(t, "refreshed", rows[0].Auth)
	require.NoError(t, st.TouchPushSubscription(ctx, a, one.ID))
	rows, err = st.ListPushSubscriptions(ctx, a, user)
	require.NoError(t, err)
	require.NotNil(t, rows[0].LastUsedAt)
	removed, err := st.DeletePushSubscription(ctx, a, uuid.New(), one.ID)
	require.NoError(t, err)
	require.False(t, removed)
	removed, err = st.DeletePushSubscription(ctx, a, user, one.ID)
	require.NoError(t, err)
	require.True(t, removed)
	require.NoError(t, st.DeletePushSubscriptionByEndpoint(ctx, b, one.Endpoint))
	rows, err = st.ListPushSubscriptions(ctx, b, user)
	require.NoError(t, err)
	require.Empty(t, rows)
}
