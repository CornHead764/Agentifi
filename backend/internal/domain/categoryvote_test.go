package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func rows(n int, category string, opts ...func(*domain.CategoryEvidence)) []domain.CategoryEvidence {
	out := make([]domain.CategoryEvidence, 0, n)
	for i := 0; i < n; i++ {
		one := domain.CategoryEvidence{CategoryID: category, Similarity: 3, AgeDays: 30 * (i + 1)}
		for _, opt := range opts {
			opt(&one)
		}
		out = append(out, one)
	}
	return out
}

func transfer(e *domain.CategoryEvidence)        { e.IsTransfer = true }
func old(e *domain.CategoryEvidence)             { e.AgeDays = 1000 }
func reviewed(e *domain.CategoryEvidence)        { e.IsReviewed = true }
func arrivedReviewed(e *domain.CategoryEvidence) { e.ReviewedOnArrival = true }
func split(e *domain.CategoryEvidence)           { e.IsSplit = true }

func TestAUnanimousHistoryIsConfident(t *testing.T) {
	verdict := domain.VoteCategory(rows(12, "interest-earned"), 0.85)
	require.Equal(t, domain.CategoryVerdictConfident, verdict.Kind)
	require.Equal(t, "interest-earned", verdict.CategoryID)
	require.InDelta(t, 1.0, verdict.Confidence, 0.001)
	require.Equal(t, 12, verdict.Agreeing)
}

func TestTransferLegsAreNotCategorized(t *testing.T) {
	// Credit card payments, mostly transfer legs: left alone, not asked of a
	// model.
	evidence := append(rows(9, "", transfer), rows(1, "")...)
	verdict := domain.VoteCategory(evidence, 0.85)
	require.Equal(t, domain.CategoryVerdictTransfer, verdict.Kind)
	require.Empty(t, verdict.CategoryID)
}

func TestNoHistoryAndUncategorizedHistoryBothGoToTheModel(t *testing.T) {
	require.Equal(t, domain.CategoryVerdictNoHistory, domain.VoteCategory(nil, 0.85).Kind)

	// Eight rows somebody reviewed and kept uncategorized vote for nothing:
	// no history, and the model is told they were left alone.
	verdict := domain.VoteCategory(rows(8, "", reviewed), 0.85)
	require.Equal(t, domain.CategoryVerdictNoHistory, verdict.Kind)
	require.Zero(t, verdict.Confidence)
	require.Equal(t, 8, verdict.Uncategorized)
	require.Equal(t, 8, verdict.LeftAlone)
	require.Zero(t, verdict.AwaitingReview)
}

func TestUnreviewedUncategorizedHistoryIsNoHistory(t *testing.T) {
	verdict := domain.VoteCategory(rows(2, ""), 0.85)
	require.Equal(t, domain.CategoryVerdictNoHistory, verdict.Kind)
	require.Zero(t, verdict.Confidence)
	require.Equal(t, 2, verdict.Total)
	require.Equal(t, 2, verdict.Uncategorized)
	require.Equal(t, 2, verdict.AwaitingReview)
	require.Zero(t, verdict.LeftAlone)

	// One the household reviewed among them is counted apart and is still no
	// history.
	mixed := domain.VoteCategory(append(rows(2, ""), rows(1, "", reviewed)...), 0.85)
	require.Equal(t, domain.CategoryVerdictNoHistory, mixed.Kind)
	require.Equal(t, 1, mixed.LeftAlone)
	require.Equal(t, 2, mixed.AwaitingReview)
}

func TestRowsThatArrivedReviewedAreCountedApart(t *testing.T) {
	// Six fund purchases imported already reviewed, two the household
	// reviewed itself, one waiting: none has a category.
	evidence := append(rows(6, "", arrivedReviewed), rows(2, "", reviewed)...)
	evidence = append(evidence, rows(1, "")...)
	verdict := domain.VoteCategory(evidence, 0.85)
	require.Equal(t, domain.CategoryVerdictNoHistory, verdict.Kind)
	require.Equal(t, 6, verdict.ArrivedReviewed)
	require.Equal(t, 2, verdict.LeftAlone)
	require.Equal(t, 1, verdict.AwaitingReview)
	require.Equal(t, 9, verdict.Uncategorized)
}

func TestOnlyTheHouseholdsReviewWeighsMore(t *testing.T) {
	// One row each for "a" and "b", same similarity and age. Reviewed by the
	// household, "b" weighs 1.25 to 1: share 1.25/2.25, count 1/5, coverage 1.
	household := domain.VoteCategory(append(rows(1, "a"), rows(1, "b", reviewed)...), 0.85)
	require.Equal(t, "b", household.CategoryID)
	require.InDelta(t, 1.25/2.25*0.2, household.Confidence, 0.0001)

	// Arrived reviewed, it weighs 1: a tie, which "a" takes by id, share 0.5.
	arrived := domain.VoteCategory(append(rows(1, "a"), rows(1, "b", arrivedReviewed)...), 0.85)
	require.Equal(t, "a", arrived.CategoryID)
	require.InDelta(t, 0.1, arrived.Confidence, 0.0001)
}

