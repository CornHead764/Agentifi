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

## Adding a service

The API is ConnectRPC: a service is declared in proto, generated, and
implemented in one file in `backend/internal/api/` whose `init()` calls
`RegisterService`. Nothing else is edited.

**1. Declare it** in `proto/agentifi/v1/<resource>.proto`, package
`agentifi.v1`. One service per resource (`WidgetService`), methods named as
AIP does (`ListWidgets`, `GetWidget`, `CreateWidget`, `UpdateWidget`,
`DeleteWidget`, or a verb such as `CloseGoal`), and a request and a response
message of its own for every method, `<Method>Request` and
`<Method>Response`, even when empty. The service says which accesses its
methods may use, and every method says which one it uses:

```proto
service WidgetService {
  option (scope) = SCOPE_TENANT;
  option (rest_prefix) = "/widgets";

  rpc ListWidgets(ListWidgetsRequest) returns (ListWidgetsResponse) {
    option idempotency_level = NO_SIDE_EFFECTS;
    option (access) = ACCESS_READ;
    option (rest) = {method: "GET", path: "/widgets", response_body: "widgets"};
  }

  rpc UpdateWidget(UpdateWidgetRequest) returns (UpdateWidgetResponse) {
    option (access) = ACCESS_WRITE;
    option (rest) = {method: "PATCH", path: "/widgets/{widget_id}", response_body: "widget"};
  }
}
```

- `ACCESS_READ` resolves the space and refuses nobody in it; `ACCESS_WRITE`
  also refuses a viewer. A READ method is `NO_SIDE_EFFECTS`, and a WRITE one
  is not; a READ method that changes only the caller's own rows is named in
  `personalWriteProcedures` in `rpc_contract_test.go`. `USER` and `PUBLIC`
  are for `SCOPE_IDENTITY` services, `SUPERUSER` for `SCOPE_ADMIN` ones.
