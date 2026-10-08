package api

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The spending plan, end to end, against a real database.
//
// Every test is a rule from calculations.md §5, and the ones that cost the
// most to get wrong are the ones about counting the same money twice: a bill
// that posted, a transfer inside Bills, a transaction two envelopes both want.
//
// **The clock is stopped.** Per-day, the run rate and days-remaining all
// divide by a day count taken from the server's today, so a test against the
// real clock would assert a different number tomorrow.

// planClock is the server's today for these tests: 20 August 2026, which
// leaves 12 of August's 31 days and 20 elapsed.
var planClock = time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)

const (
	julyMonth   = "2026-07"
	augustMonth = "2026-08"
)

// clockedLedger is buildLedger with the server's clock stopped.
func clockedLedger(t *testing.T, at time.Time) *ledger {
	t.Helper()
	l := buildLedger(t)
	l.env = NewEnv(testConfig(), db(t), WithClock(func() time.Time { return at }))
	l.alex = l.as("alex")
	return l
}

func planLedger(t *testing.T) *ledger { return clockedLedger(t, planClock) }

func planFor(l *ledger, month string) map[string]any {
	l.t.Helper()
	return l.alex.get("/spending-plan/" + month).requireStatus(http.StatusOK).json()
}

func planBucket(t *testing.T, month map[string]any, key string) map[string]any {
	t.Helper()
	buckets, ok := month["buckets"].([]any)
	require.True(t, ok, "the month carries no buckets: %v", month)
	for _, raw := range buckets {
		bucket := raw.(map[string]any)
		if bucket["key"] == key {
			return bucket
		}
	}
	t.Fatalf("no bucket %q in the response", key)
	return nil
}

func effective(t *testing.T, month map[string]any, key string) string {
	t.Helper()
	return planBucket(t, month, key)["effective_amount"].(string)
}

func calculated(t *testing.T, month map[string]any, key string) string {
	t.Helper()
	return planBucket(t, month, key)["calculated_amount"].(string)
}

func billRows(month map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, raw := range month["bills"].([]any) {
		out = append(out, raw.(map[string]any))
	}
	return out
}

func billSubtotal(t *testing.T, month map[string]any, group string) string {
	t.Helper()
	for _, raw := range month["bill_subtotals"].([]any) {
		row := raw.(map[string]any)
		if row["group"] == group {
			return row["amount"].(string)
		}
	}
	t.Fatalf("no bill subtotal for group %q", group)
	return ""
}

func envelopes(month map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, raw := range month["envelopes"].([]any) {
		out = append(out, raw.(map[string]any))
	}
	return out
}

func envelopeNamed(t *testing.T, month map[string]any, name string) map[string]any {
	t.Helper()
	for _, envelope := range envelopes(month) {
		if envelope["name"] == name {
			return envelope
		}
	}
	t.Fatalf("no envelope named %q", name)
	return nil
}

// requireRate compares a percentage without depending on how many trailing
// zeros the decimal chose to print.
func requireRate(t *testing.T, want string, got any) {
	t.Helper()
	raw, ok := got.(string)
	require.True(t, ok, "a rate crosses the wire as a string, got %T", got)
	require.True(t, decimal.RequireFromString(want).Equal(decimal.RequireFromString(raw)),
		"want %s, got %s", want, raw)
}

// --- Fixtures ----------------------------------------------------------------

func monthStart(t *testing.T, month string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01", month)
	require.NoError(t, err)
	return parsed
}

type seriesSeed struct {
	Key        string
	Name       string
	Kind       domain.SeriesKind
	Amount     string
	Alias      domain.RecurrenceAlias
	ByMonthDay []int
	StartOn    domain.Date
	NextDueOn  domain.Date
}

// seedSeries writes a live series straight to the table. The /series resource
// has its own tests, and a plan test that went through it would fail for that
// resource's reasons as well as its own.
func seedSeries(l *ledger, seed seriesSeed) uuid.UUID {
	l.t.Helper()
	id := uuid.New()
	_, err := l.env.DB.Pool().Exec(l.t.Context(), `
		INSERT INTO series (id, space_id, account_id, kind, description, amount, currency,
			alias, frequency, "interval", by_month_day, start_on, next_due_on,
			reminder_days, match_criteria, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, 'USD', $7, 'MONTHLY', 1, $8, $9, $10, 3, 'auto', true)`,
		id, l.id("space"), l.id("checking"), string(seed.Kind), seed.Name,
		dbconv.Money(domain.MustFromString(seed.Amount)), string(seed.Alias), seed.ByMonthDay,
		seed.StartOn.Time(), seed.NextDueOn.Time())
	require.NoError(l.t, err)
	l.ids[seed.Key] = id
	return id
}

func seedTxn(l *ledger, key string, txn *store.Transaction) uuid.UUID {
	l.t.Helper()
	if txn.Currency == "" {
		txn.Currency = "USD"
	}
	if txn.Source == "" {
		txn.Source = domain.SourceSync
	}
	require.NoError(l.t, l.env.DB.CreateTransaction(l.t.Context(),
		store.SpaceIDOf(l.id("space")), txn))
	l.ids[key] = txn.ID
	return txn.ID
}

// closeMonth freezes a month at a figure no recalculation would produce, which
// is what lets a test tell a frozen number from a fresh one. The month has to
// have been read once first, so a row exists to stamp.
func closeMonth(l *ledger, month, otherSpend string) {
	l.t.Helper()
	tag, err := l.env.DB.Pool().Exec(l.t.Context(), `
		UPDATE spending_plan_months
		   SET is_closed_out = true, closed_out_at = $4,
		       calculated_spent_amount = $3, left_to_spend_amount = $3
		 WHERE space_id = $1 AND month = $2`,
		l.id("space"), monthStart(l.t, month), dbconv.Money(domain.MustFromString(otherSpend)),
		planClock)
	require.NoError(l.t, err)
	require.EqualValues(l.t, 1, tag.RowsAffected(), "the month must be materialized before it closes")
}

// --- The baseline month ------------------------------------------------------

func TestTheMonthAddsUpToItsHeadlineAndCarriesTheServersToday(t *testing.T) {
	// The seeded August is 50.00 of groceries and 25.00 at the corner store.
	// The transfer pair is not spending — the money did not leave the
	// household — and the card charge is filed in September by effective date.
	l := planLedger(t)
	month := planFor(l, augustMonth)

	require.Equal(t, augustMonth, month["month"])
	require.Equal(t, "2026-08-20", month["as_of"])
	require.Equal(t, false, month["is_closed_out"])

	require.Equal(t, "0.00", effective(t, month, "income"))
	require.Equal(t, "0.00", effective(t, month, "bills"))
	require.Equal(t, "0.00", effective(t, month, "planned_spend"))
	require.Equal(t, "-75.00", effective(t, month, "other_spend"))
	require.Equal(t, "0.00", effective(t, month, "goals"))
	require.Equal(t, "0.00", effective(t, month, "rollover"))

	require.Equal(t, "-75.00", month["left_this_month"])
	require.Equal(t, float64(12), month["days_remaining"])
	require.Equal(t, "-6.25", month["per_day"])
	require.Equal(t, "75.00", month["other_spend_to_date"])
	// Run rate: 75.00 over 20 elapsed days, carried across all 31.
	require.Equal(t, "116.25", month["projected_other_spending"])
	require.Equal(t, "-116.25", month["projected_left"])
}

func TestTheOtherSpendBucketNamesTheRowsBehindItsFigure(t *testing.T) {
	// The audit trail is what makes the number explainable rather than merely
	// correct.
	l := planLedger(t)
	month := planFor(l, augustMonth)

	contributing := planBucket(t, month, "other_spend")["contributing_txn_ids"].([]any)
	require.ElementsMatch(t,
		[]any{l.str("august_groceries"), l.str("august_corner")}, contributing)
}

// --- Closed months -----------------------------------------------------------

func TestAClosedMonthIsReturnedFrozenRatherThanRecalculated(t *testing.T) {
	// calculations.md §5 rule 5. Without the freeze, editing a transaction from
	// two years ago silently rewrites every plan since.
	l := planLedger(t)
	require.Equal(t, "-100.00", effective(t, planFor(l, julyMonth), "other_spend"))

	closeMonth(l, julyMonth, "-10.00")

	frozen := planFor(l, julyMonth)
	require.Equal(t, true, frozen["is_closed_out"])
	require.Equal(t, "-10.00", effective(t, frozen, "other_spend"))
	require.Equal(t, "-10.00", frozen["left_this_month"])

	// Reading it again must not have written today's recalculation over it.
	require.Equal(t, "-10.00", effective(t, planFor(l, julyMonth), "other_spend"))
	// And the transaction underneath is untouched: it is the plan that froze,
	// not the ledger.
	txn := l.alex.get("/transactions/" + l.str("july")).requireStatus(http.StatusOK).json()
	require.Equal(t, "-100.00", txn["amount"])
}

func TestAMutationAgainstAClosedMonthIsRefusedRatherThanSilentlyApplied(t *testing.T) {
	// A write that landed on a frozen month would be stored and then never
	// reflected, which is worse than a refusal because nothing says so.
	l := planLedger(t)
	planFor(l, julyMonth)
	closeMonth(l, julyMonth, "-10.00")

	writes := []func() *response{
		func() *response {
			return l.alex.patch("/spending-plan/"+julyMonth+"/buckets/other_spend",
				map[string]any{"overwritten_amount": "-1.00"})
		},
		func() *response {
			return l.alex.post("/spending-plan/"+julyMonth+"/buckets/other_spend/exclusions",
				map[string]any{"entry_id": l.str("july")})
		},
		func() *response {
			return l.alex.del("/spending-plan/" + julyMonth + "/buckets/other_spend/exclusions/" + l.str("july"))
		},
		func() *response {
			return l.alex.post("/spending-plan/"+julyMonth+"/envelopes", map[string]any{
				"name": "Groceries", "target_amount": "100.00",
				"category_ids": []string{l.str("groceries")},
			})
		},
		func() *response {
			return l.alex.post("/spending-plan/"+julyMonth+"/envelopes/release-all", map[string]any{})
		},
		func() *response {
			return l.alex.patch("/spending-plan/"+julyMonth+"/projection",
				map[string]any{"type": "prior_month"})
		},
	}
	for i, write := range writes {
		response := write()
		response.requireStatus(http.StatusConflict)
		require.Contains(t, response.Body.String(), "closed out", "write %d", i)
	}

	require.Equal(t, "-10.00", effective(t, planFor(l, julyMonth), "other_spend"))
}

