-- The Agentifi schema: every table with its constraints, indexes and column
-- comments, and the trigger that clears transactions.category_from_pair when
-- a row's category is changed by anything other than transfer pairing. A new
-- database applies this file first; each later change to the schema is a
-- migration numbered after it.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION transactions_category_from_pair() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.category_id IS DISTINCT FROM OLD.category_id
       AND NEW.category_from_pair = OLD.category_from_pair THEN
        NEW.category_from_pair := false;
    END IF;
    RETURN NEW;
END
$$;

CREATE TABLE accounts (
    id uuid NOT NULL,
    connection_id uuid,
    institution_id uuid,
    external_id character varying(255),
    name character varying(255) NOT NULL,
    description character varying(500),
    notes text,
    kind character varying(32) NOT NULL,
    type character varying(48) NOT NULL,
    usage_type character varying(32),
    currency character varying(3) NOT NULL,
    masked_number character varying(8),
    logo_url character varying(500),
    sort_order integer NOT NULL,
    provider_balance numeric(15,2),
    provider_balance_at timestamp with time zone,
    opening_balance numeric(15,2) DEFAULT 0 NOT NULL,
    opening_balance_on date,
    goal_balance numeric(15,2) DEFAULT 0 NOT NULL,
    pending_holds numeric(15,2) DEFAULT 0 NOT NULL,
    credit_limit numeric(15,2),
    statement_balance numeric(15,2),
    minimum_due numeric(15,2),
    due_date date,
    interest_rate numeric(20,10),
    statement_close_day smallint,
    excluded_from_reports boolean DEFAULT false NOT NULL,
    excluded_from_spending_plan boolean DEFAULT false NOT NULL,
    excluded_from_account_bar boolean DEFAULT false NOT NULL,
    include_in_net_worth boolean DEFAULT true NOT NULL,
    exclude_bank_pending boolean DEFAULT false NOT NULL,
    is_closed boolean DEFAULT false NOT NULL,
    closed_on date,
    is_deleted boolean DEFAULT false NOT NULL,
    simplefin_account_id character varying(255),
    sync_floor_on date,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    custom_logo_url character varying(500),
    property_address character varying(500),
    vehicle_vin character varying(32),
    vehicle_mileage integer,
    vehicle_mileage_as_of date,
    vehicle_miles_per_year integer,
    valuation_source character varying(32),
    valued_at timestamp with time zone,
    secured_by_account_id uuid,
    provider_extra jsonb,
    withheld_balance numeric(15,2),
    withheld_balance_at timestamp with time zone,
    accept_zero_balance boolean DEFAULT false NOT NULL,
    statement_bill_id uuid,
    synced_through_on date,
    history_starts_on date,
    history_rebuilt_from date,
    hide_below_balance numeric(15,2),
    ignored_at timestamp with time zone,
    default_register_tab text,
    withheld_balance_reason text DEFAULT ''::text NOT NULL,
    requires_receipts boolean,
    CONSTRAINT accounts_default_register_tab_check CHECK ((default_register_tab = ANY (ARRAY['all'::text, 'spending'::text, 'income'::text])))
);

COMMENT ON COLUMN accounts.provider_extra IS 'Unmapped provider payload: SimpleFIN''s extra object and any non-spec keys.';

