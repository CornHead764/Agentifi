// Package merchants is one module per merchant and the contract they share.
// internal/connector drives them; the connectors are described in
// docs/connectors/merchants.md.
package merchants

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The states a sign-in passes through (agent.State*). `email`, `password` and
// `factor` the loop answers itself and never reports.
const (
	StateEmail       = agent.StateEmail
	StatePassword    = agent.StatePassword
	StateFactor      = agent.StateFactor
	StateOTP         = agent.StateOTP
	StateCaptcha     = agent.StateCaptcha
	StateApproval    = agent.StateApproval
	StateSignedIn    = agent.StateSignedIn
	StateInteractive = agent.StateInteractive
	StateFailed      = agent.StateFailed
)

type State = agent.State

type FactorChoice = agent.FactorChoice

// Module is one merchant: its sign-in pages (agent.SignInModule, whose
// SignInURL is the purchases page a signed-out visitor is sent away from and
// back to) and its pull.
type Module interface {
	agent.SignInModule
	ID() domain.MerchantID
	// SessionKinds are the handed-over session shapes this merchant takes
	// besides a browser's storage state. A session of one of these opens no
	// browser at all.
	SessionKinds() []string
	// Attach listens from the first request for what the page never shows.
	Attach(page browser.Page)
	Fetch(call Call) (Result, error)
}

// PageSessionModule is a merchant whose signed-in page holds a session it can
// keep without a browser, which is kept in place of the page's storage state
// when a sign-in completes. Found false keeps the storage state.
type PageSessionModule interface {
	SessionFromPage(page browser.Page, at time.Time) (session json.RawMessage, found bool, err error)
}

// InvoiceBackfiller is a module whose invoice documents are the merchant's own
// printable pages, which it can reopen for orders a pull read long ago. It
// hands each order to each as it goes, a nil invoice being a page that gave
// nothing, and says why it stopped early and whether that was a sign-in.
type InvoiceBackfiller interface {
	BackfillInvoices(call Call, orders []string, each func(orderID string, printed *Invoice)) (stopped string, signIn bool)
}

type Call struct {
	Ctx context.Context
	// Page is the browser this pull opened, or nil for a handed-over session,
	// which makes its calls through HTTP instead.
	Page browser.Page
	// Session is the sealed session as it arrived: a browser's storage state,
	// or an object whose `kind` the module named.
	Session json.RawMessage
	// SinceDays is already clamped.
	SinceDays int
	// SkipDetails names the orders whose invoice has already been read in
	// full.
	SkipDetails map[string]bool
	// Invoiced names the orders whose invoice document is already on file.
	Invoiced map[string]bool
	// RefundChecks names orders on file, newest first, whose invoice is read
	// again for what was refunded: a return lands weeks after the order.
	RefundChecks []string
	Notes        *Notes
	HTTP         browser.Fetcher
	Now          agent.Clock
}

func (c Call) At() time.Time { return c.Now.At() }

func (c Call) context() context.Context {
	if c.Ctx != nil {
		return c.Ctx
	}
	return context.Background()
}

// Report tells the person watching the pull what it is doing now.
func (c Call) Report(format string, args ...any) {
	provider.ReportPull(c.context(), fmt.Sprintf(format, args...))
}

func (c Call) Since() string {
	return isoDay(c.At().AddDate(0, 0, -c.SinceDays))
}

func (c Call) Do(req *http.Request) (*http.Response, error) {
	if c.HTTP == nil {
		return nil, fmt.Errorf("merchants: this pull was given no way to make a call")
	}
	return c.HTTP.Do(req)
}

type Result struct {
	// NeedsSignIn is the session challenged; Reason is the merchant's own
	// words.
	NeedsSignIn bool
	Reason      string
	// Image is what the wall looked like, base64 PNG.
	Image       string
	AccountHint string
	// Parsed is what the pull read, as merchantimport reads the same file
	// uploaded by hand; nil when it found nothing to import.
	Parsed *merchantimport.Parsed
	// StorageState is set by a module that rotates its own session. A browser
	// pull leaves it empty and the engine takes the jar out of the context.
	StorageState json.RawMessage
	Orders       int
	Charges      int
	Invoices     []Invoice
}

// Invoice is one order's paperwork: a PDF the merchant's own page printed, or
// HTML the module made from what the merchant answered, which the engine
// prints. The HTML is printed with scripts off and every request refused, so
// it must carry everything it shows.
type Invoice struct {
	OrderID  string
	Filename string
	PDF      []byte
	HTML     string
}

// Notes is what a module tells the household about a pull that half-worked.
type Notes = agent.Notes

type Registry struct {
	modules map[domain.MerchantID]Module
}

func NewRegistry() *Registry { return NewWith(Amazon(), Costco()) }

// NewWith is a registry of the given modules only.
func NewWith(modules ...Module) *Registry {
	out := &Registry{modules: map[domain.MerchantID]Module{}}
	for _, module := range modules {
		out.modules[module.ID()] = module
	}
	return out
}

// Pick defaults to Amazon when the caller names none, for a `/sign-in` body
// that carries no merchant field.
func (r *Registry) Pick(id domain.MerchantID) (Module, error) {
	if id == "" {
		id = domain.MerchantAmazon
	}
	module, known := r.modules[id]
	if !known {
		return nil, fmt.Errorf("unknown merchant %q", id)
	}
	return module, nil
}

// Name is the catalogue's name for the merchant, or the bare id for a module
// in a test.
func Name(module Module) string {
	if merchant, known := domain.MerchantByID(module.ID()); known {
		return merchant.Name
	}
	return string(module.ID())
}
