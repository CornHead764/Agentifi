-- +goose Up
-- +goose StatementBegin
CREATE TABLE sessions (
    id TEXT NOT NULL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BLOB NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('web', 'device', 'personal')),
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER
) STRICT;

CREATE INDEX ix_sessions_user ON sessions (user_id);

DROP TABLE revoked_tokens;

ALTER TABLE users DROP COLUMN sessions_valid_from;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN sessions_valid_from INTEGER;

CREATE TABLE revoked_tokens (
    jti TEXT NOT NULL PRIMARY KEY,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX ix_revoked_tokens_expires ON revoked_tokens (expires_at);

DROP TABLE sessions;
-- +goose StatementEnd