func TestTheRolloverCascadeStopsAtAClosedMonth(t *testing.T) {
	// §5 rule 6. August carries in July's *stored* figure, not what July would
	// compute today.
	l := planLedger(t)
	planFor(l, julyMonth)
	closeMonth(l, julyMonth, "-10.00")

	august := planFor(l, augustMonth)
	require.Equal(t, "-10.00", effective(t, august, "rollover"))
	require.Equal(t, "-85.00", august["left_this_month"])
}

func TestTheMonthsOwnResultTravelsBesideTheHeadline(t *testing.T) {
	// The rail leads with the month without its rollover; sending it saves
	// the client taking a bucket off the headline itself.
	l := planLedger(t)
	planFor(l, julyMonth)
	august := planFor(l, augustMonth)
	require.Equal(t, "-100.00", effective(t, august, "rollover"))
	require.Equal(t, "-175.00", august["left_this_month"])
	require.Equal(t, "-75.00", august["month_result"])
	require.Equal(t, august["projected_left"],
		domain.MustFromString(august["projected_month_result"].(string)).
			Add(domain.MustFromString("-100.00")).String())
	require.Contains(t, august, "month_result_per_day")
	require.Contains(t, august, "days_elapsed")
}

// --- Whole-month responses ---------------------------------------------------

func TestEveryMutationAnswersWithTheWholeRecalculatedMonth(t *testing.T) {
	// A response carrying only the bucket that was touched leaves the headline
	// on screen stale.
	l := planLedger(t)
	body := l.alex.patch("/spending-plan/"+augustMonth+"/buckets/income",
		map[string]any{"overwritten_amount": "1000.00"}).
		requireStatus(http.StatusOK).json()

	require.Len(t, body["buckets"], 6)
	require.Equal(t, "1000.00", effective(t, body, "income"))
	require.Equal(t, "-75.00", effective(t, body, "other_spend"))
	require.Equal(t, "925.00", body["left_this_month"])
	require.NotNil(t, body["bills"])
	require.NotNil(t, body["envelopes"])
	require.NotNil(t, body["per_day"])

	require.Equal(t, body["left_this_month"], planFor(l, augustMonth)["left_this_month"])
}

func TestAnOverrideInAnEarlierMonthCascadesIntoTheNextMonthsRollover(t *testing.T) {
	// The whole point of the chain: month N's rollover is month N−1's headline.
	l := planLedger(t)
	planFor(l, julyMonth)
	require.Equal(t, "-100.00", effective(t, planFor(l, augustMonth), "rollover"))

	edited := l.alex.patch("/spending-plan/"+julyMonth+"/buckets/other_spend",
		map[string]any{"overwritten_amount": "-10.00"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "-10.00", edited["left_this_month"])

	august := planFor(l, augustMonth)
	require.Equal(t, "-10.00", effective(t, august, "rollover"))
	require.Equal(t, "-85.00", august["left_this_month"])
}

// --- Overrides ---------------------------------------------------------------

func TestAUserOverrideWinsOverTheCalculatedFigure(t *testing.T) {
	l := planLedger(t)
	body := l.alex.patch("/spending-plan/"+augustMonth+"/buckets/other_spend",
		map[string]any{"overwritten_amount": "-10.00"}).
		requireStatus(http.StatusOK).json()

	bucket := planBucket(t, body, "other_spend")
	require.Equal(t, "-75.00", bucket["calculated_amount"])
	require.Equal(t, "-10.00", bucket["overwritten_amount"])
	require.Equal(t, "-10.00", bucket["effective_amount"])
	require.Equal(t, "-10.00", body["left_this_month"])
}

func TestClearingAnOverrideRestoresTheCalculationRatherThanZero(t *testing.T) {
	l := planLedger(t)
	path := "/spending-plan/" + augustMonth + "/buckets/other_spend"
	l.alex.patch(path, map[string]any{"overwritten_amount": "-10.00"}).
		requireStatus(http.StatusOK)

	cleared := l.alex.patch(path, map[string]any{"overwritten_amount": nil}).
		requireStatus(http.StatusOK).json()

	bucket := planBucket(t, cleared, "other_spend")
	require.Nil(t, bucket["overwritten_amount"])
	require.Equal(t, "-75.00", bucket["effective_amount"])
	require.Equal(t, "-75.00", cleared["left_this_month"])
}

func TestAnOverrideOfZeroIsADecisionAndNotAnAbsentOne(t *testing.T) {
	// HasOverwritten distinguishes "the user said zero" from "the user said
	// nothing"; collapsing them makes a deliberate zero un-expressible.
	l := planLedger(t)
	body := l.alex.patch("/spending-plan/"+augustMonth+"/buckets/other_spend",
		map[string]any{"overwritten_amount": "0.00"}).
		requireStatus(http.StatusOK).json()

	bucket := planBucket(t, body, "other_spend")
	require.Equal(t, "0.00", bucket["overwritten_amount"])
	require.Equal(t, "0.00", bucket["effective_amount"])
	require.Equal(t, "0.00", body["left_this_month"])
}

func TestAnOverrideNeedsAnExplicitAmountOrAnExplicitNull(t *testing.T) {
	l := planLedger(t)
	response := l.alex.patch("/spending-plan/"+augustMonth+"/buckets/other_spend", map[string]any{})
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "overwritten_amount")
}

func TestTheRolloverBucketHasNothingToOverride(t *testing.T) {
	// It is carried in, not computed from rows.
	l := planLedger(t)
	l.alex.patch("/spending-plan/"+augustMonth+"/buckets/rollover",
		map[string]any{"overwritten_amount": "5.00"}).requireStatus(http.StatusNotFound)
}

// --- Per-month exclusions ----------------------------------------------------

func TestAPerMonthExclusionDropsTheRowWithoutMutatingTheTransaction(t *testing.T) {
	// §5's five-field pattern: excluded_txn_ids drops a row for this month
	// only. The transaction still counts in reports and in the register.
	l := planLedger(t)
	path := "/spending-plan/" + augustMonth + "/buckets/other_spend/exclusions"

	excluded := l.alex.post(path, map[string]any{"entry_id": l.str("august_groceries")}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "-25.00", effective(t, excluded, "other_spend"))
	require.Equal(t, []any{l.str("august_groceries")},
		planBucket(t, excluded, "other_spend")["excluded_entry_ids"])

	txn := l.alex.get("/transactions/" + l.str("august_groceries")).
		requireStatus(http.StatusOK).json()
	require.Equal(t, false, txn["excluded_from_spending_plan"])
	require.Equal(t, false, txn["excluded_from_reports"])
	require.Equal(t, "-50.00", txn["amount"])
	require.Contains(t, idsIn(registerPage(l, "limit=500")), l.str("august_groceries"))

	restored := l.alex.del(path + "/" + l.str("august_groceries")).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "-75.00", effective(t, restored, "other_spend"))
	require.Empty(t, planBucket(t, restored, "other_spend")["excluded_entry_ids"])
}

func TestARowExcludedFromOneBucketDoesNotReappearInAnother(t *testing.T) {
	// Eligibility is decided over the union of every bucket's exclusions.
	l := planLedger(t)
	l.alex.post("/spending-plan/"+augustMonth+"/buckets/income/exclusions",
		map[string]any{"entry_id": l.str("august_groceries")}).requireStatus(http.StatusOK)

	month := planFor(l, augustMonth)
	require.Equal(t, "-25.00", effective(t, month, "other_spend"))
}

func TestRemovingAnExclusionThatWasNeverThereIsNotFound(t *testing.T) {
	l := planLedger(t)
	l.alex.del("/spending-plan/" + augustMonth + "/buckets/other_spend/exclusions/" +
		l.str("august_groceries")).requireStatus(http.StatusNotFound)
}

// --- Bills -------------------------------------------------------------------

func seedPowerBill(l *ledger) uuid.UUID {
	return seedSeries(l, seriesSeed{
		Key: "power", Name: "Power", Kind: domain.SeriesBill, Amount: "-60.00",
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{12},
		StartOn:   domain.NewDate(2026, time.January, 12),
		NextDueOn: domain.NewDate(2026, time.August, 12),
	})
}

func TestAnExpectedBillIsCountedAtTheAmountTheSeriesPredicts(t *testing.T) {
	l := planLedger(t)
	seedPowerBill(l)
	month := planFor(l, augustMonth)

	rows := billRows(month)
	require.Len(t, rows, 1)
	require.Equal(t, "Power", rows[0]["name"])
	require.Equal(t, "bill", rows[0]["group"])
	require.Equal(t, "2026-08-12", rows[0]["due_on"])
	require.Equal(t, "-60.00", rows[0]["amount"])
	require.Equal(t, false, rows[0]["is_fulfilled"])
	require.Equal(t, "-60.00", effective(t, month, "bills"))
	require.Equal(t, "-60.00", billSubtotal(t, month, "bill"))
	require.Equal(t, "-135.00", month["left_this_month"])
}

func TestABillThatHasPostedCountsOnceNotTwice(t *testing.T) {
	// §5 rule 1: an occurrence is either expected or fulfilled, never both. The
	// posted figure wins because it is what actually happened.
	l := planLedger(t)
	series := seedPowerBill(l)
	seedTxn(l, "power_paid", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 12),
		Amount: domain.MustFromString("-70.00"), StatementName: "CITY POWER",
		Payee: "City Power", SeriesID: series, SeriesDueOn: domain.NewDate(2026, time.August, 12),
	})

	month := planFor(l, augustMonth)
	rows := billRows(month)
	require.Len(t, rows, 1)
	require.Equal(t, true, rows[0]["is_fulfilled"])
	require.Equal(t, "-70.00", rows[0]["amount"])
	require.Equal(t, []any{l.str("power_paid")}, rows[0]["txn_ids"])

	require.Equal(t, "-70.00", effective(t, month, "bills"), "the expected 60 must not be added on top")
	// And the posted row is a bill, not other spending, so it is not counted a
	// second time there either.
	require.Equal(t, "-75.00", effective(t, month, "other_spend"))
	require.Equal(t, "-145.00", month["left_this_month"])
}

