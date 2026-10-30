package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Transfer pairing writes a shared token on both legs so neither counts as
// income or expense; domain.PlanPairs decides which legs pair.

// pairableSources excludes manual rows, which are never paired behind the
// user's back, and the bookkeeping sources, which move no money.
var pairableSources = map[domain.Source]bool{
	domain.SourceSync:           true,
	domain.SourceFileImport:     true,
	domain.SourceSimplifiImport: true,
}

type Transfers struct{ base }

func NewTransfers(st *store.Store) *Transfers { return &Transfers{newBase(st)} }

type PairOptions struct {
	// CandidateIDs: a pair is proposed only if at least one leg is a
	// candidate. Nil means every leg is a candidate; a non-nil empty slice
	// proposes nothing.
	CandidateIDs []uuid.UUID
	// DateToleranceDays defaults to domain.DefaultDateToleranceDays when zero.
	DateToleranceDays int
}

func (o PairOptions) plan() domain.PairOptions {
	out := domain.PairOptions{DateToleranceDays: o.DateToleranceDays}
	if o.CandidateIDs != nil {
		out.CandidateIDs = make([]domain.ID, len(o.CandidateIDs))
		for i, id := range o.CandidateIDs {
			out.CandidateIDs[i] = domain.ID(id.String())
		}
	}
	return out
}

// loadLegs reads every unpaired, pairable row in the space outside ignored
// accounts, selecting only the columns pairing needs.
func (t *Transfers) loadLegs(
	ctx context.Context, spaceID store.SpaceID, from, to domain.Date,
) ([]domain.TransferLeg, error) {
	db, _, err := t.household(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	sources, args := store.InArgs(pairableSourceList())
	sql := `
		SELECT t.id, t.account_id, t.amount, t.currency, t.date, a.kind
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		WHERE ` + store.MoneyMovedOn("t") + `
		  AND t.transfer_pair_id IS NULL
		  AND a.ignored_at IS NULL
		  AND t.source IN ` + sources
	if !from.IsZero() {
		sql += ` AND t.date >= ? AND t.date <= ?`
		args = append(args, from.String(), to.String())
	}

	rows, err := db.Read().QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("service: load transfer legs: %w", err)
	}
	defer rows.Close()

	var legs []domain.TransferLeg
	for rows.Next() {
		var (
			leg           domain.TransferLeg
			id, accountID uuid.UUID
			amount        int64
			on            string
			kind          string
		)
		if err := rows.Scan(&id, &accountID, &amount, &leg.Currency, &on, &kind); err != nil {
			return nil, fmt.Errorf("service: load transfer legs: %w", err)
		}
		value, err := moneyFromMinor(amount, leg.Currency)
		if err != nil {
			return nil, err
		}
		leg.Amount = value
		leg.ID, leg.AccountID = domain.ID(id.String()), domain.ID(accountID.String())
		leg.On, err = domain.ParseDate(on)
		if err != nil {
			return nil, err
		}
		leg.AccountIsCard = domain.AccountKind(kind) == domain.KindCreditCard
		legs = append(legs, leg)
	}
	return legs, rows.Err()
}

