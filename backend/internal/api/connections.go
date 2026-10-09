package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
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
	Register(Resource{Prefix: "/connections", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listConnections)
		rt.Write(http.MethodPost, "/", createConnection)
		rt.Read(http.MethodGet, "/{connection_id}/candidates", listLinkCandidates)
		rt.Write(http.MethodPost, "/{connection_id}/links/finish", finishLinking)
		rt.Write(http.MethodDelete, "/{connection_id}/ignored/{ignored_id}", restoreRemoteAccount)
		rt.Write(http.MethodPost, "/{connection_id}/sync", syncConnection)
		rt.Write(http.MethodPost, "/{connection_id}/token", replaceSetupToken)
		rt.Write(http.MethodPatch, "/{connection_id}", updateConnection)
		rt.Write(http.MethodDelete, "/{connection_id}", deleteConnection)
		rt.Write(http.MethodDelete, "/{connection_id}/accounts/{account_id}", unlinkAccount)
	}})
}

// ConnectionResponse is one connection as the client reads it. There is
// deliberately no credential field of any kind.
type ConnectionResponse struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
	// StatusDetail is the sentence to show beside a failing connection.
	StatusDetail *string `json:"status_detail"`
	// NeedsSetupToken is the one failure the user acts on by pasting a new
	// token. A bank warning is not it.
	NeedsSetupToken bool `json:"needs_setup_token"`
	// BankWarnings are institutions inside a healthy connection that need
	// reauthorization at the Bridge.
	BankWarnings []BankWarningResponse `json:"bank_warnings"`

	// Ignored are accounts at the bank this connection must not create.
	Ignored []IgnoredAccountResponse `json:"ignored"`

	LastSyncAt           *time.Time `json:"last_sync_at"`
	LastSuccessfulSyncAt *time.Time `json:"last_successful_sync_at"`
	// RetryNotBefore is set while a throttled connection is parked. The
	// credential is fine and the UI says so.
	RetryNotBefore *time.Time `json:"retry_not_before"`
	CreatedAt      time.Time  `json:"created_at"`

	// Sync is the run in progress, or the last one this server ran; null when
	// it has run none since it started.
	Sync *SyncProgressResponse `json:"sync"`
}

// SyncProgressResponse is service.SyncProgress on the wire.
type SyncProgressResponse struct {
	// State is running, succeeded, failed or skipped.
	State string `json:"state"`
	// Phase is fetching, importing or settling while running, else null.
	Phase *string `json:"phase"`
	// Account is the position, from 1, of the account being read among
	// Accounts; once the run has ended, Accounts is how many it reached.
	Account              int        `json:"account"`
	Accounts             int        `json:"accounts"`
	AccountName          *string    `json:"account_name"`
	TransactionsImported int        `json:"transactions_imported"`
	TransactionsUpdated  int        `json:"transactions_updated"`
	AccountsCreated      int        `json:"accounts_created"`
	Warnings             int        `json:"warnings"`
	BalancesHeld         int        `json:"balances_held"`
	Message              *string    `json:"message"`
	StartedAt            time.Time  `json:"started_at"`
	FinishedAt           *time.Time `json:"finished_at"`
}

// IgnoredAccountResponse is one account at the bank that will not be created.
// The name is a snapshot from when it was ignored; nothing asks the bank again.
type IgnoredAccountResponse struct {
	ID           uuid.UUID `json:"id"`
	ExternalID   string    `json:"external_id"`
	Name         string    `json:"name"`
	Institution  string    `json:"institution"`
	MaskedNumber string    `json:"masked_number"`
	IgnoredAt    time.Time `json:"ignored_at"`
}

type BankWarningResponse struct {
	Institution string    `json:"institution"`
	Message     string    `json:"message"`
	At          time.Time `json:"at"`
}

// ConnectionListResponse is the collection plus whether this deployment can
// make a new one: SIMPLEFIN_ENABLED is off by default.
type ConnectionListResponse struct {
	SimpleFINEnabled bool                 `json:"simplefin_enabled"`
	Connections      []ConnectionResponse `json:"connections"`
	Schedule         SyncScheduleResponse `json:"schedule"`
}

// SyncScheduleResponse is when the server will sync by itself.
type SyncScheduleResponse struct {
	Enabled bool `json:"enabled"`
	// At is the window in the server's own timezone, "04:00", and TimeZone
	// names it.
	At        string     `json:"at"`
	TimeZone  string     `json:"time_zone"`
	NextRunAt *time.Time `json:"next_run_at"`
}

// ConnectionCreate is a claim. The setup token is single-use at the Bridge and
// write-only here: it is exchanged, and neither it nor what it buys comes back.
type ConnectionCreate struct {
	SetupToken string      `json:"setup_token"`
	Name       Opt[string] `json:"name"`
}

