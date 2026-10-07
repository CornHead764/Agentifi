package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The Amazon connector: accounts, files, orders, and the match from a bank row
// to the order behind it — and what that does to the category check.

const merchantCSV = `Website,Order ID,Order Date,Currency,Unit Price,Total Owed,ASIN,Product Condition,Quantity,Order Status,Ship Date,Product Name
Amazon.com,113-1234567-0000001,2026-08-01T14:03:11Z,USD,13.00,14.00,B0CABLE,New,1,Closed,2026-08-02T03:00:00Z,USB-C Cable
Amazon.com,113-1234567-0000001,2026-08-01T14:03:11Z,USD,24.00,26.00,B0STORAGE,New,1,Closed,2026-08-02T03:00:00Z,Glass Storage Set
Amazon.com,113-7654321-0000002,2026-08-10T09:00:00Z,USD,8.00,16.00,B0BATT,New,2,Closed,2026-08-10T20:00:00Z,Batteries
`

func merchantAccount(l *ledger, label string) string {
	l.t.Helper()
	return l.alex.post("/merchants/amazon/accounts", map[string]any{"label": label}).
		requireStatus(http.StatusCreated).json()["id"].(string)
}

func TestMerchantAccountsAreLabelsAndLabelsAreUnique(t *testing.T) {
	l := buildLedger(t)
	id := merchantAccount(l, "Alex")
	l.alex.post("/merchants/amazon/accounts", map[string]any{"label": "Alex"}).requireStatus(http.StatusConflict)

	l.alex.patch("/merchants/amazon/accounts/"+id, map[string]any{"label": "Alex (personal)"}).
		requireStatus(http.StatusOK)
	accounts := l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list()
	require.Len(t, accounts, 1)
	require.Equal(t, "Alex (personal)", accounts[0]["label"])

	// A viewer can see the accounts and cannot add one.
	l.as("vera").get("/merchants/amazon/accounts").requireStatus(http.StatusOK)
	l.as("vera").post("/merchants/amazon/accounts", map[string]any{"label": "Mine"}).requireStatus(http.StatusForbidden)

	l.alex.del("/merchants/amazon/accounts/" + id).requireStatus(http.StatusNoContent)
	require.Empty(t, l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list())
}

func TestAmazonsCSVIsPreviewedThenImportedAndImportingTwiceAddsNothing(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Alex")

	preview := uploadTo(l.alex, "/merchants/amazon/imports", "Retail.OrderHistory.1.csv", merchantCSV,
		map[string]string{"merchant_account_id": account, "dry_run": "true"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, preview["dry_run"])
	require.Equal(t, "amazon_csv", preview["format"])
	require.Equal(t, float64(2), preview["orders"])
	require.Equal(t, float64(2), preview["new_orders"])
	require.Equal(t, float64(3), preview["items"])
	require.Equal(t, float64(0), l.alex.get("/merchants/amazon/summary").requireStatus(http.StatusOK).json()["orders"],
		"a preview writes nothing")

	written := uploadTo(l.alex, "/merchants/amazon/imports", "Retail.OrderHistory.1.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(2), written["new_orders"])

	again := uploadTo(l.alex, "/merchants/amazon/imports", "Retail.OrderHistory.1.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), again["new_orders"], "the same file twice changes nothing")

	orders := l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(2), orders["total"])
	first := orders["orders"].([]any)[0].(map[string]any)
	require.Equal(t, "113-7654321-0000002", first["order_number"], "newest first")
	second := orders["orders"].([]any)[1].(map[string]any)
	require.Equal(t, "40.00", second["total"])
	require.Equal(t, "Alex", second["account_label"])
	items := second["items"].([]any)
	require.Len(t, items, 2)
	require.Equal(t, "Glass Storage Set", items[1].(map[string]any)["title"])
	require.Equal(t, "26.00", items[1].(map[string]any)["total_owed"])

	summary := l.alex.get("/merchants/amazon/summary").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(2), summary["orders"])
	require.Equal(t, float64(3), summary["items"])
	require.Equal(t, "2026-08-10", summary["newest_order"])

	// Something that is not an Amazon file is refused, not read as empty.
	uploadTo(l.alex, "/merchants/amazon/imports", "x.csv", "Date,Account,Payee\n2026-01-01,A,B\n",
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusBadRequest)
	uploadTo(l.alex, "/merchants/amazon/imports", "x.csv", merchantCSV, map[string]string{}).
		requireStatus(http.StatusUnprocessableEntity)
	empty := uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", "",
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, empty.Body.String(), "orders.csv is empty")
}

// merchantRow writes a bank row worded the way Amazon's charges are.
func merchantRow(l *ledger, date, amount string) string {
	l.t.Helper()
	return l.alex.post("/transactions", map[string]any{
		"account_id": l.str("card"), "date": date, "amount": amount,
		"payee": "Amazon", "statement_name": "AMAZON.COM*2K4D1R6Q3 AMZN.COM/BILL WA",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
}

func TestABankRowIsMatchedToTheOrderThatChargedIt(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	whole := merchantRow(l, "2026-08-03", "-40.00")
	part := merchantRow(l, "2026-08-11", "-16.00")
	stranger := merchantRow(l, "2026-08-11", "-87.00")

	report := uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(2), report["matched"], "the import offers every unmatched Amazon row the new orders")

	match := l.alex.get("/merchants/transactions/" + whole).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.MerchantMatchOrderTotal, match["basis"])
	order := match["order"].(map[string]any)
	require.Equal(t, "113-1234567-0000001", order["order_number"])
	require.Equal(t, "Casey", order["account_label"])
	require.Len(t, order["items"].([]any), 2)

	second := l.alex.get("/merchants/transactions/" + part).requireStatus(http.StatusOK).json()
	require.Equal(t, "113-7654321-0000002", second["order"].(map[string]any)["order_number"])

	l.alex.get("/merchants/transactions/" + stranger).requireStatus(http.StatusNotFound)

	summary := l.alex.get("/merchants/amazon/summary").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(3), summary["merchant_transactions"])
	require.Equal(t, float64(2), summary["matched_transactions"])

	// The orders list says which rows each order explains.
	orders := l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	require.Equal(t, []any{part}, orders[0].(map[string]any)["matched_transaction_ids"])
	unmatched := l.alex.get("/merchants/amazon/orders?unmatched=true").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), unmatched["total"])

	// A person can undo a match, and match by hand.
	l.alex.del("/merchants/transactions/" + whole + "/orders/" + order["id"].(string)).
		requireStatus(http.StatusNoContent)
	l.alex.get("/merchants/transactions/" + whole).requireStatus(http.StatusOK) // re-matched on read: same answer
	l.alex.post("/merchants/transactions/"+stranger+"/orders", map[string]any{"order_id": order["id"]}).
		requireStatus(http.StatusOK)
	byHand := l.alex.get("/merchants/transactions/" + stranger).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.MerchantMatchManual, byHand["basis"])
}

