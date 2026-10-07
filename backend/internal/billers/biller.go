// Package billers is one module per bill provider and the contract they share;
// see docs/connectors/adding-a-bill-provider.md.
//
// A module declares behaviour, never facts: id, name, access path and the rest
// come from domain.Billers through Biller(). Money is domain.Money from the
// moment it is read, and a figure a module cannot read is a note and no bill,
// never 0.
package billers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Session is a module's own kept session, sealed by the caller. The engine
// reads only its kind and expiry; everything else is the module's.
type Session json.RawMessage

// Kind is what the module called this session's shape, or "" for a browser's
// storage state, which has none.
func (s Session) Kind() string {
	var shape struct {
		Kind string `json:"kind"`
	}
	if len(s) == 0 || json.Unmarshal(s, &shape) != nil {
		return ""
	}
	return shape.Kind
}

// ExpiresAt is when the module said this session stops working, or the zero
// time when it said nothing.
func (s Session) ExpiresAt() time.Time {
	var shape struct {
		ExpiresAt string `json:"expires_at"`
	}
	if len(s) == 0 || json.Unmarshal(s, &shape) != nil || shape.ExpiresAt == "" {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339, shape.ExpiresAt)
	if err != nil {
		return time.Time{}
	}
	return at
}

// Expired treats a session with no stated expiry as gone, and one a minute
// from expiry as gone already, because a token that dies mid-pull is a pull
// that has to start over.
func (s Session) Expired(now time.Time) bool {
	at := s.ExpiresAt()
	return at.IsZero() || !at.After(now.Add(time.Minute))
}

// Credentials are what a sign-in is made with. Nothing here is ever logged,
// noted or answered.
type Credentials struct {
	Username string
	Password string
	// Code is a second factor somebody typed.
	Code string
	// Secret is the authenticator's setup key, kept beside the password. A
	// module mints the code from it at the moment it signs in, because a code
	// minted when the pull started has expired by the time a slow provider
	// asks for it.
	Secret string
	// SecondFactor is how the household chose to answer this login's second
	// factor: "", "email", "sms" or "totp". A factor page takes it, or the
	// sign-in stops naming what was offered.
	SecondFactor string
}

func (c Credentials) HasLogin() bool { return c.Username != "" && c.Password != "" }

// String, GoString and LogValue say which fields are set and nothing more, so
// a %v of a session or a slog attribute that reaches these prints no secret.
func (c Credentials) String() string {
	return fmt.Sprintf("billers.Credentials{Username:%s Password:%s Code:%s Secret:%s}",
		redacted(c.Username), redacted(c.Password), redacted(c.Code), redacted(c.Secret))
}

func (c Credentials) GoString() string { return c.String() }

func (c Credentials) LogValue() slog.Value { return slog.StringValue(c.String()) }

func redacted(value string) string {
	if value == "" {
		return `""`
	}
	return "[redacted]"
}

// Subaccount is one thing a login bills, as the provider names it.
type Subaccount = provider.BillSubaccountRef

// Bill is one statement as a module read it.
type Bill = provider.WireBill

// Document is a statement's bytes.
type Document struct {
	Bytes       []byte
	ContentType string
	Filename    string
}

// Challenge is a provider stopping to ask something only an answer can settle.
// An API module sets Answer to finish its own call, since there is no page to
// type into; a browser module leaves it nil and the engine types into the page.
type Challenge struct {
	// State is otp, captcha or approval — the merchants' own words.
	State string
	// Method names the channel an otp came by: totp, sms, email, push.
	Method string
	Prompt string
	// Image is a PNG, base64, for a CAPTCHA.
	Image  string
	Answer func(ctx context.Context, code string, notes *Notes) SignIn
}

// SignIn is what an attempt at signing in came to: exactly one of the three.
type SignIn struct {
	Session   Session
	Challenge *Challenge
	// Failed is the provider's own reason, for a sign-in it refused.
	Failed string
	// Err is a sign-in that never reached the provider's answer: the call
	// failed, or the provider answered with its own outage. Failed says the
	// same in words for the dialog; a pull reads this, because a password
	// the provider never saw has not been refused.
	Err error
}

// Payment is one payment a provider lists as received.
type Payment = provider.WirePayment

// Pull is what a module found, or why it could not look.
type Pull struct {
	Bills []Bill
	// Payments are the money the provider says it received, at a provider
	// whose statements are receipts (catalogue Medical).
	Payments []Payment
	// Subaccounts are the billed accounts as the pull found them, at a module
	// that reads them anyway, so a label the provider's own page gives is
	// carried to accounts already on file.
	Subaccounts []Subaccount
	// Session is the rolled one, where the module rotated it. Empty leaves the
	// session as it was.
	Session     Session
	NeedsSignIn bool
	Reason      string
	// Image is what the wall looked like, base64, when the module thought it
	// worth seeing.
	Image     string
	Challenge *Challenge
}

