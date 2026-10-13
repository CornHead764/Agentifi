package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Connecting an eBay account through eBay's consent page, against a token
// endpoint that answers what eBay's does. Every key, code and token is
// invented.

type ebayTokenEndpoint struct{ codes []string }

func (e *ebayTokenEndpoint) Do(req *http.Request) (*http.Response, error) {
	_ = req.ParseForm()
	e.codes = append(e.codes, req.PostForm.Get("code"))
	body := `{"access_token":"access","expires_in":7200,"refresh_token":"refresh","refresh_token_expires_in":47304000}`
	if req.PostForm.Get("code") == "stale" {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`))}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestAnEbayAccountIsConnectedByWhatItsConsentPageLandsOn(t *testing.T) {
	merchants, sealed, space, _ := merchantsWithAgent(t, &fakeMerchantAgent{})
	account := store.MerchantAccount{Merchant: domain.MerchantEbay, Label: "Alex"}
	require.NoError(t, sealed.CreateMerchantAccount(t.Context(), space, &account))

	_, err := merchants.EbayConsentURL(t.Context(), space, account.ID)
	require.ErrorAs(t, err, &EbayConnectRefused{}, "a server with no keys connects nothing")

	endpoint := &ebayTokenEndpoint{}
	merchants.Ebay = provider.EbayApp{AppID: "Example-App-PRD-0", CertID: "PRD-invented", RuName: "Example-RuName"}
	merchants.HTTP = endpoint
	consent, err := merchants.EbayConsentURL(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Contains(t, consent, "client_id=Example-App-PRD-0")

	require.ErrorAs(t, merchants.ConnectEbay(t.Context(), space, account.ID,
		"https://auth2.ebay.com/oauth2/ThirdPartyAuthSucessFailure?isAuthSuccessful=true&code=stale"),
		&EbayConnectRefused{}, "a used code says so")

	require.NoError(t, merchants.ConnectEbay(t.Context(), space, account.ID,
		"https://auth2.ebay.com/oauth2/ThirdPartyAuthSucessFailure?isAuthSuccessful=true&code=v%5E1.1%23fresh&expires_in=299"))
	require.Equal(t, []string{"stale", "v^1.1#fresh"}, endpoint.codes)

	now := accountNow(t, sealed, space, account)
	require.True(t, now.SyncEnabled, "a connection switches the daily pull on")
	kept, err := sealed.MerchantSession(t.Context(), space, account.ID)
	require.NoError(t, err)
	var session provider.EbaySession
	require.NoError(t, json.Unmarshal([]byte(kept), &session))
	require.Equal(t, provider.EbaySessionKind, session.Kind)
	require.Equal(t, "refresh", session.RefreshToken)
}

func TestOnlyAnEbayAccountIsConnectedThatWayAndAnEbayAccountNoOtherWay(t *testing.T) {
	merchants, sealed, space, amazon := merchantsWithAgent(t, &fakeMerchantAgent{})
	merchants.Ebay = provider.EbayApp{AppID: "a", CertID: "b", RuName: "c"}
	require.ErrorAs(t, merchants.ConnectEbay(t.Context(), space, amazon.ID, "code"), &EbayConnectRefused{})

	ebay := store.MerchantAccount{Merchant: domain.MerchantEbay}
	require.NoError(t, sealed.CreateMerchantAccount(t.Context(), space, &ebay))
	_, err := merchants.StartSignIn(t.Context(), space, ebay.ID, "alex@example.invalid", "invented", "", domain.SecondFactorAny)
	require.ErrorAs(t, err, &EbayConnectRefused{})
	require.ErrorAs(t, merchants.StartInvoiceBackfill(t.Context(), space, ebay.ID), &BackfillRefused{},
		"eBay's documents come with each pull")
}
