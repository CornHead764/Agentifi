package provider

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// eBay's OAuth and Trading API against a server answering what eBay answers.
// The XML is in eBay's shape; every order, item, figure and token is invented.

// toServer sends every request to the test server, whatever host it named,
// and keeps what was asked.
type toServer struct {
	server *httptest.Server
	asked  []*http.Request
	bodies []string
}

func (c *toServer) Do(req *http.Request) (*http.Response, error) {
	body := ""
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		body = string(raw)
		req.Body = io.NopCloser(strings.NewReader(body))
	}
	c.asked = append(c.asked, req)
	c.bodies = append(c.bodies, body)
	target, _ := url.Parse(c.server.URL)
	sent := req.Clone(req.Context())
	sent.URL.Scheme, sent.URL.Host, sent.Host = target.Scheme, target.Host, target.Host
	sent.RequestURI = ""
	return c.server.Client().Do(sent)
}

func ebayServer(t *testing.T, handle http.HandlerFunc) *toServer {
	t.Helper()
	server := httptest.NewServer(handle)
	t.Cleanup(server.Close)
	return &toServer{server: server}
}

var invented = EbayApp{AppID: "Example-Agentifi-PRD-000000000-00000000", CertID: "PRD-invented-cert",
	RuName: "Example-Example-Agenti-abcdefg"}

func TestTheConsentPageAsksForTheBuyerScopeThroughTheRuName(t *testing.T) {
	consent, err := url.Parse(invented.ConsentURL())
	require.NoError(t, err)
	require.Equal(t, "auth.ebay.com", consent.Host)
	require.Equal(t, invented.AppID, consent.Query().Get("client_id"))
	require.Equal(t, invented.RuName, consent.Query().Get("redirect_uri"))
	require.Equal(t, "https://api.ebay.com/oauth/api_scope", consent.Query().Get("scope"))

	sandbox, _ := url.Parse(EbayApp{Sandbox: true, AppID: "x", RuName: "y"}.ConsentURL())
	require.Equal(t, "auth.sandbox.ebay.com", sandbox.Host)
}

func TestTheCodeIsTakenFromTheAddressOrOnItsOwn(t *testing.T) {
	code, err := EbayCodeFrom("https://auth2.ebay.com/oauth2/ThirdPartyAuthSucessFailure?isAuthSuccessful=true" +
		"&code=v%5E1.1%23i%5E1%23invented&expires_in=299")
	require.NoError(t, err)
	require.Equal(t, "v^1.1#i^1#invented", code)

	code, err = EbayCodeFrom("  v%5E1.1%23i%5E1%23invented  ")
	require.NoError(t, err)
	require.Equal(t, "v^1.1#i^1#invented", code)

	_, err = EbayCodeFrom("https://auth2.ebay.com/oauth2/ThirdPartyAuthSucessFailure?isAuthSuccessful=false")
	require.ErrorContains(t, err, "not granted")
	_, err = EbayCodeFrom("https://auth2.ebay.com/oauth2/ThirdPartyAuthSucessFailure?isAuthSuccessful=true")
	require.ErrorContains(t, err, "no code")
	_, err = EbayCodeFrom(" ")
	require.Error(t, err)
}

func TestACodeBecomesASessionAndTheSessionRefreshesItsAccess(t *testing.T) {
	client := ebayServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/identity/v1/oauth2/token", r.URL.Path)
		user, pass, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, invented.AppID, user)
		require.Equal(t, invented.CertID, pass)
		require.NoError(t, r.ParseForm())
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			require.Equal(t, "the-code", r.Form.Get("code"))
			require.Equal(t, invented.RuName, r.Form.Get("redirect_uri"))
			_, _ = io.WriteString(w, `{"access_token":"access-1","expires_in":7200,`+
				`"refresh_token":"refresh-1","refresh_token_expires_in":47304000}`)
		case "refresh_token":
			require.Equal(t, "refresh-1", r.Form.Get("refresh_token"))
			_, _ = io.WriteString(w, `{"access_token":"access-2","expires_in":7200}`)
		}
	})
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)

	session, err := invented.ExchangeCode(context.Background(), client, "the-code", now)
	require.NoError(t, err)
	require.Equal(t, EbaySessionKind, session.Kind)
	require.Equal(t, "refresh-1", session.RefreshToken)
	require.Equal(t, now.Add(47304000*time.Second), session.RefreshExpiresAt)

	same, changed, err := invented.Fresh(context.Background(), client, session, now.Add(time.Hour))
	require.NoError(t, err)
	require.False(t, changed, "an access token with time left is kept")
	require.Equal(t, "access-1", same.AccessToken)

	later, changed, err := invented.Fresh(context.Background(), client, session, now.Add(115*time.Minute))
	require.NoError(t, err)
	require.True(t, changed, "one about to lapse is replaced")
	require.Equal(t, "access-2", later.AccessToken)
	require.Equal(t, "refresh-1", later.RefreshToken)

	_, _, err = invented.Fresh(context.Background(), client, session, now.AddDate(2, 0, 0))
	require.ErrorIs(t, err, ErrEbayConsentEnded, "a consent past its eighteen months has ended")
}

