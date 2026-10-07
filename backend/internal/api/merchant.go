package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/connector"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
	"github.com/CornHead764/agentifi/backend/internal/totp"
)

// The merchant connectors' resource: /merchants/{merchant}/… for one merchant's
// accounts, files and orders, and /merchants/transactions/… for a bank row,
// whatever merchant stands behind it. An unknown merchant is not found, so a
// handler under {merchant} always has one. docs/connectors/merchants.md says
// where the files come from.

type merchantKey struct{}

func init() {
	Register(Resource{Prefix: "/merchants", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/transactions/{id}", merchantForTransaction)
		rt.Read(http.MethodGet, "/transactions/{id}/candidates", merchantOrderCandidates)
		rt.Write(http.MethodPost, "/transactions/{id}/orders", addMerchantMatch)
		rt.Write(http.MethodDelete, "/transactions/{id}/orders/{order_id}", removeMerchantMatch)

		rt.Read(http.MethodGet, "/{merchant}/accounts", perMerchant(listMerchantAccounts))
		rt.Write(http.MethodPost, "/{merchant}/accounts", perMerchant(createMerchantAccount))
		rt.Write(http.MethodPatch, "/{merchant}/accounts/{id}", perMerchant(updateMerchantAccount))
		rt.Write(http.MethodDelete, "/{merchant}/accounts/{id}", perMerchant(deleteMerchantAccount))
		rt.Read(http.MethodGet, "/{merchant}/agent", perMerchant(merchantAgentStatus))
		rt.Write(http.MethodPost, "/{merchant}/accounts/{id}/sign-in", perMerchant(startMerchantSignIn))
		rt.Read(http.MethodGet, "/{merchant}/accounts/{id}/sign-in/{session}", perMerchant(merchantSignInStatus))
		rt.Write(http.MethodPost, "/{merchant}/accounts/{id}/sign-in/{session}/answer", perMerchant(merchantSignIn.answerStep))
		rt.Write(http.MethodPost, "/{merchant}/accounts/{id}/sign-in/{session}/mailed-code", perMerchant(merchantSignIn.answerFromMail))
		rt.Write(http.MethodPost, "/{merchant}/accounts/{id}/sign-in/{session}/complete", perMerchant(completeMerchantSignIn))
		rt.Write(http.MethodDelete, "/{merchant}/accounts/{id}/session", perMerchant(forgetMerchantSession))
		rt.Write(http.MethodDelete, "/{merchant}/accounts/{id}/credential", perMerchant(merchantSignIn.forgetCredential))
		rt.Write(http.MethodPost, "/{merchant}/accounts/{id}/pull", perMerchant(pullMerchantAccount))
		rt.Read(http.MethodGet, "/{merchant}/accounts/{id}/failure-screenshot", perMerchant(merchantFailureScreenshot))
		rt.Read(http.MethodGet, "/{merchant}/accounts/{id}/backfill", perMerchant(merchantBackfillWanting))
		rt.Write(http.MethodPost, "/{merchant}/accounts/{id}/backfill", perMerchant(startMerchantBackfill))
		rt.Write(http.MethodPost, "/{merchant}/imports", perMerchant(importMerchantFile))
		rt.Read(http.MethodGet, "/{merchant}/orders", perMerchant(listMerchantOrders))
		rt.Write(http.MethodPatch, "/{merchant}/orders/{id}", perMerchant(updateMerchantOrder))
		rt.Read(http.MethodGet, "/{merchant}/orders/{id}/candidates", perMerchant(merchantMatchCandidates))
		rt.Read(http.MethodGet, "/{merchant}/summary", perMerchant(merchantSummary))
		rt.Write(http.MethodPost, "/{merchant}/match", perMerchant(matchMerchant))
		rt.Write(http.MethodPost, "/{merchant}/suggest-categories", perMerchant(suggestMerchantCategories))
	}})
}

// perMerchant resolves the {merchant} segment before the handler runs, and
// refuses a merchant the catalog does not know.
func perMerchant(h SpaceHandler) SpaceHandler {
	return func(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
		merchant, ok := domain.MerchantByID(domain.MerchantID(chi.URLParam(r, "merchant")))
		if !ok {
			return errNotFound("Merchant")
		}
		return h(env, w, r.WithContext(context.WithValue(r.Context(), merchantKey{}, merchant)), sp)
	}
}

// merchantOf is the merchant a request is about, from its path. Only routes
// under {merchant} have one.
func merchantOf(r *http.Request) domain.Merchant {
	if m, ok := r.Context().Value(merchantKey{}).(domain.Merchant); ok {
		return m
	}
	return domain.Merchants[0]
}

type MerchantAccountResponse struct {
	ID       uuid.UUID `json:"id"`
	Merchant string    `json:"merchant"`
	// Label is the name somebody gave the account, and may be empty; Name is
	// what to show: the label, else the sign-in email, else the merchant's.
	Label  string `json:"label"`
	Name   string `json:"name"`
	Orders int    `json:"orders"`
	// Email is the login used to sign in.
	Email string `json:"email"`
	// Connected says a browser session is on file; the daily pull needs one.
	Connected  bool       `json:"connected"`
	SignedInAt *time.Time `json:"signed_in_at"`
	// HasPassword and HasTOTP say a password, and an authenticator setup key
	// beside it, are kept. Only ever whether: neither leaves the server.
	HasPassword bool `json:"has_password"`
	HasTOTP     bool `json:"has_totp"`
	// SecondFactor is how the household chose to answer this login's second
	// factor: "", "email", "sms" or "totp". Never the key.
	SecondFactor string `json:"second_factor"`
	// SignInPausedFor is why a pull stopped signing in with the kept
	// password on its own: password_refused, code_needed, or "" for no pause.
	SignInPausedFor string `json:"sign_in_paused"`
	// SyncEnabled and SyncDays are the daily pull's switch and reach.
	SyncEnabled bool `json:"sync_enabled"`
	SyncDays    int  `json:"sync_days"`
	// The last pull: when, how it ended ("", ok, needs_sign_in, failed), why.
	LastSyncedAt   *time.Time `json:"last_synced_at"`
	LastSyncStatus string     `json:"last_sync_status"`
	LastSyncError  string     `json:"last_sync_error"`
	// HasFailureScreenshot says /failure-screenshot has the page the last pull
	// failed on.
	HasFailureScreenshot bool `json:"has_failure_screenshot"`
	NeedsSignIn          bool `json:"needs_sign_in"`
	// Pulling says a pull of this account is running now. The last_sync fields
	// describe the one before until it finishes.
	Pulling bool `json:"pulling"`
	// Backfill is the invoice backfill running now, else the last one to
	// finish; nil when there has been none.
	Backfill *MerchantBackfillResponse `json:"backfill"`
	// The gift card balance, once a pull has read one: the household account
	// that holds it, the figure, and when it was read.
	GiftCardAccountID *uuid.UUID `json:"gift_card_account_id"`
	GiftCardBalance   *string    `json:"gift_card_balance"`
	GiftCardBalanceAt *time.Time `json:"gift_card_balance_at"`
	CreatedAt         time.Time  `json:"created_at"`
}

