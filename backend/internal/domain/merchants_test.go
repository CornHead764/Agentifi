package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestEveryMerchantIsWhole(t *testing.T) {
	require.Len(t, domain.Merchants, 2)
	seen := map[domain.MerchantID]bool{}
	for _, m := range domain.Merchants {
		require.False(t, seen[m.ID], "%s twice", m.ID)
		seen[m.ID] = true
		require.NotEmpty(t, m.Name)
		require.NotEmpty(t, m.Noun)
		require.NotEmpty(t, m.Plural)
		require.NotNil(t, m.Wording)
		require.NotEmpty(t, m.SignInAlert)
		// Settings lists Merchants once and the shop is a path segment, so an
		// alert deep-links straight to the shop it is about.
		require.Equal(t, "/settings/merchants/"+string(m.ID), m.SettingsPath)
		found, ok := domain.MerchantByID(m.ID)
		require.True(t, ok)
		require.Equal(t, m.Name, found.Name)
	}
	_, ok := domain.MerchantByID("target")
	require.False(t, ok)
}

func TestWordingTellsTheMerchantsApart(t *testing.T) {
	cases := []struct {
		statement, payee string
		want             []domain.MerchantID
	}{
		{"AMAZON.COM*2K4D1R6Q3 AMZN.COM/BILL WA", "Amazon", []domain.MerchantID{domain.MerchantAmazon}},
		{"AMZN Mktp US*1A2B3C", "", []domain.MerchantID{domain.MerchantAmazon}},
		{"COSTCO WHSE #0123 SPRINGFIELD ZZ", "Costco", []domain.MerchantID{domain.MerchantCostco}},
		{"COSTCO.COM *ONLINE", "", []domain.MerchantID{domain.MerchantCostco}},
		{"COSTCO GAS #0123", "Costco Gas", []domain.MerchantID{domain.MerchantCostco}},
		{"", "Costco Wholesale", []domain.MerchantID{domain.MerchantCostco}},
		{"POS DEBIT TMOBILE*AUTO", "T-Mobile", nil},
		// A row that names both is left to both, Amazon first.
		{"AMAZON RETURN OF COSTCO ITEM", "", []domain.MerchantID{domain.MerchantAmazon, domain.MerchantCostco}},
	}
	for _, c := range cases {
		var got []domain.MerchantID
		for _, m := range domain.MerchantsFor(c.statement, c.payee) {
			got = append(got, m.ID)
		}
		require.Equal(t, c.want, got, "%q / %q", c.statement, c.payee)
	}
	require.True(t, domain.IsCostcoWording("costco whse", ""))
	require.False(t, domain.IsCostcoWording("AMAZON.COM", "Amazon"))
	require.False(t, domain.IsAmazonWording("COSTCO WHSE", "Costco"))
}

func TestPurchaseKindsAreTheThreeNamed(t *testing.T) {
	for _, kind := range []string{domain.PurchaseOnline, domain.PurchaseWarehouse, domain.PurchaseFuel} {
		require.True(t, domain.IsPurchaseKind(kind))
	}
	require.False(t, domain.IsPurchaseKind("gas"))
	require.False(t, domain.IsPurchaseKind(""))
}

func TestAShopCardIsAnInstrumentTheBankNeverSaw(t *testing.T) {
	require.True(t, domain.IsGiftCardInstrument("Costco Shop Card"))
	require.True(t, domain.IsGiftCardInstrument("Amazon Gift Card"))
	require.True(t, domain.IsGiftCardInstrument(""))
	require.True(t, domain.IsGiftCardInstrument("Cash"))
	require.False(t, domain.IsGiftCardInstrument("Visa ••••1234"))
	require.False(t, domain.IsGiftCardInstrument("Amazon Visa ••••1234"))
}