func TestTwoOccurrencesOfOneSeriesInOneMonthDoNotCollapse(t *testing.T) {
	// The link is the series *and* the date slot. Matching on the series alone
	// makes both of a twice-monthly bill's occurrences look fulfilled by
	// whichever transaction posted first.
	l := planLedger(t)
	series := seedSeries(l, seriesSeed{
		Key: "rent_half", Name: "Half Rent", Kind: domain.SeriesBill, Amount: "-30.00",
		Alias: domain.AliasTwiceAMonth, ByMonthDay: []int{5, 20},
		StartOn:   domain.NewDate(2026, time.January, 5),
		NextDueOn: domain.NewDate(2026, time.August, 5),
	})
	seedTxn(l, "rent_paid", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 5),
		Amount: domain.MustFromString("-35.00"), StatementName: "LANDLORD",
		Payee: "Landlord", SeriesID: series, SeriesDueOn: domain.NewDate(2026, time.August, 5),
	})

	month := planFor(l, augustMonth)
	rows := billRows(month)
	require.Len(t, rows, 2)
	require.Equal(t, "2026-08-05", rows[0]["due_on"])
	require.Equal(t, true, rows[0]["is_fulfilled"])
	require.Equal(t, "-35.00", rows[0]["amount"])
	require.Equal(t, "2026-08-20", rows[1]["due_on"])
	require.Equal(t, false, rows[1]["is_fulfilled"])
	require.Equal(t, "-30.00", rows[1]["amount"])

	require.Equal(t, "-65.00", effective(t, month, "bills"))
}

func TestTransfersAndCardPaymentsNetToZeroInsideBills(t *testing.T) {
	// §5 rule 2. They are listed for visibility and must not reduce
	// free-to-spend: the money was already counted when the purchase posted.
	l := planLedger(t)
	before := planFor(l, augustMonth)["left_this_month"]

	seedSeries(l, seriesSeed{
		Key: "to_savings", Name: "To Savings", Kind: domain.SeriesTransfer, Amount: "-500.00",
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{15},
		StartOn:   domain.NewDate(2026, time.January, 15),
		NextDueOn: domain.NewDate(2026, time.August, 15),
	})
	seedSeries(l, seriesSeed{
		Key: "card_payment", Name: "Card Payment", Kind: domain.SeriesCreditCardPayment,
		Amount: "-200.00", Alias: domain.AliasEveryMonth, ByMonthDay: []int{18},
		StartOn:   domain.NewDate(2026, time.January, 18),
		NextDueOn: domain.NewDate(2026, time.August, 18),
	})

	month := planFor(l, augustMonth)
	rows := billRows(month)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, "transfer", row["group"])
		require.Equal(t, "0.00", row["amount"])
	}
	require.Equal(t, "0.00", billSubtotal(t, month, "transfer"))
	require.Equal(t, "0.00", effective(t, month, "bills"))
	require.Equal(t, before, month["left_this_month"], "a transfer must not reduce free-to-spend")
}

func TestTheThreeBillGroupsSubtotalSeparatelyUnderOneFoldedBucket(t *testing.T) {
	// calculations.md §5 folds three families into one line; data-model §5
	// keeps three column families. Both are true, and the response carries the
	// group on every row so the screenshot's three subtotals and the plan's one
	// line come from the same rows.
	l := planLedger(t)
	seedPowerBill(l)
	seedSeries(l, seriesSeed{
		Key: "streaming", Name: "Streaming", Kind: domain.SeriesSubscription, Amount: "-15.00",
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{3},
		StartOn:   domain.NewDate(2026, time.January, 3),
		NextDueOn: domain.NewDate(2026, time.August, 3),
	})
	seedSeries(l, seriesSeed{
		Key: "to_savings", Name: "To Savings", Kind: domain.SeriesTransfer, Amount: "-500.00",
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{15},
		StartOn:   domain.NewDate(2026, time.January, 15),
		NextDueOn: domain.NewDate(2026, time.August, 15),
	})

	month := planFor(l, augustMonth)
	require.Equal(t, "-60.00", billSubtotal(t, month, "bill"))
	require.Equal(t, "-15.00", billSubtotal(t, month, "subscription"))
	require.Equal(t, "0.00", billSubtotal(t, month, "transfer"))
	require.Equal(t, "-75.00", effective(t, month, "bills"))
}

// --- The composite exclusion id ----------------------------------------------

func TestAnUnfulfilledOccurrenceIsExcludedByItsSeriesAndDueDate(t *testing.T) {
	// An unfulfilled occurrence has no transaction id, so its identity is the
	// slot. The exclusion endpoints take that composite as well as a uuid.
	l := planLedger(t)
	series := seedPowerBill(l)
	slot := fmt.Sprintf("%s:2026-08-12", series)

	month := planFor(l, augustMonth)
	require.Equal(t, slot, billRows(month)[0]["id"])

	excluded := l.alex.post("/spending-plan/"+augustMonth+"/buckets/bills/exclusions",
		map[string]any{"entry_id": slot}).requireStatus(http.StatusOK).json()

	require.Equal(t, "0.00", effective(t, excluded, "bills"))
	require.Equal(t, "0.00", billSubtotal(t, excluded, "bill"))
	require.Equal(t, true, billRows(excluded)[0]["is_excluded"], "the row stays, flagged, so it can be offered back")
	// The stored form is a derived uuid; the wire form is the composite the
	// client sent and the only one it can act on.
	require.Equal(t, []any{slot}, planBucket(t, excluded, "bills")["excluded_entry_ids"])

	restored := l.alex.del("/spending-plan/" + augustMonth + "/buckets/bills/exclusions/" + slot).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "-60.00", effective(t, restored, "bills"))
	require.Equal(t, false, billRows(restored)[0]["is_excluded"])
}

func TestAPostedBillIsExcludedByItsTransactionId(t *testing.T) {
	// Once something has posted, the row's id is the transaction's — and a
	// fulfilled occurrence the user excluded contributes zero rather than
	// reverting to the series' prediction (calculations.md §13).
	l := planLedger(t)
	series := seedPowerBill(l)
	seedTxn(l, "power_paid", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 12),
		Amount: domain.MustFromString("-70.00"), StatementName: "CITY POWER",
		Payee: "City Power", SeriesID: series, SeriesDueOn: domain.NewDate(2026, time.August, 12),
	})

	excluded := l.alex.post("/spending-plan/"+augustMonth+"/buckets/bills/exclusions",
		map[string]any{"entry_id": l.str("power_paid")}).requireStatus(http.StatusOK).json()

	require.Equal(t, "0.00", effective(t, excluded, "bills"))
	require.Equal(t, []any{l.str("power_paid")}, planBucket(t, excluded, "bills")["excluded_entry_ids"])

	// The row has to keep saying which transaction it is and that it was
	// dropped, or the UI has nothing to offer back.
	row := billRows(excluded)[0]
	require.Equal(t, l.str("power_paid"), row["id"])
	require.Equal(t, true, row["is_excluded"])
	require.Equal(t, []any{l.str("power_paid")}, row["txn_ids"])

	restored := l.alex.del("/spending-plan/" + augustMonth + "/buckets/bills/exclusions/" +
		l.str("power_paid")).requireStatus(http.StatusOK).json()
	require.Equal(t, "-70.00", effective(t, restored, "bills"))
}

// The occurrence excluded before the money moved, and the charge that then
// paid it. The exclusion is stored under the slot's derived uuid; from the
// moment a charge claims the slot the bucket presents the same occurrence
// under the charge's id, which is the only id the client can send back.
// Matching on that one id alone would refuse the release with "Exclusion
// not found" and leave the row excluded with no way back.
func TestAnOccurrenceExcludedBeforeItWasPaidIsStillIncludedAfterwards(t *testing.T) {
	l := planLedger(t)
	series := seedPowerBill(l)
	slot := fmt.Sprintf("%s:2026-08-12", series)

	l.alex.post("/spending-plan/"+augustMonth+"/buckets/bills/exclusions",
		map[string]any{"entry_id": slot}).requireStatus(http.StatusOK)

	seedTxn(l, "power_paid", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 12),
		Amount: domain.MustFromString("-70.00"), StatementName: "CITY POWER",
		Payee: "City Power", SeriesID: series, SeriesDueOn: domain.NewDate(2026, time.August, 12),
	})

	paid := planFor(l, augustMonth)
	row := billRows(paid)[0]
	require.Equal(t, l.str("power_paid"), row["id"], "the charge names the occurrence now")
	require.Equal(t, true, row["is_excluded"])

	restored := l.alex.del("/spending-plan/" + augustMonth + "/buckets/bills/exclusions/" +
		l.str("power_paid")).requireStatus(http.StatusOK).json()
	require.Equal(t, "-70.00", effective(t, restored, "bills"))
	require.Equal(t, false, billRows(restored)[0]["is_excluded"])
	require.Empty(t, planBucket(t, restored, "bills")["excluded_entry_ids"])
}

// The duplicate the sync wrote twice, one copy deleted.
//
// A deleted row is a skipped holder of its slot — the shape a skip tombstone
// takes — so reading the slot as "not happening" while the surviving charge
// sits in it would draw that charge as excluded by nothing: the transaction
// carries no exclusion flag, no exclusion row exists, and Include would
// answer "Exclusion not found".
func TestADeletedDuplicateDoesNotExcludeTheChargeThatSurvivedIt(t *testing.T) {
	l := planLedger(t)
	series := seedPowerBill(l)
	due := domain.NewDate(2026, time.August, 12)
	seedTxn(l, "power_paid", &store.Transaction{
		AccountID: l.id("checking"), Date: due,
		Amount: domain.MustFromString("-70.00"), StatementName: "CITY POWER",
		Payee: "City Power", SeriesID: series, SeriesDueOn: due,
	})
	duplicate := seedTxn(l, "power_dupe", &store.Transaction{
		AccountID: l.id("checking"), Date: due,
		Amount: domain.MustFromString("-70.00"), StatementName: "CITY POWER",
		Payee: "City Power", SeriesID: series, SeriesDueOn: due,
	})
	l.alex.del("/transactions/" + duplicate.String()).requireStatus(http.StatusNoContent)

	month := planFor(l, augustMonth)
	row := billRows(month)[0]
	require.Equal(t, false, row["is_excluded"], "the surviving charge is not excluded by anything")
	require.Equal(t, "-70.00", effective(t, month, "bills"), "and it still costs the month")
}

func TestAnEntryIdIsEitherAUuidOrASlot(t *testing.T) {
	l := planLedger(t)
	for _, entry := range []string{"not-an-id", "not-a-uuid:2026-08-12", uuid.NewString() + ":yesterday"} {
		response := l.alex.post("/spending-plan/"+augustMonth+"/buckets/bills/exclusions",
			map[string]any{"entry_id": entry})
		response.requireStatus(http.StatusUnprocessableEntity)
		require.Contains(t, response.Body.String(), "entry_id")
	}
}

