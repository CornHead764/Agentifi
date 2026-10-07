package domain

// Watchlists (calculations.md §7). Two traps: the trailing average uses full
// months only, since the partial current month drags it down; and spend is the
// net of the matching rows, so a refund gives money back rather than counting
// twice — the departure §13 records against §7.

import (
	"sort"

	"github.com/shopspring/decimal"
)

// Watchlist is a saved slice, holding the id of the filter that defines it.
// A missing target (HasTarget false) is not a target of zero, which every row
// would breach.
type Watchlist struct {
	ID       ID
	Name     string
	FilterID ID

	TargetAmount Money
	HasTarget    bool

	Emoji string
	// TrendMonths is how many bars the Overtime chart draws.
	TrendMonths int
}

// Part is one part of one posting: a split, or the whole row when it has
// none, with its own amount and its own category resolved. A split's share is
// not the row's amount and its category is not its parent's.
type Part struct {
	Posting  Posting
	Split    Split
	HasSplit bool
	Amount   Money

	// Category is the part's resolved category, as CategoryID reports it. Set
	// by NewPart; a part assembled by hand carries neither and is treated as
	// uncategorized, which counts.
	Category    Category
	HasCategory bool
}

type WatchlistPart = Part

// NewPart builds a part from one matched allocation, resolving its category
// here so CategoryID and Category can never name two different categories.
func NewPart(posting Posting, match Match, categories map[ID]Category) Part {
	part := Part{Posting: posting, Amount: match.Amount}
	if match.Split == nil {
		// Use the posting's resolved category: a lookup in a map the caller may
		// not have would read as uncategorized and send a store refund to Income.
		part.Category, part.HasCategory = posting.Category, posting.HasCategory
		return part
	}
	part.Split, part.HasSplit = *match.Split, true
	if id := part.CategoryID(); id != "" {
		part.Category, part.HasCategory = categories[id]
	}
	return part
}

// PartsOf is every part of one posting, categories resolved — the whole row
// when it has no splits, one part per split when it has.
func PartsOf(posting Posting, categories map[ID]Category) []Part {
	matches := Allocations(posting)
	out := make([]Part, 0, len(matches))
	for _, match := range matches {
		out = append(out, NewPart(posting, match, categories))
	}
	return out
}

func (p Part) Signed() Money { return p.Amount }

// Key identifies one part: the row, plus the split when there is one.
func (p Part) Key() ID {
	if p.HasSplit {
		return p.Posting.Txn.ID + ":" + p.Split.ID
	}
	return p.Posting.Txn.ID
}

// CategoryID is the split's category when there is one — never its parent's.
// A split with no category is uncategorized.
func (p Part) CategoryID() ID {
	if p.HasSplit {
		return p.Split.CategoryID
	}
	return p.Posting.Txn.CategoryID
}

// Payee is always the parent row's display payee; a split carries none.
func (p Part) Payee() string {
	return p.Posting.Txn.DisplayPayee()
}

// TagIDs is the row's tags plus the split's own.
func (p Part) TagIDs() []ID {
	seen := map[ID]bool{}
	var tags []ID
	add := func(ids []ID) {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				tags = append(tags, id)
			}
		}
	}
	add(p.Posting.Txn.TagIDs)
	if p.HasSplit {
		add(p.Split.TagIDs)
	}
	return tags
}

// WatchlistMatcher yields the allocations of a posting that a watchlist's
// filter selects, empty when the row does not match.
type WatchlistMatcher func(Watchlist, Posting, DateMode) []WatchlistPart

// WatchlistMatcherFor is the matcher every reader of a watchlist uses. Parts
// are built by NewPart so a split carries its category's exclusion flags. A
// watchlist whose filter is missing matches nothing.
func WatchlistMatcherFor(filters map[ID]Filter, facets map[ID]Facets, categories map[ID]Category) WatchlistMatcher {
	return func(watchlist Watchlist, posting Posting, mode DateMode) []WatchlistPart {
		filter, ok := filters[watchlist.FilterID]
		if !ok {
			return nil
		}
		matches := MatchingParts(filter, posting, facets[posting.Txn.ID], mode)
		parts := make([]WatchlistPart, 0, len(matches))
		for _, match := range matches {
			parts = append(parts, NewPart(posting, match, categories))
		}
		return parts
	}
}

type BreakdownDimension string

const (
	BreakdownByCategory BreakdownDimension = "category"
	BreakdownByPayee    BreakdownDimension = "payee"
	BreakdownByTag      BreakdownDimension = "tag"
)

