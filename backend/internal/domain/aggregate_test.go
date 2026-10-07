package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// One test per rule in aggregate.go's header: each is a wrong number, not a
// crash.

var (
	aggFood      = Category{ID: "cat-food", Name: "Food & Dining", Kind: CategoryExpense}
	aggGroceries = Category{ID: "cat-groceries", Name: "Groceries", Kind: CategoryExpense, ParentID: "cat-food"}
	aggHousehold = Category{ID: "cat-household", Name: "Household", Kind: CategoryExpense}
	aggSalary    = Category{ID: "cat-salary", Name: "Salary", Kind: CategoryIncome}
	aggPrivate   = Category{
		ID: "cat-private", Name: "Private", Kind: CategoryExpense, ExcludedFromReports: true,
	}
)

func aggCategories() map[ID]Category {
	return map[ID]Category{
		aggFood.ID:      aggFood,
		aggGroceries.ID: aggGroceries,
		aggHousehold.ID: aggHousehold,
		aggSalary.ID:    aggSalary,
		aggPrivate.ID:   aggPrivate,
	}
}

func aggAccount() Account {
	return Account{ID: "acct-1", Name: "Everyday Checking", Kind: KindCash, Currency: "USD"}
}

// aggPosting is one ordinary synced expense, ready to be bent per test.
func aggPosting(id ID, amount string, category Category) Posting {
	txn := Transaction{
		ID:            id,
		AccountID:     "acct-1",
		Date:          NewDate(2026, time.August, 15),
		Amount:        MustFromString(amount),
		StatementName: "SAFEWAY #1234",
		Payee:         "Safeway",
		CategoryID:    category.ID,
		Source:        SourceSync,
		Currency:      "USD",
	}
	return Posting{Txn: txn, Account: aggAccount(), Category: category, HasCategory: category.ID != ""}
}

func aggSpending(groupBy AggregateGroupBy) AggregateOptions {
	return AggregateOptions{
		GroupBy:    groupBy,
		Direction:  AggregateSpending,
		Mode:       DateEffective,
		Categories: aggCategories(),
	}
}

func bucketTotals(buckets []AggregateBucket) map[string]string {
	out := map[string]string{}
	for _, bucket := range buckets {
		out[bucket.Label] = bucket.Total.String()
	}
	return out
}

func TestAggregateFilesEachSplitUnderItsOwnCategory(t *testing.T) {
	// A $200 receipt split $50 groceries / $150 household is $50 under Food &
	// Dining.
	posting := aggPosting("t1", "-200.00", Category{})
	posting.Txn.CategoryID = ""
	posting.HasCategory = false
	posting.Txn.Splits = []Split{
		{ID: "s1", Amount: MustFromString("-50.00"), CategoryID: aggGroceries.ID},
		{ID: "s2", Amount: MustFromString("-150.00"), CategoryID: aggHousehold.ID},
	}

	got := Aggregate([]Posting{posting}, aggSpending(AggregateByCategory))
	require.Equal(t, "-200.00", got.Total.String())
	require.Equal(t, 2, got.Count)
	require.Equal(t, map[string]string{
		"Food & Dining": "-50.00",
		"Household":     "-150.00",
	}, bucketTotals(got.Buckets))
}

func TestAggregateCountsOnlyTheSplitsAFilterKept(t *testing.T) {
	// Under a groceries filter only the $50 part matched, as in the
	// register's total and a report over the filter.
	posting := aggPosting("t1", "-200.00", Category{})
	posting.Txn.CategoryID = ""
	posting.HasCategory = false
	posting.Txn.Splits = []Split{
		{ID: "s1", Amount: MustFromString("-50.00"), CategoryID: aggGroceries.ID},
		{ID: "s2", Amount: MustFromString("-150.00"), CategoryID: aggHousehold.ID},
	}
	groceries := Filter{Items: []FilterItem{
		{Field: FieldCategory, Values: []string{string(aggGroceries.ID)}},
	}}

	opts := aggSpending(AggregateByCategory)
	opts.Partial = map[ID][]Match{
		posting.Txn.ID: PartialParts(groceries, posting, Facets{}, DateEffective),
	}
	got := Aggregate([]Posting{posting}, opts)
	require.Equal(t, "-50.00", got.Total.String())
	require.Equal(t, 1, got.Count)
	require.Equal(t, map[string]string{"Food & Dining": "-50.00"}, bucketTotals(got.Buckets))
}

