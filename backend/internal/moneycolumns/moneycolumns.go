// Package moneycolumns is the current schema's contract for its money
// columns: which columns hold integer minor units, which currency decides
// where each one's decimal point goes, and what every other INTEGER column
// holds instead. The go-jet generator and the legacy importer read it; it
// imports nothing of this module, so the store, which imports the generator's
// output, can read it too.
package moneycolumns

// A Denomination names the currency of a money column's minor units. Exactly
// one of Own, Primary, Fixed and Parent is set.
type Denomination struct {
	// Own is the row's own currency column.
	Own bool
	// Primary is the household's primary currency.
	Primary bool
	// Fixed is an ISO 4217 code that every row is in.
	Fixed string
	// Parent is the table whose currency column names it, for the parent row
	// whose id the Via column holds.
	Parent, Via string
}

var (
	own     = Denomination{Own: true}
	primary = Denomination{Primary: true}
)

func parent(table, via string) Denomination { return Denomination{Parent: table, Via: via} }

// Columns maps every money column, as table.column, to its denomination.
var Columns = map[string]Denomination{
	"accounts.provider_balance": own, "accounts.opening_balance": own, "accounts.goal_balance": own,
	"accounts.pending_holds": own, "accounts.credit_limit": own, "accounts.statement_balance": own,
	"accounts.minimum_due": own, "accounts.withheld_balance": own, "accounts.hide_below_balance": own,
	"alert_rules.threshold_amount":      primary,
	"assistant_corrections.amount":      parent("transactions", "transaction_id"),
	"balance_snapshots.balance":         parent("accounts", "account_id"),
	"balance_snapshots.balance_primary": primary,
	"balance_snapshots.anchor_balance":  parent("accounts", "account_id"),
	// Bills' payment feed has no currency: service/bills.go subaccountCurrency is USD.
	"bill_payments.amount": {Fixed: "USD"}, "bills.amount_due": own, "bills.minimum_due": own,
	"envelopes.target_amount": primary, "envelopes.overwritten_target_amount": primary,
	"envelopes.calculated_spent_amount": primary, "envelopes.rollover_amount": primary,
	"filter_items.amount_min": primary, "filter_items.amount_max": primary,
	"goals.target_amount": primary, "goals.contribution_amount": primary,
	"goals.contributed_this_month": primary, "goals.saved_so_far": primary, "goals.spent": primary,
	"holdings.cost_basis":             parent("securities", "security_id"),
	"holdings.market_value":           parent("securities", "security_id"),
	"institutions.hide_below_balance": primary,
	// The store keeps every merchant amount except an order's own at the
	// household's primary scale.
	"merchant_accounts.gift_card_balance": primary,
	// provider/merchantcatalog.go queries sameday.costco.com (the US catalog).
	"merchant_catalog.price":          {Fixed: "USD"},
	"merchant_charges.amount":         primary,
	"merchant_matches.amount":         primary,
	"merchant_order_items.unit_price": parent("merchant_orders", "order_id"),
	"merchant_order_items.total_owed": parent("merchant_orders", "order_id"),
	"merchant_orders.total":           own, "merchant_orders.gift_card_amount": own, "merchant_orders.tax": own,
	"merchant_orders.shipping": own, "merchant_orders.refund_total": own,
	"merchant_refunds.amount": primary,
	"series.amount":           own, "series.override_next_amount": own,
	"series.match_amount_min": own, "series.match_amount_max": own,
	"spending_plan_months.calculated_rollover_amount":          primary,
	"spending_plan_months.calculated_income_amount":            primary,
	"spending_plan_months.overwritten_income_amount":           primary,
	"spending_plan_months.calculated_bills_amount":             primary,
	"spending_plan_months.overwritten_bills_amount":            primary,
	"spending_plan_months.calculated_subscriptions_amount":     primary,
	"spending_plan_months.overwritten_subscriptions_amount":    primary,
	"spending_plan_months.calculated_transfer_amount":          primary,
	"spending_plan_months.overwritten_transfer_amount":         primary,
	"spending_plan_months.calculated_goals_amount":             primary,
	"spending_plan_months.overwritten_goals_amount":            primary,
	"spending_plan_months.calculated_planned_spending_amount":  primary,
	"spending_plan_months.overwritten_planned_spending_amount": primary,
	"spending_plan_months.calculated_spent_amount":             primary,
	"spending_plan_months.overwritten_spent_amount":            primary,
	"spending_plan_months.set_aside":                           primary,
	"spending_plan_months.total_to_spend_amount":               primary,
	"spending_plan_months.left_to_spend_amount":                primary,
	"spending_plan_months.projected_other_spending":            primary,
	"spending_plan_months.projection_buffer":                   primary,
	"transaction_splits.amount":                                parent("transactions", "transaction_id"),
	"transactions.amount":                                      own, "transactions.amount_primary": primary, "transactions.balance": own,
	"watchlists.target_amount": primary,
}

