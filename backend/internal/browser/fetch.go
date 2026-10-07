package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
)

// A `fetch` that runs inside Chromium rather than in this process, because
// some providers' edges refuse Go's HTTP client with an HTML error page. The
// page is opened lazily on a light document at the provider's
// origin, so the browser sends the real Origin and keeps the edge's cookies. A
// cross-origin GET the page may not read (no CORS) falls back to navigating a
// second page to it.

// Fetcher is what a JSON provider module calls; `*http.Client` satisfies it.
type Fetcher interface {
	Do(req *http.Request) (*http.Response, error)
}

// Navigator goes to the address in a page of its own and takes what comes
// back, response or download.
type Navigator interface {
	Bytes(address string) (status int, contentType string, body []byte, err error)
}

type FetchSurface struct {
	Page      Page
	Navigator Navigator
	Close     func() error
}

// forbiddenHeaders are supplied by the browser; setting them makes the fetch
// itself refuse.
var forbiddenHeaders = map[string]bool{
	"origin": true, "referer": true, "user-agent": true, "host": true,
	"cookie": true, "content-length": true, "connection": true, "accept-encoding": true,
}

const FetchTimeout = 60 * time.Second

// FetchTimeoutMS is the time left before ctx's deadline, capped at
// FetchTimeout, for a script to arm its own AbortController with.
func FetchTimeoutMS(ctx context.Context) int64 {
	limit := FetchTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if left := time.Until(deadline); left < limit {
			limit = left
		}
	}
	if limit < time.Millisecond {
		limit = time.Millisecond
	}
	return limit.Milliseconds()
}

// EvaluateContext is page.Evaluate that answers when ctx ends rather than when
// the script does. The script keeps running in the page; one that fetches
// must also abort itself (FetchTimeoutMS), or it holds the page regardless.
func EvaluateContext(ctx context.Context, page Page, script string, arg any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type answer struct {
		value any
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		value, err := page.Evaluate(script, arg)
		done <- answer{value, err}
	}()
	select {
	case got := <-done:
		return got.value, got.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PageFetcher is a Fetcher for one provider session.
type PageFetcher struct {
	Origin string
	// Document is a light page at that origin to sit on.
	Document string
	// Open makes the surface, once, on the first call.
	Open func() (FetchSurface, error)

	mu      sync.Mutex
	surface *FetchSurface
}

func (f *PageFetcher) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	surface, err := f.open()
	if err != nil {
		return nil, err
	}
	call, err := pageRequest(req)
	if err != nil {
		return nil, err
	}
	answer, failed := CallFromPage(ctx, surface.Page, call)
	if failed == nil && answer.Error != "" {
		failed = errors.New(answer.Error)
	}
	if failed != nil {
		if ctx.Err() != nil || answer.TimedOut || !RefusedByBrowser(failed) ||
			!f.navigable(call) || surface.Navigator == nil {
			return nil, fmt.Errorf("browser: the page could not fetch %s: %w", call.URL, failed)
		}
		status, contentType, body, err := surface.Navigator.Bytes(call.URL)
		if err != nil {
			return nil, err
		}
		return responseFrom(req, status, map[string]string{"content-type": contentType}, body), nil
	}
	read, err := readPageAnswer(answer)
	if err != nil {
		return nil, err
	}
	return answeredFrom(req, read), nil
}

// RefusedByBrowser says whether a failed evaluation is the fetch's own network
// refusal — Chrome's "Failed to fetch", Firefox's "NetworkError" — which for a
// cross-origin GET is usually a response the page may not read, and so worth a
// navigation. Anything else (an abort, a closed or navigated page) is not.
func RefusedByBrowser(err error) bool {
	text := err.Error()
	return strings.Contains(text, "Failed to fetch") || strings.Contains(text, "NetworkError")
}

func (f *PageFetcher) Close() error {
	f.mu.Lock()
	surface := f.surface
	f.surface = nil
	f.mu.Unlock()
	if surface == nil || surface.Close == nil {
		return nil
	}
	return surface.Close()
}

func (f *PageFetcher) open() (*FetchSurface, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.surface != nil {
		return f.surface, nil
	}
	if f.Open == nil {
		return nil, fmt.Errorf("browser: this fetcher has no way to open a page")
	}
	surface, err := f.Open()
	if err != nil {
		return nil, err
	}
	f.surface = &surface
	return f.surface, nil
}

// navigable says whether a refused fetch is worth a navigation: only a GET, and
// only away from the origin the page is already on, where "may not read the
// response" is the likely reason rather than a refusal by the provider.
func (f *PageFetcher) navigable(call PageCall) bool {
	if call.Method != http.MethodGet {
		return false
	}
	target, err := url.Parse(call.URL)
	if err != nil {
		return false
	}
	home, err := url.Parse(f.Origin)
	if err != nil {
		return false
	}
	return target.Scheme+"://"+target.Host != home.Scheme+"://"+home.Host
}

// pageRequest reads the body as bytes; the browser's default credentials, not
// "include", because a cross-origin call that carries cookies needs the service
// to allow credentials explicitly, and the portal's own call does not.
func pageRequest(req *http.Request) (PageCall, error) {
	headers := map[string]string{}
	for name, values := range req.Header {
		if forbiddenHeaders[strings.ToLower(name)] || len(values) == 0 {
			continue
		}
		headers[name] = values[0]
	}
	call := PageCall{
		URL: req.URL.String(), Method: strings.ToUpper(req.Method), Headers: headers, Read: ReadBytes,
	}
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return PageCall{}, err
		}
		_ = req.Body.Close()
		if len(raw) > 0 {
			call.Body = string(raw)
		}
	}
	return call, nil
}

