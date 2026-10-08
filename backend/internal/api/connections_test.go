package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The connections endpoints, over the whole stack.
//
// The assertion that matters most is negative: no response from this resource
// ever contains the Access URL or the setup token, in any field. It is checked
// against the raw response body rather than against a struct field, because a
// struct field is the leak that would not have happened.

const testAccessSecret = "demo-not-a-real-credential"

// simplefinBridge is a stand-in SimpleFIN server. The connector reaches it
// over the loopback with its own default client, so no seam is needed.
func simplefinBridge(t *testing.T, accounts string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/claim/"):
			fmt.Fprint(w, bridgeAccessURL(server.URL))
		case strings.HasSuffix(r.URL.Path, "/accounts"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, accounts)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func bridgeAccessURL(base string) string {
	return strings.Replace(base, "http://", "http://demo:"+testAccessSecret+"@", 1) + "/simplefin"
}

func bridgeSetupToken(base string) string {
	return base64.StdEncoding.EncodeToString([]byte(base + "/simplefin/claim/abc123"))
}

const oneBridgeAccount = `{"errlist": [], "accounts": [
	{"id": "acc-1", "name": "Bridge Checking", "currency": "USD", "balance": "1200.00",
	 "org": {"name": "Big Bank"}, "transactions": []}
]}`

// connectionsClient is a client whose deployment has the connector switched on
// or off, which the seeded ledger's own environment does not vary.
func connectionsClient(t *testing.T, l *ledger, who string, enabled bool) *client {
	t.Helper()
	cfg := testConfig()
	cfg.SimpleFINEnabled = enabled
	env := NewEnv(cfg, db(t))
	user, ok := l.users[who]
	require.True(t, ok, "no seeded user named %q", who)
	return (&client{t: t, env: env, handler: RouterFor(env)}).
		as(user).inSpace(store.SpaceIDOf(l.id("space")))
}

// seedConnection writes a connection the way a claim would have, so the read
// paths can be exercised without a bridge.
func seedConnection(t *testing.T, spaceID store.SpaceID) store.Connection {
	t.Helper()
	cipher, err := store.NewCipher(testConfig().CredentialKey())
	require.NoError(t, err)
	connection := store.Connection{Name: "SimpleFIN", Status: store.ConnectionActive}
	require.NoError(t, db(t).WithCipher(cipher).CreateConnection(t.Context(), spaceID, &connection,
		"https://demo:"+testAccessSecret+"@bridge.simplefin.org/simplefin"))
	return connection
}

func TestAConnectionListingNeverCarriesTheCredential(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	connection := seedConnection(t, space)

	response := connectionsClient(t, l, "alex", true).get("/connections").
		requireStatus(http.StatusOK)
	body := response.Body.String()
	require.NotContains(t, body, testAccessSecret)
	require.NotContains(t, body, "bridge.simplefin.org")
	require.NotContains(t, body, "access_url")

	parsed := response.json()
	require.Equal(t, true, parsed["simplefin_enabled"])
	connections := parsed["connections"].([]any)
	require.Len(t, connections, 1)
	first := connections[0].(map[string]any)
	require.Equal(t, connection.ID.String(), first["id"])
	require.Equal(t, "active", first["status"])
	require.Equal(t, false, first["needs_setup_token"])
}

func TestAListingSaysWhetherTheConnectorIsSwitchedOn(t *testing.T) {
	// Off by default, and a settings page that cannot tell "no connections
	// yet" from "the connector is off" offers a form that always refuses.
	l := buildLedger(t)
	body := connectionsClient(t, l, "alex", false).get("/connections").
		requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["simplefin_enabled"])
}

func TestClaimingIsRefusedWhenTheConnectorIsOff(t *testing.T) {
	l := buildLedger(t)
	response := connectionsClient(t, l, "alex", false).
		post("/connections", map[string]any{"setup_token": "whatever"})
	response.requireStatus(http.StatusBadRequest)
	require.Contains(t, response.Body.String(), "not enabled")
}

