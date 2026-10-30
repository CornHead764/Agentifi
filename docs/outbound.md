# What leaves the server

Every destination the server contacts, what it sends, and the setting that
turns it off. Three are on in a fresh install with nothing configured: the
update check and the two Yahoo Finance calls. Everything else waits for a
key, an address or a connection that someone sets up.

"Live" means a change saved in Server admin applies at once; "restart" means
it is read at start-up; "env only" means it is a secret and can only be set
in the environment. A value in the environment always wins over one saved in
the app.

## On with no configuration

| Destination | When | What it sends | Turn it off |
| --- | --- | --- | --- |
| GitHub (`api.github.com`), the release tags | An administrator loads the app; the answer is kept for six hours | Nothing beyond the request | `UPDATE_CHECK_ENABLED=false` (live) |
| Yahoo Finance (`query1.finance.yahoo.com`), prices | Someone refreshes prices or backfills a security's history; never on a timer | Ticker symbols | `MARKET_PRICES_ENABLED=false` (restart) |
| Yahoo Finance, news | The portfolio page is opened; one request per held ticker, kept for 15 minutes | Every ticker the household holds | `INVESTMENT_NEWS_ENABLED=false` (restart) |

Institution logos are Google favicon addresses
(`www.google.com/s2/favicons`). The server only stores the address; the
viewer's browser fetches the image, which tells Google the bank's domain.

## Only once configured

| Destination | When | What it sends | Needs |
| --- | --- | --- | --- |
| SimpleFIN Bridge, or the host in the setup token | Connecting, the daily sync at `SYNC_AT`, and "Sync now" | The access URL and its credentials | `SIMPLEFIN_ENABLED=true` (live) and a connection |
| Open Exchange Rates (`openexchangerates.org`) | Once a day, and when a foreign-currency transaction is saved | The app id and the supported currency codes | `OPENEXCHANGERATES_APP_ID` (env only) |
| The assistant's model, at the space's base URL | Chat, automations (including after each sync and import), mail rule drafts, and reading sign-in codes from mail | The API key, the conversation and the tool results it asks for: transactions, accounts, categories, mail text | A space saves an enabled model connection. Saving one that may write also creates the category-check automation, in propose mode. `ASSISTANT_ALLOWED_HOSTS` (live) limits the hosts. |
| The household's configured model, at its base URL | `agentifi eval categorize --with-model`, run by hand | Minimised transaction context, with the API key | The `--with-model` flag; without it the command never calls a model |
| An SMTP relay | Alert sweeps after syncs, pulls, mailbox polls and backups | The SMTP login, and each alert's title, text and link | `SMTP_HOST` (restart) |
| Browser push services (each subscriber's endpoint) | The same alert sweeps | An encrypted title, text and link, and the VAPID token | A person turns on notifications in their browser |
| An IMAP mailbox, or Microsoft Graph | Every `EMAIL_POLL_MINUTES`, and when the mailbox is saved | The mailbox login or token; mail is read, not sent | A space configures the bill mailbox |
| The OIDC provider at `OIDC_DISCOVERY_URL` | Sign-in, and when an administrator tests the settings | The client id and secret, and the sign-in code | `OIDC_ENABLED=true` |
| Zillow and Kelley Blue Book, through Camoufox | Once a day for a property or vehicle valued more than 30 days ago, and a manual revalue | The address, or the VIN and mileage | `CAMOUFOX_URL` (env only) and an asset with valuation on |
| Costco's same-day catalogue (`sameday.costco.com`) | Each scheduler tick, once per new Costco item number | Item numbers and the configured shop, zone and postal code | `MERCHANT_CATALOG_ENABLED` and all three `COSTCO_CATALOG_*` settings (restart) |
| eBay (`api.ebay.com`, `auth.ebay.com`) | Connecting, then a daily pull | The app credentials and the account's token | `EBAY_APP_ID`, `EBAY_CERT_ID`, `EBAY_RUNAME` (env only) and a connected account |
| Amazon and Costco orders | Connecting, then a daily pull | The account's sign-in and session | A connected account with its daily pull on; Costco also needs `CAMOUFOX_URL` |
| Each bill provider's site (the list is `domain.Billers`) | Connecting, the daily pull, and a keepalive visit every few days that keeps the session signed in | The sign-in, its second-factor codes, and codes read from mail | A connection with its pull on; Chrome, or Camoufox for the providers that run in Firefox |
| Google Chrome's own background traffic | While Chrome runs a sign-in, pull or keepalive | Chrome's own requests | Chrome is installed (the compose `chrome` service) |

`SYNC_ENABLED=false` (restart) stops the scheduled bank sync, bill pulls and
keepalive visits, and merchant pulls with their catalogue lookups. Asking for a
pull by hand still works.
