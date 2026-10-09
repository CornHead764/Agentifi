# Connectors

A connector is any way data from outside reaches the ledger. There are five:
bank sync through SimpleFIN, bill providers, the order merchants, the watched
mailbox, and files a person imports. Every one is optional; Agentifi runs from
hand entry alone.

| Connector | Brings in | Code | Documentation |
| --- | --- | --- | --- |
| SimpleFIN | Accounts, balances, transactions | `internal/provider/simplefin.go`, `internal/service/sync.go` | This page |
| Bill providers | Bills, statements, due dates | `internal/billers`, `internal/connector`, `internal/service/bills.go` | [bills.md](bills.md), [providers.md](providers.md) |
| Mailbox | E-mailed bills and statements, sign-in codes | `internal/provider/mailbox.go`, `internal/billmail`, `internal/service/mailbox.go` | [bills.md](bills.md) |
| Merchants | Amazon and Costco orders and receipts | `internal/merchants`, `internal/connector`, `internal/importer/merchantimport`, `internal/service/merchant.go` | [merchants.md](merchants.md) |
| Files | Simplifi exports, CSV, OFX and QFX | `internal/importer` | [../importing.md](../importing.md) |

Credentials a connector keeps (a SimpleFIN access URL, a provider password, a
kept browser session) are sealed at rest with `CREDENTIAL_ENCRYPTION_KEY`, or
with a key derived from `SECRET_KEY` when that is unset. They are as sensitive
as the accounts they open.

## Bank sync: SimpleFIN

SimpleFIN is the only bank aggregator. A person links their banks at
SimpleFIN Bridge and pastes the setup token into Settings; Agentifi claims it
for an access URL and from then on reads accounts and transactions over it.
There are no widget or OAuth connection flows and no other aggregator.

- Every SimpleFIN protocol shape lives in `provider/simplefin.go`; nothing
  else sees a SimpleFIN field name. The first sync reads a year of history in
  windows of 45 days, the widest the Bridge accepts without warning; a routine
  sync reads about a month.
- A second sync imports nothing new: rows match on the provider's id, then on
  day, amount and wording, and a deleted row still counts as present.
- An account's sync floor (`sync_floor_on`) is enforced in front of the
  insert: the aggregator's rows dated before it are ignored. An account
  imported from Simplifi arrives unlinked; linking it to its SimpleFIN
  account sets the floor at its newest imported row, so the sync does not
  duplicate history the import already holds
  ([../importing.md](../importing.md)).
- One bank's failure is recorded against its connection and the other
  accounts keep syncing. Only a refused access URL needs a new setup token.
- New rows settle through `settleNewRows` in `service`, the same path the file
  importers use: rules, bill matching, transfer pairing and running balances.

