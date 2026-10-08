package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Reports: one engine, two renderings, and a gallery of presets.
//
// The engine's report types are presets over two knobs plus a sign convention
// (calculations.md §11): one selection path, one grouping path, and a table of
// defaults. The presets that read something else name their own endpoint.
//
//   - Splits are the unit, not transactions. Every grouping runs over
//     allocations; a transaction with no splits is one allocation.
//   - Monthly Summary's Top Categories and Top Payees exclude bills and
//     subscriptions; that is what separates it from the Spending report.
//   - A percentage against a zero prior period is null. The client renders an
//     em dash.
//   - Running a report is a GET, so viewers can run them.
//
// A saved report is a row in the filters table with scope "report": the filter
// is the report's filter (ground rule 3), and the knobs and the sign ride in
// query_text as JSON.

func init() {
	Register(Resource{Prefix: "/reports", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/presets", listReportPresets)
		rt.Read(http.MethodGet, "/run", runReport)
		rt.Read(http.MethodGet, "/monthly-summary", readMonthlySummary)
		rt.Read(http.MethodGet, "/savings", readSavingsReport)
		rt.Read(http.MethodGet, "/spending", readSpendingReport)

		rt.Read(http.MethodGet, "/", listSavedReports)
		rt.Write(http.MethodPost, "/", createSavedReport)
		rt.Read(http.MethodGet, "/{report_id}", readSavedReport)
		rt.Write(http.MethodPatch, "/{report_id}", updateSavedReport)
		rt.Write(http.MethodDelete, "/{report_id}", deleteSavedReport)
	}})
}

// reportScope is the filter scope a saved report is stored under.
const reportScope = "report"

// topEntries is how many rows the Monthly Summary's two lists carry.
const topEntries = 5

// --- Configuration -----------------------------------------------------------

// ReportConfig is the report shell: two knobs and a sign convention.
type ReportConfig struct {
	// Preset names the gallery entry this configuration came from. It is a
	// label over the fields below, never a branch in the engine.
	Preset string `json:"preset"`
	// Mode is "transaction" — rows grouped by a hierarchy with a subtotal at
	// every level — or "summary", a pivot of rows by columns.
	Mode string `json:"mode"`
	// Rows is the row dimension: category, account, tag, payee or txf.
	Rows string `json:"rows"`
	// Columns is the column dimension, summary mode only: time, account, tag
	// or payee.
	Columns string `json:"columns"`
	// TimeGrain applies when Columns is time: day, week, month, quarter, year.
	TimeGrain string `json:"time_grain"`
	// Sign is which side of the ledger the report shows: expenses, income or
	// both. Storage never flips a sign; this only selects.
	Sign string `json:"sign"`
}

// reportPreset is one gallery entry.
type reportPreset struct {
	Config ReportConfig
	Label  string
	// ServedBy names the endpoint that answers this preset when the engine
	// does not: Net Worth is balances, and the Monthly Summary a snapshot.
	ServedBy string
}

// reportPresets is the gallery.
var reportPresets = map[string]reportPreset{
	// Spending sets periods side by side by category, payee or tag: its own
	// resource over the register's aggregate. A saved one keeps its period
	// length in TimeGrain and its grouping in Rows.
	"spending": {Label: "Spending", ServedBy: "/reports/spending", Config: ReportConfig{
		Mode: "summary", Rows: "category", Columns: "time", TimeGrain: "month", Sign: "expenses"}},
	"income": {Label: "Income", Config: ReportConfig{
		Mode: "transaction", Rows: "category", Sign: "income"}},
	"income_summary": {Label: "Income Summary", Config: ReportConfig{
		Mode: "summary", Rows: "category", Columns: "time", TimeGrain: "month", Sign: "income"}},
	"income_expense": {Label: "Income & Expense", Config: ReportConfig{
		Mode: "summary", Rows: "category", Columns: "time", TimeGrain: "month", Sign: "both"}},
	// Savings is balances, not transactions: its own resource, like Net Worth.
	"savings": {Label: "Savings", ServedBy: "/reports/savings"},
	"taxes": {Label: "Taxes", Config: ReportConfig{
		Mode: "transaction", Rows: "txf", Sign: "both"}},
	"net_worth":       {Label: "Net Worth", ServedBy: "/net-worth"},
	"monthly_summary": {Label: "Monthly Summary", ServedBy: "/reports/monthly-summary"},
}

type ReportPresetResponse struct {
	Preset string       `json:"preset"`
	Label  string       `json:"label"`
	Config ReportConfig `json:"config"`
	// ServedBy is null for the presets this engine answers.
	ServedBy *string `json:"served_by"`
}

func listReportPresets(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	names := make([]string, 0, len(reportPresets))
	for name := range reportPresets {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]ReportPresetResponse, 0, len(names))
	for _, name := range names {
		preset := reportPresets[name]
		config := preset.Config
		config.Preset = name
		out = append(out, ReportPresetResponse{
			Preset:   name,
			Label:    preset.Label,
			Config:   config,
			ServedBy: dbconv.NullText(preset.ServedBy),
		})
	}
	return writeJSON(w, http.StatusOK, out)
}

