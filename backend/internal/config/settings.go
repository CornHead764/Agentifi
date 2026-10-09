package config

// The settings an administrator may change from Server admin → Settings. Each
// is an environment variable Load already reads; a value saved in the app sits
// beneath the environment, so a variable set in the environment or .env
// always wins and the screen shows it read-only.
//
// Not here: every secret, what the compose file fixes (DATABASE_URL,
// CAMOUFOX_URL, BACKUP_DIR, AGENT_PROFILES_DIR, HTTP_ADDR, DEBUG), the
// process's own plumbing (DATABASE_MAX_CONNS, STORAGE_PATH and the browser
// paths), PRIMARY_CURRENCY, which the command line reads before there is a
// database, and the OIDC and backup settings, which have screens of their own.

// Source says which layer answered for a setting.
type Source string

const (
	FromEnvironment Source = "environment"
	FromDatabase    Source = "database"
	FromDefault     Source = "default"
)

// Resolved is one setting's value as Resolve read it. Value and Default are
// written the way the environment variable is, so either can be saved back.
type Resolved struct {
	Value   string
	Default string
	Source  Source
}

// SettingKind is the control the screen draws and the shape a saved value
// must parse as.
type SettingKind string

const (
	KindToggle SettingKind = "toggle"
	KindNumber SettingKind = "number"
	// KindTime is HH:MM in the server's time zone.
	KindTime SettingKind = "time"
	KindText SettingKind = "text"
	// KindList is comma-separated.
	KindList SettingKind = "list"
)

type Setting struct {
	// Key is the environment variable.
	Key   string
	Group string
	Label string
	Help  string
	Kind  SettingKind
	// Live settings are read per request, so a save applies at once; the
	// rest are read when the server starts.
	Live bool
}

