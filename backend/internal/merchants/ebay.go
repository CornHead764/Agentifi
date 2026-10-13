package merchants

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The eBay module; the connector is described in docs/connectors/merchants.md.
//
// eBay is read through its API with the household's own developer
// application, never through its site: www.ebay.com refuses an automated
// browser. The account is connected by the account holder's consent on eBay's
// own page (service.Merchants.ConnectEbay), which leaves a provider.EbaySession
// as the account's session; there is no sign-in form for this module to fill.

type ebayModule struct {
	app provider.EbayApp
}

// Ebay is the eBay module reading through app, which may be unset: an install
// with no keys cannot connect eBay, and a pull says so.
func Ebay(app provider.EbayApp) Module { return &ebayModule{app: app} }

func (m *ebayModule) ID() domain.MerchantID { return domain.MerchantEbay }

func (m *ebayModule) SessionKinds() []string { return []string{provider.EbaySessionKind} }

func (m *ebayModule) Attach(page browser.Page) {}

// --- No sign-in form ---------------------------------------------------------------
//
// merchants.Module carries the sign-in loop's methods; eBay answers every one
// with "not here", and the service sends an eBay sign-in to its consent page
// before the loop is ever reached.

const ebayNoForm = "eBay is connected through eBay's own consent page, not a sign-in"

func (m *ebayModule) SignInURL() string    { return "https://www.ebay.com/" }
func (m *ebayModule) LandingURL() string   { return "https://www.ebay.com/" }
func (m *ebayModule) SignInPrompt() string { return ebayNoForm }

func (m *ebayModule) Classify(page browser.Page) (State, error) {
	return State{State: StateFailed, Prompt: ebayNoForm, Error: ebayNoForm}, nil
}

func (m *ebayModule) FillEmail(page browser.Page, email string) (agent.Step, error) {
	return agent.Step{}, nil
}

func (m *ebayModule) FillPassword(page browser.Page, password, email string) (agent.Step, error) {
	return agent.Step{}, nil
}

func (m *ebayModule) CaptchaImage(page browser.Page) (string, error) { return "", nil }

func (m *ebayModule) Answer(page browser.Page, where State, code, password string) (agent.Step, error) {
	return agent.Step{}, nil
}

func (m *ebayModule) ChooseFactor(page browser.Page, prefer string) (agent.Factor, error) {
	return agent.Factor{}, nil
}

func (m *ebayModule) AccountHint(page browser.Page) (string, error) { return "", nil }

// --- The pull ----------------------------------------------------------------------

// What one pull lays out at most; what is left waits for the next.
const (
	ebayListingsMax = 60
	// ebayPicturesMax is the photos one listing's document keeps.
	ebayPicturesMax = 6
)

// ebayCheckoutGap is how far apart two orders' payments may be and still be
// one checkout: eBay pays a cart's seller orders one after another, seconds
// apart, and charges the card once for all of them.
const ebayCheckoutGap = 2 * time.Minute

func (m *ebayModule) Fetch(call Call) (Result, error) {
	if !m.app.Set() {
		return Result{}, errors.New("this server has no eBay application keys (EBAY_APP_ID, EBAY_CERT_ID, " +
			"EBAY_RUNAME); see docs/configuration.md")
	}
	if call.HTTP == nil {
		return Result{}, errors.New("the eBay pull was given no way to make a call")
	}
	var session provider.EbaySession
	if err := json.Unmarshal(call.Session, &session); err != nil || session.Kind != provider.EbaySessionKind {
		return Result{NeedsSignIn: true, Reason: "eBay is not connected; connect it again"}, nil
	}
	if session.Sandbox != m.app.Sandbox {
		return Result{NeedsSignIn: true, Reason: "eBay was connected under the other environment " +
			"(sandbox or production) than this server now uses; connect it again"}, nil
	}

	ctx := call.context()
	session, refreshed, err := m.app.Fresh(ctx, call.HTTP, session, call.At())
	if errors.Is(err, provider.ErrEbayConsentEnded) {
		return Result{NeedsSignIn: true, Reason: err.Error()}, nil
	}
	if err != nil {
		return Result{}, err
	}
	var kept json.RawMessage
	if refreshed {
		if kept, err = json.Marshal(session); err != nil {
			return Result{}, err
		}
	}

	to := call.At()
	// A minute short of eBay's ninety days, so a slow clock on either side is
	// not a refused range.
	from := to.Add(-min(time.Duration(call.SinceDays)*24*time.Hour, provider.EbayLookback-time.Minute))
	call.Report("Reading eBay orders since %s", isoDay(from))
	orders, err := m.app.Orders(ctx, call.HTTP, session, from, to)
	if errors.Is(err, provider.ErrEbayConsentEnded) {
		return Result{NeedsSignIn: true, Reason: err.Error()}, nil
	}
	if err != nil {
		return Result{}, err
	}

	checkouts := groupEbayCheckouts(orders)
	file := merchantimport.EbayFile{
		Source: merchantimport.EbaySource, ExtractedAt: call.At().UTC().Format(time.RFC3339),
		Checkouts: []merchantimport.EbayCheckout{},
	}
	payments := 0
	for _, checkout := range checkouts {
		row := checkout.file()
		payments += len(row.Payments)
		file.Checkouts = append(file.Checkouts, row)
	}

	var invoices []Invoice
	if len(checkouts) > 0 {
		invoices = m.documents(call, session, checkouts)
	}
	if len(file.Checkouts) == 0 {
		return Result{StorageState: kept}, nil
	}
	return Result{
		Parsed: readFile(merchantimport.FromEbay(file)), StorageState: kept,
		Orders: len(file.Checkouts), Charges: payments, Invoices: invoices,
	}, nil
}

