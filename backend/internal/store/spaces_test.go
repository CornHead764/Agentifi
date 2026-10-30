package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// households is STRICT, so the sidebar's account types must be bound as text:
// a byte slice is a BLOB and the update fails.
func TestSidebarAccountTypesKeepNeverChosenNoneAndAList(t *testing.T) {
	st := newSQLiteTest(t)
	space := &Space{Name: "sidebar"}
	require.NoError(t, st.CreateSpace(t.Context(), space))

	for _, tc := range []struct {
		name  string
		types []string
	}{
		{"a list", []string{"checking", "credit_card"}},
		{"none", []string{}},
		{"never chosen", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			space.SidebarAccountTypes = tc.types
			require.NoError(t, st.UpdateSpace(t.Context(), space))
			got, err := st.GetSpace(t.Context(), space.ID)
			require.NoError(t, err)
			require.Equal(t, tc.types, got.SidebarAccountTypes)
		})
	}
}
