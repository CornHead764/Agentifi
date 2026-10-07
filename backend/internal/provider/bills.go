package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The shapes internal/connector answers a bill connect and pull in, kept here
// so service/ and api/ read them without importing the engine. A statement
// comes back as a ref that FetchDocument trades for the bytes, once.

// The sign_in_paused_for values a bill connection and a merchant account
// share: the provider refused the kept password, or it asked for a code
// nothing kept can answer. Either stops the scheduler signing in until a
// person does.
const (
	SignInPausedPasswordRefused = "password_refused"
	SignInPausedCodeNeeded      = "code_needed"
)

// ErrBillsAgentUnavailable is a build with no bills modules registered.
var ErrBillsAgentUnavailable = errors.New("provider: this build has no bills engine")

// Everything but signing_in, signed_in and failed is a question for somebody;
// `accounts` is a choice rather than a challenge. signing_in is never settled
// into a failure (see connector's `settled`).
const (
	BillConnectSigningIn   = "signing_in"
	BillConnectSignedIn    = "signed_in"
	BillConnectOTP         = "otp"
	BillConnectCaptcha     = "captcha"
	BillConnectApproval    = "approval"
	BillConnectAccounts    = "accounts"
	BillConnectInteractive = "interactive"
	BillConnectFailed      = "failed"
)

// BillProvider is one module the engine carries: what the engine can do, kept
// deliberately apart from domain.Billers (what the app knows), so a mismatch
// shows on the settings page.
type BillProvider struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Access          string           `json:"access"`
	Home            string           `json:"home"`
	SignIn          BillProviderSign `json:"sign_in"`
	Challenges      []string         `json:"challenges"`
	SessionPersists bool             `json:"session_persists"`
	KeepaliveDays   int              `json:"keepalive_days"`
	ReportsAutopay  bool             `json:"reports_autopay"`
	HasDocuments    bool             `json:"has_documents"`
}

type BillProviderSign struct {
	Kinds  []string `json:"kinds"`
	Prompt string   `json:"prompt"`
}

type BillConnectState struct {
	SessionID string `json:"session_id"`
	Provider  string `json:"provider"`
	State     string `json:"state"`
	Prompt    string `json:"prompt"`
	// Image is a base64 PNG.
	Image string `json:"image"`
	Error string `json:"error"`
	// Width and Height are the live browser's viewport.
	Width    int                 `json:"width"`
	Height   int                 `json:"height"`
	Accounts []BillSubaccountRef `json:"accounts"`
	// Method is totp, sms, email or push beside an otp state, when known.
	Method string `json:"method"`
	// Trail is carried on a failed state because the session may be gone by
	// the time anybody asks the trail route for it.
	Trail []BillTrailEntry `json:"trail"`
}

// BillSignInEnded is how a sign-in a person started ended without landing
// (it failed, the person closed it, or nobody came back to it), for the
// connection to keep: a sentence, the page as a base64 PNG ("" when there was
// none) and the trail.
type BillSignInEnded struct {
	SessionID string
	Detail    string
	Image     string
	Trail     []BillTrailEntry
}

// BillTrailEntry is one round of a sign-in, or one page or sentence of a
// pull, as the engine saw it. It never carries what was typed: no field
// value, screenshot or cookie.
type BillTrailEntry struct {
	At time.Time `json:"at"`
	// Step is sign-in, factor, answer or complete, or read for a pull's line,
	// whose State is page (with a Snapshot) or note.
	Step  string `json:"step"`
	State string `json:"state"`
	// URL has no query string: a sign-in address carries tokens in one.
	URL    string              `json:"url"`
	Title  string              `json:"title"`
	Form   BillSignInFormFlags `json:"form"`
	Inputs map[string]int      `json:"inputs"`
	// Error is capped at 200 characters.
	Error string           `json:"error"`
	Did   BillSignInAction `json:"did"`
	// Choices and Chose are set on a factor round only.
	Choices []BillSignInChoice `json:"choices"`
	Chose   string             `json:"chose"`
	// Forced says the round's press had to be taken past the page's own
	// checks: a factor choice neither a click nor a tick would land, or a
	// button whose click timed out with nothing on top of it.
	Forced bool `json:"forced"`
	// Snapshot is the page's structure on the round that ended a sign-in, and
	// on each page a pull read. Never a field's value, an attribute named for
	// a secret, or a long run of digits.
	Snapshot string `json:"snapshot,omitempty"`
	Note     string `json:"note,omitempty"`
}

