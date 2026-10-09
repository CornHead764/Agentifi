package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// A merchant's return, linked to the purchase it gives back without anyone
// picking it: the credit and the purchase are both matched to the order, so
// the order says which row refunded which. Orders and figures are invented.

const refundOrder = "115-0000007-0000007"

// refundPull is one order of $41.00 charged to the card on 2026-08-21, with
// the given refunds (date, amount) and the payments page's own listing of
// each as a credit.
func refundPull(refunds ...[2]string) string {
	charges := fmt.Sprintf(`{"order_id":%q,"date":"2026-08-21","amount":"-41.00","instrument":"Prime Visa ••••1234"}`, refundOrder)
	listed := ""
	for _, one := range refunds {
		charges += fmt.Sprintf(`,{"order_id":%q,"date":%q,"amount":%q,"instrument":"Prime Visa ••••1234"}`,
			refundOrder, one[0], one[1])
		if listed != "" {
			listed += ","
		}
		listed += fmt.Sprintf(`{"order_id":%q,"asin":"B0CABLE","title":"USB-C Cable","quantity":1,
			"date":%q,"amount":%q,"instrument":"Prime Visa ••••1234","status":"Refund issued"}`,
			refundOrder, one[0], one[1])
	}
	return fmt.Sprintf(`{"source":"agentifi-amazon-extract","account_hint":"Casey","orders":[
	 {"order_id":%q,"date":"2026-08-20","total":"41.00","currency":"USD","status":"Closed",
	  "url":"","gift_card":"0.00","tax":"0.00","shipping":"0.00",
	  "items":[{"title":"USB-C Cable","asin":"B0CABLE","quantity":1,"price":"41.00","url":""}]}],
	 "charges":[%s],"refunds":[%s]}`, refundOrder, charges, listed)
}

func chargesGivenBack(l *ledger, credit string) []string {
	l.t.Helper()
	links := l.alex.get("/refunds/transactions/" + credit).requireStatus(http.StatusOK).json()
	var ids []string
	for _, one := range links["refunds"].([]any) {
		ids = append(ids, one.(map[string]any)["id"].(string))
	}
	return ids
}

func TestAnOrderRefundedAtOnceKeepsItsReceiptOnThePurchaseAndTheCreditLinksBack(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	purchase := merchantRow(l, "2026-08-22", "-41.00")
	credit := merchantRow(l, "2026-08-24", "41.00")

	// The credit is the newer row, so the matcher reaches it first.
	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", refundPull([2]string{"2026-08-21", "41.00"}),
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	bought := l.alex.get("/merchants/transactions/" + purchase).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.MerchantMatchCharge, bought["basis"])
	require.Equal(t, refundOrder, bought["order"].(map[string]any)["order_number"])

	refunded := l.alex.get("/merchants/transactions/" + credit).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.MerchantMatchRefund, refunded["basis"])

	require.Equal(t, []string{purchase}, chargesGivenBack(l, credit))
	links := l.alex.get("/refunds/transactions/" + purchase).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.RefundStateFull, links["refund_state"])
}

func TestAPartialRefundLinksToThePurchaseItCameOutOf(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	purchase := merchantRow(l, "2026-08-22", "-41.00")
	credit := merchantRow(l, "2026-09-06", "15.00")

	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", refundPull([2]string{"2026-09-02", "15.00"}),
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	require.Equal(t, []string{purchase}, chargesGivenBack(l, credit))
}

func TestTwoPartialRefundsShareOnePurchase(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	purchase := merchantRow(l, "2026-08-22", "-41.00")
	first := merchantRow(l, "2026-09-06", "25.00")
	second := merchantRow(l, "2026-09-12", "10.00")

	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", refundPull(
		[2]string{"2026-09-02", "25.00"}, [2]string{"2026-09-08", "10.00"}),
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	require.Equal(t, []string{purchase}, chargesGivenBack(l, first))
	require.Equal(t, []string{purchase}, chargesGivenBack(l, second))
	links := l.alex.get("/refunds/transactions/" + purchase).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.RefundStatePartial, links["refund_state"], "$35.00 of $41.00 came back")
	require.Len(t, links["refunded_by"], 2)
}

func TestARefundLargerThanTheRoomLeftIsNotLinked(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	merchantRow(l, "2026-08-22", "-41.00")
	first := merchantRow(l, "2026-09-06", "30.00")
	second := merchantRow(l, "2026-09-12", "20.00")

	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", refundPull(
		[2]string{"2026-09-02", "30.00"}, [2]string{"2026-09-08", "20.00"}),
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	require.Len(t, chargesGivenBack(l, first), 1)
	require.Empty(t, chargesGivenBack(l, second), "only $11.00 of the purchase was left to give back")
}

