package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/stretchr/testify/require"
)

var syncDay = domain.NewDate(2026, time.March, 20)

// bridge answers /simplefin/accounts and /claim.
type bridge struct {
	server   *httptest.Server
	provider *SimpleFin
	creds    Credentials
	// requests records the query of every accounts call, in order.
	requests []url.Values
	// authorized records the Basic Auth pair each accounts call carried.
	authorized []string
}

func newBridge(t *testing.T, accounts http.HandlerFunc) *bridge {
	t.Helper()
	b := &bridge{}
	b.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/accounts") {
			b.requests = append(b.requests, r.URL.Query())
			user, pass, _ := r.BasicAuth()
			b.authorized = append(b.authorized, user+":"+pass)
			w.Header().Set("Content-Type", "application/json")
			accounts(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(b.server.Close)

	b.provider = &SimpleFin{
		HTTPClient: b.server.Client(), AllowPrivate: true,
		Now: func() time.Time { return syncDay.Time() },
	}
	b.creds = Credentials{AccessURL: b.accessURL()}
	return b
}

func (b *bridge) accessURL() string {
	return strings.Replace(b.server.URL, "http://", "http://demo:secret@", 1) + "/simplefin"
}

func serve(payload string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, payload) }
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func encodeSetupToken(claimURL string) string {
	return base64.StdEncoding.EncodeToString([]byte(claimURL))
}

func TestASetupTokenRoundTrips(t *testing.T) {
	decoded, err := decodeSetupToken(encodeSetupToken("https://bridge.simplefin.org/simplefin/claim/abc123"))
	require.NoError(t, err)
	require.Equal(t, "https://bridge.simplefin.org/simplefin/claim/abc123", decoded)
}

func TestAPastedSetupTokenSurvivesWhitespaceAndLostPadding(t *testing.T) {
	raw := encodeSetupToken("https://bridge.simplefin.org/simplefin/claim/xyz")
	sloppy := "  " + strings.TrimRight(raw, "=") + "\n  "
	decoded, err := decodeSetupToken(sloppy)
	require.NoError(t, err)
	require.Contains(t, decoded, "claim/xyz")
}

func TestARejectedSetupToken(t *testing.T) {
	for name, token := range map[string]string{
		"empty":       "   ",
		"not a URL":   encodeSetupToken("ftp://nope.example"),
		"not base64":  "this-is-not-base64!!!@@@",
		"not any URL": encodeSetupToken("just some text"),
	} {
		_, err := decodeSetupToken(token)
		require.Error(t, err, name)
	}
}

func TestTheAccessURLsCredentialsMoveIntoTheAuthHeader(t *testing.T) {
	// A URL with userinfo in it ends up in proxy logs and error strings.
	endpoint, user, pass, err := accountsURLAndAuth("https://demo:secret@bridge.simplefin.org/simplefin/")
	require.NoError(t, err)
	require.Equal(t, "https://bridge.simplefin.org/simplefin/accounts", endpoint)
	require.Equal(t, "demo", user)
	require.Equal(t, "secret", pass)
}

func TestAnEpochFieldThatIsUnsetIsNotNineteenSeventy(t *testing.T) {
	// SimpleFIN sends 0 for a transaction that has not posted yet.
	require.True(t, sfNumber{}.date().IsZero())
	require.True(t, sfNumber{text: "0"}.date().IsZero())
	require.Equal(t, domain.NewDate(2024, time.January, 15), sfNumber{text: "1705276800"}.date())
}

func TestATickerIsNormalizedForItsColumn(t *testing.T) {
	require.Equal(t, "AAPL", normalizeTicker("  aapl "))
	require.Len(t, normalizeTicker(strings.Repeat("X", 64)), 32)
}

