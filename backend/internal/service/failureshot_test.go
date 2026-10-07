package service

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func pngOf(t *testing.T, picture image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, png.Encode(&out, picture))
	return out.Bytes()
}

func plainPage(width, height int) image.Image {
	page := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			page.Set(x, y, color.RGBA{R: 240, G: 240, B: 250, A: 255})
		}
	}
	return page
}

func TestAFailureKeepsItsPageAsAJPEGOfTheSameSize(t *testing.T) {
	page := pngOf(t, plainPage(320, 200))
	for name, shot := range map[string][]byte{
		"from the error":           failureShot(&provider.PageFailure{Err: errors.New("covered"), Screenshot: page}, ""),
		"from the stopped run":     failureShot(nil, base64.StdEncoding.EncodeToString(page)),
		"the error before the run": failureShot(&provider.PageFailure{Err: errors.New("covered"), Screenshot: page}, "not base64"),
	} {
		t.Run(name, func(t *testing.T) {
			require.NotNil(t, shot)
			config, err := jpeg.DecodeConfig(bytes.NewReader(shot))
			require.NoError(t, err, "kept as a JPEG")
			require.Equal(t, 320, config.Width)
			require.Equal(t, 200, config.Height)
		})
	}
}

func TestAFailureWithNoPictureOfAPageKeepsNone(t *testing.T) {
	require.Nil(t, failureShot(nil, ""))
	require.Nil(t, failureShot(errors.New("timeout"), ""))
	require.Nil(t, failureShot(nil, "not base64"))
	require.Nil(t, failureShot(&provider.PageFailure{Err: errors.New("x"), Screenshot: []byte("not an image")}, ""))
}

func TestAPageTooBigToKeepIsNotKept(t *testing.T) {
	// Noise is what JPEG cannot shrink: at 2400 by 2400 it stays over the bound
	// at every quality tried.
	noise := image.NewGray(image.Rect(0, 0, 2400, 2400))
	random := rand.New(rand.NewPCG(1, 2))
	for i := range noise.Pix {
		noise.Pix[i] = uint8(random.IntN(256))
	}
	page := pngOf(t, noise)
	require.Greater(t, len(page), store.MaxFailureScreenshotBytes)
	require.Nil(t, failureShot(&provider.PageFailure{Err: errors.New("covered"), Screenshot: page}, ""))
}

func TestAnUpdateThatStopsOnAPageSaysItKeptThePage(t *testing.T) {
	fixture := signedInFixture(t)
	fixture.agent.fail = &provider.PageFailure{
		Err: errors.New("something covered the button"), Screenshot: pngOf(t, plainPage(64, 48)),
	}

	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullFailed, result.Status)
	require.True(t, result.Screenshot)
	kept, err := fixture.bills.store.BillPullScreenshot(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.NotEmpty(t, kept)

	fixture.agent.fail = errors.New("dial tcp: i/o timeout")
	result, err = fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.False(t, result.Screenshot)
}
