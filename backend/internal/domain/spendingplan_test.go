package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var planAugust = NewMonth(2026, time.August)

var (
	planSalaryCategory   = Category{ID: "cat-salary", Name: "Salary", Kind: CategoryIncome}
	planTransferCategory = Category{ID: "cat-transfer", Name: "Transfer", Kind: CategoryTransfer}
)

func planPosting(txnID, amount string, on Date, mutate ...func(*Posting)) Posting {
	account := Account{ID: "acct-1", Name: "Checking 1", Kind: KindCash}
	posting := Posting{
		Txn: Transaction{
			ID:            ID(txnID),
			AccountID:     account.ID,
			Date:          on,
			Amount:        MustFromString(amount),
			StatementName: "SQ *COFFEE 1234",
			Payee:         "Coffee",
			CategoryID:    envGroceriesCategory.ID,
			Source:        SourceSync,
		},
		Account:     account,
		Category:    envGroceriesCategory,
		HasCategory: true,
	}
	for _, apply := range mutate {
		apply(&posting)
	}
	return posting
}

func planSpend(txnID, amount string, mutate ...func(*Posting)) Posting {
	return planPosting(txnID, amount, NewDate(2026, time.August, 15), mutate...)
}

func planIncome(txnID, amount string, mutate ...func(*Posting)) Posting {
	return planPosting(txnID, amount, NewDate(2026, time.August, 1), append([]func(*Posting){
		func(p *Posting) {
			p.Category = planSalaryCategory
			p.Txn.CategoryID = planSalaryCategory.ID
		},
	}, mutate...)...)
}

// planUncategorized is a card payment or goal transfer: no category at all,
// which is not the same as a transfer category.
func planUncategorized(p *Posting) {
	p.Category = Category{}
	p.HasCategory = false
	p.Txn.CategoryID = ""
}

func planEnvelope(mutate ...func(*Envelope)) Envelope {
	return envEnvelope(mutate...)
}

func planInputs(mutate ...func(*MonthInputs)) MonthInputs {
	inputs := MonthInputs{Month: planAugust}
	for _, apply := range mutate {
		apply(&inputs)
	}
	return inputs
}

// planFrom builds a stored month straight from bucket amounts.
func planFrom(amounts map[BucketKey]string) SpendingPlanMonth {
	buckets := make(map[BucketKey]Bucket, len(BucketOrder))
	for _, key := range BucketOrder {
		amount := Zero
		if literal, ok := amounts[key]; ok {
			amount = MustFromString(literal)
		}
		buckets[key] = Bucket{Key: key, CalculatedAmount: amount}
	}
	return SpendingPlanMonth{Month: planAugust, Buckets: buckets}
}

func TestTheEffectiveAmountIsTheCalculatedOneByDefault(t *testing.T) {
	bucket := Bucket{Key: BucketBills, CalculatedAmount: MustFromString("-100")}

	require.Equal(t, "-100.00", bucket.Effective().String())
}

func TestAUserOverrideWinsOverTheCalculation(t *testing.T) {
	bucket := Bucket{
		Key:                  BucketBills,
		CalculatedAmount:     MustFromString("-100"),
		OverwrittenAmount:    MustFromString("-250"),
		HasOverwrittenAmount: true,
	}

	require.Equal(t, "-250.00", bucket.Effective().String())
}

func TestResettingTheOverrideFallsBackWithoutLosingIt(t *testing.T) {
	bucket := Bucket{
		Key:                  BucketBills,
		CalculatedAmount:     MustFromString("-100"),
		OverwrittenAmount:    MustFromString("-250"),
		HasOverwrittenAmount: true,
		ResetOverwritten:     true,
	}

	require.Equal(t, "-100.00", bucket.Effective().String())
	require.Equal(t, "-250.00", bucket.OverwrittenAmount.String())
}

func TestEveryBucketIsPresentEvenWhenTheMonthIsEmpty(t *testing.T) {
	month := ComputeMonth(planInputs(), Zero, nil)

	require.Len(t, month.Buckets, len(BucketOrder))
	for _, key := range BucketOrder {
		require.Contains(t, month.Buckets, key)
	}
	require.Equal(t, "0.00", month.LeftThisMonth().String())
}

func TestLeftThisMonthIsTheSumOfTheSixBuckets(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planIncome("t-pay", "3000"), planSpend("t-shop", "-200")}
		in.Envelopes = []Envelope{planEnvelope()}
		in.GoalContributions = []GoalContribution{{
			GoalID: "g-1", TxnID: "t-goal", Amount: MustFromString("-100"), IsTakenFromPlan: true,
			AccountID: "acct-checking", On: NewDate(2026, time.August, 15),
		}}
	}), MustFromString("150"), nil)

	// 150 rollover + 3000 income - 400 planned - 200 other - 100 goals
	require.Equal(t, "2450.00", month.LeftThisMonth().String())
}