func TestConnectClaimsAnAccessURLAndParsesTheAccounts(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[
		{"id":"acc-1","name":"Everyday Checking","currency":"USD","balance":"1234.56",
		 "conn_id":"MX-MBR-1","org":{"name":"Chase","url":"https://www.chase.com"}}],
		"connections":[{"conn_id":"MX-MBR-1","name":"Chase Bank"}]}`))

	claimServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		fmt.Fprint(w, b.accessURL())
	}))
	defer claimServer.Close()

	connection, err := b.provider.Connect(context.Background(), encodeSetupToken(claimServer.URL+"/claim"))
	require.NoError(t, err)
	require.Equal(t, "MX-MBR-1", connection.ExternalID)
	require.Equal(t, "SimpleFIN", connection.InstitutionName)
	require.Equal(t, b.accessURL(), connection.Credentials.AccessURL)
	require.False(t, connection.Credentials.ClaimedAt.IsZero())
	require.Empty(t, connection.Warnings)

	require.Len(t, connection.Accounts, 1)
	account := connection.Accounts[0]
	require.Equal(t, "acc-1", account.ExternalID)
	require.Equal(t, "Everyday Checking", account.Name)
	require.Equal(t, "1234.56", account.Balance.String())
	// The account's own org wins over the connections list.
	require.Equal(t, "Chase", account.InstitutionName)
	require.Contains(t, account.InstitutionLogoURL, "chase.com")

	// Transactions are read per account, so the claim must not ask for them.
	require.Equal(t, "", b.requests[0].Get("pending"))
}

func TestAReusedSetupTokenReadsAsCredentialsExpired(t *testing.T) {
	b := newBridge(t, serve(`{}`))
	claimServer := httptest.NewServer(status(http.StatusForbidden))
	defer claimServer.Close()

	_, err := b.provider.Connect(context.Background(), encodeSetupToken(claimServer.URL+"/claim"))
	require.ErrorIs(t, err, ErrCredentialsExpired)

	var expired *CredentialsExpiredError
	require.ErrorAs(t, err, &expired)
	require.Equal(t, "https://bridge.simplefin.org/", expired.HelpURL)
}

func TestTheConnectionIDIsStableAcrossReclaimsWhenTheBridgeNamesNoConnection(t *testing.T) {
	payload := &sfPayload{}
	require.Equal(t,
		stableExternalID(payload, "https://bridge.simplefin.org/claim/abc"),
		stableExternalID(payload, "https://bridge.simplefin.org/claim/abc"))
	require.NotContains(t, stableExternalID(payload, "https://bridge.simplefin.org/claim/abc"), "claim")
}

func TestAGeneralAuthErrorMeansTheWholeConnectionNeedsANewToken(t *testing.T) {
	b := newBridge(t, serve(`{"errlist":[{"code":"gen.auth","msg":"Reauthorize"}],"accounts":[]}`))

	_, err := b.provider.Accounts(context.Background(), b.creds)
	require.ErrorIs(t, err, ErrCredentialsExpired)
}

func TestAConnectionAuthErrorIsRecordedWithoutBreakingTheOtherBanks(t *testing.T) {
	// The Access URL still works and the other banks keep syncing.
	b := newBridge(t, serve(`{"errlist":[{"code":"con.auth","conn_id":"MBR-1","msg":"Bank needs reauth"}],
		"accounts":[
			{"id":"a","name":"Chase Checking","balance":"1","conn_id":"MX-MBR-1","org":{"name":"Chase"}},
			{"id":"b","name":"Ally Savings","balance":"2","conn_id":"MX-MBR-2","org":{"name":"Ally"}}],
		"connections":[{"conn_id":"MX-MBR-1","name":"Chase Bank"}]}`))

	accounts, warnings, err := b.provider.AccountsWithWarnings(context.Background(), b.creds)
	require.NoError(t, err)
	require.Len(t, accounts, 2)
	require.Len(t, warnings, 1)
	// The errlist drops the MX- prefix the accounts carry.
	require.Equal(t, "Chase", warnings[0].Institution)
	require.Equal(t, "Bank needs reauth", warnings[0].Message)
}

func TestAnUnknownErrorCodeIsASoftWarning(t *testing.T) {
	b := newBridge(t, serve(`{"errlist":[{"code":"act.failed","msg":"Bank site is down"}],
		"accounts":[{"id":"a","name":"Checking","balance":"5"}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
}

func TestARejectedAccessURLReadsAsCredentialsExpired(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		b := newBridge(t, status(code))
		_, err := b.provider.Accounts(context.Background(), b.creds)
		require.ErrorIs(t, err, ErrCredentialsExpired)
	}
}

func TestARateLimitIsNotAReconnectPrompt(t *testing.T) {
	// The credential is fine; the caller parks the connection and retries.
	b := newBridge(t, status(http.StatusTooManyRequests))

	_, err := b.provider.Accounts(context.Background(), b.creds)
	require.ErrorIs(t, err, ErrRateLimited)
	require.NotErrorIs(t, err, ErrCredentialsExpired)
}

func TestAMissingAccessURLIsCredentialsExpiredNotACrash(t *testing.T) {
	provider := &SimpleFin{}
	_, err := provider.Accounts(context.Background(), Credentials{})
	require.ErrorIs(t, err, ErrCredentialsExpired)
}

