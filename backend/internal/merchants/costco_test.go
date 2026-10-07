package merchants

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/stretchr/testify/require"
)

var (
	_ browser.FirefoxOriginProvider = (*costcoModule)(nil)
	_ PageSessionModule             = (*costcoModule)(nil)
)

// What the Costco module makes of the JSON the site answers with.
//
// The fixtures under testdata are in the site's own shapes with every value
// invented.

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, httpx.DecodeJSON(raw, &out))
	return out
}

// receiptsFixture is the four receipts of the fixture, in its own order:
// a warehouse purchase, a gas fill, a return, and one from 2024.
func receiptsFixture(t *testing.T) (map[string]any, []receipt) {
	t.Helper()
	body := fixture(t, "costco-receipts.json")
	found := &harvested{}
	harvest(body, found, 0)
	require.Len(t, found.receipts, 4)
	return body, found.receipts
}

func onlineFixture(t *testing.T) (map[string]any, onlineOrder) {
	t.Helper()
	body := fixture(t, "costco-online-orders.json")
	found := &harvested{}
	harvest(body, found, 0)
	require.Len(t, found.onlineOrders, 1)
	return body, found.onlineOrders[0]
}

func TestTheModuleNamesItself(t *testing.T) {
	module := Costco()
	require.Equal(t, "costco", string(module.ID()))
	require.Equal(t, "Costco", Name(module))
	require.Equal(t, []string{"costco-b2c"}, module.SessionKinds())
}

func TestAWarehouseReceiptBecomesAnOrderOfKindWarehouse(t *testing.T) {
	_, receipts := receiptsFixture(t)
	order, _ := receiptToOrder(receipts[0])
	require.Equal(t, "warehouse", order.Kind)
	require.Equal(t, "21100123456789012345", order.OrderID)
	require.Equal(t, "2026-09-05", order.Date)
	require.Equal(t, "188.50", order.Total)
	require.Equal(t, "9.50", order.Tax)
	require.Equal(t, "Costco Springfield #0123", order.Location)
	require.Equal(t, "0.00", order.GiftCard)
	require.Equal(t, "USD", order.Currency)
}

func TestTheItemsCarryTheUnitPriceTheLineTotalAndTheItemNumber(t *testing.T) {
	_, receipts := receiptsFixture(t)
	order, _ := receiptToOrder(receipts[0])
	eggs := order.Items[0]
	require.Equal(t, "1234567", eggs.SKU)
	require.Equal(t, "KS ORGANIC EGGS", eggs.Title)
	require.Equal(t, 2, eggs.Quantity)
	require.Equal(t, "8.00", eggs.Price)
	require.Equal(t, "16.00", eggs.Total)
	// Both description lines, when there are two.
	require.Equal(t, "KS ROTISSERIE CHICKEN", order.Items[1].Title)
}

func TestAnInstantSavingsLineStaysOnTheOrderAsANegativeItem(t *testing.T) {
	_, receipts := receiptsFixture(t)
	order, _ := receiptToOrder(receipts[0])
	discount := order.Items[len(order.Items)-1]
	require.Equal(t, "TPD/1234567", discount.Title)
	require.Equal(t, "-2.00", discount.Total)
	require.Equal(t, "-2.00", discount.Price)
}

func TestACardTenderOnAPurchaseIsAChargeTheBankSawAsMoneyOut(t *testing.T) {
	_, receipts := receiptsFixture(t)
	_, charges := receiptToOrder(receipts[0])
	require.Equal(t, []merchantimport.CostcoCharge{{
		OrderID: "21100123456789012345", Date: "2026-09-05",
		Amount: "-188.50", Instrument: "VISA ••••1234",
	}}, charges)
}

func TestAReturnReceiptIsNegativeAndItsTenderIsPositive(t *testing.T) {
	_, receipts := receiptsFixture(t)
	order, charges := receiptToOrder(receipts[2])
	require.Equal(t, "-25.00", order.Total)
	require.Equal(t, "Return", order.Status)
	require.Equal(t, "-25.00", order.Items[0].Total)
	require.Equal(t, "25.00", charges[0].Amount)
}

