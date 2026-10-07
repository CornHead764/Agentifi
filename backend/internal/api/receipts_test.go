package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Receipts an account requires (docs/data-model.md): an HSA holds every
// spending row to a document behind it, a person may settle one as needing
// none, and the register counts and filters the rows still missing one.

// hsaLedger is buildLedger plus a health savings account with one row of each
// kind the rule tells apart. Every figure is invented.
func hsaLedger(t *testing.T) *ledger {
	t.Helper()
	l := buildLedger(t)
	ctx := t.Context()
	space := store.SpaceIDOf(l.id("space"))

	hsa := &store.Account{
		Name: "Health Savings", Kind: domain.KindCash, Type: "hsa", Currency: "USD",
		IncludeInNetWorth: true,
	}
	require.NoError(t, l.env.DB.CreateAccount(ctx, space, hsa))
	l.ids["hsa"] = hsa.ID

	transfer := &store.Category{Name: "Transfer", Kind: domain.CategoryTransfer, IsUserAssignable: true}
	require.NoError(t, l.env.DB.CreateCategory(ctx, space, transfer))

	rows := []struct {
		key      string
		amount   string
		payee    string
		category *store.Category
	}{
		{"hsa_pharmacy", "-42.00", "Corner Pharmacy", nil},
		{"hsa_dentist", "-180.00", "Bright Smile Dental", nil},
		{"hsa_clinic", "-60.00", "Walk-in Clinic", nil},
		{"hsa_fee", "-2.00", "Monthly Fee", nil},
		{"hsa_interest", "0.37", "Interest Paid", nil},
		{"hsa_contribution", "150.00", "Contribution", nil},
		{"hsa_to_investments", "-500.00", "Transfer to Investments", transfer},
	}
	for day, row := range rows {
		txn := &store.Transaction{
			AccountID: hsa.ID, Date: domain.NewDate(2026, time.March, day+2),
			Amount: domain.MustFromString(row.amount), Currency: "USD",
			StatementName: row.payee, Payee: row.payee, Source: domain.SourceSync,
		}
		if row.category != nil {
			txn.CategoryID = row.category.ID
		}
		require.NoError(t, l.env.DB.CreateTransaction(ctx, space, txn))
		l.ids[row.key] = txn.ID
	}
	attachTo(l, l.str("hsa_dentist"), "dentist.png", pngBytes())
	// Filed as a receipt the way a settled bill or a matched order files one.
	fileReceipt(l, "hsa_clinic")
	return l
}

func missingReceiptFilter(l *ledger) string {
	l.t.Helper()
	return l.alex.post("/filters", map[string]any{
		"scope": "ad_hoc",
		"items": []map[string]any{
			{"field": "is_missing_receipt", "operator": "is_true", "state": true},
		},
	}).requireStatus(http.StatusCreated).json()["id"].(string)
}

func missingReceipts(l *ledger) map[string]any {
	l.t.Helper()
	return registerPage(l, "filter_id="+missingReceiptFilter(l)+"&limit=500")
}

func receiptStatuses(l *ledger) map[string]any {
	l.t.Helper()
	out := map[string]any{}
	for _, item := range registerPage(l, "limit=500")["items"].([]any) {
		row := item.(map[string]any)
		out[row["id"].(string)] = row["receipt_status"]
	}
	return out
}

func TestEachHSARowSaysWhereItStandsOnItsReceipt(t *testing.T) {
	l := hsaLedger(t)
	statuses := receiptStatuses(l)

	require.Equal(t, "missing", statuses[l.str("hsa_pharmacy")])
	require.Equal(t, "on_file", statuses[l.str("hsa_dentist")])
	require.Equal(t, "on_file", statuses[l.str("hsa_clinic")], "a bill's or an order's receipt counts")
	require.Equal(t, "missing", statuses[l.str("hsa_fee")])
	for _, name := range []string{"hsa_interest", "hsa_contribution", "hsa_to_investments", "august_groceries"} {
		require.Nil(t, statuses[l.str(name)], name)
	}

	one := l.alex.get("/transactions/" + l.str("hsa_pharmacy")).requireStatus(http.StatusOK).json()
	require.Equal(t, "missing", one["receipt_status"])
}

func TestTheRegisterCountsExactlyTheRowsMissingAReceipt(t *testing.T) {
	l := hsaLedger(t)
	page := missingReceipts(l)
	require.Equal(t, map[string]bool{l.str("hsa_pharmacy"): true, l.str("hsa_fee"): true}, idsIn(page))
	require.EqualValues(t, 2, page["count"])
	require.Equal(t, "-44.00", page["total"])

	attachTo(l, l.str("hsa_pharmacy"), "pharmacy.png", pngBytes())
	require.EqualValues(t, 1, missingReceipts(l)["count"])
}

