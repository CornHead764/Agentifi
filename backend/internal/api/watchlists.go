package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Watchlists: a saved slice of spending, backed by the one filter entity.
//
// The summary carries no twelve-month average. The trend goes over the wire
// with `is_partial` per entry and the client averages the full months, so the
// bars and the average cannot disagree and the current month never drags it
// down.
//
// Spend is the net of the matching rows, never the sum of magnitudes, so a
// refund reduces it (calculations.md §13, a departure from §7).

func init() {
	Register(Resource{Prefix: "/watchlists", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listWatchlistsRoute)
		rt.Write(http.MethodPost, "/", createWatchlist)
		rt.Read(http.MethodGet, "/{watchlist_id}", readWatchlist)
		rt.Write(http.MethodPatch, "/{watchlist_id}", updateWatchlist)
		rt.Write(http.MethodDelete, "/{watchlist_id}", deleteWatchlist)
	}})
}

// WatchlistFilterScope marks the filters this resource creates.
const WatchlistFilterScope = "watchlist"

// watchlistPeriods are the periods a watchlist is written with, each keyed by
// itself and by its "-ly" spelling, which stored rows may carry and an edit
// sends back unchanged.
var watchlistPeriods = map[string]string{
	"month": "month", "monthly": "month",
	"quarter": "quarter", "quarterly": "quarter",
	"year": "year", "yearly": "year",
	"custom": "custom",
}

// watchlistPeriod is the stored spelling of a period sent on the wire.
func watchlistPeriod(sent string) (string, error) {
	period, ok := watchlistPeriods[strings.ToLower(strings.TrimSpace(sent))]
	if !ok {
		return "", errInvalid("enum", []string{"body", "period"},
			"%q is not a watchlist period; send month, quarter, year or custom", sent)
	}
	return period, nil
}

// defaultTrendMonths is how many bars the Overtime chart draws by default:
// thirteen, so this month compares with the same month a year ago.
const defaultTrendMonths = 13

// maxTrendMonths bounds one request's month walk.
const maxTrendMonths = 60

// --- Wire types --------------------------------------------------------------

// TrendEntry is one bar of the Overtime chart. IsPartial marks the current
// month, which no average may include.
type TrendEntry struct {
	Month     string       `json:"month"`
	Spent     domain.Money `json:"spent"`
	IsPartial bool         `json:"is_partial"`
}

// WatchlistSummary is the card. There is deliberately no twelve_month_average;
// the client averages the full months of MonthlyTrend.
type WatchlistSummary struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	FilterID uuid.UUID `json:"filter_id"`
	Emoji    *string   `json:"emoji"`
	Period   string    `json:"period"`

	TargetAmount *domain.Money `json:"target_amount"`
	// LeftToTarget goes negative once the target is breached. Null when the
	// user set no target — an unset target cannot be breached.
	LeftToTarget *domain.Money `json:"left_to_target"`
	// PctOfTarget is null with no target, and also with a target of zero,
	// which cannot be divided into.
	PctOfTarget           *domain.Rate `json:"pct_of_target"`
	IsOverTarget          bool         `json:"is_over_target"`
	IsProjectedOverTarget bool         `json:"is_projected_over_target"`

	ThisMonthSpent  domain.Money `json:"this_month_spent"`
	MonthProjection domain.Money `json:"month_projection"`
	YearToDate      domain.Money `json:"year_to_date"`
	MonthlyTrend    []TrendEntry `json:"monthly_trend"`

	AsOf Date `json:"as_of"`
}

// BreakdownEntry is one slice of the Category / Payee / Tag donut.
//
// Shares are fractions of the window's total spend, so a two-tag transaction
// counts fully under each tag and shares can pass 100%. Key is a category or
// tag id, or the payee's name; Label is resolved here. Both are empty for the
// uncategorized or untagged slice.
type BreakdownEntry struct {
	Key   string       `json:"key"`
	Label string       `json:"label"`
	Spent domain.Money `json:"spent"`
	Share *domain.Rate `json:"share"`
}

