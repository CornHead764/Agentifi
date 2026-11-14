package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The review queue. The expected values come from calculations.md §15.

func reviewRow(category ID) ReviewRow {
	return ReviewRow{Txn: Transaction{ID: "new", CategoryID: category, Amount: MustFromString("-12.00")}}
}

func TestReviewReasonOf(t *testing.T) {
	cases := []struct {
		name string
		row  func() ReviewRow
		want ReviewReason
	}{
		{"categorized looks right", func() ReviewRow { return reviewRow("groceries") }, ""},
		{"no category", func() ReviewRow { return reviewRow("") }, ReviewNoCategory},
		{"undetermined though filed", func() ReviewRow {
			r := reviewRow("groceries")
			r.Undetermined = true
			return r
		}, ReviewNoCategory},
		{"transfer leg needs no category", func() ReviewRow {
			r := reviewRow("")
			r.Txn.TransferPairID = "pair"
			return r
		}, ""},
		{"split with an unfiled part", func() ReviewRow {
			r := reviewRow("")
			r.Txn.Splits = []Split{{CategoryID: "groceries"}, {}}
			return r
		}, ReviewNoCategory},
		{"sure suggestion", func() ReviewRow {
			r := reviewRow("")
			r.HasSuggestion, r.HasConfidence, r.Confidence = true, true, 0.7
			return r
		}, ""},
		{"suggestion under 0.7", func() ReviewRow {
			r := reviewRow("")
			r.HasSuggestion, r.HasConfidence, r.Confidence = true, true, 0.69
			return r
		}, ReviewGuess},
		{"suggestion with no confidence", func() ReviewRow {
			r := reviewRow("groceries")
			r.HasSuggestion = true
			return r
		}, ReviewGuess},
		{"unusual amount", func() ReviewRow {
			r := reviewRow("groceries")
			r.Amount = AmountUnusual
			return r
		}, ReviewUnusualAmount},
		{"not recognised comes first", func() ReviewRow {
			r := reviewRow("")
			r.NotRecognised, r.DuplicateLeg, r.Amount = true, true, AmountUnusual
			return r
		}, ReviewNotRecognised},
		{"duplicate before no category", func() ReviewRow {
			r := reviewRow("")
			r.DuplicateLeg = true
			return r
		}, ReviewPossibleDuplicate},
		{"no category before a guess", func() ReviewRow {
			r := reviewRow("")
			r.Undetermined, r.HasSuggestion = true, true
			return r
		}, ReviewNoCategory},
		{"a guess before unusual", func() ReviewRow {
			r := reviewRow("")
			r.HasSuggestion, r.Amount = true, AmountUnusual
			return r
		}, ReviewGuess},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ReviewReasonOf(c.row())
			require.Equal(t, c.want, got)
			require.Equal(t, c.want == "", got.LooksRight())
		})
	}
}

func transit(id, on, amount string) Transaction {
	return Transaction{
		ID: ID(id), StatementName: "CITY TRANSIT", Date: dupDay(on),
		Amount: MustFromString(amount), Currency: "USD", IsReviewed: true,
	}
}

// The six reviewed rows of the worked example: a median of $20.00.
func transitHistory() []Transaction {
	return []Transaction{
		transit("h1", "2026-03-01", "-18.00"),
		transit("h2", "2026-03-02", "-20.00"),
		transit("h3", "2026-03-03", "-20.00"),
		transit("h4", "2026-03-04", "-20.00"),
		transit("h5", "2026-03-05", "-22.00"),
		transit("h6", "2026-03-06", "-25.00"),
	}
}

func TestAmountUsualnessWorkedExample(t *testing.T) {
	require.Equal(t, AmountUnusual, AmountUsualness(transit("new", "2026-03-10", "-40.00"), transitHistory(), SeriesRange{}))
	require.Equal(t, AmountUsual, AmountUsualness(transit("new", "2026-03-10", "-39.99"), transitHistory(), SeriesRange{}))
}

func TestAmountUsualnessOddHistoryMedian(t *testing.T) {
	history := transitHistory()[:3]
	history[2].Amount = MustFromString("-30.00") // 18, 20, 30: a median of $20.00
	require.Equal(t, AmountUnusual, AmountUsualness(transit("new", "2026-03-10", "-40.00"), history, SeriesRange{}))
	require.Equal(t, AmountUsual, AmountUsualness(transit("new", "2026-03-10", "-39.99"), history, SeriesRange{}))
}

func TestAmountUsualnessNeedsThreeRows(t *testing.T) {
	require.Equal(t, AmountUnknown, AmountUsualness(transit("new", "2026-03-10", "-400.00"), transitHistory()[:2], SeriesRange{}))
}

func TestAmountUsualnessReadsOnlyTheLatestSix(t *testing.T) {
	// The latest six are 10, 10, 10, 30, 30, 30: a median of $20.00. Read with
	// the older $30.00 row, the median would be $30.00 and $40.00 usual.
	history := transitHistory()
	for i, amount := range []string{"-10.00", "-10.00", "-10.00", "-30.00", "-30.00", "-30.00"} {
		history[i].Amount = MustFromString(amount)
	}
	history = append(history, transit("h0", "2026-02-01", "-30.00"))
	require.Equal(t, AmountUnusual, AmountUsualness(transit("new", "2026-03-10", "-40.00"), history, SeriesRange{}))
}

