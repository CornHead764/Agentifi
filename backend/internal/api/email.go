package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The watched mailbox: where a mailed-only bill comes from, and where a parked
// sign-in's code is read.
//
// A mailbox's secret is read access to all the household's mail, so no response
// has a field for it: store.EmailConnection carries only a boolean, and the
// cursor is left out too because a Graph delta link is a capability URL. The
// sign-in and secret routes are in dispatchDeniedRoutes.

func init() {
	Register(Resource{Prefix: "/email", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/connections", listEmailConnections)
		rt.Write(http.MethodPost, "/connections", createEmailConnection)
		rt.Read(http.MethodGet, "/connections/{connection_id}", readEmailConnection)
		rt.Write(http.MethodPatch, "/connections/{connection_id}", updateEmailConnection)
		rt.Write(http.MethodDelete, "/connections/{connection_id}", deleteEmailConnection)

		rt.Write(http.MethodPost, "/connections/{connection_id}/sign-in", startEmailSignIn)
		rt.Read(http.MethodGet, "/connections/{connection_id}/sign-in/{session}", emailSignInState)
		rt.Write(http.MethodDelete, "/connections/{connection_id}/secret", forgetEmailSecret)

		rt.Write(http.MethodPost, "/connections/{connection_id}/poll", pollEmailConnection)
		rt.Read(http.MethodGet, "/connections/{connection_id}/messages", listEmailMessages)
		rt.Write(http.MethodPost,
			"/connections/{connection_id}/messages/{message_id}/reread", rereadEmailMessage)

		rt.Read(http.MethodGet, "/messages", listAllEmailMessages)
		rt.Read(http.MethodGet, "/messages/{message_id}/text", readEmailMessageText)
		rt.Write(http.MethodPost, "/messages/{message_id}/suggest-rule", suggestMailRule)

		rt.Read(http.MethodGet, "/rules", listMailRules)
		rt.Write(http.MethodPost, "/rules", createMailRule)
		rt.Write(http.MethodPost, "/rules/try", tryMailRule)
		rt.Write(http.MethodPatch, "/rules/{rule_id}", updateMailRule)
		rt.Write(http.MethodDelete, "/rules/{rule_id}", deleteMailRule)
	}})
}

// EmailConnectionResponse is one watched mailbox. Connected says a secret is
// sealed on the row; there is deliberately no field for it or the cursor.
type EmailConnectionResponse struct {
	ID       uuid.UUID `json:"id"`
	Label    string    `json:"label"`
	Kind     string    `json:"kind"`
	Address  string    `json:"address"`
	ClientID string    `json:"client_id"`
	Tenant   string    `json:"tenant"`
	Host     string    `json:"host"`
	Port     *int      `json:"port"`
	Username string    `json:"username"`
	Folder   string    `json:"folder"`
	Enabled  bool      `json:"enabled"`
	// Connected says the mailbox has something to sign in with.
	Connected     bool       `json:"connected"`
	LastPolledAt  *time.Time `json:"last_polled_at"`
	LastPollError string     `json:"last_poll_error"`
	CreatedAt     time.Time  `json:"created_at"`
}

type EmailConnectionCreate struct {
	Label    string      `json:"label"`
	Kind     string      `json:"kind"`
	Address  string      `json:"address"`
	ClientID Opt[string] `json:"client_id"`
	Tenant   Opt[string] `json:"tenant"`
	Host     Opt[string] `json:"host"`
	Port     Opt[int]    `json:"port"`
	Username Opt[string] `json:"username"`
	Folder   Opt[string] `json:"folder"`
}

type EmailConnectionUpdate struct {
	Label    Opt[string] `json:"label"`
	Address  Opt[string] `json:"address"`
	ClientID Opt[string] `json:"client_id"`
	Tenant   Opt[string] `json:"tenant"`
	Host     Opt[string] `json:"host"`
	Port     Opt[int]    `json:"port"`
	Username Opt[string] `json:"username"`
	Folder   Opt[string] `json:"folder"`
	Enabled  Opt[bool]   `json:"enabled"`
}

// EmailSignInRequest is a password for an IMAP mailbox and nothing at all for
// an Office 365 one, which signs in on microsoft.com with a device code.
type EmailSignInRequest struct {
	Password string `json:"password"`
}