func TestTheAccountsRequestAuthenticatesWithAHeader(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[]}`))

	_, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, []string{"demo:secret"}, b.authorized)
	require.Equal(t, "2", b.requests[0].Get("version"))
}

func TestADebtBalanceIsStoredNegativeAtIngest(t *testing.T) {
	// docs/calculations.md: a positive amount owed is normalized on
	// ingest.
	b := newBridge(t, serve(`{"accounts":[
		{"id":"card","name":"Sample Rewards Card","balance":"2400.00","extra":{"type":"credit card"}},
		{"id":"chk","name":"Checking","balance":"800.00"}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, domain.KindCreditCard, accounts[0].Kind)
	require.Equal(t, "-2400.00", accounts[0].Balance.String())
	require.Equal(t, domain.KindCash, accounts[1].Kind)
	require.Equal(t, "800.00", accounts[1].Balance.String())
}

func TestAnAlreadyNegativeDebtBalanceIsNotFlippedBack(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[
		{"id":"card","name":"Sample Rewards Card","balance":"-2400.00","extra":{"type":"credit_card"}}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, "-2400.00", accounts[0].Balance.String())
}

func TestABalanceArrivingAsAJSONNumberKeepsItsDigits(t *testing.T) {
	// The spec says strings; connectors also send numbers, which must not pass
	// through float64.
	b := newBridge(t, serve(`{"accounts":[{"id":"a","name":"Checking","balance":1234.56}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, "1234.56", accounts[0].Balance.String())
}

func TestANonISOAccountCurrencyFallsBackToUSD(t *testing.T) {
	// The columns are varchar(3); an oversized value fails the whole sync.
	b := newBridge(t, serve(`{"accounts":[{"id":"a","name":"Wallet","balance":"1","currency":"DOGECOIN"}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, "USD", accounts[0].Currency)
}

func TestEachAccountIsTaggedWithItsOwnBank(t *testing.T) {
	// One Access URL spans every bank the user linked, so the institution
	// lives on the account, not the connection.
	b := newBridge(t, serve(`{"accounts":[
		{"id":"a","name":"Checking","balance":"1","conn_id":"c1","org":{"name":"Chase","domain":"chase.com"}},
		{"id":"b","name":"Savings","balance":"2","conn_id":"c2"}],
		"connections":[{"conn_id":"c2","name":"Ally","org_url":"https://www.ally.com"}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, "Chase", accounts[0].InstitutionName)
	require.Contains(t, accounts[0].InstitutionLogoURL, "chase.com")
	require.Equal(t, "Ally", accounts[1].InstitutionName)
	require.Contains(t, accounts[1].InstitutionLogoURL, "ally.com")
}

func TestAnAccountWithoutAnIDIsSkipped(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"name":"Nameless","balance":"1"},{"id":"a","balance":"2"}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "Account", accounts[0].Name)
}

func TestTheConnectionLogoComesFromTheSimpleFinHost(t *testing.T) {
	provider := &SimpleFin{}
	logo, err := provider.InstitutionLogo(context.Background(),
		Credentials{AccessURL: "https://demo:secret@bridge.simplefin.org/simplefin"})
	require.NoError(t, err)
	require.Contains(t, logo, "bridge.simplefin.org")
	require.NotContains(t, logo, "secret")
}

func TestTransactionsAreFilteredToTheAccountAndKeepTheirSigns(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[
		{"id":"acc-1","currency":"USD","transactions":[
			{"id":"t1","posted":1742428800,"amount":"-42.50","description":"WHOLE FOODS MKT","payee":"Whole Foods"},
			{"id":"t2","posted":1742428800,"amount":"1500.00","description":"ACME PAYROLL","pending":true}]},
		{"id":"acc-2","transactions":[{"id":"t3","posted":1742428800,"amount":"-9.99","description":"OTHER"}]}]}`))

	txns, err := b.provider.Transactions(context.Background(), b.creds, "acc-1",
		TransactionQuery{Since: syncDay.AddDays(-3), PayeeSource: PayeeFromProvider})
	require.NoError(t, err)
	require.Len(t, txns, 2)

	require.Equal(t, "-42.50", txns[0].Amount.String())
	require.Equal(t, "WHOLE FOODS MKT", txns[0].StatementName)
	require.Equal(t, "Whole Foods", txns[0].Payee)
	require.False(t, txns[0].Pending)
	require.Empty(t, txns[0].Extra, "nothing beyond the spec's own fields was sent")

	require.Equal(t, "1500.00", txns[1].Amount.String())
	require.True(t, txns[1].Pending)
	// The bank's wording is all there is when the feed sends no payee.
	require.Empty(t, txns[1].Payee)
}

func TestThePayeeCanBeSuppressedSoRulesDeriveItInstead(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"acc-1","transactions":[
		{"id":"t1","posted":1742428800,"amount":"-1","description":"SQ *COFFEE","payee":"Square"}]}]}`))

	txns, err := b.provider.Transactions(context.Background(), b.creds, "acc-1",
		TransactionQuery{Since: syncDay.AddDays(-3), PayeeSource: PayeeNone})
	require.NoError(t, err)
	require.Empty(t, txns[0].Payee)
	require.Equal(t, "SQ *COFFEE", txns[0].StatementName)
}

func TestAnUnpostedTransactionFallsBackToWhenItWasTransacted(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"acc-1","transactions":[
		{"id":"t1","posted":0,"transacted_at":1742428800,"amount":"-1","description":"PENDING"}]}]}`))

	txns, err := b.provider.Transactions(context.Background(), b.creds, "acc-1",
		TransactionQuery{Since: syncDay.AddDays(-3)})
	require.NoError(t, err)
	require.Len(t, txns, 1)
	require.Equal(t, domain.NewDate(2025, time.March, 20), txns[0].Date)
}

