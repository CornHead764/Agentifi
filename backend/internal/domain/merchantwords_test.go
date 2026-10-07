package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdentifyingWordsDropTheVaryingTailAndKeepTheOrder(t *testing.T) {
	// Keeping the ids and dates would split one payee into a group per charge.
	require.Equal(t, []string{"acme", "utilities"}, IdentifyingWords("ACME UTILITIES 4471 03/02"))
	require.Equal(t, []string{"pos", "at", "fresh", "fresh"}, IdentifyingWords("POS AT FRESH #0417 FRESH B"))
}

func TestMerchantWordingOverlapCountsTheSharedWords(t *testing.T) {
	shared, same := MerchantWordingOverlap("FRESH MKT RETURN", "FRESH MKT SPRINGFIELD")
	require.Equal(t, 2, shared)
	require.True(t, same)
	shared, same = MerchantWordingOverlap("FRESH MKT RETURN", "FRESH CUTS SALON")
	require.Equal(t, 1, shared)
	require.False(t, same)
}

func TestMerchantTokensKeepOnlyTheWordsThatNameTheMerchant(t *testing.T) {
	require.Equal(t, []string{"fresh", "mkt", "springfield"},
		MerchantTokens("POS PURCHASE FRESH MKT #0417 Springfield fresh"))
	require.Empty(t, MerchantTokens("ACH DEBIT 2291 WEB PMT"))
}

func TestSharesMerchantWordingNeedsHalfTheSubjectsWordsRoundedUp(t *testing.T) {
	// Two words: one in common is half.
	require.True(t, SharesMerchantWording("FRESH MKT", "MKT 0417 ELSEWHERE"))
	// Three words: one in common is under half, two is enough.
	require.False(t, SharesMerchantWording("FRESH MKT RETURN", "FRESH CUTS SALON"))
	require.True(t, SharesMerchantWording("FRESH MKT RETURN", "FRESH MKT SPRINGFIELD"))
	// Wording made only of bank boilerplate names nobody.
	require.False(t, SharesMerchantWording("CHECK 1044", "CHECK 1045"))
}

func TestMerchantWordingFoldsAccentsSoEveryCallerAgrees(t *testing.T) {
	require.Equal(t, MerchantTokens("CAFE LUMIERE"), MerchantTokens("Café Lumière"))
	require.True(t, SharesMerchantWording("CAFÉ LUMIÈRE", "CAFE LUMIERE 0417"))
	require.Equal(t, []string{"cafe", "bar"}, Words("CAFÉ-Bar"))
}
