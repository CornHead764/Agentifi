package merchantimport_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
)

// The three files, each read into the same shape.

const amazonCSV = "\xef\xbb\xbf" + `Website,Order ID,Order Date,Purchase Order Number,Currency,Unit Price,Unit Price Tax,Shipping Charge,Total Discounts,Total Owed,Shipment Item Subtotal,Shipment Item Subtotal Tax,ASIN,Product Condition,Quantity,Payment Instrument Type,Order Status,Shipment Status,Ship Date,Shipping Option,Shipping Address,Billing Address,Carrier Name & Tracking Number,Product Name,Gift Message,Gift Sender Name,Gift Recipient Contact Details,Item Serial Number
Amazon.com,113-1234567-0000001,2026-08-01T14:03:11Z,Not Available,USD,13.00,1.00,0,0,14.00,13.00,1.00,B0EXAMPLE1,New,1,Visa - 1234,Closed,Shipped,2026-08-02T03:00:00Z,standard,Not Available,Not Available,AMZN_US(TBA1),USB-C Cable,Not Available,Not Available,Not Available,Not Available
Amazon.com,113-1234567-0000001,2026-08-01T14:03:11Z,Not Available,USD,24.00,2.00,0,0,26.00,24.00,2.00,B0EXAMPLE2,New,1,Visa - 1234,Closed,Shipped,2026-08-04T03:00:00Z,standard,Not Available,Not Available,AMZN_US(TBA2),Glass Storage Set,Not Available,Not Available,Not Available,Not Available
Amazon.com,113-7654321-0000002,2026-08-10T09:00:00Z,Not Available,USD,8.00,0,0,0,16.00,16.00,0,B0EXAMPLE3,New,2,Visa - 1234,Closed,Shipped,2026-08-10T20:00:00Z,standard,Not Available,Not Available,AMZN_US(TBA3),Batteries,Not Available,Not Available,Not Available,Not Available
`

func TestAmazonsOwnCSVGroupsLinesIntoOrders(t *testing.T) {
	parsed, err := merchantimport.Parse(domain.MerchantAmazon, []byte(amazonCSV))
	require.NoError(t, err)
	require.Equal(t, merchantimport.FormatAmazonCSV, parsed.Format)
	require.Len(t, parsed.Orders, 2)
	require.Empty(t, parsed.Warnings)

	first := parsed.Orders[0]
	require.Equal(t, "113-1234567-0000001", first.Number)
	require.Equal(t, "2026-08-01", first.OrderedOn.String())
	require.Equal(t, "40.00", first.Total.String(), "the order total is what was owed across lines")
	require.Len(t, first.Items, 2)
	require.Equal(t, "USB-C Cable", first.Items[0].Title)
	require.Equal(t, "B0EXAMPLE1", first.Items[0].SKU)
	require.Equal(t, "14.00", first.Items[0].TotalOwed.String())
	require.Equal(t, "2026-08-02", first.Items[0].ShippedOn.String())
	require.Equal(t, "https://www.amazon.com/dp/B0EXAMPLE1", first.Items[0].URL)
	// Two lines shipped on two days: two charges the bank will see.
	shipments := first.Shipments()
	require.Len(t, shipments, 2)
	require.Equal(t, "14.00", shipments[0].String())
	require.Equal(t, "26.00", shipments[1].String())

	second := parsed.Orders[1]
	require.Equal(t, 2, second.Items[0].Quantity)
	require.Equal(t, "16.00", second.Total.String())
}