func TestAGasReceiptIsKindFuelAndFallsBackToABuiltOrderNumber(t *testing.T) {
	_, receipts := receiptsFixture(t)
	order, charges := receiptToOrder(receipts[1])
	require.Equal(t, "fuel", order.Kind)
	require.Equal(t, "123-20260903-4567", order.OrderID)
	require.Equal(t, "Costco Shop Card", charges[0].Instrument)
	require.Equal(t, "-48.00", charges[0].Amount)
}

func TestAShopCardCashAndACheckAreNamedSoTheBankIsNotLookedFor(t *testing.T) {
	require.Equal(t, "Costco Shop Card", tenderInstrument(tender{TenderDescription: "COSTCO SHOP CARD"}))
	require.Equal(t, "Costco Shop Card", tenderInstrument(tender{TenderDescription: "COSTCO CASH CARD"}))
	require.Equal(t, "Cash", tenderInstrument(tender{TenderDescription: "CASH"}))
	require.Equal(t, "Check", tenderInstrument(tender{TenderDescription: "CHECK"}))
	require.Equal(t, "MASTERCARD ••••9999", tenderInstrument(tender{
		TenderDescription: "MASTERCARD", DisplayAccountNumber: "************9999",
	}))
	require.Equal(t, "DEBIT", tenderInstrument(tender{TenderDescription: "DEBIT"}))
}

func TestAnOnlineOrderKeepsItsNumberItsLinkAndItsLines(t *testing.T) {
	_, raw := onlineFixture(t)
	order, charges := onlineOrderToOrder(raw)
	require.Equal(t, "online", order.Kind)
	require.Equal(t, "1234567890", order.OrderID)
	require.Equal(t, "2026-09-01", order.Date)
	require.Equal(t, "325.00", order.Total)
	require.Equal(t, "25.00", order.Tax)
	require.Equal(t, "", order.Location)
	// Unknown, not zero: an online order's gift card share is not on this page.
	require.Equal(t, "", order.GiftCard)
	require.True(t, strings.HasSuffix(order.URL, "OrderStatusDetailsCmd?orderNumber=1234567890"), order.URL)
	require.Equal(t, []merchantimport.CostcoItem{{
		SKU: "4000123456", Title: "Countertop Blender",
		Quantity: 1, Price: "300.00", Total: "300.00",
	}}, order.Items)
	require.Equal(t, []merchantimport.CostcoCharge{{
		OrderID: "1234567890", Date: "2026-09-02", Amount: "-325.00", Instrument: "Visa ••••1234",
	}}, charges)
}

func TestAnOnlineOrderWithNoPaymentLinesHasNoCharges(t *testing.T) {
	_, charges := onlineOrderToOrder(onlineOrder{
		OrderNumber: "9", OrderPlacedDate: "2026-09-01", OrderTotal: "10",
		OrderLineItems: []orderLine{},
	})
	require.Empty(t, charges)
}

func TestTheTwoShapesAreFoundWhereverTheyAreNested(t *testing.T) {
	receipts, _ := receiptsFixture(t)
	orders, _ := onlineFixture(t)
	found := &harvested{}
	harvest(receipts, found, 0)
	require.Len(t, found.receipts, 4)
	require.Empty(t, found.onlineOrders)
	harvest(orders, found, 0)
	require.Len(t, found.onlineOrders, 1)
}

func TestAShapeThatMatchedNothingIsRememberedByItsKeys(t *testing.T) {
	found := &harvested{}
	harvest(map[string]any{
		"data": map[string]any{
			"orderSomethingElse": map[string]any{
				"orderList": []any{map[string]any{"orderId": "1", "itemThing": "2"}},
			},
		},
	}, found, 0)
	require.Empty(t, found.receipts)
	require.NotEmpty(t, found.shapes, "the unrecognised shapes should be remembered")
}