func TestAggregateRollsASubcategoryUpIntoItsParent(t *testing.T) {
	got := Aggregate(
		[]Posting{aggPosting("t1", "-50.00", aggGroceries)},
		aggSpending(AggregateByCategory),
	)
	require.Equal(t, []AggregateBucket{
		{Key: "cat-food", Label: "Food & Dining", Total: MustFromString("-50.00")},
	}, got.Buckets)
}

// An invented three-level tree: Travel, its children Lodging and Transport,
// and their children.
var (
	drillTravel    = Category{ID: "cat-travel", Name: "Travel", Kind: CategoryExpense}
	drillLodging   = Category{ID: "cat-lodging", Name: "Lodging", Kind: CategoryExpense, ParentID: "cat-travel"}
	drillTransport = Category{ID: "cat-transport", Name: "Transport", Kind: CategoryExpense, ParentID: "cat-travel"}
	drillHostels   = Category{ID: "cat-hostels", Name: "Hostels", Kind: CategoryExpense, ParentID: "cat-lodging"}
	drillRail      = Category{ID: "cat-rail", Name: "Rail", Kind: CategoryExpense, ParentID: "cat-transport"}
	drillTaxi      = Category{ID: "cat-taxi", Name: "Taxi", Kind: CategoryExpense, ParentID: "cat-transport"}
	drillFerry     = Category{ID: "cat-ferry", Name: "Ferry", Kind: CategoryExpense, ParentID: "cat-transport"}
)

func drillSpending(under ID) AggregateOptions {
	options := aggSpending(AggregateByCategory)
	for _, category := range []Category{
		drillTravel, drillLodging, drillTransport, drillHostels, drillRail, drillTaxi, drillFerry,
	} {
		options.Categories[category.ID] = category
	}
	options.Under = under
	return options
}

// drillPostings is one row on every category of the tree, the parents' own
// rows included, and one outside it.
func drillPostings() []Posting {
	return []Posting{
		aggPosting("t-travel", "-12.00", drillTravel),
		aggPosting("t-lodging", "-80.00", drillLodging),
		aggPosting("t-hostels", "-40.00", drillHostels),
		aggPosting("t-transport", "-5.00", drillTransport),
		aggPosting("t-rail", "-26.00", drillRail),
		aggPosting("t-taxi", "-15.50", drillTaxi),
		aggPosting("t-ferry", "-9.50", drillFerry),
		aggPosting("t-household", "-30.00", aggHousehold),
	}
}

// filedOn is the postings filed on the given categories: what the register's
// category filter, which expands a category to its subtree, hands the chart.
func filedOn(postings []Posting, ids ...ID) []Posting {
	var out []Posting
	for _, posting := range postings {
		for _, id := range ids {
			if posting.Txn.CategoryID == id {
				out = append(out, posting)
			}
		}
	}
	return out
}

func requireLinesSumToTotal(t *testing.T, got AggregateResult) {
	t.Helper()
	lines := Sum(got.Buckets, func(bucket AggregateBucket) Money { return bucket.Total })
	require.Equal(t, got.Total.String(), lines.String())
}

func TestAggregateFilesEveryDescendantUnderItsTopLevelCategory(t *testing.T) {
	got := Aggregate(drillPostings(), drillSpending(""))
	// A grandchild rolls up two levels, not one: Hostels is Travel's spending.
	require.Equal(t, map[string]string{"Travel": "-188.00", "Household": "-30.00"}, bucketTotals(got.Buckets))
	requireLinesSumToTotal(t, got)
}

func TestAggregateDrilledIntoAParentListsEachChildAndTheParentsOwnRows(t *testing.T) {
	postings := filedOn(drillPostings(), "cat-travel", "cat-lodging", "cat-transport",
		"cat-hostels", "cat-rail", "cat-taxi", "cat-ferry")
	got := Aggregate(postings, drillSpending("cat-travel"))
	require.Equal(t, map[string]string{
		"Travel":    "-12.00",
		"Lodging":   "-120.00",
		"Transport": "-56.00",
	}, bucketTotals(got.Buckets))
	require.Equal(t, "-188.00", got.Total.String())
	requireLinesSumToTotal(t, got)
}

func TestAggregateDrilledIntoAChildListsEachGrandchild(t *testing.T) {
	postings := filedOn(drillPostings(), "cat-transport", "cat-rail", "cat-taxi", "cat-ferry")
	got := Aggregate(postings, drillSpending("cat-transport"))
	require.Equal(t, map[string]string{
		"Transport": "-5.00",
		"Rail":      "-26.00",
		"Taxi":      "-15.50",
		"Ferry":     "-9.50",
	}, bucketTotals(got.Buckets))
	require.Equal(t, "-56.00", got.Total.String())
	requireLinesSumToTotal(t, got)
}