func TestAMatchedMerchantRowIsDecidedFromItsItemsNotThePayeesHistory(t *testing.T) {
	// Eight Amazon rows filed under Shopping is exactly the kind of history
	// that would otherwise decide the category outright: the vote says
	// Shopping at 1.00 for a bag of a storage set. With the order attached,
	// the history stands aside, the model is asked, and it is shown the items
	// and whose account placed the order.
	l := buildLedger(t)
	homeSupplies := l.alex.post("/categories", map[string]any{
		"name": "Home Supplies", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	shopping := l.alex.post("/categories", map[string]any{
		"name": "Shopping", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	for i := 0; i < 8; i++ {
		l.alex.post("/transactions", map[string]any{
			"account_id": l.str("card"), "date": "2026-0" + string(rune('1'+i%6)) + "-1" + string(rune('0'+i%9)),
			"amount": "-20.00", "payee": "Amazon", "statement_name": "AMAZON.COM*OLDER AMZN.COM/BILL WA",
			"category_id": shopping,
		}).requireStatus(http.StatusCreated)
	}
	arrived := merchantRow(l, "2026-08-03", "-40.00")
	account := merchantAccount(l, "Casey")
	uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+arrived+`","category_id":"`+homeSupplies+
			`","summary":"A storage set and a cable on Casey's Amazon account; mostly home supplies"}`),
		answerReply("Home Supplies: the order was a storage set and a cable."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)

	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, "model", run["decided_by"], "the history does not act alone on an enriched row")
	require.Equal(t, float64(1), run["actions"])
	require.NotEmpty(t, model.seen)

	opening := run["conversation"].(map[string]any)["messages"].([]any)[0].(map[string]any)
	shown := opening["content"].(string)
	require.Contains(t, shown, "The Amazon order behind this charge")
	require.Contains(t, shown, "Glass Storage Set")
	require.Contains(t, shown, `"merchant_account": "Casey"`)
	require.Contains(t, shown, "An Amazon order is attached to this row")
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, homeSupplies, action["body"].(map[string]any)["category_id"])
}

const merchantAgentJSON = `{"source":"agentifi-amazon-extract","account_hint":"Alex","orders":[
 {"order_id":"114-0000001-0000001","date":"2026-08-01","total":"44.00","currency":"USD","status":"Delivered","url":"",
  "gift_card":"5.00","tax":"4.00","shipping":"0.00",
  "items":[{"title":"Glass Storage Set","asin":"B0STORAGE","quantity":1,"price":"26.00","url":""},
           {"title":"USB-C Cable","asin":"B0CABLE","quantity":1,"price":"14.00","url":""}]},
 {"order_id":"114-0000002-0000002","date":"2026-08-05","total":"17.00","currency":"USD","status":"Delivered","url":"",
  "gift_card":"17.00","tax":"1.00","shipping":"0.00",
  "items":[{"title":"Kitchen Sponge 12 Pack","asin":"B0SPONGE","quantity":1,"price":"16.00","url":""}]},
 {"order_id":"114-0000003-0000003","date":"2026-04-11","total":"10.00","currency":"USD","status":"Delivered","url":"",
  "items":[{"title":"Zip ties","asin":"B0ZIP","quantity":1,"price":"9.00","url":""}]},
 {"order_id":"114-0000004-0000004","date":"2026-04-13","total":"10.00","currency":"USD","status":"Delivered","url":"",
  "items":[{"title":"Velcro strips","asin":"B0VELCRO","quantity":1,"price":"9.00","url":""}]}],
 "charges":[
  {"order_id":"114-0000001-0000001","date":"2026-08-02","amount":"-39.00","instrument":"Amazon Visa ••••1234"},
  {"order_id":"114-0000001-0000001","date":"2026-08-01","amount":"-5.00","instrument":"Amazon Gift Card"},
  {"order_id":"114-0000002-0000002","date":"2026-08-05","amount":"-17.00","instrument":""},
  {"order_id":"114-0000003-0000003","date":"2026-04-11","amount":"-10.00","instrument":"Amazon Visa ••••1234"},
  {"order_id":"114-0000004-0000004","date":"2026-04-13","amount":"-10.00","instrument":"Amazon Visa ••••1234"}]}`

func TestTheBankSeesTheCardNotTheGiftCardAndAWholeOrderIsDividedByItem(t *testing.T) {
	// A pull shaped like this: an order partly paid from a
	// gift card balance, one paid by it entirely, and two orders for the same
	// amount two days apart. The bank saw $39.00 of the first, nothing of the
	// second, and two rows of $10.00.
	l := buildLedger(t)
	account := merchantAccount(l, "Alex")
	whole := merchantRow(l, "2026-08-03", "-39.00")
	gift := merchantRow(l, "2026-08-06", "-17.00")
	first := merchantRow(l, "2026-04-12", "-10.00")
	second := merchantRow(l, "2026-04-14", "-10.00")

	report := uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", merchantAgentJSON,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(4), report["new_orders"])
	require.Equal(t, float64(3), report["matched"], "the gift-card row finds nothing")

	// The partly gift-carded order matched what the card was charged, and the
	// row is divided across its two items with the tax in proportion: $39.00
	// over $26.00 and $14.00 is $25.35 and $13.65, to the cent.
	match := l.alex.get("/merchants/transactions/" + whole).requireStatus(http.StatusOK).json()
	require.Equal(t, "charge", match["basis"])
	require.Equal(t, "114-0000001-0000001", match["order"].(map[string]any)["order_number"])
	txn := l.alex.get("/transactions/" + whole).requireStatus(http.StatusOK).json()
	splits := txn["splits"].([]any)
	require.Len(t, splits, 2)
	require.Equal(t, "-25.35", splits[0].(map[string]any)["amount"])
	require.Equal(t, "Glass Storage Set", splits[0].(map[string]any)["memo"])
	require.Equal(t, "-13.65", splits[1].(map[string]any)["amount"])
	require.Equal(t, "USB-C Cable", splits[1].(map[string]any)["memo"])

	// The order a gift card paid in full is nobody's bank row, and it is not
	// waiting for one.
	l.alex.get("/merchants/transactions/" + gift).requireStatus(http.StatusNotFound)
	unmatched := l.alex.get("/merchants/amazon/orders?unmatched=true").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), unmatched["total"], "every order that reached the bank is matched")
	orders := l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	byNumber := map[string]map[string]any{}
	for _, one := range orders {
		order := one.(map[string]any)
		byNumber[order["order_number"].(string)] = order
	}
	giftOrder := byNumber["114-0000002-0000002"]
	require.Equal(t, true, giftOrder["paid_by_gift_card"])
	require.Equal(t, "0.00", giftOrder["card_total"])
	require.Equal(t, "17.00", *stringPtr(giftOrder["gift_card_amount"]))

	// The two $10.00 rows found two orders, not the same one twice; and the
	// orders list says which bank row each order explains.
	firstMatch := l.alex.get("/merchants/transactions/" + first).requireStatus(http.StatusOK).json()
	secondMatch := l.alex.get("/merchants/transactions/" + second).requireStatus(http.StatusOK).json()
	require.NotEqual(t, firstMatch["order"].(map[string]any)["id"], secondMatch["order"].(map[string]any)["id"])
	wholeOrder := byNumber["114-0000001-0000001"]
	require.Equal(t, "44.00", wholeOrder["total"])
	require.Equal(t, "39.00", wholeOrder["card_total"])
	matched := wholeOrder["matched_transactions"].([]any)
	require.Len(t, matched, 1)
	row := matched[0].(map[string]any)
	require.Equal(t, whole, row["id"])
	require.Equal(t, "2026-08-03", row["date"])
	require.Equal(t, "-39.00", row["amount"])
	require.NotEmpty(t, row["account_name"])
	require.Equal(t, "charge", row["basis"])
}

