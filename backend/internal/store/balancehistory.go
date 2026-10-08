package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"

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
	for _, snapshot := range snapshots {
		if err := s.execOne(ctx, "store: upsert balance snapshot", `
			INSERT INTO balance_snapshots
				(id, account_id, as_of, balance, space_id, anchor_on, anchor_balance)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (account_id, as_of)
			DO UPDATE SET balance = EXCLUDED.balance, is_imported = false,
				anchor_on = EXCLUDED.anchor_on, anchor_balance = EXCLUDED.anchor_balance,
				updated_at = now()`,
			uuid.New(), snapshot.AccountID, snapshot.AsOf,
			dbconv.Money(snapshot.Balance), spaceID.UUID(),
			anchorDay(snapshot.Anchor, snapshot.HasAnchor),
			dbconv.NullMoney(snapshot.Anchor.Balance, snapshot.HasAnchor)); err != nil {
			return err
		}
	}
	return nil
}

func anchorDay(anchor domain.BalanceAnchor, has bool) *domain.Date {
	if !has {
		return nil
	}
	return dbconv.NullDate(anchor.On)
}

// ListBalanceHistory reads every snapshot in the space through the given day,
// oldest first — the order domain.BalancesOn expects.
func (s *Store) ListBalanceHistory(
	ctx context.Context, spaceID SpaceID, through domain.Date,
) ([]domain.BalancePoint, error) {
	return s.listBalanceHistory(ctx, spaceID, dbconv.NullDate(through))
}

// ListAllBalanceHistory reads every snapshot in the space, oldest first, since
// domain.RederiveHistory may need an observation past the chart's window.
func (s *Store) ListAllBalanceHistory(
	ctx context.Context, spaceID SpaceID,
) ([]domain.BalancePoint, error) {
	return s.listBalanceHistory(ctx, spaceID, nil)
}

