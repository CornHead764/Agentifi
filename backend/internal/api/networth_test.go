package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Net worth, end to end. calculations.md §4.
//
// The seeded ledger's figures are chosen so the window arithmetic is checkable
// by hand. Over August the checking account opens at 400.00 — July's 100.00 is
// behind the window — and closes at 125.00, while the card walks backwards
// from the provider's −300.00 to −425.00 at the window's start.

func netWorth(l *ledger, query string) map[string]any {
	l.t.Helper()
	path := "/net-worth"
	if query != "" {
		path += "?" + query
	}
	return l.alex.get(path).requireStatus(http.StatusOK).json()
}

func groupOf(t *testing.T, body map[string]any, kind string) map[string]any {
	t.Helper()
	for _, raw := range body["groups"].([]any) {
		group := raw.(map[string]any)
		if group["kind"] == kind {
			return group
		}
	}
	t.Fatalf("no net worth group for %s", kind)
	return nil
}

func accountRow(t *testing.T, group map[string]any, name string) map[string]any {
	t.Helper()
	for _, raw := range group["accounts"].([]any) {
		row := raw.(map[string]any)
		if row["name"] == name {
			return row
		}
	}
	t.Fatalf("no account row named %s", name)
	return nil
}

// --- Windowed group rows -----------------------------------------------------

func TestGroupRowsShowTheChangeOverTheSelectedWindow(t *testing.T) {
	// §4: the accounts panel reports the change over the *selected window*,
	// not month over month, with the percentage relative to the window start.
	l := buildLedger(t)
	body := netWorth(l, "from=2026-08-01&to=2026-08-31")

	cash := groupOf(t, body, string(domain.KindCash))
	require.Equal(t, "banking", cash["class"])
	require.Equal(t, "asset", cash["side"])
	require.Equal(t, "400.00", cash["start"])
	require.Equal(t, "125.00", cash["end"])
	require.Equal(t, "-275.00", cash["change"])
	require.Equal(t, "-68.75", cash["change_pct"])

	// The card walks backwards from the provider's figure: −300.00 today, so
	// −425.00 before August's payment and charge.
	card := groupOf(t, body, string(domain.KindCreditCard))
	require.Equal(t, "debt", card["side"])
	require.Equal(t, "-425.00", card["start"])
	require.Equal(t, "-300.00", card["end"])
	require.Equal(t, "125.00", card["change"])
	require.Equal(t, "29.41", card["change_pct"])

	// A group's own figures come from the same two snapshots its rows do.
	checking := accountRow(t, cash, "Everyday Checking")
	require.Equal(t, cash["start"], checking["start"])
	require.Equal(t, cash["end"], checking["end"])
	require.Equal(t, cash["change_pct"], checking["change_pct"])
}

func TestAWiderWindowMovesTheGroupFiguresBecauseTheyAreNotMonthOverMonth(t *testing.T) {
	// The same rows read over two windows must give two answers. A panel that
	// answered month over month would report the same change for both.
	l := buildLedger(t)
	august := groupOf(t, netWorth(l, "from=2026-08-01&to=2026-08-31"), string(domain.KindCash))
	summer := groupOf(t, netWorth(l, "from=2026-07-01&to=2026-08-31"), string(domain.KindCash))

	require.Equal(t, "500.00", summer["start"])
	require.Equal(t, "125.00", summer["end"])
	require.Equal(t, "-375.00", summer["change"])
	require.Equal(t, "-75", summer["change_pct"])
	require.NotEqual(t, august["change"], summer["change"])
}

func TestAPercentageAgainstAZeroWindowStartIsNull(t *testing.T) {
	// §4: undefined, rendered as an em dash. Neither zero nor infinity.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	account := &store.Account{
		Name: "Vintage Guitar", Kind: domain.KindAsset, Type: "other_asset",
		Currency: "USD", IncludeInNetWorth: true,
		OpeningBalance: domain.Zero, OpeningBalanceOn: domain.NewDate(2026, time.January, 1),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, account))
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, &store.Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, time.August, 5),
		Amount: domain.MustFromString("1200.00"), Currency: "USD",
		StatementName: "APPRAISAL", Payee: "Appraisal", Source: domain.SourceManual,
	}))

	body := netWorth(l, "from=2026-08-01&to=2026-08-31")
	group := groupOf(t, body, string(domain.KindAsset))
	require.Equal(t, "0.00", group["start"])
	require.Equal(t, "1200.00", group["end"])
	require.Equal(t, "1200.00", group["change"])
	require.Contains(t, group, "change_pct")
	require.Nil(t, group["change_pct"], "a change from nothing came back as a number")
	require.Nil(t, accountRow(t, group, "Vintage Guitar")["change_pct"])
}

