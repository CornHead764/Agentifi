package billers

import (
	"cmp"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
)

// TruGreen reads the customer detail the signed-in portal's own app fetches.
// See docs/connectors/providers.md.
//
// The services page's app posts `GetCustomerDetail_ver7` to api.trugreen.com
// with a customer number it encrypts itself and a key header it mints, and the
// same call made by anybody else is refused as anonymous. So the call is not
// made here: the response the app receives is recorded from the browser and
// read. The detail carries every invoice of every year; the page's year
// selector filters them in the page.
//
// `/myaccount` is the entry because signed out it bounces to the form. The
// signed-in landing page has no sign-out link the page reading recognises, so
// the account area must claim it. The area is a function rather than a
// pattern because "this prefix, but not these" needs a lookahead Go's regular
// expressions lack; the public pages are excluded because claiming one would
// finish a sign-in nobody completed.

const (
	truGreenHome     = "https://www.trugreen.com"
	truGreenSignIn   = truGreenHome + "/myaccount"
	truGreenSummary  = truGreenHome + "/my-account/account-summary"
	truGreenServices = truGreenHome + "/my-account/my-services"

	// The app asks for the detail as soon as the services page renders; this
	// is the ceiling before the answer is that it never asked.
	truGreenDetailWait = 45 * time.Second
)

// truGreenDetailCall is the app's customer-detail call, whatever version its
// name carries.
var truGreenDetailCall = regexp.MustCompile(
	`(?i)^https://api\.trugreen\.com/account/GetCustomerDetail_ver\d+(?:[?#]|$)`)

// truGreenPublic is the first path segment under `/my-account/` of the pages a
// signed-out visitor sees there.
var truGreenPublic = regexp.MustCompile(`(?i)^(?:login|logout|log-out|sign-?in|sign-?out|register|forgot|reset)`)

// truGreenInside is TruGreen's account area: its own host, under
// `/my-account/`, and not one of the public pages there.
func truGreenInside(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	if host := strings.ToLower(parsed.Hostname()); host != "www.trugreen.com" && host != "trugreen.com" {
		return false
	}
	rest, under := strings.CutPrefix(strings.ToLower(parsed.Path), "/my-account/")
	return under && rest != "" && !truGreenPublic.MatchString(rest)
}

type TruGreen struct {
	Draft
}

func NewTruGreen() *TruGreen {
	return &TruGreen{Draft{
		BillerID:    domain.BillerTruGreen,
		Home:        truGreenHome,
		SignIn:      truGreenSignIn,
		Landing:     truGreenSignIn,
		AccountArea: truGreenInside,
		AccountPage: truGreenSummary,
	}}
}

// TruGreenDetail is the part of the customer detail a bill is read from. The
// figures stay as the JSON wrote them, for Amount.
type TruGreenDetail struct {
	CustomerNumber  any                 `json:"CustomerNumber"`
	SalesAgreements []TruGreenAgreement `json:"SalesAgreements"`
}

// TruGreenAgreement is one plan the customer is signed up to.
type TruGreenAgreement struct {
	CustomerNumber any               `json:"customerNumber"`
	Plan           string            `json:"saTemplateDescription"`
	Invoices       []TruGreenInvoice `json:"Invoices"`
}

// TruGreenInvoice is one service visit's invoice.
type TruGreenInvoice struct {
	Number  any `json:"invoiceNum"`
	Amount  any `json:"invoiceAmount"`
	Balance any `json:"invoiceBalance"`
	Date    any `json:"invoiceDate"`
	// Open is "N" once the invoice is settled.
	Open         any `json:"invoiceOpen"`
	WorkOrderRef any `json:"workOrderRef"`
}

type truGreenRaw struct {
	// Customer is the customer number as the portal writes it, for the
	// statement's address.
	Customer  string `json:"customer"`
	WorkOrder string `json:"work_order"`
}

// truGreenRead is what one visit to the services page found.
type truGreenRead struct {
	detail    TruGreenDetail
	customers []string
}

