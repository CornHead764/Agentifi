package billmail

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Invented, in the shape of Apple's receipt mail. The fixture exercises
// exactly the rule the parser writes down.
const (
	appleReceiptSender = "no_reply@email.apple.com"
	appleReceipt       = "APPLE ACCOUNT  alex.example@example.com\n" +
		"DATE  Sep 10, 2026\n" +
		"ORDER ID  ML7001234\n" +
		"iCloud+ 200GB  Monthly\n" +
		"TOTAL  $3.00\n"
)

func TestAppleReadsTheReceiptTotal(t *testing.T) {
	claim, ok := Match(Message{
		ID:      "<ap1@mail.example.com>",
		Sender:  appleReceiptSender,
		Subject: "Your receipt from Apple.",
		Text:    appleReceipt,
	})
	if !ok || claim.Biller != domain.BillerApple || len(claim.Bills) != 1 {
		t.Fatalf("claim = %+v, ok = %v", claim, ok)
	}

	bill := claim.Bills[0]
	if !bill.AmountDue.Equal(domain.MustFromString("3.00")) {
		t.Fatalf("amount = %s", bill.AmountDue)
	}
	day := domain.NewDate(2026, time.September, 10)
	if bill.DueOn != day || bill.AutopayOn != day {
		t.Fatalf("due = %s, autopay = %s", bill.DueOn, bill.AutopayOn)
	}
}

func TestAppleIgnoresItsOtherMail(t *testing.T) {
	for _, m := range []Message{
		{Sender: appleReceiptSender, Subject: "Your subscription renews soon", Text: appleReceipt},
		{Sender: "news@insideapple.apple.com", Subject: "Meet the new Apple Watch", Text: appleReceipt},
	} {
		if claim, ok := (appleParser{}).Match(m); ok {
			t.Fatalf("%q from %q was claimed: %+v", m.Subject, m.Sender, claim)
		}
	}
}
