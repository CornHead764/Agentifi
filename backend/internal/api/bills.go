package api

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Bills: the household's logins at each provider, what each bills, the
// statements themselves, and the reminder each is attached to.
//
//   - No credential leaves here: store.BillConnection has no field for the
//     ciphertext, so nothing serialized from a listing can carry one.
//   - A biller is a value: the provider must be one of domain.Billers.
//
// What a bill does to a reminder is merged by the domain at read time and
// shown by /series and /occurrences.

func init() {
	Register(Resource{Prefix: "/bills", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/connections", listBillConnections)
		rt.Write(http.MethodPost, "/connections", createBillConnection)
		rt.Read(http.MethodGet, "/connections/{connection_id}", readBillConnection)
		rt.Write(http.MethodPatch, "/connections/{connection_id}", updateBillConnection)
		rt.Write(http.MethodDelete, "/connections/{connection_id}", deleteBillConnection)

		rt.Read(http.MethodGet, "/subaccounts", listBillSubaccounts)
		rt.Write(http.MethodPost, "/connections/{connection_id}/subaccounts", createBillSubaccount)
		rt.Write(http.MethodPatch, "/subaccounts/{subaccount_id}", updateBillSubaccount)
		rt.Read(http.MethodGet, "/subaccounts/{subaccount_id}/bills", listSubaccountBills)
		rt.Read(http.MethodGet, "/subaccounts/{subaccount_id}/suggested-reminder", suggestBillReminder)
		rt.Write(http.MethodPost, "/connections/{connection_id}/bills", createBill)
		rt.Write(http.MethodPost, "/statements/{bill_id}/paid", markBillPaid)

		rt.Write(http.MethodPost, "/links", createBillLink)
		rt.Write(http.MethodDelete, "/links/{series_id}", deleteBillLink)

		// The engine's status, then the routes that reach the browser. The
		// assistant's in-process dispatch is refused those that use a credential
		// or free a browser (dispatchDeniedRoutes); a pull and the challenge
		// listing stay open to it.
		rt.Read(http.MethodGet, "/agent", billAgentStatus)
		rt.Write(http.MethodPost, "/connections/{connection_id}/sign-in", startBillSignIn)
		rt.Read(http.MethodGet, "/connections/{connection_id}/sign-in/{session}", billSignInStatus)
		// A GET is a Read even here: route_contract_test.go refuses a
		// write-scoped GET.
		rt.Read(http.MethodGet, "/connections/{connection_id}/sign-in/{session}/trail", billSignInTrail)
		rt.Write(http.MethodPost, "/connections/{connection_id}/sign-in/{session}/answer", billSignIn.answerStep)
		rt.Write(http.MethodPost, "/connections/{connection_id}/sign-in/{session}/mailed-code", billSignIn.answerFromMail)
		rt.Write(http.MethodPost, "/connections/{connection_id}/sign-in/{session}/complete", completeBillSignIn)
		rt.Write(http.MethodDelete, "/connections/{connection_id}/sign-in/{session}", cancelBillSignIn)
		// The four developer steers, for writing a provider's module against a
		// live sign-in. Owner-only; see docs/connectors/bills.md.
		rt.Write(http.MethodPost, "/connections/{connection_id}/sign-in/{session}/goto", steerBillSignIn)
		rt.Write(http.MethodPost, "/connections/{connection_id}/sign-in/{session}/click", clickBillSignIn)
		rt.Write(http.MethodPost, "/connections/{connection_id}/sign-in/{session}/dom", readBillSignInDOM)
		rt.Write(http.MethodPost, "/connections/{connection_id}/sign-in/{session}/fetch", fetchFromBillSignIn)
		// Letting go of the browser: the session, password and profile all stay.
		// The only way to clear a lock a restart left on the profiles volume.
		rt.Write(http.MethodDelete, "/connections/{connection_id}/browser", releaseBillBrowser)
		rt.Write(http.MethodDelete, "/connections/{connection_id}/session", forgetBillSession)
		rt.Write(http.MethodDelete, "/connections/{connection_id}/credential", billSignIn.forgetCredential)
		rt.Write(http.MethodPost, "/connections/{connection_id}/pull", pullBillConnection)
		rt.Read(http.MethodGet, "/connections/{connection_id}/failure-screenshot", billFailureScreenshot)
		rt.Read(http.MethodGet, "/connections/{connection_id}/trail", billPullTrail)

		rt.Read(http.MethodGet, "/challenges", listBillChallenges)
		rt.Read(http.MethodGet, "/challenges/{challenge_id}", readBillChallenge)
		rt.Write(http.MethodPost, "/challenges/{challenge_id}/answer", answerBillChallenge)

	}})
}