// A charge filed under a goal leaves the plan entirely, and does not land in
// the Goals bucket either: that money was set aside in earlier months.
func TestSpendingRecordedAgainstAGoalLeavesThePlan(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-flights", "-400"), planSpend("t-lunch", "-30")}
		in.GoalContributions = []GoalContribution{{
			GoalID: "g-1", TxnID: "t-flights", Amount: MustFromString("-400"),
			Kind: GoalSpent, IsTakenFromPlan: true,
			AccountID: "acct-checking", On: NewDate(2026, time.August, 15),
		}}
	}), Zero, nil)

	require.Equal(t, "-30.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
	require.NotContains(t, month.Bucket(BucketOtherSpend).ContributingTxnIDs, ID("t-flights"))
	require.Equal(t, "0.00", month.Bucket(BucketGoals).CalculatedAmount.String())
	require.Empty(t, month.Bucket(BucketGoals).ContributingTxnIDs)
}

// The exclusion is the link, not a flag on the row, so unlinking restores it.
func TestAChargeNoLongerFiledUnderAGoalIsBackInThePlan(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-flights", "-400")}
	}), Zero, nil)

	require.Equal(t, "-400.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
}

func TestATransactionOnlyCountsInTheMonthItsEffectiveDateFallsIn(t *testing.T) {
	charge := planSpend("t-card", "-90", func(p *Posting) {
		p.Txn.EffectiveDate = NewDate(2026, time.September, 3)
	})

	month := ComputeMonth(planInputs(func(in *MonthInputs) { in.Postings = []Posting{charge} }), Zero, nil)

	require.Equal(t, "0.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
}

func TestRowsThePlanDoesNotCountNeverReachABucket(t *testing.T) {
	skipped := planSpend("t-skip", "-90", func(p *Posting) { p.Txn.ExcludedFromSpendingPlan = true })

	month := ComputeMonth(planInputs(func(in *MonthInputs) { in.Postings = []Posting{skipped} }), Zero, nil)

	require.Empty(t, month.Bucket(BucketOtherSpend).ContributingTxnIDs)
}

func TestThePerMonthExclusionListDropsARowAndRecordsIt(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-a", "-40"), planSpend("t-b", "-60")}
		in.ExcludedTxnIDs = map[BucketKey][]ID{BucketOtherSpend: {"t-b"}}
	}), Zero, nil)

	bucket := month.Bucket(BucketOtherSpend)
	require.Equal(t, "-40.00", bucket.CalculatedAmount.String())
	require.Equal(t, []ID{"t-a"}, bucket.ContributingTxnIDs)
	require.Equal(t, []ID{"t-b"}, bucket.ExcludedTxnIDs)
}

var planRent = SeriesOccurrence{
	SeriesID:       "s-rent",
	DueOn:          NewDate(2026, time.August, 1),
	Kind:           SeriesBill,
	ExpectedAmount: MustFromString("-1500"),
}

func TestAnUnfulfilledOccurrenceCountsAtItsExpectedAmount(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Occurrences = []SeriesOccurrence{planRent}
	}), Zero, nil)

	require.Equal(t, "-1500.00", month.Bucket(BucketBills).CalculatedAmount.String())
}

func TestABillThatHasPostedCountsOnceNotTwice(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-rent", "-1520.00")}
		in.Occurrences = []SeriesOccurrence{planRent}
		in.SeriesLinks = []SeriesLink{{TxnID: "t-rent", SeriesID: "s-rent", DueOn: NewDate(2026, time.August, 1)}}
	}), Zero, nil)

	bills := month.Bucket(BucketBills)
	// The posted amount, once — not the expected amount as well, and not in
	// Other Spend on top of that.
	require.Equal(t, "-1520.00", bills.CalculatedAmount.String())
	require.Equal(t, []ID{"t-rent"}, bills.ContributingTxnIDs)
	require.Equal(t, "0.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
}

func TestAPaymentThePlanDoesNotCountLeavesItsOccurrenceExpected(t *testing.T) {
	// A row the plan never counts cannot be what paid the bill, or the month
	// says nothing is owed while nothing it counts has moved.
	payment := planSpend("t-rent", "-1520.00", func(p *Posting) {
		p.Txn.ExcludedFromSpendingPlan = true
	})

	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{payment}
		in.Occurrences = []SeriesOccurrence{planRent}
		in.SeriesLinks = []SeriesLink{{TxnID: "t-rent", SeriesID: "s-rent", DueOn: planRent.DueOn}}
	}), Zero, nil)

	bills := month.Bucket(BucketBills)
	require.Equal(t, "-1500.00", bills.CalculatedAmount.String())
	require.Equal(t, "0.00", bills.PostedAmount.String())
	require.Empty(t, bills.ContributingTxnIDs)
}

func TestAPostedBillTheMonthExcludedStaysFulfilled(t *testing.T) {
	// calculations.md §13: excluding a posted bill reads as "do not count this
	// bill", not "count what I predicted instead", so the occurrence keeps its
	// claimant and the bucket contributes zero.
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-rent", "-1520.00")}
		in.Occurrences = []SeriesOccurrence{planRent}
		in.SeriesLinks = []SeriesLink{{TxnID: "t-rent", SeriesID: "s-rent", DueOn: planRent.DueOn}}
		in.ExcludedTxnIDs = map[BucketKey][]ID{BucketBills: {"t-rent"}}
	}), Zero, nil)

	require.Equal(t, "0.00", month.Bucket(BucketBills).CalculatedAmount.String())
}

