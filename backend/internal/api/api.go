// Package api is the HTTP surface: routing, tenancy, serialization, and the
// orchestration between internal/store and internal/domain.
//
// Three rules, each enforced structurally:
//
//   - A route resolves a tenant or it does not exist. Handlers are
//     SpaceHandler, registered only through Routes.Read and Routes.Write;
//     route_contract_test.go walks every route to assert it.
//   - One error mapping. Handlers return error and the adapter maps it
//     (errors.go); no handler restates a failure status.
//   - Money crosses the wire as a string in both directions, through
//     domain.Money's JSON methods, which refuse a JSON number.
package api

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Env is what every handler is given besides the request. Passed explicitly
// rather than held in a package variable, so a test can stand up a second one
// with a different clock or store.
type Env struct {
	// Cfg is the configuration the process started with, saved settings
	// included. A setting marked Live in config.Settings is read through
	// Live() instead, which each save swaps for its own.
	Cfg *config.Config
	DB  *store.Store

	live atomic.Pointer[config.Config]
	// started is how each of config.Settings resolved when the process
	// started, which is what a setting read only at start is still running on.
	started map[string]config.Resolved

	Tokens   *auth.Tokens
	Pending  *auth.PendingLogin
	Passkeys *auth.Passkeys
	TOTP     *auth.TOTP
	OIDC     *auth.OIDC

	// Keys and Codes are the credential stores, in Postgres; nil when NewEnv
	// has no database.
	Keys  auth.PasskeyStore
	Codes auth.RecoveryCodeStore

	// Secrets holds short-lived state a ceremony needs between two HTTP calls:
	// passkey challenges, OIDC state, half-finished logins, TOTP replay marks
	// and login attempt counters.
	Secrets *auth.SecretStore

	// Storage is where attachment bytes live. Nil only if an option clears it;
	// the attachment resource then answers 502.
	Storage provider.StorageProvider

	// Automations is the automation service when this process runs its worker.
	// When nil, a handler builds a fallback and runs inline.
	Automations *service.Automations
	lazyAutomationsState

	// simplifiImports holds each space's Simplifi upload between its preview
	// and its write.
	simplifiImports simplifiImportJobs

	// The connector engine, built once: it holds sign-in sessions
	// and the browser, neither of which may be rebuilt per request.
	lazyBrowserState
	lazyConnectorState

	// BillsAgent and MerchantAgent stand in for those engines. Nil (deployed)
	// is the browser in this process; tests set them.
	BillsAgent    service.BillsAgent
	MerchantAgent service.MerchantAgent

	// Pusher stands in for web push. Nil (deployed) signs with the VAPID
	// keypair; tests set it.
	Pusher service.Pusher

	vapidMu sync.Mutex
	vapid   provider.VapidKeys

	// News is the ticker-news source. Nil is supported: the investments
	// screen drops the carousel.
	News provider.MarketNewsProvider

	// Prices is the market-price source behind the manual refresh. Nil is
	// supported: the refresh answers that no source is configured.
	Prices provider.MarketPriceProvider

	// Catalog is where a merchant's item numbers are looked up. Nil is
	// supported: receipt lines keep the register's abbreviations.
	Catalog provider.MerchantCatalog

	// Backups is the server's backup service; nil until first asked for, when
	// it is built from the configuration. Tests set it.
	Backups     *service.Backups
	backupsOnce sync.Once

	// Now is nil for the real clock.
	Now func() time.Time
}

func (e *Env) now() time.Time {
	if e.Now == nil {
		return time.Now()
	}
	return e.Now()
}

// Option adjusts the environment NewEnv builds.
type Option func(*Env)

// NewEnv assembles the environment from configuration.
func NewEnv(cfg *config.Config, db *store.Store, opts ...Option) *Env {
	secrets := auth.NewSecretStore()
	env := &Env{
		Cfg:     cfg,
		DB:      db,
		Storage: &provider.LocalStorage{BasePath: cfg.StoragePath},
		Secrets: secrets,
		Tokens: &auth.Tokens{
			SecretKey: cfg.SecretKey,
			Expiry:    cfg.AccessTokenExpiry,
			Revoked:   secrets,
		},
		Pending: &auth.PendingLogin{Store: secrets, TTL: 5 * time.Minute},
		Passkeys: &auth.Passkeys{
			RPName:       cfg.WebAuthnRPName,
			RPID:         cfg.WebAuthnRPID,
			Origin:       cfg.WebAuthnOrigin,
			ChallengeTTL: 5 * time.Minute,
			Challenges:   secrets,
		},
		TOTP: &auth.TOTP{
			Issuer:            "Agentifi",
			ValidWindow:       1,
			MaxAttempts:       5,
			AttemptWindow:     5 * time.Minute,
			RecoveryCodeCount: 10,
			State:             secrets,
		},
		// The provider is set below through Configure, the same path a save
		// from the settings screen takes.
		OIDC: &auth.OIDC{
			RedirectURI: cfg.OIDCRedirectURI,
			FrontendURL: cfg.FrontendURL,
			State:       secrets,
		},
	}
	env.OIDC.Configure(OIDCFromEnvironment(cfg))

	// Cached: the carousel draws on every portfolio visit, one request per
	// held ticker, against a source with a hard rate limit.
	if cfg.InvestmentNewsEnabled {
		env.News = &provider.CachedNews{Inner: &provider.YahooProvider{}}
	}
	if cfg.MarketPricesEnabled {
		env.Prices = &provider.YahooProvider{}
	}
	if cfg.MerchantCatalogEnabled && cfg.CostcoCatalog.Set() {
		catalog := provider.NewCostcoCatalog()
		catalog.ShopID = cfg.CostcoCatalog.ShopID
		catalog.ZoneID = cfg.CostcoCatalog.ZoneID
		catalog.PostalCode = cfg.CostcoCatalog.PostalCode
		env.Catalog = catalog
	}

	// The nil check is load-bearing: a nil *store.Store inside a non-nil
	// interface is not nil, and would panic on first use.
	if db != nil {
		env.Keys = storePasskeys{db: db}
		env.Codes = db
		// Logout outlives a restart only if the marks are durable; the
		// in-memory copy is this process's fast path.
		env.Tokens.Durable = db
	}

	for _, opt := range opts {
		opt(env)
	}
	return env
}

// RouterFor is the whole application API, ready to mount under /api. Resources
// are mounted sorted by prefix so the URL space does not depend on init order.
func RouterFor(env *Env) http.Handler {
	r := chi.NewRouter()
	for _, mw := range middlewares(env.Cfg) {
		r.Use(mw)
	}
	for _, resource := range registered() {
		mountResource(r, env, resource)
	}
	// Without its own, this router inherits the single-page app's not-found
	// handler, and an unknown API path would answer 200 with index.html.
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		writeError(w, req, errNotFound("Endpoint"))
	})
	return r
}

func mountResource(r chi.Router, env *Env, resource resource) {
	r.Route(resource.Prefix, func(sub chi.Router) {
		for _, route := range resource.Routes {
			sub.Method(route.Method, route.Pattern, env.adapt(route))
		}
	})
}