// reportConfigFromRequest reads the shell off the query string, a preset
// supplying whatever was left out.
func reportConfigFromRequest(r *http.Request) (ReportConfig, error) {
	var config ReportConfig
	if name := strings.TrimSpace(r.URL.Query().Get("preset")); name != "" {
		preset, known := reportPresets[name]
		if !known {
			return ReportConfig{}, errInvalid("enum", []string{"query", "preset"},
				"%q is not a report preset", name)
		}
		if preset.ServedBy != "" {
			return ReportConfig{}, errInvalid("wrong_endpoint", []string{"query", "preset"},
				"the %s report is served by %s, not by this engine", name, preset.ServedBy)
		}
		config = preset.Config
		config.Preset = name
	}

	query := r.URL.Query()
	if value := strings.TrimSpace(query.Get("mode")); value != "" {
		config.Mode = value
	}
	if value := strings.TrimSpace(query.Get("rows")); value != "" {
		config.Rows = value
	}
	if value := strings.TrimSpace(query.Get("columns")); value != "" {
		config.Columns = value
	}
	if value := strings.TrimSpace(query.Get("time_grain")); value != "" {
		config.TimeGrain = value
	}
	if value := strings.TrimSpace(query.Get("sign")); value != "" {
		config.Sign = value
	}
	return checkReportConfig(config)
}

func checkReportConfig(config ReportConfig) (ReportConfig, error) {
	if config.Mode == "" {
		config.Mode = "transaction"
	}
	if config.Rows == "" {
		config.Rows = "category"
	}
	if config.Sign == "" {
		config.Sign = "both"
	}
	switch config.Mode {
	case "transaction":
		config.Columns, config.TimeGrain = "", ""
	case "summary":
		if config.Columns == "" {
			config.Columns = "time"
		}
		if config.Columns == "time" && config.TimeGrain == "" {
			config.TimeGrain = "month"
		}
		if config.Columns != "time" {
			config.TimeGrain = ""
		}
	default:
		return ReportConfig{}, errInvalid("enum", []string{"query", "mode"},
			"mode must be transaction or summary, got %q", config.Mode)
	}

	switch config.Rows {
	case "category", "account", "tag", "payee", "txf":
	default:
		return ReportConfig{}, errInvalid("enum", []string{"query", "rows"},
			"rows must be category, account, tag, payee or txf, got %q", config.Rows)
	}
	if config.Mode == "summary" {
		switch config.Columns {
		case "time", "account", "tag", "payee":
		default:
			return ReportConfig{}, errInvalid("enum", []string{"query", "columns"},
				"columns must be time, account, tag or payee, got %q", config.Columns)
		}
		if config.Columns == "time" {
			switch config.TimeGrain {
			case "day", "week", "month", "quarter", "year":
			default:
				return ReportConfig{}, errInvalid("enum", []string{"query", "time_grain"},
					"time_grain must be day, week, month, quarter or year, got %q", config.TimeGrain)
			}
		}
	}
	switch config.Sign {
	case "both", "expenses", "income":
	default:
		return ReportConfig{}, errInvalid("enum", []string{"query", "sign"},
			"sign must be both, expenses or income, got %q", config.Sign)
	}
	return config, nil
}

// --- Result shapes -----------------------------------------------------------

// ReportTransactionRow is one transaction at the bottom of a hierarchy.
type ReportTransactionRow struct {
	TransactionID uuid.UUID  `json:"transaction_id"`
	SplitID       *uuid.UUID `json:"split_id"`
	On            Date       `json:"on"`
	// Payee is the display name; nothing in a report matches on wording.
	Payee      string       `json:"payee"`
	AccountID  uuid.UUID    `json:"account_id"`
	CategoryID *uuid.UUID   `json:"category_id"`
	Amount     domain.Money `json:"amount"`
	Notes      *string      `json:"notes"`
}

// ReportNode is one level of the drill-down, with its own subtotal.
type ReportNode struct {
	Key          string                 `json:"key"`
	Label        string                 `json:"label"`
	Depth        int                    `json:"depth"`
	Total        domain.Money           `json:"total"`
	Count        int                    `json:"count"`
	Children     []ReportNode           `json:"children"`
	Transactions []ReportTransactionRow `json:"transactions"`
}

type ReportTransactionResult struct {
	Groups []ReportNode `json:"groups"`
	Total  domain.Money `json:"total"`
	Count  int          `json:"count"`
}

type ReportColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type ReportPivotRow struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Section is the row's top-level family — Income, Expenses — when the row
	// dimension carries one (categories do; accounts, tags and payees do not).
	Section string `json:"section"`
	// Cells runs parallel to Columns.
	Cells []domain.Money `json:"cells"`
	Total domain.Money   `json:"total"`
}

// ReportSection is one family of pivot rows with its own subtotal, so income
// and expense do not interleave in one list.
type ReportSection struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Cells runs parallel to Columns; the section's own subtotal row.
	Cells []domain.Money `json:"cells"`
	Total domain.Money   `json:"total"`
}

type ReportSummaryResult struct {
	RowDimension    string           `json:"row_dimension"`
	ColumnDimension string           `json:"column_dimension"`
	Columns         []ReportColumn   `json:"columns"`
	Rows            []ReportPivotRow `json:"rows"`
	// Sections is empty when the rows all share one family; the client then
	// renders a flat list, which is what a Spending Summary is.
	Sections []ReportSection `json:"sections"`
	// ColumnTotals is the Total row, and for an Income & Expense report it is
	// the net line: income plus expenses per period, not a second query.
	ColumnTotals []domain.Money `json:"column_totals"`
	Total        domain.Money   `json:"total"`
}

