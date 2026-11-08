package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// A charge Amazon moved by a day is the charge on file, not a second one
// (docs/connectors/merchants.md). Orders and amounts are invented.
func TestTheSameChargeReadUnderAShiftedDateMovesInsteadOfRepeating(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	account := &store.MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Household"}
	require.NoError(t, st.CreateMerchantAccount(t.Context(), space, account))
	upsert := func(day int, amount string) bool {
		t.Helper()
		added, err := st.UpsertMerchantCharge(t.Context(), space, &store.MerchantCharge{
			Merchant: domain.MerchantAmazon, MerchantAccountID: account.ID, OrderNumber: "111-0000000-0000001",
			ChargedOn: domain.NewDate(2026, time.October, day), Amount: domain.MustFromString(amount),
		})
		require.NoError(t, err)
		return added
	}
	chargeDays := func() []string {
		t.Helper()
		found, err := st.MerchantChargesBetween(t.Context(), space, domain.MerchantAmazon,
			domain.NewDate(2026, time.October, 1), domain.NewDate(2026, time.October, 31))
		require.NoError(t, err)
		var out []string
		for _, one := range found {
			out = append(out, one.ChargedOn.String()+" "+one.Amount.String())
		}
		return out
	}

	require.True(t, upsert(11, "-16.00"))
	require.False(t, upsert(11, "-16.00"), "the same day is the same charge")
	require.False(t, upsert(12, "-16.00"), "a day later is the same charge moved")
	require.Equal(t, []string{"2026-10-12 -16.00"}, chargeDays())

	require.True(t, upsert(12, "-12.00"), "another amount is another charge")
	require.True(t, upsert(20, "-16.00"), "a week later is another charge")
	require.ElementsMatch(t, []string{"2026-10-12 -16.00", "2026-10-12 -12.00", "2026-10-20 -16.00"}, chargeDays())
}

// Two charges of one amount read together a day apart are two shipments, not
// one charge moved, however they arrive.
func TestChargesReadTogetherAreKeptApart(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	account := &store.MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Household"}
	require.NoError(t, st.CreateMerchantAccount(t.Context(), space, account))
	read := func(days ...int) {
		t.Helper()
		for _, day := range days {
			charge := &store.MerchantCharge{
				Merchant: domain.MerchantAmazon, MerchantAccountID: account.ID, OrderNumber: "111-0000000-0000002",
				ChargedOn: domain.NewDate(2026, time.October, day), Amount: domain.MustFromString("-20.00"),
			}
			for _, other := range days {
				if other != day {
					charge.ReadAlongside = append(charge.ReadAlongside, domain.NewDate(2026, time.October, other))
				}
			}
			_, err := st.UpsertMerchantCharge(t.Context(), space, charge)
			require.NoError(t, err)
		}
	}
	days := func() []string {
		t.Helper()
		found, err := st.MerchantChargesBetween(t.Context(), space, domain.MerchantAmazon,
			domain.NewDate(2026, time.October, 1), domain.NewDate(2026, time.October, 31))
		require.NoError(t, err)
		var out []string
		for _, one := range found {
			out = append(out, one.ChargedOn.String())
		}
		return out
	}

	read(11, 12)
	require.Equal(t, []string{"2026-10-11", "2026-10-12"}, days())
	read(12, 11)
	require.Equal(t, []string{"2026-10-11", "2026-10-12"}, days(), "a later read in either order keeps both")
}
