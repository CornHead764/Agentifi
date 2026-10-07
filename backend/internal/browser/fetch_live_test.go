package browser

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The transport against a real Chromium, carrying the argument shape
// fetch_test.go stubs.
//
// Guarded by AGENTIFI_BROWSER_TEST for the reason engine_test.go gives: the
// image carries no browser, so a test that skips itself when one is missing
// would pass without exercising anything.
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/browser/
//
// A local server stands in for the provider, so the test needs no network and
// names no real host. It answers what it was asked, which is how the headers
// and the body are asserted from this side.
func TestAPageFetchCarriesTheHeadersAndTheBodyToARealServer(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/1/bill/Current" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<!doctype html><title>the light document</title>")
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"method":        r.Method,
			"authorization": r.Header.Get("Authorization"),
			"uid":           r.Header.Get("uid"),
			"body":          string(body),
		})
	}))
	t.Cleanup(service.Close)

	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	fetcher := &PageFetcher{
		Origin:   service.URL,
		Document: "/",
		Open:     func() (FetchSurface, error) { return engine.OpenFetchSurface(service.URL, "/") },
	}
	t.Cleanup(func() { require.NoError(t, fetcher.Close()) })

	req, err := http.NewRequest(http.MethodPost, service.URL+"/api/1/bill/Current",
		strings.NewReader(`{"accountNumbers":["1234"]}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer invented")
	req.Header.Set("uid", "2")

	response, err := fetcher.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var echoed struct {
		Method        string `json:"method"`
		Authorization string `json:"authorization"`
		UID           string `json:"uid"`
		Body          string `json:"body"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&echoed))
	require.Equal(t, http.MethodPost, echoed.Method)
	require.Equal(t, "Bearer invented", echoed.Authorization)
	require.Equal(t, "2", echoed.UID)
	require.Equal(t, `{"accountNumbers":["1234"]}`, echoed.Body)
}

// The statement host's shape: a signed landing address that sets a cookie and
// redirects to the file, which is served inline and only to that cookie.
// Headless Chrome shows an inline PDF in its viewer and hands the viewer's
// page back as the navigation's body, so the navigator reads the file again
// from inside the page rather than treating that body as the statement.
func TestANavigationToAnInlinePDFAnswersTheFileAndNotTheViewer(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	statement := []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj " +
		"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj " +
		"3 0 obj<</Type/Page/MediaBox[0 0 612 792]/Parent 2 0 R>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!doctype html><title>the light document</title>")
	}))
	t.Cleanup(portal.Close)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Landing.aspx":
			http.SetCookie(w, &http.Cookie{Name: "viewer", Value: "invented", Path: "/"})
			http.Redirect(w, r, "/statement", http.StatusFound)
		case "/statement":
			if _, err := r.Cookie("viewer"); err != nil {
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, "<html>no session</html>")
				return
			}
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", "inline")
			_, _ = w.Write(statement)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(host.Close)

	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	fetcher := &PageFetcher{
		Origin:   portal.URL,
		Document: "/",
		Open:     func() (FetchSurface, error) { return engine.OpenFetchSurface(portal.URL, "/") },
	}
	t.Cleanup(func() { require.NoError(t, fetcher.Close()) })

	req, err := http.NewRequest(http.MethodGet, host.URL+"/Landing.aspx?id=invented", nil)
	require.NoError(t, err)
	response, err := fetcher.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, statement, body)
}

// A statement in one portal's shape: the portal's POST answers a redirect to another
// origin, which answers the file as an attachment. The page's own fetch may not
// follow it; the browser still shows the redirect's Location, opening that in
// a page of its own answers the file, and so does submitting the post as a
// form, as a download rather than a response.
func TestARedirectToAnAttachmentIsReadableEveryWayButThePagesOwnFetch(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	statement := []byte("%PDF-1.4\n% an invented statement\n%%EOF\n")
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="invoice.pdf"`)
		_, _ = w.Write(statement)
	}))
	t.Cleanup(files.Close)
	target := files.URL + "/render/invented"
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pdf/download" && r.Method == http.MethodPost {
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!doctype html><title>documents</title><body></body>")
	}))
	t.Cleanup(portal.Close)

	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	context, err := engine.NewContext("", DefaultViewport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })
	opened, err := OpenPage(context)
	require.NoError(t, err)
	page := Wrap(opened)
	require.NoError(t, page.Goto(portal.URL+"/documents"))

	var mu sync.Mutex
	var seen []Response
	var downloads []Download
	page.OnResponse(func(r Response) { mu.Lock(); seen = append(seen, r); mu.Unlock() })
	page.OnDownload(func(d Download) { mu.Lock(); downloads = append(downloads, d); mu.Unlock() })
	heard := func() ([]Response, []Download) {
		mu.Lock()
		defer mu.Unlock()
		return append([]Response(nil), seen...), append([]Download(nil), downloads...)
	}

	ctx := t.Context()
	followed, err := CallFromPage(ctx, page, PageCall{
		URL: portal.URL + "/api/pdf/download", Method: "POST", Read: ReadBytes,
	})
	require.NoError(t, err)
	require.Contains(t, followed.Error, "Failed to fetch")

	manual, err := CallFromPage(ctx, page, PageCall{
		URL: portal.URL + "/api/pdf/download", Method: "POST", Redirect: "manual", Read: ReadBytes,
	})
	require.NoError(t, err)
	require.True(t, manual.Redirected)
	location := ""
	require.Eventually(t, func() bool {
		responses, _ := heard()
		for _, response := range responses {
			if response.Status() == http.StatusFound {
				location = response.Header("location")
			}
		}
		return location != ""
	}, 5*time.Second, 50*time.Millisecond)
	require.Equal(t, target, location)

	status, _, body, err := page.Bytes(location)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, statement, body)

	_, err = page.Evaluate(`(action) => {
  const form = document.createElement('form');
  form.method = 'POST';
  form.action = action;
  document.body.appendChild(form);
  form.submit();
}`, portal.URL+"/api/pdf/download")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, files := heard()
		return len(files) > 0
	}, 10*time.Second, 50*time.Millisecond)
	_, saved := heard()
	require.Equal(t, target, saved[0].URL())
	file, err := saved[0].Bytes()
	require.NoError(t, err)
	require.Equal(t, statement, file)
}
