package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A $50 grocery charge and a $20 credit against it net to $30 of groceries,
// as Simplifi's own envelope shows for the same pair (calculations.md §13).

func refundCategory(kind CategoryKind, name string) Category {
	return Category{ID: ID("cat-" + name), Name: name, Kind: kind}
}

var (
	refundGroceries = refundCategory(CategoryExpense, "Groceries")
	refundSalary    = refundCategory(CategoryIncome, "Salary")
	refundMoving    = refundCategory(CategoryTransfer, "Transfer")
)

func refundPart(amount string, category Category, hasCategory bool) Part {
	txn := Transaction{ID: "txn-1", Amount: MustFromString(amount)}
	posting := Posting{Txn: txn, Account: predAccount(), Category: category, HasCategory: hasCategory}
	return Part{Posting: posting, Amount: txn.Amount, Category: category, HasCategory: hasCategory}
}

func TestLedgerKindReadsTheCategoryNotTheSign(t *testing.T) {
	cases := []struct {
		name   string
		amount string
		cat    Category
		has    bool
		want   CategoryKind
	}{
		{"a grocery charge is spending", "-50.00", refundGroceries, true, CategoryExpense},
		{"a grocery refund is still spending", "20.00", refundGroceries, true, CategoryExpense},
		{"a paycheck is income", "2500.00", refundSalary, true, CategoryIncome},
		{"a clawed-back paycheck is still income", "-2500.00", refundSalary, true, CategoryIncome},
		{"a transfer is neither", "500.00", refundMoving, true, CategoryTransfer},
		{"an unfiled charge is spending", "-50.00", Category{}, false, CategoryExpense},
		{"an unfiled credit is spending too", "50.00", Category{}, false, CategoryExpense},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			require.Equal(t, one.want, refundPart(one.amount, one.cat, one.has).LedgerKind())
		})
	}
}

// The whole point of the rule: a store return is not earnings.
func TestARefundIsNotEarnings(t *testing.T) {
	refund := refundPart("20.00", refundGroceries, true)
	require.True(t, refund.IsRefund())
	require.False(t, refund.IsEarnings())

	paycheck := refundPart("2500.00", refundSalary, true)
	require.False(t, paycheck.IsRefund())
	require.True(t, paycheck.IsEarnings())

	// Income is what somebody filed as income. An unfiled credit nets against
	// spending until it is filed or linked to the charge it gives back.
	unfiled := refundPart("20.00", Category{}, false)
	require.True(t, unfiled.IsRefund())
	require.False(t, unfiled.IsEarnings())
}

// --- The plan --------------------------------------------------------------

// refundMonth is one month holding whatever postings a test hands it.
func refundMonth(postings ...Posting) MonthInputs {
	return MonthInputs{
		Month:    NewMonth(2026, time.August),
		Postings: postings,
		Categories: map[ID]Category{
			refundGroceries.ID: refundGroceries,
			refundSalary.ID:    refundSalary,
		},
	}
}

func refundPosting(id, amount string, day int, category Category, hasCategory bool) Posting {
	txn := Transaction{
		ID:         ID(id),
		AccountID:  "acct-1",
		Date:       NewDate(2026, time.August, day),
		Amount:     MustFromString(amount),
		Payee:      "Fresh Market",
		Source:     SourceSync,
		Currency:   "USD",
		CategoryID: category.ID,
	}
	if !hasCategory {
		txn.CategoryID = ""
	}
	return Posting{Txn: txn, Account: predAccount(), Category: category, HasCategory: hasCategory}
}

