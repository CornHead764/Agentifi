package api

import (
	"errors"
	"net/http"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
)

// Connecting an eBay account: eBay's own consent page, through the
// installation's eBay application, instead of a sign-in. The page ends on an
// eBay address carrying a one-time code, which the person pastes back here.

// EbayApp is the installation's eBay application as the connector takes it.
func EbayApp(cfg *config.Config) provider.EbayApp {
	if cfg == nil {
		return provider.EbayApp{}
	}
	return provider.EbayApp{
		Sandbox: cfg.Ebay.Sandbox(), AppID: cfg.Ebay.AppID, CertID: cfg.Ebay.CertID, RuName: cfg.Ebay.RuName,
	}
}

// EbayConsentResponse is where the person grants access.
type EbayConsentResponse struct {
	URL string `json:"url"`
	// Sandbox says the consent is for eBay's test marketplace.
	Sandbox bool `json:"sandbox"`
}

// EbayConnectRequest is what the person pasted back: the address eBay showed
// after they agreed, or its code alone.
type EbayConnectRequest struct {
	Address string `json:"address"`
}

func ebayConsent(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	if _, err := ownMerchantAccount(env, r, sp, id); err != nil {
		return err
	}
	consent, err := merchantService(env).EbayConsentURL(r.Context(), sp.ID(), id)
	if err != nil {
		return ebayConnectFailure(r, err)
	}
	return writeJSON(w, http.StatusOK, EbayConsentResponse{URL: consent, Sandbox: EbayApp(env.Cfg).Sandbox})
}

func connectEbay(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	if _, err := ownMerchantAccount(env, r, sp, id); err != nil {
		return err
	}
	var body EbayConnectRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	merchants := merchantService(env)
	if err := merchants.ConnectEbay(r.Context(), sp.ID(), id, body.Address); err != nil {
		return ebayConnectFailure(r, err)
	}
	// The first pull starts now, as after a sign-in.
	started := merchants.PullAfterSignIn(sp.ID(), id)
	out, err := merchantAccountOut(env, r, sp, id)
	if err != nil {
		return err
	}
	out.Pulling = out.Pulling || started
	return writeJSON(w, http.StatusOK, out)
}

func ebayConnectFailure(r *http.Request, err error) error {
	var refused service.EbayConnectRefused
	if errors.As(err, &refused) {
		return errConflict("%s", refused.Reason)
	}
	return agentError(r, err)
}
