package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func payersOrder(matched ...store.MerchantMatchedTransaction) store.MerchantOrder {
	money := domain.MustFromString
	return store.MerchantOrder{
		Total: money("28.00"), Tax: money("0.80"), HasTax: true,
		FSAHSA: money("16.00"), HasFSAHSA: true,
		Items: []store.MerchantOrderItem{
			{Title: "Vanilla paste", Quantity: 1, UnitPrice: money("15.00"), HasUnitPrice: true},
			{Title: "Cold remedy", Quantity: 1, UnitPrice: money("20.00"), HasUnitPrice: true, FSAHSAEligible: true},
		},
		Matched: matched,
	}
}

func dayOf(text string) domain.Date {
	on, err := domain.ParseDate(text)
	if err != nil {
		panic(err)
	}
	return on
}

func chargeRow(id uuid.UUID, amount string) store.MerchantMatchedTransaction {
	return store.MerchantMatchedTransaction{ID: id, Amount: domain.MustFromString(amount)}
}

func TestEachMatchedRowOfAnOrderChargedInPartsOwnsTheItemsAmazonSaidItBilled(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	payers, ok := ItemPayers(payersOrder(chargeRow(a, "-12.00"), chargeRow(b, "-16.00")))
	require.True(t, ok)
	require.Equal(t, []uuid.UUID{a, b}, payers)
}

func TestTheChargeNoRowHasPaidYetLeavesItsItemsUnclaimed(t *testing.T) {
	a := uuid.New()
	payers, ok := ItemPayers(payersOrder(chargeRow(a, "-16.00")))
	require.True(t, ok)
	require.Equal(t, []uuid.UUID{uuid.Nil, a}, payers)
}

func TestChargesAmazonDidNotSayTheItemsOfAreNotGuessedFromPrices(t *testing.T) {
	order := payersOrder(chargeRow(uuid.New(), "-16.00"), chargeRow(uuid.New(), "-12.00"))
	order.FSAHSA, order.HasFSAHSA = domain.Zero, false
	order.Items[1].FSAHSAEligible = false
	_, ok := ItemPayers(order)
	require.False(t, ok)
}

func TestShipmentsPairWithTheChargesByDate(t *testing.T) {
	money := domain.MustFromString
	a, b := uuid.New(), uuid.New()
	first, second := 0, 1
	order := payersOrder(
		store.MerchantMatchedTransaction{ID: a, Amount: money("-12.00"), Date: dayOf("2025-10-10")},
		store.MerchantMatchedTransaction{ID: b, Amount: money("-16.00"), Date: dayOf("2025-10-13")},
	)
	order.FSAHSA, order.HasFSAHSA = domain.Zero, true
	order.Items[1].FSAHSAEligible = false
	order.Items[0].Shipment, order.Items[0].ShipmentOn = &first, dayOf("2025-10-11")
	order.Items[1].Shipment, order.Items[1].ShipmentOn = &second, dayOf("2025-10-14")
	payers, ok := ItemPayers(order)
	require.True(t, ok)
	require.Equal(t, []uuid.UUID{a, b}, payers)
}

func TestAnOrderPaidByOneRowKeepsAllItsItems(t *testing.T) {
	_, ok := ItemPayers(payersOrder(chargeRow(uuid.New(), "-28.00")))
	require.False(t, ok)
	_, ok = ItemPayers(payersOrder())
	require.False(t, ok)
}

func TestARowThatPaidMoreThanTheOrderCostIsNotDivided(t *testing.T) {
	_, ok := ItemPayers(payersOrder(chargeRow(uuid.New(), "-40.00")))
	require.False(t, ok)
}

func TestAPartialChargeIsDividedAcrossOnlyItsOwnItems(t *testing.T) {
	money := domain.MustFromString
	a, b := uuid.New(), uuid.New()
	priced := func(title, price string) store.MerchantOrderItem {
		return store.MerchantOrderItem{Title: title, Quantity: 1, UnitPrice: money(price), HasUnitPrice: true}
	}
	order := store.MerchantOrder{
		Total: money("66.00"), Tax: money("6.00"), HasTax: true,
		FSAHSA: money("11.00"), HasFSAHSA: true,
		Items:   []store.MerchantOrderItem{priced("Lamp", "10.00"), priced("Stand", "20.00"), priced("Cable", "30.00")},
		Matched: []store.MerchantMatchedTransaction{chargeRow(a, "-11.00"), chargeRow(b, "-55.00")},
	}
	order.Items[0].FSAHSAEligible = true
	first := &MerchantEnrichment{
		Matches: []store.MerchantMatch{{TransactionID: a, Amount: money("-11.00")}},
		Orders:  []store.MerchantOrder{order},
	}
	require.Len(t, first.Items(), 1)
	require.Equal(t, []domain.Money{money("-11.00")}, first.Shares())

	second := &MerchantEnrichment{
		Matches: []store.MerchantMatch{{TransactionID: b, Amount: money("-55.00")}},
		Orders:  []store.MerchantOrder{order},
	}
	require.Len(t, second.Items(), 2)
	require.Equal(t, []domain.Money{money("-22.00"), money("-33.00")}, second.Shares())
}
