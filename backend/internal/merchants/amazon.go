package merchants

import (
	"cmp"
	_ "embed"
	"fmt"
	"regexp"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The Amazon module; the connector is described in docs/connectors/merchants.md.
// Amazon changes its pages, so every selector is a best guess kept here, and a
// pull that finds nothing says so in the notes rather than answering an empty
// history.

const amazonHome = "https://www.amazon.com"

// The three patterns every reader on the page shares, passed into each script
// so the page and this file cannot disagree about what an order number is.
const (
	orderNumberPattern = `\b(\d{3}-\d{7}-\d{7})\b`
	amountPattern      = `([-+−]?)\s*\$\s*([\d,]+\.\d{2})`
	asinPattern        = `/(?:dp|gp/product|gp/aw/d)/([A-Z0-9]{10})`
)

var patterns = map[string]any{
	"orderRe": orderNumberPattern, "amountRe": amountPattern, "asinRe": asinPattern,
}

// --- Where the sign-in stands ----------------------------------------------------

const (
	amazonCheckField  = `#captchacharacters`
	amazonAuthError   = `#auth-error-message-box, .a-alert-error`
	amazonCaptchaImg  = `#auth-captcha-image, img[src*="captcha"]`
	amazonOTPField    = `#auth-mfa-otpcode`
	amazonOTPPrompt   = `#auth-mfa-form .a-row, #auth-mfa-form p, .cvf-widget-form p`
	amazonCodeField   = `input[name="code"], #cvf-input-code, input[name="otpCode"]`
	amazonCodePrompt  = `.cvf-widget-form p, #channelDetails, .a-spacing-small`
	amazonApproval    = `#resend-approval-link, [data-a-target="transaction-approval"]`
	amazonEmailField  = `#ap_email, #ap_email_login, input[name="email"]`
	amazonPassword    = `#ap_password`
	amazonRegister    = `#ap_customer_name, #ap_register_form`
	amazonOrderCards  = `.order-card, .js-order-card, #ordersContainer, .your-orders-content-container`
	amazonGreetingSel = `#nav-link-accountList-nav-line-1, #nav-link-accountList .nav-line-1`
	amazonHeading     = `h1, .a-spacing-small`
)

var amazonGroups = map[string]string{
	"check":      amazonCheckField,
	"error":      amazonAuthError,
	"captcha":    amazonCaptchaImg,
	"otp":        amazonOTPField,
	"otpPrompt":  amazonOTPPrompt,
	"code":       amazonCodeField,
	"codePrompt": amazonCodePrompt,
	"approval":   amazonApproval,
	"email":      amazonEmailField,
	"password":   amazonPassword,
	"register":   amazonRegister,
	"orders":     amazonOrderCards,
	"greeting":   amazonGreetingSel,
	"heading":    amazonHeading,
}

var (
	amazonAuthPath  = regexp.MustCompile(`/ap/`)
	amazonCheckPath = regexp.MustCompile(`validateCaptcha`)
	amazonBadLogin  = regexp.MustCompile(`(?i)incorrect|not match|cannot find|no account`)
	amazonChallenge = regexp.MustCompile(`(?i)/ap/cvf|challenge|verify`)
	amazonApproving = regexp.MustCompile(`(?i)approve the notification|check your (phone|email)`)
	amazonNewToIt   = regexp.MustCompile(`(?i)new to Amazon`)
	amazonClaimPath = regexp.MustCompile(`/ax/claim`)
	amazonHelloWord = regexp.MustCompile(`(?i)^(?:hello|hi),\s*`)
	amazonSignInWrd = regexp.MustCompile(`(?i)sign in`)
)

type amazonModule struct{}

func Amazon() Module { return amazonModule{} }

func (amazonModule) ID() domain.MerchantID { return domain.MerchantAmazon }

func (amazonModule) SignInURL() string { return amazonHome + "/gp/css/order-history" }

func (amazonModule) LandingURL() string { return amazonHome + "/your-orders/orders" }

func (amazonModule) SignInPrompt() string { return "Sign in to Amazon in the browser below" }

func (amazonModule) SessionKinds() []string { return nil }

func (amazonModule) Attach(page browser.Page) {}

// Classify: every one of Amazon's authentication screens lives under /ap/, and
// the form fields settle which screen.
func (amazonModule) Classify(page browser.Page) (State, error) {
	read, err := readPage(page, amazonGroups)
	if err != nil {
		return State{}, err
	}
	authPage := amazonAuthPath.MatchString(read.URL)

	// Amazon's check page is not under /ap/: a bare page with a distorted
	// picture and one field. It is a CAPTCHA to the person all the same.
	if amazonCheckPath.MatchString(read.URL) || read.shows("check") {
		return State{
			State: StateCaptcha, Blocking: true,
			Prompt: "Amazon showed a check page: type the characters in the picture",
		}, nil
	}
	if read.shows("error") {
		message := read.textOf("error")
		if amazonBadLogin.MatchString(message) {
			return State{State: StateFailed, Prompt: message, Error: message}, nil
		}
	}
	if read.shows("captcha") {
		return State{State: StateCaptcha, Prompt: "Type the characters in the picture"}, nil
	}
	if read.shows("otp") {
		prompt := read.textOf("otpPrompt")
		if prompt == "" {
			return State{
				State:  StateOTP,
				Prompt: "Enter the one-time code from your authenticator app or text message",
			}, nil
		}
		return State{State: StateOTP, Prompt: prompt, Method: billers.CodeChannel(prompt)}, nil
	}
	if read.shows("code") && amazonChallenge.MatchString(read.URL+read.textOf("heading")) {
		prompt := read.textOf("codePrompt")
		if prompt == "" {
			prompt = "Enter the verification code Amazon just sent you"
		}
		return State{State: StateOTP, Prompt: prompt}, nil
	}
	if read.shows("approval") {
		return State{
			State:  StateApproval,
			Prompt: "Amazon sent an approval notification to your phone. Approve it, then continue.",
		}, nil
	}
	if authPage && amazonApproving.MatchString(read.Text) {
		return State{
			State:  StateApproval,
			Prompt: "Amazon sent an approval notification. Approve it on your device, then continue.",
		}, nil
	}
	if read.shows("email") {
		return State{State: StateEmail}, nil
	}
	if read.shows("password") {
		return State{State: StatePassword}, nil
	}
	if amazonClaimPath.MatchString(read.URL) || read.shows("register") || amazonNewToIt.MatchString(read.Text) {
		return State{
			State:  StateFailed,
			Prompt: "Amazon has no account for that email or number and offered to create one",
			Error:  "Amazon has no account for that email or number",
		}, nil
	}
	// Signed in is a positive finding, never a default: the account menu is
	// there with a name in it rather than "Hello, sign in", or the orders page
	// is showing orders.
	if !authPage {
		greeting := read.textOf("greeting")
		if (greeting != "" && !amazonSignInWrd.MatchString(greeting)) || read.shows("orders") {
			return State{State: StateSignedIn}, nil
		}
	}
	return State{
		State:  StateFailed,
		Prompt: "Amazon showed a page the agent does not recognise",
		Error:  "unrecognised page at " + read.URL,
	}, nil
}

func (amazonModule) AccountHint(page browser.Page) (string, error) {
	read, err := readPage(page, map[string]string{"greeting": amazonGreetingSel})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(amazonHelloWord.ReplaceAllString(read.textOf("greeting"), "")), nil
}

// --- The sign-in form ------------------------------------------------------------

func (amazonModule) FillEmail(page browser.Page, email string) (agent.Step, error) {
	if err := page.Fill(amazonEmailField, email); err != nil {
		return agent.Step{}, err
	}
	return agent.Submit(page, `#continue, input#continue, #continue input, input[type="submit"]`)
}

func (amazonModule) FillPassword(page browser.Page, password, email string) (agent.Step, error) {
	if err := page.Fill(amazonPassword, password); err != nil {
		return agent.Step{}, err
	}
	// "Keep me signed in", where Amazon offers it: a session that is not kept
	// is a code by text every night.
	if _, err := page.CheckIfUnchecked(`input[name="rememberMe"]`); err != nil {
		return agent.Step{}, err
	}
	return agent.Submit(page, `#signInSubmit, input#signInSubmit, input[type="submit"]`)
}

func (amazonModule) CaptchaImage(page browser.Page) (string, error) {
	return imageOf(page, `#auth-captcha-image, img[src*="captcha"], form img`)
}

func (m amazonModule) Answer(page browser.Page, where State, code, password string) (agent.Step, error) {
	switch {
	case where.State == StateOTP:
		if err := page.Fill(amazonOTPField+", "+amazonCodeField, code); err != nil {
			return agent.Step{}, err
		}
		if _, err := page.CheckIfUnchecked(`#auth-mfa-remember-device`); err != nil {
			return agent.Step{}, err
		}
		return agent.Submit(page,
			`#auth-signin-button, #cvf-submit-otp-button, input[type="submit"]`)
	case where.State == StateCaptcha && where.Blocking:
		if err := page.Fill(amazonCheckField, code); err != nil {
			return agent.Step{}, err
		}
		step, err := agent.Submit(page, `button[type="submit"], input[type="submit"]`)
		if err != nil {
			return agent.Step{}, err
		}
		// The check page drops the visitor on the home page; go back to where
		// the sign-in starts.
		if !amazonAuthPath.MatchString(page.URL()) {
			return step, page.Goto(m.SignInURL())
		}
		return step, nil
	case where.State == StateCaptcha:
		if err := page.Fill(`#auth-captcha-guess`, code); err != nil {
			return agent.Step{}, err
		}
		// Amazon empties the password box when it puts the picture puzzle on
		// the sign-in, so a submit that does not put it back posts a blank
		// password and comes back saying the password was wrong.
		if _, err := page.FillVisible(amazonPassword, password); err != nil {
			return agent.Step{}, err
		}
		return agent.Submit(page, `#signInSubmit, input[type="submit"]`)
	case where.State == StateApproval:
		// Nothing to type; the person approved on their phone and the page
		// moves on by itself. Re-reading is the answer.
		page.Settle()
		return agent.Step{Acted: true, Changed: true}, nil
	}
	return agent.Step{}, nil
}

// ChooseFactor takes nothing: Amazon sends its code where the account says
// and draws no menu this module reads as one.
func (amazonModule) ChooseFactor(page browser.Page, prefer string) (agent.Factor, error) {
	return agent.Factor{}, nil
}

// --- The pull ------------------------------------------------------------------

// filtersFor is which of Amazon's order filters cover the window.
func filtersFor(sinceDays int, today domain.Date) []string {
	if sinceDays <= 30 {
		return []string{"last30"}
	}
	if sinceDays <= 90 {
		return []string{"months-3"}
	}
	var filters []string
	for year := today.Year; year >= today.AddDays(-sinceDays).Year; year-- {
		filters = append(filters, fmt.Sprintf("year-%d", year))
	}
	return filters
}

type amazonOrder struct {
	OrderID  string       `json:"order_id"`
	DateText string       `json:"date_text"`
	Total    string       `json:"total"`
	Currency string       `json:"currency"`
	Status   string       `json:"status"`
	URL      string       `json:"url"`
	Items    []amazonItem `json:"items"`
	// What the invoice added, once one was read: the day off the card, and
	// the three figures the card never carries.
	Date     string `json:"-"`
	GiftCard string `json:"-"`
	Tax      string `json:"-"`
	Shipping string `json:"-"`
}

type amazonItem struct {
	Title    string `json:"title"`
	ASIN     string `json:"asin"`
	Quantity int    `json:"quantity"`
	Price    string `json:"price"`
	URL      string `json:"url"`
}

const amazonReadOrders = `({ orderRe, amountRe, asinRe }) => {` + agent.CleanJS + `
  const orderNumber = new RegExp(orderRe);
  const amount = new RegExp(amountRe);
  const asin = new RegExp(asinRe);
  const cards = document.querySelectorAll('.order-card, .js-order-card, .order, [class*="order-card"]');
  const out = [];
  for (const card of cards) {
    const text = clean(card.innerText);
    const number = text.match(orderNumber);
    if (!number) continue;
    const placed = text.match(/ORDER PLACED\s*([A-Za-z]+ \d{1,2}, \d{4})/i);
    const total = text.match(/TOTAL\s*([-+−]?\s*\$\s*[\d,]+\.\d{2})/i);
    const items = [];
    const seen = new Set();
    for (const a of card.querySelectorAll('a[href*="/dp/"], a[href*="/gp/product/"]')) {
      const title = clean(a.innerText);
      const m = a.getAttribute('href').match(asin);
      if (!m || title.length < 3) continue;
      if (seen.has(m[1])) continue;
      seen.add(m[1]);
      let quantity = 1;
      const row = a.closest('.a-fixed-left-grid-inner, .yohtmlc-item, .a-row, li');
      const qty = row && row.innerText.match(/\bQty:?\s*(\d+)/i);
      if (qty) quantity = Number(qty[1]);
      const badge = row && row.querySelector('.item-view-qty');
      if (badge && /^\d+$/.test(badge.innerText.trim())) quantity = Number(badge.innerText.trim());
      let price = '';
      const priceEl = row && row.querySelector('.a-color-price, [class*="price"]');
      if (priceEl) {
        const pm = priceEl.innerText.match(amount);
        if (pm) price = pm[2].replace(/,/g, '');
      }
      items.push({
        title,
        asin: m[1],
        quantity,
        price,
        url: new URL(a.getAttribute('href'), location.origin).toString().split('?')[0],
      });
    }
    const statusEl = card.querySelector(
      '.delivery-box__primary-text, .js-shipment-info-container .a-size-medium, .shipment-top .a-size-medium'
    );
    out.push({
      order_id: number[1],
      date_text: placed ? placed[1] : '',
      total: total ? total[1].replace(/[^\d.,-]/g, '').replace(/,/g, '') : '',
      currency: 'USD',
      status: statusEl ? clean(statusEl.innerText) : '',
      url: location.origin + '/gp/your-account/order-details?orderID=' + number[1],
      items,
    });
  }
  return out;
}`

func readOrders(page browser.Page) ([]amazonOrder, error) {
	var out []amazonOrder
	if err := browser.EvaluateInto(page, amazonReadOrders, patterns, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type amazonCharge struct {
	OrderID    string `json:"order_id"`
	DateText   string `json:"date_text"`
	Amount     string `json:"amount"`
	Instrument string `json:"instrument"`
	Date       string `json:"-"`
}

type amazonCharges struct {
	Charges []amazonCharge `json:"charges"`
	HasNext bool           `json:"hasNext"`
}

//go:embed amazon_charges.js
var amazonChargesOn string

var amazonReadCharges = `({ orderRe, amountRe }) => ({
  charges: ` + strings.TrimSpace(amazonChargesOn) + `(document, new RegExp(orderRe), new RegExp(amountRe)),
  hasNext: Boolean(document.querySelector('input[name*="NextPageNavigationEvent"]')),
})`

func readCharges(page browser.Page) (amazonCharges, error) {
	var out amazonCharges
	if err := browser.EvaluateInto(page, amazonReadCharges, patterns, &out); err != nil {
		return amazonCharges{}, err
	}
	return out, nil
}

// invoice is one order's printable page, read by its labels rather than by
// selectors.
type invoice struct {
	Items      []invoiceItem   `json:"items"`
	Subtotal   string          `json:"subtotal"`
	Shipping   string          `json:"shipping"`
	Tax        string          `json:"tax"`
	GrandTotal string          `json:"grand_total"`
	GiftCard   string          `json:"gift_card"`
	Refund     string          `json:"refund"`
	Charges    []invoiceCharge `json:"charges"`
	Title      string          `json:"title"`
	Glimpse    string          `json:"glimpse"`
}

type invoiceItem struct {
	Quantity int    `json:"quantity"`
	Title    string `json:"title"`
	ASIN     string `json:"asin"`
	Price    string `json:"price"`
}

type invoiceCharge struct {
	Instrument string `json:"instrument"`
	DateText   string `json:"date_text"`
	Amount     string `json:"amount"`
}

const amazonReadInvoice = `({ amountRe, asinRe }) => {` + agent.CleanJS + `
  const amount = new RegExp(amountRe);
  const asin = new RegExp(asinRe);
  const money = (t) => {
    const m = clean(t).match(amount);
    return m ? (m[1] === '-' || m[1] === '−' ? '-' : '') + m[2].replace(/,/g, '') : '';
  };
  const items = [];
  const amountAll = new RegExp(amountRe, 'g');
  const seen = new Set();
  for (const a of document.querySelectorAll('a[href*="/dp/"], a[href*="/gp/product/"]')) {
    const title = clean(a.innerText);
    if (title.length < 3) continue;
    const m = a.getAttribute('href').match(asin);
    const code = m ? m[1] : '';
    if (seen.has(code || title)) continue;
    let box = a.parentElement;
    for (let hops = 0; box && hops < 8; hops += 1) {
      const t = box.innerText || '';
      if (/Subtotal|Grand Total/i.test(t)) { box = null; break; }
      if (amount.test(t) && /Sold by|Supplied by|Qty|Return|Buy it again/i.test(t)) break;
      box = box.parentElement;
    }
    if (!box) continue;
    const text = clean(box.innerText);
    const amounts = [...text.matchAll(amountAll)].map((x) => x[2].replace(/,/g, ''));
    if (amounts.length === 0) continue;
    seen.add(code || title);
    let quantity = 1;
    const qty = text.match(/\bQty:?\s*(\d+)/i) || text.match(/\b(\d+)\s+of:/);
    if (qty) quantity = Number(qty[1]) || 1;
    const badge = box.querySelector('.item-view-qty, [class*="quantity"]');
    if (badge && /^\d+$/.test(clean(badge.innerText))) quantity = Number(clean(badge.innerText));
    items.push({ quantity, title, asin: code, price: amounts[0] });
  }
  const body = clean(document.body.innerText);
  if (items.length === 0) {
    const lineRe = /(\d+)\s+of:\s*(.+?)(?=\s+(?:Sold by|Supplied by|Condition:))[^$]{0,400}?\$\s*([\d,]+\.\d{2})/g;
    let m;
    while ((m = lineRe.exec(body)) !== null) {
      items.push({ quantity: Number(m[1]) || 1, title: m[2].trim(), asin: '', price: m[3].replace(/,/g, '') });
    }
  }
  const figure = (label) => {
    const m = body.match(new RegExp(label + '\\s*:?\\s*([-+−]?\\s*\\$\\s*[\\d,]+\\.\\d{2})', 'i'));
    return m ? money(m[1]) : '';
  };
  const charges = [];
  const cardRe = /([A-Za-z][A-Za-z ]{1,30}?)\s+ending in\s+(\d{4})\s*:\s*([A-Za-z]+ \d{1,2}, \d{4})\s*:\s*([-+−]?\s*\$\s*[\d,]+\.\d{2})/g;
  let c;
  while ((c = cardRe.exec(body)) !== null) {
    const value = money(c[4]);
    charges.push({
      instrument: c[1].trim() + ' ••••' + c[2],
      date_text: c[3],
      amount: value.startsWith('-') ? value : '-' + value,
    });
  }
  return {
    items,
    subtotal: figure('Item\\(s\\) Subtotal'),
    shipping: figure('Shipping (?:&|and) Handling'),
    tax: figure('(?:Estimated tax to be collected|Estimated tax|Sales Tax|Tax Collected)'),
    grand_total: figure('Grand Total'),
    gift_card: figure('Gift Card Amount'),
    refund: figure('Refund Total'),
    charges,
    title: document.title,
    glimpse: (() => {
      const at = body.search(/Items? Ordered|of:/i);
      return at < 0 ? body.slice(0, 700) : body.slice(Math.max(0, at - 40), at + 700);
    })(),
  };
}`

func readInvoice(page browser.Page) (invoice, error) {
	var out invoice
	if err := browser.EvaluateInto(page, amazonReadInvoice, patterns, &out); err != nil {
		return invoice{}, err
	}
	return out, nil
}

// mergeInvoice marries the invoice's items (which carry the price) to the
// order card's (which carry the ASIN): by ASIN where the invoice links the
// item, by the start of the title otherwise.
func mergeInvoice(order *amazonOrder, read invoice) {
	norm := func(title string) string {
		var out strings.Builder
		lastSpace := false
		for _, r := range strings.ToLower(title) {
			switch {
			case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
				out.WriteRune(r)
				lastSpace = false
			case !lastSpace:
				out.WriteRune(' ')
				lastSpace = true
			}
		}
		return textutil.Clip(strings.TrimSpace(out.String()), 40)
	}
	left := make([]int, 0, len(order.Items))
	for i := range order.Items {
		left = append(left, i)
	}
	drop := func(at int) { left = append(left[:at], left[at+1:]...) }

	for _, one := range read.Items {
		found := -1
		if one.ASIN != "" {
			for at, index := range left {
				if order.Items[index].ASIN == one.ASIN {
					found = at
					break
				}
			}
		}
		if found < 0 {
			for at, index := range left {
				card := norm(order.Items[index].Title)
				if card != "" && strings.HasPrefix(norm(one.Title), textutil.Clip(card, 25)) {
					found = at
					break
				}
			}
		}
		if found < 0 {
			for at, index := range left {
				if strings.HasPrefix(norm(order.Items[index].Title), textutil.Clip(norm(one.Title), 25)) {
					found = at
					break
				}
			}
		}
		if found >= 0 {
			item := &order.Items[left[found]]
			item.Price = one.Price
			if one.Quantity > 1 {
				item.Quantity = one.Quantity
			}
			drop(found)
			continue
		}
		order.Items = append(order.Items, amazonItem{
			Title: one.Title, ASIN: one.ASIN, Quantity: one.Quantity, Price: one.Price,
		})
	}

	if read.GrandTotal != "" && (order.Total == "" || order.Total == "0.00") {
		order.Total = strings.ReplaceAll(read.GrandTotal, "-", "")
	}
	// One item: its price is the subtotal by definition, whatever the page
	// showed beside it (a deal price, a per-unit price for a multi-pack), and
	// a subtotal that is a whole multiple of the price is a quantity the page
	// did not spell out.
	if subtotal, ok := domain.ParseMoneyText(read.Subtotal); ok && subtotal.IsPositive() && len(order.Items) == 1 {
		item := &order.Items[0]
		price, hasPrice := domain.ParseMoneyText(item.Price)
		if hasPrice && price.IsPositive() && item.Quantity == 1 && subtotal.GreaterThan(price) {
			if count, ok := wholeMultiple(subtotal, price); ok && count >= 2 {
				item.Quantity = count
			}
		}
		if item.Quantity < 1 {
			item.Quantity = 1
		}
		if !hasPrice || !price.IsPositive() ||
			price.MulInt(item.Quantity).Sub(subtotal).Abs().GreaterThan(penny) {
			item.Price = share(subtotal, item.Quantity).String()
		}
	}
	order.GiftCard = "0.00"
	if read.GiftCard != "" {
		order.GiftCard = strings.ReplaceAll(read.GiftCard, "-", "")
	}
	order.Tax = cmp.Or(read.Tax, "0.00")
	order.Shipping = cmp.Or(read.Shipping, "0.00")
}

// penny is the slack a total is allowed against the sum of its parts: a cent,
// and the rounding either side of it.
var penny = domain.MustFromString("0.011")

func wholeMultiple(total, each domain.Money) (int, bool) {
	if each.IsZero() {
		return 0, false
	}
	ratio, ok := domain.Ratio(total, each)
	if !ok {
		return 0, false
	}
	count := int(ratio.Round(0).IntPart())
	if count < 1 {
		return 0, false
	}
	if each.MulInt(count).Sub(total).Abs().GreaterThan(penny) {
		return 0, false
	}
	return count, true
}