// read answers false for a profile that is not signed in; a signed-in page
// that answered nothing readable is a note and an empty read.
func (t *TruGreen) read(call Call) (truGreenRead, bool) {
	page := call.Page
	recorded := browser.RecordResponses(page, truGreenDetailCall)
	// The navigation's own error is not fatal: where the browser stands
	// afterwards is the answer.
	_ = page.Goto(truGreenServices)
	page.Settle()
	if !truGreenInside(page.URL()) {
		call.Notes.Tracef("trugreen.com sent the services page on to %s; the profile is not signed in",
			browser.WithoutQuery(page.URL()))
		return truGreenRead{}, false
	}
	response, ok := recorded.Await(page, truGreenDetailWait)
	if !ok {
		call.Notes.Addf("TruGreen's services page did not load the account's invoices within %s; nothing was read",
			truGreenDetailWait)
		return truGreenRead{}, true
	}
	if status := response.Status(); status == 401 || status == 403 {
		call.Notes.Tracef("the app's customer-detail call answered HTTP %d", status)
		return truGreenRead{}, false
	}
	body, err := response.Body()
	var detail TruGreenDetail
	if err == nil {
		err = httpx.DecodeJSON(body, &detail)
	}
	if response.Status() != 200 || err != nil {
		call.Notes.Addf("TruGreen's account detail answered HTTP %d with something unreadable; nothing was read",
			response.Status())
		return truGreenRead{}, true
	}
	return truGreenRead{detail: detail, customers: t.customers(call)}, true
}

// customers is every customer number the app keeps in `tg_cust`. Nothing else
// in the entry is read.
func (t *TruGreen) customers(call Call) []string {
	entries, err := browser.ReadStorage(call.Page, []string{"localStorage"}, `^tg_cust$`)
	if err != nil || len(entries) == 0 {
		call.Notes.Tracef("the page keeps no tg_cust entry")
		return nil
	}
	var kept struct {
		Customers []struct {
			CustomerNumber any `json:"customerNumber"`
		} `json:"customers"`
	}
	if httpx.DecodeJSON([]byte(entries[0].Value), &kept) != nil {
		call.Notes.Tracef("the page's tg_cust entry is not JSON")
		return nil
	}
	numbers := make([]string, 0, len(kept.Customers))
	for _, one := range kept.Customers {
		numbers = append(numbers, Text(one.CustomerNumber))
	}
	return numbers
}

func (t *TruGreen) Subaccounts(call Call) ([]Subaccount, error) {
	if call.Page == nil {
		return nil, nil
	}
	read, signedIn := t.read(call)
	if !signedIn {
		return nil, ErrNeedsSignIn
	}
	found := TruGreenSubaccounts(read.customers, read.detail)
	call.Notes.Addf("TruGreen lists %d customer account%s", len(found), plural(len(found)))
	return found, nil
}

func (t *TruGreen) FetchBills(call Call) (Pull, error) {
	if call.Page == nil {
		return t.NoPage(), nil
	}
	read, signedIn := t.read(call)
	if !signedIn {
		return t.SignInAgain(nil), nil
	}
	answered := map[string]bool{}
	for _, agreement := range read.detail.SalesAgreements {
		answered[truGreenCustomerKey(truGreenCustomerOf(agreement, read.detail))] = true
	}
	for _, wanted := range call.Subaccounts {
		if !answered[wanted] && len(read.detail.SalesAgreements) > 0 {
			call.Notes.Addf("TruGreen's services page showed another customer account, so %s was not read",
				domain.MaskAccount(wanted))
		}
	}

	var bills []Bill
	for _, bill := range TruGreenBillsFromDetail(read.detail, call.Notes) {
		if len(call.Subaccounts) > 0 && !call.Wanted(bill.Subaccount) {
			continue
		}
		bills = append(bills, bill)
	}
	call.Notes.Addf("TruGreen answered %d paid bill%s", len(bills), plural(len(bills)))
	return Pull{Bills: bills}, nil
}

// FetchDocument is the bill's own work order's invoice, a same-origin GET that
// carries the page's cookies.
func (t *TruGreen) FetchDocument(call Call, bill Bill) (*Document, error) {
	raw := rawOf[truGreenRaw](bill)
	if raw.Customer == "" || raw.WorkOrder == "" || call.Page == nil {
		return nil, nil
	}
	body, ok := fetchedPDF(call, "the TruGreen invoice "+bill.Invoice+" of "+bill.IssuedOn,
		truGreenStatementURL(raw.Customer, raw.WorkOrder))
	if !ok {
		return nil, nil
	}
	return pdfDocument(body, statementFilename("trugreen", bill, bill.IssuedOn+"-"+bill.Invoice)), nil
}

func truGreenStatementURL(customer, workOrder string) string {
	return truGreenHome + "/document/" + url.PathEscape(customer) + "/workorder/" + url.PathEscape(workOrder)
}

// truGreenCustomerKey is a customer number as a billed account's key: the
// storage entry writes it as a string and the detail may write it as a number,
// so the leading zeros one keeps and the other drops are not part of it.
func truGreenCustomerKey(number string) string {
	trimmed := strings.TrimLeft(strings.TrimSpace(number), "0")
	if trimmed == "" {
		return strings.TrimSpace(number)
	}
	return trimmed
}

func truGreenCustomerOf(agreement TruGreenAgreement, detail TruGreenDetail) string {
	if number := strings.TrimSpace(Text(agreement.CustomerNumber)); number != "" {
		return number
	}
	return strings.TrimSpace(Text(detail.CustomerNumber))
}

