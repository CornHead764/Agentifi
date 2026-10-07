package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A "Suggest categories" request, followed after it was queued: how far its
// runs have got, and, over rows that already had a category, how often the
// check named the same one. POST /assistant-automations/fire makes one.
func init() {
	Register(Resource{Prefix: "/category-suggestion-batches", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/latest", readLatestSuggestionBatch)
		rt.Read(http.MethodGet, "/{batch_id}", readSuggestionBatch)
		rt.Write(http.MethodPost, "/{batch_id}/cancel", cancelSuggestionBatch)
		rt.Write(http.MethodPost, "/{batch_id}/dismiss", dismissSuggestionBatch)
	}})
}

// followedBatchRows is the smallest batch the register follows: one row's own
// spinner already says how it is going.
const followedBatchRows = 2

// SuggestionBatchResponse is a batch's progress and its comparison, from
// domain.SummarizeSuggestionBatch. Reviewed and Unreviewed are the rows'
// review state when the batch was queued.
type SuggestionBatchResponse struct {
	ID         uuid.UUID              `json:"id"`
	CreatedAt  time.Time              `json:"created_at"`
	Cancelled  bool                   `json:"cancelled"`
	Dismissed  bool                   `json:"dismissed"`
	Rows       int                    `json:"rows"`
	Done       int                    `json:"done"`
	Pending    int                    `json:"pending"`
	NotRun     int                    `json:"not_run"`
	Finished   bool                   `json:"finished"`
	Reviewed   domain.SuggestionTally `json:"reviewed"`
	Unreviewed domain.SuggestionTally `json:"unreviewed"`
}

// LatestSuggestionBatchResponse wraps the batch the register shows, null when
// there is none or it was put away.
type LatestSuggestionBatchResponse struct {
	Batch *SuggestionBatchResponse `json:"batch"`
}

func suggestionBatchResponse(
	env *Env, r *http.Request, sp auth.SpaceContext, batch store.SuggestionBatch,
) (SuggestionBatchResponse, error) {
	runs, err := env.DB.SuggestionBatchRuns(r.Context(), sp.ID(), batch.ID)
	if err != nil {
		return SuggestionBatchResponse{}, err
	}
	summary := domain.SummarizeSuggestionBatch(batch.RowCount, runs)
	return SuggestionBatchResponse{
		ID: batch.ID, CreatedAt: batch.CreatedAt,
		Cancelled: batch.CancelledAt != nil, Dismissed: batch.DismissedAt != nil,
		Rows: summary.Rows, Done: summary.Done(), Pending: summary.Pending, NotRun: summary.NotRun,
		Finished: summary.Finished(), Reviewed: summary.Reviewed, Unreviewed: summary.Unreviewed,
	}, nil
}

func readLatestSuggestionBatch(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	batch, err := env.DB.LatestSuggestionBatch(r.Context(), sp.ID(), followedBatchRows)
	if errors.Is(err, store.ErrNotFound) || (err == nil && batch.DismissedAt != nil) {
		return writeJSON(w, http.StatusOK, LatestSuggestionBatchResponse{})
	}
	if err != nil {
		return err
	}
	out, err := suggestionBatchResponse(env, r, sp, batch)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, LatestSuggestionBatchResponse{Batch: &out})
}

func suggestionBatchFromPath(env *Env, r *http.Request, sp auth.SpaceContext) (store.SuggestionBatch, error) {
	id, err := pathUUID(r, "batch_id", "Suggestion batch")
	if err != nil {
		return store.SuggestionBatch{}, err
	}
	batch, err := env.DB.GetSuggestionBatch(r.Context(), sp.ID(), id)
	return batch, notFoundAs(err, "Suggestion batch")
}

func readSuggestionBatch(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	batch, err := suggestionBatchFromPath(env, r, sp)
	if err != nil {
		return err
	}
	out, err := suggestionBatchResponse(env, r, sp, batch)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// cancelSuggestionBatch drops the runs that have not started; the ones under
// way finish, and what they found still counts.
func cancelSuggestionBatch(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	batch, err := suggestionBatchFromPath(env, r, sp)
	if err != nil {
		return err
	}
	if _, err := env.DB.CancelSuggestionBatch(r.Context(), sp.ID(), batch.ID); err != nil {
		return err
	}
	if batch, err = env.DB.GetSuggestionBatch(r.Context(), sp.ID(), batch.ID); err != nil {
		return err
	}
	out, err := suggestionBatchResponse(env, r, sp, batch)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func dismissSuggestionBatch(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	batch, err := suggestionBatchFromPath(env, r, sp)
	if err != nil {
		return err
	}
	if err := env.DB.DismissSuggestionBatch(r.Context(), sp.ID(), batch.ID); err != nil {
		return err
	}
	return writeNoContent(w)
}