func TestAWithdrawnConsentSaysSo(t *testing.T) {
	client := ebayServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"the provided authorization refresh token is invalid"}`)
	})
	_, _, err := invented.Fresh(context.Background(), client, EbaySession{RefreshToken: "gone"}, time.Now())
	require.ErrorIs(t, err, ErrEbayConsentEnded)
	_, err = invented.ExchangeCode(context.Background(), client, "stale", time.Now())
	require.ErrorContains(t, err, "used once")
}

const ebayOrdersPage1 = `<?xml version="1.0" encoding="UTF-8"?>
<GetOrdersResponse xmlns="urn:ebay:apis:eBLBaseComponents">
  <Ack>Success</Ack>
  <HasMoreOrders>true</HasMoreOrders>
  <OrderArray>
    <Order>
      <OrderID>01-00000-00001</OrderID>
      <OrderStatus>Completed</OrderStatus>
      <CheckoutStatus><PaymentMethod>PayPal</PaymentMethod><Status>Complete</Status></CheckoutStatus>
      <ShippingServiceSelected><ShippingServiceCost currencyID="USD">4.00</ShippingServiceCost></ShippingServiceSelected>
      <Total currencyID="USD">40.00</Total>
      <AmountPaid currencyID="USD">40.00</AmountPaid>
      <CreatedTime>2026-09-05T02:22:58.000Z</CreatedTime>
      <PaidTime>2026-09-05T02:22:57.892Z</PaidTime>
      <SellerUserID>someone_else</SellerUserID>
      <SellerEmail>someone@example.invalid</SellerEmail>
      <TransactionArray>
        <Transaction>
          <Item><ItemID>100000000002</ItemID><Title>Power Cable 8 Pin</Title></Item>
          <QuantityPurchased>2</QuantityPurchased>
          <TransactionPrice currencyID="USD">16.00</TransactionPrice>
          <Taxes><TotalTaxAmount currencyID="USD">4.00</TotalTaxAmount></Taxes>
        </Transaction>
      </TransactionArray>
    </Order>
  </OrderArray>
</GetOrdersResponse>`

const ebayOrdersPage2 = `<?xml version="1.0" encoding="UTF-8"?>
<GetOrdersResponse xmlns="urn:ebay:apis:eBLBaseComponents">
  <Ack>Success</Ack>
  <HasMoreOrders>false</HasMoreOrders>
  <OrderArray>
    <Order>
      <OrderID>01-00000-00002</OrderID>
      <OrderStatus>Active</OrderStatus>
      <CheckoutStatus><PaymentMethod>None</PaymentMethod><Status>Incomplete</Status></CheckoutStatus>
      <Total currencyID="USD">9.00</Total>
      <AmountPaid currencyID="USD">0.00</AmountPaid>
      <CreatedTime>2026-09-06T10:00:00.000Z</CreatedTime>
      <TransactionArray>
        <Transaction>
          <Item><ItemID>100000000003</ItemID><Title>Thermostat</Title></Item>
          <QuantityPurchased>1</QuantityPurchased>
          <TransactionPrice currencyID="USD">9.00</TransactionPrice>
        </Transaction>
      </TransactionArray>
    </Order>
  </OrderArray>