// --- Envelopes ---------------------------------------------------------------

func createEnvelopeIn(l *ledger, month, name, target string) map[string]any {
	l.t.Helper()
	return l.alex.post("/spending-plan/"+month+"/envelopes", map[string]any{
		"name": name, "target_amount": target,
		"category_ids": []string{l.str("groceries")},
	}).requireStatus(http.StatusOK).json()
}

func TestEnvelopeSpendLeavesTheOtherSpendBucket(t *testing.T) {
	// §5 rule 3. A transaction an envelope claims must not also be counted as
	// other spending, or free-to-spend is reduced twice for the same money.
	l := planLedger(t)
	month := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")

	envelope := envelopeNamed(t, month, "Groceries")
	require.Equal(t, "100.00", envelope["target"])
	require.Equal(t, "50.00", envelope["spent"])
	require.Equal(t, "50.00", envelope["available"])
	require.Equal(t, "100.00", envelope["budget"])
	requireRate(t, "50", envelope["pct_used"])
	require.Equal(t, "normal", envelope["state"])
	require.Equal(t, []any{l.str("august_groceries")}, envelope["txn_ids"])

	require.Equal(t, "-25.00", effective(t, month, "other_spend"), "the claimed row left other spend")
	// planned_spend reserves the target, never the actual spend.
	require.Equal(t, "-100.00", effective(t, month, "planned_spend"))
	require.Equal(t, "-125.00", month["left_this_month"])
}

func TestATransactionMatchingTwoEnvelopesLandsInExactlyOne(t *testing.T) {
	// Creation order decides, and the loser is recorded so a figure the user
	// expected elsewhere can be explained.
	l := planLedger(t)
	first := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	firstID := envelopeNamed(t, first, "Groceries")["id"].(string)
	month := createEnvelopeIn(l, augustMonth, "Food", "200.00")
	secondID := envelopeNamed(t, month, "Food")["id"].(string)

	require.Equal(t, "50.00", envelopeNamed(t, month, "Groceries")["spent"])
	require.Equal(t, "0.00", envelopeNamed(t, month, "Food")["spent"])
	require.Equal(t, []any{l.str("august_groceries")},
		envelopeNamed(t, month, "Groceries")["txn_ids"])
	require.Equal(t, map[string]any{l.str("august_groceries"): []any{secondID}},
		month["contested_txn_ids"])
	require.NotEqual(t, firstID, secondID)

	// The row is spent once, not twice: other spend keeps only the corner shop.
	require.Equal(t, "-25.00", effective(t, month, "other_spend"))
	require.Equal(t, "-300.00", effective(t, month, "planned_spend"))
}

func TestAZeroTargetEnvelopeWithSpendIsFullyUsedRatherThanADivideByZero(t *testing.T) {
	l := planLedger(t)
	month := createEnvelopeIn(l, augustMonth, "Groceries", "0.00")

	envelope := envelopeNamed(t, month, "Groceries")
	require.Equal(t, "0.00", envelope["target"])
	require.Equal(t, "50.00", envelope["spent"])
	require.Equal(t, "-50.00", envelope["available"])
	requireRate(t, "100", envelope["pct_used"])
	requireRate(t, "100", envelope["bar_pct"])
	require.Equal(t, "overspent", envelope["state"])
}

func TestAnOverspentEnvelopeReadsPastOneHundredPercentWhileItsBarStops(t *testing.T) {
	l := planLedger(t)
	month := createEnvelopeIn(l, augustMonth, "Groceries", "20.00")

	envelope := envelopeNamed(t, month, "Groceries")
	requireRate(t, "250", envelope["pct_used"])
	requireRate(t, "100", envelope["bar_pct"])
	require.Equal(t, "overspent", envelope["state"])
}

func TestEnvelopeSpentNetsARefundRatherThanSummingMagnitudes(t *testing.T) {
	// calculations.md §13's recorded departure from §5: summing |amount| makes
	// a returned purchase consume the envelope twice. ComputeMonth has to offer
	// the refund to AssignEnvelopes, or it lands in Income instead.
	l := planLedger(t)
	seedTxn(l, "grocery_refund", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 18),
		Amount: domain.MustFromString("20.00"), StatementName: "SAFEWAY REFUND",
		Payee: "Safeway", CategoryID: l.id("groceries"),
	})
	month := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")

	envelope := envelopeNamed(t, month, "Groceries")
	require.Equal(t, "30.00", envelope["spent"])
	require.ElementsMatch(t, []any{l.str("august_groceries"), l.str("grocery_refund")},
		envelope["txn_ids"])
}

func TestAnEnvelopeTargetOverrideMovesThePlannedSpendBucket(t *testing.T) {
	l := planLedger(t)
	created := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	id := envelopeNamed(t, created, "Groceries")["id"].(string)

	month := l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"overwritten_target_amount": "250.00"}).
		requireStatus(http.StatusOK).json()

	envelope := envelopeNamed(t, month, "Groceries")
	require.Equal(t, "100.00", envelope["target_amount"])
	require.Equal(t, "250.00", envelope["overwritten_target_amount"])
	require.Equal(t, "250.00", envelope["target"])
	require.Equal(t, "-250.00", effective(t, month, "planned_spend"))
	require.Equal(t, "-275.00", month["left_this_month"])

	cleared := l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"overwritten_target_amount": nil}).
		requireStatus(http.StatusOK).json()
	require.Nil(t, envelopeNamed(t, cleared, "Groceries")["overwritten_target_amount"])
	require.Equal(t, "100.00", envelopeNamed(t, cleared, "Groceries")["target"])
}

func TestReleasingAnEnvelopesRolloverReturnsItToFreeToSpendExactlyOnce(t *testing.T) {
	// planned_spend reserves targets only, so the released money is already in
	// the plan's rollover. Adding it again would let the same dollar be spent
	// twice.
	l := planLedger(t)
	created := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	id := envelopeNamed(t, created, "Groceries")["id"].(string)

	carried := l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"rollover_amount": "40.00"}).requireStatus(http.StatusOK).json()
	envelope := envelopeNamed(t, carried, "Groceries")
	require.Equal(t, "40.00", envelope["rollover_amount"])
	require.Equal(t, "140.00", envelope["budget"])
	require.Equal(t, "90.00", envelope["available"])
	require.Equal(t, "with_rollover", envelope["state"])
	require.Equal(t, "-100.00", effective(t, carried, "planned_spend"),
		"carried funds are not reserved a second time by the plan")

	released := l.alex.post("/spending-plan/"+augustMonth+"/envelopes/"+id+"/release", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "0.00", envelopeNamed(t, released, "Groceries")["rollover_amount"])
	require.Equal(t, carried["left_this_month"], released["left_this_month"])
}

func TestReleaseAllEmptiesEveryEnvelopesRollover(t *testing.T) {
	l := planLedger(t)
	first := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	firstID := envelopeNamed(t, first, "Groceries")["id"].(string)
	second := createEnvelopeIn(l, augustMonth, "Food", "50.00")
	secondID := envelopeNamed(t, second, "Food")["id"].(string)

	for _, id := range []string{firstID, secondID} {
		l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
			map[string]any{"rollover_amount": "25.00"}).requireStatus(http.StatusOK)
	}

	month := l.alex.post("/spending-plan/"+augustMonth+"/envelopes/release-all", nil).
		requireStatus(http.StatusOK).json()
	for _, envelope := range envelopes(month) {
		require.Equal(t, "0.00", envelope["rollover_amount"])
	}
}

func TestAnEnvelopeCanBeFlippedBetweenRecurringAndThisMonthOnly(t *testing.T) {
	// The *Edit expense series* dialog: recurring is what the envelope is, not
	// what this month did with it, and creation is not the last chance to say
	// so.
	l := planLedger(t)
	created := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	envelope := envelopeNamed(t, created, "Groceries")
	require.Equal(t, true, envelope["recurring"], "created recurring by default")
	id := envelope["id"].(string)

	flipped := l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"recurring": false}).requireStatus(http.StatusOK).json()
	require.Equal(t, false, envelopeNamed(t, flipped, "Groceries")["recurring"])

	restored := l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"recurring": true}).requireStatus(http.StatusOK).json()
	require.Equal(t, true, envelopeNamed(t, restored, "Groceries")["recurring"])
}

func TestAnEnvelopeWithNoCategoriesIsRefused(t *testing.T) {
	// A filter with no items matches everything: the whole ledger would land in
	// one envelope and Other Spend would read zero.
	l := planLedger(t)
	response := l.alex.post("/spending-plan/"+augustMonth+"/envelopes", map[string]any{
		"name": "Everything", "target_amount": "100.00", "category_ids": []string{},
	})
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "category_ids")
}

// --- Projection --------------------------------------------------------------

func TestTheProjectionMethodIsStoredSoTheFigureCanBeExplained(t *testing.T) {
	l := planLedger(t)
	month := l.alex.patch("/spending-plan/"+augustMonth+"/projection", map[string]any{
		"type": "average_n_months", "window_months": 6, "buffer": "10.00",
	}).requireStatus(http.StatusOK).json()

	projection := month["projection"].(map[string]any)
	require.Equal(t, "average_n_months", projection["type"])
	require.Equal(t, float64(6), projection["window_months"])
	require.Equal(t, "10.00", projection["buffer"])

	require.Equal(t, projection, planFor(l, augustMonth)["projection"].(map[string]any))
}

func TestAProjectionNeverClaimsTheUserUnspentMoney(t *testing.T) {
	// prior_month on a light prior month would otherwise report more left than
	// the user has.
	l := planLedger(t)
	planFor(l, julyMonth)
	month := l.alex.patch("/spending-plan/"+augustMonth+"/projection",
		map[string]any{"type": "prior_month"}).requireStatus(http.StatusOK).json()

	// July spent 100, August has spent 75 already: the floor does not bite.
	require.Equal(t, "100.00", month["projected_other_spending"])
	require.Equal(t, "75.00", month["other_spend_to_date"])
}

func TestAnUnknownProjectionMethodIsRefused(t *testing.T) {
	l := planLedger(t)
	response := l.alex.patch("/spending-plan/"+augustMonth+"/projection",
		map[string]any{"type": "vibes"})
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "run_rate")

	l.alex.patch("/spending-plan/"+augustMonth+"/projection",
		map[string]any{"type": "run_rate", "window_months": 500}).
		requireStatus(http.StatusUnprocessableEntity)
}