func TestAPostingWhoseLinkNamesNoOccurrenceStillCountsAsItsSeries(t *testing.T) {
	// Simplifi links a transaction to a series without an occurrence when the
	// schedule was created later. It is still that series' bill, and Simplifi
	// keeps it out of Other Spend.
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-pest", "-140.00")}
		in.Occurrences = nil
		in.SeriesLinks = []SeriesLink{{TxnID: "t-pest", SeriesID: "s-pest", Kind: SeriesBill}}
	}), Zero, nil)

	bills := month.Bucket(BucketBills)
	require.Equal(t, "-140.00", bills.CalculatedAmount.String())
	require.Equal(t, []ID{"t-pest"}, bills.ContributingTxnIDs)
	require.Equal(t, "0.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
}

func TestAnUnslottedPostingIsNotCountedTwiceWhenItAlsoFillsASlot(t *testing.T) {
	// The slotted path and the unslotted one must not both claim a posting.
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-rent", "-1520.00")}
		in.Occurrences = []SeriesOccurrence{planRent}
		in.SeriesLinks = []SeriesLink{{
			TxnID: "t-rent", SeriesID: "s-rent",
			DueOn: NewDate(2026, time.August, 1), Kind: SeriesBill,
		}}
	}), Zero, nil)

	require.Equal(t, "-1520.00", month.Bucket(BucketBills).CalculatedAmount.String())
	require.Equal(t, []ID{"t-rent"}, month.Bucket(BucketBills).ContributingTxnIDs)
}

func TestAnUnslottedTransferPostingStillNetsToZero(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-card", "-400.00")}
		in.Occurrences = nil
		in.SeriesLinks = []SeriesLink{{
			TxnID: "t-card", SeriesID: "s-card", Kind: SeriesCreditCardPayment,
		}}
	}), Zero, nil)

	require.Equal(t, "0.00", month.Bucket(BucketBills).CalculatedAmount.String())
	require.Equal(t, "0.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
}

func TestTheLinkIsTheDueDateSlotNotTheSeriesAlone(t *testing.T) {
	// A twice-monthly series with one occurrence posted: keying on the series
	// would let the posted row satisfy both slots.
	first := SeriesOccurrence{SeriesID: "s-gym", DueOn: NewDate(2026, time.August, 1), Kind: SeriesSubscription, ExpectedAmount: MustFromString("-50")}
	second := SeriesOccurrence{SeriesID: "s-gym", DueOn: NewDate(2026, time.August, 15), Kind: SeriesSubscription, ExpectedAmount: MustFromString("-50")}

	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-gym-1", "-55")}
		in.Occurrences = []SeriesOccurrence{first, second}
		in.SeriesLinks = []SeriesLink{{TxnID: "t-gym-1", SeriesID: "s-gym", DueOn: NewDate(2026, time.August, 1)}}
	}), Zero, nil)

	require.Equal(t, "-105.00", month.Bucket(BucketBills).CalculatedAmount.String())
}

func TestAnIncomeSeriesLandsInIncomeNotBills(t *testing.T) {
	payday := SeriesOccurrence{SeriesID: "s-pay", DueOn: NewDate(2026, time.August, 1), Kind: SeriesIncome, ExpectedAmount: MustFromString("3000")}

	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Occurrences = []SeriesOccurrence{payday}
	}), Zero, nil)

	require.Equal(t, "3000.00", month.Bucket(BucketIncome).CalculatedAmount.String())
	require.Equal(t, "0.00", month.Bucket(BucketBills).CalculatedAmount.String())
}

func TestTransfersAndCreditCardPaymentsNetToZeroInsideBills(t *testing.T) {
	for _, kind := range []SeriesKind{SeriesTransfer, SeriesCreditCardPayment} {
		t.Run(string(kind), func(t *testing.T) {
			occurrence := SeriesOccurrence{SeriesID: "s-pay-card", DueOn: NewDate(2026, time.August, 20), Kind: kind, ExpectedAmount: MustFromString("-400")}

			month := ComputeMonth(planInputs(func(in *MonthInputs) {
				in.Occurrences = []SeriesOccurrence{occurrence}
			}), Zero, nil)

			require.Equal(t, "0.00", month.Bucket(BucketBills).CalculatedAmount.String())
		})
	}
}

func TestAPostedCreditCardPaymentDoesNotReduceFreeToSpend(t *testing.T) {
	// The spending came out of the plan when the purchase posted.
	occurrence := SeriesOccurrence{SeriesID: "s-pay-card", DueOn: NewDate(2026, time.August, 20), Kind: SeriesCreditCardPayment, ExpectedAmount: MustFromString("-400")}

	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planIncome("t-pay", "3000"), planSpend("t-cc-payment", "-400", planUncategorized)}
		in.Occurrences = []SeriesOccurrence{occurrence}
		in.SeriesLinks = []SeriesLink{{TxnID: "t-cc-payment", SeriesID: "s-pay-card", DueOn: NewDate(2026, time.August, 20)}}
	}), Zero, nil)

	require.Equal(t, "3000.00", month.LeftThisMonth().String())
}