type WatchlistDetail struct {
	WatchlistSummary
	// Month is the window the breakdown covers, which the user selects; the
	// card's figures are always about now.
	Month      string           `json:"month"`
	Spent      domain.Money     `json:"spent"`
	ByCategory []BreakdownEntry `json:"by_category"`
	ByPayee    []BreakdownEntry `json:"by_payee"`
	ByTag      []BreakdownEntry `json:"by_tag"`
}

type WatchlistCreate struct {
	Name         string        `json:"name"`
	Emoji        *string       `json:"emoji"`
	TargetAmount *domain.Money `json:"target_amount"`
	Period       *string       `json:"period"`
	// FilterID points at an existing saved selection. Items are written into a
	// filter the watchlist owns. The three lists are shortcuts for one item
	// each. Exactly one of the five must be sent.
	FilterID    *uuid.UUID        `json:"filter_id"`
	Items       []FilterItemWrite `json:"items"`
	CategoryIDs []uuid.UUID       `json:"category_ids"`
	PayeeNames  []string          `json:"payee_names"`
	TagIDs      []uuid.UUID       `json:"tag_ids"`
	TrendMonths *int              `json:"trend_months"`
}

// WatchlistUpdate edits the label, the target, the period and the selection. A
// null target clears it.
//
// FilterID repoints the card; Items rewrite the selection, in place when the
// watchlist owns its filter and into a new owned one otherwise, so editing the
// card never edits a saved report. At most one of the two.
type WatchlistUpdate struct {
	Name         Opt[string]        `json:"name"`
	Emoji        Opt[string]        `json:"emoji"`
	TargetAmount Opt[domain.Money]  `json:"target_amount"`
	Period       Opt[string]        `json:"period"`
	FilterID     Opt[uuid.UUID]     `json:"filter_id"`
	Items        *[]FilterItemWrite `json:"items"`
}

// --- Handlers ----------------------------------------------------------------

func listWatchlistsRoute(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := listWatchlistRows(r.Context(), env, sp)
	if err != nil {
		return err
	}
	summaries, err := summarizeWatchlists(r.Context(), env, sp, rows)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, summaries)
}

func createWatchlist(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body WatchlistCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Name) == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	period := "month"
	if body.Period != nil {
		checked, err := watchlistPeriod(*body.Period)
		if err != nil {
			return err
		}
		period = checked
	}
	subjects := 0
	if body.FilterID != nil {
		subjects++
	}
	for _, count := range []int{
		len(body.Items), len(body.CategoryIDs), len(body.PayeeNames), len(body.TagIDs),
	} {
		if count > 0 {
			subjects++
		}
	}
	// None would watch the whole ledger; more than one is ambiguous.
	if subjects != 1 {
		return errInvalid("missing", []string{"body", "filter_id"},
			"send exactly one of filter_id, items, category_ids, payee_names or tag_ids")
	}

	var filterID uuid.UUID
	if body.FilterID != nil {
		// Any live filter in the space: a report's, an envelope's or another
		// watchlist's.
		stored, err := requireFilter(r.Context(), env, sp, *body.FilterID)
		if err != nil {
			return err
		}
		filterID = stored.ID
	} else {
		var items []store.FilterItem
		if len(body.Items) > 0 {
			built, err := buildFilterItems(body.Items)
			if err != nil {
				return err
			}
			items = built
		} else {
			item, err := watchlistSubject(r.Context(), env, sp, body)
			if err != nil {
				return err
			}
			items = []store.FilterItem{item}
		}
		filter := &store.Filter{Name: body.Name, Scope: WatchlistFilterScope, Items: items}
		if err := env.DB.CreateFilter(r.Context(), sp.ID(), filter); err != nil {
			return err
		}
		filterID = filter.ID
	}

	trend := defaultTrendMonths
	if body.TrendMonths != nil {
		trend = *body.TrendMonths
		if trend < 1 || trend > maxTrendMonths {
			return errInvalid("out_of_range", []string{"body", "trend_months"},
				"trend_months must be between 1 and %d", maxTrendMonths)
		}
	}

	row := watchlistRow{
		Watchlist: store.Watchlist{
			FilterID: filterID,
			Name:     body.Name,
			Emoji:    store.Deref(body.Emoji, ""),
			Period:   period,
		},
		TrendMonths: trend,
	}
	if body.TargetAmount != nil {
		row.TargetAmount, row.HasTarget = body.TargetAmount.Round(), true
	}
	if err := env.DB.CreateWatchlist(r.Context(), sp.ID(), &row.Watchlist); err != nil {
		return err
	}

	summaries, err := summarizeWatchlists(r.Context(), env, sp, []watchlistRow{row})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, summaries[0])
}