// A card line like "SQ *XKCD4821" says nothing a category can be read off; the
// memo beside it often does.
func TestTheMemoIsKeptBesideTheDescriptionRatherThanInsteadOfIt(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"acc-1","transactions":[
		{"id":"t1","posted":1742428800,"amount":"-9.00","description":"SQ *XKCD4821",
		 "memo":"CAFE MOCHA / TIP"}]}]}`))

	txns, err := b.provider.Transactions(context.Background(), b.creds, "acc-1",
		TransactionQuery{Since: syncDay.AddDays(-3)})
	require.NoError(t, err)
	require.Equal(t, "SQ *XKCD4821", txns[0].StatementName)
	require.Equal(t, "CAFE MOCHA / TIP", txns[0].Memo)
}

func TestAMemoStillStandsInForAMissingDescription(t *testing.T) {
	raw := sfTransaction{ID: sfString{"t1"}, Posted: sfNumber{text: "1742428800"},
		Amount: sfNumber{text: "-1"}, Memo: "ATM WITHDRAWAL"}
	txn, ok := buildTransaction(raw, PayeeFromProvider)
	require.True(t, ok)
	require.Equal(t, "ATM WITHDRAWAL", txn.StatementName)
	require.Equal(t, "ATM WITHDRAWAL", txn.Memo)
}

// Two dates, not one with a fallback: a card charge swiped on the 28th and
// posted on the 2nd is one row the bank knows two things about.
func TestWhenAChargeHappenedIsKeptSeparatelyFromWhenItPosted(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"acc-1","transactions":[
		{"id":"t1","posted":1742428800,"transacted_at":1742169600,"amount":"-20","description":"X"},
		{"id":"t2","posted":1742428800,"transacted_at":1742428800,"amount":"-20","description":"Y"},
		{"id":"t3","posted":1742428800,"amount":"-20","description":"Z"}]}]}`))

	txns, err := b.provider.Transactions(context.Background(), b.creds, "acc-1",
		TransactionQuery{Since: syncDay.AddDays(-8)})
	require.NoError(t, err)
	require.Len(t, txns, 3)

	require.Equal(t, domain.NewDate(2025, time.March, 20), txns[0].Date)
	require.Equal(t, domain.NewDate(2025, time.March, 17), txns[0].TransactedOn)
	// Same day: repeating the posting date would read as a fact the bank
	// stated.
	require.True(t, txns[1].TransactedOn.IsZero())
	require.True(t, txns[2].TransactedOn.IsZero())
}

func TestEverythingTheFeedSendsThatNoFieldNamesIsKept(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"acc-1","transactions":[
		{"id":"t1","posted":1742428800,"amount":"-42.50","description":"WHOLE FOODS MKT",
		 "payee":"Whole Foods","memo":"GROCERY",
		 "extra":{"category":"Groceries","mcc":"5411"},
		 "some_connector_key":"whatever it means"}]}]}`))

	txns, err := b.provider.Transactions(context.Background(), b.creds, "acc-1",
		TransactionQuery{Since: syncDay.AddDays(-3)})
	require.NoError(t, err)

	extra := txns[0].Extra
	require.Equal(t, map[string]any{"category": "Groceries", "mcc": "5411"}, extra["extra"])
	require.Equal(t, "whatever it means", extra["some_connector_key"])
	// Mapped fields are not duplicated into the extras.
	for _, named := range []string{"id", "posted", "amount", "description", "payee", "memo"} {
		require.NotContains(t, extra, named)
	}
}

func TestATransactionWithNoUsableDateIsDropped(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"acc-1","transactions":[
		{"id":"t1","amount":"-1","description":"NO DATE"},
		{"id":"t2","posted":1742428800,"description":"NO AMOUNT"},
		{"posted":1742428800,"amount":"-1","description":"NO ID"}]}]}`))

	txns, err := b.provider.Transactions(context.Background(), b.creds, "acc-1",
		TransactionQuery{Since: syncDay.AddDays(-3)})
	require.NoError(t, err)
	require.Empty(t, txns)
}