func TestTheExportFileIsTheContractShapeWindowedAndDeduplicated(t *testing.T) {
	receipts, _ := receiptsFixture(t)
	orders, _ := onlineFixture(t)
	found := &harvested{}
	harvest(orders, found, 0)
	harvest(receipts, found, 0)
	// The same receipts twice: the page's own call and ours.
	harvest(receipts, found, 0)

	file := buildExport(*found, "Alex", "2026-08-01", "2026-09-12T15:04:05Z")
	require.Equal(t, "agentifi-costco-extract", file.Source)
	require.Equal(t, "2026-09-12T15:04:05Z", file.ExtractedAt)
	require.Equal(t, "Alex", file.AccountHint)
	// Three receipts in the window plus one online order; the 2024 one is out
	// and the repeats are folded.
	require.Equal(t,
		[]string{"21100123456789012345", "123-20260903-4567", "21100999888777666555", "1234567890"},
		numbersOf(file.Orders))
	require.Equal(t,
		[]string{"warehouse", "fuel", "warehouse", "online"}, kindsOf(file.Orders))
	require.Len(t, file.Charges, 4)
}

func parsedNumbers(orders []merchantimport.Order) []string {
	out := make([]string, 0, len(orders))
	for _, one := range orders {
		out = append(out, one.Number)
	}
	return out
}

func numbersOf(orders []merchantimport.CostcoOrder) []string {
	out := make([]string, 0, len(orders))
	for _, one := range orders {
		out = append(out, one.OrderID)
	}
	return out
}

func kindsOf(orders []merchantimport.CostcoOrder) []string {
	out := make([]string, 0, len(orders))
	for _, one := range orders {
		out = append(out, one.Kind)
	}
	return out
}

func TestTheSmallReaders(t *testing.T) {
	require.Equal(t, "0.00", moneyString("0"))
	require.Equal(t, "1234.50", moneyString("$1,234.50"))
	require.Equal(t, "0.00", moneyString("-0"))
	require.Equal(t, "", moneyString(""))
	require.Equal(t, "", moneyString("nonsense"))
	require.Equal(t, "2026-09-05", dayOf("2026-09-05T14:22:11.000"))
	require.Equal(t, "2026-09-05", dayOf("09/05/2026"))
	require.Equal(t, "", dayOf(""))
	require.Equal(t, "fuel", receiptKind(receipt{DocumentType: "GasReceipt"}))
	require.Equal(t, "warehouse", receiptKind(receipt{ReceiptType: "Warehouse"}))
	require.Equal(t, "Costco Springfield #0123",
		warehouseLabel(receipt{WarehouseName: "Costco Springfield", WarehouseNumber: "123"}))
	require.Equal(t, "Costco", warehouseLabel(receipt{}))
	require.Equal(t, "42", receiptID(receipt{TransactionBarcode: " 42 "}))
	// The page's own date form, which the endpoint answers a backend 404 to an
	// ISO date for.
	require.Equal(t, "4/01/2026", costcoDate("2026-04-01"))
}

func TestAReceiptWantsDetailUntilEveryLineHasADescription(t *testing.T) {
	_, receipts := receiptsFixture(t)
	require.False(t, needsDetail(receipts[0]))
	require.True(t, needsDetail(thin(receipts[0])))
	require.True(t, needsDetail(receipt{
		TransactionBarcode: "2110", TotalItemCount: "3", ItemArray: []receiptLine{},
	}))
	require.False(t, needsDetail(receipt{ItemArray: []receiptLine{
		{ItemNumber: "1", FuelGradeDescription: "Regular"},
	}}))
	require.Equal(t, "fuel", detailDocumentType(receipt{DocumentType: "GasReceipt"}))
	require.Equal(t, "warehouse", detailDocumentType(receipt{DocumentType: "WarehouseReceiptDetail"}))
}