type MerchantAccountRequest struct {
	Label       *string `json:"label"`
	SyncEnabled *bool   `json:"sync_enabled"`
}

// MerchantAgentResponse says whether a browser is there to sign in with.
// Unavailable is why this merchant cannot be signed in to although the engine
// is there, "" when it can.
type MerchantAgentResponse struct {
	Configured  bool   `json:"configured"`
	Reachable   bool   `json:"reachable"`
	Detail      string `json:"detail"`
	Unavailable string `json:"unavailable"`
}

// MerchantSignInRequest is the login. The password goes to the agent's browser
// and is sealed onto the account once the sign-in lands.
type MerchantSignInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// TOTPSecret is the authenticator setup key, sealed with the password.
	TOTPSecret string `json:"totp_secret"`
	// SecondFactor is "" (none or not sure), "email" (a code the mailbox
	// reads), "sms" (a text the person reads into the dialog) or "totp" (the
	// authenticator above). Kept on the account.
	SecondFactor string `json:"second_factor"`
}

// MerchantSignInResponse is where the sign-in stands.
type MerchantSignInResponse struct {
	SessionID string `json:"session_id"`
	// State is signed_in, otp, captcha, approval or failed.
	State  string `json:"state"`
	Prompt string `json:"prompt"`
	// Image is a PNG, base64, for a CAPTCHA or a failure worth seeing.
	Image string `json:"image"`
	Error string `json:"error"`
}

type MerchantOrderItemResponse struct {
	// SKU is the merchant's item id: an ASIN, a Costco item number.
	SKU       string  `json:"sku"`
	Title     string  `json:"title"`
	Quantity  int     `json:"quantity"`
	UnitPrice *string `json:"unit_price"`
	TotalOwed *string `json:"total_owed"`
	ShippedOn *string `json:"shipped_on"`
	Condition string  `json:"condition"`
	URL       string  `json:"url"`
	// Catalog is what the merchant's own listing says the number is, when
	// it has been found: the full name behind a receipt's abbreviation.
	Catalog *MerchantCatalogItemResponse `json:"catalog"`
}

// MerchantRefundResponse is one return the merchant's records show: what came
// back out of an order, and where the money went.
type MerchantRefundResponse struct {
	ID uuid.UUID `json:"id"`
	// SKU and Title name the line that came back; both are empty for a
	// refund of the whole order.
	SKU        string `json:"sku"`
	Title      string `json:"title"`
	Quantity   int    `json:"quantity"`
	RefundedOn string `json:"refunded_on"`
	// Amount is positive: money coming back.
	Amount     string `json:"amount"`
	Instrument string `json:"instrument"`
	// ToGiftCard says the money went to a gift card balance, so no bank row
	// will ever explain it and none is waiting for one.
	ToGiftCard bool   `json:"to_gift_card"`
	Status     string `json:"status"`
	// TransactionID is the bank row matched to this refund, when one has been.
	TransactionID *uuid.UUID `json:"transaction_id"`
}

type MerchantCatalogItemResponse struct {
	Title    string `json:"title"`
	Brand    string `json:"brand"`
	Size     string `json:"size"`
	Category string `json:"category"`
	ImageURL string `json:"image_url"`
	URL      string `json:"url"`
}

type MerchantOrderResponse struct {
	ID                uuid.UUID `json:"id"`
	MerchantAccountID uuid.UUID `json:"merchant_account_id"`
	AccountLabel      string    `json:"account_label"`
	// Merchant is whose order this is; an order behind a bank row may be
	// either merchant's.
	Merchant    string `json:"merchant"`
	OrderNumber string `json:"order_number"`
	// Kind is online, warehouse or fuel; Location is the warehouse for the
	// latter two and empty for an online order.
	Kind       string                      `json:"kind"`
	Location   string                      `json:"location"`
	OrderedOn  string                      `json:"ordered_on"`
	Total      string                      `json:"total"`
	Currency   string                      `json:"currency"`
	Status     string                      `json:"status"`
	DetailsURL string                      `json:"details_url"`
	Source     string                      `json:"source"`
	Items      []MerchantOrderItemResponse `json:"items"`
	// Refunds is what the order gave back, card and gift card alike.
	Refunds               []MerchantRefundResponse `json:"refunds"`
	MatchedTransactionIDs []uuid.UUID              `json:"matched_transaction_ids"`
	// Matched carries the same bank rows with what a person needs to
	// recognise each.
	Matched []MerchantMatchedTransactionResponse `json:"matched_transactions"`
	// CardTotal is what a card was charged: the total less what a gift card
	// paid. GiftCard is null until a pull has read the invoice.
	CardTotal      string  `json:"card_total"`
	GiftCard       *string `json:"gift_card_amount"`
	Tax            *string `json:"tax"`
	PaidByGiftCard bool    `json:"paid_by_gift_card"`
	Cancelled      bool    `json:"cancelled"`
	// Ignored says a person marked the order as one no bank row will
	// explain: charged to a card Agentifi does not track.
	Ignored bool `json:"ignored"`
}

// MerchantOrderRequest is what a person can change about an order on file.
type MerchantOrderRequest struct {
	Ignored *bool `json:"ignored"`
}

type MerchantMatchedTransactionResponse struct {
	ID            uuid.UUID `json:"id"`
	AccountID     uuid.UUID `json:"account_id"`
	Date          string    `json:"date"`
	Amount        string    `json:"amount"`
	Payee         string    `json:"payee"`
	StatementName string    `json:"statement_name"`
	AccountName   string    `json:"account_name"`
	Basis         string    `json:"basis"`
	Confidence    float64   `json:"confidence"`
}

type MerchantOrderList struct {
	Orders []MerchantOrderResponse `json:"orders"`
	Total  int                     `json:"total"`
}

