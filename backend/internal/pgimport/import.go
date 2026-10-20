package pgimport

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"golang.org/x/text/unicode/norm"
)

type Options struct {
	From, Data, StoragePath string
	Output                  io.Writer
}
type HouseholdSummary struct {
	ID          string
	Tables      map[string]int
	Attachments int
}
type Report struct {
	Households                        []HouseholdSummary
	DeletedSpaces, DeletedMemberships int
}
type target struct {
	db               *sqlitedb.DB
	tables           map[string][]column
	id, primary, dir string
	added            map[string]row
	attachments      int
}

func Import(ctx context.Context, o Options) (report Report, err error) {
	if o.From == "" || o.Data == "" {
		return report, errors.New("--from and --data are required")
	}
	if o.Output == nil {
		o.Output = io.Discard
	}
	if err = os.MkdirAll(o.Data, 0700); err != nil {
		return report, err
	}
	lock, err := sqlitedb.AcquireLock(filepath.Join(o.Data, "server.lock"))
	if err != nil {
		return report, err
	}
	defer lock.Close()
	for _, name := range []string{"server.db", "households", "attachments"} {
		if _, e := os.Lstat(filepath.Join(o.Data, name)); e == nil {
			return report, fmt.Errorf("refusing non-empty --data: %s already exists", name)
		} else if !os.IsNotExist(e) {
			return report, e
		}
	}
	tmp, err := os.MkdirTemp(o.Data, ".import-postgres-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(tmp)
	conn, err := pgx.Connect(ctx, o.From)
	if err != nil {
		return report, err
	}
	defer conn.Close(context.Background())
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return report, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SET LOCAL TIME ZONE 'UTC'"); err != nil {
		return report, err
	}
	s, err := loadSource(ctx, tx)
	if err != nil {
		return report, err
	}
	server, err := openTarget(ctx, filepath.Join(tmp, "server.db"), "", "", tmp)
	if err != nil {
		return report, err
	}
	defer server.db.Close()
	active := map[string]bool{}
	for _, r := range s.data["spaces"] {
		if val(r, "is_deleted") == "true" || val(r, "is_deleted") == "t" {
			report.DeletedSpaces++
			continue
		}
		active[val(r, "id")] = true
	}
	for _, r := range s.data["memberships"] {
		if !active[val(r, "space_id")] {
			report.DeletedMemberships++
		}
	}
	serverRows, err := s.serverRows(active)
	if err != nil {
		return report, err
	}
	if err = s.writeTables(ctx, server, serverRows); err != nil {
		return report, err
	}
	if err = s.verify(ctx, server, serverRows); err != nil {
		return report, err
	}
	if err = os.MkdirAll(filepath.Join(tmp, "households"), 0700); err != nil {
		return report, err
	}
	if err = os.MkdirAll(filepath.Join(tmp, "attachments"), 0700); err != nil {
		return report, err
	}
	for _, space := range s.data["spaces"] {
		id := val(space, "id")
		if !active[id] {
			continue
		}
		h, e := openTarget(ctx, filepath.Join(tmp, "households", id+".db"), id, val(space, "primary_currency"), tmp)
		if e != nil {
			return report, e
		}
		e = func() error {
			defer h.db.Close()
			rows := map[string][]row{}
			for table := range h.tables {
				for _, r := range s.data[table] {
					if s.belongs(table, r, id) {
						rows[table] = append(rows[table], r)
					}
				}
			}
			if e := s.attach(ctx, h, rows, o.StoragePath); e != nil {
				return e
			}
			if e := s.writeTables(ctx, h, rows); e != nil {
				return e
			}
			if e := s.verify(ctx, h, rows); e != nil {
				return e
			}
			summary := HouseholdSummary{ID: id, Tables: map[string]int{}, Attachments: h.attachments}
			var names []string
			for table := range h.tables {
				names = append(names, table)
			}
			sort.Strings(names)
			total := 0
			for _, table := range names {
				n := len(rows[table])
				summary.Tables[table] = n
				total += n
				fmt.Fprintf(o.Output, "%s %s: %d rows\n", id, table, n)
			}
			report.Households = append(report.Households, summary)
			fmt.Fprintf(o.Output, "household %s: %d tables, %d rows, %d attachments; verified counts, per-currency money sums, every column, foreign keys and change capture\n", id, len(names), total, h.attachments)
			return nil
		}()
		if e != nil {
			return report, e
		}
	}
	if err = server.db.Close(); err != nil {
		return report, err
	}
	// Publish only closed, verified databases. Roll back the publication on any rename failure.
	var published []string
	for _, name := range []string{"server.db", "households", "attachments"} {
		if err = os.Rename(filepath.Join(tmp, name), filepath.Join(o.Data, name)); err != nil {
			for _, p := range published {
				_ = os.RemoveAll(filepath.Join(o.Data, p))
			}
			return report, err
		}
		published = append(published, name)
	}
	fmt.Fprintf(o.Output, "skipped %d deleted spaces and %d memberships; server verified\n", report.DeletedSpaces, report.DeletedMemberships)
	return report, nil
}
func openTarget(ctx context.Context, path, id, primary, dir string) (*target, error) {
	d, err := sqlitedb.Open(ctx, path, time.Now)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			d.Close()
		}
	}()
	if id == "" {
		_, err = d.MigrateServer(ctx, "")
	} else {
		_, err = d.MigrateHousehold(ctx, "")
	}
	if err != nil {
		return nil, err
	}
	t := &target{db: d, id: id, primary: primary, dir: dir, tables: map[string][]column{}, added: map[string]row{}}
	rs, err := d.Read().QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT IN ('goose_db_version','change_counter','tombstones') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rs.Next() {
		var n string
		if err = rs.Scan(&n); err != nil {
			rs.Close()
			return nil, err
		}
		names = append(names, n)
	}
	err = rs.Err()
	rs.Close()
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		rs, err := d.Read().QueryContext(ctx, "PRAGMA table_info("+quote(n)+")")
		if err != nil {
			return nil, err
		}
		for rs.Next() {
			var cid, notnull, pk int
			var name, kind string
			var def any
			if err = rs.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
				rs.Close()
				return nil, err
			}
			if name != "change_seq" {
				t.tables[n] = append(t.tables[n], column{name, kind})
			}
		}
		err = rs.Err()
		rs.Close()
		if err != nil {
			return nil, err
		}
	}
	ok = true
	return t, nil
}
func (s *source) serverRows(active map[string]bool) (map[string][]row, error) {
	out := map[string][]row{}
	for _, table := range []string{"users", "passkeys", "recovery_codes", "revoked_tokens", "server_settings", "backup_runs", "merchant_catalog"} {
		out[table] = s.data[table]
	}
	for _, r := range s.data["spaces"] {
		if active[val(r, "id")] {
			out["households"] = append(out["households"], r)
		}
	}
	for _, r := range s.data["memberships"] {
		if active[val(r, "space_id")] {
			out["memberships"] = append(out["memberships"], r)
		}
	}
	for _, table := range []string{"fx_rates", "security_prices"} {
		seen := map[string]row{}
		for _, r := range s.data[table] {
			if !active[val(r, "space_id")] {
				continue
			}
			key := val(r, "base_currency") + "/" + val(r, "quote_currency") + "/" + val(r, "date")
			value := val(r, "rate")
			if table == "security_prices" {
				p := s.byID["securities"][val(r, "security_id")]
				if p == nil {
					return nil, fmt.Errorf("security_prices missing security %s", val(r, "security_id"))
				}
				r["symbol"] = p["symbol"]
				r["currency"] = p["currency"]
				key = val(r, "symbol") + "/" + val(r, "on_date")
				value = val(r, "close")
			}
			if prev := seen[key]; prev != nil {
				old := val(prev, "rate")
				if table == "security_prices" {
					old = val(prev, "close")
				}
				a, e := decimal.NewFromString(old)
				if e != nil {
					return nil, e
				}
				b, e := decimal.NewFromString(value)
				if e != nil {
					return nil, e
				}
				if !a.Equal(b) {
					return nil, fmt.Errorf("%s conflict %s: spaces %s and %s disagree (%s versus %s)", table, key, val(prev, "space_id"), val(r, "space_id"), old, value)
				}
				if table == "security_prices" {
					// Row IDs and tenant-local security IDs differ by construction;
					// every other source column must agree before sharing a quote.
					for _, name := range []string{"currency", "created_at"} {
						if val(prev, name) != val(r, name) {
							return nil, fmt.Errorf("%s conflict %s: spaces %s and %s disagree (%s %s versus %s)", table, key, val(prev, "space_id"), val(r, "space_id"), name, val(prev, name), val(r, name))
						}
					}
				}
				continue
			}
			seen[key] = r
			out[table] = append(out[table], r)
		}
	}
	return out, nil
}
func folded(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.TrimSpace(s)) {
		if !unicode.Is(unicode.Diacritic, r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
func timestamp(s string) (int64, error) {
	for _, layout := range []string{"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999Z07", "2006-01-02T15:04:05.999999999Z07:00"} {
		if t, e := time.Parse(layout, s); e == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("invalid timestamp %q", s)
}
func (s *source) convert(t *target, table string, c column, r row) (any, error) {
	name := c.name
	sourceTable := table
	if table == "households" {
		sourceTable = "spaces"
	}
	if name == "file_name" {
		return "households/" + val(r, "id") + ".db", nil
	}
	if name == "household_id" {
		return val(r, "space_id"), nil
	}
	if name == "price_currency" {
		if r["price"] != nil {
			return "USD", nil
		}
		return nil, nil
	}
	if strings.HasSuffix(name, "_folded") {
		return folded(val(r, strings.TrimSuffix(name, "_folded"))), nil
	}
	v := r[name]
	if v == nil && name == "updated_at" {
		v = r["created_at"]
		if v == nil {
			v = s.parentTime(table, r)
		}
	}
	if v == nil {
		return nil, nil
	}
	kind := ""
	for _, sc := range s.columns[sourceTable] {
		if sc.name == name {
			kind = sc.kind
			break
		}
	}
	if rule, ok := MoneyCurrencies[table+"."+name]; ok {
		_ = rule
		code, e := s.denomination(table, name, r, t.primary)
		if e != nil {
			return nil, e
		}
		scale, e := decimals(code)
		if e != nil {
			return nil, e
		}
		return minor(*v, scale)
	}
	if name == "threshold_pct" {
		return minor(*v, 2)
	}
	if table == "accounts" && name == "interest_rate" {
		return minor(*v, 10)
	}
	switch {
	case strings.Contains(kind, "timestamp") || name == "updated_at" && kind == "":
		return timestamp(*v)
	case kind == "boolean":
		if *v == "true" || *v == "t" {
			return int64(1), nil
		}
		return int64(0), nil
	case kind == "bytea":
		return hex.DecodeString(strings.TrimPrefix(*v, `\x`))
	case strings.HasPrefix(kind, "numeric"):
		d, e := decimal.NewFromString(*v)
		if e != nil {
			return nil, e
		}
		return d.String(), nil
	case c.kind == "INTEGER":
		return strconv.ParseInt(*v, 10, 64)
	case c.kind == "REAL":
		return strconv.ParseFloat(*v, 64)
	default:
		return *v, nil
	}
}
func (s *source) parentTime(table string, r row) *string {
	for _, c := range s.columns[table] {
		if strings.HasSuffix(c.name, "_id") {
			parent := strings.TrimSuffix(c.name, "_id") + "s"
			if p := s.byID[parent][val(r, c.name)]; p != nil {
				if p["updated_at"] != nil {
					return p["updated_at"]
				}
				if p["created_at"] != nil {
					return p["created_at"]
				}
			}
		}
	}
	return nil
}
func (s *source) writeTables(ctx context.Context, t *target, data map[string][]row) error {
	// Cyclic account/bill links cannot be ordered; re-enable enforcement and check
	// every edge before publishing, rather than silently dropping either side.
	if _, e := t.db.Write().ExecContext(ctx, "PRAGMA foreign_keys=OFF"); e != nil {
		return e
	}
	names := dependencyOrder(t)
	for _, table := range names {
		cols := t.tables[table]
		var quoted, marks []string
		for _, c := range cols {
			quoted = append(quoted, quote(c.name))
			marks = append(marks, "?")
		}
		q := "INSERT INTO " + quote(table) + " (" + strings.Join(quoted, ",") + ") VALUES (" + strings.Join(marks, ",") + ")"
		rows := data[table]
		for start := 0; start < len(rows); start += 1000 {
			end := min(start+1000, len(rows))
			if e := t.db.InTx(ctx, func(d *sqlitedb.DB) error {
				for _, r := range rows[start:end] {
					args := make([]any, len(cols))
					for i, c := range cols {
						v, e := s.convert(t, table, c, r)
						if e != nil {
							return fmt.Errorf("%s.%s id %s: %w", table, c.name, val(r, "id"), e)
						}
						args[i] = v
					}
					if _, e := d.Write().ExecContext(ctx, q, args...); e != nil {
						return fmt.Errorf("insert %s id %s: %w", table, val(r, "id"), e)
					}
				}
				return nil
			}); e != nil {
				return e
			}
		}
	}
	if _, e := t.db.Write().ExecContext(ctx, "PRAGMA foreign_keys=ON"); e != nil {
		return e
	}
	var on int
	if e := t.db.Write().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&on); e != nil {
		return e
	}
	if on != 1 {
		return errors.New("foreign_keys did not enable")
	}
	return nil
}
func dependencyOrder(t *target) []string {
	var names []string
	for n := range t.tables {
		names = append(names, n)
	}
	sort.Strings(names)
	done := map[string]bool{}
	visiting := map[string]bool{}
	var out []string
	var visit func(string)
	visit = func(n string) {
		if done[n] || visiting[n] {
			return
		}
		visiting[n] = true
		rs, e := t.db.Read().QueryContext(context.Background(), "PRAGMA foreign_key_list("+quote(n)+")")
		var parents []string
		if e == nil {
			for rs.Next() {
				var id, seq int
				var parent, from, to, onupdate, ondelete, match string
				if rs.Scan(&id, &seq, &parent, &from, &to, &onupdate, &ondelete, &match) == nil {
					parents = append(parents, parent)
				}
			}
			rs.Close()
		}
		sort.Strings(parents)
		for _, p := range parents {
			if _, ok := t.tables[p]; ok {
				visit(p)
			}
		}
		done[n] = true
		out = append(out, n)
	}
	for _, n := range names {
		visit(n)
	}
	return out
}