// MonthSpend is one bar of the Overtime chart.
type MonthSpend struct {
	Month Month
	Spent Money
	// IsPartial marks the current month, which averages must leave out.
	IsPartial bool
}

// BreakdownRow is one slice of the breakdown donut. Key is empty for the
// uncategorized or untagged slice; HasShare is false when the window had no
// spend.
type BreakdownRow struct {
	Key      string
	Spent    Money
	Share    Rate
	HasShare bool
}

type WatchlistSummary struct {
	Watchlist          Watchlist
	ThisMonthSpent     Money
	MonthProjection    Money
	YearToDate         Money
	TwelveMonthAverage Money
	MonthlyTrend       []MonthSpend
}

// LeftToTarget is what remains of the target, negative once it is breached; ok
// is false when there is no target.
func (s WatchlistSummary) LeftToTarget() (Money, bool) {
	if !s.Watchlist.HasTarget {
		return Zero, false
	}
	return s.Watchlist.TargetAmount.Sub(s.ThisMonthSpent).Round(), true
}

// PctOfTarget is the share of the target used; ok is false when there is no
// target, or the target is zero and cannot be divided into.
func (s WatchlistSummary) PctOfTarget() (Rate, bool) {
	if !s.Watchlist.HasTarget {
		return decimal.Zero, false
	}
	return Percent(s.ThisMonthSpent, s.Watchlist.TargetAmount)
}

func (s WatchlistSummary) IsOverTarget() bool {
	left, ok := s.LeftToTarget()
	return ok && left.IsNegative()
}

func (s WatchlistSummary) IsProjectedOverTarget() bool {
	return s.Watchlist.HasTarget && s.MonthProjection.GreaterThan(s.Watchlist.TargetAmount)
}

// WatchlistSpentBetween is spend over a window, positive, net of refunds.
// Rows excluded from reports are excluded here too: Simplifi's Reports
// checkbox "affects Reports, Watchlists, and Upcoming Summary".
func WatchlistSpentBetween(watchlist Watchlist, postings []Posting, matches WatchlistMatcher, start, end Date, mode DateMode) Money {
	parts := watchlistParts(watchlist, postings, matches, start, end, mode)
	return Sum(parts, partAmount).Neg()
}

func partAmount(p WatchlistPart) Money { return p.Amount }

func WatchlistSpentInMonth(watchlist Watchlist, postings []Posting, matches WatchlistMatcher, month Month, mode DateMode) Money {
	return WatchlistSpentBetween(watchlist, postings, matches, month.FirstDay(), month.LastDay(), mode)
}

// WatchlistThisMonthSpent runs to the end of the month, not to today: a row
// dated later this month is already committed.
func WatchlistThisMonthSpent(watchlist Watchlist, postings []Posting, matches WatchlistMatcher, today Date, mode DateMode) Money {
	return WatchlistSpentInMonth(watchlist, postings, matches, MonthOf(today), mode)
}

// ProjectMonthSpend continues this month's run rate to month end. Days elapsed
// include today, so the divisor is never zero.
func ProjectMonthSpend(spent Money, today Date) Money {
	perDay, ok := spent.DivInt(DaysElapsedInMonth(today))
	if !ok {
		return spent
	}
	return perDay.Scale(decimal.NewFromInt(int64(MonthOf(today).Days()))).Round()
}

// WatchlistYearToDate runs January 1 to the end of today's month, on the
// calendar year.
func WatchlistYearToDate(watchlist Watchlist, postings []Posting, matches WatchlistMatcher, today Date, mode DateMode) Money {
	january := Date{Year: today.Year, Month: 1, Day: 1}
	return WatchlistSpentBetween(watchlist, postings, matches, january, MonthOf(today).LastDay(), mode)
}

// WatchlistMonthlyTrend is the Overtime bars, oldest first, ending with the
// current month flagged partial. A months of zero falls back to the
// watchlist's own setting.
func WatchlistMonthlyTrend(watchlist Watchlist, postings []Posting, matches WatchlistMatcher, today Date, months int, mode DateMode) []MonthSpend {
	if months <= 0 {
		months = watchlist.TrendMonths
	}
	current := MonthOf(today)
	trend := make([]MonthSpend, 0, months)
	for offset := months - 1; offset >= 0; offset-- {
		month := current.Shift(-offset)
		trend = append(trend, MonthSpend{
			Month:     month,
			Spent:     WatchlistSpentInMonth(watchlist, postings, matches, month, mode),
			IsPartial: month == current,
		})
	}
	return trend
}

