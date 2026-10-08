package api

import (
	"context"
	"errors"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/service"
)

// The first-run sign-up: on a server with no accounts, the sign-in screen
// offers to make one, and that account administers the server. Once any
// account exists the sign-up is closed, whoever asks.

// firstSpaceName is what `agentifi user add` names a space by default.
const firstSpaceName = "Household"

// GetFirstAccount says whether the sign-up is open, which is all a caller with
// no session learns about the accounts here.
func (s authService) GetFirstAccount(ctx context.Context, _ *agentifiv1.GetFirstAccountRequest) (*agentifiv1.GetFirstAccountResponse, error) {
	exists, err := s.env.DB.HasUsers(ctx)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetFirstAccountResponse{Open: !exists}, nil
}

// CreateFirstAccount makes the server's first account and signs it in. The
// password was chosen by the person holding it, so it is not one to change.
func (s authService) CreateFirstAccount(ctx context.Context, req *agentifiv1.CreateFirstAccountRequest) (*agentifiv1.CreateFirstAccountResponse, error) {
	env := s.env
	if err := meterLogin(env, callRequest(ctx)); err != nil {
		return nil, err
	}
	email := strings.TrimSpace(req.GetEmail())
	if email == "" {
		return nil, errInvalid("missing", []string{"body", "email"}, "email is required")
	}
	if len(email) > maxIdentifierLength || !strings.Contains(email, "@") {
		return nil, errInvalid("value", []string{"body", "email"}, "an email address is required")
	}
	space := strings.TrimSpace(req.GetSpaceName())
	if space == "" {
		space = firstSpaceName
	}

	created, err := service.CreateFirstAccount(ctx, env.DB, service.NewAccount{
		Email:     email,
		FullName:  req.GetFullName(),
		Password:  req.GetPassword(),
		Placement: service.Placement{SpaceName: space, Currency: env.Cfg.PrimaryCurrency},
	}, env.now())
	switch {
	case errors.Is(err, service.ErrNotFirstAccount):
		return nil, errConflictCode("first_account_taken",
			"This server already has an account. Sign in, or ask whoever runs it for one.")
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong):
		return nil, errInvalid("value", []string{"body", "password"}, "%s", err.Error())
	case err != nil:
		return nil, err
	}

	token, err := issueSession(ctx, env, created)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateFirstAccountResponse{
		AccessToken: token, TokenType: "bearer", MfaMethods: []string{},
	}, nil
}
