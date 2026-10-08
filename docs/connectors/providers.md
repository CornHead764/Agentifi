# The bill providers

One section per provider in `domain.Billers`, in catalogue order. How
connecting, pulling and the mailbox work for all of them is in
[`bills.md`](bills.md); how to add one is in
[`adding-a-bill-provider.md`](adding-a-bill-provider.md).

| provider | kind | browser | second factor | statement | keepalive |
|---|---|---|---|---|---|
| Alliant Energy | API | none (calls run in a page) | none | PDF | token refresh |
| Erie Insurance | browser | Chrome | authenticator or text, every sign-in | PDF | 7 days |
| Spectrum | browser | Chrome | text or e-mail | PDF | 7 days |
| We Energies | browser | Chrome | none | PDF | daily |
| T-Mobile | browser | Chrome | authenticator | PDF | 7 days |
| Apple | e-mail | none | none | none | none |
| rsync.net | browser | Chrome | optional, not supported | PDF receipt | 7 days |
| Our Community Connect | browser | Camoufox | interstitial check | PDF | signs in every pull |
| Northwestern Mutual | browser | Chrome | authenticator | none | 7 days |
| TruGreen | browser | Chrome | none | PDF | 7 days |
| MyChart | browser | Chrome | e-mail or text, unless the device is trusted | PDF, from its viewer, or the viewer printed | signs in every pull |
| Emailed bills | e-mail | none | none | the mail's PDF, or the mail printed | none |

Most providers also have a **bill-mail parser** in
`backend/internal/billmail/`, so a bill that arrives by mail lands on the same
billed account and due date a pull would use.

## Alliant Energy

**Signs into** the customer portal's JSON service at
`myaccount.alliantenergy.com` (a SmartCMobile platform). Username and
password; no second factor. The sign-in is
`POST /UsermanagementAPI/api/1/Login/auth`, and the session is a bearer token
plus the refresh token that minted it (kind `alliant-token`). The `uid`
header is the platform's channel for the call: 1 on the login, 2 after it.

**Browser.** The service answers a Go client with an HTML 403, so the module
declares `BrowserOrigin`: its calls run inside a page on the portal's origin,
sitting on a light static document. The code is otherwise plain HTTP.

**Reads.**

- Billed accounts from `GET /Services/api/1/Addresses/User/{uuid}`; the
  external id is premise and account.
- The current bill from `bill/Current`: its net due date, the upcoming
  autopay date and the remaining balance decide the status.
- Earlier bills from `bill/History`, which carries no due date. They are
  filed paid, with the due date derived from the bill date plus the current
  bill's term (`AlliantEarlierBills`).
- The statement from `bill/GetBillPdf`, which answers a signed link; the link
  is fetched and the bytes checked to start with `%PDF-`.

`AlliantBillFromRow` is the pure reader. Field names are read through lists
of fallbacks because the platform has renamed fields.

**Limits.** The refresh does not revive a session whose access token has
expired, so most scheduled pulls sign in with the kept password. The bill
mail (`noreply@myaccount.alliantenergy.com`) is parsed, one bill per table
row.

## Erie Insurance

**Signs into** the customer account at `erieinsurance.com`, through a SAML
bridge and an F5 gateway to a PingOne DaVinci sign-in widget whose form swaps
itself in place. A code is asked at **every** sign-in, offered as a passkey,
an authenticator app or a text; there is no e-mail option. Only the
authenticator can be answered unattended, from a kept setup key; a passkey
cannot be used.

**Browser.** Chrome, with a kept profile.

**Reads** three web applications behind the one sign-in: the document list,
the billing centre and the account page that carries the online account id.
Calls are same-origin JSON through a `browser.PageFetcher` with the page's
cookies. There is one billed account per in-force policy.

- The three newest invoices (`ErieBillsFromPolicy`).
- The amount is read from the invoice PDF through `billmail`.
- The due date comes from the PDF, then the invoice's own row, then a day
  that falls due in the invoice's cycle in the installment schedule, the
  activity ledger, the term or the policy tile, and on a policy drawn
  automatically the payment in that cycle. Erie publishes no fixed gap
  between an invoice and its due date, so none is assumed: a bill with no
  date is not filed, and a note lists what each source answered as keys and
  types, never values. The PDF can be the only source: the installments call
  can answer an empty list, and neither the invoice row nor the summary tile
  need carry a date. The term's `equityDate` (the day the
  premium paid so far covers the policy to) and `nonPayCancelPendDate` (when
  a cancellation for non-payment would take effect) are not due dates and
  are not read as one; Erie documents neither.
