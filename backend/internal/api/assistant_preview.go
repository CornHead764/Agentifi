package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// What a proposed change reads as on its card: the request in words, with
// every id named, so nobody approves a uuid. Written once at proposal time from
// the body the card will send and the names the tool resolved, never from the
// model's own description, which is the summary's and kept separate so the two
// can be compared.
//
// Generic on purpose: `fields` is a label and value per thing the request
// sets, enough for any endpoint. A rule's conditions are left to the page's
// Rules renderer.

// previewField is one line of a card.
type previewField struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Kind is how the page draws the value: "money", "name", "flag" or "text".
	Kind string `json:"kind"`
}

// actionPreview is the preview stored with a proposed change.
func actionPreview(action store.AssistantAction, names map[string]string) map[string]any {
	resource := strings.SplitN(strings.Trim(action.Path, "/"), "/", 2)[0]
	used := map[string]string{}
	fields := describeBody(action.Body, names, used, "")
	// A rule's conditions are not described here but the page names their ids
	// all the same, so every name the request mentions anywhere goes with it.
	encoded, _ := json.Marshal(action.Body)
	for id, name := range names {
		if strings.Contains(string(encoded), id) || strings.Contains(action.Path, id) {
			used[id] = name
		}
	}
	return map[string]any{
		"resource": resource,
		"verb":     previewVerb(action.Method),
		"fields":   mustList(fields),
		"names":    used,
	}
}

func previewVerb(method string) string {
	switch method {
	case http.MethodPost:
		return "create"
	case http.MethodDelete:
		return "delete"
	}
	return "update"
}

// previewLabels are the words for the fields every typed tool writes. A field
// nobody listed is labelled from its own name, which is how a card over an
// endpoint without a tool still reads.
var previewLabels = map[string]string{
	"name": "Name", "payee": "Payee", "notes": "Note", "date": "Date", "amount": "Amount",
	"account_id": "Account", "category_id": "Category", "parent_id": "Inside",
	"tag_ids": "Tags", "is_reviewed": "Reviewed", "is_active": "Active",
	"excluded_from_reports":       "Excluded from reports",
	"excluded_from_spending_plan": "Excluded from the spending plan",
	"excluded_from_category_list": "Hidden from the category list",
	"excluded_from_account_bar":   "Hidden from the account bar",
	"include_in_net_worth":        "Counts in net worth", "is_closed": "Closed",
	"kind": "Kind", "description": "Matches statement text", "display_name": "Shown as",
	"start_on": "Next due", "override_next_due_on": "Next due",
	"target_amount": "Target", "overwritten_target_amount": "This month's target",
	"overwritten_amount": "Planned amount", "category_ids": "Categories",
	"payee_names": "Payees", "emoji": "Emoji", "recurring": "Repeats every month",
	"rollover": "Rolls over", "target_on": "By", "recurrence": "Repeats",
	"set_category_id": "Then category", "set_payee": "Then rename to",
	"set_notes": "Then note", "add_tag_ids": "Then tag",
	"set_excluded_from_reports":       "Then exclude from reports",
	"set_excluded_from_spending_plan": "Then exclude from the spending plan",
	"set_is_reviewed":                 "Then mark reviewed",
}

// previewOrder puts the fields a person looks for first at the top.
var previewOrder = []string{
	"name", "display_name", "kind", "account_id", "date", "amount", "payee", "description",
	"category_id", "parent_id", "category_ids", "payee_names", "tag_ids", "target_amount",
}

var previewMoney = map[string]bool{
	"amount": true, "target_amount": true, "overwritten_target_amount": true,
	"overwritten_amount": true, "rollover_amount": true,
}

