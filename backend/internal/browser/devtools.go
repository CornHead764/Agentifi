package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"

	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The developer helpers (owner-only routes) for steering a live browser while
// writing a provider module. Two safety rules:
//
//   - Never off the provider's own site: a signed-in browser sent to an
//     address somebody else chose is a session handed over. Every steer goes
//     through WithinSite.
//   - Never a field's value: the page reading takes labels, the DOM reader
//     drops `value` and anything named token, secret or password, and the
//     capture keeps only the scheme of an authorization header and a body
//     passed through Redacted.

const (
	pageTextCap     = 60 * 1024
	pageElementsCap = 400
	domLimitCap     = 40
	domTextCap      = 300
	domHTMLCap      = 2500
	domAttributeCap = 200
	capturedCap     = 200
	capturedBodyCap = 3000
	capturedAnswer  = 6000
)

// WithinSite is the absolute address a steer may go to: the requested one when
// it is on home's host or a subdomain of it, and "" otherwise. A relative
// address resolves against current.
func WithinSite(requested, home, current string) string {
	address := strings.TrimSpace(requested)
	if address == "" {
		// An empty address would resolve to the current page and read as allowed.
		return ""
	}
	base := current
	if base == "" {
		base = home
	}
	root, err := url.Parse(base)
	if err != nil {
		return ""
	}
	target, err := root.Parse(address)
	if err != nil {
		return ""
	}
	if target.Scheme != "https" && target.Scheme != "http" {
		return ""
	}
	site, err := url.Parse(home)
	if err != nil {
		return ""
	}
	wanted := strings.ToLower(strings.TrimPrefix(site.Hostname(), "www."))
	if wanted == "" {
		return ""
	}
	host := strings.ToLower(target.Hostname())
	if host == wanted || strings.HasSuffix(host, "."+wanted) {
		return target.String()
	}
	return ""
}

// FrameCensus is one of a page's frames, for a diagnostic that has to say
// where a form is: the classifier reads only the main frame, so a form in an
// iframe reads as a page with nothing on it.
type FrameCensus struct {
	// Name is "main" for the document itself, the frame's own name, or
	// "(unnamed)".
	Name string
	// URL has identifiers stripped: see frameAddress.
	URL string
	// Inputs counts the frame's boxes; Readable says whether it could be asked.
	Inputs   int
	Readable bool
}

// FramesOf is nil for a page with no browser behind it.
func FramesOf(p Page) []FrameCensus {
	live, ok := Unwrap(p)
	if !ok {
		return nil
	}
	frames := live.Frames()
	out := make([]FrameCensus, 0, len(frames))
	for index, frame := range frames {
		one := FrameCensus{Name: "main", URL: frameAddress(frame.URL())}
		if index > 0 {
			one.Name = frame.Name()
			if one.Name == "" {
				one.Name = "(unnamed)"
			}
		}
		if answered, err := frame.Evaluate("() => document.querySelectorAll('input').length"); err == nil {
			one.Readable = true
			if count, ok := answered.(int); ok {
				one.Inputs = count
			} else if count, ok := answered.(float64); ok {
				one.Inputs = int(count)
			}
		}
		out = append(out, one)
	}
	return out
}

// PageText is a live browser's page as words.
type PageText struct {
	Provider string
	URL      string
	Title    string
	// Text is the visible text, capped, with the page's links, buttons and
	// fields listed under it.
	Text string
}

// PageReading is what the in-page script answers. `value` is deliberately
// never read.
type PageReading struct {
	Text     string        `json:"text"`
	Elements []PageElement `json:"elements"`
}

type PageElement struct {
	Tag  string `json:"tag"`
	Text string `json:"text"`
	Name string `json:"name"`
	ID   string `json:"id"`
	Type string `json:"type"`
	Href string `json:"href"`
}

