package browser

import (
	"fmt"
	"sync"
)

// StubSurface is a LiveSurface that is not a browser. It records what was
// played into it and answers a picture nothing has to decode.
type StubSurface struct {
	// mu guards the recording: the live view plays into it from one request
	// while another asks it for a picture.
	mu sync.Mutex

	Location string
	// Shot is the picture JPEG answers; with neither Shot nor OnShot set it
	// answers a short stand-in.
	Shot   []byte
	OnShot func() ([]byte, error)

	// Acted is every mouse and keyboard call, in order, as a short phrase:
	// "click 40,60 left x2", "type ok", "key Enter".
	Acted []string
	Shots int
}

func NewStubSurface(location string) *StubSurface {
	return &StubSurface{Location: location}
}

func (s *StubSurface) URL() string { return s.Location }

func (s *StubSurface) did(phrase string, args ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Acted = append(s.Acted, fmt.Sprintf(phrase, args...))
	return nil
}

func (s *StubSurface) MouseMove(x, y float64) error { return s.did("move %.0f,%.0f", x, y) }

func (s *StubSurface) MouseDown(button string) error { return s.did("down %s", button) }

func (s *StubSurface) MouseUp(button string) error { return s.did("up %s", button) }

func (s *StubSurface) MouseClick(x, y float64, button string, clicks int) error {
	return s.did("click %.0f,%.0f %s x%d", x, y, button, clicks)
}

func (s *StubSurface) MouseWheel(dx, dy float64) error { return s.did("wheel %.0f,%.0f", dx, dy) }

func (s *StubSurface) KeyDown(key string) error { return s.did("keydown %s", key) }

func (s *StubSurface) KeyUp(key string) error { return s.did("keyup %s", key) }

func (s *StubSurface) KeyPress(key string) error { return s.did("key %s", key) }

func (s *StubSurface) KeyType(text string) error { return s.did("type %s", text) }

func (s *StubSurface) JPEG() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Shots++
	if s.OnShot != nil {
		return s.OnShot()
	}
	if s.Shot != nil {
		return s.Shot, nil
	}
	return []byte("\xff\xd8\xff stub"), nil
}

// StubCaster is a Caster with no CDP behind it: it records what was sent and
// paints whatever frames a test hands it.
type StubCaster struct {
	Sent []string
	// Acked is the session id of every frame acknowledged.
	Acked []any

	handlers []func(any)
}

func (c *StubCaster) Send(method string, params map[string]any) (any, error) {
	c.Sent = append(c.Sent, method)
	if method == "Page.screencastFrameAck" {
		c.Acked = append(c.Acked, params["sessionId"])
	}
	return nil, nil
}

func (c *StubCaster) On(name string, handler any) {
	typed, ok := handler.(func(any))
	if !ok || name != "Page.screencastFrame" {
		return
	}
	c.handlers = append(c.handlers, typed)
}

func (c *StubCaster) Detach() error {
	c.Sent = append(c.Sent, "detach")
	return nil
}

// Paint hands the view one screencast frame, shaped as Chromium's own arrives.
func (c *StubCaster) Paint(sessionID int, data string, width, height int) {
	for _, handler := range c.handlers {
		handler(map[string]any{
			"data":      data,
			"sessionId": float64(sessionID),
			"metadata": map[string]any{
				"deviceWidth":  float64(width),
				"deviceHeight": float64(height),
			},
		})
	}
}
