package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewReportServiceHandler(reportService{env}, opts...)
	})
}

type reportService struct{ env *Env }

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

func (s reportService) ListReportPresets(
	context.Context, *agentifiv1.ListReportPresetsRequest,
) (*agentifiv1.ListReportPresetsResponse, error) {
	names := make([]string, 0, len(reportPresets))
	for name := range reportPresets {
		names = append(names, name)
	}
	sort.Strings(names)

	out := &agentifiv1.ListReportPresetsResponse{Presets: make([]*agentifiv1.ReportPreset, 0, len(names))}
	for _, name := range names {
		preset := reportPresets[name]
		config := preset.Config
		config.Preset = name
		out.Presets = append(out.Presets, &agentifiv1.ReportPreset{
			Preset:   name,
			Label:    preset.Label,
			Config:   reportConfigProto(config),
			ServedBy: dbconv.NullText(preset.ServedBy),
		})
	}
	return out, nil
}

// reportConfigOf reads the shell off the request, a preset supplying whatever
// was left out.
func reportConfigOf(req *agentifiv1.RunReportRequest) (ReportConfig, error) {
	var config ReportConfig
	if name := strings.TrimSpace(req.GetPreset()); name != "" {
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

	for _, knob := range []struct {
		value string
		into  *string
	}{
		{req.GetMode(), &config.Mode},
		{req.GetRows(), &config.Rows},
		{req.GetColumns(), &config.Columns},
		{req.GetTimeGrain(), &config.TimeGrain},
		{req.GetSign(), &config.Sign},
	} {
		if value := strings.TrimSpace(knob.value); value != "" {
			*knob.into = value
		}
	}
	return checkReportConfig(config)
}

func reportConfigProto(c ReportConfig) *agentifiv1.ReportConfig {
	return &agentifiv1.ReportConfig{
		Preset: c.Preset, Mode: c.Mode, Rows: c.Rows, Columns: c.Columns, TimeGrain: c.TimeGrain, Sign: c.Sign,
	}
}

func reportConfigFrom(c *agentifiv1.ReportConfig) ReportConfig {
	return ReportConfig{
		Preset: c.GetPreset(), Mode: c.GetMode(), Rows: c.GetRows(), Columns: c.GetColumns(),
		TimeGrain: c.GetTimeGrain(), Sign: c.GetSign(),
	}
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

// ReportTotals is the headline beneath any rendering.
type ReportTotals struct {
	Income   domain.Money
	Expenses domain.Money
	Net      domain.Money
	// SavingsRate is net over income, nil when nothing came in.
	SavingsRate *domain.Rate
	Count       int
}

func reportTotalsProto(t ReportTotals) *agentifiv1.ReportTotals {
	return &agentifiv1.ReportTotals{
		Income:      moneyProto(t.Income),
		Expenses:    moneyProto(t.Expenses),
		Net:         moneyProto(t.Net),
		SavingsRate: ratePtrProto(t.SavingsRate),
		Count:       int32(t.Count),
	}
}

// --- Running -----------------------------------------------------------------

// RunReport is the whole engine: select, then render one of two ways.
func (s reportService) RunReport(
	ctx context.Context, req *agentifiv1.RunReportRequest,
) (*agentifiv1.RunReportResponse, error) {
	window, err := reportWindowOf(req)
	if err != nil {
		return nil, err
	}
	config, err := reportConfigOf(req)
	if err != nil {
		return nil, err
	}
	filterID, hasFilter, err := queryUUIDOf("filter_id", req.GetFilterId())
	if err != nil {
		return nil, err
	}

	rows, err := selectAllocations(ctx, s.env, spaceFrom(ctx), window, config, filterID, hasFilter)
	if err != nil {
		return nil, err
	}

	out := &agentifiv1.RunReportResponse{
		Window: windowProto(window),
		Config: reportConfigProto(config),
		Totals: reportTotalsProto(reportTotals(rows)),
	}
	if hasFilter {
		out.FilterId = proto.String(filterID.String())
	}
	if config.Mode == "transaction" {
		out.Transaction = groupAllocations(rows, config)
		return out, nil
	}
	out.Summary = pivotAllocations(rows, config)
	return out, nil
}

// reportWindowOf is the engine's window, defaulting to the effective date
// (calculations.md §2), where the register defaults to the posted date. The
// resolver is shared; only the default differs.
func reportWindowOf(req *agentifiv1.RunReportRequest) (Window, error) {
	if req.GetDateField() == "" {
		return windowBetween(req.GetFrom(), req.GetTo(), domain.DateEffective)
	}
	return windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
}

// queryUUIDOf reads an optional id parameter. Unlike a path id, a malformed
// one is the caller's mistake to be told about, not a row to hide.
func queryUUIDOf(key, raw string) (uuid.UUID, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, false, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false, errInvalid("uuid_parsing", []string{"query", key}, "%s must be a uuid", key)
	}
	return id, true, nil
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
func groupAllocations(rows []allocation, config ReportConfig) *agentifiv1.ReportTransactionResult {
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

	type rendered struct {
		node  *agentifiv1.ReportNode
		total domain.Money
		count int
	}
	var render func(node *bucket, depth int) []rendered
	render = func(node *bucket, depth int) []rendered {
		keys := append([]string{}, node.order2...)
		sort.Slice(keys, func(i, j int) bool {
			return node.children[keys[i]].key.Label < node.children[keys[j]].key.Label
		})
		out := make([]rendered, 0, len(keys))
		for _, key := range keys {
			child := node.children[key]
			children := render(child, depth+1)
			total := domain.Sum(child.rows, func(a allocation) domain.Money { return a.Amount })
			count := len(child.rows)
			nodes := make([]*agentifiv1.ReportNode, 0, len(children))
			for _, grandchild := range children {
				total = domain.Total(total, grandchild.total)
				count += grandchild.count
				nodes = append(nodes, grandchild.node)
			}
			out = append(out, rendered{total: total, count: count, node: &agentifiv1.ReportNode{
				Key:          child.key.Key,
				Label:        child.key.Label,
				Depth:        int32(depth),
				Total:        moneyProto(total),
				Count:        int32(count),
				Children:     nodes,
				Transactions: transactionRows(child.rows),
			}})
		}
		return out
	}

	groups := render(root, 0)
	out := &agentifiv1.ReportTransactionResult{
		Groups: make([]*agentifiv1.ReportNode, 0, len(groups)),
		Total:  moneyProto(domain.Sum(rows, func(a allocation) domain.Money { return a.Amount })),
		Count:  int32(len(rows)),
	}
	for _, group := range groups {
		out.Groups = append(out.Groups, group.node)
	}
	return out
}

func transactionRows(rows []allocation) []*agentifiv1.ReportTransactionRow {
	sorted := append([]allocation{}, rows...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].On != sorted[j].On {
			return sorted[i].On.Before(sorted[j].On)
		}
		return sorted[i].TransactionID.String() < sorted[j].TransactionID.String()
	})
	out := make([]*agentifiv1.ReportTransactionRow, 0, len(sorted))
	for _, one := range sorted {
		row := &agentifiv1.ReportTransactionRow{
			TransactionId: one.TransactionID.String(),
			On:            one.On.String(),
			Payee:         one.Payee,
			AccountId:     one.AccountID.String(),
			Amount:        moneyProto(one.Amount),
			Notes:         dbconv.NullText(one.Notes),
		}
		if one.SplitID != uuid.Nil {
			row.SplitId = proto.String(one.SplitID.String())
		}
		if one.CategoryID != uuid.Nil {
			row.CategoryId = proto.String(one.CategoryID.String())
		}
		out = append(out, row)
	}
	return out
}

