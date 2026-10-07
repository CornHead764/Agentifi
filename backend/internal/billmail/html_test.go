package billmail

import (
	"reflect"
	"testing"
)

func TestStripHTMLBreaksBlocksAndKeepsCellsTogether(t *testing.T) {
	got := StripHTML(`<html><head><style>p { color: #fff }</style></head><body>
		<p>Hello   Alex Example<br>Your bill is ready</p>
		<table><tr><td>Total&nbsp;Due</td><td>$41.00</td></tr></table>
		<!-- a tracking pixel --><script>track(1 > 0)</script>
		<ul><li>One</li><li>Two &amp; a half</li></ul></body></html>`)

	want := "Hello Alex Example\nYour bill is ready\nTotal Due $41.00\nOne\nTwo & a half"
	if got != want {
		t.Fatalf("StripHTML =\n%q\nwant\n%q", got, want)
	}
}

func TestStripHTMLReadsASourceLineBreakAsASpaceOutsidePre(t *testing.T) {
	got := StripHTML("<table><tr>\r\n  <td>Pot of\n tea</td>\n  <td>$3.00</td>\n</tr></table>" +
		"<pre>Scone  1\nJam    1</pre>")

	want := "Pot of tea $3.00\nScone 1\nJam 1"
	if got != want {
		t.Fatalf("StripHTML =\n%q\nwant\n%q", got, want)
	}
}

func TestTableCellsReadsEveryRow(t *testing.T) {
	got := TableCells(`<table>
		<tr><th>Account No.</th><th>Total Amount Due($)</th><th>Due Date</th></tr>
		<tr><td>30001234</td><td>150.00</td><td>04-15-2026</td></tr>
		<tr><td class="figure">30005678</td><td>87</td><td>04-15-2026</td></tr>
	</table>`)

	want := [][]string{
		{"Account No.", "Total Amount Due($)", "Due Date"},
		{"30001234", "150.00", "04-15-2026"},
		{"30005678", "87", "04-15-2026"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TableCells = %#v, want %#v", got, want)
	}
}

func TestBodyTextFallsBackToTheHTML(t *testing.T) {
	if got := bodyText(Message{HTML: "<p>Total Due $9.00</p>"}); got != "Total Due $9.00" {
		t.Fatalf("bodyText = %q", got)
	}
	if got := bodyText(Message{Text: "plain", HTML: "<p>markup</p>"}); got != "plain" {
		t.Fatalf("bodyText = %q, want the text part", got)
	}
}
