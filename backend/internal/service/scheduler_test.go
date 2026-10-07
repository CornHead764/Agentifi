package service

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func at(day string, clock string) time.Time {
	moment, err := time.Parse("2006-01-02 15:04", day+" "+clock)
	if err != nil {
		panic(err)
	}
	return moment
}

func TestTheWindowIsTheMostRecentOccurrence(t *testing.T) {
	window := SyncWindow{Hour: 4, Minute: 0}

	// After the hour: today's window is the one we are in.
	require.Equal(t, at("2026-08-22", "04:00"), window.MostRecent(at("2026-08-22", "09:30")))
	// Before it: yesterday's, because today's has not opened.
	require.Equal(t, at("2026-08-21", "04:00"), window.MostRecent(at("2026-08-22", "03:59")))
	// Exactly on it: the window is open, not pending.
	require.Equal(t, at("2026-08-22", "04:00"), window.MostRecent(at("2026-08-22", "04:00")))
}

func TestAMissedWindowIsStillOpen(t *testing.T) {
	// A server asleep at 04:00 and started at 09:00 must see a connection last
	// synced the previous morning as due.
	window := SyncWindow{Hour: 4, Minute: 0}
	lastSync := at("2026-08-21", "04:00")

	require.True(t, lastSync.Before(window.MostRecent(at("2026-08-22", "09:00"))),
		"a connection last synced yesterday is not due after this morning's missed window")
}

func TestASyncedConnectionIsNotDueAgainThatDay(t *testing.T) {
	window := SyncWindow{Hour: 4, Minute: 0}
	lastSync := at("2026-08-22", "04:03")

	require.False(t, lastSync.Before(window.MostRecent(at("2026-08-22", "23:59"))),
		"a connection synced in this window came up due again the same day")
}

func TestTheNextRunIsAlwaysAhead(t *testing.T) {
	window := SyncWindow{Hour: 4, Minute: 0}

	require.Equal(t, at("2026-08-23", "04:00"), window.Next(at("2026-08-22", "09:30")))
	require.Equal(t, at("2026-08-22", "04:00"), window.Next(at("2026-08-22", "03:59")))
	// On the boundary the next window is tomorrow's, never "now".
	require.Equal(t, at("2026-08-23", "04:00"), window.Next(at("2026-08-22", "04:00")))
}

func TestTheWindowFollowsTheDayItIsAskedAbout(t *testing.T) {
	// Across a month end.
	window := SyncWindow{Hour: 23, Minute: 30}
	require.Equal(t, at("2026-08-31", "23:30"), window.MostRecent(at("2026-09-01", "00:15")))
	require.Equal(t, at("2026-09-01", "23:30"), window.Next(at("2026-09-01", "00:15")))
}

func TestAWindowKeepsTheClockItWasGiven(t *testing.T) {
	// The window is read in the server's own zone, never moved to UTC.
	zone := time.FixedZone("Fixed", -5*60*60)
	now := time.Date(2026, 8, 22, 9, 0, 0, 0, zone)
	got := SyncWindow{Hour: 4, Minute: 0}.MostRecent(now)

	require.Equal(t, zone.String(), got.Location().String())
	require.Equal(t, 4, got.Hour())
}

// --- Panic recovery ----------------------------------------------------------

func TestAPanickingJobDoesNotStopTheTick(t *testing.T) {
	scheduler := &Scheduler{Log: quietLogger()}

	runs := 0
	scheduler.guard(t.Context(), "explodes", func() { panic("the provider returned nonsense") })
	scheduler.guard(t.Context(), "works", func() { runs++ })

	require.Equal(t, 1, runs, "the job after the panicking one did not run")
}

func TestAPanickingPassDoesNotStopTheScheduler(t *testing.T) {
	// With no store every job panics; Run must survive and keep ticking until
	// the context is cancelled.
	ctx, cancel := context.WithCancel(t.Context())
	ticks := 0
	scheduler := &Scheduler{
		Every: time.Millisecond,
		Log:   quietLogger(),
		Now: func() time.Time {
			ticks++
			if ticks >= 6 {
				cancel()
			}
			return time.Now()
		},
	}

	stopped := make(chan struct{})
	go func() {
		scheduler.Run(ctx)
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the scheduler never returned: a panic escaped the guard")
	}
	require.GreaterOrEqual(t, ticks, 6, "the scheduler stopped ticking at the first panic")
}

