// Package pgimport performs an offline, verified PostgreSQL-to-SQLite cutover.
package pgimport

import (
	"context"
	"fmt"
	"github.com/CornHead764/agentifi/backend/migrations"
	"github.com/jackc/pgx/v5"
	"strconv"
	"strings"
	"time"
)

// HasData probes the legacy source without changing it. A configured source
// must be reachable: otherwise creating server.db could orphan an installation
// whose Postgres is still starting. Retry briefly before failing closed.
func HasData(ctx context.Context, dsn string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return false, fmt.Errorf("invalid DATABASE_URL; correct it or unset DATABASE_URL for a fresh installation")
	}
	cfg.ConnectTimeout = 2 * time.Second
	var conn *pgx.Conn
	for {
		conn, err = pgx.ConnectConfig(ctx, cfg)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("cannot reach legacy PostgreSQL host %q; start Postgres or unset DATABASE_URL for a fresh installation: %w", cfg.Host, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	queryCtx, queryCancel := context.WithTimeout(ctx, 3*time.Second)
	defer queryCancel()
	ctx = queryCtx
	defer conn.Close(context.Background())
	for _, table := range []string{"users", "spaces"} {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=$1)`, table).Scan(&exists); err != nil {
			return false, fmt.Errorf("inspect legacy PostgreSQL: %w", err)
		}
		if !exists {
			continue
		}
		var data bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+quote(table)+")").Scan(&data); err != nil {
			return false, fmt.Errorf("inspect legacy PostgreSQL %s: %w", table, err)
		}
		if data {
			return true, nil
		}
	}
	return false, nil
}

type row map[string]*string
type column struct{ name, kind string }
type source struct {
	tx      pgx.Tx
	columns map[string][]column
	data    map[string][]row
	byID    map[string]map[string]row
}

func val(r row, k string) string {
	if r[k] == nil {
		return ""
	}
	return *r[k]
}
func ptr(s string) *string  { return &s }
func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func lastMigration() (int, error) {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return 0, err
	}
	last := 0
	for _, e := range entries {
		n, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		v, err := strconv.Atoi(n)
		if err == nil && v > last {
			last = v
		}
	}
	return last, nil
}
func loadSource(ctx context.Context, tx pgx.Tx) (*source, error) {
	last, err := lastMigration()
	if err != nil {
		return nil, err
	}
	var version int
	if err = tx.QueryRow(ctx, "SELECT coalesce(max(version_id),0) FROM goose_db_version WHERE is_applied").Scan(&version); err != nil {
		return nil, fmt.Errorf("source migration: %w", err)
	}
	if version != last {
		return nil, fmt.Errorf("source migration is %d; required last embedded PostgreSQL migration is %d", version, last)
	}
	s := &source{tx: tx, columns: map[string][]column{}, data: map[string][]row{}, byID: map[string]map[string]row{}}
	rs, err := tx.Query(ctx, `SELECT c.relname,a.attname,format_type(a.atttypid,a.atttypmod) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid WHERE n.nspname=current_schema() AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped AND c.relname<>'goose_db_version' ORDER BY c.relname,a.attnum`)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var t, n, k string
		if err = rs.Scan(&t, &n, &k); err != nil {
			rs.Close()
			return nil, err
		}
		s.columns[t] = append(s.columns[t], column{n, k})
	}
	err = rs.Err()
	rs.Close()
	if err != nil {
		return nil, err
	}
	for t := range s.columns {
		rows, err := s.read(ctx, t, "")
		if err != nil {
			return nil, err
		}
		s.data[t] = rows
		s.byID[t] = map[string]row{}
		for _, r := range rows {
			s.byID[t][val(r, "id")] = r
		}
	}
	return s, nil
}
func (s *source) read(ctx context.Context, table, household string) ([]row, error) {
	var expressions []string
	tenant := false
	for _, c := range s.columns[table] {
		e := quote(c.name) + "::text"
		if strings.HasSuffix(c.kind, "[]") {
			e = "array_to_json(" + quote(c.name) + ")::text"
		}
		expressions = append(expressions, e)
		if c.name == "space_id" {
			tenant = true
		}
	}
	q := "SELECT " + strings.Join(expressions, ",") + " FROM " + quote(table)
	var args []any
	if household != "" && tenant {
		q += " WHERE space_id=$1"
		args = append(args, household)
	}
	// Physical order, so SQLite rowids keep PostgreSQL's insertion order for
	// queries (goal funding accounts) whose output order is that order.
	q += " ORDER BY ctid"
	rs, err := s.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		values := make([]*string, len(expressions))
		dest := make([]any, len(values))
		for i := range dest {
			dest[i] = &values[i]
		}
		if err = rs.Scan(dest...); err != nil {
			return nil, err
		}
		r := row{}
		for i, c := range s.columns[table] {
			r[c.name] = values[i]
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func (s *source) belongs(table string, r row, id string) bool {
	if r["space_id"] != nil {
		return val(r, "space_id") == id
	}
	var parent, key string
	switch table {
	case "goal_funding_accounts":
		parent, key = "goals", "goal_id"
	case "split_tags":
		parent, key = "transaction_splits", "split_id"
	case "transaction_tags":
		parent, key = "transactions", "transaction_id"
	default:
		return false
	}
	return s.belongs(parent, s.byID[parent][val(r, key)], id)
}
