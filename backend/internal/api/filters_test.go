package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func TestAFilterItemThatMatchesNothingOrEverythingIsRefusedAndNamed(t *testing.T) {
	t.Parallel()
	money := func(text string) *domain.Money {
		amount := domain.MustFromString(text)
		return &amount
	}
	refused := func(write FilterItemWrite) *invalid {
		t.Helper()
		_, err := filterItemsFrom([]FilterItemWrite{write})
		var got *invalid
		require.ErrorAs(t, err, &got)
		return got
	}

	inverted := refused(FilterItemWrite{Field: domain.FieldAmount, Operator: domain.OpBetween,
		AmountMin: money("80.00"), AmountMax: money("25.00")})
	require.Equal(t, []string{"body", "items", "amount_min"}, inverted.Loc)
	require.Equal(t, "amount_min 80.00 is greater than amount_max 25.00", inverted.Msg)

	unbounded := refused(FilterItemWrite{Field: domain.FieldAmount, Operator: domain.OpBetween})
	require.Equal(t, []string{"body", "items", "amount_min"}, unbounded.Loc)
	require.Contains(t, unbounded.Msg, "needs amount_min, amount_max or both")

	noPayee := refused(FilterItemWrite{Field: domain.FieldPayee, Operator: domain.OpIn,
		ValueTexts: []string{" "}})
	require.Equal(t, []string{"body", "items", "value_texts"}, noPayee.Loc)
	require.Equal(t, "a payee condition needs at least one value", noPayee.Msg)

	noCategory := refused(FilterItemWrite{Field: domain.FieldCategory})
	require.Equal(t, []string{"body", "items", "value_ids"}, noCategory.Loc)
	require.Equal(t, "a category condition needs at least one category; "+
		"if the ones it named were deleted, remove the condition or choose another", noCategory.Msg)

	expense := false
	for _, fine := range []FilterItemWrite{
		{Field: domain.FieldAmount, Operator: domain.OpBetween,
			AmountMin: money("1.00"), AmountMax: money("1.00")},
		{Field: domain.FieldAmount, Operator: domain.OpBetween,
			AmountMin: money("-15.00"), AmountMax: money("40.00")},
		{Field: domain.FieldAmount, Operator: domain.OpBetween, AmountMin: money("15.00")},
		{Field: domain.FieldAmount, Operator: domain.OpBetween, State: &expense},
		{Field: domain.FieldAmount, Operator: domain.OpEquals, State: &expense},
		{Field: domain.FieldHasTag, Operator: domain.OpIn},
		{Field: domain.FieldCategory, ValueIDs: []uuid.UUID{uuid.New()}},
	} {
		_, err := filterItemsFrom([]FilterItemWrite{fine})
		require.NoError(t, err, "%+v", fine)
	}
}

func TestEveryFilterWriterRefusesAnInvertedRange(t *testing.T) {
	t.Parallel()
	l := planLedger(t)
	inverted := []map[string]any{{
		"field": "amount", "operator": "between", "amount_min": "80.00", "amount_max": "25.00",
	}}
	requireLoc := func(response *response, loc ...string) {
		t.Helper()
		response.requireStatus(http.StatusUnprocessableEntity)
		detail := response.json()["detail"].([]any)[0].(map[string]any)
		require.Equal(t, append([]any{"body"}, toAny(loc)...), detail["loc"])
	}

	requireLoc(l.alex.post("/filters", map[string]any{
		"name": "Mid-size", "scope": "saved_view", "items": inverted,
	}), "items", "amount_min")
	requireLoc(l.alex.post("/watchlists", map[string]any{
		"name": "Mid-size", "items": inverted,
	}), "items", "amount_min")
	requireLoc(l.alex.post("/watchlists", map[string]any{
		"name": "Nobody", "items": []map[string]any{{"field": "payee", "operator": "in"}},
	}), "items", "value_texts")
}

