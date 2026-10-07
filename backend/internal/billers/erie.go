package billers

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
)

// Erie Insurance is three web applications behind one sign-in, read from the
// kept browser profile. See docs/connectors/providers.md.
//
//   - The landing page carries no JSON; the account id and each policy's
//     billing link are taken from its links.
//   - DocumentListWeb lists the in-force policies and the invoices, and a form
//     post answers the PDF.
//   - BillingCenterWeb is the term, activity and installments. Its session is
//     established only by following the landing page's billing link through
//     Inquiry/Index?transferKey=…; before that its JSON answers nothing.
//
// An invoice's due date is read from its PDF, then its own row, then a day
// that falls due in its cycle anywhere Erie answers one: the installments, the
// activity ledger, the term, the summary tile and, on a policy drawn
// automatically, the payment in that cycle, because Erie draws on the due
// date. Erie publishes no fixed gap between an invoice and its due date, so
// there is no offset to fall back on: a bill with no date from any of these is
// not filed, and a note names what each source answered, keys and types only.
//
// A statement is asked for by pressing the invoice's own link on the
// documents page, and failing that posted from that page as it posts them. A
// post the page's fetch is refused is asked again without following redirects,
// and the redirect's target, which the browser shows though the page may not
// read it, is opened in a page of its own; then the post is submitted as a
// form, and the PDF is taken from the responses and downloads the browser saw.
//
// The sign-in is the draft's: the portal's DaVinci widget swaps every step in
// place at one address, and what tells its forms apart lives in draft.go.

const (
	erieHome    = "https://www.erieinsurance.com"
	erieLanding = erieHome + "/Customer/ManageAccount/account"

	// The vendor the portal tags its own billing links with.
	erieVendor = "STG"

	// Each invoice read costs a form post and a PDF.
	erieInvoicesToRead = 3

	// The window GetDocuments is asked for, in the portal's own words.
	erieDocumentWindow      = "Last 13 months"
	erieDocumentWindowDays  = 396
	erieDocumentTransaction = "Changes Doc Date Filter"

	// The identity provider, whose access policy gateway a download can be
	// held at.
	erieIdentityHome = "https://custsso.erieinsurance.com"

	erieDocumentsPath = "/DocumentListWeb/Documents/MyDocuments/"
	erieStatementPath = "/DocumentListWeb/api/pdf/download"

	// How long a submitted statement form is given to answer, and how often
	// what the browser saw is looked at meanwhile.
	erieFormWait = 20 * time.Second
	erieFormLook = 500 * time.Millisecond
	// Once something has answered, a chain of redirects is followed until it
	// has been quiet for erieFormQuiet, and for erieChainWait at the most.
	erieFormQuiet = 8 * time.Second
	erieChainWait = 45 * time.Second
	// erieFormHeard caps the downloads and popups kept, and erieChainHeard the
	// lines a note lists.
	erieFormHeard  = 6
	erieChainHeard = 12
)

// erieAccountArea is the three web applications and the account pages, and
// nothing else: the sign-in host, the identity provider and the logout page
// are all somewhere a sign-in passes through rather than arrives at. The bare
// `/account` is the portal's own link to the account, which a signed-out
// browser is redirected away from.
var erieAccountArea = regexp.MustCompile(
	`(?i)erieinsurance\.com/(?:Customer|BillingCenterWeb|DocumentListWeb|PaymentCenterWeb|Account/ManageAccount|account/?(?:[?#]|$))`)

var (
	erieDocumentsHref = regexp.MustCompile(
		`(?i)/DocumentListWeb/Documents/MyDocuments/account/([0-9a-f-]{8,64})/`)
	erieBillingHref = regexp.MustCompile(
		`(?i)/BillingCenterWeb/Inquiry/Account/([0-9a-f-]{8,64})/Policy/([A-Za-z0-9-]+)/VendorId/([A-Za-z0-9]+)`)

	// Everything but letters and digits, so a policy number that is hyphenated
	// in one answer and not in the next is one policy.
	eriePunctuation = regexp.MustCompile(`[^A-Za-z0-9]`)
)

// The invoice PDF writes these labels the same way the company's mail does, so
// they are read through the mail parser's own reader.
var (
	erieDueLabels    = []string{"Due Date", "Payment Due Date", "Due By"}
	erieAmountLabels = []string{"Minimum Due", "Amount Due", "Total Due"}
)

// erieDueKeys are the due-date spellings the portal's calls are known to use;
// erieDueKey is any other key that names one.
var (
	erieDueKeys = []string{
		"dueDate", "installmentDueDate", "installmentDate", "nextDueDate", "paymentDueDate",
		"minimumDueDate", "policyMinDueDate",
	}
	erieDueKey = regexp.MustCompile(`(?i)due.?(?:date|dt|on)|date.?due|installment.?date`)
)

const erieLinksScript = `() => [...document.querySelectorAll('a[href]')].map((a) => a.href)`

// erieTileScript is the summary tile's line beside "Minimum due", which carries
// the date when something is owed.
const erieTileScript = `() => {` + agent.CleanJS + `
  const el = document.querySelector('#minimumDueMessage, .minimum-due-date');
  return el ? clean(el.innerText || el.textContent) : '';
}`

// erieFormTokenScript is the documents page's anti-forgery token, which its
// own form posts carry.
const erieFormTokenScript = `() => {
  const input = document.querySelector('input[name="__RequestVerificationToken"]');
  if (input && input.value) return { name: input.name, value: input.value };
  const meta = document.querySelector(
    'meta[name="__RequestVerificationToken"], meta[name="RequestVerificationToken"], meta[name="csrf-token"]');
  if (meta && meta.content) return { name: '__RequestVerificationToken', value: meta.content };
  return { name: '', value: '' };
}`

// erieSubmitScript posts a statement form as a navigation, the way the
// documents page opens one.
const erieSubmitScript = `(arg) => {
  const form = document.createElement('form');
  form.method = 'POST';
  form.action = arg.action;
  form.style.display = 'none';
  for (const field of arg.fields) {
    const input = document.createElement('input');
    input.type = 'hidden';
    input.name = field.name;
    input.value = field.value;
    form.appendChild(input);
  }
  (document.body || document.documentElement).appendChild(form);
  HTMLFormElement.prototype.submit.call(form);
  return true;
}`

type Erie struct {
	Draft
	// forms is each page's statement-form state, by page.
	forms *sync.Map
}

func NewErie() *Erie {
	return &Erie{forms: &sync.Map{}, Draft: Draft{
		BillerID: domain.BillerErie,
		Home:     erieHome,
		// `/login` is a 404 and `/account` is a 302 onto this path, which then
		// hands the sign-in off to the identity provider.
		SignIn:      erieHome + "/Account/Login/Login",
		Landing:     erieLanding,
		AccountArea: URLMatches(erieAccountArea),
	}}
}

// Forget drops a page's statement-form state, for the engine, which knows when
// a page is done.
func (e *Erie) Forget(page browser.Page) { e.forms.Delete(page) }

// erieRaw is everything the statement form asks for.
type erieRaw struct {
	DocumentHandle string `json:"document_handle"`
	DocumentID     string `json:"document_id,omitempty"`
	// DocumentPolicy is the policy number as the *documents* call writes it,
	// which is hyphenated where the rest of the portal's is not.
	DocumentPolicy string `json:"document_policy"`
	OnlineAccount  string `json:"online_account"`
	StartDate      string `json:"start_date"`
	EndDate        string `json:"end_date"`
}

type ErieAccount struct {
	ID      string
	Billing map[string]string
}

// ErieAccountFromLinks takes the account id from the links rather than asking
// for it: the portal never shows it anywhere a person would read it. It is
// never noted or logged.
func ErieAccountFromLinks(hrefs []string) ErieAccount {
	found := ErieAccount{Billing: map[string]string{}}
	for _, href := range hrefs {
		if match := erieBillingHref.FindStringSubmatch(href); match != nil {
			if found.ID == "" {
				found.ID = match[1]
			}
			found.Billing[eriePolicyKey(match[2])] = href
			continue
		}
		if match := erieDocumentsHref.FindStringSubmatch(href); match != nil && found.ID == "" {
			found.ID = match[1]
		}
	}
	return found
}

// eriePolicyKey is a policy number as a key: letters and digits, so the
// hyphenated spelling the documents call answers and the plain one the billing
// link carries are the same policy.
func eriePolicyKey(policy string) string {
	return strings.ToUpper(eriePunctuation.ReplaceAllString(policy, ""))
}

// account answers false for a profile that is not signed in.
func (e *Erie) account(call Call) (ErieAccount, bool) {
	page := call.Page
	_ = page.Goto(erieLanding)
	page.Settle()
	if !erieAccountArea.MatchString(page.URL()) {
		call.Notes.Addf(
			"erieinsurance.com answered %s instead of the account page; the profile is not signed in",
			browser.WithoutQuery(page.URL()))
		return ErieAccount{}, false
	}
	var hrefs []string
	if err := browser.EvaluateInto(page, erieLinksScript, nil, &hrefs); err != nil {
		call.Notes.Addf("the account page could not be read: %v", err)
		return ErieAccount{}, false
	}
	found := ErieAccountFromLinks(hrefs)
	if found.ID == "" {
		call.Notes.Addf("the account page carried no policy links; %s", browser.Glimpse(page, 160))
		return ErieAccount{}, false
	}
	return found, true
}

// The documents page is visited first because its own application will not
// answer JSON to a session that has never loaded it.
func (e *Erie) policies(call Call, account ErieAccount) ([]map[string]any, bool) {
	_ = call.Page.Goto(erieDocumentsPage(account.ID))
	call.Page.Settle()
	answer, err := e.get(call, erieHome+"/DocumentListWeb/Documents/Policies/Account/"+
		url.PathEscape(account.ID)+"/Policy/0")
	if err != nil {
		call.Notes.Addf("the policy list could not be asked for: %v", err)
		return nil, false
	}
	if !answer.OK {
		call.Notes.Addf("the policy list answered HTTP %d", answer.Status)
		return nil, answer.Status != 401 && answer.Status != 403
	}
	var shape struct {
		InForceProducts []map[string]any `json:"inForceProducts"`
	}
	if err := httpx.DecodeJSON(answer.Raw, &shape); err != nil {
		call.Notes.Addf("the policy list answered something that is not the policy list")
		return nil, true
	}
	return shape.InForceProducts, true
}

func (e *Erie) Subaccounts(call Call) ([]Subaccount, error) {
	if call.Page == nil {
		return nil, nil
	}
	account, ok := e.account(call)
	if !ok {
		return nil, nil
	}
	policies, ok := e.policies(call, account)
	if !ok {
		return nil, ErrNeedsSignIn
	}
	found := ErieSubaccountsFromPolicies(policies)
	call.Notes.Addf("Erie Insurance lists %d in-force polic%s", len(found), erieCy(len(found)))
	return found, nil
}

// ErieSubaccountsFromPolicies labels each account as "Auto policy", "Home
// policy" and so on; the number itself is the external id and the mask.
func ErieSubaccountsFromPolicies(rows []map[string]any) []Subaccount {
	found := make([]Subaccount, 0, len(rows))
	for _, row := range rows {
		number := strings.TrimSpace(Text(Pick(row, "policyNumber")))
		if number == "" {
			continue
		}
		label := strings.TrimSpace(Text(Pick(row, "productName", "productGroupName", "product")))
		if label == "" {
			label = "Policy"
		} else {
			label += " policy"
		}
		found = append(found, Subaccount{
			ExternalID:   number,
			Label:        label,
			MaskedNumber: domain.MaskAccount(number),
		})
	}
	return found
}

