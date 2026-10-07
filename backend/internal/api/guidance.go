package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Guidance: what the household tells the assistant that the ledger cannot, such
// as "a small charge here is a sandwich and a large one is fuel". A note is
// handed to the model when it is about to decide a row the note is about.
//
// The conditions are a filter (ground rule 3), written through the rules
// resource's own buildRuleItems, so what a rule refuses is refused here too:
// an empty clause list, which the evaluator reads as matching nothing, and the
// bill/subscription facet.
//
// A note is words, never an action: nothing here parses or acts on it; the
// model reads it beside the facts and decides.

func init() {
	Register(Resource{Prefix: "/guidance", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listGuidance)
		rt.Write(http.MethodPost, "/", createGuidance)
		rt.Write(http.MethodPost, "/reorder", reorderGuidance)
		rt.Read(http.MethodGet, "/{guidance_id}", readGuidance)
		rt.Write(http.MethodPatch, "/{guidance_id}", updateGuidance)
		rt.Write(http.MethodDelete, "/{guidance_id}", deleteGuidance)
	}})
}

// GuidanceFilterScope marks the filters this resource creates and owns.
const GuidanceFilterScope = "guidance"

// maxGuidanceInstruction bounds one note: a paragraph of household rules, short
// of a rewrite of the automation's prompt.
const maxGuidanceInstruction = 4000

// --- Wire types --------------------------------------------------------------

type GuidanceResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Instruction string    `json:"instruction"`
	// Filter is inlined so the list can render every note's chips without a
	// fetch per row.
	FilterID uuid.UUID      `json:"filter_id"`
	Filter   FilterResponse `json:"filter"`
	// OwnsFilter reports a filter this note created and may edit in place. A
	// shared one is edited through /filters.
	OwnsFilter bool `json:"owns_filter"`
	// AppliesTo is the conditions in words — the same sentence the model is
	// shown, so the list and the prompt cannot describe a note differently.
	AppliesTo string `json:"applies_to"`
	IsActive  bool   `json:"is_active"`
	Position  int    `json:"position"`
}

type GuidanceCreate struct {
	Name        string `json:"name"`
	Instruction string `json:"instruction"`
	// FilterID points at an existing saved filter; Conditions is the builder's
	// own clause list. Exactly one of the two, as on a rule.
	FilterID   *uuid.UUID        `json:"filter_id"`
	Conditions []FilterItemWrite `json:"conditions"`
	IsActive   *bool             `json:"is_active"`
}

type GuidanceUpdate struct {
	Name        Opt[string]    `json:"name"`
	Instruction Opt[string]    `json:"instruction"`
	FilterID    Opt[uuid.UUID] `json:"filter_id"`
	// Conditions replaces the note's own clause list. Absent leaves it alone;
	// an empty list is refused, as on a rule.
	Conditions *[]FilterItemWrite `json:"conditions"`
	IsActive   Opt[bool]          `json:"is_active"`
}

// GuidanceReorder is the whole live set in order. Partial lists are refused, as
// on the rules reorder: renumbering half leaves an order nobody chose.
type GuidanceReorder struct {
	GuidanceIDs []uuid.UUID `json:"guidance_ids"`
}

// --- Handlers ----------------------------------------------------------------

func listGuidance(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	notes, err := env.DB.ListGuidance(r.Context(), sp.ID(), store.GuidanceQuery{})
	if err != nil {
		return err
	}
	names, err := filterValueNames(r.Context(), env, sp)
	if err != nil {
		return err
	}
	out := make([]GuidanceResponse, 0, len(notes))
	for _, note := range notes {
		response, err := guidanceResponse(r.Context(), env, sp, note, names)
		if err != nil {
			return err
		}
		out = append(out, response)
	}
	return writeJSON(w, http.StatusOK, out)
}

func createGuidance(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body GuidanceCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Name) == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if err := checkInstruction(body.Instruction); err != nil {
		return err
	}
	if (body.FilterID == nil) == (len(body.Conditions) == 0) {
		// The rules resource's own check: neither would be read as matching
		// nothing, and both leaves it ambiguous which conditions decide.
		return errInvalid("missing", []string{"body", "conditions"},
			"send either filter_id or a non-empty conditions")
	}

	note := &store.Guidance{
		Name:        strings.TrimSpace(body.Name),
		Instruction: strings.TrimSpace(body.Instruction),
		IsActive:    true,
		CreatedBy:   sp.User.ID,
	}
	if body.IsActive != nil {
		note.IsActive = *body.IsActive
	}
	if body.FilterID != nil {
		filter, err := requireFilter(r.Context(), env, sp, *body.FilterID)
		if err != nil {
			return err
		}
		note.FilterID = filter.ID
	} else {
		filter, err := guidanceConditions.writeOwnedFilter(
			r.Context(), env, sp, note.Name, body.Conditions)
		if err != nil {
			return err
		}
		note.FilterID = filter.ID
	}

	position, err := env.DB.NextGuidancePosition(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	note.Position = position
	if err := env.DB.CreateGuidance(r.Context(), sp.ID(), note); err != nil {
		return err
	}

	response, err := oneGuidanceResponse(r.Context(), env, sp, *note)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, response)
}

func readGuidance(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	note, err := liveGuidance(r, env, sp)
	if err != nil {
		return err
	}
	response, err := oneGuidanceResponse(r.Context(), env, sp, note)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, response)
}

