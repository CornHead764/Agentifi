package browser

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

// The live view with no Chromium behind it: a fake CDP session painting
// frames, and a fake surface recording what was played into it.
//
// What these hold is the two halves the dialog depends on and neither of which
// a real-browser test can assert cheaply: that a frame is handed out once and
// then withheld until the picture moves, and that a click the frontend sent
// arrives at the mouse as the same click.

func TestTheViewportIsClampedToWhatTheDialogCanDraw(t *testing.T) {
	require.Equal(t, Size{Width: 1024, Height: 768}, ClampViewport(Size{}))
	require.Equal(t, Size{Width: 1280, Height: 1000}, ClampViewport(Size{Width: 4000, Height: 3000}))
	require.Equal(t, Size{Width: 360, Height: 480}, ClampViewport(Size{Width: 100, Height: 40}))
	require.Equal(t, Size{Width: 900, Height: 640}, ClampViewport(Size{Width: 900, Height: 640}))
}

func TestAPaintedFrameIsHandedOutOnceAndAcknowledged(t *testing.T) {
	surface := NewStubSurface("https://example.test/login")
	view := NewLiveView(surface, Size{Width: 900, Height: 640})
	cast := &StubCaster{}
	require.NoError(t, view.Attach(cast))
	require.Equal(t, []string{"Page.startScreencast"}, cast.Sent)

	cast.Paint(7, "first-picture", 900, 640)

	frame := view.Frame(0)
	require.Equal(t, 1, frame.Seq)
	require.Equal(t, "first-picture", frame.Image)
	require.Equal(t, 900, frame.Width)
	require.Equal(t, 640, frame.Height)
	// The ack is what asks Chromium for the next picture: without it the
	// screencast stops after a handful and the live view freezes.
	require.Equal(t, []any{float64(7)}, cast.Acked)

	// Asked again from the sequence just seen, the picture is withheld: the
	// dialog polls twice a second and a page that has not moved costs nothing.
	unchanged := view.Frame(frame.Seq)
	require.Equal(t, 1, unchanged.Seq)
	require.Empty(t, unchanged.Image)
	require.Equal(t, 0, surface.Shots, "a painting screencast is never screenshotted")

	cast.Paint(7, "second-picture", 900, 640)
	next := view.Frame(frame.Seq)
	require.Equal(t, 2, next.Seq)
	require.Equal(t, "second-picture", next.Image)
}

func TestAFrameSaysThePictureItWasPaintedAtNotTheOneAsked(t *testing.T) {
	view := NewLiveView(NewStubSurface("https://example.test/"), Size{Width: 1024, Height: 768})
	cast := &StubCaster{}
	require.NoError(t, view.Attach(cast))
	// A page that has zoomed itself paints smaller, and the dialog scales by
	// what the frame says rather than by what was asked for.
	cast.Paint(1, "small", 512, 384)

	frame := view.Frame(0)
	require.Equal(t, 512, frame.Width)
	require.Equal(t, 384, frame.Height)
}

func TestAScreencastThatIsNotPaintingIsScreenshottedInstead(t *testing.T) {
	surface := NewStubSurface("https://example.test/")
	surface.Shot = []byte("jpeg-bytes")
	view := NewLiveView(surface, Size{Width: 800, Height: 600})
	cast := &StubCaster{}
	require.NoError(t, view.Attach(cast))

	// Nothing has been painted, so the first poll pays for a screenshot.
	frame := view.Frame(0)
	require.Equal(t, 1, surface.Shots)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("jpeg-bytes")), frame.Image)
	require.Equal(t, 1, frame.Seq)

	// Once it is painting the screenshot stops.
	cast.Paint(1, "painted", 800, 600)
	require.Equal(t, "painted", view.Frame(frame.Seq).Image)
	require.Equal(t, 1, surface.Shots)

	// A navigation may swallow the screencast: it is restarted, and until a
	// frame arrives under the new document the screenshot stands in again.
	view.Navigated()
	require.Equal(t, []string{
		"Page.startScreencast", "Page.screencastFrameAck", "Page.startScreencast",
	}, cast.Sent)
	view.Frame(0)
	require.Equal(t, 2, surface.Shots)
}

func TestAPageThatCannotBeScreenshottedIsTheNextPollsProblem(t *testing.T) {
	surface := NewStubSurface("https://example.test/")
	// Mid-navigation: Chromium has no picture to give.
	surface.OnShot = func() ([]byte, error) { return nil, nil }
	view := NewLiveView(surface, Size{Width: 800, Height: 600})

	frame := view.Frame(0)
	require.Equal(t, 0, frame.Seq)
	require.Empty(t, frame.Image)
	require.Equal(t, 800, frame.Width, "the dialog still knows how big the browser is")
}

func TestTheFrontendsEventsArriveAtTheMouseAndKeyboardAsThemselves(t *testing.T) {
	surface := NewStubSurface("https://example.test/")
	view := NewLiveView(surface, Size{Width: 800, Height: 600})

	view.Play([]LiveInput{
		{Type: "move", X: 10, Y: 20},
		{Type: "click", X: 40, Y: 60},
		{Type: "click", X: 40, Y: 60, Button: "right", Clicks: 2},
		{Type: "wheel", X: 40, Y: 60, DY: 240},
		{Type: "key", Key: "Enter"},
		{Type: "key", Key: "a"},
		{Type: "text", Text: "someone@example.test"},
	})

	require.Equal(t, []string{
		"move 10,20",
		"click 40,60 left x1",
		"click 40,60 right x2",
		"move 40,60", "wheel 0,240",
		"key Enter",
		"key a",
		"type someone@example.test",
	}, surface.Acted)
}

func TestAKeyWithNoKeystrokeBehindItIsNotPressed(t *testing.T) {
	surface := NewStubSurface("https://example.test/")
	view := NewLiveView(surface, Size{Width: 800, Height: 600})

	view.Play([]LiveInput{
		// A browser's own names for keys Playwright does not know. Pressing
		// one is an error where a person expected a keystroke.
		{Type: "key", Key: "Unidentified"},
		{Type: "key", Key: "AudioVolumeUp"},
		{Type: "text", Text: ""},
		{Type: "nonsense"},
		// And a key held with a modifier: down, press, up, in that order.
		{Type: "key", Key: "a", Modifiers: []string{"Control", "shift", "hyper"}},
	})

	require.Equal(t, []string{
		"keydown Control", "keydown Shift", "key a", "keyup Shift", "keyup Control",
	}, surface.Acted)
}

func TestClosingTheViewStopsThePaintingAndLetsTheSessionGo(t *testing.T) {
	view := NewLiveView(NewStubSurface("https://example.test/"), Size{Width: 800, Height: 600})
	cast := &StubCaster{}
	require.NoError(t, view.Attach(cast))

	view.Close()
	view.Close()
	require.Equal(t, []string{"Page.startScreencast", "Page.stopScreencast", "detach"}, cast.Sent)
}
