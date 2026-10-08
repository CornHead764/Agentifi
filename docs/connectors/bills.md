# Bill providers

A bill provider is a company that sends a bill: the electric utility, the
insurer, the internet company. Agentifi signs in to each one, reads the
current bill, files its statement, and puts the real figures on the reminder
that stands for that bill. The Upcoming list then says what the bill *is*
this month rather than what it usually runs to, and the cash-flow projection
steps on the day the money actually leaves.

Settings → Bill providers is the screen. It has one card per **connection**
(one login at one provider), the **billed accounts** behind each connection,
the bills on file and the pull status. The series editor's Bill Connect tab
points a reminder at a billed account.

Nothing here syncs a bank. Balances and transactions are SimpleFIN's job. A
bill provider is reached the way the merchants are
([`merchants.md`](merchants.md)): through a browser on the server, signed in
to the user's own account.

The per-provider details are in [`providers.md`](providers.md). Writing a new
provider is [`adding-a-bill-provider.md`](adding-a-bill-provider.md).

## The catalogue

`domain.Billers` in `backend/internal/domain/bills.go` is the one list of
providers, and every fact about a provider lives there: its name, how it is
reached (`Access`), its home page, whether it has statements
(`HasDocuments`), whether it states its own autopay date (`ReportsAutopay`),
which second factors it is known to raise (`Challenges`), how often its kept
session is touched (`KeepaliveDays`), and whether each customer has its own
deployment (`NeedsSite`). `billers.New()` in `backend/internal/billers/`
lists the modules this build carries; the frontend mirrors the catalogue in
`frontend/src/lib/billers.ts`.

There are three kinds of provider:

| `Access` | reached through | examples |
|---|---|---|
| `api` | the portal's own JSON service, called from this process | Alliant Energy |
| `browser` | a kept browser signed in to the portal | Spectrum, Erie Insurance |
| `email` | the billing mail, read from the mailbox | Apple, Emailed bills |

A provider can be a **draft**: a module that signs in and keeps a session
but has no reader. A pull of a draft answers no bills and a note saying
the reader is not written, never a figure nobody read off a page.