func TestAmountUsualnessHistoryFilters(t *testing.T) {
	row := transit("new", "2026-03-10", "-40.00")
	cases := []struct {
		name  string
		alter func(*Transaction)
	}{
		{"unreviewed", func(h *Transaction) { h.IsReviewed = false }},
		{"another statement name", func(h *Transaction) { h.StatementName = "CITY PARKING" }},
		{"another currency", func(h *Transaction) { h.Currency = "CAD" }},
		{"the other sign", func(h *Transaction) { h.Amount = h.Amount.Neg() }},
		{"zero", func(h *Transaction) { h.Amount = Zero }},
		{"dated after the row", func(h *Transaction) { h.Date = dupDay("2026-03-11") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			history := transitHistory()[:3]
			c.alter(&history[0])
			require.Equal(t, AmountUnknown, AmountUsualness(row, history, SeriesRange{}))
		})
	}
	t.Run("the row itself", func(t *testing.T) {
		history := append(transitHistory()[:2], row)
		require.Equal(t, AmountUnknown, AmountUsualness(row, history, SeriesRange{}))
	})
}

func TestAmountUsualnessFoldsTheStatementName(t *testing.T) {
	history := transitHistory()
	for i := range history {
		history[i].StatementName = "Café Luna"
	}
	row := transit("new", "2026-03-10", "-40.00")
	row.StatementName = " CAFE LUNA "
	require.Equal(t, AmountUnusual, AmountUsualness(row, history, SeriesRange{}))
}

func TestAmountUsualnessSeriesRangeWins(t *testing.T) {
	band := SeriesRange{Low: MustFromString("-50.00"), High: MustFromString("-60.00"), Set: true}
	for amount, want := range map[string]Usualness{
		"-49.99": AmountUnusual,
		"-50.00": AmountUsual,
		"-60.00": AmountUsual,
		"-60.01": AmountUnusual,
	} {
		require.Equal(t, want, AmountUsualness(transit("new", "2026-03-10", amount), nil, band), amount)
	}
	// The history would call $40.00 unusual; the range says usual.
	wide := SeriesRange{Low: MustFromString("10.00"), High: MustFromString("100.00"), Set: true}
	require.Equal(t, AmountUsual, AmountUsualness(transit("new", "2026-03-10", "-40.00"), transitHistory(), wide))
}

func TestEveryReviewKindHasATier(t *testing.T) {
	last := ReviewActNow
	for _, kind := range ReviewKinds {
		require.GreaterOrEqual(t, kind.Tier(), last, kind)
		last = kind.Tier()
	}
	require.Equal(t, ReviewIdeas, ReviewKind("something_new").Tier())
}

func TestBuildReviewQueueOrder(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	items := []ReviewItem{
		{Key: "idea", Kind: ReviewRecurringSuggestion},
		{Key: "proposal", Kind: ReviewProposal},
		{Key: "dup-small", Kind: ReviewDuplicate, Amount: MustFromString("-10.00")},
		{Key: "dup-big", Kind: ReviewDuplicate, Amount: MustFromString("-90.00")},
		{Key: "held-old", Kind: ReviewHeldBalance, Amount: MustFromString("90.00"), Since: now.Add(-48 * time.Hour)},
		{Key: "held-b", Kind: ReviewHeldBalance, Amount: MustFromString("90.00"), Since: now},
		{Key: "held-a", Kind: ReviewHeldBalance, Amount: MustFromString("90.00"), Since: now},
		{Key: "bill-recent", Kind: ReviewBillPastDue, DueAt: dupDay("2026-03-08").Time()},
		{Key: "bill-oldest", Kind: ReviewBillPastDue, DueAt: dupDay("2026-03-01").Time()},
		{Key: "reconnect", Kind: ReviewBankReconnect},
		{Key: "code", Kind: ReviewSignInCode, DueAt: now.Add(4 * time.Minute)},
	}
	got := BuildReviewQueue(items, nil, now)
	var keys []string
	for _, item := range got {
		keys = append(keys, item.Key)
	}
	require.Equal(t, []string{
		"code", "reconnect",
		"bill-oldest", "bill-recent", "dup-big", "held-old", "held-a", "held-b", "dup-small",
		"proposal",
		"idea",
	}, keys)
}

func TestBuildReviewQueueSnoozes(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	items := []ReviewItem{
		{Key: "snoozed", Kind: ReviewHeldBalance},
		{Key: "woke", Kind: ReviewHeldBalance},
		{Key: "open", Kind: ReviewHeldBalance},
	}
	snoozes := []ReviewSnooze{
		{Key: "snoozed", Until: now.Add(time.Hour)},
		{Key: "woke", Until: now},
	}
	got := BuildReviewQueue(items, snoozes, now)
	require.Len(t, got, 2)
	require.Equal(t, "open", got[0].Key)
	require.Equal(t, "woke", got[1].Key)
}

func TestReviewCountsBadge(t *testing.T) {
	items := []ReviewItem{
		{Kind: ReviewSignInCode}, {Kind: ReviewBankReconnect},
		{Kind: ReviewBillPastDue}, {Kind: ReviewBillPastDue}, {Kind: ReviewDuplicate},
		{Kind: ReviewHeldBalance}, {Kind: ReviewOrphanTransfers, Count: 3},
		{Kind: ReviewRecurringSuggestion}, {Kind: ReviewUnmatchedOrder},
	}
	rows := make([]ReviewReason, 23)
	for i := range 9 {
		rows[i] = ReviewGuess
	}
	got := ReviewCounts(items, rows)
	require.Equal(t, ReviewSummary{ActNow: 2, MoneyAtStake: 5, Ideas: 2, New: 23, NeedsALook: 9}, got)
	require.Equal(t, 16, got.Badge())
}

func TestReviewCountsADuplicateCountsAsItemAndLegs(t *testing.T) {
	got := ReviewCounts(
		[]ReviewItem{{Kind: ReviewDuplicate}},
		[]ReviewReason{ReviewPossibleDuplicate, ReviewPossibleDuplicate},
	)
	require.Equal(t, 3, got.Badge())
}
