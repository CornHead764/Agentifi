package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The typed change tools. Each takes an account or category by id or by name,
// resolved on the server against this space's rows through domain.ResolveName.
// A name that fits several rows is refused with the choices for the model to
// put to the person: the first of two matching cards must never be picked
// silently.
//
// Each builder produces the request a person would have sent from the browser.
// A bulk change produces several cards joined by a group id, so each is still
// one request, claimed once and settled once.

// maxBulk is how many rows one bulk tool call may propose changing; larger
// belongs on the register, where the rows are visible.
const maxBulk = 100

// actionPlaceholder marks an id that does not exist yet: the row a pending card
// in the same conversation will create. It is replaced by the created row's id
// when the carrying card is applied.
const actionPlaceholder = "@action:"

// resolver turns the names a model sends into ids, and remembers what every id
// it has seen is called, for the card.
type resolver struct {
	a              assistantTools
	ctx            context.Context
	conversationID uuid.UUID
	names          map[string]string
}

func (a assistantTools) resolver(ctx context.Context, conversationID uuid.UUID) *resolver {
	return &resolver{a: a, ctx: ctx, conversationID: conversationID, names: map[string]string{}}
}

// resolve finds one row of a kind by name or id.
func (r *resolver) resolve(kind, value string, candidates []domain.NameCandidate) (string, error) {
	id, _, err := r.lookup(kind, value, candidates)
	return id, err
}

// lookup is resolve, also saying what the name matched, so a caller can tell
// "nothing is called that" (which a pending card may answer) from "several
// things are" (which only the person can).
func (r *resolver) lookup(
	kind, value string, candidates []domain.NameCandidate,
) (string, domain.NameResolution, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", domain.NameResolution{}, fmt.Errorf("say which %s", kind)
	}
	found := domain.ResolveName(value, candidates)
	id, err := r.explain(kind, value, found, candidates)
	return id, found, err
}

func (r *resolver) explain(
	kind, value string, found domain.NameResolution, candidates []domain.NameCandidate,
) (string, error) {
	if found.Match != nil {
		r.names[found.Match.ID] = found.Match.Name
		return found.Match.ID, nil
	}
	if found.Ambiguous() {
		choices := make([]string, 0, len(found.Choices))
		for _, one := range found.Choices {
			choices = append(choices, fmt.Sprintf("%q (id %s)", one.Name, one.ID))
		}
		return "", fmt.Errorf("%q could mean more than one %s: %s. Ask the person which one they "+
			"mean; do not pick one yourself", value, kind, strings.Join(choices, ", "))
	}
	known := make([]string, 0, len(candidates))
	for _, one := range candidates {
		known = append(known, fmt.Sprintf("%q", one.Name))
	}
	sort.Strings(known)
	if len(known) > 25 {
		known = append(known[:25], "…")
	}
	if len(known) == 0 {
		return "", fmt.Errorf("there is no %s called %q, and there are none at all", kind, value)
	}
	return "", fmt.Errorf("there is no %s called %q. The %ss are: %s. Ask the person which "+
		"they mean, or propose creating it first", kind, value, kind, strings.Join(known, ", "))
}

// named resolves one row of a kind against candidates().
func (r *resolver) named(kind, noun, value string) (string, error) {
	candidates, err := r.candidates(kind)
	if err != nil {
		return "", err
	}
	return r.resolve(noun, value, candidates)
}

func (r *resolver) account(value string) (string, error) {
	return r.named("account", "account", value)
}

// category resolves a category, or a category a pending card in this
// conversation is about to create.
func (r *resolver) category(value string) (string, error) {
	return r.creatable("category", "create_category", value)
}

// tag is category's twin: an existing tag, or one a card is about to create.
func (r *resolver) tag(value string) (string, error) {
	return r.creatable("tag", "create_tag", value)
}

func (r *resolver) creatable(kind, tool, value string) (string, error) {
	candidates, err := r.candidates(kind)
	if err != nil {
		return "", err
	}
	id, found, err := r.lookup(kind, value, candidates)
	if err != nil && len(found.Choices) == 0 {
		if pending, ok := r.proposedCreation(tool, value); ok {
			return pending, nil
		}
	}
	return id, err
}

