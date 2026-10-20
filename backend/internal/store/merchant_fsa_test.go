package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func fsaOrder(account MerchantAccount, items ...MerchantOrderItem) *MerchantOrder {
	return &MerchantOrder{
		Merchant: domain.MerchantAmazon, MerchantAccountID: account.ID, OrderNumber: "111-0000000-0000001",
		OrderedOn: domain.NewDate(2026, time.October, 9), Total: domain.MustFromString("28.00"),
		Currency: "USD", Status: "Delivered", Source: "agentifi_json", Items: items,
	}
}

func storedItems(t *testing.T, space SpaceID, account MerchantAccount) (MerchantOrder, []MerchantOrderItem) {
	t.Helper()
	orders, _, err := db(t).ListMerchantOrders(t.Context(), space, MerchantOrderQuery{
		Merchant: domain.MerchantAmazon, AccountID: account.ID,
	})
	require.NoError(t, err)
	require.Len(t, orders, 1)
	return orders[0], orders[0].Items
}

func TestWhatAnInvoiceSaidOfFSAHSASurvivesAReadOfTheOrderCardAlone(t *testing.T) {
	space := newSpace(t)
	account := MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Household"}
	require.NoError(t, db(t).CreateMerchantAccount(t.Context(), space, &account))
	money := domain.MustFromString
	box := func(n int) *int { return &n }

	invoiced := fsaOrder(account,
		MerchantOrderItem{SKU: "B00TESTONE", Title: "Vanilla paste", Quantity: 1, UnitPrice: money("15.00"), HasUnitPrice: true},
		MerchantOrderItem{SKU: "B00TESTTWO", Title: "Cold remedy", Quantity: 1, UnitPrice: money("20.00"), HasUnitPrice: true,
			FSAHSAEligible: true},
	)
	invoiced.FSAHSA, invoiced.HasFSAHSA = money("16.00"), true
	_, err := db(t).UpsertMerchantOrder(t.Context(), space, invoiced)
	require.NoError(t, err)

	cardOnly := fsaOrder(account,
		MerchantOrderItem{SKU: "B00TESTONE", Title: "Vanilla paste", Quantity: 1, Shipment: box(0),
			ShipmentOn: domain.NewDate(2026, time.October, 11)},
		MerchantOrderItem{SKU: "B00TESTTWO", Title: "Cold remedy", Quantity: 1, Shipment: box(1),
			ShipmentOn: domain.NewDate(2026, time.October, 14)},
	)
	_, err = db(t).UpsertMerchantOrder(t.Context(), space, cardOnly)
	require.NoError(t, err)

	order, items := storedItems(t, space, account)
	require.True(t, order.HasFSAHSA)
	require.Equal(t, money("16.00"), order.FSAHSA)
	require.False(t, items[0].FSAHSAEligible)
	require.True(t, items[1].FSAHSAEligible)
	require.True(t, items[0].HasUnitPrice, "the priced items stay")
	require.Equal(t, 0, *items[0].Shipment, "the card's boxes are filed onto them")
	require.Equal(t, 1, *items[1].Shipment)
	require.Equal(t, domain.NewDate(2026, time.October, 14), items[1].ShipmentOn)
}

