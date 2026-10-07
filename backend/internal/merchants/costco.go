package merchants

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The Costco module; the connector is described in docs/connectors/merchants.md.
//
// The pull that runs in practice is the handed-over session: a Microsoft B2C
// refresh token (costco_session.go) that every pull refreshes and hands back
// rotated, with its calls made from a Camoufox page (costco_session.go says
// why). The three browser-pull paths, most robust first
// (keep the JSON the page's own calls are answered with; ask the endpoint from
// inside the page with the stored token; read the DOM, which has no items),
// are guesses from what the site's client is understood to send. Every read
// that finds nothing says so in the notes with a glimpse of the page.

const costcoHome = "https://www.costco.com"

// The hash route is what the site's own account menu links to; the fallback is
// the older server-rendered page.
const (
	costcoOrdersURL         = costcoHome + "/myaccount/#/orders-and-purchases"
	costcoOrdersURLFallback = costcoHome + "/OrderStatusCmd"
)

func costcoOrderURL(number string) string {
	return costcoHome + "/OrderStatusDetailsCmd?orderNumber=" + url.QueryEscape(number)
}

// Every call to the endpoint carries a fixed client identifier, the member's
// bearer under two names, and three routing headers without which the
// gateway's backend answers 404.
const (
	costcoGraphQL           = "https://ecom-api.costco.com/ebusiness/order/v1/orders/graphql"
	costcoClientIdentifier  = "481b1aec-aa3b-454b-b81b-48187e28f205"
	costcoWCSClientID       = "4900eb1f-0c10-4bd9-99c3-c59e6c1ecebf"
	costcoService           = "restOrders"
	costcoEnv               = "ecom"
	costcoWarehouseNumber   = "847"
	costcoGraphQLPageSize   = 25
	costcoGraphQLMaxPages   = 20
	costcoReceiptDetailsMax = 400
)

var costcoGraphQLMatch = regexp.MustCompile(`(?i)/orders/graphql|/ebusiness/order`)

// Costco's edge never answers a request without a browser's User-Agent: the
// connection is left to time out.
const costcoUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// The Entra B2C tenant, policy and client id a costco.com MSAL cache holds; a
// session carries its own and these are the fallback.
const (
	costcoSessionKind = "costco-b2c"
	costcoB2CHost     = "https://signin.costco.com"
	costcoB2CTenant   = "e0714dd4-784d-46d6-a278-3e29553483eb"
	costcoB2CPolicy   = "B2C_1A_SSO_WCS_signup_signin_209"
	costcoB2CClientID = "a3a5186b-7c89-4b4c-93a8-dd604e930757"
)

// The errors that mean the person has to sign in again, as against the
// endpoint being unwell.
var costcoSignInAgainErrors = map[string]bool{"invalid_grant": true, "interaction_required": true}

// The page's own queries. The receipts list names each item by number only. A
// receipt query takes dates as M/DD/YYYY and the online orders query as
// YYYY-MM-DD, and the online orders query insists on a warehouse number, a
// routing value rather than a filter: 847 is costco.com itself.
const costcoReceiptsQuery = `
query receiptsWithCounts($startDate: String!, $endDate: String!,$documentType:String!,$documentSubType:String!) {
    receiptsWithCounts(startDate: $startDate, endDate: $endDate,documentType:$documentType,documentSubType:$documentSubType) {
    inWarehouse
    gasStation
    carWash
    gasAndCarWash
    receipts{
    warehouseName receiptType  documentType transactionDateTime transactionBarcode warehouseName transactionType total
    totalItemCount
    itemArray {
      itemNumber
    }
    tenderArray {
      tenderTypeCode
      tenderDescription
      amountTender
    }
    couponArray {
      upcnumberCoupon
    }
  }
}
  }`

const costcoReceiptDetailQuery = `
query receiptsWithCounts($barcode: String!,$documentType:String!) {
  receiptsWithCounts(barcode: $barcode,documentType:$documentType) {
    receipts{
      warehouseName
      warehouseShortName
      warehouseNumber
      receiptType
      documentType
      transactionDateTime
      transactionDate
      transactionNumber
      transactionType
      transactionBarcode
      registerNumber
      total
      subTotal
      taxes
      instantSavings
      totalItemCount
      membershipNumber
      itemArray {
        itemNumber
        itemDescription01
        itemDescription02
        itemIdentifier
        itemDepartmentNumber
        unit
        amount
        taxFlag
        itemUnitPriceAmount
        fuelUnitQuantity
        fuelGradeDescription
        fuelUomDescription
      }
      tenderArray {
        tenderTypeCode
        tenderSubTypeCode
        tenderDescription
        tenderTypeName
        amountTender
        displayAccountNumber
        walletType
      }
    }
  }
}`

