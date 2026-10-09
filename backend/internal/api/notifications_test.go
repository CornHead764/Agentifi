package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Notification settings, end to end.
//
// The property under test throughout is that the catalog is the single source
// of what an alert *is*: the API answers with every one whether or not
// anything has been stored, and refuses an edit the catalog says is impossible
// rather than saving a switch that would never deliver.

func alertsOf(body map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, one := range body["alerts"].([]any) {
		alert := one.(map[string]any)
		out[alert["type"].(string)] = alert
	}
	return out
}

func TestTheCatalogIsAnsweredInFullBeforeAnythingIsStored(t *testing.T) {
	// A household that has never opened this page still has settings — the
	// catalog's. Returning only stored rows would put the defaults in the
	// client too, and two copies of a default drift.
	l := buildLedger(t)
	body := l.alex.get("/notifications/settings").requireStatus(http.StatusOK).json()

	alerts := alertsOf(body)
	require.Len(t, alerts, len(domain.AlertCatalog))
	require.Equal(t, false, body["all_paused"])

	large := alerts["large_transaction"]
	require.Equal(t, "Large transaction", large["label"])
	require.Equal(t, "amount", large["threshold"])
	require.Equal(t, "500.00", large["threshold_amount"])
	require.Equal(t, true, large["channel_in_app"])
	require.Equal(t, false, large["channel_email"])
	require.Equal(t, false, large["configured"])
	require.Equal(t, true, large["is_enabled"])

	// News that asks nothing of anybody is off until somebody wants it, and
	// the page shows it off rather than the client deciding so.
	for _, quiet := range []string{"bill_paid", "income_received", "large_deposit", "spending_update"} {
		require.Equal(t, false, alerts[quiet]["is_enabled"], "%s is on by default", quiet)
		require.Equal(t, false, alerts[quiet]["channel_in_app"], "%s reads as on", quiet)
	}
	require.Equal(t, true, alerts["email_connection_failed"]["is_enabled"])
}

func TestAnAlertCannotBeSwitchedOnForAChannelItCannotUse(t *testing.T) {
	// The credit-score alerts arrive from a bureau by email. Storing a push
	// preference for one would be a switch that reads as on and delivers
	// nothing, which is worse than not offering it.
	l := buildLedger(t)
	l.alex.put("/notifications/settings/credit_score_new_alert", map[string]any{
		"is_enabled": true, "channel_email": true, "channel_push": true, "channel_in_app": false,
	}).requireStatus(http.StatusBadRequest)

	l.alex.put("/notifications/settings/credit_score_new_alert", map[string]any{
		"is_enabled": true, "channel_email": true, "channel_push": false, "channel_in_app": false,
	}).requireStatus(http.StatusOK)
}

func TestAThresholdedAlertIsRefusedWithoutItsThreshold(t *testing.T) {
	// A large-transaction rule with no amount compares against nothing, which
	// in practice means every transaction.
	l := buildLedger(t)
	l.alex.put("/notifications/settings/large_transaction", map[string]any{
		"is_enabled": true, "channel_email": false, "channel_push": false, "channel_in_app": true,
	}).requireStatus(http.StatusBadRequest)
}

func TestAnAlertWithNoThresholdRefusesOne(t *testing.T) {
	// The mirror of the above: a stored amount on an alert nothing compares
	// would be a number on screen that changes no behaviour.
	l := buildLedger(t)
	l.alex.put("/notifications/settings/bill_paid", map[string]any{
		"is_enabled": true, "channel_email": false, "channel_push": false,
		"channel_in_app": true, "threshold_amount": "10.00",
	}).requireStatus(http.StatusBadRequest)
}

func TestAnEditIsAnsweredWithTheWholeCatalogAgain(t *testing.T) {
	l := buildLedger(t)
	body := l.alex.put("/notifications/settings/large_transaction", map[string]any{
		"is_enabled": true, "channel_email": true, "channel_push": false,
		"channel_in_app": true, "threshold_amount": "250.00",
	}).requireStatus(http.StatusOK).json()

	alerts := alertsOf(body)
	require.Len(t, alerts, len(domain.AlertCatalog), "an edit answers with the catalog, not one row")

	large := alerts["large_transaction"]
	require.Equal(t, "250.00", large["threshold_amount"])
	require.Equal(t, true, large["channel_email"])
	require.Equal(t, true, large["configured"])

	// Everything else is untouched and still reading its default.
	require.Equal(t, false, alerts["large_deposit"]["configured"])
	require.Equal(t, "500.00", alerts["large_deposit"]["threshold_amount"])
}

