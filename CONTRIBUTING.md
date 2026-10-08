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

You need Go (the version in `backend/go.mod`), Node 22, Python 3 and, to
change the API's protos, [buf](https://buf.build/docs/installation) 1.47.2.
[mise](https://mise.jdx.dev/) is optional and runs the tasks below.

```sh
cd frontend && npm install
```

### A database

There is nothing to set up. The database is a SQLite file, and every
database-backed test creates its own in a temporary directory, so the backend
tests run, rather than skip, on a bare checkout.

The one exception is `internal/pgimport`, which copies an install from the
earlier Postgres-backed release and is tested end to end against a real
Postgres. Those tests read `TEST_DATABASE_URL` and **skip without it, which
looks exactly like passing** (`CI=1` makes that a failure).
`scripts/dev-postgres.py` runs a PostgreSQL that ships its own binaries, with
no Docker and no root, over a unix socket with its data in the gitignored
`.dev-postgres/`:

```sh
uv run --python 3.12 --with pgserver scripts/dev-postgres.py        # start it
export TEST_DATABASE_URL="postgres://postgres@/agentifi_test?host=$PWD/.dev-postgres"
uv run --python 3.12 --with pgserver scripts/dev-postgres.py --stop # when done
```

Any other Postgres where that user may create databases works too. The root
`docker-compose.yml` is the self-hosted stack, which pulls the published
image; it is not a development environment.

To run the server itself, the binary reads the environment and a `.env` in
its own working directory, so `go run` from `backend/` reads `backend/.env`.
`DATABASE_PATH` defaults to `./data/agentifi.db`, a gitignored file
under `backend/data/` created on first start. `DEBUG=true` lets a workstation run with the default
`SECRET_KEY`:

```sh
echo "DEBUG=true" > backend/.env
```

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
| Protos | `buf lint && buf format -d --exit-code && buf generate`, leaving no diff | |
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

Every calculation has unit tests over invented data, which run in CI. A second,
optional set, the golden tests, compares the calculations with the figures in
a real Simplifi export; they run only where an export is on disk and skip
everywhere else, including CI. Run them against your own export
([`docs/importing.md`](docs/importing.md)) after any change to a calculation.

## Changes

- Keep a change to one concern, with a test that fails without it.
- How the code is laid out and how to add an API resource, a connector, an
  assistant tool, a migration or a screen is
  [`docs/development.md`](docs/development.md).
- A new derived number gets a named function in `backend/internal/domain/` and
  a rule in [`docs/calculations.md`](docs/calculations.md).
- A new bill provider starts at
  [`docs/connectors/adding-a-bill-provider.md`](docs/connectors/adding-a-bill-provider.md).

Security problems go through [`SECURITY.md`](SECURITY.md), not a public issue.