func TestReviewedByHouseholdIsSomebodyHereSettingTheFlag(t *testing.T) {
	cases := []struct {
		name     string
		reviewed bool
		source   domain.Source
		account  domain.AccountKind
		want     bool
	}{
		{"synced card charge reviewed in the app", true, domain.SourceSync, domain.KindCreditCard, true},
		{"hand-entered checking row", true, domain.SourceManual, domain.KindCash, true},
		{"not reviewed", false, domain.SourceSync, domain.KindCash, false},
		{"Simplifi import carries the flag", true, domain.SourceSimplifiImport, domain.KindCash, false},
		{"file import carries the flag", true, domain.SourceFileImport, domain.KindCreditCard, false},
		{"investment rows are born reviewed", true, domain.SourceSync, domain.KindInvestment, false},
		{"loan rows are born reviewed", true, domain.SourceManual, domain.KindLoan, false},
		{"asset rows are born reviewed", true, domain.SourceSync, domain.KindAsset, false},
	}
	for _, one := range cases {
		require.Equal(t, one.want,
			domain.ReviewedByHousehold(one.reviewed, one.source, one.account), one.name)
	}
}

func TestTheRowBeingDecidedIsReviewedWhereverItWasReviewed(t *testing.T) {
	require.True(t, domain.ReviewedCategoryStands(true, domain.KindCash))
	require.True(t, domain.ReviewedCategoryStands(true, domain.KindCreditCard))
	require.False(t, domain.ReviewedCategoryStands(false, domain.KindCash))
	require.False(t, domain.ReviewedCategoryStands(true, domain.KindInvestment),
		"born reviewed is nobody's decision")
	require.False(t, domain.ReviewedCategoryStands(true, domain.KindLoan))
}

func TestARowsOwnCategoryIsWeighedNeverTakenAsFact(t *testing.T) {
	require.Equal(t, "", domain.CategoryStanding(false, true))
	require.Equal(t, "", domain.CategoryStanding(false, false))
	require.Equal(t, domain.CategoryStandingReviewed, domain.CategoryStanding(true, true))
	require.Equal(t, domain.CategoryStandingUnreviewed, domain.CategoryStanding(true, false))
	require.Contains(t, domain.CategoryStandingReviewed, "only when the evidence plainly shows it is wrong")
	require.Contains(t, domain.CategoryStandingUnreviewed, "not as the answer")
}

func TestATransferLegIsNeitherLeftAloneNorAwaitingReview(t *testing.T) {
	// One reviewed transfer leg among three unreviewed rows: under the
	// transfer share, so still a vote, and the leg is counted as a transfer
	// rather than as the household leaving the payee alone.
	evidence := append(rows(3, ""), rows(1, "", transfer, reviewed)...)
	verdict := domain.VoteCategory(evidence, 0.85)
	require.Equal(t, domain.CategoryVerdictNoHistory, verdict.Kind)
	require.Equal(t, 1, verdict.Transfers)
	require.Equal(t, 4, verdict.Uncategorized)
	require.Equal(t, 3, verdict.AwaitingReview)
	require.Zero(t, verdict.LeftAlone)
}

func TestUnreviewedRowsBesideACategorizedOneAreCountedApart(t *testing.T) {
	// One visit filed under Fast Food, one reviewed and kept uncategorized,
	// three still waiting. The leading category stands; the counts tell the
	// model which uncategorized rows were a decision.
	evidence := append(rows(1, "fast-food"), rows(1, "", reviewed)...)
	evidence = append(evidence, rows(3, "")...)
	verdict := domain.VoteCategory(evidence, 0.85)
	require.Equal(t, domain.CategoryVerdictUncertain, verdict.Kind)
	require.Equal(t, "fast-food", verdict.CategoryID)
	require.Equal(t, 4, verdict.Uncategorized)
	require.Equal(t, 1, verdict.LeftAlone)
	require.Equal(t, 3, verdict.AwaitingReview)
	// Share 1, one row of five for count, one voter of five for coverage.
	require.InDelta(t, 0.04, verdict.Confidence, 0.001)
}

func TestASplitHistoryIsUncertainAndShowsTheSplit(t *testing.T) {
	evidence := append(rows(6, "loans"), rows(5, "investment")...)
	verdict := domain.VoteCategory(evidence, 0.85)
	require.Equal(t, domain.CategoryVerdictUncertain, verdict.Kind)
	require.Equal(t, "loans", verdict.CategoryID, "the larger side still leads")
	require.Less(t, verdict.Confidence, 0.85)
	require.Len(t, verdict.Shares, 2)
	require.InDelta(t, 1.0, verdict.Shares[0].Share+verdict.Shares[1].Share, 0.001)
}