const readPage = `() => {` + EscapedJS + `
  const label = (el) => {
    const own = (el.innerText || el.placeholder || '').trim();
    if (own) return own;
    const aria = el.getAttribute('aria-label') || '';
    if (aria) return aria;
    const id = el.getAttribute('id');
    const tied = id ? document.querySelector('label[for="' + escaped(id) + '"]') : null;
    return tied ? (tied.innerText || '').trim() : '';
  };
  const elements = [];
  for (const el of document.querySelectorAll('a, button, input, select')) {
    const box = el.getBoundingClientRect();
    if (box.width === 0 && box.height === 0) continue;
    elements.push({
      tag: el.tagName.toLowerCase(),
      text: label(el),
      name: el.getAttribute('name') || '',
      id: el.getAttribute('id') || '',
      type: el.getAttribute('type') || '',
      href: el.tagName === 'A' ? el.getAttribute('href') || '' : '',
    });
    if (elements.length >= 400) break;
  }
  return { text: document.body ? document.body.innerText : '', elements };
}`

// Snapshot is the live page in text. Nothing that identifies the session
// crosses: no cookies, storage, headers or field values.
func Snapshot(provider string, page Page) PageText {
	title, _ := page.Title()
	var read PageReading
	_ = EvaluateInto(page, readPage, nil, &read)
	return ShapePage(provider, page.URL(), title, read)
}

func ShapePage(provider, address, title string, read PageReading) PageText {
	text := textutil.Clip(read.Text, pageTextCap)
	elements := read.Elements
	if len(elements) > pageElementsCap {
		elements = elements[:pageElementsCap]
	}
	lines := make([]string, 0, len(elements))
	for _, element := range elements {
		lines = append(lines, elementLine(element))
	}
	if len(lines) > 0 {
		text = text + "\n\n--- interactive elements ---\n" + strings.Join(lines, "\n")
	}
	return PageText{Provider: provider, URL: address, Title: title, Text: text}
}

func elementLine(element PageElement) string {
	parts := []string{strings.ToLower(element.Tag)}
	for _, pair := range [][2]string{
		{"type", element.Type}, {"name", element.Name},
		{"id", element.ID}, {"href", element.Href},
	} {
		if pair[1] != "" {
			parts = append(parts, pair[0]+"="+textutil.Clip(pair[1], domAttributeCap))
		}
	}
	line := strings.Join(parts, " ")
	label := textutil.Clip(strings.TrimSpace(collapseSpace(element.Text)), 120)
	if label == "" {
		return line
	}
	return line + "  " + label
}

type DOMElement struct {
	Tag        string            `json:"tag"`
	Attributes map[string]string `json:"attributes"`
	Text       string            `json:"text"`
	HTML       string            `json:"html"`
}

var secretAttribute = regexp.MustCompile(`(?i)token|secret|password`)

