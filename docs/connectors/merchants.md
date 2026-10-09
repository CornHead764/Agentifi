# Merchants: Amazon and Costco

A bank row from a merchant says `AMAZON.COM*2K4D1R6Q3` or `COSTCO WHSE #0123`
and nothing else, so it gets filed under whatever that merchant usually is.
The merchant knows what was bought. The merchant connectors bring each order
or receipt into Agentifi, match every bank row to the purchase that charged
it, divide the row into one split per item, and hand the items to the
category check, so a television bought at Costco is filed as a television.

Merchants are not banks. Their records never create ledger rows (apart from
Amazon's gift card account, below); they explain rows the bank already
delivered, matched by amount and date.

## The merchant is a value

`domain.Merchants` (`backend/internal/domain/merchants.go`) lists the
merchants, Amazon and Costco, with each one's name, wording, settings path
and capabilities; `frontend/src/lib/merchants.ts` mirrors it for the client.
Every merchant account and every order carries its merchant, and nothing
assumes one.

Which merchants a bank row can belong to is decided from its wording by
`domain.MerchantsFor(statementName, payee)`: "amazon" or "amzn" names Amazon,
"costco" names Costco. A row is offered only the orders of the merchants it
names, and the order and charge queries are filtered by merchant, so a Costco
row is never offered an Amazon order. A row that names both is offered to
both; a row on the Amazon gift card account is Amazon's whatever it says. A
person matching by hand can reach any row (see [By hand](#by-hand)).

The screen is Settings → Merchants → Amazon or Costco
(`/settings/merchants/amazon`, `/settings/merchants/costco`): the accounts,
the orders on file and how many of the merchant's bank rows are matched. The
API follows the same split: what belongs to a merchant is under
`/merchants/{merchant}/…`, what belongs to a bank row under
`/merchants/transactions/{id}`.

## Accounts and the daily pull

A household can have several accounts per merchant. **Add account** opens the
sign-in; a name ("Alex", "Casey") is optional, and without one the account
shows as its sign-in email.

Neither merchant has an API for a person's own orders, so the server does
what a person does: it plays the email and password into the merchant's own
sign-in form in a browser of its own. Amazon runs in the in-process Chrome;
Costco runs in Camoufox, because its sign-in only works in Firefox (see
[`../operations.md`](../operations.md#the-browsers)); with `CAMOUFOX_URL`
unset the Costco page offers no sign-in and says why, from the `unavailable`
reason `GET /merchants/costco/agent` answers. Whatever the site asks
next, a code, a picture to read or an approval on a phone, is asked in the
same dialog in the site's own words. Each press on the form waits for the
page to change, up to six seconds, before the page is read again
(`agent.Submit`). The sign-in is the one a bill provider's runs
(`internal/connector`): the password is typed once per try, a factor page is
pressed through at most twice, two rounds that leave the page as it was end
the try, and each round writes a trail line (see
[`bills.md`](bills.md#what-the-provider-showed)). When the sign-in lands,
the browser's session is sealed onto the account with the credential cipher
and the daily pull is switched on.

**The kept password.** The password is sealed onto the account beside the
session, the same way a bill provider's is (see [`bills.md`](bills.md)),
because an account whose session expires without a kept password stops
pulling. It is held in memory until the sign-in lands, so a password the site
turned down is never the one kept. The **Second factor** select (*None / not
sure*, *Code sent by e-mail*, *Authenticator app (setup
key)*) says how the
account's codes are answered (a code sent by text is typed in at the sign-in); with the authenticator, its setup key is kept
with the password and the server makes the code itself. Neither is ever sent
back. **Forget password** deletes the password and key and keeps the session;
**Forget session** does the reverse, and the account then reads "needs
sign-in" with its **Daily** switch left as it was: nothing pulls until a
person signs in, and **Update now** answers 409 saying so. Removing the
account deletes everything, including its orders (matched rows keep their
categories).

**The pull.** Every day at `SYNC_AT` the scheduler pulls each account whose
**Daily** switch is on (`service.Merchants.PullDue`, on the same scheduled pass
as bill connections and mailboxes; see [`bills.md`](bills.md#pulling)), imports what it read exactly as an uploaded file
would be, then matches. The first pull after a sign-in reads a year back and
starts at once; later pulls read the last 30 days. **Update now** pulls on
demand, and **Fetch history…** reaches further back. Every pull re-seals the
session it was handed back as soon as the pull returns, before the import, so
a pull that gets in and then fails leaves the next one the merchant's latest
session. A pull that reads nothing is recorded on the account as a failed pull
with the engine's note, never as success. While a pull runs, the engine and the
module say what they are doing (`Call.Report`, carried on the context by
`provider.WithPullProgress`): opening the site, which order-history year and
page, how many orders so far, which invoice of how many, matching. The service
keeps the latest line in memory beside the pull's claim, and the account
answers it as `progress` (`line`, `started_at`, `updated_at`) while `pulling`
is true; the settings row and the sign-in dialog show it with the elapsed time
while they poll. A pull
files invoices only for the orders it reads inside its own window, so a
regular pull costs the site no more requests than the window does; the
orders on file from before it get theirs from **Backfill invoices…** (see
[What the pull reads](#what-the-pull-reads)).

**When the session lapses**, a pull with a kept password signs in once, in a
fresh browser seeded with the lapsed session so the device is one the site
knows, and carries on. What it ends on decides the rest:

- The site lets it in: the pull carries on with the new session.
- It asks for an authenticator code and a key is kept: the code is answered.
- It e-mails or texts a code: the household's mailbox is watched for it for
  three minutes and a code that arrives is answered.
- Any other code, a code that never arrives, an approval or a picture: the
  account shows **Code needed**.
- The password is turned down (the form asks again, or the site's words say
  the login was wrong or the account locked): the account shows **Password
  refused**, with the site's words and the page it stopped on.
- It stops anywhere else, such as a page nothing recognises, once that page
  has had 20 seconds to arrive somewhere: the pull stops naming the page's
  host and path, and the next sync tries the password again.
- The site could not be reached: a failed pull, tried again next day.

The two stops pause the account: the password stays kept, but the scheduler
passes the account over, session and all, until a person signs in, forgets or
replaces the password, or presses **Update now**, which tries it once more. A password tried every
night is how an account gets locked. Without a kept password the pull stops
at **Session expired**. In every case everyone who can write to the space is
notified with a link that opens the account's sign-in, and the next good pull
withdraws the notification.

A pull that gets in and then fails (the engine errors, nothing on the page is
read, the import fails) is a failed pull, tried again next day. The first
failure of a streak raises **Order pull stopped**, under Accounts in Settings →
Notifications with push on by default, and the next good pull withdraws it.

A pull or sign-in that stopped on a page keeps a masked screenshot of it until
the next pull that gets in, and the account's note offers **Show screenshot**
(see [the page a run failed on](README.md#the-page-a-run-failed-on)).

## Amazon

### Files

Each file is uploaded under one Amazon account (`POST
/merchants/amazon/imports`, with `merchant_account_id`). The shape is read
from the content, never the name:

1. **Amazon's own export**, the complete history. Your Account → Manage your
   data → Request your data → Your Orders. Amazon e-mails a link, usually
   within a day; the ZIP holds `Retail.OrderHistory.1.csv` (and `.2.csv` and
   on). Upload each. It has every order, one line per item, with what each
   line cost after tax and discounts and the day it shipped, which is the
   only source that lets a row match one *shipment* of an order.
2. **[Order History Exporter for Amazon](https://github.com/xenolphthalein/order-history-exporter-for-amazon)**,
   a browser extension that exports the orders page as JSON: order number,
   date, total, status and each item's title, ASIN, quantity and price. It
   does not know what was charged per shipment.

These are real financial data. Keep any copy in the checkout's gitignored
`data/` directory; nothing from them enters the repository.

### What the pull reads

The pull reads the orders pages, the card charges and card refunds from the
Transactions page, and each new order's printable invoice, which carries each
item's price, the tax, what a gift card paid and its **Refund Total**. An
order whose invoice has been read in full and whose invoice document is on
file is not opened again for those.

**Refund totals.** A return lands weeks after its order, when the invoice was
read long ago, so the pull also reads again the invoice of each order on file
that is due a check (`store.MerchantOrdersDueRefundCheck`): every order of
the last year never read for its refund, and every order of the last 100 days
not read for it in 14 days. These come after the new orders, inside the same
limit of 150 invoice pages, newest first; an order already opened by the pull
is not opened twice. Each read keeps the order's `refund_total` (zero for an
invoice that shows none) and the day it was read. The first pull after this
reaches back a year once, and later pulls check two or three orders a day. A
refund to the gift card balance is listed nowhere with its order: the
Transactions page shows card refunds only, and the balance's line reads
"Refund from Amazon.com order" with no number. The refund total is what ties
it to an order.

**The invoice document.** While the invoice page is open, the in-process
Chrome prints it to a Letter PDF, which is kept in the document store as the
order's invoice (`amazon-invoice-<order>.pdf`, a document link of kind
`merchant_order`, role `invoice`, source `merchant_pull`). An order read in
full whose PDF is not on file is opened once more for the print, inside the
same limit of 150 invoice pages a pull, so no pull opens more pages than it
would anyway. Only headless Chrome prints: with `AGENTIFI_BROWSER_HEADFUL` on, or in
any browser that cannot print, the pull notes once that no invoices were
filed and stops opening pages only to print, and the orders, items and
charges still land. A file import files no invoices.

**Older orders.** A pull opens only the orders placed within its window, so
an order imported from a file, or whose PDF is not on file, stays
without one until a person asks. **Backfill invoices…** in the account's menu
(offered to a signed-in account with orders on file, on a server with a
browser engine) first says how many orders on file have no invoice, then,
confirmed, opens each one's invoice page in turn for the print alone, newest
first and three seconds apart, so a backfill is never quicker on Amazon than
a pull. It is the pull's own page, read the pull's way, with the session the
account already has: no other request and no sign-in, not even with a kept
password. An order with no order page on file, or a cancelled one, is never
offered. A page that gives nothing to print counts as a miss
(`merchant_orders.invoice_misses`), and an order that has missed three times
is offered no more. The first page that asks to sign in, or shows a check
page, stops the backfill there and counts as no one's miss; the orders
already filed stay filed, and running it again carries on with the rest.

A backfill runs in the background under the account's pull claim, so a pull
and a backfill of the same account never run at once: **Update now**, **Fetch
history…** and a second backfill wait, the scheduler skips the account, and
the start answers 409 while either runs. The row shows "Backfilling
invoices: 4 of 9…" while it runs, and afterwards what it filed, what it left
and why it stopped early, if it did; the last result is kept on the account
(`merchant_accounts.invoice_backfill_*`). The routes are
`GET /merchants/{merchant}/accounts/{id}/backfill`, which counts the orders it
would reach, and `POST` to the same path, which starts it and answers 202.

It also reads the gift card balance page. The first time it finds a balance
it creates a gift card account named "Amazon gift card · <account>", included
in net worth, whose balance is the page's balance. Each line of the balance's
activity (a reload or refund in, an order paid from it out) is a row in that
account, written once. Those rows are matched only to the gift card's side of
orders: gift card charges and the gift card's share of an order's total.

## Costco

Costco has **purchases** of three kinds:

| Kind | What it is | Its number |
| --- | --- | --- |
| `warehouse` | an in-warehouse receipt | the receipt's transaction barcode |
| `fuel` | a gas station receipt | the same |
| `online` | a costco.com order | the order number |

A receipt without a barcode is numbered `<warehouse>-<yyyymmdd>-<transaction>`,
which is stable across pulls. A return receipt is a purchase with a negative
total. Keeping fuel its own kind is what lets a tank of fuel be told from a
trip round the warehouse.

Costco keeps about two years of purchases and offers no export, so the pull
is the only source; the importer reads only the pull's own format, and the
settings screen offers no Costco upload.

**Session.** What is kept is the session Costco's sign-in library (MSAL)
leaves in the page: a refresh token and the account's name, read from
`localStorage` when the sign-in lands (`SessionFromPage`) and sealed as a
`costco-b2c` session. "Keep me signed in" is ticked on the way, which makes
the refresh token long-lived. Every pull renews it and keeps the new one.

**How the pull reads.** Every call runs from a Camoufox page at Costco's own
origin, which is where the endpoints answer, with the page's own session. The
token refresh comes first, then the calls the Orders & Purchases page makes:
the receipts list by date range, each receipt's detail by barcode (which
carries the descriptions, quantities and amounts a split needs), and the
online orders with their lines. Everything the pull depends on, the token
endpoint, client id, routing headers and query text, sits in one block at the
top of `backend/internal/merchants/costco.go`. A detail query the
endpoint refuses is noted on the account; those rows still match but cannot
be split.

**What lands.** Every receipt line becomes an item: number, description,
quantity and amount. Instant-savings lines are folded into the item they
name, so the items still add up to what was charged. Each tender on the
receipt becomes a charge, signed as the bank sees it; a receipt paid with two
cards is two bank rows. A **Costco Shop Card** tender, like cash, is money
the bank never saw: a receipt it paid in full matches nothing and is not
counted as waiting.

**The receipt document.** Costco's purchases API answers with a receipt's
lines, not with a document, and the Camoufox page the calls run from shows no
receipt to print. So for each warehouse or fuel receipt whose detail was read
in full (every line described, a total, a barcode), the pull lays out a
receipt of its own from those lines: warehouse, date, number, items, tax,
total and tenders, headed "Costco receipt" or "Costco return" and saying on
its face that Agentifi laid it out and that it is not a copy of Costco's
printed receipt. The in-process Chrome prints it to PDF with scripts off,
offline and with every request blocked, and it is kept as the purchase's
invoice (`costco-receipt-<number>.pdf`, the same link and role as Amazon's).
It costs Costco no request the pull was not already making. A receipt already
on file is not laid out again, and online orders get none: their lines are
read, but costco.com's printable order page is a separate page the pull does
not open.

**Receipts for purchases on file.** A pull lays out receipts only for the
purchases it reads. **Backfill invoices…** in the account's menu lays out a
receipt for every stored warehouse or fuel purchase that has no receipt
document and whose every item is described, newest first, from the lines,
tax, total and tenders already stored. It makes no request to Costco, so it
needs no session and is offered to any account with purchases on file on a
server with a browser engine; it runs in the background under the pull claim
and reports on the row the way Amazon's does. A purchase listed by item
number alone gets none until its lines are described. The first print that
fails stops it and says so.

**The item catalog.** A receipt names each line by item number and a register
abbreviation ("KS ORG EGGS"). After every import and once a day,
`service.Merchants.EnrichCatalog` looks each new number up in Costco's
same-day listing at sameday.costco.com, verifying that the hit carries the
number back. The name, brand, size, category, picture and price are kept in
`merchant_catalog`, keyed by merchant and number with no space or account,
because an item number means the same thing in every household. A number the
listing lacks is asked about again after 30 days; a row once found is never
overwritten with nothing. The purchase panel, split memos and the category
check all name the product, with the abbreviation beside it. The client is
`backend/internal/provider/merchantcatalog.go`; its persisted-query hashes
are what break when the listing changes, and the pass then logs
`PersistedQueryNotFound` and tries next day. `MERCHANT_CATALOG_ENABLED=false`
turns it off.

The listing answers only for one warehouse, and Agentifi ships no default
one: until `COSTCO_CATALOG_SHOP_ID`, `COSTCO_CATALOG_ZONE_ID` and
`COSTCO_CATALOG_POSTAL_CODE` are all set, nothing is looked up and receipt
lines keep their register abbreviations. To find them, choose a warehouse on
sameday.costco.com and read `shopId`, `zoneId` and `postalCode` from the
variables of any search request in the browser's developer tools. The
warehouse sets only the price; the item a number names is the same
everywhere.

## Matching

Matching (`domain.MatchMerchantOrder`, used for every merchant, and
`domain.MatchMerchantRefund`) offers each bank row the orders of its
merchants, money first and time second. The tiers, strongest first, each
recorded as the match's basis:

| Basis | Match | Confidence |
| --- | --- | --- |
| `charge` | a charge or tender to the cent, within three days | 0.98 |
| `order_total` | the order's card total, placed up to 14 days before the row (or 2 after) | 0.9 |
| `shipment` | one shipment's total | 0.8 |
| `item` | one item's total | 0.7 |
| `refund` | a return, for a credit | 0.95 |
| `refund_total` | what an order's invoice says was refunded, for a credit no return explains | 0.85 |
| `manual` | chosen by a person | 1 |

Two orders that fit equally lower the confidence by 0.2 rather than picking
one. A charge used by one row is not offered to a second, and an order
matched in full is not offered again by its total, so two rows of the same
amount find two orders. Only money out uses up a charge: a credit matched to
the order never does, so an order refunded at once keeps its purchase row, and
the invoice shows on the purchase as well as on the credit. A charge for an order placed longer ago (a
back-order) is found by the order number it carries.

**Gift cards.** The bank sees only what a card was charged. An order partly
paid by gift card matches on its total less the gift card share (from the
invoice, else the gift card charges); an order a gift card paid in full
matches nothing and is not counted as waiting.

**Returns.** A credit is first asked which return it gives back. A return is
a record of its own: the order, the line that came back when the merchant
names one, the day, the amount and where the money went. Amazon's come from
the refunds on the Transactions page. A credit matches a return by amount,
issued up to 14 days before the row, preferring a record that names the line,
and only where the money went: a refund to the gift card balance matches a
line on the gift card account, a refund to a card a bank row.

A credit no return explains is offered the orders whose invoice says
something was refunded (`domain.MatchMerchantRefundTotal`), placed up to 100
days before it, less what the credits already matched to each order took. The
order whose refund the credit is exactly comes first; two such orders lower
the confidence by 0.2 and the older is taken. A credit smaller than every
refund is taken as a part of one only on the gift card account, and only when
one order alone has room for it: a bank credit worded as Amazon's may be a
reward. On the gift card account only a line that says "refund" is offered,
since a reload is money in too. A match writes `transaction_refund_links`, the same
link a person makes by hand, so every calculation reads the credit as
spending returned rather than income, and the credit takes the purchase's
category (or the returned item's split category). The link goes to one of the
rows matched to the order as money out
(`domain.ChooseRefundedCharge`): a full or a partial refund, and several
partial refunds may share one purchase while the charge has room for them.
The link is made when the second of the two rows is matched, whichever it is,
once per credit: a credit that already has a link, made by hand or by an
earlier match, is left alone, and a link a person removes is not made again by
the next pull, which matches only rows still unmatched. A refund to a gift card
balance is a line on the gift card account, linked to the purchase like any
other credit.

**One split per item.** When a row is matched to the whole of an order with
two or more priced items, the row is divided into one split per item, each
item's share of the charge in proportion to its price (tax, shipping and
gift card fall on the items the same way), exact to the cent, with the item's
name as the memo. Every split takes the row's category and the parent's
category is cleared. A row somebody already split is left as it is.

Matching runs after every merchant import and pull, after every bank sync and
ledger file import for the rows that arrived, and on **Match now** (the
Orders card), which offers every unmatched row of the merchant, whatever its
date, to the orders on file. It only touches unmatched rows, so a match made
by hand is never second-guessed.

### By hand

Each order's menu has **Match a bank row…**, which offers the merchant's rows
from three days before the order to two months after; a search reaches any
row however the bank worded it (a Zelle payment does not say "Amazon"). Both
pickers rank the same way (`domain.RankMerchantOrders` and
`domain.RankMerchantRows`): first what the matcher's tiers would agree on
(a card charge, the card total, a shipment, an item) whatever the date, then
nearest to the card total, gift card share taken off, then nearest in date. Choosing one only ever adds: a row already paying for
another order takes this one beside it, and each order then carries its own
card total as its share of the row, so one payment can be split across
several orders. **Undo** on the matched list is the one way a match is taken
away.

**Ignore: not on a tracked card** takes an order out of the matcher's offer
and out of the waiting count, for an order charged to a card Agentifi does
not track. Matching a row to it takes the ignore back.

From the other side, a bank row's detail in the register has one **Purchase**
section whatever the merchant, listing every order or receipt behind the row
and whose account it came from, and **Purchase behind this row…** in the
row's menu opens a picker over the merchants the wording names (or all of
them for a row whose wording names none), with a filter and a search; the
search stays with those merchants, so a Costco row is never offered an
Amazon order unless the person picks Amazon in the filter.

Prime, Kindle, Audible and other digital purchases are Amazon rows with no
order in these files; they stay unmatched.

### The invoice on the row

A bank row matched to an order carries the order's invoice as a receipt: a
document link of kind `receipt`, role `invoice`, which the transaction's
Attachments panel lists as "From the order" with the merchant and order
number, to open or download. Only a row that paid for the order carries it:
a credit matched to the order gets none, and its detail shows only the order
it came back from, with **Refund of** linking to the purchase it gives back.
The purchase's detail says **Fully refunded** or **Partially refunded**
(`domain.RefundState`) and links each credit. The
receipt is filed whichever comes first, the match or the invoice, and goes
when the match is undone, the order's account is removed or the row is
deleted; the invoice itself stays on its order. `store.ReconcileReceipts` is
the only writer, the same one that files a bill's statement (see
[`bills.md`](bills.md#pulling)): it never touches a file a person attached,
and running it again changes nothing. An invoice a backfill files for an
older order lands on the row already matched to it the same way.

Each pull's notes say what it filed and what is still missing, as one line
among the import's warnings: "Invoices: 12 invoices filed; 61 orders on file
still without one; Backfill invoices files them" for Amazon, and the same
with purchases for Costco. A pull that filed nothing and leaves nothing
missing says nothing.

## The category check

For a matched row, the category check (see
[`../assistant.md`](../assistant.md#automations)) is given the order: which
account placed it, when, the total, what a gift card paid, how it was
matched, and every item with its cost and its share of the charge. The
payee's history ("Amazon is Shopping") is shown but the items decide. With
shares, the model proposes `split_transaction` with those exact amounts, one
category per item; with one item, `update_transaction`.

A proposed split of a matched row must be faithful to the order: one that
drops items or invents tax or shipping lines is refused. When the split lines
up share for share with the items, each memo is replaced with the item's own
title before the card is stored, because the model paraphrases titles. Only
the names are replaced; the categories stay the model's. The card shows each
part's item, share and category by name, with a picker on each, and a
category a person changes at Accept is recorded as a correction the next
check is shown.

The bank often delivers a charge before its order is on file, so a match made
by a merchant import, a pull or a person's hand fires the space's transaction
automations again for that row. **Suggest categories** on the orders card
(`POST /merchants/{merchant}/suggest-categories`) queues a category
suggestion, in propose mode, for every row an order explains; the register
has the same for any ticked rows. Proposals
arrive on the Assistant page, and nothing changes until one is accepted.
