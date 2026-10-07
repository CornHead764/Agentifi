package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
)

// Shapes internal/connector answers a merchant in, kept here so service/ and api/ can
// read them without importing the engine.

// ErrMerchantAgentUnavailable is a build with no merchant modules registered.
var ErrMerchantAgentUnavailable = errors.New("provider: this build has no merchant engine")

// Every sign-in state but signed_in and failed is a question for the person.
const (
	MerchantSignInSignedIn = "signed_in"
	MerchantSignInOTP      = "otp"
	MerchantSignInCaptcha  = "captcha"
	MerchantSignInApproval = "approval"
	MerchantSignInFailed   = "failed"
	// MerchantSignInInteractive is a live browser the person drives themself.
	MerchantSignInInteractive = "interactive"
)

type MerchantSignInState struct {
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	// Prompt is the merchant's own wording.
	Prompt string `json:"prompt"`
	// Image is a base64 PNG.
	Image string `json:"image"`
	Error string `json:"error"`
}

type MerchantFetchResult struct {
	NeedsSignIn bool   `json:"needs_sign_in"`
	Reason      string `json:"reason"`
	AccountHint string `json:"account_hint"`
	// StorageState must be stored after every pull: cookies roll.
	StorageState json.RawMessage `json:"storage_state"`
	// Parsed is nil when there was nothing to import.
	Parsed  *merchantimport.Parsed `json:"-"`
	Orders  int                    `json:"orders"`
	Charges int                    `json:"charges"`
	Image   string                 `json:"image"`
	Notes   []string               `json:"notes"`
	// Paused is a MerchantPause* value when a password sign-in stopped at
	// something only a person can fix; an unreachable merchant is an error.
	Paused string `json:"paused"`
	// Invoices are the orders' invoice PDFs the pull fetched.
	Invoices []MerchantInvoice `json:"-"`
}

type MerchantInvoice struct {
	OrderNumber string
	Filename    string
	PDF         []byte
}

// MerchantBackfillResult is how a walk through older orders' invoice pages
// ended; each invoice was handed over as it was printed.
type MerchantBackfillResult struct {
	// StorageState is the jar to keep, or nil when the walk stopped at a
	// sign-in and the session it held is no good.
	StorageState json.RawMessage
	// Stopped is why it ended before the last order; "" is every order tried.
	Stopped string
	// NeedsSignIn is a stop at a sign-in page or a challenge.
	NeedsSignIn bool
	Notes       []string
}

// MerchantReceipt is a stored purchase to lay out as its receipt document:
// amounts as the purchase recorded them, tenders positive.
type MerchantReceipt struct {
	Merchant domain.MerchantID
	Number   string
	Location string
	Date     string
	Return   bool
	Items    []MerchantReceiptLine
	Tax      string
	Total    string
	Tenders  []MerchantReceiptLine
}

type MerchantReceiptLine struct {
	Number   string
	Title    string
	Quantity int
	Amount   string
}

// MerchantCredential is never logged, noted or answered.
type MerchantCredential struct {
	Email    string
	Password string
	// TOTPSecret mints a code only when the merchant asks.
	TOTPSecret string
	// MailedCode waits for a mailed code sent after since; nil when no mailbox
	// can.
	MailedCode func(ctx context.Context, since time.Time) (string, bool)
	// SecondFactor answers a code box that does not say where its code went.
	SecondFactor domain.SecondFactor
}

func (c *MerchantCredential) Usable() bool {
	return c != nil && c.Email != "" && c.Password != ""
}

// String, GoString and LogValue print only which fields are set.
func (c MerchantCredential) String() string {
	return fmt.Sprintf("provider.MerchantCredential{Email:%s Password:%s TOTPSecret:%s}",
		setOrNot(c.Email), setOrNot(c.Password), setOrNot(c.TOTPSecret))
}

func (c MerchantCredential) GoString() string { return c.String() }

func (c MerchantCredential) LogValue() slog.Value { return slog.StringValue(c.String()) }

func setOrNot(value string) string {
	if value == "" {
		return "unset"
	}
	return "set"
}

// Engine refusal kinds; the API maps each to a status.
var (
	ErrAgentBadRequest = errors.New("provider: the engine refused the request")
	ErrAgentNotFound   = errors.New("provider: the engine holds no such session")
	ErrAgentConflict   = errors.New("provider: the engine cannot do that now")
	ErrAgentUpstream   = errors.New("provider: the provider failed the engine")
	ErrAgentFailed     = errors.New("provider: the engine failed")
)

type AgentError struct {
	Kind    error
	Message string
}

func (e *AgentError) Error() string { return e.Message }

func (e *AgentError) Unwrap() error { return e.Kind }

// PageFailure is a connector's error with the page it failed on: a PNG of the
// browser's viewport, every typed field masked. The error's text and kind are
// Err's.
type PageFailure struct {
	Err        error
	Screenshot []byte
}

func (e *PageFailure) Error() string { return e.Err.Error() }

func (e *PageFailure) Unwrap() error { return e.Err }

// ScreenshotOf is the page err failed on, nil when it carries none.
func ScreenshotOf(err error) []byte {
	var failure *PageFailure
	if errors.As(err, &failure) {
		return failure.Screenshot
	}
	return nil
}

func AgentMessage(err error) string {
	var refusal *AgentError
	if errors.As(err, &refusal) {
		return refusal.Message
	}
	if err == nil {
		return ""
	}
	return err.Error()
}