func (e *Erie) FetchBills(call Call) (Pull, error) {
	if call.Page == nil {
		return e.NoPage(), nil
	}
	account, ok := e.account(call)
	if !ok {
		return e.SignInAgain(nil), nil
	}
	policies, ok := e.policies(call, account)
	if !ok {
		return e.SignInAgain(nil), nil
	}

	asked := ErieSubaccountsFromPolicies(policies)
	if len(call.Subaccounts) > 0 {
		wanted := asked[:0:0]
		for _, one := range asked {
			if call.Wanted(one.ExternalID) {
				wanted = append(wanted, one)
			}
		}
		asked = wanted
	}
	if len(asked) == 0 {
		call.Notes.Addf("Erie Insurance lists %d in-force polic%s, none of them the %d this pull asked for",
			len(policies), erieCy(len(policies)), len(call.Subaccounts))
		return Pull{}, nil
	}

	var bills []Bill
	unread, lapsed := 0, 0
	for _, policy := range asked {
		read, term := e.readPolicy(call, account, policy.ExternalID)
		if term != erieTermRead {
			unread++
		}
		if term == erieTermLapsed {
			lapsed++
		}
		found := ErieBillsFromPolicy(read, call.Notes)
		call.Notes.Addf("Erie Insurance answered %d bill%s for the policy ending %s",
			len(found), plural(len(found)), domain.LastFour(policy.ExternalID))
		bills = append(bills, found...)
	}
	if lapsed > 0 && unread == len(asked) {
		return e.SignInAgain(bills), nil
	}
	return Pull{Bills: bills}, nil
}

// erieTermState tells a billing session that has lapsed from a policy that
// answered.
type erieTermState int

const (
	erieTermRead erieTermState = iota
	erieTermFailed
	erieTermLapsed
)

// The invoices are another application's and are read whatever the billing
// calls answered.
func (e *Erie) readPolicy(call Call, account ErieAccount, policy string) (ErieRead, erieTermState) {
	// The billing application's session is the redirect this link starts, and
	// its JSON answers nothing until that has happened.
	link, held := account.Billing[eriePolicyKey(policy)]
	if !held {
		link = erieInquiryLink(account.ID, policy)
	}
	_ = call.Page.Goto(link)
	call.Page.Settle()

	read := ErieRead{Policy: policy, InvoiceText: map[string]string{}}
	if tile, err := call.Page.Evaluate(erieTileScript, nil); err == nil {
		read.TileDue, _ = tile.(string)
	}

	terms, term := e.termList(call, policy)
	read.Terms = terms
	if term != erieTermLapsed {
		read.Activity, _ = e.rows(call, erieActivityURL(policy), "the billing activity")
		read.Installments, read.InstallmentsMissed = e.installments(call, policy)
	}

	from, to := erieWindow(call.At())
	read.Window = [2]string{from, to}
	read.Account = account.ID
	e.onDocumentsPage(call, account.ID)
	documents := e.documents(call, erieDocumentsURL(account.ID, policy, from, to))
	read.Documents = documents

	for _, invoice := range ErieInvoices(documents, policy) {
		handle := Text(Pick(invoice, "documentHandle"))
		if handle == "" {
			continue
		}
		body, ok := e.statement(call, account.ID, erieStatementForm(account, invoice, from, to), erieStatementRef{
			Handle: handle, ID: Text(Pick(invoice, "documentId")), Policy: Text(Pick(invoice, "policyNumber")),
			Printed: erieIssuedOn(invoice), Account: account.ID, From: from, To: to, Row: erieScalars(invoice),
		})
		if !ok {
			continue
		}
		read.InvoiceText[handle] = billmail.PDFText(body)
	}
	return read, term
}

// termList is the policy's terms. A 401, a 403 or a page where JSON belongs is
// the billing application's session gone, which a pull cannot mend.
func (e *Erie) termList(call Call, policy string) ([]map[string]any, erieTermState) {
	answer, err := e.get(call, erieTermListURL(policy))
	if err != nil {
		call.Notes.Addf("the term list could not be asked for: %v", err)
		return nil, erieTermFailed
	}
	if erieSessionLapsed(answer) {
		call.Notes.Addf("the term list for the policy ending %s answered HTTP %d and not its JSON; "+
			"the billing session has lapsed", domain.LastFour(policy), answer.Status)
		return nil, erieTermLapsed
	}
	if !answer.OK {
		call.Notes.Addf("the term list for the policy ending %s answered HTTP %d", domain.LastFour(policy), answer.Status)
		return nil, erieTermFailed
	}
	var rows []map[string]any
	if err := httpx.DecodeJSON(answer.Raw, &rows); err != nil {
		call.Notes.Addf("the term list for the policy ending %s answered something that is not a list",
			domain.LastFour(policy))
		return nil, erieTermFailed
	}
	return rows, erieTermRead
}

// installments is the installments call's answer, whatever its shape, or what
// went wrong asking for it.
func (e *Erie) installments(call Call, policy string) (any, string) {
	answer, err := e.get(call, erieInstallmentsURL(policy))
	if err != nil {
		return nil, "could not be asked for"
	}
	if !answer.OK {
		return nil, fmt.Sprintf("answered HTTP %d", answer.Status)
	}
	var decoded any
	if httpx.DecodeJSON(answer.Raw, &decoded) != nil {
		return nil, "answered something that is not JSON"
	}
	return decoded, ""
}

func erieSessionLapsed(answer Answered) bool {
	if answer.Status == 401 || answer.Status == 403 {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(string(answer.Raw)), "<")
}

// documents is the document list, which is the one call here that wraps its
// rows in an object rather than answering them.
func (e *Erie) documents(call Call, address string) []map[string]any {
	answer, err := e.get(call, address)
	if err != nil {
		call.Notes.Addf("the document list could not be asked for: %v", err)
		return nil
	}
	if !answer.OK {
		call.Notes.Addf("the document list answered HTTP %d", answer.Status)
		return nil
	}
	var shape struct {
		Documents []map[string]any `json:"documents"`
	}
	if err := httpx.DecodeJSON(answer.Raw, &shape); err != nil {
		call.Notes.Addf("the document list answered something that is not the document list")
		return nil
	}
	return shape.Documents
}

func (e *Erie) rows(call Call, address, what string) ([]map[string]any, bool) {
	answer, err := e.get(call, address)
	if err != nil {
		call.Notes.Addf("%s could not be asked for: %v", what, err)
		return nil, false
	}
	if !answer.OK {
		call.Notes.Addf("%s answered HTTP %d", what, answer.Status)
		return nil, false
	}
	var rows []map[string]any
	if err := httpx.DecodeJSON(answer.Raw, &rows); err != nil {
		call.Notes.Addf("%s answered something that is not a list", what)
		return nil, false
	}
	return rows, true
}

func (e *Erie) get(call Call, address string) (Answered, error) {
	return Ask(call.Ctx, eriePageFetcher(call.Page), "GET", address,
		map[string]string{"Accept": "application/json"}, nil, "Erie Insurance")
}

// onDocumentsPage puts the page on the documents page, whose own posts open a
// statement.
func (e *Erie) onDocumentsPage(call Call, account string) {
	if account == "" || strings.Contains(call.Page.URL(), erieDocumentsPath) {
		return
	}
	_ = call.Page.Goto(erieDocumentsPage(account))
	call.Page.Settle()
}

// erieForms is one page's statement requests: whether the page's own call is
// refused them, whether every other way was too, and what the browser was
// answered, handed and opened while a statement was out.
type erieForms struct {
	mu        sync.Mutex
	listening bool
	armed     bool
	seen      []browser.Response
	files     []browser.Download
	popups    []browser.Page
	redirects bool
	refused   bool
}

func (e *Erie) formsOn(page browser.Page) *erieForms {
	held, _ := e.forms.LoadOrStore(page, &erieForms{})
	return held.(*erieForms)
}

func (f *erieForms) arm(page browser.Page) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.listening {
		f.listening = true
		page.OnResponse(f.record)
		page.OnDownload(f.download)
		page.OnPopup(f.popup)
	}
	f.armed, f.seen, f.files, f.popups = true, nil, nil, nil
}

// record, download and popup run on the driver's goroutine, so they keep
// what they are handed and read nothing from it. A popup is listened to as
// soon as it exists, because a PDF opened in a new window arrives there.
func (f *erieForms) record(response browser.Response) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.armed && len(f.seen) < 8*erieFormHeard {
		f.seen = append(f.seen, response)
	}
}

func (f *erieForms) download(download browser.Download) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.armed && len(f.files) < erieFormHeard {
		f.files = append(f.files, download)
	}
}

func (f *erieForms) popup(page browser.Page) {
	f.mu.Lock()
	kept := f.armed && len(f.popups) < erieFormHeard
	if kept {
		f.popups = append(f.popups, page)
	}
	f.mu.Unlock()
	if kept {
		page.OnResponse(f.record)
		page.OnDownload(f.download)
	}
}

func (f *erieForms) heard() ([]browser.Response, []browser.Download, []browser.Page) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]browser.Response(nil), f.seen...), append([]browser.Download(nil), f.files...),
		append([]browser.Page(nil), f.popups...)
}

func (f *erieForms) disarm() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed, f.seen, f.files, f.popups = false, nil, nil, nil
}

func (f *erieForms) state() (redirects, refused bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.redirects, f.refused
}

func (f *erieForms) settle(redirects, refused bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redirects, f.refused = redirects, refused
}

// erieStatementRef is what finds an invoice on the documents page and fills
// that page's own form for it.
type erieStatementRef struct {
	Handle, ID, Policy string
	// Printed is the invoice's print date, ISO.
	Printed string
	// Account and the document window From and To are the module's own.
	Account, From, To string
	// Row is the document list's row, its scalars as text; empty for a bill
	// asked for again.
	Row map[string]any
}

// statement is one invoice PDF. It is asked for as the documents page itself
// asks for it (askThePage), and failing that posted with the page's own anti-forgery token. A page whose own
// call is refused is taken round that call (followStatement) from then on, and
// a page every way failed on is not asked again: the note that said why was
// written the first time.
func (e *Erie) statement(call Call, account string, form url.Values, ref erieStatementRef) ([]byte, bool) {
	forms := e.formsOn(call.Page)
	redirects, refused := forms.state()
	if refused {
		return nil, false
	}
	e.onDocumentsPage(call, account)
	from := eriePlace(call.Page.URL())
	body, clicked := e.askThePage(call, ref)
	if body != nil {
		call.Notes.Tracef("an Erie statement was read asking the page itself: %s", clicked)
		return body, true
	}
	clicked = "asking the page itself: " + clicked
	// A press can navigate the page away, and the token belongs to the page.
	e.onDocumentsPage(call, account)
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	var token struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if browser.EvaluateInto(call.Page, erieFormTokenScript, nil, &token) == nil && token.Value != "" {
		form.Set(token.Name, token.Value)
		headers["RequestVerificationToken"] = token.Value
	}
	if redirects {
		body, said := e.followStatement(call, headers, form)
		if body == nil {
			call.Notes.Addf("a statement could not be read from %s: %s; %s", from, clicked, said)
		} else {
			call.Notes.Tracef("an Erie statement: %s; %s", clicked, said)
		}
		return body, body != nil
	}

	answer, err := Ask(call.Ctx, eriePageFetcher(call.Page), "POST", erieHome+erieStatementPath,
		headers, []byte(form.Encode()), "Erie Insurance")
	if err == nil {
		if answer.OK && isPDF(answer.Raw) {
			return answer.Raw, true
		}
		call.Notes.Addf("a statement answered HTTP %d as something that is not a PDF; %s", answer.Status, clicked)
		return nil, false
	}
	if !browser.RefusedByBrowser(err) {
		call.Notes.Addf("a statement could not be asked for: %v; %s", err, clicked)
		return nil, false
	}
	body, said := e.followStatement(call, headers, form)
	if body != nil {
		forms.settle(true, false)
		call.Notes.Tracef("the page's own call for an Erie statement was refused; %s; %s", clicked, said)
		return body, true
	}
	forms.settle(true, true)
	call.Notes.Addf("a statement could not be read from %s: %s; the page's own call was refused (%v); %s; "+
		"no further statement is asked for from this page", from, clicked, err, said)
	return nil, false
}

