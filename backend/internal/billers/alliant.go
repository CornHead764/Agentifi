package billers

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Alliant Energy is an `api` provider: it reads the portal's SmartCMobile JSON
// service, and a session is a bearer token plus the refresh token that minted
// it. See docs/connectors/providers.md for the routes and shapes.
//
// The service is reached from a page on the portal's origin (BrowserOrigin
// below); the code here is plain HTTP.

const (
	alliantOrigin = "https://myaccount.alliantenergy.com"
	// A light document at the origin for the page to sit on.
	alliantDocument = "/portal/public/browserInfo.js"

	alliantAuthPath      = "/UsermanagementAPI/api/1/Login/auth"
	alliantAddressesPath = "/Services/api/1/Addresses/User"
	// A refusal here is reported as a sign-in owed: the refresh does not revive
	// a session whose access token has expired.
	alliantRefreshPath = "/UsermanagementAPI/api/1/Login/auth/refresh"

	alliantCurrentBillPath = "/Services/api/1/bill/Current"
	alliantBillHistoryPath = "/Services/api/1/bill/History"
	alliantStatementPath   = "/Services/api/1/bill/GetBillPdf"
	alliantDocumentType    = "Bill"

	alliantSessionKind = "alliant-token"
)

// The `uid` header is the platform's channel for the call, not the person: 1
// on the login and 2 on everything after it.
const (
	alliantUIDLogin   = "1"
	alliantUIDService = "2"
)

// The alternative field names are fallbacks, because the SEW platform renames
// fields between its releases.
var (
	alliantAddressKeys = []string{"serviceAddress", "premiseAddress", "mailingAddress", "address", "addressLine1", "fullAddress"}
	alliantAccountKeys = []string{"accountNumber", "AccountNumber", "accountNo"}
	alliantPremiseKeys = []string{"premiseNumber", "PremiseNumber", "premiseNo"}

	alliantBillIDKeys      = []string{"invoiceId", "invoiceNumber", "billId", "billID"}
	alliantDueKeys         = []string{"netDueDate", "billDueDate", "dueDate", "paymentDueDate"}
	alliantIssuedKeys      = []string{"invoiceDate", "billDate", "statementDate"}
	alliantAmountKeys      = []string{"totalAmountDue", "amountDue", "currentCharges"}
	alliantPeriodStartKeys = []string{"billPeriodStartDate", "billStartDate", "serviceStartDate"}
	alliantPeriodEndKeys   = []string{"billPeriodEndDate", "billEndDate", "serviceEndDate"}
	alliantAutopayKeys     = []string{"upcomingAutoPayDate", "autoPayDate", "scheduledPaymentDate"}
	alliantBalanceKeys     = []string{"remainingBalance"}
)

// Base is settable so a test can point it at a scripted service; nothing else
// changes where the password is sent.
type Alliant struct {
	Base string
}

func NewAlliant() *Alliant {
	return &Alliant{Base: "https://alliant-svc.smartcmobile.com"}
}

func (a *Alliant) ID() domain.BillerID { return domain.BillerAlliant }

func (a *Alliant) SessionKinds() []string { return []string{alliantSessionKind} }

func (a *Alliant) BrowserOrigin() (string, string) { return alliantOrigin, alliantDocument }