// TruGreenSubaccounts is one billed account per customer number the app keeps,
// or the detail's own when it keeps none, named by the plans the detail lists
// for it. A customer the services page did not show has no plan to name it by.
func TruGreenSubaccounts(customers []string, detail TruGreenDetail) []Subaccount {
	plans := map[string][]string{}
	var shown []string
	for _, agreement := range detail.SalesAgreements {
		key := truGreenCustomerKey(truGreenCustomerOf(agreement, detail))
		if key == "" {
			continue
		}
		if !slices.Contains(shown, key) {
			shown = append(shown, key)
		}
		if plan := strings.TrimSpace(agreement.Plan); plan != "" && !slices.Contains(plans[key], plan) {
			plans[key] = append(plans[key], plan)
		}
	}
	order := shown
	if len(customers) > 0 {
		order = nil
		for _, number := range customers {
			if key := truGreenCustomerKey(number); key != "" && !slices.Contains(order, key) {
				order = append(order, key)
			}
		}
	}
	found := make([]Subaccount, 0, len(order))
	for _, key := range order {
		label := strings.Join(plans[key], ", ")
		if label == "" {
			label = "TruGreen lawn service"
		}
		found = append(found, Subaccount{ExternalID: key, Label: label, MaskedNumber: domain.MaskAccount(key)})
	}
	return found
}

// TruGreenBillsFromDetail is one bill per settled invoice. Visits invoiced on
// one day are charged to the card separately, so each is its own bill, told
// apart by its invoice number, or its work order when it carries no number.
// An invoice with neither is left out with a note: a later pull would have
// nothing to find it again by.
//
// The detail states no due date anywhere, and an account on EasyPay has the
// card charged on invoicing. A settled invoice is therefore dated by its
// invoice date, the one day the portal states, and filed Paid. An invoice
// still open is left out with a note: its due date would be a guess, and an
// open bill's due date moves a reminder. An invoice is open when the portal
// says so, owes a balance, or says neither readably, because calling an
// invoice settled on a figure nobody could read hides one that may be owed.
// A credit or a zero is not a bill.
func TruGreenBillsFromDetail(detail TruGreenDetail, notes *Notes) []Bill {
	var bills []Bill
	filed := map[string]bool{}
	for _, agreement := range detail.SalesAgreements {
		customer := truGreenCustomerOf(agreement, detail)
		if truGreenCustomerKey(customer) == "" {
			notes.Addf("a TruGreen plan carried no customer number; its invoices are left out")
			continue
		}
		for _, invoice := range agreement.Invoices {
			date := ISODate(invoice.Date)
			amount, priced := Amount(invoice.Amount)
			if date == "" || !priced {
				notes.Addf("a TruGreen invoice carried no readable date or amount (%s, %s); it is left out",
					jsonish(invoice.Date), jsonish(invoice.Amount))
				continue
			}
			if !Owes(amount) {
				continue
			}
			if truGreenInvoiceOpen(invoice) {
				notes.Addf("TruGreen shows an open invoice of %s dated %s and states no due date for it, "+
					"so it is not filed as a bill", amount, date)
				continue
			}
			workOrder := strings.TrimSpace(Text(invoice.WorkOrderRef))
			number := cmp.Or(strings.TrimSpace(Text(invoice.Number)), workOrder)
			if number == "" {
				notes.Addf("a settled TruGreen invoice of %s dated %s carried no invoice number or work order; "+
					"it is left out", amount, date)
				continue
			}
			key := truGreenCustomerKey(customer) + ":" + number
			if filed[key] {
				notes.Addf("TruGreen listed the invoice %s twice; the first is kept", number)
				continue
			}
			filed[key] = true
			raw, _ := json.Marshal(truGreenRaw{Customer: customer, WorkOrder: workOrder})
			bills = append(bills, Bill{
				Subaccount: truGreenCustomerKey(customer),
				ExternalID: key,
				Invoice:    number,
				IssuedOn:   date,
				DueOn:      date,
				AmountDue:  amount,
				Currency:   "USD",
				Status:     "Paid",
				Raw:        raw,
			})
		}
	}
	slices.SortStableFunc(bills, func(a, b Bill) int {
		return cmp.Or(cmp.Compare(a.DueOn, b.DueOn), cmp.Compare(a.Invoice, b.Invoice))
	})
	return bills
}

func truGreenInvoiceOpen(invoice TruGreenInvoice) bool {
	balance, readable := Amount(invoice.Balance)
	if readable && Owes(balance) {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(Text(invoice.Open))) {
	case "Y":
		return true
	case "N":
		return false
	}
	return !readable
}