// EmailDeviceCodeResponse is what the dialog shows while Microsoft waits.
type EmailDeviceCodeResponse struct {
	SessionID       string    `json:"session_id"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// EmailSignInStateResponse is how a waiting device-code sign-in is going.
type EmailSignInStateResponse struct {
	State string `json:"state"`
	Error string `json:"error"`
}

// EmailPollResponse is what one poll did.
type EmailPollResponse struct {
	Read         int    `json:"read"`
	Bills        int    `json:"bills"`
	Rules        int    `json:"rules"`
	OTPs         int    `json:"otps"`
	Unrecognised int    `json:"unrecognised"`
	Proposed     int    `json:"proposed"`
	Failed       int    `json:"failed"`
	Error        string `json:"error"`
}

// EmailMessageResponse is one row of the per-message log. No body and no code:
// a passcode is not kept at all.
type EmailMessageResponse struct {
	ID           uuid.UUID  `json:"id"`
	ConnectionID uuid.UUID  `json:"connection_id"`
	MessageID    string     `json:"message_id"`
	ReceivedAt   time.Time  `json:"received_at"`
	Sender       string     `json:"sender"`
	Subject      string     `json:"subject"`
	Biller       string     `json:"biller"`
	Outcome      string     `json:"outcome"`
	Note         string     `json:"note"`
	BillID       *uuid.UUID `json:"bill_id"`
	DocumentID   *uuid.UUID `json:"document_id"`
	// RuleID is the household's rule that claimed the message, and
	// TransactionID the row it posted.
	RuleID        *uuid.UUID `json:"rule_id"`
	TransactionID *uuid.UUID `json:"transaction_id"`
	// BillConnectionID is the connection a filed bill is on; Biller alone
	// cannot tell two connections apart.
	BillConnectionID *uuid.UUID `json:"bill_connection_id"`
}

func listEmailConnections(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListEmailConnections(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	out := make([]EmailConnectionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, emailConnectionResponse(row))
	}
	return writeJSON(w, http.StatusOK, out)
}

func createEmailConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body EmailConnectionCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Label == "" {
		return errInvalid("missing", []string{"body", "label"}, "label is required")
	}
	connection := &store.EmailConnection{
		Label: body.Label, Kind: body.Kind, Address: body.Address,
		Folder: "Inbox", Enabled: true,
	}
	applyNullable(body.ClientID, &connection.ClientID)
	applyNullable(body.Tenant, &connection.Tenant)
	applyNullable(body.Host, &connection.Host)
	applyNullable(body.Username, &connection.Username)
	if err := applyRequired("port", body.Port, &connection.Port); err != nil {
		return err
	}
	if body.Folder.Present() && body.Folder.Value != "" {
		connection.Folder = body.Folder.Value
	}
	if err := checkMailbox(connection); err != nil {
		return err
	}
	if err := checkMailboxLabelFree(env, r, sp, *connection, uuid.Nil); err != nil {
		return err
	}
	if err := env.DB.CreateEmailConnection(r.Context(), sp.ID(), connection); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, emailConnectionResponse(*connection))
}

func readEmailConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := emailConnection(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, emailConnectionResponse(connection))
}

// updateEmailConnection writes the fields a person owns. Repointing the reader
// resets the cursor, since a delta link or IMAP UID belongs to one folder; the
// lookback re-read skips nothing.
func updateEmailConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := emailConnection(r, env, sp)
	if err != nil {
		return err
	}
	var body EmailConnectionUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	before := connection
	if err := applyRequired("label", body.Label, &connection.Label); err != nil {
		return err
	}
	applyNullable(body.Address, &connection.Address)
	applyNullable(body.ClientID, &connection.ClientID)
	applyNullable(body.Tenant, &connection.Tenant)
	applyNullable(body.Host, &connection.Host)
	applyNullable(body.Username, &connection.Username)
	if err := applyRequired("port", body.Port, &connection.Port); err != nil {
		return err
	}
	if body.Folder.Present() && body.Folder.Value != "" {
		connection.Folder = body.Folder.Value
	}
	if err := applyRequired("enabled", body.Enabled, &connection.Enabled); err != nil {
		return err
	}
	if err := checkMailbox(&connection); err != nil {
		return err
	}
	if err := checkMailboxLabelFree(env, r, sp, connection, connection.ID); err != nil {
		return err
	}
	if err := env.DB.UpdateEmailConnection(r.Context(), sp.ID(), &connection); err != nil {
		return err
	}
	if movedMailbox(before, connection) {
		if err := env.DB.SaveEmailCursor(r.Context(), sp.ID(), connection.ID, nil); err != nil {
			return err
		}
		connection.Cursor = nil
	}
	return writeJSON(w, http.StatusOK, emailConnectionResponse(connection))
}

func deleteEmailConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := emailConnection(r, env, sp)
	if err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteEmailConnection(r.Context(), sp.ID(), connection.ID),
		"Email connection")
}

// startEmailSignIn connects the mailbox: a device code for Office 365, an app
// password for IMAP. The password is checked against the server before it is
// sealed, and is in no response and no log line.
func startEmailSignIn(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := emailConnection(r, env, sp)
	if err != nil {
		return err
	}
	var body EmailSignInRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	mailbox := NewMailbox(env)

	if connection.Kind == store.EmailKindGraph {
		started, err := mailbox.StartGraphSignIn(r.Context(), sp.ID(), connection.ID)
		if err != nil {
			return errInvalid("sign_in_failed", []string{"body"}, "%s", err.Error())
		}
		return writeJSON(w, http.StatusOK, EmailDeviceCodeResponse{
			SessionID: started.SessionID, UserCode: started.UserCode,
			VerificationURI: started.VerificationURI, ExpiresAt: started.ExpiresAt,
		})
	}

	if err := mailbox.SetIMAPPassword(r.Context(), sp.ID(), connection.ID, body.Password); err != nil {
		// The server's own refusal, which is what the dialog shows. It names
		// the host and the reason and never the password.
		return errInvalid("sign_in_failed", []string{"body", "password"}, "%s", err.Error())
	}
	connection.HasSecret = true
	return writeJSON(w, http.StatusOK, emailConnectionResponse(connection))
}

func emailSignInState(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := emailConnection(r, env, sp)
	if err != nil {
		return err
	}
	state, detail := NewMailbox(env).GraphSignInState(sp.ID(), connection.ID, chi.URLParam(r, "session"))
	return writeJSON(w, http.StatusOK, EmailSignInStateResponse{State: state, Error: detail})
}

// forgetEmailSecret disconnects the mailbox and leaves everything else: the
// message log is what was read, whoever is signed in now.
func forgetEmailSecret(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := emailConnection(r, env, sp)
	if err != nil {
		return err
	}
	if err := env.DB.ClearEmailSecret(r.Context(), sp.ID(), connection.ID); err != nil {
		return err
	}
	connection.HasSecret = false
	return writeJSON(w, http.StatusOK, emailConnectionResponse(connection))
}

func pollEmailConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := emailConnection(r, env, sp)
	if err != nil {
		return err
	}
	result, err := NewMailbox(env).Poll(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, EmailPollResponse{
		Read: result.Read, Bills: result.Bills, Rules: result.Rules, OTPs: result.OTPs,
		Unrecognised: result.Unrecognised, Proposed: result.Proposed,
		Failed: result.Failed, Error: result.Error,
	})
}

func listEmailMessages(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := emailConnection(r, env, sp)
	if err != nil {
		return err
	}
	limit, err := queryLimit(r, 50, 500)
	if err != nil {
		return err
	}
	rows, err := env.DB.ListBillEmails(r.Context(), sp.ID(), connection.ID, limit)
	if err != nil {
		return err
	}
	out, err := emailMessageResponses(env, r, sp, rows)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// rereadEmailMessage reads one logged message again against today's rules and
// parsers. The poll skips logged messages, so this is how a new rule reaches
// old mail.
func rereadEmailMessage(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := emailConnection(r, env, sp)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "message_id", "Message")
	if err != nil {
		return err
	}
	row, err := NewMailbox(env).Reread(r.Context(), sp.ID(), connection.ID, id)
	switch {
	case errors.Is(err, service.ErrMailAlreadyRead):
		return errConflict("%s", err.Error())
	case errors.Is(err, service.ErrMailGone):
		return errNotFound("Message")
	case isNotFound(err):
		return errNotFound("Message")
	case err != nil:
		return err
	}
	out, err := emailMessageResponses(env, r, sp, []store.BillEmail{row})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out[0])
}

// emailMessageResponses is the log's rows as the page reads them, each filed
// bill with the connection it is on.
func emailMessageResponses(
	env *Env, r *http.Request, sp auth.SpaceContext, rows []store.BillEmail,
) ([]EmailMessageResponse, error) {
	var bills []uuid.UUID
	for _, row := range rows {
		if row.BillID != uuid.Nil {
			bills = append(bills, row.BillID)
		}
	}
	filedOn, err := env.DB.BillConnectionsOf(r.Context(), sp.ID(), bills)
	if err != nil {
		return nil, err
	}
	out := make([]EmailMessageResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, EmailMessageResponse{
			ID: row.ID, ConnectionID: row.ConnectionID, MessageID: row.MessageID,
			ReceivedAt: row.ReceivedAt, Sender: row.Sender, Subject: row.Subject,
			Biller: string(row.Biller), Outcome: row.Outcome, Note: row.Note,
			BillID: pgconv.NullUUID(row.BillID), DocumentID: pgconv.NullUUID(row.DocumentID),
			RuleID: pgconv.NullUUID(row.RuleID), TransactionID: pgconv.NullUUID(row.TransactionID),
			BillConnectionID: pgconv.NullUUID(filedOn[row.BillID]),
		})
	}
	return out, nil
}

// NewMailbox builds the reader over this environment.
func NewMailbox(env *Env) *service.Mailbox {
	st := env.DB
	if sealed, err := sealedStore(env); err == nil {
		st = sealed
	}
	mailbox := service.NewMailbox(st)
	mailbox.Now = env.now
	mailbox.Open = openMailbox
	mailbox.Bills = NewBills(env)
	mailbox.Documents = env.documents()
	mailbox.Print = printMail
	if mailbox.Print == nil && env.Cfg != nil {
		mailbox.Print = env.browserEngine().PrintPDF
	}
	mailbox.Alerts = newAlerts(env)
	mailbox.Ingest = NewIngest(env)
	mailbox.Model = service.MailModelFor(st)
	if env.Cfg != nil {
		mailbox.Relays = env.Cfg.EmailOTPRelays
		mailbox.Forwarders = env.Cfg.EmailForwarders
		mailbox.PollEvery = env.Cfg.EmailPollEvery
		if env.Cfg.EmailLookbackDays > 0 {
			mailbox.Lookback = time.Duration(env.Cfg.EmailLookbackDays) * 24 * time.Hour
		}
	}
	return mailbox
}

// openMailbox builds the reader for a connection. Nil is the real one (Graph or
// IMAP); a test substitutes its own.
var openMailbox func(store.EmailConnection, string) (provider.Mailbox, error)

// printMail prints a mailed bill's statement. Nil is the browser in this
// process; a test substitutes its own.
var printMail func(page string) ([]byte, error)

// newMailboxAnswerer is the code answerer as the bridge takes it. The reader is
// built inside the call because NewBills builds this and NewMailbox calls
// NewBills.
func newMailboxAnswerer(env *Env) service.BillChallengeAnswerer {
	return func(
		ctx context.Context, spaceID store.SpaceID,
		connection store.BillConnection, challenge store.BillChallenge,
	) (string, string, bool) {
		return NewMailbox(env).AnswerChallenge(ctx, spaceID, connection, challenge)
	}
}

func emailConnection(r *http.Request, env *Env, sp auth.SpaceContext) (store.EmailConnection, error) {
	return fromPath(r, sp, "connection_id", "Email connection", env.DB.GetEmailConnection)
}

// checkMailbox refuses a connection that could never be read for lack of a
// fact its kind needs.
func checkMailbox(one *store.EmailConnection) error {
	if one.Address == "" {
		return errInvalid("missing", []string{"body", "address"}, "address is required")
	}
	switch one.Kind {
	case store.EmailKindGraph:
		if one.ClientID == "" {
			return errInvalid("missing", []string{"body", "client_id"},
				"an Office 365 mailbox needs the app registration's client id")
		}
		if one.Tenant == "" {
			return errInvalid("missing", []string{"body", "tenant"},
				"an Office 365 mailbox needs the tenant it is in")
		}
	case store.EmailKindIMAP:
		if one.Host == "" {
			return errInvalid("missing", []string{"body", "host"},
				"an IMAP mailbox needs the server to connect to")
		}
		if one.Username == "" {
			one.Username = one.Address
		}
		if one.Port == 0 {
			one.Port = 993
		}
	default:
		return errInvalid("invalid", []string{"body", "kind"}, "kind must be graph or imap")
	}
	return nil
}

func checkMailboxLabelFree(
	env *Env, r *http.Request, sp auth.SpaceContext, one store.EmailConnection, self uuid.UUID,
) error {
	rows, err := env.DB.ListEmailConnections(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Label == one.Label && row.ID != self {
			return errConflict("a mailbox labelled %q already exists", one.Label)
		}
	}
	return nil
}

// movedMailbox says the reader is now pointed somewhere else, which is what
// makes the cursor meaningless.
func movedMailbox(before, after store.EmailConnection) bool {
	return before.Folder != after.Folder || before.Host != after.Host ||
		before.Address != after.Address || before.Username != after.Username
}

func emailConnectionResponse(one store.EmailConnection) EmailConnectionResponse {
	out := EmailConnectionResponse{
		ID: one.ID, Label: one.Label, Kind: one.Kind, Address: one.Address,
		ClientID: one.ClientID, Tenant: one.Tenant, Host: one.Host,
		Username: one.Username, Folder: one.Folder, Enabled: one.Enabled,
		Connected: one.HasSecret, LastPolledAt: one.LastPolledAt,
		LastPollError: one.LastPollError, CreatedAt: one.CreatedAt,
	}
	if one.Port != 0 {
		port := one.Port
		out.Port = &port
	}
	return out
}

// --- The household's own mail rules ---------------------------------------------
//
// A rule is what somebody wrote about their own mail: which sender, which
// phrase, and the labels the figures sit behind. Nothing here is a credential,
// so none of it is denied to the in-process dispatch.

// MailRuleResponse is one rule.
type MailRuleResponse struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Enabled bool      `json:"enabled"`

	Sender          string `json:"sender"`
	SubjectContains string `json:"subject_contains"`
	BodyContains    string `json:"body_contains"`

	AmountLabel      string `json:"amount_label"`
	AmountPattern    string `json:"amount_pattern"`
	DateLabel        string `json:"date_label"`
	DatePattern      string `json:"date_pattern"`
	ReferenceLabel   string `json:"reference_label"`
	ReferencePattern string `json:"reference_pattern"`
	IssuedLabel      string `json:"issued_label"`
	IssuedPattern    string `json:"issued_pattern"`
	// MinimumLabel and MinimumPattern read a card or loan statement's minimum
	// payment; a bill rule's, and optional.
	MinimumLabel   string `json:"minimum_label"`
	MinimumPattern string `json:"minimum_pattern"`
	Payee          string `json:"payee"`
	PayeeLabel     string `json:"payee_label"`
	// NotesLabel and NotesEndLabel bound the stretch of the mail a transaction
	// rule keeps in the notes: the lines below the one holding NotesLabel, up to
	// the line holding NotesEndLabel or, with none, the first blank line.
	NotesLabel    string `json:"notes_label"`
	NotesEndLabel string `json:"notes_end_label"`

	Action     string     `json:"action"`
	AccountID  *uuid.UUID `json:"account_id"`
	CategoryID *uuid.UUID `json:"category_id"`
	Direction  string     `json:"direction"`
	// BillConnectionID and BillSubaccountID are a bill rule's: the provider,
	// and the billed account when the rule pins one.
	BillConnectionID *uuid.UUID `json:"bill_connection_id"`
	BillSubaccountID *uuid.UUID `json:"bill_subaccount_id"`

	PadIncome        bool       `json:"pad_income"`
	IncomeAccountID  *uuid.UUID `json:"income_account_id"`
	IncomeCategoryID *uuid.UUID `json:"income_category_id"`
	IncomePayee      string     `json:"income_payee"`

	SortOrder int       `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
}

