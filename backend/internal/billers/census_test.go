package billers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The census is sorted, so two readings of one page read the same and a
// difference between them is a difference in the page.
func TestTheInputCensusReadsTheSameTwice(t *testing.T) {
	require.Equal(t, "none", inputCensus(nil))
	require.Equal(t, "checkbox 1 · email 1 · password 1",
		inputCensus(map[string]int{"password": 1, "email": 1, "checkbox": 1}))
}