const costcoOnlineOrdersQuery = `
query getOnlineOrders($startDate:String!, $endDate:String!, $pageNumber:Int , $pageSize:Int, $warehouseNumber:String! ){
  getOnlineOrders(startDate:$startDate, endDate:$endDate, pageNumber : $pageNumber, pageSize :  $pageSize, warehouseNumber :  $warehouseNumber) {
    pageNumber
    pageSize
    totalNumberOfRecords
    bcOrders {
      orderHeaderId
      orderPlacedDate : orderedDate
      orderNumber : sourceOrderNumber
      orderTotal
      warehouseNumber
      status
      orderLineItems {
        orderLineItemId
        itemId
        itemNumber
        lineNumber
        itemDescription
        deliveryDate
        status
        orderStatus
        parentOrderLineItemId
        shippingType
        configuredItemData
        shipment {
          shipmentId
          shippedDate
          deliveredDate
          status
          pickUpCompletedDate
        }
      }
    }
  }
}`

// costcoDate is "4/01/2026": the endpoint answers a backend 404 to an ISO date.
func costcoDate(iso string) string {
	parts := strings.Split(iso, "-")
	if len(parts) != 3 {
		return iso
	}
	return fmt.Sprintf("%d/%s/%s", mustInt(parts[1]), parts[2], parts[0])
}

func mustInt(value string) int {
	out := 0
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return out
		}
		out = out*10 + int(digit-'0')
	}
	return out
}

// --- Where the sign-in stands ----------------------------------------------------

const (
	costcoEmailField    = `#signInName, input[name="signInName"], #logonId, input[name="logonId"], input[type="email"]`
	costcoPasswordField = `#password, input[name="password"], input[type="password"]`
	costcoCodeField     = `#verificationCode, input[name="verificationCode"], ` +
		`input[autocomplete="one-time-code"], #otpCode, input[name="otp"], #email_ver_input`
	costcoSubmitButton = `#next, #continue, #signInSubmit, button[type="submit"], input[type="submit"]`
	// A card or row on the Orders & Purchases page. Whatever it is called, it
	// is the positive finding that says a session is signed in.
	costcoPurchaseCard = `[class*="order-card"], [class*="orderCard"], [class*="receipt-card"], ` +
		`[class*="receiptCard"], [data-testid*="receipt"], [data-testid*="order-"], ` +
		`.order-summary, .receipt-summary`
	costcoGreeting = `[data-testid*="account-menu"], #header-account, .header-account, .account-name, ` +
		`.myaccount-greeting, [class*="signed-in"]`
	costcoError           = `#signInNameError, .error-message, [role="alert"], .alert-danger`
	costcoBlockedSelector = `#px-captcha, iframe[src*="recaptcha"], iframe[src*="hcaptcha"], [id*="challenge"]`
	costcoCodePrompt      = `.verification-text, .intro p, form p, h1`
)

// The sign-in form's submission. Costco's site can answer it with a 403 the
// page never shows: the spinner stops and the form sits there. That refusal
// comes before the credentials are read, and is reported as such.
var costcoSignInPost = regexp.MustCompile(`(?i)/SelfAsserted`)

const costcoRefusedPrompt = "Costco's site refused the sign-in from this server " +
	"before it reached the password. Try again later."

var (
	costcoAuthPage     = regexp.MustCompile(`(?i)signin\.costco\.com|/logon|/login|b2c_1a|/ap/signin`)
	costcoBlockedWords = regexp.MustCompile(`(?i)access denied|pardon our interruption|reference\s*#\s*\d|unusual (?:traffic|activity)|are you a (?:human|robot)`)
	costcoBadLogin     = regexp.MustCompile(`(?i)incorrect|does not match|not recognized|invalid|no account|locked`)
	costcoSignedOut    = regexp.MustCompile(`(?i)\bsign out\b|\blog out\b`)
	costcoSignInWords  = regexp.MustCompile(`(?i)sign in`)
)

// costcoModule keeps the refusal it heard per page: one module value serves
// every sign-in open at once.
//
// Costco's sign-in page acts only on input typed into it: fields set whole
// and a button pressed the instant they are, as Fill and a bare click do,
// send nothing. So every field is typed key by key at the browser's pace,
// and each press follows a short pause after the last key.
type costcoModule struct {
	refused *sync.Map
	pace    browser.Pace
}

func Costco() Module { return &costcoModule{refused: &sync.Map{}, pace: browser.TypingPace} }

func (m *costcoModule) ID() domain.MerchantID { return domain.MerchantCostco }

func (m *costcoModule) SignInURL() string { return costcoOrdersURL }

func (m *costcoModule) LandingURL() string { return costcoOrdersURL }

func (m *costcoModule) SignInPrompt() string { return "Sign in to Costco in the browser below" }

