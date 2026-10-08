# AGENTS.md

Instructions for coding agents (and people) working in this repository.

Agentifi is a self-hosted personal finance manager whose feature set is
modelled on Quicken Simplifi: one Go binary that serves the API and an
embedded React app, over a SQLite file. What it does and how to install it is in
[`README.md`](README.md); setup and the checks CI runs are in
[`CONTRIBUTING.md`](CONTRIBUTING.md). This file is the rules and a map.
Follow the links for depth.

## Where the truth lives

| Question | Document |
| --- | --- |
| What does a derived number mean, and what should it be? | [`docs/calculations.md`](docs/calculations.md). The one that matters most: a finance app that renders a wrong number is worse than one that renders nothing. |
| What are the entities and their invariants? | [`docs/data-model.md`](docs/data-model.md) |
| How do the packages fit together? | [`docs/architecture.md`](docs/architecture.md) |
| How do I add a resource, a connector, a tool, a migration, a screen? | [`docs/development.md`](docs/development.md) |
| How do bank sync, bill providers and merchants work? | [`docs/connectors/`](docs/connectors/README.md); a new bill provider is [`adding-a-bill-provider.md`](docs/connectors/adding-a-bill-provider.md) |
| How does a Simplifi, CSV or OFX import work? | [`docs/importing.md`](docs/importing.md) |
| What can the assistant do? | [`docs/assistant.md`](docs/assistant.md) |
| How is it installed, backed up and configured? | [`docs/operations.md`](docs/operations.md), [`docs/configuration.md`](docs/configuration.md) |

## Ground rules

Code cites these as "ground rule N"; keep the numbering.

1. **Money is decimal, always.** On the server it is `domain.Money` over
   `shopspring/decimal`, parsed from a string, never from a float. A money
   column holds INTEGER hundredths and any other decimal its exact text, and
   both go through `dbconv`. It crosses the wire as a string. The client
   coerces it once, in `lib/api.ts`, into `Money` (integer cents), and
   combines amounts only through `lib/money.ts`. A rate, a price, a share
   count or a percentage is `domain.Rate`, not `Money`.
2. **Every derived number is a named function in `internal/domain` with a
   test**, and is specified in [`docs/calculations.md`](docs/calculations.md).
   The test's expected value comes from the specification or from a real
   Simplifi export (the golden tests), never from running the implementation.
   A screen, a report and the assistant read the same function, so no copy of
   a rule is ever edited alone.
3. **One `Filter` object, one editor.** Watchlists, planned-spend envelopes,
   reports, rules and the transaction list share one filter entity.
   `components/transactions/FilterFacets.tsx` is its only editor,
   `lib/transactions/filter.ts` the only encoder of its wire shape and
   `lib/reports/savedFilter.ts` the inverse. A screen that asks "which
   transactions?" mounts `FilterFacets` and adds no second draft model.
4. **Two exclusion flags, at four levels.** `excluded_from_reports` and
   `excluded_from_spending_plan` are independent, and each is set on the
   transaction, the account, the **category** and the rule; the category pair
   is the one that gets forgotten. A report or total asks
   `domain.CountsAsIncomeOrExpense` or `domain.CountsTowardSpendingPlan`
   rather than restating their clauses. A split row's category lives on its
   splits, so a split path asks `Category.CountsAsIncomeOrExpense` and
   `Category.CountsTowardSpendingPlan` per split.
5. **Two names per transaction.** `statement_name` is the bank's wording and
   never changes; `payee` is the editable clean name. Matching (rules, bill
   series, merchants, deduplication) reads the statement name; display reads
   the payee.
6. **SimpleFIN is the only bank aggregator**, with no widget or OAuth flows.
   Accounts imported from Simplifi arrive unlinked and are linked to SimpleFIN
   accounts in a separate step that sets a sync floor. Merchants are not
   banks: Amazon and Costco orders arrive as files or from a connector and
   are matched to bank rows by amount and date. The merchant is a value from
   `domain.Merchants`, decided by a row's wording; a Costco row is never
   offered an Amazon order.
7. **The spending plan is materialized per month.** `spending_plan_months`
   holds one row per month with each bucket's calculated amount, the ids of
   the rows that contributed, the rows excluded and the person's overrides.
   `domain.RecalculateChain` computes it and `service/plan.go` writes it; a
   closed-out month keeps what it said. Read
   [`docs/calculations.md`](docs/calculations.md) before changing it.
8. **No real financial data in the repository.** Fixtures are invented, never
   captured. A figure copied from a real export is a leak even with no name
   beside it, because it reconciles with the rest of that export. Real
   exports live in the gitignored `data/`.

## Traps

Rules that are easy to break. Each has a test; code cites them as "trap N",
so keep the numbering.

1. **Decimal columns serialize as JSON strings.** Coerce them once, at the
   API client, by declaring the fields in the client's `MoneyShape`. A money
   field left out of a shape arrives as a string, which `auditUndeclaredMoney`
   reports in development.