func readWatchlist(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "watchlist_id", "Watchlist")
	if err != nil {
		return err
	}
	row, err := loadWatchlist(r.Context(), env, sp, id)
	if err != nil {
		return err
	}
	today := domain.DateOf(env.now())
	month, given, err := queryMonth(r, "month")
	if err != nil {
		return err
	}
	if !given {
		month = domain.MonthOf(today)
	}

	// The summary looks back over the whole trend, and the breakdown is about
	// the selected month, so the load spans both.
	from := domain.MonthOf(today).Shift(-(row.TrendMonths - 1))
	if month.Before(from) {
		from = month
	}
	to := domain.MonthOf(today)
	if month.After(to) {
		to = month
	}
	postings, txnRows, err := watchlistPostings(r.Context(), env, sp, from, to)
	if err != nil {
		return err
	}
	matcher, filters, err := watchlistMatcher(r.Context(), env, sp, []watchlistRow{row}, txnRows)
	if err != nil {
		return err
	}
	if _, ok := filters[domain.ID(row.FilterID.String())]; !ok {
		return errConflict("watchlist %s points at a filter that is not in this space", row.ID)
	}

	watchlist := row.domainWatchlist()
	summary := domain.SummarizeWatchlist(watchlist, postings, matcher, today, domain.DateEffective)
	detail := WatchlistDetail{
		WatchlistSummary: watchlistSummary(row, summary, today),
		Month:            month.String(),
		Spent: domain.WatchlistSpentInMonth(
			watchlist, postings, matcher, month, domain.DateEffective),
	}
	names, err := breakdownNamesFor(r.Context(), env, sp)
	if err != nil {
		return err
	}
	for _, spec := range []struct {
		dimension domain.BreakdownDimension
		into      *[]BreakdownEntry
	}{
		{domain.BreakdownByCategory, &detail.ByCategory},
		{domain.BreakdownByPayee, &detail.ByPayee},
		{domain.BreakdownByTag, &detail.ByTag},
	} {
		rows := domain.WatchlistBreakdown(watchlist, postings, matcher,
			month.FirstDay(), month.LastDay(), spec.dimension, domain.DateEffective)
		*spec.into = breakdownEntries(rows, names, spec.dimension)
	}
	return writeJSON(w, http.StatusOK, detail)
}

func updateWatchlist(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "watchlist_id", "Watchlist")
	if err != nil {
		return err
	}
	row, err := loadWatchlist(r.Context(), env, sp, id)
	if err != nil {
		return err
	}
	var body WatchlistUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Name.Set {
		if body.Name.Null || strings.TrimSpace(body.Name.Value) == "" {
			return errInvalid("missing", []string{"body", "name"}, "name is required")
		}
		row.Name = strings.TrimSpace(body.Name.Value)
	}
	if body.Emoji.Set {
		row.Emoji = body.Emoji.Value
	}
	if body.TargetAmount.Set {
		if body.TargetAmount.Null {
			row.TargetAmount, row.HasTarget = domain.Zero, false
		} else {
			row.TargetAmount, row.HasTarget = body.TargetAmount.Value.Round(), true
		}
	}
	if body.Period.Set {
		if body.Period.Null || strings.TrimSpace(body.Period.Value) == "" {
			return errInvalid("missing", []string{"body", "period"}, "period is required")
		}
		period, err := watchlistPeriod(body.Period.Value)
		if err != nil {
			return err
		}
		row.Period = period
	}
	if body.FilterID.Set && body.Items != nil {
		return errInvalid("invalid", []string{"body", "filter_id"},
			"send filter_id or items, not both")
	}
	if body.FilterID.Set {
		if body.FilterID.Null {
			return errInvalid("missing", []string{"body", "filter_id"}, "filter_id is required")
		}
		stored, err := requireFilter(r.Context(), env, sp, body.FilterID.Value)
		if err != nil {
			return err
		}
		row.FilterID = stored.ID
	}
	if body.Items != nil {
		filterID, err := rewriteWatchlistSelection(r.Context(), env, sp, row.Watchlist, *body.Items)
		if err != nil {
			return err
		}
		row.FilterID = filterID
	}
	if err := env.DB.UpdateWatchlist(r.Context(), sp.ID(), row.Watchlist); err != nil {
		return notFoundAs(err, "Watchlist")
	}
	summaries, err := summarizeWatchlists(r.Context(), env, sp, []watchlistRow{row})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, summaries[0])
}

