package portcheck

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
)

type Difference struct {
	Path                        string
	Before, After               any
	BeforeMissing, AfterMissing bool
}

// Diff uses JSON pointers so request URLs and object keys remain unambiguous.
func Diff(a, b *Snapshot) []Difference {
	var left, right any
	encode := func(s *Snapshot) any {
		raw, _ := json.Marshal(s)
		var v any
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		_ = d.Decode(&v)
		return v
	}
	left, right = encode(a), encode(b)
	out := []Difference{}
	escape := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
	var walk func(string, any, any)
	walk = func(path string, a, b any) {
		if reflect.DeepEqual(a, b) || sameInstant(a, b) {
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			ordered := make([]string, 0, len(keys))
			for k := range keys {
				ordered = append(ordered, k)
			}
			sort.Strings(ordered)
			for _, k := range ordered {
				av, ao := am[k]
				bv, bo := bm[k]
				p := path + "/" + escape(k)
				if !ao || !bo {
					out = append(out, Difference{p, av, bv, !ao, !bo})
				} else {
					walk(p, av, bv)
				}
			}
			return
		}
		aa, aok := a.([]any)
		ba, bok := b.([]any)
		if aok && bok {
			n := max(len(aa), len(ba))
			for i := 0; i < n; i++ {
				p := fmt.Sprintf("%s/%d", path, i)
				if i >= len(aa) {
					out = append(out, Difference{p, nil, ba[i], true, false})
				} else if i >= len(ba) {
					out = append(out, Difference{p, aa[i], nil, false, true})
				} else {
					walk(p, aa[i], ba[i])
				}
			}
			return
		}
		out = append(out, Difference{Path: path, Before: a, After: b})
	}
	walk("", left, right)
	return out
}

// sameInstant treats two RFC 3339 timestamps as equal when they name the same
// millisecond. SQLite stores Unix milliseconds and renders UTC; PostgreSQL
// renders microseconds in the session zone. Neither is a behaviour change.
func sameInstant(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if !aok || !bok {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, as)
	if err != nil {
		return false
	}
	bt, err := time.Parse(time.RFC3339Nano, bs)
	if err != nil {
		return false
	}
	return at.Truncate(time.Millisecond).Equal(bt.Truncate(time.Millisecond))
}

func PrintDiff(w io.Writer, differences []Difference, limit int) {
	value := func(v any, missing bool) string {
		if missing {
			return "<missing>"
		}
		raw, _ := json.Marshal(v)
		s := string(raw)
		if len(s) > 500 {
			s = s[:500] + "..."
		}
		return s
	}
	for i, d := range differences {
		if i >= limit {
			break
		}
		fmt.Fprintf(w, "%s\n  before: %s\n  after:  %s\n", d.Path, value(d.Before, d.BeforeMissing), value(d.After, d.AfterMissing))
	}
	fmt.Fprintf(w, "%d differences", len(differences))
	if len(differences) > limit {
		fmt.Fprintf(w, " (showing first %d)", limit)
	}
	fmt.Fprintln(w)
}
