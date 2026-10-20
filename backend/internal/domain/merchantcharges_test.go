package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func owedItems(amounts ...string) []domain.ChargeItem {
	out := make([]domain.ChargeItem, 0, len(amounts))
	for _, a := range amounts {
		out = append(out, domain.ChargeItem{TotalOwed: amt(a), HasTotalOwed: true})
	}
	return out
}

func eligible(flags ...bool) []domain.ChargeItem {
	out := make([]domain.ChargeItem, 0, len(flags))
	for _, f := range flags {
		out = append(out, domain.ChargeItem{FSAHSAEligible: f})
	}
	return out
}

// boxed is items in the given shipment boxes; a box's day is the day given
// for its first item, which every other item of the box repeats.
func boxed(boxes []int, days []string) []domain.ChargeItem {
	out := make([]domain.ChargeItem, 0, len(boxes))
	for i, box := range boxes {
		item := domain.ChargeItem{Shipment: box, HasShipment: true}
		if days[i] != "" {
			item.On = day(days[i])
		}
		out = append(out, item)
	}
	return out
}

func orderCharges(amounts []string, days ...string) []domain.OrderCharge {
	out := make([]domain.OrderCharge, 0, len(amounts))
	for i, a := range amounts {
		charge := domain.OrderCharge{Amount: amt(a)}
		if i < len(days) && days[i] != "" {
			charge.On = day(days[i])
		}
		out = append(out, charge)
	}
	return out
}

