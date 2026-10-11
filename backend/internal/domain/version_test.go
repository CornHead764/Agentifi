package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVersionTakesTagsAndBareNumbers(t *testing.T) {
	for text, want := range map[string]Version{
		"v0.2.0":         {Major: 0, Minor: 2, Patch: 0},
		"1.10.3":         {Major: 1, Minor: 10, Patch: 3},
		"v2.0.0-rc.1":    {Major: 2, PreRelease: "rc.1"},
		"1.0.0+20261008": {Major: 1},
	} {
		got, ok := ParseVersion(text)
		require.True(t, ok, text)
		require.Equal(t, want, got, text)
	}
	for _, text := range []string{"", "latest", "v1.2", "1.2.3.4", "v1.x.0", "01.2.3", "-1.0.0"} {
		_, ok := ParseVersion(text)
		require.False(t, ok, text)
	}
}

func TestVersionsOrderNumericallyWithPreReleasesFirst(t *testing.T) {
	order := []string{"0.1.0", "0.1.1", "0.2.0", "0.10.0", "1.0.0-beta", "1.0.0-rc.1", "1.0.0", "1.0.1"}
	for i := 1; i < len(order); i++ {
		older, _ := ParseVersion(order[i-1])
		newer, _ := ParseVersion(order[i])
		require.Negative(t, older.Compare(newer), "%s < %s", order[i-1], order[i])
		require.Positive(t, newer.Compare(older), "%s > %s", order[i], order[i-1])
	}
	same, _ := ParseVersion("v0.2.0")
	require.Zero(t, same.Compare(Version{Minor: 2}))
}

func TestUpdateAvailableNeedsAKnownRunningVersion(t *testing.T) {
	require.True(t, UpdateAvailable("0.2.0", "v0.3.0"))
	require.False(t, UpdateAvailable("0.2.0", "v0.2.0"))
	require.False(t, UpdateAvailable("0.3.0", "v0.2.0"))
	require.False(t, UpdateAvailable("0.3.0-rc.1", "v0.2.0"))
	require.True(t, UpdateAvailable("0.3.0-rc.1", "v0.3.0"))
	require.False(t, UpdateAvailable("", "v9.0.0"))
	require.False(t, UpdateAvailable("0.2.0", "nightly"))
}
