package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The materialized daily balance history the net-worth chart reads
// (calculations.md §12): one row per account per day, from the Simplifi import
// and the scheduler's daily pass. A derived row carries the figure it was
// walked from, so readers re-walk it over the current ledger
// (domain.RederiveHistory) rather than trust a stale number.

// BalanceSnapshot is one account's balance at the end of one day.
type BalanceSnapshot struct {
	AccountID uuid.UUID
	AsOf      domain.Date
	Balance   domain.Money
	Anchor    domain.BalanceAnchor
	HasAnchor bool
}

// BalanceHistoryMode is the date mode the snapshots are materialized in.
// Readers that mix snapshots with ledger walks must walk in this mode too, or
// one chart answers two accounts under two date definitions.
const BalanceHistoryMode = domain.DatePosted

// UpsertBalanceSnapshots writes one row per account per day, idempotent by
// (account_id, as_of), so the nightly pass and a backfill are safe to replay.
func (s *Store) UpsertBalanceSnapshots(
	ctx context.Context, spaceID SpaceID, snapshots []BalanceSnapshot,
) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		var currency string
		if err := db.Read().QueryRowContext(ctx, `SELECT currency FROM accounts WHERE id = ?`, snapshot.AccountID).Scan(&currency); err != nil {
			return wrap("store: snapshot account currency", err)
		}
		balance, err := toMinor(snapshot.Balance.Decimal(), currency)
		if err != nil {
			return wrap("store: snapshot balance", err)
		}
		var anchorOn, anchorBalance any
		if snapshot.HasAnchor {
			anchorOn = snapshot.Anchor.On.String()
			anchorBalance, err = toMinor(snapshot.Anchor.Balance.Decimal(), currency)
			if err != nil {
				return wrap("store: snapshot anchor", err)
			}
		}
		now := db.Now()
		if err := sqliteExecOne(ctx, db.Write(), "store: upsert balance snapshot", `
			INSERT INTO balance_snapshots
				(id, account_id, as_of, balance, anchor_on, anchor_balance, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (account_id, as_of)
			DO UPDATE SET balance = excluded.balance, is_imported = false,
				anchor_on = excluded.anchor_on, anchor_balance = excluded.anchor_balance,
				updated_at = excluded.updated_at`,
			uuid.New(), snapshot.AccountID, snapshot.AsOf.String(), balance,
			anchorOn, anchorBalance, now, now); err != nil {
			return err
		}
	}
	return nil
}

func anchorDay(anchor domain.BalanceAnchor, has bool) *time.Time {
	if !has {
		return nil
	}
	t := anchor.On.Time()
	return &t
}

// ListBalanceHistory reads every snapshot in the space through the given day,
// oldest first — the order domain.BalancesOn expects.
func (s *Store) ListBalanceHistory(
	ctx context.Context, spaceID SpaceID, through domain.Date,
) ([]domain.BalancePoint, error) {
	return s.listBalanceHistory(ctx, spaceID, anchorDay(domain.BalanceAnchor{On: through}, !through.IsZero()))
}

// ListAllBalanceHistory reads every snapshot in the space, oldest first, since
// domain.RederiveHistory may need an observation past the chart's window.
func (s *Store) ListAllBalanceHistory(
	ctx context.Context, spaceID SpaceID,
) ([]domain.BalancePoint, error) {
	return s.listBalanceHistory(ctx, spaceID, nil)
}

func (s *Store) listBalanceHistory(
	ctx context.Context, spaceID SpaceID, through *time.Time,
) ([]domain.BalancePoint, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	var cutoff any
	if through != nil {
		cutoff = domain.DateOf(*through).String()
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list balance history", func(row scanner) (domain.BalancePoint, error) {
		var accountID uuid.UUID
		var asOf, currency string
		var balance int64
		var imported bool
		var anchorOn sql.Null[string]
		var anchorBalance sql.Null[int64]
		if err := row.Scan(&accountID, &asOf, &balance, &imported, &anchorOn, &anchorBalance, &currency); err != nil {
			return domain.BalancePoint{}, err
		}
		day, err := time.Parse("2006-01-02", asOf)
		if err != nil {
			return domain.BalancePoint{}, err
		}
		parsed, err := fromMinor(balance, currency)
		if err != nil {
			return domain.BalancePoint{}, err
		}
		point := domain.BalancePoint{
			AccountID: domain.ID(accountID.String()), On: domain.DateOf(day),
			Balance: domain.FromDecimal(parsed), Observed: imported,
		}
		if anchorOn.Valid && anchorBalance.Valid {
			on, err := time.Parse("2006-01-02", anchorOn.V)
			if err != nil {
				return domain.BalancePoint{}, err
			}
			amount, err := fromMinor(anchorBalance.V, currency)
			if err != nil {
				return domain.BalancePoint{}, err
			}
			point.Anchor = domain.BalanceAnchor{On: domain.DateOf(on), Balance: domain.FromDecimal(amount)}
			point.HasAnchor = true
		}
		return point, nil
	}, `SELECT b.account_id, b.as_of, b.balance, b.is_imported, b.anchor_on, b.anchor_balance, a.currency
		FROM balance_snapshots b JOIN accounts a ON a.id = b.account_id
		WHERE (? IS NULL OR b.as_of <= ?) ORDER BY b.as_of`, cutoff, cutoff)
}