// erieMark is the attribute erieFindScript marks the invoice's own link with,
// and erieInnerMark the control inside its panel, so a press is Playwright's
// pointer click on the element itself.
const (
	erieMark      = `[data-agentifi-statement]`
	erieInnerMark = `[data-agentifi-inner]`
)

// erieHelpersJS is what the documents page's scripts share. They answer names
// and addresses only: tags, attribute names, link and form addresses, input
// and function names, never a value.
const erieHelpersJS = agent.EscapedJS + `
  const lower = (s) => String(s == null ? '' : s).toLowerCase();
  const values = (el) => lower([...el.attributes].map((a) => a.value).join(' '));
  const clickable = 'a, button, input[type=button], input[type=submit], [role=button], [role=link], [onclick], [ng-click], [data-ng-click]';
  const address = (el, name) => {
    const raw = el.getAttribute(name);
    if (raw == null) return '';
    const text = raw.trim();
    if (/^javascript:/i.test(text)) return 'javascript:';
    if (text === '' || text.startsWith('#')) return '#';
    try { return new URL(text, document.baseURI).href; } catch { return '?'; }
  };
  const jquery = (el) => {
    try {
      const events = window.jQuery && window.jQuery._data && window.jQuery._data(el, 'events');
      return events ? Object.keys(events) : [];
    } catch { return []; }
  };
  const describe = (el) => {
    const form = el.form || el.closest('form');
    return {
      tag: el.tagName.toLowerCase(),
      attributes: [...el.attributes].map((a) => a.name).filter((n) => !n.startsWith('data-agentifi')).slice(0, 20),
      href: address(el, 'href'),
      onclick: typeof el.onclick === 'function' || el.hasAttribute('onclick'),
      jquery: jquery(el),
      target: el.getAttribute('target') || '',
      form: form ? (address(form, 'action') || '(no action)') : '',
      visible: el.getClientRects().length > 0,
    };
  };
`

// erieScopeJS reads Angular from the page's own world (Erie runs in Chrome,
// where page scripts share it): the scope holding the document object whose
// handle or id is the invoice's, looked for above the element first and then
// through every scope from the root, since a list the page reloads leaves no
// marked element behind; and the functions on that scope, its parents and any
// controller they hold.
const erieScopeJS = `
  const scopeOf = (el, handle, id) => {
    const ng = window.angular;
    if (!ng || !ng.element) return { state: 'absent', functions: [] };
    const scopeAt = (node) => { try { return node ? ng.element(node).scope() : null; } catch { return null; } };
    const root = scopeAt(document.querySelector('[ng-app], [data-ng-app]')) || scopeAt(document.body);
    const start = scopeAt(el) || root;
    if (!start) return { state: 'unreadable', functions: [] };
    const seen = new WeakSet();
    const isDoc = (v) => (handle && String(v.documentHandle) === handle) || (id && String(v.documentId) === id);
    const findDoc = (v, depth) => {
      if (!v || typeof v !== 'object' || depth > 4 || seen.has(v)) return null;
      seen.add(v);
      if (!Array.isArray(v) && isDoc(v)) return v;
      const keys = Array.isArray(v) ? v.map((_, i) => i).slice(0, 500) : Object.keys(v).filter((k) => !k.startsWith('$')).slice(0, 200);
      for (const k of keys) {
        let found = null;
        try { found = findDoc(v[k], depth + 1); } catch {}
        if (found) return found;
      }
      return null;
    };
    const holds = (s) => {
      for (const key of Object.keys(s).filter((k) => !k.startsWith('$'))) {
        let value;
        try { value = s[key]; } catch { continue; }
        const doc = findDoc(value, 1);
        if (doc) return doc;
      }
      return null;
    };
    let home = null, doc = null;
    for (let s = start; s && !doc; s = s.$parent) { doc = holds(s); if (doc) home = s; }
    if (!doc && root) {
      const queue = [root];
      for (let n = 0; queue.length && n < 3000; n += 1) {
        const s = queue.shift();
        doc = holds(s);
        if (doc) { home = s; break; }
        for (let c = s.$$childHead; c; c = c.$$nextSibling) queue.push(c);
      }
    }
    const methods = (value) => {
      const proto = Object.getPrototypeOf(value);
      const names = new Set(Object.keys(value));
      if (proto && proto !== Object.prototype) for (const n of Object.getOwnPropertyNames(proto)) names.add(n);
      return [...names].filter((n) => n !== 'constructor' && !n.startsWith('$')).slice(0, 80);
    };
    const functions = [];
    for (let s = home || start, depth = 0; s && depth < 12; s = s.$parent, depth += 1) {
      for (const key of Object.keys(s).filter((k) => !k.startsWith('$'))) {
        let value;
        try { value = s[key]; } catch { continue; }
        if (typeof value === 'function') functions.push({ name: key, owner: s, method: key, scope: s });
        else if (value && typeof value === 'object' && !Array.isArray(value)) {
          for (const inner of methods(value)) {
            let fn;
            try { fn = value[inner]; } catch { continue; }
            if (typeof fn === 'function') functions.push({ name: key + '.' + inner, owner: value, method: inner, scope: s });
          }
        }
      }
    }
    return { state: 'scope', scope: home || start, doc, functions };
  };
  const ngClick = (el) => el.getAttribute('ng-click') || el.getAttribute('data-ng-click') || '';
  const called = (expr) => String(expr || '').match(/[A-Za-z_$][\w$.]*(?=\s*\()/g) || [];
  const downloads = (name) => {
    const last = name.toLowerCase().split('.').pop();
    if (/^(get|reset|load|refresh|set|is|has|toggle|filter|sort|format|showhide)/.test(last)) return false;
    return /download|pdf/.test(last) || (/view|open|print/.test(last) && /doc|file|invoice|statement/.test(last));
  };
`

// erieFindScript finds the documents page's own link or button for one
// invoice, best first: one whose attributes carry the document handle or id,
// then one in a row that carries either, then one in an invoice row with the
// print date (and the policy). A row is a row only while it holds a few
// controls; a list's container would match every invoice. It says whether
// what it found opens a collapsed panel, and how the page asks for a file.
const erieFindScript = `(arg) => {` + erieHelpersJS + `
  const squeeze = (s) => lower(s).replace(/[^a-z0-9]/g, '');
  for (const old of document.querySelectorAll('[data-agentifi-statement]')) old.removeAttribute('data-agentifi-statement');
  const handle = lower(arg.handle), id = lower(arg.id), policy = squeeze(arg.policy);
  const dates = (arg.dates || []).map(lower).filter(Boolean);
  const all = [...document.querySelectorAll(clickable)];
  const rowOf = (el) => el.closest('tr, li, [role=row], .list-group-item, .card, .document') || el.parentElement;
  let best = null, score = 0, how = '';
  for (const el of all) {
    const own = values(el);
    const row = rowOf(el);
    const alone = row && row.querySelectorAll(clickable).length <= 6;
    const rowText = alone ? lower(row.innerText) + ' ' + values(row) : '';
    let s = 0, h = '';
    if (handle && own.includes(handle)) { s = 6; h = 'its document handle'; }
    else if (id && own.includes(id)) { s = 5; h = 'its document id'; }
    else if (handle && rowText.includes(handle)) { s = 4; h = 'the document handle in its row'; }
    else if (id && rowText.includes(id)) { s = 3; h = 'the document id in its row'; }
    else if (rowText.includes('invoice') && dates.some((d) => rowText.includes(d))) {
      if (policy && squeeze(row.innerText).includes(policy)) { s = 2; h = 'the print date and the policy in its row'; }
      else { s = 1; h = 'the print date in its row'; }
    }
    if (s > score) { best = el; score = s; how = h; }
  }
  const naming = all
    .filter((el) => /invoice|view|download|pdf|document/i.test(values(el) + ' ' + (el.innerText || '').slice(0, 200)))
    .slice(0, 3).map(describe);
  const forms = [...document.forms].slice(0, 4).map((f) => ({
    action: address(f, 'action') || '(no action)', method: lower(f.getAttribute('method') || 'get'),
    inputs: [...f.elements].map((e) => e.name).filter(Boolean).slice(0, 20),
  }));
  const page = { clickable: all.length, naming, forms };
  if (!best) return { found: false, page };
  best.setAttribute('data-agentifi-statement', '1');
  const toggleAttr = best.getAttribute('data-toggle') || best.getAttribute('data-bs-toggle') || '';
  const controls = best.getAttribute('aria-controls');
  const hash = best.getAttribute('href') || '';
  const panel = controls ? '#' + escaped(controls)
    : (best.getAttribute('data-target') || best.getAttribute('data-bs-target') || (hash.length > 1 && hash.startsWith('#') ? hash : ''));
  const toggle = {
    is: /collapse/i.test(toggleAttr) || best.hasAttribute('aria-expanded') || !!controls,
    expanded: best.getAttribute('aria-expanded') || '', panel,
  };
  return { found: true, how, element: describe(best), toggle, page };
}`

// erieAngularScript asks the page's Angular for the invoice's document object
// and for what downloads it. The page's own ng-click on the invoice's link or
// a control in its row or panel comes first, evaluated in that control's own scope
// so its arguments are the page's; then a function on the scopes, called on
// the document object. Only a name that says it downloads, or views, opens or
// prints a document or file, qualifies: a list's getter or reset reloads the
// list. With call set it runs the one found after answering, since a call
// that navigates would tear the answer down. It answers the names the
// header's and the row's ng-click call, never their arguments.
const erieAngularScript = `(arg) => {` + erieHelpersJS + erieScopeJS + `
  const header = document.querySelector('[data-agentifi-statement]');
  const found = scopeOf(header, String(arg.handle || ''), String(arg.id || ''));
  if (found.state !== 'scope') return { state: found.state };
  const around = header ? (header.closest('tr, li, .panel, .card, .accordion-item') || header.parentElement) : null;
  let panel = null;
  try { const c = header && header.getAttribute('aria-controls'); panel = c ? document.getElementById(c) : null; } catch {}
  const rowEls = [];
  for (const box of [around, panel]) {
    if (box) for (const el of box.querySelectorAll('[ng-click], [data-ng-click]')) if (el !== header) rowEls.push(el);
  }
  const answer = {
    state: 'scope',
    doc: found.doc ? Object.keys(found.doc).filter((k) => !k.startsWith('$')).slice(0, 40) : [],
    functions: found.functions.map((f) => f.name).filter((n) => /doc|pdf|file|invoice|form|download|view|open|print/i.test(n)).slice(0, 20),
    header: header ? called(ngClick(header)).slice(0, 6) : [],
    row: [...new Set(rowEls.flatMap((el) => called(ngClick(el))))].slice(0, 12),
    chosen: '', via: '', called: false,
  };
  let pick = null;
  for (const el of header ? [header, ...rowEls] : rowEls) {
    const name = called(ngClick(el)).find(downloads);
    if (name) { pick = { el }; answer.chosen = name; answer.via = 'ng-click'; break; }
  }
  if (!pick && found.doc) {
    const fn = found.functions.find((f) => downloads(f.name));
    if (fn) { pick = { fn }; answer.chosen = fn.name; answer.via = 'scope'; }
  }
  if (arg.call && pick) {
    setTimeout(() => {
      try {
        if (pick.el) window.angular.element(pick.el).scope().$apply(ngClick(pick.el));
        else pick.fn.scope.$apply(() => pick.fn.owner[pick.fn.method](found.doc));
      } catch {}
    }, 0);
    answer.called = true;
  }
  return answer;
}`