// --- The rows behind a bucket ------------------------------------------------

func entriesIn(t *testing.T, month map[string]any, key, list string) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, raw := range planBucket(t, month, key)[list].([]any) {
		out = append(out, raw.(map[string]any))
	}
	return out
}

func seedSalary(l *ledger) uuid.UUID {
	return seedSeries(l, seriesSeed{
		Key: "salary", Name: "Salary", Kind: domain.SeriesIncome, Amount: "2000.00",
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{25},
		StartOn:   domain.NewDate(2026, time.January, 25),
		NextDueOn: domain.NewDate(2026, time.August, 25),
	})
}

func TestABucketNamesItsRowsAndNotOnlyTheirIds(t *testing.T) {
	// The id list is the audit trail; the panel needs the row. Sending only
	// the ids leaves the client to fetch the month's transactions back to draw
	// one bucket, and a second read is a second chance to disagree.
	l := planLedger(t)
	month := planFor(l, augustMonth)

	rows := entriesIn(t, month, "other_spend", "contributing")
	require.Len(t, rows, 2)
	byName := map[string]map[string]any{}
	for _, row := range rows {
		byName[row["name"].(string)] = row
	}

	safeway := byName["Safeway"]
	require.Equal(t, l.str("august_groceries"), safeway["id"])
	require.Equal(t, l.str("august_groceries"), safeway["txn_id"])
	require.Nil(t, safeway["series_id"])
	require.Equal(t, "2026-08-05", safeway["due_on"])
	// "Paid", not a status of its own: having no series behind it is a fact
	// about where the row came from, not about what happened to the money.
	require.Equal(t, "paid", safeway["status"])
	require.Equal(t, "Groceries", safeway["category_name"])
	require.Equal(t, "-50.00", safeway["amount"])
	require.Nil(t, safeway["group"])
	require.Equal(t, false, safeway["is_transfer"])
	require.Nil(t, byName["Corner Store"]["category_name"])

	// The named rows and the audit trail are the same rows.
	require.ElementsMatch(t,
		planBucket(t, month, "other_spend")["contributing_txn_ids"],
		[]any{safeway["id"], byName["Corner Store"]["id"]})
}

func TestABillNothingHasPostedAgainstIsStillARowInItsBucket(t *testing.T) {
	// No posting can supply it, which is why the entries are built from the
	// occurrence expansion and not from a lookup over the audit trail.
	l := planLedger(t)
	series := seedPowerBill(l)

	rows := entriesIn(t, planFor(l, augustMonth), "bills", "contributing")
	require.Len(t, rows, 1)
	require.Equal(t, fmt.Sprintf("%s:2026-08-12", series), rows[0]["id"])
	require.Nil(t, rows[0]["txn_id"])
	require.Equal(t, series.String(), rows[0]["series_id"])
	require.Equal(t, "bill", rows[0]["group"])
	require.Equal(t, "-60.00", rows[0]["amount"])
	// Due on the 12th, and the server's today is the 20th.
	require.Equal(t, "past_due", rows[0]["status"])
}

func TestAPostedBillIsOneRowAndReadsAsPaid(t *testing.T) {
	l := planLedger(t)
	series := seedPowerBill(l)
	seedTxn(l, "power_paid", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 12),
		Amount: domain.MustFromString("-70.00"), StatementName: "CITY POWER",
		Payee: "City Power", SeriesID: series, SeriesDueOn: domain.NewDate(2026, time.August, 12),
	})

	rows := entriesIn(t, planFor(l, augustMonth), "bills", "contributing")
	require.Len(t, rows, 1, "the occurrence and the row that fulfilled it are one entry, not two")
	require.Equal(t, "paid", rows[0]["status"])
	require.Equal(t, l.str("power_paid"), rows[0]["txn_id"])
	require.Equal(t, "-70.00", rows[0]["amount"])
}

func TestAnExcludedRowIsNamedInTheBucketItWasDroppedFrom(t *testing.T) {
	// The excluded list is the difference between the engine's figure and the
	// one on screen. A bucket the user cannot reconcile is one they stop
	// trusting.
	l := planLedger(t)
	excluded := l.alex.post("/spending-plan/"+augustMonth+"/buckets/other_spend/exclusions",
		map[string]any{"entry_id": l.str("august_groceries")}).
		requireStatus(http.StatusOK).json()

	require.Len(t, entriesIn(t, excluded, "other_spend", "contributing"), 1)
	rows := entriesIn(t, excluded, "other_spend", "excluded")
	require.Len(t, rows, 1)
	require.Equal(t, "Safeway", rows[0]["name"])
	require.Equal(t, "-50.00", rows[0]["amount"])
}

// familyPayment is a payment split across two categories, every part filed:
// the parent carries no category, as every saved split row's does not.
func familyPayment(l *ledger, on domain.Date) uuid.UUID {
	l.t.Helper()
	return seedTxn(l, "family_payment", &store.Transaction{
		AccountID: l.id("checking"), Date: on, Amount: domain.MustFromString("-90.00"),
		StatementName: "ZELLE TO COUSIN", Payee: "Cousin Pat",
		Splits: []store.Split{
			{Position: 0, Amount: domain.MustFromString("-60.00"), CategoryID: l.id("groceries")},
			{Position: 1, Amount: domain.MustFromString("-30.00"), CategoryID: l.id("food")},
		},
	})
}

func TestAnExcludedSplitRowIsNamedByItsSplitsCategories(t *testing.T) {
	l := planLedger(t)
	familyPayment(l, domain.NewDate(2026, time.August, 12))
	excluded := l.alex.post("/spending-plan/"+augustMonth+"/buckets/other_spend/exclusions",
		map[string]any{"entry_id": l.str("family_payment")}).
		requireStatus(http.StatusOK).json()

	rows := entriesIn(t, excluded, "other_spend", "excluded")
	require.Len(t, rows, 1)
	require.Equal(t, "Cousin Pat", rows[0]["name"])
	require.Equal(t, "Groceries, Food & Dining", rows[0]["category_name"],
		"a split row is filed under its splits, never Uncategorized")
}

func TestAClosedMonthsChartFilesASplitRowUnderItsSplits(t *testing.T) {
	// A closed month froze transaction ids, not parts. Walked as whole rows,
	// a split row is one part with no category and becomes an Uncategorized
	// bubble of its own.
	l := planLedger(t)
	_, err := l.env.DB.Pool().Exec(t.Context(),
		`UPDATE transactions SET category_id = $2 WHERE id = $1`, l.id("july"), l.id("groceries"))
	require.NoError(t, err)
	familyPayment(l, domain.NewDate(2026, time.July, 20))
	require.Equal(t, "-190.00", effective(t, planFor(l, julyMonth), "other_spend"))
	closeMonth(l, julyMonth, "-190.00")

	frozen := planFor(l, julyMonth)
	require.Equal(t, true, frozen["is_closed_out"])
	slices := frozen["other_spend_by_category"].([]any)
	require.Len(t, slices, 1, "everything in July is filed under Food & Dining: %v", slices)
	group := slices[0].(map[string]any)
	require.Equal(t, "Food & Dining", group["category_name"])
	require.Equal(t, "190.00", group["spent"])
	leaves := map[string]string{}
	for _, raw := range group["children"].([]any) {
		leaf := raw.(map[string]any)
		leaves[leaf["category_name"].(string)] = leaf["spent"].(string)
	}
	require.Equal(t, "160.00", leaves["Groceries"], "the July row and the payment's grocery split")
}

func TestIncomeCarriesItsOwnOccurrenceRowsAndStaysOutOfTheBillsLine(t *testing.T) {
	l := planLedger(t)
	seedSalary(l)
	month := planFor(l, augustMonth)

	require.Equal(t, "2000.00", effective(t, month, "income"))
	rows := entriesIn(t, month, "income", "contributing")
	require.Len(t, rows, 1)
	require.Equal(t, "Salary", rows[0]["name"])
	require.Equal(t, "2000.00", rows[0]["amount"])
	// The 25th has not arrived; a paycheck that has not landed is upcoming,
	// never overdue.
	require.Equal(t, "upcoming", rows[0]["status"])
	require.Nil(t, rows[0]["group"], "income is one undivided list")
	require.Empty(t, billRows(month))
}

func TestAnUnfulfilledIncomeOccurrenceIsExcludedByItsSlot(t *testing.T) {
	// The exclusion is filed under the income family. Reading only the bills
	// one left an excluded paycheck counting, with nothing on screen to say so.
	l := planLedger(t)
	series := seedSalary(l)
	slot := fmt.Sprintf("%s:2026-08-25", series)

	excluded := l.alex.post("/spending-plan/"+augustMonth+"/buckets/income/exclusions",
		map[string]any{"entry_id": slot}).requireStatus(http.StatusOK).json()

	require.Equal(t, "0.00", effective(t, excluded, "income"))
	require.Equal(t, []any{slot}, planBucket(t, excluded, "income")["excluded_entry_ids"])
	rows := entriesIn(t, excluded, "income", "excluded")
	require.Len(t, rows, 1)
	require.Equal(t, slot, rows[0]["id"])
	require.Empty(t, entriesIn(t, excluded, "income", "contributing"))
}

func TestOtherSpendIsBrokenOutByCategoryGroupForTheChart(t *testing.T) {
	l := planLedger(t)
	month := planFor(l, augustMonth)

	slices := month["other_spend_by_category"].([]any)
	require.Len(t, slices, 2)

	// The top level is the group, not the leaf. Forty leaf categories drawn as
	// forty circles is the same total rendered unreadably, and "which group is
	// this in" is the question somebody clicking a bubble is asking.
	first := slices[0].(map[string]any)
	require.Equal(t, "Food & Dining", first["category_name"])
	require.Equal(t, l.str("food"), first["category_id"])
	require.Equal(t, "50.00", first["spent"], "positive, and the largest share first")

	inside := first["children"].([]any)
	require.Len(t, inside, 1)
	leaf := inside[0].(map[string]any)
	require.Equal(t, "Groceries", leaf["category_name"])
	require.Equal(t, l.str("groceries"), leaf["category_id"])
	require.Equal(t, "50.00", leaf["spent"])

	// A category with no parent is its own group and expands into nothing: a
	// group of one would open into a copy of itself.
	second := slices[1].(map[string]any)
	require.Equal(t, "Uncategorized", second["category_name"])
	require.Nil(t, second["category_id"])
	require.Equal(t, "25.00", second["spent"])
	require.Empty(t, second["children"])

	// Every slice names the rows behind it, so the panel can list them without
	// a query that could disagree with the figure printed over it.
	require.Len(t, first["txn_ids"], 1)
	require.Len(t, leaf["txn_ids"], 1)
	require.Equal(t, first["txn_ids"], leaf["txn_ids"])

	// The slices are the bucket split up, not a second pass over the ledger.
	require.Equal(t, "75.00", month["other_spend_to_date"])
}