// EstablishedBalance is the last non-zero balance an account carried and how
// long that run lasted, read from the snapshot history rather than the current
// provider balance: an account whose feed has already dropped to 0 still has
// its real balance in history, to judge by and to restore.
type EstablishedBalance struct {
	Balance domain.Money
	// AsOf is the day that figure was last seen.
	AsOf time.Time
	// Days is how long the non-zero run ending at AsOf lasted: back to the day
	// after the previous zero, or to the earliest snapshot.
	Days int
}

// LastEstablishedBalance reads the account's last non-zero balance from the
// snapshots; ok is false when there is none, and the caller applies the feed.
func (s *Store) LastEstablishedBalance(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID,
) (EstablishedBalance, bool, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return EstablishedBalance{}, false, err
	}
	var amount int64
	var asOfText, startText, currency string
	err = db.Read().QueryRowContext(ctx, `
		WITH last_good AS (
			SELECT as_of, balance FROM balance_snapshots
			 WHERE account_id = ? AND balance <> 0 ORDER BY as_of DESC LIMIT 1)
		SELECT lg.balance, lg.as_of,
			COALESCE(
				(SELECT max(as_of) FROM balance_snapshots WHERE account_id = ? AND balance = 0 AND as_of < lg.as_of),
				(SELECT min(as_of) FROM balance_snapshots WHERE account_id = ?)),
			(SELECT currency FROM accounts WHERE id = ?)
		FROM last_good lg`, accountID, accountID, accountID, accountID).Scan(&amount, &asOfText, &startText, &currency)
	if errors.Is(err, sql.ErrNoRows) {
		return EstablishedBalance{}, false, nil
	}
	if err != nil {
		return EstablishedBalance{}, false, wrap("store: last established balance", err)
	}
	asOf, err := time.Parse("2006-01-02", asOfText)
	if err != nil {
		return EstablishedBalance{}, false, err
	}
	runStart, err := time.Parse("2006-01-02", startText)
	if err != nil {
		return EstablishedBalance{}, false, err
	}
	balance, err := fromMinor(amount, currency)
	if err != nil {
		return EstablishedBalance{}, false, wrap("store: parse established balance", err)
	}
	days := domain.DaysBetween(domain.DateOf(runStart), domain.DateOf(asOf))
	return EstablishedBalance{Balance: domain.FromDecimal(balance), AsOf: asOf, Days: days}, true, nil
}

