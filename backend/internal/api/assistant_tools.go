package api

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// What the assistant reads. The tools live here because the loaders do: the
// same postings, balance function and plan engine the screens use, so the
// assistant never quotes a number no screen shows.
//
// Every one is read-only; the catalogue in internal/domain says so and a test
// enforces it.

// assistantTools answers tool calls for one request. It carries the space
// context because the loaders it reuses do.
type assistantTools struct {
	env *Env
	sp  auth.SpaceContext
	// strictKinds refuses an income category for money out, and holds money
	// in under an expense category (a refund) as a card even where changes
	// apply unasked. On for an automation, where nobody is watching; off in
	// chat.
	strictKinds bool
}

func (a assistantTools) Run(
	ctx context.Context, name string, arguments map[string]any,
) (any, error) {
	switch name {
	case "list_accounts":
		return a.accounts(ctx)
	case "search_transactions":
		return a.transactions(ctx, arguments)
	case "spending_by_category":
		return a.spendingByCategory(ctx, arguments)
	case "net_worth":
		return a.netWorth(ctx)
	case "spending_plan":
		return a.spendingPlan(ctx, arguments)
	case "portfolio_holdings":
		return a.holdings(ctx, arguments)
	case "upcoming_bills":
		return a.upcomingBills(ctx, arguments)
	case "income_and_expense":
		return a.incomeAndExpense(ctx, arguments)
	case "recurring_series":
		return a.series(ctx)
	case "cash_flow":
		return a.cashFlow(ctx, arguments)
	case "savings_goals":
		return a.goals(ctx)
	case "watchlists":
		return a.watchlists(ctx, arguments)
	case "list_categories":
		return a.categories(ctx)
	case "list_tags":
		return a.tags(ctx)
	case "list_rules":
		return a.rules(ctx)
	case "recent_alerts":
		return a.alerts(ctx, arguments)
	case "list_mail":
		return a.listMail(ctx, arguments)
	case "read_mail":
		return a.readMail(ctx, arguments)
	case "find":
		return a.find(ctx, arguments)
	case "list_endpoints":
		return a.endpoints(arguments)
	case "read_endpoint":
		return a.readEndpoint(ctx, arguments)
	default:
		// A write tool reaches Propose, not here, so landing here means the
		// space has changes switched off. Saying so stops the model retrying
		// under the belief the tool does not exist.
		if tool, known := domain.AssistantToolByName(name); known && tool.Writes {
			return nil, fmt.Errorf(
				"%q would change something, and this household has not switched changes on "+
					"for the assistant", name)
		}
		return nil, fmt.Errorf("there is no tool called %q", name)
	}
}

func (a assistantTools) accounts(ctx context.Context) (any, error) {
	accounts, err := a.env.DB.ListAccounts(ctx, a.sp.ID(), store.AccountQuery{})
	if err != nil {
		return nil, err
	}
	postings, err := postingsByAccount(ctx, a.env, a.sp, accounts)
	if err != nil {
		return nil, err
	}

	type row struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Currency string `json:"currency"`
		Balance  string `json:"balance"`
		IsClosed bool   `json:"is_closed"`
		// RequiresReceipts is an account whose spending rows each need one.
		RequiresReceipts bool `json:"requires_receipts,omitempty"`
	}
	out := make([]row, 0, len(accounts))
	for _, account := range accounts {
		one := store.DomainAccount(account)
		out = append(out, row{
			ID: account.ID.String(), Name: account.Name, Kind: string(one.Kind),
			Currency: account.Currency, IsClosed: account.IsClosed,
			Balance:          domain.AccountBalance(one, postings[account.ID]).String(),
			RequiresReceipts: one.RequiresReceipts,
		})
	}
	return map[string]any{"accounts": out}, nil
}

