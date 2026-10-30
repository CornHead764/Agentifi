-- A sign-in page that puts a "Verify you are human" check in front of its form
-- cannot be signed in to unattended, so it pauses the scheduler like a refused
-- password or a code nobody can answer.

-- +goose Up
ALTER TABLE bill_connections DROP CONSTRAINT bill_connections_sign_in_paused_for_check;
ALTER TABLE bill_connections ADD CONSTRAINT bill_connections_sign_in_paused_for_check
    CHECK (sign_in_paused_for = ANY (ARRAY[''::text, 'password_refused'::text, 'code_needed'::text, 'page_check'::text]));

ALTER TABLE merchant_accounts DROP CONSTRAINT merchant_accounts_sign_in_paused_for_check;
ALTER TABLE merchant_accounts ADD CONSTRAINT merchant_accounts_sign_in_paused_for_check
    CHECK (sign_in_paused_for = ANY (ARRAY[''::text, 'password_refused'::text, 'code_needed'::text, 'page_check'::text]));

-- +goose Down
UPDATE bill_connections SET sign_in_paused_for = 'code_needed' WHERE sign_in_paused_for = 'page_check';
UPDATE merchant_accounts SET sign_in_paused_for = 'code_needed' WHERE sign_in_paused_for = 'page_check';

ALTER TABLE bill_connections DROP CONSTRAINT bill_connections_sign_in_paused_for_check;
ALTER TABLE bill_connections ADD CONSTRAINT bill_connections_sign_in_paused_for_check
    CHECK (sign_in_paused_for = ANY (ARRAY[''::text, 'password_refused'::text, 'code_needed'::text]));

ALTER TABLE merchant_accounts DROP CONSTRAINT merchant_accounts_sign_in_paused_for_check;
ALTER TABLE merchant_accounts ADD CONSTRAINT merchant_accounts_sign_in_paused_for_check
    CHECK (sign_in_paused_for = ANY (ARRAY[''::text, 'password_refused'::text, 'code_needed'::text]));