// MailRuleDraft is a rule as it is written: every field optional, so the same
// body creates one and patches one.
type MailRuleDraft struct {
	Name    Opt[string] `json:"name"`
	Enabled Opt[bool]   `json:"enabled"`

	Sender          Opt[string] `json:"sender"`
	SubjectContains Opt[string] `json:"subject_contains"`
	BodyContains    Opt[string] `json:"body_contains"`

	AmountLabel      Opt[string] `json:"amount_label"`
	AmountPattern    Opt[string] `json:"amount_pattern"`
	DateLabel        Opt[string] `json:"date_label"`
	DatePattern      Opt[string] `json:"date_pattern"`
	ReferenceLabel   Opt[string] `json:"reference_label"`
	ReferencePattern Opt[string] `json:"reference_pattern"`
	IssuedLabel      Opt[string] `json:"issued_label"`
	IssuedPattern    Opt[string] `json:"issued_pattern"`
	MinimumLabel     Opt[string] `json:"minimum_label"`
	MinimumPattern   Opt[string] `json:"minimum_pattern"`
	Payee            Opt[string] `json:"payee"`
	PayeeLabel       Opt[string] `json:"payee_label"`
	NotesLabel       Opt[string] `json:"notes_label"`
	NotesEndLabel    Opt[string] `json:"notes_end_label"`

	Action     Opt[string]    `json:"action"`
	AccountID  Opt[uuid.UUID] `json:"account_id"`
	CategoryID Opt[uuid.UUID] `json:"category_id"`
	Direction  Opt[string]    `json:"direction"`

	BillConnectionID Opt[uuid.UUID] `json:"bill_connection_id"`
	BillSubaccountID Opt[uuid.UUID] `json:"bill_subaccount_id"`

	PadIncome        Opt[bool]      `json:"pad_income"`
	IncomeAccountID  Opt[uuid.UUID] `json:"income_account_id"`
	IncomeCategoryID Opt[uuid.UUID] `json:"income_category_id"`
	IncomePayee      Opt[string]    `json:"income_payee"`

	SortOrder Opt[int] `json:"sort_order"`
}

