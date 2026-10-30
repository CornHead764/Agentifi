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

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"golang.org/x/crypto/bcrypt"
)

// HTTP tests exercise the real SQLite files through the complete stack, including
// household isolation and exact monetary round trips.

// Each test binary owns its attachment directory, and each configuration a
// directory beneath it, because a backup walks the whole of it while tests
// running in parallel write their files.
var testStoragePath = filepath.Join(os.TempDir(), "agentifi-api-"+uuid.NewString()+"_attachments")

func TestMain(m *testing.M) {
	auth.SetBcryptCostForTests(bcrypt.MinCost)
	storetest.Main(m, "api", func() { _ = os.RemoveAll(testStoragePath) })
}

func mustJSON(t testing.TB, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
}

// db is this test's private store; storetest fixtures share it via Bind.
func db(t testing.TB) *store.Store { return storetest.Private(t) }

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
		DataDir:          filepath.Join(testStoragePath, uuid.NewString()),
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

// ledger is a seeded household and, for the tests that ask for it, a second
// space it must never be able to see. Every tenancy assertion in this package
// is "reach for a row in the other space and get a 404".
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
	require.True(l.t, ok, "no seeded id named %q; the other space's rows need withOtherHousehold", name)
	return id
}

func (l *ledger) str(name string) string { return l.id(name).String() }

func (l *ledger) user(name string) store.User {
	l.t.Helper()
	user, ok := l.users[name]
	require.True(l.t, ok, "no seeded user named %q; bob needs withOtherHousehold", name)
	return user
}

// as returns a client authenticated as one of the seeded users, pointed at the
// household space. `vera` is a viewer, `alex` owns the space.
func (l *ledger) as(name string) *client {
	l.t.Helper()
	return (&client{t: l.t, env: l.env, handler: RouterFor(l.env)}).
		as(l.user(name)).inSpace(store.SpaceIDOf(l.id("space")))
}

func (l *ledger) addMember(space store.SpaceID, name string, role store.Role) {
	l.t.Helper()
	ctx := l.t.Context()
	accepted := time.Now().UTC()
	user := &store.User{
		Email:    fmt.Sprintf("%s-%s@example.test", name, uuid.NewString()),
		FullName: strings.ToUpper(name[:1]) + name[1:],
		IsActive: true,
	}
	require.NoError(l.t, l.env.DB.CreateUser(ctx, user))
	require.NoError(l.t, l.env.DB.CreateMembership(ctx, space, &store.Membership{
		UserID: user.ID, Role: role, AcceptedAt: &accepted,
	}))
	l.users[name] = *user
}

// buildLedger seeds one household, owned by alex, with vera as a viewer.
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
	require.NoError(t, database.CreateSpace(ctx, space))
	ids["space"] = space.ID.UUID()

	world := &ledger{t: t, env: env, ids: ids, users: map[string]store.User{}}
	world.addMember(space.ID, "alex", store.RoleOwner)
	world.addMember(space.ID, "vera", store.RoleViewer)

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
	require.NoError(t, database.CreateAccount(ctx, space.ID, checking))
	require.NoError(t, database.CreateAccount(ctx, space.ID, card))
	ids["checking"] = checking.ID
	ids["card"] = card.ID

	food := &store.Category{
		Name: "Food & Dining", Kind: domain.CategoryExpense,
		IsUserAssignable: true, IsEditable: true,
	}
	system := &store.Category{
		Name: "Opening Balance", Kind: domain.CategoryExpense,
		KnownCategoryID: "OPENING_BALANCE", IsUserAssignable: false, IsEditable: false,
	}
	require.NoError(t, database.CreateCategory(ctx, space.ID, food))
	require.NoError(t, database.CreateCategory(ctx, space.ID, system))

	groceries := &store.Category{
		ParentID: food.ID, Name: "Groceries", Kind: domain.CategoryExpense,
		IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(t, database.CreateCategory(ctx, space.ID, groceries))
	ids["food"] = food.ID
	ids["system_category"] = system.ID
	ids["groceries"] = groceries.ID

	tag := &store.Tag{Name: "reimbursable"}
	require.NoError(t, database.CreateTag(ctx, space.ID, tag))
	ids["tag"] = tag.ID

	groceryFilter := &store.Filter{
		Name: "Groceries", Scope: "watchlist",
		Items: []store.FilterItem{{
			Field: string(domain.FieldCategory), Operator: string(domain.OpIn),
			ValueIDs: []uuid.UUID{groceries.ID},
		}},
	}
	require.NoError(t, database.CreateFilter(ctx, space.ID, groceryFilter))
	ids["filter"] = groceryFilter.ID

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

	world.alex = world.as("alex")
	return world
}

// withOtherHousehold adds a second space the household cannot see, owned by
// bob, with an account, a category, a tag, a filter and a transaction of its
// own. Every SQLite household is a file of its own, so only a test of
// permissions or of isolation between households pays for one.
func (l *ledger) withOtherHousehold() *ledger {
	t := l.t
	t.Helper()
	database := l.env.DB
	ctx := t.Context()

	other := &store.Space{Name: "Somebody Else", PrimaryCurrency: "USD"}
	require.NoError(t, database.CreateSpace(ctx, other))
	l.ids["other_space"] = other.ID.UUID()
	l.addMember(other.ID, "bob", store.RoleOwner)

	strangerAccount := &store.Account{
		Name: "Their Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
	}
	require.NoError(t, database.CreateAccount(ctx, other.ID, strangerAccount))
	l.ids["stranger_account"] = strangerAccount.ID

	strangerCategory := &store.Category{
		Name: "Their Groceries", Kind: domain.CategoryExpense,
		IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(t, database.CreateCategory(ctx, other.ID, strangerCategory))
	l.ids["stranger_category"] = strangerCategory.ID

	strangerTag := &store.Tag{Name: "theirs"}
	require.NoError(t, database.CreateTag(ctx, other.ID, strangerTag))
	l.ids["stranger_tag"] = strangerTag.ID

	strangerFilter := &store.Filter{Name: "Theirs", Scope: "watchlist"}
	require.NoError(t, database.CreateFilter(ctx, other.ID, strangerFilter))
	l.ids["stranger_filter"] = strangerFilter.ID

	stranger := &store.Transaction{
		AccountID: strangerAccount.ID, Date: domain.NewDate(2026, time.August, 12),
		Amount: domain.MustFromString("-10.00"), Currency: "USD",
		StatementName: "NOT YOURS", Payee: "Not Yours", Source: domain.SourceSync,
	}
	require.NoError(t, database.CreateTransaction(ctx, other.ID, stranger))
	l.ids["stranger_txn"] = stranger.ID
	return l
}

func householdSQL(t testing.TB, st *store.Store, space store.SpaceID) sqlitedb.Conn {
	t.Helper()
	h, err := st.HouseholdDB(t.Context(), space)
	require.NoError(t, err)
	return h.Write()
}

func serverSQL(t testing.TB, st *store.Store) sqlitedb.Conn {
	t.Helper()
	h, err := st.ServerDB()
	require.NoError(t, err)
	return h.Write()
}

func (l *ledger) sql() sqlitedb.Conn {
	return householdSQL(l.t, l.env.DB, store.SpaceIDOf(l.id("space")))
}

func usdMinor(t testing.TB, raw string) int64 {
	t.Helper()
	n, err := minorunits.ToMinor(domain.MustFromString(raw).Decimal(), "USD")
	require.NoError(t, err)
	return n
}
