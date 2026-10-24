package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestARevocationIsVisibleAndPrunable(t *testing.T) {
	ctx := t.Context()
	st := newSQLiteTest(t)
	jti := "jti-" + t.Name()

	require.NoError(t, st.RevokeToken(ctx, jti, time.Now().Add(time.Hour)))
	revoked, err := st.TokenRevoked(ctx, jti)
	require.NoError(t, err)
	require.True(t, revoked)

	// Re-revoking moves the mark rather than fighting the primary key.
	past := time.Now().Add(-time.Minute)
	require.NoError(t, st.RevokeToken(ctx, jti, past))

	n, err := st.PruneRevocations(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(1))

	revoked, err = st.TokenRevoked(ctx, jti)
	require.NoError(t, err)
	require.False(t, revoked, "a pruned mark must read as unrevoked")
}
