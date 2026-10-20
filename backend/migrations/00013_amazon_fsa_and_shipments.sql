-- What Amazon states about which charge paid for which items of an order it
-- billed in parts: the FSA/HSA-eligible total its invoice shows (NULL until an
-- invoice has been read for it, zero when it showed none), the items it marks
-- eligible, and the shipment box each item sits in with the day that box
-- shipped or arrives.

-- +goose Up
ALTER TABLE merchant_orders ADD COLUMN fsa_hsa_amount numeric(15,2);
ALTER TABLE merchant_order_items ADD COLUMN fsa_hsa_eligible boolean NOT NULL DEFAULT false;
ALTER TABLE merchant_order_items ADD COLUMN shipment integer;
ALTER TABLE merchant_order_items ADD COLUMN shipment_on date;

-- +goose Down
ALTER TABLE merchant_order_items DROP COLUMN shipment_on;
ALTER TABLE merchant_order_items DROP COLUMN shipment;
ALTER TABLE merchant_order_items DROP COLUMN fsa_hsa_eligible;
ALTER TABLE merchant_orders DROP COLUMN fsa_hsa_amount;
