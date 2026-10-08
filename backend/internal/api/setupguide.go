package api

import (
	"context"
	"net/http"
	"slices"

	"connectrpc.com/connect"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The setup guide: the steps a new space goes through, in order, each checked
// off by what the ledger shows rather than by a click. Hiding it and skipping
// a step are the only things stored; both are the space's, so whoever sets it
// up next sees what the last person chose.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewSetupGuideServiceHandler(setupGuideService{env}, opts...)
	})
}

type setupGuideService struct{ env *Env }

// The steps, in the order they are offered. Import comes before connecting:
// imported accounts arrive unlinked and are then matched to the bank feeds.
const (
	stepImport    = "import"
	stepConnect   = "connect"
	stepMatch     = "match"
	stepAssistant = "assistant"
	stepBills     = "bills"
	stepBackups   = "backups"
)

// skippableSteps may be marked as not applying. Matching is not: it follows
// from connecting, and is skipped with it.
var skippableSteps = []string{stepImport, stepConnect, stepAssistant, stepBills, stepBackups}

const (
	stepDone    = "done"
	stepSkipped = "skipped"
	stepOpen    = "open"
)

func (s setupGuideService) GetSetupGuide(
	ctx context.Context, _ *agentifiv1.GetSetupGuideRequest,
) (*agentifiv1.GetSetupGuideResponse, error) {
	sp := spaceFrom(ctx)
	guide, err := s.env.DB.GetSetupGuide(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	out, err := setupGuideProto(ctx, s.env, sp, guide)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetSetupGuideResponse{Guide: out}, nil
}

func (s setupGuideService) UpdateSetupGuide(
	ctx context.Context, req *agentifiv1.UpdateSetupGuideRequest,
) (*agentifiv1.UpdateSetupGuideResponse, error) {
	sp := spaceFrom(ctx)
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	guide, err := s.env.DB.GetSetupGuide(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	if dismissed := optOf(mask, "dismissed", req.Dismissed); dismissed.Present() {
		guide.DismissedAt = nil
		if dismissed.Value {
			now := s.env.now().UTC()
			guide.DismissedAt = &now
		}
	}
	if req.Skipped != nil {
		skipped := []string{}
		for _, id := range req.Skipped.GetIds() {
			if !slices.Contains(skippableSteps, id) {
				return nil, errInvalid("enum", []string{"body", "skipped"}, "%q is not a step that can be skipped", id)
			}
			if !slices.Contains(skipped, id) {
				skipped = append(skipped, id)
			}
		}
		guide.Skipped = skipped
	}
	if err := s.env.DB.SaveSetupGuide(ctx, sp.ID(), guide.DismissedAt, guide.Skipped); err != nil {
		return nil, err
	}
	out, err := setupGuideProto(ctx, s.env, sp, guide)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateSetupGuideResponse{Guide: out}, nil
}

func setupGuideProto(
	ctx context.Context, env *Env, sp auth.SpaceContext, guide store.SetupGuide,
) (*agentifiv1.SetupGuide, error) {
	skipped := guide.Skipped
	if skipped == nil {
		skipped = []string{}
	}
	isSkipped := func(id string) bool { return slices.Contains(skipped, id) }
	state := func(id string, done bool) string {
		switch {
		case done:
			return stepDone
		case isSkipped(id):
			return stepSkipped
		}
		return stepOpen
	}

	importState := state(stepImport, guide.Imported)
	// An import takes only an empty space, so once accounts are here it can
	// no longer be the next step.
	if importState == stepOpen && guide.HasAccounts {
		importState = stepSkipped
	}
	matchState := state(stepMatch, guide.Synced)
	if matchState == stepOpen && isSkipped(stepConnect) && !guide.Connected {
		matchState = stepSkipped
	}

	steps := []*agentifiv1.SetupGuideStep{
		{Id: stepImport, State: importState},
		{Id: stepConnect, State: state(stepConnect, guide.Connected)},
		{Id: stepMatch, State: matchState},
		{Id: stepAssistant, State: state(stepAssistant, guide.Assistant), Optional: true},
		{Id: stepBills, State: state(stepBills, guide.BillProviders), Optional: true},
	}
	// The backup key is the server's, so only somebody who administers it is
	// asked for one.
	if sp.User.IsSuperuser {
		settings, err := env.ServerBackups().Settings(ctx)
		if err != nil {
			return nil, err
		}
		steps = append(steps, &agentifiv1.SetupGuideStep{
			Id: stepBackups, State: state(stepBackups, len(settings.Keys()) > 0), Optional: true,
		})
	}

	complete := true
	for _, step := range steps {
		if step.GetState() == stepOpen {
			complete = false
		}
	}
	return &agentifiv1.SetupGuide{
		Dismissed: guide.DismissedAt != nil,
		Complete:  complete,
		Steps:     steps,
		Skipped:   skipped,
	}, nil
}
