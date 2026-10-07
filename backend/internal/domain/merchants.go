package domain

import "strings"

// The merchants the order connector knows. The order engine is shared; what
// differs per merchant is the facts below, and every layer reads them from
// here rather than assuming Amazon. A merchant is a value, not a connection:
// which one a bank row belongs to is decided from its wording alone.

// MerchantID names one merchant: "amazon", "costco".
type MerchantID string

const (
	MerchantAmazon MerchantID = "amazon"
	MerchantCostco MerchantID = "costco"
)

// Merchant is one seller's facts.
type Merchant struct {
	ID MerchantID
	// Name is the seller as the household says it: "Amazon", "Costco".
	Name string
	// Noun is what one purchase record is called: Costco has orders online
	// and receipts in the warehouse, so "purchase" covers both.
	Noun, Plural string
	// Wording says whether a bank row's two names belong to this merchant.
	Wording func(statementName, payee string) bool
	// HasGiftCardBalance says the daily pull can read a stored balance and
	// keep it as an account. A Costco Shop Card's balance is only shown card
	// by card.
	HasGiftCardBalance bool
	// HasCatalog says item numbers can be looked up in the merchant's own
	// listing, to read a receipt line's register abbreviation. Amazon's
	// records already carry the full title.
	HasCatalog bool
	// Invoices is where a purchase's invoice document comes from.
	Invoices InvoiceSource
	// SignInAlert is raised when the merchant challenges the kept session.
	SignInAlert AlertType
	// SettingsPath is where the connector's screen lives in the app.
	SettingsPath string
}

// InvoiceSource says how a merchant's invoice documents are made.
type InvoiceSource string

const (
	// InvoicesFromPage is the merchant's own printable invoice page, which a
	// pull opens and prints.
	InvoicesFromPage InvoiceSource = "page"
	// InvoicesLaidOut is a receipt Agentifi lays out from the lines on file,
	// for a merchant with no printable page.
	InvoicesLaidOut InvoiceSource = "laid_out"
)

// Merchants is every merchant, in display order.
var Merchants = []Merchant{
	{
		ID: MerchantAmazon, Name: "Amazon", Noun: "order", Plural: "orders",
		Wording: IsAmazonWording, HasGiftCardBalance: true, Invoices: InvoicesFromPage,
		SignInAlert: AlertAmazonSignIn, SettingsPath: "/settings/merchants/amazon",
	},
	{
		ID: MerchantCostco, Name: "Costco", Noun: "purchase", Plural: "purchases",
		Wording: IsCostcoWording, HasGiftCardBalance: false, HasCatalog: true, Invoices: InvoicesLaidOut,
		SignInAlert: AlertCostcoSignIn, SettingsPath: "/settings/merchants/costco",
	},
}

// MerchantByID finds a merchant, or reports that the id names none.
func MerchantByID(id MerchantID) (Merchant, bool) {
	for _, m := range Merchants {
		if m.ID == id {
			return m, true
		}
	}
	return Merchant{}, false
}

// MerchantsFor is every merchant whose wording a bank row carries, in display
// order. A row that names two is left to both.
func MerchantsFor(statementName, payee string) []Merchant {
	var out []Merchant
	for _, m := range Merchants {
		if m.Wording(statementName, payee) {
			out = append(out, m)
		}
	}
	return out
}

// IsCostcoWording reports whether a bank row's wording names Costco. The
// warehouse, website and gas station all carry the word. The membership
// renewal has no receipt, so the match finds nothing for it.
func IsCostcoWording(statementName, payee string) bool {
	return strings.Contains(strings.ToLower(statementName+" "+payee), "costco")
}

// The kinds of purchase a merchant record can be; the bank words each
// differently.
const (
	PurchaseOnline    = "online"
	PurchaseWarehouse = "warehouse"
	PurchaseFuel      = "fuel"
)

func IsPurchaseKind(kind string) bool {
	switch kind {
	case PurchaseOnline, PurchaseWarehouse, PurchaseFuel:
		return true
	}
	return false
}