func TestItemsByCharge(t *testing.T) {
	none := domain.NoCharge
	cases := []struct {
		name    string
		items   []domain.ChargeItem
		charges []domain.OrderCharge
		fsa     string
		want    []int
	}{
		{
			name:    "the eligible item goes to the charge equal to the FSA total, whatever its price",
			items:   eligible(false, true),
			charges: orderCharges([]string{"-12.00", "-16.00"}),
			fsa:     "16.00", want: []int{0, 1},
		},
		{
			name:    "the FSA charge may come first",
			items:   eligible(true, false),
			charges: orderCharges([]string{"-16.00", "-12.00"}),
			fsa:     "16.00", want: []int{0, 1},
		},
		{
			name:    "an FSA total no charge equals is no answer",
			items:   eligible(false, true),
			charges: orderCharges([]string{"-12.00", "-16.00"}),
			fsa:     "14.00",
		},
		{
			name:    "an invoice that named no FSA total is no answer",
			items:   eligible(false, true),
			charges: orderCharges([]string{"-12.00", "-16.00"}),
			fsa:     "0",
		},
		{
			name:    "every item eligible is no answer",
			items:   eligible(true, true),
			charges: orderCharges([]string{"-12.00", "-16.00"}),
			fsa:     "16.00",
		},
		{
			name:    "no item eligible is no answer",
			items:   eligible(false, false),
			charges: orderCharges([]string{"-12.00", "-16.00"}),
			fsa:     "16.00",
		},
		{
			name:    "two charges both equal to the FSA total are no answer",
			items:   eligible(false, true),
			charges: orderCharges([]string{"-16.00", "-16.00"}),
			fsa:     "16.00",
		},
		{
			name:    "with three charges the eligible item is placed and the rest are not",
			items:   eligible(false, true, false),
			charges: orderCharges([]string{"-5.00", "-16.00", "-7.00"}),
			fsa:     "16.00", want: []int{none, 1, none},
		},
		{
			name: "with three charges the rest are placed by shipment",
			items: []domain.ChargeItem{
				{Shipment: 0, HasShipment: true, On: day("2025-03-04")},
				{FSAHSAEligible: true, Shipment: 1, HasShipment: true, On: day("2025-03-02")},
				{Shipment: 2, HasShipment: true, On: day("2025-03-01")},
			},
			charges: orderCharges([]string{"-5.00", "-16.00", "-7.00"}, "2025-03-05", "2025-03-02", "2025-03-03"),
			fsa:     "16.00", want: []int{0, 1, 2},
		},
		{
			name:    "item totals that partition exactly",
			items:   owedItems("11.07", "16.32"),
			charges: orderCharges([]string{"-16.32", "-11.07"}),
			want:    []int{1, 0},
		},
		{
			name:    "item totals that sum several to a charge",
			items:   owedItems("10.00", "20.00", "5.50"),
			charges: orderCharges([]string{"-30.00", "-5.50"}),
			want:    []int{0, 0, 1},
		},
		{
			name:    "item totals that miss are no answer",
			items:   owedItems("11.07", "16.32"),
			charges: orderCharges([]string{"-16.00", "-11.39"}),
		},
		{
			name:    "item totals that divide two ways are no answer",
			items:   owedItems("12.00", "12.00"),
			charges: orderCharges([]string{"-12.00", "-12.00"}),
		},
		{
			name:    "item totals beat the FSA marking",
			items:   append(owedItems("11.07"), domain.ChargeItem{TotalOwed: amt("16.32"), HasTotalOwed: true, FSAHSAEligible: true}),
			charges: orderCharges([]string{"-16.32", "-11.07"}),
			fsa:     "16.32", want: []int{1, 0},
		},
		{
			name:    "two shipments on distinct days pair with the charges in date order",
			items:   boxed([]int{0, 1}, []string{"2025-03-09", "2025-03-04"}),
			charges: orderCharges([]string{"-10.00", "-20.00"}, "2025-03-05", "2025-03-10"),
			want:    []int{1, 0},
		},
		{
			name:    "a box's items share its charge",
			items:   boxed([]int{0, 0, 1}, []string{"2025-03-04", "", "2025-03-09"}),
			charges: orderCharges([]string{"-10.00", "-20.00"}, "2025-03-05", "2025-03-10"),
			want:    []int{0, 0, 1},
		},
		{
			name:    "two shipments on one day are no answer",
			items:   boxed([]int{0, 1}, []string{"2025-03-04", "2025-03-04"}),
			charges: orderCharges([]string{"-10.00", "-20.00"}, "2025-03-05", "2025-03-10"),
		},
		{
			name:    "two charges on one day are no answer",
			items:   boxed([]int{0, 1}, []string{"2025-03-04", "2025-03-09"}),
			charges: orderCharges([]string{"-10.00", "-20.00"}, "2025-03-05", "2025-03-05"),
		},
		{
			name:    "a charge no row is matched to has no day",
			items:   boxed([]int{0, 1}, []string{"2025-03-04", "2025-03-09"}),
			charges: orderCharges([]string{"-10.00", "-20.00"}, "2025-03-05"),
		},
		{
			name:    "a box with no day is no answer",
			items:   boxed([]int{0, 1}, []string{"2025-03-04", ""}),
			charges: orderCharges([]string{"-10.00", "-20.00"}, "2025-03-05", "2025-03-10"),
		},
		{
			name:    "more shipments than charges is no answer",
			items:   boxed([]int{0, 1, 2}, []string{"2025-03-04", "2025-03-06", "2025-03-09"}),
			charges: orderCharges([]string{"-10.00", "-20.00"}, "2025-03-05", "2025-03-10"),
		},
		{
			name:    "fewer shipments than charges is no answer",
			items:   boxed([]int{0, 0}, []string{"2025-03-04", "2025-03-04"}),
			charges: orderCharges([]string{"-10.00", "-20.00"}, "2025-03-05", "2025-03-10"),
		},
		{
			name: "an item with no shipment is no answer",
			items: []domain.ChargeItem{
				{Shipment: 0, HasShipment: true, On: day("2025-03-04")},
				{},
			},
			charges: orderCharges([]string{"-10.00", "-20.00"}, "2025-03-05", "2025-03-10"),
		},
		{
			name:    "one charge is not a split",
			items:   eligible(false, true),
			charges: orderCharges([]string{"-28.00"}),
			fsa:     "28.00",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fsa, has := amt("0"), c.fsa != ""
			if has {
				fsa = amt(c.fsa)
			}
			require.Equal(t, c.want, domain.ItemsByCharge(c.items, c.charges, fsa, has))
		})
	}
}
