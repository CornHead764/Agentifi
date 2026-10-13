package merchants

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The eBay module over a server answering what eBay's API answers. Every
// order, item, figure, token and picture is invented.

var ebayTestApp = provider.EbayApp{AppID: "Example-Agentifi-PRD-0", CertID: "PRD-invented", RuName: "Example-RuName"}

// ebayFake answers eBay's token, GetOrders, GetItem and picture addresses.
type ebayFake struct {
	server  *httptest.Server
	calls   []string
	orders  string
	refused bool
}

func (f *ebayFake) Do(req *http.Request) (*http.Response, error) {
	f.calls = append(f.calls, req.URL.Host+req.URL.Path+" "+req.Header.Get("X-EBAY-API-CALL-NAME"))
	target, _ := url.Parse(f.server.URL)
	sent := req.Clone(req.Context())
	sent.URL.Scheme, sent.URL.Host, sent.Host = target.Scheme, target.Host, target.Host
	sent.Header.Set("X-Asked-Path", req.URL.Path)
	sent.RequestURI = ""
	return f.server.Client().Do(sent)
}

func newEbayFake(t *testing.T) *ebayFake {
	t.Helper()
	fake := &ebayFake{orders: ebayCartAndSingle}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/identity/v1/oauth2/token":
			if fake.refused {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"access-fresh","expires_in":7200}`)
		case r.Header.Get("X-EBAY-API-CALL-NAME") == "GetOrders":
			_, _ = io.WriteString(w, fake.orders)
		case r.Header.Get("X-EBAY-API-CALL-NAME") == "GetItem":
			body, _ := io.ReadAll(r.Body)
			id := strings.TrimSuffix(strings.SplitN(string(body), "<ItemID>", 2)[1][:12], "<")
			_, _ = io.WriteString(w, `<GetItemResponse xmlns="urn:ebay:apis:eBLBaseComponents"><Ack>Success</Ack><Item>`+
				`<ItemID>`+id+`</ItemID><Title>Listing `+id+`</Title><ConditionDisplayName>Used</ConditionDisplayName>`+
				`<Description>&lt;p&gt;Works.&lt;script&gt;steal()&lt;/script&gt;&lt;img src="https://tracker.example.invalid/x.gif"&gt;&lt;/p&gt;</Description>`+
				`<PictureDetails><PictureURL>https://i.ebayimg.com/images/g/`+id+`/s-l1600.jpg</PictureURL></PictureDetails>`+
				`<ItemSpecifics><NameValueList><Name>Brand</Name><Value>Example</Value></NameValueList></ItemSpecifics>`+
				`<Seller><UserID>someone_else</UserID><Email>someone@example.invalid</Email></Seller></Item></GetItemResponse>`)
		default:
			_, _ = w.Write([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00invented"))
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// Two seller orders paid seconds apart by PayPal (one checkout), and one paid
// half an hour later by card (its own).
const ebayCartAndSingle = `<GetOrdersResponse xmlns="urn:ebay:apis:eBLBaseComponents"><Ack>Success</Ack>
<HasMoreOrders>false</HasMoreOrders><OrderArray>
<Order><OrderID>01-00000-00002</OrderID><OrderStatus>Completed</OrderStatus>
 <CheckoutStatus><PaymentMethod>PayPal</PaymentMethod></CheckoutStatus>
 <ShippingServiceSelected><ShippingServiceCost currencyID="USD">0.00</ShippingServiceCost></ShippingServiceSelected>
 <Total currencyID="USD">36.00</Total><AmountPaid currencyID="USD">36.00</AmountPaid>
 <CreatedTime>2026-09-05T02:23:01.000Z</CreatedTime><PaidTime>2026-09-05T02:23:01.119Z</PaidTime>
 <SellerUserID>someone_else</SellerUserID>
 <TransactionArray><Transaction><Item><ItemID>100000000002</ItemID><Title>Power Cable</Title></Item>
  <QuantityPurchased>2</QuantityPurchased><TransactionPrice currencyID="USD">16.00</TransactionPrice>
  <Taxes><TotalTaxAmount currencyID="USD">4.00</TotalTaxAmount></Taxes></Transaction></TransactionArray></Order>
<Order><OrderID>01-00000-00001</OrderID><OrderStatus>Completed</OrderStatus>
 <CheckoutStatus><PaymentMethod>PayPal</PaymentMethod></CheckoutStatus>
 <ShippingServiceSelected><ShippingServiceCost currencyID="USD">4.00</ShippingServiceCost></ShippingServiceSelected>
 <Total currencyID="USD">64.00</Total><AmountPaid currencyID="USD">64.00</AmountPaid>
 <CreatedTime>2026-09-05T02:22:58.000Z</CreatedTime><PaidTime>2026-09-05T02:22:57.892Z</PaidTime>
 <TransactionArray><Transaction><Item><ItemID>100000000001</ItemID><Title>Server Bezel</Title></Item>
  <QuantityPurchased>1</QuantityPurchased><TransactionPrice currencyID="USD">54.00</TransactionPrice>
  <Taxes><TotalTaxAmount currencyID="USD">6.00</TotalTaxAmount></Taxes></Transaction></TransactionArray></Order>
<Order><OrderID>02-00000-00003</OrderID><OrderStatus>Completed</OrderStatus>
 <CheckoutStatus><PaymentMethod>CreditCard</PaymentMethod></CheckoutStatus>
 <Total currencyID="USD">20.00</Total><AmountPaid currencyID="USD">20.00</AmountPaid>
 <CreatedTime>2026-09-05T02:57:40.000Z</CreatedTime><PaidTime>2026-09-05T02:57:40.057Z</PaidTime>
 <TransactionArray><Transaction><Item><ItemID>100000000003</ItemID><Title>Thermostat</Title></Item>
  <QuantityPurchased>1</QuantityPurchased><TransactionPrice currencyID="USD">20.00</TransactionPrice></Transaction></TransactionArray></Order>
</OrderArray></GetOrdersResponse>`

func ebaySession(t *testing.T, s provider.EbaySession) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(s)
	require.NoError(t, err)
	return raw
}

var ebayPullAt = time.Date(2026, time.September, 12, 9, 0, 0, 0, time.UTC)

func ebayCall(fake *ebayFake, session json.RawMessage) Call {
	return Call{HTTP: fake, Session: session, SinceDays: 365, Notes: &Notes{}, Invoiced: map[string]bool{},
		Now: func() time.Time { return ebayPullAt }}
}

func TestAnEbayPullFilesEachCheckoutAsOneOrderChargedOnce(t *testing.T) {
	fake := newEbayFake(t)
	session := ebaySession(t, provider.EbaySession{Kind: provider.EbaySessionKind, RefreshToken: "refresh",
		RefreshExpiresAt: ebayPullAt.AddDate(1, 0, 0), AccessToken: "access-old", AccessExpiresAt: ebayPullAt.Add(-time.Minute)})

	result, err := Ebay(ebayTestApp).Fetch(ebayCall(fake, session))
	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.NotNil(t, result.Parsed)

	var kept provider.EbaySession
	require.NoError(t, json.Unmarshal(result.StorageState, &kept))
	require.Equal(t, "access-fresh", kept.AccessToken, "a lapsed access token is replaced and kept")
	require.Equal(t, "refresh", kept.RefreshToken)

	orders := result.Parsed.Orders
	require.Len(t, orders, 2, "two seller orders paid together are one checkout")
	single, cart := orders[0], orders[1]
	require.Equal(t, "01-00000-00001", cart.Number, "filed under the lowest seller order number")
	require.Equal(t, "100.00", cart.Total.String())
	require.Equal(t, "10.00", cart.Tax.String())
	require.Equal(t, "4.00", cart.Shipping.String())
	require.Len(t, cart.Items, 2)
	require.Equal(t, "02-00000-00003", single.Number)

	charges := map[string]merchantimport.Charge{}
	for _, charge := range result.Parsed.Charges {
		charges[charge.OrderNumber] = charge
	}
	require.Len(t, charges, 2)
	require.Equal(t, "-100.00", charges[cart.Number].Amount.String(), "the cart is charged once, in full")
	require.Equal(t, "PayPal", charges[cart.Number].Instrument)
	require.Equal(t, "Card", charges[single.Number].Instrument)
	require.False(t, domain.IsGiftCardInstrument(charges[single.Number].Instrument))
}

func TestAnEbayPullLaysOutTheOrderAndEachListingWithoutTheSeller(t *testing.T) {
	fake := newEbayFake(t)
	session := ebaySession(t, provider.EbaySession{Kind: provider.EbaySessionKind, RefreshToken: "r",
		AccessToken: "a", AccessExpiresAt: ebayPullAt.Add(time.Hour)})
	call := ebayCall(fake, session)
	call.Invoiced["02-00000-00003"] = true

	result, err := Ebay(ebayTestApp).Fetch(call)
	require.NoError(t, err)
	require.Empty(t, result.StorageState, "an access token with time left is not rewritten")

	names := map[string]string{}
	for _, invoice := range result.Invoices {
		require.Equal(t, "01-00000-00001", invoice.OrderID, "a checkout already filed is laid out again by nothing")
		names[invoice.Filename] = invoice.HTML
	}
	require.Len(t, names, 3, "the order and its two listings")
	order := names["ebay-order-01-00000-00001.pdf"]
	require.Contains(t, order, "01-00000-00002")
	require.Contains(t, order, "Laid out by Agentifi")

	listing := names["ebay-listing-100000000001.pdf"]
	require.Contains(t, listing, "Brand")
	require.Contains(t, listing, "Works.")
	require.Contains(t, listing, "data:image/jpeg;base64,", "its photo is carried inline")
	for _, document := range names {
		require.NotContains(t, document, "someone_else", "no seller is kept")
		require.NotContains(t, document, "someone@example.invalid")
		require.NotContains(t, document, "steal()", "the seller's script is not kept")
		require.NotContains(t, document, "tracker.example.invalid", "nothing in it reaches out")
	}
}

func TestAnEndedEbayConsentAsksForAConnection(t *testing.T) {
	fake := newEbayFake(t)
	fake.refused = true
	session := ebaySession(t, provider.EbaySession{Kind: provider.EbaySessionKind, RefreshToken: "withdrawn"})
	result, err := Ebay(ebayTestApp).Fetch(ebayCall(fake, session))
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Contains(t, result.Reason, "connect it again")

	sandbox := ebaySession(t, provider.EbaySession{Kind: provider.EbaySessionKind, Sandbox: true, RefreshToken: "r"})
	result, err = Ebay(ebayTestApp).Fetch(ebayCall(fake, sandbox))
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn, "a sandbox consent does not read the live marketplace")

	_, err = Ebay(provider.EbayApp{}).Fetch(ebayCall(fake, sandbox))
	require.ErrorContains(t, err, "EBAY_APP_ID")
}

func TestAnUnpaidEbayOrderIsItsOwnCheckoutWithNoCharge(t *testing.T) {
	checkouts := groupEbayCheckouts([]provider.EbayOrder{
		{OrderID: "01-00000-00001", Created: ebayPullAt, Total: domain.MustFromString("9.00"), Status: "Active"},
		{OrderID: "01-00000-00002", Created: ebayPullAt.Add(time.Second), Total: domain.MustFromString("5.00"),
			Status: "Active"},
	})
	require.Len(t, checkouts, 2)
	require.Empty(t, checkouts[0].file().Payments)
}

func TestASellersDescriptionIsKeptAsItsWords(t *testing.T) {
	require.Equal(t, "Line one\nLine two & three",
		plainDescription(`<style>p{}</style><div>Line one<br>Line   two &amp; three</div><script>x()</script>`))
	require.Equal(t, "", plainDescription(""))
}
