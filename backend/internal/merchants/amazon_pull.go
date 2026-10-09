package merchants

import (
	"cmp"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The Amazon pull (docs/connectors/merchants.md). Each page is read inside its own
// failure, so a page that has changed shape costs its own reader and not the
// pull.

func (m amazonModule) Fetch(call Call) (Result, error) {
	page := call.Page
	if page == nil {
		return Result{}, fmt.Errorf("the Amazon pull was given no page")
	}
	sinceISO := call.Since()
	filters := filtersFor(call.SinceDays, domain.DateOf(call.At()))

	call.Report("Opening Amazon's order history")
	if err := page.Goto(fmt.Sprintf("%s/your-orders/orders?timeFilter=%s", amazonHome, filters[0])); err != nil {
		return Result{}, err
	}
	page.Settle()
	where, err := m.Classify(page)
	if err != nil {
		return Result{}, err
	}
	if where.State != StateSignedIn {
		return Result{
			NeedsSignIn: true,
			Reason:      cmp.Or(where.Prompt, "Amazon asked to sign in again ("+where.State+")"),
			Image:       agent.Screenshot(page),
		}, nil
	}
	hint, _ := m.AccountHint(page)

	orders, err := m.readOrderPages(call, filters, sinceISO)
	if err != nil {
		return Result{}, err
	}

	charges, invoices, refunded := m.readInvoices(call, orders)
	charges = append(charges, m.readTransactions(call, sinceISO, charges)...)
	balance := m.readBalance(call, sinceISO)

	file := merchantimport.AmazonFile{
		Source: "agentifi-amazon-extract", ExtractedAt: call.At().UTC().Format(time.RFC3339),
		AccountHint: hint, Orders: []merchantimport.AmazonOrder{}, Charges: []merchantimport.AmazonCharge{},
		Refunds: []merchantimport.AmazonRefund{}, RefundTotals: refunded, GiftCard: balance,
	}
	for _, order := range orders {
		out := merchantimport.AmazonOrder{
			OrderID: order.OrderID, Date: order.Date, Total: order.Total,
			Currency: order.Currency, Status: order.Status, URL: order.URL,
			GiftCard: order.GiftCard, Tax: order.Tax, Shipping: order.Shipping,
			Items: []merchantimport.AmazonItem{},
		}
		for _, item := range order.Items {
			out.Items = append(out.Items, merchantimport.AmazonItem{
				Title: item.Title, SKU: item.ASIN, Quantity: item.Quantity,
				Price: item.Price, URL: item.URL,
			})
		}
		file.Orders = append(file.Orders, out)
	}
	for _, charge := range charges {
		file.Charges = append(file.Charges, merchantimport.AmazonCharge{
			OrderID: charge.OrderID, Date: charge.Date, Amount: charge.Amount,
			Instrument: charge.Instrument,
		})
	}
	return Result{
		AccountHint: hint, Parsed: readFile(merchantimport.FromAmazon(file)),
		Orders: len(file.Orders), Charges: len(file.Charges), Invoices: invoices,
	}, nil
}

func (m amazonModule) readOrderPages(call Call, filters []string, sinceISO string) ([]*amazonOrder, error) {
	page := call.Page
	var orders []*amazonOrder
	seen := map[string]*amazonOrder{}

	for index, filter := range filters {
		older := false
		for start := 0; start < 400 && !older; start += 10 {
			call.Report("Reading order history: %s, page %d (%s found so far)",
				filterLabel(filter), start/10+1, countOf(len(orders), "order"))
			// The first page of the first filter is already open; every other
			// page, including the first of each later filter, is loaded.
			if start > 0 || index > 0 {
				if err := page.Goto(fmt.Sprintf("%s/your-orders/orders?timeFilter=%s&startIndex=%d",
					amazonHome, filter, start)); err != nil {
					return nil, err
				}
				page.Settle()
			}
			found, err := readOrders(page)
			if err != nil {
				return nil, err
			}
			if len(found) == 0 && start == 0 {
				// One more look before giving up on a whole year: Amazon's
				// first answer to a filter is sometimes a page still loading.
				_ = page.Goto(page.URL())
				page.Settle()
				if found, err = readOrders(page); err != nil {
					return nil, err
				}
			}
			if len(found) == 0 {
				if start == 0 {
					where, _ := m.Classify(page)
					title, _ := page.Title()
					call.Notes.Addf(
						"no order cards found for %s (page %q, state %s, %s); the page layout may have changed",
						filter, textutil.Clip(title, 80), where.State, textutil.Clip(page.URL(), 120))
				}
				break
			}
			for _, one := range found {
				date := parseDateText(one.DateText)
				if date == "" {
					call.Notes.Addf("order %s: unreadable date %q", one.OrderID, one.DateText)
					continue
				}
				if date < sinceISO {
					older = true
					continue
				}
				if _, had := seen[one.OrderID]; had {
					continue
				}
				kept := one
				kept.Date = date
				orders = append(orders, &kept)
				seen[one.OrderID] = &kept
			}
		}
	}
	return orders, nil
}

// amazonInvoicePagesMax is the invoice pages one pull opens.
const amazonInvoicePagesMax = 150

// readInvoices opens each new order's printable invoice for what the order
// cards do not show: each item's price, the tax, what a gift card paid and
// what has been refunded. While it is open the page is printed, for the
// order's invoice document; an order already read in full is opened again only
// for a print not yet on file. Then the orders due a refund check are opened
// for their refund total alone, inside the same limit of pages.
func (m amazonModule) readInvoices(
	call Call, orders []*amazonOrder,
) ([]amazonCharge, []Invoice, []merchantimport.AmazonRefundTotal) {
	visit := &invoiceVisit{module: m, call: call}
	visit.printer, visit.prints = call.Page.(browser.PDFPrinter)
	var charges []amazonCharge
	var invoices []Invoice
	refunded := []merchantimport.AmazonRefundTotal{}
	today := domain.DateOf(call.At()).String()
	read := map[string]bool{}
	// An invoice that names no refund total has refunded nothing.
	keep := func(orderID string, page invoice) {
		read[orderID] = true
		refunded = append(refunded, merchantimport.AmazonRefundTotal{
			OrderID: orderID, Amount: cmp.Or(strings.TrimPrefix(page.Refund, "-"), "0.00"), Date: today,
		})
	}
	waiting := 0
	for _, order := range orders {
		if (!call.SkipDetails[order.OrderID] || (visit.prints && !call.Invoiced[order.OrderID])) &&
			!cancelled.MatchString(order.Status) {
			waiting++
		}
	}
	waiting = min(waiting, amazonInvoicePagesMax)
	for _, order := range orders {
		details := !call.SkipDetails[order.OrderID]
		printing := visit.prints && !call.Invoiced[order.OrderID]
		if (!details && !printing) || cancelled.MatchString(order.Status) {
			continue
		}
		if visit.opened >= amazonInvoicePagesMax {
			call.Notes.Addf("more than %d new orders; the rest of the invoices wait for the next pull",
				amazonInvoicePagesMax)
			break
		}
		call.Report("Reading invoices: %d of %d", visit.opened+1, waiting)
		page, ok := visit.open(order.OrderID)
		if visit.stopped != "" {
			call.Notes.Addf("the invoice pages asked to sign in; item prices were skipped")
			break
		}
		if !ok {
			continue
		}
		keep(order.OrderID, page)
		if printing {
			if printed, ok := visit.print(order.OrderID); ok {
				invoices = append(invoices, printed)
			}
		}
		if !details {
			continue
		}
		if len(page.Items) == 0 {
			visit.unreadable++
			if visit.unreadable <= 2 {
				call.Notes.Addf("invoice for %s had a total but no item lines; item prices were "+
					"skipped. Seen: %q", order.OrderID, page.Glimpse)
			}
		}
		mergeInvoice(order, page)
		for _, one := range page.Charges {
			date := parseDateText(one.DateText)
			if date == "" {
				continue
			}
			charges = append(charges, amazonCharge{
				OrderID: order.OrderID, Date: date, Amount: one.Amount, Instrument: one.Instrument,
			})
		}
	}

	var checks []string
	for _, orderID := range call.RefundChecks {
		if !read[orderID] {
			checks = append(checks, orderID)
		}
	}
	for at, orderID := range checks {
		if visit.stopped != "" {
			break
		}
		if visit.opened >= amazonInvoicePagesMax {
			call.Notes.Addf("%d orders wait for the next pull to be checked for refunds", len(checks)-at)
			break
		}
		call.Report("Checking orders for refunds: %d of %d", at+1, len(checks))
		page, ok := visit.open(orderID)
		if visit.stopped != "" {
			call.Notes.Addf("the invoice pages asked to sign in; refunds were not checked")
			break
		}
		if ok {
			keep(orderID, page)
		}
	}
	return charges, invoices, refunded
}

// invoiceVisit is one pull's walk through the invoice pages.
type invoiceVisit struct {
	module  amazonModule
	call    Call
	printer browser.PDFPrinter
	// prints goes false at the first failed print: a browser that cannot print
	// one invoice prints none of them.
	prints             bool
	opened, unreadable int
	// stopped is what an invoice page that asked to sign in, or to prove a
	// person is there, said; no more are opened.
	stopped string
}

// open loads one order's invoice page and reads it; false is a page that
// gave nothing to read.
func (v *invoiceVisit) open(orderID string) (invoice, bool) {
	page, notes := v.call.Page, v.call.Notes
	v.opened++
	if err := page.Goto(fmt.Sprintf("%s/gp/css/summary/print.html?ie=UTF8&orderID=%s",
		amazonHome, orderID)); err != nil {
		v.unreadable++
		if v.unreadable <= 3 {
			notes.Addf("invoice for %s: %v", orderID, err)
		}
		return invoice{}, false
	}
	page.Settle()
	if where, err := v.module.Classify(page); err == nil && where.State != StateSignedIn &&
		(where.Blocking || amazonAuthPath.MatchString(page.URL())) {
		v.stopped = cmp.Or(where.Prompt, "Amazon asked to sign in again")
		return invoice{}, false
	}
	read, err := readInvoice(page)
	if err != nil {
		v.unreadable++
		if v.unreadable <= 3 {
			notes.Addf("invoice for %s: %v", orderID, err)
		}
		return invoice{}, false
	}
	if len(read.Items) == 0 && read.GrandTotal == "" {
		v.unreadable++
		if v.unreadable <= 3 {
			notes.Addf("invoice for %s had no items or total (page %q); the layout may have "+
				"changed. Seen: %q", orderID, textutil.Clip(read.Title, 60), read.Glimpse)
		}
		return invoice{}, false
	}
	return read, true
}

// print prints the open invoice page as the order's invoice document.
func (v *invoiceVisit) print(orderID string) (Invoice, bool) {
	printed, err := v.printer.PDF()
	if err != nil {
		v.call.Notes.Addf("the invoices could not be printed, so none were filed: %v", err)
		v.prints = false
		return Invoice{}, false
	}
	return Invoice{OrderID: orderID, Filename: "amazon-invoice-" + orderID + ".pdf", PDF: printed}, true
}

var cancelled = regexp.MustCompile(`(?i)cancel`)

// readTransactions reads the charges Amazon put on each card, which are the
// figures the bank sees. A charge the invoice already named is replaced, not
// added: the payments page carries the day the card was charged.
func (m amazonModule) readTransactions(call Call, sinceISO string, known []amazonCharge) []amazonCharge {
	page := call.Page
	call.Report("Reading card charges")
	if err := page.Goto(amazonHome + "/cpe/yourpayments/transactions"); err != nil {
		call.Notes.Addf("the transactions page: %v", err)
		return nil
	}
	page.Settle()
	if where, err := m.Classify(page); err != nil || where.State != StateSignedIn {
		call.Notes.Addf("the transactions page asked to sign in; charges were skipped")
		return nil
	}
	var added []amazonCharge
	for pageNo := 1; pageNo <= 60; pageNo++ {
		call.Report("Reading card charges: page %d", pageNo)
		read, err := readCharges(page)
		if err != nil {
			call.Notes.Addf("the transactions page: %v", err)
			return added
		}
		if len(read.Charges) == 0 && pageNo == 1 {
			call.Notes.Addf("no charges found on the transactions page; the page layout may have changed")
		}
		older := false
		for _, one := range read.Charges {
			date := parseDateText(one.DateText)
			if date == "" {
				continue
			}
			if date < sinceISO {
				older = true
				continue
			}
			if twin := twinOf(known, one.OrderID, one.Amount, date); twin != nil {
				twin.Date = date
				if twin.Instrument == "" {
					twin.Instrument = one.Instrument
				}
				continue
			}
			// And against what this loop has already taken: the page pages by
			// offset, so a charge posting between two fetches shifts the
			// window and the last row of one page is the first of the next.
			if twinOf(added, one.OrderID, one.Amount, date) != nil {
				continue
			}
			added = append(added, amazonCharge{
				OrderID: one.OrderID, Date: date, Amount: one.Amount, Instrument: one.Instrument,
			})
		}
		if older || !read.HasNext {
			break
		}
		if _, err := page.ClickVisible(`input[name*="NextPageNavigationEvent"]`); err != nil {
			break
		}
		page.Settle()
	}
	return added
}

func twinOf(charges []amazonCharge, order, amount, date string) *amazonCharge {
	for i := range charges {
		if charges[i].OrderID != order || charges[i].Amount != amount {
			continue
		}
		if withinDays(charges[i].Date, date, 3) {
			return &charges[i]
		}
	}
	return nil
}

func withinDays(left, right string, days int) bool {
	a, errA := domain.ParseDate(left)
	b, errB := domain.ParseDate(right)
	if errA != nil || errB != nil {
		return false
	}
	gap := domain.DaysBetween(a, b)
	return -days <= gap && gap <= days
}

func (m amazonModule) readBalance(call Call, sinceISO string) *merchantimport.AmazonGiftCard {
	page := call.Page
	call.Report("Reading the gift card balance")
	if err := page.Goto(amazonHome + "/gc/balance"); err != nil {
		call.Notes.Addf("gift card balance: %v", err)
		return nil
	}
	page.Settle()
	if where, err := m.Classify(page); err != nil || where.State != StateSignedIn {
		call.Notes.Addf("the gift card balance page asked to sign in; the balance was skipped")
		return nil
	}
	read, err := readGiftCard(page)
	if err != nil {
		call.Notes.Addf("gift card balance: %v", err)
		return nil
	}
	activity := giftCardLines(read.Rows)
	if read.Balance == "" && len(activity) == 0 {
		call.Notes.Addf("no gift card balance found on the balance page; the layout may have changed. "+
			"Seen: %q", read.Glimpse)
		return nil
	}
	kept := &merchantimport.AmazonGiftCard{Balance: read.Balance, Activity: []merchantimport.AmazonGiftCardActivity{}}
	for _, line := range activity {
		if line.Date >= sinceISO {
			kept.Activity = append(kept.Activity, line)
		}
	}
	return kept
}

// giftCardLines: money applied to an order leaves the balance, and the page
// may write that with a minus or only in words.
func giftCardLines(rows []giftCardRow) []merchantimport.AmazonGiftCardActivity {
	var out []merchantimport.AmazonGiftCardActivity
	for _, row := range rows {
		dateText := giftCardDate.FindString(row.Line)
		date := parseDateText(dateText)
		if date == "" {
			continue
		}
		description := clean(strings.NewReplacer(
			dateText, "", "$"+row.Amount, "", "-", "", "−", "", "+", "",
		).Replace(row.Line))
		negative := row.Sign == "-" || row.Sign == "−" ||
			(spentWords.MatchString(description) && !receivedWords.MatchString(description))
		amount := row.Amount
		if negative {
			amount = "-" + amount
		}
		out = append(out, merchantimport.AmazonGiftCardActivity{
			Date: date, Description: description, Amount: amount, OrderID: row.OrderID,
		})
	}
	return out
}

var (
	giftCardDate = regexp.MustCompile(
		`(?i)\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.? \d{1,2},? \d{4}\b|\b\d{1,2}/\d{1,2}/\d{2,4}\b`)
	spentWords    = regexp.MustCompile(`(?i)applied|used|redeemed on|spent|order`)
	receivedWords = regexp.MustCompile(`(?i)refund|reload|added|received|credit`)
)

type giftCardRow struct {
	Line    string `json:"line"`
	Sign    string `json:"sign"`
	Amount  string `json:"amount"`
	OrderID string `json:"order_id"`
}

type giftCardReading struct {
	Balance string        `json:"balance"`
	Rows    []giftCardRow `json:"rows"`
	Glimpse string        `json:"glimpse"`
}

// The gift card balance page has been seen from the outside only. The balance
// is the first money after "balance" in the page's text, and a line of
// activity is any row or list item carrying a date and an amount. A page that
// yields neither reports a glimpse of what it did say.
const amazonReadGiftCard = `({ orderRe, amountRe }) => {` + agent.CleanJS + `
  const orderNumber = new RegExp(orderRe);
  const amount = new RegExp(amountRe);
  const text = clean(document.body.innerText);
  const balanceMatch =
    text.match(/(?:gift card|gift-card|available)?\s*balance[^$]{0,40}\$\s*([\d,]+\.\d{2})/i) ||
    text.match(/\$\s*([\d,]+\.\d{2})[^.]{0,30}\bbalance/i);
  const rows = [];
  const nodes = document.querySelectorAll(
    'table tr, [class*="activity"] li, [class*="activity"] [class*="row"], [class*="transaction"] li, [class*="transaction"] [class*="row"], [data-testid*="activity"], [class*="gc-"] [class*="row"]'
  );
  const seen = new Set();
  for (const el of nodes) {
    const line = clean(el.innerText);
    if (!line || seen.has(line) || line.length > 400) continue;
    const money = line.match(amount);
    if (!money) continue;
    const hasDate = /\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.? \d{1,2},? \d{4}\b|\b\d{1,2}\/\d{1,2}\/\d{2,4}\b/i.test(line);
    if (!hasDate) continue;
    seen.add(line);
    const order = line.match(orderNumber);
    rows.push({ line, sign: money[1], amount: money[2].replace(/,/g, ''), order_id: order ? order[1] : '' });
  }
  return { balance: balanceMatch ? balanceMatch[1].replace(/,/g, '') : '', rows, glimpse: text.slice(0, 240) };
}`

func readGiftCard(page browser.Page) (giftCardReading, error) {
	var out giftCardReading
	if err := browser.EvaluateInto(page, amazonReadGiftCard, patterns, &out); err != nil {
		return giftCardReading{}, err
	}
	return out, nil
}

// filterLabel is an order filter in words: "2024", "the last 3 months".
func filterLabel(filter string) string {
	switch {
	case filter == "last30":
		return "the last 30 days"
	case filter == "months-3":
		return "the last 3 months"
	case strings.HasPrefix(filter, "year-"):
		return strings.TrimPrefix(filter, "year-")
	}
	return filter
}

func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
