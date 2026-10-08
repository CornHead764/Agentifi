-- The Agentifi schema for SQLite: every table with its constraints and
-- indexes, and the trigger that clears transactions.category_from_pair when a
-- row's category is changed by anything other than transfer pairing. A new
-- database applies this file first; each later change to the schema is a
-- migration numbered after it.
--
-- Storage classes (internal/dbconv reads and writes them):
--   money                               INTEGER hundredths (cents)
--   rates, prices, shares, percentages  TEXT, an exact decimal literal
--   uuid                                TEXT, canonical lowercase form
--   date                                TEXT, YYYY-MM-DD (checked by length)
--   timestamp                           TEXT, YYYY-MM-DDTHH:MM:SS.ffffffZ in UTC
--                                       (checked by length)
--   boolean                             INTEGER 0 or 1
--   array and json                      TEXT, a JSON document
--
-- now() and gen_random_uuid() are functions the server registers on every
-- connection (internal/sqlitedb), so a default naming now() needs the server
-- or a client that registers the same function.

-- +goose Up
-- +goose StatementBegin

-- accounts.provider_extra: Unmapped provider payload: SimpleFIN's extra object and any non-spec keys.
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
    provider_balance_at TEXT CHECK (length(provider_balance_at) = 27),
    opening_balance INTEGER DEFAULT 0 NOT NULL,
    opening_balance_on TEXT CHECK (length(opening_balance_on) = 10),
    goal_balance INTEGER DEFAULT 0 NOT NULL,
    pending_holds INTEGER DEFAULT 0 NOT NULL,
    credit_limit INTEGER,
    statement_balance INTEGER,
    minimum_due INTEGER,
    due_date TEXT CHECK (length(due_date) = 10),
    interest_rate TEXT,
    statement_close_day INTEGER,
    excluded_from_reports INTEGER DEFAULT 0 NOT NULL,
    excluded_from_spending_plan INTEGER DEFAULT 0 NOT NULL,
    excluded_from_account_bar INTEGER DEFAULT 0 NOT NULL,
    include_in_net_worth INTEGER DEFAULT 1 NOT NULL,
    exclude_bank_pending INTEGER DEFAULT 0 NOT NULL,
    is_closed INTEGER DEFAULT 0 NOT NULL,
    closed_on TEXT CHECK (length(closed_on) = 10),
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    simplefin_account_id TEXT,
    sync_floor_on TEXT CHECK (length(sync_floor_on) = 10),
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    custom_logo_url TEXT,
    property_address TEXT,
    vehicle_vin TEXT,
    vehicle_mileage INTEGER,
    vehicle_mileage_as_of TEXT CHECK (length(vehicle_mileage_as_of) = 10),
    vehicle_miles_per_year INTEGER,
    valuation_source TEXT,
    valued_at TEXT CHECK (length(valued_at) = 27),
    secured_by_account_id TEXT,
    provider_extra TEXT,
    withheld_balance INTEGER,
    withheld_balance_at TEXT CHECK (length(withheld_balance_at) = 27),
    accept_zero_balance INTEGER DEFAULT 0 NOT NULL,
    statement_bill_id TEXT,
    synced_through_on TEXT CHECK (length(synced_through_on) = 10),
    history_starts_on TEXT CHECK (length(history_starts_on) = 10),
    history_rebuilt_from TEXT CHECK (length(history_rebuilt_from) = 10),
    hide_below_balance INTEGER,
    ignored_at TEXT CHECK (length(ignored_at) = 27),
    default_register_tab TEXT,
    withheld_balance_reason TEXT DEFAULT '' NOT NULL,
    requires_receipts INTEGER,
    CONSTRAINT accounts_default_register_tab_check CHECK ((default_register_tab IN ('all', 'spending', 'income'))),
    CONSTRAINT accounts_pkey PRIMARY KEY (id),
    CONSTRAINT uq_account_connection_external UNIQUE (connection_id, external_id),
    CONSTRAINT accounts_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES connections(id) ON DELETE SET NULL,
    CONSTRAINT accounts_institution_id_fkey FOREIGN KEY (institution_id) REFERENCES institutions(id) ON DELETE SET NULL,
    CONSTRAINT accounts_secured_by_account_id_fkey FOREIGN KEY (secured_by_account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT accounts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT accounts_statement_bill_id_fkey FOREIGN KEY (statement_bill_id) REFERENCES bills(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE alert_rules (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    alert_type TEXT NOT NULL,
    account_id TEXT,
    is_enabled INTEGER DEFAULT 1 NOT NULL,
    is_paused INTEGER DEFAULT 0 NOT NULL,
    channel_email INTEGER DEFAULT 0 NOT NULL,
    channel_push INTEGER DEFAULT 0 NOT NULL,
    channel_in_app INTEGER DEFAULT 1 NOT NULL,
    threshold_amount INTEGER,
    threshold_count INTEGER,
    threshold_pct TEXT,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT alert_rules_pkey PRIMARY KEY (id),
    CONSTRAINT uq_alert_rule_scope UNIQUE (space_id, user_id, alert_type, account_id),
    CONSTRAINT alert_rules_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT alert_rules_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT alert_rules_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE assistant_actions (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    summary TEXT DEFAULT '' NOT NULL,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    body TEXT,
    status TEXT DEFAULT 'pending' NOT NULL,
    result TEXT DEFAULT '' NOT NULL,
    status_code INTEGER DEFAULT 0 NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    decided_at TEXT CHECK (length(decided_at) = 27),
    proposed_body TEXT,
    preview TEXT,
    group_id TEXT,
    resource_id TEXT,
    decline_reason TEXT DEFAULT '' NOT NULL,
    CONSTRAINT assistant_actions_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_actions_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES assistant_conversations(id) ON DELETE CASCADE,
    CONSTRAINT assistant_actions_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE assistant_automation_runs (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
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
    queued_at TEXT DEFAULT (now()) NOT NULL CHECK (length(queued_at) = 27),
    started_at TEXT CHECK (length(started_at) = 27),
    finished_at TEXT CHECK (length(finished_at) = 27),
    dry_run INTEGER DEFAULT 0 NOT NULL,
    confidence REAL,
    decided_by TEXT DEFAULT '' NOT NULL,
    blind INTEGER DEFAULT 0 NOT NULL,
    expected_category_id TEXT,
    error_code TEXT DEFAULT '' NOT NULL,
    batch_id TEXT,
    reviewed_when_queued INTEGER DEFAULT 0 NOT NULL,
    category_result TEXT DEFAULT '' NOT NULL,
    bulk INTEGER DEFAULT 0 NOT NULL,
    CONSTRAINT assistant_automation_runs_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_automation_runs_automation_id_fkey FOREIGN KEY (automation_id) REFERENCES assistant_automations(id) ON DELETE CASCADE,
    CONSTRAINT assistant_automation_runs_batch_id_fkey FOREIGN KEY (batch_id) REFERENCES category_suggestion_batches(id) ON DELETE SET NULL,
    CONSTRAINT assistant_automation_runs_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES assistant_conversations(id) ON DELETE SET NULL,
    CONSTRAINT assistant_automation_runs_expected_category_id_fkey FOREIGN KEY (expected_category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT assistant_automation_runs_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT assistant_automation_runs_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE assistant_automations (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    created_by TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT DEFAULT '' NOT NULL,
    is_enabled INTEGER DEFAULT 1 NOT NULL,
    trigger TEXT NOT NULL,
    trigger_config TEXT DEFAULT '{}' NOT NULL,
    prompt TEXT NOT NULL,
    context TEXT DEFAULT '{}' NOT NULL,
    mode TEXT DEFAULT 'propose' NOT NULL,
    tools TEXT DEFAULT '[]' NOT NULL,
    model TEXT DEFAULT '' NOT NULL,
    max_tool_rounds INTEGER DEFAULT 6 NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    confidence_threshold REAL DEFAULT 0 NOT NULL,
    template_key TEXT DEFAULT '' NOT NULL,
    filter_id TEXT,
    prompt_from_template INTEGER DEFAULT 0 NOT NULL,
    description_from_template INTEGER DEFAULT 0 NOT NULL,
    CONSTRAINT assistant_automations_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_automations_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT assistant_automations_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT,
    CONSTRAINT assistant_automations_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE assistant_connections (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    name TEXT DEFAULT '' NOT NULL,
    base_url TEXT NOT NULL,
    model TEXT NOT NULL,
    api_key_encrypted TEXT DEFAULT '' NOT NULL,
    is_enabled INTEGER DEFAULT 1 NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    allow_writes INTEGER DEFAULT 0 NOT NULL,
    apply_without_asking INTEGER DEFAULT 0 NOT NULL,
    tool_call_style TEXT DEFAULT 'native' NOT NULL,
    CONSTRAINT assistant_connections_pkey PRIMARY KEY (id),
    CONSTRAINT uq_assistant_connection_space UNIQUE (space_id),
    CONSTRAINT assistant_connections_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE assistant_conversations (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    title TEXT DEFAULT '' NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    automation_run_id TEXT,
    mail_id TEXT,
    CONSTRAINT assistant_conversations_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_conversations_mail_id_fkey FOREIGN KEY (mail_id) REFERENCES bill_emails(id) ON DELETE SET NULL,
    CONSTRAINT assistant_conversations_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT assistant_conversations_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE assistant_corrections (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
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
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    CONSTRAINT assistant_corrections_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_corrections_action_id_fkey FOREIGN KEY (action_id) REFERENCES assistant_actions(id) ON DELETE CASCADE,
    CONSTRAINT assistant_corrections_corrected_by_fkey FOREIGN KEY (corrected_by) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT assistant_corrections_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE assistant_guidance (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    name TEXT NOT NULL,
    instruction TEXT DEFAULT '' NOT NULL,
    filter_id TEXT,
    is_active INTEGER DEFAULT 1 NOT NULL,
    "position" INTEGER DEFAULT 0 NOT NULL,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    created_by TEXT,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT assistant_guidance_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_guidance_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL,
    CONSTRAINT assistant_guidance_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT,
    CONSTRAINT assistant_guidance_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE assistant_messages (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT DEFAULT '' NOT NULL,
    tool_name TEXT DEFAULT '' NOT NULL,
    tool_arguments TEXT,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    CONSTRAINT assistant_messages_pkey PRIMARY KEY (id),
    CONSTRAINT assistant_messages_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES assistant_conversations(id) ON DELETE CASCADE,
    CONSTRAINT assistant_messages_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE backup_runs (
    id TEXT NOT NULL,
    trigger TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT DEFAULT (now()) NOT NULL CHECK (length(started_at) = 27),
    finished_at TEXT CHECK (length(finished_at) = 27),
    set_name TEXT,
    encrypted INTEGER DEFAULT 0 NOT NULL,
    bytes INTEGER,
    error TEXT,
    CONSTRAINT backup_runs_status_check CHECK ((status IN ('running', 'succeeded', 'failed', 'skipped'))),
    CONSTRAINT backup_runs_trigger_check CHECK ((trigger IN ('nightly', 'manual'))),
    CONSTRAINT backup_runs_pkey PRIMARY KEY (id)
) STRICT;

CREATE TABLE balance_snapshots (
    id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    as_of TEXT NOT NULL CHECK (length(as_of) = 10),
    balance INTEGER DEFAULT 0 NOT NULL,
    balance_primary INTEGER,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    is_imported INTEGER DEFAULT 0 NOT NULL,
    anchor_on TEXT CHECK (length(anchor_on) = 10),
    anchor_balance INTEGER,
    CONSTRAINT balance_snapshots_pkey PRIMARY KEY (id),
    CONSTRAINT uq_balance_snapshot_account_day UNIQUE (account_id, as_of),
    CONSTRAINT balance_snapshots_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT balance_snapshots_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE bill_challenges (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    connection_id TEXT NOT NULL,
    agent_session TEXT NOT NULL,
    method TEXT NOT NULL,
    prompt TEXT DEFAULT '' NOT NULL,
    image TEXT,
    state TEXT NOT NULL,
    answered_by TEXT,
    raised_by TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    expires_at TEXT NOT NULL CHECK (length(expires_at) = 27),
    answered_at TEXT CHECK (length(answered_at) = 27),
    CONSTRAINT bill_challenges_pkey PRIMARY KEY (id),
    CONSTRAINT bill_challenges_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES bill_connections(id) ON DELETE CASCADE,
    CONSTRAINT bill_challenges_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE bill_connections (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    biller TEXT NOT NULL,
    label TEXT NOT NULL,
    username TEXT DEFAULT '' NOT NULL,
    credential_source TEXT DEFAULT 'session' NOT NULL,
    session_state TEXT,
    profile_id TEXT,
    signed_in_at TEXT CHECK (length(signed_in_at) = 27),
    needs_sign_in INTEGER DEFAULT 0 NOT NULL,
    autopay_rule TEXT DEFAULT 'none' NOT NULL,
    autopay_days INTEGER,
    autopay_day INTEGER,
    autopay_account_id TEXT,
    pull_enabled INTEGER DEFAULT 1 NOT NULL,
    pull_at TEXT,
    last_pulled_at TEXT CHECK (length(last_pulled_at) = 27),
    last_pull_status TEXT DEFAULT '' NOT NULL,
    last_pull_error TEXT DEFAULT '' NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    last_keepalive_at TEXT CHECK (length(last_keepalive_at) = 27),
    credential_sealed TEXT,
    credential_has_totp INTEGER DEFAULT 0 NOT NULL,
    site TEXT,
    sign_in_paused_at TEXT CHECK (length(sign_in_paused_at) = 27),
    sign_in_paused_for TEXT DEFAULT '' NOT NULL,
    second_factor TEXT DEFAULT '' NOT NULL,
    last_pull_screenshot BLOB,
    last_pull_trail TEXT,
    CONSTRAINT bill_connections_last_pull_screenshot_check CHECK ((length(last_pull_screenshot) <= 1048576)),
    CONSTRAINT bill_connections_second_factor_check CHECK ((second_factor IN ('', 'email', 'sms', 'totp'))),
    CONSTRAINT bill_connections_sign_in_paused_for_check CHECK ((sign_in_paused_for IN ('', 'password_refused', 'code_needed'))),
    CONSTRAINT bill_connections_pkey PRIMARY KEY (id),
    CONSTRAINT uq_bill_connection_label UNIQUE (space_id, biller, label),
    CONSTRAINT bill_connections_autopay_account_id_fkey FOREIGN KEY (autopay_account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT bill_connections_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE bill_emails (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    connection_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    received_at TEXT NOT NULL CHECK (length(received_at) = 27),
    sender TEXT NOT NULL,
    subject TEXT DEFAULT '' NOT NULL,
    biller TEXT DEFAULT '' NOT NULL,
    outcome TEXT NOT NULL,
    note TEXT DEFAULT '' NOT NULL,
    bill_id TEXT,
    document_id TEXT,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    rule_id TEXT,
    transaction_id TEXT,
    CONSTRAINT bill_emails_message_key UNIQUE (connection_id, message_id),
    CONSTRAINT bill_emails_pkey PRIMARY KEY (id),
    CONSTRAINT bill_emails_bill_id_fkey FOREIGN KEY (bill_id) REFERENCES bills(id) ON DELETE SET NULL,
    CONSTRAINT bill_emails_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES email_connections(id) ON DELETE CASCADE,
    CONSTRAINT bill_emails_document_id_fkey FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE SET NULL,
    CONSTRAINT bill_emails_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES mail_rules(id) ON DELETE SET NULL,
    CONSTRAINT bill_emails_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT bill_emails_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE bill_payments (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    subaccount_id TEXT NOT NULL,
    external_id TEXT NOT NULL,
    paid_on TEXT NOT NULL CHECK (length(paid_on) = 10),
    amount INTEGER NOT NULL,
    method TEXT DEFAULT '' NOT NULL,
    transaction_id TEXT,
    fetched_at TEXT NOT NULL CHECK (length(fetched_at) = 27),
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT bill_payments_amount_positive CHECK ((amount > 0)),
    CONSTRAINT bill_payments_pkey PRIMARY KEY (id),
    CONSTRAINT uq_bill_payment UNIQUE (subaccount_id, external_id),
    CONSTRAINT bill_payments_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT bill_payments_subaccount_id_fkey FOREIGN KEY (subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE CASCADE,
    CONSTRAINT bill_payments_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE bill_subaccounts (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    connection_id TEXT NOT NULL,
    external_id TEXT NOT NULL,
    label TEXT NOT NULL,
    masked_number TEXT,
    is_selected INTEGER DEFAULT 1 NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    account_id TEXT,
    CONSTRAINT bill_subaccounts_pkey PRIMARY KEY (id),
    CONSTRAINT uq_bill_subaccount_external UNIQUE (connection_id, external_id),
    CONSTRAINT bill_subaccounts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT bill_subaccounts_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES bill_connections(id) ON DELETE CASCADE,
    CONSTRAINT bill_subaccounts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE bills (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    subaccount_id TEXT NOT NULL,
    due_on TEXT NOT NULL CHECK (length(due_on) = 10),
    amount_due INTEGER NOT NULL,
    currency TEXT NOT NULL,
    issued_on TEXT CHECK (length(issued_on) = 10),
    period_start TEXT CHECK (length(period_start) = 10),
    period_end TEXT CHECK (length(period_end) = 10),
    autopay_on TEXT CHECK (length(autopay_on) = 10),
    status TEXT DEFAULT 'open' NOT NULL,
    source TEXT NOT NULL,
    external_id TEXT DEFAULT '' NOT NULL,
    statement_url TEXT DEFAULT '' NOT NULL,
    raw TEXT,
    fetched_at TEXT NOT NULL CHECK (length(fetched_at) = 27),
    amended_at TEXT CHECK (length(amended_at) = 27),
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    minimum_due INTEGER,
    invoice TEXT DEFAULT '' NOT NULL,
    marked_paid_at TEXT CHECK (length(marked_paid_at) = 27),
    CONSTRAINT bills_pkey PRIMARY KEY (id),
    CONSTRAINT uq_bill_cycle UNIQUE (subaccount_id, due_on, invoice),
    CONSTRAINT bills_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT bills_subaccount_id_fkey FOREIGN KEY (subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE cash_flow_forecasts (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    automation_id TEXT,
    automation_run_id TEXT,
    model TEXT DEFAULT '' NOT NULL,
    generated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(generated_at) = 27),
    generated_on TEXT NOT NULL CHECK (length(generated_on) = 10),
    periods TEXT NOT NULL,
    narrative TEXT DEFAULT '' NOT NULL,
    raw_answer TEXT DEFAULT '' NOT NULL,
    account_id TEXT,
    method TEXT DEFAULT 'model' NOT NULL,
    CONSTRAINT cash_flow_forecasts_method_check CHECK ((method IN ('model', 'average'))),
    CONSTRAINT cash_flow_forecasts_pkey PRIMARY KEY (id),
    CONSTRAINT cash_flow_forecasts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT cash_flow_forecasts_automation_id_fkey FOREIGN KEY (automation_id) REFERENCES assistant_automations(id) ON DELETE SET NULL,
    CONSTRAINT cash_flow_forecasts_run_id_fkey FOREIGN KEY (automation_run_id) REFERENCES assistant_automation_runs(id) ON DELETE SET NULL,
    CONSTRAINT cash_flow_forecasts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE categories (
    id TEXT NOT NULL,
    parent_id TEXT,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    known_category_id TEXT,
    txf_id TEXT,
    txf_ids TEXT DEFAULT '[]' NOT NULL,
    is_user_assignable INTEGER DEFAULT 1 NOT NULL,
    is_editable INTEGER DEFAULT 1 NOT NULL,
    excluded_from_reports INTEGER DEFAULT 0 NOT NULL,
    excluded_from_spending_plan INTEGER DEFAULT 0 NOT NULL,
    excluded_from_category_list INTEGER DEFAULT 0 NOT NULL,
    sort_order INTEGER NOT NULL,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT categories_pkey PRIMARY KEY (id),
    CONSTRAINT categories_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT categories_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE category_suggestion_batches (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    created_by TEXT NOT NULL,
    row_count INTEGER NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    cancelled_at TEXT CHECK (length(cancelled_at) = 27),
    dismissed_at TEXT CHECK (length(dismissed_at) = 27),
    CONSTRAINT category_suggestion_batches_row_count_check CHECK ((row_count >= 0)),
    CONSTRAINT category_suggestion_batches_pkey PRIMARY KEY (id),
    CONSTRAINT category_suggestion_batches_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE connections (
    id TEXT NOT NULL,
    name TEXT,
    access_url_encrypted TEXT NOT NULL,
    status TEXT NOT NULL,
    status_detail TEXT,
    sync_errors TEXT,
    last_sync_at TEXT CHECK (length(last_sync_at) = 27),
    last_successful_sync_at TEXT CHECK (length(last_successful_sync_at) = 27),
    retry_not_before TEXT CHECK (length(retry_not_before) = 27),
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT connections_pkey PRIMARY KEY (id),
    CONSTRAINT connections_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE document_links (
    document_id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    role TEXT DEFAULT 'attachment' NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    CONSTRAINT document_links_pkey PRIMARY KEY (document_id, kind, target_id),
    CONSTRAINT document_links_document_id_fkey FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE,
    CONSTRAINT document_links_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE documents (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    content_sha256 TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    filename TEXT NOT NULL,
    storage_key TEXT NOT NULL,
    source TEXT NOT NULL,
    source_ref TEXT DEFAULT '' NOT NULL,
    uploaded_by_user_id TEXT,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT documents_pkey PRIMARY KEY (id),
    CONSTRAINT documents_space_id_content_sha256_key UNIQUE (space_id, content_sha256),
    CONSTRAINT documents_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT documents_uploaded_by_user_id_fkey FOREIGN KEY (uploaded_by_user_id) REFERENCES users(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE email_connections (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
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
    cursor TEXT,
    enabled INTEGER DEFAULT 1 NOT NULL,
    last_polled_at TEXT CHECK (length(last_polled_at) = 27),
    last_poll_error TEXT DEFAULT '' NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    poll_failures INTEGER DEFAULT 0 NOT NULL,
    poll_failing_since TEXT CHECK (length(poll_failing_since) = 27),
    CONSTRAINT email_connections_label_key UNIQUE (space_id, label),
    CONSTRAINT email_connections_pkey PRIMARY KEY (id),
    CONSTRAINT email_connections_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
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
    auto_release_rollover INTEGER DEFAULT 0 NOT NULL,
    recurring INTEGER DEFAULT 1 NOT NULL,
    txn_ids TEXT DEFAULT '[]' NOT NULL,
    excluded_txn_ids TEXT DEFAULT '[]' NOT NULL,
    hide_excluded_from_ui INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    rollover_is_carried INTEGER DEFAULT 0 NOT NULL,
    CONSTRAINT envelopes_pkey PRIMARY KEY (id),
    CONSTRAINT envelopes_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT,
    CONSTRAINT envelopes_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT envelopes_spending_plan_month_id_fkey FOREIGN KEY (spending_plan_month_id) REFERENCES spending_plan_months(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE filter_items (
    id TEXT NOT NULL,
    filter_id TEXT NOT NULL,
    field TEXT NOT NULL,
    operator TEXT NOT NULL,
    group_index INTEGER NOT NULL,
    "position" INTEGER NOT NULL,
    negated INTEGER DEFAULT 0 NOT NULL,
    value_ids TEXT DEFAULT '[]' NOT NULL,
    value_texts TEXT DEFAULT '[]' NOT NULL,
    text TEXT,
    amount_min INTEGER,
    amount_max INTEGER,
    date_from TEXT CHECK (length(date_from) = 10),
    date_to TEXT CHECK (length(date_to) = 10),
    date_preset TEXT,
    state INTEGER,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT filter_items_pkey PRIMARY KEY (id),
    CONSTRAINT filter_items_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE CASCADE,
    CONSTRAINT filter_items_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE filters (
    id TEXT NOT NULL,
    name TEXT,
    scope TEXT NOT NULL,
    query_text TEXT,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    "position" INTEGER DEFAULT 0 NOT NULL,
    CONSTRAINT filters_pkey PRIMARY KEY (id),
    CONSTRAINT filters_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE fx_rates (
    id TEXT NOT NULL,
    base_currency TEXT NOT NULL,
    quote_currency TEXT NOT NULL,
    date TEXT NOT NULL CHECK (length(date) = 10),
    rate TEXT NOT NULL,
    source TEXT NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT fx_rates_pkey PRIMARY KEY (id),
    CONSTRAINT uq_fx_rate_space_base_quote_date UNIQUE (space_id, base_currency, quote_currency, date),
    CONSTRAINT fx_rates_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
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
    target_on TEXT CHECK (length(target_on) = 10),
    start_on TEXT CHECK (length(start_on) = 10),
    completed_on TEXT CHECK (length(completed_on) = 10),
    contribution_amount INTEGER DEFAULT 0 NOT NULL,
    contribution_frequency TEXT,
    contributed_this_month INTEGER DEFAULT 0 NOT NULL,
    saved_so_far INTEGER DEFAULT 0 NOT NULL,
    spent INTEGER DEFAULT 0 NOT NULL,
    txn_ids TEXT DEFAULT '[]' NOT NULL,
    is_taken_from_plan INTEGER DEFAULT 1 NOT NULL,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    emoji TEXT,
    withdrawal_txn_ids TEXT DEFAULT '[]' NOT NULL,
    spending_txn_ids TEXT DEFAULT '[]' NOT NULL,
    closed_on TEXT CHECK (length(closed_on) = 10),
    CONSTRAINT goals_pkey PRIMARY KEY (id),
    CONSTRAINT goals_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT goals_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT goals_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE holdings (
    id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    security_id TEXT NOT NULL,
    external_id TEXT,
    shares TEXT NOT NULL,
    cost_basis INTEGER,
    average_cost TEXT,
    is_cost_basis_complete INTEGER DEFAULT 1 NOT NULL,
    market_value INTEGER,
    as_of TEXT CHECK (length(as_of) = 10),
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT holdings_pkey PRIMARY KEY (id),
    CONSTRAINT uq_holding_account_security UNIQUE (account_id, security_id),
    CONSTRAINT holdings_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT holdings_security_id_fkey FOREIGN KEY (security_id) REFERENCES securities(id) ON DELETE RESTRICT,
    CONSTRAINT holdings_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE ignored_remote_accounts (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    connection_id TEXT NOT NULL,
    external_id TEXT NOT NULL,
    name TEXT DEFAULT '' NOT NULL,
    institution TEXT DEFAULT '' NOT NULL,
    masked_number TEXT DEFAULT '' NOT NULL,
    ignored_at TEXT DEFAULT (now()) NOT NULL CHECK (length(ignored_at) = 27),
    CONSTRAINT ignored_remote_accounts_pkey PRIMARY KEY (id),
    CONSTRAINT uq_ignored_remote_account UNIQUE (connection_id, external_id),
    CONSTRAINT ignored_remote_accounts_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES connections(id) ON DELETE CASCADE,
    CONSTRAINT ignored_remote_accounts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE institutions (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    external_id TEXT,
    domain TEXT,
    logo_url TEXT,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    hide_below_balance INTEGER,
    CONSTRAINT institutions_pkey PRIMARY KEY (id),
    CONSTRAINT uq_institution_space_external UNIQUE (space_id, external_id),
    CONSTRAINT institutions_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE mail_rules (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    name TEXT NOT NULL,
    enabled INTEGER DEFAULT 1 NOT NULL,
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
    pad_income INTEGER DEFAULT 0 NOT NULL,
    income_account_id TEXT,
    income_category_id TEXT,
    income_payee TEXT DEFAULT '' NOT NULL,
    sort_order INTEGER DEFAULT 0 NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    bill_connection_id TEXT,
    bill_subaccount_id TEXT,
    issued_label TEXT DEFAULT '' NOT NULL,
    issued_pattern TEXT DEFAULT '' NOT NULL,
    minimum_label TEXT DEFAULT '' NOT NULL,
    minimum_pattern TEXT DEFAULT '' NOT NULL,
    notes_label TEXT DEFAULT '' NOT NULL,
    notes_end_label TEXT DEFAULT '' NOT NULL,
    CONSTRAINT mail_rules_name_key UNIQUE (space_id, name),
    CONSTRAINT mail_rules_pkey PRIMARY KEY (id),
    CONSTRAINT mail_rules_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_bill_connection_id_fkey FOREIGN KEY (bill_connection_id) REFERENCES bill_connections(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_bill_subaccount_id_fkey FOREIGN KEY (bill_subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_income_account_id_fkey FOREIGN KEY (income_account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_income_category_id_fkey FOREIGN KEY (income_category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT mail_rules_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE manual_transfer_pairs (
    transfer_pair_id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    CONSTRAINT manual_transfer_pairs_pkey PRIMARY KEY (transfer_pair_id),
    CONSTRAINT manual_transfer_pairs_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE memberships (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    role TEXT NOT NULL,
    invited_at TEXT CHECK (length(invited_at) = 27),
    accepted_at TEXT CHECK (length(accepted_at) = 27),
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    dashboard_layout TEXT,
    CONSTRAINT memberships_pkey PRIMARY KEY (id),
    CONSTRAINT uq_membership_space_user UNIQUE (space_id, user_id),
    CONSTRAINT memberships_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT memberships_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE merchant_accounts (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    label TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    email TEXT DEFAULT '' NOT NULL,
    session_state TEXT,
    signed_in_at TEXT CHECK (length(signed_in_at) = 27),
    sync_enabled INTEGER DEFAULT 0 NOT NULL,
    sync_days INTEGER DEFAULT 30 NOT NULL,
    last_synced_at TEXT CHECK (length(last_synced_at) = 27),
    last_sync_status TEXT DEFAULT '' NOT NULL,
    last_sync_error TEXT DEFAULT '' NOT NULL,
    needs_sign_in INTEGER DEFAULT 0 NOT NULL,
    gift_card_account_id TEXT,
    gift_card_balance INTEGER,
    gift_card_balance_at TEXT CHECK (length(gift_card_balance_at) = 27),
    merchant TEXT DEFAULT 'amazon' NOT NULL,
    credential_sealed TEXT,
    credential_has_totp INTEGER DEFAULT 0 NOT NULL,
    sign_in_paused_at TEXT CHECK (length(sign_in_paused_at) = 27),
    sign_in_paused_for TEXT DEFAULT '' NOT NULL,
    second_factor TEXT DEFAULT '' NOT NULL,
    invoice_backfill_at TEXT CHECK (length(invoice_backfill_at) = 27),
    invoice_backfill_filed INTEGER DEFAULT 0 NOT NULL,
    invoice_backfill_left INTEGER DEFAULT 0 NOT NULL,
    invoice_backfill_stopped TEXT DEFAULT '' NOT NULL,
    last_sync_screenshot BLOB,
    CONSTRAINT merchant_accounts_last_sync_screenshot_check CHECK ((length(last_sync_screenshot) <= 1048576)),
    CONSTRAINT merchant_accounts_second_factor_check CHECK ((second_factor IN ('', 'email', 'sms', 'totp'))),
    CONSTRAINT merchant_accounts_sign_in_paused_for_check CHECK ((sign_in_paused_for IN ('', 'password_refused', 'code_needed'))),
    CONSTRAINT amazon_accounts_pkey PRIMARY KEY (id),
    CONSTRAINT amazon_accounts_gift_card_account_id_fkey FOREIGN KEY (gift_card_account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT amazon_accounts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE merchant_catalog (
    merchant TEXT NOT NULL,
    sku TEXT NOT NULL,
    status TEXT NOT NULL,
    title TEXT DEFAULT '' NOT NULL,
    brand TEXT DEFAULT '' NOT NULL,
    size TEXT DEFAULT '' NOT NULL,
    category TEXT DEFAULT '' NOT NULL,
    image_url TEXT DEFAULT '' NOT NULL,
    url TEXT DEFAULT '' NOT NULL,
    price INTEGER,
    source TEXT DEFAULT '' NOT NULL,
    source_ref TEXT DEFAULT '' NOT NULL,
    raw TEXT,
    looked_up_at TEXT DEFAULT (now()) NOT NULL CHECK (length(looked_up_at) = 27),
    found_at TEXT CHECK (length(found_at) = 27),
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT merchant_catalog_pkey PRIMARY KEY (merchant, sku)
) STRICT;

CREATE TABLE merchant_charges (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    merchant_account_id TEXT NOT NULL,
    order_number TEXT NOT NULL,
    charged_on TEXT NOT NULL CHECK (length(charged_on) = 10),
    amount INTEGER NOT NULL,
    instrument TEXT DEFAULT '' NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    merchant TEXT DEFAULT 'amazon' NOT NULL,
    CONSTRAINT amazon_charges_pkey PRIMARY KEY (id),
    CONSTRAINT amazon_charges_space_id_amazon_account_id_order_number_char_key UNIQUE (space_id, merchant_account_id, order_number, charged_on, amount),
    CONSTRAINT amazon_charges_amazon_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(id) ON DELETE CASCADE,
    CONSTRAINT amazon_charges_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE merchant_matches (
    transaction_id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    order_id TEXT NOT NULL,
    amount INTEGER NOT NULL,
    basis TEXT NOT NULL,
    confidence REAL DEFAULT 1 NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    refund_id TEXT,
    CONSTRAINT amazon_matches_pkey PRIMARY KEY (transaction_id, order_id),
    CONSTRAINT amazon_matches_order_id_fkey FOREIGN KEY (order_id) REFERENCES merchant_orders(id) ON DELETE CASCADE,
    CONSTRAINT amazon_matches_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT amazon_matches_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE,
    CONSTRAINT merchant_matches_refund_id_fkey FOREIGN KEY (refund_id) REFERENCES merchant_refunds(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE merchant_order_items (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    order_id TEXT NOT NULL,
    "position" INTEGER NOT NULL,
    sku TEXT DEFAULT '' NOT NULL,
    title TEXT NOT NULL,
    quantity INTEGER DEFAULT 1 NOT NULL,
    unit_price INTEGER,
    total_owed INTEGER,
    shipped_on TEXT CHECK (length(shipped_on) = 10),
    condition TEXT DEFAULT '' NOT NULL,
    url TEXT DEFAULT '' NOT NULL,
    CONSTRAINT amazon_order_items_pkey PRIMARY KEY (id),
    CONSTRAINT amazon_order_items_order_id_fkey FOREIGN KEY (order_id) REFERENCES merchant_orders(id) ON DELETE CASCADE,
    CONSTRAINT amazon_order_items_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE merchant_orders (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    merchant_account_id TEXT NOT NULL,
    order_number TEXT NOT NULL,
    ordered_on TEXT NOT NULL CHECK (length(ordered_on) = 10),
    total INTEGER DEFAULT 0 NOT NULL,
    currency TEXT DEFAULT 'USD' NOT NULL,
    status TEXT DEFAULT '' NOT NULL,
    details_url TEXT DEFAULT '' NOT NULL,
    source TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    gift_card_amount INTEGER,
    tax INTEGER,
    shipping INTEGER,
    ignored_at TEXT CHECK (length(ignored_at) = 27),
    merchant TEXT DEFAULT 'amazon' NOT NULL,
    kind TEXT DEFAULT 'online' NOT NULL,
    location TEXT DEFAULT '' NOT NULL,
    invoice_misses INTEGER DEFAULT 0 NOT NULL,
    CONSTRAINT amazon_orders_pkey PRIMARY KEY (id),
    CONSTRAINT amazon_orders_space_id_amazon_account_id_order_number_key UNIQUE (space_id, merchant_account_id, order_number),
    CONSTRAINT amazon_orders_amazon_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(id) ON DELETE CASCADE,
    CONSTRAINT amazon_orders_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE merchant_refunds (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    merchant TEXT NOT NULL,
    merchant_account_id TEXT NOT NULL,
    order_number TEXT NOT NULL,
    sku TEXT DEFAULT '' NOT NULL,
    title TEXT DEFAULT '' NOT NULL,
    quantity INTEGER DEFAULT 1 NOT NULL,
    refunded_on TEXT NOT NULL CHECK (length(refunded_on) = 10),
    amount INTEGER NOT NULL,
    instrument TEXT DEFAULT '' NOT NULL,
    destination TEXT DEFAULT 'card' NOT NULL,
    status TEXT DEFAULT '' NOT NULL,
    source TEXT DEFAULT '' NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT merchant_refunds_pkey PRIMARY KEY (id),
    CONSTRAINT merchant_refunds_space_id_merchant_account_id_order_number__key UNIQUE (space_id, merchant_account_id, order_number, sku, refunded_on, amount),
    CONSTRAINT merchant_refunds_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(id) ON DELETE CASCADE,
    CONSTRAINT merchant_refunds_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE notifications (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    alert_type TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    url TEXT,
    data TEXT,
    dedupe_key TEXT NOT NULL,
    read_at TEXT CHECK (length(read_at) = 27),
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    condition_key TEXT,
    resolved_at TEXT CHECK (length(resolved_at) = 27),
    cleared_at TEXT CHECK (length(cleared_at) = 27),
    CONSTRAINT notifications_pkey PRIMARY KEY (id),
    CONSTRAINT uq_notification_dedupe UNIQUE (user_id, dedupe_key),
    CONSTRAINT notifications_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT notifications_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE passkeys (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    credential_id BLOB NOT NULL,
    public_key BLOB NOT NULL,
    sign_count INTEGER NOT NULL,
    name TEXT NOT NULL,
    transports TEXT,
    rp_id TEXT,
    is_discoverable INTEGER DEFAULT 0 NOT NULL,
    last_used_at TEXT CHECK (length(last_used_at) = 27),
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT passkeys_pkey PRIMARY KEY (id),
    CONSTRAINT uq_passkey_credential_id UNIQUE (credential_id),
    CONSTRAINT passkeys_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE push_subscriptions (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    user_agent TEXT DEFAULT '' NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    last_used_at TEXT CHECK (length(last_used_at) = 27),
    CONSTRAINT push_subscriptions_pkey PRIMARY KEY (id),
    CONSTRAINT uq_push_subscription_endpoint UNIQUE (user_id, endpoint),
    CONSTRAINT push_subscriptions_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT push_subscriptions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE recovery_codes (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    code_hash TEXT NOT NULL,
    used_at TEXT CHECK (length(used_at) = 27),
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT recovery_codes_pkey PRIMARY KEY (id),
    CONSTRAINT recovery_codes_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE revoked_tokens (
    jti TEXT NOT NULL,
    expires_at TEXT NOT NULL CHECK (length(expires_at) = 27),
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    CONSTRAINT revoked_tokens_pkey PRIMARY KEY (jti)
) STRICT;

CREATE TABLE rules (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    filter_id TEXT NOT NULL,
    priority INTEGER NOT NULL,
    is_active INTEGER DEFAULT 1 NOT NULL,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    set_payee TEXT,
    set_category_id TEXT,
    add_tag_ids TEXT DEFAULT '[]' NOT NULL,
    set_notes TEXT,
    set_excluded_from_reports INTEGER,
    set_excluded_from_spending_plan INTEGER,
    set_is_reviewed INTEGER,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    source_ref TEXT DEFAULT '' NOT NULL,
    CONSTRAINT rules_pkey PRIMARY KEY (id),
    CONSTRAINT rules_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT,
    CONSTRAINT rules_set_category_id_fkey FOREIGN KEY (set_category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT rules_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
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
    last_price TEXT,
    last_price_at TEXT CHECK (length(last_price_at) = 27),
    prior_close TEXT,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT securities_pkey PRIMARY KEY (id),
    CONSTRAINT uq_security_space_symbol UNIQUE (space_id, symbol),
    CONSTRAINT securities_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE security_prices (
    id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    security_id TEXT NOT NULL,
    on_date TEXT NOT NULL CHECK (length(on_date) = 10),
    close TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    CONSTRAINT security_prices_pkey PRIMARY KEY (id),
    CONSTRAINT uq_security_price_day UNIQUE (space_id, security_id, on_date),
    CONSTRAINT security_prices_security_id_fkey FOREIGN KEY (security_id) REFERENCES securities(id) ON DELETE CASCADE,
    CONSTRAINT security_prices_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
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
    by_month_day TEXT DEFAULT '[]' NOT NULL,
    by_day TEXT DEFAULT '[]' NOT NULL,
    start_on TEXT NOT NULL CHECK (length(start_on) = 10),
    end_on TEXT CHECK (length(end_on) = 10),
    next_due_on TEXT CHECK (length(next_due_on) = 10),
    override_next_due_on TEXT CHECK (length(override_next_due_on) = 10),
    override_next_amount INTEGER,
    auto_adjust_due_on INTEGER DEFAULT 0 NOT NULL,
    reminder_days INTEGER NOT NULL,
    auto_accept_days INTEGER,
    match_criteria TEXT NOT NULL,
    match_amount_min INTEGER,
    match_amount_max INTEGER,
    learned_descriptions TEXT DEFAULT '[]' NOT NULL,
    template_payee TEXT,
    template_notes TEXT,
    template_tag_ids TEXT DEFAULT '[]' NOT NULL,
    template_excluded_from_reports INTEGER DEFAULT 0 NOT NULL,
    template_excluded_from_spending_plan INTEGER DEFAULT 0 NOT NULL,
    template_splits TEXT,
    is_active INTEGER DEFAULT 1 NOT NULL,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    by_month TEXT DEFAULT '[]' NOT NULL,
    CONSTRAINT series_pkey PRIMARY KEY (id),
    CONSTRAINT series_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT series_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT series_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE series_bill_links (
    series_id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    subaccount_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    CONSTRAINT series_bill_links_pkey PRIMARY KEY (series_id),
    CONSTRAINT uq_series_bill_link_subaccount UNIQUE (subaccount_id),
    CONSTRAINT series_bill_links_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE,
    CONSTRAINT series_bill_links_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT series_bill_links_subaccount_id_fkey FOREIGN KEY (subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE server_settings (
    key TEXT NOT NULL,
    value_encrypted TEXT NOT NULL,
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT server_settings_pkey PRIMARY KEY (key)
) STRICT;

CREATE TABLE spaces (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    primary_currency TEXT NOT NULL,
    timezone TEXT NOT NULL,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    default_date_range TEXT,
    sidebar_account_types TEXT,
    setup_guide_dismissed_at TEXT CHECK (length(setup_guide_dismissed_at) = 27),
    setup_guide_skipped TEXT DEFAULT '[]' NOT NULL,
    CONSTRAINT spaces_pkey PRIMARY KEY (id)
) STRICT;

CREATE TABLE spending_plan_months (
    id TEXT NOT NULL,
    month TEXT NOT NULL CHECK (length(month) = 10),
    calculated_rollover_amount INTEGER DEFAULT 0 NOT NULL,
    calculated_income_amount INTEGER DEFAULT 0 NOT NULL,
    income_txn_ids TEXT DEFAULT '[]' NOT NULL,
    excluded_income_txn_ids TEXT DEFAULT '[]' NOT NULL,
    overwritten_income_amount INTEGER,
    reset_overwritten_income INTEGER DEFAULT 0 NOT NULL,
    calculated_bills_amount INTEGER DEFAULT 0 NOT NULL,
    bills_txn_ids TEXT DEFAULT '[]' NOT NULL,
    excluded_bills_txn_ids TEXT DEFAULT '[]' NOT NULL,
    overwritten_bills_amount INTEGER,
    reset_overwritten_bills INTEGER DEFAULT 0 NOT NULL,
    calculated_subscriptions_amount INTEGER DEFAULT 0 NOT NULL,
    subscriptions_txn_ids TEXT DEFAULT '[]' NOT NULL,
    excluded_subscriptions_txn_ids TEXT DEFAULT '[]' NOT NULL,
    overwritten_subscriptions_amount INTEGER,
    reset_overwritten_subscriptions INTEGER DEFAULT 0 NOT NULL,
    calculated_transfer_amount INTEGER DEFAULT 0 NOT NULL,
    transfer_txn_ids TEXT DEFAULT '[]' NOT NULL,
    excluded_transfer_txn_ids TEXT DEFAULT '[]' NOT NULL,
    overwritten_transfer_amount INTEGER,
    reset_overwritten_transfer INTEGER DEFAULT 0 NOT NULL,
    calculated_goals_amount INTEGER DEFAULT 0 NOT NULL,
    goals_txn_ids TEXT DEFAULT '[]' NOT NULL,
    excluded_goals_txn_ids TEXT DEFAULT '[]' NOT NULL,
    overwritten_goals_amount INTEGER,
    reset_overwritten_goals INTEGER DEFAULT 0 NOT NULL,
    calculated_planned_spending_amount INTEGER DEFAULT 0 NOT NULL,
    planned_spending_txn_ids TEXT DEFAULT '[]' NOT NULL,
    excluded_planned_spending_txn_ids TEXT DEFAULT '[]' NOT NULL,
    overwritten_planned_spending_amount INTEGER,
    reset_overwritten_planned_spending INTEGER DEFAULT 0 NOT NULL,
    calculated_spent_amount INTEGER DEFAULT 0 NOT NULL,
    spent_txn_ids TEXT DEFAULT '[]' NOT NULL,
    excluded_spent_txn_ids TEXT DEFAULT '[]' NOT NULL,
    overwritten_spent_amount INTEGER,
    reset_overwritten_spent INTEGER DEFAULT 0 NOT NULL,
    set_aside INTEGER DEFAULT 0 NOT NULL,
    total_to_spend_amount INTEGER DEFAULT 0 NOT NULL,
    left_to_spend_amount INTEGER DEFAULT 0 NOT NULL,
    projected_other_spending INTEGER DEFAULT 0 NOT NULL,
    projection_type TEXT NOT NULL,
    projection_start_date TEXT CHECK (length(projection_start_date) = 10),
    projection_end_date TEXT CHECK (length(projection_end_date) = 10),
    projection_buffer INTEGER DEFAULT 0 NOT NULL,
    projection_window_months INTEGER NOT NULL,
    is_closed_out INTEGER DEFAULT 0 NOT NULL,
    closed_out_at TEXT CHECK (length(closed_out_at) = 27),
    show_closed_out INTEGER DEFAULT 0 NOT NULL,
    new_month_viewed INTEGER DEFAULT 0 NOT NULL,
    hide_excluded_from_ui INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT spending_plan_months_pkey PRIMARY KEY (id),
    CONSTRAINT uq_spending_plan_space_month UNIQUE (space_id, month),
    CONSTRAINT spending_plan_months_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
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
    space_id TEXT NOT NULL,
    signature TEXT NOT NULL,
    dismissed_at TEXT DEFAULT (now()) NOT NULL CHECK (length(dismissed_at) = 27),
    CONSTRAINT suggestion_dismissals_pkey PRIMARY KEY (id),
    CONSTRAINT uq_suggestion_dismissal UNIQUE (space_id, signature),
    CONSTRAINT suggestion_dismissals_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE tags (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    color TEXT,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT tags_pkey PRIMARY KEY (id),
    CONSTRAINT uq_tag_space_name UNIQUE (space_id, name),
    CONSTRAINT tags_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE transaction_refund_links (
    space_id TEXT NOT NULL,
    refund_txn_id TEXT NOT NULL,
    charge_txn_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    CONSTRAINT transaction_refund_links_distinct CHECK ((refund_txn_id <> charge_txn_id)),
    CONSTRAINT transaction_refund_links_pkey PRIMARY KEY (space_id, refund_txn_id, charge_txn_id),
    CONSTRAINT transaction_refund_links_charge_txn_id_fkey FOREIGN KEY (charge_txn_id) REFERENCES transactions(id) ON DELETE CASCADE,
    CONSTRAINT transaction_refund_links_refund_txn_id_fkey FOREIGN KEY (refund_txn_id) REFERENCES transactions(id) ON DELETE CASCADE,
    CONSTRAINT transaction_refund_links_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE transaction_splits (
    id TEXT NOT NULL,
    transaction_id TEXT NOT NULL,
    "position" INTEGER NOT NULL,
    amount INTEGER NOT NULL,
    category_id TEXT,
    memo TEXT,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT transaction_splits_pkey PRIMARY KEY (id),
    CONSTRAINT transaction_splits_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT transaction_splits_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
    CONSTRAINT transaction_splits_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE transaction_tags (
    transaction_id TEXT NOT NULL,
    tag_id TEXT NOT NULL,
    CONSTRAINT transaction_tags_pkey PRIMARY KEY (transaction_id, tag_id),
    CONSTRAINT transaction_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE,
    CONSTRAINT transaction_tags_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE
) STRICT;

-- transactions.memo: The provider's memo line, verbatim.
-- transactions.transacted_on: When the purchase happened, if the feed distinguishes it from the posting date.
-- transactions.provider_extra: Unmapped provider payload: SimpleFIN's extra object and any non-spec keys.
-- transactions.category_checked_at: When a category check last finished on this row without filing or proposing a category.
-- transactions.category_check_note: That run's closing line, shown as the reason beside "Undetermined".
-- transactions.category_check_run_id: The assistant run that last decided this row's category, for the register's "why".
CREATE TABLE transactions (
    id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    external_id TEXT,
    date TEXT NOT NULL CHECK (length(date) = 10),
    effective_date TEXT CHECK (length(effective_date) = 10),
    amount INTEGER NOT NULL,
    currency TEXT NOT NULL,
    amount_primary INTEGER,
    fx_rate_used TEXT,
    statement_name TEXT DEFAULT '' NOT NULL,
    payee TEXT DEFAULT '' NOT NULL,
    notes TEXT,
    check_number TEXT,
    category_id TEXT,
    source TEXT NOT NULL,
    is_pending INTEGER DEFAULT 0 NOT NULL,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    is_reviewed INTEGER DEFAULT 0 NOT NULL,
    excluded_from_reports INTEGER DEFAULT 0 NOT NULL,
    excluded_from_spending_plan INTEGER DEFAULT 0 NOT NULL,
    is_bill INTEGER DEFAULT 0 NOT NULL,
    is_subscription INTEGER DEFAULT 0 NOT NULL,
    transfer_pair_id TEXT,
    user_flag TEXT,
    user_flag_note TEXT,
    series_id TEXT,
    series_due_on TEXT CHECK (length(series_due_on) = 10),
    estimate_status TEXT,
    accepted_on TEXT CHECK (length(accepted_on) = 10),
    expires_on TEXT CHECK (length(expires_on) = 10),
    rule_id TEXT,
    balance INTEGER,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    needs_settle INTEGER DEFAULT 0 NOT NULL,
    memo TEXT DEFAULT '' NOT NULL,
    transacted_on TEXT CHECK (length(transacted_on) = 10),
    provider_extra TEXT,
    category_checked_at TEXT CHECK (length(category_checked_at) = 27),
    category_check_note TEXT DEFAULT '' NOT NULL,
    category_check_run_id TEXT,
    category_from_pair INTEGER DEFAULT 0 NOT NULL,
    padded_txn_id TEXT,
    receipt_not_needed INTEGER DEFAULT 0 NOT NULL,
    CONSTRAINT transactions_check CHECK ((padded_txn_id <> id)),
    CONSTRAINT transactions_pkey PRIMARY KEY (id),
    CONSTRAINT uq_transaction_account_external UNIQUE (account_id, external_id),
    CONSTRAINT transactions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE RESTRICT,
    CONSTRAINT transactions_category_check_run_id_fkey FOREIGN KEY (category_check_run_id) REFERENCES assistant_automation_runs(id) ON DELETE SET NULL,
    CONSTRAINT transactions_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL,
    CONSTRAINT transactions_padded_txn_id_fkey FOREIGN KEY (padded_txn_id) REFERENCES transactions(id) ON DELETE CASCADE,
    CONSTRAINT transactions_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES rules(id) ON DELETE SET NULL,
    CONSTRAINT transactions_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE SET NULL,
    CONSTRAINT transactions_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE users (
    id TEXT NOT NULL,
    email TEXT NOT NULL,
    hashed_password TEXT,
    full_name TEXT,
    oidc_subject TEXT,
    oidc_issuer TEXT,
    totp_secret TEXT,
    is_active INTEGER DEFAULT 1 NOT NULL,
    is_superuser INTEGER DEFAULT 0 NOT NULL,
    is_verified INTEGER DEFAULT 0 NOT NULL,
    locale TEXT NOT NULL,
    theme TEXT NOT NULL,
    privacy_mode INTEGER DEFAULT 0 NOT NULL,
    last_login_at TEXT CHECK (length(last_login_at) = 27),
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    must_change_password INTEGER DEFAULT 0 NOT NULL,
    sessions_valid_from TEXT CHECK (length(sessions_valid_from) = 27),
    swipe_left_action TEXT DEFAULT 'menu' NOT NULL,
    swipe_right_action TEXT DEFAULT 'review' NOT NULL,
    animation_duration_ms INTEGER DEFAULT 160 NOT NULL,
    toast_duration_ms INTEGER DEFAULT 6000 NOT NULL,
    CONSTRAINT uq_user_oidc_identity UNIQUE (oidc_issuer, oidc_subject),
    CONSTRAINT users_pkey PRIMARY KEY (id)
) STRICT;

CREATE TABLE watchlists (
    id TEXT NOT NULL,
    filter_id TEXT NOT NULL,
    name TEXT NOT NULL,
    emoji TEXT,
    target_amount INTEGER,
    period TEXT NOT NULL,
    start_date TEXT CHECK (length(start_date) = 10),
    end_date TEXT CHECK (length(end_date) = 10),
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    space_id TEXT NOT NULL,
    created_at TEXT DEFAULT (now()) NOT NULL CHECK (length(created_at) = 27),
    updated_at TEXT DEFAULT (now()) NOT NULL CHECK (length(updated_at) = 27),
    CONSTRAINT watchlists_pkey PRIMARY KEY (id),
    CONSTRAINT watchlists_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT,
    CONSTRAINT watchlists_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
) STRICT;

CREATE INDEX accounts_secured_by_idx ON accounts (space_id, secured_by_account_id) WHERE (secured_by_account_id IS NOT NULL);
CREATE INDEX bill_payments_space_unmatched ON bill_payments (space_id) WHERE (transaction_id IS NULL);
CREATE UNIQUE INDEX bill_payments_transaction ON bill_payments (transaction_id) WHERE (transaction_id IS NOT NULL);
CREATE UNIQUE INDEX bill_subaccounts_account_id_key ON bill_subaccounts (account_id) WHERE (account_id IS NOT NULL);
CREATE INDEX document_links_target ON document_links (space_id, kind, target_id);
CREATE INDEX documents_space_created ON documents (space_id, created_at);
CREATE INDEX ix_accounts_simplefin ON accounts (space_id, simplefin_account_id);
CREATE INDEX ix_accounts_space_id ON accounts (space_id);
CREATE INDEX ix_alert_rules_space_id ON alert_rules (space_id);
CREATE INDEX ix_assistant_actions_conversation ON assistant_actions (conversation_id, created_at);
CREATE INDEX ix_assistant_automation_runs_batch ON assistant_automation_runs (batch_id) WHERE (batch_id IS NOT NULL);
CREATE INDEX ix_assistant_automation_runs_feed ON assistant_automation_runs (space_id, queued_at DESC);
CREATE INDEX ix_assistant_automation_runs_queue ON assistant_automation_runs (bulk, queued_at, id) WHERE (status = 'queued');
CREATE INDEX ix_assistant_automations_space ON assistant_automations (space_id, created_at);
CREATE INDEX ix_assistant_conversations_feed ON assistant_conversations (space_id, user_id, updated_at);
CREATE INDEX ix_assistant_conversations_run ON assistant_conversations (automation_run_id) WHERE (automation_run_id IS NOT NULL);
CREATE INDEX ix_assistant_corrections_space ON assistant_corrections (space_id, created_at DESC);
CREATE INDEX ix_assistant_guidance_space ON assistant_guidance (space_id, is_deleted, "position");
CREATE INDEX ix_assistant_messages_conversation ON assistant_messages (conversation_id, created_at);
CREATE INDEX ix_backup_runs_started ON backup_runs (started_at DESC);
CREATE INDEX ix_balance_snapshots_space_day ON balance_snapshots (space_id, as_of);
CREATE INDEX ix_balance_snapshots_space_id ON balance_snapshots (space_id);
CREATE INDEX ix_bill_challenges_waiting ON bill_challenges (space_id, connection_id, state);
CREATE INDEX ix_bill_emails_outcome ON bill_emails (space_id, outcome, received_at DESC);
CREATE INDEX ix_bill_emails_recent ON bill_emails (space_id, connection_id, received_at DESC);
CREATE INDEX ix_bills_subaccount_due ON bills (space_id, subaccount_id, due_on);
CREATE INDEX ix_cash_flow_forecasts_account_latest ON cash_flow_forecasts (space_id, account_id, generated_at DESC) WHERE (account_id IS NOT NULL);
CREATE INDEX ix_cash_flow_forecasts_latest ON cash_flow_forecasts (space_id, generated_at DESC);
CREATE INDEX ix_categories_known ON categories (space_id, known_category_id);
CREATE INDEX ix_categories_space_id ON categories (space_id);
CREATE INDEX ix_category_suggestion_batches_space ON category_suggestion_batches (space_id, created_at DESC);
CREATE INDEX ix_connections_space_id ON connections (space_id);
CREATE INDEX ix_email_connections_due ON email_connections (enabled, last_polled_at);
CREATE INDEX ix_envelopes_group ON envelopes (space_id, recurring_group_id);
CREATE INDEX ix_envelopes_month ON envelopes (space_id, spending_plan_month_id);
CREATE INDEX ix_envelopes_space_id ON envelopes (space_id);
CREATE INDEX ix_filter_items_filter ON filter_items (filter_id, group_index, "position");
CREATE INDEX ix_filter_items_space_id ON filter_items (space_id);
CREATE INDEX ix_filters_space_id ON filters (space_id);
CREATE INDEX ix_fx_rates_space_id ON fx_rates (space_id);
CREATE INDEX ix_fx_rates_space_quote_date ON fx_rates (space_id, quote_currency, date);
CREATE INDEX ix_goals_space_id ON goals (space_id);
CREATE INDEX ix_holdings_space_account ON holdings (space_id, account_id);
CREATE INDEX ix_holdings_space_id ON holdings (space_id);
CREATE INDEX ix_ignored_remote_accounts_space_id ON ignored_remote_accounts (space_id);
CREATE INDEX ix_institutions_space_id ON institutions (space_id);
CREATE INDEX ix_mail_rules_order ON mail_rules (space_id, sort_order, name);
CREATE INDEX ix_memberships_space_id ON memberships (space_id);
CREATE INDEX ix_notifications_feed ON notifications (space_id, user_id, created_at);
CREATE INDEX ix_notifications_space_id ON notifications (space_id);
CREATE INDEX ix_passkeys_user ON passkeys (user_id);
CREATE INDEX ix_push_subscriptions_user ON push_subscriptions (space_id, user_id);
CREATE INDEX ix_recovery_codes_user ON recovery_codes (user_id);
CREATE INDEX ix_revoked_tokens_expires ON revoked_tokens (expires_at);
CREATE INDEX ix_rules_space_id ON rules (space_id);
CREATE INDEX ix_securities_space_id ON securities (space_id);
CREATE INDEX ix_security_prices_series ON security_prices (space_id, security_id, on_date);
CREATE INDEX ix_series_due ON series (space_id, next_due_on);
CREATE INDEX ix_series_space_id ON series (space_id);
CREATE INDEX ix_spending_plan_months_space_id ON spending_plan_months (space_id);
CREATE INDEX ix_splits_category ON transaction_splits (space_id, category_id);
CREATE INDEX ix_suggestion_dismissals_space_id ON suggestion_dismissals (space_id);
CREATE INDEX ix_tags_space_id ON tags (space_id);
CREATE INDEX ix_transaction_refund_links_charge ON transaction_refund_links (space_id, charge_txn_id);
CREATE INDEX ix_transaction_splits_space_id ON transaction_splits (space_id);
CREATE INDEX ix_transactions_category ON transactions (space_id, category_id);
CREATE INDEX ix_transactions_effective ON transactions (space_id, effective_date);
CREATE INDEX ix_transactions_register ON transactions (space_id, account_id, date);
CREATE INDEX ix_transactions_relink_dedupe ON transactions (account_id, date, amount);
CREATE INDEX ix_transactions_series_slot ON transactions (space_id, series_id, series_due_on);
CREATE INDEX ix_transactions_transfer_pair ON transactions (space_id, transfer_pair_id);
CREATE INDEX ix_watchlists_space_id ON watchlists (space_id);
CREATE UNIQUE INDEX merchant_accounts_space_merchant_label_key ON merchant_accounts (space_id, merchant, label) WHERE (label <> '');
CREATE INDEX merchant_catalog_missing ON merchant_catalog (merchant, looked_up_at) WHERE (status = 'missing');
CREATE INDEX merchant_charges_space_merchant_date ON merchant_charges (space_id, merchant, charged_on);
CREATE INDEX merchant_matches_order ON merchant_matches (order_id);
CREATE INDEX merchant_order_items_order ON merchant_order_items (order_id);
CREATE INDEX merchant_orders_space_date ON merchant_orders (space_id, ordered_on);
CREATE INDEX merchant_orders_space_merchant_date ON merchant_orders (space_id, merchant, ordered_on);
CREATE INDEX merchant_refunds_order ON merchant_refunds (space_id, order_number);
CREATE INDEX merchant_refunds_space_merchant_date ON merchant_refunds (space_id, merchant, refunded_on);
CREATE INDEX transactions_needs_settle_idx ON transactions (space_id, created_at) WHERE needs_settle;
CREATE UNIQUE INDEX uq_alert_rule ON alert_rules (COALESCE(space_id, ''), COALESCE(user_id, ''), COALESCE(alert_type, ''), COALESCE(account_id, ''));
CREATE UNIQUE INDEX uq_assistant_automation_runs_transaction ON assistant_automation_runs (automation_id, transaction_id) WHERE (fired_by = 'transaction');
CREATE UNIQUE INDEX uq_notification_open_condition ON notifications (user_id, condition_key) WHERE ((condition_key IS NOT NULL) AND (resolved_at IS NULL));
CREATE UNIQUE INDEX uq_rules_source_ref ON rules (space_id, source_ref) WHERE (source_ref <> '');
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
CREATE UNIQUE INDEX ux_transactions_padded ON transactions (padded_txn_id) WHERE (padded_txn_id IS NOT NULL);

CREATE TRIGGER transactions_category_from_pair
AFTER UPDATE OF category_id ON transactions
FOR EACH ROW
WHEN NEW.category_id IS NOT OLD.category_id
 AND NEW.category_from_pair = OLD.category_from_pair
 AND NEW.category_from_pair = 1
BEGIN
    UPDATE transactions SET category_from_pair = 0 WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS transactions_category_from_pair;
DROP TABLE IF EXISTS watchlists;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS transaction_tags;
DROP TABLE IF EXISTS transaction_splits;
DROP TABLE IF EXISTS transaction_refund_links;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS suggestion_dismissals;
DROP TABLE IF EXISTS split_tags;
DROP TABLE IF EXISTS spending_plan_months;
DROP TABLE IF EXISTS spaces;
DROP TABLE IF EXISTS server_settings;
DROP TABLE IF EXISTS series_bill_links;
DROP TABLE IF EXISTS series;
DROP TABLE IF EXISTS security_prices;
DROP TABLE IF EXISTS securities;
DROP TABLE IF EXISTS rules;
DROP TABLE IF EXISTS revoked_tokens;
DROP TABLE IF EXISTS recovery_codes;
DROP TABLE IF EXISTS push_subscriptions;
DROP TABLE IF EXISTS passkeys;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS merchant_refunds;
DROP TABLE IF EXISTS merchant_orders;
DROP TABLE IF EXISTS merchant_order_items;
DROP TABLE IF EXISTS merchant_matches;
DROP TABLE IF EXISTS merchant_charges;
DROP TABLE IF EXISTS merchant_catalog;
DROP TABLE IF EXISTS merchant_accounts;
DROP TABLE IF EXISTS memberships;
DROP TABLE IF EXISTS manual_transfer_pairs;
DROP TABLE IF EXISTS mail_rules;
DROP TABLE IF EXISTS institutions;
DROP TABLE IF EXISTS ignored_remote_accounts;
DROP TABLE IF EXISTS holdings;
DROP TABLE IF EXISTS goals;
DROP TABLE IF EXISTS goal_funding_accounts;
DROP TABLE IF EXISTS fx_rates;
DROP TABLE IF EXISTS filters;
DROP TABLE IF EXISTS filter_items;
DROP TABLE IF EXISTS envelopes;
DROP TABLE IF EXISTS email_connections;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS document_links;
DROP TABLE IF EXISTS connections;
DROP TABLE IF EXISTS category_suggestion_batches;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS cash_flow_forecasts;
DROP TABLE IF EXISTS bills;
DROP TABLE IF EXISTS bill_subaccounts;
DROP TABLE IF EXISTS bill_payments;
DROP TABLE IF EXISTS bill_emails;
DROP TABLE IF EXISTS bill_connections;
DROP TABLE IF EXISTS bill_challenges;
DROP TABLE IF EXISTS balance_snapshots;
DROP TABLE IF EXISTS backup_runs;
DROP TABLE IF EXISTS assistant_messages;
DROP TABLE IF EXISTS assistant_guidance;
DROP TABLE IF EXISTS assistant_corrections;
DROP TABLE IF EXISTS assistant_conversations;
DROP TABLE IF EXISTS assistant_connections;
DROP TABLE IF EXISTS assistant_automations;
DROP TABLE IF EXISTS assistant_automation_runs;
DROP TABLE IF EXISTS assistant_actions;
DROP TABLE IF EXISTS alert_rules;
DROP TABLE IF EXISTS accounts;
-- +goose StatementEnd
