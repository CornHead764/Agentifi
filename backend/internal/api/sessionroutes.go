package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A person's own sessions: every browser, app and personal token signed in
// as them, each revocable, and the personal tokens a script uses in place of
// a browser's cookie.

type SessionResponse struct {
	ID         uuid.UUID         `json:"id"`
	Kind       store.SessionKind `json:"kind"`
	Name       string            `json:"name"`
	CreatedAt  time.Time         `json:"created_at"`
	LastUsedAt time.Time         `json:"last_used_at"`
	ExpiresAt  time.Time         `json:"expires_at"`
	// Current is the session this request came in on.
	Current bool `json:"current"`
}

// PersonalTokenResponse carries the token, once. Only its digest is kept, so
// this is the only time it exists outside the caller's hands.
type PersonalTokenResponse struct {
	Token   string          `json:"token"`
	Session SessionResponse `json:"session"`
}

// maxSessionNameLength bounds a name the person types; it is a label in a list.
const maxSessionNameLength = 100

func listSessions(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	sessions, err := env.DB.ListSessions(r.Context(), user.ID, env.now())
	if err != nil {
		return err
	}
	current := currentSession(r).ID
	out := make([]SessionResponse, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, sessionResponse(session, current))
	}
	return writeJSON(w, http.StatusOK, out)
}

func revokeSession(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	id, err := pathUUID(r, "session_id", "Session")
	if err != nil {
		return err
	}
	if err := env.DB.RevokeSession(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("Session")
		}
		return err
	}
	return writeNoContent(w)
}

// signOutEverywhere ends every session the person has, this one and their
// personal tokens included.
func signOutEverywhere(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	if err := env.DB.RevokeUserSessions(r.Context(), user.ID, uuid.Nil); err != nil {
		return err
	}
	clearSessionCookie(w)
	return writeNoContent(w)
}

func createPersonalToken(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return errInvalid("missing", []string{"body", "name"}, "a token needs a name")
	}
	if len(name) > maxSessionNameLength {
		return errInvalid("too_long", []string{"body", "name"},
			"a name is at most %d characters", maxSessionNameLength)
	}
	token, session, err := env.Tokens.Issue(r.Context(), user.ID, store.SessionPersonal, name)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, PersonalTokenResponse{
		Token: token, Session: sessionResponse(session, currentSession(r).ID),
	})
}

func sessionResponse(session store.Session, current uuid.UUID) SessionResponse {
	return SessionResponse{
		ID:         session.ID,
		Kind:       session.Kind,
		Name:       session.Name,
		CreatedAt:  session.CreatedAt,
		LastUsedAt: session.LastUsedAt,
		ExpiresAt:  session.ExpiresAt,
		Current:    session.ID == current,
	}
}