func (m *costcoModule) SessionKinds() []string { return []string{costcoSessionKind} }

func (m *costcoModule) Attach(page browser.Page) {
	page.OnResponse(func(r browser.Response) {
		if r.Status() == http.StatusForbidden && costcoSignInPost.MatchString(r.URL()) {
			m.refused.Store(page, true)
		}
	})
}

func (m *costcoModule) Forget(page browser.Page) { m.refused.Delete(page) }

var costcoGroups = map[string]string{
	"email":    costcoEmailField,
	"password": costcoPasswordField,
	"code":     costcoCodeField,
	"error":    costcoError,
	"blocked":  costcoBlockedSelector,
	"card":     costcoPurchaseCard,
	"greeting": costcoGreeting,
	"prompt":   costcoCodePrompt,
}

// Classify reports Costco's refusal as a blocking check: there is nothing to
// type. A sign-in page with nothing on it to go on yet is read again as it
// settles, and only then called unrecognised.
func (m *costcoModule) Classify(page browser.Page) (State, error) {
	for look := 0; ; look++ {
		read, err := readCostco(page)
		if err != nil {
			return State{}, err
		}
		if where, known := m.classify(page, read); known {
			return where, nil
		}
		// One more look after a settle even when nothing says the page is
		// busy: B2C draws its step after the document has loaded.
		if look == costcoLooks || (look > 0 && !read.unsettled()) {
			return read.unrecognised(), nil
		}
		if look == 0 {
			page.Settle()
		}
		page.Sleep(costcoRecheck)
	}
}

// classify is one look; false is a page it has no rule for.
func (m *costcoModule) classify(page browser.Page, read costcoReading) (State, bool) {
	if _, heard := m.refused.Load(page); heard {
		return State{
			State: StateFailed, Prompt: costcoRefusedPrompt,
			Error: "Costco answered the sign-in form with 403",
		}, true
	}
	// A bare "Access Denied" page carrying a reference number is the same
	// check, shown to a person with no form to answer it.
	if costcoBlockedWords.MatchString(read.Text) || read.shows("blocked") {
		return State{
			State: StateCaptcha, Blocking: true,
			Prompt: "Costco showed a check before its sign-in that did not clear within the wait. " +
				"Nothing typed here answers it; try again later.",
		}, true
	}
	// Whatever B2C says about the last press is the answer to it, in its own
	// words: a wrong password, a locked account, too many attempts. Beside a
	// code box it is about the code, which the person can type again.
	if message := read.siteError(); message != "" {
		if read.shows("code") {
			return State{State: StateOTP, Prompt: message, Method: billers.CodeChannel(read.codePrompt())}, true
		}
		return State{State: StateFailed, Prompt: message, Error: message}, true
	}
	if read.shows("error") {
		message := read.textOf("error")
		if costcoBadLogin.MatchString(message) {
			return State{State: StateFailed, Prompt: message, Error: message}, true
		}
	}
	if read.shows("code") {
		prompt := read.codePrompt()
		if prompt == "" {
			return State{State: StateOTP, Prompt: "Enter the verification code Costco just sent you"}, true
		}
		return State{State: StateOTP, Prompt: prompt, Method: billers.CodeChannel(prompt)}, true
	}
	// The Azure B2C page carries both fields at once, so the password decides.
	if read.shows("password") {
		return State{State: StatePassword}, true
	}
	// A step asking which way to send a code, or to press for one, comes
	// before the email field: B2C's email verification shows the address in
	// an email field beside its send button.
	if costcoAuthPage.MatchString(read.URL) {
		if choices := read.factorChoices(); len(choices) > 0 {
			return State{
				State: StateFactor, Choices: choices,
				Prompt: "Costco asked how to send a verification code",
			}, true
		}
	}
	if read.shows("email") {
		return State{State: StateEmail}, true
	}
	// Signed in is a positive finding, never a default: the header greets
	// somebody by name or offers to sign them out, or the purchases are on the
	// page in front of us.
	if !costcoAuthPage.MatchString(read.URL) {
		greeting := read.textOf("greeting")
		signedIn := (greeting != "" && !costcoSignInWords.MatchString(greeting)) ||
			costcoSignedOut.MatchString(read.Text) || read.shows("card")
		if signedIn {
			return State{State: StateSignedIn}, true
		}
	}
	return State{}, false
}

func (m *costcoModule) AccountHint(page browser.Page) (string, error) {
	read, err := readPage(page, map[string]string{"greeting": costcoGreeting})
	if err != nil {
		return "", err
	}
	line := costcoHello.ReplaceAllString(read.textOf("greeting"), "")
	if line == "" || costcoSignInWords.MatchString(line) {
		return "", nil
	}
	// "Alex's Account" and "Alex Smith" are both the member's first name.
	first := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == ',' })
	if len(first) == 0 {
		return "", nil
	}
	return strings.TrimSuffix(strings.TrimSuffix(first[0], "'s"), "’s"), nil
}

