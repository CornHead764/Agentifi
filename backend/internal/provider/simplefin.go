package provider

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
	"github.com/shopspring/decimal"
)

// Every SimpleFIN protocol shape (setup token, claim, accounts payload,
// errlist) lives in this file; nothing outside it sees a SimpleFIN field name.
const (
	// The Bridge accepts ninety days but warns on anything wider than 45 that
	// it may start capping there.
	simplefinMaxWindowDays      = 45
	simplefinInitialHistoryDays = 365
	// Long enough for the Bridge to fan a request out to every linked bank.
	simplefinHTTPTimeout = 60 * time.Second
)

const (
	simplefinHelpURL     = "https://bridge.simplefin.org/"
	simplefinInstitution = "SimpleFIN"
	simplefinUserAgent   = "Agentifi (+https://github.com/CornHead764/agentifi)"
	// A claim answers with one URL; anything much larger is not an answer.
	simplefinMaxClaimBody = 1 << 20
	// Years of history across every account of a large household.
	simplefinMaxAnswer = 64 << 20
)

// SimpleFin is the SimpleFIN Bridge connector. It keeps no per-read state, so
// one connection's failures cannot appear in another's report.
type SimpleFin struct {
	// nil means a client that connects only to public addresses.
	HTTPClient *http.Client
	// AllowPrivate permits plain HTTP and non-public addresses; tests only.
	// The setup token is a URL chosen by whoever adds a connection, and the
	// server must not be made to call into its own network with it.
	AllowPrivate bool
	Now          func() time.Time
}

func (s *SimpleFin) Name() string { return "simplefin" }

func (s *SimpleFin) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	if s.AllowPrivate {
		return &http.Client{Timeout: simplefinHTTPTimeout}
	}
	return &http.Client{
		Timeout: simplefinHTTPTimeout,
		// No proxy: the address check is on the address actually dialled.
		Transport: &http.Transport{DialContext: pinnedDial(nil, ErrSimpleFINAddressRefused)},
	}
}

var ErrSimpleFINAddressRefused = errors.New("simplefin: that address is not a public https address")

func (s *SimpleFin) checkAddress(raw string) error {
	if s.AllowPrivate {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return ErrSimpleFINAddressRefused
	}
	return nil
}

func (s *SimpleFin) today() domain.Date {
	if s.Now != nil {
		return domain.DateOf(s.Now().UTC())
	}
	return domain.DateOf(time.Now().UTC())
}

func (s *SimpleFin) logoFor(website string) string {
	return FaviconURL(website)
}

// Connect exchanges a setup token for an access URL. The claim URL's path is
// the token's secret, so it never reaches an error string, and the claim's
// answer is never echoed back.
func (s *SimpleFin) Connect(ctx context.Context, setupToken string) (*Connection, error) {
	claimURL, err := decodeSetupToken(setupToken)
	if err != nil {
		return nil, err
	}
	if err := s.checkAddress(claimURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claimURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("simplefin: the setup token does not hold a usable address")
	}
	// http.NoBody with a zero length is sent as an explicit Content-Length: 0.
	req.ContentLength = 0
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", simplefinUserAgent)

	host := urlHost(claimURL)
	resp, err := httpx.Read(s.client(), req, simplefinMaxClaimBody)
	if err != nil {
		return nil, fmt.Errorf("simplefin: could not reach the claim server at %s: %w",
			host, redactedError(err, claimURL, host))
	}

	if resp.Status == http.StatusForbidden {
		return nil, credentialsExpired(simplefinHelpURL,
			"simplefin: this setup token has already been used or has expired; "+
				"generate a fresh token from the SimpleFIN Bridge and connect again")
	}
	if resp.Status >= 400 {
		return nil, fmt.Errorf("simplefin: the claim server at %s answered with status %d", host, resp.Status)
	}

	accessURL := strings.Trim(strings.TrimSpace(string(resp.Body)), `"`)
	if !strings.HasPrefix(accessURL, "http://") && !strings.HasPrefix(accessURL, "https://") {
		return nil, fmt.Errorf("simplefin: the claim server did not answer with an access URL")
	}
	if err := s.checkAddress(accessURL); err != nil {
		return nil, err
	}

	creds := Credentials{AccessURL: accessURL, ClaimedAt: time.Now().UTC()}
	payload, err := s.fetchAccounts(ctx, creds, accountsQuery{})
	if err != nil {
		return nil, err
	}
	connAuth, err := surfaceErrors(payload.Errlist, "claim")
	if err != nil {
		return nil, err
	}

	return &Connection{
		ExternalID:      stableExternalID(payload, claimURL),
		InstitutionName: simplefinInstitution,
		Credentials:     creds,
		Accounts:        s.parseAccounts(payload),
		LogoURL:         s.logoFor(accessURL),
		Warnings:        s.warningsFor(connAuth, payload),
	}, nil
}

