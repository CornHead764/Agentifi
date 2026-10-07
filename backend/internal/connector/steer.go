package connector

import (
	"context"
	"net/url"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The developer steers: goto, click, dom and fetch, over a live sign-in, for
// writing a provider module. Owner-only app routes, refused to an in-process
// call, and never off the provider's own site. What may cross is ruled in
// internal/browser/devtools.go.

// steerWait is how long a control is given to appear before a click gives up;
// an SPA paints its lists after the document is ready. Polled each steerRound
// so a control that arrives early is pressed early.
const (
	steerWait  = 10 * time.Second
	steerRound = time.Second
)

func (e Bills) SteerTo(
	ctx context.Context, sessionID, address string,
) (provider.BillSignInSteer, error) {
	s, page, err := e.steerable(sessionID)
	if err != nil {
		return provider.BillSignInSteer{}, err
	}
	target, err := e.within(s, address)
	if err != nil {
		return provider.BillSignInSteer{}, err
	}
	return guarded(e.Engine, s.name, "the steer", func() (provider.BillSignInSteer, error) {
		traffic := browser.Capture(page, func() { _ = page.Goto(target) })
		return e.steered(s, page, traffic), nil
	})
}

func (e Bills) SteerClick(
	ctx context.Context, sessionID, text, selector string,
) (provider.BillSignInSteer, error) {
	s, page, err := e.steerable(sessionID)
	if err != nil {
		return provider.BillSignInSteer{}, err
	}
	if text == "" && selector == "" {
		return provider.BillSignInSteer{}, agentError(provider.ErrAgentBadRequest,
			"text or selector is required")
	}
	return guarded(e.Engine, s.name, "the steer", func() (provider.BillSignInSteer, error) {
		press, err := pressing(page, text, selector)
		if err != nil {
			return provider.BillSignInSteer{}, err
		}
		var pressed bool
		traffic := browser.Capture(page, func() { pressed = waitAndPress(page, press) })
		if !pressed {
			if text != "" {
				return provider.BillSignInSteer{}, agentError(provider.ErrAgentNotFound,
					"nothing on the page reads %q", text)
			}
			return provider.BillSignInSteer{}, agentError(provider.ErrAgentNotFound,
				"nothing on the page matches that selector")
		}
		return e.steered(s, page, traffic), nil
	})
}

// SteerDOM reads the markup behind a selector, read-only, never a field's
// value.
func (e Bills) SteerDOM(
	ctx context.Context, sessionID, selector string, limit int,
) (provider.BillSignInDOM, error) {
	s, page, err := e.steerable(sessionID)
	if err != nil {
		return provider.BillSignInDOM{}, err
	}
	if selector == "" {
		return provider.BillSignInDOM{}, agentError(provider.ErrAgentBadRequest, "selector is required")
	}
	return guarded(e.Engine, s.name, "the dom reading", func() (provider.BillSignInDOM, error) {
		found, err := browser.ReadDOM(page, selector, limit)
		if err != nil {
			return provider.BillSignInDOM{}, agentError(provider.ErrAgentBadRequest,
				"that selector could not be read: %v", err)
		}
		elements := make([]provider.BillSignInDOMElement, 0, len(found))
		for _, one := range found {
			elements = append(elements, provider.BillSignInDOMElement{
				Tag: one.Tag, Attributes: one.Attributes, Text: one.Text, HTML: one.HTML,
			})
		}
		return provider.BillSignInDOM{
			Provider: string(s.module.ID()), URL: page.URL(),
			Count: len(elements), Elements: elements,
		}, nil
	})
}

// SteerFetch makes one call from inside the signed-in page, on the provider's
// site only. Nothing is stored.
func (e Bills) SteerFetch(
	ctx context.Context, sessionID string, request provider.BillSignInFetchRequest,
) (provider.BillSignInFetch, error) {
	s, page, err := e.steerable(sessionID)
	if err != nil {
		return provider.BillSignInFetch{}, err
	}
	target, err := e.within(s, request.URL)
	if err != nil {
		return provider.BillSignInFetch{}, err
	}
	return guarded(e.Engine, s.name, "the page fetch", func() (provider.BillSignInFetch, error) {
		answered, err := browser.FetchPage(ctx, page, target, request.Method, request.Body,
			request.Headers, request.Find, request.Context)
		if err != nil {
			return provider.BillSignInFetch{}, agentError(provider.ErrAgentUpstream,
				"the page could not make that call: %v", err)
		}
		return provider.BillSignInFetch{
			Provider: string(s.module.ID()), URL: target,
			Status: answered.Status, ContentType: answered.ContentType,
			Length: answered.Length, Text: answered.Text, Matches: answered.Matches,
		}, nil
	})
}

func (e Bills) steerable(sessionID string) (*session, browser.Page, error) {
	s, err := e.find(sessionID)
	if err != nil {
		return nil, nil, err
	}
	opened := s.Opened()
	if opened == nil || opened.Page == nil {
		return nil, nil, agentError(provider.ErrAgentConflict, "this connect has no page to steer")
	}
	return s, opened.Page, nil
}

// within is the address a steer may go to, or a refusal: a signed-in browser
// sent to an address somebody else chose is a session handed over.
func (e Bills) within(s *session, address string) (string, error) {
	home := e.home(s.module)
	if home == "" {
		return "", agentError(provider.ErrAgentConflict,
			"%s has no home page to steer within", s.name)
	}
	page := ""
	if opened := s.Opened(); opened != nil && opened.Page != nil {
		page = opened.Page.URL()
	}
	target := browser.WithinSite(address, home, page)
	if target == "" {
		return "", agentError(provider.ErrAgentBadRequest,
			"the live browser stays on %s", hostOf(home))
	}
	return target, nil
}

// home is the catalogue's site for the provider, or an aimed module's own
// deployment where that lies outside it (billers.SiteHome); a module the
// catalogue does not carry is not steerable.
func (e Bills) home(module billers.Module) string {
	biller, known := domain.BillerByID(module.ID())
	if !known {
		return ""
	}
	if sited, ok := module.(billers.SiteHome); ok && biller.NeedsSite {
		return sited.SiteHome()
	}
	return biller.Home
}

func hostOf(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return address
	}
	return parsed.Hostname()
}

