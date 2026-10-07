package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The Costco connector: the same engine as Amazon's, mounted under /costco,
// reading the one file Costco has, and keeping the two merchants' records
// apart — a Costco row is never offered an Amazon order, and a Costco account
// is not an Amazon account under another name.
//
// Every order, item, location and figure here is invented.

const costcoJSON = `{"source":"agentifi-costco-extract","account_hint":"Alex","orders":[
  {"order_id":"21100123456789012345","kind":"warehouse","date":"2026-09-05","total":"50.00",
   "location":"Costco Springfield #0123","tax":"9.00",
   "items":[{"sku":"1234567","title":"KS ORGANIC EGGS","quantity":2,"price":"8.00","total":"16.00"},
            {"sku":"7654321","title":"TPD/1234567","quantity":1,"price":"-2.00","total":"-2.00"},
            {"sku":"2223334","title":"ROTISSERIE CHKN","quantity":1,"price":"5.00","total":"5.00"},
            {"sku":"5556667","title":"KS PAPER TOWEL","quantity":1,"price":"22.00","total":"22.00"}]},
  {"order_id":"1234567890","kind":"online","date":"2026-09-01","total":"325.00","status":"Delivered",
   "url":"https://www.costco.com/OrderStatusDetailsCmd?orderNumber=1234567890","tax":"25.00",
   "items":[{"sku":"4000123456","title":"Countertop Blender","quantity":1,"price":"300.00","total":"300.00"}]},
  {"order_id":"21100999","kind":"warehouse","date":"2026-09-07","total":"-22.00","location":"Costco Springfield #0123",
   "items":[{"sku":"5556667","title":"KS PAPER TOWEL","quantity":1,"price":"-22.00","total":"-22.00"}]},
  {"order_id":"21100777","kind":"fuel","date":"2026-09-06","total":"52.00","location":"Costco Gas Springfield",
   "items":[{"sku":"1","title":"REGULAR","quantity":1,"price":"52.00","total":"52.00"}]}],
 "charges":[
  {"order_id":"21100123456789012345","date":"2026-09-05","amount":"-50.00","instrument":"Visa ••••1234"},
  {"order_id":"1234567890","date":"2026-09-02","amount":"-325.00","instrument":"Visa ••••1234"},
  {"order_id":"21100999","date":"2026-09-07","amount":"22.00","instrument":"Visa ••••1234"},
  {"order_id":"21100777","date":"2026-09-06","amount":"-52.00","instrument":"Costco Shop Card"}]}`

// costcoRow writes a bank row worded the way Costco's charges are.
func costcoRow(l *ledger, date, amount, statement string) string {
	l.t.Helper()
	return l.alex.post("/transactions", map[string]any{
		"account_id": l.str("card"), "date": date, "amount": amount,
		"payee": "Costco", "statement_name": statement,
	}).requireStatus(http.StatusCreated).json()["id"].(string)
}

