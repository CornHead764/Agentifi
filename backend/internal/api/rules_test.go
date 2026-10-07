package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Rules, end to end.
//
// The properties worth a test here are the ones that make the feature safe to
// trust: a preview that writes nothing, an apply that does exactly what the
// preview said and nothing that arrived since, an order that decides which of
// two rules wins, two exclusion flags that stay two, and a rule that is
// invisible from another household.

func newRule(l *ledger, body map[string]any) map[string]any {
	l.t.Helper()
	return l.alex.post("/rules", body).requireStatus(http.StatusCreated).json()
}

// statementRule matches on the bank's string, which is what a rule should read:
// the rename action rewrites the payee, so a payee-matched rule stops firing on
// its own output.
func statementRule(l *ledger, name, keyword string, actions map[string]any) map[string]any {
	l.t.Helper()
	return newRule(l, map[string]any{
		"name": name,
		"conditions": []map[string]any{{
			"field":       string(domain.FieldStatementName),
			"operator":    string(domain.OpContains),
			"value_texts": []string{keyword},
		}},
		"actions": actions,
	})
}

func ruleID(t *testing.T, rule map[string]any) string {
	t.Helper()
	id, ok := rule["id"].(string)
	require.True(t, ok, "rule has no id: %v", rule)
	return id
}

func preview(l *ledger, rule map[string]any) map[string]any {
	l.t.Helper()
	return l.alex.get("/rules/" + ruleID(l.t, rule) + "/preview").
		requireStatus(http.StatusOK).json()
}

// changedIDs is what the user approved, and is what the apply is bounded to.
func changedIDs(t *testing.T, preview map[string]any) []string {
	t.Helper()
	var out []string
	for _, raw := range preview["changes"].([]any) {
		out = append(out, raw.(map[string]any)["transaction_id"].(string))
	}
	return out
}

func reloadTxn(l *ledger, id uuid.UUID) store.Transaction {
	l.t.Helper()
	txn, err := l.env.DB.GetTransaction(l.t.Context(), store.SpaceIDOf(l.id("space")), id)
	require.NoError(l.t, err)
	return txn
}

// otherSpace is Bob, who owns a different household entirely.
func otherSpace(l *ledger) *client {
	l.t.Helper()
	return (&client{t: l.t, env: l.env, handler: RouterFor(l.env)}).
		as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
}

// --- The conditions are the one filter -----------------------------------------

func TestARuleStoresItsConditionsAsAFilter(t *testing.T) {
	l := buildLedger(t)
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{"set_payee": "Corner Store"})

	require.True(t, rule["owns_filter"].(bool))
	filter := rule["filter"].(map[string]any)
	require.Equal(t, rule["filter_id"], filter["id"])
	items := filter["items"].([]any)
	require.Len(t, items, 1)
	require.Equal(t, string(domain.FieldStatementName), items[0].(map[string]any)["field"])

	// The same row is readable through /filters, because it is the same row.
	stored := l.alex.get("/filters/" + filter["id"].(string)).requireStatus(http.StatusOK).json()
	require.Equal(t, RuleFilterScope, stored["scope"])
}

func TestARuleWithNoConditionsIsRefused(t *testing.T) {
	// An empty filter on the register is the unfiltered register; on a rule it
	// would recategorize every row the user owns.
	l := buildLedger(t)
	l.alex.post("/rules", map[string]any{
		"name": "Everything", "conditions": []map[string]any{},
		"actions": map[string]any{"set_is_reviewed": true},
	}).requireStatus(http.StatusUnprocessableEntity)

	// The same refusal for a saved filter that happens to have no items: a
	// filter panel with no facets chosen is the whole register.
	empty := l.alex.post("/filters", map[string]any{"name": "Everything"}).
		requireStatus(http.StatusCreated).json()
	l.alex.post("/rules", map[string]any{
		"name": "Everything", "filter_id": empty["id"],
		"actions": map[string]any{"set_is_reviewed": true},
	}).requireStatus(http.StatusConflict)
}

