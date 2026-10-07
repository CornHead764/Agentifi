package importer

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// A template split holding a value JSON cannot carry stops the import, where
// a NULL column would have dropped it without a word.
func TestAJSONColumnThatWillNotEncodeFailsTheImport(t *testing.T) {
	w := &writer{}
	require.Nil(t, w.jsonArg("recurring_series.template_splits", nil))
	require.NoError(t, w.err)

	w.jsonArg("recurring_series.template_splits", []any{math.Inf(1)})
	require.ErrorContains(t, w.err, "recurring_series.template_splits")
}
