// Package provider is everything outside the process: the bank aggregator,
// price and valuation feeds, exchange rates, attachment storage and web push.
// Upstream values are normalized into domain types once, here.
package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// NormalizeBalance makes a debt account's balance negative. It runs once, on
// ingest (see docs/calculations.md §3), and again whenever
// a user re-classifies an account, since the kind decides the sign.
func NormalizeBalance(kind domain.AccountKind, reported domain.Money) domain.Money {
	if kind.IsDebt() {
		return reported.Abs().Neg()
	}
	return reported
}

// Account is one account at one bank, as a sync reads it.
type Account struct {
	ExternalID string
	Name       string
	Kind       domain.AccountKind
	// Type is the picker label the feed's words name, for an account created
	// from this one; empty leaves it to the kind.
	Type string
	// Balance is already sign-normalized by NormalizeBalance.
	Balance domain.Money
	// HasBalance is false when the feed reported no usable balance, so a good
	// stored balance is never overwritten with a figure the feed did not give.
	HasBalance bool
	Currency   string

	CreditLimit    domain.Money
	HasCreditLimit bool

	StatementCloseDay int
	PaymentDueDay     int

	MinimumPayment    domain.Money
	HasMinimumPayment bool

	// Amounts owed are magnitudes, not signed ledger positions, whatever sign
	// the connector used.
	StatementBalance    domain.Money
	HasStatementBalance bool
	// PaymentDueOn is a specific day; PaymentDueDay is the recurring day of
	// the month, a different fact.
	PaymentDueOn    domain.Date
	InterestRate    domain.Rate
	HasInterestRate bool

	// Extra is everything the feed sent that no field above names, stored
	// whole.
	Extra map[string]any

	// MaskedNumber is the last four characters of the bank's identifier; the
	// full identifier is a payable bank reference and is never stored.
	MaskedNumber string

	// One SimpleFIN Access URL spans every bank the user linked, so the
	// institution lives on the account, not the connection.
	InstitutionName    string
	InstitutionLogoURL string
}

// Transaction is one row from a bank feed.
type Transaction struct {
	ExternalID string

	// StatementName is the bank's immutable wording, read by matching; Payee
	// is the editable display name.
	StatementName string
	Payee         string
	Memo          string

	// Money out is negative.
	Amount domain.Money
	// Date is when the charge posted; TransactedOn is when it happened, where
	// the feed says so separately.
	Date         domain.Date
	TransactedOn domain.Date

	// Currency is empty, meaning unknown rather than USD, when the feed's value
	// is not an ISO 4217 code; the sync layer falls back to the account's, then
	// the space's.
	Currency string

	Pending bool

	// BillExternalID lets a card charge's effective_date follow its billing
	// cycle rather than its posting date.
	BillExternalID string

	// Extra is everything the feed sent that no field above names, stored
	// whole: a re-sync only reaches back as far as the connector's window.
	Extra map[string]any
}

type Connection struct {
	ExternalID      string
	InstitutionName string
	Credentials     Credentials
	Accounts        []Account
	LogoURL         string
	// Warnings never fail the connect.
	Warnings []BankWarning
}

// Credentials is held in the clear: encryption at rest belongs to the
// persistence layer, so only one package can decrypt.
type Credentials struct {
	AccessURL string
	ClaimedAt time.Time
}

// BankWarning is one bank inside a connection that needs re-authorization
// while the connection's own credential still works.
type BankWarning struct {
	Institution string
	Message     string
}

// Bill is a normalized credit-card statement; anything feed-specific stays in
// Raw.
type Bill struct {
	ExternalID  string
	DueDate     domain.Date
	TotalAmount domain.Money
	Currency    string

	MinimumPayment    domain.Money
	HasMinimumPayment bool

	Raw map[string]any
}

// Holding is a normalized investment position. An optional figure is zero
// when not supplied, and a reported zero means the same: brokerages send a 0
// cost basis for retirement accounts.
type Holding struct {
	ExternalID   string
	Name         string
	Currency     string
	CurrentValue domain.Money

	Quantity  domain.Rate
	UnitPrice domain.Rate

	// CostBasis is the whole position's cost; AveragePrice is per unit.
	CostBasis    domain.Money
	AveragePrice domain.Rate

	PurchaseDate domain.Date

	// AccountExternalID is resolved at sync time so an account's balance and
	// its positions are not both counted.
	AccountExternalID string

	ISIN     string
	Ticker   string
	Metadata map[string]any
}

// PayeeSource selects whether a feed's own payee field is trusted.
type PayeeSource string

const (
	PayeeFromProvider PayeeSource = "auto"
	// PayeeNone leaves the payee blank so rules derive it from the statement
	// name instead.
	PayeeNone PayeeSource = "none"
)

type TransactionQuery struct {
	// A zero Since means as far back as the provider goes.
	Since       domain.Date
	PayeeSource PayeeSource
}

// ErrCredentialsExpired puts a reconnect banner in front of the user, so
// nothing transient may be reported as this.
var ErrCredentialsExpired = errors.New("provider: credentials expired")

// ErrRateLimited is transient: callers skip this run rather than flagging the
// connection broken.
var ErrRateLimited = errors.New("provider: rate limited")

// CredentialsExpiredError matches ErrCredentialsExpired with errors.Is and
// carries the page the user must visit to fix it.
type CredentialsExpiredError struct {
	Reason  string
	HelpURL string
}

func (e *CredentialsExpiredError) Error() string { return e.Reason }

func (e *CredentialsExpiredError) Unwrap() error { return ErrCredentialsExpired }

func credentialsExpired(helpURL, format string, args ...any) error {
	return &CredentialsExpiredError{Reason: fmt.Sprintf(format, args...), HelpURL: helpURL}
}

// BankProvider is a read-only bank aggregator. A provider without bills or
// holdings returns nil, nil rather than an error, which would read as a failed
// request.
type BankProvider interface {
	Name() string

	Connect(ctx context.Context, setupToken string) (*Connection, error)

	Accounts(ctx context.Context, creds Credentials) ([]Account, error)

	Transactions(ctx context.Context, creds Credentials, accountExternalID string, q TransactionQuery) ([]Transaction, error)

	Holdings(ctx context.Context, creds Credentials) ([]Holding, error)

	Bills(ctx context.Context, creds Credentials, accountExternalID string) ([]Bill, error)

	// InstitutionLogo backfills a logo for a connection stored without one.
	InstitutionLogo(ctx context.Context, creds Credentials) (string, error)
}

var (
	_ BankProvider           = (*SimpleFin)(nil)
	_ MarketPriceProvider    = (*YahooProvider)(nil)
	_ MarketNewsProvider     = (*YahooProvider)(nil)
	_ MarketNewsProvider     = (*CachedNews)(nil)
	_ AssetValuationProvider = (*ZillowProperty)(nil)
	_ AssetValuationProvider = (*KelleyBlueBook)(nil)
	_ FxRateProvider         = (*OpenExchangeRates)(nil)
	_ StorageProvider        = (*LocalStorage)(nil)
)
