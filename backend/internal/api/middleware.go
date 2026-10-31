package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// middlewares is the stack every request passes through, outermost first.
func middlewares(cfg *config.Config) []func(http.Handler) http.Handler {
	stack := []func(http.Handler) http.Handler{
		middleware.RequestID,
	}
	// The client IP the login rate limiter meters on. X-Forwarded-For is
	// believed only from a configured trusted proxy (chi's RealIP trusts it
	// from anyone, so a rotated header would mint a fresh bucket per request).
	// With none configured, the limiter meters on the TCP peer.
	if len(cfg.TrustedProxies) > 0 {
		stack = append(stack, middleware.ClientIPFromXFF(cfg.TrustedProxies...))
	}
	return append(stack,
		middleware.Recoverer,
		cors(cfg.FrontendURL),
		requestLog,
	)
}

// cors allows exactly one origin, never a wildcard or a reflected Origin: a
// page that can read a response sent with the session can read the whole
// ledger. A mismatched origin is served without the header.
func cors(frontend string) func(http.Handler) http.Handler {
	allowed := strings.TrimRight(strings.TrimSpace(frontend), "/")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimRight(r.Header.Get("Origin"), "/")
			// Vary regardless of the outcome: a cached response with the
			// header would otherwise be served to an origin that was refused.
			w.Header().Add("Vary", "Origin")

			if allowed != "" && origin == allowed {
				// The trimmed form, not the raw config value: a trailing slash
				// would echo a header the browser rejects.
				w.Header().Set("Access-Control-Allow-Origin", allowed)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id")
				if r.Method == http.MethodOptions {
					w.Header().Set("Access-Control-Allow-Methods",
						"GET, POST, PATCH, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers",
						"Authorization, Content-Type, "+auth.HeaderSpaceID)
					w.Header().Set("Access-Control-Max-Age", "600")
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestLog writes one structured line per request, with the request id the
// error body also carries, so a reported 500 finds its line.
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(recorder, r)

		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.Status(),
			"bytes", recorder.BytesWritten(),
			"duration_ms", time.Since(started).Milliseconds(),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}

// sessionCookie carries a browser's session. HttpOnly, so no script on the
// page can read it; SameSite=Strict and currentUser's origin check, so no
// other site can spend it.
const sessionCookie = "agentifi_session"

// presentedSession is the session a request authenticated with.
type presentedSession struct {
	store.Session
	// cookie is the token when it came in the session cookie, and empty for a
	// bearer token.
	cookie string
	// extended is a use that slid the expiry, which the cookie's own follows.
	extended bool
}

// currentUser resolves the request's token to its session and the account it
// names: a bearer token if there is one, else the session cookie. The session
// says who and nothing else, so deactivation and a revoked membership take
// effect on the next request.
//
// A missing, unknown, expired or revoked token is one 401. A valid token for a
// since-disabled account is a 403: the caller authenticated, so there is
// nothing left to enumerate. A cookie on a request that can change state must
// come with this app's Origin.
func (e *Env) currentUser(r *http.Request) (store.User, presentedSession, error) {
	var presented presentedSession
	raw := bearerToken(r)
	if raw == "" {
		if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
			raw, presented.cookie = cookie.Value, cookie.Value
			if !safeMethod(r.Method) {
				if err := auth.CheckSameOrigin(r, e.Cfg.FrontendURL); err != nil {
					return store.User{}, presentedSession{}, err
				}
			}
		}
	}
	session, extended, err := e.Tokens.Authenticate(r.Context(), raw)
	if err != nil {
		return store.User{}, presentedSession{}, err
	}
	presented.Session, presented.extended = session, extended
	user, err := e.DB.GetUser(r.Context(), session.UserID)
	if err != nil {
		if isNotFound(err) {
			return store.User{}, presentedSession{}, auth.ErrInvalidCredentials
		}
		return store.User{}, presentedSession{}, err
	}
	if !user.IsActive {
		return store.User{}, presentedSession{}, auth.ErrInactiveUser
	}
	return user, presented, nil
}

// safeMethod is a request that changes nothing, which a browser sends without
// an Origin header.
func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// setSessionCookie hands a browser its session. It expires with the session,
// and is set again whenever a use slides that.
func setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/api",
		Expires:  expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/api",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

type sessionKey struct{}

// currentSession is the session serve resolved for this request.
func currentSession(r *http.Request) store.Session {
	session, _ := r.Context().Value(sessionKey{}).(store.Session)
	return session
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}