- A statement is asked for as the documents page itself asks for it,
  because a statement post made any other way is answered with a redirect
  into DocumentListWeb's own sign-in. In order:
  - the page's own Angular download, when its scopes are readable: the
    ng-click on the invoice's link or a control in its row or panel,
    evaluated in that control's own scope, then a function on the scope
    holding the invoice's document object, called on it. Only a name that
    says it downloads, or views, opens or prints a document or file, is
    run: the controller's other functions reload or filter the list.
  - the invoice's own link (found by its document handle or id in the link
    or its row, then by the print date and policy in an invoice row), with
    any collapsed groups around it opened first. On this page it is an
    accordion header, so its panel is opened and the control inside it that
    names a file is pressed.
  - the page's own hidden form to the statement path, the one whose inputs
    the invoice's data covers best, filled from the page's document object,
    the document list row, the page's own values and what the module knows,
    in that order. Its target opens a popup, which lands on the identity
    provider's F5 access policy gateway (`/vdesk`). The popup is followed:
    a page of the gateway's that carries only hidden inputs, or a continue
    control, is passed on as it would pass itself on, and a form a person
    would type into is never sent. When the popup brings no file, the form
    is submitted again in the documents page's own window, without its
    target, since the gateway's session may by then hold for it.

  Each way is listened to for a download, a response and a popup, and a
  chain of redirects is followed until it has been quiet for 8 seconds (45
  at the most), so a sign-in round trip that comes back with the file is
  not cut short.
- Failing the press, the statement is posted from the documents page with
  its anti-forgery token. When the page's own fetch is refused, the call is
  repeated without following redirects, and the redirect's `Location`,
  which the browser shows though the page cannot read it, is opened in a
  page of its own (`Page.Bytes`); then the post is submitted as the page's
  own form, and the PDF is taken from what the browser saw, an attachment
  arriving as a download (`Page.OnDownload`) rather than a response.
- A note that no way worked says what each way found and brought: whether
  Angular was readable and the names it holds, the accordion's state and the
  controls in its panel, which form input came from where, every redirect in
  the chain, where a popup rests (address, title, headings with digits
  masked, forms, and which of the gateway's own vendor-named session
  cookies are held, other cookies only counted), and how the documents page
  asks for a file (the links naming a document and its forms). It carries tags, attribute, input and function
  names and addresses, never a value. An address is its host and path up to
  the first segment with a digit, and analytics responses are left out.
- The autopay date equals the due date when the policy is enrolled in
  automatic payment. The catalogue does not claim `ReportsAutopay`.

**Limits.** Passkeys, past policies, more than one online account per login
and payments are not read. Because the catalogue lists texts among Erie's
challenges, a login is scheduled unattended only with a kept authenticator
key. The invoice mail (`donotreply-edelivery@erieinsurance.com`) is parsed,
one per policy.

## Spectrum

**Signs into** `spectrum.net`, entering at `spectrum.net/billing`: signed
out it bounces to `id.spectrum.net` carrying the parameters the form needs;
signed in it is the billing page. The bare sign-in form renders nothing
without those parameters. The provider asks for a text or e-mail code; choose
e-mail to the mailbox for unattended pulls.

**Browser.** Chrome, with a kept profile.

**Reads** the GraphQL service at `apis.spectrum.net/selfservice/graph`, with
the bearer the app itself sends, recorded by an init script. Calls run in the
page; the token never leaves the browser. The operations are
`fetchAccountGlobal`, the statement lists, `fetchDigitalBill` and
`FetchStatementPDFEncodedString` for the PDF. The three newest statements are
read (`SpectrumBillFromStatement`).

**Limits.** `autoPayDate` is often empty, so `ReportsAutopay` is false and
the connection's rule supplies the autopay day. Mobile accounts and a second
core account on one login are not read. The statement mail states no due
date, so its parser uses the autopay date as the due date and says so in a
note.

## We Energies

**Signs into** `we-energies.com`, entering at
`/secure/auth/l/acct/summary_accounts`: signed out it redirects through an
F5 gateway to an Azure AD B2C form; signed in it is the account overview.
No second factor. The sign-in offers no "remember this device", so cookies
are pinned and the keepalive is daily. The account area is `/secure/`,
`/AccountSummary/` and `/UpdateAccount/`.

**Browser.** Chrome, with a kept profile.