// ebayCheckout is the seller orders one payment covered.
type ebayCheckout struct {
	orders []provider.EbayOrder
}

// groupEbayCheckouts puts orders paid by one method within a moment of each
// other into one checkout, newest first. An order not paid for is its own.
func groupEbayCheckouts(orders []provider.EbayOrder) []*ebayCheckout {
	sorted := slices.Clone(orders)
	sort.SliceStable(sorted, func(i, j int) bool { return ebayWhen(sorted[i]).Before(ebayWhen(sorted[j])) })
	var out []*ebayCheckout
	for _, order := range sorted {
		if n := len(out); n > 0 {
			last := out[n-1].orders[len(out[n-1].orders)-1]
			if ebayPaid(order) && ebayPaid(last) && order.PaymentMethod == last.PaymentMethod &&
				order.Paid.Sub(last.Paid) <= ebayCheckoutGap {
				out[n-1].orders = append(out[n-1].orders, order)
				continue
			}
		}
		out = append(out, &ebayCheckout{orders: []provider.EbayOrder{order}})
	}
	slices.Reverse(out)
	return out
}

func ebayPaid(order provider.EbayOrder) bool {
	return !order.Paid.IsZero() && order.AmountPaid.IsPositive()
}

func ebayWhen(order provider.EbayOrder) time.Time {
	if !order.Paid.IsZero() {
		return order.Paid
	}
	return order.Created
}

// number is what the checkout is filed under: its lowest seller order number,
// which eBay's order page opens the whole checkout by.
func (c *ebayCheckout) number() string {
	numbers := make([]string, 0, len(c.orders))
	for _, order := range c.orders {
		numbers = append(numbers, order.OrderID)
	}
	return slices.Min(numbers)
}

// ebayLocalDay is the day a moment fell on where the household is: eBay
// answers in UTC, and a purchase at nine in the evening is that day's.
func ebayLocalDay(at time.Time) string { return isoDay(at.In(time.Local)) }

// ebayInstrument is how eBay names a payment method, in the household's words.
func ebayInstrument(method string) string {
	switch method {
	case "CreditCard":
		return "Card"
	case "PayPal":
		return "PayPal"
	}
	return method
}

// file is the checkout as the import reads it.
func (c *ebayCheckout) file() merchantimport.EbayCheckout {
	number := c.number()
	out := merchantimport.EbayCheckout{
		OrderID: number, Date: ebayLocalDay(c.orders[0].Created), Currency: c.orders[0].Currency,
		URL: ebayOrderURL(number),
	}
	var total, paid, tax, shipping domain.Money
	var statuses []string
	for _, order := range c.orders {
		out.Date = min(out.Date, ebayLocalDay(order.Created))
		out.SellerOrders = append(out.SellerOrders, merchantimport.EbaySellerOrder{
			OrderID: order.OrderID, Total: order.Total.String(),
		})
		total, paid = total.Add(order.Total), paid.Add(order.AmountPaid)
		tax, shipping = tax.Add(order.Tax), shipping.Add(order.Shipping)
		if order.Status != "" && !slices.Contains(statuses, order.Status) {
			statuses = append(statuses, order.Status)
		}
		for _, item := range order.Items {
			out.Items = append(out.Items, merchantimport.EbayItem{
				ItemID: item.ItemID, Title: item.Title, Quantity: item.Quantity,
				Price: item.Price.String(), Total: item.Price.MulInt(item.Quantity).String(),
				URL: "https://www.ebay.com/itm/" + url.PathEscape(item.ItemID),
			})
		}
	}
	out.Total, out.Tax, out.Shipping = total.String(), tax.String(), shipping.String()
	out.Status = strings.Join(statuses, ", ")
	if first := c.orders[0]; ebayPaid(first) {
		out.Payments = []merchantimport.EbayPayment{{
			Date: ebayLocalDay(first.Paid), Amount: paid.String(), Instrument: ebayInstrument(first.PaymentMethod),
		}}
	}
	return out
}

