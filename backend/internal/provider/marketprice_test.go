package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestAnOrdinaryCurrencyPassesThrough(t *testing.T) {
	currency, price := NormalizeMinorUnits("USD", decimal.RequireFromString("101.25"))
	require.Equal(t, "USD", currency)
	require.Equal(t, "101.25", price.String())
}

func TestPenceBecomePounds(t *testing.T) {
	currency, price := NormalizeMinorUnits("GBp", decimal.RequireFromString("1234.5"))
	require.Equal(t, "GBP", currency)
	require.Equal(t, "12.345", price.String())
}

func TestSouthAfricanCentsBecomeRand(t *testing.T) {
	currency, price := NormalizeMinorUnits("ZAc", decimal.RequireFromString("8500"))
	require.Equal(t, "ZAR", currency)
	require.Equal(t, "85", price.String())
}

func TestTheMinorUnitConversionIsExact(t *testing.T) {
	// No float tail.
	_, price := NormalizeMinorUnits("GBp", decimal.RequireFromString("1064.7"))
	require.Equal(t, "10.647", price.String())
}

func yahooServer(t *testing.T, handler http.HandlerFunc) *YahooProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &YahooProvider{BaseURL: server.URL, HTTPClient: server.Client()}
}

func chartJSON(price, currency string) string {
	return fmt.Sprintf(`{"chart":{"result":[{"meta":{
		"currency":%q,"symbol":"AAPL","exchangeName":"NMS","fullExchangeName":"NasdaqGS",
		"instrumentType":"EQUITY","longName":"Apple Inc.","shortName":"Apple",
		"regularMarketPrice":%s,"chartPreviousClose":190.10}}],"error":null}}`, currency, price)
}

func TestAQuoteCarriesTheSourcesOwnDigits(t *testing.T) {
	// A float64 would round this to 1234.5678901234567.
	yahoo := yahooServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v8/finance/chart/AAPL", r.URL.Path)
		fmt.Fprint(w, chartJSON("1234.56789012345678901", "USD"))
	})

	quote, err := yahoo.Quote(context.Background(), "aapl")
	require.NoError(t, err)
	require.NotNil(t, quote)
	require.Equal(t, "1234.56789012345678901", quote.Price.String())
	require.Equal(t, "AAPL", quote.Symbol)
	require.Equal(t, "USD", quote.Currency)
	require.Equal(t, "Apple Inc.", quote.Name)
	require.Equal(t, "NasdaqGS", quote.Exchange)
	require.Equal(t, "EQUITY", quote.QuoteType)
}

func TestALondonListingIsQuotedInPoundsNotPence(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, chartJSON("1064.7", "GBp"))
	})

	quote, err := yahoo.Quote(context.Background(), "IITU.L")
	require.NoError(t, err)
	require.Equal(t, "GBP", quote.Currency)
	require.Equal(t, "10.647", quote.Price.String())
}

func TestAtTheOpenTheQuoteFallsBackToThePreviousClose(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"chart":{"result":[{"meta":{"currency":"USD","symbol":"AAPL",
			"regularMarketPrice":null,"chartPreviousClose":190.10}}]}}`)
	})

	quote, err := yahoo.Quote(context.Background(), "AAPL")
	require.NoError(t, err)
	require.Equal(t, "190.1", quote.Price.String())
}

func TestAnUnknownSymbolIsAnAnswerNotAFailure(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	quote, err := yahoo.Quote(context.Background(), "NOSUCHTICKER")
	require.NoError(t, err)
	require.Nil(t, quote)
}

func TestARateLimitIsReportedSoTheCallerBacksOff(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := yahoo.Quote(context.Background(), "AAPL")
	require.ErrorIs(t, err, ErrRateLimited)
}

func TestASymbolWithoutAPriceIsAbsentRatherThanZero(t *testing.T) {
	// A zero would wipe the holding's value on the next refresh.
	yahoo := yahooServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/finance/chart/GHOST" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, chartJSON("42.5", "USD"))
	})

	prices, err := yahoo.LatestPrices(context.Background(), []string{"aapl", "GHOST", "AAPL", "  "})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	require.Equal(t, "42.5", prices["AAPL"].String())
	require.NotContains(t, prices, "GHOST")
}

func TestABatchOfNothingMakesNoRequest(t *testing.T) {
	yahoo := yahooServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request should have been made")
	})

	prices, err := yahoo.LatestPrices(context.Background(), []string{"", "   "})
	require.NoError(t, err)
	require.Empty(t, prices)
}

func TestSearchPrefersTheLongName(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/finance/search", r.URL.Path)
		require.Equal(t, "apple", r.URL.Query().Get("q"))
		require.Equal(t, "5", r.URL.Query().Get("quotesCount"))
		fmt.Fprint(w, `{"quotes":[
			{"symbol":"AAPL","shortname":"Apple","longname":"Apple Inc.","exchDisp":"NasdaqGS","quoteType":"equity"},
			{"symbol":"APLE","shortname":"Apple Hospitality","exchDisp":"NYSE","quoteType":"equity"},
			{"shortname":"no symbol at all"}]}`)
	})

	matches, err := yahoo.Search(context.Background(), "apple", 5)
	require.NoError(t, err)
	require.Len(t, matches, 2)
	require.Equal(t, SymbolMatch{Symbol: "AAPL", Name: "Apple Inc.", Exchange: "NasdaqGS", QuoteType: "EQUITY"}, matches[0])
	require.Equal(t, "Apple Hospitality", matches[1].Name)
}

func TestAnEmptyQuerySearchesNothing(t *testing.T) {
	yahoo := yahooServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request should have been made")
	})

	matches, err := yahoo.Search(context.Background(), "   ", 10)
	require.NoError(t, err)
	require.Empty(t, matches)
}