func TestTheDetailAnswerIsFoundOrSaidToBeMissing(t *testing.T) {
	receipts, _ := receiptsFixture(t)
	listing := receipts["data"].(map[string]any)["receiptsWithCounts"].(map[string]any)["receipts"].([]any)
	one, ok := detailOf(map[string]any{
		"data": map[string]any{"receiptsWithCounts": map[string]any{"receipts": []any{listing[0]}}},
	})
	require.True(t, ok)
	require.Equal(t, "21100123456789012345", one.TransactionBarcode)

	_, ok = detailOf(map[string]any{
		"data": map[string]any{"receiptsWithCounts": map[string]any{"receipts": []any{}}},
	})
	require.False(t, ok)
	_, ok = detailOf(map[string]any{"errors": []any{map[string]any{"message": "no"}}})
	require.False(t, ok)
}

// --- The handed-over session, with no browser ------------------------------------

// scripted is a caller that answers from a script, one entry per call, and
// remembers what it was asked. It refuses a call the script did not foresee,
// so a test also proves how many calls were made.
type scripted struct {
	t     *testing.T
	steps []func(req *http.Request, body string) (*http.Response, error)
	calls []recorded
}

type recorded struct {
	URL     string
	Method  string
	Headers http.Header
	Body    string
}

func (s *scripted) Do(req *http.Request) (*http.Response, error) {
	body := ""
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		require.NoError(s.t, err)
		body = string(raw)
	}
	s.calls = append(s.calls, recorded{
		URL: req.URL.String(), Method: req.Method, Headers: req.Header.Clone(), Body: body,
	})
	if len(s.steps) == 0 {
		return nil, fmt.Errorf("unexpected call to %s", req.URL)
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step(req, body)
}

func answers(status int, body any) func(*http.Request, string) (*http.Response, error) {
	return func(*http.Request, string) (*http.Response, error) {
		return jsonResponse(status, body), nil
	}
}

