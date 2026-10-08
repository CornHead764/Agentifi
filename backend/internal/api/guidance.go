package api

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewGuidanceServiceHandler(guidanceService{env}, opts...)
	})
}

type guidanceService struct{ env *Env }

// GuidanceFilterScope marks the filters this resource creates and owns.
const GuidanceFilterScope = "guidance"

// maxGuidanceInstruction bounds one note: a paragraph of household rules, short
// of a rewrite of the automation's prompt.
const maxGuidanceInstruction = 4000

// --- Handlers ----------------------------------------------------------------

func (s guidanceService) ListGuidance(
	ctx context.Context, _ *agentifiv1.ListGuidanceRequest,
) (*agentifiv1.ListGuidanceResponse, error) {
	notes, err := listGuidanceProtos(ctx, s.env, spaceFrom(ctx))
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ListGuidanceResponse{Guidance: notes}, nil
}

func listGuidanceProtos(ctx context.Context, env *Env, sp auth.SpaceContext) ([]*agentifiv1.Guidance, error) {
	notes, err := env.DB.ListGuidance(ctx, sp.ID(), store.GuidanceQuery{})
	if err != nil {
		return nil, err
	}
	names, err := filterValueNames(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	out := make([]*agentifiv1.Guidance, 0, len(notes))
	for _, note := range notes {
		response, err := guidanceProto(ctx, env, sp, note, names)
		if err != nil {
			return nil, err
		}
		out = append(out, response)
	}
	return out, nil
}

func (s guidanceService) CreateGuidance(
	ctx context.Context, req *agentifiv1.CreateGuidanceRequest,
) (*agentifiv1.CreateGuidanceResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if strings.TrimSpace(req.GetName()) == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if err := checkInstruction(req.GetInstruction()); err != nil {
		return nil, err
	}
	if (req.FilterId == nil) == (len(req.GetConditions()) == 0) {
		// The rules resource's own check: neither would be read as matching
		// nothing, and both leaves it ambiguous which conditions decide.
		return nil, errInvalid("missing", []string{"body", "conditions"},
			"send either filter_id or a non-empty conditions")
	}

	note := &store.Guidance{
		Name:        strings.TrimSpace(req.GetName()),
		Instruction: strings.TrimSpace(req.GetInstruction()),
		IsActive:    req.IsActive == nil || req.GetIsActive(),
		CreatedBy:   sp.User.ID,
	}
	if req.FilterId != nil {
		id, err := uuidFrom(req.GetFilterId(), "body", "filter_id")
		if err != nil {
			return nil, err
		}
		filter, err := requireFilter(ctx, env, sp, id)
		if err != nil {
			return nil, err
		}
		note.FilterID = filter.ID
	} else {
		writes, err := filterItemWrites(req.GetConditions(), "conditions")
		if err != nil {
			return nil, err
		}
		filter, err := guidanceConditions.writeOwnedFilter(ctx, env, sp, note.Name, writes)
		if err != nil {
			return nil, err
		}
		note.FilterID = filter.ID
	}

	position, err := env.DB.NextGuidancePosition(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	note.Position = position
	if err := env.DB.CreateGuidance(ctx, sp.ID(), note); err != nil {
		return nil, err
	}

	response, err := oneGuidanceProto(ctx, env, sp, *note)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateGuidanceResponse{Guidance: response}, nil
}

func (s guidanceService) GetGuidance(
	ctx context.Context, req *agentifiv1.GetGuidanceRequest,
) (*agentifiv1.GetGuidanceResponse, error) {
	sp := spaceFrom(ctx)
	note, err := liveGuidance(ctx, s.env, sp, req.GetGuidanceId())
	if err != nil {
		return nil, err
	}
	response, err := oneGuidanceProto(ctx, s.env, sp, note)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetGuidanceResponse{Guidance: response}, nil
}

func (s guidanceService) UpdateGuidance(
	ctx context.Context, req *agentifiv1.UpdateGuidanceRequest,
) (*agentifiv1.UpdateGuidanceResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	note, err := liveGuidance(ctx, env, sp, req.GetGuidanceId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	if err := applyRequired("name", optOf(mask, "name", req.Name), &note.Name); err != nil {
		return nil, err
	}
	if err := applyRequired("instruction", optOf(mask, "instruction", req.Instruction), &note.Instruction); err != nil {
		return nil, err
	}
	if err := checkInstruction(note.Instruction); err != nil {
		return nil, err
	}
	if err := applyRequired("is_active", optOf(mask, "is_active", req.IsActive), &note.IsActive); err != nil {
		return nil, err
	}
	filterID := optOf(mask, "filter_id", req.FilterId)
	if filterID.Cleared() {
		// A note with no conditions matches nothing, so clearing the pointer
		// would silence the note while it still looks live.
		return nil, errConflict("filter_id cannot be cleared")
	}
	if filterID.Present() {
		id, err := uuidFrom(filterID.Value, "body", "filter_id")
		if err != nil {
			return nil, err
		}
		filter, err := requireFilter(ctx, env, sp, id)
		if err != nil {
			return nil, err
		}
		note.FilterID = filter.ID
	}
	if mask["conditions"] {
		writes, err := filterItemWrites(req.GetConditions(), "conditions")
		if err != nil {
			return nil, err
		}
		if err := guidanceConditions.replaceOwnedConditions(
			ctx, env, sp, note.ID, note.FilterID, writes); err != nil {
			return nil, err
		}
	}
	if err := env.DB.UpdateGuidance(ctx, sp.ID(), &note); err != nil {
		return nil, err
	}

	response, err := oneGuidanceProto(ctx, env, sp, note)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateGuidanceResponse{Guidance: response}, nil
}

func (s guidanceService) DeleteGuidance(
	ctx context.Context, req *agentifiv1.DeleteGuidanceRequest,
) (*agentifiv1.DeleteGuidanceResponse, error) {
	sp := spaceFrom(ctx)
	note, err := liveGuidance(ctx, s.env, sp, req.GetGuidanceId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteGuidance(ctx, sp.ID(), note.ID); err != nil {
		return nil, notFoundAs(err, "Guidance note")
	}
	return &agentifiv1.DeleteGuidanceResponse{}, nil
}

// ReorderGuidance takes the whole live set in order. Partial lists are refused,
// as on the rules reorder: renumbering half leaves an order nobody chose.
func (s guidanceService) ReorderGuidance(
	ctx context.Context, req *agentifiv1.ReorderGuidanceRequest,
) (*agentifiv1.ReorderGuidanceResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	ids, err := uuidsFrom(req.GetGuidanceIds(), "body", "guidance_ids")
	if err != nil {
		return nil, err
	}
	notes, err := env.DB.ListGuidance(ctx, sp.ID(), store.GuidanceQuery{})
	if err != nil {
		return nil, err
	}
	if len(ids) != len(notes) {
		return nil, errInvalid("incomplete", []string{"body", "guidance_ids"},
			"send every note: %d were given and the space has %d",
			len(ids), len(notes))
	}
	if err := env.DB.ReorderGuidance(ctx, sp.ID(), ids); err != nil {
		return nil, notFoundAs(err, "Guidance note")
	}
	out, err := listGuidanceProtos(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ReorderGuidanceResponse{Guidance: out}, nil
}

// --- Plumbing ----------------------------------------------------------------

func liveGuidance(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (store.Guidance, error) {
	id, err := idFrom(rawID, "Guidance note")
	if err != nil {
		return store.Guidance{}, err
	}
	note, err := env.DB.GetGuidance(ctx, sp.ID(), id)
	if err != nil {
		return store.Guidance{}, notFoundAs(err, "Guidance note")
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

// guidanceProto renders one note, with `names` resolving its condition ids
// (loaded once per listing). A missing filter is tolerated, as ruleProto
// tolerates it, so one deleted row cannot 409 the whole screen.
func guidanceProto(
	ctx context.Context, env *Env, sp auth.SpaceContext, note store.Guidance,
	names map[domain.ID]string,
) (*agentifiv1.Guidance, error) {
	filter, err := env.DB.GetFilter(ctx, sp.ID(), note.FilterID)
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	return &agentifiv1.Guidance{
		Id:          note.ID.String(),
		Name:        note.Name,
		Instruction: note.Instruction,
		FilterId:    note.FilterID.String(),
		Filter:      filterProto(filter),
		OwnsFilter:  filter.Scope == GuidanceFilterScope,
		AppliesTo:   domain.DescribeFilter(store.DomainFilter(filter), names),
		IsActive:    note.IsActive,
		Position:    int32(note.Position),
	}, nil
}

// oneGuidanceProto is the single-note rendering, loading the id names this
// note's own conditions may hold.
func oneGuidanceProto(
	ctx context.Context, env *Env, sp auth.SpaceContext, note store.Guidance,
) (*agentifiv1.Guidance, error) {
	names, err := filterValueNames(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	return guidanceProto(ctx, env, sp, note, names)
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