func (e *Engine) steered(
	s *session, page browser.Page, traffic browser.Traffic,
) provider.BillSignInSteer {
	read := browser.Snapshot(string(s.module.ID()), page)
	out := provider.BillSignInSteer{
		Provider: read.Provider, URL: read.URL, Title: read.Title, Text: read.Text,
		Requests: make([]provider.BillSignInRequestLine, 0, len(traffic.Requests)),
		Opened:   traffic.Opened,
	}
	for _, one := range traffic.Requests {
		out.Requests = append(out.Requests, provider.BillSignInRequestLine{
			Method: one.Method, URL: one.URL, Status: one.Status, Type: one.Type,
			Authorization: one.Authorization, Body: one.Body, Response: one.Response,
		})
	}
	if traffic.Download != nil {
		out.Download = &provider.BillSignInDownload{
			Filename: traffic.Download.Filename, URL: traffic.Download.URL,
		}
	}
	return out
}

func pressing(page browser.Page, text, selector string) (func() (bool, error), error) {
	if selector != "" {
		return func() (bool, error) { return page.ClickVisible(selector) }, nil
	}
	pattern, err := browser.ClickPattern(text)
	if err != nil {
		return nil, agentError(provider.ErrAgentBadRequest, "text or selector is required")
	}
	return func() (bool, error) {
		return page.ClickText(`a, button, [role="button"], [role="link"]`, pattern)
	}, nil
}

func waitAndPress(page browser.Page, press func() (bool, error)) bool {
	for waited := time.Duration(0); ; waited += steerRound {
		if pressed, err := press(); err == nil && pressed {
			return true
		}
		if waited >= steerWait {
			return false
		}
		page.Sleep(steerRound)
	}
}