func TestANettedPaymentIsStillListedForVisibility(t *testing.T) {
	occurrence := SeriesOccurrence{SeriesID: "s-move", DueOn: NewDate(2026, time.August, 20), Kind: SeriesTransfer, ExpectedAmount: MustFromString("-500")}

	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-move", "-500", planUncategorized)}
		in.Occurrences = []SeriesOccurrence{occurrence}
		in.SeriesLinks = []SeriesLink{{TxnID: "t-move", SeriesID: "s-move", DueOn: NewDate(2026, time.August, 20)}}
	}), Zero, nil)

	require.Equal(t, []ID{"t-move"}, month.Bucket(BucketBills).ContributingTxnIDs)
}

func TestAMatchedTransferLegNeverReachesThePlanAtAll(t *testing.T) {
	asTransferLeg := func(p *Posting) {
		p.Category = planTransferCategory
		p.Txn.CategoryID = planTransferCategory.ID
		p.Txn.TransferPairID = "pair-1"
	}

	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{
			planSpend("t-out", "-500", asTransferLeg),
			planSpend("t-in", "500", asTransferLeg),
		}
	}), Zero, nil)

	require.Equal(t, "0.00", month.LeftThisMonth().String())
}

func TestEnvelopeSpendLeavesTheOtherSpendBucket(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-groceries", "-120"), planSpend("t-random", "-30")}
		in.Envelopes = []Envelope{planEnvelope()}
	}), Zero, envMatcher(map[ID][]ID{envGroceriesID: {"t-groceries"}}))

	other := month.Bucket(BucketOtherSpend)
	require.Equal(t, "-30.00", other.CalculatedAmount.String())
	require.Equal(t, []ID{"t-random"}, other.ContributingTxnIDs)
	require.Equal(t, "120.00", month.Envelopes[0].Spent.String())
}

func TestPlannedSpendReservesTheTargetsNotTheActualSpend(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-groceries", "-120")}
		in.Envelopes = []Envelope{planEnvelope()}
	}), Zero, envMatcher(map[ID][]ID{envGroceriesID: {"t-groceries"}}))

	planned := month.Bucket(BucketPlannedSpend)
	require.Equal(t, "-400.00", planned.CalculatedAmount.String())
	require.Equal(t, []ID{"t-groceries"}, planned.ContributingTxnIDs)
}

func TestATransactionMatchingTwoEnvelopesBelongsToExactlyOneAndTheLoserIsRecorded(t *testing.T) {
	older := planEnvelope()
	newer := planEnvelope(func(e *Envelope) {
		e.ID = envDiningID
		e.Name = "Dining"
		e.CreatedAt = time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	})

	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-both", "-60")}
		in.Envelopes = []Envelope{older, newer}
	}), Zero, envMatcher(map[ID][]ID{envGroceriesID: {"t-both"}, envDiningID: {"t-both"}}))

	spent := map[ID]string{}
	for _, status := range month.Envelopes {
		spent[status.EnvelopeID] = status.Spent.String()
	}
	require.Equal(t, map[ID]string{envGroceriesID: "60.00", envDiningID: "0.00"}, spent)
	require.Equal(t, map[ID][]ID{"t-both": {envDiningID}}, month.ContestedEnvelopeTxnIDs)
}

func TestIncomeIsNeverSwallowedByAnEnvelope(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planIncome("t-pay", "3000")}
		in.Envelopes = []Envelope{planEnvelope()}
	}), Zero, envMatcher(map[ID][]ID{envGroceriesID: {"t-pay"}}))

	require.Equal(t, "3000.00", month.Bucket(BucketIncome).CalculatedAmount.String())
	require.Equal(t, "0.00", month.Envelopes[0].Spent.String())
}

func TestContributionsTakenFromThePlanReduceFreeToSpendOnce(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-goal", "-200", planUncategorized)}
		in.GoalContributions = []GoalContribution{{
			GoalID: "g-1", TxnID: "t-goal", Amount: MustFromString("-200"), IsTakenFromPlan: true,
			AccountID: "acct-checking", On: NewDate(2026, time.August, 15),
		}}
	}), Zero, nil)

	require.Equal(t, "-200.00", month.Bucket(BucketGoals).CalculatedAmount.String())
	require.Equal(t, "0.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
}

func TestAContributionCountsInTheMonthItPosted(t *testing.T) {
	// The cascade hands every month the whole chain's contributions.
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-goal", "-200", planUncategorized)}
		in.GoalContributions = []GoalContribution{{
			GoalID: "g-1", TxnID: "t-goal", Amount: MustFromString("-200"), IsTakenFromPlan: true,
			AccountID: "acct-checking", On: NewDate(2026, time.September, 3),
		}}
	}), Zero, nil)

	require.Equal(t, "0.00", month.Bucket(BucketGoals).CalculatedAmount.String())
}

func TestAGoalFundedFromAlreadyCountedMoneyIsNotCountedAgain(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-goal", "-200", planUncategorized)}
		in.GoalContributions = []GoalContribution{{
			GoalID: "g-1", TxnID: "t-goal", Amount: MustFromString("-200"), IsTakenFromPlan: false,
			AccountID: "acct-checking", On: NewDate(2026, time.August, 15),
		}}
	}), Zero, nil)

	require.Equal(t, "0.00", month.Bucket(BucketGoals).CalculatedAmount.String())
	require.Equal(t, "0.00", month.LeftThisMonth().String())
}

