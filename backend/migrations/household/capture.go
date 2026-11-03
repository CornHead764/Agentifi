package household

import (
	"bytes"
	"strings"
	"text/template"
)

// SyncTable describes a client-visible household table. Composite row ids are
// canonical JSON arrays of primary-key values in the listed order.
type SyncTable struct {
	Name         string
	Keys         []string
	HasUpdatedAt bool
}

// CaptureTables is the change-capture manifest as migration 2 installs it;
// IDListTables adds the tables of migration 4. Excluded tables hold
// credentials/sessions (connections, bill_connections, email_connections,
// merchant_accounts, assistant_connections, push_subscriptions), operational
// queues/challenges/mail/cache (bill_challenges, bill_emails, cash_flow_forecasts,
// category_suggestion_batches), or assistant execution internals
// (assistant_actions, assistant_automation_runs, assistant_corrections,
// assistant_messages). ignored_remote_accounts is connector-internal state.
// User-facing conversation metadata, automation definitions, financial data,
// documents and relationship tables do sync. Originally timeless relationships
// gain a nullable updated_at, managed by callers just like other timestamps.
// Capture only bumps sequences; deletion timestamps use the injected DB clock.
var CaptureTables = []SyncTable{
	{Name: "accounts", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "alert_rules", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "assistant_automations", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "assistant_conversations", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "assistant_guidance", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "balance_snapshots", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "bill_payments", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "bill_subaccounts", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "bills", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "categories", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "document_links", Keys: []string{"document_id", "kind", "target_id"}, HasUpdatedAt: false},
	{Name: "documents", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "envelopes", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "filter_items", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "filters", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "goal_funding_accounts", Keys: []string{"goal_id", "account_id"}, HasUpdatedAt: false},
	{Name: "goals", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "household_metadata", Keys: []string{"id"}, HasUpdatedAt: false},
	{Name: "holdings", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "institutions", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "mail_rules", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "manual_transfer_pairs", Keys: []string{"transfer_pair_id"}, HasUpdatedAt: false},
	{Name: "merchant_charges", Keys: []string{"id"}, HasUpdatedAt: false},
	{Name: "merchant_matches", Keys: []string{"transaction_id", "order_id"}, HasUpdatedAt: false},
	{Name: "merchant_order_items", Keys: []string{"id"}, HasUpdatedAt: false},
	{Name: "merchant_orders", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "merchant_refunds", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "notifications", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "rules", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "securities", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "series", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "series_bill_links", Keys: []string{"series_id"}, HasUpdatedAt: false},
	{Name: "spending_plan_months", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "split_tags", Keys: []string{"split_id", "tag_id"}, HasUpdatedAt: false},
	{Name: "suggestion_dismissals", Keys: []string{"id"}, HasUpdatedAt: false},
	{Name: "tags", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "transaction_refund_links", Keys: []string{"refund_txn_id", "charge_txn_id"}, HasUpdatedAt: false},
	{Name: "transaction_splits", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "transaction_tags", Keys: []string{"transaction_id", "tag_id"}, HasUpdatedAt: false},
	{Name: "transactions", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "watchlists", Keys: []string{"id"}, HasUpdatedAt: true},
	{Name: "duplicate_candidates", Keys: []string{"id"}, HasUpdatedAt: false},
}

const captureTemplate = `
CREATE INDEX IF NOT EXISTS {{.Name}}_change_seq ON {{.Name}} (change_seq);
CREATE TRIGGER IF NOT EXISTS {{.Name}}_ai AFTER INSERT ON {{.Name}} BEGIN
    UPDATE change_counter SET seq = seq + 1 WHERE id = 1;
    UPDATE {{.Name}} SET change_seq = (SELECT seq FROM change_counter) WHERE {{.Predicate}};
END;
CREATE TRIGGER IF NOT EXISTS {{.Name}}_au AFTER UPDATE ON {{.Name}}
WHEN NEW.change_seq = OLD.change_seq BEGIN
    UPDATE change_counter SET seq = seq + 1 WHERE id = 1;
    UPDATE {{.Name}} SET change_seq = (SELECT seq FROM change_counter)
    WHERE {{.Predicate}};
END;
CREATE TRIGGER IF NOT EXISTS {{.Name}}_ad AFTER DELETE ON {{.Name}} BEGIN
    UPDATE change_counter SET seq = seq + 1 WHERE id = 1;
    INSERT INTO tombstones VALUES ((SELECT seq FROM change_counter), '{{.Name}}', {{.RowID}},
        agentifi_now((SELECT file FROM pragma_database_list WHERE name = 'main')));
END;
`

// ChangeCaptureSQL installs change-capture indexes and triggers after migration
// has added the columns. It is safe to apply more than once.
func ChangeCaptureSQL(tables []SyncTable) string {
	tmpl := template.Must(template.New("capture").Parse(captureTemplate))
	var out bytes.Buffer
	for _, table := range tables {
		predicates := make([]string, len(table.Keys))
		oldKeys := make([]string, len(table.Keys))
		for i, key := range table.Keys {
			predicates[i] = key + " = NEW." + key
			oldKeys[i] = "OLD." + key
		}
		rowID := oldKeys[0]
		if len(oldKeys) > 1 {
			rowID = "json_array(" + strings.Join(oldKeys, ", ") + ")"
		}
		data := struct {
			SyncTable
			Predicate, RowID string
		}{table, strings.Join(predicates, " AND "), rowID}
		if err := tmpl.Execute(&out, data); err != nil {
			panic(err)
		}
	}
	return out.String()
}