// BillConnectionResponse is one signed-in account at one provider. Connected
// says a session is on file; there is deliberately no field for the session,
// nor for a password.
type BillConnectionResponse struct {
	ID       uuid.UUID `json:"id"`
	Biller   string    `json:"biller"`
	Label    string    `json:"label"`
	Username string    `json:"username"`
	// Site is which deployment this login is at, for a provider deployed per
	// customer. "" elsewhere, and at such a provider not yet given one.
	Site string `json:"site"`
	// CredentialSource is how a pull gets in: session, stored or typed.
	// "stored" is all a listing says about a kept password.
	CredentialSource string `json:"credential_source"`
	// HasTOTP says an authenticator setup key is sealed beside the password.
	HasTOTP bool `json:"has_totp"`
	// SecondFactor is "" (none or not sure), "email" (a code the billing
	// mailbox reads) or "totp". Never the key.
	SecondFactor string     `json:"second_factor"`
	Connected    bool       `json:"connected"`
	SignedInAt   *time.Time `json:"signed_in_at"`
	NeedsSignIn  bool       `json:"needs_sign_in"`
	// SignInPaused is why unattended sign-ins have stopped: "password_refused",
	// "code_needed", or "". The scheduler leaves the connection alone until a
	// person signs in, changes the password, or asks for a pull.
	SignInPaused string `json:"sign_in_paused"`

	AutopayRule      string     `json:"autopay_rule"`
	AutopayDays      *int       `json:"autopay_days"`
	AutopayDay       *int       `json:"autopay_day"`
	AutopayAccountID *uuid.UUID `json:"autopay_account_id"`

	PullEnabled    bool       `json:"pull_enabled"`
	PullAt         *string    `json:"pull_at"`
	LastPulledAt   *time.Time `json:"last_pulled_at"`
	LastPullStatus string     `json:"last_pull_status"`
	LastPullError  string     `json:"last_pull_error"`
	// HasFailureScreenshot says /failure-screenshot has the page the last pull
	// or unfinished sign-in stopped on, and HasTrail that
	// /trail has the unfinished sign-in's trail. LastPullStatus is
	// sign_in_failed for a sign-in a person started that never landed.
	HasFailureScreenshot bool `json:"has_failure_screenshot"`
	HasTrail             bool `json:"has_trail"`
	// Pulling says a pull of this connection is running now. The last_pull
	// fields describe the one before until it finishes.
	Pulling   bool      `json:"pulling"`
	CreatedAt time.Time `json:"created_at"`
}

type BillConnectionCreate struct {
	Biller           string         `json:"biller"`
	Label            string         `json:"label"`
	Username         string         `json:"username"`
	Site             string         `json:"site"`
	AutopayRule      Opt[string]    `json:"autopay_rule"`
	AutopayDays      Opt[int]       `json:"autopay_days"`
	AutopayDay       Opt[int]       `json:"autopay_day"`
	AutopayAccountID Opt[uuid.UUID] `json:"autopay_account_id"`
	PullEnabled      Opt[bool]      `json:"pull_enabled"`
	PullAt           Opt[string]    `json:"pull_at"`
}

type BillConnectionUpdate struct {
	Label            Opt[string]    `json:"label"`
	Username         Opt[string]    `json:"username"`
	Site             Opt[string]    `json:"site"`
	CredentialSource Opt[string]    `json:"credential_source"`
	AutopayRule      Opt[string]    `json:"autopay_rule"`
	AutopayDays      Opt[int]       `json:"autopay_days"`
	AutopayDay       Opt[int]       `json:"autopay_day"`
	AutopayAccountID Opt[uuid.UUID] `json:"autopay_account_id"`
	PullEnabled      Opt[bool]      `json:"pull_enabled"`
	PullAt           Opt[string]    `json:"pull_at"`
}

// BillSubaccountResponse is one thing a login bills.
type BillSubaccountResponse struct {
	ID           uuid.UUID `json:"id"`
	ConnectionID uuid.UUID `json:"connection_id"`
	Biller       string    `json:"biller"`
	ExternalID   string    `json:"external_id"`
	Label        string    `json:"label"`
	MaskedNumber *string   `json:"masked_number"`
	IsSelected   bool      `json:"is_selected"`
	// SeriesID is the reminder attached to this subaccount, when one is.
	SeriesID *uuid.UUID `json:"series_id"`
	// AccountID is the household account this billed account is — the card
	// or the loan its statements fill — when it is linked to one.
	AccountID *uuid.UUID `json:"account_id"`
}