func (a assistantTools) transactions(
	ctx context.Context, arguments map[string]any,
) (any, error) {
	from, to, err := toolWindow(arguments)
	if err != nil {
		return nil, err
	}
	limit, err := toolLimit(arguments, 40, 100)
	if err != nil {
		return nil, err
	}
	window, err := ResolveWindow(from, true, to, true, domain.DatePosted)
	if err != nil {
		return nil, err
	}
	// The register's own query and matcher, so the tool finds what the
	// Transactions screen finds for the same search.
	query := RegisterQuery{
		Window:     window,
		Search:     toolString(arguments, "search"),
		Descending: true,
	}
	if toolString(arguments, "account_id") != "" {
		id, err := toolUUID(arguments, "account_id")
		if err != nil {
			return nil, err
		}
		query.Accounts = accountFilter{IDs: []uuid.UUID{id}, Given: true}
	}
	if toolBool(arguments, "unreviewed") {
		query.HasReviewed, query.IsReviewed = true, false
	}
	category := domain.ID("")
	if toolString(arguments, "category_id") != "" {
		id, err := toolUUID(arguments, "category_id")
		if err != nil {
			return nil, err
		}
		category = domain.ID(id.String())
	}
	uncategorized := toolBool(arguments, "uncategorized")
	missingReceipt := toolBool(arguments, "missing_receipt")

	matched, err := matchRegister(ctx, a.env, a.sp, query)
	if err != nil {
		return nil, err
	}
	documented, err := a.env.DB.TransactionsWithDocuments(ctx, a.sp.ID())
	if err != nil {
		return nil, err
	}
	receipts, err := service.ReceiptStatuses(ctx, a.env.DB, a.sp.ID(), matched.Rows, documented)
	if err != nil {
		return nil, err
	}

	type row struct {
		ID string `json:"id"`
		// Date is the posted date, which is what the register shows.
		Date       string `json:"date"`
		Payee      string `json:"payee"`
		Amount     string `json:"amount"`
		Account    string `json:"account"`
		Category   string `json:"category"`
		IsReviewed bool   `json:"is_reviewed"`
		// Receipt is set only on a row its account holds to a receipt.
		Receipt domain.ReceiptStatus `json:"receipt,omitempty"`
	}
	accounts, err := a.env.DB.ListAccounts(ctx, a.sp.ID(),
		store.AccountQuery{IncludeClosed: true, IncludeDeleted: true})
	if err != nil {
		return nil, err
	}
	names := map[uuid.UUID]string{}
	for _, account := range accounts {
		names[account.ID] = account.Name
	}
	categories, err := a.env.DB.ListCategories(ctx, a.sp.ID(), true)
	if err != nil {
		return nil, err
	}
	categoryNames := make(map[uuid.UUID]string, len(categories))
	for _, category := range categories {
		categoryNames[category.ID] = category.Name
	}

	out := make([]row, 0, limit)
	count := 0
	for _, posting := range matched.Postings {
		// A split row carries its categories on the splits and the parent's is
		// nulled, so both questions are asked of the allocations.
		if uncategorized && !posting.Txn.IsUncategorized() {
			continue
		}
		if category != "" && !filedUnder(posting, category) {
			continue
		}
		key := uuid.MustParse(string(posting.Txn.ID))
		if missingReceipt && receipts[key] != domain.ReceiptMissing {
			continue
		}
		count++
		if len(out) >= limit {
			continue
		}
		one := matched.Rows[key]
		out = append(out, row{
			ID: one.ID.String(), Date: one.Date.String(),
			Payee:  posting.Txn.DisplayPayee(),
			Amount: one.Amount.String(), Account: names[one.AccountID],
			Category: categoryNames[one.CategoryID], IsReviewed: one.IsReviewed,
			Receipt: receipts[key],
		})
	}
	// The whole window is matched before the page is cut, so the count is
	// the true number of matches, not the length of a page.
	return map[string]any{
		"transactions": out, "returned": len(out), "count": count,
		"has_more": count > len(out),
	}, nil
}

// filedUnder reports whether any part of the posting is filed under category.
func filedUnder(posting domain.Posting, category domain.ID) bool {
	for _, part := range domain.Allocations(posting) {
		if part.CategoryID() == category {
			return true
		}
	}
	return false
}

