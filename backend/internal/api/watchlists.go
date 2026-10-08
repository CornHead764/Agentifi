package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewWatchlistServiceHandler(watchlistService{env}, opts...)
	})
}

type watchlistService struct{ env *Env }

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

// --- Handlers ----------------------------------------------------------------

func (s watchlistService) ListWatchlists(
	ctx context.Context, _ *agentifiv1.ListWatchlistsRequest,
) (*agentifiv1.ListWatchlistsResponse, error) {
	sp := spaceFrom(ctx)
	rows, err := listWatchlistRows(ctx, s.env, sp)
	if err != nil {
		return nil, err
	}
	summaries, err := summarizeWatchlists(ctx, s.env, sp, rows)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ListWatchlistsResponse{Watchlists: summaries}, nil
}

func (s watchlistService) CreateWatchlist(
	ctx context.Context, req *agentifiv1.CreateWatchlistRequest,
) (*agentifiv1.CreateWatchlistResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if strings.TrimSpace(req.GetName()) == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	period := "month"
	if req.Period != nil {
		checked, err := watchlistPeriod(req.GetPeriod())
		if err != nil {
			return nil, err
		}
		period = checked
	}
	subjects := 0
	if req.FilterId != nil {
		subjects++
	}
	for _, count := range []int{
		len(req.GetItems()), len(req.GetCategoryIds()), len(req.GetPayeeNames()), len(req.GetTagIds()),
	} {
		if count > 0 {
			subjects++
		}
	}
	// None would watch the whole ledger; more than one is ambiguous.
	if subjects != 1 {
		return nil, errInvalid("missing", []string{"body", "filter_id"},
			"send exactly one of filter_id, items, category_ids, payee_names or tag_ids")
	}

	var filterID uuid.UUID
	if req.FilterId != nil {
		id, err := uuidFrom(req.GetFilterId(), "body", "filter_id")
		if err != nil {
			return nil, err
		}
		// Any live filter in the space: a report's, an envelope's or another
		// watchlist's.
		stored, err := requireFilter(ctx, env, sp, id)
		if err != nil {
			return nil, err
		}
		filterID = stored.ID
	} else {
		var items []store.FilterItem
		if len(req.GetItems()) > 0 {
			writes, err := filterItemWrites(req.GetItems(), "items")
			if err != nil {
				return nil, err
			}
			if items, err = buildFilterItems(writes); err != nil {
				return nil, err
			}
		} else {
			item, err := watchlistSubject(ctx, env, sp, req)
			if err != nil {
				return nil, err
			}
			items = []store.FilterItem{item}
		}
		filter := &store.Filter{Name: req.GetName(), Scope: WatchlistFilterScope, Items: items}
		if err := env.DB.CreateFilter(ctx, sp.ID(), filter); err != nil {
			return nil, err
		}
		filterID = filter.ID
	}

	trend := defaultTrendMonths
	if req.TrendMonths != nil {
		trend = int(req.GetTrendMonths())
		if trend < 1 || trend > maxTrendMonths {
			return nil, errInvalid("out_of_range", []string{"body", "trend_months"},
				"trend_months must be between 1 and %d", maxTrendMonths)
		}
	}

	row := watchlistRow{
		Watchlist: store.Watchlist{
			FilterID: filterID,
			Name:     req.GetName(),
			Emoji:    req.GetEmoji(),
			Period:   period,
		},
		TrendMonths: trend,
	}
	if req.TargetAmount != nil {
		target, err := moneyFrom(req.TargetAmount, "body", "target_amount")
		if err != nil {
			return nil, err
		}
		row.TargetAmount, row.HasTarget = target.Round(), true
	}
	if err := env.DB.CreateWatchlist(ctx, sp.ID(), &row.Watchlist); err != nil {
		return nil, err
	}

	summaries, err := summarizeWatchlists(ctx, env, sp, []watchlistRow{row})
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateWatchlistResponse{Watchlist: summaries[0]}, nil
}

