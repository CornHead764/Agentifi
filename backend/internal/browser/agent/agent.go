// Package agent is the contract between internal/connector and the modules it
// drives (internal/billers and internal/merchants): the page states a sign-in
// reports, the sign-in module a connector implements, the browser a session
// holds, the choice between Chrome and Camoufox, where a connector's calls go,
// and pressing a form and waiting for the page to answer.
//
// A connector that runs in Camoufox never runs in Chrome: with no Camoufox
// server both its browser and its calls are browser.ErrNoFirefox.
package agent

import (
	"encoding/base64"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The states a sign-in page can be in. `email` and `password` are answered by
// the loop itself and never reported, and `factor` (which second factor) is
// pressed through by the loop too; the rest are provider.MerchantSignIn* and
// the bill connect states alike.
const (
	StateEmail       = "email"
	StatePassword    = "password"
	StateOTP         = "otp"
	StateCaptcha     = "captcha"
	StateApproval    = "approval"
	StateFactor      = "factor"
	StateSignedIn    = "signed_in"
	StateInteractive = "interactive"
	StateFailed      = "failed"
)

// MethodAuthenticator is the Method of a code from an authenticator app, which
// a kept setup key can answer; a code sent by text or e-mail cannot be.
const MethodAuthenticator = "totp"

// State is what a page is showing.
type State struct {
	State string
	// Method is the channel a code comes by: MethodAuthenticator, "email",
	// "sms", or "" where the page does not say.
	Method string
	Prompt string
	Image  string
	Error  string
	// Blocking is a check page the site shows in front of everything else,
	// rather than a puzzle inside its sign-in form; where nothing typed answers
	// it, a person or a kept profile is what clears it.
	Blocking bool
	// Bridged is how many one-button interstitial pages the reading pressed
	// through. Nothing branches on it; it feeds the bills trail.
	Bridged int
	// Choices is the menu a factor page offered when nothing on it could be
	// answered, so the failure can name what was on offer.
	Choices []FactorChoice
}

// Authenticator is whether the code asked for is an authenticator app's.
func (s State) Authenticator() bool { return s.Method == MethodAuthenticator }

// FactorChoice is one way to verify, as a factor page offers it.
type FactorChoice struct {
	// At is the choice's place in the page's own query, which is how the click
	// finds the one the ranking picked.
	At   int    `json:"at"`
	Kind string `json:"kind"`
	// Words is the choice's accessible name, the provider's own words; digits
	// the portal wrote about the household come off before it is kept.
	Words string `json:"words"`
	// Selects says choosing this one selects and sends nothing, so the page's
	// own button has to be pressed after it.
	Selects bool `json:"selects"`
	// Checked says the page already has this one selected. Clicking again would
	// untick a custom radio that toggles.
	Checked bool `json:"checked"`
}

// Screenshot is the page as a person would see it, base64 PNG, or "" for a
// page mid-navigation, which is not a failure of its own.
func Screenshot(page browser.Page) string {
	if page == nil {
		return ""
	}
	shot, err := page.Screenshot()
	if err != nil || len(shot) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(shot)
}

// Pictured is err with the page it happened on, as a provider.PageFailure. An
// error already pictured, or one with no page to show, is returned as it is.
func Pictured(page browser.Page, err error) error {
	if err == nil || page == nil || provider.ScreenshotOf(err) != nil {
		return err
	}
	shot, shotErr := page.Screenshot()
	if shotErr != nil || len(shot) == 0 {
		return err
	}
	return &provider.PageFailure{Err: err, Screenshot: shot}
}
