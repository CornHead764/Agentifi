package billmail

import (
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Invented, in the shape of Spectrum's statement mail: three labelled
// paragraphs, the account given only by its last four, and no due date
// anywhere.
const spectrumStatement = `<html><body>
<p>Hi Alex Example,</p>
<p>Account Number:</p>
<p>Ending in 1234</p>
<p>Statement Amount:</p>
<p>$75.00</p>
<p>Auto Pay Date:</p>
<p>October 3, 2026</p>
<p>View your statement in My Spectrum.</p>
</body></html>`

const spectrumMarketingSender = "spectrum@exchange.spectrum.com"

func TestSpectrumTakesTheAutopayDateAsTheDueDate(t *testing.T) {
	claim, ok := Match(Message{
		ID:      "<s1@mail.example.com>",
		Sender:  strings.ToUpper(spectrumSender),
		Subject: "Your Spectrum Statement is Ready",
		HTML:    spectrumStatement,
	})
	if !ok || claim.Biller != domain.BillerSpectrum || len(claim.Bills) != 1 {
		t.Fatalf("claim = %+v, ok = %v", claim, ok)
	}
	if claim.Note != spectrumNote {
		t.Fatalf("note = %q", claim.Note)
	}

	bill := claim.Bills[0]
	if bill.ExternalID != "1234" || bill.MaskedNumber != "••••1234" {
		t.Fatalf("bill identity = %+v", bill)
	}
	if !bill.AmountDue.Equal(domain.MustFromString("75.00")) {
		t.Fatalf("amount = %s", bill.AmountDue)
	}
	want := domain.NewDate(2026, time.October, 3)
	if bill.DueOn != want || bill.AutopayOn != want {
		t.Fatalf("due = %s, autopay = %s", bill.DueOn, bill.AutopayOn)
	}
}

func TestSpectrumIgnoresItsOtherMail(t *testing.T) {
	for _, m := range []Message{
		{Sender: spectrumMarketingSender, Subject: "Your Spectrum Statement is Ready", HTML: spectrumStatement},
		{Sender: spectrumSender, Subject: "New sign in to your Spectrum account", HTML: spectrumStatement},
	} {
		if claim, ok := (spectrumParser{}).Match(m); ok {
			t.Fatalf("%q from %q was claimed: %+v", m.Subject, m.Sender, claim)
		}
	}
}