func jsonResponse(status int, body any) *http.Response {
	var encoded []byte
	switch typed := body.(type) {
	case string:
		encoded = []byte(typed)
	default:
		encoded, _ = json.Marshal(typed)
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(string(encoded))),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func fails(err error) func(*http.Request, string) (*http.Response, error) {
	return func(*http.Request, string) (*http.Response, error) { return nil, err }
}

// session is the shape a Costco sign-in keeps, invented.
func handedOver() json.RawMessage {
	return json.RawMessage(`{
		"kind": "costco-b2c",
		"tenant": "e0714dd4-784d-46d6-a278-3e29553483eb",
		"policy": "B2C_1A_SSO_WCS_signup_signin_209",
		"client_id": "a3a5186b-7c89-4b4c-93a8-dd604e930757",
		"refresh_token": "old-refresh",
		"id_token": "stale-id",
		"azure_token": "stale-azure",
		"account": {"username": "alex@example.test", "name": "Alex", "home_account_id": "h"},
		"handed_over_at": "2026-09-12T18:40:00Z"
	}`)
}

func callWith(t *testing.T, caller *scripted, days int) (Call, *Notes) {
	t.Helper()
	notes := &Notes{}
	return Call{
		Ctx: context.Background(), Session: handedOver(), SinceDays: days,
		Notes: notes, HTTP: caller,
		Now: func() time.Time { return time.Date(2026, 9, 12, 15, 4, 5, 0, time.UTC) },
	}, notes
}

func bearerOf(call recorded) string {
	return strings.TrimPrefix(call.Headers.Get("authorization"), "Bearer ")
}

func TestAHandedOverSessionRefreshesAsksAndRotatesTheRefreshToken(t *testing.T) {
	receipts, _ := receiptsFixture(t)
	orders, _ := onlineFixture(t)
	caller := &scripted{t: t, steps: []func(*http.Request, string) (*http.Response, error){
		answers(200, map[string]any{
			"id_token": "fresh-id", "access_token": "fresh-access",
			"refresh_token": "new-refresh", "expires_in": 900,
		}),
		// A list that already carries descriptions wants no detail.
		answers(200, receipts),
		answers(200, orders),
	}}
	call, notes := callWith(t, caller, 400)
	result, err := Costco().Fetch(call)
	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Len(t, caller.calls, 3, "one refresh, one receipts query, one page of online orders")

	// The refresh: form-encoded, to the session's own tenant and policy.
	refresh := caller.calls[0]
	require.Contains(t, refresh.URL, "signin.costco.com")
	require.Contains(t, refresh.URL,
		"e0714dd4-784d-46d6-a278-3e29553483eb/B2C_1A_SSO_WCS_signup_signin_209/oauth2/v2.0/token")
	require.Equal(t, http.MethodPost, refresh.Method)
	require.Equal(t, "application/x-www-form-urlencoded", refresh.Headers.Get("Content-Type"))
	form, err := url.ParseQuery(refresh.Body)
	require.NoError(t, err)
	require.Equal(t, "refresh_token", form.Get("grant_type"))
	require.Equal(t, "a3a5186b-7c89-4b4c-93a8-dd604e930757", form.Get("client_id"))
	require.Equal(t, "old-refresh", form.Get("refresh_token"))
	require.Equal(t, "openid offline_access a3a5186b-7c89-4b4c-93a8-dd604e930757", form.Get("scope"))

	// The queries: the page's own headers — the routing three included — and
	// the fresh id token under both names.
	for _, one := range caller.calls[1:] {
		require.Contains(t, one.URL, "ecom-api.costco.com")
		require.Equal(t, "application/json-patch+json", one.Headers.Get("Content-Type"))
		require.Equal(t, "481b1aec-aa3b-454b-b81b-48187e28f205", one.Headers.Get("client-identifier"))
		require.Equal(t, "restOrders", one.Headers.Get("costco.service"))
		require.Equal(t, "ecom", one.Headers.Get("costco.env"))
		require.Equal(t, "4900eb1f-0c10-4bd9-99c3-c59e6c1ecebf", one.Headers.Get("costco-x-wcs-clientId"))
		require.Contains(t, one.Headers.Get("User-Agent"), "Chrome")
		require.Equal(t, "fresh-id", bearerOf(one))
		require.Equal(t, "Bearer fresh-id", one.Headers.Get("costco-x-authorization"))
	}
	asked := askedOf(t, caller.calls[1])
	require.Contains(t, asked.Query, "receiptsWithCounts")
	require.Equal(t, "all", asked.Variables["documentType"])
	// The page's own date form, and the other query's the other way round.
	require.Regexp(t, `^\d{1,2}/\d{2}/\d{4}$`, asked.Variables["startDate"])
	online := askedOf(t, caller.calls[2])
	require.Contains(t, online.Query, "getOnlineOrders")
	require.Regexp(t, `warehouseNumber\s*:\s*\$warehouseNumber`, online.Query)
	require.EqualValues(t, "1", asString(online.Variables["pageNumber"]))
	require.Equal(t, "847", online.Variables["warehouseNumber"])
	require.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, online.Variables["startDate"])
	require.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, online.Variables["endDate"])

	// What was read is the mapping the tests above pin, from the same fixtures.
	require.NotNil(t, result.Parsed)
	require.Equal(t, "Alex", result.Parsed.AccountHint)
	require.Equal(t, "Alex", result.AccountHint)
	require.Equal(t,
		[]string{"21100123456789012345", "123-20260903-4567", "21100999888777666555", "1234567890"},
		parsedNumbers(result.Parsed.Orders))
	require.Equal(t, 4, result.Orders)
	// The detailed receipts come back laid out for printing, at no extra call.
	require.Len(t, result.Invoices, 2)
	require.Equal(t, "21100123456789012345", result.Invoices[0].OrderID)
	require.NotEmpty(t, result.Invoices[0].HTML)
	require.Equal(t, 4, result.Charges)
	require.Len(t, result.Parsed.Charges, 4)

	// The session comes back rotated, and otherwise as it was.
	var kept map[string]any
	require.NoError(t, json.Unmarshal(result.StorageState, &kept))
	require.Equal(t, "costco-b2c", kept["kind"])
	require.Equal(t, "new-refresh", kept["refresh_token"])
	require.Equal(t, "fresh-id", kept["id_token"])
	require.Equal(t, "stale-azure", kept["azure_token"])
	require.Equal(t, "2026-09-12T18:40:00Z", kept["handed_over_at"])
	require.Equal(t, "2026-09-12T15:04:05Z", kept["refreshed_at"])

	// How the tokens went is for the log; a pull that read everything has
	// nothing to tell the household beyond its counts.
	require.Empty(t, notes.List())
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "accepted the id token")
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "a new refresh token")
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "4 purchases and 4 charges")
}