// rewriteWatchlistSelection replaces what the watchlist counts and answers the
// filter that now holds it: its own filter in place, or a new one rather than
// editing someone else's.
func rewriteWatchlistSelection(
	ctx context.Context, env *Env, sp auth.SpaceContext, row store.Watchlist, writes []FilterItemWrite,
) (uuid.UUID, error) {
	items, err := buildFilterItems(writes)
	if err != nil {
		return uuid.Nil, err
	}
	// No items is a watchlist over the whole ledger, which nobody meant.
	if len(items) == 0 {
		return uuid.Nil, errInvalid("missing", []string{"body", "items"},
			"a watchlist must keep at least one item")
	}
	current, err := env.DB.GetFilter(ctx, sp.ID(), row.FilterID)
	if err != nil && !isNotFound(err) {
		return uuid.Nil, err
	}
	if err == nil && !current.IsDeleted && current.Scope == WatchlistFilterScope {
		current.Items = items
		if err := env.DB.ReplaceFilterItems(ctx, sp.ID(), &current); err != nil {
			return uuid.Nil, err
		}
		return current.ID, nil
	}
	filter := &store.Filter{Name: row.Name, Scope: WatchlistFilterScope, Items: items}
	if err := env.DB.CreateFilter(ctx, sp.ID(), filter); err != nil {
		return uuid.Nil, err
	}
	return filter.ID, nil
}

// deleteWatchlist soft-deletes the watchlist and the filter only it uses
// (store.DeleteWatchlist).
func deleteWatchlist(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "watchlist_id", "Watchlist")
	if err != nil {
		return err
	}
	if _, err := loadWatchlist(r.Context(), env, sp, id); err != nil {
		return err
	}
	if err := env.DB.DeleteWatchlist(r.Context(), sp.ID(), id); err != nil {
		return err
	}
	return writeNoContent(w)
}

// --- Summaries ---------------------------------------------------------------

func summarizeWatchlists(ctx context.Context, env *Env, sp auth.SpaceContext, rows []watchlistRow) ([]WatchlistSummary, error) {
	out := make([]WatchlistSummary, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	today := domain.DateOf(env.now())

	// One load for every card, over the longest trend any of them asks for,
	// and back to January so the year-to-date figure is not truncated.
	longest := defaultTrendMonths
	for _, row := range rows {
		if row.TrendMonths > longest {
			longest = row.TrendMonths
		}
	}
	from := domain.MonthOf(today).Shift(-(longest - 1))
	january := domain.NewMonth(today.Year, time.January)
	if january.Before(from) {
		from = january
	}
	postings, txnRows, err := watchlistPostings(ctx, env, sp, from, domain.MonthOf(today))
	if err != nil {
		return nil, err
	}
	matcher, filters, err := watchlistMatcher(ctx, env, sp, rows, txnRows)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		if _, ok := filters[domain.ID(row.FilterID.String())]; !ok {
			// A watchlist whose filter was hard-deleted would otherwise match
			// nothing and read as zero spending, which looks like a fact.
			return nil, errConflict("watchlist %s points at a filter that is not in this space", row.ID)
		}
		summary := domain.SummarizeWatchlist(
			row.domainWatchlist(), postings, matcher, today, domain.DateEffective)
		out = append(out, watchlistSummary(row, summary, today))
	}
	return out, nil
}