- `dispatch` (or the service's `service_dispatch`) is `DISPATCH_DENIED` or
  `DISPATCH_HUMAN_ONLY` with a `human_link` for a method the assistant must
  not call; it agrees with the lists in `dispatch.go` while those exist.
- Money is `Money` (`common.proto`), or `NullableMoney` when it may be
  absent, never a string or a number. A rate is a `string`, a date a
  `string` ("YYYY-MM-DD"), a count `int32`, a timestamp
  `google.protobuf.Timestamp`. A field that may be unset is `optional`. A
  list window is `string from`, `string to`, `string date_field` on the
  request and a `Window window` on the response (trap 5). A value from a
  closed set the REST wire wrote as a string stays a `string`.
- An Update request is flat: the path ids, `optional` fields and
  `google.protobuf.FieldMask update_mask`. A field named in the mask and unset
  is cleared; a field not named is left alone.
- Field names and their order are the REST response's, so the bridge below
  writes the same bytes.

**2. Generate**, from the repository root, after `npm install` in
`frontend/`: `buf lint && buf format -w && buf generate`. Go lands in
`backend/internal/gen`, TypeScript in `frontend/src/gen`; both are committed
and CI fails when they differ from the protos.

**3. Implement** it:

```go
func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewWidgetServiceHandler(widgetService{env}, opts...)
	})
}

type widgetService struct{ env *Env }

func (s widgetService) UpdateWidget(
	ctx context.Context, req *agentifiv1.UpdateWidgetRequest,
) (*agentifiv1.UpdateWidgetResponse, error) {
	sp := spaceFrom(ctx)
	widget, err := liveWidget(ctx, s.env, sp, req.GetWidgetId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	if err := applyRequired("name", optOf(mask, "name", req.Name), &widget.Name); err != nil {
		return nil, err
	}
	applyNullable(optOf(mask, "notes", req.Notes), &widget.Notes)
	...
}
```

- The access interceptor has resolved the caller before the handler runs:
  `spaceFrom(ctx)` for a READ or WRITE method, `userFrom(ctx)` for any but a
  PUBLIC one. `requestFrom(ctx, header)` rebuilds the host, TLS state and
  peer for code that reads an `*http.Request` (a WebAuthn origin, the login
  meter).
- Return the errors in `errors.go` as a REST handler does (`errNotFound`,
  `errInvalid`, `errConflict`, …). `classify` maps each to a Connect code and
  a `Problem` detail carrying the status, code and field errors.
- The conversions are in `rpcwire.go`: `moneyProto` and `nullableMoneyProto`
  out, `moneyFrom` in, `rateProto`, `idFrom` (a malformed id is the same 404
  as another space's row), `windowOf` and `windowProto`, and `maskOf` with
  `optOf` and `optMoneyOf` for a patch.

**4. While the REST bridge exists,** each method carries a `rest` annotation
naming the URL it answered (`restbridge.go` serves it from the procedure, for
the web app, the MCP server and the assistant), and its `Register` block is
deleted in the same change: the registry refuses a URL served twice. Path
parameters are request fields of the same name; query parameters and body
keys are too. `status` is the success status when it is not 200, and
`response_body` names a field answered bare. `body_optional` marks a POST
that takes no body. The REST tests are the conformance suite: they pass
unchanged.

**5. Test** it with typed calls, against a database, as the other `api`
tests are: `call[Req, Res](client, procedure, req)` returns the response or
the `*connect.Error`, and `problemIn(t, err)` its Problem; `client.rpc(procedure,
body)` sends raw JSON and its `requireStatus` reads the Problem's status, with
`requireCode` for the Connect code. Cover another space's rows, a viewer's
write, a patch's absent, set and cleared field, and an unknown field. In
development and tests a response with a `Money` field left unset fails.

The upload, download and redirect endpoints (`POST /documents`,
`POST /imports`, the failure screenshots, the OIDC login) stay plain HTTP: a
file in `internal/api/` whose `init()` calls `Register`, with `rt.Read` and
`rt.Write` as the only tenant-scoped routes and `decodeBody` and `Opt[T]` for
a body. `RegisterIdentity` and `RegisterAdmin` are closed lists.

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

Migrations are goose SQL files in `backend/migrations/sqlite/`, embedded in
the binary, written in SQLite's dialect. Name a new one with the next
five-digit version and a short description, such as
`sqlite/00002_add_widget_colour.sql`. Each file has a `-- +goose Up` section
and a `-- +goose Down` section, and Down is the exact reverse of Up. A
statement goose cannot split on semicolons (a trigger body) goes between
`-- +goose StatementBegin` and `-- +goose StatementEnd`.
`sqlite/00001_initial.sql` is the schema as a whole and is never edited;
changes go in new files. `backend/migrations/00001_initial.sql` is the
Postgres-backed release's schema, which `import-postgres` reads; it is never
applied and takes no new migrations.

A new column follows the storage classes the header of
`sqlite/00001_initial.sql` lists, because `internal/dbconv` and
`internal/sqlitedb` read and write by them: money is `INTEGER` hundredths,
written and read through `dbconv.Money` and `dbconv.ReadMoney`; a rate, price,
share count or percentage is `TEXT` holding the exact decimal, through
`dbconv.Numeric`; a uuid is `TEXT`; a date is `TEXT` `YYYY-MM-DD` and a
timestamp `TEXT` in UTC with microseconds, each with the `CHECK (length(…))`
the other columns carry; a boolean is `INTEGER` 0 or 1; an array or JSON
value is `TEXT` holding a JSON document. Tables are `STRICT`. A default or
query may call the functions `sqlitedb` registers on every connection:
`now()`, `gen_random_uuid()`, `ts_add(timestamp, '±N unit')`, `regexp` (so
`x REGEXP pattern` works) and `decimal_sum()` (an exact sum of decimal text).
SQLite's `ALTER TABLE` adds, renames and drops columns but cannot change a
column's type or constraints; that takes rebuilding the table. Every
connection turns foreign keys on, and with them on `DROP TABLE` first deletes
the table's rows, which fires the `ON DELETE` actions of the tables that
reference it.

Run `go run ./cmd/agentifi migrate` from `backend/`; CI runs it
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
a long name and an empty list among them. A procedure's fixture is keyed
`POST /agentifi.v1.Service/Method` and built with `procedure()` from
`layout/fixtures/procedure.ts`, which takes the response in the REST wire's
shape and writes the message's JSON. A GET or a procedure with no fixture fails the page. The clock is fixed, and Inter
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

- **The database.** The `store`, `service`, `api` and importer tests run
  against a real SQLite file and need nothing set up: `internal/testdb` gives
  each test process its own file in a temporary directory and removes it
  afterwards. The exception is `internal/pgimport`, whose end-to-end tests
  read a Postgres at `TEST_DATABASE_URL` and **without one skip, which reads
  exactly like passing**; with `CI` set an unreachable Postgres is fatal
  instead. CONTRIBUTING.md has the commands, with `scripts/dev-postgres.py`.
  A test package outside `store` makes its `TestMain` one call to
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

**Protos.** `buf lint`, `buf format -d --exit-code`, and `buf generate`
leaving no diff, from the repository root; CI runs all three.

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