func (r *resolver) tags(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		id, err := r.tag(value)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func (r *resolver) categories(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		id, err := r.category(value)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func (r *resolver) rule(value string) (string, error) {
	return r.named("rule", "rule", value)
}

func (r *resolver) series(value string) (string, error) {
	return r.named("recurring", "recurring bill or income", value)
}

func (r *resolver) watchlist(value string) (string, error) {
	return r.named("watchlist", "watchlist", value)
}

// envelope resolves a planned-spending envelope within one month's plan.
func (r *resolver) envelope(month, value string) (string, error) {
	var view SpendingPlanMonth
	if err := r.a.get(r.ctx, "/spending-plan/"+month, nil, &view); err != nil {
		return "", err
	}
	candidates := make([]domain.NameCandidate, 0, len(view.Envelopes))
	for _, one := range view.Envelopes {
		candidates = append(candidates, domain.NameCandidate{ID: one.ID.String(), Name: one.Name})
	}
	return r.resolve("planned-spending envelope", value, candidates)
}

// proposedCreation finds an undeclined card in this conversation that creates a
// row of this name, and answers its placeholder, or the real id once created.
func (r *resolver) proposedCreation(tool, name string) (string, bool) {
	actions, err := r.a.env.DB.ListAssistantActions(r.ctx, r.a.sp.ID(), r.conversationID)
	if err != nil {
		return "", false
	}
	for i := len(actions) - 1; i >= 0; i-- {
		one := actions[i]
		if one.ToolName != tool || !strings.EqualFold(toolString(one.Body, "name"), name) {
			continue
		}
		switch one.Status {
		case domain.AssistantActionApplied:
			if one.ResourceID != uuid.Nil {
				r.names[one.ResourceID.String()] = toolString(one.Body, "name")
				return one.ResourceID.String(), true
			}
		case domain.AssistantActionPending, domain.AssistantActionFailed:
			key := actionPlaceholder + one.ID.String()
			r.names[key] = toolString(one.Body, "name")
			return key, true
		}
	}
	return "", false
}

// nameIDs records the names of ids the model sent directly, for the card. An
// unknown id is left unnamed; the API refuses it at apply time.
func (r *resolver) nameIDs(body map[string]any) {
	var walk func(key string, value any)
	walk = func(key string, value any) {
		switch v := value.(type) {
		case map[string]any:
			for k, inner := range v {
				walk(k, inner)
			}
		case []any:
			for _, inner := range v {
				walk(key, inner)
			}
		case []string:
			for _, inner := range v {
				walk(key, inner)
			}
		case []map[string]any:
			for _, inner := range v {
				walk(key, inner)
			}
		case string:
			if _, named := r.names[v]; named {
				return
			}
			id, err := uuid.Parse(v)
			if err != nil {
				return
			}
			if name := r.nameOf(key, id); name != "" {
				r.names[v] = name
			}
		}
	}
	walk("", body)
}

func (r *resolver) nameOf(key string, id uuid.UUID) string {
	ctx, space := r.ctx, r.a.sp.ID()
	switch {
	case strings.Contains(key, "category"), key == "parent_id":
		if one, err := r.a.env.DB.GetCategory(ctx, space, id); err == nil {
			return one.Name
		}
	case strings.Contains(key, "account"):
		if one, err := r.a.env.DB.GetAccount(ctx, space, id); err == nil {
			return one.Name
		}
	case strings.Contains(key, "tag"):
		if one, err := r.a.env.DB.GetTag(ctx, space, id); err == nil {
			return one.Name
		}
	case key == "value_ids":
		for _, try := range []string{"category_id", "account_id", "tag_id"} {
			if name := r.nameOf(try, id); name != "" {
				return name
			}
		}
	}
	return ""
}

// --- The typed builders ------------------------------------------------------

type proposalBuilder func(r *resolver, arguments map[string]any) ([]store.AssistantAction, error)

// typedProposals are the change tools whose arguments need the ledger to become
// a request. Tools that need only their arguments stay in buildAction.
var typedProposals = map[string]proposalBuilder{
	"create_rule":             proposeCreateRule,
	"update_rule":             proposeUpdateRule,
	"update_transactions":     proposeUpdateTransactions,
	"mark_reviewed":           proposeMarkReviewed,
	"update_category":         proposeUpdateCategory,
	"update_tag":              proposeUpdateTag,
	"create_watchlist":        proposeCreateWatchlist,
	"update_watchlist":        proposeUpdateWatchlist,
	"set_plan_amount":         proposeSetPlanAmount,
	"create_planned_spending": proposeCreatePlannedSpending,
	"update_planned_spending": proposeUpdatePlannedSpending,
	"create_recurring":        proposeCreateRecurring,
	"update_recurring":        proposeUpdateRecurring,
	"update_account":          proposeUpdateAccount,
}

// one wraps a single request as the list a builder returns.
func one(method, path string, body map[string]any) []store.AssistantAction {
	return []store.AssistantAction{{Method: method, Path: path, Body: body}}
}

// ruleConditionArgs are the arguments that describe which rows a rule matches.
var ruleConditionArgs = []string{
	"keywords", "matches", "match_on", "match_type", "account", "amount_min", "amount_max",
	"direction",
}

func proposeCreateRule(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	name := toolString(arguments, "name")
	if name == "" {
		return nil, fmt.Errorf("`name` is required: what to call the rule")
	}
	conditions, err := r.ruleConditions(arguments, nil)
	if err != nil {
		return nil, err
	}
	if len(conditions) == 0 {
		return nil, fmt.Errorf("a rule has to match something: give `keywords`, an `account` or " +
			"an amount. A rule with no conditions would rewrite every transaction")
	}
	actions, err := r.ruleActions(arguments)
	if err != nil {
		return nil, err
	}
	if len(actions) == 0 {
		return nil, fmt.Errorf("a rule that sets nothing does nothing; give at least one of " +
			"set_category, set_payee, set_notes, add_tags, the exclusion flags or mark_reviewed")
	}
	return one(http.MethodPost, "/rules", map[string]any{
		"name": name, "conditions": conditions, "actions": actions,
	}), nil
}

func proposeUpdateRule(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	id, err := r.rule(textutil.FirstNonBlank(toolString(arguments, "rule"), toolString(arguments, "rule_id")))
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if err := putString(body, arguments, "name", "name"); err != nil {
		return nil, err
	}
	if text, _ := body["name"].(string); body["name"] != nil && strings.TrimSpace(text) == "" {
		delete(body, "name")
	}
	putBool(body, arguments, "is_active", "is_active")

	touchesConditions := false
	for _, key := range ruleConditionArgs {
		if _, present := arguments[key]; present {
			touchesConditions = true
		}
	}
	if touchesConditions {
		var current RuleResponse
		if err := r.a.get(r.ctx, "/rules/"+id, nil, &current); err != nil {
			return nil, err
		}
		if !current.OwnsFilter {
			return nil, fmt.Errorf("that rule's conditions are a saved filter other screens " +
				"share; change them on the Rules page rather than here")
		}
		conditions, err := r.ruleConditions(arguments, current.Filter.Items)
		if err != nil {
			return nil, err
		}
		if len(conditions) == 0 {
			return nil, fmt.Errorf("that would leave the rule matching nothing in particular, " +
				"which is every transaction; keep at least one condition")
		}
		body["conditions"] = conditions
	}
	actions, err := r.ruleActions(arguments)
	if err != nil {
		return nil, err
	}
	if len(actions) > 0 {
		body["actions"] = actions
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("that would change nothing; say what to set")
	}
	return one(http.MethodPatch, "/rules/"+id, body), nil
}

// ruleConditions builds a rule's clause list in the one wire shape every
// surface saves. On an update, stored items of kinds the arguments do not
// mention are kept, so "add an amount limit" does not drop the keywords.
func (r *resolver) ruleConditions(
	arguments map[string]any, current []FilterItemResponse,
) ([]map[string]any, error) {
	var items []map[string]any

	keywords := toolStrings(arguments, "keywords")
	if matches := toolString(arguments, "matches"); matches != "" && len(keywords) == 0 {
		keywords = []string{matches}
	}
	field := textutil.FirstNonBlank(toolString(arguments, "match_on"), string(domain.FieldStatementName))
	if field != string(domain.FieldStatementName) && field != string(domain.FieldPayee) {
		return nil, fmt.Errorf("`match_on` has to be \"statement_name\" or \"payee\", not %q", field)
	}
	operator := textutil.FirstNonBlank(toolString(arguments, "match_type"), string(domain.OpContains))
	if operator != string(domain.OpContains) && operator != string(domain.OpIsExactly) {
		return nil, fmt.Errorf("`match_type` has to be \"contains\" or \"is_exactly\", not %q", operator)
	}
	_, keywordsNamed := arguments["keywords"]
	_, matchesNamed := arguments["matches"]
	if len(keywords) > 0 {
		items = append(items, conditionItem(field, operator, map[string]any{"value_texts": keywords}))
	} else if !keywordsNamed && !matchesNamed {
		items = append(items, keptItems(current, "statement_name", "payee")...)
	}

	if account := textutil.FirstNonBlank(toolString(arguments, "account"), toolString(arguments, "account_id")); account != "" {
		id, err := r.account(account)
		if err != nil {
			return nil, err
		}
		items = append(items, conditionItem(string(domain.FieldAccount), string(domain.OpIn),
			map[string]any{"value_ids": []string{id}}))
	} else if _, named := arguments["account"]; !named {
		items = append(items, keptItems(current, "account")...)
	}

	minimum, err := optionalMagnitude(arguments, "amount_min")
	if err != nil {
		return nil, err
	}
	maximum, err := optionalMagnitude(arguments, "amount_max")
	if err != nil {
		return nil, err
	}
	direction := strings.ToLower(toolString(arguments, "direction"))
	if direction != "" && direction != "expense" && direction != "income" {
		return nil, fmt.Errorf("`direction` has to be \"expense\" or \"income\", not %q", direction)
	}
	if minimum != "" || maximum != "" || direction != "" {
		amount := map[string]any{"amount_min": nil, "amount_max": nil, "state": nil}
		switch {
		case minimum != "" && maximum != "":
			amount["operator"] = string(domain.OpBetween)
		case minimum != "":
			amount["operator"] = string(domain.OpGreaterThan)
		case maximum != "":
			amount["operator"] = string(domain.OpLessThan)
		default:
			amount["operator"] = string(domain.OpBetween)
		}
		if minimum != "" {
			amount["amount_min"] = minimum
		}
		if maximum != "" {
			amount["amount_max"] = maximum
		}
		if direction != "" {
			amount["state"] = direction == "income"
		}
		items = append(items, conditionItem(string(domain.FieldAmount), "", amount))
	} else {
		items = append(items, keptItems(current, "amount")...)
	}

	for _, kept := range current {
		switch kept.Field {
		case "statement_name", "payee", "account", "amount":
		default:
			items = append(items, keptItem(kept))
		}
	}
	for position, item := range items {
		item["position"] = position
	}
	return items, nil
}

func conditionItem(field, operator string, values map[string]any) map[string]any {
	item := map[string]any{
		"field": field, "operator": operator, "group_index": 0, "position": 0,
		"negated": false, "value_ids": []string{}, "value_texts": []string{},
	}
	for key, value := range values {
		item[key] = value
	}
	return item
}

// keptItems are the stored items of these fields, for an update that leaves
// them alone. Only group 0: a rule with alternative keyword sets is the Rules
// page's to edit.
func keptItems(current []FilterItemResponse, fields ...string) []map[string]any {
	var out []map[string]any
	for _, item := range current {
		for _, field := range fields {
			if string(item.Field) == field {
				out = append(out, keptItem(item))
			}
		}
	}
	return out
}

func keptItem(item FilterItemResponse) map[string]any {
	encoded := mustMap(item)
	encoded["group_index"] = 0
	delete(encoded, "id")
	return encoded
}

func (r *resolver) ruleActions(arguments map[string]any) (map[string]any, error) {
	actions := map[string]any{}
	if category := textutil.FirstNonBlank(toolString(arguments, "set_category"),
		toolString(arguments, "set_category_id")); category != "" {
		id, err := r.category(category)
		if err != nil {
			return nil, err
		}
		actions["set_category_id"] = id
	}
	for _, key := range []string{"set_payee", "set_notes"} {
		if value := toolString(arguments, key); value != "" {
			actions[key] = value
		}
	}
	if tags := firstList(arguments, "add_tags", "add_tag_ids"); len(tags) > 0 {
		ids, err := r.tags(tags)
		if err != nil {
			return nil, err
		}
		actions["add_tag_ids"] = ids
	}
	putBool(actions, arguments, "set_excluded_from_reports", "set_excluded_from_reports")
	putBool(actions, arguments, "set_excluded_from_spending_plan", "set_excluded_from_spending_plan")
	putBool(actions, arguments, "mark_reviewed", "set_is_reviewed")
	return actions, nil
}

// transactionEdit is the PATCH /transactions/{id} body for the fields an edit
// names, with added and removed tags worked out against the row's current ones.
func (r *resolver) transactionEdit(
	arguments map[string]any, current store.Transaction,
) (map[string]any, error) {
	body := map[string]any{}
	if category := textutil.FirstNonBlank(toolString(arguments, "category"),
		toolString(arguments, "category_id")); category != "" {
		id, err := r.category(category)
		if err != nil {
			return nil, err
		}
		body["category_id"] = id
	}
	for _, key := range []string{"payee", "notes"} {
		if value := toolString(arguments, key); value != "" {
			body[key] = value
		}
	}
	putBool(body, arguments, "is_reviewed", "is_reviewed")
	putBool(body, arguments, "excluded_from_reports", "excluded_from_reports")
	putBool(body, arguments, "excluded_from_spending_plan", "excluded_from_spending_plan")

	add, remove := toolStrings(arguments, "add_tags"), toolStrings(arguments, "remove_tags")
	if len(add) > 0 || len(remove) > 0 {
		adding, err := r.tags(add)
		if err != nil {
			return nil, err
		}
		removing, err := r.tags(remove)
		if err != nil {
			return nil, err
		}
		gone := map[string]bool{}
		for _, id := range removing {
			gone[id] = true
		}
		next := []string{}
		seen := map[string]bool{}
		for _, id := range current.TagIDs {
			if !gone[id.String()] && !seen[id.String()] {
				next = append(next, id.String())
				seen[id.String()] = true
			}
		}
		for _, id := range adding {
			if !seen[id] {
				next = append(next, id)
				seen[id] = true
			}
		}
		body["tag_ids"] = next
	}
	return body, nil
}

func proposeUpdateTransactions(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	ids, err := transactionIDs(arguments)
	if err != nil {
		return nil, err
	}
	out := make([]store.AssistantAction, 0, len(ids))
	for _, id := range ids {
		txn, err := r.a.env.DB.GetTransaction(r.ctx, r.a.sp.ID(), id)
		if err != nil {
			if isNotFound(err) {
				return nil, fmt.Errorf("there is no transaction %s here; read the ids from "+
					"search_transactions", id)
			}
			return nil, err
		}
		body, err := r.transactionEdit(arguments, txn)
		if err != nil {
			return nil, err
		}
		if len(body) == 0 {
			return nil, fmt.Errorf("that would change nothing; say what to set")
		}
		out = append(out, store.AssistantAction{
			Method: http.MethodPatch, Path: "/transactions/" + id.String(), Body: body,
			Summary: rowLine(txn),
		})
	}
	return out, nil
}

func proposeMarkReviewed(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	ids, err := transactionIDs(arguments)
	if err != nil {
		return nil, err
	}
	reviewed := true
	if value, ok := arguments["reviewed"].(bool); ok {
		reviewed = value
	}
	out := make([]store.AssistantAction, 0, len(ids))
	for _, id := range ids {
		txn, err := r.a.env.DB.GetTransaction(r.ctx, r.a.sp.ID(), id)
		if err != nil {
			if isNotFound(err) {
				return nil, fmt.Errorf("there is no transaction %s here", id)
			}
			return nil, err
		}
		out = append(out, store.AssistantAction{
			Method: http.MethodPatch, Path: "/transactions/" + id.String(),
			Body: map[string]any{"is_reviewed": reviewed}, Summary: rowLine(txn),
		})
	}
	return out, nil
}

// rowLine names one row of a bulk change, for its line on the group's card.
func rowLine(txn store.Transaction) string {
	return fmt.Sprintf("%s · %s · %s", txn.Date.String(),
		store.DomainTransaction(txn).DisplayPayee(), txn.Amount.String())
}

func transactionIDs(arguments map[string]any) ([]uuid.UUID, error) {
	raw := toolStrings(arguments, "transaction_ids")
	if len(raw) == 0 {
		return nil, fmt.Errorf("`transaction_ids` is required: the ids from search_transactions")
	}
	if len(raw) > maxBulk {
		return nil, fmt.Errorf("that is %d transactions; propose at most %d at once and say "+
			"there are more", len(raw), maxBulk)
	}
	out := make([]uuid.UUID, 0, len(raw))
	seen := map[uuid.UUID]bool{}
	for _, text := range raw {
		id, err := uuid.Parse(text)
		if err != nil {
			return nil, fmt.Errorf("%q is not a transaction id — read them from search_transactions",
				text)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func proposeUpdateCategory(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	id, err := r.category(textutil.FirstNonBlank(toolString(arguments, "category"),
		toolString(arguments, "category_id")))
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(id, actionPlaceholder) {
		return nil, fmt.Errorf("that category is still only proposed; change the proposal " +
			"instead, or wait until it is created")
	}
	body := map[string]any{}
	if name := toolString(arguments, "name"); name != "" {
		body["name"] = name
	}
	if parent := toolString(arguments, "parent"); parent != "" {
		parentID, err := r.category(parent)
		if err != nil {
			return nil, err
		}
		body["parent_id"] = parentID
	}
	putBool(body, arguments, "excluded_from_reports", "excluded_from_reports")
	putBool(body, arguments, "excluded_from_spending_plan", "excluded_from_spending_plan")
	putBool(body, arguments, "hidden", "excluded_from_category_list")
	if len(body) == 0 {
		return nil, fmt.Errorf("that would change nothing; say what to set")
	}
	return one(http.MethodPatch, "/categories/"+id, body), nil
}

func proposeUpdateTag(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	id, err := r.tag(textutil.FirstNonBlank(toolString(arguments, "tag"), toolString(arguments, "tag_id")))
	if err != nil {
		return nil, err
	}
	name := toolString(arguments, "name")
	if name == "" {
		return nil, fmt.Errorf("`name` is required: the tag's new name")
	}
	return one(http.MethodPatch, "/tags/"+id, map[string]any{"name": name}), nil
}

func proposeCreateWatchlist(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	name := toolString(arguments, "name")
	if name == "" {
		return nil, fmt.Errorf("`name` is required")
	}
	body := map[string]any{"name": name}
	if emoji := toolString(arguments, "emoji"); emoji != "" {
		body["emoji"] = emoji
	}
	if target, err := optionalMagnitude(arguments, "target_amount"); err != nil {
		return nil, err
	} else if target != "" {
		body["target_amount"] = target
	}
	subjects := 0
	if categories := toolStrings(arguments, "categories"); len(categories) > 0 {
		ids, err := r.categories(categories)
		if err != nil {
			return nil, err
		}
		body["category_ids"] = ids
		subjects++
	}
	if payees := toolStrings(arguments, "payees"); len(payees) > 0 {
		body["payee_names"] = payees
		subjects++
	}
	if tags := toolStrings(arguments, "tags"); len(tags) > 0 {
		ids, err := r.tags(tags)
		if err != nil {
			return nil, err
		}
		body["tag_ids"] = ids
		subjects++
	}
	if subjects != 1 {
		return nil, fmt.Errorf("a watchlist watches exactly one of `categories`, `payees` or `tags`")
	}
	return one(http.MethodPost, "/watchlists", body), nil
}

func proposeUpdateWatchlist(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	id, err := r.watchlist(textutil.FirstNonBlank(toolString(arguments, "watchlist"),
		toolString(arguments, "watchlist_id")))
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if name := toolString(arguments, "name"); name != "" {
		body["name"] = name
	}
	if emoji := toolString(arguments, "emoji"); emoji != "" {
		body["emoji"] = emoji
	}
	if target, err := optionalMagnitude(arguments, "target_amount"); err != nil {
		return nil, err
	} else if target != "" {
		body["target_amount"] = target
	}
	if value, ok := arguments["clear_target"].(bool); ok && value {
		body["target_amount"] = nil
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("that would change nothing; say what to set")
	}
	return one(http.MethodPatch, "/watchlists/"+id, body), nil
}

func toolMonth(arguments map[string]any, now time.Time) (domain.Month, error) {
	raw := toolString(arguments, "month")
	if raw == "" {
		return domain.MonthOf(domain.DateOf(now)), nil
	}
	month, err := parseMonth(raw)
	if err != nil {
		return domain.Month{}, fmt.Errorf("`month` must look like 2026-08, not %q", raw)
	}
	return month, nil
}

func proposeSetPlanAmount(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	month, err := toolMonth(arguments, r.a.env.now())
	if err != nil {
		return nil, err
	}
	bucket := toolString(arguments, "bucket")
	if _, ok := bucketFamily(domain.BucketKey(bucket)); !ok {
		return nil, fmt.Errorf("`bucket` has to be one of income, bills, planned_spend, " +
			"other_spend or goals")
	}
	body := map[string]any{}
	if value, ok := arguments["clear"].(bool); ok && value {
		body["overwritten_amount"] = nil
	} else {
		amount, err := toolMoney(arguments, "amount")
		if err != nil {
			return nil, err
		}
		body["overwritten_amount"] = amount.Abs().String()
	}
	return one(http.MethodPatch, "/spending-plan/"+month.String()+"/buckets/"+bucket, body), nil
}

func proposeCreatePlannedSpending(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	month, err := toolMonth(arguments, r.a.env.now())
	if err != nil {
		return nil, err
	}
	name := toolString(arguments, "name")
	if name == "" {
		return nil, fmt.Errorf("`name` is required")
	}
	amount, err := toolMoney(arguments, "target_amount")
	if err != nil {
		return nil, err
	}
	categories := toolStrings(arguments, "categories")
	if len(categories) == 0 {
		return nil, fmt.Errorf("`categories` is required: which spending this envelope sets aside for")
	}
	ids, err := r.categories(categories)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"name": name, "target_amount": amount.Abs().String(), "category_ids": ids,
	}
	putBool(body, arguments, "recurring", "recurring")
	putBool(body, arguments, "rollover", "rollover")
	return one(http.MethodPost, "/spending-plan/"+month.String()+"/envelopes", body), nil
}

func proposeUpdatePlannedSpending(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	month, err := toolMonth(arguments, r.a.env.now())
	if err != nil {
		return nil, err
	}
	id, err := r.envelope(month.String(), textutil.FirstNonBlank(toolString(arguments, "envelope"),
		toolString(arguments, "envelope_id")))
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if name := toolString(arguments, "name"); name != "" {
		body["name"] = name
	}
	if raw := toolString(arguments, "target_amount"); raw != "" {
		amount, err := toolMoney(arguments, "target_amount")
		if err != nil {
			return nil, err
		}
		body["overwritten_target_amount"] = amount.Abs().String()
	}
	if categories := toolStrings(arguments, "categories"); len(categories) > 0 {
		ids, err := r.categories(categories)
		if err != nil {
			return nil, err
		}
		body["category_ids"] = ids
	}
	putBool(body, arguments, "recurring", "recurring")
	if len(body) == 0 {
		return nil, fmt.Errorf("that would change nothing; say what to set")
	}
	return one(http.MethodPatch, "/spending-plan/"+month.String()+"/envelopes/"+id, body), nil
}

// recurrenceFor spells how often a series repeats as the request's recurrence,
// from the one word the tool takes.
func recurrenceFor(repeats string, start domain.Date) (map[string]any, error) {
	day := start.Time().Day()
	switch strings.ToLower(strings.TrimSpace(repeats)) {
	case "", "monthly":
		return map[string]any{"frequency": "MONTHLY", "by_month_day": []int{day}}, nil
	case "weekly":
		return map[string]any{"frequency": "WEEKLY", "by_day": []string{weekdayCode(start)}}, nil
	case "every_two_weeks", "biweekly":
		return map[string]any{
			"frequency": "WEEKLY", "interval": 2, "by_day": []string{weekdayCode(start)},
		}, nil
	case "quarterly":
		return map[string]any{"frequency": "MONTHLY", "interval": 3, "by_month_day": []int{day}}, nil
	case "yearly":
		return map[string]any{"frequency": "YEARLY"}, nil
	case "once":
		return map[string]any{"alias": string(domain.AliasOneTime)}, nil
	}
	return nil, fmt.Errorf("`repeats` has to be weekly, every_two_weeks, monthly, quarterly, "+
		"yearly or once, not %q", repeats)
}

func weekdayCode(date domain.Date) string {
	return [...]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}[date.Time().Weekday()]
}

func proposeCreateRecurring(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	kind := strings.ToLower(toolString(arguments, "kind"))
	switch domain.SeriesKind(kind) {
	case domain.SeriesBill, domain.SeriesSubscription, domain.SeriesIncome:
	default:
		return nil, fmt.Errorf("`kind` has to be bill, subscription or income")
	}
	accountID, err := r.account(toolString(arguments, "account"))
	if err != nil {
		return nil, err
	}
	description := toolString(arguments, "matches")
	if description == "" {
		return nil, fmt.Errorf("`matches` is required: the text the bank's statement name has " +
			"for this payment, which is how its transactions are found")
	}
	amount, err := toolMoney(arguments, "amount")
	if err != nil {
		return nil, err
	}
	// The sign is the direction, and the direction is the kind: a bill is
	// money out whatever sign the model wrote.
	if kind == string(domain.SeriesIncome) {
		amount = amount.Abs()
	} else {
		amount = amount.Abs().Neg()
	}
	start, err := toolRequiredDate(arguments, "next_due_on")
	if err != nil {
		return nil, err
	}
	recurrence, err := recurrenceFor(toolString(arguments, "repeats"), start)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"account_id": accountID, "kind": kind, "description": description,
		"amount": amount.String(), "start_on": start.String(), "recurrence": recurrence,
	}
	if name := toolString(arguments, "name"); name != "" {
		body["display_name"] = name
	}
	if category := toolString(arguments, "category"); category != "" {
		id, err := r.category(category)
		if err != nil {
			return nil, err
		}
		body["category_id"] = id
	}
	return one(http.MethodPost, "/series", body), nil
}

func proposeUpdateRecurring(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	id, err := r.series(textutil.FirstNonBlank(toolString(arguments, "recurring"),
		toolString(arguments, "series_id")))
	if err != nil {
		return nil, err
	}
	var current struct {
		Kind    string `json:"kind"`
		StartOn string `json:"start_on"`
	}
	if err := r.a.get(r.ctx, "/series/"+id, nil, &current); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if name := toolString(arguments, "name"); name != "" {
		body["display_name"] = name
	}
	if raw := toolString(arguments, "amount"); raw != "" {
		amount, err := toolMoney(arguments, "amount")
		if err != nil {
			return nil, err
		}
		if current.Kind == string(domain.SeriesIncome) {
			amount = amount.Abs()
		} else {
			amount = amount.Abs().Neg()
		}
		body["amount"] = amount.String()
	}
	if category := toolString(arguments, "category"); category != "" {
		categoryID, err := r.category(category)
		if err != nil {
			return nil, err
		}
		body["category_id"] = categoryID
	}
	if err := putDate(body, arguments, "next_due_on", "override_next_due_on"); err != nil {
		return nil, err
	}
	if repeats := toolString(arguments, "repeats"); repeats != "" {
		start, err := parseDate(current.StartOn)
		if err != nil {
			start = domain.DateOf(r.a.env.now())
		}
		recurrence, err := recurrenceFor(repeats, start)
		if err != nil {
			return nil, err
		}
		body["recurrence"] = recurrence
	}
	putBool(body, arguments, "is_active", "is_active")
	if len(body) == 0 {
		return nil, fmt.Errorf("that would change nothing; say what to set")
	}
	return one(http.MethodPatch, "/series/"+id, body), nil
}

func proposeUpdateAccount(r *resolver, arguments map[string]any) ([]store.AssistantAction, error) {
	id, err := r.account(textutil.FirstNonBlank(toolString(arguments, "account"),
		toolString(arguments, "account_id")))
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if name := toolString(arguments, "name"); name != "" {
		body["name"] = name
	}
	putBool(body, arguments, "excluded_from_reports", "excluded_from_reports")
	putBool(body, arguments, "excluded_from_spending_plan", "excluded_from_spending_plan")
	putBool(body, arguments, "include_in_net_worth", "include_in_net_worth")
	putBool(body, arguments, "hidden_from_account_bar", "excluded_from_account_bar")
	putBool(body, arguments, "is_closed", "is_closed")
	switch starts := toolString(arguments, "history_starts_on"); {
	case starts == "":
	case strings.EqualFold(starts, "automatic"):
		body["history_starts_on"] = nil
	default:
		if _, err := parseDate(starts); err != nil {
			return nil, fmt.Errorf("history_starts_on must be YYYY-MM-DD or \"automatic\", got %q", starts)
		}
		body["history_starts_on"] = starts
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("that would change nothing; say what to set")
	}
	return one(http.MethodPatch, "/accounts/"+id, body), nil
}

// resolveLegacyArguments lets the id-taking tools accept a name in the same
// place, resolving it (or answering the choices) rather than refusing it for
// not being a uuid.
func (r *resolver) resolveLegacyArguments(arguments map[string]any) error {
	resolve := func(target map[string]any, key string, find func(string) (string, error)) error {
		value := toolString(target, key)
		if value == "" || strings.HasPrefix(value, actionPlaceholder) {
			return nil
		}
		if _, err := uuid.Parse(value); err == nil {
			return nil
		}
		id, err := find(value)
		if err != nil {
			return err
		}
		target[key] = id
		return nil
	}
	for key, find := range map[string]func(string) (string, error){
		"category_id": r.category, "parent_id": r.category, "set_category_id": r.category,
		"account_id": r.account,
	} {
		if err := resolve(arguments, key, find); err != nil {
			return err
		}
	}
	for _, key := range []string{"tag_ids", "add_tag_ids"} {
		values, ok := arguments[key].([]any)
		if !ok {
			continue
		}
		for i, value := range values {
			text, _ := value.(string)
			if text == "" || strings.HasPrefix(text, actionPlaceholder) {
				continue
			}
			if _, err := uuid.Parse(text); err == nil {
				continue
			}
			id, err := r.tag(text)
			if err != nil {
				return err
			}
			values[i] = id
		}
	}
	for _, split := range toolSplits(arguments) {
		if err := resolve(split, "category_id", r.category); err != nil {
			return err
		}
	}
	return nil
}

// --- Looking a name up --------------------------------------------------------

// find is the change tools' name resolution offered on its own, so the model
// can put the choices to the person before proposing anything.
func (a assistantTools) find(ctx context.Context, arguments map[string]any) (any, error) {
	r := a.resolver(ctx, uuid.Nil)
	kind := strings.ToLower(toolString(arguments, "kind"))
	candidates, err := r.candidates(kind)
	if err != nil {
		return nil, err
	}
	rows := func(list []domain.NameCandidate) []map[string]string {
		out := make([]map[string]string, 0, len(list))
		for _, one := range list {
			out = append(out, map[string]string{"id": one.ID, "name": one.Name})
		}
		return out
	}
	name := toolString(arguments, "name")
	if name == "" {
		return map[string]any{"kind": kind, "all": rows(candidates)}, nil
	}
	found := domain.ResolveName(name, candidates)
	switch {
	case found.Match != nil:
		return map[string]any{"kind": kind, "match": rows([]domain.NameCandidate{*found.Match})[0]}, nil
	case found.Ambiguous():
		return map[string]any{
			"kind": kind, "choices": rows(found.Choices),
			"note": "more than one fits. Ask the person which they mean; do not pick one.",
		}, nil
	}
	return map[string]any{
		"kind": kind, "match": nil, "all": rows(candidates),
		"note": "nothing is called that. Ask the person, or propose creating it.",
	}, nil
}

// candidates is every row of a kind, as the name resolver sees them.
func (r *resolver) candidates(kind string) ([]domain.NameCandidate, error) {
	var out []domain.NameCandidate
	add := func(id uuid.UUID, name string, aliases ...string) {
		one := domain.NameCandidate{ID: id.String(), Name: name}
		for _, alias := range aliases {
			if strings.TrimSpace(alias) != "" {
				one.Aliases = append(one.Aliases, alias)
			}
		}
		out = append(out, one)
	}
	ctx, space := r.ctx, r.a.sp.ID()
	switch kind {
	case "account":
		rows, err := r.a.env.DB.ListAccounts(ctx, space, store.AccountQuery{IncludeClosed: true})
		if err != nil {
			return nil, err
		}
		for _, one := range rows {
			add(one.ID, one.Name, one.MaskedNumber, one.Description)
		}
	case "category":
		rows, err := r.a.env.DB.ListCategories(ctx, space, false)
		if err != nil {
			return nil, err
		}
		parents := map[uuid.UUID]string{}
		for _, one := range rows {
			parents[one.ID] = one.Name
		}
		for _, one := range rows {
			if parent, ok := parents[one.ParentID]; ok {
				add(one.ID, one.Name, parent+" › "+one.Name)
			} else {
				add(one.ID, one.Name)
			}
		}
	case "tag":
		rows, err := r.a.env.DB.ListTags(ctx, space, false)
		if err != nil {
			return nil, err
		}
		for _, one := range rows {
			add(one.ID, one.Name)
		}
	case "rule":
		rows, err := r.a.env.DB.ListRules(ctx, space, store.RuleQuery{})
		if err != nil {
			return nil, err
		}
		for _, one := range rows {
			add(one.ID, one.Name)
		}
	case "recurring":
		rows, err := querySeries(ctx, r.a.env, r.a.sp, seriesFilter{})
		if err != nil {
			return nil, err
		}
		for _, one := range rows {
			add(one.ID, textutil.FirstNonBlank(one.DisplayName, one.Description), one.Description)
		}
	case "watchlist":
		rows, err := r.a.env.DB.ListWatchlists(ctx, space)
		if err != nil {
			return nil, err
		}
		for _, one := range rows {
			add(one.ID, one.Name)
		}
	case "goal":
		rows, err := r.a.env.DB.ListGoals(ctx, space, false)
		if err != nil {
			return nil, err
		}
		for _, one := range rows {
			add(one.ID, one.Name)
		}
	default:
		return nil, fmt.Errorf("`kind` has to be one of account, category, tag, rule, recurring, " +
			"watchlist or goal")
	}
	return out, nil
}

// --- Reading back what an argument said ---------------------------------------

// toolStrings reads a list of text, tolerating the one string a model sends
// where a list belongs.
func toolStrings(arguments map[string]any, key string) []string {
	switch raw := arguments[key].(type) {
	case string:
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		return []string{strings.TrimSpace(raw)}
	case []any:
		out := make([]string, 0, len(raw))
		for _, one := range raw {
			if text, ok := one.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	}
	return nil
}

func firstList(arguments map[string]any, keys ...string) []string {
	for _, key := range keys {
		if list := toolStrings(arguments, key); len(list) > 0 {
			return list
		}
	}
	return nil
}

// optionalMagnitude reads an amount given as a size — "20", "$20.00" — for a
// comparison that runs on the magnitude. Empty when absent.
func optionalMagnitude(arguments map[string]any, key string) (string, error) {
	var raw string
	switch value := arguments[key].(type) {
	case nil:
		return "", nil
	case float64:
		raw = strconv.FormatFloat(value, 'f', -1, 64)
	case string:
		if raw = strings.TrimSpace(value); raw == "" {
			return "", nil
		}
	default:
		return "", fmt.Errorf("`%s` has to be an amount such as \"20.00\"", key)
	}
	amount, ok := domain.ParseMoneyText(raw)
	if !ok {
		return "", fmt.Errorf("`%s` has to be an amount such as \"20.00\", not %q", key, raw)
	}
	return amount.Abs().String(), nil
}

// mustMap is a response struct as the JSON object it encodes to.
func mustMap(value any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(encoded, &out)
	return out
}
