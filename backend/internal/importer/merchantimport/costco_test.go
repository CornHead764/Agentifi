package merchantimport_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
)

const costcoExport = `{
  "source": "agentifi-costco-extract",
  "extracted_at": "2026-09-12T15:04:05Z",
  "account_hint": "Alex",
  "orders": [
    {"order_id": "21100123456789012345", "kind": "warehouse", "date": "2026-09-05", "total": "50.00",
     "currency": "USD", "status": "", "url": "", "location": "Costco Springfield #0123", "tax": "9.00",
     "gift_card": "",
     "items": [
       {"sku": "8880002", "title": "/0000000", "quantity": 1, "price": "-1.00", "total": "-1.00"},
       {"sku": "1234567", "title": "KS ORGANIC EGGS", "quantity": 2, "price": "8.00", "total": "16.00"},
       {"sku": "7654321", "title": "TPD/1234567", "quantity": 1, "price": "-2.00", "total": "-2.00"},
       {"sku": "2223334", "title": "ROTISSERIE CHKN", "quantity": 1, "price": "5.00", "total": "5.00"},
       {"sku": "5556667", "title": "KS PAPER TOWEL", "quantity": 1, "price": "22.00", "total": "22.00"},
       {"sku": "9990001", "title": "/5556667", "quantity": 1, "price": "-4.00", "total": "-4.00"}
     ]},
    {"order_id": "1234567890", "kind": "online", "date": "2026-09-01", "total": "325.00",
     "currency": "USD", "status": "Delivered",
     "url": "https://www.costco.com/OrderStatusDetailsCmd?orderNumber=1234567890", "location": "",
     "tax": "25.00", "gift_card": "",
     "items": [{"sku": "4000123456", "title": "Countertop Blender", "quantity": 1, "price": "300.00", "total": "300.00"}]},
    {"order_id": "21100999", "kind": "warehouse", "date": "2026-09-07", "total": "-22.00",
     "location": "Costco Springfield #0123",
     "items": [{"sku": "5556667", "title": "KS PAPER TOWEL", "quantity": 1, "price": "-22.00", "total": "-22.00"}]},
    {"order_id": "21100777", "kind": "gas", "date": "2026-09-06", "total": "52.00",
     "location": "Costco Gas Springfield",
     "items": [{"sku": "1", "title": "REGULAR", "quantity": 1, "price": "52.00", "total": "52.00"}]}
  ],
  "charges": [
    {"order_id": "21100123456789012345", "date": "2026-09-05", "amount": "-50.00", "instrument": "Visa ••••1234"},
    {"order_id": "1234567890", "date": "2026-09-02", "amount": "-325.00", "instrument": "Visa ••••1234"},
    {"order_id": "21100999", "date": "2026-09-07", "amount": "22.00", "instrument": "Visa ••••1234"},
    {"order_id": "21100777", "date": "2026-09-06", "amount": "-52.00", "instrument": "Costco Shop Card"}
  ]
}`

func TestCostcoReceiptsBecomeOrdersWithSavingsFoldedIntoTheirItems(t *testing.T) {
	parsed, err := merchantimport.Parse(domain.MerchantCostco, []byte(costcoExport))
	require.NoError(t, err)
	require.Equal(t, merchantimport.FormatAgentifiJSON, parsed.Format)
	require.Equal(t, "Alex", parsed.AccountHint)
	require.Len(t, parsed.Orders, 4)
	require.Len(t, parsed.Charges, 4)

	receipt := parsed.Orders[0]
	require.Equal(t, domain.PurchaseWarehouse, receipt.Kind)
	require.Equal(t, "Costco Springfield #0123", receipt.Location)
	require.Equal(t, "50.00", receipt.Total.String())
	require.True(t, receipt.HasTax)
	require.Equal(t, "9.00", receipt.Tax.String())
	// Two savings named their items and were folded in; one came before any
	// item it could belong to and was left out with a warning. No negative
	// line remains.
	require.Len(t, receipt.Items, 3)
	require.Equal(t, "KS ORGANIC EGGS", receipt.Items[0].Title)
	require.Equal(t, "14.00", receipt.Items[0].TotalOwed.String())
	require.False(t, receipt.Items[0].HasUnitPrice, "a discounted item's unit price no longer describes it")
	require.Equal(t, "5.00", receipt.Items[1].TotalOwed.String())
	require.Equal(t, "18.00", receipt.Items[2].TotalOwed.String())
	require.Len(t, parsed.Warnings, 1)
	require.Contains(t, parsed.Warnings[0], "/0000000")

	online := parsed.Orders[1]
	require.Equal(t, domain.PurchaseOnline, online.Kind)
	require.Equal(t, "Delivered", online.Status)
	require.Equal(t, "300.00", online.Items[0].TotalOwed.String())
	require.True(t, online.Items[0].HasUnitPrice)

	// A return receipt is all negative and is left exactly as written.
	returned := parsed.Orders[2]
	require.Equal(t, "-22.00", returned.Total.String())
	require.Len(t, returned.Items, 1)
	require.Equal(t, "-22.00", returned.Items[0].TotalOwed.String())

	// An unknown kind is read as online rather than refused.
	require.Equal(t, domain.PurchaseOnline, parsed.Orders[3].Kind)

	// The tenders say what the bank never saw: the gas was all Shop Card,
	// the warehouse receipt all card.
	require.True(t, parsed.Orders[3].HasGiftCard)
	require.Equal(t, "52.00", parsed.Orders[3].GiftCard.String())
	require.True(t, receipt.HasGiftCard)
	require.True(t, receipt.GiftCard.IsZero())

	require.Equal(t, "-50.00", parsed.Charges[0].Amount.String())
	require.Equal(t, "Visa ••••1234", parsed.Charges[0].Instrument)
	require.Equal(t, "22.00", parsed.Charges[2].Amount.String(), "a return's tender is money back")
	require.True(t, domain.IsGiftCardInstrument(parsed.Charges[3].Instrument), "a Shop Card is one the bank never saw")
}

func TestCostcoRefusesAmazonsFileAndAmazonRefusesCostcos(t *testing.T) {
	_, err := merchantimport.Parse(domain.MerchantCostco, []byte(`{"source":"agentifi-amazon-extract","orders":[]}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not Agentifi's Costco export")
	_, err = merchantimport.Parse(domain.MerchantAmazon, []byte(costcoExport))
	require.Error(t, err)
	_, err = merchantimport.Parse(domain.MerchantCostco, []byte("Website,Order ID\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no file of its own")
}