func watchlistSummary(row watchlistRow, summary domain.WatchlistSummary, today domain.Date) WatchlistSummary {
	out := WatchlistSummary{
		ID:                    row.ID,
		Name:                  row.Name,
		FilterID:              row.FilterID,
		Emoji:                 pgconv.NullText(row.Emoji),
		Period:                row.Period,
		TargetAmount:          store.PtrIf(row.TargetAmount, row.HasTarget),
		IsOverTarget:          summary.IsOverTarget(),
		IsProjectedOverTarget: summary.IsProjectedOverTarget(),
		ThisMonthSpent:        summary.ThisMonthSpent,
		MonthProjection:       summary.MonthProjection,
		YearToDate:            summary.YearToDate,
		MonthlyTrend:          make([]TrendEntry, 0, len(summary.MonthlyTrend)),
		AsOf:                  Date(today),
	}
	if left, ok := summary.LeftToTarget(); ok {
		out.LeftToTarget = &left
	}
	if pct, ok := summary.PctOfTarget(); ok {
		out.PctOfTarget = &pct
	}
	for _, entry := range summary.MonthlyTrend {
		out.MonthlyTrend = append(out.MonthlyTrend, TrendEntry{
			Month:     entry.Month.String(),
			Spent:     entry.Spent,
			IsPartial: entry.IsPartial,
		})
	}
	return out
}

func breakdownEntries(rows []domain.BreakdownRow, names breakdownNames, dimension domain.BreakdownDimension) []BreakdownEntry {
	out := make([]BreakdownEntry, 0, len(rows))
	for _, row := range rows {
		entry := BreakdownEntry{
			Key:   row.Key,
			Label: names.label(dimension, row.Key),
			Spent: row.Spent,
		}
		if row.HasShare {
			share := row.Share
			entry.Share = &share
		}
		out = append(out, entry)
	}
	return out
}

// breakdownNames resolves the ids a breakdown groups by into what a chip reads.
type breakdownNames struct {
	categories map[uuid.UUID]string
	tags       map[uuid.UUID]string
}

// label is the printable name of one slice. The payee dimension groups by the
// name itself; the other two group by id, and an id in a chip is not a label.
func (n breakdownNames) label(dimension domain.BreakdownDimension, key string) string {
	switch dimension {
	case domain.BreakdownByPayee:
		return key
	case domain.BreakdownByTag:
		return n.lookup(n.tags, key)
	}
	return n.lookup(n.categories, key)
}

func (n breakdownNames) lookup(names map[uuid.UUID]string, key string) string {
	id, err := uuid.Parse(key)
	if err != nil {
		return ""
	}
	return names[id]
}

// breakdownNamesFor loads the names, deleted rows included, so a deleted tag
// still labels its spending.
func breakdownNamesFor(ctx context.Context, env *Env, sp auth.SpaceContext) (breakdownNames, error) {
	categories, err := env.DB.ListCategories(ctx, sp.ID(), true)
	if err != nil {
		return breakdownNames{}, err
	}
	tags, err := env.DB.ListTags(ctx, sp.ID(), true)
	if err != nil {
		return breakdownNames{}, err
	}

	names := breakdownNames{
		categories: make(map[uuid.UUID]string, len(categories)),
		tags:       make(map[uuid.UUID]string, len(tags)),
	}
	for _, category := range categories {
		names.categories[category.ID] = category.Name
	}
	for _, tag := range tags {
		names.tags[tag.ID] = tag.Name
	}
	return names, nil
}

// watchlistPostings loads the ledger over the window every figure on the card
// needs, filed by effective date because that is the date reports read.
func watchlistPostings(
	ctx context.Context,
	env *Env,
	sp auth.SpaceContext,
	from, to domain.Month,
) ([]domain.Posting, map[uuid.UUID]store.Transaction, error) {
	return service.LoadPostings(ctx, env.DB, sp.ID(), store.TransactionQuery{
		From:     from.FirstDay(),
		To:       to.LastDay(),
		DateMode: domain.DateEffective,
	})
}

