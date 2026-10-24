package store

import (
	"context"
	"database/sql"
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

const suggestionBatchColumns = `id, created_by, row_count, created_at, cancelled_at, dismissed_at`

func scanSuggestionBatch(row scanner) (SuggestionBatch, error) {
	var one SuggestionBatch
	var created int64
	var cancelled, dismissed sql.Null[int64]
	err := row.Scan(&one.ID, &one.CreatedBy, &one.RowCount, &created, &cancelled, &dismissed)
	one.CreatedAt = fromUnixMillis(created)
	one.CancelledAt, one.DismissedAt = nullableTime(cancelled), nullableTime(dismissed)
	return one, err
}

func (s *Store) CreateSuggestionBatch(ctx context.Context, spaceID SpaceID, one *SuggestionBatch) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `INSERT INTO category_suggestion_batches (id, created_by, row_count, created_at) VALUES (?, ?, ?, ?)`, one.ID, one.CreatedBy, one.RowCount, now)
	if err == nil {
		one.CreatedAt = fromUnixMillis(now)
	}
	return wrap("store: create suggestion batch", err)
}

func (s *Store) GetSuggestionBatch(ctx context.Context, spaceID SpaceID, id uuid.UUID) (SuggestionBatch, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return SuggestionBatch{}, err
	}
	one, err := scanSuggestionBatch(db.Read().QueryRowContext(ctx, `SELECT `+suggestionBatchColumns+` FROM category_suggestion_batches WHERE id = ?`, id))
	one.SpaceID = spaceID
	return one, wrap("store: suggestion batch", err)
}

// LatestSuggestionBatch is the space's newest batch of at least minRows rows;
// ErrNotFound for none. A dismissed one is still returned: a newer dismissal
// does not bring an older summary back.
func (s *Store) LatestSuggestionBatch(ctx context.Context, spaceID SpaceID, minRows int) (SuggestionBatch, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return SuggestionBatch{}, err
	}
	one, err := scanSuggestionBatch(db.Read().QueryRowContext(ctx, `SELECT `+suggestionBatchColumns+` FROM category_suggestion_batches WHERE row_count >= ? ORDER BY created_at DESC, id DESC LIMIT 1`, minRows))
	one.SpaceID = spaceID
	return one, wrap("store: latest suggestion batch", err)
}

// SuggestionBatchRuns is every run the batch queued that is still about a
// row. A run whose row was deleted is left out, so the row counts as not run.
func (s *Store) SuggestionBatchRuns(
	ctx context.Context, spaceID SpaceID, batchID uuid.UUID,
) ([]domain.SuggestionBatchRun, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: suggestion batch runs",
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
		  WHERE batch_id = ? AND transaction_id IS NOT NULL`, batchID)
}

// CancelSuggestionBatch removes the batch's runs that have not started and
// marks it cancelled; a run already under way finishes. Reports how many
// were removed.
func (s *Store) CancelSuggestionBatch(ctx context.Context, spaceID SpaceID, id uuid.UUID) (int, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	removed := 0
	err = h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if err := sqliteExecOne(ctx, db.Write(), "store: cancel suggestion batch",
			`UPDATE category_suggestion_batches SET cancelled_at = COALESCE(cancelled_at, ?) WHERE id = ?`, db.Now(), id); err != nil {
			return err
		}
		tag, err := db.Write().ExecContext(ctx, `DELETE FROM assistant_automation_runs WHERE batch_id = ? AND status = ?`, id, domain.AutomationRunQueued)
		if err != nil {
			return wrap("store: cancel suggestion batch runs", err)
		}
		n, err := tag.RowsAffected()
		removed = int(n)
		return wrap("store: cancel suggestion batch runs", err)
	})
	return removed, err
}

// DeleteSuggestionBatch removes a batch that queued nothing.
func (s *Store) DeleteSuggestionBatch(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `DELETE FROM category_suggestion_batches WHERE id = ?`, id)
	return wrap("store: delete suggestion batch", err)
}

func (s *Store) DismissSuggestionBatch(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: dismiss suggestion batch",
		`UPDATE category_suggestion_batches SET dismissed_at = COALESCE(dismissed_at, ?) WHERE id = ?`, db.Now(), id)
}
