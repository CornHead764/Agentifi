// Command jetgen writes go-jet's table and model packages for the household
// and server schemas into <out>/household and <out>/server. Each schema is
// built from the embedded migrations through sqlitedb, Go migrations
// included, and read back through the same pure-Go driver, so generating
// needs neither cgo nor an installed jet CLI.
//
// It removes <out> before writing, and refuses an <out> holding anything it
// did not write.
//
//	go run ./cmd/jetgen internal/gen/jet
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-jet/jet/v2/generator/metadata"
	jetsqlite "github.com/go-jet/jet/v2/generator/sqlite"
	"github.com/go-jet/jet/v2/generator/template"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: jetgen <out>")
		os.Exit(2)
	}
	if err := generate(context.Background(), os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// The migrations stamp nothing that reaches the schema with the clock, but a
// fixed one keeps the build independent of when it runs.
func clock() time.Time { return time.Unix(0, 0).UTC() }

type schema struct {
	name  string
	build func(ctx context.Context, path string) error
}

var schemas = []schema{
	{"household", func(ctx context.Context, path string) error {
		db, err := sqlitedb.OpenNewHousehold(ctx, path, clock)
		if err != nil {
			return err
		}
		return db.Close()
	}},
	{"server", func(ctx context.Context, path string) error {
		db, err := sqlitedb.Open(ctx, path, clock)
		if err != nil {
			return err
		}
		_, err = db.MigrateServer(ctx, "")
		return errors.Join(err, db.Close())
	}},
}

func generate(ctx context.Context, out string) error {
	if err := owned(out); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "jetgen-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	unclaimed := maps.Clone(money)
	for _, s := range schemas {
		path := filepath.Join(dir, s.name+".db")
		if err := s.build(ctx, path); err != nil {
			return fmt.Errorf("jetgen: building the %s schema: %w", s.name, err)
		}
		if err := generateSchema(path, filepath.Join(out, s.name), unclaimed); err != nil {
			return fmt.Errorf("jetgen: %s: %w", s.name, err)
		}
	}
	if len(unclaimed) > 0 {
		return fmt.Errorf("jetgen: money columns in neither schema: %s", strings.Join(slices.Sorted(maps.Keys(unclaimed)), ", "))
	}
	return nil
}

// owned refuses to remove a directory that holds anything but a previous run's
// output, so a mistyped <out> cannot take a source tree with it.
func owned(out string) error {
	entries, err := os.ReadDir(out)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !slices.ContainsFunc(schemas, func(s schema) bool { return s.name == entry.Name() }) {
			return fmt.Errorf("jetgen: %s holds %s, which jetgen did not write; refusing to remove it", out, entry.Name())
		}
	}
	return nil
}

func generateSchema(path, out string, unclaimed map[string]bool) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	kinds, err := columnKinds(db, unclaimed)
	if err != nil {
		return err
	}
	return jetsqlite.GenerateDB(db, out, generatorTemplate(kinds))
}

type kind int

const (
	plain kind = iota
	uuidColumn
	flag
	amount
)

// money is every column holding integer minor units: each of
// pgimport.MoneyCurrencies, which a test checks, and any added since.
var money = map[string]bool{}

func init() {
	for _, column := range strings.Fields(`
		accounts.provider_balance accounts.opening_balance accounts.goal_balance accounts.pending_holds
		accounts.credit_limit accounts.statement_balance accounts.minimum_due accounts.withheld_balance
		accounts.hide_below_balance
		alert_rules.threshold_amount assistant_corrections.amount
		balance_snapshots.balance balance_snapshots.balance_primary balance_snapshots.anchor_balance
		bill_payments.amount bills.amount_due bills.minimum_due
		envelopes.target_amount envelopes.overwritten_target_amount envelopes.calculated_spent_amount
		envelopes.rollover_amount
		filter_items.amount_min filter_items.amount_max
		goals.target_amount goals.contribution_amount goals.contributed_this_month goals.saved_so_far goals.spent
		holdings.cost_basis holdings.market_value institutions.hide_below_balance
		merchant_accounts.gift_card_balance merchant_catalog.price merchant_charges.amount merchant_matches.amount
		merchant_order_items.unit_price merchant_order_items.total_owed
		merchant_orders.total merchant_orders.gift_card_amount merchant_orders.tax merchant_orders.shipping
		merchant_orders.refund_total merchant_refunds.amount
		series.amount series.override_next_amount series.match_amount_min series.match_amount_max
		spending_plan_months.calculated_rollover_amount
		spending_plan_months.calculated_income_amount spending_plan_months.overwritten_income_amount
		spending_plan_months.calculated_bills_amount spending_plan_months.overwritten_bills_amount
		spending_plan_months.calculated_subscriptions_amount spending_plan_months.overwritten_subscriptions_amount
		spending_plan_months.calculated_transfer_amount spending_plan_months.overwritten_transfer_amount
		spending_plan_months.calculated_goals_amount spending_plan_months.overwritten_goals_amount
		spending_plan_months.calculated_planned_spending_amount spending_plan_months.overwritten_planned_spending_amount
		spending_plan_months.calculated_spent_amount spending_plan_months.overwritten_spent_amount
		spending_plan_months.set_aside spending_plan_months.total_to_spend_amount
		spending_plan_months.left_to_spend_amount spending_plan_months.projected_other_spending
		spending_plan_months.projection_buffer
		transaction_splits.amount transactions.amount transactions.amount_primary transactions.balance
		watchlists.target_amount`) {
		money[column] = true
	}
}