// watchlistMatcher yields the allocations a watchlist's filter selects: per
// allocation, not per row, by internal/domain's one implementation.
func watchlistMatcher(
	ctx context.Context,
	env *Env,
	sp auth.SpaceContext,
	rows []watchlistRow,
	txnRows map[uuid.UUID]store.Transaction,
) (domain.WatchlistMatcher, map[domain.ID]domain.Filter, error) {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.FilterID)
	}
	filters, facets, err := service.PrepareFilters(ctx, env.DB, sp.ID(), ids, txnRows)
	if err != nil {
		return nil, nil, err
	}

	// Deleted categories included: they still label and exclude their spending.
	categoryRows, err := env.DB.ListCategories(ctx, sp.ID(), true)
	if err != nil {
		return nil, nil, err
	}
	categories := make(map[domain.ID]domain.Category, len(categoryRows))
	for _, row := range categoryRows {
		categories[domain.ID(row.ID.String())] = store.DomainCategory(row)
	}
	return domain.WatchlistMatcherFor(filters, facets, categories), filters, nil
}

// --- Rows --------------------------------------------------------------------

// watchlistRow is a stored watchlist plus the length of its chart, which no
// column holds.
type watchlistRow struct {
	store.Watchlist
	TrendMonths int
}

func (w watchlistRow) domainWatchlist() domain.Watchlist {
	return domain.Watchlist{
		ID:           domain.ID(w.ID.String()),
		Name:         w.Name,
		FilterID:     domain.ID(w.FilterID.String()),
		TargetAmount: w.TargetAmount,
		HasTarget:    w.HasTarget,
		Emoji:        w.Emoji,
		TrendMonths:  w.TrendMonths,
	}
}

func listWatchlistRows(ctx context.Context, env *Env, sp auth.SpaceContext) ([]watchlistRow, error) {
	stored, err := env.DB.ListWatchlists(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	out := make([]watchlistRow, 0, len(stored))
	for _, one := range stored {
		out = append(out, watchlistRow{Watchlist: one, TrendMonths: defaultTrendMonths})
	}
	return out, nil
}

func loadWatchlist(ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID) (watchlistRow, error) {
	stored, err := env.DB.GetWatchlist(ctx, sp.ID(), id)
	if err != nil {
		return watchlistRow{}, notFoundAs(err, "Watchlist")
	}
	return watchlistRow{Watchlist: stored, TrendMonths: defaultTrendMonths}, nil
}

// watchlistSubject is the one filter item a shortcut stands for, exactly the
// item a saved filter would hold, so both claim the same rows. A payee is
// matched by set membership on the display name, as the register's Payees
// facet does, never by substring.
func watchlistSubject(
	ctx context.Context, env *Env, sp auth.SpaceContext, body WatchlistCreate,
) (store.FilterItem, error) {
	switch {
	case len(body.CategoryIDs) > 0:
		for _, id := range body.CategoryIDs {
			if err := checkCategory(ctx, env, sp, id); err != nil {
				return store.FilterItem{}, err
			}
		}
		return store.FilterItem{
			Field:    string(domain.FieldCategory),
			Operator: string(domain.OpIn),
			ValueIDs: body.CategoryIDs,
		}, nil

	case len(body.TagIDs) > 0:
		tagIDs, err := resolveTags(ctx, env, sp, body.TagIDs)
		if err != nil {
			return store.FilterItem{}, err
		}
		return store.FilterItem{
			Field:    string(domain.FieldTag),
			Operator: string(domain.OpIn),
			ValueIDs: tagIDs,
		}, nil
	}

	names := make([]string, 0, len(body.PayeeNames))
	seen := map[string]bool{}
	for _, name := range body.PayeeNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		names = append(names, trimmed)
	}
	if len(names) == 0 {
		return store.FilterItem{}, errInvalid("missing", []string{"body", "payee_names"},
			"payee_names must name at least one payee")
	}
	return store.FilterItem{
		Field:      string(domain.FieldPayee),
		Operator:   string(domain.OpIn),
		ValueTexts: names,
	}, nil
}
