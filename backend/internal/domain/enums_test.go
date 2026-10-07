package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The list and the predicate are one answer; series matching reads both.
func TestTheCashFlowListIsExactlyThePredicate(t *testing.T) {
	listed := map[Source]bool{}
	for _, source := range CashFlowSources() {
		listed[source] = true
	}

	for _, source := range AllSources {
		require.Equal(t, source.IsCashFlow(), listed[source],
			"%s is in one answer and not the other", source)
	}
	require.Len(t, CashFlowSources(), len(AllSources)-2,
		"bookkeeping is the only thing left out")
}

// AllSources is what makes the derivation right, so it has to be complete.
func TestEverySourceIsListed(t *testing.T) {
	require.ElementsMatch(t, []Source{
		SourceSync, SourceManual, SourceFileImport, SourceSimplifiImport,
		SourceEmail, SourceOpeningBalance, SourceBalanceAdjustment,
	}, AllSources)
}
