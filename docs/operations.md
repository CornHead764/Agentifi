# Operating Agentifi

How to run Agentifi on a host of your own: first install, upgrades and
rollback, secrets, backups, restoring, HTTPS and the Camoufox browser. Every
command runs in the directory that holds `docker-compose.yml`, the clone of
this repository. Every setting is in [`configuration.md`](configuration.md).

## The stack

[`docker-compose.yml`](../docker-compose.yml) runs six services:

| Service | What it is |
| --- | --- |
| `secrets` | a one-shot that writes the secrets into `./data/secrets`, generating any that are missing |
| `postgres` | retained Postgres 17 source in `./data/postgres`, used only for the one-time legacy import; not application storage |
| `chrome` | a one-shot that downloads Google Chrome stable from Google into the `chrome` volume (see [The browsers](#the-browsers)) |
| `migrate` | a one-shot `agentifi migrate`, run before the application; it backs up before changing the schema |
| `agentifi` | the application: the API, the frontend, the scheduler, the nightly backup and an in-process Chrome |
| `camoufox` | the Camoufox (Firefox) browser server for providers that run in Firefox |

The application image and the Camoufox image share a name:
`AGENTIFI_IMAGE` (default `ghcr.io/cornhead764/agentifi`) names the first,
and the second is the same name with `-camoufox` appended. CI publishes both with the same tags on every build of
`master` (`latest` and the commit sha) and on a `v*` tag (also the version),
so `AGENTIFI_TAG` always pins a matching pair.

## First install

```sh
git clone https://github.com/CornHead764/agentifi.git
cd agentifi
docker compose up -d
```

The app is then on port 8100 of the host. Nothing has to be set first:
`SECRET_KEY` and the Camoufox websocket path are generated on the first start
(see [Secrets](#secrets)). SQLite stores the application's data in
`./data/server.db` and `./data/households/<id>.db`. The retained source database
is only for [legacy upgrades](#upgrading-from-postgres). Most installs want two settings
soon after, in a `.env` beside `docker-compose.yml` (copy
[`.env.example`](../.env.example)):

- `TZ`, the host's time zone. The daily sync runs in it, and the browser
  reports it to the sign-in pages, some of which check it.
- `FRONTEND_URL`, the address people reach the app on, once that is not
  `http://localhost:8100`. Single sign-on and links back into the app use it;
  the app itself works on any address that reaches the server.

`docker compose up -d` again applies a change.

The first account is created on the sign-in screen: a server with no
accounts offers to make one, with a space of its own, and that account
administers the server. Two people trying at once get one account and one
refusal, and once any account exists the screen offers only sign-in. Open
the app before exposing the port to anyone else, since until then whoever
reaches it first becomes the administrator. An OIDC sign-in with automatic
registration on is the same: the first account it creates administers the
server.

From the shell, `docker compose exec agentifi /agentifi user add --email
you@example.com --superuser` makes an account instead. `user add` prompts
for a password, creates the account with a space of its own (`--space`,
default "Household"; `--currency`) and makes it change the password at first
sign-in unless `--no-force-change` is given. `--superuser` lets that account administer the server from the app.
`agentifi user passwd --email <address>` resets a password.

`agentifi user admin --email <address> --on` lets an existing account
administer the server, and `--off` takes that away. The last active
superuser keeps it: make another account a superuser first.
`agentifi user list` prints every account with whether it is active and
whether it is a superuser, which is the way back in when nobody remembers
which account administers the server.

`docker compose up` prepares secrets and the legacy import source, runs
`migrate` and `chrome`, then starts the application and Camoufox. `docker compose ps`
should show `agentifi` healthy within a minute; its healthcheck is the binary probing
itself (`/agentifi healthcheck`).

Every variable in [`configuration.md`](configuration.md) is honoured by
setting it in `.env`, because compose hands the whole file to the container.
An empty value is a value: `SYNC_AT=` sets the empty string, and deleting the
line is what gets the default.

## Upgrading from Postgres

The SQLite release imports an existing Postgres installation automatically
when `agentifi migrate` runs and `DATA_DIR/server.db` does not exist.
`docker compose up -d` runs that migration before starting the application.
Keep the Postgres service, its data volume, `DATABASE_URL` secret, and the old
`STORAGE_PATH` attachment directory available for this first upgrade.
Compose retains Postgres only for this one-time import; a later release
removes it.
Compose forces `DATA_DIR=/data/db` for both application and migration, backed by
host `./data/db`; a `DATA_DIR` entry in `.env` cannot move it off that mount.

Runtime attachments always live in `DATA_DIR/attachments`, and stored keys
are relative to `DATA_DIR`, beginning with `attachments/`. `STORAGE_PATH` is
only the legacy import input (default `./data/attachments`), not a runtime
override. Compose sets it to `/data/attachments` for `migrate` and mounts the
old host `./data/attachments` directory there read-only. Imported and newly
uploaded attachments live in host `./data/db/attachments` instead. If the old
installation stored attachments elsewhere, change only the host side of the
legacy mount in `migrate` to that directory. Ensure uid 65532 can read the
source files; the secrets service no longer changes their ownership.

Stop the old application before upgrading and keep it stopped. The migration
takes PostgreSQL table locks without waiting: an in-flight legacy writer causes
an error telling you to stop the old agentifi. These locks prevent writes during
the repeatable-read snapshot, verification and publication; they do not prevent
an old application from resuming writes after the importer exits.

The migration copies data and attachments into a staging directory inside
`DATA_DIR`, verifies the import, then publishes `server.db` last. Just before
publication it durably records a one-time `agentifi_sqlite_migrated` marker in
the source PostgreSQL schema, outside `DATA_DIR`. The legacy data and attachments
are otherwise unchanged and remain the fallback snapshot. The import user needs
permission to lock the source tables and create this marker table. Keep the old
image tag and Postgres data until you have checked the imported households and
attachments.

For a non-Compose installation, preserve the old configuration and run:

```sh
DATA_DIR=/path/to/sqlite DATABASE_URL='postgres://...' \
  STORAGE_PATH=/path/to/old/attachments agentifi migrate
DATA_DIR=/path/to/sqlite agentifi serve
```

The importer requires the last Postgres schema supported by this release;
an older schema or an unrepresentable value stops the upgrade with an error.
Fix the reported source issue and rerun `agentifi migrate`. Before publication,
a crashed import's staging directory is discarded and retried. Once the durable
PostgreSQL marker is recorded, a missing `server.db` (including an accidentally
deleted or repointed `DATA_DIR`) makes both migrate and serve refuse: restore the
SQLite data directory from backup, rather than re-importing the stale Postgres
snapshot. An interruption between recording the marker and publication also
fails closed; retain any surviving verified staging files for recovery. Once
`server.db` exists, subsequent migrations never import Postgres again.

SQLite backup retention never deletes legacy format-1 `pg_dump` backup sets,
even when they are older than the retention window or cannot be restored by the
SQLite binary. Keep these sets for rollback and remove them manually only when
you no longer need the PostgreSQL fallback.

Starting `agentifi serve` directly against an empty `DATA_DIR` while reachable
Postgres holds users or spaces refuses with an instruction to run
`agentifi migrate`; it does not silently start a new installation. A fresh
installation is allowed only when `DATABASE_URL` is unset or reachable Postgres
contains no users or spaces. With `DATABASE_URL` set, both commands retry an
unreachable source for up to 30 seconds, then refuse and name its host.
Start Postgres or unset `DATABASE_URL` explicitly to create a fresh installation.
To roll back, stop the SQLite application
and run the previous image with its original Postgres database and attachment
directory. Writes made in SQLite after the upgrade are not copied back.

## The browsers

The bill and merchant connectors sign in to websites with a real browser:
Google Chrome, inside the application container, driven through
playwright-go.

**The image does not include Chrome.** Google's terms do not allow
redistributing it, so each install downloads its own from Google, and that
copy is subject to [Google's terms](https://www.google.com/chrome/terms/).
The one-shot `chrome` service runs `scripts/install-chrome.sh` on every
start: it reads Google's apt repository index
(`https://dl.google.com/linux/chrome/deb/dists/stable/main/binary-amd64/Packages`),
downloads the `google-chrome-stable` package it lists over HTTPS, checks it
against the SHA-256 the index gives, and unpacks it into the `chrome`
volume, which the application mounts read-only at `/chrome`. An install
tracks Chrome stable, updated whenever the stack starts:

- The first start downloads about 120 MB; the volume holds about 450 MB per
  version.
- A later start installs the newer stable when the index lists one, beside
  the installed version, and switches to it only once it is fully unpacked,
  so a failed or partial download never breaks the Chrome that is there.
  The version before it is kept, for a browser still running from it.
- A start that cannot reach Google keeps the installed Chrome, says so in
  `docker compose logs chrome`, and exits 0.
- With no Chrome installed and no route to Google, the service exits 1. The
  application starts anyway (it waits for `chrome` but does not require
  it), and everything except the connectors works; a connector fails with an
  error that says how to install Chrome. `docker compose up -d` once the
  host can reach `dl.google.com` installs it.

```sh
docker compose exec agentifi /agentifi browser-selftest
```

launches that Chrome, loads a page and prints a PDF, and reports where the
driver and Chrome came from: the package, URL and checksum the `chrome`
service installed it from, and the version that launched. The connectors
need it to pass; nothing else does.

**A host with no route out** gets Chrome by copying the volume's contents
from a machine that has one. On a Docker host that can reach Google, with
the same image:

```sh
docker volume create chrome-seed
docker run --rm -v chrome-seed:/chrome --entrypoint install-chrome \
  ghcr.io/cornhead764/agentifi:latest /chrome
docker run --rm -v chrome-seed:/chrome --entrypoint tar \
  ghcr.io/cornhead764/agentifi:latest -C /chrome -cf - . > chrome.tar
```

then on the offline host, in the directory that holds `docker-compose.yml`:

```sh
docker compose run --rm -T --no-deps --entrypoint tar chrome -C /chrome -xf - < chrome.tar
```

The next start finds that Chrome, cannot reach Google, and keeps it.
`AGENTIFI_CHROME_PATH` points the engine at a Chrome somewhere else
([configuration.md](configuration.md#internals)).

Providers that run in Firefox are served by the `camoufox`
container ([`tools/camoufox/`](../tools/camoufox/README.md)), and so
does the optional estimate for a house or car (Zillow, Kelley Blue Book). A
Playwright server has no authentication of its own, and during a pull the
browser behind it is signed in to a provider, so the container publishes no
port, shares a network (`agentifi-browser`) with the application only, and
refuses to start unless its websocket path is at least 24 characters. That
path is the shared secret: the `secrets` service generates it into
`./data/secrets/CAMOUFOX_WS_PATH` and builds the application's `CAMOUFOX_URL`
from the same value. To change it, delete both files (or set
`CAMOUFOX_WS_PATH` in `.env`) and run `docker compose up -d --force-recreate
agentifi camoufox`. A provider that needs Camoufox fails with a clear
error when `CAMOUFOX_URL` is unset; it never falls back to Chrome.

The connectors' signed-in browser profiles live in the
`merchant-agent-profiles` volume, one per connection. Compose prefixes the
volume with the project name, which is the directory's name unless
`COMPOSE_PROJECT_NAME` says otherwise, so moving the directory needs
`COMPOSE_PROJECT_NAME` set to the old name.

## Upgrades and rollback

```sh
git pull
docker compose pull agentifi camoufox && docker compose up -d
```

Every `up` runs `migrate`, which takes a backup set before it applies a
migration (see [Backups](#backups)), then starts the application. To pin or
roll back, set `AGENTIFI_TAG` (in `.env` or on the
command line) to a commit sha or version. The compose file and the image go
together, so a rollback past a change to `docker-compose.yml` checks out that
commit's file too. Rolling the application back does not roll back a
migration it has already applied; restore the set taken before the upgrade
(named `…_upgrade`) if the schema must go back too.

Agentifi tells its administrators when a newer release is out: Server admin
shows the running version beside the newest one, and the app shows a notice
until it is dismissed for that release. The check is one anonymous request to
`api.github.com` for the repository's tags, made at most every six hours and
only when an administrator opens the app; the newest `vX.Y.Z` tag is the
release, whether or not a GitHub Release page was written for it. Nothing is
sent but the request. `UPDATE_CHECK_ENABLED=false` (or the toggle in Server
admin) turns it off. A build that was not stamped with a version, as `go run`
is, reports no update.

`serve` refuses a database that is behind the binary's own migrations, not
only an empty one, and exits saying to run `agentifi migrate`. Compose runs
`migrate` first, so a normal upgrade never meets this.

New households copy a blank schema template built once per application process
from the embedded migrations, including their recorded versions and change-capture
triggers. Each new file gets its own household identity and clock; no household
data is cached in the template. Existing files still follow the normal migration
and identity checks, and schema upgrades still require the pre-migration snapshot.

After an upgrade, `docker compose ps` should show `agentifi` healthy. A
`migrate` that exited with an error is a held upgrade: `docker compose logs
migrate` says why, and a backup that could not be written is the usual
reason. A `git pull` brings the compose file up to date; a new setting
missing from `.env` gets its default.

An import or sync that was interrupted can leave rows waiting on the settle
that follows it (rules, transfer pairing, recurring matches, running
balances). A space's next sync clears that backlog. For a space that does not
sync, `docker compose exec agentifi /agentifi settle --space <id>` (or
`--owner-email <address>`) settles it.

## Secrets

The secrets live in `./data/secrets`, one file each, and the other services
mount that directory at `/run/secrets`. On every start the `secrets` service
writes `SECRET_KEY` and `CAMOUFOX_WS_PATH`: a value set in `.env` is written
as given, and otherwise a missing file is generated once and kept.
`CAMOUFOX_URL` is rebuilt from them. Compose also retains
`POSTGRES_PASSWORD` and `DATABASE_URL` solely for the one-time legacy import;
preserve these until that upgrade is verified. An install whose `.env`
already carries its secrets keeps using them.

`SECRET_KEY` signs session tokens. The application refuses to start with one
shorter than 32 characters. Changing it signs everyone out.

`CREDENTIAL_ENCRYPTION_KEY` seals every stored credential: the SimpleFIN
Access URLs, the bill and merchant connectors' sessions and kept passwords
and authenticator keys, mailbox secrets and the assistant's API key. Left
empty, it is derived from `SECRET_KEY`, which ties the two together: rotating
`SECRET_KEY` would then make every sealed credential unreadable, and so would
losing `./data/secrets`. Before rotating `SECRET_KEY`, set
`CREDENTIAL_ENCRYPTION_KEY` in `.env` to the derived value:

```sh
printf 'agentifi:credential-encryption:v1:%s' "$(cat data/secrets/SECRET_KEY)" | sha256sum
```

(the hex digest is the value). Then delete `data/secrets/SECRET_KEY`, or set a
new `SECRET_KEY` in `.env`, and run `docker compose up -d`. There is no re-key
command, so changing `CREDENTIAL_ENCRYPTION_KEY` itself strands every sealed
credential; each connection then has to be set up again.

The retained legacy import source initializes its password only on first
use. Do not rotate its source credentials during the upgrade; see
[Upgrading from Postgres](#upgrading-from-postgres).

Other secrets (`SMTP_PASSWORD`, `OIDC_CLIENT_SECRET` and so on) may be set in `.env`, or placed in a file named after them in
`data/secrets`, owned by uid 65532, where a process listing cannot read them.

## Backups

The application writes the backups itself, into `./backups` on the host
(mounted at `/backups`, which is `BACKUP_DIR`). Each backup is a *set*, one
directory named by its local time and what took it, such as
`backups/2026-03-03_033000_nightly/`:

- `database.tar.gz.age`: standalone SQLite snapshots of `DATA_DIR/server.db`
  and each active `DATA_DIR/households/*.db`, including committed WAL data,
- `attachments.tar.gz.age`: the contents of `DATA_DIR/attachments`, with paths
  relative to that directory (the archive format is unchanged),
- `secrets.tar.gz.age`: `data/secrets`. Without `SECRET_KEY` (or
  `CREDENTIAL_ENCRYPTION_KEY`) the stored connections in the dump cannot be
  decrypted.
- `manifest.json`: plaintext, and nothing secret in it: when the set was
  taken and why, the size and SHA-256 of each file as stored, the schema
  version, and the public keys it was encrypted to.

A set is taken:

- **nightly**, at the time set in **Settings → Server admin → Backups**
  (`BACKUP_AT` until one is saved, default `03:30`, in `TZ`). A server that
  was down at that time takes it when it starts again.
- **before every upgrade that changes the schema.** `agentifi migrate` writes
  a set before it applies a migration to a database that holds data, and a
  set it cannot write holds the upgrade: migrate exits with the reason and
  the application does not start. `docker compose run --rm migrate migrate
  --skip-backup` goes ahead without one.
- **before a restore**, of what the restore is about to overwrite.
- **before a space is deleted** (`…_space-delete`). An owner deletes a space
  from **Settings → Spaces & sharing**, typing its name; the delete waits for
  the set, and a set that fails deletes nothing. With backups off the space
  is deleted with no set behind it, and the dialog says so. Restoring that
  set brings the space back, along with everything else as it was then.
- **on request**: the button on the same screen, or
  `docker compose exec agentifi /agentifi backup`.

### Encryption

Every file in a set is encrypted with [age](https://age-encryption.org) to
the public keys (recipients, `age1…`) saved under **Backups**. The server
holds only the public half, so a copy of `backups/`, or the whole machine's
backup including `data/secrets`, cannot be read without the private key
(the identity, `AGE-SECRET-KEY-1…`), which is kept somewhere else.

**Generate a key pair** makes one in the browser, shows the private key once
with a download (`agentifi-backup-key.txt`), and saves only the public key.
**Save to password manager** in the same dialog submits the key as a login,
user name `agentifi-backup@<host>` and the key as its password, which the
browser's own password manager or an extension such as Bitwarden offers to
save; where the browser supports the Credential Management API in a secure
context (Chromium over HTTPS) it is asked directly as well. The rehearsal
form asks for the key under the same login, so the manager fills it back
in. Keep the download too: a password manager is one copy, not a backup.

`age-keygen` makes a key pair too; paste its public key under **Add a public
key**. An SSH public key you already keep the private half of works the same
way: `ssh-ed25519`, or `ssh-rsa` of at least 2048 bits, pasted as a line
from `~/.ssh/*.pub`. Its comment becomes the label when none is given, and
the list shows its SHA-256 fingerprint as `ssh-keygen -l` prints it. Its
private key, passphrase-protected or not, then opens the sets. These are
age's own SSH recipient types, so `age -d -i ~/.ssh/id_ed25519` opens a set
without the application. ECDSA and security-key (`sk-`) SSH keys cannot be
encrypted to. There is no plain RSA or OpenSSL format beside these: age
already covers RSA keys with a vetted, specified construction (RSA-OAEP
wrapping a per-file key), and a second format would be a second thing to
get right and a set the age CLI cannot read.

Any one of several keys opens a set, so a second person or an offline copy
can each have their own. Lose every key a set was encrypted to and the set
is lost: no one can decrypt it.

Until a key is saved, sets are still written, unencrypted, and the screen
says so in a warning that stays until a key is added. Every file is mode
0600 and every set directory 0700, owned by uid 65532, encrypted or not.

### Checks, retention and failures

Each database is snapshotted with SQLite's `VACUUM INTO`, then opened and
checked with `PRAGMA integrity_check`. **Verified** means every snapshot
returned `ok`. **Intact** means every file is on disk at its recorded size;
restore also checks its SHA-256. A rehearsal proves a key opens the set.

The manifest format is now **2**. Old Postgres dump sets are refused: restore
them with the previous release, then run `agentifi import-postgres` to move
the recovered data into SQLite.

After a set is written, sets older than the days to keep (`BACKUP_KEEP_DAYS`
until saved, default 14) are deleted, except the newest intact set, which is
kept whatever its age. A failed run deletes nothing.

The screen shows the last runs and the full error of a failed one. A nightly
run that fails, or cannot start (for example, no writable backup directory),
raises **Server backup failed** for every server
administrator, in each household they belong to: on the bell, and by push or
email where those are switched on for it. The next successful run withdraws
it.

`docker compose exec agentifi /agentifi backup list` lists the sets from the
shell.

### Keeping a copy elsewhere

The sets sit on the same disk as the data. The machine's own backup, or a
copy job of your own, is what takes `backups/` off the host; with a key
saved, that copy is safe to keep anywhere. To write the sets somewhere else
on the host, change the host side of the `./backups:/backups` mounts on both
`agentifi` and `migrate` in `docker-compose.yml`; the directory must be
owned by uid 65532.

**Not backed up:** the `merchant-agent-profiles` volume. Losing it costs each
connector one fresh sign-in, not data. It is as sensitive as the database
(anyone holding it is signed in to those accounts), so restrict access to the
host accordingly.

## Restoring

### Rehearse first

On **Server admin → Backups**, **Restore…** on a set, then paste the
identity or upload the key file and **Rehearse**. The SQLite files are unpacked
into a scratch directory, opened and integrity-checked, and every table's rows
are counted. The attachments and secrets archives are read through, then the
scratch directory is deleted. Live data is not touched; the identity is used
for that one request and never stored. From the shell:

```sh
docker compose exec -T agentifi /agentifi restore --from 2026-03-03 --rehearse \
  --identity - < agentifi-backup-key.txt
```

`--from` takes a set's name, a day (that day's newest intact set), or
`latest`.

`--identity` takes the age key file or an SSH private key. For an SSH key
with a passphrase, the screen shows a passphrase field once the identity is
an SSH key; from the shell the passphrase is read from
`BACKUP_IDENTITY_PASSPHRASE`, or asked for on the terminal when there is
one. `docker compose run -T` and `exec -T` have none, so pass it through:

```sh
read -s BACKUP_IDENTITY_PASSPHRASE && export BACKUP_IDENTITY_PASSPHRASE
docker compose exec -T -e BACKUP_IDENTITY_PASSPHRASE agentifi /agentifi restore \
  --from 2026-03-03 --rehearse --identity - < ~/.ssh/id_ed25519
```

### Restore

A restore overwrites the live database and attachments, so it runs on the
host with the application stopped, not from the screen: the running server
would go on writing syncs, imports and automations into the database being
overwritten. The screen shows these commands once the set's name is typed
back:

```sh
docker compose stop agentifi
docker compose run --rm -T migrate restore --from <set> --confirm <set> \
  --identity - < agentifi-backup-key.txt
docker compose up -d
```

`--confirm` is the set's name, typed back. In order, the restore:

1. takes the `DATA_DIR/server.lock` instance lock, refusing if the server is running;
2. checks the database checksum, unpacks snapshots into `DATA_DIR/.restore-<timestamp>/`,
   opens every file and checks its integrity;
3. unpacks the attachments into staging and takes a set of the current database,
   attachments and secrets, encrypted to the saved keys; failure changes nothing;
4. moves current data into `DATA_DIR/.before-restore-<timestamp>/`, preserving lock
   files and dot-directories, and publishes staged files with `server.db` last;
5. sets the current attachments aside and moves restored ones in;
6. applies this binary's migrations, then removes staging.

The replaced data directory contents are always kept for recovery. The
`--keep-previous` flag also keeps replaced attachments. Staging and swapping
entries inside `DATA_DIR` works when it is a Docker bind mount.
There is no `--disconnect`: stop the server before restoring.

Secrets are not restored in place: the install keeps its own. Each set
records which credential key sealed its stored connections, and a restore
refuses a set sealed with a different `SECRET_KEY` or
`CREDENTIAL_ENCRYPTION_KEY` until that set's secrets are put back (below), or
`--ignore-key-mismatch` restores it anyway and each connection is set up
again.

Then check a number, not a page: sign in and confirm net worth and the
current month's spending plan match what they were before. A restore from the
wrong set is undone by restoring the set it took first (named `…_restore`).

### On a new host

The secrets go back before the first start, since starting without them
generates new ones. With the [age CLI](https://age-encryption.org):

```sh
git clone https://github.com/CornHead764/agentifi.git && cd agentifi
# copy the backups/ directory across, then:
mkdir -p data
age -d -i agentifi-backup-key.txt backups/<set>/secrets.tar.gz.age | sudo tar -xz -C data/
docker compose run --rm -T migrate restore --from <set> --confirm <set> \
  --identity - < agentifi-backup-key.txt
docker compose up -d
```

A `SECRET_KEY` in `.env` overrides the restored file.

### Without the application

If the image cannot run, format-2 sets open with the age CLI and standard tar.
Stop the application first, unpack into a fresh directory, and check each `.db`
with `sqlite3 FILE 'PRAGMA integrity_check'` before replacing the live files.
For an unencrypted set, read the archive directly instead of using `age -d`.

```sh
docker compose stop agentifi
mkdir -p recovered
age -d -i agentifi-backup-key.txt backups/<set>/database.tar.gz.age | tar -xz -C recovered
# Check recovered/databases/server.db and every recovered/databases/households/*.db.
sudo mv data/db data/db.before-restore
sudo mv recovered/databases data/db
sudo mv data/attachments data/attachments.before-restore
age -d -i agentifi-backup-key.txt backups/<set>/attachments.tar.gz.age | sudo tar -xz -C data/
sudo chown -R 65532:65532 data/db data/attachments
docker compose up -d
```

`migrate` on that `up` brings recovered SQLite data up to the current schema.
For format-1 Postgres sets, use the previous release to restore, then run
`agentifi import-postgres`; do not unpack them into `DATA_DIR`.

## HTTPS

The stack serves plain HTTP on `AGENTIFI_PORT`. Put a TLS-terminating reverse
proxy with a hostname in front of it (Caddy, Traefik, nginx), set
`FRONTEND_URL` to the `https://` address people use, and set
`TRUSTED_PROXY_CIDRS` to the proxy's address so the login rate limiter reads
the real client from `X-Forwarded-For`. Empty, it trusts no proxy and meters
on the TCP peer, which is right when the app is reached directly.

Reached over plain HTTP by an IP address or any name other than `localhost`,
the application works, but browsers withhold two features outside a secure
context:

- **Passkeys.** WebAuthn requires a secure context. The login page hides the
  passkey button and Settings → Security says passkeys need HTTPS.
- **Web push.** The service worker and `PushManager` are unavailable, so no
  browser can subscribe. Email alerts still work.

Both turn on once the app is served over HTTPS with a hostname. Nothing else
changes: `FRONTEND_URL`, and for passkeys possibly `WEBAUTHN_RP_ID` and
`WEBAUTHN_ORIGIN` (empty follows the browser's origin).

## Other settings

[`configuration.md`](configuration.md) lists every setting, and most that are
not secrets can be changed in **Settings → Server admin → Settings**, beneath
whatever `.env` sets. The ones most installs touch:

- `SIMPLEFIN_ENABLED`, and `SYNC_ENABLED` / `SYNC_AT` for the daily bank sync
  and connector pulls.
- `SMTP_*` for email alerts; unset `SMTP_HOST` leaves email off.
- `VAPID_SUBJECT` for web push, which otherwise needs no setting: the server
  makes its own keypair and keeps it in the database.
- `ASSISTANT_ALLOWED_HOSTS`, which restricts the hosts a space may point its
  assistant at (see [`assistant.md`](assistant.md)).
- `OIDC_*` for single sign-on. These are defaults; what is saved in
  Settings → Server admin wins.
- `OPENEXCHANGERATES_APP_ID` for foreign-exchange rates; unset, the install
  makes no calls for them.
