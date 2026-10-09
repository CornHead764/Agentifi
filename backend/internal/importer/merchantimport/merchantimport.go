// Package merchantimport reads the files a merchant's purchase history
// arrives in, and turns each into the same Parsed value whatever the merchant
// and whatever the file.
//
// Amazon has three files (amazon.go): the order-history CSV its privacy page
// sends on request, the JSON an order-history browser extension exports, and
// the JSON shape Agentifi's daily pull builds. Costco has one
// (costco.go): the JSON the daily pull builds, holding online orders and
// warehouse receipts alike.
//
// The shape is sniffed from the content, never the file name, and a file that
// is not one of the merchant's is refused rather than read as an empty
// history.
package merchantimport

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The three shapes.
const (
	FormatAmazonCSV     = "amazon_csv"
	FormatExtensionJSON = "extension_json"
	FormatAgentifiJSON  = "agentifi_json"
)

// Order is one order, however it arrived.
type Order struct {
	Number    string
	OrderedOn domain.Date
	Total     domain.Money
	Currency  string
	Status    string
	URL       string
	// Kind is one of domain.Purchase*, online when the file did not say;
	// Location is where a warehouse or gas purchase was made.
	Kind     string
	Location string
	Items    []Item
	// GiftCard is what a gift card balance paid of the total, Tax and
	// Shipping what the invoice listed; each is present only when the file
	// carried the invoice, which of the three shapes only Agentifi's does.
	GiftCard    domain.Money
	HasGiftCard bool
	Tax         domain.Money
	HasTax      bool
	Shipping    domain.Money
	HasShipping bool
}

// Item is one line of an order.
type Item struct {
	SKU      string
	Title    string
	Quantity int
	// UnitPrice and TotalOwed are present when the file carried them; the
	// extension export has a price and no total, the CSV has both.
	UnitPrice    domain.Money
	HasUnitPrice bool
	TotalOwed    domain.Money
	HasTotalOwed bool
	ShippedOn    domain.Date
	Condition    string
	URL          string
}

// Charge is one debit or credit Amazon put on a card for an order, signed as
// the bank sees it.
type Charge struct {
	OrderNumber string
	ChargedOn   domain.Date
	Amount      domain.Money
	Instrument  string
}

// Refund is one return the merchant's records show: what an order, or one
// line of it, gave back and where the money went.
//
// Distinct from a Charge with a positive amount: a charge names the order and
// a refund names the item, and only the refund says whether a gift card
// balance took it.
type Refund struct {
	OrderNumber string
	// SKU and Title are the line that came back, when the merchant's record
	// named one; empty for a refund of the whole order.
	SKU      string
	Title    string
	Quantity int
	// RefundedOn is the day the merchant issued it.
	RefundedOn domain.Date
	// Amount is positive: money coming back.
	Amount domain.Money
	// Instrument is where it went in the merchant's words; ToGiftCard says
	// those words name a balance the bank never sees.
	Instrument string
	ToGiftCard bool
	Status     string
}

// RefundTotal is what an order's invoice said was refunded on the day it was
// read, zero for an invoice that showed no refund. It names neither the day of
// the refund nor where the money went, so it is no Refund; it is what ties a
// refund nothing else names an order for to the order.
type RefundTotal struct {
	OrderNumber string
	Amount      domain.Money
	ReadOn      domain.Date
}

// GiftCard is the gift card balance as the pull read it: the figure, and the
// activity behind it — reloads, refunds to the balance, and orders paid from
// it — signed as a bank would see them.
type GiftCard struct {
	Balance    domain.Money
	HasBalance bool
	Activity   []GiftCardActivity
}

// GiftCardActivity is one line of the balance's history.
type GiftCardActivity struct {
	On          domain.Date
	Description string
	Amount      domain.Money
	// OrderNumber is the order the line names, when it names one.
	OrderNumber string
}

// Parsed is a file, read.
type Parsed struct {
	Format string
	Orders []Order
	// Charges is empty for every shape but Agentifi's own.
	Charges []Charge
	// Refunds is what came back, when the pull read the merchant's returns
	// pages. Empty also when the returns page would not read, so an empty
	// list never clears what is on file.
	Refunds []Refund
	// RefundTotals is each invoice the pull read, for what it said was
	// refunded.
	RefundTotals []RefundTotal
	// GiftCard is set when Agentifi's own shape carried the balance page.
	GiftCard *GiftCard
	// AccountHint is whatever the file said about whose account it is — the
	// signed-in name Agentifi's pull saw — for the person choosing which of
	// the household's Amazon accounts to file it under.
	AccountHint string
	// Warnings are lines skipped and why. A file with warnings still imports.
	Warnings []string
}

// ErrNothingRead is a file, or a pull, with nothing in it to import.
var ErrNothingRead = errors.New("merchantimport: nothing to import")

// Parse reads a file for one merchant, in any of the shapes that merchant
// has.
func Parse(merchant domain.MerchantID, raw []byte) (Parsed, error) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return Parsed{}, fmt.Errorf("the file is empty")
	}
	switch merchant {
	case domain.MerchantCostco:
		return parseCostco(trimmed)
	default:
		return parseAmazon(trimmed)
	}
}

// --- Shared ------------------------------------------------------------------

func (o Order) Shipments() []domain.Money {
	byDay := map[domain.Date]domain.Money{}
	var days []domain.Date
	for _, item := range o.Items {
		if item.ShippedOn.IsZero() || !item.HasTotalOwed {
			continue
		}
		if _, seen := byDay[item.ShippedOn]; !seen {
			days = append(days, item.ShippedOn)
		}
		byDay[item.ShippedOn] = byDay[item.ShippedOn].Add(item.TotalOwed)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	out := make([]domain.Money, 0, len(days))
	for _, day := range days {
		out = append(out, byDay[day])
	}
	return out
}