// BillSignInChoice is one way to verify, as the factor page offered it. The
// masked tail of a phone number is removed from Words before it gets here.
type BillSignInChoice struct {
	// Kind is "radio", "button" or "link".
	Kind  string `json:"kind"`
	Words string `json:"words"`
}

// BillSignInAction is what one round did to the page. Words are the text on
// the provider's own control, never anything a household typed.
type BillSignInAction struct {
	Acted bool `json:"acted"`
	// Pressed is "button", "input", or "enter" when nothing on the page
	// matched.
	Pressed string `json:"pressed"`
	Words   string `json:"words"`
	// Waited says the control was present but not pressable at first.
	Waited  bool `json:"waited"`
	Changed bool `json:"changed"`
	// Dismissed is the cookie banner declined before typing: the consent
	// platform and the words on the control pressed.
	Dismissed string `json:"dismissed"`
}

type BillSignInFormFlags struct {
	Password    bool `json:"password"`
	Username    bool `json:"username"`
	OTP         bool `json:"otp"`
	SignOutLink bool `json:"sign_out_link"`
}

// The developer steers are owner-only routes, refused to an in-process call.
// What they may do (never leave the provider's site, never read a field's
// value) is decided in internal/browser/devtools.go.

type BillSignInSteer struct {
	Provider string                  `json:"provider"`
	URL      string                  `json:"url"`
	Title    string                  `json:"title"`
	Text     string                  `json:"text"`
	Requests []BillSignInRequestLine `json:"requests"`
	// Download is the file the steer started, named and then cancelled.
	Download *BillSignInDownload `json:"download"`
	// Opened is where a second tab the steer opened landed.
	Opened string `json:"opened"`
}

type BillSignInRequestLine struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Status int    `json:"status"`
	Type   string `json:"type"`
	// Authorization is only the scheme ("Bearer"), never the credential.
	Authorization string `json:"authorization,omitempty"`
	Body          string `json:"body,omitempty"`
	Response      string `json:"response,omitempty"`
}

type BillSignInDownload struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

type BillSignInDOM struct {
	Provider string                 `json:"provider"`
	URL      string                 `json:"url"`
	Count    int                    `json:"count"`
	Elements []BillSignInDOMElement `json:"elements"`
}

// BillSignInDOMElement is one match. Attributes never include `value` or
// anything named token, secret or password, and the markup has its values
// emptied.
type BillSignInDOMElement struct {
	Tag        string            `json:"tag"`
	Attributes map[string]string `json:"attributes"`
	Text       string            `json:"text"`
	HTML       string            `json:"html"`
}

// BillSignInFetchRequest is a call made by the signed-in page itself.
type BillSignInFetchRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers"`
	// Find searches a large answer (an app bundle) and returns each match with
	// Context characters after it instead of the whole body.
	Find    string `json:"find"`
	Context int    `json:"context"`
}

type BillSignInFetch struct {
	Provider    string   `json:"provider"`
	URL         string   `json:"url"`
	Status      int      `json:"status"`
	ContentType string   `json:"content_type"`
	Length      int      `json:"length"`
	Text        string   `json:"text,omitempty"`
	Matches     []string `json:"matches,omitempty"`
}

type BillSubaccountRef struct {
	ExternalID   string `json:"external_id"`
	Label        string `json:"label"`
	MaskedNumber string `json:"masked_number"`
}

type BillConnectComplete struct {
	Provider string `json:"provider"`
	// SessionState is opaque here and sealed row-bound by the caller.
	SessionState json.RawMessage     `json:"session_state"`
	Subaccounts  []BillSubaccountRef `json:"subaccounts"`
	// AccountHint is the name the provider greets the person by, for a login
	// nobody typed a username into.
	AccountHint string `json:"account_hint"`
}

// BillChallengeState is a sign-in a pull left parked; the session stays open
// in the engine, so an answer within its life resumes that pull.
type BillChallengeState struct {
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	Method    string `json:"method"`
	Prompt    string `json:"prompt"`
	Image     string `json:"image"`
}