func TestARowSaidToNeedNoReceiptLeavesTheCount(t *testing.T) {
	l := hsaLedger(t)
	updated := l.alex.patch("/transactions/"+l.str("hsa_fee"), map[string]any{"receipt_not_needed": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "not_needed", updated["receipt_status"])
	require.Equal(t, true, updated["receipt_not_needed"])
	require.Equal(t, map[string]bool{l.str("hsa_pharmacy"): true}, idsIn(missingReceipts(l)))

	l.alex.patch("/transactions/"+l.str("hsa_fee"), map[string]any{"receipt_not_needed": false}).
		requireStatus(http.StatusOK)
	require.EqualValues(t, 2, missingReceipts(l)["count"])

	l.as("vera").patch("/transactions/"+l.str("hsa_fee"), map[string]any{"receipt_not_needed": true}).
		requireStatus(http.StatusForbidden)
}

func TestTheAccountSettingDecidesWhichAccountsAreHeldToReceipts(t *testing.T) {
	l := hsaLedger(t)
	account := func(name string) map[string]any {
		return l.alex.get("/accounts/" + l.str(name)).requireStatus(http.StatusOK).json()
	}
	require.Equal(t, true, account("hsa")["requires_receipts"])
	require.Equal(t, false, account("checking")["requires_receipts"])

	l.alex.patch("/accounts/"+l.str("hsa"), map[string]any{"requires_receipts": false}).
		requireStatus(http.StatusOK)
	require.EqualValues(t, 0, missingReceipts(l)["count"])

	// A card kept for eligible expenses: every charge on it, refunds aside.
	l.alex.patch("/accounts/"+l.str("card"), map[string]any{"requires_receipts": true}).
		requireStatus(http.StatusOK)
	require.Equal(t, map[string]bool{l.str("card_charge"): true}, idsIn(missingReceipts(l)))

	// Null goes back to what the type implies.
	l.alex.patch("/accounts/"+l.str("hsa"), map[string]any{"requires_receipts": nil}).
		requireStatus(http.StatusOK)
	require.Equal(t, true, account("hsa")["requires_receipts"])
	require.EqualValues(t, 3, missingReceipts(l)["count"])

	created := l.alex.post("/accounts", map[string]any{
		"name": "Flexible Spending", "kind": "cash", "type": "checking", "currency": "USD",
		"requires_receipts": true,
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, true, created["requires_receipts"])
}

func TestNothingThatRunsAsARowArrivesMatchesOnAMissingReceipt(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/rules", map[string]any{
		"name": "Missing receipts",
		"conditions": []map[string]any{
			{"field": "is_missing_receipt", "operator": "is_true", "state": true},
		},
		"actions": map[string]any{"set_is_reviewed": true},
	}).requireStatus(http.StatusUnprocessableEntity)
}

// heicBytes is the leading ftyp box of an iPhone photo, then filler.
func heicBytes() []byte {
	box := []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c', 0, 0, 0, 0,
		'm', 'i', 'f', '1', 'h', 'e', 'i', 'c'}
	return append(box, []byte("not a real image")...)
}

func TestAPhoneCameraHEICIsKeptAndServedAsItself(t *testing.T) {
	l := hsaLedger(t)
	created := attachTo(l, l.str("hsa_pharmacy"), "IMG_0001.HEIC", heicBytes())
	require.Equal(t, "image/heic", created["content_type"])
	require.Equal(t, "IMG_0001.heic", created["filename"])

	content := l.alex.get("/documents/" + created["id"].(string) + "/content?download=true").
		requireStatus(http.StatusOK)
	require.Equal(t, "image/heic", content.Header().Get("Content-Type"))
	require.Equal(t, heicBytes(), content.Body.Bytes())
	require.Equal(t, "on_file", receiptStatuses(l)[l.str("hsa_pharmacy")])
}

func TestTheAssistantFindsTheRowsMissingAReceipt(t *testing.T) {
	l := hsaLedger(t)
	model := newFakeModel(t,
		toolReply("search_transactions",
			`{"from":"2000-01-01","to":"2100-01-01","missing_receipt":true}`),
		answerReply("done"))
	id := configure(l, model)

	body := l.alex.post("/assistant/conversations/"+id+"/ask",
		map[string]any{"question": "which HSA rows need a receipt?"}).
		requireStatus(http.StatusOK).json()
	messages := body["conversation"].(map[string]any)["messages"].([]any)

	var page struct {
		Count        int `json:"count"`
		Transactions []struct {
			ID      string `json:"id"`
			Receipt string `json:"receipt"`
		} `json:"transactions"`
	}
	require.NoError(t, json.Unmarshal(
		[]byte(messages[1].(map[string]any)["content"].(string)), &page))
	require.Equal(t, 2, page.Count)
	found := map[string]string{}
	for _, row := range page.Transactions {
		found[row.ID] = row.Receipt
	}
	require.Equal(t, map[string]string{
		l.str("hsa_pharmacy"): "missing", l.str("hsa_fee"): "missing",
	}, found)
}
