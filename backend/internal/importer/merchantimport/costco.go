package merchantimport

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Costco's one file: the JSON the daily pull builds from the signed-in site.
// It holds online orders and warehouse receipts in one list, each saying which
// it is, and the tenders each was paid with as charges.
//
// Costco prints an instant saving as a negative line of its own, naming the
// item it belongs to, and a negative line cannot be a share of a charge.
// Those lines are folded into the item they name, so each item leaves this
// file at what it actually cost.

// costcoSource is the marker the export carries, whoever wrote it.
const costcoSource = "agentifi-costco-extract"

// CostcoFile is Agentifi's Costco export, which the merchant engine builds
// in-process for FromCostco.
type CostcoFile struct {
	Source      string         `json:"source"`
	ExtractedAt string         `json:"extracted_at"`
	AccountHint string         `json:"account_hint"`
	Orders      []CostcoOrder  `json:"orders"`
	Charges     []CostcoCharge `json:"charges"`
}

type CostcoOrder struct {
	OrderID  string       `json:"order_id"`
	Kind     string       `json:"kind"`
	Date     string       `json:"date"`
	Total    string       `json:"total"`
	Currency string       `json:"currency"`
	Status   string       `json:"status"`
	URL      string       `json:"url"`
	Location string       `json:"location"`
	Tax      string       `json:"tax"`
	GiftCard string       `json:"gift_card"`
	Items    []CostcoItem `json:"items"`
}

type CostcoItem struct {
	SKU      string `json:"sku"`
	Title    string `json:"title"`
	Quantity int    `json:"quantity"`
	Price    string `json:"price"`
	Total    string `json:"total"`
	URL      string `json:"url"`
}

type CostcoCharge struct {
	OrderID    string `json:"order_id"`
	Date       string `json:"date"`
	Amount     string `json:"amount"`
	Instrument string `json:"instrument"`
}

func parseCostco(raw []byte) (Parsed, error) {
	if raw[0] != '{' {
		return Parsed{}, fmt.Errorf("the file is not Agentifi's Costco export: Costco has no file of its own; " +
			"its purchases arrive by the daily update")
	}
	var file CostcoFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return Parsed{}, fmt.Errorf("the file is not Agentifi's Costco export: %v", err)
	}
	if file.Source != costcoSource {
		return Parsed{}, fmt.Errorf("the file is not Agentifi's Costco export (its source is %q)", file.Source)
	}
	return FromCostco(file)
}

// CostcoSource is the marker a CostcoFile carries.
const CostcoSource = costcoSource

