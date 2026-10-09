package browser

import (
	"encoding/base64"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/textutil"
	"github.com/playwright-community/playwright-go"
)

// The live browser: the CDP screencast's newest JPEG out, kept with a sequence
// number so an unchanged page costs the poll nothing, and a person's clicks and
// keys played back in at the same coordinates, because the picture is the
// viewport. Caster and LiveSurface are seams so this is testable without
// Chromium.

const (
	liveMinWidth  = 360
	liveMaxWidth  = 1280
	liveMinHeight = 480
	liveMaxHeight = 1000
	// liveQuality is the JPEG quality of both the screencast and the
	// screenshot that stands in for it.
	liveQuality = 70
	// firefoxFrameGap is the least time between two screenshots of a Camoufox
	// page: with no screencast to lean on, each poll takes one, and a poll
	// that comes sooner is answered with the last.
	firefoxFrameGap = time.Second
)

func ClampViewport(asked Size) Size {
	width, height := asked.Width, asked.Height
	if width == 0 {
		width = 1024
	}
	if height == 0 {
		height = 768
	}
	return Size{
		Width:  int(math.Min(liveMaxWidth, math.Max(liveMinWidth, float64(width)))),
		Height: int(math.Min(liveMaxHeight, math.Max(liveMinHeight, float64(height)))),
	}
}

// LiveFrame is one picture of the live browser. Image is base64 JPEG, and is
// empty on a frame the caller has already seen.
type LiveFrame struct {
	Image  string
	Width  int
	Height int
	Seq    int
}

// Caster is the CDP session a screencast runs over; playwright.CDPSession
// satisfies it.
type Caster interface {
	Send(method string, params map[string]any) (any, error)
	On(name string, handler any)
	Detach() error
}

// LiveSurface is what a person drives. Separate from Page because a provider
// module works through selectors, and a coordinate is something only a person
// has.
type LiveSurface interface {
	URL() string
	MouseMove(x, y float64) error
	MouseDown(button string) error
	MouseUp(button string) error
	MouseClick(x, y float64, button string, clicks int) error
	MouseWheel(dx, dy float64) error
	KeyDown(key string) error
	KeyUp(key string) error
	KeyPress(key string) error
	KeyType(text string) error
	// JPEG stands in when the screencast is not painting.
	JPEG() ([]byte, error)
}

