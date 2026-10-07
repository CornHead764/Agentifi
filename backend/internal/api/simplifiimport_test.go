package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Importing a Simplifi export from the app. The export is the importer's own
// invented fixture; what is under test here is the door, not the mapping.

func inventedExport(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../importer/testdata/invented-export.json")
	require.NoError(t, err)
	return string(raw)
}

// emptyHousehold is a space as signing up leaves it, with one person in each
// role.
type emptyHousehold struct {
	space store.SpaceID
	users map[store.Role]store.User
	env   *Env
}

func newEmptyHousehold(t *testing.T) *emptyHousehold {
	t.Helper()
	database := db(t)
	ctx := t.Context()
	space := &store.Space{Name: "Personal", PrimaryCurrency: "USD"}
	require.NoError(t, database.CreateSpace(ctx, space))
	h := &emptyHousehold{space: space.ID, users: map[store.Role]store.User{},
		env: NewEnv(testConfig(), database)}
	accepted := time.Now().UTC()
	for _, role := range []store.Role{store.RoleOwner, store.RoleAdmin, store.RoleMember, store.RoleViewer} {
		user := &store.User{
			Email:    fmt.Sprintf("%s-%s@example.test", role, uuid.NewString()),
			IsActive: true,
		}
		require.NoError(t, database.CreateUser(ctx, user))
		require.NoError(t, database.CreateMembership(ctx, space.ID, &store.Membership{
			UserID: user.ID, Role: role, AcceptedAt: &accepted,
		}))
		h.users[role] = *user
	}
	return h
}

func (h *emptyHousehold) as(t *testing.T, role store.Role) *client {
	t.Helper()
	return (&client{t: t, env: h.env, handler: RouterFor(h.env)}).as(h.users[role]).inSpace(h.space)
}

// uploadExport posts files by field name, and any plain fields, as a browser
// would.
func uploadExport(c *client, files map[string]string, fields map[string]string) *response {
	c.t.Helper()
	var buffer bytes.Buffer
	form := multipart.NewWriter(&buffer)
	for field, body := range files {
		part, err := form.CreateFormFile(field, field+".json")
		require.NoError(c.t, err)
		_, err = part.Write([]byte(body))
		require.NoError(c.t, err)
	}
	for key, value := range fields {
		require.NoError(c.t, form.WriteField(key, value))
	}
	require.NoError(c.t, form.Close())

	r := httptest.NewRequest(http.MethodPost, "/simplifi-import", &buffer)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("X-Space-Id", c.spaceID)
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, r)
	return &response{t: c.t, ResponseRecorder: recorder}
}

func previewOf(c *client, export string) map[string]any {
	c.t.Helper()
	return uploadExport(c, map[string]string{"file": export}, nil).requireStatus(http.StatusOK).json()
}

// finished polls a started job until it stops running.
func finished(c *client, id string) map[string]any {
	c.t.Helper()
	var status map[string]any
	require.Eventually(c.t, func() bool {
		status = c.get("/simplifi-import/" + id).requireStatus(http.StatusOK).json()
		return status["state"] != "running"
	}, 30*time.Second, 20*time.Millisecond)
	return status
}

func TestOnlyTheOwnerOrAnAdminMayImportFromSimplifi(t *testing.T) {
	h := newEmptyHousehold(t)
	export := inventedExport(t)
	for _, role := range []store.Role{store.RoleMember, store.RoleViewer} {
		uploadExport(h.as(t, role), map[string]string{"file": export}, nil).
			requireStatus(http.StatusForbidden)
	}

	preview := previewOf(h.as(t, store.RoleAdmin), export)
	require.Equal(t, true, preview["can_import"])
	id := preview["id"].(string)
	for _, role := range []store.Role{store.RoleMember, store.RoleViewer} {
		h.as(t, role).get("/simplifi-import/" + id).requireStatus(http.StatusForbidden)
		h.as(t, role).post("/simplifi-import/"+id+"/start", nil).requireStatus(http.StatusForbidden)
	}
}

func TestTheAssistantCannotReachTheSimplifiImport(t *testing.T) {
	for _, path := range []string{"/simplifi-import", "/simplifi-import/" + uuid.NewString() + "/start"} {
		err := refuseDeniedPath(path)
		require.Error(t, err, path)
		require.Contains(t, err.Error(), "(/settings/accounts)")
	}
	for _, route := range dispatchableRoutes() {
		require.NotEqual(t, "/simplifi-import", route.Prefix)
	}

	h := newEmptyHousehold(t)
	_, err := h.env.dispatch(t.Context(), auth.SpaceContext{
		Space: store.Space{ID: h.space}, User: h.users[store.RoleOwner],
		Membership: store.Membership{Role: store.RoleOwner},
	}, http.MethodPost, "/simplifi-import", nil, nil)
	require.Error(t, err)
}