// alliantSession is the kept token. The field names are fixed: sessions already
// sealed in this shape must still open.
type alliantSession struct {
	Kind         string `json:"kind"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	UUID         string `json:"uuid"`
	ExpiresAt    string `json:"expires_at"`
	Username     string `json:"username"`
	AccountHint  string `json:"account_hint"`
	RefreshedAt  string `json:"refreshed_at"`
}

func (a *Alliant) headers(uid, token string) map[string]string {
	out := map[string]string{
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "en-US,en;q=0.9",
		"Content-Type":    "application/json",
		"Origin":          alliantOrigin,
		"Referer":         alliantOrigin + "/",
		"pt":              "1",
		"st":              "PL",
		"uid":             uid,
	}
	if token != "" {
		out["Authorization"] = "Bearer " + token
	}
	return out
}

func (a *Alliant) ask(
	ctx context.Context, call Call, method, path string, headers map[string]string, body any,
) (Answered, error) {
	url := a.Base + path
	if body == nil {
		return Ask(ctx, call.Fetch, method, url, headers, nil, "the Alliant service")
	}
	return AskJSON(ctx, call.Fetch, method, url, headers, body, "the Alliant service")
}

func (a *Alliant) Authenticate(ctx context.Context, credentials Credentials, call Call) SignIn {
	if !credentials.HasLogin() {
		return SignIn{Failed: "Alliant Energy needs a username and a password"}
	}
	answered, err := a.ask(ctx, call, http.MethodPost, alliantAuthPath, a.headers(alliantUIDLogin, ""),
		map[string]any{
			"username":   credentials.Username,
			"password":   credentials.Password,
			"guestToken": "",
			"customattributes": map[string]any{
				"ip": "", "client": "Web", "version": "-",
				"deviceId": "||Chrome||128||Windows||-||", "deviceName": "Chrome",
				"deviceType": 0, "os": "Windows",
			},
		})
	if err != nil {
		return SignIn{Failed: err.Error(), Err: err}
	}
	envelope := ReadEnvelope(answered.Body)
	row := envelope.Row()
	if !answered.OK || !envelope.OK || row == nil {
		reason := envelope.Message
		if reason == "" {
			reason = fmt.Sprintf("the sign-in answered HTTP %d: %s", answered.Status, answered.Excerpt)
		}
		if answered.Status >= http.StatusInternalServerError {
			return SignIn{Failed: reason, Err: fmt.Errorf("Alliant Energy's sign-in failed: %s", reason)}
		}
		return SignIn{Failed: reason}
	}
	session := alliantSessionFrom(row, alliantSession{Username: credentials.Username}, call.At())
	if session.AccessToken == "" || session.UUID == "" {
		return SignIn{Failed: "the sign-in answered without a token or a user id"}
	}
	call.Notes.Tracef("signed in to Alliant Energy; the token is good until %s", session.ExpiresAt)
	return SignIn{Session: mustSession(session)}
}

// Refresh renews the kept token. The refresh token rotates a live session but
// does not revive one whose access token has already expired, so a refusal
// here is a sign-in owed, which with a kept password the engine makes itself.
func (a *Alliant) Refresh(call Call) (Session, bool, string) {
	kept, err := readAlliantSession(call.Session)
	if err != nil || kept.RefreshToken == "" {
		return nil, true, "the kept Alliant Energy session carries no refresh token"
	}
	answered, err := a.ask(call.Ctx, call, http.MethodPost, alliantRefreshPath,
		a.headers(alliantUIDService, kept.AccessToken),
		map[string]any{"refreshToken": kept.RefreshToken})
	if err != nil {
		return nil, true, err.Error()
	}
	envelope := ReadEnvelope(answered.Body)
	row := envelope.Row()
	if !answered.OK || !envelope.OK || row == nil || Text(row["accessToken"]) == "" {
		reason := envelope.Message
		if reason == "" {
			reason = answered.Excerpt
		}
		refused := &httpx.StatusError{Status: answered.Status, Excerpt: reason}
		call.Notes.Tracef("the refresh at %s answered %s; a sign-in is asked for instead",
			alliantRefreshPath, refused)
		return nil, true, fmt.Sprintf("the refresh answered HTTP %d", answered.Status)
	}
	call.Notes.Tracef("the kept Alliant Energy session refreshed")
	return mustSession(alliantSessionFrom(row, kept, call.At())), false, ""
}

func (a *Alliant) Subaccounts(call Call) ([]Subaccount, error) {
	kept, err := readAlliantSession(call.Session)
	if err != nil {
		return nil, err
	}
	answered, err := a.ask(call.Ctx, call, http.MethodGet,
		alliantAddressesPath+"/"+kept.UUID, a.headers(alliantUIDService, kept.AccessToken), nil)
	if err != nil {
		return nil, err
	}
	if answered.Status == http.StatusUnauthorized || answered.Status == http.StatusForbidden {
		return nil, ErrNeedsSignIn
	}
	envelope := ReadEnvelope(answered.Body)
	rows := envelope.Rows()
	if !answered.OK || !envelope.OK || rows == nil {
		reason := envelope.Message
		if reason == "" {
			reason = answered.Excerpt
		}
		call.Notes.Addf("the account walk answered HTTP %d: %s", answered.Status, reason)
		return nil, nil
	}
	var found []Subaccount
	for _, row := range rows {
		if one, ok := AlliantSubaccountFromAddress(row); ok {
			found = append(found, one)
		}
	}
	call.Notes.Addf("Alliant Energy lists %d billed account%s", len(found), plural(len(found)))
	return found, nil
}

// FetchBills reads bill/Current per billed account, and bill/History behind it
// for the earlier bills, which are sent settled so their statements can be
// filed; AlliantEarlierBills says how they are dated.
func (a *Alliant) FetchBills(call Call) (Pull, error) {
	kept, err := readAlliantSession(call.Session)
	if err != nil {
		return Pull{}, err
	}
	if len(call.Subaccounts) == 0 {
		call.Notes.Addf("no billed account was named, so no bill was asked for")
	}
	out := Pull{}
	for _, id := range call.Subaccounts {
		accountNumber := alliantAccountNumberOf(id)
		answered, err := a.ask(call.Ctx, call, http.MethodPost, alliantCurrentBillPath,
			a.headers(alliantUIDService, kept.AccessToken),
			map[string]any{"accountNumbers": []string{accountNumber}})
		if err != nil {
			return Pull{}, err
		}
		if answered.Status == http.StatusUnauthorized || answered.Status == http.StatusForbidden {
			return Pull{NeedsSignIn: true, Reason: "Alliant Energy refused the kept token"}, nil
		}
		if !answered.OK {
			call.Notes.Addf("%s answered HTTP %d for %s: %s",
				alliantCurrentBillPath, answered.Status, id, answered.Excerpt)
			continue
		}
		envelope := ReadEnvelope(answered.Body)
		row := envelope.Row()
		if !envelope.OK || row == nil {
			detail := ""
			if envelope.Message != "" {
				detail = ": " + envelope.Message
			}
			call.Notes.Addf("%s answered no current bill for %s%s", alliantCurrentBillPath, id, detail)
			continue
		}
		current, ok := AlliantBillFromRow(row, id, call.Notes)
		if ok {
			out.Bills = append(out.Bills, current)
		} else {
			// Its row is in the history too, where it must not pass for settled.
			current = Bill{ExternalID: Text(Pick(row, alliantBillIDKeys...))}
		}
		out.Bills = append(out.Bills,
			AlliantEarlierBills(a.billHistory(call, kept, accountNumber), current, id, call.Notes)...)
	}
	return out, nil
}

// billHistory is the last two years. A failure here is a note, never a failed pull, because the
// current bill is already in hand.
func (a *Alliant) billHistory(call Call, kept alliantSession, accountNumber string) []map[string]any {
	now := call.At()
	start := now.AddDate(-2, 0, 0)
	answered, err := a.ask(call.Ctx, call, http.MethodPost, alliantBillHistoryPath,
		a.headers(alliantUIDService, kept.AccessToken),
		map[string]any{
			"accountNumbers": []string{accountNumber},
			"startDate":      start.Format("20060102"),
			"endDate":        now.Format("20060102"),
		})
	if err != nil {
		call.Notes.Addf("%s could not be read: %v", alliantBillHistoryPath, err)
		return nil
	}
	if !answered.OK {
		call.Notes.Addf("%s answered HTTP %d: %s", alliantBillHistoryPath, answered.Status, answered.Excerpt)
		return nil
	}
	return ReadEnvelope(answered.Body).Rows()
}

// FetchDocument gets the statement in two steps: GetBillPdf answers a signed
// link on the statement host, and the bytes come from a plain GET of that link
// — the portal opens it in a new tab with nothing attached, so nothing is sent.
//
// Answers nil rather than an error for the same reason FetchBills notes rather
// than fails: the bill is worth having without its statement.
func (a *Alliant) FetchDocument(call Call, bill Bill) (*Document, error) {
	kept, err := readAlliantSession(call.Session)
	if err != nil {
		return nil, nil
	}
	invoiceID := bill.ExternalID
	var raw map[string]any
	if len(bill.Raw) > 0 && httpx.DecodeJSON(bill.Raw, &raw) == nil {
		if found := Pick(raw, alliantBillIDKeys...); found != nil {
			invoiceID = Text(found)
		}
	}
	answered, err := a.ask(call.Ctx, call, http.MethodPost, alliantStatementPath,
		a.headers(alliantUIDService, kept.AccessToken),
		map[string]any{
			"accountNumber": alliantAccountNumberOf(bill.Subaccount),
			"invoiceId":     invoiceID,
			"documentType":  alliantDocumentType,
		})
	if err != nil {
		call.Notes.Addf("the statement for %s could not be asked for: %v", bill.ExternalID, err)
		return nil, nil
	}
	if !answered.OK {
		call.Notes.Addf("%s answered HTTP %d for %s: %s",
			alliantStatementPath, answered.Status, bill.ExternalID, answered.Excerpt)
		return nil, nil
	}
	envelope := ReadEnvelope(answered.Body)
	row := envelope.Row()
	link := ""
	if row != nil {
		link = Text(row["billPdf"])
	}
	if !envelope.OK || !strings.HasPrefix(link, "https://") {
		detail := ""
		if envelope.Message != "" {
			detail = ": " + envelope.Message
		}
		call.Notes.Addf("%s answered no statement link for %s%s", alliantStatementPath, bill.ExternalID, detail)
		return nil, nil
	}

	statement, err := Ask(call.Ctx, call.Fetch, http.MethodGet, link,
		map[string]string{"Accept": "application/pdf,*/*"}, nil, "the statement host")
	if err != nil {
		call.Notes.Addf("the statement link for %s could not be fetched: %v", bill.ExternalID, err)
		return nil, nil
	}
	if !statement.OK {
		call.Notes.Addf("the statement host answered HTTP %d for %s", statement.Status, bill.ExternalID)
		return nil, nil
	}
	if len(statement.Raw) == 0 {
		call.Notes.Addf("the statement host answered an empty file for %s", bill.ExternalID)
		return nil, nil
	}
	if !isPDF(statement.Raw) {
		call.Notes.Addf(
			"the statement host answered something that is not a PDF for %s (it began %q); "+
				"the link may want a browser session",
			bill.ExternalID, alliantOpening(statement.Raw))
		return nil, nil
	}
	stamp := cmp.Or(bill.DueOn, bill.IssuedOn, "statement")
	return pdfDocument(statement.Raw, fmt.Sprintf("alliant-%s-%s.pdf", bill.Subaccount, stamp)), nil
}

// AlliantSubaccountFromAddress reads one row of Addresses/User/{uuid}. The
// external id is `{premise}-{account}`, which is the form the platform's
// own usage service takes an account in, so the billing call can be made from
// the id alone.
func AlliantSubaccountFromAddress(row map[string]any) (Subaccount, bool) {
	account := Pick(row, alliantAccountKeys...)
	if account == nil {
		return Subaccount{}, false
	}
	external := Text(account)
	if premise := Pick(row, alliantPremiseKeys...); premise != nil {
		external = Text(premise) + "-" + external
	}
	label := AlliantAddressLabel(row)
	if label == "" {
		label = "Account " + Text(account)
	}
	return Subaccount{
		ExternalID:   external,
		Label:        label,
		MaskedNumber: domain.MaskAccount(Text(account)),
	}, true
}

// AlliantAddressLabel is the nickname when one is set, else the service
// address's street and city. The address may be an object or a string.
func AlliantAddressLabel(row map[string]any) string {
	if nick := Pick(row, "nickName", "nickname"); nick != nil {
		return strings.TrimSpace(Text(nick))
	}
	address := Pick(row, alliantAddressKeys...)
	if address == nil {
		return ""
	}
	object, ok := address.(map[string]any)
	if !ok {
		return strings.TrimSpace(Text(address))
	}
	street := strings.TrimSpace(strings.TrimSpace(Text(object["address1"])) + " " +
		strings.TrimSpace(Text(object["address2"])))
	city := strings.TrimSpace(Text(object["city"]))
	parts := make([]string, 0, 2)
	if street != "" {
		parts = append(parts, street)
	}
	if city != "" {
		parts = append(parts, city)
	}
	return strings.Join(parts, ", ")
}

// Only bill/Current carries a due date, so this is the one row that becomes a
// bill. A row it cannot price is a note rather than a zero, and the status is
// worked out from the balance because the service states none.
func AlliantBillFromRow(row map[string]any, subaccount string, notes *Notes) (Bill, bool) {
	if row == nil {
		return Bill{}, false
	}
	rawAmount := Pick(row, alliantAmountKeys...)
	amount, read := Amount(rawAmount)
	if !read {
		notes.Addf(
			"a bill on %s carried an amount this module could not read (%s); "+
				"the bill is left out rather than sent as zero",
			subaccount, jsonish(rawAmount))
		return Bill{}, false
	}
	due := ISODate(Pick(row, alliantDueKeys...))
	issued := ISODate(Pick(row, alliantIssuedKeys...))
	external := ""
	if id := Pick(row, alliantBillIDKeys...); id != nil {
		external = Text(id)
	} else {
		stamp := due
		if stamp == "" {
			stamp = issued
		}
		if stamp == "" {
			stamp = "undated"
		}
		external = subaccount + ":" + stamp
	}
	encoded, _ := json.Marshal(row)
	return Bill{
		Subaccount:  subaccount,
		ExternalID:  external,
		IssuedOn:    issued,
		DueOn:       due,
		AmountDue:   amount,
		Currency:    "USD",
		PeriodStart: ISODate(Pick(row, alliantPeriodStartKeys...)),
		PeriodEnd:   ISODate(Pick(row, alliantPeriodEndKeys...)),
		AutopayOn:   ISODate(Pick(row, alliantAutopayKeys...)),
		Status:      alliantStatusOf(row),
		Raw:         encoded,
	}, true
}

// The service states no status.
func alliantStatusOf(row map[string]any) string {
	balance, read := Amount(Pick(row, alliantBalanceKeys...))
	if read && !Owes(balance) {
		return "Paid"
	}
	return "Open"
}

// AlliantEarlierBills is bill/History as bills, each keyed by its invoice so a
// later pull restates it rather than adding it again.
//
// History states no due date, and a bill is stored under one. Each earlier
// bill is given its bill date plus the current bill's own bill-to-due gap —
// the one term this account is known to be billed on — and is sent Paid: it
// is a place to file a statement, never a reminder, and a paid bill claims no
// series slot and is never overdue. Without a current bill to take the gap
// from, the due date is the bill date itself; the note says which.
func AlliantEarlierBills(rows []map[string]any, current Bill, subaccount string, notes *Notes) []Bill {
	gap, gapKnown := alliantDueGap(current)
	var out []Bill
	unkeyed := 0
	for _, row := range rows {
		id := Pick(row, alliantBillIDKeys...)
		issued := ISODate(Pick(row, alliantIssuedKeys...))
		if id == nil || Text(id) == "" || issued == "" {
			unkeyed++
			continue
		}
		if Text(id) == current.ExternalID {
			continue
		}
		rawAmount := Pick(row, alliantAmountKeys...)
		amount, read := Amount(rawAmount)
		if !read {
			notes.Addf("an earlier bill on %s carried an amount this module could not read (%s); it is left out",
				subaccount, jsonish(rawAmount))
			continue
		}
		due := ISODate(Pick(row, alliantDueKeys...))
		if due == "" {
			due = alliantDaysAfter(issued, gap)
		}
		if due == "" {
			unkeyed++
			continue
		}
		encoded, _ := json.Marshal(row)
		out = append(out, Bill{
			Subaccount: subaccount,
			ExternalID: Text(id),
			IssuedOn:   issued,
			DueOn:      due,
			AmountDue:  amount,
			Currency:   "USD",
			Status:     "Paid",
			Raw:        encoded,
		})
	}
	if unkeyed > 0 {
		notes.Addf("%d earlier bill%s on %s carried no invoice or no bill date and %s left out",
			unkeyed, plural(unkeyed), subaccount, map[bool]string{true: "was", false: "were"}[unkeyed == 1])
	}
	if len(out) == 0 {
		return nil
	}
	if gapKnown {
		notes.Addf("%d earlier bill%s on %s filed as paid, each due %d days after its bill date "+
			"as the current bill is; the history states no due date",
			len(out), plural(len(out)), subaccount, gap)
	} else {
		notes.Addf("%d earlier bill%s on %s filed as paid and dated on the bill date; "+
			"the history states no due date and there was no current bill to take the term from",
			len(out), plural(len(out)), subaccount)
	}
	return out
}

func alliantDueGap(current Bill) (int, bool) {
	issued, err := domain.ParseDate(current.IssuedOn)
	if err != nil {
		return 0, false
	}
	due, err := domain.ParseDate(current.DueOn)
	if err != nil || due.Before(issued) {
		return 0, false
	}
	return domain.DaysBetween(issued, due), true
}

func alliantDaysAfter(day string, days int) string {
	at, err := domain.ParseDate(day)
	if err != nil {
		return ""
	}
	return at.AddDays(days).String()
}

// alliantOpening is the start of an answer that should have been a PDF, for
// the note that says what came instead.
func alliantOpening(body []byte) string {
	return textutil.Clip(strings.Join(strings.Fields(string(body)), " "), 80)
}

// alliantAccountNumberOf is the account number behind a subaccount id, which is
// `<premise>-<account>`.
func alliantAccountNumberOf(subaccountID string) string {
	parts := strings.Split(subaccountID, "-")
	return parts[len(parts)-1]
}

func alliantSessionFrom(row map[string]any, previous alliantSession, now time.Time) alliantSession {
	user, _ := row["user"].(map[string]any)
	minutes := int64(5)
	if stated, err := strconv.ParseInt(Text(row["expiresIn"]), 10, 64); err == nil && stated > 0 {
		minutes = stated
	}
	out := alliantSession{
		Kind:         alliantSessionKind,
		AccessToken:  Text(row["accessToken"]),
		RefreshToken: Text(row["refreshToken"]),
		UUID:         Text(user["uuid"]),
		// expiresIn is minutes on this platform.
		ExpiresAt:   now.Add(time.Duration(minutes) * time.Minute).UTC().Format(time.RFC3339),
		Username:    previous.Username,
		AccountHint: Text(user["firstName"]),
		RefreshedAt: now.UTC().Format(time.RFC3339),
	}
	if out.RefreshToken == "" {
		out.RefreshToken = previous.RefreshToken
	}
	if out.UUID == "" {
		out.UUID = previous.UUID
	}
	if out.AccountHint == "" {
		out.AccountHint = Text(user["firstname"])
	}
	if out.AccountHint == "" {
		out.AccountHint = previous.AccountHint
	}
	return out
}

func readAlliantSession(session Session) (alliantSession, error) {
	var kept alliantSession
	if len(session) == 0 {
		return kept, fmt.Errorf("billers: Alliant Energy was asked for bills with no kept session")
	}
	if err := json.Unmarshal(session, &kept); err != nil {
		return kept, fmt.Errorf("billers: the kept Alliant Energy session is unreadable: %w", err)
	}
	return kept, nil
}

func mustSession(value any) Session {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return Session(encoded)
}
