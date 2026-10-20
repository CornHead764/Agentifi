package merchants

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The Amazon order-card and invoice readers over invented pages in a real
// Chromium: the shipment boxes of a card and the FSA/HSA lines of an invoice.
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/merchants/ -run AgainstARealBrowser

const twoShipmentCard = `<!doctype html><html><body>
<div class="order-card js-order-card">
  <div class="order-header">ORDER PLACED October 9, 2025 TOTAL $28.00 ORDER # 111-0000000-0000001</div>
  <div class="a-box shipment">
    <div class="js-shipment-info-container"><span class="a-size-medium delivery-box__primary-text">Delivered October 11</span></div>
    <div class="yohtmlc-item"><a href="/dp/B0TESTONE1?ref=x">Vanilla paste, 4 oz</a> Qty: 1</div>
  </div>
  <div class="a-box shipment">
    <div class="js-shipment-info-container"><span class="a-size-medium delivery-box__primary-text">Arriving October 14</span></div>
    <div class="yohtmlc-item"><a href="/dp/B0TESTTWO2">Cold remedy, 30 count</a> Qty: 1</div>
  </div>
</div>
<div class="order-card js-order-card">
  <div class="order-header">ORDER PLACED October 9, 2025 TOTAL $9.00 ORDER # 111-0000000-0000002</div>
  <div class="yohtmlc-item"><a href="/dp/B0TESTTHR3">Desk lamp</a> Qty: 1</div>
</div>
</body></html>`

const fsaInvoice = `<!doctype html><html><head><title>Final Details for Order #111-0000000-0000001</title></head><body>
<div class="item">
  <a href="/dp/B0TESTONE1">Vanilla paste, 4 oz</a>
  <div>Sold by: Invented Seller</div><div>Qty: 1</div><div>$15.00</div>
</div>
<div class="item">
  <a href="/dp/B0TESTTWO2">Cold remedy, 30 count</a>
  <div>Sold by: Invented Seller</div><div>Qty: 1</div><div>FSA or HSA eligible</div><div>$20.00</div>
</div>
<div class="summary">
  <div>Item(s) Subtotal: $35.00</div>
  <div>Shipping &amp; Handling: $0.00</div>
  <div>Estimated tax to be collected: $0.80</div>
  <div>Grand Total: $28.00</div>
  <div>FSA or HSA eligible: $16.00</div>
  <div>(inc. tax and shipping)</div>
</div>
</body></html>`

const plainInvoice = `<!doctype html><html><head><title>Final Details</title></head><body>
<div class="item">
  <a href="/dp/B0TESTTHR3">Desk lamp</a>
  <div>Sold by: Invented Seller</div><div>Qty: 1</div><div>$9.00</div>
</div>
<div class="summary"><div>Item(s) Subtotal: $9.00</div><div>Grand Total: $9.00</div></div>
</body></html>`

// both holds two products in one box with no row of their own, so which
// carries the eligibility mark cannot be told.
const crowdedInvoice = `<!doctype html><html><head><title>Final Details</title></head><body>
<div class="items">
  <a href="/dp/B0TESTONE1">Vanilla paste, 4 oz</a>
  <a href="/dp/B0TESTTWO2">Cold remedy, 30 count</a>
  <div>Sold by: Invented Seller</div><div>Qty: 1</div><div>FSA or HSA eligible</div><div>$15.00</div><div>$20.00</div>
</div>
<div class="summary"><div>Grand Total: $35.00</div><div>FSA or HSA eligible: $16.00</div></div>
</body></html>`

func TestTheOrderCardGroupsItemsByShipmentAgainstARealBrowser(t *testing.T) {
	page, base := costcoLivePage(t, map[string]string{"/card": twoShipmentCard})
	require.NoError(t, page.Goto(base+"/card"))
	page.Settle()

	orders, err := readOrders(page)
	require.NoError(t, err)
	require.Len(t, orders, 2)

	split := orders[0].Items
	require.Len(t, split, 2)
	require.NotNil(t, split[0].Shipment)
	require.NotNil(t, split[1].Shipment)
	require.Equal(t, 0, *split[0].Shipment)
	require.Equal(t, 1, *split[1].Shipment)
	require.Equal(t, "Delivered October 11", split[0].ShipmentText)
	require.Equal(t, "Arriving October 14", split[1].ShipmentText)

	loose := orders[1].Items
	require.Len(t, loose, 1)
	require.Nil(t, loose[0].Shipment, "an item in no box has no shipment")
}

func TestTheInvoiceReadsItsFSAHSALinesAgainstARealBrowser(t *testing.T) {
	page, base := costcoLivePage(t, map[string]string{
		"/fsa": fsaInvoice, "/plain": plainInvoice, "/crowded": crowdedInvoice,
	})

	require.NoError(t, page.Goto(base+"/fsa"))
	read, err := readInvoice(page)
	require.NoError(t, err)
	require.Equal(t, "16.00", read.FSAHSA)
	require.Len(t, read.Items, 2)
	require.False(t, read.Items[0].FSAHSA)
	require.True(t, read.Items[1].FSAHSA)

	require.NoError(t, page.Goto(base+"/plain"))
	read, err = readInvoice(page)
	require.NoError(t, err)
	require.Empty(t, read.FSAHSA, "an invoice with no line names no total")
	require.Len(t, read.Items, 1)
	require.False(t, read.Items[0].FSAHSA)

	require.NoError(t, page.Goto(base+"/crowded"))
	read, err = readInvoice(page)
	require.NoError(t, err)
	require.Equal(t, "16.00", read.FSAHSA)
	for _, item := range read.Items {
		require.False(t, item.FSAHSA, "a mark over two products belongs to neither")
	}
}