// ReportTotals is the headline beneath any rendering.
type ReportTotals struct {
	Income   domain.Money `json:"income"`
	Expenses domain.Money `json:"expenses"`
	Net      domain.Money `json:"net"`
	// SavingsRate is net over income, null when nothing came in.
	SavingsRate *domain.Rate `json:"savings_rate"`
	Count       int          `json:"count"`
}

type ReportResult struct {
	Window WindowResponse `json:"window"`
	Config ReportConfig   `json:"config"`
	// FilterID is the saved Filter the report was narrowed by, if any.
	FilterID *uuid.UUID   `json:"filter_id"`
	Totals   ReportTotals `json:"totals"`
	// Exactly one of these is set, by Config.Mode.
	Transaction *ReportTransactionResult `json:"transaction"`
	Summary     *ReportSummaryResult     `json:"summary"`
}

// --- Running -----------------------------------------------------------------

func runReport(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	result, err := buildReport(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

// buildReport is the whole engine: select, then render one of two ways.
func buildReport(r *http.Request, env *Env, sp auth.SpaceContext) (ReportResult, error) {
	window, err := reportWindow(r)
	if err != nil {
		return ReportResult{}, err
	}
	config, err := reportConfigFromRequest(r)
	if err != nil {
		return ReportResult{}, err
	}
	filterID, hasFilter, err := queryUUID(r, "filter_id")
	if err != nil {
		return ReportResult{}, err
	}

	rows, err := selectAllocations(r.Context(), env, sp, window, config, filterID, hasFilter)
	if err != nil {
		return ReportResult{}, err
	}

	result := ReportResult{
		Window:   windowResponse(window),
		Config:   config,
		FilterID: dbconv.NullUUID(filterID),
		Totals:   reportTotals(rows),
	}
	if config.Mode == "transaction" {
		grouped := groupAllocations(rows, config)
		result.Transaction = &grouped
		return result, nil
	}
	pivoted := pivotAllocations(rows, config)
	result.Summary = &pivoted
	return result, nil
}

// reportWindow is the engine's window, defaulting to the effective date
// (calculations.md §2), where the register defaults to the posted date. The
// resolver is shared; only the default differs.
func reportWindow(r *http.Request) (Window, error) {
	window, err := WindowFromRequest(r)
	if err != nil {
		return Window{}, err
	}
	if r.URL.Query().Has("date_field") {
		return window, nil
	}
	return resolveWindow(window.From, window.HasFrom, window.To, window.HasTo, domain.DateEffective)
}

// allocation is one contribution to a report: a split, or a whole transaction
// when it has none.
type allocation struct {
	TransactionID uuid.UUID
	SplitID       uuid.UUID
	AccountID     uuid.UUID
	AccountName   string
	CategoryID    uuid.UUID
	Category      domain.Category
	HasCategory   bool
	// ParentName is the category's parent, for the hierarchy's middle level.
	ParentName string
	TagIDs     []uuid.UUID
	TagNames   []string

	Payee  string
	Notes  string
	On     domain.Date
	Amount domain.Money

	IsBillOrSubscription bool
}

// selectAllocations is the one selection path every rendering shares.
func selectAllocations(
	ctx context.Context, env *Env, sp auth.SpaceContext, window Window,
	config ReportConfig, filterID uuid.UUID, hasFilter bool,
) ([]allocation, error) {
	// Exactly one is populated: filtered keeps the matched allocations,
	// unfiltered keeps whole postings to expand.
	var selected []domain.Match
	from, to, mode := window.storeQuery()
	postings, rows, err := service.LoadPostings(ctx, env.DB, sp.ID(), store.TransactionQuery{
		From: from, To: to, DateMode: mode,
	})
	if err != nil {
		return nil, err
	}

	kept := make([]domain.Posting, 0, len(postings))
	for _, posting := range postings {
		// visibleAccountsOnly is false: a report over history still has to
		// account for what was spent in an account that has since been closed.
		if domain.CountsAsIncomeOrExpense(posting, false) {
			kept = append(kept, posting)
		}
	}

	if hasFilter {
		evaluated, err := loadFilter(ctx, env, sp, filterID)
		if err != nil {
			return nil, err
		}
		facets, err := service.FilterFacets(ctx, env.DB, sp.ID(), rows, evaluated)
		if err != nil {
			return nil, err
		}
		// The parts the filter matched, not every part of a row one part
		// matched, as the watchlist reads them.
		for _, posting := range kept {
			selected = append(selected,
				domain.MatchingParts(evaluated, posting, facets[posting.Txn.ID], window.Mode)...)
		}
	} else {
		for _, posting := range kept {
			selected = append(selected, domain.Allocations(posting)...)
		}
	}

	accounts, err := env.DB.ListAccounts(ctx, sp.ID(),
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	accountNames := make(map[uuid.UUID]string, len(accounts))
	for _, account := range accounts {
		accountNames[account.ID] = account.Name
	}
	categories, err := env.DB.ListCategories(ctx, sp.ID(), true)
	if err != nil {
		return nil, err
	}
	categoryByID := make(map[uuid.UUID]store.Category, len(categories))
	for _, category := range categories {
		categoryByID[category.ID] = category
	}
	tags, err := env.DB.ListTags(ctx, sp.ID(), true)
	if err != nil {
		return nil, err
	}
	tagNames := make(map[uuid.UUID]string, len(tags))
	for _, tag := range tags {
		tagNames[tag.ID] = tag.Name
	}

	out, err := expandAllocations(selected, rows, accountNames, categoryByID, tagNames, window.Mode)
	if err != nil {
		return nil, err
	}

	// The sign convention selects a side; it never rewrites one. The side is
	// the category's answer, not the sign's: a grocery refund belongs in an
	// expenses report, reducing groceries. A zero allocation is on neither.
	if config.Sign != "both" {
		side := make([]allocation, 0, len(out))
		for _, one := range out {
			if (config.Sign == "expenses") == one.isSpending() && !one.Amount.IsZero() {
				side = append(side, one)
			}
		}
		out = side
	}
	return out, nil
}

// expandAllocations turns selected parts into the per-category units every
// rendering shares. A transaction with no splits is one allocation of its
// whole amount; one with splits never contributes its parent. Reports and the
// assistant's spending-by-category both come through here.
//
// It takes parts, so which parts (filtered or all) is the caller's business,
// and reads every amount in primary currency.
func expandAllocations(
	parts []domain.Match,
	rows map[uuid.UUID]store.Transaction,
	accountNames map[uuid.UUID]string,
	categoryByID map[uuid.UUID]store.Category,
	tagNames map[uuid.UUID]string,
	mode domain.DateMode,
) ([]allocation, error) {
	out := make([]allocation, 0, len(parts))
	for _, part := range parts {
		posting := part.Posting
		key, err := store.ParseID(posting.Txn.ID)
		if err != nil {
			return nil, err
		}
		row := rows[key]
		one := allocation{
			TransactionID:        row.ID,
			AccountID:            row.AccountID,
			AccountName:          accountNames[row.AccountID],
			Payee:                posting.Txn.DisplayPayee(),
			Notes:                row.Notes,
			On:                   domain.ReportingDate(posting.Txn, mode),
			IsBillOrSubscription: row.IsBill || row.IsSubscription,
			Amount:               part.Amount,
		}
		categoryID := row.CategoryID
		one.TagIDs = row.TagIDs
		if part.Split != nil {
			if id, err := store.ParseID(part.Split.ID); err == nil {
				one.SplitID = id
			}
			categoryID = uuid.Nil
			if id, err := store.ParseID(part.Split.CategoryID); err == nil {
				categoryID = id
			}
			// A split's tags are its own, plus whatever the parent carries.
			one.TagIDs = append(append([]uuid.UUID{}, row.TagIDs...), splitTagIDs(part.Split)...)
		}
		fillCategory(&one, categoryID, categoryByID)
		// Asked again per allocation: the selection asked the parent, whose
		// category a split row does not have.
		if one.HasCategory && !one.Category.CountsAsIncomeOrExpense() {
			continue
		}
		fillTags(&one, tagNames)
		out = append(out, one)
	}
	return out, nil
}

// splitTagIDs is the split's own tags, as the store's ids.
func splitTagIDs(split *domain.Split) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(split.TagIDs))
	for _, id := range split.TagIDs {
		parsed, err := store.ParseID(id)
		if err != nil {
			continue
		}
		out = append(out, parsed)
	}
	return out
}

func fillCategory(one *allocation, id uuid.UUID, byID map[uuid.UUID]store.Category) {
	if id == uuid.Nil {
		return
	}
	category, known := byID[id]
	if !known {
		return
	}
	one.CategoryID = id
	one.Category = store.DomainCategory(category)
	one.HasCategory = true
	if parent, hasParent := byID[category.ParentID]; hasParent {
		one.ParentName = parent.Name
	}
}

func fillTags(one *allocation, names map[uuid.UUID]string) {
	seen := map[uuid.UUID]bool{}
	for _, id := range one.TagIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		one.TagNames = append(one.TagNames, names[id])
	}
}

