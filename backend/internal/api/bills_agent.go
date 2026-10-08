package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/connector"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/merchants"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/totp"
)

// The bills agent: a sign-in, a pull, a challenge. BillSignInService is here,
// with the agent's status and the pull, which are BillService's.
//
// A credential crosses this file and is never stored by it. What is kept is
// the session the provider hands back, sealed row-bound by the store, which no
// response here has a field for.

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

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewBillSignInServiceHandler(billSignInService{env}, opts...)
	})
}

type billSignInService struct{ env *Env }

func (s billService) GetBillAgent(
	ctx context.Context, _ *agentifiv1.GetBillAgentRequest,
) (*agentifiv1.GetBillAgentResponse, error) {
	bills := billsService(s.env)
	out := &agentifiv1.GetBillAgentResponse{
		Configured: bills.HasAgent(), Providers: []*agentifiv1.BillProvider{},
	}
	if !out.Configured {
		out.Error = "This build carries no browser engine. " +
			"Bills can still be filed by a mailbox rule or the assistant, or kept as recurring items on Upcoming."
		return out, nil
	}
	providers, err := bills.Providers(ctx)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.Healthy = true
	for _, one := range providers {
		out.Providers = append(out.Providers, &agentifiv1.BillProvider{
			Id: one.ID, Name: one.Name, Access: one.Access, Home: one.Home,
			SignIn:          &agentifiv1.BillProviderSignIn{Kinds: one.SignIn.Kinds, Prompt: one.SignIn.Prompt},
			Challenges:      one.Challenges,
			SessionPersists: one.SessionPersists, KeepaliveDays: int32(one.KeepaliveDays),
			ReportsAutopay: one.ReportsAutopay, HasDocuments: one.HasDocuments,
		})
	}
	return out, nil
}

// StartBillSignIn opens a connect, typed or live.
func (s billSignInService) StartBillSignIn(
	ctx context.Context, req *agentifiv1.StartBillSignInRequest,
) (*agentifiv1.StartBillSignInResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err := keepBillSite(ctx, s.env, sp, &connection, req.GetSite()); err != nil {
		return nil, err
	}
	bills := billsService(s.env)

	if req.GetMode() == "live" {
		state, err := bills.StartLiveConnect(ctx, sp.ID(), connection.ID,
			int(req.GetWidth()), int(req.GetHeight()))
		if err != nil {
			return nil, billAgentError(err)
		}
		return &agentifiv1.StartBillSignInResponse{State: billSignInState(state)}, nil
	}
	if req.GetMode() != "" && req.GetMode() != "typed" {
		return nil, errInvalid("invalid", []string{"body", "mode"}, "mode is typed or live")
	}
	username := strings.TrimSpace(req.GetUsername())
	if username == "" || req.GetPassword() == "" {
		return nil, errInvalid("missing", []string{"body", "username"},
			"The %s username and password are both needed", connection.ProviderName())
	}
	secret := totp.Normalize(req.GetTotpSecret())
	if secret != "" && !totp.Valid(secret) {
		return nil, errInvalid("invalid", []string{"body", "totp_secret"},
			"the authenticator secret is not a base32 setup key")
	}
	factor := domain.SecondFactor(strings.TrimSpace(req.GetSecondFactor()))
	if !factor.Valid() {
		return nil, errInvalid("invalid", []string{"body", "second_factor"},
			"the second factor is none, email, sms or totp")
	}
	state, err := bills.StartConnect(ctx, sp.ID(), connection.ID,
		username, req.GetPassword(), strings.TrimSpace(req.GetTotp()), secret, factor)
	if err != nil {
		return nil, billAgentError(err)
	}
	// The username is kept so the next form fills itself in; the password is
	// sealed only on complete.
	if username != connection.Username {
		connection.Username = username
		if err := s.env.DB.UpdateBillConnection(ctx, sp.ID(), &connection); err != nil {
			return nil, err
		}
	}
	return &agentifiv1.StartBillSignInResponse{State: billSignInState(state)}, nil
}