func updateGuidance(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	note, err := liveGuidance(r, env, sp)
	if err != nil {
		return err
	}
	var body GuidanceUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if err := applyRequired("name", body.Name, &note.Name); err != nil {
		return err
	}
	if err := applyRequired("instruction", body.Instruction, &note.Instruction); err != nil {
		return err
	}
	if err := checkInstruction(note.Instruction); err != nil {
		return err
	}
	if err := applyRequired("is_active", body.IsActive, &note.IsActive); err != nil {
		return err
	}
	if body.FilterID.Cleared() {
		// A note with no conditions matches nothing, so clearing the pointer
		// would silence the note while it still looks live.
		return errConflict("filter_id cannot be cleared")
	}
	if body.FilterID.Present() {
		filter, err := requireFilter(r.Context(), env, sp, body.FilterID.Value)
		if err != nil {
			return err
		}
		note.FilterID = filter.ID
	}
	if body.Conditions != nil {
		if err := guidanceConditions.replaceOwnedConditions(
			r.Context(), env, sp, note.ID, note.FilterID, *body.Conditions); err != nil {
			return err
		}
	}
	if err := env.DB.UpdateGuidance(r.Context(), sp.ID(), &note); err != nil {
		return err
	}

	response, err := oneGuidanceResponse(r.Context(), env, sp, note)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, response)
}

func deleteGuidance(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	note, err := liveGuidance(r, env, sp)
	if err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteGuidance(r.Context(), sp.ID(), note.ID), "Guidance note")
}

func reorderGuidance(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body GuidanceReorder
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	notes, err := env.DB.ListGuidance(r.Context(), sp.ID(), store.GuidanceQuery{})
	if err != nil {
		return err
	}
	if len(body.GuidanceIDs) != len(notes) {
		return errInvalid("incomplete", []string{"body", "guidance_ids"},
			"send every note: %d were given and the space has %d",
			len(body.GuidanceIDs), len(notes))
	}
	if err := env.DB.ReorderGuidance(r.Context(), sp.ID(), body.GuidanceIDs); err != nil {
		return notFoundAs(err, "Guidance note")
	}
	return listGuidance(env, w, r, sp)
}

// --- Plumbing ----------------------------------------------------------------

func liveGuidance(r *http.Request, env *Env, sp auth.SpaceContext) (store.Guidance, error) {
	note, err := fromPath(r, sp, "guidance_id", "Guidance note", env.DB.GetGuidance)
	if err != nil {
		return store.Guidance{}, err
	}
	if note.IsDeleted {
		return store.Guidance{}, errNotFound("Guidance note")
	}
	return note, nil
}

func checkInstruction(instruction string) error {
	if strings.TrimSpace(instruction) == "" {
		return errInvalid("missing", []string{"body", "instruction"},
			"a note with nothing to say would be handed to the model as a blank line")
	}
	if len(instruction) > maxGuidanceInstruction {
		return errInvalid("too_long", []string{"body", "instruction"},
			"an instruction is at most %d characters", maxGuidanceInstruction)
	}
	return nil
}

// guidanceResponse renders one note, with `names` resolving its condition ids
// (loaded once per listing). A missing filter is tolerated, as ruleResponse
// tolerates it, so one deleted row cannot 409 the whole screen.
func guidanceResponse(
	ctx context.Context, env *Env, sp auth.SpaceContext, note store.Guidance,
	names map[domain.ID]string,
) (GuidanceResponse, error) {
	filter, err := env.DB.GetFilter(ctx, sp.ID(), note.FilterID)
	if err != nil && !isNotFound(err) {
		return GuidanceResponse{}, err
	}
	return GuidanceResponse{
		ID:          note.ID,
		Name:        note.Name,
		Instruction: note.Instruction,
		FilterID:    note.FilterID,
		Filter:      filterResponse(filter),
		OwnsFilter:  filter.Scope == GuidanceFilterScope,
		AppliesTo:   domain.DescribeFilter(store.DomainFilter(filter), names),
		IsActive:    note.IsActive,
		Position:    note.Position,
	}, nil
}

// oneGuidanceResponse is the single-note rendering, loading the id names this
// note's own conditions may hold.
func oneGuidanceResponse(
	ctx context.Context, env *Env, sp auth.SpaceContext, note store.Guidance,
) (GuidanceResponse, error) {
	names, err := filterValueNames(ctx, env, sp)
	if err != nil {
		return GuidanceResponse{}, err
	}
	return guidanceResponse(ctx, env, sp, note, names)
}

// filterValueNames resolves the ids a filter's clauses hold into the words the
// description prints: accounts, categories and tags in one map, deleted rows
// included so a note still names a retired category.
func filterValueNames(
	ctx context.Context, env *Env, sp auth.SpaceContext,
) (map[domain.ID]string, error) {
	names := map[domain.ID]string{}
	accounts, err := env.DB.ListAccounts(ctx, sp.ID(),
		store.AccountQuery{IncludeClosed: true, IncludeDeleted: true})
	if err != nil {
		return nil, err
	}
	for _, one := range accounts {
		names[domain.ID(one.ID.String())] = one.Name
	}
	categories, err := env.DB.ListCategories(ctx, sp.ID(), true)
	if err != nil {
		return nil, err
	}
	for _, one := range categories {
		names[domain.ID(one.ID.String())] = one.Name
	}
	tags, err := env.DB.ListTags(ctx, sp.ID(), true)
	if err != nil {
		return nil, err
	}
	for _, one := range tags {
		names[domain.ID(one.ID.String())] = one.Name
	}
	return names, nil
}