func TestAClosedOutMonthFreezesInsteadOfRecalculating(t *testing.T) {
	stored := planFrom(map[BucketKey]string{BucketIncome: "1000"})
	stored.IsClosedOut = true
	edited := planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planIncome("t-pay", "9999")}
		in.IsClosedOut = true
	})

	results, err := RecalculateChain([]MonthInputs{edited}, Zero, map[Month]SpendingPlanMonth{planAugust: stored}, nil)

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, stored, results[0])
	require.Equal(t, "1000.00", results[0].LeftThisMonth().String())
}

func TestClosingOutAMonthWithNothingStoredIsALoudFailure(t *testing.T) {
	_, err := RecalculateChain([]MonthInputs{planInputs(func(in *MonthInputs) { in.IsClosedOut = true })}, Zero, nil, nil)

	require.ErrorContains(t, err, "closed out but no stored plan")
}

func planJuly(amount string) MonthInputs {
	earned := planPosting("t-jul", amount, NewDate(2026, time.July, 1), func(p *Posting) {
		p.Category = planSalaryCategory
		p.Txn.CategoryID = planSalaryCategory.ID
	})
	return MonthInputs{Month: NewMonth(2026, time.July), Postings: []Posting{earned}}
}

func TestAMonthsRolloverIsThePriorMonthsLeftThisMonth(t *testing.T) {
	august := planInputs(func(in *MonthInputs) { in.Postings = []Posting{planIncome("t-aug", "300")} })

	results, err := RecalculateChain([]MonthInputs{planJuly("500"), august}, Zero, nil, nil)

	require.NoError(t, err)
	require.Equal(t, "500.00", results[0].LeftThisMonth().String())
	require.Equal(t, "500.00", results[1].Bucket(BucketRollover).CalculatedAmount.String())
	require.Equal(t, "800.00", results[1].LeftThisMonth().String())
}

func TestRecalculatingAnEarlierMonthCascadesThroughEveryOpenMonth(t *testing.T) {
	august := planInputs(func(in *MonthInputs) { in.Postings = []Posting{planIncome("t-aug", "300")} })
	september := MonthInputs{Month: NewMonth(2026, time.September)}

	before, err := RecalculateChain([]MonthInputs{planJuly("500"), august, september}, Zero, nil, nil)
	require.NoError(t, err)
	after, err := RecalculateChain([]MonthInputs{planJuly("900"), august, september}, Zero, nil, nil)
	require.NoError(t, err)

	require.Equal(t, "800.00", before[2].Bucket(BucketRollover).CalculatedAmount.String())
	require.Equal(t, "1200.00", after[1].LeftThisMonth().String())
	require.Equal(t, "1200.00", after[2].Bucket(BucketRollover).CalculatedAmount.String())
}

func TestAClosedMonthStopsTheCascade(t *testing.T) {
	frozenAugust := planFrom(map[BucketKey]string{BucketIncome: "42"})
	frozenAugust.IsClosedOut = true
	august := planInputs(func(in *MonthInputs) { in.IsClosedOut = true })
	september := MonthInputs{Month: NewMonth(2026, time.September)}

	results, err := RecalculateChain(
		[]MonthInputs{planJuly("9000"), august, september},
		Zero,
		map[Month]SpendingPlanMonth{planAugust: frozenAugust},
		nil,
	)

	require.NoError(t, err)
	// September carries August's stored figure, not one recomputed from a July
	// edit that August was closed before seeing.
	require.Equal(t, "42.00", results[2].Bucket(BucketRollover).CalculatedAmount.String())
}

// envelopeChain is July with a 400 Groceries envelope and a July purchase of
// `spent`, and August holding that envelope's next month.
func envelopeChain(spent string, august func(*Envelope)) []MonthInputs {
	july := planEnvelope(func(e *Envelope) { e.ID = "env-jul" })
	next, _ := RollForward(july, EnvelopeStatus{})
	next.ID = "env-aug"
	august(&next)
	purchase := planPosting("t-jul-groceries", spent, NewDate(2026, time.July, 10))
	return []MonthInputs{
		{Month: NewMonth(2026, time.July), Postings: []Posting{purchase}, Envelopes: []Envelope{july}},
		{Month: planAugust, Envelopes: []Envelope{next}},
	}
}

func groceriesClaimsEverything(Envelope, Part) bool { return true }

func TestACarriedEnvelopeRolloverFollowsThePriorMonthsSpend(t *testing.T) {
	before, err := RecalculateChain(envelopeChain("-150", func(*Envelope) {}), Zero, nil, groceriesClaimsEverything)
	require.NoError(t, err)
	after, err := RecalculateChain(envelopeChain("-390", func(*Envelope) {}), Zero, nil, groceriesClaimsEverything)
	require.NoError(t, err)

	require.Equal(t, "250.00", before[1].Envelopes[0].RolloverIn.String())
	// A late July purchase still moves what August carries.
	require.Equal(t, "10.00", after[1].Envelopes[0].RolloverIn.String())
}