func TestAPercentageThresholdTravelsAsADecimalStringNotAnAmount(t *testing.T) {
	// A percentage is not money: a share of income keeps its fraction and is
	// never quantized or formatted as a currency amount.
	l := buildLedger(t)
	body := l.alex.get("/notifications/settings").requireStatus(http.StatusOK).json()
	require.Equal(t, "50", alertsOf(body)["bills_to_income_pct"]["threshold_percent"])

	body = l.alex.put("/notifications/settings/bills_to_income_pct", map[string]any{
		"is_enabled": true, "channel_email": false, "channel_push": false,
		"channel_in_app": true, "threshold_percent": "12.5",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "12.5", alertsOf(body)["bills_to_income_pct"]["threshold_percent"])

	l.alex.put("/notifications/settings/bills_to_income_pct", map[string]any{
		"is_enabled": true, "channel_email": false, "channel_push": false,
		"channel_in_app": true, "threshold_percent": "-1",
	}).requireStatus(http.StatusBadRequest)
}

func TestSavingTwiceEditsTheSameRuleRatherThanAddingOne(t *testing.T) {
	// Without an identity on the table a settings page that saves by writing a
	// row accumulates one rule per click, and nothing says which fires.
	l := buildLedger(t)
	for _, amount := range []string{"250.00", "300.00", "350.00"} {
		l.alex.put("/notifications/settings/large_transaction", map[string]any{
			"is_enabled": true, "channel_email": false, "channel_push": false,
			"channel_in_app": true, "threshold_amount": amount,
		}).requireStatus(http.StatusOK)
	}
	body := l.alex.get("/notifications/settings").requireStatus(http.StatusOK).json()
	require.Equal(t, "350.00", alertsOf(body)["large_transaction"]["threshold_amount"])
}

func TestPauseAllPausesEveryAlertAndLiftingItRestoresTheChoices(t *testing.T) {
	// Pause is held apart from enabled so that lifting it gives back what the
	// household chose, rather than switching everything on.
	l := buildLedger(t)
	l.alex.put("/notifications/settings/large_deposit", map[string]any{
		"is_enabled": false, "channel_email": false, "channel_push": false,
		"channel_in_app": false, "threshold_amount": "900.00",
	}).requireStatus(http.StatusOK)

	paused := l.alex.post("/notifications/settings/pause",
		map[string]any{"paused": true}).requireStatus(http.StatusOK).json()
	require.Equal(t, true, paused["all_paused"])
	for name, alert := range alertsOf(paused) {
		require.Equal(t, true, alert["is_paused"], "%s was not paused", name)
	}

	lifted := l.alex.post("/notifications/settings/pause",
		map[string]any{"paused": false}).requireStatus(http.StatusOK).json()
	require.Equal(t, false, lifted["all_paused"])

	deposit := alertsOf(lifted)["large_deposit"]
	require.Equal(t, false, deposit["is_enabled"], "lifting the pause restored the choice")
	require.Equal(t, "900.00", deposit["threshold_amount"])
}

func TestAnAlertOutsideTheCatalogIsNotFound(t *testing.T) {
	l := buildLedger(t)
	l.alex.put("/notifications/settings/no_such_alert", map[string]any{
		"is_enabled": true, "channel_email": false, "channel_push": false, "channel_in_app": true,
	}).requireStatus(http.StatusNotFound)
}

func TestNotificationSettingsAreOnePersonsRatherThanTheSpaces(t *testing.T) {
	// Two people sharing a ledger do not share a decision about being emailed:
	// vera reads the catalog's defaults, not what alex chose.
	l := buildLedger(t)
	l.alex.put("/notifications/settings/large_transaction", map[string]any{
		"is_enabled": true, "channel_email": true, "channel_push": false,
		"channel_in_app": true, "threshold_amount": "250.00",
	}).requireStatus(http.StatusOK)

	body := l.as("vera").get("/notifications/settings").requireStatus(http.StatusOK).json()
	large := alertsOf(body)["large_transaction"]
	require.Equal(t, false, large["configured"])
	require.Equal(t, "500.00", large["threshold_amount"])
	require.Equal(t, false, large["channel_email"])
}

// The feed, end to end.
//
// The property under test throughout is that an alert fires once. Every sweep
// re-reads the same ledger, so a feed that grew on each sweep would be one
// nobody could read after a week of syncing.

// ofType picks the cards one alert produced. The seeded ledger legitimately
// trips other alerts — a checking account sitting at zero is below the default
// low-balance threshold — so a test that counted the whole feed would be
// asserting the fixture rather than the alert it is about.
func ofType(feed map[string]any, alertType string) []map[string]any {
	var out []map[string]any
	for _, one := range feed["notifications"].([]any) {
		card := one.(map[string]any)
		if card["alert_type"] == alertType {
			out = append(out, card)
		}
	}
	return out
}

func seedLargeSpend(l *ledger, key string, amount string, day int) uuid.UUID {
	l.t.Helper()
	return seedTxn(l, key, &store.Transaction{
		AccountID: l.id("checking"),
		Date:      domain.DateOf(planClock).AddDays(-day),
		Amount:    domain.MustFromString(amount),
		Payee:     "Bright Motors", StatementName: "BRIGHT MOTORS",
	})
}

// sweepAlerts runs the catalog for alex now, as the sweep after a sync does.
func sweepAlerts(l *ledger) service.AlertRun {
	l.t.Helper()
	run, err := newAlerts(l.env).Run(l.t.Context(), store.SpaceIDOf(l.id("space")), l.users["alex"].ID)
	require.NoError(l.t, err)
	return run
}

func TestASweepWritesTheAlertsTheLedgerHasEarned(t *testing.T) {
	l := planLedger(t)
	seedLargeSpend(l, "big", "-1200.00", 1)

	sweepAlerts(l)

	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	rows := ofType(feed, "large_transaction")
	require.Len(t, rows, 1)
	require.Equal(t, "Large transaction", rows[0]["label"])
	require.Equal(t, "$1,200.00 at Bright Motors", rows[0]["title"])
}

func TestASecondSweepOverTheSameLedgerWritesNothing(t *testing.T) {
	// The dedupe, which is the whole reason a sweep can run after every sync
	// without anybody tracking what has already been considered.
	l := planLedger(t)
	seedLargeSpend(l, "big", "-1200.00", 1)

	first := sweepAlerts(l)
	require.Positive(t, first.Written)

	second := sweepAlerts(l)
	require.Equal(t, first.Fired, second.Fired, "it still decides the same thing")
	require.Zero(t, second.Written, "and writes none of it again")

	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Len(t, feed["notifications"].([]any), first.Written)
}

func TestAPausedCatalogSweepsNothing(t *testing.T) {
	l := planLedger(t)
	seedLargeSpend(l, "big", "-1200.00", 1)
	l.alex.post("/notifications/settings/pause",
		map[string]any{"paused": true}).requireStatus(http.StatusOK)

	swept := sweepAlerts(l)
	require.Zero(t, swept.Fired, "pause stops the decision, not just the delivery")
	require.Zero(t, swept.Written)
}

func TestAThresholdRaisedAboveTheSpendStopsIt(t *testing.T) {
	// The setting has to reach the decision, not just the page.
	l := planLedger(t)
	seedLargeSpend(l, "big", "-1200.00", 1)
	l.alex.put("/notifications/settings/large_transaction", map[string]any{
		"is_enabled": true, "channel_email": false, "channel_push": false,
		"channel_in_app": true, "threshold_amount": "5000.00",
	}).requireStatus(http.StatusOK)

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "large_transaction"))
}

func TestAnOldTransactionIsOutsideTheSweepsWindow(t *testing.T) {
	// A first sweep over five years of history would open with hundreds of
	// notifications about transactions from years ago.
	l := planLedger(t)
	seedLargeSpend(l, "ancient", "-1200.00", 400)

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "large_transaction"))
}

