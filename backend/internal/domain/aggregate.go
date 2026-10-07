package domain

import (
	"sort"
	"strings"
)

// The register's Spending and Income tabs. Not the report engine (that is a
// kind/parent/category walk with no account filter), but it shares the same
// predicates so the two cannot disagree about what counts. Rules that are each
// a wrong number if missed:
//
//   - Per allocation, not per row: a split contributes each part to its own
//     category.
//   - CountsAsIncomeOrExpense is asked again per split, because a split row's
//     parent carries no category.
//   - Opening balances and balance adjustments do not count: net worth
//     moves, nobody spent anything.
//   - The reporting date is the effective date; a card charge's posted date
//     can be a statement cycle away.
//   - A tagged allocation lands in every tag it carries, in full, so bucket
//     totals can exceed the grand total, which is summed from allocations.

// AggregateGroupBy is the dimension the ranking is built over.
type AggregateGroupBy string

const (
	AggregateByCategory AggregateGroupBy = "category"
	AggregateByPayee    AggregateGroupBy = "payee"
	AggregateByTag      AggregateGroupBy = "tag"
	AggregateByNone     AggregateGroupBy = "none"
)

// AggregateDirection is which side of the ledger the tab shows. The side is
// LedgerKind's answer, not the sign's: a grocery refund reduces Spending
// rather than adding to Income. A zero allocation is neither side.
type AggregateDirection string

const (
	AggregateSpending AggregateDirection = "spending"
	AggregateIncome   AggregateDirection = "income"
)

// AggregateOptions is everything the grouping needs that a posting does not
// carry: the dimension, the side, the date field, and the names.
type AggregateOptions struct {
	GroupBy   AggregateGroupBy
	Direction AggregateDirection
	Mode      DateMode
	// Categories resolves a split's own category (the posting carries only
	// the parent's) and the parent a subcategory rolls up into.
	Categories map[ID]Category
	Tags       map[ID]string
	// Under is the category the chart is drilled into, empty at the top. See
	// CategoryDrillLevel.
	Under ID
	// Partial is PartialParts by row id: a split row the filter kept only
	// part of contributes just those parts, matching the register total and
	// reports. A row absent from it counts every allocation.
	Partial map[ID][]Match
}

type AggregateBucket struct {
	Key   string
	Label string
	Total Money
}

// AggregateMonth is one month of the over-time chart. Only non-empty buckets
// are listed.
type AggregateMonth struct {
	Month   string
	Buckets []AggregateBucket
}

type AggregateResult struct {
	// Total is summed from the allocations, never from Buckets (tags
	// double-count).
	Total Money
	// Count is how many allocations contributed, not how many rows.
	Count   int
	Buckets []AggregateBucket
	Months  []AggregateMonth
}

// Aggregate ranks the match set and breaks it down by month.
func Aggregate(postings []Posting, opts AggregateOptions) AggregateResult {
	tally := newAggregateTally()
	byMonth := map[string]*aggregateTally{}
	var months []string
	var amounts []Money
	count := 0

	for _, posting := range postings {
		if !CountsAsIncomeOrExpense(posting, false) {
			continue
		}
		month := MonthOf(ReportingDate(posting.Txn, opts.Mode)).String()

		matches, partial := opts.Partial[posting.Txn.ID]
		if !partial {
			matches = Allocations(posting)
		}
		for _, match := range matches {
			// NewPart is the one place a split joins its own category, so this
			// and the spending plan cannot file a split differently.
			part := NewPart(posting, match, opts.Categories)
			// Asked again per allocation: the check above read the parent,
			// whose category a split row does not have.
			if part.HasCategory && !part.Category.CountsAsIncomeOrExpense() {
				continue
			}
			if !opts.selects(part) {
				continue
			}

			amounts = append(amounts, part.Amount)
			count++

			within, seen := byMonth[month]
			if !seen {
				within = newAggregateTally()
				byMonth[month] = within
				months = append(months, month)
			}
			for _, key := range opts.keysFor(match) {
				tally.add(key, part.Amount)
				within.add(key, part.Amount)
			}
		}
	}

	sort.Strings(months)
	out := AggregateResult{
		Total:   Sum(amounts, func(m Money) Money { return m }),
		Count:   count,
		Buckets: tally.ranked(),
		Months:  make([]AggregateMonth, 0, len(months)),
	}
	for _, month := range months {
		out.Months = append(out.Months, AggregateMonth{
			Month:   month,
			Buckets: byMonth[month].ranked(),
		})
	}
	return out
}

