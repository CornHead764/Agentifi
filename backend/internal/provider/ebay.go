package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// eBay's API, through the household's own developer application: OAuth for
// the account holder's consent, and the Trading API's buyer side for their
// purchases. It sits on api.ebay.com, outside the bot manager that refuses an
// automated browser at www.ebay.com. docs/connectors/merchants.md#ebay.
//
// Nothing here keeps or returns another eBay user's identity: the seller's
// username and email come back on every order and are never read, which is
// what the application's exemption from eBay's account-deletion notices rests
// on.

// EbaySessionKind is the kind a sealed eBay session carries.
const EbaySessionKind = "ebay-oauth"

// EbayApp is one developer application's keyset.
type EbayApp struct {
	Sandbox               bool
	AppID, CertID, RuName string
}

func (a EbayApp) Set() bool { return a.AppID != "" && a.CertID != "" && a.RuName != "" }

// ebayScope is the one scope the buyer side needs.
const ebayScope = "https://api.ebay.com/oauth/api_scope"

// ebayCompatibility is the Trading API schema version the parsing below was
// written against.
const ebayCompatibility = "1331"

func (a EbayApp) authHost() string {
	if a.Sandbox {
		return "https://auth.sandbox.ebay.com"
	}
	return "https://auth.ebay.com"
}

func (a EbayApp) apiHost() string {
	if a.Sandbox {
		return "https://api.sandbox.ebay.com"
	}
	return "https://api.ebay.com"
}

// ConsentURL is eBay's page where the account holder grants the application
// read access. It ends on the page the RuName names, whose address carries
// the code ExchangeCode takes.
func (a EbayApp) ConsentURL() string {
	query := url.Values{
		"client_id": {a.AppID}, "response_type": {"code"}, "redirect_uri": {a.RuName},
		"scope": {ebayScope}, "prompt": {"login"},
	}
	return a.authHost() + "/oauth2/authorize?" + query.Encode()
}

// EbaySession is a connected account's consent, sealed onto it as its
// session. The refresh token is the consent; the access token is a two-hour
// copy kept so a pull does not refresh it every time.
type EbaySession struct {
	Kind             string    `json:"kind"`
	Sandbox          bool      `json:"sandbox"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	AccessToken      string    `json:"access_token,omitempty"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
}

// ErrEbayConsentEnded is a consent eBay no longer honours: expired after its
// eighteen months, or withdrawn by the account holder.
var ErrEbayConsentEnded = errors.New("the eBay connection has ended; connect it again")

// HTTPDoer is the client the calls go through.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// EbayCodeFrom takes what the person pasted back after consenting — the
// address eBay landed them on, or the code alone — and returns the code.
func EbayCodeFrom(pasted string) (string, error) {
	pasted = strings.TrimSpace(pasted)
	if pasted == "" {
		return "", errors.New("paste the address eBay showed after you agreed")
	}
	if !strings.Contains(pasted, "://") {
		if unescaped, err := url.QueryUnescape(pasted); err == nil {
			return unescaped, nil
		}
		return pasted, nil
	}
	address, err := url.Parse(pasted)
	if err != nil {
		return "", fmt.Errorf("that address could not be read: %w", err)
	}
	query := address.Query()
	if query.Get("isAuthSuccessful") == "false" {
		return "", errors.New("eBay says the access was not granted; open the link and agree again")
	}
	code := query.Get("code")
	if code == "" {
		return "", errors.New("that address has no code in it; paste the whole address eBay showed after you agreed")
	}
	return code, nil
}

