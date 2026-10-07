package billers

import (
	"cmp"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
)

// Northwestern Mutual reads what the signed-in portal's own app fetches. See
// docs/connectors/providers.md.
//
// The factor page offers its one choice as an ARIA radio (`div role=radio`)
// rather than an input, which form.go and factor.go read.
//
// `login.northwesternmutual.com` carries the form, the factor page and the
// code page alike, so none of it is the account area. The signed-in site is
// `plan.northwesternmutual.com`, which signed out sends to that form; its
// `/saml/` paths are the hand-over and the sign-out, so they are not inside.
//
// Every page's app posts its GraphQL questions to one address with the
// session's cookies, so the answer a page needs is told apart by what it
// holds, and recorded rather than asked again.

const (
	northwesternMutualHome     = "https://www.northwesternmutual.com"
	northwesternMutualSignIn   = northwesternMutualHome + "/login/"
	northwesternMutualPlan     = "https://plan.northwesternmutual.com"
	northwesternMutualSummary  = northwesternMutualPlan + "/summary"
	northwesternMutualBilling  = northwesternMutualPlan + "/billing"
	northwesternMutualActivity = northwesternMutualPlan + "/wallet/payment-activity"

	// The app asks as soon as each page renders; this is the ceiling before
	// the answer is that it never asked.
	northwesternMutualWait = 45 * time.Second

	// northwesternMutualSameDraft is how far a processed payment may land from
	// the day the billing page scheduled it and still be that payment.
	northwesternMutualSameDraft = 5
)

var northwesternMutualGraphQL = regexp.MustCompile(
	`(?i)^https://api\.plan\.northwesternmutual\.com/graphql(?:[?#]|$)`)

// northwesternMutualInside is the plan site, minus its SAML paths.
func northwesternMutualInside(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "plan.northwesternmutual.com") {
		return false
	}
	path := strings.ToLower(parsed.Path)
	return path != "/saml" && !strings.HasPrefix(path, "/saml/") && !strings.HasPrefix(path, "/logout")
}

type NorthwesternMutual struct {
	Draft
}

func NewNorthwesternMutual() *NorthwesternMutual {
	return &NorthwesternMutual{Draft{
		BillerID:    domain.BillerNorthwesternMutual,
		Home:        northwesternMutualHome,
		SignIn:      northwesternMutualSignIn,
		Landing:     northwesternMutualSummary,
		AccountArea: northwesternMutualInside,
		AccountPage: northwesternMutualSummary,
	}}
}

// NorthwesternMutualBillingAccount is one billing account on the billing
// page. Its figures are display strings: "$1,234.56" and "Mar 3, 2027".
type NorthwesternMutualBillingAccount struct {
	Number    string `json:"isaNumber"`
	Frequency string `json:"frequency"`
	Payment   struct {
		DueAmount any    `json:"dueAmount"`
		DueDate   string `json:"dueDate"`
		DateLabel string `json:"dueDateLabel"`
	} `json:"paymentDetails"`
	Tag struct {
		Text string `json:"text"`
	} `json:"tag"`
}