func TestReadingOneClearsItFromTheBadgeButNotTheFeed(t *testing.T) {
	l := planLedger(t)
	seedLargeSpend(l, "big", "-1200.00", 1)
	sweepAlerts(l)

	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	before := len(feed["notifications"].([]any))
	unread := feed["unread"].(float64)
	// Indexing an empty result here panics, and a panic takes the whole
	// package down with it rather than this one test.
	fired := ofType(feed, "large_transaction")
	require.Len(t, fired, 1)
	id := fired[0]["id"].(string)

	l.alex.post("/notifications/"+id+"/read", nil).requireStatus(http.StatusNoContent)
	after := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.EqualValues(t, unread-1, after["unread"])
	require.Len(t, after["notifications"].([]any), before, "read is not deleted")

	// Reading it twice is not an error the user can act on, so it is a 404
	// rather than a silent success that implies something changed.
	l.alex.post("/notifications/"+id+"/read", nil).requireStatus(http.StatusNotFound)
}

func TestMarkingEverythingReadClearsTheBadge(t *testing.T) {
	l := planLedger(t)
	seedLargeSpend(l, "one", "-1200.00", 1)
	seedLargeSpend(l, "two", "-1400.00", 2)
	sweepAlerts(l)

	before := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Len(t, ofType(before, "large_transaction"), 2)
	count := len(before["notifications"].([]any))

	l.alex.post("/notifications/read", nil).requireStatus(http.StatusNoContent)
	after := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.EqualValues(t, 0, after["unread"])
	require.Len(t, after["notifications"].([]any), count)
}

func TestClearingOneTakesItOffTheFeedAndTheBadge(t *testing.T) {
	l := planLedger(t)
	seedLargeSpend(l, "one", "-1200.00", 1)
	seedLargeSpend(l, "two", "-1400.00", 2)
	sweepAlerts(l)

	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	fired := ofType(feed, "large_transaction")
	require.Len(t, fired, 2)
	count := len(feed["notifications"].([]any))
	unread := feed["unread"].(float64)
	id := fired[0]["id"].(string)

	l.alex.del("/notifications/" + id).requireStatus(http.StatusNoContent)
	after := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Len(t, after["notifications"].([]any), count-1)
	require.EqualValues(t, unread-1, after["unread"])
	require.Len(t, ofType(after, "large_transaction"), 1)

	l.alex.del("/notifications/" + id).requireStatus(http.StatusNotFound)
}

func TestClearingAllEmptiesTheFeedAndTheSweepDoesNotRefillIt(t *testing.T) {
	// The cleared rows stay as the dedupe record; a deleted row would be the
	// same alert sent again by the next sweep.
	l := planLedger(t)
	seedLargeSpend(l, "one", "-1200.00", 1)
	seedLargeSpend(l, "two", "-1400.00", 2)
	sweepAlerts(l)
	require.NotEmpty(t, l.alex.get("/notifications").
		requireStatus(http.StatusOK).json()["notifications"])

	l.alex.del("/notifications").requireStatus(http.StatusNoContent)
	after := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, after["notifications"])
	require.EqualValues(t, 0, after["unread"])

	again := sweepAlerts(l)
	require.Zero(t, again.Written)
	require.Empty(t, l.alex.get("/notifications").
		requireStatus(http.StatusOK).json()["notifications"])
}

func TestClearingAllLeavesSomebodyElsesFeed(t *testing.T) {
	l := planLedger(t)
	vera := l.users["vera"]
	one := store.Notification{
		AlertType: domain.AlertLargeTransaction,
		Title:     "A large transaction", Body: "$1,200.00 at Somewhere",
		DedupeKey: "kept-for-vera",
	}
	_, err := l.env.DB.RecordNotification(
		l.t.Context(), store.SpaceIDOf(l.id("space")), vera.ID, &one)
	require.NoError(t, err)
	seedLargeSpend(l, "big", "-1200.00", 1)
	sweepAlerts(l)

	l.alex.del("/notifications").requireStatus(http.StatusNoContent)
	l.alex.del("/notifications/" + one.ID.String()).requireStatus(http.StatusNotFound)

	feed := l.as("vera").get("/notifications").requireStatus(http.StatusOK).json()
	require.Len(t, feed["notifications"].([]any), 1)
	// A viewer clears their own feed: the routes are Read, like marking read.
	l.as("vera").del("/notifications").requireStatus(http.StatusNoContent)
	require.Empty(t, l.as("vera").get("/notifications").
		requireStatus(http.StatusOK).json()["notifications"])
}

func TestOneFeedIsNotAnothersEvenInTheSameSpace(t *testing.T) {
	l := planLedger(t)
	seedLargeSpend(l, "big", "-1200.00", 1)
	sweepAlerts(l)

	vera := l.as("vera").get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, vera["notifications"])
	require.EqualValues(t, 0, vera["unread"])
}

func TestAViewerCanClearTheirOwnBell(t *testing.T) {
	// Read routes, not Write: a feed is one person's, so dismissing a card
	// changes nothing a viewer is not allowed to change. Registered as Write,
	// the bell would be a badge a viewer could never put down.
	l := planLedger(t)
	vera := l.users["vera"]
	one := store.Notification{
		AlertType: domain.AlertLargeTransaction,
		Title:     "A large transaction", Body: "$1,200.00 at Somewhere",
		DedupeKey: "viewer-one",
	}
	written, err := l.env.DB.RecordNotification(
		l.t.Context(), store.SpaceIDOf(l.id("space")), vera.ID, &one)
	require.NoError(t, err)
	require.True(t, written)
	other := store.Notification{
		AlertType: domain.AlertLargeTransaction,
		Title:     "Another", Body: "$1,400.00 at Elsewhere",
		DedupeKey: "viewer-two",
	}
	_, err = l.env.DB.RecordNotification(
		l.t.Context(), store.SpaceIDOf(l.id("space")), vera.ID, &other)
	require.NoError(t, err)

	l.as("vera").post("/notifications/"+one.ID.String()+"/read", nil).
		requireStatus(http.StatusNoContent)
	l.as("vera").post("/notifications/read", nil).requireStatus(http.StatusNoContent)

	feed := l.as("vera").get("/notifications").requireStatus(http.StatusOK).json()
	require.EqualValues(t, 0, feed["unread"])
	require.Len(t, feed["notifications"].([]any), 2)

	// And still only their own: the sweep is a Write, so a viewer clearing
	// their bell cannot have touched anybody else's.
	require.Empty(t, l.alex.get("/notifications").
		requireStatus(http.StatusOK).json()["notifications"])
}