**Reads** server-rendered pages: the account list from the payment-history
page, the ledger from `PaymentHistory` with bills included, and the statement
from `BillPdf`. The three newest bills are read (`WeEnergiesBillsFromLedger`).
A bill is paid when a payment posts on or after its date, but the account
list's balance outranks that. The autopay date comes from the scheduled
payments table.

**Limits.** A login with several accounts is handled but has no test. The
bill-ready mail parser reads only Amount Due, because budget billing prints
a levelled amount beside the true balance.

## T-Mobile

**Signs into** `t-mobile.com`, entering at `t-mobile.com/account`, which
redirects through an OAuth authorization endpoint to a two-step form
(username, then password) whose buttons carry no `type`. A factor page
follows, offering the authenticator as a radio covered by an empty stretched
label; the authenticator is answered from a kept setup key. The account area
is `t-mobile.com/my-account`, `/bill` and `/account`.

**Browser.** Chrome, with a kept profile.

**Reads** the BriteBill endpoints the billing screens use: `bill-summary`
(the bill list and session data), `bill-dataset` (the autopay due and
scheduled dates, and the autopay status) and `billdetails`, which needs a
bearer recorded by an init script and answers the statement PDF when asked
for `application/pdf` in summary mode. These calls answer only from
`/bill/historical`, and each has its own deadline. The three newest cycles
are read (`TMobileBillFromCycle`, `TMobileBillFromDocument`).

**Limits.** A cycle whose document token is missing is left out rather than
given a guessed due date. The login is one billed account, labelled by its
line count.

## Apple

An **e-mail** provider: nothing to sign in to and nothing to pull. The
parser reads Apple's receipt and invoice mail (from `@email.apple.com` or
`@apple.com`), taking TOTAL and DATE. The charge is taken the day of the
receipt, so that date is both due and autopay date.

## rsync.net

**Signs into** the Account Manager at `rsync.net/am/`. `billing_info.html`
is both the sign-in and the landing address, and the form is served in place
rather than by redirect. Two-factor is offered by the provider as an option
and is not supported by the module. Signed out, the Account Manager answers 403 rather than
redirecting.

**Browser.** Chrome, with a kept profile.

**Reads** the billing page: the Services block ("Next billing" and the price
per period), the amount due, and the transactions table. Payment rows are
filed as paid bills, each dated by its own day as both issue and due date;
refunds and credits are not. Three bills are read. The receipt PDF is
`rsync_receipt.pdf`, and the dashboard supplies the account id.

**Limits.** Only the first filesystem on a login is read.

## Our Community Connect

One product **deployed once per municipality**. The connection names its
deployment (`NeedsSite`), and the one slug is every address the module uses:
the app at `<site>.ourcommunityconnect.com` and the API at
`api.miviewpoint.net/<site>/…`. The deployment is the town's, and each
connection names its own.

**Signs in** with username and password; the form carries a check widget,
which is waited for and never answered, and one that does not clear
stops the sign-in for a person. The session is a Firebase ID token
in the tab's `sessionStorage`, sent as `Authorization`, so no profile keeps
it: every pull signs in with the kept password and there is no keepalive.
Every call runs in the page with that bearer.

**Browser.** Camoufox, with the time zone and locale the image sets. With `CAMOUFOX_URL` unset
every sign-in and pull fails.

**Reads.**

- `UtilityPortalHome`: the utility customers are the billed accounts, and an
  account on autopay has its autopay date set to the due date.
- `GetCustomerSummarizedTransactions`: billing rows only, the newest three
  (`CommunityConnectCycles`). A closed cycle states no due date, so it is
  dated by its bill date and filed paid. An open cycle with a zero balance
  takes its billing row's amount.
- `GetUtilityBill` for the PDF.

**Limits.** The account area is `/home`, `/utility-billing` and
`/accounts-receivable`. Accounts receivable is not read, and neither is the
per-bill itemisation: the total is what the portal states.

## Northwestern Mutual

**Signs into** `northwesternmutual.com/login/`, which lands on
`login.northwesternmutual.com`. A OneTrust cookie banner is declined. The
factor page is at `/mfaverify` and offers an authenticator as an ARIA radio,
answered from a kept setup key. A login that has not set up its security
profile is sent to an enrolment page, which ends the sign-in with a request
to finish that on the provider's site. The signed-in landing is
`plan.northwesternmutual.com/summary`, which is also where the keepalive goes.

The form, the factor page and the code page all sit on the login host, so the
account area is the plan host alone, minus its SAML hand-over and sign-out
(`/saml/`, `/logout`). A plan page opened signed out is sent back to the login
host.