// TestBanksOffClaimsNothing: with SimpleFIN turned off in Server admin a pass
// leaves every connection's turn alone. With no store, a claim would panic
// inside its guard and say so in the log.
func TestBanksOffClaimsNothing(t *testing.T) {
	var logged bytes.Buffer
	on := false
	scheduler := &Scheduler{
		Sync:    &Sync{},
		BanksOn: func() bool { return on },
		Log:     slog.New(slog.NewTextHandler(&logged, nil)),
	}

	ran, finished := scheduler.syncDue(t.Context(), time.Now())
	require.Equal(t, 0, ran)
	require.True(t, finished)
	require.Empty(t, logged.String(), "a claim was attempted with banks off")

	on = true
	scheduler.syncDue(t.Context(), time.Now())
	require.Contains(t, logged.String(), "job=claim", "turning banks on did not reach the claim")
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- Balance history ---------------------------------------------------------

func TestTheDailyPassRebuildsTheTrailingWeek(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "History Checking")
	today := domain.NewDate(2026, time.September, 10)
	// Held since August, so the whole trailing week is inside its history.
	account.OpeningBalanceOn = domain.NewDate(2026, time.August, 1)
	require.NoError(t, db(t).UpdateAccount(t.Context(), space, account))
	// Three days back: only the trailing rebuild writes that day's row.
	row := &store.Transaction{
		AccountID: account.ID, Date: today.AddDays(-3),
		Amount: domain.MustFromString("-75"), Currency: "USD",
		StatementName: "X", Payee: "X", Source: domain.SourceManual,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, row))

	scheduler := &Scheduler{
		Store: db(t),
		Log:   quietLogger(),
		Now:   func() time.Time { return today.Time().Add(2 * time.Minute) },
	}
	scheduler.snapshotBalances(t.Context())

	history, err := db(t).ListBalanceHistory(t.Context(), space, today)
	require.NoError(t, err)
	byDay := map[string]string{}
	for _, point := range history {
		if point.AccountID == domain.ID(account.ID.String()) {
			byDay[point.On.String()] = point.Balance.String()
		}
	}
	for day := today.AddDays(-rebuildTrailingDays); !day.After(today); day = day.AddDays(1) {
		require.Contains(t, byDay, day.String(), "one row per day, the trailing week and today")
	}
	// The history starts August 1 and the pass filled it back to there.
	require.Len(t, byDay, 41)
	require.Equal(t, "0.00", byDay["2026-08-01"])
	require.NotContains(t, byDay, "2026-07-31")
	require.Equal(t, "0.00", byDay[today.AddDays(-4).String()])
	require.Equal(t, "-75.00", byDay[today.AddDays(-3).String()])
	require.Equal(t, "-75.00", byDay[today.String()])
}

func TestTheFirstPassOfTheDayBackfillsReceipts(t *testing.T) {
	space := newSpace(t)
	merchantAccount := &store.MerchantAccount{Merchant: domain.MerchantCostco, Label: "Warehouse"}
	require.NoError(t, db(t).CreateMerchantAccount(t.Context(), space, merchantAccount))
	order := &store.MerchantOrder{
		MerchantAccountID: merchantAccount.ID, Merchant: domain.MerchantCostco,
		OrderNumber: "21100123456789012345", OrderedOn: domain.NewDate(2026, time.September, 4),
		Total: domain.MustFromString("60.00"), Currency: "USD", Source: "pull",
	}
	_, err := db(t).UpsertMerchantOrder(t.Context(), space, order)
	require.NoError(t, err)
	invoice := &store.Document{
		ContentSHA256: "7c0ffee0000000000000000000000000000000000000000000000000000000a1",
		ContentType:   "application/pdf", SizeBytes: 2048, Filename: "costco-receipt.pdf",
		StorageKey: space.String() + "/costco-receipt.pdf", Source: store.DocumentSourceMerchantPull,
	}
	require.NoError(t, db(t).CreateDocument(t.Context(), space, invoice))
	require.NoError(t, db(t).LinkDocument(t.Context(), space, store.DocumentLink{
		DocumentID: invoice.ID, Kind: store.DocumentLinkMerchantOrder, TargetID: order.ID,
		Role: store.DocumentRoleInvoice,
	}))
	card := newAccount(t, space, "Rewards Card")
	row := &store.Transaction{
		AccountID: card.ID, Date: domain.NewDate(2026, time.September, 4),
		Amount: domain.MustFromString("-60.00"), Currency: "USD",
		StatementName: "COSTCO WHSE #0000", Payee: "Costco", Source: domain.SourceManual,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, row))
	require.NoError(t, db(t).SetMerchantMatch(t.Context(), space, &store.MerchantMatch{
		TransactionID: row.ID, OrderID: order.ID, Amount: domain.MustFromString("60.00"),
		Basis: "total", Confidence: 1,
	}))
	// A match with no receipt linked to it yet.
	_, err = db(t).Pool().Exec(t.Context(),
		`DELETE FROM document_links WHERE space_id = $1 AND kind = 'receipt'`, space.UUID())
	require.NoError(t, err)

	today := domain.NewDate(2026, time.September, 10)
	scheduler := &Scheduler{
		Store: db(t),
		Log:   quietLogger(),
		Now:   func() time.Time { return today.Time().Add(time.Minute) },
	}
	scheduler.reconcileReceipts(t.Context())

	receipts, err := db(t).ListDocumentsByLink(t.Context(), space, store.DocumentLinkReceipt, row.ID)
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	require.Equal(t, invoice.ID, receipts[0].ID)
	require.Equal(t, today.String(), scheduler.receiptsOn, "and not again today")
}
