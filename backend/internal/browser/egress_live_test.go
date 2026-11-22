package browser

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/testenv"
	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"
)

// The invented site stands on loopback, which is the network the egress guard
// keeps every page from, so each way a page can reach it must leave the site
// unvisited. Guarded by AGENTIFI_BROWSER_TEST for the reason engine_test.go
// gives.
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/browser/ -run Loopback

func loopbackSite(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>inside</title><p>the household's own network</p>"))
	}))
	t.Cleanup(site.Close)
	return site, &hits
}

// reachFor has the page try the site every way it can by itself (an image, a
// fetch, a websocket and a frame) and waits for each attempt to end, then
// navigates there.
func reachFor(t *testing.T, page playwright.Page, address string) {
	t.Helper()
	require.NoError(t, page.SetContent(`<!doctype html><title>outside</title>`))
	_, err := page.Evaluate(`(address) => Promise.all([
		new Promise((done) => { const image = new Image(); image.onload = image.onerror = done; image.src = address + '/image.png'; }),
		fetch(address + '/fetched', { mode: 'no-cors' }).then(() => {}, () => {}),
		new Promise((done) => { const socket = new WebSocket(address.replace('http', 'ws') + '/socket'); socket.onclose = done; }),
		new Promise((done) => { const frame = document.createElement('iframe'); frame.onload = done; frame.src = address + '/framed'; document.body.appendChild(frame); }),
	])`, address)
	require.NoError(t, err)
	if answer, err := page.Goto(address + "/navigated"); err == nil && answer != nil {
		require.NotEqual(t, http.StatusOK, answer.Status())
	}
}

func TestAPageCannotReachALoopbackSite(t *testing.T) {
	testenv.Browser(t)
	site, hits := loopbackSite(t)
	engine := NewEngine(guardedSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	context, err := engine.NewContext("", DefaultViewport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })
	page, err := OpenPage(context)
	require.NoError(t, err)

	reachFor(t, page, site.URL)
	require.Zero(t, hits.Load())
}

func TestAProfilePageCannotReachALoopbackSite(t *testing.T) {
	testenv.Browser(t)
	site, hits := loopbackSite(t)
	engine := NewEngine(guardedSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	dir, err := ProfileDir(t.TempDir(), "connection-1")
	require.NoError(t, err)
	require.NoError(t, engine.Claim(dir))
	t.Cleanup(func() { engine.Release(dir) })
	context, err := engine.OpenProfile(dir, DefaultDevice(DefaultViewport, testUserAgent))
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })
	page, err := OpenPage(context)
	require.NoError(t, err)

	reachFor(t, page, site.URL)
	require.Zero(t, hits.Load())
}

// The allowance the other browser tests run under: the same reach lands, which
// is what shows the guard, not the page, kept the site unvisited above.
func TestTheTestAllowanceReachesALoopbackSite(t *testing.T) {
	testenv.Browser(t)
	site, hits := loopbackSite(t)
	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	context, err := engine.NewContext("", DefaultViewport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })
	page, err := OpenPage(context)
	require.NoError(t, err)

	answer, err := page.Goto(site.URL + "/navigated")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, answer.Status())
	require.NotZero(t, hits.Load())
}