// --- The holdings trap -------------------------------------------------------

func TestNetWorthCountsTheBrokerageBalanceAndNeverTheHoldingsOnTop(t *testing.T) {
	// §4: a position inside a connected brokerage is already inside that
	// account's balance. Net worth sums balances, full stop — adding the
	// 2,000.00 of positions would count them twice.
	l := buildPortfolioLedger(t)
	body := netWorth(l, "from=2026-08-01&to=2026-08-31")

	end := body["end"].(map[string]any)
	require.Equal(t, "14125.00", end["assets"])
	require.NotEqual(t, "16125.00", end["assets"], "the holdings were added on top of their account")
	require.Equal(t, "300.00", end["debt"])
	require.Equal(t, "13825.00", end["net"])

	investments := groupOf(t, body, string(domain.KindInvestment))
	require.Equal(t, "investments", investments["class"])
	require.Equal(t, "14000.00", investments["end"])

	// The portfolio page must reach the same figure from the other side, and
	// that is the whole point of asserting it here. Adding back only the
	// investment accounts holding *no* positions would read 6,000.00 against
	// net worth's 14,000.00, leaving the brokerage's 8,000.00 of uncommitted
	// cash in no figure on that page at all. Two screens disagreeing by
	// 8,000.00 about the same accounts is the failure this line exists to
	// catch.
	totals := holdingsPage(l, "")["totals"].(map[string]any)
	require.Equal(t, investments["end"], totals["total_value"],
		"the portfolio page and net worth disagree about the same accounts")
}

func TestTheHeadlineIsTheGroupsAddedUp(t *testing.T) {
	l := buildPortfolioLedger(t)
	body := netWorth(l, "from=2026-08-01&to=2026-08-31")

	sum := domain.Zero
	accounts := 0
	for _, raw := range body["groups"].([]any) {
		group := raw.(map[string]any)
		rows := domain.Zero
		for _, member := range group["accounts"].([]any) {
			rows = rows.Add(domain.MustFromString(member.(map[string]any)["end"].(string)))
		}
		require.Equal(t, group["end"], rows.String(),
			"the %v subtotal disagrees with its own rows", group["kind"])
		sum = sum.Add(rows)
		accounts += int(group["account_count"].(float64))
	}
	require.Equal(t, body["end"].(map[string]any)["net"], sum.String())
	require.Equal(t, float64(accounts), body["included_accounts"])
}

// --- Inclusion ---------------------------------------------------------------

func TestAnExcludedAccountIsCountedInTheChipButNotInTheTotal(t *testing.T) {
	// include_in_net_worth is its own flag: an account can be excluded from
	// net worth without being deleted or hidden anywhere else.
	l := buildLedger(t)
	before := netWorth(l, "from=2026-08-01&to=2026-08-31")
	require.Equal(t, float64(2), before["included_accounts"])
	require.Equal(t, float64(2), before["total_accounts"])

	l.alex.patch("/accounts/"+l.str("card"),
		map[string]any{"include_in_net_worth": false}).requireStatus(http.StatusOK)

	after := netWorth(l, "from=2026-08-01&to=2026-08-31")
	require.Equal(t, float64(1), after["included_accounts"])
	require.Equal(t, float64(2), after["total_accounts"])
	require.Equal(t, "0.00", after["end"].(map[string]any)["debt"])
	require.Equal(t, []any{}, groupKinds(after))
}

