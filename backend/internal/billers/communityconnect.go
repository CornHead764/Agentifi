package billers

import (
	"cmp"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Our Community Connect is one product deployed once per municipality. Each
// town has its own subdomain, and the same slug is a path segment on the API's
// separate host:
//
//	https://<site>.ourcommunityconnect.com/     the app
//	https://api.miviewpoint.net/<site>/…        its API
//
// so one configured value (`bill_connections.site`, `NeedsSite`) is every
// address this module uses, and `WithSite` aims a copy at one deployment. The
// site is an account's municipality, so it is never in this repository, a
// fixture or a test. See docs/connectors/providers.md.
//
// The sign-in runs in Firefox via Camoufox and its form carries a page check,
// which is waited for and never answered; the session is a Firebase ID token in the tab's sessionStorage, sent as
// `Authorization`: no profile keeps it, so an unattended pull signs in every
// time with the kept password. Every call is evaluated in the page with that
// bearer; the token never leaves the browser and is never logged, noted or
// returned.
//
// `GetPortalCustomerTransactions` (one bill's itemised lines) is deliberately
// not called: the total is stated, and summing the lines would answer a
// second figure the portal never printed.
//
// A page that yields no token is a sign-in owed, unlike T-Mobile, because here
// every call needs the bearer.

const (
	communityConnectHome   = "https://www.ourcommunityconnect.com"
	communityConnectDomain = "ourcommunityconnect.com"
	// The API's own host, shared by every deployment; the slug is the first
	// segment of the path.
	communityConnectAPI = "https://api.miviewpoint.net"

	communityConnectStatementsToRead = 3

	// A signed-in portal stores its user within a second; this is the ceiling
	// before the answer is "not signed in".
	communityConnectBearerWait = 45 * time.Second
)

// communityConnectToken is the `firebase:authUser:*` entry in the tab's
// sessionStorage; the app's bearer is `"Bearer " + accessToken`. A token within
// a minute of expiring counts as none; the app mints a fresh one on every load.
var communityConnectToken = browser.StorageToken{
	Stores:  []string{"sessionStorage"},
	Key:     "^firebase:authUser:",
	Path:    []string{"stsTokenManager", "accessToken"},
	Expires: []string{"stsTokenManager", "expirationTime"},
	Headers: []string{"authorization"},
	Scheme:  "Bearer ",
}

// communityConnectSettled is a token, or the app having sent this tab to its
// sign-in.
var communityConnectSettled = `() => ` + browser.StorageTokenHeld(communityConnectToken) +
	` || /^\/login/i.test(location.pathname)`

var communityConnectHasToken = `() => ` + browser.StorageTokenHeld(communityConnectToken)

// communityConnectSlug is a hostname label, which is also a path segment. The
// API validates the same at the edge (`ValidBillSite`); this is the module's
// half, because a dot or a slash would reach another host or another path.
var communityConnectSlug = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

type CommunityConnect struct {
	Draft
	site string
}

// NewCommunityConnect is unaimed, and deliberately has no addresses:
// SignInURL and LandingURL answer "" and the account area matches nothing,
// rather than interpolating an empty site into an address that resolves
// nowhere, or somewhere nobody chose. The engine refuses a `NeedsSite`
// connection with no site before it opens a browser.
func NewCommunityConnect() *CommunityConnect {
	return &CommunityConnect{Draft: Draft{
		BillerID: domain.BillerCommunityConnect,
		Home:     communityConnectHome,
		Prompt: "Sign in to Our Community Connect at your own community's address. " +
			"Agentifi waits for the page's check; if it does not clear within the wait, the sign-in stops for you.",
		// Its Sign in button never reads as stable to Playwright in Camoufox,
		// so the ordinary click only waits out its timeout before the forced one.
		PressDirectly: true,
	}}
}

// WithSite answers a copy rather than mutating: the registry holds one module
// per provider and two connections at two municipalities run at the same time.
func (c *CommunityConnect) WithSite(site string) Module {
	site = strings.ToLower(strings.TrimSpace(site))
	if !communityConnectSlug.MatchString(site) {
		return c
	}
	root := "https://" + site + "." + communityConnectDomain
	aimed := &CommunityConnect{Draft: c.Draft, site: site}
	// A single-page app: signed out, every in-app route redirects to `/login`
	// client-side, so there is no protected address that renders a form and
	// the root is the entry.
	aimed.SignIn = root + "/"
	aimed.Landing = root + "/"
	aimed.AccountArea = communityConnectAccountArea(site)
	return aimed
}

// RunsInFirefox: the sign-in runs in Camoufox, and the session lives in one
// tab's sessionStorage, so the pull has to be in the browser that signed in.
func (c *CommunityConnect) RunsInFirefox() bool { return true }

func (c *CommunityConnect) Site() string { return c.site }

// communityConnectAccountArea names the app's in-app paths rather than
// matching the host: the sign-in form is at `/login` and the root serves it to
// a signed-out visitor, so a pattern including either would read a portal
// asking for a password as signed in.
func communityConnectAccountArea(site string) InsideAccount {
	return URLMatches(regexp.MustCompile(
		`(?i)^https://` + regexp.QuoteMeta(site+"."+communityConnectDomain) +
			`/(?:home|utility-billing|accounts-receivable)(?:[/?#]|$)`))
}

// endpoint is on the API's host, not the app's: the app's host answers every
// /<site>/api/… path with its own index.html, a 200 that reads as a portal
// with nothing to say. An unaimed module answers "" here, and every other
// address builds on this one.
func (c *CommunityConnect) endpoint(kind, rest string) string {
	if c.site == "" {
		return ""
	}
	return communityConnectAPI + "/" + c.site + "/" + kind + "/" + rest
}

// portalHomeURL with no customer lists every account on the login.
func (c *CommunityConnect) portalHomeURL(customer string) string {
	address := c.endpoint("api", "UtilityManagementPortal/UtilityPortalHome")
	if address == "" || customer == "" {
		return address
	}
	return address + "?selectedCustomerId=" + url.QueryEscape(customer)
}

// historyURL puts the argument inside the parentheses: these are OData
// functions, and the same call with the account in the query string answers
// 404.
func (c *CommunityConnect) historyURL(customer string) string {
	return c.endpoint("odata",
		"UMCustomerSummarizedTransactions/GetCustomerSummarizedTransactions(customerID="+customer+")")
}

// statementURL takes the ISO timestamp the portal itself stated, not a day
// this module rendered: it is the key the endpoint takes.
func (c *CommunityConnect) statementURL(customer, billDate string) string {
	address := c.endpoint("api", "UtilityManagementPortal/GetUtilityBill")
	if address == "" {
		return ""
	}
	return address + "?customerId=" + url.QueryEscape(customer) +
		"&billDate=" + url.QueryEscape(billDate)
}

// --- the calls -----------------------------------------------------------------

// aimed is a note and no bills rather than a sign-in owed: what is missing is
// a setting, not a session.
func (c *CommunityConnect) aimed(call Call) bool {
	if c.site != "" {
		return true
	}
	call.Notes.Addf(
		"%s is deployed once per municipality and this connection has not been told which "+
			"deployment to use, so there is no address to read", c.Name())
	return false
}

// opened goes to the app and waits for a token or the login page. False is a
// page there is nothing to ask with.
func (c *CommunityConnect) opened(call Call) bool {
	page := call.Page
	// The navigation's own error is not fatal: a portal that redirects mid-load
	// still leaves the browser somewhere, and where it stands is the answer.
	_ = page.Goto(c.Landing)
	_ = page.WaitForFunction(communityConnectSettled, communityConnectBearerWait)
	has, err := page.Evaluate(communityConnectHasToken, nil)
	if signedIn, _ := has.(bool); err != nil || !signedIn {
		call.Notes.Addf("%s holds no signed-in user at %s; the browser is not signed in",
			c.Name(), browser.WithoutQuery(page.URL()))
		return false
	}
	return true
}

func (c *CommunityConnect) get(call Call, address string) (PageAnswer, error) {
	return AskPage(call, plainPageCall, communityConnectRequest(address, "application/json", browser.ReadJSON))
}

func (c *CommunityConnect) getPDF(call Call, address string) (PageAnswer, error) {
	return AskPage(call, plainPageCall, communityConnectRequest(address, "application/pdf", browser.ReadBytes))
}

// communityConnectRequest omits credentials: the API is another host and
// authorises by the bearer alone.
func communityConnectRequest(address, accept string, read browser.PageRead) browser.PageCall {
	token := communityConnectToken
	return browser.PageCall{
		URL: address, Credentials: "omit", Read: read, Token: &token,
		Headers: map[string]string{"accept": accept},
	}
}

func (c *CommunityConnect) refusedSession(found []Bill) Pull {
	return Pull{Bills: found, NeedsSignIn: true, Reason: c.Name() + " refused the kept session"}
}

// lapsed treats a call that timed out as a sign-in owed: a lapsed session and
// an edge refusing this browser look alike, and an empty pull would claim the
// account has no bills.
func (c *CommunityConnect) lapsed(call Call, what string, found []Bill) Pull {
	call.Notes.Addf(
		"%s never answered %s at %s within the time one call is given, so a sign-in is what settles it",
		c.Name(), what, browser.WithoutQuery(call.Page.URL()))
	return c.SignInAgain(found)
}

func (c *CommunityConnect) Subaccounts(call Call) ([]Subaccount, error) {
	if call.Page == nil || !c.aimed(call) {
		return nil, nil
	}
	if !c.opened(call) {
		return nil, ErrNeedsSignIn
	}
	answer, err := c.get(call, c.portalHomeURL(""))
	if err != nil {
		return nil, err
	}
	if answer.Refused() || answer.NoToken {
		return nil, ErrNeedsSignIn
	}
	if answer.TimedOut {
		call.Notes.Addf("%s never answered the account walk at %s",
			c.Name(), browser.WithoutQuery(call.Page.URL()))
		return nil, ErrNeedsSignIn
	}
	if answer.JSON == nil {
		call.Notes.Addf("the account walk answered HTTP %d: %s", answer.Status, answer.Excerpt)
		return nil, nil
	}
	found := CommunityConnectSubaccounts(answer.JSON)
	call.Notes.Addf("%s lists %d billed account%s", c.Name(), len(found), plural(len(found)))
	return found, nil
}

func (c *CommunityConnect) FetchBills(call Call) (Pull, error) {
	if call.Page == nil {
		return c.NoPage(), nil
	}
	if !c.aimed(call) {
		return Pull{}, nil
	}
	if !c.opened(call) {
		return c.SignInAgain(nil), nil
	}

	listed, err := c.get(call, c.portalHomeURL(""))
	if err != nil {
		return Pull{}, err
	}
	if listed.Refused() || listed.NoToken {
		return c.refusedSession(nil), nil
	}
	if listed.TimedOut {
		return c.lapsed(call, "the accounts on this login", nil), nil
	}
	if listed.JSON == nil {
		call.Notes.Addf("the portal home answered HTTP %d: %s", listed.Status, listed.Excerpt)
		return Pull{}, nil
	}
	customers := CommunityConnectSubaccounts(listed.JSON)
	if len(customers) == 0 {
		call.Notes.Addf("%s named no billed account on this login", c.Name())
		return Pull{}, nil
	}

	var bills []Bill
	read := 0
	for _, customer := range customers {
		if len(call.Subaccounts) > 0 && !call.Wanted(customer.ExternalID) {
			continue
		}
		read++

		// Asked again per account: the first answer states whichever account
		// the portal selected by default.
		home, err := c.get(call, c.portalHomeURL(customer.ExternalID))
		if err != nil {
			return Pull{}, err
		}
		if home.Refused() || home.NoToken {
			return c.refusedSession(bills), nil
		}
		if home.TimedOut {
			return c.lapsed(call, "one account's balance", bills), nil
		}
		if home.JSON == nil {
			call.Notes.Addf("one account's balance answered HTTP %d: %s", home.Status, home.Excerpt)
			continue
		}
		open, hasOpen := CommunityConnectOpenBill(customer.ExternalID, home.JSON, call.At(), call.Notes)
		opened := func() []Bill {
			if hasOpen {
				return append(bills, open)
			}
			return bills
		}

		history, err := c.get(call, c.historyURL(customer.ExternalID))
		if err != nil {
			return Pull{}, err
		}
		if history.Refused() || history.NoToken {
			return c.refusedSession(opened()), nil
		}
		if history.TimedOut {
			return c.lapsed(call, "one account's bill history", opened()), nil
		}
		if history.JSON == nil {
			call.Notes.Addf("one account's bill history answered HTTP %d: %s",
				history.Status, history.Excerpt)
			bills = opened()
			continue
		}
		bills = append(bills, CommunityConnectCycles(open, hasOpen,
			CommunityConnectHistory(customer.ExternalID, history.Rows("value"), "", call.Notes))...)
	}
	call.Notes.Addf("%s answered %d statement%s across %d account%s",
		c.Name(), len(bills), plural(len(bills)), read, plural(read))
	return Pull{Bills: bills}, nil
}

func (c *CommunityConnect) FetchDocument(call Call, bill Bill) (*Document, error) {
	raw := rawOf[communityConnectRaw](bill)
	if raw.Customer == "" || raw.BillDate == "" || call.Page == nil {
		return nil, nil
	}
	address := c.statementURL(raw.Customer, raw.BillDate)
	if address == "" {
		return nil, nil
	}
	answer, err := c.getPDF(call, address)
	if err != nil {
		call.Notes.Addf("the statement for %s could not be asked for: %v", bill.ExternalID, err)
		return nil, nil
	}
	if answer.NoToken {
		call.Notes.Addf("the statement for %s could not be fetched", bill.ExternalID)
		call.Notes.Tracef("the statement for %s was not asked for: this page was handed no bearer token",
			bill.ExternalID)
		return nil, nil
	}
	if answer.TimedOut {
		call.Notes.Addf("the statement for %s did not answer in time", bill.ExternalID)
		return nil, nil
	}
	if answer.Status != 200 {
		call.Notes.Addf("the statement for %s answered HTTP %d", bill.ExternalID, answer.Status)
		return nil, nil
	}
	body, ok := decodedPDF(call, "the statement for "+bill.ExternalID, answer.Base64)
	if !ok {
		return nil, nil
	}
	return pdfDocument(body, statementFilename("community-connect", bill,
		cmp.Or(raw.IssuedOn, bill.IssuedOn, bill.DueOn, "statement"))), nil
}

// --- the readers, pure ---------------------------------------------------------

// communityConnectRaw keeps the bill date as the portal stated it, because the
// PDF endpoint takes that timestamp.
type communityConnectRaw struct {
	Customer string `json:"customer"`
	BillDate string `json:"bill_date"`
	IssuedOn string `json:"issued_on"`
}

// CommunityConnectSubaccounts reads `OtherUtilityCustomers`, which includes
// the selected account. A login that lists none still bills the customer the
// portal home is about, so the top-level id is the fallback.
func CommunityConnectSubaccounts(home map[string]any) []Subaccount {
	if home == nil {
		return nil
	}
	var found []Subaccount
	others, _ := home["OtherUtilityCustomers"].([]any)
	for _, one := range others {
		row, ok := one.(map[string]any)
		if !ok {
			continue
		}
		id := Text(row["ID"])
		if id == "" {
			continue
		}
		found = append(found, Subaccount{
			ExternalID:   id,
			Label:        communityConnectLabel(row),
			MaskedNumber: domain.MaskAccount(text(row["CustomerNumber"])),
		})
	}
	if len(found) > 0 {
		return found
	}
	id := Text(home["CustomerID"])
	if id == "" {
		return nil
	}
	return []Subaccount{{
		ExternalID:   id,
		Label:        communityConnectLabel(home),
		MaskedNumber: domain.MaskAccount(text(home["CustomerNumber"])),
	}}
}

// CommunityConnectOpenBill is the current cycle as the portal home states it.
//
// AutopayOn is the due date, and only when the flag is on: the provider states
// no separate day autopay runs, so the money moves on the due date.
//
// A balance that will not read, or a cycle with no due date, is left out with
// a note rather than filed as zero or given a guessed due date, which would
// fire a reminder on a day nobody is billed.
func CommunityConnectOpenBill(
	customer string, home map[string]any, today time.Time, notes *Notes,
) (Bill, bool) {
	if customer == "" || home == nil {
		return Bill{}, false
	}
	amount, read := Amount(home["CurrentBalance"])
	due := ISODate(home["CurrentDueDate"])
	issued := ISODate(home["CurrentBillDate"])
	if !read || due == "" {
		notes.Addf(
			"a Community Connect account stated no readable balance (%s) or no due date (%s); "+
				"its open bill is left out",
			jsonish(home["CurrentBalance"]), jsonish(home["CurrentDueDate"]))
		return Bill{}, false
	}

	autopay := ""
	if Flag(home["AutoPay"]) {
		autopay = due
	}

	status := "Paid"
	if Owes(amount) || due >= today.Format("2006-01-02") {
		status = "Open"
	}
	raw, _ := json.Marshal(communityConnectRaw{
		Customer: customer,
		BillDate: strings.TrimSpace(Text(home["CurrentBillDate"])),
		IssuedOn: issued,
	})
	return Bill{
		Subaccount: customer,
		ExternalID: customer + ":" + cmp.Or(issued, due),
		IssuedOn:   issued,
		DueOn:      due,
		AmountDue:  amount,
		Currency:   "USD",
		PeriodEnd:  issued,
		AutopayOn:  autopay,
		Status:     status,
		Raw:        raw,
	}, true
}

// CommunityConnectHistory is the cycles behind the open one, newest first.
//
// Only the `Billings` rows are bills: the portal states the charge and the
// payment that settled it as two rows, so filing the payments too would double
// every bill. (rsync.net is the opposite: there the payment row is the charge.)
// The open cycle is skipped because the portal home has already stated it.
//
// A closed cycle carries its bill date as its due date, since the portal
// states none and a bill with no due date is dropped downstream. It is filed
// Paid, so nothing is reminded on it.
func CommunityConnectHistory(
	customer string, rows []map[string]any, alreadyFiled string, notes *Notes,
) []Bill {
	if customer == "" {
		return nil
	}
	charges := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(Text(row["Description"])), "Billings") {
			continue
		}
		charges = append(charges, row)
	}
	sort.SliceStable(charges, func(a, b int) bool {
		return ISODate(charges[a]["PeriodEnd"]) > ISODate(charges[b]["PeriodEnd"])
	})
	if len(charges) > communityConnectStatementsToRead {
		charges = charges[:communityConnectStatementsToRead]
	}

	var bills []Bill
	for _, row := range charges {
		date := ISODate(row["PeriodEnd"])
		amount, read := Amount(row["Amount"])
		if date == "" || !read {
			notes.Addf(
				"a Community Connect charge carried no readable amount (%s) or no date (%s); "+
					"it is left out",
				jsonish(row["Amount"]), jsonish(row["PeriodEnd"]))
			continue
		}
		external := customer + ":" + date
		if external == alreadyFiled {
			continue
		}
		raw, _ := json.Marshal(communityConnectRaw{
			Customer: customer,
			BillDate: strings.TrimSpace(Text(row["PeriodEnd"])),
			IssuedOn: date,
		})
		bills = append(bills, Bill{
			Subaccount: customer,
			ExternalID: external,
			IssuedOn:   date,
			DueOn:      date,
			AmountDue:  amount,
			Currency:   "USD",
			PeriodEnd:  date,
			Status:     "Paid",
			Raw:        raw,
		})
	}
	return bills
}

// CommunityConnectCycles is the open cycle and the history as one list, with
// the open cycle filed once.
//
// The portal home states a balance, not a bill: once autopay has run the open
// cycle's CurrentBalance is 0, and the charge is only in the history's
// Billings row. So an open cycle with nothing owed takes its amount from that
// row and keeps the due date and autopay the home stated. A cycle with money
// still owed keeps the balance.
func CommunityConnectCycles(open Bill, hasOpen bool, history []Bill) []Bill {
	if !hasOpen {
		return history
	}
	out := make([]Bill, 0, len(history)+1)
	for _, cycle := range history {
		if cycle.ExternalID != open.ExternalID {
			out = append(out, cycle)
			continue
		}
		if !Owes(open.AmountDue) {
			open.AmountDue = cycle.AmountDue
		}
	}
	return append([]Bill{open}, out...)
}

func communityConnectLabel(row map[string]any) string {
	if address := strings.TrimSpace(Text(row["ServiceAddress"])); address != "" {
		return address
	}
	if name := strings.TrimSpace(Text(row["Name"])); name != "" {
		return name
	}
	return "Utility"
}
