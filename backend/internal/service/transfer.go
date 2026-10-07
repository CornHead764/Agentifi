package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
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
	sql := `
		SELECT t.id, t.account_id, t.amount, t.currency, t.date, a.kind
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		WHERE t.space_id = $1
		  AND ` + store.MoneyMovedOn("t") + `
		  AND t.transfer_pair_id IS NULL
		  AND a.ignored_at IS NULL
		  AND t.source = ANY($2)`
	args := []any{spaceID.UUID(), pairableSourceList()}
	if !from.IsZero() {
		sql += ` AND t.date >= $3 AND t.date <= $4`
		args = append(args, from.Time(), to.Time())
	}

	rows, err := t.conn().Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("service: load transfer legs: %w", err)
	}
	defer rows.Close()

	var legs []domain.TransferLeg
	for rows.Next() {
		var (
			leg           domain.TransferLeg
			id, accountID uuid.UUID
			amount        pgtype.Numeric
			on            time.Time
			kind          string
		)
		if err := rows.Scan(&id, &accountID, &amount, &leg.Currency, &on, &kind); err != nil {
			return nil, fmt.Errorf("service: load transfer legs: %w", err)
		}
		if leg.Amount, err = pgconv.ReadMoney(amount, "transactions.amount"); err != nil {
			return nil, err
		}
		leg.ID, leg.AccountID = domain.ID(id.String()), domain.ID(accountID.String())
		leg.On = domain.DateOf(on)
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
		var earliest, latest *time.Time
		err := t.conn().QueryRow(ctx,
			`SELECT min(date), max(date) FROM transactions WHERE space_id = $1 AND id = ANY($2)`,
			spaceID.UUID(), opts.CandidateIDs).Scan(&earliest, &latest)
		if err != nil {
			return 0, fmt.Errorf("service: candidate date span: %w", err)
		}
		if earliest == nil || latest == nil {
			return 0, nil
		}
		// Nothing outside the candidates' dates plus the tolerance can pair
		// with them.
		from = domain.DateOf(*earliest).AddDays(-plan.Tolerance())
		to = domain.DateOf(*latest).AddDays(plan.Tolerance())
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
	err = t.inTx(ctx, func(tx *store.Store) error {
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
	rows, err := t.conn().Query(ctx, `
		SELECT id, transfer_pair_id, is_deleted FROM transactions
		WHERE space_id = $1 AND transfer_pair_id IS NOT NULL
		ORDER BY date, id`, spaceID.UUID())
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

	if _, err := t.conn().Exec(ctx, `
		UPDATE transactions
		SET series_due_on = NULL, estimate_status = NULL, accepted_on = NULL,
		    expires_on = NULL, updated_at = now()
		WHERE space_id = $1 AND series_id IS NULL AND series_due_on IS NOT NULL`,
		spaceID.UUID()); err != nil {
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
