package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/service"
)

// The first-run sign-up: on a server with no accounts, the sign-in screen
// offers to make one, and that account administers the server. Once any
// account exists the sign-up is closed, whoever asks.

// FirstAccountStatus says whether the sign-up is open, which is all a caller
// with no session learns about the accounts here.
type FirstAccountStatus struct {
	Open bool `json:"open"`
}

type FirstAccountCreate struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Password string `json:"password"`
	// SpaceName names the space the account owns; "" is firstSpaceName.
	SpaceName string `json:"space_name"`
}

// firstSpaceName is what `agentifi user add` names a space by default.
const firstSpaceName = "Household"

func readFirstAccount(env *Env, w http.ResponseWriter, r *http.Request) error {
	exists, err := env.DB.HasUsers(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, FirstAccountStatus{Open: !exists})
}

// createFirstAccount makes the server's first account and signs it in. The
// password was chosen by the person holding it, so it is not one to change.
func createFirstAccount(env *Env, w http.ResponseWriter, r *http.Request) error {
	if err := meterLogin(env, r); err != nil {
		return err
	}
	var body FirstAccountCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	email := strings.TrimSpace(body.Email)
	if email == "" {
		return errInvalid("missing", []string{"body", "email"}, "email is required")
	}
	if len(email) > maxIdentifierLength || !strings.Contains(email, "@") {
		return errInvalid("value", []string{"body", "email"}, "an email address is required")
	}
	space := strings.TrimSpace(body.SpaceName)
	if space == "" {
		space = firstSpaceName
	}

	created, err := service.CreateFirstAccount(r.Context(), env.DB, service.NewAccount{
		Email:     email,
		FullName:  body.FullName,
		Password:  body.Password,
		Placement: service.Placement{SpaceName: space, Currency: env.Cfg.PrimaryCurrency},
	}, env.now())
	switch {
	case errors.Is(err, service.ErrNotFirstAccount):
		return errConflictCode("first_account_taken",
			"This server already has an account. Sign in, or ask whoever runs it for one.")
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong):
		return errInvalid("value", []string{"body", "password"}, "%s", err.Error())
	case err != nil:
		return err
	}

	token, err := issueSession(r.Context(), env, created)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, LoginResponse{
		AccessToken: token, TokenType: "bearer", MfaMethods: []string{},
	})
}
