package seed

import (
	"context"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func seedMerchants(ctx context.Context, h *household) error {
	st, sid := h.st, h.space.ID
	a := store.MerchantAccount{Merchant: "amazon", Label: "Seed shared shopping", Email: "seed@example.invalid", SyncDays: 30}
	if err := st.CreateMerchantAccount(ctx, sid, &a); err != nil {
		return err
	}
	if err := st.SaveMerchantSession(ctx, sid, a.ID, a.Email, `{"cookies":[]}`, true); err != nil {
		return err
	}
	order := store.MerchantOrder{Merchant: a.Merchant, MerchantAccountID: a.ID, OrderNumber: "SEED-001", OrderedOn: h.date(0, -20), Total: money("49.95"), Currency: h.space.PrimaryCurrency, Status: "delivered", Source: "seed", HasTax: true, Tax: money("4.50"), Items: []store.MerchantOrderItem{{Position: 0, SKU: "SEED-MUG", Title: "Café mug ☕", Quantity: 1, UnitPrice: money("45.45"), HasUnitPrice: true, TotalOwed: money("49.95"), HasTotalOwed: true}}}
	if _, err := st.UpsertMerchantOrder(ctx, sid, &order); err != nil {
		return err
	}
	if _, err := st.UpsertMerchantCharge(ctx, sid, &store.MerchantCharge{Merchant: a.Merchant, MerchantAccountID: a.ID, OrderNumber: order.OrderNumber, ChargedOn: h.date(0, -19), Amount: money("49.95"), Instrument: "Visa •1234"}); err != nil {
		return err
	}
	refund := store.MerchantRefund{Merchant: a.Merchant, MerchantAccountID: a.ID, OrderNumber: order.OrderNumber, SKU: "SEED-MUG", Title: "Returned café mug", Quantity: 1, RefundedOn: h.date(0, -12), Amount: money("49.95"), Status: "completed", Source: "seed"}
	if _, err := st.UpsertMerchantRefund(ctx, sid, &refund); err != nil {
		return err
	}
	return st.SetMerchantMatch(ctx, sid, &store.MerchantMatch{TransactionID: h.txns[9].ID, OrderID: order.ID, Amount: money("49.95"), Basis: "manual", Confidence: 1})
}
