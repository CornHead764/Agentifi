package billmail

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Invented throughout. One mail can carry several accounts, as it does for a
// customer billed for more than one premises.
const alliantBillReady = `<html><body>
<p>Hello Alex Example,</p>
<p>Your Alliant Energy bill is ready to view.</p>
<table>
  <tr><th>Account No.</th><th>Total Amount Due($)</th><th>Due Date</th></tr>
  <tr><td>30001234</td><td>150.00</td><td>04-15-2026</td></tr>
  <tr><td>30005678</td><td>87</td><td>04-15-2026</td></tr>
</table>
<p>Sign in to My Account to view it.</p>
</body></html>`

// The marketing list, which sends the same household energy-saving tips.
const alliantMarketingSender = "alliantenergy@m.alliantenergy.com"

func TestAlliantReadsARowPerAccount(t *testing.T) {
	claim, ok := Match(Message{
		ID:      "<a1@mail.example.com>",
		Sender:  alliantSender,
		Subject: "FW: Your Alliant Energy bill is ready to view",
		HTML:    alliantBillReady,
	})
	if !ok {
		t.Fatal("the bill-ready mail was not claimed")
	}
	if claim.Biller != domain.BillerAlliant || len(claim.Bills) != 2 {
		t.Fatalf("claim = %+v", claim)
	}

	first, second := claim.Bills[0], claim.Bills[1]
	if first.ExternalID != "30001234" || first.MaskedNumber != "••••1234" {
		t.Fatalf("first bill account = %q / %q", first.ExternalID, first.MaskedNumber)
	}
	if !first.AmountDue.Equal(domain.MustFromString("150.00")) {
		t.Fatalf("first amount = %s", first.AmountDue)
	}
	if first.DueOn != domain.NewDate(2026, time.April, 15) {
		t.Fatalf("first due = %s", first.DueOn)
	}
	if second.ExternalID != "30005678" || !second.AmountDue.Equal(domain.MustFromString("87")) {
		t.Fatalf("second bill = %+v", second)
	}
}

func TestAlliantIgnoresItsOtherMail(t *testing.T) {
	for _, m := range []Message{
		{Sender: alliantSender, Subject: "Payment Received", HTML: alliantBillReady},
		{Sender: alliantMarketingSender, Subject: "Your Alliant Energy bill is ready to view", HTML: alliantBillReady},
	} {
		if claim, ok := (alliantParser{}).Match(m); ok {
			t.Fatalf("%q from %q was claimed: %+v", m.Subject, m.Sender, claim)
		}
	}
}
