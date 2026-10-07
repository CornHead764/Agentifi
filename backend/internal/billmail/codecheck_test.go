package billmail

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The gate a model's answer goes through. Invented mail throughout.

func TestACandidateCodeHasToBeInTheMailVerbatimAndLookLikeACode(t *testing.T) {
	mail := Message{
		Sender: "MyAccount@spectrumemails.com", Subject: "Finish signing in",
		Text: "Use 314159 to finish signing in. Account 55512345.",
	}
	require.True(t, CheckCode("314159", mail, CodeDigits))
	require.False(t, CheckCode("271828", mail, CodeDigits), "not in the mail")
	require.False(t, CheckCode("3141", mail, CodeDigits), "part of a longer number")
	require.False(t, CheckCode("5551", mail, CodeDigits), "part of an account number")
	require.False(t, CheckCode("123", mail, CodeDigits), "too short")
	require.False(t, CheckCode("31415926535", mail, CodeDigits), "too long")
	require.False(t, CheckCode("Use", mail, CodeDigits), "not the alphabet")
	require.False(t, CheckCode("", mail, CodeDigits))
}

func TestAMerchantsCodeMailIsWithheldAndItsReceiptIsNot(t *testing.T) {
	require.True(t, MayCarryCode(Message{
		Sender: "account-update@amazon.com", Subject: "Amazon sign-in",
		Text: "Your one-time password is 482913.",
	}))
	require.False(t, MayCarryCode(Message{
		Sender: "auto-confirm@amazon.com", Subject: "Your order has shipped",
		Text: "Order #111-0000000-0000000 is on its way.",
	}))
	require.False(t, MayCarryCode(Message{
		Sender: "receipts@cafe.example.invalid", Subject: "Your receipt",
		Text: "Your verification code is 482913.",
	}), "a code from nobody who sends codes is not one")
	require.Equal(t, []string{"@amazon.com"}, MerchantCodeSendersFor(domain.MerchantAmazon))
}
