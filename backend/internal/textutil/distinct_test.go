package textutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDistinctKeepsOrderAndDropsZeroValues(t *testing.T) {
	require.Equal(t, []string{"a", "b"}, Distinct([]string{"a", "", "b", "a"}))
	require.Equal(t, []int{3, 1}, Distinct([]int{3, 0, 1, 3}))
}