func (s *SimpleFin) Accounts(ctx context.Context, creds Credentials) ([]Account, error) {
	accounts, _, err := s.AccountsWithWarnings(ctx, creds)
	return accounts, err
}

// AccountsWithWarnings is Accounts plus the banks the Bridge says need
// re-authorization; those are not failures, as the other banks still sync.
func (s *SimpleFin) AccountsWithWarnings(ctx context.Context, creds Credentials) ([]Account, []BankWarning, error) {
	payload, err := s.fetchAccounts(ctx, creds, accountsQuery{})
	if err != nil {
		return nil, nil, err
	}
	connAuth, err := surfaceErrors(payload.Errlist, "accounts")
	if err != nil {
		return nil, nil, err
	}
	return s.parseAccounts(payload), s.warningsFor(connAuth, payload), nil
}

// Transactions reads one account's history from q.Since through today in
// Bridge-sized windows. A row the Bridge repeats in two windows is returned
// once.
func (s *SimpleFin) Transactions(ctx context.Context, creds Credentials, accountExternalID string, q TransactionQuery) ([]Transaction, error) {
	today := s.today()
	first := q.Since
	if first.IsZero() {
		first = today.AddDays(-simplefinInitialHistoryDays)
	}
	// An empty read would record a successful sync that found nothing.
	if first.After(today) {
		return nil, fmt.Errorf("simplefin: the requested start day %s is after today (%s)", first, today)
	}

	var out []Transaction
	seen := map[string]bool{}
	for start := first; !start.After(today); start = start.AddDays(simplefinMaxWindowDays) {
		last := start.AddDays(simplefinMaxWindowDays - 1)
		if last.After(today) {
			last = today
		}
		payload, err := s.fetchAccounts(ctx, creds, accountsQuery{
			start:     start,
			end:       last.AddDays(1),
			accountID: accountExternalID,
			pending:   true,
		})
		if err != nil {
			return nil, err
		}
		if _, err := surfaceErrors(payload.Errlist, "transactions"); err != nil {
			return nil, err
		}
		for _, acc := range payload.Accounts {
			if acc.ID.String() != accountExternalID {
				continue
			}
			for _, raw := range acc.Transactions {
				txn, ok := buildTransaction(raw, q.PayeeSource)
				if !ok || seen[txn.ExternalID] {
					continue
				}
				seen[txn.ExternalID] = true
				out = append(out, txn)
			}
		}
	}
	return out, nil
}

func (s *SimpleFin) Holdings(ctx context.Context, creds Credentials) ([]Holding, error) {
	payload, err := s.fetchAccounts(ctx, creds, accountsQuery{})
	if err != nil {
		return nil, err
	}
	if _, err := surfaceErrors(payload.Errlist, "holdings"); err != nil {
		return nil, err
	}

	var out []Holding
	for _, acc := range payload.Accounts {
		accCurrency := acc.Currency
		if accCurrency == "" {
			accCurrency = "USD"
		}
		for _, raw := range acc.Holdings {
			id := raw.ID.String()
			if id == "" {
				continue
			}
			marketValue, ok := raw.MarketValue.money()
			if !ok {
				continue
			}
			shares, _ := raw.Shares.rate()
			costBasis, averagePrice := costFigures(raw, shares)

			unitPrice := decimal.Zero
			if !shares.IsZero() {
				unitPrice = marketValue.Decimal().Div(shares)
			}

			name := raw.Description
			if name == "" {
				name = raw.Symbol
			}
			if name == "" {
				name = id
			}

			out = append(out, Holding{
				ExternalID:        id,
				Name:              name,
				Currency:          isoCurrency(raw.Currency, accCurrency),
				Ticker:            normalizeTicker(raw.Symbol),
				CurrentValue:      marketValue,
				Quantity:          shares,
				UnitPrice:         unitPrice,
				CostBasis:         costBasis,
				AveragePrice:      averagePrice,
				PurchaseDate:      raw.Created.date(),
				AccountExternalID: acc.ID.String(),
				ISIN:              raw.ISIN,
				Metadata: map[string]any{
					"symbol":     raw.Symbol,
					"cost_basis": raw.CostBasis.text,
				},
			})
		}
	}
	return out, nil
}

// Bills is always empty: SimpleFIN has no statement feed, so a card charge's
// effective date comes from the locally computed billing cycle instead.
func (s *SimpleFin) Bills(context.Context, Credentials, string) ([]Bill, error) {
	return nil, nil
}

func (s *SimpleFin) InstitutionLogo(_ context.Context, creds Credentials) (string, error) {
	return s.logoFor(creds.AccessURL), nil
}

