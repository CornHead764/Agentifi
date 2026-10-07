package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The golden test: does this app reproduce Simplifi's own figures?
//
// The import stores Simplifi's `calculated*` values verbatim, untouched by any
// arithmetic of ours. This recomputes each month from the imported
// transactions, series, envelopes and goals, and compares. Every mismatch is a
// finding: either Simplifi did something the calculations doc does not
// describe, or this app does.
//
// It needs a real export, which lives outside the repository because it is
// somebody's whole financial history. Without one the test skips — and says
// exactly what is missing, because a skipped test looks precisely like a
// passing one and this is the last test anybody should be fooled by.
//
//	AGENTIFI_GOLDEN_EXPORT=data/simplifi/simplifi-export-….json go test ./internal/api/ -run Golden

// goldenExportPath is the export the golden tests read, or "" when there is
// none.
func goldenExportPath() string {
	if path := os.Getenv("AGENTIFI_GOLDEN_EXPORT"); path != "" {
		return path
	}
	// The conventional home, so the usual case needs no environment.
	matches, _ := filepath.Glob("../../../data/simplifi/simplifi-export-*.json")
	sort.Strings(matches)
	if len(matches) > 0 {
		return matches[len(matches)-1]
	}
	return ""
}

// goldenExport finds the export, or explains its absence.
func goldenExport(t *testing.T) []byte {
	t.Helper()
	path := goldenExportPath()
	if path == "" {
		t.Skip("no Simplifi export: run tools/extractors/extract-simplifi.js and put the JSON " +
			"in data/simplifi/, or set AGENTIFI_GOLDEN_EXPORT.")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s", path)
	t.Logf("golden export: %s (%d bytes)", path, len(raw))
	return raw
}

// importGolden loads an export into a space of its own and returns it, with
// the day the export was captured.
//
// The space is named for the test, because the importer refuses a second space
// of the same name under the same owner — the guard against importing an export
// twice — and every golden test here imports the same file.
//
// The capture day is the extractor's exportedAt stamp, the moment it read
// Simplifi's stored figures. A figure that depends on what had happened yet is
// judged against that day, never against the day the test runs, or the result
// changes with the calendar while neither the export nor the code does.
func importGolden(t *testing.T, database *store.Store, raw []byte) (store.SpaceID, domain.Date) {
	t.Helper()
	export, err := importer.ParseExport(raw)
	require.NoError(t, err, "parsing the export")
	exportedAt, err := time.Parse(time.RFC3339, export.ExportedAt)
	require.NoErrorf(t, err, "the export's exportedAt %q is not a timestamp", export.ExportedAt)
	captured := domain.DateOf(exportedAt)
	t.Logf("golden export captured on %s", captured)

	mapped, err := importer.MapExport(export, "", importer.Options{SpaceName: t.Name()})
	require.NoError(t, err, "mapping the export")
	for _, problem := range mapped.Report.Errors {
		t.Errorf("mapping refused a record: %s", problem.Render())
	}
	require.True(t, mapped.Report.OK(), "the export could not be mapped")
	require.NotNil(t, mapped.Space, "the export produced no space")

	require.NoError(t, importer.Write(t.Context(), database, mapped), "writing the export")
	return mapped.Space.ID, captured
}

// goldenMonth is everything Simplifi stored for one month, read before
// anything here recomputes it: a plan read saves what it computed over the
// imported row.
type goldenMonth struct {
	// figures are the stored bucket amounts, already folded into Agentifi's
	// six buckets.
	figures map[domain.BucketKey]string
	// txnIDs are the rows Simplifi lists behind each bucket that lists any.
	txnIDs map[domain.BucketKey]map[string]bool
	// leftToSpend is Simplifi's month total, and leftIsTheSum whether it is
	// the plain sum of the stored figures; on a forecast month it is not.
	leftToSpend  string
	leftIsTheSum bool
	envelopes    map[string]goldenEnvelope
}

// goldenEnvelope is one envelope's stored spend, as a magnitude.
type goldenEnvelope struct {
	name  string
	spent string
	// listed is the same figure summed from the rows Simplifi lists behind
	// it. Where the two differ the stored figure was computed against rows
	// that have changed since, and nothing today can reproduce it.
	listed string
}

// storedMonths reads what the import wrote, which is Simplifi's own arithmetic.
//
// The two products do not carve the month up the same way, and the difference
// is deliberate. Simplifi keeps Bills, Subscriptions and Transfer as three
// stored figures; Agentifi's Bills bucket is the one line the Spending Plan
// shows for all three. Comparing bills against bills alone therefore reports a
// mismatch in any month that has a single subscription in it, and none of
// them is a defect.
//
// Every stored figure has to land in exactly one bucket below. A stored column
// that reached no bucket would be arithmetic nobody checked.
func storedMonths(t *testing.T, database *store.Store, spaceID store.SpaceID) map[domain.Month]*goldenMonth {
	t.Helper()
	rows, err := database.Pool().Query(t.Context(), `
		SELECT month,
		       calculated_income_amount::text, calculated_bills_amount::text,
		       calculated_subscriptions_amount::text, calculated_transfer_amount::text,
		       calculated_goals_amount::text, calculated_planned_spending_amount::text,
		       calculated_spent_amount::text, left_to_spend_amount::text,
		       income_txn_ids::text[],
		       (bills_txn_ids || subscriptions_txn_ids || transfer_txn_ids)::text[],
		       spent_txn_ids::text[]
		  FROM spending_plan_months WHERE space_id = $1 ORDER BY month`, spaceID.UUID())
	require.NoError(t, err)
	defer rows.Close()

	set := func(ids []string) map[string]bool {
		out := make(map[string]bool, len(ids))
		for _, id := range ids {
			out[id] = true
		}
		return out
	}
	out := map[domain.Month]*goldenMonth{}
	for rows.Next() {
		var (
			month                                         time.Time
			income, bills, subs, transfer, goals, planned string
			spent, left                                   string
			incomeIDs, billsIDs, spentIDs                 []string
		)
		require.NoError(t, rows.Scan(&month, &income, &bills, &subs, &transfer,
			&goals, &planned, &spent, &left, &incomeIDs, &billsIDs, &spentIDs))
		one := &goldenMonth{
			figures: map[domain.BucketKey]string{
				domain.BucketIncome:       sum(t, income),
				domain.BucketBills:        sum(t, bills, subs, transfer),
				domain.BucketGoals:        sum(t, goals),
				domain.BucketPlannedSpend: sum(t, planned),
				domain.BucketOtherSpend:   sum(t, spent),
			},
			txnIDs: map[domain.BucketKey]map[string]bool{
				domain.BucketIncome:     set(incomeIDs),
				domain.BucketBills:      set(billsIDs),
				domain.BucketOtherSpend: set(spentIDs),
			},
			leftToSpend:  sum(t, left),
			leftIsTheSum: sum(t, left) == sum(t, income, bills, subs, transfer, goals, planned, spent),
			envelopes:    map[string]goldenEnvelope{},
		}
		out[domain.MonthOf(domain.DateOf(month))] = one
	}
	require.NoError(t, rows.Err())

	envelopes, err := database.Pool().Query(t.Context(), `
		SELECT m.month, e.id::text, e.name, e.calculated_spent_amount::text,
		       (-coalesce((SELECT sum(t.amount) FROM transactions t
		                    WHERE t.id = ANY(e.txn_ids)), 0))::text
		  FROM envelopes e JOIN spending_plan_months m ON m.id = e.spending_plan_month_id
		 WHERE m.space_id = $1`, spaceID.UUID())
	require.NoError(t, err)
	defer envelopes.Close()
	for envelopes.Next() {
		var month time.Time
		var id, name, spent, listed string
		require.NoError(t, envelopes.Scan(&month, &id, &name, &spent, &listed))
		out[domain.MonthOf(domain.DateOf(month))].envelopes[id] = goldenEnvelope{
			name: name, spent: sum(t, spent), listed: sum(t, listed),
		}
	}
	require.NoError(t, envelopes.Err())
	return out
}

// reopenEveryMonth clears every imported month's closed-out flag.
//
// A closed month is frozen at what it closed with (calculations.md §5 rule 5,
// PlanMonthRow.FrozenMonth), and nearly every finished month in an export is
// closed. Read closed, such a month hands back Simplifi's own stored figures,
// and comparing those against Simplifi checks the import, not the arithmetic.
// Reopened, every month is recomputed from the imported transactions, series,
// envelopes and goals, which is the comparison this test exists to make.
func reopenEveryMonth(t *testing.T, database *store.Store, spaceID store.SpaceID) {
	t.Helper()
	_, err := database.Pool().Exec(t.Context(),
		`UPDATE spending_plan_months SET is_closed_out = false WHERE space_id = $1`, spaceID.UUID())
	require.NoError(t, err)
}

// unverifiable names the buckets this export cannot arbitrate, and why.
//
// Being explicit is the point: a test that skipped any bucket whose name it
// did not recognise could quietly check three buckets of six and report the
// other three as agreeing when nothing had compared them. A bucket goes here
// only when Simplifi genuinely stores no figure for it, and the reason is
// printed on every run.
var unverifiable = map[domain.BucketKey]string{
	domain.BucketRollover: "freeToSpendStore.calculatedRolloverAmount is absent on every month; " +
		"Simplifi carries no rollover figure to compare against",
}

// forecastGaps names the future months whose Income and Bills the export
// cannot arbitrate, and why, read from golden-forecast-gaps.json beside the
// export: an object of "YYYY-MM" to the reason. With no such file every month
// is checked.
//
// Comparing a month that has not begun compares two expansions of the same
// recurrence rules — Simplifi's, materialized as forecast rows, against this
// app's. Where they disagree for a reason the export itself shows (Simplifi
// stopping a rule's forecast early, or its last forecast row falling inside
// the final month), the month is named with that evidence rather than the
// window being quietly shortened. A gap that is a defect here is written as
// one: a gap named in a skip is one somebody can find again. The reasons
// describe a particular export's bills, so they stay alongside that export.
func forecastGaps(t *testing.T) map[string]string {
	t.Helper()
	gaps := map[string]string{}
	path := filepath.Join(filepath.Dir(goldenExportPath()), "golden-forecast-gaps.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return gaps
	}
	require.NoError(t, err, "reading %s", path)
	require.NoError(t, json.Unmarshal(raw, &gaps), "%s is an object of month to reason", path)
	return gaps
}

// overspendPlannedMonths are the months whose stored planned-spending figure
// differs from the plain sum of targets because an envelope spent past what it
// had.
//
// One formula reproduces Simplifi's stored figure on every month of the
// export: per envelope, the larger of its target and its spend net of any
// positive rollover, plus any negative rollover —
//
//	Σ max(target, spent − max(rollover, 0)) + min(rollover, 0)
//
// so an envelope that overran what it had reserves what it actually cost.
// Agentifi reserves the targets (calculations.md §13, "planned_spend reserves
// targets"); the two agree on every month where no envelope overran, and those
// months are compared. A month qualifies here only on the evidence: its stored
// figure is Simplifi's formula over the imported envelopes and is not the sum of
// targets. A month that is neither is compared, and fails.
func overspendPlannedMonths(t *testing.T, database *store.Store, spaceID store.SpaceID) map[domain.Month]bool {
	t.Helper()
	rows, err := database.Pool().Query(t.Context(), `
		SELECT m.month,
		       m.calculated_planned_spending_amount::text,
		       coalesce(sum(coalesce(e.overwritten_target_amount, e.target_amount)), 0)::text,
		       coalesce(sum(greatest(coalesce(e.overwritten_target_amount, e.target_amount),
		                             abs(e.calculated_spent_amount) - greatest(e.rollover_amount, 0))
		                    + least(e.rollover_amount, 0)), 0)::text
		  FROM spending_plan_months m
		  LEFT JOIN envelopes e ON e.spending_plan_month_id = m.id
		 WHERE m.space_id = $1
		 GROUP BY m.month, m.calculated_planned_spending_amount`, spaceID.UUID())
	require.NoError(t, err)
	defer rows.Close()

	out := map[domain.Month]bool{}
	for rows.Next() {
		var month time.Time
		var stored, targets, overspent string
		require.NoError(t, rows.Scan(&month, &stored, &targets, &overspent))
		figure := sum(t, stored)
		if figure != "-"+sum(t, targets) && figure == "-"+sum(t, overspent) {
			out[domain.MonthOf(domain.DateOf(month))] = true
		}
	}
	require.NoError(t, rows.Err())
	return out
}

// goldenRow is what a mismatch report needs to know about one transaction, and
// the two facts that settle a mismatch Simplifi's own data explains.
type goldenRow struct {
	text string
	// billWithoutSeries is a row flagged as a bill or subscription that no
	// series claims. An export can hold one, listed under Bills; this app
	// fills Bills from series alone (calculations.md §5), so it counts the row
	// as Other Spend. One row cannot say whether Simplifi files by the flag or
	// the row lost its series after the month closed.
	billWithoutSeries bool
	// notSpentFrom is a row on an account money is not spent from
	// (AccountKind.SpendsFrom). Simplifi leaves these out of its plan too, so
	// one it listed is Simplifi's anomaly, not a rule missing here.
	notSpentFrom bool
	// zero is a row that cannot move a figure, which Simplifi lists and this
	// app does not.
	zero bool
}

func describeRow(t *testing.T, database *store.Store, id string) goldenRow {
	t.Helper()
	var (
		on, effective                                time.Time
		amount, category, kind, accountKind          string
		excludedFromPlan, accountExcluded, hasSeries bool
		isBill, isSubscription, isTransfer, isPaired bool
	)
	err := database.Pool().QueryRow(t.Context(), `
		SELECT t.date, coalesce(t.effective_date, t.date), t.amount::text,
		       coalesce(c.name, ''), coalesce(c.kind, ''), a.kind,
		       t.excluded_from_spending_plan OR coalesce(c.excluded_from_spending_plan, false),
		       a.excluded_from_spending_plan, t.series_id IS NOT NULL,
		       t.is_bill, t.is_subscription, coalesce(c.kind, '') = 'transfer',
		       t.transfer_pair_id IS NOT NULL
		  FROM transactions t
		  JOIN accounts a ON a.id = t.account_id
		  LEFT JOIN categories c ON c.id = t.category_id
		 WHERE t.id = $1`, id).Scan(&on, &effective, &amount, &category, &kind, &accountKind,
		&excludedFromPlan, &accountExcluded, &hasSeries, &isBill, &isSubscription, &isTransfer, &isPaired)
	if err != nil {
		return goldenRow{text: fmt.Sprintf("%s (not imported: %v)", id, err)}
	}
	return goldenRow{
		text: fmt.Sprintf("%s dated %s (effective %s) %s, category %q (%s), account %s, "+
			"excluded from the plan: row or category %t, account %t; series %t, bill %t, "+
			"subscription %t, transfer %t, paired %t",
			id, on.Format(time.DateOnly), effective.Format(time.DateOnly), amount, category,
			kind, accountKind, excludedFromPlan, accountExcluded, hasSeries, isBill,
			isSubscription, isTransfer, isPaired),
		billWithoutSeries: (isBill || isSubscription) && !hasSeries,
		notSpentFrom:      !domain.AccountKind(accountKind).SpendsFrom(),
		zero:              domain.MustFromString(amount).IsZero(),
	}
}

// explainBucket compares the rows behind one bucket on each side. It returns
// what differs, and whether every row that differs is one Simplifi's own data
// accounts for (goldenRow).
func explainBucket(t *testing.T, database *store.Store, want map[string]bool, got []any) ([]string, bool) {
	t.Helper()
	ours := map[string]bool{}
	for _, id := range got {
		ours[fmt.Sprint(id)] = true
	}
	var lines []string
	explained := true
	note := func(side, id string) {
		row := describeRow(t, database, id)
		if row.zero {
			return
		}
		lines = append(lines, side+": "+row.text)
		if !row.billWithoutSeries && !row.notSpentFrom {
			explained = false
		}
	}
	for id := range want {
		if !ours[id] {
			note("only Simplifi lists", id)
		}
	}
	for id := range ours {
		if !want[id] {
			note("only this app lists", id)
		}
	}
	sort.Strings(lines)
	return lines, explained && len(lines) > 0
}

// sum adds Simplifi's stored figures the way Agentifi's bucket does.
func sum(t *testing.T, amounts ...string) string {
	t.Helper()
	total := domain.Zero
	for _, raw := range amounts {
		amount, err := domain.FromString(raw)
		require.NoErrorf(t, err, "stored amount %q", raw)
		total = total.Add(amount)
	}
	return total.String()
}

func TestGoldenSpendingPlanMatchesSimplifi(t *testing.T) {
	// db(t) before goldenExport(t): with an export present but no database
	// every helper below would nil-deref, and a panic is not a skip.
	database := db(t)
	raw := goldenExport(t)
	spaceID, captured := importGolden(t, database, raw)

	stored := storedMonths(t, database, spaceID)
	require.NotEmpty(t, stored, "the export produced no spending plan months")
	for _, key := range domain.BucketOrder {
		if unverifiable[key] != "" {
			t.Logf("%s is not checked: %s", key, unverifiable[key])
			continue
		}
		for month, one := range stored {
			require.Containsf(t, one.figures, key,
				"%s: no stored figure reaches the %s bucket, so nothing checks it", month, key)
		}
	}

	months := make([]domain.Month, 0, len(stored))
	for month := range stored {
		months = append(months, month)
	}
	sort.Slice(months, func(i, j int) bool { return months[i].Before(months[j]) })
	t.Logf("months to check: %d", len(months))

	gaps := forecastGaps(t)
	for _, month := range months {
		if why := gaps[month.String()]; why != "" {
			t.Logf("%s income and bills are not checked: %s", month, why)
		}
	}

	overspendPlanned := overspendPlannedMonths(t, database, spaceID)
	for _, month := range months {
		if overspendPlanned[month] {
			t.Logf("%s planned_spend is not checked: Simplifi's stored figure reserves "+
				"an overspent envelope's spend, and this app reserves targets", month)
		}
	}

	reopenEveryMonth(t, database, spaceID)

	owner := goldenOwner(t, database, spaceID)
	env := NewEnv(testConfig(), database)
	client := (&client{t: t, env: env, handler: RouterFor(env)}).as(owner).inSpace(spaceID)

	var compared, mismatches, named int
	var envelopesCompared, envelopesStale, totalsCompared, totalsOwing int
	for _, month := range months {
		want := stored[month]
		ended := month.LastDay().Before(captured)
		body := client.get(fmt.Sprintf("/spending-plan/%s", month)).
			requireStatus(200).json()

		recomputed := map[domain.BucketKey]string{}
		contributing := map[domain.BucketKey][]any{}
		owing := false
		for _, raw := range body["buckets"].([]any) {
			bucket := raw.(map[string]any)
			key := domain.BucketKey(fmt.Sprint(bucket["key"]))
			// Simplifi's stored bucket amount equals the sum of the
			// transaction ids it lists, on every month of the export and every
			// bucket. For a month that had ended when the export was captured
			// every one of those ids posted, so Income and Bills compare on the
			// posted figure. Agentifi's calculated figure additionally carries
			// the month's still-expected occurrences (calculations.md §5 rule
			// 1), which is a deliberate difference and the subject of its own
			// unit tests, not of this one. Every other bucket compares as
			// calculated.
			//
			// Any later month compares Income and Bills as calculated, and it
			// is the same rule rather than a relaxation of it. From the month
			// the capture fell in onward, the ids Simplifi lists include its
			// own forecast rows, the occurrences it materializes as far as a
			// year ahead: in part for the month in progress, wholly for every
			// month after it. Its stored figure is then a projection. Agentifi
			// keeps a forecast out of every posted figure — it is an estimate,
			// not money that changed hands — and holds it in the calculated
			// one, so the projection is what compares against a projection:
			// this app's expansion of the recurrence rules against Simplifi's
			// own, with the months that disagree named by forecastGaps.
			//
			// The boundary is the capture day, not the day the test runs. A
			// month that began after the capture holds nothing but forecasts
			// in the export however long ago it began, and its posted figure
			// here is zero.
			field := "calculated_amount"
			if (key == domain.BucketIncome || key == domain.BucketBills) && ended {
				field = "posted_amount"
			}
			recomputed[key] = fmt.Sprint(bucket[field])
			if bucket["calculated_amount"] != bucket["posted_amount"] {
				owing = true
			}
			contributing[key], _ = bucket["contributing_txn_ids"].([]any)
		}

		everyBucketAgrees := true
		for _, key := range domain.BucketOrder {
			if unverifiable[key] != "" {
				continue
			}
			if key == domain.BucketPlannedSpend && overspendPlanned[month] {
				everyBucketAgrees = false
				continue
			}
			if (key == domain.BucketIncome || key == domain.BucketBills) &&
				gaps[month.String()] != "" {
				everyBucketAgrees = false
				continue
			}
			compared++
			got, present := recomputed[key]
			// A bucket the response does not carry is a finding, not a
			// reason to check one bucket fewer. Skipping it quietly is how a
			// test like this ends up proving nothing.
			if !present {
				t.Errorf("%s %s: the response carries no such bucket", month, key)
				mismatches++
				everyBucketAgrees = false
				continue
			}
			if got == want.figures[key] {
				continue
			}
			everyBucketAgrees = false
			// A finished month lists only posted rows on both sides, so the
			// rows that differ are the mismatch, named.
			var rows []string
			explained := false
			if ids, listed := want.txnIDs[key]; listed && ended {
				rows, explained = explainBucket(t, database, ids, contributing[key])
			}
			if explained {
				named++
				t.Logf("%s %s is not checked: Simplifi says %s, this app computes %s, and "+
					"every row that differs is one the export accounts for:\n\t%s",
					month, key, want.figures[key], got, strings.Join(rows, "\n\t"))
				continue
			}
			mismatches++
			// Every mismatch is a finding, so each is named rather than
			// the run stopping at the first.
			t.Errorf("%s %s: Simplifi says %s, this app computes %s\n\t%s",
				month, key, want.figures[key], got, strings.Join(rows, "\n\t"))
		}

		// Envelope by envelope. Simplifi's figure is a magnitude after the
		// import, as this app's is (domain.EnvelopeSpent).
		ours := map[string]string{}
		for _, raw := range body["envelopes"].([]any) {
			envelope := raw.(map[string]any)
			ours[fmt.Sprint(envelope["id"])] = fmt.Sprint(envelope["spent"])
		}
		for id, envelope := range want.envelopes {
			if envelope.spent != envelope.listed {
				envelopesStale++
				continue
			}
			envelopesCompared++
			got, present := ours[id]
			if !present {
				mismatches++
				t.Errorf("%s envelope %q: the response does not carry it", month, envelope.name)
				continue
			}
			if got != envelope.spent {
				mismatches++
				t.Errorf("%s envelope %q: Simplifi says it spent %s, this app computes %s",
					month, envelope.name, envelope.spent, got)
			}
		}

		// The month's total. Simplifi stores no rollover, so its left-to-spend
		// is this app's month_result: the five buckets without the rollover
		// (calculations.md §5). It is compared on the months where every
		// bucket was compared, agreed, and adds up to Simplifi's own total; a
		// forecast month's total is not the sum of its stored figures.
		//
		// month_result adds the calculated figures, which keep an occurrence
		// nobody paid as still owed after its month ends (§5 rule 1). Simplifi
		// stores no forecast row for a slot in the past, so its finished month
		// drops one. Such a month is counted, not compared: the buckets above
		// compared it on the posted figure already.
		if everyBucketAgrees && want.leftIsTheSum && owing && ended {
			totalsOwing++
		}
		if everyBucketAgrees && want.leftIsTheSum && !(owing && ended) {
			totalsCompared++
			if got := fmt.Sprint(body["month_result"]); got != want.leftToSpend {
				mismatches++
				t.Errorf("%s month_result: Simplifi's left to spend is %s, this app computes %s",
					month, want.leftToSpend, got)
			}
		}
	}
	t.Logf("%d months: %d bucket figures compared and %d named; %d envelope figures compared "+
		"and %d not, whose stored figure disagrees with the rows Simplifi lists behind it; "+
		"%d month totals compared and %d finished months not, which still owe an occurrence; "+
		"%d mismatches",
		len(months), compared, named, envelopesCompared, envelopesStale, totalsCompared,
		totalsOwing, mismatches)
}

// goldenOwner is whoever the import made the owner of the space.
func goldenOwner(t *testing.T, database *store.Store, spaceID store.SpaceID) store.User {
	t.Helper()
	memberships, err := database.ListMemberships(context.Background(), spaceID)
	require.NoError(t, err)
	require.NotEmpty(t, memberships, "the imported space has no members")
	user, err := database.GetUser(context.Background(), memberships[0].UserID)
	require.NoError(t, err)
	return user
}

// --- Net worth ----------------------------------------------------------------

// exportBalance is Simplifi's own newest balance per account, read straight
// out of the export rather than out of anything this app mapped.
//
// Amounts arrive as JSON numbers and are carried as text the whole way: a
// balance that went through a float64 would be a balance nobody could reconcile.
type exportBalance struct {
	amount string
	on     string
	// current is Simplifi's CURRENT balance, which supersedes ONLINE on the
	// same day: it is the figure including anything the bank has not settled.
	current bool
}

// exportNetWorth sums the export's balances the way the product does.
//
// Two accounts can hold the same money. A GOAL account is a reservation inside
// a funding account — Simplifi shows it beside the others, but the balance is
// already inside the account funding it, carried there as goalBalance. Summing
// both counts that money twice, which is the whole of the difference between
// the naive total and the one the app reports.
//
// byName is each counted account's own newest balance, keyed by its name.
func exportNetWorth(t *testing.T, raw []byte) (assets, debt, net domain.Money, asOf string, byName map[string]string) {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var doc struct {
		Datasets map[string]map[string]struct {
			Data struct {
				ResourcesByID map[string]json.RawMessage `json:"resourcesById"`
			} `json:"data"`
		} `json:"datasets"`
	}
	require.NoError(t, decoder.Decode(&doc), "reading the export")
	require.Len(t, doc.Datasets, 1, "the export carries more than one dataset")

	var stores map[string]struct {
		Data struct {
			ResourcesByID map[string]json.RawMessage `json:"resourcesById"`
		} `json:"data"`
	}
	for _, one := range doc.Datasets {
		stores = one
	}

	// Simplifi writes undefined as a "#UNDEFINED_<hash>" string, so a flag is
	// only true when it decodes as a real boolean.
	isTrue := func(v any) bool { flag, ok := v.(bool); return ok && flag }

	type account struct {
		name    string
		kind    string
		skipped bool
	}
	accounts := map[string]account{}
	for _, blob := range stores["accountsStore"].Data.ResourcesByID {
		var row map[string]any
		if err := json.Unmarshal(blob, &row); err != nil {
			continue // a marker key, not a record
		}
		id, ok := row["id"].(string)
		if !ok {
			continue
		}
		kind, _ := row["type"].(string)
		name, _ := row["name"].(string)
		accounts[id] = account{
			name:    name,
			kind:    kind,
			skipped: isTrue(row["isDeleted"]) || isTrue(row["isClosed"]) || kind == "GOAL",
		}
	}

	newest := map[string]exportBalance{}
	for _, blob := range stores["accountsBalancesStore"].Data.ResourcesByID {
		var row struct {
			AccountID   string      `json:"accountId"`
			Amount      json.Number `json:"balanceAmount"`
			On          string      `json:"balanceOn"`
			BalanceType string      `json:"balanceType"`
		}
		if err := json.Unmarshal(blob, &row); err != nil || row.On == "" {
			continue
		}
		one := exportBalance{amount: row.Amount.String(), on: row.On, current: row.BalanceType == "CURRENT"}
		have, seen := newest[row.AccountID]
		if !seen || one.on > have.on || (one.on == have.on && one.current && !have.current) {
			newest[row.AccountID] = one
		}
	}

	counted := 0
	byName = map[string]string{}
	for id, balance := range newest {
		one, known := accounts[id]
		if !known || one.skipped {
			continue
		}
		amount := domain.MustFromString(balance.amount)
		counted++
		_, twice := byName[one.name]
		require.Falsef(t, twice, "two counted accounts are named %q, so a balance cannot be "+
			"told apart by name", one.name)
		byName[one.name] = amount.Round().String()
		if balance.on > asOf {
			asOf = balance.on
		}
		net = net.Add(amount)
		if amount.IsNegative() {
			debt = debt.Add(amount.Neg())
		} else {
			assets = assets.Add(amount)
		}
	}
	require.NotZero(t, counted, "the export carries no account balances")
	t.Logf("export balances: %d accounts as of %s, assets %s, debt %s, net %s",
		counted, asOf, assets, debt, net)
	return assets.Round(), debt.Round(), net.Round(), asOf, byName
}

// TestGoldenNetWorthMatchesTheExportsBalances is the headline figure, checked
// against the balances Simplifi itself stored.
//
// What this proves is the aggregation rather than the per-account arithmetic:
// the import takes each account's balance from the same store this reads, so
// the two agree per account by construction. What can still go wrong — and is
// exactly what this catches — is everything on top of that. A debt summed with
// the wrong sign, an account included that should not be, and above all a GOAL
// account counted beside the account already holding its money, which would
// double-count the whole goal reserve in the net worth.
func TestGoldenNetWorthMatchesTheExportsBalances(t *testing.T) {
	database := db(t)
	raw := goldenExport(t)
	spaceID, _ := importGolden(t, database, raw)

	assets, debt, net, asOf, byName := exportNetWorth(t, raw)

	owner := goldenOwner(t, database, spaceID)
	env := NewEnv(testConfig(), database)
	client := (&client{t: t, env: env, handler: RouterFor(env)}).as(owner).inSpace(spaceID)

	// The window ends on the newest balance the export carries, taken from the
	// export rather than written down here: the ledger holds future-dated rows,
	// and a later `to` would walk them forward past every anchor. Reading the
	// date from the file is also what lets this survive the next extraction.
	body := client.get("/net-worth?from=2026-01-01&to=" + asOf).
		requireStatus(http.StatusOK).json()
	end := body["end"].(map[string]any)

	require.Equal(t, net.String(), end["net"], "net worth")
	require.Equal(t, assets.String(), end["assets"], "assets")
	require.Equal(t, debt.String(), end["debt"], "debt")

	// Account by account, both ways: every balance the export counts is on
	// the page at Simplifi's figure, and the page shows no account the
	// export leaves out unless that account is at zero.
	shown := map[string]string{}
	for _, raw := range body["groups"].([]any) {
		for _, row := range raw.(map[string]any)["accounts"].([]any) {
			account := row.(map[string]any)
			name := fmt.Sprint(account["name"])
			_, twice := shown[name]
			require.Falsef(t, twice, "the page shows two accounts named %q", name)
			shown[name] = fmt.Sprint(account["end"])
		}
	}
	for name, want := range byName {
		got, present := shown[name]
		if !present {
			t.Errorf("account %q: the export counts a balance of %s and the page does not show it", name, want)
			continue
		}
		if got != want {
			t.Errorf("account %q: Simplifi's newest balance is %s, the page ends at %s", name, want, got)
		}
	}
	for name, got := range shown {
		if _, counted := byName[name]; !counted && got != domain.Zero.String() {
			t.Errorf("account %q: the page ends at %s, and the export counts no balance for it", name, got)
		}
	}
	t.Logf("%d account balances compared", len(byName))
}