func TestACostcoReceiptIsMatchedByItsTenderAndDividedByItsItems(t *testing.T) {
	l := buildLedger(t)
	created := l.alex.post("/merchants/costco/accounts", map[string]any{"label": "Alex"}).
		requireStatus(http.StatusCreated).json()
	require.Equal(t, "costco", created["merchant"])
	require.Nil(t, created["gift_card_account_id"])
	account := created["id"].(string)
	// The same label under each merchant is two accounts, not a conflict.
	amazon := merchantAccount(l, "Alex")

	// The two merchants' accounts are listed apart, and one is not reachable
	// as the other.
	require.Len(t, l.alex.get("/merchants/costco/accounts").requireStatus(http.StatusOK).list(), 1)
	require.Len(t, l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list(), 1)
	l.alex.del("/merchants/amazon/accounts/" + account + "/session").requireStatus(http.StatusNotFound)
	l.alex.patch("/merchants/amazon/accounts/"+account, map[string]any{"label": "Stolen"}).requireStatus(http.StatusNotFound)
	l.alex.del("/merchants/amazon/accounts/" + account).requireStatus(http.StatusNotFound)

	warehouse := costcoRow(l, "2026-09-05", "-50.00", "COSTCO WHSE #0123 SPRINGFIELD ZZ")
	online := costcoRow(l, "2026-09-02", "-325.00", "COSTCO.COM *ONLINE 800-000-0000")
	returned := costcoRow(l, "2026-09-07", "22.00", "COSTCO WHSE #0123 SPRINGFIELD ZZ")
	gas := costcoRow(l, "2026-09-06", "-52.00", "COSTCO GAS #0123")
	// An Amazon row for the same figure on the same day is not Costco's.
	twin := merchantRow(l, "2026-09-05", "-50.00")

	// Costco's file is not Amazon's, and a Costco account is not reachable
	// through Amazon's import.
	uploadTo(l.alex, "/merchants/amazon/imports", "costco.json", costcoJSON,
		map[string]string{"merchant_account_id": amazon}).requireStatus(http.StatusBadRequest)
	uploadTo(l.alex, "/merchants/amazon/imports", "costco.json", costcoJSON,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusNotFound)

	report := uploadTo(l.alex, "/merchants/costco/imports", "costco.json", costcoJSON,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK).json()
	require.Equal(t, "agentifi_json", report["format"])
	require.Equal(t, float64(4), report["orders"])
	require.Equal(t, float64(4), report["charges"])
	require.Equal(t, float64(3), report["matched"], "the warehouse receipt, the online order and the return; not the gas paid by Shop Card")

	match := l.alex.get("/merchants/transactions/" + warehouse).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.MerchantMatchCharge, match["basis"])
	order := match["order"].(map[string]any)
	require.Equal(t, "21100123456789012345", order["order_number"])
	require.Equal(t, "warehouse", order["kind"])
	require.Equal(t, "Costco Springfield #0123", order["location"])
	require.Equal(t, "Alex", order["account_label"])
	items := order["items"].([]any)
	require.Len(t, items, 3, "the instant saving was folded into its item")
	require.Equal(t, "1234567", items[0].(map[string]any)["sku"])
	require.Equal(t, "14.00", *stringPtr(items[0].(map[string]any)["total_owed"]))

	// The whole receipt matched, so the row was divided one split per item,
	// tax spread across them in proportion, to the cent.
	txn := l.alex.get("/transactions/" + warehouse).requireStatus(http.StatusOK).json()
	splits := txn["splits"].([]any)
	require.Len(t, splits, 3)
	sum := decimal.Zero
	memos := []string{}
	for _, one := range splits {
		split := one.(map[string]any)
		sum = sum.Add(decimal.RequireFromString(split["amount"].(string)))
		memos = append(memos, split["memo"].(string))
	}
	require.Equal(t, "-50.00", sum.StringFixed(2))
	require.Equal(t, []string{"KS ORGANIC EGGS", "ROTISSERIE CHKN", "KS PAPER TOWEL"}, memos)

	require.Equal(t, "online", l.alex.get("/merchants/transactions/" + online).requireStatus(http.StatusOK).
		json()["order"].(map[string]any)["kind"])
	require.Equal(t, "-22.00", l.alex.get("/merchants/transactions/" + returned).requireStatus(http.StatusOK).
		json()["order"].(map[string]any)["total"], "a return receipt matches its refund")
	l.alex.get("/merchants/transactions/" + gas).requireStatus(http.StatusNotFound)
	l.alex.get("/merchants/transactions/" + twin).requireStatus(http.StatusNotFound)
	require.Equal(t, "costco", order["merchant"], "an order behind a row says whose it is")

	// Offered by hand, the Amazon twin sees Amazon's orders — none — searched
	// or not, unless the person asks for Costco's.
	require.Empty(t, l.alex.get("/merchants/transactions/" + twin + "/candidates").
		requireStatus(http.StatusOK).json()["candidates"])
	require.Empty(t, l.alex.get("/merchants/transactions/" + twin + "/candidates?q=blender").
		requireStatus(http.StatusOK).json()["candidates"], "a search stays with the row's merchant")
	forTwin := l.alex.get("/merchants/transactions/" + twin + "/candidates?merchant=costco").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.NotEmpty(t, forTwin)
	require.Equal(t, "costco", forTwin[0].(map[string]any)["merchant"])
	searched := l.alex.get("/merchants/transactions/" + twin + "/candidates?merchant=costco&q=blender").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, searched, 1)
	// And a person may say the Amazon-worded row is this Costco receipt after all.
	receipt := forTwin[0].(map[string]any)["id"].(string)
	l.alex.post("/merchants/transactions/"+twin+"/orders", map[string]any{"order_id": receipt}).
		requireStatus(http.StatusOK)
	l.alex.del("/merchants/transactions/" + twin + "/orders/" + receipt).requireStatus(http.StatusNoContent)

	costco := l.alex.get("/merchants/costco/summary").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(4), costco["merchant_transactions"])
	require.Equal(t, float64(3), costco["matched_transactions"])
	require.Equal(t, float64(4), costco["orders"])
	other := l.alex.get("/merchants/amazon/summary").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), other["merchant_transactions"])
	require.Equal(t, float64(0), other["matched_transactions"])
	require.Equal(t, float64(0), other["orders"])

	// The orders list is one merchant's, and the gas receipt waits for
	// nothing: a Shop Card paid it, so no bank row ever will.
	require.Equal(t, float64(4), l.alex.get("/merchants/costco/orders").requireStatus(http.StatusOK).json()["total"])
	require.Equal(t, float64(0), l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["total"])
	require.Equal(t, float64(0), l.alex.get("/merchants/costco/orders?unmatched=true").requireStatus(http.StatusOK).json()["total"])

	// The rows offered to a receipt by hand are Costco's, not the Amazon twin.
	candidates := l.alex.get("/merchants/costco/orders/" + order["id"].(string) + "/candidates").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	for _, one := range candidates {
		require.NotEqual(t, twin, one.(map[string]any)["id"])
	}
	// And a Costco receipt is not an Amazon order under /amazon.
	l.alex.get("/merchants/amazon/orders/" + order["id"].(string) + "/candidates").requireStatus(http.StatusNotFound)
	l.alex.patch("/merchants/amazon/orders/"+order["id"].(string), map[string]any{"ignored": true}).requireStatus(http.StatusNotFound)
}