func TestAggregateDrilledIntoALeafIsOneLine(t *testing.T) {
	got := Aggregate(filedOn(drillPostings(), "cat-rail"), drillSpending("cat-rail"))
	require.Equal(t, []AggregateBucket{
		{Key: "cat-rail", Label: "Rail", Total: MustFromString("-26.00")},
	}, got.Buckets)
}

func TestCategoryDrillLevelSurvivesAParentCycle(t *testing.T) {
	loopA := Category{ID: "loop-a", Name: "A", ParentID: "loop-b"}
	loopB := Category{ID: "loop-b", Name: "B", ParentID: "loop-a"}
	categories := map[ID]Category{loopA.ID: loopA, loopB.ID: loopB}
	require.Equal(t, ID("loop-b"), CategoryDrillLevel(loopA, "", categories).ID)
}

func TestAggregateDropsRowsThatAreExcludedFromReports(t *testing.T) {
	// Exclusion at each of the levels ground rule 4 lists.
	cases := []struct {
		what string
		bend func(*Posting)
	}{
		{"the transaction's own flag", func(p *Posting) { p.Txn.ExcludedFromReports = true }},
		{"the account's flag", func(p *Posting) { p.Account.ExcludedFromReports = true }},
		{"the category's flag", func(p *Posting) {
			p.Txn.CategoryID = aggPrivate.ID
			p.Category = aggPrivate
		}},
		{"a matched transfer leg", func(p *Posting) { p.Txn.TransferPairID = "pair-1" }},
		{"a forecast", func(p *Posting) { p.Txn.IsEstimate = true }},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			posting := aggPosting("t1", "-50.00", aggGroceries)
			tc.bend(&posting)
			got := Aggregate([]Posting{posting}, aggSpending(AggregateByCategory))
			require.Equal(t, "0.00", got.Total.String())
			require.Zero(t, got.Count)
			require.Empty(t, got.Buckets)
		})
	}
}

func TestAggregateDropsASplitFiledUnderAnExcludedCategory(t *testing.T) {
	// A split row's parent carries no category, so the category exclusion is
	// asked per allocation.
	posting := aggPosting("t1", "-200.00", Category{})
	posting.Txn.CategoryID = ""
	posting.HasCategory = false
	posting.Txn.Splits = []Split{
		{ID: "s1", Amount: MustFromString("-50.00"), CategoryID: aggGroceries.ID},
		{ID: "s2", Amount: MustFromString("-150.00"), CategoryID: aggPrivate.ID},
	}

	got := Aggregate([]Posting{posting}, aggSpending(AggregateByCategory))
	require.Equal(t, "-50.00", got.Total.String())
	require.Equal(t, 1, got.Count)
}

func TestAggregateIgnoresOpeningBalancesAndBalanceAdjustments(t *testing.T) {
	// Opening balances and revaluations move net worth, not spending.
	for _, source := range []Source{SourceOpeningBalance, SourceBalanceAdjustment} {
		t.Run(string(source), func(t *testing.T) {
			posting := aggPosting("t1", "-10900.00", aggHousehold)
			posting.Txn.Source = source
			got := Aggregate([]Posting{posting}, aggSpending(AggregateByCategory))
			require.Equal(t, "0.00", got.Total.String())
			require.Empty(t, got.Buckets)
		})
	}
}

func TestAggregateFilesAMonthByTheEffectiveDate(t *testing.T) {
	// A charge posted 25 August on a statement due 10 September is
	// September's spending.
	posting := aggPosting("t1", "-75.00", aggHousehold)
	posting.Txn.Date = NewDate(2026, time.August, 25)
	posting.Txn.EffectiveDate = NewDate(2026, time.September, 10)

	effective := Aggregate([]Posting{posting}, aggSpending(AggregateByCategory))
	require.Equal(t, []string{"2026-09"}, monthKeys(effective))

	posted := aggSpending(AggregateByCategory)
	posted.Mode = DatePosted
	require.Equal(t, []string{"2026-08"}, monthKeys(Aggregate([]Posting{posting}, posted)))
}

