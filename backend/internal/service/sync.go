package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// SimpleFIN sync, from a pasted setup token to rows in the register.
//
// Invariants: a second sync imports nothing new (rows match on the provider id,
// then on day, amount and wording; deleted rows count as present); the sync floor
// is enforced in front of the insert; a routine sync reads about a month; one
// bank's failure is recorded against the connection while the other accounts
// keep syncing, and only a refused Access URL needs a fresh setup token; and new
// rows go through Ingest.AfterIngest, the same path the file importers use.

const (
	// syncResumeOverlapDays is how far behind its newest row a sync reaches. Banks
	// post late, and a posted charge keeps changing (a pending settles at another
	// figure, a tip is added, a hold is withdrawn); those corrections arrive only
	// while their day is still being asked about, so the overlap covers a statement.
	syncResumeOverlapDays = 30

	// syncRateLimitBackoff parks a throttled connection. The Bridge caps reads per
	// Access URL over a long window, so retrying in a minute earns another 429.
	syncRateLimitBackoff = 6 * time.Hour
)

// Aggregator is provider.BankProvider plus AccountsWithWarnings, which is where
// a dead Access URL is told apart from a single bank needing reauthorization.
type Aggregator interface {
	provider.BankProvider

	AccountsWithWarnings(ctx context.Context, creds provider.Credentials) (
		[]provider.Account, []provider.BankWarning, error)
}

type Sync struct {
	base
	provider Aggregator
	// Alerts, when set, sweeps the alert catalog once the rows have landed.
	Alerts *Alerts
	// Ingest is what the arriving rows go through once they are written.
	Ingest Ingest
	// Now is nil for the real clock.
	Now func() time.Time
}

// NewSync needs a store that carries the credential cipher (store.WithCipher);
// without it the credential queries refuse rather than store an Access URL in
// the clear.
func NewSync(st *store.Store, bank Aggregator) *Sync {
	return &Sync{base: newBase(st), provider: bank}
}

func (s *Sync) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now().UTC()
}

// Reclaim replaces a refused Access URL with one bought by a fresh setup token,
// keeping the connection. Claiming again would make a second connection whose
// accounts match nothing, since (connection_id, external_id) is the join.
func (s *Sync) Reclaim(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, setupToken string,
) (store.Connection, error) {
	conn, err := s.store.GetConnection(ctx, spaceID, connectionID)
	if err != nil {
		return store.Connection{}, err
	}
	claimed, err := s.provider.Connect(ctx, strings.TrimSpace(setupToken))
	if err != nil {
		return store.Connection{}, err
	}
	if err := s.store.SetConnectionAccessURL(ctx, spaceID, conn.ID, claimed.Credentials.AccessURL); err != nil {
		return store.Connection{}, err
	}

	now := s.now()
	conn.Status = store.ConnectionActive
	conn.StatusDetail = ""
	conn.RetryNotBefore = nil
	conn.SyncErrors = bankWarnings(claimed.Warnings, now)
	conn.LastSyncAt = &now
	conn.LastSuccessfulSyncAt = &now
	if err := s.store.UpdateConnection(ctx, spaceID, &conn); err != nil {
		return store.Connection{}, err
	}
	linked, err := s.linkAccounts(ctx, spaceID, conn.ID, claimed.Accounts)
	if err != nil {
		return store.Connection{}, err
	}
	for i := range linked {
		if _, err := s.settleBalance(ctx, spaceID, &linked[i], nil, now); err != nil {
			return store.Connection{}, err
		}
	}
	return conn, nil
}

// SyncReport is what one run did, in the terms the settings page shows.
type SyncReport struct {
	ConnectionID uuid.UUID
	Status       store.ConnectionStatus
	StatusDetail string
	// AccountsSeen is what the Bridge reached; AccountsLinked is how many of
	// those have a local account.
	AccountsSeen         int
	AccountsLinked       int
	AccountsCreated      int
	TransactionsImported int
	TransactionsUpdated  int
	// TransactionsRetired counts synced pending rows the feed stopped reporting:
	// voided authorizations, and pendings that reposted under a new external id.
	TransactionsRetired int
	// BankWarnings are institutions inside a healthy connection that need
	// attention. Never a reason to call the run a failure.
	BankWarnings []store.BankSyncError
	// BalancesHeld counts accounts whose reported balance the run declined to
	// apply. The account row carries the hold and its reason; it is not also a
	// bank warning, so a held balance is reported once.
	BalancesHeld   int
	RetryNotBefore *time.Time
	// AlertSweepFailed records that the rows landed but the alert sweep did not.
	// The sync still succeeded, and the next sweep picks up what this one missed.
	AlertSweepFailed bool
	// CurrencyStampFailed is the same for the conversion pass.
	CurrencyStampFailed bool
	// Skipped is a run that never reached the Bridge because the connection is
	// parked: neither a failure nor a success.
	Skipped bool
}

// Claim exchanges a single-use setup token for a stored connection. It imports
// nothing: DiscoverAccounts feeds the match screen, and the first history pull
// is an ordinary, resumable sync.
func (s *Sync) Claim(
	ctx context.Context, spaceID store.SpaceID, setupToken, name string,
) (store.Connection, error) {
	claimed, err := s.provider.Connect(ctx, strings.TrimSpace(setupToken))
	if err != nil {
		return store.Connection{}, err
	}

	now := s.now()
	conn := store.Connection{
		Name: textutil.FirstNonBlank(name, claimed.InstitutionName, "SimpleFIN"),
		// Not yet linked, and no account is created: an imported household already
		// holds these accounts with their history, and only the user can say which
		// remote account is which local one.
		Status:     store.ConnectionPendingLink,
		SyncErrors: bankWarnings(claimed.Warnings, now),
		LastSyncAt: &now,
		// LastSuccessfulSyncAt stays nil: nothing has synced.
	}
	if err := s.store.CreateConnection(ctx, spaceID, &conn, claimed.Credentials.AccessURL); err != nil {
		return store.Connection{}, err
	}
	return conn, nil
}

