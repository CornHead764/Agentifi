package billmail

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Invented, in the shape of Erie Insurance's invoice mail: one policy per
// mail, the figures on one line between the two buttons.
const erieInvoice = "Hello Alex Example,\n" +
	"Your new invoice is ready.\n" +
	"Policy  Auto (Q00-0001234)  Due Date  04/21/2026  Total Due  $1,200.00  VIEW INVOICE  MAKE A PAYMENT\n" +
	"Questions? Contact your agent.\n"

// The newsletter and the wallet notice, both on the insurer's own domain.
const (
	erieNewsletterSender = "Eriesense@emails.erieinsurance.com"
	erieNoticeSender     = "no-reply@erieinsurance.com"
)

func TestErieReadsThePolicyLine(t *testing.T) {
	claim, ok := Match(Message{
		ID:      "<e1@mail.example.com>",
		Sender:  erieSender,
		Subject: "Your new Erie Insurance invoice",
		Text:    erieInvoice,
	})
	if !ok || claim.Biller != domain.BillerErie || len(claim.Bills) != 1 {
		t.Fatalf("claim = %+v, ok = %v", claim, ok)
	}

	bill := claim.Bills[0]
	if bill.ExternalID != "Q00-0001234" || bill.MaskedNumber != "••••1234" || bill.Label != "Auto" {
		t.Fatalf("bill identity = %+v", bill)
	}
	if !bill.AmountDue.Equal(domain.MustFromString("1200.00")) {
		t.Fatalf("amount = %s", bill.AmountDue)
	}
	if bill.DueOn != domain.NewDate(2026, time.April, 21) {
		t.Fatalf("due = %s", bill.DueOn)
	}
}

func TestErieIgnoresItsOtherMail(t *testing.T) {
	for _, m := range []Message{
		{Sender: erieNewsletterSender, Subject: "Your new Erie Insurance invoice", Text: erieInvoice},
		{Sender: erieNoticeSender, Subject: "Your ERIE wallet card is ready", Text: erieInvoice},
		{Sender: erieSender, Subject: "Your policy documents are ready", Text: erieInvoice},
	} {
		if claim, ok := (erieParser{}).Match(m); ok {
			t.Fatalf("%q from %q was claimed: %+v", m.Subject, m.Sender, claim)
		}
	}
}
