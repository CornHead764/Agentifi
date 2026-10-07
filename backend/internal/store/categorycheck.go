// The category check, as the register reads it. Whether a check is in flight
// is the run table's business; whether the last one determined anything is
// written on the transaction, so the filter evaluator (domain.Facets, built
// from the row alone) can see it.

package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PendingCategoryChecks is which of these rows have a real category check
// queued or running. Dry and blind runs change nothing, so they show no
// spinner.
func (s *Store) PendingCategoryChecks(
	ctx context.Context, spaceID SpaceID, ids []uuid.UUID,
) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	pending, err := queryAll(ctx, s.db, "store: pending category checks", scanValue[uuid.UUID],
		`SELECT DISTINCT transaction_id
		   FROM assistant_automation_runs
		  WHERE space_id = $1 AND transaction_id = ANY($2)
		    AND status IN ('queued', 'running')
		    AND NOT dry_run AND NOT blind`,
		spaceID.UUID(), ids)
	if err != nil {
		return nil, err
	}
	for _, id := range pending {
		out[id] = true
	}
	return out, nil
}

// needsCategorySQL is domain.Transaction.IsUncategorized over the transactions
// row aliased `t`: an unpaired row with no category and no splits, or one with
// a split that has none. A split row's parent never carries a category, so
// `category_id IS NULL` alone is true of every split row.
const needsCategorySQL = `(t.transfer_pair_id IS NULL AND (
	(t.category_id IS NULL AND NOT EXISTS (
		SELECT 1 FROM transaction_splits s WHERE s.transaction_id = t.id))
	OR EXISTS (
		SELECT 1 FROM transaction_splits s WHERE s.transaction_id = t.id AND s.category_id IS NULL)))`

// MarkCategoryUndetermined records that a check finished on this row and filed
// nothing. `note` is the reason a person sees; `runID` lets the cell open the
// run's detail. Only a still-uncategorized row takes the mark, or a correctly
// filed row would show "could not place it" the day its category is cleared.
func (s *Store) MarkCategoryUndetermined(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, at time.Time, note string, runID uuid.UUID,
) error {
	_, err := s.db.Exec(ctx,
		`UPDATE transactions t
		    SET category_checked_at = $3, category_check_note = $4, category_check_run_id = $5
		  WHERE t.space_id = $1 AND t.id = $2 AND `+needsCategorySQL,
		spaceID.UUID(), id, at, note, runID)
	return wrap("store: mark category undetermined", err)
}

// ClearCategoryCheck drops the undetermined verdict and stamps the run that
// reached a category. Called on every finished run that filed or proposed one,
// marked or not.
func (s *Store) ClearCategoryCheck(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, runID uuid.UUID,
) error {
	_, err := s.db.Exec(ctx,
		`UPDATE transactions
		    SET category_checked_at = NULL, category_check_note = '', category_check_run_id = $3
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, runID)
	return wrap("store: clear category check", err)
}
