-- A code sent by text is typed in by the person, as with any code the app
-- cannot fetch, so "sms" is no longer a second factor a login can be set to.
-- A login that was set to it goes back to "" (type it yourself), which is how
-- it already behaved: nothing read the text for it.

-- +goose Up
UPDATE bill_connections SET second_factor = '' WHERE second_factor = 'sms';
UPDATE merchant_accounts SET second_factor = '' WHERE second_factor = 'sms';

ALTER TABLE bill_connections DROP CONSTRAINT bill_connections_second_factor_check;
ALTER TABLE bill_connections ADD CONSTRAINT bill_connections_second_factor_check
    CHECK (second_factor = ANY (ARRAY[''::text, 'email'::text, 'totp'::text]));

ALTER TABLE merchant_accounts DROP CONSTRAINT merchant_accounts_second_factor_check;
ALTER TABLE merchant_accounts ADD CONSTRAINT merchant_accounts_second_factor_check
    CHECK (second_factor = ANY (ARRAY[''::text, 'email'::text, 'totp'::text]));

-- +goose Down
ALTER TABLE bill_connections DROP CONSTRAINT bill_connections_second_factor_check;
ALTER TABLE bill_connections ADD CONSTRAINT bill_connections_second_factor_check
    CHECK (second_factor = ANY (ARRAY[''::text, 'email'::text, 'sms'::text, 'totp'::text]));

ALTER TABLE merchant_accounts DROP CONSTRAINT merchant_accounts_second_factor_check;
ALTER TABLE merchant_accounts ADD CONSTRAINT merchant_accounts_second_factor_check
    CHECK (second_factor = ANY (ARRAY[''::text, 'email'::text, 'sms'::text, 'totp'::text]));