// MailRuleTryRequest is a rule and a pasted sample mail to try it against. The
// sample is never stored.
type MailRuleTryRequest struct {
	Rule   MailRuleDraft `json:"rule"`
	Sample struct {
		Sender  string `json:"sender"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	} `json:"sample"`
}

// MailRuleTryResponse is what the rule would have made of the sample. For a
// bill rule WouldFile is true, Date is the due date, IssuedOn the statement
// date, MinimumDue the minimum and Account the account number as read;
// WouldPost is empty.
type MailRuleTryResponse struct {
	Matched    bool                  `json:"matched"`
	Amount     *domain.Money         `json:"amount"`
	Date       *Date                 `json:"date"`
	Reference  string                `json:"reference"`
	Payee      string                `json:"payee"`
	Notes      string                `json:"notes"`
	Error      string                `json:"error"`
	WouldPost  []MailRulePostingJSON `json:"would_post"`
	WouldFile  bool                  `json:"would_file"`
	IssuedOn   *Date                 `json:"issued_on"`
	MinimumDue *domain.Money         `json:"minimum_due"`
	Account    string                `json:"account"`
}

// MailRulePostingJSON is one row the rule would write.
type MailRulePostingJSON struct {
	AccountID  *uuid.UUID   `json:"account_id"`
	Amount     domain.Money `json:"amount"`
	Payee      string       `json:"payee"`
	CategoryID *uuid.UUID   `json:"category_id"`
}

func listMailRules(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListMailRules(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	out := make([]MailRuleResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, mailRuleResponse(row))
	}
	return writeJSON(w, http.StatusOK, out)
}

func createMailRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body MailRuleDraft
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	rule := store.MailRule{
		Enabled: true, Action: store.MailRuleTransaction, Direction: store.MailRuleExpense,
	}
	if err := applyMailRule(body, &rule); err != nil {
		return err
	}
	if err := checkMailRule(env, r, sp, rule, uuid.Nil); err != nil {
		return err
	}
	if err := env.DB.CreateMailRule(r.Context(), sp.ID(), &rule); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, mailRuleResponse(rule))
}

func updateMailRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rule, err := mailRule(r, env, sp)
	if err != nil {
		return err
	}
	var body MailRuleDraft
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if err := applyMailRule(body, &rule); err != nil {
		return err
	}
	if err := checkMailRule(env, r, sp, rule, rule.ID); err != nil {
		return err
	}
	if err := env.DB.UpdateMailRule(r.Context(), sp.ID(), &rule); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, mailRuleResponse(rule))
}

func deleteMailRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rule, err := mailRule(r, env, sp)
	if err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteMailRule(r.Context(), sp.ID(), rule.ID), "Mail rule")
}

// tryMailRule answers what a rule would make of a pasted mail, and writes
// nothing at all — not the rule, and above all not the sample.
func tryMailRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body MailRuleTryRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	rule := store.MailRule{
		Enabled: true, Action: store.MailRuleTransaction, Direction: store.MailRuleExpense,
	}
	if err := applyMailRule(body.Rule, &rule); err != nil {
		return err
	}
	if err := checkMailRuleReadable(rule); err != nil {
		return err
	}
	tried := service.TryRule(rule, billmail.Message{
		Sender:  strings.ToLower(strings.TrimSpace(body.Sample.Sender)),
		Subject: body.Sample.Subject, Text: body.Sample.Text,
		ReceivedAt: env.now(),
	})

	out := MailRuleTryResponse{
		Matched: tried.Matched, Reference: tried.Reference, Payee: tried.Payee,
		Notes: tried.Notes, Error: tried.Error, WouldPost: []MailRulePostingJSON{},
		WouldFile: tried.WouldFile, Account: tried.Account,
	}
	if tried.HasIssued {
		issued := Date(tried.IssuedOn)
		out.IssuedOn = &issued
	}
	if tried.HasMinimum {
		minimum := tried.MinimumDue
		out.MinimumDue = &minimum
	}
	if tried.HasAmount {
		amount := tried.Amount
		out.Amount = &amount
	}
	if tried.HasDate {
		day := Date(tried.Date)
		out.Date = &day
	}
	for _, posting := range tried.WouldPost {
		out.WouldPost = append(out.WouldPost, MailRulePostingJSON{
			AccountID: pgconv.NullUUID(posting.AccountID), Amount: posting.Amount,
			Payee: posting.Payee, CategoryID: pgconv.NullUUID(posting.CategoryID),
		})
	}
	return writeJSON(w, http.StatusOK, out)
}

func mailRule(r *http.Request, env *Env, sp auth.SpaceContext) (store.MailRule, error) {
	return fromPath(r, sp, "rule_id", "Mail rule", env.DB.GetMailRule)
}

// applyMailRule writes the fields the body carried and leaves the rest.
func applyMailRule(body MailRuleDraft, rule *store.MailRule) error {
	if err := applyRequired("name", body.Name, &rule.Name); err != nil {
		return err
	}
	if err := applyRequired("enabled", body.Enabled, &rule.Enabled); err != nil {
		return err
	}
	for _, field := range []struct {
		opt Opt[string]
		dst *string
	}{
		{body.Sender, &rule.Sender},
		{body.SubjectContains, &rule.SubjectContains},
		{body.BodyContains, &rule.BodyContains},
		{body.AmountLabel, &rule.AmountLabel},
		{body.AmountPattern, &rule.AmountPattern},
		{body.DateLabel, &rule.DateLabel},
		{body.DatePattern, &rule.DatePattern},
		{body.ReferenceLabel, &rule.ReferenceLabel},
		{body.ReferencePattern, &rule.ReferencePattern},
		{body.IssuedLabel, &rule.IssuedLabel},
		{body.IssuedPattern, &rule.IssuedPattern},
		{body.MinimumLabel, &rule.MinimumLabel},
		{body.MinimumPattern, &rule.MinimumPattern},
		{body.Payee, &rule.Payee},
		{body.PayeeLabel, &rule.PayeeLabel},
		{body.NotesLabel, &rule.NotesLabel},
		{body.NotesEndLabel, &rule.NotesEndLabel},
		{body.IncomePayee, &rule.IncomePayee},
	} {
		applyNullable(field.opt, field.dst)
	}
	if err := applyRequired("action", body.Action, &rule.Action); err != nil {
		return err
	}
	if err := applyRequired("direction", body.Direction, &rule.Direction); err != nil {
		return err
	}
	if err := applyRequired("pad_income", body.PadIncome, &rule.PadIncome); err != nil {
		return err
	}
	if err := applyRequired("sort_order", body.SortOrder, &rule.SortOrder); err != nil {
		return err
	}
	applyNullable(body.AccountID, &rule.AccountID)
	applyNullable(body.CategoryID, &rule.CategoryID)
	applyNullable(body.IncomeAccountID, &rule.IncomeAccountID)
	applyNullable(body.IncomeCategoryID, &rule.IncomeCategoryID)
	applyNullable(body.BillConnectionID, &rule.BillConnectionID)
	applyNullable(body.BillSubaccountID, &rule.BillSubaccountID)
	return nil
}

// checkMailRule refuses a rule that could not run, and one whose accounts are
// another space's, which would fail every message silently.
func checkMailRule(
	env *Env, r *http.Request, sp auth.SpaceContext, rule store.MailRule, self uuid.UUID,
) error {
	if err := checkMailRuleShape(rule); err != nil {
		return err
	}
	for _, named := range []struct {
		field string
		id    uuid.UUID
	}{
		{"account_id", rule.AccountID}, {"income_account_id", rule.IncomeAccountID},
	} {
		if named.id == uuid.Nil {
			continue
		}
		account, err := env.DB.GetAccount(r.Context(), sp.ID(), named.id)
		if err != nil || account.IsDeleted {
			if err != nil && !isNotFound(err) {
				return err
			}
			return errInvalid("invalid", []string{"body", named.field},
				"account %s is not in this space", named.id)
		}
	}
	if err := checkMailRuleBill(env, r, sp, rule); err != nil {
		return err
	}
	for _, named := range []struct {
		field string
		id    uuid.UUID
	}{
		{"category_id", rule.CategoryID}, {"income_category_id", rule.IncomeCategoryID},
	} {
		if named.id == uuid.Nil {
			continue
		}
		if _, err := env.DB.GetCategory(r.Context(), sp.ID(), named.id); err != nil {
			if !isNotFound(err) {
				return err
			}
			return errInvalid("invalid", []string{"body", named.field},
				"category %s is not in this space", named.id)
		}
	}

	rows, err := env.DB.ListMailRules(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Name == rule.Name && row.ID != self {
			return errConflict("a mail rule called %q already exists", rule.Name)
		}
	}
	return nil
}

// checkMailRuleBill refuses a bill rule whose provider or billed account is
// not this household's, or whose billed account is at some other provider.
func checkMailRuleBill(env *Env, r *http.Request, sp auth.SpaceContext, rule store.MailRule) error {
	if rule.Action != store.MailRuleBill {
		return nil
	}
	if _, err := env.DB.GetBillConnection(r.Context(), sp.ID(), rule.BillConnectionID); err != nil {
		if !isNotFound(err) {
			return err
		}
		return errInvalid("invalid", []string{"body", "bill_connection_id"},
			"bill provider %s is not in this space", rule.BillConnectionID)
	}
	if rule.BillSubaccountID == uuid.Nil {
		return nil
	}
	subaccount, err := env.DB.GetBillSubaccount(r.Context(), sp.ID(), rule.BillSubaccountID)
	if err != nil {
		if !isNotFound(err) {
			return err
		}
		return errInvalid("invalid", []string{"body", "bill_subaccount_id"},
			"billed account %s is not in this space", rule.BillSubaccountID)
	}
	if subaccount.ConnectionID != rule.BillConnectionID {
		return errInvalid("invalid", []string{"body", "bill_subaccount_id"},
			"billed account %s is not one of that provider's", rule.BillSubaccountID)
	}
	return nil
}

// checkMailRuleShape is a rule that could not be saved: no name, nothing to
// post or file with, or an amount nothing could read.
func checkMailRuleShape(rule store.MailRule) error {
	if strings.TrimSpace(rule.Name) == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if err := checkMailRuleReadable(rule); err != nil {
		return err
	}
	if rule.Action == store.MailRuleBill {
		if rule.BillConnectionID == uuid.Nil {
			return errInvalid("missing", []string{"body", "bill_connection_id"},
				"a rule that files a bill needs the bill provider to file it on")
		}
		return nil
	}
	if rule.AccountID == uuid.Nil {
		return errInvalid("missing", []string{"body", "account_id"},
			"a rule needs the account to post to")
	}
	if rule.CategoryID == uuid.Nil {
		return errInvalid("missing", []string{"body", "category_id"},
			"a rule needs the category to post under")
	}
	if rule.PadIncome && rule.IncomeCategoryID == uuid.Nil {
		return errInvalid("missing", []string{"body", "income_category_id"},
			"a rule that pads income needs the category the income lands under")
	}
	return nil
}

// checkMailRuleReadable is the half a rule being tried must pass: whether it
// can read anything. The try box runs before the account and category are
// chosen.
func checkMailRuleReadable(rule store.MailRule) error {
	if rule.Action != store.MailRuleTransaction && rule.Action != store.MailRuleBill {
		return errInvalid("enum", []string{"body", "action"}, "action must be transaction or bill")
	}
	if rule.Direction != store.MailRuleExpense && rule.Direction != store.MailRuleIncome {
		return errInvalid("enum", []string{"body", "direction"},
			"direction must be expense or income")
	}
	if rule.AmountLabel == "" && rule.AmountPattern == "" {
		return errInvalid("missing", []string{"body", "amount_label"},
			"a rule needs an amount label or an amount pattern")
	}
	for _, named := range []struct {
		field, pattern string
	}{
		{"amount_pattern", rule.AmountPattern},
		{"date_pattern", rule.DatePattern},
		{"reference_pattern", rule.ReferencePattern},
		{"issued_pattern", rule.IssuedPattern},
		{"minimum_pattern", rule.MinimumPattern},
	} {
		if named.pattern == "" {
			continue
		}
		if err := billmail.CheckPattern(named.pattern); err != nil {
			return errInvalid("invalid", []string{"body", named.field}, "%s", err.Error())
		}
	}
	return nil
}

func mailRuleResponse(one store.MailRule) MailRuleResponse {
	return MailRuleResponse{
		ID: one.ID, Name: one.Name, Enabled: one.Enabled, Sender: one.Sender,
		SubjectContains: one.SubjectContains, BodyContains: one.BodyContains,
		AmountLabel: one.AmountLabel, AmountPattern: one.AmountPattern,
		DateLabel: one.DateLabel, DatePattern: one.DatePattern,
		ReferenceLabel: one.ReferenceLabel, ReferencePattern: one.ReferencePattern,
		IssuedLabel: one.IssuedLabel, IssuedPattern: one.IssuedPattern,
		MinimumLabel: one.MinimumLabel, MinimumPattern: one.MinimumPattern,
		Payee: one.Payee, PayeeLabel: one.PayeeLabel, NotesLabel: one.NotesLabel,
		NotesEndLabel: one.NotesEndLabel, Action: one.Action,
		AccountID: pgconv.NullUUID(one.AccountID), CategoryID: pgconv.NullUUID(one.CategoryID),
		BillConnectionID: pgconv.NullUUID(one.BillConnectionID),
		BillSubaccountID: pgconv.NullUUID(one.BillSubaccountID),
		Direction:        one.Direction, PadIncome: one.PadIncome,
		IncomeAccountID:  pgconv.NullUUID(one.IncomeAccountID),
		IncomeCategoryID: pgconv.NullUUID(one.IncomeCategoryID),
		IncomePayee:      one.IncomePayee, SortOrder: one.SortOrder, CreatedAt: one.CreatedAt,
	}
}