// valueAttribute strips values from outerHTML, which carries what was typed.
// Case-insensitive and either quote: Chromium answers outerHTML with the
// attribute spelled as the page spelled it.
var valueAttribute = regexp.MustCompile(`(?i)\svalue\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)

// readDOM drops the same attributes Redact does, so a value never crosses the
// process boundary at all.
const readDOM = `({ selector, limit }) => {` + CleanJS + `
  const cap = Math.min(Number(limit) || 10, 40);
  return [...document.querySelectorAll(selector)].slice(0, cap).map((el) => ({
    tag: el.tagName.toLowerCase(),
    attributes: Object.fromEntries(
      [...el.attributes]
        .filter((a) => a.name !== 'value' && !/token|secret|password/i.test(a.name))
        .map((a) => [a.name, String(a.value).slice(0, 200)])
    ),
    text: clean(el.innerText || el.textContent || '').slice(0, 300),
    html: el.outerHTML.replace(/\svalue\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)/gi, ' value=""').slice(0, 2500),
  }));
}`

func ReadDOM(page Page, selector string, limit int) ([]DOMElement, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > domLimitCap {
		limit = domLimitCap
	}
	var found []DOMElement
	err := EvaluateInto(page, readDOM,
		map[string]any{"selector": selector, "limit": limit}, &found)
	if err != nil {
		return nil, err
	}
	return Redact(found), nil
}

// Redact applies readDOM's rule again on this side: it keeps a value out of an
// answer if the script is ever loosened or a page rewrites the reading, and it
// is the half a test can hold.
func Redact(elements []DOMElement) []DOMElement {
	out := make([]DOMElement, 0, len(elements))
	for _, element := range elements {
		clean := DOMElement{
			Tag:        element.Tag,
			Text:       textutil.Clip(element.Text, domTextCap),
			HTML:       textutil.Clip(valueAttribute.ReplaceAllString(element.HTML, ` value=""`), domHTMLCap),
			Attributes: map[string]string{},
		}
		for name, value := range element.Attributes {
			if strings.EqualFold(name, "value") || secretAttribute.MatchString(name) {
				continue
			}
			clean.Attributes[name] = textutil.Clip(value, domAttributeCap)
		}
		out = append(out, clean)
	}
	return out
}

type PageFetch struct {
	Status      int      `json:"status"`
	ContentType string   `json:"content_type"`
	Length      int      `json:"length"`
	Text        string   `json:"text"`
	Matches     []string `json:"matches"`
}

// FetchPage is a call made by the signed-in page itself, so the provider's own
// cookies and its edge's opinion of this client both apply. Nothing is stored.
// find answers each match in a large body (an app bundle) with a little context
// instead of the body.
func FetchPage(
	ctx context.Context, page Page, target, method, body string, headers map[string]string,
	find string, around int,
) (PageFetch, error) {
	if method == "" {
		method = "GET"
	}
	around = min(max(around, 400), 6000)
	call := PageCall{URL: target, Method: method, Headers: headers, Read: ReadText}
	if body != "" {
		call.Body = body
	}
	if find != "" {
		call.Read, call.Find, call.Around = ReadMatches, find, around
	}
	answer, err := CallFromPage(ctx, page, call)
	if err != nil {
		return PageFetch{}, err
	}
	if answer.Error != "" {
		return PageFetch{}, errors.New(answer.Error)
	}
	out := PageFetch{Status: answer.Status, ContentType: answer.Type, Length: answer.Length, Matches: answer.Matches}
	if find == "" {
		out.Text, out.Length, out.Matches = answer.Text, 0, nil
	}
	return out, nil
}

type CapturedRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Status int    `json:"status"`
	Type   string `json:"type"`
	// Authorization is the scheme only ("Bearer"), never the credential.
	Authorization string `json:"authorization,omitempty"`
	Body          string `json:"body,omitempty"`
	Response      string `json:"response,omitempty"`
}

// CapturedDownload is a file the steer started; it is cancelled.
type CapturedDownload struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

type Traffic struct {
	Requests []CapturedRequest `json:"requests"`
	Download *CapturedDownload `json:"download"`
	Opened   string            `json:"opened"`
}

// Capture runs a steer and reports what the page fetched, opened and
// downloaded. A page this package did not wrap runs without capture.
func Capture(page Page, run func()) Traffic {
	out := Traffic{Requests: []CapturedRequest{}}
	live, ok := page.(*livePage)
	if !ok {
		run()
		return out
	}
	real := live.page
	var mu sync.Mutex
	var responses []playwright.Response
	var downloads []playwright.Download
	var opened []playwright.Page

	// The handlers only record: calling back into the driver from inside its
	// own dispatch deadlocks. Bodies are read and downloads cancelled after run().
	onResponse := func(response playwright.Response) {
		mu.Lock()
		defer mu.Unlock()
		if len(responses) < capturedCap {
			responses = append(responses, response)
		}
	}
	onDownload := func(download playwright.Download) {
		mu.Lock()
		defer mu.Unlock()
		downloads = append(downloads, download)
	}
	onPage := func(other playwright.Page) {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, other)
	}
	real.OnResponse(onResponse)
	real.OnDownload(onDownload)
	context := real.Context()
	if context != nil {
		context.OnPage(onPage)
	}
	func() {
		defer func() {
			real.RemoveListener("response", onResponse)
			real.RemoveListener("download", onDownload)
			if context != nil {
				context.RemoveListener("page", onPage)
			}
		}()
		run()
		page.Settle()
	}()

	here := page.URL()
	mu.Lock()
	seen, files, pages := responses, downloads, opened
	mu.Unlock()
	for _, response := range seen {
		if entry, keep := capturedRequest(response, here); keep {
			out.Requests = append(out.Requests, entry)
		}
	}
	for _, download := range files {
		out.Download = &CapturedDownload{
			Filename: download.SuggestedFilename(),
			URL:      WithoutQuery(download.URL()),
		}
		_ = download.Cancel()
	}
	for _, other := range pages {
		_ = other.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State:   playwright.LoadStateDomcontentloaded,
			Timeout: playwright.Float(float64((3 * time.Second).Milliseconds())),
		})
		out.Opened = WithoutQuery(other.URL())
		_ = other.Close()
	}
	return out
}

// capturedRequest lists same-site scripts by name alone, so a lazily loaded
// bundle can be found; only the provider's own data calls carry bodies, and a
// third party is named and nothing more.
func capturedRequest(response playwright.Response, here string) (CapturedRequest, bool) {
	request := response.Request()
	kind := request.ResourceType()
	address := response.URL()
	sameSite := sameHost(address, here)
	if kind == "script" {
		if !sameSite {
			return CapturedRequest{}, false
		}
		return CapturedRequest{
			Method: "GET", URL: WithoutQuery(address), Status: response.Status(), Type: kind,
		}, true
	}
	if kind != "document" && kind != "xhr" && kind != "fetch" {
		return CapturedRequest{}, false
	}
	entry := CapturedRequest{
		Method: request.Method(), URL: WithoutQuery(address),
		Status: response.Status(), Type: kind,
	}
	if !sameSite {
		return entry, true
	}
	if fields := strings.Fields(request.Headers()["authorization"]); len(fields) > 0 {
		entry.Authorization = fields[0]
	}
	if body, err := request.PostData(); err == nil {
		entry.Body = textutil.Clip(Redacted(body), capturedBodyCap)
	}
	if text, err := response.Text(); err == nil {
		entry.Response = textutil.Clip(Redacted(text), capturedAnswer)
	}
	return entry, true
}

var (
	redactedForm = regexp.MustCompile(
		`(?i)([?&]|^)([a-z_]*(?:passw?d?|password|secret|token|key|otp|code|pin|ssn|auth)[a-z_]*)=[^&]*`)
	redactedJSON = regexp.MustCompile(
		`(?i)"([a-z_]*(?:pass|passwd|password|pwd|secret|token|key|otp|code|pin|ssn)[a-z_]*)"(\s*:\s*)"[^"]*"`)
)