2. **Release a transfer pair before deleting either leg.** A leg whose
   partner is gone stays out of income and expense with nothing balancing it,
   so the money silently stops existing in the reports.
   `domain.FindOrphanTransferLegs` finds such legs and
   `POST /transfers/orphans/repair` releases them.
3. **Transfer pairing runs on synced and imported rows only**
   (`pairableSources` in `service/transfer.go`). A hand-entered row is never
   paired behind the person's back.
4. **`effective_date`, not `date`, is the reporting date.** The two can be a
   statement cycle apart for a card charge. Reports, the spending plan and
   totals read the effective date.
5. **An omitted date filter is not "all time" everywhere.** A page that pairs
   a list endpoint with a summary endpoint sends both the same window. On the
   server every list endpoint resolves its window through
   `WindowFromRequest` in `api/window.go` and echoes the window it used.
6. **Excluding an account-backed holding is half a rule.** A holding filed
   under a brokerage account is already inside that account's balance, so a
   total that filters holdings out must add the owning account's balance back
   in the same function (as `portfolioValue` in `api/investments.go` does,
   through `domain.CashOutsideHoldings`).
7. **A recurring series' `description` is matching input, not a label.**
   Display reads `label`, which is `display_name` or else `description`
   (`domain.Series.Label`); no matching path reads the label.

## Map

### Top level