// SetupTokenReplacement re-credentials a connection whose Access URL was
// refused. Its own type, so a client cannot send a name and think it renamed.
type SetupTokenReplacement struct {
	SetupToken string `json:"setup_token"`
}

// ConnectionUpdate renames. Nothing else about a connection is the user's to
// set — the status is the Bridge's answer, not a preference.
type ConnectionUpdate struct {
	Name Opt[string] `json:"name"`
}

// LinkCandidatesResponse is the match screen: what the bank offers, and what
// the household already holds that it might be. Pairings are ranked as
// docs/importing.md specifies, and nothing is written until
// the user confirms each.
type LinkCandidatesResponse struct {
	Remote []RemoteAccountResponse `json:"remote"`
	// Local is every account no connection owns, in register order. The client,
	// not the server, stops one being paired twice.
	Local []LinkTargetResponse `json:"local"`
	// Ignored are the ones already refused, kept out of Remote so the match
	// screen asks only about accounts still awaiting a decision.
	Ignored []IgnoredAccountResponse `json:"ignored"`
}

// RemoteAccountResponse is one account the Bridge reaches.
type RemoteAccountResponse struct {
	ExternalID   string       `json:"external_id"`
	Name         string       `json:"name"`
	Kind         string       `json:"kind"`
	Institution  string       `json:"institution"`
	MaskedNumber string       `json:"masked_number"`
	Balance      domain.Money `json:"balance"`
	Currency     string       `json:"currency"`
	// LinkedAccountID is set once somebody has paired this one, so a reloaded
	// match screen shows the decisions already made.
	LinkedAccountID *uuid.UUID `json:"linked_account_id"`
	// Suggested is the local account this most likely is, best first.
	Suggested []uuid.UUID `json:"suggested"`
	// Likely is the first suggestion when it is sure enough for the match
	// screen to choose it, and Match says why it is or is not.
	Likely *uuid.UUID `json:"likely"`
	Match  LinkMatch  `json:"match"`
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
	ID           uuid.UUID    `json:"id"`
	Name         string       `json:"name"`
	Kind         string       `json:"kind"`
	MaskedNumber string       `json:"masked_number"`
	Balance      domain.Money `json:"balance"`
	// SyncFloorOn is the newest transaction this account holds, which becomes
	// the floor if it is paired.
	SyncFloorOn *Date `json:"sync_floor_on"`
	// LinkedTo names the provider account already chosen for it, if any.
	LinkedTo string `json:"linked_to"`
}

// FinishRequest is the match screen's every answer, applied together.
type FinishRequest struct {
	Choices []LinkChoiceRequest `json:"choices"`
}

// LinkChoiceRequest is one account at the bank: "link" names the household's
// account in AccountID, "create" makes a new one, "ignore" refuses it.
type LinkChoiceRequest struct {
	ExternalID string     `json:"external_id"`
	Action     string     `json:"action"`
	AccountID  *uuid.UUID `json:"account_id"`
}

func listLinkCandidates(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if !env.Live().SimpleFINEnabled {
		return errBadRequest("SimpleFIN is not enabled on this server")
	}
	connection, err := liveConnection(r, env, sp)
	if err != nil {
		return err
	}
	sync, err := connectionSync(env)
	if err != nil {
		return err
	}
	remote, err := sync.DiscoverAccounts(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return err
	}

	accounts, err := env.DB.ListAccounts(r.Context(), sp.ID(), store.AccountQuery{})
	if err != nil {
		return err
	}
	// The same balances the accounts listing shows, from the same helper.
	postings, err := postingsByAccount(r.Context(), env, sp, accounts)
	if err != nil {
		return err
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
	ignored, err := env.DB.ListIgnoredRemoteAccounts(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return err
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
	candidates.Ignored = ignoredResponses(ignored)
	return writeJSON(w, http.StatusOK, candidates)
}

// finishLinking applies the match screen's choices, then starts the first
// sync. The accounts exist when it answers; their transactions follow.
func finishLinking(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if !env.Live().SimpleFINEnabled {
		return errBadRequest("SimpleFIN is not enabled on this server")
	}
	connection, err := liveConnection(r, env, sp)
	if err != nil {
		return err
	}
	var body FinishRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	choices := make([]service.LinkChoice, 0, len(body.Choices))
	for i, one := range body.Choices {
		choice := service.LinkChoice{ExternalID: one.ExternalID, Action: service.LinkAction(one.Action)}
		if choice.Action == service.LinkToAccount {
			if one.AccountID == nil {
				return errInvalid("missing", []string{"body", "choices", strconv.Itoa(i), "account_id"},
					"name the account to pair with")
			}
			choice.AccountID = *one.AccountID
		}
		choices = append(choices, choice)
	}
	sync, err := connectionSync(env)
	if err != nil {
		return err
	}
	updated, err := sync.FinishLinking(r.Context(), sp.ID(), connection.ID, choices)
	if err != nil {
		return linkError(err)
	}
	if _, err := sync.StartSync(r.Context(), sp.ID(), connection.ID); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, connectionResponse(updated))
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

// restoreRemoteAccount lifts a refusal. The account is not re-created here:
// the next sync does that, which is the same path every other account takes.
func restoreRemoteAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := liveConnection(r, env, sp)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "ignored_id", "Ignored account")
	if err != nil {
		return err
	}
	restored, err := env.DB.RestoreRemoteAccount(r.Context(), sp.ID(), connection.ID, id)
	if err != nil {
		return err
	}
	if !restored {
		return errNotFound("Ignored account")
	}
	return writeNoContent(w)
}