func summarize(rows []allocation) domain.AllocationSummary {
	amounts := make([]domain.ReportedAmount, len(rows))
	for index, one := range rows {
		amounts[index] = domain.ReportedAmount{
			Amount:               one.Amount,
			Kind:                 one.ledgerKind(),
			IsBillOrSubscription: one.IsBillOrSubscription,
		}
	}
	return domain.SummarizeAllocations(amounts)
}

func reportTotals(rows []allocation) ReportTotals {
	summary := summarize(rows)
	return ReportTotals{
		Income:      summary.Income,
		Expenses:    summary.Expenses,
		Net:         summary.Net,
		SavingsRate: store.PtrIf(summary.SavingsRate, summary.HasSavingsRate),
		Count:       summary.Count,
	}
}

// --- Grouping ----------------------------------------------------------------

// reportKey is one level of a grouping path.
type reportKey struct{ Key, Label string }

// reportPaths is where an allocation files under a dimension. More than one
// path only for tags; the grand total comes from the allocations, so a two-tag
// row does not inflate it.
func reportPaths(dimension string, one allocation, grain string) [][]reportKey {
	switch dimension {
	case "account":
		return [][]reportKey{{{Key: one.AccountID.String(), Label: one.AccountName}}}
	case "payee":
		return [][]reportKey{{{Key: strings.ToLower(one.Payee), Label: textutil.FirstNonBlank(one.Payee, "No payee")}}}
	case "tag":
		if len(one.TagNames) == 0 {
			return [][]reportKey{{{Key: "", Label: "Untagged"}}}
		}
		paths := make([][]reportKey, 0, len(one.TagNames))
		for index, name := range one.TagNames {
			paths = append(paths, []reportKey{{Key: one.TagIDs[index].String(), Label: name}})
		}
		return paths
	case "txf":
		// Kind, form, line item, payee: the reference product's tax walk. The
		// category's TXF code becomes the form and line through txfLines.
		line := txfLine{Form: "Unmapped", Item: "No tax line assigned"}
		if one.HasCategory && one.Category.TxfID != "" {
			line = txfLineFor(one.Category.TxfID)
		}
		return [][]reportKey{{
			{Key: string(one.ledgerKind()), Label: kindLabel(one.ledgerKind())},
			{Key: line.Form, Label: line.Form},
			{Key: line.Form + "/" + line.Item, Label: line.Item},
			{Key: strings.ToLower(one.Payee), Label: textutil.FirstNonBlank(one.Payee, "No payee")},
		}}
	case "time":
		key, label := periodKey(one.On, grain)
		return [][]reportKey{{{Key: key, Label: label}}}
	default:
		kind := one.ledgerKind()
		path := []reportKey{{Key: string(kind), Label: kindLabel(kind)}}
		if one.ParentName != "" {
			path = append(path, reportKey{Key: string(one.Category.ParentID), Label: one.ParentName})
		}
		if one.HasCategory {
			path = append(path, reportKey{Key: one.CategoryID.String(), Label: one.Category.Name})
		} else {
			path = append(path, reportKey{Key: "", Label: "Uncategorized"})
		}
		return [][]reportKey{path}
	}
}