type MerchantImportResponse struct {
	Format      string `json:"format"`
	DryRun      bool   `json:"dry_run"`
	AccountHint string `json:"account_hint"`
	Orders      int    `json:"orders"`
	NewOrders   int    `json:"new_orders"`
	Items       int    `json:"items"`
	Charges     int    `json:"charges"`
	NewCharges  int    `json:"new_charges"`
	Refunds     int    `json:"refunds"`
	NewRefunds  int    `json:"new_refunds"`
	Matched     int    `json:"matched"`
	// GiftCardRows is how many lines of the gift card balance were new.
	GiftCardRows int      `json:"gift_card_rows"`
	Warnings     []string `json:"warnings"`
}

type MerchantSummaryResponse struct {
	Accounts             int     `json:"accounts"`
	Orders               int     `json:"orders"`
	Items                int     `json:"items"`
	Charges              int     `json:"charges"`
	MerchantTransactions int     `json:"merchant_transactions"`
	MatchedTransactions  int     `json:"matched_transactions"`
	NewestOrder          *string `json:"newest_order"`
	OldestOrder          *string `json:"oldest_order"`
}

// MerchantMatchResponse is the order behind one bank row — or the orders, when
// one payment settled several: Order is the first and Orders is all of them,
// each with its own share of the row.
type MerchantMatchResponse struct {
	TransactionID uuid.UUID                      `json:"transaction_id"`
	Amount        string                         `json:"amount"`
	Basis         string                         `json:"basis"`
	Confidence    float64                        `json:"confidence"`
	Order         MerchantOrderResponse          `json:"order"`
	Orders        []MerchantMatchedOrderResponse `json:"orders"`
	// Refund is the return this credit gives back, when matched to one. Null
	// for a purchase.
	Refund *MerchantRefundResponse `json:"refund"`
}

// MerchantMatchedOrderResponse is one of a row's orders with its share of the row.
type MerchantMatchedOrderResponse struct {
	Amount string                `json:"amount"`
	Basis  string                `json:"basis"`
	Order  MerchantOrderResponse `json:"order"`
}

type MerchantMatchRequest struct {
	OrderID uuid.UUID `json:"order_id"`
}

// MerchantMatchCandidateResponse is a bank row a person might match to an
// order by hand. MatchedOrderNumber names the order the row already explains,
// when it does; matching it here moves it.
type MerchantMatchCandidateResponse struct {
	ID                 uuid.UUID `json:"id"`
	AccountID          uuid.UUID `json:"account_id"`
	AccountName        string    `json:"account_name"`
	Date               string    `json:"date"`
	Amount             string    `json:"amount"`
	Payee              string    `json:"payee"`
	StatementName      string    `json:"statement_name"`
	IsPending          bool      `json:"is_pending"`
	MatchedOrderID     *string   `json:"matched_order_id"`
	MatchedOrderNumber string    `json:"matched_order_number"`
}

type MerchantMatchCandidateList struct {
	Candidates []MerchantMatchCandidateResponse `json:"candidates"`
}

// MerchantOrderCandidateList is the orders a bank row might be matched to by
// hand, nearest in amount first; each carries the rows it already pays for.
type MerchantOrderCandidateList struct {
	Candidates []MerchantOrderResponse `json:"candidates"`
}

// MerchantPullRequest asks the pull to reach further back than the account's
// setting: the household's history, not just the month.
type MerchantPullRequest struct {
	Days int `json:"days"`
}

func merchantService(env *Env) *service.Merchants { return NewMerchants(env) }

// NewMerchants builds the connector over this environment, for serve's
// scheduler and every handler.
func NewMerchants(env *Env) *service.Merchants {
	st := env.DB
	if sealed, err := sealedStore(env); err == nil {
		st = sealed
	}
	merchants := service.NewMerchants(st)
	if automations, err := env.automations(); err == nil {
		merchants.Automations = automations
	}
	merchants.Agent = env.merchantEngine()
	merchants.Catalog = env.Catalog
	merchants.Documents = env.documents()
	merchants.Alerts = newAlerts(env)
	merchants.MailedCode = func(
		ctx context.Context, spaceID store.SpaceID, merchant domain.MerchantID, since time.Time,
	) (string, bool) {
		return NewMailbox(env).WaitForMerchantCode(ctx, spaceID, merchant, since)
	}
	merchants.HasMailbox = func(ctx context.Context, spaceID store.SpaceID) bool {
		return NewMailbox(env).HasReadableMailbox(ctx, spaceID)
	}
	return merchants
}

func (e *Env) merchantEngine() service.MerchantAgent {
	if e.MerchantAgent != nil {
		return e.MerchantAgent
	}
	return connector.Merchants{Engine: e.connectorEngine()}
}

func merchantAccountResponse(one store.MerchantAccount, orders int) MerchantAccountResponse {
	out := MerchantAccountResponse{
		ID: one.ID, Merchant: string(one.Merchant), Label: one.Label, Name: one.Name(),
		Orders: orders, Email: one.Email,
		Connected:  one.HasSession,
		SignedInAt: one.SignedInAt, HasPassword: one.HasPassword, HasTOTP: one.HasTOTP,
		SecondFactor:    string(one.SecondFactor),
		SignInPausedFor: one.SignInPausedFor,
		SyncEnabled:     one.SyncEnabled, SyncDays: one.SyncDays,
		LastSyncedAt: one.LastSyncedAt, LastSyncStatus: one.LastSyncStatus,
		LastSyncError: one.LastSyncError, HasFailureScreenshot: one.HasFailureScreenshot,
		NeedsSignIn: one.NeedsSignIn,
		Pulling:     service.MerchantPullRunning(one.ID),
		CreatedAt:   one.CreatedAt}
	out.GiftCardAccountID = pgconv.NullUUID(one.GiftCardAccountID)
	if one.HasGiftCardBalance {
		figure := one.GiftCardBalance.String()
		out.GiftCardBalance = &figure
		out.GiftCardBalanceAt = one.GiftCardBalanceAt
	}
	out.Backfill = merchantBackfillResponse(one)
	return out
}

