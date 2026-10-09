# Importing

Three ways to bring history in:

- a **Simplifi dataset**, read out of Simplifi's web app with
  [`tools/extractors/extract-simplifi.js`](../tools/extractors/extract-simplifi.js)
  and imported from Settings or with `agentifi import`, which carries accounts, categories,
  rules, the spending plan and everything else, not only transactions;
- a **Simplifi CSV** transaction export; and
- an **OFX or QFX** bank statement.

Simplifi's accounts arrive unlinked and are joined to SimpleFIN afterwards,
with a sync floor (see [Relinking to SimpleFIN](#relinking-to-simplefin)).
Amazon and Costco order files are a separate import, described in
[`connectors/merchants.md`](connectors/merchants.md).

## Getting the dataset out of Simplifi

Simplifi has no export API, and its *Export to CSV* gives only the visible
columns of the transaction grid. The web app is offline-capable, though: it
mirrors the whole dataset into IndexedDB (`localforage` → `keyvaluepairs`,
one entry per store, keyed `<userId>:<datasetId>:<storeName>`). The extractor
reads that back in the browser, read-only, and calls no Simplifi API. It
needs a live Simplifi subscription, so run it before the subscription lapses.

1. Sign in at <https://simplifi.quicken.com> in Chrome.
2. **Hydrate every store.** Stores fill lazily, so visit each of these once
   and let it finish loading:
   - Transactions: All, Spending, Income
   - Net Worth, with the range set to **All**
   - Spending Plan, stepping back through several months
   - Bills & Income: Overview, Cash Flow, All Series, Refunds
   - Investments: Portfolio, Balances, Performance, Transactions
   - Watchlist, Savings Goals, Reports
   - Settings: Accounts, Categories & tags, Rules, Notifications
3. DevTools → Console → paste `extract-simplifi.js` → Enter.
4. Read the summary it prints. Every store you visited should report a count
   in line with what the app shows: one `freeToSpendStore` record per month of
   spending plan, one `goalStore` record per goal. A store with `n=0` means
   the page that fills it was not opened; go back to step 2.
5. `simplifi-export-<datasetId>-<timestamp>.json` downloads. Move it to
   `data/simplifi/`.
6. **Save the transaction rules**, which are not in IndexedDB (see
   [Rules](#rules)). DevTools → Network, filter on `transaction-rules`, then
   open Settings → Rules (`/settings/transactionRules`), reloading if the
   request list is empty. Select the `GET transaction-rules?limit=5000`
   request → Response → save it as `data/simplifi/transaction-rules.json`.
   The file is `{"resources": [...]}`, one entry per rule.

Running the extractor twice, some days apart, and diffing the two files
catches stores that were stale on the first pass.

Worth taking on the same day, as cross-checks: Simplifi's *Export to CSV*
over the whole date range (an independent witness for amounts, dates and
categories, which `agentifi import-csv` can load into a space of its own),
and screenshots of the headline numbers (net worth, each account balance, the
current month's spending plan, each envelope, year-to-date income and
expense) to compare against after the import.

### Handling the file

The export is real financial data: account numbers, balances, every
transaction. `data/` is gitignored and that is where it stays. Nothing
derived from it enters the repository, not a trimmed subset, not a redacted
fixture, not a single figure: real figures reconcile with each other, which
makes them private even with the names gone. Test fixtures are invented,
never captured (ground rule 8 in [`AGENTS.md`](../AGENTS.md#ground-rules)). The
golden tests read the export where it is:
`AGENTIFI_GOLDEN_EXPORT`, or else the newest
`data/simplifi/simplifi-export-*.json`.

## Importing from the app

A space's owner or an admin can import a Simplifi export without a shell.
On an empty space, the dashboard's setup guide offers **Import from
Simplifi** as its first step; **Settings → Accounts** has it too. Import
before connecting SimpleFIN: the import needs an empty space, and its
accounts are what the SimpleFIN accounts are matched to afterwards.

The setup guide walks a new space through the import, connecting SimpleFIN
and matching the accounts, then the optional steps: the assistant, bill
providers, and, for whoever administers the server, a backup key. Each step
is checked off from what the space holds (imported rows, a connection, linked
accounts that have synced), not from a click. A step that does not apply can
be skipped, and the guide can be hidden; both are kept per space, and the
Help menu brings the guide back.

1. Make the export. The dialog's "How to export from Simplifi" has the
   exporter script to copy or download; run it as described above.
2. Choose the `.json` file it saved and, optionally, the
   `transaction-rules.json` saved from the Network panel.
3. **Preview** shows what the file holds, table by table, with any errors
   and warnings. Warnings come one line per kind, with a count, and open to
   the records behind them; fields and stores the importer does not read are
   listed last, as a note. Nothing is written until you choose **Import**. An
   export with errors cannot be imported; choose a corrected file to read it
   again.
4. The import runs in the background, usually for a few seconds. It is one
   database transaction: if it fails, nothing is written.
5. When it is done, **Match accounts** opens Settings → Accounts to join each
   imported account to its SimpleFIN account (see
   [Relinking to SimpleFIN](#relinking-to-simplefin)). Until then the
   imported accounts are unlinked and do not sync. The warnings stay on
   screen, each kind with a link to where it is resolved, such as the rules
   that arrived switched off. A failed import offers **Try again**, and
   closing the dialog clears it, so it opens on a fresh start.

The import fills the space you are in and renames it to the export's
dataset name. The space must be empty: one that already holds accounts,
transactions, categories or anything else, deleted rows included, is
refused, and so is an export already imported into another of your spaces.
The default categories a new space starts with (see
[Categories](data-model.md#categories)) count as empty while they are
untouched: every one live, at its place in the tree, with the name, kind,
markers and flags it was seeded with, and named by nothing (a rule, filter,
series, mail rule or assistant record). The import then deletes them and
writes the export's categories, ids and markers in their place, inside the
same transaction, so the space ends with one row per category. A space
whose defaults were renamed, moved, deleted, added to or put to use is
refused like any other that holds categories.
To import into a new space, create the space and switch to it first, or use
`agentifi import` on the server, which creates one. The assistant cannot
start an import.

## Importing from the command line

```sh
agentifi import data/simplifi/<export>.json --dry-run
agentifi import data/simplifi/<export>.json --owner-email you@example.com \
  --transaction-rules data/simplifi/transaction-rules.json
```

| Flag | Meaning |
| --- | --- |
| `--dry-run` | map and report, write nothing; needs no database |
| `--owner-email` | the owner of the space the import creates (default `owner@agentifi.local`) |
| `--dataset` | which dataset, when the export holds several |
| `--space-name` | the space's name, instead of the dataset's own |
| `--transaction-rules` | the saved rules response from step 6 |
| `--rules-only` | import rules into an existing space (see below) |
| `--space-id` | with `--rules-only`, the space to write to |

The exit code is the result: 0 clean, 1 something the schema cannot
represent (nothing is written), 2 an unreadable file or bad flags, 3 the
write failed. A write is one transaction, so an interrupted import rolls
back.

Run `--dry-run` first. Every lookup in
`backend/internal/importer/taxonomy.go` (account types, filter facets, alert
codes and other stored enum values) fails loudly on a value it has not been
taught, rather than guessing: a mortgage defaulted to a cash account would
sit on the asset side of net worth and raise nothing. The dry run's error
list is exactly the set of values still to teach it. A field or store the
importer neither reads nor lists as ignored (`IgnoredStores` in
`reading.go`, which includes `profileStore`) is different: Simplifi's newer
exporters add them, one the importer does not read changes nothing it
writes, so each is noted in the report, with a count, and left out. The
same holds for the rules file.

## What the import produces

The import is the first proof that the model is right: it writes the schema
from the export and fails on anything it cannot represent. It runs in this
order (`Mapper.Run` in `backend/internal/importer/mapping.go`), each step
depending on the ones before:

1. `datasetsStore` → the space, with a user and an owner membership
2. `institutionsStore` → institutions, as reference data only
3. `accountsStore` → accounts, all **unlinked**: full history, no connection
4. `categoryStore` → categories, keeping `parentId`, `txfId` and the
   `knownCategoryId` system markers
5. `tagStore` → tags
6. `filterStore` → filters, before anything that references one
7. `transactionStore` → transactions with their splits, attachments and
   transfer pairs; every row has `source = 'simplifi_import'` and no
   external id
8. `scheduledTransactionsStore` → recurring series and their RRULEs;
   transactions are linked to their series by `stModelId` and `stDueOn`
9. `renameRuleStore` and the rules file → rules (see [Rules](#rules))
10. `goalStore` and each account's `goalBalance` → goals
11. `freeToSpendStore` → one spending-plan month per record, with
    `plannedSpendingItems` as envelopes and every `calculated*`, `*TxnIds`,
    `excluded*` and `overwritten*` field copied verbatim
12. `spendingWatchListStore` → watchlists
13. `securitiesStore`, `investmentHoldingsV2Store`, `investmentQuotesV2Store`
    → the security master and holdings
14. `accountsBalancesStore` → balance history
15. `alertRulesStore` → notification preferences: channels and on/off from
    each rule's preferences, and the threshold from the rule's own threshold
    field, else `inputValues`, else the default Simplifi offered in
    `inputVariables`, else the catalog's default. A threshold alert is never
    written without a number, because a stored rule with none never fires.

The spending plan's `calculated*` values are imported rather than recomputed
so that our own engine can be run over the imported transactions and
compared, month by month, with Simplifi's answers. Every mismatch is either a
bug in the engine or a rule not yet understood (see
[`calculations.md`](calculations.md)).

Transfer pairs come from `transfer`, which resolves in both directions.
`matchedTxn` is Simplifi's link from an entry to the downloaded row it was
merged with; its partner is usually gone and the links are not reciprocal,
so pairing on it would create false pairs, and a false pair silently removes
both rows from every income and expense figure. The importer never pairs on
it.

### What has no source

Each of these is a named warning in the run's report.

| Gap | What is written |
| --- | --- |
| `freeToSpendStore.goalsIds` names goals, not transactions | The goals bucket's amounts import verbatim with no contributing transaction ids. |
| Planned spending keeps one amount field | No transaction ids, exclusions, override or reset for planned spending; Other Spend has ids and exclusions but no override. |
| An envelope with no `rolloverAmount` | Its rollover starts at zero. |
| No `effective_date` equivalent | `effective_date` is null (read as "same as `date`"); card-charge cash-flow timing comes from the account's `statement_close_day` afterwards. |
| Attachment files are not in the export | The document record is; `storage_key` is `simplifi-import:<documentId>` with no file behind it. |
| Transaction `type` / `subtype` (investment actions) and category `usageType` | No column; warned per occurrence. |
| `accountNumberMasked` longer than eight characters | The trailing eight are kept, which is what the relink screen pairs on. |

Where Simplifi's shapes vary, the importer accepts each known form and
errors on anything else: a split as `allocations`, as a `split` object
holding `allocations` or `items`, or as a `split` flag; balance points as
flat records or under `balances`, `points` or `history`. It also reports
whether `plannedSpendingItems` ids repeat across months, which says whether
an envelope keeps its identity from month to month.

## Rules

Simplifi has two kinds of rule, and IndexedDB holds only one.

- **Rename rules** (`renameRuleStore`: `renamePayeeFrom`, `renamePayeeTo`,
  "If Payee Contains") each become a rule whose filter is one statement-name
  clause and whose action is the rename.
- **Transaction rules**, the ones on Settings → Rules with payee, account,
  category and amount conditions, are kept on Simplifi's server. What reaches
  IndexedDB is the `transactionRuleId` written on each row a rule changed.
  The saved `transaction-rules` response (step 6) supplies the rules
  themselves.

Each rule in the file becomes a rule here (`transactionrules.go`):

| Simplifi | Here |
| --- | --- |
| Payee, original statement name (`STATEMENT`) | a `statement_name` condition |
| Payee, Quicken name (`INFERRED`) | a `payee` condition |
| Contains `A B` | `contains` with the keywords `A` and `B`, all required |
| Is exactly | `is_exactly` |
| each "+" payee alternative | a filter group of its own; groups are ORed |
| specific accounts, categories, an amount (expense or income) | the same condition repeated in every group |
| rename, category, tags, note | `set_payee`, `set_category_id`, `add_tag_ids`, `set_notes` |
| exclude from reports, from the spending plan | the two flags, each only when the rule sets it |
| mark as reviewed | `set_is_reviewed` |

Simplifi also wants a Contains rule's keywords in the order typed; here they
are required in any order, and each such rule is a warning. The rules keep
Simplifi's order as their priority. A rule that matches or sets business
usage, names an account, category or tag the export lacks, or has no
condition or no action arrives switched off with a warning saying why; a rule
Simplifi marked deleted arrives switched off. An operator or payee type the
importer does not know is an error.

A `transactionRuleId` on the rows that the file does not define (no file was
given, or the rule was deleted in Simplifi since) is rebuilt from those rows.
Its condition is the statement names the rows arrived with, matched exactly;
its actions are only what every one of the rows agrees on: the payee they
were all renamed to, the category every unsplit row carries, the tags they
share, and each exclusion flag on its own. Reviewed is never inferred. The
rule is named "… (rebuilt from Simplifi)", or, when the rows agree on
nothing, "… (rebuilt from Simplifi, choose its actions)" and switched off. An
account, an amount range or a wider "contains" cannot be recovered from the
rows, so compare each rebuilt rule with Simplifi's before relying on it.

Every imported rule records its origin in `rules.source_ref`:
`simplifi:renameRuleStore:<id>`, `simplifi:transaction-rules:<id>` from the
file, or `simplifi:transactionRuleId:<id>` when rebuilt from rows. A
transaction's `rule_id` names the rule that changed it.

### Importing the rules again

A space already imported can take the rules again without a second full
import:

```sh
agentifi import export.json --rules-only --owner-email you@example.com \
  --transaction-rules transaction-rules.json --dry-run
```

It maps the whole export, finds the space by owner and name (the dataset's
name, or `--space-name`; or `--space-id`), and writes only rules and their
filters. A rule whose `source_ref` is already in the space is left alone,
deleted or not, so a second run writes nothing. A rule earlier rebuilt from
rows is upgraded in place from the file's definition, found by the Simplifi
id both `source_ref`s end in: it keeps its id, so the rows that name it
still do, and takes the definition's conditions, actions, name, priority and
`source_ref`. One edited here since is listed, because the definition
replaces the edit. A rename rule without a `source_ref` that matches on its
clause and its rename is marked rather than duplicated.

Accounts are found by name, categories by full path and tags by name. If the
space no longer has one, or has two, that rule is refused and the run writes
nothing. This dry run needs the database, because it reports what the space
already holds, and rolls back.

## Relinking to SimpleFIN

SimpleFIN is the only bank aggregator. Simplifi's connections have no
portable credential, so no imported account arrives connected: the import
brings the history and SimpleFIN brings what follows. Joining the two is its
own step, and without the sync floor it would import the overlap a second
time.

1. Import every account unlinked.
2. Connect SimpleFIN (Settings → Accounts). A new connection waits in
   `pending_link` and discovers its accounts without syncing.
3. The match screen on Settings → Accounts pairs each SimpleFIN account with
   an imported one. `GET /connections/{id}/candidates` ranks the local
   accounts for each: an equal masked number weighs most, then an equal name,
   the same kind of account, the institution's name in the account's name,
   and a balance within a dollar. The first is the account's **likely**
   match only when an equal masked number or name backs it, it outscores
   every other, and no other bank account has the same one; the screen
   chooses a likely match in advance and every other account starts as
   **Create a new account**, with a line saying why (a tie, or only the
   kind, bank or balance alike). A person changes any choice, or chooses
   **Do not import this account**; checkboxes, with a quick selection of
   the balances under an amount (on the magnitude, strictly, as the small
   balance rule in [`calculations.md`](calculations.md) compares), refuse
   several at once. Nothing is written while choosing.
4. **Finish and sync** (`POST /connections/{id}/links/finish`,
   `service.Sync.FinishLinking`) writes every choice in one transaction, so
   the accounts are in place when it answers. A paired account takes the
   SimpleFIN account and its **sync floor**, the date of its newest posted
   row; an account chosen as new is created with the balance the Bridge
   reports; a refused one is listed under **Not imported**. A choice that
   cannot be applied (one account chosen for two at the bank, an account
   deleted or fed by another connection) refuses the whole finish and writes
   nothing. Pointing a feed at a different account keeps the account it fed
   before, detached, with its rows; finishing again with nothing changed
   leaves an account already fed alone, floor and all.
5. Finish then starts the first sync in the background. Settings → Accounts
   and the sidebar's refresh control show how far it has got: the account
   being read, of how many, and the transactions imported so far. When it
   ends, the card sums up what arrived (accounts, new transactions,
   warnings), or says why it stopped and offers **Try again**; rows already
   imported stay. The card's sync button and the daily scheduled sync run
   the same way and report the same progress. One sync of a connection runs
   at a time: a second request joins the run in progress. The progress is
   kept by the server process, so a reload shows it and a restart forgets it.

The first sync reads from 30 days before the account's newest row, in
requests of at most 45 days. The floor refuses only the creation of a row
dated before it; the read still reaches behind it, so an existing row can be
updated and a pending charge from before the link can settle.

Imported rows have no SimpleFIN id, so the sync dedupes in two steps
(`backend/internal/service/sync.go`): first on `(account_id, external_id)`,
then by content, on `fingerprint` of the date, the amount and the bank
wording folded to lowercase letters and digits. Each existing row can be
claimed once.

The floor and the content check still let a bank copy through when Simplifi
dated the charge a day or two before the bank posted it and the wording
differs, since the bank's row is then on the newer side of the floor and
matches nothing by content. Such a pair is not guessed at. After every sync, a
file import and a Simplifi import, the app looks for two rows in one account
with exactly the same amount, dated within two days of each other, written by
different sources, and lists them for a verdict at **Settings → Possible
duplicates** (a notice on the Transactions page links there). **Same charge**
retires one copy, the bank's by default, and the kept row takes over the bank's
id so the next sync settles it instead of writing it again. **Two
transactions** remembers the answer and the pair is never asked about again.
**Look again** on that screen checks the whole history, for rows that were
there before the check was. The rule is
[`calculations.md` §2](calculations.md#possible_duplicatesrows-ruled_out---pairs).

After the first sync, compare each linked account's balance in the ledger
with the bank's. The sync does not check this for you: a linked account takes
the balance SimpleFIN reports. A mismatch means rows were duplicated or
missed around the floor.

Accounts SimpleFIN cannot reach (manual assets, vehicles, real estate,
closed accounts) stay unlinked. That is a normal end state.

## CSV, OFX and QFX

Settings → Accounts → **Import a statement** takes a `.csv`, `.ofx` or
`.qfx` file, shows a dry run, then writes it. The API is `POST /imports`
(multipart: `file`, and optionally `account` and `dry_run`); the format is
read from the content. The same imports run from the command line:

```sh
agentifi import-csv <file.csv> --space <id or name> --dry-run
agentifi import-ofx <file.ofx|.qfx> --space <id or name> --account "Checking" --dry-run
```

Both write into an existing space (`--space`, or `--owner-email`), set
`source = 'file_import'` on each row, and dedupe on `(account_id,
external_id)`, so importing the same file twice adds nothing. Rules,
transfer pairing and recurring matches run after the write, as after a sync;
if that step fails, `agentifi settle` finishes it (see
[`operations.md`](operations.md#upgrades-and-rollback)).

- **CSV** is Simplifi's *Export transactions* file and no other layout. Its
  columns are Date, Account, Flag, Reviewed, Status, Payee, Statement name,
  Category, Split, Tags, Notes, Attachments, Exclusion, Recurring, Amount and
  Check #; Date, Account, Payee, Category and Amount are required, and a
  column not on that list is noted and skipped. Accounts, categories and tags are found by
  name. The external id is derived from the account, date, amount, statement
  name and payee.
- **OFX and QFX** are one reader. The external id is `ofx:<FITID>`, or the
  derived key when a statement has no FITID. The account is the one named by
  `--account` (or the upload's `account` field), otherwise one named from the
  statement, such as "Checking ····1234".
