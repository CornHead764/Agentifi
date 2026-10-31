# Contributing

Read [`AGENTS.md`](AGENTS.md) first: the ground rules and traps in it
(decimal money, one filter model, a pure calculation core) are enforced by
tests, and its map says where each part of the code lives.

## One rule above the others: no real financial data

This is a finance application. Fixtures are invented, never captured. Nothing
copied from a real account enters the repository: not a figure, a payee, an
account name, a screenshot or an export. A figure copied out of a real export
is a leak even with no name beside it, because it reconciles with the rest of
that export.

## Setup

You need Go (the version in `backend/go.mod`), Node 26 and Python 3.
[mise](https://mise.jdx.dev/) is optional and runs the tasks below.

```sh
cd frontend && npm install
```

### Nix (optional)

If you manage tools with Nix, `nix develop` (or direnv, via `.envrc`) gives
Go, Node, mise and PostgreSQL. mise still runs every task, using the Nix
copies of Go and Node. The versions are pinned in `flake.nix`; keep them in
step with `backend/go.mod` and `frontend/mise.toml`. PostgreSQL is only for
the legacy import tests below.

### Storage

The backend runs on SQLite: `DATA_DIR` (default `./data`) contains `server.db`
and one `households/<id>.db` per household. Database-backed tests create
temporary SQLite files themselves; no database service or client tools are
required, including for backup round trips.

The binary reads the environment and a `.env` in its working directory, so
`go run` from `backend/` reads `backend/.env`. For a local development server:

```sh
printf 'DEBUG=true\nDATA_DIR=./data\n' > backend/.env
```

Only the one-time legacy import tests need a PostgreSQL source. They skip
when `TEST_DATABASE_URL` is unset, and fail instead when `CI` is set, as CI
sets it; the ordinary SQLite tests do not skip for lack of a service. To exercise legacy import locally, the optional helper
starts an isolated source database without Docker or root:

```sh
uv run --python 3.12 --with pgserver scripts/dev-postgres.py
export TEST_DATABASE_URL="postgres://postgres@/agentifi_test?host=$PWD/.dev-postgres"
cd backend && go test ./internal/pgimport ./cmd/agentifi
# From the repository root, stop it when done:
uv run --python 3.12 --with pgserver scripts/dev-postgres.py --stop
```

`DATABASE_URL` is only a legacy import source, never the application's
runtime storage. Leave it unset for a fresh installation. See
[Upgrading from Postgres](docs/operations.md#upgrading-from-postgres).

### Running it

```sh
cd backend
go run ./cmd/agentifi migrate
go run ./cmd/agentifi user add --email you@example.com --superuser
go run ./cmd/agentifi serve             # API and the embedded app on :8000
cd ../frontend && npm run dev           # live-reloading UI on :5173
```

`serve` embeds the frontend from `backend/internal/web/dist`, which holds only
a committed `.gitkeep` until you build into it with `mise run //frontend:embed`.
Don't delete that `.gitkeep`: without it `go build` fails in a fresh clone.

## Checks

Run what CI runs before you push:

| What | Command | With mise |
| --- | --- | --- |
| Backend build | `cd backend && go build ./...` | `mise run //backend:build` |
| Backend format and vet | `cd backend && test -z "$(gofmt -l .)" && go vet ./...` | `mise run //backend:lint` |
| Backend tests | `cd backend && go test ./internal/...` | `mise run //backend:test` |
| Frontend lint | `cd frontend && npm run lint` | `mise run //frontend:lint` |
| Frontend tests | `cd frontend && npm run test -- --run` | `mise run //frontend:test` |
| Frontend types | `cd frontend && npm run build` | `mise run //frontend:build` |
| Frontend layout | `cd frontend && npm run layout` | `mise run //frontend:layout` |
| Browser tests | `cd backend && AGENTIFI_BROWSER_TEST=1 go test ./internal/billers/ ./internal/browser/ ./internal/merchants/` (setup below) | |

**`npm run build` is the only typecheck.** Vitest strips types without
checking them, so a type error passes the test suite and fails the build.
(`npm run typecheck` runs the same `tsc -b` on its own.)

**`npm run layout` renders every page in a real browser.** It needs
Chromium once, from `cd frontend && npx playwright install chromium` (add
`--with-deps` on a machine without its system libraries). It starts vite on
port 5176, answers every API call from the invented fixtures in
`frontend/layout/fixtures/`, and checks each top-level page at 390, 820, 1180
(with the accounts drawer open) and 1440px wide. The failures name the page,
the width and the element; screenshots of every page land in the gitignored
`frontend/layout/output/screenshots/`. [`docs/development.md`](docs/development.md#the-layout-check)
says what it checks and how its allowlist works.

**The browser tests drive a real Google Chrome** against invented pages
(sign-in walls, payment drafts, merchant order pages) in `internal/billers`,
`internal/browser` and `internal/merchants`. They skip unless
`AGENTIFI_BROWSER_TEST=1` is set, so the backend tests above leave them out;
CI's Browser job runs them. Locally they need two things:

- **Google Chrome**, at `AGENTIFI_CHROME_PATH`. Not Playwright's Chromium,
  which some providers block: the engine launches Google Chrome and nothing
  else. A Chrome from Google's own package is at `/opt/google/chrome/chrome`;
  on a Debian or Ubuntu workstation, `scripts/install-chrome.sh <dir>` puts
  the current stable at `<dir>/current/chrome`, as the compose file's
  `chrome` service and CI do.
- **The Playwright driver.** playwright-go's own download of it fails, so
  `scripts/playwright-driver.sh` assembles it from npm into
  `~/.cache/ms-playwright-go/<version>`, where playwright-go looks (or into
  `PLAYWRIGHT_DRIVER_PATH`). The version is the one the playwright-go module
  in `backend/go.mod` pins. It copies the `node` on your `PATH` into the
  driver, so run it again after upgrading that module or Node.

```sh
scripts/playwright-driver.sh
export AGENTIFI_CHROME_PATH=/opt/google/chrome/chrome
cd backend && AGENTIFI_BROWSER_TEST=1 go test ./internal/billers/ ./internal/browser/ ./internal/merchants/
```

Every calculation has unit tests over invented data, which run in CI, with
expected values from the rules in [`docs/calculations.md`](docs/calculations.md).
A second, optional and local suite, the golden tests, compares the
calculations with the figures in a real Simplifi export; they run only where
an export is on disk and skip everywhere else, including CI. Run them against
your own export ([`docs/importing.md`](docs/importing.md)) after any change to
a calculation. The browser, live and golden suites are listed, with how to
run each, in [`docs/development.md`](docs/development.md#optional-suites).

## Dependency updates

[Renovate](https://docs.renovatebot.com/) opens the dependency-update pull
requests, early on Monday (UTC), from [`renovate.json5`](renovate.json5). Its
Dependency Dashboard issue lists everything pending, and can open an update
ahead of the schedule. Nothing merges by itself.

- **Minor and patch updates come one pull request per ecosystem:** Go
  modules, npm packages, GitHub Actions (pinned to a commit SHA with the
  version in a comment), Docker images, mise tools, and the Nix flake inputs
  (`nix flake update`). An npm release waits three days before it is
  offered.
- **A major update is a pull request of its own**, one per package. Postgres
  majors are never offered: a major cannot open the data directory of the one
  before it.
- **Go and Node versions move in one pull request each**, "Go toolchain" and
  "Node toolchain", across every file that names them: the Dockerfile stages,
  the `go` directives, `actions/setup-node`, `mise.toml`, and the nixpkgs
  attributes (`go_1_26`, `nodejs_26`) in `flake.nix` and `nix/`.
- **playwright-go comes alone.** It runs only the driver version it pins, so
  its pull request says what to change by hand: `nix/playwright-driver.nix`
  (version and hash) and `playwright==` in `tools/camoufox/Dockerfile`.

A Go update changes `vendorHash` in `nix/package.nix`. On Renovate's pull
requests the `nix hashes` workflow builds the package, and commits the hash
Nix reports (`scripts/nix-hashes.sh`, which a person can run too). npm
updates need no hash: the Nix build reads each package's hash from
`package-lock.json`. The workflow pushes with its own token, so the checks on
its commit wait until someone with write access selects **Approve workflows
to run** on the pull request. To have them start by themselves, add a
repository secret `NIX_HASHES_TOKEN`: a fine-grained personal access token
for this repository only, with Contents read and write.

Before merging an update:

- Every check is green on the newest commit, the `nix` check included. A
  red `nix` check on Renovate's own commit is expected until the hash
  commit lands.
- For a major or a toolchain update, read the release notes the pull
  request links. CI builds both images and runs the browser tests, but not
  the Camoufox providers, so a Camoufox or Python image update is worth one
  sign-in by hand.
- A Go toolchain update needs nixpkgs to carry the new `go_1_N`; until the
  flake inputs do, its `nix` check fails, and it waits for them.

## Changes

- Keep a change to one concern, with a test that fails without it. What that
  test should prove is in
  [`docs/development.md`](docs/development.md#what-a-test-proves).
- Write docs, comments and test names for a reviewer new to the repository:
  plain English, short sentences, terms defined where they first appear. A
  doc comment is one or two lines; a longer explanation goes in `docs/`.
  Plans and proposals go in issues, not `docs/`. The full standard is under
  "Writing code here" in [`AGENTS.md`](AGENTS.md).
- How the code is laid out and how to add an API resource, a connector, an
  assistant tool, a migration or a screen is
  [`docs/development.md`](docs/development.md).
- A new derived number gets a named function in `backend/internal/domain/` and
  a rule in [`docs/calculations.md`](docs/calculations.md).
- A new bill provider starts at
  [`docs/connectors/adding-a-bill-provider.md`](docs/connectors/adding-a-bill-provider.md).

Security problems go through [`SECURITY.md`](SECURITY.md), not a public issue.