// isUniqueViolation is Postgres saying a second row would duplicate a key.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func merchantOrderResponse(one store.MerchantOrder, labels map[uuid.UUID]string) MerchantOrderResponse {
	out := MerchantOrderResponse{
		ID: one.ID, MerchantAccountID: one.MerchantAccountID, AccountLabel: labels[one.MerchantAccountID],
		Merchant: string(one.Merchant), OrderNumber: one.OrderNumber, Kind: one.Kind, Location: one.Location,
		OrderedOn: one.OrderedOn.String(), Total: one.Total.String(),
		Currency: one.Currency, Status: one.Status, DetailsURL: one.DetailsURL, Source: one.Source,
		Items:                 []MerchantOrderItemResponse{},
		Refunds:               []MerchantRefundResponse{},
		MatchedTransactionIDs: []uuid.UUID{},
		Matched:               []MerchantMatchedTransactionResponse{},
		CardTotal:             one.CardTotal().String(),
		PaidByGiftCard:        one.Total.IsPositive() && one.CardTotal().IsZero(),
		Cancelled:             strings.HasPrefix(strings.ToLower(one.Status), "cancel"),
		Ignored:               one.Ignored(),
	}
	if one.HasGiftCard {
		s := one.GiftCard.String()
		out.GiftCard = &s
	}
	if one.HasTax {
		s := one.Tax.String()
		out.Tax = &s
	}
	if one.MatchedTransactionIDs != nil {
		out.MatchedTransactionIDs = one.MatchedTransactionIDs
	}
	for _, refund := range one.Refunds {
		out.Refunds = append(out.Refunds, merchantRefundResponse(refund))
	}
	for _, m := range one.Matched {
		out.Matched = append(out.Matched, MerchantMatchedTransactionResponse{
			ID: m.ID, AccountID: m.AccountID, Date: m.Date.String(), Amount: m.Amount.String(), Payee: m.Payee,
			StatementName: m.StatementName, AccountName: m.AccountName, Basis: m.Basis,
			Confidence: m.Confidence,
		})
	}
	for _, item := range one.Items {
		row := MerchantOrderItemResponse{
			SKU: item.SKU, Title: item.Title, Quantity: item.Quantity, Condition: item.Condition,
			URL: item.URL,
		}
		if item.HasUnitPrice {
			s := item.UnitPrice.String()
			row.UnitPrice = &s
		}
		if item.HasTotalOwed {
			s := item.TotalOwed.String()
			row.TotalOwed = &s
		}
		if item.Catalog != nil {
			row.Catalog = &MerchantCatalogItemResponse{
				Title: item.Catalog.Title, Brand: item.Catalog.Brand, Size: item.Catalog.Size,
				Category: item.Catalog.Category, ImageURL: item.Catalog.ImageURL, URL: item.Catalog.URL,
			}
		}
		if !item.ShippedOn.IsZero() {
			s := item.ShippedOn.String()
			row.ShippedOn = &s
		}
		out.Items = append(out.Items, row)
	}
	return out
}

func merchantRefundResponse(one store.MerchantRefund) MerchantRefundResponse {
	out := MerchantRefundResponse{
		ID: one.ID, SKU: one.SKU, Title: one.Title, Quantity: one.Quantity,
		RefundedOn: one.RefundedOn.String(), Amount: one.Amount.String(),
		Instrument: one.Instrument, ToGiftCard: one.ToGiftCard(), Status: one.Status,
	}
	if one.TransactionID != uuid.Nil {
		id := one.TransactionID
		out.TransactionID = &id
	}
	return out
}

func merchantLabels(r *http.Request, env *Env, sp auth.SpaceContext) (map[uuid.UUID]string, error) {
	labels := map[uuid.UUID]string{}
	for _, merchant := range domain.Merchants {
		accounts, err := env.DB.ListMerchantAccounts(r.Context(), sp.ID(), merchant.ID)
		if err != nil {
			return nil, err
		}
		for _, one := range accounts {
			labels[one.ID] = one.Name()
		}
	}
	return labels, nil
}

// --- Accounts ----------------------------------------------------------------

func listMerchantAccounts(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	accounts, err := env.DB.ListMerchantAccounts(r.Context(), sp.ID(), merchantOf(r).ID)
	if err != nil {
		return err
	}
	counts, err := env.DB.CountMerchantOrdersByAccount(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	out := make([]MerchantAccountResponse, 0, len(accounts))
	for _, one := range accounts {
		out = append(out, merchantAccountResponse(one, counts[one.ID]))
	}
	return writeJSON(w, http.StatusOK, out)
}

func merchantLabelOf(r *http.Request, body MerchantAccountRequest) (string, error) {
	label := ""
	if body.Label != nil {
		label = strings.TrimSpace(*body.Label)
	}
	if len(label) > 80 {
		return "", errInvalid("too_long", []string{"body", "label"}, "Keep the label under 80 characters")
	}
	return label, nil
}

func createMerchantAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body MerchantAccountRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	label, err := merchantLabelOf(r, body)
	if err != nil {
		return err
	}
	one := store.MerchantAccount{Merchant: merchantOf(r).ID, Label: label}
	if err := env.DB.CreateMerchantAccount(r.Context(), sp.ID(), &one); err != nil {
		if isUniqueViolation(err) {
			return errConflict("There is already a %s account labelled %q", merchantOf(r).Name, label)
		}
		return err
	}
	one, err = env.DB.GetMerchantAccount(r.Context(), sp.ID(), one.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, merchantAccountResponse(one, 0))
}

func updateMerchantAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	var body MerchantAccountRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	one, err := ownMerchantAccount(env, r, sp, id)
	if err != nil {
		return err
	}
	if body.Label != nil {
		label, err := merchantLabelOf(r, body)
		if err != nil {
			return err
		}
		one.Label = label
		if err := env.DB.UpdateMerchantAccount(r.Context(), sp.ID(), &one); err != nil {
			if isUniqueViolation(err) {
				return errConflict("There is already a %s account labelled %q", merchantOf(r).Name, label)
			}
			return err
		}
	}
	if body.SyncEnabled != nil {
		if *body.SyncEnabled && !one.HasSession {
			return errConflict("Sign in to %s before switching the daily pull on", one.Name())
		}
		if err := env.DB.UpdateMerchantSync(r.Context(), sp.ID(), id, *body.SyncEnabled, one.SyncDays); err != nil {
			return err
		}
	}
	return writeMerchantAccount(env, w, r, sp, id)
}

func writeMerchantAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext, id uuid.UUID) error {
	out, err := merchantAccountOut(env, r, sp, id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func merchantAccountOut(env *Env, r *http.Request, sp auth.SpaceContext, id uuid.UUID) (MerchantAccountResponse, error) {
	one, err := ownMerchantAccount(env, r, sp, id)
	if err != nil {
		return MerchantAccountResponse{}, err
	}
	counts, err := env.DB.CountMerchantOrdersByAccount(r.Context(), sp.ID())
	if err != nil {
		return MerchantAccountResponse{}, err
	}
	return merchantAccountResponse(one, counts[one.ID]), nil
}

// ownMerchantAccount loads an account under the merchant the request is
// about; one that belongs to another merchant is not found here.
func ownMerchantAccount(env *Env, r *http.Request, sp auth.SpaceContext, id uuid.UUID) (store.MerchantAccount, error) {
	one, err := env.DB.GetMerchantAccount(r.Context(), sp.ID(), id)
	if err != nil {
		return one, notFoundAs(err, merchantOf(r).Name+" account")
	}
	if one.Merchant != merchantOf(r).ID {
		return one, errNotFound(merchantOf(r).Name + " account")
	}
	return one, nil
}

// ownMerchantOrder loads an order under the merchant the request is about;
// another merchant's order is not found here.
func ownMerchantOrder(env *Env, r *http.Request, sp auth.SpaceContext, id uuid.UUID) (store.MerchantOrder, error) {
	order, err := env.DB.GetMerchantOrder(r.Context(), sp.ID(), id)
	if err != nil {
		return order, notFoundAs(err, merchantOf(r).Name+" "+merchantOf(r).Noun)
	}
	if order.Merchant != merchantOf(r).ID {
		return order, errNotFound(merchantOf(r).Name + " " + merchantOf(r).Noun)
	}
	return order, nil
}

// --- The browser: sign-in and the pull -------------------------------------------

func merchantAgentStatus(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	merchants := merchantService(env)
	out := MerchantAgentResponse{Configured: merchants.HasAgent()}
	if !out.Configured {
		out.Detail = "This build carries no browser engine. " +
			textutil.Capitalize(merchantOf(r).Plural) + " can still be imported from files."
		return writeJSON(w, http.StatusOK, out)
	}
	merchant := merchantOf(r)
	if err := merchants.Health(r.Context(), merchant.ID); errors.Is(err, browser.ErrNoFirefox) {
		out.Unavailable = merchant.Name + " runs only in Camoufox, and this server has no " +
			"CAMOUFOX_URL set, so signing in to " + merchant.Name + " is off."
	} else if err != nil {
		out.Detail = err.Error()
	} else {
		out.Reachable = true
	}
	return writeJSON(w, http.StatusOK, out)
}

func signInResponse(state provider.MerchantSignInState) MerchantSignInResponse {
	return MerchantSignInResponse{
		SessionID: state.SessionID, State: state.State, Prompt: state.Prompt, Image: state.Image,
		Error: state.Error,
	}
}

// merchantSignIn is the merchant accounts' half of the shared sign-in routes.
var merchantSignIn = signInConnector[provider.MerchantSignInState, MerchantSignInResponse]{
	nouns: merchantNouns,
	target: func(env *Env, r *http.Request, sp auth.SpaceContext) (uuid.UUID, error) {
		id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
		if err != nil {
			return uuid.Nil, err
		}
		_, err = ownMerchantAccount(env, r, sp, id)
		return id, err
	},
	answer: func(env *Env, ctx context.Context, space store.SpaceID, id uuid.UUID, session, code string) (provider.MerchantSignInState, error) {
		return merchantService(env).AnswerSignIn(ctx, space, id, session, code)
	},
	fromMail: func(env *Env, ctx context.Context, space store.SpaceID, id uuid.UUID, session string) (provider.MerchantSignInState, bool, error) {
		return service.AnswerFromMail[provider.MerchantSignInState](ctx, merchantService(env), space, id, session)
	},
	// The password and the authenticator key beside it go; the session stays.
	forget: func(env *Env, ctx context.Context, space store.SpaceID, id uuid.UUID) error {
		return merchantService(env).ForgetPassword(ctx, space, id)
	},
	state: signInResponse,
	mailed: func(state MerchantSignInResponse, found bool) any {
		return MerchantMailedCodeResponse{MerchantSignInResponse: state, MailedCodeFound: found}
	},
	written: writeMerchantAccount,
}

func merchantNouns(r *http.Request) agentNouns {
	name := merchantOf(r).Name
	return agentNouns{
		SignIn:     name + " sign-in",
		Connection: name + " account",
		Unavailable: "This build carries no browser engine, so Agentifi cannot sign in to " +
			name + ". Import files instead",
	}
}

func agentError(r *http.Request, err error) error { return connectorAgentError(err, merchantNouns(r)) }

func startMerchantSignIn(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	if _, err := ownMerchantAccount(env, r, sp, id); err != nil {
		return err
	}
	var body MerchantSignInRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	body.Email = strings.TrimSpace(body.Email)
	if body.Email == "" || body.Password == "" {
		return errInvalid("missing", []string{"body", "email"},
			"The %s email and password are both needed", merchantOf(r).Name)
	}
	secret := totp.Normalize(body.TOTPSecret)
	if secret != "" && !totp.Valid(secret) {
		return errInvalid("invalid", []string{"body", "totp_secret"},
			"the authenticator secret is not a base32 setup key")
	}
	factor := domain.SecondFactor(strings.TrimSpace(body.SecondFactor))
	if !factor.Valid() {
		return errInvalid("invalid", []string{"body", "second_factor"},
			"the second factor is none, email, sms or totp")
	}
	state, err := merchantService(env).StartSignIn(r.Context(), sp.ID(), id,
		body.Email, body.Password, secret, factor)
	if err != nil {
		return agentError(r, err)
	}
	return writeJSON(w, http.StatusOK, signInResponse(state))
}

func merchantSignInStatus(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	state, err := merchantService(env).SignInStatus(r.Context(), sp.ID(), id, chi.URLParam(r, "session"))
	if err != nil {
		return agentError(r, err)
	}
	return writeJSON(w, http.StatusOK, signInResponse(state))
}

// MerchantMailedCodeResponse is the sign-in state after the mailbox was
// watched for its code, and whether one came.
type MerchantMailedCodeResponse struct {
	MerchantSignInResponse
	MailedCodeFound bool `json:"mailed_code_found"`
}

func completeMerchantSignIn(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	// The email is optional here: the sign-in already carried it, and a
	// client that just wants the session kept may send nothing.
	var body MerchantSignInRequest
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			return err
		}
	}
	merchants := merchantService(env)
	if err := merchants.CompleteSignIn(r.Context(), sp.ID(), id, chi.URLParam(r, "session"),
		strings.TrimSpace(body.Email)); err != nil {
		return agentError(r, err)
	}
	// Says pulling whenever a pull started, even one already finished; the
	// dialog reads the outcome either way.
	started := merchants.PullAfterSignIn(sp.ID(), id)
	out, err := merchantAccountOut(env, r, sp, id)
	if err != nil {
		return err
	}
	out.Pulling = out.Pulling || started
	return writeJSON(w, http.StatusOK, out)
}

