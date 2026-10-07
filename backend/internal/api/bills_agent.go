package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/connector"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/merchants"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/totp"
)

// The bills agent: a sign-in, a pull, a challenge. The routes are registered
// in bills.go.
//
// A credential crosses this file and is never stored by it. What is kept is
// the session the provider hands back, sealed row-bound by the store, which no
// response here has a field for.

// BillAgentResponse says whether the browser is there, and what it can reach.
// Providers is the engine's own list rather than the catalogue's.
type BillAgentResponse struct {
	Configured bool   `json:"configured"`
	Healthy    bool   `json:"healthy"`
	Error      string `json:"error"`
	// Providers is empty when the engine cannot answer, which Healthy says.
	Providers []provider.BillProvider `json:"providers"`
}

// BillSignInRequest is one step of a connect.
//
// Mode is "typed" (the engine types a username and password into the
// provider's form) or "live" (a developer drives the browser by hand through
// the frame and input routes); the settings page only sends typed. A typed
// password is always kept, sealed once the sign-in lands.
type BillSignInRequest struct {
	Mode string `json:"mode"`
	// Site is which deployment to sign in to, for a provider deployed per
	// customer. Kept on the connection before the sign-in starts, because it
	// is the address the browser is about to open.
	Site     string `json:"site"`
	Username string `json:"username"`
	Password string `json:"password"`
	TOTP     string `json:"totp"`
	// TOTPSecret is the authenticator setup key, sealed with the password so
	// an unattended pull can mint its own codes.
	TOTPSecret string `json:"totp_secret"`
	// SecondFactor is "" (none or not sure), "email" (a code the billing
	// mailbox reads), "sms" (a text the person reads into the dialog) or
	// "totp" (the authenticator above).
	SecondFactor string `json:"second_factor"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

// BillSignInState is where a connect stands. The provider is not on it: the
// connection knows its own biller.
type BillSignInState struct {
	SessionID string `json:"session_id"`
	// State is signing_in, interactive, email, password, otp, captcha,
	// approval, accounts, signed_in or failed. `signing_in` asks nothing;
	// Prompt says what the agent is at.
	State  string `json:"state"`
	Prompt string `json:"prompt"`
	// Image is a PNG, base64, for a CAPTCHA or a failure worth seeing.
	Image string `json:"image"`
	Error string `json:"error"`
	// Width and Height are the live browser's viewport, zero for a typed
	// sign-in.
	Width  int `json:"width"`
	Height int `json:"height"`
	// Method names the second factor an otp state is asking for: totp, sms,
	// email or push, where the page said which.
	Method string `json:"method"`
	// Accounts is what the login bills, for a provider that lists them during
	// the sign-in.
	Accounts []BillAccountChoice `json:"accounts"`
	// Trail is what the provider showed at each round, on a failed state only.
	Trail []BillTrailEntry `json:"trail"`
}

// BillAccountChoice is one billed account a connect turned up.
type BillAccountChoice struct {
	ExternalID   string `json:"external_id"`
	Label        string `json:"label"`
	MaskedNumber string `json:"masked_number"`
}

// BillTrailEntry is one round of a sign-in, or one page or sentence of a
// pull (step read, state page or note), as the engine saw it.
//
// Never what was typed: the household must be able to paste this to somebody,
// so no field values, no picture, no cookies.
type BillTrailEntry struct {
	At     time.Time          `json:"at"`
	Step   string             `json:"step"`
	State  string             `json:"state"`
	URL    string             `json:"url"`
	Title  string             `json:"title"`
	Form   BillSignInFormRead `json:"form"`
	Inputs map[string]int     `json:"inputs"`
	Error  string             `json:"error"`
	// Did is what the round did about the page, and Choices the menu a factor
	// round was offered with the one it took. Both are the provider's own
	// words off its own controls, which is what makes them safe here.
	Did     BillSignInAction   `json:"did"`
	Choices []BillSignInChoice `json:"choices"`
	Chose   string             `json:"chose"`
	// Forced says the round's press was taken past the page's own checks.
	Forced bool `json:"forced"`
	// Snapshot is the page's structure on the round that ended the sign-in
	// and on each page a pull read; "" on every other line. No value, no
	// secret-named attribute, and no long run of digits.
	Snapshot string `json:"snapshot"`
	// Note is a sentence about the round nothing above says: the login's
	// preferred second factor, when the menu did not offer it, or how a
	// button's press went when a plain click did not land, with Playwright's
	// own steps and elements by tag, id and class; on a pull's line, which
	// page it is or what the module did.
	Note string `json:"note"`
}

// BillSignInAction is what one round did to the page.
type BillSignInAction struct {
	Acted   bool   `json:"acted"`
	Pressed string `json:"pressed"`
	Words   string `json:"words"`
	Waited  bool   `json:"waited"`
	Changed bool   `json:"changed"`
	// Dismissed is the cookie banner the round declined before it typed.
	Dismissed string `json:"dismissed"`
}

// BillSignInChoice is one way to verify, as a factor page offered it. `kind`
// is "radio" for a control that selects, "button" or "link" for one that acts
// on its own.
type BillSignInChoice struct {
	Kind  string `json:"kind"`
	Words string `json:"words"`
}

// BillSignInFormRead is the page-reading the state was decided from.
type BillSignInFormRead struct {
	Password    bool `json:"password"`
	Username    bool `json:"username"`
	OTP         bool `json:"otp"`
	SignOutLink bool `json:"sign_out_link"`
}

// The developer steers' bodies and answers. Nothing in the app calls these;
// `docs/connectors/bills.md` says how a module author does.

// BillSignInSteerRequest sends the live browser to one of the provider's own
// pages. An address off that site is refused.
type BillSignInSteerRequest struct {
	URL string `json:"url"`
}

// BillSignInClickRequest presses one control: the words on it, or a selector
// naming it outright when the words do not.
type BillSignInClickRequest struct {
	Text     string `json:"text"`
	Selector string `json:"selector"`
}

// BillSignInDOMRequest reads the markup behind a selector.
type BillSignInDOMRequest struct {
	Selector string `json:"selector"`
	Limit    int    `json:"limit"`
}

// BillSignInSteerResponse is the page after a steer and what the steer set off
// on the wire.
type BillSignInSteerResponse struct {
	Provider string                   `json:"provider"`
	URL      string                   `json:"url"`
	Title    string                   `json:"title"`
	Text     string                   `json:"text"`
	Requests []BillSignInRequestLine  `json:"requests"`
	Download *BillSignInDownloadEntry `json:"download"`
	Opened   string                   `json:"opened"`
}

// BillSignInRequestLine is one call the steer made. Authorization is the
// scheme a same-site call carried and never the credential after it.
type BillSignInRequestLine struct {
	Method        string `json:"method"`
	URL           string `json:"url"`
	Status        int    `json:"status"`
	Type          string `json:"type"`
	Authorization string `json:"authorization,omitempty"`
	Body          string `json:"body,omitempty"`
	Response      string `json:"response,omitempty"`
}

// BillSignInDownloadEntry is a file the steer started, named and cancelled.
type BillSignInDownloadEntry struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

// BillSignInDOMResponse is the markup behind a selector, with no field values
// in it.
type BillSignInDOMResponse struct {
	Provider string                 `json:"provider"`
	URL      string                 `json:"url"`
	Count    int                    `json:"count"`
	Elements []BillSignInDOMElement `json:"elements"`
}

// BillSignInDOMElement is one match.
type BillSignInDOMElement struct {
	Tag        string            `json:"tag"`
	Attributes map[string]string `json:"attributes"`
	Text       string            `json:"text"`
	HTML       string            `json:"html"`
}

// BillSignInFetchResponse is what a call made by the signed-in page answered.
type BillSignInFetchResponse struct {
	Provider    string   `json:"provider"`
	URL         string   `json:"url"`
	Status      int      `json:"status"`
	ContentType string   `json:"content_type"`
	Length      int      `json:"length"`
	Text        string   `json:"text,omitempty"`
	Matches     []string `json:"matches,omitempty"`
}

// BillConnectionWithSubaccounts is the connection and what it turned out to
// bill, which is what a finished sign-in answers.
type BillConnectionWithSubaccounts struct {
	BillConnectionResponse
	Subaccounts []BillSubaccountResponse `json:"subaccounts"`
}

// BillChallengeResponse is a sign-in a pull left parked.
type BillChallengeResponse struct {
	ID           uuid.UUID `json:"id"`
	ConnectionID uuid.UUID `json:"connection_id"`
	// Method is totp, sms, email, push or captcha.
	Method string `json:"method"`
	// Prompt is the provider's own wording of what it wants.
	Prompt string `json:"prompt"`
	// Image is a CAPTCHA, base64, and null for every other method.
	Image *string `json:"image"`
	// State is waiting, answered, expired or failed.
	State string `json:"state"`
	// AnsweredBy is mailbox or person, and null until one of them has.
	AnsweredBy *string `json:"answered_by"`
	// RaisedBy is pull, connect or keepalive.
	RaisedBy  string    `json:"raised_by"`
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is when the engine forgets the sign-in this names, after
	// which the answer route says so rather than pretending.
	ExpiresAt  time.Time  `json:"expires_at"`
	AnsweredAt *time.Time `json:"answered_at"`
}

// BillPullResult is how one pull ended. Status uses last_pull_status's
// vocabulary: ok, challenge, needs_sign_in or failed.
type BillPullResult struct {
	Status    string `json:"status"`
	New       int    `json:"new"`
	Amended   int    `json:"amended"`
	Unchanged int    `json:"unchanged"`
	// Documents is how many statements this pull fetched and filed.
	Documents int                    `json:"documents"`
	Challenge *BillChallengeResponse `json:"challenge"`
	Error     string                 `json:"error"`
	Notes     []string               `json:"notes"`
	// HasFailureScreenshot is whether the failure-screenshot route has the
	// page this pull stopped on.
	HasFailureScreenshot bool `json:"has_failure_screenshot"`
}

// BillBrowserRelease is what letting go of a connection's browser gave up.
// Message is the whole of what the screen shows.
type BillBrowserRelease struct {
	Released  bool   `json:"released"`
	Sessions  int    `json:"sessions"`
	Lock      bool   `json:"lock"`
	Singleton bool   `json:"singleton"`
	Message   string `json:"message"`
}

// NewBills builds the bills service over this environment, for every handler
// here and for serve's scheduler.
func NewBills(env *Env) *service.Bills {
	st := env.DB
	if sealed, err := sealedStore(env); err == nil {
		st = sealed
	}
	bills := service.NewBills(st)
	bills.Now = env.now
	bills.Documents = env.documents()
	bills.Alerts = newAlerts(env)
	// The mailbox answers a code before anybody is told about it. A function
	// value, so neither service holds the other.
	bills.Answerers = append(bills.Answerers, newMailboxAnswerer(env))
	bills.MailedCode = func(
		ctx context.Context, spaceID store.SpaceID, connection store.BillConnection, since time.Time,
	) (string, bool) {
		return NewMailbox(env).WaitForBillCode(ctx, spaceID, connection, since)
	}
	bills.HasMailbox = func(ctx context.Context, spaceID store.SpaceID) bool {
		return NewMailbox(env).HasReadableMailbox(ctx, spaceID)
	}
	bills.Agent = env.billsEngine()
	return bills
}

// lazyConnectorState is the in-process connector engine, built once per server
// and shared by bills and merchants: it holds the session a dialog comes back
// to between its two calls, so a per-request engine would forget a sign-in.
// The browser starts only when a connection or account first signs in or is
// pulled.
type lazyConnectorState struct {
	lazyConnector      sync.Once
	lazyConnectorValue *connector.Engine
}

func (e *Env) connectorEngine() *connector.Engine {
	e.lazyConnector.Do(func() {
		engine := connector.New(e.browserEngine(), billers.New(), merchants.NewRegistry())
		engine.SignInEnded = func(end provider.BillSignInEnded) {
			NewBills(e).KeepSignInEnd(context.Background(), end)
		}
		e.lazyConnectorValue = engine
	})
	return e.lazyConnectorValue
}

// lazyBrowserState is the one browser this process drives. One per agent
// would be a second Chromium and a second set of profile claims that cannot
// see the first.
type lazyBrowserState struct {
	lazyBrowser      sync.Once
	lazyBrowserValue *browser.Engine
}

func (e *Env) browserEngine() *browser.Engine {
	e.lazyBrowser.Do(func() {
		e.lazyBrowserValue = browser.NewEngine(e.Cfg.Browser)
	})
	return e.lazyBrowserValue
}

func (e *Env) billsEngine() service.BillsAgent {
	if e.BillsAgent != nil {
		return e.BillsAgent
	}
	return connector.Bills{Engine: e.connectorEngine()}
}

func billsService(env *Env) *service.Bills { return NewBills(env) }

func billAgentStatus(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	bills := billsService(env)
	out := BillAgentResponse{
		Configured: bills.HasAgent(), Providers: []provider.BillProvider{},
	}
	if !out.Configured {
		out.Error = "This build carries no browser engine. " +
			"Bills can still be filed by a mailbox rule or the assistant, or kept as recurring items on Upcoming."
		return writeJSON(w, http.StatusOK, out)
	}
	providers, err := bills.Providers(r.Context())
	if err != nil {
		out.Error = err.Error()
		return writeJSON(w, http.StatusOK, out)
	}
	out.Healthy = true
	if providers != nil {
		out.Providers = providers
	}
	return writeJSON(w, http.StatusOK, out)
}

// startBillSignIn opens a connect, typed or live.
func startBillSignIn(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	var body BillSignInRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if err := keepBillSite(env, r, sp, &connection, body.Site); err != nil {
		return err
	}
	bills := billsService(env)

	if body.Mode == "live" {
		state, err := bills.StartLiveConnect(r.Context(), sp.ID(), connection.ID, body.Width, body.Height)
		if err != nil {
			return billAgentError(err)
		}
		return writeJSON(w, http.StatusOK, billSignInState(state))
	}
	if body.Mode != "" && body.Mode != "typed" {
		return errInvalid("invalid", []string{"body", "mode"}, "mode is typed or live")
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" || body.Password == "" {
		return errInvalid("missing", []string{"body", "username"},
			"The %s username and password are both needed", connection.ProviderName())
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
	state, err := bills.StartConnect(r.Context(), sp.ID(), connection.ID,
		body.Username, body.Password, strings.TrimSpace(body.TOTP), secret, factor)
	if err != nil {
		return billAgentError(err)
	}
	// The username is kept so the next form fills itself in; the password is
	// sealed only on complete.
	if body.Username != connection.Username {
		connection.Username = body.Username
		if err := env.DB.UpdateBillConnection(r.Context(), sp.ID(), &connection); err != nil {
			return err
		}
	}
	return writeJSON(w, http.StatusOK, billSignInState(state))
}

// keepBillSite writes the deployment a sign-in named onto the connection,
// before that sign-in opens a browser at it. A sign-in that named nothing
// changes nothing.
func keepBillSite(
	env *Env, r *http.Request, sp auth.SpaceContext, connection *store.BillConnection, asked string,
) error {
	biller, _ := domain.BillerByID(connection.Biller)
	site, err := billSite(asked, biller)
	if err != nil {
		return err
	}
	if site == "" || site == connection.Site {
		return nil
	}
	connection.Site = site
	return env.DB.UpdateBillConnection(r.Context(), sp.ID(), connection)
}

func billSignInStatus(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	state, err := billsService(env).ConnectStatus(
		r.Context(), sp.ID(), connection.ID, chi.URLParam(r, "session"))
	if err != nil {
		return billAgentError(err)
	}
	return writeJSON(w, http.StatusOK, billSignInState(state))
}

// billSignInTrail is the trail of a sign-in that is still open.
func billSignInTrail(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	trail, err := billsService(env).ConnectTrail(
		r.Context(), sp.ID(), connection.ID, chi.URLParam(r, "session"))
	if err != nil {
		return billAgentError(err)
	}
	return writeJSON(w, http.StatusOK, billTrailEntries(trail))
}

// The four developer steers, over a sign-in somebody is already sitting at.
//
// Owner-only: a steer drives a browser signed in to a household's utility
// account. It may never leave that site and never answers a field's value;
// both rules are in internal/browser/devtools.go.

func steerBillSignIn(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, body, err := billSteerRequest[BillSignInSteerRequest](r, env, sp)
	if err != nil {
		return err
	}
	steered, err := billsService(env).SteerTo(r.Context(), sp.ID(), connection.ID,
		chi.URLParam(r, "session"), strings.TrimSpace(body.URL))
	if err != nil {
		return billAgentError(err)
	}
	return writeJSON(w, http.StatusOK, billSignInSteered(steered))
}

func clickBillSignIn(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, body, err := billSteerRequest[BillSignInClickRequest](r, env, sp)
	if err != nil {
		return err
	}
	steered, err := billsService(env).SteerClick(r.Context(), sp.ID(), connection.ID,
		chi.URLParam(r, "session"), strings.TrimSpace(body.Text), strings.TrimSpace(body.Selector))
	if err != nil {
		return billAgentError(err)
	}
	return writeJSON(w, http.StatusOK, billSignInSteered(steered))
}

func readBillSignInDOM(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, body, err := billSteerRequest[BillSignInDOMRequest](r, env, sp)
	if err != nil {
		return err
	}
	found, err := billsService(env).SteerDOM(r.Context(), sp.ID(), connection.ID,
		chi.URLParam(r, "session"), strings.TrimSpace(body.Selector), body.Limit)
	if err != nil {
		return billAgentError(err)
	}
	out := BillSignInDOMResponse{
		Provider: found.Provider, URL: found.URL, Count: found.Count,
		Elements: make([]BillSignInDOMElement, 0, len(found.Elements)),
	}
	for _, one := range found.Elements {
		out.Elements = append(out.Elements, BillSignInDOMElement{
			Tag: one.Tag, Attributes: one.Attributes, Text: one.Text, HTML: one.HTML,
		})
	}
	return writeJSON(w, http.StatusOK, out)
}

func fetchFromBillSignIn(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, body, err := billSteerRequest[provider.BillSignInFetchRequest](r, env, sp)
	if err != nil {
		return err
	}
	body.URL = strings.TrimSpace(body.URL)
	answered, err := billsService(env).SteerFetch(r.Context(), sp.ID(), connection.ID,
		chi.URLParam(r, "session"), body)
	if err != nil {
		return billAgentError(err)
	}
	return writeJSON(w, http.StatusOK, BillSignInFetchResponse{
		Provider: answered.Provider, URL: answered.URL, Status: answered.Status,
		ContentType: answered.ContentType, Length: answered.Length,
		Text: answered.Text, Matches: answered.Matches,
	})
}

// billSteerRequest is the three things every steer needs: an owner, the
// connection, and the body.
func billSteerRequest[T any](r *http.Request, env *Env, sp auth.SpaceContext) (store.BillConnection, T, error) {
	var body T
	if err := sp.RequireOwner(); err != nil {
		return store.BillConnection{}, body, err
	}
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return store.BillConnection{}, body, err
	}
	if err := decodeBody(r, &body); err != nil {
		return store.BillConnection{}, body, err
	}
	return connection, body, nil
}

// billSignInSteered is the engine's answer in the shape the route promises.
func billSignInSteered(steered provider.BillSignInSteer) BillSignInSteerResponse {
	out := BillSignInSteerResponse{
		Provider: steered.Provider, URL: steered.URL, Title: steered.Title,
		Text: steered.Text, Opened: steered.Opened,
		Requests: make([]BillSignInRequestLine, 0, len(steered.Requests)),
	}
	for _, one := range steered.Requests {
		out.Requests = append(out.Requests, BillSignInRequestLine{
			Method: one.Method, URL: one.URL, Status: one.Status, Type: one.Type,
			Authorization: one.Authorization, Body: one.Body, Response: one.Response,
		})
	}
	if steered.Download != nil {
		out.Download = &BillSignInDownloadEntry{
			Filename: steered.Download.Filename, URL: steered.Download.URL,
		}
	}
	return out
}

// BillMailedCodeResponse is the sign-in state after the mailbox was watched
// for its code, and whether one came.
type BillMailedCodeResponse struct {
	BillSignInState
	MailedCodeFound bool `json:"mailed_code_found"`
}

// cancelBillSignIn gives up a sign-in nobody finished, so the browser it
// claimed is free for the next attempt.
func cancelBillSignIn(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	if err := billsService(env).CancelConnect(r.Context(), sp.ID(), connection.ID,
		chi.URLParam(r, "session")); err != nil {
		return billAgentError(err)
	}
	return writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}

// completeBillSignIn seals the session, starts the first pull on it, and
// answers with the connection and what it bills. The answer says pulling
// whenever a pull started, even one already finished; the dialog reads the
// outcome either way.
func completeBillSignIn(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	bills := billsService(env)
	subaccounts, err := bills.CompleteConnect(
		r.Context(), sp.ID(), connection.ID, chi.URLParam(r, "session"))
	if err != nil {
		return billAgentError(err)
	}
	started := bills.PullAfterSignIn(sp.ID(), connection.ID)
	out, err := billConnectionWithSubaccounts(env, r, sp, connection.ID, subaccounts)
	if err != nil {
		return err
	}
	out.Pulling = out.Pulling || started
	return writeJSON(w, http.StatusOK, out)
}

// releaseBillBrowser lets go of the browser a connection is holding, and
// nothing else: the session, password and profile stay. Owner-only, because it
// takes a lock off the profiles volume.
func releaseBillBrowser(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if err := sp.RequireOwner(); err != nil {
		return err
	}
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	released, err := billsService(env).ReleaseBrowser(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return billAgentError(err)
	}
	return writeJSON(w, http.StatusOK, BillBrowserRelease{
		Released: released.Released(), Sessions: len(released.Sessions),
		Lock: released.Lock, Singleton: released.Singleton,
		Message: billBrowserReleased(released),
	})
}

// billBrowserReleased says what was given up, in one sentence. Nothing to give
// up means the hold had already timed out, and reads as reassurance.
func billBrowserReleased(released provider.BillProfileRelease) string {
	if !released.Released() {
		return "Nothing was holding this connection's browser, so there was nothing to " +
			"release. Sign in again"
	}
	var gave []string
	if count := len(released.Sessions); count > 0 {
		if count == 1 {
			gave = append(gave, "closed the sign-in that was still open")
		} else {
			gave = append(gave, "closed the "+strconv.Itoa(count)+" sign-ins that were still open")
		}
	}
	if released.Lock {
		gave = append(gave, "gave up the lock a restart left behind")
	}
	if released.Singleton {
		gave = append(gave, "cleared what the old browser left in the profile")
	}
	return "Released this connection's browser: " + strings.Join(gave, ", ") +
		". The kept session and password are untouched"
}

// forgetBillSession disconnects: the session here and the profile next door.
func forgetBillSession(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	if err := billsService(env).Forget(r.Context(), sp.ID(), connection.ID); err != nil {
		return notFoundAs(err, "Bill connection")
	}
	return writeBillConnection(env, w, r, sp, connection.ID, nil)
}

// pullBillConnection runs the pull now and answers how it ended — including
// the challenge it parked, which is the case a person has to act on.
func pullBillConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	result, err := billsService(env).Pull(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return billAgentError(err)
	}
	return writeJSON(w, http.StatusOK, billPullResult(result))
}

func listBillChallenges(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	switch state {
	case "", store.BillChallengeWaiting, store.BillChallengeAnswered,
		store.BillChallengeExpired, store.BillChallengeFailed:
	default:
		return errInvalid("invalid", []string{"query", "state"},
			"state is waiting, answered, expired or failed")
	}
	rows, err := env.DB.ListBillChallenges(r.Context(), sp.ID(), state)
	if err != nil {
		return err
	}
	out := make([]BillChallengeResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, billChallengeResponse(row))
	}
	return writeJSON(w, http.StatusOK, out)
}

func readBillChallenge(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	challenge, err := billChallenge(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, billChallengeResponse(challenge))
}

// answerBillChallenge gives the provider the code and carries the stopped pull
// on, so the answer's reply is the pull's own result.
func answerBillChallenge(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	challenge, err := billChallenge(r, env, sp)
	if err != nil {
		return err
	}
	var body SignInAnswer
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	// An empty code is an answer only to a push approval, which is a tap on
	// somebody's phone.
	if strings.TrimSpace(body.Code) == "" && challenge.Method != string(domain.ChallengePush) {
		return errInvalid("missing", []string{"body", "code"}, "code is required")
	}
	result, err := billsService(env).AnswerChallenge(
		r.Context(), sp.ID(), challenge.ID, strings.TrimSpace(body.Code))
	if err != nil {
		return billAgentError(err)
	}
	return writeJSON(w, http.StatusOK, billPullResult(result))
}

// billChallenge loads one challenge in this space.
func billChallenge(r *http.Request, env *Env, sp auth.SpaceContext) (store.BillChallenge, error) {
	return fromPath(r, sp, "challenge_id", "Bill sign-in", env.DB.GetBillChallenge)
}

// writeBillConnection re-reads the connection and answers with it.
func writeBillConnection(
	env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext,
	id uuid.UUID, subaccounts []store.BillSubaccount,
) error {
	out, err := billConnectionWithSubaccounts(env, r, sp, id, subaccounts)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func billConnectionWithSubaccounts(
	env *Env, r *http.Request, sp auth.SpaceContext, id uuid.UUID, subaccounts []store.BillSubaccount,
) (BillConnectionWithSubaccounts, error) {
	connection, err := env.DB.GetBillConnection(r.Context(), sp.ID(), id)
	if err != nil {
		return BillConnectionWithSubaccounts{}, notFoundAs(err, "Bill connection")
	}
	out := BillConnectionWithSubaccounts{
		BillConnectionResponse: billConnectionResponse(connection),
		Subaccounts:            make([]BillSubaccountResponse, 0, len(subaccounts)),
	}
	links, err := env.DB.ListSeriesBillLinks(r.Context(), sp.ID(), nil)
	if err != nil {
		return BillConnectionWithSubaccounts{}, err
	}
	linked := make(map[uuid.UUID]uuid.UUID, len(links))
	for _, link := range links {
		linked[link.SubaccountID] = link.SeriesID
	}
	for _, one := range subaccounts {
		out.Subaccounts = append(out.Subaccounts, BillSubaccountResponse{
			ID: one.ID, ConnectionID: one.ConnectionID, Biller: string(connection.Biller),
			ExternalID: one.ExternalID, Label: one.Label,
			MaskedNumber: pgconv.NullText(one.MaskedNumber), IsSelected: one.IsSelected,
			SeriesID: pgconv.NullUUID(linked[one.ID]),
		})
	}
	return out, nil
}

func billSignInState(state provider.BillConnectState) BillSignInState {
	out := BillSignInState{
		SessionID: state.SessionID, State: state.State, Prompt: state.Prompt,
		Image: state.Image, Error: state.Error, Width: state.Width, Height: state.Height,
		Method: state.Method, Accounts: make([]BillAccountChoice, 0, len(state.Accounts)),
	}
	for _, one := range state.Accounts {
		out.Accounts = append(out.Accounts, BillAccountChoice{
			ExternalID: one.ExternalID, Label: one.Label, MaskedNumber: one.MaskedNumber,
		})
	}
	out.Trail = billTrailEntries(state.Trail)
	return out
}

// billTrailEntries is the engine's trail in the dialog's shape. Always a
// list, never null: a failed sign-in that never reached a page has no trail.
func billTrailEntries(trail []provider.BillTrailEntry) []BillTrailEntry {
	out := make([]BillTrailEntry, 0, len(trail))
	for _, one := range trail {
		inputs := one.Inputs
		if inputs == nil {
			inputs = map[string]int{}
		}
		choices := make([]BillSignInChoice, 0, len(one.Choices))
		for _, choice := range one.Choices {
			choices = append(choices, BillSignInChoice{Kind: choice.Kind, Words: choice.Words})
		}
		out = append(out, BillTrailEntry{
			At: one.At, Step: one.Step, State: one.State, URL: one.URL, Title: one.Title,
			Form: BillSignInFormRead{
				Password: one.Form.Password, Username: one.Form.Username,
				OTP: one.Form.OTP, SignOutLink: one.Form.SignOutLink,
			},
			Inputs: inputs, Error: one.Error,
			Did: BillSignInAction{
				Acted: one.Did.Acted, Pressed: one.Did.Pressed, Words: one.Did.Words,
				Waited: one.Did.Waited, Changed: one.Did.Changed, Dismissed: one.Did.Dismissed,
			},
			Choices: choices, Chose: one.Chose, Forced: one.Forced, Snapshot: one.Snapshot, Note: one.Note,
		})
	}
	return out
}

func billChallengeResponse(one store.BillChallenge) BillChallengeResponse {
	return BillChallengeResponse{
		ID: one.ID, ConnectionID: one.ConnectionID, Method: one.Method, Prompt: one.Prompt,
		Image: pgconv.NullText(one.Image), State: one.State,
		AnsweredBy: pgconv.NullText(one.AnsweredBy), RaisedBy: one.RaisedBy,
		CreatedAt: one.CreatedAt, ExpiresAt: one.ExpiresAt, AnsweredAt: one.AnsweredAt,
	}
}

func billPullResult(result service.BillPullResult) BillPullResult {
	out := BillPullResult{
		Status: result.Status, New: result.New, Amended: result.Amended,
		Unchanged: result.Unchanged, Documents: result.Documents,
		Error: result.Error, Notes: result.Notes, HasFailureScreenshot: result.Screenshot,
	}
	if out.Notes == nil {
		out.Notes = []string{}
	}
	if result.Challenge != nil {
		challenge := billChallengeResponse(*result.Challenge)
		out.Challenge = &challenge
	}
	return out
}

// billSiteLabel is what a deployment slug may be: lowercase letters, digits
// and hyphens, 1 to 63 characters.
//
// It is a hostname label and a path segment in the same provider's API, so it
// is checked against the narrower rule. A dot would reach a different host and
// a slash a different path, with a signed-in browser.
var billSiteLabel = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

// ValidBillSite reports whether a value may be used as a provider deployment
// slug.
func ValidBillSite(site string) bool { return billSiteLabel.MatchString(site) }

// billSite is the site a request asked for, or the refusal it earns. Empty is
// allowed (a connection is made before anyone signs in); a site at a
// single-deployment provider is refused. A SiteAddress provider's site is kept
// as domain.SiteAddressOf writes it.
func billSite(site string, biller domain.Biller) (string, error) {
	site = strings.TrimSpace(site)
	if site == "" {
		return "", nil
	}
	if !biller.NeedsSite {
		return "", errInvalid("invalid", []string{"body", "site"},
			"%s is one site for everybody; there is no deployment to name", biller.Name)
	}
	if biller.SiteAddress {
		address, ok := domain.SiteAddressOf(site)
		if !ok {
			return "", errInvalid("invalid", []string{"body", "site"},
				"that is not an address %s can be signed in at: copy it from the address bar while "+
					"you are signed in, e.g. https://portal.example.org/Portal", biller.Name)
		}
		return address, nil
	}
	site = strings.ToLower(site)
	if !ValidBillSite(site) {
		return "", errInvalid("invalid", []string{"body", "site"},
			"a community is lowercase letters, digits and hyphens, and nothing else — "+
				"it is the part before the first dot in the address you use")
	}
	return site, nil
}

// billSignIn is the bill connections' half of the shared sign-in routes.
var billSignIn = signInConnector[provider.BillConnectState, BillSignInState]{
	nouns: func(*http.Request) agentNouns { return billNouns },
	target: func(env *Env, r *http.Request, sp auth.SpaceContext) (uuid.UUID, error) {
		connection, err := billConnection(r, env, sp)
		return connection.ID, err
	},
	answer: func(env *Env, ctx context.Context, space store.SpaceID, id uuid.UUID, session, code string) (provider.BillConnectState, error) {
		return billsService(env).AnswerConnect(ctx, space, id, session, code)
	},
	fromMail: func(env *Env, ctx context.Context, space store.SpaceID, id uuid.UUID, session string) (provider.BillConnectState, bool, error) {
		return service.AnswerFromMail[provider.BillConnectState](ctx, billsService(env), space, id, session)
	},
	// Forgetting the password disconnects too; see service.Bills.ForgetCredential.
	forget: func(env *Env, ctx context.Context, space store.SpaceID, id uuid.UUID) error {
		return billsService(env).ForgetCredential(ctx, space, id)
	},
	state: billSignInState,
	mailed: func(state BillSignInState, found bool) any {
		return BillMailedCodeResponse{BillSignInState: state, MailedCodeFound: found}
	},
	written: func(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext, id uuid.UUID) error {
		return writeBillConnection(env, w, r, sp, id, nil)
	},
}

var billNouns = agentNouns{
	SignIn:     "Bill sign-in",
	Connection: "Bill connection",
	Unavailable: "This build carries no browser engine, so Agentifi cannot " +
		"reach this provider. A mailbox rule or the assistant can still file its bills; " +
		"otherwise keep the bill as a recurring item on Upcoming",
}

func billAgentError(err error) error { return connectorAgentError(err, billNouns) }