// DiscoverAccounts reads what the connection reaches and creates nothing; no
// row is written until the user has matched remote accounts to local ones.
func (s *Sync) DiscoverAccounts(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) ([]provider.Account, error) {
	accessURL, err := s.store.ConnectionAccessURL(ctx, spaceID, connectionID)
	if err != nil {
		return nil, err
	}
	accounts, _, err := s.provider.AccountsWithWarnings(
		ctx, provider.Credentials{AccessURL: accessURL})
	return accounts, err
}

// LinkAction is the match screen's answer for one account at the bank.
type LinkAction string

const (
	// LinkToAccount feeds an account the household already keeps.
	LinkToAccount LinkAction = "link"
	// LinkAsNew makes a new account for it.
	LinkAsNew LinkAction = "create"
	// LinkIgnore neither creates nor feeds anything for it.
	LinkIgnore LinkAction = "ignore"
)

// LinkChoice is one account at the bank and what becomes of it. AccountID is
// read only with LinkToAccount.
type LinkChoice struct {
	ExternalID string
	Action     LinkAction
	AccountID  uuid.UUID
}

// LinkRefused is a set of choices that cannot be applied, in words for the
// person making them.
type LinkRefused struct{ Reason string }

func (e LinkRefused) Error() string { return e.Reason }

// FinishLinking applies the match screen's choices in one transaction and
// releases the connection to sync: each paired account is adopted with its
// floor, each refused one recorded, and every other account the Bridge reaches
// is created, with its reported balance. An account at the bank with no choice
// keeps the pairing it has, or becomes a new account. No transaction is
// imported here; the sync that follows does that.
//
// The Bridge is read once first, for what a new account is named and holds.
func (s *Sync) FinishLinking(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, choices []LinkChoice,
) (store.Connection, error) {
	conn, err := s.store.GetConnection(ctx, spaceID, connectionID)
	if err != nil {
		return store.Connection{}, err
	}
	remote, err := s.DiscoverAccounts(ctx, spaceID, connectionID)
	if err != nil {
		return store.Connection{}, err
	}
	reached := make(map[string]provider.Account, len(remote))
	for _, one := range remote {
		reached[one.ExternalID] = one
	}
	fed := map[uuid.UUID]bool{}
	for _, choice := range choices {
		if _, ok := reached[choice.ExternalID]; !ok {
			return store.Connection{}, LinkRefused{
				"An account on the match screen is not shared at the Bridge any more. Reload it and finish again."}
		}
		switch choice.Action {
		case LinkToAccount:
			if fed[choice.AccountID] {
				return store.Connection{}, LinkRefused{"One of your accounts was chosen for two accounts at the bank."}
			}
			fed[choice.AccountID] = true
		case LinkAsNew, LinkIgnore:
		default:
			return store.Connection{}, LinkRefused{fmt.Sprintf("%q is not a choice the match screen offers.", choice.Action)}
		}
	}

	err = s.inTx(ctx, func(tx *store.Store) error {
		within := &Sync{base: newBase(tx), provider: s.provider, Now: s.Now}
		for _, choice := range choices {
			if err := within.applyChoice(ctx, spaceID, conn.ID, choice, reached[choice.ExternalID]); err != nil {
				return err
			}
		}
		if _, err := within.linkAccounts(ctx, spaceID, conn.ID, remote); err != nil {
			return err
		}
		if conn.Status == store.ConnectionPendingLink {
			conn.Status = store.ConnectionActive
			conn.StatusDetail = ""
			return tx.UpdateConnection(ctx, spaceID, &conn)
		}
		return nil
	})
	if err != nil {
		return store.Connection{}, err
	}
	return conn, nil
}

// applyChoice prepares one account at the bank for linkAccounts: the account it
// is to feed carries its id and floor, and nothing else does. Whatever fed it
// before is detached and kept, with its rows, as a manual account.
func (s *Sync) applyChoice(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
	choice LinkChoice, remote provider.Account,
) error {
	if choice.Action == LinkIgnore {
		_, err := s.ignoreAccount(ctx, spaceID, connectionID, store.IgnoredRemoteAccount{
			ExternalID:   remote.ExternalID,
			Name:         remote.Name,
			Institution:  remote.InstitutionName,
			MaskedNumber: remote.MaskedNumber,
		})
		return err
	}
	var target store.Account
	if choice.Action == LinkToAccount {
		account, err := s.store.GetAccount(ctx, spaceID, choice.AccountID)
		if err != nil {
			return err
		}
		if account.IsDeleted {
			return LinkRefused{fmt.Sprintf("%s has been deleted. Choose another account.", account.Name)}
		}
		if account.ConnectionID != uuid.Nil && account.ConnectionID != connectionID {
			return LinkRefused{fmt.Sprintf("%s is fed by another connection. Choose another account.", account.Name)}
		}
		if account.ConnectionID == connectionID && account.ExternalID == remote.ExternalID {
			// Already fed by it. Linking again would raise the floor past rows
			// the bank has yet to post.
			return nil
		}
		target = account
	}

	current, found, err := s.findLocalAccount(ctx, spaceID, connectionID, remote)
	if err != nil {
		return err
	}
	if found && current.ID != target.ID {
		if err := s.store.UnlinkAccount(ctx, spaceID, current.ID); err != nil {
			return err
		}
	}
	if choice.Action == LinkAsNew {
		return nil
	}
	if target.ConnectionID != uuid.Nil {
		// It fed another account at the bank; that one's choice decides its feed.
		if err := s.store.UnlinkAccount(ctx, spaceID, target.ID); err != nil {
			return err
		}
	}
	_, err = s.linkAccount(ctx, spaceID, target.ID, remote.ExternalID)
	return err
}