var costcoHello = regexp.MustCompile(`(?i)^(?:hi|hello|welcome)[,!]?\s*`)

func (m *costcoModule) FillEmail(page browser.Page, email string) (agent.Step, error) {
	if err := page.TypeInto(costcoEmailField, email); err != nil {
		return agent.Step{}, err
	}
	page.Sleep(m.pace.Pause())
	return agent.Submit(page, costcoSubmitButton)
}

// FillPassword types the email too, since B2C shows both fields on one page;
// TypeInto leaves a field that already holds the address alone.
func (m *costcoModule) FillPassword(page browser.Page, password, email string) (agent.Step, error) {
	if email != "" {
		if _, err := page.TypeIntoVisible(costcoEmailField, email); err != nil {
			return agent.Step{}, err
		}
	}
	if err := page.TypeInto(costcoPasswordField, password); err != nil {
		return agent.Step{}, err
	}
	if _, err := page.CheckIfUnchecked(`input[name="rememberMe"], #rememberMe`); err != nil {
		return agent.Step{}, err
	}
	page.Sleep(m.pace.Pause())
	return agent.Submit(page, costcoSubmitButton)
}

func (m *costcoModule) CaptchaImage(page browser.Page) (string, error) {
	return imageOf(page, `#captcha img, img[src*="captcha"]`)
}

func (m *costcoModule) Answer(page browser.Page, where State, code, _ string) (agent.Step, error) {
	switch where.State {
	case StateOTP:
		if err := page.TypeInto(costcoCodeField, code); err != nil {
			return agent.Step{}, err
		}
		if _, err := page.CheckIfUnchecked(`#rememberDevice, input[name="rememberDevice"]`); err != nil {
			return agent.Step{}, err
		}
		page.Sleep(m.pace.Pause())
		before := agent.Signature(page)
		verified, err := page.ClickVisible(costcoVerifyButton)
		if err != nil {
			return agent.Step{}, err
		}
		if verified {
			return agent.Changed(page, before, agent.Step{Acted: true, Pressed: agent.PressedButton}), nil
		}
		return agent.Submit(page, costcoSubmitButton)
	case StateCaptcha:
		// A blocking check has nothing to type into: the page is given a moment
		// and the next read says where things stand.
		page.Settle()
		return agent.Step{Acted: true, Changed: true}, nil
	}
	return agent.Step{}, nil
}

// --- Reading Costco's JSON into Agentifi's shape ---------------------------------
//
// Everything from here to the pull is pure: it turns the JSON the site answers
// with into a merchantimport.CostcoFile and nothing else. It is what the tests exercise.

// receipt has two names for several fields because the list query and the
// detail query do not agree about them.
type receipt struct {
	WarehouseName      string `json:"warehouseName"`
	WarehouseShortName string `json:"warehouseShortName"`
	WarehouseNumber    loose  `json:"warehouseNumber"`
	ReceiptType        string `json:"receiptType"`
	DocumentType       string `json:"documentType"`
	DocumentSubType    string `json:"documentSubType"`
	TransactionType    string `json:"transactionType"`

	TransactionDate     string `json:"transactionDate"`
	TransactionDateTime string `json:"transactionDateTime"`
	ReceiptDate         string `json:"receiptDate"`

	TransactionBarcode string `json:"transactionBarcode"`
	TransactionNumber  loose  `json:"transactionNumber"`
	SequenceNumber     loose  `json:"sequenceNumber"`
	RegisterNumber     loose  `json:"registerNumber"`

	Total          loose `json:"total"`
	SubTotal       loose `json:"subTotal"`
	Taxes          loose `json:"taxes"`
	Tax            loose `json:"tax"`
	TotalItemCount loose `json:"totalItemCount"`

	ItemArray   []receiptLine `json:"itemArray"`
	Items       []receiptLine `json:"items"`
	TenderArray []tender      `json:"tenderArray"`
	Tenders     []tender      `json:"tenders"`
}

func (r receipt) lines() []receiptLine {
	if len(r.ItemArray) > 0 {
		return r.ItemArray
	}
	return r.Items
}

func (r receipt) tenderLines() []tender {
	if len(r.TenderArray) > 0 {
		return r.TenderArray
	}
	return r.Tenders
}

type receiptLine struct {
	ItemNumber        loose  `json:"itemNumber"`
	ItemIdentifier    loose  `json:"itemIdentifier"`
	ItemDescription01 string `json:"itemDescription01"`
	ItemDescription02 string `json:"itemDescription02"`
	Unit              loose  `json:"unit"`
	Quantity          loose  `json:"quantity"`
	Amount            loose  `json:"amount"`
	ItemAmount        loose  `json:"itemAmount"`
	ExtendedAmount    loose  `json:"extendedAmount"`

	FuelGradeDescription string `json:"fuelGradeDescription"`
	FuelUnitQuantity     loose  `json:"fuelUnitQuantity"`
	FuelUomDescription   string `json:"fuelUomDescription"`
}

