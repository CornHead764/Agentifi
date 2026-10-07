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

// cors allows exactly one origin, never a wildcard or a reflected Origin: the
// API is bearer-authenticated and a page that can read a response can read the
// whole ledger. A mismatched origin is served without the header.
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

// currentUser resolves the bearer token to the account it names. The token says
// who and nothing else, so deactivation and a revoked membership take effect on
// the next request.
//
// A missing, malformed, expired or signed-out token is one 401. A valid token
// for a since-disabled account is a 403: the caller authenticated, so there is
// nothing left to enumerate.
func (e *Env) currentUser(r *http.Request) (store.User, error) {
	raw := bearerToken(r)
	if raw == "" {
		return store.User{}, auth.ErrInvalidCredentials
	}
	claims, err := e.Tokens.Authenticate(r.Context(), raw)
	if err != nil {
		return store.User{}, err
	}
	user, err := e.DB.GetUser(r.Context(), claims.Subject)
	if err != nil {
		if isNotFound(err) {
			return store.User{}, auth.ErrInvalidCredentials
		}
		return store.User{}, err
	}
	if !user.IsActive {
		return store.User{}, auth.ErrInactiveUser
	}
	// A password change ends every session. Revocation cannot, being keyed on
	// a jti this process may never have seen and not surviving a restart, so a
	// token issued before the cutoff is refused here.
	if cutoff := user.SessionsValidFrom; cutoff != nil && claims.IssuedAt.Before(*cutoff) {
		return store.User{}, auth.ErrInvalidCredentials
	}
	return user, nil
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}
