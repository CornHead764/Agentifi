-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id TEXT NOT NULL PRIMARY KEY,
    email TEXT NOT NULL,
    hashed_password TEXT,
    full_name TEXT,
    oidc_subject TEXT,
    oidc_issuer TEXT,
    totp_secret TEXT,
    is_active INTEGER NOT NULL CHECK (is_active IN (0, 1)),
    is_superuser INTEGER NOT NULL CHECK (is_superuser IN (0, 1)),
    is_verified INTEGER NOT NULL CHECK (is_verified IN (0, 1)),
    locale TEXT NOT NULL,
    theme TEXT NOT NULL CHECK (theme IN ('light', 'dark', 'system')),
    privacy_mode INTEGER NOT NULL CHECK (privacy_mode IN (0, 1)),
    last_login_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    must_change_password INTEGER NOT NULL CHECK (must_change_password IN (0, 1)),
    sessions_valid_from INTEGER,
    swipe_left_action TEXT NOT NULL
        CHECK (swipe_left_action IN ('menu', 'review', 'flag', 'exclude', 'edit', 'none')),
    swipe_right_action TEXT NOT NULL
        CHECK (swipe_right_action IN ('menu', 'review', 'flag', 'exclude', 'edit', 'none')),
    animation_duration_ms INTEGER NOT NULL,
    toast_duration_ms INTEGER NOT NULL,
    UNIQUE (oidc_issuer, oidc_subject)
) STRICT;

CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

CREATE TABLE passkeys (
    id TEXT NOT NULL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id BLOB NOT NULL UNIQUE,
    public_key BLOB NOT NULL,
    sign_count INTEGER NOT NULL,
    name TEXT NOT NULL,
    transports TEXT,
    rp_id TEXT,
    is_discoverable INTEGER NOT NULL CHECK (is_discoverable IN (0, 1)),
    last_used_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    backup_eligible INTEGER CHECK (backup_eligible IN (0, 1))
) STRICT;

CREATE INDEX ix_passkeys_user ON passkeys (user_id);

CREATE TABLE recovery_codes (
    id TEXT NOT NULL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX ix_recovery_codes_user ON recovery_codes (user_id);

CREATE TABLE revoked_tokens (
    jti TEXT NOT NULL PRIMARY KEY,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX ix_revoked_tokens_expires ON revoked_tokens (expires_at);

CREATE TABLE households (
    id TEXT NOT NULL PRIMARY KEY,
    name TEXT NOT NULL,
    primary_currency TEXT NOT NULL,
    timezone TEXT NOT NULL,
    is_deleted INTEGER NOT NULL CHECK (is_deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    default_date_range TEXT,
    sidebar_account_types TEXT CHECK (json_valid(sidebar_account_types)),
    setup_guide_dismissed_at INTEGER,
    setup_guide_skipped TEXT NOT NULL
        CHECK (json_valid(setup_guide_skipped) AND json_type(setup_guide_skipped) = 'array'),
    file_name TEXT NOT NULL UNIQUE
) STRICT;

CREATE TABLE memberships (
    id TEXT NOT NULL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    invited_at INTEGER,
    accepted_at INTEGER,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    dashboard_layout TEXT CHECK (json_valid(dashboard_layout)),
    UNIQUE (household_id, user_id)
) STRICT;

CREATE INDEX ix_memberships_household_id ON memberships (household_id);
CREATE INDEX ix_memberships_user_id ON memberships (user_id);

CREATE TABLE server_settings (
    key TEXT NOT NULL PRIMARY KEY,
    value_encrypted TEXT NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE backup_runs (
    id TEXT NOT NULL PRIMARY KEY,
    trigger TEXT NOT NULL CHECK (trigger IN ('nightly', 'manual')),
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'skipped')),
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    set_name TEXT,
    encrypted INTEGER NOT NULL CHECK (encrypted IN (0, 1)),
    bytes INTEGER,
    error TEXT
) STRICT;

CREATE INDEX ix_backup_runs_started ON backup_runs (started_at DESC);

CREATE TABLE merchant_catalog (
    merchant TEXT NOT NULL,
    sku TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('found', 'missing')),
    title TEXT NOT NULL,
    brand TEXT NOT NULL,
    size TEXT NOT NULL,
    category TEXT NOT NULL,
    image_url TEXT NOT NULL,
    url TEXT NOT NULL,
    -- Price is in the currency's minor units.
    price INTEGER,
    price_currency TEXT CHECK (price_currency GLOB '[A-Z][A-Z][A-Z]'),
    source TEXT NOT NULL,
    source_ref TEXT NOT NULL,
    raw TEXT CHECK (json_valid(raw)),
    looked_up_at INTEGER NOT NULL,
    found_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (merchant, sku),
    CHECK ((price IS NULL) = (price_currency IS NULL))
) STRICT;

CREATE INDEX merchant_catalog_missing ON merchant_catalog (merchant, looked_up_at) WHERE status = 'missing';

-- Full numeric(20,10) ranges do not fit int64 at scale 10^10.
CREATE TABLE fx_rates (
    id TEXT NOT NULL PRIMARY KEY,
    base_currency TEXT NOT NULL,
    quote_currency TEXT NOT NULL,
    date TEXT NOT NULL CHECK (date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    -- Canonical decimal TEXT: a rate such as IRR→KWD (~7e-6) keeps only a few
    -- significant digits at scale 1e10.
    rate TEXT NOT NULL,
    source TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (base_currency, quote_currency, date)
) STRICT;

CREATE INDEX ix_fx_rates_quote_date ON fx_rates (quote_currency, date);

-- Symbols identify shared quotes without referencing household securities.
CREATE TABLE security_prices (
    id TEXT NOT NULL PRIMARY KEY,
    symbol TEXT NOT NULL,
    on_date TEXT NOT NULL CHECK (on_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    -- Canonical decimal TEXT: quotes in any currency can exceed 9.22e8.
    close TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (symbol, on_date)
) STRICT;
-- +goose StatementEnd