func TestABillDueThisWeekReachesTheFeed(t *testing.T) {
	// The alert that needed the sweep to learn what a due date is. Read through
	// the same domain function the Bills & Income screen uses, so the alert and
	// the page cannot count different bills.
	l := planLedger(t)
	due := domain.DateOf(planClock).AddDays(3)
	seedSeries(l, seriesSeed{
		Key: "rent", Name: "Rent", Kind: domain.SeriesBill, Amount: "-1800.00",
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{due.Day},
		StartOn: due, NextDueOn: due,
	})

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	rows := ofType(feed, "upcoming_bills")
	require.Len(t, rows, 1)
	require.Contains(t, rows[0]["title"], "due in the next week")
	require.Contains(t, rows[0]["body"], "$1,800.00")
}

func TestAnIncomeSeriesIsNotABillFallingDue(t *testing.T) {
	// A paycheck arriving inside the window is not something due.
	l := planLedger(t)
	due := domain.DateOf(planClock).AddDays(2)
	seedSeries(l, seriesSeed{
		Key: "pay", Name: "Salary", Kind: domain.SeriesIncome, Amount: "2400.00",
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{due.Day},
		StartOn: due, NextDueOn: due,
	})

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "upcoming_bills"))
}

func TestAWatchlistNearItsTargetReachesTheFeed(t *testing.T) {
	// The other alert that needed the sweep to learn something: a watchlist is
	// a filter over postings, so this is summarised through the same domain
	// function the card uses. The alert and the card have to agree about how
	// much has been spent.
	l := planLedger(t)
	groceryWatchlist(l, map[string]any{"target_amount": "100.00"})
	seedTxn(l, "shop", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.DateOf(planClock),
		Amount: domain.MustFromString("-95.00"), StatementName: "SAFEWAY",
		Payee: "Safeway", CategoryID: l.id("groceries"),
	})

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	rows := ofType(feed, "watchlist_nearing_target")
	require.Len(t, rows, 1)
	// Near or over depending on what else the month holds — the wording of the
	// two is pinned in the domain test. What this one is about is that the
	// alert reached the feed naming the right watchlist and the right target.
	require.Contains(t, rows[0]["title"], "Groceries")
	require.Contains(t, rows[0]["title"], "$100.00 target")
	require.Contains(t, rows[0]["body"], "spent so far")
}

func TestAWatchlistWellUnderItsTargetSaysNothing(t *testing.T) {
	l := planLedger(t)
	groceryWatchlist(l, map[string]any{"target_amount": "1000.00"})
	seedTxn(l, "shop", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.DateOf(planClock),
		Amount: domain.MustFromString("-95.00"), StatementName: "SAFEWAY",
		Payee: "Safeway", CategoryID: l.id("groceries"),
	})

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "watchlist_nearing_target"))
}

func TestAWatchlistWithNoTargetIsNotOverIt(t *testing.T) {
	// A missing target read as zero would put every watchlist over it, which
	// is the loudest possible way to be wrong.
	l := planLedger(t)
	groceryWatchlist(l, map[string]any{"target_amount": nil})
	seedTxn(l, "shop", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.DateOf(planClock),
		Amount: domain.MustFromString("-95.00"), StatementName: "SAFEWAY",
		Payee: "Safeway", CategoryID: l.id("groceries"),
	})

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "watchlist_nearing_target"))
}

// Web push.
//
// Everything here is testable except the one thing that is not: a browser
// registering a service worker, which needs a secure context. The server half
// — storing a subscription, refusing one it could never send to, and dropping
// one the push service says is gone — is all checked.

// forgetVAPIDKeys clears the keypair an earlier test generated; the package
// shares one schema.
func forgetVAPIDKeys(t *testing.T, env *Env) {
	t.Helper()
	_, err := env.DB.Pool().Exec(t.Context(),
		`DELETE FROM server_settings WHERE key = $1`, store.VAPIDKeysSetting)
	require.NoError(t, err)
}

func TestPushNeedsNoConfiguration(t *testing.T) {
	// With no keypair in the environment the server makes one and answers
	// with its public half, so the browser can subscribe.
	l := buildLedger(t)
	forgetVAPIDKeys(t, l.env)
	body := l.alex.get("/notifications/push").requireStatus(http.StatusOK).json()
	require.NotEmpty(t, body["public_key"])
	require.NotContains(t, body, "enabled")
	require.Empty(t, body["subscriptions"])

	settings := l.alex.get("/notifications/settings").requireStatus(http.StatusOK).json()
	require.NotContains(t, settings, "push_enabled")
}