func TestACostcoRowInTheCategoryCheckSeesTheReceipt(t *testing.T) {
	l := buildLedger(t)
	groceries := l.alex.post("/categories", map[string]any{
		"name": "Groceries", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	account := l.alex.post("/merchants/costco/accounts", map[string]any{"label": "Alex"}).
		requireStatus(http.StatusCreated).json()["id"].(string)
	row := costcoRow(l, "2026-09-05", "-50.00", "COSTCO WHSE #0123 SPRINGFIELD ZZ")
	uploadTo(l.alex, "/merchants/costco/imports", "costco.json", costcoJSON,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+row+`","category_id":"`+groceries+
			`","summary":"Eggs, a chicken and paper towels at Costco Springfield; groceries"}`),
		answerReply("Groceries: the receipt was food and paper towels."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": row}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "model", run["decided_by"])

	opening := run["conversation"].(map[string]any)["messages"].([]any)[0].(map[string]any)
	shown := opening["content"].(string)
	require.Contains(t, shown, "The Costco purchase behind this charge")
	require.Contains(t, shown, `"merchant": "Costco"`)
	require.Contains(t, shown, "Costco Springfield #0123")
	require.Contains(t, shown, "KS ORGANIC EGGS")
	require.Contains(t, shown, "share_of_charge")
	require.Contains(t, shown, "A Costco purchase is attached to this row")
}

// A name is optional: one login at a merchant needs none to be told apart.
// An unnamed account is shown by the email it signs in with once it has one,
// by the merchant's own name before that, and two unnamed accounts are two
// accounts. A name somebody did give is still one per merchant.
func TestAMerchantAccountNeedsNoName(t *testing.T) {
	l := buildLedger(t)
	first := l.alex.post("/merchants/costco/accounts", map[string]any{}).
		requireStatus(http.StatusCreated).json()
	require.Equal(t, "", first["label"])
	require.Equal(t, "Costco account", first["name"])
	l.alex.post("/merchants/costco/accounts", map[string]any{"label": "  "}).
		requireStatus(http.StatusCreated)

	named := l.alex.post("/merchants/costco/accounts", map[string]any{"label": "Alex"}).
		requireStatus(http.StatusCreated).json()
	require.Equal(t, "Alex", named["name"])
	l.alex.post("/merchants/costco/accounts", map[string]any{"label": "Alex"}).
		requireStatus(http.StatusConflict)

	// Naming an account and taking the name away again are both edits.
	id := first["id"].(string)
	renamed := l.alex.patch("/merchants/costco/accounts/"+id, map[string]any{"label": "Casey"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "Casey", renamed["name"])
	cleared := l.alex.patch("/merchants/costco/accounts/"+id, map[string]any{"label": ""}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "Costco account", cleared["name"])

	// A Costco sign-in happens on the server, in Camoufox: no route takes a
	// session handed over from a browser elsewhere.
	l.alex.post("/merchants/costco/accounts/"+id+"/bridge", nil).requireStatus(http.StatusNotFound)
	l.alex.post("/merchants/costco/accounts/"+id+"/session", map[string]any{"session": map[string]any{}}).
		requireStatus(http.StatusMethodNotAllowed)
}

// fakeCatalog is a listing that knows a few numbers and counts the asking.
type fakeCatalog struct {
	entries map[string]provider.CatalogEntry
	asked   []string
}

func (f *fakeCatalog) Lookup(_ context.Context, merchant domain.MerchantID, sku string) (provider.CatalogEntry, bool, error) {
	if merchant != domain.MerchantCostco {
		return provider.CatalogEntry{}, false, provider.ErrNoCatalog
	}
	f.asked = append(f.asked, sku)
	entry, ok := f.entries[sku]
	return entry, ok, nil
}

func TestCostcoItemNumbersAreLookedUpOnceAndWhatWasLearnedOutlivesTheAccount(t *testing.T) {
	l := buildLedger(t)
	ctx := context.Background()
	account := l.alex.post("/merchants/costco/accounts", map[string]any{"label": "Alex"}).
		requireStatus(http.StatusCreated).json()["id"].(string)
	warehouse := costcoRow(l, "2026-09-05", "-50.00", "COSTCO WHSE #0123 SPRINGFIELD ZZ")

	// A number the catalog already knows: the split made at import reads as
	// the product, not the register's abbreviation.
	require.NoError(t, l.env.DB.UpsertCatalogItem(ctx, &store.CatalogItem{
		Merchant: domain.MerchantCostco, SKU: "2223334", Status: store.CatalogFound,
		Title: "Kirkland Signature Roast Chicken", Brand: "kirkland signature", Category: "Deli",
		Source: "costco-sameday",
	}))
	uploadTo(l.alex, "/merchants/costco/imports", "costco.json", costcoJSON,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	txn := l.alex.get("/transactions/" + warehouse).requireStatus(http.StatusOK).json()
	memos := []string{}
	for _, one := range txn["splits"].([]any) {
		memos = append(memos, one.(map[string]any)["memo"].(string))
	}
	require.Equal(t, []string{"KS ORGANIC EGGS", "Kirkland Signature Roast Chicken", "KS PAPER TOWEL"}, memos)

	// The pass asks about every number not yet answered, once each, newest
	// purchase first, and records the misses as well as the hits.
	catalog := &fakeCatalog{entries: map[string]provider.CatalogEntry{
		"1234567": {
			Title: "Kirkland Signature Organic Eggs, 24-count", Brand: "kirkland signature", Size: "each",
			Category: "Whole Eggs", ImageURL: "https://img.example/eggs.jpg",
			URL: "https://sameday.costco.com/store/costco/products/1", Price: domain.MustFromString("10.00"), HasPrice: true,
			Source: "costco-sameday", SourceRef: "items_359-1", Raw: []byte(`{"name":"eggs"}`),
		},
	}}
	l.env.Catalog = catalog
	report, err := NewMerchants(l.env).EnrichCatalog(ctx, domain.MerchantCostco, 100)
	require.NoError(t, err)
	require.Equal(t, service.CatalogReport{LookedUp: 4, Found: 1, Missing: 3}, report,
		"the instant saving was folded into its item at import, so its number is not one")
	require.Equal(t, []string{"5556667", "1", "1234567", "4000123456"}, catalog.asked,
		"the return's paper towel first (newest), the known chicken never")

	match := l.alex.get("/merchants/transactions/" + warehouse).requireStatus(http.StatusOK).json()
	items := match["order"].(map[string]any)["items"].([]any)
	eggs := items[0].(map[string]any)
	require.Equal(t, "KS ORGANIC EGGS", eggs["title"], "the receipt's own words stay")
	found := eggs["catalog"].(map[string]any)
	require.Equal(t, "Kirkland Signature Organic Eggs, 24-count", found["title"])
	require.Equal(t, "Whole Eggs", found["category"])
	require.Equal(t, "https://img.example/eggs.jpg", found["image_url"])
	require.Equal(t, "Kirkland Signature Roast Chicken", items[1].(map[string]any)["catalog"].(map[string]any)["title"])
	require.Nil(t, items[2].(map[string]any)["catalog"], "a number the listing did not have has no catalog entry")

	// A second pass has nothing to ask: misses are not retried for a month.
	report, err = NewMerchants(l.env).EnrichCatalog(ctx, domain.MerchantCostco, 100)
	require.NoError(t, err)
	require.Equal(t, service.CatalogReport{}, report)
	require.Len(t, catalog.asked, 4)

	// What was learned is kept: a later lookup that finds nothing does not
	// erase it, and neither does the account going away.
	require.NoError(t, l.env.DB.UpsertCatalogItem(ctx, &store.CatalogItem{
		Merchant: domain.MerchantCostco, SKU: "1234567", Status: store.CatalogMissing,
	}))
	l.alex.del("/merchants/costco/accounts/" + account).requireStatus(http.StatusNoContent)
	var status, title, price, raw string
	require.NoError(t, l.env.DB.Pool().QueryRow(ctx,
		`SELECT status, title, price::text, raw::text
		   FROM merchant_catalog WHERE merchant = $1 AND sku = '1234567'`,
		string(domain.MerchantCostco)).Scan(&status, &title, &price, &raw))
	require.Equal(t, store.CatalogFound, status)
	require.Equal(t, "Kirkland Signature Organic Eggs, 24-count", title)
	require.Equal(t, "10.00", price)
	require.JSONEq(t, `{"name":"eggs"}`, raw)
	require.NoError(t, l.env.DB.Pool().QueryRow(ctx,
		`SELECT status FROM merchant_catalog WHERE merchant = $1 AND sku = '1'`,
		string(domain.MerchantCostco)).Scan(&status))
	require.Equal(t, store.CatalogMissing, status)
}