// linkAccount points an existing local account at a provider account. It stamps
// the id and the floor only; linkAccounts does the adoption.
//
// The floor is the account's newest transaction, not today, so the first sync's
// month of overlap cannot duplicate an imported history.
func (s *Sync) linkAccount(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, externalID string,
) (store.Account, error) {
	account, err := s.store.GetAccount(ctx, spaceID, accountID)
	if err != nil {
		return store.Account{}, err
	}
	floor, err := s.newestTransactionDate(ctx, spaceID, accountID)
	if err != nil {
		return store.Account{}, err
	}

	account.SimpleFINAccountID = externalID
	if !floor.IsZero() {
		account.SyncFloorOn = floor
	}
	if err := s.store.UpdateAccount(ctx, spaceID, &account); err != nil {
		return store.Account{}, err
	}
	// The first sync after a relink reads back from the account's own history.
	if err := s.store.SetAccountSyncedThrough(ctx, spaceID, accountID, domain.Date{}); err != nil {
		return store.Account{}, err
	}
	account.SyncedThroughOn = domain.Date{}
	return account, nil
}

// ignoreAccount records that this connection must not create or feed one of the
// accounts it reaches, and detaches any account it already feeds (otherwise the
// next sync would overrule the refusal). The local account and its rows are kept.
func (s *Sync) ignoreAccount(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
	entry store.IgnoredRemoteAccount,
) (store.IgnoredRemoteAccount, error) {
	entry.ConnectionID = connectionID
	if err := s.store.IgnoreRemoteAccount(ctx, spaceID, &entry); err != nil {
		return store.IgnoredRemoteAccount{}, err
	}
	// Both shapes a pairing can be in: owned by the connection, or stamped but
	// not yet adopted.
	account, found, err := s.findLocalAccount(ctx, spaceID, connectionID,
		provider.Account{ExternalID: entry.ExternalID})
	if err != nil {
		return store.IgnoredRemoteAccount{}, err
	}
	if found {
		if err := s.store.UnlinkAccount(ctx, spaceID, account.ID); err != nil {
			return store.IgnoredRemoteAccount{}, err
		}
	}
	return entry, nil
}

// newestTransactionDate is the newest day this account holds a posted row on,
// which a fresh link stamps as its floor. Estimates are excluded and today caps
// it: an imported forecast row can be dated a year out, and a floor there would
// stop the account importing.
func (s *Sync) newestTransactionDate(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID,
) (domain.Date, error) {
	var newest *time.Time
	err := s.conn().QueryRow(ctx,
		`SELECT max(date) FROM transactions
		 WHERE space_id = $1 AND account_id = $2 AND `+store.MoneyMoved,
		spaceID.UUID(), accountID).Scan(&newest)
	if err != nil {
		return domain.Date{}, fmt.Errorf("service: newest transaction date: %w", err)
	}
	if newest == nil {
		return domain.Date{}, nil
	}
	return earlierOf(domain.DateOf(*newest), domain.DateOf(s.now())), nil
}

func earlierOf(a, b domain.Date) domain.Date {
	if a.After(b) {
		return b
	}
	return a
}

// SyncConnection reads every account the connection reaches and imports what is
// new, reporting its progress as it goes (SyncProgressOf). A refused
// credential, a throttle and a bank needing reauthorization are recorded on the
// connection and reported, not returned; an error from here is a fault in this
// process.
func (s *Sync) SyncConnection(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) (SyncReport, error) {
	run, report, started, err := s.beginRun(ctx, spaceID, connectionID)
	if err != nil || !started {
		return report, err
	}
	return s.runSync(ctx, spaceID, connectionID, run)
}

// syncTimeout bounds a sync nobody is waiting on. A first sync reads years of
// history for every account the Bridge reaches, which takes minutes.
const syncTimeout = time.Hour