// SnapshotAccounts materializes one row per live account for the given day,
// quiet accounts included, none before an account's history start. The
// figure is domain.BalanceAsOf for that day — not the current balance, which
// has no date filter and would disagree with the rebuild.
func (s *Store) SnapshotAccounts(
	ctx context.Context, spaceID SpaceID, on domain.Date,
) (int, error) {
	accounts, byAccount, err := s.accountLedgers(ctx, spaceID)
	if err != nil {
		return 0, err
	}

	written := 0
	for _, account := range accounts {
		if !domain.HasHistoryOn(DomainAccount(account), byAccount[account.ID], on) {
			continue
		}
		balance := domain.BalanceAsOf(
			DomainAccount(account), byAccount[account.ID], on, domain.DatePosted)
		anchor, hasAnchor := domain.AnchorFor(DomainAccount(account), byAccount[account.ID], on)
		if err := s.UpsertBalanceSnapshots(ctx, spaceID, []BalanceSnapshot{{
			AccountID: account.ID, AsOf: on, Balance: balance,
			Anchor: anchor, HasAnchor: hasAnchor,
		}}); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// RebuildBalanceHistory replaces every snapshot in [from, through] with figures
// derived from the ledgers (§12's periodic full rebuild). Imported history
// cannot be re-derived and stands; days before an account's history start get
// no row. O(days × ledger), so nightly only.
func (s *Store) RebuildBalanceHistory(
	ctx context.Context, spaceID SpaceID, from, through domain.Date,
) (int, error) {
	if through.Before(from) {
		return 0, fmt.Errorf("store: rebuild window ends before it starts")
	}
	accounts, byAccount, err := s.accountLedgers(ctx, spaceID)
	if err != nil {
		return 0, err
	}

	var derived []BalanceSnapshot
	for _, account := range accounts {
		rows := byAccount[account.ID]
		anchor, hasAnchor := domain.AnchorFor(DomainAccount(account), rows, through)
		for day := from; !day.After(through); day = day.AddDays(1) {
			if !domain.HasHistoryOn(DomainAccount(account), rows, day) {
				continue
			}
			derived = append(derived, BalanceSnapshot{
				AccountID: account.ID, AsOf: day,
				Balance: domain.BalanceAsOf(DomainAccount(account), rows, day, BalanceHistoryMode),
				Anchor:  anchor, HasAnchor: hasAnchor,
			})
		}
	}

	scoped, err := s.ForHousehold(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	err = scoped.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if _, err := db.Write().ExecContext(ctx,
			`DELETE FROM balance_snapshots WHERE as_of >= ? AND as_of <= ?`,
			from.String(), through.String()); err != nil {
			return wrap("store: rebuild balance history", err)
		}
		return tx.UpsertBalanceSnapshots(ctx, spaceID, derived)
	})
	if err != nil {
		return 0, err
	}
	return len(derived), nil
}

// accountLedgers is every account in the space with its postings, closed and
// ignored included, so un-ignoring leaves no gap in the chart.
func (s *Store) accountLedgers(
	ctx context.Context, spaceID SpaceID,
) ([]Account, map[uuid.UUID][]domain.Posting, error) {
	accounts, err := s.ListAccounts(ctx, spaceID, AccountQuery{IncludeClosed: true, IncludeIgnored: true})
	if err != nil {
		return nil, nil, err
	}
	all, err := s.LoadPostings(ctx, spaceID, TransactionQuery{})
	if err != nil {
		return nil, nil, err
	}
	byAccount := make(map[uuid.UUID][]domain.Posting)
	for _, posting := range all {
		id, parseErr := ParseID(posting.Txn.AccountID)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		byAccount[id] = append(byAccount[id], posting)
	}
	return accounts, byAccount, nil
}

// ReconcileHistoryStarts rebuilds the snapshots of every account whose history
// start (domain.HistoryStart) moved since its last rebuild, so imports, edits
// and overrides need not trigger one themselves. Reads are right meanwhile:
// every reader drops points before the start and walks the ledger instead.
func (s *Store) ReconcileHistoryStarts(
	ctx context.Context, spaceID SpaceID, through domain.Date,
) (int, error) {
	accounts, byAccount, err := s.accountLedgers(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	rebuilt := 0
	for _, account := range accounts {
		start := domain.HistoryStart(DomainAccount(account), byAccount[account.ID])
		if start == account.HistoryRebuiltFrom {
			continue
		}
		if err := s.rebuildAccountHistory(
			ctx, spaceID, account, byAccount[account.ID], through); err != nil {
			return rebuilt, err
		}
		rebuilt++
	}
	return rebuilt, nil
}

// RebuildAccountHistory rebuilds one account's snapshots now, for a caller
// that just moved its history start.
func (s *Store) RebuildAccountHistory(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, through domain.Date,
) error {
	account, err := s.GetAccount(ctx, spaceID, accountID)
	if err != nil {
		return err
	}
	postings, err := s.LoadPostings(ctx, spaceID, TransactionQuery{AccountIDs: []uuid.UUID{accountID}})
	if err != nil {
		return err
	}
	return s.rebuildAccountHistory(ctx, spaceID, account, postings, through)
}

// rebuildAccountHistory drops derived rows dated before the history start
// (imported rows stay: they are observations) and fills every missing day from
// the start through `through` from domain.BalanceHistory, so a lone imported
// row is not carried forward. Existing rows are not rewritten: an old row was
// walked back from the provider's figure of its own day.
func (s *Store) rebuildAccountHistory(
	ctx context.Context, spaceID SpaceID, account Account, postings []domain.Posting,
	through domain.Date,
) error {
	domainAccount := DomainAccount(account)
	start := domain.HistoryStart(domainAccount, postings)
	return s.InHouseholdTx(ctx, spaceID, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if !start.IsZero() {
			if _, err := db.Write().ExecContext(ctx, `
				DELETE FROM balance_snapshots
				 WHERE account_id = ? AND NOT is_imported AND as_of < ?`,
				account.ID, start.String()); err != nil {
				return wrap("store: trim balance history", err)
			}

			points := domain.BalanceHistory(domainAccount, postings, start, through)
			anchor, hasAnchor := domain.AnchorFor(domainAccount, postings, through)
			if err := tx.insertMissingSnapshots(
				ctx, spaceID, account.ID, points, anchor, hasAnchor); err != nil {
				return err
			}
		}
		var startDay any
		if !start.IsZero() {
			startDay = start.String()
		}
		_, err = db.Write().ExecContext(ctx,
			`UPDATE accounts SET history_rebuilt_from = ?, updated_at = ? WHERE id = ?`,
			startDay, db.Now(), account.ID)
		return wrap("store: record history rebuild", err)
	})
}

// insertMissingSnapshots writes derived rows for days that have none, in one
// statement.
func (s *Store) insertMissingSnapshots(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, points domain.BalanceProjection,
	anchor domain.BalanceAnchor, hasAnchor bool,
) error {
	if len(points) == 0 {
		return nil
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	var currency string
	if err := db.Read().QueryRowContext(ctx, `SELECT currency FROM accounts WHERE id = ?`, accountID).Scan(&currency); err != nil {
		return wrap("store: fill balance history currency", err)
	}
	var anchorOn, anchorBalance any
	if hasAnchor {
		anchorOn = anchor.On.String()
		anchorBalance, err = toMinor(anchor.Balance.Decimal(), currency)
		if err != nil {
			return wrap("store: fill balance history anchor", err)
		}
	}
	for _, point := range points {
		balance, err := toMinor(point.Balance.Decimal(), currency)
		if err != nil {
			return wrap("store: fill balance history amount", err)
		}
		now := db.Now()
		_, err = db.Write().ExecContext(ctx, `INSERT INTO balance_snapshots
			(id, account_id, as_of, balance, anchor_on, anchor_balance, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (account_id, as_of) DO NOTHING`,
			uuid.New(), accountID, point.On.String(), balance, anchorOn, anchorBalance, now, now)
		if err != nil {
			return wrap("store: fill balance history", err)
		}
	}
	return nil
}

// RederiveBalanceHistory re-walks every derived snapshot over the current
// ledger (domain.RederiveHistory) and writes back those that moved, returning
// the count. Readers re-derive anyway; this keeps the column honest for direct
// readers. Imported rows are never written.
func (s *Store) RederiveBalanceHistory(ctx context.Context, spaceID SpaceID) (int, error) {
	accounts, byAccount, err := s.accountLedgers(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	history, err := s.ListAllBalanceHistory(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	domainAccounts := make([]domain.Account, 0, len(accounts))
	postings := make(map[domain.ID][]domain.Posting, len(byAccount))
	for _, account := range accounts {
		domainAccounts = append(domainAccounts, DomainAccount(account))
		postings[domain.ID(account.ID.String())] = byAccount[account.ID]
	}

	type key struct {
		account domain.ID
		on      domain.Date
	}
	stored := make(map[key]domain.Money, len(history))
	for _, point := range history {
		if !point.Observed {
			stored[key{point.AccountID, point.On}] = point.Balance
		}
	}
	var accountIDs, days, balances []string
	for _, point := range domain.RederiveHistory(domainAccounts, postings, history) {
		if point.Observed {
			continue
		}
		if was := stored[key{point.AccountID, point.On}]; was.Cmp(point.Balance) == 0 {
			continue
		}
		accountIDs = append(accountIDs, string(point.AccountID))
		days = append(days, point.On.String())
		balances = append(balances, point.Balance.String())
	}
	if len(days) == 0 {
		return 0, nil
	}
	scoped, err := s.ForHousehold(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	written := 0
	err = scoped.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		for i, day := range days {
			var currency string
			if err := db.Read().QueryRowContext(ctx, `SELECT currency FROM accounts WHERE id = ?`, accountIDs[i]).Scan(&currency); err != nil {
				return wrap("store: rederive balance currency", err)
			}
			amount, err := domain.FromString(balances[i])
			if err != nil {
				return err
			}
			minor, err := toMinor(amount.Decimal(), currency)
			if err != nil {
				return err
			}
			result, err := db.Write().ExecContext(ctx, `UPDATE balance_snapshots
				SET balance = ?, updated_at = ? WHERE NOT is_imported AND account_id = ? AND as_of = ?`,
				minor, db.Now(), accountIDs[i], day)
			if err != nil {
				return wrap("store: rederive balance history", err)
			}
			n, err := result.RowsAffected()
			if err != nil {
				return wrap("store: rederive balance history", err)
			}
			written += int(n)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}
