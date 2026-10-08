package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/CornHead764/agentifi/backend/internal/api"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/web"
)

// shutdownGrace is how long in-flight requests have to finish once a signal
// arrives.
const shutdownGrace = 15 * time.Second

// The connection time caps. Body size is capped per route in internal/api; a
// cap on bytes without one on time still lets a client hold a connection open
// by sending slowly.
//
// Sized for the slowest legitimate requests: a large Simplifi import over a
// slow link, and an assistant turn against a local model that may think for
// minutes. No handler streams, so one generous WriteTimeout is enough.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 5 * time.Minute
	writeTimeout      = 5 * time.Minute
	idleTimeout       = 2 * time.Minute
)

func serve(ctx context.Context, cfg *config.Config) error {
	db, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := checkSchema(ctx, db); err != nil {
		return err
	}

	// The settings saved in Server admin, beneath the environment. Everything
	// below reads cfg, so each takes effect from here.
	cfg, started := api.ServerConfig(ctx, cfg, db)

	// One environment for the API and the automation worker, so a run reaches
	// the same handlers, through the same dispatcher, as a request does.
	env := api.NewEnv(cfg, db, api.WithStartedSettings(started))
	automations, err := api.NewAutomations(env)
	if err != nil {
		return err
	}
	automations.Workers = cfg.AutomationWorkers
	env.Automations = automations

	// Single sign-on is a stored setting as well as an environment one, so a
	// restart has to pick up whatever the administration screen last saved.
	// Not fatal: an unreadable settings row should leave the rest of the
	// server running with the environment's provider, not refuse to boot.
	if err := env.ReloadOIDC(ctx); err != nil {
		slog.Warn("reading the saved single sign-on settings failed; "+
			"the environment's provider stands", "error", err)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           newRouterWith(cfg, db, env),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Asset re-pricing is enabled separately
	// from bank sync: a household with a house and no bank connection still
	// wants its net worth to move.
	scheduler := &service.Scheduler{
		Store:   db,
		At:      service.SyncWindow{Hour: cfg.SyncAt.Hour, Minute: cfg.SyncAt.Minute},
		Every:   cfg.SyncCheckEvery,
		Between: 5 * time.Second,
	}
	// SimpleFIN is asked on every pass, since Server admin turns it on and off
	// without a restart.
	if cfg.SyncEnabled {
		sync, err := api.NewSimpleFinSync(env)
		if err != nil {
			return err
		}
		scheduler.Sync = sync
		scheduler.BanksOn = func() bool { return env.Live().SimpleFINEnabled }
	} else {
		slog.Info("scheduled sync off: SYNC_ENABLED is false")
	}
	scheduler.Valuation = api.NewAssetValuation(env)
	if cfg.Browser.CamoufoxURL == "" {
		slog.Info("asset valuation off: the Zillow and Kelley Blue Book lookups run in Camoufox, " +
			"and CAMOUFOX_URL is unset")
	}
	// Mounted regardless of configuration: with no provider credential the
	// fetch is a no-op, and the conversion still applies the rates the
	// Simplifi import brought across.
	scheduler.Currency = api.NewCurrency(cfg, db)
	scheduler.Merchants = api.NewMerchants(env)
	// Mounted regardless of configuration: the challenge expiry still has to
	// run, or a row parked by an earlier deployment waits for ever.
	scheduler.Bills = api.NewBills(env)
	scheduler.Bills.Between = 5 * time.Second
	// On its own interval: the code a parked sign-in waits for is good for
	// minutes.
	scheduler.Mail = api.NewMailbox(env)
	scheduler.Documents = api.NewDocuments(env)
	scheduler.Forecasts = service.NewAccountForecasts(db)
	if cfg.OpenExchangeRatesAppID == "" {
		slog.Info("exchange rates: no OPENEXCHANGERATES_APP_ID in the environment; " +
			"foreign amounts convert only from rates already stored")
	}

	if scheduler.Sync != nil || scheduler.Valuation != nil || scheduler.Currency != nil ||
		scheduler.Merchants.HasAgent() || scheduler.Bills != nil || scheduler.Mail != nil {
		go scheduler.Run(ctx)
	}
	go automations.Work(ctx)
	if backups := env.ServerBackups(); backups.Enabled() {
		go backups.Run(ctx)
	} else {
		slog.Info("backups off: BACKUP_DIR is unset")
	}
	browsersDone := make(chan struct{})
	go func() {
		env.KeepBrowsers(ctx)
		close(browsersDone)
	}()

	errs := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
		close(errs)
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	err = server.Shutdown(shutdownCtx)
	select {
	case <-browsersDone:
	case <-shutdownCtx.Done():
	}
	return err
}

// checkSchema refuses to serve against a database the binary is ahead of,
// rather than 500 on the first query that names a missing column.
func checkSchema(ctx context.Context, db *store.Store) error {
	version, err := db.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	latest, err := db.LatestMigrationVersion()
	if err != nil {
		return err
	}
	return schemaComplaint(version, latest)
}

func schemaComplaint(version, latest int64) error {
	switch {
	case version == 0:
		return fmt.Errorf("agentifi: the database has no schema — run `agentifi migrate` first")
	case version < latest:
		return fmt.Errorf("agentifi: the database is at migration %d and this binary carries %d — run `agentifi migrate` first",
			version, latest)
	}
	return nil
}

// newRouterWith builds the HTTP surface: the operational endpoints here, and
// the application's routes mounted whole from internal/api.
func newRouterWith(cfg *config.Config, db *store.Store, env *api.Env) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	// Only the listed types are compressed, so an attachment (a PDF, a
	// photograph) is served as-is.
	r.Use(middleware.Compress(5,
		"text/html", "text/css", "text/plain", "text/javascript",
		"application/javascript", "application/json", "image/svg+xml"))

	r.Get("/health", health(db))
	r.Mount("/api", api.RouterFor(env))

	// Registered as the not-found handler so /health and /api match first and
	// every other path, including client-side deep links, gets the app.
	r.NotFound(web.Handler().ServeHTTP)

	return r
}