// A TEXT column holds a uuid when it is named id or *_id, or is one of
// alsoUUID, unless notUUID names it bare or as table.column.
var (
	notUUID = map[string]bool{
		"external_id": true, "simplefin_account_id": true, "known_category_id": true,
		"txf_id": true, "profile_id": true, "message_id": true, "client_id": true,
		"passkeys.rp_id":                       true, // the WebAuthn relying party, a host name
		"tombstones.row_id":                    true, // a composite key is a JSON array
		"assistant_automation_runs.decided_by": true, // who decided: "model", "rule", …
	}
	alsoUUID = map[string]bool{
		"created_by": true, "corrected_by": true, "decided_by": true, "household_uuid": true,
	}
)

func isUUID(table, column, typ string) bool {
	return typ == "TEXT" && !notUUID[column] && !notUUID[table+"."+column] &&
		(column == "id" || strings.HasSuffix(column, "_id") || alsoUUID[column])
}

// pragma_table_info does not report CHECK constraints, so flags are read
// from the DDL; every 0/1 constraint in the schema is written this way.
var flagCheck = regexp.MustCompile(`(?m)^\s*(\w+) INTEGER\b.*\bCHECK \((\w+) IN \(0, 1\)\)`)

// columnKinds maps table.column to its kind for every column whose Go type
// is not jet's default. A money column found here is removed from unclaimed.
func columnKinds(db *sql.DB, unclaimed map[string]bool) (map[string]kind, error) {
	kinds := map[string]kind{}
	rows, err := db.Query(`
		SELECT m.name, m.sql, p.name, p.type
		FROM sqlite_schema m, pragma_table_info(m.name) p
		WHERE m.type = 'table'
		ORDER BY m.name, p.cid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parsed := map[string]bool{}
	for rows.Next() {
		var table, ddl, column, typ string
		if err := rows.Scan(&table, &ddl, &column, &typ); err != nil {
			return nil, err
		}
		if !parsed[table] {
			parsed[table] = true
			for _, m := range flagCheck.FindAllStringSubmatch(ddl, -1) {
				if m[1] == m[2] {
					kinds[table+"."+m[1]] = flag
				}
			}
		}
		key := table + "." + column
		switch {
		case money[key]:
			if typ != "INTEGER" {
				return nil, fmt.Errorf("money column %s is %s, not INTEGER", key, typ)
			}
			kinds[key] = amount
			delete(unclaimed, key)
		case isUUID(table, column, typ):
			kinds[key] = uuidColumn
		}
	}
	return kinds, rows.Err()
}

var (
	uuidType   = template.NewType(uuid.UUID{})
	boolType   = template.NewType(true)
	amountType = template.Type{
		ImportPath: "github.com/CornHead764/agentifi/backend/internal/store/minor",
		Name:       "minor.Amount",
	}
)

func generatorTemplate(kinds map[string]kind) template.Template {
	return template.Default(sqlite.Dialect).UseSchema(func(s metadata.Schema) template.Schema {
		return template.DefaultSchema(s).
			UseModel(template.DefaultModel().UseTable(func(t metadata.Table) template.TableModel {
				model := template.DefaultTableModel(t).UseField(func(c metadata.Column) template.TableModelField {
					field := template.DefaultTableModelField(c)
					var typ template.Type
					switch kinds[t.Name+"."+c.Name] {
					case uuidColumn:
						typ = uuidType
					case flag:
						typ = boolType
					case amount:
						typ = amountType
					default:
						return field
					}
					if c.IsNullable {
						typ.Name = "*" + typ.Name
					}
					return field.UseType(typ)
				})
				model.Skip = t.Name == "goose_db_version"
				return model
			})).
			UseSQLBuilder(template.DefaultSQLBuilder().UseTable(func(t metadata.Table) template.TableSQLBuilder {
				builder := template.DefaultTableSQLBuilder(t).UseColumn(func(c metadata.Column) template.TableSQLBuilderColumn {
					column := template.DefaultTableSQLBuilderColumn(c)
					if kinds[t.Name+"."+c.Name] == flag {
						column.Type = "Bool"
					}
					return column
				})
				builder.Skip = t.Name == "goose_db_version"
				return builder
			}))
	})
}