**Browser.** Chrome, with a kept profile.

**Reads** what the plan site's own app fetches. Every page posts its queries
to one address, `api.plan.northwesternmutual.com/graphql`, so a response is
told apart by what it holds and recorded from the browser
(`Recorded.AwaitWhere`), with a 45-second ceiling per page. A 401 or 403 from
that address means the session is gone.

- `/billing` runs `Accounts`; its `data.payments.billingAccounts` are the
  billed accounts, one per `isaNumber`, labelled "Billing Account ****1234"
  with the payment frequency.
- `/wallet/payment-activity` runs `BanksWithTransactions`; its
  `data.wallet.paymentActivity.transactionHistory.transactions` are the
  payments. Only rows whose action is `BILLING_LINK` are billing payments;
  the action's display name ends in the billing account's last four, which
  must match exactly one account.

**Bills** (`NorthwesternMutualBills`).

- Every `PROCESSED` payment is a paid bill dated by its date. Payments to
  one account on one day are one bill of their sum.
- A payment in any other status is not filed; a note names it.
- The billing page's amount due and due date (written "Mar 3, 2027") are
  one open bill. A zero amount files nothing. When the date's label is
  "Scheduled For" (the account shows "Autopay On"), autopay drafts the
  payment that day, so the autopay date is the due date.
- A scheduled payment that has already processed is filed once: the open
  bill is dropped when a processed payment of the same amount on the same
  account lands within five days of its due date.

**Limits.** There is no statement document, only payment activity, so
`HasDocuments` is false: a payment matched to one of these bills names the
bill in its Attachments panel but carries no file. If a draft lands on a different day after a pull
filed the open bill, that open row stays until the next due date supersedes
it. A billing page that never asks within the ceiling is a note, and the
pull answers no bills; an activity page that never asks still files what is
due.

## TruGreen

**Signs into** `trugreen.com/myaccount`, whose form is at
`/my-account/login` and whose signed-in landing is
`/my-account/account-summary`. The account area is `/my-account/` minus the
login, logout, registration and password pages. No second factor is known.

**Browser.** Chrome, with a kept profile.

**Reads** what the portal's own app fetches. Opening `/my-account/my-services`
makes the app post `GetCustomerDetail_ver7` to `api.trugreen.com` with a
customer number it encrypts itself and an `EncKey` header it mints; the same
call made outside the app is refused as an anonymous user. So no call is
made: the response the app receives is recorded from the browser
(`browser.RecordResponses`) and read, with a 45-second ceiling. The customer
numbers come from the `tg_cust` entry the app keeps in `localStorage`.

- One billed account per customer number, labelled with its plan names and
  the masked number. Leading zeros are not part of the key, because storage
  writes the number as a string and the detail may write it as a number.
- The detail's `SalesAgreements[].Invoices` carry every year, not only the
  one the page's selector shows. Every invoice settled (`invoiceOpen` `N`
  with no balance) is filed as its own paid bill, dated by `invoiceDate`
  (`TruGreenBillsFromDetail`). Visits invoiced on one day are charged to the
  card separately, so each invoice is a bill, told apart by `invoiceNum`, or
  by `workOrderRef` when it carries no number. An invoice with neither is
  left out with a note.
- The statement is the work order's invoice,
  `www.trugreen.com/document/<customer>/workorder/<workOrderRef>`, a
  same-origin GET with the page's cookies that answers `application/pdf`.
  Each bill fetches its own invoice's work order.

**No due date.** Neither the detail nor the document list
(`DocumentDueDate` is always null) states one. EasyPay charges the card
shortly after each invoice, but the portal states no date for it. So:

- a settled invoice is dated by its invoice date and filed paid;
- an **open** invoice (flagged `Y`, a balance owed, or neither readable) is
  not filed. A pull says so in a note with its amount and date, because an
  open bill's due date moves a reminder and this one would be a guess;
- `ReportsAutopay` is false and no autopay date is written. The connection's
  rule cannot place one either, because it is counted from a due date.

**Limits.** Only the customer the services page shows is read; a login with
several customer numbers notes the others. Open invoices give no reminder.
Payments and the older "Statement(s)" documents are not read.

## MyChart

A patient portal that every health system runs at its own address, so this is one provider **deployed once per customer**
(`NeedsSite`) whose deployment is a whole address rather than a hostname
label (`SiteAddress`): the connection keeps the portal's root,
`https://<host>/<root>` (usually `/MyChart`), folded from any page of the
portal pasted into the dialog (`domain.SiteAddressOf`). A household with
several health systems adds one connection per portal. Addresses in the
repository are invented (`mychart.examplehealth.example`).