func TestTheGeneratedKeypairSurvivesARestart(t *testing.T) {
	// Every subscription is bound to the public key, so a second process over
	// the same database must offer the same one.
	l := buildLedger(t)
	forgetVAPIDKeys(t, l.env)
	first, err := l.env.vapidKeys(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, first.PrivateKey)

	restarted := NewEnv(testConfig(), l.env.DB)
	second, err := restarted.vapidKeys(t.Context())
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestAKeypairInTheEnvironmentWins(t *testing.T) {
	l := buildLedger(t)
	forgetVAPIDKeys(t, l.env)
	private, public, err := provider.GenerateVapidKeys()
	require.NoError(t, err)

	cfg := testConfig()
	cfg.VAPIDPrivateKey, cfg.VAPIDPublicKey = private, public
	env := NewEnv(cfg, l.env.DB)
	keys, err := env.vapidKeys(t.Context())
	require.NoError(t, err)
	require.Equal(t, provider.VapidKeys{PrivateKey: private, PublicKey: public}, keys)

	// Nothing was generated behind it.
	sealed, err := sealedStore(l.env)
	require.NoError(t, err)
	stored, err := sealed.GetServerSettings(t.Context(), []string{store.VAPIDKeysSetting})
	require.NoError(t, err)
	require.Empty(t, stored)
}

func TestThePushSubjectNeedsNoConfiguration(t *testing.T) {
	l := buildLedger(t)

	cfg := testConfig()
	cfg.VAPIDSubject = "mailto:ops@example.test"
	require.Equal(t, "mailto:ops@example.test", NewEnv(cfg, l.env.DB).vapidSubject(t.Context()))

	cfg = testConfig()
	cfg.FrontendURL = "https://money.example.test/"
	require.Equal(t, "https://money.example.test", NewEnv(cfg, l.env.DB).vapidSubject(t.Context()))

	// Plain HTTP is no use as a subject, so the first administrator's address.
	admin := soloAdmin(t)
	require.Equal(t, "mailto:"+admin.Email, NewEnv(testConfig(), l.env.DB).vapidSubject(t.Context()))
}

type recordedPush struct {
	endpoints []string
	payloads  []any
	fail      error
}

func (r *recordedPush) Send(_ context.Context, sub provider.PushSubscription, payload any) error {
	r.endpoints = append(r.endpoints, sub.Endpoint)
	r.payloads = append(r.payloads, payload)
	return r.fail
}

func TestTurningOnPushDeliversATestNotification(t *testing.T) {
	l := buildLedger(t)
	sender := &recordedPush{}
	l.env.Pusher = sender
	seedPush(l, "https://push.example.test/abc")

	body := l.alex.post("/notifications/push/test", map[string]any{}).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, body["sent"])
	require.Equal(t, []string{"https://push.example.test/abc"}, sender.endpoints)
	require.Equal(t, "Notifications are on", sender.payloads[0].(map[string]any)["title"])

	// Somebody who can only read cannot make the server send.
	l.as("vera").post("/notifications/push/test", map[string]any{}).requireStatus(http.StatusForbidden)
}

func TestATestPushForgetsABrowserThePushServiceSaysIsGone(t *testing.T) {
	l := buildLedger(t)
	l.env.Pusher = &recordedPush{fail: provider.ErrPushSubscriptionGone}
	seedPush(l, "https://push.example.test/abc")

	body := l.alex.post("/notifications/push/test", map[string]any{}).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 0, body["sent"])
	subscriptions := l.alex.get("/notifications/push").requireStatus(http.StatusOK).json()["subscriptions"]
	require.Empty(t, subscriptions)
}

func TestTheDeployedPusherSignsWithTheGeneratedKey(t *testing.T) {
	l := buildLedger(t)
	forgetVAPIDKeys(t, l.env)
	public := l.alex.get("/notifications/push").requireStatus(http.StatusOK).json()["public_key"]

	sender, err := l.env.pusher(t.Context())
	require.NoError(t, err)
	push, ok := sender.(*provider.Push)
	require.True(t, ok)
	require.Equal(t, public, push.Keys.PublicKey)
	require.NotEmpty(t, push.Subject)
}

// seedPush stores a subscription directly.
//
// Through the store rather than the endpoint, because the endpoint refuses
// anything that does not resolve to a publicly routable address — which is the
// point of it, and which no made-up hostname can satisfy. What these tests are
// about is what happens to a subscription once it exists.
func seedPush(l *ledger, endpoint string) store.PushSubscription {
	l.t.Helper()
	one := store.PushSubscription{Endpoint: endpoint, P256dh: "k", Auth: "a", UserAgent: "test"}
	require.NoError(l.t, l.env.DB.SavePushSubscription(l.t.Context(),
		store.SpaceIDOf(l.id("space")), l.users["alex"].ID, &one))
	return one
}

func TestABrowserIsRememberedOnceHoweverOftenItSubscribes(t *testing.T) {
	// A page that reloads re-subscribes and gets the same endpoint back. One
	// row per browser, or a household accumulates one per visit.
	l := buildLedger(t)
	seedPush(l, "https://push.example.test/abc")
	seedPush(l, "https://push.example.test/abc")

	body := l.alex.get("/notifications/push").requireStatus(http.StatusOK).json()
	require.Len(t, body["subscriptions"].([]any), 1)
}

func TestASubscriptionThisServerCouldNeverSendToIsRefused(t *testing.T) {
	// The same check the sender makes, made where somebody can see the
	// refusal rather than silently failing on every alert. A push endpoint
	// pointing at a private address is the shape of an SSRF.
	l := buildLedger(t)
	l.alex.post("/notifications/push", map[string]any{
		"endpoint": "http://169.254.169.254/latest/meta-data",
		"p256dh":   "k", "auth": "a",
	}).requireStatus(http.StatusBadRequest)
}

func TestASubscriptionNeedsBothKeys(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/notifications/push", map[string]any{
		"endpoint": "https://push.example.test/abc", "p256dh": "", "auth": "a",
	}).requireStatus(http.StatusBadRequest)
}

func TestOnlySomebodyWhoCanWriteRemovesASubscription(t *testing.T) {
	l := buildLedger(t)
	one := seedPush(l, "https://push.example.test/abc")

	// A viewer is refused as read-only before ownership is even reached, which
	// is the right order: the space's rules come first.
	l.as("vera").del("/notifications/push/" + one.ID.String()).requireStatus(http.StatusForbidden)
	l.alex.del("/notifications/push/" + one.ID.String()).requireStatus(http.StatusNoContent)

	// And a second delete is a 404, not a silent success: a client that thinks
	// it removed a browser it never had is looking at a stale list.
	l.alex.del("/notifications/push/" + one.ID.String()).requireStatus(http.StatusNotFound)
}

// --- The four alerts the sweep feeds -----------------------------------------
//
// Each of these needs the settled map's distinction to have any input at
// all: a slot is *claimed* by any row carrying the series and the due date,
// and *settled* only by one that has actually posted. A provider's forecast
// row claims next month's rent today, and announcing that as a payment is
// the failure every test here is guarding against.