// ledgerKind is which side of the ledger this allocation counts on:
// domain.LedgerKind, the category's answer rather than the sign's. An
// uncategorized allocation is spending, like Simplifi's Uncategorized.
func (one allocation) ledgerKind() domain.CategoryKind {
	return domain.LedgerKind(one.Category, one.HasCategory)
}

// isSpending is the expense side named, for the readers that only want to know
// which side.
func (one allocation) isSpending() bool { return one.ledgerKind() == domain.CategoryExpense }

func kindLabel(kind domain.CategoryKind) string {
	switch kind {
	case domain.CategoryIncome:
		return "Income"
	case domain.CategoryTransfer:
		return "Transfers"
	default:
		return "Expenses"
	}
}

// periodKey is a date's column in a time pivot. The keys sort lexicographically
// into chronological order.
func periodKey(on domain.Date, grain string) (key, label string) {
	switch grain {
	case "day":
		return on.String(), on.String()
	case "week":
		monday := on.AddDays(-((int(on.Time().Weekday()) + 6) % 7))
		return monday.String(), monday.String()
	case "quarter":
		quarter := (int(on.Month)-1)/3 + 1
		key = fmt.Sprintf("%04d-Q%d", on.Year, quarter)
		return key, key
	case "year":
		key = strconv.Itoa(on.Year)
		return key, key
	default:
		key = domain.MonthOf(on).String()
		return key, key
	}
}

// groupAllocations builds the drill-down, with a subtotal at every level.
func groupAllocations(rows []allocation, config ReportConfig) ReportTransactionResult {
	type bucket struct {
		key      reportKey
		order    int
		rows     []allocation
		children map[string]*bucket
		order2   []string
	}
	root := &bucket{children: map[string]*bucket{}}

	for _, one := range rows {
		for _, path := range reportPaths(config.Rows, one, config.TimeGrain) {
			node := root
			for _, step := range path {
				child, seen := node.children[step.Key]
				if !seen {
					child = &bucket{key: step, order: len(node.order2), children: map[string]*bucket{}}
					node.children[step.Key] = child
					node.order2 = append(node.order2, step.Key)
				}
				node = child
			}
			node.rows = append(node.rows, one)
		}
	}

	var render func(node *bucket, depth int) []ReportNode
	render = func(node *bucket, depth int) []ReportNode {
		keys := append([]string{}, node.order2...)
		sort.Slice(keys, func(i, j int) bool {
			return node.children[keys[i]].key.Label < node.children[keys[j]].key.Label
		})
		out := make([]ReportNode, 0, len(keys))
		for _, key := range keys {
			child := node.children[key]
			children := render(child, depth+1)
			total := domain.Sum(child.rows, func(a allocation) domain.Money { return a.Amount })
			count := len(child.rows)
			for _, grandchild := range children {
				total = domain.Total(total, grandchild.Total)
				count += grandchild.Count
			}
			out = append(out, ReportNode{
				Key:          child.key.Key,
				Label:        child.key.Label,
				Depth:        depth,
				Total:        total,
				Count:        count,
				Children:     children,
				Transactions: transactionRows(child.rows),
			})
		}
		return out
	}

	groups := render(root, 0)
	return ReportTransactionResult{
		Groups: groups,
		Total:  domain.Sum(rows, func(a allocation) domain.Money { return a.Amount }),
		Count:  len(rows),
	}
}

