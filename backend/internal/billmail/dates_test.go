package billmail

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestParseDate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want domain.Date
		ok   bool
	}{
		{"04/21/2026", domain.NewDate(2026, time.April, 21), true},
		{"4/5/2026", domain.NewDate(2026, time.April, 5), true},
		{"04-15-2026", domain.NewDate(2026, time.April, 15), true},
		{"October 3, 2026", domain.NewDate(2026, time.October, 3), true},
		{"Oct 3, 2026", domain.NewDate(2026, time.October, 3), true},
		{"Oct 3 2026", domain.NewDate(2026, time.October, 3), true},
		{"Oct 1st, 2026", domain.NewDate(2026, time.October, 1), true},
		{"2026-10-03", domain.NewDate(2026, time.October, 3), true},
		{"04/21/2026  Total Due  $1,200.00", domain.NewDate(2026, time.April, 21), true},
		{"no day here", domain.Date{}, false},
		{"13/45/2026", domain.Date{}, false},
	} {
		got, ok := ParseDate(tc.in)
		if ok != tc.ok {
			t.Fatalf("ParseDate(%q) ok = %v, want %v", tc.in, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Fatalf("ParseDate(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestFindAfterLabel(t *testing.T) {
	text := "Policy  Auto (Q00-0001234)  Due Date  04/21/2026  Total Due  $1,200.00  VIEW INVOICE\n" +
		"Account Number:\n\nEnding in 1234\nOverdue  nothing\n"

	for _, tc := range []struct{ label, want string }{
		{"Total Due", "$1,200.00  VIEW INVOICE"},
		{"Due Date", "04/21/2026  Total Due  $1,200.00  VIEW INVOICE"},
		{"Account Number", "Ending in 1234"},
		{"Statement Amount", ""},
		{"due", "Date  04/21/2026  Total Due  $1,200.00  VIEW INVOICE"},
	} {
		if got := findAfterLabel(text, tc.label); got != tc.want {
			t.Fatalf("findAfterLabel(%q) = %q, want %q", tc.label, got, tc.want)
		}
	}
}
