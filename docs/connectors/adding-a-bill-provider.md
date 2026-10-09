# Adding a bill provider

This is the path from "my provider is not in the list" to a connector that
can be trusted to run unattended. Read [`bills.md`](bills.md) first; this
page assumes you know what a connection, a billed account and a pull are.
[`providers.md`](providers.md) describes each shipped provider and is where
the new one's section goes.

Two rules hold for every connector:

- **A module declares behaviour, never facts.** The provider's name, access
  path, statements, autopay reporting and keepalive live in the catalogue
  only. A second copy in a module would drift.
- **A figure a module cannot read is a note, never a zero.** A bill reported
  as nothing owed tells the user they owe nothing.
- **A note is for the household; a trace is for the log.** `call.Notes.Addf`
  is read on a settings card and in the toast after a pull, so it says what
  the person gets or misses in plain words. Which token a refresh returned,
  which cookies were pinned or what a query asked is `call.Notes.Tracef`.

## 1. Choose the kind of connector

| kind | `Access` | what it costs | when it is right |
|---|---|---|---|
| e-mail | `AccessEmail` | a parser in `internal/billmail/`, or a user's mail rule; no session, no profile, no schedule | the provider mails the bill and its sign-in adds nothing |
| API | `AccessAPI` | reading the portal's network traffic; no browser at pull time | the portal is a single-page app over a JSON service that answers a plain HTTP call |
| browser | `AccessBrowser` | a browser profile per connection, a minute per pull, a kept password | everything else with a sign-in |

Take the cheapest kind that answers the question, and check the mailbox
before the portal. A company whose bill only needs reading from mail needs no
code at all: the user files it with a mail rule on an **Emailed bills**
connection. A card issuer is not a separate kind: its statement e-mail, filed
by a mail rule onto a billed account linked to the card, writes the card's
statement figures.

An API connector is available only if the portal's own service answers a call
made from this process. A service that answers only from a page on its own
origin can still be an API connector through `BrowserOrigin` (section 5).

Two things decide whether a browser connector can run **unattended**:

- **The second factor.** A provider that texts or asks for a push approval at
  every sign-in cannot be pulled at night, and the scheduler skips it unless
  the connection has its own hour or the login's second factor is an
  authenticator with a kept key or a mailed code
  (`domain.Biller.NeedsAPersonForACode`, `domain.SecondFactor.Unattended`).
  A passkey cannot be carried into the server's browser at all.
- **"Remember this device".** A provider that offers none usually dates no
  cookies. `browser.PinSessionCookies` writes every undated cookie back with a
  30-day expiry, and `KeepaliveDays` decides how often the profile is opened
  without pulling. Such a provider usually needs a daily keepalive.

## 2. Declare it in the catalogue

`backend/internal/domain/bills.go` is the only place a provider's facts are
written: an id constant and an entry in `var Billers`.

```go
BillerYourProvider BillerID = "your-provider"
```

```go
{
    // Why this provider raises what it raises, and where that was read.
    ID: BillerYourProvider, Name: "Your Provider", Access: AccessBrowser,
    Home: "https://www.example.test", HasDocuments: false, ReportsAutopay: false,
    Challenges: []BillChallengeKind{ChallengeTOTP}, KeepaliveDays: 7,
},
```

