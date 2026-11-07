package domain

import "sort"

const (
	maxChargeItems  = 10
	maxChargeSplits = 4
)

// NoCharge marks an item ItemsByCharge could not place on a charge.
const NoCharge = -1

// ChargeItem is one line of an order, with whatever the merchant stated about
// which charge paid for it.
type ChargeItem struct {
	// TotalOwed is what the line owes, tax included, when the merchant said.
	TotalOwed    Money
	HasTotalOwed bool
	// FSAHSAEligible is the invoice marking the line FSA/HSA eligible.
	FSAHSAEligible bool
	// Shipment numbers the line's shipment box on the order page from zero,
	// and On is the day that box shipped or arrives; HasShipment false and a
	// zero On when the page did not say.
	Shipment    int
	HasShipment bool
	On          Date
}

// OrderCharge is one card charge for an order. On is the day the bank row
// carries, zero when no row is matched to the charge yet.
type OrderCharge struct {
	Amount Money
	On     Date
}

// ItemsByCharge says which of an order's card charges paid for each item,
// when the merchant billed the order in parts and stated enough to tell. The
// result has one entry per item, the index into charges or NoCharge, and is
// nil when no item could be placed, so a caller shows every item as before.
// Nothing is estimated from prices; the first rule that settles the order
// wins:
//
//  1. Every item says what it owes, and exactly one way of dividing the items
//     makes each charge equal the sum of its items.
//  2. The invoice names an FSA/HSA total that equals exactly one charge, and
//     some but not all items are marked eligible: the eligible items go to
//     that charge. With two charges the others go to the other; with more,
//     the others are placed by rule 3 among the remaining charges, else left
//     unplaced.
//  3. Every item has a shipment box, the boxes are as many as the charges,
//     each box has one day and each charge a row, all on distinct days: boxes
//     and charges pair in date order.
//
// More than ten items or four charges is beyond rule 1.
func ItemsByCharge(items []ChargeItem, charges []OrderCharge, fsaHSA Money, hasFSAHSA bool) []int {
	if len(items) < 2 || len(charges) < 2 {
		return nil
	}
	every := make([]int, len(items))
	for i := range every {
		every[i] = i
	}
	all := make([]int, len(charges))
	for i := range all {
		all[i] = i
	}
	out := byTotalOwed(items, charges)
	if out == nil {
		out = byFSAHSA(items, charges, fsaHSA, hasFSAHSA)
	}
	if out == nil {
		out = placeByShipment(blankPlacement(len(items)), items, every, charges, all)
	}
	return out
}

func blankPlacement(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = NoCharge
	}
	return out
}

func placed(at []int) bool {
	for _, c := range at {
		if c != NoCharge {
			return true
		}
	}
	return false
}

func byTotalOwed(items []ChargeItem, charges []OrderCharge) []int {
	n, k := len(items), len(charges)
	if n > maxChargeItems || k > maxChargeSplits {
		return nil
	}
	for _, item := range items {
		if !item.HasTotalOwed {
			return nil
		}
	}
	assign := make([]int, n)
	var found []int
	count := 0
	var walk func(at int)
	walk = func(at int) {
		if count > 1 {
			return
		}
		if at < n {
			for c := 0; c < k; c++ {
				assign[at] = c
				walk(at + 1)
			}
			return
		}
		sums := make([]Money, k)
		has := make([]bool, k)
		for i, c := range assign {
			sums[c], has[c] = sums[c].Add(items[i].TotalOwed), true
		}
		for c, charge := range charges {
			if !has[c] || !sums[c].Equal(charge.Amount.Abs()) {
				return
			}
		}
		count++
		found = append(found[:0], assign...)
	}
	walk(0)
	if count != 1 {
		return nil
	}
	return found
}

func byFSAHSA(items []ChargeItem, charges []OrderCharge, fsaHSA Money, hasFSAHSA bool) []int {
	if !hasFSAHSA || !fsaHSA.IsPositive() {
		return nil
	}
	fsa := NoCharge
	for c, charge := range charges {
		if charge.Amount.Abs().Equal(fsaHSA) {
			if fsa != NoCharge {
				return nil
			}
			fsa = c
		}
	}
	if fsa == NoCharge {
		return nil
	}
	var rest []int
	out := blankPlacement(len(items))
	for i, item := range items {
		if item.FSAHSAEligible {
			out[i] = fsa
		} else {
			rest = append(rest, i)
		}
	}
	if len(rest) == 0 || len(rest) == len(items) {
		return nil
	}
	var others []int
	for c := range charges {
		if c != fsa {
			others = append(others, c)
		}
	}
	if len(others) == 1 {
		for _, i := range rest {
			out[i] = others[0]
		}
		return out
	}
	return placeByShipment(out, items, rest, charges, others)
}

// placeByShipment writes onto out the charges (indexes into charges) that the
// shipment boxes of the given items pair with, and returns out when it placed
// anything; nil otherwise.
func placeByShipment(out []int, items []ChargeItem, which []int, charges []OrderCharge, among []int) []int {
	days := map[int]Date{}
	var boxes []int
	for _, i := range which {
		item := items[i]
		if !item.HasShipment {
			return finishPlacement(out)
		}
		if _, seen := days[item.Shipment]; !seen {
			boxes = append(boxes, item.Shipment)
			days[item.Shipment] = Date{}
		}
		if item.On.IsZero() {
			continue
		}
		if day := days[item.Shipment]; day.IsZero() {
			days[item.Shipment] = item.On
		} else if day != item.On {
			return finishPlacement(out)
		}
	}
	if len(boxes) != len(among) {
		return finishPlacement(out)
	}
	boxDays := map[Date]bool{}
	for _, box := range boxes {
		day := days[box]
		if day.IsZero() || boxDays[day] {
			return finishPlacement(out)
		}
		boxDays[day] = true
	}
	chargeDays := map[Date]bool{}
	for _, c := range among {
		day := charges[c].On
		if day.IsZero() || chargeDays[day] {
			return finishPlacement(out)
		}
		chargeDays[day] = true
	}
	sort.Slice(boxes, func(a, b int) bool { return days[boxes[a]].Before(days[boxes[b]]) })
	byDay := append([]int(nil), among...)
	sort.Slice(byDay, func(a, b int) bool { return charges[byDay[a]].On.Before(charges[byDay[b]].On) })
	chargeOf := map[int]int{}
	for at, box := range boxes {
		chargeOf[box] = byDay[at]
	}
	for _, i := range which {
		out[i] = chargeOf[items[i].Shipment]
	}
	return finishPlacement(out)
}

func finishPlacement(out []int) []int {
	if !placed(out) {
		return nil
	}
	return out
}
