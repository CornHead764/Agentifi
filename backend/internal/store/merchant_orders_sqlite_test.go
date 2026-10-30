package store_test

import (
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSQLiteMerchantOrders(t *testing.T) {
	st := storetest.New(t)
	sp := storetest.NewHousehold(t, st)
	other := storetest.NewHousehold(t, st)
	ctx := t.Context()
	day := domain.NewDate(2026, time.October, 8)
	login := &store.MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Orders"}
	require.NoError(t, st.CreateMerchantAccount(ctx, sp, login))
	order := &store.MerchantOrder{Merchant: domain.MerchantAmazon, MerchantAccountID: login.ID, OrderNumber: "O-1", OrderedOn: day, Total: domain.MustFromString("12.34"), Currency: "USD", Source: "pull", Items: []store.MerchantOrderItem{{SKU: "SKU", Title: "A 100% item", Quantity: 1, UnitPrice: domain.MustFromString("12.34"), HasUnitPrice: true}}}
	inserted, err := st.UpsertMerchantOrder(ctx, sp, order)
	require.NoError(t, err)
	require.True(t, inserted)
	require.NoError(t, st.UpsertCatalogItem(ctx, &store.CatalogItem{Merchant: domain.MerchantAmazon, SKU: "SKU", Status: store.CatalogFound, Title: "Catalog item"}))
	got, err := st.GetMerchantOrder(ctx, sp, order.ID)
	require.NoError(t, err)
	require.Equal(t, "12.34", got.Total.String())
	require.Len(t, got.Items, 1)
	require.Equal(t, "Catalog item", got.Items[0].DisplayTitle())
	require.NoError(t, st.InHouseholdTx(ctx, sp, func(tx *store.Store) error {
		inTx, err := tx.GetMerchantOrder(ctx, sp, order.ID)
		require.NoError(t, err)
		require.Len(t, inTx.Items, 1)
		require.Equal(t, "Catalog item", inTx.Items[0].DisplayTitle())
		_, _, err = tx.ListMerchantOrders(ctx, sp, store.MerchantOrderQuery{Merchant: domain.MerchantAmazon})
		return err
	}))
	order.Items = []store.MerchantOrderItem{{Title: "unpriced", Quantity: 1}}
	inserted, err = st.UpsertMerchantOrder(ctx, sp, order)
	require.NoError(t, err)
	require.False(t, inserted)
	got, err = st.GetMerchantOrder(ctx, sp, order.ID)
	require.NoError(t, err)
	require.Equal(t, "SKU", got.Items[0].SKU)
	_, err = st.GetMerchantOrder(ctx, other, order.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	charge := &store.MerchantCharge{Merchant: domain.MerchantAmazon, MerchantAccountID: login.ID, OrderNumber: order.OrderNumber, ChargedOn: day, Amount: domain.MustFromString("-12.34")}
	inserted, err = st.UpsertMerchantCharge(ctx, sp, charge)
	require.NoError(t, err)
	require.True(t, inserted)
	inserted, err = st.UpsertMerchantCharge(ctx, sp, charge)
	require.NoError(t, err)
	require.False(t, inserted)
	charges, err := st.MerchantChargesForOrders(ctx, sp, []string{order.OrderNumber})
	require.NoError(t, err)
	require.Len(t, charges, 1)
	require.Equal(t, "-12.34", charges[0].Amount.String())
	refund := &store.MerchantRefund{Merchant: domain.MerchantAmazon, MerchantAccountID: login.ID, OrderNumber: order.OrderNumber, RefundedOn: day, Amount: domain.MustFromString("2.34")}
	inserted, err = st.UpsertMerchantRefund(ctx, sp, refund)
	require.NoError(t, err)
	require.True(t, inserted)
	refund.Status = "completed"
	inserted, err = st.UpsertMerchantRefund(ctx, sp, refund)
	require.NoError(t, err)
	require.False(t, inserted)
	got, err = st.GetMerchantOrder(ctx, sp, order.ID)
	require.NoError(t, err)
	require.Len(t, got.Refunds, 1)
	require.Equal(t, "completed", got.Refunds[0].Status)
	bank := &store.Account{Name: "Bank", Kind: domain.AccountKind("checking"), Type: "checking", Currency: "USD"}
	require.NoError(t, st.CreateAccount(ctx, sp, bank))
	txn := &store.Transaction{AccountID: bank.ID, Date: day, Amount: domain.MustFromString("-12.34"), Currency: "USD", Source: domain.Source("manual"), Payee: "Amazon"}
	require.NoError(t, st.CreateTransaction(ctx, sp, txn))
	candidates, err := st.MerchantMatchCandidates(ctx, sp, *order, "", 10)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	orders, err := st.MerchantOrderCandidates(ctx, sp, nil, *txn, "100%", 10)
	require.NoError(t, err)
	require.Len(t, orders, 1)
	match := &store.MerchantMatch{TransactionID: txn.ID, OrderID: order.ID, Amount: domain.MustFromString("12.34"), Basis: "manual", Confidence: 1}
	require.NoError(t, st.SetMerchantMatch(ctx, sp, match))
	matches, err := st.ListMerchantMatches(ctx, sp, txn.ID)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, "12.34", matches[0].Amount.String())
	page, total, err := st.ListMerchantOrders(ctx, sp, store.MerchantOrderQuery{Merchant: domain.MerchantAmazon})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, page[0].Matched, 1)
	summary, err := st.MerchantSummary(ctx, sp, domain.MerchantAmazon)
	require.NoError(t, err)
	require.Equal(t, 1, summary.MatchedTransactions)
	require.NoError(t, st.DeleteMerchantMatchForOrder(ctx, sp, txn.ID, order.ID))
	matches, err = st.ListMerchantMatches(ctx, sp, txn.ID)
	require.NoError(t, err)
	require.Empty(t, matches)
}
