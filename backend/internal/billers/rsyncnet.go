package billers

import (
	"cmp"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// rsync.net's Account Manager is server-rendered HTML with no API, so the
// reader walks the DOM. See docs/connectors/providers.md.
//
// Signed out, `am/billing_info.html` serves the sign-in form in place, with no
// redirect, so the billing page and the sign-in form are one URL. The signed-out
// page also carries a nav "Login" control the page reading counts as a
// sign-out, so the classifier's fallback would call it signed in. The
// account-area pattern and RsyncNetPage.SignedIn are what tell them apart.
//
// Each transaction's receipt at `am/rsync_receipt.pdf?sd=YYYY-MM-DD` is a real
// PDF served to the cookie alone.

const (
	rsyncNetHome    = "https://www.rsync.net"
	rsyncNetManager = rsyncNetHome + "/am"
	rsyncNetBilling = rsyncNetManager + "/billing_info.html"
	rsyncNetReceipt = rsyncNetManager + "/rsync_receipt.pdf"
	// Read only for the account's id, which the billing page never prints.
	rsyncNetDashboard = rsyncNetManager + "/dashboard.html"

	rsyncNetBillsToRead = 3

	// rsyncNetOneAccount keys the billed account when the dashboard names
	// none: a login bills a single filesystem, so a stable placeholder is
	// honest where an invented number would not be.
	rsyncNetOneAccount = "rsync-net"
)

// rsyncNetAnyDay is a day as the portal may write one: "June 1, 2027",
// "6/1/2027" or "2027-06-01".
const rsyncNetAnyDay = `([A-Za-z]{3,9}\.?\s+\d{1,2},?\s+\d{4}|\d{1,2}/\d{1,2}/\d{4}|\d{4}-\d{2}-\d{2})`

var (
	rsyncNetAccountArea = regexp.MustCompile(`(?i)rsync\.net/am(?:/|$)`)

	// "Next billing" is the only due date this portal states for a charge.
	rsyncNetRecurring = regexp.MustCompile(
		`(?i)\$\s*([\d,]+(?:\.\d{1,2})?)\s*(?:per|/|a)\s*(year|month|quarter|week|day)\b`)
	// "Next billing: June 1, 2027" and "Next Billing Date: 6/1/2027" both;
	// without the optional "Date" the word itself would be read as the month.
	rsyncNetNextBilling = regexp.MustCompile(
		`(?i)next\s+billing(?:\s+date)?\s*:?\s*` + rsyncNetAnyDay)
	// The figure may be parenthesised, which `Amount` reads as negative.
	rsyncNetAmountDue = regexp.MustCompile(
		`(?i)amount\s+due:?\s*\$?\s*(\(?-?[\d,]+(?:\.\d{1,2})?\)?)`)
	// A day the page states for the balance itself. "Amount Due" alone is not
	// one: its colon is followed by the figure, never by a day.
	rsyncNetBalanceDueOn = regexp.MustCompile(
		`(?i)\b(?:due\s+(?:date|by|on)|payable\s+by)\s*:?\s*` + rsyncNetAnyDay)
	// The quota is dropped from the service description: it changes with the
	// plan, which would rename the billed account under it.
	rsyncNetQuota = regexp.MustCompile(`(?i)^[\d.,]+\s*[KMGTP]B\s+`)

	// Money going the other way. "Credit card" is not one of them, which is
	// why the card is named out of the description before the word is read.
	rsyncNetMoneyBack  = regexp.MustCompile(`(?i)\b(refund|reversal|chargeback|credit)\b`)
	rsyncNetCreditCard = regexp.MustCompile(`(?i)\bcredit\s+cards?\b`)
)

// rsyncNetBillingScript answers the whole page's text rather than a selector
// for the Services block, which is a run of plain text in a layout div with
// nothing naming it.
const rsyncNetBillingScript = `() => {` + agent.CollapseSpacesJS + `
  // The transactions table by its own headings, and never simply the first
  // table on the page. Signed out, the portal serves its sign-in form at this
  // address as a one-row table, and a reader that took the first table would
  // hand back a row from the login form — which is how a lapsed session gets
  // reported as a pull that found nothing instead of a sign-in owed.
  const wanted = (table) => {
    const head = [...table.querySelectorAll('tr')][0];
    if (!head) return false;
    const cells = [...head.querySelectorAll('td, th')].map((c) => collapseSpaces(c).toLowerCase());
    return cells.length >= 3 && cells[0] === 'date' && /amount/.test(cells[2] || '');
  };
  const table = [...document.querySelectorAll('table')].find(wanted) || null;
  const rows = table ? [...table.querySelectorAll('tr')].map((row) => {
    const cells = [...row.querySelectorAll('td, th')].map(collapseSpaces);
    return { date: cells[0] || '', description: cells[1] || '', amount: cells[2] || '' };
  }) : [];
  return {
    text: collapseSpaces(document.body),
    balance: [...document.querySelectorAll('div.table_cell.big')].map(collapseSpaces).join('\n'),
    rows,
  };
}`

// The dashboard's usage grid is `div`s, not a table. `div.col.uid` is both the
// heading and the value; the heading also carries `hdr` and is dropped, or the
// account would be called "Account".
const rsyncNetDashboardScript = `() => {` + agent.CleanJS + `
  return [...document.querySelectorAll('div.col.uid')]
    .filter((el) => !el.classList.contains('hdr'))
    .map((el) => clean(el.innerText))
    .filter((said) => said !== '');
}`

type RsyncNet struct {
	Draft
}

func NewRsyncNet() *RsyncNet {
	return &RsyncNet{Draft{
		BillerID:    domain.BillerRsyncNet,
		Home:        rsyncNetHome,
		SignIn:      rsyncNetBilling,
		Landing:     rsyncNetBilling,
		AccountArea: URLMatches(rsyncNetAccountArea),
	}}
}

type RsyncNetRow struct {
	Date        string `json:"date"`
	Description string `json:"description"`
	Amount      string `json:"amount"`
}

type RsyncNetPage struct {
	Text    string        `json:"text"`
	Balance string        `json:"balance"`
	Rows    []RsyncNetRow `json:"rows"`
}

// SignedIn is decided by the reading because the address cannot say. A billing
// page always carries a transactions table header, a balance or a services
// block; the form carries none of the three.
func (p RsyncNetPage) SignedIn() bool {
	return p.Dated() > 0 || p.Balance != "" ||
		rsyncNetRecurring.MatchString(p.Text) || rsyncNetNextBilling.MatchString(p.Text)
}

// Dated counts rows carrying a day this portal writes; a bare row count is no
// evidence, since the table always has its header.
func (p RsyncNetPage) Dated() int {
	found := 0
	for _, row := range p.Rows {
		if DayIn(row.Date) != "" {
			found++
		}
	}
	return found
}

type RsyncNetService struct {
	Description string
	Amount      domain.Money
	Priced      bool
	// Every is only reported in a note; nothing branches on it, because the
	// portal states the next date itself.
	Every string
	DueOn string
}

type rsyncNetRaw struct {
	// ReceiptOn is the whole of the receipt's address.
	ReceiptOn string `json:"receipt_on"`
}

// billing answers false for a portal that served its sign-in form.
func (r *RsyncNet) billing(call Call) (RsyncNetPage, bool) {
	page := call.Page
	_ = page.Goto(rsyncNetBilling)
	page.Settle()
	if !rsyncNetAccountArea.MatchString(page.URL()) {
		call.Notes.Addf(
			"rsync.net answered %s instead of its billing page; the profile is not signed in",
			browser.WithoutQuery(page.URL()))
		return RsyncNetPage{}, false
	}
	var read RsyncNetPage
	if err := browser.EvaluateInto(page, rsyncNetBillingScript, nil, &read); err != nil {
		call.Notes.Addf("the rsync.net billing page could not be read: %v", err)
		return RsyncNetPage{}, false
	}
	if !read.SignedIn() {
		call.Notes.Addf(
			"rsync.net served its sign-in form at the billing page's own address; " +
				"the profile is not signed in")
		return RsyncNetPage{}, false
	}
	return read, true
}

func (r *RsyncNet) dashboardIDs(call Call) []string {
	page := call.Page
	_ = page.Goto(rsyncNetDashboard)
	page.Settle()
	var ids []string
	if err := browser.EvaluateInto(page, rsyncNetDashboardScript, nil, &ids); err != nil {
		call.Notes.Addf("the rsync.net dashboard could not be read: %v", err)
		return nil
	}
	return ids
}

// Subaccounts reads the billing page first because it tells a kept session
// from a lapsed one; the dashboard is read after it for the id.
func (r *RsyncNet) Subaccounts(call Call) ([]Subaccount, error) {
	if call.Page == nil {
		return nil, nil
	}
	read, ok := r.billing(call)
	if !ok {
		return nil, nil
	}
	found := RsyncNetSubaccountsFrom(r.dashboardIDs(call), read, call.Notes)
	call.Notes.Addf("rsync.net bills %d account%s", len(found), plural(len(found)))
	return found, nil
}

// account reads the dashboard again for a pull that named none rather than
// inventing a key: a second spelling would file the same bill twice.
func (r *RsyncNet) account(call Call) string {
	if len(call.Subaccounts) > 0 {
		if len(call.Subaccounts) > 1 {
			call.Notes.Addf(
				"rsync.net bills one filesystem per login; this pull asked for %d and the first is read",
				len(call.Subaccounts))
		}
		return call.Subaccounts[0]
	}
	if ids := r.dashboardIDs(call); len(ids) > 0 {
		return strings.TrimSpace(ids[0])
	}
	return rsyncNetOneAccount
}

// The account is resolved before the billing page is opened, so the pull ends
// standing on the page the receipts are fetched from.
func (r *RsyncNet) FetchBills(call Call) (Pull, error) {
	if call.Page == nil {
		return r.NoPage(), nil
	}
	account := r.account(call)
	read, ok := r.billing(call)
	if !ok {
		return r.SignInAgain(nil), nil
	}
	bills := RsyncNetBillsFromPage(read, account, call.Notes)
	call.Notes.Addf("rsync.net answered %d bill%s of the %d row%s its transactions table shows",
		len(bills), plural(len(bills)), len(read.Rows), plural(len(read.Rows)))
	return Pull{Bills: bills}, nil
}

// FetchDocument answers no document for the open bill, which has no receipt:
// the portal issues one per transaction.
func (r *RsyncNet) FetchDocument(call Call, bill Bill) (*Document, error) {
	raw := rawOf[rsyncNetRaw](bill)
	if raw.ReceiptOn == "" || call.Page == nil {
		return nil, nil
	}
	address := rsyncNetReceipt + "?" + url.Values{"sd": {raw.ReceiptOn}}.Encode()
	body, ok := fetchedPDF(call, "the receipt for "+bill.ExternalID, address)
	if !ok {
		return nil, nil
	}
	return pdfDocument(body, statementFilename("rsync-net", bill, raw.ReceiptOn)), nil
}

// RsyncNetSubaccountsFrom labels the account by the service rather than the
// id. The mask is empty for an id with fewer than four digits.
func RsyncNetSubaccountsFrom(ids []string, page RsyncNetPage, notes *Notes) []Subaccount {
	label := RsyncNetServiceFrom(page).Description
	if label == "" {
		label = "rsync.net"
	}
	found := make([]Subaccount, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		found = append(found, Subaccount{
			ExternalID: id, Label: label, MaskedNumber: domain.MaskAccount(id),
		})
	}
	if len(found) == 0 {
		notes.Addf(
			"rsync.net's dashboard named no account id; the filesystem this login bills is kept as %q",
			rsyncNetOneAccount)
		return []Subaccount{{ExternalID: rsyncNetOneAccount, Label: label}}
	}
	notes.Addf("rsync.net's dashboard names the account %s",
		cmp.Or(found[0].MaskedNumber, "by an id too short to mask"))
	return found
}

// RsyncNetServiceFrom reads the charge ("$N per year"), the description before
// it, and "Next billing", the one due date this portal states.
func RsyncNetServiceFrom(page RsyncNetPage) RsyncNetService {
	out := RsyncNetService{}
	if at := rsyncNetRecurring.FindStringSubmatchIndex(page.Text); at != nil {
		out.Amount, out.Priced = Amount(page.Text[at[2]:at[3]])
		out.Every = strings.ToLower(page.Text[at[4]:at[5]])
		out.Description = rsyncNetDescription(page.Text[:at[0]])
	}
	if named := rsyncNetNextBilling.FindStringSubmatch(page.Text); named != nil {
		out.DueOn = DayIn(named[1])
	}
	return out
}

// RsyncNetBalanceFrom reads the block's cells first and the whole page after
// them: the cell class is layout rather than a label.
func RsyncNetBalanceFrom(page RsyncNetPage) (domain.Money, bool) {
	for _, where := range []string{page.Balance, page.Text} {
		if named := rsyncNetAmountDue.FindStringSubmatch(where); named != nil {
			return Amount(named[1])
		}
	}
	return domain.Zero, false
}

func RsyncNetBalanceDueFrom(page RsyncNetPage) string {
	for _, where := range []string{page.Balance, page.Text} {
		if named := rsyncNetBalanceDueOn.FindStringSubmatch(where); named != nil {
			if day := DayIn(named[1]); day != "" {
				return day
			}
		}
	}
	return ""
}

func RsyncNetBillsFromPage(page RsyncNetPage, account string, notes *Notes) []Bill {
	bills := make([]Bill, 0, rsyncNetBillsToRead+2)
	bills = append(bills, rsyncNetOpenBills(page, account, notes)...)
	return append(bills, rsyncNetPaidBills(page, account, notes)...)
}

// rsyncNetOpenBills is the next charge and any balance owed now.
//
// A zero balance is not a zero bill: "Amount Due: $0.00" beside a future charge
// means nothing is overdue, so the charge's amount is the recurring charge.
//
// An owed balance is its own bill on the day the page states for it, not on
// the next billing date, except that it stands in for a recurring charge that
// cannot be read. A balance with no stated day is a note. Two bills on one day
// are one bill.
func rsyncNetOpenBills(page RsyncNetPage, account string, notes *Notes) []Bill {
	open := func(day string, amount domain.Money) Bill {
		return Bill{
			Subaccount: account,
			ExternalID: account + ":" + day,
			DueOn:      day,
			AmountDue:  amount,
			Currency:   "USD",
			Status:     "Open",
		}
	}
	service := RsyncNetServiceFrom(page)
	balance, _ := RsyncNetBalanceFrom(page)
	owed := Owes(balance)
	balanceOn := RsyncNetBalanceDueFrom(page)

	var bills []Bill
	switch {
	case service.DueOn == "":
		notes.Addf("rsync.net's services block states no next billing date " +
			"(neither \"Next billing:\" nor \"Next Billing Date:\" followed by a day); " +
			"no upcoming charge is reported")
	case service.Priced:
		bills = append(bills, open(service.DueOn, service.Amount))
	case !owed:
		notes.Addf(
			"rsync.net names a charge due %s that could not be read, and owes nothing today; "+
				"no open bill is reported", service.DueOn)
	case balanceOn == "" || balanceOn == service.DueOn:
		notes.Addf(
			"rsync.net's recurring charge could not be read; the balance due is reported for %s instead",
			service.DueOn)
		return []Bill{open(service.DueOn, balance)}
	default:
		notes.Addf("rsync.net names a charge due %s that could not be read; no bill is reported for it",
			service.DueOn)
	}
	if !owed {
		return bills
	}
	if balanceOn == "" {
		notes.Addf(
			"rsync.net shows $%s owed today and states no day it is due; it is not reported as a bill",
			balance)
		return bills
	}
	for index, bill := range bills {
		if bill.DueOn != balanceOn {
			continue
		}
		if bill.AmountDue.Equal(balance) {
			notes.Addf("rsync.net's balance of $%s due %s is the charge due that day; it is reported once",
				balance, balanceOn)
		} else {
			notes.Addf("rsync.net's balance of $%s and its charge of $%s are both due %s; "+
				"they are reported as one bill", balance, bill.AmountDue, balanceOn)
			bills[index].AmountDue = bill.AmountDue.Add(balance)
		}
		return bills
	}
	notes.Addf("rsync.net shows $%s owed, due %s", balance, balanceOn)
	return append([]Bill{open(balanceOn, balance)}, bills...)
}

// rsyncNetPaidBills files payment rows as paid bills: the table is a payment
// ledger with no invoice row, so a payment row is the record of that charge
// (Community Connect is the opposite). Money coming back is left out, and so is
// a row whose amount will not parse.
//
// Each row's own day is both IssuedOn and DueOn: the portal states no due date
// for a past charge.
func rsyncNetPaidBills(page RsyncNetPage, account string, notes *Notes) []Bill {
	var bills []Bill
	for _, row := range page.Rows {
		day := DayIn(row.Date)
		if day == "" {
			// The table's own header row, and any layout row beside it: a cell
			// that does not read as a day is not a transaction.
			continue
		}
		if rsyncNetIsMoneyBack(row.Description) {
			continue
		}
		amount, _ := Amount(row.Amount)
		if !Owes(amount) {
			notes.Addf("a rsync.net transaction dated %s carried no readable amount; it is left out", day)
			continue
		}
		raw, _ := json.Marshal(rsyncNetRaw{ReceiptOn: day})
		bills = append(bills, Bill{
			Subaccount: account,
			ExternalID: account + ":" + day,
			IssuedOn:   day,
			DueOn:      day,
			AmountDue:  amount,
			Currency:   "USD",
			Status:     "Paid",
			Raw:        raw,
		})
	}
	// The table is newest first, but that is the portal's sort order and not a
	// promise; the newest are taken by their own dates.
	sort.SliceStable(bills, func(a, b int) bool { return bills[a].DueOn > bills[b].DueOn })
	if len(bills) > rsyncNetBillsToRead {
		bills = bills[:rsyncNetBillsToRead]
	}
	return bills
}

// The amounts in this table are bare decimals with no sign on them, so the
// description is the only signal there is.
func rsyncNetIsMoneyBack(description string) bool {
	return rsyncNetMoneyBack.MatchString(rsyncNetCreditCard.ReplaceAllString(description, "card"))
}

func rsyncNetDescription(before string) string {
	lines := strings.Split(before, "\n")
	for at := len(lines) - 1; at >= 0; at-- {
		line := strings.TrimSpace(lines[at])
		if line == "" {
			continue
		}
		return strings.TrimSpace(rsyncNetQuota.ReplaceAllString(line, ""))
	}
	return ""
}