The scheduler syncs each due connection once a day in the `SYNC_AT` window
(see [../architecture.md](../architecture.md#background-work)); a person can
also sync on demand. `SIMPLEFIN_ENABLED` turns it on.

## Bill providers

A bill provider is a company that sends the household a bill. A connection
signs in to the household's own account there, reads the current bill and its
statement, and puts the real amount and due date on the household's bill
reminder. `domain.Billers` is the catalogue of shipped providers and each
has one module in `internal/billers`. A provider is reached one of three ways
(`domain.BillerAccess`):

- **API**: plain HTTP against the provider's own JSON service, no browser.
- **Browser**: a kept browser session, in Chrome or Camoufox (below).
- **E-mail**: no live connection; the bill is read from the watched mailbox.

Pulls run daily in the sync window, and a pull that meets a second factor is
parked until someone answers it, or until a kept authenticator key or the
mailbox answers it. [bills.md](bills.md) covers connecting, pulling, second
factors, the mailbox and what the assistant may do;
[providers.md](providers.md) is each shipped provider's behaviour and
requirements; [adding-a-bill-provider.md](adding-a-bill-provider.md) is how to
add one, starting with `agentifi probe-sign-in <url>`.

## The mailbox

A space can connect mailboxes over IMAP or Microsoft Graph. The scheduler
polls them on their own interval, shorter than a day, because a
sign-in code is good for minutes. `internal/billmail` recognizes what a
message is, offline: which provider sent it, what bill or statement it
carries, or a one-time code a parked sign-in is waiting for. Mail rules file
e-mailed bills and statements onto bill connections. See [bills.md](bills.md).

## Merchants

Merchants are not banks. An Amazon or Costco order arrives either as a file a
person uploads or from a connector that signs in to their account and pulls
the order history daily, and is then matched to the bank row that paid for it
by amount first and date second. A pull builds the same file an upload would,
so `importer/merchantimport` is the only reader of that shape. The merchant
is a value: `domain.Merchants` says which bank rows name which merchant, from
their wording alone, and a Costco row is never offered an Amazon order.
[merchants.md](merchants.md) covers both merchants, the order files and the
matching.

## The two browsers

Connectors that need a browser drive one of two, and neither knows anything
about any site; that is the modules' job.

- **Chrome** runs inside the application process, driven by playwright-go
  (`internal/browser`). The image does not carry it: the compose file's
  `chrome` service downloads Google Chrome stable from Google into a volume
  (see [operations.md](../operations.md#the-browsers)). Bill connections keep a
  persistent profile per connection under `AGENT_PROFILES_DIR`, because device
  trust lives in places a snapshot does not reach; merchants keep a sealed
  storage state instead. `agentifi browser-selftest` checks that the browser
  starts in a container.
- **Camoufox**, a Firefox build, runs in its own container (`tools/camoufox`)
  and is reached at `CAMOUFOX_URL`, whose path is the shared secret the
  container was started with. It is for providers whose sign-in only works
  in Firefox. A module opts in by implementing
  `browser.FirefoxProvider` (`RunsInFirefox() bool`); a merchant whose calls
  run outside a pull also implements `browser.FirefoxOriginProvider`
  (`FirefoxOrigin()`). From then on its sign-in, pulls and calls all run
  there. With `CAMOUFOX_URL` unset such a provider fails with a clear error
  and never falls back to Chrome. The engine makes that choice in one place,
  `internal/browser/agent` (`agent.For` for the browser, `agent.Fetchers` for
  the calls).

Camoufox differs from Chrome in ways a module author must know. Its scripts
run in an isolated world: they see the page's DOM and the origin's storage but
not the page's JavaScript, so a module reads what the page stores and never
hooks what it sends. It keeps no profile on disk, so each run is a fresh
context seeded from the sealed session. And it has no screencast (that is
Chrome's DevTools protocol), so its live view is a screenshot of the page about
once a second with the typed fields covered (`browser.StartFirefoxLiveView`).
A person uses it for one thing: a "Verify you are human" check that the app
never ticks itself (see [`bills.md`](bills.md#a-page-check-only-a-person-can-tick)).
Every provider there still signs in through the typed form.

### The page a run failed on

When a browser-driven sign-in or pull fails on a page, in either browser, the
engine photographs the page before the browser closes (`agent.Pictured`, which
wraps the error in a `provider.PageFailure`; the call sites are the connector's
pull, merchant fetch and re-sign-in paths). Every frame's typed fields (inputs
other than buttons, checkboxes, radios and hidden ones, text areas and
editable regions) are covered by Playwright's `Mask` option, so a password or
code on screen is a solid box. Chrome's live view is not masked: it is the
person's own sign-in. Camoufox's covers the same typed fields, because the
engine fills them and the person is there only to tick a box. A failure that is not about a page (a provider's API
refused, the network was down) carries no picture.

The service re-encodes the picture as a JPEG under 1MB and keeps it beside the
error, in `bill_connections.last_pull_screenshot` or
`merchant_accounts.last_sync_screenshot`. Only the latest is kept: the next
failure's page takes its place, a failure with no page leaves none, and a pull
that gets in, a fresh sign-in or a skipped run clears it. A picture over the
cap is dropped and the failure is still recorded.

A connection or account says `has_failure_screenshot`, and the image is
served, `image/jpeg` and never cached, only to the household that owns it:

- `GET /bills/connections/{id}/failure-screenshot`
- `GET /merchants/{merchant}/accounts/{id}/failure-screenshot`

On the settings pages the failure note offers **Show screenshot**, as do
**Update now**'s toast for a bill connection and the toast for the first pull
after a sign-in. A sign-in that fails in its dialog shows the page in the
dialog itself, under **The page ‹provider› showed**.

A bill sign-in a person started that never lands is kept the same way, in the
last pull's place, so it can be read after the dialog is gone: when it fails,
when the person closes the dialog, or when nobody comes back and the session
is reaped. The engine reports each once (`Engine.SignInEnded`, at the failure
or as the session shuts, while its page is still there) and the service keeps
it with `store.MarkBillSignInEnded`: `last_pull_status` is `sign_in_failed`,
`last_pull_error` the failure's words or where the sign-in stood ("The
sign-in was closed before it finished, while ‹provider› was asking for a
code."), the page in `last_pull_screenshot`, and the trail in
`last_pull_trail`, written and cleared with them. When the connection last
pulled, whether it needs a sign-in and any pause are left as they were.

Every bill pull keeps its trail in the same column, whether it got in or not:
the rounds of any sign-in it did with the kept password, and the lines its
module asked for. A module records a page with `Call.Saw` (step `read`,
state `page`, the address without its query, and a structural snapshot of the
page: `billers.ReadingSnapshot`, which also names each link's path and
classes and reads the dialog in front or the page's `main`) and a sentence
with `Call.Mark` (state `note`). The engine keeps at most 80 lines and the
store at most 1MB, so a pull's trail is bounded; the next pull's trail takes
its place, and a pull that kept none leaves none. A snapshot carries no
typed value, no secret-named attribute and no run of four or more digits
other than a year a date prints. The connection says `has_trail`, and the
trail is served in the dialog's shape, to the household that owns it, at
`GET /bills/connections/{id}/trail`. The card offers it under **What the
provider showed**: in the failure note when the last pull or sign-in
stopped, and otherwise among the card's facts.

## Files

The Simplifi importer (`agentifi import`, fed by
`tools/extractors/extract-simplifi.js`), the CSV and OFX/QFX importers
(`agentifi import-csv`, `agentifi import-ofx`, and the upload under
Settings, Accounts) and the Simplifi rules import are described in
[../importing.md](../importing.md).