type askedQuery struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func askedOf(t *testing.T, call recorded) askedQuery {
	t.Helper()
	var out askedQuery
	require.NoError(t, httpx.DecodeJSON([]byte(call.Body), &out))
	return out
}

func asString(value any) string { return text(value) }

func TestAnInvalidGrantMeansTheHouseholdSignsInAgainInB2CsOwnWords(t *testing.T) {
	caller := &scripted{t: t, steps: []func(*http.Request, string) (*http.Response, error){
		answers(400, map[string]any{
			"error": "invalid_grant",
			"error_description": "AADB2C90080: The provided grant has expired. Please re-authenticate " +
				"and try again.\r\nCorrelation ID: abc\r\nTimestamp: 2026-09-12 18:40:00Z\r\n",
		}),
	}}
	call, notes := callWith(t, caller, 30)
	result, err := Costco().Fetch(call)
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t,
		"AADB2C90080: The provided grant has expired. Please re-authenticate and try again.",
		result.Reason)
	require.Len(t, caller.calls, 1, "no purchases are asked for without a token")
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "invalid_grant")
}

func TestA401OnTheIDTokenIsFollowedByTheAccessTokenWhichIsThenUsedThroughout(t *testing.T) {
	receipts, _ := receiptsFixture(t)
	orders, _ := onlineFixture(t)
	caller := &scripted{t: t, steps: []func(*http.Request, string) (*http.Response, error){
		answers(200, map[string]any{
			"id_token": "fresh-id", "access_token": "fresh-access", "refresh_token": "new-refresh",
		}),
		answers(401, map[string]any{"message": "Unauthorized"}),
		answers(200, thinList(t, receipts)),
		answers(404, "no such query"),
		answers(200, orders),
	}}
	call, notes := callWith(t, caller, 400)
	result, err := Costco().Fetch(call)
	require.NoError(t, err)
	require.Len(t, caller.calls, 5)
	require.Equal(t, "fresh-id", bearerOf(caller.calls[1]))
	require.Equal(t, "fresh-access", bearerOf(caller.calls[2]))
	require.Equal(t, "fresh-access", bearerOf(caller.calls[3]),
		"the accepted token is not tried again from scratch")
	require.Equal(t, "fresh-access", bearerOf(caller.calls[4]))
	require.Equal(t, 4, result.Orders)
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "accepted the access token")
}