// Settings is the closed list, in the order the screen shows it.
var Settings = []Setting{
	{Key: "SIMPLEFIN_ENABLED", Group: "Banks and sync", Kind: KindToggle, Live: true,
		Label: "SimpleFIN bank connections",
		Help:  "Lets a space connect its banks with a SimpleFIN setup token, and the daily sync pull them."},
	{Key: "SYNC_ENABLED", Group: "Banks and sync", Kind: KindToggle,
		Label: "Daily sync",
		Help:  "The daily bank sync and the bill and merchant pulls. A sync on request works either way."},
	{Key: "SYNC_AT", Group: "Banks and sync", Kind: KindTime,
		Label: "Daily sync time",
		Help:  "In the server's time zone."},
	{Key: "SYNC_CHECK_MINUTES", Group: "Banks and sync", Kind: KindNumber,
		Label: "Scheduler check (minutes)",
		Help:  "How often the scheduler looks for due work, which bounds how late a run starts."},

	{Key: "INVESTMENT_NEWS_ENABLED", Group: "Features", Kind: KindToggle,
		Label: "Investment news",
		Help:  "The portfolio's news carousel, read from a public market source."},
	{Key: "MARKET_PRICES_ENABLED", Group: "Features", Kind: KindToggle,
		Label: "Market prices",
		Help:  "Lets the manual price refresh reach the market-data source."},
	{Key: "MERCHANT_CATALOG_ENABLED", Group: "Features", Kind: KindToggle,
		Label: "Costco item lookup",
		Help:  "Looks Costco item numbers up once each, so receipt lines read as products. Needs the three store settings below."},
	{Key: "COSTCO_CATALOG_SHOP_ID", Group: "Features", Kind: KindText,
		Label: "Costco item lookup: shop",
		Help:  "The shop id of the same-day storefront to ask, as its own requests name it."},
	{Key: "COSTCO_CATALOG_ZONE_ID", Group: "Features", Kind: KindText,
		Label: "Costco item lookup: zone",
		Help:  "The zone id that goes with the shop."},
	{Key: "COSTCO_CATALOG_POSTAL_CODE", Group: "Features", Kind: KindText,
		Label: "Costco item lookup: postal code",
		Help:  "A postal code the shop delivers to."},
	{Key: "SUPPORTED_CURRENCIES", Group: "Features", Kind: KindList,
		Label: "Currencies",
		Help:  "The ISO codes a space may report in, comma-separated."},

	{Key: "EMAIL_POLL_MINUTES", Group: "Bill mailbox", Kind: KindNumber,
		Label: "Read every (minutes)",
		Help:  "How often the watched mailbox is read."},
	{Key: "EMAIL_LOOKBACK_DAYS", Group: "Bill mailbox", Kind: KindNumber,
		Label: "First read reaches back (days)",
		Help:  "How far back a mailbox's first read goes."},
	{Key: "EMAIL_OTP_RELAYS", Group: "Bill mailbox", Kind: KindList,
		Label: "Text message relays",
		Help:  "Addresses an SMS-to-email forwarder sends relayed codes from, comma-separated."},
	{Key: "EMAIL_FORWARDERS", Group: "Bill mailbox", Kind: KindList,
		Label: "Forwarders",
		Help:  "Addresses beyond the mailbox's own domain whose forwarded mail is read as the original, comma-separated."},

	{Key: "ASSISTANT_ALLOWED_HOSTS", Group: "Assistant", Kind: KindList, Live: true,
		Label: "Allowed model hosts",
		Help:  "Hosts a space's assistant may point at, comma-separated. Empty is unrestricted."},
	{Key: "AUTOMATION_WORKERS", Group: "Assistant", Kind: KindNumber,
		Label: "Automation runs at once",
		Help:  "Raise it for a model server that batches."},

	{Key: "FRONTEND_URL", Group: "Sign-in", Kind: KindText,
		Label: "App address",
		Help:  "The address people reach the app on, scheme and port included. Single sign-on and links back into the app use it."},
	{Key: "ACCESS_TOKEN_EXPIRE_MINUTES", Group: "Sign-in", Kind: KindNumber,
		Label: "Session lifetime (minutes)"},
	{Key: "LOGIN_MAX_ATTEMPTS", Group: "Sign-in", Kind: KindNumber, Live: true,
		Label: "Password attempts per window",
		Help:  "From one address."},
	{Key: "LOGIN_ATTEMPT_WINDOW_SECONDS", Group: "Sign-in", Kind: KindNumber, Live: true,
		Label: "Attempt window (seconds)"},
	{Key: "TRUSTED_PROXY_CIDRS", Group: "Sign-in", Kind: KindList,
		Label: "Trusted proxies",
		Help:  "CIDRs of reverse proxies whose X-Forwarded-For is believed, comma-separated. Empty trusts none."},
	{Key: "WEBAUTHN_RP_NAME", Group: "Sign-in", Kind: KindText,
		Label: "Passkey name",
		Help:  "The name a passkey prompt shows."},
	{Key: "WEBAUTHN_RP_ID", Group: "Sign-in", Kind: KindText,
		Label: "Passkey domain",
		Help:  "Pins passkeys to one domain. Empty follows the browser's origin."},
	{Key: "WEBAUTHN_ORIGIN", Group: "Sign-in", Kind: KindText,
		Label: "Passkey origin",
		Help:  "Empty follows the browser's origin."},

	{Key: "SMTP_HOST", Group: "Email and push", Kind: KindText,
		Label: "Mail server",
		Help:  "Empty leaves email alerts off. SMTP_PASSWORD stays in the environment."},
	{Key: "SMTP_PORT", Group: "Email and push", Kind: KindNumber,
		Label: "Mail server port",
		Help:  "465 is implicit TLS, detected from the port."},
	{Key: "SMTP_USERNAME", Group: "Email and push", Kind: KindText,
		Label: "Mail user name",
		Help:  "A LAN relay commonly wants none."},
	{Key: "SMTP_FROM", Group: "Email and push", Kind: KindText,
		Label: "Sender",
		Help:  "Empty uses the user name when that is an address."},
	{Key: "SMTP_STARTTLS", Group: "Email and push", Kind: KindToggle,
		Label: "STARTTLS",
		Help:  "For ports 587 and 25."},
	{Key: "VAPID_SUBJECT", Group: "Email and push", Kind: KindText,
		Label: "Push contact",
		Help:  "A mailto: address the push services can reach whoever runs this server at. Empty uses this server's https address, else the first administrator's email."},
}

// SettingByKey finds a setting in the closed list.
func SettingByKey(key string) (Setting, bool) {
	for _, setting := range Settings {
		if setting.Key == key {
			return setting, true
		}
	}
	return Setting{}, false
}

// adjustableOnly drops anything outside Settings, so a stored row can never
// stand in for a secret or a setting the compose file fixes.
func adjustableOnly(stored map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range stored {
		if _, ok := SettingByKey(key); ok {
			out[key] = value
		}
	}
	return out
}