func transactionRows(rows []allocation) []ReportTransactionRow {
	out := make([]ReportTransactionRow, 0, len(rows))
	for _, one := range rows {
		out = append(out, ReportTransactionRow{
			TransactionID: one.TransactionID,
			SplitID:       dbconv.NullUUID(one.SplitID),
			On:            Date(one.On),
			Payee:         one.Payee,
			AccountID:     one.AccountID,
			CategoryID:    dbconv.NullUUID(one.CategoryID),
			Amount:        one.Amount,
			Notes:         dbconv.NullText(one.Notes),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].On != out[j].On {
			return domain.Date(out[i].On).Before(domain.Date(out[j].On))
		}
		return out[i].TransactionID.String() < out[j].TransactionID.String()
	})
	return out
}

// pivotAllocations builds the row-by-column grid, with a Total row and a Total
// column. A row's identity is its section and its leaf, or income and expense
// Uncategorized would merge.
func pivotAllocations(rows []allocation, config ReportConfig) ReportSummaryResult {
	type rowID struct{ section, key string }
	cells := map[rowID]map[string]domain.Money{}
	rowLabels, columnLabels := map[rowID]string{}, map[string]string{}
	sectionLabels := map[string]string{}

	for _, one := range rows {
		for _, rowPath := range reportPaths(config.Rows, one, config.TimeGrain) {
			rowKey := rowPath[len(rowPath)-1]
			id := rowID{key: rowKey.Key}
			// Only a dimension whose paths are deeper than one level carries a
			// family; accounts, tags, payees and time are flat.
			if len(rowPath) > 1 {
				id.section = rowPath[0].Key
				sectionLabels[rowPath[0].Key] = rowPath[0].Label
			}
			rowLabels[id] = rowKey.Label
			for _, columnPath := range reportPaths(config.Columns, one, config.TimeGrain) {
				columnKey := columnPath[len(columnPath)-1]
				columnLabels[columnKey.Key] = columnKey.Label
				if cells[id] == nil {
					cells[id] = map[string]domain.Money{}
				}
				cells[id][columnKey.Key] = cells[id][columnKey.Key].Add(one.Amount)
			}
		}
	}

	columns := make([]ReportColumn, 0, len(columnLabels))
	for key, label := range columnLabels {
		columns = append(columns, ReportColumn{Key: key, Label: label})
	}
	sort.Slice(columns, func(i, j int) bool {
		if config.Columns == "time" {
			return columns[i].Key < columns[j].Key
		}
		return columns[i].Label < columns[j].Label
	})

	ids := make([]rowID, 0, len(rowLabels))
	for id := range rowLabels {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].section != ids[j].section {
			return sectionRank(ids[i].section) < sectionRank(ids[j].section)
		}
		return rowLabels[ids[i]] < rowLabels[ids[j]]
	})

	columnTotals := make([]domain.Money, len(columns))
	sectionCells := map[string][]domain.Money{}
	out := make([]ReportPivotRow, 0, len(ids))
	for _, id := range ids {
		row := ReportPivotRow{
			Key: id.key, Label: rowLabels[id], Section: id.section,
			Cells: make([]domain.Money, len(columns)),
		}
		if sectionCells[id.section] == nil {
			sectionCells[id.section] = make([]domain.Money, len(columns))
		}
		for index, column := range columns {
			amount := cells[id][column.Key].Round()
			row.Cells[index] = amount
			row.Total = row.Total.Add(amount)
			columnTotals[index] = columnTotals[index].Add(amount)
			sectionCells[id.section][index] = sectionCells[id.section][index].Add(amount)
		}
		row.Total = row.Total.Round()
		out = append(out, row)
	}
	for index := range columnTotals {
		columnTotals[index] = columnTotals[index].Round()
	}

	// One family is no split at all: a Spending Summary stays a flat list.
	sections := []ReportSection{}
	if len(sectionLabels) > 1 {
		for key, label := range sectionLabels {
			section := ReportSection{Key: key, Label: label, Cells: sectionCells[key]}
			for _, amount := range section.Cells {
				section.Total = section.Total.Add(amount)
			}
			section.Total = section.Total.Round()
			sections = append(sections, section)
		}
		sort.Slice(sections, func(i, j int) bool {
			return sectionRank(sections[i].Key) < sectionRank(sections[j].Key)
		})
	}

	return ReportSummaryResult{
		RowDimension:    config.Rows,
		ColumnDimension: config.Columns,
		Columns:         columns,
		Rows:            out,
		Sections:        sections,
		ColumnTotals:    columnTotals,
		Total:           domain.Sum(rows, func(a allocation) domain.Money { return a.Amount }),
	}
}

// sectionRank fixes the family order: income above expenses, the way the
// transaction rendering and the reference product both read.
func sectionRank(section string) int {
	switch domain.CategoryKind(section) {
	case domain.CategoryIncome:
		return 0
	case domain.CategoryExpense:
		return 1
	case domain.CategoryTransfer:
		return 2
	}
	return 3
}

// --- Monthly Summary ---------------------------------------------------------

