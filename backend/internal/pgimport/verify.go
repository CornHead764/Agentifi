package pgimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/shopspring/decimal"
	"strconv"
	"strings"
	"time"
)

func identity(r row) string {
	for _, k := range []string{"id", "jti", "key", "transfer_pair_id"} {
		if r[k] != nil {
			return val(r, k)
		}
	}
	var parts []string
	for _, k := range []string{"merchant", "sku", "document_id", "kind", "target_id", "role", "goal_id", "account_id", "transaction_id", "order_id", "series_id", "subaccount_id", "split_id", "tag_id", "refund_txn_id", "charge_txn_id"} {
		if r[k] != nil {
			parts = append(parts, k+"="+val(r, k))
		}
	}
	return strings.Join(parts, "/")
}
func (s *source) verify(ctx context.Context, t *target, written map[string][]row) error {
	// Read PostgreSQL again under the same repeatable-read snapshot. The verifier
	// decodes stored values back to source units; it never calls convert or minor.
	fresh := map[string][]row{}
	for table := range t.tables {
		if table == "household_metadata" {
			// SQLite-only: derived from the space's primary currency, not a PostgreSQL table.
			fresh[table] = []row{{"id": ptr("1"), "primary_currency": ptr(t.primary)}}
			continue
		}
		pgtable := table
		if table == "households" {
			pgtable = "spaces"
		}
		rs, e := s.read(ctx, pgtable, t.id)
		if e != nil {
			return e
		}
		for _, r := range rs {
			if t.id == "" || s.belongs(table, r, t.id) {
				fresh[table] = append(fresh[table], r)
			}
		}
	}
	if t.id == "" {
		copySource := *s
		copySource.data = map[string][]row{}
		for k, v := range s.data {
			copySource.data[k] = v
		}
		for k, v := range fresh {
			if k == "households" {
				copySource.data["spaces"] = v
			} else {
				copySource.data[k] = v
			}
		}
		active := map[string]bool{}
		for _, r := range fresh["households"] {
			if val(r, "is_deleted") == "false" || val(r, "is_deleted") == "f" {
				active[val(r, "id")] = true
			}
		}
		var e error
		fresh, e = copySource.serverRows(active)
		if e != nil {
			return e
		}
	}
	for table, cols := range t.tables {
		expected := map[string]row{}
		for _, r := range fresh[table] {
			expected[identity(r)] = r
		}
		if table == "documents" {
			for id, r := range t.added {
				expected[id] = r
			}
		}
		var names []string
		for _, c := range cols {
			names = append(names, quote(c.name))
		}
		rs, e := t.db.Read().QueryContext(ctx, "SELECT "+strings.Join(names, ",")+" FROM "+quote(table))
		if e != nil {
			return e
		}
		count := 0
		pgSums := map[string]decimal.Decimal{}
		sqSums := map[string]decimal.Decimal{}
		for rs.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			actual := row{}
			for i := range values {
				dest[i] = &values[i]
			}
			if e = rs.Scan(dest...); e != nil {
				rs.Close()
				return e
			}
			for i, c := range cols {
				if values[i] != nil {
					actual[c.name] = ptr(fmt.Sprint(values[i]))
				}
			}
			id := identity(actual)
			r := expected[id]
			if r == nil {
				rs.Close()
				return fmt.Errorf("verify %s: unexpected row %s", table, id)
			}
			count++
			for i, c := range cols {
				want, got, e := s.compareColumn(t, table, c, r, values[i], written)
				if e != nil {
					rs.Close()
					return fmt.Errorf("verify %s.%s id %s: %w", table, c.name, id, e)
				}
				if want != got {
					rs.Close()
					return fmt.Errorf("verify %s.%s id %s: PostgreSQL %q != SQLite %q", table, c.name, id, want, got)
				}
				if _, ok := MoneyCurrencies[table+"."+c.name]; ok && r[c.name] != nil {
					code, e := s.denomination(table, c.name, r, t.primary)
					if e != nil {
						rs.Close()
						return e
					}
					key := c.name + "/" + code
					a, e := decimal.NewFromString(want)
					if e != nil {
						rs.Close()
						return e
					}
					b, e := decimal.NewFromString(got)
					if e != nil {
						rs.Close()
						return e
					}
					pgSums[key] = pgSums[key].Add(a)
					sqSums[key] = sqSums[key].Add(b)
				}
			}
		}
		e = rs.Err()
		rs.Close()
		if e != nil {
			return e
		}
		if count != len(expected) {
			return fmt.Errorf("verify %s row count: PostgreSQL %d SQLite %d", table, len(expected), count)
		}
		for k, v := range pgSums {
			if !v.Equal(sqSums[k]) {
				return fmt.Errorf("verify %s money sum %s differs", table, k)
			}
		}
	}
	rs, e := t.db.Read().QueryContext(ctx, "PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	has := rs.Next()
	rs.Close()
	if has {
		return fmt.Errorf("household %s foreign_key_check failed", t.id)
	}
	if t.id != "" {
		var tombs int
		if e = t.db.Read().QueryRowContext(ctx, "SELECT count(*) FROM tombstones").Scan(&tombs); e != nil {
			return e
		}
		if tombs != 0 {
			return fmt.Errorf("import created %d spurious tombstones", tombs)
		}
		var counter int64
		if e = t.db.Read().QueryRowContext(ctx, "SELECT seq FROM change_counter").Scan(&counter); e != nil {
			return e
		}
		for table := range t.tables {
			var captured int
			if e = t.db.Read().QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info(?) WHERE name='change_seq'", table).Scan(&captured); e != nil {
				return e
			}
			if captured == 0 {
				continue
			}
			var bad int
			if e = t.db.Read().QueryRowContext(ctx, "SELECT count(*) FROM "+quote(table)+" WHERE change_seq<=0 OR change_seq> ?", counter).Scan(&bad); e != nil {
				return e
			}
			if bad != 0 {
				return fmt.Errorf("%s has inconsistent change_seq", table)
			}
		}
	}
	return nil
}
func (s *source) compareColumn(t *target, table string, c column, r row, value any, written map[string][]row) (string, string, error) {
	name := c.name
	raw := r[name]
	pgtable := table
	if table == "households" {
		pgtable = "spaces"
	}
	kind := ""
	for _, col := range s.columns[pgtable] {
		if col.name == name {
			kind = col.kind
			break
		}
	}
	if name == "file_name" {
		raw = ptr("households/" + val(r, "id") + ".db")
	}
	if name == "household_id" {
		raw = r["space_id"]
	}
	if name == "price_currency" && r["price"] != nil {
		raw = ptr("USD")
	}
	if strings.HasSuffix(name, "_folded") {
		raw = ptr(folded(val(r, strings.TrimSuffix(name, "_folded"))))
	}
	if name == "storage_key" {
		raw = ptr("attachments/" + t.id + "/" + val(r, "content_sha256"))
	}
	if strings.HasSuffix(name, "_document_id") {
		old := strings.TrimSuffix(name, "_document_id")
		if r[old] != nil {
			b := []byte(val(r, old))
			if strings.HasSuffix(old, "screenshot") {
				var e error
				b, e = hex.DecodeString(strings.TrimPrefix(string(b), `\x`))
				if e != nil {
					return "", "", e
				}
			}
			hash := sha256.Sum256(b)
			for _, d := range written["documents"] {
				if val(d, "content_sha256") == hex.EncodeToString(hash[:]) {
					raw = d["id"]
					break
				}
			}
		}
	}
	if raw == nil && name == "updated_at" {
		raw = r["created_at"]
		if raw == nil {
			raw = s.parentTime(table, r)
		}
	}
	if raw == nil {
		if value == nil {
			return "<NULL>", "<NULL>", nil
		}
		return "<NULL>", fmt.Sprint(value), nil
	}
	if value == nil {
		return *raw, "<NULL>", nil
	}
	text := fmt.Sprint(value)
	if _, ok := MoneyCurrencies[table+"."+name]; ok {
		code, e := s.denomination(table, name, r, t.primary)
		if e != nil {
			return "", "", e
		}
		scale, e := decimals(code)
		if e != nil {
			return "", "", e
		}
		a, e := decimal.NewFromString(*raw)
		if e != nil {
			return "", "", e
		}
		b, e := decimal.NewFromString(text)
		if e != nil {
			return "", "", e
		}
		return a.String(), b.Shift(-int32(scale)).String(), nil
	}
	if name == "threshold_pct" || table == "accounts" && name == "interest_rate" {
		scale := int32(2)
		if name == "interest_rate" {
			scale = 10
		}
		a, e := decimal.NewFromString(*raw)
		if e != nil {
			return "", "", e
		}
		b, e := decimal.NewFromString(text)
		if e != nil {
			return "", "", e
		}
		return a.String(), b.Shift(-scale).String(), nil
	}
	switch {
	case strings.Contains(kind, "timestamp") || name == "updated_at" && kind == "":
		var parsed time.Time
		var e error
		for _, layout := range []string{"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999Z07"} {
			parsed, e = time.Parse(layout, *raw)
			if e == nil {
				break
			}
		}
		if e != nil {
			return "", "", e
		}
		ms, e := strconv.ParseInt(text, 10, 64)
		if e != nil {
			return "", "", e
		}
		return parsed.Truncate(time.Millisecond).UTC().Format(time.RFC3339Nano), time.UnixMilli(ms).UTC().Format(time.RFC3339Nano), nil
	case kind == "boolean":
		want := "0"
		if *raw == "true" || *raw == "t" {
			want = "1"
		}
		return want, text, nil
	case kind == "bytea":
		b, e := hex.DecodeString(strings.TrimPrefix(*raw, `\x`))
		if e != nil {
			return "", "", e
		}
		stored, ok := value.([]byte)
		if !ok || !bytes.Equal(b, stored) {
			return *raw, text, nil
		}
		return *raw, *raw, nil
	case strings.HasPrefix(kind, "numeric"):
		d, e := decimal.NewFromString(*raw)
		if e != nil {
			return "", "", e
		}
		return d.String(), text, nil
	case c.kind == "REAL":
		a, e := strconv.ParseFloat(*raw, 64)
		if e != nil {
			return "", "", e
		}
		b, e := strconv.ParseFloat(text, 64)
		return strconv.FormatFloat(a, 'g', -1, 64), strconv.FormatFloat(b, 'g', -1, 64), e
	default:
		return *raw, text, nil
	}
}