// keepBillSite writes the deployment a sign-in named onto the connection,
// before that sign-in opens a browser at it. A sign-in that named nothing
// changes nothing.
func keepBillSite(
	ctx context.Context, env *Env, sp auth.SpaceContext, connection *store.BillConnection, asked string,
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
	return env.DB.UpdateBillConnection(ctx, sp.ID(), connection)
}

func (s billSignInService) GetBillSignIn(
	ctx context.Context, req *agentifiv1.GetBillSignInRequest,
) (*agentifiv1.GetBillSignInResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	state, err := billsService(s.env).ConnectStatus(ctx, sp.ID(), connection.ID, req.GetSession())
	if err != nil {
		return nil, billAgentError(err)
	}
	return &agentifiv1.GetBillSignInResponse{State: billSignInState(state)}, nil
}

// GetBillSignInTrail is the trail of a sign-in that is still open.
func (s billSignInService) GetBillSignInTrail(
	ctx context.Context, req *agentifiv1.GetBillSignInTrailRequest,
) (*agentifiv1.GetBillSignInTrailResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	trail, err := billsService(s.env).ConnectTrail(ctx, sp.ID(), connection.ID, req.GetSession())
	if err != nil {
		return nil, billAgentError(err)
	}
	return &agentifiv1.GetBillSignInTrailResponse{Entries: billTrailEntries(trail)}, nil
}

func (s billSignInService) AnswerBillSignIn(
	ctx context.Context, req *agentifiv1.AnswerBillSignInRequest,
) (*agentifiv1.AnswerBillSignInResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	state, err := billSignIn.step(ctx, s.env, sp, connection.ID, req.GetSession(), req.GetCode(), billNouns)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.AnswerBillSignInResponse{State: billSignInState(state)}, nil
}

func (s billSignInService) AnswerBillSignInFromMail(
	ctx context.Context, req *agentifiv1.AnswerBillSignInFromMailRequest,
) (*agentifiv1.AnswerBillSignInFromMailResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	state, found, err := billSignIn.stepFromMail(ctx, s.env, sp, connection.ID, req.GetSession(), billNouns)
	if err != nil {
		return nil, err
	}
	shown := billSignInState(state)
	return &agentifiv1.AnswerBillSignInFromMailResponse{
		SessionId: shown.SessionId, State: shown.State, Prompt: shown.Prompt, Image: shown.Image,
		Error: shown.Error, Width: shown.Width, Height: shown.Height, Method: shown.Method,
		Accounts: shown.Accounts, Trail: shown.Trail, MailedCodeFound: found,
	}, nil
}

// The four developer steers, over a sign-in somebody is already sitting at.
//
// Owner-only: a steer drives a browser signed in to a household's utility
// account. It may never leave that site and never answers a field's value;
// both rules are in internal/browser/devtools.go.

