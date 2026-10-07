package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
)

// The hands-off half of the Amazon connector, against a scripted engine: a
// sign-in that asks for a code, a session that is kept, a pull that imports
// and matches, and a pull that meets a sign-in wall and tells the household.

// fakeAgent is a service.MerchantAgent with the challenge and the pull
// scripted. It stands where the engine that drives Chromium stands, so these
// tests exercise the resource rather than a browser.
type fakeAgent struct {
	t  *testing.T
	mu sync.Mutex
	// wall makes the next fetch answer needs_sign_in.
	wall bool
	// export is what a fetch returns as the Agentifi JSON file.
	export map[string]any
	// seen records the storage state each fetch was given, and credentials the
	// kept login beside it.
	seen        []string
	credentials []*provider.MerchantCredential
	// days records how far back each fetch was asked to read.
	days []int
	// paused is how a fetch that meets the wall with a kept password ends:
	// "" signs in again and pulls, anything else stops paused for that reason.
	paused string
	// unread makes a fetch get in, roll the session and read no orders.
	unread bool
	// failure, when set, is what a fetch fails with.
	failure error
}

func newFakeAgent(t *testing.T) *fakeAgent {
	t.Helper()
	return &fakeAgent{t: t}
}

func (a *fakeAgent) Available() bool { return true }

func (a *fakeAgent) Health(context.Context, domain.MerchantID) error { return nil }

func (a *fakeAgent) StartSignIn(
	ctx context.Context, merchant domain.MerchantID, email, password string,
) (provider.MerchantSignInState, error) {
	if password == "wrong" {
		return provider.MerchantSignInState{
			SessionID: "s1", State: provider.MerchantSignInFailed,
			Error: "The password is incorrect",
		}, nil
	}
	return provider.MerchantSignInState{
		SessionID: "s1", State: provider.MerchantSignInOTP,
		Prompt: "Enter the code from your authenticator app",
	}, nil
}

func (a *fakeAgent) AnswerSignIn(
	ctx context.Context, sessionID, code string,
) (provider.MerchantSignInState, error) {
	if code == "busy" {
		return provider.MerchantSignInState{}, &provider.AgentError{
			Kind: provider.ErrAgentConflict, Message: "Amazon has nothing to answer",
		}
	}
	if code != "123456" {
		return provider.MerchantSignInState{
			SessionID: "s1", State: provider.MerchantSignInOTP,
			Prompt: "That code was not right",
		}, nil
	}
	return provider.MerchantSignInState{
		SessionID: "s1", State: provider.MerchantSignInSignedIn,
	}, nil
}

func (a *fakeAgent) SignInStatus(
	ctx context.Context, sessionID string,
) (provider.MerchantSignInState, error) {
	return provider.MerchantSignInState{
		SessionID: sessionID, State: provider.MerchantSignInSignedIn,
	}, nil
}

// CompleteSignIn hands back a session whose cookie says which way the person
// came in, which is what the pull's assertions read.
func (a *fakeAgent) CompleteSignIn(
	ctx context.Context, sessionID string,
) (json.RawMessage, string, error) {
	if sessionID == "live1" {
		return json.RawMessage(`{"cookies":[{"name":"session-id","value":"byhand"}]}`), "Alex", nil
	}
	return json.RawMessage(`{"cookies":[{"name":"session-id","value":"abc"}]}`), "Casey", nil
}

