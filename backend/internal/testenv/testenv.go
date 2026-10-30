// Package testenv decides what a test does when something it needs is absent.
// An optional suite is off unless asked for, and its skip says which suite it
// is, so a run can count them apart from the ordinary tests. A prerequisite
// of the ordinary suite fails under CI and skips elsewhere.
package testenv

import (
	"fmt"
	"os"
	"testing"
)

// Prefix opens every optional suite's skip message.
const Prefix = "optional suite"

// Browser skips unless AGENTIFI_BROWSER_TEST=1: the test drives a real
// Google Chrome against pages it serves itself.
func Browser(t testing.TB) {
	t.Helper()
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skipf("%s browser: set AGENTIFI_BROWSER_TEST=1 to drive a real Google Chrome", Prefix)
	}
}

// Live returns the variable that names what a test reaches on a real site,
// and skips when it is unset.
func Live(t testing.TB, variable, what string) string {
	t.Helper()
	value := os.Getenv(variable)
	if value == "" {
		t.Skipf("%s live: set %s to %s", Prefix, variable, what)
	}
	return value
}

// Golden skips with why: the golden suite needs a real Simplifi export, which
// never enters the repository.
func Golden(t testing.TB, why string) {
	t.Helper()
	t.Skipf("%s golden: %s", Prefix, why)
}

// Missing is a prerequisite the ordinary suite needs and does not have. Under
// CI it fails, so a run cannot pass by leaving tests out; elsewhere it skips.
func Missing(t testing.TB, format string, args ...any) {
	t.Helper()
	reason := fmt.Sprintf(format, args...)
	if InCI() {
		t.Fatalf("missing prerequisite: %s", reason)
	} else {
		t.Skipf("missing prerequisite: %s (fails when CI is set)", reason)
	}
}

// InCI is whether CI is set, as GitHub Actions sets it.
func InCI() bool {
	switch os.Getenv("CI") {
	case "", "0", "false":
		return false
	}
	return true
}
