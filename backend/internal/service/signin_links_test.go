package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// A "needs you to sign in again" alert opens that login's sign-in, not the
// screen it is listed on: the screens read `sign-in` and start it.
func TestSignInAlertsOpenTheSignInItself(t *testing.T) {
	id := uuid.MustParse("7d3f7a52-4a0c-4c47-9a39-0f5d6a1c2b10")

	require.Equal(t, "/settings/bills?sign-in="+id.String(), billSignInURL(id))

	amazon, ok := domain.MerchantByID(domain.MerchantAmazon)
	require.True(t, ok)
	require.Equal(t, "/settings/merchants/amazon?sign-in="+id.String(), merchantSignInURL(amazon, id))
}
