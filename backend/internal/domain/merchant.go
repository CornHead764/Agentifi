package domain

import (
	"cmp"
	"slices"
	"sort"
	"strings"

	"github.com/shopspring/decimal"
)

// Matching a bank row to the merchant order that produced it: by money first and
// time second. The wording never enters into it.

type MerchantOrderFacts struct {
	Ref       string
	OrderedOn Date
	Total     Money
	// GiftCard is the part of the total a gift card balance paid. The bank
	// never saw it, so what can agree with a bank row is Total less this.
	GiftCard Money
	// Shipments are present only when the file said which items shipped
	// together. Items are each line's own total.
	Shipments []Money
	Items     []Money
}

// CardTotal is the total less whatever a gift card paid; zero for an order a
// gift card paid in full.
func (o MerchantOrderFacts) CardTotal() Money {
	card := o.Total.Sub(o.GiftCard)
	if card.IsNegative() {
		return Money{}
	}
	return card
}

// MerchantChargeFacts is one charge the merchant put on a card, signed as the
// bank sees it: negative for a charge, positive for a refund.
type MerchantChargeFacts struct {
	OrderRef  string
	ChargedOn Date
	Amount    Money
}

// MerchantRefundFacts is one refund the merchant's own records show. Unlike a
// payments-page credit it can name the item that came back. ToGiftCard is a
// refund no bank row will ever explain.
type MerchantRefundFacts struct {
	Ref      string
	OrderRef string
	// ItemRef names the line that came back, empty for a whole-order refund.
	// It tells the payments page's and the returns page's records of one
	// refund apart.
	ItemRef string
	// RefundedOn is the day the merchant issued it, before the bank posts it.
	RefundedOn Date
	// Amount is positive, signed as the bank row is.
	Amount     Money
	ToGiftCard bool
}

type MerchantMatch struct {
	OrderRef string
	// RefundRef is set only for a credit matched to a refund record.
	RefundRef string
	// Amount is the figure that agreed, signed as the bank row is.
	Amount     Money
	Basis      string
	Confidence float64
}

// How a match was made, strongest first.
const (
	// MerchantMatchCharge: the merchant's own record of charging the card agrees.
	MerchantMatchCharge     = "charge"
	MerchantMatchOrderTotal = "order_total"
	// MerchantMatchShipment: the order was charged in parts.
	MerchantMatchShipment = "shipment"
	// MerchantMatchItem: the order was charged per item.
	MerchantMatchItem   = "item"
	MerchantMatchRefund = "refund"
	MerchantMatchManual = "manual"
)

// merchantWindow is how many days before and after a bank row a merchant's
// record may be dated and still explain it.
type merchantWindow struct{ before, after int }

// span is the dates a record may carry for a bank row on on.
func (w merchantWindow) span(on Date) (from, to Date) {
	return on.AddDays(-w.before), on.AddDays(w.after)
}

// distance is how many days apart a record dated at and a bank row on on
// are, and whether the record falls in the window.
func (w merchantWindow) distance(at, on Date) (int, bool) {
	days := DaysBetween(at, on)
	return abs(days), days <= w.before && days >= -w.after
}

// A card is charged when an item ships, usually the order day, occasionally a
// week later for a back-order; the bank posts a day or two after that.
var (
	merchantChargeWindow = merchantWindow{before: 3, after: 3}
	merchantOrderWindow  = merchantWindow{before: 14, after: 2}
	// A refund lands on the card three to ten days after it is issued, and the
	// merchant may date it up to two days after the bank does.
	merchantRefundWindow = merchantWindow{before: 14, after: 2}
)

// MerchantChargeSpan is the dates a merchant's charge record may carry and
// still explain a bank row on on.
func MerchantChargeSpan(on Date) (from, to Date) { return merchantChargeWindow.span(on) }

// MerchantOrderSpan is the dates an order may be placed on and still explain
// a bank row on on.
func MerchantOrderSpan(on Date) (from, to Date) { return merchantOrderWindow.span(on) }

// MerchantRefundSpan is the dates a refund may be issued on and still explain
// a bank credit on on.
func MerchantRefundSpan(on Date) (from, to Date) { return merchantRefundWindow.span(on) }