// ebayToken is the token endpoint's answer.
type ebayToken struct {
	AccessToken           string `json:"access_token"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
}

func (a EbayApp) token(ctx context.Context, client HTTPDoer, form url.Values) (ebayToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiHost()+"/identity/v1/oauth2/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return ebayToken{}, err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(a.AppID+":"+a.CertID)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return ebayToken{}, fmt.Errorf("eBay's token endpoint: %w", err)
	}
	defer resp.Body.Close()
	var answer ebayToken
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(body, &answer); err != nil {
		return ebayToken{}, fmt.Errorf("eBay's token endpoint answered %d with no token", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || answer.AccessToken == "" {
		if answer.Error == "invalid_grant" {
			return ebayToken{}, ErrEbayConsentEnded
		}
		return ebayToken{}, fmt.Errorf("eBay's token endpoint refused it (%d): %s",
			resp.StatusCode, firstOf(answer.ErrorDescription, answer.Error))
	}
	return answer, nil
}

// ExchangeCode trades the code from the consent page for a session.
func (a EbayApp) ExchangeCode(ctx context.Context, client HTTPDoer, code string, now time.Time) (EbaySession, error) {
	got, err := a.token(ctx, client, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {a.RuName},
	})
	if errors.Is(err, ErrEbayConsentEnded) {
		return EbaySession{}, errors.New("eBay refused that code: it is used once and lasts five minutes; open the link and agree again")
	}
	if err != nil {
		return EbaySession{}, err
	}
	if got.RefreshToken == "" {
		return EbaySession{}, errors.New("eBay granted no lasting access")
	}
	return EbaySession{
		Kind: EbaySessionKind, Sandbox: a.Sandbox,
		RefreshToken:     got.RefreshToken,
		RefreshExpiresAt: now.Add(time.Duration(got.RefreshTokenExpiresIn) * time.Second),
		AccessToken:      got.AccessToken,
		AccessExpiresAt:  now.Add(time.Duration(got.ExpiresIn) * time.Second),
	}, nil
}

// ebayAccessMargin is how long before it lapses an access token is replaced,
// so one cannot run out partway through a pull.
const ebayAccessMargin = 10 * time.Minute

// Fresh is the session with an access token good for a pull, and whether it
// changed and should be kept.
func (a EbayApp) Fresh(ctx context.Context, client HTTPDoer, s EbaySession, now time.Time) (EbaySession, bool, error) {
	if s.AccessToken != "" && now.Add(ebayAccessMargin).Before(s.AccessExpiresAt) {
		return s, false, nil
	}
	if !s.RefreshExpiresAt.IsZero() && now.After(s.RefreshExpiresAt) {
		return s, false, ErrEbayConsentEnded
	}
	got, err := a.token(ctx, client, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {s.RefreshToken}, "scope": {ebayScope},
	})
	if err != nil {
		return s, false, err
	}
	s.AccessToken = got.AccessToken
	s.AccessExpiresAt = now.Add(time.Duration(got.ExpiresIn) * time.Second)
	return s, true, nil
}

// --- The Trading API --------------------------------------------------------------

type ebayAck struct {
	Ack    string `xml:"Ack"`
	Errors []struct {
		Code     string `xml:"ErrorCode"`
		Short    string `xml:"ShortMessage"`
		Long     string `xml:"LongMessage"`
		Severity string `xml:"SeverityCode"`
	} `xml:"Errors"`
}

// ebayAuthErrors are the Trading API's codes for a token it will not take.
var ebayAuthErrors = map[string]bool{"931": true, "932": true, "21916984": true, "21917053": true}

func (r ebayAck) err(call string) error {
	if r.Ack == "Success" || r.Ack == "Warning" {
		return nil
	}
	for _, one := range r.Errors {
		if one.Severity != "Error" {
			continue
		}
		if ebayAuthErrors[one.Code] {
			return ErrEbayConsentEnded
		}
		return fmt.Errorf("eBay's %s refused (%s): %s", call, one.Code, firstOf(one.Long, one.Short))
	}
	return fmt.Errorf("eBay's %s answered %q", call, r.Ack)
}

func (a EbayApp) trading(ctx context.Context, client HTTPDoer, s EbaySession, call, body string, into any) error {
	payload := `<?xml version="1.0" encoding="utf-8"?><` + call + `Request xmlns="urn:ebay:apis:eBLBaseComponents">` +
		body + `</` + call + `Request>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiHost()+"/ws/api.dll", strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("X-EBAY-API-CALL-NAME", call)
	req.Header.Set("X-EBAY-API-SITEID", "0")
	req.Header.Set("X-EBAY-API-COMPATIBILITY-LEVEL", ebayCompatibility)
	req.Header.Set("X-EBAY-API-IAF-TOKEN", s.AccessToken)
	req.Header.Set("Content-Type", "text/xml")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("eBay's %s: %w", call, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("eBay's %s: %w", call, err)
	}
	var ack ebayAck
	if err := xml.Unmarshal(raw, &ack); err != nil {
		return fmt.Errorf("eBay's %s answered %d with something unreadable", call, resp.StatusCode)
	}
	if err := ack.err(call); err != nil {
		return err
	}
	return xml.Unmarshal(raw, into)
}

