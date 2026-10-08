package api

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A "Suggest categories" request, followed after it was queued: how far its
// runs have got, and, over rows that already had a category, how often the
// check named the same one. POST /assistant-automations/fire makes one.
func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewSuggestionBatchServiceHandler(suggestionBatchService{env}, opts...)
	})
}

type suggestionBatchService struct{ env *Env }

// followedBatchRows is the smallest batch the register follows: one row's own
// spinner already says how it is going.
const followedBatchRows = 2

func (s suggestionBatchService) GetLatestSuggestionBatch(
	ctx context.Context, _ *agentifiv1.GetLatestSuggestionBatchRequest,
) (*agentifiv1.GetLatestSuggestionBatchResponse, error) {
	batch, err := s.env.DB.LatestSuggestionBatch(ctx, spaceFrom(ctx).ID(), followedBatchRows)
	if errors.Is(err, store.ErrNotFound) || (err == nil && batch.DismissedAt != nil) {
		return &agentifiv1.GetLatestSuggestionBatchResponse{}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.batchProto(ctx, batch)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetLatestSuggestionBatchResponse{Batch: out}, nil
}

func (s suggestionBatchService) GetSuggestionBatch(
	ctx context.Context, req *agentifiv1.GetSuggestionBatchRequest,
) (*agentifiv1.GetSuggestionBatchResponse, error) {
	batch, err := s.load(ctx, req.GetBatchId())
	if err != nil {
		return nil, err
	}
	out, err := s.batchProto(ctx, batch)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetSuggestionBatchResponse{Batch: out}, nil
}

// CancelSuggestionBatch drops the runs that have not started; the ones under
// way finish, and what they found still counts.
func (s suggestionBatchService) CancelSuggestionBatch(
	ctx context.Context, req *agentifiv1.CancelSuggestionBatchRequest,
) (*agentifiv1.CancelSuggestionBatchResponse, error) {
	sp := spaceFrom(ctx)
	batch, err := s.load(ctx, req.GetBatchId())
	if err != nil {
		return nil, err
	}
	if _, err := s.env.DB.CancelSuggestionBatch(ctx, sp.ID(), batch.ID); err != nil {
		return nil, err
	}
	if batch, err = s.env.DB.GetSuggestionBatch(ctx, sp.ID(), batch.ID); err != nil {
		return nil, err
	}
	out, err := s.batchProto(ctx, batch)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CancelSuggestionBatchResponse{Batch: out}, nil
}

func (s suggestionBatchService) DismissSuggestionBatch(
	ctx context.Context, req *agentifiv1.DismissSuggestionBatchRequest,
) (*agentifiv1.DismissSuggestionBatchResponse, error) {
	batch, err := s.load(ctx, req.GetBatchId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DismissSuggestionBatch(ctx, spaceFrom(ctx).ID(), batch.ID); err != nil {
		return nil, err
	}
	return &agentifiv1.DismissSuggestionBatchResponse{}, nil
}

func (s suggestionBatchService) load(ctx context.Context, rawID string) (store.SuggestionBatch, error) {
	id, err := idFrom(rawID, "Suggestion batch")
	if err != nil {
		return store.SuggestionBatch{}, err
	}
	batch, err := s.env.DB.GetSuggestionBatch(ctx, spaceFrom(ctx).ID(), id)
	return batch, notFoundAs(err, "Suggestion batch")
}

// batchProto is a batch's progress and its comparison, from
// domain.SummarizeSuggestionBatch.
func (s suggestionBatchService) batchProto(
	ctx context.Context, batch store.SuggestionBatch,
) (*agentifiv1.SuggestionBatch, error) {
	runs, err := s.env.DB.SuggestionBatchRuns(ctx, spaceFrom(ctx).ID(), batch.ID)
	if err != nil {
		return nil, err
	}
	summary := domain.SummarizeSuggestionBatch(batch.RowCount, runs)
	return &agentifiv1.SuggestionBatch{
		Id:         batch.ID.String(),
		CreatedAt:  timestamppb.New(batch.CreatedAt),
		Cancelled:  batch.CancelledAt != nil,
		Dismissed:  batch.DismissedAt != nil,
		Rows:       int32(summary.Rows),
		Done:       int32(summary.Done()),
		Pending:    int32(summary.Pending),
		NotRun:     int32(summary.NotRun),
		Finished:   summary.Finished(),
		Reviewed:   suggestionTallyProto(summary.Reviewed),
		Unreviewed: suggestionTallyProto(summary.Unreviewed),
	}, nil
}

func suggestionTallyProto(t domain.SuggestionTally) *agentifiv1.SuggestionTally {
	return &agentifiv1.SuggestionTally{
		Agreed: int32(t.Agreed), Differs: int32(t.Differs), Unsure: int32(t.Unsure),
		Suggested: int32(t.Suggested), Undetermined: int32(t.Undetermined),
		Skipped: int32(t.Skipped), Failed: int32(t.Failed),
	}
}