func TestAClaimAnswersWithoutEchoingTheSetupToken(t *testing.T) {
	// The token is write-only. It is single-use at the Bridge, and there is no
	// reason for it to exist anywhere after the exchange.
	l := buildLedger(t)
	bridge := simplefinBridge(t, oneBridgeAccount)
	token := bridgeSetupToken(bridge.URL)

	response := connectionsClient(t, l, "alex", true).post("/connections", map[string]any{
		"setup_token": token,
		"name":        "Household bridge",
	}).requireStatus(http.StatusCreated)

	body := response.Body.String()
	require.NotContains(t, body, token)
	require.NotContains(t, body, testAccessSecret)
	require.NotContains(t, body, bridge.URL)

	created := response.json()
	require.Equal(t, "Household bridge", created["name"])
	// Claimed, not yet linked: a claim buys a credential and creates nothing,
	// because which remote account is which local one is the user's to say.
	require.Equal(t, "pending_link", created["status"])

	accounts := connectionsClient(t, l, "alex", true).get("/accounts").
		requireStatus(http.StatusOK).list()
	for _, account := range accounts {
		require.NotEqual(t, "Bridge Checking", account["name"],
			"the claim created an account before anybody matched it")
	}
}

func TestTheMatchScreenOffersTheHouseholdsOwnAccounts(t *testing.T) {
	// The screen docs/importing.md calls for: the bank's
	// accounts on one side, the household's unlinked ones on the other, and a
	// ranked guess at which is which.
	l := buildLedger(t)
	bridge := simplefinBridge(t, oneBridgeAccount)
	c := connectionsClient(t, l, "alex", true)
	created := c.post("/connections", map[string]any{
		"setup_token": bridgeSetupToken(bridge.URL),
	}).requireStatus(http.StatusCreated).json()

	body := c.get("/connections/" + created["id"].(string) + "/candidates").
		requireStatus(http.StatusOK).json()

	remote := body["remote"].([]any)
	require.Len(t, remote, 1)
	first := remote[0].(map[string]any)
	require.Equal(t, "Bridge Checking", first["name"])
	require.Nil(t, first["linked_account_id"], "nothing is paired before the user says so")

	require.NotEmpty(t, body["local"], "the household's own accounts were not offered")
}

// waitForSync polls the listing, as the page does, until the connection's
// sync has ended, and returns how it ended.
func waitForSync(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	var progress map[string]any
	require.Eventually(t, func() bool {
		for _, one := range c.get("/connections").requireStatus(http.StatusOK).json()["connections"].([]any) {
			connection := one.(map[string]any)
			if connection["id"] != id {
				continue
			}
			progress, _ = connection["sync"].(map[string]any)
			return progress != nil && progress["state"] != "running"
		}
		return false
	}, 10*time.Second, 20*time.Millisecond)
	return progress
}