func TestAggregateCountsATaggedAllocationInFullUnderEveryTag(t *testing.T) {
	// Each tag gets the full amount, so bucket totals may exceed the grand
	// total, which is summed from allocations.
	posting := aggPosting("t1", "-60.00", aggGroceries)
	posting.Txn.TagIDs = []ID{"tag-work", "tag-travel"}

	options := aggSpending(AggregateByTag)
	options.Tags = map[ID]string{"tag-work": "Reimbursable", "tag-travel": "Travel"}

	got := Aggregate([]Posting{posting}, options)
	require.Equal(t, "-60.00", got.Total.String())
	require.Equal(t, map[string]string{
		"Reimbursable": "-60.00",
		"Travel":       "-60.00",
	}, bucketTotals(got.Buckets))
}

func TestAggregateGivesASplitTheTagsItsParentCarries(t *testing.T) {
	posting := aggPosting("t1", "-100.00", Category{})
	posting.Txn.CategoryID = ""
	posting.HasCategory = false
	posting.Txn.TagIDs = []ID{"tag-work"}
	posting.Txn.Splits = []Split{
		{ID: "s1", Amount: MustFromString("-40.00"), CategoryID: aggGroceries.ID, TagIDs: []ID{"tag-travel"}},
		{ID: "s2", Amount: MustFromString("-60.00"), CategoryID: aggHousehold.ID},
	}

	options := aggSpending(AggregateByTag)
	options.Tags = map[ID]string{"tag-work": "Reimbursable", "tag-travel": "Travel"}

	got := Aggregate([]Posting{posting}, options)
	require.Equal(t, map[string]string{
		"Reimbursable": "-100.00",
		"Travel":       "-40.00",
	}, bucketTotals(got.Buckets))
}

func TestAggregateSelectsOneSideOfTheLedger(t *testing.T) {
	postings := []Posting{
		aggPosting("t1", "-50.00", aggGroceries),
		aggPosting("t2", "3000.00", aggSalary),
		aggPosting("t3", "0.00", aggHousehold),
	}

	spending := Aggregate(postings, aggSpending(AggregateByNone))
	require.Equal(t, "-50.00", spending.Total.String())

	income := aggSpending(AggregateByNone)
	income.Direction = AggregateIncome
	require.Equal(t, "3000.00", Aggregate(postings, income).Total.String())
}

func TestAggregateBreaksTheWindowDownByMonth(t *testing.T) {
	july := aggPosting("t1", "-100.00", aggGroceries)
	july.Txn.Date = NewDate(2026, time.July, 15)
	august := aggPosting("t2", "-25.00", aggHousehold)

	got := Aggregate([]Posting{august, july}, aggSpending(AggregateByCategory))
	// Ascending by month whatever order the rows arrived in.
	require.Equal(t, []string{"2026-07", "2026-08"}, monthKeys(got))
	require.Equal(t, map[string]string{"Food & Dining": "-100.00"}, bucketTotals(got.Months[0].Buckets))
	require.Equal(t, map[string]string{"Household": "-25.00"}, bucketTotals(got.Months[1].Buckets))
}

func TestAggregateRanksTheBiggestBucketFirst(t *testing.T) {
	got := Aggregate([]Posting{
		aggPosting("t1", "-25.00", aggGroceries),
		aggPosting("t2", "-400.00", aggHousehold),
	}, aggSpending(AggregateByCategory))
	require.Equal(t, []string{"Household", "Food & Dining"},
		[]string{got.Buckets[0].Label, got.Buckets[1].Label})
}

func TestAggregateNamesTheAbsences(t *testing.T) {
	// Rows with no category get a real bucket, not a dash.
	posting := aggPosting("t1", "-25.00", Category{})
	posting.Txn.CategoryID = ""
	posting.HasCategory = false
	posting.Txn.Payee = ""
	posting.Txn.StatementName = ""

	byCategory := Aggregate([]Posting{posting}, aggSpending(AggregateByCategory))
	require.Equal(t, "Uncategorized", byCategory.Buckets[0].Label)

	byPayee := Aggregate([]Posting{posting}, aggSpending(AggregateByPayee))
	require.Equal(t, "No payee", byPayee.Buckets[0].Label)

	byTag := Aggregate([]Posting{posting}, aggSpending(AggregateByTag))
	require.Equal(t, "No tag", byTag.Buckets[0].Label)
}

func monthKeys(result AggregateResult) []string {
	out := make([]string, 0, len(result.Months))
	for _, month := range result.Months {
		out = append(out, month.Month)
	}
	return out
}
