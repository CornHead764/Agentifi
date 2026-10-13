package merchantimport_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
)

const ebayExport = `{
  "source": "agentifi-ebay-extract",
  "extracted_at": "2026-09-12T15:04:05Z",
  "account_hint": "Alex",
  "checkouts": [
    {"order_id": "01-00000-00001",
     "seller_orders": [
       {"order_id": "01-00000-00001", "seller": "example_parts", "total": "60.00"},
       {"order_id": "01-00000-00002", "seller": "example_cables", "total": "40.00"}
     ],
     "date": "2026-09-05", "total": "100.00", "currency": "USD", "status": "Delivered",
     "url": "https://order.ebay.com/ord/show?orderId=01-00000-00001",
     "tax": "6.00", "shipping": "4.00",
     "items": [
       {"item_id": "100000000001", "title": "Server Bezel", "quantity": 1, "price": "54.00", "total": "54.00",
        "condition": "Used", "url": "https://www.ebay.com/itm/100000000001"},
       {"item_id": "100000000002", "title": "Power Cable", "quantity": 2, "price": "18.00", "total": "36.00",
        "condition": "New", "url": "https://www.ebay.com/itm/100000000002"}
     ],
     "payments": [{"date": "2026-09-05", "amount": "100.00", "instrument": "PayPal"}]},
    {"order_id": "02-00000-00003",
     "seller_orders": [{"order_id": "02-00000-00003", "seller": "example_shop", "total": "30.00"}],
     "date": "2026-09-08", "total": "30.00",
     "items": [{"item_id": "100000000003", "title": "Thermostat", "quantity": 1, "price": "30.00"}],
     "payments": [
       {"date": "2026-09-08", "amount": "10.00", "instrument": "eBay gift card"},
       {"date": "2026-09-08", "amount": "20.00", "instrument": "Visa ending in 0000"}
     ]}
  ]
}`

func TestAnEbayCheckoutIsOneOrderChargedOnceForEverySeller(t *testing.T) {
	parsed, err := merchantimport.Parse(domain.MerchantEbay, []byte(ebayExport))
	require.NoError(t, err)
	require.Equal(t, merchantimport.FormatAgentifiJSON, parsed.Format)
	require.Equal(t, "Alex", parsed.AccountHint)
	require.Len(t, parsed.Orders, 2)

	cart := parsed.Orders[0]
	require.Equal(t, "01-00000-00001", cart.Number)
	require.Equal(t, domain.PurchaseOnline, cart.Kind)
	require.Equal(t, "100.00", cart.Total.String())
	require.Equal(t, "6.00", cart.Tax.String())
	require.Equal(t, "4.00", cart.Shipping.String())
	require.Len(t, cart.Items, 2, "both sellers' items are the checkout's")
	require.Equal(t, "100000000002", cart.Items[1].SKU)
	require.Equal(t, "36.00", cart.Items[1].TotalOwed.String())
	require.Equal(t, "Used", cart.Items[0].Condition)
	require.True(t, cart.HasGiftCard)
	require.True(t, cart.GiftCard.IsZero())

	var cartCharges []merchantimport.Charge
	for _, charge := range parsed.Charges {
		if charge.OrderNumber == cart.Number {
			cartCharges = append(cartCharges, charge)
		}
	}
	require.Len(t, cartCharges, 1, "one payment for the whole cart")
	require.Equal(t, "-100.00", cartCharges[0].Amount.String(), "signed as the bank sees a charge")
}

func TestAGiftCardPartOfAnEbayCheckoutIsWhatTheBankNeverSaw(t *testing.T) {
	parsed, err := merchantimport.Parse(domain.MerchantEbay, []byte(ebayExport))
	require.NoError(t, err)
	order := parsed.Orders[1]
	require.Equal(t, "10.00", order.GiftCard.String())
	require.Equal(t, "30.00", order.Items[0].TotalOwed.String(), "a line with only a price owes price × quantity")
	require.Equal(t, "USD", order.Currency)
}

func TestAnEbayPaymentWithNoMethodReadIsNotAGiftCard(t *testing.T) {
	parsed, err := merchantimport.FromEbay(merchantimport.EbayFile{
		Source: merchantimport.EbaySource,
		Checkouts: []merchantimport.EbayCheckout{{
			OrderID: "03-00000-00004", Date: "2026-09-09", Total: "25.00",
			Payments: []merchantimport.EbayPayment{{Date: "2026-09-09", Amount: "25.00"}},
		}},
	})
	require.NoError(t, err)
	require.True(t, parsed.Orders[0].GiftCard.IsZero())
	require.False(t, domain.IsGiftCardInstrument(parsed.Charges[0].Instrument))
}

func TestAFileThatIsNotEbaysIsRefused(t *testing.T) {
	_, err := merchantimport.Parse(domain.MerchantEbay, []byte(costcoExport))
	require.ErrorContains(t, err, "not Agentifi's eBay export")
	_, err = merchantimport.Parse(domain.MerchantEbay, []byte("Order ID,Order Date\n"))
	require.ErrorContains(t, err, "the history script on this screen saves one")
	_, err = merchantimport.FromEbay(merchantimport.EbayFile{Source: merchantimport.EbaySource})
	require.ErrorIs(t, err, merchantimport.ErrNothingRead)
}

// What tools/extractors/extract-ebay.js saves: the purchases list only, so
// seller orders' totals and items without prices or payments.
const ebayHistoryScriptFile = `{
  "source": "agentifi-ebay-extract",
  "extracted_at": "2026-10-12T20:00:00.000Z",
  "account_hint": "",
  "checkouts": [
    {"order_id": "05-00000-00001",
     "seller_orders": [{"order_id": "05-00000-00002", "total": "12.50"}, {"order_id": "05-00000-00001", "total": "30.00"}],
     "date": "2024-03-02", "currency": "USD", "status": "Delivered",
     "url": "https://order.ebay.com/ord/show?orderId=05-00000-00001",
     "items": [
       {"item_id": "200000000001", "title": "Network Switch", "quantity": 1, "url": "https://www.ebay.com/itm/200000000001"},
       {"item_id": "200000000002", "title": "Patch Cables", "quantity": 3, "url": "https://www.ebay.com/itm/200000000002"}
     ]}
  ]
}`

func TestTheHistoryScriptsFileImportsAsCheckoutsWithNoChargeInvented(t *testing.T) {
	parsed, err := merchantimport.Parse(domain.MerchantEbay, []byte(ebayHistoryScriptFile))
	require.NoError(t, err)
	require.Len(t, parsed.Orders, 1)
	order := parsed.Orders[0]
	require.Equal(t, "05-00000-00001", order.Number)
	require.Equal(t, "42.50", order.Total.String(), "the seller orders add up to the checkout")
	require.Len(t, order.Items, 2)
	require.Equal(t, 3, order.Items[1].Quantity)
	require.False(t, order.Items[1].HasTotalOwed, "the list names no price, and none is made up")
	require.False(t, order.HasGiftCard, "no payment read leaves the gift card part unsaid")
	require.Empty(t, parsed.Charges, "the order matches by its total, not by a charge nobody read")
}