func describeBody(
	body map[string]any, names, used map[string]string, prefix string,
) []previewField {
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	rank := func(key string) int {
		for i, one := range previewOrder {
			if one == key {
				return i
			}
		}
		return len(previewOrder)
	}
	sort.Slice(keys, func(i, j int) bool {
		if rank(keys[i]) != rank(keys[j]) {
			return rank(keys[i]) < rank(keys[j])
		}
		return keys[i] < keys[j]
	})

	var out []previewField
	for _, key := range keys {
		value := body[key]
		switch key {
		case "conditions":
			// Drawn by the page from the request itself; see the file comment.
			continue
		case "actions":
			if inner, ok := value.(map[string]any); ok {
				out = append(out, describeBody(inner, names, used, "")...)
			}
			continue
		case "recurrence":
			out = append(out, previewField{
				Label: "Repeats", Value: describeRecurrence(value), Kind: "text",
			})
			continue
		case "splits":
			continue
		}
		label := previewLabels[key]
		if label == "" {
			label = prefix + humanize(key)
		}
		if value == nil {
			out = append(out, previewField{Label: label, Value: "cleared", Kind: "text"})
			continue
		}
		switch v := value.(type) {
		case bool:
			text := "No"
			if v {
				text = "Yes"
			}
			out = append(out, previewField{Label: label, Value: text, Kind: "flag"})
		case string:
			if name, ok := nameFor(v, names); ok {
				used[v] = name
				out = append(out, previewField{Label: label, Value: name, Kind: "name"})
				continue
			}
			kind := "text"
			if previewMoney[key] {
				kind = "money"
			}
			out = append(out, previewField{Label: label, Value: v, Kind: kind})
		case []string, []any:
			parts := []string{}
			for _, item := range previewStrings(v) {
				if name, ok := nameFor(item, names); ok {
					used[item] = name
					parts = append(parts, name)
				} else {
					parts = append(parts, item)
				}
			}
			out = append(out, previewField{Label: label, Value: strings.Join(parts, ", "), Kind: "text"})
		case map[string]any:
			out = append(out, describeBody(v, names, used, label+" · ")...)
		default:
			encoded, _ := json.Marshal(v)
			out = append(out, previewField{Label: label, Value: string(encoded), Kind: "text"})
		}
	}
	return out
}

// nameFor is what an id, or a placeholder for one, is called.
func nameFor(value string, names map[string]string) (string, bool) {
	name, ok := names[value]
	if !ok {
		return "", false
	}
	if strings.HasPrefix(value, actionPlaceholder) {
		return name + " (proposed above)", true
	}
	return name, true
}

func previewStrings(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, one := range v {
			if text, ok := one.(string); ok {
				out = append(out, text)
			} else {
				encoded, _ := json.Marshal(one)
				out = append(out, string(encoded))
			}
		}
		return out
	}
	return nil
}

func describeRecurrence(value any) string {
	recurrence, _ := value.(map[string]any)
	if alias, _ := recurrence["alias"].(string); alias == "ONE_TIME" {
		return "Once"
	}
	interval := 1
	switch n := recurrence["interval"].(type) {
	case int:
		interval = n
	case float64:
		interval = int(n)
	}
	switch frequency, _ := recurrence["frequency"].(string); frequency {
	case "WEEKLY":
		if interval == 2 {
			return "Every two weeks"
		}
		return "Every week"
	case "MONTHLY":
		if interval == 3 {
			return "Every quarter"
		}
		return "Every month"
	case "YEARLY":
		return "Every year"
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func humanize(key string) string {
	words := strings.ReplaceAll(strings.TrimSuffix(strings.TrimSuffix(key, "_ids"), "_id"), "_", " ")
	if words == "" {
		return key
	}
	return textutil.Capitalize(words)
}

func mustList(fields []previewField) []any {
	out := make([]any, 0, len(fields))
	for _, field := range fields {
		out = append(out, map[string]any{"label": field.Label, "value": field.Value, "kind": field.Kind})
	}
	return out
}

// nameSubject records what the row a path is about is called: the rule an
// update edits, the account whose settings change.
func (r *resolver) nameSubject(path string) {
	id, ok := subjectID(path)
	if !ok {
		return
	}
	if _, named := r.names[id.String()]; named {
		return
	}
	ctx, space := r.ctx, r.a.sp.ID()
	var name string
	switch strings.SplitN(strings.Trim(path, "/"), "/", 2)[0] {
	case "rules":
		if rows, err := r.a.env.DB.ListRules(ctx, space, store.RuleQuery{}); err == nil {
			for _, one := range rows {
				if one.ID == id {
					name = one.Name
				}
			}
		}
	case "watchlists":
		if one, err := r.a.env.DB.GetWatchlist(ctx, space, id); err == nil {
			name = one.Name
		}
	case "transactions":
		if one, err := r.a.env.DB.GetTransaction(ctx, space, id); err == nil {
			name = store.DomainTransaction(one).DisplayPayee()
		}
	default:
		name = r.nameOf(strings.TrimSuffix(strings.SplitN(strings.Trim(path, "/"), "/", 2)[0], "s")+"_id", id)
		if name == "" && strings.HasPrefix(path, "/categories/") {
			name = r.nameOf("category_id", id)
		}
		if name == "" && strings.HasPrefix(path, "/series/") {
			var series struct {
				DisplayName string `json:"display_name"`
				Description string `json:"description"`
			}
			if err := r.a.get(ctx, "/series/"+id.String(), nil, &series); err == nil {
				name = textutil.FirstNonBlank(series.DisplayName, series.Description)
			}
		}
	}
	if name != "" {
		r.names[id.String()] = name
	}
}