type tender struct {
	TenderTypeCode       string `json:"tenderTypeCode"`
	TenderDescription    string `json:"tenderDescription"`
	TenderTypeName       string `json:"tenderTypeName"`
	TenderType           string `json:"tenderType"`
	AmountTender         loose  `json:"amountTender"`
	Amount               loose  `json:"amount"`
	DisplayAccountNumber string `json:"displayAccountNumber"`
}

type onlineOrder struct {
	OrderNumber      string `json:"orderNumber"`
	SalesOrderNumber string `json:"salesOrderNumber"`
	OrderHeaderID    loose  `json:"orderHeaderId"`

	OrderPlacedDate     string `json:"orderPlacedDate"`
	OrderDate           string `json:"orderDate"`
	OrderedDate         string `json:"orderedDate"`
	OrderPlacedDateTime string `json:"orderPlacedDateTime"`

	OrderTotal   loose  `json:"orderTotal"`
	Total        loose  `json:"total"`
	GrandTotal   loose  `json:"grandTotal"`
	CurrencyCode string `json:"currencyCode"`
	Status       string `json:"status"`
	OrderStatus  string `json:"orderStatus"`

	OrderTaxTotal loose `json:"orderTaxTotal"`
	TaxTotal      loose `json:"taxTotal"`
	Tax           loose `json:"tax"`

	OrderLineItems []orderLine `json:"orderLineItems"`
	LineItems      []orderLine `json:"lineItems"`
	Items          []orderLine `json:"items"`

	PaymentInfo []payment `json:"paymentInfo"`
	Payments    []payment `json:"payments"`
	TenderArray []payment `json:"tenderArray"`
}

func (o onlineOrder) lines() []orderLine {
	switch {
	case len(o.OrderLineItems) > 0:
		return o.OrderLineItems
	case len(o.LineItems) > 0:
		return o.LineItems
	default:
		return o.Items
	}
}

func (o onlineOrder) paymentLines() []payment {
	switch {
	case len(o.PaymentInfo) > 0:
		return o.PaymentInfo
	case len(o.Payments) > 0:
		return o.Payments
	default:
		return o.TenderArray
	}
}

type orderLine struct {
	ItemNumber  loose  `json:"itemNumber"`
	ItemID      loose  `json:"itemId"`
	PartNumber  loose  `json:"partNumber"`
	Description string `json:"description"`

	ItemDescription string `json:"itemDescription"`
	ItemName        string `json:"itemName"`
	ProductName     string `json:"productName"`

	Quantity        loose `json:"quantity"`
	OrderedQuantity loose `json:"orderedQuantity"`
	Unit            loose `json:"unit"`

	UnitPrice     loose `json:"unitPrice"`
	Price         loose `json:"price"`
	LineTotal     loose `json:"lineTotal"`
	ExtendedPrice loose `json:"extendedPrice"`
	Amount        loose `json:"amount"`
}

type payment struct {
	Amount        loose `json:"amount"`
	AmountTender  loose `json:"amountTender"`
	PaymentAmount loose `json:"paymentAmount"`

	DisplayAccountNumber string `json:"displayAccountNumber"`
	CardNumber           string `json:"cardNumber"`
	MaskedCardNumber     string `json:"maskedCardNumber"`

	CardType          string `json:"cardType"`
	TenderDescription string `json:"tenderDescription"`
	PaymentType       string `json:"paymentType"`

	PaymentDate string `json:"paymentDate"`
}

var (
	fuelWords    = regexp.MustCompile(`(?i)gas|fuel|gasoline|pump`)
	returnWords  = regexp.MustCompile(`(?i)return|refund|credit`)
	shopCardWord = regexp.MustCompile(`(?i)shop card|cash card|gift`)
	cashWord     = regexp.MustCompile(`(?i)^cash\b|\bcash$`)
	checkWord    = regexp.MustCompile(`(?i)check|cheque`)
	costcoNamed  = regexp.MustCompile(`(?i)costco`)
	allDigits    = regexp.MustCompile(`^\d+$`)
)

func receiptKind(r receipt) string {
	words := strings.Join([]string{r.ReceiptType, r.DocumentType, r.DocumentSubType, r.TransactionType}, " ")
	if fuelWords.MatchString(words) {
		return "fuel"
	}
	return "warehouse"
}

func isReturn(r receipt) bool {
	words := strings.Join([]string{r.ReceiptType, r.DocumentType, r.TransactionType}, " ")
	if returnWords.MatchString(words) {
		return true
	}
	total, ok := r.Total.Money()
	return ok && total.IsNegative()
}