func (a *fakeAgent) Fetch(
	ctx context.Context, merchant domain.MerchantID, storageState json.RawMessage,
	sinceDays int, skipDetails, invoiced []string, credential *provider.MerchantCredential,
) (provider.MerchantFetchResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen = append(a.seen, string(storageState))
	a.credentials = append(a.credentials, credential)
	a.days = append(a.days, sinceDays)
	if a.failure != nil {
		return provider.MerchantFetchResult{}, a.failure
	}
	if a.wall && credential.Usable() && a.paused != "" {
		return provider.MerchantFetchResult{
			NeedsSignIn: true, Paused: a.paused, Reason: "Amazon did not accept the kept password",
		}, nil
	}
	if a.wall && !credential.Usable() {
		return provider.MerchantFetchResult{
			NeedsSignIn: true, Reason: "Amazon asked for a one-time code",
		}, nil
	}
	if a.unread {
		return provider.MerchantFetchResult{
			StorageState: json.RawMessage(`{"cookies":[{"name":"session-id","value":"unread"}]}`),
			Notes:        []string{"no order list on the page"},
		}, nil
	}
	export, err := json.Marshal(a.export)
	require.NoError(a.t, err)
	parsed, err := merchantimport.Parse(merchant, export)
	require.NoError(a.t, err)
	return provider.MerchantFetchResult{
		AccountHint:  "Casey",
		StorageState: json.RawMessage(`{"cookies":[{"name":"session-id","value":"rolled"}]}`),
		Parsed:       &parsed, Orders: 1, Charges: 1,
	}, nil
}

// BackfillInvoices finds every page empty, which is enough for the resource.
func (a *fakeAgent) BackfillInvoices(
	ctx context.Context, merchant domain.MerchantID, storageState json.RawMessage, orders []string,
	each func(string, *provider.MerchantInvoice),
) (provider.MerchantBackfillResult, error) {
	for _, order := range orders {
		each(order, nil)
	}
	return provider.MerchantBackfillResult{}, nil
}

func (a *fakeAgent) LayOutReceipt(provider.MerchantReceipt) (string, []byte, error) {
	return "", nil, errors.New("the scripted engine prints nothing")
}

// noMerchantAgent is a build with no engine at all. Not the deployed case —
// serve always builds one — but the refusal it answers with is what the
// connector falls back to, so it is pinned rather than assumed.
type noMerchantAgent struct{ *fakeAgent }

func (noMerchantAgent) Available() bool { return false }

// noCamoufoxAgent is an engine with Chrome and no Camoufox server.
type noCamoufoxAgent struct{ *fakeAgent }

func (noCamoufoxAgent) Health(ctx context.Context, merchant domain.MerchantID) error {
	if merchant == domain.MerchantCostco {
		return browser.ErrNoFirefox
	}
	return nil
}

func agentExport() map[string]any {
	return map[string]any{
		"source": "agentifi-amazon-extract", "account_hint": "Casey",
		"orders": []any{map[string]any{
			"order_id": "113-1234567-0000001", "date": "2026-08-01", "total": "40.00",
			"currency": "USD", "status": "Delivered", "url": "",
			"items": []any{
				map[string]any{"title": "Glass Storage Set", "asin": "B0STORAGE", "quantity": 1, "price": "26.00", "url": ""},
				map[string]any{"title": "USB-C Cable", "asin": "B0CABLE", "quantity": 1, "price": "14.00", "url": ""},
			},
		}},
		"charges": []any{map[string]any{
			"order_id": "113-1234567-0000001", "date": "2026-08-02", "amount": "-40.00", "instrument": "Visa ••••1234",
		}},
	}
}

func withAgent(l *ledger, agent *fakeAgent) {
	l.env.MerchantAgent = agent
}

// awaitMerchantPull waits out the pull a finished sign-in starts in the
// background, so what a test does next is not refused as a second pull of the
// same account, and nothing is still writing when the test's database goes.
func awaitMerchantPull(t *testing.T, id string) {
	t.Helper()
	account := uuid.MustParse(id)
	require.Eventually(t, func() bool { return !service.MerchantPullRunning(account) },
		5*time.Second, 2*time.Millisecond, "the pull a sign-in started never finished")
}