// $50 spent, $20 back: groceries cost $30 and nobody earned anything.
func TestAGroceryRefundNetsAgainstSpendRatherThanCountingAsIncome(t *testing.T) {
	month := ComputeMonth(refundMonth(
		refundPosting("charge", "-50.00", 4, refundGroceries, true),
		refundPosting("credit", "20.00", 9, refundGroceries, true),
	), Zero, nil)

	require.Equal(t, "0.00", month.Bucket(BucketIncome).CalculatedAmount.String())
	require.Equal(t, "-30.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
	require.ElementsMatch(t, []ID{"charge", "credit"},
		month.Bucket(BucketOtherSpend).ContributingTxnIDs)
}

// Counted by its sign the pair reports $20 of income and $50 of spending.
func TestARefundDoesNotInflateBothBuckets(t *testing.T) {
	month := ComputeMonth(refundMonth(
		refundPosting("charge", "-50.00", 4, refundGroceries, true),
		refundPosting("credit", "20.00", 9, refundGroceries, true),
	), Zero, nil)

	require.Equal(t, "-30.00", month.LeftThisMonth().String())
	require.NotEqual(t, "20.00", month.Bucket(BucketIncome).CalculatedAmount.String())
}

func TestAPaycheckIsStillIncome(t *testing.T) {
	month := ComputeMonth(refundMonth(
		refundPosting("pay", "2500.00", 1, refundSalary, true),
		refundPosting("charge", "-50.00", 4, refundGroceries, true),
	), Zero, nil)

	require.Equal(t, "2500.00", month.Bucket(BucketIncome).CalculatedAmount.String())
	require.Equal(t, "-50.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
}

// --- Links -----------------------------------------------------------------

func TestALinkedRefundFilesAnUnfiledCreditUnderTheChargeCategory(t *testing.T) {
	// The credit the bank filed under nothing. Unfiled, it already nets against
	// Other Spend rather than counting as income; the link is what files it
	// under the charge's category.
	charge := refundPosting("charge", "-50.00", 4, refundGroceries, true)
	credit := refundPosting("credit", "20.00", 9, Category{}, false)
	categories := map[ID]Category{refundGroceries.ID: refundGroceries}
	links := []RefundLink{{
		RefundTxnID: "credit", ChargeTxnID: "charge", ChargeCategoryID: refundGroceries.ID,
	}}

	unlinked := ComputeMonth(refundMonth(charge, credit), Zero, nil)
	require.Equal(t, "0.00", unlinked.Bucket(BucketIncome).CalculatedAmount.String())
	require.Equal(t, "-30.00", unlinked.Bucket(BucketOtherSpend).CalculatedAmount.String())

	refiled := ApplyRefundLinks([]Posting{charge, credit}, links, categories)
	linked := ComputeMonth(refundMonth(refiled...), Zero, nil)
	require.Equal(t, "0.00", linked.Bucket(BucketIncome).CalculatedAmount.String())
	require.Equal(t, "-30.00", linked.Bucket(BucketOtherSpend).CalculatedAmount.String())
	require.Equal(t, unlinked.LeftThisMonth().String(), linked.LeftThisMonth().String())
	require.Contains(t, linked.OtherSpendParts, SpendPart{
		TxnID: "credit", CategoryID: refundGroceries.ID, HasCategory: true,
		Amount: MustFromString("20.00"),
	})
}

// Reports read the same refiled postings, so the category the money went back to
// is the one whose total falls.
func TestALinkedRefundReducesTheChargeCategoryInReports(t *testing.T) {
	charge := refundPosting("charge", "-50.00", 4, refundGroceries, true)
	credit := refundPosting("credit", "20.00", 9, Category{}, false)
	categories := map[ID]Category{refundGroceries.ID: refundGroceries}
	refiled := ApplyRefundLinks([]Posting{charge, credit}, []RefundLink{{
		RefundTxnID: "credit", ChargeTxnID: "charge", ChargeCategoryID: refundGroceries.ID,
	}}, categories)

	opts := AggregateOptions{
		GroupBy:    AggregateByCategory,
		Direction:  AggregateSpending,
		Categories: categories,
	}
	spending := Aggregate(refiled, opts)
	require.Equal(t, "-30.00", spending.Total.String())
	require.Len(t, spending.Buckets, 1)
	require.Equal(t, "Groceries", spending.Buckets[0].Label)
	require.Equal(t, "-30.00", spending.Buckets[0].Total.String())

	opts.Direction = AggregateIncome
	require.Equal(t, "0.00", Aggregate(refiled, opts).Total.String())
}

// An unlinked refund with its own spending category nets in reports as it
// does in the plan.
func TestReportsFileARefundOnTheSpendingSide(t *testing.T) {
	postings := []Posting{
		refundPosting("charge", "-50.00", 4, refundGroceries, true),
		refundPosting("credit", "20.00", 9, refundGroceries, true),
		refundPosting("pay", "2500.00", 1, refundSalary, true),
	}
	categories := map[ID]Category{refundGroceries.ID: refundGroceries, refundSalary.ID: refundSalary}

	spending := Aggregate(postings, AggregateOptions{
		GroupBy: AggregateByCategory, Direction: AggregateSpending, Categories: categories,
	})
	require.Equal(t, "-30.00", spending.Total.String())

	income := Aggregate(postings, AggregateOptions{
		GroupBy: AggregateByCategory, Direction: AggregateIncome, Categories: categories,
	})
	require.Equal(t, "2500.00", income.Total.String())
}

func TestApplyRefundLinksLeavesTheGuessesAlone(t *testing.T) {
	dining := refundCategory(CategoryExpense, "Dining")
	categories := map[ID]Category{refundGroceries.ID: refundGroceries, dining.ID: dining}

	t.Run("two charges disagreeing on a category refile nothing", func(t *testing.T) {
		credit := refundPosting("credit", "20.00", 9, Category{}, false)
		out := ApplyRefundLinks([]Posting{credit}, []RefundLink{
			{RefundTxnID: "credit", ChargeTxnID: "a", ChargeCategoryID: refundGroceries.ID},
			{RefundTxnID: "credit", ChargeTxnID: "b", ChargeCategoryID: dining.ID},
		}, categories)
		require.False(t, out[0].HasCategory)
	})

	t.Run("two charges agreeing on a category do refile", func(t *testing.T) {
		credit := refundPosting("credit", "20.00", 9, Category{}, false)
		out := ApplyRefundLinks([]Posting{credit}, []RefundLink{
			{RefundTxnID: "credit", ChargeTxnID: "a", ChargeCategoryID: refundGroceries.ID},
			{RefundTxnID: "credit", ChargeTxnID: "b", ChargeCategoryID: refundGroceries.ID},
		}, categories)
		require.True(t, out[0].HasCategory)
		require.Equal(t, refundGroceries.ID, out[0].Category.ID)
	})

	t.Run("a split credit keeps its splits", func(t *testing.T) {
		credit := refundPosting("credit", "20.00", 9, Category{}, false)
		credit.Txn.Splits = []Split{
			{ID: "s1", Amount: MustFromString("12.00"), CategoryID: dining.ID},
			{ID: "s2", Amount: MustFromString("8.00"), CategoryID: refundGroceries.ID},
		}
		out := ApplyRefundLinks([]Posting{credit}, []RefundLink{
			{RefundTxnID: "credit", ChargeTxnID: "a", ChargeCategoryID: refundGroceries.ID},
		}, categories)
		require.False(t, out[0].HasCategory)
		require.Equal(t, ID(""), out[0].Txn.CategoryID)
	})

	t.Run("an uncategorized charge refiles nothing", func(t *testing.T) {
		credit := refundPosting("credit", "20.00", 9, Category{}, false)
		out := ApplyRefundLinks([]Posting{credit}, []RefundLink{
			{RefundTxnID: "credit", ChargeTxnID: "a"},
		}, categories)
		require.False(t, out[0].HasCategory)
	})

	t.Run("the postings handed in are not mutated", func(t *testing.T) {
		credit := refundPosting("credit", "20.00", 9, Category{}, false)
		in := []Posting{credit}
		ApplyRefundLinks(in, []RefundLink{
			{RefundTxnID: "credit", ChargeTxnID: "a", ChargeCategoryID: refundGroceries.ID},
		}, categories)
		require.False(t, in[0].HasCategory)
	})
}

// --- Where the affordance is offered ---------------------------------------

func TestCanBeARefundRefusesWhereARefundMeansNothing(t *testing.T) {
	credit := refundPosting("credit", "20.00", 9, refundGroceries, true)
	require.True(t, CanBeARefund(credit))

	t.Run("a charge is not a refund", func(t *testing.T) {
		require.False(t, CanBeARefund(refundPosting("c", "-20.00", 9, refundGroceries, true)))
	})

	t.Run("income is earnings the user already named", func(t *testing.T) {
		require.False(t, CanBeARefund(refundPosting("c", "20.00", 9, refundSalary, true)))
	})

	t.Run("a transfer category is money moving inside the household", func(t *testing.T) {
		require.False(t, CanBeARefund(refundPosting("c", "20.00", 9, refundMoving, true)))
	})

	// A credit-card payment is a transfer, and this is the predicate that says
	// so — a paired leg, whatever it is filed under.
	t.Run("a paired leg is never a refund", func(t *testing.T) {
		paid := credit
		paid.Txn.TransferPairID = "pair-1"
		require.False(t, CanBeARefund(paid))
	})

	t.Run("a forecast and a deleted row moved no money", func(t *testing.T) {
		forecast := credit
		forecast.Txn.IsEstimate = true
		require.False(t, CanBeARefund(forecast))
		gone := credit
		gone.Txn.IsDeleted = true
		require.False(t, CanBeARefund(gone))
	})

	t.Run("an unfiled credit is exactly the case a link is for", func(t *testing.T) {
		require.True(t, CanBeARefund(refundPosting("c", "20.00", 9, Category{}, false)))
	})
}

func TestRankRefundCandidatesOffersTheLikeliestChargeFirst(t *testing.T) {
	refund := refundPosting("credit", "20.00", 20, Category{}, false)
	refund.Txn.Payee = "Fresh Market"

	other := predAccount()
	other.ID = "acct-2"

	exact := refundPosting("exact", "-20.00", 18, refundGroceries, true)
	exact.Txn.Payee = "Fresh Market"

	samePayee := refundPosting("same-payee", "-90.00", 17, refundGroceries, true)
	samePayee.Txn.Payee = "FRESH  market"

	sameAccount := refundPosting("same-account", "-80.00", 16, refundGroceries, true)
	sameAccount.Txn.Payee = "Hardware Store"

	elsewhere := refundPosting("elsewhere", "-70.00", 19, refundGroceries, true)
	elsewhere.Txn.Payee = "Hardware Store"
	elsewhere.Account = other

	ranked := RankRefundCandidates(refund,
		[]Posting{elsewhere, sameAccount, samePayee, exact}, RefundCandidateWindowDays)
	require.Equal(t, []ID{"exact", "same-payee", "same-account", "elsewhere"}, postingIDs(ranked))
}

// Same account, no amount match: only the merchant words separate these, and
// nearest-first would otherwise put the hardware store and salon first.
func TestRankRefundCandidatesMatchesTheMerchantAcrossStoreNumbers(t *testing.T) {
	refund := refundPosting("credit", "20.00", 20, Category{}, false)
	refund.Txn.StatementName = "FRESH MKT 0822 RETURN"

	shop := refundPosting("shop", "-90.00", 12, refundGroceries, true)
	shop.Txn.StatementName = "FRESH MKT 0417 SPRINGFIELD"

	hardware := refundPosting("hardware", "-80.00", 18, refundGroceries, true)
	hardware.Txn.StatementName = "CORNER HARDWARE 22"

	// One word in common of the credit's three is under half of them.
	salon := refundPosting("salon", "-70.00", 17, refundGroceries, true)
	salon.Txn.StatementName = "FRESH CUTS SALON"

	ranked := RankRefundCandidates(refund, []Posting{hardware, salon, shop}, RefundCandidateWindowDays)
	require.Equal(t, []ID{"shop", "hardware", "salon"}, postingIDs(ranked))
}

// Ground rule 5: the bank's name ranks a charge, so the rows above, renamed
// every way that could mislead, rank exactly as they did.
func TestRankRefundCandidatesIgnoresARenamedPayee(t *testing.T) {
	refund := refundPosting("credit", "20.00", 20, Category{}, false)
	refund.Txn.StatementName = "FRESH MKT 0822 RETURN"
	refund.Txn.Payee = "Weekly shop"

	shop := refundPosting("shop", "-90.00", 12, refundGroceries, true)
	shop.Txn.StatementName = "FRESH MKT 0417 SPRINGFIELD"
	shop.Txn.Payee = "Farmers stall"

	hardware := refundPosting("hardware", "-80.00", 18, refundGroceries, true)
	hardware.Txn.StatementName = "CORNER HARDWARE 22"
	hardware.Txn.Payee = "Weekly shop"

	salon := refundPosting("salon", "-70.00", 17, refundGroceries, true)
	salon.Txn.StatementName = "FRESH CUTS SALON"
	salon.Txn.Payee = "Fresh Mkt"

	ranked := RankRefundCandidates(refund, []Posting{hardware, salon, shop}, RefundCandidateWindowDays)
	require.Equal(t, []ID{"shop", "hardware", "salon"}, postingIDs(ranked))
}

func TestRankRefundCandidatesFallsBackToThePayeeOfAHandEnteredCharge(t *testing.T) {
	refund := refundPosting("credit", "20.00", 20, Category{}, false)
	refund.Txn.StatementName = "FRESH MKT 0417"
	refund.Txn.Payee = ""

	handEntered := refundPosting("hand-entered", "-90.00", 15, refundGroceries, true)
	handEntered.Txn.Source = SourceManual
	handEntered.Txn.Payee = "fresh mkt 0417"

	nearer := refundPosting("nearer", "-80.00", 18, refundGroceries, true)
	nearer.Txn.StatementName = "CORNER HARDWARE 22"

	ranked := RankRefundCandidates(refund, []Posting{nearer, handEntered}, RefundCandidateWindowDays)
	require.Equal(t, []ID{"hand-entered", "nearer"}, postingIDs(ranked))
}

func TestRankRefundCandidatesDropsWhatNoCreditCouldReverse(t *testing.T) {
	refund := refundPosting("credit", "50.00", 20, Category{}, false)

	tooSmall := refundPosting("too-small", "-30.00", 18, refundGroceries, true)
	later := refundPosting("later", "-90.00", 22, refundGroceries, true)
	tooOld := refundPosting("too-old", "-90.00", 18, refundGroceries, true)
	tooOld.Txn.Date = NewDate(2026, time.August, 20).AddDays(-RefundCandidateWindowDays - 1)
	aTransfer := refundPosting("transfer", "-90.00", 18, refundMoving, true)
	anotherCredit := refundPosting("credit-2", "90.00", 18, refundGroceries, true)
	equal := refundPosting("equal", "-50.00", 18, refundGroceries, true)

	charges := []Posting{tooSmall, later, tooOld, aTransfer, anotherCredit, equal, refund}
	ranked := RankRefundCandidates(refund, charges, RefundCandidateWindowDays)
	require.Equal(t, []ID{"equal"}, postingIDs(ranked))

	// An unbounded search reaches past the window, and past nothing else: the
	// charge that is too small, the one dated later and the transfer stay out.
	searched := RankRefundCandidates(refund, charges, 0)
	require.Equal(t, []ID{"equal", "too-old"}, postingIDs(searched))
}

func postingIDs(postings []Posting) []ID {
	out := make([]ID, 0, len(postings))
	for _, one := range postings {
		out = append(out, one.Txn.ID)
	}
	return out
}

// The vote's clear original purchase and the picker's first offer are the
// same charge: the same amount, the same merchant by the bank's wording, on or
// before the credit by the date each is reported on, within the window.
func TestTheOriginalPurchaseIsReadAsTheRefundPickerReadsIt(t *testing.T) {
	credit := refundPosting("credit", "25.00", 20, Category{}, false).Txn
	credit.StatementName = "GARDEN DEPOT 0822 RETURN"
	purchaseOn := func(mod func(*Transaction)) (int, bool) {
		charge := refundPosting("rake", "-25.00", 8, refundGroceries, true).Txn
		charge.StatementName = "GARDEN DEPOT 0417"
		mod(&charge)
		return OriginalPurchaseAge(credit, charge)
	}

	age, ok := purchaseOn(func(*Transaction) {})
	require.True(t, ok)
	require.Equal(t, 12, age)

	_, ok = purchaseOn(func(c *Transaction) { c.Amount = MustFromString("-25.01") })
	require.False(t, ok, "a cent off is another purchase")
	_, ok = purchaseOn(func(c *Transaction) { c.StatementName = "CORNER HARDWARE 22" })
	require.False(t, ok, "another merchant's charge")
	_, ok = purchaseOn(func(c *Transaction) { c.StatementName, c.Payee = "CORNER HARDWARE 22", "Garden Depot" })
	require.False(t, ok, "a payee renamed to the merchant does not make it the merchant's (ground rule 5)")
	_, ok = purchaseOn(func(c *Transaction) { c.Date = NewDate(2026, time.August, 23) })
	require.False(t, ok, "dated after the credit")
	_, ok = purchaseOn(func(c *Transaction) {
		c.Date = NewDate(2026, time.August, 20).AddDays(-RefundCandidateWindowDays - 1)
	})
	require.False(t, ok, "more than the window before the credit")

	// Posted before the credit but reported after it: a card charge whose
	// statement comes due later than the credit lands.
	_, ok = purchaseOn(func(c *Transaction) { c.EffectiveDate = NewDate(2026, time.September, 10) })
	require.False(t, ok, "the reporting date decides, not the posted one (trap 4)")
	age, ok = purchaseOn(func(c *Transaction) {
		c.Date = NewDate(2026, time.August, 22)
		c.EffectiveDate = NewDate(2026, time.August, 15)
	})
	require.True(t, ok)
	require.Equal(t, 5, age)

	charge := refundPosting("rake", "-25.00", 8, refundGroceries, true)
	charge.Txn.StatementName = "GARDEN DEPOT 0417"
	refund := refundPosting("credit", "25.00", 20, Category{}, false)
	refund.Txn = credit
	require.Equal(t, []ID{"rake"}, postingIDs(RankRefundCandidates(refund, []Posting{charge}, RefundCandidateWindowDays)))
}

func TestARefundIsFullWhenTheCreditsGiveTheWholeChargeBack(t *testing.T) {
	charge := MustFromString("-60.00")
	half := MustFromString("30.00")
	if got := RefundState(charge, nil); got != RefundStateNone {
		t.Fatalf("no credits: %q", got)
	}
	if got := RefundState(charge, []Money{half}); got != RefundStatePartial {
		t.Fatalf("one of two items back: %q", got)
	}
	if got := RefundState(charge, []Money{half, half}); got != RefundStateFull {
		t.Fatalf("both items back: %q", got)
	}
	if got := RefundState(MustFromString("-120.00"), []Money{MustFromString("125.00")}); got != RefundStateFull {
		t.Fatalf("more back than the row paid, with tax and a gift card share: %q", got)
	}
}
