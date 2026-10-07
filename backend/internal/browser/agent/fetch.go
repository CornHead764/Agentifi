package agent

import (
	"net/http"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// Fetcher is the HTTP caller a connector's calls go through, and its release.
// origin "" is a plain client. Any other origin is a page there, in Camoufox
// when firefox is set, because that site only answers a browser.
type Fetcher func(firefox bool, origin, document string) (browser.Fetcher, func(), error)

// Fetchers makes the callers of Fetcher in engine's browsers.
func Fetchers(engine *browser.Engine) Fetcher {
	return func(firefox bool, origin, document string) (browser.Fetcher, func(), error) {
		if origin == "" {
			return PlainClient(), func() {}, nil
		}
		open := func() (browser.FetchSurface, error) {
			return engine.OpenFetchSurface(origin, document)
		}
		if firefox {
			if engine == nil || !engine.HasFirefox() {
				return nil, nil, browser.ErrNoFirefox
			}
			open = func() (browser.FetchSurface, error) {
				return engine.OpenFirefoxFetchSurface(origin, document)
			}
		}
		fetcher := &browser.PageFetcher{Origin: origin, Document: document, Open: open}
		return fetcher, func() { _ = fetcher.Close() }, nil
	}
}

// PlainClient's timeout is a whole call, not a connection: a portal is slow,
// a statement is a few megabytes, and a pull is unattended anyway.
func PlainClient() browser.Fetcher {
	return &http.Client{Timeout: 2 * time.Minute}
}