func TestARolloverTheUserSetIsNotRecomputed(t *testing.T) {
	chain := envelopeChain("-150", func(e *Envelope) { *e = SetRollover(*e, MustFromString("75")) })

	results, err := RecalculateChain(chain, Zero, nil, groceriesClaimsEverything)

	require.NoError(t, err)
	require.Equal(t, "75.00", results[1].Envelopes[0].RolloverIn.String())
}

func TestAnAutoReleasedEnvelopeCarriesNothingThroughTheChain(t *testing.T) {
	chain := envelopeChain("-150", func(*Envelope) {})
	chain[0].Envelopes[0].AutoReleaseRollover = true

	results, err := RecalculateChain(chain, Zero, nil, groceriesClaimsEverything)

	require.NoError(t, err)
	require.True(t, results[1].Envelopes[0].RolloverIn.IsZero())
}

func TestWhatIsLeftIsSpreadOverTheDaysThatRemainTodayIncluded(t *testing.T) {
	perDay, ok := planFrom(map[BucketKey]string{BucketIncome: "1000"}).PerDay(NewDate(2026, time.August, 21))

	require.True(t, ok)
	require.Equal(t, "90.91", perDay.String())
}

func TestAMonthThatIsOverHasNoDailyFigureRatherThanAZeroDivisor(t *testing.T) {
	_, ok := planFrom(map[BucketKey]string{BucketIncome: "1000"}).PerDay(NewDate(2026, time.September, 1))

	require.False(t, ok)
}

func TestAFutureMonthSpreadsOverAllOfItsDays(t *testing.T) {
	perDay, ok := planFrom(map[BucketKey]string{BucketIncome: "310"}).PerDay(NewDate(2026, time.July, 15))

	require.True(t, ok)
	require.Equal(t, "10.00", perDay.String())
}

func planWithProjection(otherSpend string, projection Projection) SpendingPlanMonth {
	month := planFrom(map[BucketKey]string{BucketOtherSpend: otherSpend})
	month.Projection = projection
	month.HasProjection = true
	return month
}

func TestOtherSpendIsActualsOnlyAndTheProjectionStaysBesideIt(t *testing.T) {
	month := planWithProjection("-100", Projection{Type: ProjectionRunRate})
	asOf := NewDate(2026, time.August, 10)

	require.Equal(t, "-100.00", month.Bucket(BucketOtherSpend).Effective().String())
	require.Equal(t, "100.00", month.OtherSpendToDate().String())
	require.Equal(t, "310.00", ProjectedOtherSpending(month, asOf).String())
}

func TestTheRunRateExtrapolatesTheDaysLivedSoFarOverTheMonth(t *testing.T) {
	month := planWithProjection("-155", Projection{Type: ProjectionRunRate})

	require.Equal(t, "320.33", ProjectedOtherSpending(month, NewDate(2026, time.August, 15)).String())
}

func TestThePriorMonthMethodReadsTheStoredWindow(t *testing.T) {
	month := planWithProjection("-100", Projection{
		Type:               ProjectionPriorMonth,
		PriorOtherSpending: []Money{MustFromString("400"), MustFromString("560")},
	})

	require.Equal(t, "560.00", ProjectedOtherSpending(month, NewDate(2026, time.August, 10)).String())
}

func TestTheAverageMethodUsesOnlyTheWindowItWasGiven(t *testing.T) {
	month := planWithProjection("-100", Projection{
		Type:         ProjectionAverageNMonths,
		WindowMonths: 3,
		PriorOtherSpending: []Money{
			MustFromString("1000"),
			MustFromString("300"),
			MustFromString("400"),
			MustFromString("500"),
		},
	})

	require.Equal(t, "400.00", ProjectedOtherSpending(month, NewDate(2026, time.August, 10)).String())
}

func TestAMonthThatHasNotStartedProjectsFromThePriorMonthsAverage(t *testing.T) {
	// A run rate over zero days would project nothing for a future month, so
	// before the month starts it reads the prior months over the average
	// method's window.
	month := planWithProjection("0", Projection{
		Type:         ProjectionRunRate,
		WindowMonths: 3,
		PriorOtherSpending: []Money{
			MustFromString("1000"),
			MustFromString("300"),
			MustFromString("400"),
			MustFromString("500"),
		},
	})

	require.Equal(t, "400.00", ProjectedOtherSpending(month, NewDate(2026, time.July, 20)).String())
	// The day the month begins, the run rate takes over again.
	require.Equal(t, "0.00", ProjectedOtherSpending(month, NewDate(2026, time.August, 1)).String())
	// With no history the figure stays at what has been spent: nothing.
	bare := planWithProjection("0", Projection{Type: ProjectionRunRate, WindowMonths: 3})
	require.Equal(t, "0.00", ProjectedOtherSpending(bare, NewDate(2026, time.July, 20)).String())
}

func TestTheProjectionBufferIsAFlatAddOn(t *testing.T) {
	month := planWithProjection("-100", Projection{
		Type:               ProjectionPriorMonth,
		PriorOtherSpending: []Money{MustFromString("400")},
		Buffer:             MustFromString("50"),
	})

	require.Equal(t, "450.00", ProjectedOtherSpending(month, NewDate(2026, time.August, 10)).String())
}