func TestFinishingPairsTheAccountAtOnceAndStartsTheSync(t *testing.T) {
	l := buildLedger(t)
	bridge := simplefinBridge(t, oneBridgeAccount)
	c := connectionsClient(t, l, "alex", true)
	created := c.post("/connections", map[string]any{
		"setup_token": bridgeSetupToken(bridge.URL),
	}).requireStatus(http.StatusCreated).json()
	id := created["id"].(string)

	local := c.get("/connections/" + id + "/candidates").requireStatus(http.StatusOK).
		json()["local"].([]any)
	mine := local[0].(map[string]any)

	finished := c.post("/connections/"+id+"/links/finish", map[string]any{
		"choices": []map[string]any{{"external_id": "acc-1", "action": "link", "account_id": mine["id"]}},
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "active", finished["status"])
	require.NotNil(t, finished["sync"], "finishing did not start the sync")

	paired := func() int {
		matching := 0
		for _, account := range c.get("/accounts").requireStatus(http.StatusOK).list() {
			if account["connection_id"] == id {
				matching++
				require.Equal(t, mine["id"], account["id"],
					"a second account was created beside the one it was pointed at")
			}
		}
		return matching
	}
	require.Equal(t, 1, paired(), "the pairing waited for the sync")

	require.Equal(t, "succeeded", waitForSync(t, c, id)["state"])
	require.Equal(t, 1, paired())
}

func TestAFinishThatPairsOneAccountTwiceIsRefused(t *testing.T) {
	l := buildLedger(t)
	bridge := simplefinBridge(t, `{"errlist": [], "accounts": [
		{"id": "acc-1", "name": "Bridge Checking", "currency": "USD", "balance": "1200.00", "transactions": []},
		{"id": "acc-2", "name": "Bridge Savings", "currency": "USD", "balance": "300.00", "transactions": []}
	]}`)
	c := connectionsClient(t, l, "alex", true)
	id := c.post("/connections", map[string]any{
		"setup_token": bridgeSetupToken(bridge.URL),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	mine := c.get("/connections/" + id + "/candidates").requireStatus(http.StatusOK).
		json()["local"].([]any)[0].(map[string]any)

	response := c.post("/connections/"+id+"/links/finish", map[string]any{
		"choices": []map[string]any{
			{"external_id": "acc-1", "action": "link", "account_id": mine["id"]},
			{"external_id": "acc-2", "action": "link", "account_id": mine["id"]},
		},
	})
	response.requireStatus(http.StatusBadRequest)
	require.Contains(t, response.Body.String(), "two accounts at the bank")
}

func TestAnUnmatchedConnectionWillNotSync(t *testing.T) {
	l := buildLedger(t)
	bridge := simplefinBridge(t, oneBridgeAccount)
	c := connectionsClient(t, l, "alex", true)
	created := c.post("/connections", map[string]any{
		"setup_token": bridgeSetupToken(bridge.URL),
	}).requireStatus(http.StatusCreated).json()

	body := c.post("/connections/"+created["id"].(string)+"/sync", nil).
		requireStatus(http.StatusAccepted).json()
	require.Equal(t, "skipped", body["sync"].(map[string]any)["state"])
	for _, account := range c.get("/accounts").requireStatus(http.StatusOK).list() {
		require.NotEqual(t, created["id"], account["connection_id"], "an unmatched connection made an account")
	}
}

func TestAClaimedSetupTokenIsRefusedWithAnActionableSentence(t *testing.T) {
	// A setup token claimed twice comes back from the Bridge as a bare 403.
	// "403" tells a household nothing about the fix being a fresh token.
	l := buildLedger(t)
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(bridge.Close)

	response := connectionsClient(t, l, "alex", true).post("/connections", map[string]any{
		"setup_token": bridgeSetupToken(bridge.URL),
	})
	response.requireStatus(http.StatusBadRequest)
	require.Contains(t, response.Body.String(), "fresh token")
}

func TestAClaimWithNoTokenIsRefusedByField(t *testing.T) {
	l := buildLedger(t)
	response := connectionsClient(t, l, "alex", true).post("/connections", map[string]any{})
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "setup_token")
}

func TestSyncingReportsWhatItDidAndLeavesNoCredentialInTheBody(t *testing.T) {
	l := buildLedger(t)
	bridge := simplefinBridge(t, oneBridgeAccount)
	client := connectionsClient(t, l, "alex", true)

	created := client.post("/connections", map[string]any{
		"setup_token": bridgeSetupToken(bridge.URL),
	}).requireStatus(http.StatusCreated).json()
	id := created["id"].(string)
	// Matching answered with "none of these are mine", which is what releases
	// the connection to sync.
	client.post("/connections/"+id+"/links/finish", map[string]any{"choices": []any{}}).
		requireStatus(http.StatusOK)
	waitForSync(t, client, id)

	response := client.post("/connections/"+id+"/sync", nil).
		requireStatus(http.StatusAccepted)
	require.NotContains(t, response.Body.String(), testAccessSecret)

	progress := waitForSync(t, client, id)
	require.Equal(t, "succeeded", progress["state"])
	require.Equal(t, float64(1), progress["accounts"])
	require.Equal(t, float64(0), progress["transactions_imported"])
	listing := client.get("/connections").requireStatus(http.StatusOK)
	require.NotContains(t, listing.Body.String(), testAccessSecret)
	connection := listing.json()["connections"].([]any)[0].(map[string]any)
	require.Equal(t, "active", connection["status"])
	require.NotNil(t, connection["last_successful_sync_at"])
}

func TestPastingAFreshTokenRevivesTheSameConnection(t *testing.T) {
	l := buildLedger(t)
	bridge := simplefinBridge(t, oneBridgeAccount)
	client := connectionsClient(t, l, "alex", true)

	created := client.post("/connections", map[string]any{
		"setup_token": bridgeSetupToken(bridge.URL),
	}).requireStatus(http.StatusCreated).json()

	response := client.post("/connections/"+created["id"].(string)+"/token", map[string]any{
		"setup_token": bridgeSetupToken(bridge.URL),
	}).requireStatus(http.StatusOK)
	require.NotContains(t, response.Body.String(), testAccessSecret)

	revived := response.json()
	require.Equal(t, created["id"], revived["id"], "a fresh token made a second connection")
	require.Equal(t, "active", revived["status"])
}

func TestRenamingAConnectionKeepsItsStatus(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	connection := seedConnection(t, space)

	body := connectionsClient(t, l, "alex", true).
		patch("/connections/"+connection.ID.String(), map[string]any{"name": "Main bridge"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "Main bridge", body["name"])
	require.Equal(t, "active", body["status"])
}

func TestDeletingAConnectionKeepsTheTransactionsAndLeavesTheAccountsManual(t *testing.T) {
	// The endpoint's whole contract: a household removes a connection to stop
	// syncing, never to erase what it already collected.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	connection := seedConnection(t, space)

	account := &store.Account{
		Name: "Bridge Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		ConnectionID: connection.ID, ExternalID: "acc-1",
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, account))
	txn := &store.Transaction{
		AccountID: account.ID, ExternalID: "t1",
		Date: domain.NewDate(2026, time.March, 15), Amount: domain.MustFromString("-25.00"),
		Currency: "USD", StatementName: "SAFEWAY #1234", Payee: "Safeway",
		Source: domain.SourceSync,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, txn))

	client := connectionsClient(t, l, "alex", true)
	client.del("/connections/" + connection.ID.String()).requireStatus(http.StatusNoContent)

	surviving := client.get("/accounts/" + account.ID.String()).
		requireStatus(http.StatusOK).json()
	require.Nil(t, surviving["connection_id"], "the account still claims a connection")

	kept := client.get("/transactions/" + txn.ID.String()).requireStatus(http.StatusOK).json()
	require.Equal(t, "-25.00", kept["amount"])

	require.Empty(t, client.get("/connections").requireStatus(http.StatusOK).
		json()["connections"].([]any))
}

func TestUnlinkingOneAccountLeavesTheRestOfTheConnectionAlone(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	connection := seedConnection(t, space)

	linked := &store.Account{
		Name: "Bridge Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		ConnectionID: connection.ID, ExternalID: "acc-1",
		ProviderBalance: domain.MustFromString("1200.00"), HasProviderBalance: true,
	}
	other := &store.Account{
		Name: "Bridge Savings", Kind: domain.KindCash, Type: "savings",
		Currency: "USD", IncludeInNetWorth: true,
		ConnectionID: connection.ID, ExternalID: "acc-2",
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, linked))
	require.NoError(t, db(t).CreateAccount(t.Context(), space, other))

	client := connectionsClient(t, l, "alex", true)
	client.del("/connections/" + connection.ID.String() + "/accounts/" + linked.ID.String()).
		requireStatus(http.StatusNoContent)

	manual := client.get("/accounts/" + linked.ID.String()).requireStatus(http.StatusOK).json()
	require.Nil(t, manual["connection_id"])
	require.Nil(t, manual["provider_balance"])
	require.Nil(t, manual["simplefin_account_id"])

	still := client.get("/accounts/" + other.ID.String()).requireStatus(http.StatusOK).json()
	require.Equal(t, connection.ID.String(), still["connection_id"])
}