func TestWithoutAnEngineTheConnectorTakesFilesOnly(t *testing.T) {
	l := buildLedger(t)
	l.env.MerchantAgent = noMerchantAgent{&fakeAgent{t: t}}
	account := merchantAccount(l, "Casey")
	status := l.alex.get("/merchants/amazon/agent").requireStatus(http.StatusOK).json()
	require.Equal(t, false, status["configured"])
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in",
		map[string]any{"email": "casey@example.com", "password": "pw"}).requireStatus(http.StatusConflict)
	l.alex.post("/merchants/amazon/accounts/"+account+"/pull", nil).requireStatus(http.StatusConflict)
}

func TestWithoutCamoufoxCostcoSaysWhySigningInIsOff(t *testing.T) {
	l := buildLedger(t)
	l.env.MerchantAgent = noCamoufoxAgent{newFakeAgent(t)}

	costco := l.alex.get("/merchants/costco/agent").requireStatus(http.StatusOK).json()
	require.Equal(t, true, costco["configured"])
	require.Equal(t, false, costco["reachable"])
	require.Contains(t, costco["unavailable"], "CAMOUFOX_URL")

	amazon := l.alex.get("/merchants/amazon/agent").requireStatus(http.StatusOK).json()
	require.Equal(t, true, amazon["reachable"])
	require.Equal(t, "", amazon["unavailable"])
}

func TestAnEngineConflictIsAConflictNotABadGateway(t *testing.T) {
	l := buildLedger(t)
	withAgent(l, newFakeAgent(t))
	account := merchantAccount(l, "Casey")
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in",
		map[string]any{"email": "casey@example.com", "password": "pw"}).requireStatus(http.StatusOK)
	refused := l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/answer",
		map[string]any{"code": "busy"}).requireStatus(http.StatusConflict).json()
	require.Contains(t, refused["detail"], "nothing to answer", "the engine's own sentence reaches the person")
}

func TestSigningInThroughAgentifiKeepsTheSessionAndPullsDaily(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeAgent(t)
	agent.export = agentExport()
	withAgent(l, agent)
	account := merchantAccount(l, "Casey")
	charge := merchantRow(l, "2026-08-03", "-40.00")

	status := l.alex.get("/merchants/amazon/agent").requireStatus(http.StatusOK).json()
	require.Equal(t, true, status["configured"])
	require.Equal(t, true, status["reachable"])

	// A wrong password is a failure the person sees, not a 500.
	wrong := l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in",
		map[string]any{"email": "casey@example.com", "password": "wrong"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "failed", wrong["state"])
	require.Contains(t, wrong["error"], "incorrect")

	// The real thing: Amazon wants a code, the person types it, done.
	step := l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in",
		map[string]any{"email": "casey@example.com", "password": "pw"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "otp", step["state"])
	require.Contains(t, step["prompt"], "authenticator")
	again := l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/answer",
		map[string]any{"code": "000000"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "otp", again["state"], "a wrong code asks again")
	done := l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/answer",
		map[string]any{"code": "123456"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "signed_in", done["state"])

	saved := l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/complete",
		map[string]any{"email": "casey@example.com"}).requireStatus(http.StatusOK).json()
	require.Equal(t, true, saved["connected"])
	require.Equal(t, true, saved["sync_enabled"], "signing in switches the daily pull on")
	require.Equal(t, "casey@example.com", saved["email"])
	require.NotNil(t, saved["signed_in_at"])
	require.Equal(t, true, saved["pulling"], "a finished sign-in starts a pull")
	awaitMerchantPull(t, account)

	// The pull the sign-in started: the agent's export is read by the same
	// importer as a file, the bank row finds its order by the charge, and the
	// rolled session is what gets kept.
	match := l.alex.get("/merchants/transactions/" + charge).requireStatus(http.StatusOK).json()
	require.Equal(t, "charge", match["basis"])
	require.Equal(t, "Casey", match["order"].(map[string]any)["account_label"])

	after := l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list()[0]
	require.Equal(t, "ok", after["last_sync_status"])
	require.NotNil(t, after["last_synced_at"])
	require.Equal(t, false, after["pulling"])
	agent.mu.Lock()
	require.Len(t, agent.seen, 1)
	require.Contains(t, agent.seen[0], `"abc"`, "the first pull used the session the sign-in kept")
	require.Equal(t, 365, agent.days[0], "the first pull after a sign-in reads a year back")
	agent.mu.Unlock()

	report := l.alex.post("/merchants/amazon/accounts/"+account+"/pull", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), report["new_orders"], "Update now after it finds nothing new")
	require.Equal(t, float64(1), report["charges"])
	require.Contains(t, agent.seen[1], `"rolled"`, "the next pull used the session the last pull handed back")
	require.Equal(t, 30, agent.days[1], "and reads the account's own reach")

	// Forgetting the session asks for a sign-in and leaves the switch alone; a
	// pull, or switching the pull on, waits for that sign-in.
	forgot := l.alex.del("/merchants/amazon/accounts/" + account + "/session").requireStatus(http.StatusOK).json()
	require.Equal(t, false, forgot["connected"])
	require.Equal(t, true, forgot["needs_sign_in"])
	require.Equal(t, true, forgot["sync_enabled"])
	l.alex.patch("/merchants/amazon/accounts/"+account, map[string]any{"sync_enabled": true}).requireStatus(http.StatusConflict)
	refused := l.alex.post("/merchants/amazon/accounts/"+account+"/pull", nil).requireStatus(http.StatusConflict).json()
	require.Contains(t, refused["detail"], "Sign in under this account")
}

