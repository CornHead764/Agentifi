package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// handPickPool is how many rows or orders a hand pick reads before ranking
// them: a search is not bounded by dates.
const handPickPool = 500

// orderFacts is what the matcher knows of each order. What a gift card paid
// never reached the bank: the invoice says so when read, otherwise the
// transactions page's gift card lines do. For a gift card row (giftCard) the
// figure that agrees is what the gift card paid. charges are every charge on
// the orders.
func (a *Merchants) orderFacts(
	ctx context.Context, spaceID store.SpaceID, orders []store.MerchantOrder, giftCard bool,
) (facts []domain.MerchantOrderFacts, charges []store.MerchantCharge, err error) {
	numbers := make([]string, 0, len(orders))
	for _, order := range orders {
		numbers = append(numbers, order.OrderNumber)
	}
	charges, err = a.store.MerchantChargesForOrders(ctx, spaceID, numbers)
	if err != nil {
		return nil, nil, err
	}
	giftByNumber := map[string]domain.Money{}
	for _, charge := range charges {
		if domain.IsGiftCardInstrument(charge.Instrument) {
			giftByNumber[charge.OrderNumber] = giftByNumber[charge.OrderNumber].Add(charge.Amount.Abs())
		}
	}
	facts = make([]domain.MerchantOrderFacts, 0, len(orders))
	for _, order := range orders {
		fact := domain.MerchantOrderFacts{
			Ref: order.ID.String(), OrderedOn: order.OrderedOn, Total: order.Total,
			GiftCard: giftByNumber[order.OrderNumber],
		}
		if order.HasGiftCard {
			fact.GiftCard = order.GiftCard
		}
		if giftCard {
			fact.Total, fact.GiftCard = fact.GiftCard, domain.Zero
		}
		shipped := merchantimport.Order{}
		for _, item := range order.Items {
			if item.HasTotalOwed {
				fact.Items = append(fact.Items, item.TotalOwed)
			}
			shipped.Items = append(shipped.Items, merchantimport.Item{
				TotalOwed: item.TotalOwed, HasTotalOwed: item.HasTotalOwed, ShippedOn: item.ShippedOn,
			})
		}
		fact.Shipments = shipped.Shipments()
		facts = append(facts, fact)
	}
	return facts, charges, nil
}

// cardCharges is the charges a card paid, keyed to the orders by id.
func cardCharges(charges []store.MerchantCharge, orders []store.MerchantOrder) []domain.MerchantChargeFacts {
	byNumber := make(map[string]uuid.UUID, len(orders))
	for _, order := range orders {
		byNumber[order.OrderNumber] = order.ID
	}
	out := make([]domain.MerchantChargeFacts, 0, len(charges))
	for _, charge := range charges {
		id, known := byNumber[charge.OrderNumber]
		if !known || domain.IsGiftCardInstrument(charge.Instrument) {
			continue
		}
		out = append(out, domain.MerchantChargeFacts{
			OrderRef: id.String(), ChargedOn: charge.ChargedOn, Amount: charge.Amount,
		})
	}
	return out
}

// OrderCandidates is the orders a person might match a bank row to by hand,
// ranked by domain.RankMerchantOrders. Empty merchants means every merchant.
func (a *Merchants) OrderCandidates(
	ctx context.Context, spaceID store.SpaceID, merchants []domain.MerchantID, txn store.Transaction,
	search string, limit int,
) ([]store.MerchantOrder, error) {
	orders, err := a.store.MerchantOrderCandidates(ctx, spaceID, merchants, txn, search, handPickPool)
	if err != nil {
		return nil, err
	}
	facts, charges, err := a.orderFacts(ctx, spaceID, orders, false)
	if err != nil {
		return nil, err
	}
	byRef := make(map[string]store.MerchantOrder, len(orders))
	for _, order := range orders {
		byRef[order.ID.String()] = order
	}
	ranked := domain.RankMerchantOrders(txn.Amount, txn.Date, facts, cardCharges(charges, orders))
	out := make([]store.MerchantOrder, 0, min(limit, len(ranked)))
	for _, fact := range ranked[:min(limit, len(ranked))] {
		out = append(out, byRef[fact.Ref])
	}
	return out, a.store.AttachMerchantMatches(ctx, out)
}

// RowCandidates is the bank rows a person might match to an order by hand,
// ranked by domain.RankMerchantRows.
func (a *Merchants) RowCandidates(
	ctx context.Context, spaceID store.SpaceID, order store.MerchantOrder, search string, limit int,
) ([]store.MerchantMatchCandidate, error) {
	found, err := a.store.MerchantMatchCandidates(ctx, spaceID, order, search, handPickPool)
	if err != nil {
		return nil, err
	}
	orders := []store.MerchantOrder{order}
	facts, charges, err := a.orderFacts(ctx, spaceID, orders, false)
	if err != nil {
		return nil, err
	}
	byRef := make(map[string]store.MerchantMatchCandidate, len(found))
	rows := make([]domain.MerchantRowFacts, 0, len(found))
	for _, one := range found {
		ref := one.Transaction.ID.String()
		byRef[ref] = one
		rows = append(rows, domain.MerchantRowFacts{Ref: ref, On: one.Transaction.Date, Amount: one.Transaction.Amount})
	}
	ranked := domain.RankMerchantRows(facts[0], cardCharges(charges, orders), rows)
	out := make([]store.MerchantMatchCandidate, 0, min(limit, len(ranked)))
	for _, row := range ranked[:min(limit, len(ranked))] {
		out = append(out, byRef[row.Ref])
	}
	return out, nil
}
