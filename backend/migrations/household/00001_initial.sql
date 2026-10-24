-- +goose NO TRANSACTION
-- Household SQLite baseline, squashed from Postgres migrations 00001 through 00010.
-- One file is one household: space_id columns/FKs and space-only indexes are removed.
-- User ids and server reference ids are plain TEXT, validated across files by the store.
-- Money is INTEGER minor units; folded search values are computed in Go.
-- Primary-currency amounts: *_primary, alert thresholds, envelopes, filters,
-- goals, institution thresholds, spending plans and watchlists.
-- Assistant correction amounts use their referenced transaction's currency.
-- Snapshot balances use the account currency; split amounts use the transaction.
-- Merchant child amounts use their order currency; gift-card balances and bill
-- payments use USD (the connectors currently cover United States providers).
-- Import timestamps truncate sub-milliseconds; added updated_at uses created_at,
-- or the parent's updated_at when a child has no creation timestamp.
-- Screenshot/trail references point at documents metadata; bytes live in attachments/.
-- PostgreSQL varchar length limits require application validation; STRICT enforces storage types.
-- +goose Up
-- +goose StatementBegin
BEGIN;

-- Authoritative currency marker; server.db households.primary_currency is a mirror.
-- household_uuid is the household this file belongs to; the store refuses to
-- open a file whose id is not the one in its name, so a renamed or misplaced
-- file is never served as another household.
CREATE TABLE household_metadata (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    household_uuid TEXT NOT NULL,
    primary_currency TEXT NOT NULL
) STRICT;