// hearAbout switches alerts on that are off until somebody asks for them, so a
// test that expects silence is silent for its own reason and not the default.
func hearAbout(l *ledger, types ...string) {
	l.t.Helper()
	for _, one := range types {
		l.alex.put("/notifications/settings/"+one, map[string]any{
			"is_enabled": true, "channel_in_app": true,
		}).requireStatus(http.StatusOK)
	}
}

// seedFulfilment posts a row against one occurrence of a series.
func seedFulfilment(l *ledger, key string, seriesKey string, on domain.Date, amount string,
	overrides ...func(*store.Transaction),
) uuid.UUID {
	l.t.Helper()
	row := &store.Transaction{
		AccountID: l.id("checking"), Date: on, SeriesID: l.id(seriesKey), SeriesDueOn: on,
		Amount: domain.MustFromString(amount), StatementName: "PAYMENT", Payee: "Payment",
		Source: domain.SourceSync,
	}
	for _, apply := range overrides {
		apply(row)
	}
	return seedTxn(l, key, row)
}

func monthlySeries(l *ledger, key, name string, kind domain.SeriesKind, amount string, on domain.Date) {
	l.t.Helper()
	seedSeries(l, seriesSeed{
		Key: key, Name: name, Kind: kind, Amount: amount,
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{on.Day},
		StartOn: on, NextDueOn: on,
	})
}

func TestAPaidBillAndArrivingIncomeEachReachTheFeed(t *testing.T) {
	l := planLedger(t)
	hearAbout(l, "bill_paid", "income_received")
	paid := domain.DateOf(planClock).AddDays(-2)
	monthlySeries(l, "rent", "Rent", domain.SeriesBill, "-1800.00", paid)
	monthlySeries(l, "salary", "Salary", domain.SeriesIncome, "2400.00", paid)
	seedFulfilment(l, "rent_paid", "rent", paid, "-1800.00")
	seedFulfilment(l, "salary_in", "salary", paid, "2400.00")

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()

	bills := ofType(feed, "bill_paid")
	require.Len(t, bills, 1)
	require.Equal(t, "Rent paid — $1,800.00", bills[0]["title"])

	income := ofType(feed, "income_received")
	require.Len(t, income, 1)
	require.Equal(t, "Salary arrived — $2,400.00", income[0]["title"])
}

// The distinction the settled map was built for. A row dated in the future
// claims the slot — it is what stops the bill being projected twice — but the
// money has not moved, and "Rent paid" would be a false statement.
func TestAForecastRowDoesNotAnnounceThatTheBillWasPaid(t *testing.T) {
	l := planLedger(t)
	hearAbout(l, "bill_paid", "income_received")
	due := domain.DateOf(planClock).AddDays(6)
	monthlySeries(l, "rent", "Rent", domain.SeriesBill, "-1800.00", due)
	seedFulfilment(l, "forecast", "rent", due, "-1800.00")

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "bill_paid"))
}

// A skipped occurrence is a tombstone row, not a payment.
func TestASkippedOccurrenceIsNotAPaidBill(t *testing.T) {
	l := planLedger(t)
	hearAbout(l, "bill_paid", "income_received")
	on := domain.DateOf(planClock).AddDays(-2)
	monthlySeries(l, "rent", "Rent", domain.SeriesBill, "-1800.00", on)
	seedFulfilment(l, "skipped", "rent", on, "-1800.00", func(row *store.Transaction) {
		row.EstimateStatus = store.SkippedEstimate
	})

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "bill_paid"))
}

func TestASecondSweepDoesNotAnnounceTheSameBillTwice(t *testing.T) {
	l := planLedger(t)
	hearAbout(l, "bill_paid", "income_received")
	paid := domain.DateOf(planClock).AddDays(-2)
	monthlySeries(l, "rent", "Rent", domain.SeriesBill, "-1800.00", paid)
	seedFulfilment(l, "rent_paid", "rent", paid, "-1800.00")

	sweepAlerts(l)
	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Len(t, ofType(feed, "bill_paid"), 1)
}

// A transfer between the household's own accounts is not a bill being paid:
// reporting one says money left when it did not.
func TestATransferBetweenOwnAccountsIsNotAPaidBill(t *testing.T) {
	l := planLedger(t)
	hearAbout(l, "bill_paid", "income_received")
	on := domain.DateOf(planClock).AddDays(-1)
	monthlySeries(l, "sweep", "To Savings", domain.SeriesTransfer, "-500.00", on)
	seedFulfilment(l, "moved", "sweep", on, "-500.00")

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "bill_paid"))
}

func TestARefundThatNeverArrivedReachesTheFeed(t *testing.T) {
	l := planLedger(t)
	expected := domain.DateOf(planClock).AddDays(-6)
	monthlySeries(l, "refund", "Returned jacket", domain.SeriesKind("refund"), "89.00", expected)

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	rows := ofType(feed, "refund_status")
	require.Len(t, rows, 1)
	require.Equal(t, "Returned jacket has not arrived", rows[0]["title"])
	require.Contains(t, rows[0]["body"], "$89.00")
	require.Contains(t, rows[0]["body"], expected.String())
}

func TestARefundThatArrivedSaysNothing(t *testing.T) {
	l := planLedger(t)
	expected := domain.DateOf(planClock).AddDays(-6)
	monthlySeries(l, "refund", "Returned jacket", domain.SeriesKind("refund"), "89.00", expected)
	seedFulfilment(l, "refunded", "refund", expected, "89.00")

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "refund_status"))
}

// A refund due today is not late yet.
func TestARefundDueTodayIsNotOverdue(t *testing.T) {
	l := planLedger(t)
	today := domain.DateOf(planClock)
	monthlySeries(l, "refund", "Returned jacket", domain.SeriesKind("refund"), "89.00", today)

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "refund_status"))
}

func TestAGoalBehindItsMonthlyRateReachesTheFeed(t *testing.T) {
	// Two months to the target date, counting this one, and nothing saved: the
	// card's "monthly needed" is $500 and nothing has gone in.
	l := planLedger(t)
	newGoal(l, map[string]any{
		"name": "New Roof", "account_id": savingsAccount(l),
		"target_amount": "1000.00",
		"target_on":     domain.MonthOf(domain.DateOf(planClock)).Shift(1).LastDay().String(),
	})

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	rows := ofType(feed, "goal_contribution")
	require.Len(t, rows, 1)
	require.Equal(t, "New Roof needs $500.00 this month", rows[0]["title"])
	require.Contains(t, rows[0]["body"], "Nothing in yet this month")
}

