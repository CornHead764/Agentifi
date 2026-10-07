package billers

import (
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// T-Mobile reads the BriteBill microapp behind t-mobile.com/bill from the kept
// browser profile. See docs/connectors/providers.md.
//
// The sign-in is the shared one in draft.go, which handles this site's quirks:
// the only `button[type="submit"]` is an invisible "OK" (so SubmitRank ranks a
// control's words above its type), and the factor page's radios sit under
// empty labels, the authenticator one named only by its `aria-label`.
//
// The billing screens draw themselves from three same-origin POSTs:
// `bill-summary` and `bill-dataset` need cookies only, `billdetails` needs a
// bearer. Two facts shape the reader:
//
//   - The endpoints answer only from a `/bill/*` page whose app has already
//     used them. Called too early, or from the dashboard, they hang with no
//     status rather than refusing, so the reader waits for the app's own first
//     call and every call it makes is under a timeout.
//   - The billing endpoints accept only the token the signed-in app holds, so
//     the module makes its calls from inside the page; the token never
//     leaves the browser. An init script records `accessTokenSAAS` as the app
//     receives it, because a second one minted by `getSessionData`, or the
//     app's own replayed, is refused with 401. The hook is on XHR and
//     installed at the context, because the microapp calls from a `src`-less
//     iframe that a hook on `window` after load would miss.

const (
	tmobileHome   = "https://www.t-mobile.com"
	tmobileSignIn = tmobileHome + "/account"
	// The historical bills page rather than /bill/summary: it draws the whole
	// cycle list.
	tmobileBilling = tmobileHome + "/bill/historical"

	tmobileSummaryURL = tmobileHome + "/self-service-pub/v1/bill-summary"
	tmobileDatasetURL = tmobileHome + "/self-service-pub/v1/bill-dataset"
	tmobileDetailsURL = tmobileHome + "/self-service-britebill/billing/v2/billdetails"

	tmobileStatementsToRead = 3

	// A signed-in billing page draws itself in a second or two; this is the
	// ceiling before the answer is "not signed in", and it must exist because
	// the page hangs rather than refusing.
	tmobileSessionWait = 45 * time.Second
)

// tmobileSessionHook catches the answer to the app's first `bill-summary` call,
// `action=getSessionData`, which carries the account number, puid and token
// `billdetails` needs. It keeps them on `window.top` because the microapp calls
// from a same-origin iframe with its own window. Every access is wrapped: a
// hook that throws inside the app's XHR breaks the page. The token leaves this
// window only as the header of a call made in it.
const tmobileSessionHook = `(() => {
  const held = () => { try { return window.top || window; } catch { return window; } };
  const keep = (body, text) => {
    try {
      if (!/(^|&)action=getSessionData(&|$)/.test(String(body == null ? '' : body))) return;
      const session = JSON.parse(text).sessionData;
      if (!session) return;
      held().__agentifiTMobile = {
        ban: String(session.BAN || ''),
        puid: String(session.PUID || ''),
        token: String(session.accessTokenSAAS || ''),
      };
    } catch {}
  };
  const open = XMLHttpRequest.prototype.open;
  XMLHttpRequest.prototype.open = function (method, url) {
    try { this.__agentifiURL = String(url || ''); } catch {}
    return open.apply(this, arguments);
  };
  const send = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.send = function (body) {
    try {
      if (String(this.__agentifiURL || '').indexOf('/self-service-pub/v1/bill-summary') >= 0) {
        this.addEventListener('load', () => keep(body, this.responseText));
      }
    } catch {}
    return send.apply(this, arguments);
  };
})();`

const tmobileHasSession = `() => Boolean(window.__agentifiTMobile)`

// tmobileDetailsCall reads the puid and ban off the window rather than from Go
// for the same reason as the token: what the hook caught stays in the page.
var tmobileDetailsCall = browser.PageCallScript(`
  const kept = window.__agentifiTMobile;
  if (!kept || !kept.token) return null;
  return {
    headers: { authorization: 'Bearer ' + kept.token, 'x-auth-originator': kept.token },
    body: { puid: kept.puid, ban: kept.ban, documentId: arg.extra.documentId },
  };`)

const tmobileBillListBody = "action=getBillList&isBillCycleGroupByYear=true"

// tmobileAccountArea also matches `/account`, the sign-in entry; a form wins
// over the pattern in StateOf, which keeps the signed-out page from reading as
// signed in. `account.t-mobile.com/home/dashboard` redirects to `/my-account`.
var tmobileAccountArea = regexp.MustCompile(`(?i)t-mobile\.com/(?:my-account|bill|account)(?:/|$)`)

type TMobile struct {
	Draft
}

func NewTMobile() *TMobile {
	return &TMobile{Draft{
		BillerID: domain.BillerTMobile,
		Home:     tmobileHome,
		// `/account` redirects to a sign-in form carrying the redirect_uri; the
		// bare form renders without it but has nowhere to land.
		SignIn:      tmobileSignIn,
		Landing:     tmobileSignIn,
		AccountArea: URLMatches(tmobileAccountArea),
		AccountPage: tmobileBilling,
	}}
}

func tmobileField(row map[string]any, path ...string) any { return field(row, path...) }

// billingPage answers whether a token was caught, which is not whether the
// session is signed in: only one of the three endpoints needs a token, so a
// missed token must not become "sign in again". The first call that comes back
// settles the session: a refusal means sign in again, and a call that never
// answers means the page never drew.
func (t *TMobile) billingPage(call Call) bool {
	page := call.Page
	if err := page.AddInitScript(tmobileSessionHook); err != nil {
		call.Notes.Tracef("the session hook could not be installed: %v", err)
		return false
	}
	// The navigation's own error is not fatal: a portal that redirects mid-load
	// still leaves the browser somewhere, and where it stands is the answer.
	_ = page.Goto(tmobileBilling)
	return page.WaitForFunction(tmobileHasSession, tmobileSessionWait) == nil
}

// lapsed treats a hang as a sign-in owed: a lapsed session and a caller the
// edge has taken against look alike.
func (t *TMobile) lapsed(call Call, what string, found []Bill) Pull {
	call.Notes.Addf(
		"T-Mobile never answered %s at %s — the billing page hangs rather than refusing "+
			"when it will not serve this browser, so a sign-in is what settles it",
		what, browser.WithoutQuery(call.Page.URL()))
	return t.SignInAgain(found)
}

func (t *TMobile) summary(call Call, body string) (PageAnswer, error) {
	return AskPage(call, plainPageCall, browser.PageCall{
		URL: tmobileSummaryURL, Method: "POST", Body: body,
		Headers: map[string]string{"content-type": "application/x-www-form-urlencoded; charset=UTF-8"},
	})
}

func (t *TMobile) dataset(call Call) (PageAnswer, error) {
	return AskPage(call, plainPageCall, browser.PageCall{
		URL: tmobileDatasetURL, Method: "POST", Body: map[string]any{},
		Headers: map[string]string{"content-type": "application/json", "accept": "application/json"},
	})
}

func (t *TMobile) details(call Call, documentID, mode string, pdf bool) (PageAnswer, error) {
	accept := "application/json"
	if pdf {
		accept = "application/pdf"
	}
	read := browser.ReadJSON
	if pdf {
		read = browser.ReadBytes
	}
	return AskPage(call, tmobileDetailsCall, browser.PageCall{
		URL: tmobileDetailsURL, Method: "POST", Read: read,
		Headers: map[string]string{"content-type": "application/json", "accept": accept, "mode": mode},
		Extra:   map[string]any{"documentId": documentID},
	})
}

// Subaccounts is one billed account, the BAN, however many lines are on it.
func (t *TMobile) Subaccounts(call Call) ([]Subaccount, error) {
	if call.Page == nil {
		return nil, nil
	}
	// The walk needs no token — only the page, with its start-up request answered.
	t.billingPage(call)
	answer, err := t.dataset(call)
	if err != nil {
		return nil, err
	}
	if answer.Refused() {
		return nil, ErrNeedsSignIn
	}
	if answer.TimedOut {
		call.Notes.Addf("T-Mobile never answered the account walk at %s",
			browser.WithoutQuery(call.Page.URL()))
		return nil, ErrNeedsSignIn
	}
	set := answer.Object("briteBillDataSet")
	if set == nil {
		call.Notes.Addf("the account walk answered HTTP %d: %s", answer.Status, answer.Excerpt)
		return nil, nil
	}
	found := TMobileSubaccountsFromDataset(set)
	if len(found) == 0 {
		call.Notes.Addf("T-Mobile's billing data named no account number")
		return nil, nil
	}
	call.Notes.Addf("T-Mobile lists %d billed account%s", len(found), plural(len(found)))
	return found, nil
}

func (t *TMobile) FetchBills(call Call) (Pull, error) {
	if call.Page == nil {
		return t.NoPage(), nil
	}
	// A token makes the older statements readable; without one the current
	// cycle still is, and that is the statement a household is waiting on.
	withToken := t.billingPage(call)

	listed, err := t.summary(call, tmobileBillListBody)
	if err != nil {
		return Pull{}, err
	}
	if listed.Refused() {
		return Pull{NeedsSignIn: true, Reason: "T-Mobile refused the kept session"}, nil
	}
	if listed.TimedOut {
		return t.lapsed(call, "its statement list", nil), nil
	}
	groups := listed.Rows("getBillList")
	if groups == nil {
		call.Notes.Addf("the statement list answered HTTP %d: %s; no statements were read",
			listed.Status, listed.Excerpt)
		return Pull{}, nil
	}

	owed, err := t.dataset(call)
	if err != nil {
		return Pull{}, err
	}
	if owed.Refused() {
		return Pull{NeedsSignIn: true, Reason: "T-Mobile refused the kept session"}, nil
	}
	if owed.TimedOut {
		return t.lapsed(call, "what the account owes", nil), nil
	}
	set := owed.Object("briteBillDataSet")
	if set == nil {
		call.Notes.Addf("what is owed answered HTTP %d: %s; without it no cycle has a due date, so no statements were read",
			owed.Status, owed.Excerpt)
		return Pull{}, nil
	}

	cycles := tmobileCycles(groups)
	listedCount := len(cycles)
	if len(cycles) > tmobileStatementsToRead {
		cycles = cycles[:tmobileStatementsToRead]
	}

	// The BAN is on both answers and is the same number; a pull that has
	// neither has nothing to file a bill against, because the billed account is
	// what a connection's statements hang off.
	ban := cmp.Or(Text(set["accountNumber"]), Text(listed.At("accountNumber")))
	if ban == "" {
		call.Notes.Addf("T-Mobile's billing data named no account number; its statements are left out")
		return Pull{}, nil
	}

	var bills []Bill
	unread := 0
	// A missing token is the one cause worth naming: it tells a broken portal
	// from a page that was never handed what it needed.
	noToken := !withToken
	for index, cycle := range cycles {
		at := TMobileCycleAt{
			BAN:        ban,
			Newest:     index == 0,
			BalanceDue: tmobileField(set, "arBalance", "balanceDue"),
			Today:      call.At(),
		}
		var bill Bill
		var ok bool
		if at.Newest {
			// Only the newest cycle is the one the dataset is talking about:
			// `autoPay.dueDate` is the current cycle's due date and nothing
			// else's. Its statement is asked for only for the day it was cut,
			// so a statement that will not answer costs the bill that date and
			// nothing else.
			if withToken {
				at.Issued = t.newestIssued(call, cycle, &noToken)
			}
			bill, ok = TMobileBillFromCycle(cycle, set, at, call.Notes)
		} else if !withToken {
			unread++
			continue
		} else {
			answer, err := t.details(call, Text(cycle["documentId"]), "reduced", false)
			if err != nil {
				return Pull{}, err
			}
			if answer.Refused() {
				return Pull{Bills: bills, NeedsSignIn: true,
					Reason: "T-Mobile refused the kept session"}, nil
			}
			// The cycle is left out rather than given a due date inferred from
			// its end: a reminder would fire on a day nobody is billed.
			if answer.NoToken {
				noToken = true
			}
			if answer.Status != 200 || answer.JSON == nil {
				unread++
				continue
			}
			bill, ok = TMobileBillFromDocument(cycle, answer.JSON, at, call.Notes)
		}
		if !ok {
			continue
		}
		if len(call.Subaccounts) > 0 && !call.Wanted(bill.Subaccount) {
			continue
		}
		bills = append(bills, bill)
	}
	if unread > 0 {
		why := "the statements themselves could not be asked for"
		if noToken {
			why = "the page would not let them be read"
			call.Notes.Trace("the statements were not asked for: this page was handed no session token")
		}
		call.Notes.Addf(
			"T-Mobile lists %d statement%s and only the current cycle states a due date, because %s; "+
				"%d older statement%s is left out rather than carrying a due date nobody stated",
			listedCount, plural(listedCount), why, unread, plural(unread))
	}
	call.Notes.Addf("T-Mobile answered %d statement%s of the %d it lists",
		len(bills), plural(len(bills)), listedCount)
	return Pull{Bills: bills}, nil
}

func (t *TMobile) newestIssued(call Call, cycle map[string]any, noToken *bool) string {
	answer, err := t.details(call, Text(cycle["documentId"]), "reduced", false)
	if err != nil {
		call.Notes.Addf("the current T-Mobile statement could not be asked for its issue date: %v", err)
		return ""
	}
	if answer.NoToken {
		*noToken = true
		call.Notes.Addf("the current T-Mobile statement could not be asked for its issue date")
		call.Notes.Tracef("the current T-Mobile statement was not asked for its issue date: " +
			"this page was handed no session token")
		return ""
	}
	issued := ""
	if answer.Status == 200 {
		issued = ISODate(answer.At("issue_date"))
	}
	if issued == "" {
		call.Notes.Addf("the current T-Mobile statement answered HTTP %d with no issue date; the bill carries none",
			answer.Status)
	}
	return issued
}

func (t *TMobile) FetchDocument(call Call, bill Bill) (*Document, error) {
	raw := rawOf[tmobileRaw](bill)
	if raw.DocumentID == "" || call.Page == nil {
		return nil, nil
	}
	answer, err := t.details(call, raw.DocumentID, "summary", true)
	if err != nil {
		call.Notes.Addf("the statement PDF for %s could not be asked for: %v", bill.ExternalID, err)
		return nil, nil
	}
	if answer.NoToken {
		call.Notes.Addf("the statement PDF for %s could not be fetched", bill.ExternalID)
		call.Notes.Tracef("the statement PDF for %s was not asked for: this page was handed no session token",
			bill.ExternalID)
		return nil, nil
	}
	if answer.Status != 200 {
		call.Notes.Addf("the statement PDF for %s answered HTTP %d: %s",
			bill.ExternalID, answer.Status, cmp.Or(answer.Excerpt, "nothing"))
		return nil, nil
	}
	body, ok := decodedPDF(call, "the statement PDF for "+bill.ExternalID, answer.Base64)
	if !ok {
		return nil, nil
	}
	return pdfDocument(body, statementFilename("tmobile", bill,
		cmp.Or(raw.IssuedOn, bill.DueOn, "statement"))), nil
}

type TMobileCycleAt struct {
	// BAN is the billed account every cycle under this login belongs to.
	BAN string
	// Newest says this is the cycle at the top of the list — the one the
	// dataset's own figures are about.
	Newest     bool
	BalanceDue any
	// Issued is the newest cycle's issue date, from its own statement where
	// that answered.
	Issued string
	Today  time.Time
}

type tmobileRaw struct {
	DocumentID  string `json:"document_id"`
	StatementID string `json:"statement_id"`
	IssuedOn    string `json:"issued_on"`
}

// TMobileBillFromCycle is the newest cycle as a bill, without a token.
// `autoPay.dueDate` is the current cycle's due date whether or not autopay is
// on; `scheduledDate` is carried only when `easyPayStatus` says autopay is on.
// The amount falls back to the cycle's `currentCharges`, which arrives
// dollar-prefixed.
//
// The cycle list states no issue date; the bill carries the statement's own
// (`at.Issued`) or none, never one inferred from the cycle's end.
func TMobileBillFromCycle(
	cycle map[string]any, dataset map[string]any, at TMobileCycleAt, notes *Notes,
) (Bill, bool) {
	if cycle == nil || dataset == nil {
		return Bill{}, false
	}
	autopay, _ := dataset["autoPay"].(map[string]any)
	due := ISODate(autopay["dueDate"])
	scheduled := ""
	if Flag(autopay["easyPayStatus"]) {
		scheduled = ISODate(autopay["scheduledDate"])
	}
	stated := tmobileField(dataset, "currentBillCharges", "currentBillDueAmount")
	amount, priced := Amount(stated)
	if !priced {
		stated = cycle["currentCharges"]
		amount, priced = Amount(stated)
	}
	return tmobileBill(cycle, at, notes, tmobileRead{
		Due:      due,
		Issued:   at.Issued,
		Autopay:  scheduled,
		Amount:   amount,
		Priced:   priced,
		Amounted: jsonish(stated),
		Start:    ISODate(cycle["startTime"]),
		End:      ISODate(cycle["endTime"]),
		Named:    "cycle",
	})
}

// TMobileBillFromDocument is an older cycle as a bill: `billdetails`, which
// needs the token, is the only place its due date is written. The figure is
// `wamountDue.amount_ex_tax`, with the cycle's `currentCharges` behind it.
func TMobileBillFromDocument(
	cycle map[string]any, document map[string]any, at TMobileCycleAt, notes *Notes,
) (Bill, bool) {
	if cycle == nil || document == nil {
		return Bill{}, false
	}
	stated := tmobileField(document, "wamountDue", "amount_ex_tax")
	amount, priced := Amount(stated)
	if !priced {
		stated = cycle["currentCharges"]
		amount, priced = Amount(stated)
	}
	return tmobileBill(cycle, at, notes, tmobileRead{
		Due:      ISODate(document["due_date"]),
		Issued:   ISODate(document["issue_date"]),
		Amount:   amount,
		Priced:   priced,
		Amounted: jsonish(stated),
		Start:    cmp.Or(ISODate(document["bill_period_from"]), ISODate(cycle["startTime"])),
		End:      cmp.Or(ISODate(document["bill_period_to"]), ISODate(cycle["endTime"])),
		Named:    "statement",
	})
}

type tmobileRead struct {
	Due     string
	Issued  string
	Autopay string
	Amount  domain.Money
	Priced  bool
	// Amounted is what the provider actually sent where the amount should have
	// been — the last figure the reader tried — for the note a figure that
	// will not read writes.
	Amounted string
	Start    string
	End      string
	Named    string
}

// tmobileBill holds the status rule. The newest cycle is open until its due
// date has passed with nothing left owed; every older one is paid, since a
// superseded cycle is not owed twice. A newest cycle whose balance will not
// read stays open with a note.
//
// A cycle with no due date is left out with a note rather than given the end
// of its period, which would fire a reminder on the wrong day.
func tmobileBill(cycle map[string]any, at TMobileCycleAt, notes *Notes, read tmobileRead) (Bill, bool) {
	if !read.Priced {
		notes.Addf("a T-Mobile %s carried no readable amount (%s); it is left out",
			read.Named, read.Amounted)
		return Bill{}, false
	}
	if read.Due == "" {
		notes.Addf("a T-Mobile %s carried no due date; it is left out rather than billed on a guess",
			read.Named)
		return Bill{}, false
	}

	balance, readable := Amount(at.BalanceDue)
	stillOwed := !readable || Owes(balance)
	if at.Newest && !readable {
		notes.Addf("T-Mobile's balance due did not read (%s); the current %s is kept open",
			jsonish(at.BalanceDue), read.Named)
	}
	dueAhead := read.Due >= at.Today.Format("2006-01-02")

	status := "Paid"
	if at.Newest && (stillOwed || dueAhead) {
		status = "Open"
	}

	statementID := Text(cycle["statementId"])
	documentID := Text(cycle["documentId"])
	raw, _ := json.Marshal(tmobileRaw{
		DocumentID: documentID, StatementID: statementID, IssuedOn: read.Issued,
	})
	return Bill{
		Subaccount:  at.BAN,
		ExternalID:  at.BAN + ":" + cmp.Or(statementID, documentID, read.Start, "undated"),
		IssuedOn:    read.Issued,
		DueOn:       read.Due,
		AmountDue:   read.Amount,
		Currency:    "USD",
		PeriodStart: read.Start,
		PeriodEnd:   read.End,
		AutopayOn:   read.Autopay,
		Status:      status,
		Raw:         raw,
	}, true
}

// TMobileSubaccountsFromDataset labels the account by a line count ("3
// lines"); an empty line list shows as Mobile rather than "0 lines".
func TMobileSubaccountsFromDataset(dataset map[string]any) []Subaccount {
	ban := Text(dataset["accountNumber"])
	if ban == "" {
		return nil
	}
	label := "Mobile"
	if lines, ok := dataset["accountLinesInfo"].([]any); ok && len(lines) > 0 {
		label = fmt.Sprintf("%d line%s", len(lines), plural(len(lines)))
	}
	return []Subaccount{{ExternalID: ban, Label: label, MaskedNumber: domain.MaskAccount(ban)}}
}

// tmobileCycles flattens the year groups, newest first: in January the newest
// three statements span two groups. The cycle start arrives as
// `2026-09-15T00:00:00` and so sorts as text.
func tmobileCycles(groups []map[string]any) []map[string]any {
	var cycles []map[string]any
	for _, group := range groups {
		list, ok := group["bills"].([]any)
		if !ok {
			continue
		}
		for _, one := range list {
			if cycle, ok := one.(map[string]any); ok {
				cycles = append(cycles, cycle)
			}
		}
	}
	sort.SliceStable(cycles, func(a, b int) bool {
		return Text(cycles[a]["startTime"]) > Text(cycles[b]["startTime"])
	})
	return cycles
}
