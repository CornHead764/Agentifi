package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The rest of what the assistant can read. Most tools go through `dispatch`:
// the same GET the screen issues, with the JSON trimmed, so the assistant gets
// the page's own figures. The trimming matters because a tool result goes into
// the model's context window, and `/cash-flow` alone answers a point per
// account per day.
//
// `income_and_expense` computes rather than dispatches; see incomeAndExpense.

// get issues a GET against this server's own API and decodes the answer. A
// refusal comes back as an error with the API's message, which the tool loop
// hands to the model so it can correct a bad id next round.
func (a assistantTools) get(ctx context.Context, path string, query url.Values, into any) error {
	response, err := a.env.dispatch(ctx, a.sp, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	if !response.OK() {
		return fmt.Errorf("%s answered %d: %s", path, response.Status,
			strings.TrimSpace(response.Body))
	}
	if response.Truncated {
		return fmt.Errorf("%s answered with more than can be read at once; "+
			"narrow it with a date range or a filter", path)
	}
	if into == nil {
		return nil
	}
	if !response.IsJSON() {
		return fmt.Errorf("%s did not answer a record", path)
	}
	if err := json.Unmarshal([]byte(response.Body), into); err != nil {
		return fmt.Errorf("%s answered something unreadable: %w", path, err)
	}
	return nil
}

// windowQuery is the from/to pair as the list endpoints take it.
func windowQuery(from, to domain.Date) url.Values {
	return url.Values{"from": {from.String()}, "to": {to.String()}}
}

// --- Reports -----------------------------------------------------------------

// incomeAndExpense is the month-by-month trend, computed from the report path's
// own allocations rather than dispatched: /reports/monthly-summary answers one
// month against its predecessor, so twelve months would be twenty-four passes.
func (a assistantTools) incomeAndExpense(
	ctx context.Context, arguments map[string]any,
) (any, error) {
	from, to, err := toolWindow(arguments)
	if err != nil {
		return nil, err
	}

	type row struct {
		Month    string `json:"month"`
		Income   string `json:"income"`
		Expenses string `json:"expenses"`
		Net      string `json:"net"`
	}
	out := make([]row, 0)
	income, expenses, net := domain.Zero, domain.Zero, domain.Zero

	// Bounded so a model asking about "the last few years" does not walk a
	// hundred months one allocation at a time.
	const maxMonths = 36
	for month := domain.MonthOf(from); !month.FirstDay().After(to); month = month.Next() {
		if len(out) >= maxMonths {
			return nil, fmt.Errorf(
				"that is more than %d months; ask about a shorter range", maxMonths)
		}
		rows, err := monthAllocations(ctx, a.env, a.sp, month)
		if err != nil {
			return nil, err
		}
		totals := reportTotals(rows)
		income = income.Add(totals.Income)
		expenses = expenses.Add(totals.Expenses)
		net = net.Add(totals.Net)
		out = append(out, row{
			Month: month.String(), Income: totals.Income.String(),
			Expenses: totals.Expenses.String(), Net: totals.Net.String(),
		})
	}

	return map[string]any{
		"months": out,
		"total": map[string]string{
			"income": income.String(), "expenses": expenses.String(), "net": net.String(),
		},
	}, nil
}

// --- Bills, income and the projection ----------------------------------------

func (a assistantTools) series(ctx context.Context) (any, error) {
	var rows []struct {
		ID         uuid.UUID `json:"id"`
		Label      string    `json:"label"`
		Amount     string    `json:"amount"`
		Kind       string    `json:"kind"`
		IsActive   bool      `json:"is_active"`
		DueOn      string    `json:"due_on"`
		Recurrence struct {
			Alias     string `json:"alias"`
			Frequency string `json:"frequency"`
			Interval  int    `json:"interval"`
			ByMonth   []int  `json:"by_month"`
		} `json:"recurrence"`
		AnnualizedAmount   string `json:"annualized_amount"`
		OccurrencesPerYear int    `json:"occurrences_per_year"`
	}
	if err := a.get(ctx, "/series", nil, &rows); err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(rows))
	for _, one := range rows {
		out = append(out, map[string]any{
			"id": one.ID.String(), "name": one.Label, "amount": one.Amount,
			"kind": one.Kind, "repeats": repeatsText(one.Recurrence.Alias,
				one.Recurrence.Frequency, one.Recurrence.Interval) + activeMonthsText(one.Recurrence.ByMonth),
			"next_due_on": one.DueOn, "is_active": one.IsActive,
			"a_year": one.AnnualizedAmount, "times_a_year": one.OccurrencesPerYear,
		})
	}
	return map[string]any{"series": out}, nil
}

