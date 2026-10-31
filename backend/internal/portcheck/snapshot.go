// Package portcheck captures the authenticated HTTP views used to compare storage engines.
package portcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/api"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

type Response struct {
	Status int `json:"status"`
	Body   any `json:"body"`
}
type Snapshot struct {
	FormatVersion int                            `json:"format_version"`
	Today         string                         `json:"today"`
	Households    map[string]map[string]Response `json:"households"`
	Skipped       map[string]map[string]string   `json:"skipped"`
}

// OfflineConfig retains only local deployment settings; no read can contact a provider.
func OfflineConfig(c *config.Config) *config.Config {
	return &config.Config{Debug: true, SecretKey: c.SecretKey, WebSessionExpiry: time.Hour,
		FrontendURL: "http://localhost:5173", WebAuthnRPName: "Agentifi", LoginMaxAttempts: 20,
		LoginAttemptWindow: 5 * time.Minute, PrimaryCurrency: "USD", DataDir: c.DataDir}
}

var parameter = regexp.MustCompile(`\{([^}]+)\}`)

func skipReason(path string) string {
	if strings.HasSuffix(path, "/connect") {
		return "ephemeral OAuth consent"
	}
	for _, s := range []string{"/sign-in/", "/failure-screenshot", "/download", "/content", "/stream", "/events"} {
		if strings.Contains(path, s) {
			return "streaming, binary, or ephemeral sign-in session"
		}
	}
	if strings.HasPrefix(path, "/imports/simplifi") || strings.HasPrefix(path, "/simplifi-import") {
		return "ephemeral in-process import session"
	}
	return ""
}

