-- What an order's invoice says was refunded, and the day the invoice was last
-- read for it. A refund to a gift card balance names no order anywhere else,
-- so the total is what ties a balance's refund line to the order it came from.
-- NULL refund_total is an invoice nobody has read for it; zero is one that
-- showed no refund.

-- +goose Up
ALTER TABLE merchant_orders ADD COLUMN refund_total numeric(15,2);
ALTER TABLE merchant_orders ADD COLUMN refund_checked_on date;

-- +goose Down
ALTER TABLE merchant_orders DROP COLUMN refund_checked_on;
ALTER TABLE merchant_orders DROP COLUMN refund_total;