func forgetMerchantSession(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	if _, err := ownMerchantAccount(env, r, sp, id); err != nil {
		return err
	}
	if err := merchantService(env).Forget(r.Context(), sp.ID(), id); err != nil {
		return notFoundAs(err, merchantOf(r).Name+" account")
	}
	return writeMerchantAccount(env, w, r, sp, id)
}

// merchantFailureScreenshot is the page the account's last pull failed on.
func merchantFailureScreenshot(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	one, err := ownMerchantAccount(env, r, sp, id)
	if err != nil {
		return err
	}
	shot, err := env.DB.MerchantSyncScreenshot(r.Context(), sp.ID(), one.ID)
	if err != nil {
		return notFoundAs(err, "Failure screenshot")
	}
	return writeFailureScreenshot(w, shot)
}

// pullMerchantAccount runs the daily pull now and answers with the import report.
func pullMerchantAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	if _, err := ownMerchantAccount(env, r, sp, id); err != nil {
		return err
	}
	var body MerchantPullRequest
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			return err
		}
	}
	if body.Days < 0 || body.Days > 3650 {
		return errInvalid("range", []string{"body", "days"}, "days must be between 1 and 3650")
	}
	report, err := merchantService(env).Pull(r.Context(), sp.ID(), id, body.Days)
	if _, backfilling := service.MerchantBackfillRunning(id); backfilling && errors.Is(err, service.ErrPullRunning) {
		return errConflict("%s", errBackfillRunning)
	}
	if err != nil {
		refused := agentError(r, err)
		if errors.Is(err, service.ErrMerchantNeedsSignIn) {
			refused = errConflict("%s needs a sign-in. Sign in under this account to resume the pull",
				merchantOf(r).Name)
		}
		var stopped *service.PullStopped
		if errors.As(err, &stopped) && stopped.Screenshot {
			refused = screenshotKept{refused}
		}
		return refused
	}
	warnings := report.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return writeJSON(w, http.StatusOK, MerchantImportResponse{
		Format: report.Format, AccountHint: report.AccountHint, Orders: report.Orders,
		NewOrders: report.NewOrders, Items: report.Items, Charges: report.Charges,
		NewCharges: report.NewCharges, Refunds: report.Refunds, NewRefunds: report.NewRefunds,
		Matched: report.Matched, GiftCardRows: report.GiftCardRows,
		Warnings: warnings,
	})
}

func deleteMerchantAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	if _, err := ownMerchantAccount(env, r, sp, id); err != nil {
		return err
	}
	if err := env.DB.DeleteMerchantAccount(r.Context(), sp.ID(), id); err != nil {
		return notFoundAs(err, merchantOf(r).Name+" account")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// --- Imports -------------------------------------------------------------------

// maxMerchantImportBytes caps an upload. Years of orders as Amazon's CSV is a
// few megabytes.
const maxMerchantImportBytes = 64 << 20

func importMerchantFile(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if err := parseUpload(w, r, "file", maxMerchantImportBytes); err != nil {
		return err
	}
	accountID, err := uuid.Parse(r.FormValue("merchant_account_id"))
	if err != nil {
		return errInvalid("missing", []string{"form", "merchant_account_id"},
			"Choose which %s account this file is from", merchantOf(r).Name)
	}
	if _, err := ownMerchantAccount(env, r, sp, accountID); err != nil {
		return err
	}
	file, err := requireUpload(r, "file", maxMerchantImportBytes)
	if err != nil {
		return err
	}
	parsed, err := merchantimport.Parse(merchantOf(r).ID, file.Bytes)
	if err != nil {
		return errBadRequest("%s could not be read: %s", file.Filename, err)
	}
	dryRun := r.FormValue("dry_run") == "true"
	report, err := merchantService(env).Import(r.Context(), sp.ID(), accountID, parsed, dryRun)
	if err != nil {
		return notFoundAs(err, merchantOf(r).Name+" account")
	}
	warnings := report.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return writeJSON(w, http.StatusOK, MerchantImportResponse{
		Format: report.Format, DryRun: dryRun, AccountHint: report.AccountHint,
		Orders: report.Orders, NewOrders: report.NewOrders, Items: report.Items,
		Charges: report.Charges, NewCharges: report.NewCharges, Refunds: report.Refunds,
		NewRefunds: report.NewRefunds, Matched: report.Matched,
		GiftCardRows: report.GiftCardRows,
		Warnings:     warnings,
	})
}

// --- Orders and matches ------------------------------------------------------

func listMerchantOrders(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	limit, err := queryInt(r, "limit", 50, 1, 500)
	if err != nil {
		return err
	}
	offset, err := queryInt(r, "offset", 0, 0, 1<<30)
	if err != nil {
		return err
	}
	query := store.MerchantOrderQuery{Merchant: merchantOf(r).ID, Limit: limit, Offset: offset}
	if query.Unmatched, _, err = queryBool(r, "unmatched"); err != nil {
		return err
	}
	if account, present, err := queryUUID(r, "account_id"); err != nil {
		return err
	} else if present {
		query.AccountID = account
	}
	orders, total, err := env.DB.ListMerchantOrders(r.Context(), sp.ID(), query)
	if err != nil {
		return err
	}
	labels, err := merchantLabels(r, env, sp)
	if err != nil {
		return err
	}
	out := MerchantOrderList{Orders: make([]MerchantOrderResponse, 0, len(orders)), Total: total}
	for _, one := range orders {
		out.Orders = append(out.Orders, merchantOrderResponse(one, labels))
	}
	return writeJSON(w, http.StatusOK, out)
}

func updateMerchantOrder(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" "+merchantOf(r).Noun)
	if err != nil {
		return err
	}
	var body MerchantOrderRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Ignored == nil {
		return errInvalid("missing", []string{"body", "ignored"}, "Say whether the order is ignored")
	}
	if _, err := ownMerchantOrder(env, r, sp, id); err != nil {
		return err
	}
	order, err := merchantService(env).IgnoreOrder(r.Context(), sp.ID(), id, *body.Ignored)
	if err != nil {
		return notFoundAs(err, merchantOf(r).Name+" "+merchantOf(r).Noun)
	}
	orders := []store.MerchantOrder{order}
	if err := env.DB.AttachMerchantMatches(r.Context(), orders); err != nil {
		return err
	}
	labels, err := merchantLabels(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, merchantOrderResponse(orders[0], labels))
}