func TestARuleWithNoActionsIsRefused(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/rules", map[string]any{
		"name": "Does nothing",
		"conditions": []map[string]any{{
			"field": string(domain.FieldStatementName), "operator": string(domain.OpContains),
			"value_texts": []string{"CORNER"},
		}},
		"actions": map[string]any{},
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestARuleCannotEditASharedFiltersConditions(t *testing.T) {
	// The seeded filter backs a watchlist. Replacing its items from the rules
	// screen would rewrite that watchlist with nothing here to say so.
	l := buildLedger(t)
	rule := newRule(l, map[string]any{
		"name": "Groceries", "filter_id": l.str("filter"),
		"actions": map[string]any{"set_is_reviewed": true},
	})
	require.False(t, rule["owns_filter"].(bool))

	l.alex.patch("/rules/"+ruleID(t, rule), map[string]any{
		"conditions": []map[string]any{{
			"field": string(domain.FieldStatementName), "operator": string(domain.OpContains),
			"value_texts": []string{"SAFEWAY"},
		}},
	}).requireStatus(http.StatusConflict)
}

func TestARuleCannotMatchOnTheSeriesMatchersVerdict(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/rules", map[string]any{
		"name": "Bills",
		"conditions": []map[string]any{{
			"field":    string(domain.FieldIsBillOrSubscription),
			"operator": string(domain.OpIsTrue), "state": true,
		}},
		"actions": map[string]any{"set_is_reviewed": true},
	}).requireStatus(http.StatusUnprocessableEntity)
}

// --- Priority ------------------------------------------------------------------

func TestANewRuleLandsLastAndTheListRunsInOrder(t *testing.T) {
	l := buildLedger(t)
	first := statementRule(l, "First", "CORNER", map[string]any{"set_payee": "First"})
	second := statementRule(l, "Second", "CORNER", map[string]any{"set_payee": "Second"})

	require.Equal(t, float64(0), first["priority"])
	require.Equal(t, float64(1), second["priority"])

	listed := l.alex.get("/rules").requireStatus(http.StatusOK).list()
	require.Equal(t, []any{first["id"], second["id"]},
		[]any{listed[0]["id"], listed[1]["id"]})
}

func TestReorderDecidesWhichOfTwoRenamesWins(t *testing.T) {
	// Two rules renaming the same row have to resolve the same way on every
	// run, and the order on this screen is what decides it.
	l := buildLedger(t)
	first := statementRule(l, "First", "CORNER", map[string]any{"set_payee": "First"})
	second := statementRule(l, "Second", "CORNER", map[string]any{"set_payee": "Second"})

	spaceID := store.SpaceIDOf(l.id("space"))
	fresh := seedTxn(l, "corner_two", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 21),
		Amount: domain.MustFromString("-11.00"), StatementName: "CORNER STORE",
	})
	_, err := service.NewRules(l.env.DB).RunRules(t.Context(), spaceID, []uuid.UUID{fresh})
	require.NoError(t, err)
	require.Equal(t, "First", reloadTxn(l, fresh).Payee)

	reordered := l.alex.post("/rules/reorder", map[string]any{
		"rule_ids": []any{second["id"], first["id"]},
	}).requireStatus(http.StatusOK).list()
	require.Equal(t, second["id"], reordered[0]["id"])
	require.Equal(t, float64(0), reordered[0]["priority"])

	again := seedTxn(l, "corner_three", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 22),
		Amount: domain.MustFromString("-12.00"), StatementName: "CORNER STORE",
	})
	_, err = service.NewRules(l.env.DB).RunRules(t.Context(), spaceID, []uuid.UUID{again})
	require.NoError(t, err)
	require.Equal(t, "Second", reloadTxn(l, again).Payee)
}

func TestAPartialReorderIsRefused(t *testing.T) {
	l := buildLedger(t)
	first := statementRule(l, "First", "CORNER", map[string]any{"set_payee": "First"})
	statementRule(l, "Second", "CORNER", map[string]any{"set_payee": "Second"})

	l.alex.post("/rules/reorder", map[string]any{
		"rule_ids": []any{first["id"]},
	}).requireStatus(http.StatusConflict)
	l.alex.post("/rules/reorder", map[string]any{
		"rule_ids": []any{first["id"], first["id"]},
	}).requireStatus(http.StatusConflict)
}

