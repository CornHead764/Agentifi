package api

import (
	"fmt"
	"net/http"
)

// writeFailureScreenshot answers with the JPEG of the page a connector's last
// run failed on. It is the household's own page, so nothing may cache it
// beyond this caller and nothing on it may run.
func writeFailureScreenshot(w http.ResponseWriter, shot []byte) error {
	header := w.Header()
	header.Set("Content-Type", "image/jpeg")
	header.Set("Content-Length", fmt.Sprint(len(shot)))
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	header.Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(shot)
	return err
}