func stringPtr(v any) *string {
	s, _ := v.(string)
	return &s
}

func TestABackOrderedItemChargedWeeksLaterStillFindsItsOrder(t *testing.T) {
	// Ordered October 29, charged November 17 when it shipped, on the bank
	// November 18: the order is far outside the two-week window, and the
	// charge names it.
	l := buildLedger(t)
	account := merchantAccount(l, "Alex")
	row := merchantRow(l, "2025-11-18", "-32.00")
	late := `{"source":"agentifi-amazon-extract","orders":[
	 {"order_id":"114-0000005-0000005","date":"2025-10-29","total":"32.00","currency":"USD","status":"Delivered","url":"",
	  "items":[{"title":"Trash Can Deodorizer Pod","asin":"B0POD","quantity":1,"price":"30.00","url":""}]},
	 {"order_id":"114-9999999-0000000","date":"2025-11-10","total":"12.00","currency":"USD","status":"Delivered","url":"",
	  "items":[{"title":"Something else","asin":"B0ELSE","quantity":1,"price":"11.00","url":""}]}],
	 "charges":[{"order_id":"114-0000005-0000005","date":"2025-11-17","amount":"-32.00","instrument":"Amazon Visa ••••1234"}]}`
	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", late,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	match := l.alex.get("/merchants/transactions/" + row).requireStatus(http.StatusOK).json()
	require.Equal(t, "charge", match["basis"])
	require.Equal(t, "114-0000005-0000005", match["order"].(map[string]any)["order_number"])
}