// --- Preview and apply -----------------------------------------------------------

func TestAPreviewWritesNothing(t *testing.T) {
	l := buildLedger(t)
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{
		"set_payee":       "Corner Coffee",
		"set_category_id": l.str("groceries"),
		"add_tag_ids":     []string{l.str("tag")},
		"set_notes":       "from a rule",
	})

	body := preview(l, rule)
	require.Equal(t, float64(1), body["matched"])
	require.Equal(t, float64(1), body["changed"])
	require.Equal(t, float64(0), body["unchanged"])

	after := reloadTxn(l, l.id("august_corner"))
	require.Equal(t, "Corner Store", after.Payee)
	require.Equal(t, uuid.Nil, after.CategoryID)
	require.Equal(t, "", after.Notes)
	require.Empty(t, after.TagIDs)
	require.Equal(t, uuid.Nil, after.RuleID)
}

func TestApplyChangesExactlyWhatThePreviewPromised(t *testing.T) {
	l := buildLedger(t)
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{
		"set_payee":       "Corner Coffee",
		"set_category_id": l.str("groceries"),
		"add_tag_ids":     []string{l.str("tag")},
		"set_notes":       "from a rule",
	})

	body := preview(l, rule)
	changes := body["changes"].([]any)
	require.Len(t, changes, 1)
	promised := changes[0].(map[string]any)
	require.Equal(t, l.str("august_corner"), promised["transaction_id"])
	require.Equal(t, "CORNER STORE", promised["statement_name"])
	// The payee before the rename is on the row too, so the diff is readable.
	require.Equal(t, "Corner Store", promised["payee"])
	actions := promised["actions"].(map[string]any)
	require.Equal(t, "Corner Coffee", actions["set_payee"])
	require.Equal(t, l.str("groceries"), actions["set_category_id"])
	require.Equal(t, "from a rule", actions["set_notes"])

	applied := l.alex.post("/rules/"+ruleID(t, rule)+"/apply", map[string]any{
		"transaction_ids": changedIDs(t, body),
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), applied["applied"])

	after := reloadTxn(l, l.id("august_corner"))
	require.Equal(t, "Corner Coffee", after.Payee)
	require.Equal(t, l.id("groceries"), after.CategoryID)
	require.Equal(t, "from a rule", after.Notes)
	require.Equal(t, []uuid.UUID{l.id("tag")}, after.TagIDs)
	require.Equal(t, uuid.MustParse(ruleID(t, rule)), after.RuleID)

	// Nothing the preview did not name moved.
	untouched := reloadTxn(l, l.id("august_groceries"))
	require.Equal(t, "Safeway", untouched.Payee)
	require.Equal(t, uuid.Nil, untouched.RuleID)
}

func TestAnApplyDoesNotSweepInWhatArrivedSinceThePreview(t *testing.T) {
	// The user approved a list. A row that landed between the two calls is not
	// on it, and re-deriving the diff over the whole ledger would take it too.
	l := buildLedger(t)
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{"set_payee": "Corner Coffee"})
	body := preview(l, rule)
	approved := changedIDs(t, body)
	require.Len(t, approved, 1)

	latecomer := seedTxn(l, "corner_late", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 21),
		Amount: domain.MustFromString("-8.00"), StatementName: "CORNER STORE ANNEX",
	})
	applied := l.alex.post("/rules/"+ruleID(t, rule)+"/apply", map[string]any{
		"transaction_ids": approved,
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, float64(1), applied["applied"])
	require.Equal(t, "", reloadTxn(l, latecomer).Payee)
}

func TestASecondPreviewAfterApplyPromisesNothing(t *testing.T) {
	l := buildLedger(t)
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{"set_payee": "Corner Coffee"})
	l.alex.post("/rules/"+ruleID(t, rule)+"/apply", map[string]any{
		"transaction_ids": changedIDs(t, preview(l, rule)),
	}).requireStatus(http.StatusOK)

	again := preview(l, rule)
	// Still matched, because matching reads the bank string and the rule only
	// rewrote the payee. Ground rule 5: a rename does not move the goalpost.
	require.Equal(t, float64(1), again["matched"])
	require.Equal(t, float64(0), again["changed"])
	require.Equal(t, float64(1), again["unchanged"])
	require.Empty(t, again["changes"])
}