// FromCostco reads a Costco export already in hand. ErrNothingRead is a file
// with no purchases and no tenders.
func FromCostco(file CostcoFile) (Parsed, error) {
	out := Parsed{Format: FormatAgentifiJSON, AccountHint: file.AccountHint}
	for _, row := range file.Orders {
		if row.OrderID == "" {
			out.Warnings = append(out.Warnings, "a purchase with no receipt or order number was skipped")
			continue
		}
		on, err := domain.ParseDate(row.Date)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("purchase %s: unreadable date %q", row.OrderID, row.Date))
			continue
		}
		order := Order{
			Number: row.OrderID, OrderedOn: on, Currency: row.Currency, Status: row.Status, URL: row.URL,
			Kind: strings.ToLower(strings.TrimSpace(row.Kind)), Location: strings.TrimSpace(row.Location),
		}
		if !domain.IsPurchaseKind(order.Kind) {
			order.Kind = domain.PurchaseOnline
		}
		if order.Currency == "" {
			order.Currency = "USD"
		}
		if m, ok := domain.ParseMoneyText(row.Total); ok {
			order.Total = m
		}
		if m, ok := domain.ParseMoneyText(row.Tax); ok {
			order.Tax, order.HasTax = m, true
		}
		if m, ok := domain.ParseMoneyText(row.GiftCard); ok {
			order.GiftCard, order.HasGiftCard = m.Abs(), true
		}
		for _, one := range row.Items {
			item := Item{SKU: strings.TrimSpace(one.SKU), Title: strings.TrimSpace(one.Title), Quantity: one.Quantity, URL: one.URL}
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
		order.Items, order.Total = foldDiscounts(order.Items, order.Total, &out.Warnings, row.OrderID)
		out.Orders = append(out.Orders, order)
	}
	for _, row := range file.Charges {
		on, err := domain.ParseDate(row.Date)
		amount, ok := domain.ParseMoneyText(row.Amount)
		if row.OrderID == "" || err != nil || !ok {
			out.Warnings = append(out.Warnings, fmt.Sprintf("a tender was skipped (purchase %q, date %q, amount %q)",
				row.OrderID, row.Date, row.Amount))
			continue
		}
		out.Charges = append(out.Charges, Charge{
			OrderNumber: row.OrderID, ChargedOn: on, Amount: amount, Instrument: strings.TrimSpace(row.Instrument),
		})
	}
	if len(out.Orders) == 0 && len(out.Charges) == 0 {
		return Parsed{}, fmt.Errorf("%w: the file holds no purchases and no tenders", ErrNothingRead)
	}
	// The receipt's tenders say what a Shop Card, cash or a cheque covered,
	// so a receipt the Shop Card paid in full is not left waiting for a bank
	// row. A purchase with no tender in the file is left unsaid.
	unseen := map[string]domain.Money{}
	tendered := map[string]bool{}
	for _, charge := range out.Charges {
		tendered[charge.OrderNumber] = true
		if domain.IsGiftCardInstrument(charge.Instrument) {
			unseen[charge.OrderNumber] = unseen[charge.OrderNumber].Add(charge.Amount.Abs())
		}
	}
	for i := range out.Orders {
		order := &out.Orders[i]
		if !order.HasGiftCard && tendered[order.Number] {
			order.GiftCard, order.HasGiftCard = unseen[order.Number], true
		}
	}
	return out, nil
}

// discountRef is how a Costco receipt names the item a saving belongs to:
// "TPD/1234567", "/1234567", sometimes with a word first.
var discountRef = regexp.MustCompile(`/\s*(\d{4,})`)

// foldDiscounts takes every negative line off the receipt and into the item
// it belongs to: the item its title names by number, else the nearest priced
// item above it — Costco prints a saving right under its item — else, when
// nothing on the receipt can carry it, it is dropped with a warning and the
// total is left as the receipt said. A return receipt, where every line is
// negative, is left alone: its lines are the refund's own shares.
//
// An item whose cost goes to zero or below through its discount is dropped
// too: a free item has no share of a charge.
func foldDiscounts(items []Item, total domain.Money, warnings *[]string, number string) ([]Item, domain.Money) {
	positive := false
	for _, item := range items {
		if item.HasTotalOwed && item.TotalOwed.IsPositive() {
			positive = true
			break
		}
	}
	if !positive {
		return items, total
	}
	kept := make([]Item, 0, len(items))
	for _, item := range items {
		if !item.HasTotalOwed || !item.TotalOwed.IsNegative() {
			kept = append(kept, item)
			continue
		}
		target := -1
		if m := discountRef.FindStringSubmatch(item.Title); m != nil {
			for i := len(kept) - 1; i >= 0; i-- {
				if kept[i].SKU == m[1] {
					target = i
					break
				}
			}
		}
		if target < 0 {
			for i := len(kept) - 1; i >= 0; i-- {
				if kept[i].HasTotalOwed && kept[i].TotalOwed.IsPositive() {
					target = i
					break
				}
			}
		}
		if target < 0 {
			*warnings = append(*warnings, fmt.Sprintf("purchase %s: the saving %q (%s) names no item and was left out",
				number, item.Title, item.TotalOwed))
			continue
		}
		kept[target].TotalOwed = kept[target].TotalOwed.Add(item.TotalOwed)
		if kept[target].Quantity > 0 && kept[target].HasUnitPrice {
			kept[target].UnitPrice, kept[target].HasUnitPrice = domain.Zero, false
		}
	}
	out := make([]Item, 0, len(kept))
	for _, item := range kept {
		if item.HasTotalOwed && !item.TotalOwed.IsPositive() {
			continue
		}
		out = append(out, item)
	}
	return out, total
}
