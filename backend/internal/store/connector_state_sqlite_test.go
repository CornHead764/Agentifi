package store_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteConnectorState(t *testing.T) {
	st := storetest.New(t)
	cipher, err := store.NewCipher("connector-test-key")
	require.NoError(t, err)
	st = st.WithCipher(cipher)
	ctx := t.Context()
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	c := store.BillConnection{Biller: domain.BillerID("test"), Label: "Home", CredentialSource: "typed", AutopayRule: "none", PullEnabled: true}
	require.NoError(t, st.CreateBillConnection(ctx, a, &c))
	credential := store.BillCredential{Username: "person", Password: "secret", TOTPSecret: "totp"}
	require.NoError(t, st.SaveBillConnectionCredential(ctx, a, c.ID, credential))
	gotCredential, err := st.BillConnectionCredential(ctx, a, c.ID)
	require.NoError(t, err)
	require.Equal(t, credential, gotCredential)
	require.NoError(t, st.SaveBillConnectionSession(ctx, a, c.ID, "session", "profile", true))
	session, err := st.BillConnectionSession(ctx, a, c.ID)
	require.NoError(t, err)
	require.Equal(t, "session", session)
	_, err = st.BillConnectionSession(ctx, b, c.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	shot, trail := []byte("screenshot"), []byte(`[{"page":"login"}]`)
	require.NoError(t, st.MarkBillPull(ctx, a, c.ID, "failed", "failed", shot, trail))
	got, err := st.BillPullScreenshot(ctx, a, c.ID)
	require.NoError(t, err)
	require.Equal(t, shot, got)
	got, err = st.BillPullTrail(ctx, a, c.ID)
	require.NoError(t, err)
	require.Equal(t, trail, got)
	require.NoError(t, st.MarkBillSignInEnded(ctx, a, c.ID, "ended", shot, trail))
	connection, err := st.GetBillConnection(ctx, a, c.ID)
	require.NoError(t, err)
	require.Equal(t, "sign_in_failed", connection.LastPullStatus)
	require.NoError(t, st.NoteBillPullSkipped(ctx, a, c.ID, "skipped"))
	_, err = st.BillPullScreenshot(ctx, a, c.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.NoError(t, st.MarkBillPull(ctx, a, c.ID, "failed", "oversized", bytes.Repeat([]byte{1}, store.MaxFailureScreenshotBytes+1), nil))
	_, err = st.BillPullScreenshot(ctx, a, c.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.NoError(t, st.ClearBillConnectionCredential(ctx, a, c.ID))
	_, err = st.BillConnectionCredential(ctx, a, c.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.NoError(t, st.SaveBillConnectionSession(ctx, a, c.ID, "fresh", "", true))
	connection, err = st.GetBillConnection(ctx, a, c.ID)
	require.NoError(t, err)
	require.Empty(t, connection.LastPullStatus)

	merchant := store.MerchantAccount{Merchant: domain.MerchantID("amazon"), Label: "House"}
	require.NoError(t, st.CreateMerchantAccount(ctx, a, &merchant))
	require.NoError(t, st.SaveMerchantSession(ctx, a, merchant.ID, "person@example.com", "merchant-session", true))
	require.NoError(t, st.SaveMerchantCredential(ctx, a, merchant.ID, credential))
	require.NoError(t, st.MarkMerchantSync(ctx, a, merchant.ID, "failed", "failed", shot))
	got, err = st.MerchantSyncScreenshot(ctx, a, merchant.ID)
	require.NoError(t, err)
	require.Equal(t, shot, got)
	accounts, err := st.ListMerchantAccountsDue(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, a, accounts[0].SpaceID)
	require.NoError(t, st.PauseMerchantSignIn(ctx, a, merchant.ID, "code_needed"))
	accounts, err = st.ListMerchantAccountsDue(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Empty(t, accounts)
	require.NoError(t, st.ClearMerchantCredential(ctx, a, merchant.ID))
	require.NoError(t, st.ClearMerchantSession(ctx, a, merchant.ID))
	_, err = st.MerchantSession(ctx, a, merchant.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.NoError(t, st.MarkMerchantSync(ctx, a, uuid.New(), "failed", "missing", shot))
}