func TestASplitOfAnMerchantRowMustFollowTheOrdersItems(t *testing.T) {
	// Shown a $17.40 charge and a $16.36 item, a model proposing "$16.36 trash
	// bags + $1.04 shipping" has tax and shipping backwards: they are the
	// item's. The guard sends that back to update_transaction, and on a
	// three-item order it insists on the shares as given.
	l := buildLedger(t)
	home := l.alex.post("/categories", map[string]any{
		"name": "Home Supplies", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	account := merchantAccount(l, "Alex")
	single := merchantRow(l, "2026-08-28", "-17.40")
	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", `{"source":"agentifi-amazon-extract","orders":[
	 {"order_id":"112-0000006-0000006","date":"2026-08-26","total":"17.40","currency":"USD","status":"Delivered","url":"",
	  "gift_card":"0.00","tax":"1.04","shipping":"0.00",
	  "items":[{"title":"Tall Kitchen Bags 40ct","asin":"B0BAGS","quantity":1,"price":"16.36","url":""}]}],
	 "charges":[{"order_id":"112-0000006-0000006","date":"2026-08-27","amount":"-17.40","instrument":"Prime Visa ••••1234"}]}`,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	model := newFakeModel(t,
		toolReply("split_transaction", `{"transaction_id":"`+single+`","summary":"Bags plus shipping",
		  "splits":[{"amount":"-16.36","category_id":"`+home+`","memo":"Kitchen bags"},{"amount":"-1.04","memo":"Shipping"}]}`),
		toolReply("update_transaction", `{"transaction_id":"`+single+`","category_id":"`+home+`","summary":"Trash bags: Home Supplies"}`),
		answerReply("Home Supplies."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": single}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, float64(1), run["actions"], "the phantom shipping split was refused; the category change stood")
	refused := ""
	for _, one := range run["conversation"].(map[string]any)["messages"].([]any) {
		if content, ok := one.(map[string]any)["content"].(string); ok &&
			strings.Contains(content, "Do not split it") {
			refused = content
		}
	}
	require.NotEmpty(t, refused, "the refusal never reached the model")
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "PATCH", action["method"])
}

func TestAnOrderOnAnUntrackedCardIsIgnoredAndAMatchIsSetByHand(t *testing.T) {
	// Some of the household's orders are charged to a card Agentifi does not
	// track. Ignoring one takes it out of the matcher's offer and out of the
	// waiting list; naming a bank row for it by hand puts it back.
	l := buildLedger(t)
	account := merchantAccount(l, "Alex")
	uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	orders := l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	batteries := orders[0].(map[string]any)
	storageSet := orders[1].(map[string]any)
	require.Equal(t, "113-1234567-0000001", storageSet["order_number"])
	require.Equal(t, false, storageSet["ignored"])

	l.alex.patch("/merchants/amazon/orders/"+storageSet["id"].(string), map[string]any{}).
		requireStatus(http.StatusUnprocessableEntity)
	ignored := l.alex.patch("/merchants/amazon/orders/"+storageSet["id"].(string), map[string]any{"ignored": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, ignored["ignored"])

	// A row that agrees to the cent arrives; the ignored order is not offered.
	whole := merchantRow(l, "2026-08-03", "-40.00")
	stranger := merchantRow(l, "2026-08-11", "-87.00")
	l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-04", "amount": "-40.00",
		"payee": "Costco", "statement_name": "COSTCO WHSE #0000",
	}).requireStatus(http.StatusCreated)
	matched := l.alex.post("/merchants/amazon/match", map[string]any{}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), matched["matched"])
	l.alex.get("/merchants/transactions/" + whole).requireStatus(http.StatusNotFound)
	waiting := l.alex.get("/merchants/amazon/orders?unmatched=true").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), waiting["total"], "only the batteries wait for a bank row")
	require.Equal(t, batteries["id"], waiting["orders"].([]any)[0].(map[string]any)["id"])

	// The candidates for the order: Amazon's rows in the weeks after it,
	// nearest in amount first. A search reaches rows the bank worded otherwise.
	candidates := l.alex.get("/merchants/amazon/orders/" + storageSet["id"].(string) + "/candidates").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, candidates, 2)
	first := candidates[0].(map[string]any)
	require.Equal(t, whole, first["id"])
	require.Equal(t, "-40.00", first["amount"])
	require.Equal(t, l.str("card"), first["account_id"])
	require.Equal(t, "", first["matched_order_number"])
	require.Equal(t, stranger, candidates[1].(map[string]any)["id"])
	searched := l.alex.get("/merchants/amazon/orders/" + storageSet["id"].(string) + "/candidates?q=costco").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, searched, 1)
	require.Equal(t, "Costco", searched[0].(map[string]any)["payee"])

	// Naming a row by hand takes the ignore back.
	byHand := l.alex.post("/merchants/transactions/"+whole+"/orders", map[string]any{"order_id": storageSet["id"]}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.MerchantMatchManual, byHand["basis"])
	require.Equal(t, false, byHand["order"].(map[string]any)["ignored"])
	orders = l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	storageSet = orders[1].(map[string]any)
	rows := storageSet["matched_transactions"].([]any)
	require.Len(t, rows, 1)
	require.Equal(t, l.str("card"), rows[0].(map[string]any)["account_id"])
	after := l.alex.get("/merchants/amazon/orders/" + storageSet["id"].(string) + "/candidates").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, after, 1, "a row matched to this order is not offered to it again")

	// Offered to another order, a row says which order it already explains;
	// matching it there moves it.
	l.alex.post("/merchants/transactions/"+stranger+"/orders", map[string]any{"order_id": storageSet["id"]}).
		requireStatus(http.StatusOK)
	elsewhere := l.alex.get("/merchants/amazon/orders/" + batteries["id"].(string) + "/candidates").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, elsewhere, 1, "the row of 2026-08-03 is a week before the order and is not offered")
	moved := elsewhere[0].(map[string]any)
	require.Equal(t, stranger, moved["id"])
	require.Equal(t, "113-1234567-0000001", moved["matched_order_number"])
	l.alex.del("/merchants/transactions/" + stranger + "/orders/" + storageSet["id"].(string)).
		requireStatus(http.StatusNoContent)
	l.alex.post("/merchants/transactions/"+stranger+"/orders", map[string]any{"order_id": batteries["id"]}).
		requireStatus(http.StatusOK)
	orders = l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	require.Equal(t, []any{stranger}, orders[0].(map[string]any)["matched_transaction_ids"])
	require.Equal(t, []any{whole}, orders[1].(map[string]any)["matched_transaction_ids"])

	// Ignoring an order with a match keeps the match; only the waiting changes.
	kept := l.alex.patch("/merchants/amazon/orders/"+batteries["id"].(string), map[string]any{"ignored": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, kept["ignored"])
	require.Equal(t, []any{stranger}, kept["matched_transaction_ids"])
	l.alex.get("/merchants/amazon/orders/" + uuid.NewString() + "/candidates").requireStatus(http.StatusNotFound)
}

func TestOnePaymentCanSettleSeveralOrders(t *testing.T) {
	// Six orders went to a card Agentifi does not track, and one Zelle payment
	// repaid them all. The payment is not worded as Amazon's, pays for every
	// order at once, and the category check sees every item with its share.
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	orders := l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	batteries := orders[0].(map[string]any)["id"].(string)  // 16.00, one item
	storageSet := orders[1].(map[string]any)["id"].(string) // 40.00, two items

	zelle := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-12", "amount": "-56.00",
		"payee": "Zelle to Casey", "statement_name": "ZELLE PAYMENT TO CASEY",
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	first := l.alex.post("/merchants/transactions/"+zelle+"/orders", map[string]any{"order_id": storageSet}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "-56.00", first["amount"], "alone, the order's share is the whole row")
	require.Len(t, first["orders"].([]any), 1)

	both := l.alex.post("/merchants/transactions/"+zelle+"/orders", map[string]any{"order_id": batteries}).
		requireStatus(http.StatusOK).json()
	shares := both["orders"].([]any)
	require.Len(t, shares, 2)
	require.Equal(t, "-40.00", shares[0].(map[string]any)["amount"], "each order carries its own card total")
	require.Equal(t, "-16.00", shares[1].(map[string]any)["amount"])
	require.Equal(t, domain.MerchantMatchManual, shares[1].(map[string]any)["basis"])

	// Not worded as Amazon's, and still enriched: every item of every order,
	// each with its share of the row.
	read := l.alex.get("/merchants/transactions/" + zelle).requireStatus(http.StatusOK).json()
	require.Len(t, read["orders"].([]any), 2)
	list := l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	for _, one := range list {
		require.Equal(t, []any{zelle}, one.(map[string]any)["matched_transaction_ids"])
	}
	summary := l.alex.get("/merchants/amazon/summary").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), summary["matched_transactions"], "one row, however many orders")
	candidates := l.alex.get("/merchants/amazon/orders/" + batteries + "/candidates?q=zelle").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, candidates, 0, "a row already paying for this order is not offered to it")
	offered := l.alex.get("/merchants/amazon/orders/" + storageSet + "/candidates?q=zelle").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, offered, 0)

	// Releasing one order leaves the other, which takes the whole row back.
	l.alex.del("/merchants/transactions/" + zelle + "/orders/" + batteries).requireStatus(http.StatusNoContent)
	left := l.alex.get("/merchants/transactions/" + zelle).requireStatus(http.StatusOK).json()
	require.Len(t, left["orders"].([]any), 1)
	require.Equal(t, "-56.00", left["amount"])
	l.alex.del("/merchants/transactions/" + zelle + "/orders/" + batteries).requireStatus(http.StatusNotFound)
}

func TestARowIsOfferedTheOrdersItMightBeNearestInAmountFirst(t *testing.T) {
	// From the register: which order was this charge? The orders placed in
	// the two months before it, nearest in card total first, each saying
	// which rows already pay for it; a search reaches an order by its items.
	l := buildLedger(t)
	account := merchantAccount(l, "Alex")
	uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	row := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-12", "amount": "-16.00",
		"payee": "Zelle to Casey", "statement_name": "ZELLE PAYMENT TO CASEY",
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	offered := l.alex.get("/merchants/transactions/" + row + "/candidates").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, offered, 2)
	require.Equal(t, "113-7654321-0000002", offered[0].(map[string]any)["order_number"], "the $16.00 batteries first")
	require.Equal(t, "113-1234567-0000001", offered[1].(map[string]any)["order_number"])

	byItem := l.alex.get("/merchants/transactions/" + row + "/candidates?q=storage%20set").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, byItem, 1)
	require.Equal(t, "113-1234567-0000001", byItem[0].(map[string]any)["order_number"])

	// Matched, the order is no longer offered to the same row, and another
	// row sees who pays for it.
	l.alex.post("/merchants/transactions/"+row+"/orders", map[string]any{"order_id": offered[0].(map[string]any)["id"]}).
		requireStatus(http.StatusOK)
	after := l.alex.get("/merchants/transactions/" + row + "/candidates").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, after, 1)
	other := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("card"), "date": "2026-08-13", "amount": "-16.00", "payee": "Amazon",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	seen := l.alex.get("/merchants/transactions/" + other + "/candidates").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	paid := seen[0].(map[string]any)["matched_transaction_ids"].([]any)
	require.Equal(t, []any{row}, paid)

	l.alex.get("/merchants/transactions/" + uuid.NewString() + "/candidates").requireStatus(http.StatusNotFound)
	// A pull that reaches back further than ten years is refused before the agent is asked.
	l.alex.post("/merchants/amazon/accounts/"+account+"/pull", map[string]any{"days": 99999}).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestASearchForACandidateReachesPastTheOfferedWindow(t *testing.T) {
	// The unsearched list is a suggestion and is bounded on purpose: Amazon's
	// wording, in the weeks a charge for this order could fall in. Someone who
	// types has already read that list and knows the row is not in it — a
	// charge that landed half a year later, an order placed long before the
	// row — so a search that still could not leave the window would answer
	// "nothing" about a row sitting right there, and matching by hand would be
	// impossible for exactly the cases it exists to rescue.
	l := buildLedger(t)
	account := merchantAccount(l, "Alex")
	uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	orders := l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	storageSet := orders[1].(map[string]any)["id"].(string)

	// A charge half a year past the order, worded as the seller rather than
	// as Amazon: outside the window on both counts.
	late := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("card"), "date": "2027-02-14", "amount": "-26.00",
		"payee": "Example Seller", "statement_name": "EXAMPLE SELLER",
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	require.Len(t, l.alex.get("/merchants/amazon/orders/" + storageSet + "/candidates").
		requireStatus(http.StatusOK).json()["candidates"].([]any), 0,
		"the unsearched list should stay inside its window")

	found := l.alex.get("/merchants/amazon/orders/" + storageSet + "/candidates?q=seller").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, found, 1, "a search could not reach a row outside the window")
	require.Equal(t, late, found[0].(map[string]any)["id"])

	// And the same the other way round: an order long before the row.
	old := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2027-03-01", "amount": "-16.00",
		"payee": "Zelle to Casey", "statement_name": "ZELLE PAYMENT TO CASEY",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	require.Len(t, l.alex.get("/merchants/transactions/" + old + "/candidates").
		requireStatus(http.StatusOK).json()["candidates"].([]any), 0)
	byItem := l.alex.get("/merchants/transactions/" + old + "/candidates?q=batteries").
		requireStatus(http.StatusOK).json()["candidates"].([]any)
	require.Len(t, byItem, 1, "a search could not reach an order outside the window")
	require.Equal(t, "113-7654321-0000002", byItem[0].(map[string]any)["order_number"])
}

func TestTheGiftCardBalanceIsAnAccountAndItsRowsMatchTheGiftCardsSide(t *testing.T) {
	// A pull reads the gift card balance page. The balance becomes a cash
	// account of the household's, each line of its activity a row written
	// once, and a row paid from the balance matches the gift card's own
	// charge — the money the bank never saw.
	l := buildLedger(t)
	account := merchantAccount(l, "Alex")
	file := `{"source":"agentifi-amazon-extract","orders":[
	 {"order_id":"112-0000006-0000006","date":"2026-08-26","total":"17.40","currency":"USD","status":"Delivered","url":"",
	  "gift_card":"17.40","tax":"1.04","shipping":"0.00",
	  "items":[{"title":"Tall Kitchen Bags 40ct","asin":"B0BAGS","quantity":1,"price":"16.36","url":""}]}],
	 "charges":[{"order_id":"112-0000006-0000006","date":"2026-08-26","amount":"-17.40","instrument":"Amazon Gift Card"}],
	 "gift_card":{"balance":"42.60","activity":[
	   {"date":"2026-08-26","description":"Gift card applied to order 112-0000006-0000006","amount":"-17.40","order_id":"112-0000006-0000006"},
	   {"date":"2026-08-20","description":"Gift card reload","amount":"60.00"}]}}`
	report := uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", file,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(2), report["gift_card_rows"])
	require.Equal(t, float64(1), report["matched"], "the gift card row found its order")

	var mine map[string]any
	for _, one := range l.alex.get("/merchants/amazon/accounts").requireStatus(http.StatusOK).list() {
		if one["id"] == account {
			mine = one
		}
	}
	require.Equal(t, "42.60", mine["gift_card_balance"])
	giftCardAccount := mine["gift_card_account_id"].(string)

	var held map[string]any
	for _, one := range l.alex.get("/accounts").requireStatus(http.StatusOK).list() {
		if one["id"] == giftCardAccount {
			held = one
		}
	}
	require.NotNil(t, held)
	require.Equal(t, "Amazon gift card · Alex", held["name"])
	require.Equal(t, "cash", held["kind"])
	require.Equal(t, "gift_card", held["type"])
	require.Equal(t, "42.60", held["provider_balance"], "the balance the page showed")

	rows := registerPage(l, "account_id="+giftCardAccount+"&date_field=posted")["items"].([]any)
	require.Len(t, rows, 2)
	spent := rows[0].(map[string]any)
	require.Equal(t, "-17.40", spent["amount"])
	require.Equal(t, "Amazon", spent["payee"])
	require.Equal(t, "Amazon gift card", rows[1].(map[string]any)["payee"])
	match := l.alex.get("/merchants/transactions/" + spent["id"].(string)).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.MerchantMatchCharge, match["basis"])
	require.Equal(t, "112-0000006-0000006", match["order"].(map[string]any)["order_number"])

	// The same page read again tomorrow writes nothing twice, and a new
	// balance replaces the old.
	again := uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", strings.Replace(file, `"balance":"42.60"`, `"balance":"25.00"`, 1),
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), again["gift_card_rows"])
	require.Len(t, registerPage(l, "account_id="+giftCardAccount+"&date_field=posted")["items"].([]any), 2)
	for _, one := range l.alex.get("/accounts").requireStatus(http.StatusOK).list() {
		if one["id"] == giftCardAccount {
			require.Equal(t, "25.00", one["provider_balance"])
		}
	}
}

// syncedMerchantRow is an Amazon charge the bank delivered, rather than one a
// person typed: the kind the transaction automations fire for.
func syncedMerchantRow(l *ledger, date, amount, categoryID string) string {
	l.t.Helper()
	on, err := domain.ParseDate(date)
	require.NoError(l.t, err)
	txn := &store.Transaction{
		AccountID: l.id("card"), Date: on,
		Amount: domain.MustFromString(amount), Currency: "USD",
		StatementName: "AMAZON.COM*2K4D1R6Q3 AMZN.COM/BILL WA", Payee: "Amazon",
		Source: domain.SourceSync,
	}
	if categoryID != "" {
		txn.CategoryID = uuid.MustParse(categoryID)
	}
	require.NoError(l.t, l.env.DB.CreateTransaction(l.t.Context(), store.SpaceIDOf(l.id("space")), txn))
	return txn.ID.String()
}

func queuedRuns(l *ledger) []map[string]any {
	l.t.Helper()
	return l.alex.get("/assistant-automations/runs?status=queued").requireStatus(http.StatusOK).list()
}

func TestAnOrderThatArrivesAfterTheRowHasTheCheckLookAgain(t *testing.T) {
	// The bank delivered the charge on the 3rd and the check filed it as
	// Shopping from the history, because the order was not on file yet. When
	// the order lands, the row is looked at again with the items in view —
	// and again when a person matches it by hand.
	l := buildLedger(t)
	homeSupplies := l.alex.post("/categories", map[string]any{
		"name": "Home Supplies", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	shopping := l.alex.post("/categories", map[string]any{
		"name": "Shopping", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	arrived := syncedMerchantRow(l, "2026-08-03", "-40.00", shopping)
	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+arrived+`","category_id":"`+homeSupplies+
			`","summary":"A storage set and a cable on Casey's Amazon account"}`),
		answerReply("Home Supplies."))
	allowWrites(l, model)
	l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated)
	require.Empty(t, queuedRuns(l))

	account := merchantAccount(l, "Casey")
	uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	waiting := queuedRuns(l)
	require.Len(t, waiting, 1)
	require.Equal(t, domain.AutomationFiredByMatch, waiting[0]["fired_by"])
	require.Equal(t, arrived, waiting[0]["transaction_id"])

	// The same file again matches nothing new and queues nothing more.
	uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	require.Len(t, queuedRuns(l), 1)

	automations, err := NewAutomations(l.env)
	require.NoError(t, err)
	automations.Drain(t.Context())
	require.Empty(t, queuedRuns(l))
	done := l.alex.get("/assistant-automations/runs?status=succeeded").requireStatus(http.StatusOK).list()
	require.Len(t, done, 1)
	require.Equal(t, "model", done[0]["decided_by"], "the items are shown, so the history does not act alone")
	require.Equal(t, float64(1), done[0]["actions"])

	// Matching by hand is a person saying "this order": the check looks again.
	orders := l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	batteries := orders[0].(map[string]any)
	if batteries["order_number"] != "113-7654321-0000002" {
		batteries = orders[1].(map[string]any)
	}
	l.alex.post("/merchants/transactions/"+arrived+"/orders", map[string]any{"order_id": batteries["id"]}).
		requireStatus(http.StatusOK)
	waiting = queuedRuns(l)
	require.Len(t, waiting, 1)
	require.Equal(t, domain.AutomationFiredByMatch, waiting[0]["fired_by"])
}