// cashFlow answers "will we make it to payday" with the opening balance, the
// projection's lowest point and when, and the bills against it, rather than a
// running balance per account per day.
func (a assistantTools) cashFlow(ctx context.Context, arguments map[string]any) (any, error) {
	from, to, err := toolWindow(arguments)
	if err != nil {
		return nil, err
	}
	query := windowQuery(from, to)
	if raw := toolString(arguments, "account_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return nil, fmt.Errorf("%q is not an account id", raw)
		}
		query.Set("account_id", raw)
	}

	var answer struct {
		Accounts []struct {
			Name            string `json:"name"`
			StartingBalance string `json:"starting_balance"`
			Lowest          *struct {
				On      string `json:"on"`
				Balance string `json:"balance"`
			} `json:"lowest"`
		} `json:"accounts"`
		Combined []struct {
			On      string `json:"on"`
			Balance string `json:"balance"`
		} `json:"combined"`
		Occurrences []struct {
			Label  string `json:"label"`
			DueOn  string `json:"due_on"`
			Amount string `json:"amount"`
		} `json:"occurrences"`
	}
	if err := a.get(ctx, "/cash-flow", query, &answer); err != nil {
		return nil, err
	}

	accounts := make([]map[string]any, 0, len(answer.Accounts))
	for _, one := range answer.Accounts {
		row := map[string]any{"account": one.Name, "starting_balance": one.StartingBalance}
		if one.Lowest != nil {
			row["lowest_balance"] = one.Lowest.Balance
			row["lowest_on"] = one.Lowest.On
		}
		accounts = append(accounts, row)
	}
	expected := make([]map[string]any, 0, len(answer.Occurrences))
	for _, one := range answer.Occurrences {
		expected = append(expected,
			map[string]any{"name": one.Label, "due_on": one.DueOn, "amount": one.Amount})
	}

	out := map[string]any{
		"from": from.String(), "to": to.String(),
		"accounts": accounts, "expected": expected,
	}
	if len(answer.Combined) > 0 {
		out["combined_opening"] = answer.Combined[0].Balance
		out["combined_closing"] = answer.Combined[len(answer.Combined)-1].Balance
	}
	return out, nil
}

// --- Goals, watchlists, and the reference lists ------------------------------

func (a assistantTools) goals(ctx context.Context) (any, error) {
	var rows []struct {
		ID           uuid.UUID `json:"id"`
		Name         string    `json:"name"`
		TargetAmount string    `json:"target_amount"`
		SavedSoFar   string    `json:"saved_so_far"`
		LeftToSave   string    `json:"left_to_save"`
		PctComplete  *string   `json:"pct_complete"`
		TargetOn     *string   `json:"target_on"`
	}
	if err := a.get(ctx, "/goals", nil, &rows); err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(rows))
	for _, one := range rows {
		out = append(out, map[string]any{
			"id": one.ID.String(), "name": one.Name, "target": one.TargetAmount,
			"saved": one.SavedSoFar, "left_to_save": one.LeftToSave,
			"percent_done": one.PctComplete, "target_on": one.TargetOn,
		})
	}
	return map[string]any{"goals": out}, nil
}