type BillDocumentRef struct {
	Ref         string `json:"ref"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	Filename    string `json:"filename"`
}

// BillStatement is one pulled bill. Subaccount, DueOn and Invoice are what the
// ingest matches on; ExternalID is what known_documents names on the next pull.
type BillStatement struct {
	Subaccount string
	ExternalID string
	// Invoice tells apart bills of one billed account due the same day. Empty
	// for a provider whose cycle is its due date.
	Invoice   string
	IssuedOn  domain.Date
	DueOn     domain.Date
	AmountDue domain.Money
	// MinimumDue is a magnitude.
	MinimumDue    domain.Money
	HasMinimumDue bool
	Currency      string
	PeriodStart   domain.Date
	PeriodEnd     domain.Date
	AutopayOn     domain.Date
	Status        string
	StatementURL  string
	// Raw is kept for diagnosis. Never a credential.
	Raw      json.RawMessage
	Document *BillDocumentRef
}

// BillPull is what one pull answered: bills, a sign-in wall, or a challenge.
// Exactly one of the three is set.
type BillPull struct {
	NeedsSignIn bool
	// PasswordRefused says the provider refused the kept password outright.
	// Never set by a challenge, nor by a provider that could not be reached.
	PasswordRefused bool
	// CodeNeeded says the password got in but a second factor was asked for
	// with nobody there: not a refusal, but not to be retried unattended.
	CodeNeeded bool
	Reason     string
	// Image is base64.
	Image string
	// SessionState is the one to store: a provider that rotates its token on
	// every call makes the incoming session stale.
	SessionState json.RawMessage
	Bills        []BillStatement
	// Payments are what the provider lists as received, at a provider that
	// lists them (a medical portal); see domain.MatchBillPayments.
	Payments  []BillPayment
	Notes     []string
	Challenge *BillChallengeState
	// Trail is what the provider showed during the pull: the rounds of any
	// sign-in it did, and the pages its module read.
	Trail []BillTrailEntry
	// Subaccounts are the billed accounts as the pull found them, at a
	// provider whose module reads them anyway, for the labels on file.
	Subaccounts []BillSubaccountRef
}

// WirePayment is a payment a provider lists for one billed account, with an
// ISO-day date that CoercePayments parses. Amount is the magnitude received.
type WirePayment struct {
	Subaccount string
	ExternalID string
	PaidOn     string
	Amount     domain.Money
	// Method is how it was paid, in the provider's words.
	Method string
}

// BillPayment is one pulled payment. Subaccount and ExternalID are what the
// ingest matches on.
type BillPayment struct {
	Subaccount string
	ExternalID string
	PaidOn     domain.Date
	Amount     domain.Money
	Method     string
}

// CoercePayments turns what a module read into payments. A payment with no
// day, no key or no money received is a note rather than a row: it could
// never be paired with the bank row that carried it.
func CoercePayments(wire []WirePayment) ([]BillPayment, []string) {
	var payments []BillPayment
	var notes []string
	for _, one := range wire {
		paid, err := domain.ParseDate(one.PaidOn)
		switch {
		case err != nil || paid.IsZero():
			notes = append(notes, fmt.Sprintf(
				"a payment for %q was dropped: %q is not a date", one.Subaccount, one.PaidOn))
		case one.ExternalID == "" || !one.Amount.IsPositive():
			notes = append(notes, fmt.Sprintf(
				"a payment for %q on %s was dropped: it carried no key or no amount received", one.Subaccount, paid))
		default:
			payments = append(payments, BillPayment{
				Subaccount: one.Subaccount, ExternalID: one.ExternalID, PaidOn: paid,
				Amount: one.Amount, Method: one.Method,
			})
		}
	}
	return payments, notes
}

// BillKeepalive is what touching a kept session found. At an API provider the
// keepalive is the token refresh, so SessionState must be re-sealed.
type BillKeepalive struct {
	OK           bool            `json:"ok"`
	SignedIn     bool            `json:"signed_in"`
	SessionState json.RawMessage `json:"session_state"`
	Reason       string          `json:"reason"`
	Notes        []string        `json:"notes"`
}

// BillProfileRelease is what giving up a connection's browser gave up: the
// sessions this process held, and the lock file and Chromium singleton a dead
// process left on the profiles volume.
type BillProfileRelease struct {
	Sessions  []string `json:"sessions"`
	Lock      bool     `json:"lock"`
	Singleton bool     `json:"singleton"`
}

func (r BillProfileRelease) Released() bool {
	return len(r.Sessions) > 0 || r.Lock || r.Singleton
}

type BillConnectStart struct {
	Provider string
	Profile  string
	// Site is the deployment of a per-customer product, for a provider whose
	// catalogue entry says NeedsSite.
	Site     string
	Username string
	Password string
	Code     string
	// Secret is the authenticator setup key, so the engine can mint a code for
	// a page that asks again.
	Secret string
	// SecondFactor is "", "email", "sms" or "totp"; a factor-choice page takes
	// it, or the sign-in stops, when it is not "".
	SecondFactor string
}

type BillPullRequest struct {
	Provider string
	Profile  string
	// Site is as on BillConnectStart.
	Site         string
	SessionState string
	// Empty means everything the login bills.
	Subaccounts []string
	// KnownDocuments are fetched once rather than every night.
	KnownDocuments []string
	// Credential is what a provider with no kept session signs in with; nothing
	// stores it.
	Credential   map[string]string
	SecondFactor string
}

// CoerceBills turns what a module read into statements. Whatever could not be
// read becomes a note rather than a row: a reminder showing 0.00 because a
// selector moved is worse than one showing last cycle's figure.
func CoerceBills(wire []WireBill) ([]BillStatement, []string) {
	var bills []BillStatement
	var notes []string
	for _, one := range wire {
		bill, dropped, note := one.Statement()
		if dropped != "" {
			notes = append(notes, dropped)
			continue
		}
		if note != "" {
			notes = append(notes, note)
		}
		bills = append(bills, bill)
	}
	return bills, notes
}

// WireBill is a bill as a provider module states it, with ISO-day dates that
// Statement parses.
type WireBill struct {
	Subaccount string
	ExternalID string
	// Invoice is as on BillStatement.
	Invoice   string
	IssuedOn  string
	DueOn     string
	AmountDue domain.Money
	// MinimumDue is the least the provider will take, for a card or a loan.
	MinimumDue    domain.Money
	HasMinimumDue bool
	Currency      string
	PeriodStart   string
	PeriodEnd     string
	AutopayOn     string
	Status        string
	StatementURL  string
	Raw           json.RawMessage
	Document      *BillDocumentRef
}

// Statement coerces one bill, or says why it was dropped. A bill with no due
// date has no identity and is dropped. A negative amount means the account is
// in credit: it is stored as nothing owed and paid, never as its absolute
// value.
func (w WireBill) Statement() (BillStatement, string, string) {
	due, err := domain.ParseDate(w.DueOn)
	if err != nil || due.IsZero() {
		return BillStatement{}, fmt.Sprintf(
			"a bill for %q was dropped: %q is not a due date", w.Subaccount, w.DueOn), ""
	}
	amount, minimum, status, note := w.AmountDue, w.MinimumDue.Abs(), w.Status, ""
	if amount.IsNegative() {
		note = fmt.Sprintf("the bill due %s for %q is in credit by %s; it is stored as nothing owed",
			due, w.Subaccount, amount.Neg())
		amount, minimum, status = domain.Zero, domain.Zero, string(domain.BillPaid)
	}
	return BillStatement{
		Subaccount: w.Subaccount, ExternalID: w.ExternalID, Invoice: w.Invoice,
		IssuedOn: optionalDate(w.IssuedOn), DueOn: due,
		AmountDue: amount, MinimumDue: minimum, HasMinimumDue: w.HasMinimumDue,
		Currency:    w.Currency,
		PeriodStart: optionalDate(w.PeriodStart), PeriodEnd: optionalDate(w.PeriodEnd),
		AutopayOn: optionalDate(w.AutopayOn), Status: status,
		StatementURL: w.StatementURL, Raw: w.Raw, Document: w.Document,
	}, "", note
}

func optionalDate(value string) domain.Date {
	parsed, err := domain.ParseDate(value)
	if err != nil {
		return domain.Date{}
	}
	return parsed
}