CREATE TABLE accounts (
    id TEXT NOT NULL,
    connection_id TEXT,
    institution_id TEXT,
    external_id TEXT,
    name TEXT NOT NULL,
    description TEXT,
    notes TEXT,
    kind TEXT NOT NULL,
    type TEXT NOT NULL,
    usage_type TEXT,
    currency TEXT NOT NULL,
    masked_number TEXT,
    logo_url TEXT,
    sort_order INTEGER NOT NULL,
    provider_balance INTEGER,
    provider_balance_at INTEGER,
    opening_balance INTEGER DEFAULT 0 NOT NULL,
    opening_balance_on TEXT CHECK (opening_balance_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    goal_balance INTEGER DEFAULT 0 NOT NULL,
    pending_holds INTEGER DEFAULT 0 NOT NULL,
    credit_limit INTEGER,
    statement_balance INTEGER,
    minimum_due INTEGER,
    due_date TEXT CHECK (due_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    -- Canonical decimal TEXT, like every domain.Rate (see internal/decimaltext).
    interest_rate TEXT CHECK ((interest_rate = '0' OR (interest_rate <> '-0' AND interest_rate NOT GLOB '*[^0-9.-]*' AND instr(substr(interest_rate, 2), '-') = 0 AND (ltrim(interest_rate, '-') GLOB '[1-9]*' OR ltrim(interest_rate, '-') GLOB '0.[0-9]*') AND ltrim(interest_rate, '-') NOT GLOB '*.*.*' AND ltrim(interest_rate, '-') NOT GLOB '*.' AND (instr(ltrim(interest_rate, '-'), '.') = 0 OR ltrim(interest_rate, '-') NOT GLOB '*0')))),
    statement_close_day INTEGER,
    excluded_from_reports INTEGER DEFAULT 0 NOT NULL CHECK (excluded_from_reports IN (0, 1)),
    excluded_from_spending_plan INTEGER DEFAULT 0 NOT NULL CHECK (excluded_from_spending_plan IN (0, 1)),
    excluded_from_account_bar INTEGER DEFAULT 0 NOT NULL CHECK (excluded_from_account_bar IN (0, 1)),
    include_in_net_worth INTEGER DEFAULT 1 NOT NULL CHECK (include_in_net_worth IN (0, 1)),
    exclude_bank_pending INTEGER DEFAULT 0 NOT NULL CHECK (exclude_bank_pending IN (0, 1)),
    is_closed INTEGER DEFAULT 0 NOT NULL CHECK (is_closed IN (0, 1)),
    closed_on TEXT CHECK (closed_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    simplefin_account_id TEXT,
    sync_floor_on TEXT CHECK (sync_floor_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    custom_logo_url TEXT,
    property_address TEXT,
    vehicle_vin TEXT,
    vehicle_mileage INTEGER,
    vehicle_mileage_as_of TEXT CHECK (vehicle_mileage_as_of GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    vehicle_miles_per_year INTEGER,
    valuation_source TEXT,
    valued_at INTEGER,
    secured_by_account_id TEXT,
    provider_extra TEXT CHECK (json_valid(provider_extra)),
    withheld_balance INTEGER,
    withheld_balance_at INTEGER,
    accept_zero_balance INTEGER DEFAULT 0 NOT NULL CHECK (accept_zero_balance IN (0, 1)),
    statement_bill_id TEXT,
    synced_through_on TEXT CHECK (synced_through_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    history_starts_on TEXT CHECK (history_starts_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    history_rebuilt_from TEXT CHECK (history_rebuilt_from GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    hide_below_balance INTEGER,
    ignored_at INTEGER,
    default_register_tab TEXT,
    withheld_balance_reason TEXT DEFAULT '' NOT NULL,
    requires_receipts INTEGER CHECK (requires_receipts IN (0, 1)),
    CONSTRAINT accounts_default_register_tab_check CHECK ((default_register_tab IN ('all', 'spending', 'income'))),
    CONSTRAINT accounts_pkey PRIMARY KEY (id),
    CONSTRAINT uq_account_connection_external UNIQUE (connection_id, external_id),
    CONSTRAINT accounts_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES connections(id) ON DELETE SET NULL,
    CONSTRAINT accounts_institution_id_fkey FOREIGN KEY (institution_id) REFERENCES institutions(id) ON DELETE SET NULL,
    CONSTRAINT accounts_secured_by_account_id_fkey FOREIGN KEY (secured_by_account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT accounts_statement_bill_id_fkey FOREIGN KEY (statement_bill_id) REFERENCES bills(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE alert_rules (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    alert_type TEXT NOT NULL,
    account_id TEXT,
    is_enabled INTEGER DEFAULT 1 NOT NULL CHECK (is_enabled IN (0, 1)),
    is_paused INTEGER DEFAULT 0 NOT NULL CHECK (is_paused IN (0, 1)),
    channel_email INTEGER DEFAULT 0 NOT NULL CHECK (channel_email IN (0, 1)),
    channel_push INTEGER DEFAULT 0 NOT NULL CHECK (channel_push IN (0, 1)),
    channel_in_app INTEGER DEFAULT 1 NOT NULL CHECK (channel_in_app IN (0, 1)),
    threshold_amount INTEGER,
    threshold_count INTEGER,
    -- Canonical percentage units (10 means 10%), not currency minor units.
    threshold_pct TEXT CHECK ((threshold_pct = '0' OR (threshold_pct <> '-0' AND threshold_pct NOT GLOB '*[^0-9.-]*' AND instr(substr(threshold_pct, 2), '-') = 0 AND (ltrim(threshold_pct, '-') GLOB '[1-9]*' OR ltrim(threshold_pct, '-') GLOB '0.[0-9]*') AND ltrim(threshold_pct, '-') NOT GLOB '*.*.*' AND ltrim(threshold_pct, '-') NOT GLOB '*.' AND (instr(ltrim(threshold_pct, '-'), '.') = 0 OR ltrim(threshold_pct, '-') NOT GLOB '*0')))),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT alert_rules_pkey PRIMARY KEY (id),
    CONSTRAINT uq_alert_rule_scope UNIQUE (user_id, alert_type, account_id),
    CONSTRAINT alert_rules_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE assistant_actions (
    id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    summary TEXT DEFAULT '' NOT NULL,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    body TEXT CHECK (json_valid(body)),
    status TEXT DEFAULT 'pending' NOT NULL,
    result TEXT DEFAULT '' NOT NULL,
    status_code INTEGER DEFAULT 0 NOT NULL,
    created_at INTEGER NOT NULL,
    decided_at INTEGER,
    proposed_body TEXT CHECK (json_valid(proposed_body)),
    preview TEXT CHECK (json_valid(preview)),
    group_id TEXT,
    resource_id TEXT,
    decline_reason TEXT DEFAULT '' NOT NULL,
    CONSTRAINT assistant_actions_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_actions_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES assistant_conversations(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE assistant_automation_runs (
    id TEXT NOT NULL,
    automation_id TEXT NOT NULL,
    fired_by TEXT NOT NULL,
    transaction_id TEXT,
    conversation_id TEXT,
    status TEXT DEFAULT 'queued' NOT NULL,
    subject TEXT DEFAULT '' NOT NULL,
    prompt TEXT DEFAULT '' NOT NULL,
    output TEXT DEFAULT '' NOT NULL,
    error TEXT DEFAULT '' NOT NULL,
    tool_calls INTEGER DEFAULT 0 NOT NULL,
    actions INTEGER DEFAULT 0 NOT NULL,
    queued_at INTEGER NOT NULL,
    started_at INTEGER,
    finished_at INTEGER,
    dry_run INTEGER DEFAULT 0 NOT NULL CHECK (dry_run IN (0, 1)),
    confidence REAL,
    decided_by TEXT DEFAULT '' NOT NULL,
    blind INTEGER DEFAULT 0 NOT NULL CHECK (blind IN (0, 1)),
    expected_category_id TEXT,
    error_code TEXT DEFAULT '' NOT NULL,
    batch_id TEXT,
    reviewed_when_queued INTEGER DEFAULT 0 NOT NULL CHECK (reviewed_when_queued IN (0, 1)),
    category_result TEXT DEFAULT '' NOT NULL,
    bulk INTEGER DEFAULT 0 NOT NULL CHECK (bulk IN (0, 1)),
    CONSTRAINT assistant_automation_runs_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_automation_runs_automation_id_fkey FOREIGN KEY (automation_id) REFERENCES assistant_automations(id) ON DELETE CASCADE,
    CONSTRAINT assistant_automation_runs_batch_id_fkey FOREIGN KEY (batch_id) REFERENCES category_suggestion_batches(id) ON DELETE SET NULL,
    CONSTRAINT assistant_automation_runs_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES assistant_conversations(id) ON DELETE SET NULL,
    CONSTRAINT assistant_automation_runs_expected_category_id_fkey FOREIGN KEY (expected_category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT assistant_automation_runs_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE assistant_automations (
    id TEXT NOT NULL,
    created_by TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT DEFAULT '' NOT NULL,
    is_enabled INTEGER DEFAULT 1 NOT NULL CHECK (is_enabled IN (0, 1)),
    trigger TEXT NOT NULL,
    trigger_config TEXT DEFAULT '{}' NOT NULL CHECK (json_valid(trigger_config)),
    prompt TEXT NOT NULL,
    context TEXT DEFAULT '{}' NOT NULL CHECK (json_valid(context)),
    mode TEXT DEFAULT 'propose' NOT NULL,
    tools TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(tools) AND json_type(tools) = 'array'),
    model TEXT DEFAULT '' NOT NULL,
    max_tool_rounds INTEGER DEFAULT 6 NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    confidence_threshold REAL DEFAULT 0 NOT NULL,
    template_key TEXT DEFAULT '' NOT NULL,
    filter_id TEXT,
    prompt_from_template INTEGER DEFAULT 0 NOT NULL CHECK (prompt_from_template IN (0, 1)),
    description_from_template INTEGER DEFAULT 0 NOT NULL CHECK (description_from_template IN (0, 1)),
    CONSTRAINT assistant_automations_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_automations_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE assistant_connections (
    id TEXT NOT NULL,
    name TEXT DEFAULT '' NOT NULL,
    base_url TEXT NOT NULL,
    model TEXT NOT NULL,
    api_key_encrypted TEXT DEFAULT '' NOT NULL,
    is_enabled INTEGER DEFAULT 1 NOT NULL CHECK (is_enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    allow_writes INTEGER DEFAULT 0 NOT NULL CHECK (allow_writes IN (0, 1)),
    apply_without_asking INTEGER DEFAULT 0 NOT NULL CHECK (apply_without_asking IN (0, 1)),
    tool_call_style TEXT DEFAULT 'native' NOT NULL,
    CONSTRAINT assistant_connections_pkey PRIMARY KEY (id)
) STRICT;
-- A UNIQUE(space_id) connection is a singleton in a one-household file.
CREATE UNIQUE INDEX uq_assistant_connection_space ON assistant_connections ((1));

CREATE TABLE assistant_conversations (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    title TEXT DEFAULT '' NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    automation_run_id TEXT,
    mail_id TEXT,
    CONSTRAINT assistant_conversations_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_conversations_mail_id_fkey FOREIGN KEY (mail_id) REFERENCES bill_emails(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE assistant_corrections (
    id TEXT NOT NULL,
    action_id TEXT NOT NULL,
    transaction_id TEXT,
    statement_name TEXT DEFAULT '' NOT NULL,
    payee TEXT DEFAULT '' NOT NULL,
    amount INTEGER,
    tool_name TEXT NOT NULL,
    memo TEXT DEFAULT '' NOT NULL,
    proposed_category_id TEXT,
    chosen_category_id TEXT,
    corrected_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    CONSTRAINT assistant_corrections_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_corrections_action_id_fkey FOREIGN KEY (action_id) REFERENCES assistant_actions(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE assistant_guidance (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    instruction TEXT DEFAULT '' NOT NULL,
    filter_id TEXT,
    is_active INTEGER DEFAULT 1 NOT NULL CHECK (is_active IN (0, 1)),
    "position" INTEGER DEFAULT 0 NOT NULL,
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    created_by TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT assistant_guidance_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_guidance_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE assistant_messages (
    id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT DEFAULT '' NOT NULL,
    tool_name TEXT DEFAULT '' NOT NULL,
    tool_arguments TEXT CHECK (json_valid(tool_arguments)),
    created_at INTEGER NOT NULL,
    CONSTRAINT assistant_messages_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_messages_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES assistant_conversations(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE balance_snapshots (
    id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    as_of TEXT NOT NULL CHECK (as_of GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    balance INTEGER DEFAULT 0 NOT NULL,
    balance_primary INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    is_imported INTEGER DEFAULT 0 NOT NULL CHECK (is_imported IN (0, 1)),
    anchor_on TEXT CHECK (anchor_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    anchor_balance INTEGER,
    CONSTRAINT balance_snapshots_pkey PRIMARY KEY (id),
    CONSTRAINT uq_balance_snapshot_account_day UNIQUE (account_id, as_of),
    CONSTRAINT balance_snapshots_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE bill_challenges (
    id TEXT NOT NULL,
    connection_id TEXT NOT NULL,
    agent_session TEXT NOT NULL,
    method TEXT NOT NULL,
    prompt TEXT DEFAULT '' NOT NULL,
    image TEXT,
    state TEXT NOT NULL,
    answered_by TEXT,
    raised_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    answered_at INTEGER,
    CONSTRAINT bill_challenges_pkey PRIMARY KEY (id),
    CONSTRAINT bill_challenges_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES bill_connections(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE bill_connections (
    id TEXT NOT NULL,
    biller TEXT NOT NULL,
    label TEXT NOT NULL,
    username TEXT DEFAULT '' NOT NULL,
    credential_source TEXT DEFAULT 'session' NOT NULL,
    session_state TEXT,
    profile_id TEXT,
    signed_in_at INTEGER,
    needs_sign_in INTEGER DEFAULT 0 NOT NULL CHECK (needs_sign_in IN (0, 1)),
    autopay_rule TEXT DEFAULT 'none' NOT NULL,
    autopay_days INTEGER,
    autopay_day INTEGER,
    autopay_account_id TEXT,
    pull_enabled INTEGER DEFAULT 1 NOT NULL CHECK (pull_enabled IN (0, 1)),
    pull_at TEXT,
    last_pulled_at INTEGER,
    last_pull_status TEXT DEFAULT '' NOT NULL,
    last_pull_error TEXT DEFAULT '' NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_keepalive_at INTEGER,
    credential_sealed TEXT,
    credential_has_totp INTEGER DEFAULT 0 NOT NULL CHECK (credential_has_totp IN (0, 1)),
    site TEXT,
    sign_in_paused_at INTEGER,
    sign_in_paused_for TEXT DEFAULT '' NOT NULL,
    second_factor TEXT DEFAULT '' NOT NULL,
    last_pull_screenshot_document_id TEXT REFERENCES documents(id) ON DELETE SET NULL,
    last_pull_trail_document_id TEXT REFERENCES documents(id) ON DELETE SET NULL,
    CONSTRAINT bill_connections_second_factor_check CHECK ((second_factor IN ('', 'email', 'totp'))),
    CONSTRAINT bill_connections_sign_in_paused_for_check CHECK ((sign_in_paused_for IN ('', 'password_refused', 'code_needed', 'page_check'))),
    CONSTRAINT bill_connections_pkey PRIMARY KEY (id),
    CONSTRAINT uq_bill_connection_label UNIQUE (biller, label),
    CONSTRAINT bill_connections_autopay_account_id_fkey FOREIGN KEY (autopay_account_id) REFERENCES accounts(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE bill_emails (
    id TEXT NOT NULL,
    connection_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    received_at INTEGER NOT NULL,
    sender TEXT NOT NULL,
    subject TEXT DEFAULT '' NOT NULL,
    biller TEXT DEFAULT '' NOT NULL,
    outcome TEXT NOT NULL,
    note TEXT DEFAULT '' NOT NULL,
    bill_id TEXT,
    document_id TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    rule_id TEXT,
    transaction_id TEXT,
    CONSTRAINT bill_emails_message_key UNIQUE (connection_id, message_id),
    CONSTRAINT bill_emails_pkey PRIMARY KEY (id),
    CONSTRAINT bill_emails_bill_id_fkey FOREIGN KEY (bill_id) REFERENCES bills(id) ON DELETE SET NULL,
    CONSTRAINT bill_emails_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES email_connections(id) ON DELETE CASCADE,
    CONSTRAINT bill_emails_document_id_fkey FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE SET NULL,
    CONSTRAINT bill_emails_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES mail_rules(id) ON DELETE SET NULL,
    CONSTRAINT bill_emails_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE bill_payments (
    id TEXT NOT NULL,
    subaccount_id TEXT NOT NULL,
    external_id TEXT NOT NULL,
    paid_on TEXT NOT NULL CHECK (paid_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    amount INTEGER NOT NULL,
    method TEXT DEFAULT '' NOT NULL,
    transaction_id TEXT,
    fetched_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT bill_payments_amount_positive CHECK ((amount > (0))),
    CONSTRAINT bill_payments_pkey PRIMARY KEY (id),
    CONSTRAINT uq_bill_payment UNIQUE (subaccount_id, external_id),
    CONSTRAINT bill_payments_subaccount_id_fkey FOREIGN KEY (subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE CASCADE,
    CONSTRAINT bill_payments_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE bill_subaccounts (
    id TEXT NOT NULL,
    connection_id TEXT NOT NULL,
    external_id TEXT NOT NULL,
    label TEXT NOT NULL,
    masked_number TEXT,
    is_selected INTEGER DEFAULT 1 NOT NULL CHECK (is_selected IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    account_id TEXT,
    CONSTRAINT bill_subaccounts_pkey PRIMARY KEY (id),
    CONSTRAINT uq_bill_subaccount_external UNIQUE (connection_id, external_id),
    CONSTRAINT bill_subaccounts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT bill_subaccounts_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES bill_connections(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE bills (
    id TEXT NOT NULL,
    subaccount_id TEXT NOT NULL,
    due_on TEXT NOT NULL CHECK (due_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    amount_due INTEGER NOT NULL,
    currency TEXT NOT NULL,
    issued_on TEXT CHECK (issued_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    period_start TEXT CHECK (period_start GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    period_end TEXT CHECK (period_end GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    autopay_on TEXT CHECK (autopay_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    status TEXT DEFAULT 'open' NOT NULL,
    source TEXT NOT NULL,
    external_id TEXT DEFAULT '' NOT NULL,
    statement_url TEXT DEFAULT '' NOT NULL,
    raw TEXT CHECK (json_valid(raw)),
    fetched_at INTEGER NOT NULL,
    amended_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    minimum_due INTEGER,
    invoice TEXT DEFAULT '' NOT NULL,
    marked_paid_at INTEGER,
    CONSTRAINT bills_pkey PRIMARY KEY (id),
    CONSTRAINT uq_bill_cycle UNIQUE (subaccount_id, due_on, invoice),
    CONSTRAINT bills_subaccount_id_fkey FOREIGN KEY (subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE cash_flow_forecasts (
    id TEXT NOT NULL,
    automation_id TEXT,
    automation_run_id TEXT,
    model TEXT DEFAULT '' NOT NULL,
    generated_at INTEGER NOT NULL,
    generated_on TEXT NOT NULL CHECK (generated_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    periods TEXT NOT NULL CHECK (json_valid(periods)),
    narrative TEXT DEFAULT '' NOT NULL,
    raw_answer TEXT DEFAULT '' NOT NULL,
    account_id TEXT,
    method TEXT DEFAULT 'model' NOT NULL,
    CONSTRAINT cash_flow_forecasts_method_check CHECK ((method IN ('model', 'average'))),
    CONSTRAINT cash_flow_forecasts_pkey PRIMARY KEY (id),
    CONSTRAINT cash_flow_forecasts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT cash_flow_forecasts_automation_id_fkey FOREIGN KEY (automation_id) REFERENCES assistant_automations(id) ON DELETE SET NULL,
    CONSTRAINT cash_flow_forecasts_run_id_fkey FOREIGN KEY (automation_run_id) REFERENCES assistant_automation_runs(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE categories (
    id TEXT NOT NULL,
    parent_id TEXT,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    known_category_id TEXT,
    txf_id TEXT,
    txf_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(txf_ids) AND json_type(txf_ids) = 'array'),
    is_user_assignable INTEGER DEFAULT 1 NOT NULL CHECK (is_user_assignable IN (0, 1)),
    is_editable INTEGER DEFAULT 1 NOT NULL CHECK (is_editable IN (0, 1)),
    excluded_from_reports INTEGER DEFAULT 0 NOT NULL CHECK (excluded_from_reports IN (0, 1)),
    excluded_from_spending_plan INTEGER DEFAULT 0 NOT NULL CHECK (excluded_from_spending_plan IN (0, 1)),
    excluded_from_category_list INTEGER DEFAULT 0 NOT NULL CHECK (excluded_from_category_list IN (0, 1)),
    sort_order INTEGER NOT NULL,
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT categories_pkey PRIMARY KEY (id),
    CONSTRAINT categories_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES categories(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE category_suggestion_batches (
    id TEXT NOT NULL,
    created_by TEXT NOT NULL,
    row_count INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    cancelled_at INTEGER,
    dismissed_at INTEGER,
    CONSTRAINT category_suggestion_batches_row_count_check CHECK ((row_count >= 0)),
    CONSTRAINT category_suggestion_batches_pkey PRIMARY KEY (id)
) STRICT;

CREATE TABLE connections (
    id TEXT NOT NULL,
    name TEXT,
    access_url_encrypted TEXT NOT NULL,
    status TEXT NOT NULL,
    status_detail TEXT,
    sync_errors TEXT CHECK (json_valid(sync_errors)),
    last_sync_at INTEGER,
    last_successful_sync_at INTEGER,
    retry_not_before INTEGER,
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT connections_pkey PRIMARY KEY (id)
) STRICT;

CREATE TABLE document_links (
    document_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    role TEXT DEFAULT 'attachment' NOT NULL,
    created_at INTEGER NOT NULL,
    CONSTRAINT document_links_pkey PRIMARY KEY (document_id, kind, target_id),
    CONSTRAINT document_links_document_id_fkey FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE documents (
    id TEXT NOT NULL,
    content_sha256 TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    filename TEXT NOT NULL,
    storage_key TEXT NOT NULL,
    source TEXT NOT NULL,
    source_ref TEXT DEFAULT '' NOT NULL,
    uploaded_by_user_id TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT documents_pkey PRIMARY KEY (id),
    CONSTRAINT documents_space_id_content_sha256_key UNIQUE (content_sha256)
) STRICT;

CREATE TABLE email_connections (
    id TEXT NOT NULL,
    label TEXT NOT NULL,
    kind TEXT NOT NULL,
    address TEXT NOT NULL,
    client_id TEXT DEFAULT '' NOT NULL,
    tenant TEXT DEFAULT '' NOT NULL,
    host TEXT DEFAULT '' NOT NULL,
    port INTEGER,
    username TEXT DEFAULT '' NOT NULL,
    secret TEXT,
    folder TEXT DEFAULT 'Inbox' NOT NULL,
    cursor TEXT CHECK (json_valid(cursor)),
    enabled INTEGER DEFAULT 1 NOT NULL CHECK (enabled IN (0, 1)),
    last_polled_at INTEGER,
    last_poll_error TEXT DEFAULT '' NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    poll_failures INTEGER DEFAULT 0 NOT NULL,
    poll_failing_since INTEGER,
    CONSTRAINT email_connections_label_key UNIQUE (label),
    CONSTRAINT email_connections_pkey PRIMARY KEY (id)
) STRICT;

CREATE TABLE envelopes (
    id TEXT NOT NULL,
    spending_plan_month_id TEXT NOT NULL,
    filter_id TEXT NOT NULL,
    recurring_group_id TEXT,
    name TEXT NOT NULL,
    target_amount INTEGER DEFAULT 0 NOT NULL,
    overwritten_target_amount INTEGER,
    calculated_spent_amount INTEGER DEFAULT 0 NOT NULL,
    rollover_amount INTEGER DEFAULT 0 NOT NULL,
    auto_release_rollover INTEGER DEFAULT 0 NOT NULL CHECK (auto_release_rollover IN (0, 1)),
    recurring INTEGER DEFAULT 1 NOT NULL CHECK (recurring IN (0, 1)),
    txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(txn_ids) AND json_type(txn_ids) = 'array'),
    excluded_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(excluded_txn_ids) AND json_type(excluded_txn_ids) = 'array'),
    hide_excluded_from_ui INTEGER DEFAULT 0 NOT NULL CHECK (hide_excluded_from_ui IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    rollover_is_carried INTEGER DEFAULT 0 NOT NULL CHECK (rollover_is_carried IN (0, 1)),
    CONSTRAINT envelopes_pkey PRIMARY KEY (id),
    CONSTRAINT envelopes_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT,
    CONSTRAINT envelopes_spending_plan_month_id_fkey FOREIGN KEY (spending_plan_month_id) REFERENCES spending_plan_months(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE filter_items (
    id TEXT NOT NULL,
    filter_id TEXT NOT NULL,
    field TEXT NOT NULL,
    operator TEXT NOT NULL,
    group_index INTEGER NOT NULL,
    "position" INTEGER NOT NULL,
    negated INTEGER DEFAULT 0 NOT NULL CHECK (negated IN (0, 1)),
    value_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(value_ids) AND json_type(value_ids) = 'array'),
    value_texts TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(value_texts) AND json_type(value_texts) = 'array'),
    text TEXT,
    amount_min INTEGER,
    amount_max INTEGER,
    date_from TEXT CHECK (date_from GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    date_to TEXT CHECK (date_to GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    date_preset TEXT,
    state INTEGER CHECK (state IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT filter_items_pkey PRIMARY KEY (id),
    CONSTRAINT filter_items_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE filters (
    id TEXT NOT NULL,
    name TEXT,
    scope TEXT NOT NULL,
    query_text TEXT,
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    "position" INTEGER DEFAULT 0 NOT NULL,
    CONSTRAINT filters_pkey PRIMARY KEY (id)
) STRICT;

CREATE TABLE goal_funding_accounts (
    goal_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    CONSTRAINT goal_funding_accounts_pkey PRIMARY KEY (goal_id, account_id),
    CONSTRAINT goal_funding_accounts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT goal_funding_accounts_goal_id_fkey FOREIGN KEY (goal_id) REFERENCES goals(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE goals (
    id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    notes TEXT,
    kind TEXT,
    image_url TEXT,
    tag_id TEXT,
    target_amount INTEGER DEFAULT 0 NOT NULL,
    target_on TEXT CHECK (target_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    start_on TEXT CHECK (start_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    completed_on TEXT CHECK (completed_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    contribution_amount INTEGER DEFAULT 0 NOT NULL,
    contribution_frequency TEXT,
    contributed_this_month INTEGER DEFAULT 0 NOT NULL,
    saved_so_far INTEGER DEFAULT 0 NOT NULL,
    spent INTEGER DEFAULT 0 NOT NULL,
    txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(txn_ids) AND json_type(txn_ids) = 'array'),
    is_taken_from_plan INTEGER DEFAULT 1 NOT NULL CHECK (is_taken_from_plan IN (0, 1)),
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    emoji TEXT,
    withdrawal_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(withdrawal_txn_ids) AND json_type(withdrawal_txn_ids) = 'array'),
    spending_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(spending_txn_ids) AND json_type(spending_txn_ids) = 'array'),
    closed_on TEXT CHECK (closed_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    CONSTRAINT goals_pkey PRIMARY KEY (id),
    CONSTRAINT goals_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT goals_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE holdings (
    id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    security_id TEXT NOT NULL,
    external_id TEXT,
    -- Canonical decimal TEXT: fractional position quantities can exceed 9.22e8 units.
    shares TEXT NOT NULL CHECK ((shares = '0' OR (shares <> '-0' AND shares NOT GLOB '*[^0-9.-]*' AND instr(substr(shares, 2), '-') = 0 AND (ltrim(shares, '-') GLOB '[1-9]*' OR ltrim(shares, '-') GLOB '0.[0-9]*') AND ltrim(shares, '-') NOT GLOB '*.*.*' AND ltrim(shares, '-') NOT GLOB '*.' AND (instr(ltrim(shares, '-'), '.') = 0 OR ltrim(shares, '-') NOT GLOB '*0')))),
    cost_basis INTEGER,
    -- Canonical decimal TEXT: per-share cost in arbitrary currencies can exceed 9.22e8.
    average_cost TEXT CHECK ((average_cost = '0' OR (average_cost <> '-0' AND average_cost NOT GLOB '*[^0-9.-]*' AND instr(substr(average_cost, 2), '-') = 0 AND (ltrim(average_cost, '-') GLOB '[1-9]*' OR ltrim(average_cost, '-') GLOB '0.[0-9]*') AND ltrim(average_cost, '-') NOT GLOB '*.*.*' AND ltrim(average_cost, '-') NOT GLOB '*.' AND (instr(ltrim(average_cost, '-'), '.') = 0 OR ltrim(average_cost, '-') NOT GLOB '*0')))),
    is_cost_basis_complete INTEGER DEFAULT 1 NOT NULL CHECK (is_cost_basis_complete IN (0, 1)),
    market_value INTEGER,
    as_of TEXT CHECK (as_of GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT holdings_pkey PRIMARY KEY (id),
    CONSTRAINT uq_holding_account_security UNIQUE (account_id, security_id),
    CONSTRAINT holdings_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT holdings_security_id_fkey FOREIGN KEY (security_id) REFERENCES securities(id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE ignored_remote_accounts (
    id TEXT NOT NULL,
    connection_id TEXT NOT NULL,
    external_id TEXT NOT NULL,
    name TEXT DEFAULT '' NOT NULL,
    institution TEXT DEFAULT '' NOT NULL,
    masked_number TEXT DEFAULT '' NOT NULL,
    ignored_at INTEGER NOT NULL,
    CONSTRAINT ignored_remote_accounts_pkey PRIMARY KEY (id),
    CONSTRAINT uq_ignored_remote_account UNIQUE (connection_id, external_id),
    CONSTRAINT ignored_remote_accounts_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES connections(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE institutions (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    external_id TEXT,
    domain TEXT,
    logo_url TEXT,
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    hide_below_balance INTEGER,
    CONSTRAINT institutions_pkey PRIMARY KEY (id),
    CONSTRAINT uq_institution_space_external UNIQUE (external_id)
) STRICT;

CREATE TABLE mail_rules (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    enabled INTEGER DEFAULT 1 NOT NULL CHECK (enabled IN (0, 1)),
    sender TEXT DEFAULT '' NOT NULL,
    subject_contains TEXT DEFAULT '' NOT NULL,
    body_contains TEXT DEFAULT '' NOT NULL,
    amount_label TEXT DEFAULT '' NOT NULL,
    amount_pattern TEXT DEFAULT '' NOT NULL,
    date_label TEXT DEFAULT '' NOT NULL,
    date_pattern TEXT DEFAULT '' NOT NULL,
    reference_label TEXT DEFAULT '' NOT NULL,
    reference_pattern TEXT DEFAULT '' NOT NULL,
    payee TEXT DEFAULT '' NOT NULL,
    payee_label TEXT DEFAULT '' NOT NULL,
    action TEXT DEFAULT 'transaction' NOT NULL,
    account_id TEXT,
    category_id TEXT,
    direction TEXT DEFAULT 'expense' NOT NULL,
    pad_income INTEGER DEFAULT 0 NOT NULL CHECK (pad_income IN (0, 1)),
    income_account_id TEXT,
    income_category_id TEXT,
    income_payee TEXT DEFAULT '' NOT NULL,
    sort_order INTEGER DEFAULT 0 NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    bill_connection_id TEXT,
    bill_subaccount_id TEXT,
    issued_label TEXT DEFAULT '' NOT NULL,
    issued_pattern TEXT DEFAULT '' NOT NULL,
    minimum_label TEXT DEFAULT '' NOT NULL,
    minimum_pattern TEXT DEFAULT '' NOT NULL,
    notes_label TEXT DEFAULT '' NOT NULL,
    notes_end_label TEXT DEFAULT '' NOT NULL,
    CONSTRAINT mail_rules_name_key UNIQUE (name),
    CONSTRAINT mail_rules_pkey PRIMARY KEY (id),
    CONSTRAINT mail_rules_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_bill_connection_id_fkey FOREIGN KEY (bill_connection_id) REFERENCES bill_connections(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_bill_subaccount_id_fkey FOREIGN KEY (bill_subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_income_account_id_fkey FOREIGN KEY (income_account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_income_category_id_fkey FOREIGN KEY (income_category_id) REFERENCES categories(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE manual_transfer_pairs (
    transfer_pair_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    CONSTRAINT manual_transfer_pairs_pkey PRIMARY KEY (transfer_pair_id)
) STRICT;

CREATE TABLE merchant_accounts (
    id TEXT NOT NULL,
    label TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    email TEXT DEFAULT '' NOT NULL,
    session_state TEXT,
    signed_in_at INTEGER,
    sync_enabled INTEGER DEFAULT 0 NOT NULL CHECK (sync_enabled IN (0, 1)),
    sync_days INTEGER DEFAULT 30 NOT NULL,
    last_synced_at INTEGER,
    last_sync_status TEXT DEFAULT '' NOT NULL,
    last_sync_error TEXT DEFAULT '' NOT NULL,
    needs_sign_in INTEGER DEFAULT 0 NOT NULL CHECK (needs_sign_in IN (0, 1)),
    gift_card_account_id TEXT,
    gift_card_balance INTEGER,
    gift_card_balance_at INTEGER,
    merchant TEXT DEFAULT 'amazon' NOT NULL,
    credential_sealed TEXT,
    credential_has_totp INTEGER DEFAULT 0 NOT NULL CHECK (credential_has_totp IN (0, 1)),
    sign_in_paused_at INTEGER,
    sign_in_paused_for TEXT DEFAULT '' NOT NULL,
    second_factor TEXT DEFAULT '' NOT NULL,
    invoice_backfill_at INTEGER,
    invoice_backfill_filed INTEGER DEFAULT 0 NOT NULL,
    invoice_backfill_left INTEGER DEFAULT 0 NOT NULL,
    invoice_backfill_stopped TEXT DEFAULT '' NOT NULL,
    last_sync_screenshot_document_id TEXT REFERENCES documents(id) ON DELETE SET NULL,
    CONSTRAINT merchant_accounts_second_factor_check CHECK ((second_factor IN ('', 'email', 'totp'))),
    CONSTRAINT merchant_accounts_sign_in_paused_for_check CHECK ((sign_in_paused_for IN ('', 'password_refused', 'code_needed', 'page_check'))),
    CONSTRAINT amazon_accounts_pkey PRIMARY KEY (id),
    CONSTRAINT amazon_accounts_gift_card_account_id_fkey FOREIGN KEY (gift_card_account_id) REFERENCES accounts(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE merchant_charges (
    id TEXT NOT NULL,
    merchant_account_id TEXT NOT NULL,
    order_number TEXT NOT NULL,
    charged_on TEXT NOT NULL CHECK (charged_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    amount INTEGER NOT NULL,
    instrument TEXT DEFAULT '' NOT NULL,
    created_at INTEGER NOT NULL,
    merchant TEXT DEFAULT 'amazon' NOT NULL,
    CONSTRAINT amazon_charges_pkey PRIMARY KEY (id),
    CONSTRAINT amazon_charges_space_id_amazon_account_id_order_number_char_key UNIQUE (merchant_account_id, order_number, charged_on, amount),
    CONSTRAINT amazon_charges_amazon_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE merchant_matches (
    transaction_id TEXT NOT NULL,
    order_id TEXT NOT NULL,
    amount INTEGER NOT NULL,
    basis TEXT NOT NULL,
    confidence REAL DEFAULT 1 NOT NULL,
    created_at INTEGER NOT NULL,
    refund_id TEXT,
    CONSTRAINT amazon_matches_pkey PRIMARY KEY (transaction_id, order_id),
    CONSTRAINT amazon_matches_order_id_fkey FOREIGN KEY (order_id) REFERENCES merchant_orders(id) ON DELETE CASCADE,
    CONSTRAINT amazon_matches_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE,
    CONSTRAINT merchant_matches_refund_id_fkey FOREIGN KEY (refund_id) REFERENCES merchant_refunds(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE merchant_order_items (
    id TEXT NOT NULL,
    order_id TEXT NOT NULL,
    "position" INTEGER NOT NULL,
    sku TEXT DEFAULT '' NOT NULL,
    title TEXT NOT NULL,
    quantity INTEGER DEFAULT 1 NOT NULL,
    unit_price INTEGER,
    total_owed INTEGER,
    shipped_on TEXT CHECK (shipped_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    condition TEXT DEFAULT '' NOT NULL,
    url TEXT DEFAULT '' NOT NULL,
    CONSTRAINT amazon_order_items_pkey PRIMARY KEY (id),
    CONSTRAINT amazon_order_items_order_id_fkey FOREIGN KEY (order_id) REFERENCES merchant_orders(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE merchant_orders (
    id TEXT NOT NULL,
    merchant_account_id TEXT NOT NULL,
    order_number TEXT NOT NULL,
    ordered_on TEXT NOT NULL CHECK (ordered_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    total INTEGER DEFAULT 0 NOT NULL,
    currency TEXT DEFAULT 'USD' NOT NULL,
    status TEXT DEFAULT '' NOT NULL,
    details_url TEXT DEFAULT '' NOT NULL,
    source TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    gift_card_amount INTEGER,
    tax INTEGER,
    shipping INTEGER,
    refund_total INTEGER,
    refund_checked_on TEXT CHECK (refund_checked_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    ignored_at INTEGER,
    merchant TEXT DEFAULT 'amazon' NOT NULL,
    kind TEXT DEFAULT 'online' NOT NULL,
    location TEXT DEFAULT '' NOT NULL,
    invoice_misses INTEGER DEFAULT 0 NOT NULL,
    CONSTRAINT amazon_orders_pkey PRIMARY KEY (id),
    CONSTRAINT amazon_orders_space_id_amazon_account_id_order_number_key UNIQUE (merchant_account_id, order_number),
    CONSTRAINT amazon_orders_amazon_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE merchant_refunds (
    id TEXT NOT NULL,
    merchant TEXT NOT NULL,
    merchant_account_id TEXT NOT NULL,
    order_number TEXT NOT NULL,
    sku TEXT DEFAULT '' NOT NULL,
    title TEXT DEFAULT '' NOT NULL,
    quantity INTEGER DEFAULT 1 NOT NULL,
    refunded_on TEXT NOT NULL CHECK (refunded_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    amount INTEGER NOT NULL,
    instrument TEXT DEFAULT '' NOT NULL,
    destination TEXT DEFAULT 'card' NOT NULL,
    status TEXT DEFAULT '' NOT NULL,
    source TEXT DEFAULT '' NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT merchant_refunds_pkey PRIMARY KEY (id),
    CONSTRAINT merchant_refunds_space_id_merchant_account_id_order_number__key UNIQUE (merchant_account_id, order_number, sku, refunded_on, amount),
    CONSTRAINT merchant_refunds_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE notifications (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    alert_type TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    url TEXT,
    data TEXT CHECK (json_valid(data)),
    dedupe_key TEXT NOT NULL,
    read_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    condition_key TEXT,
    resolved_at INTEGER,
    cleared_at INTEGER,
    CONSTRAINT notifications_pkey PRIMARY KEY (id),
    CONSTRAINT uq_notification_dedupe UNIQUE (user_id, dedupe_key)
) STRICT;

CREATE TABLE push_subscriptions (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    user_agent TEXT DEFAULT '' NOT NULL,
    created_at INTEGER NOT NULL,
    last_used_at INTEGER,
    CONSTRAINT push_subscriptions_pkey PRIMARY KEY (id),
    CONSTRAINT uq_push_subscription_endpoint UNIQUE (user_id, endpoint)
) STRICT;

CREATE TABLE rules (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    filter_id TEXT NOT NULL,
    priority INTEGER NOT NULL,
    is_active INTEGER DEFAULT 1 NOT NULL CHECK (is_active IN (0, 1)),
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    set_payee TEXT,
    set_category_id TEXT,
    add_tag_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(add_tag_ids) AND json_type(add_tag_ids) = 'array'),
    set_notes TEXT,
    set_excluded_from_reports INTEGER CHECK (set_excluded_from_reports IN (0, 1)),
    set_excluded_from_spending_plan INTEGER CHECK (set_excluded_from_spending_plan IN (0, 1)),
    set_is_reviewed INTEGER CHECK (set_is_reviewed IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    source_ref TEXT DEFAULT '' NOT NULL,
    CONSTRAINT rules_pkey PRIMARY KEY (id),
    CONSTRAINT rules_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT,
    CONSTRAINT rules_set_category_id_fkey FOREIGN KEY (set_category_id) REFERENCES categories(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE securities (
    id TEXT NOT NULL,
    symbol TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    exchange TEXT,
    currency TEXT NOT NULL,
    cusip TEXT,
    isin TEXT,
    -- Canonical decimal TEXT: quotes in arbitrary currencies can exceed 9.22e8.
    last_price TEXT CHECK ((last_price = '0' OR (last_price <> '-0' AND last_price NOT GLOB '*[^0-9.-]*' AND instr(substr(last_price, 2), '-') = 0 AND (ltrim(last_price, '-') GLOB '[1-9]*' OR ltrim(last_price, '-') GLOB '0.[0-9]*') AND ltrim(last_price, '-') NOT GLOB '*.*.*' AND ltrim(last_price, '-') NOT GLOB '*.' AND (instr(ltrim(last_price, '-'), '.') = 0 OR ltrim(last_price, '-') NOT GLOB '*0')))),
    last_price_at INTEGER,
    -- Canonical decimal TEXT: prior quotes have the same range as last_price.
    prior_close TEXT CHECK ((prior_close = '0' OR (prior_close <> '-0' AND prior_close NOT GLOB '*[^0-9.-]*' AND instr(substr(prior_close, 2), '-') = 0 AND (ltrim(prior_close, '-') GLOB '[1-9]*' OR ltrim(prior_close, '-') GLOB '0.[0-9]*') AND ltrim(prior_close, '-') NOT GLOB '*.*.*' AND ltrim(prior_close, '-') NOT GLOB '*.' AND (instr(ltrim(prior_close, '-'), '.') = 0 OR ltrim(prior_close, '-') NOT GLOB '*0')))),
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT securities_pkey PRIMARY KEY (id),
    CONSTRAINT uq_security_space_symbol UNIQUE (symbol)
) STRICT;

CREATE TABLE series (
    id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    category_id TEXT,
    kind TEXT NOT NULL,
    description TEXT NOT NULL,
    display_name TEXT,
    amount INTEGER DEFAULT 0 NOT NULL,
    currency TEXT NOT NULL,
    alias TEXT NOT NULL,
    frequency TEXT,
    "interval" INTEGER NOT NULL,
    by_month_day TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(by_month_day) AND json_type(by_month_day) = 'array'),
    by_day TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(by_day) AND json_type(by_day) = 'array'),
    start_on TEXT NOT NULL CHECK (start_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    end_on TEXT CHECK (end_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    next_due_on TEXT CHECK (next_due_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    override_next_due_on TEXT CHECK (override_next_due_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    override_next_amount INTEGER,
    auto_adjust_due_on INTEGER DEFAULT 0 NOT NULL CHECK (auto_adjust_due_on IN (0, 1)),
    reminder_days INTEGER NOT NULL,
    auto_accept_days INTEGER,
    match_criteria TEXT NOT NULL,
    match_amount_min INTEGER,
    match_amount_max INTEGER,
    learned_descriptions TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(learned_descriptions) AND json_type(learned_descriptions) = 'array'),
    template_payee TEXT,
    template_notes TEXT,
    template_tag_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(template_tag_ids) AND json_type(template_tag_ids) = 'array'),
    template_excluded_from_reports INTEGER DEFAULT 0 NOT NULL CHECK (template_excluded_from_reports IN (0, 1)),
    template_excluded_from_spending_plan INTEGER DEFAULT 0 NOT NULL CHECK (template_excluded_from_spending_plan IN (0, 1)),
    template_splits TEXT CHECK (json_valid(template_splits)),
    is_active INTEGER DEFAULT 1 NOT NULL CHECK (is_active IN (0, 1)),
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    by_month TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(by_month) AND json_type(by_month) = 'array'),
    CONSTRAINT series_pkey PRIMARY KEY (id),
    CONSTRAINT series_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT series_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE series_bill_links (
    series_id TEXT NOT NULL,
    subaccount_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    CONSTRAINT series_bill_links_pkey PRIMARY KEY (series_id),
    CONSTRAINT uq_series_bill_link_subaccount UNIQUE (subaccount_id),
    CONSTRAINT series_bill_links_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE,
    CONSTRAINT series_bill_links_subaccount_id_fkey FOREIGN KEY (subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE spending_plan_months (
    id TEXT NOT NULL,
    month TEXT NOT NULL CHECK (month GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    calculated_rollover_amount INTEGER DEFAULT 0 NOT NULL,
    calculated_income_amount INTEGER DEFAULT 0 NOT NULL,
    income_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(income_txn_ids) AND json_type(income_txn_ids) = 'array'),
    excluded_income_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(excluded_income_txn_ids) AND json_type(excluded_income_txn_ids) = 'array'),
    overwritten_income_amount INTEGER,
    reset_overwritten_income INTEGER DEFAULT 0 NOT NULL CHECK (reset_overwritten_income IN (0, 1)),
    calculated_bills_amount INTEGER DEFAULT 0 NOT NULL,
    bills_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(bills_txn_ids) AND json_type(bills_txn_ids) = 'array'),
    excluded_bills_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(excluded_bills_txn_ids) AND json_type(excluded_bills_txn_ids) = 'array'),
    overwritten_bills_amount INTEGER,
    reset_overwritten_bills INTEGER DEFAULT 0 NOT NULL CHECK (reset_overwritten_bills IN (0, 1)),
    calculated_subscriptions_amount INTEGER DEFAULT 0 NOT NULL,
    subscriptions_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(subscriptions_txn_ids) AND json_type(subscriptions_txn_ids) = 'array'),
    excluded_subscriptions_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(excluded_subscriptions_txn_ids) AND json_type(excluded_subscriptions_txn_ids) = 'array'),
    overwritten_subscriptions_amount INTEGER,
    reset_overwritten_subscriptions INTEGER DEFAULT 0 NOT NULL CHECK (reset_overwritten_subscriptions IN (0, 1)),
    calculated_transfer_amount INTEGER DEFAULT 0 NOT NULL,
    transfer_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(transfer_txn_ids) AND json_type(transfer_txn_ids) = 'array'),
    excluded_transfer_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(excluded_transfer_txn_ids) AND json_type(excluded_transfer_txn_ids) = 'array'),
    overwritten_transfer_amount INTEGER,
    reset_overwritten_transfer INTEGER DEFAULT 0 NOT NULL CHECK (reset_overwritten_transfer IN (0, 1)),
    calculated_goals_amount INTEGER DEFAULT 0 NOT NULL,
    goals_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(goals_txn_ids) AND json_type(goals_txn_ids) = 'array'),
    excluded_goals_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(excluded_goals_txn_ids) AND json_type(excluded_goals_txn_ids) = 'array'),
    overwritten_goals_amount INTEGER,
    reset_overwritten_goals INTEGER DEFAULT 0 NOT NULL CHECK (reset_overwritten_goals IN (0, 1)),
    calculated_planned_spending_amount INTEGER DEFAULT 0 NOT NULL,
    planned_spending_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(planned_spending_txn_ids) AND json_type(planned_spending_txn_ids) = 'array'),
    excluded_planned_spending_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(excluded_planned_spending_txn_ids) AND json_type(excluded_planned_spending_txn_ids) = 'array'),
    overwritten_planned_spending_amount INTEGER,
    reset_overwritten_planned_spending INTEGER DEFAULT 0 NOT NULL CHECK (reset_overwritten_planned_spending IN (0, 1)),
    calculated_spent_amount INTEGER DEFAULT 0 NOT NULL,
    spent_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(spent_txn_ids) AND json_type(spent_txn_ids) = 'array'),
    excluded_spent_txn_ids TEXT DEFAULT '[]' NOT NULL CHECK (json_valid(excluded_spent_txn_ids) AND json_type(excluded_spent_txn_ids) = 'array'),
    overwritten_spent_amount INTEGER,
    reset_overwritten_spent INTEGER DEFAULT 0 NOT NULL CHECK (reset_overwritten_spent IN (0, 1)),
    set_aside INTEGER DEFAULT 0 NOT NULL,
    total_to_spend_amount INTEGER DEFAULT 0 NOT NULL,
    left_to_spend_amount INTEGER DEFAULT 0 NOT NULL,
    projected_other_spending INTEGER DEFAULT 0 NOT NULL,
    projection_type TEXT NOT NULL,
    projection_start_date TEXT CHECK (projection_start_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    projection_end_date TEXT CHECK (projection_end_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    projection_buffer INTEGER DEFAULT 0 NOT NULL,
    projection_window_months INTEGER NOT NULL,
    is_closed_out INTEGER DEFAULT 0 NOT NULL CHECK (is_closed_out IN (0, 1)),
    closed_out_at INTEGER,
    show_closed_out INTEGER DEFAULT 0 NOT NULL CHECK (show_closed_out IN (0, 1)),
    new_month_viewed INTEGER DEFAULT 0 NOT NULL CHECK (new_month_viewed IN (0, 1)),
    hide_excluded_from_ui INTEGER DEFAULT 0 NOT NULL CHECK (hide_excluded_from_ui IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT spending_plan_months_pkey PRIMARY KEY (id),
    CONSTRAINT uq_spending_plan_space_month UNIQUE (month)
) STRICT;

CREATE TABLE split_tags (
    split_id TEXT NOT NULL,
    tag_id TEXT NOT NULL,
    CONSTRAINT split_tags_pkey PRIMARY KEY (split_id, tag_id),
    CONSTRAINT split_tags_split_id_fkey FOREIGN KEY (split_id) REFERENCES transaction_splits(id) ON DELETE CASCADE,
    CONSTRAINT split_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE suggestion_dismissals (
    id TEXT NOT NULL,
    signature TEXT NOT NULL,
    dismissed_at INTEGER NOT NULL,
    CONSTRAINT suggestion_dismissals_pkey PRIMARY KEY (id),
    CONSTRAINT uq_suggestion_dismissal UNIQUE (signature)
) STRICT;

CREATE TABLE tags (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    color TEXT,
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT tags_pkey PRIMARY KEY (id),
    CONSTRAINT uq_tag_space_name UNIQUE (name)
) STRICT;

CREATE TABLE transaction_refund_links (
    refund_txn_id TEXT NOT NULL,
    charge_txn_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    CONSTRAINT transaction_refund_links_distinct CHECK ((refund_txn_id <> charge_txn_id)),
    CONSTRAINT transaction_refund_links_pkey PRIMARY KEY (refund_txn_id, charge_txn_id),
    CONSTRAINT transaction_refund_links_charge_txn_id_fkey FOREIGN KEY (charge_txn_id) REFERENCES transactions(id) ON DELETE CASCADE,
    CONSTRAINT transaction_refund_links_refund_txn_id_fkey FOREIGN KEY (refund_txn_id) REFERENCES transactions(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE transaction_splits (
    id TEXT NOT NULL,
    transaction_id TEXT NOT NULL,
    "position" INTEGER NOT NULL,
    amount INTEGER NOT NULL,
    category_id TEXT,
    memo TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT transaction_splits_pkey PRIMARY KEY (id),
    CONSTRAINT transaction_splits_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT transaction_splits_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE transaction_tags (
    transaction_id TEXT NOT NULL,
    tag_id TEXT NOT NULL,
    CONSTRAINT transaction_tags_pkey PRIMARY KEY (transaction_id, tag_id),
    CONSTRAINT transaction_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE,
    CONSTRAINT transaction_tags_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE transactions (
    id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    external_id TEXT,
    date TEXT NOT NULL CHECK (date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    effective_date TEXT CHECK (effective_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    amount INTEGER NOT NULL,
    currency TEXT NOT NULL,
    amount_primary INTEGER,
    -- Canonical decimal TEXT: extreme currency ratios must match server fx_rates without overflow.
    fx_rate_used TEXT CHECK ((fx_rate_used = '0' OR (fx_rate_used <> '-0' AND fx_rate_used NOT GLOB '*[^0-9.-]*' AND instr(substr(fx_rate_used, 2), '-') = 0 AND (ltrim(fx_rate_used, '-') GLOB '[1-9]*' OR ltrim(fx_rate_used, '-') GLOB '0.[0-9]*') AND ltrim(fx_rate_used, '-') NOT GLOB '*.*.*' AND ltrim(fx_rate_used, '-') NOT GLOB '*.' AND (instr(ltrim(fx_rate_used, '-'), '.') = 0 OR ltrim(fx_rate_used, '-') NOT GLOB '*0')))),
    statement_name TEXT DEFAULT '' NOT NULL,
    payee TEXT DEFAULT '' NOT NULL,
    notes TEXT,
    check_number TEXT,
    category_id TEXT,
    source TEXT NOT NULL,
    is_pending INTEGER DEFAULT 0 NOT NULL CHECK (is_pending IN (0, 1)),
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    is_reviewed INTEGER DEFAULT 0 NOT NULL CHECK (is_reviewed IN (0, 1)),
    excluded_from_reports INTEGER DEFAULT 0 NOT NULL CHECK (excluded_from_reports IN (0, 1)),
    excluded_from_spending_plan INTEGER DEFAULT 0 NOT NULL CHECK (excluded_from_spending_plan IN (0, 1)),
    is_bill INTEGER DEFAULT 0 NOT NULL CHECK (is_bill IN (0, 1)),
    is_subscription INTEGER DEFAULT 0 NOT NULL CHECK (is_subscription IN (0, 1)),
    transfer_pair_id TEXT,
    user_flag TEXT,
    user_flag_note TEXT,
    series_id TEXT,
    series_due_on TEXT CHECK (series_due_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    estimate_status TEXT,
    accepted_on TEXT CHECK (accepted_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    expires_on TEXT CHECK (expires_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    rule_id TEXT,
    balance INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    needs_settle INTEGER DEFAULT 0 NOT NULL CHECK (needs_settle IN (0, 1)),
    memo TEXT DEFAULT '' NOT NULL,
    transacted_on TEXT CHECK (transacted_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    provider_extra TEXT CHECK (json_valid(provider_extra)),
    category_checked_at INTEGER,
    category_check_note TEXT DEFAULT '' NOT NULL,
    category_check_run_id TEXT,
    category_from_pair INTEGER DEFAULT 0 NOT NULL CHECK (category_from_pair IN (0, 1)),
    padded_txn_id TEXT,
    receipt_not_needed INTEGER DEFAULT 0 NOT NULL CHECK (receipt_not_needed IN (0, 1)),
    CONSTRAINT transactions_check CHECK ((padded_txn_id <> id)),
    CONSTRAINT transactions_pkey PRIMARY KEY (id),
    CONSTRAINT uq_transaction_account_external UNIQUE (account_id, external_id),
    CONSTRAINT transactions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE RESTRICT,
    CONSTRAINT transactions_category_check_run_id_fkey FOREIGN KEY (category_check_run_id) REFERENCES assistant_automation_runs(id) ON DELETE SET NULL,
    CONSTRAINT transactions_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT transactions_padded_txn_id_fkey FOREIGN KEY (padded_txn_id) REFERENCES transactions(id) ON DELETE CASCADE,
    CONSTRAINT transactions_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES rules(id) ON DELETE SET NULL,
    CONSTRAINT transactions_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE watchlists (
    id TEXT NOT NULL,
    filter_id TEXT NOT NULL,
    name TEXT NOT NULL,
    emoji TEXT,
    target_amount INTEGER,
    period TEXT NOT NULL,
    start_date TEXT CHECK (start_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    end_date TEXT CHECK (end_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    is_deleted INTEGER DEFAULT 0 NOT NULL CHECK (is_deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT watchlists_pkey PRIMARY KEY (id),
    CONSTRAINT watchlists_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE duplicate_candidates (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    first_txn_id TEXT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    second_txn_id TEXT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    days_apart INTEGER NOT NULL,
    verdict TEXT,
    kept_txn_id TEXT REFERENCES transactions(id) ON DELETE SET NULL,
    decided_by TEXT,
    decided_at INTEGER,
    created_at INTEGER NOT NULL,
    CONSTRAINT duplicate_candidates_ordered CHECK (first_txn_id < second_txn_id),
    CONSTRAINT duplicate_candidates_verdict CHECK (verdict IN ('duplicate', 'distinct')),
    CONSTRAINT duplicate_candidates_decided CHECK ((verdict IS NULL) = (decided_at IS NULL)),
    CONSTRAINT duplicate_candidates_pair_key UNIQUE (first_txn_id, second_txn_id)
) STRICT;

CREATE INDEX accounts_secured_by_idx ON accounts (secured_by_account_id) WHERE (secured_by_account_id IS NOT NULL);
CREATE INDEX bill_payments_space_unmatched ON bill_payments (id) WHERE (transaction_id IS NULL);
CREATE UNIQUE INDEX bill_payments_transaction ON bill_payments (transaction_id) WHERE (transaction_id IS NOT NULL);
CREATE UNIQUE INDEX bill_subaccounts_account_id_key ON bill_subaccounts (account_id) WHERE (account_id IS NOT NULL);
CREATE INDEX document_links_target ON document_links (kind, target_id);
CREATE INDEX documents_space_created ON documents (created_at);
CREATE INDEX ix_accounts_simplefin ON accounts (simplefin_account_id);
CREATE INDEX ix_assistant_actions_conversation ON assistant_actions (conversation_id, created_at);
CREATE INDEX ix_assistant_automation_runs_batch ON assistant_automation_runs (batch_id) WHERE (batch_id IS NOT NULL);
CREATE INDEX ix_assistant_automation_runs_feed ON assistant_automation_runs (queued_at DESC);
CREATE INDEX ix_assistant_automation_runs_queue ON assistant_automation_runs (bulk, queued_at, id) WHERE ((status) = 'queued');
CREATE INDEX ix_assistant_automations_space ON assistant_automations (created_at);
CREATE INDEX ix_assistant_conversations_feed ON assistant_conversations (user_id, updated_at);
CREATE INDEX ix_assistant_conversations_run ON assistant_conversations (automation_run_id) WHERE (automation_run_id IS NOT NULL);
CREATE INDEX ix_assistant_corrections_space ON assistant_corrections (created_at DESC);
CREATE INDEX ix_assistant_guidance_space ON assistant_guidance (is_deleted, "position");
CREATE INDEX ix_assistant_messages_conversation ON assistant_messages (conversation_id, created_at);
CREATE INDEX ix_balance_snapshots_space_day ON balance_snapshots (as_of);
CREATE INDEX ix_bill_challenges_waiting ON bill_challenges (connection_id, state);
CREATE INDEX ix_bill_emails_outcome ON bill_emails (outcome, received_at DESC);
CREATE INDEX ix_bill_emails_recent ON bill_emails (connection_id, received_at DESC);
CREATE INDEX ix_bills_subaccount_due ON bills (subaccount_id, due_on);
CREATE INDEX ix_cash_flow_forecasts_account_latest ON cash_flow_forecasts (account_id, generated_at DESC) WHERE (account_id IS NOT NULL);
CREATE INDEX ix_cash_flow_forecasts_latest ON cash_flow_forecasts (generated_at DESC);
CREATE INDEX ix_categories_known ON categories (known_category_id);
CREATE INDEX ix_category_suggestion_batches_space ON category_suggestion_batches (created_at DESC);
CREATE INDEX ix_email_connections_due ON email_connections (enabled, last_polled_at);
CREATE INDEX ix_envelopes_group ON envelopes (recurring_group_id);
CREATE INDEX ix_envelopes_month ON envelopes (spending_plan_month_id);
CREATE INDEX ix_filter_items_filter ON filter_items (filter_id, group_index, "position");
CREATE INDEX ix_holdings_space_account ON holdings (account_id);
CREATE INDEX ix_mail_rules_order ON mail_rules (sort_order, name);
CREATE INDEX ix_notifications_feed ON notifications (user_id, created_at);
CREATE INDEX ix_push_subscriptions_user ON push_subscriptions (user_id);
CREATE INDEX ix_series_due ON series (next_due_on);
CREATE INDEX ix_splits_category ON transaction_splits (category_id);
CREATE INDEX ix_transaction_refund_links_charge ON transaction_refund_links (charge_txn_id);
CREATE INDEX ix_transactions_category ON transactions (category_id);
CREATE INDEX ix_transactions_effective ON transactions (effective_date);
CREATE INDEX ix_transactions_register ON transactions (account_id, date);
CREATE INDEX ix_transactions_relink_dedupe ON transactions (account_id, date, amount);
CREATE INDEX ix_transactions_series_slot ON transactions (series_id, series_due_on);
CREATE INDEX ix_transactions_transfer_pair ON transactions (transfer_pair_id);
CREATE UNIQUE INDEX merchant_accounts_space_merchant_label_key ON merchant_accounts (merchant, label) WHERE ((label) <> '');
CREATE INDEX merchant_charges_space_merchant_date ON merchant_charges (merchant, charged_on);
CREATE INDEX merchant_matches_order ON merchant_matches (order_id);
CREATE INDEX merchant_order_items_order ON merchant_order_items (order_id);
CREATE INDEX merchant_orders_space_date ON merchant_orders (ordered_on);
CREATE INDEX merchant_orders_space_merchant_date ON merchant_orders (merchant, ordered_on);
CREATE INDEX merchant_refunds_order ON merchant_refunds (order_number);
CREATE INDEX merchant_refunds_space_merchant_date ON merchant_refunds (merchant, refunded_on);
CREATE INDEX transactions_needs_settle_idx ON transactions (created_at) WHERE needs_settle;
CREATE UNIQUE INDEX uq_assistant_automation_runs_transaction ON assistant_automation_runs (automation_id, transaction_id) WHERE ((fired_by) = 'transaction');
CREATE UNIQUE INDEX uq_notification_open_condition ON notifications (user_id, condition_key) WHERE ((condition_key IS NOT NULL) AND (resolved_at IS NULL));
CREATE UNIQUE INDEX uq_rules_source_ref ON rules (source_ref) WHERE ((source_ref) <> '');
CREATE UNIQUE INDEX ux_transactions_padded ON transactions (padded_txn_id) WHERE (padded_txn_id IS NOT NULL);
CREATE INDEX ix_duplicate_candidates_space_open ON duplicate_candidates (id) WHERE verdict IS NULL;
CREATE INDEX ix_duplicate_candidates_second ON duplicate_candidates (second_txn_id);
-- NULLS NOT DISTINCT: split nullable account scope into disjoint unique indexes.
CREATE UNIQUE INDEX uq_alert_rule_account ON alert_rules (user_id, alert_type, account_id) WHERE account_id IS NOT NULL;
CREATE UNIQUE INDEX uq_alert_rule_household ON alert_rules (user_id, alert_type) WHERE account_id IS NULL;

-- SQLite cannot assign NEW in a BEFORE trigger. The guarded AFTER update has
-- identical final-row semantics, including NULL-safe category comparisons.
-- UPDATE RETURNING observes the pre-AFTER-trigger flag; read the stored row
-- afterward when the caller needs the normalized category_from_pair value.
CREATE TRIGGER transactions_category_from_pair AFTER UPDATE OF category_id ON transactions
WHEN NEW.category_id IS NOT OLD.category_id AND NEW.category_from_pair = OLD.category_from_pair
BEGIN
    UPDATE transactions SET category_from_pair = 0 WHERE id = NEW.id;
END;

-- SQLite CHECK cannot contain json_each subqueries; preserve array containment
-- with validation triggers, including JSON element types (not numeric strings).
CREATE TRIGGER series_by_month_ins BEFORE INSERT ON series
WHEN EXISTS (SELECT 1 FROM json_each(NEW.by_month) WHERE type <> 'integer' OR value NOT BETWEEN 1 AND 12)
BEGIN
    SELECT RAISE(ABORT, 'series_by_month_check');
END;
CREATE TRIGGER series_by_month_upd BEFORE UPDATE OF by_month ON series
WHEN EXISTS (SELECT 1 FROM json_each(NEW.by_month) WHERE type <> 'integer' OR value NOT BETWEEN 1 AND 12)
BEGIN
    SELECT RAISE(ABORT, 'series_by_month_check');
END;

CREATE TABLE change_counter (id INTEGER PRIMARY KEY CHECK (id = 1), seq INTEGER NOT NULL) STRICT;
INSERT INTO change_counter VALUES (1, 0);
CREATE TABLE tombstones (
    seq INTEGER PRIMARY KEY,
    table_name TEXT NOT NULL,
    row_id TEXT NOT NULL,
    deleted_at INTEGER NOT NULL
) STRICT;

COMMIT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
BEGIN;
DROP TRIGGER series_by_month_upd;
DROP TRIGGER series_by_month_ins;
DROP TRIGGER transactions_category_from_pair;
DROP TABLE tombstones;
DROP TABLE change_counter;
DROP TABLE duplicate_candidates;
DROP TABLE watchlists;
DROP TABLE transactions;
DROP TABLE transaction_tags;
DROP TABLE transaction_splits;
DROP TABLE transaction_refund_links;
DROP TABLE tags;
DROP TABLE suggestion_dismissals;
DROP TABLE split_tags;
DROP TABLE spending_plan_months;
DROP TABLE series_bill_links;
DROP TABLE series;
DROP TABLE securities;
DROP TABLE rules;
DROP TABLE push_subscriptions;
DROP TABLE notifications;
DROP TABLE merchant_refunds;
DROP TABLE merchant_orders;
DROP TABLE merchant_order_items;
DROP TABLE merchant_matches;
DROP TABLE merchant_charges;
DROP TABLE merchant_accounts;
DROP TABLE manual_transfer_pairs;
DROP TABLE mail_rules;
DROP TABLE institutions;
DROP TABLE ignored_remote_accounts;
DROP TABLE holdings;
DROP TABLE goals;
DROP TABLE goal_funding_accounts;
DROP TABLE filters;
DROP TABLE filter_items;
DROP TABLE envelopes;
DROP TABLE email_connections;
DROP TABLE documents;
DROP TABLE document_links;
DROP TABLE connections;
DROP TABLE category_suggestion_batches;
DROP TABLE categories;
DROP TABLE cash_flow_forecasts;
DROP TABLE bills;
DROP TABLE bill_subaccounts;
DROP TABLE bill_payments;
DROP TABLE bill_emails;
DROP TABLE bill_connections;
DROP TABLE bill_challenges;
DROP TABLE balance_snapshots;
DROP TABLE assistant_messages;
DROP TABLE assistant_guidance;
DROP TABLE assistant_corrections;
DROP TABLE assistant_conversations;
DROP TABLE assistant_connections;
DROP TABLE assistant_automations;
DROP TABLE assistant_automation_runs;
DROP TABLE assistant_actions;
DROP TABLE alert_rules;
DROP TABLE accounts;
DROP TABLE household_metadata;
COMMIT;
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