func TestAPullThatReadsNothingKeepsItsSessionAndTellsTheHouseholdOnce(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeAgent(t)
	agent.export = agentExport()
	withAgent(l, agent)
	account := merchantAccount(l, "Alex")
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in",
		map[string]any{"email": "alex@example.com", "password": "pw"}).requireStatus(http.StatusOK)
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/answer", map[string]any{"code": "123456"}).
		requireStatus(http.StatusOK)
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/complete", nil).requireStatus(http.StatusOK)
	awaitMerchantPull(t, account)

	pullFailedAlerts := func() int {
		feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
		count := 0
		for _, item := range feed["notifications"].([]any) {
			if item.(map[string]any)["alert_type"] == "merchant_pull_failed" {
				count++
			}
		}
		return count
	}

	agent.mu.Lock()
	agent.unread = true
	agent.mu.Unlock()
	failed := l.alex.post("/merchants/amazon/accounts/"+account+"/pull", nil)
	require.GreaterOrEqual(t, failed.Code, 400)
	l.alex.post("/merchants/amazon/accounts/"+account+"/pull", nil)

	after := l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list()[0]
	require.Equal(t, "failed", after["last_sync_status"])
	require.Contains(t, after["last_sync_error"], "no order list on the page")
	require.Equal(t, 1, pullFailedAlerts(), "a failing streak is one alert, not one a pull")

	agent.mu.Lock()
	agent.unread = false
	agent.mu.Unlock()
	l.alex.post("/merchants/amazon/accounts/"+account+"/pull", nil).requireStatus(http.StatusOK)
	agent.mu.Lock()
	require.Contains(t, agent.seen[len(agent.seen)-1], `"unread"`,
		"the session a failed pull rolled is the one the next pull starts from")
	agent.mu.Unlock()
	require.Equal(t, "ok", l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).
		list()[0]["last_sync_status"])
}

