package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestASuggestionBasisIsOneOfTheNamedOnes(t *testing.T) {
	require.Equal(t, domain.SuggestionBasisPayeeName, domain.SuggestionBasis("payee_name"))
	require.Equal(t, domain.SuggestionBasisRefund, domain.SuggestionBasis("refund"))
	require.Equal(t, "", domain.SuggestionBasis("it is a coffee shop"))
	require.Equal(t, "", domain.SuggestionBasis(""))
}
