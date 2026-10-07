package browser

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The screencast, against a real Chromium.
//
// Guarded by AGENTIFI_BROWSER_TEST for the reason engine_test.go gives: the
// image carries no browser, so a test that skips itself when one is missing
// would pass without exercising anything.
//
//	PLAYWRIGHT_DRIVER_PATH=… AGENTIFI_BROWSER_TEST=1 go test ./internal/browser/
//
// It proves the frame handler's payload shape under playwright-go, which is
// `map[string]any` and not a typed frame, and the one thing no fake can: that
// a click played at a coordinate lands on the control that was drawn there.
func TestAScreencastPaintsARealFrameAndAClickLandsWhereItWasDrawn(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// One big button at a known place, and a page that says so when it is
		// pressed. The colour is there so the JPEG has something to compress:
		// a white page paints a frame too, but a coloured one proves the
		// picture is of this page.
		_, _ = io.WriteString(w, `<!doctype html><title>Live test</title><body style="margin:0">
<div style="height:120px;background:#2b6cb0"></div>
<button id="go" style="position:absolute;left:40px;top:200px;width:240px;height:60px">Continue</button>
<p id="said">waiting</p>
<script>
  document.getElementById('go').addEventListener('click', () => {
    document.getElementById('said').textContent = 'pressed';
    document.title = 'Pressed';
  });
</script>
</body>`)
	}))
	t.Cleanup(site.Close)

	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	viewport := Size{Width: 800, Height: 600}
	context, err := engine.NewContext("", viewport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })
	opened, err := OpenPage(context)
	require.NoError(t, err)
	page := Wrap(opened)

	view, err := StartLiveView(page, viewport)
	require.NoError(t, err)
	t.Cleanup(view.Close)
	require.NoError(t, page.Goto(site.URL))

	// A frame, with bytes a decoder would take: an empty `data` is how the
	// payload shape would be wrong.
	//
	// Waited for as a *painted* frame rather than as any frame: the screenshot
	// that stands in for the screencast can answer first.
	frame := waitForPaintedFrame(t, view, 0)
	require.NotEmpty(t, frame.Image)
	require.Greater(t, view.Painted(), 0, "the CDP screencast is what painted this")
	decoded, err := base64.StdEncoding.DecodeString(frame.Image)
	require.NoError(t, err)
	require.Greater(t, len(decoded), 500, "a frame of a painted page is not a few bytes")
	require.Equal(t, []byte{0xff, 0xd8, 0xff}, decoded[:3], "a JPEG starts with its own marker")
	require.Greater(t, frame.Width, 0)
	require.Greater(t, frame.Height, 0)

	// And the click: the button is drawn at 40,200 and is 240 by 60, so its
	// middle is 160,230 in the same pixels the frame was painted in.
	view.Play([]LiveInput{{Type: "click", X: 160, Y: 230}})
	require.Eventually(t, func() bool {
		title, err := page.Title()
		return err == nil && title == "Pressed"
	}, 5*time.Second, 50*time.Millisecond, "the click reached the button it was drawn on")

	// The page moved, so the screencast paints again.
	moved := waitForFrame(t, view, frame.Seq)
	require.NotEmpty(t, moved.Image)

	// And the snapshot reads the page the person is looking at, labels and
	// all, with nothing of the session in it.
	read := Snapshot("live-test", page)
	require.Contains(t, read.Text, "pressed")
	require.Contains(t, read.Text, "--- interactive elements ---")
	require.Contains(t, read.Text, "button id=go  Continue")
	require.Equal(t, "Pressed", read.Title)
}

// waitForPaintedFrame is waitForFrame for the assertions that are about the
// screencast itself: it waits for a frame Chromium painted, not for whichever
// image arrives first.
func waitForPaintedFrame(t *testing.T, view *LiveView, after int) LiveFrame {
	t.Helper()
	var frame LiveFrame
	require.Eventually(t, func() bool {
		frame = view.Frame(after)
		return frame.Image != "" && view.Painted() > 0
	}, 15*time.Second, 100*time.Millisecond, "the live browser painted nothing from the screencast")
	return frame
}

// waitForFrame polls the view the way the dialog does.
func waitForFrame(t *testing.T, view *LiveView, after int) LiveFrame {
	t.Helper()
	var frame LiveFrame
	require.Eventually(t, func() bool {
		frame = view.Frame(after)
		return frame.Image != ""
	}, 15*time.Second, 100*time.Millisecond, "the live browser painted nothing")
	return frame
}
