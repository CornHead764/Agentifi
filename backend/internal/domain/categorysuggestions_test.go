package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestARunIsClassifiedByWhatItDidToTheRow(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome domain.CategoryCheckOutcome
		want    domain.CategoryCheckResult
	}{
		{"a proposal on a categorized row",
			domain.CategoryCheckOutcome{Status: domain.AutomationRunSucceeded, Actions: 1},
			domain.CheckDiffers},
		{"a proposal on an uncategorized row",
			domain.CategoryCheckOutcome{Status: domain.AutomationRunSucceeded, Actions: 1, NeedsCategory: true},
			domain.CheckSuggested},
		{"history that names the row's own category",
			domain.CategoryCheckOutcome{Status: domain.AutomationRunSkipped, Settled: true, Agrees: true},
			domain.CheckAgreed},
		{"a paired transfer leg",
			domain.CategoryCheckOutcome{Status: domain.AutomationRunSkipped, Settled: true},
			domain.CheckSkipped},
		{"no answer on an uncategorized row",
			domain.CategoryCheckOutcome{Status: domain.AutomationRunSucceeded, NeedsCategory: true},
			domain.CheckUndetermined},
		{"no answer on a categorized row",
			domain.CategoryCheckOutcome{Status: domain.AutomationRunSucceeded},
			domain.CheckUnsure},
		{"a failure, whatever else it reports",
			domain.CategoryCheckOutcome{Status: domain.AutomationRunFailed, Actions: 1, Agrees: true},
			domain.CheckFailed},
	} {
		require.Equal(t, tc.want, domain.CategoryCheckResultOf(tc.outcome), tc.name)
	}
}

func TestABatchComparesItsAnswersWithTheRowsReviewedAndNot(t *testing.T) {
	// Nine rows named. Two reviewed rows agree and one differs; among the
	// unreviewed, one agrees, one differs, one had no category and got a
	// suggestion, one failed. One row is still waiting and one never ran.
	runs := []domain.SuggestionBatchRun{
		{TransactionID: "r1", Reviewed: true, Status: domain.AutomationRunSkipped, Result: domain.CheckAgreed},
		{TransactionID: "r2", Reviewed: true, Status: domain.AutomationRunSucceeded, Result: domain.CheckAgreed},
		{TransactionID: "r3", Reviewed: true, Status: domain.AutomationRunSucceeded, Result: domain.CheckDiffers},
		{TransactionID: "u1", Status: domain.AutomationRunSkipped, Result: domain.CheckAgreed},
		{TransactionID: "u2", Status: domain.AutomationRunSucceeded, Result: domain.CheckDiffers},
		{TransactionID: "u3", Status: domain.AutomationRunSucceeded, Result: domain.CheckSuggested},
		{TransactionID: "u4", Status: domain.AutomationRunFailed, Result: domain.CheckFailed},
		{TransactionID: "w1", Status: domain.AutomationRunQueued},
	}

	got := domain.SummarizeSuggestionBatch(9, runs)
	require.Equal(t, domain.SuggestionBatchSummary{
		Rows: 9, Pending: 1, NotRun: 1,
		Reviewed:   domain.SuggestionTally{Agreed: 2, Differs: 1},
		Unreviewed: domain.SuggestionTally{Agreed: 1, Differs: 1, Suggested: 1, Failed: 1},
	}, got)
	require.Equal(t, 8, got.Done())
	require.False(t, got.Finished())
}

func TestARowWithTwoRunsIsCountedOnceByItsMostTellingResult(t *testing.T) {
	// A household with a second transaction automation runs both on each row.
	// A disagreement outranks an agreement, and a row still waiting on either
	// run is pending.
	runs := []domain.SuggestionBatchRun{
		{TransactionID: "a", Status: domain.AutomationRunSkipped, Result: domain.CheckAgreed},
		{TransactionID: "a", Status: domain.AutomationRunSucceeded, Result: domain.CheckDiffers},
		{TransactionID: "b", Status: domain.AutomationRunSucceeded, Result: domain.CheckUnsure},
		{TransactionID: "b", Status: domain.AutomationRunFailed, Result: domain.CheckFailed},
		{TransactionID: "c", Status: domain.AutomationRunSkipped, Result: domain.CheckAgreed},
		{TransactionID: "c", Status: domain.AutomationRunRunning},
	}

	got := domain.SummarizeSuggestionBatch(3, runs)
	require.Equal(t, domain.SuggestionBatchSummary{
		Rows: 3, Pending: 1,
		Unreviewed: domain.SuggestionTally{Differs: 1, Unsure: 1},
	}, got)
}

func TestACancelledBatchIsFinishedWithTheRestNotRun(t *testing.T) {
	runs := []domain.SuggestionBatchRun{
		{TransactionID: "a", Reviewed: true, Status: domain.AutomationRunSkipped, Result: domain.CheckAgreed},
	}
	got := domain.SummarizeSuggestionBatch(500, runs)
	require.Equal(t, 499, got.NotRun)
	require.Equal(t, 500, got.Done())
	require.True(t, got.Finished())
}