func warehouseLabel(r receipt) string {
	name := clean(r.WarehouseName)
	if name == "" {
		name = clean(r.WarehouseShortName)
	}
	number := r.WarehouseNumber.String()
	if allDigits.MatchString(number) {
		for len(number) < 4 {
			number = "0" + number
		}
	}
	named := "Costco"
	if name != "" {
		named = name
		if !costcoNamed.MatchString(name) {
			named = "Costco " + name
		}
	}
	if number == "" {
		return named
	}
	return named + " #" + number
}

func receiptID(r receipt) string {
	if barcode := clean(r.TransactionBarcode); barcode != "" {
		return barcode
	}
	day := strings.ReplaceAll(dayOf(r.TransactionDate, r.TransactionDateTime), "-", "")
	warehouse := r.WarehouseNumber.String()
	if warehouse == "" {
		warehouse = "0000"
	}
	number := cmp.Or(r.TransactionNumber.String(), r.SequenceNumber.String(), r.RegisterNumber.String())
	if number == "" {
		number = "0"
	}
	return fmt.Sprintf("%s-%s-%s", warehouse, day, number)
}

func dayOf(values ...string) string {
	for _, value := range values {
		if day := parseDateText(value); day != "" {
			return day
		}
	}
	return ""
}

// receiptItem keeps an instant-savings line's negative total: folding it into
// the items around it is merchantimport's job.
func receiptItem(line receiptLine, negate bool) merchantimport.CostcoItem {
	amount, known := firstMoney(line.Amount, line.ItemAmount, line.ExtendedAmount)
	quantity := 1
	if count, ok := firstCount(line.Unit, line.Quantity); ok && count > 0 {
		quantity = count
	}
	if negate {
		amount = amount.Neg()
	}
	title := strings.Join(textutil.Distinct([]string{clean(line.ItemDescription01), clean(line.ItemDescription02)}), " ")
	if title == "" {
		title = fuelTitle(line)
	}
	if title == "" {
		title = clean("Item " + line.ItemNumber.String())
	}
	item := merchantimport.CostcoItem{
		SKU:      cmp.Or(line.ItemNumber.String(), line.ItemIdentifier.String()),
		Title:    title,
		Quantity: quantity,
	}
	if known {
		item.Price = share(amount, quantity).String()
		item.Total = amount.String()
	}
	return item
}

func fuelTitle(line receiptLine) string {
	grade := clean(line.FuelGradeDescription)
	if grade == "" {
		return ""
	}
	gallons, ok := line.FuelUnitQuantity.Money()
	if !ok || !gallons.IsPositive() {
		return grade
	}
	unit := clean(line.FuelUomDescription)
	if unit == "" {
		unit = "GAL"
	}
	return fmt.Sprintf("%s %s %s", grade, line.FuelUnitQuantity.String(), unit)
}

func firstMoney(values ...loose) (domain.Money, bool) {
	for _, value := range values {
		if amount, ok := value.Money(); ok {
			return amount, true
		}
	}
	return domain.Zero, false
}

func firstCount(values ...loose) (int, bool) {
	for _, value := range values {
		if count, ok := value.Int(); ok {
			return count, true
		}
	}
	return 0, false
}

func needsDetail(r receipt) bool {
	lines := r.lines()
	if len(lines) == 0 {
		count, ok := r.TotalItemCount.Int()
		return clean(r.TransactionBarcode) != "" && ok && count > 0
	}
	for _, line := range lines {
		if clean(line.ItemDescription01) == "" && clean(line.ItemDescription02) == "" &&
			clean(line.FuelGradeDescription) == "" {
			return true
		}
	}
	return false
}

func detailDocumentType(r receipt) string {
	if receiptKind(r) == "fuel" {
		return "fuel"
	}
	return "warehouse"
}

func tenderInstrument(t tender) string {
	description := cmp.Or(clean(t.TenderDescription), clean(t.TenderTypeName), clean(t.TenderType))
	// A Costco Shop Card (the "Cash Card") is money the bank never saw — say
	// so before the word "cash" in its name sends it to the branch below.
	if shopCardWord.MatchString(description) {
		return "Costco Shop Card"
	}
	if cashWord.MatchString(description) {
		return "Cash"
	}
	if checkWord.MatchString(description) {
		return "Check"
	}
	four := domain.LastFour(clean(t.DisplayAccountNumber))
	if four == "" {
		return description
	}
	if description == "" {
		description = "Card"
	}
	return description + " ••••" + four
}