func TestAFailedUpdateNowSaysWhetherItKeptThePage(t *testing.T) {
	// "Update now" answers a failure as an error, and the toast that reports
	// it offers the page as the account's card does: the body says one was
	// kept, and the screenshot route serves it.
	l := buildLedger(t)
	agent := newFakeAgent(t)
	agent.export = agentExport()
	withAgent(l, agent)
	account := merchantAccount(l, "Alex")
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in",
		map[string]any{"email": "alex@example.com", "password": "pw"}).requireStatus(http.StatusOK)
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/answer", map[string]any{"code": "123456"}).
		requireStatus(http.StatusOK)
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/complete", nil).requireStatus(http.StatusOK)
	awaitMerchantPull(t, account)

	var page bytes.Buffer
	require.NoError(t, png.Encode(&page, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	agent.mu.Lock()
	agent.failure = &provider.PageFailure{Err: errors.New("the order list never loaded"), Screenshot: page.Bytes()}
	agent.mu.Unlock()
	failed := l.alex.post("/merchants/amazon/accounts/"+account+"/pull", nil).
		requireStatus(http.StatusBadGateway).json()
	require.Equal(t, "the order list never loaded", failed["detail"])
	require.Equal(t, true, failed["has_failure_screenshot"])
	l.alex.get("/merchants/amazon/accounts/" + account + "/failure-screenshot").requireStatus(http.StatusOK)

	agent.mu.Lock()
	agent.failure = errors.New("the order list never loaded")
	agent.mu.Unlock()
	bare := l.alex.post("/merchants/amazon/accounts/"+account+"/pull", nil).
		requireStatus(http.StatusBadGateway).json()
	require.NotContains(t, bare, "has_failure_screenshot", "a failure that kept no page offers none")
}

func TestAPullThatMeetsASignInWallStopsAndTellsTheHousehold(t *testing.T) {
	l := buildLedger(t)
	agent := newFakeAgent(t)
	agent.export = agentExport()
	withAgent(l, agent)
	account := merchantAccount(l, "Alex")
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in",
		map[string]any{"email": "alex@example.com", "password": "pw"}).requireStatus(http.StatusOK)
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/answer", map[string]any{"code": "123456"}).
		requireStatus(http.StatusOK)
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/complete", nil).requireStatus(http.StatusOK)
	awaitMerchantPull(t, account)
	// A session with no password beside it, which nothing can sign past.
	l.alex.del("/merchants/amazon/accounts/" + account + "/credential").requireStatus(http.StatusOK)

	agent.mu.Lock()
	agent.wall = true
	agent.mu.Unlock()
	l.alex.post("/merchants/amazon/accounts/"+account+"/pull", nil).requireStatus(http.StatusConflict)

	after := l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list()[0]
	require.Equal(t, true, after["needs_sign_in"])
	require.Equal(t, "needs_sign_in", after["last_sync_status"])
	require.Contains(t, after["last_sync_error"], "one-time code")
	require.Equal(t, true, after["connected"], "the old session is kept until a new one replaces it")

	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	items := feed["notifications"].([]any)
	require.NotEmpty(t, items)
	first := items[0].(map[string]any)
	require.Equal(t, "amazon_sign_in", first["alert_type"])
	require.Contains(t, first["title"], "Alex")
	require.Equal(t, "/settings/merchants/amazon?sign-in="+account, first["url"],
		"the alert opens that account's sign-in, not just the page")

	// The scheduler's daily pass skips an account waiting on a sign-in, and
	// signing in again clears the flag and resumes.
	amazon := NewMerchants(l.env)
	require.Equal(t, 0, amazon.PullDue(context.Background(), time.Now().Add(-time.Hour)))

	agent.mu.Lock()
	agent.wall = false
	agent.mu.Unlock()
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in",
		map[string]any{"email": "alex@example.com", "password": "pw"}).requireStatus(http.StatusOK)
	l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/answer", map[string]any{"code": "123456"}).
		requireStatus(http.StatusOK)
	resumed := l.alex.post("/merchants/amazon/accounts/"+account+"/sign-in/s1/complete", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, false, resumed["needs_sign_in"])
	awaitMerchantPull(t, account)
	after = l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list()[0]
	require.Equal(t, "ok", after["last_sync_status"], "the sign-in's own pull resumed it")
	require.Equal(t, 0, amazon.PullDue(context.Background(), time.Now().Add(-time.Hour)),
		"so the scheduler has nothing left to pull")
}