// MonthlySummaryEntry is one row of Top Categories or Top Payees.
type MonthlySummaryEntry struct {
	Key   string       `json:"key"`
	Label string       `json:"label"`
	Total domain.Money `json:"total"`
	// Count is how many transactions made it up — the occurrence count beside
	// each row.
	Count int `json:"count"`
	// ChangePct is against the same row last month, null when it had nothing.
	ChangePct *domain.Rate `json:"change_pct"`
}

// MonthlySummaryResponse is the narrative snapshot.
type MonthlySummaryResponse struct {
	Month      string `json:"month"`
	PriorMonth string `json:"prior_month"`

	Income   domain.Money `json:"income"`
	Expenses domain.Money `json:"expenses"`
	Net      domain.Money `json:"net"`

	IncomeChangePct   *domain.Rate `json:"income_change_pct"`
	ExpensesChangePct *domain.Rate `json:"expenses_change_pct"`
	NetChangePct      *domain.Rate `json:"net_change_pct"`

	// Bills is what the bills-versus-everything-else bar annotates, and
	// Discretionary is its complement. The two sum to Expenses.
	Bills         domain.Money `json:"bills"`
	Discretionary domain.Money `json:"discretionary"`

	// TopCategories and TopPayees exclude bills and subscriptions.
	TopCategories []MonthlySummaryEntry `json:"top_categories"`
	TopPayees     []MonthlySummaryEntry `json:"top_payees"`
}

func readMonthlySummary(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	month, err := monthFromRequest(r, env)
	if err != nil {
		return err
	}
	prior := month.Prev()

	// A calendar month, not a window, so a "month" cannot be six weeks.
	current, err := monthAllocations(r.Context(), env, sp, month)
	if err != nil {
		return err
	}
	previous, err := monthAllocations(r.Context(), env, sp, prior)
	if err != nil {
		return err
	}

	now := summarize(current)
	nowTotals, wasTotals := reportTotals(current), reportTotals(previous)
	incomePct, hasIncomePct := domain.Percent(
		nowTotals.Income.Sub(wasTotals.Income), wasTotals.Income.Abs())
	expensesPct, hasExpensesPct := domain.Percent(
		nowTotals.Expenses.Sub(wasTotals.Expenses), wasTotals.Expenses.Abs())
	netPct, hasNetPct := domain.Percent(nowTotals.Net.Sub(wasTotals.Net), wasTotals.Net.Abs())

	return writeJSON(w, http.StatusOK, MonthlySummaryResponse{
		Month:             month.String(),
		PriorMonth:        prior.String(),
		Income:            nowTotals.Income,
		Expenses:          nowTotals.Expenses,
		Net:               nowTotals.Net,
		IncomeChangePct:   store.PtrIf(incomePct, hasIncomePct),
		ExpensesChangePct: store.PtrIf(expensesPct, hasExpensesPct),
		NetChangePct:      store.PtrIf(netPct, hasNetPct),
		Bills:             now.Bills,
		Discretionary:     now.Discretionary,
		TopCategories:     topEntriesFor("category", current, previous),
		TopPayees:         topEntriesFor("payee", current, previous),
	})
}

func monthFromRequest(r *http.Request, env *Env) (domain.Month, error) {
	month, given, err := queryMonth(r, "month")
	if err != nil || given {
		return month, err
	}
	return domain.MonthOf(domain.DateOf(env.now())), nil
}

func monthAllocations(
	ctx context.Context, env *Env, sp auth.SpaceContext, month domain.Month,
) ([]allocation, error) {
	window, err := ResolveWindow(month.FirstDay(), true, month.LastDay(), true, domain.DateEffective)
	if err != nil {
		return nil, err
	}
	return selectAllocations(ctx, env, sp, window, ReportConfig{Sign: "both"}, uuid.Nil, false)
}

// topEntriesFor ranks a dimension by spend, bills and subscriptions left out.
func topEntriesFor(dimension string, current, previous []allocation) []MonthlySummaryEntry {
	now := discretionaryTotals(dimension, current)
	was := discretionaryTotals(dimension, previous)

	entries := make([]MonthlySummaryEntry, 0, len(now))
	for key, row := range now {
		pct, hasPct := domain.Percent(row.total.Sub(was[key].total), was[key].total.Abs())
		entries = append(entries, MonthlySummaryEntry{
			Key:       key,
			Label:     row.label,
			Total:     row.total.Round(),
			Count:     row.count,
			ChangePct: store.PtrIf(pct, hasPct),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].Total.Equal(entries[j].Total) {
			return entries[i].Total.LessThan(entries[j].Total)
		}
		return entries[i].Label < entries[j].Label
	})
	if len(entries) > topEntries {
		entries = entries[:topEntries]
	}
	return entries
}

type summaryBucket struct {
	label string
	total domain.Money
	count int
}

func discretionaryTotals(dimension string, rows []allocation) map[string]summaryBucket {
	out := map[string]summaryBucket{}
	seen := map[string]map[uuid.UUID]bool{}
	for _, one := range rows {
		if one.IsBillOrSubscription || !one.isSpending() {
			continue
		}
		for _, path := range reportPaths(dimension, one, "") {
			key := path[len(path)-1]
			bucket := out[key.Key]
			bucket.label = key.Label
			bucket.total = bucket.total.Add(one.Amount)
			// Counted per transaction, not per split: a split receipt is one
			// visit to the shop, and the panel reports visits.
			if seen[key.Key] == nil {
				seen[key.Key] = map[uuid.UUID]bool{}
			}
			if !seen[key.Key][one.TransactionID] {
				seen[key.Key][one.TransactionID] = true
				bucket.count++
			}
			out[key.Key] = bucket
		}
	}
	return out
}