It is an ordinary bill provider that also files receipts (`Medical`): its
statements are filed as bills on the guarantor account they belong to, its
payments are paired with the bank rows that carried them, and the statement
each payment settled is filed on that row as a receipt for a health savings
account (see [bills.md](bills.md#statements-filed-as-receipts)). It bills one
visit at a time, so its accounts are linked to no recurring reminder.

**Why the web pages, not FHIR.** The vendor's public endpoint directory (a
SMART user-access brands bundle) lists organisations, their locations and
their FHIR base URLs, but no patient portal address and no logo, so it
cannot spare a household from pasting the address; it is not used. The
patient-facing FHIR APIs carry no patient statements or payments: `Account`
is the payer side's premium billing, `ExplanationOfBenefit` is a health
plan's claims, and there is no `Invoice`. A patient-facing app also needs a
client id registered with the vendor and its redirect address, which a
self-hosted install at its own address cannot share. The billing pages are
what the household itself sees.

**Signs into** `<root>/Billing/Summary`, a page only an account holder can
see: signed out it sends the browser to the sign-in form, signed in it is
where a pull starts. The account area is the portal's own host under its
root, minus the sign-in, sign-up, recovery and public pages
(`MyChartInside`). The shared draft does the sign-in: a username and password
form, then, on a device it does not trust, a page asking where to send a code
(e-mail or text, the address and number printed masked, sometimes an
authenticator app) with a **Send code** button, and a "trust this device" box
on the code page that the shared reading ticks. The sign-in takes exactly the
way chosen under **Second factor** and stops naming what was offered when the
portal does not offer it. Choosing **Code sent by e-mail** lets a nightly pull
read the code from the mailbox,
which waits for mail from the portal's parent domain
(`mychart.example.org` is mailed from `example.org`). The session lapses
within the hour, so every pull signs in with the kept password; the trusted
device is what keeps the code away.

**Browser.** Chrome, with a kept profile. Developer steers stay on the
portal's own host (`billers.SiteHome`).

**Reads** the rendered pages, by in-page scripts whose selectors and words
are all in `myChartPage`. The billing pages as the vendor's help pages and
health systems' guides show them: a summary of cards, one per guarantor
account ("Guarantor #", "View account" or "View balance details", "View last
statement"); an account's page with tabs such as Overview, Account Details,
Patient Payments and Billing Documents (elsewhere "Statements/Letters" or
"Communications"); under a "Statements" heading the latest statement, its
day drawn as a calendar (month, day and year apart), opened by a bare "View"
or an envelope icon; and the older ones behind "Show all statements" or
"View past statements". Detailed bills sit beside them and are no statement.

- The summary's cards (`myChartSummaryScript`): around each printed account
  number, the widest element printing no other; and around each link to
  `/Billing/Details` outside those, the widest element holding only that
  link. A card never reaches the page's frame: the main region, a header, a
  menu, the page's title or a "skip to content" link. A card that prints
  "Guarantor #" or "Account #" and a number is a billed account
  (`MyChartSubaccounts`); one that prints none is known by the number its
  own page prints, and is left out with a note when that prints none either.
  Its label is "Billing account ****1234" and the card's own name for it:
  the earliest line naming something among the three before the number,
  else the three after, and never a line of the frame.
- Each wanted account's page (`myChartWalk`): the rows it shows, then each
  tab named for statements, letters, documents, communications, payments,
  activity or history (not one that pays, signs up or sets up), each read
  after it shows. On every view, every control that shows more of a list
  ("Show all statements", "View past statements", "Load more", one naming
  statements first) and then every "Next" of a paged list is pressed, on the
  page or in the dialog it opens, until it is gone, disabled, or shows
  nothing new (at most 25 presses an account; one that shows nothing is
  passed over for the next). The rows are those around each statement
  control (the widest element holding only that one) and around each printed
  day (the widest holding only that one day), read from the dialog in front
  or the main region, and a row read twice is one row. A statement control
  is one that says statement, or that says only "View", "Open", "PDF" and
  the like, a day, or nothing, under a heading or tab naming statements;
  never a setting, a detailed or itemised bill, a letter, a receipt, or a
  control that chooses what a list shows (a radio, a label, a filter such as
  "Currently viewing: Payments since last statement"). A row's section is
  its tab, else the heading before it. A row may print its day twice, a
  calendar for a screen reader and the day as the eye sees it; that is one
  day.
- A view that offers to show its list for a period further back than the one
  it opened on (the Payments tab's "Viewing options": "Since last
  statement", "Year to date", "Last year", "Date range") shows each such
  period in turn, "Year to date" then "Last year": the radio is ticked (in a
  collapsed panel too), its "Apply" pressed, and the list read as above. A
  date range, which asks for days, is not chosen. Each period is a line of
  the trail with the rows it added.
- A statement control that is no link (a button, or a link to `#` or a
  script) is pressed once, up to 30 an account, to learn where its file is:
  the window it opens, the file it downloads, the page it goes to (and back),
  or the viewer a dialog shows.

Every page read, after each tab and after each list's presses, is kept on
the pull's trail with a reading snapshot, and each press, each control
opened and each document's way is a line of it (see
[README.md](README.md)); **What the provider showed** on the connection's
card shows it after every update.

**Statements** (`MyChartStatements`): dated by the day labelled statement or
issued, else the first day printed; a calendar's month, day and year, apart
or run together, are one day. Its due date is the one labelled due, else its
issue date, since a statement list prints none. One that owes something is
filed open and is superseded by the next, as the balance carries forward;
one that owes nothing is filed paid. The amount is the one labelled owed, or the row's only amount; a
statement with neither is a note and no bill. Of the rows that tell of one
day's statement, a list's (one day printed, fewest amounts) is filed over an
overview's "last statement" beside the account's balance, with the first
link any of them offers. Charges, insurance and adjustments are kept beside
it as printed, and days labelled service or visit are its period.

**Files** (`FetchDocument`), with the session's cookies and only from the
portal's own account area: the statement's link, kept when it answers a PDF.
One that answers a page is a statement viewer: the file its frame, embed,
object, download link or viewer address names is fetched (one frame deep),
a blob the page made is read in the page, and a viewer that offers none is
printed to PDF (Google Chrome only), so a statement always carries a
file. The trail says which way each one came.

**Payments** (`MyChartPayments`): a dated row under a payments tab or
heading, or one whose words say payment, that prints one day (in any number
of spellings) and one amount. An insurer's payment, an adjustment, a
statement row, and a charge are not payments; a receipt or a period filter
on the row does not make it a statement row. One shown as scheduled,
pending, declined or reversed is a note. Each view (a tab, or a period it
is shown for) lists a payment once: payments are told apart by day, amount
and method and their place among those alike in one view, so a payment
listed by "Since last statement" and "Year to date" is read once and two
alike copays in one view are two. A payment is known by its account, day
and amount (and its place among identical ones that day).

**Labels.** Every pull carries the cards' labels, and an account already on
file whose label is still the provider's own ("Billing account ****1234",
with or without a name after it) takes the fresh one, so a name read wrongly
is put right by the next pull; a label somebody wrote is kept.

**Test coverage.** Covered by tests: the readers and the walk against
invented rows and a scripted page; the whole walk in Google Chrome
against an invented portal (`mychart_live_test.go`: a summary of three cards
behind the portal's frame, one printing no number; a Billing Documents tab
whose past statements are in a dialog that loads more and open in a window;
a Payments tab with each day printed twice, a "Currently viewing" filter, a
collapsed panel of period radios whose Apply swaps the list in by script,
and older payments only under "Year to date" and "Last year"; a paged
Statements/Letters tab; a viewer framing the PDF, a direct PDF and a
statement page printed); the pairing and the receipt against the database. Not
covered by tests: the summary's card and account number, a Documents tab
whose "Show all statements" lists every statement, the Payments tab's words
and controls as the walk's test builds them, a statement link that answers a
page rather than a PDF, whether a portal's Apply swaps the payments list in
place or loads the page again, what a statement's viewer is, the code page's
wording, and how long a trusted device lasts.

**Limits.** Visits and their itemised charges are not read: the statement
PDF carries them. A payment made before any statement (a copay at the desk)
pairs with its bank row but has no statement to file. A payment from an
account the bank feed does not see pairs with nothing, and so does any
payment with a second bank row of the same amount within its window.

## Emailed bills

The catalogue entry (`email-only`) for any company with no connector. The
connection's own name is the company, it has one billed account made with it,
and it is never pulled. Its bills come from a [mail rule](bills.md#mail-rules)
the user writes; the statement is the mail's PDF, or the mail printed to one.
A billed account here can be linked to a card or loan so the issuer's
statement mail fills the card's statement figures.
