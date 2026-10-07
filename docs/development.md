# Development

How code is written in this repository: how to add the common kinds of
thing, how a screen is built, and how it is tested. The rules every change
keeps are in [`AGENTS.md`](../AGENTS.md), how the pieces fit is in
[architecture.md](architecture.md), and setup and the checks CI runs are in
[`CONTRIBUTING.md`](../CONTRIBUTING.md).

## The rules

The ground rules every change keeps and the traps that are easy to break are
in [`AGENTS.md`](../AGENTS.md#ground-rules). Code cites them as "ground rule
N" and "trap N" by the numbers given there.

## Adding an API resource

A resource is one new file in `backend/internal/api/`. Its `init()` calls
`Register`, and nothing else is edited:

```go
func init() {
	Register(Resource{Prefix: "/widgets", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listWidgets)
		rt.Write(http.MethodPatch, "/{widget_id}", updateWidget)
	}})
}

func listWidgets(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListWidgets(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, rows)
}
```

- `rt.Read` and `rt.Write` are the only ways to add a tenant-scoped route.
  Both resolve the space; `Write` refuses a viewer. Every store call takes
  `sp.ID()`. `route_contract_test.go` fails on a route that does neither.
- Return an error rather than writing a failure status: `errNotFound`,
  `errInvalid`, `errConflict` and the others in `errors.go` map to statuses in
  one place. Decode bodies with `decodeBody`; a field that may be absent,
  null or set is an `Opt[T]`.
- Money fields are `domain.Money`, which serializes as a string.
- The route is reachable by the assistant through in-process dispatch the
  moment it is registered. A route that uses a credential goes in
  `dispatchDeniedRoutes` in `dispatch.go`.
- Test it through the HTTP stack against Postgres, like the other `api`
  tests, including that another space's rows are invisible.

Identity routes (`RegisterIdentity`) and superuser routes (`RegisterAdmin`)
are closed lists; a new resource is tenant-scoped.

## Adding a bill provider

Follow [connectors/adding-a-bill-provider.md](connectors/adding-a-bill-provider.md).
In outline: start with `agentifi probe-sign-in <url>`, add the provider's
facts to `domain.Billers`, write one module in `internal/billers/` on the
shared sign-in and reading helpers, add it to `billers.New()` in
`billers/registry.go`, and record its behaviour in
[connectors/providers.md](connectors/providers.md). A module declares
behaviour, never facts, and a figure it cannot read is a note, never zero.

## Adding a merchant connector

A merchant is facts in one table and behaviour in one module:

1. Add a `domain.Merchant` to `domain.Merchants` (`domain/merchants.go`) with
   its wording function, which decides from a bank row's two names alone
   whether the row belongs to it, and a sign-in alert type in
   `domain/alerts.go`.
2. Teach `importer/merchantimport` its file shape. A connector's pull builds
   the same file an upload would, so this package is the only reader.
3. Implement `merchants.Module` in a new file in `internal/merchants/`
   and add it to `NewRegistry` in `merchants/merchant.go`. A site whose
   sign-in only works in Firefox also implements `browser.FirefoxProvider`, and
   `browser.FirefoxOriginProvider` if it makes calls outside a pull.
4. Add the client's entry to `MERCHANTS` and `ALL_MERCHANTS` in
   `frontend/src/lib/merchants.ts`, keeping its wording stems in step with the
   server's.

Matching is shared and already merchant-neutral; see
[connectors/merchants.md](connectors/merchants.md).

## Adding an assistant tool

The catalogues are in `backend/internal/domain/assistant.go`. Each tool is a
`domain.AssistantTool` with a name, a description written for the model, and
a JSON Schema for its parameters.

- **A read tool** goes in `domain.AssistantTools` and gets a case in
  `assistantTools.Run` (`api/assistant_tools.go`), answered from the same
  loaders or endpoints the screens use, so the assistant never quotes a
  number no screen shows. It must not change anything:
  `TestNoReadToolCanChangeAnything` refuses a read tool marked as writing or
  named like a change.
- **A write tool** goes in `domain.AssistantWriteTools` with `Writes: true`
  and a required `summary` argument, and gets a case in `buildAction`
  (`api/assistant_actions.go`) that turns its arguments into the method, path
  and body of an existing route. It proposes; it never edits. The proposal is
  applied through in-process dispatch with the person's own permissions.
- Mark a tool `Attended` if an automation must never be offered it, and
  `Unrecorded` if its result must not be kept in the conversation.

Often no new tool is needed: `read_endpoint` and `change_endpoint` reach any
registered route. See [assistant.md](assistant.md).

## Adding a migration

Migrations are goose SQL files in `backend/migrations/`, embedded in the
binary. Name a new one with the next five-digit version and a short
description, such as `00002_add_widget_colour.sql`. Each file has a
`-- +goose Up` section and a `-- +goose Down` section, and Down is the exact
reverse of Up. A statement goose cannot split on semicolons (a function body)
goes between `-- +goose StatementBegin` and `-- +goose StatementEnd`.
`00001_initial.sql` is the schema as a whole and is never edited; changes go
in new files. Run `go run ./cmd/agentifi migrate` from `backend/`; CI runs it
twice to prove a second run is a no-op, and `serve` refuses a database behind
the binary.

## Adding a frontend screen

1. Put the page in `frontend/src/pages/` as a named export, with its private
   components in a subdirectory named after it.
2. Add its lazy import and its `<Route>` in `src/routes.tsx`, inside
   `AppShell`. If it belongs in the navigation, add it to
   `components/shell/destinations.ts`.
3. Put its API calls in a client module in `src/lib/clients/`: request
   through `api` from `lib/api.ts`, declare every money field in a
   `MoneyShape`, read with TanStack Query and write with
   `useInvalidatingMutation`, naming what the change invalidates.
4. Put its logic in plain functions under `lib/` with `*.test.ts` beside
   them, and add the screen to `pages/screens.smoke.test.tsx`, which renders
   each screen with no data.
5. Build it from `components/ui` and the tokens in `styles/tokens.css`, using
   the checklist below. A screen that selects transactions mounts
   `FilterFacets`.
6. Add it to `ROUTES` in `frontend/layout/routes.ts`, give every request it
   makes a fixture in `layout/fixtures/`, and run `npm run layout` (see
   [The layout check](#the-layout-check)).

### Which primitive for which shape

| Shape | Primitive |
| --- | --- |
| The first row of a page or a flush card | `PageHeader`: `tabs`, `title`, `leading`, in-line `children`, and `actions` (`size="sm"`, with a `SearchInput size="sm"` last) |
| A box of content | `Card`, whose `actions` are `size="sm"` buttons |
| Rows with columns to compare | `Table`, with `Th`, `Td` and `SortableTh` |
| Rows that are not a table: a name, a second line, a figure | `List` of `ListRow` (`title`, `badge`, `sub`, `figures`, `figuresSub`, `actions`, `onSelect`) |
| The buttons at the end of a row | `RowActions`, at `size="sm"`, in a right-aligned cell (`Td numeric`) |
| A button that is only an icon | `IconButton` with a `label`, `size` `sm` or `md` |
| A small tinted label: a status, a count, a flag | `Badge`, with a `tone` |
| A sentence the reader should notice | `Callout`: `info` for a fact, `warning` to act on, `expense` for a failure |
| Nothing to show | `EmptyState`; `compact` inside a card, a list or a picker |
| A dialog's buttons | `DialogActions` in `DialogContent`'s `footer`, or `FormDialog` and `ConfirmDialog`, which use it |
| A row of controls inside a card | `.toolbar`, with `.toolbar__spacer`, `--flush`, `--pack` and `.toolbar__note` |

`DialogActions` fixes the footer's order: anything that is not an answer (a
delete, a reset, a link) in `start`, then the dismissal as a `secondary`
button (`cancel="Close"` where there is no draft to lose), then the answer,
one `primary` or `danger` button. No dialog writes its own Cancel.

### Where controls go

- **The page's "New …" action** is the one `primary` button, the last of the
  page `PageHeader`'s `actions`, so it sits at the top right of every page.
  An empty state below it does not repeat it.
- **A range or a period that changes the whole page** (a date range, the
  month on screen, a mode that changes every card) goes in the page
  `PageHeader`: its `children`, or its `tabs` when the choices are tabs.
- **A filter that narrows one list** (chips, a search, a sort, a list or card
  switch) goes in the first row of the card holding that list: the `Card`'s
  `actions`, or a `PageHeader` as the first row of a flush card. It never
  goes in the page header, where it would read as filtering every card.
  A page with both keeps the range in the page header and puts every filter
  of the list (the filter popover, the quick filters, the search) in one
  toolbar row of the list's card, the search at its right end.
- **A row's or a card's own actions** are in `RowActions`, visible at all
  times rather than on hover, at the right end of the row or the card's title
  row.

### Sizes

- **Controls.** A control in a toolbar, a card's actions, a row or a page
  header is the small size, `--control-height-sm`: `size="sm"` buttons and
  menu triggers, `SearchInput size="sm"`, and chips. `--control-height` is for
  a form field and a dialog's buttons. The controls in one row are one
  height and one family: a text button beside a field, a select or a chip is
  `secondary` (or the `primary` action), never `ghost`, which is for icon
  buttons and rows of nothing but ghost buttons. Below 48rem a chip is 2rem
  tall for touch, and so is every small control in a row that holds one.
- **Rows.** A `Table` picks `density`: `sm` (`--row-height-sm`), the default
  `md` (`--row-height`) or `lg` (`--row-height-lg`). A table whose rows carry
  a second line (`.cell__sub`) sets `lines={2}` instead, so every row is
  `--row-height-two-line` tall whether or not its own cells wrap. A `ListRow`
  is the same height as a table row with the same number of lines.
- **Spacing.** Sections of a page are spaced by `.page`'s gap and cards in a
  grid by `--gap-cards`; do not add margins between them. Everything inside
  takes the `--space-*` scale.
- **Figures.** A page's own total (net worth, an account's balance, a
  report's total) takes `.figure--total`; a labelled stat among others takes
  `.figure--stat`; a row's figure keeps the row's size. Every amount is in the
  numeric face (`--font-numeric`, tabular figures), which `Money`,
  `Td numeric` and `Input numeric` set; a column heading keeps the text face.

### Widths

- **The root is 125% on a wide screen and 100% below 48rem**, so every rem in
  a rule is 20px wide and 16px narrow. A rem inside a media query is always
  the browser's 16px, whatever the root is: `@media (max-width: 48rem)` is
  768 CSS pixels on every screen.
- **Media queries use the ladder**: 30, 40, 48, 56, 64, 70 and 86rem, all
  `max-width`. stylelint fails any other width.
- **A container query, not a media query, for a component whose width is not
  the window's**: anything that sits in a card, a panel or beside the
  accounts drawer. Give the container `container-type: inline-size` and a
  name, and query that.
- **Check the screen at 390px wide and at 1180px with the accounts drawer
  open**, the two widths where a layout that fits elsewhere overflows, and at
  a wide desktop width. `npm run layout` renders it at all of them.

### What the linters hold you to

- `npm run lint` runs stylelint over `src/**/*.css`: no raw colour outside
  `tokens.css`, no raw length in padding, margin, gap, font size or radius
  (0, 1px, `em` and a `calc` of tokens are fine), no numeric font weight, and
  no media width off the ladder.
- It also runs ESLint, which fails an inline `style` other than a
  data-driven value (a colour, a size, a transform, a position or a column
  template computed from data) or a custom property. Anything fixed is a
  class.
- `src/styles/vars.test.ts` fails a `var(--x)` that nothing sets, and
  `collisions.test.ts` fails a class laid out from two stylesheets.

### The layout check

`npm run layout` (Playwright, `frontend/layout/`) renders every route in
`layout/routes.ts` in Chromium at 390, 820, 1180 and 1440px wide, the 1180
one with the accounts drawer open. The routes are every destination in
`components/shell/destinations.ts`, their tabs and every Settings section; a
test fails when a destination or section has no route. Vite runs on port
5176 (`LAYOUT_PORT` overrides it), and every `/api` request is answered from
`layout/fixtures/`, which are invented: round figures and names that belong
to nobody, with a long payee, a two-line row, a badge, a flagged account with
a long name and an empty list among them. A request with no fixture fails the page. The clock is fixed, and Inter
and the monospace face are served from `@fontsource-variable` so a row wraps
at the same word on every machine.

On each page and width it fails on:

- a horizontal page scroll;
- an element inside a `.card` that crosses the card's edge by more than
  1px, outside a scroll container;
- rows of the same kind in one `Table` or `List` whose heights differ by more
  than 1px (a `ListRow` with `wrap` is exempt);
- controls in one row whose heights differ by more than 1px, where a row is
  a `.toolbar`, a card's actions, or a page header's own controls together
  with its actions (tabs are not controls, chips are, and a button inside a
  search box belongs to the box);
- a text button in such a row drawn without a border or a fill beside boxed
  controls, or text buttons in one row with different padding;
- page-header actions that do not end at the header's right edge;
- a top bar whose page title is cut short;
- a console error or an uncaught exception.

`layout/allowlist.ts` lists the violations the pages carry, each with
the route, widths, check, the page that owns the fix and why. An entry
tolerates that check on that route at those widths; an entry that
matches nothing fails too, so fixing a page means deleting its entry.
Screenshots of every page at every width are written to the gitignored
`layout/output/screenshots/<width>/`. CI runs the check only when its workflow is started by hand.

## Testing

**Backend.** `cd backend && go test ./internal/...`. Tests use the standard
`testing` package with `testify/require`.

- **Postgres.** The `store`, `service`, `api` and importer tests need a
  database, and **without one they skip, which reads exactly like passing**.
  `internal/testdb` gives each test process its own schema and drops it
  afterwards. It reads `TEST_DATABASE_URL`, falling back to the socket that
  `scripts/dev-postgres.py` starts under `.dev-postgres/`; with `CI` set an
  unreachable database is fatal instead of a skip. CONTRIBUTING.md has the
  commands. A test package outside `store` makes its `TestMain` one call to
  `storetest.Main`, reads the store through `storetest.DB` and builds its
  rows with `storetest`'s fixtures (`NewSpace`, `NewAccount` and the rest).
  `store`'s own tests cannot import `storetest`, which imports `store`, so
  they call `testdb` directly.
- **Domain tests** run anywhere, over invented data, with expected values
  from [calculations.md](calculations.md).
- **Golden tests** (`api/golden_test.go`) compare the calculations with the
  figures in a real Simplifi export in `data/simplifi/`, or the file named by
  `AGENTIFI_GOLDEN_EXPORT`. They skip where there is no export, including CI.
  Run them after changing a calculation. A forecast month the export cannot
  arbitrate is named, with the reason, in `golden-forecast-gaps.json` beside
  the export (`{"YYYY-MM": "why"}`), which stays out of the repository with it.
- **Live tests** (files named `*_live_test.go`) drive a real browser or site
  and skip unless their environment is set. Those behind
  `AGENTIFI_BROWSER_TEST=1` drive Google Chrome against invented pages served
  by the test itself; CI's Browser job runs them, and CONTRIBUTING.md has the
  local setup. Those behind an `AGENTIFI_LIVE_*` variable reach a real site
  and run only by hand.
- **Dead code.** CI runs `deadcode` and fails if a function in `domain` or
  `service` has no caller outside tests: a calculation nothing reaches is a
  number the app never shows.
- **Format and vet.** `gofmt -l .` must print nothing, and `go vet ./...`
  must pass.

**Frontend.** `cd frontend`, then `npm run test -- --run` (vitest, in a node
environment, `src/**/*.test.{ts,tsx}`), `npm run lint` (ESLint with the React
hooks and accessibility plugins, then stylelint), and `npm run build`.
**`npm run build` is the only typecheck**: vitest strips types without
checking them, so a type error passes the tests and fails the build. `npm run typecheck` runs the same
`tsc -b` alone. A component test renders to static markup through
`renderScreen` (`src/test/renderScreen.tsx`), which supplies the query cache
(seeded with `seed`, never retrying), privacy mode, toasts, tooltips and a
router at `route`; a provider it lacks, such as a signed-in session, wraps
the node passed in. A test that needs only a client uses `testQueryClient()`.

**Scripts and tools.** `tools/agentifi-mcp/selftest.py` checks the MCP server
against a running instance.

## Code style

- Write code that reads like the code around it: its naming, its comment
  density, its idiom. Backend names say what a thing is in the household's
  terms; handler, store and service names follow the resource.
- Comment only to state a constraint the code cannot show: why an ordering
  matters, what an outside system does, what would break. Do not comment what
  the next line does.
- Comments and documentation describe the code as it is. They carry no
  history: no "used to", no earlier implementations, no dates, no record of
  how a change came about. That belongs in the commit message.
- Keep a change to one concern, with a test that fails without it. A new
  derived number gets a function in `internal/domain` and a line in
  [calculations.md](calculations.md).