| field | meaning |
|---|---|
| `ID` | the wire value the dialog sends and a connection stores; spell it as the provider's domain does |
| `Name` | what every note, reason and prompt is built from; keep it on the same source line as `ID` |
| `Access` | the kind from section 1 |
| `Home` | where a person signs in, and the fence the developer steers run inside |
| `ReportsAutopay` | the provider states the day the money leaves; false means the connection's rule supplies it. Start false: a field that is empty on every statement is not reported autopay |
| `HasDocuments` | the module can fetch a statement. A pull calls `FetchDocument` only when this is true, so it goes true in the change that writes `FetchDocument`; `TestAModuleWithAStatementReaderSaysSo` holds the two together |
| `Challenges` | the second factors the provider is known to raise; empty means nothing known, not nothing asked |
| `KeepaliveDays` | how often the kept session is touched; 0 for a provider that signs in every pull |
| `NeedsSite` | one deployment per customer (section 4) |
| `SiteAddress` | that deployment is a whole web address, not a hostname label (section 4) |
| `Medical` | its statements are receipts filed on the payments that settled them; its pull answers payments too ([bills.md](bills.md#statements-filed-as-receipts)) |

Then three more edits, all required:

1. `backend/internal/billers/registry.go`: add the constructor to `New()`.
   Display order comes from the catalogue.
2. `frontend/src/lib/billers.ts`: the same entry, in the same order, in the
   `BillerId` union and the `BILLERS` table. `billers.test.ts` parses
   `domain/bills.go` with regular expressions, so the Go literal's field
   order and one-line layout are load-bearing.
3. `backend/internal/billers/registry_test.go`: `providersWithNoModuleYet`
   lists catalogued live providers this build does not carry, and is empty.
   A new `api` or `browser` entry with no module fails the coverage test.

There is no migration. `bill_connections.biller` is validated against the
catalogue in the API layer, so an unknown id is a 422.

## 3. Probe the sign-in

```
agentifi probe-sign-in <url> [--wait 6s]
```

It drives a real Chrome to the address and prints what the shared classifier
makes of it. It needs no database, only the browser, so a container can run
it; `AGENTIFI_BROWSER_HEADFUL=true` shows the window. It prints nothing that
could be a secret: no field value, cookie, storage or request body. A frame's
address loses its query and fragment; the landed address keeps its query,
because that is where an identity provider puts the parameters its form needs.

| line | meaning |
|---|---|
| `asked for`, `redirect`, `landed` | the address, the server's redirects, and where the page ended up; a page that moved itself with JavaScript shows no redirects |
| `state` | the classifier's reading: `email`, `password`, `otp`, `captcha`, `factor`, `signed_in` or `interactive` (a page nobody recognises) |
| `form` | boxes, submits, and whether a username, password or code box and a sign-out link were found |
| `inputs` | every visible input by type |
| `blank` | whether the page reads as empty, which decides whether the classifier waits for it |
| `frames` | the classifier reads only the main frame, so a form in an iframe reads as nothing |
| `shadow` | boxes behind shadow roots, which the classifier does not pierce |
| `controls` | every visible control a fill could press, with its `type="submit"`, disabled state and rank |
| `presses` | which control the fill would press, or that it would fall through to Enter |
| `way in` | visible controls whose whole text is "Sign in" or "Log in", which the classifier follows |

At a page it reads as `factor`, it also prints the menu with each option's
rank.

**The classifier waits for a page only when it reads as completely empty**:
up to 15 seconds for something to paint. An app that has drawn its header and
a spinner but not its form is not empty, so it is read at once as
`interactive`. The probe's second reading after `--wait` shows this: if the
page reads one way at once and another after the wait, a sign-in is racing
it, and the fix is an entry where the form is already on its way.

Things the probe commonly shows, and what they mean:

- **A form with zero submits beside its boxes.** A button with
  `type="button"` is not counted as a submit. The fill still presses it by
  its words, but the bridge and factor readings reason on the submit count.
- **A hidden `type="submit"` and a visible type-less "Next".** The fill ranks
  visible controls only, so the visible one wins (section 5).
- **Dozens of advertising frames and an invisible reCAPTCHA.** Worth knowing
  before a sign-in starts failing for reasons the page does not explain.
- **A page that will not load in Chrome**, or stalls there, or shows an
  interstitial check that does not clear. The provider belongs in Camoufox
  (section 6).

## 4. Choose the entry URL

A provider's public page is not its sign-in page, and a bookmarked form
usually is not either: the form is often the end of an OAuth or SAML
handshake and renders nothing without its parameters. `/login` is a guess;
probe it.

> **Enter at a page only an account holder can see.** Signed out, it bounces
> through to the real form carrying whatever that form needs. Signed in, it
> is a page the pull wants to be on anyway.

A billing or account-summary page usually serves, and the module's sign-in
address and landing address are then one address. That is the same property
stated twice: the address that tells a kept session from a lapsed one is the
one that shows a form to a sign-in.

**The account area is a separate question.** `Draft.AccountArea` is the
pattern for "this address is inside the signed-in site", and it must cover
every host and path prefix the signed-in session lands on. A page recognised
only by the fallback on the words "Sign Out" is recognised by luck. Exclude
the sign-in, sign-out, registration and password-reset paths when they sit
under the same prefix.

Check that the site you enter is the one with the bills. A company's own
domain may carry only a web shop, while its billing lives on a third-party
portal; a sign-in there succeeds and finds no bill.

### One deployment per customer

Some products are deployed once per customer (a municipality, a district, a
co-op), each on its own subdomain, so there is no entry URL to write into the
module.

- **The deployment is configuration on the connection**, not a constant and
  not a second provider. `bill_connections.site` holds it, `NeedsSite` marks
  the catalogue entry, and the API accepts only a hostname label
  (`ValidBillSite`), because it is interpolated into an address a signed-in
  browser visits.
- **The module is aimed, not parameterised.** Implement `billers.SiteModule`
  (`WithSite(site string) Module`) and return a copy with its addresses built.
  Do not widen `BrowserModule`.
- **An unaimed module has no addresses.** Answer `""` rather than a URL
  around an empty slug, and match no account area. The API refuses a
  `NeedsSite` connection with no site with a 400.
- **The deployment is private.** It is usually where the user lives. It
  belongs in the database only, never in a module, fixture, test or document.
  Invent a slug (`exampletown`) wherever an example is needed.
- **A deployment at its own domain is an address.** A product every customer
  hosts on its own domain (MyChart) sets `SiteAddress`: the API keeps the
  site as `domain.SiteAddressOf` folds it (https, the host, the portal's root
  path) and refuses an IP address, a port or a private name, and the module
  validates with the same function in `WithSite`. Such a module also
  implements `billers.SiteHome`, so the developer steers stay on the
  deployment's host rather than the catalogue's `Home`.

## 5. Write the module

One new file in `backend/internal/billers/`, named after the provider. The
contract is in `biller.go`: every module is a `Module`, and then either an
`APIModule` or a `BrowserModule`.

### What is shared and what is yours

Anything more than one portal does is already written. Your file holds only
what is true of your provider.

| shared | where |
|---|---|
| page reading and state, code-channel and enrolment readings | `form.go` |
| the dialog in front of the page, and waiting for the page to answer a press | `internal/browser/agent` |
| the way-in step, the fills, the code answer | `draft.go` |
| identity-provider bridge pages and account hops | `bridge.go` |
| cookie banners (OneTrust, TrustArc, Cookiebot) | `consent.go` |
| submit ranking | `submit.go` |
| "remember this device" | `remember.go` |
| a code drawn one box per character | `segments.go` |
| the factor menu | `factor.go` |
| the failure snapshot | `snapshot.go` |
| calls with reportable bodies, envelopes, dates, amounts, masked numbers, statement names | `shared.go` |
| a call made by the signed-in page itself, under a deadline (`AskPage` over `browser.PageCallScript`) | `pagecall.go` |
| a response the page's own app received, for a call nobody else can make (`browser.RecordResponses`; `Recorded.AwaitWhere` picks one by what it holds) | `internal/browser` |
| minting a code from a kept key | `internal/totp` |
| the browsers, kept profiles, cookie pinning, page fetcher, steers, Camoufox | `internal/browser` |
| the sign-in states, opening Chrome or Camoufox, where calls go, the held browser | `internal/browser/agent` |
| the sign-in loop, sessions, challenges, resuming a pull, keepalive, statement refs | `internal/connector` |

A helper nothing about your provider decides does not live in your file, and
a selector or vocabulary that belongs to one portal does not go in the shared
page reading. There is no shared OAuth layer and there should not be one: an
OAuth redirect in front of a sign-in is something the browser follows, not
something this code implements.

### What a module owes

- **`Subaccounts(call) ([]Subaccount, error)`**: what this login bills, as the
  provider names it. Each becomes a billed account the user can tick; give
  each a label a person recognises.
- **`FetchBills(call) (Pull, error)`**: bills; or `NeedsSignIn` with a reason;
  or neither and a note.
- **`FetchDocument(call, bill) (*Document, error)`**: one statement's bytes,
  or `nil`. Its error never fails a pull: the figures are what a reminder
  needs. A file served as an attachment reaches the page as a download, not
  a response: `Page.OnDownload` sees it, and `Page.Bytes` opens an address in
  a page of its own and answers the response, the PDF viewer's file or the
  download alike.

`Call` carries the context, the kept session, the `browser.Page` this pull
opened (nil at an API provider), a `browser.Fetcher`, the billed accounts
asked for, the site, the notes and an injectable `Now`. `call.Wanted(id)`
says whether a billed account was asked for; `call.At()` is now.

An **API module** adds `SessionKinds`, `Authenticate`, `Refresh` and,
optionally, `BrowserOrigin`, the origin whose page makes the calls when the
service is strict about which client reaches it directly over TLS. `Refresh`
reports whether the session is still good; a pull whose refresh cannot revive
an expired token signs in with the password.

### What the draft gives a browser module

A browser module embeds `Draft` and gets the whole sign-in. It is configured
with `BillerID`, `Home`, `SignIn`, `Landing` and `AccountArea`, plus an
optional `Prompt` for the dialog and an `AccountPage` to open when a sign-in
lands somewhere that is neither a form nor the account area.

| method | what it does |
|---|---|
| `Classify` | reads the page and its state, takes the way in, crosses a bridge page, hops into the account area |
| `FillEmail`, `FillPassword` | declines a known cookie banner (never accepts), fills inside the dialog in front of the page if there is one, ticks "remember this device", presses the best control, waits for the page to differ |
| `Answer` | types a code into the box the page named, the box under the label, or one box per character |
| `ChooseFactor` | reads the factor menu whole, prefers the login's own choice, confirms a radio selection |
| `SignInPrompt`, `AccountHint`, `CaptchaImage` | sensible defaults |
| `NoPage()`, `SignInAgain(found)` | the two pulls every browser module owes: no browser was opened, and the portal stopped answering |

Replace a method only when the portal makes the generic one wrong.

### Which control a fill presses

A fill asks the page for every visible control and ranks them in Go with
`SubmitRank`:

| rank | control | example |
|---|---|---|
| 1 | its whole text is the act: next, continue, submit, proceed, confirm, verify, sign in, sign on, log in, log on (a trailing arrow or full stop allowed) | `Next`, `LOG IN` |
| 2 | it carries `type="submit"`, whatever it says | `<button type="submit">Phone app</button>` |
| 3 | it starts with a rank-1 word | `Sign in to your account` |
| 0 | anything else, never pressed | `Sign up`, `Cancel`, `Forgot password?` |

- An invisible control is not a control, so a hidden submit cannot beat a
  visible "Next".
- A control that is not pressable is waited for, never pressed, because
  widgets enable their buttons on the event the fill raised and disable them
  again while the form validates. Not pressable is disabled, `aria-disabled`,
  or the computed `pointer-events: none` a component library's disabled
  class sets. Right before every click the control is given ten seconds
  (`submitWait`) to be pressable, whatever the ranking read. One that never
  enables is not counted as pressed.
- The click is Playwright's, with its actionability checks and a five-second
  bound. When it times out, its call log decides what happened
  (`browser.ClickLog`, `browser.ClickPerformed`). A log showing the click done
  timed out in the wait for the navigation the click started: the press
  counts, and the round's own wait for the page covers the rest. A click that
  never got that far, at a control the page shows pressable with nothing on
  top of it, is pressed once more with `ForceClickVisible`, past Playwright's
  checks; the pending check has cleared and the target is the one read, so
  nothing the press needs is left to wait on. A covered or disabled control
  is never forced, since a forced click at a cover presses the cover. The
  round's trail line and the notes carry how the press went and the tail of
  Playwright's log, its own step lines only, with elements by tag, id and
  class; the failure sentence a person reads carries neither.
- A provider whose button Playwright never sees as stable sets
  `Draft.PressDirectly` (Our Community Connect's Angular Material button in
  Camoufox): once the pending check has cleared, a control the page shows
  pressable with nothing on top is pressed with `ForceClickVisible` straight
  away, and the trail says it was pressed directly. A control the page does
  not show clear, or a forced click that does not land, takes the ordinary
  press above.
- Enter is the last resort, and a round that reaches it says so in the
  trail.

The ranking is pure over what the page reported, so the probe prints the
same answer the press acts on. Every fill answers a `Step` saying what it
pressed, in the provider's words, and whether the page changed. The engine
gives up after two rounds that left the page unchanged, and the failure names
what was pressed.

### A dialog in front of the form

Every reading and every act is confined to the dialog in front of the page
when there is one (`agent.DialogScopeJS`, marked for selectors by
`scope.go`): an open `<dialog>`, `role="dialog"` or
`alertdialog`, `aria-modal="true"`, or Bootstrap's `.modal.show`. A dialog
not declared modal counts only when it covers most of the controls behind it,
and a cookie banner never counts. Three readings ride on it:

- **A code split one box per character** (four to eight single-character
  boxes side by side) is a code box.
- **A code request carries the page's own sentence** as its prompt, and the
  channel it names (a phone number, an address, an app) is the state's
  method. A kept key is minted only into a box that asks for an
  authenticator code or names no channel.
- **An enrolment ends the sign-in.** A page setting up how the login will be
  verified, with nothing asked yet, fails with a sentence asking the user to
  finish that on the provider's site, because pressing through would choose
  their security settings unattended.

An interstitial check is given 20 seconds to clear; one that does not
clear within the wait stops the sign-in for a person and is reported as
`captcha` with `Blocking` set. A provider whose interstitial does not clear in
Chrome runs in Firefox via Camoufox.

### Which module to copy

| your provider is | copy |
|---|---|
| a JSON service that answers a plain HTTP call | `alliant.go`: a kept bearer, `Authenticate` and `Refresh`, the `{status, data}` envelope, `BrowserOrigin` |
| a single-page app over its own API | `spectrum.go`: an init script records the bearer the app sends, and calls run in the page with it |
| server-rendered markup | `weenergies.go`: in-page scripts read rows into structs, and a pure reader turns them into bills |
| several applications behind one sign-in | `erie.go`: a `browser.PageFetcher` makes same-origin calls with the page's cookies, and a figure the JSON lacks is read from the PDF |
| a portal whose billing pages nobody has seen | `Draft` embedded and nothing overridden (section 9) |

`shared.go` has `Ask` and `AskJSON` for a call with a reportable body,
`ReadEnvelope` and `Pick` for a service that renames fields, and `ISODate`
and `Amount` for the two coercions every bill needs. When `Amount` answers
false, write a note and leave the bill out.

### The pure reader

Every module splits in two. The walk that talks to the provider is thin: open
this page, run that script, make these calls. The half that decides what a
bill *is* is an exported pure function over plain structs, with no page and
no network: `SpectrumBillFromStatement`, `WeEnergiesBillsFromLedger`,
`AlliantBillFromRow`, `TMobileBillFromCycle`, `CommunityConnectCycles`. Put
every judgement there: which row is a bill and which a payment, when a bill
counts as paid, which date is the due date, what the external id is.

**A closed cycle whose due date the portal never states.** A due date on a
`Paid` bill only picks which reminder slot its amount fills; on an `Open`
bill it is a promise, because only an open bill moves a reminder to its due
date and steps the projection on its autopay day (`domain.SlotBill`, then
`domain.OccurrenceDueOn`). So:

- use the provider's own due date wherever it states one;
- when it is stated but not obtainable this pull, leave the cycle out rather
  than guess;
- when it is never stated for a closed cycle, date the cycle by the day the
  portal does state (its bill date, or the day the money moved), file it
  `Paid`, and say in a comment what the date is;
- never guess the due date of an **open** bill: that one moves a reminder.

## 6. Camoufox, for providers that run in Firefox

Some sites accept only a regular desktop browser. For those, Camoufox (a
Firefox build) runs as a Playwright server in its own container
(`tools/camoufox`), driven from Go exactly as Chrome is. A module opts in:

```go
func (m *YourProvider) RunsInFirefox() bool { return true } // browser.FirefoxProvider
```

The engine then opens Camoufox at every step: sign-in, pull and keepalive.
Never one browser for one step and the other for the next: a session carried
between them does not survive. With `CAMOUFOX_URL` unset the provider fails
with `browser.ErrNoFirefox` and never falls back to Chrome. To run one
locally, start `tools/camoufox` with a `CAMOUFOX_WS_PATH` of 24 or more
characters and set `CAMOUFOX_URL=ws://<host>:9333/<that path>`.

Rules for writing against it:

- **Scripts run in an isolated world.** `Evaluate`, `WaitForFunction` and
  init scripts see the DOM and the origin's storage but not the page's own
  JavaScript. A hook on the app's `fetch` patches a copy nobody calls. Read
  what the app stores; making your own `fetch` is fine.
- **No kept profile.** Each run is a fresh context seeded from the sealed
  storage state. A session kept in cookies or `localStorage` survives; one
  kept in `sessionStorage` does not, so the provider signs in every pull and
  its `KeepaliveDays` is 0.
- **No screencast, and no developer live sign-in.** The live sign-in and the
  steers are Chrome's. Camoufox has a screenshot view (about one JPEG a
  second, typed fields covered) that a typed sign-in uses only to park on a
  page check for the person to tick; see
  [`bills.md`](bills.md#a-page-check-only-a-person-can-tick).
- **Bytes cross the world boundary as a data URL.** The page-call script
  (`browser.PageCallScript`) reads bodies with `FileReader`.
- **A bearer the app keeps in storage is the call's `Token`.** A
  `browser.StorageToken` names the stores, the key pattern, the path into the
  entry's JSON (or the longest JWT in it), an expiry and the headers that
  carry it; the script reads it before the call and makes no call without
  one. `browser.StorageTokenHeld` is the same read as a wait condition.
- The image sets a time zone and locale, so the browser reports the
  deployment's own rather than UTC with the C locale; some portals check them
  at sign-in.
- **A check the page may show is waited for, never answered.** In Camoufox
  (`browser.InFirefox`), `billers.AwaitPageCheck` runs before every reading
  of the sign-in page and again before the submit press. It reads the DOM
  for a pending check: a Turnstile token input with no value, a
  `.cf-turnstile` or `[data-sitekey]` container, a frame from
  `challenges.cloudflare.com`, or an Azure Front Door WAF challenge page
  (`/.azwaf/`). A page showing none is given two seconds to draw one; a
  pending check is given 45 seconds to clear. One still pending then parks
  the sign-in for the person to tick in a live view (or, with nobody at the
  sign-in, stops it) and the button is never pressed past it, since the
  portal refuses a form sent without the check's token.
  In Chrome nothing waits. The submit control is read only once the check has cleared, since a
  page holds its button disabled until then.

## 7. The second factor

`Classify` tells apart a password form, a username-only form, a code box, a
picture or interstitial check page, and the **factor page** between the
password and the code that asks which way to verify. The factor page is its
own state because "Text me a code" is not a code box.

**Reading the menu.** One query over buttons, links, `role="button"`,
`role="radio"` and radio inputs, each named as a screen reader would name it
(its `aria-label`, `aria-labelledby`, the label it sits in, the label bound
by `for`, a label beside it, then the nearest text holding no other choice).
If the page has radios, the radios are the menu and the buttons send it.

**Choosing.** `FactorKind` classifies one option's words, reading a phone
call first, then a text, then an e-mail, then an app, because "Call me with a
code from your app" carries every word an app is known by; a push or an
unnamed app is nothing, and only then is a phone named or printed masked
("Send to my phone", "(\*\*\*) \*\*\*-1234") a text. `FactorRank` puts the
authenticator app first and a text second; nothing else is pressed by
default. The login's own second-factor choice arrives as `ChooseFactor`'s
`prefer` argument and is the only way taken when it is set: a menu that does
not offer it presses nothing and the sign-in stops naming both. That is also
the only way an e-mail option is chosen.

**The page.** Besides a question naming the ways, a "Select Method" heading
over buttons, and a bare radio group, a page that only asks where to send a
code ("We need to verify your identity", "Send code") over buttons naming an
e-mail, a text or a masked number is the factor page (MyChart's shape). A
"Send code" is the button that sends a selected radio.

**Taking it.** A radio is clicked through its label wherever it has one
(which also clears the common empty `<label for>` stretched over the input),
then ticked with Playwright's `Check`, then force-ticked; a forced choice is
marked on the trail line. A choice none of the three could take ends the
sign-in: "‹Provider› would not let "‹option›" be chosen". A radio is then
confirmed with the page's own button; a button or link option has already
sent the page and is not pressed again.

**The trail records the menu**, each option's words and kind and which was
taken, before any error. Runs of digits are stripped from option words, so a masked
phone number is never kept.

**A kept key answers unattended.** The user pastes the authenticator setup
key beside the password; it is refused unless it is base32 and the password
is being kept, and it is sealed in the same blob. The engine receives the key
and mints the code when the page asks (`billers.MintedCode`), waiting for
the next window if fewer than five seconds remain.

Everything else parks a challenge: the mailbox gets three minutes, then the
user is notified, and the sign-in stays open for 20 minutes.

## 8. Test with invented fixtures

**The pure reader** gets a table test driven through the exported function.

**The walk** gets a scripted page or fetcher. `browser.StubPage` implements
the whole `Page` interface with each method as a field and records
`Visited`, `Filled`, `Clicked`, `Checked`, `Pressed`, `Scripts` and every
`Evaluate` argument; `Missing` says a box is absent. `scripted` in
`scripted_test.go` answers a list of steps and records what it was asked,
which is why a module takes a `browser.Fetcher` rather than its own HTTP
client.

**The page reading** gets a live test, gated because it drives a real
browser:

```
cd backend && AGENTIFI_BROWSER_TEST=1 go test ./internal/billers/ ./internal/browser/
```

`draft_live_test.go` serves invented pages shaped like real portals from a
local server and drives Chrome at them. A new shape the shared reading has
to learn (a decoy box, a code box named only by its label, a bridge page, a
factor page) gets a page there.

**Fixtures are invented, never captured.** A figure copied from a real portal
is a leak even with no name beside it, because it reconciles with the
figures that had names. Write the fixture to the shape the provider answers
and invent every value. The same rule covers the provider's section in
[`providers.md`](providers.md): shapes, routes and selectors, never figures,
account numbers, addresses or names.

To read the signed-in site while writing the reader, use the owner-only
steer routes described in [`bills.md`](bills.md#developer-steering).

## 9. Land the sign-in before the reader

A module with no reader is a shape the package already has: `Draft` embedded
with the five fields and nothing overridden. It signs in, keeps its profile
and is touched on the keepalive, and a pull answers no bills and a note that
the reader is not written. It never reports a figure nobody read. The order
for a provider nobody has written:

1. Probe it (section 3) and choose the entry (section 4).
2. Declare it (section 2) and land the draft: the catalogue entry with
   `HasDocuments` and `ReportsAutopay` false, the line in `New()`, the
   frontend row, a file whose doc comment says what was observed and what is
   left to write, and its section in [`providers.md`](providers.md).
3. Connect it and let it keep a session for a week or two. That shows
   whether the sign-in works unattended, which no test can.
4. Steer the signed-in session, then write `Subaccounts`, `FetchBills`,
   `FetchDocument` and the pure reader, and turn `HasDocuments` on with
   `FetchDocument`.

## 10. What "done" means

A connector is done when it has earned the right to run unattended:

1. `go build ./... && go vet ./... && gofmt -l .` are clean and
   `go test ./internal/...` passes, including the registry's coverage test.
2. The live page-reading tests pass with `AGENTIFI_BROWSER_TEST=1`.
3. A real connect lands through the typed form and lists billed accounts
   with labels a person recognises.
4. **Update now** files bills whose figures match the portal, with the
   provider's own due date, and the statement opens from the reminder. The
   pull's notes have been read.
5. A reminder pointed at the billed account shows the bill's amount and due
   date, and the projection steps on the autopay day.
6. **A scheduled pull works unattended**, overnight in the ordinary sync
   window, with no browser a person signed in to minutes before. The card
   reads a fresh pull, not "needs sign-in". This is the step that finds a
   token refresh that does not revive an expired session.
7. The keepalive holds: the provider does not forget the device sooner than
   `KeepaliveDays`.
8. The provider's section in [`providers.md`](providers.md) says what it
   signs into and why that entry address, its second factor, what it reads,
   which browser, and what it does not handle.