func (a assistantTools) spendingByCategory(
	ctx context.Context, arguments map[string]any,
) (any, error) {
	from, to, err := toolWindow(arguments)
	if err != nil {
		return nil, err
	}
	window, err := ResolveWindow(from, true, to, true, domain.DateEffective)
	if err != nil {
		return nil, err
	}
	// The expenses report's own allocations: the category decides what is
	// spending, so a refund filed under Groceries lowers Groceries.
	allocations, err := selectAllocations(ctx, a.env, a.sp, window,
		ReportConfig{Sign: "expenses"}, uuid.Nil, false)
	if err != nil {
		return nil, err
	}

	totals := map[string]domain.Money{}
	for _, one := range allocations {
		name := one.Category.Name
		if !one.HasCategory {
			name = "Uncategorized"
		}
		totals[name] = totals[name].Sub(one.Amount)
	}

	type row struct {
		Category string `json:"category"`
		Spent    string `json:"spent"`
	}
	out := make([]row, 0, len(totals))
	for name, total := range totals {
		out = append(out, row{Category: name, Spent: total.String()})
	}
	sort.Slice(out, func(i, j int) bool {
		left := domain.MustFromString(out[i].Spent)
		right := domain.MustFromString(out[j].Spent)
		if !left.Equal(right) {
			return left.GreaterThan(right)
		}
		return out[i].Category < out[j].Category
	})
	return map[string]any{"from": from.String(), "to": to.String(), "categories": out}, nil
}

// netWorth is today's point from the Net Worth screen's own endpoint. Debt
// kinds are positive there, as "how much do we owe" means them.
func (a assistantTools) netWorth(ctx context.Context) (any, error) {
	today := domain.DateOf(a.env.now())
	var answer struct {
		End struct {
			Assets domain.Money `json:"assets"`
			Debt   domain.Money `json:"debt"`
			Net    domain.Money `json:"net"`
			ByKind []struct {
				Kind   string       `json:"kind"`
				Amount domain.Money `json:"amount"`
			} `json:"by_kind"`
		} `json:"end"`
	}
	if err := a.get(ctx, "/net-worth", windowQuery(today, today), &answer); err != nil {
		return nil, err
	}
	groups := make(map[string]string, len(answer.End.ByKind))
	for _, one := range answer.End.ByKind {
		groups[one.Kind] = one.Amount.String()
	}
	return map[string]any{
		"net_worth": answer.End.Net.String(),
		"assets":    answer.End.Assets.String(),
		"debts":     answer.End.Debt.String(),
		"by_kind":   groups,
		"as_of":     today.String(),
	}, nil
}

func (a assistantTools) spendingPlan(
	ctx context.Context, arguments map[string]any,
) (any, error) {
	month, err := toolMonth(arguments, a.env.now())
	if err != nil {
		return nil, err
	}
	view, err := plan(a.env).Compute(ctx, a.sp.ID(), month)
	if err != nil {
		return nil, err
	}
	rendered := monthResponse(view)

	buckets := make(map[string]string, len(rendered.Buckets))
	for _, bucket := range rendered.Buckets {
		buckets[string(bucket.Key)] = bucket.EffectiveAmount.String()
	}
	return map[string]any{"month": month.String(), "buckets": buckets}, nil
}

// upcomingBills is the household's reminders: what falls due in [from, to],
// and every bill still unpaid from before it, as the Reminders strip shows
// them.
func (a assistantTools) upcomingBills(
	ctx context.Context, arguments map[string]any,
) (any, error) {
	from, to, err := toolWindow(arguments)
	if err != nil {
		return nil, err
	}
	reachBack := domain.DateOf(a.env.now()).AddDays(-reminderDaysBack)
	if from.Before(reachBack) {
		reachBack = from
	}
	reminders, err := loadReminders(ctx, a.env, a.sp, reachBack, to)
	if err != nil {
		return nil, err
	}

	type row struct {
		Name   string `json:"name"`
		DueOn  string `json:"due_on"`
		Amount string `json:"amount"`
		// PaysOn is the day the money leaves for an autopaying bill, omitted
		// when nobody knows.
		PaysOn string `json:"pays_on,omitempty"`
		Status string `json:"status"`
		// PayManually marks a provider's newest unpaid statement that no
		// autopay takes and no recurring reminder carries.
		PayManually bool `json:"pay_manually,omitempty"`
	}
	out := make([]row, 0)
	for _, one := range reminders {
		if one.DueOn.Before(from) && one.Status != entryPastDue {
			continue
		}
		item := row{
			Name: one.Label, DueOn: one.DueOn.String(), Amount: one.Amount.String(),
			Status: one.Status, PayManually: one.SeriesID == "",
		}
		if !one.PaysOn.IsZero() {
			item.PaysOn = one.PaysOn.String()
		}
		out = append(out, item)
	}
	return map[string]any{"from": from.String(), "to": to.String(), "expected": out}, nil
}