// BillSubaccountCreate adds what a login bills by hand, which is what a
// provider nobody has connected yet needs: a pull would have found these.
type BillSubaccountCreate struct {
	// ExternalID is the provider's own key. Left out, the label stands in until
	// a pull finds one.
	ExternalID   string      `json:"external_id"`
	Label        string      `json:"label"`
	MaskedNumber Opt[string] `json:"masked_number"`
}

type BillSubaccountUpdate struct {
	Label        Opt[string] `json:"label"`
	MaskedNumber Opt[string] `json:"masked_number"`
	IsSelected   Opt[bool]   `json:"is_selected"`
	// AccountID links the billed account to the card or loan it is, and null
	// unlinks it. A card linked from another billed account moves here.
	AccountID Opt[uuid.UUID] `json:"account_id"`
}

// BillResponse is one statement.
type BillResponse struct {
	ID           uuid.UUID    `json:"id"`
	SubaccountID uuid.UUID    `json:"subaccount_id"`
	DueOn        Date         `json:"due_on"`
	AmountDue    domain.Money `json:"amount_due"`
	// MinimumDue is the minimum payment the statement names, when it names
	// one.
	MinimumDue  *domain.Money `json:"minimum_due"`
	Currency    string        `json:"currency"`
	IssuedOn    *Date         `json:"issued_on"`
	PeriodStart *Date         `json:"period_start"`
	PeriodEnd   *Date         `json:"period_end"`
	AutopayOn   *Date         `json:"autopay_on"`
	// PaysOn is when the money actually leaves: AutopayOn where the provider
	// stated one, otherwise the connection's rule applied to the due date.
	PaysOn       *Date      `json:"pays_on"`
	Status       string     `json:"status"`
	Source       string     `json:"source"`
	StatementURL string     `json:"statement_url"`
	DocumentID   *uuid.UUID `json:"document_id"`
	FetchedAt    time.Time  `json:"fetched_at"`
	// AmendedAt is when a later pull changed this cycle's figures. A corrected
	// bill is the same bill.
	AmendedAt *time.Time `json:"amended_at"`
}

// BillCreate is a statement entered by hand, which is how a bill reaches the
// record before any provider is connected.
type BillCreate struct {
	// SubaccountID may be left out when the connection bills exactly one
	// thing, which is the ordinary case for a manually kept provider.
	SubaccountID *uuid.UUID   `json:"subaccount_id"`
	DueOn        Date         `json:"due_on"`
	AmountDue    domain.Money `json:"amount_due"`
	// MinimumDue is a card or loan statement's minimum payment, a magnitude.
	MinimumDue   Opt[domain.Money] `json:"minimum_due"`
	Currency     Opt[string]       `json:"currency"`
	AutopayOn    Opt[Date]         `json:"autopay_on"`
	IssuedOn     Opt[Date]         `json:"issued_on"`
	PeriodStart  Opt[Date]         `json:"period_start"`
	PeriodEnd    Opt[Date]         `json:"period_end"`
	StatementURL Opt[string]       `json:"statement_url"`
}

// BillIngestResponse is what one write did to the record, in the same three
// counts a provider's pull reports.
type BillIngestResponse struct {
	New        int            `json:"new"`
	Amended    int            `json:"amended"`
	Unchanged  int            `json:"unchanged"`
	Superseded int            `json:"superseded"`
	Bills      []BillResponse `json:"bills"`
}

type BillLinkCreate struct {
	SeriesID     uuid.UUID `json:"series_id"`
	SubaccountID uuid.UUID `json:"subaccount_id"`
}