func (s billSignInService) SteerBillSignIn(
	ctx context.Context, req *agentifiv1.SteerBillSignInRequest,
) (*agentifiv1.SteerBillSignInResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billSteerTarget(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	steered, err := billsService(s.env).SteerTo(ctx, sp.ID(), connection.ID,
		req.GetSession(), strings.TrimSpace(req.GetUrl()))
	if err != nil {
		return nil, billAgentError(err)
	}
	return billSignInSteered(steered), nil
}

func (s billSignInService) ClickBillSignIn(
	ctx context.Context, req *agentifiv1.ClickBillSignInRequest,
) (*agentifiv1.ClickBillSignInResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billSteerTarget(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	steered, err := billsService(s.env).SteerClick(ctx, sp.ID(), connection.ID,
		req.GetSession(), strings.TrimSpace(req.GetText()), strings.TrimSpace(req.GetSelector()))
	if err != nil {
		return nil, billAgentError(err)
	}
	shown := billSignInSteered(steered)
	return &agentifiv1.ClickBillSignInResponse{
		Provider: shown.Provider, Url: shown.Url, Title: shown.Title, Text: shown.Text,
		Requests: shown.Requests, Download: shown.Download, Opened: shown.Opened,
	}, nil
}

func (s billSignInService) ReadBillSignInDom(
	ctx context.Context, req *agentifiv1.ReadBillSignInDomRequest,
) (*agentifiv1.ReadBillSignInDomResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billSteerTarget(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	found, err := billsService(s.env).SteerDOM(ctx, sp.ID(), connection.ID,
		req.GetSession(), strings.TrimSpace(req.GetSelector()), int(req.GetLimit()))
	if err != nil {
		return nil, billAgentError(err)
	}
	out := &agentifiv1.ReadBillSignInDomResponse{
		Provider: found.Provider, Url: found.URL, Count: int32(found.Count),
		Elements: make([]*agentifiv1.BillSignInDomElement, 0, len(found.Elements)),
	}
	for _, one := range found.Elements {
		out.Elements = append(out.Elements, &agentifiv1.BillSignInDomElement{
			Tag: one.Tag, Attributes: one.Attributes, Text: one.Text, Html: one.HTML,
		})
	}
	return out, nil
}

func (s billSignInService) FetchFromBillSignIn(
	ctx context.Context, req *agentifiv1.FetchFromBillSignInRequest,
) (*agentifiv1.FetchFromBillSignInResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billSteerTarget(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	answered, err := billsService(s.env).SteerFetch(ctx, sp.ID(), connection.ID, req.GetSession(),
		provider.BillSignInFetchRequest{
			URL: strings.TrimSpace(req.GetUrl()), Method: req.GetMethod(), Body: req.GetBody(),
			Headers: req.GetHeaders(), Find: req.GetFind(), Context: int(req.GetContext()),
		})
	if err != nil {
		return nil, billAgentError(err)
	}
	return &agentifiv1.FetchFromBillSignInResponse{
		Provider: answered.Provider, Url: answered.URL, Status: int32(answered.Status),
		ContentType: answered.ContentType, Length: int32(answered.Length),
		Text: answered.Text, Matches: answered.Matches,
	}, nil
}

// billSteerTarget is the owner check every steer makes, then the connection.
func billSteerTarget(ctx context.Context, env *Env, sp auth.SpaceContext, raw string) (store.BillConnection, error) {
	if err := sp.RequireOwner(); err != nil {
		return store.BillConnection{}, err
	}
	return billConnectionOf(ctx, env, sp, raw)
}

// billSignInSteered is the engine's answer in the shape a steer promises.
func billSignInSteered(steered provider.BillSignInSteer) *agentifiv1.SteerBillSignInResponse {
	out := &agentifiv1.SteerBillSignInResponse{
		Provider: steered.Provider, Url: steered.URL, Title: steered.Title,
		Text: steered.Text, Opened: steered.Opened,
		Requests: make([]*agentifiv1.BillSignInRequestLine, 0, len(steered.Requests)),
	}
	for _, one := range steered.Requests {
		out.Requests = append(out.Requests, &agentifiv1.BillSignInRequestLine{
			Method: one.Method, Url: one.URL, Status: int32(one.Status), Type: one.Type,
			Authorization: one.Authorization, Body: one.Body, Response: one.Response,
		})
	}
	if steered.Download != nil {
		out.Download = &agentifiv1.BillSignInDownload{
			Filename: steered.Download.Filename, Url: steered.Download.URL,
		}
	}
	return out
}

// CancelBillSignIn gives up a sign-in nobody finished, so the browser it
// claimed is free for the next attempt.
func (s billSignInService) CancelBillSignIn(
	ctx context.Context, req *agentifiv1.CancelBillSignInRequest,
) (*agentifiv1.CancelBillSignInResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err := billsService(s.env).CancelConnect(ctx, sp.ID(), connection.ID, req.GetSession()); err != nil {
		return nil, billAgentError(err)
	}
	return &agentifiv1.CancelBillSignInResponse{Cancelled: true}, nil
}

// CompleteBillSignIn seals the session, starts the first pull on it, and
// answers with the connection and what it bills. The answer says pulling
// whenever a pull started, even one already finished; the dialog reads the
// outcome either way.
func (s billSignInService) CompleteBillSignIn(
	ctx context.Context, req *agentifiv1.CompleteBillSignInRequest,
) (*agentifiv1.CompleteBillSignInResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	bills := billsService(s.env)
	subaccounts, err := bills.CompleteConnect(ctx, sp.ID(), connection.ID, req.GetSession())
	if err != nil {
		return nil, billAgentError(err)
	}
	started := bills.PullAfterSignIn(sp.ID(), connection.ID)
	out, err := billConnectionWithSubaccounts(ctx, s.env, sp, connection.ID, subaccounts)
	if err != nil {
		return nil, err
	}
	out.Pulling = out.Pulling || started
	return &agentifiv1.CompleteBillSignInResponse{Connection: out}, nil
}

// ReleaseBillBrowser lets go of the browser a connection is holding, and
// nothing else: the session, password and profile stay. Owner-only, because it
// takes a lock off the profiles volume.
func (s billSignInService) ReleaseBillBrowser(
	ctx context.Context, req *agentifiv1.ReleaseBillBrowserRequest,
) (*agentifiv1.ReleaseBillBrowserResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billSteerTarget(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	released, err := billsService(s.env).ReleaseBrowser(ctx, sp.ID(), connection.ID)
	if err != nil {
		return nil, billAgentError(err)
	}
	return &agentifiv1.ReleaseBillBrowserResponse{
		Released: released.Released(), Sessions: int32(len(released.Sessions)),
		Lock: released.Lock, Singleton: released.Singleton,
		Message: billBrowserReleased(released),
	}, nil
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

// ForgetBillSession disconnects: the session here and the profile next door.
func (s billSignInService) ForgetBillSession(
	ctx context.Context, req *agentifiv1.ForgetBillSessionRequest,
) (*agentifiv1.ForgetBillSessionResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err := billsService(s.env).Forget(ctx, sp.ID(), connection.ID); err != nil {
		return nil, notFoundAs(err, "Bill connection")
	}
	out, err := billConnectionWithSubaccounts(ctx, s.env, sp, connection.ID, nil)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ForgetBillSessionResponse{Connection: out}, nil
}

// ForgetBillCredential forgets the kept password, which disconnects too; see
// service.Bills.ForgetCredential.
func (s billSignInService) ForgetBillCredential(
	ctx context.Context, req *agentifiv1.ForgetBillCredentialRequest,
) (*agentifiv1.ForgetBillCredentialResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err := billSignIn.forgetKept(ctx, s.env, sp, connection.ID, billNouns); err != nil {
		return nil, err
	}
	out, err := billConnectionWithSubaccounts(ctx, s.env, sp, connection.ID, nil)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ForgetBillCredentialResponse{Connection: out}, nil
}

// PullBillConnection runs the pull now and answers how it ended — including
// the challenge it parked, which is the case a person has to act on.
func (s billService) PullBillConnection(
	ctx context.Context, req *agentifiv1.PullBillConnectionRequest,
) (*agentifiv1.PullBillConnectionResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	result, err := billsService(s.env).Pull(ctx, sp.ID(), connection.ID)
	if err != nil {
		return nil, billAgentError(err)
	}
	return &agentifiv1.PullBillConnectionResponse{Result: billPullResult(result)}, nil
}

func (s billSignInService) ListBillChallenges(
	ctx context.Context, req *agentifiv1.ListBillChallengesRequest,
) (*agentifiv1.ListBillChallengesResponse, error) {
	state := strings.TrimSpace(req.GetState())
	switch state {
	case "", store.BillChallengeWaiting, store.BillChallengeAnswered,
		store.BillChallengeExpired, store.BillChallengeFailed:
	default:
		return nil, errInvalid("invalid", []string{"query", "state"},
			"state is waiting, answered, expired or failed")
	}
	rows, err := s.env.DB.ListBillChallenges(ctx, spaceFrom(ctx).ID(), state)
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListBillChallengesResponse{Challenges: make([]*agentifiv1.BillChallenge, 0, len(rows))}
	for _, row := range rows {
		out.Challenges = append(out.Challenges, billChallengeProto(row))
	}
	return out, nil
}

func (s billSignInService) GetBillChallenge(
	ctx context.Context, req *agentifiv1.GetBillChallengeRequest,
) (*agentifiv1.GetBillChallengeResponse, error) {
	challenge, err := billChallengeOf(ctx, s.env, spaceFrom(ctx), req.GetChallengeId())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetBillChallengeResponse{Challenge: billChallengeProto(challenge)}, nil
}

// AnswerBillChallenge gives the provider the code and carries the stopped
// pull on, so the answer's reply is the pull's own result.
func (s billSignInService) AnswerBillChallenge(
	ctx context.Context, req *agentifiv1.AnswerBillChallengeRequest,
) (*agentifiv1.AnswerBillChallengeResponse, error) {
	sp := spaceFrom(ctx)
	challenge, err := billChallengeOf(ctx, s.env, sp, req.GetChallengeId())
	if err != nil {
		return nil, err
	}
	code := strings.TrimSpace(req.GetCode())
	// An empty code is an answer only to a push approval, which is a tap on
	// somebody's phone.
	if code == "" && challenge.Method != string(domain.ChallengePush) {
		return nil, errInvalid("missing", []string{"body", "code"}, "code is required")
	}
	result, err := billsService(s.env).AnswerChallenge(ctx, sp.ID(), challenge.ID, code)
	if err != nil {
		return nil, billAgentError(err)
	}
	return &agentifiv1.AnswerBillChallengeResponse{Result: billPullResult(result)}, nil
}

// billChallengeOf loads one challenge in this space.
func billChallengeOf(ctx context.Context, env *Env, sp auth.SpaceContext, raw string) (store.BillChallenge, error) {
	id, err := idFrom(raw, "Bill sign-in")
	if err != nil {
		return store.BillChallenge{}, err
	}
	row, err := env.DB.GetBillChallenge(ctx, sp.ID(), id)
	if err != nil {
		return store.BillChallenge{}, notFoundAs(err, "Bill sign-in")
	}
	return row, nil
}

// billConnectionWithSubaccounts re-reads the connection and renders it with
// the subaccounts given.
func billConnectionWithSubaccounts(
	ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID, subaccounts []store.BillSubaccount,
) (*agentifiv1.BillConnectionWithSubaccounts, error) {
	connection, err := env.DB.GetBillConnection(ctx, sp.ID(), id)
	if err != nil {
		return nil, notFoundAs(err, "Bill connection")
	}
	linked, err := billSeriesLinks(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	one := billConnectionProto(connection)
	out := &agentifiv1.BillConnectionWithSubaccounts{
		Id: one.Id, Biller: one.Biller, Label: one.Label, Username: one.Username, Site: one.Site,
		CredentialSource: one.CredentialSource, HasTotp: one.HasTotp, SecondFactor: one.SecondFactor,
		Connected: one.Connected, SignedInAt: one.SignedInAt, NeedsSignIn: one.NeedsSignIn,
		SignInPaused: one.SignInPaused, AutopayRule: one.AutopayRule, AutopayDays: one.AutopayDays,
		AutopayDay: one.AutopayDay, AutopayAccountId: one.AutopayAccountId,
		PullEnabled: one.PullEnabled, PullAt: one.PullAt, LastPulledAt: one.LastPulledAt,
		LastPullStatus: one.LastPullStatus, LastPullError: one.LastPullError,
		HasFailureScreenshot: one.HasFailureScreenshot, HasTrail: one.HasTrail,
		Pulling: one.Pulling, CreatedAt: one.CreatedAt,
		Subaccounts: make([]*agentifiv1.BillSubaccount, 0, len(subaccounts)),
	}
	for _, sub := range subaccounts {
		out.Subaccounts = append(out.Subaccounts,
			billSubaccountProto(sub, connection.Biller, linked[sub.ID], uuid.Nil))
	}
	return out, nil
}

func billSignInState(state provider.BillConnectState) *agentifiv1.BillSignInState {
	out := &agentifiv1.BillSignInState{
		SessionId: state.SessionID, State: state.State, Prompt: state.Prompt,
		Image: state.Image, Error: state.Error, Width: int32(state.Width), Height: int32(state.Height),
		Method: state.Method, Accounts: make([]*agentifiv1.BillAccountChoice, 0, len(state.Accounts)),
	}
	for _, one := range state.Accounts {
		out.Accounts = append(out.Accounts, &agentifiv1.BillAccountChoice{
			ExternalId: one.ExternalID, Label: one.Label, MaskedNumber: one.MaskedNumber,
		})
	}
	out.Trail = billTrailEntries(state.Trail)
	return out
}

// billTrailEntries is the engine's trail in the dialog's shape. Never what
// was typed: the household must be able to paste this to somebody, so no
// field values, no picture, no cookies.
func billTrailEntries(trail []provider.BillTrailEntry) []*agentifiv1.BillTrailEntry {
	out := make([]*agentifiv1.BillTrailEntry, 0, len(trail))
	for _, one := range trail {
		inputs := make(map[string]int32, len(one.Inputs))
		for name, count := range one.Inputs {
			inputs[name] = int32(count)
		}
		choices := make([]*agentifiv1.BillSignInChoice, 0, len(one.Choices))
		for _, choice := range one.Choices {
			choices = append(choices, &agentifiv1.BillSignInChoice{Kind: choice.Kind, Words: choice.Words})
		}
		out = append(out, &agentifiv1.BillTrailEntry{
			At: timestamppb.New(one.At), Step: one.Step, State: one.State, Url: one.URL, Title: one.Title,
			Form: &agentifiv1.BillSignInFormRead{
				Password: one.Form.Password, Username: one.Form.Username,
				Otp: one.Form.OTP, SignOutLink: one.Form.SignOutLink,
			},
			Inputs: inputs, Error: one.Error,
			Did: &agentifiv1.BillSignInAction{
				Acted: one.Did.Acted, Pressed: one.Did.Pressed, Words: one.Did.Words,
				Waited: one.Did.Waited, Changed: one.Did.Changed, Dismissed: one.Did.Dismissed,
			},
			Choices: choices, Chose: one.Chose, Forced: one.Forced, Snapshot: one.Snapshot, Note: one.Note,
		})
	}
	return out
}

func billChallengeProto(one store.BillChallenge) *agentifiv1.BillChallenge {
	return &agentifiv1.BillChallenge{
		Id: one.ID.String(), ConnectionId: one.ConnectionID.String(), Method: one.Method,
		Prompt: one.Prompt, Image: dbconv.NullText(one.Image), State: one.State,
		AnsweredBy: dbconv.NullText(one.AnsweredBy), RaisedBy: one.RaisedBy,
		CreatedAt: timestamppb.New(one.CreatedAt), ExpiresAt: timestamppb.New(one.ExpiresAt),
		AnsweredAt: billNullableTimestamp(one.AnsweredAt),
	}
}

func billPullResult(result service.BillPullResult) *agentifiv1.BillPullResult {
	out := &agentifiv1.BillPullResult{
		Status: result.Status, New: int32(result.New), Amended: int32(result.Amended),
		Unchanged: int32(result.Unchanged), Documents: int32(result.Documents),
		Error: result.Error, Notes: result.Notes, HasFailureScreenshot: result.Screenshot,
	}
	if out.Notes == nil {
		out.Notes = []string{}
	}
	if result.Challenge != nil {
		out.Challenge = billChallengeProto(*result.Challenge)
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

// billSignIn is the bill connections' half of the shared sign-in steps.
var billSignIn = signInConnector[provider.BillConnectState, *agentifiv1.BillSignInState]{
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
}

var billNouns = agentNouns{
	SignIn:     "Bill sign-in",
	Connection: "Bill connection",
	Unavailable: "This build carries no browser engine, so Agentifi cannot " +
		"reach this provider. A mailbox rule or the assistant can still file its bills; " +
		"otherwise keep the bill as a recurring item on Upcoming",
}

func billAgentError(err error) error { return connectorAgentError(err, billNouns) }
