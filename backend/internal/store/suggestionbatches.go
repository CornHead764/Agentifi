package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// SuggestionBatch is one "Suggest categories" request. Its progress and its
// comparison are counted from its runs (domain.SummarizeSuggestionBatch).
type SuggestionBatch struct {
	ID        uuid.UUID
	SpaceID   SpaceID
	CreatedBy uuid.UUID
	// RowCount is how many rows the request named, which a run displaced by a
	// later request for the same row does not reduce.
	RowCount    int
	CreatedAt   time.Time
	CancelledAt *time.Time
	// DismissedAt is the household putting the finished summary away.
	DismissedAt *time.Time
}

const suggestionBatchColumns = `id, space_id, created_by, row_count, created_at, cancelled_at,
	dismissed_at`

func scanSuggestionBatch(row scanner) (SuggestionBatch, error) {
	var (
		one     SuggestionBatch
		spaceID uuid.UUID
	)
	err := row.Scan(&one.ID, &spaceID, &one.CreatedBy, &one.RowCount, &one.CreatedAt,
		&one.CancelledAt, &one.DismissedAt)
	one.SpaceID = SpaceIDOf(spaceID)
	return one, err
}

func (s *Store) CreateSuggestionBatch(ctx context.Context, spaceID SpaceID, one *SuggestionBatch) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	err := s.db.QueryRow(ctx,
		`INSERT INTO category_suggestion_batches (id, space_id, created_by, row_count)
		 VALUES ($1, $2, $3, $4)
		 RETURNING created_at`,
		one.ID, spaceID.UUID(), one.CreatedBy, one.RowCount).Scan(&one.CreatedAt)
	return wrap("store: create suggestion batch", err)
}

func (s *Store) GetSuggestionBatch(ctx context.Context, spaceID SpaceID, id uuid.UUID) (SuggestionBatch, error) {
	one, err := scanSuggestionBatch(s.db.QueryRow(ctx,
		`SELECT `+suggestionBatchColumns+` FROM category_suggestion_batches
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id))
	return one, wrap("store: suggestion batch", err)
}

// LatestSuggestionBatch is the space's newest batch of at least minRows rows;
// ErrNotFound for none. A dismissed one is still returned: a newer dismissal
// does not bring an older summary back.
func (s *Store) LatestSuggestionBatch(
	ctx context.Context, spaceID SpaceID, minRows int,
) (SuggestionBatch, error) {
	one, err := scanSuggestionBatch(s.db.QueryRow(ctx,
		`SELECT `+suggestionBatchColumns+` FROM category_suggestion_batches
		  WHERE space_id = $1 AND row_count >= $2
		  ORDER BY created_at DESC, id DESC LIMIT 1`, spaceID.UUID(), minRows))
	return one, wrap("store: latest suggestion batch", err)
}

// SuggestionBatchRuns is every run the batch queued that is still about a
// row. A run whose row was deleted is left out, so the row counts as not run.
func (s *Store) SuggestionBatchRuns(
	ctx context.Context, spaceID SpaceID, batchID uuid.UUID,
) ([]domain.SuggestionBatchRun, error) {
	return queryAll(ctx, s.db, "store: suggestion batch runs",
		func(row scanner) (domain.SuggestionBatchRun, error) {
			var (
				one         domain.SuggestionBatchRun
				transaction uuid.UUID
				result      string
			)
			err := row.Scan(&transaction, &one.Reviewed, &one.Status, &result)
			one.TransactionID = domain.ID(transaction.String())
			one.Result = domain.CategoryCheckResult(result)
			return one, err
		},
		`SELECT transaction_id, reviewed_when_queued, status, category_result
		   FROM assistant_automation_runs
		  WHERE space_id = $1 AND batch_id = $2 AND transaction_id IS NOT NULL`,
		spaceID.UUID(), batchID)
}

// CancelSuggestionBatch removes the batch's runs that have not started and
// marks it cancelled; a run already under way finishes. Reports how many
// were removed.
func (s *Store) CancelSuggestionBatch(ctx context.Context, spaceID SpaceID, id uuid.UUID) (int, error) {
	removed := 0
	err := s.InTx(ctx, func(tx *Store) error {
		if err := tx.execOne(ctx, "store: cancel suggestion batch",
			`UPDATE category_suggestion_batches SET cancelled_at = COALESCE(cancelled_at, now())
			  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id); err != nil {
			return err
		}
		tag, err := tx.db.Exec(ctx,
			`DELETE FROM assistant_automation_runs
			  WHERE space_id = $1 AND batch_id = $2 AND status = $3`,
			spaceID.UUID(), id, domain.AutomationRunQueued)
		if err != nil {
			return wrap("store: cancel suggestion batch runs", err)
		}
		removed = int(tag.RowsAffected())
		return nil
	})
	return removed, err
}

// DeleteSuggestionBatch removes a batch that queued nothing.
func (s *Store) DeleteSuggestionBatch(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM category_suggestion_batches WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
	return wrap("store: delete suggestion batch", err)
}

func (s *Store) DismissSuggestionBatch(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: dismiss suggestion batch",
		`UPDATE category_suggestion_batches SET dismissed_at = COALESCE(dismissed_at, now())
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
}
