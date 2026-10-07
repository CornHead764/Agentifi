package merchants

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Costco's purchases API answers with a receipt's lines, not with a document,
// so the receipt a pull files is one Agentifi lays out from those lines and
// the engine prints. It says so on its face: it is not Costco's own paper.

var costcoReceiptTemplate = template.Must(template.New("receipt").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Costco receipt {{.Number}}</title>
<style>
body { font: 11pt/1.4 -apple-system, "Segoe UI", Helvetica, Arial, sans-serif; color: #111; margin: 0; }
h1 { font-size: 16pt; margin: 0 0 4pt; }
.meta { color: #444; margin: 0 0 12pt; }
.note { color: #666; font-size: 9pt; margin: 0 0 14pt; }
table { width: 100%; border-collapse: collapse; margin: 0 0 12pt; }
th, td { text-align: left; padding: 3pt 4pt; border-bottom: 1px solid #ddd; vertical-align: top; }
th { font-size: 9pt; color: #555; text-transform: uppercase; }
.num { text-align: right; white-space: nowrap; }
.totals td { border: none; }
.totals .label { text-align: right; }
.grand td { font-weight: 600; border-top: 1px solid #111; }
</style></head><body>
<h1>Costco {{if .Return}}return{{else}}receipt{{end}}</h1>
<p class="meta">{{.Location}}{{if .Date}} &middot; {{.Date}}{{end}}{{if .Number}} &middot; Receipt {{.Number}}{{end}}</p>
<p class="note">Laid out by Agentifi from the receipt Costco's account holds for this purchase. It is not a copy of Costco's printed receipt.</p>
<table>
<thead><tr><th>Item</th><th>Description</th><th class="num">Qty</th><th class="num">Amount</th></tr></thead>
<tbody>
{{range .Items}}<tr><td>{{.Number}}</td><td>{{.Title}}</td><td class="num">{{.Quantity}}</td><td class="num">{{.Amount}}</td></tr>
{{end}}</tbody>
</table>
<table class="totals">
{{if .Tax}}<tr><td class="label">Tax</td><td class="num">{{.Tax}}</td></tr>{{end}}
<tr class="grand"><td class="label">Total</td><td class="num">{{.Total}}</td></tr>
</table>
{{if .Tenders}}<table>
<thead><tr><th>{{if .Return}}Refunded to{{else}}Paid with{{end}}</th><th class="num">Amount</th></tr></thead>
<tbody>
{{range .Tenders}}<tr><td>{{.Title}}</td><td class="num">{{.Amount}}</td></tr>
{{end}}</tbody>
</table>{{end}}
</body></html>
`))

// costcoReceiptPages lays out each warehouse receipt whose lines were read in
// detail and whose document is not already on file. A receipt whose items are
// by number only, or one read from the page's cards, has nothing worth
// printing.
func costcoReceiptPages(found harvested, sinceISO string, invoiced map[string]bool) []Invoice {
	var pages []Invoice
	seen := map[string]bool{}
	for _, one := range found.receipts {
		if len(pages) >= costcoReceiptDetailsMax {
			break
		}
		barcode := clean(one.TransactionBarcode)
		id := receiptID(one)
		if barcode == "" || invoiced[id] || seen[id] || !describedInFull(one) {
			continue
		}
		order, charges := receiptToOrder(one)
		if order.Date == "" || (sinceISO != "" && order.Date < sinceISO) || order.Total == "" {
			continue
		}
		seen[id] = true
		printed := provider.MerchantReceipt{
			Merchant: domain.MerchantCostco, Location: order.Location, Date: order.Date, Number: id,
			Return: isReturn(one), Tax: order.Tax, Total: order.Total,
		}
		for _, item := range order.Items {
			printed.Items = append(printed.Items, provider.MerchantReceiptLine{
				Number: item.SKU, Title: item.Title, Quantity: item.Quantity, Amount: item.Total,
			})
		}
		for _, charge := range charges {
			printed.Tenders = append(printed.Tenders, provider.MerchantReceiptLine{
				Title: charge.Instrument, Amount: strings.TrimPrefix(charge.Amount, "-"),
			})
		}
		page, err := costcoReceiptPage(printed)
		if err != nil {
			continue
		}
		pages = append(pages, Invoice{OrderID: id, Filename: costcoReceiptFilename(id), HTML: page})
	}
	return pages
}

func costcoReceiptPage(receipt provider.MerchantReceipt) (string, error) {
	var page bytes.Buffer
	if err := costcoReceiptTemplate.Execute(&page, receipt); err != nil {
		return "", err
	}
	return page.String(), nil
}

func costcoReceiptFilename(number string) string { return "costco-receipt-" + number + ".pdf" }

// Receipt lays out a stored Costco purchase as its receipt page, the same
// layout a pull makes, and names the file it prints to.
func Receipt(receipt provider.MerchantReceipt) (filename, page string, err error) {
	if receipt.Merchant != domain.MerchantCostco {
		return "", "", fmt.Errorf("merchants: only a Costco receipt is laid out, not %q", receipt.Merchant)
	}
	if page, err = costcoReceiptPage(receipt); err != nil {
		return "", "", err
	}
	return costcoReceiptFilename(receipt.Number), page, nil
}

func describedInFull(r receipt) bool {
	lines := r.lines()
	return len(lines) > 0 && !needsDetail(r)
}