func TestAnAccountFromAnotherConnectionCannotBeUnlinkedThroughThisOne(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	connection := seedConnection(t, space)

	client := connectionsClient(t, l, "alex", true)
	client.del("/connections/" + connection.ID.String() + "/accounts/" + l.str("checking")).
		requireStatus(http.StatusNotFound)
}

func TestAViewerCannotClaimSyncOrRemoveAConnection(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	connection := seedConnection(t, space)
	vera := connectionsClient(t, l, "vera", true)

	vera.get("/connections").requireStatus(http.StatusOK)
	vera.post("/connections", map[string]any{"setup_token": "x"}).requireStatus(http.StatusForbidden)
	vera.post("/connections/"+connection.ID.String()+"/sync", nil).requireStatus(http.StatusForbidden)
	vera.del("/connections/" + connection.ID.String()).requireStatus(http.StatusForbidden)
}

func TestAConnectionInAnotherHouseholdIsANotFound(t *testing.T) {
	l := buildLedger(t)
	stranger := seedConnection(t, store.SpaceIDOf(l.id("other_space")))
	client := connectionsClient(t, l, "alex", true)

	client.get("/connections").requireStatus(http.StatusOK)
	client.patch("/connections/"+stranger.ID.String(), map[string]any{"name": "mine now"}).
		requireStatus(http.StatusNotFound)
	client.post("/connections/"+stranger.ID.String()+"/sync", nil).requireStatus(http.StatusNotFound)
	client.del("/connections/" + stranger.ID.String()).requireStatus(http.StatusNotFound)
}