type ebayAmount struct {
	Value    string `xml:",chardata"`
	Currency string `xml:"currencyID,attr"`
}

func (m ebayAmount) money() domain.Money {
	value, _ := domain.ParseMoneyText(strings.TrimSpace(m.Value))
	return value
}

type ebayOrdersPage struct {
	HasMoreOrders bool `xml:"HasMoreOrders"`
	Orders        []struct {
		OrderID        string     `xml:"OrderID"`
		OrderStatus    string     `xml:"OrderStatus"`
		CreatedTime    string     `xml:"CreatedTime"`
		PaidTime       string     `xml:"PaidTime"`
		Total          ebayAmount `xml:"Total"`
		AmountPaid     ebayAmount `xml:"AmountPaid"`
		CheckoutStatus struct {
			PaymentMethod string `xml:"PaymentMethod"`
		} `xml:"CheckoutStatus"`
		ShippingServiceSelected struct {
			Cost ebayAmount `xml:"ShippingServiceCost"`
		} `xml:"ShippingServiceSelected"`
		Transactions []struct {
			Item struct {
				ItemID string `xml:"ItemID"`
				Title  string `xml:"Title"`
			} `xml:"Item"`
			Quantity int        `xml:"QuantityPurchased"`
			Price    ebayAmount `xml:"TransactionPrice"`
			Tax      ebayAmount `xml:"Taxes>TotalTaxAmount"`
		} `xml:"TransactionArray>Transaction"`
	} `xml:"OrderArray>Order"`
}

// EbayOrder is one seller's order as the buyer sees it: what was bought from
// whom (never who), when, and what was paid.
type EbayOrder struct {
	OrderID, Status string
	Created, Paid   time.Time
	// Total is the order's whole cost; Paid is zero for an order not yet paid
	// for.
	Total, AmountPaid, Tax, Shipping domain.Money
	Currency, PaymentMethod          string
	Items                            []EbayOrderItem
}

type EbayOrderItem struct {
	ItemID, Title string
	Quantity      int
	// Price is one unit's.
	Price domain.Money
}

// ebayPageSize is the most orders the Trading API answers in one page.
const ebayPageSize = 100

// EbayLookback is how far back GetOrders reaches.
const EbayLookback = 90 * 24 * time.Hour

// Orders is every order the account bought between from and to, which must
// be within the last ninety days.
func (a EbayApp) Orders(ctx context.Context, client HTTPDoer, s EbaySession, from, to time.Time) ([]EbayOrder, error) {
	var out []EbayOrder
	for page := 1; page <= 50; page++ {
		var answer ebayOrdersPage
		body := fmt.Sprintf(`<OrderRole>Buyer</OrderRole><CreateTimeFrom>%s</CreateTimeFrom>`+
			`<CreateTimeTo>%s</CreateTimeTo><DetailLevel>ReturnAll</DetailLevel>`+
			`<Pagination><EntriesPerPage>%d</EntriesPerPage><PageNumber>%d</PageNumber></Pagination>`,
			from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), ebayPageSize, page)
		if err := a.trading(ctx, client, s, "GetOrders", body, &answer); err != nil {
			return nil, err
		}
		for _, raw := range answer.Orders {
			order := EbayOrder{
				OrderID: strings.TrimSpace(raw.OrderID), Status: raw.OrderStatus,
				Total: raw.Total.money(), AmountPaid: raw.AmountPaid.money(),
				Shipping: raw.ShippingServiceSelected.Cost.money(),
				Currency: firstOf(raw.Total.Currency, "USD"), PaymentMethod: raw.CheckoutStatus.PaymentMethod,
			}
			order.Created, _ = time.Parse(time.RFC3339, raw.CreatedTime)
			order.Paid, _ = time.Parse(time.RFC3339, raw.PaidTime)
			for _, tx := range raw.Transactions {
				order.Tax = order.Tax.Add(tx.Tax.money())
				order.Items = append(order.Items, EbayOrderItem{
					ItemID: strings.TrimSpace(tx.Item.ItemID), Title: strings.TrimSpace(tx.Item.Title),
					Quantity: max(tx.Quantity, 1), Price: tx.Price.money(),
				})
			}
			out = append(out, order)
		}
		if !answer.HasMoreOrders {
			return out, nil
		}
	}
	return out, nil
}

