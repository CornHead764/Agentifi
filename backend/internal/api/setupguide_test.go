package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// stepStates reads the guide as id → state, and which steps it offers.
func stepStates(t *testing.T, body map[string]any) map[string]string {
	t.Helper()
	steps, ok := body["steps"].([]any)
	require.True(t, ok)
	out := map[string]string{}
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		id, _ := step["id"].(string)
		state, _ := step["state"].(string)
		out[id] = state
	}
	return out
}

func guideClient(t *testing.T, adjust ...func(*store.User)) (*client, store.Space) {
	t.Helper()
	c := newClient(t)
	owner := makeUser(t, testPassword, adjust...)
	space := makeSpace(t, owner, "New household", store.RoleOwner, true)
	return c.as(owner).inSpace(space.ID), space
}

func TestANewSpaceStartsTheGuideAtTheImport(t *testing.T) {
	c, _ := guideClient(t)
	guide := c.get("/setup-guide").requireStatus(http.StatusOK).json()
	require.Equal(t, false, guide["dismissed"])
	require.Equal(t, false, guide["complete"])
	require.Equal(t, map[string]string{
		"import": "open", "connect": "open", "match": "open", "assistant": "open", "bills": "open",
	}, stepStates(t, guide))
}

func TestTheGuideChecksStepsOffFromTheLedger(t *testing.T) {
	c, space := guideClient(t)
	account := storetest.NewAccount(t, space.ID, "Checking")
	imported := &store.Transaction{
		AccountID: account.ID, Date: domain.NewDate(2025, 3, 4), Amount: domain.MustFromString("-8.00"),
		Currency: "USD", Source: domain.SourceSimplifiImport,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space.ID, imported))
	require.Equal(t, "done", stepStates(t, c.get("/setup-guide").json())["import"])

	connection := storetest.NewConnection(t, space.ID)
	states := stepStates(t, c.get("/setup-guide").json())
	require.Equal(t, "done", states["connect"])
	require.Equal(t, "open", states["match"], "connected, but nothing linked or synced")

	linked := storetest.NewAccount(t, space.ID, "Linked", storetest.WithConnection(connection))
	require.NotNil(t, linked)
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE connections SET last_successful_sync_at = $2 WHERE id = $1`, connection, time.Now())
	require.NoError(t, err)
	require.Equal(t, "done", stepStates(t, c.get("/setup-guide").json())["match"])
}

func TestASpaceWithAccountsButNoImportHasPassedTheImport(t *testing.T) {
	c, space := guideClient(t)
	storetest.NewAccount(t, space.ID, "Cash")
	require.Equal(t, "skipped", stepStates(t, c.get("/setup-guide").json())["import"])
}

func TestSkippingAndHidingTheGuideIsKeptForTheSpace(t *testing.T) {
	c, _ := guideClient(t)

	saved := c.patch("/setup-guide", map[string]any{
		"skipped": []string{"import", "connect", "assistant", "bills"},
	}).requireStatus(http.StatusOK).json()
	states := stepStates(t, saved)
	require.Equal(t, "skipped", states["import"])
	require.Equal(t, "skipped", states["match"], "matching follows connecting")
	require.Equal(t, true, saved["complete"])

	c.patch("/setup-guide", map[string]any{"dismissed": true}).requireStatus(http.StatusOK)
	guide := c.get("/setup-guide").json()
	require.Equal(t, true, guide["dismissed"])
	require.Equal(t, "skipped", stepStates(t, guide)["import"])

	c.patch("/setup-guide", map[string]any{"dismissed": false, "skipped": []string{}}).
		requireStatus(http.StatusOK)
	guide = c.get("/setup-guide").json()
	require.Equal(t, false, guide["dismissed"])
	require.Equal(t, "open", stepStates(t, guide)["import"])
}

func TestOnlyASkippableStepIsSkipped(t *testing.T) {
	c, _ := guideClient(t)
	c.patch("/setup-guide", map[string]any{"skipped": []string{"match"}}).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestAViewerReadsTheGuideButDoesNotChangeIt(t *testing.T) {
	c := newClient(t)
	viewer := makeUser(t, testPassword)
	space := makeSpace(t, viewer, "Shared", store.RoleViewer, true)
	c.as(viewer).inSpace(space.ID)
	c.get("/setup-guide").requireStatus(http.StatusOK)
	c.patch("/setup-guide", map[string]any{"dismissed": true}).requireStatus(http.StatusForbidden)
}

func TestTheBackupKeyIsAskedOnlyOfWhoeverAdministersTheServer(t *testing.T) {
	c, _ := guideClient(t, func(u *store.User) { u.IsSuperuser = true })
	clearBackupSettings(t, c.env)
	require.Equal(t, "open", stepStates(t, c.get("/setup-guide").json())["backups"])
}