// Call is everything a module's work is given.
type Call struct {
	Ctx context.Context
	// Session is the kept one, for a module that keeps a token.
	Session Session
	// Page is the browser this pull opened, for a module that reads one. Nil
	// at an API provider.
	Page browser.Page
	// Fetch is how a module makes an HTTP call: a plain client, or one whose
	// calls run inside Chromium where the provider's site only answers a
	// browser.
	Fetch browser.Fetcher
	// Subaccounts are the provider's own keys for what to read. Empty means
	// the module was asked for nothing.
	Subaccounts []string
	// Site is the deployment at a per-customer provider (see SiteModule). The
	// module is already aimed at it; this is for a reader that must put the slug
	// in a path or payload. Empty at every shared provider.
	Site  string
	Notes *Notes
	Now   agent.Clock
	// Trail keeps a line on the pull's trail, which the connection keeps
	// after the pull for anybody asking what the provider showed: the
	// sentence, and with look the page as it stands. Nil keeps nothing; a
	// module calls it through Saw and Mark.
	Trail func(note string, look bool)
}

// Saw keeps the page as it stands on the pull's trail, with a sentence
// saying which page it is.
func (c Call) Saw(note string) {
	if c.Trail != nil {
		c.Trail(note, true)
	}
}

// Mark keeps a sentence on the pull's trail without the page.
func (c Call) Mark(format string, args ...any) {
	if c.Trail != nil {
		c.Trail(fmt.Sprintf(format, args...), false)
	}
}

// At is the call's idea of now.
func (c Call) At() time.Time { return c.Now.At() }

// Wanted says whether this billed account was asked for. Nothing asked for is
// nothing wanted: the engine always names what it wants.
func (c Call) Wanted(externalID string) bool {
	for _, one := range c.Subaccounts {
		if one == externalID {
			return true
		}
	}
	return false
}

// Notes is what a module tells the household about a pull that half-worked.
type Notes = agent.Notes

// ErrNeedsSignIn is a module saying the kept session no longer opens anything,
// from the account walk as well as from a pull.
var ErrNeedsSignIn = errors.New("billers: the provider refused the kept session")

// Module is what every provider module is. The facts come from the catalogue.
type Module interface {
	ID() domain.BillerID
	// Subaccounts is what this login bills.
	Subaccounts(call Call) ([]Subaccount, error)
	FetchBills(call Call) (Pull, error)
	// FetchDocument is one bill's statement, or nil when there is none. Its
	// error never fails a pull: the figures are what a reminder needs.
	FetchDocument(call Call, bill Bill) (*Document, error)
}

// SiteModule is a provider that is one product deployed per customer. The
// engine aims it once, where it picks it, so every address downstream is
// already this connection's.
type SiteModule interface {
	Module
	WithSite(site string) Module
}

// SiteHome is an aimed SiteModule whose deployments are not under the
// catalogue's Home: SiteHome is this deployment's own address, the fence a
// developer steer stays inside, and "" while the module is unaimed.
type SiteHome interface {
	SiteHome() string
}

// APIModule is a provider reached over HTTP with a kept token. No page, no
// selectors.
type APIModule interface {
	Module
	// SessionKinds are the shapes of kept session this module takes, so a
	// token from another provider is refused before it is sent anywhere.
	SessionKinds() []string
	Authenticate(ctx context.Context, credentials Credentials, call Call) SignIn
	// Refresh renews a kept session, or says a sign-in is owed.
	Refresh(call Call) (Session, bool, string)
	// BrowserOrigin is the provider's own origin when this module's calls have
	// to be made from inside Chromium, and "" when plain HTTP serves. The
	// second value is a light document at that origin to sit on.
	BrowserOrigin() (string, string)
}

// BrowserModule is a provider signed in to in a page.
type BrowserModule interface {
	Module
	agent.SignInModule
}

// The states a sign-in page can be in (agent.State*).
const (
	StateEmail       = agent.StateEmail
	StatePassword    = agent.StatePassword
	StateOTP         = agent.StateOTP
	StateCaptcha     = agent.StateCaptcha
	StateApproval    = agent.StateApproval
	StateFactor      = agent.StateFactor
	StateSignedIn    = agent.StateSignedIn
	StateInteractive = agent.StateInteractive
	StateFailed      = agent.StateFailed
)

// State is what a page is showing.
type State = agent.State
