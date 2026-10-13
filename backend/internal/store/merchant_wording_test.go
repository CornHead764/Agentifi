package store

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Each merchant's bulk-match list holds its own bank rows and no other's,
// because the SQL wording is a copy of the domain's. Wording is invented.
func TestEachMerchantListsOnlyRowsWordedForIt(t *testing.T) {
	space := newSpace(t)
	card := newAccount(t, space, "Everyday Card")
	amazon := newTransactionFor(t, space, card.ID, "AMZN Mktp US*0A0A0A0A0")
	costco := newTransactionFor(t, space, card.ID, "COSTCO WHSE #0000")
	ebay := newTransactionFor(t, space, card.ID, "PAYPAL *EBAY")
	newTransactionFor(t, space, card.ID, "PAYPAL *EXAMPLESHOP")

	want := map[domain.MerchantID]string{
		domain.MerchantAmazon: amazon.ID.String(),
		domain.MerchantCostco: costco.ID.String(),
		domain.MerchantEbay:   ebay.ID.String(),
	}
	require.Len(t, want, len(domain.Merchants), "a merchant with no case here matches no row")
	for _, merchant := range domain.Merchants {
		rows, err := db(t).ListUnmatchedMerchantTransactions(t.Context(), space, merchant.ID, 0)
		require.NoError(t, err)
		require.Len(t, rows, 1, "%s", merchant.ID)
		require.Equal(t, want[merchant.ID], rows[0].ID.String(), "%s", merchant.ID)
		require.True(t, merchant.Wording(rows[0].StatementName, rows[0].Payee), "%s", merchant.ID)
	}
}