// merchantMatchCandidates offers the bank rows a person might match to an
// order by hand: the rows in the weeks after it was placed, the likeliest
// first, or with ?q= any row worded like the search.
func merchantMatchCandidates(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" "+merchantOf(r).Noun)
	if err != nil {
		return err
	}
	order, err := ownMerchantOrder(env, r, sp, id)
	if err != nil {
		return err
	}
	limit, err := queryInt(r, "limit", 25, 1, 200)
	if err != nil {
		return err
	}
	found, err := merchantService(env).RowCandidates(r.Context(), sp.ID(), order, r.URL.Query().Get("q"), limit)
	if err != nil {
		return err
	}
	out := MerchantMatchCandidateList{Candidates: make([]MerchantMatchCandidateResponse, 0, len(found))}
	for _, one := range found {
		row := MerchantMatchCandidateResponse{
			ID: one.Transaction.ID, AccountID: one.Transaction.AccountID, AccountName: one.AccountName,
			Date: one.Transaction.Date.String(), Amount: one.Transaction.Amount.String(),
			Payee: one.Transaction.Payee, StatementName: one.Transaction.StatementName,
			IsPending: one.Transaction.IsPending, MatchedOrderNumber: one.MatchedOrderNumber,
		}
		if one.MatchedOrderID != uuid.Nil {
			s := one.MatchedOrderID.String()
			row.MatchedOrderID = &s
		}
		out.Candidates = append(out.Candidates, row)
	}
	return writeJSON(w, http.StatusOK, out)
}

func merchantSummary(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	summary, err := env.DB.MerchantSummary(r.Context(), sp.ID(), merchantOf(r).ID)
	if err != nil {
		return err
	}
	out := MerchantSummaryResponse{
		Accounts: summary.Accounts, Orders: summary.Orders, Items: summary.Items,
		Charges: summary.Charges, MerchantTransactions: summary.MerchantTransactions,
		MatchedTransactions: summary.MatchedTransactions,
	}
	if !summary.NewestOrder.IsZero() {
		s := summary.NewestOrder.String()
		out.NewestOrder = &s
	}
	if !summary.OldestOrder.IsZero() {
		s := summary.OldestOrder.String()
		out.OldestOrder = &s
	}
	return writeJSON(w, http.StatusOK, out)
}

func matchMerchant(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	matched, err := merchantService(env).MatchUnmatched(r.Context(), sp.ID(), merchantOf(r).ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]int{"matched": matched})
}

// suggestMerchantCategories fires the transaction automations at every bank row
// an order explains. Each run is a proposal; nothing changes until accepted.
func suggestMerchantCategories(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	automations, err := env.automations()
	if err != nil {
		return err
	}
	if err := requireTransactionAutomation(r, sp, automations); err != nil {
		return err
	}
	ids, err := env.DB.ListMatchedMerchantTransactionIDs(r.Context(), sp.ID(), merchantOf(r).ID)
	if err != nil {
		return err
	}
	queued, err := automations.EnqueueForRows(r.Context(), sp.ID(), ids, domain.AutomationFiredByManual, false)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, map[string]int{"rows": len(ids), "queued": queued})
}

func merchantMatchResponse(env *Env, r *http.Request, sp auth.SpaceContext, enrichment *service.MerchantEnrichment) (MerchantMatchResponse, error) {
	labels, err := merchantLabels(r, env, sp)
	if err != nil {
		return MerchantMatchResponse{}, err
	}
	out := MerchantMatchResponse{
		TransactionID: enrichment.Match.TransactionID, Amount: enrichment.Match.Amount.String(),
		Basis: enrichment.Match.Basis, Confidence: enrichment.Match.Confidence,
		Order:  merchantOrderResponse(enrichment.Order, labels),
		Orders: make([]MerchantMatchedOrderResponse, 0, len(enrichment.Orders)),
	}
	if enrichment.Refund != nil {
		refund := merchantRefundResponse(*enrichment.Refund)
		out.Refund = &refund
	}
	for i, order := range enrichment.Orders {
		out.Orders = append(out.Orders, MerchantMatchedOrderResponse{
			Amount: enrichment.Matches[i].Amount.String(), Basis: enrichment.Matches[i].Basis,
			Order: merchantOrderResponse(order, labels),
		})
	}
	return out, nil
}

