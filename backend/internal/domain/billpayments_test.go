package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func payment(ref, on, amount string) domain.BillPaymentFacts {
	return domain.BillPaymentFacts{Ref: ref, PaidOn: day(on), Amount: amt(amount)}
}

func bankRow(ref, on, amount string) domain.BillPaymentRowFacts {
	return domain.BillPaymentRowFacts{Ref: ref, On: day(on), Amount: amt(amount)}
}

func TestAPaymentIsTheOneOutflowOfItsAmountInTheWeekAfter(t *testing.T) {
	matched := domain.MatchBillPayments(
		[]domain.BillPaymentFacts{payment("p1", "2026-03-10", "137.00")},
		[]domain.BillPaymentRowFacts{
			bankRow("other", "2026-03-11", "-18.00"),
			bankRow("card", "2026-03-12", "-137.00"),
		})
	require.Equal(t, map[string]string{"p1": "card"}, matched)
}

func TestAPaymentSpanIsTwoDaysBeforeToFiveAfter(t *testing.T) {
	paid := payment("p1", "2026-03-10", "60.00")
	for on, want := range map[string]bool{
		"2026-03-07": false,
		"2026-03-08": true,
		"2026-03-10": true,
		"2026-03-15": true,
		"2026-03-16": false,
	} {
		require.Equalf(t, want, domain.RowCouldBeBillPayment(paid, bankRow("r", on, "-60.00")), "posted %s", on)
	}
}

func TestOnlyMoneyOutOfExactlyThatAmountIsAPayment(t *testing.T) {
	paid := payment("p1", "2026-03-10", "60.00")
	require.False(t, domain.RowCouldBeBillPayment(paid, bankRow("refund", "2026-03-10", "60.00")))
	require.False(t, domain.RowCouldBeBillPayment(paid, bankRow("near", "2026-03-10", "-60.01")))
	require.True(t, domain.RowCouldBeBillPayment(paid, bankRow("exact", "2026-03-10", "-60")))
}

func TestTwoRowsOfThePaymentsAmountMatchNeither(t *testing.T) {
	matched := domain.MatchBillPayments(
		[]domain.BillPaymentFacts{payment("p1", "2026-03-10", "25.00")},
		[]domain.BillPaymentRowFacts{
			bankRow("clinic", "2026-03-10", "-25.00"),
			bankRow("lunch", "2026-03-12", "-25.00"),
		})
	require.Empty(t, matched)
}

func TestTwoPaymentsAfterOneRowMatchNeither(t *testing.T) {
	matched := domain.MatchBillPayments(
		[]domain.BillPaymentFacts{payment("p1", "2026-03-10", "25.00"), payment("p2", "2026-03-12", "25.00")},
		[]domain.BillPaymentRowFacts{bankRow("only", "2026-03-12", "-25.00")})
	require.Empty(t, matched)
}

func TestARowTwoPaymentsCouldBeIsNeithersThoughOneHasNoOtherRow(t *testing.T) {
	// p1's only row is "early", but p2 could be "early" too: if p1 was paid
	// from an account the bank feed never sees, "early" is p2's and "late" is
	// somebody else's forty dollars.
	matched := domain.MatchBillPayments(
		[]domain.BillPaymentFacts{payment("p2", "2026-03-06", "40.00"), payment("p1", "2026-03-01", "40.00")},
		[]domain.BillPaymentRowFacts{bankRow("early", "2026-03-04", "-40.00"), bankRow("late", "2026-03-09", "-40.00")})
	require.Empty(t, matched)
}

func TestTwoPaymentsAWeekApartEachMatchTheirOwnRow(t *testing.T) {
	matched := domain.MatchBillPayments(
		[]domain.BillPaymentFacts{payment("p1", "2026-03-01", "40.00"), payment("p2", "2026-03-12", "40.00")},
		[]domain.BillPaymentRowFacts{bankRow("first", "2026-03-02", "-40.00"), bankRow("second", "2026-03-13", "-40.00")})
	require.Equal(t, map[string]string{"p1": "first", "p2": "second"}, matched)
}

func TestAPaymentOfNothingMatchesNothing(t *testing.T) {
	matched := domain.MatchBillPayments(
		[]domain.BillPaymentFacts{payment("p1", "2026-03-10", "0.00")},
		[]domain.BillPaymentRowFacts{bankRow("zero", "2026-03-10", "0.00")})
	require.Empty(t, matched)
}

func TestAPaymentSettledTheLatestStatementIssuedByItsDay(t *testing.T) {
	statements := []domain.BillIssueFacts{
		{ID: "jan", IssuedOn: day("2026-01-14")},
		{ID: "feb", IssuedOn: day("2026-02-14")},
		{ID: "mar", IssuedOn: day("2026-03-14")},
	}
	require.Equal(t, []domain.ID{"feb"}, domain.StatementsPaidBy(day("2026-03-02"), statements))
	require.Equal(t, []domain.ID{"mar"}, domain.StatementsPaidBy(day("2026-03-14"), statements))
}

func TestAPaymentBeforeAnyStatementSettledNone(t *testing.T) {
	statements := []domain.BillIssueFacts{{ID: "feb", IssuedOn: day("2026-02-14")}, {ID: "undated"}}
	require.Empty(t, domain.StatementsPaidBy(day("2026-02-01"), statements))
}

func TestStatementsIssuedTheSameDayAreAllWhatThePaymentSettled(t *testing.T) {
	statements := []domain.BillIssueFacts{
		{ID: "professional", IssuedOn: day("2026-02-14")},
		{ID: "hospital", IssuedOn: day("2026-02-14")},
	}
	require.Equal(t, []domain.ID{"hospital", "professional"}, domain.StatementsPaidBy(day("2026-02-20"), statements))
}

func TestASiteAddressIsThePortalsHostAndRoot(t *testing.T) {
	for raw, want := range map[string]string{
		"https://mychart.example-health.example/MyChart/Billing/Summary?x=1": "https://mychart.example-health.example/MyChart",
		"mychart.example-health.example/MyChart/":                            "https://mychart.example-health.example/MyChart",
		"HTTP://MyChart.Example-Health.example/mychart-prd#top":              "https://mychart.example-health.example/mychart-prd",
		"https://mychart.example-health.example":                             "https://mychart.example-health.example",
		"https://mychart.example-health.example:443/MyChart":                 "https://mychart.example-health.example/MyChart",
	} {
		got, ok := domain.SiteAddressOf(raw)
		require.Truef(t, ok, "should be usable: %q", raw)
		require.Equalf(t, want, got, "from %q", raw)
	}
}

func TestASiteAddressTheServersBrowserShouldNotVisitIsRefused(t *testing.T) {
	for _, raw := range []string{
		"",
		"example",
		"https://192.0.2.5/MyChart",
		"https://[::1]/MyChart",
		"https://localhost/MyChart",
		"https://router.lan/MyChart",
		"https://nas.home.arpa/MyChart",
		"https://mychart.example-health.example:8443/MyChart",
		"https://someone@mychart.example-health.example/MyChart",
		"ftp://mychart.example-health.example/MyChart",
		"javascript:alert(1)",
		"https://mychart.example-health.example/My%2FChart",
		"https://mychart..example/MyChart",
		"https://mychart.example health.example/MyChart",
	} {
		_, ok := domain.SiteAddressOf(raw)
		require.Falsef(t, ok, "should be refused: %q", raw)
	}
}