**Emailed bills** is one catalogue entry for any company with no connector.
The connection's own name is the company ("Example Water"), it has one
billed account made with it, it is never pulled, and its bills arrive through
a [mail rule](#mail-rules). Add provider offers it as **Track it from the
bills it emails you**.

## The browsers

Chrome runs inside the application process, driven through playwright-go.
Each browser-provider connection has a **signed-in browser profile of its
own** on the profiles volume, and every pull and keepalive opens that same
profile with the same user agent, window size, language and time zone. That
is what device trust needs: a provider's "remember this device" lives in
cookies, local storage and service-worker caches, and a browser started fresh
each night is a new device each night. The sealed session in the database is
the fallback: if the volume is lost, the next pull rebuilds a profile from it.
The profiles are as sensitive as the database and are backed up and
restricted the same way.

A profile must only ever be open in one process, because two Chromiums on one
profile directory corrupt it. A lock file with a 30-second heartbeat holds
it; a lock whose heartbeat is more than three minutes old is stale.

A provider whose sign-in only works in Firefox runs in **Camoufox**, a
Firefox build served by its own container (`tools/camoufox`) and reached at
`CAMOUFOX_URL`. A module opts in by implementing
`browser.FirefoxProvider`, and from then on its sign-in, pull and keepalive
all run there; nothing is handed between the two browsers. With
`CAMOUFOX_URL` unset such a provider fails with "this provider runs in
Camoufox and CAMOUFOX_URL is not set" and never falls back to Chrome.
Camoufox keeps no profile on disk: each run is a fresh context seeded from
the sealed session.

Where a provider offers **remember this device**, the sign-in ticks it. A
provider that offers none often dates none of its cookies either, which
would end the session when the browser closed, so before a signed-in browser
closes every undated cookie is written back with a 30-day expiry
(`browser.PinSessionCookies`). Passkeys cannot be carried into the server's
browser; sign in with the password.

## Connecting

Press **Add provider**, pick the provider, and set the **Autopay** switch,
which every provider has. Off is the rule `none`. On shows how the autopay
date is worked out (on the due date, which it starts at, so many days before
it, or a fixed day of the month); the account that pays it is
`autopay_account_id` on the resource. A provider-stated autopay date wins
over the rule, and the rule never lands after the due date. With autopay off,
each billed account's newest unpaid statement is a reminder to pay it by hand
([Pay-manually reminders](#pay-manually-reminders)). A name is
optional; the card shows the provider's name unless one is given, and a
second unnamed connection at the same provider is refused with the advice to
name them. At a provider with one deployment per customer the dialog also
asks which one, in the provider's words: a single hostname label ("Which
community?"), or at a `SiteAddress` provider the portal's address ("MyChart
address"), which the server folds to the portal's root.

Every provider signs in one way: the **typed form**. Username, password and a
**Second factor** select. The engine plays them into the provider's own pages,
and anything the provider asks next (a code, a picture, an approval on a
phone) is asked in the dialog in the provider's own words. The dialog shows a
line per round of what the engine is doing ("Filling in the password",
"Erie Insurance asked which way to verify").

**The second factor** select offers *None / not sure*, *Code sent by e-mail*,
*Text message* and *Authenticator app (setup key)*. It starts on the choice
kept last time, otherwise on the authenticator where the catalogue says the
provider asks for one. The choice is kept on the connection (`second_factor`
is `""`, `email`, `sms` or `totp`) and does three things:

- A page asking which way to verify takes the chosen way and no other. When
  the page does not offer it, the sign-in stops: "‹Provider› asked which way
  to verify and offered "Email", "Text message". This login's second factor
  is set to an authenticator app, which ‹Provider› did not offer", and the
  trail line that showed the menu says so too. Taking another way would send
  a code where nobody is waiting for one. Only *None / not sure* lets the
  ranking decide (an authenticator, then a text; an e-mail never).
- A code box that names no channel is answered from the kept key for an
  authenticator login, and left for the mailbox or the person for an e-mail
  or text login. A minted code is never typed into a box waiting for a sent
  one. With *Text message*, or an authenticator with no key kept, the dialog
  asks the person for the code.
- A login whose second factor is a mailed code, or an authenticator with its
  key kept, is pulled unattended even at a provider known to text
  (`domain.SecondFactor.Unattended`).

**The authenticator setup key.** Enrol an authenticator at the provider in
the ordinary way and paste the setup key it shows (the base32 string behind
the QR code, not a six-digit code). Anything that is not a base32 key is
refused. The key is sealed beside the password and never shown again. The
engine is sent the key, not a code, and mints the code at the moment the
page asks (`billers.MintedCode`), waiting for the next window when fewer than
five seconds of the current one remain, because a code minted when the pull
started has expired by the time a slow portal asks.

With e-mail chosen and no mailbox connected, the dialog says so and links to
Settings → Email. A sign-in that reaches a code other than an authenticator's
watches the mailbox in the background, and whichever answer arrives first,
the mailbox's or the person's, is used.

**The password is always kept.** It is sealed onto the connection only after
the provider accepted it, and every pull signs in with it whenever the kept
session does not serve. **Forget password** (or **Forget password and key**)
drops it and the session together.

**A finished sign-in pulls at once**, on the server and through the same path
as Update now; the dialog says "Signed in. Fetching your bills from …" and
then how the pull went. Only one pull of a connection runs at a time, so
Update now answers 409 while one is running and the connection's `pulling`
field says so.

**Minimize.** While a sign-in is working, waiting or fetching, the dialog can
be minimized to a pill in the corner that says "needs a code" when it wants
somebody. The sign-in carries on and any page can be visited meanwhile. Cancel
and × give it up; closing the dialog stops the rounds and frees the profile
at the next round. A reload forgets the pill; the server's browser for it is
reaped 20 minutes after it was last touched.

Signing in lists the billed accounts the login can see (a utility login often
covers more than one service address). Untick the ones that do not matter. In the series
editor's Bill Connect tab, one select points a reminder at a billed account,
and two switches decide whether the bill may move its amount, its date, or
both. A person's own override on the reminder still wins over the bill.

Nothing is typed in by hand. The card offers no form for a bill or an
account: a bill comes from a pull, the mailbox or the assistant.

### A MyChart account

One connection per health system, because each runs its own MyChart.

1. Sign in to the health system's MyChart in an ordinary browser and copy the
   address from the address bar; any page will do
   (`https://mychart.example.org/MyChart/Home` is kept as
   `https://mychart.example.org/MyChart`).
2. **Add provider**, then **MyChart**. Name it after the health system and
   paste the address. Leave **Autopay** off unless the health system takes
   its bills from a card on file by itself: off, each account's newest
   unpaid statement is a reminder to pay it
   ([Pay-manually reminders](#pay-manually-reminders)).
3. **Sign in** with the MyChart username and password and choose **Code sent
   by e-mail** as the second factor if the portal offers it, so a nightly
   update can read the code from a connected mailbox. Tick nothing: the
   engine ticks "trust this device" when the portal shows one.
4. Untick any guarantor account that is not the household's.

Each update files the account's statements and pairs its payments with the
bank rows that paid them; the statement shows on that row as its receipt.

## Pulling

Every connection is pulled once a day in the sync window, API providers
first and browser providers after, a few seconds apart
(`service.Bills.PullDue`). A connection can name its own hour instead.
Bill connections, merchant accounts and mailboxes share one scheduled pass
(`service.runDue`): it holds a named lock per kind while it lists and runs,
so a second pass waits and then finds each row already stamped rather than
running it twice. Which rows are due is one query for
bills and merchants (`store.listDue`): the pull on, not paused, something to
sign in with, and not run in this window.
**Update now** runs one immediately and says what it found: new bills,
amended bills, statements filed.

A provider known to ask for a text message or a phone approval
(`domain.Biller.NeedsAPersonForACode`) is not scheduled unless the connection
has its own hour or its login's second factor is unattended, and the card says
so: a pull certain to stop on a code at night is worse than none.

What a pull does with what it finds:

- **A bill's identity is its billed account, due date and invoice.** The
  invoice is empty unless the provider bills separately on one day (TruGreen
  sets it, so two visits invoiced together are two bills). Finding a bill
  again changes nothing; a new amount on the same due date amends it; a newer
  due date supersedes every older open bill. A statement listed again under a
  different due date, with the same statement id and issue date, is still
  that bill and keeps the due date it was first stated with.
- The statement is fetched once and filed in the document store; the
  Statement button on the reminder, the Upcoming list and the dashboard opens
  it. The engine holds a statement for 10 minutes and Agentifi fetches it at
  once, so a statement is never a replayable URL.
- **The statement is filed on the row that paid it.** A bank row that settled
  the slot of a reminder linked to the billed account carries that bill's
  statement as a receipt: a document link of kind `receipt`, role
  `statement`, which the transaction's Attachments panel lists as "From the
  bill", with the provider's label and the due date. It is filed whichever
  comes first, the statement or the payment, and moves when the payment is
  unmatched from the slot, relinked to another reminder or deleted.
  `store.ReconcileReceipts` is the only writer: it computes what a row's
  receipts should be and adds or removes the difference, so running it again
  changes nothing, and it never touches a file a person attached. The
  scheduler runs it over the whole space on its first pass after start and
  once a day after, so a bill paid before its reminder was linked has its
  statement filed too.
- Each slot of the linked reminder, past-due ones included, shows the amount
  of the open or paid bill due inside its match window (`domain.SlotBill`).
  An open bill also gives its slot a due date and autopay day, and the
  projection steps on the autopay day. A paid bill moves nothing and settles
  nothing: its slot stays past due until a bank transaction matches it.
- **Bank history is matched again.** A bill changes what its slot is worth,
  so a past bank row the reminder's estimate refused may fit the bill.
  Whenever a bill is filed or amended (pulled, mailed or entered by hand),
  the reminder linked to its billed account is offered every unlinked bank
  row of its account dated within the reach of those bills
  (`domain.BillPaymentDates`: the match window of each slot a bill could
  claim), oldest first, through the ordinary series matcher
  (`service.SeriesMatcher.MatchHistory`). Linking a reminder to a billed
  account does the same across every open and paid bill on file. Only the
  linked reminder is offered the rows, and every matching gate applies: a
  row outside the band stays unlinked, a slot a row already holds is not
  claimed twice, an old slot is back-filled without moving the pointer, and
  the slot the reminder is waiting on moves it on. A row that takes a slot
  receives the bill's statement by the rule above.
- **Match history, on request.** The connection card's menu has **Match
  history**, and each billed account linked to a reminder has a **Match
  history** button. Either runs the same pass a new link runs, across every
  open and paid bill on file for each linked account
  (`service.Bills.MatchHistory`, `POST /bill-payments/connections/{id}/match`
  or `/bill-payments/subaccounts/{id}/match`), reconciles the statements on
  the reminder's rows, and reports per account: the rows it linked, the bills
  a row settles (`domain.TallyBillHistory`, by the same `BillClaimsSlot` that
  files a statement), how many of those have a statement on file, and the
  bills due by today no row settles. A bill due later counts in neither. A
  viewer is refused.
- **A statement can be filed on the payment it settled.** See
  [Statements filed as receipts](#statements-filed-as-receipts) below.
- **A paid bill shows on its payment even with no statement.** A provider
  whose bills are its payment history (`HasDocuments` false, such as
  Northwestern Mutual) never files a document, so its payments carry no
  receipt. `GET /bill-payments/transactions/{id}` answers the open or paid
  bills whose slot the row holds, and the Attachments panel names any of
  them with no statement ("Pays the … bill due …. No statement document came
  with it.").
- **A reminder on the wrong schedule is named.** When the median gap between
  a billed account's due dates (`domain.BillDueGap`) is under half or over
  one and a half times the linked reminder's period
  (`domain.BillCadenceDisagrees`), Match history says so. A monthly reminder
  linked to a yearly premium shows the premium in every month, and only the
  slot near each due date can take its payment; the fix is the reminder's
  frequency, which matching never changes.
- A billed account linked to a card or loan (see [Mail rules](#mail-rules))
  also writes that account's statement figures.
- **A billed account with bills and no reminder is offered one.** Its row in
  Settings → Bill providers has *Suggest a reminder*
  (`GET /bills/subaccounts/{id}/suggested-reminder`): the series its
  statements describe, with the frequency, the active months of a seasonal
  provider, the usual due day and amount, and the account and wording of the
  bank rows that paid them ([calculations §9](../calculations.md#a-reminder-suggested-from-a-billed-accounts-bills)).
  It opens in the series editor to confirm; saving creates the series and
  links it, which matches the bank history as above. When those rows already
  belong to a reminder, the row offers to link that one instead.
- The session the provider handed back replaces the one before it.
- A figure a module could not read is a note on the pull, never a bill for
  zero.

**Keepalive.** A browser provider's kept session is touched without pulling
on its `KeepaliveDays` (weekly at most providers, daily at one that offers no
"remember this device"), so a forgotten session is found before the bill is
due. At an API provider the keepalive is the token refresh. A pull or a
sign-in counts as a touch, so a connection pulled within its
`KeepaliveDays` needs no keepalive.

### Statements filed as receipts

A provider marked `Medical` in the catalogue (MyChart) is a bill provider like
any other, listed with the rest, that also files each statement as the
receipt of the card payment that paid it, as a health savings account asks.
It bills one visit at a time, so its billed accounts feed no recurring
reminder. Its pull answers the payments the portal lists beside its
statements, and they are kept per billed account in `bill_payments`, known by
the provider's own key.

- **A payment is paired with a bank row** (`store.MatchBillPayments`, over
  `domain.MatchBillPayments`) when the row is money out of exactly the amount
  paid, posted from two days before the payment to five days after
  (`domain.BillPaymentSpan`), not pending, not a transfer leg, and held by no
  other payment, and when it is that payment's only such row and the row that
  payment's only candidate. Anything ambiguous is left unpaired rather than
  guessed. Pairing runs after every pull of the provider and in the daily
  receipts pass, which finds the bank rows synced since.
- **The row's receipt is the statement the payment settled**: the bill
  issued on the latest day on or before the payment
  (`domain.StatementsPaidBy`), every one of them when several were issued
  that day. It is filed by `store.ReconcileReceipts` like any statement on
  its payment, is listed "From the bill" in the Attachments panel, and
  `GET /bill-payments/transactions/{id}` answers that bill.
- **A pairing comes undone** when its row is deleted or becomes a forecast,
  and when the provider reads the payment again with another day or amount;
  the receipt goes with it, and the next pass may pair it again. Editing the
  row keeps both.

### Pay-manually reminders

With a connection's **Autopay** switch off, each shown billed account's
newest unpaid statement is a one-off reminder in Upcoming, on the
dashboard's Bills panel and in the register's Reminders strip, reading
"pay manually" with the due date (the issue date where none is printed),
the amount owed, the provider and the billed account, and the statement to
open. It ends when a payment that settles it is paired with a bank row,
when a newer statement arrives, or when a person marks it paid from the
reminder. A billed account linked to a recurring reminder keeps that
reminder, which says "pay manually" too, and gets no second one. The rule is
[calculations §9](../calculations.md#pay-manually-reminders).

## When a pull stops

**The kept session lapses.** A pull whose session does not serve signs in
with the kept password once, on the same pull, and never twice. Then one of
five things happens:

- *It gets in.* The bills are pulled and the new session is kept. Until that
  pull runs the card reads "Session expired; the kept password signs in at
  the next update".
- *The provider turns it away.* The pull stops with a reason naming the page
  it stopped on and the provider's own words, the card offers **Sign in
  again**, and unattended sign-ins are **paused** (`password_refused`),
  because a refused password retried nightly is how a provider locks an
  account. Turned away means evidence: the sign-in form asking again, or
  the provider's words saying the login was wrong or the account locked.
- *It ends on a page nothing recognises.* After the password such a page is
  read again, settled, every second for 20 seconds, since it is usually a
  hand-off still on its way to the account; then the provider's account page
  is read once more. If that does not get in either, the pull stops with the
  page's host and path in its reason and notes, the card offers **Sign in
  again**, and nothing is paused: the next update tries the password again.
- *The provider asks for a second factor nothing can answer* (a text, a push
  approval, a code by e-mail with no mailbox). Unattended sign-ins are paused
  (`code_needed`) and the card reads "‹Provider› asked for a code. Automatic
  updates wait until you sign in."
- *The provider cannot be reached.* The pull fails and the next one tries
  again; a password the provider never saw has not been refused.

A paused connection keeps its password and is not scheduled. **Update now**
tries once more, and the pause lifts on a pull that gets in, a person's
sign-in, or a password changed or forgotten.

**The provider asks for a code.** The engine keeps the sign-in open for 20
minutes and Agentifi records a **challenge**. Anything that can answer it
automatically goes first: the mailbox, for up to three minutes. Only then
does the "Bill sign-in needs a code" notification go out. It opens the
challenge dialog on Settings → Bill providers, with a field for the code, the
picture for a puzzle, or "I approved it, continue" for a phone approval, and
the pull finishes as though it had never stopped. After 20 minutes the
challenge expires and the next Update now asks afresh.

**Something else fails**: the site changed, the browser would not start. The
card shows the error.

A pull that stopped on a page keeps a masked screenshot of it until the next
pull that gets in, and the card's note offers **Show screenshot** (see
[the page a run failed on](README.md#the-page-a-run-failed-on)).

The alerts, in Settings → Notifications under Bills with push on by default:

| alert | raised when |
|---|---|
| Bill sign-in needs a code | a challenge is waiting and nothing answered it |
| Bill pull stopped | a pull failed, once per failing streak; "needs you to sign in again" when the session was the reason, and it opens that sign-in |
| Mailbox stopped reading | the mailbox failed at least two reads in a row over at least an hour |

### What the provider showed

Every round of a sign-in writes a trail line: the moment, the step, what the
engine made of the page, the address without its query string, the title,
which boxes were on show and how many of each, any line on the page that
reads like a complaint, and what the round **did**, in the provider's own
words ("did: pressed the button "Next"", or "pressed Enter, nothing on the
page matched a button"), then "the page changed" or "the page did not
change". The engine gives up after two rounds that left the page as they
found it, and the failure names what was pressed ("the page did not change
after pressing "Next"").

The notes say once which browser the sign-in ran in ("… sign-in ran in
Camoufox" or "in Chrome"). A press that times out says what the page shows.
Something lying over the button is named by what is on top at its centre, by
its markup and never its text: a page check is said as one ("the page check
had not cleared and covered the "Sign in" button"), and
anything else by its tag with its id or first class, or a frame by its host.
With nothing on top, a button still disabled is said to have stayed disabled
("the "Sign in" button on … page stayed disabled"), and a page that shows
neither gets "could not be pressed", with no cause claimed.

Before that sentence is reached, a button the page shows pressable with
nothing on top is pressed once more past Playwright's checks, and a click
whose log shows it done is counted as pressed, since only the wait for the
page it led to ran out. Either way the round's trail line says so in its
note, followed by "Playwright:" and the last steps of Playwright's own log
("attempting click action; element is not stable; retrying click action"),
with any element named only by its tag, id and class. A line of that log
carrying anything else is left out whole, so nothing typed can reach the
trail. A button pressed that way is marked forced on the trail.

A failed sign-in folds the trail out under **What the provider showed**,
with a snapshot of the last page (`billers.Snapshot`): its headings, the
start of its text, and every control with its tag, id, role, type, name,
aria-label and words, including open shadow roots and same-origin frames. It
is read before the browser closes. It is safe to paste into a message:
nothing typed is in it, no field value, no attribute named for a token,
secret or password, no link address, no cookie, no picture, and no run of
four or more digits.

## Releasing the browser

A sign-in refuses to start while another holds the connection's profile:
"An earlier sign-in to this connection is still open, or a restart left its
browser locked." Either a sign-in this server is still running holds it, or
a restart mid-sign-in left a lock file and Chromium's leftovers on the
volume. **Release the browser**, on the dialog that shows the refusal, gives
up both and nothing else: the session, password and profile stay. "There was
nothing to release" is an ordinary answer. It is refused while something is
still driving that browser; a lock whose heartbeat stopped is released after
three minutes. Only an owner may release a browser.

## Forgetting

**Forget password** drops the kept password and the authenticator key with
it, and forgets the session too. A connection with no password kept offers
**Forget session**. Forgetting a session clears it and deletes the
connection's browser profile, because either one left behind is a lie about
the other. The connection, its billed accounts, links and bills stay, and it
reads "needs sign-in" (a merchant account forgets its session the same way).
A pull with neither a session nor a password stops as "needs sign-in" too.
Deleting the connection removes its billed accounts,
unlinks their reminders, takes their statements off the rows that paid them,
and leaves the statements to the document store's purge of unlinked files.

## Developer steering

Writing a module needs to see the signed-in site, and four owner-only routes
steer a live sign-in over the connection's kept profile:
`/bills/connections/{id}/sign-in/{session}/goto`, `click`, `dom` and `fetch`.
Start the session with `POST /bills/connections/{id}/sign-in` and
`{"mode": "live"}`; the settings do not open a live sign-in, so this is done
from the browser console or with the API token. Close it with `DELETE` on the
session. A Camoufox provider refuses a live sign-in.

`goto` and `click` answer the page snapshot and the traffic: the provider's
document, XHR and fetch calls with their redacted bodies and the start of
their answers, its scripts by name, a file a click started, and an address a
click opened in a second tab. `dom` answers the markup behind a selector.
`fetch` makes a call from the signed-in page, and with `find` searches a
large answer such as an app bundle.

Two rules hold for all four. The browser never leaves the registrable host of
the catalogue's `Home` or a subdomain of it, because a signed-in browser sent
elsewhere is a session handed over. And nothing they answer carries a field
value: the DOM reader drops `value` and any attribute named for a token,
secret or password, request bodies read `[removed]` for any field named
around password, secret, token, key, otp, code, pin or ssn, and an
authorization header reports only its scheme.

## What the assistant may do

The assistant reaches the bills routes through the in-process dispatch
([`../assistant.md`](../assistant.md)). It can list and read connections,
billed accounts, bills and challenges, create and edit connections, billed
accounts, bills and links as the viewer and write rules allow, and run a
pull. `dispatchDeniedRoutes` refuses it everything under a connection's
`sign-in`, `session`, `browser` and `credential`, and a challenge's `answer`:
a model must not type a second factor nobody approved, and releasing a lock
is done with the connection in front of a person.

## Safety

- The password, the authenticator key and the kept session are sealed in one
  blob on the connection row with the credential key (AES-256-GCM, bound to
  the space and the connection id), so a dump or a copy onto another row
  opens nothing. The password is held in memory only between a sign-in's
  start and its landing and during a pull. No route returns it: a listing
  says `credential_source: "stored"` and `has_totp`, nothing more.
  `billers.Credentials` redacts itself in every log format.
- To keep a provider's password out of Agentifi, do not connect it; its
  mailed bills can still be filed by a mail rule.
- Money arrives from a module as a string and is parsed once into a decimal.
- Nothing captured from a real provider goes in the repository. Fixtures are
  invented, never captured.

## The mailbox

A provider with no sign-in worth having mails its bill, and a provider with
a sign-in mails its codes. One mailbox Agentifi can read answers both.
**Settings → Email** holds the mailbox connection, **Recent mail** and the
**Mail rules**; it also answers the merchants' sign-in codes.

Use a **dedicated mailbox**, for example a shared mailbox on a Microsoft 365
tenant, which needs no licence. Move billing mail into it with a rule on the
primary mailbox. Prefer **redirect** over forward: a redirected mail arrives
as the provider sent it. A forwarded one is unwrapped (its sender and date
taken from the quoted header block) only when it came from an address at the
watched mailbox's own domain or one listed in `EMAIL_FORWARDERS`; a "From:"
line in a stranger's mail is a claim, not a sender. The card's **Folder** is
what the reader watches.

The watched folder is read every 30 minutes (`EMAIL_POLL_MINUTES`), starting
30 days back on the first pass. A mail a parser or a rule recognises becomes
a bill on the same billed account a pull would use, under the same identity
rule, so an emailed and a pulled bill for one cycle are one bill. A PDF
attachment is filed as the statement. A bill with no PDF is printed to one
from its headers and body, by Chrome with JavaScript off, offline and every
request aborted, so printing tells the sender nothing; a mail that may carry
a sign-in code is never printed.

### Office 365, by device code

An app registration in the tenant (Entra ID → App registrations), single
tenant, no redirect URI, **Allow public client flows: Yes** (without it the
sign-in is refused with `unauthorized_client`), and the delegated Microsoft
Graph permissions `Mail.Read`, `Mail.Read.Shared` and `offline_access`.
`Mail.Read.Shared` is what reads a shared mailbox. There is no client
secret.

**Add mailbox**: Office 365, a display name, the address, and the
registration's application (client) id and directory (tenant) id, neither of
which is a secret. **Sign in** shows a code and a link to
`microsoft.com/devicelogin`; sign in there as an account that can read the
mailbox, and the dialog notices on its own. The refresh token is sealed with
the credential key; the access token lives in memory. A revoked consent or
changed password reads "Could not read the mailbox: …" on the card, and
**Sign in again** repeats the device-code step.

### Gmail, by app password

Gmail and Workspace go in as **IMAP**: `imap.gmail.com`, port 993, the
address as the username, and a folder (`INBOX`, or a label by name). The
password is an **app password**, which needs 2-Step Verification on the
account; the account password is refused as "Invalid credentials". The app
password is sealed only after the server accepts it. **Forget sign-in** drops
it and keeps the mailbox row, folder and log.

### Recent mail

Each mailbox shows the folder it watches and when it was last read; **Read
now** runs one immediately. **Recent mail** lists the last 50 messages with
what the reader made of each: *Filed a bill*, *Answered a code*, *Posted by
rule*, *Not a bill*, *Kept for you to read*, or *Could not file: …*. A row's
menu offers **Ask the assistant**, **Suggest a rule** and, for a message
nothing was made of, **Read again**, which runs the reader over it once more
(a row that already produced something is refused).

### Mail rules

A mail rule is a parser the user writes on the Email page. It either
**posts a transaction** (a receipt) or **files a bill** on a bill provider.
It has three groups:

- **When a mail**: the sender (one address, or `@` and a domain), a subject
  fragment and a body fragment. Every condition given must hold.
- **Read**: the amount, from **after a label** or from **a pattern** (a
  regular expression with one capture group, refused if it does not
  compile); the date, the same two ways or **the day it arrived**; an
  optional reference; and the payee, fixed or the rest of the line after a
  label. Prefer a label: anybody can pick one by looking at the mail.
  A transaction rule may also keep a stretch of the mail in the notes, such
  as the list of what was bought: **Notes from below** names the label whose
  line it starts under, and **Notes up to** the label whose line ends it (left
  empty, the first blank line ends it). Neither line is kept, each kept line
  has its spacing collapsed, and the stretch stops at 40 lines and 2,000
  characters. The notes are the reference, then the stretch on the lines
  below it. A start label the mail lacks, or an end label that never comes,
  keeps nothing and posts the transaction all the same. Bills carry no notes.
- **Then**: for a transaction, the account, category and direction, and
  **Also record the income it was taken from** for a payroll deduction, which
  posts the same figure back as income so the pair nets to nothing against
  the balance. The income row is linked to the purchase: the register lists
  the purchase with a mark and hides the income row behind a chip that says
  how many are hidden and what they add up to, the plan folds them into one
  line, and deleting the purchase deletes its income row. Every figure still
  counts it as income
  ([calculations.md](../calculations.md#is_paddingtxn---bool)). For a bill,
  the provider (a connected one or an Emailed bills one) and optionally the billed account; the date is the **due date**, the
  reference is the **account number**, and a **statement date** and
  **minimum payment** label are added.

An invented receipt and its rule:

    Subject: Cafe Receipt

    Your receipt from The Corner Cafe
    Receipt Date: 9/27/26
    Item         Qty  Price
    Soup          1   2.75
    Coffee        1   1.75
    Receipt Total: $4.50
    ReceiptID: AB1234567

Sender `@cafe.example`, subject `Cafe Receipt`, amount after `Receipt
Total:`, date after `Receipt Date:`, reference after `ReceiptID:`, payee the
rest of the line after `Your receipt from`, notes from below `Item` up to
`Receipt Total`. The transaction's notes read:

    AB1234567
    Soup 1 2.75
    Coffee 1 1.75

An HTML mail is read as its text: each table row is one line with its cells
side by side, so an order table keeps a row per item.

A bill rule files through `Bills.Ingest` exactly as a pull does, so a mailed
and a pulled bill for one cycle are one bill. With no billed account named,
it picks the one whose number ends the same, else the only one, else a new
one keyed on the number. A rule that names a due-date label and cannot find
it files nothing and says so; one that names no due date files on the day
the mail arrived.

**A card statement fills the card.** SimpleFIN reports no statement balance,
minimum or due date for a card. Link a billed account to the card or loan it
is (**Link a card** on its row, or **Statement of** in the rule dialog), and
a bill filed on it writes the card's statement balance, minimum due and due
date: a later due date replaces all three and clears a minimum it does not
state; the same due date refreshes figures a bill wrote but not ones typed
by hand; an earlier due date writes nothing. One billed account links to one
card. This is separate from the connection's autopay account, which pays the
bill.

**Try it** at the bottom of the dialog takes a pasted message and shows what
the rule would read and post or file, notes included, without saving
anything. A rule runs only on mail read after it exists; **Read again**
applies it to an older message that nothing was made of. A message that
already posted a transaction is not read again, so a notes label added to a
rule fills the notes of later receipts only.

### Codes by e-mail

Where a provider lets the user choose how a code is sent, choose e-mail to
this mailbox. While a sign-in waits for a code, the mailbox is read every 15
seconds for up to three minutes, and a code from the provider is answered
into the waiting challenge and logged as *Answered a code*, never read twice.
A provider that only texts needs the text relayed into the mailbox by any
SMS forwarder; set `EMAIL_OTP_RELAYS` to the forwarder's address(es), or a
relayed message is mail from a stranger and is ignored.

Three kinds of sign-in wait on the mailbox: a challenge a bill pull parked,
a merchant's sign-in dialog, and an unattended merchant pull that signs in
again. A push approval, a picture and an authenticator code without a kept
key never wait: the mailbox cannot answer them.

The digit reader goes first: a run of four to eight digits after "code",
"passcode", "verification" and similar, from the provider's own code sender
or a relay. While a sign-in is waiting it also accepts mail from the
provider's own domain (its catalogue home's host and subdomains; at a
`SiteAddress` provider, the domain its portal's host is a subdomain of), and when
the digit reader finds nothing it asks the configured assistant model one
question with no tools. That answer is used only if it appears verbatim in
the mail as a whole token of four to ten digits. The model is never asked
about mail when nothing is waiting. A code is kept in memory for the waiting
sign-in and never logged or stored.

The assistant's own access to mail (`list_mail`, `read_mail`, Ask the
assistant, Suggest a rule) is described in
[`../assistant.md`](../assistant.md). A message that carried a sign-in code
is refused to all of them.
