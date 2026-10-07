package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The page a connector's last run failed on, as the settings pages fetch it:
// said on the row, served as an image to the household, and to nobody else.

var inventedPage = []byte("\xff\xd8\xff an invented page")

func TestABillPullsFailureScreenshotIsServedToItsHouseholdOnly(t *testing.T) {
	l := buildLedger(t)
	connection := newBillConnection(l.alex, nil)
	id := connection["id"].(string)
	path := "/bills/connections/" + id + "/failure-screenshot"
	require.Equal(t, false, connection["has_failure_screenshot"])
	l.alex.get(path).requireStatus(http.StatusNotFound)

	space := store.SpaceIDOf(l.id("space"))
	require.NoError(t, l.env.DB.MarkBillPull(t.Context(), space, uuid.MustParse(id), store.BillPullFailed,
		"something covered the button", inventedPage, nil))

	read := l.alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, true, read["has_failure_screenshot"])
	served := l.alex.get(path).requireStatus(http.StatusOK)
	require.Equal(t, "image/jpeg", served.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", served.Header().Get("X-Content-Type-Options"))
	require.Contains(t, served.Header().Get("Cache-Control"), "no-store")
	require.Equal(t, inventedPage, served.Body.Bytes())
	l.as("vera").get(path).requireStatus(http.StatusOK)

	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	bob.get(path).requireStatus(http.StatusNotFound)

	require.NoError(t, l.env.DB.MarkBillPull(t.Context(), space, uuid.MustParse(id), store.BillPullOK, "", nil, nil))
	l.alex.get(path).requireStatus(http.StatusNotFound)
}

// A sign-in that never landed: its sentence and status on the row, its page
// where a pull's is served, and its trail in the dialog's shape.
func TestAnUnfinishedSignInsTrailIsServedToItsHouseholdOnly(t *testing.T) {
	l := buildLedger(t)
	connection := newBillConnection(l.alex, nil)
	id := connection["id"].(string)
	path := "/bills/connections/" + id + "/trail"
	require.Equal(t, false, connection["has_trail"])
	l.alex.get(path).requireStatus(http.StatusNotFound)

	space := store.SpaceIDOf(l.id("space"))
	trail := []byte(`[{"at":"2026-01-05T10:00:00Z","step":"factor","state":"factor","url":"https://portal.example.test/verify",` +
		`"choices":[{"kind":"radio","words":"Email"}],"chose":""}]`)
	require.NoError(t, l.env.DB.MarkBillSignInEnded(t.Context(), space, uuid.MustParse(id),
		"The sign-in was closed before it finished, while the portal was asking which way to verify.",
		inventedPage, trail))

	read := l.alex.get("/bills/connections/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, "sign_in_failed", read["last_pull_status"])
	require.Equal(t, true, read["has_trail"])
	require.Equal(t, true, read["has_failure_screenshot"])
	l.alex.get("/bills/connections/" + id + "/failure-screenshot").requireStatus(http.StatusOK)

	served := l.alex.get(path).requireStatus(http.StatusOK).list()
	require.Len(t, served, 1)
	require.Equal(t, "factor", served[0]["step"])
	require.Equal(t, "https://portal.example.test/verify", served[0]["url"])
	require.Equal(t, map[string]any{}, served[0]["inputs"], "in the dialog's shape, never null")
	l.as("vera").get(path).requireStatus(http.StatusOK)

	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	bob.get(path).requireStatus(http.StatusNotFound)

	require.NoError(t, l.env.DB.MarkBillPull(t.Context(), space, uuid.MustParse(id), store.BillPullOK, "", nil, nil))
	l.alex.get(path).requireStatus(http.StatusNotFound)
}

func TestAMerchantPullsFailureScreenshotIsServedToItsHouseholdOnly(t *testing.T) {
	l := buildLedger(t)
	id := merchantAccount(l, "Casey")
	path := "/merchants/amazon/accounts/" + id + "/failure-screenshot"
	l.alex.get(path).requireStatus(http.StatusNotFound)

	space := store.SpaceIDOf(l.id("space"))
	require.NoError(t, l.env.DB.MarkMerchantSync(t.Context(), space, uuid.MustParse(id), store.MerchantSyncFailed,
		"the orders page never loaded", inventedPage))

	listed := l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list()[0]
	require.Equal(t, true, listed["has_failure_screenshot"])
	served := l.alex.get(path).requireStatus(http.StatusOK)
	require.Equal(t, "image/jpeg", served.Header().Get("Content-Type"))
	require.Equal(t, inventedPage, served.Body.Bytes())

	l.alex.get("/merchants/costco/accounts/" + id + "/failure-screenshot").requireStatus(http.StatusNotFound)
	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	bob.get(path).requireStatus(http.StatusNotFound)
}