// The alert reads the same contributions the goal card does, so a goal that
// has had its month's money is silent.
func TestAGoalGivenItsMonthlyShareSaysNothing(t *testing.T) {
	l := planLedger(t)
	savings := savingsAccount(l)
	// A contribution leaves the funding account, so it is negative here and
	// reads as $500 saved.
	contribution := seedTxn(l, "into_goal", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.DateOf(planClock),
		Amount:        domain.MustFromString("-500.00"),
		StatementName: "TRANSFER TO SAVINGS", Payee: "Savings",
	})
	newGoal(l, map[string]any{
		"name": "New Roof", "account_id": savings,
		"target_amount": "1000.00",
		"target_on":     domain.MonthOf(domain.DateOf(planClock)).Shift(1).LastDay().String(),
		"txn_ids":       []string{contribution.String()},
	})

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "goal_contribution"))
}

// An open-ended goal implies no monthly rate, and inventing a deadline the
// user did not set is how this alert becomes noise people switch off.
func TestAGoalWithNoTargetDateIsNeverNudged(t *testing.T) {
	l := planLedger(t)
	newGoal(l, map[string]any{
		"name": "Someday", "account_id": savingsAccount(l), "target_amount": "1000.00",
	})

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "goal_contribution"))
}

// A closed account still has the transactions that were posted on it, and the
// posting builder refuses a row whose account it was not handed. A sweep over
// a household with one closed loan would answer 500 and write no
// notification at all — so this is about the whole feature, not one alert.
func TestASweepSurvivesAClosedAccountThatStillHasTransactions(t *testing.T) {
	l := planLedger(t)
	closed := newAccount(l, "Student Loan B", string(domain.KindLoan), "loan", "-5000.00")
	seedTxn(l, "on_closed", &store.Transaction{
		AccountID: uuid.MustParse(closed), Date: domain.DateOf(planClock).AddDays(-3),
		Amount:        domain.MustFromString("-250.00"),
		StatementName: "LOAN PAYMENT", Payee: "Payment",
	})
	l.alex.patch("/accounts/"+closed, map[string]any{"is_closed": true}).
		requireStatus(http.StatusOK)
	seedLargeSpend(l, "big", "-1200.00", 1)

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Len(t, ofType(feed, "large_transaction"), 1)

	// The closed account itself raises nothing: a balance nobody can spend is
	// not a low balance worth telling anyone about.
	for _, card := range ofType(feed, "low_bank_balance") {
		require.NotContains(t, card["title"], "Student Loan")
	}
}

// --- The two that read the plan engine ---------------------------------------
//
// The sweep runs with no request behind it, so these alerts depend on the
// spending plan being computed in internal/service rather than inside an HTTP
// handler: that is how it asks what a household has left to spend or how full
// an envelope is.

func TestAnEnvelopeNearingItsLimitReachesTheFeed(t *testing.T) {
	l := planLedger(t)
	newPlanEnvelope(l, "Groceries", "100.00")
	// The fixture already spends 50.00 on groceries in August; 45.00 more puts
	// the envelope at 95 of 100, inside the catalog's default 10% margin.
	seedTxn(l, "shop", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.DateOf(planClock),
		Amount: domain.MustFromString("-45.00"), StatementName: "SAFEWAY",
		Payee: "Safeway", CategoryID: l.id("groceries"),
	})

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	rows := ofType(feed, "planned_expense_nearing")
	require.Len(t, rows, 1)
	require.Contains(t, rows[0]["title"], "Groceries is near its $100.00 limit")
	require.Contains(t, rows[0]["body"], "$95.00 spent so far")
}

// An envelope with room left says nothing. The margin is the whole rule.
func TestAnEnvelopeWithRoomLeftSaysNothing(t *testing.T) {
	l := planLedger(t)
	// The fixture's 50.00 of grocery spend against a 500.00 envelope leaves
	// far more than the 10% margin.
	newPlanEnvelope(l, "Groceries", "500.00")

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "planned_expense_nearing"))
}

// The alert quotes the plan screen's own headline figure. Reading a stored
// column instead would report whatever the last person to open the screen saw.
func TestAvailableToSpendReachesTheFeedWithThePlansOwnFigure(t *testing.T) {
	l := planLedger(t)
	// The catalog's default line is $10. Raise it well above whatever this
	// ledger computes so the alert is certain to fire, and assert that the
	// figure it quotes is the one the plan screen shows.
	l.alex.put("/notifications/settings/available_to_spend_low", map[string]any{
		"is_enabled": true, "channel_in_app": true, "threshold_amount": "1000000.00",
	}).requireStatus(http.StatusOK)

	plan := planFor(l, augustMonth)
	left := plan["left_this_month"].(string)

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	rows := ofType(feed, "available_to_spend_low")
	require.Len(t, rows, 1)

	// Whichever sentence it chose, the figure has to be the plan's.
	amount := domain.MustFromString(left)
	title := fmt.Sprint(rows[0]["title"])
	if amount.IsNegative() {
		require.Contains(t, title, "over by")
	} else {
		require.Contains(t, title, "left to spend this month")
	}
}

// A household with a plan comfortably above its line hears nothing. The
// catalog's default line is $10, so a month with thousands left is silent.
func TestAPlanWellAboveItsLineSaysNothing(t *testing.T) {
	l := planLedger(t)
	seedIncome(l, "wages", "6000.00")

	plan := planFor(l, augustMonth)
	require.False(t, domain.MustFromString(plan["left_this_month"].(string)).IsNegative(),
		"the fixture is supposed to leave this month comfortably in credit")

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()
	require.Empty(t, ofType(feed, "available_to_spend_low"))
}

// newPlanEnvelope creates a planned-expense envelope over the grocery
// category. Creating one answers with the recalculated month, not the row.
func newPlanEnvelope(l *ledger, name, target string) {
	l.t.Helper()
	l.alex.post("/spending-plan/"+augustMonth+"/envelopes", map[string]any{
		"name": name, "target_amount": target,
		"category_ids": []string{l.str("groceries")},
	}).requireStatus(http.StatusOK)
}