// pivotAllocations builds the row-by-column grid, with a Total row and a Total
// column. A row's identity is its section and its leaf, or income and expense
// Uncategorized would merge.
func pivotAllocations(rows []allocation, config ReportConfig) *agentifiv1.ReportSummaryResult {
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

	columns := make([]*agentifiv1.ReportColumn, 0, len(columnLabels))
	for key, label := range columnLabels {
		columns = append(columns, &agentifiv1.ReportColumn{Key: key, Label: label})
	}
	sort.Slice(columns, func(i, j int) bool {
		if config.Columns == "time" {
			return columns[i].GetKey() < columns[j].GetKey()
		}
		return columns[i].GetLabel() < columns[j].GetLabel()
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
	out := make([]*agentifiv1.ReportPivotRow, 0, len(ids))
	for _, id := range ids {
		row := &agentifiv1.ReportPivotRow{Key: id.key, Label: rowLabels[id], Section: id.section}
		if sectionCells[id.section] == nil {
			sectionCells[id.section] = make([]domain.Money, len(columns))
		}
		var total domain.Money
		for index, column := range columns {
			amount := cells[id][column.GetKey()].Round()
			row.Cells = append(row.Cells, moneyProto(amount))
			total = total.Add(amount)
			columnTotals[index] = columnTotals[index].Add(amount)
			sectionCells[id.section][index] = sectionCells[id.section][index].Add(amount)
		}
		row.Total = moneyProto(total.Round())
		out = append(out, row)
	}
	for index := range columnTotals {
		columnTotals[index] = columnTotals[index].Round()
	}

	// One family is no split at all: a Spending Summary stays a flat list.
	sections := []*agentifiv1.ReportSection{}
	if len(sectionLabels) > 1 {
		for key, label := range sectionLabels {
			section := &agentifiv1.ReportSection{Key: key, Label: label, Cells: moneyProtos(sectionCells[key])}
			var total domain.Money
			for _, amount := range sectionCells[key] {
				total = total.Add(amount)
			}
			section.Total = moneyProto(total.Round())
			sections = append(sections, section)
		}
		sort.Slice(sections, func(i, j int) bool {
			return sectionRank(sections[i].GetKey()) < sectionRank(sections[j].GetKey())
		})
	}

	return &agentifiv1.ReportSummaryResult{
		RowDimension:    config.Rows,
		ColumnDimension: config.Columns,
		Columns:         columns,
		Rows:            out,
		Sections:        sections,
		ColumnTotals:    moneyProtos(columnTotals),
		Total:           moneyProto(domain.Sum(rows, func(a allocation) domain.Money { return a.Amount })),
	}
}

func moneyProtos(amounts []domain.Money) []*agentifiv1.Money {
	out := make([]*agentifiv1.Money, 0, len(amounts))
	for _, amount := range amounts {
		out = append(out, moneyProto(amount))
	}
	return out
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

// GetMonthlySummary is the narrative snapshot. Bills is what the
// bills-versus-everything-else bar annotates, and Discretionary is its
// complement; the two sum to Expenses. Top Categories and Top Payees exclude
// bills and subscriptions.
func (s reportService) GetMonthlySummary(
	ctx context.Context, req *agentifiv1.GetMonthlySummaryRequest,
) (*agentifiv1.GetMonthlySummaryResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	month, err := summaryMonthOf(req.GetMonth(), env)
	if err != nil {
		return nil, err
	}
	prior := month.Prev()

	// A calendar month, not a window, so a "month" cannot be six weeks.
	current, err := monthAllocations(ctx, env, sp, month)
	if err != nil {
		return nil, err
	}
	previous, err := monthAllocations(ctx, env, sp, prior)
	if err != nil {
		return nil, err
	}

	now := summarize(current)
	nowTotals, wasTotals := reportTotals(current), reportTotals(previous)
	incomePct, hasIncomePct := domain.Percent(
		nowTotals.Income.Sub(wasTotals.Income), wasTotals.Income.Abs())
	expensesPct, hasExpensesPct := domain.Percent(
		nowTotals.Expenses.Sub(wasTotals.Expenses), wasTotals.Expenses.Abs())
	netPct, hasNetPct := domain.Percent(nowTotals.Net.Sub(wasTotals.Net), wasTotals.Net.Abs())

	return &agentifiv1.GetMonthlySummaryResponse{
		Month:             month.String(),
		PriorMonth:        prior.String(),
		Income:            moneyProto(nowTotals.Income),
		Expenses:          moneyProto(nowTotals.Expenses),
		Net:               moneyProto(nowTotals.Net),
		IncomeChangePct:   rateProto(incomePct, hasIncomePct),
		ExpensesChangePct: rateProto(expensesPct, hasExpensesPct),
		NetChangePct:      rateProto(netPct, hasNetPct),
		Bills:             moneyProto(now.Bills),
		Discretionary:     moneyProto(now.Discretionary),
		TopCategories:     topEntriesFor("category", current, previous),
		TopPayees:         topEntriesFor("payee", current, previous),
	}, nil
}

// summaryMonthOf is the month asked for, or this one.
func summaryMonthOf(raw string, env *Env) (domain.Month, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.MonthOf(domain.DateOf(env.now())), nil
	}
	month, err := parseMonth(raw)
	if err != nil {
		return domain.Month{}, errInvalid("date_parsing", []string{"query", "month"}, "%s", err)
	}
	return month, nil
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
func topEntriesFor(dimension string, current, previous []allocation) []*agentifiv1.MonthlySummaryEntry {
	now := discretionaryTotals(dimension, current)
	was := discretionaryTotals(dimension, previous)

	type entry struct {
		key   string
		row   summaryBucket
		total domain.Money
	}
	entries := make([]entry, 0, len(now))
	for key, row := range now {
		entries = append(entries, entry{key: key, row: row, total: row.total.Round()})
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].total.Equal(entries[j].total) {
			return entries[i].total.LessThan(entries[j].total)
		}
		return entries[i].row.label < entries[j].row.label
	})
	if len(entries) > topEntries {
		entries = entries[:topEntries]
	}

	out := make([]*agentifiv1.MonthlySummaryEntry, 0, len(entries))
	for _, one := range entries {
		pct, hasPct := domain.Percent(one.row.total.Sub(was[one.key].total), was[one.key].total.Abs())
		out = append(out, &agentifiv1.MonthlySummaryEntry{
			Key:       one.key,
			Label:     one.row.label,
			Total:     moneyProto(one.total),
			Count:     int32(one.row.count),
			ChangePct: rateProto(pct, hasPct),
		})
	}
	return out
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

// savedReportConfig is what rides in the filter row's query_text: the free text
// and the report shell. There is no reports table (ground rule 3).
type savedReportConfig struct {
	Config    ReportConfig `json:"config"`
	QueryText string       `json:"query_text"`
}

func (s reportService) ListSavedReports(
	ctx context.Context, _ *agentifiv1.ListSavedReportsRequest,
) (*agentifiv1.ListSavedReportsResponse, error) {
	stored, err := s.env.DB.ListFilters(ctx, spaceFrom(ctx).ID(), false)
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListSavedReportsResponse{Reports: make([]*agentifiv1.SavedReport, 0, len(stored))}
	for _, filter := range stored {
		if filter.Scope != reportScope {
			continue
		}
		config, _ := decodeSavedReport(filter)
		out.Reports = append(out.Reports, savedReportProto(filter, config))
	}
	return out, nil
}

func (s reportService) GetSavedReport(
	ctx context.Context, req *agentifiv1.GetSavedReportRequest,
) (*agentifiv1.GetSavedReportResponse, error) {
	filter, config, err := liveSavedReport(ctx, s.env, spaceFrom(ctx), req.GetReportId())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetSavedReportResponse{Report: savedReportProto(filter, config)}, nil
}

func (s reportService) CreateSavedReport(
	ctx context.Context, req *agentifiv1.CreateSavedReportRequest,
) (*agentifiv1.CreateSavedReportResponse, error) {
	if strings.TrimSpace(req.GetName()) == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	config, err := checkReportConfig(reportConfigFrom(req.GetConfig()))
	if err != nil {
		return nil, err
	}
	writes, err := filterItemWritesOf(req.GetItems())
	if err != nil {
		return nil, err
	}
	items, err := buildFilterItems(writes)
	if err != nil {
		return nil, err
	}

	stored := &store.Filter{
		Name:      req.GetName(),
		Scope:     reportScope,
		QueryText: encodeSavedReport(config, req.GetQueryText()),
		Items:     items,
	}
	if err := s.env.DB.CreateFilter(ctx, spaceFrom(ctx).ID(), stored); err != nil {
		return nil, err
	}
	return &agentifiv1.CreateSavedReportResponse{Report: savedReportProto(*stored, config)}, nil
}

// UpdateSavedReport reads its own mask: items is a list, which has no
// presence for maskOf to read, so naming it is what asks for a replacement.
func (s reportService) UpdateSavedReport(
	ctx context.Context, req *agentifiv1.UpdateSavedReportRequest,
) (*agentifiv1.UpdateSavedReportResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	filter, config, err := liveSavedReport(ctx, env, sp, req.GetReportId())
	if err != nil {
		return nil, err
	}
	mask, err := savedReportMask(req)
	if err != nil {
		return nil, err
	}
	if err := applyRequired("name", optOf(mask, "name", req.Name), &filter.Name); err != nil {
		return nil, err
	}
	if mask["config"] && req.GetConfig() != nil {
		if config, err = checkReportConfig(reportConfigFrom(req.GetConfig())); err != nil {
			return nil, err
		}
	}
	_, text := decodeSavedReport(filter)
	filter.QueryText = encodeSavedReport(config, text)

	if err := env.DB.UpdateFilter(ctx, sp.ID(), &filter); err != nil {
		return nil, err
	}
	if mask["items"] {
		writes, err := filterItemWritesOf(req.GetItems())
		if err != nil {
			return nil, err
		}
		items, err := buildFilterItems(writes)
		if err != nil {
			return nil, err
		}
		filter.Items = items
		if err := env.DB.ReplaceFilterItems(ctx, sp.ID(), &filter); err != nil {
			return nil, err
		}
	}
	return &agentifiv1.UpdateSavedReportResponse{Report: savedReportProto(filter, config)}, nil
}

// savedReportMask is the fields an update changes: those update_mask names,
// or without one, those set (a list only when it holds something).
func savedReportMask(req *agentifiv1.UpdateSavedReportRequest) (patchMask, error) {
	set := map[string]bool{
		"name":   req.Name != nil,
		"config": req.Config != nil,
		"items":  len(req.GetItems()) > 0,
	}
	if req.GetUpdateMask() == nil {
		mask := patchMask{}
		for name, isSet := range set {
			if isSet {
				mask[name] = true
			}
		}
		return mask, nil
	}
	mask := patchMask{}
	for _, path := range req.GetUpdateMask().GetPaths() {
		if _, known := set[path]; !known {
			return nil, errInvalid("extra_forbidden", []string{"body", path},
				"%s is not a field this request can change", path)
		}
		mask[path] = true
	}
	for _, name := range []string{"name", "config", "items"} {
		if set[name] && !mask[name] {
			return nil, errInvalid("extra_forbidden", []string{"body", name},
				"%s is set but update_mask does not name it", name)
		}
	}
	return mask, nil
}

func (s reportService) DeleteSavedReport(
	ctx context.Context, req *agentifiv1.DeleteSavedReportRequest,
) (*agentifiv1.DeleteSavedReportResponse, error) {
	sp := spaceFrom(ctx)
	filter, _, err := liveSavedReport(ctx, s.env, sp, req.GetReportId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteFilter(ctx, sp.ID(), filter.ID); err != nil {
		return nil, notFoundAs(err, "Report")
	}
	return &agentifiv1.DeleteSavedReportResponse{}, nil
}

func liveSavedReport(
	ctx context.Context, env *Env, sp auth.SpaceContext, rawID string,
) (store.Filter, ReportConfig, error) {
	id, err := idFrom(rawID, "Report")
	if err != nil {
		return store.Filter{}, ReportConfig{}, err
	}
	filter, err := env.DB.GetFilter(ctx, sp.ID(), id)
	if err != nil {
		return store.Filter{}, ReportConfig{}, notFoundAs(err, "Report")
	}
	if filter.IsDeleted || filter.Scope != reportScope {
		return store.Filter{}, ReportConfig{}, errNotFound("Report")
	}
	config, _ := decodeSavedReport(filter)
	return filter, config, nil
}

// savedReportProto renders the filter through filterResponse, the filter's one
// renderer, with the report's own free text in place of the encoded shell.
func savedReportProto(filter store.Filter, config ReportConfig) *agentifiv1.SavedReport {
	response := filterResponse(filter)
	_, text := decodeSavedReport(filter)
	out := &agentifiv1.SavedReportFilter{
		Id:        response.ID.String(),
		Name:      response.Name,
		Scope:     response.Scope,
		QueryText: dbconv.NullText(text),
		Position:  int32(response.Position),
		Items:     make([]*agentifiv1.SavedReportFilterItem, 0, len(response.Items)),
	}
	for _, item := range response.Items {
		out.Items = append(out.Items, &agentifiv1.SavedReportFilterItem{
			Id:         item.ID.String(),
			Field:      string(item.Field),
			Operator:   string(item.Operator),
			GroupIndex: int32(item.GroupIndex),
			Position:   int32(item.Position),
			Negated:    item.Negated,
			ValueIds:   idStrings(item.ValueIDs),
			ValueTexts: item.ValueTexts,
			Text:       item.Text,
			AmountMin:  moneyPtrProto(item.AmountMin),
			AmountMax:  moneyPtrProto(item.AmountMax),
			DateFrom:   datePtrProto(item.DateFrom),
			DateTo:     datePtrProto(item.DateTo),
			DatePreset: item.DatePreset,
			State:      item.State,
		})
	}
	return &agentifiv1.SavedReport{
		Id:     filter.ID.String(),
		Name:   filter.Name,
		Config: reportConfigProto(config),
		Filter: out,
	}
}

func datePtrProto(d *Date) *string {
	if d == nil {
		return nil
	}
	return proto.String(domain.Date(*d).String())
}

// filterItemWritesOf is the items as buildFilterItems, the filter's one
// validator, reads them.
func filterItemWritesOf(items []*agentifiv1.SavedReportFilterItemInput) ([]FilterItemWrite, error) {
	out := make([]FilterItemWrite, 0, len(items))
	for _, item := range items {
		write := FilterItemWrite{
			Field:      domain.FilterField(item.GetField()),
			Operator:   domain.FilterOperator(item.GetOperator()),
			GroupIndex: int(item.GetGroupIndex()),
			Position:   int(item.GetPosition()),
			Negated:    item.GetNegated(),
			ValueTexts: item.GetValueTexts(),
			Text:       item.Text,
			DatePreset: item.DatePreset,
			State:      item.State,
		}
		for _, raw := range item.GetValueIds() {
			id, err := uuid.Parse(raw)
			if err != nil {
				return nil, errInvalid("uuid_parsing", []string{"body", "items", "value_ids"},
					"%q is not a uuid", raw)
			}
			write.ValueIDs = append(write.ValueIDs, id)
		}
		for _, bound := range []struct {
			name  string
			value *agentifiv1.NullableMoney
			into  **domain.Money
		}{
			{"amount_min", item.GetAmountMin(), &write.AmountMin},
			{"amount_max", item.GetAmountMax(), &write.AmountMax},
		} {
			if bound.value == nil {
				continue
			}
			amount, err := moneyFrom(bound.value, "body", "items", bound.name)
			if err != nil {
				return nil, err
			}
			*bound.into = &amount
		}
		for _, bound := range []struct {
			name  string
			value *string
			into  **Date
		}{
			{"date_from", item.DateFrom, &write.DateFrom},
			{"date_to", item.DateTo, &write.DateTo},
		} {
			if bound.value == nil {
				continue
			}
			on, err := parseDate(*bound.value)
			if err != nil {
				return nil, errInvalid("date_parsing", []string{"body", "items", bound.name}, "%s", err)
			}
			wire := Date(on)
			*bound.into = &wire
		}
		out = append(out, write)
	}
	return out, nil
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
