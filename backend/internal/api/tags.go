package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Tags.
//
// Deleting a tag unlinks it from its rows before soft-deleting it; otherwise
// the register renders a blank chip nobody can remove.

func init() {
	Register(Resource{Prefix: "/tags", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listTags)
		rt.Write(http.MethodPost, "/", createTag)
		rt.Read(http.MethodGet, "/{tag_id}", readTag)
		rt.Write(http.MethodPatch, "/{tag_id}", updateTag)
		rt.Write(http.MethodDelete, "/{tag_id}", deleteTag)
	}})
}

type TagResponse struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Color *string   `json:"color"`
}

type TagCreate struct {
	Name  string      `json:"name"`
	Color Opt[string] `json:"color"`
}

type TagUpdate struct {
	Name  Opt[string] `json:"name"`
	Color Opt[string] `json:"color"`
}

func listTags(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListTags(r.Context(), sp.ID(), false)
	if err != nil {
		return err
	}
	out := make([]TagResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, tagResponse(row))
	}
	return writeJSON(w, http.StatusOK, out)
}

func createTag(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body TagCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Name == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if err := checkTagNameFree(env, r, sp, body.Name, uuid.Nil); err != nil {
		return err
	}

	tag := &store.Tag{Name: body.Name}
	applyNullable(body.Color, &tag.Color)
	if err := env.DB.CreateTag(r.Context(), sp.ID(), tag); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, tagResponse(*tag))
}

func readTag(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	tag, err := liveTag(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, tagResponse(tag))
}

func updateTag(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	tag, err := liveTag(r, env, sp)
	if err != nil {
		return err
	}
	var body TagUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Name.Present() && body.Name.Value != tag.Name {
		if err := checkTagNameFree(env, r, sp, body.Name.Value, tag.ID); err != nil {
			return err
		}
	}
	if err := applyRequired("name", body.Name, &tag.Name); err != nil {
		return err
	}
	applyNullable(body.Color, &tag.Color)

	if err := env.DB.UpdateTag(r.Context(), sp.ID(), &tag); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, tagResponse(tag))
}

func deleteTag(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	tag, err := liveTag(r, env, sp)
	if err != nil {
		return err
	}
	if err := unlinkTag(env, r, sp, tag.ID); err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteTag(r.Context(), sp.ID(), tag.ID), "Tag")
}

// unlinkTag takes a tag off every row and split that carries it, by a
// whole-space scan: internal/store has no "rows carrying this tag" query.
func unlinkTag(env *Env, r *http.Request, sp auth.SpaceContext, tagID uuid.UUID) error {
	// Estimates too: a projected occurrence can carry the tag, and a chip a
	// delete cannot reach is a chip that outlives its tag.
	rows, err := env.DB.ListTransactions(r.Context(), sp.ID(),
		store.TransactionQuery{IncludeDeleted: true, IncludeEstimates: true})
	if err != nil {
		return err
	}
	for _, row := range rows {
		remaining, changed := withoutTag(row.TagIDs, tagID)
		if changed {
			if err := env.DB.SetTransactionTags(r.Context(), sp.ID(), row.ID, remaining); err != nil {
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
			if err := env.DB.ReplaceSplits(r.Context(), sp.ID(), &row); err != nil {
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

func liveTag(r *http.Request, env *Env, sp auth.SpaceContext) (store.Tag, error) {
	tag, err := fromPath(r, sp, "tag_id", "Tag", env.DB.GetTag)
	if err != nil {
		return store.Tag{}, err
	}
	if tag.IsDeleted {
		return store.Tag{}, errNotFound("Tag")
	}
	return tag, nil
}

// checkTagNameFree reads deleted rows too: the unique constraint spans them.
func checkTagNameFree(env *Env, r *http.Request, sp auth.SpaceContext, name string, self uuid.UUID) error {
	rows, err := env.DB.ListTags(r.Context(), sp.ID(), true)
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

func tagResponse(t store.Tag) TagResponse {
	return TagResponse{ID: t.ID, Name: t.Name, Color: pgconv.NullText(t.Color)}
}