// StartSync is SyncConnection in the background, answering at once with where
// the run stands. A run of the connection already going here is joined rather
// than started twice.
func (s *Sync) StartSync(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) (SyncProgress, error) {
	run, _, started, err := s.beginRun(ctx, spaceID, connectionID)
	if err != nil {
		return SyncProgress{}, err
	}
	if started {
		go func() {
			// A panic here would take the server down; runSync has already
			// recorded it on the run.
			defer func() {
				if recovered := recover(); recovered != nil {
					slog.Error("sync panicked", "connection", connectionID,
						"panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
				}
			}()
			background, cancel := context.WithTimeout(context.WithoutCancel(ctx), syncTimeout)
			defer cancel()
			if _, err := s.runSync(background, spaceID, connectionID, run); err != nil {
				slog.Error("sync failed", "connection", connectionID, "error", err)
			}
		}()
	}
	return run.snapshot(), nil
}

// beginRun registers a sync of the connection, or says why none starts: it is
// parked, or a run of it is already going here, which is the run returned.
func (s *Sync) beginRun(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) (*syncRun, SyncReport, bool, error) {
	conn, err := s.store.GetConnection(ctx, spaceID, connectionID)
	if err != nil {
		return nil, SyncReport{}, false, err
	}
	if report, parked := s.parked(conn); parked {
		return connectionSyncs.skipped(conn.ID, report, s.now()), report, false, nil
	}
	run, started := connectionSyncs.begin(conn.ID, s.now())
	if !started {
		return run, alreadySyncing(conn), false, nil
	}
	return run, SyncReport{}, true, nil
}

// parked is a connection that must not be read yet.
func (s *Sync) parked(conn store.Connection) (SyncReport, bool) {
	// Claimed but not matched: syncing now would duplicate every account the
	// household already keeps.
	if conn.Status == store.ConnectionPendingLink {
		return SyncReport{
			ConnectionID: conn.ID,
			Status:       conn.Status,
			StatusDetail: "Match this connection's accounts to your own before syncing.",
			Skipped:      true,
		}, true
	}
	// Parked by a throttle. Refused here as well as in the scheduler, because the
	// Bridge answers a read it has already refused with a longer wait, so "Sync now"
	// would make it worse.
	if conn.RetryNotBefore != nil && s.now().Before(*conn.RetryNotBefore) {
		return SyncReport{
			ConnectionID:   conn.ID,
			Status:         conn.Status,
			StatusDetail:   conn.StatusDetail,
			RetryNotBefore: conn.RetryNotBefore,
			Skipped:        true,
		}, true
	}
	return SyncReport{}, false
}

func alreadySyncing(conn store.Connection) SyncReport {
	return SyncReport{
		ConnectionID: conn.ID,
		Status:       conn.Status,
		StatusDetail: "A sync of this connection is already running.",
		Skipped:      true,
	}
}

// runSync is the sync itself, recording on run what it is doing and how it
// ended.
func (s *Sync) runSync(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, run *syncRun,
) (report SyncReport, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			run.end(SyncReport{}, fmt.Errorf("the sync stopped unexpectedly: %v", recovered), s.now())
			panic(recovered)
		}
		run.end(report, err, s.now())
	}()

	// One sync of a connection at a time, across processes. Skipping beats queueing:
	// the run in flight will import what just arrived.
	unlock, ok, err := s.store.TryNamedLock(ctx,
		syncLockKey(spaceID, connectionID))
	if err != nil {
		return SyncReport{}, err
	}
	if !ok {
		return alreadySyncing(store.Connection{ID: connectionID}), nil
	}
	defer unlock()

	// Read again under the lock: another server's run may have parked it.
	conn, err := s.store.GetConnection(ctx, spaceID, connectionID)
	if err != nil {
		return SyncReport{}, err
	}
	if parked, skip := s.parked(conn); skip {
		return parked, nil
	}

	// Rows an earlier run wrote and could not settle. Read before the bank is
	// touched, so a concurrent sync of another connection keeps its own new rows,
	// and settled below with whatever arrives now. Without this, a failed settle's
	// rows would never be asked for again, since later runs resume past them.
	backlog, err := s.store.TransactionsNeedingSettle(ctx, spaceID)
	if err != nil {
		return SyncReport{}, err
	}

	accessURL, err := s.store.ConnectionAccessURL(ctx, spaceID, connectionID)
	if err != nil {
		return SyncReport{}, err
	}
	creds := provider.Credentials{AccessURL: accessURL}

	now := s.now()
	conn.LastSyncAt = &now
	report = SyncReport{ConnectionID: conn.ID}

	accounts, warnings, err := s.provider.AccountsWithWarnings(ctx, creds)
	if err != nil {
		return s.recordFailure(ctx, spaceID, &conn, report, now, err)
	}
	report.AccountsSeen = len(accounts)

	// The read worked, so a recorded throttle or failure is over; cleared now so a
	// recovered connection stops being parked.
	conn.Status = store.ConnectionActive
	conn.StatusDetail = ""
	conn.RetryNotBefore = nil
	// Rebuilt each run rather than appended to, so a bank that has been fixed
	// stops being reported.
	conn.SyncErrors = bankWarnings(warnings, now)

	linked, err := s.linkAccounts(ctx, spaceID, conn.ID, accounts)
	if err != nil {
		return SyncReport{}, err
	}
	report.AccountsLinked = len(linked)
	for _, row := range linked {
		if row.created {
			report.AccountsCreated++
		}
	}
	run.update(func(p *SyncProgress) { p.Accounts = len(linked) })

	var inserted []uuid.UUID
	// Accounts whose existing rows moved but which gained none. The settle rewrites
	// running balances only for accounts it inserted into, so these need it too.
	restated := map[uuid.UUID]bool{}
	reads := map[uuid.UUID]*accountImport{}
	for i, row := range linked {
		run.update(func(p *SyncProgress) {
			p.Phase = SyncImporting
			p.Account = i + 1
			p.AccountName = row.account.Name
		})
		read, err := s.importAccount(ctx, spaceID, creds, row.account)
		switch {
		case err == nil:
			run.update(func(p *SyncProgress) {
				p.TransactionsImported += len(read.inserted)
				p.TransactionsUpdated += read.updated
			})
			if err := s.store.SetAccountSyncedThrough(ctx, spaceID, row.account.ID, domain.DateOf(now)); err != nil {
				return SyncReport{}, err
			}
			reads[row.account.ID] = &read
			inserted = append(inserted, read.inserted...)
			if len(read.inserted) == 0 && read.updated+read.retired > 0 {
				restated[row.account.ID] = true
			}
			report.TransactionsUpdated += read.updated
			report.TransactionsRetired += read.retired
		case errors.Is(err, provider.ErrRateLimited):
			// The whole Access URL is throttled, so the remaining accounts would fail too.
			// What has already been imported stands.
			conn.Status = store.ConnectionRateLimited
			conn.StatusDetail = "SimpleFIN is rate limiting this connection. The stored credential is fine."
			retry := now.Add(syncRateLimitBackoff)
			conn.RetryNotBefore = &retry
		case errors.Is(err, provider.ErrCredentialsExpired):
			applyCredentialFailure(&conn, err)
		default:
			// One account's read failing is not the connection failing.
			conn.SyncErrors = append(conn.SyncErrors, store.BankSyncError{
				Institution: row.account.Name,
				Message:     err.Error(),
				At:          now,
			})
			continue
		}
		if conn.Status != store.ConnectionActive {
			break
		}
	}
	report.TransactionsImported = len(inserted)
	run.update(func(p *SyncProgress) { p.Phase = SyncSettling })

	// A bank the Bridge says needs reauthorization reports stale figures, so its
	// accounts' rows explain nothing.
	flagged := map[string]bool{}
	for _, warning := range warnings {
		flagged[warning.Institution] = true
	}
	for i := range linked {
		row := &linked[i]
		read := reads[row.account.ID]
		if flagged[row.institution] {
			read = nil
		}
		held, err := s.settleBalance(ctx, spaceID, row, read, now)
		if err != nil {
			return SyncReport{}, err
		}
		if held {
			report.BalancesHeld++
		}
	}

	// The backlog and the new rows in one pass, before anything records success. A
	// failure leaves the marks and no successful-sync stamp, so the rows are retried.
	// Conversion goes before alerts: a large-transaction alert compares the
	// amount in the household's own currency.
	ingested, err := s.Ingest.AfterIngest(ctx, s.store, spaceID, mergeIDs(backlog, inserted))
	if err != nil {
		return SyncReport{}, err
	}
	report.CurrencyStampFailed = ingested.CurrencyStampFailed
	for _, row := range linked {
		if !restated[row.account.ID] {
			continue
		}
		if err := RecomputeRunningBalances(ctx, s.store, spaceID, row.account.ID); err != nil {
			return SyncReport{}, err
		}
	}

	if conn.Status == store.ConnectionActive {
		conn.LastSuccessfulSyncAt = &now
	}
	if err := s.store.UpdateConnection(ctx, spaceID, &conn); err != nil {
		return SyncReport{}, err
	}

	// A failed sweep does not fail the sync: the rows are written, and deduping
	// means the next sweep picks up whatever this one missed.
	if s.Alerts != nil {
		if _, err := s.Alerts.RunForEveryone(ctx, spaceID); err != nil {
			report.AlertSweepFailed = true
		}
	}
	return finish(report, conn), nil
}

