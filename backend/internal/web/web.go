// Package web serves the compiled React application from inside the binary.
//
// `dist/` is populated by `npm run build` and not committed, except for
// `.gitkeep`, which gives //go:embed something to match so the backend builds
// in a checkout where the frontend never has.
package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// Go's MIME table has no entry for .webmanifest, and sniffing reads JSON as
// text/plain, which a browser may ignore as a manifest.
func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

//go:embed all:dist
var dist embed.FS

// Handler serves the single-page application. Any path that is not a real file
// falls through to index.html, since routing happens in the browser.
func Handler() http.Handler {
	assets, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("web: dist directory missing from the binary: " + err.Error())
	}
	return handlerOver(assets)
}

// handlerOver is Handler over whichever files it is given, so tests can state
// the files rather than depend on the last `npm run build`.
func handlerOver(assets fs.FS) http.Handler {
	files := http.FS(assets)
	server := http.FileServer(files)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))

		// Anything that is not a real file is a client-side route. index.html
		// is never cached: an old copy requests asset names that no longer
		// exist, and the app fails silently.
		if name == "." || name == "index.html" || !exists(assets, name) {
			// Served directly: the file server redirects /index.html to /,
			// which would loop a deep link.
			w.Header().Set("Cache-Control", "no-cache")
			index, err := fs.ReadFile(assets, "index.html")
			if err != nil {
				http.Error(w, "frontend not built", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(index)
			return
		}

		// Vite fingerprints these, so they can be cached forever.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		server.ServeHTTP(w, r)
	})
}

func exists(assets fs.FS, name string) bool {
	f, err := assets.Open(name)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