func TestThinAgreementIsDiscounted(t *testing.T) {
	// One reviewed row agreeing with itself is unanimous and not convincing.
	one := domain.VoteCategory(rows(1, "groceries"), 0.85)
	require.Equal(t, domain.CategoryVerdictUncertain, one.Kind)
	require.InDelta(t, 0.2, one.Confidence, 0.001)

	five := domain.VoteCategory(rows(5, "groceries"), 0.85)
	require.Equal(t, domain.CategoryVerdictConfident, five.Kind)
}

func TestMostlyUncategorizedHistoryIsDiscounted(t *testing.T) {
	// Five agreeing rows among forty never categorized: the model is asked.
	evidence := append(rows(5, "fees"), rows(35, "")...)
	verdict := domain.VoteCategory(evidence, 0.85)
	require.Equal(t, domain.CategoryVerdictUncertain, verdict.Kind)
	require.Equal(t, "fees", verdict.CategoryID)
	require.InDelta(t, 0.125, verdict.Confidence, 0.001)
}

func TestRecentRowsOutweighOldOnes(t *testing.T) {
	// Six old rows say A, four recent ones say B: B leads, and nothing is
	// confident enough to act on without asking.
	evidence := append(rows(6, "a", old), rows(4, "b")...)
	verdict := domain.VoteCategory(evidence, 0.85)
	require.Equal(t, "b", verdict.CategoryID)
	require.Equal(t, domain.CategoryVerdictUncertain, verdict.Kind)
}

func TestAZeroThresholdNeverActsAlone(t *testing.T) {
	verdict := domain.VoteCategory(rows(12, "interest-earned"), 0)
	require.Equal(t, domain.CategoryVerdictUncertain, verdict.Kind)
	require.InDelta(t, 1.0, verdict.Confidence, 0.001)
}

func TestAWinnerRunningTheWrongWayForTheMoneyDoesNotActAlone(t *testing.T) {
	// Dividends filed under Fast Food: unanimous and wrong. A positive amount
	// in an expense category stops the vote acting alone.
	confident := domain.VoteCategory(rows(12, "fast-food"), 0.85)
	require.Equal(t, domain.CategoryVerdictConfident, confident.Kind)

	checked := confident.WithKindCheck(true, false)
	require.Equal(t, domain.CategoryVerdictUncertain, checked.Kind)
	require.True(t, checked.KindConflict)
	require.Equal(t, "fast-food", checked.CategoryID, "the leading category is still named")

	// Money out into an expense category is the ordinary case and untouched.
	require.Equal(t, domain.CategoryVerdictConfident, confident.WithKindCheck(false, false).Kind)
	// Money in into an income category likewise.
	require.Equal(t, domain.CategoryVerdictConfident, confident.WithKindCheck(true, true).Kind)
}

// The refund vote: a credit at a shop is decided by what was bought there.

func charge(e *domain.CategoryEvidence) { e.IsCharge = true }

// purchase is a charge domain.OriginalPurchaseAge found to be the credit's
// purchase, ageDays before it.
func purchase(id string, ageDays int) func(*domain.CategoryEvidence) {
	return func(e *domain.CategoryEvidence) {
		e.IsCharge, e.IsPurchase, e.TxnID, e.PurchaseAgeDays = true, true, id, ageDays
	}
}

func TestACreditAtAShopTakesTheCategoryOfItsPurchases(t *testing.T) {
	// Six purchases under Home Improvement and a $25 credit matching none of
	// them to the cent: the charges vote, and money coming back to a spending
	// category is a refund, not a history running the wrong way.
	verdict, merchant := domain.VoteRefundCategory(rows(6, "home", charge), 0.85)
	require.True(t, merchant)
	require.True(t, verdict.IsRefund)
	require.Equal(t, "home", verdict.CategoryID)
	require.Empty(t, verdict.RefundOf)
	require.Equal(t, domain.CategoryVerdictConfident, verdict.Kind)

	checked := verdict.WithKindCheck(true, false)
	require.Equal(t, domain.CategoryVerdictConfident, checked.Kind)
	require.False(t, checked.KindConflict)
}

func TestAPurchaseOfTheSameAmountNamesTheCategory(t *testing.T) {
	// Five Home Improvement charges and one Garden charge for exactly the
	// credit's amount twelve days before it. The purchase being given back
	// wins over the merchant's usual category.
	evidence := append(rows(5, "home", charge),
		rows(1, "garden", purchase("t-rake", 12))...)
	verdict, merchant := domain.VoteRefundCategory(evidence, 0.85)
	require.True(t, merchant)
	require.Equal(t, "garden", verdict.CategoryID)
	require.Equal(t, "t-rake", verdict.RefundOf)
	require.Equal(t, domain.CategoryVerdictConfident, verdict.Kind)
}