// The register's header counts the accounts somebody has, and this chip has
// to count the same number. A chip that counted one more — a closed account,
// still in the arithmetic — would have two screens reporting the same
// household two ways.
func TestAClosedAccountIsInNeitherCount(t *testing.T) {
	l := buildLedger(t)
	require.Equal(t, float64(2), netWorth(l, "from=2026-08-01&to=2026-08-31")["total_accounts"])

	l.alex.patch("/accounts/"+l.str("card"), map[string]any{"is_closed": true}).
		requireStatus(http.StatusOK)

	after := netWorth(l, "from=2026-08-01&to=2026-08-31")
	require.Equal(t, float64(1), after["included_accounts"])
	require.Equal(t, float64(1), after["total_accounts"])
}

func groupKinds(body map[string]any) []any {
	out := []any{}
	for _, raw := range body["groups"].([]any) {
		if raw.(map[string]any)["kind"] == string(domain.KindCreditCard) {
			out = append(out, raw)
		}
	}
	return out
}

// --- The chart ---------------------------------------------------------------

func TestTheChartAndTheHeadlineAgreeAtBothEdges(t *testing.T) {
	l := buildLedger(t)
	body := netWorth(l, "from=2026-08-01&to=2026-08-31")

	require.Equal(t, "day", body["granularity"])
	points := body["points"].([]any)
	require.Len(t, points, 31)

	first := points[0].(map[string]any)
	last := points[len(points)-1].(map[string]any)
	require.Equal(t, "2026-08-01", first["on"])
	require.Equal(t, "2026-08-31", last["on"])
	require.Equal(t, body["start"].(map[string]any)["net"], first["net"])
	require.Equal(t, body["end"].(map[string]any)["net"], last["net"])

	require.Equal(t, "-25.00", body["start"].(map[string]any)["net"])
	require.Equal(t, "-150.00", body["change"])
	require.Equal(t, "-600", body["change_pct"])
	require.Equal(t, "2.4", body["debt_to_asset"])
}

// Equity is the chart's fourth line: every asset less the loans secured on
// it. It has to disagree with both Assets (which counts the house at full
// value) and Net (which nets the mortgage against the whole household) or the
// view is redundant with one already on the chart.
func TestTheChartCarriesEquityAlongsideAssetsAndDebt(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	house := &store.Account{
		Name: "Maple Street", Kind: domain.KindAsset, Type: "real_estate",
		Currency: "USD", IncludeInNetWorth: true,
		OpeningBalance:   domain.MustFromString("420000.00"),
		OpeningBalanceOn: domain.NewDate(2026, time.January, 1),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, house))
	mortgage := &store.Account{
		Name: "Maple Street Mortgage", Kind: domain.KindLoan, Type: "mortgage",
		Currency: "USD", IncludeInNetWorth: true,
		OpeningBalance:   domain.MustFromString("-284000.00"),
		OpeningBalanceOn: domain.NewDate(2026, time.January, 1),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, mortgage))
	l.alex.patch("/accounts/"+mortgage.ID.String(), map[string]any{
		"secured_by_account_id": house.ID.String(),
	}).requireStatus(http.StatusOK)

	body := netWorth(l, "from=2026-08-01&to=2026-08-31")
	end := body["end"].(map[string]any)
	require.Equal(t, "136000.00", end["equity"])
	require.NotEqual(t, end["assets"], end["equity"],
		"the mortgage's asset counted in full for Assets but not for Equity")
	require.NotEqual(t, end["net"], end["equity"],
		"Net nets the mortgage against the whole household, not just the house it secures")

	points := body["points"].([]any)
	last := points[len(points)-1].(map[string]any)
	require.Equal(t, end["equity"], last["equity"])
	require.Equal(t, end["equity"], body["start"].(map[string]any)["equity"],
		"the mortgage and the house were both open at the window's start too")
}

func TestALongWindowCoarsensTheSamplingRatherThanTruncatingIt(t *testing.T) {
	// A chart missing its first four years would look like a household that
	// appeared from nowhere.
	l := buildLedger(t)
	body := netWorth(l, "from=2021-01-01&to=2026-08-31&granularity=day")

	require.NotEqual(t, "day", body["granularity"])
	points := body["points"].([]any)
	require.LessOrEqual(t, len(points), maxNetWorthPoints)
	require.Equal(t, "2021-01-01", points[0].(map[string]any)["on"])
	require.Equal(t, "2026-08-31", points[len(points)-1].(map[string]any)["on"])
}