func ebayOrderURL(number string) string {
	return "https://order.ebay.com/ord/show?orderId=" + url.QueryEscape(number)
}

// --- The documents -------------------------------------------------------------------

// documents lays out each new checkout's order and each of its items' listing,
// for a checkout whose documents are not yet on file. A listing eBay will no
// longer answer for, or a picture it will not send, is noted and left out.
func (m *ebayModule) documents(call Call, session provider.EbaySession, checkouts []*ebayCheckout) []Invoice {
	ctx := call.context()
	var out []Invoice
	listings := 0
	for _, checkout := range checkouts {
		number := checkout.number()
		if call.Invoiced[number] {
			continue
		}
		out = append(out, Invoice{
			OrderID: number, Filename: "ebay-order-" + number + ".pdf", HTML: ebayOrderDocument(checkout, call.At()),
		})
		for _, order := range checkout.orders {
			for _, item := range order.Items {
				if listings >= ebayListingsMax {
					call.Notes.Addf("more than %d listings to keep; the rest wait for the next pull", ebayListingsMax)
					return out
				}
				listings++
				call.Report("Keeping the listing of %s", item.Title)
				listing, err := m.app.Item(ctx, call.HTTP, session, item.ItemID)
				if err != nil {
					call.Notes.Addf("the listing of item %s could not be read: %v", item.ItemID, err)
					continue
				}
				pictures := m.pictures(call, listing)
				out = append(out, Invoice{
					OrderID: number, Filename: "ebay-listing-" + item.ItemID + ".pdf",
					HTML: ebayListingDocument(listing, item, number, pictures, call.At()),
				})
			}
		}
	}
	return out
}

// pictures is a listing's photos as data addresses, for a document that may
// reach for nothing when it is printed.
func (m *ebayModule) pictures(call Call, listing provider.EbayListing) []string {
	var out []string
	for _, address := range listing.Pictures {
		if len(out) == ebayPicturesMax {
			break
		}
		picture, kind, err := m.app.Picture(call.context(), call.HTTP, address)
		if err != nil {
			call.Notes.Addf("a photo of item %s was left out: %v", listing.ItemID, err)
			continue
		}
		out = append(out, "data:"+kind+";base64,"+base64.StdEncoding.EncodeToString(picture))
	}
	return out
}

const ebayDocumentStyle = `<style>
body{margin:0;font:12px/1.45 sans-serif;color:#222}
h1{font-size:18px;margin:0 0 4px}
p.note{color:#666;font-size:10px;margin:0 0 14px}
table{border-collapse:collapse;width:100%;margin:8px 0 14px}
th,td{text-align:left;padding:4px 6px;border-bottom:1px solid #ddd;vertical-align:top}
td.n,th.n{text-align:right;white-space:nowrap}
.photos img{max-width:48%;max-height:320px;margin:0 2% 8px 0;break-inside:avoid}
.description{white-space:pre-wrap}
</style>`