// IsGiftCardInstrument says whether a charge's payment method is one the bank
// never saw: a gift card, a Costco Shop Card, cash or a check. Amazon's
// transactions page names a gift card by nothing at all, so an empty
// instrument is a gift card too.
func IsGiftCardInstrument(instrument string) bool {
	text := strings.ToLower(strings.TrimSpace(instrument))
	if text == "" || strings.Contains(text, "gift") || strings.Contains(text, "shop card") {
		return true
	}
	switch text {
	case "cash", "check", "cheque":
		return true
	}
	return false
}

// IsGiftCardDestination says whether a refund's destination is a balance the
// bank never sees. Unlike IsGiftCardInstrument, an unnamed destination is a
// card: a returns page spells out a gift card balance and is merely terse
// about the card it credited.
func IsGiftCardDestination(instrument string) bool {
	return strings.TrimSpace(instrument) != "" && IsGiftCardInstrument(instrument)
}

// MatchMerchantOrder finds the order behind one bank row, or reports that
// none fits. orders and charges are one merchant's.
//
// amount and on are the bank row's. Tiers: charges, then the order's card
// total, then a shipment, then a single item. Within a tier the nearest date
// wins, and a tie between two orders lowers the confidence.
//
// taken are the matches already made, so two bank rows for the same amount
// find two orders rather than the same one twice. Gift card charges must not
// be among charges (IsGiftCardInstrument).
func MatchMerchantOrder(
	amount Money, on Date, orders []MerchantOrderFacts, charges []MerchantChargeFacts,
	taken []MerchantMatch,
) (MerchantMatch, bool) {
	if amount.IsZero() {
		return MerchantMatch{}, false
	}
	// A charge or figure is offered only while a match of it is unaccounted for.
	type key struct {
		ref    string
		amount string
	}
	used := map[key]int{}
	matchedOrders := map[string]bool{}
	for _, t := range taken {
		used[key{t.OrderRef, t.Amount.Abs().String()}]++
		matchedOrders[t.OrderRef] = true
	}
	free := func(ref string, figure Money) bool {
		k := key{ref, figure.Abs().String()}
		if used[k] > 0 {
			used[k]--
			return false
		}
		return true
	}

	type hit struct {
		ref      string
		distance int
	}
	pick := func(hits []hit, basis string, base float64) (MerchantMatch, bool) {
		if len(hits) == 0 {
			return MerchantMatch{}, false
		}
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].distance != hits[j].distance {
				return hits[i].distance < hits[j].distance
			}
			return hits[i].ref < hits[j].ref
		})
		confidence := base
		if len(hits) > 1 && hits[1].ref != hits[0].ref && hits[1].distance == hits[0].distance {
			confidence -= 0.2
		}
		return MerchantMatch{
			OrderRef: hits[0].ref, Amount: amount, Basis: basis, Confidence: confidence,
		}, true
	}

	var hits []hit
	for _, charge := range charges {
		distance, inWindow := merchantChargeWindow.distance(charge.ChargedOn, on)
		if !charge.Amount.Equal(amount) || !inWindow {
			continue
		}
		if !free(charge.OrderRef, charge.Amount) {
			continue
		}
		hits = append(hits, hit{ref: charge.OrderRef, distance: distance})
	}
	if match, ok := pick(hits, MerchantMatchCharge, 0.98); ok {
		return match, true
	}

	// Order-side figures are unsigned amounts owed; only money out is tried.
	if !amount.IsNegative() {
		return MerchantMatch{}, false
	}
	owed := amount.Neg()
	inWindow := func(order MerchantOrderFacts) (int, bool) {
		return merchantOrderWindow.distance(order.OrderedOn, on)
	}

	hits = hits[:0]
	for _, order := range orders {
		if order.CardTotal().IsZero() || matchedOrders[order.Ref] {
			continue
		}
		if distance, ok := inWindow(order); ok && order.CardTotal().Equal(owed) {
			hits = append(hits, hit{ref: order.Ref, distance: distance})
		}
	}
	if match, ok := pick(hits, MerchantMatchOrderTotal, 0.9); ok {
		return match, true
	}

	hits = hits[:0]
	for _, order := range orders {
		distance, ok := inWindow(order)
		if !ok || order.CardTotal().IsZero() {
			continue
		}
		for _, shipment := range order.Shipments {
			if shipment.Equal(owed) && free(order.Ref, shipment) {
				hits = append(hits, hit{ref: order.Ref, distance: distance})
				break
			}
		}
	}
	if match, ok := pick(hits, MerchantMatchShipment, 0.8); ok {
		return match, true
	}

	hits = hits[:0]
	for _, order := range orders {
		distance, ok := inWindow(order)
		if !ok || order.CardTotal().IsZero() {
			continue
		}
		for _, item := range order.Items {
			if item.Equal(owed) && free(order.Ref, item) {
				hits = append(hits, hit{ref: order.Ref, distance: distance})
				break
			}
		}
	}
	return pick(hits, MerchantMatchItem, 0.7)
}