type fetchAnswer struct {
	Status int
	// URL is where the fetch ended after redirects; empty when the page did
	// not say.
	URL     string
	Headers map[string]string
	Body    []byte
}

func readPageAnswer(answer PageCallAnswer) (fetchAnswer, error) {
	body, err := answer.Bytes()
	if err != nil {
		return fetchAnswer{}, fmt.Errorf("browser: the page answered a body that is not base64: %w", err)
	}
	return fetchAnswer{Status: answer.Status, URL: answer.URL, Headers: answer.Headers, Body: body}, nil
}

// answeredFrom is the page's answer as a response. Like net/http's client, a
// followed redirect shows as a Request whose URL is where the fetch ended.
func answeredFrom(req *http.Request, answer fetchAnswer) *http.Response {
	response := responseFrom(req, answer.Status, answer.Headers, answer.Body)
	if answer.URL == "" || answer.URL == req.URL.String() {
		return response
	}
	if final, err := url.Parse(answer.URL); err == nil {
		redirected := req.Clone(req.Context())
		redirected.URL = final
		response.Request = redirected
	}
	return response
}

func responseFrom(req *http.Request, status int, headers map[string]string, body []byte) *http.Response {
	header := http.Header{}
	for name, value := range headers {
		header.Set(name, value)
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

type contextNavigator struct {
	context playwright.BrowserContext
}

const navigationWait = 20 * time.Second

// Bytes navigates to the address and takes the response's body or the
// download. A navigation that turns into a download answers an error by
// design, so Goto's error is ignored.
func (n *contextNavigator) Bytes(address string) (int, string, []byte, error) {
	page, err := n.context.NewPage()
	if err != nil {
		return 0, "", nil, err
	}
	defer page.Close()

	downloads := make(chan playwright.Download, 1)
	page.On("download", func(download playwright.Download) {
		select {
		case downloads <- download:
		default:
		}
	})

	response, _ := page.Goto(address, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateCommit,
	})
	if response != nil {
		if body, err := response.Body(); err == nil && len(body) > 0 {
			contentType := response.Headers()["content-type"]
			if viewedPDF(contentType, body) {
				return rereadInPage(page, response.URL())
			}
			return response.Status(), contentType, body, nil
		}
	}
	select {
	case download := <-downloads:
		body, err := downloadBytes(download)
		if err != nil {
			return 0, "", nil, err
		}
		return http.StatusOK, "application/octet-stream", body, nil
	case <-time.After(navigationWait):
		return 0, "", nil, fmt.Errorf(
			"browser: nothing came back from %s: no response body and no download", WithoutQuery(address))
	}
}

// downloadWait bounds a download that has started and never finishes.
const downloadWait = 30 * time.Second

// downloadBytes saves the file rather than reading its Path: a browser reached
// over a connection (Camoufox) keeps its downloads on its own machine, where
// Path answers an error and SaveAs streams the file across.
func downloadBytes(download playwright.Download) ([]byte, error) {
	dir, err := os.MkdirTemp("", "agentifi-download-")
	if err != nil {
		return nil, fmt.Errorf("browser: no directory to save a download in: %w", err)
	}
	defer os.RemoveAll(dir)
	defer func() { _ = download.Delete() }()
	path := filepath.Join(dir, "download")
	saved := make(chan error, 1)
	go func() { saved <- download.SaveAs(path) }()
	select {
	case err := <-saved:
		if err != nil {
			return nil, fmt.Errorf("browser: the download from %s could not be saved: %w", WithoutQuery(download.URL()), err)
		}
	case <-time.After(downloadWait):
		_ = download.Cancel()
		return nil, fmt.Errorf("browser: the download from %s did not finish within %s", WithoutQuery(download.URL()), downloadWait)
	}
	return os.ReadFile(path)
}

// viewedPDF says a navigation's body is Chrome's PDF viewer wrapper rather
// than the file: headless Chrome answers an inline PDF with the viewer's HTML.
func viewedPDF(contentType string, body []byte) bool {
	return strings.Contains(strings.ToLower(contentType), "application/pdf") &&
		!bytes.HasPrefix(body, []byte("%PDF-"))
}

// rereadInPage fetches the address the viewer is showing from inside that
// page, which is by then on the file's own origin, so the call is same-origin.
func rereadInPage(page playwright.Page, address string) (int, string, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), navigationWait)
	defer cancel()
	answer, err := CallFromPage(ctx, Wrap(page), PageCall{URL: address, Method: http.MethodGet, Read: ReadBytes})
	if err == nil && answer.Error != "" {
		err = errors.New(answer.Error)
	}
	if err != nil {
		return 0, "", nil, fmt.Errorf("browser: %s opened in the PDF viewer and could not be read again: %w",
			WithoutQuery(address), err)
	}
	read, err := readPageAnswer(answer)
	if err != nil {
		return 0, "", nil, err
	}
	return read.Status, read.Headers["content-type"], read.Body, nil
}

// OpenFetchSurface's navigator shares the page's context, so a download
// carries the session's cookies.
func (e *Engine) OpenFetchSurface(origin, document string) (FetchSurface, error) {
	context, err := e.NewContext("", DefaultViewport)
	if err != nil {
		return FetchSurface{}, err
	}
	return fetchSurfaceIn(context, origin, document)
}

func fetchSurfaceIn(context playwright.BrowserContext, origin, document string) (FetchSurface, error) {
	page, err := context.NewPage()
	if err != nil {
		_ = context.Close()
		return FetchSurface{}, err
	}
	target, err := url.Parse(origin)
	if err != nil {
		_ = context.Close()
		return FetchSurface{}, err
	}
	if document != "" {
		if at, err := url.Parse(document); err == nil {
			target = target.ResolveReference(at)
		}
	}
	if _, err := page.Goto(target.String(), playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		_ = context.Close()
		return FetchSurface{}, fmt.Errorf("browser: %s would not open: %w", target, err)
	}
	return FetchSurface{
		Page:      Wrap(page),
		Navigator: &contextNavigator{context: context},
		Close:     func() error { return context.Close() },
	}, nil
}