// ebayOrderDocument is the checkout laid out from eBay's record of it, which
// says on its face that it is not eBay's own page.
func ebayOrderDocument(c *ebayCheckout, at time.Time) string {
	row := c.file()
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8">` + ebayDocumentStyle + `</head><body>`)
	fmt.Fprintf(&b, `<h1>eBay order %s</h1>`, html.EscapeString(row.OrderID))
	fmt.Fprintf(&b, `<p class="note">Laid out by Agentifi on %s from eBay's record of the order. It is not eBay's own page.</p>`,
		html.EscapeString(isoDay(at)))
	fmt.Fprintf(&b, `<p>Placed %s · %s</p>`, html.EscapeString(row.Date), html.EscapeString(row.Status))
	if len(row.SellerOrders) > 1 {
		b.WriteString(`<p>One checkout of several sellers' orders: `)
		for i, seller := range row.SellerOrders {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, `%s (%s)`, html.EscapeString(seller.OrderID), html.EscapeString(seller.Total))
		}
		b.WriteString(`</p>`)
	}
	b.WriteString(`<table><tr><th>Item</th><th>Number</th><th class="n">Qty</th><th class="n">Each</th><th class="n">Total</th></tr>`)
	for _, item := range row.Items {
		fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td class="n">%d</td><td class="n">%s</td><td class="n">%s</td></tr>`,
			html.EscapeString(item.Title), html.EscapeString(item.ItemID), item.Quantity,
			html.EscapeString(item.Price), html.EscapeString(item.Total))
	}
	fmt.Fprintf(&b, `<tr><td colspan="4">Shipping</td><td class="n">%s</td></tr>`, html.EscapeString(row.Shipping))
	fmt.Fprintf(&b, `<tr><td colspan="4">Tax</td><td class="n">%s</td></tr>`, html.EscapeString(row.Tax))
	fmt.Fprintf(&b, `<tr><th colspan="4">Total (%s)</th><th class="n">%s</th></tr></table>`,
		html.EscapeString(row.Currency), html.EscapeString(row.Total))
	for _, payment := range row.Payments {
		fmt.Fprintf(&b, `<p>Paid %s by %s on %s</p>`, html.EscapeString(payment.Amount),
			html.EscapeString(payment.Instrument), html.EscapeString(payment.Date))
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

// ebayListingDocument is one listing as eBay kept it: what was offered, its
// photos and the seller's description, without the seller.
func ebayListingDocument(
	listing provider.EbayListing, item provider.EbayOrderItem, number string, pictures []string, at time.Time,
) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8">` + ebayDocumentStyle + `</head><body>`)
	fmt.Fprintf(&b, `<h1>%s</h1>`, html.EscapeString(firstNonEmpty(listing.Title, item.Title)))
	fmt.Fprintf(&b, `<p class="note">eBay listing %s, bought in order %s. Laid out by Agentifi on %s from eBay's record of the listing.</p>`,
		html.EscapeString(item.ItemID), html.EscapeString(number), html.EscapeString(isoDay(at)))
	b.WriteString(`<table>`)
	rows := [][2]string{
		{"Price paid", fmt.Sprintf("%s × %d", item.Price.String(), item.Quantity)},
		{"Condition", listing.Condition},
		{"Category", listing.Category},
		{"Listing", listing.URL},
	}
	for _, specific := range listing.Specifics {
		rows = append(rows, [2]string{specific.Name, strings.Join(specific.Values, ", ")})
	}
	for _, row := range rows {
		if row[1] == "" {
			continue
		}
		fmt.Fprintf(&b, `<tr><th>%s</th><td>%s</td></tr>`, html.EscapeString(row[0]), html.EscapeString(row[1]))
	}
	b.WriteString(`</table>`)
	if len(pictures) > 0 {
		b.WriteString(`<div class="photos">`)
		for _, picture := range pictures {
			fmt.Fprintf(&b, `<img alt="" src="%s">`, html.EscapeString(picture))
		}
		b.WriteString(`</div>`)
	}
	if text := plainDescription(listing.Description); text != "" {
		fmt.Fprintf(&b, `<h2>Description</h2><div class="description">%s</div>`, html.EscapeString(text))
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var (
	descriptionHidden = regexp.MustCompile(`(?is)<(script|style|head)\b.*?</(script|style|head)\s*>`)
	descriptionBreak  = regexp.MustCompile(`(?i)<(br|/p|/div|/li|/tr|/h[1-6])\b[^>]*>`)
	descriptionTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	descriptionBlank  = regexp.MustCompile(`\n[ \t]*(\n[ \t]*)+`)
)

// descriptionLimit is how much of a seller's description a document keeps.
const descriptionLimit = 20_000

// plainDescription is a seller's HTML description as its words: the seller's
// markup is laid out by nothing, and its images and scripts reach for
// nothing.
func plainDescription(markup string) string {
	text := descriptionHidden.ReplaceAllString(markup, "")
	text = descriptionBreak.ReplaceAllString(text, "\n")
	text = html.UnescapeString(descriptionTag.ReplaceAllString(text, ""))
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	text = strings.TrimSpace(descriptionBlank.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
	if runes := []rune(text); len(runes) > descriptionLimit {
		text = string(runes[:descriptionLimit]) + "…"
	}
	return text
}
