package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/CornHead764/agentifi/backend/internal/testdb"
)

// The HTTP tests run against a real Postgres, because the properties they
// check — that a row in another space is invisible through every endpoint,
// that an amount survives the round trip to the cent — are properties of the
// database and of the whole stack, not of any Go code a fake would exercise. A
// stub would answer from the dictionary the test itself filled.

// testStoragePath is where uploaded attachments land. Under the process's own
// temp directory for the reason the schema carries the pid: several runs share
// a workstation, and a fixed path means one run's cleanup deletes another's
// files.
var testStoragePath = filepath.Join(os.TempDir(), testdb.Name("api")+"_attachments")

func TestMain(m *testing.M) {
	storetest.Main(m, "api", func() { _ = os.RemoveAll(testStoragePath) })
}

var db = storetest.DB

// testConfig is a deployment with everything off that reaches the network.
func testConfig() *config.Config {
	return &config.Config{
		Debug:              true,
		SecretKey:          "test-only-secret-key",
		AccessTokenExpiry:  time.Hour,
		FrontendURL:        "http://localhost:5173",
		WebAuthnRPName:     "Agentifi",
		LoginMaxAttempts:   20,
		LoginAttemptWindow: 5 * time.Minute,
		SyncEnabled:        true,
		// The stand-in bridge is a loopback server over plain HTTP.
		SimpleFINAllowPrivate: true,
		SyncAt:                config.ParseTimeOfDay("04:00"),
		SyncCheckEvery:        5 * time.Minute,

		OIDCProviderName: "OIDC",
		PrimaryCurrency:  "USD",
		StoragePath:      testStoragePath,
	}
}

// client drives the router the way the frontend does: a bearer token, an
// X-Space-Id header, and JSON.
type client struct {
	t       *testing.T
	env     *Env
	handler http.Handler
	token   string
	spaceID string
}

func newClient(t *testing.T) *client {
	t.Helper()
	env := NewEnv(testConfig(), db(t))
	return &client{t: t, env: env, handler: RouterFor(env)}
}

func (c *client) as(user store.User) *client {
	c.t.Helper()
	token, err := c.env.Tokens.Issue(user.ID)
	require.NoError(c.t, err)
	c.token = token
	return c
}

func (c *client) inSpace(id store.SpaceID) *client {
	c.spaceID = id.String()
	return c
}

func (c *client) do(method, path string, body any) *response {
	c.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(c.t, err)
		reader = strings.NewReader(string(encoded))
	}
	r := httptest.NewRequest(method, path, reader)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.spaceID != "" {
		r.Header.Set("X-Space-Id", c.spaceID)
	}

	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, r)
	return &response{t: c.t, ResponseRecorder: recorder}
}

// raw sends a body the JSON encoder would not produce — a float in a money
// field, an unknown key — which is exactly what several of these tests are for.
func (c *client) raw(method, path, body string) *response {
	c.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.spaceID != "" {
		r.Header.Set("X-Space-Id", c.spaceID)
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, r)
	return &response{t: c.t, ResponseRecorder: recorder}
}

// form posts a form-encoded body, which is what the OAuth2 password grant at
// /auth/token takes.
func (c *client) form(path string, values url.Values) *response {
	c.t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, r)
	return &response{t: c.t, ResponseRecorder: recorder}
}

func (c *client) get(path string) *response { return c.do(http.MethodGet, path, nil) }
func (c *client) del(path string) *response { return c.do(http.MethodDelete, path, nil) }
func (c *client) post(path string, body any) *response {
	return c.do(http.MethodPost, path, body)
}
func (c *client) patch(path string, body any) *response {
	return c.do(http.MethodPatch, path, body)
}
func (c *client) put(path string, body any) *response { return c.do(http.MethodPut, path, body) }

type response struct {
	t *testing.T
	*httptest.ResponseRecorder
}

func (r *response) requireStatus(want int) *response {
	r.t.Helper()
	require.Equal(r.t, want, r.Code, "body: %s", r.Body.String())
	return r
}

func (r *response) json() map[string]any {
	r.t.Helper()
	var out map[string]any
	require.NoError(r.t, json.Unmarshal(r.Body.Bytes(), &out), "body: %s", r.Body.String())
	return out
}

func (r *response) list() []map[string]any {
	r.t.Helper()
	var out []map[string]any
	require.NoError(r.t, json.Unmarshal(r.Body.Bytes(), &out), "body: %s", r.Body.String())
	return out
}

// --- The seeded ledger -------------------------------------------------------