func TestAConnectionResponseHasNoFieldThatCouldHoldACredential(t *testing.T) {
	// Structural rather than incidental: the response type is enumerated here
	// so a field added to it later has to be defended on purpose.
	fields := (&agentifiv1.Connection{}).ProtoReflect().Descriptor().Fields()
	for i := range fields.Len() {
		switch name := string(fields.Get(i).Name()); name {
		// "ignored" carries names and provider account ids for accounts the
		// household refused. Neither is a credential: the provider id is what
		// the match screen already shows, and nothing there reaches the bank.
		// "sync" is a run's phase, counts, an account's name and a sentence.
		case "sync", "id", "name", "status", "status_detail", "needs_setup_token", "bank_warnings",
			"ignored", "last_sync_at", "last_successful_sync_at", "retry_not_before",
			"created_at":
		default:
			t.Fatalf("connections responses grew a %q field; is it a credential?", name)
		}
	}
}

func TestAListingSaysWhenTheServerWillSyncByItself(t *testing.T) {
	// A household that cannot tell whether its ledger refreshes on its own
	// answers that by pressing Sync now every morning, which is the traffic
	// the daily window exists to avoid.
	l := buildLedger(t)
	seedConnection(t, store.SpaceIDOf(l.id("space")))

	body := connectionsClient(t, l, "alex", true).get("/connections").
		requireStatus(http.StatusOK).json()

	schedule, ok := body["schedule"].(map[string]any)
	require.True(t, ok, "the listing carries no schedule")
	require.Equal(t, true, schedule["enabled"])
	require.Equal(t, "04:00", schedule["at"])
	// The abbreviation a clock shows, never Go's literal "Local", which is what
	// Location().String() returns when the zone came from TZ.
	require.NotEmpty(t, schedule["time_zone"], "a window with no zone is a time the browser guesses")
	require.NotEqual(t, "Local", schedule["time_zone"])
	require.NotNil(t, schedule["next_run_at"])
}

func TestASchedulelessServerSaysSoRatherThanPromisingASync(t *testing.T) {
	l := buildLedger(t)
	cfg := testConfig()
	cfg.SimpleFINEnabled = true
	cfg.SyncEnabled = false
	env := NewEnv(cfg, db(t))
	c := (&client{t: t, env: env, handler: RouterFor(env)}).
		as(l.users["alex"]).inSpace(store.SpaceIDOf(l.id("space")))

	schedule := c.get("/connections").requireStatus(http.StatusOK).json()["schedule"].(map[string]any)
	require.Equal(t, false, schedule["enabled"])
	require.Nil(t, schedule["next_run_at"], "a disabled schedule promised a next run")
}