// WatchlistTrailingAverage is average spend over the trailing full months, the
// current month excluded. Divides by the months in the window, not those with
// spending: a month with no matching rows is a month of zero spend.
func WatchlistTrailingAverage(watchlist Watchlist, postings []Posting, matches WatchlistMatcher, today Date, months int, mode DateMode) Money {
	if months <= 0 {
		return Zero
	}
	window := FullMonthsBefore(today, months)
	monthly := make([]Money, 0, len(window))
	for _, month := range window {
		monthly = append(monthly, WatchlistSpentInMonth(watchlist, postings, matches, month, mode))
	}
	mean, ok := Total(monthly...).DivInt(months)
	if !ok {
		return Zero
	}
	return mean.Round()
}

// WatchlistBreakdown is the selected window's spend split by category, payee or
// tag. Shares are of the window's total, so a two-tag row counts in full under
// each tag and the shares can pass 100% rather than inventing a division.
func WatchlistBreakdown(watchlist Watchlist, postings []Posting, matches WatchlistMatcher, start, end Date, dimension BreakdownDimension, mode DateMode) []BreakdownRow {
	parts := watchlistParts(watchlist, postings, matches, start, end, mode)
	sums := sumPartsBy(parts, dimension)
	spent := Sum(parts, partAmount).Neg()

	rows := make([]BreakdownRow, 0, len(sums))
	for key, amount := range sums {
		rowSpent := amount.Neg()
		share, hasShare := Ratio(rowSpent, spent)
		rows = append(rows, BreakdownRow{Key: key, Spent: rowSpent, Share: share, HasShare: hasShare})
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Spent.Equal(rows[j].Spent) {
			return rows[i].Spent.GreaterThan(rows[j].Spent)
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

func SummarizeWatchlist(watchlist Watchlist, postings []Posting, matches WatchlistMatcher, today Date, mode DateMode) WatchlistSummary {
	spent := WatchlistThisMonthSpent(watchlist, postings, matches, today, mode)
	return WatchlistSummary{
		Watchlist:          watchlist,
		ThisMonthSpent:     spent,
		MonthProjection:    ProjectMonthSpend(spent, today),
		YearToDate:         WatchlistYearToDate(watchlist, postings, matches, today, mode),
		TwelveMonthAverage: WatchlistTrailingAverage(watchlist, postings, matches, today, 12, mode),
		MonthlyTrend:       WatchlistMonthlyTrend(watchlist, postings, matches, today, 0, mode),
	}
}

// sumPartsBy groups matches by one dimension and sums them. A two-tag match
// lands in two groups in full, as Simplifi's per-tag donut shows, so group
// totals can exceed the filtered total.
func sumPartsBy(parts []WatchlistPart, dimension BreakdownDimension) map[string]Money {
	grouped := map[string][]Money{}
	for _, part := range parts {
		for _, key := range breakdownKeys(part, dimension) {
			grouped[key] = append(grouped[key], part.Amount)
		}
	}
	sums := make(map[string]Money, len(grouped))
	for key, amounts := range grouped {
		sums[key] = Total(amounts...)
	}
	return sums
}

func breakdownKeys(part WatchlistPart, dimension BreakdownDimension) []string {
	switch dimension {
	case BreakdownByPayee:
		return []string{part.Payee()}
	case BreakdownByTag:
		tags := part.TagIDs()
		if len(tags) == 0 {
			// An untagged row is its own slice, so the slices sum to the total.
			return []string{""}
		}
		keys := make([]string, 0, len(tags))
		for _, tag := range tags {
			keys = append(keys, string(tag))
		}
		return keys
	}
	return []string{string(part.CategoryID())}
}

func watchlistParts(watchlist Watchlist, postings []Posting, matches WatchlistMatcher, start, end Date, mode DateMode) []WatchlistPart {
	if matches == nil {
		return nil
	}
	var parts []WatchlistPart
	for _, posting := range postings {
		if !CountsAsIncomeOrExpense(posting, true) {
			continue
		}
		on := posting.Txn.ReportingDate(mode)
		if on.Before(start) || on.After(end) {
			continue
		}
		for _, part := range matches(watchlist, posting, mode) {
			// The category clause asked again per part: a split row's posting
			// carries no category (ground rule 4).
			if part.HasCategory && !part.Category.CountsAsIncomeOrExpense() {
				continue
			}
			parts = append(parts, part)
		}
	}
	return parts
}