func TestAnEnvelopeNamesTheCategoriesItsFilterSelects(t *testing.T) {
	// Ground rule 3: the membership rule lives in the filter, and the chips
	// beside the envelope's name are a rendering of it rather than a copy.
	l := planLedger(t)
	envelope := envelopeNamed(t, createEnvelopeIn(l, augustMonth, "Groceries", "100.00"), "Groceries")

	require.Equal(t, []any{map[string]any{"id": l.str("groceries"), "name": "Groceries"}},
		envelope["categories"])
}

func TestAFrozenMonthSaysWhenItWasClosed(t *testing.T) {
	l := planLedger(t)
	planFor(l, julyMonth)
	closeMonth(l, julyMonth, "-10.00")

	require.Equal(t, "2026-08-20", planFor(l, julyMonth)["closed_out_at"])
	require.Nil(t, planFor(l, augustMonth)["closed_out_at"])
}

// --- Money on the wire -------------------------------------------------------

func TestMoneyCrossesTheSpendingPlanWireAsAString(t *testing.T) {
	l := planLedger(t)
	month := planFor(l, augustMonth)
	require.IsType(t, "", month["left_this_month"])
	require.IsType(t, "", month["other_spend_to_date"])
	require.IsType(t, "", effective(t, month, "other_spend"))
	require.IsType(t, "", calculated(t, month, "other_spend"))
}

func TestAJsonNumberInASpendingPlanMoneyFieldIsRefused(t *testing.T) {
	l := planLedger(t)
	override := l.alex.raw(http.MethodPatch, "/spending-plan/"+augustMonth+"/buckets/other_spend",
		`{"overwritten_amount": -10.5}`)
	override.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, override.Body.String(), "string")

	envelope := l.alex.raw(http.MethodPost, "/spending-plan/"+augustMonth+"/envelopes", fmt.Sprintf(
		`{"name": "Groceries", "target_amount": 100, "category_ids": [%q]}`, l.str("groceries")))
	envelope.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, envelope.Body.String(), "string")
}

func TestAMonthPathThatIsNotAMonthIsRefused(t *testing.T) {
	l := planLedger(t)
	l.alex.get("/spending-plan/2026-08-01").requireStatus(http.StatusUnprocessableEntity)
	l.alex.get("/spending-plan/august").requireStatus(http.StatusUnprocessableEntity)
}

// --- Tenancy -----------------------------------------------------------------

func seedStrangerEnvelope(l *ledger) (uuid.UUID, uuid.UUID) {
	l.t.Helper()
	monthID, envelopeID := uuid.New(), uuid.New()
	_, err := l.env.DB.Pool().Exec(l.t.Context(), `
		INSERT INTO spending_plan_months (id, space_id, month, projection_type, projection_window_months)
		VALUES ($1, $2, $3, 'run_rate', 3)`,
		monthID, l.id("other_space"), monthStart(l.t, augustMonth))
	require.NoError(l.t, err)
	_, err = l.env.DB.Pool().Exec(l.t.Context(), `
		INSERT INTO envelopes (id, space_id, spending_plan_month_id, filter_id, name, target_amount)
		VALUES ($1, $2, $3, $4, 'Theirs', 100)`,
		envelopeID, l.id("other_space"), monthID, l.id("stranger_filter"))
	require.NoError(l.t, err)
	return monthID, envelopeID
}

func TestASpendingPlanRowFromAnotherSpaceIsInvisible(t *testing.T) {
	l := planLedger(t)
	_, envelopeID := seedStrangerEnvelope(l)

	l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+envelopeID.String(),
		map[string]any{"rollover_amount": "10.00"}).requireStatus(http.StatusNotFound)
	l.alex.post("/spending-plan/"+augustMonth+"/envelopes/"+envelopeID.String()+"/release", nil).
		requireStatus(http.StatusNotFound)

	// And this space's plan never shows it.
	require.Empty(t, envelopes(planFor(l, augustMonth)))
}

func TestAnEnvelopeCannotBorrowAnotherSpacesCategory(t *testing.T) {
	l := planLedger(t)
	l.alex.post("/spending-plan/"+augustMonth+"/envelopes", map[string]any{
		"name": "Theirs", "target_amount": "100.00",
		"category_ids": []string{l.str("stranger_category")},
	}).requireStatus(http.StatusConflict)
}

func TestAViewerReadsTheSpendingPlanButCannotChangeIt(t *testing.T) {
	l := planLedger(t)
	vera := l.as("vera")
	vera.get("/spending-plan/" + augustMonth).requireStatus(http.StatusOK)

	vera.patch("/spending-plan/"+augustMonth+"/buckets/other_spend",
		map[string]any{"overwritten_amount": "-1.00"}).requireStatus(http.StatusForbidden)
	vera.post("/spending-plan/"+augustMonth+"/buckets/other_spend/exclusions",
		map[string]any{"entry_id": l.str("august_groceries")}).requireStatus(http.StatusForbidden)
	vera.post("/spending-plan/"+augustMonth+"/envelopes", map[string]any{
		"name": "Groceries", "target_amount": "100.00",
		"category_ids": []string{l.str("groceries")},
	}).requireStatus(http.StatusForbidden)
	vera.patch("/spending-plan/"+augustMonth+"/projection",
		map[string]any{"type": "prior_month"}).requireStatus(http.StatusForbidden)
}

// Editing an envelope's membership.
//
// An envelope stores its categories in a filter of its own — ground rule 3 —
// capable of holding several. A month's PATCH that cannot reach it would
// leave an envelope stuck with whatever categories it was created with, and
// a widening would mean deleting it and losing its rollover.

func TestAnEnvelopeCanBeWidenedToASecondCategory(t *testing.T) {
	l := planLedger(t)
	month := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	envelope := envelopeNamed(t, month, "Groceries")
	require.Equal(t, "50.00", envelope["spent"], "the seeded August groceries charge")
	require.Len(t, envelope["categories"].([]any), 1)

	// File the corner store under Food, so widening has something to pick up.
	l.alex.patch("/transactions/"+l.str("august_corner"),
		map[string]any{"category_id": l.str("food")}).requireStatus(http.StatusOK)

	after := l.alex.patch(
		"/spending-plan/"+augustMonth+"/envelopes/"+envelope["id"].(string),
		map[string]any{"category_ids": []string{l.str("groceries"), l.str("food")}},
	).requireStatus(http.StatusOK).json()

	widened := envelopeNamed(t, after, "Groceries")
	require.Equal(t, "75.00", widened["spent"],
		"the second category's spend did not follow the envelope")
	require.Len(t, widened["categories"].([]any), 2)
	require.Len(t, widened["txn_ids"].([]any), 2)

	// And the money did not land in two places at once: a row an envelope
	// claims is not also other spending.
	require.Equal(t, envelope["filter_id"], widened["filter_id"],
		"the envelope was repointed at a new filter instead of keeping its own")
}

func TestNarrowingAnEnvelopeGivesTheSpendBack(t *testing.T) {
	l := planLedger(t)
	month := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	id := envelopeNamed(t, month, "Groceries")["id"].(string)

	after := l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"category_ids": []string{l.str("food")}}).
		requireStatus(http.StatusOK).json()

	narrowed := envelopeNamed(t, after, "Groceries")
	require.Equal(t, "0.00", narrowed["spent"],
		"the groceries charge is no longer this envelope's")
	require.Empty(t, narrowed["txn_ids"].([]any))
}

func TestAnEnvelopeCanBeRenamedWhenItsCategoriesChange(t *testing.T) {
	// A "Groceries" envelope widened to cover dining out is not called
	// Groceries any more, and an edit that cannot fix the name leaves a label
	// that lies about what the row counts.
	l := planLedger(t)
	month := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	id := envelopeNamed(t, month, "Groceries")["id"].(string)

	after := l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"name": "Food & Drink"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "Food & Drink", envelopeNamed(t, after, "Food & Drink")["name"])

	l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"name": "   "}).requireStatus(http.StatusUnprocessableEntity)
}

func TestAnEnvelopeCannotBeEmptiedOfCategories(t *testing.T) {
	// A filter with no items matches everything, so an emptied envelope would
	// claim the whole ledger and read Other Spend as zero.
	l := planLedger(t)
	month := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	envelope := envelopeNamed(t, month, "Groceries")
	id := envelope["id"].(string)

	l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"category_ids": []string{}}).requireStatus(http.StatusUnprocessableEntity)

	// The generic filters route is the other way in to the same filter.
	l.alex.patch("/filters/"+envelope["filter_id"].(string),
		map[string]any{"items": []any{}}).requireStatus(http.StatusUnprocessableEntity)

	// A saved view has no money resting on it, so emptying one is just an
	// unfiltered view rather than a wrong answer.
	saved := l.alex.post("/filters", map[string]any{
		"name": "Everything", "scope": "saved_view",
		"items": []any{map[string]any{
			"field": "category", "operator": "in", "value_ids": []string{l.str("groceries")},
		}},
	}).requireStatus(http.StatusCreated).json()
	l.alex.patch("/filters/"+saved["id"].(string),
		map[string]any{"items": []any{}}).requireStatus(http.StatusOK)

	// Nothing moved.
	still := envelopeNamed(t, planFor(l, augustMonth), "Groceries")
	require.Len(t, still["categories"].([]any), 1)
	require.Equal(t, "50.00", still["spent"])
}

func TestAnEnvelopeCannotBorrowACategoryFromAnotherSpace(t *testing.T) {
	l := planLedger(t)
	month := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	id := envelopeNamed(t, month, "Groceries")["id"].(string)

	// The same answer creating one gives: a conflict, because the body was
	// well-formed and the caller could not have known.
	l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"category_ids": []string{l.str("stranger_category")}}).
		requireStatus(http.StatusConflict)
}

