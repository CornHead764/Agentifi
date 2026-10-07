package service

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The daily sync. SimpleFIN's Bridge answers with whatever it last saw, so
// the schedule is a daily window rather than an interval: a connection is due
// when its last attempt predates the current window, and a server that was
// asleep through the window catches up once when it wakes. This file decides
// only when; what a sync does is Sync's.

// SyncWindow is the time of day the daily run belongs to. Config validates the
// string; this decides what it means.
type SyncWindow struct {
	Hour   int
	Minute int
}

func (w SyncWindow) On(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), w.Hour, w.Minute, 0, 0, day.Location())
}

// MostRecent is the latest occurrence at or before now, so a server that
// starts after the window still sees an unsynced connection as due.
func (w SyncWindow) MostRecent(now time.Time) time.Time {
	today := w.On(now)
	if today.After(now) {
		return today.AddDate(0, 0, -1)
	}
	return today
}

// Next is the first occurrence strictly after now, for the UI.
func (w SyncWindow) Next(now time.Time) time.Time {
	return w.MostRecent(now).AddDate(0, 0, 1)
}

func (w SyncWindow) String() string { return fmt.Sprintf("%02d:%02d", w.Hour, w.Minute) }

// adHocFilterAge is how long an ad_hoc filter outlives its last write. A
// page caches the id it was given, so this is set well past how long a page
// usually stays open.
const adHocFilterAge = 24 * time.Hour

type Scheduler struct {
	Store *store.Store
	Sync  *Sync
	// BanksOn is asked before each bank pass, so turning SimpleFIN on or off
	// from Server admin needs no restart. Nil is always on.
	BanksOn func() bool
	// Valuation re-prices the physical assets. Nil turns that off.
	Valuation *Valuation
	// Currency fetches the day's exchange rates and converts foreign rows that
	// arrived without one. Nil turns that off.
	Currency *Currency
	// Merchants pulls each connected merchant account's orders once a day, in
	// the same window as the banks. Nil, or one with no agent, does nothing.
	Merchants *Merchants
	// Bills pulls each connected provider's statements in the same window, and
	// keeps rarely pulled sessions alive. Nil, or no bridge, does nothing.
	Bills *Bills
	// Mail reads the household's watched mailbox. Nil, or no mailbox, does
	// nothing.
	Mail *Mailbox
	// Documents collects unlinked files. Nil leaves orphaned blobs on disk.
	Documents *Documents
	// Forecasts re-estimates every account's cash flow once a day, after the
	// connections have synced. Nil turns it off.
	Forecasts *AccountForecasts

	At SyncWindow
	// Every is how often to look for work: the window's resolution, not the
	// sync interval.
	Every time.Duration
	// Between pauses between connections so they are not all opened on the
	// Bridge at once.
	Between time.Duration

	Log *slog.Logger
	Now func() time.Time

	// revaluedOn is the calendar day the asset pass last ran; this and the
	// fields below gate each daily job to once a day.
	revaluedOn    string
	snapshottedOn string
	ratesOn       string
	forecastedOn  string
	receiptsOn    string
}

func (s *Scheduler) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (s *Scheduler) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

// Run works until the context is cancelled. The first pass is immediate, so a
// server that missed the window does not wait a tick.
func (s *Scheduler) Run(ctx context.Context) {
	every := s.Every
	if every <= 0 {
		every = 5 * time.Minute
	}
	s.log().Info("sync scheduler started", "at", s.At.String(), "checking_every", every)

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		// The outer net: jobs inside RunDue are already guarded, but a panic in
		// this goroutine must never take the HTTP server down.
		s.guard(ctx, "tick", func() { s.RunDue(ctx) })
		select {
		case <-ctx.Done():
			s.log().Info("sync scheduler stopped")
			return
		case <-ticker.C:
		}
	}
}

// guard runs one job and logs a panic with its stack. Per job, so one broken
// provider does not skip the rest of the tick. Cancellation is not caught.
func (s *Scheduler) guard(ctx context.Context, job string, run func()) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		s.log().Error("scheduler job panicked",
			"job", job,
			"panic", fmt.Sprint(recovered),
			"shutting_down", ctx.Err() != nil,
			"stack", string(debug.Stack()))
	}()
	run()
}

