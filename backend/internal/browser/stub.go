package browser

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// StubPage is a Page that is not a browser, shared by several packages' tests.
// A nil answer function is a method that does nothing and succeeds.
type StubPage struct {
	Location string
	PageText string
	// Firefox is a page standing in for a Camoufox one.
	Firefox bool

	OnGoto     func(url string) error
	OnEvaluate func(script string, arg any) (any, error)
	OnWaitFor  func(script string, timeout time.Duration) error
	OnFill     func(selector, value string) error
	OnTypeInto func(selector, value string) error
	OnClick    func(selector string) error
	// OnForceClick unset means a forced click landed.
	OnForceClick func(selector string) error
	OnClickText  func(selectors string, pattern *regexp.Regexp) (bool, error)
	// OnChoose unset means the first click landed.
	OnChoose   func(target, radio string) (string, error)
	OnCount    func(selector string) (int, error)
	OnCheck    func(selector string) (bool, error)
	OnScreen   func() ([]byte, error)
	OnScreenOf func(selector string) ([]byte, error)
	OnSelect   func(selector string, pattern *regexp.Regexp) (string, error)
	// Missing says a selector matches nothing on this page.
	Missing func(selector string) bool
	// OnBytes answers Bytes; unset, the address answers nothing.
	OnBytes func(address string) (int, string, []byte, error)

	// What happened, in order.
	Visited []string
	Filled  []StubFill
	// TypedInto is TypeInto's record, apart from Filled so a test says which
	// of the two a field was given.
	TypedInto []StubFill
	Clicked   []string
	// Forced is ForceClickVisible's record, apart from Clicked.
	Forced  []string
	Checked []string
	Pressed []string
	Typed   []string
	Scripts []string
	// Args is every Evaluate argument as the caller built it, before it was
	// shaped for the page.
	Args     []any
	Slept    time.Duration
	Selected []string
	// Opened is every address Bytes went to.
	Opened []string

	// Closed is Close having been called.
	Closed bool
	// Cookies is the names CookieNames answers, whatever the addresses.
	Cookies []string

	listeners []func(r Response)
	fetchers  []func(d Download)
	openers   []func(popup Page)
}

func (p *StubPage) OnPopup(handler func(popup Page)) {
	p.openers = append(p.openers, handler)
}

// Popup hands a page to every OnPopup handler, as the browser does when this
// page opens a window.
func (p *StubPage) Popup(popup *StubPage) {
	for _, opener := range p.openers {
		opener(popup)
	}
}

func (p *StubPage) CookieNames(...string) ([]string, error) { return p.Cookies, nil }

func (p *StubPage) Close() error {
	p.Closed = true
	return nil
}

type StubFill struct{ Selector, Value string }

func (p *StubPage) URL() string { return p.Location }

func (p *StubPage) Title() (string, error) { return "", nil }

func (p *StubPage) Goto(url string) error {
	p.Visited = append(p.Visited, url)
	p.Location = url
	if p.OnGoto != nil {
		return p.OnGoto(url)
	}
	return nil
}

// Evaluate shapes the argument exactly as a real page does before answering.
// Args keeps what the caller actually built.
func (p *StubPage) Evaluate(script string, arg any) (any, error) {
	p.Args = append(p.Args, arg)
	shaped, err := jsonArg(arg)
	if err != nil {
		return nil, err
	}
	if p.OnEvaluate != nil {
		return p.OnEvaluate(script, shaped)
	}
	return nil, nil
}

func (p *StubPage) WaitForFunction(script string, timeout time.Duration) error {
	if p.OnWaitFor != nil {
		return p.OnWaitFor(script, timeout)
	}
	return nil
}

func (p *StubPage) AddInitScript(script string) error {
	p.Scripts = append(p.Scripts, script)
	return nil
}

func (p *StubPage) Fill(selector, value string) error {
	p.Filled = append(p.Filled, StubFill{Selector: selector, Value: value})
	if p.OnFill != nil {
		return p.OnFill(selector, value)
	}
	return nil
}

func (p *StubPage) FillVisible(selector, value string) (bool, error) {
	if p.Missing != nil && p.Missing(selector) {
		return false, nil
	}
	if err := p.Fill(selector, value); err != nil {
		return false, err
	}
	return true, nil
}

func (p *StubPage) TypeInto(selector, value string) error {
	p.TypedInto = append(p.TypedInto, StubFill{Selector: selector, Value: value})
	if p.OnTypeInto != nil {
		return p.OnTypeInto(selector, value)
	}
	return nil
}

func (p *StubPage) TypeIntoVisible(selector, value string) (bool, error) {
	if p.Missing != nil && p.Missing(selector) {
		return false, nil
	}
	if err := p.TypeInto(selector, value); err != nil {
		return false, err
	}
	return true, nil
}

