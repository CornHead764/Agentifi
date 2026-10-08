# Architecture

Agentifi is one Go binary and a SQLite file. The binary carries the
schema, serves the API and the compiled React app, and runs the background
work in goroutines of the same process: there is no queue, no worker service
and no second runtime. The only other container is Camoufox, a browser server
that a few connectors need.

This page is a map. What each number means is in
[calculations.md](calculations.md), the entities in
[data-model.md](data-model.md), the rules for changing the code in
[`AGENTS.md`](../AGENTS.md) and how to add to it in
[development.md](development.md).

## The binary

`backend/cmd/agentifi` is the only command. Its subcommands:

| Subcommand | What it does |
| --- | --- |
| `serve` | The API, the embedded app and the background work |
| `migrate` | Applies the embedded migrations; `serve` refuses a database behind the binary |
| `healthcheck` | The container's probe: polls `/health` of a running `serve` |
| `user add`, `user passwd` | Makes an account, or changes its password |
| `user admin`, `user list` | Grants or takes away the right to administer the server, or lists accounts |
| `import` | Imports a Simplifi export; `--dry-run` reports without writing |
| `import-csv`, `import-ofx` | Imports a CSV, OFX or QFX file into a space |
| `import-postgres --from <url>` | Copies a database written by the Postgres-backed release into an empty `DATABASE_PATH` ([operations.md](operations.md#upgrading-from-a-postgres-backed-install)) |
| `settle` | Finishes rows an import or sync wrote but did not settle |
| `backup`, `backup list` | Takes a backup set now, or lists the sets on disk |
| `restore` | Restores a backup set |
| `browser-selftest` | Launches the installed Chrome and reports its version and where it came from |
| `probe-sign-in <url>` | Reports what a sign-in page shows the page reader |

Migrating is a separate step rather than part of `serve`, so two instances
cannot race to migrate one database. `browser-selftest` and `probe-sign-in`
need no configuration; everything else reads it from the environment and a
`.env` in the working directory (`internal/config`).

`serve` mounts `/health`, the API under `/api`, and the single-page app as
the not-found handler, so every other path, including a deep link, gets the
app. It sets the security headers (a strict Content-Security-Policy among
them) on every response.

## Backend packages

Everything is under `backend/internal/`.

| Package | Responsibility |
| --- | --- |
| `domain` | The pure calculation core: plain structs in, `Money` out, no I/O |
| `store` | Hand-written SQL over `sqlitedb`, the row-to-domain mapping, migrations |
| `sqlitedb` | The database handle: a SQLite file through the pure-Go modernc driver, `$N` placeholders rewritten to `?N`, argument and scan conversions for times, dates and JSON, and the functions it registers on every connection (`now()`, `gen_random_uuid()`, `ts_add()`, `regexp`, `decimal_sum()`) |
| `service` | Orchestration: loads a calculation's inputs, writes its outputs; sync, settling, transfers, rules, the spending plan, the scheduler |
| `api` | HTTP: the route registry, tenancy, serialization, in-process dispatch, the assistant's tools |
| `auth` | Who the caller is and which space a request is about: tokens, passwords, passkeys, TOTP verification, OIDC |
| `config` | Every setting, read from the environment, with what Server admin saved beneath it |
| `buildinfo` | The commit and build time the image build links in, shown in Server admin |
| `provider` | Everything outside the process: SimpleFIN, prices, news, valuation, exchange rates, mailboxes (IMAP and Microsoft Graph), the assistant's model, storage, e-mail and web push |
| `httpx` | One outbound call read the same way for every connector: a capped body, a status error quoting the start of the answer, JSON numbers kept as text, and one retry after a fresh sign-in when the credential is refused |
| `importer` | The Simplifi importer, and `csvimport`, `ofximport` and `merchantimport` beneath it |
| `billers` | One module per bill provider, and the helpers they share |
| `merchants` | One module per merchant (Amazon, Costco): signing in, and pulling orders and invoices |
| `connector` | The one engine that signs in to and pulls from both: `connector.Bills` serves `service.Bills` and `connector.Merchants` serves `service.Merchants` |
| `billmail` | Recognizes billing mail, offline: which provider, what bill, or a sign-in code |
| `browser` | The browsers the connectors drive, with no knowledge of any site |
| `browser/agent` | The contract between `connector` and the modules it drives: a sign-in's states, the sign-in module, the browser a session holds, the Chrome or Camoufox choice, where calls go, pressing a form and waiting for the page to answer |
| `totp` | Mints the six-digit code for a provider that demands one at every sign-in |
| `dbconv` | Exact conversion between `Money` and INTEGER hundredths, and between other decimals and their exact text |
| `backup` | The backup sets: a `VACUUM INTO` snapshot checked for integrity and sealed with age encryption, the manifest, retention, and the restore into a new file that is swapped in |
| `pgimport` | `agentifi import-postgres`: copies a database written by the earlier Postgres-backed release into an empty SQLite file, verifying row counts and money totals |
| `textutil` | String helpers with no domain knowledge, such as `Clip`, which caps text by character rather than byte |
| `web` | Serves the compiled frontend embedded from `web/dist` |
| `testdb` | The per-process SQLite file every database-backed test uses |
| `storetest` | The `TestMain`, the migrated store and the row fixtures of every database-backed test outside `store` |

### The calculation core

`domain` holds every derived number as a pure function over plain structs,
so a wrong number is findable by a unit test. It imports only the standard
library, `shopspring/decimal` and `golang.org/x/text`, and `purity_test.go`
fails the build if it imports anything else. `domain.Money` is a distinct type
over `decimal.Decimal`, not an alias, so a rate, a share count or a
percentage (`domain.Rate`) cannot be added to a balance. It parses from a
string with `domain.FromString` and there is no `FromFloat`. Its JSON methods
write a string and refuse a JSON number. An amount in loosely typed JSON (an
export, a provider's payload, a model's answer) is read by
`domain.MoneyFromJSONValue`: a JSON number from its literal digits, a string
as `domain.ParseMoneyText` reads one, and a `float64` never. User-written patterns are matched
with Go's `regexp`, which is linear in its input, so a rule cannot stall the
server (`domain/saferegex.go`).

`domain` also holds the catalogues other layers read rather than restate:
`domain.Billers` (the bill providers), `domain.Merchants` (the order
merchants), and `domain.AssistantTools` and `domain.AssistantWriteTools`.

### Persistence

`store` is a `sqlitedb` handle and hand-written SQL, with no ORM and no code
generation. Every tenant-owned query takes a space id and puts it in its
`WHERE` clause. A money column holds INTEGER hundredths and every other
decimal its exact text; both are read and written through `dbconv.Number`,
never through `float64`, and a `NULL` in a `NOT NULL` numeric column is an
error rather than a zero. One process owns the file: SQLite serializes
writers (every transaction begins `IMMEDIATE`), and the named locks that keep
two runs of one connection apart are in-process. A few services own the SQL of
their own tables (the spending plan's is in `service/plan.go`).

`service` splits each operation into a pure decision (rows in, a decision or
diff out) and an apply step that writes exactly that decision, so a preview
cannot drift from what is applied. It reimplements no calculation.

### The API

A resource is one file in `internal/api/` whose `init()` calls `Register`.
Nobody edits a shared router: `RouterFor` mounts whatever the registry holds.
`Routes.Read` and `Routes.Write` are the only ways to add a tenant-scoped
route. Both hand the handler a resolved `auth.SpaceContext`, and `Write`
refuses a viewer before the handler runs. The exceptions are named:
`RegisterIdentity` for `/auth` and `/spaces`, which work before a space is
chosen, and `RegisterAdmin` for `/admin`, whose routes are superuser-only.
`route_contract_test.go` walks the registry and fails on any route that breaks
these rules, and checks that the mounted router matches the registry.

Handlers return an error instead of writing a failure status; `errors.go` maps
every refusal. Money crosses the wire as a string in both directions.

List endpoints take their date window through one resolver,
`WindowFromRequest` in `api/window.go`, and echo the window they used, with
`date_field` choosing the posted or the effective date.

### In-process dispatch

`internal/api/dispatch.go` serves every `Read` and `Write` route from inside
the process, against the same handlers a browser reaches. It is how the
assistant reads and changes the application, so a new resource is reachable
by the assistant the day it lands. It runs with the caller's own resolved
space and refuses a viewer a write exactly as the router does. Identity,
administration and the assistant's own routes are unreachable from it, as are
the routes that use a credential (bill, merchant and mailbox sign-ins, kept
passwords and sessions, challenge answers).

Two details matter to anyone touching it. The chi route context of the outer
request must be cleared before dispatching, or chi reuses its consumed path
and every dispatched request 404s. And not every endpoint answers JSON (an
attachment answers its bytes, an export answers CSV), so a response carries
its content type and `IsJSON` decides whether a tool may hand it to a model.

### The assistant

The assistant is a chat and an automation worker over any OpenAI-compatible
chat-completions endpoint (`provider/assistant.go`). Its read tools are
closed catalogues declared in `domain/assistant.go` and answered in
`api/assistant_tools.go` and `api/assistant_reads.go` from the same loaders the
screens use. Its write tools never edit: each becomes a recorded proposal
(`api/assistant_actions.go`) holding the method, path and body it stands for,
which a person applies through in-process dispatch. The automation worker is
`service.Automations`, started by `serve`. See [assistant.md](assistant.md).

### Background work

`serve` starts four long-running goroutines beside the HTTP server:

- **The scheduler** (`service.Scheduler`), which wakes every few minutes and
  runs what is due. The daily jobs belong to the sync window, `SYNC_AT` in the
  server's time zone; a server asleep through the window catches up once when
  it wakes. In order, each under its own panic guard: re-valuing physical
  assets, the day's balance snapshot, exchange rates, purging unlinked
  documents, pruning expired token revocations, the merchant order pulls, the
  bill pulls (after expiring unanswered challenges, and with session
  keep-alives), polling the watched mailbox on its own shorter interval, the
  SimpleFIN sync of each due connection, and last the per-account cash-flow
  forecasts, so they read what the sync brought in.
- **The automation worker** (`service.Automations.Work`), which runs the
  assistant's automations and fails runs a restart interrupted.
- **Browser upkeep** (`Env.KeepBrowsers` in `api/browserupkeep.go`), which
  reaps idle browser sessions and closes them all on shutdown.
- **The nightly backup** (`service.Backups.Run`), when `BACKUP_DIR` is set,
  which writes a set once a day at the saved time, catching up once after a
  server was down through it, and tells the administrators when one fails.
  A lock file beside the database keeps it, `agentifi migrate`'s backup and
  one taken with `docker compose exec` from overlapping. See [operations.md](operations.md#backups).

### Ingest

The SimpleFIN sync and the CSV and OFX importers finish through one entry
point, `settleNewRows`: it
stamps the cash-flow date, runs the rules, matches bill series, pairs
transfers and rewrites running balances. A row stays marked `needs_settle`
until every step succeeds, and `agentifi settle` finishes a backlog.

### The browsers

Chrome runs inside the application process, driven by playwright-go
(`internal/browser`). The image carries Playwright's driver but not Chrome,
whose terms do not allow redistributing it: the compose file's one-shot
`chrome` service runs `scripts/install-chrome.sh`, which downloads Google
Chrome stable from Google's apt repository into the `chrome` volume, and the
engine launches it from `AGENTIFI_CHROME_PATH`. The application never
fetches a browser itself, and with no Chrome installed a connector fails with
an error that says how to get one, never falling back to Playwright's
Chromium. Kept profiles live under
`AGENT_PROFILES_DIR`. Providers that run in Firefox use
Camoufox, a Firefox build served by the separate container in
`tools/camoufox` and reached at `CAMOUFOX_URL`, whose path is the secret the
container was started with. A module opts in by implementing
`browser.FirefoxProvider`; with `CAMOUFOX_URL` unset such a provider fails
with a clear error and never falls back to Chrome. See
[connectors/README.md](connectors/README.md).

The connector engine (`connector`) runs one sign-in loop and one session
store for bill providers and merchants alike: it types a kept password once
per try, presses through a factor page at most twice, gives up on a round
that changes nothing, and keeps a trail of the pages it saw. A module
classifies the page into an `agent.State` and fills or presses what the loop
asks; it never runs a loop of its own. The engine opens, holds and chooses
its browsers through `browser/agent`. `agent.For`
picks the opener a connector runs in and refuses a Camoufox connector with
`browser.ErrNoFirefox` when there is no Camoufox server. `agent.Chrome` opens a
connection's persistent profile when it names one (bills) and a throwaway
context seeded from the sealed state otherwise (merchants); `agent.Firefox`
always opens a fresh Camoufox context. `agent.Fetchers` is where a connector's
calls go: a plain client, or a page at the provider's origin in the browser
the connector runs in. `agent.Hold` is the browser a session drives, closed
once. `agent.Submit` presses a form's button (or Enter) and waits for the page
to differ from what it was before the press (`agent.Signature`,
`agent.AwaitChange`), then settles; the bill providers' own submit ranking
waits the same way.

Every request a connector has a signed-in page make goes through one script,
`browser.PageCallScript`, driven by a `browser.PageCall`: the address,
method, headers and body, the credentials mode, how to read the answer (JSON,
text, bytes, or matches in a large body), and optionally a bearer to read
from the page's storage first (`browser.StorageToken`). The fetcher
(`browser.PageFetcher`), the developer steer's fetch, the bill providers'
`billers.AskPage` and the Costco pull all use it. It reads what the page
stores and never hooks what the page sends, so it behaves the same in
Camoufox's isolated world as in Chrome.

### Migrations

The schema is goose SQL in `backend/migrations/sqlite/`, embedded in the
binary by `migrations/embed.go`, so `agentifi migrate` needs no files on disk.
`sqlite/00001_initial.sql` is the whole schema a new database starts from;
later files apply on top of it. Its header lists the storage class of each
kind of value. `backend/migrations/00001_initial.sql` beside it is the
schema of the Postgres-backed release, never applied: it is the source
`import-postgres` reads, and its tests build their Postgres database from it.

## The frontend

`frontend/` is React 19 and TypeScript, built by Vite. `npm run build` writes
the bundle that `internal/web` embeds; in development `npm run dev` serves it
on port 5173 and proxies `/api` to the backend.

| Path under `frontend/src/` | What lives there |
| --- | --- |
| `main.tsx`, `App.tsx`, `routes.tsx` | Entry point, providers, and every route |
| `pages/` | One component per screen, with its private pieces in a subdirectory (`pages/settings/`, `pages/rules/`, …) |
| `components/` | Shared components; `ui/` is the primitive kit over Radix, `shell/` the navigation, `transactions/` the register |
| `lib/api.ts` | The only code that calls `fetch` |
| `lib/clients/` | Per-resource API clients and their query hooks (the register's are in `lib/transactions/`) |
| `lib/` | Client-side logic with its tests: money, formatting, the filter, reports |
| `contexts/` | Auth, active space, theme, privacy and motion providers |
| `styles/` | Plain CSS over design tokens (`tokens.css`) |

**Routing** is React Router, declared in one file, `routes.tsx`. Each page is
a lazily loaded chunk. `RequireAuth` and `RequireOwnPassword` wrap every
signed-in route and `AppShell` draws the navigation, whose entries are in
`components/shell/destinations.ts`.

**Server state** is TanStack Query. The shared client is in
`lib/queryClient.ts`, and mutations go through `useInvalidatingMutation`,
which names the queries a change invalidates. A panel draws its query through
`QueryBoundary` (`components/QueryBoundary.tsx`): a skeleton while pending,
`LoadFailure` with a retry when it failed. A page that cannot draw at all
without its query shows `LoadFailure` in its place. Typed searches wait for
the typing to stop through `useSettled` (`lib/useSettled.ts`), and a list
filtered on the client, the category tree included, matches through
`matchesSearch` (`lib/search.ts`), which ignores case and accents.

**Preferences kept on the device** go through `lib/storage.ts`, which
namespaces every key, survives a browser that refuses storage, writes flags
as `true`/`false` and parses stored JSON (`readStoredJson`, whose caller
checks the shape). Nothing else touches `localStorage`.

**Text.** Counts, percentages and dates print through `lib/format.ts`:
`plural` and `formatCount` for counts, `formatPercent` for a fraction,
`capitalize`, `asSentence` and `initial` for labels. A value shown to be
copied is a `CopyButton` or `CopyableSecret` from `components/ui`. An
account's name by id is `accountNamer` (`lib/accounts.ts`), or
`useAccountName()` over the space's accounts: "Unknown account" for an id the
loaded list lacks, blank until it loads.

**Date windows** come from `lib/dateRanges.ts`. A chosen range is a token
resolved by `windowOf`; a fixed reach around today (the next 30 days, the
last 90, the reminder strip's 60 back and 30 ahead) is `dayWindow(back,
ahead)`, counted in calendar days; and a month calendar's six weeks are
`calendarGrid`. A day is written with `toIsoDate`, in the viewer's time zone.

**Reporting work.** Work that takes more than a moment (a sync, a pull, a
re-price, a mailbox read) runs through `useProgressToast()` from
`components/ui`: one toast that spins while it runs and is replaced where it
stands by the result or by the failure, a title over `describeApiError`'s
sentence. A mutation run that way has no failure handler of its own. An
instant result is a single `show`, and a failure with nothing to wait for is
`useFailureToast`, which builds the same failure toast. A toast dismisses
itself after the person's `toast_duration_ms`, with a bar along its foot that
drains over that time and stops while Radix pauses the timer; an error or a
running toast stays until dismissed. Every spinner is `<Spinner>`; a busy
button keeps its own beside the toast.

**Shared controls.** A screen reaches for these before drawing its own:

- `OverflowMenu` (`components/ui`) is every row's and card's "more" menu: a
  list of actions, optional named sections, checks and submenus, with danger
  items always gathered at the bottom. Its trigger is `OverflowMenuButton`,
  and its label names what it acts on: "Actions for …".
- `ChipGroup` (`components/ui`) is one choice of a few, drawn as chips. It is a
  radio group: Tab lands on the chosen chip and the arrow keys move the
  choice. `RangeChips` is a `ChipGroup` of date presets. A choice between
  panels of content is `Tabs` instead.
- `Meter` (`components/ui`) draws every bar: segments laid end to end, each a
  percent of the track with a tone, plus an optional printed reading and
  marker. A goal's two segments come from `goalBarSegments` in `lib/goals.ts`.
- `SearchInput` (`components/ui`) is every search box: a magnifier, the field
  and a clear button in one box, where Escape clears the text too. It is
  controlled, and the owner debounces with `useSettled`.
- `lib/categoryTree.ts` builds the category tree and its search for every
  category list. `CategoryPicker` (`components/transactions/Pickers.tsx`)
  picks one category, and `CategoryChecklist` picks several.
- `AccountSelect` (`components/AccountSelect.tsx`) picks one account by name,
  with an optional first choice that is not an account ("Every account", "No
  account"). The accounts a holding can go into are `openInvestmentAccounts`
  (`lib/accounts.ts`).
- `Checklist` (`components/ui`) ticks several things from a flat list or one
  grouped under headings, and `SearchableChecklist` puts a search box over
  one; the tag picker and the filter's tag, account, payee and flag facets
  are these. `useArrowList` gives a list under a search box its arrow keys,
  and the category picker and checklist use it too. Adding or removing one
  id from a list or set is `toggled` or `toggledSet` (`lib/toggle.ts`).
- `CollapsibleCard` (`components/ui`) is a card whose body folds away under
  a toggle after its actions, remembered per browser under its `storageKey`.
- `SortableTh` (`components/ui`) is a table column head that sorts, with
  `aria-sort`. Its `SortButton` is the label and arrow, and a sorting head
  outside a `<table>` (the register's date) draws it with its order in words.
- A chart imports from `@/components/charts`, and one drawn outside it takes
  the same pieces: `AXIS` and `GRID` for its axes and grid, `BAR_CURSOR` or
  `LINE_CURSOR` for the hover mark, `seriesColor(index)` for a categorical
  series and `useMoneyTick` for money ticks. Tick text is sized in
  `screens.css`. A spending-plan bucket's colour is its `--bucket-<key>`
  token in `plan.css`, shared by the plan rail and the dashboard ring.

**Dialogs.** A dialog whose form holds a draft is a `FormDialog`: its children
mount on each opening, so every opening starts from the values passed in, and
stay mounted while it animates closed. A caller never keys a dialog to reset
it. A dialog opened on a target (a row, a member) and closed by clearing it
reads the target through `useHeld`, which keeps the last one drawn through
the close. Naming or renaming one thing (a space, a tag, a report, a
connection, a shop login) is a `NameDialog` (`components/ui`): a labelled
field that submits on Enter, reads "Saving…" while the save runs, closes when
it succeeds and stays open with a failure toast when it does not. Like
`useConfirm`, it is handed a mutation made without a failure handler.

A destructive action is confirmed through `useConfirm(mutation, options)`,
whose `dialog` is spread onto a `ConfirmDialog`. The dialog stays open while
the mutation runs, closes when it succeeds, and stays open with a failure
toast when it does not, so the mutation hook is created without its own
failure handler. Forgetting a connector's stored sign-in is one
`ForgetSignInConfirm` (`pages/settings/connector/`) for mailboxes, bill
providers and merchant accounts.

**Connector rows.** Bill providers and merchant accounts read their sign-in
state through one `signInNeed` (`pages/settings/connector/signInNeed.ts`),
which ranks a pause above the needs-sign-in flag, and label their buttons
with its `signInLabel` and `forgetPasswordLabel`. `ConnectorControls.tsx`
holds the pieces every connector row draws: `ConnectorBadge`,
`SignInButton`, `SessionExpiredNote`, and `RefreshButton`, which is every
"run it now" button (update a login, read a mailbox, sync a bank).
`usePullAfterSignIn` (`lib/clients/pullAfterSignIn.ts`) follows the pull a
sign-in starts, for both kinds.

**Money** arrives as decimal strings and is coerced exactly once, in
`lib/api.ts`: each client declares which response fields are money (a
`MoneyShape`) and `coerceMoney` turns them into `Money`, which on the client
is branded integer cents (`lib/money.ts`). Nothing downstream parses an
amount, and combining amounts goes through the `lib/money.ts` functions. An
amount on screen is `<Money>`; an amount inside a sentence, a toast or a
label goes through `useMoneyText()` (`components/moneyText.ts`), so privacy
mode masks it there too.

**The filter** has one wire encoder and one editor.
`lib/transactions/filter.ts` turns the editor's draft into the wire shape and
`lib/reports/savedFilter.ts` is its inverse. `amountOperator` is the one reading of
an amount facet's comparison, and `amountBound` parses a typed bound.
`components/transactions/FilterFacets.tsx` is the facet editor, mounted by
the register's filter popover, the rules and guidance condition column, the
watchlist dialog and the automation editor.

**Installable app.** `public/manifest.webmanifest` makes the app installable,
and `public/sw.js` is a small service worker that only receives web push
alerts; it caches nothing.

## Tools

- `tools/camoufox` is the Camoufox container: a Playwright server and nothing
  else.
- `tools/extractors/` holds the browser-console script a user runs to export
  a Simplifi dataset (see [importing.md](importing.md)).
- `tools/agentifi-mcp` is optional developer tooling: an MCP server that
  drives a running instance over its HTTP API. It is not part of the
  application.