// --- Saved reports -----------------------------------------------------------

// SavedReportResponse is a stored report: the shell, and the one Filter it is
// narrowed by.
type SavedReportResponse struct {
	ID     uuid.UUID      `json:"id"`
	Name   string         `json:"name"`
	Config ReportConfig   `json:"config"`
	Filter FilterResponse `json:"filter"`
}

type SavedReportCreate struct {
	Name   string            `json:"name"`
	Config ReportConfig      `json:"config"`
	Items  []FilterItemWrite `json:"items"`
	// QueryText is the report's free-text search box, kept beside the
	// structured items because the user typed it.
	QueryText *string `json:"query_text"`
}

type SavedReportUpdate struct {
	Name   Opt[string]        `json:"name"`
	Config Opt[ReportConfig]  `json:"config"`
	Items  *[]FilterItemWrite `json:"items"`
}

// savedReportConfig is what rides in the filter row's query_text: the free text
// and the report shell. There is no reports table (ground rule 3).
type savedReportConfig struct {
	Config    ReportConfig `json:"config"`
	QueryText string       `json:"query_text"`
}

func listSavedReports(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	stored, err := env.DB.ListFilters(r.Context(), sp.ID(), false)
	if err != nil {
		return err
	}
	out := make([]SavedReportResponse, 0, len(stored))
	for _, filter := range stored {
		if filter.Scope != reportScope {
			continue
		}
		config, _ := decodeSavedReport(filter)
		out = append(out, savedReportResponse(filter, config))
	}
	return writeJSON(w, http.StatusOK, out)
}

func readSavedReport(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	filter, config, err := liveSavedReport(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, savedReportResponse(filter, config))
}

func createSavedReport(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body SavedReportCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Name) == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	config, err := checkReportConfig(body.Config)
	if err != nil {
		return err
	}
	items, err := buildFilterItems(body.Items)
	if err != nil {
		return err
	}

	stored := &store.Filter{
		Name:      body.Name,
		Scope:     reportScope,
		QueryText: encodeSavedReport(config, store.Deref(body.QueryText, "")),
		Items:     items,
	}
	if err := env.DB.CreateFilter(r.Context(), sp.ID(), stored); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, savedReportResponse(*stored, config))
}

func updateSavedReport(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	filter, config, err := liveSavedReport(r, env, sp)
	if err != nil {
		return err
	}
	var body SavedReportUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if err := applyRequired("name", body.Name, &filter.Name); err != nil {
		return err
	}
	if body.Config.Present() {
		if config, err = checkReportConfig(body.Config.Value); err != nil {
			return err
		}
	}
	_, text := decodeSavedReport(filter)
	filter.QueryText = encodeSavedReport(config, text)

	if err := env.DB.UpdateFilter(r.Context(), sp.ID(), &filter); err != nil {
		return err
	}
	if body.Items != nil {
		items, err := buildFilterItems(*body.Items)
		if err != nil {
			return err
		}
		filter.Items = items
		if err := env.DB.ReplaceFilterItems(r.Context(), sp.ID(), &filter); err != nil {
			return err
		}
	}
	return writeJSON(w, http.StatusOK, savedReportResponse(filter, config))
}

func deleteSavedReport(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	filter, _, err := liveSavedReport(r, env, sp)
	if err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteFilter(r.Context(), sp.ID(), filter.ID), "Report")
}

func liveSavedReport(r *http.Request, env *Env, sp auth.SpaceContext) (store.Filter, ReportConfig, error) {
	filter, err := fromPath(r, sp, "report_id", "Report", env.DB.GetFilter)
	if err != nil {
		return store.Filter{}, ReportConfig{}, err
	}
	if filter.IsDeleted || filter.Scope != reportScope {
		return store.Filter{}, ReportConfig{}, errNotFound("Report")
	}
	config, _ := decodeSavedReport(filter)
	return filter, config, nil
}

func savedReportResponse(filter store.Filter, config ReportConfig) SavedReportResponse {
	response := filterResponse(filter)
	_, text := decodeSavedReport(filter)
	response.QueryText = dbconv.NullText(text)
	return SavedReportResponse{
		ID:     filter.ID,
		Name:   filter.Name,
		Config: config,
		Filter: response,
	}
}

func encodeSavedReport(config ReportConfig, queryText string) string {
	encoded, err := json.Marshal(savedReportConfig{Config: config, QueryText: queryText})
	if err != nil {
		return ""
	}
	return string(encoded)
}

// decodeSavedReport reads the shell back, defaulting a row written before this
// encoding existed rather than failing the whole listing.
func decodeSavedReport(filter store.Filter) (ReportConfig, string) {
	var decoded savedReportConfig
	if err := json.Unmarshal([]byte(filter.QueryText), &decoded); err != nil {
		config, _ := checkReportConfig(ReportConfig{})
		return config, filter.QueryText
	}
	config, err := checkReportConfig(decoded.Config)
	if err != nil {
		config, _ = checkReportConfig(ReportConfig{})
	}
	return config, decoded.QueryText
}