func (p *StubPage) Click(selector string) error {
	p.Clicked = append(p.Clicked, selector)
	if p.OnClick != nil {
		return p.OnClick(selector)
	}
	return nil
}

func (p *StubPage) ClickVisible(selector string) (bool, error) {
	if p.Missing != nil && p.Missing(selector) {
		return false, nil
	}
	return true, p.Click(selector)
}

func (p *StubPage) ForceClickVisible(selector string) (bool, error) {
	if p.Missing != nil && p.Missing(selector) {
		return false, nil
	}
	p.Forced = append(p.Forced, selector)
	if p.OnForceClick != nil {
		return true, p.OnForceClick(selector)
	}
	return true, nil
}

// Choose records the press as a click, so a test reads the choice and the
// button that sends it in order.
func (p *StubPage) Choose(target, radio string) (string, error) {
	if p.Missing != nil && p.Missing(target) {
		return "", nil
	}
	if p.OnChoose != nil {
		took, err := p.OnChoose(target, radio)
		if took != "" {
			p.Clicked = append(p.Clicked, target)
		}
		return took, err
	}
	if err := p.Click(target); err != nil {
		return "", err
	}
	return TookClick, nil
}

func (p *StubPage) ClickText(selectors string, pattern *regexp.Regexp) (bool, error) {
	if p.OnClickText != nil {
		return p.OnClickText(selectors, pattern)
	}
	return false, nil
}

func (p *StubPage) CheckIfUnchecked(selector string) (bool, error) {
	if p.OnCheck == nil {
		return false, nil
	}
	ticked, err := p.OnCheck(selector)
	if ticked {
		p.Checked = append(p.Checked, selector)
	}
	return ticked, err
}

func (p *StubPage) Count(selector string) (int, error) {
	if p.OnCount != nil {
		return p.OnCount(selector)
	}
	return 0, nil
}

func (p *StubPage) Press(key string) error {
	p.Pressed = append(p.Pressed, key)
	return nil
}

func (p *StubPage) Type(text string) error {
	p.Typed = append(p.Typed, text)
	return nil
}

func (p *StubPage) Settle() {}

func (p *StubPage) Sleep(d time.Duration) { p.Slept += d }

func (p *StubPage) Screenshot() ([]byte, error) {
	if p.OnScreen != nil {
		return p.OnScreen()
	}
	return []byte("\x89PNG stub"), nil
}

func (p *StubPage) ScreenshotOf(selector string) ([]byte, error) {
	if p.OnScreenOf != nil {
		return p.OnScreenOf(selector)
	}
	return nil, nil
}

func (p *StubPage) SelectMatching(selector string, pattern *regexp.Regexp) (string, error) {
	if p.OnSelect == nil {
		return "", nil
	}
	picked, err := p.OnSelect(selector, pattern)
	if picked != "" {
		p.Selected = append(p.Selected, picked)
	}
	return picked, err
}

func (p *StubPage) OnResponse(handler func(r Response)) {
	p.listeners = append(p.listeners, handler)
}

// Respond hands a response to every OnResponse handler, as the browser does
// when the page receives one.
func (p *StubPage) Respond(response StubResponse) {
	for _, listener := range p.listeners {
		listener(response)
	}
}

// StubResponse is a Response that is not a browser's.
type StubResponse struct {
	Address string
	Code    int
	Headers map[string]string
	Payload []byte
}

func (r StubResponse) URL() string           { return r.Address }
func (r StubResponse) Status() int           { return r.Code }
func (r StubResponse) Body() ([]byte, error) { return r.Payload, nil }

func (r StubResponse) Header(name string) string {
	for key, value := range r.Headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func (p *StubPage) OnDownload(handler func(d Download)) {
	p.fetchers = append(p.fetchers, handler)
}

// Download hands a download to every OnDownload handler, as the browser does
// when a navigation is answered with a file.
func (p *StubPage) Download(download StubDownload) {
	for _, fetcher := range p.fetchers {
		fetcher(download)
	}
}

// StubDownload is a Download that is not a browser's; Err is what saving it
// answers.
type StubDownload struct {
	Address string
	Payload []byte
	Err     error
}

func (d StubDownload) URL() string            { return d.Address }
func (d StubDownload) Bytes() ([]byte, error) { return d.Payload, d.Err }

func (p *StubPage) Bytes(address string) (int, string, []byte, error) {
	p.Opened = append(p.Opened, address)
	if p.OnBytes != nil {
		return p.OnBytes(address)
	}
	return 0, "", nil, errors.New("browser: the stub page opens nothing")
}