CREATE TABLE alert_rules (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    alert_type character varying(64) NOT NULL,
    account_id uuid,
    is_enabled boolean DEFAULT true NOT NULL,
    is_paused boolean DEFAULT false NOT NULL,
    channel_email boolean DEFAULT false NOT NULL,
    channel_push boolean DEFAULT false NOT NULL,
    channel_in_app boolean DEFAULT true NOT NULL,
    threshold_amount numeric(15,2),
    threshold_count integer,
    threshold_pct numeric(15,2),
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE assistant_actions (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    tool_name character varying(64) NOT NULL,
    summary text DEFAULT ''::text NOT NULL,
    method character varying(10) NOT NULL,
    path character varying(500) NOT NULL,
    body jsonb,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    result text DEFAULT ''::text NOT NULL,
    status_code integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    decided_at timestamp with time zone,
    proposed_body jsonb,
    preview jsonb,
    group_id uuid,
    resource_id uuid,
    decline_reason text DEFAULT ''::text NOT NULL
);

CREATE TABLE assistant_automation_runs (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    automation_id uuid NOT NULL,
    fired_by character varying(16) NOT NULL,
    transaction_id uuid,
    conversation_id uuid,
    status character varying(16) DEFAULT 'queued'::character varying NOT NULL,
    subject text DEFAULT ''::text NOT NULL,
    prompt text DEFAULT ''::text NOT NULL,
    output text DEFAULT ''::text NOT NULL,
    error text DEFAULT ''::text NOT NULL,
    tool_calls integer DEFAULT 0 NOT NULL,
    actions integer DEFAULT 0 NOT NULL,
    queued_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    dry_run boolean DEFAULT false NOT NULL,
    confidence double precision,
    decided_by character varying(16) DEFAULT ''::character varying NOT NULL,
    blind boolean DEFAULT false NOT NULL,
    expected_category_id uuid,
    error_code text DEFAULT ''::text NOT NULL,
    batch_id uuid,
    reviewed_when_queued boolean DEFAULT false NOT NULL,
    category_result character varying(16) DEFAULT ''::character varying NOT NULL,
    bulk boolean DEFAULT false NOT NULL
);

CREATE TABLE assistant_automations (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    created_by uuid NOT NULL,
    name character varying(120) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    is_enabled boolean DEFAULT true NOT NULL,
    trigger character varying(32) NOT NULL,
    trigger_config jsonb DEFAULT '{}'::jsonb NOT NULL,
    prompt text NOT NULL,
    context jsonb DEFAULT '{}'::jsonb NOT NULL,
    mode character varying(16) DEFAULT 'propose'::character varying NOT NULL,
    tools jsonb DEFAULT '[]'::jsonb NOT NULL,
    model character varying(120) DEFAULT ''::character varying NOT NULL,
    max_tool_rounds integer DEFAULT 6 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    confidence_threshold double precision DEFAULT 0 NOT NULL,
    template_key character varying(64) DEFAULT ''::character varying NOT NULL,
    filter_id uuid,
    prompt_from_template boolean DEFAULT false NOT NULL,
    description_from_template boolean DEFAULT false NOT NULL
);

CREATE TABLE assistant_connections (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    name character varying(120) DEFAULT ''::character varying NOT NULL,
    base_url character varying(500) NOT NULL,
    model character varying(120) NOT NULL,
    api_key_encrypted text DEFAULT ''::text NOT NULL,
    is_enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    allow_writes boolean DEFAULT false NOT NULL,
    apply_without_asking boolean DEFAULT false NOT NULL,
    tool_call_style character varying(16) DEFAULT 'native'::character varying NOT NULL
);

CREATE TABLE assistant_conversations (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    user_id uuid NOT NULL,
    title character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    automation_run_id uuid,
    mail_id uuid
);

CREATE TABLE assistant_corrections (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    action_id uuid NOT NULL,
    transaction_id uuid,
    statement_name text DEFAULT ''::text NOT NULL,
    payee text DEFAULT ''::text NOT NULL,
    amount numeric(15,2),
    tool_name character varying(64) NOT NULL,
    memo text DEFAULT ''::text NOT NULL,
    proposed_category_id uuid,
    chosen_category_id uuid,
    corrected_by uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE assistant_guidance (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    name character varying(255) NOT NULL,
    instruction text DEFAULT ''::text NOT NULL,
    filter_id uuid,
    is_active boolean DEFAULT true NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE assistant_messages (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    role character varying(16) NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    tool_name character varying(64) DEFAULT ''::character varying NOT NULL,
    tool_arguments jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE backup_runs (
    id uuid NOT NULL,
    trigger text NOT NULL,
    status text NOT NULL,
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    finished_at timestamp with time zone,
    set_name text,
    encrypted boolean DEFAULT false NOT NULL,
    bytes bigint,
    error text,
    CONSTRAINT backup_runs_status_check CHECK ((status = ANY (ARRAY['running'::text, 'succeeded'::text, 'failed'::text, 'skipped'::text]))),
    CONSTRAINT backup_runs_trigger_check CHECK ((trigger = ANY (ARRAY['nightly'::text, 'manual'::text])))
);

CREATE TABLE balance_snapshots (
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    as_of date NOT NULL,
    balance numeric(15,2) DEFAULT 0 NOT NULL,
    balance_primary numeric(15,2),
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    is_imported boolean DEFAULT false NOT NULL,
    anchor_on date,
    anchor_balance numeric(15,2)
);

CREATE TABLE bill_challenges (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    agent_session character varying(64) NOT NULL,
    method character varying(16) NOT NULL,
    prompt text DEFAULT ''::text NOT NULL,
    image text,
    state character varying(16) NOT NULL,
    answered_by character varying(16),
    raised_by character varying(16) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    answered_at timestamp with time zone
);

CREATE TABLE bill_connections (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    biller character varying(32) NOT NULL,
    label character varying(120) NOT NULL,
    username character varying(255) DEFAULT ''::character varying NOT NULL,
    credential_source character varying(16) DEFAULT 'session'::character varying NOT NULL,
    session_state text,
    profile_id character varying(64),
    signed_in_at timestamp with time zone,
    needs_sign_in boolean DEFAULT false NOT NULL,
    autopay_rule character varying(16) DEFAULT 'none'::character varying NOT NULL,
    autopay_days smallint,
    autopay_day smallint,
    autopay_account_id uuid,
    pull_enabled boolean DEFAULT true NOT NULL,
    pull_at character varying(5),
    last_pulled_at timestamp with time zone,
    last_pull_status character varying(16) DEFAULT ''::character varying NOT NULL,
    last_pull_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    last_keepalive_at timestamp with time zone,
    credential_sealed text,
    credential_has_totp boolean DEFAULT false NOT NULL,
    site text,
    sign_in_paused_at timestamp with time zone,
    sign_in_paused_for text DEFAULT ''::text NOT NULL,
    second_factor text DEFAULT ''::text NOT NULL,
    last_pull_screenshot bytea,
    last_pull_trail jsonb,
    CONSTRAINT bill_connections_last_pull_screenshot_check CHECK ((octet_length(last_pull_screenshot) <= 1048576)),
    CONSTRAINT bill_connections_second_factor_check CHECK ((second_factor = ANY (ARRAY[''::text, 'email'::text, 'sms'::text, 'totp'::text]))),
    CONSTRAINT bill_connections_sign_in_paused_for_check CHECK ((sign_in_paused_for = ANY (ARRAY[''::text, 'password_refused'::text, 'code_needed'::text])))
);

CREATE TABLE bill_emails (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    message_id character varying(500) NOT NULL,
    received_at timestamp with time zone NOT NULL,
    sender character varying(255) NOT NULL,
    subject character varying(500) DEFAULT ''::character varying NOT NULL,
    biller character varying(32) DEFAULT ''::character varying NOT NULL,
    outcome character varying(16) NOT NULL,
    note text DEFAULT ''::text NOT NULL,
    bill_id uuid,
    document_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    rule_id uuid,
    transaction_id uuid
);

CREATE TABLE bill_payments (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    subaccount_id uuid NOT NULL,
    external_id character varying(200) NOT NULL,
    paid_on date NOT NULL,
    amount numeric(15,2) NOT NULL,
    method text DEFAULT ''::text NOT NULL,
    transaction_id uuid,
    fetched_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT bill_payments_amount_positive CHECK ((amount > (0)::numeric))
);

CREATE TABLE bill_subaccounts (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    external_id character varying(120) NOT NULL,
    label character varying(160) NOT NULL,
    masked_number character varying(8),
    is_selected boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    account_id uuid
);

CREATE TABLE bills (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    subaccount_id uuid NOT NULL,
    due_on date NOT NULL,
    amount_due numeric(15,2) NOT NULL,
    currency character varying(3) NOT NULL,
    issued_on date,
    period_start date,
    period_end date,
    autopay_on date,
    status character varying(16) DEFAULT 'open'::character varying NOT NULL,
    source character varying(16) NOT NULL,
    external_id character varying(200) DEFAULT ''::character varying NOT NULL,
    statement_url text DEFAULT ''::text NOT NULL,
    raw jsonb,
    fetched_at timestamp with time zone NOT NULL,
    amended_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    minimum_due numeric(15,2),
    invoice text DEFAULT ''::text NOT NULL,
    marked_paid_at timestamp with time zone
);

CREATE TABLE cash_flow_forecasts (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    automation_id uuid,
    automation_run_id uuid,
    model text DEFAULT ''::text NOT NULL,
    generated_at timestamp with time zone DEFAULT now() NOT NULL,
    generated_on date NOT NULL,
    periods jsonb NOT NULL,
    narrative text DEFAULT ''::text NOT NULL,
    raw_answer text DEFAULT ''::text NOT NULL,
    account_id uuid,
    method text DEFAULT 'model'::text NOT NULL,
    CONSTRAINT cash_flow_forecasts_method_check CHECK ((method = ANY (ARRAY['model'::text, 'average'::text])))
);

CREATE TABLE categories (
    id uuid NOT NULL,
    parent_id uuid,
    name character varying(255) NOT NULL,
    kind character varying(16) NOT NULL,
    known_category_id character varying(64),
    txf_id character varying(32),
    txf_ids character varying[] DEFAULT '{}'::character varying[] NOT NULL,
    is_user_assignable boolean DEFAULT true NOT NULL,
    is_editable boolean DEFAULT true NOT NULL,
    excluded_from_reports boolean DEFAULT false NOT NULL,
    excluded_from_spending_plan boolean DEFAULT false NOT NULL,
    excluded_from_category_list boolean DEFAULT false NOT NULL,
    sort_order integer NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE category_suggestion_batches (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    created_by uuid NOT NULL,
    row_count integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    cancelled_at timestamp with time zone,
    dismissed_at timestamp with time zone,
    CONSTRAINT category_suggestion_batches_row_count_check CHECK ((row_count >= 0))
);

CREATE TABLE connections (
    id uuid NOT NULL,
    name character varying(255),
    access_url_encrypted text NOT NULL,
    status character varying(32) NOT NULL,
    status_detail text,
    sync_errors jsonb,
    last_sync_at timestamp with time zone,
    last_successful_sync_at timestamp with time zone,
    retry_not_before timestamp with time zone,
    is_deleted boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE document_links (
    document_id uuid NOT NULL,
    space_id uuid NOT NULL,
    kind character varying(16) NOT NULL,
    target_id uuid NOT NULL,
    role character varying(16) DEFAULT 'attachment'::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE documents (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    content_sha256 character(64) NOT NULL,
    content_type character varying(128) NOT NULL,
    size_bytes integer NOT NULL,
    filename character varying(255) NOT NULL,
    storage_key character varying(1000) NOT NULL,
    source character varying(16) NOT NULL,
    source_ref character varying(500) DEFAULT ''::character varying NOT NULL,
    uploaded_by_user_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE email_connections (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    label character varying(120) NOT NULL,
    kind character varying(16) NOT NULL,
    address character varying(255) NOT NULL,
    client_id character varying(255) DEFAULT ''::character varying NOT NULL,
    tenant character varying(255) DEFAULT ''::character varying NOT NULL,
    host character varying(255) DEFAULT ''::character varying NOT NULL,
    port integer,
    username character varying(255) DEFAULT ''::character varying NOT NULL,
    secret text,
    folder character varying(255) DEFAULT 'Inbox'::character varying NOT NULL,
    cursor jsonb,
    enabled boolean DEFAULT true NOT NULL,
    last_polled_at timestamp with time zone,
    last_poll_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    poll_failures integer DEFAULT 0 NOT NULL,
    poll_failing_since timestamp with time zone
);

CREATE TABLE envelopes (
    id uuid NOT NULL,
    spending_plan_month_id uuid NOT NULL,
    filter_id uuid NOT NULL,
    recurring_group_id uuid,
    name character varying(255) NOT NULL,
    target_amount numeric(15,2) DEFAULT 0 NOT NULL,
    overwritten_target_amount numeric(15,2),
    calculated_spent_amount numeric(15,2) DEFAULT 0 NOT NULL,
    rollover_amount numeric(15,2) DEFAULT 0 NOT NULL,
    auto_release_rollover boolean DEFAULT false NOT NULL,
    recurring boolean DEFAULT true NOT NULL,
    txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    excluded_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    hide_excluded_from_ui boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    rollover_is_carried boolean DEFAULT false NOT NULL
);

CREATE TABLE filter_items (
    id uuid NOT NULL,
    filter_id uuid NOT NULL,
    field character varying(48) NOT NULL,
    operator character varying(24) NOT NULL,
    group_index integer NOT NULL,
    "position" integer NOT NULL,
    negated boolean DEFAULT false NOT NULL,
    value_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    value_texts character varying[] DEFAULT '{}'::character varying[] NOT NULL,
    text character varying(500),
    amount_min numeric(15,2),
    amount_max numeric(15,2),
    date_from date,
    date_to date,
    date_preset character varying(32),
    state boolean,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE filters (
    id uuid NOT NULL,
    name character varying(255),
    scope character varying(32) NOT NULL,
    query_text character varying(1000),
    is_deleted boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    "position" integer DEFAULT 0 NOT NULL
);

CREATE TABLE fx_rates (
    id uuid NOT NULL,
    base_currency character varying(3) NOT NULL,
    quote_currency character varying(3) NOT NULL,
    date date NOT NULL,
    rate numeric(20,10) NOT NULL,
    source character varying(50) NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE goal_funding_accounts (
    goal_id uuid NOT NULL,
    account_id uuid NOT NULL
);

CREATE TABLE goals (
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    name character varying(255) NOT NULL,
    description character varying(500),
    notes text,
    kind character varying(32),
    image_url character varying(500),
    tag_id uuid,
    target_amount numeric(15,2) DEFAULT 0 NOT NULL,
    target_on date,
    start_on date,
    completed_on date,
    contribution_amount numeric(15,2) DEFAULT 0 NOT NULL,
    contribution_frequency character varying(32),
    contributed_this_month numeric(15,2) DEFAULT 0 NOT NULL,
    saved_so_far numeric(15,2) DEFAULT 0 NOT NULL,
    spent numeric(15,2) DEFAULT 0 NOT NULL,
    txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    is_taken_from_plan boolean DEFAULT true NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    emoji character varying(16),
    withdrawal_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    spending_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    closed_on date
);

CREATE TABLE holdings (
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    security_id uuid NOT NULL,
    external_id character varying(255),
    shares numeric(20,10) NOT NULL,
    cost_basis numeric(15,2),
    average_cost numeric(20,10),
    is_cost_basis_complete boolean DEFAULT true NOT NULL,
    market_value numeric(15,2),
    as_of date,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE ignored_remote_accounts (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    external_id character varying(255) NOT NULL,
    name character varying(255) DEFAULT ''::character varying NOT NULL,
    institution character varying(255) DEFAULT ''::character varying NOT NULL,
    masked_number character varying(8) DEFAULT ''::character varying NOT NULL,
    ignored_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE institutions (
    id uuid NOT NULL,
    name character varying(255) NOT NULL,
    external_id character varying(255),
    domain character varying(255),
    logo_url character varying(500),
    is_deleted boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    hide_below_balance numeric(15,2)
);

CREATE TABLE mail_rules (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    name character varying(120) NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    sender character varying(255) DEFAULT ''::character varying NOT NULL,
    subject_contains character varying(255) DEFAULT ''::character varying NOT NULL,
    body_contains character varying(255) DEFAULT ''::character varying NOT NULL,
    amount_label character varying(120) DEFAULT ''::character varying NOT NULL,
    amount_pattern character varying(500) DEFAULT ''::character varying NOT NULL,
    date_label character varying(120) DEFAULT ''::character varying NOT NULL,
    date_pattern character varying(500) DEFAULT ''::character varying NOT NULL,
    reference_label character varying(120) DEFAULT ''::character varying NOT NULL,
    reference_pattern character varying(500) DEFAULT ''::character varying NOT NULL,
    payee character varying(120) DEFAULT ''::character varying NOT NULL,
    payee_label character varying(120) DEFAULT ''::character varying NOT NULL,
    action character varying(16) DEFAULT 'transaction'::character varying NOT NULL,
    account_id uuid,
    category_id uuid,
    direction character varying(16) DEFAULT 'expense'::character varying NOT NULL,
    pad_income boolean DEFAULT false NOT NULL,
    income_account_id uuid,
    income_category_id uuid,
    income_payee character varying(120) DEFAULT ''::character varying NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    bill_connection_id uuid,
    bill_subaccount_id uuid,
    issued_label character varying(120) DEFAULT ''::character varying NOT NULL,
    issued_pattern character varying(500) DEFAULT ''::character varying NOT NULL,
    minimum_label character varying(120) DEFAULT ''::character varying NOT NULL,
    minimum_pattern character varying(500) DEFAULT ''::character varying NOT NULL,
    notes_label character varying(120) DEFAULT ''::character varying NOT NULL,
    notes_end_label character varying(120) DEFAULT ''::character varying NOT NULL
);

CREATE TABLE manual_transfer_pairs (
    transfer_pair_id uuid NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE memberships (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    role character varying(16) NOT NULL,
    invited_at timestamp with time zone,
    accepted_at timestamp with time zone,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    dashboard_layout jsonb
);

CREATE TABLE merchant_accounts (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    label character varying(80) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    email character varying(255) DEFAULT ''::character varying NOT NULL,
    session_state text,
    signed_in_at timestamp with time zone,
    sync_enabled boolean DEFAULT false NOT NULL,
    sync_days integer DEFAULT 30 NOT NULL,
    last_synced_at timestamp with time zone,
    last_sync_status character varying(16) DEFAULT ''::character varying NOT NULL,
    last_sync_error text DEFAULT ''::text NOT NULL,
    needs_sign_in boolean DEFAULT false NOT NULL,
    gift_card_account_id uuid,
    gift_card_balance numeric(15,2),
    gift_card_balance_at timestamp with time zone,
    merchant character varying(16) DEFAULT 'amazon'::character varying NOT NULL,
    credential_sealed text,
    credential_has_totp boolean DEFAULT false NOT NULL,
    sign_in_paused_at timestamp with time zone,
    sign_in_paused_for text DEFAULT ''::text NOT NULL,
    second_factor text DEFAULT ''::text NOT NULL,
    invoice_backfill_at timestamp with time zone,
    invoice_backfill_filed integer DEFAULT 0 NOT NULL,
    invoice_backfill_left integer DEFAULT 0 NOT NULL,
    invoice_backfill_stopped text DEFAULT ''::text NOT NULL,
    last_sync_screenshot bytea,
    CONSTRAINT merchant_accounts_last_sync_screenshot_check CHECK ((octet_length(last_sync_screenshot) <= 1048576)),
    CONSTRAINT merchant_accounts_second_factor_check CHECK ((second_factor = ANY (ARRAY[''::text, 'email'::text, 'sms'::text, 'totp'::text]))),
    CONSTRAINT merchant_accounts_sign_in_paused_for_check CHECK ((sign_in_paused_for = ANY (ARRAY[''::text, 'password_refused'::text, 'code_needed'::text])))
);

CREATE TABLE merchant_catalog (
    merchant text NOT NULL,
    sku text NOT NULL,
    status text NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    brand text DEFAULT ''::text NOT NULL,
    size text DEFAULT ''::text NOT NULL,
    category text DEFAULT ''::text NOT NULL,
    image_url text DEFAULT ''::text NOT NULL,
    url text DEFAULT ''::text NOT NULL,
    price numeric(14,2),
    source text DEFAULT ''::text NOT NULL,
    source_ref text DEFAULT ''::text NOT NULL,
    raw jsonb,
    looked_up_at timestamp with time zone DEFAULT now() NOT NULL,
    found_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE merchant_charges (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    merchant_account_id uuid NOT NULL,
    order_number character varying(32) NOT NULL,
    charged_on date NOT NULL,
    amount numeric(15,2) NOT NULL,
    instrument character varying(80) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    merchant character varying(16) DEFAULT 'amazon'::character varying NOT NULL
);

CREATE TABLE merchant_matches (
    transaction_id uuid NOT NULL,
    space_id uuid NOT NULL,
    order_id uuid NOT NULL,
    amount numeric(15,2) NOT NULL,
    basis character varying(16) NOT NULL,
    confidence double precision DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    refund_id uuid
);

CREATE TABLE merchant_order_items (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    order_id uuid NOT NULL,
    "position" integer NOT NULL,
    sku character varying(16) DEFAULT ''::character varying NOT NULL,
    title text NOT NULL,
    quantity integer DEFAULT 1 NOT NULL,
    unit_price numeric(15,2),
    total_owed numeric(15,2),
    shipped_on date,
    condition character varying(32) DEFAULT ''::character varying NOT NULL,
    url text DEFAULT ''::text NOT NULL
);

CREATE TABLE merchant_orders (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    merchant_account_id uuid NOT NULL,
    order_number character varying(32) NOT NULL,
    ordered_on date NOT NULL,
    total numeric(15,2) DEFAULT 0 NOT NULL,
    currency character varying(3) DEFAULT 'USD'::character varying NOT NULL,
    status character varying(64) DEFAULT ''::character varying NOT NULL,
    details_url text DEFAULT ''::text NOT NULL,
    source character varying(32) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    gift_card_amount numeric(15,2),
    tax numeric(15,2),
    shipping numeric(15,2),
    ignored_at timestamp with time zone,
    merchant character varying(16) DEFAULT 'amazon'::character varying NOT NULL,
    kind character varying(16) DEFAULT 'online'::character varying NOT NULL,
    location character varying(160) DEFAULT ''::character varying NOT NULL,
    invoice_misses integer DEFAULT 0 NOT NULL
);

CREATE TABLE merchant_refunds (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    merchant character varying(16) NOT NULL,
    merchant_account_id uuid NOT NULL,
    order_number character varying(32) NOT NULL,
    sku character varying(32) DEFAULT ''::character varying NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    quantity integer DEFAULT 1 NOT NULL,
    refunded_on date NOT NULL,
    amount numeric(15,2) NOT NULL,
    instrument character varying(80) DEFAULT ''::character varying NOT NULL,
    destination character varying(16) DEFAULT 'card'::character varying NOT NULL,
    status character varying(64) DEFAULT ''::character varying NOT NULL,
    source character varying(32) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE notifications (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    alert_type character varying(64) NOT NULL,
    title character varying(255) NOT NULL,
    body text NOT NULL,
    url character varying(500),
    data jsonb,
    dedupe_key character varying(255) NOT NULL,
    read_at timestamp with time zone,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    condition_key character varying(255),
    resolved_at timestamp with time zone,
    cleared_at timestamp with time zone
);

CREATE TABLE passkeys (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    credential_id bytea NOT NULL,
    public_key bytea NOT NULL,
    sign_count bigint NOT NULL,
    name character varying(255) NOT NULL,
    transports character varying(255),
    rp_id character varying(255),
    is_discoverable boolean DEFAULT false NOT NULL,
    last_used_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE push_subscriptions (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    user_id uuid NOT NULL,
    endpoint character varying(1000) NOT NULL,
    p256dh character varying(255) NOT NULL,
    auth character varying(255) NOT NULL,
    user_agent character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_used_at timestamp with time zone
);

CREATE TABLE recovery_codes (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    code_hash character varying(64) NOT NULL,
    used_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE revoked_tokens (
    jti text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE rules (
    id uuid NOT NULL,
    name character varying(255) NOT NULL,
    filter_id uuid NOT NULL,
    priority integer NOT NULL,
    is_active boolean DEFAULT true NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    set_payee character varying(255),
    set_category_id uuid,
    add_tag_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    set_notes text,
    set_excluded_from_reports boolean,
    set_excluded_from_spending_plan boolean,
    set_is_reviewed boolean,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    source_ref character varying(255) DEFAULT ''::character varying NOT NULL
);

CREATE TABLE securities (
    id uuid NOT NULL,
    symbol character varying(32) NOT NULL,
    name character varying(255) NOT NULL,
    kind character varying(32) NOT NULL,
    exchange character varying(32),
    currency character varying(3) NOT NULL,
    cusip character varying(16),
    isin character varying(16),
    last_price numeric(20,10),
    last_price_at timestamp with time zone,
    prior_close numeric(20,10),
    is_deleted boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE security_prices (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    security_id uuid NOT NULL,
    on_date date NOT NULL,
    close numeric(20,10) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE series (
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    category_id uuid,
    kind character varying(32) NOT NULL,
    description character varying(500) NOT NULL,
    display_name character varying(255),
    amount numeric(15,2) DEFAULT 0 NOT NULL,
    currency character varying(3) NOT NULL,
    alias character varying(32) NOT NULL,
    frequency character varying(16),
    "interval" integer NOT NULL,
    by_month_day integer[] DEFAULT '{}'::integer[] NOT NULL,
    by_day character varying[] DEFAULT '{}'::character varying[] NOT NULL,
    start_on date NOT NULL,
    end_on date,
    next_due_on date,
    override_next_due_on date,
    override_next_amount numeric(15,2),
    auto_adjust_due_on boolean DEFAULT false NOT NULL,
    reminder_days integer NOT NULL,
    auto_accept_days integer,
    match_criteria character varying(16) NOT NULL,
    match_amount_min numeric(15,2),
    match_amount_max numeric(15,2),
    learned_descriptions character varying[] DEFAULT '{}'::character varying[] NOT NULL,
    template_payee character varying(255),
    template_notes text,
    template_tag_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    template_excluded_from_reports boolean DEFAULT false NOT NULL,
    template_excluded_from_spending_plan boolean DEFAULT false NOT NULL,
    template_splits jsonb,
    is_active boolean DEFAULT true NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    by_month integer[] DEFAULT '{}'::integer[] NOT NULL,
    CONSTRAINT series_by_month_check CHECK ((by_month <@ ARRAY[1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12]))
);

CREATE TABLE series_bill_links (
    series_id uuid NOT NULL,
    space_id uuid NOT NULL,
    subaccount_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE server_settings (
    key character varying(64) NOT NULL,
    value_encrypted text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE spaces (
    id uuid NOT NULL,
    name character varying(255) NOT NULL,
    primary_currency character varying(3) NOT NULL,
    timezone character varying(64) NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    default_date_range character varying(8),
    sidebar_account_types jsonb,
    setup_guide_dismissed_at timestamp with time zone,
    setup_guide_skipped text[] DEFAULT '{}'::text[] NOT NULL
);

CREATE TABLE spending_plan_months (
    id uuid NOT NULL,
    month date NOT NULL,
    calculated_rollover_amount numeric(15,2) DEFAULT 0 NOT NULL,
    calculated_income_amount numeric(15,2) DEFAULT 0 NOT NULL,
    income_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    excluded_income_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    overwritten_income_amount numeric(15,2),
    reset_overwritten_income boolean DEFAULT false NOT NULL,
    calculated_bills_amount numeric(15,2) DEFAULT 0 NOT NULL,
    bills_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    excluded_bills_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    overwritten_bills_amount numeric(15,2),
    reset_overwritten_bills boolean DEFAULT false NOT NULL,
    calculated_subscriptions_amount numeric(15,2) DEFAULT 0 NOT NULL,
    subscriptions_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    excluded_subscriptions_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    overwritten_subscriptions_amount numeric(15,2),
    reset_overwritten_subscriptions boolean DEFAULT false NOT NULL,
    calculated_transfer_amount numeric(15,2) DEFAULT 0 NOT NULL,
    transfer_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    excluded_transfer_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    overwritten_transfer_amount numeric(15,2),
    reset_overwritten_transfer boolean DEFAULT false NOT NULL,
    calculated_goals_amount numeric(15,2) DEFAULT 0 NOT NULL,
    goals_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    excluded_goals_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    overwritten_goals_amount numeric(15,2),
    reset_overwritten_goals boolean DEFAULT false NOT NULL,
    calculated_planned_spending_amount numeric(15,2) DEFAULT 0 NOT NULL,
    planned_spending_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    excluded_planned_spending_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    overwritten_planned_spending_amount numeric(15,2),
    reset_overwritten_planned_spending boolean DEFAULT false NOT NULL,
    calculated_spent_amount numeric(15,2) DEFAULT 0 NOT NULL,
    spent_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    excluded_spent_txn_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    overwritten_spent_amount numeric(15,2),
    reset_overwritten_spent boolean DEFAULT false NOT NULL,
    set_aside numeric(15,2) DEFAULT 0 NOT NULL,
    total_to_spend_amount numeric(15,2) DEFAULT 0 NOT NULL,
    left_to_spend_amount numeric(15,2) DEFAULT 0 NOT NULL,
    projected_other_spending numeric(15,2) DEFAULT 0 NOT NULL,
    projection_type character varying(24) NOT NULL,
    projection_start_date date,
    projection_end_date date,
    projection_buffer numeric(15,2) DEFAULT 0 NOT NULL,
    projection_window_months integer NOT NULL,
    is_closed_out boolean DEFAULT false NOT NULL,
    closed_out_at timestamp with time zone,
    show_closed_out boolean DEFAULT false NOT NULL,
    new_month_viewed boolean DEFAULT false NOT NULL,
    hide_excluded_from_ui boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE split_tags (
    split_id uuid NOT NULL,
    tag_id uuid NOT NULL
);

CREATE TABLE suggestion_dismissals (
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    signature character varying(64) NOT NULL,
    dismissed_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE tags (
    id uuid NOT NULL,
    name character varying(255) NOT NULL,
    color character varying(16),
    is_deleted boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE transaction_refund_links (
    space_id uuid NOT NULL,
    refund_txn_id uuid NOT NULL,
    charge_txn_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT transaction_refund_links_distinct CHECK ((refund_txn_id <> charge_txn_id))
);

CREATE TABLE transaction_splits (
    id uuid NOT NULL,
    transaction_id uuid NOT NULL,
    "position" integer NOT NULL,
    amount numeric(15,2) NOT NULL,
    category_id uuid,
    memo text,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE transaction_tags (
    transaction_id uuid NOT NULL,
    tag_id uuid NOT NULL
);

CREATE TABLE transactions (
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    external_id character varying(255),
    date date NOT NULL,
    effective_date date,
    amount numeric(15,2) NOT NULL,
    currency character varying(3) NOT NULL,
    amount_primary numeric(15,2),
    fx_rate_used numeric(20,10),
    statement_name character varying(500) DEFAULT ''::character varying NOT NULL,
    payee character varying(255) DEFAULT ''::character varying NOT NULL,
    notes text,
    check_number character varying(32),
    category_id uuid,
    source character varying(32) NOT NULL,
    is_pending boolean DEFAULT false NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    is_reviewed boolean DEFAULT false NOT NULL,
    excluded_from_reports boolean DEFAULT false NOT NULL,
    excluded_from_spending_plan boolean DEFAULT false NOT NULL,
    is_bill boolean DEFAULT false NOT NULL,
    is_subscription boolean DEFAULT false NOT NULL,
    transfer_pair_id uuid,
    user_flag character varying(16),
    user_flag_note text,
    series_id uuid,
    series_due_on date,
    estimate_status character varying(16),
    accepted_on date,
    expires_on date,
    rule_id uuid,
    balance numeric(15,2),
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    needs_settle boolean DEFAULT false NOT NULL,
    memo text DEFAULT ''::text NOT NULL,
    transacted_on date,
    provider_extra jsonb,
    category_checked_at timestamp with time zone,
    category_check_note text DEFAULT ''::text NOT NULL,
    category_check_run_id uuid,
    category_from_pair boolean DEFAULT false NOT NULL,
    padded_txn_id uuid,
    receipt_not_needed boolean DEFAULT false NOT NULL,
    CONSTRAINT transactions_check CHECK ((padded_txn_id <> id))
);

COMMENT ON COLUMN transactions.memo IS 'The provider''s memo line, verbatim.';

COMMENT ON COLUMN transactions.transacted_on IS 'When the purchase happened, if the feed distinguishes it from the posting date.';

COMMENT ON COLUMN transactions.provider_extra IS 'Unmapped provider payload: SimpleFIN''s extra object and any non-spec keys.';

COMMENT ON COLUMN transactions.category_checked_at IS 'When a category check last finished on this row without filing or proposing a category.';

COMMENT ON COLUMN transactions.category_check_note IS 'That run''s closing line, shown as the reason beside "Undetermined".';

COMMENT ON COLUMN transactions.category_check_run_id IS 'The assistant run that last decided this row''s category, for the register''s "why".';

CREATE TABLE users (
    id uuid NOT NULL,
    email character varying(320) NOT NULL,
    hashed_password character varying(255),
    full_name character varying(255),
    oidc_subject character varying(255),
    oidc_issuer character varying(255),
    totp_secret character varying(255),
    is_active boolean DEFAULT true NOT NULL,
    is_superuser boolean DEFAULT false NOT NULL,
    is_verified boolean DEFAULT false NOT NULL,
    locale character varying(16) NOT NULL,
    theme character varying(16) NOT NULL,
    privacy_mode boolean DEFAULT false NOT NULL,
    last_login_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    must_change_password boolean DEFAULT false NOT NULL,
    sessions_valid_from timestamp with time zone,
    swipe_left_action character varying(32) DEFAULT 'menu'::character varying NOT NULL,
    swipe_right_action character varying(32) DEFAULT 'review'::character varying NOT NULL,
    animation_duration_ms integer DEFAULT 160 NOT NULL,
    toast_duration_ms integer DEFAULT 6000 NOT NULL
);

CREATE TABLE watchlists (
    id uuid NOT NULL,
    filter_id uuid NOT NULL,
    name character varying(255) NOT NULL,
    emoji character varying(16),
    target_amount numeric(15,2),
    period character varying(16) NOT NULL,
    start_date date,
    end_date date,
    is_deleted boolean DEFAULT false NOT NULL,
    space_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY accounts
    ADD CONSTRAINT accounts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY alert_rules
    ADD CONSTRAINT alert_rules_pkey PRIMARY KEY (id);

ALTER TABLE ONLY merchant_accounts
    ADD CONSTRAINT amazon_accounts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY merchant_charges
    ADD CONSTRAINT amazon_charges_pkey PRIMARY KEY (id);

ALTER TABLE ONLY merchant_charges
    ADD CONSTRAINT amazon_charges_space_id_amazon_account_id_order_number_char_key UNIQUE (space_id, merchant_account_id, order_number, charged_on, amount);

ALTER TABLE ONLY merchant_matches
    ADD CONSTRAINT amazon_matches_pkey PRIMARY KEY (transaction_id, order_id);

ALTER TABLE ONLY merchant_order_items
    ADD CONSTRAINT amazon_order_items_pkey PRIMARY KEY (id);

ALTER TABLE ONLY merchant_orders
    ADD CONSTRAINT amazon_orders_pkey PRIMARY KEY (id);

ALTER TABLE ONLY merchant_orders
    ADD CONSTRAINT amazon_orders_space_id_amazon_account_id_order_number_key UNIQUE (space_id, merchant_account_id, order_number);

ALTER TABLE ONLY assistant_actions
    ADD CONSTRAINT assistant_actions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY assistant_automation_runs
    ADD CONSTRAINT assistant_automation_runs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY assistant_automations
    ADD CONSTRAINT assistant_automations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY assistant_connections
    ADD CONSTRAINT assistant_connections_pkey PRIMARY KEY (id);

ALTER TABLE ONLY assistant_conversations
    ADD CONSTRAINT assistant_conversations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY assistant_corrections
    ADD CONSTRAINT assistant_corrections_pkey PRIMARY KEY (id);

ALTER TABLE ONLY assistant_guidance
    ADD CONSTRAINT assistant_guidance_pkey PRIMARY KEY (id);

ALTER TABLE ONLY assistant_messages
    ADD CONSTRAINT assistant_messages_pkey PRIMARY KEY (id);

ALTER TABLE ONLY backup_runs
    ADD CONSTRAINT backup_runs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY balance_snapshots
    ADD CONSTRAINT balance_snapshots_pkey PRIMARY KEY (id);

ALTER TABLE ONLY bill_challenges
    ADD CONSTRAINT bill_challenges_pkey PRIMARY KEY (id);

ALTER TABLE ONLY bill_connections
    ADD CONSTRAINT bill_connections_pkey PRIMARY KEY (id);

ALTER TABLE ONLY bill_emails
    ADD CONSTRAINT bill_emails_message_key UNIQUE (connection_id, message_id);

ALTER TABLE ONLY bill_emails
    ADD CONSTRAINT bill_emails_pkey PRIMARY KEY (id);

ALTER TABLE ONLY bill_payments
    ADD CONSTRAINT bill_payments_pkey PRIMARY KEY (id);

ALTER TABLE ONLY bill_subaccounts
    ADD CONSTRAINT bill_subaccounts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY bills
    ADD CONSTRAINT bills_pkey PRIMARY KEY (id);

ALTER TABLE ONLY cash_flow_forecasts
    ADD CONSTRAINT cash_flow_forecasts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY categories
    ADD CONSTRAINT categories_pkey PRIMARY KEY (id);

ALTER TABLE ONLY category_suggestion_batches
    ADD CONSTRAINT category_suggestion_batches_pkey PRIMARY KEY (id);

ALTER TABLE ONLY connections
    ADD CONSTRAINT connections_pkey PRIMARY KEY (id);

ALTER TABLE ONLY document_links
    ADD CONSTRAINT document_links_pkey PRIMARY KEY (document_id, kind, target_id);

ALTER TABLE ONLY documents
    ADD CONSTRAINT documents_pkey PRIMARY KEY (id);

ALTER TABLE ONLY documents
    ADD CONSTRAINT documents_space_id_content_sha256_key UNIQUE (space_id, content_sha256);

ALTER TABLE ONLY email_connections
    ADD CONSTRAINT email_connections_label_key UNIQUE (space_id, label);

ALTER TABLE ONLY email_connections
    ADD CONSTRAINT email_connections_pkey PRIMARY KEY (id);

ALTER TABLE ONLY envelopes
    ADD CONSTRAINT envelopes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY filter_items
    ADD CONSTRAINT filter_items_pkey PRIMARY KEY (id);

ALTER TABLE ONLY filters
    ADD CONSTRAINT filters_pkey PRIMARY KEY (id);

ALTER TABLE ONLY fx_rates
    ADD CONSTRAINT fx_rates_pkey PRIMARY KEY (id);

ALTER TABLE ONLY goal_funding_accounts
    ADD CONSTRAINT goal_funding_accounts_pkey PRIMARY KEY (goal_id, account_id);

ALTER TABLE ONLY goals
    ADD CONSTRAINT goals_pkey PRIMARY KEY (id);

ALTER TABLE ONLY holdings
    ADD CONSTRAINT holdings_pkey PRIMARY KEY (id);

ALTER TABLE ONLY ignored_remote_accounts
    ADD CONSTRAINT ignored_remote_accounts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY institutions
    ADD CONSTRAINT institutions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY mail_rules
    ADD CONSTRAINT mail_rules_name_key UNIQUE (space_id, name);

ALTER TABLE ONLY mail_rules
    ADD CONSTRAINT mail_rules_pkey PRIMARY KEY (id);

ALTER TABLE ONLY manual_transfer_pairs
    ADD CONSTRAINT manual_transfer_pairs_pkey PRIMARY KEY (transfer_pair_id);

ALTER TABLE ONLY memberships
    ADD CONSTRAINT memberships_pkey PRIMARY KEY (id);

ALTER TABLE ONLY merchant_catalog
    ADD CONSTRAINT merchant_catalog_pkey PRIMARY KEY (merchant, sku);

ALTER TABLE ONLY merchant_refunds
    ADD CONSTRAINT merchant_refunds_pkey PRIMARY KEY (id);

ALTER TABLE ONLY merchant_refunds
    ADD CONSTRAINT merchant_refunds_space_id_merchant_account_id_order_number__key UNIQUE (space_id, merchant_account_id, order_number, sku, refunded_on, amount);

ALTER TABLE ONLY notifications
    ADD CONSTRAINT notifications_pkey PRIMARY KEY (id);

ALTER TABLE ONLY passkeys
    ADD CONSTRAINT passkeys_pkey PRIMARY KEY (id);

ALTER TABLE ONLY push_subscriptions
    ADD CONSTRAINT push_subscriptions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY recovery_codes
    ADD CONSTRAINT recovery_codes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY revoked_tokens
    ADD CONSTRAINT revoked_tokens_pkey PRIMARY KEY (jti);

ALTER TABLE ONLY rules
    ADD CONSTRAINT rules_pkey PRIMARY KEY (id);

ALTER TABLE ONLY securities
    ADD CONSTRAINT securities_pkey PRIMARY KEY (id);

ALTER TABLE ONLY security_prices
    ADD CONSTRAINT security_prices_pkey PRIMARY KEY (id);

ALTER TABLE ONLY series_bill_links
    ADD CONSTRAINT series_bill_links_pkey PRIMARY KEY (series_id);

ALTER TABLE ONLY series
    ADD CONSTRAINT series_pkey PRIMARY KEY (id);

ALTER TABLE ONLY server_settings
    ADD CONSTRAINT server_settings_pkey PRIMARY KEY (key);

ALTER TABLE ONLY spaces
    ADD CONSTRAINT spaces_pkey PRIMARY KEY (id);

ALTER TABLE ONLY spending_plan_months
    ADD CONSTRAINT spending_plan_months_pkey PRIMARY KEY (id);

ALTER TABLE ONLY split_tags
    ADD CONSTRAINT split_tags_pkey PRIMARY KEY (split_id, tag_id);

ALTER TABLE ONLY suggestion_dismissals
    ADD CONSTRAINT suggestion_dismissals_pkey PRIMARY KEY (id);

ALTER TABLE ONLY tags
    ADD CONSTRAINT tags_pkey PRIMARY KEY (id);

ALTER TABLE ONLY transaction_refund_links
    ADD CONSTRAINT transaction_refund_links_pkey PRIMARY KEY (space_id, refund_txn_id, charge_txn_id);

ALTER TABLE ONLY transaction_splits
    ADD CONSTRAINT transaction_splits_pkey PRIMARY KEY (id);

ALTER TABLE ONLY transaction_tags
    ADD CONSTRAINT transaction_tags_pkey PRIMARY KEY (transaction_id, tag_id);

ALTER TABLE ONLY transactions
    ADD CONSTRAINT transactions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY accounts
    ADD CONSTRAINT uq_account_connection_external UNIQUE (connection_id, external_id);

ALTER TABLE ONLY alert_rules
    ADD CONSTRAINT uq_alert_rule_scope UNIQUE (space_id, user_id, alert_type, account_id);

ALTER TABLE ONLY assistant_connections
    ADD CONSTRAINT uq_assistant_connection_space UNIQUE (space_id);

ALTER TABLE ONLY balance_snapshots
    ADD CONSTRAINT uq_balance_snapshot_account_day UNIQUE (account_id, as_of);

ALTER TABLE ONLY bill_connections
    ADD CONSTRAINT uq_bill_connection_label UNIQUE (space_id, biller, label);

ALTER TABLE ONLY bills
    ADD CONSTRAINT uq_bill_cycle UNIQUE (subaccount_id, due_on, invoice);

ALTER TABLE ONLY bill_payments
    ADD CONSTRAINT uq_bill_payment UNIQUE (subaccount_id, external_id);

ALTER TABLE ONLY bill_subaccounts
    ADD CONSTRAINT uq_bill_subaccount_external UNIQUE (connection_id, external_id);

ALTER TABLE ONLY fx_rates
    ADD CONSTRAINT uq_fx_rate_space_base_quote_date UNIQUE (space_id, base_currency, quote_currency, date);

ALTER TABLE ONLY holdings
    ADD CONSTRAINT uq_holding_account_security UNIQUE (account_id, security_id);

ALTER TABLE ONLY ignored_remote_accounts
    ADD CONSTRAINT uq_ignored_remote_account UNIQUE (connection_id, external_id);

ALTER TABLE ONLY institutions
    ADD CONSTRAINT uq_institution_space_external UNIQUE (space_id, external_id);

ALTER TABLE ONLY memberships
    ADD CONSTRAINT uq_membership_space_user UNIQUE (space_id, user_id);

ALTER TABLE ONLY notifications
    ADD CONSTRAINT uq_notification_dedupe UNIQUE (user_id, dedupe_key);

ALTER TABLE ONLY passkeys
    ADD CONSTRAINT uq_passkey_credential_id UNIQUE (credential_id);

ALTER TABLE ONLY push_subscriptions
    ADD CONSTRAINT uq_push_subscription_endpoint UNIQUE (user_id, endpoint);

ALTER TABLE ONLY security_prices
    ADD CONSTRAINT uq_security_price_day UNIQUE (space_id, security_id, on_date);

ALTER TABLE ONLY securities
    ADD CONSTRAINT uq_security_space_symbol UNIQUE (space_id, symbol);

ALTER TABLE ONLY series_bill_links
    ADD CONSTRAINT uq_series_bill_link_subaccount UNIQUE (subaccount_id);

ALTER TABLE ONLY spending_plan_months
    ADD CONSTRAINT uq_spending_plan_space_month UNIQUE (space_id, month);

ALTER TABLE ONLY suggestion_dismissals
    ADD CONSTRAINT uq_suggestion_dismissal UNIQUE (space_id, signature);

ALTER TABLE ONLY tags
    ADD CONSTRAINT uq_tag_space_name UNIQUE (space_id, name);

ALTER TABLE ONLY transactions
    ADD CONSTRAINT uq_transaction_account_external UNIQUE (account_id, external_id);

ALTER TABLE ONLY users
    ADD CONSTRAINT uq_user_oidc_identity UNIQUE (oidc_issuer, oidc_subject);

ALTER TABLE ONLY users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);

ALTER TABLE ONLY watchlists
    ADD CONSTRAINT watchlists_pkey PRIMARY KEY (id);

CREATE INDEX accounts_secured_by_idx ON accounts USING btree (space_id, secured_by_account_id) WHERE (secured_by_account_id IS NOT NULL);

CREATE INDEX bill_payments_space_unmatched ON bill_payments USING btree (space_id) WHERE (transaction_id IS NULL);

CREATE UNIQUE INDEX bill_payments_transaction ON bill_payments USING btree (transaction_id) WHERE (transaction_id IS NOT NULL);

CREATE UNIQUE INDEX bill_subaccounts_account_id_key ON bill_subaccounts USING btree (account_id) WHERE (account_id IS NOT NULL);

CREATE INDEX document_links_target ON document_links USING btree (space_id, kind, target_id);

CREATE INDEX documents_space_created ON documents USING btree (space_id, created_at);

CREATE INDEX ix_accounts_simplefin ON accounts USING btree (space_id, simplefin_account_id);

CREATE INDEX ix_accounts_space_id ON accounts USING btree (space_id);

CREATE INDEX ix_alert_rules_space_id ON alert_rules USING btree (space_id);

CREATE INDEX ix_assistant_actions_conversation ON assistant_actions USING btree (conversation_id, created_at);

CREATE INDEX ix_assistant_automation_runs_batch ON assistant_automation_runs USING btree (batch_id) WHERE (batch_id IS NOT NULL);

CREATE INDEX ix_assistant_automation_runs_feed ON assistant_automation_runs USING btree (space_id, queued_at DESC);

CREATE INDEX ix_assistant_automation_runs_queue ON assistant_automation_runs USING btree (bulk, queued_at, id) WHERE ((status)::text = 'queued'::text);

CREATE INDEX ix_assistant_automations_space ON assistant_automations USING btree (space_id, created_at);

CREATE INDEX ix_assistant_conversations_feed ON assistant_conversations USING btree (space_id, user_id, updated_at);

CREATE INDEX ix_assistant_conversations_run ON assistant_conversations USING btree (automation_run_id) WHERE (automation_run_id IS NOT NULL);

CREATE INDEX ix_assistant_corrections_space ON assistant_corrections USING btree (space_id, created_at DESC);

CREATE INDEX ix_assistant_guidance_space ON assistant_guidance USING btree (space_id, is_deleted, "position");

CREATE INDEX ix_assistant_messages_conversation ON assistant_messages USING btree (conversation_id, created_at);

CREATE INDEX ix_backup_runs_started ON backup_runs USING btree (started_at DESC);

CREATE INDEX ix_balance_snapshots_space_day ON balance_snapshots USING btree (space_id, as_of);

CREATE INDEX ix_balance_snapshots_space_id ON balance_snapshots USING btree (space_id);

CREATE INDEX ix_bill_challenges_waiting ON bill_challenges USING btree (space_id, connection_id, state);

CREATE INDEX ix_bill_emails_outcome ON bill_emails USING btree (space_id, outcome, received_at DESC);

CREATE INDEX ix_bill_emails_recent ON bill_emails USING btree (space_id, connection_id, received_at DESC);

CREATE INDEX ix_bills_subaccount_due ON bills USING btree (space_id, subaccount_id, due_on);

CREATE INDEX ix_cash_flow_forecasts_account_latest ON cash_flow_forecasts USING btree (space_id, account_id, generated_at DESC) WHERE (account_id IS NOT NULL);

CREATE INDEX ix_cash_flow_forecasts_latest ON cash_flow_forecasts USING btree (space_id, generated_at DESC);

CREATE INDEX ix_categories_known ON categories USING btree (space_id, known_category_id);

CREATE INDEX ix_categories_space_id ON categories USING btree (space_id);

CREATE INDEX ix_category_suggestion_batches_space ON category_suggestion_batches USING btree (space_id, created_at DESC);

CREATE INDEX ix_connections_space_id ON connections USING btree (space_id);

CREATE INDEX ix_email_connections_due ON email_connections USING btree (enabled, last_polled_at);

CREATE INDEX ix_envelopes_group ON envelopes USING btree (space_id, recurring_group_id);

CREATE INDEX ix_envelopes_month ON envelopes USING btree (space_id, spending_plan_month_id);

CREATE INDEX ix_envelopes_space_id ON envelopes USING btree (space_id);

CREATE INDEX ix_filter_items_filter ON filter_items USING btree (filter_id, group_index, "position");

CREATE INDEX ix_filter_items_space_id ON filter_items USING btree (space_id);

CREATE INDEX ix_filters_space_id ON filters USING btree (space_id);

CREATE INDEX ix_fx_rates_space_id ON fx_rates USING btree (space_id);

CREATE INDEX ix_fx_rates_space_quote_date ON fx_rates USING btree (space_id, quote_currency, date);

CREATE INDEX ix_goals_space_id ON goals USING btree (space_id);

CREATE INDEX ix_holdings_space_account ON holdings USING btree (space_id, account_id);

CREATE INDEX ix_holdings_space_id ON holdings USING btree (space_id);

CREATE INDEX ix_ignored_remote_accounts_space_id ON ignored_remote_accounts USING btree (space_id);

CREATE INDEX ix_institutions_space_id ON institutions USING btree (space_id);

CREATE INDEX ix_mail_rules_order ON mail_rules USING btree (space_id, sort_order, name);

CREATE INDEX ix_memberships_space_id ON memberships USING btree (space_id);

CREATE INDEX ix_notifications_feed ON notifications USING btree (space_id, user_id, created_at);

CREATE INDEX ix_notifications_space_id ON notifications USING btree (space_id);

CREATE INDEX ix_passkeys_user ON passkeys USING btree (user_id);

CREATE INDEX ix_push_subscriptions_user ON push_subscriptions USING btree (space_id, user_id);

CREATE INDEX ix_recovery_codes_user ON recovery_codes USING btree (user_id);

CREATE INDEX ix_revoked_tokens_expires ON revoked_tokens USING btree (expires_at);

CREATE INDEX ix_rules_space_id ON rules USING btree (space_id);

CREATE INDEX ix_securities_space_id ON securities USING btree (space_id);

CREATE INDEX ix_security_prices_series ON security_prices USING btree (space_id, security_id, on_date);

CREATE INDEX ix_series_due ON series USING btree (space_id, next_due_on);

CREATE INDEX ix_series_space_id ON series USING btree (space_id);

CREATE INDEX ix_spending_plan_months_space_id ON spending_plan_months USING btree (space_id);

CREATE INDEX ix_splits_category ON transaction_splits USING btree (space_id, category_id);

CREATE INDEX ix_suggestion_dismissals_space_id ON suggestion_dismissals USING btree (space_id);

CREATE INDEX ix_tags_space_id ON tags USING btree (space_id);

CREATE INDEX ix_transaction_refund_links_charge ON transaction_refund_links USING btree (space_id, charge_txn_id);

CREATE INDEX ix_transaction_splits_space_id ON transaction_splits USING btree (space_id);

CREATE INDEX ix_transactions_category ON transactions USING btree (space_id, category_id);

CREATE INDEX ix_transactions_effective ON transactions USING btree (space_id, effective_date);

CREATE INDEX ix_transactions_register ON transactions USING btree (space_id, account_id, date);

CREATE INDEX ix_transactions_relink_dedupe ON transactions USING btree (account_id, date, amount);

CREATE INDEX ix_transactions_series_slot ON transactions USING btree (space_id, series_id, series_due_on);

CREATE INDEX ix_transactions_transfer_pair ON transactions USING btree (space_id, transfer_pair_id);

CREATE INDEX ix_watchlists_space_id ON watchlists USING btree (space_id);

CREATE UNIQUE INDEX merchant_accounts_space_merchant_label_key ON merchant_accounts USING btree (space_id, merchant, label) WHERE ((label)::text <> ''::text);

CREATE INDEX merchant_catalog_missing ON merchant_catalog USING btree (merchant, looked_up_at) WHERE (status = 'missing'::text);

CREATE INDEX merchant_charges_space_merchant_date ON merchant_charges USING btree (space_id, merchant, charged_on);

CREATE INDEX merchant_matches_order ON merchant_matches USING btree (order_id);

CREATE INDEX merchant_order_items_order ON merchant_order_items USING btree (order_id);

CREATE INDEX merchant_orders_space_date ON merchant_orders USING btree (space_id, ordered_on);

CREATE INDEX merchant_orders_space_merchant_date ON merchant_orders USING btree (space_id, merchant, ordered_on);

CREATE INDEX merchant_refunds_order ON merchant_refunds USING btree (space_id, order_number);

CREATE INDEX merchant_refunds_space_merchant_date ON merchant_refunds USING btree (space_id, merchant, refunded_on);

CREATE INDEX transactions_needs_settle_idx ON transactions USING btree (space_id, created_at) WHERE needs_settle;

CREATE UNIQUE INDEX uq_alert_rule ON alert_rules USING btree (space_id, user_id, alert_type, account_id) NULLS NOT DISTINCT;

CREATE UNIQUE INDEX uq_assistant_automation_runs_transaction ON assistant_automation_runs USING btree (automation_id, transaction_id) WHERE ((fired_by)::text = 'transaction'::text);

CREATE UNIQUE INDEX uq_notification_open_condition ON notifications USING btree (user_id, condition_key) WHERE ((condition_key IS NOT NULL) AND (resolved_at IS NULL));

CREATE UNIQUE INDEX uq_rules_source_ref ON rules USING btree (space_id, source_ref) WHERE ((source_ref)::text <> ''::text);

CREATE UNIQUE INDEX users_email_lower_key ON users USING btree (lower((email)::text));

CREATE UNIQUE INDEX ux_transactions_padded ON transactions USING btree (padded_txn_id) WHERE (padded_txn_id IS NOT NULL);

CREATE TRIGGER transactions_category_from_pair BEFORE UPDATE OF category_id ON transactions FOR EACH ROW EXECUTE FUNCTION transactions_category_from_pair();

ALTER TABLE ONLY accounts
    ADD CONSTRAINT accounts_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES connections(id) ON DELETE SET NULL;

ALTER TABLE ONLY accounts
    ADD CONSTRAINT accounts_institution_id_fkey FOREIGN KEY (institution_id) REFERENCES institutions(id) ON DELETE SET NULL;

ALTER TABLE ONLY accounts
    ADD CONSTRAINT accounts_secured_by_account_id_fkey FOREIGN KEY (secured_by_account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE ONLY accounts
    ADD CONSTRAINT accounts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY accounts
    ADD CONSTRAINT accounts_statement_bill_id_fkey FOREIGN KEY (statement_bill_id) REFERENCES bills(id) ON DELETE SET NULL;

ALTER TABLE ONLY alert_rules
    ADD CONSTRAINT alert_rules_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY alert_rules
    ADD CONSTRAINT alert_rules_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY alert_rules
    ADD CONSTRAINT alert_rules_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_accounts
    ADD CONSTRAINT amazon_accounts_gift_card_account_id_fkey FOREIGN KEY (gift_card_account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE ONLY merchant_accounts
    ADD CONSTRAINT amazon_accounts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_charges
    ADD CONSTRAINT amazon_charges_amazon_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_charges
    ADD CONSTRAINT amazon_charges_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_matches
    ADD CONSTRAINT amazon_matches_order_id_fkey FOREIGN KEY (order_id) REFERENCES merchant_orders(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_matches
    ADD CONSTRAINT amazon_matches_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_matches
    ADD CONSTRAINT amazon_matches_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_order_items
    ADD CONSTRAINT amazon_order_items_order_id_fkey FOREIGN KEY (order_id) REFERENCES merchant_orders(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_order_items
    ADD CONSTRAINT amazon_order_items_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_orders
    ADD CONSTRAINT amazon_orders_amazon_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_orders
    ADD CONSTRAINT amazon_orders_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_actions
    ADD CONSTRAINT assistant_actions_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES assistant_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_actions
    ADD CONSTRAINT assistant_actions_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_automation_runs
    ADD CONSTRAINT assistant_automation_runs_automation_id_fkey FOREIGN KEY (automation_id) REFERENCES assistant_automations(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_automation_runs
    ADD CONSTRAINT assistant_automation_runs_batch_id_fkey FOREIGN KEY (batch_id) REFERENCES category_suggestion_batches(id) ON DELETE SET NULL;

ALTER TABLE ONLY assistant_automation_runs
    ADD CONSTRAINT assistant_automation_runs_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES assistant_conversations(id) ON DELETE SET NULL;

ALTER TABLE ONLY assistant_automation_runs
    ADD CONSTRAINT assistant_automation_runs_expected_category_id_fkey FOREIGN KEY (expected_category_id) REFERENCES categories(id) ON DELETE SET NULL;

ALTER TABLE ONLY assistant_automation_runs
    ADD CONSTRAINT assistant_automation_runs_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_automation_runs
    ADD CONSTRAINT assistant_automation_runs_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE SET NULL;

ALTER TABLE ONLY assistant_automations
    ADD CONSTRAINT assistant_automations_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_automations
    ADD CONSTRAINT assistant_automations_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT;

ALTER TABLE ONLY assistant_automations
    ADD CONSTRAINT assistant_automations_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_connections
    ADD CONSTRAINT assistant_connections_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_conversations
    ADD CONSTRAINT assistant_conversations_mail_id_fkey FOREIGN KEY (mail_id) REFERENCES bill_emails(id) ON DELETE SET NULL;

ALTER TABLE ONLY assistant_conversations
    ADD CONSTRAINT assistant_conversations_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_conversations
    ADD CONSTRAINT assistant_conversations_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_corrections
    ADD CONSTRAINT assistant_corrections_action_id_fkey FOREIGN KEY (action_id) REFERENCES assistant_actions(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_corrections
    ADD CONSTRAINT assistant_corrections_corrected_by_fkey FOREIGN KEY (corrected_by) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_corrections
    ADD CONSTRAINT assistant_corrections_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_guidance
    ADD CONSTRAINT assistant_guidance_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE ONLY assistant_guidance
    ADD CONSTRAINT assistant_guidance_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT;

ALTER TABLE ONLY assistant_guidance
    ADD CONSTRAINT assistant_guidance_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_messages
    ADD CONSTRAINT assistant_messages_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES assistant_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY assistant_messages
    ADD CONSTRAINT assistant_messages_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY balance_snapshots
    ADD CONSTRAINT balance_snapshots_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY balance_snapshots
    ADD CONSTRAINT balance_snapshots_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY bill_challenges
    ADD CONSTRAINT bill_challenges_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES bill_connections(id) ON DELETE CASCADE;

ALTER TABLE ONLY bill_challenges
    ADD CONSTRAINT bill_challenges_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY bill_connections
    ADD CONSTRAINT bill_connections_autopay_account_id_fkey FOREIGN KEY (autopay_account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE ONLY bill_connections
    ADD CONSTRAINT bill_connections_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY bill_emails
    ADD CONSTRAINT bill_emails_bill_id_fkey FOREIGN KEY (bill_id) REFERENCES bills(id) ON DELETE SET NULL;

ALTER TABLE ONLY bill_emails
    ADD CONSTRAINT bill_emails_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES email_connections(id) ON DELETE CASCADE;

ALTER TABLE ONLY bill_emails
    ADD CONSTRAINT bill_emails_document_id_fkey FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE SET NULL;

ALTER TABLE ONLY bill_emails
    ADD CONSTRAINT bill_emails_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES mail_rules(id) ON DELETE SET NULL;

ALTER TABLE ONLY bill_emails
    ADD CONSTRAINT bill_emails_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY bill_emails
    ADD CONSTRAINT bill_emails_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE SET NULL;

ALTER TABLE ONLY bill_payments
    ADD CONSTRAINT bill_payments_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY bill_payments
    ADD CONSTRAINT bill_payments_subaccount_id_fkey FOREIGN KEY (subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY bill_payments
    ADD CONSTRAINT bill_payments_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE SET NULL;

ALTER TABLE ONLY bill_subaccounts
    ADD CONSTRAINT bill_subaccounts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE ONLY bill_subaccounts
    ADD CONSTRAINT bill_subaccounts_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES bill_connections(id) ON DELETE CASCADE;

ALTER TABLE ONLY bill_subaccounts
    ADD CONSTRAINT bill_subaccounts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY bills
    ADD CONSTRAINT bills_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY bills
    ADD CONSTRAINT bills_subaccount_id_fkey FOREIGN KEY (subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY cash_flow_forecasts
    ADD CONSTRAINT cash_flow_forecasts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY cash_flow_forecasts
    ADD CONSTRAINT cash_flow_forecasts_automation_id_fkey FOREIGN KEY (automation_id) REFERENCES assistant_automations(id) ON DELETE SET NULL;

ALTER TABLE ONLY cash_flow_forecasts
    ADD CONSTRAINT cash_flow_forecasts_run_id_fkey FOREIGN KEY (automation_run_id) REFERENCES assistant_automation_runs(id) ON DELETE SET NULL;

ALTER TABLE ONLY cash_flow_forecasts
    ADD CONSTRAINT cash_flow_forecasts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY categories
    ADD CONSTRAINT categories_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES categories(id) ON DELETE SET NULL;

ALTER TABLE ONLY categories
    ADD CONSTRAINT categories_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY category_suggestion_batches
    ADD CONSTRAINT category_suggestion_batches_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY connections
    ADD CONSTRAINT connections_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY document_links
    ADD CONSTRAINT document_links_document_id_fkey FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE;

ALTER TABLE ONLY document_links
    ADD CONSTRAINT document_links_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY documents
    ADD CONSTRAINT documents_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY documents
    ADD CONSTRAINT documents_uploaded_by_user_id_fkey FOREIGN KEY (uploaded_by_user_id) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE ONLY email_connections
    ADD CONSTRAINT email_connections_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY envelopes
    ADD CONSTRAINT envelopes_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT;

ALTER TABLE ONLY envelopes
    ADD CONSTRAINT envelopes_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY envelopes
    ADD CONSTRAINT envelopes_spending_plan_month_id_fkey FOREIGN KEY (spending_plan_month_id) REFERENCES spending_plan_months(id) ON DELETE CASCADE;

ALTER TABLE ONLY filter_items
    ADD CONSTRAINT filter_items_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE CASCADE;

ALTER TABLE ONLY filter_items
    ADD CONSTRAINT filter_items_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY filters
    ADD CONSTRAINT filters_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY fx_rates
    ADD CONSTRAINT fx_rates_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY goal_funding_accounts
    ADD CONSTRAINT goal_funding_accounts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY goal_funding_accounts
    ADD CONSTRAINT goal_funding_accounts_goal_id_fkey FOREIGN KEY (goal_id) REFERENCES goals(id) ON DELETE CASCADE;

ALTER TABLE ONLY goals
    ADD CONSTRAINT goals_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY goals
    ADD CONSTRAINT goals_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY goals
    ADD CONSTRAINT goals_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE SET NULL;

ALTER TABLE ONLY holdings
    ADD CONSTRAINT holdings_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY holdings
    ADD CONSTRAINT holdings_security_id_fkey FOREIGN KEY (security_id) REFERENCES securities(id) ON DELETE RESTRICT;

ALTER TABLE ONLY holdings
    ADD CONSTRAINT holdings_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY ignored_remote_accounts
    ADD CONSTRAINT ignored_remote_accounts_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES connections(id) ON DELETE CASCADE;

ALTER TABLE ONLY ignored_remote_accounts
    ADD CONSTRAINT ignored_remote_accounts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY institutions
    ADD CONSTRAINT institutions_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY mail_rules
    ADD CONSTRAINT mail_rules_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE ONLY mail_rules
    ADD CONSTRAINT mail_rules_bill_connection_id_fkey FOREIGN KEY (bill_connection_id) REFERENCES bill_connections(id) ON DELETE SET NULL;

ALTER TABLE ONLY mail_rules
    ADD CONSTRAINT mail_rules_bill_subaccount_id_fkey FOREIGN KEY (bill_subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE SET NULL;

ALTER TABLE ONLY mail_rules
    ADD CONSTRAINT mail_rules_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL;

ALTER TABLE ONLY mail_rules
    ADD CONSTRAINT mail_rules_income_account_id_fkey FOREIGN KEY (income_account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE ONLY mail_rules
    ADD CONSTRAINT mail_rules_income_category_id_fkey FOREIGN KEY (income_category_id) REFERENCES categories(id) ON DELETE SET NULL;

ALTER TABLE ONLY mail_rules
    ADD CONSTRAINT mail_rules_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY manual_transfer_pairs
    ADD CONSTRAINT manual_transfer_pairs_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY memberships
    ADD CONSTRAINT memberships_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY memberships
    ADD CONSTRAINT memberships_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_matches
    ADD CONSTRAINT merchant_matches_refund_id_fkey FOREIGN KEY (refund_id) REFERENCES merchant_refunds(id) ON DELETE SET NULL;

ALTER TABLE ONLY merchant_refunds
    ADD CONSTRAINT merchant_refunds_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY merchant_refunds
    ADD CONSTRAINT merchant_refunds_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY notifications
    ADD CONSTRAINT notifications_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY notifications
    ADD CONSTRAINT notifications_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY passkeys
    ADD CONSTRAINT passkeys_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY push_subscriptions
    ADD CONSTRAINT push_subscriptions_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY push_subscriptions
    ADD CONSTRAINT push_subscriptions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY recovery_codes
    ADD CONSTRAINT recovery_codes_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY rules
    ADD CONSTRAINT rules_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT;

ALTER TABLE ONLY rules
    ADD CONSTRAINT rules_set_category_id_fkey FOREIGN KEY (set_category_id) REFERENCES categories(id) ON DELETE SET NULL;

ALTER TABLE ONLY rules
    ADD CONSTRAINT rules_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY securities
    ADD CONSTRAINT securities_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY security_prices
    ADD CONSTRAINT security_prices_security_id_fkey FOREIGN KEY (security_id) REFERENCES securities(id) ON DELETE CASCADE;

ALTER TABLE ONLY security_prices
    ADD CONSTRAINT security_prices_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY series
    ADD CONSTRAINT series_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY series_bill_links
    ADD CONSTRAINT series_bill_links_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE;

ALTER TABLE ONLY series_bill_links
    ADD CONSTRAINT series_bill_links_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY series_bill_links
    ADD CONSTRAINT series_bill_links_subaccount_id_fkey FOREIGN KEY (subaccount_id) REFERENCES bill_subaccounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY series
    ADD CONSTRAINT series_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL;

ALTER TABLE ONLY series
    ADD CONSTRAINT series_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY spending_plan_months
    ADD CONSTRAINT spending_plan_months_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY split_tags
    ADD CONSTRAINT split_tags_split_id_fkey FOREIGN KEY (split_id) REFERENCES transaction_splits(id) ON DELETE CASCADE;

ALTER TABLE ONLY split_tags
    ADD CONSTRAINT split_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE;

ALTER TABLE ONLY suggestion_dismissals
    ADD CONSTRAINT suggestion_dismissals_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY tags
    ADD CONSTRAINT tags_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY transaction_refund_links
    ADD CONSTRAINT transaction_refund_links_charge_txn_id_fkey FOREIGN KEY (charge_txn_id) REFERENCES transactions(id) ON DELETE CASCADE;

ALTER TABLE ONLY transaction_refund_links
    ADD CONSTRAINT transaction_refund_links_refund_txn_id_fkey FOREIGN KEY (refund_txn_id) REFERENCES transactions(id) ON DELETE CASCADE;

ALTER TABLE ONLY transaction_refund_links
    ADD CONSTRAINT transaction_refund_links_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY transaction_splits
    ADD CONSTRAINT transaction_splits_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL;

ALTER TABLE ONLY transaction_splits
    ADD CONSTRAINT transaction_splits_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY transaction_splits
    ADD CONSTRAINT transaction_splits_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE;

ALTER TABLE ONLY transaction_tags
    ADD CONSTRAINT transaction_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE;

ALTER TABLE ONLY transaction_tags
    ADD CONSTRAINT transaction_tags_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE;

ALTER TABLE ONLY transactions
    ADD CONSTRAINT transactions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY transactions
    ADD CONSTRAINT transactions_category_check_run_id_fkey FOREIGN KEY (category_check_run_id) REFERENCES assistant_automation_runs(id) ON DELETE SET NULL;

ALTER TABLE ONLY transactions
    ADD CONSTRAINT transactions_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL;

ALTER TABLE ONLY transactions
    ADD CONSTRAINT transactions_padded_txn_id_fkey FOREIGN KEY (padded_txn_id) REFERENCES transactions(id) ON DELETE CASCADE;

ALTER TABLE ONLY transactions
    ADD CONSTRAINT transactions_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES rules(id) ON DELETE SET NULL;

ALTER TABLE ONLY transactions
    ADD CONSTRAINT transactions_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE SET NULL;

ALTER TABLE ONLY transactions
    ADD CONSTRAINT transactions_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY watchlists
    ADD CONSTRAINT watchlists_filter_id_fkey FOREIGN KEY (filter_id) REFERENCES filters(id) ON DELETE RESTRICT;

ALTER TABLE ONLY watchlists
    ADD CONSTRAINT watchlists_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS watchlists CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS transactions CASCADE;
DROP TABLE IF EXISTS transaction_tags CASCADE;
DROP TABLE IF EXISTS transaction_splits CASCADE;
DROP TABLE IF EXISTS transaction_refund_links CASCADE;
DROP TABLE IF EXISTS tags CASCADE;
DROP TABLE IF EXISTS suggestion_dismissals CASCADE;
DROP TABLE IF EXISTS split_tags CASCADE;
DROP TABLE IF EXISTS spending_plan_months CASCADE;
DROP TABLE IF EXISTS spaces CASCADE;
DROP TABLE IF EXISTS server_settings CASCADE;
DROP TABLE IF EXISTS series_bill_links CASCADE;
DROP TABLE IF EXISTS series CASCADE;
DROP TABLE IF EXISTS security_prices CASCADE;
DROP TABLE IF EXISTS securities CASCADE;
DROP TABLE IF EXISTS rules CASCADE;
DROP TABLE IF EXISTS revoked_tokens CASCADE;
DROP TABLE IF EXISTS recovery_codes CASCADE;
DROP TABLE IF EXISTS push_subscriptions CASCADE;
DROP TABLE IF EXISTS passkeys CASCADE;
DROP TABLE IF EXISTS notifications CASCADE;
DROP TABLE IF EXISTS merchant_refunds CASCADE;
DROP TABLE IF EXISTS merchant_orders CASCADE;
DROP TABLE IF EXISTS merchant_order_items CASCADE;
DROP TABLE IF EXISTS merchant_matches CASCADE;
DROP TABLE IF EXISTS merchant_charges CASCADE;
DROP TABLE IF EXISTS merchant_catalog CASCADE;
DROP TABLE IF EXISTS merchant_accounts CASCADE;
DROP TABLE IF EXISTS memberships CASCADE;
DROP TABLE IF EXISTS manual_transfer_pairs CASCADE;
DROP TABLE IF EXISTS mail_rules CASCADE;
DROP TABLE IF EXISTS institutions CASCADE;
DROP TABLE IF EXISTS ignored_remote_accounts CASCADE;
DROP TABLE IF EXISTS holdings CASCADE;
DROP TABLE IF EXISTS goals CASCADE;
DROP TABLE IF EXISTS goal_funding_accounts CASCADE;
DROP TABLE IF EXISTS fx_rates CASCADE;
DROP TABLE IF EXISTS filters CASCADE;
DROP TABLE IF EXISTS filter_items CASCADE;
DROP TABLE IF EXISTS envelopes CASCADE;
DROP TABLE IF EXISTS email_connections CASCADE;
DROP TABLE IF EXISTS documents CASCADE;
DROP TABLE IF EXISTS document_links CASCADE;
DROP TABLE IF EXISTS connections CASCADE;
DROP TABLE IF EXISTS category_suggestion_batches CASCADE;
DROP TABLE IF EXISTS categories CASCADE;
DROP TABLE IF EXISTS cash_flow_forecasts CASCADE;
DROP TABLE IF EXISTS bills CASCADE;
DROP TABLE IF EXISTS bill_subaccounts CASCADE;
DROP TABLE IF EXISTS bill_payments CASCADE;
DROP TABLE IF EXISTS bill_emails CASCADE;
DROP TABLE IF EXISTS bill_connections CASCADE;
DROP TABLE IF EXISTS bill_challenges CASCADE;
DROP TABLE IF EXISTS balance_snapshots CASCADE;
DROP TABLE IF EXISTS backup_runs CASCADE;
DROP TABLE IF EXISTS assistant_messages CASCADE;
DROP TABLE IF EXISTS assistant_guidance CASCADE;
DROP TABLE IF EXISTS assistant_corrections CASCADE;
DROP TABLE IF EXISTS assistant_conversations CASCADE;
DROP TABLE IF EXISTS assistant_connections CASCADE;
DROP TABLE IF EXISTS assistant_automations CASCADE;
DROP TABLE IF EXISTS assistant_automation_runs CASCADE;
DROP TABLE IF EXISTS assistant_actions CASCADE;
DROP TABLE IF EXISTS alert_rules CASCADE;
DROP TABLE IF EXISTS accounts CASCADE;
DROP FUNCTION IF EXISTS transactions_category_from_pair();
-- +goose StatementEnd