// MerchantRowFacts is a bank row offered to an order by hand, amount signed
// as the bank shows it.
type MerchantRowFacts struct {
	Ref    string
	On     Date
	Amount Money
}

// How a bank row's amount agrees with an order, in MatchMerchantOrder's tier
// order: a hand pick is offered the matcher's own reading first.
const (
	merchantFitCharge = iota
	merchantFitOrderTotal
	merchantFitShipment
	merchantFitItem
	merchantFitNone
)

type merchantFit struct {
	tier int
	gap  Money
	days int
}

func (f merchantFit) compare(other merchantFit) int {
	return cmp.Or(cmp.Compare(f.tier, other.tier), f.gap.Cmp(other.gap), cmp.Compare(f.days, other.days))
}

// fitMerchantOrder is how well a bank row of amount on on agrees with order.
// Only charges of the order a card paid count; gift card charges must not be
// among them.
func fitMerchantOrder(amount Money, on Date, order MerchantOrderFacts, charges []MerchantChargeFacts) merchantFit {
	owed := amount.Abs()
	fit := merchantFit{tier: merchantFitNone, gap: order.CardTotal().Sub(owed).Abs(),
		days: abs(DaysBetween(order.OrderedOn, on))}
	agrees := func(figures []Money) bool {
		return slices.ContainsFunc(figures, func(figure Money) bool { return figure.Equal(owed) })
	}
	switch {
	case slices.ContainsFunc(charges, func(charge MerchantChargeFacts) bool {
		return charge.OrderRef == order.Ref && charge.Amount.Equal(amount)
	}):
		fit.tier = merchantFitCharge
	case order.CardTotal().IsZero():
	case order.CardTotal().Equal(owed):
		fit.tier = merchantFitOrderTotal
	case agrees(order.Shipments):
		fit.tier = merchantFitShipment
	case agrees(order.Items):
		fit.tier = merchantFitItem
	}
	return fit
}

// RankMerchantOrders orders the orders a person might match one bank row to
// by hand: by MatchMerchantOrder's tiers whatever the dates, then nearest in
// what the card paid, then nearest in date, newest first on a tie. Gift card
// charges must not be among charges (IsGiftCardInstrument).
func RankMerchantOrders(amount Money, on Date, orders []MerchantOrderFacts, charges []MerchantChargeFacts) []MerchantOrderFacts {
	ranked := slices.Clone(orders)
	fits := make(map[string]merchantFit, len(ranked))
	for _, order := range ranked {
		fits[order.Ref] = fitMerchantOrder(amount, on, order, charges)
	}
	slices.SortStableFunc(ranked, func(a, b MerchantOrderFacts) int {
		return cmp.Or(fits[a.Ref].compare(fits[b.Ref]),
			b.OrderedOn.Time().Compare(a.OrderedOn.Time()), strings.Compare(a.Ref, b.Ref))
	})
	return ranked
}

// RankMerchantRows orders the bank rows a person might match to one order by
// hand, the other way round from RankMerchantOrders and by the same fit.
func RankMerchantRows(order MerchantOrderFacts, charges []MerchantChargeFacts, rows []MerchantRowFacts) []MerchantRowFacts {
	ranked := slices.Clone(rows)
	fits := make(map[string]merchantFit, len(ranked))
	for _, row := range ranked {
		fits[row.Ref] = fitMerchantOrder(row.Amount, row.On, order, charges)
	}
	slices.SortStableFunc(ranked, func(a, b MerchantRowFacts) int {
		return cmp.Or(fits[a.Ref].compare(fits[b.Ref]), strings.Compare(a.Ref, b.Ref))
	})
	return ranked
}

