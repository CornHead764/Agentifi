package billmail

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// As Outlook's Forward writes it. Invented throughout.
const forwardedAlliant = `<html><body>
<p>Take a look.</p>
<hr>
<p><b>From:</b> Alliant Energy &lt;noreply@myaccount.alliantenergy.com&gt;<br>
<b>Sent:</b> Thursday, April 2, 2026 9:17 AM<br>
<b>To:</b> Alex Example &lt;alex@example.com&gt;<br>
<b>Subject:</b> Your Alliant Energy bill is ready to view</p>
` + alliantBillReady

func TestAForwardedBillReadsAsTheProviderSentIt(t *testing.T) {
	outer := Message{
		ID:      "<fw1@mail.example.com>",
		Sender:  "alex@example.com",
		Subject: "FW: Your Alliant Energy bill is ready to view",
		HTML:    forwardedAlliant,
		Text:    StripHTML(forwardedAlliant),
	}
	if _, ok := Match(outer); ok {
		t.Fatal("the wrapped mail matched before it was unwrapped; the sender rule is not holding")
	}

	inner, ok := UnwrapForward(outer)
	if !ok {
		t.Fatal("the forward was not unwrapped")
	}
	if inner.Sender != "noreply@myaccount.alliantenergy.com" {
		t.Fatalf("sender = %q", inner.Sender)
	}
	claim, ok := Match(inner)
	if !ok || claim.Biller != domain.BillerAlliant || len(claim.Bills) != 2 {
		t.Fatalf("claim = %+v (%v)", claim, ok)
	}
}

func TestAMailThatIsNotAForwardIsLeftAlone(t *testing.T) {
	plain := Message{Sender: "alex@example.com", Subject: "lunch?", Text: "Friday works for me."}
	if _, ok := UnwrapForward(plain); ok {
		t.Fatal("a plain mail was unwrapped")
	}

	// A quoted reply carries a From: further down; the header block of a
	// forward is at the top, and nothing else is one.
	reply := Message{Sender: "alex@example.com", Subject: "Re: the bill",
		Text: "Paid it.\n\n" + repeat("filler text ", 120) +
			"\nFrom: Alliant Energy <noreply@myaccount.alliantenergy.com>\nSubject: old thread"}
	if _, ok := UnwrapForward(reply); ok {
		t.Fatal("a quoted reply was unwrapped")
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

// A card statement forwarded by hand through Outlook. Invented throughout.
const forwardedCardStatement = `<html><body>
<div>FYI</div>
<hr>
<div><b>From:</b> Northwind Card Services &lt;statements@alerts.northwind-card.example&gt;<br>
<b>Sent:</b> Thursday, September 24, 2026 6:02 AM<br>
<b>To:</b> Alex Example &lt;alex@example.com&gt;<br>
<b>Subject:</b> Your Northwind Card statement is ready</div>
<table role="presentation" width="600">
  <tr><td><h2>Your statement is ready</h2></td></tr>
  <tr><td>Account ending in 1234</td></tr>
  <tr><td>
    <table>
      <tr><td style="padding:4px">Statement Balance:</td><td style="padding:4px"><b>$1,250.00</b></td></tr>
      <tr><td style="padding:4px">Minimum Payment Due:</td><td style="padding:4px"><b>$40.00</b></td></tr>
      <tr><td style="padding:4px">Payment Due Date:</td><td style="padding:4px"><b>10/21/2026</b></td></tr>
    </table>
  </td></tr>
  <tr><td>Pay by 5 p.m. ET on your due date to avoid a late fee of up to $41.00.</td></tr>
</table>
</body></html>`

func TestAForwardedCardStatementIsReadAsTheIssuerSentItOnTheDayItSentIt(t *testing.T) {
	forwardedOn := time.Date(2026, time.September, 26, 14, 30, 0, 0, time.UTC)
	outer := Message{
		ID:         "<fw-card@mail.example.com>",
		Sender:     "alex@example.com",
		Subject:    "Fw: Your Northwind Card statement is ready",
		HTML:       forwardedCardStatement,
		Text:       StripHTML(forwardedCardStatement),
		ReceivedAt: forwardedOn,
	}
	rule := Rule{
		Name: "Northwind statement", Sender: "@alerts.northwind-card.example",
		SubjectContains: "statement is ready",
		AmountLabel:     "Statement Balance", MinimumLabel: "Minimum Payment Due",
		DateLabel: "Payment Due Date", ReferenceLabel: "Account ending in",
	}
	if rule.Match(outer) {
		t.Fatal("the rule matched the household's own address before the forward was unwrapped")
	}

	inner, ok := UnwrapForward(outer)
	if !ok {
		t.Fatal("the forward was not unwrapped")
	}
	if inner.Sender != "statements@alerts.northwind-card.example" {
		t.Fatalf("sender = %q", inner.Sender)
	}
	if got := domain.DateOf(inner.ReceivedAt); got != domain.NewDate(2026, time.September, 24) {
		t.Fatalf("received = %s, want the day the issuer sent it", got)
	}
	if !rule.Match(inner) {
		t.Fatal("the rule does not match the unwrapped statement")
	}

	bill, err := rule.ExtractBill(inner)
	if err != nil {
		t.Fatal(err)
	}
	if bill.AmountDue.String() != "1250.00" {
		t.Fatalf("amount = %s", bill.AmountDue)
	}
	if !bill.HasMinimumDue || bill.MinimumDue.String() != "40.00" {
		t.Fatalf("minimum = %s (%v)", bill.MinimumDue, bill.HasMinimumDue)
	}
	if bill.DueOn != domain.NewDate(2026, time.October, 21) {
		t.Fatalf("due = %s", bill.DueOn)
	}
	if bill.MaskedNumber != "••••1234" || bill.ExternalID != "1234" {
		t.Fatalf("account = %q / %q", bill.ExternalID, bill.MaskedNumber)
	}

	// A rule that names no due date files on the day the issuer sent the
	// statement, not the day it was forwarded.
	onArrival := rule
	onArrival.DateLabel = ""
	bill, err = onArrival.ExtractBill(inner)
	if err != nil {
		t.Fatal(err)
	}
	if bill.DueOn != domain.NewDate(2026, time.September, 24) {
		t.Fatalf("due = %s, want the original's day", bill.DueOn)
	}
}

func TestAForwardsDateIsTheOriginalsOnlyWhenItReadsAsOne(t *testing.T) {
	received := time.Date(2026, time.September, 26, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		block string
		want  domain.Date
	}{
		{"\nDate: Wed, Sep 23, 2026 at 6:02 AM\nSubject: x", domain.NewDate(2026, time.September, 23)},
		{"\nSubject: x\nDate: September 22, 2026 at 6:02:11 AM UTC", domain.NewDate(2026, time.September, 22)},
		{"\nSent: yesterday\nSubject: x", domain.NewDate(2026, time.September, 26)},
		{"\nSent: Tuesday, September 29, 2026 6:02 AM", domain.NewDate(2026, time.September, 26)},
		{"\nSubject: no date at all", domain.NewDate(2026, time.September, 26)},
	} {
		if got := domain.DateOf(forwardedDate(tc.block, received)); got != tc.want {
			t.Errorf("%q: %s, want %s", tc.block, got, tc.want)
		}
	}
}
