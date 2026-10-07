package domain

// A batch of category suggestions doubles as a comparison: run over rows that
// already carry a category, it says how often the check would have filed them
// the same way. Each finished run is reduced to one result when it finishes,
// and a batch is summarized from those results per row.

// BulkSuggestionRows is the most rows a batch may name and not be bulk. A
// person is asked before a bulk batch is queued, and its runs wait behind
// every other run.
const BulkSuggestionRows = 200

// CategoryCheckResult is what one run of a transaction automation concluded
// about its row's category.
type CategoryCheckResult string

const (
	// CheckAgreed is a row with a category the check named itself.
	CheckAgreed CategoryCheckResult = "agreed"
	// CheckDiffers is a row with a category the check proposed or applied
	// another for.
	CheckDiffers CategoryCheckResult = "differs"
	// CheckUnsure is a row with a category the check reached no answer about.
	CheckUnsure CategoryCheckResult = "unsure"
	// CheckSuggested is a row needing a category the check proposed or
	// applied one for.
	CheckSuggested CategoryCheckResult = "suggested"
	// CheckUndetermined is a row needing a category the check could not place.
	CheckUndetermined CategoryCheckResult = "undetermined"
	// CheckSkipped is a row the check settled without comparing a category: a
	// paired transfer leg, or a split with every part filed.
	CheckSkipped CategoryCheckResult = "skipped"
	CheckFailed  CategoryCheckResult = "failed"
)

// CategoryCheckOutcome is what a finished run reports, reduced to what decides
// its result.
type CategoryCheckOutcome struct {
	Status  string
	Actions int
	// Settled is a run that reached an answer without proposing anything.
	Settled bool
	// Agrees is a settled run whose answer was the row's own category.
	Agrees bool
	// NeedsCategory is the row as the run found it: uncategorized, or a split
	// with a part uncategorized.
	NeedsCategory bool
}

// CategoryCheckResultOf classifies one finished run. Actions decide over the
// run's status, because a proposal is a disagreement whether the history or
// the model made it.
func CategoryCheckResultOf(outcome CategoryCheckOutcome) CategoryCheckResult {
	switch {
	case outcome.Status == AutomationRunFailed:
		return CheckFailed
	case outcome.Actions > 0 && outcome.NeedsCategory:
		return CheckSuggested
	case outcome.Actions > 0:
		return CheckDiffers
	case outcome.Agrees:
		return CheckAgreed
	case outcome.Settled:
		return CheckSkipped
	case outcome.NeedsCategory:
		return CheckUndetermined
	}
	return CheckUnsure
}

// SuggestionBatchRun is one run of a batch, as its summary reads it.
type SuggestionBatchRun struct {
	TransactionID ID
	// Reviewed is the row's review state when the batch was queued, so
	// accepting a suggestion part-way through does not move the row between
	// the two halves of the comparison.
	Reviewed bool
	Status   string
	// Result is empty until the run finishes.
	Result CategoryCheckResult
}

// SuggestionTally counts a batch's finished rows by result.
type SuggestionTally struct {
	Agreed       int `json:"agreed"`
	Differs      int `json:"differs"`
	Unsure       int `json:"unsure"`
	Suggested    int `json:"suggested"`
	Undetermined int `json:"undetermined"`
	Skipped      int `json:"skipped"`
	Failed       int `json:"failed"`
}

func (t *SuggestionTally) add(result CategoryCheckResult) {
	switch result {
	case CheckAgreed:
		t.Agreed++
	case CheckDiffers:
		t.Differs++
	case CheckUnsure:
		t.Unsure++
	case CheckSuggested:
		t.Suggested++
	case CheckUndetermined:
		t.Undetermined++
	case CheckSkipped:
		t.Skipped++
	case CheckFailed:
		t.Failed++
	}
}

// SuggestionBatchSummary is how far a batch has got and how its answers
// compare with the categories the rows already had, split by whether the
// household had reviewed them.
type SuggestionBatchSummary struct {
	// Rows is how many rows the batch named.
	Rows int
	// Pending is the rows with a run still queued or running.
	Pending int
	// NotRun is the rows with no finished run and none waiting: cancelled, taken
	// over by a later request for the same row, or deleted.
	NotRun     int
	Reviewed   SuggestionTally
	Unreviewed SuggestionTally
}

// Done counts every row that will not change again, including those not run.
func (s SuggestionBatchSummary) Done() int { return s.Rows - s.Pending }

// Finished is a batch with nothing left waiting.
func (s SuggestionBatchSummary) Finished() bool { return s.Pending == 0 }

// resultRank decides a row with more than one run (a household with two
// transaction automations): the most telling result stands for the row.
var resultRank = map[CategoryCheckResult]int{
	CheckFailed: 1, CheckSkipped: 2, CheckUndetermined: 3, CheckUnsure: 4,
	CheckAgreed: 5, CheckSuggested: 6, CheckDiffers: 7,
}

// SummarizeSuggestionBatch counts a batch of rows from its runs. A row with any
// run still waiting is pending, whatever its other runs found.
func SummarizeSuggestionBatch(rows int, runs []SuggestionBatchRun) SuggestionBatchSummary {
	type rowState struct {
		pending  bool
		reviewed bool
		result   CategoryCheckResult
	}
	byRow := make(map[ID]*rowState, len(runs))
	for _, run := range runs {
		state := byRow[run.TransactionID]
		if state == nil {
			state = &rowState{reviewed: run.Reviewed}
			byRow[run.TransactionID] = state
		}
		if run.Status == AutomationRunQueued || run.Status == AutomationRunRunning {
			state.pending = true
			continue
		}
		if resultRank[run.Result] > resultRank[state.result] {
			state.result = run.Result
		}
	}

	out := SuggestionBatchSummary{Rows: rows}
	for _, state := range byRow {
		switch {
		case state.pending:
			out.Pending++
		case state.result == "":
			out.NotRun++
		case state.reviewed:
			out.Reviewed.add(state.result)
		default:
			out.Unreviewed.add(state.result)
		}
	}
	if missing := rows - len(byRow); missing > 0 {
		out.NotRun += missing
	}
	return out
}
