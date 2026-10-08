# Operating Agentifi

How to run Agentifi on a host of your own: first install, upgrades and
rollback, secrets, backups, restoring, HTTPS and the Camoufox browser. Every
command runs in the directory that holds `docker-compose.yml`, the clone of
this repository. Every setting is in [`configuration.md`](configuration.md).

## The stack

[`docker-compose.yml`](../docker-compose.yml) runs five services:

| Service | What it is |
| --- | --- |
| `secrets` | a one-shot that writes the secrets into `./data/secrets`, generating any that are missing |
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
(see [Secrets](#secrets)), and `migrate` creates the database, a SQLite file
at `./data/db/agentifi.db`. Most installs want two settings
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

`docker compose up` starts, in order: `secrets`, `migrate` and `chrome`,
then the application and Camoufox. `docker compose ps`
should show `agentifi` healthy within a minute; its healthcheck is the binary probing
itself (`/agentifi healthcheck`).

Every variable in [`configuration.md`](configuration.md) is honoured by
setting it in `.env`, because compose hands the whole file to the container.
An empty value is a value: `SYNC_AT=` sets the empty string, and deleting the
line is what gets the default.

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

`serve` refuses a database that is behind the binary's own migrations, not
only an empty one, and exits saying to run `agentifi migrate`. Compose runs
`migrate` first, so a normal upgrade never meets this.

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

### Upgrading from a Postgres-backed install

An install whose compose file still has a `postgres` service keeps its data
in `./data/postgres`, which this version does not read. `agentifi
import-postgres --from <postgres URL>` copies every row of it into the SQLite
file, in one transaction: it checks foreign keys, every table's row count and
the exact sum of every money column against Postgres before it commits, and
on any failure writes nothing. It refuses a target that already holds rows, a
Postgres database at a schema version other than the last Postgres release's,
and any table or column the two schemas do not share. `./data/postgres` is
only read, so it stays as the way back until it is removed by hand.

The `migrate` service has no network, so the copy runs in a container of its
own beside a temporary Postgres on a temporary network. The old secrets
service left the database's address in `data/secrets/DATABASE_URL`, naming
the host `postgres`, which the temporary container answers to.

```sh
# with the old compose file still in place, stop the old stack
docker compose down
git pull
docker compose pull
# creates ./data/db, owned by the application's user
docker compose run --rm secrets

docker network create agentifi-import
docker run -d --name agentifi-postgres-import --network agentifi-import --network-alias postgres \
  -v "$PWD/data/postgres:/var/lib/postgresql/data" docker.io/postgres:17-alpine
docker exec agentifi-postgres-import pg_isready   # repeat until it accepts connections

docker run --rm --network agentifi-import \
  -v "$PWD/data/secrets:/run/secrets:ro" -v "$PWD/data/db:/data/db" \
  ghcr.io/cornhead764/agentifi:latest \
  import-postgres --from "$(sudo cat data/secrets/DATABASE_URL)"

docker rm -f agentifi-postgres-import
docker network rm agentifi-import
docker compose up -d
```

With `AGENTIFI_IMAGE` or `AGENTIFI_TAG` set in `.env`, name that image in
place of `ghcr.io/cornhead764/agentifi:latest`. The import prints each table's
row count and ends with the totals it matched. If it refuses because the
SQLite file already holds rows (the new stack was started before the
import), stop the stack, remove `data/db/agentifi.db` and its `-wal` and
`-shm` files with `sudo`, and run it again; a file that `migrate` created and
nothing wrote to is accepted as it is.

Then check a number, not a page: net worth and the current month's spending
plan should match what the old install showed. Once satisfied, remove
`data/postgres`, `data/secrets/POSTGRES_PASSWORD` and
`data/secrets/DATABASE_URL` with `sudo rm -rf`, and the `POSTGRES_*` and
`DATABASE_URL` lines from `.env`, which nothing reads. The backup sets the old
install wrote stay listed as not intact (see
[Checks, retention and failures](#checks-retention-and-failures)).

## Secrets

The secrets live in `./data/secrets`, one file each, and the other services
mount that directory at `/run/secrets`. On every start the `secrets` service
writes `SECRET_KEY` and `CAMOUFOX_WS_PATH`: a value set in `.env` is written
as given, and otherwise a missing file is generated once and kept.
`CAMOUFOX_URL` is rebuilt from them. So an
install whose `.env` already carries these keeps using them, and one that
never set them never has to.

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

Other secrets (`SMTP_PASSWORD`, `OIDC_CLIENT_SECRET`, `VAPID_PRIVATE_KEY` and
so on) may be set in `.env`, or placed in a file named after them in
`data/secrets`, owned by uid 65532, where a process listing cannot read them.

## Backups

The application writes the backups itself, into `./backups` on the host
(mounted at `/backups`, which is `BACKUP_DIR`). Each backup is a *set*, one
directory named by its local time and what took it, such as
`backups/2026-03-03_033000_nightly/`:

- `database.sqlite.age`: a consistent `VACUUM INTO` snapshot of the SQLite
  file, taken while the application runs,
- `attachments.tar.gz.age`: the attachments, the only application state
  outside the database,
- `secrets.tar.gz.age`: `data/secrets`. Without `SECRET_KEY` (or
  `CREDENTIAL_ENCRYPTION_KEY`) the stored connections in the database cannot
  be decrypted.
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

A lock file beside the database, `agentifi.db.backup-lock`, keeps two sets
from being written at once: the server's, one from `docker compose exec`, and
the one `migrate` takes before an upgrade.

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

The snapshot passes `PRAGMA integrity_check` before it is encrypted, and one
that fails it fails the run. That is what **Verified** means in the list. The
snapshot sits briefly in plaintext in `./data/db/.agentifi-snapshot-*`, beside
the database rather than in `./backups`, and is removed once sealed.
**Intact** means every file is on disk at the size the manifest recorded,
which needs no key. A rehearsal (below) proves a key opens the set.

After a set is written, sets older than the days to keep (`BACKUP_KEEP_DAYS`
until saved, default 14) are deleted, except the newest intact set, which is
kept whatever its age. A failed run deletes nothing.

Sets are manifest format 2. A set written by a Postgres-backed install
(format 1) holds a Postgres dump, which this version cannot restore: the list
shows it as not intact, and retention never deletes it. Delete those
directories by hand once they are no longer wanted.

The screen shows the last runs and the full error of a failed one. A nightly
run that fails, or cannot start (no writable directory, no database
file), raises **Server backup failed** for every server
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
identity or upload the key file and **Rehearse**. The set's database is
decrypted into a scratch file beside the live one
(`agentifi.db.rehearse-<time>`), checked for integrity and schema version,
every table's rows are counted, the attachments and secrets archives are read
through, and the scratch file is removed. The live database is not touched,
so a rehearsal is safe while the server runs, and the identity is used for
that one request and never stored. From the shell:

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

1. checks each file against its manifest checksum and decrypts in memory;
2. unpacks the attachments into a staging directory beside the current ones;
3. takes a set of the current database, attachments and secrets, encrypted
   to the saved keys; if that fails, nothing changes;
4. decrypts the database into `agentifi.db.restore-<time>` and checks its
   integrity and schema version; if that fails, the new file is removed and
   the live one is as it was;
5. checks nothing has the database file open, moves `agentifi.db` (and any
   `-wal` and `-shm` files) aside as `agentifi.db.before-restore-<time>`, and
   renames the new file in;
6. sets the current attachments aside and moves the restored ones in;
7. applies this binary's migrations;
8. removes the replaced database and attachments (`--keep-previous` keeps
   them, and says where).

It refuses while anything else has the database file open: the application,
or a command run with `docker compose exec`. Stop it first (`docker compose
stop agentifi`).

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

If the image cannot run, the sets open with the age CLI alone; the `sqlite3`
shell is optional, for checking the file. `data/db` belongs to uid 65532, so
the set is decrypted beside `docker-compose.yml` and moved in with `sudo`. An
application stopped cleanly leaves no `-wal` file; one left by a crash holds
the newest writes to the replaced database, so move it aside beside
`agentifi.db.before-restore` instead of removing it if that copy matters.
For an unencrypted set, read the file directly in place of `age -d -i …`.

```sh
docker compose stop agentifi
age -d -i agentifi-backup-key.txt backups/<set>/database.sqlite.age > agentifi.db.new
sqlite3 agentifi.db.new 'PRAGMA integrity_check'   # optional; prints ok
sudo mv data/db/agentifi.db data/db/agentifi.db.before-restore
sudo rm -f data/db/agentifi.db-wal data/db/agentifi.db-shm
sudo mv agentifi.db.new data/db/agentifi.db && sudo chown 65532:65532 data/db/agentifi.db
sudo mv data/attachments data/attachments.before-restore
age -d -i agentifi-backup-key.txt backups/<set>/attachments.tar.gz.age | sudo tar -xz -C data/
sudo chown -R 65532:65532 data/attachments
docker compose up -d
```

`migrate` runs on that `up` and brings an older set's database up to the
current schema.

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
- **Web push.** The service worker and `PushManager` are unavailable, so a
  configured VAPID keypair registers no subscriptions. Email alerts still
  work.

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
- `VAPID_*` for web push.
- `ASSISTANT_ALLOWED_HOSTS`, which restricts the hosts a space may point its
  assistant at (see [`assistant.md`](assistant.md)).
- `OIDC_*` for single sign-on. These are defaults; what is saved in
  Settings → Server admin wins.
- `OPENEXCHANGERATES_APP_ID` for foreign-exchange rates; unset, the install
  makes no calls for them.
