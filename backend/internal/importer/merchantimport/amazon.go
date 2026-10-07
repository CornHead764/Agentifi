package merchantimport

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Amazon's three files.
//
//   - The order-history CSV Amazon's privacy page sends on request ("Request
//     your data" → Your Orders → Retail.OrderHistory.N.csv). One row per item
//     shipment, with what each line actually cost.
//   - The JSON an order-history browser extension exports: an array of orders
//     with their items, no charges.
//   - The JSON shape Agentifi's own daily pull builds from a signed-in page:
//     the orders, and the charges Amazon actually put on the card, per order,
//     signed.

// parseAmazon sniffs which of the three a trimmed file is.
func parseAmazon(trimmed []byte) (Parsed, error) {
	switch trimmed[0] {
	case '[':
		return parseExtensionJSON(trimmed)
	case '{':
		return parseAgentifiJSON(trimmed)
	}
	return parseAmazonCSV(trimmed)
}

// --- Amazon's CSV --------------------------------------------------------------

// The columns of Retail.OrderHistory.N.csv that matter. Looked up by name, so
// a column Amazon adds or moves changes nothing here.
const (
	colOrderID       = "Order ID"
	colOrderDate     = "Order Date"
	colCurrency      = "Currency"
	colUnitPrice     = "Unit Price"
	colTotalOwed     = "Total Owed"
	colASIN          = "ASIN"
	colCondition     = "Product Condition"
	colQuantity      = "Quantity"
	colOrderStatus   = "Order Status"
	colShipDate      = "Ship Date"
	colProductName   = "Product Name"
	colWebsite       = "Website"
	colShipmentTotal = "Shipment Item Subtotal"
)

// amazonNotAvailable is how the CSV spells an empty cell.
const amazonNotAvailable = "Not Available"

func parseAmazonCSV(raw []byte) (Parsed, error) {
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	header, err := reader.Read()
	if err != nil {
		return Parsed{}, fmt.Errorf("the file is not a CSV")
	}
	index := map[string]int{}
	for i, name := range header {
		index[strings.TrimSpace(name)] = i
	}
	for _, required := range []string{colOrderID, colOrderDate, colProductName} {
		if _, ok := index[required]; !ok {
			return Parsed{}, fmt.Errorf("the file is not an Amazon order history: no %q column "+
				"(Amazon's Request Your Data export has one)", required)
		}
	}
	cell := func(record []string, name string) string {
		i, ok := index[name]
		if !ok || i >= len(record) {
			return ""
		}
		value := strings.TrimSpace(record[i])
		if value == amazonNotAvailable {
			return ""
		}
		return value
	}

	out := Parsed{Format: FormatAmazonCSV}
	orders := map[string]*Order{}
	var numbers []string
	line := 1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		number := cell(record, colOrderID)
		if number == "" {
			out.Warnings = append(out.Warnings, fmt.Sprintf("line %d: no order id", line))
			continue
		}
		order, seen := orders[number]
		if !seen {
			on, err := domain.ParseDate(cell(record, colOrderDate))
			if err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("line %d: order %s has no "+
					"readable date (%q)", line, number, cell(record, colOrderDate)))
				continue
			}
			order = &Order{
				Number: number, OrderedOn: on, Currency: cell(record, colCurrency),
				Status: cell(record, colOrderStatus),
				URL:    orderURL(cell(record, colWebsite), number),
			}
			if order.Currency == "" {
				order.Currency = "USD"
			}
			orders[number] = order
			numbers = append(numbers, number)
		}
		item := Item{
			SKU: cell(record, colASIN), Title: cell(record, colProductName),
			Condition: cell(record, colCondition), Quantity: 1,
		}
		if q, err := strconv.Atoi(cell(record, colQuantity)); err == nil && q > 0 {
			item.Quantity = q
		}
		if m, ok := domain.ParseMoneyText(cell(record, colUnitPrice)); ok {
			item.UnitPrice, item.HasUnitPrice = m, true
		}
		if m, ok := domain.ParseMoneyText(cell(record, colTotalOwed)); ok {
			item.TotalOwed, item.HasTotalOwed = m, true
			order.Total = order.Total.Add(m)
		}
		if shipped, err := domain.ParseDate(cell(record, colShipDate)); err == nil {
			item.ShippedOn = shipped
		}
		if item.SKU != "" {
			item.URL = productURL(cell(record, colWebsite), item.SKU)
		}
		order.Items = append(order.Items, item)
	}
	for _, number := range numbers {
		out.Orders = append(out.Orders, *orders[number])
	}
	if len(out.Orders) == 0 {
		return Parsed{}, fmt.Errorf("the file has the right columns and no orders")
	}
	return out, nil
}

// --- The extension's JSON ------------------------------------------------------

// extensionOrder is one order as "Order History Exporter for Amazon" writes it.
type extensionOrder struct {
	OrderID     string          `json:"orderId"`
	OrderDate   string          `json:"orderDate"`
	TotalAmount json.Number     `json:"totalAmount"`
	Currency    string          `json:"currency"`
	OrderStatus string          `json:"orderStatus"`
	DetailsURL  string          `json:"detailsUrl"`
	Items       []extensionItem `json:"items"`
}

