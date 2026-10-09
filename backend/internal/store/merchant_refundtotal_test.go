package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Which invoices a pull reads again for what was refunded, by the day the
// order was placed and the day it was last read for it. Orders are invented.
func TestAnOrderIsCheckedForRefundsOnceInAYearAndFortnightlyWhileRecent(t *testing.T) {
	space := newSpace(t)
	account := &MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Household"}
	require.NoError(t, db(t).CreateMerchantAccount(t.Context(), space, account))
	today := domain.NewDate(2026, time.October, 8)
	order := func(number string, ago int, status string) {
		t.Helper()
		_, err := db(t).UpsertMerchantOrder(t.Context(), space, &MerchantOrder{
			MerchantAccountID: account.ID, Merchant: domain.MerchantAmazon, OrderNumber: number,
			OrderedOn: today.AddDays(-ago), Total: domain.MustFromString("20.00"), Currency: "USD",
			Status: status, Source: "pull",
		})
		require.NoError(t, err)
	}
	checked := func(number string, ago int, refunded string) {
		t.Helper()
		require.NoError(t, db(t).SetMerchantOrderRefundTotal(t.Context(), space, account.ID, number,
			domain.MustFromString(refunded), today.AddDays(-ago)))
	}

	order("111-0000000-0000001", 300, "Delivered") // never read: due
	order("111-0000000-0000002", 300, "Delivered") // old and read: done
	checked("111-0000000-0000002", 200, "0.00")
	order("111-0000000-0000003", 40, "Delivered") // recent, read 20 days ago: due
	checked("111-0000000-0000003", 20, "0.00")
	order("111-0000000-0000004", 40, "Delivered") // recent, read 5 days ago: not yet
	checked("111-0000000-0000004", 5, "0.00")
	order("111-0000000-0000005", 400, "Delivered") // older than a year: never
	order("111-0000000-0000006", 10, "Cancelled")  // cancelled: never

	due, err := db(t).MerchantOrdersDueRefundCheck(t.Context(), space, account.ID, today)
	require.NoError(t, err)
	require.Equal(t, []string{"111-0000000-0000003", "111-0000000-0000001"}, due, "newest first")

	require.ErrorIs(t, db(t).SetMerchantOrderRefundTotal(t.Context(), space, account.ID,
		"111-9999999-9999999", domain.MustFromString("1.00"), today), ErrNotFound)

	checked("111-0000000-0000003", 0, "12.50")
	refunded, err := db(t).MerchantOrdersRefundedBetween(t.Context(), space, domain.MerchantAmazon,
		today.AddDays(-100), today)
	require.NoError(t, err)
	require.Len(t, refunded, 1)
	require.Equal(t, "111-0000000-0000003", refunded[0].OrderNumber)
	require.True(t, refunded[0].HasRefundTotal)
	require.Equal(t, "12.50", refunded[0].RefundTotal.String())
}
