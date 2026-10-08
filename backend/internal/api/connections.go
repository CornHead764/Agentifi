package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// SimpleFIN connections: claim one, sync it, rename it, remove it.
//
// Nothing here renders a credential. The setup token is write-only, and
// store.Connection has nowhere to hold the Access URL, so no response built
// from one can contain it.
//
// `needs_setup_token` (the Access URL itself refused; only a fresh token
// fixes it) and `bank_warnings` (one institution needs reauthorizing at the
// Bridge) are kept apart all the way to the client.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewConnectionServiceHandler(connectionService{env}, opts...)
	})
}

type connectionService struct{ env *Env }

// linkCandidates is the match screen: what the bank offers, and what the
// household already holds that it might be. Pairings are ranked as
// docs/importing.md specifies, and nothing is written until the user confirms
// each.
type linkCandidates struct {
	Remote []RemoteAccountResponse
	// Local is every account no connection owns, in register order. The client,
	// not the server, stops one being paired twice.
	Local []LinkTargetResponse
}

// RemoteAccountResponse is one account the Bridge reaches.
type RemoteAccountResponse struct {
	ExternalID   string
	Name         string
	Kind         string
	Institution  string
	MaskedNumber string
	Balance      domain.Money
	Currency     string
	// LinkedAccountID is set once somebody has paired this one, so a reloaded
	// match screen shows the decisions already made.
	LinkedAccountID *uuid.UUID
	// Suggested is the local account this most likely is, best first.
	Suggested []uuid.UUID
	// Likely is the first suggestion when it is sure enough for the match
	// screen to choose it, and Match says why it is or is not.
	Likely *uuid.UUID
	Match  LinkMatch
}

// LinkMatch is how sure the match screen is of its first suggestion. Only an
// account number or a name identifies an account; the kind, the institution
// and the balance are shared by too many accounts to choose one by.
type LinkMatch string

const (
	// MatchNumber: the masked number is the same, and the first suggestion
	// outscores every other.
	MatchNumber LinkMatch = "number"
	// MatchName: the name is the same, and the first suggestion outscores
	// every other.
	MatchName LinkMatch = "name"
	// MatchTie: the leading suggestions score alike, or another account at
	// the bank has the same likely match.
	MatchTie LinkMatch = "tie"
	// MatchWeak: the suggestions share only a kind, an institution or a
	// balance.
	MatchWeak LinkMatch = "weak"
	// MatchNone: nothing here resembles it.
	MatchNone LinkMatch = "none"
)

// LinkTargetResponse is a local account the user could pair.
type LinkTargetResponse struct {
	ID           uuid.UUID
	Name         string
	Kind         string
	MaskedNumber string
	Balance      domain.Money
	// SyncFloorOn is the newest transaction this account holds, which becomes
	// the floor if it is paired.
	SyncFloorOn domain.Date
	// LinkedTo names the provider account already chosen for it, if any.
	LinkedTo string
}

