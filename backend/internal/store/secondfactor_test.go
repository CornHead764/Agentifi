package store

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// How a login's second factor is answered: a plain preference on the row,
// read back with it, untouched by a settings save, and never anything the
// schema's check constraint refuses.

func TestABillConnectionKeepsItsSecondFactorChoice(t *testing.T) {
	space := newSpace(t)
	connection := newBillConnection(t, space)
	require.Equal(t, domain.SecondFactorAny, connection.SecondFactor)

	require.NoError(t, sealedDB(t).SetBillConnectionSecondFactor(t.Context(), space, connection.ID, domain.SecondFactorEmail))
	kept, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, domain.SecondFactorEmail, kept.SecondFactor)

	kept.Label = "Main account, renamed"
	require.NoError(t, sealedDB(t).UpdateBillConnection(t.Context(), space, &kept))
	again, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, domain.SecondFactorEmail, again.SecondFactor, "a settings save does not undo it")

	require.Error(t, sealedDB(t).SetBillConnectionSecondFactor(t.Context(), space, connection.ID, "sms"),
		"nothing reads texts, so the schema refuses them")
	require.Error(t, sealedDB(t).SetBillConnectionSecondFactor(t.Context(), space, connection.ID, "carrier-pigeon"))
	require.ErrorIs(t, sealedDB(t).SetBillConnectionSecondFactor(t.Context(), newSpace(t), connection.ID,
		domain.SecondFactorTOTP), ErrNotFound, "another space's row is not found")
}

func TestAMerchantAccountKeepsItsSecondFactorChoice(t *testing.T) {
	space := newSpace(t)
	account := newSignedInMerchantAccount(t, space, "Alex")
	require.Equal(t, domain.SecondFactorAny, account.SecondFactor)

	require.NoError(t, sealedDB(t).SetMerchantSecondFactor(t.Context(), space, account.ID, domain.SecondFactorTOTP))
	kept, err := sealedDB(t).GetMerchantAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Equal(t, domain.SecondFactorTOTP, kept.SecondFactor)

	require.Error(t, sealedDB(t).SetMerchantSecondFactor(t.Context(), space, account.ID, "sms"))
	require.Error(t, sealedDB(t).SetMerchantSecondFactor(t.Context(), space, account.ID, "carrier-pigeon"))
}
