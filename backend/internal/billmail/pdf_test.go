package billmail

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// onePagePDF builds fixtures rather than capturing them: a real invoice is
// somebody's address and account.
func onePagePDF(lines []string) []byte {
	var content strings.Builder
	content.WriteString("BT\n/F1 12 Tf\n")
	for i, line := range lines {
		fmt.Fprintf(&content, "1 0 0 1 72 %d Tm\n(%s) Tj\n", 720-18*i, pdfEscape(line))
	}
	content.WriteString("ET\n")

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()),
	}

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, body := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}

	// The xref entries are twenty bytes each, trailing space included; a reader
	// seeks by that width and reads the wrong object if one is short.
	startxref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, at := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", at)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, startxref)
	return out.Bytes()
}

func pdfEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(s)
}

func TestPDFTextReadsEachRow(t *testing.T) {
	text := pdfText(onePagePDF([]string{"Invoice 4821", "Total Due  $100.00", "Due Date  05/01/2026"}))
	for _, want := range []string{"Invoice 4821", "Total Due $100.00", "Due Date 05/01/2026"} {
		if !strings.Contains(text, want) {
			t.Fatalf("pdfText missing %q, got %q", want, text)
		}
	}
}

func TestPDFTextOnRubbishIsEmpty(t *testing.T) {
	if text := pdfText([]byte("not a pdf at all")); text != "" {
		t.Fatalf("pdfText on rubbish = %q, want empty", text)
	}
}
