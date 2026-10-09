-- +goose Up
ALTER TABLE passkeys ADD COLUMN backup_eligible boolean;
COMMENT ON COLUMN passkeys.backup_eligible IS 'The authenticator''s backup-eligible flag from registration; null when not yet recorded, learned at the next login.';

-- +goose Down
ALTER TABLE passkeys DROP COLUMN backup_eligible;