func (a assistantTools) watchlists(ctx context.Context, arguments map[string]any) (any, error) {
	query := url.Values{}
	if toolString(arguments, "month") != "" {
		month, err := toolMonth(arguments, a.env.now())
		if err != nil {
			return nil, err
		}
		query.Set("month", month.String())
	}

	var rows []struct {
		ID           uuid.UUID `json:"id"`
		Name         string    `json:"name"`
		Period       string    `json:"period"`
		TargetAmount *string   `json:"target_amount"`
		LeftToTarget *string   `json:"left_to_target"`
		IsOverTarget bool      `json:"is_over_target"`
		ThisMonth    string    `json:"this_month_spent"`
		YearToDate   string    `json:"year_to_date"`
	}
	if err := a.get(ctx, "/watchlists", query, &rows); err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(rows))
	for _, one := range rows {
		out = append(out, map[string]any{
			"id": one.ID.String(), "name": one.Name, "period": one.Period,
			"target": one.TargetAmount, "left_to_target": one.LeftToTarget,
			"is_over_target":   one.IsOverTarget,
			"spent_this_month": one.ThisMonth, "spent_this_year": one.YearToDate,
		})
	}
	return map[string]any{"watchlists": out}, nil
}

// categories answers with the tree flattened and the parent named, so two
// "Groceries" under different parents can be told apart, plus the id.
func (a assistantTools) categories(ctx context.Context) (any, error) {
	rows, err := a.env.DB.ListCategories(ctx, a.sp.ID(), false)
	if err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]string, len(rows))
	for _, one := range rows {
		names[one.ID] = one.Name
	}

	out := make([]map[string]any, 0, len(rows))
	for _, one := range rows {
		if !store.DomainCategory(one).CanBeSuggested() {
			continue
		}
		row := map[string]any{
			"id": one.ID.String(), "name": one.Name, "kind": string(one.Kind),
		}
		if one.ParentID != uuid.Nil {
			row["parent"] = names[one.ParentID]
		}
		out = append(out, row)
	}
	return map[string]any{"categories": out}, nil
}

func (a assistantTools) tags(ctx context.Context) (any, error) {
	rows, err := a.env.DB.ListTags(ctx, a.sp.ID(), false)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, one := range rows {
		out = append(out, map[string]any{"id": one.ID.String(), "name": one.Name})
	}
	return map[string]any{"tags": out}, nil
}

func (a assistantTools) rules(ctx context.Context) (any, error) {
	var rows []RuleResponse
	if err := a.get(ctx, "/rules", nil, &rows); err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(rows))
	for _, one := range rows {
		row := map[string]any{
			"id": one.ID.String(), "name": one.Name,
			"priority": one.Priority, "is_active": one.IsActive,
		}
		// Only the actions that are set. A row of seven nulls says nothing
		// about what the rule does and costs as much to read as one that does.
		sets := map[string]any{}
		if one.Actions.SetPayee != nil {
			sets["set_payee"] = *one.Actions.SetPayee
		}
		if one.Actions.SetCategoryID != nil {
			sets["set_category_id"] = one.Actions.SetCategoryID.String()
		}
		if one.Actions.SetNotes != nil {
			sets["set_notes"] = *one.Actions.SetNotes
		}
		if len(one.Actions.AddTagIDs) > 0 {
			sets["add_tag_ids"] = one.Actions.AddTagIDs
		}
		row["sets"] = sets
		out = append(out, row)
	}
	return map[string]any{"rules": out}, nil
}

func (a assistantTools) alerts(ctx context.Context, arguments map[string]any) (any, error) {
	limit, err := toolLimit(arguments, 20, 50)
	if err != nil {
		return nil, err
	}
	rows, err := a.env.DB.ListNotifications(ctx, a.sp.ID(), a.sp.UserID(), store.NotificationQuery{
		Limit: limit,
	})
	if err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(rows))
	for _, one := range rows {
		out = append(out, map[string]any{
			"type": string(one.AlertType), "title": one.Title, "body": one.Body,
			"at": one.CreatedAt.Format(time.RFC3339), "is_read": one.ReadAt != nil,
		})
	}
	return map[string]any{"alerts": out}, nil
}

// --- The escape hatches ------------------------------------------------------

