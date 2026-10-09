-- +goose Up
-- +goose StatementBegin
CREATE TABLE duplicate_candidates (
    id uuid PRIMARY KEY,
    space_id uuid NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    first_txn_id uuid NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    second_txn_id uuid NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    days_apart integer NOT NULL,
    verdict character varying(16),
    kept_txn_id uuid REFERENCES transactions(id) ON DELETE SET NULL,
    decided_by uuid REFERENCES users(id) ON DELETE SET NULL,
    decided_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT duplicate_candidates_ordered CHECK (first_txn_id < second_txn_id),
    CONSTRAINT duplicate_candidates_verdict CHECK (verdict IN ('duplicate', 'distinct')),
    CONSTRAINT duplicate_candidates_decided CHECK ((verdict IS NULL) = (decided_at IS NULL)),
    CONSTRAINT duplicate_candidates_pair_key UNIQUE (first_txn_id, second_txn_id)
);

CREATE INDEX ix_duplicate_candidates_space_open ON duplicate_candidates USING btree (space_id) WHERE verdict IS NULL;

CREATE INDEX ix_duplicate_candidates_second ON duplicate_candidates USING btree (second_txn_id);

COMMENT ON TABLE duplicate_candidates IS 'Two transactions proposed as one charge recorded twice, and the household''s verdict. A distinct verdict keeps the pair from being proposed again.';

COMMENT ON COLUMN duplicate_candidates.first_txn_id IS 'The lower of the two transaction ids, so a pair has one row however it was found.';

COMMENT ON COLUMN duplicate_candidates.verdict IS 'Null while undecided; duplicate when one copy was retired, distinct when both are real.';

COMMENT ON COLUMN duplicate_candidates.kept_txn_id IS 'The copy a duplicate verdict kept.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS duplicate_candidates CASCADE;
-- +goose StatementEnd