type extensionItem struct {
	Title    string      `json:"title"`
	SKU      string      `json:"asin"`
	Quantity json.Number `json:"quantity"`
	Price    json.Number `json:"price"`
	ItemURL  string      `json:"itemUrl"`
}

func parseExtensionJSON(raw []byte) (Parsed, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var rows []extensionOrder
	if err := decoder.Decode(&rows); err != nil {
		return Parsed{}, fmt.Errorf("the file is not an order-history export: %v", err)
	}
	out := Parsed{Format: FormatExtensionJSON}
	for i, row := range rows {
		if row.OrderID == "" {
			return Parsed{}, fmt.Errorf("the file is not an order-history export: entry %d has "+
				"no orderId", i+1)
		}
		on, err := domain.ParseDate(row.OrderDate)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("order %s: unreadable date %q",
				row.OrderID, row.OrderDate))
			continue
		}
		order := Order{
			Number: row.OrderID, OrderedOn: on, Currency: row.Currency,
			Status: row.OrderStatus, URL: row.DetailsURL,
		}
		if order.Currency == "" {
			order.Currency = "USD"
		}
		if m, err := domain.MoneyFromJSONValue(row.TotalAmount); err == nil {
			order.Total = m
		}
		for _, one := range row.Items {
			item := Item{SKU: one.SKU, Title: one.Title, Quantity: 1, URL: one.ItemURL}
			if q, err := one.Quantity.Int64(); err == nil && q > 0 {
				item.Quantity = int(q)
			}
			if m, err := domain.MoneyFromJSONValue(one.Price); err == nil {
				item.UnitPrice, item.HasUnitPrice = m, true
			}
			order.Items = append(order.Items, item)
		}
		out.Orders = append(out.Orders, order)
	}
	return out, nil
}

// --- Agentifi's own JSON -------------------------------------------------------

// The shape the merchant engine builds in-process for FromAmazon. Amounts are
// strings, never floats.
type AmazonFile struct {
	Source      string          `json:"source"`
	ExtractedAt string          `json:"extracted_at"`
	AccountHint string          `json:"account_hint"`
	Orders      []AmazonOrder   `json:"orders"`
	Charges     []AmazonCharge  `json:"charges"`
	Refunds     []AmazonRefund  `json:"refunds"`
	GiftCard    *AmazonGiftCard `json:"gift_card"`
}

type AmazonGiftCard struct {
	Balance  string                   `json:"balance"`
	Activity []AmazonGiftCardActivity `json:"activity"`
}

type AmazonGiftCardActivity struct {
	Date        string `json:"date"`
	Description string `json:"description"`
	Amount      string `json:"amount"`
	OrderID     string `json:"order_id"`
}

type AmazonOrder struct {
	OrderID  string       `json:"order_id"`
	Date     string       `json:"date"`
	Total    string       `json:"total"`
	Currency string       `json:"currency"`
	Status   string       `json:"status"`
	URL      string       `json:"url"`
	Items    []AmazonItem `json:"items"`
	// From the invoice, when the agent read it; absent otherwise.
	GiftCard string `json:"gift_card"`
	Tax      string `json:"tax"`
	Shipping string `json:"shipping"`
}

type AmazonItem struct {
	Title    string `json:"title"`
	SKU      string `json:"asin"`
	Quantity int    `json:"quantity"`
	Price    string `json:"price"`
	URL      string `json:"url"`
}

type AmazonCharge struct {
	OrderID    string `json:"order_id"`
	Date       string `json:"date"`
	Amount     string `json:"amount"`
	Instrument string `json:"instrument"`
}

type AmazonRefund struct {
	OrderID    string `json:"order_id"`
	SKU        string `json:"asin"`
	Title      string `json:"title"`
	Quantity   int    `json:"quantity"`
	Date       string `json:"date"`
	Amount     string `json:"amount"`
	Instrument string `json:"instrument"`
	Status     string `json:"status"`
}

// agentifiSource is the marker the pull's shape carries.
const agentifiSource = "agentifi-amazon-extract"

func parseAgentifiJSON(raw []byte) (Parsed, error) {
	var file AmazonFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return Parsed{}, fmt.Errorf("the file is not Agentifi's Amazon export: %v", err)
	}
	if file.Source != agentifiSource {
		return Parsed{}, fmt.Errorf("the file is not Agentifi's Amazon export (its source is %q)",
			file.Source)
	}
	return FromAmazon(file)
}

// AmazonSource is the marker an AmazonFile carries.
const AmazonSource = agentifiSource