func TestAPricedReplacementKeepsTheFlagsAndBoxesItDidNotRead(t *testing.T) {
	space := newSpace(t)
	account := MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Household"}
	require.NoError(t, db(t).CreateMerchantAccount(t.Context(), space, &account))
	money := domain.MustFromString
	zero := 0

	first := fsaOrder(account,
		MerchantOrderItem{SKU: "B00TESTONE", Title: "Vanilla paste", Quantity: 1, UnitPrice: money("15.00"), HasUnitPrice: true,
			Shipment: &zero, ShipmentOn: domain.NewDate(2026, time.October, 11), FSAHSAEligible: true},
		MerchantOrderItem{SKU: "B00TESTTWO", Title: "Cold remedy", Quantity: 1, UnitPrice: money("20.00"), HasUnitPrice: true},
	)
	first.FSAHSA, first.HasFSAHSA = money("16.00"), true
	_, err := db(t).UpsertMerchantOrder(t.Context(), space, first)
	require.NoError(t, err)

	file := fsaOrder(account,
		MerchantOrderItem{SKU: "B00TESTONE", Title: "Vanilla paste", Quantity: 1, TotalOwed: money("15.77"), HasTotalOwed: true},
		MerchantOrderItem{SKU: "B00TESTTWO", Title: "Cold remedy", Quantity: 1, TotalOwed: money("11.62"), HasTotalOwed: true},
	)
	_, err = db(t).UpsertMerchantOrder(t.Context(), space, file)
	require.NoError(t, err)

	order, items := storedItems(t, space, account)
	require.Equal(t, money("16.00"), order.FSAHSA)
	require.True(t, items[0].FSAHSAEligible)
	require.Equal(t, 0, *items[0].Shipment)
	require.True(t, items[0].HasTotalOwed)

	reread := fsaOrder(account,
		MerchantOrderItem{SKU: "B00TESTONE", Title: "Vanilla paste", Quantity: 1, UnitPrice: money("15.00"), HasUnitPrice: true},
		MerchantOrderItem{SKU: "B00TESTTWO", Title: "Cold remedy", Quantity: 1, UnitPrice: money("20.00"), HasUnitPrice: true},
	)
	reread.FSAHSA, reread.HasFSAHSA = domain.Zero, true
	_, err = db(t).UpsertMerchantOrder(t.Context(), space, reread)
	require.NoError(t, err)
	order, items = storedItems(t, space, account)
	require.True(t, order.HasFSAHSA)
	require.True(t, order.FSAHSA.IsZero(), "an invoice read again is the authority")
	require.False(t, items[0].FSAHSAEligible)
}

func TestOrdersChargedInPartsWithNoFSAHSAReadAreDueOne(t *testing.T) {
	space := newSpace(t)
	account := MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Household"}
	require.NoError(t, db(t).CreateMerchantAccount(t.Context(), space, &account))
	charge := func(order string, day int, amount string) {
		t.Helper()
		_, err := db(t).UpsertMerchantCharge(t.Context(), space, &MerchantCharge{
			Merchant: domain.MerchantAmazon, MerchantAccountID: account.ID, OrderNumber: order,
			ChargedOn: domain.NewDate(2026, time.October, day), Amount: domain.MustFromString(amount),
		})
		require.NoError(t, err)
	}
	order := func(number string, read bool) {
		t.Helper()
		one := fsaOrder(account)
		one.OrderNumber = number
		if read {
			one.FSAHSA, one.HasFSAHSA = domain.Zero, true
		}
		_, err := db(t).UpsertMerchantOrder(t.Context(), space, one)
		require.NoError(t, err)
	}
	order("111-0000000-0000001", false)
	charge("111-0000000-0000001", 11, "-12.00")
	charge("111-0000000-0000001", 14, "-16.00")
	order("111-0000000-0000002", false)
	charge("111-0000000-0000002", 11, "-9.00")
	order("111-0000000-0000003", true)
	charge("111-0000000-0000003", 11, "-5.00")
	charge("111-0000000-0000003", 14, "-6.00")

	due, err := db(t).MerchantOrdersDueFSAHSARead(t.Context(), space, account.ID, domain.MerchantAmazon,
		domain.NewDate(2026, time.September, 1))
	require.NoError(t, err)
	require.Equal(t, []string{"111-0000000-0000001"}, due)

	due, err = db(t).MerchantOrdersDueFSAHSARead(t.Context(), space, account.ID, domain.MerchantAmazon,
		domain.NewDate(2026, time.October, 10))
	require.NoError(t, err)
	require.Empty(t, due, "an order before the window is not read again")
}