// respondWithMerchantMatch answers with everything behind the row, which a
// by-hand match has just made sure exists whatever the row's wording.
func respondWithMerchantMatch(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext, id uuid.UUID) error {
	txn, err := env.DB.GetTransaction(r.Context(), sp.ID(), id)
	if err != nil {
		return err
	}
	enrichment, err := merchantService(env).EnrichmentFor(r.Context(), sp.ID(), txn)
	if err != nil {
		return err
	}
	if enrichment == nil {
		return errNotFound("Order for this transaction")
	}
	out, err := merchantMatchResponse(env, r, sp, enrichment)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func merchantForTransaction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	txn, err := fromPath(r, sp, "id", "Transaction", env.DB.GetTransaction)
	if err != nil {
		return err
	}
	enrichment, err := merchantService(env).EnrichmentFor(r.Context(), sp.ID(), txn)
	if err != nil {
		return err
	}
	if enrichment == nil {
		return errNotFound("Order for this transaction")
	}
	out, err := merchantMatchResponse(env, r, sp, enrichment)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// merchantOrderCandidates offers the orders a bank row might be, for a person
// matching from the register: the likeliest first, or with ?q= the orders
// whose number or items mention it.
func merchantOrderCandidates(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	txn, err := fromPath(r, sp, "id", "Transaction", env.DB.GetTransaction)
	if err != nil {
		return err
	}
	limit, err := queryInt(r, "limit", 25, 1, 200)
	if err != nil {
		return err
	}
	// The merchants the wording names, or every merchant for a row the wording
	// missed; ?merchant= narrows to one. A search reaches past the date window
	// but not to another merchant's orders.
	var merchants []domain.MerchantID
	if chosen, ok := domain.MerchantByID(domain.MerchantID(r.URL.Query().Get("merchant"))); ok {
		merchants = []domain.MerchantID{chosen.ID}
	} else {
		for _, m := range domain.MerchantsFor(txn.StatementName, txn.Payee) {
			merchants = append(merchants, m.ID)
		}
	}
	orders, err := merchantService(env).OrderCandidates(r.Context(), sp.ID(), merchants, txn, r.URL.Query().Get("q"), limit)
	if err != nil {
		return err
	}
	labels, err := merchantLabels(r, env, sp)
	if err != nil {
		return err
	}
	out := MerchantOrderCandidateList{Candidates: make([]MerchantOrderResponse, 0, len(orders))}
	for _, one := range orders {
		out.Candidates = append(out.Candidates, merchantOrderResponse(one, labels))
	}
	return writeJSON(w, http.StatusOK, out)
}

// addMerchantMatch says the row also pays for this order: one payment that
// settled several orders, each then carrying its own share of the row.
func addMerchantMatch(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", "Transaction")
	if err != nil {
		return err
	}
	var body MerchantMatchRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.OrderID == uuid.Nil {
		return errInvalid("missing", []string{"body", "order_id"}, "Name the order this row also pays for")
	}
	if err := merchantService(env).AddMatchByHand(r.Context(), sp.ID(), id, body.OrderID); err != nil {
		return notFoundAs(err, "Transaction or order")
	}
	return respondWithMerchantMatch(env, w, r, sp, id)
}

// removeMerchantMatch releases the row from one order and leaves the rest.
func removeMerchantMatch(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", "Transaction")
	if err != nil {
		return err
	}
	orderID, err := pathUUID(r, "order_id", "Order")
	if err != nil {
		return err
	}
	if err := merchantService(env).RemoveMatch(r.Context(), sp.ID(), id, orderID); err != nil {
		return notFoundAs(err, "Match")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// merchantSection is the order behind a run's subject, as the model reads it —
// or the orders, when one payment settled several, every item of every
// order listed with its share of the row.
func merchantSection(enrichment *service.MerchantEnrichment) map[string]any {
	shares := enrichment.Shares()
	items := make([]map[string]any, 0, len(enrichment.Items()))
	orders := make([]map[string]any, 0, len(enrichment.Orders))
	at := 0
	for _, order := range enrichment.Orders {
		summary := map[string]any{
			"order_number": order.OrderNumber, "ordered_on": order.OrderedOn.String(),
			"order_total": order.Total.String(),
		}
		if order.Kind != "" && order.Kind != domain.PurchaseOnline {
			summary["kind"] = order.Kind
			summary["location"] = order.Location
		}
		if order.HasGiftCard && order.GiftCard.IsPositive() {
			summary["paid_by_gift_card"] = order.GiftCard.String()
			summary["charged_to_card"] = order.CardTotal().String()
		}
		orders = append(orders, summary)
		for _, item := range order.Items {
			row := map[string]any{"title": item.DisplayTitle(), "quantity": item.Quantity}
			if item.Catalog != nil {
				if item.Title != item.DisplayTitle() {
					row["receipt_title"] = item.Title
				}
				if item.Catalog.Brand != "" {
					row["brand"] = item.Catalog.Brand
				}
				if item.Catalog.Category != "" {
					row["category"] = item.Catalog.Category
				}
			}
			if len(enrichment.Orders) > 1 {
				row["order_number"] = order.OrderNumber
			}
			if item.SKU != "" {
				row["sku"] = item.SKU
			}
			if item.HasTotalOwed {
				row["cost"] = item.TotalOwed.String()
			} else if item.HasUnitPrice {
				row["unit_price"] = item.UnitPrice.String()
			}
			if shares != nil {
				row["share_of_charge"] = shares[at].String()
			}
			at++
			items = append(items, row)
		}
	}
	merchant := enrichment.Merchant()
	basis := "the amount " + merchant.Name + " charged the card agrees to the cent"
	switch enrichment.Match.Basis {
	case domain.MerchantMatchOrderTotal:
		basis = "the order total agrees to the cent"
	case domain.MerchantMatchShipment:
		basis = "one shipment of this order agrees to the cent; the order was charged in parts"
	case domain.MerchantMatchItem:
		basis = "one item of this order agrees to the cent; the order was charged per item"
	case domain.MerchantMatchManual:
		basis = "a person matched it"
	}
	out := map[string]any{
		"merchant":         merchant.Name,
		"merchant_account": enrichment.Account.Name(),
		"order_number":     enrichment.Order.OrderNumber,
		"ordered_on":       enrichment.Order.OrderedOn.String(),
		"order_total":      enrichment.Order.Total.String(),
		"matched_by":       basis,
		"confidence":       enrichment.Match.Confidence,
		"items":            items,
	}
	if enrichment.Order.Kind != "" && enrichment.Order.Kind != domain.PurchaseOnline {
		out["kind"] = enrichment.Order.Kind
		out["location"] = enrichment.Order.Location
	}
	if enrichment.Order.HasGiftCard && enrichment.Order.GiftCard.IsPositive() {
		out["paid_by_gift_card"] = enrichment.Order.GiftCard.String()
		out["charged_to_card"] = enrichment.Order.CardTotal().String()
	}
	if len(enrichment.Orders) > 1 {
		out["orders"] = orders
		out["covers_orders"] = fmt.Sprintf("This one payment settled %d orders; every item below "+
			"belongs to one of them, and items carries all of them together.", len(enrichment.Orders))
	}
	if refund := enrichment.Refund; refund != nil {
		returned := refund.Title
		if returned == "" {
			returned = "the whole order"
		}
		out["refund_of"] = returned
		out["refund_hint"] = "This row is money coming back, not income. It is already filed under " +
			"the category the purchase was filed under; leave it there unless the purchase moves."
	}
	switch {
	case len(items) == 1:
		out["single_item"] = "One item: the whole charge, tax and shipping included, is this item. " +
			"Do not split it; propose update_transaction with one category."
	case shares != nil:
		out["split_hint"] = "Each item's share_of_charge divides this charge exactly, tax and " +
			"shipping included. Group the items by category: items of the same category become one " +
			"split whose amount is the sum of its items' shares, and whose memo lists what is in it. " +
			"If every item is the same category, use update_transaction instead of splitting."
	}
	return out
}