func receiptToOrder(r receipt) (merchantimport.CostcoOrder, []merchantimport.CostcoCharge) {
	negate := isReturn(r)
	date := dayOf(r.TransactionDate, r.TransactionDateTime, r.ReceiptDate)
	id := receiptID(r)
	order := merchantimport.CostcoOrder{
		OrderID:  id,
		Kind:     receiptKind(r),
		Date:     date,
		Currency: "USD",
		Location: warehouseLabel(r),
		Tax:      moneyString(cmp.Or(r.Taxes.String(), r.Tax.String())),
		// A Costco Shop Card is a tender, not a discount on the receipt: the
		// order itself never has a gift card share.
		GiftCard: "0.00",
		Items:    []merchantimport.CostcoItem{},
	}
	if negate {
		order.Status = "Return"
	}
	if total, ok := r.Total.Money(); ok {
		if negate {
			total = total.Abs().Neg()
		}
		order.Total = total.String()
	}
	for _, line := range r.lines() {
		order.Items = append(order.Items, receiptItem(line, negate))
	}
	var charges []merchantimport.CostcoCharge
	for _, paid := range r.tenderLines() {
		amount, ok := firstMoney(paid.AmountTender, paid.Amount)
		if !ok || amount.IsZero() {
			continue
		}
		// The bank sees a purchase as money out and a return as money in.
		signed := amount.Abs()
		if !negate {
			signed = signed.Neg()
		}
		charges = append(charges, merchantimport.CostcoCharge{
			OrderID: id, Date: date, Amount: signed.String(), Instrument: tenderInstrument(paid),
		})
	}
	return order, charges
}

func onlineOrderToOrder(raw onlineOrder) (merchantimport.CostcoOrder, []merchantimport.CostcoCharge) {
	number := cmp.Or(clean(raw.OrderNumber), clean(raw.SalesOrderNumber), raw.OrderHeaderID.String())
	date := dayOf(raw.OrderPlacedDate, raw.OrderDate, raw.OrderedDate, raw.OrderPlacedDateTime)
	currency := clean(raw.CurrencyCode)
	if currency == "" {
		currency = "USD"
	}
	order := merchantimport.CostcoOrder{
		OrderID:  number,
		Kind:     "online",
		Date:     date,
		Total:    moneyString(cmp.Or(raw.OrderTotal.String(), raw.Total.String(), raw.GrandTotal.String())),
		Currency: currency,
		Status:   cmp.Or(clean(raw.Status), clean(raw.OrderStatus)),
		Tax:      moneyString(cmp.Or(raw.OrderTaxTotal.String(), raw.TaxTotal.String(), raw.Tax.String())),
		// Unknown rather than zero: an online order's payment lines are not
		// always on the page that lists it.
		GiftCard: "",
		Items:    []merchantimport.CostcoItem{},
	}
	if number != "" {
		order.URL = costcoOrderURL(number)
	}
	for _, line := range raw.lines() {
		quantity := 1
		if count, ok := firstCount(line.Quantity, line.OrderedQuantity, line.Unit); ok && count > 0 {
			quantity = count
		}
		item := merchantimport.CostcoItem{
			SKU:      cmp.Or(line.ItemNumber.String(), line.ItemID.String(), line.PartNumber.String()),
			Title:    cmp.Or(clean(line.ItemDescription), clean(line.ItemName), clean(line.ProductName), clean(line.Description)),
			Quantity: quantity,
		}
		price, hasPrice := firstMoney(line.UnitPrice, line.Price)
		lineTotal, hasTotal := firstMoney(line.LineTotal, line.ExtendedPrice, line.Amount)
		switch {
		case hasPrice:
			item.Price = price.String()
		case hasTotal:
			item.Price = share(lineTotal, quantity).String()
		}
		switch {
		case hasTotal:
			item.Total = lineTotal.String()
		case hasPrice:
			item.Total = price.MulInt(quantity).String()
		}
		order.Items = append(order.Items, item)
	}
	var charges []merchantimport.CostcoCharge
	for _, paid := range raw.paymentLines() {
		amount, ok := firstMoney(paid.Amount, paid.AmountTender, paid.PaymentAmount)
		if !ok || amount.IsZero() {
			continue
		}
		description := cmp.Or(clean(paid.CardType), clean(paid.TenderDescription), clean(paid.PaymentType))
		if description == "" {
			description = "Card"
		}
		instrument := description
		four := domain.LastFour(
			cmp.Or(clean(paid.DisplayAccountNumber), clean(paid.CardNumber), clean(paid.MaskedCardNumber)))
		switch {
		case shopCardWord.MatchString(description):
			instrument = "Costco Shop Card"
		case four != "":
			instrument = description + " ••••" + four
		}
		charges = append(charges, merchantimport.CostcoCharge{
			OrderID:    number,
			Date:       cmp.Or(parseDateText(paid.PaymentDate), date),
			Amount:     amount.Abs().Neg().String(),
			Instrument: instrument,
		})
	}
	return order, charges
}