// The zero value asks for every account with no transaction window.
type accountsQuery struct {
	start domain.Date
	// exclusive, as the Bridge reads it.
	end       domain.Date
	accountID string
	pending   bool
}

func (s *SimpleFin) fetchAccounts(ctx context.Context, creds Credentials, q accountsQuery) (*sfPayload, error) {
	if strings.TrimSpace(creds.AccessURL) == "" {
		return nil, credentialsExpired(simplefinHelpURL,
			"simplefin: this connection has no access URL; connect it again with a new setup token")
	}
	endpoint, username, password, err := accountsURLAndAuth(creds.AccessURL)
	if err != nil {
		return nil, err
	}

	params := url.Values{"version": {"2"}}
	if q.pending {
		params.Set("pending", "1")
	}
	if !q.start.IsZero() {
		params.Set("start-date", strconv.FormatInt(q.start.Time().Unix(), 10))
	}
	if !q.end.IsZero() {
		params.Set("end-date", strconv.FormatInt(q.end.Time().Unix(), 10))
	}
	if q.accountID != "" {
		params.Set("account", q.accountID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("simplefin: the access URL is not a usable address")
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", simplefinUserAgent)

	resp, err := httpx.Read(s.client(), req, simplefinMaxAnswer)
	if err != nil {
		return nil, fmt.Errorf("simplefin: could not reach the SimpleFIN server: %w",
			redactedError(err, creds.AccessURL))
	}

	switch {
	case resp.Refused():
		return nil, credentialsExpired(simplefinHelpURL,
			"simplefin: the SimpleFIN Bridge no longer accepts this connection; "+
				"generate a new setup token there and connect again")
	case resp.Status == http.StatusTooManyRequests:
		return nil, fmt.Errorf("simplefin: the SimpleFIN Bridge is limiting requests; the next sync will try again: %w",
			ErrRateLimited)
	case resp.Status >= 400:
		return nil, fmt.Errorf("simplefin: the SimpleFIN server answered: %w", resp.Err())
	}

	var payload sfPayload
	if err := httpx.DecodeJSON(resp.Body, &payload); err != nil {
		return nil, fmt.Errorf("simplefin: the SimpleFIN server's answer was not readable: %w", err)
	}
	return &payload, nil
}

type sfPayload struct {
	Errlist     []sfError      `json:"errlist"`
	Accounts    []sfAccount    `json:"accounts"`
	Connections []sfConnection `json:"connections"`
}

// Servers disagree on the message key, so both are read.
type sfError struct {
	Code    string   `json:"code"`
	Msg     string   `json:"msg"`
	Message string   `json:"message"`
	ConnID  sfString `json:"conn_id"`
}

func (e sfError) text() string {
	if e.Msg != "" {
		return e.Msg
	}
	return e.Message
}

type sfOrg struct {
	Name    string `json:"name"`
	Domain  string `json:"domain"`
	URL     string `json:"url"`
	SfinURL string `json:"sfin-url"`
}

type sfConnection struct {
	ConnID  sfString `json:"conn_id"`
	Name    string   `json:"name"`
	OrgURL  string   `json:"org_url"`
	URL     string   `json:"url"`
	SfinURL string   `json:"sfin_url"`
}

type sfAccount struct {
	ID           sfString        `json:"id"`
	Name         string          `json:"name"`
	Currency     string          `json:"currency"`
	Balance      sfNumber        `json:"balance"`
	ConnID       sfString        `json:"conn_id"`
	Org          sfOrg           `json:"org"`
	Transactions []sfTransaction `json:"transactions"`
	Holdings     []sfHolding     `json:"holdings"`
	// The only place any connector reports what kind of account this is.
	Extra map[string]any `json:"extra"`

	// Raw is the account as it arrived, so an unnamed field can be read later
	// without a re-sync.
	Raw map[string]any `json:"-"`
}

func (a *sfAccount) UnmarshalJSON(data []byte) error {
	type plain sfAccount
	var fields plain
	if err := httpx.DecodeJSON(data, &fields); err != nil {
		return err
	}
	*a = sfAccount(fields)
	return httpx.DecodeJSON(data, &a.Raw)
}

type sfTransaction struct {
	ID           sfString `json:"id"`
	Posted       sfNumber `json:"posted"`
	TransactedAt sfNumber `json:"transacted_at"`
	Amount       sfNumber `json:"amount"`
	Description  string   `json:"description"`
	Payee        string   `json:"payee"`
	Memo         string   `json:"memo"`
	Currency     string   `json:"currency"`
	Pending      bool     `json:"pending"`

	Raw map[string]any `json:"-"`
}

func (t *sfTransaction) UnmarshalJSON(data []byte) error {
	type plain sfTransaction
	var fields plain
	if err := httpx.DecodeJSON(data, &fields); err != nil {
		return err
	}
	*t = sfTransaction(fields)
	return httpx.DecodeJSON(data, &t.Raw)
}

type sfHolding struct {
	ID            sfString `json:"id"`
	Created       sfNumber `json:"created"`
	CostBasis     sfNumber `json:"cost_basis"`
	PurchasePrice sfNumber `json:"purchase_price"`
	MarketValue   sfNumber `json:"market_value"`
	Shares        sfNumber `json:"shares"`
	Currency      string   `json:"currency"`
	Description   string   `json:"description"`
	Symbol        string   `json:"symbol"`
	ISIN          string   `json:"isin"`
}

// sfString accepts a string or a number: ids arrive both ways depending on the
// connector.
type sfString struct{ value string }

func (s *sfString) UnmarshalJSON(data []byte) error {
	text := string(bytes.TrimSpace(data))
	if text == "null" {
		s.value = ""
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var unquoted string
		if err := json.Unmarshal(data, &unquoted); err != nil {
			return err
		}
		s.value = unquoted
		return nil
	}
	s.value = text
	return nil
}

func (s sfString) String() string { return s.value }

// sfNumber is a numeric JSON field held as its literal text, so it becomes a
// decimal without passing through float64. The spec says amounts are strings;
// connectors send both.
type sfNumber struct {
	text   string
	quoted bool
}

func (n *sfNumber) UnmarshalJSON(data []byte) error {
	text := string(bytes.TrimSpace(data))
	if text == "null" {
		n.text = ""
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var unquoted string
		if err := json.Unmarshal(data, &unquoted); err != nil {
			return err
		}
		n.text, n.quoted = strings.TrimSpace(unquoted), true
		return nil
	}
	n.text, n.quoted = text, false
	return nil
}

func (n sfNumber) rate() (domain.Rate, bool) {
	if n.text == "" {
		return decimal.Zero, false
	}
	d, err := decimal.NewFromString(n.text)
	if err != nil {
		return decimal.Zero, false
	}
	return d, true
}

func (n sfNumber) money() (domain.Money, bool) {
	if n.text == "" {
		return domain.Zero, false
	}
	var value any = json.Number(n.text)
	if n.quoted {
		value = n.text
	}
	amount, err := domain.MoneyFromJSONValue(value)
	return amount, err == nil
}

// date reads epoch seconds as a UTC calendar day. Connectors send 0 for "not
// posted", so zero and below are unset rather than 1970-01-01.
func (n sfNumber) date() domain.Date {
	seconds, ok := n.rate()
	if !ok {
		return domain.Date{}
	}
	whole := seconds.Truncate(0)
	if !whole.IsPositive() {
		return domain.Date{}
	}
	return domain.DateOf(time.Unix(whole.IntPart(), 0).UTC())
}

func (s *SimpleFin) parseAccounts(payload *sfPayload) []Account {
	connections := connectionsByID(payload)
	var out []Account
	for _, raw := range payload.Accounts {
		id := raw.ID.String()
		if id == "" {
			continue
		}
		name := raw.Name
		if name == "" {
			name = "Account"
		}
		kind := accountKind(raw)
		accountType := typeFromWords(raw, kind)
		balance, hasBalance := raw.Balance.money()
		institution, logo := s.institution(raw, connections)
		terms := readCardTerms(raw)
		out = append(out, Account{
			ExternalID:          id,
			Name:                name,
			Kind:                kind,
			Type:                accountType,
			Balance:             NormalizeBalance(kind, balance),
			HasBalance:          hasBalance,
			Currency:            isoCurrency(raw.Currency, "USD"),
			InstitutionName:     institution,
			InstitutionLogoURL:  logo,
			CreditLimit:         terms.creditLimit,
			HasCreditLimit:      terms.hasCreditLimit,
			StatementBalance:    terms.statementBalance,
			HasStatementBalance: terms.hasStatementBalance,
			MinimumPayment:      terms.minimumPayment,
			HasMinimumPayment:   terms.hasMinimumPayment,
			PaymentDueOn:        terms.paymentDueOn,
			InterestRate:        terms.interestRate,
			HasInterestRate:     terms.hasInterestRate,
			Extra:               unmappedAccountFields(raw.Raw),
		})
	}
	return out
}

// accountKind classifies an account so its balance can be signed correctly.
// The spec has no account type; a connector may declare one in "extra",
// otherwise the name is guessed from, failing toward cash. The sync layer keeps
// a user-set kind over this.
func accountKind(raw sfAccount) domain.AccountKind {
	// A declared type is believed even when unrecognised, and the name is then
	// not read: "Mortgage Payoff Savings" is a savings account.
	guessable := raw.Name
	if declared, ok := declaredType(raw); ok {
		if kind, found := kindFromWords(declared); found {
			return kind
		}
		guessable = ""
	}
	// Holdings are proof, so they outrank the name.
	if len(raw.Holdings) > 0 {
		return domain.KindInvestment
	}
	if kind, found := kindFromWords(guessable); found {
		return kind
	}
	return domain.KindCash
}

// typeFromWords is the picker label an account's words name, read from the
// same text accountKind read; empty leaves the label to the kind. A wallet is
// only crypto once something else made it an investment: most wallets hold
// dollars. An HSA is whichever side its kind says: the cash the debit card
// draws on, or the brokerage that cash is swept into.
func typeFromWords(raw sfAccount, kind domain.AccountKind) string {
	text := raw.Name
	if declared, ok := declaredType(raw); ok {
		text = declared
	}
	words := normalizedWords(text)
	hsa := textutil.HasWord(words, "hsa", textutil.WordOptions{})
	if kind == domain.KindCash && hsa {
		return domain.AccountTypeHSA
	}
	if kind != domain.KindInvestment {
		return ""
	}
	switch {
	case hsa:
		return domain.AccountTypeHSAInvestment
	case textutil.ContainsAnyFold(words, lifeInsuranceWords):
		return "life_insurance"
	case textutil.ContainsAnyFold(words, cryptoWords),
		textutil.HasWord(words, "wallet", textutil.WordOptions{}),
		textutil.HasWord(words, "btc", textutil.WordOptions{}),
		textutil.HasWord(words, "eth", textutil.WordOptions{}):
		return "crypto"
	}
	return ""
}

func declaredType(raw sfAccount) (string, bool) {
	for _, key := range []string{"type", "account-type", "account_type", "subtype"} {
		text, ok := raw.Extra[key].(string)
		if ok && strings.TrimSpace(text) != "" {
			return text, true
		}
	}
	return "", false
}

// Everything not listed goes to Extra whole, including the spec's nested
// `extra` object.
var mappedAccountFields = map[string]bool{
	"id": true, "name": true, "currency": true, "balance": true,
	"conn_id": true, "org": true, "transactions": true, "holdings": true,
}

// unmappedAccountFields returns nil rather than an empty map so the column
// holds NULL rather than `{}`.
func unmappedAccountFields(raw map[string]any) map[string]any {
	var out map[string]any
	for key, value := range raw {
		if mappedAccountFields[key] {
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		out[key] = value
	}
	return out
}

// cardTerms is what a connector reports about a card's statement. None of
// these are in the spec, so they are read by alias, and a figure not reported
// is absent, not zero.
type cardTerms struct {
	creditLimit         domain.Money
	hasCreditLimit      bool
	statementBalance    domain.Money
	hasStatementBalance bool
	minimumPayment      domain.Money
	hasMinimumPayment   bool
	paymentDueOn        domain.Date
	interestRate        domain.Rate
	hasInterestRate     bool
}

// Exact aliases, not substring matching: "available-credit" contains "credit"
// and is not a limit. Written in normalizeFieldKey's form.
var (
	creditLimitKeys = []string{
		"creditlimit", "creditline", "totalcreditline", "limit",
	}
	statementBalanceKeys = []string{
		"statementbalance", "laststatementbalance", "closingbalance",
		"statementclosingbalance",
	}
	minimumPaymentKeys = []string{
		"minimumpayment", "minimumpaymentamount", "minimumpaymentdue",
		"minpayment", "minpaymentdue", "minimumdue",
	}
	paymentDueKeys = []string{
		"paymentduedate", "nextpaymentduedate", "paymentdueat",
		"duedate", "nextduedate", "dueat",
	}
	interestRateKeys = []string{
		"interestrate", "apr", "purchaseapr", "annualpercentagerate",
	}
)

func readCardTerms(raw sfAccount) cardTerms {
	fields := accountFields(raw)
	var terms cardTerms

	if amount, ok := extraMoney(fields, creditLimitKeys); ok && amount.IsPositive() {
		// A zero or negative limit is not a limit.
		terms.creditLimit, terms.hasCreditLimit = amount, true
	}
	// Owed as a magnitude: see Account.StatementBalance.
	if amount, ok := extraMoney(fields, statementBalanceKeys); ok {
		terms.statementBalance, terms.hasStatementBalance = amount.Abs(), true
	}
	if amount, ok := extraMoney(fields, minimumPaymentKeys); ok {
		terms.minimumPayment, terms.hasMinimumPayment = amount.Abs(), true
	}
	terms.paymentDueOn = extraDate(fields, paymentDueKeys)
	if reported, ok := extraRate(fields, interestRateKeys); ok {
		terms.interestRate, terms.hasInterestRate = domain.APRAsRate(reported)
	}
	return terms
}

// accountFields flattens the account and its `extra` into one lookup with
// normalized keys: connectors put these figures in either place.
func accountFields(raw sfAccount) map[string]any {
	fields := map[string]any{}
	for key, value := range raw.Raw {
		fields[normalizeFieldKey(key)] = value
	}
	// `extra` wins: it is where the spec says a connector's own facts belong.
	for key, value := range raw.Extra {
		fields[normalizeFieldKey(key)] = value
	}
	return fields
}

func normalizeFieldKey(key string) string {
	return strings.ToLower(keySeparators.Replace(strings.TrimSpace(key)))
}

var keySeparators = strings.NewReplacer("_", "", "-", "", " ", "", ".", "")

// extraNumber reads the first of keys that carries something numeric,
// stripping decoration such as "24.99%".
func extraNumber(fields map[string]any, keys []string) (sfNumber, bool) {
	for _, key := range keys {
		value, present := fields[key]
		if !present {
			continue
		}
		var text string
		switch typed := value.(type) {
		case string:
			text = typed
		case json.Number:
			text = typed.String()
		default:
			continue
		}
		text = strings.TrimSpace(extraDecoration.Replace(text))
		if text == "" {
			continue
		}
		return sfNumber{text: text}, true
	}
	return sfNumber{}, false
}

var extraDecoration = strings.NewReplacer("$", "", ",", "", "%", "", " ", "")

// extraMoney reads the first of keys that carries an amount, written as
// domain.MoneyFromJSONValue reads one.
func extraMoney(fields map[string]any, keys []string) (domain.Money, bool) {
	for _, key := range keys {
		switch value := fields[key].(type) {
		case string, json.Number:
			amount, err := domain.MoneyFromJSONValue(value)
			if errors.Is(err, domain.ErrNoAmount) {
				continue
			}
			return amount, err == nil
		}
	}
	return domain.Zero, false
}

func extraRate(fields map[string]any, keys []string) (domain.Rate, bool) {
	number, ok := extraNumber(fields, keys)
	if !ok {
		return decimal.Zero, false
	}
	return number.rate()
}

// extraDate reads a due date, written either as a calendar day or as epoch
// seconds. The zero Date is "not reported".
func extraDate(fields map[string]any, keys []string) domain.Date {
	for _, key := range keys {
		switch typed := fields[key].(type) {
		case string:
			if when, err := domain.ParseDate(strings.TrimSpace(typed)); err == nil {
				return when
			}
		case json.Number:
			if when := (sfNumber{text: typed.String()}).date(); !when.IsZero() {
				return when
			}
		}
	}
	return domain.Date{}
}

// kindFromWords reads a kind out of a type string or an account name. Order
// matters: "Home Equity Line of Credit" is a loan and "Credit Card Loan" is a
// card.
func kindFromWords(text string) (domain.AccountKind, bool) {
	words := normalizedWords(text)
	switch {
	case strings.Contains(words, "credit card"), strings.Contains(words, "creditcard"),
		words == "credit", strings.Contains(words, "visa"),
		strings.Contains(words, "mastercard"), strings.Contains(words, "amex"):
		return domain.KindCreditCard, true

	case strings.Contains(words, "mortgage"), strings.Contains(words, "loan"),
		strings.Contains(words, "line of credit"), strings.Contains(words, "heloc"),
		strings.Contains(words, "lease"):
		return domain.KindLoan, true

	case strings.Contains(words, "invest"), strings.Contains(words, "brokerage"),
		strings.Contains(words, "401"), strings.Contains(words, "403b"),
		strings.Contains(words, "roth"), strings.Contains(words, "retirement"),
		strings.Contains(words, "pension"), strings.Contains(words, "annuity"),
		strings.Contains(words, "529"),
		textutil.ContainsAnyFold(words, cryptoWords), textutil.ContainsAnyFold(words, lifeInsuranceWords),
		// Whole words only: "Admiral Savings" contains "ira".
		textutil.HasWord(words, "ira", textutil.WordOptions{}),
		textutil.HasWord(words, "403", textutil.WordOptions{}):
		return domain.KindInvestment, true
	}
	return domain.KindCash, false
}

// Substrings, as kindFromWords matches: none is a fragment of a banking word.
var (
	cryptoWords = []string{"crypto", "bitcoin", "ethereum", "coinbase", "kraken", "staked",
		"staking"}
	// A policy's cash value; "cash value" must outrank the cash it contains.
	lifeInsuranceWords = []string{"life insurance", "whole life", "universal life",
		"variable life", "cash value"}
)

// normalizedWords lowercases text and turns its separators into spaces, the
// form kindFromWords reads.
func normalizedWords(text string) string {
	return strings.ToLower(strings.NewReplacer("_", " ", "-", " ", "(", " ", ")", " ").Replace(text))
}

func buildTransaction(raw sfTransaction, source PayeeSource) (Transaction, bool) {
	id := raw.ID.String()
	if id == "" {
		return Transaction{}, false
	}
	amount, ok := raw.Amount.money()
	if !ok {
		return Transaction{}, false
	}
	transacted := raw.TransactedAt.date()
	date := raw.Posted.date()
	if date.IsZero() {
		date = transacted
	}
	if date.IsZero() {
		return Transaction{}, false
	}
	if transacted == date {
		transacted = domain.Date{}
	}

	statementName := "Transaction"
	for _, candidate := range []string{raw.Description, raw.Payee, raw.Memo} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			statementName = trimmed
			break
		}
	}
	payee := strings.TrimSpace(raw.Payee)
	if source == PayeeNone {
		payee = ""
	}

	return Transaction{
		ExternalID:    id,
		StatementName: textutil.Clip(statementName, 500),
		Payee:         payee,
		Memo:          textutil.Clip(strings.TrimSpace(raw.Memo), 500),
		Amount:        amount,
		Date:          date,
		TransactedOn:  transacted,
		Currency:      isoCurrency(raw.Currency, ""),
		Pending:       raw.Pending,
		Extra:         unmappedFields(raw.Raw),
	}, true
}

var mappedFields = map[string]bool{
	"id": true, "posted": true, "transacted_at": true, "amount": true,
	"description": true, "payee": true, "memo": true, "currency": true,
	"pending": true,
}

// unmappedFields keeps whatever the row carries beyond the mapped keys, and
// returns nil rather than an empty map so the column holds NULL.
func unmappedFields(raw map[string]any) map[string]any {
	var out map[string]any
	for key, value := range raw {
		if mappedFields[key] {
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		out[key] = value
	}
	return out
}

// costFigures returns (total cost, cost per unit), deriving whichever of
// cost_basis and purchase_price the connector left out from the share count.
// A reported zero means "not supplied": brokerages send 0 for retirement
// accounts, which would otherwise make the whole balance unrealized gain.
func costFigures(raw sfHolding, shares domain.Rate) (domain.Money, domain.Rate) {
	costBasis, _ := raw.CostBasis.money()
	perUnit, _ := raw.PurchasePrice.rate()

	if costBasis.IsZero() && !perUnit.IsZero() && !shares.IsZero() {
		costBasis = domain.FromDecimal(perUnit.Mul(shares))
	}
	if perUnit.IsZero() && !costBasis.IsZero() && !shares.IsZero() {
		perUnit = costBasis.Decimal().Div(shares)
	}
	return costBasis, perUnit
}

func connectionsByID(payload *sfPayload) map[string]sfConnection {
	out := map[string]sfConnection{}
	for _, conn := range payload.Connections {
		if id := conn.ConnID.String(); id != "" {
			out[id] = conn
		}
	}
	return out
}

// institution returns the bank's name and logo. The account's own org wins
// over the top-level connection entry, because that name is what the account
// row stores.
func (s *SimpleFin) institution(raw sfAccount, connections map[string]sfConnection) (string, string) {
	conn := connections[raw.ConnID.String()]
	name := cmp.Or(raw.Org.Name, raw.Org.Domain, conn.Name)
	website := cmp.Or(raw.Org.URL, raw.Org.Domain, conn.OrgURL, conn.URL, raw.Org.SfinURL, conn.SfinURL)
	return name, s.logoFor(website)
}

// warningsFor resolves con.auth entries to the bank names the UI already shows.
func (s *SimpleFin) warningsFor(connAuth []sfError, payload *sfPayload) []BankWarning {
	if len(connAuth) == 0 {
		return nil
	}
	connections := connectionsByID(payload)

	// Prefer the name the account rows store: the connections-list name can
	// differ and would render as a phantom extra bank.
	nameByConnID := map[string]string{}
	for _, acc := range payload.Accounts {
		key := matchKey(acc.ConnID.String())
		if key == "" || nameByConnID[key] != "" {
			continue
		}
		if name, _ := s.institution(acc, connections); name != "" {
			nameByConnID[key] = name
		}
	}
	connByKey := map[string]sfConnection{}
	for id, conn := range connections {
		connByKey[matchKey(id)] = conn
	}

	out := make([]BankWarning, 0, len(connAuth))
	for _, entry := range connAuth {
		key := matchKey(entry.ConnID.String())
		institution := nameByConnID[key]
		if institution == "" {
			institution = connByKey[key].Name
		}
		message := entry.text()
		if message == "" {
			message = "Needs reauthorization"
		}
		out = append(out, BankWarning{Institution: institution, Message: message})
	}
	return out
}

// surfaceErrors fails the read on gen.auth (the access URL is refused), returns
// con.auth entries (one bank needs reauthorization) and logs the rest.
func surfaceErrors(errlist []sfError, context string) ([]sfError, error) {
	if len(errlist) == 0 {
		return nil, nil
	}
	var connAuth []sfError
	for _, entry := range errlist {
		switch strings.ToLower(strings.TrimSpace(entry.Code)) {
		case "gen.auth":
			if message := strings.TrimSpace(entry.text()); message != "" {
				return nil, credentialsExpired(simplefinHelpURL, "simplefin: %s", message)
			}
			return nil, credentialsExpired(simplefinHelpURL,
				"simplefin: the SimpleFIN Bridge needs this connection reauthorized; "+
					"sign in there and generate a new setup token")
		case "con.auth":
			slog.Warn("simplefin: a bank needs reauthorization",
				"context", context, "conn_id", entry.ConnID.String(), "message", entry.text())
			connAuth = append(connAuth, entry)
		default:
			slog.Warn("simplefin: the Bridge reported a problem",
				"context", context, "code", entry.Code, "conn_id", entry.ConnID.String(), "message", entry.text())
		}
	}
	return connAuth, nil
}

// matchKey normalizes a conn_id: the Bridge sends MX-MBR-<uuid> on accounts and
// connections but a bare MBR-<uuid> in errlist.
func matchKey(connID string) string {
	return strings.TrimPrefix(connID, "MX-")
}

// stableExternalID gives the connection an id that survives a re-claim: its
// conn_id, or else a hash of the claim URL, which does not leak the URL.
func stableExternalID(payload *sfPayload, claimURL string) string {
	if len(payload.Connections) > 0 {
		if id := payload.Connections[0].ConnID.String(); id != "" {
			return id
		}
	}
	for _, acc := range payload.Accounts {
		if id := acc.ConnID.String(); id != "" {
			return id
		}
	}
	sum := sha256.Sum256([]byte(claimURL))
	return "simplefin-" + hex.EncodeToString(sum[:])[:24]
}

// decodeSetupToken forgives whitespace and lost trailing padding, since tokens
// are pasted.
func decodeSetupToken(raw string) (string, error) {
	token := setupTokenSpace.Replace(raw)
	if token == "" {
		return "", errors.New("simplefin: the setup token is empty; paste the token the SimpleFIN Bridge gave you")
	}
	if rem := len(token) % 4; rem != 0 {
		token += strings.Repeat("=", 4-rem)
	}
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", errors.New("simplefin: that is not a SimpleFIN setup token; copy it again from the SimpleFIN Bridge")
	}
	claimURL := string(decoded)
	if !strings.HasPrefix(claimURL, "http://") && !strings.HasPrefix(claimURL, "https://") {
		return "", errors.New("simplefin: that setup token does not hold a claim address; copy it again from the SimpleFIN Bridge")
	}
	return claimURL, nil
}

var setupTokenSpace = strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "")