// RunDue claims and syncs everything due right now, and reports how many ran.
// Exported so tests share the scheduler's one definition of "due".
func (s *Scheduler) RunDue(ctx context.Context) int {
	now := s.now()
	// Each job under its own guard. The asset pass runs before the connections
	// so a shutdown mid-pass has still re-priced the assets.
	s.guard(ctx, "valuation", func() { s.revalueAssets(ctx) })

	// Today's row of the materialized balance history.
	s.guard(ctx, "balance snapshot", func() { s.snapshotBalances(ctx) })

	// Before the connections: foreign rows need converting even with no bank
	// connection.
	s.guard(ctx, "exchange rates", func() { s.refreshRates(ctx) })

	// Statements and invoices filed on the rows that paid them. The hooks do
	// this as it happens; the first tick after start is the backfill, and the
	// daily pass catches a write no hook saw.
	s.guard(ctx, "receipts", func() { s.reconcileReceipts(ctx) })

	// Documents nothing has linked for a week.
	s.guard(ctx, "document purge", func() {
		if s.Documents == nil {
			return
		}
		if purged, err := s.Documents.Purge(ctx); err != nil {
			s.log().Error("could not purge unlinked documents", "error", err)
		} else if purged > 0 {
			s.log().Info("purged unlinked documents", "count", purged)
		}
	})

	// Expired revocation marks are dead tokens.
	s.guard(ctx, "revocation prune", func() {
		if pruned, err := s.Store.PruneRevocations(ctx); err != nil {
			s.log().Error("could not prune token revocations", "error", err)
		} else if pruned > 0 {
			s.log().Info("pruned token revocations", "count", pruned)
		}
	})

	// The register's and reports' throwaway searches, once a day old.
	s.guard(ctx, "ad hoc filter prune", func() {
		if pruned, err := s.Store.PruneAdHocFilters(ctx, now.Add(-adHocFilterAge)); err != nil {
			s.log().Error("could not prune ad hoc filters", "error", err)
		} else if pruned > 0 {
			s.log().Info("pruned ad hoc filters", "count", pruned)
		}
	})

	// Merchant accounts, in the banks' window, so card charges have posted.
	s.guard(ctx, "amazon", func() {
		if s.Merchants != nil {
			s.Merchants.PullDue(ctx, s.At.MostRecent(now))
		}
	})

	// Bill providers, in the same window. Expire unanswered challenges first,
	// or the pull writes a second row for a question nobody can still answer.
	s.guard(ctx, "bills", func() {
		if s.Bills == nil {
			return
		}
		if expired, err := s.Bills.ExpireChallenges(ctx); err != nil {
			s.log().Error("could not expire bill challenges", "error", err)
		} else if expired > 0 {
			s.log().Info("expired bill challenges", "count", expired)
		}
		s.Bills.PullDue(ctx, s.At.MostRecent(now))
		s.Bills.KeepaliveDue(ctx, now)
	})

	// The mailbox runs on its own interval, not the daily window: codes are
	// measured in minutes.
	s.guard(ctx, "mailbox", func() {
		if s.Mail == nil {
			return
		}
		every := s.Mail.PollEvery
		if every <= 0 {
			every = 30 * time.Minute
		}
		s.Mail.PollDue(ctx, now.Add(-every))
	})

	ran, finished := s.syncDue(ctx, now)
	if !finished {
		return ran
	}

	// Last, so the estimates read what the connections just brought in.
	s.guard(ctx, "account forecasts", func() { s.refreshForecasts(ctx, now) })
	return ran
}

// syncDue claims and syncs the bank connections that are due. finished is false
// when the claim failed or a shutdown cut the pass short. With banks off
// nothing is claimed, so a connection keeps its turn for when they are on.
func (s *Scheduler) syncDue(ctx context.Context, now time.Time) (ran int, finished bool) {
	if s.Sync == nil || (s.BanksOn != nil && !s.BanksOn()) {
		return 0, true
	}
	due, err := s.claimDue(ctx, now)
	if err != nil {
		// Log and let the next tick try; returning would stop the scheduler for
		// the life of the server.
		s.log().Error("sync scheduler could not claim work", "error", err)
		return 0, false
	}

	for _, one := range due {
		if ctx.Err() != nil {
			// Shutting down: the rest keep the claim's stamp and wait for tomorrow.
			return ran, false
		}
		// Guarded per connection, so one bad bank does not cost the others.
		s.guard(ctx, "sync", func() { s.syncOne(ctx, one) })
		ran++
		if s.Between > 0 && ran < len(due) {
			select {
			case <-ctx.Done():
				return ran, false
			case <-time.After(s.Between):
			}
		}
	}
	return ran, true
}

