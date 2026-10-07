package domain_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func amt(s string) domain.Money {
	m, err := domain.FromString(s)
	if err != nil {
		panic(err)
	}
	return m
}

func day(s string) domain.Date {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestAChargeThatAgreesToTheCentIsTheOrder(t *testing.T) {
	orders := []domain.MerchantOrderFacts{
		{Ref: "a", OrderedOn: day("2026-08-01"), Total: amt("45.00")},
		{Ref: "b", OrderedOn: day("2026-08-01"), Total: amt("45.00")},
	}
	charges := []domain.MerchantChargeFacts{
		{OrderRef: "b", ChargedOn: day("2026-08-02"), Amount: amt("-45.00")},
	}
	match, ok := domain.MatchMerchantOrder(amt("-45.00"), day("2026-08-03"), orders, charges, nil)
	require.True(t, ok)
	require.Equal(t, "b", match.OrderRef, "the charge record picks between two identical totals")
	require.Equal(t, domain.MerchantMatchCharge, match.Basis)
	require.InDelta(t, 0.98, match.Confidence, 0.001)
}

func TestAnOrderTotalMatchesWithinTheOrderWindow(t *testing.T) {
	orders := []domain.MerchantOrderFacts{
		{Ref: "a", OrderedOn: day("2026-08-01"), Total: amt("45.00")},
	}
	match, ok := domain.MatchMerchantOrder(amt("-45.00"), day("2026-08-04"), orders, nil, nil)
	require.True(t, ok)
	require.Equal(t, domain.MerchantMatchOrderTotal, match.Basis)

	_, ok = domain.MatchMerchantOrder(amt("-45.00"), day("2026-09-04"), orders, nil, nil)
	require.False(t, ok, "a month later is a different purchase")
	_, ok = domain.MatchMerchantOrder(amt("-45.01"), day("2026-08-04"), orders, nil, nil)
	require.False(t, ok, "a cent off is not a match")
}

func TestAnOrderChargedPerShipmentMatchesEachPart(t *testing.T) {
	orders := []domain.MerchantOrderFacts{
		{Ref: "a", OrderedOn: day("2026-08-01"), Total: amt("60.00"),
			Shipments: []domain.Money{amt("25.00"), amt("35.00")},
			Items:     []domain.Money{amt("10.00"), amt("15.00"), amt("35.00")}},
	}
	part, ok := domain.MatchMerchantOrder(amt("-25.00"), day("2026-08-02"), orders, nil, nil)
	require.True(t, ok)
	require.Equal(t, domain.MerchantMatchShipment, part.Basis)

	item, ok := domain.MatchMerchantOrder(amt("-15.00"), day("2026-08-02"), orders, nil, nil)
	require.True(t, ok)
	require.Equal(t, domain.MerchantMatchItem, item.Basis)
	require.Less(t, item.Confidence, part.Confidence)
}

func TestTwoOrdersSameDaySameAmountMatchWithLessConfidence(t *testing.T) {
	orders := []domain.MerchantOrderFacts{
		{Ref: "a", OrderedOn: day("2026-08-01"), Total: amt("13.00")},
		{Ref: "b", OrderedOn: day("2026-08-01"), Total: amt("13.00")},
	}
	match, ok := domain.MatchMerchantOrder(amt("-13.00"), day("2026-08-02"), orders, nil, nil)
	require.True(t, ok)
	require.Equal(t, "a", match.OrderRef, "deterministic")
	require.InDelta(t, 0.7, match.Confidence, 0.001)
}

func TestARefundMatchesOnlyARefundCharge(t *testing.T) {
	orders := []domain.MerchantOrderFacts{
		{Ref: "a", OrderedOn: day("2026-08-01"), Total: amt("30.00")},
	}
	_, ok := domain.MatchMerchantOrder(amt("30.00"), day("2026-08-10"), orders, nil, nil)
	require.False(t, ok, "money in has no order total to agree with")

	charges := []domain.MerchantChargeFacts{
		{OrderRef: "a", ChargedOn: day("2026-08-10"), Amount: amt("30.00")},
	}
	match, ok := domain.MatchMerchantOrder(amt("30.00"), day("2026-08-11"), orders, charges, nil)
	require.True(t, ok)
	require.Equal(t, "a", match.OrderRef)
}

func TestMerchantWording(t *testing.T) {
	require.True(t, domain.IsAmazonWording("AMAZON.COM*2K4D1R6Q3 AMZN.COM/BILL", "Amazon"))
	require.True(t, domain.IsAmazonWording("AMZN Mktp US*1A2B3C", ""))
	require.False(t, domain.IsAmazonWording("COSTCO WHSE #0000", "Costco"))
}

func TestAGiftCardIsNotACardTheBankKnows(t *testing.T) {
	require.True(t, domain.IsGiftCardInstrument(""))
	require.True(t, domain.IsGiftCardInstrument("Amazon Gift Card"))
	require.True(t, domain.IsGiftCardInstrument("Gift Card Balance"))
	require.False(t, domain.IsGiftCardInstrument("Amazon Visa ••••1234"))
	require.False(t, domain.IsGiftCardInstrument("Discover ••••5678"))
}

func TestAnOrderPartlyPaidByGiftCardMatchesWhatTheCardWasCharged(t *testing.T) {
	// $50.00 order, $20.00 of it from a gift card balance: the bank saw $30.00.
	orders := []domain.MerchantOrderFacts{
		{Ref: "a", OrderedOn: day("2026-04-11"), Total: amt("50.00"), GiftCard: amt("20.00")},
	}
	require.Equal(t, amt("30.00"), orders[0].CardTotal())
	match, ok := domain.MatchMerchantOrder(amt("-30.00"), day("2026-04-12"), orders, nil, nil)
	require.True(t, ok)
	require.Equal(t, domain.MerchantMatchOrderTotal, match.Basis)
	_, ok = domain.MatchMerchantOrder(amt("-50.00"), day("2026-04-12"), orders, nil, nil)
	require.False(t, ok, "nothing the bank saw came to the full total")
}

func TestAnOrderAGiftCardPaidInFullMatchesNothing(t *testing.T) {
	orders := []domain.MerchantOrderFacts{
		{Ref: "a", OrderedOn: day("2026-02-01"), Total: amt("17.00"), GiftCard: amt("17.00"),
			Items: []domain.Money{amt("17.00")}},
	}
	require.True(t, orders[0].CardTotal().IsZero())
	_, ok := domain.MatchMerchantOrder(amt("-17.00"), day("2026-02-02"), orders, nil, nil)
	require.False(t, ok)
}

func TestAChargeAlreadyMatchedIsNotOfferedToASecondBankRow(t *testing.T) {
	orders := []domain.MerchantOrderFacts{
		{Ref: "a", OrderedOn: day("2026-04-11"), Total: amt("10.00")},
		{Ref: "b", OrderedOn: day("2026-04-13"), Total: amt("10.00")},
	}
	charges := []domain.MerchantChargeFacts{
		{OrderRef: "a", ChargedOn: day("2026-04-11"), Amount: amt("-10.00")},
		{OrderRef: "b", ChargedOn: day("2026-04-13"), Amount: amt("-10.00")},
	}
	first, ok := domain.MatchMerchantOrder(amt("-10.00"), day("2026-04-12"), orders, charges, nil)
	require.True(t, ok)
	require.Equal(t, "a", first.OrderRef)
	second, ok := domain.MatchMerchantOrder(amt("-10.00"), day("2026-04-13"), orders, charges,
		[]domain.MerchantMatch{first})
	require.True(t, ok)
	require.Equal(t, "b", second.OrderRef)
	require.Equal(t, 0.98, second.Confidence, "with a taken, b is the only candidate: no tie")
	_, ok = domain.MatchMerchantOrder(amt("-10.00"), day("2026-04-14"), orders, charges,
		[]domain.MerchantMatch{first, second})
	require.False(t, ok, "both charges are spoken for")
}

func TestAnOrderMatchedInFullIsNotOfferedAgainByItsTotal(t *testing.T) {
	orders := []domain.MerchantOrderFacts{
		{Ref: "a", OrderedOn: day("2026-06-28"), Total: amt("24.00")},
	}
	first, ok := domain.MatchMerchantOrder(amt("-24.00"), day("2026-06-28"), orders, nil, nil)
	require.True(t, ok)
	_, ok = domain.MatchMerchantOrder(amt("-24.00"), day("2026-07-01"), orders, nil, []domain.MerchantMatch{first})
	require.False(t, ok)
}

func TestSplittingAChargeAcrossItemsIsExactToTheCent(t *testing.T) {
	// Three items at $26.00, $14.00 and $10.00 came to $53.50 after tax; the
	// $3.50 of tax lands on them in proportion, and the parts sum exactly.
	parts := domain.SplitByWeight(amt("-53.50"), []domain.Money{amt("26.00"), amt("14.00"), amt("10.00")})
	require.Len(t, parts, 3)
	require.Equal(t, amt("-53.50"), domain.Total(parts...))
	require.Equal(t, amt("-27.82"), parts[0])
	require.Equal(t, amt("-14.98"), parts[1])
	require.Equal(t, amt("-10.70"), parts[2])

	// A refund splits the same way, positive.
	refund := domain.SplitByWeight(amt("53.50"), []domain.Money{amt("26.00"), amt("14.00"), amt("10.00")})
	require.Equal(t, amt("53.50"), domain.Total(refund...))
	require.True(t, refund[0].IsPositive())

	// Three equal items and a cent that will not divide: one of them gets it.
	thirds := domain.SplitByWeight(amt("-10.00"), []domain.Money{amt("1"), amt("1"), amt("1")})
	require.Equal(t, amt("-10.00"), domain.Total(thirds...))
	require.Equal(t, amt("-3.34"), thirds[0])
	require.Equal(t, amt("-3.33"), thirds[1])

	require.Nil(t, domain.SplitByWeight(amt("-10.00"), nil))
	require.Nil(t, domain.SplitByWeight(amt("-10.00"), []domain.Money{amt("0"), amt("0")}))
	require.Nil(t, domain.SplitByWeight(amt("-10.00"), []domain.Money{amt("5"), amt("-1")}))
}

func TestAnItemsMemoKeepsTheHeadAndCutsOnARune(t *testing.T) {
	short := "Wooden Toy Train Set"
	if got := domain.ItemMemo("  " + short + "  "); got != short {
		t.Fatalf("a short title should come back whole and trimmed, got %q", got)
	}

	long := strings.Repeat("é", 200)
	got := domain.ItemMemo(long)
	if runes := []rune(got); len(runes) != domain.ItemMemoLimit {
		t.Fatalf("a long title should be cut to %d runes, got %d", domain.ItemMemoLimit, len(runes))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("a cut title should say it was cut, got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatal("the cut landed inside a rune")
	}
}

func TestACreditIsMatchedToTheReturnThatIssuedIt(t *testing.T) {
	refunds := []domain.MerchantRefundFacts{
		{Ref: "r1", OrderRef: "o1", ItemRef: "BCABLE", RefundedOn: day("2026-09-10"), Amount: amt("14.00")},
	}
	match, ok := domain.MatchMerchantRefund(amt("14.00"), day("2026-09-17"), refunds, nil)
	require.True(t, ok)
	require.Equal(t, "o1", match.OrderRef)
	require.Equal(t, "r1", match.RefundRef)
	require.Equal(t, domain.MerchantMatchRefund, match.Basis)
	require.InDelta(t, 0.95, match.Confidence, 0.001)

	_, ok = domain.MatchMerchantRefund(amt("14.00"), day("2026-10-05"), refunds, nil)
	require.False(t, ok, "three weeks later is a different credit")
	_, ok = domain.MatchMerchantRefund(amt("14.01"), day("2026-09-17"), refunds, nil)
	require.False(t, ok, "a cent off is not the refund")
	_, ok = domain.MatchMerchantRefund(amt("-14.00"), day("2026-09-17"), refunds, nil)
	require.False(t, ok, "a charge is not a refund whatever its size")
}

func TestARefundToAGiftCardBalanceIsOfferedToNoBankRow(t *testing.T) {
	refunds := []domain.MerchantRefundFacts{
		{Ref: "r1", OrderRef: "o1", RefundedOn: day("2026-09-10"), Amount: amt("25.00"), ToGiftCard: true},
	}
	_, ok := domain.MatchMerchantRefund(amt("25.00"), day("2026-09-12"), refunds, nil)
	require.False(t, ok, "the bank never saw that money, so no bank row is it")
}

func TestOneRefundOnTwoPagesIsOneRefundAndTheLineWins(t *testing.T) {
	// The payments page's credit names the order; the returns page's record
	// names the item that came back. Both describe the same money.
	refunds := []domain.MerchantRefundFacts{
		{Ref: "fromCharge", OrderRef: "o1", RefundedOn: day("2026-09-10"), Amount: amt("26.00")},
		{Ref: "fromReturn", OrderRef: "o1", ItemRef: "BSTORAGE", RefundedOn: day("2026-09-10"),
			Amount: amt("26.00")},
	}
	match, ok := domain.MatchMerchantRefund(amt("26.00"), day("2026-09-14"), refunds, nil)
	require.True(t, ok)
	require.Equal(t, "fromReturn", match.RefundRef, "the record that names the item is the better one")
	require.InDelta(t, 0.95, match.Confidence, 0.001, "one refund described twice is not an ambiguity")
}

func TestTwoReturnsOfOnePriceAreNotBothTheFirstCredit(t *testing.T) {
	refunds := []domain.MerchantRefundFacts{
		{Ref: "r1", OrderRef: "o1", RefundedOn: day("2026-09-10"), Amount: amt("20.00")},
		{Ref: "r2", OrderRef: "o2", RefundedOn: day("2026-09-10"), Amount: amt("20.00")},
	}
	first, ok := domain.MatchMerchantRefund(amt("20.00"), day("2026-09-12"), refunds, nil)
	require.True(t, ok)
	require.InDelta(t, 0.75, first.Confidence, 0.001, "two orders, one figure: say so rather than guess")

	second, ok := domain.MatchMerchantRefund(amt("20.00"), day("2026-09-12"), refunds,
		[]domain.MerchantMatch{{RefundRef: first.RefundRef}})
	require.True(t, ok)
	require.NotEqual(t, first.RefundRef, second.RefundRef, "a return one credit gave back is not offered twice")
}

func TestAnUnnamedRefundDestinationIsACardAndAnUnnamedPaymentIsNot(t *testing.T) {
	// The payments page leaves a gift card blank; the returns page is terse
	// about the card it credited and spells the balance out.
	require.True(t, domain.IsGiftCardInstrument(""))
	require.False(t, domain.IsGiftCardDestination(""))
	require.True(t, domain.IsGiftCardDestination("Amazon Gift Card"))
	require.False(t, domain.IsGiftCardDestination("Visa ••••1234"))
}

func TestTheDatesAStoreReadsAreTheDatesTheMatchAccepts(t *testing.T) {
	on := day("2026-08-20")
	from, to := domain.MerchantOrderSpan(on)
	require.Equal(t, day("2026-08-06"), from, "an order placed a fortnight before the bank row")
	require.Equal(t, day("2026-08-22"), to)
	from, to = domain.MerchantChargeSpan(on)
	require.Equal(t, day("2026-08-17"), from)
	require.Equal(t, day("2026-08-23"), to)
	from, to = domain.MerchantRefundSpan(on)
	require.Equal(t, day("2026-08-06"), from)
	require.Equal(t, day("2026-08-22"), to)

	orderOn := func(placed string) bool {
		_, ok := domain.MatchMerchantOrder(amt("-12.50"), on,
			[]domain.MerchantOrderFacts{{Ref: "a", OrderedOn: day(placed), Total: amt("12.50")}}, nil, nil)
		return ok
	}
	require.True(t, orderOn("2026-08-06"))
	require.False(t, orderOn("2026-08-05"))
	require.True(t, orderOn("2026-08-22"))
	require.False(t, orderOn("2026-08-23"))

	chargedOn := func(charged string) bool {
		_, ok := domain.MatchMerchantOrder(amt("-12.50"), on, nil,
			[]domain.MerchantChargeFacts{{OrderRef: "a", ChargedOn: day(charged), Amount: amt("-12.50")}}, nil)
		return ok
	}
	require.True(t, chargedOn("2026-08-17"))
	require.False(t, chargedOn("2026-08-16"))
	require.True(t, chargedOn("2026-08-23"))
	require.False(t, chargedOn("2026-08-24"))

	refundedOn := func(refunded string) bool {
		_, ok := domain.MatchMerchantRefund(amt("12.50"), on,
			[]domain.MerchantRefundFacts{{Ref: "r", OrderRef: "a", RefundedOn: day(refunded), Amount: amt("12.50")}}, nil)
		return ok
	}
	require.True(t, refundedOn("2026-08-06"))
	require.False(t, refundedOn("2026-08-05"))
	require.True(t, refundedOn("2026-08-22"))
	require.False(t, refundedOn("2026-08-23"))
}

func refs(orders []domain.MerchantOrderFacts) []string {
	out := make([]string, 0, len(orders))
	for _, order := range orders {
		out = append(out, order.Ref)
	}
	return out
}

func TestAHandPickOffersFirstTheOrderTheCardPaidAfterTheGiftCard(t *testing.T) {
	// The row is 30.00. "gift" totals 50.00 of which a gift card paid 20.00,
	// so the card paid exactly the row; "near" totals 30.50 and paid it all.
	orders := []domain.MerchantOrderFacts{
		{Ref: "near", OrderedOn: day("2026-08-10"), Total: amt("30.50")},
		{Ref: "gift", OrderedOn: day("2026-08-01"), Total: amt("50.00"), GiftCard: amt("20.00")},
	}
	ranked := domain.RankMerchantOrders(amt("-30.00"), day("2026-08-11"), orders, nil)
	require.Equal(t, []string{"gift", "near"}, refs(ranked))
}

func TestAHandPickRanksByTheMatchersTiersThenAmountThenDate(t *testing.T) {
	orders := []domain.MerchantOrderFacts{
		{Ref: "far", OrderedOn: day("2026-07-01"), Total: amt("99.00")},
		{Ref: "close", OrderedOn: day("2026-08-09"), Total: amt("41.00")},
		{Ref: "item", OrderedOn: day("2026-08-09"), Total: amt("80.00"), Items: []domain.Money{amt("40.00"), amt("40.00")}},
		{Ref: "shipment", OrderedOn: day("2026-08-09"), Total: amt("70.00"), Shipments: []domain.Money{amt("40.00"), amt("30.00")}},
		{Ref: "total", OrderedOn: day("2026-06-01"), Total: amt("40.00")},
		{Ref: "charged", OrderedOn: day("2026-06-01"), Total: amt("90.00")},
		{Ref: "paid by gift card", OrderedOn: day("2026-08-10"), Total: amt("40.00"), GiftCard: amt("40.00")},
	}
	charges := []domain.MerchantChargeFacts{{OrderRef: "charged", ChargedOn: day("2026-08-09"), Amount: amt("-40.00")}}
	ranked := domain.RankMerchantOrders(amt("-40.00"), day("2026-08-10"), orders, charges)
	require.Equal(t, []string{"charged", "total", "shipment", "item", "close", "paid by gift card", "far"}, refs(ranked),
		"an order a gift card paid in full agrees with no card row, so ranks by its card total of nothing")
}

func TestAHandPickBreaksATieByDateThenTheNewerOrder(t *testing.T) {
	orders := []domain.MerchantOrderFacts{
		{Ref: "before", OrderedOn: day("2026-08-08"), Total: amt("15.00")},
		{Ref: "after", OrderedOn: day("2026-08-12"), Total: amt("15.00")},
		{Ref: "nearest", OrderedOn: day("2026-08-09"), Total: amt("15.00")},
	}
	ranked := domain.RankMerchantOrders(amt("-15.00"), day("2026-08-10"), orders, nil)
	require.Equal(t, []string{"nearest", "after", "before"}, refs(ranked))
}

func TestARowsOfferedToAnOrderByHandAreRankedByTheSameFit(t *testing.T) {
	order := domain.MerchantOrderFacts{
		Ref: "o", OrderedOn: day("2026-08-01"), Total: amt("60.00"), GiftCard: amt("10.00"),
		Shipments: []domain.Money{amt("35.00"), amt("25.00")},
	}
	rows := []domain.MerchantRowFacts{
		{Ref: "whole total", On: day("2026-08-02"), Amount: amt("-60.00")},
		{Ref: "shipment", On: day("2026-08-04"), Amount: amt("-35.00")},
		{Ref: "card total later", On: day("2026-08-20"), Amount: amt("-50.00")},
		{Ref: "card total", On: day("2026-08-03"), Amount: amt("-50.00")},
	}
	ranked := domain.RankMerchantRows(order, nil, rows)
	out := make([]string, 0, len(ranked))
	for _, row := range ranked {
		out = append(out, row.Ref)
	}
	require.Equal(t, []string{"card total", "card total later", "shipment", "whole total"}, out,
		"the 60.00 row is the total before the gift card, which no card was charged")
}
