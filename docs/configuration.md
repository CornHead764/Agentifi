# Configuration

Every setting Agentifi reads, for the stack in the root
[`docker-compose.yml`](../docker-compose.yml). None is required: a fresh
checkout runs with `docker compose up -d`. Operating the install (upgrades,
backups, restore, HTTPS) is [`operations.md`](operations.md).

## How settings are read

Settings go in `.env` beside `docker-compose.yml`. Compose hands the
application every variable in that file (`env_file: .env`), so anything below
is honoured by setting it there and nothing else. Compose also reads `.env`
for its own variables (the image, the port, the database names), which the
application ignores.

**An empty value is a value.** `SYNC_AT=` sets the empty string, which is not
the same as leaving the line out and getting the default. Delete lines you do
not want; do not blank them.

A few settings are fixed by the compose file and cannot be changed in `.env`:
`DATABASE_URL` and `CAMOUFOX_URL`, which name the services on the compose
networks; `AGENT_PROFILES_DIR`, which names the profiles volume;
`BACKUP_DIR`, which is `/backups`, the container side of the `./backups`
mount (move the backups by changing the mount's host side); `DEBUG`,
which is forced off; and `HTTP_ADDR`, which the image sets to `:8000` and the
port mapping publishes. Change `AGENTIFI_PORT` instead.

Outside compose (a binary run from source, or systemd), the binary reads the
same names from its environment and from a `.env` in its working directory,
and the environment wins.

### Changing settings from the app

**Settings → Server admin → Settings** changes most of the settings below
that are not secrets: SimpleFIN, the sync schedule, the feature switches,
the mailbox, the assistant, sign-in limits, passkeys and email. The order is
fixed and shown on the screen:

1. a variable set in the environment or `.env` wins, and the screen shows
   it as set by the environment, read-only;
2. otherwise what was saved on the screen;
3. otherwise the default.

A save is checked the way the next start reads it, so a value the server
would refuse to start with is refused at the save. SimpleFIN, the assistant's
allowed hosts and the login attempt limits apply as soon as they are saved;
every other setting there applies when the server next starts, and the screen
says so beside it. Secrets never appear on the screen, and neither do the
settings the compose file fixes, `PRIMARY_CURRENCY`, or the backup and
single sign-on settings, which have screens of their own where a saved value
wins over the environment.

## Secrets

The `secrets` service writes one file per secret into `./data/secrets` on
every start, and the other services mount that directory at `/run/secrets`:

| File | What it is |
| --- | --- |
| `POSTGRES_PASSWORD` | the database password |
| `SECRET_KEY` | signs session tokens; changing it signs everyone out |
| `CAMOUFOX_WS_PATH` | the Camoufox browser server's websocket path, its only protection |
| `DATABASE_URL`, `CAMOUFOX_URL` | built from the files above on every start |

A value set in `.env` is written as given, so `.env` stays authoritative.
Otherwise a missing file is generated once, from 32 random bytes, and kept.

Postgres applies its password only when `./data/postgres` is empty. After
that, `POSTGRES_PASSWORD` must go on matching the database's: changing it in
`.env` alone breaks the connection. Change it in the database first
(`ALTER ROLE`), then here.

`CREDENTIAL_ENCRYPTION_KEY` seals every stored credential (SimpleFIN Access
URLs, connector sessions and passwords, mailbox secrets, the assistant's API
key). Unset, it is derived from `SECRET_KEY`, which ties the two together; see
[Secrets](operations.md#secrets) before rotating either.

Any setting the application treats as a secret — `SECRET_KEY`,
`CREDENTIAL_ENCRYPTION_KEY`, `DATABASE_URL`, `CAMOUFOX_URL`,
`OIDC_CLIENT_SECRET`, `SMTP_PASSWORD`, `VAPID_PRIVATE_KEY`,
`OPENEXCHANGERATES_APP_ID` — may instead be a file named after it under
`/run/secrets` (or `$CREDENTIALS_DIRECTORY`), where a process listing cannot
read it. A non-empty environment variable wins over the file. A file placed in
`./data/secrets` must be readable by uid 65532, the user the application runs
as.

## The stack

| Variable | Default | |
| --- | --- | --- |
| `AGENTIFI_IMAGE` | `ghcr.io/cornhead764/agentifi` | the application image, without a tag; Camoufox is the same name with `-camoufox` |
| `AGENTIFI_TAG` | `latest` | a version or commit sha; pins both images |
| `AGENTIFI_PORT` | `8100` | the host port the app is published on |
| `FRONTEND_URL` | `http://localhost:${AGENTIFI_PORT}` | see below |
| `TZ` | `UTC` | see below |
| `POSTGRES_USER`, `POSTGRES_DB` | `agentifi` | created only when `./data/postgres` is empty |
| `COMPOSE_PROJECT_NAME` | the directory's name | prefixes the browser-profiles and Chrome volumes |

`FRONTEND_URL` is the address a browser reaches the app on, scheme and port
included. The app works on any address that reaches the server, because the
binary serves the pages and the API from one origin. `FRONTEND_URL` is where
the single sign-on callback and links back into the app point, the one other
origin allowed to call the API, and whether the sign-on cookie is marked
Secure (it is when the address is `https://`).

`TZ` is the zone `SYNC_AT` is read in, and the zone the Camoufox browser
reports to the sites it signs in to. Set `TZ` to the deployment's own zone.

An existing install moved to another directory keeps its signed-in browser
profiles only with `COMPOSE_PROJECT_NAME` set to the original directory's
name.

## Money and banks

| Variable | Default | |
| --- | --- | --- |
| `PRIMARY_CURRENCY` | `USD` | what aggregates are reported in |
| `SUPPORTED_CURRENCIES` | `USD,EUR,GBP,CAD,AUD,CHF,JPY,MXN,BRL,INR,SEK,DKK,NOK,PLN,CZK,NZD` | |
| `OPENEXCHANGERATES_APP_ID` | unset | FX rates for accounts in another currency ([free tier](https://openexchangerates.org/signup/free)) |
| `SIMPLEFIN_ENABLED` | `false` | the only bank aggregator; setup tokens are pasted in the app |
| `SYNC_ENABLED` | `true` | the daily bank sync and connector pulls; Sync now works either way |
| `SYNC_AT` | `04:00` | HH:MM in `TZ` |
| `SYNC_CHECK_MINUTES` | `5` | how often the scheduler looks for due work |
| `MERCHANT_CATALOG_ENABLED` | `true` | look Costco item numbers up once each, so receipt lines read as products; needs the three store settings below |
| `COSTCO_CATALOG_SHOP_ID` | unset | the warehouse the item lookup asks; with any of the three unset nothing is looked up |
| `COSTCO_CATALOG_ZONE_ID` | unset | the zone that goes with the shop |
| `COSTCO_CATALOG_POSTAL_CODE` | unset | a postal code the shop delivers to |
| `INVESTMENT_NEWS_ENABLED` | `true` | the portfolio's news carousel |
| `MARKET_PRICES_ENABLED` | `true` | the manual price refresh |

With `OPENEXCHANGERATES_APP_ID` unset, the install makes no calls for exchange
rates and pays nothing. Optional house and car estimates have no setting of
their own: an account set to take its value from Zillow or Kelley Blue Book is
priced by opening that site's public estimate page in the Camoufox browser, so
the option exists wherever `CAMOUFOX_URL` is set, as it is under compose. You
are responsible for complying with the terms of any third-party site you
configure.

## Backups

**Settings → Server admin → Backups** edits all three of these, and what it
saves wins; these are the values until something is saved there.

| Variable | Default | |
| --- | --- | --- |
| `BACKUP_AT` | `03:30` | the nightly set's time, HH:MM in `TZ` |
| `BACKUP_KEEP_DAYS` | `14` | days of sets to keep; the newest intact set is always kept |
| `BACKUP_RECIPIENTS` | empty | comma-separated age public keys (`age1…`) to encrypt the sets to; empty writes them unencrypted |
| `BACKUP_DIR` | empty | where the sets are written; empty turns the backups off. Fixed to `/backups` by the compose file |

The image carries `pg_dump` and `pg_restore` at the compose file's Postgres
major version. What is backed up, the encryption, and how to restore are in
[Backups](operations.md#backups).

## Sign-in

| Variable | Default | |
| --- | --- | --- |
| `ACCESS_TOKEN_EXPIRE_MINUTES` | `1440` | session lifetime |
| `LOGIN_MAX_ATTEMPTS` | `20` | password attempts per address per window |
| `LOGIN_ATTEMPT_WINDOW_SECONDS` | `300` | |
| `TRUSTED_PROXY_CIDRS` | unset | reverse proxies whose `X-Forwarded-For` is believed, comma-separated |
| `WEBAUTHN_RP_NAME` | `Agentifi` | |
| `WEBAUTHN_RP_ID`, `WEBAUTHN_ORIGIN` | unset | pin passkeys to one domain; unset follows the browser's origin |

`TRUSTED_PROXY_CIDRS` unset trusts no proxy and meters the login limiter on
the real TCP peer, which a client cannot spoof. Set it to the proxy in front of
the app when there is one. Passkeys need HTTPS and a hostname; see
[HTTPS](operations.md#https).

### Single sign-on (OIDC)

| Variable | Default |
| --- | --- |
| `OIDC_ENABLED` | `false` |
| `OIDC_PROVIDER_NAME` | `OIDC` |
| `OIDC_DISCOVERY_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | unset |
| `OIDC_REDIRECT_URI` | `${FRONTEND_URL}/auth/oidc/callback` |
| `OIDC_SCOPES` | `openid email profile` |
| `OIDC_AUTO_REGISTER` | `false` |
| `OIDC_REQUIRE_VERIFIED_EMAIL` | `true` |
| `OIDC_LINK_EXISTING_EMAIL` | `false` |

These are the defaults a fresh install starts from. Every one is also
settable in Settings → Server admin, and what is saved there wins and takes
effect on the next sign-in. `OIDC_AUTO_REGISTER` lets an unknown identity
create an account; `OIDC_LINK_EXISTING_EMAIL` lets a verified address adopt an
existing local account. Both are off because an open or careless provider
would otherwise let strangers in.

## Notifications

Unset `SMTP_HOST` leaves email off; the notifications page says so.

| Variable | Default | |
| --- | --- | --- |
| `SMTP_HOST` | unset | |
| `SMTP_PORT` | `587` | 465 is implicit TLS, detected from the port |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | unset | a LAN relay commonly wants neither |
| `SMTP_FROM` | the username | needed when the username is not an address |
| `SMTP_STARTTLS` | `true` | for 587 and 25 |
| `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY` | unset | web push; both or neither |
| `VAPID_SUBJECT` | `mailto:admin@example.com` | a `mailto:` address you control |

Without a VAPID keypair each start makes a new one, which invalidates every
push subscription. Push needs a secure context, so on plain HTTP the keys are
configured and dormant.

## The bill mailbox

The watched mailbox is connected in the app. These tune it:

| Variable | Default | |
| --- | --- | --- |
| `EMAIL_POLL_MINUTES` | `30` | how often it is read |
| `EMAIL_LOOKBACK_DAYS` | `30` | how far back its first read goes |
| `EMAIL_OTP_RELAYS` | unset | addresses an SMS-to-email forwarder sends relayed codes from, comma-separated |
| `EMAIL_FORWARDERS` | unset | addresses beyond the mailbox's own domain whose forwarded mail is read as the original |

## The assistant

The model connection is configured per space in the app
([`assistant.md`](assistant.md)).

| Variable | Default | |
| --- | --- | --- |
| `ASSISTANT_ALLOWED_HOSTS` | unset | hosts a space's assistant may point at, comma-separated; unset is unrestricted |
| `AUTOMATION_WORKERS` | `1` | automation runs at once; raise it for a model server that batches |

A single household running its own model wants `ASSISTANT_ALLOWED_HOSTS`
unset. A server shared by people who should not reach each other's network
sets it, so the assistant cannot be turned into a proxy for an internal
service.

## Internals

| Variable | Default | |
| --- | --- | --- |
| `DATABASE_MAX_CONNS` | `10` | the connection pool's cap |
| `STORAGE_PATH` | `/data/attachments` in the image | where attachments are kept |
| `AGENTIFI_BROWSER_HEADFUL` | `false` | show Chrome's window; for a workstation with a display |
| `AGENTIFI_CHROME_PATH` | `/chrome/current/chrome` | the Google Chrome the connectors launch; the default is where the `chrome` service installs it ([operations.md](operations.md#the-browsers)) |
| `PLAYWRIGHT_DRIVER_PATH` | set by the image | |
| `DEBUG` | `false` | accepts a default or short `SECRET_KEY`; forced off by the compose file |