// refreshForecasts re-estimates every account once a calendar day, not
// before the sync window opens (a pass at 00:05 would average yesterday's
// ledger all day). Marked done only when every space succeeded.
func (s *Scheduler) refreshForecasts(ctx context.Context, now time.Time) {
	if s.Forecasts == nil || now.Before(s.At.On(now)) {
		return
	}
	today := domain.DateOf(now)
	if s.forecastedOn == today.String() {
		return
	}
	spaces, err := s.Store.ListSpaces(ctx)
	if err != nil {
		s.log().Error("account forecasts could not list spaces", "error", err)
		return
	}
	failed := false
	for _, space := range spaces {
		if ctx.Err() != nil {
			return
		}
		made, err := s.Forecasts.RefreshSpace(ctx, space.ID, today)
		if err != nil {
			s.log().Error("account forecasts failed", "space", space.Name, "error", err)
			failed = true
			continue
		}
		s.log().Info("account forecasts refreshed", "space", space.Name, "accounts", made)
	}
	if !failed && ctx.Err() == nil {
		s.forecastedOn = today.String()
	}
}

// reconcileReceipts pairs bill payments with the bank rows synced since
// (store.MatchBillPayments) and runs store.ReconcileSpaceReceipts, over every
// space once a calendar day. Marked done only when every space succeeded.
func (s *Scheduler) reconcileReceipts(ctx context.Context) {
	today := domain.DateOf(s.now())
	if s.receiptsOn == today.String() {
		return
	}
	spaces, err := s.Store.ListSpaces(ctx)
	if err != nil {
		s.log().Error("receipts could not list spaces", "error", err)
		return
	}
	failed := false
	for _, space := range spaces {
		if ctx.Err() != nil {
			return
		}
		paired, err := s.Store.MatchBillPayments(ctx, space.ID)
		if err != nil {
			s.log().Error("bill payments pairing failed", "space", space.Name, "error", err)
			failed = true
			continue
		}
		if paired > 0 {
			s.log().Info("bill payments paired", "space", space.Name, "paired", paired)
		}
		added, removed, err := s.Store.ReconcileSpaceReceipts(ctx, space.ID)
		if err != nil {
			s.log().Error("receipts reconcile failed", "space", space.Name, "error", err)
			failed = true
			continue
		}
		if added > 0 || removed > 0 {
			s.log().Info("receipts reconciled", "space", space.Name, "added", added, "removed", removed)
		}
	}
	if !failed && ctx.Err() == nil {
		s.receiptsOn = today.String()
	}
}

// refreshRates fetches the day's rates and converts what is waiting on them,
// once a calendar day. A fetched rate never applied leaves a foreign row summed
// at face value; a provider that is down still lets stored rates convert.
func (s *Scheduler) refreshRates(ctx context.Context) {
	if s.Currency == nil {
		return
	}
	today := domain.DateOf(s.now())
	if s.ratesOn == today.String() {
		return
	}

	spaces, err := s.Store.ListSpaces(ctx)
	if err != nil {
		s.log().Error("exchange rates could not list spaces", "error", err)
		return
	}
	failed := false
	for _, space := range spaces {
		if ctx.Err() != nil {
			return
		}
		if err := s.Currency.FetchRates(ctx, space.ID, today); err != nil {
			// Not fatal: stored rates still convert below.
			s.log().Error("exchange rate fetch failed", "space", space.Name, "error", err)
			failed = true
		}
		result, err := s.Currency.StampSpace(ctx, space.ID)
		if err != nil {
			s.log().Error("currency conversion failed", "space", space.Name, "error", err)
			failed = true
			continue
		}
		if result.Stamped > 0 || result.Unrated > 0 {
			s.log().Info("converted foreign transactions", "space", space.Name,
				"stamped", result.Stamped, "unrated", result.Unrated)
		}
	}
	// Marked done only on a clean pass, so a transient error retries.
	if !failed && ctx.Err() == nil {
		s.ratesOn = today.String()
	}
}