func TestAnInvertedNetWorthWindowIsRefused(t *testing.T) {
	l := buildLedger(t)
	l.alex.get("/net-worth?from=2026-08-31&to=2026-08-01").
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.get("/net-worth?granularity=fortnightly").
		requireStatus(http.StatusUnprocessableEntity)
}

// --- The wire and tenancy ----------------------------------------------------

func TestEveryNetWorthFigureCrossesTheWireAsAString(t *testing.T) {
	l := buildLedger(t)
	body := netWorth(l, "from=2026-08-01&to=2026-08-31")
	for _, key := range []string{"assets", "debt", "net"} {
		require.IsType(t, "", body["end"].(map[string]any)[key])
	}
	require.IsType(t, "", body["change"])
	require.IsType(t, "", groupOf(t, body, string(domain.KindCash))["start"])
}

func TestNetWorthNeverReachesAnotherSpacesAccounts(t *testing.T) {
	l := buildLedger(t)
	body := netWorth(l, "")
	for _, raw := range body["groups"].([]any) {
		for _, member := range raw.(map[string]any)["accounts"].([]any) {
			require.NotEqual(t, "Their Checking", member.(map[string]any)["name"])
			require.NotEqual(t, l.str("stranger_account"), member.(map[string]any)["account_id"])
		}
	}
	require.Equal(t, float64(2), body["total_accounts"])
}

func TestAViewerReadsNetWorth(t *testing.T) {
	l := buildLedger(t)
	l.as("vera").get("/net-worth?from=2026-08-01&to=2026-08-31").requireStatus(http.StatusOK)
}

// The stacked by-type views draw from this, and a chart whose slices do not add
// up to the headline beside them is worse than no chart. Debt kinds are
// reported positive here, matching the point's own Debt field.
func TestEachPointsKindsAddUpToThatPointsHeadline(t *testing.T) {
	l := buildPortfolioLedger(t)
	body := netWorth(l, "from=2026-08-01&to=2026-08-31")

	// The window's own ends are points too: a chart and the headline beside it
	// are drawn from the same field, and a null on either end is a gap.
	points := append(body["points"].([]any), body["start"], body["end"])
	for _, raw := range points {
		point := raw.(map[string]any)
		byKind, ok := point["by_kind"].([]any)
		require.True(t, ok, "point %v carries no by_kind", point["on"])

		assets, debt := domain.Zero, domain.Zero
		for _, entry := range byKind {
			row := entry.(map[string]any)
			value := domain.MustFromString(row["amount"].(string))
			if domain.AccountKind(row["kind"].(string)).IsDebt() {
				debt = debt.Add(value)
			} else {
				assets = assets.Add(value)
			}
		}
		require.Equal(t, point["assets"], assets.String(), "assets on %v", point["on"])
		require.Equal(t, point["debt"], debt.String(), "debt on %v", point["on"])
	}
}

// One line per kind, so a series cannot lose a day in the middle of the window.
func TestEveryPointCarriesTheSameKindsInTheSameOrder(t *testing.T) {
	l := buildPortfolioLedger(t)
	body := netWorth(l, "from=2026-08-01&to=2026-08-31")

	var first []any
	for _, raw := range body["points"].([]any) {
		kinds := []any{}
		for _, entry := range raw.(map[string]any)["by_kind"].([]any) {
			kinds = append(kinds, entry.(map[string]any)["kind"])
		}
		if first == nil {
			first = kinds
			require.NotEmpty(t, first)
			continue
		}
		require.Equal(t, first, kinds)
	}
}