// NotMoney lists every other INTEGER column of the current schema, as
// table.column, under what it holds instead.
var NotMoney = map[string][]string{
	"a time in Unix milliseconds": {
		"accounts.created_at", "accounts.ignored_at", "accounts.provider_balance_at",
		"accounts.updated_at", "accounts.valued_at", "accounts.withheld_balance_at",
		"alert_rules.created_at", "alert_rules.updated_at", "assistant_actions.created_at",
		"assistant_actions.decided_at", "assistant_automation_runs.finished_at",
		"assistant_automation_runs.queued_at", "assistant_automation_runs.started_at",
		"assistant_automations.created_at", "assistant_automations.updated_at",
		"assistant_connections.created_at", "assistant_connections.updated_at",
		"assistant_conversations.created_at", "assistant_conversations.updated_at",
		"assistant_corrections.created_at", "assistant_guidance.created_at",
		"assistant_guidance.updated_at", "assistant_messages.created_at", "backup_runs.finished_at",
		"backup_runs.started_at", "balance_snapshots.created_at", "balance_snapshots.updated_at",
		"bill_challenges.answered_at", "bill_challenges.created_at", "bill_challenges.expires_at",
		"bill_connections.created_at", "bill_connections.last_keepalive_at",
		"bill_connections.last_pulled_at", "bill_connections.sign_in_paused_at",
		"bill_connections.signed_in_at", "bill_connections.updated_at", "bill_emails.created_at",
		"bill_emails.received_at", "bill_emails.updated_at", "bill_payments.created_at",
		"bill_payments.fetched_at", "bill_payments.updated_at", "bill_subaccounts.created_at",
		"bill_subaccounts.updated_at", "bills.amended_at", "bills.created_at", "bills.fetched_at",
		"bills.marked_paid_at", "bills.updated_at", "cash_flow_forecasts.generated_at",
		"categories.created_at", "categories.updated_at", "category_suggestion_batches.cancelled_at",
		"category_suggestion_batches.created_at", "category_suggestion_batches.dismissed_at",
		"connections.created_at", "connections.last_successful_sync_at", "connections.last_sync_at",
		"connections.retry_not_before", "connections.updated_at", "document_links.created_at",
		"document_links.updated_at", "documents.created_at", "documents.updated_at",
		"duplicate_candidates.created_at", "duplicate_candidates.decided_at",
		"duplicate_candidates.updated_at", "email_connections.created_at",
		"email_connections.last_polled_at", "email_connections.poll_failing_since",
		"email_connections.updated_at", "envelopes.created_at", "envelopes.updated_at",
		"filter_items.created_at", "filter_items.updated_at", "filters.created_at",
		"filters.updated_at", "fx_rates.created_at", "fx_rates.updated_at",
		"goal_funding_accounts.updated_at", "goals.created_at", "goals.updated_at",
		"holdings.created_at", "holdings.updated_at", "household_metadata.updated_at",
		"households.created_at", "households.setup_guide_dismissed_at", "households.updated_at",
		"ignored_remote_accounts.ignored_at", "institutions.created_at", "institutions.updated_at",
		"mail_rules.created_at", "mail_rules.updated_at", "manual_transfer_pairs.created_at",
		"manual_transfer_pairs.updated_at", "memberships.accepted_at", "memberships.created_at",
		"memberships.invited_at", "memberships.updated_at", "merchant_accounts.created_at",
		"merchant_accounts.gift_card_balance_at", "merchant_accounts.invoice_backfill_at",
		"merchant_accounts.last_synced_at", "merchant_accounts.sign_in_paused_at",
		"merchant_accounts.signed_in_at", "merchant_accounts.updated_at", "merchant_catalog.created_at",
		"merchant_catalog.found_at", "merchant_catalog.looked_up_at", "merchant_catalog.updated_at",
		"merchant_charges.created_at", "merchant_charges.updated_at", "merchant_matches.created_at",
		"merchant_matches.updated_at", "merchant_order_items.updated_at", "merchant_orders.created_at",
		"merchant_orders.ignored_at", "merchant_orders.updated_at", "merchant_refunds.created_at",
		"merchant_refunds.updated_at", "notifications.cleared_at", "notifications.created_at",
		"notifications.read_at", "notifications.resolved_at", "notifications.updated_at",
		"passkeys.created_at", "passkeys.last_used_at", "passkeys.updated_at",
		"push_subscriptions.created_at", "push_subscriptions.last_used_at", "recovery_codes.created_at",
		"recovery_codes.updated_at", "recovery_codes.used_at", "rules.created_at", "rules.updated_at",
		"securities.created_at", "securities.last_price_at", "securities.updated_at",
		"security_prices.created_at", "series.created_at", "series.updated_at",
		"series_bill_links.created_at", "series_bill_links.updated_at", "server_settings.updated_at",
		"sessions.created_at", "sessions.expires_at", "sessions.last_used_at", "sessions.revoked_at",
		"spending_plan_months.closed_out_at", "spending_plan_months.created_at",
		"spending_plan_months.updated_at", "split_tags.updated_at",
		"suggestion_dismissals.dismissed_at", "suggestion_dismissals.updated_at", "tags.created_at",
		"tags.updated_at", "tombstones.deleted_at", "transaction_refund_links.created_at",
		"transaction_refund_links.updated_at", "transaction_splits.created_at",
		"transaction_splits.updated_at", "transaction_tags.updated_at",
		"transactions.category_checked_at", "transactions.created_at", "transactions.updated_at",
		"users.created_at", "users.last_login_at", "users.updated_at", "watchlists.created_at",
		"watchlists.updated_at",
	},
	"a duration in milliseconds": {
		"users.animation_duration_ms", "users.toast_duration_ms",
	},
	"a 0/1 flag": {
		"accounts.accept_zero_balance", "accounts.exclude_bank_pending",
		"accounts.excluded_from_account_bar", "accounts.excluded_from_reports",
		"accounts.excluded_from_spending_plan", "accounts.include_in_net_worth", "accounts.is_closed",
		"accounts.is_deleted", "accounts.requires_receipts", "alert_rules.channel_email",
		"alert_rules.channel_in_app", "alert_rules.channel_push", "alert_rules.is_enabled",
		"alert_rules.is_paused", "assistant_automation_runs.blind", "assistant_automation_runs.bulk",
		"assistant_automation_runs.dry_run", "assistant_automation_runs.reviewed_when_queued",
		"assistant_automations.description_from_template", "assistant_automations.is_enabled",
		"assistant_automations.prompt_from_template", "assistant_connections.allow_writes",
		"assistant_connections.apply_without_asking", "assistant_connections.is_enabled",
		"assistant_guidance.is_active", "assistant_guidance.is_deleted", "backup_runs.encrypted",
		"balance_snapshots.is_imported", "bill_connections.credential_has_totp",
		"bill_connections.needs_sign_in", "bill_connections.pull_enabled",
		"bill_subaccounts.is_selected", "categories.excluded_from_category_list",
		"categories.excluded_from_reports", "categories.excluded_from_spending_plan",
		"categories.is_deleted", "categories.is_editable", "categories.is_user_assignable",
		"connections.is_deleted", "email_connections.enabled", "envelopes.auto_release_rollover",
		"envelopes.hide_excluded_from_ui", "envelopes.recurring", "envelopes.rollover_is_carried",
		"filter_items.negated", "filter_items.state", "filters.is_deleted", "goals.is_deleted",
		"goals.is_taken_from_plan", "holdings.is_cost_basis_complete", "households.is_deleted",
		"institutions.is_deleted", "mail_rules.enabled", "mail_rules.pad_income",
		"merchant_accounts.credential_has_totp", "merchant_accounts.needs_sign_in",
		"merchant_accounts.sync_enabled", "passkeys.backup_eligible", "passkeys.is_discoverable",
		"rules.is_active", "rules.is_deleted", "rules.set_excluded_from_reports",
		"rules.set_excluded_from_spending_plan", "rules.set_is_reviewed", "securities.is_deleted",
		"series.auto_adjust_due_on", "series.is_active", "series.is_deleted",
		"series.template_excluded_from_reports", "series.template_excluded_from_spending_plan",
		"spending_plan_months.hide_excluded_from_ui", "spending_plan_months.is_closed_out",
		"spending_plan_months.new_month_viewed", "spending_plan_months.reset_overwritten_bills",
		"spending_plan_months.reset_overwritten_goals", "spending_plan_months.reset_overwritten_income",
		"spending_plan_months.reset_overwritten_planned_spending",
		"spending_plan_months.reset_overwritten_spent",
		"spending_plan_months.reset_overwritten_subscriptions",
		"spending_plan_months.reset_overwritten_transfer", "spending_plan_months.show_closed_out",
		"tags.is_deleted", "transactions.category_from_pair", "transactions.excluded_from_reports",
		"transactions.excluded_from_spending_plan", "transactions.is_bill", "transactions.is_deleted",
		"transactions.is_pending", "transactions.is_reviewed", "transactions.is_subscription",
		"transactions.needs_settle", "transactions.receipt_not_needed", "users.is_active",
		"users.is_superuser", "users.is_verified", "users.must_change_password", "users.privacy_mode",
		"watchlists.is_deleted",
	},
	"a change-capture or write sequence number": {
		"accounts.change_seq", "alert_rules.change_seq", "assistant_automations.change_seq",
		"assistant_conversations.change_seq", "assistant_guidance.change_seq",
		"balance_snapshots.change_seq", "bill_payments.change_seq", "bill_subaccounts.change_seq",
		"bills.change_seq", "categories.change_seq", "change_counter.seq", "document_links.change_seq",
		"documents.change_seq", "duplicate_candidates.change_seq", "envelopes.change_seq",
		"filter_items.change_seq", "filters.change_seq", "goal_funding_accounts.change_seq",
		"goals.change_seq", "holdings.change_seq", "household_metadata.change_seq",
		"institutions.change_seq", "mail_rules.change_seq", "manual_transfer_pairs.change_seq",
		"merchant_charges.change_seq", "merchant_matches.change_seq", "merchant_order_items.change_seq",
		"merchant_orders.change_seq", "merchant_refunds.change_seq", "notifications.change_seq",
		"rules.change_seq", "securities.change_seq", "series.change_seq",
		"series_bill_links.change_seq", "spending_plan_months.change_seq", "split_tags.change_seq",
		"suggestion_dismissals.change_seq", "tags.change_seq", "tombstones.seq",
		"transaction_refund_links.change_seq", "transaction_splits.change_seq",
		"transaction_tags.change_seq", "transactions.change_seq", "watchlists.change_seq",
		"write_counter.seq",
	},
	"the id of a table's one row, always 1": {
		"change_counter.id", "household_metadata.id", "write_counter.id",
	},
	"goose's record of an applied migration": {
		"goose_db_version.id", "goose_db_version.is_applied", "goose_db_version.version_id",
	},
	"a count": {
		"alert_rules.threshold_count", "assistant_automation_runs.actions",
		"assistant_automation_runs.tool_calls", "assistant_automations.max_tool_rounds",
		"category_suggestion_batches.row_count", "email_connections.poll_failures",
		"merchant_accounts.invoice_backfill_filed", "merchant_accounts.invoice_backfill_left",
		"merchant_order_items.quantity", "merchant_orders.invoice_misses", "merchant_refunds.quantity",
		"passkeys.sign_count", "series.interval",
	},
	"a position or sort order": {
		"accounts.sort_order", "assistant_guidance.position", "categories.sort_order",
		"filter_items.group_index", "filter_items.position", "filters.position",
		"mail_rules.sort_order", "merchant_order_items.position", "rules.priority",
		"transaction_splits.position",
	},
	"a number of days": {
		"bill_connections.autopay_days", "duplicate_candidates.days_apart",
		"merchant_accounts.sync_days", "series.auto_accept_days", "series.reminder_days",
	},
	"a day of the month": {
		"accounts.statement_close_day", "bill_connections.autopay_day",
	},
	"a number of months": {
		"spending_plan_months.projection_window_months",
	},
	"a distance in miles": {
		"accounts.vehicle_mileage", "accounts.vehicle_miles_per_year",
	},
	"a size in bytes": {
		"backup_runs.bytes", "documents.size_bytes",
	},
	"an HTTP status code": {
		"assistant_actions.status_code",
	},
	"a TCP port": {
		"email_connections.port",
	},
}