func TestEachReceiptsDetailReplacesTheListsNumberOnlyLines(t *testing.T) {
	receipts, parsed := receiptsFixture(t)
	steps := []func(*http.Request, string) (*http.Response, error){
		answers(200, map[string]any{
			"id_token": "fresh-id", "access_token": "fresh-access", "refresh_token": "new-refresh",
		}),
		answers(200, thinList(t, receipts)),
	}
	barcoded := 0
	for _, one := range parsed {
		if one.TransactionBarcode != "" {
			barcoded++
			steps = append(steps, detailFor(t, receipts))
		}
	}
	orders, _ := onlineFixture(t)
	steps = append(steps, answers(200, orders))

	caller := &scripted{t: t, steps: steps}
	call, notes := callWith(t, caller, 400)
	result, err := Costco().Fetch(call)
	require.NoError(t, err)
	require.Len(t, caller.calls, 3+barcoded, "a receipt with no barcode cannot be asked about")

	// The detail query is the page's own: receiptsWithCounts by barcode, with
	// the document type the list gave the receipt.
	for _, one := range caller.calls[2 : 2+barcoded] {
		asked := askedOf(t, one)
		require.Contains(t, asked.Query, "receiptsWithCounts(barcode: $barcode")
		require.Equal(t, "warehouse", asked.Variables["documentType"])
	}

	require.NotNil(t, result.Parsed)
	first, _ := receiptToOrder(parsed[0])
	want, err := merchantimport.FromCostco(merchantimport.CostcoFile{
		Source: merchantimport.CostcoSource, Orders: []merchantimport.CostcoOrder{first},
	})
	require.NoError(t, err)
	require.Equal(t, want.Orders[0].Number, result.Parsed.Orders[0].Number)
	require.Equal(t, want.Orders[0].Total, result.Parsed.Orders[0].Total)
	require.Equal(t, want.Orders[0].Items, result.Parsed.Orders[0].Items,
		"the detail restored the descriptions and amounts")
	for _, order := range result.Parsed.Orders {
		for _, item := range order.Items {
			require.NotRegexp(t, `^Item \d+$`, item.Title, "every barcoded receipt has its descriptions")
		}
	}
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "receipt details: 3")
}

func TestARefusedDetailQueryKeepsTheNumberOnlyLinesAndAsksNoFurther(t *testing.T) {
	receipts, parsed := receiptsFixture(t)
	orders, _ := onlineFixture(t)
	caller := &scripted{t: t, steps: []func(*http.Request, string) (*http.Response, error){
		answers(200, map[string]any{
			"id_token": "fresh-id", "access_token": "fresh-access", "refresh_token": "new-refresh",
		}),
		answers(200, thinList(t, receipts)),
		answers(404, map[string]any{
			"context": map[string]any{
				"statusMessage": map[string]any{"statusCode": "APIGATEWAY.BACKEND.FAULT"},
			},
		}),
		answers(200, orders),
	}}
	call, notes := callWith(t, caller, 400)
	result, err := Costco().Fetch(call)
	require.NoError(t, err)
	require.Len(t, caller.calls, 4, "one refresh, the list, one refused detail, one page of online orders")
	require.Equal(t, parsed[0].TransactionBarcode, askedOf(t, caller.calls[2]).Variables["barcode"])
	require.Contains(t, strings.Join(notes.List(), "\n"), "items are by number only")
	require.Equal(t, 4, result.Orders, "the receipts still match by their tenders and totals")

	require.NotNil(t, result.Parsed)
	require.Regexp(t, `^Item \d+$`, result.Parsed.Orders[0].Items[0].Title)
}

func TestARefusalOfEveryTokenIsAFailedPullNotASignInTheHouseholdOwes(t *testing.T) {
	caller := &scripted{t: t, steps: []func(*http.Request, string) (*http.Response, error){
		answers(200, map[string]any{
			"id_token": "fresh-id", "access_token": "fresh-access", "refresh_token": "new-refresh",
		}),
		answers(401, map[string]any{"message": "Unauthorized"}),
		answers(403, "Forbidden"),
	}}
	call, _ := callWith(t, caller, 30)
	_, err := Costco().Fetch(call)
	require.Error(t, err)
	require.Contains(t, err.Error(), "refused every token")
	require.Contains(t, err.Error(), "the id token: HTTP 401")
	require.Contains(t, err.Error(), "the access token: HTTP 403")
}

