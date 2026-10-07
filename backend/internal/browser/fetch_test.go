package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The answer shape the in-page fetch hands back, as the driver would: plain
// maps, which is what decodeJSON has to cope with.
func pageAnswer(status int, contentType, body string) map[string]any {
	return map[string]any{
		"status":  float64(status),
		"headers": map[string]any{"content-type": contentType},
		"base64":  base64.StdEncoding.EncodeToString([]byte(body)),
	}
}

func decodedAnswer(t *testing.T, answered any) PageCallAnswer {
	t.Helper()
	var answer PageCallAnswer
	require.NoError(t, decodeJSON(answered, &answer))
	return answer
}

func TestAPageRequestDropsTheHeadersAPageMayNotSet(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://service.example.test/api/1/bill/Current",
		strings.NewReader(`{"accountNumbers":["1"]}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer invented")
	req.Header.Set("uid", "2")
	req.Header.Set("Origin", "https://portal.example.test")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Cookie", "session=invented")

	call, err := pageRequest(req)
	require.NoError(t, err)
	shaped, err := call.Argument(t.Context())
	require.NoError(t, err)
	require.NoError(t, Serializable(shaped),
		"a page call's argument must stay serializable")
	require.Equal(t, "bytes", shaped["read"])
	headers := shaped["headers"].(map[string]any)
	require.Equal(t, "application/json", headers["Content-Type"])
	require.Equal(t, "Bearer invented", headers["Authorization"])
	require.Equal(t, "2", headers["Uid"])
	require.NotContains(t, headers, "Origin")
	require.NotContains(t, headers, "User-Agent")
	require.NotContains(t, headers, "Cookie")
	require.Equal(t, http.MethodPost, shaped["method"])
	require.Equal(t, `{"accountNumbers":["1"]}`, shaped["body"])
}

func TestAGetCarriesNoBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://service.example.test/whoami", nil)
	require.NoError(t, err)
	call, err := pageRequest(req)
	require.NoError(t, err)
	shaped, err := call.Argument(t.Context())
	require.NoError(t, err)
	require.NotContains(t, shaped, "body")
}

func TestWhatThePageHandsBackReadsLikeAResponse(t *testing.T) {
	answer, err := readPageAnswer(decodedAnswer(t, pageAnswer(200, "application/json", `{"ok":true}`)))
	require.NoError(t, err)
	require.Equal(t, 200, answer.Status)
	require.Equal(t, "application/json", answer.Headers["content-type"])

	req, _ := http.NewRequest(http.MethodGet, "https://service.example.test/", nil)
	response := answeredFrom(req, answer)
	require.Equal(t, 200, response.StatusCode)
	require.Same(t, req, response.Request)
	require.Equal(t, "application/json", response.Header.Get("Content-Type"))
	read, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, `{"ok":true}`, string(read))
}

func TestAFollowedRedirectShowsWhereTheFetchEnded(t *testing.T) {
	answered := pageAnswer(200, "text/html", "<html></html>")
	answered["url"] = "https://service.example.test/homes/details/7_id/"
	answer, err := readPageAnswer(decodedAnswer(t, answered))
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodGet, "https://service.example.test/homes/search/", nil)
	response := answeredFrom(req, answer)
	require.Equal(t, "https://service.example.test/homes/details/7_id/", response.Request.URL.String())
	require.Equal(t, "https://service.example.test/homes/search/", req.URL.String(),
		"the caller's request is not rewritten")
}

func TestThePageOpensOnceAndEveryCallIsAFetchInsideIt(t *testing.T) {
	opened := 0
	var asked []string
	page := &StubPage{
		OnEvaluate: func(script string, arg any) (any, error) {
			require.Contains(t, script, "await fetch(arg.url")
			shaped := arg.(map[string]any)
			asked = append(asked, fmt.Sprintf("%s %s", shaped["method"], shaped["url"]))
			return pageAnswer(200, "application/json", `{"ok":true}`), nil
		},
	}
	closed := 0
	fetcher := &PageFetcher{
		Origin:   "https://portal.example.test",
		Document: "/public/light.js",
		Open: func() (FetchSurface, error) {
			opened++
			return FetchSurface{Page: page, Close: func() error { closed++; return nil }}, nil
		},
	}

	for range 3 {
		req, _ := http.NewRequest(http.MethodGet, "https://service.example.test/thing", nil)
		response, err := fetcher.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode)
	}
	require.Equal(t, 1, opened, "the page is opened once and every call goes through it")
	require.Len(t, asked, 3)
	require.Equal(t, "GET https://service.example.test/thing", asked[0])

	require.NoError(t, fetcher.Close())
	require.Equal(t, 1, closed)
}

func TestACrossOriginGetThePageCannotReadIsTakenOffANavigation(t *testing.T) {
	page := &StubPage{
		OnEvaluate: func(string, any) (any, error) {
			return nil, errors.New("TypeError: Failed to fetch")
		},
	}
	navigated := ""
	fetcher := &PageFetcher{
		Origin: "https://portal.example.test",
		Open: func() (FetchSurface, error) {
			return FetchSurface{Page: page, Navigator: navigatorFunc(func(address string) (int, string, []byte, error) {
				navigated = address
				return 200, "application/pdf", []byte("%PDF-1.4 invented"), nil
			})}, nil
		},
	}

	req, _ := http.NewRequest(http.MethodGet, "https://statements.example.test/signed/abc", nil)
	response, err := fetcher.Do(req)
	require.NoError(t, err)
	require.Equal(t, "https://statements.example.test/signed/abc", navigated)
	require.Equal(t, "application/pdf", response.Header.Get("Content-Type"))
	read, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(read), "%PDF-"))
}

func TestARefusedCallOnTheProvidersOwnOriginIsNotNavigatedTo(t *testing.T) {
	page := &StubPage{
		OnEvaluate: func(string, any) (any, error) { return nil, errors.New("Failed to fetch") },
	}
	fetcher := &PageFetcher{
		Origin: "https://portal.example.test",
		Open: func() (FetchSurface, error) {
			return FetchSurface{Page: page, Navigator: navigatorFunc(func(string) (int, string, []byte, error) {
				t.Fatal("a call on the page's own origin must not be navigated to")
				return 0, "", nil, nil
			})}, nil
		},
	}
	// Same origin as the page: a refusal here is the provider's, not the
	// browser's rule about reading a cross-origin answer.
	req, _ := http.NewRequest(http.MethodGet, "https://portal.example.test/api/thing", nil)
	_, err := fetcher.Do(req)
	require.ErrorContains(t, err, "could not fetch")

	// And a POST is never navigated to, wherever it was going.
	post, _ := http.NewRequest(http.MethodPost, "https://statements.example.test/x", strings.NewReader("{}"))
	_, err = fetcher.Do(post)
	require.ErrorContains(t, err, "could not fetch")
}

func TestAnEvaluationThatFailedForAnotherReasonIsNotNavigatedTo(t *testing.T) {
	page := &StubPage{
		OnEvaluate: func(string, any) (any, error) {
			return nil, errors.New("Execution context was destroyed, most likely because of a navigation")
		},
	}
	fetcher := &PageFetcher{
		Origin: "https://portal.example.test",
		Open: func() (FetchSurface, error) {
			return FetchSurface{Page: page, Navigator: navigatorFunc(func(string) (int, string, []byte, error) {
				t.Fatal("only the fetch's own network refusal is worth a navigation")
				return 0, "", nil, nil
			})}, nil
		},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://statements.example.test/signed/abc", nil)
	_, err := fetcher.Do(req)
	require.ErrorContains(t, err, "could not fetch")
}

func TestTheInPageFetchIsHandedTheRequestsDeadline(t *testing.T) {
	var handed any
	page := &StubPage{
		OnEvaluate: func(script string, arg any) (any, error) {
			require.Contains(t, script, "controller.abort()")
			handed = arg.(map[string]any)["timeoutMs"]
			return pageAnswer(200, "application/json", `{}`), nil
		},
	}
	fetcher := &PageFetcher{
		Origin: "https://portal.example.test",
		Open:   func() (FetchSurface, error) { return FetchSurface{Page: page}, nil },
	}

	req, _ := http.NewRequest(http.MethodGet, "https://portal.example.test/api", nil)
	_, err := fetcher.Do(req)
	require.NoError(t, err)
	require.EqualValues(t, FetchTimeout.Milliseconds(), handed, "no deadline is the default ceiling")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = fetcher.Do(req.WithContext(ctx))
	require.NoError(t, err)
	require.LessOrEqual(t, handed.(float64), float64(5000))
	require.Greater(t, handed.(float64), float64(0))
}

func TestACancelledRequestDoesNotWaitOnThePage(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	page := &StubPage{
		OnEvaluate: func(string, any) (any, error) {
			<-release
			return pageAnswer(200, "application/json", `{}`), nil
		},
	}
	fetcher := &PageFetcher{
		Origin: "https://portal.example.test",
		Open:   func() (FetchSurface, error) { return FetchSurface{Page: page}, nil },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://portal.example.test/api", nil)
	_, err := fetcher.Do(req)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// Headless Chrome answers a navigation to an inline PDF with its viewer's own
// page as the body, under the server's application/pdf; that and only that is
// read again from inside the page.
func TestOnlyAPDFAnswerWhoseBodyIsNotThePDFIsTheViewer(t *testing.T) {
	viewer := []byte("<!doctype html>\n<html><body><iframe type=\"application/pdf\"></iframe></body></html>")
	require.True(t, viewedPDF("application/pdf", viewer))
	require.True(t, viewedPDF("Application/PDF; charset=binary", viewer))
	require.False(t, viewedPDF("application/pdf", []byte("%PDF-1.4\n")))
	require.False(t, viewedPDF("text/html", viewer))
	require.False(t, viewedPDF("application/octet-stream", []byte("%PDF-1.4\n")))
}

// navigatorFunc is a Navigator that is one function.
type navigatorFunc func(address string) (int, string, []byte, error)

func (f navigatorFunc) Bytes(address string) (int, string, []byte, error) { return f(address) }

func TestAFetchThatThrewInThePageIsARefusalToo(t *testing.T) {
	page := &StubPage{
		OnEvaluate: func(string, any) (any, error) {
			return map[string]any{"status": float64(0), "error": "Failed to fetch"}, nil
		},
	}
	navigated := ""
	fetcher := &PageFetcher{
		Origin: "https://portal.example.test",
		Open: func() (FetchSurface, error) {
			return FetchSurface{Page: page, Close: func() error { return nil },
				Navigator: navigatorFunc(func(address string) (int, string, []byte, error) {
					navigated = address
					return 200, "application/pdf", []byte("%PDF-"), nil
				})}, nil
		},
	}
	defer func() { _ = fetcher.Close() }()
	req, _ := http.NewRequest(http.MethodGet, "https://statements.example.test/signed/def", nil)
	response, err := fetcher.Do(req)
	require.NoError(t, err)
	require.Equal(t, "https://statements.example.test/signed/def", navigated)
	read, _ := io.ReadAll(response.Body)
	require.Equal(t, "%PDF-", string(read))
}