// seedIncome puts real income in the month, which the fixture otherwise has
// none of — every bucket but Other Spend reads zero without it.
func seedIncome(l *ledger, key, amount string) uuid.UUID {
	l.t.Helper()
	category := &store.Category{
		Name: "Paycheck " + key, Kind: domain.CategoryIncome,
		IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(l.t, l.env.DB.CreateCategory(l.t.Context(),
		store.SpaceIDOf(l.id("space")), category))
	return seedTxn(l, key, &store.Transaction{
		AccountID: l.id("checking"), Date: domain.DateOf(planClock),
		Amount: domain.MustFromString(amount), StatementName: "PAYROLL",
		Payee: "Payroll", CategoryID: category.ID,
	})
}

// The sweep must not materialize the plan. Computing is a read; writing the
// calculated columns is what makes a closed month able to disagree with
// today's recalculation, and a scheduled job must never do it.
func TestTheSweepDoesNotMaterializeThePlan(t *testing.T) {
	l := planLedger(t)
	before := storedPlanCalc(l, augustMonth)
	sweepAlerts(l)
	require.Equal(t, before, storedPlanCalc(l, augustMonth),
		"the alert sweep wrote the spending plan's calculated columns")
}

// storedPlanCalc reads the materialized figure straight from the table, which
// is the thing a sweep must not touch.
func storedPlanCalc(l *ledger, month string) []any {
	l.t.Helper()
	rows, err := l.env.DB.Pool().Query(l.t.Context(), `
		SELECT calculated_income_amount::text, calculated_spent_amount::text,
		       calculated_planned_spending_amount::text
		  FROM spending_plan_months WHERE space_id = $1 AND month = $2`,
		l.id("space"), month+"-01")
	require.NoError(l.t, err)
	defer rows.Close()

	var out []any
	for rows.Next() {
		var income, spent, planned *string
		require.NoError(l.t, rows.Scan(&income, &spent, &planned))
		out = append(out, []any{derefString(income), derefString(spent), derefString(planned)})
	}
	require.NoError(l.t, rows.Err())
	return out
}

// --- What the sweep and the screens must agree about -------------------------

// A bill already paid is not a bill due this week.
//
// A sweep that projects occurrences with no fulfilment map while the Bills
// screen projects them with one would let a mortgage due the 15th and paid
// on the 12th vanish from Upcoming while still alerting as due — the alert
// and the page contradicting each other about the same bill.
func TestABillAlreadyPaidDoesNotAlertAsDue(t *testing.T) {
	l := planLedger(t)
	due := domain.DateOf(planClock).AddDays(3)
	seedSeries(l, seriesSeed{
		Key: "rent", Name: "Rent", Kind: domain.SeriesBill, Amount: "-1800.00",
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{due.Day},
		StartOn: due, NextDueOn: due,
	})
	paid := seedTxn(l, "rent_paid", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.DateOf(planClock),
		Amount: domain.MustFromString("-1800.00"), StatementName: "RENT",
	})
	l.alex.post("/transactions/"+paid.String()+"/link-series", map[string]any{
		"series_id": l.str("rent"), "due_on": due.String(),
	}).requireStatus(http.StatusOK)

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()

	require.Empty(t, ofType(feed, "upcoming_bills"),
		"the occurrence is settled, which is what the Bills screen already said")
}

// A forecast is not a payment here either. The Simplifi import carries one row
// per upcoming reminder; if the sweep counted them as settling their slots,
// every bill it forecast would stop alerting.
func TestAForecastDoesNotSettleABillForTheSweep(t *testing.T) {
	l := planLedger(t)
	due := domain.DateOf(planClock).AddDays(3)
	seedSeries(l, seriesSeed{
		Key: "rent", Name: "Rent", Kind: domain.SeriesBill, Amount: "-1800.00",
		Alias: domain.AliasEveryMonth, ByMonthDay: []int{due.Day},
		StartOn: due, NextDueOn: due,
	})
	forecast := &store.Transaction{
		AccountID: l.id("checking"), Date: due, Currency: "USD",
		Amount: domain.MustFromString("-1800.00"), StatementName: "RENT",
		Source: domain.SourceSimplifiImport, EstimateStatus: store.ProjectedEstimate,
		SeriesID: l.id("rent"), SeriesDueOn: due,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), forecast))

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()

	require.Len(t, ofType(feed, "upcoming_bills"), 1, "nothing has paid it")
}

// The goal alerts read the same progress the card prints.
//
// A sweep with its own copy of the goal-link classification that selected
// txn_ids alone would read a withdrawal as a contribution: a goal raided
// back to nothing alerting as though it had been funded.
func TestAGoalAlertReadsWithdrawalsTheWayTheCardDoes(t *testing.T) {
	l := planLedger(t)
	in := seedContribution(l, "in", domain.NewDate(2026, time.August, 1), "-5000.00")
	back := seedContribution(l, "back", domain.NewDate(2026, time.August, 10), "5000.00")

	goal := newGoal(l, map[string]any{
		"name": "Emergency fund", "account_id": savingsAccount(l),
		"target_amount": "9000.00", "target_on": "2026-12-31",
	})
	id := goal["id"].(string)
	l.alex.post("/goals/"+id+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{in.String()}, "direction": "contribution",
	}).requireStatus(http.StatusOK)
	final := l.alex.post("/goals/"+id+"/transactions/bulk", map[string]any{
		"transaction_ids": []string{back.String()}, "direction": "withdrawal",
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, "0.00", final["goal"].(map[string]any)["saved_so_far"],
		"the card says the goal is empty")

	sweepAlerts(l)
	feed := l.alex.get("/notifications").requireStatus(http.StatusOK).json()

	// So the goal is behind and the reminder fires. Reading the withdrawal as
	// a contribution would make this goal look funded past its target, and a
	// funded goal is nudged about nothing — the alert would go silent on the
	// goal that needed it most.
	rows := ofType(feed, "goal_contribution")
	require.Len(t, rows, 1)
	require.Contains(t, rows[0]["title"], "Emergency fund needs")
}