func TestATokenEndpointThatIsDownOrUnwellFailsWithTheStatusAndAnExcerpt(t *testing.T) {
	offline := &scripted{t: t, steps: []func(*http.Request, string) (*http.Response, error){
		fails(fmt.Errorf("connection refused")),
	}}
	call, _ := callWith(t, offline, 30)
	_, err := Costco().Fetch(call)
	require.ErrorContains(t, err, "token endpoint could not be reached")

	unwell := &scripted{t: t, steps: []func(*http.Request, string) (*http.Response, error){
		answers(503, "<html>Service Unavailable</html>"),
	}}
	call, _ = callWith(t, unwell, 30)
	_, err = Costco().Fetch(call)
	require.ErrorContains(t, err, "token endpoint answered HTTP 503: <html>Service Unavailable</html>")
}

func TestAnAnswerWithNothingThatLooksLikeAPurchaseIsNotedByItsShapes(t *testing.T) {
	caller := &scripted{t: t, steps: []func(*http.Request, string) (*http.Response, error){
		answers(200, map[string]any{"id_token": "fresh-id", "refresh_token": "new-refresh"}),
		answers(200, map[string]any{"data": map[string]any{
			"receiptsWithCounts": map[string]any{
				"receiptList": []any{map[string]any{"receiptDate": "2026-09-01", "lines": []any{}}},
			},
		}}),
		answers(200, map[string]any{"data": map[string]any{
			"getOnlineOrders": map[string]any{"orders": []any{}, "recordCount": 0},
		}}),
	}}
	call, notes := callWith(t, caller, 30)
	result, err := Costco().Fetch(call)
	require.NoError(t, err)
	require.Equal(t, 0, result.Orders)

	var kept map[string]any
	require.NoError(t, json.Unmarshal(result.StorageState, &kept))
	require.Equal(t, "new-refresh", kept["refresh_token"])

	require.Equal(t, []string{"no Costco purchases found since 2026-08-13"}, notes.List())
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "receiptList")
}

// thinList is the list as the page asks it: every item by number only. A
// receipt with no barcode cannot be asked for its detail, so it stays as it is.
func thinList(t *testing.T, receipts map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(receipts)
	require.NoError(t, err)
	var copied map[string]any
	require.NoError(t, httpx.DecodeJSON(encoded, &copied))
	listing := copied["data"].(map[string]any)["receiptsWithCounts"].(map[string]any)["receipts"].([]any)
	for _, entry := range listing {
		one := entry.(map[string]any)
		if text(one["transactionBarcode"]) == "" {
			continue
		}
		var thinned []any
		for _, line := range one["itemArray"].([]any) {
			thinned = append(thinned, map[string]any{"itemNumber": line.(map[string]any)["itemNumber"]})
		}
		one["itemArray"] = thinned
	}
	return copied
}

// thin is one receipt with its descriptions taken off.
func thin(one receipt) receipt {
	out := one
	out.ItemArray = nil
	for _, line := range one.lines() {
		out.ItemArray = append(out.ItemArray, receiptLine{ItemNumber: line.ItemNumber})
	}
	return out
}

// detailFor answers the detail of whichever barcode was asked about.
func detailFor(t *testing.T, receipts map[string]any) func(*http.Request, string) (*http.Response, error) {
	t.Helper()
	return func(_ *http.Request, body string) (*http.Response, error) {
		var asked askedQuery
		require.NoError(t, httpx.DecodeJSON([]byte(body), &asked))
		listing := receipts["data"].(map[string]any)["receiptsWithCounts"].(map[string]any)["receipts"].([]any)
		var found []any
		for _, entry := range listing {
			if text(entry.(map[string]any)["transactionBarcode"]) == asked.Variables["barcode"] {
				found = append(found, entry)
			}
		}
		return jsonResponse(200, map[string]any{
			"data": map[string]any{"receiptsWithCounts": map[string]any{"receipts": found}},
		}), nil
	}
}