func TestTheCheckCanBeRunOverEveryMatchedRowOnce(t *testing.T) {
	// The one-time pass: rows categorized before their orders were on file
	// are all offered to the check, and pressing it twice queues nothing more.
	l := buildLedger(t)
	first := syncedMerchantRow(l, "2026-08-03", "-40.00", "")
	second := syncedMerchantRow(l, "2026-08-11", "-16.00", "")
	syncedMerchantRow(l, "2026-08-11", "-87.00", "")
	account := merchantAccount(l, "Casey")
	uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	// Nothing to fire without a model. With one — and with changes switched
	// on — the category check is created on the household's behalf, so this
	// does not depend on somebody having set an automation up first.
	l.alex.post("/merchants/amazon/suggest-categories", nil).requireStatus(http.StatusConflict)
	allowWrites(l, newFakeModel(t, answerReply("nothing to say")))

	out := l.alex.post("/merchants/amazon/suggest-categories", nil).requireStatus(http.StatusAccepted).json()
	require.Equal(t, float64(2), out["rows"], "the stranger has no order")
	require.Equal(t, float64(2), out["queued"])
	waiting := queuedRuns(l)
	require.Len(t, waiting, 2)
	ids := []any{waiting[0]["transaction_id"], waiting[1]["transaction_id"]}
	require.ElementsMatch(t, []any{first, second}, ids)
	require.Equal(t, domain.AutomationFiredByManual, waiting[0]["fired_by"])

	again := l.alex.post("/merchants/amazon/suggest-categories", nil).requireStatus(http.StatusAccepted).json()
	require.Equal(t, float64(2), again["rows"])
	require.Equal(t, float64(0), again["queued"], "the rows are already waiting")
}

