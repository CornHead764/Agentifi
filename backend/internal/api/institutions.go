package api

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The banks the household's accounts are held at, and the setting that belongs
// to a bank: hiding every account there that holds next to nothing
// (domain.HiddenForSmallBalance). The institution's rather than the
// connection's, because one SimpleFIN connection spans every linked bank.
// Institutions are created by the sync only.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewInstitutionServiceHandler(institutionService{env}, opts...)
	})
}

type institutionService struct{ env *Env }

func (s institutionService) ListInstitutions(
	ctx context.Context, _ *agentifiv1.ListInstitutionsRequest,
) (*agentifiv1.ListInstitutionsResponse, error) {
	rows, err := s.env.DB.ListInstitutions(ctx, spaceFrom(ctx).ID())
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListInstitutionsResponse{Institutions: make([]*agentifiv1.Institution, 0, len(rows))}
	for _, row := range rows {
		out.Institutions = append(out.Institutions, institutionProto(row))
	}
	return out, nil
}

func (s institutionService) UpdateInstitution(
	ctx context.Context, req *agentifiv1.UpdateInstitutionRequest,
) (*agentifiv1.UpdateInstitutionResponse, error) {
	id, err := idFrom(req.GetInstitutionId(), "Institution")
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	hideBelow, err := optMoneyOf(mask, "hide_below_balance", req.HideBelowBalance)
	if err != nil {
		return nil, err
	}
	if !hideBelow.Set {
		return nil, errInvalid("missing", []string{"body", "hide_below_balance"},
			"hide_below_balance is required; send null to clear it")
	}
	if err := checkHideBelow(hideBelow); err != nil {
		return nil, err
	}
	var threshold domain.Money
	var present bool
	applyNullableMoney(hideBelow, &threshold, &present)
	row, err := s.env.DB.SetInstitutionHideBelow(ctx, spaceFrom(ctx).ID(), id, threshold, present)
	if err != nil {
		return nil, notFoundAs(err, "Institution")
	}
	return &agentifiv1.UpdateInstitutionResponse{Institution: institutionProto(row)}, nil
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

func institutionProto(row store.Institution) *agentifiv1.Institution {
	return &agentifiv1.Institution{
		Id:               row.ID.String(),
		Name:             row.Name,
		LogoUrl:          dbconv.NullText(row.LogoURL),
		HideBelowBalance: nullableMoneyProto(row.HideBelowBalance, row.HasHideBelowBalance),
	}
}

// institutionThresholds is each institution's small-balance rule, keyed for
// the account listing.
func institutionThresholds(ctx context.Context, env *Env, sp auth.SpaceContext) (map[uuid.UUID]domain.SmallBalanceThreshold, error) {
	rows, err := env.DB.ListInstitutions(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]domain.SmallBalanceThreshold, len(rows))
	for _, row := range rows {
		out[row.ID] = domain.SmallBalanceThreshold{Amount: row.HideBelowBalance, Set: row.HasHideBelowBalance}
	}
	return out, nil
}