type linkedAccount struct {
	account store.Account
	created bool
	// The balance the Bridge reported, sign-normalized against the stored kind.
	// A new account takes it on link; an existing one in settleBalance.
	reported    domain.Money
	hasReported bool
	institution string
}

// accountImport is what importAccount did to one account: the ids it inserted,
// how many existing rows it corrected, how many vanished pendings it retired,
// and the rows it could touch, read before and after.
type accountImport struct {
	inserted         []uuid.UUID
	updated, retired int
	before, after    []store.Transaction
}

// settleBalance applies the balance the Bridge reported for an existing
// account, or holds it out when it looks like the feed dropping a figure
// (domain.SuspectBalanceReset) and the account's rows do not explain it
// (domain.ExplainReportedBalance). read is what this run imported for the
// account, nil when it read no transactions for it. Reports whether it held
// the balance.
//
// The trusted figure comes from snapshot history when the stored balance is a
// zero, because an account whose feed already dropped to 0 reads 0; so a hold
// also restores a balance that was already lost, dated when it was last seen.
// A hold keeps the trusted figure carried forward by the rows that did arrive,
// records the declined figure and why, and keeps WithheldBalanceAt stable
// while the same value keeps arriving.
func (s *Sync) settleBalance(
	ctx context.Context, spaceID store.SpaceID, row *linkedAccount, read *accountImport, now time.Time,
) (bool, error) {
	if row.created {
		return false, nil
	}
	account := &row.account
	established, hasEstablished, err := s.store.LastEstablishedBalance(ctx, spaceID, account.ID)
	if err != nil {
		return false, err
	}

	if domain.SuspectBalanceReset(
		established.Balance, hasEstablished, row.reported, row.hasReported,
		established.Days, account.AcceptZeroBalance,
	) {
		previous, fromStored := domain.TrustedBalance(
			account.ProviderBalance, account.HasProviderBalance, account.HasWithheldBalance, established.Balance)
		feed := domain.FeedUnread
		var before, after []domain.Transaction
		if read != nil {
			synced, err := s.store.AccountHasSyncedTransactions(ctx, spaceID, account.ID)
			if err != nil {
				return false, err
			}
			feed = domain.FeedAbsent
			if synced {
				feed = domain.FeedRead
			}
			before, after = store.DomainTransactions(read.before), store.DomainTransactions(read.after)
		}
		explanation := domain.ExplainReportedBalance(
			previous, row.reported, row.hasReported, before, after, feed)
		if !explanation.Explained {
			account.ProviderBalance = explanation.Expected
			account.HasProviderBalance = true
			if !fromStored {
				asOf := established.AsOf
				account.ProviderBalanceAt = &asOf
			}
			sameHold := account.HasWithheldBalance &&
				account.WithheldBalanceAt != nil &&
				account.WithheldBalance.Equal(row.reported)
			account.WithheldBalance = row.reported
			account.HasWithheldBalance = true
			account.WithheldBalanceReason = explanation.Reason()
			if !sameHold {
				at := now
				account.WithheldBalanceAt = &at
			}
			if err := s.store.SetAccountBalance(ctx, spaceID, account); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	applyReportedBalance(account, row.reported, now)
	return false, s.store.SetAccountBalance(ctx, spaceID, account)
}

// applyReportedBalance takes the Bridge's figure and clears any hold.
func applyReportedBalance(account *store.Account, reported domain.Money, now time.Time) {
	account.ProviderBalance = reported
	account.HasProviderBalance = true
	account.ProviderBalanceAt = &now
	account.WithheldBalance = domain.Zero
	account.HasWithheldBalance = false
	account.WithheldBalanceAt = nil
	account.WithheldBalanceReason = ""
}

// linkAccounts matches the Bridge's accounts to local rows, creating what is
// missing. In order:
//
//  1. Already linked, on (connection_id, external_id).
//  2. Awaiting a link: an imported account the relink step stamped with this
//     provider account and a sync floor. Adopted, floor and all, rather than
//     duplicated.
//  3. New.
//
// A user-set name is never overwritten; balance, logo and institution are the
// provider's.
func (s *Sync) linkAccounts(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, incoming []provider.Account,
) ([]linkedAccount, error) {
	now := s.now()
	out := make([]linkedAccount, 0, len(incoming))

	// A refused account is neither created nor updated. Skipped here rather than
	// upstream so every path into a sync obeys it.
	ignored, err := s.store.IgnoredExternalIDs(ctx, spaceID, connectionID)
	if err != nil {
		return nil, err
	}

	for _, remote := range incoming {
		if remote.ExternalID == "" || ignored[remote.ExternalID] {
			continue
		}
		institutionID, err := s.store.EnsureInstitution(ctx, spaceID,
			remote.InstitutionName, remote.InstitutionLogoURL)
		if err != nil {
			return nil, err
		}

		account, found, err := s.findLocalAccount(ctx, spaceID, connectionID, remote)
		if err != nil {
			return nil, err
		}
		// Ignored and deleted accounts keep their link, so neither is fed nor
		// re-created, and un-ignoring resumes the feed on the same row.
		if found && (account.IsIgnored() || account.IsDeleted) {
			continue
		}
		if !found {
			account = store.Account{
				Name:              remote.Name,
				Kind:              remote.Kind,
				Type:              textutil.FirstNonBlank(remote.Type, string(remote.Kind)),
				Currency:          remote.Currency,
				IncludeInNetWorth: true,
			}
		}

		account.ConnectionID = connectionID
		account.ExternalID = remote.ExternalID
		account.SimpleFINAccountID = remote.ExternalID
		account.InstitutionID = institutionID
		account.MaskedNumber = textutil.FirstNonBlank(remote.MaskedNumber, account.MaskedNumber)
		account.LogoURL = textutil.FirstNonBlank(remote.InstitutionLogoURL, account.LogoURL)
		// NormalizeBalance already ran in the provider, over the kind the provider
		// guessed. The user's kind wins, so the sign is re-derived here.
		incomingBalance := provider.NormalizeBalance(account.Kind, remote.Balance)
		if !found {
			applyReportedBalance(&account, incomingBalance, now)
		}
		// Each statement figure is written only when the issuer reported one, so a
		// sync never wipes a figure somebody typed in by hand.
		if remote.HasCreditLimit {
			account.CreditLimit, account.HasCreditLimit = remote.CreditLimit, true
		}
		if remote.HasStatementBalance {
			account.StatementBalance, account.HasStatementBalance = remote.StatementBalance, true
		}
		if remote.HasMinimumPayment {
			account.MinimumDue, account.HasMinimumDue = remote.MinimumPayment, true
		}
		if !remote.PaymentDueOn.IsZero() {
			account.DueDate = remote.PaymentDueOn
		}
		if remote.HasStatementBalance || remote.HasMinimumPayment || !remote.PaymentDueOn.IsZero() {
			// The issuer's own report replaces a filed bill's copy.
			account.StatementBillID = uuid.Nil
		}
		if remote.HasInterestRate {
			account.InterestRate, account.HasInterestRate = remote.InterestRate, true
		}
		// The whole payload, mapped fields included, so which issuers report a
		// statement is a query against this column.
		account.ProviderExtra = providerExtra(remote.Extra)

		if found {
			if err := s.store.UpdateAccount(ctx, spaceID, &account); err != nil {
				return nil, err
			}
		} else if err := s.store.CreateAccount(ctx, spaceID, &account); err != nil {
			return nil, err
		}
		out = append(out, linkedAccount{
			account:     account,
			created:     !found,
			reported:    incomingBalance,
			hasReported: remote.HasBalance,
			institution: remote.InstitutionName,
		})
	}
	return out, nil
}

func (s *Sync) findLocalAccount(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, remote provider.Account,
) (store.Account, bool, error) {
	account, err := s.store.GetAccountByExternalID(ctx, spaceID, connectionID, remote.ExternalID)
	if err == nil {
		return account, true, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.Account{}, false, err
	}

	account, err = s.store.GetAccountAwaitingLink(ctx, spaceID, remote.ExternalID)
	if err == nil {
		return account, true, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.Account{}, false, err
	}
	return store.Account{}, false, nil
}

// importAccount pulls one account's history and writes what is new.
func (s *Sync) importAccount(
	ctx context.Context, spaceID store.SpaceID, creds provider.Credentials, account store.Account,
) (accountImport, error) {
	since, err := s.resumeFrom(ctx, spaceID, account)
	if err != nil {
		return accountImport{}, err
	}

	rows, err := s.provider.Transactions(ctx, creds, account.ExternalID, provider.TransactionQuery{
		Since:       since,
		PayeeSource: provider.PayeeFromProvider,
	})
	if err != nil {
		return accountImport{}, err
	}

	window := store.TransactionQuery{
		AccountIDs: []uuid.UUID{account.ID},
		From:       since,
		// A row the user deleted counts as present; otherwise every sync would
		// resurrect it.
		IncludeDeleted: true,
	}
	existing, err := s.store.ListTransactions(ctx, spaceID, window)
	if err != nil {
		return accountImport{}, err
	}

	byExternalID := map[string]store.Transaction{}
	for _, row := range existing {
		if row.ExternalID != "" {
			byExternalID[row.ExternalID] = row
		}
	}
	pool := newClaimPool(existing)

	var inserted []uuid.UUID
	updated := 0
	today := domain.DateOf(s.now())
	for _, row := range rows {
		// The resume window is enforced on the data too: a connector can answer wider
		// than asked, and the dedupe sets were loaded from `since`, so an older row
		// would be inserted a second time.
		if !since.IsZero() && row.Date.Before(since) {
			continue
		}
		// A pending charge cannot be dated after today; a connector reporting a
		// scheduled payment this way is describing an expectation, which belongs to the
		// series projection, not the ledger.
		if row.Pending && row.Date.After(today) {
			continue
		}
		if match, ok := byExternalID[row.ExternalID]; ok {
			changed, err := s.settleExisting(ctx, spaceID, account, match, row)
			if err != nil {
				return accountImport{}, err
			}
			if changed {
				updated++
			}
			continue
		}
		// No id matches, so claim by content: a row worded and dated the same, or the
		// pending authorization this posted charge settles. A claim is spent either way.
		claimed, found := pool.claimByContent(row)
		if !found {
			claimed, found = pool.claimSettledPending(row)
		}
		if found {
			// A row with an external id is some other synced charge that reads the same,
			// and a deleted row is gone on purpose; neither is written to. Adoption is only
			// for rows no aggregator id reaches.
			if claimed.ExternalID != "" || claimed.IsDeleted {
				continue
			}
			changed, err := s.settleExisting(ctx, spaceID, account, *claimed, row)
			if err != nil {
				return accountImport{}, err
			}
			if changed {
				updated++
			}
			continue
		}

		// The floor applies only here, in front of a row that would be created.
		// Corrections to rows the ledger already holds (above) are wanted at any depth;
		// the floor stops the backfill re-creating history the migration already wrote.
		if !account.SyncFloorOn.IsZero() && row.Date.Before(account.SyncFloorOn) {
			continue
		}

		txn := &store.Transaction{
			AccountID:     account.ID,
			ExternalID:    row.ExternalID,
			Date:          row.Date,
			Amount:        row.Amount,
			Currency:      textutil.FirstNonBlank(row.Currency, account.Currency),
			StatementName: row.StatementName,
			// The bank's wording seeds the display name; a rename rule or the user
			// replaces it. The statement name never changes again.
			Payee:         textutil.FirstNonBlank(row.Payee, row.StatementName),
			Memo:          row.Memo,
			TransactedOn:  row.TransactedOn,
			ProviderExtra: providerExtra(row.Extra),
			IsPending:     row.Pending,
			Source:        domain.SourceSync,
			// Investment, loan and asset rows skip the review queue — see BornReviewed.
			IsReviewed: account.Kind.BornReviewed(),
			// Written with the row, because the settle runs after the insert commits: a
			// process that dies in between must leave the backlog on the row.
			// AfterIngest clears it.
			NeedsSettle: true,
		}
		// Stamp the cash-flow date before the row lands, so a card charge files under
		// its statement rather than its swipe day.
		ApplyEffectiveDate(txn, account, domain.Date{}, false)
		if err := s.store.CreateTransaction(ctx, spaceID, txn); err != nil {
			return accountImport{}, err
		}
		byExternalID[row.ExternalID] = *txn
		inserted = append(inserted, txn.ID)
	}

	retired, err := s.retireVanishedPendings(ctx, spaceID, since, existing, rows)
	if err != nil {
		return accountImport{}, err
	}
	after, err := s.store.ListTransactions(ctx, spaceID, window)
	if err != nil {
		return accountImport{}, err
	}
	return accountImport{
		inserted: inserted,
		updated:  updated,
		retired:  retired,
		before:   existing,
		after:    after,
	}, nil
}

// retireVanishedPendings soft-deletes synced pending rows the feed stopped
// reporting: voided authorizations, and pendings reposted under a new external
// id. Only rows this sync asked about are considered (inside the window, synced,
// still pending), and deletion goes through DeleteTransaction so a transfer pair
// releases before its leg does.
func (s *Sync) retireVanishedPendings(
	ctx context.Context, spaceID store.SpaceID, since domain.Date,
	existing []store.Transaction, incoming []provider.Transaction,
) (int, error) {
	reported := make(map[string]bool, len(incoming))
	for _, row := range incoming {
		if row.ExternalID != "" {
			reported[row.ExternalID] = true
		}
	}

	retired := 0
	for _, row := range existing {
		if row.IsDeleted || !row.IsPending || row.Source != domain.SourceSync || row.ExternalID == "" {
			continue
		}
		if reported[row.ExternalID] || (!since.IsZero() && row.Date.Before(since)) {
			continue
		}
		if err := s.store.DeleteTransaction(ctx, spaceID, row.ID); err != nil {
			return retired, err
		}
		retired++
	}
	return retired, nil
}

// settleExisting updates the fields the bank owns on a row already here, as when
// a pending charge posts. Nothing the user owns (payee, category, note) is
// touched.
//
// The external id is one of the bank's fields: reached through the claim pool,
// writing it adopts an imported row, and later syncs settle it by id.
func (s *Sync) settleExisting(
	ctx context.Context, spaceID store.SpaceID, account store.Account,
	stored store.Transaction, incoming provider.Transaction,
) (bool, error) {
	if stored.IsDeleted {
		return false, nil
	}
	if stored.ExternalID == incoming.ExternalID &&
		stored.Date == incoming.Date && stored.Amount.Equal(incoming.Amount) &&
		stored.IsPending == incoming.Pending && stored.StatementName == incoming.StatementName {
		return false, nil
	}

	// A moved date moves the statement and so the effective date. Re-derive it only
	// when the stored value was itself derived (unset, or what the old date gives),
	// so a hand-corrected date survives.
	dateMoved := stored.Date != incoming.Date
	amountMoved := !stored.Amount.Equal(incoming.Amount)
	wasDerived := stored.EffectiveDate.IsZero() ||
		stored.EffectiveDate == domain.EffectiveDateFor(
			stored.Date, account.Kind == domain.KindCreditCard,
			StatementCloseDay(account), PaymentDueDay(account), domain.Date{})

	stored.ExternalID = incoming.ExternalID
	stored.Date = incoming.Date
	stored.Amount = incoming.Amount
	stored.IsPending = incoming.Pending
	stored.StatementName = incoming.StatementName
	if dateMoved {
		ApplyEffectiveDate(&stored, account, domain.Date{}, wasDerived)
	}
	// A changed amount invalidates the stored conversion; clear it so the next
	// StampSpace re-derives from the posted amount.
	if amountMoved {
		ClearPrimaryAmount(&stored)
	}
	if err := s.store.UpdateTransaction(ctx, spaceID, &stored); err != nil {
		return false, err
	}
	return true, nil
}

// resumeFrom is the earliest day this sync may ask for: the later of the newest
// row held and the last day a sync read this account through, less the overlap.
// Zero means the provider's own initial backfill.
//
// The read-through day keeps a quiet account (a loan, an idle brokerage) to one
// request instead of re-reading back to its last row every night. Estimates are
// excluded and today is the ceiling, as in newestTransactionDate.
//
// The floor deliberately does not narrow this: a card posts a charge under its
// swipe date, which can fall before the floor, and it must still be read to
// settle the pending copy. importAccount enforces the floor in front of the
// insert only.
func (s *Sync) resumeFrom(
	ctx context.Context, spaceID store.SpaceID, account store.Account,
) (domain.Date, error) {
	newest, err := s.newestTransactionDate(ctx, spaceID, account.ID)
	if err != nil {
		return domain.Date{}, err
	}
	resumeAt := newest
	if read := earlierOf(account.SyncedThroughOn, domain.DateOf(s.now())); read.After(resumeAt) {
		resumeAt = read
	}
	if resumeAt.IsZero() {
		return domain.Date{}, nil
	}
	return resumeAt.AddDays(-syncResumeOverlapDays), nil
}

// recordFailure writes the state a failed read leaves the connection in and
// reports it, rather than returning an error the user would see as a 500.
func (s *Sync) recordFailure(
	ctx context.Context, spaceID store.SpaceID, conn *store.Connection,
	report SyncReport, now time.Time, cause error,
) (SyncReport, error) {
	switch {
	case errors.Is(cause, provider.ErrCredentialsExpired):
		applyCredentialFailure(conn, cause)
	case errors.Is(cause, provider.ErrRateLimited):
		conn.Status = store.ConnectionRateLimited
		conn.StatusDetail = "SimpleFIN is rate limiting this connection. The stored credential is fine."
		retry := now.Add(syncRateLimitBackoff)
		conn.RetryNotBefore = &retry
	default:
		// A transport failure or malformed payload says nothing about the credential,
		// so the status is left alone. last_sync_at still moves, which makes a
		// connection that keeps failing visible.
		if err := s.store.UpdateConnection(ctx, spaceID, conn); err != nil {
			return SyncReport{}, err
		}
		return SyncReport{}, cause
	}
	if err := s.store.UpdateConnection(ctx, spaceID, conn); err != nil {
		return SyncReport{}, err
	}
	return finish(report, *conn), nil
}

// applyCredentialFailure marks the one failure only a fresh setup token fixes.
func applyCredentialFailure(conn *store.Connection, cause error) {
	conn.Status = store.ConnectionCredentialsExpired
	conn.StatusDetail = cause.Error()
	var expired *provider.CredentialsExpiredError
	if errors.As(cause, &expired) {
		conn.StatusDetail = expired.Reason
	}
	conn.RetryNotBefore = nil
}

func finish(report SyncReport, conn store.Connection) SyncReport {
	report.ConnectionID = conn.ID
	report.Status = conn.Status
	report.StatusDetail = conn.StatusDetail
	report.BankWarnings = conn.SyncErrors
	report.RetryNotBefore = conn.RetryNotBefore
	return report
}

// bankWarnings stamps the provider's per-bank warnings with the time they were
// observed, so a stale one is obvious.
func bankWarnings(warnings []provider.BankWarning, at time.Time) []store.BankSyncError {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]store.BankSyncError, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, store.BankSyncError{
			Institution: warning.Institution,
			Message:     warning.Message,
			At:          at,
		})
	}
	return out
}

