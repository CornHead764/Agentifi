package api

import (
	"net/http"
	"slices"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The setup guide: the steps a new space goes through, in order, each checked
// off by what the ledger shows rather than by a click. Hiding it and skipping
// a step are the only things stored; both are the space's, so whoever sets it
// up next sees what the last person chose.

func init() {
	Register(Resource{Prefix: "/setup-guide", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", readSetupGuide)
		rt.Write(http.MethodPatch, "/", updateSetupGuide)
	}})
}

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

type SetupGuideStep struct {
	ID string `json:"id"`
	// State is done (the ledger shows it), skipped (somebody said it does not
	// apply, or it no longer can) or open.
	State string `json:"state"`
	// Optional steps do not keep the guide open.
	Optional bool `json:"optional"`
}

type SetupGuideResponse struct {
	Dismissed bool `json:"dismissed"`
	// Complete is every step done or skipped.
	Complete bool             `json:"complete"`
	Steps    []SetupGuideStep `json:"steps"`
	// Skipped is what somebody said does not apply, which a step skipped
	// because it no longer can is not.
	Skipped []string `json:"skipped"`
}

type SetupGuideUpdate struct {
	Dismissed Opt[bool] `json:"dismissed"`
	// Skipped is the whole list of steps that do not apply.
	Skipped Opt[[]string] `json:"skipped"`
}

func readSetupGuide(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	guide, err := env.DB.GetSetupGuide(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	out, err := setupGuideResponse(env, r, sp, guide)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func updateSetupGuide(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body SetupGuideUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	guide, err := env.DB.GetSetupGuide(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	if body.Dismissed.Present() {
		guide.DismissedAt = nil
		if body.Dismissed.Value {
			now := env.now().UTC()
			guide.DismissedAt = &now
		}
	}
	if body.Skipped.Present() {
		skipped := []string{}
		for _, id := range body.Skipped.Value {
			if !slices.Contains(skippableSteps, id) {
				return errInvalid("enum", []string{"body", "skipped"}, "%q is not a step that can be skipped", id)
			}
			if !slices.Contains(skipped, id) {
				skipped = append(skipped, id)
			}
		}
		guide.Skipped = skipped
	}
	if err := env.DB.SaveSetupGuide(r.Context(), sp.ID(), guide.DismissedAt, guide.Skipped); err != nil {
		return err
	}
	out, err := setupGuideResponse(env, r, sp, guide)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func setupGuideResponse(
	env *Env, r *http.Request, sp auth.SpaceContext, guide store.SetupGuide,
) (SetupGuideResponse, error) {
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

	steps := []SetupGuideStep{
		{ID: stepImport, State: importState},
		{ID: stepConnect, State: state(stepConnect, guide.Connected)},
		{ID: stepMatch, State: matchState},
		{ID: stepAssistant, State: state(stepAssistant, guide.Assistant), Optional: true},
		{ID: stepBills, State: state(stepBills, guide.BillProviders), Optional: true},
	}
	// The backup key is the server's, so only somebody who administers it is
	// asked for one.
	if sp.User.IsSuperuser {
		settings, err := env.ServerBackups().Settings(r.Context())
		if err != nil {
			return SetupGuideResponse{}, err
		}
		steps = append(steps, SetupGuideStep{
			ID: stepBackups, State: state(stepBackups, len(settings.Keys()) > 0), Optional: true,
		})
	}

	complete := true
	for _, step := range steps {
		if step.State == stepOpen {
			complete = false
		}
	}
	return SetupGuideResponse{
		Dismissed: guide.DismissedAt != nil,
		Complete:  complete,
		Steps:     steps,
		Skipped:   skipped,
	}, nil
}