// LiveInput is one thing the person did, in the picture's own pixels. The
// frontend sends click, wheel, key and text; move, down, up, keydown and keyup
// are accepted too because a drag and a held modifier are made of them.
type LiveInput struct {
	Type   string  `json:"type"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	DX     float64 `json:"dx"`
	DY     float64 `json:"dy"`
	Button string  `json:"button"`
	Clicks int     `json:"clicks"`
	Key    string  `json:"key"`
	Text   string  `json:"text"`
	// Modifiers are held for the length of one key event: Shift, Control,
	// Alt or Meta.
	Modifiers []string `json:"modifiers"`
}

// namedKeys are the keys a browser names that Playwright knows by the same
// name.
var namedKeys = map[string]bool{
	"Enter": true, "Backspace": true, "Tab": true, "Delete": true, "Escape": true,
	"Home": true, "End": true, "PageUp": true, "PageDown": true,
	"ArrowLeft": true, "ArrowRight": true, "ArrowUp": true, "ArrowDown": true, "Space": true,
}

var heldModifiers = map[string]string{
	"shift": "Shift", "control": "Control", "ctrl": "Control",
	"alt": "Alt", "meta": "Meta", "command": "Meta",
}

const typedTextCap = 4096

type LiveView struct {
	surface  LiveSurface
	viewport Size

	mu    sync.Mutex
	cast  Caster
	seq   int
	frame LiveFrame
	held  bool
	// since is how many frames the screencast has painted since the last
	// navigation. Zero means it is not painting and a screenshot stands in.
	since  int
	closed bool
	// gap is the least time between two screenshots, and shot when the last
	// was taken; a zero gap takes one on every poll.
	gap  time.Duration
	shot time.Time
}

// NewLiveView is a view before any screencast is attached; until then it
// answers screenshots.
func NewLiveView(surface LiveSurface, viewport Size) *LiveView {
	return &LiveView{surface: surface, viewport: ClampViewport(viewport)}
}

func (v *LiveView) Viewport() Size { return v.viewport }

// Attach starts the screencast over a CDP session.
func (v *LiveView) Attach(cast Caster) error {
	v.mu.Lock()
	v.cast = cast
	v.mu.Unlock()
	cast.On("Page.screencastFrame", func(params any) {
		// A panic in a driver callback has no caller to unwind to: it would
		// take the API process down over one malformed frame.
		defer func() { _ = recover() }()
		v.accept(params)
	})
	return v.start()
}

func (v *LiveView) start() error {
	v.mu.Lock()
	cast, viewport := v.cast, v.viewport
	v.mu.Unlock()
	if cast == nil {
		return nil
	}
	_, err := cast.Send("Page.startScreencast", map[string]any{
		"format":        "jpeg",
		"quality":       liveQuality,
		"maxWidth":      viewport.Width,
		"maxHeight":     viewport.Height,
		"everyNthFrame": 1,
	})
	return err
}

// Navigated restarts the screencast, which does not always survive a
// cross-document navigation. Until a frame arrives under the new document,
// Frame takes screenshots instead.
func (v *LiveView) Navigated() {
	v.mu.Lock()
	v.since = 0
	v.mu.Unlock()
	_ = v.start()
}

// accept keeps and acknowledges one screencastFrame: unacknowledged, Chromium
// stops painting after a handful. The frame's deviceWidth/Height is not always
// the viewport (a zoomed page paints smaller), and the dialog scales by it.
func (v *LiveView) accept(params any) {
	frame, ok := params.(map[string]any)
	if !ok {
		return
	}
	data, _ := frame["data"].(string)
	if data == "" {
		return
	}
	width, height := v.viewport.Width, v.viewport.Height
	if metadata, ok := frame["metadata"].(map[string]any); ok {
		if painted, ok := metadata["deviceWidth"].(float64); ok && painted > 0 {
			width = int(painted)
		}
		if painted, ok := metadata["deviceHeight"].(float64); ok && painted > 0 {
			height = int(painted)
		}
	}
	v.mu.Lock()
	v.seq++
	v.frame = LiveFrame{Image: data, Width: width, Height: height, Seq: v.seq}
	v.held = true
	v.since++
	cast := v.cast
	v.mu.Unlock()
	if cast != nil {
		if sessionID, ok := frame["sessionId"]; ok {
			_, _ = cast.Send("Page.screencastFrameAck", map[string]any{"sessionId": sessionID})
		}
	}
}

// Frame's image is empty when nothing has changed since the caller's sequence
// number.
func (v *LiveView) Frame(after int) LiveFrame {
	v.mu.Lock()
	painting := v.since > 0
	v.mu.Unlock()
	if !painting {
		v.snapshot()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	out := LiveFrame{Width: v.viewport.Width, Height: v.viewport.Height, Seq: v.seq}
	if v.held {
		out.Width, out.Height = v.frame.Width, v.frame.Height
		if v.seq > after {
			out.Image = v.frame.Image
		}
	}
	return out
}

// snapshot stands in for a screencast that is not painting. A page
// mid-navigation has none, which is the next poll's problem.
func (v *LiveView) snapshot() {
	v.mu.Lock()
	recent := v.gap > 0 && v.held && time.Since(v.shot) < v.gap
	v.mu.Unlock()
	if recent {
		return
	}
	shot, err := v.surface.JPEG()
	if err != nil || len(shot) == 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.seq++
	v.shot = time.Now()
	v.frame = LiveFrame{
		Image:  base64.StdEncoding.EncodeToString(shot),
		Width:  v.viewport.Width,
		Height: v.viewport.Height,
		Seq:    v.seq,
	}
	v.held = true
}

// Play replays what the person did, in order. One event's failure never stops
// the next, as for a person. Nothing is logged: a text event is as often a
// password as not.
func (v *LiveView) Play(events []LiveInput) {
	for _, event := range events {
		_ = v.play(event)
	}
}

func (v *LiveView) play(event LiveInput) error {
	switch strings.ToLower(event.Type) {
	case "move":
		return v.surface.MouseMove(event.X, event.Y)
	case "down":
		if err := v.surface.MouseMove(event.X, event.Y); err != nil {
			return err
		}
		return v.surface.MouseDown(mouseButton(event.Button))
	case "up":
		if err := v.surface.MouseMove(event.X, event.Y); err != nil {
			return err
		}
		return v.surface.MouseUp(mouseButton(event.Button))
	case "click":
		return v.surface.MouseClick(event.X, event.Y, mouseButton(event.Button), clickCount(event.Clicks))
	case "wheel":
		if err := v.surface.MouseMove(event.X, event.Y); err != nil {
			return err
		}
		return v.surface.MouseWheel(event.DX, event.DY)
	case "key", "keypress":
		return v.withModifiers(event, func(key string) error { return v.surface.KeyPress(key) })
	case "keydown":
		return v.withModifiers(event, func(key string) error { return v.surface.KeyDown(key) })
	case "keyup":
		return v.withModifiers(event, func(key string) error { return v.surface.KeyUp(key) })
	case "text", "type":
		text := textutil.Clip(event.Text, typedTextCap)
		if text == "" {
			return nil
		}
		return v.surface.KeyType(text)
	}
	return nil
}

func (v *LiveView) withModifiers(event LiveInput, act func(key string) error) error {
	if !playableKey(event.Key) {
		return nil
	}
	var held []string
	for _, asked := range event.Modifiers {
		name, known := heldModifiers[strings.ToLower(asked)]
		if !known {
			continue
		}
		if err := v.surface.KeyDown(name); err != nil {
			continue
		}
		held = append(held, name)
	}
	defer func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = v.surface.KeyUp(held[i])
		}
	}()
	return act(event.Key)
}

// playableKey is a key Playwright names, or one character; pressing any other
// browser key name is an error rather than a keypress.
func playableKey(key string) bool {
	return namedKeys[key] || len([]rune(key)) == 1
}

func mouseButton(asked string) string {
	if strings.EqualFold(asked, "right") {
		return "right"
	}
	if strings.EqualFold(asked, "middle") {
		return "middle"
	}
	return "left"
}

func clickCount(asked int) int {
	if asked < 1 {
		return 1
	}
	if asked > 3 {
		return 3
	}
	return asked
}

func (v *LiveView) URL() string { return v.surface.URL() }

// Close stops the screencast and lets the CDP session go. The page is not
// closed: it belongs to whoever opened it.
func (v *LiveView) Close() {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.closed = true
	cast := v.cast
	v.cast = nil
	v.mu.Unlock()
	if cast == nil {
		return
	}
	_, _ = cast.Send("Page.stopScreencast", map[string]any{})
	_ = cast.Detach()
}

// StartLiveView is the live browser over a real page. A page the screencast
// will not start on is still a live view that answers screenshots: a person
// signing in would rather have a slow picture than a refusal.
func StartLiveView(page Page, viewport Size) (*LiveView, error) {
	live, ok := page.(*livePage)
	if !ok {
		return nil, fmt.Errorf("browser: that page is not a live one")
	}
	view := NewLiveView(live, viewport)
	cast, err := live.page.Context().NewCDPSession(live.page)
	if err != nil {
		return view, nil
	}
	if err := view.Attach(cast); err != nil {
		return view, nil
	}
	live.page.OnFrameNavigated(func(frame playwright.Frame) {
		if frame != live.page.MainFrame() {
			return
		}
		view.Navigated()
	})
	return view, nil
}

// StartFirefoxLiveView is the live browser over a Camoufox page. Camoufox has
// no DevTools protocol, so there is no screencast: each poll that comes at
// least firefoxFrameGap after the last takes a screenshot, with every typed
// field covered (the engine fills those; the person is there to tick a box).
// A page nobody polls is never photographed.
func StartFirefoxLiveView(page Page) (*LiveView, error) {
	live, ok := page.(*livePage)
	if !ok || !live.firefox {
		return nil, fmt.Errorf("browser: that page is not a Camoufox one")
	}
	size := DefaultViewport
	if shown := live.page.ViewportSize(); shown != nil && shown.Width > 0 && shown.Height > 0 {
		size = Size{Width: shown.Width, Height: shown.Height}
	}
	view := NewLiveView(live, size)
	view.gap = firefoxFrameGap
	return view, nil
}

// --- the surface over a real page ----------------------------------------------

func (p *livePage) MouseMove(x, y float64) error { return p.page.Mouse().Move(x, y) }

func (p *livePage) MouseDown(button string) error {
	return p.page.Mouse().Down(playwright.MouseDownOptions{Button: mouseButtonOption(button)})
}

func (p *livePage) MouseUp(button string) error {
	return p.page.Mouse().Up(playwright.MouseUpOptions{Button: mouseButtonOption(button)})
}

func (p *livePage) MouseClick(x, y float64, button string, clicks int) error {
	return p.page.Mouse().Click(x, y, playwright.MouseClickOptions{
		Button:     mouseButtonOption(button),
		ClickCount: playwright.Int(clicks),
	})
}

func (p *livePage) MouseWheel(dx, dy float64) error { return p.page.Mouse().Wheel(dx, dy) }

func (p *livePage) KeyDown(key string) error { return p.page.Keyboard().Down(key) }

func (p *livePage) KeyUp(key string) error { return p.page.Keyboard().Up(key) }

func (p *livePage) KeyPress(key string) error { return p.page.Keyboard().Press(key) }

func (p *livePage) KeyType(text string) error { return p.page.Keyboard().Type(text) }

func (p *livePage) JPEG() ([]byte, error) {
	options := playwright.PageScreenshotOptions{
		Type:     playwright.ScreenshotTypeJpeg,
		Quality:  playwright.Int(liveQuality),
		FullPage: playwright.Bool(false),
	}
	if p.firefox {
		options.Mask = typedFieldMask(p.page)
		options.Scale = playwright.ScreenshotScaleCss
		options.Timeout = playwright.Float(screenshotTimeoutMS)
	}
	return p.page.Screenshot(options)
}

func mouseButtonOption(button string) *playwright.MouseButton {
	switch button {
	case "right":
		return playwright.MouseButtonRight
	case "middle":
		return playwright.MouseButtonMiddle
	}
	return playwright.MouseButtonLeft
}