// erieRevealScript opens the collapsed groups the invoice's own link sits in,
// outermost first, through each group's own toggle.
const erieRevealScript = `() => {` + agent.EscapedJS + `
  const el = document.querySelector('[data-agentifi-statement]');
  if (!el) return { hidden: 0, opened: 0 };
  const toggles = [];
  let hidden = 0;
  for (let node = el.parentElement; node && node !== document.body; node = node.parentElement) {
    const collapsed = (node.classList.contains('collapse') && !node.classList.contains('in') && !node.classList.contains('show')) ||
      getComputedStyle(node).display === 'none';
    if (!collapsed) continue;
    hidden += 1;
    if (!node.id) continue;
    const id = escaped(node.id);
    const toggle = document.querySelector('[aria-controls="' + id + '"], [data-target="#' + id + '"], [data-bs-target="#' + id + '"], [href="#' + id + '"]');
    if (toggle && toggle !== el) toggles.push(toggle);
  }
  for (const toggle of toggles.reverse()) toggle.click();
  return { hidden, opened: toggles.length };
}`

// erieAPMScript reads the page a popup rests on, an F5 access policy page at
// the identity provider among them: its title and headings (digits masked),
// its forms (addresses and input names) and any continue control. With act
// set it submits a form that carries only hidden inputs, which is the page
// passing itself on, or presses a continue control; a form a person would
// type into is never sent.
const erieAPMScript = `(arg) => {` + erieHelpersJS + agent.CleanJS + `
  const mask = (s) => clean(s).replace(/\d/g, '#').slice(0, 60);
  const passing = (f) => {
    const named = [...f.elements].filter((e) => e.name);
    return named.length > 0 && named.every((e) => e.type === 'hidden' || e.type === 'submit' || e.tagName === 'BUTTON');
  };
  const forms = [...document.forms].slice(0, 4).map((f) => ({
    action: address(f, 'action') || '(no action)', method: lower(f.getAttribute('method') || 'get'),
    inputs: [...f.elements].map((e) => e.name).filter(Boolean).slice(0, 20), passing: passing(f),
  }));
  const controls = [...document.querySelectorAll('a, button, input[type=submit], input[type=button]')]
    .filter((el) => /continue|proceed|click here/i.test((el.innerText || el.value || '') + ' ' + (el.getAttribute('title') || '')));
  const answer = {
    title: mask(document.title),
    headings: [...document.querySelectorAll('h1, h2, h3')].map((h) => mask(h.innerText)).filter(Boolean).slice(0, 4),
    forms,
    scripted: [...document.scripts].some((s) => /\.submit\(\)/.test(s.textContent || '')),
    controls: controls.slice(0, 3).map(describe),
    acted: '',
  };
  if (arg.act) {
    const form = [...document.forms].find((f) => {
      const action = f.getAttribute('action');
      return action && !action.startsWith('#') && passing(f);
    });
    if (form) { setTimeout(() => HTMLFormElement.prototype.submit.call(form), 0); answer.acted = 'form'; }
    else if (controls.length) { setTimeout(() => controls[0].click(), 0); answer.acted = 'control'; }
  }
  return answer;
}`

// erieInnerScript finds, in the panel the invoice's own header opens, the
// control that names a file (download or pdf first, then view, open or print)
// and marks it; a matched icon is pressed through the control around it.
const erieInnerScript = `(arg) => {` + erieHelpersJS + `
  for (const old of document.querySelectorAll('[data-agentifi-inner]')) old.removeAttribute('data-agentifi-inner');
  const toggle = document.querySelector('[data-agentifi-statement]');
  let panel = null;
  try { panel = arg.panel ? document.querySelector(arg.panel) : null; } catch {}
  if (!panel && toggle) panel = toggle.closest('.panel, .card, .accordion-item, li, tr') || toggle.parentElement;
  const answer = { shown: !!(panel && panel.getClientRects().length), expanded: toggle ? (toggle.getAttribute('aria-expanded') || '') : '', controls: [], found: false };
  if (!panel) return answer;
  const pressable = 'a, button, [role=button], [ng-click], [data-ng-click], [onclick]';
  const candidates = [...panel.querySelectorAll(pressable + ', i, img, span[class*=icon], [class*=download], [class*=pdf]')]
    .filter((el) => el !== toggle && !el.contains(toggle));
  const words = (el) => [...el.attributes].map((a) => a.name + ' ' + a.value).join(' ') + ' ' + (el.innerText || '').slice(0, 100);
  let best = null, score = 0;
  for (const el of candidates) {
    const text = words(el);
    let s = /download|pdf/i.test(text) ? 3 : /view|open|print/i.test(text) ? 2 : /document|invoice/i.test(text) ? 1 : 0;
    if (s && (el.matches(pressable) || jquery(el).length)) s += 0.5;
    if (s > score) { best = el; score = s; }
  }
  answer.controls = candidates.filter((el) => el.matches(pressable)).slice(0, 6).map(describe);
  if (!best) return answer;
  const press = best.matches(pressable) ? best : (best.closest(pressable) || best);
  if (!panel.contains(press)) return answer;
  press.setAttribute('data-agentifi-inner', '1');
  answer.found = true;
  answer.element = describe(press);
  return answer;
}`

// erieOwnFormScript fills the documents page's own hidden form to the
// statement path and submits it after answering. Of several, it takes the one
// whose inputs the invoice's own data covers best. Each input is the page's
// document object's value, then the document list row's, then the value the
// page already holds, then one the module knows; the answer names where each
// came from, never the value.
const erieOwnFormScript = `(arg) => {` + erieHelpersJS + erieScopeJS + `
  const forms = [...document.forms].filter((f) => /\/api\/pdf\/download/i.test(f.getAttribute('action') || ''));
  if (!forms.length) return { found: false };
  const found = scopeOf(document.querySelector('[data-agentifi-statement]'), String(arg.handle || ''), String(arg.id || ''));
  const doc = found.doc;
  const row = arg.row || {};
  const known = arg.known || {};
  const held = (name) => (doc && doc[name] != null && typeof doc[name] !== 'object') || row[name] != null;
  const names = (f) => [...f.elements].map((e) => e.name).filter(Boolean);
  const cover = (f) => { const n = names(f); return n.length ? n.filter(held).length / n.length : 0; };
  const form = forms.reduce((a, b) => (cover(b) > cover(a) ? b : a));
  const filled = [];
  for (const input of [...form.elements]) {
    if (!input.name) continue;
    let value = '', from = 'nothing';
    if (doc && doc[input.name] != null && typeof doc[input.name] !== 'object') { value = String(doc[input.name]); from = "the page's document object"; }
    else if (row[input.name] != null) { value = String(row[input.name]); from = 'the document list row'; }
    else if (input.value) { value = input.value; from = "the page's own value"; }
    else if (known[input.name]) { value = String(known[input.name].value); from = known[input.name].from; }
    input.value = value;
    filled.push({ name: input.name, from });
  }
  if (arg.self) form.removeAttribute('target');
  if (arg.submit) setTimeout(() => HTMLFormElement.prototype.submit.call(form), 0);
  return {
    found: true, forms: forms.length, action: address(form, 'action') || '(no action)',
    target: form.getAttribute('target') || '', angular: found.state, doc: !!doc, filled,
  };
}`

// erieScriptClickScript presses the marked element from the page, for one a
// person could not see to press.
const erieScriptClickScript = `(selector) => {
  const el = document.querySelector(selector);
  if (!el) return false;
  el.click();
  return true;
}`

type erieFound struct {
	Found   bool        `json:"found"`
	How     string      `json:"how"`
	Element erieElement `json:"element"`
	Toggle  struct {
		Is       bool   `json:"is"`
		Expanded string `json:"expanded"`
		Panel    string `json:"panel"`
	} `json:"toggle"`
	Page struct {
		Clickable int           `json:"clickable"`
		Naming    []erieElement `json:"naming"`
		Forms     []struct {
			Action string   `json:"action"`
			Method string   `json:"method"`
			Inputs []string `json:"inputs"`
		} `json:"forms"`
	} `json:"page"`
}

type erieElement struct {
	Tag        string   `json:"tag"`
	Attributes []string `json:"attributes"`
	Href       string   `json:"href"`
	OnClick    bool     `json:"onclick"`
	JQuery     []string `json:"jquery"`
	Target     string   `json:"target"`
	Form       string   `json:"form"`
	Visible    bool     `json:"visible"`
}

// String names an element by its tag, its attributes' names and its
// addresses, never an attribute's value.
func (el erieElement) String() string {
	parts := []string{"<" + erieName(el.Tag) + ">"}
	if len(el.Attributes) > 0 {
		parts = append(parts, "attributes "+erieNames(el.Attributes))
	}
	if el.Href != "" {
		parts = append(parts, "href "+erieAddress(el.Href))
	}
	switch {
	case el.OnClick:
		parts = append(parts, "an onclick handler")
	case len(el.JQuery) > 0:
		parts = append(parts, "jQuery handlers "+erieNames(el.JQuery))
	default:
		parts = append(parts, "no onclick or jQuery handler")
	}
	if el.Target != "" {
		parts = append(parts, "target "+erieName(el.Target))
	}
	if el.Form != "" {
		parts = append(parts, "in a form to "+erieAddress(el.Form))
	}
	if !el.Visible {
		parts = append(parts, "not visible")
	}
	return strings.Join(parts, ", ")
}

func erieElements(elements []erieElement) string {
	described := make([]string, len(elements))
	for index, el := range elements {
		described[index] = el.String()
	}
	return strings.Join(described, "; ")
}

// machinery is how the documents page asks for a file, for a note.
func (f erieFound) machinery() string {
	parts := []string{fmt.Sprintf("the documents page has %d links and buttons", f.Page.Clickable)}
	if len(f.Page.Naming) > 0 {
		parts = append(parts, "those naming a document: "+erieElements(f.Page.Naming))
	}
	if len(f.Page.Forms) == 0 {
		parts = append(parts, "no form")
	}
	for _, form := range f.Page.Forms {
		parts = append(parts, fmt.Sprintf("a %s form to %s with inputs %s",
			erieName(form.Method), erieAddress(form.Action), cmp.Or(erieNames(form.Inputs), "none")))
	}
	return "(" + strings.Join(parts, "; ") + ")"
}

// erieName is a name from the page as a note may carry it: one that looks
// like an identifier is "<id>".
func erieName(name string) string {
	if identifierKey.MatchString(name) {
		return "<id>"
	}
	return name
}

func erieNames(names []string) string {
	kept := make([]string, len(names))
	for index, name := range names {
		kept[index] = erieName(name)
	}
	return strings.Join(kept, " ")
}

// erieAddress is an address a page script answered, as a note names it.
func erieAddress(address string) string {
	switch address {
	case "#", "javascript:", "?", "(no action)":
		return address
	}
	return eriePlace(address)
}

// erieDayVariants is a day as the documents page may write it.
func erieDayVariants(iso string) []any {
	day, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return []any{}
	}
	return []any{
		day.Format("01/02/2006"), day.Format("1/2/2006"), day.Format("Jan 2, 2006"), day.Format("January 2, 2006"), iso,
	}
}

