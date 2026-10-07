package billmail

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Invented, in the shape of rsync.net's invoice mail.
const (
	rsyncNetBillingSender = "billing@rsync.net"
	rsyncNetInvoice       = "rsync.net\n" +
		"Account  cloud1234\n" +
		"Total  $60.00\n" +
		"Due  10/01/2026\n" +
		"Thank you for your business.\n"
)

func TestRsyncNetReadsTheInvoice(t *testing.T) {
	claim, ok := Match(Message{
		ID:      "<r1@mail.example.com>",
		Sender:  rsyncNetBillingSender,
		Subject: "rsync.net invoice",
		Text:    rsyncNetInvoice,
	})
	if !ok || claim.Biller != domain.BillerRsyncNet || len(claim.Bills) != 1 {
		t.Fatalf("claim = %+v, ok = %v", claim, ok)
	}

	bill := claim.Bills[0]
	if bill.MaskedNumber != "••••1234" {
		t.Fatalf("masked = %q", bill.MaskedNumber)
	}
	if !bill.AmountDue.Equal(domain.MustFromString("60.00")) {
		t.Fatalf("amount = %s", bill.AmountDue)
	}
	if bill.DueOn != domain.NewDate(2026, time.October, 1) {
		t.Fatalf("due = %s", bill.DueOn)
	}
}

func TestRsyncNetIgnoresItsOtherMail(t *testing.T) {
	for _, m := range []Message{
		{Sender: rsyncNetBillingSender, Subject: "Scheduled maintenance window", Text: rsyncNetInvoice},
		{Sender: rsyncNetBillingSender, Subject: "rsync.net invoice", Text: "Your invoice is in the portal.\n"},
	} {
		if claim, ok := (rsyncNetParser{}).Match(m); ok {
			t.Fatalf("%q was claimed: %+v", m.Subject, claim)
		}
	}
}
