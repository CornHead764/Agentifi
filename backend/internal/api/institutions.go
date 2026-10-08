package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The banks the household's accounts are held at, and the setting that belongs
// to a bank: hiding every account there that holds next to nothing
// (domain.HiddenForSmallBalance). The institution's rather than the
// connection's, because one SimpleFIN connection spans every linked bank.
// Institutions are created by the sync only.

func init() {
	Register(Resource{Prefix: "/institutions", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listInstitutions)
		rt.Write(http.MethodPatch, "/{institution_id}", updateInstitution)
	}})
}

// InstitutionResponse is one bank as the client reads it.
type InstitutionResponse struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	LogoURL *string   `json:"logo_url"`
	// HideBelowBalance is null when the institution hides nothing.
	HideBelowBalance *domain.Money `json:"hide_below_balance"`
}

// InstitutionUpdate is a partial edit. Explicit null clears the threshold.
type InstitutionUpdate struct {
	HideBelowBalance Opt[domain.Money] `json:"hide_below_balance"`
}

func listInstitutions(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListInstitutions(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	out := make([]InstitutionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, institutionResponse(row))
	}
	return writeJSON(w, http.StatusOK, out)
}

func updateInstitution(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "institution_id", "Institution")
	if err != nil {
		return err
	}
	var body InstitutionUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if !body.HideBelowBalance.Set {
		return errInvalid("missing", []string{"body", "hide_below_balance"},
			"hide_below_balance is required; send null to clear it")
	}
	if err := checkHideBelow(body.HideBelowBalance); err != nil {
		return err
	}
	var threshold domain.Money
	var present bool
	applyNullableMoney(body.HideBelowBalance, &threshold, &present)
	row, err := env.DB.SetInstitutionHideBelow(r.Context(), sp.ID(), id, threshold, present)
	if err != nil {
		return notFoundAs(err, "Institution")
	}
	return writeJSON(w, http.StatusOK, institutionResponse(row))
}

// checkHideBelow refuses a negative threshold, which the rule compares a
// magnitude against and so could never hide anything.
func checkHideBelow(opt Opt[domain.Money]) error {
	if opt.Present() && opt.Value.IsNegative() {
		return errInvalid("out_of_range", []string{"body", "hide_below_balance"},
			"hide_below_balance cannot be negative")
	}
	return nil
}

func institutionResponse(row store.Institution) InstitutionResponse {
	return InstitutionResponse{
		ID:               row.ID,
		Name:             row.Name,
		LogoURL:          dbconv.NullText(row.LogoURL),
		HideBelowBalance: store.PtrIf(row.HideBelowBalance, row.HasHideBelowBalance),
	}
}

// institutionThresholds is each institution's small-balance rule, keyed for
// the account listing.
func institutionThresholds(r *http.Request, env *Env, sp auth.SpaceContext) (map[uuid.UUID]domain.SmallBalanceThreshold, error) {
	rows, err := env.DB.ListInstitutions(r.Context(), sp.ID())
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]domain.SmallBalanceThreshold, len(rows))
	for _, row := range rows {
		out[row.ID] = domain.SmallBalanceThreshold{Amount: row.HideBelowBalance, Set: row.HasHideBelowBalance}
	}
	return out, nil
}