// erieExpandWait is how long a pressed header is given to open its panel.
const erieExpandWait = 5 * time.Second

// askThePage asks the documents page for the invoice in the page's own ways,
// most faithful first: the page's own Angular download for the document,
// then the invoice's own link, opening its panel first when it is an
// accordion header and pressing the control inside, then the page's own
// hidden form. It answers the PDF and how it came, or what each way came to
// and how the page asks for a file.
func (e *Erie) askThePage(call Call, ref erieStatementRef) ([]byte, string) {
	find := map[string]any{
		"handle": ref.Handle, "id": ref.ID, "policy": eriePolicyKey(ref.Policy), "dates": erieDayVariants(ref.Printed),
	}
	var found erieFound
	err := browser.EvaluateInto(call.Page, erieFindScript, find, &found)
	var said []string
	if err != nil {
		said = append(said, "the documents page could not be searched ("+err.Error()+")")
	}
	body, line, called := e.askAngular(call, ref)
	if body != nil {
		return body, strings.Join(append(said, line), "; ")
	}
	said = append(said, line)
	if called && found.Found {
		// What the page ran can redraw the list, and the mark with it.
		_ = browser.EvaluateInto(call.Page, erieFindScript, find, &found)
	}
	if found.Found {
		body, line = e.pressStatement(call, found)
		if body != nil {
			return body, strings.Join(append(said, line), "; ")
		}
		said = append(said, line)
	} else if err == nil {
		said = append(said, "the documents page shows no link or button for the invoice")
	}
	body, line = e.submitOwnForm(call, ref)
	if body != nil {
		return body, strings.Join(append(said, line), "; ")
	}
	said = append(said, line)
	return nil, strings.Join(said, "; ") + " " + found.machinery()
}

type erieAngular struct {
	State     string   `json:"state"`
	Doc       []string `json:"doc"`
	Functions []string `json:"functions"`
	Header    []string `json:"header"`
	Row       []string `json:"row"`
	Chosen    string   `json:"chosen"`
	Via       string   `json:"via"`
	Called    bool     `json:"called"`
}

// askAngular runs the page's own download for the invoice, when the page's
// Angular has one (erieAngularScript), and says whether it ran anything.
func (e *Erie) askAngular(call Call, ref erieStatementRef) ([]byte, string, bool) {
	arg := map[string]any{"handle": ref.Handle, "id": ref.ID, "call": false}
	var seen erieAngular
	if err := browser.EvaluateInto(call.Page, erieAngularScript, arg, &seen); err != nil {
		return nil, "the page's Angular could not be asked (" + err.Error() + ")", false
	}
	switch seen.State {
	case "scope":
	case "unreadable":
		return nil, "the page's Angular answers no scope (its debug information is off)", false
	default:
		return nil, "the page carries no Angular", false
	}
	held := "no document object for the invoice"
	if len(seen.Doc) > 0 {
		held = "the invoice's document object (keys " + erieNames(seen.Doc) + ")"
	}
	what := fmt.Sprintf("the page's Angular scope holds %s and functions %s; the header's ng-click calls %s; "+
		"the row's ng-click calls %s", held, cmp.Or(erieNames(seen.Functions), "none naming a document"),
		cmp.Or(erieNames(seen.Header), "nothing"), cmp.Or(erieNames(seen.Row), "nothing"))
	if seen.Chosen == "" {
		return nil, what + "; none of them downloads a file, so none was called", false
	}
	how := "on the document object"
	if seen.Via == "ng-click" {
		how = "through the page's own ng-click"
	}
	forms := e.formsOn(call.Page)
	forms.arm(call.Page)
	defer forms.disarm()
	arg["call"] = true
	if err := browser.EvaluateInto(call.Page, erieAngularScript, arg, &seen); err != nil || !seen.Called {
		return nil, what + "; " + erieName(seen.Chosen) + " could not be called", false
	}
	body, saw := e.heardOut(call, forms, e.await(call, forms))
	if body != nil {
		return body, what + "; called " + erieName(seen.Chosen) + " " + how + ", " + saw, true
	}
	return nil, what + "; called " + erieName(seen.Chosen) + " " + how + ": " + saw, true
}

type erieInner struct {
	Shown    bool          `json:"shown"`
	Expanded string        `json:"expanded"`
	Controls []erieElement `json:"controls"`
	Found    bool          `json:"found"`
	Element  erieElement   `json:"element"`
}

// pressStatement presses the invoice's own link with the browser listened to.
// A link inside collapsed groups has them opened first; an accordion header
// is opened and the control inside its panel is what is pressed.
func (e *Erie) pressStatement(call Call, found erieFound) ([]byte, string) {
	element := fmt.Sprintf("the invoice's own %s (matched by %s)", found.Element, found.How)
	var revealed struct {
		Hidden int `json:"hidden"`
		Opened int `json:"opened"`
	}
	if !found.Element.Visible && browser.EvaluateInto(call.Page, erieRevealScript, nil, &revealed) == nil &&
		revealed.Hidden > 0 {
		element += fmt.Sprintf(", inside %d hidden group%s of which %d were opened",
			revealed.Hidden, plural(revealed.Hidden), revealed.Opened)
		call.Page.Settle()
	}
	if !found.Toggle.Is {
		return e.pressAndHear(call, erieMark, element)
	}
	before := cmp.Or(found.Toggle.Expanded, "unset")
	if found.Toggle.Expanded != "true" {
		e.press(call, erieMark)
		panel, _ := json.Marshal(found.Toggle.Panel)
		_ = call.Page.WaitForFunction(fmt.Sprintf(`() => {
  const toggle = document.querySelector('[data-agentifi-statement]');
  if (toggle && toggle.getAttribute('aria-expanded') === 'true') return true;
  let panel = null;
  try { panel = %s ? document.querySelector(%s) : null; } catch {}
  return !!(panel && panel.getClientRects().length);
}`, panel, panel), erieExpandWait)
		call.Page.Settle()
	}
	var inner erieInner
	if err := browser.EvaluateInto(call.Page, erieInnerScript, map[string]any{"panel": found.Toggle.Panel}, &inner); err != nil {
		return nil, "opened " + element + ", an accordion header, and its panel could not be read (" + err.Error() + ")"
	}
	shown := "not shown"
	if inner.Shown {
		shown = "shown"
	}
	opened := fmt.Sprintf("opened %s, an accordion header (aria-expanded %s, then %s), its panel %s",
		element, before, cmp.Or(inner.Expanded, "unset"), shown)
	if len(inner.Controls) > 0 {
		opened += ", with controls " + erieElements(inner.Controls)
	}
	if !inner.Found {
		return nil, opened + "; inside it nothing names a file"
	}
	body, saw := e.pressAndHear(call, erieInnerMark, "the control inside it, "+inner.Element.String())
	return body, opened + "; " + saw
}

// pressAndHear presses a marked element with the browser listened to.
func (e *Erie) pressAndHear(call Call, selector, element string) ([]byte, string) {
	forms := e.formsOn(call.Page)
	forms.arm(call.Page)
	defer forms.disarm()
	pressed := e.press(call, selector)
	body, saw := e.heardOut(call, forms, e.await(call, forms))
	if body != nil {
		return body, pressed + " " + element + ", " + saw
	}
	return nil, pressed + " " + element + ": " + saw
}

// press is a pointer click on a marked element, and failing that a click from
// the page's own script, for an element nobody could see to press.
func (e *Erie) press(call Call, selector string) string {
	if visible, err := call.Page.ClickVisible(selector); err == nil && visible {
		return "pressed"
	}
	_, _ = call.Page.Evaluate(erieScriptClickScript, selector)
	return "pressed from a script, a pointer click not landing,"
}

