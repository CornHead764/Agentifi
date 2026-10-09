# Calculations

Every number the app displays that is not stored verbatim. This is the
specification the `backend/internal/domain/` package implements and the test
suite verifies against the Simplifi export.

> **The rule for this document.** A number that appears on a screen either has
> a named function here, or it does not ship. When behaviour is ambiguous, the
> Simplifi export is the arbiter, not intuition about what is correct.
>
> The stated exception is a status derived per row rather than summed or
> compared, such as a transaction's receipt status: it is specified beside
> the entity it describes, in *Receipts* in
> [§7 of data-model.md](data-model.md#7-accounts-and-connectivity), which
> covers `domain.ReceiptStatusOf` and the dashboard's *Missing receipts*
> count.

## How these get tested

Two layers, in increasing order of what they prove:

1. **Unit** — pure functions over hand-built structs. Fast, exhaustive on
   edge cases (empty months, zero targets, sign flips, month boundaries).
   Expected values come from this document, never from running the
   implementation.
2. **Golden** — run the engine over the imported Simplifi transactions and
   assert it reproduces Simplifi's own stored `calculated*` values, for every
   month (`backend/internal/api/golden_test.go`). Any drift is a finding, not
   a test failure to paper over. It reads a Simplifi export you supply in
   `data/simplifi`; without one the golden tests skip and say what is
   missing.

Layer 2 is the reason the import preserves Simplifi's answers instead of
recomputing them (see [importing.md](importing.md)). Do not skip it.

The golden tests cover two things. `TestGoldenSpendingPlanMatchesSimplifi`
reopens every imported month, so a month Simplifi closed out is recomputed
rather than read back frozen, and compares every bucket, every envelope's
spend and the month's total against the stored figures; a bucket that
disagrees is reported with the rows only one side lists. Each figure it
cannot arbitrate is named with its evidence on every run.
`TestGoldenNetWorthMatchesTheExportsBalances` compares net worth, assets,
debt and every account's balance against the export's own balances. Anything else
this document says Simplifi does is pinned by unit and API tests only, and
[§13](#13-settled-choices-and-departures) lists where that matters.

---

## 1. Money primitives

- **Storage** — `numeric(15, 2)` in Postgres, `domain.Money` in Go — a
  distinct type over `shopspring/decimal` with no `FromFloat` — never float.
- **Serialization** — JSON string. Coerced to `number` once, at the API
  client boundary, per field. A `numeric` column arriving as `"1900.00"` and
  `+` concatenating it is the failure this rule prevents.
- **Rounding** — half away from zero (`ROUND_HALF_UP` in the decimal sense:
  −0.005 becomes −0.01) to 2 dp, applied **once**, at the end of a
  calculation chain (`Money.Round`). Never round intermediates.
- **Sign convention** — one convention, everywhere: **expenses are negative,
  income is positive**, in the stored `amount`. Display flips signs; storage
  never does. Debt account balances are stored negative.
- **Currency** — every amount carries a currency and an `amount_primary`
  converted at `fx_rate_used`. Cross-account aggregates (net worth, reports,
  watchlists) sum the primary amount (`Transaction.PrimaryAmount`); anything
  that combines with one account's own figures sums native amounts
  ([§13](#choices-the-rules-leave-open)). Simplifi is single-currency; this is
  a capability worth keeping.

---

## 2. What counts

The single most important definition in the system, and the one that must
live in exactly one place: `backend/internal/domain/predicates.go`.

### `counts_as_income_or_expense(txn) -> bool`

`domain.CountsAsIncomeOrExpense(posting, visibleAccountsOnly)`. False when
**any** of:

- the row is deleted, or is a provider's estimate row (a forecast held in the
  ledger only for its slot on the Bills screen)
- `txn.excluded_from_reports`
- the account has `excluded_from_reports`
- the account is **ignored** (`ignored_at` set;
  [§3](#ignored-accounts)), whatever its flags say and whichever accounts the
  caller asked for
- **the category has `excluded_from_reports`**
- the account is closed *and* the caller asked for visible accounts only
  (`visibleAccountsOnly` has no default: whether a closed account counts
  differs per screen)
- the row is a **transfer**, which is either of two things and not only the
  first: `txn.transfer_pair_id` is set — **both** legs of a matched pair — or
  the row is filed under a transfer-type category, which is how an unmatched
  one is spelled
- `txn.source == 'opening_balance'`
- `txn.source == 'balance_adjustment'` (asset revaluations are not spending);
  these two are `Source.IsCashFlow`

> **"Is this a transfer" has one owner: `domain.Posting.IsTransfer`.** Both
> clauses, together, in one place. Written as two clauses it gets written as
> two clauses everywhere else too, and then somebody writes only the first —
> and a copy that tests the pair id alone reports a transfer-category deposit
> as portfolio growth or as spending. Anything that needs the answer asks the
> method; anything holding only a category asks `Category.IsTransfer`, which
> is the half it can see.

> **The exclusion flags live at four levels.** Transaction, account,
> category and rule, eight independent booleans (see ground rule 4 in
> [`AGENTS.md`](../AGENTS.md#ground-rules)). The category pair is the one that is easy
> to forget: it is a standing instruction about every row filed under the
> category, it is what Simplifi's `isExcludedFromReports` /
> `isExcludedFromBudgets` on `categoryStore` mean, and the settings screen
> offers both. Every path from a stored row to a domain value must carry the
> category's flags, or the switch does nothing.

> **The category clause has to be askable of a category alone.** A split
> row's category lives on its splits and the parent carries none, so reports,
> watchlists and the assistant's spending-by-category ask this question once
> per allocation as well as once per row. It is one function —
> `domain.Category.CountsAsIncomeOrExpense`, which folds in the
> transfer-category rule — precisely so the two altitudes cannot answer
> differently. The spending plan is the exception and does so knowingly: its
> unit is the whole transaction, so a *split* row's category flags are
> invisible to its eligibility test, exactly as a split under a transfer
> category is.

> **Trap.** A transfer leg whose partner was deleted keeps its
> `transfer_pair_id` and is therefore excluded from profit and loss forever
> while being ineligible to ever re-match. Anything that deletes transactions
> must release the surviving partner first. Find orphans by grouping on pair
> id and looking for groups of one (`domain.FindOrphanTransferLegs`;
> `POST /transfers/orphans/repair` releases them). An orphan can swing a
> month's savings rate far below −100%.

### `counts_toward_spending_plan(txn) -> bool`

`domain.CountsTowardSpendingPlan(posting, monthExcludedTxnIDs)`, and
`domain.Category.CountsTowardSpendingPlan` for the category alone.
Deliberately **not** the same predicate. False when any of:

- the row is deleted or an estimate row (the Bills bucket already projects
  the occurrence from the series)
- the row is in the month's own `excluded*TxnIds` lists on the spending-plan
  row
- the independent flag `excluded_from_spending_plan` (Simplifi's
  `isExcludedFromF2S`) on the transaction, on the account, or on the
  **category** (Simplifi's `isExcludedFromBudgets` on the category)
- the account is ignored ([§3](#ignored-accounts)) or closed
- the account is not one money is spent from — only cash and credit-card
  accounts are (`AccountKind.SpendsFrom`). What posts to a loan, investment
  or asset is repayment, trades or revaluation, already counted on the side
  the money came from
- the row is a transfer (`Posting.IsTransfer`); a credit-card payment is one
- the source is an opening balance or a balance adjustment

`ComputeMonth` also adds a row filed under a goal as spending to the month's
excluded set (see [§6](#6-goals)).

### `counts_toward_balance(txn) -> bool`

Different again. Excluding a transaction from reports does not undo money
leaving the bank. `domain.CountsTowardBalance` excludes only deleted rows and
estimate rows — a bill due next month has moved nothing yet. Pending rows are
in: the money has moved even though the bank has not settled it, and a caller
wanting the settled figure asks `domain.IsSettled`. The function exists so
that every balance site states that in one place.

### `ledger_kind(amount, category) -> income | expense | transfer`

**Which side of the ledger one part counts on. The category decides, not the
sign.**

`domain.LedgerKind(category, hasCategory)`, and `Part.LedgerKind` /
`Part.IsRefund` / `Part.IsEarnings` for the readers whose unit is a part.

- a part filed under an **income** category is earnings, whichever way its
  amount points — a clawed-back paycheck is still income;
- a part filed under a **spending** category is spending, whichever way its
  amount points — a $100 grocery refund belongs under Groceries and brings
  the month's grocery total **down** by $100;
- a part filed under a **transfer** category is neither, which is money
  moving inside the household;
- a part with **no category at all** is spending, whichever way its amount
  points. Income is what somebody filed as income. An unfiled credit is most
  often a refund nobody has placed yet, and it nets against spending until it
  is filed or linked to the charge it gives back
  ([`refund_link`](#refund_linkrefund_txn-charge_txn)); the review queue's
  Uncategorized tile is where it waits. This is also how Simplifi files it:
  its *Uncategorized* sits among the spending categories, and its spending
  plan's Income is the rows filed under an income category.

Asked per part, never per row: a split row's category lives on its splits,
so a receipt with one refunded line answers twice.

Read by the sign instead, a refund gets two figures wrong at once — income is
inflated by money nobody earned, and the category the money came back to
keeps showing spend that has since come back. The sum of the two is right,
which is why it goes unnoticed. The register's Spending tab
(`domain.Aggregate`), the reports engine's totals, its sign convention and its
kind dimension, the Monthly Summary panels, and the spending plan's Income
and Other Spend buckets all route through this one function, so a store
return cannot be spending on one screen and income on the next.

Simplifi answers the same way for its Spending aggregation: on its Spending
screens the ranked legend's tail bucket can be negative when refunds outweigh
spend, and the over-time bars put negatives below the axis. Simplifi's Income
& Expense *report* is not documented on this point; the report follows the
same rule for consistency with the tab, and no golden test covers reports.
The spending plan's Income and Other Spend buckets, which also read it, are
golden-tested.

### `refund_link(refund_txn, charge_txn)`

**The user saying which charge a credit gives back**, for the credit no
category can place: the one the bank filed under nothing, or filed under the
wrong thing.

`domain.ApplyRefundLinks` refiles a linked credit under the category of the
charge it gives back, so every figure downstream — plan, reports, watchlists,
register tabs — counts it where the money left. It rewrites the **posting**,
not the stored row: the link is a statement about what money means, not an
edit of what the user typed, so the register still shows the category the
bank supplied.

Directional and deliberately not one-to-one. A $30 credit against a $200
hotel bill is a partial refund, and three credits against one order are three
links naming the same charge. Three shapes refile nothing, because the answer
would be a guess or there is nothing to move: a credit whose links name
charges filed under **different** categories (split the credit instead), a
credit that already has **splits** (the user has already said where each part
goes), and a link to an **uncategorized** charge.

A merchant's own record of a return can make the link without the user: when
a credit matched to a return and a purchase matched to the same order as money
out are both on file, `domain.ChooseRefundedCharge` names the purchase the
credit goes to. A purchase takes a credit only while it has room: its amount
less the credits already linked to it must cover the credit. Of those with
room, the one the credit settles exactly comes first, then the oldest.

A credit no return record explains is tied to its order by what the order's
invoice says was refunded (`domain.MatchMerchantRefundTotal`): an order placed
up to 100 days before the credit, whose refund total less the credits already
matched to it is the credit exactly (two such orders: the older, at lower
confidence), or, for a line on a gift card balance only, the one order whose
remaining refund can hold it. The link to the purchase then follows as above.

A charge is **fully refunded** when the credits linked to it add up to its
amount or more, and **partially refunded** when they add up to less
(`domain.RefundState`). More than the charge is still full: a refund can carry
the tax or a gift card's share the card charge never held.

Where the affordance is offered is `domain.CanBeARefund` and
`domain.CanBeRefunded`, and where a charge ranks is
`domain.RankRefundCandidates`: dated on or before the credit by the
reporting date of each (`domain.RefundAge`, trap 4), nearest first. A refund is meaningless on a transfer leg, on
a credit-card payment — which is a transfer, and `Posting.IsTransfer` is what
says so — and on a row already filed as income. The ranking's "same merchant"
test reads `statement_name` and never the payee (ground rule 5): renaming a
charge must not move it up or down the list. Only a row with no bank wording
at all — a hand-entered one — is read by its payee instead
(`Transaction.MatchName`). The two wordings are compared by merchant words,
not whole strings, because a credit's line seldom repeats its charge's:
`domain.MerchantTokens` drops words with a digit (store numbers, references),
words under three letters and bank boilerplate, and
`domain.SharesMerchantWording` counts a charge as the same merchant when it
has at least half the credit's words, rounded up. "FRESH MKT 0822 RETURN" is
the same merchant as "FRESH MKT 0417 SPRINGFIELD" and not as "FRESH CUTS
SALON".

Storage is `transaction_refund_links`, one row per link rather than a token
on each side. A link stored on the rows it joins can half-exist, as a
transfer pair can; `store.DeleteTransaction` drops a row's links in the same
database transaction as the delete so no caller can forget.

### `category_vote(history, threshold)`

**How sure the payee's history is about a row's category.** Not a
calculation either: it decides only whether the category check files a row
from history alone or asks the model, and what the model is told. The model
is asked whenever the vote does not settle it, and is expected to guess from
the row itself when there is no history at all; the threshold is the only
gate between the two.

`domain.VoteCategory` over the payee's past rows, each with a similarity to
the row being decided, an age in days, a category (or none), and whether it
is a transfer leg:

- **Transfer.** When transfer legs are at least 0.6 of the rows, the verdict
  is *transfer* with that share as its confidence, and the check proposes
  Transfer, or Credit Card Payment for a row on a credit card
  (`domain.TransferCategoryFor`).
- **Voters** are categorized rows that are not transfer legs. Each weighs its
  similarity over the best similarity in the history (a best of zero counts
  as one), times an age factor — 1 up to 180 days, 0.7 up to 730, 0.5 beyond
  — times 1.25 when the household reviewed it.
- **Split rows** whose every split is filed are neither voters nor
  uncategorized: they name no one category, and their parent's empty one is
  not a decision. They are out of `coverage` and the model sees their
  splits. A split with a part unfiled is uncategorized
  (`Transaction.IsUncategorized`).
- **No voters** is *no history*, however many uncategorized rows there are.
  An uncategorized row says nothing about which category; it is counted, and
  the model is told how it came to be uncategorized.
- Otherwise the leader is the category with the largest summed weight (ties
  by id), and

  - `share` = leader's weight / all voters' weight,
  - `count` = min(leader's rows / 5, 1),
  - `coverage` = voters / (rows − transfer legs − filed split rows),
  - `confidence` = share × count × coverage.

  At or above a non-zero threshold the verdict is *confident* and acts
  without the model; otherwise it is *uncertain*. A threshold of zero never
  acts alone. A confident winner that runs the wrong way for the money is
  held back to uncertain (`CategoryVerdict.WithKindCheck`).

**Who reviewed a row** is `domain.ReviewedByHousehold`: the reviewed flag
counts only when somebody here set it. A row imported from Simplifi or a
file carries the flag from the file, and a row on an account that is born
reviewed (anything but cash and credit cards, `AccountKind.BornReviewed`)
has it however it arrived, so neither is a household decision. Uncategorized
non-transfer rows are counted three ways, weakest first: *awaiting* (nobody
has looked), *arrived reviewed* (the flag came with the row), and
*left alone* (the household reviewed it and kept it uncategorized). Only the
last lowers the model's confidence; none forbids a category.

**Uncategorized is never suggested.** It is the absence of a category, so
`Category.CanBeSuggested` is false for a category that carries the name (an
import can create one). The model is not offered it, a proposal or stated
answer naming it is dropped, a recurring or mail-drafted rule never takes it,
and past rows filed under it vote as uncategorized rows.

### `refund_category_suggestion(credit, merchant_history)`

**What the categorize check proposes for a credit from a shop the household
buys from.** Not a calculation — nothing is refiled until somebody accepts it
— but it is the other half of the rule above: an unfiled credit nets against
spending, and this is what gets it filed under the category it came back to.

`domain.VoteRefundCategory`, run by the category check on any credit (money
in, not on a loan account) before the ordinary vote is acted on:

- the history is the payee's own rows, transfer legs aside: rows sharing at
  least half the credit's `domain.MerchantTokens`, rounded up, where each
  row's tokens are taken from its `statement_name` and its `payee` together
  (a credit whose wording has no tokens reads its own account's rows
  instead);
- it is a **merchant's** history only when its charges outnumber its
  credits. An employer or a person who is mostly paying the household in
  keeps the ordinary vote, and the direction check below;
- the charges alone vote, by the same weights and threshold as the ordinary
  vote, and a credit voted into their expense category is **not** a
  direction conflict — money coming back to a spending category is exactly
  what a refund is;
- a **clear original purchase** wins outright
  (`domain.OriginalPurchaseAge`): a categorized charge for the same amount to
  the cent from the same merchant by the refund picker's own test
  (`domain.SameRefundMerchant`, the bank's wording and never the payee),
  dated on or before the credit and no more than
  `domain.RefundCandidateWindowDays` (100) before it. Both dates are the
  reporting date (`domain.RefundAge`, trap 4), as in the picker, so the vote
  and the picker's first offer name the same charge. The nearest such charge
  names the category, and the proposal says which purchase it gives back.

The result goes through the check's own mode, like every other verdict it
reaches, except that a refund is never filed unasked: *propose* and *apply*
both leave a suggestion on the row for a person to accept, *observe* only
reports. A model's answer is held the same way, and the assessment it is shown
says that money in under an expense category is what a refund looks like
rather than a history gone wrong. A threshold of zero means the history never
acts alone, and a clear purchase does not change that.

### `suggestion_batch_summary(rows, runs)`

**How a "Suggest categories" request went, and how its answers compare with
the categories the rows already had.** Not money, but a count a person reads
as a verdict on the check, so it has a named function:
`domain.SummarizeSuggestionBatch`, over each run's stored result.

Each finished run about a row is reduced to one result when it finishes
(`domain.CategoryCheckResultOf`), from the row as the run found it:

| Result | The run |
| --- | --- |
| `agreed` | named the row's own category: the history confirmed it, or the model's answer named it |
| `differs` | proposed or applied another category for a row that had one |
| `unsure` | reached no answer about a row that had a category |
| `suggested` | proposed or applied a category for a row that needed one |
| `undetermined` | reached no answer about a row that needed one |
| `skipped` | settled the row without comparing a category: a paired transfer leg, a split with every part filed |
| `failed` | failed, whatever else it reports |

A proposal outranks everything but a failure, so a row is `differs` whether
the history or the model disagreed. A row with more than one run (a second
transaction automation) counts once, as its most telling result: `differs`,
then `suggested`, `agreed`, `unsure`, `undetermined`, `skipped`, `failed`. A
row with any run still waiting is *pending*; a row with no run at all
(cancelled, taken over by a later request for the same row, or deleted) is
*not run*. *Done* is every row that is not pending. The finished rows are
counted twice over: by result, and by whether the row was **reviewed when the
batch was queued**, so accepting a suggestion part-way through does not move
a row from one half of the comparison to the other.

### `is_padding(txn) -> bool`

**An income row that restores what a paycheck deduction took for a
purchase**: the second row a mail rule with **Also record the income it was
taken from** writes, linked to the purchase (`padded_txn_id`,
`domain.Transaction.IsPadding`). It changes no figure. The pad is income in
reports, in the plan's Income bucket and in every balance and net worth, and
the register's running balance is computed over every row, pads included.

Hiding is display only. A register query with `padding=hide` leaves pads out
of the listed rows and of "mark all as reviewed" (`domain.FoldPadding`), and
answers `padding_count` and `padding_total` beside `count` and `total`, so
the listed total plus the padding total is what every matching row nets to;
the aggregate over the same query counts them. The register and the
dashboard's tiles ask for it; a query that does not, such as a category's
usage count, sees every row. The plan's bucket lists fold padding rows into
one line per name and category, summing them; the bucket figure is unchanged.

### `possible_duplicates(rows, ruled_out) -> pairs`

`domain.FindDuplicateCandidates`. **Two rows that look like one charge
written by two sources**, proposed for a person to decide. It exists for the
Simplifi-import-then-SimpleFIN case: the sync floor refuses a bank row only
when it is dated before the newest imported row, and the sync's own content
check needs the date and the folded wording to be equal, so a bank row posted a
day after Simplifi's date, under the bank's wording, is created beside the
imported one.

Two rows are a candidate pair when all of these hold:

- both are live, not forecasts, not an opening-balance or adjustment row
  (`Source.IsCashFlow`), and not zero;
- same account, same currency, and the same amount to the cent (a charge and
  its refund differ in sign and never pair);
- their posted `date`s are at most `DuplicateToleranceDays` (2) apart. The
  posted date, not the effective date: the question is whether the sources
  agree on the day, not which month a card charge files under;
- their `source`s differ. Two rows of one source are never paired, because a
  source dedupes itself and two coffees on one day are two coffees;
- the pair is not in `ruled_out`, the pairs a person said were two real
  transactions.

The wording is not compared: it is what differs between sources (ground rule
5: matching reads `statement_name`, and here the two spellings are the
disagreement). The screen shows both for the person to judge.

A row is in at most one pair. Pairs are taken fewest days apart first, then
oldest, then by id; ruling a pair out before the assignment frees its rows for
other pairs. The result does not depend on input order. When a run follows an
ingest, only pairs touching a row that just arrived are proposed
(`CandidateIDs`).

`SuggestedDuplicateKeep` picks the copy to keep if the pair is a duplicate: the
one not written by the sync (it may carry the person's category, note or
renamed payee), else the earlier dated, else the lower id. A duplicate verdict
retires the other copy through the same path as deleting it, so its transfer
pair is released first (trap 2) and any partner leg re-pairs if it can; when
the retired copy was the bank's, the kept row takes its aggregator id. A
distinct verdict is stored and never revisited.

### `reporting_date(txn, mode) -> date`

`domain.ReportingDate` / `Transaction.ReportingDate(mode)`, with `mode` one of
`domain.DatePosted` and `domain.DateEffective`.

`txn.date` is when it happened. `txn.effective_date` is when it hits cash
flow. They differ for credit cards, where `effective_date` is the due date of
the statement the charge lands in ([§3](#credit-card-fields)). **Reports,
watchlists, the spending plan, and the date conditions of rules, guidance
notes and automations read `effective_date`; the register shows `date`.** Getting this backwards shifts a whole month of card spending. A row
with no effective date (every non-card row) falls back to `date`.

---

## 3. Account balances

```
balance                = provider figure for connected accounts
                       = opening_balance + Σ settled amounts            for manual
balance_with_pending   = balance + Σ pending txn amounts
available_balance      = balance − goal_balance − pending_holds
goal_balance           = Σ savings goals' saved_so_far for goals on this account
```

`domain.AccountBalance`, `domain.BalanceWithPending`,
`domain.AvailableBalance`. Every per-account sum uses native amounts
(`Posting.NativeAmount`), never converted ones, because the opening balance,
provider balance, goal reserves and holds are all in the account's own
currency. A manual account's `balance` sums settled rows (`IsSettled`);
pending rows appear in `balance_with_pending`.

### Debt accounts and connected balances

- **Debt accounts** store negative. The provider reports a positive amount
  owed; normalize on ingest and store the normalized value
  (`provider.NormalizeBalance`, run on ingest and again whenever an account
  is re-classified, since the kind decides the sign). Simplifi stores
  `normalizedBalance` alongside `onlineBalance` for the same reason: never
  negate at each call site.
- **Manual loans** have no outside figure, so the transactions *are* the
  debt.
- **Connected loans**: the ledger reconciles toward the negated provider
  balance, with the synthetic `opening_balance` row absorbing the difference
  (`domain.OpeningBalanceForConnected` = provider figure − settled total) —
  which for a loan is its original principal, the one figure checkable
  against the paperwork. If the user states `opening_balance` explicitly,
  write it verbatim and stop re-deriving.
- **A balance as of a past date walks *backwards* from the provider's current
  figure**, adding back movements after that date. Never forward from the
  ledger. Forward accumulation drifts whenever a payment has left the payer
  but not been applied by the lender.

### A connected balance that drops to zero

A feed that loses a figure often reports 0.00 for it, and applying that can
drop a mortgage off net worth. A card paid off in full reads 0.00 as well.
The sync tells them apart by asking whether the account's own transactions
explain the zero.

```
suspect   = SuspectBalanceReset(established, reported, established_days, accept_zero)
previous  = TrustedBalance(stored, held, established)
movement  = Σ over rows whose counted amount changed this sync: after − before
expected  = previous + movement
explained = feed read ∧ reported given
            ∧ (expected = reported  ∨  previous + posted_movement = reported)   to the cent
```

- **Suspect** (`domain.SuspectBalanceReset`): the reported figure is zero, or
  missing, on an account whose last non-zero balance in history
  (`established`) is at least `SuspectResetThreshold` (100) in size and stood
  at least `SuspectResetMinDays` (14) days, and `accept_zero_balance` is off.
  Anything else is applied as reported.
- **Previous** (`domain.TrustedBalance`): the stored provider figure while it
  is a hold or is not zero; otherwise `established`, because a stored zero may
  be the dropped figure.
- **Movement** (`domain.ExplainReportedBalance`): the account's rows inside the
  sync's read window, read before and after its import and compared by id. A
  new row adds its amount, a pending row posting at a new amount adds the
  difference, a retired or deleted row takes its amount back; a pending row
  reposted under a new id therefore moves nothing. Rows counted are those that
  count toward the balance, pending included, as the ledger's walk counts
  them; `posted_movement` counts posted rows only, so a pending row that posts
  moves it by its amount. The feed does not say whether a balance includes
  pending rows, and banks differ, so either reading explains the figure.
- **Explained**: applied, and any earlier hold is cleared. This is a card paid
  in full: a payment equal to the balance owed, after however quiet a month.
  It is also how a hold ends when a later sync brings the payment that the
  first one lacked.
- **Not explained**: held. The account keeps `expected` (the trusted balance
  carried forward by the rows that did arrive), dated as the stored figure
  was or, from history, the day it was last seen; `withheld_balance` records
  the reported figure, and `withheld_balance_reason`
  (`BalanceExplanation.Reason`) the comparison: "SimpleFIN reported $0.00;
  the last balance -$1,234.56 plus 1 new transaction (+$1,000.00) comes to
  -$234.56." The account carries the sentence and the confirm action; it is
  not also one of the connection's bank warnings, which are the Bridge's
  word about a bank, so a held balance is reported once.
- **Nothing can explain** a figure when the account's transactions were not
  read this sync (the read failed, or the Bridge flagged the account's bank
  for reauthorization, whose figures are stale), when no balance was
  reported, or when the feed has never sent the account a transaction (a
  balance-only loan). Those keep the suspect rule alone, and the reason says
  which.
- A sync that reports a non-zero figure is not suspect and clears a hold
  without explanation; confirming a hold (`accept_zero_balance`) applies the
  zero and turns the check off for the account.

### Running balance in the register

Stored per row (Simplifi keeps `balance` on the transaction), not computed on
scroll. `domain.RunningBalances` computes it over every row that counts
toward the balance, pending included, ordered by posted date and then id. A
manual account starts from its opening balance; a connected account starts
from the provider figure minus every counted row, so the newest row ends on
the provider's figure. `service.RecomputeRunningBalances` rewrites the whole
account (not only a tail, so same-day rows keep a stable order) after any
insert, edit, delete, move between accounts or settle.

> **Trap.** An account register that seeds its running balance from a
> summary endpoint's `opening_balance` and then walks the rows must send the
> *same explicit date window* to both endpoints. A list endpoint that treats
> a missing `from` as all-time beside a summary that falls back to the first
> of the current month counts everything before this month twice, visibly
> only on accounts with real history.

### Historical balance

`domain.BalanceHistory(account, postings, from, through)` — the account
card's "Historical cash flow", served by `GET /account-balance-history`:

```
history(d) = BalanceAsOf(account, postings, d, posted date)
             for each d in [max(from, history_start), through]
```

The line begins at the account's history start ([§4](#4-net-worth)) when
that is inside the window: before it the account has no point, not a point of
zero.

One point per day, the balance at the end of that day, oldest first. It is
the same definition the net-worth history
([§12](#12-what-is-materialized-and-what-is-derived)) and the projection's
opening balance ([§8](#8-cash-flow-projection)) use,
walked once instead of recomputed per day, so the history ends where the
projection starts. The projection reads the ledger's walk,
`domain.LedgerBalanceAsOf`, which is the same figure on every day of the
history; it differs only before the start, where an account linked today
still opens its projection at its balance rather than at zero.

- **Posted date, not `effective_date`.** A balance moves the day the bank
  posts the row. A card charge's `effective_date` is its statement's due date
  ([§2](#reporting_datetxn-mode---date)), and filing the line by it would hold
  every charge off the balance for a month. `effective_date` stays the
  reporting date for reports; a balance is not a report.
- **Connected accounts** walk back from the provider's figure:
  `history(d) = provider_balance − Σ amount of rows dated after d`. **Manual
  accounts** walk forward:
  `opening_balance + Σ amount of settled rows dated on or before d`.
- **Pending rows:** left out of a manual account's line (they are not
  settled). In a connected account's walk they are subtracted like any row
  dated after `d`, because that is what `BalanceAsOf` does; see
  [§13](#choices-the-rules-leave-open).
- **Excluded rows count.** `excluded_from_reports`,
  `excluded_from_spending_plan` and an account's own exclusion flags do not
  apply: excluding a row from reports does not undo money leaving the bank
  (`counts_toward_balance`). Only deleted rows and a provider's estimate rows
  are out.
- **`through` is held at today**: the endpoint clamps its `to` there. For an
  account with no row dated after today, the last point equals the balance
  the account header shows (`AccountBalance`); a manual account with a
  future-dated settled row differs from the header by exactly that row until
  its day arrives.
- **The endpoint takes both ends explicitly** (`account_id`, `from`, `to`)
  and refuses a request without them. At most 731 days per request.

### Revaluing an asset

A house or a car priced from an outside estimate `E` on day `t`
(`domain.RevaluationOf`):

```
adjustment = E − balance                          (domain.RevaluationAmount)
row        = balance_adjustment of `adjustment`, dated t
anchor     = E, provider_balance_at = now          only when the account has a provider balance
```

No row is written when `E` equals the balance. The row is not cash flow
(`counts_as_income_or_expense` refuses `balance_adjustment`).

- **A manual asset** is its rows, so the row alone moves `balance` to `E`
  and every day before `t` keeps its value.
- **An asset with a provider balance** — the Simplifi import gives every
  imported account one, linked to SimpleFIN or not — reads `balance` from
  that figure and walks its past back from it. The row alone would leave
  `balance` where it was and push every day before `t` down by
  `adjustment`; a second revaluation, measured against the same unmoved
  figure, would push them down again. Moving the anchor to `E` in the same
  database transaction as the row makes `balance = E`, and the walk,
  `E − adjustment − Σ later rows`, gives every earlier day exactly what it
  read before. The next revaluation is measured from `E`.
- **A connected asset** (a connection or a SimpleFIN link supplies its
  figure) is not revalued: the next sync would overwrite the anchor, and a row
  against it only bends the history. The run reports it skipped and makes no
  lookup.
- `valued_at` is stamped whether or not a row was written, so an estimate
  that agrees does not leave the asset due. Editing what the estimate is
  looked up by — the property address, the VIN, the mileage, its date or
  the miles per year — clears `valued_at`, so the asset is due at once.

### Credit card fields

```
statement_balance   reported in a connector's `extra`, copied from a filed statement, or entered by hand
minimum_due         the same
due_date            the same
credit_used_pct     = |balance| / credit_limit
interest_rate       the Simplifi import, a connector's `extra`, or by hand
```

There is no bill feed from the bank — SimpleFIN's spec has no statement at
all — so the first three are whatever an issuer's connector volunteered,
whatever the issuer's statement said when it was filed as a bill on a billed
account linked to the card (`domain.StatementAfterBill` decides whether a
filed statement replaces what the card holds: copied, never computed), or
whatever somebody typed in, and any of them can be absent. **Absent is
hidden, not dashed:** the header leaves a figure out rather than drawing an
em dash, because five permanent dashes teach a person to stop reading the
strip. `credit_used_pct` (`domain.CreditUsedPct`) is null without a limit (or
with a limit of zero) and is still never zero in that case — "no limit on
file" and "none of the limit used" are opposite facts. `interest_rate` is a
rate and is read at either scale on the way in by `domain.APRAsRate`: 24.99
and 0.2499 are the same APR, and nothing that reports one says which it
meant. A figure of 1 or more is read as a percentage; a negative figure, or
one still above 100% after scaling, is refused rather than stored wrong.

`effective_date` for a card charge is the `due_date` of the bill it falls
into — from the bill's due date when one is on file, otherwise computed from
the statement cycle: the first payment-due day after the statement close
that falls strictly after the charge, so a charge on the closing day belongs
to the next statement (`domain.EffectiveDateFor`). A card without both a
statement-close day and a due date gets none, and a non-card row stores none;
both fall back to `date`. A hand-corrected effective date survives a re-sync.

### Small balances hidden from the account list

`domain.HiddenForSmallBalance(balance, account_threshold,
institution_threshold)` — the `hidden_small_balance` flag on `GET /accounts`:

```
threshold = account.hide_below_balance      if set
          = institution.hide_below_balance  otherwise, if set
          = none
hidden    = threshold is not none  and  |balance| < threshold
```

A crypto exchange behind SimpleFIN reports an account per coin, most of them
holding nothing; this is the rule that stops the drawer listing twenty rows
of $0.00.

- **The account's setting wins**, and `0` is "always show" — `|balance| < 0`
  is never true — which is how one account opts out of its institution's
  rule.
- **Magnitude, strictly under.** A few cents of credit hide as readily as a
  few cents held, and a balance exactly at the threshold shows.
- **`balance` is the listing's own** (`AccountBalance` above), not the
  provider's raw figure.
- **Display only.** The drawer, the settings list, and the Investing page's
  account tables (Balances, Performance) and holdings list leave the row out
  behind a "N small balances hidden" line that reveals them; a holding
  belongs to one account and goes with it. The drawer's group totals are
  still summed over every account in the group, hidden or not, and every
  Investing figure (the headline totals, the charts, the allocation, the
  holdings' share of the portfolio) still counts the hidden accounts. A
  picker that files something into an account — a transaction's account in
  the register and its dialog, a transfer's two sides, a goal's, a
  holding's, a series', a loan's "secured on", a bill's linked card, a mail
  rule's statement account (`pickerAccounts`) — leaves the row out behind
  the same line, and always lists the account already chosen. Net worth,
  reports, the register's account scope and every picker that chooses
  which rows to see (the Filter popover's accounts, the Investing account
  selector) ignore the flag: the rows in a hidden account still count, so a
  filter must still be able to reach them. A figure that silently lost
  forty cents is a wrong figure. An account that should count nowhere is ignored instead
  (below).

### Ignored accounts

An account the household has ignored — `accounts.ignored_at` set, answered
in the domain as `Account.IsIgnored` — is not one of theirs to count. Where
the small-balance rule above only tidies a list, this takes the account out
of every figure:

- **Out of** the account lists and every account picker (`GET /accounts` and
  every default `AccountQuery`), net worth and equity ([§4](#4-net-worth)),
  reports and watchlists (`counts_as_income_or_expense`), the spending plan
  (`counts_toward_spending_plan`), the register's all-accounts view, transfer
  pairing and merchant-order matching, alerts, and the sync: a linked account
  that is ignored is neither updated nor fed, and keeps its link.
- **Still true:** its balance (`counts_toward_balance` is unchanged — money
  moved), its transactions, its balance history (the daily snapshots keep
  running), its own register when asked for by id, and its four exclusion
  flags, which keep their values so un-ignoring puts back exactly what was
  there.
- **Transfer pairs are kept.** A payment from checking to an ignored
  exchange is still a transfer and not spending; releasing the pair would
  turn it into an expense the household never had.
- **"Ignore every empty account at an institution"** takes the accounts
  there for which `domain.HoldsNothing(balance)` — the listing's balance,
  zero to the cent — and ignores them in one act. It is a selection, not a
  standing rule: an account there that later empties is not ignored by it.

`POST /ignored-accounts`, `POST /ignored-accounts/empty`,
`GET /ignored-accounts` and `DELETE /ignored-accounts/{account_id}` are the
whole surface.

---

## 4. Net worth

```
net_worth(d)      = Σ account_balance_at(a, d) for a where include_in_net_worth(a)
                                                      and not ignored(a)
assets(d)         = Σ positive balances
debt(d)           = Σ negative balances          (reported as a positive figure)
debt_to_asset(d)  = |debt(d)| / assets(d)
change(d0, d1)    = net_worth(d1) − net_worth(d0)
change_pct        = change / |net_worth(d0)|     — undefined when d0 is 0, render "—"
```

- `include_in_net_worth` is its own account flag, distinct from the
  account-bar and reports flags. An ignored account is out whatever it says:
  `domain.Account.CountsInNetWorth` is the two together, and net worth, the
  kind breakdown, the group rows and equity all ask it.
- **An account contributes nothing before its history start.**
  `account_balance_at(a, d) = 0` for `d < history_start(a)`, and the account
  has no point on any balance line before it. Without this floor a past
  balance, which walks back from today's provider figure, answers today's
  figure for every day back to the household's first: a wallet linked this
  morning would read as money held for years. `domain.HistoryStart` is the
  one definition:

  ```
  history_start(a)   = a.history_starts_on                  when set (the override)
                     = automatic_start(a)                    otherwise
  automatic_start(a) = earliest of:                          (domain.AutomaticHistoryStart)
                         posted date of a's earliest row that counts toward the
                           balance (not deleted, not an estimate; pending counts)
                         opening_balance_on
                         the earliest balance the Simplifi import carried over
                         the day the account was added (created_at, server time)
  ```

  It moves on its own: importing older rows moves it back, with no manual
  step. An asset's imported value history is rows (revaluation adjustments),
  so an asset starts at its earliest value point. With no rows at all the
  start is the day the account was added, which is when its balance was
  first seen. The override may be earlier or later than the evidence;
  clearing it returns to automatic. `domain.BalanceAsOf` answers zero before
  the start; the ledger's own walk, `domain.LedgerBalanceAsOf`, does not, and
  is what a register window's opening balance and the projection's opening
  balance ([§8](#8-cash-flow-projection)) read, because both are about the
  ledger rather than about what was held that day. `/performance` reads the
  same balance series, so it honours the start too.
- **Holdings double-counting.** A holding filed under a connected brokerage
  account is already inside that account's balance. A total that filters
  those out must add the owning account balances back. Doing only the first
  half makes every SimpleFIN-synced position silently vanish from the Assets
  page. Any total that filters on "asset counts toward totals" adds the
  account side **in the same function**. `domain.CashOutsideHoldings` is
  that account side: each investment account's balance less the market
  value of the positions filed under it, so a cash-only account counts in
  full and a brokerage account adds only the cash beside its positions.
- Group rows in the Net Worth accounts panel show the change over the
  *selected window*, not month-over-month. Percentages are relative to the
  window start.
- **`account_balance_at(a, d)` is read two ways, over one definition.** The
  chart reads the materialized `balance_snapshots` row for `d`; anything
  asking the question at read time calls `domain.BalanceAsOf`. The stored row
  is `BalanceAsOf` too, but written from the provider figure as of the last
  sync, so a recent row can lag a read-time answer until it converges. The
  chart re-walks every derived row over the ledger as it stands when it reads
  it (`domain.RederiveHistory`), so a row edited, deleted or imported under
  an old point moves that point at once — see
  [§12](#what-a-balance_snapshots-row-holds) for exactly what a row holds.

---

## 5. Spending plan

Materialized one row per month (`domain.ComputeMonth`,
`domain.RecalculateChain`). Buckets, in the order the UI stacks them
(`domain.BucketOrder`):

```
income          + Σ income series occurrences ∪ actual income txns   (positive)
bills           − Σ bill + subscription + transfer/CC-payment series (negative)
planned_spend   − Σ envelope targets                                 (negative)
other_spend     − Σ actual spend not in bills and not in an envelope (negative)
goals           − Σ goal contributions where is_taken_from_plan      (negative)
rollover        ± carried in from the prior month
─────────────────────────────────────────────────────────────────────
left_this_month = rollover + income + bills + planned_spend + other_spend + goals
per_day         = left_this_month / days_remaining_in_month   (today included)
```

`per_day` has no value once the month is over, rather than dividing by zero.

> **Six buckets here, seven columns in storage.** `bills` above folds
> together what Simplifi keeps as three separate bucket families — bills,
> subscriptions, and transfers/card-payments. The stored row keeps all three
> separately, field for field, so the golden test can compare against
> Simplifi's own `calculatedBillsAmount` / `calculatedSubscriptionsAmount` /
> `calculatedTransferAmount` directly. The UI stacks them as one line. The
> two are not in conflict; see [data-model.md §5](data-model.md#5-spending-plan).

Every bucket carries the five-field pattern from Simplifi:

```
calculated_amount     what the engine computed
contributing_txn_ids  the exact rows — this is the audit trail, and it is what
                      makes the number explainable in the UI
excluded_txn_ids      rows the user dropped for this month only, without
                      mutating the transaction
overwritten_amount    a user override that wins over calculated_amount
reset_overwritten     clears the override
```

`effective(bucket) = overwritten_amount if set else calculated_amount`
(`Bucket.Effective`). A row excluded from one bucket is excluded from every
bucket of that month (`MonthInputs.AllExcludedTxnIDs`), so it cannot reappear
in another.

### Rules that are easy to get wrong

1. **A bill that has posted counts once, not twice.** An occurrence is
   either still expected (use the series' expected amount) or fulfilled by a
   matched transaction (use the posted amount). The link is `stModelId` +
   `stDueOn` — the series *and* the date slot (`domain.SeriesSlot`,
   `domain.MatchOccurrences`). Matching on series alone double-counts a
   month with two occurrences.

   **The posted amount lands in the month the money moved, not the month the
   slot falls in.** The two are routinely different: a utility bill can post
   about a month *ahead* of the occurrence it fills, and a Simplifi export's
   own figures put each one in the month it posted. A filled
   occurrence therefore contributes nothing to its own month's arithmetic —
   it is still listed there, still showing what it cost, but the amount
   belongs to the month (by effective date) whose free-to-spend actually
   fell. A posting that belongs to a series but fills no occurrence at all
   counts the same way, which covers the rows naming a series and no slot
   and the schedules the recurrence cannot express. A series-linked posting
   is always claimed by its series, so it never resurfaces as Other Spend in
   the month it posted.

   Each bucket therefore carries two figures. `calculated_amount` includes
   the month's still-expected occurrences, because the plan has to say what
   is still owed. `posted_amount` is the part a transaction backs, and it is
   the only one comparable against Simplifi, whose stored bucket figure
   equals the sum of the transaction ids it lists on every month of a
   Simplifi export; the golden test compares Income and Bills on it.
2. **Transfers and credit-card payments net to zero** inside Bills
   (`SeriesKind.NetsToZero`). They are listed for visibility, grouped
   separately, and their subtotal is $0.00. They must not reduce
   free-to-spend — the spending already counted when the purchase posted.
3. **Envelope spend is not also Other Spend.** The unit is a *part* — one
   split, or the whole row when it has none (`domain.Part`). A part matched
   by an envelope's filter leaves the Other Spend bucket; a part matching two
   envelopes belongs to exactly one — resolved deterministically by creation
   order, then id (`domain.AssignEnvelopes`), with the losers recorded — or
   the totals will not reconcile. **A split transaction is placed one part at
   a time**, so a receipt split between two envelopes' categories gives each
   envelope its own share, and a share matching no envelope falls to Other
   Spend. Ownership is what makes the month reconcile, and it holds per
   part: every part is claimed once or not at all.

   Worked example: a $340.00 receipt split Groceries $240.00 / Home
   Improvement $100.00, with only a Groceries envelope, charges that envelope
   $240.00 and Other Spend $100.00. A $60.00 row split Gaming $30.00 / Gifts
   $30.00, with an envelope for each, charges each $30.00 — neither envelope
   carries the other's spending.
4. **Other Spend is actuals only**, never projections. The projection is a
   separate field (`projected_other_spending`).
5. **Closed-out months freeze.** `is_closed_out` stops recalculation.
   Editing a transaction in a closed month changes reports but not that
   month's plan. Without this, every historical plan silently rewrites
   itself.
6. **Rollover chains.** Month N's rollover is month N−1's `left_this_month`,
   which depends on *its* rollover. Recalculating month N−1 must cascade
   forward through every open month (`domain.RecalculateChain`). Closed
   months stop the cascade: later months carry the closed month's stored
   figure.
7. **Income is what was filed as income.** A positive part counts in Income
   only when an income series claims it or it is filed under an income
   category (`ledger_kind`, [§2](#2-what-counts)). An **uncategorized**
   positive is not income: it nets against Other Spend — or against the
   envelope whose filter claims it — exactly as a refund filed under a
   spending category does, and it is listed among Other Spend's rows for
   somebody to file. A month holding only an unfiled $75 credit has Income
   $0.00 and Other Spend +$75.00. The converse holds too: a negative part
   filed under an income category — a clawed-back paycheck, a reward
   reversed — lowers Income and never reaches Other Spend or an envelope,
   which is where Simplifi's stored figures put it. Open months pick this up on their next
   computation, since every plan read recomputes the chain from the
   transactions; a closed-out month keeps what it closed with (rule 5).

### Envelopes (planned spend)

```
target             = overwritten_target if set else target_amount
spent              = Σ |amount| of txns matching filter_id in this month
rollover_in        = prior month's (target + rollover_in − spent), if rollover enabled
available          = target + rollover_in − spent
pct_used           = spent / (target + rollover_in)
state              = overspent  if available < 0
                   = with_rollover if rollover_in > 0
                   = normal
```

`spent` nets refunds rather than summing magnitudes, and the rollover can
carry negative; both are listed in
[§13](#departures-from-a-stated-rule). The figures are
`domain.EnvelopeStatus` (`Budget`, `Available`, `PctUsed`, `BarPct`,
`State`).

- **Rollover is a stored, editable amount**, not a boolean. Three
  operations: *release unspent funds* (return to free-to-spend now;
  `domain.ReleaseRollover` returns the amount the caller must add back),
  *change rollover amount* (set it directly; `domain.SetRollover`, after
  which the chain stops recomputing it), *auto-release* (release every month
  automatically, so nothing carries). A target override applies to one
  month and does not carry forward; a this-month-only envelope does not roll
  forward at all.
- **The rows behind an envelope are the parts it was charged**, never a
  second query by its filter. The engine has already settled that set —
  after the series and goal claims, the month's exclusions, and the
  creation-order tie-break with any other envelope that matched the same
  part. A split transaction contributes one line per part charged, carrying
  that part's share and its own category, which is what makes the list sum
  to `spent`. A closed month froze rows rather than parts and is listed row
  by row, a split row as its splits.
- A zero target with spend is 100% used (overspent), not a division by
  zero (`domain.FullyUsedPercent`).
- `pct_used` has no upper bound in the label (Simplifi shows 250%) but the
  bar fills at 100%.

### Projected other spending

```
projection_type ∈ {run_rate, prior_month, average_n_months}
run_rate:  other_spend_to_date / days_elapsed × days_in_month
projected_left = left_this_month − (projected_other_spending − other_spend_to_date)
projection_buffer applies as a flat add-on
```

`domain.ProjectedOtherSpending`, `domain.ProjectedLeft`. Store the
projection type and window on the month row — the user can change it, and a
projection whose method is not recorded cannot be explained. `prior_month`
reads the last prior month, and `average_n_months` the mean of the trailing
window; either falls back to spend to date with no prior months. An unknown
type falls back to the run rate and is logged, never silently guessed.

A month that has not started has no run rate: nothing has been spent over
zero days, and extrapolating that says the month will cost nothing. Before
the month's first day `run_rate` reads the prior months instead — the mean of
the same trailing window `average_n_months` uses — and returns to the run
rate on the first. That is what a future month's headline is made of: its
scheduled income and bills, its envelope targets, and other spending
projected from the months before.

The rail leads with the month's own result (the five buckets without the
rollover) — how the month ended, how it stands so far, or what it is
expected to cost — and shows the rollover, and the two added together, one
line down. `left_this_month` is unchanged; it is that second line.

```
month_result           = income + bills + planned_spend + other_spend + goals
month_result_per_day   = month_result / days_remaining_in_month
projected_month_result = month_result − (projected_other_spending − other_spend_to_date)
days_elapsed           = 0 before the month, its day count after it, today's day during it
```

`days_elapsed` (`SpendingPlanMonth.DaysElapsed`) is the run rate's divisor,
sent so the words beside the projection name the same day count the
projection used.

---

## 6. Goals

```
saved_so_far      = Σ contributions − Σ withdrawals   (tracked via txn_ids)
withdrawn         = Σ withdrawals, positive
funded            = saved_so_far + withdrawn
spent_on_goal     = Σ −amount of the rows filed as spending
left_to_save      = max(0, target_amount − saved_so_far)
pct_complete      = saved_so_far / target_amount      (clamp display at 100%)
pct_funded        = funded       / target_amount      (clamp display at 100%)
contributed_this_month = net movement into the goal this month
monthly_needed    = left_to_save / months_until(target_on)
unassigned_withdrawn = max(0, withdrawn − spent_on_goal)
stage             = closed    if the user closed it
                  = spending  if withdrawn > 0 or spent_on_goal ≠ 0
                  = funded    if target_amount > 0 and is_funded
                  = saving    otherwise
```

`domain.SavedSoFar`, `Withdrawn`, `Funded`, `SpentOn`, `LeftToSave`,
`PctComplete`, `PctFunded`, `ContributedThisMonth`, `MonthsUntil`,
`UnassignedWithdrawn`, gathered in `domain.GoalProgress` with its `Stage`.
`saved_so_far` is not clamped at zero, or the account's reserve and the goal's
figure would disagree. Both percentages are undefined for a zero target.
`monthly_needed` is unset without a target date, and once the goal is funded
or closed. Contributions are tracked by transaction id, never inferred from
account movements, or every unrelated deposit would fund the goal.

- **A goal has a life, and the card is drawn by its stage.** It is saved
  toward, reached, drawn on and spent, and finally closed. While *saving*,
  the card is the target, what is saved, `left_to_save`, `monthly_needed` and
  the target date. From *funded* on it is a flow instead: saved (`funded`),
  taken out (`withdrawn`), spent (`spent_on_goal`), what is not yet linked to
  spending (`unassigned_withdrawn`) and what is still in the account
  (`saved_so_far`). Once a goal has been funded, `left_to_save` is not shown
  and nothing is asked of a month: the household met the target and took
  the money to where it is spent, and "left to save" on a goal that did that
  is a contradiction, not a figure. A goal drawn on before it was funded is
  *spending* too, since its money is already on its way out. A zero target
  names no amount to reach, so it is never *funded*. The contribution
  reminder reads the same rule, through `monthly_needed`.
- **`unassigned_withdrawn` is money taken out that no purchase accounts for
  yet.** The usual path is savings → checking → a card, so the withdrawal and
  the purchases are different rows on different accounts and only the user
  can pair them. Spending beyond the withdrawals was paid from elsewhere and
  leaves nothing unassigned, so the figure never goes negative.
- **Finding rows.** `domain.SuggestGoalRows` offers rows of one kind for the
  user to file in bulk; nothing is linked on its own. Contributions are money
  arriving in the reserve account up to the first withdrawal; withdrawals are
  money leaving it after the first contribution, and transfers out of the
  other funding accounts that did not go to the reserve; spending is money
  leaving any other account from 14 days before the first withdrawal to 60
  days after the later of the target date and the last withdrawal, without
  transfers or card payments, ranked by a category and then a payee already
  among the goal's spending and by days from the nearest withdrawal. A row any
  goal counts, and the other leg of a counted transfer, are never offered:
  either would count the same money twice.
- **A closed goal reserves nothing.** Closing stamps `closed_on`; the goal
  drops out of `ReservedInAccount`, so whatever it still holds is the
  account's available balance again. Its rows, figures and breakdown stay
  readable, and reopening clears the date and reserves again. In the spending
  plan it adds nothing to the Goals bucket from the month it closed on
  (`GoalContribution.CountsInGoalsBucket`); earlier months keep what they
  set aside, because the money really was being set aside then. Its rows stay
  the goal's, so a contribution does not fall into Other Spend, and spending
  filed under it still leaves the plan.

- A goal names one `account_id`; its `saved_so_far` inflates that account's
  `goal_balance` and therefore *reduces* the account's available balance.
- **The bar fills to `pct_funded`, not to `pct_complete`.** A household that
  saved the whole target, moved it to the account it would spend from and
  spent it has met that goal; a bar drawn off `saved_so_far` walks back to
  zero as the money goes and ends by saying the saving never happened. The
  bar is two segments over the target: `pct_complete` solid, and the rest of
  `pct_funded` as a quiet tint — money that was set aside and has since been
  taken back out. `is_funded` is whether the target was ever reached and
  `is_complete` whether the money is still there; they differ for exactly
  the goals this rule is about. `funded` is gross rather than a high-water
  mark, because the rows carry no ordering that would make a peak meaningful
  — a contribution backdated into last March arrives after a withdrawal made
  this week. Spending rows lengthen neither figure: they report where the
  withdrawn money went, and counting them would draw the same money twice.
- **Which way a counted row moves the goal is recorded, not read off its
  sign.** `withdrawal_txn_ids` is the subset of `txn_ids` that comes back
  out, and `GoalContribution.Saved()` is `|amount|` for a contribution and
  `−|amount|` for a withdrawal. The sign cannot answer it: money leaving a
  funding account is a contribution when it went to the account the goal
  reserves in and a withdrawal when it paid for the thing the goal was saving
  for, and the two rows are identical. The sign is still decisive on the
  goal's *own* account — money arriving there can only add to the reserve,
  money leaving can only draw it down — which is all the goal's pickers use
  it for. Rows arriving from the Simplifi import are classified by sign,
  which is exactly right for them: Simplifi keeps a goal's money in a GOAL
  account, so the surviving leg of a contribution is negative and of a
  withdrawal positive.
- **A third kind reports rather than moves.** `spending_txn_ids` is what the
  goal's money was spent on, from any account — the card the trip was booked
  on, which the goal's own account never touched. It leaves `saved_so_far`
  alone, because the reserve was already reduced by the withdrawal that
  funded the purchase. `spent_on_goal` keeps the ledger sign negated rather
  than folding to an absolute, so a refund against a purchase brings the
  total back down. The card's flow therefore labels `withdrawn` **Taken
  out**, not Simplifi's "Spent": that figure is money out of the reserve, and
  `spent_on_goal` is money spent on the thing.
- **Money spent on a goal leaves the spending plan.** It was planned, and it
  was planned outside the month's bounds: the household decided on the
  purchase over the months they saved for it, and the month it is finally
  booked in did not budget for it and cannot be judged against it.
  `ComputeMonth` drops those rows the way it drops a row the user excluded by
  hand, so they reach neither Other Spend nor an envelope — nor the Goals
  bucket, which reserves money being set aside *this* month and would
  otherwise reserve the same savings twice on the way out. The rows still
  count in reports. Filing the row under the goal is what says so; there is
  no second flag to set, and unlinking puts it straight back. This is a
  departure from Simplifi, which has no third kind of link and charges the
  month.
- **The breakdown is why those rows keep their ordinary categories.**
  `spending_by_category` is `spent_on_goal` grouped by what each row was
  filed under, largest first, split-aware, with uncategorized rows listed
  rather than dropped so the parts sum to the total. A goal is a second axis
  across the category tree, not a category of its own: the trip is still
  airfare and lodging and meals, and the goal is how they are read together.
- `is_taken_from_plan` decides whether the contribution appears in the
  spending plan's Goals bucket. A goal funded from money already counted as
  spend must not be counted twice. A contribution counts in the month it
  posted.
- Goals can be funded from multiple accounts (the create flow's *Add
  Account* repeats the row). Modelled as a join table, not a single foreign
  key.

---

## 7. Watchlists

```
this_month_spent      = Σ |amount| matching filter_id, current month
month_projection      = this_month_spent / days_elapsed × days_in_month
year_to_date          = Σ matching, Jan 1 → today
monthly_trend[n]      = Σ matching, per month, last n months
twelve_month_average  = Σ trailing 12 full months / 12
```

`domain.SummarizeWatchlist`, over `WatchlistThisMonthSpent`,
`ProjectMonthSpend`, `WatchlistYearToDate`, `WatchlistMonthlyTrend` and
`WatchlistTrailingAverage`. Spend nets refunds, `this_month_spent` and
`year_to_date` both run to the end of the current month, and watchlists
honour `excluded_from_reports`; all three are in
[§13](#13-settled-choices-and-departures).

- **The trailing average uses full months only** — including the current
  partial month drags it down every time and makes the figure meaningless
  early in a month. It divides by the months in the window, not the months
  with spending: a month with no matching rows is a month of zero spend.
  Simplifi labels it "12 month / Monthly average". No golden test covers
  watchlists; `backend/internal/api/watchlists_test.go` pins the full-months
  rule.
- `month_projection` counts today as elapsed, so its divisor is never zero.
- The Overtime bars (`monthly_trend`) end with the current month, flagged
  partial; a request for zero months uses the watchlist's own setting.
- The breakdown donut splits the selected window by category, payee or tag.
  A row with two tags counts in full under each, as Simplifi's per-tag donut
  shows, so the shares can pass 100%.

---

## 8. Cash flow projection

Per account, forward from today (`domain.ProjectBalances`):

```
balance(d) = current_balance + Σ expected occurrences with due_date ≤ d
```

- Occurrences come from the series RRULEs, expanded over the horizon, minus
  any already fulfilled by a posted transaction. A pay-manually reminder
  ([§9](#pay-manually-reminders)) is paid from no named account, so no line
  steps on it.
- **An occurrence's amount** (`domain.OccurrenceAmount`) is, in order: the
  `override_next_amount` when the slot is the series' current due date; the
  amount of the slot's linked bill; the series amount. A slot's bill
  (`domain.SlotBill`) is the open or paid bill whose due date falls inside
  the slot's match window ([§9](#9-recurrence)), the nearest when two do,
  the later on a tie. Bills due on that same day (a provider that invoices
  one billed account twice on one day) all speak about the slot
  (`domain.SlotBills`): the slot costs their sum, and the reminder lists one
  occurrence per bill, each at its own amount and payment date, all on the
  one slot, which a single charge settles. Every slot reads its own bill,
  past-due slots included, and no switch gates it: the provider's figure is
  the cycle's real cost, and the estimate is only what stands in until one
  arrives.
- **Only an open bill moves a slot.** With `auto_adjust_due_on` on, an open
  bill shows its slot on the bill's due date
  (`domain.OccurrenceDueOn`), and its autopay day is the slot's payment date
  (`domain.OccurrencePaysOn`). A paid bill sets its slot's amount and
  nothing else: a closed cycle's due date may be one the reader inferred.
- **A provider reporting a bill paid does not settle the slot.** A past-due
  slot whose bill is paid shows the bill's amount and stays past due, with
  the bill's `status` (`paid`) beside it, until a bank transaction matches
  it. The provider's word is a reason to look for the charge, not a charge.
- **A biller states what is owed, which is a magnitude; an occurrence is
  signed.** The loader that reads a statement signs it against the series it
  belongs to (`service.Bills.SeriesBills`), so an adjusted bill lowers the
  projection rather than raising it by what it is about to take out.
- **An occurrence with a known payment date is stepped on that date**, not
  on its due date: a bill due the 26th that autopays on the 21st lowers the
  projected balance on the 21st, which is the day the bank will show it. The
  golden tests do not cover this — Simplifi's export carries no autopay date
  — so `TestProjectBalancesStepsOnTheAutopayDate` is the whole proof.
- **An occurrence has two dates: the slot it was scheduled on and the day it
  is shown.** A one-off override or an `auto_adjust_due_on` bill moves the
  second and never the first, so the ledger files a charge under
  `scheduled_on` and every "has this been paid, skipped or claimed" lookup
  asks with that — otherwise a bill whose due date moved reads as unpaid
  after the charge posts.
- The opening balance is the ledger's balance at the end of the day before
  the window (`domain.LedgerBalanceAsOf`); see [§3](#historical-balance) and
  [§13](#choices-the-rules-leave-open).
- Render one line per selected account; the account selector is a filter
  over the same set, not a separate query.
- **Low-balance projection** (a notification) is this function crossing a
  threshold, not a separate calculation.

### An account's own estimate

`domain.EstimateAccountCashFlow(domain.AccountCashFlows(postings), today)` —
the "Estimate" under a single account's projection card. The projection
above is arithmetic over known series; this is the account's own rhythm
carried forward, and it is shown beside the line, never drawn on it.

```
months read  = complete calendar months before today's, at most 12, from where
               the account's history begins
money_in     = Σ positive settled amounts in those months / months read
money_out    = Σ |negative settled amounts| in those months / months read
forecast     = six months, starting with today's, each money_in / money_out
```

- **Rows read** (`AccountCashFlows`): settled rows of the account, by posted
  date, native amount. Pending rows, deleted rows and a provider's estimate
  rows are out. **Transfers stay in** — the household forecast drops them
  because they net to nothing across the household, but for one account the
  paycheck swept to savings or the card paid from checking moves its
  balance. The reports and spending-plan exclusion flags do not apply, for
  the same reason as the historical balance.
- **Where the history begins.** A row dated before the twelve-month window
  means the account existed throughout, and all twelve months are read,
  empty ones as zero. Otherwise reading starts at the month of the account's
  first row — and that month is **skipped when the first row is after the
  1st**, because a history that begins mid-month (a bank backfill almost
  always does) holds only part of it. When that partial month is the only
  month before today's, it is read anyway and the narrative says it may not
  be a full month. No row before today's month means no estimate.
- **Rounding:** each average is rounded once, half away from zero.
- The windows, the reconciliation against the projection and the "covers
  the first N days" caveat are the household forecast's
  (`domain.BucketCashFlowForecast`, `domain.ReconcileCashFlowForecast`).
- **When it is made:** every night by the scheduler, once the day's sync
  window (`SYNC_AT`, default 04:00) has opened and after the connections due
  in that pass have synced; on the card's first read for an account with
  none, or whose stored one was made in an earlier month; and on "Re-run
  projection" (`POST /cash-flow-forecast/run?account_id=`). Saving replaces
  the account's previous estimate. The household-wide forecast a model makes
  is untouched and is what `GET /cash-flow-forecast` answers without exactly
  one `account_id`.

---

## 9. Recurrence

Store RFC 5545 RRULE fields: `frequency`, `interval`, `by_month_day[]`,
`by_day[]`, plus the human `alias` for round-tripping the UI label
(`domain.Recurrence`, `domain.RecurrenceAlias`). The rule fields are the
truth; the alias only names them. A negative `by_month_day` counts back from
the end of the month (−1 is the last day).

```
EVERY_WEEK        FREQ=WEEKLY;INTERVAL=1
EVERY_MONTH       FREQ=MONTHLY;INTERVAL=1;BYMONTHDAY=[n]
TWICE_A_MONTH     FREQ=MONTHLY;INTERVAL=1;BYMONTHDAY=[n,m]
EVERY_QUARTER     FREQ=MONTHLY;INTERVAL=3;BYMONTHDAY=[n] (optional)
EVERY_YEAR        FREQ=YEARLY;INTERVAL=1
EVERY_X_DAYS      FREQ=DAILY;INTERVAL=x
MULTIPLE_FIXED    FREQ=MONTHLY;BYMONTHDAY=[...]
ONE_TIME          no recurrence
```

**Month end clamps; it never skips.** A `BYMONTHDAY=[31]` series in February
lands on the 28th or 29th, never skipped and never rolled into March.

**Active months make a series seasonal.** `by_month` (RRULE `BYMONTH`, as it
limits a daily, weekly or monthly rule) names the months of the year the rule
is active in; empty is every month (`domain.Recurrence.ByMonth`, kept sorted
and distinct by `domain.ActiveMonths`, and all twelve stored as none). It
filters and never shifts: an occurrence that falls in an off month is
dropped, not shifted into the season, and an every-14-days rule keeps its phase
across the gap. Every reader expands through `domain.ExpandOccurrences`, so
the reminders and upcoming bills, the matcher's slots (an off month has no
slot, so nothing in it reads as missing or past due), `annualized_amount`,
the cash-flow projection and the spending plan's Bills all skip the off
season: lawn care monthly on the 12th from April to October is seven
occurrences a year, and the one after 12 October is 12 April. The match
window stays the in-season one (`domain.PeriodDays` is the period between
occurrences inside the season). A yearly or one-time rule takes no active
months, since its anchor already names its month. A start date in an off
month moves to the rule's first occurrence (`domain.ActiveStart`), and a rule
that never lands in an active month (quarterly from January, active only in
February) is refused.

`annualized_amount = amount × occurrences_per_year`
(`domain.AnnualizedAmount`, `domain.OccurrencesPerYear`), where occurrences
per year comes from expanding the rule over a real calendar year, not from a
lookup table — every-14-days is 26 or 27 depending on the year, and "extra
paycheck month" is a shipped notification that depends on getting this
right.

**A series' kind, never its sign, says what its occurrences are.** The Bills
& Income overview totals expected occurrences with
`domain.SummarizeOccurrences`, the way §5's buckets read them: income series
are Income, a transfer or card payment adds nothing, and every other kind
counts against Expenses with its sign, so an expected refund nets against
spending rather than passing for earnings. The alerts read the same rule —
the bills-to-income share takes this month's income-series occurrences and
the bills due soon from the kinds §5 lists under Bills
(`domain.SeriesKind.IsBill`), and "extra paycheck month" counts only income
series.

### Matching a posted transaction to a series

Candidate must be (`domain.IsCandidate`): not deleted, same account, same
direction, same currency, dated inside the occurrence window
(`domain.MatchWindow`: 3 days early to 5 late for monthly, tighter for
shorter periods; see [§13](#choices-the-rules-leave-open)), within the
amount tolerance, and similar enough in wording.

- **Amount tolerance is a band**: `exact | any | range(lo, hi) | auto`
  (`domain.AmountTolerance`). Simplifi's four Match Criteria map directly.
  An unset tolerance reads as `exact`. `auto` widens from observed history.
- **The band is centred on what the slot shows, not on the estimate.** A
  charge is weighed against `domain.MatchContext.Expected`, which is
  `domain.OccurrenceAmount` for the scheduled slot it would fill
  ([§8](#8-cash-flow-projection)): the pointer slot's override, else the
  slot's linked bill (open or paid), else the series amount. The direction
  check, the band and the amount closeness all read it, so the matcher and
  the reminder cannot disagree about what a slot is worth. A power bill
  estimated at 300.00 whose provider billed July at 85.00 takes a July
  charge of 85.00, which the estimate's band would refuse, and that charge
  settles the past-due July slot.
- **Wording similarity is learned.** Compare against the series description
  *and* the descriptions of charges already linked to it, reading the
  charge's `statement_name` and its payee (`domain.ChargeTexts`).
  Similarity (`domain.TokenSimilarity`) is the share of distinct
  whitespace-separated words the two share, over the word count of the
  longer; a candidate needs at least `domain.SimilarityThreshold` (0.6).
  "Paycheck" shares nothing with "ACME CORP DES:PAYROLL"; the first link is
  manual, after which the bank's phrasing is on file. Candidates that clear
  every gate are ranked by wording, amount and date closeness, weighted
  0.5 / 0.3 / 0.2.
- **Name and matching text are separate fields.** `description` is what gets
  compared; `display_name` is what a person reads. Renaming a rule to read
  nicely must not stop it matching. Every display path reads `label`
  (= `display_name or description`); every matching path reads
  `description`.
- A charge that finds a materialized placeholder upgrades it in place and
  advances the schedule pointer. A charge that finds only the series is
  stamped and advances it. Back-filling an old occurrence leaves the pointer
  alone.
- **Rows are offered when they arrive, and again when a bill arrives.** A
  sync or import offers its new rows to every series of their accounts. A
  bill filed or amended, or a series linked to a billed account, offers that
  series alone the account's unlinked rows dated from the earliest of those
  bills' reach to the latest's, oldest first, through the same decision
  (`service.SeriesMatcher.MatchHistory`). A bill's reach
  (`domain.BillPaymentDates`) is the two match windows added, since the slot
  lies within the window of the due date and the charge within the window
  of the slot: 8 days either side of the due date for a monthly series. See
  [Bill providers](connectors/bills.md#pulling).
- **Match history counts bills against held slots.** On request the same
  pass runs over every open and paid bill of a billed account, and
  `domain.TallyBillHistory` counts each bill once: *settled* when a bank row
  that is neither a forecast nor deleted holds a slot the bill claims
  (`domain.BillClaimsSlot`),
  *with statement* when it is settled and its statement is on file,
  *unsettled* when it is due on or before today and settled by no row, and
  in neither when it is due later. A bill due 22 March claims a monthly
  slot on the 20th (2 days after, inside 3 before / 5 after); a slot held on
  24 May claims nothing of a bill due 2 June (9 days).
- **A bill cadence that does not fit the reminder is flagged.**
  `domain.BillDueGap` is the median of the gaps between consecutive distinct
  due dates (two or more needed; of an even count, the upper middle).
  `domain.BillCadenceDisagrees` is true when that gap is under half or over
  one and a half times `PeriodDays` of the reminder: 364 days against a
  monthly reminder (30.44) disagrees, 28 or 31 does not.

### A reminder suggested from a billed account's bills

A billed account with bills and no linked series is offered the series its
statements describe (`domain.SuggestBillReminder`). It is a suggestion:
nothing is written until the person saves it in the series editor, which
creates the series and links the billed account.

- **One due date is one occurrence.** Superseded bills are left out, and the
  invoices due on one day are added together: two visits invoiced together
  are paid together. Fewer than three due dates suggest nothing.
- **The rhythm is the gaps' cadence** (`domain.ClassifyCadence`, the same
  test the Suggested tab uses). Monthly and quarterly take the median day of
  the month the bills fall due on; weekly, fortnightly and yearly anchor on
  the last due date.
- **A season is the widest gap, when it is one.** If the gaps fit no
  cadence and the widest is longer than `domain.SeasonGapDays` (62), spans
  two or more whole months, and no bill in any year falls in one of those
  months, those months are the off season and every other month is active
  (§9's `by_month`). The rhythm is then read from the gaps of 62 days or
  less. Bills from April to October with a winter gap suggest monthly,
  April to October, even when visits six weeks apart leave June without a
  bill in some year. A gap of one whole month is a late bill, not a season.
- **About monthly is a guess, and says so.** Gaps whose median is between
  20 and 62 days that fit no cadence suggest monthly on the median day, not
  `confident`; anything else suggests nothing.
- **The amount** is the median of the due dates' totals, as money out, with
  `exact` matching when every total is the same and `auto` otherwise
  (`domain.SuggestTolerance`).
- **The start** is the slot the earliest open bill claims
  (`domain.BillClaimsSlot`), so the bill already waiting shows on the new
  reminder; with none open, the next slot after the last bill that is not
  yet past (`domain.NextStart`).
- **The bank rows that paid the bills** name the account, the matching text
  and the category. For each due date, the nearest row that took out the
  day's total or one of its invoices within the bill's reach
  (`domain.BillPaymentDates`) is that date's payment, each row once. The
  account most of them left is the series' account, and the latest of its
  rows gives the statement name to match and the category. With none found
  the account is left for the person to choose and the matching text is the
  provider's name. When the rows already belong to a live series, the
  suggestion names it, since linking that series beats a second one.

### Pay-manually reminders

A billed account whose connection has no autopay is paid by hand, so its
newest unpaid statement is a one-off reminder until it is paid
(`domain.PayManually`, loaded by `service.Bills.PayManually`). It is an
occurrence like a series' slot, listed by `domain.ExpectedOccurrences`, so
Upcoming's list and month calendar, the dashboard's Bills panel, the
register's Reminders strip, the bills-due notification and the assistant's
upcoming bills all read it from the one expansion. It has no series and no
account: its statement is its `bill`, its provider its `bill_link`.

```
reminder = each statement of the newest day on a shown billed account
           (by issue date, else due date; a superseded one is never newest)
           where the connection's autopay rule is none
             and no recurring reminder is linked to the account
             and status = open, amount_due > 0, no stated autopay day
             and no person marked it paid
             and no paired payment settled it (domain.StatementsPaidBy)
due_on   = the statement's due date (its issue date when none is printed)
amount   = −amount_due
```

- **Only the newest.** A provider that carries an unpaid balance forward
  restates it on every statement, so the older ones are not separate debts.
  A newer statement ends the reminder and, if it owes something, is the
  next one.
- **A paired payment settles what it paid**: a payment the provider lists
  that a bank row was paired with settles the statements issued on the
  latest day on or before it, the rule that files the receipt
  ([bills](connectors/bills.md#statements-filed-as-receipts)). A payment no
  bank row carries yet settles nothing, and any payment, of any size,
  settles the statement it follows; what is still owed comes back on the
  next statement.
- **Marked paid** (`POST /bills/statements/{id}/paid`) is a person's word
  that it was paid where the ledger cannot see. A pull never clears it.
- **A provider's own word is not overridden**: a statement the provider
  reports paid, or that states the day autopay takes it, is no reminder.
- **A linked billed account keeps its series.** The series' occurrences
  are its reminders, so no one-off one is made beside them; with the
  connection's autopay off they say to pay it by hand (`bill_link.autopay`
  false).
- **Where it counts, and where it does not.** It is money that will leave,
  so it counts once among Upcoming's Expenses and Net
  (`domain.SummarizeOccurrences`), in the bills due this week and the
  bills-to-income share, in what the assistant and the cash-flow forecast
  automation are told is due. It is paid from no account anyone named, so it
  steps no account's line on the cash-flow chart and is not in the
  spending report's projection, both of which read occurrences per account;
  an account filter on the listing leaves it out. Once paid, the bank row
  counts where any spending counts. It is no series, so the spending plan's
  Bills bucket does not hold it.

---

## 10. Investments

```
market_value    = Σ shares × latest_price
cost_basis      = Σ shares × average_cost      — null if any lot is unknown
total_gain      = market_value − cost_basis    — flag "incomplete" if any null
day_change      = Σ shares × (price − prior_close)
day_change_pct  = day_change / (market_value − day_change)
```

- **Incomplete cost basis is a first-class state**, not zero
  (`IsCostBasisIncomplete`). Simplifi badges the total gain "Incomplete" and
  shows a dash per affected holding. Rendering a gain of `market_value − 0`
  is a lie.
- **TWR** (time-weighted, `domain.TimeWeightedReturn`) removes the effect of
  contributions: chain-link sub-period returns at each external cash flow.
- **IRR** (money-weighted, `domain.InternalRateOfReturn`) solves for the rate
  where NPV of all flows is zero. Newton with a bisection fallback; return
  null rather than a wrong root when it does not converge.
- Both are offered as a toggle on the same chart, served together by
  `GET /performance`. Do not pick one.

**What a row on an investment account *was*** is derived, not stored. There
is no investment-transaction table: a purchase, a dividend and a 401(k)
contribution are ordinary rows, and `domain.ClassifyActivity` names them for
the Transactions tab's Action column. The order of trust is the whole rule —
the bank's own wording first, the transfer pair second, and the category's
*kind* last, never its name, because a brokerage's dividends can end up
filed under a category like Fast Food. A row nothing recognizes is `unknown`
and the screen prints no action: money out of a brokerage is a withdrawal, a
purchase, a fee or a wire, and naming one of the four beside a real amount
invents a fact.

`domain.InvestmentIncome` counts dividends, interest *and* reinvested
distributions — the reinvested ones are earnings that never left the
account, and are deliberately not cash flows for the return calculation
above. `domain.InvestmentFees` reports its total positive, because a cost is
read as a magnitude.

**Allocation** is `domain.AllocationBy` over one set of valuations, grouped
by security, asset class or account, so the three views of the card cannot
disagree with each other or with the table under them.

---

## 11. Reports

One query engine, two renderings:

- **Transaction mode** — rows grouped by the chosen hierarchy (category →
  subcategory → transaction), subtotal at every level, grand total.
- **Summary mode** — a pivot: row dimension × column dimension, cells are
  sums, a Total column and a Total row. Column dimension is Time (Day /
  Week / Month / Quarter / Year), Account, Tag or Payee.

Both take the same `Filter` and date range, and file rows by
`effective_date` ([§2](#reporting_datetxn-mode---date)). The engine's report
types are presets over these two knobs plus a sign convention, not one
implementation each; Spending, Net Worth, Savings and the Monthly Summary
read their own endpoints.

- **Income & Expense** renders income positive, expenses negative, net as a
  line. The net line is `income + expenses` per period, not a separate
  query. Which column a row lands in is `ledger_kind`
  ([§2](#ledger_kindamount-category---income--expense--transfer)). The
  totals row — income, expenses, net and savings rate (net ÷ income, none
  when nothing came in) — is `domain.SummarizeAllocations`.
- **Taxes** groups by the category's TXF code — form, then line item, then
  payee. No extra data required.
- **Monthly Summary** is a narrative snapshot: totals with month-over-month
  percentage deltas, and Top Categories / Top Payees **excluding bills and
  subscriptions**, each with an occurrence count. That exclusion is the
  point of it — it answers "where did the discretionary money go". Its
  Bills and Discretionary figures split the month's expenses the same way,
  in the same `domain.SummarizeAllocations`, so the two always sum to its
  Expenses.

Percentage deltas against a zero prior period are undefined; render "—",
never "∞" or "0%".

### Spending report

One calendar month, quarter or year of spending beside a comparison, by
category, payee or tag (`GET /reports/spending`,
`backend/internal/domain/spendinganalysis.go`). Every figure is
`domain.Aggregate` — the register's Spending and Income tabs — over the rows
whose effective date falls in a window (`domain.SpendingIn`), taken with the
register's own account selection, filter and category drill. So the report,
the register's Spending tab and the engine's expense total give one number
for one window and one scope: the same two exclusion flags at every level,
transfers out, splits per split, refunds netted by `ledger_kind`.

```
window(period)        = period start → period end, or → today for the period today is in
income                = Σ income-kind allocations in the window
spent                 = Σ spending-kind allocations in the window (negative)
remaining             = income + spent          (negative is "Overspent")
savings_rate          = remaining / income      none unless income > 0
spending_rate         = −spent / income         none unless income > 0
difference            = −amount − (−comparison) (positive: more spent)
difference_pct        = difference / |comparison| × 100, none when the comparison is 0
share (donut)         = −amount / Σ −amount over the lines that net to spend
flow share            = band / income
```

- **The period in progress runs through today**, not to its end: a card
  charge whose statement is due later this month is counted when that day
  comes. **The same-days rule:** a comparison against a period cut short is
  cut at the same point into its own period — as many whole months in and
  the same day of the month, clamped to that month's last day
  (`SpendingWindow.SameDaysInto`). October 1–3 is compared with September
  1–3; January 1 – October 3 with January 1 – October 3 of the year before; a
  quarter on May 31 with January 1 – February 28. A whole period is compared
  with whole periods.
- **The Compare menu** (`domain.SpendingComparisonsFor`): a month offers the
  same month last year, the prior month, the year-to-date average and the 3,
  6 and 12-month averages; a quarter the same quarter last year, the prior
  quarter, the year-to-date average and the 2 and 4-quarter averages; a year
  the prior year and the 3-year average. Each offers no comparison. The
  default is the prior period.
- **Averages** (`domain.ComparisonPeriods`, `domain.AverageSpending`): an
  N-period average is the N periods before the selected one; a year-to-date
  average is the periods of the selected one's year before it, so a January
  or a first quarter has nothing to compare with. Each period is cut by the
  same-days rule, every line is summed and divided by the number of periods
  — a period with nothing on that line is a zero, as in the watchlist
  average ([§7](#7-watchlists)) — and rounded once.
- **A difference is spend against spend.** A line netting to a credit is
  negative spend, so $50 back against $20 spent is $70 less, −350%. Nothing
  to compare with is **New spend** (no percentage); nothing this period
  against something is **No spend yet**; both empty is 0.0%.
  `domain.CompareSpending`.
- **Shares** of the donut are over the lines that net to spend, so a line
  netting to a credit has none, and a row with two tags still leaves the tag
  shares summing to 100%. The flow's shares are all of income, the bands
  being income spent: a credit line flows into Total Spent beside income, and
  an income line netting negative draws no band. Where every income line
  rolls up to one income category, the flow lists that category's children
  (`domain.IncomeFlowUnder`).
- **Savings rating** (`domain.RateSavings`): none at or below 0% or with no
  rate, low under 15%, good from 15%, great from 30%. For the period in
  progress it rates the projection below, since a paycheck due on the 30th
  belongs in the month's rate on the 3rd; a whole period rates what happened.
- **The period in progress is projected to its end** (`domain.ProjectSpending`)
  by the recurring occurrences still expected in it — the Reminders strip's
  own list, so the occurrences no transaction has settled
  ([§9](#9-recurrence)):

  ```
  expected        = reminders whose money moves (pays-on date, else due date)
                    from today through the period's last day
  expected_income = Σ income-series occurrences in expected
  expected_spent  = Σ every other kind's occurrences in expected, signed
  projection      = the cards over (income + expected_income, spent + expected_spent)
  ```

  An occurrence already paid is settled and counted once, by its posting; a
  past-due one is not the rest of the period and is left out; one due today
  and not yet settled is still to come. A transfer or card payment counts
  nowhere, as in §5's Bills. An occurrence counts only where its posting
  would: on an account the report reads that is not excluded from reports,
  under a series category that counts as income or expense, in the
  household's currency; a pay-manually reminder, on no account, counts
  nowhere here ([§9](#pay-manually-reminders)). A stored filter, a search or a category drill narrows
  the report to rows a schedule cannot be matched against, so none of them is
  projected, and a whole period has nothing left to expect. The cards show
  the actual figure with the projection beneath it ("Expected by Oct 31").
- **The chart** is twelve months, five quarters or five years ending with
  the period today is in (`domain.ChartPeriods`), each bar the period's net
  spend (−spent) from a zero baseline: a period whose credits outweigh its
  spending — a tax refund — is a bar below zero, in the income colour, and
  its tooltip and the spent card call it a net credit. The comparison bars
  draw a line netting to a credit by its size, in the same colour;
  **the table** has the same
  columns, except that years reach back to the first year with a counted row
  (`domain.TablePeriods`), and sets its last column against the column
  before it under the same-days rule (`domain.SpendingTable`).
- **"N transactions need to be categorized"** counts the rows in the window
  that would count and still need a category (`domain.CountUncategorized`,
  `Transaction.IsUncategorized`).

None of this is golden-tested: the export carries none of the Spending
page's figures. `api/spendingreport_test.go` pins that the report's total and
lines equal the register's Spending tab and the engine's expense total over
the same window, and that a bill paid before its due date is not projected
again.

### Date ranges

One vocabulary, one window per word, wherever it is offered — register,
reports and the space's default range alike:

- **Month / Quarter / Year to date** run from the first day of the current
  calendar period through today.
- **Last month / quarter / year** are the previous calendar period, whole.
- **Recent N months** (3, 6, 12) is rolling: the same day N months ago
  through today, clamped to the shorter month's last day (May 31 → Feb 28).
  It is not calendar-aligned. Simplifi offers these beside the calendar
  "Last …" presets under a different word, and its date grammar is relative
  (`date=-30d`), so "recent" is read as a relative window.
- **The space's default range** (`1M`, `3M`, `6M`, `1Y`, `5Y`, `QTD`,
  `YTD`, `ALL`), which is also the charts' chips: the durations are rolling
  windows ending today, since the calendar ones have their own names. `1Y` is
  the last twelve months, `QTD` and `YTD` are quarter and year to date, and
  `ALL` is unbounded but still ends today.

`frontend/src/lib/dateRanges.ts` is the only resolver. The register, the
reports and the charts store a range as its token and resolve it there, so a
report and the register filtered to the same words sum the same rows, and one
default opens every screen on the same window.

---

## 12. What is materialized and what is derived

Simplifi stores aggregates rather than deriving them: transaction running
balances, account value changes, watchlist trends, spending-plan bucket
totals. That buys instant reads at the cost of an invalidation story. The
choice here is made per aggregate:

| Aggregate | Stored or derived |
| --- | --- |
| spending-plan month rows | **Materialized.** Required for closed-out months and user overrides to mean anything. |
| transaction running balance | **Materialized.** Recomputed per account on every change ([§3](#running-balance-in-the-register)). |
| account balance history | **Materialized** daily by the scheduler; the net-worth chart is unusable without it. |
| watchlist trends / projections | **Derived.** Cheap, and staleness is visible. |
| envelope spent | **Derived** within an open month, frozen on close-out. |
| net worth at a date | **Derived** from materialized balance history. |
| investment TWR/IRR | **Derived** per request by `GET /performance`. It is the most expensive derivation here; materializing it nightly is the remedy if requests become slow. See [§13](#departures-from-a-stated-rule). |
| an account's own cash-flow estimate ([§8](#an-accounts-own-estimate)) | **Materialized** nightly after the sync, replacing the last; re-made on demand. |
| an account's historical balance ([§3](#historical-balance)) | **Derived** on read from the ledger; it must end on the balance the projection starts from. |

Anything materialized needs: a recompute function, a trigger list (what
invalidates it), and a periodic full rebuild that must produce identical
output. The rebuild-equals-incremental test is what keeps materialization
honest.

### What a `balance_snapshots` row holds

The net-worth chart reads this table rather than deriving anything, so what
one row means is fixed here. It is written by
`service.Scheduler.snapshotBalances`, through `store.SnapshotAccounts` and
`store.RebuildBalanceHistory`, both over `domain.BalanceAsOf`:

- **A row dated `D` holds `domain.BalanceAsOf(account, postings, D)`** — the
  balance at the end of `D` by posted date, the same function every
  read-time "balance at a date" question uses. For a manual account that is
  the opening balance plus every settled posting dated on or before `D`; a
  transaction dated next week is not inside today's row. For a connected
  account it walks back from the provider's stored figure, subtracting
  everything dated after `D`.
- **Today's row is written at the first scheduler tick of the day**, in the
  server's own timezone — normally within `SYNC_CHECK_MINUTES` (default 5)
  of local midnight. The provider figure it walks back from is as fresh as
  the last sync, so on a `SYNC_AT=04:00` install the row written at 00:02 on
  `D` carries the bank's `D−1` position until the next pass rewrites it.
- **Every daily pass also rebuilds the trailing week** (`D−7` through `D−1`)
  from the ledger. That is what revisits a row after a pending charge
  settles, after a sync backfills a row the bank dated last week, and after
  the provider figure for a connected account has moved; it also fills the
  rows a day the process was down never wrote. The rebuild does not reach
  further back; the re-walk below is what keeps an older row current.
- **A derived row records the figure it was walked from** (`anchor_on`,
  `anchor_balance`; `domain.AnchorFor`): for a connected account, the
  provider figure and the day of the newest row it contains (the later of
  that and the row's own day). A manual account's row records none; its
  ledger alone is its balance.
- **Every reader re-walks every derived row over the ledger as it stands**
  (`domain.RederiveHistory`, in `api.loadBalanceHistory` for the net-worth,
  savings and investment charts). A manual account's row is `BalanceAsOf`
  for its day. A connected account's row at `D` walks back over the rows
  between `D` and the nearest anchor after it — the next imported
  observation of the account, or the row's own anchor, whichever is earlier:
  `anchor − Σ counted rows dated after D and on or before the anchor's day`.
  So an edit, delete or import under a row from March moves March on the
  next read, and with the ledger unchanged the walk reproduces the figure
  written (the rebuild-equals-incremental rule). A row with no recorded
  anchor and no observation after it stands as written. The daily pass ends
  with `store.RederiveBalanceHistory`, which writes the same figures back to
  the table so the stored column agrees with the chart.
- **A reading the feed dropped is not read** (`domain.DroppedReadings`). A
  run of imported zeros is dropped when the readings either side of it agree
  within a tenth (`domain.DroppedReadingTolerance`) of a balance of at least
  `domain.SuspectResetThreshold` — a mortgage reading 0.00 on a month-end and
  its old balance the next day. It is the sync's
  `domain.SuspectBalanceReset` guard seen from both sides: an imported
  history has the next reading, so it does not need the guard's run-length
  rule. A zero with no reading after it, a zero where the balance came back
  at a different figure (a card paid off and used again) and every zero on an
  account with `accept_zero_balance` are kept. A dropped row stays in the
  table; the day reads the observation before it, as a day with no row does,
  and derived rows before it walk from the reading after it.
- The day gate is in-memory, so a restart re-runs the pass; both writes are
  upserts of the same definition, so the re-run is harmless.
- **No row predates the account's history start ([§4](#4-net-worth)).**
  Neither write makes one, and the pass ends with
  `store.ReconcileHistoryStarts`: every account whose start differs from
  `accounts.history_rebuilt_from` (the start its rows were last built for)
  has its derived rows before the start deleted and every day from the start
  through today that has no row filled from `domain.BalanceHistory`. That is
  the trigger list for the start — older rows imported, a first row, a
  deleted earliest row, an override — and none of them has to remember to
  rebuild. Changing the override or `opening_balance_on` through the account
  API, and importing an asset's value history, rebuild that account at once.
  It only adds rows for days that have none, anchored like any other derived
  row; the re-walk above sets their figures. Readers drop any stored row
  dated before the start (`domain.HistoryWithin`), so the chart is right
  before the pass runs.
- **An imported row is an observation, not a derivation**
  (`balance_snapshots.is_imported`). Nothing rewrites one: the rebuild never
  deletes one, even before an override that starts the history later;
  readers ignore it there, and moving the override back brings it back. The
  earliest one is evidence of the start. Between two observations, a derived
  gap-fill row walks back from the later one rather than from today's
  provider figure, so the line does not saw between the export's readings
  and a walk across years of a ledger that does not quite reconcile with
  them.

Put together: **a row dated `D` converges on the close of `D` by posted
date** within a week, and today's row is the best current estimate of it.
That is still not identical to Simplifi's own end-of-day balance, which is
whatever its aggregator reported that day; a reconciliation should compare
against rows at least a week old and expect the last week to still be
settling.

---

## 13. Settled choices and departures

Places where the implementation knowingly departs from a rule stated above,
and places where the rules above leave a choice open. Each entry is the
current behaviour, and says whether the golden test pins it; an entry the
golden test does not reach is pinned by the unit and API tests named in the
code that implements it.

### Departures from a stated rule

| Rule as written | What is implemented | Why |
| --- | --- | --- |
| Envelope `spent = Σ \|amount\|` (§5) | Sums signed amounts and flips the sign once (`domain.EnvelopeSpent`) | A refund inside an envelope has to give the money back. Summing magnitudes makes a returned purchase consume the envelope twice. Simplifi's own envelope nets a charge and its credit the same way. |
| `projected_left = left − (projected − to_date)` (§5) | Same, with both terms as positive magnitudes, and the projection floored at spend to date before the buffer is added | The formula only balances if the two terms share a sign. Flooring stops `prior_month` on a heavy month reporting more left than the user has. |
| Envelope rollover (§5) | Carries negative when the envelope overspent (`domain.CarriedRollover`) | The formula has no `max(0, …)`, so an overrun starts the next month in the hole. Not golden-tested: the export stores no rollover figure. |
| Watchlist `this_month_spent = Σ \|amount\|` (§7) | Nets the signed amounts | Same reasoning as the envelope: taking magnitudes makes a returned $100 purchase read as $200 of spending. |
| Watchlist `year_to_date` runs "Jan 1 → today" (§7) | `this_month_spent` and `WatchlistYearToDate` both run to the end of the current month | A row dated later this month is already committed, and a year-to-date figure that shrinks intra-month reads as data loss. |
| Cost basis is null "if any lot is unknown" (§10) | Also null when the lots cover fewer shares than the position holds | A partial lot set yields a number that silently belongs to a smaller position — the same lie the missing-price case produces. |
| `day_change` (§10) | Flagged incomplete (`IsDayChangeIncomplete`) when any holding lacks a prior close | `day_change` can tell the same lie as a missing cost basis. |
| TWR/IRR are expensive (§12) | Derived per request by `GET /performance` (`domain.TimeWeightedReturn`, `domain.InternalRateOfReturn`) | No nightly job exists. The figures are the same either way; only the speed differs. |

### Choices the rules leave open

- **"Recent N months" is rolling, not calendar-aligned**
  ([§11](#date-ranges)), in reports and the register alike, so the same
  label sums the same window everywhere. Not golden-tested.
- **An envelope counts the splits it matches, not the whole row**
  ([§5](#5-spending-plan) rule 3). `domain.EnvelopeMatcher` answers per part
  and `domain.EnvelopeSpent` sums the parts assigned to it, exactly as a
  watchlist over the same filter counts. A per-row claim charges an envelope
  a share belonging to no envelope, or lets one envelope carry another's
  spending (the worked example under rule 3). The `other_spend_by_category`
  chart is built from the same parts, so it sums to its bucket; a closed
  month is walked from the ids it froze with, because a chart under a frozen
  total should keep saying what that total meant, and a split row among them
  is its splits under their own categories, never one Uncategorized part.
  Whether Simplifi charges a
  whole row or the matching splits is pinned only through the Other Spend
  bucket the golden test compares.
- **The register's filter total counts the splits it matches, too.** Under a
  filter, a split row the filter kept only part of is still one row, but it
  is worth the parts it kept (`domain.PartialParts`, summed by
  `domain.MatchedAmount`), so the result chip's total agrees with a report
  or an envelope over the same filter. The register's Spending and Income
  tabs take the same parts (`AggregateOptions.Partial`), so the tab and the
  chip give one number for one filter. The page also carries the same rows
  at their whole amounts (`full_total`) and how many rows differ between the
  two (`partial_count`); with none, the chip shows one figure. A row kept
  whole contributes its own amount rather than the sum of its splits, so a
  filter that splits nothing leaves the total exactly the sum of the rows.
- **The Spending and Income tabs' category lines are one level of the tree.**
  `domain.CategoryDrillLevel`: at the top a category of any depth files under
  the top-level category it descends from; drilled into a category
  (`under=`), under the child of it that it descends from, and a row filed on
  the drilled category itself is that category's own line. A grandchild never
  sits beside its parent, so the lines at each level sum to the line they
  were drilled from.
- **`planned_spend` reserves targets and ignores envelope rollover.** It is
  `−Σ effective targets`. Carried funds appear in the envelope's own
  `available`; counting them in the plan's reservation too would reserve the
  same money twice. Simplifi's stored figure follows a different rule on
  every month of a Simplifi export: per envelope,
  `max(target, spent − max(rollover, 0)) + min(rollover, 0)`, so an envelope
  that overran what it had reserves what it actually cost. The two agree
  wherever no envelope overran; the golden test compares those months and
  names the others, each on the evidence that its stored figure is
  Simplifi's formula and not the sum of targets.
- **A fulfilled occurrence stays fulfilled when the user excludes the posted
  row for that month.** The bucket contributes zero rather than reverting to
  the series' expected amount — excluding a posted bill reads as "do not
  count this bill", not "count what I predicted instead". A link claims its
  slot only when the plan counts the transaction at all: a forecast row or a
  payment on an ignored account does not cancel the bill.
- **The series match window is derived from the rule's average period**
  (`domain.PeriodDays`), not from the frequency name: ≤2 days → (0, 0),
  ≤9 → (2, 2), ≤16 → (3, 4), otherwise (3, 5). This gives
  [§9](#9-recurrence)'s 3-early/5-late for monthly and tighter windows for
  weekly, and gives twice-a-month a window narrow enough that a charge
  cannot be claimed by the neighbouring occurrence.
- **The `auto` amount band** spans the minimum and maximum of observed
  history including the slot's expected amount, padded by 10% of it on each
  side; with no history it is the expected amount ±10%. Simplifi does not
  publish its widening.
- **`override_next_amount` / `override_next_due_on` apply only to the
  occurrence at the series' current due date**, and beat that slot's linked
  bill when both are set. Applying an override to every future occurrence
  would let one large power bill project the user broke by March.
- **Back-fill is decided on the occurrence date, not the charge date**
  (`occurrence_on >= series.due_on`, `domain.FulfillsPointer`), so a charge
  posting three days early still advances the pointer.
- **`monthly_needed` counts the current month** toward
  `months_until(target_on)`, because the user can still contribute to it. A
  target in the past or in this month leaves exactly one month, never zero;
  `domain.TargetHasPassed` tells "one month left" from "overdue".
- **Amount filters compare magnitudes**, matching a UI that shows expenses
  as positive figures.
- **Watchlists respect `excluded_from_reports`**, at every level including a
  split's category, per the detail modal's promise that the Reports checkbox
  affects watchlists — and so does the watchlist alert.
- **Series matching requires the same account.** A bill paid from an
  unexpected account will not auto-link. The gate is deliberate.
- **A series-linked transaction counts in the month it posted, and fills its
  slot wherever the slot lives.** A rent due January 31 whose payment posts
  February 2 marks January's occurrence as paid (January stops counting
  the expected amount) and counts at the posted amount in February's Bills;
  February does not also count it as Other Spend. This is
  [§5](#5-spending-plan) rule 1, and the golden test pins it on the posted
  Income and Bills figures.
- **A refund outside an envelope nets against Other Spend, not Income.** A
  positive carrying an expense category is a reversal of spending, mirroring
  how an envelope refund nets against its envelope; only income-category
  positives count as Income. Counting it as income inflates both buckets by
  the refund while "left this month" stays right by coincidence. This is
  `ledger_kind` ([§2](#2-what-counts)), and reports and the register's tabs
  read it too. Golden-tested through Income and Other Spend.
- **An uncategorized positive is spending, not income.** Counting an unfiled
  credit by its sign counts every uncategorized refund as income, and a
  household that gets refunds sees its plan's income inflated by its own
  money coming back. An unfiled credit nets against Other Spend under
  Uncategorized — not in whatever category lands nearest, so a genuine cash
  deposit understates that month's spending until it is filed as income, and
  the review queue is what surfaces it. The category check proposes the
  purchase's category for a merchant's credit (`refund_category_suggestion`,
  [§2](#2-what-counts)), and a refund link settles it by hand.
- **A linked refund refiles the posting, not the row.** The register keeps
  showing what the bank supplied; only the calculations move. A link to an
  *uncategorized* charge therefore refiles nothing — there is no category to
  move the money to — and a credit linked to charges that disagree on a
  category keeps its own.
- **Per-account balances are computed from native amounts, never converted
  ones** ([§3](#3-account-balances)). An account's opening balance, provider
  balance, goal reserves and pending holds are stored in the account's own
  currency, so every figure that combines with them sums native amounts;
  `Transaction.PrimaryAmount` is for cross-account aggregates (net worth,
  reports, watchlists). Otherwise a EUR account in a USD household mixes
  euros into dollars on every register row.
- **The retirement drawdown's conventions are chosen, not recovered.**
  Simplifi draws High / Expected / Low estimates to a life expectancy
  without publishing its arithmetic. Here (`backend/internal/domain/retirement.go`):
  the return is nominal and compounds monthly at one twelfth of the annual
  figure, times (1 − tax rate of the phase); contributions land at the end
  of each month, after that month's growth; the drawdown draws (living
  expenses − retirement income) / 12 each month, both stated in today's
  dollars and inflated to the year drawn; a surplus of retirement income
  draws nothing and is not reinvested; the band walks the same arithmetic at
  return ± 2 points (the API's `return_spread`, overridable up to 25
  points). The expected line going ≤ 0 during a positive draw is reported as
  "runs out at age N", never floored away. The running balance keeps
  sub-cent precision and is rounded only for each reported yearly figure.
- **A connected account's running balance anchors at the provider figure
  minus every counted row including pending ones.** If the bank's figure
  excludes pending authorizations (typical), each settled row's running
  balance is shifted by the pending total until they settle — and then it is
  exact. Anchoring on settled rows only would instead leave the final row
  disagreeing with the balance header, which is the number users check
  first.
- **A connected account's historical balance ([§3](#historical-balance))
  takes pending rows off with the rest**, because it is `BalanceAsOf`, and
  `BalanceAsOf` walks back over every counted row. If the bank's figure
  excludes pending authorizations, a day before a pending row reads that
  row's amount too high until it settles, and the net-worth history and the
  projection's opening balance carry the same shift. Kept identical on
  purpose: three definitions of "the balance on a day" would disagree on the
  chart where two of them meet. The golden net-worth test compares each
  account's newest balance only; a week-old `balance_snapshots` row against Simplifi's own
  daily balance is the comparison that would reach this.
- **The history start ([§4](#4-net-worth)) counts pending rows and the day
  the account was added.** A pending row is the bank reporting activity on
  the account that day, settled or not; a declined authorization that
  disappears moves the start later again on its own. Adding the account is
  when its balance was first seen, which is the only evidence an account
  linked with no rows has. Not golden-tested: what Simplifi does for an
  account added with no history would show in its first
  `accountsBalancesStore` row against the account's creation.
- **Before its history start an account is zero in net worth, not absent
  from it.** The group rows keep the account and open at 0.00, so a group
  that began inside the window has a null percentage rather than
  disappearing.
- **The projection's point for today starts from yesterday's close**
  (`readCashFlow` opens at `LedgerBalanceAsOf(today − 1)` and adds
  occurrences, not rows posted today), so on an account with rows posted
  today the "Both" chart's two lines meet a step apart at today. Each line
  keeps its own figure rather than one being patched to meet the other.

### Gaps in §9's alias table

- **Biweekly has no entry.** It is `EVERY_X_DAYS` with `INTERVAL=14`, not
  `FREQ=WEEKLY;INTERVAL=2`. The two are not interchangeable for phase, and
  the 26-versus-27-occurrence behaviour that "extra paycheck month" depends
  on comes out of the daily form.
- **`EVERY_QUARTER` takes an optional `BYMONTHDAY`**, while `EVERY_MONTH`
  always has one. With none, the series' anchor date supplies the day.
- **`EVERY_YEAR` carries no `BYMONTH`** — the anchor date supplies the month
  and, without a `BYMONTHDAY`, the day. A Simplifi export can hold yearly
  rules with a `byMonth` list; such a rule expands here to its anchor's
  month alone, one occurrence short per year. The golden test names the
  forecast month this affects rather than comparing it.
