package api

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Tags.
//
// Deleting a tag unlinks it from its rows before soft-deleting it; otherwise
// the register renders a blank chip nobody can remove.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewTagServiceHandler(tagService{env}, opts...)
	})
}

type tagService struct{ env *Env }

// TagResponse is a tag as the REST routes that embed one (/unused) write it.
type TagResponse struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Color *string   `json:"color"`
}

func (s tagService) ListTags(ctx context.Context, _ *agentifiv1.ListTagsRequest) (*agentifiv1.ListTagsResponse, error) {
	rows, err := s.env.DB.ListTags(ctx, spaceFrom(ctx).ID(), false)
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListTagsResponse{Tags: make([]*agentifiv1.Tag, 0, len(rows))}
	for _, row := range rows {
		out.Tags = append(out.Tags, tagProto(row))
	}
	return out, nil
}

func (s tagService) CreateTag(ctx context.Context, req *agentifiv1.CreateTagRequest) (*agentifiv1.CreateTagResponse, error) {
	sp := spaceFrom(ctx)
	if req.GetName() == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if err := checkTagNameFree(ctx, s.env, sp, req.GetName(), uuid.Nil); err != nil {
		return nil, err
	}

	tag := &store.Tag{Name: req.GetName(), Color: req.GetColor()}
	if err := s.env.DB.CreateTag(ctx, sp.ID(), tag); err != nil {
		return nil, err
	}
	return &agentifiv1.CreateTagResponse{Tag: tagProto(*tag)}, nil
}

func (s tagService) GetTag(ctx context.Context, req *agentifiv1.GetTagRequest) (*agentifiv1.GetTagResponse, error) {
	tag, err := liveTag(ctx, s.env, spaceFrom(ctx), req.GetTagId())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetTagResponse{Tag: tagProto(tag)}, nil
}

func (s tagService) UpdateTag(ctx context.Context, req *agentifiv1.UpdateTagRequest) (*agentifiv1.UpdateTagResponse, error) {
	sp := spaceFrom(ctx)
	tag, err := liveTag(ctx, s.env, sp, req.GetTagId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	name := optOf(mask, "name", req.Name)
	if name.Present() && name.Value != tag.Name {
		if err := checkTagNameFree(ctx, s.env, sp, name.Value, tag.ID); err != nil {
			return nil, err
		}
	}
	if err := applyRequired("name", name, &tag.Name); err != nil {
		return nil, err
	}
	applyNullable(optOf(mask, "color", req.Color), &tag.Color)

	if err := s.env.DB.UpdateTag(ctx, sp.ID(), &tag); err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateTagResponse{Tag: tagProto(tag)}, nil
}

func (s tagService) DeleteTag(ctx context.Context, req *agentifiv1.DeleteTagRequest) (*agentifiv1.DeleteTagResponse, error) {
	sp := spaceFrom(ctx)
	tag, err := liveTag(ctx, s.env, sp, req.GetTagId())
	if err != nil {
		return nil, err
	}
	if err := unlinkTag(ctx, s.env, sp, tag.ID); err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteTag(ctx, sp.ID(), tag.ID); err != nil {
		return nil, notFoundAs(err, "Tag")
	}
	return &agentifiv1.DeleteTagResponse{}, nil
}

// unlinkTag takes a tag off every row and split that carries it, by a
// whole-space scan: internal/store has no "rows carrying this tag" query.
func unlinkTag(ctx context.Context, env *Env, sp auth.SpaceContext, tagID uuid.UUID) error {
	// Estimates too: a projected occurrence can carry the tag, and a chip a
	// delete cannot reach is a chip that outlives its tag.
	rows, err := env.DB.ListTransactions(ctx, sp.ID(),
		store.TransactionQuery{IncludeDeleted: true, IncludeEstimates: true})
	if err != nil {
		return err
	}
	for _, row := range rows {
		remaining, changed := withoutTag(row.TagIDs, tagID)
		if changed {
			if err := env.DB.SetTransactionTags(ctx, sp.ID(), row.ID, remaining); err != nil {
				return err
			}
		}
		splitsChanged := false
		for i := range row.Splits {
			if kept, dropped := withoutTag(row.Splits[i].TagIDs, tagID); dropped {
				row.Splits[i].TagIDs = kept
				splitsChanged = true
			}
		}
		if splitsChanged {
			if err := env.DB.ReplaceSplits(ctx, sp.ID(), &row); err != nil {
				return err
			}
		}
	}
	return nil
}

func withoutTag(ids []uuid.UUID, drop uuid.UUID) ([]uuid.UUID, bool) {
	kept := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id != drop {
			kept = append(kept, id)
		}
	}
	return kept, len(kept) != len(ids)
}

func liveTag(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (store.Tag, error) {
	id, err := idFrom(rawID, "Tag")
	if err != nil {
		return store.Tag{}, err
	}
	tag, err := env.DB.GetTag(ctx, sp.ID(), id)
	if err != nil {
		return store.Tag{}, notFoundAs(err, "Tag")
	}
	if tag.IsDeleted {
		return store.Tag{}, errNotFound("Tag")
	}
	return tag, nil
}

// checkTagNameFree reads deleted rows too: the unique constraint spans them.
func checkTagNameFree(ctx context.Context, env *Env, sp auth.SpaceContext, name string, self uuid.UUID) error {
	rows, err := env.DB.ListTags(ctx, sp.ID(), true)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Name == name && row.ID != self {
			return errConflict("a tag named %q already exists", name)
		}
	}
	return nil
}

func tagProto(t store.Tag) *agentifiv1.Tag {
	return &agentifiv1.Tag{Id: t.ID.String(), Name: t.Name, Color: dbconv.NullText(t.Color)}
}

func tagResponse(t store.Tag) TagResponse {
	return TagResponse{ID: t.ID, Name: t.Name, Color: dbconv.NullText(t.Color)}
}
