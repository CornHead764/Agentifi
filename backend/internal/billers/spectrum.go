package billers

import (
	"cmp"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Spectrum (Charter) reads the billing GraphQL behind spectrum.net from the
// kept browser profile. See docs/connectors/providers.md.
//
// The digital statement list's ids name a DIGITAL_DOCUMENT, and
// `fetchAccountStatementList`'s name the PDF_DOCUMENT for the same date, so the
// PDF id is looked up by date.
//
// The billing endpoints accept only the token the signed-in app holds, so the
// module makes its calls from inside the page; the token never leaves the
// browser. An init script records the Authorization header the app itself
// sends, rather than replaying the app's refresh. A page that never sends one
// is not signed in.

const (
	spectrumHome    = "https://www.spectrum.net"
	spectrumBilling = spectrumHome + "/billing"
	spectrumGraph   = "https://apis.spectrum.net/selfservice/graph"

	spectrumStatementsToRead = 3

	// A signed-in portal sends its first bearer within a second; this is the
	// ceiling before the answer is "not signed in".
	spectrumBearerWait = 45 * time.Second
)

// spectrumBearerHook runs before the app: whichever of XHR or fetch carries the
// bearer, the header's value is kept on the window for the module's own calls.
const spectrumBearerHook = `(() => {
  const keep = (value) => { if (value && /^Bearer /i.test(String(value))) window.__agentifiBearer = String(value); };
  const setHeader = XMLHttpRequest.prototype.setRequestHeader;
  XMLHttpRequest.prototype.setRequestHeader = function (name, value) {
    if (String(name).toLowerCase() === 'authorization') keep(value);
    return setHeader.call(this, name, value);
  };
  const originalFetch = window.fetch;
  window.fetch = function (input, init) {
    try {
      const headers = init && init.headers;
      if (headers instanceof Headers) keep(headers.get('authorization'));
      else if (headers) keep(headers.authorization || headers.Authorization);
    } catch {}
    return originalFetch.apply(this, arguments);
  };
})();`

const spectrumHasBearer = `() => Boolean(window.__agentifiBearer)`

// Credentials are included because the API host is a sibling of the page's and
// it wants the app's thumbprint cookie beside the bearer: a cross-site fetch
// carries cookies only when asked to.
var spectrumCall = browser.PageCallScript(
	`return window.__agentifiBearer ? { headers: { authorization: window.__agentifiBearer } } : null;`)

const (
	spectrumAccountsQuery = "query fetchAccountGlobal { accounts { core { accountNumber serviceAddress { line1 city state } } } }"
	spectrumDigitalQuery  = "query fetchDigitalBillStatementsList { viewer { account { digitalStatementList { statements { date id version } } } } }"
	spectrumPDFListQuery  = "query fetchAccountStatementList { viewer { account { statementList { statements { id date } } } } }"
	spectrumBillQuery     = "query fetchDigitalBill($statementId: String!) { viewer { account { digitalStatement(statementId: $statementId) { customer { accountNumber autoPayDate cycleDate endDate paymentDueDate paymentDueText paymentsMade previousBalance startDate statementDate totalAmountDue unpaidBalance serviceAddress { address1 city } } } } } }"
	spectrumPDFQuery      = "query FetchStatementPDFEncodedString($statementId: String!) { viewer { account { statementPdf(statementId: $statementId) } } }"
)

var spectrumAccountArea = regexp.MustCompile(
	`(?i)spectrum\.net/(?:billing|account|account-summary|myaccount|home|manage|services|profile)`)

// dueInText is the date a household on autopay gets in place of a due date.

type Spectrum struct {
	Draft
}

// NewSpectrum starts the sign-in at the billing page, not `/login`, which
// redirects to a marketing page with no form. Signed out, the billing page
// bounces to id.spectrum.net with the client id, PKCE challenge and redirect
// the form will not render without.
func NewSpectrum() *Spectrum {
	return &Spectrum{Draft{
		BillerID:    domain.BillerSpectrum,
		Home:        spectrumHome,
		SignIn:      spectrumBilling,
		Landing:     spectrumBilling,
		AccountArea: URLMatches(spectrumAccountArea),
	}}
}

// bearerOnPage answers false for a page that is not signed in.
func (s *Spectrum) bearerOnPage(call Call) bool {
	page := call.Page
	if err := page.AddInitScript(spectrumBearerHook); err != nil {
		call.Notes.Tracef("the bearer hook could not be installed: %v", err)
		return false
	}
	// The navigation's own error is not fatal: a portal that redirects mid-load
	// still leaves the browser somewhere, and where it stands is the answer.
	_ = page.Goto(spectrumBilling)
	if err := page.WaitForFunction(spectrumHasBearer, spectrumBearerWait); err != nil {
		call.Notes.Tracef("spectrum.net sent no bearer token from %s; the profile is not signed in",
			browser.WithoutQuery(page.URL()))
		return false
	}
	return true
}

func (s *Spectrum) graph(call Call, operation, query string, variables map[string]any) (PageAnswer, error) {
	if variables == nil {
		variables = map[string]any{}
	}
	return AskPage(call, spectrumCall, browser.PageCall{
		URL:     spectrumGraph,
		Method:  "POST",
		Headers: map[string]string{"content-type": "application/json"},
		Body: map[string]any{
			"operationName": operation,
			"variables":     variables,
			"query":         query,
		},
	})
}

// Subaccounts is one billed account per service address.
func (s *Spectrum) Subaccounts(call Call) ([]Subaccount, error) {
	if call.Page == nil {
		return nil, nil
	}
	if !s.bearerOnPage(call) {
		return nil, ErrNeedsSignIn
	}
	answer, err := s.graph(call, "fetchAccountGlobal", spectrumAccountsQuery, nil)
	if err != nil {
		return nil, err
	}
	if answer.Refused() {
		return nil, ErrNeedsSignIn
	}
	core := answer.Rows("data", "accounts", "core")
	if core == nil {
		call.Notes.Addf("the account walk answered HTTP %d: %s", answer.Status, answer.Excerpt)
		return nil, nil
	}
	found := make([]Subaccount, 0, len(core))
	for _, one := range core {
		number := Text(one["accountNumber"])
		if number == "" {
			continue
		}
		found = append(found, Subaccount{
			ExternalID:   number,
			Label:        spectrumAddressLabel(one["serviceAddress"]),
			MaskedNumber: domain.MaskAccount(number),
		})
	}
	call.Notes.Addf("Spectrum lists %d billed account%s", len(found), plural(len(found)))
	return found, nil
}

func (s *Spectrum) FetchBills(call Call) (Pull, error) {
	if call.Page == nil {
		return s.NoPage(), nil
	}
	if !s.bearerOnPage(call) {
		return s.SignInAgain(nil), nil
	}

	digital, err := s.graph(call, "fetchDigitalBillStatementsList", spectrumDigitalQuery, nil)
	if err != nil {
		return Pull{}, err
	}
	if digital.Refused() {
		return Pull{NeedsSignIn: true, Reason: "Spectrum refused the kept session"}, nil
	}
	statements := digital.Rows("data", "viewer", "account", "digitalStatementList", "statements")
	if statements == nil {
		call.Notes.Addf("the statement list answered HTTP %d: %s; no statements were read",
			digital.Status, digital.Excerpt)
		return Pull{}, nil
	}

	// The PDF ids are a second list, matched to the digital ones by date: the
	// two name different documents for the same statement.
	pdfByDate := map[string]string{}
	if pdfs, err := s.graph(call, "fetchAccountStatementList", spectrumPDFListQuery, nil); err == nil {
		for _, one := range pdfs.Rows("data", "viewer", "account", "statementList", "statements") {
			if id := Text(one["id"]); id != "" {
				pdfByDate[ISODate(one["date"])] = id
			}
		}
	}

	recent := make([]map[string]any, 0, len(statements))
	for _, one := range statements {
		if Text(one["id"]) != "" {
			recent = append(recent, one)
		}
	}
	sort.SliceStable(recent, func(a, b int) bool {
		return ISODate(recent[a]["date"]) > ISODate(recent[b]["date"])
	})
	if len(recent) > spectrumStatementsToRead {
		recent = recent[:spectrumStatementsToRead]
	}

	var bills []Bill
	for index, one := range recent {
		answer, err := s.graph(call, "fetchDigitalBill", spectrumBillQuery,
			map[string]any{"statementId": Text(one["id"])})
		if err != nil {
			return Pull{}, err
		}
		if answer.Refused() {
			return Pull{Bills: bills, NeedsSignIn: true, Reason: "Spectrum refused the kept session"}, nil
		}
		customer, ok := answer.At("data", "viewer", "account", "digitalStatement", "customer").(map[string]any)
		if !ok {
			call.Notes.Addf("the statement of %s answered HTTP %d: %s",
				ISODate(one["date"]), answer.Status, answer.Excerpt)
			continue
		}
		bill, ok := SpectrumBillFromStatement(customer, SpectrumStatementAt{
			PDFID:  pdfByDate[ISODate(one["date"])],
			Newest: index == 0,
			Today:  call.At(),
		}, call.Notes)
		if !ok {
			continue
		}
		if len(call.Subaccounts) > 0 && !call.Wanted(bill.Subaccount) {
			continue
		}
		bills = append(bills, bill)
	}
	call.Notes.Addf("Spectrum answered %d statement%s of the %d it lists",
		len(bills), plural(len(bills)), len(statements))
	return Pull{Bills: bills}, nil
}

func (s *Spectrum) FetchDocument(call Call, bill Bill) (*Document, error) {
	raw := rawOf[spectrumRaw](bill)
	if raw.PDFID == "" || call.Page == nil {
		return nil, nil
	}
	answer, err := s.graph(call, "FetchStatementPDFEncodedString", spectrumPDFQuery,
		map[string]any{"statementId": raw.PDFID})
	if err != nil {
		call.Notes.Addf("the statement PDF for %s could not be asked for: %v", bill.ExternalID, err)
		return nil, nil
	}
	encoded, _ := answer.At("data", "viewer", "account", "statementPdf").(string)
	if encoded == "" {
		call.Notes.Addf("the statement PDF for %s answered HTTP %d: %s",
			bill.ExternalID, answer.Status, answer.Excerpt)
		return nil, nil
	}
	body, ok := decodedPDF(call, "the statement PDF for "+bill.ExternalID,
		strings.TrimPrefix(encoded, "data:application/pdf;base64,"))
	if !ok {
		return nil, nil
	}
	return pdfDocument(body, statementFilename("spectrum", bill,
		cmp.Or(raw.StatementDate, bill.DueOn, "statement"))), nil
}

type SpectrumStatementAt struct {
	// PDFID is the PDF_DOCUMENT id for this statement's date, where the second
	// list had one.
	PDFID  string
	Newest bool
	Today  time.Time
}

type spectrumRaw struct {
	PDFID         string `json:"pdf_id"`
	StatementDate string `json:"statement_date"`
}

// The due date is the statement's own; a household on autopay sometimes gets an
// empty one with the date in `paymentDueText` instead, and then the autopay date
// stands, because that is the day the money leaves. The newest statement is open
// until its due date has passed with nothing left unpaid; every older one is
// paid, whatever its figures say — a cycle that was superseded is not owed twice.
// A newest statement whose unpaid balance will not read stays open with a note:
// calling a bill paid on a figure nobody could read hides one that may be owed.
func SpectrumBillFromStatement(
	customer map[string]any, at SpectrumStatementAt, notes *Notes,
) (Bill, bool) {
	if customer == nil {
		return Bill{}, false
	}
	account := Text(customer["accountNumber"])
	amount, priced := Amount(customer["totalAmountDue"])
	if account == "" || !priced {
		notes.Addf(
			"a Spectrum statement carried no account or no readable total (%s); it is left out",
			jsonish(customer["totalAmountDue"]))
		return Bill{}, false
	}

	issued := ISODate(customer["statementDate"])
	autopay := ISODate(customer["autoPayDate"])
	due := ISODate(customer["paymentDueDate"])
	if due == "" {
		due = cmp.Or(DayIn(Text(customer["paymentDueText"])), autopay)
	}

	unpaid, readable := Amount(customer["unpaidBalance"])
	stillOwed := !readable || Owes(unpaid)
	if at.Newest && !readable {
		notes.Addf("the newest Spectrum statement carried no readable unpaid balance (%s); it is kept open",
			jsonish(customer["unpaidBalance"]))
	}
	dueAhead := due != "" && due >= at.Today.Format("2006-01-02")

	status := "Paid"
	if at.Newest && (stillOwed || dueAhead) {
		status = "Open"
	}
	raw, _ := json.Marshal(spectrumRaw{PDFID: at.PDFID, StatementDate: issued})
	return Bill{
		Subaccount:  account,
		ExternalID:  account + ":" + cmp.Or(issued, due, "undated"),
		IssuedOn:    issued,
		DueOn:       due,
		AmountDue:   amount,
		Currency:    "USD",
		PeriodStart: ISODate(customer["startDate"]),
		PeriodEnd:   ISODate(customer["endDate"]),
		AutopayOn:   autopay,
		Status:      status,
		Raw:         raw,
	}, true
}

func spectrumAddressLabel(address any) string {
	object, ok := address.(map[string]any)
	if !ok {
		return "Internet"
	}
	parts := make([]string, 0, 2)
	for _, key := range []string{"line1", "city"} {
		if value := strings.TrimSpace(Text(object[key])); value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return "Internet"
	}
	return strings.Join(parts, ", ")
}