// NorthwesternMutualPayment is one row of the wallet's payment activity.
// Amount is a JSON number; Date is ISO.
type NorthwesternMutualPayment struct {
	Amount      any    `json:"amount"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Status      string `json:"status"`
	StatusShown string `json:"statusFormatted"`
	Action      struct {
		Name  string `json:"name"`
		Shown string `json:"displayName"`
	} `json:"action"`
}

type northwesternMutualBillingAnswer struct {
	Data struct {
		Payments *struct {
			BillingAccounts []NorthwesternMutualBillingAccount `json:"billingAccounts"`
		} `json:"payments"`
	} `json:"data"`
}

type northwesternMutualActivityAnswer struct {
	Data struct {
		Wallet *struct {
			PaymentActivity *struct {
				TransactionHistory struct {
					Transactions []NorthwesternMutualPayment `json:"transactions"`
				} `json:"transactionHistory"`
			} `json:"paymentActivity"`
		} `json:"wallet"`
	} `json:"data"`
}

type northwesternMutualRaw struct {
	Account     string `json:"account"`
	Frequency   string `json:"frequency,omitempty"`
	Description string `json:"description,omitempty"`
	Shown       string `json:"shown,omitempty"`
}

// recorded opens page and hands take each GraphQL answer the app receives
// until take keeps one. It answers false for a profile that is not signed in;
// an answer that never came is a note and true, with heard false.
func (n *NorthwesternMutual) recorded(
	call Call, page, what string, take func(body []byte) bool,
) (signedIn, heard bool) {
	recorded := browser.RecordResponses(call.Page, northwesternMutualGraphQL)
	// The navigation's own error is not fatal: where the browser stands
	// afterwards is the answer.
	_ = call.Page.Goto(page)
	call.Page.Settle()
	if !northwesternMutualInside(call.Page.URL()) {
		call.Notes.Tracef("northwesternmutual.com sent %s on to %s; the profile is not signed in",
			page, browser.WithoutQuery(call.Page.URL()))
		return false, false
	}
	response, ok := recorded.AwaitWhere(call.Page, northwesternMutualWait, func(response browser.Response) bool {
		if status := response.Status(); status == 401 || status == 403 {
			return true
		}
		body, err := response.Body()
		return err == nil && take(body)
	})
	if !ok {
		call.Notes.Addf("Northwestern Mutual's %s did not load within %s; it was not read", what, northwesternMutualWait)
		return true, false
	}
	if status := response.Status(); status == 401 || status == 403 {
		call.Notes.Tracef("the app's GraphQL call on %s answered HTTP %d", page, status)
		return false, false
	}
	return true, true
}

// read is the billing accounts and, when activity is asked for, the payment
// activity. It answers false for a profile that is not signed in.
func (n *NorthwesternMutual) read(call Call, activity bool) (
	accounts []NorthwesternMutualBillingAccount, payments []NorthwesternMutualPayment, heard, signedIn bool,
) {
	signedIn, heard = n.recorded(call, northwesternMutualBilling, "billing page", func(body []byte) bool {
		var answer northwesternMutualBillingAnswer
		if httpx.DecodeJSON(body, &answer) != nil || answer.Data.Payments == nil {
			return false
		}
		accounts = answer.Data.Payments.BillingAccounts
		return true
	})
	if !signedIn || !heard || !activity {
		return accounts, nil, heard, signedIn
	}
	signedIn, _ = n.recorded(call, northwesternMutualActivity, "payment activity", func(body []byte) bool {
		var answer northwesternMutualActivityAnswer
		if httpx.DecodeJSON(body, &answer) != nil ||
			answer.Data.Wallet == nil || answer.Data.Wallet.PaymentActivity == nil {
			return false
		}
		payments = answer.Data.Wallet.PaymentActivity.TransactionHistory.Transactions
		return true
	})
	if !signedIn {
		return nil, nil, false, false
	}
	return accounts, payments, true, true
}

func (n *NorthwesternMutual) Subaccounts(call Call) ([]Subaccount, error) {
	if call.Page == nil {
		return nil, nil
	}
	accounts, _, _, signedIn := n.read(call, false)
	if !signedIn {
		return nil, ErrNeedsSignIn
	}
	found := NorthwesternMutualSubaccounts(accounts, call.Notes)
	call.Notes.Addf("Northwestern Mutual lists %d billing account%s", len(found), plural(len(found)))
	return found, nil
}

func (n *NorthwesternMutual) FetchBills(call Call) (Pull, error) {
	if call.Page == nil {
		return n.NoPage(), nil
	}
	accounts, payments, heard, signedIn := n.read(call, true)
	if !signedIn {
		return n.SignInAgain(nil), nil
	}
	if !heard {
		return Pull{}, nil
	}
	var bills []Bill
	open := 0
	for _, bill := range NorthwesternMutualBills(accounts, payments, call.Notes) {
		if len(call.Subaccounts) > 0 && !call.Wanted(bill.Subaccount) {
			continue
		}
		if bill.Status == "Open" {
			open++
		}
		bills = append(bills, bill)
	}
	paid := len(bills) - open
	call.Notes.Addf("Northwestern Mutual answered %d paid bill%s and %d due", paid, plural(paid), open)
	return Pull{Bills: bills}, nil
}

// northwesternMutualLabel is a billing account as the portal names it.
func northwesternMutualLabel(account NorthwesternMutualBillingAccount) string {
	return "Billing Account ****" + domain.LastFour(account.Number)
}

// NorthwesternMutualSubaccounts is one billed account per billing account,
// labelled with its masked number and how often it is billed.
func NorthwesternMutualSubaccounts(accounts []NorthwesternMutualBillingAccount, notes *Notes) []Subaccount {
	found := make([]Subaccount, 0, len(accounts))
	for _, account := range accounts {
		number := strings.TrimSpace(account.Number)
		if domain.LastFour(number) == "" {
			notes.Addf("a Northwestern Mutual billing account carried no readable number; it is left out")
			continue
		}
		label := northwesternMutualLabel(account)
		if frequency := strings.TrimSpace(account.Frequency); frequency != "" {
			label += ", " + frequency
		}
		found = append(found, Subaccount{ExternalID: number, Label: label, MaskedNumber: domain.MaskAccount(number)})
	}
	return found
}

// NorthwesternMutualBills is each billing account's processed payments, filed
// paid, and what its billing page says is due next, filed open.
//
// The portal states no due date for a past payment, so a paid bill is dated by
// the day the payment was processed, summed per day: a bill is known by its
// billed account and its date. The open bill is due on the billing page's
// date; labelled "Scheduled For", that is the day the money is drafted.
//
// A processed payment of the open bill's amount within a few days of its date
// is that bill already paid, so the open bill is left out: autopay drafts on
// the scheduled day, the paid bill then falls on the same date and the two are
// one bill, and a draft a day or two late is not a second one.
//
// Only a payment from a listed billing account is a bill. A payment in a
// status other than processed is a note, because a pending or returned one
// has not settled the bill and an unknown one cannot be read as settled.
func NorthwesternMutualBills(
	accounts []NorthwesternMutualBillingAccount, payments []NorthwesternMutualPayment, notes *Notes,
) []Bill {
	byFour := map[string][]NorthwesternMutualBillingAccount{}
	for _, account := range accounts {
		if four := domain.LastFour(account.Number); four != "" {
			byFour[four] = append(byFour[four], account)
		}
	}

	type day struct {
		account NorthwesternMutualBillingAccount
		date    string
		amount  domain.Money
		said    []string
	}
	days := map[string]*day{}
	for _, payment := range payments {
		if !strings.EqualFold(strings.TrimSpace(payment.Action.Name), "BILLING_LINK") {
			continue
		}
		shown := strings.TrimSpace(payment.Action.Shown)
		matched := byFour[domain.LastFour(shown)]
		if !strings.Contains(strings.ToLower(shown), "billing account") || len(matched) != 1 {
			notes.Addf("Northwestern Mutual shows a payment from %q, which is not one billing account "+
				"the billing page lists; it is left out", shown)
			continue
		}
		account := matched[0]
		date := ISODate(payment.Date)
		amount, priced := Amount(payment.Amount)
		if date == "" || !priced {
			notes.Addf("a Northwestern Mutual payment from %s carried no readable date or amount (%s, %s); "+
				"it is left out", northwesternMutualLabel(account), jsonish(payment.Date), jsonish(payment.Amount))
			continue
		}
		if !Owes(amount) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(payment.Status), "PROCESSED") {
			notes.Addf("Northwestern Mutual shows a payment of %s from %s on %s as %q; "+
				"only a processed payment is filed", amount, northwesternMutualLabel(account), date,
				cmp.Or(strings.TrimSpace(payment.StatusShown), strings.TrimSpace(payment.Status)))
			continue
		}
		key := strings.TrimSpace(account.Number) + ":" + date
		one := days[key]
		if one == nil {
			one = &day{account: account, date: date, amount: domain.Zero}
			days[key] = one
		}
		one.amount = one.amount.Add(amount)
		if said := strings.TrimSpace(payment.Description); said != "" {
			one.said = append(one.said, said)
		}
	}

	var bills []Bill
	for key, one := range days {
		raw, _ := json.Marshal(northwesternMutualRaw{
			Account: northwesternMutualLabel(one.account), Frequency: one.account.Frequency,
			Description: strings.Join(one.said, ", "),
		})
		bills = append(bills, Bill{
			Subaccount: strings.TrimSpace(one.account.Number),
			ExternalID: key,
			IssuedOn:   one.date,
			DueOn:      one.date,
			AmountDue:  one.amount,
			Currency:   "USD",
			Status:     "Paid",
			Raw:        raw,
		})
	}

	for _, account := range accounts {
		bill, ok := northwesternMutualDue(account, notes)
		if !ok {
			continue
		}
		if paid, found := northwesternMutualPaidNear(bills, bill); found {
			notes.Tracef("the %s due %s is the payment processed %s", bill.AmountDue, bill.DueOn, paid)
			continue
		}
		bills = append(bills, bill)
	}

	sort.Slice(bills, func(i, j int) bool { return bills[i].ExternalID < bills[j].ExternalID })
	return bills
}

// northwesternMutualDue is what the billing page says the account owes next,
// or false when it owes nothing or says so unreadably.
func northwesternMutualDue(account NorthwesternMutualBillingAccount, notes *Notes) (Bill, bool) {
	number := strings.TrimSpace(account.Number)
	if domain.LastFour(number) == "" {
		return Bill{}, false
	}
	shownAmount := strings.TrimSpace(Text(account.Payment.DueAmount))
	shownDate := strings.TrimSpace(account.Payment.DueDate)
	if shownAmount == "" && shownDate == "" {
		return Bill{}, false
	}
	amount, priced := Amount(account.Payment.DueAmount)
	due := NorthwesternMutualShownDay(shownDate)
	if !priced || due == "" {
		notes.Addf("Northwestern Mutual's billing page shows %s due as %s on %s, which could not be read; "+
			"no bill is filed for it", northwesternMutualLabel(account), jsonish(account.Payment.DueAmount),
			jsonish(account.Payment.DueDate))
		return Bill{}, false
	}
	if !Owes(amount) {
		return Bill{}, false
	}
	autopay := ""
	if strings.EqualFold(strings.TrimSpace(account.Payment.DateLabel), "Scheduled For") {
		autopay = due
	}
	raw, _ := json.Marshal(northwesternMutualRaw{
		Account: northwesternMutualLabel(account), Frequency: account.Frequency,
		Shown: strings.TrimSpace(account.Payment.DateLabel + " " + account.Tag.Text),
	})
	return Bill{
		Subaccount: number,
		ExternalID: number + ":" + due,
		DueOn:      due,
		AmountDue:  amount,
		Currency:   "USD",
		AutopayOn:  autopay,
		Status:     "Open",
		Raw:        raw,
	}, true
}

// northwesternMutualPaidNear is the date of a paid bill on the same account,
// for the same amount, within northwesternMutualSameDraft days of due.
func northwesternMutualPaidNear(bills []Bill, due Bill) (string, bool) {
	dueOn, err := domain.ParseDate(due.DueOn)
	if err != nil {
		return "", false
	}
	for _, bill := range bills {
		if bill.Subaccount != due.Subaccount || bill.Status != "Paid" || !bill.AmountDue.Equal(due.AmountDue) {
			continue
		}
		paidOn, err := domain.ParseDate(bill.DueOn)
		if err != nil {
			continue
		}
		if apart := domain.DaysBetween(dueOn, paidOn); apart >= -northwesternMutualSameDraft &&
			apart <= northwesternMutualSameDraft {
			return bill.DueOn, true
		}
	}
	return "", false
}

// NorthwesternMutualShownDay is a date the billing page writes, "Mar 3, 2027"
// or "March 3, 2027", as ISO, or "" for anything else — an ISO or slashed day,
// or text carrying more than the date, is not this page's own spelling.
func NorthwesternMutualShownDay(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	day, ok := billmail.ParseDay(text, []string{"Jan 2, 2006", "January 2, 2006"})
	if !ok {
		return ""
	}
	return day.String()
}