// EbayListing is what a listing showed: never its seller.
type EbayListing struct {
	ItemID, Title, Condition, Category, URL string
	// Description is the seller's own HTML.
	Description string
	Specifics   []EbaySpecific
	Pictures    []string
	Price       domain.Money
	Currency    string
}

type EbaySpecific struct {
	Name   string
	Values []string
}

type ebayItemAnswer struct {
	Item struct {
		ItemID          string `xml:"ItemID"`
		Title           string `xml:"Title"`
		Description     string `xml:"Description"`
		Condition       string `xml:"ConditionDisplayName"`
		PrimaryCategory struct {
			Name string `xml:"CategoryName"`
		} `xml:"PrimaryCategory"`
		ListingDetails struct {
			URL string `xml:"ViewItemURL"`
		} `xml:"ListingDetails"`
		SellingStatus struct {
			Price ebayAmount `xml:"CurrentPrice"`
		} `xml:"SellingStatus"`
		Pictures  []string `xml:"PictureDetails>PictureURL"`
		Specifics []struct {
			Name   string   `xml:"Name"`
			Values []string `xml:"Value"`
		} `xml:"ItemSpecifics>NameValueList"`
	} `xml:"Item"`
}

// Item is one listing as eBay keeps it, which it does for a while after it
// ends.
func (a EbayApp) Item(ctx context.Context, client HTTPDoer, s EbaySession, itemID string) (EbayListing, error) {
	var answer ebayItemAnswer
	var body bytes.Buffer
	body.WriteString("<ItemID>")
	if err := xml.EscapeText(&body, []byte(itemID)); err != nil {
		return EbayListing{}, err
	}
	body.WriteString("</ItemID><DetailLevel>ReturnAll</DetailLevel><IncludeItemSpecifics>true</IncludeItemSpecifics>")
	if err := a.trading(ctx, client, s, "GetItem", body.String(), &answer); err != nil {
		return EbayListing{}, err
	}
	item := answer.Item
	out := EbayListing{
		ItemID: item.ItemID, Title: strings.TrimSpace(item.Title), Condition: item.Condition,
		Category: item.PrimaryCategory.Name, URL: item.ListingDetails.URL, Description: item.Description,
		Pictures: item.Pictures, Price: item.SellingStatus.Price.money(),
		Currency: firstOf(item.SellingStatus.Price.Currency, "USD"),
	}
	for _, specific := range item.Specifics {
		out.Specifics = append(out.Specifics, EbaySpecific{Name: specific.Name, Values: specific.Values})
	}
	return out, nil
}

// ebayPictureLimit is the largest picture kept in a listing's document.
const ebayPictureLimit = 4 << 20

// Picture fetches one of a listing's pictures from eBay's image host, and says
// what kind it is.
func (a EbayApp) Picture(ctx context.Context, client HTTPDoer, address string) ([]byte, string, error) {
	link, err := url.Parse(address)
	if err != nil || !ebayPictureHost(link) {
		return nil, "", fmt.Errorf("not one of eBay's pictures: %q", address)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, "", err
	}
	// A listing's addresses are the seller's to influence, so a redirect is
	// followed only to eBay's image host too.
	if plain, ok := client.(*http.Client); ok {
		guarded := *plain
		guarded.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 || !ebayPictureHost(next.URL) {
				return fmt.Errorf("a picture redirected away from eBay's image host: %q", next.URL.Host)
			}
			return nil
		}
		client = &guarded
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("the picture answered %d", resp.StatusCode)
	}
	picture, err := io.ReadAll(io.LimitReader(resp.Body, ebayPictureLimit+1))
	if err != nil {
		return nil, "", err
	}
	if len(picture) > ebayPictureLimit {
		return nil, "", errors.New("the picture is too large to keep")
	}
	kind := http.DetectContentType(picture)
	if !strings.HasPrefix(kind, "image/") {
		return nil, "", fmt.Errorf("the picture is %s, not an image", kind)
	}
	return picture, kind, nil
}

// ebayPictureHost says an address is on eBay's image host, ebayimg.com or a
// name under it, over https.
func ebayPictureHost(link *url.URL) bool {
	host := strings.TrimSuffix(strings.ToLower(link.Hostname()), ".")
	return link.Scheme == "https" && (host == "ebayimg.com" || strings.HasSuffix(host, ".ebayimg.com"))
}

func firstOf(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