| Path | What it is |
| --- | --- |
| `backend/` | The Go module: `cmd/agentifi` (the only binary), `internal/` (every package), `migrations/` (goose SQL, embedded) |
| `frontend/` | The React app (Vite, TypeScript, TanStack Query): `src/`, and `layout/` for the Playwright layout check |
| `docs/` | Everything that is not setup; [`docs/README.md`](docs/README.md) is the index |
| `tools/agentifi-mcp/` | An MCP server that exposes a running instance to an MCP client |
| `tools/camoufox/` | The Camoufox (Firefox) browser container for providers that run in Firefox |
| `tools/extractors/` | The browser-console script that saves a Simplifi dataset as a JSON file |
| `scripts/` | `dev-postgres.py` (a throwaway Postgres for the `pgimport` tests), `playwright-driver.sh` (the driver the browser tests need), `install-chrome.sh` (Google Chrome stable from Google, run by the compose file's `chrome` service and CI; the image does not carry Chrome) |
| `Dockerfile`, `docker-compose.yml`, `.env.example` | The image and the self-hosted stack |
| `mise.toml` | Optional task runner; `backend/mise.toml` and `frontend/mise.toml` hold the tasks |

### Backend (`backend/internal/`)

| Concern | Where |
| --- | --- |
| Every calculation, pure | `domain/`. Imports only the standard library, `shopspring/decimal` and `golang.org/x/text`; `purity_test.go` enforces it. `Money` is a distinct type with no `FromFloat`. |
| HTTP routes | `api/`, one file per resource whose `init()` calls `Register`. `rt.Read` and `rt.Write` are the only tenant-scoped routes; `route_contract_test.go` enforces it. Errors map to statuses in `errors.go`. |
| In-process calls (the assistant's reach) | `api/dispatch.go` serves every `Read` and `Write` route with the caller's own space and permissions. The chi route context must be cleared, and not every endpoint answers JSON. |
| Assistant tools | Catalogue in `domain/assistant.go`; read tools run in `api/assistant_tools.go`, write tools become routes in `api/assistant_actions.go` |
| SQL and row mapping | `store/` (hand-written SQL); `dbconv/` converts `Money` to INTEGER hundredths and other decimals to exact text |
| The database handle | `sqlitedb/`: a SQLite file through the pure-Go modernc driver; rewrites `$N` placeholders to `?N`, converts times, dates and JSON both ways, and registers `now()`, `gen_random_uuid()`, `ts_add()`, `regexp` and `decimal_sum()` on every connection. One process owns the file. |
| Orchestration and background work | `service/`: sync, settling, transfers, rules, the spending plan (`plan.go`), the scheduler |
| Outside services | `provider/`: SimpleFIN, prices, valuation, mailboxes, the model, web push |
| Bill providers | `billers/`, one module per provider on shared sign-in and reading helpers, listed in `billers/registry.go`; facts in `domain.Billers`. A new one starts with `agentifi probe-sign-in <url>`. |
| Merchants (Amazon, Costco) | `merchants/`, one module each, listed in `merchants/merchant.go`; facts in `domain.Merchants` |
| The sign-in and pull engine | `connector/`, shared by bills and merchants |
| Billing mail | `billmail/`, offline recognizers of a provider's mail |
| Browsers | `browser/` (Google Chrome in-process through playwright-go, launched from `AGENTIFI_CHROME_PATH`, where the `chrome` service installs it, and never Playwright's Chromium; Camoufox at `CAMOUFOX_URL` for a module that implements `browser.FirefoxProvider`, with no fallback to Chrome; a Camoufox script runs in an isolated world and reads what the page stores, never hooks what it sends), `browser/agent/` (the contract between `connector` and the modules) |
| Imports | `importer/` (Simplifi), with `csvimport/`, `ofximport/` and `merchantimport/` beneath it |
| Identity | `auth/` (tokens, passwords, passkeys, TOTP, OIDC), `totp/` |
| Settings | `config/`, from the environment and a `.env` |
| Backups | `backup/`: a `VACUUM INTO` snapshot, integrity-checked and sealed with age, and the restore that swaps a file in |
| Moving a Postgres-backed install | `pgimport/`, run as `agentifi import-postgres --from <url>` |
| Test support | `testdb/` (a per-process SQLite file), `storetest/` (`Main`, `DB`, row fixtures) |
| Migrations | `backend/migrations/sqlite/`, goose files in SQLite's dialect with Up and Down; `sqlite/00001_initial.sql` is never edited, its header lists the storage classes, and the next migration is `sqlite/00002_*.sql`. `backend/migrations/00001_initial.sql` is the Postgres-backed release's schema, which `import-postgres` reads; it is never applied |

### Frontend (`frontend/src/`)

| Concern | Where |
| --- | --- |
| Routes | `routes.tsx`; navigation in `components/shell/destinations.ts` |
| Screens | `pages/`, one named export per page, its private components in a subdirectory named after it |
| Design-system primitives | `components/ui/` (`Card`, `Table`, `List`/`ListRow`, `PageHeader`, `Badge`, `Callout`, `DialogActions`, …); which one for which shape is in [`docs/development.md`](docs/development.md#which-primitive-for-which-shape) |
| API clients | `lib/clients/`, one module per resource, through `api` in `lib/api.ts`, with every money field declared in a `MoneyShape` |
| Money | `lib/money.ts` (integer cents), rendered by `components/Money.tsx` |
| The one filter | `lib/transactions/filter.ts` (encoder), `lib/reports/savedFilter.ts` (decoder), `components/transactions/FilterFacets.tsx` (editor) |
| Styles | `styles/`, with every colour, length and size a token in `styles/tokens.css` |
| Test helpers | `test/renderScreen.tsx`; `pages/screens.smoke.test.tsx` renders every screen with no data |
| Layout check | `frontend/layout/`: `routes.ts` (every route), `fixtures/` (invented API answers), `allowlist.ts`, `checks.ts` |

## Commands

From the repository root unless a `cd` says otherwise.

```sh
# Setup
cd frontend && npm install

# Backend (database-backed tests make their own SQLite files; nothing to start)
cd backend && go build ./...
cd backend && test -z "$(gofmt -l .)" && go vet ./...
cd backend && go test ./internal/...

# The pgimport tests also need a Postgres (no Docker, no root; data in the gitignored .dev-postgres/)
uv run --python 3.12 --with pgserver scripts/dev-postgres.py
export TEST_DATABASE_URL="postgres://postgres@/agentifi_test?host=$PWD/.dev-postgres"
cd backend && CI=1 go test ./internal/pgimport/  # CI=1 makes a missing Postgres fatal

# Browser tests (real Google Chrome, invented pages)
scripts/playwright-driver.sh
export AGENTIFI_CHROME_PATH=/opt/google/chrome/chrome   # or scripts/install-chrome.sh <dir>; <dir>/current/chrome
cd backend && AGENTIFI_BROWSER_TEST=1 go test ./internal/billers/ ./internal/browser/ ./internal/merchants/

# Frontend
cd frontend && npm run build                      # the only typecheck
cd frontend && npm run test -- --run
cd frontend && npm run lint                       # ESLint, then stylelint
cd frontend && npm run layout                     # needs `npx playwright install chromium` once

# Run it
cd backend && go run ./cmd/agentifi migrate && go run ./cmd/agentifi serve   # :8000
cd frontend && npm run dev                                                   # :5173
```

[`CONTRIBUTING.md`](CONTRIBUTING.md) has the details: the `.env` a local
server reads and the mise equivalents.

## Tests that skip look like tests that pass

- The database-backed tests (`store`, `service`, `api`, the importers) each
  make their own SQLite file and do not skip for want of a database. The
  exception is `internal/pgimport`, whose end-to-end tests read a Postgres at
  `TEST_DATABASE_URL` and **skip** without one. Start one as above, and run
  with `CI=1` to turn a skip into a failure.
- Vitest strips types without checking them: only `npm run build` (or
  `npm run typecheck`) catches a type error.
- Browser tests skip without `AGENTIFI_BROWSER_TEST=1`; `AGENTIFI_LIVE_*`
  tests reach real sites and run only by hand; golden tests skip without a
  real Simplifi export in `data/simplifi/`.

## Writing code here

- Match the surrounding code: its naming, comment density and idiom. The
  frontend has no formatter; do not run Prettier or reformat files you touch.
- Comment only to state a constraint the code cannot show. Comments and docs
  describe the code as it is, with no history and no dates; that belongs in
  the commit message.
- Keep a change to one concern, with a test that fails without it. A new
  derived number gets a function in `internal/domain` and a rule in
  [`docs/calculations.md`](docs/calculations.md).
- Fixtures are invented (ground rule 8): round figures and names that belong
  to nobody.
