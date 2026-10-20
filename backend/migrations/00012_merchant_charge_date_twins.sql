-- A merchant's charge read on two days under a shifted date was stored twice,
-- the unique key being the date as well. Keep the later-dated row of any
-- charges of one order and amount within three days of each other.

-- +goose Up
DELETE FROM merchant_charges a
 USING merchant_charges b
 WHERE a.space_id = b.space_id
   AND a.merchant_account_id = b.merchant_account_id
   AND a.order_number = b.order_number
   AND a.amount = b.amount
   AND b.charged_on > a.charged_on
   AND b.charged_on - a.charged_on <= 3;

-- +goose Down
-- The rows deleted were copies of rows that remain; nothing restores them.
SELECT 1;
