package billmail

import (
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Invented, in the shape of We Energies' bill-ready mail. The fixture
// exercises exactly the rule the parser writes down.
const (
	weEnergiesNoticeSender = "noreply@we-energies.com"
	weEnergiesBillReady    = "Account Number  70001234\n" +
		"Amount Due  $130.00\n" +
		"Due Date  10/20/2026\n" +
		"Sign in to view your bill.\n"
)

func TestWeEnergiesReadsTheAmountDue(t *testing.T) {
	claim, ok := Match(Message{
		ID:      "<w1@mail.example.com>",
		Sender:  weEnergiesNoticeSender,
		Subject: "Your We Energies bill is ready",
		Text:    weEnergiesBillReady,
	})
	if !ok || claim.Biller != domain.BillerWeEnergies || len(claim.Bills) != 1 {
		t.Fatalf("claim = %+v, ok = %v", claim, ok)
	}

	bill := claim.Bills[0]
	if bill.ExternalID != "1234" || bill.MaskedNumber != "••••1234" {
		t.Fatalf("bill identity = %+v", bill)
	}
	if !bill.AmountDue.Equal(domain.MustFromString("130.00")) {
		t.Fatalf("amount = %s", bill.AmountDue)
	}
	if bill.DueOn != domain.NewDate(2026, time.October, 20) {
		t.Fatalf("due = %s", bill.DueOn)
	}
}

func TestAHyphenatedAccountNumberMasksAsThePullMasksIt(t *testing.T) {
	// The pull masks every digit joined; reading the longest run instead would
	// give 5512 and miss the subaccount the pull stored as ••••1234.
	claim, ok := Match(Message{
		ID:      "<w2@mail.example.com>",
		Sender:  weEnergiesNoticeSender,
		Subject: "Your We Energies bill is ready",
		Text:    strings.Replace(weEnergiesBillReady, "70001234", "5555512-34", 1),
	})
	if !ok || len(claim.Bills) != 1 {
		t.Fatalf("claim = %+v, ok = %v", claim, ok)
	}
	if got := claim.Bills[0].MaskedNumber; got != domain.MaskAccount("5555512-34") || got != "••••1234" {
		t.Fatalf("masked = %q", got)
	}
}

func TestWeEnergiesIgnoresItsOtherMail(t *testing.T) {
	for _, m := range []Message{
		{Sender: weEnergiesNoticeSender, Subject: "Ways to save energy this winter", Text: weEnergiesBillReady},
		{Sender: weEnergiesNoticeSender, Subject: "Your We Energies bill is ready", Text: "Sign in to view your bill.\n"},
	} {
		if claim, ok := (weEnergiesParser{}).Match(m); ok {
			t.Fatalf("%q was claimed: %+v", m.Subject, claim)
		}
	}
}
