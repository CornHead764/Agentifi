package api

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"strings"
	"time"

	connectcors "connectrpc.com/cors"
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
		keepRequestMeta,
		requestLog,
	)
}

// requestMeta is what a procedure cannot read off a request it is never
// handed: the host and TLS state a WebAuthn ceremony binds its origin to, and
// the peer the login rate limiter meters on.
type requestMeta struct {
	host       string
	tls        *tls.ConnectionState
	remoteAddr string
}

type requestMetaKey struct{}

func keepRequestMeta(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta := requestMeta{host: r.Host, tls: r.TLS, remoteAddr: r.RemoteAddr}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestMetaKey{}, meta)))
	})
}

// requestFrom rebuilds the parts of the original request that helpers taking
// an *http.Request read (auth.RequestOrigin, clientKey), for a procedure.
func requestFrom(ctx context.Context, header http.Header) *http.Request {
	meta, _ := ctx.Value(requestMetaKey{}).(requestMeta)
	r := &http.Request{Header: header, Host: meta.host, TLS: meta.tls, RemoteAddr: meta.remoteAddr}
	return r.WithContext(ctx)
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
				w.Header().Set("Access-Control-Expose-Headers",
					strings.Join(append([]string{"X-Request-Id"}, connectcors.ExposedHeaders()...), ", "))
				if r.Method == http.MethodOptions {
					w.Header().Set("Access-Control-Allow-Methods",
						"GET, POST, PATCH, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders(), ", "))
					w.Header().Set("Access-Control-Max-Age", "600")
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// allowedHeaders is what a request may carry: the session's two headers and
// everything the Connect protocol sends.
func allowedHeaders() []string {
	seen := map[string]bool{}
	var out []string
	for _, header := range append([]string{"Authorization", auth.HeaderSpaceID}, connectcors.AllowedHeaders()...) {
		if key := http.CanonicalHeaderKey(header); !seen[key] {
			seen[key] = true
			out = append(out, header)
		}
	}
	return out
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
	return e.userFromToken(r.Context(), bearerToken(r))
}

func (e *Env) userFromToken(ctx context.Context, raw string) (store.User, error) {
	if raw == "" {
		return store.User{}, auth.ErrInvalidCredentials
	}
	claims, err := e.Tokens.Authenticate(ctx, raw)
	if err != nil {
		return store.User{}, err
	}
	user, err := e.DB.GetUser(ctx, claims.Subject)
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

func bearerToken(r *http.Request) string { return bearerFrom(r.Header) }

func bearerFrom(headers http.Header) string {
	header := headers.Get("Authorization")
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}