// twoItemOrder is a bank row matched to an order of two priced items, which is
// the shape a split proposal is judged against.
func twoItemOrder(l *ledger, first, second string) string {
	l.t.Helper()
	account := merchantAccount(l, "Alex")
	row := merchantRow(l, "2026-08-28", "-30.00")
	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", `{"source":"agentifi-amazon-extract","orders":[
	 {"order_id":"112-9000001-9000001","date":"2026-08-26","total":"30.00","currency":"USD","status":"Delivered","url":"",
	  "gift_card":"0.00","tax":"0.00","shipping":"0.00",
	  "items":[{"title":"`+first+`","asin":"B0FIRST","quantity":1,"price":"20.00","url":""},
	           {"title":"`+second+`","asin":"B0SECOND","quantity":1,"price":"10.00","url":""}]}],
	 "charges":[{"order_id":"112-9000001-9000001","date":"2026-08-27","amount":"-30.00","instrument":"Prime Visa ••••1234"}]}`,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	return row
}

func TestASplitOfAnMerchantRowIsNamedFromTheOrderNotFromTheModel(t *testing.T) {
	// Asked for the item's name the model paraphrases it, and sometimes sends
	// nothing at all — so the card proposes a category for a thing it cannot
	// name, and the memo it would write into the ledger is not the item's. The
	// order knows the answer and the amounts say which item each part is.
	l := buildLedger(t)
	row := twoItemOrder(l, "Wooden Toy Train Set", "Travel Umbrella, 2-Pack")

	model := newFakeModel(t,
		toolReply("split_transaction", `{"transaction_id":"`+row+`","summary":"Two items",
		  "splits":[{"amount":"-20.00","category_id":"`+l.str("food")+`","memo":""},
		            {"amount":"-10.00","category_id":"`+l.str("groceries")+`","memo":"A pouch"}]}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "split it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	splits := action["body"].(map[string]any)["splits"].([]any)
	require.Len(t, splits, 2)
	require.Equal(t, "Wooden Toy Train Set", splits[0].(map[string]any)["memo"])
	require.Equal(t, "Travel Umbrella, 2-Pack", splits[1].(map[string]any)["memo"])
	// The categories are still the model's: only the name it was copying is
	// taken out of its hands.
	require.Equal(t, l.str("food"), splits[0].(map[string]any)["category_id"])
}