func TestTheNearestSameAmountPurchaseIsTheOneGivenBack(t *testing.T) {
	evidence := append(rows(1, "garden", purchase("t-older", 40)),
		rows(1, "tools", purchase("t-newer", 9))...)
	evidence = append(evidence, rows(1, "home", purchase("t-same-day-later-id", 9))...)
	verdict, _ := domain.VoteRefundCategory(evidence, 0.85)
	require.Equal(t, "t-newer", verdict.RefundOf)
	require.Equal(t, "tools", verdict.CategoryID)
}

func TestAnUncategorizedSameAmountChargeNamesNothing(t *testing.T) {
	evidence := append(rows(5, "home", charge),
		rows(1, "", purchase("t-unfiled", 5))...)
	verdict, _ := domain.VoteRefundCategory(evidence, 0.85)
	require.Empty(t, verdict.RefundOf)
	require.Equal(t, "home", verdict.CategoryID)
}

func TestAPayeeThatMostlyPaysInIsNotAMerchant(t *testing.T) {
	// An employer with one reimbursed expense among its paychecks: the
	// ordinary vote, and its direction check, decide.
	evidence := append(rows(6, "salary"), rows(1, "office", purchase("t-lunch", 3))...)
	_, merchant := domain.VoteRefundCategory(evidence, 0.85)
	require.False(t, merchant)

	// Charges and credits level is not a merchant either.
	level := append(rows(2, "salary"), rows(2, "office", charge)...)
	_, merchant = domain.VoteRefundCategory(level, 0.85)
	require.False(t, merchant)

	// Nor is a payee with no charges at all — the dividends filed under Fast
	// Food keep their direction conflict.
	_, merchant = domain.VoteRefundCategory(rows(12, "fast-food"), 0.85)
	require.False(t, merchant)
}

func TestTransferLegsDoNotMakeAMerchant(t *testing.T) {
	evidence := append(rows(4, "", transfer, charge), rows(1, "salary")...)
	_, merchant := domain.VoteRefundCategory(evidence, 0.85)
	require.False(t, merchant)
}

func TestAZeroThresholdKeepsAClearPurchaseFromActingAlone(t *testing.T) {
	evidence := rows(1, "garden", purchase("t-rake", 12))
	verdict, _ := domain.VoteRefundCategory(evidence, 0)
	require.Equal(t, "garden", verdict.CategoryID)
	require.Equal(t, domain.CategoryVerdictUncertain, verdict.Kind)
}

func TestARefundIntoChargesFiledAsIncomeIsStillAConflict(t *testing.T) {
	verdict, _ := domain.VoteRefundCategory(rows(6, "salary", charge), 0.85)
	checked := verdict.WithKindCheck(true, true)
	require.Equal(t, domain.CategoryVerdictUncertain, checked.Kind)
	require.True(t, checked.KindConflict)
}

func TestAFullyFiledSplitRowIsNotUncategorizedHistory(t *testing.T) {
	// Four payments filed under Gifts and two the household split and filed
	// part by part, then reviewed. The split rows are not "reviewed and kept
	// uncategorized", and they do not dilute the four that agree.
	evidence := append(rows(4, "gifts"), rows(2, "", split, reviewed)...)
	verdict := domain.VoteCategory(evidence, 0.85)
	require.Equal(t, "gifts", verdict.CategoryID)
	require.Equal(t, 2, verdict.Splits)
	require.Zero(t, verdict.Uncategorized)
	require.Zero(t, verdict.LeftAlone)
	require.Equal(t, 4, verdict.Voters)
	// Share 1, four rows of five for count, four voters of four for coverage.
	require.InDelta(t, 0.8, verdict.Confidence, 0.001)
}

func TestASplitRowWithAnUnfiledPartIsUncategorizedHistory(t *testing.T) {
	// A split with a part still unfiled arrives as plain uncategorized
	// evidence: no category and not IsSplit.
	evidence := append(rows(4, "gifts"), rows(1, "", reviewed)...)
	verdict := domain.VoteCategory(evidence, 0.85)
	require.Zero(t, verdict.Splits)
	require.Equal(t, 1, verdict.Uncategorized)
	require.Equal(t, 1, verdict.LeftAlone)
}

func TestOnlySplitRowsAreNoHistory(t *testing.T) {
	verdict := domain.VoteCategory(rows(3, "", split, reviewed), 0.85)
	require.Equal(t, domain.CategoryVerdictNoHistory, verdict.Kind)
	require.Equal(t, 3, verdict.Splits)
	require.Zero(t, verdict.Uncategorized)
	require.Zero(t, verdict.LeftAlone)
}
