-- An Amazon order's total is before any gift card (domain.MerchantOrderFacts),
-- but the pull stored Amazon's own figure, which is what was left after one, so
-- the card's share came out short by the gift card a second time. Every order
-- the pull wrote with a gift card holds that figure.

-- +goose Up
UPDATE merchant_orders
   SET total = total + gift_card_amount
 WHERE merchant = 'amazon' AND source = 'agentifi_json' AND gift_card_amount > 0;

-- +goose Down
UPDATE merchant_orders
   SET total = total - gift_card_amount
 WHERE merchant = 'amazon' AND source = 'agentifi_json' AND gift_card_amount > 0;
