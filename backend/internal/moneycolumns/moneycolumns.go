// Package moneycolumns is the current schema's contract for its money
// columns: which columns hold integer minor units, and which currency decides
// where each one's decimal point goes. The go-jet generator and the legacy
// importer read it; it imports nothing of this module, so the store, which
// imports the generator's output, can read it too.
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