// mergeIDs concatenates two id lists without repeating one: settling a row
// twice would run the user's rules over it twice.
func mergeIDs(first, second []uuid.UUID) []uuid.UUID {
	if len(first) == 0 {
		return second
	}
	seen := make(map[uuid.UUID]bool, len(first)+len(second))
	merged := make([]uuid.UUID, 0, len(first)+len(second))
	for _, id := range append(append([]uuid.UUID{}, first...), second...) {
		if seen[id] {
			continue
		}
		seen[id] = true
		merged = append(merged, id)
	}
	return merged
}

// fingerprint stands in for a provider id on a row that has none, such as an
// imported history overlapping the Bridge's first read. Wording is folded
// because the same charge reaches two systems punctuated differently.
func fingerprint(on domain.Date, amount domain.Money, statementName string) string {
	return on.String() + "|" + amount.String() + "|" + foldStatementName(statementName)
}

func foldStatementName(name string) string {
	var folded strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			folded.WriteRune(r)
		}
	}
	return folded.String()
}

// providerExtra serializes what the feed sent that no column names. A payload
// that will not marshal is dropped rather than failing the sync.
func providerExtra(extra map[string]any) json.RawMessage {
	if len(extra) == 0 {
		return nil
	}
	raw, err := json.Marshal(extra)
	if err != nil {
		return nil
	}
	return raw
}

// syncLockKey namespaces the advisory lock away from other features' locks.
func syncLockKey(spaceID store.SpaceID, connectionID uuid.UUID) string {
	return "agentifi:sync:" + spaceID.UUID().String() + ":" + connectionID.String()
}
