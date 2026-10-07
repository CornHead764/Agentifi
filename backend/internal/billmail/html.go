package billmail

import (
	"html"
	"regexp"
	"strings"
)

var (
	scriptOpen  = regexp.MustCompile(`(?is)<script[^>]*>`)
	scriptShut  = regexp.MustCompile(`(?is)</script\s*>`)
	styleOpen   = regexp.MustCompile(`(?is)<style[^>]*>`)
	styleShut   = regexp.MustCompile(`(?is)</style\s*>`)
	breakTag    = regexp.MustCompile(`(?is)<br\s*/?>|</p\s*>|</tr\s*>|</li\s*>|</div\s*>|</h[1-6]\s*>`)
	cellShut    = regexp.MustCompile(`(?is)</t[dh]\s*>`)
	anyTag      = regexp.MustCompile(`(?s)<[^>]*>`)
	comment     = regexp.MustCompile(`(?s)<!--.*?-->`)
	runOfSpace  = regexp.MustCompile(`[\p{Zs}\t\v\f\r]+`)
	rowTag      = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr\s*>`)
	cellTag     = regexp.MustCompile(`(?is)<t[dh][^>]*>(.*?)</t[dh]\s*>`)
	preBlock    = regexp.MustCompile(`(?is)<pre\b[^>]*>.*?</pre\s*>`)
	sourceBreak = regexp.MustCompile(`\r?\n`)
)

// StripHTML ends a cell with a space rather than a break, so a table row stays
// one line and a label keeps its figure beside it. A line break in the source
// is a space, as a browser shows it, except inside <pre>. An attribute value
// containing '>' is not handled.
func StripHTML(body string) string {
	body = cut(body, scriptOpen, scriptShut)
	body = cut(body, styleOpen, styleShut)
	body = comment.ReplaceAllString(body, "")
	body = preBlock.ReplaceAllStringFunc(body, func(block string) string {
		return sourceBreak.ReplaceAllString(block, "<br>")
	})
	body = sourceBreak.ReplaceAllString(body, " ")
	body = breakTag.ReplaceAllString(body, "\n")
	body = cellShut.ReplaceAllString(body, " ")
	body = anyTag.ReplaceAllString(body, "")
	body = html.UnescapeString(body)

	lines := strings.Split(body, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(runOfSpace.ReplaceAllString(line, " "))
		if line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// TableCells does not handle nested tables: the outer row ends at the inner
// row's close.
func TableCells(body string) [][]string {
	var rows [][]string
	for _, row := range rowTag.FindAllStringSubmatch(body, -1) {
		cells := []string{}
		for _, cell := range cellTag.FindAllStringSubmatch(row[1], -1) {
			cells = append(cells, oneLine(StripHTML(cell[1])))
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	return rows
}

func bodyText(m Message) string {
	if strings.TrimSpace(m.Text) != "" {
		return m.Text
	}
	return StripHTML(m.HTML)
}

func cut(body string, open, shut *regexp.Regexp) string {
	for {
		start := open.FindStringIndex(body)
		if start == nil {
			return body
		}
		rest := shut.FindStringIndex(body[start[1]:])
		if rest == nil {
			return body[:start[0]]
		}
		body = body[:start[0]] + body[start[1]+rest[1]:]
	}
}

func oneLine(s string) string {
	return strings.TrimSpace(runOfSpace.ReplaceAllString(strings.ReplaceAll(s, "\n", " "), " "))
}