</GetOrdersResponse>`

func TestTheBuyersOrdersAreReadPageByPageWithoutTheSeller(t *testing.T) {
	client := ebayServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/ws/api.dll", r.URL.Path)
		require.Equal(t, "GetOrders", r.Header.Get("X-EBAY-API-CALL-NAME"))
		require.Equal(t, "access-1", r.Header.Get("X-EBAY-API-IAF-TOKEN"))
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), "<OrderRole>Buyer</OrderRole>")
		if strings.Contains(string(body), "<PageNumber>1</PageNumber>") {
			_, _ = io.WriteString(w, ebayOrdersPage1)
			return
		}
		_, _ = io.WriteString(w, ebayOrdersPage2)
	})
	from := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	orders, err := invented.Orders(context.Background(), client, EbaySession{AccessToken: "access-1"},
		from, from.AddDate(0, 1, 10))
	require.NoError(t, err)
	require.Len(t, orders, 2)
	require.Contains(t, client.bodies[0], "<CreateTimeFrom>2026-08-01T00:00:00Z</CreateTimeFrom>")

	paid := orders[0]
	require.Equal(t, "01-00000-00001", paid.OrderID)
	require.Equal(t, "PayPal", paid.PaymentMethod)
	require.Equal(t, "40.00", paid.AmountPaid.String())
	require.Equal(t, "4.00", paid.Tax.String())
	require.Equal(t, "4.00", paid.Shipping.String())
	require.Equal(t, time.Date(2026, time.September, 5, 2, 22, 57, 892_000_000, time.UTC), paid.Paid)
	require.Equal(t, []EbayOrderItem{{ItemID: "100000000002", Title: "Power Cable 8 Pin", Quantity: 2,
		Price: paid.Items[0].Price}}, paid.Items)
	require.Equal(t, "16.00", paid.Items[0].Price.String(), "the transaction price is one unit's")

	unpaid := orders[1]
	require.True(t, unpaid.Paid.IsZero())
	require.True(t, unpaid.AmountPaid.IsZero())
}

func TestARefusedTokenEndsTheConsentAndOtherFailuresSayWhat(t *testing.T) {
	answer := ""
	client := ebayServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, answer) })
	session := EbaySession{AccessToken: "old"}
	now := time.Now()

	answer = `<GetOrdersResponse xmlns="urn:ebay:apis:eBLBaseComponents"><Ack>Failure</Ack><Errors>` +
		`<ShortMessage>Auth token is hard expired.</ShortMessage><ErrorCode>932</ErrorCode>` +
		`<SeverityCode>Error</SeverityCode></Errors></GetOrdersResponse>`
	_, err := invented.Orders(context.Background(), client, session, now, now)
	require.ErrorIs(t, err, ErrEbayConsentEnded)

	answer = `<GetOrdersResponse xmlns="urn:ebay:apis:eBLBaseComponents"><Ack>Failure</Ack><Errors>` +
		`<ShortMessage>Invalid date range.</ShortMessage><ErrorCode>21916985</ErrorCode>` +
		`<SeverityCode>Error</SeverityCode></Errors></GetOrdersResponse>`
	_, err = invented.Orders(context.Background(), client, session, now, now)
	require.ErrorContains(t, err, "Invalid date range")
	require.NotErrorIs(t, err, ErrEbayConsentEnded)
}

func TestAListingIsReadWithoutItsSeller(t *testing.T) {
	client := ebayServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GetItem", r.Header.Get("X-EBAY-API-CALL-NAME"))
		_, _ = io.WriteString(w, `<GetItemResponse xmlns="urn:ebay:apis:eBLBaseComponents"><Ack>Success</Ack><Item>
  <ItemID>100000000002</ItemID><Title>Power Cable 8 Pin</Title>
  <Description>&lt;p&gt;Two cables.&lt;/p&gt;</Description>
  <ConditionDisplayName>New</ConditionDisplayName>
  <PrimaryCategory><CategoryName>Computers:Cables</CategoryName></PrimaryCategory>
  <ListingDetails><ViewItemURL>https://www.ebay.com/itm/100000000002</ViewItemURL></ListingDetails>
  <SellingStatus><CurrentPrice currencyID="USD">16.00</CurrentPrice></SellingStatus>
  <PictureDetails><PictureURL>https://i.ebayimg.com/images/g/a/s-l1600.jpg</PictureURL></PictureDetails>
  <ItemSpecifics><NameValueList><Name>Brand</Name><Value>Example</Value></NameValueList></ItemSpecifics>
  <Seller><UserID>someone_else</UserID><Email>someone@example.invalid</Email></Seller>
</Item></GetItemResponse>`)
	})
	listing, err := invented.Item(context.Background(), client, EbaySession{AccessToken: "a"}, "100000000002")
	require.NoError(t, err)
	require.Equal(t, "Power Cable 8 Pin", listing.Title)
	require.Equal(t, "New", listing.Condition)
	require.Equal(t, "<p>Two cables.</p>", listing.Description)
	require.Equal(t, []EbaySpecific{{Name: "Brand", Values: []string{"Example"}}}, listing.Specifics)
	require.Equal(t, []string{"https://i.ebayimg.com/images/g/a/s-l1600.jpg"}, listing.Pictures)
}

func TestOnlyEbaysOwnPicturesAreFetched(t *testing.T) {
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00invented")
	client := ebayServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(jpeg) })
	picture, kind, err := invented.Picture(context.Background(), client, "https://i.ebayimg.com/images/g/a/s-l1600.jpg")
	require.NoError(t, err)
	require.Equal(t, jpeg, picture)
	require.Equal(t, "image/jpeg", kind)

	for _, address := range []string{
		"http://i.ebayimg.com/a.jpg", "https://tracker.example.invalid/a.jpg", "::",
		"https://evilebayimg.com/a.jpg", "https://i.ebayimg.com.example.invalid/a.jpg",
	} {
		_, _, err := invented.Picture(context.Background(), client, address)
		require.Error(t, err, address)
	}
}

func TestAPictureThatRedirectsAwayFromEbayIsRefused(t *testing.T) {
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the redirect away from eBay's image host was followed")
	}))
	t.Cleanup(away.Close)
	bounce := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, away.URL+"/x.jpg", http.StatusFound)
	}))
	t.Cleanup(bounce.Close)

	// eBay's image host, served here: every request reaches the bouncing server.
	client := bounce.Client()
	transport := client.Transport.(*http.Transport).Clone()
	target := strings.TrimPrefix(bounce.URL, "https://")
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	client.Transport = transport

	_, _, err := invented.Picture(context.Background(), client, "https://i.ebayimg.com/images/g/a/s-l1600.jpg")
	require.ErrorContains(t, err, "redirected away")
}