// ledger is a seeded household, and a second space it must never be able to
// see. Every tenancy assertion in this package is "reach for a row in the
// other space and get a 404".
type ledger struct {
	t     *testing.T
	env   *Env
	ids   map[string]uuid.UUID
	users map[string]store.User
	alex  *client
}

func (l *ledger) id(name string) uuid.UUID {
	l.t.Helper()
	id, ok := l.ids[name]
	require.True(l.t, ok, "no seeded id named %q", name)
	return id
}

func (l *ledger) str(name string) string { return l.id(name).String() }

// as returns a client authenticated as one of the seeded users, pointed at the
// household space. `vera` is a viewer, `alex` owns the space.
func (l *ledger) as(name string) *client {
	l.t.Helper()
	user, ok := l.users[name]
	require.True(l.t, ok, "no seeded user named %q", name)
	return (&client{t: l.t, env: l.env, handler: RouterFor(l.env)}).
		as(user).inSpace(store.SpaceIDOf(l.id("space")))
}

// buildLedger seeds one household and a second space the household cannot see.
//
// The figures are chosen so the window arithmetic is checkable by hand: the
// checking account opens at 500.00, loses 100.00 in July and 275.00 in August,
// and ends at 125.00.
func buildLedger(t *testing.T) *ledger {
	t.Helper()
	database := db(t)
	ctx := t.Context()
	env := NewEnv(testConfig(), database)
	ids := map[string]uuid.UUID{}

	space := &store.Space{Name: "Household", PrimaryCurrency: "USD"}
	other := &store.Space{Name: "Somebody Else", PrimaryCurrency: "USD"}
	require.NoError(t, database.CreateSpace(ctx, space))
	require.NoError(t, database.CreateSpace(ctx, other))
	ids["space"] = space.ID.UUID()
	ids["other_space"] = other.ID.UUID()

	users := map[string]store.User{}
	accepted := time.Now().UTC()
	for name, role := range map[string]struct {
		space store.SpaceID
		role  store.Role
	}{
		"alex": {space.ID, store.RoleOwner},
		"vera": {space.ID, store.RoleViewer},
		"bob":  {other.ID, store.RoleOwner},
	} {
		user := &store.User{
			Email:    fmt.Sprintf("%s-%s@example.test", name, uuid.NewString()),
			FullName: strings.ToUpper(name[:1]) + name[1:],
			IsActive: true,
		}
		require.NoError(t, database.CreateUser(ctx, user))
		require.NoError(t, database.CreateMembership(ctx, role.space, &store.Membership{
			UserID: user.ID, Role: role.role, AcceptedAt: &accepted,
		}))
		users[name] = *user
	}

	checking := &store.Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		OpeningBalance:   domain.MustFromString("500.00"),
		OpeningBalanceOn: domain.NewDate(2026, time.January, 1),
	}
	card := &store.Account{
		Name: "Rewards Card", Kind: domain.KindCreditCard, Type: "credit_card",
		Currency: "USD", IncludeInNetWorth: true,
		ProviderBalance: domain.MustFromString("-300.00"), HasProviderBalance: true,
		CreditLimit: domain.MustFromString("5000.00"), HasCreditLimit: true,
		// Held since January, before the windows the tests read; its own
		// first row is not until August.
		HistoryStartsOn: domain.NewDate(2026, time.January, 1),
	}
	strangerAccount := &store.Account{
		Name: "Their Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
	}
	require.NoError(t, database.CreateAccount(ctx, space.ID, checking))
	require.NoError(t, database.CreateAccount(ctx, space.ID, card))
	require.NoError(t, database.CreateAccount(ctx, other.ID, strangerAccount))
	ids["checking"] = checking.ID
	ids["card"] = card.ID
	ids["stranger_account"] = strangerAccount.ID

	food := &store.Category{
		Name: "Food & Dining", Kind: domain.CategoryExpense,
		IsUserAssignable: true, IsEditable: true,
	}
	system := &store.Category{
		Name: "Opening Balance", Kind: domain.CategoryExpense,
		KnownCategoryID: "OPENING_BALANCE", IsUserAssignable: false, IsEditable: false,
	}
	strangerCategory := &store.Category{
		Name: "Their Groceries", Kind: domain.CategoryExpense,
		IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(t, database.CreateCategory(ctx, space.ID, food))
	require.NoError(t, database.CreateCategory(ctx, space.ID, system))
	require.NoError(t, database.CreateCategory(ctx, other.ID, strangerCategory))

	groceries := &store.Category{
		ParentID: food.ID, Name: "Groceries", Kind: domain.CategoryExpense,
		IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(t, database.CreateCategory(ctx, space.ID, groceries))
	ids["food"] = food.ID
	ids["system_category"] = system.ID
	ids["groceries"] = groceries.ID
	ids["stranger_category"] = strangerCategory.ID

	tag := &store.Tag{Name: "reimbursable"}
	strangerTag := &store.Tag{Name: "theirs"}
	require.NoError(t, database.CreateTag(ctx, space.ID, tag))
	require.NoError(t, database.CreateTag(ctx, other.ID, strangerTag))
	ids["tag"] = tag.ID
	ids["stranger_tag"] = strangerTag.ID

	groceryFilter := &store.Filter{
		Name: "Groceries", Scope: "watchlist",
		Items: []store.FilterItem{{
			Field: string(domain.FieldCategory), Operator: string(domain.OpIn),
			ValueIDs: []uuid.UUID{groceries.ID},
		}},
	}
	strangerFilter := &store.Filter{Name: "Theirs", Scope: "watchlist"}
	require.NoError(t, database.CreateFilter(ctx, space.ID, groceryFilter))
	require.NoError(t, database.CreateFilter(ctx, other.ID, strangerFilter))
	ids["filter"] = groceryFilter.ID
	ids["stranger_filter"] = strangerFilter.ID

	pairID := uuid.New()
	ids["transfer_pair"] = pairID

	seed := []struct {
		key string
		txn *store.Transaction
	}{
		{"july", &store.Transaction{
			AccountID: checking.ID, Date: domain.NewDate(2026, time.July, 15),
			Amount: domain.MustFromString("-100.00"), Currency: "USD",
			StatementName: "OLD BANK STRING", Payee: "Old Payee",
			Source: domain.SourceSimplifiImport,
		}},
		{"august_groceries", &store.Transaction{
			AccountID: checking.ID, Date: domain.NewDate(2026, time.August, 5),
			Amount: domain.MustFromString("-50.00"), Currency: "USD",
			StatementName: "SAFEWAY #1234 SPRINGFIELD ZZ", Payee: "Safeway",
			CategoryID: groceries.ID, Source: domain.SourceSync,
		}},
		{"august_corner", &store.Transaction{
			AccountID: checking.ID, Date: domain.NewDate(2026, time.August, 20),
			Amount: domain.MustFromString("-25.00"), Currency: "USD",
			StatementName: "CORNER STORE", Payee: "Corner Store",
			IsReviewed: true, Source: domain.SourceSync,
		}},
		{"transfer_out", &store.Transaction{
			AccountID: checking.ID, Date: domain.NewDate(2026, time.August, 10),
			Amount: domain.MustFromString("-200.00"), Currency: "USD",
			StatementName: "ONLINE TRANSFER TO CARD", Payee: "Transfer to Rewards Card",
			TransferPairID: pairID, Source: domain.SourceSync,
		}},
		{"transfer_in", &store.Transaction{
			AccountID: card.ID, Date: domain.NewDate(2026, time.August, 10),
			Amount: domain.MustFromString("200.00"), Currency: "USD",
			StatementName: "PAYMENT THANK YOU", Payee: "Payment from Checking",
			TransferPairID: pairID, Source: domain.SourceSync,
		}},
		// A card charge posted in August whose cash-flow date is in September:
		// the row that makes `date` and `effective_date` visibly different.
		{"card_charge", &store.Transaction{
			AccountID: card.ID, Date: domain.NewDate(2026, time.August, 25),
			EffectiveDate: domain.NewDate(2026, time.September, 10),
			Amount:        domain.MustFromString("-75.00"), Currency: "USD",
			StatementName: "SQ *COFFEE XXXXXX1234 USA", Payee: "Harbor Coffee",
			Source: domain.SourceSync,
		}},
	}
	for _, row := range seed {
		require.NoError(t, database.CreateTransaction(ctx, space.ID, row.txn))
		ids[row.key] = row.txn.ID
	}

	stranger := &store.Transaction{
		AccountID: strangerAccount.ID, Date: domain.NewDate(2026, time.August, 12),
		Amount: domain.MustFromString("-10.00"), Currency: "USD",
		StatementName: "NOT YOURS", Payee: "Not Yours", Source: domain.SourceSync,
	}
	require.NoError(t, database.CreateTransaction(ctx, other.ID, stranger))
	ids["stranger_txn"] = stranger.ID

	world := &ledger{t: t, env: env, ids: ids, users: users}
	world.alex = world.as("alex")
	return world
}