func (s *Store) listBalanceHistory(
	ctx context.Context, spaceID SpaceID, through *domain.Date,
) ([]domain.BalancePoint, error) {
	return queryAll(ctx, s.db, "store: list balance history", func(row scanner) (domain.BalancePoint, error) {
		var (
			accountID     uuid.UUID
			asOf          time.Time
			balance       dbconv.Number
			imported      bool
			anchorOn      *time.Time
			anchorBalance dbconv.Number
		)
		if err := row.Scan(
			&accountID, &asOf, &balance, &imported, &anchorOn, &anchorBalance,
		); err != nil {
			return domain.BalancePoint{}, err
		}
		parsed, err := dbconv.ReadMoney(balance, "balance_snapshots.balance")
		if err != nil {
			return domain.BalancePoint{}, err
		}
		point := domain.BalancePoint{
			AccountID: domain.ID(accountID.String()),
			On:        domain.DateOf(asOf),
			Balance:   parsed,
			Observed:  imported,
		}
		if anchorOn != nil {
			anchor, present, err := dbconv.ReadNullMoney(anchorBalance, "anchor_balance")
			if err != nil {
				return domain.BalancePoint{}, err
			}
			if present {
				point.Anchor = domain.BalanceAnchor{On: domain.DateOf(*anchorOn), Balance: anchor}
				point.HasAnchor = true
			}
		}
		return point, nil
	}, `
		SELECT account_id, as_of, balance, is_imported, anchor_on, anchor_balance
		FROM balance_snapshots
		WHERE space_id = $1 AND ($2 IS NULL OR as_of <= $2)
		ORDER BY as_of`,
		spaceID.UUID(), through)
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
	var (
		balanceNumber  dbconv.Number
		asOf, runStart time.Time
	)
	err := s.db.QueryRow(ctx, `
		WITH last_good AS (
			SELECT as_of, balance FROM balance_snapshots
			 WHERE space_id = $1 AND account_id = $2 AND balance <> 0
			 ORDER BY as_of DESC LIMIT 1)
		SELECT lg.balance, lg.as_of,
			COALESCE(
				(SELECT max(as_of) FROM balance_snapshots
				   WHERE space_id = $1 AND account_id = $2 AND balance = 0 AND as_of < lg.as_of),
				(SELECT min(as_of) FROM balance_snapshots
				   WHERE space_id = $1 AND account_id = $2))
		FROM last_good lg`,
		spaceID.UUID(), accountID).Scan(&balanceNumber, &asOf, &runStart)
	if errors.Is(err, sqlitedb.ErrNoRows) {
		return EstablishedBalance{}, false, nil
	}
	if err != nil {
		return EstablishedBalance{}, false, wrap("store: last established balance", err)
	}
	balance, err := dbconv.ReadMoney(balanceNumber, "balance_snapshots.balance")
	if err != nil {
		return EstablishedBalance{}, false, wrap("store: parse established balance", err)
	}
	days := domain.DaysBetween(domain.DateOf(runStart), domain.DateOf(asOf))
	return EstablishedBalance{Balance: balance, AsOf: asOf, Days: days}, true, nil
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

	err = s.InTx(ctx, func(tx *Store) error {
		if _, err := tx.db.Exec(ctx,
			`DELETE FROM balance_snapshots WHERE space_id = $1 AND as_of >= $2 AND as_of <= $3`,
			spaceID.UUID(), from, through); err != nil {
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
	return s.InTx(ctx, func(tx *Store) error {
		if !start.IsZero() {
			if _, err := tx.db.Exec(ctx, `
				DELETE FROM balance_snapshots
				 WHERE space_id = $1 AND account_id = $2 AND NOT is_imported AND as_of < $3`,
				spaceID.UUID(), account.ID, start); err != nil {
				return wrap("store: trim balance history", err)
			}

			points := domain.BalanceHistory(domainAccount, postings, start, through)
			anchor, hasAnchor := domain.AnchorFor(domainAccount, postings, through)
			if err := tx.insertMissingSnapshots(
				ctx, spaceID, account.ID, points, anchor, hasAnchor); err != nil {
				return err
			}
		}
		_, err := tx.db.Exec(ctx,
			`UPDATE accounts SET history_rebuilt_from = $3 WHERE space_id = $1 AND id = $2`,
			spaceID.UUID(), account.ID, dbconv.NullDate(start))
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
	values := make([]domain.Money, 0, len(points))
	for _, point := range points {
		values = append(values, point.Balance)
	}
	balances, err := moneyArray(values)
	if err != nil {
		return wrap("store: fill balance history", err)
	}
	given := make([][3]any, len(points))
	for i, point := range points {
		given[i] = [3]any{uuid.NewString(), point.On.String(), balances[i]}
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO balance_snapshots
			(id, account_id, as_of, balance, space_id, anchor_on, anchor_balance)
		SELECT value ->> 0, $2, value ->> 1, value ->> 2, $1, $4, $5
		  FROM json_each($3)
		 WHERE true
		ON CONFLICT (account_id, as_of) DO NOTHING`,
		spaceID.UUID(), accountID, given,
		anchorDay(anchor, hasAnchor), dbconv.NullMoney(anchor.Balance, hasAnchor))
	return wrap("store: fill balance history", err)
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
	var accountIDs, days []string
	var values []domain.Money
	for _, point := range domain.RederiveHistory(domainAccounts, postings, history) {
		if point.Observed {
			continue
		}
		if was := stored[key{point.AccountID, point.On}]; was.Cmp(point.Balance) == 0 {
			continue
		}
		accountIDs = append(accountIDs, string(point.AccountID))
		days = append(days, point.On.String())
		values = append(values, point.Balance)
	}
	if len(days) == 0 {
		return 0, nil
	}
	balances, err := moneyArray(values)
	if err != nil {
		return 0, wrap("store: rederive balance history", err)
	}
	rows := make([][3]any, len(days))
	for i := range days {
		rows[i] = [3]any{accountIDs[i], days[i], balances[i]}
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE balance_snapshots AS snap
		   SET balance = given.balance, updated_at = now()
		  FROM (SELECT value ->> 0 AS account_id, value ->> 1 AS as_of, value ->> 2 AS balance
		          FROM json_each($2)) AS given
		 WHERE snap.space_id = $1 AND NOT snap.is_imported
		   AND snap.account_id = given.account_id AND snap.as_of = given.as_of`,
		spaceID.UUID(), rows)
	if err != nil {
		return 0, wrap("store: rederive balance history", err)
	}
	return int(tag.RowsAffected()), nil
}