func ignoredResponse(one store.IgnoredRemoteAccount) IgnoredAccountResponse {
	return IgnoredAccountResponse{
		ID:           one.ID,
		ExternalID:   one.ExternalID,
		Name:         one.Name,
		Institution:  one.Institution,
		MaskedNumber: one.MaskedNumber,
		IgnoredAt:    one.IgnoredAt,
	}
}

func ignoredResponses(all []store.IgnoredRemoteAccount) []IgnoredAccountResponse {
	out := make([]IgnoredAccountResponse, 0, len(all))
	for _, one := range all {
		out = append(out, ignoredResponse(one))
	}
	return out
}

// buildLinkCandidates pairs what the bank offers against what the household
// holds, and ranks the guesses: masked number, then name, kind and
// institution, then balance within a dollar. Every pairing is a suggestion the
// user confirms, a likely one included.
func buildLinkCandidates(
	remote []provider.Account, local []store.Account, balances map[uuid.UUID]domain.Money,
) LinkCandidatesResponse {
	out := LinkCandidatesResponse{
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
		target := LinkTargetResponse{
			ID:           account.ID,
			Name:         account.Name,
			Kind:         string(account.Kind),
			MaskedNumber: account.MaskedNumber,
			Balance:      balances[account.ID],
			LinkedTo:     byExternal[account.ID],
		}
		if !account.SyncFloorOn.IsZero() {
			floor := Date(account.SyncFloorOn)
			target.SyncFloorOn = &floor
		}
		out.Local = append(out.Local, target)
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

func listConnections(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connections, err := env.DB.ListConnections(r.Context(), sp.ID(), false)
	if err != nil {
		return err
	}
	out := make([]ConnectionResponse, 0, len(connections))
	for _, connection := range connections {
		response := connectionResponse(connection)
		ignored, err := env.DB.ListIgnoredRemoteAccounts(r.Context(), sp.ID(), connection.ID)
		if err != nil {
			return err
		}
		response.Ignored = ignoredResponses(ignored)
		out = append(out, response)
	}
	return writeJSON(w, http.StatusOK, ConnectionListResponse{
		SimpleFINEnabled: env.Live().SimpleFINEnabled,
		Connections:      out,
		Schedule:         syncSchedule(env),
	})
}

// createConnection claims a setup token and stores the connection and its
// accounts. The first sync is a separate call the client makes next.
func createConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if !env.Live().SimpleFINEnabled {
		return errBadRequest("SimpleFIN is not enabled on this server")
	}
	var body ConnectionCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.SetupToken == "" {
		return errInvalid("missing", []string{"body", "setup_token"}, "setup_token is required")
	}
	name := ""
	if err := applyRequired("name", body.Name, &name); err != nil {
		return err
	}

	sync, err := connectionSync(env)
	if err != nil {
		return err
	}
	connection, err := sync.Claim(r.Context(), sp.ID(), body.SetupToken, name)
	if err != nil {
		return claimError(err)
	}
	return writeJSON(w, http.StatusCreated, connectionResponse(connection))
}

// syncConnection starts a sync in the background and answers with the
// connection, whose `sync` says how the run stands; a run already going is
// joined. A parked or unmatched connection answers with a skipped run.
func syncConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if !env.Live().SimpleFINEnabled {
		return errBadRequest("SimpleFIN is not enabled on this server")
	}
	connection, err := liveConnection(r, env, sp)
	if err != nil {
		return err
	}
	sync, err := connectionSync(env)
	if err != nil {
		return err
	}
	if _, err := sync.StartSync(r.Context(), sp.ID(), connection.ID); err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, connectionResponse(connection))
}