func TestTheExtensionsJSONIsRead(t *testing.T) {
	raw := `[{"orderId":"113-1111111-2222222","orderDate":"2026-07-30T00:00:00.000Z",
	  "totalAmount":45.00,"currency":"USD","orderStatus":"Delivered",
	  "detailsUrl":"https://www.amazon.com/gp/your-account/order-details?orderID=113-1111111-2222222",
	  "items":[{"title":"Single-Board Computer 8GB","asin":"B0SBCEXAMP","quantity":1,"price":80.00,
	  "discount":0,"itemUrl":"https://www.amazon.com/dp/B0SBCEXAMP"}],
	  "promotions":[],"totalSavings":0}]`
	parsed, err := merchantimport.Parse(domain.MerchantAmazon, []byte(raw))
	require.NoError(t, err)
	require.Equal(t, merchantimport.FormatExtensionJSON, parsed.Format)
	require.Len(t, parsed.Orders, 1)
	order := parsed.Orders[0]
	require.Equal(t, "2026-07-30", order.OrderedOn.String())
	require.Equal(t, "45.00", order.Total.String())
	require.Equal(t, "Single-Board Computer 8GB", order.Items[0].Title)
	require.True(t, order.Items[0].HasUnitPrice)
	require.Equal(t, "80.00", order.Items[0].UnitPrice.String())
	require.False(t, order.Items[0].HasTotalOwed, "the extension does not know what a line cost")
}

func TestAgentifisOwnJSONCarriesCharges(t *testing.T) {
	raw := `{"source":"agentifi-amazon-extract","account_hint":"Alex",
	  "orders":[{"order_id":"113-3333333-4444444","date":"2026-08-20","total":"22.00",
	    "currency":"USD","status":"Delivered","url":"",
	    "items":[{"title":"Dog treats","asin":"B0DOG","quantity":2,"price":"11.00","url":""}]}],
	  "charges":[{"order_id":"113-3333333-4444444","date":"2026-08-21","amount":"-$22.00","instrument":"Visa ••••1234"},
	             {"order_id":"113-3333333-4444444","date":"2026-08-25","amount":"+$11.00","instrument":"Visa ••••1234"}]}`
	parsed, err := merchantimport.Parse(domain.MerchantAmazon, []byte(raw))
	require.NoError(t, err)
	require.Equal(t, merchantimport.FormatAgentifiJSON, parsed.Format)
	require.Equal(t, "Alex", parsed.AccountHint)
	require.Len(t, parsed.Charges, 2)
	require.Equal(t, "-22.00", parsed.Charges[0].Amount.String())
	require.Equal(t, "11.00", parsed.Charges[1].Amount.String(), "a refund is money in")
	require.Equal(t, "2026-08-21", parsed.Charges[0].ChargedOn.String())
}

func TestSomethingElseIsRefused(t *testing.T) {
	_, err := merchantimport.Parse(domain.MerchantAmazon, []byte("Date,Account,Payee,Category,Amount\n2026-01-01,Checking,X,Y,-1\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not an Amazon order history")

	_, err = merchantimport.Parse(domain.MerchantAmazon, []byte(`{"source":"something"}`))
	require.Error(t, err)

	_, err = merchantimport.Parse(domain.MerchantAmazon, []byte(`[{"foo":1}]`))
	require.Error(t, err)
}

func TestAgentifisOwnJSONCarriesTheGiftCardBalance(t *testing.T) {
	raw := `{"source":"agentifi-amazon-extract","orders":[],"charges":[],
	  "gift_card":{"balance":"$42.00","activity":[
	    {"date":"2026-08-20","description":"Gift card applied to order 113-3333333-4444444","amount":"-$11.00","order_id":"113-3333333-4444444"},
	    {"date":"2026-08-15","description":"Gift card reload","amount":"$50.00"},
	    {"date":"bad","description":"nonsense","amount":"$1.00"}]}}`
	parsed, err := merchantimport.Parse(domain.MerchantAmazon, []byte(raw))
	require.NoError(t, err)
	require.NotNil(t, parsed.GiftCard)
	require.True(t, parsed.GiftCard.HasBalance)
	require.Equal(t, "42.00", parsed.GiftCard.Balance.String())
	require.Len(t, parsed.GiftCard.Activity, 2)
	require.Equal(t, "-11.00", parsed.GiftCard.Activity[0].Amount.String())
	require.Equal(t, "113-3333333-4444444", parsed.GiftCard.Activity[0].OrderNumber)
	require.Equal(t, "50.00", parsed.GiftCard.Activity[1].Amount.String())
	require.Len(t, parsed.Warnings, 1, "the unreadable line is skipped and said so")
}