// --- Finding those objects in whatever the site answered -------------------------

// isReceiptShape and isOnlineOrderShape read the raw JSON rather than a typed
// value: what is being asked is which of the two a nested object even is.
func isReceiptShape(value map[string]any) bool {
	if text(value["transactionDate"]) == "" && text(value["transactionDateTime"]) == "" {
		return false
	}
	if _, listed := value["itemArray"].([]any); listed {
		return true
	}
	if _, listed := value["tenderArray"].([]any); listed {
		return true
	}
	return text(value["transactionBarcode"]) != ""
}

func isOnlineOrderShape(value map[string]any) bool {
	if isReceiptShape(value) {
		return false
	}
	if text(value["orderNumber"]) == "" && text(value["salesOrderNumber"]) == "" &&
		text(value["orderHeaderId"]) == "" {
		return false
	}
	for _, key := range []string{"orderLineItems", "lineItems", "items"} {
		if _, listed := value[key].([]any); listed {
			return true
		}
	}
	return false
}

// text takes a JSON number as a string: the site writes ids as numbers as
// often as not.
func text(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return clean(typed)
	case json.Number:
		return typed.String()
	case bool:
		return ""
	default:
		return ""
	}
}

type harvested struct {
	receipts     []receipt
	onlineOrders []onlineOrder
	// shapes is what could not be placed, by its keys, so a run that found
	// nothing can say what it was looking at.
	shapes []string
}

func (h *harvested) count() int { return len(h.receipts) + len(h.onlineOrders) }

// harvest picks the two shapes out of any JSON, wherever they are nested. Keys
// are walked sorted so a note naming the shapes it saw is stable.
func harvest(value any, into *harvested, depth int) {
	if into == nil || depth > 10 {
		return
	}
	switch typed := value.(type) {
	case []any:
		for _, entry := range typed {
			harvest(entry, into, depth+1)
		}
	case map[string]any:
		if isReceiptShape(typed) {
			var one receipt
			if decodeInto(typed, &one) == nil {
				into.receipts = append(into.receipts, one)
			}
			return
		}
		if isOnlineOrderShape(typed) {
			var one onlineOrder
			if decodeInto(typed, &one) == nil {
				into.onlineOrders = append(into.onlineOrders, one)
			}
			return
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if depth <= 4 && len(into.shapes) < 12 && anyMatches(keys, shapeWords) {
			signature := strings.Join(keys[:min(len(keys), 12)], ",")
			if !contains(into.shapes, signature) {
				into.shapes = append(into.shapes, signature)
			}
		}
		for _, key := range keys {
			harvest(typed[key], into, depth+1)
		}
	}
}

var shapeWords = regexp.MustCompile(`(?i)receipt|order|transaction|item`)

func anyMatches(values []string, pattern *regexp.Regexp) bool {
	for _, value := range values {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func detailOf(body any) (receipt, bool) {
	shaped, ok := body.(map[string]any)
	if !ok {
		return receipt{}, false
	}
	data, ok := shaped["data"].(map[string]any)
	if !ok {
		return receipt{}, false
	}
	listing := data["receipts"]
	if counted, ok := data["receiptsWithCounts"].(map[string]any); ok {
		listing = counted["receipts"]
	}
	one := listing
	if listed, ok := listing.([]any); ok {
		if len(listed) == 0 {
			return receipt{}, false
		}
		one = listed[0]
	}
	shapedOne, ok := one.(map[string]any)
	if !ok || !isReceiptShape(shapedOne) {
		return receipt{}, false
	}
	var out receipt
	if decodeInto(shapedOne, &out) != nil {
		return receipt{}, false
	}
	return out, true
}

// buildExport drops purchases older than the window, and keeps once one seen
// by both the page's own call and ours.
func buildExport(found harvested, accountHint, sinceISO, extractedAt string) merchantimport.CostcoFile {
	file := merchantimport.CostcoFile{
		Source: "agentifi-costco-extract", ExtractedAt: extractedAt, AccountHint: accountHint,
		Orders: []merchantimport.CostcoOrder{}, Charges: []merchantimport.CostcoCharge{},
	}
	kept := map[string]bool{}
	keep := func(order merchantimport.CostcoOrder, charges []merchantimport.CostcoCharge) {
		if order.OrderID == "" || order.Date == "" {
			return
		}
		if sinceISO != "" && order.Date < sinceISO {
			return
		}
		if kept[order.OrderID] {
			return
		}
		kept[order.OrderID] = true
		file.Orders = append(file.Orders, order)
		file.Charges = append(file.Charges, charges...)
	}
	for _, one := range found.receipts {
		keep(receiptToOrder(one))
	}
	for _, one := range found.onlineOrders {
		keep(onlineOrderToOrder(one))
	}
	return file
}
