package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The sign-in routes the browser connectors share: answering a step, answering
// it from the mailbox, and forgetting a kept password. A connector supplies its
// lookups, its service calls and its response shapes; S is the engine's state
// and O the route's.
type signInConnector[S, O any] struct {
	nouns    func(r *http.Request) agentNouns
	target   func(env *Env, r *http.Request, sp auth.SpaceContext) (uuid.UUID, error)
	answer   func(env *Env, ctx context.Context, space store.SpaceID, id uuid.UUID, session, code string) (S, error)
	fromMail func(env *Env, ctx context.Context, space store.SpaceID, id uuid.UUID, session string) (S, bool, error)
	forget   func(env *Env, ctx context.Context, space store.SpaceID, id uuid.UUID) error
	state    func(S) O
	mailed   func(state O, found bool) any
	written  func(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext, id uuid.UUID) error
}

// SignInAnswer is a code, or a choice, typed into the step a sign-in stopped at.
type SignInAnswer struct {
	Code string `json:"code"`
}

func (c signInConnector[S, O]) answerStep(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := c.target(env, r, sp)
	if err != nil {
		return err
	}
	var body SignInAnswer
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	state, err := c.answer(env, r.Context(), sp.ID(), id, chi.URLParam(r, "session"),
		strings.TrimSpace(body.Code))
	if err != nil {
		return connectorAgentError(err, c.nouns(r))
	}
	return writeJSON(w, http.StatusOK, c.state(state))
}

// answerFromMail waits for the code the site sent and types it in. Under
// /sign-in, so the assistant's dispatch never reaches it.
func (c signInConnector[S, O]) answerFromMail(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := c.target(env, r, sp)
	if err != nil {
		return err
	}
	state, found, err := c.fromMail(env, r.Context(), sp.ID(), id, chi.URLParam(r, "session"))
	if err != nil {
		return connectorAgentError(err, c.nouns(r))
	}
	return writeJSON(w, http.StatusOK, c.mailed(c.state(state), found))
}

func (c signInConnector[S, O]) forgetCredential(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := c.target(env, r, sp)
	if err != nil {
		return err
	}
	if err := c.forget(env, r.Context(), sp.ID(), id); err != nil {
		return notFoundAs(err, c.nouns(r).Connection)
	}
	return c.written(env, w, r, sp, id)
}

// agentNouns is how a connector's refusals name its things.
type agentNouns struct {
	SignIn     string
	Connection string
	// Unavailable is the sentence for a build with no browser engine.
	Unavailable string
}

// connectorAgentError turns a refusal from a connector's engine into something
// a person can act on: a lost session is a sign-in to restart, a step out of
// order a conflict, and a refused or unsupported request the request's fault.
// Only an engine that failed is a 502, which the client renders with no body,
// so anything with a useful sentence must not be one.
func connectorAgentError(err error, nouns agentNouns) error {
	switch {
	case errors.Is(err, provider.ErrBillsAgentUnavailable), errors.Is(err, provider.ErrMerchantAgentUnavailable):
		return errConflict("%s", nouns.Unavailable)
	case errors.Is(err, service.ErrNoMailboxForCode), errors.Is(err, service.ErrBillChallengeExpired),
		errors.Is(err, service.ErrPullRunning):
		return errConflict("%s", err.Error())
	case errors.Is(err, provider.ErrAgentNotFound):
		return errNotFound(nouns.SignIn)
	case isNotFound(err):
		if strings.Contains(err.Error(), "sign-in") {
			return errNotFound(nouns.SignIn)
		}
		return errNotFound(nouns.Connection)
	case errors.Is(err, provider.ErrAgentConflict):
		// The commonest conflict is a hold on a connection's browser profile:
		// an unfinished sign-in, or a lock file a restart left on the profiles
		// volume. Releasing the browser covers both.
		if strings.Contains(err.Error(), "already open") {
			return errConflict("An earlier sign-in to this connection is still open, or a " +
				"restart left its browser locked. Release the browser — the button is on " +
				"this dialog and on the connection's card — and sign in again; it gives up " +
				"the browser only, never the kept session or the password")
		}
		return errConflict("%s", provider.AgentMessage(err))
	case errors.Is(err, provider.ErrAgentBadRequest):
		// Not a fault: this engine will not do that, and its own words say
		// what to do instead.
		return errBadRequest("%s", provider.AgentMessage(err))
	}
	return errBadGateway("%s", err.Error())
}
