package billmail

import (
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// A household's own rule against a receipt of the shape one arrives in.
// Invented throughout: the employer, the cafe, the figures and the receipt id.

const lunchReceipt = "Your receipt from Some Cafe\n" +
	"Receipt Date: 9/27/24\n" +
	"Receipt Total: $4.50\n" +
	"\n" +
	"Item              Qty   Price\n" +
	"Soup               1     2.50\n" +
	"Coffee             1     2.00\n" +
	"\n" +
	"ReceiptID: AB1234567\n" +
	"Paid by payroll deduction.\n"

func lunchMail() Message {
	return Message{
		ID:         "<r1@mail.example.invalid>",
		Sender:     "receipts@example.invalid",
		Subject:    "Cafe Receipt",
		ReceivedAt: time.Date(2024, time.September, 28, 13, 0, 0, 0, time.UTC),
		Text:       lunchReceipt,
	}
}

func lunchRule() Rule {
	return Rule{
		Name: "Lunch", Sender: "@example.invalid", SubjectContains: "Cafe Receipt",
		AmountLabel: "Receipt Total", DateLabel: "Receipt Date",
		ReferenceLabel: "ReceiptID", PayeeLabel: "Your receipt from",
	}
}

func TestARuleReadsTheFiguresOffItsOwnReceipt(t *testing.T) {
	rule := lunchRule()
	if !rule.Match(lunchMail()) {
		t.Fatal("the rule did not claim the mail it was written for")
	}

	read, err := rule.Extract(lunchMail())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !read.Amount.Equal(domain.MustFromString("4.50")) {
		t.Fatalf("amount = %s", read.Amount)
	}
	if !read.HasDate || read.Date != domain.NewDate(2024, time.September, 27) {
		t.Fatalf("date = %s, has = %v", read.Date, read.HasDate)
	}
	if read.Reference != "AB1234567" {
		t.Fatalf("reference = %q", read.Reference)
	}
	if read.Payee != "Some Cafe" {
		t.Fatalf("payee = %q", read.Payee)
	}
}

func TestARuleThatDoesNotMatchIsNotTheRuleForTheMail(t *testing.T) {
	rule := lunchRule()
	for _, m := range []Message{
		{Sender: "receipts@elsewhere.invalid", Subject: "Cafe Receipt", Text: lunchReceipt},
		{Sender: "receipts@example.invalid", Subject: "Lunch on Thursday?", Text: lunchReceipt},
	} {
		if rule.Match(m) {
			t.Fatalf("%q from %q was claimed", m.Subject, m.Sender)
		}
	}

	// The body rule is the third one, and it reads the body a parser would.
	body := lunchRule()
	body.BodyContains = "payroll deduction"
	if !body.Match(lunchMail()) {
		t.Fatal("the body rule did not hold against a body that carries the phrase")
	}
	body.BodyContains = "reimbursed by the company"
	if body.Match(lunchMail()) {
		t.Fatal("the body rule held against a body that does not carry the phrase")
	}
}

func TestAPatternReadsWhatNoLabelReaches(t *testing.T) {
	// The escape hatch: a figure the mail writes in a sentence rather than
	// behind a label of its own.
	rule := Rule{
		Name:             "Lunch",
		AmountPattern:    `charged ([0-9.]+) to your account`,
		DatePattern:      `on (\d{1,2}/\d{1,2}/\d{4})`,
		ReferencePattern: `confirmation ([A-Z0-9]+)`,
		Payee:            "Some Cafe",
	}
	read, err := rule.Extract(Message{
		Text: "We charged 4.50 to your account on 9/27/2024, confirmation AB1234567.\n",
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !read.Amount.Equal(domain.MustFromString("4.50")) {
		t.Fatalf("amount = %s", read.Amount)
	}
	if !read.HasDate || read.Date != domain.NewDate(2024, time.September, 27) {
		t.Fatalf("date = %s, has = %v", read.Date, read.HasDate)
	}
	if read.Reference != "AB1234567" || read.Payee != "Some Cafe" {
		t.Fatalf("reference = %q, payee = %q", read.Reference, read.Payee)
	}
}

func TestARuleWithNoDateSourceLeavesTheDayToTheReader(t *testing.T) {
	rule := lunchRule()
	rule.DateLabel = ""
	read, err := rule.Extract(lunchMail())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if read.HasDate {
		t.Fatalf("a rule that names no date source read %s", read.Date)
	}

	// So does a rule whose date source the mail does not answer.
	rule.DateLabel = "Service Date"
	read, err = rule.Extract(lunchMail())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if read.HasDate {
		t.Fatalf("a label the mail does not carry read %s", read.Date)
	}
}

func TestAnAmountNothingCanReadNamesTheFieldItLookedAt(t *testing.T) {
	rule := lunchRule()
	rule.AmountLabel = "Amount Charged"
	if _, err := rule.Extract(lunchMail()); err == nil {
		t.Fatal("a mail with no readable amount was extracted")
	} else if got := err.Error(); got != `no amount follows "Amount Charged" in this mail` {
		t.Fatalf("error = %q", got)
	}

	// A rule that names no amount source at all cannot run on any message.
	none := Rule{Name: "Lunch", Payee: "Some Cafe"}
	if err := none.Check(); err == nil {
		t.Fatal("a rule with no amount source passed its own check")
	}
	if _, err := none.Extract(lunchMail()); err == nil {
		t.Fatal("a rule with no amount source extracted something")
	}
}

func TestAPatternThatWillNotCompileIsRefusedBeforeItIsStored(t *testing.T) {
	rule := lunchRule()
	rule.AmountPattern = `total: ([0-9`
	if err := rule.Check(); err == nil {
		t.Fatal("an unclosed group passed the check")
	}
	// A pattern with nothing to capture reads the empty string forever.
	rule.AmountPattern = `total: [0-9.]+`
	if err := rule.Check(); err == nil {
		t.Fatal("a pattern with no capture group passed the check")
	}
}

// A water utility's bill-ready mail, of the shape a small provider with no
// portal worth signing in to sends one. Invented throughout.
const waterBillHTML = `<html><head><style>td { padding: 4px }</style></head><body>
<p>Your Example Water bill is ready.</p>
<table>
<tr><td>Account number:</td><td>5550 0056 78</td></tr>
<tr><td>Statement date:</td><td>October 2, 2026</td></tr>
<tr><td>Amount due:</td><td>$60.00</td></tr>
<tr><td>Due date:</td><td>October 23, 2026</td></tr>
</table>
<p>Questions? Reply to this mail.</p></body></html>`

func waterBillMail() Message {
	return Message{
		ID: "<w1@mail.example.invalid>", Sender: "billing@water.example.invalid",
		Subject:    "Your water bill is ready",
		ReceivedAt: time.Date(2026, time.October, 2, 14, 0, 0, 0, time.UTC),
		HTML:       waterBillHTML,
	}
}

func waterBillRule() Rule {
	return Rule{
		Name: "Water bill", Sender: "@water.example.invalid", SubjectContains: "bill is ready",
		AmountLabel: "Amount due", DateLabel: "Due date", IssuedLabel: "Statement date",
		ReferenceLabel: "Account number",
	}
}

func TestABillRuleReadsAnHTMLOnlyBill(t *testing.T) {
	rule := waterBillRule()
	if !rule.Match(waterBillMail()) {
		t.Fatal("the rule did not claim the bill it was written for")
	}
	bill, err := rule.ExtractBill(waterBillMail())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !bill.AmountDue.Equal(domain.MustFromString("60.00")) {
		t.Fatalf("amount due = %s", bill.AmountDue)
	}
	if bill.DueOn != domain.NewDate(2026, time.October, 23) {
		t.Fatalf("due = %s", bill.DueOn)
	}
	if bill.IssuedOn != domain.NewDate(2026, time.October, 2) {
		t.Fatalf("issued = %s", bill.IssuedOn)
	}
	// The three groups are one number, and the words after it are not.
	if bill.ExternalID != "5550005678" || bill.MaskedNumber != "••••5678" {
		t.Fatalf("account = %q, %q", bill.ExternalID, bill.MaskedNumber)
	}
}

// A minimum payment is a statement's, and optional: a bill that states none
// is a bill without one, whatever the rule asked for.
func TestABillRuleReadsAMinimumPaymentOnlyWhereTheMailStatesOne(t *testing.T) {
	rule := waterBillRule()
	bill, err := rule.ExtractBill(waterBillMail())
	if err != nil || bill.HasMinimumDue {
		t.Fatalf("a rule that asks for no minimum read one: %+v, %v", bill, err)
	}
	rule.MinimumLabel = "Minimum payment"
	bill, err = rule.ExtractBill(waterBillMail())
	if err != nil || bill.HasMinimumDue {
		t.Fatalf("a mail that states no minimum was given one: %+v, %v", bill, err)
	}

	mail := waterBillMail()
	mail.HTML = strings.Replace(waterBillHTML, "<tr><td>Due date:",
		"<tr><td>Minimum payment:</td><td>$25.00</td></tr><tr><td>Due date:", 1)
	bill, err = rule.ExtractBill(mail)
	if err != nil || !bill.HasMinimumDue || !bill.MinimumDue.Equal(domain.MustFromString("25.00")) {
		t.Fatalf("minimum = %s (%v), %v", bill.MinimumDue, bill.HasMinimumDue, err)
	}

	rule.MinimumLabel, rule.MinimumPattern = "", `minimum: ([0-9`
	if err := rule.Check(); err == nil || !strings.Contains(err.Error(), "minimum_pattern") {
		t.Fatalf("a minimum pattern that will not compile passed the check: %v", err)
	}
}

func TestABillRuleThatNamesADueDateTheMailLacksFails(t *testing.T) {
	rule := waterBillRule()
	rule.DateLabel = "Pay by"
	if _, err := rule.ExtractBill(waterBillMail()); err == nil {
		t.Fatal("a bill whose due date could not be read was filed on some other day")
	} else if got := err.Error(); got != `no due date follows "Pay by" in this mail` {
		t.Fatalf("error = %q", got)
	}

	// Naming no due date at all is the mail that is its own payment: the day
	// it arrived is the day it is due.
	rule.DateLabel = ""
	bill, err := rule.ExtractBill(waterBillMail())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if bill.DueOn != domain.NewDate(2026, time.October, 2) {
		t.Fatalf("due = %s", bill.DueOn)
	}
}

func TestABillRuleReadsTheAccountNumberAsTheMailWritesIt(t *testing.T) {
	for _, tc := range []struct {
		written, external, masked string
	}{
		{"5550 0056 78 (Residential)", "5550005678", "••••5678"},
		{"ending in 1234", "1234", "••••1234"},
		{"••••1234", "", "••••1234"},
		{"XXXX-XXXX-1234", "", "••••1234"},
		{"AB-21234 for 14 Sample Street", "AB21234", "••••1234"},
		{"on file", "", ""},
		{"12", "", ""},
	} {
		external, masked := accountReference(tc.written)
		if external != tc.external || masked != tc.masked {
			t.Errorf("%q read as %q, %q; want %q, %q",
				tc.written, external, masked, tc.external, tc.masked)
		}
	}
}

func TestABillRuleReadsAPlainTextBillWithPatterns(t *testing.T) {
	rule := Rule{
		Name: "Storage unit", Sender: "office@storage.example.invalid",
		AmountPattern:    `Balance of \$([0-9.,]+)`,
		DatePattern:      `due on (\d{1,2}/\d{1,2}/\d{4})`,
		ReferencePattern: `Unit (\S+)`,
	}
	bill, err := rule.ExtractBill(Message{
		Sender: "office@storage.example.invalid", Subject: "Invoice",
		ReceivedAt: time.Date(2026, time.October, 1, 8, 0, 0, 0, time.UTC),
		Text:       "Unit B-0000 has a Balance of $100.00 due on 10/15/2026.",
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !bill.AmountDue.Equal(domain.MustFromString("100.00")) || bill.DueOn != domain.NewDate(2026, time.October, 15) {
		t.Fatalf("bill = %s due %s", bill.AmountDue, bill.DueOn)
	}
	if bill.ExternalID != "B0000" || bill.MaskedNumber != "••••0000" {
		t.Fatalf("account = %q, %q", bill.ExternalID, bill.MaskedNumber)
	}
	if !bill.IssuedOn.IsZero() {
		t.Fatalf("a statement date nobody asked for was read: %s", bill.IssuedOn)
	}
}

func TestANotesLabelKeepsTheItemsOfAPlainTextReceipt(t *testing.T) {
	rule := lunchRule()
	rule.NotesLabel = "Receipt Total"
	want := "Item Qty Price\nSoup 1 2.50\nCoffee 1 2.00"

	read, err := rule.Extract(lunchMail())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if read.Section != want {
		t.Fatalf("with no end label, section = %q, want %q", read.Section, want)
	}

	rule.NotesLabel, rule.NotesEndLabel = "Item", "ReceiptID"
	read, err = rule.Extract(lunchMail())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if read.Section != "Soup 1 2.50\nCoffee 1 2.00" {
		t.Fatalf("up to the end label, section = %q", read.Section)
	}
}

// Invented: the shop, the items and the figures.
const bakeryReceiptHTML = `<html><body>
<p>Thanks for stopping by Example Bakery</p>
<table>
<tr><td colspan="3"><b>Your order</b></td></tr>
<tr><th>Item</th><th>Qty</th><th>Price</th></tr>
<tr><td>Rye loaf</td><td>1</td><td>$5.50</td></tr>
<tr>
  <td>Almond&nbsp;croissant</td>
  <td>2</td>
  <td>$7.00</td>
</tr>
<tr><td colspan="2">Subtotal</td><td>$12.50</td></tr>
<tr><td colspan="2">Order total</td><td>$12.50</td></tr>
</table>
</body></html>`

func TestANotesLabelKeepsTheRowsOfAnHTMLTable(t *testing.T) {
	rule := Rule{
		Name: "Bakery", AmountLabel: "Order total",
		NotesLabel: "Your order", NotesEndLabel: "Subtotal",
	}
	read, err := rule.Extract(Message{HTML: bakeryReceiptHTML})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	want := "Item Qty Price\nRye loaf 1 $5.50\nAlmond croissant 2 $7.00"
	if read.Section != want {
		t.Fatalf("section = %q, want %q", read.Section, want)
	}
}

func TestANotesSectionTheMailDoesNotCarryIsLeftOut(t *testing.T) {
	for _, bounds := range [][2]string{
		{"Items purchased", ""},
		{"Your order", "Balance carried forward"},
	} {
		rule := Rule{
			Name: "Bakery", AmountLabel: "Order total",
			NotesLabel: bounds[0], NotesEndLabel: bounds[1],
		}
		read, err := rule.Extract(Message{HTML: bakeryReceiptHTML})
		if err != nil {
			t.Fatalf("%v: a missing section failed the extraction: %v", bounds, err)
		}
		if read.Section != "" {
			t.Fatalf("%v: section = %q", bounds, read.Section)
		}
		if !read.Amount.Equal(domain.MustFromString("12.50")) {
			t.Fatalf("%v: amount = %s", bounds, read.Amount)
		}
	}
}

func TestANotesSectionIsCapped(t *testing.T) {
	var body strings.Builder
	body.WriteString("Total: $1.00\nItems\n")
	for i := 0; i < 50; i++ {
		body.WriteString("Sticker 1 $0.02\n")
	}
	rule := Rule{Name: "Stickers", AmountLabel: "Total", NotesLabel: "Items"}
	read, err := rule.Extract(Message{Text: body.String()})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	lines := strings.Split(read.Section, "\n")
	if len(lines) != 41 || lines[39] != "Sticker 1 $0.02" || lines[40] != "…" {
		t.Fatalf("%d lines, ending %q", len(lines), lines[len(lines)-1])
	}

	long := "Total: $1.00\nItems\n" + strings.Repeat("x", 2500) + "\n"
	read, err = rule.Extract(Message{Text: long})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if read.Section != strings.Repeat("x", 2000)+"…" {
		t.Fatalf("a long line kept %d characters", len([]rune(read.Section)))
	}
}