func TestAViewerCannotRepointAnEnvelope(t *testing.T) {
	l := planLedger(t)
	month := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	id := envelopeNamed(t, month, "Groceries")["id"].(string)

	l.as("vera").patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"category_ids": []string{l.str("food")}}).
		requireStatus(http.StatusForbidden)
}

func TestRenamingAnEnvelopeRenamesEveryMonthOfIt(t *testing.T) {
	// A recurring envelope is one filter and a row per month, so its
	// categories were already one rule across all of them. A rename that
	// stopped at this month would leave the plan holding one envelope under
	// two names, and would make one dialog do two different things.
	l := planLedger(t)
	month := createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	id := envelopeNamed(t, month, "Groceries")["id"].(string)

	// Materialize the next month, which copies the envelope forward.
	envelopeNamed(t, planFor(l, "2026-09"), "Groceries")

	l.alex.patch("/spending-plan/"+augustMonth+"/envelopes/"+id,
		map[string]any{"name": "Food & Drink"}).requireStatus(http.StatusOK)

	for _, m := range []string{augustMonth, "2026-09"} {
		names := []string{}
		for _, envelope := range envelopes(planFor(l, m)) {
			names = append(names, envelope["name"].(string))
		}
		require.Equal(t, []string{"Food & Drink"}, names, "%s", m)
	}
}

// --- Envelopes across months --------------------------------------------------

func TestARecurringEnvelopeReachesTheNextMonthWithWhatItLeft(t *testing.T) {
	l := planLedger(t)
	createEnvelopeIn(l, augustMonth, "Groceries", "100.00")

	september := envelopeNamed(t, planFor(l, "2026-09"), "Groceries")

	require.Equal(t, "100.00", september["target"])
	require.Equal(t, "50.00", september["rollover_amount"], "August spent 50 of 100")
	require.Equal(t, "150.00", september["budget"])
	require.Equal(t, "-100.00", effective(t, planFor(l, "2026-09"), "planned_spend"),
		"September reserves the target, and the carried funds stay in the rollover bucket")
}

func TestAMonthSkippedOverStillCarriesTheEnvelope(t *testing.T) {
	l := planLedger(t)
	createEnvelopeIn(l, augustMonth, "Groceries", "100.00")

	november := envelopeNamed(t, planFor(l, "2026-11"), "Groceries")

	// September and October spent nothing, so each carried its whole budget on.
	require.Equal(t, "250.00", november["rollover_amount"])
	envelopeNamed(t, planFor(l, "2026-10"), "Groceries")
}

func TestACarriedRolloverFollowsALaterChangeToThePriorMonth(t *testing.T) {
	l := planLedger(t)
	createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	require.Equal(t, "50.00", envelopeNamed(t, planFor(l, "2026-09"), "Groceries")["rollover_amount"])

	l.alex.patch("/transactions/"+l.str("august_groceries"), map[string]any{
		"excluded_from_spending_plan": true,
	}).requireStatus(http.StatusOK)

	require.Equal(t, "100.00", envelopeNamed(t, planFor(l, "2026-09"), "Groceries")["rollover_amount"],
		"September was materialized before the August change and must not keep the old carry")
}

func TestARolloverTheUserSetStaysTheirs(t *testing.T) {
	l := planLedger(t)
	createEnvelopeIn(l, augustMonth, "Groceries", "100.00")
	id := envelopeNamed(t, planFor(l, "2026-09"), "Groceries")["id"].(string)

	l.alex.patch("/spending-plan/2026-09/envelopes/"+id,
		map[string]any{"rollover_amount": "20.00"}).requireStatus(http.StatusOK)
	l.alex.patch("/transactions/"+l.str("august_groceries"), map[string]any{
		"excluded_from_spending_plan": true,
	}).requireStatus(http.StatusOK)

	require.Equal(t, "20.00", envelopeNamed(t, planFor(l, "2026-09"), "Groceries")["rollover_amount"])
}

func TestAThisMonthOnlyEnvelopeStaysInItsMonth(t *testing.T) {
	l := planLedger(t)
	l.alex.post("/spending-plan/"+augustMonth+"/envelopes", map[string]any{
		"name": "Party", "target_amount": "60.00", "recurring": false,
		"category_ids": []string{l.str("groceries")},
	}).requireStatus(http.StatusOK)

	require.Empty(t, envelopes(planFor(l, "2026-09")))
}

func TestTwoFirstReadsOfAMonthAtOnceBothSucceedAndAgree(t *testing.T) {
	// The dashboard and the plan page both ask for a month nobody has opened.
	// Both find no row; the loser of the insert must not answer 500.
	l := planLedger(t)
	createEnvelopeIn(l, augustMonth, "Groceries", "100.00")

	const readers = 6
	responses := make([]*response, readers)
	var wg sync.WaitGroup
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses[i] = l.alex.get("/spending-plan/2026-10")
		}()
	}
	wg.Wait()

	for _, got := range responses {
		got.requireStatus(http.StatusOK)
	}
	october := planFor(l, "2026-10")
	require.Len(t, envelopes(october), 1, "one envelope, not one per reader")
	require.Equal(t, envelopeNamed(t, october, "Groceries")["id"],
		envelopeNamed(t, responses[0].json(), "Groceries")["id"])
}

// --- Money spent on a goal ---------------------------------------------------

// Filing a charge under a goal takes it out of the plan, and unfiling puts it
// back.
//
// It was planned, and it was planned outside the month's bounds: the household
// decided on the trip over the months they saved for it, and August did not
// budget for the flights. Charging them to Other Spend makes a month read as a
// catastrophic overspend for a purchase that went exactly to plan.
//
// The direction has to survive the trip through the plan's own loader, which
// is what this asserts that the domain test cannot: a link whose kind is lost
// on the way reads as a contribution, and the same row would then reserve
// itself in the Goals bucket while still sitting in Other Spend.
func TestAChargeFiledUnderAGoalLeavesTheSpendingPlan(t *testing.T) {
	l := planLedger(t)
	baseline := planFor(l, augustMonth)
	require.Equal(t, "-75.00", effective(t, baseline, "other_spend"))

	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	id := goal["id"].(string)
	l.alex.post("/goals/"+id+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{l.str("august_groceries")}, "direction": "spending",
	}).requireStatus(http.StatusOK)

	filed := planFor(l, augustMonth)
	require.Equal(t, "-25.00", effective(t, filed, "other_spend"),
		"the 50.00 of groceries was filed under the goal")
	require.Equal(t, "0.00", effective(t, filed, "goals"),
		"spending does not reserve: the money was set aside in earlier months")
	require.NotContains(t,
		planBucket(t, filed, "other_spend")["contributing_txn_ids"].([]any),
		l.str("august_groceries"))

	// The exclusion is the link, not a flag written on the row.
	l.alex.del("/goals/" + id + "/transactions/" + l.str("august_groceries")).
		requireStatus(http.StatusOK)
	require.Equal(t, "-75.00", effective(t, planFor(l, augustMonth), "other_spend"))
}

// A withdrawal must not read as a contribution once it reaches the plan. It is
// the same row shape and the same sign as the money that went in, and only the
// recorded direction tells them apart.
func TestAWithdrawalDoesNotReserveMoneyInTheGoalsBucket(t *testing.T) {
	l := planLedger(t)
	goal := newGoal(l, map[string]any{
		"name": "Japan Trip", "account_id": savingsAccount(l), "target_amount": "15000.00",
	})
	id := goal["id"].(string)

	l.alex.post("/goals/"+id+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{l.str("august_groceries")}, "direction": "contribution",
	}).requireStatus(http.StatusOK)
	require.Equal(t, "-50.00", effective(t, planFor(l, augustMonth), "goals"))

	l.alex.post("/goals/"+id+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{l.str("august_groceries")}, "direction": "withdrawal",
	}).requireStatus(http.StatusOK)
	require.Equal(t, "50.00", effective(t, planFor(l, augustMonth), "goals"),
		"money taken back out is money the month no longer reserves")
}

// A skipped bill stops costing the month.
//
// A plan that cannot see a skip at all — it is written as a deleted
// transaction, and the plan's ledger is the register's, with no deleted rows
// — would let the Bills screen show the occurrence skipped, the alert sweep
// go quiet, and the plan go on reserving the full amount, leaving
// free-to-spend low for the rest of the month with nothing on screen to
// explain it.
func TestASkippedOccurrenceStopsCostingTheMonth(t *testing.T) {
	l := planLedger(t)
	due := domain.NewDate(2026, time.August, 25)
	seedSeries(l, seriesSeed{
		Key: "rent", Name: "Rent", Kind: domain.SeriesBill, Amount: "-1200.00",
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{due.Day},
		StartOn: due, NextDueOn: due,
	})

	before := planFor(l, augustMonth)
	require.Equal(t, "-1200.00", effective(t, before, "bills"),
		"the bill is expected and the month reserves for it")

	l.alex.post("/occurrences/skip", map[string]any{
		"series_id": l.str("rent"), "due_on": due.String(),
	}).requireStatus(http.StatusNoContent)

	after := planFor(l, augustMonth)
	require.Equal(t, "0.00", effective(t, after, "bills"),
		"the household said it is not happening")

	// The occurrence is still listed, flagged, the way an excluded one is —
	// the screen has to be able to show what happened to it.
	var found bool
	for _, raw := range after["bills"].([]any) {
		row := raw.(map[string]any)
		if row["series_id"] == l.str("rent") {
			found = true
			require.Equal(t, true, row["is_excluded"])
		}
	}
	require.True(t, found, "a skipped bill is still a row on the plan")
}

// --- The category's own exclusion -------------------------------------------

// Ground rule 4's third level: the categories table carries
// `excluded_from_reports` and `excluded_from_spending_plan`, the settings
// screen offers both and the importer maps both from Simplifi, so a
// row→domain mapping that dropped them would leave ticking "exclude from the
// spending plan" on a category changing nothing at all.
//
// August is seeded with 50.00 of groceries and 25.00 uncategorized at the
// corner store.

func TestACategoryExcludedFromThePlanStopsConsumingFreeToSpend(t *testing.T) {
	l := planLedger(t)
	require.Equal(t, "-75.00", effective(t, planFor(l, augustMonth), "other_spend"))

	l.alex.patch("/categories/"+l.str("groceries"),
		map[string]any{"excluded_from_spending_plan": true}).requireStatus(http.StatusOK)

	month := planFor(l, augustMonth)
	require.Equal(t, "-25.00", effective(t, month, "other_spend"),
		"the excluded category is still spending the month's money")
	require.Equal(t, "-25.00", month["left_this_month"])

	// And the bucket's audit trail agrees with its figure, rather than still
	// naming a row that no longer counts.
	require.Equal(t, []any{l.str("august_corner")},
		planBucket(t, month, "other_spend")["contributing_txn_ids"])
}