// FromAmazon reads an Amazon export already in hand. ErrNothingRead is a file
// with nothing in it.
func FromAmazon(file AmazonFile) (Parsed, error) {
	out := Parsed{Format: FormatAgentifiJSON, AccountHint: file.AccountHint}
	for _, row := range file.Orders {
		if row.OrderID == "" {
			out.Warnings = append(out.Warnings, "an order with no order id was skipped")
			continue
		}
		on, err := domain.ParseDate(row.Date)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("order %s: unreadable date %q",
				row.OrderID, row.Date))
			continue
		}
		order := Order{
			Number: row.OrderID, OrderedOn: on, Currency: row.Currency, Status: row.Status,
			URL: row.URL,
		}
		if order.Currency == "" {
			order.Currency = "USD"
		}
		if m, ok := domain.ParseMoneyText(row.Total); ok {
			order.Total = m
		}
		if m, ok := domain.ParseMoneyText(row.GiftCard); ok {
			order.GiftCard, order.HasGiftCard = m.Abs(), true
		}
		if m, ok := domain.ParseMoneyText(row.Tax); ok {
			order.Tax, order.HasTax = m, true
		}
		if m, ok := domain.ParseMoneyText(row.Shipping); ok {
			order.Shipping, order.HasShipping = m, true
		}
		for _, one := range row.Items {
			item := Item{SKU: one.SKU, Title: one.Title, Quantity: one.Quantity, URL: one.URL}
			if item.Quantity <= 0 {
				item.Quantity = 1
			}
			if m, ok := domain.ParseMoneyText(one.Price); ok {
				item.UnitPrice, item.HasUnitPrice = m, true
			}
			order.Items = append(order.Items, item)
		}
		out.Orders = append(out.Orders, order)
	}
	for _, row := range file.Charges {
		on, err := domain.ParseDate(row.Date)
		amount, ok := domain.ParseMoneyText(row.Amount)
		if row.OrderID == "" || err != nil || !ok {
			out.Warnings = append(out.Warnings, fmt.Sprintf("a charge was skipped (order %q, "+
				"date %q, amount %q)", row.OrderID, row.Date, row.Amount))
			continue
		}
		out.Charges = append(out.Charges, Charge{
			OrderNumber: row.OrderID, ChargedOn: on, Amount: amount, Instrument: row.Instrument,
		})
	}
	for _, row := range file.Refunds {
		on, err := domain.ParseDate(row.Date)
		amount, ok := domain.ParseMoneyText(row.Amount)
		if row.OrderID == "" || err != nil || !ok || amount.IsZero() {
			out.Warnings = append(out.Warnings, fmt.Sprintf("a refund was skipped (order %q, "+
				"date %q, amount %q)", row.OrderID, row.Date, row.Amount))
			continue
		}
		quantity := row.Quantity
		if quantity <= 0 {
			quantity = 1
		}
		// The returns page writes a refund as money coming back without
		// always signing it, and the invoice writes it as a negative line of
		// the order. One direction here, and it is the bank's: positive.
		out.Refunds = append(out.Refunds, Refund{
			OrderNumber: row.OrderID, SKU: row.SKU, Title: strings.TrimSpace(row.Title),
			Quantity: quantity, RefundedOn: on, Amount: amount.Abs(),
			Instrument: strings.TrimSpace(row.Instrument),
			ToGiftCard: domain.IsGiftCardDestination(row.Instrument),
			Status:     strings.TrimSpace(row.Status),
		})
	}
	if file.GiftCard != nil {
		card := &GiftCard{}
		if m, ok := domain.ParseMoneyText(file.GiftCard.Balance); ok {
			card.Balance, card.HasBalance = m, true
		}
		for _, row := range file.GiftCard.Activity {
			on, err := domain.ParseDate(row.Date)
			amount, ok := domain.ParseMoneyText(row.Amount)
			if err != nil || !ok {
				out.Warnings = append(out.Warnings, fmt.Sprintf("a gift card line was skipped "+
					"(date %q, amount %q)", row.Date, row.Amount))
				continue
			}
			card.Activity = append(card.Activity, GiftCardActivity{
				On: on, Description: strings.TrimSpace(row.Description), Amount: amount,
				OrderNumber: row.OrderID,
			})
		}
		if card.HasBalance || len(card.Activity) > 0 {
			out.GiftCard = card
		}
	}
	if len(out.Orders) == 0 && len(out.Charges) == 0 && len(out.Refunds) == 0 && out.GiftCard == nil {
		return Parsed{}, fmt.Errorf("%w: the file holds no orders and no charges", ErrNothingRead)
	}
	return out, nil
}

func orderURL(website, number string) string {
	if website == "" {
		website = "Amazon.com"
	}
	return "https://www." + strings.ToLower(strings.TrimPrefix(website, "www.")) +
		"/gp/your-account/order-details?orderID=" + number
}

func productURL(website, sku string) string {
	if website == "" {
		website = "Amazon.com"
	}
	return "https://www." + strings.ToLower(strings.TrimPrefix(website, "www.")) + "/dp/" + sku
}

// Shipments groups an order's lines by the day they shipped and sums what was
// owed for each, which is what the card was charged when Amazon charged per
// shipment. Lines with no ship date or no total are left out.