// selects is which side of the ledger one allocation counts on. LedgerKind,
// not the sign: a positive under a spending category is a refund and reduces
// the Spending total. A zero allocation belongs to neither side.
func (o AggregateOptions) selects(part Part) bool {
	if part.Amount.IsZero() {
		return false
	}
	if o.Direction == AggregateIncome {
		return part.IsEarnings()
	}
	return part.LedgerKind() == CategoryExpense
}

// keysFor is where one allocation files. More than one key only for tags.
func (o AggregateOptions) keysFor(part Match) []AggregateBucket {
	switch o.GroupBy {
	case AggregateByNone:
		return []AggregateBucket{{Key: "all", Label: "All categories"}}
	case AggregateByPayee:
		name := part.Payee()
		if name == "" {
			return []AggregateBucket{{Key: "", Label: "No payee"}}
		}
		// Keyed on the folded name, the same key the report engine's payee
		// dimension uses.
		return []AggregateBucket{{Key: strings.ToLower(name), Label: name}}
	case AggregateByTag:
		ids := part.TagIDs()
		if len(ids) == 0 {
			return []AggregateBucket{{Key: "", Label: "No tag"}}
		}
		out := make([]AggregateBucket, 0, len(ids))
		for _, id := range ids {
			name, known := o.Tags[id]
			if !known {
				name = "Unknown tag"
			}
			out = append(out, AggregateBucket{Key: string(id), Label: name})
		}
		return out
	default:
		id := part.CategoryID()
		if id == "" {
			return []AggregateBucket{{Key: "uncategorized", Label: "Uncategorized"}}
		}
		category, known := o.Categories[id]
		if !known {
			return []AggregateBucket{{Key: string(id), Label: "Unknown category"}}
		}
		category = CategoryDrillLevel(category, o.Under, o.Categories)
		return []AggregateBucket{{Key: string(category.ID), Label: category.Name}}
	}
}

// CategoryDrillLevel is the category an allocation files under on a chart
// drilled into `under`, however deep the tree runs:
//
//   - at the top (under empty), the top-level category it descends from;
//   - under a category, the child of `under` it descends from, or `under`
//     itself for a row filed on it directly — the parent's own line;
//   - outside under's subtree, the top-level category again.
//
// A grandchild therefore files under its child at every level, never beside
// it, and the lines at one level sum to the level above's line.
func CategoryDrillLevel(category Category, under ID, categories map[ID]Category) Category {
	// From the category up to its root. The guard stops a parent cycle in
	// stored data from looping forever.
	path := []Category{category}
	seen := map[ID]bool{category.ID: true}
	for {
		parent, known := categories[path[len(path)-1].ParentID]
		if !known || seen[parent.ID] {
			break
		}
		seen[parent.ID] = true
		path = append(path, parent)
	}
	if under != "" {
		for i, step := range path {
			if step.ID != under {
				continue
			}
			if i == 0 {
				return step
			}
			return path[i-1]
		}
	}
	return path[len(path)-1]
}

// aggregateTally is one level's running totals, keeping the label each key was
// first seen with.
type aggregateTally struct {
	totals map[string]Money
	labels map[string]string
}

func newAggregateTally() *aggregateTally {
	return &aggregateTally{totals: map[string]Money{}, labels: map[string]string{}}
}

func (t *aggregateTally) add(bucket AggregateBucket, amount Money) {
	if _, seen := t.labels[bucket.Key]; !seen {
		t.labels[bucket.Key] = bucket.Label
	}
	t.totals[bucket.Key] = Total(t.totals[bucket.Key], amount)
}

// ranked is largest magnitude first; ties break on the label so the order
// does not depend on map iteration.
func (t *aggregateTally) ranked() []AggregateBucket {
	out := make([]AggregateBucket, 0, len(t.totals))
	for key, total := range t.totals {
		out = append(out, AggregateBucket{Key: key, Label: t.labels[key], Total: total})
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].Total.Abs(), out[j].Total.Abs()
		if !left.Equal(right) {
			return left.GreaterThan(right)
		}
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].Key < out[j].Key
	})
	return out
}