type erieOwnForm struct {
	Found   bool   `json:"found"`
	Forms   int    `json:"forms"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Angular string `json:"angular"`
	Doc     bool   `json:"doc"`
	Filled  []struct {
		Name string `json:"name"`
		From string `json:"from"`
	} `json:"filled"`
}

// submitOwnForm fills and submits the documents page's own hidden form to the
// statement path, with the browser listened to. A form that opened a popup
// and brought no file is submitted again in this window, its target taken
// away, once the popup has been followed: an access policy the popup passed
// through may hold for this window's next request.
func (e *Erie) submitOwnForm(call Call, ref erieStatementRef) ([]byte, string) {
	body, said, popped := e.submitOwn(call, ref, false)
	if body != nil || !popped {
		return body, said
	}
	again, saw, _ := e.submitOwn(call, ref, true)
	if again != nil {
		return again, said + "; submitted again in this window, " + saw
	}
	return nil, said + "; submitted again in this window: " + saw
}

// submitOwn is one submit of the page's own form, and whether a popup opened;
// self takes the form's target away. The second answer is the whole line for
// the first submit and only what the browser made of it for a second.
func (e *Erie) submitOwn(call Call, ref erieStatementRef, self bool) ([]byte, string, bool) {
	forms := e.formsOn(call.Page)
	forms.arm(call.Page)
	defer forms.disarm()
	var own erieOwnForm
	err := browser.EvaluateInto(call.Page, erieOwnFormScript, map[string]any{
		"handle": ref.Handle, "id": ref.ID, "row": ref.Row, "known": erieKnownFields(ref), "submit": true, "self": self,
	}, &own)
	if err != nil {
		return nil, "the page's own form could not be filled (" + err.Error() + ")", false
	}
	if !own.Found {
		return nil, "the page has no form of its own to the statement path", false
	}
	heard := e.await(call, forms)
	popped := len(heard.popups) > 0
	body, saw := e.heardOut(call, forms, heard)
	if self {
		return body, saw, popped
	}
	var order []string
	sources := map[string][]string{}
	for _, field := range own.Filled {
		if _, held := sources[field.From]; !held {
			order = append(order, field.From)
		}
		sources[field.From] = append(sources[field.From], erieName(field.Name))
	}
	fields := make([]string, 0, len(order))
	for _, from := range order {
		fields = append(fields, strings.Join(sources[from], " ")+" from "+from)
	}
	submitted := fmt.Sprintf("submitted the page's own form to %s (one of %d, target %s) with %s",
		erieAddress(own.Action), own.Forms, cmp.Or(erieName(own.Target), "none"), strings.Join(fields, ", "))
	if body != nil {
		return body, submitted + ", " + saw, popped
	}
	return nil, submitted + ": " + saw, popped
}

// erieScalars is a row's scalar fields as text, as a form input takes them.
func erieScalars(row map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range row {
		switch value.(type) {
		case string, float64, bool, json.Number:
			out[key] = Text(value)
		}
	}
	return out
}

// erieKnownFields is what the module knows of the statement form's inputs,
// with where it knows it from, for an input neither the page nor the document
// list fills.
func erieKnownFields(ref erieStatementRef) map[string]any {
	field := func(value, from string) map[string]any { return map[string]any{"value": value, "from": from} }
	return map[string]any{
		"referenceId":        field("", "the module's constant"),
		"documentHandle":     field(ref.Handle, "the document list"),
		"transactionName":    field("Views Document", "the module's constant"),
		"documentType":       field("Invoice", "the module's constant"),
		"origin":             field("DocumentsPage", "the module's constant"),
		"onlineAccountId":    field(ref.Account, "the account page's links"),
		"policyNumber":       field(ref.Policy, "the document list"),
		"policySourceSystem": field("PMS", "the document list's own query"),
		"startDate":          field(ref.From, "the document window"),
		"endDate":            field(ref.To, "the document window"),
	}
}

// heardOut is what a press or a submit brought: the PDF and how it came, or
// what the browser saw and whether a popup opened. A popup is followed
// (followPopup), and one that rests anywhere but an access policy page is
// opened again in a page of its own, which reads a PDF viewer's file or a
// download alike. Popups are closed.
func (e *Erie) heardOut(call Call, forms *erieForms, heard erieHeard) ([]byte, string) {
	if heard.body != nil {
		e.closePopups(heard.popups)
		return heard.body, heard.how
	}
	if len(heard.popups) == 0 {
		return nil, "the browser saw " + heard.saw() + "; no popup opened"
	}
	popup := heard.popups[0]
	opened := eriePlace(popup.URL())
	body, followed := e.followPopup(call, forms, popup)
	defer func() {
		_, _, popups := forms.heard()
		e.closePopups(append(heard.popups, popups...))
	}()
	if body != nil {
		return body, "as the file the popup it opened brought, " + followed
	}
	if at := popup.URL(); strings.HasPrefix(at, "http") && !erieAPMPath.MatchString(at) {
		if body, _ := e.openStatement(call, at); body != nil {
			return body, "as the file the popup it opened shows, at " + eriePlace(at)
		}
	}
	return nil, "the browser saw " + heard.saw() + "; a popup opened at " + opened + " and brought no PDF: " + followed
}

func (e *Erie) closePopups(popups []browser.Page) {
	for _, popup := range popups {
		_ = popup.Close()
	}
}

// erieAPMPath is an F5 BIG-IP access policy page: a request the gateway holds
// until its policy has run lands on one of these, and the file comes later.
var erieAPMPath = regexp.MustCompile(`(?i)/(?:vdesk|my\.policy|my\.logon\.php3|my\.logout\.php3)(?:/|$|\?)`)

// erieAPMCookies are the gateway's own session cookies, named by the vendor
// rather than by anybody's account, so a note may say which are held.
var erieAPMCookies = []string{"MRHSession", "LastMRH_Session", "F5_ST", "F5_fullWT", "MRHSHint", "TIN"}

// erieAPMRounds bounds how many times a popup resting on an access policy
// page is acted on and listened to again.
const erieAPMRounds = 3

type erieAPM struct {
	Title    string   `json:"title"`
	Headings []string `json:"headings"`
	Forms    []struct {
		Action  string   `json:"action"`
		Method  string   `json:"method"`
		Inputs  []string `json:"inputs"`
		Passing bool     `json:"passing"`
	} `json:"forms"`
	Scripted bool          `json:"scripted"`
	Controls []erieElement `json:"controls"`
	Acted    string        `json:"acted"`
}

// followPopup follows a popup until it brings the file or stops moving. On an
// access policy page it passes the page on as the page itself would
// (erieAPMScript), once per address, and listens again. It answers the PDF,
// or where the popup rests: its address, title, headings, forms, and which of
// the gateway's session cookies the browser holds.
func (e *Erie) followPopup(call Call, forms *erieForms, popup browser.Page) ([]byte, string) {
	var steps []string
	acted := map[string]bool{}
	var page erieAPM
	for round := 0; round < erieAPMRounds; round++ {
		at := popup.URL()
		act := erieAPMPath.MatchString(at) && !acted[at]
		page = erieAPM{}
		if err := browser.EvaluateInto(popup, erieAPMScript, map[string]any{"act": act}, &page); err != nil {
			steps = append(steps, "the popup at "+eriePlace(at)+" could not be read ("+err.Error()+")")
			break
		}
		switch page.Acted {
		case "form":
			acted[at] = true
			steps = append(steps, "at "+eriePlace(at)+" it submitted the page's own form")
		case "control":
			acted[at] = true
			steps = append(steps, "at "+eriePlace(at)+" it pressed the page's continue control")
		}
		heard := e.await(call, forms)
		if heard.body != nil {
			return heard.body, strings.Join(append(steps, heard.how), "; ")
		}
		if page.Acted == "" && popup.URL() == at {
			break
		}
	}
	steps = append(steps, e.popupRests(popup, page))
	return nil, strings.Join(steps, "; ")
}

// popupRests says where a popup rests, by names and addresses only.
func (e *Erie) popupRests(popup browser.Page, page erieAPM) string {
	parts := []string{"it rests at " + eriePlace(popup.URL())}
	if page.Title != "" {
		parts = append(parts, fmt.Sprintf("titled %q", page.Title))
	}
	if len(page.Headings) > 0 {
		quoted := make([]string, len(page.Headings))
		for index, heading := range page.Headings {
			quoted[index] = fmt.Sprintf("%q", heading)
		}
		parts = append(parts, "headings "+strings.Join(quoted, " "))
	}
	for _, form := range page.Forms {
		kind := "a"
		if form.Passing {
			kind = "a hidden"
		}
		parts = append(parts, fmt.Sprintf("%s %s form to %s with inputs %s", kind, erieName(form.Method),
			erieAddress(form.Action), cmp.Or(erieNames(form.Inputs), "none")))
	}
	if len(page.Forms) == 0 {
		parts = append(parts, "no form")
	}
	if page.Scripted {
		parts = append(parts, "a script that submits a form")
	}
	if len(page.Controls) > 0 {
		parts = append(parts, "continue controls "+erieElements(page.Controls))
	}
	names, err := popup.CookieNames(erieHome, erieIdentityHome)
	switch {
	case err != nil:
		parts = append(parts, "its cookies could not be read")
	default:
		var held []string
		for _, name := range erieAPMCookies {
			if slices.Contains(names, name) {
				held = append(held, name)
			}
		}
		parts = append(parts, fmt.Sprintf("the gateway's session cookies held: %s, beside %d other cookie%s",
			cmp.Or(strings.Join(held, " "), "none"), len(names)-len(held), plural(len(names)-len(held))))
	}
	return strings.Join(parts, ", ")
}

// followStatement takes a statement round a page call that is refused it. The
// call is asked again without following redirects, which the page may not
// read but the browser shows, so the redirect's target is opened in a page of
// its own; then the form is submitted as the page submits it, and the PDF is
// whatever the browser was answered or handed. It answers the PDF, or nil, and
// what each way came to.
func (e *Erie) followStatement(call Call, headers map[string]string, form url.Values) ([]byte, string) {
	probed, target := e.probeStatement(call, headers, form)
	said := []string{"asked again without following redirects it " + probed}
	read := func(how string) string { return strings.Join(append(said, "the statement was read "+how), "; ") }
	if target != "" {
		body, opened := e.openStatement(call, target)
		if body != nil {
			return body, read("by opening the redirect it answered, at " + eriePlace(target))
		}
		said = append(said, opened)
	}
	heard := e.postStatement(call, form)
	if heard.body != nil {
		return heard.body, read("by submitting the documents page's form, " + heard.how)
	}
	said = append(said, "submitted as the page's form, the browser saw "+heard.saw())
	if heard.target != "" && heard.target != target {
		body, opened := e.openStatement(call, heard.target)
		if body != nil {
			return body, read("by opening the redirect the page's form answered, at " + eriePlace(heard.target))
		}
		said = append(said, opened)
	}
	return nil, strings.Join(said, "; ")
}

// probeStatement asks again without following redirects, which tells a
// redirect the page may not follow from a call refused outright, and answers
// the redirect's target when the browser showed it.
func (e *Erie) probeStatement(call Call, headers map[string]string, form url.Values) (string, string) {
	forms := e.formsOn(call.Page)
	forms.arm(call.Page)
	defer forms.disarm()
	answer, err := browser.CallFromPage(call.Ctx, call.Page, browser.PageCall{
		URL: erieHome + erieStatementPath, Method: "POST", Headers: headers, Body: form.Encode(),
		Redirect: "manual", Read: browser.ReadBytes,
	})
	switch {
	case err != nil:
		return "could not be made (" + err.Error() + ")", ""
	case answer.Redirected:
		target := ""
		for look := 0; look < 2 && target == ""; look++ {
			if look > 0 {
				call.Page.Sleep(erieFormLook)
			}
			seen, _, _ := forms.heard()
			for _, response := range seen {
				if target = erieRedirectTarget(response); target != "" {
					break
				}
			}
		}
		if target == "" {
			return "answered a redirect the browser showed no target for", ""
		}
		return "answered a redirect to " + eriePlace(target), target
	case answer.Error != "":
		return "was refused again (" + answer.Error + ")", ""
	}
	return fmt.Sprintf("answered HTTP %d as %s", answer.Status, cmp.Or(answer.Type, "no content type")), ""
}

// erieRedirectTarget is where a redirect from the statement path points, or "".
func erieRedirectTarget(response browser.Response) string {
	if !strings.Contains(response.URL(), erieStatementPath) {
		return ""
	}
	return erieLocation(response)
}

// erieLocation is where a redirect points, resolved against the address that
// answered it, or "".
func erieLocation(response browser.Response) string {
	if response.Status() < 300 || response.Status() > 399 {
		return ""
	}
	location := strings.TrimSpace(response.Header("location"))
	if location == "" {
		return ""
	}
	base, err := url.Parse(response.URL())
	if err != nil {
		return location
	}
	target, err := base.Parse(location)
	if err != nil {
		return location
	}
	return target.String()
}

// openStatement opens a redirect's target in a page of its own, which answers
// a response, the file a PDF viewer shows, or a download alike.
func (e *Erie) openStatement(call Call, target string) ([]byte, string) {
	place := eriePlace(target)
	status, kind, body, err := call.Page.Bytes(target)
	if err != nil {
		// The browser's error names the whole address, and the address is
		// where the portal puts its identifiers.
		text := strings.ReplaceAll(err.Error(), target, place)
		text = strings.ReplaceAll(text, browser.WithoutQuery(target), place)
		return nil, fmt.Sprintf("opened in a page of its own, %s answered nothing readable (%s)", place, text)
	}
	if isPDF(body) {
		return body, ""
	}
	return nil, fmt.Sprintf("opened in a page of its own, %s answered HTTP %d as %s (not a PDF)",
		place, status, cmp.Or(kind, "no content type"))
}

// erieAnalytics is the hosts of the trackers a page reports to, which a note
// on a statement leaves out.
var erieAnalytics = regexp.MustCompile(`(?i)(?:^|\.)(?:google|doubleclick|googleadservices|googlesyndication|` +
	`googletagmanager|google-analytics|facebook|bing|clarity|hotjar|demdex|omtrdc|adobedtm|linkedin|` +
	`tiktok|nr-data|newrelic|quantserve|pinterest|yahoo|everesttech|adsrvr|criteo|taboola)\.[a-z.]+$`)

// postStatement submits the form as a navigation and waits for what the
// browser makes of it.
func (e *Erie) postStatement(call Call, form url.Values) erieHeard {
	forms := e.formsOn(call.Page)
	forms.arm(call.Page)
	defer forms.disarm()

	names := make([]string, 0, len(form))
	for name := range form {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]any, 0, len(names))
	for _, name := range names {
		fields = append(fields, map[string]any{"name": name, "value": form.Get(name)})
	}
	// A submit that navigates tears down the script's context, so its error
	// says nothing about the post.
	_, _ = call.Page.Evaluate(erieSubmitScript, map[string]any{
		"action": erieHome + erieStatementPath, "fields": fields,
	})
	heard := e.await(call, forms)
	for _, popup := range heard.popups {
		_ = popup.Close()
	}
	return heard
}

// erieHeard is what the browser was answered, handed and opened after a
// statement was asked for.
type erieHeard struct {
	// body is the first PDF, and how says how it came.
	body []byte
	how  string
	// lines are what came instead, for a note.
	lines []string
	// target is where the statement path redirected, when it did.
	target string
	popups []browser.Page
}

func (h erieHeard) saw() string {
	if len(h.lines) == 0 {
		return fmt.Sprintf("no response and no download within %s", erieFormWait)
	}
	return strings.Join(h.lines, ", ")
}

// await takes the first PDF the browser is answered or handed as a download,
// and otherwise says what it saw: analytics responses only counted, each
// redirect with where it led. It waits erieFormWait for anything at all, and
// once something has come, until erieFormQuiet passes with nothing more or
// erieChainWait in all, so a sign-in round trip is followed to its end.
func (e *Erie) await(call Call, forms *erieForms) erieHeard {
	var heard erieHeard
	tell := func(line string) {
		if len(heard.lines) < erieChainHeard {
			heard.lines = append(heard.lines, line)
		}
	}
	trackers, read, saved := 0, 0, 0
	heardAt := time.Duration(-1)
	for waited := time.Duration(0); ; waited += erieFormLook {
		seen, files, popups := forms.heard()
		if len(popups) > len(heard.popups) {
			heardAt = waited
		}
		heard.popups = popups
		for ; saved < len(files); saved++ {
			heardAt = waited
			file := files[saved]
			got, err := file.Bytes()
			if err == nil && isPDF(got) {
				heard.body, heard.how = got, "as a download from "+eriePlace(file.URL())
				return heard
			}
			what := "not a PDF"
			if err != nil {
				what = "could not be saved"
			}
			tell(fmt.Sprintf("a download from %s (%s)", eriePlace(file.URL()), what))
		}
		for ; read < len(seen); read++ {
			response := seen[read]
			if erieAnalytics.MatchString(erieHost(response.URL())) {
				trackers++
				continue
			}
			heardAt = waited
			if to := erieRedirectTarget(response); to != "" && heard.target == "" {
				heard.target = to
			}
			if to := erieLocation(response); to != "" {
				tell(fmt.Sprintf("HTTP %d at %s redirecting to %s",
					response.Status(), eriePlace(response.URL()), eriePlace(to)))
				continue
			}
			got, err := response.Body()
			if err == nil && isPDF(got) {
				heard.body, heard.how = got, "as the response from "+eriePlace(response.URL())
				return heard
			}
			what := "not a PDF"
			if err != nil {
				what = "no readable body"
			}
			tell(fmt.Sprintf("HTTP %d at %s (%s)", response.Status(), eriePlace(response.URL()), what))
		}
		quiet := heardAt < 0 && waited >= erieFormWait || heardAt >= 0 && waited-heardAt >= erieFormQuiet
		if quiet || waited >= erieChainWait || call.Ctx.Err() != nil {
			break
		}
		call.Page.Sleep(erieFormLook)
	}
	if trackers > 0 {
		heard.lines = append(heard.lines, fmt.Sprintf("%d analytics response%s left out", trackers, plural(trackers)))
	}
	return heard
}

func erieHost(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// eriePlace is an address as a note may name it: the host and the path up to
// the first segment that carries a digit, which is where the portal puts the
// account id and the policy number.
func eriePlace(address string) string {
	parsed, err := url.Parse(address)
	if err == nil && parsed.Host == "" && parsed.Scheme != "" {
		return "a " + parsed.Scheme + ": address"
	}
	if err != nil || parsed.Host == "" {
		return "a page with no address"
	}
	kept := []string{parsed.Host}
	for _, segment := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
		if segment == "" || strings.ContainsAny(segment, "0123456789") {
			break
		}
		kept = append(kept, segment)
	}
	return strings.Join(kept, "/")
}

// eriePageFetcher makes calls inside the page, so the portal's session cookies
// go with them and nothing is copied out of the browser.
func eriePageFetcher(page browser.Page) *browser.PageFetcher {
	return &browser.PageFetcher{
		Origin: erieHome,
		Open: func() (browser.FetchSurface, error) {
			return browser.FetchSurface{Page: page}, nil
		},
	}
}

func (e *Erie) FetchDocument(call Call, bill Bill) (*Document, error) {
	raw := rawOf[erieRaw](bill)
	if raw.DocumentHandle == "" || call.Page == nil {
		return nil, nil
	}
	form := url.Values{
		"referenceId":     {""},
		"documentHandle":  {raw.DocumentHandle},
		"transactionName": {"Views Document"},
		"documentType":    {"Invoice"},
		"origin":          {"DocumentsPage"},
		"onlineAccountId": {raw.OnlineAccount},
		"policyNumber":    {raw.DocumentPolicy},
		"startDate":       {raw.StartDate},
		"endDate":         {raw.EndDate},
	}
	body, ok := e.statement(call, raw.OnlineAccount, form, erieStatementRef{
		Handle: raw.DocumentHandle, ID: raw.DocumentID, Policy: raw.DocumentPolicy, Printed: bill.IssuedOn,
		Account: raw.OnlineAccount, From: raw.StartDate, To: raw.EndDate, Row: map[string]any{},
	})
	if !ok {
		return nil, nil
	}
	return pdfDocument(body, statementFilename("erie", bill,
		cmp.Or(bill.IssuedOn, bill.DueOn, "invoice"))), nil
}

func erieDocumentsPage(account string) string {
	return erieHome + "/DocumentListWeb/Documents/MyDocuments/account/" + url.PathEscape(account) + "/Policy/0"
}

func erieInquiryLink(account, policy string) string {
	return erieHome + "/BillingCenterWeb/Inquiry/Account/" + url.PathEscape(account) +
		"/Policy/" + url.PathEscape(policy) + "/VendorId/" + erieVendor
}

func erieTermListURL(policy string) string {
	return erieHome + "/BillingCenterWeb/DetailedTransActivity/GetTermList?policyNumber=" +
		url.QueryEscape(policy) + "&vendorId="
}

func erieActivityURL(policy string) string {
	return erieHome + "/BillingCenterWeb/DetailedTransActivity/GetActivity?policyNumber=" +
		url.QueryEscape(policy) + "&accountNumber=&vendorId=" + erieVendor
}

func erieInstallmentsURL(policy string) string {
	return erieHome + "/BillingCenterWeb/Modal/GetFutureInstallments?policyNumber=" +
		url.QueryEscape(policy) + "&accountNumber=&vendorId=" + erieVendor
}

func erieDocumentsURL(account, policy, from, to string) string {
	query := url.Values{
		"endDate":               {to},
		"filterValue":           {erieDocumentWindow},
		"isDateDropdownChanged": {"false"},
		"onlineAccountId":       {account},
		"policyNumber":          {policy},
		"policySourceSystem":    {"PMS"},
		"startDate":             {from},
		"transactionName":       {erieDocumentTransaction},
	}
	return erieHome + "/DocumentListWeb/MyDocuments/GetDocuments?" + query.Encode()
}

func erieWindow(today time.Time) (string, string) {
	return today.AddDate(0, 0, -erieDocumentWindowDays).Format("2006-01-02"), today.Format("2006-01-02")
}

func erieStatementForm(account ErieAccount, invoice map[string]any, from, to string) url.Values {
	return url.Values{
		"referenceId":     {""},
		"documentHandle":  {Text(Pick(invoice, "documentHandle"))},
		"transactionName": {"Views Document"},
		"documentType":    {"Invoice"},
		"origin":          {"DocumentsPage"},
		"onlineAccountId": {account.ID},
		"policyNumber":    {Text(Pick(invoice, "policyNumber"))},
		"startDate":       {from},
		"endDate":         {to},
	}
}

// ErieRead is everything one policy's pages answered, in the portal's shapes.
type ErieRead struct {
	Policy string
	// Account is the account's own id, kept on each bill so the statement
	// form can be posted again without walking the landing page.
	Account  string
	Terms    []map[string]any
	Activity []map[string]any
	// Installments is the installments call's answer as it came, whatever its
	// shape; InstallmentsMissed is what went wrong asking, when something did.
	Installments       any
	InstallmentsMissed string
	Documents          []map[string]any
	// InvoiceText is each invoice PDF's text layer, by document handle. Empty
	// for an invoice whose PDF would not answer or carries no text.
	InvoiceText map[string]string
	// TileDue is the summary tile's line beside "Minimum due", which is where
	// the due date is when the invoice does not say.
	TileDue string
	// Window is the document window the invoices were listed for, for the form
	// that opens one again.
	Window [2]string
}

// ErieInvoices is the invoices of one policy, newest first and capped.
func ErieInvoices(documents []map[string]any, policy string) []map[string]any {
	want := eriePolicyKey(policy)
	found := make([]map[string]any, 0, len(documents))
	for _, row := range documents {
		if !strings.EqualFold(strings.TrimSpace(Text(Pick(row, "documentType"))), "Invoice") {
			continue
		}
		// The documents call hyphenates its policy numbers where the rest of
		// the portal does not; a policy is a policy either way.
		if number := Text(Pick(row, "policyNumber")); number != "" && eriePolicyKey(number) != want {
			continue
		}
		found = append(found, row)
	}
	sort.SliceStable(found, func(a, b int) bool {
		return erieIssuedOn(found[a]) > erieIssuedOn(found[b])
	})
	if len(found) > erieInvoicesToRead {
		found = found[:erieInvoicesToRead]
	}
	return found
}

func erieIssuedOn(invoice map[string]any) string {
	return ISODate(Pick(invoice, "printDate", "ecmPrintDate", "effectiveDate", "processDateTime"))
}

// ErieBillsFromPolicy is one policy's bills: each invoice in the window (an
// annual policy bills once a year, so what is owed alone would show nothing for
// eleven months), with `policyMinDue` folded into the invoice it belongs to.
//
// An invoice's due date is its own text's, then its row's, then the first day
// Erie answers as due in its cycle (erieDueDays). The amount owed's is the
// newest invoice's text, then the term's, then the first unpaid installment,
// then the tile. A bill with none of them is not filed: the day an invoice was
// printed is not the day it is due, and Erie publishes no gap between the two.
//
// A policy whose term would not answer is still its invoices; only what it
// owes now is unknown.
func ErieBillsFromPolicy(read ErieRead, notes *Notes) []Bill {
	term := erieTerm(read.Terms)
	if term == nil {
		notes.Addf("Erie Insurance listed no term for the policy ending %s; its invoices are read "+
			"without what the policy owes now", domain.LastFour(read.Policy))
	}
	paid := eriePayments(read.Activity)
	autopay := erieOnAutopay(term, read.Installments)
	installments := erieInstallments(read.Installments)
	cycles := erieDueDays(read, term, installments, paid, autopay)
	owed, owes := Amount(Pick(term, "policyMinDue"))
	owes = owes && Owes(owed)

	var bills []Bill
	undated := false
	invoices := ErieInvoices(read.Documents, read.Policy)
	for index, invoice := range invoices {
		handle := Text(Pick(invoice, "documentHandle"))
		text := read.InvoiceText[handle]
		issued := erieIssuedOn(invoice)
		amount, found := erieAmountInText(text)
		if !found {
			// The pay plan's own instalment, for an invoice whose PDF would
			// not open or carries no text layer.
			amount, found = Amount(Pick(term, "paymentAmount", "currentInstallment"))
		}
		if !found {
			notes.Addf("an Erie invoice of %s on the policy ending %s carried no readable amount; it is left out",
				cmp.Or(issued, "an unknown day"), domain.LastFour(read.Policy))
			continue
		}
		due := cmp.Or(erieDateInText(text), erieDueIn(invoice))
		if due == "" {
			next := ""
			if index > 0 {
				next = erieIssuedOn(invoices[index-1])
			}
			due = erieDueInCycle(cycles, issued, next)
		}
		// The newest invoice's due date, when something is owed, is settled
		// with the amount owed below, and so is the note when there is none.
		if due == "" && !(index == 0 && owes) {
			notes.Addf("an Erie invoice of %s on the policy ending %s states no due date, and nothing Erie "+
				"answered falls due in its cycle; it is not filed",
				cmp.Or(issued, "an unknown day"), domain.LastFour(read.Policy))
			undated = true
			continue
		}
		status := "Open"
		if erieSettled(paid, cmp.Or(issued, due), amount) {
			status = "Paid"
		}
		raw, _ := json.Marshal(erieRaw{
			DocumentHandle: handle,
			DocumentID:     Text(Pick(invoice, "documentId")),
			DocumentPolicy: Text(Pick(invoice, "policyNumber")),
			OnlineAccount:  read.Account,
			StartDate:      read.Window[0],
			EndDate:        read.Window[1],
		})
		bills = append(bills, Bill{
			Subaccount: read.Policy,
			ExternalID: read.Policy + ":" + cmp.Or(issued, due),
			IssuedOn:   issued,
			DueOn:      due,
			AmountDue:  amount,
			Currency:   "USD",
			AutopayOn:  erieAutopayOn(autopay, due, status),
			Status:     status,
			Raw:        raw,
		})
	}

	if owes {
		due := erieDueDate(read, term, installments)
		// The amount owed is the newest invoice's, not a bill of its own: two
		// would be one reminder amended by the other for ever.
		if len(bills) > 0 {
			bills[0].AmountDue = owed
			bills[0].Status = "Open"
			bills[0].DueOn = cmp.Or(due, bills[0].DueOn)
			bills[0].AutopayOn = erieAutopayOn(autopay, bills[0].DueOn, "Open")
		} else if due != "" {
			bills = append(bills, Bill{
				Subaccount: read.Policy,
				ExternalID: read.Policy + ":" + due,
				DueOn:      due,
				AmountDue:  owed,
				Currency:   "USD",
				AutopayOn:  erieAutopayOn(autopay, due, "Open"),
				Status:     "Open",
			})
		}
		if len(bills) == 0 || bills[0].DueOn == "" {
			notes.Addf("Erie Insurance shows an amount owed on the policy ending %s and no due date in its "+
				"invoice, its term, its installments or its summary tile; it is not filed",
				domain.LastFour(read.Policy))
			undated = true
		}
	}

	filed := bills[:0]
	for _, bill := range bills {
		if bill.DueOn != "" {
			filed = append(filed, bill)
		}
	}
	if undated {
		notes.Add(erieWhatWasRead(read, term, invoices))
	}
	return filed
}

// erieWhatWasRead is a note for a bill left without a due date: what each
// source answered, as keys and types and never values, so the next pull says
// where the date went.
func erieWhatWasRead(read ErieRead, term map[string]any, invoices []map[string]any) string {
	pdfs, texts, labelled := 0, 0, 0
	for _, invoice := range invoices {
		text, held := read.InvoiceText[Text(Pick(invoice, "documentHandle"))]
		if !held {
			continue
		}
		pdfs++
		if strings.TrimSpace(text) != "" {
			texts++
		}
		if erieDateInText(text) != "" {
			labelled++
		}
	}
	installments := cmp.Or(read.InstallmentsMissed, Shape(read.Installments))
	row := "none"
	if len(invoices) > 0 {
		row = Shape(invoices[0])
	}
	termShape := "none"
	if term != nil {
		termShape = Shape(term)
	}
	tile := "read nothing"
	switch {
	case DayIn(read.TileDue) != "":
		tile = "carries a date"
	case strings.TrimSpace(read.TileDue) != "":
		tile = "carries no date"
	}
	return fmt.Sprintf("what Erie Insurance answered for the policy ending %s, keys and types only: "+
		"invoice PDFs %d of %d read, %d with text, %d with a due-date label (%s); "+
		"installments call %s; term %s; activity %s; invoice row %s; summary tile %s",
		domain.LastFour(read.Policy), pdfs, len(invoices), texts, labelled, strings.Join(erieDueLabels, ", "),
		installments, termShape, ShapeEvery(read.Activity), row, tile)
}

// erieTerm is the term a bill belongs to: the one in force, and failing that
// the one that started most recently.
func erieTerm(terms []map[string]any) map[string]any {
	var best map[string]any
	var bestOn string
	for _, row := range terms {
		on := ISODate(Pick(row, "expirationDate"))
		inForce := strings.EqualFold(strings.TrimSpace(Text(Pick(row, "policyStatusDescription"))), "In Force")
		switch {
		case best == nil,
			inForce && !strings.EqualFold(strings.TrimSpace(Text(Pick(best, "policyStatusDescription"))), "In Force"),
			inForce && on > bestOn:
			best, bestOn = row, on
		}
	}
	return best
}

type eriePayment struct {
	on     string
	amount domain.Money
}

func eriePayments(activity []map[string]any) []eriePayment {
	var found []eriePayment
	for _, row := range activity {
		kind := strings.ToUpper(strings.TrimSpace(Text(Pick(row, "transactionType"))))
		category := strings.TrimSpace(Text(Pick(row, "transactionCategory")))
		if kind != "PAY" && !strings.EqualFold(category, "Payments") {
			continue
		}
		on := ISODate(Pick(row, "transactionEffectiveDate", "transactionEntryDate"))
		if on == "" {
			continue
		}
		// The ledger writes a payment as a credit, so its sign is the
		// ledger's and not the payment's.
		amount, read := Amount(Pick(row, "transactionAmount"))
		if !read {
			continue
		}
		found = append(found, eriePayment{on: on, amount: amount.Abs()})
	}
	return found
}

// erieSettled: a bill is paid once payments on or after its own day cover it.
//
// The portal marks nothing paid; what it has is a ledger, and a payment dated
// inside a cycle is that cycle's.
func erieSettled(payments []eriePayment, on string, owed domain.Money) bool {
	if on == "" {
		return false
	}
	total := domain.Zero
	for _, payment := range payments {
		if payment.on >= on {
			total = total.Add(payment.amount)
		}
	}
	return total.Cmp(owed) >= 0
}

// erieOnAutopay reads both calls, which say it differently: the term carries
// the payment method's token id, the installments call carries the flag.
// Neither is a date; Erie draws the money on the due date.
func erieOnAutopay(term map[string]any, installments any) bool {
	if Text(Pick(term, "recurringEFTTokenId")) != "" {
		return true
	}
	answer, _ := installments.(map[string]any)
	return Flag(answer["isAutoPay"])
}

// erieAutopayOn is the day the money leaves: the due date, and only for a bill
// that is still open on a policy enrolled in recurring payments.
func erieAutopayOn(autopay bool, due, status string) string {
	if !autopay || due == "" || status != "Open" {
		return ""
	}
	return due
}

// erieDueDate is the day the amount owed is owed on: the newest invoice's own
// text, then the term's own due date, then the first installment not yet paid,
// then the summary tile. Only the newest invoice: an older one's date is an
// older bill's.
func erieDueDate(read ErieRead, term map[string]any, installments []erieInstallment) string {
	if invoices := ErieInvoices(read.Documents, read.Policy); len(invoices) > 0 {
		if due := erieDateInText(read.InvoiceText[Text(Pick(invoices[0], "documentHandle"))]); due != "" {
			return due
		}
	}
	if due := erieDueIn(term); due != "" {
		return due
	}
	for _, installment := range installments {
		if !installment.paid {
			return installment.due
		}
	}
	return DayIn(read.TileDue)
}

// erieDueIn is the due date a row carries: under a spelling the portal is
// known to use, then under any key that names one, in key order.
func erieDueIn(row map[string]any) string {
	if row == nil {
		return ""
	}
	if due := ISODate(Pick(row, erieDueKeys...)); due != "" {
		return due
	}
	keys := make([]string, 0, len(row))
	for key := range row {
		if erieDueKey.MatchString(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if due := ISODate(row[key]); due != "" {
			return due
		}
	}
	return ""
}

type erieInstallment struct {
	due  string
	paid bool
}

// erieInstallments is every row the installments call answers that carries a
// due date, in whatever list and under whatever key, earliest first. The
// answer's own top-level due date, when it carries one, is an unpaid
// installment of its own.
func erieInstallments(answer any) []erieInstallment {
	var found []erieInstallment
	var walk func(node any, depth int)
	walk = func(node any, depth int) {
		if depth > 4 {
			return
		}
		switch typed := node.(type) {
		case []any:
			for _, item := range typed {
				row, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if due := erieDueIn(row); due != "" {
					found = append(found, erieInstallment{due: due, paid: erieInstallmentPaid(row)})
					continue
				}
				walk(row, depth+1)
			}
		case map[string]any:
			for _, value := range typed {
				walk(value, depth+1)
			}
		}
	}
	walk(answer, 0)
	if top, ok := answer.(map[string]any); ok {
		if due := erieDueIn(top); due != "" {
			found = append(found, erieInstallment{due: due})
		}
	}
	sort.SliceStable(found, func(a, b int) bool { return found[a].due < found[b].due })
	return found
}

func erieInstallmentPaid(row map[string]any) bool {
	status := strings.ToLower(strings.TrimSpace(Text(Pick(row, "status", "installmentStatus", "statusDescription"))))
	return strings.HasPrefix(status, "paid") || Flag(Pick(row, "isPaid", "paid"))
}

// erieDueDays is every day Erie answers as a due date, earliest first: the
// installments, the activity ledger's rows, the term, the summary tile and,
// on a policy drawn automatically, the payments, because the draft is taken
// on the due date.
func erieDueDays(
	read ErieRead, term map[string]any, installments []erieInstallment, payments []eriePayment, autopay bool,
) []string {
	seen := map[string]bool{}
	add := func(day string) {
		if day != "" {
			seen[day] = true
		}
	}
	for _, installment := range installments {
		add(installment.due)
	}
	for _, row := range read.Activity {
		add(erieDueIn(row))
	}
	add(erieDueIn(term))
	add(DayIn(read.TileDue))
	if autopay {
		for _, payment := range payments {
			add(payment.on)
		}
	}
	days := make([]string, 0, len(seen))
	for day := range seen {
		days = append(days, day)
	}
	sort.Strings(days)
	return days
}

// erieInvoiceCycleDays bounds how long after its printing an invoice can fall
// due: a day further out than this is a later cycle's.
const erieInvoiceCycleDays = 45

// erieDueInCycle is the first of days that falls in an invoice's cycle: on or
// after the day it was printed, before the next invoice was, and within
// erieInvoiceCycleDays.
func erieDueInCycle(days []string, issued, next string) string {
	if issued == "" {
		return ""
	}
	printed, err := domain.ParseDate(issued)
	if err != nil {
		return ""
	}
	until := printed.AddDays(erieInvoiceCycleDays).String()
	if next != "" && next < until {
		until = next
	}
	for _, day := range days {
		if day >= issued && day < until {
			return day
		}
	}
	return ""
}

// erieDateInText and erieAmountInText read the invoice through
// internal/billmail, which already knows this company's wording.
func erieDateInText(text string) string {
	if text == "" {
		return ""
	}
	for _, label := range erieDueLabels {
		if day := DayIn(billmail.AfterLabel(text, label)); day != "" {
			return day
		}
	}
	return ""
}

func erieAmountInText(text string) (domain.Money, bool) {
	if text == "" {
		return domain.Zero, false
	}
	for _, label := range erieAmountLabels {
		if amount, ok := billmail.ParseAmount(billmail.AfterLabel(text, label)); ok && !amount.IsZero() {
			return amount, true
		}
	}
	return domain.Zero, false
}

func erieCy(count int) string {
	if count == 1 {
		return "y"
	}
	return "ies"
}
