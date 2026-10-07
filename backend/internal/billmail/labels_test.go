package billmail

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// A label is the text a household typed off its mail, read literally. Each
// row puts one character a pattern would read as syntax into the label, with
// a decoy line the label would match if it were a pattern. Invented
// throughout.

func TestALabelIsMatchedAsTheTextItSays(t *testing.T) {
	for _, tc := range []struct {
		name, label, text, want string
	}{
		{"hash", "Receipt #:", "Receipt 7: decoy\nReceipt #: 4417\n", "4417"},
		{"hash against a digit", "Receipt #", "Receipt 7 decoy\nReceipt #4417\n", "4417"},
		{"dot", "Amt. due", "Amtx due 1.00\nAmt. due 2.00\n", "2.00"},
		{"parentheses", "Total (USD)", "Total USD 1.00\nTotal (USD) 3.25\n", "3.25"},
		{"dollar", "Paid $", "Paid 1.00\nPaid $ 4.75\n", "4.75"},
		{"dollar against the figure", "Paid $", "Paid 1.00\nPaid $4.75\n", "4.75"},
		{"star", "Total*", "Totallll 1.00\nTotal* 5.10\n", "5.10"},
		{"plus", "Tax+tip", "Taxxtip 1.00\nTax+tip 0.90\n", "0.90"},
		{"question mark", "Owed?", "Owe 1.00\nOwed? 6.40\n", "6.40"},
		{"brackets and pipe", "[Due|Owed]", "Due 1.00\n[Due|Owed] 7.05\n", "7.05"},
		{"backslash and caret", `Ref\^`, "Ref 1\nRef\\^ 8.15\n", "8.15"},
		{"case aside", "receipt #:", "RECEIPT #: 4417\n", "4417"},
		{"word edge still holds", "Due", "Overdue 1.00\nDue 9.20\n", "9.20"},
		{"label absent", "Receipt #:", "Receipt: 4417\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := findAfterLabel(tc.text, tc.label); got != tc.want {
				t.Fatalf("findAfterLabel(%q) = %q, want %q", tc.label, got, tc.want)
			}
		})
	}
}

func TestANotesSectionIsBoundedByLiteralLabels(t *testing.T) {
	text := "Items (2)\nItems 2 decoy\nBread 3.00\nJam 2.50\nTotal ($): 5.50\nTotal $ decoy\n"
	for _, tc := range []struct {
		name, start, end, want string
	}{
		{"both labels carry syntax", "Items (2)", "Total ($):", "Items 2 decoy\nBread 3.00\nJam 2.50"},
		{"an end that only a pattern would find is no end", "Items (2)", "Total (.):", ""},
		{"a start that only a pattern would find is no start", "Items .2.", "Total ($):", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sectionAfterLabel(text, tc.start, tc.end); got != tc.want {
				t.Fatalf("sectionAfterLabel(%q, %q) = %q, want %q", tc.start, tc.end, got, tc.want)
			}
		})
	}
}

func TestARuleReadsEveryFieldOffLabelsWithSyntaxInThem(t *testing.T) {
	mail := Message{
		ID:         "<r2@mail.example.invalid>",
		Sender:     "receipts@example.invalid",
		Subject:    "Your order",
		ReceivedAt: time.Date(2024, time.October, 3, 9, 0, 0, 0, time.UTC),
		Text: "Sold by: Example Hardware (Main St.)\n" +
			"Order date (local): 10/02/2024\n" +
			"Order #: HX-20417\n" +
			"Total ($): 18.40\n" +
			"Items [1+]\n" +
			"Hinge 2 6.20\n" +
			"Screws* 1 6.00\n" +
			"End?\n",
	}
	rule := Rule{
		AmountLabel: "Total ($):", DateLabel: "Order date (local):", ReferenceLabel: "Order #:",
		PayeeLabel: "Sold by:", NotesLabel: "Items [1+]", NotesEndLabel: "End?",
	}
	read, err := rule.Extract(mail)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !read.Amount.Equal(domain.MustFromString("18.40")) {
		t.Fatalf("amount = %s", read.Amount)
	}
	if !read.HasDate || read.Date != domain.NewDate(2024, time.October, 2) {
		t.Fatalf("date = %s, has = %v", read.Date, read.HasDate)
	}
	if read.Reference != "HX-20417" {
		t.Fatalf("reference = %q", read.Reference)
	}
	if read.Payee != "Example Hardware (Main St.)" {
		t.Fatalf("payee = %q", read.Payee)
	}
	if read.Section != "Hinge 2 6.20\nScrews* 1 6.00" {
		t.Fatalf("section = %q", read.Section)
	}
}
