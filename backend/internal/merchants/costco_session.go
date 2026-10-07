package merchants

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// Costco's endpoints answer a request sent straight from the server with an
// HTTP/2 INTERNAL_ERROR, so every handed-over session's calls are made from a
// Camoufox page at www.costco.com, the origin both endpoints allow.

// The page the calls are made from: a document that runs no script, so the
// call is the browser's own fetch.
const costcoFetchDocument = "/robots.txt"

func (m *costcoModule) RunsInFirefox() bool { return true }

func (m *costcoModule) FirefoxOrigin() (string, string) {
	return costcoHome, costcoFetchDocument
}

// costcoMSALKeys are the entries of MSAL's cache that CostcoSessionFromCache
// reads.
const costcoMSALKeys = `^(idToken|azure_token)$|-refreshtoken-|-idtoken-|signin\.costco\.com-$`

var (
	costcoMSALRefresh = regexp.MustCompile(`(?i)-refreshtoken-`)
	costcoMSALID      = regexp.MustCompile(`(?i)-idtoken-`)
	costcoMSALAccount = regexp.MustCompile(`(?i)signin\.costco\.com-$`)
)

// costcoMSALCache sorts MSAL's entries by key, as raw strings: what they mean
// is decided in CostcoSessionFromCache.
func costcoMSALCache(entries []browser.StorageEntry) CostcoMSALCache {
	var cache CostcoMSALCache
	for _, entry := range entries {
		switch {
		case entry.Key == "idToken":
			cache.IDToken = entry.Value
		case entry.Key == "azure_token":
			cache.AzureToken = entry.Value
		case costcoMSALRefresh.MatchString(entry.Key):
			cache.Refresh = entry.Value
		case costcoMSALID.MatchString(entry.Key):
			cache.ID = entry.Value
		case costcoMSALAccount.MatchString(entry.Key):
			cache.Account = entry.Value
		}
	}
	return cache
}

// CostcoMSALCache is what MSAL left in a signed-in costco.com tab.
type CostcoMSALCache struct {
	Refresh    string `json:"refresh"`
	ID         string `json:"id"`
	Account    string `json:"account"`
	IDToken    string `json:"id_token"`
	AzureToken string `json:"azure_token"`
}

// SessionFromPage needs the page at costco.com, where MSAL keeps its cache;
// CompleteSignIn stands on the landing page first.
func (m *costcoModule) SessionFromPage(page browser.Page, at time.Time) (json.RawMessage, bool, error) {
	entries, err := browser.ReadStorage(page, []string{"localStorage"}, costcoMSALKeys)
	if err != nil {
		return nil, false, err
	}
	session, found := CostcoSessionFromCache(costcoMSALCache(entries), at)
	return session, found, nil
}

var costcoIssuerTenant = regexp.MustCompile(`(?i)/([0-9a-f-]{36})/`)

// CostcoSessionFromCache is false when the cache holds no refresh token.
func CostcoSessionFromCache(cache CostcoMSALCache, at time.Time) (json.RawMessage, bool) {
	var refresh struct {
		Secret        string `json:"secret"`
		Realm         string `json:"realm"`
		ClientID      string `json:"clientId"`
		HomeAccountID string `json:"homeAccountId"`
	}
	if json.Unmarshal([]byte(cache.Refresh), &refresh) != nil || refresh.Secret == "" {
		return nil, false
	}
	var id struct {
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal([]byte(cache.ID), &id)
	var account struct {
		Username      string `json:"username"`
		Name          string `json:"name"`
		HomeAccountID string `json:"homeAccountId"`
	}
	_ = json.Unmarshal([]byte(cache.Account), &account)

	idToken := cmp.Or(id.Secret, cache.IDToken)
	claims := jwtClaims(idToken)
	tenant := ""
	if found := costcoIssuerTenant.FindStringSubmatch(text(claims["iss"])); found != nil {
		tenant = found[1]
	}
	audience := text(claims["aud"])
	if list, ok := claims["aud"].([]any); ok && len(list) > 0 {
		audience = text(list[0])
	}

	type handedAccount struct {
		Username      string `json:"username,omitempty"`
		Name          string `json:"name,omitempty"`
		HomeAccountID string `json:"home_account_id,omitempty"`
	}
	session := struct {
		Kind         string        `json:"kind"`
		Tenant       string        `json:"tenant"`
		Policy       string        `json:"policy"`
		ClientID     string        `json:"client_id"`
		RefreshToken string        `json:"refresh_token"`
		IDToken      string        `json:"id_token"`
		AzureToken   string        `json:"azure_token,omitempty"`
		Account      handedAccount `json:"account"`
		HandedOverAt string        `json:"handed_over_at"`
	}{
		Kind:         costcoSessionKind,
		Tenant:       cmp.Or(tenant, refresh.Realm, costcoB2CTenant),
		Policy:       cmp.Or(text(claims["tfp"]), text(claims["acr"]), costcoB2CPolicy),
		ClientID:     cmp.Or(refresh.ClientID, audience),
		RefreshToken: refresh.Secret,
		IDToken:      idToken,
		AzureToken:   cache.AzureToken,
		Account: handedAccount{
			Username: account.Username, Name: account.Name,
			HomeAccountID: cmp.Or(account.HomeAccountID, refresh.HomeAccountID),
		},
		HandedOverAt: at.UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(session)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// jwtClaims is a JWT's payload, unverified: it is read for where the token
// came from, never for whether to trust it. Anything unreadable is no claims.
func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return map[string]any{}
	}
	claims := map[string]any{}
	if json.Unmarshal(raw, &claims) != nil {
		return map[string]any{}
	}
	return claims
}