func TestAProjectionNeverFallsBelowWhatHasAlreadyBeenSpent(t *testing.T) {
	month := planWithProjection("-800", Projection{
		Type:               ProjectionPriorMonth,
		PriorOtherSpending: []Money{MustFromString("400")},
	})

	require.Equal(t, "800.00", ProjectedOtherSpending(month, NewDate(2026, time.August, 10)).String())
}

func TestAMonthWithNoProjectionProjectsExactlyWhatHappened(t *testing.T) {
	month := planFrom(map[BucketKey]string{BucketOtherSpend: "-100"})
	asOf := NewDate(2026, time.August, 10)

	require.Equal(t, "100.00", ProjectedOtherSpending(month, asOf).String())
	require.Equal(t, "-100.00", ProjectedLeft(month, asOf).String())
}

func TestProjectedLeftSubtractsOnlyTheSpendingStillToCome(t *testing.T) {
	month := planFrom(map[BucketKey]string{BucketIncome: "1000", BucketOtherSpend: "-100"})
	month.Projection = Projection{Type: ProjectionRunRate}
	month.HasProjection = true

	// 310 projected, 100 already out of LeftThisMonth, so 210 still to go.
	require.Equal(t, "900.00", month.LeftThisMonth().String())
	require.Equal(t, "690.00", ProjectedLeft(month, NewDate(2026, time.August, 10)).String())
}

func TestTheMonthsOwnResultLeavesTheRolloverOut(t *testing.T) {
	// calculations.md §5: month_result is the five buckets without the
	// rollover.
	month := planFrom(map[BucketKey]string{
		BucketRollover: "-400", BucketIncome: "1000", BucketOtherSpend: "-100",
	})
	month.Projection = Projection{Type: ProjectionRunRate}
	month.HasProjection = true
	asOf := NewDate(2026, time.August, 10)

	require.Equal(t, "500.00", month.LeftThisMonth().String())
	require.Equal(t, "900.00", month.MonthResult().String())

	// 22 days left including the 10th: 900 / 22 rounds to 40.91.
	perDay, ok := month.MonthResultPerDay(asOf)
	require.True(t, ok)
	require.Equal(t, "40.91", perDay.String())

	// 100 over 10 days is 310 over the month, so 210 still to come.
	require.Equal(t, "290.00", ProjectedLeft(month, asOf).String())
	require.Equal(t, "690.00", ProjectedMonthResult(month, asOf).String())
}

func TestTheMonthsOwnResultHasNoDailyRateOnceTheMonthIsOver(t *testing.T) {
	month := planFrom(map[BucketKey]string{BucketIncome: "1000"})
	_, ok := month.MonthResultPerDay(NewDate(2026, time.September, 1))
	require.False(t, ok)

	// A month not yet started spreads over all of its own days: 1000 / 31.
	perDay, ok := month.MonthResultPerDay(NewDate(2026, time.July, 20))
	require.True(t, ok)
	require.Equal(t, "32.26", perDay.String())
}

func TestDaysElapsedIsTheRunRatesDivisor(t *testing.T) {
	month := planFrom(map[BucketKey]string{})
	require.Equal(t, 0, month.DaysElapsed(NewDate(2026, time.July, 31)))
	require.Equal(t, 10, month.DaysElapsed(NewDate(2026, time.August, 10)))
	require.Equal(t, 31, month.DaysElapsed(NewDate(2026, time.October, 2)))
}

func TestAMonthRecomputedThroughTheCascadeMatchesTheSameMonthComputedDirectly(t *testing.T) {
	// Without the matcher, the cascade reserves each envelope's target in
	// planned_spend while dropping its spend into Other Spend.
	matches := envMatcher(map[ID][]ID{envGroceriesID: {"t-groceries"}})
	august := planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-groceries", "-120"), planSpend("t-random", "-30")}
		in.Envelopes = []Envelope{planEnvelope()}
	})
	july := planJuly("500")

	results, err := RecalculateChain([]MonthInputs{july, august}, Zero, nil, matches)
	require.NoError(t, err)

	direct := ComputeMonth(august, results[0].LeftThisMonth(), matches)
	require.Equal(t, direct, results[1])
	require.Equal(t, "-30.00", results[1].Bucket(BucketOtherSpend).CalculatedAmount.String())
	require.Equal(t, "120.00", results[1].Envelopes[0].Spent.String())
}