// DetectPairs finds transfer pairs and writes the shared token on both legs.
// It returns the number of pairs created.
func (t *Transfers) DetectPairs(
	ctx context.Context, spaceID store.SpaceID, opts PairOptions,
) (int, error) {
	plan := opts.plan()
	var from, to domain.Date
	if opts.CandidateIDs != nil {
		if len(opts.CandidateIDs) == 0 {
			return 0, nil
		}
		db, _, err := t.household(ctx, spaceID)
		if err != nil {
			return 0, err
		}
		ids, args := store.InArgs(opts.CandidateIDs)
		var earliest, latest sql.Null[string]
		err = db.Read().QueryRowContext(ctx,
			`SELECT min(date), max(date) FROM transactions WHERE id IN `+ids,
			args...).Scan(&earliest, &latest)
		if err != nil {
			return 0, fmt.Errorf("service: candidate date span: %w", err)
		}
		if !earliest.Valid || !latest.Valid {
			return 0, nil
		}
		from = nullDate(earliest).AddDays(-plan.Tolerance())
		to = nullDate(latest).AddDays(plan.Tolerance())
	}

	legs, err := t.loadLegs(ctx, spaceID, from, to)
	if err != nil {
		return 0, err
	}
	proposals := domain.PlanPairs(legs, plan)
	if len(proposals) == 0 {
		return 0, nil
	}

	paired := 0
	err = t.inTx(ctx, spaceID, func(tx *store.Store) error {
		for _, proposal := range proposals {
			_, ok, err := tx.PairTransactions(ctx, spaceID,
				uuid.MustParse(string(proposal.PayingID)), uuid.MustParse(string(proposal.ReceivingID)), false)
			if err != nil {
				return err
			}
			if ok {
				paired++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return paired, nil
}

// UnlinkPair releases both legs of one pair and reports how many rows moved.
func (t *Transfers) UnlinkPair(
	ctx context.Context, spaceID store.SpaceID, pairID uuid.UUID,
) (int, error) {
	return t.store.UnlinkTransferPair(ctx, spaceID, pairID)
}

// FindOrphanPairs returns the ids of legs whose transfer partner no longer
// exists. It writes nothing.
func (t *Transfers) FindOrphanPairs(
	ctx context.Context, spaceID store.SpaceID,
) ([]uuid.UUID, error) {
	// Only the three columns FindOrphanTransferLegs reads, rather than full
	// rows with splits and tags.
	db, _, err := t.household(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	rows, err := db.Read().QueryContext(ctx, `
		SELECT id, transfer_pair_id, is_deleted FROM transactions
		WHERE transfer_pair_id IS NOT NULL ORDER BY date, id`)
	if err != nil {
		return nil, fmt.Errorf("service: load transfer pairs: %w", err)
	}
	defer rows.Close()

	var legs []domain.Transaction
	byID := map[domain.ID]uuid.UUID{}
	for rows.Next() {
		var id, pairID uuid.UUID
		var isDeleted bool
		if err := rows.Scan(&id, &pairID, &isDeleted); err != nil {
			return nil, fmt.Errorf("service: load transfer pairs: %w", err)
		}
		key := domain.ID(id.String())
		byID[key] = id
		legs = append(legs, domain.Transaction{
			ID: key, TransferPairID: domain.ID(pairID.String()), IsDeleted: isDeleted,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("service: load transfer pairs: %w", err)
	}

	orphans := domain.FindOrphanTransferLegs(legs)
	out := make([]uuid.UUID, 0, len(orphans))
	for _, leg := range orphans {
		out = append(out, byID[leg.ID])
	}
	return out, nil
}

// RepairOrphanPairs releases every leg whose partner is gone and returns the
// ids released, putting the survivor back in income and expense.
//
// It also clears series_due_on and the acceptance columns on rows whose
// series_id a hard delete nulled, since no FK cleans those up.
func (t *Transfers) RepairOrphanPairs(
	ctx context.Context, spaceID store.SpaceID,
) ([]uuid.UUID, error) {
	orphans, err := t.FindOrphanPairs(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if len(orphans) > 0 {
		if err := t.store.ReleaseTransferLegs(ctx, spaceID, orphans); err != nil {
			return nil, err
		}
	}

	db, _, err := t.household(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if _, err := db.Write().ExecContext(ctx, `
		UPDATE transactions
		SET series_due_on = NULL, estimate_status = NULL, accepted_on = NULL,
		    expires_on = NULL, updated_at = ?
		WHERE series_id IS NULL AND series_due_on IS NOT NULL`, db.Now()); err != nil {
		return nil, fmt.Errorf("service: repair orphan series links: %w", err)
	}
	return orphans, nil
}

func pairableSourceList() []string {
	out := make([]string, 0, len(pairableSources))
	for source := range pairableSources {
		out = append(out, string(source))
	}
	sort.Strings(out)
	return out
}
