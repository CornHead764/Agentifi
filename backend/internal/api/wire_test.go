package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Money on the wire, and the three states of a patch field.
//
// Ground rule 1 and calculations.md §1: every amount is a quantized *string*
// in JSON, so the client has exactly one coercion point. The inbound half
// matters as much as the outbound one — a float that reaches a balance is only
// discoverable as a cent of drift months later.

// moneyFields are the response keys that must never be a JSON number.
var moneyFields = []string{
	"amount", "amount_primary", "balance", "opening_balance", "goal_balance",
	"pending_holds", "credit_limit", "provider_balance", "statement_balance",
	"minimum_due", "total", "ending_balance",
}

func requireMoneyIsAString(t *testing.T, body map[string]any) {
	t.Helper()
	for _, field := range moneyFields {
		value, present := body[field]
		if !present || value == nil {
			continue
		}
		require.IsType(t, "", value, "%s came back as a JSON number", field)
	}
}

func TestEveryAmountOnTheWireIsAString(t *testing.T) {
	l := buildLedger(t)

	requireMoneyIsAString(t, l.alex.get("/transactions/"+l.str("august_groceries")).
		requireStatus(http.StatusOK).json())
	requireMoneyIsAString(t, l.alex.get("/accounts/"+l.str("checking")).
		requireStatus(http.StatusOK).json())

	card := l.alex.get("/accounts/" + l.str("checking") + "/summary").
		requireStatus(http.StatusOK).json()
	requireMoneyIsAString(t, card)
	requireMoneyIsAString(t, card["balances"].(map[string]any))

	page := l.alex.get("/transactions?limit=500").requireStatus(http.StatusOK).json()
	requireMoneyIsAString(t, page)
	for _, item := range page["items"].([]any) {
		requireMoneyIsAString(t, item.(map[string]any))
	}
}

func TestAFloatInAMoneyFieldIsRefusedOnEveryWritePath(t *testing.T) {
	l := buildLedger(t)

	create := l.alex.raw(http.MethodPost, "/transactions", fmt.Sprintf(
		`{"account_id": %q, "date": "2026-08-28", "amount": -25.5}`, l.str("checking")))
	create.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, create.Body.String(), "JSON string")

	update := l.alex.raw(http.MethodPatch, "/transactions/"+l.str("august_groceries"),
		`{"amount": -25.5}`)
	update.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, update.Body.String(), "JSON string")

	splits := l.alex.raw(http.MethodPut, "/transactions/"+l.str("august_groceries")+"/splits",
		`{"splits": [{"amount": -50.0}]}`)
	splits.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, splits.Body.String(), "JSON string")
}

func TestAnAmountThatDoesNotParseIsNamedByItsField(t *testing.T) {
	var body struct {
		Name  string            `json:"name"`
		Goal  Opt[domain.Money] `json:"goal"`
		Items []FilterItemWrite `json:"items"`
	}
	decode := func(sent string) *invalid {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(sent))
		err := decodeBody(request, &body)
		var refused *invalid
		require.ErrorAs(t, err, &refused)
		return refused
	}

	nested := decode(`{"name": "1250.00", "items": [{"field": "amount", "amount_min": "10.00"},
		{"field": "amount", "amount_max": "$1,000"}]}`)
	require.Equal(t, []string{"body", "items", "amount_max"}, nested.Loc)
	require.Equal(t, `"$1,000" is not an amount such as "1250.00"`, nested.Msg)

	patched := decode(`{"goal": "twelve"}`)
	require.Equal(t, []string{"body", "goal"}, patched.Loc)
	require.Equal(t, `"twelve" is not an amount such as "1250.00"`, patched.Msg)

	number := decode(`{"name": "x", "goal": 19.99}`)
	require.Equal(t, []string{"body", "goal"}, number.Loc)
	require.Contains(t, number.Msg, "JSON string")
}

func TestAnUnknownFieldIsRefusedAndNamed(t *testing.T) {
	// A client that sends a field the schema does not accept is told, rather
	// than having it silently dropped and believing the edit landed.
	l := buildLedger(t)
	response := l.alex.raw(http.MethodPatch, "/accounts/"+l.str("checking"),
		`{"provider_balance": "-1.00"}`)
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "provider_balance")
}

func TestAPatchTellsAbsentFromNullFromAValue(t *testing.T) {
	// Absent leaves the note alone; null clears it. Collapsing the two is how
	// an edit to a payee wipes the note beside it.
	l := buildLedger(t)
	path := "/transactions/" + l.str("august_groceries")

	withNote := l.alex.patch(path, map[string]any{"notes": "check the receipt"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "check the receipt", withNote["notes"])

	untouched := l.alex.patch(path, map[string]any{"payee": "Safeway"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "check the receipt", untouched["notes"], "an absent field was rewritten")

	cleared := l.alex.raw(http.MethodPatch, path, `{"notes": null}`).
		requireStatus(http.StatusOK).json()
	require.Nil(t, cleared["notes"])
}

func TestADateCrossesTheWireAsACalendarDay(t *testing.T) {
	// Never a timestamp: a transaction happens on a day, and a timestamp makes
	// "which month is this in" depend on the reader's zone.
	l := buildLedger(t)
	body := l.alex.get("/transactions/" + l.str("card_charge")).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-08-25", body["date"])
	require.Equal(t, "2026-09-10", body["effective_date"])
}

func TestCORSAnswersOneOriginAndNoOther(t *testing.T) {
	// Not a wildcard and not a reflection: the API is bearer-authenticated and
	// a page that can read a response can read the whole ledger.
	env := NewEnv(testConfig(), nil)
	handler := RouterFor(env)

	allowed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/transactions", nil)
	request.Header.Set("Origin", env.Cfg.FrontendURL)
	handler.ServeHTTP(allowed, request)
	require.Equal(t, env.Cfg.FrontendURL, allowed.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, allowed.Header().Get("Access-Control-Allow-Headers"), "X-Space-Id")

	refused := httptest.NewRecorder()
	other := httptest.NewRequest(http.MethodOptions, "/transactions", nil)
	other.Header.Set("Origin", "https://evil.example")
	handler.ServeHTTP(refused, other)
	require.Empty(t, refused.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, refused.Header().Values("Vary"), "Origin")
}

func TestCORSServesTheSPAsOwnOriginWhateverFrontendURLSays(t *testing.T) {
	// The binary serves the SPA, so a browser on a LAN address FRONTEND_URL
	// does not name is same-origin: its writes carry an Origin header and must
	// reach the handler, which the browser then reads without any CORS header.
	reached := false
	handler := cors("http://localhost:8100")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://192.0.2.7:8100/api/auth/login", nil)
	request.Header.Set("Origin", "http://192.0.2.7:8100")
	handler.ServeHTTP(recorder, request)
	require.True(t, reached)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, recorder.Header().Get("Access-Control-Allow-Origin"))
}