func TestANonISOTransactionCurrencyIsUnknownRatherThanUSD(t *testing.T) {
	// The sync layer resolves an unset currency to the account's, then to the
	// space's; claiming USD would skip both.
	raw := sfTransaction{ID: sfString{"t1"}, Posted: sfNumber{text: "1742428800"},
		Amount: sfNumber{text: "-1"}, Description: "X", Currency: "DOGE"}
	txn, ok := buildTransaction(raw, PayeeFromProvider)
	require.True(t, ok)
	require.Empty(t, txn.Currency)

	raw.Currency = "eur"
	txn, ok = buildTransaction(raw, PayeeFromProvider)
	require.True(t, ok)
	require.Equal(t, "EUR", txn.Currency)
}

func TestALongHistoryIsWalkedInFortyFiveDayChunks(t *testing.T) {
	// The Bridge warns past 45 days, so backfill is chunked and overlaps must
	// not double-count.
	b := newBridge(t, serve(`{"accounts":[{"id":"acc-1","transactions":[
		{"id":"t1","posted":1742428800,"amount":"-1","description":"X"}]}]}`))

	txns, err := b.provider.Transactions(context.Background(), b.creds, "acc-1", TransactionQuery{})
	require.NoError(t, err)
	require.Len(t, txns, 1, "the same row appeared in more than one chunk")
	require.Len(t, b.requests, 9, "a year and a day should be nine calls of at most 45 days")

	for i, request := range b.requests {
		require.Equal(t, "acc-1", request.Get("account"))
		require.Equal(t, "1", request.Get("pending"))
		start := requestDay(t, request, "start-date")
		end := requestDay(t, request, "end-date")
		require.LessOrEqual(t, end.Time().Sub(start.Time()).Hours()/24, float64(45),
			"chunk %d spans more than the bridge recommends", i)
		if i > 0 {
			require.Equal(t, requestDay(t, b.requests[i-1], "end-date"), start,
				"chunk %d does not start where the one before it ended", i)
		}
	}
	// The bridge's end date is exclusive.
	require.Equal(t, syncDay.AddDays(-simplefinInitialHistoryDays), requestDay(t, b.requests[0], "start-date"))
	require.Equal(t, syncDay.AddDays(1), requestDay(t, b.requests[len(b.requests)-1], "end-date"))
}

func TestAResumeWithinTheWindowIsOneRequest(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[]}`))

	_, err := b.provider.Transactions(context.Background(), b.creds, "acc-1",
		TransactionQuery{Since: syncDay.AddDays(-44)})
	require.NoError(t, err)
	require.Len(t, b.requests, 1)
	require.Equal(t, syncDay.AddDays(-44), requestDay(t, b.requests[0], "start-date"))
	require.Equal(t, syncDay.AddDays(1), requestDay(t, b.requests[0], "end-date"))
}

func TestAResumeDayAfterTodayIsAnErrorRatherThanAnEmptyRead(t *testing.T) {
	// An inverted window would otherwise return nothing and read as a
	// successful empty sync.
	b := newBridge(t, serve(`{"accounts":[]}`))

	_, err := b.provider.Transactions(context.Background(), b.creds, "acc-1",
		TransactionQuery{Since: syncDay.AddDays(1)})
	require.ErrorContains(t, err, "after today")
	require.Empty(t, b.requests, "the bridge was asked for a window that has not happened")
}

func requestDay(t *testing.T, request url.Values, key string) domain.Date {
	t.Helper()
	seconds := sfNumber{text: request.Get(key)}
	day := seconds.date()
	require.False(t, day.IsZero(), "%s was not sent", key)
	return day
}

func TestHoldingsAreParsedWithTheirCostFigures(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"brk","currency":"USD","holdings":[
		{"id":"h1","description":"Apple Inc.","symbol":"aapl","shares":"10","market_value":"1900.00",
		 "cost_basis":"1000.00","purchase_price":"100.00","created":1705276800,"isin":"US0378331005"}]}]}`))

	holdings, err := b.provider.Holdings(context.Background(), b.creds)
	require.NoError(t, err)
	require.Len(t, holdings, 1)

	h := holdings[0]
	require.Equal(t, "h1", h.ExternalID)
	require.Equal(t, "Apple Inc.", h.Name)
	require.Equal(t, "AAPL", h.Ticker)
	require.Equal(t, "US0378331005", h.ISIN)
	require.Equal(t, "brk", h.AccountExternalID)
	require.Equal(t, "1900.00", h.CurrentValue.String())
	require.Equal(t, "10", h.Quantity.String())
	require.Equal(t, "190", h.UnitPrice.String())
	require.Equal(t, "1000.00", h.CostBasis.String())
	require.Equal(t, "100", h.AveragePrice.String())
	require.Equal(t, domain.NewDate(2024, time.January, 15), h.PurchaseDate)
}