func TestARemovedRefundLinkIsNotMadeAgainByTheNextPull(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	purchase := merchantRow(l, "2026-08-22", "-41.00")
	credit := merchantRow(l, "2026-09-06", "15.00")
	pull := refundPull([2]string{"2026-09-02", "15.00"})
	params := map[string]string{"merchant_account_id": account}

	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", pull, params).requireStatus(http.StatusOK)
	require.Equal(t, []string{purchase}, chargesGivenBack(l, credit))

	l.alex.del("/refunds/transactions/" + credit + "/charges/" + purchase).requireStatus(http.StatusNoContent)
	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", pull, params).requireStatus(http.StatusOK)
	require.Empty(t, chargesGivenBack(l, credit))
}

func TestAHandMadeRefundLinkIsNotAddedToByTheMatch(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	merchantRow(l, "2026-08-22", "-41.00")
	other := merchantRow(l, "2026-08-23", "-12.00")
	credit := merchantRow(l, "2026-09-06", "10.00")
	l.alex.post("/refunds/transactions/"+credit+"/charges",
		map[string]any{"charge_transaction_id": other}).requireStatus(http.StatusOK)

	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", refundPull([2]string{"2026-09-02", "10.00"}),
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	require.Equal(t, []string{other}, chargesGivenBack(l, credit))
}

// totalPull is the same order with no refund record at all, only what its
// invoice said was refunded, and the gift card balance's activity lines.
func totalPull(refunded string, balance ...[3]string) string {
	lines := ""
	for _, one := range balance {
		if lines != "" {
			lines += ","
		}
		lines += fmt.Sprintf(`{"date":%q,"description":%q,"amount":%q,"order_id":""}`, one[0], one[1], one[2])
	}
	return fmt.Sprintf(`{"source":"agentifi-amazon-extract","account_hint":"Casey","orders":[
	 {"order_id":%q,"date":"2026-08-20","total":"41.00","currency":"USD","status":"Closed",
	  "url":"","gift_card":"0.00","tax":"0.00","shipping":"0.00",
	  "items":[{"title":"USB-C Cable","asin":"B0CABLE","quantity":1,"price":"41.00","url":""}]}],
	 "charges":[{"order_id":%q,"date":"2026-08-21","amount":"-41.00","instrument":"Prime Visa ••••1234"}],
	 "refunds":[],
	 "refund_totals":[{"order_id":%q,"amount":%q,"date":"2026-09-20"}],
	 "gift_card":{"balance":"60.00","activity":[%s]}}`, refundOrder, refundOrder, refundOrder, refunded, lines)
}

// refundedBy is each credit linked to a charge, as account name and amount.
func refundedBy(l *ledger, charge string) []string {
	l.t.Helper()
	links := l.alex.get("/refunds/transactions/" + charge).requireStatus(http.StatusOK).json()
	var out []string
	for _, one := range links["refunded_by"].([]any) {
		row := one.(map[string]any)
		out = append(out, fmt.Sprintf("%s %s", row["account_name"], row["amount"]))
	}
	return out
}

// A refund to the gift card balance names no order on the balance's page; the
// order's invoice saying what was refunded is what ties the line to it.
func TestAGiftCardRefundLineIsLinkedThroughTheOrdersRefundTotal(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	purchase := merchantRow(l, "2026-08-22", "-41.00")

	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", totalPull("15.00",
		[3]string{"2026-09-05", "Refund from Amazon.com order", "15.00"},
		[3]string{"2026-09-06", "Gift Card added Claim code: xxxxxxxxxxABCD", "15.00"}),
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	require.Equal(t, []string{"Amazon gift card · Casey 15.00"}, refundedBy(l, purchase),
		"the refund line, and not the reload of the same amount")
}

// A bank credit the payments page never listed is matched to the refund
// total too, but only whole: a card credit may be a reward.
func TestABankCreditIsLinkedThroughAWholeRefundTotalOnly(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	purchase := merchantRow(l, "2026-08-22", "-41.00")
	whole := merchantRow(l, "2026-09-06", "15.00")
	part := merchantRow(l, "2026-09-07", "5.00")

	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", totalPull("15.00"),
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	require.Equal(t, []string{purchase}, chargesGivenBack(l, whole))
	matched := l.alex.get("/merchants/transactions/" + whole).requireStatus(http.StatusOK).json()
	require.Equal(t, domain.MerchantMatchRefundTotal, matched["basis"])
	require.Empty(t, chargesGivenBack(l, part))
}

// The payments page's own listing of a refund to the gift card is matched to
// the balance's line, which the bank never saw.
func TestARefundRecordToTheGiftCardMatchesTheBalancesLine(t *testing.T) {
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	purchase := merchantRow(l, "2026-08-22", "-41.00")
	pull := strings.Replace(totalPull("0.00",
		[3]string{"2026-09-05", "Refund from Amazon.com order", "12.00"}),
		`"refunds":[]`, fmt.Sprintf(`"refunds":[{"order_id":%q,"asin":"","title":"","quantity":1,
		"date":"2026-09-04","amount":"12.00","instrument":"Amazon Gift Card","status":""}]`, refundOrder), 1)

	uploadTo(l.alex, "/merchants/amazon/imports", "pull.json", pull,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK)

	require.Equal(t, []string{"Amazon gift card · Casey 12.00"}, refundedBy(l, purchase))
}