func TestSyncingAParkedConnectionReportsThatNothingWasRead(t *testing.T) {
	// Zeroes with `skipped` false would read as a sync that found nothing,
	// which is a different and much more alarming thing to say about a bank.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	connection := seedConnection(t, space)

	cipher, err := store.NewCipher(testConfig().CredentialKey())
	require.NoError(t, err)
	sealed := db(t).WithCipher(cipher)
	later := time.Now().Add(time.Hour)
	connection.Status = store.ConnectionRateLimited
	connection.StatusDetail = "the Bridge is throttling this connection"
	connection.RetryNotBefore = &later
	require.NoError(t, sealed.UpdateConnection(t.Context(), space, &connection))

	body := connectionsClient(t, l, "alex", true).
		post("/connections/"+connection.ID.String()+"/sync", nil).
		requireStatus(http.StatusAccepted).json()

	progress := body["sync"].(map[string]any)
	require.Equal(t, "skipped", progress["state"])
	require.Equal(t, float64(0), progress["transactions_imported"])
	require.Equal(t, "rate_limited", body["status"])
}

// --- ConnectionService over its own protocol ---------------------------------

func TestListConnectionsOverTheProcedureCarriesNoCredential(t *testing.T) {
	l := buildLedger(t)
	connection := seedConnection(t, store.SpaceIDOf(l.id("space")))
	client := connectionsClient(t, l, "alex", true)

	answer := client.rpc(agentifiv1connect.ConnectionServiceListConnectionsProcedure, `{}`).
		requireStatus(http.StatusOK)
	require.NotContains(t, answer.Body.String(), testAccessSecret)
	require.NotContains(t, answer.Body.String(), "bridge.simplefin.org")

	res, err := call[agentifiv1.ListConnectionsRequest, agentifiv1.ListConnectionsResponse](
		client, agentifiv1connect.ConnectionServiceListConnectionsProcedure, &agentifiv1.ListConnectionsRequest{})
	require.Nil(t, err)
	require.True(t, res.GetSimplefinEnabled())
	require.Len(t, res.GetConnections(), 1)
	require.Equal(t, connection.ID.String(), res.GetConnections()[0].GetId())
	require.NotNil(t, res.GetConnections()[0].GetCreatedAt())
	require.NotNil(t, res.GetSchedule())
}

func TestAConnectionIsRenamedButNeverNameless(t *testing.T) {
	l := buildLedger(t)
	connection := seedConnection(t, store.SpaceIDOf(l.id("space")))
	client := connectionsClient(t, l, "alex", true)

	res, err := call[agentifiv1.UpdateConnectionRequest, agentifiv1.UpdateConnectionResponse](
		client, agentifiv1connect.ConnectionServiceUpdateConnectionProcedure,
		&agentifiv1.UpdateConnectionRequest{ConnectionId: connection.ID.String(), Name: proto.String("Family bank")})
	require.Nil(t, err)
	require.Equal(t, "Family bank", res.GetConnection().GetName())

	_, err = call[agentifiv1.UpdateConnectionRequest, agentifiv1.UpdateConnectionResponse](
		client, agentifiv1connect.ConnectionServiceUpdateConnectionProcedure,
		&agentifiv1.UpdateConnectionRequest{
			ConnectionId: connection.ID.String(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		})
	require.Equal(t, connect.CodeFailedPrecondition, err.Code())
}

func TestAnotherHouseholdsConnectionIsNotFoundOverTheProcedure(t *testing.T) {
	l := buildLedger(t)
	stranger := seedConnection(t, store.SpaceIDOf(l.id("other_space")))
	client := connectionsClient(t, l, "alex", true)
	_, err := call[agentifiv1.DeleteConnectionRequest, agentifiv1.DeleteConnectionResponse](
		client, agentifiv1connect.ConnectionServiceDeleteConnectionProcedure,
		&agentifiv1.DeleteConnectionRequest{ConnectionId: stranger.ID.String()})
	require.Equal(t, connect.CodeNotFound, err.Code())
	require.Equal(t, "Connection not found", err.Message())
}

func TestAViewerIsRefusedAConnectionWriteOverTheProcedure(t *testing.T) {
	l := buildLedger(t)
	connection := seedConnection(t, store.SpaceIDOf(l.id("space")))
	_, err := call[agentifiv1.SyncConnectionRequest, agentifiv1.SyncConnectionResponse](
		connectionsClient(t, l, "vera", true), agentifiv1connect.ConnectionServiceSyncConnectionProcedure,
		&agentifiv1.SyncConnectionRequest{ConnectionId: connection.ID.String()})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}
