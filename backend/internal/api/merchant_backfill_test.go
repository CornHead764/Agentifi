package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/service"
)

// The invoice backfill as the Settings page drives it: how many orders it
// would reach, a start that answers at once, and how it ended on the account.
// Every order here is invented.

func TestABackfillCountsItsOrdersStartsInTheBackgroundAndReportsOnTheAccount(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeAgent(t)
	export := agentExport()
	order := export["orders"].([]any)[0].(map[string]any)
	order["url"] = "https://www.amazon.com/gp/your-account/order-details?orderID=113-1234567-0000001"
	agent.export = export
	withAgent(l, agent)
	account := merchantAccount(l, "Casey")

	refused := l.alex.post("/merchants/amazon/accounts/"+account+"/backfill", nil).
		requireStatus(http.StatusConflict).json()
	require.Contains(t, refused["detail"], "Sign in to Amazon before backfilling")

	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in",
		map[string]any{"email": "casey@example.com", "password": "pw"}).requireStatus(http.StatusOK)
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/answer", map[string]any{"code": "123456"}).
		requireStatus(http.StatusOK)
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/complete", nil).requireStatus(http.StatusOK)
	awaitMerchantPull(t, account)

	before := l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list()[0]
	require.Nil(t, before["backfill"], "no backfill has run")
	wanting := l.alex.get("/merchants/amazon/accounts/" + account + "/backfill").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), wanting["wanting"])

	started := l.alex.post("/merchants/amazon/accounts/"+account+"/backfill", nil).
		requireStatus(http.StatusAccepted).json()
	require.Equal(t, account, started["id"])
	id := uuid.MustParse(account)
	require.Eventually(t, func() bool {
		_, running := service.MerchantBackfillRunning(id)
		return !running && !service.MerchantPullRunning(id)
	}, 5*time.Second, 2*time.Millisecond)

	after := l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list()[0]
	backfill := after["backfill"].(map[string]any)
	require.Equal(t, false, backfill["running"])
	require.Equal(t, float64(0), backfill["filed"])
	require.Equal(t, float64(1), backfill["left"], "an empty page is tried again next time")
	require.Equal(t, "", backfill["stopped"])
	require.NotNil(t, backfill["finished_at"])
	require.Equal(t, before["last_synced_at"], after["last_synced_at"], "a backfill is not a pull")
}

func TestTheAssistantIsRefusedTheBackfillWithALinkToIt(t *testing.T) {
	const id = "8c6b1f33-0000-4000-8000-000000000001"
	err := refuseDeniedPath("/merchants/amazon/accounts/" + id + "/backfill")
	require.Error(t, err)
	require.Contains(t, err.Error(), "(/settings/merchants/amazon?backfill="+id+")")
	require.Contains(t, err.Error(), "the person starts it")
}
