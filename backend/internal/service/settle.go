package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Ingest is what every path that writes new rows into the ledger runs once
// they are there: a sync, a file import, a merchant's gift card balance and a
// mail rule. Each follower is optional; a nil one is skipped.
type Ingest struct {
	// Merchants matches arriving merchant rows to the orders on file, before
	// the automations that will read the match are queued.
	Merchants *Merchants
	// Automations queues a run for every arriving row an automation asked to
	// be told about.
	Automations *Automations
	// Currency stamps the primary amount on rows that arrived in another
	// currency, before an automation or an alert reads the amount.
	Currency *Currency
}

// IngestReport is what the followers that do not fail an ingest reported.
type IngestReport struct {
	CurrencyStampFailed bool
	// Matched counts the rows that found a merchant order.
	Matched int
}

// AfterIngest settles the rows, then stamps their currency, matches them to
// merchant orders and queues their automations. Only the settle fails the
// call: the rows are in the ledger, and a follower that could not run is
// logged or reported rather than a reason to write them again.
func (in Ingest) AfterIngest(
	ctx context.Context, st *store.Store, spaceID store.SpaceID, ids []uuid.UUID,
) (IngestReport, error) {
	var report IngestReport
	if len(ids) == 0 {
		return report, nil
	}
	if err := settleNewRows(ctx, st, spaceID, ids); err != nil {
		return report, err
	}
	if in.Currency != nil {
		if _, err := in.Currency.StampSpace(ctx, spaceID); err != nil {
			slog.Warn("ingest: stamping the primary currency", "space", spaceID, "error", err)
			report.CurrencyStampFailed = true
		}
	}
	if in.Merchants != nil {
		matched, err := in.Merchants.MatchTransactions(ctx, spaceID, ids)
		if err != nil {
			slog.Warn("ingest: matching merchant orders", "space", spaceID, "error", err)
		}
		report.Matched = matched
	}
	if in.Automations != nil {
		if _, err := in.Automations.EnqueueForTransactions(ctx, spaceID, ids); err != nil {
			slog.Warn("ingest: queueing automations", "space", spaceID, "error", err)
		}
	}
	return report, nil
}

// settleNewRows stamps the cash-flow date, runs the user's rules, matches the
// rows against the bill series, pairs transfers among them, and rewrites the
// running balance of every account they touch.
//
// Pairing is given the new ids explicitly; a nil slice there would make every
// unpaired leg in the ledger a candidate. It takes only synced and imported
// rows (trap 3), so a hand-entered or mailed row passes through untouched.
//
// Not atomic with the insert: each step commits on its own. The
// transactions.needs_settle mark is cleared last, only after every step
// succeeds, so a failure leaves a backlog for SettleBacklog.
func settleNewRows(
	ctx context.Context, st *store.Store, spaceID store.SpaceID, ids []uuid.UUID,
) error {
	if len(ids) == 0 {
		return nil
	}

	accountIDs, err := stampEffectiveDates(ctx, st, spaceID, ids)
	if err != nil {
		return err
	}

	if _, err := NewRules(st).RunRules(ctx, spaceID, ids); err != nil {
		return err
	}

	// Series matching runs after the rules, so renames have landed, and before
	// pairing, so a row the matcher retired is not offered as a transfer leg.
	rows, err := st.ListTransactions(ctx, spaceID, store.TransactionQuery{IDs: ids})
	if err != nil {
		return err
	}
	byID := make(map[uuid.UUID]store.Transaction, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	charges := make([]store.Transaction, 0, len(rows))
	for _, id := range ids {
		if row, ok := byID[id]; ok {
			charges = append(charges, row)
		}
	}
	outcomes, err := NewSeriesMatcher(st).MatchAll(ctx, spaceID, charges)
	if err != nil {
		return err
	}
	retired := map[uuid.UUID]bool{}
	for _, outcome := range outcomes {
		if outcome.RetiredID != uuid.Nil {
			retired[outcome.RetiredID] = true
		}
	}

	candidates := ids
	if len(retired) > 0 {
		candidates = make([]uuid.UUID, 0, len(ids))
		for _, id := range ids {
			if !retired[id] {
				candidates = append(candidates, id)
			}
		}
	}
	if _, err := NewTransfers(st).DetectPairs(ctx, spaceID,
		PairOptions{CandidateIDs: candidates}); err != nil {
		return err
	}

	// Last, because every step above can add or remove rows.
	for _, accountID := range accountIDs {
		if err := RecomputeRunningBalances(ctx, st, spaceID, accountID); err != nil {
			return err
		}
	}

	return st.ClearNeedsSettle(ctx, spaceID, ids)
}

// SettleBacklog runs AfterIngest over every row in a space still marked
// needs_settle and reports how many there were: a row whose settle failed
// never reached the followers either.
func (in Ingest) SettleBacklog(ctx context.Context, st *store.Store, spaceID store.SpaceID) (int, error) {
	ids, err := st.TransactionsNeedingSettle(ctx, spaceID)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if _, err := in.AfterIngest(ctx, st, spaceID, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// stampEffectiveDates derives the cash-flow date of every row that has none,
// and reports the accounts the rows belong to, first seen first. An existing
// value is left alone: it may be a hand correction.
func stampEffectiveDates(
	ctx context.Context, st *store.Store, spaceID store.SpaceID, ids []uuid.UUID,
) ([]uuid.UUID, error) {
	accounts := map[uuid.UUID]store.Account{}
	var order []uuid.UUID

	for _, id := range ids {
		txn, err := st.GetTransaction(ctx, spaceID, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, err
		}
		account, known := accounts[txn.AccountID]
		if !known {
			account, err = st.GetAccount(ctx, spaceID, txn.AccountID)
			if err != nil {
				return nil, err
			}
			accounts[txn.AccountID] = account
			order = append(order, txn.AccountID)
		}
		if !ApplyEffectiveDate(&txn, account, domain.Date{}, false) {
			continue
		}
		if err := st.UpdateTransaction(ctx, spaceID, &txn); err != nil {
			return nil, err
		}
	}
	return order, nil
}
