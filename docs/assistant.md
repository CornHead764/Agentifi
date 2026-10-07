# The assistant

The assistant is an agent over the ledger. It reads anything the app can show,
and when a space lets it, it proposes any change the app supports. You say
what you want in chat; it answers with a card showing exactly what it would
do; nothing happens until a person presses **Accept**.

## The model

Each space connects its own model on the Assistant page (**Change the
model**): an OpenAI-compatible chat-completions endpoint, a model name and an
API key. A self-hosted model works. The key is sealed with the credential
cipher (see [`operations.md`](operations.md#secrets)) and is never sent back;
the API says only whether one is kept. Only the space's owner or an admin
may save or remove the connection, including its three switches: any other
member can read the connection and ask the assistant things, but cannot
repoint it, hand it a different key, or change what it is allowed to do.

Tool calls come in one of two styles, chosen in the **Tool calls** select:

- **Native**, the default: the API's own `tool_calls` field. A vLLM server
  needs `--enable-auto-tool-choice` and a `--tool-call-parser` for its model.
- **In the prompt**: the tools are described in the prompt and the model's
  calls are parsed back out of its text, for a server without a parser.

**Test** (`POST /assistant/connection/test`) probes both styles with a
harmless tool and reports whether the server is reachable, which styles work
and which it recommends. It does not change the setting.

The connection has three switches: **Assistant enabled**, **Let it propose
changes** (on for a new connection, so category suggestions work from the
first save; each change is a card a person accepts) and **Apply without
asking** (off until a person turns it on). With changes off the model is not
offered the change tools at all.

`ASSISTANT_ALLOWED_HOSTS` is the only server setting: a comma-separated list
of hosts a space may point its assistant at. Empty leaves the base URL
unrestricted.

## Conversations

A conversation belongs to the person who started it. Each question is one
request and one answer, no streaming, with at most ten rounds of tool calls
between them. Tool calls and their results are stored in the conversation so
a figure in an answer can be checked, but they are not replayed to the model
on later questions.

A conversation can be started about one logged email. The mail's text is
fetched for each question, handed to the model as untrusted data and never
stored; a mail carrying a sign-in code is withheld.

## Tools

The catalogue is `domain.AssistantTools` and `domain.AssistantWriteTools` in
`backend/internal/domain/assistant.go`. Every tool call is served in-process
by `internal/api/dispatch.go`, which issues the request against the API's
own route registry in the caller's own space and with the caller's role,
exactly as the browser would; a viewer is refused a write there as the router
refuses it.

**Reading:** `list_accounts`, `search_transactions`, `spending_by_category`,
`income_and_expense`, `net_worth`, `spending_plan`, `portfolio_holdings`,
`upcoming_bills`, `recurring_series`, `cash_flow`, `savings_goals`,
`watchlists`, `list_categories`, `list_tags`, `list_rules`, `recent_alerts`,
`list_mail` and `read_mail`. `find` says which account, category, tag, rule,
recurring series, watchlist or goal a name means. `list_endpoints` and
`read_endpoint` reach anything else the API serves. The mail tools are only
offered in a conversation a person is having, never to an automation.

A reading tool answers through the path its screen reads, so the two cannot
quote different numbers: `net_worth` is today's point from `/net-worth`,
`spending_by_category` is the expenses report's allocations (the category
decides, so a refund lowers its category), and `search_transactions` is the
register's own matcher, counting every match in the window. `upcoming_bills`
is the Reminders strip's read: what falls due in the window, and every bill
from the last 60 days still unpaid, each with its `upcoming` or `past_due`
status. Its arguments are
read by the parsers the query string uses; a date, month, amount or `limit`
the query string would refuse is refused back to the model.

**Proposing.** Each of these records a card and stops:

| Area | Tools |
| --- | --- |
| Rules | `create_rule`, `update_rule` |
| Transactions | `update_transaction`, `update_transactions` (bulk), `mark_reviewed`, `split_transaction`, `create_transaction`, `delete_transaction` |
| Categories and tags | `create_category`, `update_category`, `create_tag`, `update_tag` |
| Watchlists | `create_watchlist`, `update_watchlist` |
| Spending plan | `set_plan_amount`, `create_planned_spending`, `update_planned_spending` |
| Bills and income | `create_recurring`, `update_recurring` |
| Accounts and goals | `update_account`, `create_goal`, `update_goal` |
| Anything else | `change_endpoint`, a request against any registered route |

A proposal's arguments are checked when it is made, not only when it is
applied: in an automation an income category on money out is refused, and an
expense category on money in (a refund, return or credit from a merchant) is
held as a card for a person even where the automation applies its changes; a
bulk tool takes at most 100 rows, and a split of a matched merchant row must
follow the order's items (see [`connectors/merchants.md`](connectors/merchants.md)).

## Names, not ids

People say "the travel card" and "groceries". Every change tool takes a name
or an id and resolves it on the server, against the space's own rows, through
`domain.ResolveName`:

1. An id matches outright.
2. A whole name or alias matches next.
3. Otherwise every meaningful word of the request has to begin a word of the
   row's name or an alias. Words such as "my", "the", "card" or "category" are
   ignored. There is no fuzzy matching.

A name that fits one row becomes its id. A name that fits several is refused,
and the refusal lists the choices so the model asks the person which one they
mean; it never picks. A name that fits nothing is refused with a list of what
does exist.

A rule may name a category that another card in the same conversation is
about to create. The request then carries a placeholder, `@action:<card id>`,
replaced with the new category's id when the rule's card is applied. If the
category's card has not been applied, the rule's card fails and says which
card to accept first.

## A card's life

A proposal is a row in `assistant_actions`: the model's one-line summary, the
exact request (`method`, `path`, `body`), and a `preview`, the same request in
words with every id it names already named. A bulk tool writes one row per
transaction, joined by a `group_id`, and the page draws the group as one card.

| Status | Meaning |
| --- | --- |
| `pending` | proposed; the card shows Accept and Decline |
| `applying` | Accept claimed it and its request is running |
| `applied` | the request succeeded; the id it created is `resource_id` |
| `failed` | the API refused it, and the card says why |
| `discarded` | declined, with an optional reason |
| `simulated` | written by an automation's dry run; final, never applied |

Accept claims the row with one conditional update, so two presses or two tabs
issue the request once. Only a pending or failed card can be claimed, so a
refused card can be tried again. A card still `applying` two minutes after it
was claimed belongs to a process that died mid-request; reading it settles it
as `failed` with a note that the change may or may not have landed.

An applied card links to what it made or changed: a rule, a recurring series
or a category opens that row in its list (`/rules?rule=<id>`,
`/upcoming/recurring?series=<id>`, `/settings/categories-tags?category=<id>`),
a watchlist its page, and a change to one transaction that transaction in its
account's register.

At Accept a person may change the category, or each split's category, before
the request is issued. The card then keeps what the model asked for beside
what was applied, and the difference is recorded as a correction (see
[Automations](#automations)).

Accepting a card about a transaction also marks that transaction reviewed,
unless the request set `is_reviewed` itself: the person has just read the row
and decided on it, which is what the review tick records.
A suggestion an automation left on a row is marked reviewed on Accept
whatever it set, since automations are told that tick is the person's.

A person who files the row under a different category themselves, through
the ordinary edit or by splitting it, answers the suggestion too: every card
still pending on that row is declined with the reason "They filed the
transaction themselves.", and a category chosen over the model's is recorded
as a correction, as at Accept. An edit that leaves the category as it was
keeps the suggestion.

**Accept all** is `POST /assistant-actions/apply-many`. It takes up to 200
cards and applies them one at a time in the order they were proposed. Cards
already applied or declined are reported as they stand. Every route that
decides a card is registered with `Write`, so a viewer can read a card but not
decide it.

## The model hears back

Deciding a card writes an `action` message into the conversation: accepted
with the id it made, refused and why, or declined with the person's reason.
The next question carries those lines at its front, marked as the app
speaking rather than the person, because several open models' chat templates
reject a system message in mid-conversation. The model never learns the
outcome inside the answer that proposed the change, so a refusal cannot send
it looking for another way through in the same turn.

## What only a person does

Some routes use a credential rather than change the ledger: signing in to a
bill provider, a mailbox or a merchant account, answering a sign-in code, and
forgetting a kept password or session. The dispatcher refuses them to the
assistant (`dispatchDeniedRoutes` in `internal/api/dispatch.go`), along with
the assistant's own routes and the server-admin routes. The refusal carries a
Markdown link to the exact control, such as `/settings/bills?sign-in=<id>` or
`/settings/bills?challenge=<id>`, and the model hands it on. The chat renders
links to paths inside the app and leaves any other link as text.

A merchant account's invoice backfill is refused too, though it uses no
credential: it opens the merchant's site once for every order still without
an invoice, far more than a pull, so starting it is the person's call. Its
refusal links `/settings/merchants/<merchant>?backfill=<id>`, which opens that
account's confirm.

Drafting a mail rule through `suggest-rule` is refused for a different
reason: it would be the model asking itself. The refusal tells it to read the
message and propose `POST /email/rules` directly.

## When it is not ready

A screen that needs the model offers the fix where it is. An automation run
that cannot proceed records `error_code` `assistant_unavailable` (no model,
or the assistant switched off) or `changes_off` (it would propose or apply
and changes are off); the page answers the first with the connection form and
the second with a **Let it propose changes** button. The mail features answer
`409` with `"code": "assistant_unavailable"` for the same reason.

Asking for category suggestions (`POST /assistant-automations/fire` and the
merchant orders card) refuses with `409` and a `code` before queueing anything
that could not run: `assistant_unavailable`, `changes_off`, or
`automation_off` (no enabled transaction automation, as when the built-in has
been switched off). The register's toast for each carries the step that fixes
it (the setup form, turning proposals on, switching the built-in back on) and
asks again once the step is done.

## Apply without asking

With **Apply without asking** on, change tools run at once through the same
claim and apply path as the Accept button, and the model is told the result
immediately and reports it in the past tense. A transaction changed this way
is not marked reviewed, because nobody has looked at it. Turning changes off
clears this switch.

## Automations

An automation runs the model on a trigger, as the person who created it.
They live on the Assistant page and under `/assistant-automations`.

- **Triggers:** a transaction arriving, a daily time, or by hand.
- **Modes:** observe (answers only), propose (cards) or apply (as apply
  without asking).
- **Runs** queue in a table and a worker takes them one at a time
  (`AUTOMATION_WORKERS` raises that). A run can be a dry run, whose cards are
  `simulated`.

The built-in templates, in `domain/automations.go`, are **Suggest
categories** on each arriving transaction (transfers skipped, propose), a
daily sweep of uncategorized rows, an unusual-spending flag and a daily
cash-flow forecast. A space whose assistant may make changes gets Suggest
categories created for it the first time rows arrive or a person asks for
suggestions, unless it already has a copy (switched off stays off) or another
enabled transaction automation that proposes or applies.

The cash-flow forecast is shown twelve months of money in and out and the
reminders through the last of its six months, the bills from the last 60 days
still unpaid among them, all as dates and amounts with no names. Transfers and
card payments are left out of both.

The category check decides from the payee's history when the history is
decisive, without calling the model: when the vote's score
(`category_vote` in [calculations.md](calculations.md)) reaches the
automation's threshold. Otherwise the model sees the row, its account, the
history and the vote, any merchant order behind the row and the household's
written guidance, and is asked for its best guess and how sure it is. History
improves a guess but is not needed for one: a fund bought on a brokerage
account, a dividend or a well-known chain is placed from the row alone, and
only a row with nothing to go on (a bare reference on an everyday account) is
answered with "Could not guess:" and the reason. The prompt's rules, a later
one outranking an earlier one, are direction (money out to expense; money in
to income only when it is earned — pay, interest, dividends, rewards — and a
refund, return or credit from a merchant to the expense category of what was
bought, which on a card is what a credit that is not a payment usually is),
the row itself, history, an existing category, the order or
receipt behind a charge, the household's corrections, and its guidance.

The row's own category is evidence, never the answer. The row in the context
carries `category_standing` (`domain.CategoryStanding`): a category somebody
reviewed (`domain.ReviewedCategoryStands`: the flag counts wherever it was
set, an import included, except on an account whose rows are born reviewed)
stands, and only something that plainly shows it is wrong (direction, an
order's items, a correction or the guidance) is reason to propose another; an
unreviewed one is weighed as one piece of evidence. History alone never files
over a reviewed category: a confident vote that disagrees with one goes to the
model. A proposal of the category the row already has is not recorded (the
model is told the category stands), and the register never shows a waiting
suggestion that equals the row's category; only differences surface. Neither
marks the row reviewed.

Uncategorized history is weak evidence, never a veto. Rows nobody has
reviewed, and rows that arrived marked reviewed (an import, or an account that
marks every row reviewed), say nothing about the category; rows the household
reviewed and left uncategorized lower the model's confidence. A payee whose history is
mostly transfer legs is filed without asking the model: the check proposes
Credit Card Payment for a row on a credit card and Transfer otherwise, at the
vote's confidence. A row nothing paired that is money in on a credit card
under a payment's wording (PAYMENT, PYMT, PMT, AUTOPAY, and none of REFUND,
RETURN, REVERSAL, CASHBACK, REWARD) is proposed as Credit Card Payment at
0.8 (`domain.LooksLikeCardPayment`) when the history does not settle it. A
paired leg is skipped: pairing files it.

The model ends its answer with a line `Confidence: 0.62`, on a scale the
prompt spells out (0.9 or more when history and the row agree, under 0.4 for a
plausible reading with little behind it). The run records that figure as its
confidence; a run decided from history records the vote's score. The
threshold is the only gate: every guess the model makes becomes a proposal
card with its confidence, and the household decides.

A model sometimes states its answer without calling `update_transaction`:
"Category: Water (id …)" and a confidence line, and nothing proposed. When the
run made no change and the answer states a confidence and names exactly one of
the space's categories by id (`domain.StatedCategory`; row and account ids in
the answer do not count), Agentifi files that category itself through the same
tool, so it is validated as the model's call would be and lands as the same
card under the automation's mode. An answer that says "Could not guess", names
two categories, or states no confidence files nothing, and the row is left
undetermined.

To ask for suggestions over rows already in the register (after an import,
or after changing the prompt or the guidance), select them in Transactions and
choose **Suggest categories** beside *Mark as reviewed* (the selection bar on a
phone), or *Suggest a category* (*again*) on one row's menu or in its dialog.
**Suggest categories for all N matching** in the register's menu, and beside
the selection once it holds every loaded row, asks for every row the
register's filter and window match, without loading them. That is
`POST /assistant-automations/fire` with `{"transaction_ids": [...],
"force": true}`, or `{"all_matching": true, "force": true}` with the
register's own query on the URL, as *Mark all as reviewed* sends it; `force`
queues each row even if it was checked before, already has a category or
falls outside the trigger's filter.

There is no cap on the rows. Above `domain.BulkSuggestionRows` (200) the
register asks first, naming the count and that the model is asked once per
row, and the request's runs wait behind every other run, so the rows a sync
brings are still checked as they arrive. Each request is a batch
(`GET /category-suggestion-batches/{id}`, and `.../latest` for the one the
register shows) that the strip above the register follows: *x of y*, a
**Cancel** that drops the runs not yet started (`POST .../cancel`), and, once
done, how the answers compare with the categories the rows already had,
reviewed and unreviewed rows apart
([`calculations.md` §2](calculations.md#suggestion_batch_summaryrows-runs)).
Suggestions surface on their rows as any other does: only where the check
disagrees.

An automation started from a built-in follows its template's prompt and
description for as long as the household leaves them as the template has
them: each is shown and run from `domain.AutomationTemplates` as the code has
it, so a rewritten template reaches every such copy with no migration. The
store decides on each save, comparing the saved text with the template's with
whitespace ignored: a prompt the household changed keeps its own words, and
one put back to the template's text follows it again. The prompt and the
description are judged apart; the trigger, context, tools and threshold are
the household's settings and are never taken from the template after the
automation is made.

Each correction a person made at Accept is stored with the payee,
bank wording, amount and both categories, and the next check shows the model
the recent corrections for the same payee and a few others. That is the only
feedback the model gets; nothing is fine-tuned.
