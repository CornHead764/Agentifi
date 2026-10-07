package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Expected answers are what a person reading the statement line would say;
// the wordings are invented in real brokerages' shapes. The one that matters
// most is the dividend misfiled as Fast Food.

func activityPosting(statement, payee, amount string) Posting {
	return Posting{Txn: Transaction{
		ID:            "txn-1",
		AccountID:     "acct-brokerage",
		Date:          NewDate(2026, time.March, 4),
		StatementName: statement,
		Payee:         payee,
		Amount:        MustFromString(amount),
	}}
}

func withCategory(p Posting, name string, kind CategoryKind) Posting {
	p.Category = Category{ID: "cat-1", Name: name, Kind: kind}
	p.HasCategory = true
	return p
}

func TestTheBanksOwnWordingNamesTheRow(t *testing.T) {
	cases := map[string]struct {
		statement string
		amount    string
		expected  ActivityKind
	}{
		"a purchase":     {"YOU BOUGHT EXAMPLE TOTAL MKT", "-1200.00", ActivityBuy},
		"a sale":         {"YOU SOLD 4 SHARES", "980.00", ActivitySell},
		"a dividend":     {"DIVIDEND RECEIVED ACME", "31.00", ActivityDividend},
		"a capital gain": {"LONG TERM CAPITAL GAIN", "12.00", ActivityDividend},
		"interest":       {"INTEREST EARNED", "0.50", ActivityInterest},
		"a fee":          {"MANAGEMENT FEE Q1", "-14.00", ActivityFee},
		"a commission":   {"COMMISSION ON TRADE", "-5.00", ActivityFee},
		"a contribution": {"EMPLOYEE CONTRIBUTION", "500.00", ActivityContribution},
		"a withdrawal":   {"WITHDRAWAL TO CHECKING", "-750.00", ActivityWithdrawal},
	}
	for name, one := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, one.expected,
				ClassifyActivity(activityPosting(one.statement, "", one.amount)))
		})
	}
}

func TestAReinvestedDividendIsAReinvestmentNotADividend(t *testing.T) {
	// Both words are on the line; "reinvest" is tested first.
	require.Equal(t, ActivityReinvestment,
		ClassifyActivity(activityPosting("DIVIDEND REINVESTMENT ACME FUND", "", "-31.00")))
}

func TestAShortWordOnlyCountsWhenItStandsAlone(t *testing.T) {
	// "DIVERSIFIED" contains div and "INTERNATIONAL" contains int; both are
	// fund names, not distributions.
	require.Equal(t, ActivityUnknown,
		ClassifyActivity(activityPosting("DIVERSIFIED INTERNATIONAL FEEDER", "", "-500.00")))
	require.Equal(t, ActivityDividend,
		ClassifyActivity(activityPosting("DIV/INT PAYMENT", "", "18.00")))
}

func TestThePayeeIsReadWhenTheStatementSaysNothing(t *testing.T) {
	require.Equal(t, ActivitySell,
		ClassifyActivity(activityPosting("", "Sell order settled", "402.00")))
}

func TestAPairedLegIsAContributionOrAWithdrawalWhateverItIsCalled(t *testing.T) {
	in := activityPosting("ACH FROM EVERYDAY", "", "400.00")
	in.Txn.TransferPairID = "txn-other"
	require.Equal(t, ActivityContribution, ClassifyActivity(in))

	out := activityPosting("ACH TO EVERYDAY", "", "-400.00")
	out.Txn.TransferPairID = "txn-other"
	require.Equal(t, ActivityWithdrawal, ClassifyActivity(out))
}

func TestADividendFiledUnderFastFoodIsStillADividend(t *testing.T) {
	// The category name is never read. The wording is.
	posting := withCategory(
		activityPosting("DIVIDEND RECEIVED", "Brokerage", "31.00"), "Fast Food", CategoryExpense)
	require.Equal(t, ActivityDividend, ClassifyActivity(posting))
}

func TestAnExpenseCategoryNeverNamesAPositiveRow(t *testing.T) {
	// Nothing in the wording, an expense category, money in: unknown.
	posting := withCategory(
		activityPosting("BKG SVC LLC", "Brokerage", "31.00"), "Fast Food", CategoryExpense)
	require.Equal(t, ActivityUnknown, ClassifyActivity(posting))
}

func TestAnIncomeCategoryNamesAPositiveRowOnlyWhenNothingElseDid(t *testing.T) {
	posting := withCategory(
		activityPosting("BKG SVC LLC", "Brokerage", "31.00"), "Investment Income", CategoryIncome)
	require.Equal(t, ActivityDividend, ClassifyActivity(posting))
}

func TestAnUnrecognizedRowIsUnknownRatherThanGuessedFromItsSign(t *testing.T) {
	require.Equal(t, ActivityUnknown,
		ClassifyActivity(activityPosting("WIRE 8841", "", "-2000.00")))
}

func TestASummaryTotalsEachKindAtTheLedgersOwnSign(t *testing.T) {
	rows := ClassifyActivities([]Posting{
		activityPosting("DIVIDEND RECEIVED", "", "31.00"),
		activityPosting("DIVIDEND RECEIVED", "", "19.00"),
		activityPosting("MANAGEMENT FEE", "", "-14.00"),
		activityPosting("WIRE 8841", "", "-2000.00"),
	})
	summary := SummarizeActivity(rows)

	// Dividends first, then fees, then the unnamed row: ActivityKinds order.
	require.Len(t, summary, 3)
	require.Equal(t, ActivityDividend, summary[0].Kind)
	require.Equal(t, 2, summary[0].Count)
	require.Equal(t, "50.00", summary[0].Total.String())
	require.Equal(t, ActivityFee, summary[1].Kind)
	require.Equal(t, "-14.00", summary[1].Total.String())
	require.Equal(t, ActivityUnknown, summary[2].Kind)
	require.Equal(t, "-2000.00", summary[2].Total.String())
}

func TestAKindWithNoRowsIsAbsentRatherThanZero(t *testing.T) {
	summary := SummarizeActivity(ClassifyActivities([]Posting{
		activityPosting("INTEREST EARNED", "", "0.50"),
	}))
	require.Len(t, summary, 1)
	require.Equal(t, ActivityInterest, summary[0].Kind)
}

func TestIncomeCountsTheDividendsThatWereReinvested(t *testing.T) {
	// $31.00 paid out, $0.50 of interest, and $75.00 that bought shares
	// instead of landing as cash: $106.50 earned, of which only $31.50 arrived
	// as money.
	rows := ClassifyActivities([]Posting{
		activityPosting("DIVIDEND RECEIVED", "", "31.00"),
		activityPosting("INTEREST EARNED", "", "0.50"),
		activityPosting("DIVIDEND REINVESTMENT", "", "75.00"),
		activityPosting("YOU BOUGHT ACME", "", "-1200.00"),
	})
	require.Equal(t, "106.50", InvestmentIncome(rows).String())
}

func TestFeesAreReportedAsAMagnitude(t *testing.T) {
	rows := ClassifyActivities([]Posting{
		activityPosting("MANAGEMENT FEE", "", "-14.00"),
		activityPosting("COMMISSION", "", "-5.00"),
		activityPosting("DIVIDEND RECEIVED", "", "31.00"),
	})
	require.Equal(t, "19.00", InvestmentFees(rows).String())
}
