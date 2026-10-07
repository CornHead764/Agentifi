package billmail

import (
	"bytes"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// pdfText answers "" for a scan, which the parser files for a person. The
// reader panics on malformed input, so the panic is recovered.
func pdfText(b []byte) (text string) {
	defer func() {
		if recover() != nil {
			text = ""
		}
	}()

	reader, err := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return ""
	}

	var out strings.Builder
	for number := 1; number <= reader.NumPage(); number++ {
		rows, err := reader.Page(number).GetTextByRow()
		if err != nil {
			continue
		}
		for _, row := range rows {
			var line strings.Builder
			for _, word := range row.Content {
				line.WriteString(word.S)
				line.WriteString(" ")
			}
			if flat := oneLine(line.String()); flat != "" {
				out.WriteString(flat)
				out.WriteString("\n")
			}
		}
	}
	if strings.TrimSpace(out.String()) != "" {
		return out.String()
	}

	// A page laid out with Td rather than Tm reports no rows; its text still
	// comes out flattened, and a label keeps its figure after it.
	flat, err := reader.GetPlainText()
	if err != nil {
		return ""
	}
	all, err := io.ReadAll(flat)
	if err != nil {
		return ""
	}
	return string(all)
}

func PDFText(b []byte) string { return pdfText(b) }
