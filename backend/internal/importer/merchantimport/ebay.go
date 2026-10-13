package merchantimport

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// eBay's one file shape: the JSON the daily pull builds from eBay's API, and
// tools/extractors/extract-ebay.js saves from the purchases page in the
// person's own browser, which reaches further back than the API. The script's
// file has each seller order's total and no payments, so its orders match by
// their totals.
//
// One checkout is one order here. eBay gives each seller in a cart its own
// order number, but the card is charged once for the whole checkout and its
// order page shows every seller's part together, so the checkout is what a
// bank row is matched to. It is filed under its first seller order's number,
// which eBay's order page opens the checkout by.

const ebaySource = "agentifi-ebay-extract"

// EbaySource is the marker an EbayFile carries.
const EbaySource = ebaySource

// EbayFile is Agentifi's eBay export, which the merchant engine builds
// in-process for FromEbay.
type EbayFile struct {
	Source      string         `json:"source"`
	ExtractedAt string         `json:"extracted_at"`
	AccountHint string         `json:"account_hint"`
	Checkouts   []EbayCheckout `json:"checkouts"`
}

type EbayCheckout struct {
	// OrderID is the first seller order's number; SellerOrders is every one.
	OrderID      string            `json:"order_id"`
	SellerOrders []EbaySellerOrder `json:"seller_orders"`
	Date         string            `json:"date"`
	Total        string            `json:"total"`
	Currency     string            `json:"currency"`
	Status       string            `json:"status"`
	URL          string            `json:"url"`
	Tax          string            `json:"tax"`
	Shipping     string            `json:"shipping"`
	Items        []EbayItem        `json:"items"`
	Payments     []EbayPayment     `json:"payments"`
}

type EbaySellerOrder struct {
	OrderID string `json:"order_id"`
	Seller  string `json:"seller"`
	Total   string `json:"total"`
}

type EbayItem struct {
	// ItemID is eBay's item number, which the listing's address carries.
	ItemID    string `json:"item_id"`
	Title     string `json:"title"`
	Quantity  int    `json:"quantity"`
	Price     string `json:"price"`
	Total     string `json:"total"`
	Condition string `json:"condition"`
	URL       string `json:"url"`
}

// EbayPayment is one way the checkout was paid, unsigned as the order page
// prints it.
type EbayPayment struct {
	Date       string `json:"date"`
	Amount     string `json:"amount"`
	Instrument string `json:"instrument"`
}

func parseEbay(raw []byte) (Parsed, error) {
	if raw[0] != '{' {
		return Parsed{}, fmt.Errorf("the file is not Agentifi's eBay export: eBay offers no file of its own; " +
			"the history script on this screen saves one")
	}
	var file EbayFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return Parsed{}, fmt.Errorf("the file is not Agentifi's eBay export: %v", err)
	}
	if file.Source != ebaySource {
		return Parsed{}, fmt.Errorf("the file is not Agentifi's eBay export (its source is %q)", file.Source)
	}
	return FromEbay(file)
}

// FromEbay reads an eBay export already in hand. ErrNothingRead is a file
// with no checkouts.
func FromEbay(file EbayFile) (Parsed, error) {
	out := Parsed{Format: FormatAgentifiJSON, AccountHint: file.AccountHint}
	for _, row := range file.Checkouts {
		if row.OrderID == "" {
			out.Warnings = append(out.Warnings, "a checkout with no order number was skipped")
			continue
		}
		on, err := domain.ParseDate(row.Date)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("order %s: unreadable date %q", row.OrderID, row.Date))
			continue
		}
		order := Order{
			Number: row.OrderID, OrderedOn: on, Currency: row.Currency, Status: row.Status, URL: row.URL,
			Kind: domain.PurchaseOnline,
		}
		if order.Currency == "" {
			order.Currency = "USD"
		}
		if m, ok := domain.ParseMoneyText(row.Total); ok {
			order.Total = m
		} else {
			// A list page that showed only each seller's part still adds up to
			// the checkout.
			for _, seller := range row.SellerOrders {
				if m, ok := domain.ParseMoneyText(seller.Total); ok {
					order.Total = order.Total.Add(m)
				}
			}
		}
		if m, ok := domain.ParseMoneyText(row.Tax); ok {
			order.Tax, order.HasTax = m, true
		}
		if m, ok := domain.ParseMoneyText(row.Shipping); ok {
			order.Shipping, order.HasShipping = m, true
		}
		for _, one := range row.Items {
			item := Item{
				SKU: strings.TrimSpace(one.ItemID), Title: strings.TrimSpace(one.Title), Quantity: one.Quantity,
				Condition: strings.TrimSpace(one.Condition), URL: one.URL,
			}
			if item.Quantity <= 0 {
				item.Quantity = 1
			}
			if m, ok := domain.ParseMoneyText(one.Price); ok {
				item.UnitPrice, item.HasUnitPrice = m, true
			}
			if m, ok := domain.ParseMoneyText(one.Total); ok {
				item.TotalOwed, item.HasTotalOwed = m, true
			} else if item.HasUnitPrice {
				item.TotalOwed, item.HasTotalOwed = item.UnitPrice.MulInt(item.Quantity), true
			}
			order.Items = append(order.Items, item)
		}

		// What a gift card paid never reaches the bank, so it is the order's
		// gift card part; a checkout read with no payment is left unsaid.
		var unseen domain.Money
		for _, payment := range row.Payments {
			paidOn, err := domain.ParseDate(payment.Date)
			amount, ok := domain.ParseMoneyText(payment.Amount)
			if err != nil || !ok {
				out.Warnings = append(out.Warnings, fmt.Sprintf("order %s: a payment was skipped (date %q, amount %q)",
					row.OrderID, payment.Date, payment.Amount))
				continue
			}
			// Unlike Amazon's payments page, eBay's order page names every
			// method, gift cards included, so a blank one is a reading gap and
			// not a gift card.
			instrument := strings.TrimSpace(payment.Instrument)
			if instrument == "" {
				instrument = "payment method not shown"
			}
			if domain.IsGiftCardInstrument(instrument) {
				unseen = unseen.Add(amount.Abs())
			}
			out.Charges = append(out.Charges, Charge{
				OrderNumber: row.OrderID, ChargedOn: paidOn, Amount: amount.Abs().Neg(), Instrument: instrument,
			})
			order.HasGiftCard = true
		}
		order.GiftCard = unseen
		out.Orders = append(out.Orders, order)
	}
	if len(out.Orders) == 0 {
		return Parsed{}, fmt.Errorf("%w: the file holds no orders", ErrNothingRead)
	}
	return out, nil
}