// contentSecurityPolicy is the policy the embedded SPA runs under. Vite emits
// one same-origin module script and stylesheet with no inline block, so
// script-src needs only 'self'. The exceptions:
//
//   - style-src 'unsafe-inline': React style={{…}} attributes compile to
//     inline style attributes. Removing it means removing those.
//   - img-src https:: institution logos come from Google's favicon service and
//     news thumbnails from each publisher's CDN, so hosts are not enumerable.
//     data: and blob: are for the attachment preview's object URLs.
//
// The session token lives in localStorage (an accepted risk); this header is
// the compensating control on what an injected script could do with it.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"worker-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders sets the browser security headers on every response. Here
// rather than in internal/api because the SPA, served from this file's
// not-found handler, is what a framing or sniffing attack targets.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		// X-Frame-Options as well as frame-ancestors: the two say the same
		// thing to browsers a decade apart, and neither is a superset.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy",
			"accelerometer=(), camera=(), geolocation=(), gyroscope=(), "+
				"magnetometer=(), microphone=(), payment=(), usb=()")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// health reports whether this process can serve, which means whether it can
// reach the database.
func health(db *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		status := http.StatusOK
		body := map[string]string{"status": "ok"}
		if err := db.Ping(ctx); err != nil {
			status = http.StatusServiceUnavailable
			body = map[string]string{"status": "unavailable", "detail": "database"}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
}

// healthcheck is the container's own probe, run by the binary itself with
// nothing but net/http rather than a separate curl process.
func healthcheck(cfg *config.Config) error {
	client := &http.Client{Timeout: 5 * time.Second}
	url := healthcheckURL(cfg.HTTPAddr)

	response, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("agentifi: healthcheck: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("agentifi: healthcheck: %s answered %d", url, response.StatusCode)
	}
	return nil
}

// healthcheckURL turns a listen address into the URL to probe. A bare port or
// wildcard host is not dialable, so it becomes loopback.
func healthcheckURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", strings.TrimPrefix(addr, ":")
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/health"
}
