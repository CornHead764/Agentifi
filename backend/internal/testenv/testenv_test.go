package testenv

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// outcome records what a helper did to the test it was handed.
type outcome struct {
	testing.TB
	failed, skipped string
}

func (o *outcome) Helper() {}

func (o *outcome) Fatalf(format string, args ...any) { o.failed = fmt.Sprintf(format, args...) }

func (o *outcome) Skipf(format string, args ...any) { o.skipped = fmt.Sprintf(format, args...) }

func TestAMissingPrerequisiteFailsUnderCI(t *testing.T) {
	for _, ci := range []string{"1", "true"} {
		t.Run(ci, func(t *testing.T) {
			t.Setenv("CI", ci)
			got := &outcome{}
			Missing(got, "no %s", "database")
			require.Equal(t, "missing prerequisite: no database", got.failed)
			require.Empty(t, got.skipped)
		})
	}
}

func TestAMissingPrerequisiteSkipsOutsideCI(t *testing.T) {
	for _, ci := range []string{"", "0", "false"} {
		t.Run(ci, func(t *testing.T) {
			t.Setenv("CI", ci)
			got := &outcome{}
			Missing(got, "no database")
			require.Empty(t, got.failed)
			require.Contains(t, got.skipped, "missing prerequisite: no database")
		})
	}
}

func TestAnOptionalSuiteSkipsUnderCIToo(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("AGENTIFI_BROWSER_TEST", "")
	t.Setenv("AGENTIFI_LIVE_EXAMPLE", "")

	browser := &outcome{}
	Browser(browser)
	require.Empty(t, browser.failed)
	require.Contains(t, browser.skipped, Prefix+" browser:")

	live := &outcome{}
	Live(live, "AGENTIFI_LIVE_EXAMPLE", "reach an example site")
	require.Empty(t, live.failed)
	require.Equal(t, Prefix+" live: set AGENTIFI_LIVE_EXAMPLE to reach an example site", live.skipped)
}

func TestAnOptionalSuiteRunsWhenAskedFor(t *testing.T) {
	t.Setenv("AGENTIFI_BROWSER_TEST", "1")
	t.Setenv("AGENTIFI_LIVE_EXAMPLE", "an address")

	browser := &outcome{}
	Browser(browser)
	require.Empty(t, browser.skipped)

	live := &outcome{}
	require.Equal(t, "an address", Live(live, "AGENTIFI_LIVE_EXAMPLE", "reach an example site"))
	require.Empty(t, live.skipped)
}