// Capture fixes the business clock at noon UTC. No response fields are normalised.
// In particular persisted timestamps, IDs, array order and decimal strings survive unchanged.
func Capture(ctx context.Context, db *store.Store, cfg *config.Config, today time.Time) (*Snapshot, error) {
	today = time.Date(today.Year(), today.Month(), today.Day(), 12, 0, 0, 0, time.UTC)
	env := api.NewEnv(OfflineConfig(cfg), db)
	env.Now = func() time.Time { return today }
	env.Releases = nil
	handler := api.RouterFor(env)
	result := &Snapshot{1, today.Format(time.DateOnly), map[string]map[string]Response{}, map[string]map[string]string{}}
	spaces, err := db.ListSpaces(ctx)
	if err != nil {
		return nil, err
	}
	// A snapshot reads a real data directory, so its sessions end with it.
	var issued []store.Session
	routes := api.RegisteredRoutes()
	sort.Slice(routes, func(i, j int) bool {
		a, b := routes[i].Path(), routes[j].Path()
		na, nb := strings.Count(a, "{"), strings.Count(b, "{")
		if na != nb {
			return na < nb
		}
		return a < b
	})
	for _, space := range spaces {
		memberships, err := db.ListMemberships(ctx, space.ID)
		if err != nil {
			return nil, err
		}
		rank := map[store.Role]int{store.RoleOwner: 0, store.RoleAdmin: 1, store.RoleMember: 2, store.RoleViewer: 3}
		sort.Slice(memberships, func(i, j int) bool {
			a, b := memberships[i], memberships[j]
			if rank[a.Role] != rank[b.Role] {
				return rank[a.Role] < rank[b.Role]
			}
			return a.UserID.String() < b.UserID.String()
		})
		token := ""
		for _, m := range memberships {
			if m.IsAccepted() {
				var session store.Session
				token, session, err = env.Tokens.Issue(ctx, m.UserID, store.SessionWeb, "portcheck")
				issued = append(issued, session)
				break
			}
		}
		if err != nil {
			return nil, err
		}
		if token == "" {
			return nil, fmt.Errorf("household %s has no accepted member", space.ID)
		}
		id := space.ID.String()
		responses := map[string]Response{}
		skipped := map[string]string{}
		result.Households[id] = responses
		result.Skipped[id] = skipped
		request := func(path string) (Response, error) {
			key := "GET " + path
			if v, ok := responses[key]; ok {
				return v, nil
			}
			req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Space-Id", id)
			// Error bodies include the request ID; supply it rather than erase it.
			req.Header.Set("X-Request-Id", "portcheck")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			var body any
			dec := json.NewDecoder(rec.Body)
			dec.UseNumber()
			if err := dec.Decode(&body); err != nil {
				return Response{}, fmt.Errorf("%s %s: non-JSON response (%d): %w", id, path, rec.Code, err)
			}
			v := Response{rec.Code, body}
			responses[key] = v
			return v, nil
		}
		// A household may have frozen plans well outside the report window.
		months := []string{}
		household, err := db.HouseholdDB(ctx, space.ID)
		if err != nil {
			return nil, err
		}
		rows, err := household.Read().QueryContext(ctx, "SELECT month FROM spending_plan_months ORDER BY month")
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var month string
			if err := rows.Scan(&month); err != nil {
				rows.Close()
				return nil, err
			}
			months = append(months, month[:7])
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		preparedParameters := false
		for _, rt := range routes {
			if rt.Method != http.MethodGet || !rt.IsRead() {
				continue
			}
			path := rt.Path()
			if strings.Contains(path, "{") && !preparedParameters {
				preparedParameters = true
				for _, txID := range discover(responses, "/transactions/{transaction_id}", "transaction_id") {
					if _, err := request("/documents?" + url.Values{"transaction_id": {txID}}.Encode()); err != nil {
						return nil, err
					}
				}
			}
			if reason := skipReason(path); reason != "" {
				skipped["GET "+path] = reason
				continue
			}
			paths := []string{path}
			for _, match := range parameter.FindAllStringSubmatch(path, -1) {
				next := []string{}
				for _, p := range paths {
					values := discover(responses, p, match[1])
					if match[1] == "month" {
						values = months
					}
					for _, value := range values {
						next = append(next, strings.ReplaceAll(p, match[0], url.PathEscape(value)))
					}
				}
				paths = next
				if len(paths) == 0 {
					skipped["GET "+path] = "no values discovered for {" + match[1] + "}"
					break
				}
			}
			for _, p := range paths {
				response, err := request(p)
				if err != nil {
					return nil, err
				}
				if body, ok := response.Body.(map[string]any); ok {
					if _, paged := body["items"].([]any); paged {
						for offset := 0; ; offset += 500 {
							page, err := request(p + "?" + url.Values{"limit": {"500"}, "offset": {fmt.Sprint(offset)}}.Encode())
							if err != nil {
								return nil, err
							}
							object, _ := page.Body.(map[string]any)
							items, _ := object["items"].([]any)
							if len(items) < 500 {
								break
							}
						}
					}
				}
			}
		}
		first := time.Date(today.Year(), today.Month(), 1, 12, 0, 0, 0, time.UTC).AddDate(0, -23, 0)
		window := url.Values{"from": {first.Format(time.DateOnly)}, "to": {today.Format(time.DateOnly)}, "date_field": {"effective"}}
		for _, path := range []string{"/transactions/aggregate", "/reports/savings", "/reports/spending"} {
			if _, err := request(path + "?" + window.Encode()); err != nil {
				return nil, err
			}
		}
		reportWindow := url.Values{"from": {first.Format(time.DateOnly)}, "to": {today.Format(time.DateOnly)}, "mode": {"summary"}, "columns": {"time"}, "time_grain": {"month"}}
		if _, err := request("/reports/run?" + reportWindow.Encode()); err != nil {
			return nil, err
		}
		for m := 0; m < 24; m++ {
			start := first.AddDate(0, m, 0)
			end := start.AddDate(0, 1, -1)
			q := url.Values{"from": {start.Format(time.DateOnly)}, "to": {end.Format(time.DateOnly)}}
			if _, err := request("/net-worth?" + q.Encode()); err != nil {
				return nil, err
			}
			if _, err := request("/reports/monthly-summary?month=" + start.Format("2006-01")); err != nil {
				return nil, err
			}
		}
		for _, account := range discover(responses, "/accounts/{account_id}", "account_id") {
			for offset := 0; ; offset += 500 {
				q := url.Values{"account_id": {account}, "limit": {"500"}, "offset": {fmt.Sprint(offset)}, "order": {"asc"}}
				page, err := request("/transactions?" + q.Encode())
				if err != nil {
					return nil, err
				}
				if page.Status != 200 {
					return nil, fmt.Errorf("household %s register: HTTP %d", id, page.Status)
				}
				body, ok := page.Body.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("register page is not an object")
				}
				items, _ := body["items"].([]any)
				if len(items) < 500 {
					break
				}
			}
		}
	}
	for _, session := range issued {
		if err := env.Tokens.Revoke(ctx, session); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func discover(responses map[string]Response, path, param string) []string {
	set := map[string]bool{}
	// A connection ID belongs to the resource that listed it, not another provider.
	prefix := strings.Split(path, "/{")[0]
	collection := map[string]string{"account_id": "/accounts", "transaction_id": "/transactions", "category_id": "/categories", "tag_id": "/tags", "rule_id": "/rules", "series_id": "/series", "goal_id": "/goals", "watchlist_id": "/watchlists", "filter_id": "/filters", "report_id": "/reports", "conversation_id": "/assistant/conversations", "automation_id": "/assistant-automations", "run_id": "/assistant-automations/runs", "action_id": "/assistant-actions", "subaccount_id": "/bills/subaccounts", "bill_id": "/bills/statements", "challenge_id": "/bills/challenges"}[param]
	if param == "id" || param == "order_id" || param == "message_id" || param == "document_id" || param == "holding_id" || param == "institution_id" || param == "batch_id" {
		collection = prefix
	}
	if param == "id" && (strings.HasPrefix(path, "/refunds/transactions/") || strings.HasPrefix(path, "/merchants/transactions/")) {
		collection = "/transactions"
	}
	if param == "security_id" {
		collection = "/securities"
	}
	if param == "account_id" && strings.HasPrefix(path, "/merchants/") {
		collection = prefix
	}
	if param == "connection_id" {
		collection = prefix
	}
	if param == "guidance_id" {
		collection = "/guidance"
	}
	if param == "action_id" {
		collection = "/assistant-automations/pending"
	}
	if param == "batch_id" {
		collection = "/category-suggestion-batches/latest"
	}
	leaf := collection[strings.LastIndex(collection, "/")+1:]
	var visit func(any, bool)
	visit = func(v any, own bool) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				visit(item, own)
			}
		case map[string]any:
			if own {
				if id, ok := x["id"].(string); ok && id != "" {
					set[id] = true
					// An entity's nested items (filter clauses, order lines, etc.)
					// are not members of the collection that listed the entity.
					own = false
				}
			}
			if id, ok := x[param].(string); collection == "" && ok && id != "" && param != "id" {
				set[id] = true
			}
			for k, value := range x {
				visit(value, own && (k == "items" || k == "results" || k == leaf || k == "batch"))
			}
		}
	}
	for key, response := range responses {
		requestPath := strings.Split(strings.TrimPrefix(key, "GET "), "?")[0]
		if collection != "" && requestPath != collection {
			continue
		}
		visit(response.Body, collection != "")
	}
	values := make([]string, 0, len(set))
	for id := range set {
		values = append(values, id)
	}
	sort.Strings(values)
	return values
}
