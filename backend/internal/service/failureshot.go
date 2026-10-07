package service

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	_ "image/png"

	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// failureShotQualities are tried in order until the page fits the store's
// bound.
var failureShotQualities = []int{75, 50, 30}

// failureShot is the page a connector failed on, as the JPEG kept beside the
// error: from the error itself, or else from the base64 image a stopped run
// came back with. It is nil when there is no page, when the bytes are not an
// image, or when no quality fits store.MaxFailureScreenshotBytes.
func failureShot(err error, image64 string) []byte {
	raw := provider.ScreenshotOf(err)
	if raw == nil && image64 != "" {
		decoded, decodeErr := base64.StdEncoding.DecodeString(image64)
		if decodeErr != nil {
			return nil
		}
		raw = decoded
	}
	if len(raw) == 0 {
		return nil
	}
	picture, _, decodeErr := image.Decode(bytes.NewReader(raw))
	if decodeErr != nil {
		return nil
	}
	for _, quality := range failureShotQualities {
		var out bytes.Buffer
		if jpeg.Encode(&out, picture, &jpeg.Options{Quality: quality}) != nil {
			return nil
		}
		if out.Len() <= store.MaxFailureScreenshotBytes {
			return out.Bytes()
		}
	}
	return nil
}