func (s connectionService) ListLinkCandidates(
	ctx context.Context, req *agentifiv1.ListLinkCandidatesRequest,
) (*agentifiv1.ListLinkCandidatesResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if !env.Live().SimpleFINEnabled {
		return nil, errBadRequest("SimpleFIN is not enabled on this server")
	}
	connection, err := liveConnection(ctx, env, sp.ID(), req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	sync, err := connectionSync(env)
	if err != nil {
		return nil, err
	}
	remote, err := sync.DiscoverAccounts(ctx, sp.ID(), connection.ID)
	if err != nil {
		return nil, err
	}

	accounts, err := env.DB.ListAccounts(ctx, sp.ID(), store.AccountQuery{})
	if err != nil {
		return nil, err
	}
	// The same balances the accounts listing shows, from the same helper.
	postings, err := postingsByAccount(ctx, env, sp, accounts)
	if err != nil {
		return nil, err
	}
	// Accounts no connection owns, plus this connection's own, so an existing
	// pairing shows. Another feed's account is never a candidate.
	local := make([]store.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.ConnectionID == uuid.Nil || account.ConnectionID == connection.ID {
			local = append(local, account)
		}
	}

	balances := make(map[uuid.UUID]domain.Money, len(local))
	for _, account := range local {
		balances[account.ID] = balancesFor(account, postings[account.ID], domain.Zero).Balance
	}
	ignored, err := env.DB.ListIgnoredRemoteAccounts(ctx, sp.ID(), connection.ID)
	if err != nil {
		return nil, err
	}
	refused := make(map[string]bool, len(ignored))
	for _, one := range ignored {
		refused[one.ExternalID] = true
	}
	asking := make([]provider.Account, 0, len(remote))
	for _, one := range remote {
		if !refused[one.ExternalID] {
			asking = append(asking, one)
		}
	}

	candidates := buildLinkCandidates(asking, local, balances)
	out := &agentifiv1.ListLinkCandidatesResponse{
		Remote:  make([]*agentifiv1.RemoteAccount, 0, len(candidates.Remote)),
		Local:   make([]*agentifiv1.LinkTarget, 0, len(candidates.Local)),
		Ignored: ignoredRemoteProtos(ignored),
	}
	for _, one := range candidates.Remote {
		row := &agentifiv1.RemoteAccount{
			ExternalId:   one.ExternalID,
			Name:         one.Name,
			Kind:         one.Kind,
			Institution:  one.Institution,
			MaskedNumber: one.MaskedNumber,
			Balance:      moneyProto(one.Balance),
			Currency:     one.Currency,
			Suggested:    idStrings(one.Suggested),
			Match:        string(one.Match),
		}
		if one.LinkedAccountID != nil {
			row.LinkedAccountId = idOrNil(*one.LinkedAccountID)
		}
		if one.Likely != nil {
			row.Likely = idOrNil(*one.Likely)
		}
		out.Remote = append(out.Remote, row)
	}
	for _, one := range candidates.Local {
		out.Local = append(out.Local, &agentifiv1.LinkTarget{
			Id:           one.ID.String(),
			Name:         one.Name,
			Kind:         one.Kind,
			MaskedNumber: one.MaskedNumber,
			Balance:      moneyProto(one.Balance),
			SyncFloorOn:  dateOrNil(one.SyncFloorOn),
			LinkedTo:     one.LinkedTo,
		})
	}
	return out, nil
}

// FinishLinking applies the match screen's choices, then starts the first
// sync. The accounts exist when it answers; their transactions follow.
func (s connectionService) FinishLinking(
	ctx context.Context, req *agentifiv1.FinishLinkingRequest,
) (*agentifiv1.FinishLinkingResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if !env.Live().SimpleFINEnabled {
		return nil, errBadRequest("SimpleFIN is not enabled on this server")
	}
	connection, err := liveConnection(ctx, env, sp.ID(), req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	choices := make([]service.LinkChoice, 0, len(req.GetChoices()))
	for i, one := range req.GetChoices() {
		choice := service.LinkChoice{ExternalID: one.GetExternalId(), Action: service.LinkAction(one.GetAction())}
		if choice.Action == service.LinkToAccount {
			at := []string{"body", "choices", strconv.Itoa(i), "account_id"}
			if one.AccountId == nil {
				return nil, errInvalid("missing", at, "name the account to pair with")
			}
			id, err := uuid.Parse(one.GetAccountId())
			if err != nil {
				return nil, errInvalid("uuid_parsing", at, "account_id must be a uuid")
			}
			choice.AccountID = id
		}
		choices = append(choices, choice)
	}
	sync, err := connectionSync(env)
	if err != nil {
		return nil, err
	}
	updated, err := sync.FinishLinking(ctx, sp.ID(), connection.ID, choices)
	if err != nil {
		return nil, linkError(err)
	}
	if _, err := sync.StartSync(ctx, sp.ID(), connection.ID); err != nil {
		return nil, err
	}
	return &agentifiv1.FinishLinkingResponse{Connection: connectionProto(updated)}, nil
}

// linkError words a refused finish for the person on the match screen.
func linkError(err error) error {
	var refused service.LinkRefused
	if errors.As(err, &refused) {
		return errBadRequest("%s", refused.Reason)
	}
	if errors.Is(err, provider.ErrCredentialsExpired) || errors.Is(err, provider.ErrRateLimited) {
		return claimError(err)
	}
	return err
}

// RestoreRemoteAccount lifts a refusal. The account is not re-created here:
// the next sync does that, which is the same path every other account takes.
func (s connectionService) RestoreRemoteAccount(
	ctx context.Context, req *agentifiv1.RestoreRemoteAccountRequest,
) (*agentifiv1.RestoreRemoteAccountResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := liveConnection(ctx, s.env, sp.ID(), req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	id, err := idFrom(req.GetIgnoredId(), "Ignored account")
	if err != nil {
		return nil, err
	}
	restored, err := s.env.DB.RestoreRemoteAccount(ctx, sp.ID(), connection.ID, id)
	if err != nil {
		return nil, err
	}
	if !restored {
		return nil, errNotFound("Ignored account")
	}
	return &agentifiv1.RestoreRemoteAccountResponse{}, nil
}

func ignoredRemoteProtos(all []store.IgnoredRemoteAccount) []*agentifiv1.IgnoredRemoteAccount {
	out := make([]*agentifiv1.IgnoredRemoteAccount, 0, len(all))
	for _, one := range all {
		out = append(out, &agentifiv1.IgnoredRemoteAccount{
			Id:           one.ID.String(),
			ExternalId:   one.ExternalID,
			Name:         one.Name,
			Institution:  one.Institution,
			MaskedNumber: one.MaskedNumber,
			IgnoredAt:    timestamppb.New(one.IgnoredAt),
		})
	}
	return out
}

// buildLinkCandidates pairs what the bank offers against what the household
// holds, and ranks the guesses: masked number, then name, kind and
// institution, then balance within a dollar. Every pairing is a suggestion the
// user confirms, a likely one included.
func buildLinkCandidates(
	remote []provider.Account, local []store.Account, balances map[uuid.UUID]domain.Money,
) linkCandidates {
	out := linkCandidates{
		Remote: make([]RemoteAccountResponse, 0, len(remote)),
		Local:  make([]LinkTargetResponse, 0, len(local)),
	}

	claimed := map[string]uuid.UUID{}
	for _, account := range local {
		if account.SimpleFINAccountID != "" {
			claimed[account.SimpleFINAccountID] = account.ID
		}
	}

	likelyFor := map[uuid.UUID]int{}
	for _, one := range remote {
		ranking := rankCandidates(one, local, balances)
		row := RemoteAccountResponse{
			ExternalID:   one.ExternalID,
			Name:         one.Name,
			Kind:         string(one.Kind),
			Institution:  one.InstitutionName,
			MaskedNumber: one.MaskedNumber,
			Balance:      one.Balance,
			Currency:     one.Currency,
			Suggested:    ranking.ids,
			Likely:       ranking.likely,
			Match:        ranking.match,
		}
		if id, ok := claimed[one.ExternalID]; ok {
			row.LinkedAccountID = &id
		}
		if row.Likely != nil {
			likelyFor[*row.Likely]++
		}
		out.Remote = append(out.Remote, row)
	}
	// One local account receives one feed, so a guess two bank accounts share
	// chooses neither.
	for i := range out.Remote {
		if likely := out.Remote[i].Likely; likely != nil && likelyFor[*likely] > 1 {
			out.Remote[i].Likely = nil
			out.Remote[i].Match = MatchTie
		}
	}

	byExternal := map[uuid.UUID]string{}
	for _, one := range remote {
		if id, ok := claimed[one.ExternalID]; ok {
			byExternal[id] = one.Name
		}
	}
	for _, account := range local {
		out.Local = append(out.Local, LinkTargetResponse{
			ID:           account.ID,
			Name:         account.Name,
			Kind:         string(account.Kind),
			MaskedNumber: account.MaskedNumber,
			Balance:      balances[account.ID],
			SyncFloorOn:  account.SyncFloorOn,
			LinkedTo:     byExternal[account.ID],
		})
	}
	return out
}

// candidateRanking is rankCandidates' answer for one provider account.
type candidateRanking struct {
	ids    []uuid.UUID
	likely *uuid.UUID
	match  LinkMatch
}

// rankCandidates scores each local account against one provider account and
// returns the plausible ones, best first. A zero score is not returned. The
// first is likely only when an account number or a name backs it and no other
// account scores as high.
func rankCandidates(
	remote provider.Account, local []store.Account, balances map[uuid.UUID]domain.Money,
) candidateRanking {
	type scored struct {
		id         uuid.UUID
		score      int
		identifies LinkMatch
	}
	var ranked []scored

	for _, account := range local {
		score := 0
		var identifies LinkMatch
		if remote.MaskedNumber != "" && account.MaskedNumber != "" &&
			strings.EqualFold(remote.MaskedNumber, account.MaskedNumber) {
			score += 100
			identifies = MatchNumber
		}
		if account.Kind == remote.Kind {
			score += 10
		}
		if remote.InstitutionName != "" &&
			strings.Contains(strings.ToLower(account.Name), strings.ToLower(remote.InstitutionName)) {
			score += 8
		}
		if strings.EqualFold(strings.TrimSpace(account.Name), strings.TrimSpace(remote.Name)) {
			score += 20
			if identifies == "" {
				identifies = MatchName
			}
		}
		// Within a dollar. Balances drift between a bank's clock and ours, and
		// an exact match on cents would miss the pairing it is meant to find.
		if balance, known := balances[account.ID]; known {
			if balance.Sub(remote.Balance).Abs().Cmp(oneDollar) <= 0 {
				score += 5
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{id: account.ID, score: score, identifies: identifies})
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	out := candidateRanking{ids: make([]uuid.UUID, 0, len(ranked)), match: MatchNone}
	for _, one := range ranked {
		out.ids = append(out.ids, one.id)
	}
	switch {
	case len(ranked) == 0:
	case len(ranked) > 1 && ranked[1].score == ranked[0].score:
		out.match = MatchTie
	case ranked[0].identifies == "":
		out.match = MatchWeak
	default:
		first := ranked[0].id
		out.likely = &first
		out.match = ranked[0].identifies
	}
	return out
}

// oneDollar is the slack allowed when matching on balance. Banks and this
// ledger disagree by a pending charge more often than they disagree by nothing.
var oneDollar = func() domain.Money {
	dollar, err := domain.FromString("1.00")
	if err != nil {
		panic(err)
	}
	return dollar
}()

func (s connectionService) ListConnections(
	ctx context.Context, _ *agentifiv1.ListConnectionsRequest,
) (*agentifiv1.ListConnectionsResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	connections, err := env.DB.ListConnections(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	out := make([]*agentifiv1.Connection, 0, len(connections))
	for _, connection := range connections {
		row := connectionProto(connection)
		ignored, err := env.DB.ListIgnoredRemoteAccounts(ctx, sp.ID(), connection.ID)
		if err != nil {
			return nil, err
		}
		row.Ignored = ignoredRemoteProtos(ignored)
		out = append(out, row)
	}
	return &agentifiv1.ListConnectionsResponse{
		SimplefinEnabled: env.Live().SimpleFINEnabled,
		Connections:      out,
		Schedule:         syncSchedule(env),
	}, nil
}

// CreateConnection claims a setup token and stores the connection and its
// accounts. The first sync is a separate call the client makes next.
func (s connectionService) CreateConnection(
	ctx context.Context, req *agentifiv1.CreateConnectionRequest,
) (*agentifiv1.CreateConnectionResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if !env.Live().SimpleFINEnabled {
		return nil, errBadRequest("SimpleFIN is not enabled on this server")
	}
	if req.GetSetupToken() == "" {
		return nil, errInvalid("missing", []string{"body", "setup_token"}, "setup_token is required")
	}

	sync, err := connectionSync(env)
	if err != nil {
		return nil, err
	}
	connection, err := sync.Claim(ctx, sp.ID(), req.GetSetupToken(), req.GetName())
	if err != nil {
		return nil, claimError(err)
	}
	return &agentifiv1.CreateConnectionResponse{Connection: connectionProto(connection)}, nil
}

// SyncConnection starts a sync in the background and answers with the
// connection, whose `sync` says how the run stands; a run already going is
// joined. A parked or unmatched connection answers with a skipped run.
func (s connectionService) SyncConnection(
	ctx context.Context, req *agentifiv1.SyncConnectionRequest,
) (*agentifiv1.SyncConnectionResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if !env.Live().SimpleFINEnabled {
		return nil, errBadRequest("SimpleFIN is not enabled on this server")
	}
	connection, err := liveConnection(ctx, env, sp.ID(), req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	sync, err := connectionSync(env)
	if err != nil {
		return nil, err
	}
	if _, err := sync.StartSync(ctx, sp.ID(), connection.ID); err != nil {
		return nil, err
	}
	return &agentifiv1.SyncConnectionResponse{Connection: connectionProto(connection)}, nil
}

// ReplaceSetupToken re-credentials an existing connection, keeping the ids the
// ledger refers to. A second claim would duplicate every account.
func (s connectionService) ReplaceSetupToken(
	ctx context.Context, req *agentifiv1.ReplaceSetupTokenRequest,
) (*agentifiv1.ReplaceSetupTokenResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if !env.Live().SimpleFINEnabled {
		return nil, errBadRequest("SimpleFIN is not enabled on this server")
	}
	connection, err := liveConnection(ctx, env, sp.ID(), req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if req.GetSetupToken() == "" {
		return nil, errInvalid("missing", []string{"body", "setup_token"}, "setup_token is required")
	}

	sync, err := connectionSync(env)
	if err != nil {
		return nil, err
	}
	reclaimed, err := sync.Reclaim(ctx, sp.ID(), connection.ID, req.GetSetupToken())
	if err != nil {
		return nil, claimError(err)
	}
	return &agentifiv1.ReplaceSetupTokenResponse{Connection: connectionProto(reclaimed)}, nil
}

func (s connectionService) UpdateConnection(
	ctx context.Context, req *agentifiv1.UpdateConnectionRequest,
) (*agentifiv1.UpdateConnectionResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := liveConnection(ctx, s.env, sp.ID(), req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	if err := applyRequired("name", optOf(mask, "name", req.Name), &connection.Name); err != nil {
		return nil, err
	}
	if err := s.env.DB.UpdateConnection(ctx, sp.ID(), &connection); err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateConnectionResponse{Connection: connectionProto(connection)}, nil
}

// DeleteConnection stops syncing and keeps everything:
// accounts.connection_id is ON DELETE SET NULL, so the accounts become manual.
func (s connectionService) DeleteConnection(
	ctx context.Context, req *agentifiv1.DeleteConnectionRequest,
) (*agentifiv1.DeleteConnectionResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := liveConnection(ctx, s.env, sp.ID(), req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteConnection(ctx, sp.ID(), connection.ID); err != nil {
		return nil, notFoundAs(err, "Connection")
	}
	return &agentifiv1.DeleteConnectionResponse{}, nil
}

// UnlinkAccount detaches one account from its connection, leaving it manual
// with its register intact. "Make manual" in the account dialog.
func (s connectionService) UnlinkAccount(
	ctx context.Context, req *agentifiv1.UnlinkAccountRequest,
) (*agentifiv1.UnlinkAccountResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := liveConnection(ctx, s.env, sp.ID(), req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	id, err := idFrom(req.GetAccountId(), "Account")
	if err != nil {
		return nil, err
	}
	account, err := s.env.DB.GetAccount(ctx, sp.ID(), id)
	if err != nil {
		return nil, notFoundAs(err, "Account")
	}
	if account.ConnectionID != connection.ID {
		return nil, errNotFound("Account")
	}
	if err := s.env.DB.UnlinkAccount(ctx, sp.ID(), account.ID); err != nil {
		return nil, notFoundAs(err, "Account")
	}
	return &agentifiv1.UnlinkAccountResponse{}, nil
}

// liveConnection reads the connection an id names, treating one in another
// space as the same 404.
func liveConnection(ctx context.Context, env *Env, spaceID store.SpaceID, rawID string) (store.Connection, error) {
	id, err := idFrom(rawID, "Connection")
	if err != nil {
		return store.Connection{}, err
	}
	connection, err := env.DB.GetConnection(ctx, spaceID, id)
	if err != nil {
		return store.Connection{}, notFoundAs(err, "Connection")
	}
	if connection.IsDeleted {
		return store.Connection{}, errNotFound("Connection")
	}
	return connection, nil
}

// connectionSync builds the pipeline over a store bound to the credential
// cipher (Cfg.CredentialKey), bound here so only this resource holds a store
// that can read one.
func connectionSync(env *Env) (*service.Sync, error) {
	return NewSimpleFinSync(env)
}

// NewIngest is what follows every write of new rows, for each path that
// writes them.
func NewIngest(env *Env) service.Ingest {
	ingest := service.Ingest{
		Merchants: NewMerchants(env),
		Currency:  NewCurrency(env.Cfg, env.DB),
	}
	if automations, err := env.automations(); err == nil {
		ingest.Automations = automations
	}
	return ingest
}

// NewSimpleFinSync builds the pipeline for both the Sync now button and the
// scheduler in `serve`, so both bind the same key.
func NewSimpleFinSync(env *Env) (*service.Sync, error) {
	cfg, db := env.Cfg, env.DB
	cipher, err := store.NewCipher(cfg.CredentialKey())
	if err != nil {
		return nil, err
	}
	bank := &provider.SimpleFin{AllowPrivate: cfg.SimpleFINAllowPrivate}
	sync := service.NewSync(db.WithCipher(cipher), bank)
	// Swept once the rows have landed, so a household is told about what
	// arrived without having to open the app and press anything.
	alerts := service.NewAlerts(db)
	if cfg.EmailEnabled() {
		alerts.Mail = &provider.Mailer{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort,
			Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
			From: cfg.SMTPFrom, StartTLS: cfg.SMTPStartTLS,
		}
	}
	sync.Alerts = alerts
	sync.Ingest = NewIngest(env)
	return sync, nil
}

// claimError turns a refused claim into a sentence the user can act on. A
// setup token claimed twice comes back as a bare 403 from the Bridge.
func claimError(err error) error {
	var expired *provider.CredentialsExpiredError
	if errors.As(err, &expired) {
		return errBadRequest("%s", userFacing(expired.Reason))
	}
	if errors.Is(err, provider.ErrRateLimited) {
		return errBadRequest("SimpleFIN is rate limiting this server. Try again later.")
	}
	return errBadRequest("%s", userFacing(err.Error()))
}

// userFacing drops the connector's package prefix. "simplefin: " in front of a
// sentence is for a log line, not for somebody who has just pasted a token.
func userFacing(message string) string {
	trimmed := strings.TrimPrefix(message, "simplefin: ")
	if trimmed == "" {
		return message
	}
	return textutil.Capitalize(trimmed)
}

// syncSchedule describes the daily run, or says there is not one.
func syncSchedule(env *Env) *agentifiv1.ConnectionSyncSchedule {
	on := env.Cfg.SyncEnabled && env.Live().SimpleFINEnabled
	// The abbreviation ("UTC"): Location().String() is "Local" when the zone
	// came from TZ.
	zone, _ := env.now().Zone()
	schedule := &agentifiv1.ConnectionSyncSchedule{
		Enabled:  on,
		At:       env.Cfg.SyncAt.String(),
		TimeZone: zone,
	}
	if on {
		next := service.SyncWindow{
			Hour:   env.Cfg.SyncAt.Hour,
			Minute: env.Cfg.SyncAt.Minute,
		}.Next(env.now())
		schedule.NextRunAt = timestamppb.New(next)
	}
	return schedule
}

func connectionProto(c store.Connection) *agentifiv1.Connection {
	warnings := make([]*agentifiv1.BankWarning, 0, len(c.SyncErrors))
	for _, warning := range c.SyncErrors {
		warnings = append(warnings, &agentifiv1.BankWarning{
			Institution: warning.Institution,
			Message:     warning.Message,
			At:          timestamppb.New(warning.At),
		})
	}
	return &agentifiv1.Connection{
		Id:                   c.ID.String(),
		Name:                 c.Name,
		Status:               string(c.Status),
		StatusDetail:         dbconv.NullText(c.StatusDetail),
		NeedsSetupToken:      c.NeedsSetupToken(),
		BankWarnings:         warnings,
		Ignored:              []*agentifiv1.IgnoredRemoteAccount{},
		LastSyncAt:           timestampOrNil(c.LastSyncAt),
		LastSuccessfulSyncAt: timestampOrNil(c.LastSuccessfulSyncAt),
		RetryNotBefore:       timestampOrNil(c.RetryNotBefore),
		CreatedAt:            timestamppb.New(c.CreatedAt),
		Sync:                 syncProgressProto(c.ID),
	}
}

func syncProgressProto(connectionID uuid.UUID) *agentifiv1.ConnectionSyncProgress {
	progress, ok := service.SyncProgressOf(connectionID)
	if !ok {
		return nil
	}
	return &agentifiv1.ConnectionSyncProgress{
		State:                string(progress.State),
		Phase:                dbconv.NullText(string(progress.Phase)),
		Account:              int32(progress.Account),
		Accounts:             int32(progress.Accounts),
		AccountName:          dbconv.NullText(progress.AccountName),
		TransactionsImported: int32(progress.TransactionsImported),
		TransactionsUpdated:  int32(progress.TransactionsUpdated),
		AccountsCreated:      int32(progress.AccountsCreated),
		Warnings:             int32(progress.Warnings),
		BalancesHeld:         int32(progress.BalancesHeld),
		Message:              dbconv.NullText(progress.Message),
		StartedAt:            timestamppb.New(progress.StartedAt),
		FinishedAt:           timestampOrNil(progress.FinishedAt),
	}
}
