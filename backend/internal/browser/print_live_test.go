package browser

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// A page printed with nothing on it allowed to reach out, against a real
// Chromium.
//
// Guarded by AGENTIFI_BROWSER_TEST for the reason engine_test.go gives:
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/browser/ -run Print
//
// A local server stands in for a tracker. The page names it every way a mail
// does — an image, a stylesheet, a script, a frame, a background, a font —
// and the server counting no request at all is the assertion. The page is
// handed over raw, not through billmail.Printable, so this is the browser's
// own three holds being tested and not the cleaning in front of them.
func TestAPrintedPageFetchesNothingAndRunsNothing(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	var asked atomic.Int32
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(tracker.Close)

	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	page := strings.NewReplacer("TRACKER", tracker.URL).Replace(`<!doctype html><html><head>
<link rel="stylesheet" href="TRACKER/mail.css">
<style>@font-face { font-family: F; src: url(TRACKER/font.woff) } body { font-family: F }
.hero { background: url(TRACKER/hero.png) }</style>
<script src="TRACKER/script.js"></script>
<script>document.title = "ran"; fetch("TRACKER/fetched"); new Image().src = "TRACKER/beacon";</script>
</head><body>
<img src="TRACKER/pixel.gif">
<div class="hero">Amount due: $60.00</div>
<iframe src="TRACKER/frame"></iframe>
</body></html>`)

	printed, err := engine.PrintPDF(page)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(printed, []byte("%PDF-")), "a PDF came back")
	require.Zero(t, asked.Load(), "the page reached the tracker")
}