// endpoints is the route registry, read from the registry itself so what the
// model is told exists and what the dispatcher serves cannot drift.
func (a assistantTools) endpoints(arguments map[string]any) (any, error) {
	search := strings.ToLower(toolString(arguments, "search"))

	out := make([]map[string]any, 0)
	for _, route := range dispatchableRoutes() {
		path := route.Path()
		if search != "" && !strings.Contains(strings.ToLower(path), search) {
			continue
		}
		out = append(out, map[string]any{
			"method": route.Method, "path": path,
			"writes": route.Method != http.MethodGet,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no endpoint's path contains %q", search)
	}
	return map[string]any{"endpoints": out, "count": len(out)}, nil
}

// readEndpoint is a GET against anything in that list, answering the raw body,
// for the questions no named tool anticipated.
func (a assistantTools) readEndpoint(
	ctx context.Context, arguments map[string]any,
) (any, error) {
	path := toolString(arguments, "path")
	if path == "" {
		return nil, fmt.Errorf("`path` is required; call list_endpoints for what there is")
	}
	if strings.Contains(path, "{") {
		return nil, fmt.Errorf(
			"%q still has a placeholder in it — put a real id in place of it", path)
	}

	if mailTextPath(path) {
		return nil, fmt.Errorf("read a message's text with read_mail instead")
	}

	query, err := toolQuery(arguments)
	if err != nil {
		return nil, err
	}
	response, err := a.env.dispatch(ctx, a.sp, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		return nil, fmt.Errorf("GET %s answered %d: %s", path, response.Status,
			strings.TrimSpace(response.Body))
	}
	if response.Truncated {
		return nil, fmt.Errorf("GET %s answered with more than can be read at once; "+
			"narrow it with a date range or a limit", path)
	}
	// An attachment answers bytes and an export answers CSV; neither is of use
	// in a model's context. Refused by name so it does not retry the path.
	if !response.IsJSON() {
		return nil, fmt.Errorf("%s answers a file rather than a record, which cannot be "+
			"read here", path)
	}

	var body any
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		return map[string]any{"path": path, "body": response.Body}, nil
	}
	return map[string]any{"path": path, "body": body}, nil
}

// toolQuery reads the query-string object a model passed, coercing values
// rather than requiring strings: `{"limit": 20}` said what it meant.
func toolQuery(arguments map[string]any) (url.Values, error) {
	raw, present := arguments["query"]
	if !present || raw == nil {
		return nil, nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("`query` has to be an object of parameters")
	}
	out := url.Values{}
	for key, value := range fields {
		switch typed := value.(type) {
		case nil:
		case string:
			out.Set(key, typed)
		case bool:
			out.Set(key, fmt.Sprintf("%t", typed))
		case float64:
			// Formatted without an exponent and without a trailing ".0":
			// a limit of 20 must not reach a handler as "2e+01".
			out.Set(key, strconv.FormatFloat(typed, 'f', -1, 64))
		case []any:
			for _, item := range typed {
				out.Add(key, fmt.Sprintf("%v", item))
			}
		default:
			return nil, fmt.Errorf("`query.%s` is not a value a query string can carry", key)
		}
	}
	return out, nil
}

func toolBool(arguments map[string]any, key string) bool {
	value, _ := arguments[key].(bool)
	return value
}

// repeatsText says how often a series falls, in the words the dropdown uses.
// The alias is absent on a series imported with a rule nobody chose from that
// list, and then the frequency and interval are used.
// activeMonthsText names a seasonal rule's months, so "every month" is not
// read as twelve bills a year.
func activeMonthsText(months []int) string {
	if len(months) == 0 {
		return ""
	}
	names := make([]string, 0, len(months))
	for _, month := range months {
		names = append(names, time.Month(month).String())
	}
	return ", only in " + strings.Join(names, ", ")
}

func repeatsText(alias, frequency string, interval int) string {
	if alias != "" {
		return strings.ToLower(strings.ReplaceAll(alias, "_", " "))
	}
	if frequency == "" {
		return ""
	}
	if interval > 1 {
		return fmt.Sprintf("every %d %s", interval, strings.ToLower(frequency))
	}
	return strings.ToLower(frequency)
}