// One chart, one definition of a balance.
//
// The snapshots are materialized in one date mode; a fallback that walked
// the ledger in whatever mode the request asked for would let a window asked
// for by effective date take posted history for the accounts that had a
// snapshot and effective arithmetic for the rest — two definitions in one
// line, with nothing on screen to say which account was which.
//
// buildLedger carries the row that tells them apart: a card charge posted in
// August whose statement puts its effective date in September. In August the
// posted reading owes it and the effective reading does not, so the two
// answers must differ for any account holding a snapshot too — handing the
// snapshot back whatever was asked for would make them identical instead.
func TestTheChartDoesNotMixSnapshotsWithAnotherDateMode(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	_, err := db(t).RebuildBalanceHistory(t.Context(), space,
		domain.NewDate(2026, time.August, 1), domain.NewDate(2026, time.August, 31))
	require.NoError(t, err)

	window := "from=2026-08-01&to=2026-08-31"
	posted := netWorth(l, window+"&date_field=posted")
	effective := netWorth(l, window+"&date_field=effective")

	require.Equal(t, "posted", posted["window"].(map[string]any)["date_field"])
	require.Equal(t, "effective", effective["window"].(map[string]any)["date_field"])
	require.NotEqual(t,
		posted["end"].(map[string]any)["debt"],
		effective["end"].(map[string]any)["debt"],
		"the effective reading is computed, not the posted snapshot relabelled")
}

// --- A stored history read against the ledger as it stands (§12) -----------

func TestAMortgageTheImportReadAsZeroForADayIsNotPaidOff(t *testing.T) {
	// The import carried over 250,000.00 owed on September 29, 0.00 on the
	// 30th and 250,000.00 again on October 1: a dropped reading. The month
	// ends on the 30th, and the chart reads the 29th's balance there.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	loan := &store.Account{
		Name: "Mortgage 1", Kind: domain.KindLoan, Type: "mortgage", Currency: "USD",
		IncludeInNetWorth: true,
		ProviderBalance:   domain.MustFromString("-250000.00"), HasProviderBalance: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, loan))
	for day, balance := range map[string]string{
		"2025-09-29": "-250000.00", "2025-09-30": "0.00", "2025-10-01": "-250000.00",
	} {
		_, err := db(t).Pool().Exec(t.Context(), `
			INSERT INTO balance_snapshots (id, account_id, as_of, balance, space_id, is_imported)
			VALUES (gen_random_uuid(), $1, $2::date, $3::numeric, $4, true)`,
			loan.ID, day, balance, space.UUID())
		require.NoError(t, err)
	}

	september := netWorth(l, "from=2025-09-01&to=2025-09-30")
	mortgage := accountRow(t, groupOf(t, september, string(domain.KindLoan)), "Mortgage 1")
	require.Equal(t, "-250000.00", mortgage["end"])
}

func TestARowDeletedAfterTheHistoryWasWrittenLeavesTheChart(t *testing.T) {
	// A card at -300.00 by the provider: -100.00 on August 2, a duplicate
	// -50.00 on the 4th, a 20.00 refund on the 5th, written into the history
	// with the duplicate on file (the 3rd: -300 less (-50 + 20) = -270). A
	// year later the duplicate is deleted. The 3rd now reads -300 less 20 =
	// -320, with no rebuild in between.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	card := &store.Account{
		Name: "Spare Card", Kind: domain.KindCreditCard, Type: "credit_card", Currency: "USD",
		IncludeInNetWorth: true,
		ProviderBalance:   domain.MustFromString("-300.00"), HasProviderBalance: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, card))
	row := func(day int, amount string) *store.Transaction {
		txn := &store.Transaction{
			AccountID: card.ID, Date: domain.NewDate(2025, time.August, day),
			Amount: domain.MustFromString(amount), Currency: "USD",
			StatementName: "X", Payee: "X", Source: domain.SourceSync,
		}
		require.NoError(t, db(t).CreateTransaction(t.Context(), space, txn))
		return txn
	}
	row(2, "-100.00")
	duplicate := row(4, "-50.00")
	row(5, "20.00")
	_, err := db(t).RebuildBalanceHistory(t.Context(), space,
		domain.NewDate(2025, time.August, 2), domain.NewDate(2025, time.August, 6))
	require.NoError(t, err)

	window := "from=2025-08-01&to=2025-08-03"
	written := accountRow(t, groupOf(t, netWorth(l, window), string(domain.KindCreditCard)), "Spare Card")
	require.Equal(t, "-270.00", written["end"])

	require.NoError(t, db(t).DeleteTransaction(t.Context(), space, duplicate.ID))
	after := accountRow(t, groupOf(t, netWorth(l, window), string(domain.KindCreditCard)), "Spare Card")
	require.Equal(t, "-320.00", after["end"])
}
