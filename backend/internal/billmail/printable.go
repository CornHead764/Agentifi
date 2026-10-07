package billmail

import (
	"html"
	"regexp"
	"strings"
)

// A bill with no PDF is printed from its mail, since the body is not stored.
// The mail is untrusted: the printing browser runs with scripts off and
// requests refused, the page carries its own CSP, and everything that would
// fetch or run is stripped here too.

const printablePolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:"

var (
	dropElement = regexp.MustCompile(
		`(?is)<(script|iframe|object|embed|noscript|template|frameset|frame|applet)\b.*?</(script|iframe|object|embed|noscript|template|frameset|frame|applet)\s*>`)
	dropTag = regexp.MustCompile(
		`(?is)<(script|iframe|object|embed|link|meta|base|form|input|frame|applet)\b[^>]*>`)
	headElement     = regexp.MustCompile(`(?is)<head\b.*?</head\s*>`)
	styleBlock      = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	bodyInner       = regexp.MustCompile(`(?is)<body\b[^>]*>(.*)</body\s*>`)
	outerTag        = regexp.MustCompile(`(?is)</?(html|body|!doctype)\b[^>]*>`)
	remoteAttribute = regexp.MustCompile(
		`(?is)\s(src|srcset|background|href|action|poster|lowsrc|dynsrc|xlink:href)\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	handlerAttribute = regexp.MustCompile(`(?is)\son[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	cssRemote        = regexp.MustCompile(`(?is)url\(\s*['"]?\s*(https?:|//|javascript:)[^)]*\)|@import[^;]*;`)
)

func Printable(m Message) string {
	out := &strings.Builder{}
	out.WriteString(`<!doctype html><html><head><meta charset="utf-8">`)
	out.WriteString(`<meta http-equiv="Content-Security-Policy" content="` + printablePolicy + `">`)
	out.WriteString(`<title>` + html.EscapeString(m.Subject) + `</title>`)
	out.WriteString(`<style>
.agentifi-mail-header { font: 12px/1.5 sans-serif; color: #222; border-bottom: 1px solid #999;
  margin: 0 0 16px; padding: 0 0 8px; }
.agentifi-mail-header th { text-align: left; padding-right: 12px; font-weight: 600; vertical-align: top; }
.agentifi-mail-text { font: 12px/1.5 monospace; white-space: pre-wrap; overflow-wrap: anywhere; }
</style>`)
	body, styles := printableBody(m)
	out.WriteString(styles)
	out.WriteString(`</head><body><table class="agentifi-mail-header">`)
	for _, row := range [][2]string{
		{"From", m.Sender},
		{"Subject", m.Subject},
		{"Date", m.ReceivedAt.UTC().Format("Mon, 2 Jan 2006 15:04 MST")},
	} {
		out.WriteString(`<tr><th>` + row[0] + `</th><td>` + html.EscapeString(row[1]) + `</td></tr>`)
	}
	out.WriteString(`</table>`)
	out.WriteString(body)
	out.WriteString(`</body></html>`)
	return out.String()
}

// printableBody returns the mail's style blocks separately, for the head.
func printableBody(m Message) (string, string) {
	if strings.TrimSpace(m.HTML) == "" {
		return `<div class="agentifi-mail-text">` + html.EscapeString(m.Text) + `</div>`, ""
	}
	page := dropElement.ReplaceAllString(m.HTML, "")
	page = comment.ReplaceAllString(page, "")
	var styles strings.Builder
	for _, block := range styleBlock.FindAllString(page, -1) {
		styles.WriteString(cssRemote.ReplaceAllString(block, ""))
	}
	page = styleBlock.ReplaceAllString(page, "")
	page = headElement.ReplaceAllString(page, "")
	if inner := bodyInner.FindStringSubmatch(page); inner != nil {
		page = inner[1]
	}
	page = outerTag.ReplaceAllString(page, "")
	page = dropTag.ReplaceAllString(page, "")
	page = handlerAttribute.ReplaceAllString(page, "")
	page = remoteAttribute.ReplaceAllStringFunc(page, keepInlineData)
	page = cssRemote.ReplaceAllString(page, "")
	return page, styles.String()
}

func keepInlineData(attribute string) string {
	_, value, _ := strings.Cut(attribute, "=")
	value = strings.Trim(strings.TrimSpace(value), `"'`)
	if strings.HasPrefix(strings.ToLower(value), "data:") && !strings.Contains(strings.ToLower(attribute), "href") {
		return attribute
	}
	return ""
}
