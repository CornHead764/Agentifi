package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Possible duplicates: one charge recorded by two sources. The expected
// values come from the rule in docs/calculations.md.

var checking = ID("checking")

func dupDay(s string) Date {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func copyOf(id string, source Source, on string, amount string) Transaction {
	return Transaction{
		ID:        ID(id),
		AccountID: checking,
		Date:      dupDay(on),
		Amount:    MustFromString(amount),
		Currency:  "USD",
		Source:    source,
	}
}

func TestADuplicateIsTheSameAmountOnTheSameAccountWithinTwoDays(t *testing.T) {
	imported := copyOf("a-import", SourceSimplifiImport, "2026-03-10", "-45.00")
	synced := copyOf("b-sync", SourceSync, "2026-03-12", "-45.00")

	got := FindDuplicateCandidates([]Transaction{synced, imported}, DuplicateOptions{})

	require.Equal(t, []DuplicateCandidate{
		{First: "a-import", Second: "b-sync", DaysApart: 2, Keep: "a-import"},
	}, got)
}

func TestThreeDaysApartIsTwoTransactions(t *testing.T) {
	got := FindDuplicateCandidates([]Transaction{
		copyOf("a", SourceSimplifiImport, "2026-03-10", "-45.00"),
		copyOf("b", SourceSync, "2026-03-13", "-45.00"),
	}, DuplicateOptions{})
	require.Empty(t, got)
}

func TestAnAmountOffByACentIsNotADuplicate(t *testing.T) {
	got := FindDuplicateCandidates([]Transaction{
		copyOf("a", SourceSimplifiImport, "2026-03-10", "-45.00"),
		copyOf("b", SourceSync, "2026-03-10", "-45.01"),
	}, DuplicateOptions{})
	require.Empty(t, got)
}

func TestAChargeAndItsRefundAreNotADuplicate(t *testing.T) {
	got := FindDuplicateCandidates([]Transaction{
		copyOf("a", SourceSimplifiImport, "2026-03-10", "-45.00"),
		copyOf("b", SourceSync, "2026-03-10", "45.00"),
	}, DuplicateOptions{})
	require.Empty(t, got)
}

func TestRowsOfOneSourceAreNeverPaired(t *testing.T) {
	got := FindDuplicateCandidates([]Transaction{
		copyOf("a", SourceSync, "2026-03-10", "-4.50"),
		copyOf("b", SourceSync, "2026-03-10", "-4.50"),
		copyOf("c", SourceSimplifiImport, "2026-03-01", "-4.50"),
		copyOf("d", SourceSimplifiImport, "2026-03-01", "-4.50"),
	}, DuplicateOptions{})
	require.Empty(t, got, "two coffees on one day are two coffees")
}

func TestOtherAccountsAndCurrenciesDoNotPair(t *testing.T) {
	elsewhere := copyOf("b", SourceSync, "2026-03-10", "-45.00")
	elsewhere.AccountID = "savings"
	euros := copyOf("c", SourceSync, "2026-03-10", "-45.00")
	euros.Currency = "EUR"

	got := FindDuplicateCandidates([]Transaction{
		copyOf("a", SourceSimplifiImport, "2026-03-10", "-45.00"), elsewhere, euros,
	}, DuplicateOptions{})
	require.Empty(t, got)
}

func TestRowsThatAreNotRealMoneyAreLeftOut(t *testing.T) {
	deleted := copyOf("b", SourceSync, "2026-03-10", "-45.00")
	deleted.IsDeleted = true
	forecast := copyOf("c", SourceManual, "2026-03-10", "-45.00")
	forecast.IsEstimate = true
	opening := copyOf("d", SourceOpeningBalance, "2026-03-10", "-45.00")
	zero := copyOf("e", SourceSync, "2026-03-10", "0.00")
	zeroImport := copyOf("f", SourceSimplifiImport, "2026-03-10", "0.00")

	got := FindDuplicateCandidates([]Transaction{
		copyOf("a", SourceSimplifiImport, "2026-03-10", "-45.00"),
		deleted, forecast, opening, zero, zeroImport,
	}, DuplicateOptions{})
	require.Empty(t, got)
}

func TestTheWordingIsNotCompared(t *testing.T) {
	imported := copyOf("a", SourceSimplifiImport, "2026-03-10", "-45.00")
	imported.StatementName, imported.Payee = "CORNER MARKET 0042", "Corner Market"
	synced := copyOf("b", SourceSync, "2026-03-11", "-45.00")
	synced.StatementName = "POS DEBIT 0311 CNR MKT"

	require.Len(t, FindDuplicateCandidates([]Transaction{imported, synced}, DuplicateOptions{}), 1)
}

func TestAPairRuledOutIsNeverProposedAgain(t *testing.T) {
	rows := []Transaction{
		copyOf("a", SourceSimplifiImport, "2026-03-10", "-45.00"),
		copyOf("b", SourceSync, "2026-03-10", "-45.00"),
	}
	opts := DuplicateOptions{Distinct: map[[2]ID]bool{DuplicateKey("b", "a"): true}}
	require.Empty(t, FindDuplicateCandidates(rows, opts))
}

func TestARowIsInOnePairAndTheNearestDatesPair(t *testing.T) {
	// One import row and two bank rows of the same amount: the bank row
	// dated the same day is the copy; the other is left alone.
	got := FindDuplicateCandidates([]Transaction{
		copyOf("a", SourceSimplifiImport, "2026-03-10", "-9.99"),
		copyOf("b", SourceSync, "2026-03-12", "-9.99"),
		copyOf("c", SourceSync, "2026-03-10", "-9.99"),
	}, DuplicateOptions{})

	require.Equal(t, []DuplicateCandidate{
		{First: "a", Second: "c", DaysApart: 0, Keep: "a"},
	}, got)
}

func TestRulingOutAPairFreesItsRowsForOthers(t *testing.T) {
	rows := []Transaction{
		copyOf("a", SourceSimplifiImport, "2026-03-10", "-9.99"),
		copyOf("b", SourceSync, "2026-03-12", "-9.99"),
		copyOf("c", SourceSync, "2026-03-10", "-9.99"),
	}
	opts := DuplicateOptions{Distinct: map[[2]ID]bool{DuplicateKey("a", "c"): true}}

	require.Equal(t, []DuplicateCandidate{
		{First: "a", Second: "b", DaysApart: 2, Keep: "a"},
	}, FindDuplicateCandidates(rows, opts))
}

func TestTheResultDoesNotDependOnInputOrder(t *testing.T) {
	rows := []Transaction{
		copyOf("a", SourceSimplifiImport, "2026-03-10", "-9.99"),
		copyOf("b", SourceSync, "2026-03-11", "-9.99"),
		copyOf("c", SourceSimplifiImport, "2026-03-12", "-9.99"),
		copyOf("d", SourceSync, "2026-03-11", "-9.99"),
	}
	want := FindDuplicateCandidates(rows, DuplicateOptions{})
	require.Len(t, want, 2)

	reversed := []Transaction{rows[3], rows[2], rows[1], rows[0]}
	require.Equal(t, want, FindDuplicateCandidates(reversed, DuplicateOptions{}))
}

func TestOnlyPairsTouchingACandidateRowAreProposed(t *testing.T) {
	rows := []Transaction{
		copyOf("a", SourceSimplifiImport, "2026-03-10", "-45.00"),
		copyOf("b", SourceSync, "2026-03-10", "-45.00"),
		copyOf("c", SourceSimplifiImport, "2026-02-10", "-12.00"),
		copyOf("d", SourceSync, "2026-02-10", "-12.00"),
	}

	got := FindDuplicateCandidates(rows, DuplicateOptions{CandidateIDs: []ID{"b"}})
	require.Len(t, got, 1)
	require.Equal(t, ID("a"), got[0].First)

	require.Empty(t, FindDuplicateCandidates(rows, DuplicateOptions{CandidateIDs: []ID{}}))
}

func TestTheCopyKeptIsTheOneNotWrittenByTheAggregator(t *testing.T) {
	cases := []struct {
		name     string
		a, b     Transaction
		wantKeep ID
	}{
		{"import over sync",
			copyOf("a", SourceSync, "2026-03-10", "-5.00"),
			copyOf("b", SourceSimplifiImport, "2026-03-11", "-5.00"), "b"},
		{"manual over sync",
			copyOf("a", SourceManual, "2026-03-11", "-5.00"),
			copyOf("b", SourceSync, "2026-03-10", "-5.00"), "a"},
		{"the earlier of two non-sync rows",
			copyOf("a", SourceFileImport, "2026-03-11", "-5.00"),
			copyOf("b", SourceSimplifiImport, "2026-03-10", "-5.00"), "b"},
		{"the lower id on a tie",
			copyOf("b", SourceFileImport, "2026-03-10", "-5.00"),
			copyOf("a", SourceSimplifiImport, "2026-03-10", "-5.00"), "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FindDuplicateCandidates([]Transaction{c.a, c.b}, DuplicateOptions{})
			require.Len(t, got, 1)
			require.Equal(t, c.wantKeep, got[0].Keep)
		})
	}
}