// revalueAssets re-prices the physical assets once a calendar day, apart from
// the connection pass because an asset has no connection. The gate is in
// memory; RevalueDue re-checks staleness per asset, so a second pass is cheap.
func (s *Scheduler) revalueAssets(ctx context.Context) {
	if s.Valuation == nil {
		return
	}
	today := s.now().Format(time.DateOnly)
	if s.revaluedOn == today {
		return
	}
	_, err := s.Valuation.RevalueDue(ctx)
	// Marked done only when the pass finished, so a transient error retries.
	if err == nil && ctx.Err() == nil {
		s.revaluedOn = today
	}
}

// claimDue is guarded so a panic in the claim (a nil pool during shutdown) is
// an empty pass rather than a dead scheduler.
func (s *Scheduler) claimDue(ctx context.Context, now time.Time) ([]store.DueConnection, error) {
	var due []store.DueConnection
	var err error
	s.guard(ctx, "claim", func() {
		due, err = s.Store.ClaimConnectionsDueForSync(ctx, s.At.MostRecent(now), now)
	})
	return due, err
}

func (s *Scheduler) syncOne(ctx context.Context, one store.DueConnection) {
	report, err := s.Sync.SyncConnection(ctx, one.SpaceID, one.ID)
	if err != nil {
		s.log().Error("scheduled sync failed",
			"connection", one.ID, "name", one.Name, "error", err)
		return
	}
	s.log().Info("scheduled sync",
		"connection", one.ID,
		"name", one.Name,
		"status", string(report.Status),
		"accounts", report.AccountsLinked,
		"imported", report.TransactionsImported,
		"updated", report.TransactionsUpdated,
		"bank_warnings", len(report.BankWarnings),
		"balances_held", report.BalancesHeld,
	)
}

// rebuildTrailingDays is how far back each pass re-derives the balance
// history: long enough for pendings to settle and backfilled rows to land,
// short enough that the O(days × ledger) rebuild stays cheap.
const rebuildTrailingDays = 7

// snapshotBalances writes today's balance-history row and re-derives the
// trailing week, once a calendar day, since a late posting changes the close
// of days already written. Both use domain.BalanceAsOf, so a rerun rewrites
// the same values. Then accounts whose history start moved are trimmed and
// filled (store.ReconcileHistoryStarts) and older derived rows are re-walked
// (store.RederiveBalanceHistory), because edits reach further back than a
// week.
func (s *Scheduler) snapshotBalances(ctx context.Context) {
	today := domain.DateOf(s.now())
	if s.snapshottedOn == today.String() {
		return
	}

	spaces, err := s.Store.ListSpaces(ctx)
	if err != nil {
		s.log().Error("balance snapshots could not list spaces", "error", err)
		return
	}
	failed := false
	for _, space := range spaces {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.Store.SnapshotAccounts(ctx, space.ID, today); err != nil {
			s.log().Error("balance snapshot failed", "space", space.Name, "error", err)
			failed = true
			continue
		}
		from := today.AddDays(-rebuildTrailingDays)
		if _, err := s.Store.RebuildBalanceHistory(ctx, space.ID, from, today.AddDays(-1)); err != nil {
			s.log().Error("balance history rebuild failed", "space", space.Name, "error", err)
			failed = true
		}
		if _, err := s.Store.ReconcileHistoryStarts(ctx, space.ID, today); err != nil {
			s.log().Error("balance history start rebuild failed", "space", space.Name, "error", err)
			failed = true
		}
		if _, err := s.Store.RederiveBalanceHistory(ctx, space.ID); err != nil {
			s.log().Error("balance history re-derive failed", "space", space.Name, "error", err)
			failed = true
		}
	}
	// Marked done only when every space snapshotted; the upsert makes a retry
	// free.
	if !failed && ctx.Err() == nil {
		s.snapshottedOn = today.String()
	}
}