func TestTheMissingHalfOfTheCostIsDerivedFromTheShareCount(t *testing.T) {
	// Connectors fill in one or the other.
	b := newBridge(t, serve(`{"accounts":[{"id":"brk","holdings":[
		{"id":"h1","symbol":"VTI","shares":"4","market_value":"1000.00","purchase_price":"200.00"},
		{"id":"h2","symbol":"VXUS","shares":"5","market_value":"500.00","cost_basis":"400.00"}]}]}`))

	holdings, err := b.provider.Holdings(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, "800.00", holdings[0].CostBasis.String())
	require.Equal(t, "80", holdings[1].AveragePrice.String())
}

func TestAReportedZeroCostIsUnknownNotFree(t *testing.T) {
	// A brokerage may send 0 cost basis for retirement accounts; taken
	// literally, the whole balance is unrealized gain.
	b := newBridge(t, serve(`{"accounts":[{"id":"ira","holdings":[
		{"id":"h1","symbol":"ABCDX","shares":"100","market_value":"50000.00","cost_basis":0,"purchase_price":0}]}]}`))

	holdings, err := b.provider.Holdings(context.Background(), b.creds)
	require.NoError(t, err)
	require.True(t, holdings[0].CostBasis.IsZero())
	require.True(t, holdings[0].AveragePrice.IsZero())
}

func TestACryptoHoldingsTickerCurrencyFallsBackToTheAccounts(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"cb","currency":"USD","holdings":[
		{"id":"h1","symbol":"DOGE","description":"Dogecoin","currency":"DOGECOIN",
		 "shares":"1000","market_value":"120.00"}]}]}`))

	holdings, err := b.provider.Holdings(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, "USD", holdings[0].Currency)
}

func TestAHoldingWithoutAMarketValueIsSkipped(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"brk","holdings":[
		{"id":"h1","symbol":"X"},{"symbol":"Y","market_value":"5"}]}]}`))

	holdings, err := b.provider.Holdings(context.Background(), b.creds)
	require.NoError(t, err)
	require.Empty(t, holdings)
}

func TestAnAccountWithHoldingsIsClassifiedAsAnInvestmentAccount(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"brk","name":"Brokerage","balance":"100",
		"holdings":[{"id":"h1","symbol":"VTI","market_value":"100"}]}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, domain.KindInvestment, accounts[0].Kind)
}