// --- argument reading --------------------------------------------------------

func toolString(arguments map[string]any, key string) string {
	value, _ := arguments[key].(string)
	return strings.TrimSpace(value)
}

// toolLimit is the `limit` argument, refused outside 1..ceiling as
// queryLimit refuses it.
func toolLimit(arguments map[string]any, fallback, ceiling int) (int, error) {
	var raw string
	switch value := arguments["limit"].(type) {
	case nil:
		return fallback, nil
	case float64:
		raw = strconv.FormatFloat(value, 'f', -1, 64)
	case string:
		if raw = strings.TrimSpace(value); raw == "" {
			return fallback, nil
		}
	default:
		return 0, fmt.Errorf("`limit` has to be a whole number")
	}
	limit, err := parseBoundedInt(raw, 1, ceiling)
	if err != nil {
		return 0, fmt.Errorf("`limit` has to be a whole number from 1 to %d, not %s", ceiling, raw)
	}
	return limit, nil
}

// toolWindow reads the date range, refusing rather than guessing: a guessed
// range answers confidently about the wrong months.
func toolWindow(arguments map[string]any) (domain.Date, domain.Date, error) {
	from, err := parseDate(toolString(arguments, "from"))
	if err != nil {
		return domain.Date{}, domain.Date{}, fmt.Errorf("`from` must be a date like 2026-08-01")
	}
	to, err := parseDate(toolString(arguments, "to"))
	if err != nil {
		return domain.Date{}, domain.Date{}, fmt.Errorf("`to` must be a date like 2026-08-31")
	}
	if to.Before(from) {
		return domain.Date{}, domain.Date{}, fmt.Errorf("`to` is before `from`")
	}
	return from, to, nil
}

// holdings answers what the portfolio holds, from `loadPortfolio`, the same
// computation the Investments table renders. Nulls are carried through rather
// than flattened to zero: no quote means no price, and no recorded basis means
// no gain, not a worthless or break-even holding.
func (a assistantTools) holdings(ctx context.Context, arguments map[string]any) (any, error) {
	book, err := loadPortfolio(ctx, a.env, a.sp, accountFilter{})
	if err != nil {
		return nil, err
	}

	symbol := strings.ToUpper(strings.TrimSpace(toolString(arguments, "symbol")))
	accounts, err := a.env.DB.ListAccounts(ctx, a.sp.ID(), store.AccountQuery{})
	if err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]string, len(accounts))
	for _, account := range accounts {
		names[account.ID] = account.Name
	}

	out := make([]map[string]any, 0, len(book.Items))
	for _, item := range book.Items {
		if symbol != "" && item.Symbol != symbol {
			continue
		}
		out = append(out, map[string]any{
			"symbol":           item.Symbol,
			"name":             item.Name,
			"account":          names[item.AccountID],
			"shares":           item.Shares.String(),
			"price":            rateText(item.Price),
			"market_value":     item.MarketValue.String(),
			"cost_basis":       moneyText(item.CostBasis),
			"total_gain":       moneyText(item.TotalGain),
			"total_gain_pct":   rateText(item.TotalGainPct),
			"day_change":       moneyText(item.DayChange),
			"day_change_pct":   rateText(item.DayChangePct),
			"has_public_quote": !item.IsUnquoted,
		})
	}
	if symbol != "" && len(out) == 0 {
		return nil, fmt.Errorf("no holding with the symbol %q", symbol)
	}
	return map[string]any{"holdings": out}, nil
}

// moneyText and rateText keep a null null. The tool's description says what
// each absence means; a zero would say something else entirely.
func moneyText(m *domain.Money) any {
	if m == nil {
		return nil
	}
	return m.String()
}

func rateText(r *domain.Rate) any {
	if r == nil {
		return nil
	}
	return r.String()
}