func TestASplitThatDoesNotFollowTheOrderKeepsTheModelsOwnMemos(t *testing.T) {
	// A name attached to the wrong amount is worse than no name, so the
	// substitution only runs when each part is exactly one item's share.
	l := buildLedger(t)
	row := twoItemOrder(l, "Wooden Toy Train Set", "Travel Umbrella, 2-Pack")

	model := newFakeModel(t,
		toolReply("split_transaction", `{"transaction_id":"`+row+`","summary":"Halves",
		  "splits":[{"amount":"-15.00","category_id":"`+l.str("food")+`","memo":"Half of it"},
		            {"amount":"-15.00","category_id":"`+l.str("groceries")+`","memo":"The other half"}]}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "split it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	splits := action["body"].(map[string]any)["splits"].([]any)
	require.Equal(t, "Half of it", splits[0].(map[string]any)["memo"])
}

func TestACardSaysWhichRowItIsAboutAndWhatTheOrderWasFor(t *testing.T) {
	// "File this under Household" over a statement line nobody recognizes is
	// not a question anybody can answer. The row's own wording, and — where an
	// order stands behind it — what was actually bought.
	l := buildLedger(t)
	account := merchantAccount(l, "Alex")
	row := merchantRow(l, "2026-08-28", "-17.00")
	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", `{"source":"agentifi-amazon-extract","orders":[
	 {"order_id":"112-9000002-9000002","date":"2026-08-26","total":"17.00","currency":"USD","status":"Delivered","url":"",
	  "gift_card":"0.00","tax":"0.00","shipping":"0.00",
	  "items":[{"title":"Wooden Toy Train Set","asin":"B0TRAIN","quantity":1,"price":"17.00","url":""}]}],
	 "charges":[{"order_id":"112-9000002-9000002","date":"2026-08-27","amount":"-17.00","instrument":"Prime Visa ••••1234"}]}`,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	model := newFakeModel(t,
		toolReply("update_transaction", `{"transaction_id":"`+row+
			`","summary":"A toy","category_id":"`+l.str("groceries")+`"}`),
		answerReply("Proposed."))
	id := allowWrites(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "categorize it"}).requireStatus(http.StatusOK).json()
	action := body["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	about := action["about"].(map[string]any)
	require.Equal(t, "2026-08-28", about["date"])
	require.Equal(t, "-17.00", about["amount"])
	require.NotEmpty(t, about["statement_name"])
	items := about["items"].([]any)
	require.Len(t, items, 1)
	require.Equal(t, "Wooden Toy Train Set", items[0].(map[string]any)["title"])
}

// The two surfaces that go looking for Amazon rows on their own must agree
// about which rows those are.
//
// Disagreeing would mean the per-sync matcher takes pending charges while the
// bulk match refuses them, leaving a pending Amazon charge the sync cannot
// resolve invisible to the button meant to resolve it *and* missing from the
// count of what is waiting. A pending settles in place, so a match made
// against one survives; refusing it would hide the row rather than protect
// anything.
func TestAPendingMerchantRowIsOfferedToTheBulkMatchAndCounted(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")

	// Orders first, so the import's own pass has nothing to match and the bulk
	// endpoint is the thing under test.
	uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	pending := merchantRow(l, "2026-08-03", "-40.00")
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE transactions SET is_pending = true WHERE id = $1`, uuid.MustParse(pending))
	require.NoError(t, err)

	summary := l.alex.get("/merchants/amazon/summary").requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, summary["merchant_transactions"],
		"the row is waiting, whatever the bank calls its state")
	require.EqualValues(t, 0, summary["matched_transactions"])

	matched := l.alex.post("/merchants/amazon/match", nil).requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, matched["matched"])

	// And the match survives the settlement, which is why taking a pending row
	// is safe: the sync updates the row in place rather than writing a second.
	_, err = db(t).Pool().Exec(t.Context(),
		`UPDATE transactions SET is_pending = false WHERE id = $1`, uuid.MustParse(pending))
	require.NoError(t, err)
	l.alex.get("/merchants/transactions/" + pending).requireStatus(http.StatusOK)
}

// A forecast is not a purchase, on any of them.
func TestAForecastIsNeverOfferedToAnMerchantOrder(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	forecast := &store.Transaction{
		AccountID: l.id("card"), Date: domain.NewDate(2026, time.August, 3), Currency: "USD",
		Amount: domain.MustFromString("-40.00"), StatementName: "AMAZON.COM*2K4D1R6Q3 AMZN.COM/BILL WA",
		Payee: "Amazon", Source: domain.SourceSimplifiImport,
		EstimateStatus: store.ProjectedEstimate,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), forecast))

	uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	summary := l.alex.get("/merchants/amazon/summary").requireStatus(http.StatusOK).json()
	require.EqualValues(t, 0, summary["merchant_transactions"])

	matched := l.alex.post("/merchants/amazon/match", nil).requireStatus(http.StatusOK).json()
	require.EqualValues(t, 0, matched["matched"])
}

func threeItemOrder(l *ledger) (row string, groceries string, household string) {
	l.t.Helper()
	account := l.alex.post("/merchants/costco/accounts", map[string]any{"label": "Alex"}).
		requireStatus(http.StatusCreated).json()["id"].(string)
	row = l.alex.post("/transactions", map[string]any{
		"account_id": l.str("card"), "date": "2026-09-05", "amount": "-50.00",
		"payee": "Costco", "statement_name": "COSTCO WHSE #0123",
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	groceries = l.alex.post("/categories", map[string]any{
		"name": "Groceries", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	household = l.alex.post("/categories", map[string]any{
		"name": "Home Supplies", "kind": string(domain.CategoryExpense),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	uploadTo(l.alex, "/merchants/costco/imports", "pull.json", `{"source":"agentifi-costco-extract","orders":[
	 {"order_id":"21100-GROUPED-TEST","kind":"warehouse","date":"2026-09-03","total":"50.00",
	  "location":"Costco Springfield","tax":"3.00",
	  "items":[
		{"sku":"1234567","title":"KS ORG EGGS","quantity":1,"price":"9.98","total":"9.98"},
		{"sku":"2345678","title":"KS CHICKEN BRST","quantity":1,"price":"12.00","total":"12.00"},
		{"sku":"3456789","title":"PAPER TOWELS 12CT","quantity":1,"price":"25.02","total":"25.02"}
	  ]}],
	 "charges":[{"order_id":"21100-GROUPED-TEST","date":"2026-09-04","amount":"-50.00","instrument":"Visa ••••1234"}]}`,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)
	return row, groceries, household
}

func TestAGroupedSplitByByCategoryPassesTheGuardAndIsProposedCorrectly(t *testing.T) {
	// The model sees three items, two groceries and one household supply, and groups
	// them: one split for the groceries ($23.38 = eggs' share + chicken's
	// share) and one for the household supply ($26.62 = paper towels' share). The
	// guard accepts this because each group sums exactly to its items' shares.
	l := buildLedger(t)
	row, groceries, household := threeItemOrder(l)

	match := l.alex.get("/merchants/transactions/" + row).requireStatus(http.StatusOK).json()
	require.NotNil(t, match["order"], "no match — row %s: %v", row, match)
	require.Equal(t, "50.00", match["order"].(map[string]any)["card_total"])

	// The shares are computed by SplitByWeight over item prices against the
	// -50.00 charge: eggs 9.98, chicken 12.00, paper towels 25.02, total 47.00.
	// SplitByWeight gives [-10.62, -12.76, -26.62].
	eggShare := domain.MustFromString("-10.62")
	chickenShare := domain.MustFromString("-12.76")
	towelShare := domain.MustFromString("-26.62")
	groceryGroup := eggShare.Add(chickenShare) // -23.38
	suppliesGroup := towelShare                // -26.62

	model := newFakeModel(t,
		toolReply("split_transaction", fmt.Sprintf(
			`{"transaction_id":"%s","summary":"Groceries and household supplies",
			  "splits":[{"amount":"%s","category_id":"%s","memo":"Organic Eggs 24ct, Chicken Breast 3lb"},
			            {"amount":"%s","category_id":"%s","memo":"Paper Towels 12ct"}]}`,
			row, groceryGroup.String(), groceries, suppliesGroup.String(), household)),
		answerReply("Proposed."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": row}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	require.Equal(t, float64(1), run["actions"])

	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "PUT", action["method"], "the grouped split was accepted, not refused")
	splits := action["body"].(map[string]any)["splits"].([]any)
	require.Len(t, splits, 2, "two groups, not three per-item splits")
	require.Equal(t, groceries, splits[0].(map[string]any)["category_id"])
	require.Equal(t, household, splits[1].(map[string]any)["category_id"])
}

func TestAGroupedSplitThatDoesNotSumToTheItemSharesIsRefused(t *testing.T) {
	// A split whose amounts are not sums of the item shares (the model
	// rounded, or invented a line for tax) is refused.
	l := buildLedger(t)
	row, groceries, household := threeItemOrder(l)

	model := newFakeModel(t,
		// Invented amounts that don't match any group of shares.
		toolReply("split_transaction", fmt.Sprintf(
			`{"transaction_id":"%s","summary":"Wrong amounts",
			  "splits":[{"amount":"-25.00","category_id":"%s","memo":"Groceries"},
			            {"amount":"-22.00","category_id":"%s","memo":"Household"},
			            {"amount":"-3.00","category_id":"%s","memo":"Tax"}]}`,
			row, groceries, household, groceries)),
		toolReply("update_transaction", fmt.Sprintf(
			`{"transaction_id":"%s","summary":"Groceries","category_id":"%s"}`, row, groceries)),
		answerReply("Filed under Groceries."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85,
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": row}).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.AutomationRunSucceeded, run["status"], run["error"])
	// The grouped split was refused, so the model fell through to update_transaction.
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, "PATCH", action["method"], "the bad split was refused; the category change stood")
}

// A return, from Amazon's own record of it to the credit on the card.
//
// The common shape: an order of two things, one of them
// sent back. The money comes back days later as a credit the bank files under
// nothing, and the question is which of the two things it gives back — because
// the answer is the category the money has to return to. And the other half:
// a refund Amazon credits to a gift card balance, which no bank row will ever
// explain and for which none is invented.
const merchantReturnsJSON = `{"source":"agentifi-amazon-extract","account_hint":"Casey","orders":[
 {"order_id":"115-0000009-0000009","date":"2026-08-20","total":"41.00","currency":"USD",
  "status":"Return complete","url":"","gift_card":"0.00","tax":"0.00","shipping":"0.00",
  "items":[{"title":"USB-C Cable","asin":"B0CABLE","quantity":1,"price":"13.00","url":""},
           {"title":"Glass Storage Set","asin":"B0STORAGE","quantity":1,"price":"28.00","url":""}]}],
 "charges":[{"order_id":"115-0000009-0000009","date":"2026-08-21","amount":"-41.00","instrument":"Prime Visa ••••1234"}],
 "refunds":[
  {"order_id":"115-0000009-0000009","asin":"B0STORAGE","title":"Glass Storage Set",
   "quantity":1,"date":"2026-09-02","amount":"28.00","instrument":"Prime Visa ••••1234",
   "status":"Refund issued"},
  {"order_id":"115-0000009-0000009","asin":"B0CABLE","title":"USB-C Cable","quantity":1,
   "date":"2026-09-03","amount":"13.00","instrument":"Amazon Gift Card","status":"Refund issued"}]}`

func TestACreditIsMatchedToTheReturnAndTakesTheReturnedItemsCategory(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	purchase := merchantRow(l, "2026-08-21", "-41.00")

	report := uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", merchantReturnsJSON,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(2), report["refunds"], "the returns page's two, and the payments page had none of its own")
	require.Equal(t, float64(2), report["new_refunds"])

	// The purchase is divided by item, and the household files each line.
	txn := l.alex.get("/transactions/" + purchase).requireStatus(http.StatusOK).json()
	splits := txn["splits"].([]any)
	require.Len(t, splits, 2)
	require.Equal(t, "USB-C Cable", splits[0].(map[string]any)["memo"])
	l.alex.put("/transactions/"+purchase+"/splits", map[string]any{"splits": []any{
		map[string]any{"amount": "-13.00", "category_id": l.str("food"), "memo": "USB-C Cable"},
		map[string]any{"amount": "-28.00", "category_id": l.str("groceries"), "memo": "Glass Storage Set"},
	}}).requireStatus(http.StatusOK)

	// The credit lands on the card a few days after Amazon issued the refund.
	credit := merchantRow(l, "2026-09-06", "28.00")
	match := l.alex.get("/merchants/transactions/" + credit).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.MerchantMatchRefund, match["basis"])
	require.Equal(t, "115-0000009-0000009", match["order"].(map[string]any)["order_number"])
	refund := match["refund"].(map[string]any)
	require.Equal(t, "B0STORAGE", refund["sku"], "the credit names the line that came back")
	require.Equal(t, "28.00", refund["amount"])
	require.Equal(t, false, refund["to_gift_card"])

	// The money goes back to the category the returned line was filed under,
	// not to the other line's and not to income.
	credited := l.alex.get("/transactions/" + credit).requireStatus(http.StatusOK).json()
	require.Equal(t, l.str("groceries"), credited["category_id"], "the storage set came back, not the cable")

	// And the link is the same one a person makes by hand, so every
	// calculation reads the credit as spending returned.
	links := l.alex.get("/refunds/transactions/" + credit).requireStatus(http.StatusOK).json()
	refunded := links["refunds"].([]any)
	require.Len(t, refunded, 1)
	require.Equal(t, purchase, refunded[0].(map[string]any)["id"], "the way back to the purchase")

	// The gift card refund is on the order and on no bank row. Nothing is
	// invented for it: the money never reached an account the bank can see.
	orders := l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	refunds := orders[0].(map[string]any)["refunds"].([]any)
	require.Len(t, refunds, 2)
	byItem := map[string]map[string]any{}
	for _, one := range refunds {
		row := one.(map[string]any)
		byItem[row["sku"].(string)] = row
	}
	require.Equal(t, true, byItem["B0CABLE"]["to_gift_card"])
	require.Nil(t, byItem["B0CABLE"]["transaction_id"], "a gift card refund waits for no bank row")
	require.Equal(t, credit, byItem["B0STORAGE"]["transaction_id"])

	// A second credit for the same figure is not the same return twice.
	twin := merchantRow(l, "2026-09-07", "28.00")
	l.alex.get("/merchants/transactions/" + twin).requireStatus(http.StatusNotFound)
}
