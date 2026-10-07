package merchants

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The session a Camoufox sign-in at Costco keeps, read out of the page's MSAL
// cache. Every token here is invented.

// inventedJWT is a token whose payload is claims; the signature is nothing.
func inventedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// The session is built from the MSAL cache field for field, in the shape the
// pull reads.
func TestACostcoSessionIsBuiltFromTheMSALCache(t *testing.T) {
	idToken := inventedJWT(t, map[string]any{
		"iss": "https://signin.example.test/11111111-2222-3333-4444-555555555555/v2.0/",
		"tfp": "B2C_1A_INVENTED_POLICY",
		"aud": "aud-from-the-token",
	})
	cache := CostcoMSALCache{
		Refresh: `{"secret":"invented-refresh","realm":"realm-tenant","clientId":"invented-client","homeAccountId":"home-1"}`,
		ID:      `{"secret":"` + idToken + `"}`,
		Account: `{"username":"someone@example.test","name":"Alex Example","homeAccountId":"home-2"}`,
	}
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	session, found := CostcoSessionFromCache(cache, at)

	require.True(t, found)
	require.JSONEq(t, `{
	  "kind": "costco-b2c",
	  "tenant": "11111111-2222-3333-4444-555555555555",
	  "policy": "B2C_1A_INVENTED_POLICY",
	  "client_id": "invented-client",
	  "refresh_token": "invented-refresh",
	  "id_token": "`+idToken+`",
	  "account": {"username": "someone@example.test", "name": "Alex Example", "home_account_id": "home-2"},
	  "handed_over_at": "2026-09-23T12:00:00Z"
	}`, string(session))
	require.True(t, isCostcoSession(session), "the pull reads it as the session it takes")
}

// With nothing but a refresh token, the rest falls back: the realm, the
// built-in policy, the token's audience.
func TestACostcoSessionFallsBackWhereTheCacheIsThin(t *testing.T) {
	cache := CostcoMSALCache{
		Refresh: `{"secret":"invented-refresh","realm":"realm-tenant"}`,
		IDToken: inventedJWT(t, map[string]any{"aud": []any{"aud-from-the-token"}}),
	}

	session, found := CostcoSessionFromCache(cache, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	require.True(t, found)
	var shape costcoSession
	require.NoError(t, json.Unmarshal(session, &shape))
	require.Equal(t, "realm-tenant", shape.Tenant)
	require.Equal(t, costcoB2CPolicy, shape.Policy)
	require.Equal(t, "aud-from-the-token", shape.ClientID)
	require.Equal(t, cache.IDToken, shape.IDToken)
}

func TestACacheWithNoRefreshTokenIsNoSession(t *testing.T) {
	for _, cache := range []CostcoMSALCache{
		{},
		{Refresh: `not json`},
		{Refresh: `{"secret":""}`},
	} {
		_, found := CostcoSessionFromCache(cache, time.Now())
		require.False(t, found)
	}
}
