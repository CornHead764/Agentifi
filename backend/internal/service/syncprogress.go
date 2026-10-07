package service

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Where each connection's sync stands, while it runs and after it ends. Held in
// this process, keyed by connection, because a service is built per request and
// the page asking is rarely the one that started the run. A restart forgets
// it; the connection row keeps what lasts (status, warnings, last success).

// SyncState is how a run stands.
type SyncState string

const (
	SyncRunning   SyncState = "running"
	SyncSucceeded SyncState = "succeeded"
	// SyncFailed is a run that stopped on a fault, or one the Bridge refused or
	// throttled. What it imported before stopping stands.
	SyncFailed SyncState = "failed"
	// SyncSkipped never asked the Bridge for anything: the connection awaits
	// matching, is parked by a throttle, or another server is syncing it.
	SyncSkipped SyncState = "skipped"
)

// SyncPhase is what a running sync is doing.
type SyncPhase string

const (
	// SyncFetching is asking the Bridge which accounts the connection reaches.
	SyncFetching SyncPhase = "fetching"
	// SyncImporting is reading one account's transactions; SyncProgress names it.
	SyncImporting SyncPhase = "importing"
	// SyncSettling is balances, rules, transfer pairing, recurring matches and
	// alerts over what arrived.
	SyncSettling SyncPhase = "settling"
)

// SyncProgress is one run as a page reads it.
type SyncProgress struct {
	State SyncState
	// Phase is empty once the run has ended.
	Phase SyncPhase
	// Account is the position, from 1, of the account being read among
	// Accounts, and AccountName its name.
	Account     int
	Accounts    int
	AccountName string

	TransactionsImported int
	TransactionsUpdated  int
	AccountsCreated      int
	// Warnings counts the banks and accounts the run could not read cleanly,
	// which the connection lists; BalancesHeld the balances it declined.
	Warnings     int
	BalancesHeld int
	// Message says why a run failed or was skipped.
	Message string

	StartedAt  time.Time
	FinishedAt *time.Time
}

type syncRun struct {
	mu       sync.Mutex
	progress SyncProgress
}

func (r *syncRun) update(change func(*SyncProgress)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	change(&r.progress)
}

func (r *syncRun) snapshot() SyncProgress {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.progress
}

// end records how the run finished: a fault, a skip, a refusal or throttle
// from the Bridge, or a success.
func (r *syncRun) end(report SyncReport, cause error, at time.Time) {
	r.update(func(p *SyncProgress) {
		p.Phase = ""
		p.FinishedAt = &at
		switch {
		case cause != nil:
			p.State = SyncFailed
			p.Message = cause.Error()
			return
		case report.Skipped:
			p.State = SyncSkipped
			p.Message = report.StatusDetail
			return
		case report.Status != store.ConnectionActive:
			p.State = SyncFailed
			p.Message = report.StatusDetail
		default:
			p.State = SyncSucceeded
			p.Message = ""
		}
		p.Accounts = report.AccountsLinked
		p.TransactionsImported = report.TransactionsImported
		p.TransactionsUpdated = report.TransactionsUpdated
		p.AccountsCreated = report.AccountsCreated
		p.Warnings = len(report.BankWarnings)
		p.BalancesHeld = report.BalancesHeld
	})
}

type syncRuns struct {
	mu           sync.Mutex
	byConnection map[uuid.UUID]*syncRun
}

var connectionSyncs = syncRuns{byConnection: map[uuid.UUID]*syncRun{}}

// begin registers a run of the connection. When one is already running here,
// that one comes back and started is false.
func (r *syncRuns) begin(connectionID uuid.UUID, now time.Time) (run *syncRun, started bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.byConnection[connectionID]; ok && current.snapshot().State == SyncRunning {
		return current, false
	}
	run = &syncRun{progress: SyncProgress{State: SyncRunning, Phase: SyncFetching, StartedAt: now}}
	r.byConnection[connectionID] = run
	return run, true
}

// skipped records a run that never started, unless one is running here, which
// comes back instead.
func (r *syncRuns) skipped(connectionID uuid.UUID, report SyncReport, now time.Time) *syncRun {
	run, started := r.begin(connectionID, now)
	if started {
		run.end(report, nil, now)
	}
	return run
}

// SyncProgressOf is the connection's running sync, or the last one this
// process ran.
func SyncProgressOf(connectionID uuid.UUID) (SyncProgress, bool) {
	connectionSyncs.mu.Lock()
	run, ok := connectionSyncs.byConnection[connectionID]
	connectionSyncs.mu.Unlock()
	if !ok {
		return SyncProgress{}, false
	}
	return run.snapshot(), true
}