func TestABillCountsInTheMonthItWasPaid(t *testing.T) {
	// A payment and the occurrence it fills can land in different months; the
	// amount belongs to the month the money moved, as in Simplifi. The
	// occurrence is still listed in its own month. calculations.md §5 rule 1.
	dueJanuary := SeriesOccurrence{
		SeriesID:       "s-rent",
		DueOn:          NewDate(2026, time.January, 31),
		Kind:           SeriesBill,
		ExpectedAmount: MustFromString("-1500"),
	}
	link := SeriesLink{TxnID: "t-rent", SeriesID: "s-rent", DueOn: dueJanuary.DueOn, Kind: SeriesBill}
	payment := planPosting("t-rent", "-1520.00", NewDate(2026, time.February, 2))

	january := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Month = NewMonth(2026, time.January)
		in.Postings = []Posting{payment}
		in.Occurrences = []SeriesOccurrence{dueJanuary}
		in.SeriesLinks = []SeriesLink{link}
	}), Zero, nil)

	// January's occurrence is filled, so January does not also charge for it.
	require.Equal(t, "0.00", january.Bucket(BucketBills).CalculatedAmount.String())
	require.Equal(t, "0.00", january.Bucket(BucketOtherSpend).CalculatedAmount.String())

	february := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Month = NewMonth(2026, time.February)
		in.Postings = []Posting{payment}
		in.SeriesLinks = []SeriesLink{link}
	}), Zero, nil)

	// February paid it, so February carries it — under Bills, because it is
	// the rent, and never as discretionary Other Spend.
	require.Equal(t, "-1520.00", february.Bucket(BucketBills).CalculatedAmount.String())
	require.Equal(t, []ID{"t-rent"}, february.Bucket(BucketBills).ContributingTxnIDs)
	require.Equal(t, "0.00", february.Bucket(BucketOtherSpend).CalculatedAmount.String())
	require.Empty(t, february.Bucket(BucketOtherSpend).ContributingTxnIDs)
}

func TestAnUnfilledOccurrenceIsExpectedButNotPosted(t *testing.T) {
	// §5 rule 1: a month still owing a bill counts it at the series' amount;
	// no transaction backs it, so it is absent from the posted figure.
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = nil
		in.Occurrences = []SeriesOccurrence{planRent}
	}), Zero, nil)

	bills := month.Bucket(BucketBills)
	require.Equal(t, "-1500.00", bills.CalculatedAmount.String())
	require.Equal(t, "0.00", bills.PostedAmount.String())
	require.Empty(t, bills.ContributingTxnIDs)
}

func TestARefundOutsideAnEnvelopeNetsAgainstOtherSpendNotIncome(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-shop", "-180"), planSpend("t-return", "100")}
	}), Zero, nil)

	// A $100 return on a non-envelope purchase reverses spending rather than
	// inflating Income.
	require.Equal(t, "-80.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
	require.Equal(t, "0.00", month.Bucket(BucketIncome).CalculatedAmount.String())
}

func TestAnUncategorizedPositiveIsNotIncome(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{
			planIncome("t-pay", "3000"),
			planSpend("t-shop", "-180"),
			planSpend("t-mystery", "75", planUncategorized),
		}
	}), Zero, nil)

	// §5: Income is what was filed as income. The unfiled $75 credit nets
	// against Other Spend, and is listed there for somebody to file.
	income := month.Bucket(BucketIncome)
	require.Equal(t, "3000.00", income.CalculatedAmount.String())
	require.Equal(t, []ID{"t-pay"}, income.ContributingTxnIDs)
	other := month.Bucket(BucketOtherSpend)
	require.Equal(t, "-105.00", other.CalculatedAmount.String())
	require.ElementsMatch(t, []ID{"t-shop", "t-mystery"}, other.ContributingTxnIDs)
	require.Equal(t, "2895.00", month.LeftThisMonth().String())
}

func TestANegativeFiledAsIncomeLowersIncomeNotOtherSpend(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{
			planIncome("t-pay", "3000"),
			planIncome("t-clawback", "-250"),
			planSpend("t-shop", "-180"),
		}
		in.Envelopes = []Envelope{planEnvelope()}
	}), Zero, envMatcher(map[ID][]ID{envGroceriesID: {"t-clawback"}}))

	// §2 ledger_kind: a part filed under an income category is earnings
	// whichever way its amount points, and no envelope takes it.
	income := month.Bucket(BucketIncome)
	require.Equal(t, "2750.00", income.CalculatedAmount.String())
	require.ElementsMatch(t, []ID{"t-pay", "t-clawback"}, income.ContributingTxnIDs)
	other := month.Bucket(BucketOtherSpend)
	require.Equal(t, "-180.00", other.CalculatedAmount.String())
	require.Equal(t, []ID{"t-shop"}, other.ContributingTxnIDs)
	require.Equal(t, "0.00", month.Envelopes[0].Spent.String())
}

func TestAnUncategorizedPositiveAloneLeavesOtherSpendAboveZero(t *testing.T) {
	month := ComputeMonth(planInputs(func(in *MonthInputs) {
		in.Postings = []Posting{planSpend("t-mystery", "75", planUncategorized)}
	}), Zero, nil)

	require.Equal(t, "0.00", month.Bucket(BucketIncome).CalculatedAmount.String())
	require.Equal(t, "75.00", month.Bucket(BucketOtherSpend).CalculatedAmount.String())
}

func TestAnUnknownProjectionTypeDoesNotPickAMethodSilently(t *testing.T) {
	// An unknown projection type degrades to the run rate.
	month := planWithProjection("-100", Projection{
		Type:         ProjectionType("whatever"),
		WindowMonths: 3,
		PriorOtherSpending: []Money{
			MustFromString("1000"), MustFromString("3000"),
		},
	})

	require.Equal(t, "310.00", ProjectedOtherSpending(month, NewDate(2026, time.August, 10)).String())
}
