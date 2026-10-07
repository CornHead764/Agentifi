package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestTheCategoriesAProcessFindsCannotBeDeleted(t *testing.T) {
	for _, known := range []string{
		domain.KnownCategoryTransfer,
		domain.KnownCategoryCreditCardPayment,
		domain.KnownCategoryFeesAndCharges,
	} {
		reason, depended := domain.CategoryDependedOn(known)
		require.True(t, depended, known)
		require.NotEmpty(t, reason, known)
		require.Equal(t, reason, domain.CategoryProtection(known, true), known)
		require.Contains(t, domain.ProtectedKnownCategoryIDs(), known)
	}
	require.Len(t, domain.ProtectedKnownCategoryIDs(), 3)
}

func TestASystemCategoryCannotBeDeletedEither(t *testing.T) {
	// Opening Balance as the file importers write it: no marker, not editable.
	require.Equal(t, "it is maintained by the app", domain.CategoryProtection("", false))
	_, depended := domain.CategoryDependedOn("")
	require.False(t, depended, "nothing finds it by marker, so its kind is not pinned")
}

func TestAnOrdinaryCategoryCanBeDeleted(t *testing.T) {
	require.Empty(t, domain.CategoryProtection("", true))
	require.Empty(t, domain.CategoryProtection("4000000000", true), "Groceries")
	_, depended := domain.CategoryDependedOn("4000000000")
	require.False(t, depended)
}

func TestEveryProtectedMarkerIsInTheDefaultTree(t *testing.T) {
	// A marker the seed never writes would protect nothing in a new space.
	seeded := map[string]bool{}
	for _, group := range domain.DefaultCategories {
		seeded[group.KnownCategoryID] = true
		for _, child := range group.Children {
			seeded[child.KnownCategoryID] = true
		}
	}
	for _, known := range domain.ProtectedKnownCategoryIDs() {
		require.True(t, seeded[known], known)
	}
}

func TestAPairTouchingACardIsACardPayment(t *testing.T) {
	require.Equal(t, domain.KnownCategoryCreditCardPayment,
		domain.TransferCategoryFor(domain.KindCash, domain.KindCreditCard))
	require.Equal(t, domain.KnownCategoryCreditCardPayment,
		domain.TransferCategoryFor(domain.KindCreditCard, domain.KindCash))
	require.Equal(t, domain.KnownCategoryCreditCardPayment,
		domain.TransferCategoryFor(domain.KindCreditCard, domain.KindCreditCard))
	require.Equal(t, domain.KnownCategoryTransfer,
		domain.TransferCategoryFor(domain.KindCash, domain.KindCash))
	require.Equal(t, domain.KnownCategoryTransfer,
		domain.TransferCategoryFor(domain.KindCash, domain.KindLoan), "a loan payment is a transfer, not a card payment")
	require.Equal(t, domain.KnownCategoryTransfer,
		domain.TransferCategoryFor(domain.KindCash, domain.KindInvestment))
}

func TestACardPaymentIsMoneyInOnACardThatSaysPayment(t *testing.T) {
	in, out := domain.MustFromString("150.00"), domain.MustFromString("-150.00")
	for statement, want := range map[string]bool{
		"PAYMENT THANK YOU":          true,
		"AUTOPAY PAYMENT RECEIVED":   true,
		"MOBILE PYMT RECEIVED":       true,
		"ONLINE PMT - THANK YOU":     true,
		"CREDIT CARD PAYMENT":        true,
		"REFUND HARBOR COFFEE":       false,
		"PAYMENT REVERSAL":           false,
		"CASHBACK REWARD":            false,
		"HARBOR COFFEE":              false,
		"":                           false,
		"RETURN - PAYMENT TERMINALS": false,
	} {
		require.Equal(t, want, domain.LooksLikeCardPayment(domain.KindCreditCard, in, statement), statement)
	}
	require.False(t, domain.LooksLikeCardPayment(domain.KindCreditCard, out, "PAYMENT THANK YOU"),
		"money out on a card is a charge")
	require.False(t, domain.LooksLikeCardPayment(domain.KindCash, in, "PAYMENT THANK YOU"),
		"a payment into checking is not a card payment")
}