func TestAViewerMayPreviewButNotApply(t *testing.T) {
	l := buildLedger(t)
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{"set_payee": "Corner Coffee"})
	vera := l.as("vera")

	vera.get("/rules/" + ruleID(t, rule) + "/preview").requireStatus(http.StatusOK)
	vera.post("/rules/"+ruleID(t, rule)+"/apply", map[string]any{}).
		requireStatus(http.StatusForbidden)
	require.Equal(t, "Corner Store", reloadTxn(l, l.id("august_corner")).Payee)
}

// --- The two exclusion flags -----------------------------------------------------

func TestTheTwoExclusionFlagsAreIndependent(t *testing.T) {
	l := buildLedger(t)
	plan := statementRule(l, "Out of the plan", "CORNER", map[string]any{
		"set_excluded_from_spending_plan": true,
	})

	body := preview(l, plan)
	actions := body["changes"].([]any)[0].(map[string]any)["actions"].(map[string]any)
	require.Equal(t, true, actions["set_excluded_from_spending_plan"])
	require.Nil(t, actions["set_excluded_from_reports"])

	l.alex.post("/rules/"+ruleID(t, plan)+"/apply", map[string]any{
		"transaction_ids": changedIDs(t, body),
	}).requireStatus(http.StatusOK)

	after := reloadTxn(l, l.id("august_corner"))
	require.True(t, after.ExcludedFromSpendingPlan)
	require.False(t, after.ExcludedFromReports, "the reports flag moved with the plan flag")

	// The other direction, on the same row, through a second rule.
	reports := statementRule(l, "Out of reports", "CORNER", map[string]any{
		"set_excluded_from_reports": true,
	})
	l.alex.post("/rules/"+ruleID(t, reports)+"/apply", map[string]any{
		"transaction_ids": changedIDs(t, preview(l, reports)),
	}).requireStatus(http.StatusOK)

	both := reloadTxn(l, l.id("august_corner"))
	require.True(t, both.ExcludedFromReports)
	require.True(t, both.ExcludedFromSpendingPlan)
}

func TestIncludeEverywhereIsItsOwnAction(t *testing.T) {
	// False is not the same instruction as unset: *Include everywhere* is the
	// inverse of *Exclude from*, and a rule has to be able to express it.
	l := buildLedger(t)
	excluded := seedTxn(l, "excluded", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 18),
		Amount: domain.MustFromString("-30.00"), StatementName: "BOOKSHOP",
		ExcludedFromReports: true, ExcludedFromSpendingPlan: true,
	})
	rule := statementRule(l, "Include everywhere", "BOOKSHOP", map[string]any{
		"set_excluded_from_reports": false,
	})

	l.alex.post("/rules/"+ruleID(t, rule)+"/apply", map[string]any{
		"transaction_ids": changedIDs(t, preview(l, rule)),
	}).requireStatus(http.StatusOK)

	after := reloadTxn(l, excluded)
	require.False(t, after.ExcludedFromReports)
	require.True(t, after.ExcludedFromSpendingPlan, "the untouched flag moved")
}

func TestClearingAnActionLeavesTheRowAlone(t *testing.T) {
	l := buildLedger(t)
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{
		"set_is_reviewed": false, "set_payee": "Corner Coffee",
	})
	updated := l.alex.patch("/rules/"+ruleID(t, rule), map[string]any{
		"actions": map[string]any{"set_is_reviewed": nil},
	}).requireStatus(http.StatusOK).json()
	require.Nil(t, updated["actions"].(map[string]any)["set_is_reviewed"])

	body := preview(l, rule)
	actions := body["changes"].([]any)[0].(map[string]any)["actions"].(map[string]any)
	require.Nil(t, actions["set_is_reviewed"])

	l.alex.post("/rules/"+ruleID(t, rule)+"/apply", map[string]any{
		"transaction_ids": changedIDs(t, body),
	}).requireStatus(http.StatusOK)
	require.True(t, reloadTxn(l, l.id("august_corner")).IsReviewed)
}