// accountsURLAndAuth moves an access URL's userinfo into Basic credentials, so
// the credential is not in a URL that logs and errors repeat.
func accountsURLAndAuth(accessURL string) (endpoint, username, password string, err error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(accessURL), "/"))
	if err != nil {
		return "", "", "", errors.New("simplefin: the stored access URL is not a valid address; connect again with a new setup token")
	}
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/accounts"
	parsed.RawPath = ""
	return parsed.String(), username, password, nil
}

func redactUserinfo(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return raw
	}
	parsed.User = url.User("redacted")
	return parsed.String()
}

// redactedError strips a credential-bearing URL out of a transport error:
// net/http puts the request URL in its error text. Without a replacement the
// URL's userinfo is masked.
func redactedError(err error, secretURL string, replacement ...string) error {
	if err == nil || secretURL == "" {
		return err
	}
	cleanedWith := redactUserinfo(secretURL)
	if len(replacement) > 0 && replacement[0] != "" {
		cleanedWith = replacement[0]
	}
	text := err.Error()
	cleaned := strings.ReplaceAll(text, secretURL, cleanedWith)
	if cleaned == text {
		return err
	}
	return fmt.Errorf("%s", cleaned)
}

// isoCurrency passes through only an ISO 4217-shaped code: the columns are
// varchar(3), and one oversized value fails the whole connection's sync.
func isoCurrency(value, fallback string) string {
	if len(value) != 3 {
		return fallback
	}
	for _, r := range value {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return fallback
		}
	}
	return strings.ToUpper(value)
}

// normalizeTicker fits the securities.ticker column's 32 characters.
func normalizeTicker(value string) string {
	return textutil.Clip(strings.ToUpper(strings.TrimSpace(value)), 32)
}

// urlHost reduces a credential-bearing URL to scheme://host for error text.
func urlHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "(url)"
	}
	return parsed.Scheme + "://" + parsed.Host
}