// Redacted takes credential-shaped field values out of a captured form-encoded
// or JSON body: the sign-in POST is the one somebody most wants to read.
func Redacted(body string) string {
	if body == "" {
		return ""
	}
	body = redactedForm.ReplaceAllString(body, "${1}${2}=[removed]")
	return redactedJSON.ReplaceAllString(body, `"${1}"${2}"[removed]"`)
}

func sameHost(address, here string) bool {
	target, err := url.Parse(address)
	if err != nil {
		return false
	}
	return WithinSite(target.String(), here, here) != ""
}

// frameAddress cuts at `;` as well as `?` and `#`: advertisers' frames carry
// cookie-sync ids and even this host's IP address after any of the three.
// The page's own landed address keeps its query elsewhere, because an identity
// provider writes the client id and PKCE challenge there.
func frameAddress(address string) string {
	if cut := strings.IndexAny(address, "?#;"); cut >= 0 {
		return address[:cut]
	}
	return address
}

// WithoutQuery is an address with its query string and fragment off: a
// portal's own calls carry tokens and account numbers in one, and a
// sign-in redirect carries a return path and sometimes the username in the
// fragment.
func WithoutQuery(address string) string {
	if cut := strings.IndexAny(address, "?#"); cut >= 0 {
		return address[:cut]
	}
	return address
}

var spaces = regexp.MustCompile(`\s+`)

func collapseSpace(value string) string { return spaces.ReplaceAllString(value, " ") }

// ClickPattern matches the words in order with any whitespace between, because
// a control's text is often split over child elements and lines.
func ClickPattern(text string) (*regexp.Regexp, error) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil, fmt.Errorf("browser: there are no words to press")
	}
	for i, word := range words {
		words[i] = regexp.QuoteMeta(word)
	}
	return regexp.Compile(`(?i)` + strings.Join(words, `\s+`))
}