// --- Activation ------------------------------------------------------------------

func TestDeactivatingStopsTheRuleFiringOnWhatArrivesNext(t *testing.T) {
	l := buildLedger(t)
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{"set_payee": "Corner Coffee"})

	off := l.alex.patch("/rules/"+ruleID(t, rule), map[string]any{"is_active": false}).
		requireStatus(http.StatusOK).json()
	require.False(t, off["is_active"].(bool))

	fresh := seedTxn(l, "corner_new", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 21),
		Amount: domain.MustFromString("-9.00"), StatementName: "CORNER STORE",
	})
	spaceID := store.SpaceIDOf(l.id("space"))
	_, err := service.NewRules(l.env.DB).RunRules(t.Context(), spaceID, []uuid.UUID{fresh})
	require.NoError(t, err)
	require.Equal(t, "", reloadTxn(l, fresh).Payee)

	on := l.alex.patch("/rules/"+ruleID(t, rule), map[string]any{"is_active": true}).
		requireStatus(http.StatusOK).json()
	require.True(t, on["is_active"].(bool))
	_, err = service.NewRules(l.env.DB).RunRules(t.Context(), spaceID, []uuid.UUID{fresh})
	require.NoError(t, err)
	require.Equal(t, "Corner Coffee", reloadTxn(l, fresh).Payee)
}

func TestADeletedRuleIsGoneFromTheListAndFromTheEngine(t *testing.T) {
	l := buildLedger(t)
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{"set_payee": "Corner Coffee"})
	l.alex.del("/rules/" + ruleID(t, rule)).requireStatus(http.StatusNoContent)

	require.Empty(t, l.alex.get("/rules").requireStatus(http.StatusOK).list())
	l.alex.get("/rules/" + ruleID(t, rule)).requireStatus(http.StatusNotFound)
	l.alex.get("/rules/" + ruleID(t, rule) + "/preview").requireStatus(http.StatusNotFound)
}

// --- Tenancy -----------------------------------------------------------------------

func TestARuleInAnotherSpaceCannotBeReadOrApplied(t *testing.T) {
	l := buildLedger(t)
	rule := statementRule(l, "Corner Store", "CORNER", map[string]any{"set_payee": "Corner Coffee"})
	bob := otherSpace(l)

	require.Empty(t, bob.get("/rules").requireStatus(http.StatusOK).list())
	bob.get("/rules/" + ruleID(t, rule)).requireStatus(http.StatusNotFound)
	bob.get("/rules/" + ruleID(t, rule) + "/preview").requireStatus(http.StatusNotFound)
	bob.patch("/rules/"+ruleID(t, rule), map[string]any{"name": "Mine now"}).
		requireStatus(http.StatusNotFound)
	bob.post("/rules/"+ruleID(t, rule)+"/apply", map[string]any{}).
		requireStatus(http.StatusNotFound)
	bob.del("/rules/" + ruleID(t, rule)).requireStatus(http.StatusNotFound)

	require.Equal(t, "Corner Store", reloadTxn(l, l.id("august_corner")).Payee)
}

func TestARuleCannotBorrowAnotherSpacesCategoryTagOrFilter(t *testing.T) {
	l := buildLedger(t)
	base := map[string]any{
		"name": "Corner Store",
		"conditions": []map[string]any{{
			"field": string(domain.FieldStatementName), "operator": string(domain.OpContains),
			"value_texts": []string{"CORNER"},
		}},
	}
	for _, actions := range []map[string]any{
		{"set_category_id": l.str("stranger_category")},
		{"add_tag_ids": []string{l.str("stranger_tag")}},
	} {
		body := map[string]any{}
		for key, value := range base {
			body[key] = value
		}
		body["actions"] = actions
		l.alex.post("/rules", body).requireStatus(http.StatusConflict)
	}

	l.alex.post("/rules", map[string]any{
		"name": "Theirs", "filter_id": l.str("stranger_filter"),
		"actions": map[string]any{"set_is_reviewed": true},
	}).requireStatus(http.StatusConflict)
}