func TestAPreviewCountsTheExportAndWritesNothing(t *testing.T) {
	h := newEmptyHousehold(t)
	owner := h.as(t, store.RoleOwner)
	preview := previewOf(owner, inventedExport(t))

	require.Equal(t, true, preview["can_import"])
	require.NotEmpty(t, preview["id"])
	require.Equal(t, "Household", preview["space_name"])
	require.Empty(t, preview["errors"])
	require.Empty(t, preview["refusal"])
	counts := preview["counts"].(map[string]any)
	require.EqualValues(t, 3, counts["accounts"])
	require.Positive(t, counts["transactions"])
	require.Contains(t, preview["summary"], "Simplifi import")

	require.Empty(t, owner.get("/accounts").requireStatus(http.StatusOK).list())
	status := owner.get("/simplifi-import/" + preview["id"].(string)).requireStatus(http.StatusOK).json()
	require.Equal(t, "ready", status["state"])
}

func TestAStartedImportFillsTheSpaceInTheBackground(t *testing.T) {
	h := newEmptyHousehold(t)
	owner := h.as(t, store.RoleOwner)
	preview := previewOf(owner, inventedExport(t))
	id := preview["id"].(string)

	started := owner.post("/simplifi-import/"+id+"/start", nil).requireStatus(http.StatusAccepted).json()
	require.Contains(t, []any{"running", "done"}, started["state"])
	status := finished(owner, id)
	require.Equal(t, "done", status["state"], status["error"])
	require.NotEmpty(t, status["finished_at"])
	require.Equal(t, preview["rows"], status["rows"])

	accounts := owner.get("/accounts").requireStatus(http.StatusOK).list()
	require.Len(t, accounts, int(preview["counts"].(map[string]any)["accounts"].(float64)))
	space, err := db(t).GetSpace(t.Context(), h.space)
	require.NoError(t, err)
	require.Equal(t, "Household", space.Name)

	// The same job cannot run twice, and a second upload finds the space full.
	owner.post("/simplifi-import/"+id+"/start", nil).requireStatus(http.StatusConflict)
	again := previewOf(owner, inventedExport(t))
	require.Equal(t, false, again["can_import"])
	require.Empty(t, again["id"])
	require.Contains(t, again["refusal"], "already holds")
}

func TestASpaceThatAlreadyHoldsDataIsRefusedWithTheReason(t *testing.T) {
	l := buildLedger(t)
	preview := previewOf(l.alex, inventedExport(t))
	require.Equal(t, false, preview["can_import"])
	require.Empty(t, preview["id"])
	require.Contains(t, preview["refusal"], "this space already holds")
	require.Contains(t, preview["refusal"], "accounts")
	require.NotContains(t, preview["refusal"], "importer:")
}

func TestAnImportIsCheckedAgainWhenItStarts(t *testing.T) {
	h := newEmptyHousehold(t)
	owner := h.as(t, store.RoleOwner)
	id := previewOf(owner, inventedExport(t))["id"].(string)

	_, err := db(t).Pool().Exec(t.Context(),
		`INSERT INTO tags (id, space_id, name) VALUES ($1, $2, 'added meanwhile')`,
		uuid.New(), h.space.UUID())
	require.NoError(t, err)

	owner.post("/simplifi-import/"+id+"/start", nil).requireStatus(http.StatusAccepted)
	status := finished(owner, id)
	require.Equal(t, "failed", status["state"])
	require.Contains(t, status["error"], "already holds 1 tags")
	require.Empty(t, owner.get("/accounts").requireStatus(http.StatusOK).list())
}

func TestAnExportImportedBeforeIsRefused(t *testing.T) {
	first := newEmptyHousehold(t)
	owner := first.as(t, store.RoleOwner)
	id := previewOf(owner, inventedExport(t))["id"].(string)
	owner.post("/simplifi-import/"+id+"/start", nil).requireStatus(http.StatusAccepted)
	require.Equal(t, "done", finished(owner, id)["state"])

	// A second empty space for the same person.
	second := &store.Space{Name: "Personal", PrimaryCurrency: "USD"}
	require.NoError(t, db(t).CreateSpace(t.Context(), second))
	accepted := time.Now().UTC()
	require.NoError(t, db(t).CreateMembership(t.Context(), second.ID, &store.Membership{
		UserID: first.users[store.RoleOwner].ID, Role: store.RoleOwner, AcceptedAt: &accepted,
	}))
	preview := previewOf(owner.inSpace(second.ID), inventedExport(t))
	require.Equal(t, false, preview["can_import"])
	require.Contains(t, preview["refusal"], `a space named "Household" already exists`)
}

func TestAnExportWithErrorsIsShownAndNeverStarted(t *testing.T) {
	h := newEmptyHousehold(t)
	decoder := json.NewDecoder(strings.NewReader(inventedExport(t)))
	decoder.UseNumber()
	var export map[string]any
	require.NoError(t, decoder.Decode(&export))
	accounts := export["datasets"].(map[string]any)["ds-1"].(map[string]any)["accountsStore"].(map[string]any)
	accounts["data"].(map[string]any)["resourcesById"].(map[string]any)["a1"].(map[string]any)["subType"] = "CRYPTO_WALLET"
	broken, err := json.Marshal(export)
	require.NoError(t, err)

	preview := previewOf(h.as(t, store.RoleOwner), string(broken))
	require.Equal(t, false, preview["can_import"])
	require.Empty(t, preview["id"])
	require.NotEmpty(t, preview["errors"])
}

