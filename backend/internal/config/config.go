// Package config reads every setting the process needs from the environment.
package config

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// credentialsDirs are the directories a secret may be read from as a file
// instead of an environment variable (systemd credentials, then the Docker
// Compose secrets default), so it stays out of /proc and `ps`.
func credentialsDirs() []string {
	raw := os.Getenv("CREDENTIALS_DIRECTORY")
	if raw == "" {
		raw = "/run/secrets"
	}
	var dirs []string
	for _, dir := range strings.Split(raw, ":") {
		if dir != "" {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// secretsDir is the first credentials directory that exists.
func secretsDir() string {
	for _, dir := range credentialsDirs() {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

type Config struct {
	Debug bool

	// DatabaseURL is a libpq URL. A unix socket goes in the `host` query
	// parameter, not the authority: postgres://user@/db?host=/run/postgresql.
	DatabaseURL string
	HTTPAddr    string
	// DatabaseMaxConns caps the pool. Postgres charges a process per
	// connection, and a self-hosted instance shares its machine with
	// everything else the household runs.
	DatabaseMaxConns int32

	// SecretKey signs access tokens. Rotating it logs everyone out, which is
	// the intended emergency response to a leak.
	SecretKey string
	// CredentialEncryptionKey seals the stored bank credentials. Empty derives
	// one from SecretKey — see CredentialKey, which is what callers read.
	CredentialEncryptionKey string
	AccessTokenExpiry       time.Duration

	// FrontendURL is the single allowed cross-origin caller, the scheme the
	// OIDC cookie's Secure flag follows, and where links back into the app
	// point. The SPA is served by this binary, so a browser on any other
	// address of the same server is same-origin and needs no CORS at all. A
	// wildcard would let any page in the browser spend the session cookie.
	FrontendURL string

	// WebAuthnRPID and WebAuthnOrigin empty mean "follow the browser's
	// origin", which is what most self-hosted deployments want; set them to
	// pin to one domain.
	WebAuthnRPName string
	WebAuthnRPID   string
	WebAuthnOrigin string

	// LoginMaxAttempts is password attempts allowed from one address per
	// window.
	LoginMaxAttempts   int
	LoginAttemptWindow time.Duration

	// TrustedProxies is the CIDRs of reverse proxies whose X-Forwarded-For may
	// be believed. Empty means trust none: the login rate limiter then meters
	// on the real TCP peer, so a spoofed forwarded header cannot mint a fresh
	// bucket per request and walk past the limit. Set it to the proxy in front
	// of the app (e.g. "192.0.2.0/24") when there is one.
	TrustedProxies []string

	// OIDC. `users` keys an identity on (issuer, subject), so repointing the
	// discovery URL yields new identities rather than colliding with old ones.
	OIDCEnabled      bool
	OIDCProviderName string
	OIDCDiscoveryURL string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCScopes       []string
	// OIDCRedirectURI is where the provider sends the browser back. Empty
	// derives it from FrontendURL, which is right only when the API is proxied
	// under it.
	OIDCRedirectURI string
	// OIDCAutoRegister decides whether an unknown subject may create an
	// account. Off by default: an open provider would otherwise let anyone
	// with an account there into this one.
	OIDCAutoRegister        bool
	OIDCRequireVerifiedMail bool
	// OIDCLinkExistingEmail decides whether a matching, verified email may
	// adopt an existing local account. Off by default — a provider that lets a
	// user set an arbitrary email would otherwise be an account-takeover path.
	OIDCLinkExistingEmail bool
	// AssistantAllowedHosts restricts which hosts a space's assistant
	// connection may point at. Empty (the default) leaves the base URL
	// unrestricted, which is right for a single household that runs its own
	// model on the LAN; a multi-user deployment sets it so one member cannot
	// turn the server into a proxy for an internal service.
	AssistantAllowedHosts []string
	// MerchantCatalogEnabled looks Costco item numbers up in Costco's
	// same-day listing, once each, so receipt lines read as products. On by
	// default; off keeps the register's abbreviations and asks nothing.
	MerchantCatalogEnabled bool
	// CostcoCatalog names the same-day storefront the lookup asks. The
	// listing answers only for a warehouse, and there is no default one, so
	// with any of the three empty nothing is looked up.
	CostcoCatalog CostcoStore

	// EmailPollEvery is how often the watched mailbox is read. Short, because
	// the codes a parked sign-in waits for are good for minutes.
	EmailPollEvery time.Duration
	// EmailLookbackDays is how far back a mailbox's first read goes.
	EmailLookbackDays int
	// EmailOTPRelays are the addresses the household's SMS-to-email
	// forwarders send from. A code from one of these is a relayed text; a
	// message from anywhere else that is not a provider's own code sender is
	// not a code at all, however much it reads like one.
	EmailOTPRelays []string
	// EmailForwarders are addresses, beyond the watched mailbox's own domain,
	// whose forwarded mail is read as the mail they forwarded.
	EmailForwarders []string

	// The server a connection is claimed from is the one its setup token
	// names, so there is no SimpleFIN address to configure.
	SimpleFINEnabled bool
	// SimpleFINAllowPrivate lets a setup token name a plain-HTTP or non-public
	// address. Never read from the environment: only a test sets it, for a
	// stand-in bridge on loopback.
	SimpleFINAllowPrivate bool

	Browser Browser

	// SyncEnabled runs the scheduled sync inside `serve`. Off means the only
	// way a connection refreshes is somebody pressing Sync now.
	SyncEnabled bool
	// SyncAt is the time of day the daily sync runs, in the server's own
	// timezone — set TZ if that is not what you want it read against.
	SyncAt TimeOfDay
	// SyncCheckEvery is how often the scheduler looks for work. It is not the
	// sync interval: it is the resolution at which the daily window is
	// noticed, and it bounds how late a sync can be after a restart.
	SyncCheckEvery time.Duration
	// AutomationWorkers is how many assistant automation runs execute at once.
	// One by default: a single local GPU is slower, not faster, with parallel
	// runs.
	AutomationWorkers int

	// PrimaryCurrency is what aggregates are reported in.
	PrimaryCurrency        string
	SupportedCurrencies    []string
	OpenExchangeRatesAppID string

	// InvestmentNewsEnabled draws the portfolio's related-news carousel, the
	// one feature that reaches a public market source with no credential.
	// Turning it off stops those outbound calls and loses only the carousel.
	InvestmentNewsEnabled bool
	// MarketPricesEnabled lets the manual price refresh reach the market-data
	// source. Off means the button says so instead of silently doing nothing.
	MarketPricesEnabled bool

	// StoragePath is the directory attachments are kept in.
	StoragePath string

	// BackupDir is where backup sets are written; empty turns the server's
	// backups off. The compose file mounts ./backups there.
	BackupDir string
	// BackupAt, BackupKeepDays and BackupRecipients are the defaults behind
	// whatever the administration screen saves: the nightly time in TZ, how
	// many days of sets to keep, and the age recipients to encrypt to.
	BackupAt         TimeOfDay
	BackupKeepDays   int
	BackupRecipients []string
	// SecretsDir is the directory the secret files are read from, which a
	// backup archives; empty when there is none.
	SecretsDir string

	// VAPID. Both halves must be set together; unset means the server uses the
	// keypair it generated and stored in the database (api.Env.vapidKeys).
	VAPIDPrivateKey string
	VAPIDPublicKey  string
	// VAPIDSubject is the `sub` claim on the VAPID token — a mailto: or https:
	// URL the push service can use to contact whoever runs this instance.
	// Empty derives one (api.Env.vapidSubject).
	VAPIDSubject string

	// SMTP. Unset SMTPHost leaves email dormant, which is the default: a
	// self-hosted install with no mail server must not fail to start, and an
	// alert nobody can be emailed is better than a startup that refuses.
	SMTPHost string
	SMTPPort int
	// SMTPUsername and SMTPPassword are optional. A LAN relay commonly wants
	// neither, and refuses a message sent with an empty AUTH.
	SMTPUsername string
	SMTPPassword string
	// SMTPFrom is the envelope sender. Defaults to the username when that is
	// an address.
	SMTPFrom string
	// SMTPStartTLS upgrades a plaintext connection. Port 465 is implicit TLS
	// instead and is detected from the port, so this is only about 587 and 25.
	SMTPStartTLS bool
}

// EmailEnabled reports whether this deployment can send mail at all. Read at
// send time, so adding a mail server later needs no other change.
func (c *Config) EmailEnabled() bool { return strings.TrimSpace(c.SMTPHost) != "" }

// Load reads the environment, applying defaults for everything unset. A
// malformed value is an error, not a warning.
func Load() (*Config, error) {
	cfg, _, err := Resolve(nil)
	return cfg, err
}

// Resolve is Load with the values saved from the administration screen
// beneath the environment: a variable set in the environment or .env wins,
// then a saved value, then the default. Only the keys in Settings are taken
// from stored. It reports, for each of those, the value and where it came
// from.
func Resolve(stored map[string]string) (*Config, map[string]Resolved, error) {
	dotEnv, err := loadDotEnv(".env")
	if err != nil {
		return nil, nil, err
	}

	e := envReader{file: dotEnv, stored: adjustableOnly(stored)}
	cfg := e.config()
	if e.err != nil {
		return nil, nil, e.err
	}
	if err := cfg.validate(); err != nil {
		return nil, nil, err
	}
	resolved := make(map[string]Resolved, len(Settings))
	for _, setting := range Settings {
		resolved[setting.Key] = e.seen[setting.Key]
	}
	return cfg, resolved, nil
}

func (e *envReader) config() *Config {
	cfg := &Config{
		Debug: e.bool("DEBUG", false),

		DatabaseURL:      e.secret("DATABASE_URL", "postgres://agentifi:agentifi@localhost:5432/agentifi"),
		HTTPAddr:         e.str("HTTP_ADDR", ":8000"),
		DatabaseMaxConns: int32(e.int("DATABASE_MAX_CONNS", 10)),

		SecretKey:               e.secret("SECRET_KEY", "change-me-in-production"),
		CredentialEncryptionKey: e.secret("CREDENTIAL_ENCRYPTION_KEY", ""),

		AccessTokenExpiry: e.minutes("ACCESS_TOKEN_EXPIRE_MINUTES", 60*24),

		FrontendURL: e.str("FRONTEND_URL", "http://localhost:5173"),

		WebAuthnRPName: e.str("WEBAUTHN_RP_NAME", "Agentifi"),
		WebAuthnRPID:   e.str("WEBAUTHN_RP_ID", ""),
		WebAuthnOrigin: e.str("WEBAUTHN_ORIGIN", ""),

		LoginMaxAttempts:   e.int("LOGIN_MAX_ATTEMPTS", 20),
		LoginAttemptWindow: e.seconds("LOGIN_ATTEMPT_WINDOW_SECONDS", 300),
		TrustedProxies:     e.list("TRUSTED_PROXY_CIDRS", "", ","),

		OIDCEnabled:             e.bool("OIDC_ENABLED", false),
		OIDCProviderName:        e.str("OIDC_PROVIDER_NAME", "OIDC"),
		OIDCDiscoveryURL:        e.str("OIDC_DISCOVERY_URL", ""),
		OIDCClientID:            e.str("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:        e.secret("OIDC_CLIENT_SECRET", ""),
		OIDCScopes:              e.list("OIDC_SCOPES", "openid email profile", " "),
		OIDCRedirectURI:         e.str("OIDC_REDIRECT_URI", ""),
		OIDCAutoRegister:        e.bool("OIDC_AUTO_REGISTER", false),
		OIDCRequireVerifiedMail: e.bool("OIDC_REQUIRE_VERIFIED_EMAIL", true),
		OIDCLinkExistingEmail:   e.bool("OIDC_LINK_EXISTING_EMAIL", false),
		AssistantAllowedHosts:   e.list("ASSISTANT_ALLOWED_HOSTS", "", ","),
		MerchantCatalogEnabled:  e.bool("MERCHANT_CATALOG_ENABLED", true),
		CostcoCatalog: CostcoStore{
			ShopID:     e.str("COSTCO_CATALOG_SHOP_ID", ""),
			ZoneID:     e.str("COSTCO_CATALOG_ZONE_ID", ""),
			PostalCode: e.str("COSTCO_CATALOG_POSTAL_CODE", ""),
		},

		EmailPollEvery:    e.minutes("EMAIL_POLL_MINUTES", 30),
		EmailLookbackDays: e.int("EMAIL_LOOKBACK_DAYS", 30),
		EmailOTPRelays:    e.list("EMAIL_OTP_RELAYS", "", ","),
		EmailForwarders:   e.list("EMAIL_FORWARDERS", "", ","),

		SimpleFINEnabled: e.bool("SIMPLEFIN_ENABLED", false),

		SyncEnabled:    e.bool("SYNC_ENABLED", true),
		SyncAt:         ParseTimeOfDay(e.str("SYNC_AT", "04:00")),
		SyncCheckEvery: e.minutes("SYNC_CHECK_MINUTES", 5),

		AutomationWorkers: e.int("AUTOMATION_WORKERS", 1),

		PrimaryCurrency: strings.ToUpper(e.str("PRIMARY_CURRENCY", "USD")),
		SupportedCurrencies: e.list(
			"SUPPORTED_CURRENCIES",
			"USD,EUR,GBP,CAD,AUD,CHF,JPY,MXN,BRL,INR,SEK,DKK,NOK,PLN,CZK,NZD",
			",",
		),
		OpenExchangeRatesAppID: e.secret("OPENEXCHANGERATES_APP_ID", ""),

		InvestmentNewsEnabled: e.bool("INVESTMENT_NEWS_ENABLED", true),
		MarketPricesEnabled:   e.bool("MARKET_PRICES_ENABLED", true),

		StoragePath: e.str("STORAGE_PATH", "./data/attachments"),

		BackupDir:        e.str("BACKUP_DIR", ""),
		BackupAt:         ParseTimeOfDay(e.str("BACKUP_AT", "03:30")),
		BackupKeepDays:   e.int("BACKUP_KEEP_DAYS", 14),
		BackupRecipients: e.list("BACKUP_RECIPIENTS", "", ","),
		SecretsDir:       secretsDir(),

		VAPIDPrivateKey: e.secret("VAPID_PRIVATE_KEY", ""),
		VAPIDPublicKey:  e.str("VAPID_PUBLIC_KEY", ""),
		SMTPHost:        e.str("SMTP_HOST", ""),
		SMTPPort:        e.int("SMTP_PORT", 587),
		SMTPUsername:    e.str("SMTP_USERNAME", ""),
		SMTPPassword:    e.secret("SMTP_PASSWORD", ""),
		SMTPFrom:        e.str("SMTP_FROM", ""),
		SMTPStartTLS:    e.bool("SMTP_STARTTLS", true),

		VAPIDSubject: e.str("VAPID_SUBJECT", ""),
	}
	cfg.Browser = e.browser()
	return cfg
}

// DefaultChromePath is in the chrome volume, in the layout
// scripts/install-chrome.sh leaves there.
const DefaultChromePath = "/chrome/current/chrome"

type Browser struct {
	// Headful shows the browser, for a workstation watching what a module
	// sees. AGENTIFI_BROWSER_HEADFUL.
	Headful bool
	// DriverDir is where the Playwright driver was vendored at build time;
	// empty is the user cache directory. PLAYWRIGHT_DRIVER_PATH.
	DriverDir string
	// ChromePath is the Google Chrome executable the engine launches; the
	// default is where the compose file's chrome service installs it.
	// AGENTIFI_CHROME_PATH.
	ChromePath string
	// ProfilesDir is the volume the kept browser profiles live on.
	// AGENT_PROFILES_DIR, the name the compose files set.
	ProfilesDir string
	// CamoufoxURL is the Camoufox server's websocket, secret path included;
	// empty means the providers that need it are refused. CAMOUFOX_URL.
	CamoufoxURL string
}

// LoadBrowser reads the browser settings alone and validates nothing else,
// for the commands that answer "is the browser here" on a container with no
// database or secret configured.
func LoadBrowser() (Browser, error) {
	dotEnv, err := loadDotEnv(".env")
	if err != nil {
		return Browser{}, err
	}
	e := envReader{file: dotEnv}
	out := e.browser()
	return out, e.err
}

func (e *envReader) browser() Browser {
	return Browser{
		Headful:     e.bool("AGENTIFI_BROWSER_HEADFUL", false),
		DriverDir:   e.str("PLAYWRIGHT_DRIVER_PATH", ""),
		ChromePath:  e.str("AGENTIFI_CHROME_PATH", DefaultChromePath),
		ProfilesDir: e.str("AGENT_PROFILES_DIR", "/profiles"),
		CamoufoxURL: e.secret("CAMOUFOX_URL", ""),
	}
}

// minSecretKeyLength is the shortest SECRET_KEY a non-debug instance will sign
// tokens with.
const minSecretKeyLength = 32

func (c *Config) validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("config: DATABASE_URL is required")
	}
	if len(c.PrimaryCurrency) != 3 {
		return fmt.Errorf("config: PRIMARY_CURRENCY must be a three-letter ISO code, got %q", c.PrimaryCurrency)
	}
	// A production instance signing tokens with the documented default would
	// accept tokens minted by anyone who has read the documentation.
	if !c.Debug && c.SecretKey == "change-me-in-production" {
		return fmt.Errorf("config: SECRET_KEY is still the default; set it or run with DEBUG=true")
	}
	// A key short enough to guess is the same failure as the default one.
	// 32 bytes is what `openssl rand -hex 32` produces, which is what the docs
	// tell an operator to run.
	if !c.Debug && len(c.SecretKey) < minSecretKeyLength {
		return fmt.Errorf("config: SECRET_KEY is %d characters and must be at least %d — generate one with `openssl rand -hex 32`",
			len(c.SecretKey), minSecretKeyLength)
	}
	if !c.SyncAt.Valid() {
		return fmt.Errorf("config: SYNC_AT must be HH:MM in 24-hour time, got %q", c.SyncAt.Raw)
	}
	if c.SyncCheckEvery <= 0 {
		return fmt.Errorf("config: SYNC_CHECK_MINUTES must be positive")
	}
	if !c.BackupAt.Valid() {
		return fmt.Errorf("config: BACKUP_AT must be HH:MM in 24-hour time, got %q", c.BackupAt.Raw)
	}
	if c.BackupKeepDays < 1 {
		return fmt.Errorf("config: BACKUP_KEEP_DAYS must be at least 1")
	}
	// Zero attempts would refuse every password, which only a shell can undo.
	if c.LoginMaxAttempts < 1 {
		return fmt.Errorf("config: LOGIN_MAX_ATTEMPTS must be at least 1")
	}
	if c.LoginAttemptWindow <= 0 {
		return fmt.Errorf("config: LOGIN_ATTEMPT_WINDOW_SECONDS must be positive")
	}
	if c.AccessTokenExpiry <= 0 {
		return fmt.Errorf("config: ACCESS_TOKEN_EXPIRE_MINUTES must be positive")
	}
	if c.EmailPollEvery <= 0 {
		return fmt.Errorf("config: EMAIL_POLL_MINUTES must be positive")
	}
	if c.AutomationWorkers < 1 {
		return fmt.Errorf("config: AUTOMATION_WORKERS must be at least 1")
	}
	if c.SMTPPort < 1 || c.SMTPPort > 65535 {
		return fmt.Errorf("config: SMTP_PORT must be a port number, got %d", c.SMTPPort)
	}
	// The proxy middleware panics on a prefix it cannot parse.
	for _, prefix := range c.TrustedProxies {
		if _, err := netip.ParsePrefix(prefix); err != nil {
			return fmt.Errorf("config: TRUSTED_PROXY_CIDRS must be CIDRs such as 192.0.2.0/24, got %q", prefix)
		}
	}
	return nil
}

// CredentialKey is the key the stored bank credentials are sealed with.
//
// Deliberately not SecretKey: rotating SECRET_KEY is the documented way to log
// everyone out, and if it also keyed the credentials every stored Access URL
// would become undecryptable. The fallback derivation lets an operator about to
// rotate SECRET_KEY set CREDENTIAL_ENCRYPTION_KEY to the derived value first.
func (c *Config) CredentialKey() string {
	if c.CredentialEncryptionKey != "" {
		return c.CredentialEncryptionKey
	}
	return DeriveCredentialKey(c.SecretKey)
}

// DeriveCredentialKey is the documented fallback: a domain-separated SHA-256 of
// SECRET_KEY, so the two secrets are never the same bytes even when one is
// derived from the other.
func DeriveCredentialKey(secretKey string) string {
	// The label is part of every derived key: changing it strands every
	// credential on an install that leaves CREDENTIAL_ENCRYPTION_KEY empty.
	sum := sha256.Sum256([]byte("agentifi:credential-encryption:v1:" + secretKey))
	return hex.EncodeToString(sum[:])
}

// envReader resolves one setting from the environment, then from .env, then
// from what was saved in the app, and keeps the first parse failure so Load
// reports it once. The .env values are held here rather than written with
// os.Setenv, which would change global state every later Load sees.
type envReader struct {
	file   map[string]string
	stored map[string]string
	// seen is every value read but the secrets, as Resolve reports it.
	seen map[string]Resolved
	err  error
}

// lookup applies the precedence: a real environment variable always wins over
// the file, so a container's -e flag beats a .env baked into an image, and
// either wins over a saved value.
func (e *envReader) lookup(key string) (string, Source, bool) {
	if v, ok := os.LookupEnv(key); ok {
		return v, FromEnvironment, true
	}
	if v, ok := e.file[key]; ok {
		return v, FromEnvironment, true
	}
	if v, ok := e.stored[key]; ok {
		return v, FromDatabase, true
	}
	return "", FromDefault, false
}

func (e *envReader) record(key, value, fallback string, source Source) {
	if e.seen == nil {
		e.seen = map[string]Resolved{}
	}
	e.seen[key] = Resolved{Value: value, Default: fallback, Source: source}
}

func (e *envReader) str(key, fallback string) string {
	v, source, ok := e.lookup(key)
	if !ok {
		v = fallback
	}
	e.record(key, v, fallback, source)
	return v
}

// secret reads a value that must not appear in a process listing, preferring a
// credentials file over the environment. It records nothing.
func (e *envReader) secret(key, fallback string) string {
	if v, _, ok := e.lookup(key); ok && v != "" {
		return v
	}
	for _, dir := range credentialsDirs() {
		// Both systemd and Compose name the file after the credential; the
		// lowercase spelling is what Compose secrets use.
		for _, name := range []string{key, strings.ToLower(key)} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil {
				return strings.TrimSpace(string(data))
			}
		}
	}
	return fallback
}

func (e *envReader) bool(key string, fallback bool) bool {
	v, source, ok := e.lookup(key)
	parsed := fallback
	if ok && v != "" {
		var err error
		if parsed, err = strconv.ParseBool(v); err != nil {
			e.fail(fmt.Errorf("config: %s must be a boolean, got %q", key, v))
			parsed = fallback
		}
	}
	e.record(key, strconv.FormatBool(parsed), strconv.FormatBool(fallback), source)
	return parsed
}

func (e *envReader) int(key string, fallback int) int {
	v, source, ok := e.lookup(key)
	parsed := fallback
	if ok && v != "" {
		var err error
		if parsed, err = strconv.Atoi(v); err != nil {
			e.fail(fmt.Errorf("config: %s must be an integer, got %q", key, v))
			parsed = fallback
		}
	}
	e.record(key, strconv.Itoa(parsed), strconv.Itoa(fallback), source)
	return parsed
}

func (e *envReader) seconds(key string, fallback int) time.Duration {
	return time.Duration(e.int(key, fallback)) * time.Second
}

func (e *envReader) minutes(key string, fallback int) time.Duration {
	return time.Duration(e.int(key, fallback)) * time.Minute
}

func (e *envReader) list(key, fallback, sep string) []string {
	var out []string
	for _, item := range strings.Split(e.str(key, fallback), sep) {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (e *envReader) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// loadDotEnv reads a .env file into a map. A missing file is not an error, and
// nothing here touches the process environment.
func loadDotEnv(path string) (map[string]string, error) {
	values := map[string]string{}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values, scanner.Err()
}

// CostcoStore is one warehouse on Costco's same-day storefront.
type CostcoStore struct {
	ShopID, ZoneID, PostalCode string
}

// Set reports whether every part is filled in; the listing needs all three.
func (s CostcoStore) Set() bool {
	return s.ShopID != "" && s.ZoneID != "" && s.PostalCode != ""
}
