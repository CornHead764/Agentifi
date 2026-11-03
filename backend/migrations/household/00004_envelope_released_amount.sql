-- +goose Up
-- +goose StatementBegin
ALTER TABLE envelopes ADD COLUMN released_amount INTEGER;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE envelopes DROP COLUMN released_amount;
-- +goose StatementEnd
