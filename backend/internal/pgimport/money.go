package pgimport

import (
	"fmt"
	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"strings"
)

// MoneyCurrencies is the single column-to-denomination contract. Parent paths
// name the foreign key followed by the parent's currency column.
var MoneyCurrencies = map[string]string{
	"accounts.provider_balance": "currency", "accounts.opening_balance": "currency", "accounts.goal_balance": "currency", "accounts.pending_holds": "currency", "accounts.credit_limit": "currency", "accounts.statement_balance": "currency", "accounts.minimum_due": "currency", "accounts.withheld_balance": "currency", "accounts.hide_below_balance": "currency",
	"alert_rules.threshold_amount": "primary", "assistant_corrections.amount": "correction",
	"balance_snapshots.balance": "accounts:account_id", "balance_snapshots.balance_primary": "primary", "balance_snapshots.anchor_balance": "accounts:account_id",
	// Bills' payment feed has no currency: service/bills.go subaccountCurrency is USD.
	"bill_payments.amount": "USD", "bills.amount_due": "currency", "bills.minimum_due": "currency",
	"envelopes.target_amount": "primary", "envelopes.overwritten_target_amount": "primary", "envelopes.calculated_spent_amount": "primary", "envelopes.rollover_amount": "primary",
	"filter_items.amount_min": "primary", "filter_items.amount_max": "primary",
	"goals.target_amount": "primary", "goals.contribution_amount": "primary", "goals.contributed_this_month": "primary", "goals.saved_so_far": "primary", "goals.spent": "primary",
	"holdings.cost_basis": "securities:security_id", "holdings.market_value": "securities:security_id", "institutions.hide_below_balance": "primary",
	// The store keeps every merchant amount except an order's own at the
	// household's primary scale.
	"merchant_accounts.gift_card_balance": "primary",
	// provider/merchantcatalog.go queries sameday.costco.com (the US catalog).
	"merchant_catalog.price":  "USD",
	"merchant_charges.amount": "primary", "merchant_matches.amount": "primary", "merchant_order_items.unit_price": "merchant_orders:order_id", "merchant_order_items.total_owed": "merchant_orders:order_id",
	"merchant_orders.total": "currency", "merchant_orders.gift_card_amount": "currency", "merchant_orders.tax": "currency", "merchant_orders.shipping": "currency", "merchant_orders.refund_total": "currency", "merchant_refunds.amount": "primary",
	"series.amount": "currency", "series.override_next_amount": "currency", "series.match_amount_min": "currency", "series.match_amount_max": "currency",
	"spending_plan_months.calculated_rollover_amount": "primary", "spending_plan_months.calculated_income_amount": "primary", "spending_plan_months.overwritten_income_amount": "primary", "spending_plan_months.calculated_bills_amount": "primary", "spending_plan_months.overwritten_bills_amount": "primary", "spending_plan_months.calculated_subscriptions_amount": "primary", "spending_plan_months.overwritten_subscriptions_amount": "primary", "spending_plan_months.calculated_transfer_amount": "primary", "spending_plan_months.overwritten_transfer_amount": "primary", "spending_plan_months.calculated_goals_amount": "primary", "spending_plan_months.overwritten_goals_amount": "primary", "spending_plan_months.calculated_planned_spending_amount": "primary", "spending_plan_months.overwritten_planned_spending_amount": "primary", "spending_plan_months.calculated_spent_amount": "primary", "spending_plan_months.overwritten_spent_amount": "primary", "spending_plan_months.set_aside": "primary", "spending_plan_months.total_to_spend_amount": "primary", "spending_plan_months.left_to_spend_amount": "primary", "spending_plan_months.projected_other_spending": "primary", "spending_plan_months.projection_buffer": "primary",
	"transaction_splits.amount": "transactions:transaction_id", "transactions.amount": "currency", "transactions.amount_primary": "primary", "transactions.balance": "currency", "watchlists.target_amount": "primary",
}

func decimals(code string) (int, error)           { return minorunits.Decimals(code) }
func minor(text string, scale int) (int64, error) { return minorunits.Minor(text, scale) }
func (s *source) denomination(table, col string, r row, primary string) (string, error) {
	rule, ok := MoneyCurrencies[table+"."+col]
	if !ok {
		return "", fmt.Errorf("unmapped money column %s.%s", table, col)
	}
	switch rule {
	case "currency":
		return val(r, "currency"), nil
	case "primary":
		return primary, nil
	case "USD":
		return "USD", nil
	case "correction":
		if p := s.byID["transactions"][val(r, "transaction_id")]; p != nil {
			return val(p, "currency"), nil
		}
		return "", fmt.Errorf("assistant correction %s has an amount without a transaction currency", val(r, "id"))
	}
	parts := strings.Split(rule, ":")
	p := s.byID[parts[0]][val(r, parts[1])]
	if p == nil {
		return "", fmt.Errorf("missing currency parent %s for %s.%s id %s", rule, table, col, val(r, "id"))
	}
	return val(p, "currency"), nil
}
