package billmail

import (
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The household's forwarder, invented: the rule in the primary mailbox drops a
// relayed text here.
var householdRelays = []string{"Relay@example.test"}

func TestFindOTPReadsAProvidersCode(t *testing.T) {
	code, ok := FindOTP(Message{
		Sender:  spectrumSender,
		Subject: "Your security code",
		Text:    "Your one-time code is 458213. It expires in 10 minutes.\n",
	}, householdRelays)
	if !ok {
		t.Fatal("the code mail was not read as a code")
	}
	if code.Code != "458213" || code.Biller != domain.BillerSpectrum || code.Relayed {
		t.Fatalf("otp = %+v", code)
	}
}

func TestFindOTPReadsARelayedTextAndNamesTheProvider(t *testing.T) {
	code, ok := FindOTP(Message{
		Sender:  "relay@example.test",
		Subject: "SMS from 555-0123",
		Text:    "Alliant Energy: your verification code is 902145. Do not share it.\n",
	}, householdRelays)
	if !ok {
		t.Fatal("the relayed text was not read as a code")
	}
	if code.Code != "902145" || code.Biller != domain.BillerAlliant || !code.Relayed {
		t.Fatalf("otp = %+v", code)
	}
}

// The invented provider's name is an ordinary word on purpose: only its use as
// a sender's name counts.
func TestFindOTPNamesAProviderThatOnlyTextsByItsName(t *testing.T) {
	catalogue := domain.Billers
	t.Cleanup(func() { domain.Billers = catalogue })
	domain.Billers = append(append([]domain.Biller(nil), catalogue...), domain.Biller{
		ID: "plainly", Name: "Plainly", Access: domain.AccessBrowser,
		Challenges: []domain.BillChallengeKind{domain.ChallengeSMS},
	})

	code, ok := FindOTP(Message{
		Sender: "relay@example.test", Subject: "SMS from 555-0123",
		Text: "Plainly: your verification code is 604183. Don't share it with anyone.\n",
	}, householdRelays)
	if !ok || code.Code != "604183" || code.Biller != "plainly" || !code.Relayed {
		t.Fatalf("otp = %+v, %v", code, ok)
	}

	code, ok = FindOTP(Message{
		Sender: "relay@example.test", Subject: "SMS from 555-0123",
		Text: "Your verification code is 604183. It is plainly not for anyone else.\n",
	}, householdRelays)
	if !ok || code.Biller != "" {
		t.Fatalf("otp = %+v, %v", code, ok)
	}
}

func TestFindCodeTakesOnlyANumberOfItsOwn(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Your Alliant Energy one-time passcode is 337791", "337791"},
		{"Your verification code: 4821", "4821"},
		{"Security code 12345678 for sign in", "12345678"},
		{"Your code is 1234567890", ""},
		{"Reference code INV-4821 for your records", ""},
		{"Use code SAVE20 by 12/31/2026", ""},
		{"Use code SAVE20 and save $1500.00", ""},
		{"Call 800-555-0199 with your code", ""},
	} {
		if got := findCode(tc.in); got != tc.want {
			t.Errorf("findCode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFindOTPRefusesWhatIsNotACode(t *testing.T) {
	for _, m := range []Message{
		{
			// A promo code, a date, an amount and a support line: every digit
			// run a marketing mail carries, and not one of them a code.
			Sender:  spectrumSender,
			Subject: "Save with promo code SAVE20",
			Text:    "Use code SAVE20 by 12/31/2026 and save $1500.00 on install. Call 800-555-0199.\n",
		},
		{
			Sender:  alliantSender,
			Subject: "Your Alliant Energy bill is ready to view",
			HTML:    alliantBillReady,
		},
		{
			// A perfect code from an address in neither table.
			Sender:  "somebody@elsewhere.test",
			Subject: "Your verification code is 458213",
		},
	} {
		if code, ok := FindOTP(m, householdRelays); ok {
			t.Fatalf("%q from %q was read as a code: %+v", m.Subject, m.Sender, code)
		}
	}
}