func TestFieldsAndStoresANewerExporterAddsDoNotStopTheImport(t *testing.T) {
	h := newEmptyHousehold(t)
	owner := h.as(t, store.RoleOwner)
	decoder := json.NewDecoder(strings.NewReader(inventedExport(t)))
	decoder.UseNumber()
	var export map[string]any
	require.NoError(t, decoder.Decode(&export))
	stores := export["datasets"].(map[string]any)["ds-1"].(map[string]any)
	stores["investmentsQuotesDetailed"] = map[string]any{"version": 1, "data": map[string]any{
		"resourcesById": map[string]any{"q1": map[string]any{"id": "q1", "symbol": "ZZZX"}},
	}}
	accounts := stores["accountsStore"].(map[string]any)["data"].(map[string]any)["resourcesById"].(map[string]any)
	accounts["a1"].(map[string]any)["colorTheme"] = "teal"
	newer, err := json.Marshal(export)
	require.NoError(t, err)

	var rules []string
	for i := 1; i <= 6; i++ {
		rules = append(rules, fmt.Sprintf(`{"id": "tr%d", "payeeTarget": "Payee %d",
			"userModifiedAt": "2001-02-03T04:05:06Z",
			"payeeSourceCondition": {"payeeType": "STATEMENT",
				"payeeMatchingCriteria": [{"payeeMatchingOperator": "CONTAINS", "payeeName": "SHOP%d"}]}}`, i, i, i))
	}
	rulesFile := `{"resources": [` + strings.Join(rules, ",") + `]}`

	preview := uploadExport(owner, map[string]string{"file": string(newer), "transaction_rules": rulesFile}, nil).
		requireStatus(http.StatusOK).json()
	require.Empty(t, preview["errors"])
	require.Equal(t, true, preview["can_import"])

	var notRead map[string]any
	for _, group := range preview["warnings"].([]any) {
		if group.(map[string]any)["kind"] == "not_read" {
			notRead = group.(map[string]any)
		}
	}
	require.NotNil(t, notRead, preview["warnings"])
	require.Equal(t, true, notRead["note"])
	require.EqualValues(t, 8, notRead["count"], "six rules, one account and one store")
	require.ElementsMatch(t, []any{
		"investmentsQuotesDetailed: not read",
		"accountsStore a1 colorTheme: not read",
		"transactionRulesStore tr1 userModifiedAt: not read (x6)",
	}, notRead["items"])

	id := preview["id"].(string)
	owner.post("/simplifi-import/"+id+"/start", nil).requireStatus(http.StatusAccepted)
	status := finished(owner, id)
	require.Equal(t, "done", status["state"], status["error"])
}

func TestAnExportWithSeveralDatasetsAsksWhichOne(t *testing.T) {
	h := newEmptyHousehold(t)
	owner := h.as(t, store.RoleOwner)
	decoder := json.NewDecoder(strings.NewReader(inventedExport(t)))
	decoder.UseNumber()
	var export map[string]any
	require.NoError(t, decoder.Decode(&export))
	datasets := export["datasets"].(map[string]any)
	datasets["ds-2"] = datasets["ds-1"]
	two, err := json.Marshal(export)
	require.NoError(t, err)

	preview := previewOf(owner, string(two))
	require.ElementsMatch(t, []any{"ds-1", "ds-2"}, preview["datasets"])
	require.Equal(t, false, preview["can_import"])

	chosen := uploadExport(owner, map[string]string{"file": string(two)},
		map[string]string{"dataset": "ds-1"}).requireStatus(http.StatusOK).json()
	require.Equal(t, true, chosen["can_import"])
}

func TestAFileThatIsNotAnExportIsRefused(t *testing.T) {
	h := newEmptyHousehold(t)
	owner := h.as(t, store.RoleOwner)
	uploadExport(owner, nil, nil).requireStatus(http.StatusUnprocessableEntity)
	uploadExport(owner, map[string]string{"file": ""}, nil).requireStatus(http.StatusUnprocessableEntity)
	uploadExport(owner, map[string]string{"file": "{"}, nil).requireStatus(http.StatusBadRequest)
	uploadExport(owner, map[string]string{"file": `{"hello": "world"}`}, nil).
		requireStatus(http.StatusBadRequest)
	uploadExport(owner, map[string]string{"file": inventedExport(t), "transaction_rules": "not json"}, nil).
		requireStatus(http.StatusBadRequest)
	owner.get("/simplifi-import/" + uuid.NewString()).requireStatus(http.StatusNotFound)
}
