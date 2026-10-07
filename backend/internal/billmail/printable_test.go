package billmail

import (
	"strings"
	"testing"
	"time"
)

// Invented, and hostile on purpose: nothing in it that would fetch or run may
// reach the page.

func TestAPrintedMailCarriesNothingThatFetchesOrRuns(t *testing.T) {
	page := Printable(Message{
		Sender: "billing@water.example.invalid", Subject: `Your bill <is> "ready"`,
		ReceivedAt: time.Date(2026, time.October, 2, 14, 5, 0, 0, time.UTC),
		HTML: `<!doctype html><html><head>
<link rel="stylesheet" href="https://cdn.tracker.example.invalid/mail.css">
<meta http-equiv="refresh" content="0;url=https://phish.example.invalid/">
<base href="https://tracker.example.invalid/">
<style>@import url("https://fonts.tracker.example.invalid/f.css"); td { color: #333 }
.hero { background: url(https://img.tracker.example.invalid/hero.png) }</style>
<script>fetch("https://tracker.example.invalid/opened")</script>
</head><body onload="track()">
<img src="https://tracker.example.invalid/pixel.gif" width="1" height="1">
<img srcset="https://img.tracker.example.invalid/logo-2x.png 2x" alt="logo">
<img src="data:image/png;base64,iVBORw0KGgo=" alt="inline logo">
<iframe src="https://tracker.example.invalid/frame"></iframe>
<table background="//img.tracker.example.invalid/bg.png"><tr><td>Amount due:</td><td>$60.00</td></tr></table>
<a href="javascript:alert(1)" onclick="track()">Pay now</a>
<div style="background-image: url('https://img.tracker.example.invalid/bg2.png')">Thanks</div>
<object data="https://tracker.example.invalid/x.swf"></object>
</body></html>`,
	})

	for _, forbidden := range []string{
		"tracker.example.invalid", "phish.example.invalid", "<script", "<iframe", "<object",
		"<link", "<base", "onload", "onclick", "javascript:", "@import", "http-equiv=\"refresh\"",
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the printable page still carries %q", forbidden)
		}
	}
	for _, kept := range []string{
		"Amount due:", "$60.00", "Pay now", "Thanks", `src="data:image/png;base64,iVBORw0KGgo="`,
		"td { color: #333 }",
		`http-equiv="Content-Security-Policy" content="default-src 'none'`,
		// The header, escaped: a subject is the sender's text too.
		"billing@water.example.invalid", "Your bill &lt;is&gt; &#34;ready&#34;",
		"Fri, 2 Oct 2026 14:05 UTC",
	} {
		if !strings.Contains(page, kept) {
			t.Errorf("the printable page lost %q", kept)
		}
	}
}

func TestAPlainTextMailIsPrintedAsItWasWritten(t *testing.T) {
	page := Printable(Message{
		Sender: "office@storage.example.invalid", Subject: "Invoice",
		ReceivedAt: time.Date(2026, time.October, 1, 8, 0, 0, 0, time.UTC),
		Text:       "Unit B-0000\nBalance: $100.00 <due> 10/15/2026",
	})
	if !strings.Contains(page, "Unit B-0000\nBalance: $100.00 &lt;due&gt; 10/15/2026") {
		t.Fatalf("the text was not kept as written:\n%s", page)
	}
}