// MatchMerchantRefund finds the return behind one bank credit. Asked before
// MatchMerchantOrder, because only the return says which item came back; a credit no
// return explains falls through to the ordinary match.
//
// A gift-card refund is never offered: no bank row agrees with it, so it could
// only mismatch an unrelated credit. A refund record already in taken is not
// offered again.
func MatchMerchantRefund(
	amount Money, on Date, refunds []MerchantRefundFacts, taken []MerchantMatch,
) (MerchantMatch, bool) {
	if !amount.IsPositive() {
		return MerchantMatch{}, false
	}
	claimed := map[string]bool{}
	for _, t := range taken {
		if t.RefundRef != "" {
			claimed[t.RefundRef] = true
		}
	}

	type hit struct {
		refund   MerchantRefundFacts
		distance int
	}
	var hits []hit
	for _, refund := range refunds {
		distance, inWindow := merchantRefundWindow.distance(refund.RefundedOn, on)
		if refund.ToGiftCard || claimed[refund.Ref] || !refund.Amount.Equal(amount) || !inWindow {
			continue
		}
		hits = append(hits, hit{refund: refund, distance: distance})
	}
	if len(hits) == 0 {
		return MerchantMatch{}, false
	}
	// Nearest first, then the record that names the line that came back.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].distance != hits[j].distance {
			return hits[i].distance < hits[j].distance
		}
		if (hits[i].refund.ItemRef != "") != (hits[j].refund.ItemRef != "") {
			return hits[i].refund.ItemRef != ""
		}
		return hits[i].refund.Ref < hits[j].refund.Ref
	})
	confidence := 0.95
	// Two refunds of the same order are one refund described twice, not an
	// ambiguity.
	if len(hits) > 1 && hits[1].distance == hits[0].distance &&
		hits[1].refund.OrderRef != hits[0].refund.OrderRef {
		confidence -= 0.2
	}
	return MerchantMatch{
		OrderRef: hits[0].refund.OrderRef, RefundRef: hits[0].refund.Ref, Amount: amount,
		Basis: MerchantMatchRefund, Confidence: confidence,
	}, true
}

// SplitByWeight divides amount across weights in proportion, to the cent,
// so the parts sum to amount exactly; leftover cents go to the parts that lost
// the most in rounding. Nil for no weights, a negative weight, or a zero sum.
func SplitByWeight(amount Money, weights []Money) []Money {
	if len(weights) == 0 {
		return nil
	}
	total := decimal.Zero
	for _, w := range weights {
		if w.IsNegative() {
			return nil
		}
		total = total.Add(w.d)
	}
	if total.IsZero() {
		return nil
	}
	cents := amount.Round().d.Mul(decimal.NewFromInt(100)).IntPart()
	sign := int64(1)
	if cents < 0 {
		sign, cents = -1, -cents
	}
	type share struct {
		index int
		frac  decimal.Decimal
	}
	parts := make([]int64, len(weights))
	shares := make([]share, len(weights))
	var given int64
	for i, w := range weights {
		exact := w.d.Mul(decimal.NewFromInt(cents)).Div(total)
		floor := exact.Floor()
		parts[i] = floor.IntPart()
		given += parts[i]
		shares[i] = share{index: i, frac: exact.Sub(floor)}
	}
	sort.SliceStable(shares, func(i, j int) bool { return shares[i].frac.GreaterThan(shares[j].frac) })
	for k := int64(0); k < cents-given; k++ {
		parts[shares[int(k)%len(shares)].index]++
	}
	out := make([]Money, len(weights))
	for i, p := range parts {
		out[i] = FromCents(sign * p)
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// IsAmazonWording reports whether a bank row's wording names Amazon. Every
// descriptor ("AMAZON.COM*2K4D1R6Q3", "AMZN Mktp US") carries one of two stems.
func IsAmazonWording(statementName, payee string) bool {
	text := strings.ToLower(statementName + " " + payee)
	return strings.Contains(text, "amazon") || strings.Contains(text, "amzn")
}

// ItemMemoLimit is how much of an item's title a split's memo keeps; Amazon
// titles' tails are marketing.
const ItemMemoLimit = 120

// ItemMemo is an order item's title as a split's memo, cut on a rune boundary.
func ItemMemo(title string) string {
	title = strings.TrimSpace(title)
	if len([]rune(title)) <= ItemMemoLimit {
		return title
	}
	return string([]rune(title)[:ItemMemoLimit-3]) + "..."
}