// Every route that builds filter items answers a bad id as the caller's
// mistake: 409 for one this household does not hold, 422 for ids on a field
// that takes none. has_tag takes none: "has one of these tags" is a tag item.
func TestEveryFilterWriterRefusesAnIDItCannotStore(t *testing.T) {
	t.Parallel()
	l := planLedger(t).withOtherHousehold()
	allowWrites(l, newFakeModel(t, answerReply("ok")))
	created := func(path string, body map[string]any) string {
		t.Helper()
		return l.alex.post(path, body).requireStatus(http.StatusCreated).json()["id"].(string)
	}
	report := map[string]any{"mode": "transaction", "rows": "category", "sign": "expenses"}
	filter := created("/filters", map[string]any{"name": "Corner", "scope": "saved_view",
		"items": []any{payeeItem("Corner Store")}})
	watchlist := groceryWatchlist(l, nil)["id"].(string)
	saved := created("/reports", map[string]any{"name": "Groceries", "config": report})
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{"set_payee": "Corner Store"})["id"].(string)
	note := created("/guidance", fuelStopNote(nil))
	automation := created("/assistant-automations", suggestCategoriesBody(nil))

	writers := []struct {
		method, path string
		body         func(items []map[string]any) map[string]any
	}{
		{http.MethodPost, "/filters", func(items []map[string]any) map[string]any {
			return map[string]any{"name": "Bad", "scope": "saved_view", "items": items}
		}},
		{http.MethodPatch, "/filters/" + filter, func(items []map[string]any) map[string]any {
			return map[string]any{"name": "Renamed", "items": items}
		}},
		{http.MethodPost, "/watchlists", func(items []map[string]any) map[string]any {
			return map[string]any{"name": "Bad", "items": items}
		}},
		{http.MethodPatch, "/watchlists/" + watchlist, func(items []map[string]any) map[string]any {
			return map[string]any{"items": items}
		}},
		{http.MethodPost, "/reports", func(items []map[string]any) map[string]any {
			return map[string]any{"name": "Bad", "config": report, "items": items}
		}},
		{http.MethodPatch, "/reports/" + saved, func(items []map[string]any) map[string]any {
			return map[string]any{"name": "Renamed", "items": items}
		}},
		{http.MethodPost, "/rules", func(items []map[string]any) map[string]any {
			return map[string]any{"name": "Bad", "conditions": items,
				"actions": map[string]any{"set_is_reviewed": true}}
		}},
		{http.MethodPatch, "/rules/" + rule, func(items []map[string]any) map[string]any {
			return map[string]any{"conditions": items}
		}},
		{http.MethodPost, "/guidance", func(items []map[string]any) map[string]any {
			return fuelStopNote(map[string]any{"name": "Bad", "conditions": items})
		}},
		{http.MethodPatch, "/guidance/" + note, func(items []map[string]any) map[string]any {
			return map[string]any{"conditions": items}
		}},
		{http.MethodPost, "/assistant-automations", func(items []map[string]any) map[string]any {
			return suggestCategoriesBody(map[string]any{"name": "Bad", "conditions": items})
		}},
		{http.MethodPatch, "/assistant-automations/" + automation, func(items []map[string]any) map[string]any {
			return map[string]any{"conditions": items}
		}},
	}

	missing := uuid.NewString()
	refusals := []struct {
		name   string
		item   map[string]any
		status int
		detail string
	}{
		{"a category naming no row", map[string]any{"field": "category", "value_ids": []string{missing}},
			http.StatusConflict, "category " + missing + " is not in this space"},
		{"another household's category", map[string]any{"field": "category", "value_ids": []string{l.str("groceries"), l.str("stranger_category")}},
			http.StatusConflict, "category " + l.str("stranger_category") + " is not in this space"},
		{"another household's tag", map[string]any{"field": "tag", "value_ids": []string{l.str("stranger_tag")}},
			http.StatusConflict, "tag " + l.str("stranger_tag") + " is not in this space"},
		{"another household's account", map[string]any{"field": "account", "value_ids": []string{l.str("stranger_account")}},
			http.StatusConflict, "account " + l.str("stranger_account") + " is not in this space"},
		{"ids on a payee item", map[string]any{"field": "payee", "value_texts": []string{"Corner Store"}, "value_ids": []string{l.str("groceries")}},
			http.StatusUnprocessableEntity, "a payee condition takes no value_ids"},
		{"ids on a has_tag item", map[string]any{"field": "has_tag", "value_ids": []string{l.str("tag")}},
			http.StatusUnprocessableEntity, "a has_tag condition takes no value_ids"},
	}
	for _, writer := range writers {
		for _, refusal := range refusals {
			c := l.alex
			var got *response
			body := writer.body([]map[string]any{refusal.item})
			if writer.method == http.MethodPost {
				got = c.post(writer.path, body)
			} else {
				got = c.patch(writer.path, body)
			}
			got.requireStatus(refusal.status)
			detail := got.json()["detail"]
			if refusal.status == http.StatusUnprocessableEntity {
				first := detail.([]any)[0].(map[string]any)
				require.Equal(t, []any{"body", "items", "value_ids"}, first["loc"], "%s %s: %s", writer.method, writer.path, refusal.name)
				detail = first["msg"]
			}
			require.Equal(t, refusal.detail, detail, "%s %s: %s", writer.method, writer.path, refusal.name)
		}
	}

	// Refused before anything was written: the name in the same PATCH stays.
	require.Equal(t, "Corner", l.alex.get("/filters/" + filter).requireStatus(http.StatusOK).json()["name"])
	require.Equal(t, "Groceries", l.alex.get("/reports/" + saved).requireStatus(http.StatusOK).json()["name"])

	// A deleted category is still this household's: a filter that names one
	// saves again unchanged.
	gone := created("/categories", map[string]any{"name": "Retired", "kind": "expense"})
	l.alex.del("/categories/" + gone).requireStatus(http.StatusNoContent)
	kept := l.alex.post("/filters", map[string]any{"scope": "saved_view", "items": []map[string]any{
		{"field": "category", "value_ids": []string{gone, l.str("groceries"), l.str("groceries")}},
		{"field": "has_tag", "position": 1},
	}}).requireStatus(http.StatusCreated).json()
	require.Equal(t, []any{gone, l.str("groceries"), l.str("groceries")},
		kept["items"].([]any)[0].(map[string]any)["value_ids"])
}

func toAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func TestAWatchlistPeriodIsOneOfTheSet(t *testing.T) {
	t.Parallel()
	l := planLedger(t)

	defaulted := groceryWatchlist(l, nil)
	require.Equal(t, "month", defaulted["period"])

	refused := l.alex.post("/watchlists", map[string]any{
		"name": "Groceries", "filter_id": l.str("filter"), "period": "fortnight",
	}).requireStatus(http.StatusUnprocessableEntity).json()
	detail := refused["detail"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"body", "period"}, detail["loc"])
	require.Contains(t, detail["msg"], `"fortnight" is not a watchlist period`)

	path := "/watchlists/" + defaulted["id"].(string)
	l.alex.patch(path, map[string]any{"period": "weekly"}).
		requireStatus(http.StatusUnprocessableEntity)
	quarterly := l.alex.patch(path, map[string]any{"period": "quarterly"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "quarter", quarterly["period"])
}

func TestAPrunedAdHocFilterIsRefusedByEveryReader(t *testing.T) {
	t.Parallel()
	l := planLedger(t)
	search := l.alex.post("/filters", map[string]any{
		"scope": "ad_hoc", "items": []any{payeeItem("Safeway")},
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	registerPage(l, "filter_id="+search)

	pruned, err := l.env.DB.PruneAdHocFilters(t.Context(), time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.GreaterOrEqual(t, pruned, int64(1))

	for _, path := range []string{
		"/transactions?filter_id=" + search,
		"/reports/run?filter_id=" + search,
	} {
		refused := l.alex.get(path).requireStatus(http.StatusConflict).json()
		require.Equal(t, "filter "+search+" is not in this space", refused["detail"], path)
	}
}

func TestAStoredRowTheWritersWouldRefuseStillLoads(t *testing.T) {
	t.Parallel()
	l := planLedger(t)
	space := store.SpaceID(l.id("space"))
	filter := &store.Filter{Name: "Kept", Scope: "saved_view", Items: []store.FilterItem{
		{Field: string(domain.FieldPayee), Operator: string(domain.OpIn),
			ValueIDs: []uuid.UUID{}, ValueTexts: []string{}},
		{Field: string(domain.FieldAmount), Operator: string(domain.OpBetween), Position: 1,
			ValueIDs: []uuid.UUID{}, ValueTexts: []string{},
			AmountMin: domain.MustFromString("30.00"), HasAmountMin: true,
			AmountMax: domain.MustFromString("10.00"), HasAmountMax: true},
	}}
	require.NoError(t, l.env.DB.CreateFilter(t.Context(), space, filter))
	watchlist := &store.Watchlist{FilterID: filter.ID, Name: "Kept", Period: "fortnightly"}
	require.NoError(t, l.env.DB.CreateWatchlist(t.Context(), space, watchlist))

	loaded := l.alex.get("/filters/" + filter.ID.String()).requireStatus(http.StatusOK).json()
	require.Len(t, loaded["items"], 2)
	card := l.alex.get("/watchlists/" + watchlist.ID.String()).requireStatus(http.StatusOK).json()
	require.Equal(t, "fortnightly", card["period"])
	l.alex.patch("/watchlists/"+watchlist.ID.String(), map[string]any{"name": "Still kept"}).
		requireStatus(http.StatusOK)
}

func TestSavedViewsListInTheOrderTheirPositionsGive(t *testing.T) {
	t.Parallel()
	l := planLedger(t)
	save := func(name string, position int) string {
		t.Helper()
		return l.alex.post("/filters", map[string]any{
			"name": name, "scope": "saved_view", "position": position,
			"items": []any{payeeItem("Corner Store")},
		}).requireStatus(http.StatusCreated).json()["id"].(string)
	}
	names := func() []string {
		t.Helper()
		var out []string
		for _, one := range l.alex.get("/filters?scope=saved_view").requireStatus(http.StatusOK).list() {
			require.Equal(t, "saved_view", one["scope"])
			out = append(out, one["name"].(string))
		}
		return out
	}

	coffee := save("Coffee", 1)
	save("Fuel", 0)
	lunch := save("Lunch", 1)
	l.alex.post("/filters", map[string]any{"scope": "ad_hoc", "items": []any{payeeItem("Corner Store")}}).
		requireStatus(http.StatusCreated)
	require.Equal(t, []string{"Fuel", "Coffee", "Lunch"}, names())

	l.alex.patch("/filters/"+lunch, map[string]any{"position": 0}).requireStatus(http.StatusOK)
	l.alex.patch("/filters/"+coffee, map[string]any{"name": "Cafes", "position": 2}).
		requireStatus(http.StatusOK)
	require.Equal(t, []string{"Fuel", "Lunch", "Cafes"}, names())

	l.alex.del("/filters/" + lunch).requireStatus(http.StatusNoContent)
	require.Equal(t, []string{"Fuel", "Cafes"}, names())

	refused := l.alex.get("/filters?scope=everything").
		requireStatus(http.StatusUnprocessableEntity).json()
	detail := refused["detail"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"query", "scope"}, detail["loc"])
}