func TestExcludingACategoryFromReportsLeavesTheSpendingPlanAlone(t *testing.T) {
	// The pair is independent at the category level exactly as it is at the
	// account and transaction levels. A user saying "do not report this" has
	// not said "do not budget for this".
	l := planLedger(t)

	l.alex.patch("/categories/"+l.str("groceries"),
		map[string]any{"excluded_from_reports": true}).requireStatus(http.StatusOK)

	require.Equal(t, "-75.00", effective(t, planFor(l, augustMonth), "other_spend"))
}

func TestAnUncategorizedRowIsUntouchedByAnyCategorysExclusion(t *testing.T) {
	// The corner store row has no category. Uncategorized spending is real
	// spending, and no category's flag may reach it.
	l := planLedger(t)

	l.alex.patch("/categories/"+l.str("groceries"),
		map[string]any{"excluded_from_spending_plan": true, "excluded_from_reports": true}).
		requireStatus(http.StatusOK)

	require.Equal(t, "-25.00", effective(t, planFor(l, augustMonth), "other_spend"))
}

// The row label the panel reads is the same definition the counting predicates
// use — domain.Posting.IsTransfer — and not a second copy of its two clauses
// sitting beside them. A label that drifts from the rule is a row the panel
// calls a transfer while the arithmetic treats it as spending.

func TestThePlanLabelsBothShapesOfATransfer(t *testing.T) {
	// The excluded list is the one place a posted row the plan does not count
	// still gets named, which makes it the only place this label is visible
	// on a transfer at all.
	l := planLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	transferCategory := &store.Category{
		Name: "Transfer", Kind: domain.CategoryTransfer,
		IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(t, db(t).CreateCategory(t.Context(), space, transferCategory))
	unmatched := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 11),
		Amount: domain.MustFromString("-300.00"), Currency: "USD",
		StatementName: "TRANSFER TO SAVINGS", Payee: "To Savings",
		CategoryID: transferCategory.ID, Source: domain.SourceSync,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, unmatched))

	for _, id := range []string{l.str("transfer_out"), unmatched.ID.String()} {
		l.alex.post("/spending-plan/"+augustMonth+"/buckets/bills/exclusions",
			map[string]any{"entry_id": id}).requireStatus(http.StatusOK)
	}

	labels := map[string]any{}
	for _, entry := range entriesIn(t, planFor(l, augustMonth), "bills", "excluded") {
		labels[entry["id"].(string)] = entry["is_transfer"]
	}

	require.Equal(t, true, labels[l.str("transfer_out")],
		"a matched transfer leg is not labelled a transfer")
	require.Equal(t, true, labels[unmatched.ID.String()],
		"a row under a transfer category is not labelled a transfer")
}

// --- A split row is placed a part at a time -----------------------------------

// An envelope claims parts, not rows. Claiming the whole transaction when any
// one split matched would charge it money that belongs elsewhere — and, when
// two envelopes each matched a different split, give one of them the other's
// spending outright.
//
// Seeded August is groceries −50.00 and an uncategorized corner store −25.00.

func TestAnEnvelopeIsChargedOnlyTheSplitItMatches(t *testing.T) {
	l := planLedger(t)
	// One receipt, half groceries and half something no envelope wants.
	row := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 14),
		Amount: domain.MustFromString("-100.00"), Currency: "USD",
		StatementName: "BIG BOX #77", Payee: "Big Box",
		Splits: []store.Split{
			{Position: 0, Amount: domain.MustFromString("-60.00"), CategoryID: l.id("groceries")},
			{Position: 1, Amount: domain.MustFromString("-40.00"), CategoryID: l.id("food")},
		},
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), row))

	l.alex.post("/spending-plan/"+augustMonth+"/envelopes", map[string]any{
		"name": "Groceries", "target_amount": "500.00",
		"category_ids": []string{l.str("groceries")},
	}).requireStatus(http.StatusOK)

	month := planFor(l, augustMonth)
	groceries := envelopeNamed(t, month, "Groceries")

	// 50.00 seeded plus the 60.00 share, not the whole 100.00 receipt.
	require.Equal(t, "110.00", groceries["spent"],
		"the envelope was charged a split that is not its own")

	// The 40.00 that belongs to no envelope reaches Other Spend, with the
	// uncategorized corner store: nothing is lost between the two buckets.
	require.Equal(t, "-65.00", effective(t, month, "other_spend"))

	// And the chart under it agrees with it, a part at a time: the 40.00 is
	// filed under the category its split names, not the whole row under the
	// nothing its parent carries.
	slices := month["other_spend_by_category"].([]any)
	byName := map[string]string{}
	total := domain.Zero
	for _, raw := range slices {
		slice := raw.(map[string]any)
		byName[slice["category_name"].(string)] = slice["spent"].(string)
		total = total.Add(domain.MustFromString(slice["spent"].(string)))
	}
	require.Equal(t, "40.00", byName["Food & Dining"], "the split's own category: %v", byName)
	require.Equal(t, "25.00", byName["Uncategorized"])
	require.Equal(t, "65.00", total.String(), "the chart does not sum to its bucket")
}

func TestTheRowsListedUnderAnEnvelopeAddUpToWhatItSaysItSpent(t *testing.T) {
	// The dialog behind an envelope lists these entries under its spent figure.
	// Listing the whole receipt under a figure charged one share of it is the
	// same defect as a chart that does not sum to its bucket, and it is what a
	// list drawn from txn_ids alone can only do.
	l := planLedger(t)
	row := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 14),
		Amount: domain.MustFromString("-100.00"), Currency: "USD",
		StatementName: "BIG BOX #77", Payee: "Big Box",
		Splits: []store.Split{
			{Position: 0, Amount: domain.MustFromString("-60.00"), CategoryID: l.id("groceries")},
			{Position: 1, Amount: domain.MustFromString("-40.00"), CategoryID: l.id("food")},
		},
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), row))
	l.alex.post("/spending-plan/"+augustMonth+"/envelopes", map[string]any{
		"name": "Groceries", "target_amount": "500.00",
		"category_ids": []string{l.str("groceries")},
	}).requireStatus(http.StatusOK)

	month := planFor(l, augustMonth)
	groceries := envelopeNamed(t, month, "Groceries")

	entries := groceries["entries"].([]any)
	require.Len(t, entries, 2, "the seeded charge and this receipt's grocery share")

	listed := domain.Zero
	amounts := map[string]string{}
	for _, raw := range entries {
		entry := raw.(map[string]any)
		amount := domain.MustFromString(entry["amount"].(string))
		listed = listed.Add(amount)
		amounts[entry["name"].(string)] = amount.String()
		if entry["name"] == "Big Box" {
			require.Equal(t, true, entry["is_split"],
				"a share of a larger receipt is marked as one")
			require.Equal(t, "Groceries", entry["category_name"],
				"the split's own category, not its parent row's")
		}
	}

	require.Equal(t, "-60.00", amounts["Big Box"], "the whole receipt is -100.00")
	require.Equal(t, groceries["spent"], listed.Neg().String(),
		"the list does not add up to the figure printed over it: %v", amounts)

	// The row is still named once in the audit trail, whatever its parts did.
	require.Len(t, groceries["txn_ids"].([]any), 2)
}

func TestTwoEnvelopesSplitOneReceiptBetweenThem(t *testing.T) {
	// One row, two envelopes: a tie-break must not hand the whole amount to
	// whichever was created first.
	l := planLedger(t)
	row := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 14),
		Amount: domain.MustFromString("-90.00"), Currency: "USD",
		StatementName: "BIG BOX #77", Payee: "Big Box",
		Splits: []store.Split{
			{Position: 0, Amount: domain.MustFromString("-30.00"), CategoryID: l.id("groceries")},
			{Position: 1, Amount: domain.MustFromString("-60.00"), CategoryID: l.id("food")},
		},
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), row))

	for _, envelope := range []struct{ name, categoryID string }{
		{"Groceries", l.str("groceries")},
		{"Food", l.str("food")},
	} {
		l.alex.post("/spending-plan/"+augustMonth+"/envelopes", map[string]any{
			"name": envelope.name, "target_amount": "500.00",
			"category_ids": []string{envelope.categoryID},
		}).requireStatus(http.StatusOK)
	}

	month := planFor(l, augustMonth)
	require.Equal(t, "80.00", envelopeNamed(t, month, "Groceries")["spent"],
		"50.00 seeded plus this receipt's 30.00 groceries share")
	require.Equal(t, "60.00", envelopeNamed(t, month, "Food")["spent"],
		"the Food envelope reported nothing while Groceries carried its spending")

	// Only the uncategorized corner store is left over.
	require.Equal(t, "-25.00", effective(t, month, "other_spend"))
}

func TestTheMonthStillAddsUpWhenARowIsSplitAcrossBuckets(t *testing.T) {
	// The invariant the whole-row claim existed to protect, checked at the new
	// altitude: every part is claimed once or not at all, so the envelopes and
	// Other Spend together account for exactly the month's spending.
	l := planLedger(t)
	row := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 14),
		Amount: domain.MustFromString("-100.00"), Currency: "USD",
		StatementName: "BIG BOX #77", Payee: "Big Box",
		Splits: []store.Split{
			{Position: 0, Amount: domain.MustFromString("-60.00"), CategoryID: l.id("groceries")},
			{Position: 1, Amount: domain.MustFromString("-40.00"), CategoryID: l.id("food")},
		},
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), row))
	l.alex.post("/spending-plan/"+augustMonth+"/envelopes", map[string]any{
		"name": "Groceries", "target_amount": "500.00",
		"category_ids": []string{l.str("groceries")},
	}).requireStatus(http.StatusOK)

	month := planFor(l, augustMonth)
	spent := domain.MustFromString(envelopeNamed(t, month, "Groceries")["spent"].(string))
	other := domain.MustFromString(effective(t, month, "other_spend"))

	// Seeded 50 + 25, plus this receipt's 100: 175.00 of spending, wherever it
	// was filed.
	require.Equal(t, "-175.00", other.Sub(spent).String(),
		"the buckets no longer account for the month's spending exactly once")
}
