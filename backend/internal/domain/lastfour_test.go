package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestLastFourJoinsEveryDigitFirst(t *testing.T) {
	for written, want := range map[string]string{
		"5555512-34":     "1234",
		"555551234":      "1234",
		"Q00-0001234":    "1234",
		"Ending in 1234": "1234",
		"XXXX-1234":      "1234",
		"cloud1234":      "1234",
		"12-3":           "",
		"":               "",
	} {
		require.Equal(t, want, domain.LastFour(written), written)
	}
}

func TestMaskAccountShowsTheLastFourOrNothing(t *testing.T) {
	require.Equal(t, "••••1234", domain.MaskAccount("5555512-34"))
	require.Equal(t, domain.MaskAccount("555551234"), domain.MaskAccount("5555512-34"),
		"a hyphenated number and a plain one are the same account")
	require.Empty(t, domain.MaskAccount("123"))
}