type BillLinkResponse struct {
	SeriesID     uuid.UUID `json:"series_id"`
	SubaccountID uuid.UUID `json:"subaccount_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// --- Connections -------------------------------------------------------------

func listBillConnections(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListBillConnections(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	out := make([]BillConnectionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, billConnectionResponse(row))
	}
	return writeJSON(w, http.StatusOK, out)
}

func createBillConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body BillConnectionCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	biller, known := domain.BillerByID(domain.BillerID(body.Biller))
	if !known {
		return errInvalid("unknown_biller", []string{"body", "biller"},
			"there is no bill provider called %q", body.Biller)
	}
	site, err := billSite(body.Site, biller)
	if err != nil {
		return err
	}

	connection := &store.BillConnection{
		Biller:           domain.BillerID(body.Biller),
		Label:            strings.TrimSpace(body.Label),
		Username:         body.Username,
		Site:             site,
		CredentialSource: store.BillCredentialSession,
		AutopayRule:      domain.AutopayNone,
		PullEnabled:      true,
	}
	if err := applyAutopay(body.AutopayRule, body.AutopayDays, body.AutopayDay, connection); err != nil {
		return err
	}
	applyNullable(body.AutopayAccountID, &connection.AutopayAccountID)
	if err := applyRequired("pull_enabled", body.PullEnabled, &connection.PullEnabled); err != nil {
		return err
	}
	applyNullable(body.PullAt, &connection.PullAt)

	if biller.Generic {
		// Nothing is ever pulled here, and the switch says so rather than
		// being left on for a pull that the scheduler would skip anyway.
		connection.PullEnabled = false
	}
	if err := checkBillName(biller, connection.Label); err != nil {
		return err
	}
	if err := checkBillLabelFree(env, r, sp, *connection, uuid.Nil); err != nil {
		return err
	}
	if err := env.DB.CreateBillConnection(r.Context(), sp.ID(), connection); err != nil {
		return err
	}
	// A provider with no sign-in bills by mail to the login itself, so its one
	// account is made here; a billed account never comes from a form.
	if biller.Access == domain.AccessEmail {
		named := connection.DisplayName()
		one := &store.BillSubaccount{
			ConnectionID: connection.ID, ExternalID: named, Label: named, IsSelected: true,
		}
		if err := env.DB.UpsertBillSubaccount(r.Context(), sp.ID(), one); err != nil {
			return err
		}
	}
	return writeJSON(w, http.StatusCreated, billConnectionResponse(*connection))
}

func readBillConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, billConnectionResponse(connection))
}

// billFailureScreenshot is the page the connection's last pull failed on.
func billFailureScreenshot(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	shot, err := env.DB.BillPullScreenshot(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return notFoundAs(err, "Failure screenshot")
	}
	return writeFailureScreenshot(w, shot)
}

// billPullTrail is the trail the connection's last unfinished sign-in
// kept, in the dialog's shape.
func billPullTrail(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	trail, err := billsService(env).PullTrail(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return notFoundAs(err, "Sign-in trail")
	}
	return writeJSON(w, http.StatusOK, billTrailEntries(trail))
}

func updateBillConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	var body BillConnectionUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if err := applyRequired("label", body.Label, &connection.Label); err != nil {
		return err
	}
	connection.Label = strings.TrimSpace(connection.Label)
	if biller, known := domain.BillerByID(connection.Biller); known {
		if err := checkBillName(biller, connection.Label); err != nil {
			return err
		}
	}
	applyNullable(body.Username, &connection.Username)
	if body.Site.Set {
		// A cleared site is allowed; an empty one is refused at the sign-in.
		biller, _ := domain.BillerByID(connection.Biller)
		site, err := billSite(body.Site.Value, biller)
		if err != nil {
			return err
		}
		connection.Site = site
	}
	if body.CredentialSource.Present() {
		switch body.CredentialSource.Value {
		case store.BillCredentialStored:
			// A password is kept by a sign-in that lands, never by a
			// settings save: there is nothing here to seal.
			if !connection.HasCredential {
				return errInvalid("invalid", []string{"body", "credential_source"},
					"no password is kept for this connection; sign in and keep the password first")
			}
			connection.CredentialSource = body.CredentialSource.Value
		case store.BillCredentialSession, store.BillCredentialTyped:
			connection.CredentialSource = body.CredentialSource.Value
		default:
			return errInvalid("invalid", []string{"body", "credential_source"},
				"credential_source must be session, stored or typed")
		}
	}
	if err := applyAutopay(body.AutopayRule, body.AutopayDays, body.AutopayDay, &connection); err != nil {
		return err
	}
	applyNullable(body.AutopayAccountID, &connection.AutopayAccountID)
	if err := applyRequired("pull_enabled", body.PullEnabled, &connection.PullEnabled); err != nil {
		return err
	}
	applyNullable(body.PullAt, &connection.PullAt)
	if err := checkBillLabelFree(env, r, sp, connection, connection.ID); err != nil {
		return err
	}
	// Moving off the kept password forgets it: a sealed secret nothing reads
	// or shows should not exist.
	if connection.HasCredential && connection.CredentialSource != store.BillCredentialStored {
		if err := env.DB.ClearBillConnectionCredential(r.Context(), sp.ID(), connection.ID); err != nil {
			return err
		}
		connection.HasCredential = false
	}
	if err := env.DB.UpdateBillConnection(r.Context(), sp.ID(), &connection); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, billConnectionResponse(connection))
}

// deleteBillConnection cascades to the subaccounts, their bills and their
// links. Nothing on a series is touched; a bill never writes there.
func deleteBillConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteBillConnection(r.Context(), sp.ID(), connection.ID),
		"Bill connection")
}

// applyAutopay writes the rule and the one figure its kind uses, clearing the
// other so a changed rule never answers with a stale date.
func applyAutopay(
	rule Opt[string], days, day Opt[int], connection *store.BillConnection,
) error {
	if rule.Present() {
		switch domain.AutopayRuleKind(rule.Value) {
		case domain.AutopayNone, domain.AutopayDaysBeforeDue,
			domain.AutopayOnDueDate, domain.AutopayDayOfMonth:
			connection.AutopayRule = domain.AutopayRuleKind(rule.Value)
		default:
			return errInvalid("invalid", []string{"body", "autopay_rule"},
				"autopay_rule must be none, days_before_due, on_due_date or day_of_month")
		}
	}
	if days.Present() {
		connection.AutopayDays = days.Value
	}
	if day.Present() {
		if day.Value < 1 || day.Value > 31 {
			return errInvalid("invalid", []string{"body", "autopay_day"},
				"autopay_day must be a day of the month")
		}
		connection.AutopayDayOfMonth = day.Value
	}
	switch connection.AutopayRule {
	case domain.AutopayDaysBeforeDue:
		connection.AutopayDayOfMonth = 0
	case domain.AutopayDayOfMonth:
		connection.AutopayDays = 0
	case domain.AutopayNone, domain.AutopayOnDueDate:
		connection.AutopayDays, connection.AutopayDayOfMonth = 0, 0
	}
	return nil
}

// --- Subaccounts and bills ---------------------------------------------------

func listBillSubaccounts(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connectionID, _, err := queryUUID(r, "connection_id")
	if err != nil {
		return err
	}
	rows, err := env.DB.ListBillSubaccounts(r.Context(), sp.ID(), connectionID)
	if err != nil {
		return err
	}
	connections, err := env.DB.ListBillConnections(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	billers := make(map[uuid.UUID]domain.BillerID, len(connections))
	for _, one := range connections {
		billers[one.ID] = one.Biller
	}
	links, err := env.DB.ListSeriesBillLinks(r.Context(), sp.ID(), nil)
	if err != nil {
		return err
	}
	linked := make(map[uuid.UUID]uuid.UUID, len(links))
	for _, link := range links {
		linked[link.SubaccountID] = link.SeriesID
	}

	out := make([]BillSubaccountResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, BillSubaccountResponse{
			ID: row.ID, ConnectionID: row.ConnectionID, Biller: string(billers[row.ConnectionID]),
			ExternalID: row.ExternalID, Label: row.Label,
			MaskedNumber: pgconv.NullText(row.MaskedNumber), IsSelected: row.IsSelected,
			SeriesID: pgconv.NullUUID(linked[row.ID]), AccountID: pgconv.NullUUID(row.AccountID),
		})
	}
	return writeJSON(w, http.StatusOK, out)
}

func createBillSubaccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	var body BillSubaccountCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Label == "" {
		return errInvalid("missing", []string{"body", "label"}, "label is required")
	}
	subaccount := &store.BillSubaccount{
		ConnectionID: connection.ID, ExternalID: body.ExternalID,
		Label: body.Label, IsSelected: true,
	}
	if subaccount.ExternalID == "" {
		subaccount.ExternalID = body.Label
	}
	applyNullable(body.MaskedNumber, &subaccount.MaskedNumber)
	if err := env.DB.UpsertBillSubaccount(r.Context(), sp.ID(), subaccount); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, BillSubaccountResponse{
		ID: subaccount.ID, ConnectionID: connection.ID, Biller: string(connection.Biller),
		ExternalID: subaccount.ExternalID, Label: subaccount.Label,
		MaskedNumber: pgconv.NullText(subaccount.MaskedNumber), IsSelected: subaccount.IsSelected,
	})
}

func updateBillSubaccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	subaccount, err := billSubaccount(r, env, sp)
	if err != nil {
		return err
	}
	var body BillSubaccountUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if err := applyRequired("label", body.Label, &subaccount.Label); err != nil {
		return err
	}
	applyNullable(body.MaskedNumber, &subaccount.MaskedNumber)
	if body.IsSelected.Present() && body.IsSelected.Value != subaccount.IsSelected {
		if err := env.DB.SetBillSubaccountSelected(
			r.Context(), sp.ID(), subaccount.ID, body.IsSelected.Value); err != nil {
			return err
		}
		subaccount.IsSelected = body.IsSelected.Value
	}
	if err := env.DB.UpsertBillSubaccount(r.Context(), sp.ID(), &subaccount); err != nil {
		return err
	}
	if body.AccountID.Set {
		if err := checkStatementAccount(r, env, sp, body.AccountID.Value); err != nil {
			return err
		}
		linked, err := service.NewBills(env.DB).LinkAccount(
			r.Context(), sp.ID(), subaccount.ID, body.AccountID.Value)
		if err != nil {
			return err
		}
		subaccount.AccountID = linked.AccountID
	}
	connection, err := env.DB.GetBillConnection(r.Context(), sp.ID(), subaccount.ConnectionID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, BillSubaccountResponse{
		ID: subaccount.ID, ConnectionID: subaccount.ConnectionID,
		Biller: string(connection.Biller), ExternalID: subaccount.ExternalID,
		Label: subaccount.Label, MaskedNumber: pgconv.NullText(subaccount.MaskedNumber),
		IsSelected: subaccount.IsSelected, AccountID: pgconv.NullUUID(subaccount.AccountID),
	})
}

// checkStatementAccount refuses a link to an account that is not this
// household's, or not billed by statement. Nil is an unlink.
func checkStatementAccount(r *http.Request, env *Env, sp auth.SpaceContext, id uuid.UUID) error {
	if id == uuid.Nil {
		return nil
	}
	account, err := env.DB.GetAccount(r.Context(), sp.ID(), id)
	if err != nil {
		if isNotFound(err) {
			return errInvalid("not_found", []string{"body", "account_id"}, "there is no such account")
		}
		return err
	}
	if account.IsDeleted {
		return errInvalid("not_found", []string{"body", "account_id"}, "there is no such account")
	}
	if !domain.HasStatement(account.Kind) {
		return errInvalid("invalid", []string{"body", "account_id"},
			"only a credit card or a loan has a statement for a bill to fill")
	}
	return nil
}

func listSubaccountBills(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	subaccount, err := billSubaccount(r, env, sp)
	if err != nil {
		return err
	}
	connection, err := env.DB.GetBillConnection(r.Context(), sp.ID(), subaccount.ConnectionID)
	if err != nil {
		return err
	}
	rows, err := env.DB.ListBills(r.Context(), sp.ID(), subaccount.ID)
	if err != nil {
		return err
	}
	rule := connection.AutopayRuleValue()
	out := make([]BillResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, billResponse(row, rule))
	}
	return writeJSON(w, http.StatusOK, out)
}

// markBillPaid takes a person's word that a statement was paid where the
// ledger cannot see, which ends its pay-manually reminder.
func markBillPaid(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	bill, err := fromPath(r, sp, "bill_id", "Bill", env.DB.GetBill)
	if err != nil {
		return err
	}
	if err := env.DB.MarkBillPaid(r.Context(), sp.ID(), bill.ID); err != nil {
		return err
	}
	return writeNoContent(w)
}

// createBill records a statement somebody read themselves, through
// service.Ingest so the same cycle twice is one bill and a newer cycle
// supersedes the old. The statement file may come in a multipart body.
func createBill(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	body, statement, err := billCreateBody(w, r)
	if err != nil {
		return err
	}
	defer removeMultipartTemp(r)
	if domain.Date(body.DueOn).IsZero() {
		return errInvalid("missing", []string{"body", "due_on"}, "due_on is required")
	}
	if body.AmountDue.IsNegative() {
		return errInvalid("invalid", []string{"body", "amount_due"},
			"amount_due is what is owed, which is a magnitude")
	}

	subaccount, err := billSubaccountFor(r, env, sp, connection, body.SubaccountID)
	if err != nil {
		return err
	}

	bill := store.Bill{
		DueOn: domain.Date(body.DueOn), AmountDue: body.AmountDue,
		Currency: sp.Space.PrimaryCurrency, Status: domain.BillOpen,
		Source: store.BillSourceManual, FetchedAt: env.now().UTC(),
	}
	if body.Currency.Present() {
		bill.Currency = body.Currency.Value
	}
	if body.MinimumDue.Present() {
		if body.MinimumDue.Value.IsNegative() {
			return errInvalid("invalid", []string{"body", "minimum_due"},
				"minimum_due is what is owed, which is a magnitude")
		}
		bill.MinimumDue, bill.HasMinimumDue = body.MinimumDue.Value, true
	}
	if body.AutopayOn.Present() {
		bill.AutopayOn = domain.Date(body.AutopayOn.Value)
	}
	if body.IssuedOn.Present() {
		bill.IssuedOn = domain.Date(body.IssuedOn.Value)
	}
	if body.PeriodStart.Present() {
		bill.PeriodStart = domain.Date(body.PeriodStart.Value)
	}
	if body.PeriodEnd.Present() {
		bill.PeriodEnd = domain.Date(body.PeriodEnd.Value)
	}
	applyNullable(body.StatementURL, &bill.StatementURL)

	documents := env.documents()
	upload := service.DocumentUpload{
		Bytes:            statement.Bytes,
		Filename:         statement.Filename,
		Source:           store.DocumentSourceUpload,
		UploadedByUserID: sp.UserID(),
		Link:             store.DocumentLink{Kind: store.DocumentLinkBill, Role: store.DocumentRoleStatement},
	}
	// Checked before the bill is written, so a refused file fails the whole
	// request rather than leaving a bill behind.
	if statement.given {
		if err := documents.Check(upload); err != nil {
			return documentStoreError(err, statement.Filename)
		}
	}

	result, err := service.NewBills(env.DB).Ingest(
		r.Context(), sp.ID(), subaccount.ID, []store.Bill{bill})
	if err != nil {
		return err
	}
	if stored, held := service.BillOfCycle(result.Bills, bill.DueOn, bill.Invoice); statement.given && held {
		document, err := documents.AttachStatement(r.Context(), sp.ID(), stored.ID, upload)
		if err != nil {
			return documentStoreError(err, statement.Filename)
		}
		for i := range result.Bills {
			if result.Bills[i].ID == stored.ID {
				result.Bills[i].DocumentID = document.ID
			}
		}
	}
	out := BillIngestResponse{
		New: result.New, Amended: result.Amended,
		Unchanged: result.Unchanged, Superseded: result.Superseded,
		Bills: make([]BillResponse, 0, len(result.Bills)),
	}
	rule := connection.AutopayRuleValue()
	for _, one := range result.Bills {
		out.Bills = append(out.Bills, billResponse(one, rule))
	}
	return writeJSON(w, http.StatusCreated, out)
}

// uploadedStatement is the file a hand-entered bill may arrive with.
type uploadedStatement struct {
	given bool
	uploadedFile
}

// billCreateBody reads a statement's own fields, and the statement file when
// one came with them. JSON or multipart: form values are re-encoded as the JSON
// object the same decoder reads, which works because every BillCreate field is
// a JSON string. A `bill` part carrying the whole object is accepted too.
func billCreateBody(w http.ResponseWriter, r *http.Request) (BillCreate, uploadedStatement, error) {
	var body BillCreate
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "multipart/form-data" {
		return body, uploadedStatement{}, decodeBody(r, &body)
	}
	if err := parseUpload(w, r, "document", service.MaxDocumentBytes); err != nil {
		return body, uploadedStatement{}, err
	}

	raw := []byte(strings.TrimSpace(r.FormValue("bill")))
	if len(raw) == 0 {
		fields := map[string]string{}
		for name, values := range r.MultipartForm.Value {
			if name == "bill" || len(values) == 0 {
				continue
			}
			fields[name] = values[0]
		}
		if raw, err = json.Marshal(fields); err != nil {
			return body, uploadedStatement{}, err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return body, uploadedStatement{}, decodeError(err, raw)
	}

	statement, found, err := readUpload(r, "document", service.MaxDocumentBytes)
	if err != nil {
		return body, uploadedStatement{}, err
	}
	return body, uploadedStatement{given: found, uploadedFile: statement}, nil
}

// --- Links -------------------------------------------------------------------

func createBillLink(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body BillLinkCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if _, err := service.NewSeriesMatcher(env.DB).GetSeries(r.Context(), sp.ID(), body.SeriesID); err != nil {
		return notFoundAs(err, "Series")
	}
	link, err := service.NewBills(env.DB).Link(r.Context(), sp.ID(), body.SeriesID, body.SubaccountID)
	if err != nil {
		return notFoundAs(err, "Bill subaccount")
	}
	return writeJSON(w, http.StatusCreated, BillLinkResponse{
		SeriesID: link.SeriesID, SubaccountID: link.SubaccountID, CreatedAt: link.CreatedAt,
	})
}

func deleteBillLink(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	seriesID, err := pathUUID(r, "series_id", "Bill link")
	if err != nil {
		return err
	}
	return deleted(w, service.NewBills(env.DB).Unlink(r.Context(), sp.ID(), seriesID), "Bill link")
}

// --- Loading and rendering ---------------------------------------------------

func billConnection(r *http.Request, env *Env, sp auth.SpaceContext) (store.BillConnection, error) {
	return fromPath(r, sp, "connection_id", "Bill connection", env.DB.GetBillConnection)
}

func billSubaccount(r *http.Request, env *Env, sp auth.SpaceContext) (store.BillSubaccount, error) {
	return fromPath(r, sp, "subaccount_id", "Bill subaccount", env.DB.GetBillSubaccount)
}

// billSubaccountFor resolves which of a connection's subaccounts a body names.
// A connection that bills exactly one thing needs no id.
func billSubaccountFor(
	r *http.Request, env *Env, sp auth.SpaceContext,
	connection store.BillConnection, named *uuid.UUID,
) (store.BillSubaccount, error) {
	rows, err := env.DB.ListBillSubaccounts(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return store.BillSubaccount{}, err
	}
	if named != nil {
		for _, row := range rows {
			if row.ID == *named {
				return row, nil
			}
		}
		return store.BillSubaccount{}, errNotFound("Bill subaccount")
	}
	switch len(rows) {
	case 0:
		return store.BillSubaccount{}, errConflict(
			"%s has nothing to bill yet; add a subaccount first", connection.DisplayName())
	case 1:
		return rows[0], nil
	default:
		return store.BillSubaccount{}, errInvalid("missing", []string{"body", "subaccount_id"},
			"%s bills several accounts, so the bill has to name one", connection.DisplayName())
	}
}

// checkBillName refuses an email-only connection with no name: the name is
// the company.
func checkBillName(biller domain.Biller, label string) error {
	if biller.Generic && strings.TrimSpace(label) == "" {
		return errInvalid("missing", []string{"body", "label"},
			"name the company: an email-only provider is called what you call it")
	}
	return nil
}

// checkBillLabelFree keeps the label unique per provider, as the schema does,
// so the refusal reads as a sentence rather than a constraint name.
func checkBillLabelFree(
	env *Env, r *http.Request, sp auth.SpaceContext, one store.BillConnection, self uuid.UUID,
) error {
	rows, err := env.DB.ListBillConnections(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Biller == one.Biller && row.Label == one.Label && row.ID != self {
			if strings.TrimSpace(one.Label) == "" {
				return errConflict("there is already a %s connection with no name; "+
					"give this one a name to tell the two apart", one.ProviderName())
			}
			return errConflict("a %s connection named %q already exists", one.ProviderName(), one.Label)
		}
	}
	return nil
}

func billConnectionResponse(one store.BillConnection) BillConnectionResponse {
	out := BillConnectionResponse{
		ID: one.ID, Biller: string(one.Biller), Label: one.Label, Username: one.Username,
		Site:             one.Site,
		CredentialSource: one.CredentialSource, HasTOTP: one.HasTOTP, Connected: one.HasSession,
		SecondFactor: string(one.SecondFactor),
		SignedInAt:   one.SignedInAt, NeedsSignIn: one.NeedsSignIn,
		SignInPaused:     one.SignInPausedFor,
		AutopayRule:      string(one.AutopayRule),
		AutopayAccountID: pgconv.NullUUID(one.AutopayAccountID),
		PullEnabled:      one.PullEnabled, PullAt: pgconv.NullText(one.PullAt),
		LastPulledAt: one.LastPulledAt, LastPullStatus: one.LastPullStatus,
		LastPullError: one.LastPullError, HasFailureScreenshot: one.HasFailureScreenshot,
		HasTrail:  one.HasTrail,
		Pulling:   service.BillPullRunning(one.ID),
		CreatedAt: one.CreatedAt,
	}
	if one.AutopayDays != 0 {
		days := one.AutopayDays
		out.AutopayDays = &days
	}
	if one.AutopayDayOfMonth != 0 {
		day := one.AutopayDayOfMonth
		out.AutopayDay = &day
	}
	return out
}

// billResponse renders one statement against the rule of the connection it
// came from, which is what resolves pays_on.
func billResponse(one store.Bill, rule domain.AutopayRule) BillResponse {
	return BillResponse{
		ID: one.ID, SubaccountID: one.SubaccountID, DueOn: Date(one.DueOn),
		AmountDue: one.AmountDue, MinimumDue: store.PtrIf(one.MinimumDue, one.HasMinimumDue),
		Currency: one.Currency,
		IssuedOn: nullableDate(one.IssuedOn), PeriodStart: nullableDate(one.PeriodStart),
		PeriodEnd: nullableDate(one.PeriodEnd), AutopayOn: nullableDate(one.AutopayOn),
		PaysOn: nullableDate(service.BillPaysOn(one, rule)),
		Status: string(one.Status), Source: one.Source, StatementURL: one.StatementURL,
		DocumentID: pgconv.NullUUID(one.DocumentID), FetchedAt: one.FetchedAt,
		AmendedAt: one.AmendedAt,
	}
}