func (s watchlistService) GetWatchlist(
	ctx context.Context, req *agentifiv1.GetWatchlistRequest,
) (*agentifiv1.GetWatchlistResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	row, err := watchlistByID(ctx, env, sp, req.GetWatchlistId())
	if err != nil {
		return nil, err
	}
	today := domain.DateOf(env.now())
	month := domain.MonthOf(today)
	if raw := strings.TrimSpace(req.GetMonth()); raw != "" {
		if month, err = parseMonth(raw); err != nil {
			return nil, errInvalid("date_parsing", []string{"query", "month"}, "%s", err)
		}
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
	postings, txnRows, err := watchlistPostings(ctx, env, sp, from, to)
	if err != nil {
		return nil, err
	}
	matcher, filters, err := watchlistMatcher(ctx, env, sp, []watchlistRow{row}, txnRows)
	if err != nil {
		return nil, err
	}
	if _, ok := filters[domain.ID(row.FilterID.String())]; !ok {
		return nil, errConflict("watchlist %s points at a filter that is not in this space", row.ID)
	}

	watchlist := row.domainWatchlist()
	summary := watchlistSummary(row,
		domain.SummarizeWatchlist(watchlist, postings, matcher, today, domain.DateEffective), today)
	detail := &agentifiv1.WatchlistDetail{
		Id:                    summary.Id,
		Name:                  summary.Name,
		FilterId:              summary.FilterId,
		Emoji:                 summary.Emoji,
		Period:                summary.Period,
		TargetAmount:          summary.TargetAmount,
		LeftToTarget:          summary.LeftToTarget,
		PctOfTarget:           summary.PctOfTarget,
		IsOverTarget:          summary.IsOverTarget,
		IsProjectedOverTarget: summary.IsProjectedOverTarget,
		ThisMonthSpent:        summary.ThisMonthSpent,
		MonthProjection:       summary.MonthProjection,
		YearToDate:            summary.YearToDate,
		MonthlyTrend:          summary.MonthlyTrend,
		AsOf:                  summary.AsOf,
		Month:                 month.String(),
		Spent: moneyProto(domain.WatchlistSpentInMonth(
			watchlist, postings, matcher, month, domain.DateEffective)),
	}
	names, err := breakdownNamesFor(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	for _, spec := range []struct {
		dimension domain.BreakdownDimension
		into      *[]*agentifiv1.WatchlistBreakdownEntry
	}{
		{domain.BreakdownByCategory, &detail.ByCategory},
		{domain.BreakdownByPayee, &detail.ByPayee},
		{domain.BreakdownByTag, &detail.ByTag},
	} {
		rows := domain.WatchlistBreakdown(watchlist, postings, matcher,
			month.FirstDay(), month.LastDay(), spec.dimension, domain.DateEffective)
		*spec.into = breakdownEntries(rows, names, spec.dimension)
	}
	return &agentifiv1.GetWatchlistResponse{Watchlist: detail}, nil
}

func (s watchlistService) UpdateWatchlist(
	ctx context.Context, req *agentifiv1.UpdateWatchlistRequest,
) (*agentifiv1.UpdateWatchlistResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	row, err := watchlistByID(ctx, env, sp, req.GetWatchlistId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	if name := optOf(mask, "name", req.Name); name.Set {
		if name.Null || strings.TrimSpace(name.Value) == "" {
			return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
		}
		row.Name = strings.TrimSpace(name.Value)
	}
	applyNullable(optOf(mask, "emoji", req.Emoji), &row.Emoji)
	target, err := optMoneyOf(mask, "target_amount", req.TargetAmount)
	if err != nil {
		return nil, err
	}
	if target.Set {
		if target.Null {
			row.TargetAmount, row.HasTarget = domain.Zero, false
		} else {
			row.TargetAmount, row.HasTarget = target.Value.Round(), true
		}
	}
	if period := optOf(mask, "period", req.Period); period.Set {
		if period.Null || strings.TrimSpace(period.Value) == "" {
			return nil, errInvalid("missing", []string{"body", "period"}, "period is required")
		}
		stored, err := watchlistPeriod(period.Value)
		if err != nil {
			return nil, err
		}
		row.Period = stored
	}
	filterID := optOf(mask, "filter_id", req.FilterId)
	if filterID.Set && mask["items"] {
		return nil, errInvalid("invalid", []string{"body", "filter_id"},
			"send filter_id or items, not both")
	}
	if filterID.Set {
		if filterID.Null {
			return nil, errInvalid("missing", []string{"body", "filter_id"}, "filter_id is required")
		}
		id, err := uuidFrom(filterID.Value, "body", "filter_id")
		if err != nil {
			return nil, err
		}
		stored, err := requireFilter(ctx, env, sp, id)
		if err != nil {
			return nil, err
		}
		row.FilterID = stored.ID
	}
	if mask["items"] {
		writes, err := filterItemWrites(req.GetItems(), "items")
		if err != nil {
			return nil, err
		}
		filterID, err := rewriteWatchlistSelection(ctx, env, sp, row.Watchlist, writes)
		if err != nil {
			return nil, err
		}
		row.FilterID = filterID
	}
	if err := env.DB.UpdateWatchlist(ctx, sp.ID(), row.Watchlist); err != nil {
		return nil, notFoundAs(err, "Watchlist")
	}
	summaries, err := summarizeWatchlists(ctx, env, sp, []watchlistRow{row})
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateWatchlistResponse{Watchlist: summaries[0]}, nil
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

// DeleteWatchlist soft-deletes the watchlist and the filter only it uses
// (store.DeleteWatchlist).
func (s watchlistService) DeleteWatchlist(
	ctx context.Context, req *agentifiv1.DeleteWatchlistRequest,
) (*agentifiv1.DeleteWatchlistResponse, error) {
	sp := spaceFrom(ctx)
	row, err := watchlistByID(ctx, s.env, sp, req.GetWatchlistId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteWatchlist(ctx, sp.ID(), row.ID); err != nil {
		return nil, err
	}
	return &agentifiv1.DeleteWatchlistResponse{}, nil
}

// --- Summaries ---------------------------------------------------------------

func summarizeWatchlists(
	ctx context.Context, env *Env, sp auth.SpaceContext, rows []watchlistRow,
) ([]*agentifiv1.WatchlistSummary, error) {
	out := make([]*agentifiv1.WatchlistSummary, 0, len(rows))
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

func watchlistSummary(row watchlistRow, summary domain.WatchlistSummary, today domain.Date) *agentifiv1.WatchlistSummary {
	left, hasLeft := summary.LeftToTarget()
	pct, hasPct := summary.PctOfTarget()
	out := &agentifiv1.WatchlistSummary{
		Id:                    row.ID.String(),
		Name:                  row.Name,
		FilterId:              row.FilterID.String(),
		Emoji:                 dbconv.NullText(row.Emoji),
		Period:                row.Period,
		TargetAmount:          nullableMoneyProto(row.TargetAmount, row.HasTarget),
		LeftToTarget:          nullableMoneyProto(left, hasLeft),
		PctOfTarget:           rateProto(pct, hasPct),
		IsOverTarget:          summary.IsOverTarget(),
		IsProjectedOverTarget: summary.IsProjectedOverTarget(),
		ThisMonthSpent:        moneyProto(summary.ThisMonthSpent),
		MonthProjection:       moneyProto(summary.MonthProjection),
		YearToDate:            moneyProto(summary.YearToDate),
		MonthlyTrend:          make([]*agentifiv1.WatchlistTrendEntry, 0, len(summary.MonthlyTrend)),
		AsOf:                  today.String(),
	}
	for _, entry := range summary.MonthlyTrend {
		out.MonthlyTrend = append(out.MonthlyTrend, &agentifiv1.WatchlistTrendEntry{
			Month:     entry.Month.String(),
			Spent:     moneyProto(entry.Spent),
			IsPartial: entry.IsPartial,
		})
	}
	return out
}

func breakdownEntries(
	rows []domain.BreakdownRow, names breakdownNames, dimension domain.BreakdownDimension,
) []*agentifiv1.WatchlistBreakdownEntry {
	out := make([]*agentifiv1.WatchlistBreakdownEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, &agentifiv1.WatchlistBreakdownEntry{
			Key:   row.Key,
			Label: names.label(dimension, row.Key),
			Spent: moneyProto(row.Spent),
			Share: rateProto(row.Share, row.HasShare),
		})
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

func watchlistByID(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (watchlistRow, error) {
	id, err := idFrom(rawID, "Watchlist")
	if err != nil {
		return watchlistRow{}, err
	}
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
	ctx context.Context, env *Env, sp auth.SpaceContext, req *agentifiv1.CreateWatchlistRequest,
) (store.FilterItem, error) {
	switch {
	case len(req.GetCategoryIds()) > 0:
		categoryIDs, err := uuidsFrom(req.GetCategoryIds(), "body", "category_ids")
		if err != nil {
			return store.FilterItem{}, err
		}
		for _, id := range categoryIDs {
			if err := checkCategory(ctx, env, sp, id); err != nil {
				return store.FilterItem{}, err
			}
		}
		return store.FilterItem{
			Field:    string(domain.FieldCategory),
			Operator: string(domain.OpIn),
			ValueIDs: categoryIDs,
		}, nil

	case len(req.GetTagIds()) > 0:
		sent, err := uuidsFrom(req.GetTagIds(), "body", "tag_ids")
		if err != nil {
			return store.FilterItem{}, err
		}
		tagIDs, err := resolveTags(ctx, env, sp, sent)
		if err != nil {
			return store.FilterItem{}, err
		}
		return store.FilterItem{
			Field:    string(domain.FieldTag),
			Operator: string(domain.OpIn),
			ValueIDs: tagIDs,
		}, nil
	}

	names := make([]string, 0, len(req.GetPayeeNames()))
	seen := map[string]bool{}
	for _, name := range req.GetPayeeNames() {
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