// replaceSetupToken re-credentials an existing connection, keeping the ids the
// ledger refers to. A second claim would duplicate every account.
func replaceSetupToken(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if !env.Live().SimpleFINEnabled {
		return errBadRequest("SimpleFIN is not enabled on this server")
	}
	connection, err := liveConnection(r, env, sp)
	if err != nil {
		return err
	}
	var body SetupTokenReplacement
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.SetupToken == "" {
		return errInvalid("missing", []string{"body", "setup_token"}, "setup_token is required")
	}

	sync, err := connectionSync(env)
	if err != nil {
		return err
	}
	reclaimed, err := sync.Reclaim(r.Context(), sp.ID(), connection.ID, body.SetupToken)
	if err != nil {
		return claimError(err)
	}
	return writeJSON(w, http.StatusOK, connectionResponse(reclaimed))
}

func updateConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := liveConnection(r, env, sp)
	if err != nil {
		return err
	}
	var body ConnectionUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if err := applyRequired("name", body.Name, &connection.Name); err != nil {
		return err
	}
	if err := env.DB.UpdateConnection(r.Context(), sp.ID(), &connection); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, connectionResponse(connection))
}

// deleteConnection stops syncing and keeps everything:
// accounts.connection_id is ON DELETE SET NULL, so the accounts become manual.
func deleteConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := liveConnection(r, env, sp)
	if err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteConnection(r.Context(), sp.ID(), connection.ID), "Connection")
}

// unlinkAccount detaches one account from its connection, leaving it manual
// with its register intact. "Make manual" in the account dialog.
func unlinkAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := liveConnection(r, env, sp)
	if err != nil {
		return err
	}
	account, err := fromPath(r, sp, "account_id", "Account", env.DB.GetAccount)
	if err != nil {
		return err
	}
	if account.ConnectionID != connection.ID {
		return errNotFound("Account")
	}
	return deleted(w, env.DB.UnlinkAccount(r.Context(), sp.ID(), account.ID), "Account")
}

// liveConnection reads the connection named in the path, treating one in
// another space as the same 404.
func liveConnection(r *http.Request, env *Env, sp auth.SpaceContext) (store.Connection, error) {
	connection, err := fromPath(r, sp, "connection_id", "Connection", env.DB.GetConnection)
	if err != nil {
		return store.Connection{}, err
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
		Merchants:  NewMerchants(env),
		Currency:   NewCurrency(env.Cfg, env.DB),
		Duplicates: service.NewDuplicates(env.DB),
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
func syncSchedule(env *Env) SyncScheduleResponse {
	on := env.Cfg.SyncEnabled && env.Live().SimpleFINEnabled
	// The abbreviation ("UTC"): Location().String() is "Local" when the zone
	// came from TZ.
	zone, _ := env.now().Zone()
	schedule := SyncScheduleResponse{
		Enabled:  on,
		At:       env.Cfg.SyncAt.String(),
		TimeZone: zone,
	}
	if on {
		next := service.SyncWindow{
			Hour:   env.Cfg.SyncAt.Hour,
			Minute: env.Cfg.SyncAt.Minute,
		}.Next(env.now())
		schedule.NextRunAt = &next
	}
	return schedule
}

func connectionResponse(c store.Connection) ConnectionResponse {
	warnings := make([]BankWarningResponse, 0, len(c.SyncErrors))
	for _, warning := range c.SyncErrors {
		warnings = append(warnings, BankWarningResponse{
			Institution: warning.Institution,
			Message:     warning.Message,
			At:          warning.At,
		})
	}
	return ConnectionResponse{
		ID:                   c.ID,
		Name:                 c.Name,
		Status:               string(c.Status),
		StatusDetail:         pgconv.NullText(c.StatusDetail),
		NeedsSetupToken:      c.NeedsSetupToken(),
		BankWarnings:         warnings,
		Ignored:              []IgnoredAccountResponse{},
		LastSyncAt:           c.LastSyncAt,
		LastSuccessfulSyncAt: c.LastSuccessfulSyncAt,
		RetryNotBefore:       c.RetryNotBefore,
		CreatedAt:            c.CreatedAt,
		Sync:                 syncProgressResponse(c.ID),
	}
}

func syncProgressResponse(connectionID uuid.UUID) *SyncProgressResponse {
	progress, ok := service.SyncProgressOf(connectionID)
	if !ok {
		return nil
	}
	return &SyncProgressResponse{
		State:                string(progress.State),
		Phase:                pgconv.NullText(string(progress.Phase)),
		Account:              progress.Account,
		Accounts:             progress.Accounts,
		AccountName:          pgconv.NullText(progress.AccountName),
		TransactionsImported: progress.TransactionsImported,
		TransactionsUpdated:  progress.TransactionsUpdated,
		AccountsCreated:      progress.AccountsCreated,
		Warnings:             progress.Warnings,
		BalancesHeld:         progress.BalancesHeld,
		Message:              pgconv.NullText(progress.Message),
		StartedAt:            progress.StartedAt,
		FinishedAt:           progress.FinishedAt,
	}
}
