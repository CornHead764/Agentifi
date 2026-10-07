package billers

import (
	"cmp"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// We Energies (WEC Energy Group) is server-rendered ASP.NET WebForms with no
// JSON, read from the kept browser profile. See docs/connectors/providers.md.
//
// The payment-history ledger is the only place the portal writes a due date
// for anything but the open bill, and it shows bill rows only once `chkBills`
// is ticked and `btnRefresh` posts back. The bill history page carries the
// same statement links but no due dates, so it is not visited. The statement
// link's parameters are the portal's ciphertext and are used exactly as the
// ledger row writes them.
//
// The signed-in pages live under /AccountSummary/ and /UpdateAccount/ as well
// as the /secure/ addresses that reach them, so the account area covers all
// three.

const (
	weEnergiesHome    = "https://www.we-energies.com"
	weEnergiesSummary = weEnergiesHome + "/secure/auth/l/acct/summary_accounts"
	// Without `show_list=true` a single-account login is redirected past the
	// list.
	weEnergiesAccountList = weEnergiesHome +
		"/secure/auth/l/acct/paymenthistory_accounts.aspx?show_list=true"
	// The ledger of whichever account the portal holds as its current one.
	weEnergiesPaymentHistory = weEnergiesHome + "/AccountSummary/View/PaymentHistory"

	weEnergiesBillsToRead = 3

	// The in-page click that starts a post-back returns before the navigation
	// does, and the old document has already reached every load state, so
	// Settle alone returns at once.
	weEnergiesPostBackWait = 20 * time.Second

	weEnergiesLedger    = "#ctl00_ctl00_TemplateBody_BodyContent_dgvPaymentHistory"
	weEnergiesScheduled = "#ctl00_ctl00_TemplateBody_BodyContent_dgvScheduledPayments"
	weEnergiesShowBills = "#ctl00_ctl00_TemplateBody_BodyContent_chkBills"
	weEnergiesRefresh   = "#ctl00_ctl00_TemplateBody_BodyContent_btnRefresh"
)

var (
	weEnergiesAccountArea = regexp.MustCompile(
		`(?i)we-energies\.com/(?:secure|accountsummary|updateaccount)/`)
	weEnergiesAccountLine = regexp.MustCompile(`(?i)account\s*#?:?\s*([0-9][0-9-]*)`)
	weEnergiesBillDue     = regexp.MustCompile(
		`(?i)bill\s+due\s+(\d{1,2}/\d{1,2}/\d{4}|\d{4}-\d{2}-\d{2})`)
	weEnergiesFigure = regexp.MustCompile(`\(?-?\$?\s*-?[0-9][0-9,]*\.[0-9]{2}\)?`)
	weEnergiesCredit = regexp.MustCompile(`(?i)\bCR\b|credit`)
)

const weEnergiesListScript = `() => {` + agent.CollapseSpacesJS + `
  return [...document.querySelectorAll('tr.selectableRow')].map((row) => {
    const submit = row.querySelector('input[type="submit"][onclick]');
    const chose = /SelectAcct\(\s*['"]([^'"]+)['"]/.exec((submit && submit.getAttribute('onclick')) || '');
    return {
      selectAcct: chose ? chose[1] : '',
      customerName: collapseSpaces(row.querySelector('.customerName')),
      address: collapseSpaces(row.querySelector('td.accountSelectionAddress')),
      dueBy: collapseSpaces(row.querySelector('span.dueBy')),
      dueAmount: collapseSpaces(row.querySelector('span.dueAmt')),
      text: collapseSpaces(row),
    };
  });
}`

var weEnergiesHasBillsBox = `() => !!document.querySelector(` + strconv.Quote(weEnergiesShowBills) + `)`

var weEnergiesShowsBills = `() => [...document.querySelectorAll(` +
	strconv.Quote(weEnergiesLedger+` span[id$="lblTransactionDescription"]`) +
	`)].some((span) => /bill\s+due/i.test(span.innerText || span.textContent || ''))`

// weEnergiesChooseScript presses the row's label rather than its submit: the
// submit is the invisible map pin beside the address, and the label is what
// the WebForms post-back is wired to. It runs in the page because the Page
// façade has no two-step locator.
const weEnergiesChooseScript = `(account) => {
  const rows = [...document.querySelectorAll('tr.selectableRow')];
  const row = rows.find((one) => String(one.innerText || '').includes(account));
  if (!row) return false;
  const control = row.querySelector('td.accountSelectionAddress label')
    || row.querySelector('input[type="submit"][onclick]');
  if (!control) return false;
  control.click();
  return true;
}`

const weEnergiesLedgerScript = `({ ledger, scheduled }) => {` + agent.CleanJS + `
  const cell = (row, suffix) => {
    const found = [...row.querySelectorAll('span[id]')].find((span) => span.id.endsWith(suffix));
    return found ? clean(found.innerText || found.textContent) : '';
  };
  const rows = [...document.querySelectorAll(ledger + ' tr')].map((row) => {
    const link = row.querySelector('a[href*="BillPdf" i]');
    return {
      date: cell(row, 'lblTransactionDate'),
      description: cell(row, 'lblTransactionDescription'),
      paymentAmount: cell(row, 'lblPaymentCreditAmount'),
      billAmount: cell(row, 'lblBillChargeAmount'),
      statementHref: link ? link.href : '',
    };
  });
  const booked = [...document.querySelectorAll(scheduled + ' tr')].map((row) => ({
    date: cell(row, 'lblScheduledPmtDate'),
    amount: cell(row, 'lblScheduledPmtAmount'),
  }));
  return { rows, booked };
}`

type WeEnergies struct {
	Draft
}

// NewWeEnergies signs in at the summary page, not the public `/myaccount`,
// which carries no form. Signed out, the summary bounces through the F5
// gateway to the B2C form.
func NewWeEnergies() *WeEnergies {
	return &WeEnergies{Draft{
		BillerID:    domain.BillerWeEnergies,
		Home:        weEnergiesHome,
		SignIn:      weEnergiesSummary,
		Landing:     weEnergiesSummary,
		AccountArea: URLMatches(weEnergiesAccountArea),
	}}
}

type WeEnergiesListRow struct {
	SelectAcct   string `json:"selectAcct"`
	CustomerName string `json:"customerName"`
	Address      string `json:"address"`
	// DueBy and DueAmount are the one place the portal states outright what is
	// owed and by when.
	DueBy     string `json:"dueBy"`
	DueAmount string `json:"dueAmount"`
	Text      string `json:"text"`
}

type WeEnergiesLedgerRow struct {
	Date          string `json:"date"`
	Description   string `json:"description"`
	PaymentAmount string `json:"paymentAmount"`
	BillAmount    string `json:"billAmount"`
	StatementHref string `json:"statementHref"`
}

type WeEnergiesBooked struct {
	Date   string `json:"date"`
	Amount string `json:"amount"`
}

type weEnergiesRaw struct {
	StatementPath string `json:"statement_path"`
}

// accountRows answers nil for a portal that served a sign-in page.
func (w *WeEnergies) accountRows(call Call) ([]WeEnergiesListRow, bool) {
	page := call.Page
	_ = page.Goto(weEnergiesAccountList)
	page.Settle()
	if !weEnergiesAccountArea.MatchString(page.URL()) {
		call.Notes.Addf(
			"we-energies.com answered %s instead of the account list; the profile is not signed in",
			browser.WithoutQuery(page.URL()))
		return nil, false
	}
	var rows []WeEnergiesListRow
	if err := browser.EvaluateInto(page, weEnergiesListScript, nil, &rows); err != nil {
		call.Notes.Addf("the account list could not be read: %v", err)
		return nil, false
	}
	return rows, true
}

func (w *WeEnergies) Subaccounts(call Call) ([]Subaccount, error) {
	if call.Page == nil {
		return nil, nil
	}
	rows, ok := w.accountRows(call)
	if !ok {
		return nil, nil
	}
	found := WeEnergiesSubaccountsFromRows(rows)
	call.Notes.Addf("We Energies lists %d billed account%s", len(found), plural(len(found)))
	return found, nil
}

// ledgerFor answers "" or what went wrong. Both post-backs are waited for by
// what they put on the page rather than by Settle, which would read the page
// being left. A choice that lands elsewhere is followed by the payment history
// itself, which answers for the portal's current account.
func (w *WeEnergies) ledgerFor(call Call, account string) string {
	page := call.Page
	var chosen bool
	if err := browser.EvaluateInto(page, weEnergiesChooseScript, account, &chosen); err != nil || !chosen {
		return "its row in the account list could not be chosen"
	}
	if page.WaitForFunction(weEnergiesHasBillsBox, weEnergiesPostBackWait) != nil {
		_ = page.Goto(weEnergiesPaymentHistory)
		page.Settle()
	}
	if !weEnergiesAccountArea.MatchString(page.URL()) {
		return "the portal answered " + browser.WithoutQuery(page.URL()) + " instead of the payment history"
	}
	if count, err := page.Count(weEnergiesShowBills); err != nil || count == 0 {
		return "the payment history at " + browser.WithoutQuery(page.URL()) + " offered no Bills box"
	}

	// Ticked rather than toggled: a box that is already ticked would be cleared
	// by a second press, and the ledger would come back with payments only.
	_, _ = page.CheckIfUnchecked(weEnergiesShowBills)
	var shown bool
	if err := browser.EvaluateInto(page, weEnergiesShowsBills, nil, &shown); err != nil || !shown {
		if count, err := page.Count(weEnergiesRefresh); err == nil && count > 0 {
			_ = page.Click(weEnergiesRefresh)
		}
		if page.WaitForFunction(weEnergiesShowsBills, weEnergiesPostBackWait) != nil {
			return "the payment history showed no bill rows after its refresh"
		}
	}
	page.Settle()
	return ""
}

func (w *WeEnergies) FetchBills(call Call) (Pull, error) {
	if call.Page == nil {
		return w.NoPage(), nil
	}
	rows, ok := w.accountRows(call)
	if !ok {
		return w.SignInAgain(nil), nil
	}

	accounts := WeEnergiesSubaccountsFromRows(rows)
	asked := accounts
	if len(call.Subaccounts) > 0 {
		asked = nil
		for _, one := range accounts {
			if call.Wanted(one.ExternalID) {
				asked = append(asked, one)
			}
		}
	}
	if len(asked) == 0 {
		call.Notes.Addf("We Energies lists %d account%s, none of them the %d this pull asked for",
			len(accounts), plural(len(accounts)), len(call.Subaccounts))
		return Pull{}, nil
	}

	listed := make(map[string]WeEnergiesListRow, len(rows))
	for _, row := range rows {
		listed[weEnergiesRowAccount(row)] = row
	}

	var bills []Bill
	for index, account := range asked {
		balance := WeEnergiesBalanceFromRow(listed[account.ExternalID], account.ExternalID)
		// Every account is reached from the list, because the portal keeps one
		// "current account" of its own and a page opened directly answers for
		// whichever that is. The list is already on screen for the first.
		if index > 0 {
			if _, ok := w.accountRows(call); !ok {
				return w.SignInAgain(bills), nil
			}
		}
		var read struct {
			Rows   []WeEnergiesLedgerRow `json:"rows"`
			Booked []WeEnergiesBooked    `json:"booked"`
		}
		if why := w.ledgerFor(call, account.ExternalID); why != "" {
			if !weEnergiesAccountArea.MatchString(call.Page.URL()) {
				return w.SignInAgain(bills), nil
			}
			call.Notes.Addf("the bills of %s could not be read: %s; only the balance the account list shows is reported",
				account.MaskedNumber, why)
		} else if err := browser.EvaluateInto(call.Page, weEnergiesLedgerScript, map[string]any{
			"ledger": weEnergiesLedger, "scheduled": weEnergiesScheduled,
		}, &read); err != nil {
			call.Notes.Addf("the payment history for %s could not be read: %v; only the balance the account list shows is reported",
				account.MaskedNumber, err)
		}
		found := WeEnergiesBillsFromLedger(read.Rows, account.ExternalID, read.Booked, call.Notes)
		found = WeEnergiesWithBalance(found, balance)
		call.Notes.Addf("We Energies answered %d bill%s for %s from %d ledger row%s and the account list",
			len(found), plural(len(found)), account.MaskedNumber,
			len(read.Rows), plural(len(read.Rows)))
		bills = append(bills, found...)
	}
	return Pull{Bills: bills}, nil
}

func (w *WeEnergies) FetchDocument(call Call, bill Bill) (*Document, error) {
	raw := rawOf[weEnergiesRaw](bill)
	if raw.StatementPath == "" || call.Page == nil {
		return nil, nil
	}
	body, ok := fetchedPDF(call, "the statement for "+bill.ExternalID, raw.StatementPath)
	if !ok {
		return nil, nil
	}
	return pdfDocument(body, statementFilename("we-energies", bill,
		cmp.Or(bill.IssuedOn, bill.DueOn, "statement"))), nil
}

// WeEnergiesSubaccountsFromRows labels each account by its service address;
// the customer's name that leads the block is dropped.
func WeEnergiesSubaccountsFromRows(rows []WeEnergiesListRow) []Subaccount {
	found := make([]Subaccount, 0, len(rows))
	for _, row := range rows {
		id := weEnergiesRowAccount(row)
		if id == "" {
			continue
		}
		skip := strings.TrimSpace(row.CustomerName)
		parts := make([]string, 0, 4)
		for _, line := range strings.Split(row.Address, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line == skip || weEnergiesAccountLine.MatchString(line) {
				continue
			}
			parts = append(parts, line)
		}
		label := strings.Join(parts, ", ")
		if label == "" {
			label = "We Energies"
		}
		found = append(found, Subaccount{
			ExternalID: id, Label: label, MaskedNumber: domain.MaskAccount(id),
		})
	}
	return found
}

// weEnergiesRowAccount is a list row's account number: the portal's own
// `SelectAcct` argument, or the "Account #:" line where there is none.
func weEnergiesRowAccount(row WeEnergiesListRow) string {
	if id := strings.TrimSpace(row.SelectAcct); id != "" {
		return id
	}
	if line := weEnergiesAccountLine.FindStringSubmatch(row.Text); line != nil {
		return strings.TrimSpace(line[1])
	}
	return ""
}

type WeEnergiesBalance struct {
	Account string
	DueOn   string
	Amount  domain.Money
	// Read says the row stated a figure at all; a row that did not says
	// nothing about what is owed, not that nothing is.
	Read bool
}

// WeEnergiesBalanceFromRow reads the spans first and the row's whole text
// after, because a row whose spans are named differently still prints "Amt due by <date> <amount>".
// A credit balance is read as a figure owed of nothing.
func WeEnergiesBalanceFromRow(row WeEnergiesListRow, account string) WeEnergiesBalance {
	out := WeEnergiesBalance{Account: account}
	due := DayIn(row.DueBy)
	figure := weEnergiesFigure.FindString(row.DueAmount)
	credit := weEnergiesCredit.MatchString(row.DueAmount)
	if due == "" || figure == "" {
		if at := strings.Index(strings.ToLower(row.Text), "due by"); at >= 0 {
			rest := row.Text[at:]
			if due == "" {
				due = DayIn(rest)
			}
			if figure == "" {
				figure = weEnergiesFigure.FindString(rest)
				credit = weEnergiesCredit.MatchString(rest)
			}
		}
	}
	out.DueOn = due
	amount, read := Amount(figure)
	if !read {
		return out
	}
	if credit && amount.IsPositive() {
		amount = amount.Neg()
	}
	out.Amount, out.Read = amount, true
	return out
}

// WeEnergiesWithBalance settles the ledger's bills against the account list's
// balance. The list is the portal saying outright what is owed, so it outranks
// the ledger's inference from payment dates: a balance owed by a date is that
// bill open, whatever payment posted after it was issued, and a bill the
// ledger never showed — because it did not open, or had not caught up — is
// reported from the list alone. A balance of nothing owed settles every bill.
func WeEnergiesWithBalance(bills []Bill, balance WeEnergiesBalance) []Bill {
	if !balance.Read {
		return bills
	}
	if !Owes(balance.Amount) {
		for index := range bills {
			bills[index].Status = "Paid"
			bills[index].AutopayOn = ""
		}
		return bills
	}
	if balance.DueOn == "" {
		return bills
	}
	for index := range bills {
		if bills[index].DueOn == balance.DueOn {
			bills[index].Status = "Open"
			return bills
		}
	}
	current := Bill{
		Subaccount: balance.Account,
		ExternalID: balance.Account + ":" + balance.DueOn,
		DueOn:      balance.DueOn,
		AmountDue:  balance.Amount,
		Currency:   "USD",
		Status:     "Open",
	}
	// A payment booked for the balance's own due date was matched to an older
	// bill by amount only because the ledger did not show this one.
	for index := range bills {
		if bills[index].AutopayOn == balance.DueOn {
			current.AutopayOn = balance.DueOn
			bills[index].AutopayOn = ""
		}
	}
	bills = append([]Bill{current}, bills...)
	sort.SliceStable(bills, func(a, b int) bool { return bills[a].DueOn > bills[b].DueOn })
	if len(bills) > weEnergiesBillsToRead {
		bills = bills[:weEnergiesBillsToRead]
	}
	return bills
}

// WeEnergiesBillsFromLedger reads bill rows (a bill amount and a "Bill due"
// date) from the ledger. A bill counts as paid once a payment has posted on or
// after its own date: the portal never says so, but a payment dated inside a
// cycle is that cycle's. `booked` is the scheduled payment, which on an
// autopay account is the autopay, reported against the bill it matches.
func WeEnergiesBillsFromLedger(
	rows []WeEnergiesLedgerRow, account string, booked []WeEnergiesBooked, notes *Notes,
) []Bill {
	var paidOn []string
	for _, row := range rows {
		if _, paid := Amount(row.PaymentAmount); !paid {
			continue
		}
		if day := ISODate(row.Date); day != "" {
			paidOn = append(paidOn, day)
		}
	}
	type scheduled struct {
		on     string
		amount domain.Money
		priced bool
	}
	var scheduledPayments []scheduled
	for _, one := range booked {
		if on := ISODate(one.Date); on != "" {
			amount, priced := Amount(one.Amount)
			scheduledPayments = append(scheduledPayments, scheduled{on: on, amount: amount, priced: priced})
		}
	}

	var bills []Bill
	for _, row := range rows {
		match := weEnergiesBillDue.FindStringSubmatch(row.Description)
		if match == nil {
			continue
		}
		due := ISODate(match[1])
		if due == "" {
			continue
		}
		issued := ISODate(row.Date)
		amount, priced := Amount(row.BillAmount)
		if !priced {
			notes.Addf("a We Energies bill due %s carried no readable amount; it is left out", due)
			continue
		}
		settled := false
		for _, day := range paidOn {
			if issued != "" && day >= issued {
				settled = true
				break
			}
		}
		autopayOn := ""
		if !settled {
			// The payment booked for this bill's own due date, or failing that
			// the one for its exact amount: the portal dates a scheduled
			// payment by when it leaves, not by which cycle it settles.
			for _, one := range scheduledPayments {
				if one.on == due {
					autopayOn = one.on
					break
				}
			}
			if autopayOn == "" {
				for _, one := range scheduledPayments {
					if one.priced && one.amount.Equal(amount) {
						autopayOn = one.on
						break
					}
				}
			}
		}
		status := "Open"
		if settled {
			status = "Paid"
		}
		raw, _ := json.Marshal(weEnergiesRaw{StatementPath: row.StatementHref})
		bills = append(bills, Bill{
			Subaccount: account,
			ExternalID: account + ":" + cmp.Or(issued, due),
			IssuedOn:   issued,
			DueOn:      due,
			AmountDue:  amount,
			Currency:   "USD",
			AutopayOn:  autopayOn,
			Status:     status,
			Raw:        raw,
		})
	}
	// The ledger is newest first, but that is the portal's sort order and not a
	// promise; the newest bills are taken by their own dates.
	sort.SliceStable(bills, func(a, b int) bool { return bills[a].DueOn > bills[b].DueOn })
	if len(bills) > weEnergiesBillsToRead {
		bills = bills[:weEnergiesBillsToRead]
	}
	return bills
}