func TestAnAccountIsClassifiedByItsNameWhenTheConnectorSendsNoType(t *testing.T) {
	// The Bridge rarely populates "extra", so unlabelled accounts are
	// classified by name.
	b := newBridge(t, serve(`{"accounts":[
		{"id":"m","name":"Home Mortgage","balance":"284000.00"},
		{"id":"v","name":"Auto Loan 2019 Hatchback","balance":"18400.00"},
		{"id":"c","name":"Visa Signature","balance":"800.00"},
		{"id":"h","name":"HELOC","balance":"12000.00"},
		{"id":"b","name":"Example Brokerage","balance":"91000.00"},
		{"id":"r","name":"Roth IRA","balance":"64000.00"},
		{"id":"k","name":"Employer 401k","balance":"210000.00"},
		{"id":"s","name":"Admiral Savings","balance":"9000.00"},
		{"id":"x","name":"Everyday Checking","balance":"1200.00"}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)

	byID := map[string]Account{}
	for _, one := range accounts {
		byID[one.ExternalID] = one
	}
	require.Equal(t, domain.KindLoan, byID["m"].Kind)
	require.Equal(t, domain.KindLoan, byID["v"].Kind)
	require.Equal(t, domain.KindCreditCard, byID["c"].Kind)
	require.Equal(t, domain.KindLoan, byID["h"].Kind)
	require.Equal(t, domain.KindInvestment, byID["b"].Kind)
	require.Equal(t, domain.KindInvestment, byID["r"].Kind)
	require.Equal(t, domain.KindInvestment, byID["k"].Kind)

	// "Admiral" contains "ira": short tokens match whole words only.
	require.Equal(t, domain.KindCash, byID["s"].Kind)
	require.Equal(t, domain.KindCash, byID["x"].Kind)

	// Classified as debt means stored as debt, by the same path as a typed one.
	require.Equal(t, "-284000.00", byID["m"].Balance.String())
}

func TestCryptoAndLifeInsuranceAreInvestmentsWithTheirOwnTypes(t *testing.T) {
	// A wallet alone is cash; holdings or a crypto word make it an investment,
	// and then it is crypto. "Cash Value" is a policy, not cash.
	b := newBridge(t, serve(`{"accounts":[
		{"id":"cb","name":"Coinbase","balance":"300.00"},
		{"id":"st","name":"SOL Staked","balance":"40.00"},
		{"id":"bw","name":"BTC Wallet","balance":"90.00",
		 "holdings":[{"id":"h1","symbol":"BTC","market_value":"90.00"}]},
		{"id":"wl","name":"Universal Life Policy","balance":"5200.00"},
		{"id":"cv","name":"Policy Cash Value","balance":"1800.00"},
		{"id":"w","name":"Travel Wallet","balance":"25.00"},
		{"id":"d","name":"Ledger","balance":"10.00","extra":{"type":"crypto"}}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)

	byID := map[string]Account{}
	for _, one := range accounts {
		byID[one.ExternalID] = one
	}
	for _, id := range []string{"cb", "st", "bw", "d"} {
		require.Equal(t, domain.KindInvestment, byID[id].Kind, id)
		require.Equal(t, "crypto", byID[id].Type, id)
	}
	for _, id := range []string{"wl", "cv"} {
		require.Equal(t, domain.KindInvestment, byID[id].Kind, id)
		require.Equal(t, "life_insurance", byID[id].Type, id)
	}
	require.Equal(t, "5200.00", byID["wl"].Balance.String(), "an asset keeps its sign")
	require.Equal(t, domain.KindCash, byID["w"].Kind)
	require.Empty(t, byID["w"].Type)
}

func TestAnHSAIsBankingUnlessItSaysItInvests(t *testing.T) {
	// Most HSAs are a checking-style account behind a debit card; the
	// brokerage their cash is swept into names itself, or holds funds.
	b := newBridge(t, serve(`{"accounts":[
		{"id":"c","name":"Family HSA","balance":"1400.00"},
		{"id":"d","name":"Spending","balance":"75.00","extra":{"type":"HSA"}},
		{"id":"i","name":"HSA Investment Account","balance":"6200.00"},
		{"id":"b","name":"HSA Brokerage","balance":"3100.00"},
		{"id":"h","name":"Health Savings HSA","balance":"880.00",
		 "holdings":[{"id":"h1","symbol":"VTI","market_value":"880.00"}]}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)

	byID := map[string]Account{}
	for _, one := range accounts {
		byID[one.ExternalID] = one
	}
	for _, id := range []string{"c", "d"} {
		require.Equal(t, domain.KindCash, byID[id].Kind, id)
		require.Equal(t, "hsa", byID[id].Type, id)
	}
	for _, id := range []string{"i", "b", "h"} {
		require.Equal(t, domain.KindInvestment, byID[id].Kind, id)
		require.Equal(t, "hsa_investment", byID[id].Type, id)
	}
}

func TestAConnectorsOwnTypeOutranksTheAccountName(t *testing.T) {
	// A sent type beats a loan word in the name.
	b := newBridge(t, serve(`{"accounts":[
		{"id":"s","name":"Mortgage Payoff Savings","balance":"4000.00","extra":{"type":"savings"}}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, domain.KindCash, accounts[0].Kind)
	require.Equal(t, "4000.00", accounts[0].Balance.String())
}

func TestAnAccountKeepsEverythingTheFeedSentThatNoFieldNames(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"c1","name":"Visa","balance":"-800.00",
		"available-balance":"4200.00","balance-date":1757000000,
		"extra":{"type":"credit card","credit-limit":"5000.00","rewards":"2% back"}}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)

	extra := accounts[0].Extra
	require.Equal(t, "4200.00", extra["available-balance"])
	require.Contains(t, extra, "balance-date")
	// `extra` stays nested as the spec has it.
	nested, ok := extra["extra"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "2% back", nested["rewards"])
	// Fields that do have a home are not repeated into the payload column.
	require.NotContains(t, extra, "id")
	require.NotContains(t, extra, "balance")
	require.NotContains(t, extra, "name")
}

func TestAnAccountWithNothingUnmappedCarriesNoPayload(t *testing.T) {
	b := newBridge(t, serve(`{"accounts":[{"id":"a1","name":"Checking","balance":"10.00"}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Nil(t, accounts[0].Extra, "nil so the column holds NULL rather than {}")
}

func TestACardsStatementFiguresAreReadOutOfTheExtraObject(t *testing.T) {
	// None of these is in the spec; spellings differ by connector.
	b := newBridge(t, serve(`{"accounts":[{"id":"c1","name":"Visa","balance":"-1250.00",
		"extra":{"credit_limit":"5,000.00","statement-balance":"-1200.00",
		 "minimum_payment":"$35.00","payment-due-date":"2026-09-28","apr":"24.99%"}}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	card := accounts[0]

	require.True(t, card.HasCreditLimit)
	require.Equal(t, "5000.00", card.CreditLimit.String())
	// Owed as a magnitude, whatever sign the connector used.
	require.True(t, card.HasStatementBalance)
	require.Equal(t, "1200.00", card.StatementBalance.String())
	require.True(t, card.HasMinimumPayment)
	require.Equal(t, "35.00", card.MinimumPayment.String())
	require.Equal(t, "2026-09-28", card.PaymentDueOn.String())
	// 24.99% and 0.2499 are the same rate; the column holds a rate.
	require.True(t, card.HasInterestRate)
	require.Equal(t, "0.2499", card.InterestRate.String())
}

func TestAnUnreportedStatementFigureIsAbsentRatherThanZero(t *testing.T) {
	// Absent fields leave a hand-entered figure alone.
	b := newBridge(t, serve(`{"accounts":[{"id":"c1","name":"Visa","balance":"-1250.00",
		"extra":{"type":"credit card","credit-limit":"0","available-credit":"4000.00"}}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	card := accounts[0]

	// A zero limit is not a limit, and available credit is not one either.
	require.False(t, card.HasCreditLimit)
	require.False(t, card.HasStatementBalance)
	require.False(t, card.HasMinimumPayment)
	require.False(t, card.HasInterestRate)
	require.True(t, card.PaymentDueOn.IsZero())
}

func TestATopLevelStatementFigureIsReadLikeOneInExtra(t *testing.T) {
	// A connector that puts its own fields beside `name` instead of inside
	// `extra` is out of spec and commonplace.
	b := newBridge(t, serve(`{"accounts":[{"id":"c1","name":"Visa","balance":"-100.00",
		"creditLimit":2500,"minimumDue":25}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.Equal(t, "2500.00", accounts[0].CreditLimit.String())
	require.Equal(t, "25.00", accounts[0].MinimumPayment.String())
}

func TestAStatementFigureWithCommasOutOfPlaceIsNotRead(t *testing.T) {
	// "5,00" is not five hundred; reading it with the comma dropped would be.
	b := newBridge(t, serve(`{"accounts":[{"id":"c1","name":"Visa","balance":"-100.00",
		"extra":{"credit_limit":"5,00","minimum_payment":"(35.00)"}}]}`))

	accounts, err := b.provider.Accounts(context.Background(), b.creds)
	require.NoError(t, err)
	require.False(t, accounts[0].HasCreditLimit)
	require.True(t, accounts[0].HasMinimumPayment)
	require.Equal(t, "35.00", accounts[0].MinimumPayment.String())
}

func TestThereIsNoBillFeed(t *testing.T) {
	// "No such concept" must not read as "the request failed".
	provider := &SimpleFin{}
	bills, err := provider.Bills(context.Background(), Credentials{}, "acc-1")
	require.NoError(t, err)
	require.Empty(t, bills)
	require.Equal(t, "simplefin", provider.Name())
}

func TestAClaimFailureNamesTheHostButNeverTheTokenPath(t *testing.T) {
	// The claim URL's secret is in the path, not the userinfo.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close() // nothing is listening: a guaranteed transport failure

	claim := encodeSetupToken(dead.URL + "/simplefin/claim/SECRET-TOKEN-PATH")
	_, err := newBridge(t, serve("{}")).provider.Connect(context.Background(), claim)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SECRET-TOKEN-PATH")
	require.Contains(t, err.Error(), dead.URL, "the host is named so the failure is diagnosable")
}
